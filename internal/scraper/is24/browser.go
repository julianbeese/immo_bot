package is24

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/julianbeese/immo_bot/internal/antidetect"
	"github.com/julianbeese/immo_bot/internal/domain"
)

// ErrWAFChallenge is returned when the IS24 "Ich bin kein Roboter" challenge
// did not clear within the timeout. Callers should treat this as a transient
// failure — retry on the next poll — rather than persist the challenge page.
var ErrWAFChallenge = errors.New("is24 WAF challenge did not pass")

// BrowserClient uses chromedp for scraping (bypasses WAF)
type BrowserClient struct {
	mu          sync.RWMutex // guards cookie for hot-reload via SetCookie
	cookie      string
	rateLimiter *antidetect.RateLimiter
	parser      *Parser
	chromePath  string
	proxy       antidetect.Proxy
	bandwidth   *antidetect.BandwidthGuard
	debug       bool
}

// currentCookie returns a snapshot of the current cookie value under RLock.
func (c *BrowserClient) currentCookie() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cookie
}

// NewBrowserClient creates a new browser-based IS24 client. A nil bandwidth
// guard disables tracking and the monthly cap.
func NewBrowserClient(cookie string, rateLimiter *antidetect.RateLimiter, chromePath string, proxy antidetect.Proxy, bandwidth *antidetect.BandwidthGuard) *BrowserClient {
	return &BrowserClient{
		cookie:      cookie,
		rateLimiter: rateLimiter,
		parser:      NewParser(),
		chromePath:  chromePath,
		proxy:       proxy,
		bandwidth:   bandwidth,
		debug:       os.Getenv("DEBUG_HTML") == "1",
	}
}

// Search performs a search using browser automation with pagination
func (c *BrowserClient) Search(ctx context.Context, profile *domain.SearchProfile) ([]domain.Listing, error) {
	searchURL := profile.SearchURL
	if searchURL == "" {
		searchURL = fmt.Sprintf("https://www.immobilienscout24.de/Suche/de/%s/wohnung-mieten", profile.City)
	}

	var allListings []domain.Listing
	seenIDs := make(map[string]bool)
	maxPages := 5 // Limit to avoid too many requests

	for page := 1; page <= maxPages; page++ {
		pageURL := c.buildPageURL(searchURL, page)

		c.rateLimiter.Wait()

		html, err := c.fetchPage(ctx, pageURL)
		// Debug-dump even on error so a captured WAF page can be inspected.
		if c.debug && html != "" {
			_ = os.MkdirAll("data/debug", 0o755)
			os.WriteFile(fmt.Sprintf("data/debug/is24_search_page%d.html", page), []byte(html), 0o644)
		}
		if err != nil {
			return nil, fmt.Errorf("fetch search page %d: %w", page, err)
		}

		listings, err := c.parser.ParseSearchResults([]byte(html))
		if err != nil {
			return nil, fmt.Errorf("parse search page %d: %w", page, err)
		}

		// No more results on this page
		if len(listings) == 0 {
			break
		}

		// Deduplicate and add
		newOnPage := 0
		for _, l := range listings {
			if !seenIDs[l.IS24ID] {
				seenIDs[l.IS24ID] = true
				l.SearchProfileID = profile.ID
				allListings = append(allListings, l)
				newOnPage++
			}
		}

		// If we got very few new results, probably last page
		if newOnPage < 5 {
			break
		}
	}

	return allListings, nil
}

// buildPageURL adds pagination parameter to the URL
func (c *BrowserClient) buildPageURL(baseURL string, page int) string {
	if page == 1 {
		return baseURL
	}

	// IS24 uses pagenumber parameter
	separator := "?"
	if strings.Contains(baseURL, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%spagenumber=%d", baseURL, separator, page)
}

// FetchExpose fetches detailed listing info
func (c *BrowserClient) FetchExpose(ctx context.Context, is24ID string) (*domain.Listing, error) {
	exposeURL := fmt.Sprintf("https://www.immobilienscout24.de/expose/%s", is24ID)

	c.rateLimiter.Wait()

	html, err := c.fetchPage(ctx, exposeURL)
	// Debug-dump even on error so a captured WAF page can be inspected.
	if c.debug && html != "" {
		_ = os.MkdirAll("data/debug", 0o755)
		_ = os.WriteFile(fmt.Sprintf("data/debug/is24_expose_%s.html", is24ID), []byte(html), 0o644)
	}
	if err != nil {
		return nil, fmt.Errorf("fetch expose: %w", err)
	}

	return c.parser.ParseExpose([]byte(html), is24ID)
}

func (c *BrowserClient) fetchPage(ctx context.Context, url string) (string, error) {
	if c.bandwidth != nil && !c.bandwidth.Allowed() {
		return "", antidetect.ErrBandwidthExceeded
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	if c.chromePath != "" {
		opts = append(opts, chromedp.ExecPath(c.chromePath))
	}

	if c.proxy.Enabled() {
		opts = append(opts, chromedp.ProxyServer(c.proxy.URL))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	// Listener must be attached before fetch.Enable runs.
	c.proxy.AttachAuthHandler(browserCtx)
	var byteSnapshot func() int64
	if c.bandwidth != nil {
		byteSnapshot = antidetect.AttachByteCounter(browserCtx)
		defer func() {
			if byteSnapshot != nil {
				c.bandwidth.Add(byteSnapshot())
			}
		}()
	}

	// Set timeout. Proxy + WAF challenge + render can take a while; 90s gives
	// the AWS-WAF challenge JS room to solve through residential-proxy latency.
	browserCtx, cancel := context.WithTimeout(browserCtx, 90*time.Second)
	defer cancel()

	var html string

	// Set cookies before navigating (snapshot under lock to allow hot-reload).
	actions := []chromedp.Action{}
	if c.bandwidth != nil {
		actions = append(actions, antidetect.NetworkEnable())
	}
	if a := c.proxy.EnableAction(); a != nil {
		actions = append(actions, a)
	}
	// Stealth: hide the most obvious automation tells before IS24's WAF JS runs.
	// AddScriptToEvaluateOnNewDocument fires before any page script on every
	// navigation, so it covers the WAF challenge page reload too.
	actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(stealthJS).Do(ctx)
		return err
	}))

	cookieStr := c.currentCookie()
	if cookieStr != "" {
		cookies := parseCookieString(cookieStr)
		for _, cookie := range cookies {
			actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
				return network.SetCookie(cookie.Name, cookie.Value).
					WithDomain(".immobilienscout24.de").
					WithPath("/").
					Do(ctx)
			}))
		}
	}

	actions = append(actions,
		chromedp.Navigate(url),
		chromedp.Sleep(3*time.Second),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		// Poll the page title until the AWS-WAF challenge clears. The challenge
		// JS retries every 200ms for up to ~10s on a direct connection; through
		// a residential proxy the extra latency pushes it well past that, so
		// we give it up to 30s. Polling beats a fixed sleep because we resume
		// the moment the challenge passes. If the challenge never clears we
		// return ErrWAFChallenge — previously this returned nil and the caller
		// silently received the challenge HTML, producing empty listings.
		chromedp.ActionFunc(func(ctx context.Context) error {
			deadline := time.Now().Add(30 * time.Second)
			for {
				var title string
				if err := chromedp.Title(&title).Do(ctx); err != nil {
					return err
				}
				if title != "Ich bin kein Roboter - ImmobilienScout24" {
					return nil
				}
				if time.Now().After(deadline) {
					return ErrWAFChallenge
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(1 * time.Second):
				}
			}
		}),
		// Wait for search results or expose content
		chromedp.Sleep(2*time.Second),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)

	if err := chromedp.Run(browserCtx, actions...); err != nil {
		return "", err
	}

	// Safety net: the WAF poll can succeed (title cleared briefly) but the
	// page can flip back to a challenge before OuterHTML runs. Detect by
	// scanning the captured HTML for the challenge title.
	if strings.Contains(html, "Ich bin kein Roboter - ImmobilienScout24") {
		return html, ErrWAFChallenge
	}

	return html, nil
}

// SetCookie updates the client's cookie. The next request applies it via the
// chromedp network.SetCookie path in fetchPage; no jar to rebuild.
func (c *BrowserClient) SetCookie(cookie string) error {
	c.mu.Lock()
	c.cookie = cookie
	c.mu.Unlock()
	return nil
}
