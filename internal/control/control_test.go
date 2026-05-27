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

	// Defaults: auto-contact on, quiet hours on.
	if !c.IsAutoContactEnabled() {
		t.Fatal("default should be auto-contact on")
	}
	if c.IsTestModeEnabled() {
		t.Fatal("default should not be test mode")
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

func TestIsQuietHoursReturnsCopy(t *testing.T) {
	c := New()
	v := c.IsQuietHoursEnabled()
	*v = false // mutating the returned pointer must not affect internal state
	if v2 := c.IsQuietHoursEnabled(); v2 == nil || !*v2 {
		t.Error("returned pointer must be a copy, internal state changed")
	}
}
