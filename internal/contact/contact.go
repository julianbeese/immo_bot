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

// Profile contains applicant information
type Profile struct {
	Salutation    string
	FirstName     string
	LastName      string
	Email         string
	Phone         string
	Street        string
	HouseNumber   string
	PostalCode    string
	City          string
	Adults        int
	Children      int
	Pets          bool
	Income        int
	MoveInDate    string
	Employment    string
	RentArrears   bool
	Insolvency    bool
	Smoker        bool
	CommercialUse bool
}

// Submitter handles contact form submission via browser automation
type Submitter struct {
	cookie     string
	behavior   *antidetect.HumanBehavior
	profile    Profile
	chromePath string
}

// NewSubmitter creates a new contact form submitter
func NewSubmitter(cookie string, profile Profile, chromePath string, behavior *antidetect.HumanBehavior) *Submitter {
	return &Submitter{
		cookie:     cookie,
		behavior:   behavior,
		profile:    profile,
		chromePath: chromePath,
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
		p := s.profile

		// Try to select "Mit Profil bewerben" (Apply with profile) if available
		s.tryClick(ctx, []string{
			`input[name="applyWithProfile"][value="true"]`,
			`input[type="radio"][value="true"]`,
			`label:contains("Mit Profil") input`,
			`input[data-qa="applyWithProfile"]`,
		})
		time.Sleep(s.behavior.ActionPause())

		// Select Anrede (Salutation)
		s.trySelect(ctx, []string{
			`select[name="salutation"]`,
			`select[name="contactFormMessage.salutation"]`,
			`select[data-qa="salutation"]`,
		}, p.Salutation)
		time.Sleep(s.behavior.ActionPause())

		// Fill Vorname (First name)
		s.tryType(ctx, []string{
			`input[name="firstName"]`,
			`input[name="contactFormMessage.firstName"]`,
			`input[data-qa="firstName"]`,
		}, p.FirstName)

		// Fill Nachname (Last name)
		s.tryType(ctx, []string{
			`input[name="lastName"]`,
			`input[name="contactFormMessage.lastName"]`,
			`input[data-qa="lastName"]`,
		}, p.LastName)

		// Fill full name if separate fields don't exist
		fullName := p.FirstName + " " + p.LastName
		s.tryType(ctx, []string{
			`input[name="contactFormMessage.fullName"]`,
			`input[name="name"]`,
			`input[data-qa="fullName"]`,
		}, fullName)

		// Fill Email
		s.tryType(ctx, []string{
			`input[name="contactFormMessage.emailAddress"]`,
			`input[name="email"]`,
			`input[type="email"]`,
			`input[data-qa="emailAddress"]`,
		}, p.Email)

		// Fill Telefon
		s.tryType(ctx, []string{
			`input[name="contactFormMessage.phoneNumber"]`,
			`input[name="phone"]`,
			`input[type="tel"]`,
			`input[data-qa="phoneNumber"]`,
		}, p.Phone)

		// Fill Straße (Street)
		s.tryType(ctx, []string{
			`input[name="street"]`,
			`input[name="contactFormMessage.street"]`,
			`input[data-qa="street"]`,
		}, p.Street)

		// Fill Hausnummer (House number)
		s.tryType(ctx, []string{
			`input[name="houseNumber"]`,
			`input[name="contactFormMessage.houseNumber"]`,
			`input[data-qa="houseNumber"]`,
		}, p.HouseNumber)

		// Fill PLZ (Postal code)
		s.tryType(ctx, []string{
			`input[name="postalCode"]`,
			`input[name="zipCode"]`,
			`input[name="contactFormMessage.postalCode"]`,
			`input[data-qa="postalCode"]`,
		}, p.PostalCode)

		// Fill Ort (City)
		s.tryType(ctx, []string{
			`input[name="city"]`,
			`input[name="contactFormMessage.city"]`,
			`input[data-qa="city"]`,
		}, p.City)

		// Fill Anzahl Erwachsene (Adults)
		s.tryType(ctx, []string{
			`input[name="numberOfAdults"]`,
			`input[name="adults"]`,
			`input[data-qa="numberOfAdults"]`,
		}, fmt.Sprintf("%d", p.Adults))

		// Fill Anzahl Kinder (Children)
		s.tryType(ctx, []string{
			`input[name="numberOfChildren"]`,
			`input[name="children"]`,
			`input[data-qa="numberOfChildren"]`,
		}, fmt.Sprintf("%d", p.Children))

		// Haustiere (Pets) - select No
		if !p.Pets {
			s.tryClick(ctx, []string{
				`input[name="pets"][value="false"]`,
				`input[name="hasPets"][value="NO"]`,
				`input[data-qa="pets-no"]`,
			})
			s.trySelect(ctx, []string{
				`select[name="pets"]`,
				`select[name="hasPets"]`,
			}, "NO")
		}

		// Fill Einkommen (Income)
		s.tryType(ctx, []string{
			`input[name="income"]`,
			`input[name="monthlyIncome"]`,
			`input[name="netHouseholdIncome"]`,
			`input[data-qa="income"]`,
		}, fmt.Sprintf("%d", p.Income))

		// Fill Einzugstermin (Move-in date)
		s.tryType(ctx, []string{
			`input[name="moveInDate"]`,
			`input[name="earliestMoveInDate"]`,
			`input[data-qa="moveInDate"]`,
		}, p.MoveInDate)
		s.trySelect(ctx, []string{
			`select[name="moveInDate"]`,
			`select[name="earliestMoveInDate"]`,
		}, "FLEXIBLE")

		// Beschäftigungsstatus (Employment)
		s.trySelect(ctx, []string{
			`select[name="employmentStatus"]`,
			`select[name="employment"]`,
			`select[data-qa="employmentStatus"]`,
		}, "PERMANENT")

		// Mietrückstände (Rent arrears) - No
		if !p.RentArrears {
			s.tryClick(ctx, []string{
				`input[name="rentArrears"][value="false"]`,
				`input[name="hasRentArrears"][value="NO"]`,
				`input[data-qa="rentArrears-no"]`,
			})
			s.trySelect(ctx, []string{
				`select[name="rentArrears"]`,
			}, "NO")
		}

		// Insolvenzverfahren (Insolvency) - No
		if !p.Insolvency {
			s.tryClick(ctx, []string{
				`input[name="insolvency"][value="false"]`,
				`input[name="hasInsolvency"][value="NO"]`,
				`input[data-qa="insolvency-no"]`,
			})
			s.trySelect(ctx, []string{
				`select[name="insolvency"]`,
			}, "NO")
		}

		// Raucher (Smoker) - No
		if !p.Smoker {
			s.tryClick(ctx, []string{
				`input[name="smoker"][value="false"]`,
				`input[name="isSmoker"][value="NO"]`,
				`input[data-qa="smoker-no"]`,
			})
			s.trySelect(ctx, []string{
				`select[name="smoker"]`,
			}, "NO")
		}

		// Gewerbliche Nutzung (Commercial use) - No
		if !p.CommercialUse {
			s.tryClick(ctx, []string{
				`input[name="commercialUse"][value="false"]`,
				`input[name="isCommercialUse"][value="NO"]`,
				`input[data-qa="commercialUse-no"]`,
			})
			s.trySelect(ctx, []string{
				`select[name="commercialUse"]`,
			}, "NO")
		}

		time.Sleep(s.behavior.ActionPause())

		// Fill message (always last)
		s.tryType(ctx, []string{
			`textarea[name="contactFormMessage.message"]`,
			`textarea[name="message"]`,
			`textarea[data-qa="message"]`,
			`textarea`,
		}, message)

		return nil
	}
}

// Helper: try to click any of the selectors
func (s *Submitter) tryClick(ctx context.Context, selectors []string) {
	for _, sel := range selectors {
		_ = chromedp.Run(ctx, chromedp.Click(sel, chromedp.ByQuery))
	}
}

// Helper: try to select value in any of the selectors
func (s *Submitter) trySelect(ctx context.Context, selectors []string, value string) {
	for _, sel := range selectors {
		_ = chromedp.Run(ctx, chromedp.SetValue(sel, value, chromedp.ByQuery))
	}
}

// Helper: try to type in any of the selectors
func (s *Submitter) tryType(ctx context.Context, selectors []string, value string) {
	if value == "" {
		return
	}
	for _, sel := range selectors {
		if err := s.typeWithDelay(ctx, sel, value); err == nil {
			time.Sleep(s.behavior.ActionPause())
			return
		}
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
