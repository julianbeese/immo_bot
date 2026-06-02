package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/contact"
	"github.com/julianbeese/immo_bot/internal/domain"
	"github.com/julianbeese/immo_bot/internal/filter"
	"github.com/julianbeese/immo_bot/internal/messenger"
	"github.com/julianbeese/immo_bot/internal/repository/sqlite"
)

// IS24Client interface for scraping
type IS24Client interface {
	Search(ctx context.Context, profile *domain.SearchProfile) ([]domain.Listing, error)
	FetchExpose(ctx context.Context, is24ID string) (*domain.Listing, error)
	// SetCookie applies a new IS24 session cookie at runtime so cookies can be
	// rotated without restarting the bot. Implementations may return errors
	// from updating their internal cookie jar.
	SetCookie(cookie string) error
}

// Notifier sends notifications about listings and bot events. Implemented by
// each channel (Telegram, WhatsApp) and fanned out via notifier.Multi.
type Notifier interface {
	NotifyNewListing(ctx context.Context, l *domain.Listing) error
	NotifyContactSent(ctx context.Context, l *domain.Listing) error
	NotifyContactFailed(ctx context.Context, l *domain.Listing, errMsg string) error
	NotifyError(ctx context.Context, errMsg string) error
	NotifyMessagePreview(ctx context.Context, l *domain.Listing, message string) error
	NotifyApprovalRequest(ctx context.Context, l *domain.Listing, message string, sentMessageID int64) error
	SendRawMessage(ctx context.Context, text string) error
	IsEnabled() bool
}

// Scheduler coordinates the search, filter, notify, contact workflow
type Scheduler struct {
	cfg       *config.Config
	repo      *sqlite.Repository
	client    IS24Client
	filter    *filter.Engine
	notifier  Notifier
	campaigns CampaignResolver
	enhancer  MessageEnhancer
	contacter *contact.Submitter
	logger    *slog.Logger

	// Callbacks to check contact mode
	isAutoContactEnabled func() bool
	isTestModeEnabled    func() bool
	isApprovalModeEnabled func() bool
	isQuietHoursEnabled  func() *bool // nil = use config, non-nil = override
	// Returns true if the given time falls inside the active quiet-hours
	// window. When nil, the scheduler falls back to cfg.IsWithinQuietHours.
	isWithinQuietHours func(time.Time) bool

	// Returns the current poll interval. When nil, the scheduler uses
	// cfg.PollInterval (static). Dashboard wires this to control.GetPollInterval
	// so changes take effect on the next tick.
	pollIntervalFn func() time.Duration
	// Receives a ping whenever the poll interval changes; the run loop resets
	// its timer. nil = no live updates (tests / no dashboard).
	pollIntervalReset <-chan struct{}

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	// Cookie-health tracking: consecutive polls where every search returned
	// nothing usually means the IS24 cookie expired.
	emptyPolls  int
	cookieAlert bool
}

// cookieWarnThreshold is the number of consecutive empty/failed polls before
// warning that the IS24 cookie likely expired.
const cookieWarnThreshold = 3

// MessageEnhancer enhances messages (OpenAI integration). campaignPrompt
// overrides the enhancer's default system prompt per campaign.
// ClassifyGender returns SalutationMale / SalutationFemale / SalutationUnknown
// for an Ansprechpartner name; the scheduler caches the result on the listing
// so the call only fires once per listing.
type MessageEnhancer interface {
	Enhance(ctx context.Context, message string, listing *domain.Listing, campaignPrompt string) (string, error)
	ClassifyGender(ctx context.Context, name string) (string, error)
}

// Campaign is the resolved personalization bundle for one search strategy.
type Campaign struct {
	Name      string // campaign key (e.g. "single"), used to look up dashboard overrides
	Generator *messenger.Generator
	AIPrompt  string
	Contact   contact.Profile
}

// testModeCycleLimit caps how many listings test mode notifies/previews per
// poll cycle, so enabling it on a broad search profile can't blast WhatsApp.
const testModeCycleLimit = 3

// CampaignResolver maps a search profile's category to its Campaign.
// Implemented in main from config.Campaigns.
type CampaignResolver interface {
	Resolve(category string) Campaign
}

// NewScheduler creates a new scheduler
func NewScheduler(
	cfg *config.Config,
	repo *sqlite.Repository,
	client IS24Client,
	filterEngine *filter.Engine,
	notifier Notifier,
	campaigns CampaignResolver,
	enhancer MessageEnhancer,
	contacter *contact.Submitter,
	logger *slog.Logger,
) *Scheduler {
	return &Scheduler{
		cfg:                  cfg,
		repo:                 repo,
		client:               client,
		filter:               filterEngine,
		notifier:             notifier,
		campaigns:            campaigns,
		enhancer:             enhancer,
		contacter:            contacter,
		logger:               logger,
		isAutoContactEnabled:  func() bool { return false }, // Default: observation mode
		isTestModeEnabled:     func() bool { return false },
		isApprovalModeEnabled: func() bool { return false },
		isQuietHoursEnabled:   func() *bool { return nil }, // nil = use config
	}
}

// SetAutoContactCallback sets the callback to check if auto-contact is enabled
func (s *Scheduler) SetAutoContactCallback(fn func() bool) {
	s.isAutoContactEnabled = fn
}

// SetTestModeCallback sets the callback to check if test mode is enabled
func (s *Scheduler) SetTestModeCallback(fn func() bool) {
	s.isTestModeEnabled = fn
}

// SetApprovalModeCallback sets the callback to check if approval mode is enabled
// (per-listing confirmation via Telegram inline buttons).
func (s *Scheduler) SetApprovalModeCallback(fn func() bool) {
	s.isApprovalModeEnabled = fn
}

// SetQuietHoursCallback sets the callback to check if quiet hours override is set
func (s *Scheduler) SetQuietHoursCallback(fn func() *bool) {
	s.isQuietHoursEnabled = fn
}

// SetQuietWindowCallback supplies an override for the active quiet-hours
// window. When set, the scheduler uses it instead of cfg.IsWithinQuietHours so
// the start/end times can be changed at runtime (e.g. via the dashboard).
func (s *Scheduler) SetQuietWindowCallback(fn func(time.Time) bool) {
	s.isWithinQuietHours = fn
}

// SetPollIntervalSource wires a dynamic poll-interval source. fn is called
// before each timer reset; reset is pinged when the value changes so the
// current sleep can be cut short. Either may be nil for a static interval.
func (s *Scheduler) SetPollIntervalSource(fn func() time.Duration, reset <-chan struct{}) {
	s.pollIntervalFn = fn
	s.pollIntervalReset = reset
}

// currentPollInterval returns the live interval (callback when set, else
// config fallback). Always > 0 to keep the timer healthy.
func (s *Scheduler) currentPollInterval() time.Duration {
	if s.pollIntervalFn != nil {
		if d := s.pollIntervalFn(); d > 0 {
			return d
		}
	}
	if s.cfg.PollInterval > 0 {
		return s.cfg.PollInterval
	}
	return 5 * time.Minute
}

// SetIS24Cookie hot-reloads the IS24 cookie: applies it to the client (so the
// next scrape uses it), persists it to the meta table (so it survives
// restarts), and clears the cookie-expired warning state. Safe to call from
// any goroutine.
func (s *Scheduler) SetIS24Cookie(ctx context.Context, cookie string) error {
	if err := s.client.SetCookie(cookie); err != nil {
		return fmt.Errorf("apply cookie to client: %w", err)
	}
	if err := s.repo.SetMeta(ctx, sqlite.MetaIS24Cookie, cookie); err != nil {
		// Logged, not fatal — the in-memory cookie is already updated. Without
		// persistence the bot still scrapes correctly until the next restart.
		s.logger.Warn("failed to persist IS24 cookie override", "error", err)
	}
	s.mu.Lock()
	s.emptyPolls = 0
	s.cookieAlert = false
	s.mu.Unlock()
	s.logger.Info("IS24 cookie hot-reloaded", "length", len(cookie))
	return nil
}

// GetStats returns current statistics
func (s *Scheduler) GetStats(ctx context.Context) (total, contacted, notified int) {
	row := s.repo.DB().QueryRowContext(ctx, `
		SELECT COUNT(*), SUM(contacted), SUM(notified) FROM listings
	`)
	row.Scan(&total, &contacted, &notified)
	return
}

// Start begins the polling loop
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.mu.Unlock()

	go s.run(ctx)
	return nil
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopCh)
	s.mu.Unlock()

	<-s.doneCh
}

// RunOnce performs a single poll cycle (useful for testing)
func (s *Scheduler) RunOnce(ctx context.Context) error {
	return s.poll(ctx)
}

func (s *Scheduler) run(ctx context.Context) {
	defer close(s.doneCh)

	// Run immediately on start
	if err := s.poll(ctx); err != nil {
		s.logger.Error("poll failed", "error", err)
		s.notifyError(ctx, err)
	}

	// Timer-based loop instead of a fixed-duration ticker, so the dashboard can
	// change the poll interval at runtime. The reset channel cuts the current
	// sleep short the moment the new value is set.
	timer := time.NewTimer(s.currentPollInterval())
	defer timer.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case <-s.pollIntervalReset:
			// Setting changed mid-sleep — start over with the new value.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			d := s.currentPollInterval()
			s.logger.Info("poll interval changed, resetting timer", "interval", d)
			timer.Reset(d)
		case <-timer.C:
			if err := s.poll(ctx); err != nil {
				s.logger.Error("poll failed", "error", err)
				s.notifyError(ctx, err)
			}
			timer.Reset(s.currentPollInterval())
		}
	}
}

func (s *Scheduler) poll(ctx context.Context) error {
	s.logger.Info("starting poll cycle")

	quietNow := s.quietHoursActive()
	if quietNow {
		s.logger.Info("quiet hours active, deferring outbound messages",
			"start", s.cfg.QuietHours.Start,
			"end", s.cfg.QuietHours.End)
	}

	// Get active search profiles
	profiles, err := s.repo.GetActiveSearchProfiles(ctx)
	if err != nil {
		return err
	}

	s.logger.Info("processing profiles", "count", len(profiles))

	totalRaw, failures := 0, 0
	for _, profile := range profiles {
		raw, err := s.processProfile(ctx, &profile)
		if err != nil {
			s.logger.Error("profile processing failed", "profile", profile.Name, "error", err)
			failures++
			continue // try other profiles
		}
		totalRaw += raw
	}
	s.checkCookieHealth(ctx, len(profiles), totalRaw, failures, quietNow)

	if !quietNow {
		// Process notifications for unnotified listings
		if err := s.sendNotifications(ctx); err != nil {
			s.logger.Error("notification sending failed", "error", err)
		}

		// Process auto-contact for uncontacted listings (only if enabled via Telegram)
		if s.cfg.Contact.Enabled && s.isAutoContactEnabled() {
			s.logger.Info("auto-contact enabled, processing uncontacted listings")
			if err := s.sendContacts(ctx); err != nil {
				s.logger.Error("contact sending failed", "error", err)
			}
		}

		// Process test mode: show message previews without sending
		if s.cfg.Contact.Enabled && s.isTestModeEnabled() {
			s.logger.Info("test mode enabled, showing message previews")
			if err := s.sendTestPreviews(ctx); err != nil {
				s.logger.Error("test preview failed", "error", err)
			}
		}

		// Process approval mode: suggest one listing at a time and wait for
		// user confirmation via Telegram inline buttons.
		if s.cfg.Contact.Enabled && s.isApprovalModeEnabled() {
			if err := s.sendApprovalRequests(ctx); err != nil {
				s.logger.Error("approval request failed", "error", err)
			}
		}
	}

	// Heartbeat for the health check.
	if err := s.repo.SetMeta(ctx, sqlite.MetaLastPollOK, time.Now().UTC().Format(time.RFC3339)); err != nil {
		s.logger.Warn("failed to record poll heartbeat", "error", err)
	}

	s.logger.Info("poll cycle complete")
	return nil
}

func (s *Scheduler) quietHoursActive() bool {
	// Window: controller-supplied override if available, else cfg defaults.
	inWindow := func() bool {
		if s.isWithinQuietHours != nil {
			return s.isWithinQuietHours(time.Now())
		}
		return s.cfg.IsWithinQuietHours()
	}
	quietOverride := s.isQuietHoursEnabled()
	if quietOverride != nil {
		return *quietOverride && inWindow()
	}
	return s.cfg.IsQuietTime()
}

// processProfile searches, filters and stores listings for one profile.
// Returns the number of raw listings the search returned (used to detect a
// likely-expired IS24 cookie when searches keep coming back empty).
// checkCookieHealth warns once when searches keep returning nothing across all
// active profiles (or all fail), the typical symptom of an expired IS24 cookie.
// It resets and clears the warning as soon as listings come back.
func (s *Scheduler) checkCookieHealth(ctx context.Context, profileCount, totalRaw, failures int, quietNow bool) {
	if profileCount == 0 {
		return // nothing to search, not a cookie problem
	}

	// A poll is "empty" if no search returned any listing (all empty or all failed).
	if totalRaw > 0 {
		s.emptyPolls = 0
		s.cookieAlert = false
		return
	}

	s.emptyPolls++
	if s.emptyPolls >= cookieWarnThreshold && !s.cookieAlert {
		if quietNow {
			s.logger.Warn("possible expired IS24 cookie, notification deferred by quiet hours",
				"empty_polls", s.emptyPolls, "failures", failures)
			return
		}
		s.cookieAlert = true
		msg := fmt.Sprintf(
			"⚠️ *Keine Inserate seit %d Durchläufen* (%d/%d Profile mit Fehler).\n\n"+
				"IS24-Cookie evtl. abgelaufen — bitte `IS24_COOKIE` aktualisieren und Bot neu starten.",
			s.emptyPolls, failures, profileCount)
		if s.notifier != nil {
			s.notifier.SendRawMessage(ctx, msg)
		}
		s.logger.Warn("possible expired IS24 cookie", "empty_polls", s.emptyPolls, "failures", failures)
	}
}

func (s *Scheduler) processProfile(ctx context.Context, profile *domain.SearchProfile) (int, error) {
	s.logger.Info("searching", "profile", profile.Name, "city", profile.City)

	// Search IS24
	listings, err := s.client.Search(ctx, profile)
	if err != nil {
		return 0, err
	}

	s.logger.Info("found listings", "count", len(listings), "profile", profile.Name)

	// Filter listings with debug logging
	var filtered []domain.Listing
	for _, l := range listings {
		result := s.filter.Filter(&l, profile)
		if result.Passed {
			filtered = append(filtered, l)
		} else {
			s.logger.Debug("listing filtered", "is24_id", l.IS24ID, "title", l.Title,
				"price", l.Price, "rooms", l.Rooms, "reasons", result.Reasons)
		}
	}
	s.logger.Info("after filtering", "count", len(filtered), "profile", profile.Name)

	// Process each listing
	newCount := 0
	for _, listing := range filtered {
		// Check if already exists
		exists, err := s.repo.ListingExists(ctx, listing.IS24ID)
		if err != nil {
			s.logger.Error("existence check failed", "is24_id", listing.IS24ID, "error", err)
			continue
		}

		if exists {
			continue
		}

		// Optionally fetch full expose details
		detailed, err := s.client.FetchExpose(ctx, listing.IS24ID)
		if err != nil {
			s.logger.Warn("expose fetch failed", "is24_id", listing.IS24ID, "error", err)
			// Use basic listing data
			detailed = &listing
		} else {
			// Preserve search profile ID
			detailed.SearchProfileID = listing.SearchProfileID
		}

		// Guard: don't persist a listing without expose data. The AI enhancer
		// would otherwise hallucinate features (Parkett, moderne Küche, …)
		// out of an empty prompt. Description only populates from a successful
		// expose parse, so it's the clearest "we actually have data" signal —
		// search-result-only fallbacks land here. The listing stays unsaved,
		// so the next polling cycle will retry the fetch (WAF challenges are
		// transient).
		if strings.TrimSpace(detailed.Description) == "" {
			s.logger.Warn("listing skipped: no expose data",
				"is24_id", listing.IS24ID,
				"title", detailed.Title,
				"reason", "empty description after fetch — likely WAF or parser miss")
			continue
		}

		// Re-filter with full details
		if !s.filter.Filter(detailed, profile).Passed {
			s.logger.Debug("listing filtered after detail fetch", "is24_id", listing.IS24ID)
			continue
		}

		// Save to database
		if err := s.repo.CreateListing(ctx, detailed); err != nil {
			s.logger.Error("listing save failed", "is24_id", detailed.IS24ID, "error", err)
			continue
		}

		s.logger.Info("new listing saved", "is24_id", detailed.IS24ID, "title", detailed.Title)
		newCount++

		// Log activity
		s.repo.LogActivity(ctx, &domain.ActivityLog{
			Action:     domain.ActionListingFound,
			EntityType: "listing",
			EntityID:   detailed.ID,
			Details:    detailed.Title,
		})
	}

	s.logger.Info("new listings saved", "count", newCount, "profile", profile.Name)
	return len(listings), nil
}

func (s *Scheduler) sendNotifications(ctx context.Context) error {
	listings, err := s.repo.GetUnnotifiedListings(ctx)
	if err != nil {
		return err
	}

	testMode := s.isTestModeEnabled()
	sent := 0
	for _, listing := range listings {
		if testMode && sent >= testModeCycleLimit {
			s.logger.Info("test mode notification cap reached", "limit", testModeCycleLimit)
			break
		}
		if err := s.notifier.NotifyNewListing(ctx, &listing); err != nil {
			s.logger.Error("notification failed", "is24_id", listing.IS24ID, "error", err)
			continue
		}
		sent++

		if err := s.repo.MarkListingNotified(ctx, listing.ID); err != nil {
			s.logger.Error("mark notified failed", "id", listing.ID, "error", err)
		}

		s.repo.LogActivity(ctx, &domain.ActivityLog{
			Action:     domain.ActionNotificationSent,
			EntityType: "listing",
			EntityID:   listing.ID,
		})
	}

	return nil
}

// campaignFor resolves the campaign for a listing via its search profile's
// category, falling back to the default campaign when the profile or category
// is missing.
func (s *Scheduler) campaignFor(ctx context.Context, listing *domain.Listing) Campaign {
	category := ""
	if listing.SearchProfileID != 0 {
		if p, err := s.repo.GetSearchProfileByID(ctx, listing.SearchProfileID); err == nil {
			category = p.Category
		} else {
			s.logger.Warn("profile lookup failed, using default campaign",
				"search_profile_id", listing.SearchProfileID, "error", err)
		}
	}
	return s.applyCampaignOverrides(ctx, s.campaigns.Resolve(category))
}

// applyCampaignOverrides layers dashboard-edited AI prompt / message template
// (persisted in the meta table) over the config-derived campaign. Empty or
// missing overrides leave the config defaults untouched.
func (s *Scheduler) applyCampaignOverrides(ctx context.Context, camp Campaign) Campaign {
	if camp.Name == "" {
		return camp
	}
	if prompt, err := s.repo.GetMeta(ctx, sqlite.CampaignPromptKey(camp.Name)); err == nil && prompt != "" {
		camp.AIPrompt = prompt
	}
	if tmpl, err := s.repo.GetMeta(ctx, sqlite.CampaignTemplateKey(camp.Name)); err == nil && tmpl != "" {
		if gen, err := messenger.NewGeneratorFromText(tmpl); err == nil {
			camp.Generator = gen
		} else {
			s.logger.Warn("invalid campaign template override, using default", "campaign", camp.Name, "error", err)
		}
	}
	return camp
}

// ensureSalutation classifies the listing's Ansprechpartner gender via OpenAI
// once and caches the result in the DB. No-op when the listing has no contact
// person or already has a cached salutation. Errors are logged and the
// listing's salutation stays unknown (template falls back to "Sehr geehrte
// Damen und Herren,"). Always returns the resolved listing state so callers
// can render the message right away.
func (s *Scheduler) ensureSalutation(ctx context.Context, listing *domain.Listing) {
	if listing.ContactPerson == "" || listing.ContactSalutation != "" {
		return
	}
	if s.enhancer == nil {
		return
	}
	gender, err := s.enhancer.ClassifyGender(ctx, listing.ContactPerson)
	if err != nil {
		s.logger.Warn("gender classification failed",
			"is24_id", listing.IS24ID,
			"contact_person", listing.ContactPerson,
			"error", err)
		gender = domain.SalutationUnknown
	}
	listing.ContactSalutation = gender
	if err := s.repo.UpdateListingContact(ctx, listing.ID, listing.ContactPerson, gender); err != nil {
		s.logger.Warn("failed to cache contact salutation",
			"id", listing.ID, "error", err)
	}
}

func (s *Scheduler) sendContacts(ctx context.Context) error {
	if s.contacter == nil {
		return nil
	}

	listings, err := s.repo.GetUncontactedListings(ctx)
	if err != nil {
		return err
	}

	for _, listing := range listings {
		if !s.isReachable(&listing) {
			s.logger.Info("auto-contact skipped: exclusive expose (Suchen+ paywall)",
				"is24_id", listing.IS24ID, "title", listing.Title)
			if err := s.repo.MarkListingContacted(ctx, listing.ID); err != nil {
				s.logger.Warn("failed to mark exclusive listing contacted", "id", listing.ID, "error", err)
			}
			continue
		}
		camp := s.campaignFor(ctx, &listing)

		// Classify Ansprechpartner gender (cached) so the template renders the
		// personalized salutation. Falls back silently to gender-neutral.
		s.ensureSalutation(ctx, &listing)

		// Generate message
		message, err := camp.Generator.Generate(&listing)
		if err != nil {
			s.logger.Error("message generation failed", "is24_id", listing.IS24ID, "error", err)
			continue
		}

		// Enhance with AI if available
		if s.enhancer != nil {
			enhanced, err := s.enhancer.Enhance(ctx, message, &listing, camp.AIPrompt)
			if err != nil {
				s.logger.Warn("message enhancement failed, using base message", "error", err)
			} else {
				message = enhanced
			}
		}

		// Record message attempt
		sentMsg := &domain.SentMessage{
			ListingID: listing.ID,
			IS24ID:    listing.IS24ID,
			Message:   message,
			Status:    domain.MessageStatusPending,
		}
		if err := s.repo.CreateSentMessage(ctx, sentMsg); err != nil {
			s.logger.Error("message record failed", "error", err)
		}

		// Submit contact form
		if err := s.contacter.Submit(ctx, &listing, message, camp.Contact); err != nil {
			s.logger.Error("contact submission failed", "is24_id", listing.IS24ID, "error", err)
			s.repo.UpdateSentMessageStatus(ctx, sentMsg.ID, domain.MessageStatusFailed, err.Error())
			s.notifier.NotifyContactFailed(ctx, &listing, err.Error())

			s.repo.LogActivity(ctx, &domain.ActivityLog{
				Action:     domain.ActionContactFailed,
				EntityType: "listing",
				EntityID:   listing.ID,
				ErrorMsg:   err.Error(),
			})
			continue
		}

		// Mark as contacted
		if err := s.repo.MarkListingContacted(ctx, listing.ID); err != nil {
			s.logger.Error("mark contacted failed", "id", listing.ID, "error", err)
		}

		s.repo.UpdateSentMessageStatus(ctx, sentMsg.ID, domain.MessageStatusSent, "")
		s.notifier.NotifyContactSent(ctx, &listing)

		s.repo.LogActivity(ctx, &domain.ActivityLog{
			Action:     domain.ActionContactSent,
			EntityType: "listing",
			EntityID:   listing.ID,
		})

		s.logger.Info("contact sent", "is24_id", listing.IS24ID)
	}

	return nil
}

func (s *Scheduler) sendTestPreviews(ctx context.Context) error {
	listings, err := s.repo.GetPreviewableListings(ctx)
	if err != nil {
		return err
	}
	if len(listings) > testModeCycleLimit {
		listings = listings[:testModeCycleLimit]
	}

	for _, listing := range listings {
		camp := s.campaignFor(ctx, &listing)

		// Classify Ansprechpartner gender (cached) so the template renders the
		// personalized salutation. Falls back silently to gender-neutral.
		s.ensureSalutation(ctx, &listing)

		// Generate message
		message, err := camp.Generator.Generate(&listing)
		if err != nil {
			s.logger.Error("message generation failed", "is24_id", listing.IS24ID, "error", err)
			continue
		}

		// Enhance with AI if available
		if s.enhancer != nil {
			enhanced, err := s.enhancer.Enhance(ctx, message, &listing, camp.AIPrompt)
			if err != nil {
				s.logger.Warn("message enhancement failed, using base message", "error", err)
			} else {
				message = enhanced
			}
		}

		// Send preview to Telegram
		if err := s.notifier.NotifyMessagePreview(ctx, &listing, message); err != nil {
			s.logger.Error("message preview notification failed", "is24_id", listing.IS24ID, "error", err)
			continue
		}

		previewMsg := &domain.SentMessage{
			ListingID: listing.ID,
			IS24ID:    listing.IS24ID,
			Message:   message,
			Status:    domain.MessageStatusPreview,
		}
		if err := s.repo.CreateSentMessage(ctx, previewMsg); err != nil {
			s.logger.Error("preview record failed", "is24_id", listing.IS24ID, "error", err)
		}

		s.repo.LogActivity(ctx, &domain.ActivityLog{
			Action:     domain.ActionNotificationSent,
			EntityType: "listing",
			EntityID:   listing.ID,
			Details:    "test_mode_preview",
		})

		s.logger.Info("test preview sent", "is24_id", listing.IS24ID)
	}

	return nil
}

func (s *Scheduler) notifyError(ctx context.Context, err error) {
	if s.notifier != nil {
		s.notifier.NotifyError(ctx, err.Error())
	}
}

// approvalRejectCooldown is the minimum age of a rejection before the same
// listing becomes eligible for a new approval suggestion. Keeps the bot from
// re-proposing the same listing immediately after ❌, but doesn't trap the
// listing forever — useful when the user wants a regenerated message later.
const approvalRejectCooldown = 6 * time.Hour

// sendApprovalRequests proposes the next eligible listing in approval mode.
// Strict sequential: skip the cycle if any pending_approval is in flight.
func (s *Scheduler) sendApprovalRequests(ctx context.Context) error {
	pending, err := s.repo.CountPendingApprovals(ctx)
	if err != nil {
		return fmt.Errorf("count pending approvals: %w", err)
	}
	if pending > 0 {
		s.logger.Debug("approval in flight, skipping", "pending", pending)
		return nil
	}

	listing, err := s.repo.GetNextApprovableListing(ctx, approvalRejectCooldown)
	if err != nil {
		return fmt.Errorf("next approvable: %w", err)
	}
	if listing == nil {
		return nil // queue empty
	}
	if !s.isReachable(listing) {
		// Listing is Suchen+-exclusive — can't actually message. Mark contacted
		// so it leaves the queue and the user isn't pestered with an unactionable
		// approval card.
		s.logger.Info("approval skipped: exclusive expose (Suchen+ paywall)",
			"is24_id", listing.IS24ID, "title", listing.Title)
		if err := s.repo.MarkListingContacted(ctx, listing.ID); err != nil {
			s.logger.Warn("failed to mark exclusive listing contacted", "id", listing.ID, "error", err)
		}
		return nil
	}

	camp := s.campaignFor(ctx, listing)
	s.ensureSalutation(ctx, listing)

	message, err := camp.Generator.Generate(listing)
	if err != nil {
		return fmt.Errorf("generate message for %s: %w", listing.IS24ID, err)
	}
	if s.enhancer != nil {
		if enhanced, err := s.enhancer.Enhance(ctx, message, listing, camp.AIPrompt); err == nil {
			message = enhanced
		} else {
			s.logger.Warn("message enhancement failed, using base message", "error", err)
		}
	}

	sentMsg := &domain.SentMessage{
		ListingID: listing.ID,
		IS24ID:    listing.IS24ID,
		Message:   message,
		Status:    domain.MessageStatusPendingApproval,
	}
	if err := s.repo.CreateSentMessage(ctx, sentMsg); err != nil {
		return fmt.Errorf("record pending approval: %w", err)
	}

	if err := s.notifier.NotifyApprovalRequest(ctx, listing, message, sentMsg.ID); err != nil {
		// Roll back the pending row so the listing comes back next cycle —
		// otherwise a transient Telegram failure would lock the queue forever.
		_ = s.repo.UpdateSentMessageStatus(ctx, sentMsg.ID, domain.MessageStatusFailed, err.Error())
		return fmt.Errorf("send approval request: %w", err)
	}

	s.logger.Info("approval request sent",
		"is24_id", listing.IS24ID, "sent_message_id", sentMsg.ID, "title", listing.Title)
	return nil
}

// isReachable reports whether the listing is contactable at all. IS24's
// "Suchen+ exclusive" listings paywall the contact form behind a subscription
// — submitting against them will just dump us on a Plus landing page. We
// never auto-send or auto-suggest such listings; the user can still see them
// in the dashboard with the paywalled badge.
func (s *Scheduler) isReachable(l *domain.Listing) bool {
	return !l.ExclusiveExpose
}

// OnApprove fulfils an approval: looks up the pending sent_message, submits
// the contact form, updates statuses, and notifies the user of the outcome.
// Safe to call multiple times — only the first call for a given pending row
// will trigger the contact submit; subsequent calls are no-ops.
func (s *Scheduler) OnApprove(ctx context.Context, sentMessageID int64) error {
	sm, err := s.repo.GetSentMessage(ctx, sentMessageID)
	if err != nil {
		return fmt.Errorf("load sent message: %w", err)
	}
	if sm == nil {
		return fmt.Errorf("sent message %d not found", sentMessageID)
	}
	if sm.Status != domain.MessageStatusPendingApproval {
		s.logger.Info("approve ignored, not pending", "sent_message_id", sentMessageID, "status", sm.Status)
		return nil
	}

	listing, err := s.repo.GetListingByID(ctx, sm.ListingID)
	if err != nil {
		return fmt.Errorf("load listing: %w", err)
	}
	if listing == nil {
		return fmt.Errorf("listing %d not found", sm.ListingID)
	}

	if s.contacter == nil {
		return fmt.Errorf("contact submitter not configured — approval mode requires CONTACT_ENABLED=true")
	}

	camp := s.campaignFor(ctx, listing)

	// Mark in-flight before the submit so a duplicate click can't double-send.
	if err := s.repo.UpdateSentMessageStatus(ctx, sm.ID, domain.MessageStatusPending, ""); err != nil {
		return fmt.Errorf("mark pending: %w", err)
	}

	if err := s.contacter.Submit(ctx, listing, sm.Message, camp.Contact); err != nil {
		s.repo.UpdateSentMessageStatus(ctx, sm.ID, domain.MessageStatusFailed, err.Error())
		s.notifier.NotifyContactFailed(ctx, listing, err.Error())
		s.repo.LogActivity(ctx, &domain.ActivityLog{
			Action: domain.ActionContactFailed, EntityType: "listing",
			EntityID: listing.ID, ErrorMsg: err.Error(),
		})
		return err
	}

	if err := s.repo.MarkListingContacted(ctx, listing.ID); err != nil {
		s.logger.Error("mark contacted failed", "id", listing.ID, "error", err)
	}
	s.repo.UpdateSentMessageStatus(ctx, sm.ID, domain.MessageStatusSent, "")
	s.notifier.NotifyContactSent(ctx, listing)
	s.repo.LogActivity(ctx, &domain.ActivityLog{
		Action: domain.ActionContactSent, EntityType: "listing", EntityID: listing.ID,
	})
	s.logger.Info("approval -> contact sent", "is24_id", listing.IS24ID)
	return nil
}

// OnReject marks the approval as rejected. The listing stays in the queue but
// becomes eligible again only after approvalRejectCooldown — so the bot won't
// immediately re-propose the same listing on the very next poll.
func (s *Scheduler) OnReject(ctx context.Context, sentMessageID int64) error {
	sm, err := s.repo.GetSentMessage(ctx, sentMessageID)
	if err != nil {
		return fmt.Errorf("load sent message: %w", err)
	}
	if sm == nil {
		return fmt.Errorf("sent message %d not found", sentMessageID)
	}
	if sm.Status != domain.MessageStatusPendingApproval {
		s.logger.Info("reject ignored, not pending", "sent_message_id", sentMessageID, "status", sm.Status)
		return nil
	}
	if err := s.repo.UpdateSentMessageStatus(ctx, sm.ID, domain.MessageStatusRejected, ""); err != nil {
		return err
	}
	s.logger.Info("approval rejected", "sent_message_id", sentMessageID, "is24_id", sm.IS24ID)
	return nil
}
