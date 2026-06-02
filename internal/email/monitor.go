package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// Classification is the AI verdict for one IS24 mail.
type Classification struct {
	IsLandlordReply bool   `json:"is_landlord_reply"` // genuine message from a provider/landlord (not a system/marketing mail)
	IS24ID          string `json:"is24_id"`           // listing id extracted from the mail, if any
	Summary         string `json:"summary"`           // one-sentence German summary
	Reason          string `json:"reason"`            // why it was (not) classified as a reply
}

// Classifier turns a fetched mail into a Classification (LLM-backed).
type Classifier interface {
	Classify(ctx context.Context, m Message) (Classification, error)
}

// Store persists inbox messages and resolves listings. Implemented by the
// sqlite repository.
type Store interface {
	InboxExists(ctx context.Context, messageID string) (bool, error)
	CreateInboxMessage(ctx context.Context, m *domain.InboxMessage) error
	GetListingByIS24ID(ctx context.Context, is24ID string) (*domain.Listing, error)
	GetMeta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
}

// Notifier pushes a formatted text alert (Telegram/WhatsApp via the bot's
// notifier.Multi).
type Notifier interface {
	SendRawMessage(ctx context.Context, text string) error
}

// metaLastUIDPrefix + mailbox names the watermark meta key.
const metaLastUIDPrefix = "email.last_uid."

// Monitor ties IMAP fetching, AI classification, persistence and notification
// together. Construct via NewMonitor and call Poll once per cycle.
type Monitor struct {
	client     *Client
	classifier Classifier
	store      Store
	notifier   Notifier
	logger     *slog.Logger
}

// NewMonitor builds a Monitor. notifier may be nil (then alerts are skipped).
func NewMonitor(client *Client, classifier Classifier, store Store, notifier Notifier, logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{client: client, classifier: classifier, store: store, notifier: notifier, logger: logger}
}

// Poll fetches new IS24 mails since the stored UID watermark, classifies the
// ones not seen before, persists them, and notifies on genuine landlord
// replies. It is safe to call every poll cycle; Message-ID dedupe guards the
// (paid) classifier so it only runs on truly new mail.
func (m *Monitor) Poll(ctx context.Context) error {
	afterUID := m.loadWatermark(ctx)

	msgs, highUID, err := m.client.FetchSince(ctx, afterUID)
	if err != nil {
		return fmt.Errorf("fetch mails: %w", err)
	}

	for _, msg := range msgs {
		if err := m.process(ctx, msg); err != nil {
			m.logger.Error("inbox message processing failed", "message_id", msg.MessageID, "error", err)
			// keep going; one bad mail shouldn't block the rest
		}
	}

	// Advance the watermark only after processing so a mid-cycle crash re-reads
	// unprocessed mail (Message-ID dedupe prevents duplicates).
	if highUID > afterUID {
		if err := m.store.SetMeta(ctx, m.watermarkKey(), fmt.Sprintf("%d", highUID)); err != nil {
			m.logger.Warn("failed to persist email UID watermark", "error", err)
		}
	}
	return nil
}

func (m *Monitor) process(ctx context.Context, msg Message) error {
	if msg.MessageID != "" {
		exists, err := m.store.InboxExists(ctx, msg.MessageID)
		if err != nil {
			return fmt.Errorf("dedupe check: %w", err)
		}
		if exists {
			return nil
		}
	}

	cls, err := m.classifier.Classify(ctx, msg)
	if err != nil {
		return fmt.Errorf("classify: %w", err)
	}

	rec := &domain.InboxMessage{
		MessageID:       msg.MessageID,
		FromAddr:        msg.From,
		Subject:         msg.Subject,
		Snippet:         snippet(msg.Body, 280),
		IS24ID:          cls.IS24ID,
		IsLandlordReply: cls.IsLandlordReply,
		Summary:         cls.Summary,
		ReceivedAt:      msg.Date,
	}

	// Link to a known listing when the AI extracted an IS24 id we have on file.
	if cls.IS24ID != "" {
		if l, err := m.store.GetListingByIS24ID(ctx, cls.IS24ID); err == nil && l != nil {
			rec.ListingID = l.ID
		}
	}

	// Notify before persisting so the stored row reflects whether the alert went
	// out. Only genuine landlord replies trigger a push.
	if cls.IsLandlordReply && m.notifier != nil {
		if err := m.notifier.SendRawMessage(ctx, formatAlert(rec)); err != nil {
			m.logger.Warn("inbox notification failed", "error", err)
		} else {
			rec.Notified = true
		}
	}

	if err := m.store.CreateInboxMessage(ctx, rec); err != nil {
		return fmt.Errorf("persist: %w", err)
	}

	m.logger.Info("inbox message processed",
		"from", msg.From, "landlord_reply", cls.IsLandlordReply, "is24_id", cls.IS24ID)
	return nil
}

func (m *Monitor) watermarkKey() string { return metaLastUIDPrefix + m.client.mailbox }

func (m *Monitor) loadWatermark(ctx context.Context) uint32 {
	v, err := m.store.GetMeta(ctx, m.watermarkKey())
	if err != nil || v == "" {
		return 0
	}
	var u uint32
	_, _ = fmt.Sscanf(v, "%d", &u)
	return u
}

// formatAlert renders the Telegram/WhatsApp alert in the shared *bold* markup.
func formatAlert(r *domain.InboxMessage) string {
	var b strings.Builder
	b.WriteString("📬 *Neue Anbieter-Nachricht (E-Mail)*\n\n")
	b.WriteString("*Von:* " + r.FromAddr + "\n")
	if r.Subject != "" {
		b.WriteString("*Betreff:* " + r.Subject + "\n")
	}
	if r.Summary != "" {
		b.WriteString("\n" + r.Summary + "\n")
	}
	if r.IS24ID != "" {
		b.WriteString("\n🔗 https://www.immobilienscout24.de/expose/" + r.IS24ID + "\n")
	}
	return b.String()
}

func snippet(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
