package telegram

import (
	"context"
	"fmt"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// BotController handles Telegram commands and controls bot state
type BotController struct {
	bot           *tgbotapi.BotAPI
	chatID        int64
	enabled       bool

	mu            sync.RWMutex
	autoContact   bool

	// Callbacks
	onStatusRequest func() string
	onStatsRequest  func() string
}

// NewBotController creates a new bot controller with command handling
func NewBotController(botToken string, chatID int64, enabled bool) (*BotController, error) {
	if !enabled || botToken == "" {
		return &BotController{enabled: false, autoContact: false}, nil
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}

	return &BotController{
		bot:         bot,
		chatID:      chatID,
		enabled:     true,
		autoContact: false, // Start in observation mode
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
		c.SetAutoContact(true)
		response = "✅ <b>Auto-Kontakt aktiviert</b>\n\nNeue Wohnungen werden automatisch angeschrieben."
	case "contact_off":
		c.SetAutoContact(false)
		response = "⏸ <b>Auto-Kontakt deaktiviert</b>\n\nBeobachtungsmodus aktiv. Neue Wohnungen werden nur gemeldet."
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

/status - Aktueller Bot-Status
/contact_on - Auto-Kontakt aktivieren
/contact_off - Nur beobachten (kein Kontakt)
/stats - Statistiken anzeigen
/help - Diese Hilfe`
}

func (c *BotController) statusMessage() string {
	c.mu.RLock()
	autoContact := c.autoContact
	c.mu.RUnlock()

	mode := "⏸ Beobachtungsmodus"
	if autoContact {
		mode = "✅ Auto-Kontakt aktiv"
	}

	status := fmt.Sprintf(`🏠 <b>ImmoBot Status</b>

<b>Modus:</b> %s

Nutze /contact_on oder /contact_off um den Modus zu wechseln.`, mode)

	if c.onStatusRequest != nil {
		status += "\n\n" + c.onStatusRequest()
	}

	return status
}

// IsAutoContactEnabled returns whether auto-contact is enabled
func (c *BotController) IsAutoContactEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.autoContact
}

// SetAutoContact enables or disables auto-contact
func (c *BotController) SetAutoContact(enabled bool) {
	c.mu.Lock()
	c.autoContact = enabled
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
