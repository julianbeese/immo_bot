package sqlite

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/julianbeese/immo_bot/internal/domain"
)

func TestMigrationsAndCategoryRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer repo.Close()

	ctx := context.Background()
	sp := &domain.SearchProfile{
		Name:      "WG Berlin",
		City:      "Berlin",
		SearchURL: "https://is24.de/Suche/x",
		Category:  "wg",
		Active:    true,
	}
	if err := repo.CreateSearchProfile(ctx, sp); err != nil {
		t.Fatalf("CreateSearchProfile: %v", err)
	}
	if sp.ID == 0 {
		t.Fatal("expected assigned ID")
	}

	got, err := repo.GetSearchProfileByID(ctx, sp.ID)
	if err != nil {
		t.Fatalf("GetSearchProfileByID: %v", err)
	}
	if got.Category != "wg" {
		t.Errorf("category = %q, want wg", got.Category)
	}
	if got.SearchURL != sp.SearchURL || got.Name != sp.Name {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	active, err := repo.GetActiveSearchProfiles(ctx)
	if err != nil {
		t.Fatalf("GetActiveSearchProfiles: %v", err)
	}
	if len(active) != 1 || active[0].Category != "wg" {
		t.Errorf("active profiles = %+v", active)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	// Open, close, reopen — migrations must run cleanly a second time.
	repo, err := New(dbPath)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	repo.Close()

	repo2, err := New(dbPath)
	if err != nil {
		t.Fatalf("second New (re-run migrations): %v", err)
	}
	defer repo2.Close()
}

func TestGetSearchProfileByIDNotFound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err := repo.GetSearchProfileByID(context.Background(), 999); err == nil {
		t.Error("expected error for missing profile")
	}
}

func TestListRecentListingsAndProfiles(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()

	sp := &domain.SearchProfile{Name: "P", City: "Berlin", Active: true, Category: "wg"}
	if err := repo.CreateSearchProfile(ctx, sp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		l := &domain.Listing{
			IS24ID:          "id" + strconv.Itoa(i),
			Title:           "W" + strconv.Itoa(i),
			URL:             "https://is24.de/expose/" + strconv.Itoa(i),
			SearchProfileID: sp.ID,
		}
		if err := repo.CreateListing(ctx, l); err != nil {
			t.Fatal(err)
		}
	}

	recent, err := repo.ListRecentListings(ctx, 2)
	if err != nil {
		t.Fatalf("ListRecentListings: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("limit not applied: got %d, want 2", len(recent))
	}

	// Deactivate → still listed by ListAll, not by GetActive.
	if err := repo.SetSearchProfileActive(ctx, sp.ID, false); err != nil {
		t.Fatal(err)
	}
	all, _ := repo.ListAllSearchProfiles(ctx)
	if len(all) != 1 || all[0].Category != "wg" {
		t.Errorf("ListAllSearchProfiles = %+v", all)
	}
	active, _ := repo.GetActiveSearchProfiles(ctx)
	if len(active) != 0 {
		t.Errorf("deactivated profile must not be active, got %d", len(active))
	}

	// Delete.
	if err := repo.DeleteSearchProfile(ctx, sp.ID); err != nil {
		t.Fatalf("DeleteSearchProfile: %v", err)
	}
	all, _ = repo.ListAllSearchProfiles(ctx)
	if len(all) != 0 {
		t.Errorf("profile should be gone, got %d", len(all))
	}
	if err := repo.DeleteSearchProfile(ctx, 999); err == nil {
		t.Error("deleting missing profile should error")
	}
}

func TestMetaSetGet(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()

	if v, err := repo.GetMeta(ctx, "missing"); err != nil || v != "" {
		t.Errorf("missing key should be empty, got %q err %v", v, err)
	}
	if err := repo.SetMeta(ctx, MetaLastPollOK, "2026-05-27T10:00:00Z"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if v, _ := repo.GetMeta(ctx, MetaLastPollOK); v != "2026-05-27T10:00:00Z" {
		t.Errorf("GetMeta = %q", v)
	}
	// Upsert overwrites.
	if err := repo.SetMeta(ctx, MetaLastPollOK, "2026-05-27T11:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if v, _ := repo.GetMeta(ctx, MetaLastPollOK); v != "2026-05-27T11:00:00Z" {
		t.Errorf("upsert failed, got %q", v)
	}
}

func TestSetSearchProfileActive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()

	sp := &domain.SearchProfile{Name: "X", City: "Berlin", Active: true}
	if err := repo.CreateSearchProfile(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSearchProfileActive(ctx, sp.ID, false); err != nil {
		t.Fatalf("SetSearchProfileActive: %v", err)
	}
	active, _ := repo.GetActiveSearchProfiles(ctx)
	if len(active) != 0 {
		t.Errorf("deactivated profile should not be active, got %d", len(active))
	}
	if err := repo.SetSearchProfileActive(ctx, 999, false); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestPreviewableListingsDoNotConsumeContactState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()

	sp := &domain.SearchProfile{Name: "X", City: "Berlin", Active: true}
	if err := repo.CreateSearchProfile(ctx, sp); err != nil {
		t.Fatal(err)
	}
	listing := &domain.Listing{
		IS24ID:          "123",
		Title:           "Wohnung",
		URL:             "https://is24.de/expose/123",
		SearchProfileID: sp.ID,
	}
	if err := repo.CreateListing(ctx, listing); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkListingNotified(ctx, listing.ID); err != nil {
		t.Fatal(err)
	}

	previewable, err := repo.GetPreviewableListings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(previewable) != 1 {
		t.Fatalf("expected listing to be previewable, got %d", len(previewable))
	}

	if err := repo.CreateSentMessage(ctx, &domain.SentMessage{
		ListingID: listing.ID,
		IS24ID:    listing.IS24ID,
		Message:   "preview",
		Status:    domain.MessageStatusPreview,
	}); err != nil {
		t.Fatal(err)
	}

	previewable, err = repo.GetPreviewableListings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(previewable) != 0 {
		t.Fatalf("preview should not repeat, got %d listings", len(previewable))
	}
	uncontacted, err := repo.GetUncontactedListings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(uncontacted) != 1 {
		t.Fatalf("preview must not mark listing contacted, got %d uncontacted", len(uncontacted))
	}
}
