package control

import "testing"

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
	c := New()

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
	c := New()
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
	c := New()
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
	c := New()
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
	c := New()
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
	c := New() // no SetProfileCallbacks
	if got := c.HandleCommand("/addprofil https://is24.de/x"); got == "" {
		t.Error("addprofil without callback should return a message, not empty")
	}
	if got := c.HandleCommand("/listprofile"); got == "" {
		t.Error("listprofile without callback should return a message")
	}
}

func TestIsQuietHoursReturnsCopy(t *testing.T) {
	c := New()
	v := c.IsQuietHoursEnabled()
	*v = false // mutating the returned pointer must not affect internal state
	if v2 := c.IsQuietHoursEnabled(); v2 == nil || !*v2 {
		t.Error("returned pointer must be a copy, internal state changed")
	}
}
