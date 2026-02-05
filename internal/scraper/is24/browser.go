package is24

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/julianbeese/immo_bot/internal/antidetect"
	"github.com/julianbeese/immo_bot/internal/domain"
)

// BrowserClient uses chromedp for scraping (bypasses WAF)
type BrowserClient struct {
	cookie      string
	rateLimiter *antidetect.RateLimiter
	parser      *Parser
	chromePath  string
	debug       bool
}

// NewBrowserClient creates a new browser-based IS24 client
func NewBrowserClient(cookie string, rateLimiter *antidetect.RateLimiter, chromePath string) *BrowserClient {
	return &BrowserClient{
		cookie:      cookie,
		rateLimiter: rateLimiter,
		parser:      NewParser(),
		chromePath:  chromePath,
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
		if err != nil {
			return nil, fmt.Errorf("fetch search page %d: %w", page, err)
		}

		// Debug: save HTML to file
		if c.debug {
			os.WriteFile(fmt.Sprintf("/tmp/is24_search_page%d.html", page), []byte(html), 0644)
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
	if err != nil {
		return nil, fmt.Errorf("fetch expose: %w", err)
	}

	return c.parser.ParseExpose([]byte(html), is24ID)
}

func (c *BrowserClient) fetchPage(ctx context.Context, url string) (string, error) {
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

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	// Set timeout
	browserCtx, cancel := context.WithTimeout(browserCtx, 60*time.Second)
	defer cancel()

	var html string

	// Set cookies before navigating
	actions := []chromedp.Action{}

	if c.cookie != "" {
		cookies := parseCookieString(c.cookie)
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
		// Wait for WAF challenge to complete (page reload)
		chromedp.Sleep(3*time.Second),
		// Wait for actual content
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		// Check if we're still on challenge page, wait more if needed
		chromedp.ActionFunc(func(ctx context.Context) error {
			var title string
			if err := chromedp.Title(&title).Do(ctx); err != nil {
				return err
			}
			// If still on robot check page, wait more
			if title == "Ich bin kein Roboter - ImmobilienScout24" {
				time.Sleep(5 * time.Second)
			}
			return nil
		}),
		// Wait for search results or expose content
		chromedp.Sleep(2*time.Second),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)

	if err := chromedp.Run(browserCtx, actions...); err != nil {
		return "", err
	}

	return html, nil
}

// SetCookie updates the client's cookie
func (c *BrowserClient) SetCookie(cookie string) {
	c.cookie = cookie
}
