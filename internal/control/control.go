// Package control holds the transport-neutral bot state and command handling
// shared by all notification channels (Telegram, WhatsApp, ...).
//
// Command responses use WhatsApp-style markup (*bold*). Transports that need a
// different format (e.g. Telegram HTML) convert it on their side.
//
// Persistence: settings (contact mode + quiet hours flag + quiet hours window)
// are loaded from a SettingsStore at construction and written back through it
// on every Set call, so changes survive bot restarts. A nil store is allowed
// (everything stays in-memory) — useful for tests.
package control

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ContactMode represents the contact behavior mode.
type ContactMode int

const (
	ContactModeOff     ContactMode = iota // Observation only
	ContactModeTest                       // Show message preview, don't send
	ContactModeApprove                    // Suggest one at a time, send on user approval
	ContactModeOn                         // Actually send contacts
)

// SettingsStore is the minimal persistence surface the controller needs.
// The sqlite repository's meta table implements it directly.
type SettingsStore interface {
	GetMeta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
}

// Meta keys under which settings are persisted in the sqlite meta table.
const (
	MetaContactMode         = "settings.contact_mode"
	MetaQuietHoursEnabled   = "settings.quiet_hours_enabled"
	MetaQuietHoursStart     = "settings.quiet_hours_start"
	MetaQuietHoursEnd       = "settings.quiet_hours_end"
	MetaPollIntervalSeconds = "settings.poll_interval_seconds"
	MetaContactTypeDelayMs  = "settings.contact_type_delay_ms"
	MetaContactActionDelayMs = "settings.contact_action_delay_ms"
	MetaExcludeFurnished    = "settings.exclude_furnished"
)

// Allowed ranges for dashboard-tunable timing knobs. Min values exist to keep
// the bot from hammering IS24 or behaving like an obvious bot during contact.
const (
	MinPollInterval        = 60 * time.Second
	MaxPollInterval        = 30 * time.Minute
	MinContactTypeDelay    = 10 * time.Millisecond
	MaxContactTypeDelay    = 500 * time.Millisecond
	MinContactActionDelay  = 100 * time.Millisecond
	MaxContactActionDelay  = 10 * time.Second
)

// Defaults bundles the start-of-day values used when nothing is persisted yet.
// The web/Telegram contact-mode default is ContactModeTest (set in New).
type Defaults struct {
	QuietHoursEnabled bool
	QuietHoursStart   string // "HH:MM"
	QuietHoursEnd     string // "HH:MM"
	Timezone          string // IANA tz, e.g. "Europe/Berlin"

	// Timing defaults — used when nothing is persisted yet. Zero falls back to
	// hard-coded fallbacks (5m / 50ms / 1s) so older callers stay compatible.
	PollInterval       time.Duration
	ContactTypeDelay   time.Duration
	ContactActionDelay time.Duration
}

// Controller holds shared bot state and turns chat commands into responses.
// It is safe for concurrent use.
type Controller struct {
	mu sync.RWMutex

	store    SettingsStore
	logger   *slog.Logger
	timezone string

	contactMode ContactMode
	quietHours  bool
	quietStart  string
	quietEnd    string

	pollInterval       time.Duration
	contactTypeDelay   time.Duration
	contactActionDelay time.Duration
	excludeFurnished   bool

	// Subscribers notified when the poll interval changes (scheduler resets its
	// ticker). Append-only; nil-safe.
	pollIntervalSubs []chan<- struct{}

	// Callbacks providing extra info for /status and /stats.
	onStatusRequest func() string
	onStatsRequest  func() string

	// Callbacks for managing search profiles (need DB access, injected by main).
	onAddProfile   func(category, url, name string) string
	onListProfiles func() string
	onDelProfile   func(id string) string

	// Callback that applies a fresh IS24 cookie at runtime (scheduler hot-reload
	// + meta persistence). Used by /cookie chat command.
	onSetCookie func(ctx context.Context, cookie string) error
}

// New creates a controller, loading any persisted settings from the store.
// Pass nil for store to keep everything in-memory (tests). The defaults are
// used only when a corresponding meta key is missing.
func New(store SettingsStore, logger *slog.Logger, def Defaults) *Controller {
	if logger == nil {
		logger = slog.Default()
	}
	c := &Controller{
		store:              store,
		logger:             logger,
		timezone:           def.Timezone,
		contactMode:        ContactModeTest,
		quietHours:         def.QuietHoursEnabled,
		quietStart:         def.QuietHoursStart,
		quietEnd:           def.QuietHoursEnd,
		pollInterval:       fallbackDuration(def.PollInterval, 30*time.Minute),
		contactTypeDelay:   fallbackDuration(def.ContactTypeDelay, 50*time.Millisecond),
		contactActionDelay: fallbackDuration(def.ContactActionDelay, 1*time.Second),
		excludeFurnished:   true,
	}
	c.loadFromStore()
	return c
}

func fallbackDuration(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

func clampDuration(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (c *Controller) loadFromStore() {
	if c.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if v, _ := c.store.GetMeta(ctx, MetaContactMode); v != "" {
		if mode, ok := parseContactMode(v); ok {
			c.contactMode = mode
		}
	}
	if v, _ := c.store.GetMeta(ctx, MetaQuietHoursEnabled); v != "" {
		c.quietHours = v == "true"
	}
	if v, _ := c.store.GetMeta(ctx, MetaQuietHoursStart); v != "" {
		c.quietStart = v
	}
	if v, _ := c.store.GetMeta(ctx, MetaQuietHoursEnd); v != "" {
		c.quietEnd = v
	}
	if v, _ := c.store.GetMeta(ctx, MetaPollIntervalSeconds); v != "" {
		if secs, err := time.ParseDuration(v + "s"); err == nil && secs > 0 {
			c.pollInterval = clampDuration(secs, MinPollInterval, MaxPollInterval)
		}
	}
	if v, _ := c.store.GetMeta(ctx, MetaContactTypeDelayMs); v != "" {
		if d, err := time.ParseDuration(v + "ms"); err == nil && d > 0 {
			c.contactTypeDelay = clampDuration(d, MinContactTypeDelay, MaxContactTypeDelay)
		}
	}
	if v, _ := c.store.GetMeta(ctx, MetaContactActionDelayMs); v != "" {
		if d, err := time.ParseDuration(v + "ms"); err == nil && d > 0 {
			c.contactActionDelay = clampDuration(d, MinContactActionDelay, MaxContactActionDelay)
		}
	}
	if v, _ := c.store.GetMeta(ctx, MetaExcludeFurnished); v != "" {
		c.excludeFurnished = v == "true"
	}
	c.logger.Info("settings loaded from meta",
		"contact_mode", contactModeString(c.contactMode),
		"quiet_hours_enabled", c.quietHours,
		"quiet_hours_window", c.quietStart+"-"+c.quietEnd,
		"poll_interval", c.pollInterval,
		"contact_type_delay", c.contactTypeDelay,
		"contact_action_delay", c.contactActionDelay,
	)
}

func (c *Controller) persist(key, value string) {
	if c.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.store.SetMeta(ctx, key, value); err != nil {
		c.logger.Warn("failed to persist setting", "key", key, "error", err)
	}
}

// SetCallbacks sets the callback functions for status and stats details.
func (c *Controller) SetCallbacks(onStatus, onStats func() string) {
	c.onStatusRequest = onStatus
	c.onStatsRequest = onStats
}

// SetProfileCallbacks wires the search-profile management commands.
func (c *Controller) SetProfileCallbacks(onAdd func(category, url, name string) string, onList func() string, onDel func(id string) string) {
	c.onAddProfile = onAdd
	c.onListProfiles = onList
	c.onDelProfile = onDel
}

// SetCookieCallback wires the /cookie chat command to the scheduler's hot
// reload (validates + persists + tells the IS24 client to use the new value).
func (c *Controller) SetCookieCallback(fn func(ctx context.Context, cookie string) error) {
	c.onSetCookie = fn
}

// HandleCommand normalizes a raw chat message and returns the response text.
// Accepts both slash and plain forms: "/contact_on", "contact on", "Status".
// Returns "" if the message is not a recognized command (caller may ignore it).
func (c *Controller) HandleCommand(raw string) string {
	// Argument commands are routed by their first token so the rest (URL, id,
	// name, cookie value) is preserved. No-arg commands fall through to
	// normalizeCommand.
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(raw), "/"))
	if len(fields) == 0 {
		return ""
	}
	switch strings.ToLower(fields[0]) {
	case "addprofil", "addprofile", "addprof":
		return c.handleAddProfile(fields[1:])
	case "listprofile", "listprofiles", "profile", "profiles":
		if c.onListProfiles != nil {
			return c.onListProfiles()
		}
		return "Profil-Verwaltung nicht verfügbar."
	case "delprofil", "delprofile", "delprof":
		if len(fields) < 2 {
			return "Nutzung: /delprofil <id>"
		}
		if c.onDelProfile != nil {
			return c.onDelProfile(fields[1])
		}
		return "Profil-Verwaltung nicht verfügbar."
	case "cookie":
		// Everything after "/cookie " is the new cookie string. Preserve the
		// raw payload (cookies contain '=' and ';' which Fields() leaves alone,
		// but use the original raw to keep internal whitespace intact).
		return c.handleCookie(stripFirstToken(raw))
	}

	cmd := normalizeCommand(raw)
	if cmd == "" {
		return ""
	}

	switch cmd {
	case "start", "help":
		return c.helpMessage()
	case "status":
		return c.statusMessage()
	case "contact_on":
		c.SetContactMode(ContactModeOn)
		return "✅ *Auto-Kontakt aktiviert*\n\nNeue Wohnungen werden automatisch angeschrieben."
	case "contact_off":
		c.SetContactMode(ContactModeOff)
		return "⏸ *Beobachtungsmodus*\n\nNeue Wohnungen werden nur gemeldet, keine Kontaktaufnahme."
	case "contact_test":
		c.SetContactMode(ContactModeTest)
		return "🧪 *Test-Modus aktiviert*\n\nNeue Wohnungen werden gemeldet und die Nachricht wird dir als Vorschau gezeigt (nicht gesendet)."
	case "contact_approve":
		c.SetContactMode(ContactModeApprove)
		return "🛂 *Approval-Modus aktiviert*\n\nNeue Wohnungen werden einzeln vorgeschlagen — bestätige mit ✅ um die Nachricht zu senden, oder ❌ um sie zu verwerfen."
	case "quiet_on":
		c.SetQuietHours(true)
		s, e := c.QuietHoursWindow()
		return fmt.Sprintf("🌙 *Ruhezeiten aktiviert*\n\nBot pausiert zwischen %s-%s Uhr.", s, e)
	case "quiet_off":
		c.SetQuietHours(false)
		return "☀️ *Ruhezeiten deaktiviert*\n\nBot läuft rund um die Uhr."
	case "stats":
		if c.onStatsRequest != nil {
			return c.onStatsRequest()
		}
		return "Statistiken nicht verfügbar."
	default:
		return "Unbekannter Befehl. Nutze /help für eine Übersicht."
	}
}

// normalizeCommand turns "/Contact On" or "contact_on" into the canonical
// "contact_on" form: drops a leading slash, lowercases, and joins
// whitespace-separated tokens with underscores. Commands take no arguments, so
// extra tokens (e.g. "contact on now") produce an unknown command rather than
// being silently trimmed.
func normalizeCommand(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "/")
	s = strings.ToLower(strings.TrimSpace(s))
	// Collapse internal whitespace to single underscores.
	s = strings.Join(strings.Fields(s), "_")
	return s
}

// handleAddProfile parses "[campaign] <url> [name...]" and delegates creation
// to the injected callback. The optional leading token (not a URL) is the
// campaign/category; the callback validates it.
func (c *Controller) handleAddProfile(args []string) string {
	const usage = "Nutzung: /addprofil [kampagne] <IS24-Such-URL> [Name]\n\nErst auf immobilienscout24.de die Suche bauen, dann die URL hierher kopieren."

	category := ""
	rest := args
	if len(rest) > 0 && !looksLikeURL(rest[0]) {
		category = strings.ToLower(rest[0])
		rest = rest[1:]
	}
	if len(rest) == 0 || !looksLikeURL(rest[0]) {
		return usage
	}
	if c.onAddProfile == nil {
		return "Profil-Verwaltung nicht verfügbar."
	}
	url := rest[0]
	name := strings.TrimSpace(strings.Join(rest[1:], " "))
	return c.onAddProfile(category, url, name)
}

// handleCookie validates the new IS24 cookie string and pushes it through the
// scheduler hot-reload callback. Reasonable length check guards against the
// user pasting only a fragment by accident.
func (c *Controller) handleCookie(value string) string {
	v := strings.TrimSpace(value)
	if len(v) < 50 {
		return "Nutzung: /cookie <gesamter Cookie-String>\n\nKopier alle Cookies von www.immobilienscout24.de aus DevTools."
	}
	if c.onSetCookie == nil {
		return "Cookie-Verwaltung nicht verfügbar."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.onSetCookie(ctx, v); err != nil {
		return fmt.Sprintf("❌ Cookie-Update fehlgeschlagen: %s", err.Error())
	}
	return fmt.Sprintf("✅ *Cookie aktualisiert* (Länge: %d).\nNächster Poll-Zyklus nutzt den neuen Cookie.", len(v))
}

// stripFirstToken returns the raw input with the first whitespace-delimited
// token removed (the command name itself). Preserves the rest verbatim,
// including any '=' or ';' characters in the payload.
func stripFirstToken(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "/")
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return ""
}

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func (c *Controller) helpMessage() string {
	return `🏠 *ImmoBot Befehle*

*Kontakt:*
/contact_on - Auto-Kontakt aktivieren
/contact_approve - Vorschläge einzeln bestätigen
/contact_test - Test-Modus (Nachricht-Vorschau)
/contact_off - Nur beobachten (kein Kontakt)

*Ruhezeiten:*
/quiet_on - Ruhezeiten an
/quiet_off - Ruhezeiten aus (24/7)

*Suchprofile:*
/addprofil [kampagne] <URL> [Name] - Profil aus IS24-Such-URL anlegen
/listprofile - Aktive Profile anzeigen
/delprofil <id> - Profil deaktivieren

*Cookie:*
/cookie <string> - IS24-Cookie aktualisieren (ohne Restart)

*Info:*
/status - Aktueller Bot-Status
/stats - Statistiken anzeigen
/help - Diese Hilfe`
}

func (c *Controller) statusMessage() string {
	c.mu.RLock()
	contactMode := c.contactMode
	quietHours := c.quietHours
	qs, qe := c.quietStart, c.quietEnd
	c.mu.RUnlock()

	mode := contactMode.Label()

	quietStatus := "☀️ Aus (24/7)"
	if quietHours {
		quietStatus = fmt.Sprintf("🌙 An (%s-%s)", qs, qe)
	}

	status := fmt.Sprintf(`🏠 *ImmoBot Status*

*Kontakt:* %s
*Ruhezeiten:* %s

Befehle: /help für alle Optionen`, mode, quietStatus)

	if c.onStatusRequest != nil {
		status += "\n\n" + c.onStatusRequest()
	}

	return status
}

// ContactModeLabel returns a human label for the current contact mode (markup).
func (c *Controller) ContactModeLabel() string {
	return c.GetContactMode().Label()
}

// Label returns the human-readable German label for a contact mode, suitable
// for chat messages and dashboard display.
func (m ContactMode) Label() string {
	switch m {
	case ContactModeOff:
		return "⏸ Beobachtungsmodus"
	case ContactModeTest:
		return "🧪 Test-Modus (Nachricht-Vorschau)"
	case ContactModeApprove:
		return "🛂 Approval-Modus (einzelne Bestätigung)"
	case ContactModeOn:
		return "✅ Auto-Kontakt aktiv"
	}
	return "unbekannt"
}

// contactModeString returns the canonical lower-case mode token used in the
// meta store and the HTTP API (off/test/approve/on).
func contactModeString(mode ContactMode) string {
	switch mode {
	case ContactModeOff:
		return "off"
	case ContactModeTest:
		return "test"
	case ContactModeApprove:
		return "approve"
	case ContactModeOn:
		return "on"
	}
	return "test"
}

// parseContactMode reverses contactModeString.
func parseContactMode(s string) (ContactMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off":
		return ContactModeOff, true
	case "test":
		return ContactModeTest, true
	case "approve":
		return ContactModeApprove, true
	case "on":
		return ContactModeOn, true
	}
	return 0, false
}

// IsAutoContactEnabled reports whether auto-contact is on (actually sends messages).
func (c *Controller) IsAutoContactEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactMode == ContactModeOn
}

// IsTestModeEnabled reports whether test mode is on (shows message preview).
func (c *Controller) IsTestModeEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactMode == ContactModeTest
}

// IsApprovalModeEnabled reports whether approval mode is on (per-listing
// confirmation via Telegram inline buttons).
func (c *Controller) IsApprovalModeEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactMode == ContactModeApprove
}

// IsQuietHoursEnabled returns a pointer to the quiet-hours override flag.
// (Pointer kept for backwards compatibility with the scheduler callback shape.)
func (c *Controller) IsQuietHoursEnabled() *bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := c.quietHours
	return &v
}

// QuietHoursWindow returns the currently effective start/end strings ("HH:MM").
func (c *Controller) QuietHoursWindow() (start, end string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.quietStart, c.quietEnd
}

// IsWithinQuietHours reports whether the given time falls inside the
// configured quiet-hours window, regardless of the enabled flag (the
// scheduler combines both). Handles overnight wrap (start > end).
func (c *Controller) IsWithinQuietHours(now time.Time) bool {
	c.mu.RLock()
	tz, qs, qe := c.timezone, c.quietStart, c.quietEnd
	c.mu.RUnlock()

	if loc, err := time.LoadLocation(tz); err == nil {
		now = now.In(loc)
	} else {
		now = now.In(time.Local)
	}
	cur := now.Hour()*60 + now.Minute()
	sh, sm := parseHHMM(qs)
	eh, em := parseHHMM(qe)
	start := sh*60 + sm
	end := eh*60 + em
	if start == end {
		return false
	}
	if start > end {
		return cur >= start || cur < end
	}
	return cur >= start && cur < end
}

// parseHHMM parses "HH:MM" leniently (defaults 0:0 on garbage).
func parseHHMM(s string) (int, int) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return atoiClamp(parts[0], 0, 23), atoiClamp(parts[1], 0, 59)
}

func atoiClamp(s string, lo, hi int) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// validHHMM reports whether s is a well-formed "HH:MM" string in [00:00, 23:59].
func validHHMM(s string) bool {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
		return false
	}
	h, m := 0, 0
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return false
		}
		h = h*10 + int(r-'0')
	}
	for _, r := range parts[1] {
		if r < '0' || r > '9' {
			return false
		}
		m = m*10 + int(r-'0')
	}
	return h <= 23 && m <= 59
}

// NormalizeHHMM checks the format and returns canonical "HH:MM" (zero-padded).
// Returns (s, true) when valid, ("", false) otherwise. Exported for the web
// handler.
func NormalizeHHMM(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !validHHMM(s) {
		return "", false
	}
	parts := strings.SplitN(s, ":", 2)
	h := atoiClamp(parts[0], 0, 23)
	m := atoiClamp(parts[1], 0, 59)
	return fmt.Sprintf("%02d:%02d", h, m), true
}

// GetContactMode returns the current contact mode.
func (c *Controller) GetContactMode() ContactMode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactMode
}

// SetContactMode sets the contact mode and persists it.
func (c *Controller) SetContactMode(mode ContactMode) {
	c.mu.Lock()
	c.contactMode = mode
	c.mu.Unlock()
	c.persist(MetaContactMode, contactModeString(mode))
}

// SetQuietHours enables or disables quiet hours and persists the flag.
func (c *Controller) SetQuietHours(enabled bool) {
	c.mu.Lock()
	c.quietHours = enabled
	c.mu.Unlock()
	val := "false"
	if enabled {
		val = "true"
	}
	c.persist(MetaQuietHoursEnabled, val)
}

// GetPollInterval returns the current poll interval (scheduler reads this each
// cycle so changes take effect on the next tick).
func (c *Controller) GetPollInterval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pollInterval
}

// SetPollInterval clamps to [MinPollInterval, MaxPollInterval], persists, and
// notifies any subscribers so the scheduler can reset its ticker immediately.
func (c *Controller) SetPollInterval(d time.Duration) time.Duration {
	v := clampDuration(d, MinPollInterval, MaxPollInterval)
	c.mu.Lock()
	c.pollInterval = v
	subs := append([]chan<- struct{}(nil), c.pollIntervalSubs...)
	c.mu.Unlock()
	c.persist(MetaPollIntervalSeconds, fmt.Sprintf("%d", int(v.Seconds())))
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return v
}

// SubscribePollInterval registers ch to receive a non-blocking ping whenever
// SetPollInterval is called. The channel should be buffered (size 1) — sends
// that would block are dropped, since the receiver only needs the latest signal.
func (c *Controller) SubscribePollInterval(ch chan<- struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pollIntervalSubs = append(c.pollIntervalSubs, ch)
}

// GetContactTypeDelay returns the current per-keystroke delay for the contact
// form (Chrome typing). Contact service reads this fresh on each form submit.
func (c *Controller) GetContactTypeDelay() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactTypeDelay
}

// SetContactTypeDelay clamps to [MinContactTypeDelay, MaxContactTypeDelay] and
// persists. Returns the effective value after clamping.
func (c *Controller) SetContactTypeDelay(d time.Duration) time.Duration {
	v := clampDuration(d, MinContactTypeDelay, MaxContactTypeDelay)
	c.mu.Lock()
	c.contactTypeDelay = v
	c.mu.Unlock()
	c.persist(MetaContactTypeDelayMs, fmt.Sprintf("%d", v.Milliseconds()))
	return v
}

// GetContactActionDelay returns the current pause between Chrome actions
// (clicks / field navigation) during contact form submission.
func (c *Controller) GetContactActionDelay() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactActionDelay
}

// SetContactActionDelay clamps to [MinContactActionDelay, MaxContactActionDelay]
// and persists. Returns the effective value after clamping.
func (c *Controller) SetContactActionDelay(d time.Duration) time.Duration {
	v := clampDuration(d, MinContactActionDelay, MaxContactActionDelay)
	c.mu.Lock()
	c.contactActionDelay = v
	c.mu.Unlock()
	c.persist(MetaContactActionDelayMs, fmt.Sprintf("%d", v.Milliseconds()))
	return v
}

// IsExcludeFurnishedEnabled reports whether the bot should drop listings
// that look furnished (möbliert / furnished). Default true.
func (c *Controller) IsExcludeFurnishedEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.excludeFurnished
}

// SetExcludeFurnished toggles the furnished filter and persists it.
func (c *Controller) SetExcludeFurnished(enabled bool) {
	c.mu.Lock()
	c.excludeFurnished = enabled
	c.mu.Unlock()
	val := "false"
	if enabled {
		val = "true"
	}
	c.persist(MetaExcludeFurnished, val)
}

// SetQuietHoursWindow stores a new "HH:MM" start/end pair. Returns an error if
// either value is not a valid clock time; nothing is changed in that case.
func (c *Controller) SetQuietHoursWindow(start, end string) error {
	s, ok := NormalizeHHMM(start)
	if !ok {
		return fmt.Errorf("invalid start time %q (want HH:MM)", start)
	}
	e, ok := NormalizeHHMM(end)
	if !ok {
		return fmt.Errorf("invalid end time %q (want HH:MM)", end)
	}
	c.mu.Lock()
	c.quietStart = s
	c.quietEnd = e
	c.mu.Unlock()
	c.persist(MetaQuietHoursStart, s)
	c.persist(MetaQuietHoursEnd, e)
	return nil
}
