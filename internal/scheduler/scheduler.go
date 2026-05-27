package scheduler

import (
	"context"
	"fmt"
	"log/slog"
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
}

// Notifier sends notifications about listings and bot events. Implemented by
// each channel (Telegram, WhatsApp) and fanned out via notifier.Multi.
type Notifier interface {
	NotifyNewListing(ctx context.Context, l *domain.Listing) error
	NotifyContactSent(ctx context.Context, l *domain.Listing) error
	NotifyContactFailed(ctx context.Context, l *domain.Listing, errMsg string) error
	NotifyError(ctx context.Context, errMsg string) error
	NotifyMessagePreview(ctx context.Context, l *domain.Listing, message string) error
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
	isQuietHoursEnabled  func() *bool // nil = use config, non-nil = override

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
type MessageEnhancer interface {
	Enhance(ctx context.Context, message string, listing *domain.Listing, campaignPrompt string) (string, error)
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
		isAutoContactEnabled: func() bool { return false }, // Default: observation mode
		isTestModeEnabled:    func() bool { return false },
		isQuietHoursEnabled:  func() *bool { return nil }, // nil = use config
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

// SetQuietHoursCallback sets the callback to check if quiet hours override is set
func (s *Scheduler) SetQuietHoursCallback(fn func() *bool) {
	s.isQuietHoursEnabled = fn
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

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.poll(ctx); err != nil {
				s.logger.Error("poll failed", "error", err)
				s.notifyError(ctx, err)
			}
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
	}

	// Heartbeat for the health check.
	if err := s.repo.SetMeta(ctx, sqlite.MetaLastPollOK, time.Now().UTC().Format(time.RFC3339)); err != nil {
		s.logger.Warn("failed to record poll heartbeat", "error", err)
	}

	s.logger.Info("poll cycle complete")
	return nil
}

func (s *Scheduler) quietHoursActive() bool {
	quietOverride := s.isQuietHoursEnabled()
	if quietOverride != nil {
		return *quietOverride && s.cfg.IsWithinQuietHours()
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

func (s *Scheduler) sendContacts(ctx context.Context) error {
	if s.contacter == nil {
		return nil
	}

	listings, err := s.repo.GetUncontactedListings(ctx)
	if err != nil {
		return err
	}

	for _, listing := range listings {
		camp := s.campaignFor(ctx, &listing)

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
