package is24

import (
	"strings"
	"testing"

	"github.com/julianbeese/immo_bot/internal/domain"
)

// newTestClient builds a minimal Client wired with just enough to exercise
// buildSearchURL — no rate limiter call, no HTTP, no cookies.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	return &Client{}
}

func TestBuildSearchURL_CustomURL_NoSorting(t *testing.T) {
	c := newTestClient(t)
	got := c.buildSearchURL(&domain.SearchProfile{
		SearchURL: "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?price=-1600.0",
	})
	want := "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?price=-1600.0&sorting=2"
	if got != want {
		t.Fatalf("buildSearchURL: got %q, want %q", got, want)
	}
}

func TestBuildSearchURL_CustomURL_OverridesPriceSort(t *testing.T) {
	// A profile created with sorting=8 (Preis) would silently break the
	// early-stop optimization if we honoured it. forceSortByNewest must
	// rewrite to sorting=2.
	c := newTestClient(t)
	got := c.buildSearchURL(&domain.SearchProfile{
		SearchURL: "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?sorting=8&price=-1600",
	})
	if !strings.Contains(got, "sorting=2") {
		t.Fatalf("expected sorting=2 in rewritten URL, got %q", got)
	}
	if strings.Contains(got, "sorting=8") {
		t.Fatalf("sorting=8 should have been overridden, got %q", got)
	}
}

func TestBuildSearchURL_CustomURL_AlreadyNewest(t *testing.T) {
	c := newTestClient(t)
	in := "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?sorting=2"
	got := c.buildSearchURL(&domain.SearchProfile{SearchURL: in})
	if got != in {
		t.Fatalf("sorting=2 URL should pass through unchanged: got %q", got)
	}
}

func TestBuildSearchURL_AutoBuilt_AlwaysSortsByNewest(t *testing.T) {
	c := newTestClient(t)
	got := c.buildSearchURL(&domain.SearchProfile{
		City:     "Berlin",
		MinPrice: 500,
		MaxPrice: 1200,
	})
	if !strings.Contains(got, "sorting=2") {
		t.Fatalf("auto-built URL must contain sorting=2, got %q", got)
	}
	if !strings.Contains(got, "/Suche/de/berlin/wohnung-mieten") {
		t.Fatalf("auto-built URL should contain city slug, got %q", got)
	}
	if !strings.Contains(got, "price=500-1200") {
		t.Fatalf("auto-built URL should contain price range, got %q", got)
	}
}
