package sqlite

import (
	"context"
	"path/filepath"
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
