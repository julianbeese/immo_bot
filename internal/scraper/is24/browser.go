package is24

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/julianbeese/immo_bot/internal/antidetect"
	"github.com/julianbeese/immo_bot/internal/domain"
)

// blockedURLPatterns are passed to Network.setBlockedURLs on every fetch. The
// parser reads HTML + inline JSON only — images, media, fonts, stylesheets,
// and third-party ad/tracker bundles are pure bandwidth overhead on a metered
// residential proxy. JS is deliberately NOT blocked: IS24's AWS-WAF challenge
// is JS-driven, and blocking scripts would wedge the bot at the challenge
// page. Wildcards follow CDP semantics ('*' matches any substring).
var blockedURLPatterns = []string{
	// Images
	"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.svg", "*.ico", "*.bmp",
	// Media
	"*.mp4", "*.webm", "*.mp3", "*.m4a", "*.ogg", "*.wav",
	// Fonts
	"*.woff", "*.woff2", "*.ttf", "*.otf", "*.eot",
	// Stylesheets
	"*.css",
	// Ad / tracker domains
	"*googletagmanager.com*", "*google-analytics.com*", "*doubleclick.net*",
	"*facebook.net*", "*facebook.com*", "*criteo.com/*", "*criteo.net/*",
	"*scorecardresearch.com*", "*hotjar.com*", "*adnxs.com*",
	"*amazon-adsystem.com*", "*adform.net*", "*onetrust.com*",
	"*adsystem.com*",
}

// sortingParamRE matches the sorting query parameter and its value. Used by
// forceSortByNewest to rewrite any user-supplied sort to newest-first, which
// the early-stop optimization depends on.
var sortingParamRE = regexp.MustCompile(`sorting=\d+`)

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

// Search performs a search using browser automation with pagination. Capped at
// 5 pages with an early-break when a page yields fewer than 5 unique results —
// tuned for the regular polling cycle, where catching the freshest listings
// fast matters more than full historical coverage. If existsFn is non-nil, the
// crawl also stops at the first listing already known to the caller (results
// are sorted newest-first, so everything beyond it is already in the DB). Use
// SearchPaginated for the one-time backfill that needs to walk every page.
func (c *BrowserClient) Search(ctx context.Context, profile *domain.SearchProfile, existsFn func(is24ID string) bool) ([]domain.Listing, error) {
	return c.searchPages(ctx, profile, 5, 5, existsFn)
}

// SearchPaginated walks up to maxPages of search results, stopping only when a
// page returns zero listings (true end of result set) or maxPages is reached.
// Unlike Search, it never breaks early on a sparse page or on known listings —
// sparse pages happen at the tail of large result sets and the backfill needs
// every listing regardless of what's already stored.
func (c *BrowserClient) SearchPaginated(ctx context.Context, profile *domain.SearchProfile, maxPages int) ([]domain.Listing, error) {
	if maxPages <= 0 {
		maxPages = 5
	}
	return c.searchPages(ctx, profile, maxPages, 0, nil)
}

// searchPages drives the paginated scrape. sparseStopThreshold is the minimum
// number of new (deduped) listings a page must contribute to keep going; set
// it to 0 to disable the early break and walk the full maxPages. existsFn, if
// non-nil, returns true when an IS24 ID is already stored — the scrape stops
// at the first hit (relies on the newest-first sort enforced in the URL).
func (c *BrowserClient) searchPages(ctx context.Context, profile *domain.SearchProfile, maxPages, sparseStopThreshold int, existsFn func(is24ID string) bool) ([]domain.Listing, error) {
	searchURL := profile.SearchURL
	if searchURL == "" {
		searchURL = fmt.Sprintf("https://www.immobilienscout24.de/Suche/de/%s/wohnung-mieten", profile.City)
	}
	searchURL = forceSortByNewest(searchURL)

	var allListings []domain.Listing
	seenIDs := make(map[string]bool)

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

		// Deduplicate and add. Walk in order so we can stop at the first known
		// listing — with sorting=newest-first, every entry below it is older
		// and therefore also already stored.
		newOnPage := 0
		hitKnown := false
		for _, l := range listings {
			if seenIDs[l.IS24ID] {
				continue
			}
			if existsFn != nil && existsFn(l.IS24ID) {
				hitKnown = true
				break
			}
			seenIDs[l.IS24ID] = true
			l.SearchProfileID = profile.ID
			allListings = append(allListings, l)
			newOnPage++
		}

		if hitKnown {
			break
		}

		if sparseStopThreshold > 0 && newOnPage < sparseStopThreshold {
			break
		}
	}

	return allListings, nil
}

// forceSortByNewest rewrites the URL so listings always come back newest-first
// (sorting=2, Erscheinungsdatum). If sorting= is already set we OVERRIDE it —
// the early-stop-on-known optimization in searchPages is silently wrong under
// any other sort, and a user-supplied sort preference (e.g. by price) doesn't
// represent intent for the scraper, only for browsing. If sorting= is absent
// we append it.
func forceSortByNewest(rawURL string) string {
	if sortingParamRE.MatchString(rawURL) {
		return sortingParamRE.ReplaceAllString(rawURL, "sorting=2")
	}
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "sorting=2"
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
	// Network.enable must be on for both the byte counter (bandwidth.go) AND
	// SetBlockedURLs. Even with no bandwidth guard wired we still want the
	// blocker active, so this runs unconditionally now.
	actions = append(actions, antidetect.NetworkEnable())
	// Block heavy / irrelevant subresources before navigation. The Network
	// domain matches by URL pattern (independent of the Fetch domain used by
	// the proxy auth handler), so it composes cleanly with proxy auth.
	actions = append(actions, network.SetBlockedURLs(blockedURLPatterns))
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
