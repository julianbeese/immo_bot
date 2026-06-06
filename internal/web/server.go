// Package web serves a minimal local dashboard: listing/status overview,
// settings (contact mode, quiet hours) and search-profile management. It shares
// the repository and control.Controller with the rest of the bot, so changes
// here take effect immediately in Telegram/WhatsApp too.
//
// Intended for localhost only (no auth/TLS) — see config.WebConfig.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/control"
	"github.com/julianbeese/immo_bot/internal/domain"
	"github.com/julianbeese/immo_bot/internal/email"
	"github.com/julianbeese/immo_bot/internal/messenger"
	"github.com/julianbeese/immo_bot/internal/repository/sqlite"
	"github.com/julianbeese/immo_bot/internal/secrets"
)

//go:embed all:frontend/out
var frontendFS embed.FS

// StatsFunc returns aggregate listing counts (total, contacted, notified).
type StatsFunc func(ctx context.Context) (total, contacted, notified int)

// CookieSetter hot-reloads the IS24 cookie (scheduler-level: applies to the
// client + persists to meta). Allowed to be nil for tests; the dashboard then
// rejects the POST instead of panicking.
type CookieSetter func(ctx context.Context, cookie string) error

// SettingsNotifier receives a formatted audit message whenever the dashboard
// changes a setting. Implemented by the Telegram notifier so the user gets a
// confirmation in the same chat where listings arrive. Nil-safe: New accepts
// a nil notifier (tests don't wire one).
type SettingsNotifier interface {
	SendRawMessage(ctx context.Context, text string) error
}

// ApprovalHandler resolves approval-card decisions from the dashboard queue
// view. Implemented by the scheduler. Nil-safe: when nil, the queue endpoints
// return read-only data and ✅/❌ POSTs reject with 503.
type ApprovalHandler interface {
	OnApprove(ctx context.Context, sentMessageID int64) error
	OnReject(ctx context.Context, sentMessageID int64) error
}

// InboxScanner triggers an on-demand IMAP poll. Implemented by the email
// monitor. Nil-safe: when nil, POST /api/inbox/scan returns 503.
type InboxScanner interface {
	Poll(ctx context.Context) (email.ScanResult, error)
}

// Server is the dashboard HTTP server.
type Server struct {
	repo            *sqlite.Repository
	ctrl            *control.Controller
	cfg             *config.Config
	stats           StatsFunc
	setCookie       CookieSetter
	settingsNotify  SettingsNotifier // nil = no audit messages
	approval        ApprovalHandler  // nil = queue read-only
	inboxScanner    InboxScanner     // nil = scan endpoint returns 503
	// cookieEnc decrypts the IS24 cookie meta row so handleGetCookie can report
	// the real cookie length, not the ciphertext length. nil-safe.
	cookieEnc *secrets.Encrypter
	logger    *slog.Logger
}

// SetCookieEncrypter installs the AES-GCM encrypter used to read the IS24
// cookie meta row. Pass nil to operate against legacy plaintext rows.
func (s *Server) SetCookieEncrypter(e *secrets.Encrypter) { s.cookieEnc = e }

// SetInboxScanner wires the on-demand mailbox-poll trigger used by the
// "Jetzt scannen"-Button in the Posteingang view. Pass nil to keep the
// endpoint disabled (the periodic scheduler poll still runs).
func (s *Server) SetInboxScanner(scan InboxScanner) { s.inboxScanner = scan }

// New creates a dashboard server. setCookie, settingsNotify and approval may
// be nil (tests). When settingsNotify is set, /api/settings changes fan out a
// diff message to that channel so the user sees what was changed.
func New(repo *sqlite.Repository, ctrl *control.Controller, cfg *config.Config, stats StatsFunc, setCookie CookieSetter, settingsNotify SettingsNotifier, approval ApprovalHandler, logger *slog.Logger) *Server {
	return &Server{repo: repo, ctrl: ctrl, cfg: cfg, stats: stats, setCookie: setCookie, settingsNotify: settingsNotify, approval: approval, logger: logger}
}

// Handler returns the dashboard's HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	distFS, err := fs.Sub(frontendFS, "frontend/out")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(distFS))

	mux.Handle("/", fileServer)
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("GET /api/listings", s.handleListings)
	mux.HandleFunc("GET /api/listings/{id}/messages", s.handleListingMessages)
	mux.HandleFunc("GET /api/inbox", s.handleInbox)
	mux.HandleFunc("POST /api/inbox/scan", s.handleInboxScan)
	mux.HandleFunc("GET /api/profiles", s.handleProfiles)
	mux.HandleFunc("GET /api/campaigns", s.handleCampaigns)
	mux.HandleFunc("POST /api/campaigns/{name}", s.handleSaveCampaign)
	mux.HandleFunc("POST /api/settings", s.handleSettings)
	mux.HandleFunc("GET /api/cookie", s.handleGetCookie)
	// Cookie update is the only endpoint that needs CORS: a bookmarklet on
	// immobilienscout24.de POSTs document.cookie here for one-click refresh.
	// All other API routes stay same-origin.
	mux.HandleFunc("POST /api/cookie", corsCookie(s.handleSetCookie))
	mux.HandleFunc("OPTIONS /api/cookie", corsCookie(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /api/email", s.handleGetEmail)
	mux.HandleFunc("POST /api/email", s.handleSetEmail)
	mux.HandleFunc("POST /api/listings/{id}/skip", s.handleSkipListing)
	mux.HandleFunc("POST /api/listings/skip", s.handleBulkSkipListings)
	mux.HandleFunc("POST /api/listings/{id}/unreject", s.handleUnrejectListing)
	mux.HandleFunc("GET /api/queue", s.handleQueue)
	mux.HandleFunc("POST /api/queue/approve", s.handleQueueApprove)
	mux.HandleFunc("POST /api/queue/reject", s.handleQueueReject)
	mux.HandleFunc("POST /api/profiles", s.handleAddProfile)
	mux.HandleFunc("POST /api/profiles/{id}/active", s.handleSetProfileActive)
	mux.HandleFunc("DELETE /api/profiles/{id}", s.handleDeleteProfile)
	return mux
}

// Start runs the server until ctx is cancelled.
func (s *Server) Start(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	s.logger.Info("web dashboard listening", "addr", addr, "url", "http://"+addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	total, contacted, notified := 0, 0, 0
	if s.stats != nil {
		total, contacted, notified = s.stats(r.Context())
	}
	lastPoll, _ := s.repo.GetMeta(r.Context(), sqlite.MetaLastPollOK)

	qStart, qEnd := s.ctrl.QuietHoursWindow()
	resp := map[string]any{
		"contact_mode":             modeString(s.ctrl.GetContactMode()),
		"contact_label":            s.ctrl.ContactModeLabel(),
		"quiet_hours":              derefBool(s.ctrl.IsQuietHoursEnabled()),
		"quiet_hours_start":        qStart,
		"quiet_hours_end":          qEnd,
		"last_poll":                lastPoll,
		"default_campaign":         s.cfg.DefaultCampaign,
		"campaigns":                s.campaignNames(),
		"poll_interval_seconds":    int(s.ctrl.GetPollInterval().Seconds()),
		"contact_type_delay_ms":    s.ctrl.GetContactTypeDelay().Milliseconds(),
		"contact_action_delay_ms":  s.ctrl.GetContactActionDelay().Milliseconds(),
		"exclude_furnished":        s.ctrl.IsExcludeFurnishedEnabled(),
		"timing_ranges": map[string]map[string]int64{
			"poll_interval_seconds":   {"min": int64(control.MinPollInterval.Seconds()), "max": int64(control.MaxPollInterval.Seconds())},
			"contact_type_delay_ms":   {"min": control.MinContactTypeDelay.Milliseconds(), "max": control.MaxContactTypeDelay.Milliseconds()},
			"contact_action_delay_ms": {"min": control.MinContactActionDelay.Milliseconds(), "max": control.MaxContactActionDelay.Milliseconds()},
		},
		"stats": map[string]int{
			"total":     total,
			"notified":  notified,
			"contacted": contacted,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListings(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	listings, err := s.repo.ListRecentListings(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cat := s.profileCategories(r.Context())

	type dto struct {
		*domain.Listing
		Campaign string `json:"campaign"`
	}
	out := make([]dto, 0, len(listings))
	for i := range listings {
		camp := cat[listings[i].SearchProfileID]
		if camp == "" {
			camp = s.cfg.DefaultCampaign
		}
		out = append(out, dto{Listing: &listings[i], Campaign: camp})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleListingMessages returns the full sent_message history for one listing
// (oldest first). Powers the dashboard's detail drawer message timeline.
func (s *Server) handleListingMessages(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid listing id"))
		return
	}
	msgs, err := s.repo.GetSentMessagesByListing(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if msgs == nil {
		msgs = []domain.SentMessage{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

// queueListingDTO is a listing decorated with its resolved campaign label —
// keeps the queue tab consistent with the listings tab without a join client-
// side.
type queueListingDTO struct {
	*domain.Listing
	Campaign string `json:"campaign"`
}

// queuePendingDTO bundles the in-flight approval card so the dashboard can
// show the message body next to the ✅/❌ buttons without a second fetch.
type queuePendingDTO struct {
	SentMessageID int64           `json:"sent_message_id"`
	Message       string          `json:"message"`
	CreatedAt     time.Time       `json:"created_at"`
	Listing       queueListingDTO `json:"listing"`
}

// handleQueue returns the approval queue: the in-flight pending approval (if
// any) plus the eligible listings the scheduler would propose next, in the
// same order. Powers the dashboard queue view.
func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cat := s.profileCategories(ctx)
	decorate := func(l *domain.Listing) queueListingDTO {
		camp := cat[l.SearchProfileID]
		if camp == "" {
			camp = s.cfg.DefaultCampaign
		}
		return queueListingDTO{Listing: l, Campaign: camp}
	}

	resp := map[string]any{
		"pending": nil,
		"next":    []queueListingDTO{},
	}

	pending, err := s.repo.GetPendingApprovalMessage(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if pending != nil {
		listing, err := s.repo.GetListingByID(ctx, pending.ListingID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if listing != nil {
			resp["pending"] = queuePendingDTO{
				SentMessageID: pending.ID,
				Message:       pending.Message,
				CreatedAt:     pending.CreatedAt,
				Listing:       decorate(listing),
			}
		}
	}

	next, err := s.repo.GetApprovalQueue(ctx, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]queueListingDTO, 0, len(next))
	for i := range next {
		out = append(out, decorate(&next[i]))
	}
	resp["next"] = out

	writeJSON(w, http.StatusOK, resp)
}

// handleQueueApprove resolves the pending approval via the scheduler's
// OnApprove path — same code path as the Telegram ✅ button.
func (s *Server) handleQueueApprove(w http.ResponseWriter, r *http.Request) {
	s.handleQueueDecision(w, r, true)
}

// handleQueueReject resolves the pending approval via the scheduler's
// OnReject path — same code path as the Telegram ❌ button.
func (s *Server) handleQueueReject(w http.ResponseWriter, r *http.Request) {
	s.handleQueueDecision(w, r, false)
}

func (s *Server) handleQueueDecision(w http.ResponseWriter, r *http.Request, approve bool) {
	if s.approval == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("approval handler not configured"))
		return
	}
	var body struct {
		SentMessageID int64 `json:"sent_message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if body.SentMessageID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("sent_message_id required"))
		return
	}
	var err error
	if approve {
		err = s.approval.OnApprove(r.Context(), body.SentMessageID)
	} else {
		err = s.approval.OnReject(r.Context(), body.SentMessageID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleInbox returns recent IS24-related emails. ?landlord=1 limits to genuine
// provider replies; otherwise all classified mails are returned.
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	landlordOnly := r.URL.Query().Get("landlord") == "1"
	msgs, err := s.repo.ListInboxMessages(r.Context(), limit, landlordOnly)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if msgs == nil {
		msgs = []domain.InboxMessage{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

// handleInboxScan triggers an on-demand IMAP poll. Bounded by a 30s context
// timeout so the dashboard never hangs on a slow remote — the periodic
// scheduler poll continues to run in parallel either way. The response carries
// the structured ScanResult so the UI can render per-mail counts and errors
// instead of a bare ok/error.
func (s *Server) handleInboxScan(w http.ResponseWriter, r *http.Request) {
	if s.inboxScanner == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("inbox monitor not configured (EMAIL_ENABLED?)"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.inboxScanner.Poll(ctx)
	if err != nil {
		s.logger.Warn("manual inbox scan failed", "error", err)
		// Include whatever stats we did collect (e.g. duration) alongside the
		// error so the UI can still tell the user how long it tried.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":  err.Error(),
			"result": res,
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.repo.ListAllSearchProfiles(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

// campaignDTO is the editable message config for one campaign, as shown in the
// dashboard. AIPrompt/Template hold the effective values (override if set,
// else the config/built-in default); the *Overridden flags say which.
type campaignDTO struct {
	Name             string `json:"name"`
	AIPrompt         string `json:"ai_prompt"`
	AIPromptOverride bool   `json:"ai_prompt_overridden"`
	Template         string `json:"template"`
	TemplateOverride bool   `json:"template_overridden"`
}

// effectiveCampaign resolves the displayed AI prompt + template for a campaign:
// dashboard override first, then config.yaml, then the built-in default.
func (s *Server) effectiveCampaign(ctx context.Context, name string) campaignDTO {
	dto := campaignDTO{Name: name}
	cfgCamp := s.cfg.ResolveCampaign(name)

	if v, err := s.repo.GetMeta(ctx, sqlite.CampaignPromptKey(name)); err == nil && v != "" {
		dto.AIPrompt, dto.AIPromptOverride = v, true
	} else if cfgCamp.AIPrompt != "" {
		dto.AIPrompt = cfgCamp.AIPrompt
	} else {
		dto.AIPrompt = messenger.DefaultSystemPrompt()
	}

	if v, err := s.repo.GetMeta(ctx, sqlite.CampaignTemplateKey(name)); err == nil && v != "" {
		dto.Template, dto.TemplateOverride = v, true
	} else if cfgCamp.MessageTemplatePath != "" {
		if b, err := os.ReadFile(cfgCamp.MessageTemplatePath); err == nil {
			dto.Template = string(b)
		} else {
			dto.Template = messenger.DefaultTemplate()
		}
	} else {
		dto.Template = messenger.DefaultTemplate()
	}
	return dto
}

func (s *Server) handleCampaigns(w http.ResponseWriter, r *http.Request) {
	names := s.campaignNames()
	out := make([]campaignDTO, 0, len(names))
	for _, n := range names {
		out = append(out, s.effectiveCampaign(r.Context(), n))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSaveCampaign(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.cfg.HasCampaign(name) {
		writeErr(w, http.StatusBadRequest, errors.New("unknown campaign: "+name))
		return
	}
	var body struct {
		AIPrompt *string `json:"ai_prompt"`
		Template *string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// An empty value clears the override (scheduler then falls back to config).
	if body.AIPrompt != nil {
		if err := s.repo.SetMeta(r.Context(), sqlite.CampaignPromptKey(name), strings.TrimSpace(*body.AIPrompt)); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if body.Template != nil {
		if err := s.repo.SetMeta(r.Context(), sqlite.CampaignTemplateKey(name), strings.TrimSpace(*body.Template)); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.effectiveCampaign(r.Context(), name))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ContactMode          *string `json:"contact_mode"`
		QuietHours           *bool   `json:"quiet_hours"`
		QuietHoursStart      *string `json:"quiet_hours_start"`
		QuietHoursEnd        *string `json:"quiet_hours_end"`
		PollIntervalSeconds  *int    `json:"poll_interval_seconds"`
		ContactTypeDelayMs   *int    `json:"contact_type_delay_ms"`
		ContactActionDelayMs *int    `json:"contact_action_delay_ms"`
		ExcludeFurnished     *bool   `json:"exclude_furnished"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Each entry: a human-readable "before → after" line, captured only when
	// the field changes value. Empty when nothing changed (no-op POST).
	var diffs []string
	addDiff := func(label, before, after string) {
		if before == after {
			return
		}
		diffs = append(diffs, fmt.Sprintf("• *%s:* %s → %s", label, before, after))
	}

	if body.ContactMode != nil {
		mode, ok := parseMode(*body.ContactMode)
		if !ok {
			writeErr(w, http.StatusBadRequest, errors.New("contact_mode must be off, test, approve or on"))
			return
		}
		before := s.ctrl.GetContactMode().Label()
		s.ctrl.SetContactMode(mode)
		addDiff("Kontakt-Modus", before, mode.Label())
	}
	if body.QuietHours != nil {
		before := quietHoursLabel(s.ctrl.IsQuietHoursEnabled())
		s.ctrl.SetQuietHours(*body.QuietHours)
		addDiff("Ruhezeiten aktiv", before, quietHoursLabel(s.ctrl.IsQuietHoursEnabled()))
	}
	if body.PollIntervalSeconds != nil {
		if *body.PollIntervalSeconds <= 0 {
			writeErr(w, http.StatusBadRequest, errors.New("poll_interval_seconds must be positive"))
			return
		}
		before := s.ctrl.GetPollInterval()
		s.ctrl.SetPollInterval(time.Duration(*body.PollIntervalSeconds) * time.Second)
		addDiff("Poll-Intervall", before.String(), s.ctrl.GetPollInterval().String())
	}
	if body.ContactTypeDelayMs != nil {
		if *body.ContactTypeDelayMs <= 0 {
			writeErr(w, http.StatusBadRequest, errors.New("contact_type_delay_ms must be positive"))
			return
		}
		before := s.ctrl.GetContactTypeDelay()
		s.ctrl.SetContactTypeDelay(time.Duration(*body.ContactTypeDelayMs) * time.Millisecond)
		addDiff("Tipp-Verzögerung", before.String(), s.ctrl.GetContactTypeDelay().String())
	}
	if body.ContactActionDelayMs != nil {
		if *body.ContactActionDelayMs <= 0 {
			writeErr(w, http.StatusBadRequest, errors.New("contact_action_delay_ms must be positive"))
			return
		}
		before := s.ctrl.GetContactActionDelay()
		s.ctrl.SetContactActionDelay(time.Duration(*body.ContactActionDelayMs) * time.Millisecond)
		addDiff("Aktions-Verzögerung", before.String(), s.ctrl.GetContactActionDelay().String())
	}
	if body.ExcludeFurnished != nil {
		before := onOff(s.ctrl.IsExcludeFurnishedEnabled())
		s.ctrl.SetExcludeFurnished(*body.ExcludeFurnished)
		addDiff("Möbliert ausschließen", before, onOff(s.ctrl.IsExcludeFurnishedEnabled()))
	}
	// Quiet-hours window is set as a pair. Allow updating one side by passing
	// the existing value for the other (frontend does this automatically).
	if body.QuietHoursStart != nil || body.QuietHoursEnd != nil {
		curStart, curEnd := s.ctrl.QuietHoursWindow()
		newStart := curStart
		newEnd := curEnd
		if body.QuietHoursStart != nil {
			newStart = *body.QuietHoursStart
		}
		if body.QuietHoursEnd != nil {
			newEnd = *body.QuietHoursEnd
		}
		if err := s.ctrl.SetQuietHoursWindow(newStart, newEnd); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		gotStart, gotEnd := s.ctrl.QuietHoursWindow()
		addDiff("Ruhezeit-Fenster",
			fmt.Sprintf("%s–%s", curStart, curEnd),
			fmt.Sprintf("%s–%s", gotStart, gotEnd))
	}

	s.notifySettingsChange(diffs)
	s.handleOverview(w, r)
}

// notifySettingsChange dispatches a summary of the diffs to the configured
// channel (Telegram) so the user sees what was modified without having to
// refresh the dashboard. Decouples from the HTTP request: a slow Telegram send
// must not stall the response, and the message must survive past r.Context()
// expiring. No-op when the notifier is unset or nothing changed.
func (s *Server) notifySettingsChange(diffs []string) {
	if s.settingsNotify == nil || len(diffs) == 0 {
		return
	}
	msg := "⚙️ *Einstellungen geändert*\n\n" + strings.Join(diffs, "\n")
	go func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.settingsNotify.SendRawMessage(sendCtx, msg); err != nil {
			s.logger.Warn("settings audit notification failed", "error", err)
		}
	}()
}

// onOff renders a bool as "An"/"Aus" for the audit message.
func onOff(b bool) string {
	if b {
		return "An"
	}
	return "Aus"
}

// quietHoursLabel handles the controller's nullable quiet-hours override.
// nil = no explicit override (falls back to config); set = on/off as written.
func quietHoursLabel(b *bool) string {
	if b == nil {
		return "—"
	}
	return onOff(*b)
}

// handleGetCookie reports cookie presence without ever leaking the cookie
// itself. Source = "meta" means a hot-reloaded override is active; "env" means
// the original IS24_COOKIE env var (or empty config value) is in effect. The
// reported length is the cookie's real length — for encrypted meta rows it is
// derived from the decrypted value so the UI doesn't show ciphertext length.
func (s *Server) handleGetCookie(w http.ResponseWriter, r *http.Request) {
	v, _ := s.repo.GetMeta(r.Context(), sqlite.MetaIS24Cookie)
	resp := map[string]any{
		"present": v != "" || strings.TrimSpace(s.cfg.IS24.Cookie) != "",
		"length":  0,
		"source":  "none",
	}
	switch {
	case v != "":
		resp["length"] = cookieLength(v, s.cookieEnc)
		resp["source"] = "meta"
	case strings.TrimSpace(s.cfg.IS24.Cookie) != "":
		resp["length"] = len(s.cfg.IS24.Cookie)
		resp["source"] = "env"
	}
	writeJSON(w, http.StatusOK, resp)
}

// cookieLength returns the length of the actual cookie value. For encrypted
// envelopes it decrypts first so the dashboard never reports ciphertext
// length. On decrypt error it returns 0 — the operator sees source="meta"
// with a zero length, which surfaces the key mismatch.
func cookieLength(stored string, enc *secrets.Encrypter) int {
	if enc != nil && secrets.IsEncrypted(stored) {
		pt, err := enc.Decrypt(stored)
		if err != nil {
			return 0
		}
		return len(pt)
	}
	return len(stored)
}

// allowedCookieOrigins is the closed list of origins permitted to POST a
// fresh IS24 cookie via the bookmarklet. Keeping this short means the
// dashboard's only CORS-enabled endpoint can't be abused by random sites the
// user happens to visit. Extend deliberately, not via env.
var allowedCookieOrigins = map[string]struct{}{
	"https://www.immobilienscout24.de": {},
	"https://immobilienscout24.de":     {},
}

// corsCookie wraps a handler so it accepts the bookmarklet's cross-origin POST
// from immobilienscout24.de. Other origins receive no CORS headers, so the
// browser blocks the call before the handler ever runs.
func corsCookie(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if _, ok := allowedCookieOrigins[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		next(w, r)
	}
}

// handleSetCookie hot-reloads the IS24 cookie via the scheduler-supplied
// CookieSetter (which applies + persists in one step). Minimal length check
// guards against pasting a fragment by accident.
func (s *Server) handleSetCookie(w http.ResponseWriter, r *http.Request) {
	if s.setCookie == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("cookie setter not wired"))
		return
	}
	var body struct {
		Cookie string `json:"cookie"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	v := strings.TrimSpace(body.Cookie)
	if len(v) < 50 {
		writeErr(w, http.StatusBadRequest, errors.New("cookie too short (expected full Cookie header, ~hundreds of chars)"))
		return
	}
	if err := s.setCookie(r.Context(), v); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"present": true, "length": len(v), "source": "meta"})
}

// emailDTO is the IMAP inbox monitor config exposed to the dashboard. The
// password itself is never returned — only PasswordSet and PasswordSource — so
// a refresh of the settings panel can't leak it. lookback is exposed as integer
// hours since the UI handles whole hours.
type emailDTO struct {
	Enabled        bool   `json:"enabled"`
	IMAPHost       string `json:"imap_host"`
	Username       string `json:"username"`
	PasswordSet    bool   `json:"password_set"`
	PasswordSource string `json:"password_source"` // "meta", "env", or "none"
	Mailbox        string `json:"mailbox"`
	LookbackHours  int    `json:"lookback_hours"`
	Senders        string `json:"senders"` // comma-separated; empty → use built-in IS24 defaults
	// MetaOverride is true when any field has been customized via the dashboard
	// (vs purely env/yaml config). Helps users see whether a stale meta value
	// is shadowing their .env.
	MetaOverride bool `json:"meta_override"`
	// RestartRequired flags that the running monitor still uses the previous
	// values; the user must restart the container to pick up changes.
	RestartRequired bool `json:"restart_required"`
}

// currentEmailDTO assembles the effective email config (env/yaml + meta merged)
// for the dashboard. Mirrors applyEmailMetaOverrides in main.go so users see
// the values the next restart will use.
func (s *Server) currentEmailDTO(ctx context.Context, restartRequired bool) emailDTO {
	enabled := s.cfg.Email.Enabled
	host := s.cfg.Email.IMAPHost
	user := s.cfg.Email.Username
	pwd := s.cfg.Email.Password
	mailbox := s.cfg.Email.Mailbox
	lookback := s.cfg.Email.Lookback
	senders := append([]string(nil), s.cfg.Email.Senders...)
	override := false

	if v, _ := s.repo.GetMeta(ctx, sqlite.MetaEmailEnabled); v != "" {
		override = true
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			enabled = true
		case "0", "false", "no", "off":
			enabled = false
		}
	}
	if v, _ := s.repo.GetMeta(ctx, sqlite.MetaEmailIMAPHost); v != "" {
		host, override = v, true
	}
	if v, _ := s.repo.GetMeta(ctx, sqlite.MetaEmailUsername); v != "" {
		user, override = v, true
	}
	if v, _ := s.repo.GetMeta(ctx, sqlite.MetaEmailMailbox); v != "" {
		mailbox, override = v, true
	}
	if v, _ := s.repo.GetMeta(ctx, sqlite.MetaEmailLookbackHours); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			lookback, override = time.Duration(n)*time.Hour, true
		}
	}
	if v, _ := s.repo.GetMeta(ctx, sqlite.MetaEmailSenders); v != "" {
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			senders, override = out, true
		}
	}

	metaPwd, _ := s.repo.GetMeta(ctx, sqlite.MetaEmailPassword)
	if metaPwd != "" {
		override = true
	}
	passwordSet := metaPwd != "" || strings.TrimSpace(pwd) != ""
	passwordSource := "none"
	switch {
	case metaPwd != "":
		passwordSource = "meta"
	case strings.TrimSpace(pwd) != "":
		passwordSource = "env"
	}

	return emailDTO{
		Enabled:         enabled,
		IMAPHost:        host,
		Username:        user,
		PasswordSet:     passwordSet,
		PasswordSource:  passwordSource,
		Mailbox:         mailbox,
		LookbackHours:   int(lookback / time.Hour),
		Senders:         strings.Join(senders, ", "),
		MetaOverride:    override,
		RestartRequired: restartRequired,
	}
}

func (s *Server) handleGetEmail(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentEmailDTO(r.Context(), false))
}

// handleSetEmail persists email-monitor settings to the meta table. Nil/missing
// fields are left untouched; empty strings clear an override (falls back to
// env/yaml). The IMAP password is AES-GCM encrypted before it is written to
// meta (same key as the IS24 cookie). Changes take effect after the next
// restart — the running monitor goroutine isn't hot-swapped.
func (s *Server) handleSetEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled       *bool   `json:"enabled"`
		IMAPHost      *string `json:"imap_host"`
		Username      *string `json:"username"`
		Password      *string `json:"password"` // encrypted at rest when set; omitted = unchanged
		Mailbox       *string `json:"mailbox"`
		LookbackHours *int    `json:"lookback_hours"`
		Senders       *string `json:"senders"` // comma-separated
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	ctx := r.Context()
	set := func(key, val string) error { return s.repo.SetMeta(ctx, key, val) }

	if body.Enabled != nil {
		v := "0"
		if *body.Enabled {
			v = "1"
		}
		if err := set(sqlite.MetaEmailEnabled, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if body.IMAPHost != nil {
		if err := set(sqlite.MetaEmailIMAPHost, strings.TrimSpace(*body.IMAPHost)); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if body.Username != nil {
		if err := set(sqlite.MetaEmailUsername, strings.TrimSpace(*body.Username)); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if body.Password != nil {
		pwd := strings.TrimSpace(*body.Password)
		if pwd == "" {
			if err := set(sqlite.MetaEmailPassword, ""); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
		} else {
			if s.cookieEnc == nil {
				writeErr(w, http.StatusServiceUnavailable, errors.New("secrets encrypter not configured"))
				return
			}
			stored, err := s.cookieEnc.Encrypt(pwd)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			if err := set(sqlite.MetaEmailPassword, stored); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
		}
	}
	if body.Mailbox != nil {
		if err := set(sqlite.MetaEmailMailbox, strings.TrimSpace(*body.Mailbox)); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if body.LookbackHours != nil {
		if *body.LookbackHours < 0 {
			writeErr(w, http.StatusBadRequest, errors.New("lookback_hours must be >= 0"))
			return
		}
		v := ""
		if *body.LookbackHours > 0 {
			v = strconv.Itoa(*body.LookbackHours)
		}
		if err := set(sqlite.MetaEmailLookbackHours, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if body.Senders != nil {
		// Normalize: split, trim, drop empties, rejoin so the value we store is
		// what the bot will actually use.
		var out []string
		for _, p := range strings.Split(*body.Senders, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		if err := set(sqlite.MetaEmailSenders, strings.Join(out, ", ")); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, s.currentEmailDTO(ctx, true))
}

// handleSkipListing toggles a listing's manual ignore flag so the user can
// exclude irrelevant finds from auto-contact without deleting them.
func (s *Server) handleSkipListing(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var body struct {
		Skipped bool `json:"skipped"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.repo.SetListingSkipped(r.Context(), id, body.Skipped); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "skipped": body.Skipped})
}

// handleUnrejectListing clears prior rejections on a listing so it becomes
// eligible for the approval queue again. Symmetric to the Telegram ❌ — the
// user took it back from the dashboard.
func (s *Server) handleUnrejectListing(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	removed, err := s.repo.ClearListingRejection(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed})
}

// handleBulkSkipListings applies the skip flag to many listings at once. Used
// by the dashboard's multi-select toolbar so users can ignore a batch of finds
// without N round-trips.
func (s *Server) handleBulkSkipListings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs     []int64 `json:"ids"`
		Skipped bool    `json:"skipped"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("ids must be a non-empty array"))
		return
	}
	// Cap the batch to keep the IN(?,?,...) parameter list well under
	// sqlite's default SQLITE_MAX_VARIABLE_NUMBER (999) and to prevent a
	// runaway client from hammering one request.
	const maxBatch = 500
	if len(body.IDs) > maxBatch {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("too many ids (%d > %d)", len(body.IDs), maxBatch))
		return
	}
	updated, err := s.repo.SetListingsSkipped(r.Context(), body.IDs, body.Skipped)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"updated": updated,
		"skipped": body.Skipped,
	})
}

func (s *Server) handleAddProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Category string `json:"category"`
		URL      string `json:"url"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	if !strings.HasPrefix(body.URL, "http://") && !strings.HasPrefix(body.URL, "https://") {
		writeErr(w, http.StatusBadRequest, errors.New("url must be an http(s) IS24 search URL"))
		return
	}
	if body.Category != "" && !s.cfg.HasCampaign(body.Category) {
		writeErr(w, http.StatusBadRequest, errors.New("unknown campaign: "+body.Category))
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = profileNameFromURL(body.URL)
	}
	sp := &domain.SearchProfile{Name: name, SearchURL: body.URL, Category: body.Category, Active: true}
	if err := s.repo.CreateSearchProfile(r.Context(), sp); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

func (s *Server) handleSetProfileActive(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.repo.SetSearchProfileActive(r.Context(), id, body.Active); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	if err := s.repo.DeleteSearchProfile(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// profileCategories maps search profile ID → category for listing enrichment.
func (s *Server) profileCategories(ctx context.Context) map[int64]string {
	m := map[int64]string{}
	profiles, err := s.repo.ListAllSearchProfiles(ctx)
	if err != nil {
		return m
	}
	for _, p := range profiles {
		m[p.ID] = p.Category
	}
	return m
}

func (s *Server) campaignNames() []string {
	names := make([]string, 0, len(s.cfg.Campaigns))
	for n := range s.cfg.Campaigns {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// --- helpers ---

func modeString(m control.ContactMode) string {
	switch m {
	case control.ContactModeOff:
		return "off"
	case control.ContactModeTest:
		return "test"
	case control.ContactModeApprove:
		return "approve"
	case control.ContactModeOn:
		return "on"
	}
	return "test"
}

func parseMode(s string) (control.ContactMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off":
		return control.ContactModeOff, true
	case "test":
		return control.ContactModeTest, true
	case "approve":
		return control.ContactModeApprove, true
	case "on":
		return control.ContactModeOn, true
	}
	return 0, false
}

func derefBool(b *bool) bool { return b != nil && *b }

func profileNameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "IS24-Suche"
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if strings.EqualFold(p, "de") && i+2 < len(parts) && parts[i+2] != "" {
			city := parts[i+2]
			return strings.ToUpper(city[:1]) + city[1:]
		}
	}
	return "IS24-Suche"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
