package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/control"
	"github.com/julianbeese/immo_bot/internal/repository/sqlite"
	"github.com/julianbeese/immo_bot/internal/secrets"
)

func newTestServer(t *testing.T) (*Server, *control.Controller) {
	t.Helper()
	repo, err := sqlite.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })

	cfg := &config.Config{
		DefaultCampaign: "single",
		Campaigns: map[string]config.Campaign{
			"single": {AIPrompt: "s"},
			"wg":     {AIPrompt: "w"},
		},
	}
	ctrl := control.New(nil, nil, control.Defaults{QuietHoursEnabled: true, QuietHoursStart: "22:00", QuietHoursEnd: "07:00", Timezone: "Europe/Berlin"})
	// StatsFunc returns (total, contacted, notified).
	stats := func(context.Context) (int, int, int) { return 5, 1, 3 }
	return New(repo, ctrl, cfg, stats, nil /* CookieSetter */, nil /* SettingsNotifier */, nil /* ApprovalHandler */, slog.Default()), ctrl
}

func TestOverview(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/overview", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var o map[string]any
	json.Unmarshal(rec.Body.Bytes(), &o)
	if o["contact_mode"] != "test" { // default is test mode
		t.Errorf("contact_mode = %v", o["contact_mode"])
	}
	stats := o["stats"].(map[string]any)
	if stats["total"].(float64) != 5 || stats["contacted"].(float64) != 1 {
		t.Errorf("stats wrong: %v", stats)
	}
}

func TestSettingsChangesController(t *testing.T) {
	s, ctrl := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(`{"contact_mode":"on","quiet_hours":false}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !ctrl.IsAutoContactEnabled() {
		t.Error("contact_mode=on should enable auto-contact")
	}
	if v := ctrl.IsQuietHoursEnabled(); v == nil || *v {
		t.Error("quiet_hours=false should disable quiet hours")
	}
}

func TestSettingsRejectsBadMode(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(`{"contact_mode":"bogus"}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad mode should 400, got %d", rec.Code)
	}
}

func TestAddProfileValidatesCampaign(t *testing.T) {
	s, _ := newTestServer(t)

	// unknown campaign → 400
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/profiles",
		strings.NewReader(`{"category":"nope","url":"https://is24.de/Suche/x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown campaign should 400, got %d", rec.Code)
	}

	// non-URL → 400
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/profiles",
		strings.NewReader(`{"category":"wg","url":"nope"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-URL should 400, got %d", rec.Code)
	}

	// valid → created and listed
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/profiles",
		strings.NewReader(`{"category":"wg","url":"https://www.immobilienscout24.de/Suche/de/berlin/berlin/wohnung-mieten"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid add should 201, got %d: %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/profiles", nil))
	var profiles []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &profiles)
	if len(profiles) != 1 || profiles[0]["category"] != "wg" {
		t.Errorf("profile not listed correctly: %v", profiles)
	}
}

func TestSetEmailPasswordEncrypted(t *testing.T) {
	repo, err := sqlite.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })

	cfg := &config.Config{}
	ctrl := control.New(nil, nil, control.Defaults{})
	stats := func(context.Context) (int, int, int) { return 0, 0, 0 }
	s := New(repo, ctrl, cfg, stats, nil, nil, nil, slog.Default())

	key := make([]byte, 32)
	copy(key, []byte("01234567890123456789012345678901"))
	enc, err := secrets.NewEncrypter(key)
	if err != nil {
		t.Fatal(err)
	}
	s.SetCookieEncrypter(enc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/email",
		strings.NewReader(`{"password":"my-app-password"}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var dto emailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if !dto.PasswordSet || dto.PasswordSource != "meta" {
		t.Errorf("dto = %+v, want password_set=true source=meta", dto)
	}

	stored, _ := repo.GetMeta(context.Background(), sqlite.MetaEmailPassword)
	if !secrets.IsEncrypted(stored) {
		t.Fatalf("stored password not encrypted: %q", stored)
	}
	pt, err := enc.Decrypt(stored)
	if err != nil || pt != "my-app-password" {
		t.Errorf("decrypt = %q, %v", pt, err)
	}
}

func TestIndexServed(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ImmoBot") {
		t.Errorf("index not served: %d", rec.Code)
	}
}
