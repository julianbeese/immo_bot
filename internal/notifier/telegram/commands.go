package telegram

import (
	"context"
	"fmt"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ContactMode represents the contact behavior mode
type ContactMode int

const (
	ContactModeOff  ContactMode = iota // Observation only
	ContactModeTest                    // Show message preview, don't send
	ContactModeOn                      // Actually send contacts
)

// BotController handles Telegram commands and controls bot state
type BotController struct {
	bot           *tgbotapi.BotAPI
	chatID        int64
	enabled       bool

	mu          sync.RWMutex
	contactMode ContactMode
	quietHours  bool

	// Callbacks
	onStatusRequest func() string
	onStatsRequest  func() string
}

// NewBotController creates a new bot controller with command handling
func NewBotController(botToken string, chatID int64, enabled bool) (*BotController, error) {
	if !enabled || botToken == "" {
		return &BotController{enabled: false, contactMode: ContactModeOff}, nil
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}

	return &BotController{
		bot:         bot,
		chatID:      chatID,
		enabled:     true,
		contactMode: ContactModeOff, // Start in observation mode
	}, nil
}

// SetCallbacks sets the callback functions for status and stats
func (c *BotController) SetCallbacks(onStatus, onStats func() string) {
	c.onStatusRequest = onStatus
	c.onStatsRequest = onStats
}

// StartCommandListener starts listening for Telegram commands
func (c *BotController) StartCommandListener(ctx context.Context) {
	if !c.enabled {
		return
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := c.bot.GetUpdatesChan(u)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-updates:
				if update.Message == nil || !update.Message.IsCommand() {
					continue
				}

				// Only respond to authorized chat
				if update.Message.Chat.ID != c.chatID {
					continue
				}

				c.handleCommand(update.Message)
			}
		}
	}()
}

func (c *BotController) handleCommand(msg *tgbotapi.Message) {
	var response string

	switch msg.Command() {
	case "start", "help":
		response = c.helpMessage()
	case "status":
		response = c.statusMessage()
	case "contact_on":
		c.SetContactMode(ContactModeOn)
		response = "✅ <b>Auto-Kontakt aktiviert</b>\n\nNeue Wohnungen werden automatisch angeschrieben."
	case "contact_off":
		c.SetContactMode(ContactModeOff)
		response = "⏸ <b>Beobachtungsmodus</b>\n\nNeue Wohnungen werden nur gemeldet, keine Kontaktaufnahme."
	case "contact_test":
		c.SetContactMode(ContactModeTest)
		response = "🧪 <b>Test-Modus aktiviert</b>\n\nNeue Wohnungen werden gemeldet und die Nachricht wird dir als Vorschau gezeigt (nicht gesendet)."
	case "quiet_on":
		c.SetQuietHours(true)
		response = "🌙 <b>Ruhezeiten aktiviert</b>\n\nBot pausiert zwischen 22:00-07:00 Uhr."
	case "quiet_off":
		c.SetQuietHours(false)
		response = "☀️ <b>Ruhezeiten deaktiviert</b>\n\nBot läuft rund um die Uhr."
	case "stats":
		if c.onStatsRequest != nil {
			response = c.onStatsRequest()
		} else {
			response = "Statistiken nicht verfügbar."
		}
	default:
		response = "Unbekannter Befehl. Nutze /help für eine Übersicht."
	}

	reply := tgbotapi.NewMessage(c.chatID, response)
	reply.ParseMode = tgbotapi.ModeHTML
	c.bot.Send(reply)
}

func (c *BotController) helpMessage() string {
	return `🏠 <b>ImmoBot Befehle</b>

<b>Kontakt:</b>
/contact_on - Auto-Kontakt aktivieren
/contact_test - Test-Modus (Nachricht-Vorschau)
/contact_off - Nur beobachten (kein Kontakt)

<b>Ruhezeiten:</b>
/quiet_on - Ruhezeiten an (22:00-07:00)
/quiet_off - Ruhezeiten aus (24/7)

<b>Info:</b>
/status - Aktueller Bot-Status
/stats - Statistiken anzeigen
/help - Diese Hilfe`
}

func (c *BotController) statusMessage() string {
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

	status := fmt.Sprintf(`🏠 <b>ImmoBot Status</b>

<b>Kontakt:</b> %s
<b>Ruhezeiten:</b> %s

Befehle: /help für alle Optionen`, mode, quietStatus)

	if c.onStatusRequest != nil {
		status += "\n\n" + c.onStatusRequest()
	}

	return status
}

// IsAutoContactEnabled returns whether auto-contact is enabled (actually sends messages)
func (c *BotController) IsAutoContactEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactMode == ContactModeOn
}

// IsTestModeEnabled returns whether test mode is enabled (shows message preview)
func (c *BotController) IsTestModeEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactMode == ContactModeTest
}

// GetContactMode returns the current contact mode
func (c *BotController) GetContactMode() ContactMode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contactMode
}

// SetContactMode sets the contact mode
func (c *BotController) SetContactMode(mode ContactMode) {
	c.mu.Lock()
	c.contactMode = mode
	c.mu.Unlock()
}

// IsQuietHoursEnabled returns whether quiet hours override is enabled
func (c *BotController) IsQuietHoursEnabled() *bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Return nil if not explicitly set (use config default)
	return &c.quietHours
}

// SetQuietHours enables or disables quiet hours
func (c *BotController) SetQuietHours(enabled bool) {
	c.mu.Lock()
	c.quietHours = enabled
	c.mu.Unlock()
}

// GetBot returns the underlying bot API for notifications
func (c *BotController) GetBot() *tgbotapi.BotAPI {
	return c.bot
}

// GetChatID returns the configured chat ID
func (c *BotController) GetChatID() int64 {
	return c.chatID
}

// IsEnabled returns whether the controller is enabled
func (c *BotController) IsEnabled() bool {
	return c.enabled
}
