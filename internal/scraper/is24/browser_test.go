package is24

import (
	"strings"
	"testing"
)

// TestBlockedURLPatterns_HasRequiredCategories guards against an accidental
// trim of the patterns list. If someone deletes the image / font / tracker
// rows during a refactor the test fails loudly — recovering the saved proxy
// bandwidth was the whole point of having the list.
func TestBlockedURLPatterns_HasRequiredCategories(t *testing.T) {
	if len(blockedURLPatterns) == 0 {
		t.Fatal("blockedURLPatterns is empty — resource blocker disabled")
	}

	mustContain := []string{
		// One representative per category. Don't enumerate every pattern —
		// that just creates churn for future additions.
		"*.png",                 // images
		"*.woff2",               // fonts
		"*.css",                 // stylesheets
		"*.mp4",                 // media
		"*googletagmanager.com*", // tracker domain
	}
	joined := strings.Join(blockedURLPatterns, "|")
	for _, p := range mustContain {
		if !strings.Contains(joined, p) {
			t.Errorf("blockedURLPatterns missing required entry %q", p)
		}
	}

	// JS must NOT be in the list — the WAF challenge depends on it.
	for _, p := range blockedURLPatterns {
		if strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".js*") {
			t.Errorf("blockedURLPatterns must not block JavaScript (found %q) — WAF challenge would never clear", p)
		}
	}
}

func TestForceSortByNewest(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no query string",
			in:   "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten",
			want: "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?sorting=2",
		},
		{
			name: "query string without sorting",
			in:   "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?price=-1600.0",
			want: "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?price=-1600.0&sorting=2",
		},
		{
			name: "sorting=2 already set, no-op",
			in:   "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?sorting=2",
			want: "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?sorting=2",
		},
		{
			name: "sorting=8 (price) rewritten to sorting=2",
			in:   "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?sorting=8",
			want: "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?sorting=2",
		},
		{
			name: "sorting in middle of param list rewritten",
			in:   "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?price=-1600.0&sorting=8&livingspace=30.0-",
			want: "https://www.immobilienscout24.de/Suche/de/berlin/wohnung-mieten?price=-1600.0&sorting=2&livingspace=30.0-",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := forceSortByNewest(tc.in)
			if got != tc.want {
				t.Errorf("forceSortByNewest(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
