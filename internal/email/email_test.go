package email

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/julianbeese/immo_bot/internal/domain"
)

func TestExtractTextMultipart(t *testing.T) {
	raw := "From: a@b.de\r\n" +
		"To: me@x.de\r\n" +
		"Subject: Test\r\n" +
		"Content-Type: multipart/alternative; boundary=BOUND\r\n" +
		"\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Hallo, gerne Besichtigung.\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>Hallo, <b>gerne</b> Besichtigung.</p>\r\n" +
		"--BOUND--\r\n"

	got := extractText([]byte(raw))
	if got != "Hallo, gerne Besichtigung." {
		t.Errorf("extractText plain = %q", got)
	}
}

func TestExtractTextHTMLOnly(t *testing.T) {
	raw := "Subject: x\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<div>Guten Tag<br>Die Wohnung ist noch <b>frei</b>.</div>"
	got := extractText([]byte(raw))
	if strings.Contains(got, "<") {
		t.Errorf("extractText html left tags: %q", got)
	}
	// <br> must not fuse "Tag" and "Die"; word content must survive.
	for _, want := range []string{"Guten Tag", "Die Wohnung", "frei"} {
		if !strings.Contains(got, want) {
			t.Errorf("extractText html = %q, missing %q", got, want)
		}
	}
}

func TestMatchesSenderAndFormatAddr(t *testing.T) {
	c := NewClient(Config{Addr: "h:993"})
	if !c.matchesSender("Service <no-reply@immobilienscout24.de>") {
		t.Error("expected is24 sender match")
	}
	if c.matchesSender("chef@example.com") {
		t.Error("unexpected match for non-is24 sender")
	}
	got := formatAddr([]imap.Address{{Name: "ImmoScout", Mailbox: "no-reply", Host: "immobilienscout24.de"}})
	if got != "ImmoScout <no-reply@immobilienscout24.de>" {
		t.Errorf("formatAddr = %q", got)
	}
}

// --- Monitor.process flow with fakes ---

type fakeStore struct {
	existing map[string]bool
	created  []*domain.InboxMessage
	listings map[string]*domain.Listing
	meta     map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{existing: map[string]bool{}, listings: map[string]*domain.Listing{}, meta: map[string]string{}}
}
func (f *fakeStore) InboxExists(_ context.Context, id string) (bool, error) {
	return f.existing[id], nil
}
func (f *fakeStore) CreateInboxMessage(_ context.Context, m *domain.InboxMessage) error {
	f.existing[m.MessageID] = true
	f.created = append(f.created, m)
	return nil
}
func (f *fakeStore) GetListingByIS24ID(_ context.Context, id string) (*domain.Listing, error) {
	return f.listings[id], nil
}
func (f *fakeStore) GetMeta(_ context.Context, k string) (string, error) { return f.meta[k], nil }
func (f *fakeStore) SetMeta(_ context.Context, k, v string) error        { f.meta[k] = v; return nil }

type fakeClassifier struct {
	out Classification
	err error
}

func (f fakeClassifier) Classify(_ context.Context, _ Message) (Classification, error) {
	return f.out, f.err
}

type fakeNotifier struct{ sent []string }

func (f *fakeNotifier) SendRawMessage(_ context.Context, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

func newTestMonitor(cls Classifier, store Store, notif Notifier) *Monitor {
	return NewMonitor(NewClient(Config{Addr: "h:993", Mailbox: "INBOX"}), cls, store, notif, nil)
}

func TestProcessLandlordReplyNotifiesAndLinks(t *testing.T) {
	store := newFakeStore()
	store.listings["999"] = &domain.Listing{ID: 42, IS24ID: "999"}
	notif := &fakeNotifier{}
	m := newTestMonitor(fakeClassifier{out: Classification{
		IsLandlordReply: true, IS24ID: "999", Summary: "Vermieter bietet Termin.",
	}}, store, notif)

	out, err := m.process(context.Background(), Message{
		MessageID: "id-1", From: "hans@example.com", Subject: "Re: Anfrage",
		Body: "Gerne Besichtigung", Date: time.Now(),
	})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if !out.Classified || !out.LandlordReply || !out.Notified {
		t.Errorf("outcome unexpected: %+v", out)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(store.created))
	}
	rec := store.created[0]
	if rec.ListingID != 42 {
		t.Errorf("ListingID = %d, want 42 (linked)", rec.ListingID)
	}
	if !rec.Notified {
		t.Error("expected Notified = true")
	}
	if len(notif.sent) != 1 {
		t.Errorf("expected 1 notification, got %d", len(notif.sent))
	}
}

func TestProcessSystemMailNoNotify(t *testing.T) {
	store := newFakeStore()
	notif := &fakeNotifier{}
	m := newTestMonitor(fakeClassifier{out: Classification{IsLandlordReply: false}}, store, notif)

	out, err := m.process(context.Background(), Message{MessageID: "id-2", Body: "Newsletter"})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if out.LandlordReply || out.Notified {
		t.Errorf("system mail outcome should not flag landlord/notified: %+v", out)
	}
	if len(store.created) != 1 || store.created[0].Notified {
		t.Errorf("system mail should be stored but not notified: %+v", store.created)
	}
	if len(notif.sent) != 0 {
		t.Error("system mail should not notify")
	}
}

func TestProcessDedupSkipsClassifier(t *testing.T) {
	store := newFakeStore()
	store.existing["seen"] = true
	cls := fakeClassifier{err: errors.New("must not be called")}
	m := newTestMonitor(cls, store, &fakeNotifier{})

	out, err := m.process(context.Background(), Message{MessageID: "seen"})
	if err != nil {
		t.Fatalf("process dedup: %v", err)
	}
	if !out.Skipped {
		t.Errorf("dedup should mark outcome.Skipped: %+v", out)
	}
	if len(store.created) != 0 {
		t.Error("already-seen mail must not be re-created")
	}
}
