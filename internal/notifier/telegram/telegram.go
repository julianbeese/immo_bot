package telegram

import (
	"context"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/julianbeese/immo_bot/internal/domain"
)

// Notifier sends messages via Telegram
type Notifier struct {
	bot     *tgbotapi.BotAPI
	chatID  int64
	enabled bool
}

// NewNotifier creates a new Telegram notifier
func NewNotifier(botToken string, chatID int64, enabled bool) (*Notifier, error) {
	if !enabled || botToken == "" {
		return &Notifier{enabled: false}, nil
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}

	return &Notifier{
		bot:     bot,
		chatID:  chatID,
		enabled: true,
	}, nil
}

// NewNotifierFromController creates a notifier using an existing BotController
func NewNotifierFromController(controller *BotController) *Notifier {
	if controller == nil || !controller.IsEnabled() {
		return &Notifier{enabled: false}
	}
	return &Notifier{
		bot:     controller.GetBot(),
		chatID:  controller.GetChatID(),
		enabled: true,
	}
}

// NotifyNewListing sends a notification about a new listing
func (n *Notifier) NotifyNewListing(ctx context.Context, listing *domain.Listing) error {
	if !n.enabled {
		return nil
	}

	text := n.formatListing(listing)

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = false

	// Add inline keyboard with link to listing
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔗 Auf IS24 ansehen", listing.URL),
		),
	)
	msg.ReplyMarkup = keyboard

	_, err := n.bot.Send(msg)
	return err
}

// NotifyContactSent sends a confirmation that contact was sent
func (n *Notifier) NotifyContactSent(ctx context.Context, listing *domain.Listing) error {
	if !n.enabled {
		return nil
	}

	text := fmt.Sprintf(
		"✅ <b>Kontaktanfrage gesendet</b>\n\n"+
		"<b>%s</b>\n"+
		"📍 %s\n"+
		"🔗 %s",
		escapeHTML(listing.Title),
		escapeHTML(listing.Address),
		listing.URL,
	)

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	_, err := n.bot.Send(msg)
	return err
}

// NotifyContactFailed sends a notification that contact attempt failed
func (n *Notifier) NotifyContactFailed(ctx context.Context, listing *domain.Listing, errMsg string) error {
	if !n.enabled {
		return nil
	}

	text := fmt.Sprintf(
		"❌ <b>Kontaktanfrage fehlgeschlagen</b>\n\n"+
		"<b>%s</b>\n"+
		"📍 %s\n"+
		"🔗 %s\n\n"+
		"<b>Fehler:</b> %s",
		escapeHTML(listing.Title),
		escapeHTML(listing.Address),
		listing.URL,
		escapeHTML(errMsg),
	)

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	_, err := n.bot.Send(msg)
	return err
}

// NotifyError sends an error notification to the admin
func (n *Notifier) NotifyError(ctx context.Context, errMsg string) error {
	if !n.enabled {
		return nil
	}

	text := fmt.Sprintf("⚠️ <b>Bot-Fehler</b>\n\n%s", escapeHTML(errMsg))

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	_, err := n.bot.Send(msg)
	return err
}

// NotifyStartup sends a notification that the bot has started
func (n *Notifier) NotifyStartup(ctx context.Context, profileCount int) error {
	if !n.enabled {
		return nil
	}

	text := fmt.Sprintf(
		"🚀 <b>ImmoBot gestartet</b>\n\n"+
		"Aktive Suchprofile: %d",
		profileCount,
	)

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	_, err := n.bot.Send(msg)
	return err
}

// formatListing creates a formatted message for a listing
func (n *Notifier) formatListing(l *domain.Listing) string {
	var sb strings.Builder

	sb.WriteString("🏠 <b>Neue Wohnung gefunden!</b>\n\n")
	sb.WriteString(fmt.Sprintf("<b>%s</b>\n\n", escapeHTML(l.Title)))

	// Location
	if l.Address != "" {
		sb.WriteString(fmt.Sprintf("📍 %s\n", escapeHTML(l.Address)))
	} else if l.District != "" && l.City != "" {
		sb.WriteString(fmt.Sprintf("📍 %s, %s\n", escapeHTML(l.District), escapeHTML(l.City)))
	} else if l.City != "" {
		sb.WriteString(fmt.Sprintf("📍 %s\n", escapeHTML(l.City)))
	}

	sb.WriteString("\n")

	// Key facts
	if l.Price > 0 {
		sb.WriteString(fmt.Sprintf("💰 <b>%d €</b> Kaltmiete\n", l.Price))
	}
	if l.Rooms > 0 {
		sb.WriteString(fmt.Sprintf("🚪 %.1f Zimmer\n", l.Rooms))
	}
	if l.Area > 0 {
		sb.WriteString(fmt.Sprintf("📐 %d m²\n", l.Area))
	}

	// Features
	var features []string
	if l.HasBalcony {
		features = append(features, "Balkon")
	}
	if l.HasEBK {
		features = append(features, "EBK")
	}
	if l.HasElevator {
		features = append(features, "Aufzug")
	}
	if len(features) > 0 {
		sb.WriteString(fmt.Sprintf("✨ %s\n", strings.Join(features, ", ")))
	}

	// Available from
	if l.AvailableFrom != "" {
		sb.WriteString(fmt.Sprintf("📅 Ab %s\n", escapeHTML(l.AvailableFrom)))
	}

	// Landlord
	if l.LandlordName != "" {
		sb.WriteString(fmt.Sprintf("\n👤 %s", escapeHTML(l.LandlordName)))
		if l.LandlordType != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", escapeHTML(l.LandlordType)))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// escapeHTML escapes HTML special characters for Telegram
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// IsEnabled returns whether the notifier is enabled
func (n *Notifier) IsEnabled() bool {
	return n.enabled
}

// SendRawMessage sends a raw HTML message
func (n *Notifier) SendRawMessage(ctx context.Context, text string) error {
	if !n.enabled {
		return nil
	}

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	_, err := n.bot.Send(msg)
	return err
}

// NotifyMessagePreview sends a preview of the message that would be sent to a listing
func (n *Notifier) NotifyMessagePreview(ctx context.Context, listing *domain.Listing, message string) error {
	if !n.enabled {
		return nil
	}

	text := fmt.Sprintf(
		"🧪 <b>Test-Modus: Nachricht-Vorschau</b>\n\n"+
			"<b>Wohnung:</b> %s\n"+
			"📍 %s\n"+
			"💰 %d € | 🚪 %.1f Zimmer\n"+
			"🔗 %s\n\n"+
			"<b>━━━ Nachricht ━━━</b>\n\n"+
			"<pre>%s</pre>",
		escapeHTML(listing.Title),
		escapeHTML(listing.Address),
		listing.Price,
		listing.Rooms,
		listing.URL,
		escapeHTML(message),
	)

	msg := tgbotapi.NewMessage(n.chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML

	_, err := n.bot.Send(msg)
	return err
}
