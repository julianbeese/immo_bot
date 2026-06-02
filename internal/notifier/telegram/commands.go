package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/julianbeese/immo_bot/internal/control"
)

// ApprovalHandler receives ✅/❌ decisions for an approval card. The scheduler
// implements both methods; this interface keeps the Telegram transport free of
// direct scheduler/repo dependencies.
type ApprovalHandler interface {
	OnApprove(ctx context.Context, sentMessageID int64) error
	OnReject(ctx context.Context, sentMessageID int64) error
}

// BotController handles Telegram commands. State and command logic live in
// control.Controller; this type is just the Telegram transport for it.
type BotController struct {
	bot      *tgbotapi.BotAPI
	chatID   int64
	enabled  bool
	ctrl     *control.Controller
	approval ApprovalHandler
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

// SetApprovalHandler wires the ✅/❌ button callbacks into the scheduler.
// Calling with nil disables approval handling (buttons become no-ops).
func (c *BotController) SetApprovalHandler(h ApprovalHandler) {
	c.approval = h
}

// StartCommandListener starts listening for Telegram commands and inline
// button callbacks (approval mode). Both arrive on the same update channel.
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
				if update.CallbackQuery != nil {
					if update.CallbackQuery.From.ID != c.chatID && update.CallbackQuery.Message.Chat.ID != c.chatID {
						continue
					}
					c.handleCallback(ctx, update.CallbackQuery)
					continue
				}
				if update.Message == nil || !update.Message.IsCommand() {
					continue
				}
				if update.Message.Chat.ID != c.chatID {
					continue
				}
				c.handleCommand(update.Message)
			}
		}
	}()
}

// handleCallback dispatches an approval inline-button press to the scheduler.
// Callback_data shape: "approve:<sentMessageID>" or "reject:<id>". Anything
// else is acknowledged silently so unknown buttons don't spin the spinner.
func (c *BotController) handleCallback(ctx context.Context, q *tgbotapi.CallbackQuery) {
	// Always answer the callback so the Telegram client's loading spinner
	// stops — even on error, we just put the reason into the toast.
	ack := func(text string) {
		cb := tgbotapi.NewCallback(q.ID, text)
		cb.ShowAlert = false
		c.bot.Request(cb)
	}

	if c.approval == nil {
		ack("Approval-Handler nicht konfiguriert.")
		return
	}

	parts := strings.SplitN(q.Data, ":", 2)
	if len(parts) != 2 {
		ack("Ungültige Aktion.")
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		ack("Ungültige ID.")
		return
	}

	switch parts[0] {
	case "approve":
		if err := c.approval.OnApprove(ctx, id); err != nil {
			ack("Fehler: " + err.Error())
			return
		}
		ack("✅ Wird gesendet…")
	case "reject":
		if err := c.approval.OnReject(ctx, id); err != nil {
			ack("Fehler: " + err.Error())
			return
		}
		ack("❌ Verworfen.")
	default:
		ack("Unbekannte Aktion.")
		return
	}

	// Drop the inline keyboard so the card visibly transitions to "done" —
	// editing the text would clobber the HTML formatting (Telegram strips it
	// from CallbackQuery.Message.Text).
	clear := tgbotapi.NewEditMessageReplyMarkup(
		q.Message.Chat.ID, q.Message.MessageID,
		tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}},
	)
	c.bot.Send(clear)
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
