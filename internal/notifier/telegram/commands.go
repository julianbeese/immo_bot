package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/julianbeese/immo_bot/internal/control"
)

// BotController handles Telegram commands. State and command logic live in
// control.Controller; this type is just the Telegram transport for it.
type BotController struct {
	bot     *tgbotapi.BotAPI
	chatID  int64
	enabled bool
	ctrl    *control.Controller
}

// NewBotController creates a new bot controller wired to the shared controller.
func NewBotController(botToken string, chatID int64, enabled bool, ctrl *control.Controller) (*BotController, error) {
	if !enabled || botToken == "" {
		return &BotController{enabled: false, ctrl: ctrl}, nil
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}

	return &BotController{
		bot:     bot,
		chatID:  chatID,
		enabled: true,
		ctrl:    ctrl,
	}, nil
}

// StartCommandListener starts listening for Telegram commands.
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
	response := c.ctrl.HandleCommand(msg.Text)
	if response == "" {
		return
	}

	reply := tgbotapi.NewMessage(c.chatID, markupToHTML(response))
	reply.ParseMode = tgbotapi.ModeHTML
	c.bot.Send(reply)
}

// markupToHTML converts the controller's WhatsApp-style *bold* markup into the
// Telegram HTML used elsewhere in this package.
func markupToHTML(s string) string {
	s = escapeHTML(s)
	var sb strings.Builder
	open := false
	for _, r := range s {
		if r == '*' {
			if open {
				sb.WriteString("</b>")
			} else {
				sb.WriteString("<b>")
			}
			open = !open
			continue
		}
		sb.WriteRune(r)
	}
	if open { // unbalanced marker: close it to keep valid HTML
		sb.WriteString("</b>")
	}
	return sb.String()
}

// GetBot returns the underlying bot API for notifications.
func (c *BotController) GetBot() *tgbotapi.BotAPI {
	return c.bot
}

// GetChatID returns the configured chat ID.
func (c *BotController) GetChatID() int64 {
	return c.chatID
}

// IsEnabled returns whether the controller is enabled.
func (c *BotController) IsEnabled() bool {
	return c.enabled
}
