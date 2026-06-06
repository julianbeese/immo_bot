package is24

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestBlockedURLs_E2E spins up a local HTTP server, drives a headless Chrome
// instance with the production blockedURLPatterns, and asserts that the
// expected resources are blocked while HTML and JavaScript still load.
//
// Opt-in: requires IS24_INTEGRATION=1 (and a working Chrome binary in PATH).
// Default `go test ./...` skips it so the unit suite stays fast and
// binary-free.
//
// Run with:
//
//	IS24_INTEGRATION=1 go test -run TestBlockedURLs_E2E ./internal/scraper/is24/
func TestBlockedURLs_E2E(t *testing.T) {
	if os.Getenv("IS24_INTEGRATION") != "1" {
		t.Skip("set IS24_INTEGRATION=1 to run (requires Chrome)")
	}

	var (
		mu   sync.Mutex
		hits = make(map[string]int)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()

		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><html><head>
<title>blocker probe</title>
<link rel="stylesheet" href="/style.css">
<link rel="preload" as="font" type="font/woff2" href="/font.woff2" crossorigin="anonymous">
</head><body>
<img src="/image.png" alt="">
<p id="marker">ready</p>
<script src="/script.js"></script>
</body></html>`)
		case "/script.js":
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, "window.__probe_js_loaded = true;")
		case "/style.css":
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, "body{color:red}")
		case "/image.png":
			w.Header().Set("Content-Type", "image/png")
			// 8-byte PNG signature is enough; we only care whether the server was hit.
			w.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
		case "/font.woff2":
			w.Header().Set("Content-Type", "font/woff2")
			w.Write([]byte("wOF2"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("disable-dev-shm-usage", true),
		)...,
	)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	browserCtx, cancelTimeout := context.WithTimeout(browserCtx, 30*time.Second)
	defer cancelTimeout()

	if err := chromedp.Run(browserCtx,
		network.Enable(),
		network.SetBlockedURLs(blockedURLPatterns),
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("#marker", chromedp.ByID),
		// Settle async resource loads. preload <link>s and <img> requests
		// fire after the parser sees them; 500ms is generous on localhost.
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		t.Fatalf("chromedp run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	t.Logf("server hits: %v", hits)

	if hits["/"] == 0 {
		t.Fatalf("HTML root was not loaded — test setup broken")
	}
	if hits["/script.js"] == 0 {
		t.Errorf("JS must NOT be blocked but server saw 0 hits — WAF challenge would break in production")
	}

	for _, blocked := range []string{"/image.png", "/style.css", "/font.woff2"} {
		if n := hits[blocked]; n != 0 {
			t.Errorf("resource %s should be blocked but server saw %d hit(s); patterns=%v",
				blocked, n, sample(blockedURLPatterns, 6))
		}
	}
}

// sample returns up to n entries from s, for diagnostics only.
func sample(s []string, n int) string {
	if len(s) <= n {
		return strings.Join(s, ",")
	}
	return strings.Join(s[:n], ",") + ",..."
}
