package contact

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/julianbeese/immo_bot/internal/antidetect"
	"github.com/julianbeese/immo_bot/internal/domain"
)

// Submitter handles contact form submission via browser automation
type Submitter struct {
	cookie      string
	behavior    *antidetect.HumanBehavior
	senderName  string
	senderEmail string
	senderPhone string
	chromePath  string
}

// NewSubmitter creates a new contact form submitter
func NewSubmitter(cookie, senderName, senderEmail, senderPhone, chromePath string, behavior *antidetect.HumanBehavior) *Submitter {
	return &Submitter{
		cookie:      cookie,
		behavior:    behavior,
		senderName:  senderName,
		senderEmail: senderEmail,
		senderPhone: senderPhone,
		chromePath:  chromePath,
	}
}

// Submit fills and submits the IS24 contact form for a listing
func (s *Submitter) Submit(ctx context.Context, listing *domain.Listing, message string) error {
	// Create browser context with options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	if s.chromePath != "" {
		opts = append(opts, chromedp.ExecPath(s.chromePath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	// Set timeout
	browserCtx, cancel := context.WithTimeout(browserCtx, 2*time.Minute)
	defer cancel()

	// Build contact URL
	contactURL := listing.ContactFormURL
	if contactURL == "" {
		contactURL = fmt.Sprintf("https://www.immobilienscout24.de/expose/%s#/basicContact/email", listing.IS24ID)
	}

	// Execute contact form submission
	err := chromedp.Run(browserCtx,
		// Set cookies if available
		s.setCookies(),

		// Navigate to contact form
		chromedp.Navigate(contactURL),

		// Wait for page to load
		chromedp.Sleep(s.behavior.ThinkPause()),

		// Wait for form to be visible
		chromedp.WaitVisible(`form[data-qa="contactForm"], .contact-form, #contactForm`, chromedp.ByQuery),

		// Fill in the form with human-like delays
		s.fillFormWithDelay(message),

		// Submit the form
		s.submitForm(),

		// Wait for confirmation
		chromedp.Sleep(2*time.Second),
	)

	if err != nil {
		return fmt.Errorf("browser automation failed: %w", err)
	}

	return nil
}

func (s *Submitter) setCookies() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		if s.cookie == "" {
			return nil
		}

		// Parse cookie string and set via network domain
		cookies := parseCookieString(s.cookie)
		for _, cookie := range cookies {
			err := network.SetCookie(cookie.Name, cookie.Value).
				WithDomain(".immobilienscout24.de").
				WithPath("/").
				Do(ctx)
			if err != nil {
				return fmt.Errorf("set cookie %s: %w", cookie.Name, err)
			}
		}
		return nil
	}
}

// Cookie represents a parsed cookie
type Cookie struct {
	Name  string
	Value string
}

// parseCookieString parses a cookie header string into individual cookies
func parseCookieString(cookieStr string) []Cookie {
	var cookies []Cookie
	pairs := strings.Split(cookieStr, ";")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := strings.Index(pair, "=")
		if idx > 0 {
			name := strings.TrimSpace(pair[:idx])
			value := strings.TrimSpace(pair[idx+1:])
			cookies = append(cookies, Cookie{Name: name, Value: value})
		}
	}
	return cookies
}

func (s *Submitter) fillFormWithDelay(message string) chromedp.ActionFunc {
	return func(ctx context.Context) error {
		// Try to select "Mit Profil bewerben" (Apply with profile) if available
		profileSelectors := []string{
			`input[name="applyWithProfile"][value="true"]`,
			`input[type="radio"][value="true"]`,
			`label:contains("Mit Profil") input`,
			`input[data-qa="applyWithProfile"]`,
		}
		for _, sel := range profileSelectors {
			_ = chromedp.Run(ctx, chromedp.Click(sel, chromedp.ByQuery))
		}

		time.Sleep(s.behavior.ActionPause())

		// Select Anrede (Salutation) - "Frau" (Mrs.)
		anredeSelectors := []string{
			`select[name="salutation"]`,
			`select[name="contactFormMessage.salutation"]`,
			`select[data-qa="salutation"]`,
			`.is24qa-salutation select`,
		}
		for _, sel := range anredeSelectors {
			// Try to select "Frau" option
			err := chromedp.Run(ctx,
				chromedp.SetValue(sel, "FEMALE", chromedp.ByQuery),
			)
			if err == nil {
				break
			}
			// Alternative: try clicking and selecting
			_ = chromedp.Run(ctx,
				chromedp.Click(sel, chromedp.ByQuery),
				chromedp.Sleep(200*time.Millisecond),
				chromedp.SendKeys(sel, "Frau", chromedp.ByQuery),
			)
		}

		time.Sleep(s.behavior.ActionPause())

		// Try different form field selectors (IS24 changes these)
		nameSelectors := []string{
			`input[name="contactFormMessage.fullName"]`,
			`input[name="name"]`,
			`#contactForm-name`,
			`.is24qa-name input`,
			`input[data-qa="fullName"]`,
		}

		emailSelectors := []string{
			`input[name="contactFormMessage.emailAddress"]`,
			`input[name="email"]`,
			`#contactForm-email`,
			`.is24qa-email input`,
			`input[type="email"]`,
			`input[data-qa="emailAddress"]`,
		}

		phoneSelectors := []string{
			`input[name="contactFormMessage.phoneNumber"]`,
			`input[name="phone"]`,
			`#contactForm-phone`,
			`.is24qa-phone input`,
			`input[type="tel"]`,
			`input[data-qa="phoneNumber"]`,
		}

		messageSelectors := []string{
			`textarea[name="contactFormMessage.message"]`,
			`textarea[name="message"]`,
			`#contactForm-message`,
			`.is24qa-message textarea`,
			`textarea[data-qa="message"]`,
			`textarea`,
		}

		// Fill name
		if s.senderName != "" {
			for _, sel := range nameSelectors {
				if err := s.typeWithDelay(ctx, sel, s.senderName); err == nil {
					break
				}
			}
		}

		time.Sleep(s.behavior.ActionPause())

		// Fill email
		if s.senderEmail != "" {
			for _, sel := range emailSelectors {
				if err := s.typeWithDelay(ctx, sel, s.senderEmail); err == nil {
					break
				}
			}
		}

		time.Sleep(s.behavior.ActionPause())

		// Fill phone
		if s.senderPhone != "" {
			for _, sel := range phoneSelectors {
				if err := s.typeWithDelay(ctx, sel, s.senderPhone); err == nil {
					break
				}
			}
		}

		time.Sleep(s.behavior.ActionPause())

		// Fill message
		for _, sel := range messageSelectors {
			if err := s.typeWithDelay(ctx, sel, message); err == nil {
				break
			}
		}

		return nil
	}
}

func (s *Submitter) typeWithDelay(ctx context.Context, selector, text string) error {
	// First check if element exists
	var exists bool
	err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector('%s') !== null`, selector), &exists),
	)
	if err != nil || !exists {
		return fmt.Errorf("element not found: %s", selector)
	}

	// Clear field first
	err = chromedp.Run(ctx,
		chromedp.Clear(selector, chromedp.ByQuery),
	)
	if err != nil {
		return err
	}

	// Focus the element
	err = chromedp.Run(ctx,
		chromedp.Focus(selector, chromedp.ByQuery),
	)
	if err != nil {
		return err
	}

	// Type character by character with delays
	for _, char := range text {
		err = chromedp.Run(ctx,
			chromedp.SendKeys(selector, string(char), chromedp.ByQuery),
		)
		if err != nil {
			return err
		}
		time.Sleep(s.behavior.TypeChar())
	}

	return nil
}

func (s *Submitter) submitForm() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		// Try different submit button selectors
		submitSelectors := []string{
			`button[data-qa="sendButton"]`,
			`button[type="submit"]`,
			`input[type="submit"]`,
			`.is24qa-submit`,
			`button.button-primary`,
			`button:contains("Nachricht senden")`,
			`button:contains("Absenden")`,
		}

		time.Sleep(s.behavior.ThinkPause())

		for _, sel := range submitSelectors {
			err := chromedp.Run(ctx,
				chromedp.Click(sel, chromedp.ByQuery),
			)
			if err == nil {
				return nil
			}
		}

		// Fallback: try submitting the form directly
		return chromedp.Run(ctx,
			chromedp.Evaluate(`document.querySelector('form').submit()`, nil),
		)
	}
}
