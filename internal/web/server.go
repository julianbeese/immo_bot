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
	"github.com/julianbeese/immo_bot/internal/messenger"
	"github.com/julianbeese/immo_bot/internal/repository/sqlite"
)

//go:embed all:frontend/out
var frontendFS embed.FS

// StatsFunc returns aggregate listing counts (total, contacted, notified).
type StatsFunc func(ctx context.Context) (total, contacted, notified int)

// CookieSetter hot-reloads the IS24 cookie (scheduler-level: applies to the
// client + persists to meta). Allowed to be nil for tests; the dashboard then
// rejects the POST instead of panicking.
type CookieSetter func(ctx context.Context, cookie string) error

// Server is the dashboard HTTP server.
type Server struct {
	repo      *sqlite.Repository
	ctrl      *control.Controller
	cfg       *config.Config
	stats     StatsFunc
	setCookie CookieSetter
	logger    *slog.Logger
}

// New creates a dashboard server. setCookie may be nil (tests).
func New(repo *sqlite.Repository, ctrl *control.Controller, cfg *config.Config, stats StatsFunc, setCookie CookieSetter, logger *slog.Logger) *Server {
	return &Server{repo: repo, ctrl: ctrl, cfg: cfg, stats: stats, setCookie: setCookie, logger: logger}
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
	mux.HandleFunc("GET /api/inbox", s.handleInbox)
	mux.HandleFunc("GET /api/profiles", s.handleProfiles)
	mux.HandleFunc("GET /api/campaigns", s.handleCampaigns)
	mux.HandleFunc("POST /api/campaigns/{name}", s.handleSaveCampaign)
	mux.HandleFunc("POST /api/settings", s.handleSettings)
	mux.HandleFunc("GET /api/cookie", s.handleGetCookie)
	mux.HandleFunc("POST /api/cookie", s.handleSetCookie)
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
		"contact_mode":      modeString(s.ctrl.GetContactMode()),
		"contact_label":     s.ctrl.ContactModeLabel(),
		"quiet_hours":       derefBool(s.ctrl.IsQuietHoursEnabled()),
		"quiet_hours_start": qStart,
		"quiet_hours_end":   qEnd,
		"last_poll":         lastPoll,
		"default_campaign":  s.cfg.DefaultCampaign,
		"campaigns":         s.campaignNames(),
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
		ContactMode     *string `json:"contact_mode"`
		QuietHours      *bool   `json:"quiet_hours"`
		QuietHoursStart *string `json:"quiet_hours_start"`
		QuietHoursEnd   *string `json:"quiet_hours_end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.ContactMode != nil {
		mode, ok := parseMode(*body.ContactMode)
		if !ok {
			writeErr(w, http.StatusBadRequest, errors.New("contact_mode must be off, test or on"))
			return
		}
		s.ctrl.SetContactMode(mode)
	}
	if body.QuietHours != nil {
		s.ctrl.SetQuietHours(*body.QuietHours)
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
	}
	s.handleOverview(w, r)
}

// handleGetCookie reports cookie presence without ever leaking the cookie
// itself. Source = "meta" means a hot-reloaded override is active; "env" means
// the original IS24_COOKIE env var (or empty config value) is in effect.
func (s *Server) handleGetCookie(w http.ResponseWriter, r *http.Request) {
	v, _ := s.repo.GetMeta(r.Context(), sqlite.MetaIS24Cookie)
	resp := map[string]any{
		"present": v != "" || strings.TrimSpace(s.cfg.IS24.Cookie) != "",
		"length":  0,
		"source":  "none",
	}
	switch {
	case v != "":
		resp["length"] = len(v)
		resp["source"] = "meta"
	case strings.TrimSpace(s.cfg.IS24.Cookie) != "":
		resp["length"] = len(s.cfg.IS24.Cookie)
		resp["source"] = "env"
	}
	writeJSON(w, http.StatusOK, resp)
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
