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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/control"
	"github.com/julianbeese/immo_bot/internal/domain"
	"github.com/julianbeese/immo_bot/internal/repository/sqlite"
)

//go:embed all:frontend/out
var frontendFS embed.FS

// StatsFunc returns aggregate listing counts (total, contacted, notified).
type StatsFunc func(ctx context.Context) (total, contacted, notified int)

// Server is the dashboard HTTP server.
type Server struct {
	repo   *sqlite.Repository
	ctrl   *control.Controller
	cfg    *config.Config
	stats  StatsFunc
	logger *slog.Logger
}

// New creates a dashboard server.
func New(repo *sqlite.Repository, ctrl *control.Controller, cfg *config.Config, stats StatsFunc, logger *slog.Logger) *Server {
	return &Server{repo: repo, ctrl: ctrl, cfg: cfg, stats: stats, logger: logger}
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
	mux.HandleFunc("GET /api/profiles", s.handleProfiles)
	mux.HandleFunc("POST /api/settings", s.handleSettings)
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

	resp := map[string]any{
		"contact_mode":     modeString(s.ctrl.GetContactMode()),
		"contact_label":    s.ctrl.ContactModeLabel(),
		"quiet_hours":      derefBool(s.ctrl.IsQuietHoursEnabled()),
		"last_poll":        lastPoll,
		"default_campaign": s.cfg.DefaultCampaign,
		"campaigns":        s.campaignNames(),
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

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.repo.ListAllSearchProfiles(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ContactMode *string `json:"contact_mode"`
		QuietHours  *bool   `json:"quiet_hours"`
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
	s.handleOverview(w, r)
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
