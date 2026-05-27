// Package control holds the transport-neutral bot state and command handling
// shared by all notification channels (Telegram, WhatsApp, ...).
//
// Command responses use WhatsApp-style markup (*bold*). Transports that need a
// different format (e.g. Telegram HTML) convert it on their side.
package control

import (
	"fmt"
	"strings"
	"sync"
)

// ContactMode represents the contact behavior mode.
type ContactMode int

const (
	ContactModeOff  ContactMode = iota // Observation only
	ContactModeTest                    // Show message preview, don't send
	ContactModeOn                      // Actually send contacts
)

// Controller holds shared bot state and turns chat commands into responses.
// It is safe for concurrent use.
type Controller struct {
	mu          sync.RWMutex
	contactMode ContactMode
	quietHours  bool

	// Callbacks providing extra info for /status and /stats.
	onStatusRequest func() string
	onStatsRequest  func() string

	// Callbacks for managing search profiles (need DB access, injected by main).
	onAddProfile   func(url, name string) string
	onListProfiles func() string
	onDelProfile   func(id string) string
}

// New creates a controller with the default state: auto-contact on, quiet hours on.
func New() *Controller {
	return &Controller{
		contactMode: ContactModeOn,
		quietHours:  true,
	}
}

// SetCallbacks sets the callback functions for status and stats details.
func (c *Controller) SetCallbacks(onStatus, onStats func() string) {
	c.onStatusRequest = onStatus
	c.onStatsRequest = onStats
}

// SetProfileCallbacks wires the search-profile management commands.
func (c *Controller) SetProfileCallbacks(onAdd func(url, name string) string, onList func() string, onDel func(id string) string) {
	c.onAddProfile = onAdd
	c.onListProfiles = onList
	c.onDelProfile = onDel
}

// HandleCommand normalizes a raw chat message and returns the response text.
// Accepts both slash and plain forms: "/contact_on", "contact on", "Status".
// Returns "" if the message is not a recognized command (caller may ignore it).
func (c *Controller) HandleCommand(raw string) string {
	// Argument commands are routed by their first token so the rest (URL, id,
	// name) is preserved. No-arg commands fall through to normalizeCommand.
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
	case "quiet_on":
		c.SetQuietHours(true)
		return "🌙 *Ruhezeiten aktiviert*\n\nBot pausiert zwischen 22:00-07:00 Uhr."
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

// handleAddProfile validates the URL argument and delegates creation to the
// injected callback. Usage: addprofil <url> [name...].
func (c *Controller) handleAddProfile(args []string) string {
	if len(args) == 0 || !looksLikeURL(args[0]) {
		return "Nutzung: /addprofil <IS24-Such-URL> [Name]\n\nErst auf immobilienscout24.de die Suche bauen, dann die URL hierher kopieren."
	}
	if c.onAddProfile == nil {
		return "Profil-Verwaltung nicht verfügbar."
	}
	url := args[0]
	name := strings.TrimSpace(strings.Join(args[1:], " "))
	return c.onAddProfile(url, name)
}

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func (c *Controller) helpMessage() string {
	return `🏠 *ImmoBot Befehle*

*Kontakt:*
/contact_on - Auto-Kontakt aktivieren
/contact_test - Test-Modus (Nachricht-Vorschau)
/contact_off - Nur beobachten (kein Kontakt)

*Ruhezeiten:*
/quiet_on - Ruhezeiten an (22:00-07:00)
/quiet_off - Ruhezeiten aus (24/7)

*Suchprofile:*
/addprofil <URL> [Name] - Profil aus IS24-Such-URL anlegen
/listprofile - Aktive Profile anzeigen
/delprofil <id> - Profil deaktivieren

*Info:*
/status - Aktueller Bot-Status
/stats - Statistiken anzeigen
/help - Diese Hilfe`
}

func (c *Controller) statusMessage() string {
	c.mu.RLock()
	contactMode := c.contactMode
	quietHours := c.quietHours
	c.mu.RUnlock()

	var mode string
	switch contactMode {
	case ContactModeOff:
		mode = "⏸ Beobachtungsmodus"
	case ContactModeTest:
		mode = "🧪 Test-Modus (Nachricht-Vorschau)"
	case ContactModeOn:
		mode = "✅ Auto-Kontakt aktiv"
	}

	quietStatus := "☀️ Aus (24/7)"
	if quietHours {
		quietStatus = "🌙 An (22:00-07:00)"
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

// IsQuietHoursEnabled returns a pointer to the quiet-hours override flag.
func (c *Controller) IsQuietHoursEnabled() *bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := c.quietHours
	return &v
}

// GetContactMode returns the current contact mode.
func (c *Controller) GetContactMode() ContactMode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactMode
}

// SetContactMode sets the contact mode.
func (c *Controller) SetContactMode(mode ContactMode) {
	c.mu.Lock()
	c.contactMode = mode
	c.mu.Unlock()
}

// SetQuietHours enables or disables quiet hours.
func (c *Controller) SetQuietHours(enabled bool) {
	c.mu.Lock()
	c.quietHours = enabled
	c.mu.Unlock()
}
