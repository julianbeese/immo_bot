package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// ScanResult summarizes a single Poll. Returned to dashboard callers so the
// "Jetzt scannen"-Button can show counts instead of a bare ok/error. Inspected
// vs Fetched distinguishes "nothing new in the mailbox" from "lots arrived but
// none matched the configured IS24 sender filter".
type ScanResult struct {
	Inspected       int      `json:"inspected"`        // UIDs > watermark in date window (raw, before sender filter)
	Fetched         int      `json:"fetched"`          // passed the sender filter — these went through classification
	AlreadyKnown    int      `json:"already_known"`    // dedupe hits — classifier skipped
	Classified      int      `json:"classified"`       // new mails sent through the LLM
	LandlordReplies int      `json:"landlord_replies"` // classifier said is_landlord_reply=true
	Notified        int      `json:"notified"`         // Telegram pushes that succeeded
	Senders         []string `json:"senders"`          // active sender substring filter (helps debug Inspected>0 / Fetched=0)
	Errors          []string `json:"errors"`           // per-mail processing errors (truncated)
	DurationMs      int64    `json:"duration_ms"`      // wall-clock for the whole poll
}

// processOutcome is the per-message tally returned by process(); Poll
// aggregates these into a ScanResult.
type processOutcome struct {
	Skipped       bool
	Classified    bool
	LandlordReply bool
	Notified      bool
}

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
// replies. The returned ScanResult is always populated — even on a fetch
// error its DurationMs reflects the time spent. Message-ID dedupe guards the
// (paid) classifier so it only runs on truly new mail.
func (m *Monitor) Poll(ctx context.Context) (res ScanResult, err error) {
	// Named return values let the deferred timing assignment actually reach
	// the caller — a non-named `res` would be copied at each `return res, …`
	// before the defer fires, leaving DurationMs at 0.
	start := time.Now()
	defer func() { res.DurationMs = time.Since(start).Milliseconds() }()

	afterUID := m.loadWatermark(ctx)
	res.Senders = m.client.Senders()

	msgs, inspected, highUID, err := m.client.FetchSince(ctx, afterUID)
	res.Inspected = inspected
	if err != nil {
		return res, fmt.Errorf("fetch mails: %w", err)
	}
	res.Fetched = len(msgs)

	for _, msg := range msgs {
		outcome, perr := m.process(ctx, msg)
		if perr != nil {
			m.logger.Error("inbox message processing failed", "message_id", msg.MessageID, "error", perr)
			res.Errors = append(res.Errors, truncateForDisplay(perr.Error()))
			// keep going; one bad mail shouldn't block the rest
			continue
		}
		switch {
		case outcome.Skipped:
			res.AlreadyKnown++
		case outcome.Classified:
			res.Classified++
			if outcome.LandlordReply {
				res.LandlordReplies++
			}
			if outcome.Notified {
				res.Notified++
			}
		}
	}

	// Advance the watermark only after processing so a mid-cycle crash re-reads
	// unprocessed mail (Message-ID dedupe prevents duplicates).
	if highUID > afterUID {
		if err := m.store.SetMeta(ctx, m.watermarkKey(), fmt.Sprintf("%d", highUID)); err != nil {
			m.logger.Warn("failed to persist email UID watermark", "error", err)
		}
	}
	return res, nil
}

func (m *Monitor) process(ctx context.Context, msg Message) (processOutcome, error) {
	var out processOutcome

	if msg.MessageID != "" {
		exists, err := m.store.InboxExists(ctx, msg.MessageID)
		if err != nil {
			return out, fmt.Errorf("dedupe check: %w", err)
		}
		if exists {
			out.Skipped = true
			return out, nil
		}
	}

	cls, err := m.classifier.Classify(ctx, msg)
	if err != nil {
		return out, fmt.Errorf("classify: %w", err)
	}
	out.Classified = true
	out.LandlordReply = cls.IsLandlordReply

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
			out.Notified = true
		}
	}

	if err := m.store.CreateInboxMessage(ctx, rec); err != nil {
		return out, fmt.Errorf("persist: %w", err)
	}

	m.logger.Info("inbox message processed",
		"from", msg.From, "landlord_reply", cls.IsLandlordReply, "is24_id", cls.IS24ID)
	return out, nil
}

// truncateForDisplay shortens a message to ~200 runes so the dashboard can
// render a per-mail error without an unbounded payload.
func truncateForDisplay(s string) string {
	r := []rune(s)
	if len(r) <= 200 {
		return s
	}
	return string(r[:200]) + "…"
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
