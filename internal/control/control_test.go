package control

import (
	"context"
	"sync"
	"testing"
	"time"
)

// newTestCtrl builds a controller with no persistence and quiet-hours-on
// defaults — matches the pre-persistence behaviour the older tests rely on.
func newTestCtrl() *Controller {
	return New(nil, nil, Defaults{
		QuietHoursEnabled: true,
		QuietHoursStart:   "22:00",
		QuietHoursEnd:     "07:00",
		Timezone:          "Europe/Berlin",
	})
}

func TestNormalizeCommand(t *testing.T) {
	cases := map[string]string{
		"/status":       "status",
		"status":        "status",
		"  /Status  ":   "status",
		"/contact_on":   "contact_on",
		"contact on":    "contact_on",
		"CONTACT ON":    "contact_on",
		"/quiet_off":    "quiet_off",
		"quiet   off":   "quiet_off", // collapses repeated whitespace
		"":              "",
		"/":             "",
		"contact on go": "contact_on_go", // extra tokens are not stripped
	}
	for in, want := range cases {
		if got := normalizeCommand(in); got != want {
			t.Errorf("normalizeCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleCommandTransitions(t *testing.T) {
	c := newTestCtrl()

	// Safe defaults: test mode (no live contact), quiet hours on.
	if c.IsAutoContactEnabled() {
		t.Fatal("default must NOT be live auto-contact")
	}
	if !c.IsTestModeEnabled() {
		t.Fatal("default should be test mode")
	}
	if v := c.IsQuietHoursEnabled(); v == nil || !*v {
		t.Fatal("default quiet hours should be on")
	}

	if got := c.HandleCommand("/contact_off"); got == "" {
		t.Fatal("contact_off returned empty")
	}
	if c.IsAutoContactEnabled() {
		t.Error("contact_off should disable auto-contact")
	}

	c.HandleCommand("contact test") // plaintext form
	if !c.IsTestModeEnabled() {
		t.Error("contact test should enable test mode")
	}
	if c.IsAutoContactEnabled() {
		t.Error("test mode is not auto-contact")
	}

	c.HandleCommand("/contact_on")
	if !c.IsAutoContactEnabled() {
		t.Error("contact_on should enable auto-contact")
	}

	c.HandleCommand("/quiet_off")
	if v := c.IsQuietHoursEnabled(); v == nil || *v {
		t.Error("quiet_off should disable quiet hours")
	}
	c.HandleCommand("quiet on")
	if v := c.IsQuietHoursEnabled(); v == nil || !*v {
		t.Error("quiet on should enable quiet hours")
	}
}

func TestHandleCommandResponses(t *testing.T) {
	c := newTestCtrl()
	if got := c.HandleCommand(""); got != "" {
		t.Errorf("empty input should yield empty response, got %q", got)
	}
	if got := c.HandleCommand("/help"); got == "" {
		t.Error("help should return text")
	}
	if got := c.HandleCommand("/bogus"); got == "" {
		t.Error("unknown command should return a hint, not empty")
	}
}

func TestStatsCallback(t *testing.T) {
	c := newTestCtrl()
	// Without callback, stats has a fallback.
	if got := c.HandleCommand("/stats"); got == "" {
		t.Error("stats without callback should still respond")
	}
	c.SetCallbacks(func() string { return "*Profile:* 3" }, func() string { return "STATSXYZ" })
	if got := c.HandleCommand("/stats"); got != "STATSXYZ" {
		t.Errorf("stats should use callback, got %q", got)
	}
	if got := c.HandleCommand("/status"); got == "" {
		t.Error("status should include callback output")
	}
}

func TestProfileCommands(t *testing.T) {
	c := newTestCtrl()
	var gotCat, gotURL, gotName, gotDel string
	c.SetProfileCallbacks(
		func(category, url, name string) string { gotCat, gotURL, gotName = category, url, name; return "ADDED" },
		func() string { return "LIST" },
		func(id string) string { gotDel = id; return "DELETED" },
	)

	// add with explicit name, no category
	if got := c.HandleCommand("/addprofil https://is24.de/Suche/x Mein Profil"); got != "ADDED" {
		t.Errorf("addprofil response = %q", got)
	}
	if gotCat != "" || gotURL != "https://is24.de/Suche/x" || gotName != "Mein Profil" {
		t.Errorf("add args: cat=%q url=%q name=%q", gotCat, gotURL, gotName)
	}

	// add with category (leading non-URL token)
	c.HandleCommand("/addprofil wg https://is24.de/Suche/y WG-Suche")
	if gotCat != "wg" || gotURL != "https://is24.de/Suche/y" || gotName != "WG-Suche" {
		t.Errorf("add with category: cat=%q url=%q name=%q", gotCat, gotURL, gotName)
	}

	// add without name → callback gets empty name
	gotName = "x"
	c.HandleCommand("addprofil https://is24.de/y")
	if gotName != "" {
		t.Errorf("expected empty name, got %q", gotName)
	}

	// list (plaintext alias)
	if got := c.HandleCommand("listprofile"); got != "LIST" {
		t.Errorf("list response = %q", got)
	}

	// delete with id
	if got := c.HandleCommand("/delprofil 7"); got != "DELETED" || gotDel != "7" {
		t.Errorf("del response=%q id=%q", got, gotDel)
	}
}

func TestProfileCommandValidation(t *testing.T) {
	c := newTestCtrl()
	c.SetProfileCallbacks(
		func(category, url, name string) string { return "ADDED" },
		func() string { return "LIST" },
		func(id string) string { return "DELETED" },
	)
	// missing URL
	if got := c.HandleCommand("/addprofil"); got == "ADDED" {
		t.Error("addprofil without URL should not call callback")
	}
	// non-URL argument
	if got := c.HandleCommand("/addprofil notaurl"); got == "ADDED" {
		t.Error("addprofil with non-URL should not call callback")
	}
	// delprofil without id
	if got := c.HandleCommand("/delprofil"); got == "DELETED" {
		t.Error("delprofil without id should not call callback")
	}
}

func TestProfileCommandsWithoutCallbacks(t *testing.T) {
	c := newTestCtrl() // no SetProfileCallbacks
	if got := c.HandleCommand("/addprofil https://is24.de/x"); got == "" {
		t.Error("addprofil without callback should return a message, not empty")
	}
	if got := c.HandleCommand("/listprofile"); got == "" {
		t.Error("listprofile without callback should return a message")
	}
}

func TestIsQuietHoursReturnsCopy(t *testing.T) {
	c := newTestCtrl()
	v := c.IsQuietHoursEnabled()
	*v = false // mutating the returned pointer must not affect internal state
	if v2 := c.IsQuietHoursEnabled(); v2 == nil || !*v2 {
		t.Error("returned pointer must be a copy, internal state changed")
	}
}

// memStore is a goroutine-safe in-memory SettingsStore for tests.
type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemStore(seed map[string]string) *memStore {
	s := &memStore{m: map[string]string{}}
	for k, v := range seed {
		s.m[k] = v
	}
	return s
}

func (s *memStore) GetMeta(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k], nil
}

func (s *memStore) SetMeta(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}

func TestPersistsSettingsToStore(t *testing.T) {
	store := newMemStore(nil)
	c := New(store, nil, Defaults{QuietHoursEnabled: true, QuietHoursStart: "22:00", QuietHoursEnd: "07:00", Timezone: "Europe/Berlin"})

	c.SetContactMode(ContactModeOn)
	c.SetQuietHours(false)
	if err := c.SetQuietHoursWindow("23:30", "06:15"); err != nil {
		t.Fatalf("SetQuietHoursWindow: %v", err)
	}

	if got := store.m[MetaContactMode]; got != "on" {
		t.Errorf("contact mode persisted = %q, want on", got)
	}
	if got := store.m[MetaQuietHoursEnabled]; got != "false" {
		t.Errorf("quiet enabled persisted = %q, want false", got)
	}
	if got := store.m[MetaQuietHoursStart]; got != "23:30" {
		t.Errorf("quiet start persisted = %q", got)
	}
	if got := store.m[MetaQuietHoursEnd]; got != "06:15" {
		t.Errorf("quiet end persisted = %q", got)
	}
}

func TestReloadsSettingsFromStore(t *testing.T) {
	store := newMemStore(map[string]string{
		MetaContactMode:       "on",
		MetaQuietHoursEnabled: "false",
		MetaQuietHoursStart:   "20:00",
		MetaQuietHoursEnd:     "05:00",
	})
	c := New(store, nil, Defaults{QuietHoursEnabled: true, QuietHoursStart: "22:00", QuietHoursEnd: "07:00", Timezone: "Europe/Berlin"})

	if !c.IsAutoContactEnabled() {
		t.Error("contact mode should reload as ContactModeOn")
	}
	if v := c.IsQuietHoursEnabled(); v == nil || *v {
		t.Error("quiet hours should reload as false")
	}
	if s, e := c.QuietHoursWindow(); s != "20:00" || e != "05:00" {
		t.Errorf("quiet window reload = %q/%q, want 20:00/05:00", s, e)
	}
}

func TestSetQuietHoursWindowRejectsGarbage(t *testing.T) {
	c := newTestCtrl()
	if err := c.SetQuietHoursWindow("99:00", "07:00"); err == nil {
		t.Error("hour > 23 should be rejected")
	}
	if err := c.SetQuietHoursWindow("22:00", "07:99"); err == nil {
		t.Error("minute > 59 should be rejected")
	}
	if err := c.SetQuietHoursWindow("22", "07:00"); err == nil {
		t.Error("missing colon should be rejected")
	}
}

func TestIsWithinQuietHoursOvernight(t *testing.T) {
	c := newTestCtrl() // 22:00-07:00 Europe/Berlin

	// 03:00 → inside overnight window
	if !c.IsWithinQuietHours(timeAt(t, 3, 0)) {
		t.Error("03:00 should be within 22:00-07:00")
	}
	// 12:00 → outside
	if c.IsWithinQuietHours(timeAt(t, 12, 0)) {
		t.Error("12:00 should be outside 22:00-07:00")
	}
	// 22:30 → inside
	if !c.IsWithinQuietHours(timeAt(t, 22, 30)) {
		t.Error("22:30 should be within 22:00-07:00")
	}
	// 07:00 boundary → end-exclusive → outside
	if c.IsWithinQuietHours(timeAt(t, 7, 0)) {
		t.Error("07:00 is end-exclusive, should be outside")
	}
}

func TestCookieCommand(t *testing.T) {
	c := newTestCtrl()
	var gotCookie string
	c.SetCookieCallback(func(_ context.Context, cookie string) error {
		gotCookie = cookie
		return nil
	})
	// Too short → usage hint, callback not called.
	if got := c.HandleCommand("/cookie short"); got == "" {
		t.Error("short cookie should produce a usage hint")
	}
	if gotCookie != "" {
		t.Error("short cookie must not reach callback")
	}

	long := "name1=" + repeat("a", 60) + "; name2=val"
	if got := c.HandleCommand("/cookie " + long); got == "" {
		t.Error("valid cookie command should produce a confirmation")
	}
	if gotCookie != long {
		t.Errorf("cookie callback got %q, want %q", gotCookie, long)
	}
}

// repeat is a tiny stand-in for strings.Repeat to keep the import set minimal.
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// timeAt returns a deterministic time.Time anchored in Europe/Berlin so the
// controller's IsWithinQuietHours (which re-interprets via its own timezone)
// sees exactly the requested clock value.
func timeAt(t *testing.T, h, m int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return time.Date(2026, 1, 15, h, m, 0, 0, loc)
}
