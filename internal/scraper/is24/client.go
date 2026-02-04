package is24

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/julianbeese/immo_bot/internal/antidetect"
	"github.com/julianbeese/immo_bot/internal/domain"
)

const (
	baseURL     = "https://www.immobilienscout24.de"
	searchPath  = "/Suche/de/%s/wohnung-mieten"
	exposePath  = "/expose/%s"
)

// Client handles HTTP requests to ImmobilienScout24
type Client struct {
	httpClient  *http.Client
	rateLimiter *antidetect.RateLimiter
	uaRotator   *antidetect.UserAgentRotator
	cookie      string
	parser      *Parser
}

// NewClient creates a new IS24 client
func NewClient(cookie string, rateLimiter *antidetect.RateLimiter, uaRotator *antidetect.UserAgentRotator) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	// Parse and set cookies if provided
	if cookie != "" {
		u, _ := url.Parse(baseURL)
		cookies := parseCookieString(cookie)
		jar.SetCookies(u, cookies)
	}

	return &Client{
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		rateLimiter: rateLimiter,
		uaRotator:   uaRotator,
		cookie:      cookie,
		parser:      NewParser(),
	}, nil
}

// Search performs a search and returns found listings
func (c *Client) Search(ctx context.Context, profile *domain.SearchProfile) ([]domain.Listing, error) {
	// Build search URL
	searchURL := c.buildSearchURL(profile)

	// Respect rate limits
	c.rateLimiter.Wait()

	// Fetch search results page
	body, err := c.fetch(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("fetch search: %w", err)
	}

	// Parse listings from HTML
	listings, err := c.parser.ParseSearchResults(body)
	if err != nil {
		return nil, fmt.Errorf("parse search: %w", err)
	}

	// Set search profile ID for all listings
	for i := range listings {
		listings[i].SearchProfileID = profile.ID
	}

	return listings, nil
}

// FetchExpose fetches detailed information for a single listing
func (c *Client) FetchExpose(ctx context.Context, is24ID string) (*domain.Listing, error) {
	exposeURL := fmt.Sprintf(baseURL+exposePath, is24ID)

	c.rateLimiter.Wait()

	body, err := c.fetch(ctx, exposeURL)
	if err != nil {
		return nil, fmt.Errorf("fetch expose: %w", err)
	}

	return c.parser.ParseExpose(body, is24ID)
}

func (c *Client) buildSearchURL(profile *domain.SearchProfile) string {
	// Use custom search URL if provided
	if profile.SearchURL != "" {
		// Ensure custom URL also sorts by newest first
		if !strings.Contains(profile.SearchURL, "sorting=") {
			if strings.Contains(profile.SearchURL, "?") {
				return profile.SearchURL + "&sorting=2"
			}
			return profile.SearchURL + "?sorting=2"
		}
		return profile.SearchURL
	}

	// Build URL from profile criteria
	city := strings.ToLower(strings.ReplaceAll(profile.City, " ", "-"))
	u := fmt.Sprintf(baseURL+searchPath, city)

	params := url.Values{}

	// Sort by newest first (sorting=2)
	params.Set("sorting", "2")

	if profile.MinPrice > 0 {
		params.Set("price", fmt.Sprintf("%d-", profile.MinPrice))
	}
	if profile.MaxPrice > 0 {
		if profile.MinPrice > 0 {
			params.Set("price", fmt.Sprintf("%d-%d", profile.MinPrice, profile.MaxPrice))
		} else {
			params.Set("price", fmt.Sprintf("-%d", profile.MaxPrice))
		}
	}

	if profile.MinRooms > 0 {
		params.Set("numberofrooms", fmt.Sprintf("%.1f-", profile.MinRooms))
	}
	if profile.MaxRooms > 0 {
		if profile.MinRooms > 0 {
			params.Set("numberofrooms", fmt.Sprintf("%.1f-%.1f", profile.MinRooms, profile.MaxRooms))
		} else {
			params.Set("numberofrooms", fmt.Sprintf("-%.1f", profile.MaxRooms))
		}
	}

	if profile.MinArea > 0 {
		params.Set("livingspace", fmt.Sprintf("%d-", profile.MinArea))
	}
	if profile.MaxArea > 0 {
		if profile.MinArea > 0 {
			params.Set("livingspace", fmt.Sprintf("%d-%d", profile.MinArea, profile.MaxArea))
		} else {
			params.Set("livingspace", fmt.Sprintf("-%d", profile.MaxArea))
		}
	}

	// Add equipment filters
	var equipment []string
	if profile.HasBalcony != nil && *profile.HasBalcony {
		equipment = append(equipment, "balcony")
	}
	if profile.HasEBK != nil && *profile.HasEBK {
		equipment = append(equipment, "builtinkitchen")
	}
	if profile.HasElevator != nil && *profile.HasElevator {
		equipment = append(equipment, "lift")
	}
	if len(equipment) > 0 {
		params.Set("equipment", strings.Join(equipment, ","))
	}

	// Add postal codes if specified
	if len(profile.PostalCodes) > 0 {
		params.Set("geocodes", strings.Join(profile.PostalCodes, ","))
	}

	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	return u
}

func (c *Client) fetch(ctx context.Context, urlStr string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}

	// Set headers to appear as a real browser
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited (429)")
	}

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("forbidden (403) - possible bot detection")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Handle gzip encoding
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	return io.ReadAll(reader)
}

func (c *Client) setHeaders(req *http.Request) {
	ua := c.uaRotator.Next()
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "de-DE,de;q=0.9")
	// Don't set Accept-Encoding - Go handles gzip automatically when not set
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	// Add cookie header if set
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
}

// parseCookieString parses a cookie header string into http.Cookie objects
func parseCookieString(cookieStr string) []*http.Cookie {
	var cookies []*http.Cookie
	parts := strings.Split(cookieStr, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.Index(part, "=")
		if eq < 0 {
			continue
		}
		name := strings.TrimSpace(part[:eq])
		value := strings.TrimSpace(part[eq+1:])
		cookies = append(cookies, &http.Cookie{
			Name:  name,
			Value: value,
		})
	}
	return cookies
}

// SetCookie updates the client's cookie
func (c *Client) SetCookie(cookie string) error {
	c.cookie = cookie
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(baseURL)
	cookies := parseCookieString(cookie)
	jar.SetCookies(u, cookies)
	c.httpClient.Jar = jar
	return nil
}
