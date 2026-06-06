package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/julianbeese/immo_bot/internal/antidetect"
	"github.com/julianbeese/immo_bot/internal/backup"
	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/contact"
	"github.com/julianbeese/immo_bot/internal/control"
	"github.com/julianbeese/immo_bot/internal/domain"
	"github.com/julianbeese/immo_bot/internal/email"
	"github.com/julianbeese/immo_bot/internal/filter"
	"github.com/julianbeese/immo_bot/internal/messenger"
	"github.com/julianbeese/immo_bot/internal/notifier"
	"github.com/julianbeese/immo_bot/internal/notifier/telegram"
	"github.com/julianbeese/immo_bot/internal/notifier/whatsapp"
	"github.com/julianbeese/immo_bot/internal/repository/sqlite"
	"github.com/julianbeese/immo_bot/internal/scheduler"
	"github.com/julianbeese/immo_bot/internal/scraper/is24"
	"github.com/julianbeese/immo_bot/internal/secrets"
	"github.com/julianbeese/immo_bot/internal/web"
)

func main() {
	// Load .env file if present (ignores error if not found)
	_ = godotenv.Load()                   // .env in current directory
	_ = godotenv.Load("deployments/.env") // fallback to deployments/.env

	// Parse command line flags
	configPath := flag.String("config", "configs/config.yaml", "Path to configuration file")
	runOnce := flag.Bool("once", false, "Run a single poll cycle and exit")
	healthcheck := flag.Bool("healthcheck", false, "Check poll heartbeat freshness and exit (0=healthy)")
	backfill := flag.Bool("backfill", false, "One-time seed: scrape paginated search results for every active profile, record every IS24 ID as already-known (notified=1, contacted=1, backfilled=1) so the next poll only notifies on genuinely new listings. Existing listings are never overwritten. Exits after seeding.")
	backfillPages := flag.Int("backfill-pages", 30, "Maximum result pages per profile during -backfill (each page ≈ 20 listings)")
	flag.Parse()

	// Setup logging
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// Health check mode: report whether the last poll is recent enough, then exit.
	if *healthcheck {
		os.Exit(runHealthCheck(cfg))
	}

	logger.Info("configuration loaded",
		"poll_interval", cfg.PollInterval,
		"telegram_enabled", cfg.Telegram.Enabled,
		"openai_enabled", cfg.OpenAI.Enabled,
		"contact_enabled", cfg.Contact.Enabled,
	)

	// Ensure data directory exists
	dataDir := filepath.Dir(cfg.DatabasePath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logger.Error("failed to create data directory", "error", err)
		os.Exit(1)
	}

	// Initialize repository
	repo, err := sqlite.New(cfg.DatabasePath)
	if err != nil {
		logger.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer repo.Close()

	logger.Info("database initialized", "path", cfg.DatabasePath)

	// Drop any meta rows that historically held plaintext credentials. Idempotent
	// — a no-op after the first run on each upgraded database.
	if err := repo.PurgeLegacySecrets(context.Background()); err != nil {
		logger.Warn("failed to purge legacy secret meta rows", "error", err)
	}

	// Load the AES-GCM key used to wrap the IS24 cookie in the meta table. The
	// key comes from SECRETS_KEY when set, otherwise a 0600 file alongside the
	// database — see internal/secrets for details.
	cookieKey, err := secrets.LoadOrGenerateKey(dataDir, logger)
	if err != nil {
		logger.Error("failed to load secrets key", "error", err)
		os.Exit(1)
	}
	cookieEnc, err := secrets.NewEncrypter(cookieKey)
	if err != nil {
		logger.Error("failed to initialize cookie encrypter", "error", err)
		os.Exit(1)
	}

	// Initialize anti-detection components
	rateLimiter := antidetect.NewRateLimiter(
		cfg.IS24.MaxRequestsPerMinute,
		cfg.IS24.MinDelay,
		cfg.IS24.MaxDelay,
	)
	humanBehavior := antidetect.NewHumanBehavior(cfg.Contact.TypeDelay, cfg.Contact.ActionDelay)
	// Wired below once the Controller exists, so dashboard timing edits take
	// effect on the next keystroke / next action without restart.

	// A previously hot-reloaded IS24 cookie (saved in the meta table via the
	// dashboard / Telegram /cookie command) overrides the env-supplied one so
	// the override survives container restarts without editing .env. The
	// stored value is AES-GCM encrypted; legacy plaintext rows are migrated
	// to the encrypted form on first read so they don't sit in the DB.
	if v, _ := repo.GetMeta(context.Background(), sqlite.MetaIS24Cookie); v != "" {
		if secrets.IsEncrypted(v) {
			pt, err := cookieEnc.Decrypt(v)
			if err != nil {
				logger.Error("failed to decrypt IS24 cookie from meta (wrong SECRETS_KEY?)", "error", err)
				os.Exit(1)
			}
			cfg.IS24.Cookie = pt
			logger.Info("IS24 cookie loaded from meta override (encrypted)")
		} else {
			cfg.IS24.Cookie = v
			if ct, err := cookieEnc.Encrypt(v); err == nil {
				if err := repo.SetMeta(context.Background(), sqlite.MetaIS24Cookie, ct); err != nil {
					logger.Warn("failed to migrate IS24 cookie to encrypted form", "error", err)
				} else {
					logger.Info("IS24 cookie migrated from plaintext to encrypted at-rest form")
				}
			}
		}
	}

	// Email config overrides from the meta table (set via the dashboard).
	// Mirrors the IS24 cookie pattern so users can configure IMAP without
	// editing .env. Empty meta values fall through to the config.yaml/env layer.
	applyEmailMetaOverrides(repo, cfg, logger)
	applyEmailPasswordFromMeta(repo, cfg, cookieEnc, logger)

	// Initialize IS24 browser client (uses chromedp to bypass WAF)
	proxy := antidetect.Proxy{
		URL:      cfg.IS24.Proxy.URL,
		Username: cfg.IS24.Proxy.Username,
		Password: cfg.IS24.Proxy.Password,
	}
	var bandwidth *antidetect.BandwidthGuard
	if proxy.Enabled() {
		bandwidth = antidetect.NewBandwidthGuard(cfg.IS24.Proxy.BandwidthCapMB, repo, logger)
		logger.Info(bandwidth.Summary())
	}
	is24Client := is24.NewBrowserClient(cfg.IS24.Cookie, rateLimiter, cfg.Contact.ChromePath, proxy, bandwidth)
	if proxy.Enabled() {
		logger.Info("IS24 browser client initialized", "proxy", proxy.URL, "auth", proxy.RequiresAuth(), "cap_mb", cfg.IS24.Proxy.BandwidthCapMB)
	} else {
		logger.Info("IS24 browser client initialized")
	}

	// One-time backfill: seed the DB with every IS24 ID currently visible across
	// each profile's search results, then exit. Runs before any notifier /
	// scheduler is wired so a backfill run can never message Telegram/WhatsApp.
	if *backfill {
		os.Exit(runBackfill(context.Background(), repo, is24Client, *backfillPages, logger))
	}

	// Initialize filter engine
	filterEngine := filter.NewEngine()

	// Shared, transport-neutral control state (contact mode, quiet hours).
	// Defaults come from config.yaml; persisted overrides loaded from the
	// sqlite meta table on construction.
	ctrl := control.New(repo, logger, control.Defaults{
		QuietHoursEnabled:  cfg.QuietHours.Enabled,
		QuietHoursStart:    cfg.QuietHours.Start,
		QuietHoursEnd:      cfg.QuietHours.End,
		Timezone:           cfg.QuietHours.Timezone,
		PollInterval:       cfg.PollInterval,
		ContactTypeDelay:   cfg.Contact.TypeDelay,
		ContactActionDelay: cfg.Contact.ActionDelay,
	})

	// Live timing: behavior reads delays from the Controller so dashboard
	// edits take effect on the next keystroke / next action.
	humanBehavior.TypeDelayFn = ctrl.GetContactTypeDelay
	humanBehavior.ActionDelayFn = ctrl.GetContactActionDelay

	// Filter reads furnished-exclusion flag from controller (dashboard toggle).
	filterEngine.ExcludeFurnishedFn = ctrl.IsExcludeFurnishedEnabled

	// Initialize Telegram bot controller (for commands)
	botController, err := telegram.NewBotController(cfg.Telegram.BotToken, cfg.Telegram.ChatID, cfg.Telegram.Enabled, ctrl)
	if err != nil {
		logger.Error("failed to initialize Telegram bot controller", "error", err)
		os.Exit(1)
	}
	tgNotifier := telegram.NewNotifierFromController(botController)

	// Initialize WhatsApp channel (notifications + commands via whatsmeow)
	waClient, err := whatsapp.New(context.Background(), cfg.WhatsApp, ctrl, logger)
	if err != nil {
		logger.Error("failed to initialize WhatsApp client", "error", err)
		os.Exit(1)
	}

	// Fan notifications out to every enabled channel.
	notif := notifier.NewMulti(tgNotifier, waClient)

	// Initialize OpenAI enhancer
	var enhancer scheduler.MessageEnhancer
	if cfg.OpenAI.Enabled && cfg.OpenAI.APIKey != "" {
		enhancer = messenger.NewOpenAIEnhancer(cfg.OpenAI.APIKey, cfg.OpenAI.Model, cfg.OpenAI.Enabled)
		logger.Info("OpenAI message enhancement enabled", "model", cfg.OpenAI.Model)
	}

	// Initialize contact submitter. When OpenAI is configured, wire an LLM
	// form-filler as fallback for when the static selectors miss IS24's DOM.
	var contacter *contact.Submitter
	if cfg.Contact.Enabled {
		var mapper contact.FieldMapper
		if cfg.OpenAI.Enabled && cfg.OpenAI.APIKey != "" {
			mapper = messenger.NewOpenAIFormFiller(cfg.OpenAI.APIKey, cfg.OpenAI.Model)
			logger.Info("contact form llm fallback enabled", "model", cfg.OpenAI.Model)
		}
		contacter = contact.NewSubmitter(
			cfg.IS24.Cookie,
			toContactProfile(cfg.Contact.Profile),
			cfg.Contact.ChromePath,
			humanBehavior,
			proxy,
			bandwidth,
			mapper,
			logger,
		)
		logger.Info("auto-contact ready (controlled via Telegram)")
	}

	// Build per-campaign personalization (message template + AI prompt + contact
	// profile) and a resolver the scheduler uses per listing.
	resolver, err := newCampaignResolver(cfg, logger)
	if err != nil {
		logger.Error("failed to build campaigns", "error", err)
		os.Exit(1)
	}

	// Create scheduler
	sched := scheduler.NewScheduler(
		cfg,
		repo,
		is24Client,
		filterEngine,
		notif,
		resolver,
		enhancer,
		contacter,
		logger,
	)

	// Wire the IMAP inbox monitor (IS24 provider replies). Requires OpenAI for
	// classification. cfg.Validate covers the env path; meta overrides can also
	// enable email after startup, so we re-check the dependencies here.
	// emailMonitor stays nil if the monitor cannot be constructed; that's a
	// signal the web layer uses to disable the "Jetzt scannen"-button.
	var emailMonitor *email.Monitor
	if cfg.Email.Enabled {
		if missing := missingEmailFields(cfg); missing != "" {
			logger.Warn("email enabled but configuration incomplete — monitor not started", "missing", missing)
		} else {
			emailClient := email.NewClient(email.Config{
				Addr:     cfg.Email.IMAPHost,
				Username: cfg.Email.Username,
				Password: cfg.Email.Password,
				Mailbox:  cfg.Email.Mailbox,
				Senders:  cfg.Email.Senders,
				Lookback: cfg.Email.Lookback,
			})
			classifier := messenger.NewOpenAIEmailClassifier(cfg.OpenAI.APIKey, cfg.OpenAI.Model)
			emailMonitor = email.NewMonitor(emailClient, classifier, repo, notif, logger)
			sched.SetEmailMonitor(emailMonitor)
			logger.Info("email inbox monitor enabled", "mailbox", cfg.Email.Mailbox, "host", cfg.Email.IMAPHost)
		}
	}

	// Cookie encrypter so the meta-table row is wrapped in AES-GCM.
	sched.SetCookieEncrypter(cookieEnc)

	// Connect shared controller state to scheduler
	sched.SetAutoContactCallback(ctrl.IsAutoContactEnabled)
	sched.SetTestModeCallback(ctrl.IsTestModeEnabled)
	sched.SetApprovalModeCallback(ctrl.IsApprovalModeEnabled)
	sched.SetQuietHoursCallback(ctrl.IsQuietHoursEnabled)

	// Wire Telegram approval buttons → scheduler.OnApprove / OnReject.
	botController.SetApprovalHandler(sched)
	// Quiet-hours WINDOW (start/end) override from controller — falls back to
	// cfg defaults inside the controller when no override is persisted.
	sched.SetQuietWindowCallback(ctrl.IsWithinQuietHours)

	// Dynamic poll interval: scheduler reads the latest value each cycle and
	// the buffered reset channel cuts the current sleep short on change.
	pollResetCh := make(chan struct{}, 1)
	ctrl.SubscribePollInterval(pollResetCh)
	sched.SetPollIntervalSource(ctrl.GetPollInterval, pollResetCh)

	// /cookie chat command → scheduler hot-reload (also persists to meta).
	ctrl.SetCookieCallback(sched.SetIS24Cookie)

	// Set status/stats callbacks (text uses *bold* markup, rendered per channel)
	ctrl.SetCallbacks(
		func() string {
			profiles, _ := repo.GetActiveSearchProfiles(context.Background())
			return fmt.Sprintf("*Aktive Suchprofile:* %d", len(profiles))
		},
		func() string {
			total, contacted, notified := sched.GetStats(context.Background())
			return fmt.Sprintf(`📊 *Statistiken*

*Wohnungen gefunden:* %d
*Benachrichtigt:* %d
*Kontaktiert:* %d`, total, notified, contacted)
		},
	)

	// Search-profile management commands (/addprofil, /listprofile, /delprofil)
	ctrl.SetProfileCallbacks(
		func(category, url, name string) string {
			if category != "" && !cfg.HasCampaign(category) {
				return fmt.Sprintf("❌ Unbekannte Kampagne %q.\n\nVerfügbar: %s", category, strings.Join(campaignNames(cfg), ", "))
			}
			if name == "" {
				name = profileNameFromURL(url)
			}
			// City is left empty: the search_url already scopes the search, and a
			// wrongly-guessed city would filter out every result.
			sp := &domain.SearchProfile{Name: name, SearchURL: url, Category: category, Active: true}
			if err := repo.CreateSearchProfile(context.Background(), sp); err != nil {
				logger.Error("add profile failed", "error", err)
				return "❌ Profil anlegen fehlgeschlagen: " + err.Error()
			}
			camp := category
			if camp == "" {
				camp = cfg.DefaultCampaign
			}
			return fmt.Sprintf("✅ *Profil angelegt* (id %d, Kampagne %s)\n\n*%s*\n🔗 %s", sp.ID, camp, name, url)
		},
		func() string {
			profiles, err := repo.GetActiveSearchProfiles(context.Background())
			if err != nil {
				return "❌ Profile laden fehlgeschlagen: " + err.Error()
			}
			if len(profiles) == 0 {
				return "Keine aktiven Suchprofile. Mit /addprofil <URL> eins anlegen."
			}
			var sb strings.Builder
			sb.WriteString("📋 *Aktive Suchprofile*\n")
			for _, p := range profiles {
				camp := p.Category
				if camp == "" {
					camp = cfg.DefaultCampaign
				}
				sb.WriteString(fmt.Sprintf("\n*%d* — %s _(%s)_", p.ID, p.Name, camp))
				if p.SearchURL != "" {
					sb.WriteString("\n   🔗 " + p.SearchURL)
				} else if p.City != "" {
					sb.WriteString("\n   📍 " + p.City)
				}
			}
			return sb.String()
		},
		func(idStr string) string {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				return "Ungültige ID. Nutzung: /delprofil <id>"
			}
			if err := repo.SetSearchProfileActive(context.Background(), id, false); err != nil {
				return "❌ " + err.Error()
			}
			return fmt.Sprintf("🗑 Profil %d deaktiviert.", id)
		},
	)

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
		sched.Stop()
	}()

	// Start Telegram command listener
	if botController.IsEnabled() {
		botController.StartCommandListener(ctx)
		logger.Info("Telegram command listener started")
	}

	// Connect WhatsApp (prints a pairing code on first run) and start its command
	// listener. A connection/pairing failure is non-fatal: the bot keeps running
	// (dashboard, Telegram, scraping) so a transient WhatsApp issue can't crash-loop it.
	if waClient.IsEnabled() {
		if err := waClient.Connect(ctx); err != nil {
			logger.Error("WhatsApp connect failed, continuing without WhatsApp", "error", err)
		} else {
			defer waClient.Disconnect()
			logger.Info("WhatsApp command listener started")
		}
	}

	// Start web dashboard (localhost by default)
	if cfg.Web.Enabled {
		websrv := web.New(repo, ctrl, cfg, sched.GetStats, sched.SetIS24Cookie, tgNotifier, sched, logger)
		websrv.SetCookieEncrypter(cookieEnc)
		// Wire the on-demand inbox scan trigger. nil when email is disabled —
		// /api/inbox/scan then reports 503.
		if emailMonitor != nil {
			websrv.SetInboxScanner(emailMonitor)
		}
		go func() {
			if err := websrv.Start(ctx, cfg.Web.Addr); err != nil {
				logger.Error("web dashboard failed", "error", err)
			}
		}()
	}

	// Get profile count for startup notification
	profiles, _ := repo.GetActiveSearchProfiles(ctx)
	if notif.IsEnabled() {
		quietLabel := "☀️ Aus (24/7)"
		if v := ctrl.IsQuietHoursEnabled(); v != nil && *v {
			quietLabel = "🌙 An (22:00-07:00)"
		}
		startupMsg := fmt.Sprintf(`🚀 *ImmoBot gestartet*

*Kontakt:* %s
*Ruhezeiten:* %s
*Suchprofile:* %d
*Poll-Intervall:* %s

*━━━ Befehle ━━━*

*Kontakt:*
/contact_on - Auto-Kontakt an
/contact_test - Test-Modus (Vorschau)
/contact_off - Nur beobachten

*Ruhezeiten:*
/quiet_on - Ruhezeiten an
/quiet_off - 24/7 aktiv

*Info:*
/status - Aktueller Status
/stats - Statistiken
/help - Alle Befehle`, ctrl.ContactModeLabel(), quietLabel, len(profiles), cfg.PollInterval)

		notif.SendRawMessage(ctx, startupMsg)
	}

	if len(profiles) == 0 {
		logger.Warn("no active search profiles found - add profiles to the database to start searching")
		fmt.Println("\nTo add a search profile, use SQL:")
		fmt.Println(`  INSERT INTO search_profiles (name, city, max_price, min_rooms, active)`)
		fmt.Println(`  VALUES ('Berlin Mitte', 'Berlin', 1500, 2, 1);`)
		fmt.Println("\nOr provide a search_url from IS24:")
		fmt.Println(`  INSERT INTO search_profiles (name, city, search_url, active)`)
		fmt.Println(`  VALUES ('Custom Search', 'Berlin', 'https://www.immobilienscout24.de/Suche/...', 1);`)
	}

	// Run
	if *runOnce {
		logger.Info("running single poll cycle")
		if err := sched.RunOnce(ctx); err != nil {
			logger.Error("poll cycle failed", "error", err)
			os.Exit(1)
		}
		logger.Info("poll cycle complete")
	} else {
		logger.Info("starting scheduler", "poll_interval", cfg.PollInterval)
		if err := sched.Start(ctx); err != nil {
			logger.Error("scheduler failed to start", "error", err)
			os.Exit(1)
		}

		// Periodic database snapshots (VACUUM INTO + retention rotation).
		if cfg.Backup.Enabled {
			go backup.Run(ctx, repo, cfg.Backup, logger)
		}

		// Wait for shutdown
		<-ctx.Done()
		logger.Info("shutdown complete")
	}
}

// profileNameFromURL derives a friendly profile name from an IS24 search URL,
// using the city segment of the path (".../Suche/de/<region>/<city>/...").
// Falls back to "IS24-Suche" when the path doesn't match.
func profileNameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "IS24-Suche"
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if strings.EqualFold(p, "de") && i+2 < len(parts) {
			city := parts[i+2]
			if city == "" {
				break
			}
			return strings.ToUpper(city[:1]) + city[1:]
		}
	}
	return "IS24-Suche"
}

// campaignResolver maps a search profile's category to its scheduler.Campaign
// (message generator + AI prompt + applicant profile), built from config.
type campaignResolver struct {
	byName      map[string]scheduler.Campaign
	defaultName string
	fallback    scheduler.Campaign
}

// Resolve returns the campaign for the category, falling back to the default
// campaign and then the global fallback.
func (r *campaignResolver) Resolve(category string) scheduler.Campaign {
	if c, ok := r.byName[category]; ok {
		return c
	}
	if c, ok := r.byName[r.defaultName]; ok {
		return c
	}
	return r.fallback
}

// newCampaignResolver parses each configured campaign's template once at startup.
func newCampaignResolver(cfg *config.Config, logger *slog.Logger) (*campaignResolver, error) {
	r := &campaignResolver{
		byName:      make(map[string]scheduler.Campaign),
		defaultName: cfg.DefaultCampaign,
	}
	for name := range cfg.Campaigns {
		camp := cfg.ResolveCampaign(name) // fills empty fields from globals
		gen, err := messenger.NewGenerator(camp.MessageTemplatePath, "", "", "")
		if err != nil {
			return nil, fmt.Errorf("campaign %q template: %w", name, err)
		}
		r.byName[name] = scheduler.Campaign{
			Name:      name,
			Generator: gen,
			AIPrompt:  camp.AIPrompt,
			Contact:   toContactProfile(camp.Contact),
		}
		logger.Info("campaign loaded", "name", name, "template", camp.MessageTemplatePath)
	}

	// Global fallback for unknown/empty categories.
	fb := cfg.ResolveCampaign("")
	gen, err := messenger.NewGenerator(fb.MessageTemplatePath, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("fallback campaign template: %w", err)
	}
	r.fallback = scheduler.Campaign{
		Name:      cfg.DefaultCampaign,
		Generator: gen,
		AIPrompt:  fb.AIPrompt,
		Contact:   toContactProfile(fb.Contact),
	}
	return r, nil
}

// toContactProfile maps the config applicant profile to the contact package type.
func toContactProfile(p config.ContactProfile) contact.Profile {
	return contact.Profile{
		Salutation:    p.Salutation,
		FirstName:     p.FirstName,
		LastName:      p.LastName,
		Email:         p.Email,
		Phone:         p.Phone,
		Street:        p.Street,
		HouseNumber:   p.HouseNumber,
		PostalCode:    p.PostalCode,
		City:          p.City,
		Adults:        p.Adults,
		Children:      p.Children,
		Pets:          p.Pets,
		Income:        p.Income,
		MoveInDate:    p.MoveInDate,
		Employment:    p.Employment,
		RentArrears:   p.RentArrears,
		Insolvency:    p.Insolvency,
		Smoker:        p.Smoker,
		CommercialUse: p.CommercialUse,
	}
}

// campaignNames returns the configured campaign names (for error messages).
func campaignNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Campaigns))
	for n := range cfg.Campaigns {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// applyEmailPasswordFromMeta loads the IMAP app password from the meta table,
// mirroring the IS24 cookie pattern: encrypted rows are decrypted, legacy
// plaintext rows are migrated, and a password supplied only via EMAIL_PASSWORD
// env is bootstrapped into encrypted at-rest form on first run.
func applyEmailPasswordFromMeta(repo *sqlite.Repository, cfg *config.Config, enc *secrets.Encrypter, logger *slog.Logger) {
	ctx := context.Background()
	v, _ := repo.GetMeta(ctx, sqlite.MetaEmailPassword)
	switch {
	case v != "":
		if secrets.IsEncrypted(v) {
			pt, err := enc.Decrypt(v)
			if err != nil {
				logger.Error("failed to decrypt email password from meta (wrong SECRETS_KEY?)", "error", err)
				os.Exit(1)
			}
			cfg.Email.Password = pt
			logger.Info("email password loaded from meta (encrypted)")
		} else {
			cfg.Email.Password = v
			if ct, err := enc.Encrypt(v); err == nil {
				if err := repo.SetMeta(ctx, sqlite.MetaEmailPassword, ct); err != nil {
					logger.Warn("failed to migrate email password to encrypted form", "error", err)
				} else {
					logger.Info("email password migrated from plaintext to encrypted at-rest form")
				}
			}
		}
	case strings.TrimSpace(cfg.Email.Password) != "":
		if ct, err := enc.Encrypt(cfg.Email.Password); err == nil {
			if err := repo.SetMeta(ctx, sqlite.MetaEmailPassword, ct); err != nil {
				logger.Warn("failed to bootstrap email password from env to encrypted form", "error", err)
			} else {
				logger.Info("email password bootstrapped from env to encrypted at-rest form")
			}
		}
	}
}

// applyEmailMetaOverrides merges dashboard-edited email settings (persisted in
// the meta table) into cfg.Email. Only non-empty meta values overwrite the
// existing config, so the merge is additive: env/yaml still wins for fields the
// user hasn't customized via the dashboard.
func applyEmailMetaOverrides(repo *sqlite.Repository, cfg *config.Config, logger *slog.Logger) {
	ctx := context.Background()
	read := func(key string) string {
		v, _ := repo.GetMeta(ctx, key)
		return v
	}

	if v := read(sqlite.MetaEmailEnabled); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			cfg.Email.Enabled = true
		case "0", "false", "no", "off":
			cfg.Email.Enabled = false
		}
	}
	if v := read(sqlite.MetaEmailIMAPHost); v != "" {
		cfg.Email.IMAPHost = v
	}
	if v := read(sqlite.MetaEmailUsername); v != "" {
		cfg.Email.Username = v
	}
	if v := read(sqlite.MetaEmailMailbox); v != "" {
		cfg.Email.Mailbox = v
	}
	if v := read(sqlite.MetaEmailLookbackHours); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cfg.Email.Lookback = time.Duration(n) * time.Hour
		}
	}
	if v := read(sqlite.MetaEmailSenders); v != "" {
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			cfg.Email.Senders = out
		}
	}

	// Log only when at least one meta override is present, to keep the start-up
	// log quiet for users on the env-only path.
	if read(sqlite.MetaEmailEnabled)+read(sqlite.MetaEmailIMAPHost)+read(sqlite.MetaEmailUsername) != "" {
		logger.Info("email config loaded from meta override",
			"enabled", cfg.Email.Enabled,
			"host", cfg.Email.IMAPHost,
			"user", cfg.Email.Username,
			"mailbox", cfg.Email.Mailbox,
		)
	}
}

// missingEmailFields returns a comma-separated list of email config fields that
// are required for the monitor but currently unset. Returns "" when the config
// is complete enough to start.
func missingEmailFields(cfg *config.Config) string {
	var missing []string
	if strings.TrimSpace(cfg.Email.IMAPHost) == "" {
		missing = append(missing, "imap_host")
	}
	if strings.TrimSpace(cfg.Email.Username) == "" {
		missing = append(missing, "username")
	}
	if strings.TrimSpace(cfg.Email.Password) == "" {
		missing = append(missing, "password")
	}
	if !cfg.OpenAI.Enabled || strings.TrimSpace(cfg.OpenAI.APIKey) == "" {
		missing = append(missing, "openai (required for classification)")
	}
	return strings.Join(missing, ", ")
}

// runHealthCheck reports whether the last successful poll is recent enough.
// Returns 0 (healthy) or 1 (stale/unknown) for use as a container HEALTHCHECK.
func runHealthCheck(cfg *config.Config) int {
	repo, err := sqlite.New(cfg.DatabasePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: open db:", err)
		return 1
	}
	defer repo.Close()

	ts, err := repo.GetMeta(context.Background(), sqlite.MetaLastPollOK)
	if err != nil || ts == "" {
		fmt.Fprintln(os.Stderr, "healthcheck: no poll heartbeat yet")
		return 1
	}
	last, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: bad heartbeat:", ts)
		return 1
	}

	// Allow up to 3 poll intervals (floor 10m) before declaring the bot stale.
	maxAge := 3 * cfg.PollInterval
	if maxAge < 10*time.Minute {
		maxAge = 10 * time.Minute
	}
	if age := time.Since(last); age > maxAge {
		fmt.Fprintf(os.Stderr, "healthcheck: last poll %s ago (> %s)\n", age.Round(time.Second), maxAge)
		return 1
	}
	return 0
}

// is24SearchPaginator is the deeper-paginated search method offered by the
// BrowserClient; declared as an interface here so runBackfill stays decoupled
// from the concrete client type (and is trivially mockable in tests).
type is24SearchPaginator interface {
	SearchPaginated(ctx context.Context, profile *domain.SearchProfile, maxPages int) ([]domain.Listing, error)
}

// runBackfill seeds the DB with every IS24 ID currently visible across each
// active profile's search results so the regular poll cycle won't treat them
// as new. Existing listings are never overwritten (INSERT OR IGNORE); only
// previously-unknown IDs get a minimal stub row with notified=1, contacted=1,
// backfilled=1, which keeps them out of every downstream queue.
//
// Expose pages are NOT fetched — search-result fields are enough to record
// "we've seen this ID". This is the whole point of the backfill: cheap to run,
// safe to repeat, and never sends a notification.
func runBackfill(ctx context.Context, repo *sqlite.Repository, client is24SearchPaginator, maxPages int, logger *slog.Logger) int {
	profiles, err := repo.GetActiveSearchProfiles(ctx)
	if err != nil {
		logger.Error("backfill: load profiles failed", "error", err)
		return 1
	}
	if len(profiles) == 0 {
		logger.Warn("backfill: no active search profiles — nothing to do")
		return 0
	}

	logger.Info("backfill starting", "profiles", len(profiles), "max_pages_per_profile", maxPages)

	var totalSeen, totalInserted, totalSkipped int
	for i := range profiles {
		profile := &profiles[i]
		logger.Info("backfill: scraping profile",
			"id", profile.ID, "name", profile.Name, "url", profile.SearchURL)

		listings, err := client.SearchPaginated(ctx, profile, maxPages)
		if err != nil {
			// Don't abort the whole backfill on a single profile failure — the
			// other profiles can still be seeded, and the user can re-run later.
			logger.Error("backfill: profile scrape failed, continuing",
				"profile", profile.Name, "error", err)
			continue
		}

		var inserted, skipped int
		for j := range listings {
			l := listings[j]
			l.SearchProfileID = profile.ID
			ok, err := repo.BackfillSeedListing(ctx, &l)
			if err != nil {
				logger.Error("backfill: insert failed",
					"is24_id", l.IS24ID, "error", err)
				continue
			}
			if ok {
				inserted++
			} else {
				skipped++ // already in DB — preserved as-is
			}
		}

		logger.Info("backfill: profile complete",
			"profile", profile.Name,
			"scraped", len(listings),
			"newly_seeded", inserted,
			"already_known", skipped)

		totalSeen += len(listings)
		totalInserted += inserted
		totalSkipped += skipped
	}

	logger.Info("backfill complete",
		"profiles", len(profiles),
		"total_scraped", totalSeen,
		"newly_seeded", totalInserted,
		"already_known_preserved", totalSkipped)
	return 0
}
