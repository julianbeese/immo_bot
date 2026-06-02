package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/julianbeese/immo_bot/internal/domain"
)

func TestSkippedExcludedFromAutoContact(t *testing.T) {
	repo, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer repo.Close()
	ctx := context.Background()

	sp := &domain.SearchProfile{Name: "P", City: "Berlin", Active: true}
	if err := repo.CreateSearchProfile(ctx, sp); err != nil {
		t.Fatalf("CreateSearchProfile: %v", err)
	}
	l := &domain.Listing{IS24ID: "abc123", Title: "Test", URL: "https://x", SearchProfileID: sp.ID, BuildYear: 2000}
	if err := repo.CreateListing(ctx, l); err != nil {
		t.Fatalf("CreateListing: %v", err)
	}
	if err := repo.MarkListingNotified(ctx, l.ID); err != nil {
		t.Fatalf("MarkListingNotified: %v", err)
	}

	// Eligible for auto-contact before skipping.
	un, err := repo.GetUncontactedListings(ctx)
	if err != nil {
		t.Fatalf("GetUncontactedListings: %v", err)
	}
	if len(un) != 1 {
		t.Fatalf("expected 1 uncontacted, got %d", len(un))
	}

	if err := repo.SetListingSkipped(ctx, l.ID, true); err != nil {
		t.Fatalf("SetListingSkipped: %v", err)
	}
	un, _ = repo.GetUncontactedListings(ctx)
	if len(un) != 0 {
		t.Errorf("skipped listing must be excluded from auto-contact, got %d", len(un))
	}

	// Round-trips through the dashboard read path.
	got, err := repo.GetListingByIS24ID(ctx, "abc123")
	if err != nil || got == nil {
		t.Fatalf("GetListingByIS24ID: %v", err)
	}
	if !got.Skipped {
		t.Error("Skipped flag not persisted/read")
	}

	// Reversible.
	if err := repo.SetListingSkipped(ctx, l.ID, false); err != nil {
		t.Fatalf("unskip: %v", err)
	}
	un, _ = repo.GetUncontactedListings(ctx)
	if len(un) != 1 {
		t.Errorf("un-skipped listing should be eligible again, got %d", len(un))
	}
}
