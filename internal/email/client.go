// Package email monitors an IMAP mailbox for ImmobilienScout24-related mails
// and classifies them (via an AI Classifier) to surface genuine replies from
// providers/landlords who answered by email instead of using the IS24 chat.
package email

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"

	// Register additional charset decoders (ISO-8859-1, windows-1252, ...) so
	// non-UTF8 German mail bodies decode correctly.
	_ "github.com/emersion/go-message/charset"
)

// defaultSenderFilters are the From-substrings that mark a mail as IS24-related.
// Lower-cased; matched against the full "name <addr>" sender string.
var defaultSenderFilters = []string{"immobilienscout24", "immoscout24"}

// Message is one fetched mail, body already decoded to plain text.
type Message struct {
	UID       uint32
	MessageID string
	From      string // "Name <addr@host>" or "addr@host"
	Subject   string
	Date      time.Time
	Body      string
}

// Client is a thin IMAP reader scoped to fetching recent IS24 mails.
type Client struct {
	addr     string // host:port, implicit TLS
	username string
	password string
	mailbox  string
	senders  []string
	lookback time.Duration
}

// Config configures the IMAP client.
type Config struct {
	Addr     string // "imap.gmail.com:993"
	Username string
	Password string
	Mailbox  string        // default "INBOX"
	Senders  []string      // From-substring filters; default defaultSenderFilters
	Lookback time.Duration // how far back the coarse SINCE search reaches; default 72h
}

// NewClient builds an IMAP client from cfg, applying defaults.
func NewClient(cfg Config) *Client {
	mbox := cfg.Mailbox
	if mbox == "" {
		mbox = "INBOX"
	}
	senders := cfg.Senders
	if len(senders) == 0 {
		senders = defaultSenderFilters
	}
	lookback := cfg.Lookback
	if lookback <= 0 {
		lookback = 72 * time.Hour
	}
	return &Client{
		addr:     cfg.Addr,
		username: cfg.Username,
		password: cfg.Password,
		mailbox:  mbox,
		senders:  senders,
		lookback: lookback,
	}
}

// FetchSince connects, selects the mailbox read-only, and returns IS24-sender
// mails with UID greater than afterUID (0 = all within the lookback window).
// The returned highUID is the largest UID seen so the caller can persist it as
// the next watermark. inspected is the number of mails in the lookback window
// with UID > afterUID BEFORE the sender filter — exposed so callers can
// distinguish "nothing new at all" from "lots of new mail but none from your
// configured senders". Sender filtering happens client-side so multiple sender
// substrings are supported without IMAP OR-criteria.
func (c *Client) FetchSince(ctx context.Context, afterUID uint32) (msgs []Message, inspected int, highUID uint32, err error) {
	cl, err := imapclient.DialTLS(c.addr, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("imap dial %s: %w", c.addr, err)
	}
	defer cl.Close()

	if err := cl.Login(c.username, c.password).Wait(); err != nil {
		return nil, 0, 0, fmt.Errorf("imap login: %w", err)
	}
	defer cl.Logout()

	if _, err := cl.Select(c.mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, 0, 0, fmt.Errorf("imap select %s: %w", c.mailbox, err)
	}

	// Coarse server-side filter: date only (IMAP SINCE ignores time). Fine
	// filtering by UID + sender happens below.
	criteria := &imap.SearchCriteria{Since: time.Now().Add(-c.lookback)}
	searchData, err := cl.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("imap search: %w", err)
	}

	var uids []imap.UID
	for _, u := range searchData.AllUIDs() {
		if uint32(u) > afterUID {
			uids = append(uids, u)
		}
		if uint32(u) > highUID {
			highUID = uint32(u)
		}
	}
	inspected = len(uids)
	if inspected == 0 {
		return nil, 0, afterUID, nil
	}

	fetchOpts := &imap.FetchOptions{
		Envelope:    true,
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}
	buffers, err := cl.Fetch(imap.UIDSetNum(uids...), fetchOpts).Collect()
	if err != nil {
		return nil, inspected, 0, fmt.Errorf("imap fetch: %w", err)
	}

	for _, b := range buffers {
		if b.Envelope == nil {
			continue
		}
		from := formatAddr(b.Envelope.From)
		if !c.matchesSender(from) {
			continue
		}
		raw := b.FindBodySection(&imap.FetchItemBodySection{})
		body := extractText(raw)
		msgs = append(msgs, Message{
			UID:       uint32(b.UID),
			MessageID: b.Envelope.MessageID,
			From:      from,
			Subject:   b.Envelope.Subject,
			Date:      b.Envelope.Date,
			Body:      body,
		})
	}
	return msgs, inspected, highUID, nil
}

// Senders returns the active sender substring filter. Surfaced so callers
// (dashboard scan endpoint) can show users *which* filter was applied when
// the inspected-but-no-match case fires.
func (c *Client) Senders() []string {
	out := make([]string, len(c.senders))
	copy(out, c.senders)
	return out
}

func (c *Client) matchesSender(from string) bool {
	f := strings.ToLower(from)
	for _, s := range c.senders {
		if strings.Contains(f, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

func formatAddr(addrs []imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	a := addrs[0]
	mail := a.Mailbox + "@" + a.Host
	if a.Name != "" {
		return a.Name + " <" + mail + ">"
	}
	return mail
}

// extractText parses a raw RFC822 message and returns its text content,
// preferring text/plain and falling back to a crude HTML strip. Truncated to a
// sane length so the classifier prompt stays small.
func extractText(raw []byte) string {
	const maxLen = 8000
	if len(raw) == 0 {
		return ""
	}
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return truncateRunes(stripHTML(string(raw)), maxLen)
	}

	var plain, html string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			b, _ := io.ReadAll(part.Body)
			if strings.Contains(ct, "html") {
				html += string(b)
			} else {
				plain += string(b)
			}
		default:
			// attachment / other — skip body
		}
	}

	text := plain
	if strings.TrimSpace(text) == "" {
		text = stripHTML(html)
	}
	return truncateRunes(strings.TrimSpace(text), maxLen)
}

// stripHTML removes tags and collapses whitespace — good enough to feed the
// classifier when only an HTML body is present.
func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			b.WriteByte(' ') // tag boundary → whitespace so <br>/<td> don't fuse words
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	// collapse runs of whitespace
	return strings.Join(strings.Fields(b.String()), " ")
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
