package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/julianbeese/immo_bot/internal/domain"
)

func TestInboxRoundTripAndDedup(t *testing.T) {
	repo, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer repo.Close()
	ctx := context.Background()

	exists, err := repo.InboxExists(ctx, "msg-1")
	if err != nil || exists {
		t.Fatalf("InboxExists before insert = %v, %v", exists, err)
	}

	m := &domain.InboxMessage{
		MessageID:       "msg-1",
		FromAddr:        "Vermieter <hans@example.com>",
		Subject:         "Re: Ihre Anfrage",
		Snippet:         "Gerne können wir einen Termin machen.",
		IS24ID:          "123456789",
		IsLandlordReply: true,
		Summary:         "Vermieter bietet Besichtigungstermin an.",
		Notified:        true,
		ReceivedAt:      time.Now().Add(-time.Hour),
	}
	if err := repo.CreateInboxMessage(ctx, m); err != nil {
		t.Fatalf("CreateInboxMessage: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("expected assigned ID")
	}

	// Idempotent on duplicate message_id.
	dup := *m
	dup.ID = 0
	if err := repo.CreateInboxMessage(ctx, &dup); err != nil {
		t.Fatalf("CreateInboxMessage dup: %v", err)
	}
	exists, _ = repo.InboxExists(ctx, "msg-1")
	if !exists {
		t.Fatal("InboxExists after insert = false")
	}

	all, err := repo.ListInboxMessages(ctx, 100, false)
	if err != nil {
		t.Fatalf("ListInboxMessages: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 row after dup insert, got %d", len(all))
	}
	got := all[0]
	if got.IS24ID != "123456789" || !got.IsLandlordReply || got.Summary == "" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Insert a non-landlord mail; landlordOnly filter must exclude it.
	if err := repo.CreateInboxMessage(ctx, &domain.InboxMessage{
		MessageID: "msg-2", FromAddr: "newsletter@immobilienscout24.de",
		Subject: "Newsletter", IsLandlordReply: false, ReceivedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateInboxMessage 2: %v", err)
	}
	landlord, err := repo.ListInboxMessages(ctx, 100, true)
	if err != nil {
		t.Fatalf("ListInboxMessages landlord: %v", err)
	}
	if len(landlord) != 1 || landlord[0].MessageID != "msg-1" {
		t.Errorf("landlordOnly = %+v, want only msg-1", landlord)
	}
}
