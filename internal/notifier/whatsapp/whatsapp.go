// Package whatsapp implements a notification + command channel over WhatsApp
// using whatsmeow (a linked-device client, no Meta Business account required).
//
// It satisfies the notifier.Notifier interface and forwards incoming chat
// commands to a shared control.Controller. Only the configured target number
// is allowed to issue commands — a linked device receives every chat, so this
// check is the security boundary.
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	// modernc.org/sqlite registers itself as "sqlite"; whatsmeow opens its store
	// via sql.Open("sqlite3", ...), so register the same pure-Go driver under
	// that name too. Keeps the build CGO-free (no mattn/go-sqlite3).
	sqlited "modernc.org/sqlite"

	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/control"
	"github.com/julianbeese/immo_bot/internal/domain"
)

func init() {
	sql.Register("sqlite3", &sqlited.Driver{})
}

// Client is a WhatsApp notification + command channel.
type Client struct {
	wa      *whatsmeow.Client
	target  types.JID // notifications go here; its number is the only authorized commander
	ctrl    *control.Controller
	logger  *slog.Logger
	enabled bool
	ctx     context.Context // connection context, used by the event handler for replies
}

// New builds a WhatsApp client. If cfg.Enabled is false it returns a disabled
// client whose Notifier methods are no-ops. Connect must be called separately.
func New(ctx context.Context, cfg config.WhatsAppConfig, ctrl *control.Controller, logger *slog.Logger) (*Client, error) {
	if !cfg.Enabled {
		return &Client{enabled: false, logger: logger}, nil
	}
	if cfg.TargetPhone == "" {
		return nil, fmt.Errorf("whatsapp enabled but target_phone is empty")
	}

	level := cfg.LogLevel
	if level == "" {
		level = "INFO"
	}
	waLogger := waLog.Stdout("WhatsApp", level, true)

	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", cfg.StorePath)
	container, err := sqlstore.New(ctx, "sqlite3", dsn, waLogger)
	if err != nil {
		return nil, fmt.Errorf("open whatsapp store: %w", err)
	}

	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	wa := whatsmeow.NewClient(device, waLogger)

	return &Client{
		wa:      wa,
		target:  types.NewJID(onlyDigits(cfg.TargetPhone), types.DefaultUserServer),
		ctrl:    ctrl,
		logger:  logger,
		enabled: true,
	}, nil
}

// Connect logs in (showing a QR code on first run) and starts handling commands.
func (c *Client) Connect(ctx context.Context) error {
	if !c.enabled {
		return nil
	}

	c.ctx = ctx
	c.wa.AddEventHandler(c.handleEvent)

	if err := c.wa.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if c.wa.Store.ID == nil {
		// Not linked yet: use phone-number pairing (more reliable than scanning a
		// terminal QR). whatsmeow returns an 8-char code to enter in WhatsApp.
		// The display name must be a real "Browser (OS)" string — WhatsApp
		// validates it server-side and returns 400 bad-request otherwise
		// (e.g. "ImmoBot" is rejected).
		code, err := c.wa.PairPhone(ctx, c.target.User, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		if err != nil {
			return fmt.Errorf("pair phone: %w", err)
		}
		fmt.Printf("\n=== WhatsApp koppeln ===\n"+
			"WhatsApp am Handy (Nummer +%s): Einstellungen → Verknüpfte Geräte →\n"+
			"\"Gerät verknüpfen\" → \"Stattdessen mit Telefonnummer verknüpfen\" →\n"+
			"diesen Code eingeben:\n\n    %s\n\n"+
			"(Code läuft nach kurzer Zeit ab — bei Bedarf Bot neu starten.)\n\n", c.target.User, code)
		c.logger.Info("WhatsApp pairing code generated", "target", c.target.User)
	}

	c.logger.Info("WhatsApp connected", "target", c.target.User)
	return nil
}

// Disconnect closes the WhatsApp connection.
func (c *Client) Disconnect() {
	if c.enabled && c.wa != nil {
		c.wa.Disconnect()
	}
}

// handleEvent processes incoming messages and runs authorized commands.
func (c *Client) handleEvent(evt any) {
	msg, ok := evt.(*events.Message)
	if !ok || msg.Info.IsFromMe {
		return
	}

	// Security: only the configured number may control the bot.
	if msg.Info.Sender.ToNonAD().User != c.target.User {
		return
	}

	text := messageText(msg.Message)
	if text == "" {
		return
	}

	response := c.ctrl.HandleCommand(text)
	if response == "" {
		return
	}

	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.send(ctx, msg.Info.Chat, response); err != nil {
		c.logger.Error("whatsapp command reply failed", "error", err)
	}
}

// messageText extracts plain text from a WhatsApp message (plain or extended).
func messageText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if t := m.GetConversation(); t != "" {
		return t
	}
	return m.GetExtendedTextMessage().GetText()
}

func (c *Client) send(ctx context.Context, to types.JID, text string) error {
	_, err := c.wa.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(text)})
	return err
}

// --- notifier.Notifier implementation ---

func (c *Client) NotifyNewListing(ctx context.Context, l *domain.Listing) error {
	if !c.enabled {
		return nil
	}
	return c.send(ctx, c.target, formatListing(l))
}

func (c *Client) NotifyContactSent(ctx context.Context, l *domain.Listing) error {
	if !c.enabled {
		return nil
	}
	text := fmt.Sprintf("✅ *Kontaktanfrage gesendet*\n\n*%s*\n📍 %s\n🔗 %s",
		l.Title, l.Address, l.URL)
	return c.send(ctx, c.target, text)
}

func (c *Client) NotifyContactFailed(ctx context.Context, l *domain.Listing, errMsg string) error {
	if !c.enabled {
		return nil
	}
	text := fmt.Sprintf("❌ *Kontaktanfrage fehlgeschlagen*\n\n*%s*\n📍 %s\n🔗 %s\n\n*Fehler:* %s",
		l.Title, l.Address, l.URL, errMsg)
	return c.send(ctx, c.target, text)
}

func (c *Client) NotifyError(ctx context.Context, errMsg string) error {
	if !c.enabled {
		return nil
	}
	return c.send(ctx, c.target, fmt.Sprintf("⚠️ *Bot-Fehler*\n\n%s", errMsg))
}

func (c *Client) NotifyMessagePreview(ctx context.Context, l *domain.Listing, message string) error {
	if !c.enabled {
		return nil
	}
	text := fmt.Sprintf("🧪 *Test-Modus: Nachricht-Vorschau*\n\n*Wohnung:* %s\n📍 %s\n💰 %d € | 🚪 %.1f Zimmer\n🔗 %s\n\n*━━━ Nachricht ━━━*\n\n%s",
		l.Title, l.Address, l.Price, l.Rooms, l.URL, message)
	return c.send(ctx, c.target, text)
}

func (c *Client) SendRawMessage(ctx context.Context, text string) error {
	if !c.enabled {
		return nil
	}
	return c.send(ctx, c.target, text)
}

func (c *Client) IsEnabled() bool {
	return c.enabled
}

// formatListing renders a listing in WhatsApp markup (*bold*, no HTML/buttons).
func formatListing(l *domain.Listing) string {
	var sb strings.Builder
	sb.WriteString("🏠 *Neue Wohnung gefunden!*\n\n")
	sb.WriteString(fmt.Sprintf("*%s*\n\n", l.Title))

	switch {
	case l.Address != "":
		sb.WriteString(fmt.Sprintf("📍 %s\n", l.Address))
	case l.District != "" && l.City != "":
		sb.WriteString(fmt.Sprintf("📍 %s, %s\n", l.District, l.City))
	case l.City != "":
		sb.WriteString(fmt.Sprintf("📍 %s\n", l.City))
	}
	sb.WriteString("\n")

	if l.Price > 0 {
		sb.WriteString(fmt.Sprintf("💰 *%d €* Kaltmiete\n", l.Price))
	}
	if l.Rooms > 0 {
		sb.WriteString(fmt.Sprintf("🚪 %.1f Zimmer\n", l.Rooms))
	}
	if l.Area > 0 {
		sb.WriteString(fmt.Sprintf("📐 %d m²\n", l.Area))
	}

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

	if l.AvailableFrom != "" {
		sb.WriteString(fmt.Sprintf("📅 Ab %s\n", l.AvailableFrom))
	}
	if l.LandlordName != "" {
		sb.WriteString(fmt.Sprintf("\n👤 %s", l.LandlordName))
		if l.LandlordType != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", l.LandlordType))
		}
		sb.WriteString("\n")
	}

	if l.URL != "" {
		sb.WriteString(fmt.Sprintf("\n🔗 %s", l.URL))
	}

	return sb.String()
}

// onlyDigits strips everything but 0-9 from a phone number (handles "+49 151 …").
func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
