package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/julianbeese/immo_bot/internal/antidetect"
	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/contact"
	"github.com/julianbeese/immo_bot/internal/filter"
	"github.com/julianbeese/immo_bot/internal/messenger"
	"github.com/julianbeese/immo_bot/internal/notifier/telegram"
	"github.com/julianbeese/immo_bot/internal/repository/sqlite"
	"github.com/julianbeese/immo_bot/internal/scheduler"
	"github.com/julianbeese/immo_bot/internal/scraper/is24"
)

func main() {
	// Load .env file if present (ignores error if not found)
	_ = godotenv.Load()                   // .env in current directory
	_ = godotenv.Load("deployments/.env") // fallback to deployments/.env

	// Parse command line flags
	configPath := flag.String("config", "configs/config.yaml", "Path to configuration file")
	runOnce := flag.Bool("once", false, "Run a single poll cycle and exit")
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

	// Initialize anti-detection components
	rateLimiter := antidetect.NewRateLimiter(
		cfg.IS24.MaxRequestsPerMinute,
		cfg.IS24.MinDelay,
		cfg.IS24.MaxDelay,
	)
	humanBehavior := antidetect.NewHumanBehavior(cfg.Contact.TypeDelay, cfg.Contact.ActionDelay)

	// Initialize IS24 browser client (uses chromedp to bypass WAF)
	is24Client := is24.NewBrowserClient(cfg.IS24.Cookie, rateLimiter, cfg.Contact.ChromePath)
	logger.Info("IS24 browser client initialized")

	// Initialize filter engine
	filterEngine := filter.NewEngine()

	// Initialize Telegram bot controller (for commands)
	botController, err := telegram.NewBotController(cfg.Telegram.BotToken, cfg.Telegram.ChatID, cfg.Telegram.Enabled)
	if err != nil {
		logger.Error("failed to initialize Telegram bot controller", "error", err)
		os.Exit(1)
	}

	// Create notifier from controller
	notifier := telegram.NewNotifierFromController(botController)

	// Initialize message generator
	msgGenerator, err := messenger.NewGenerator(
		cfg.Message.TemplatePath,
		cfg.Message.SenderName,
		cfg.Message.SenderEmail,
		cfg.Message.SenderPhone,
	)
	if err != nil {
		logger.Error("failed to initialize message generator", "error", err)
		os.Exit(1)
	}

	// Initialize OpenAI enhancer
	var enhancer scheduler.MessageEnhancer
	if cfg.OpenAI.Enabled && cfg.OpenAI.APIKey != "" {
		enhancer = messenger.NewOpenAIEnhancer(cfg.OpenAI.APIKey, cfg.OpenAI.Model, cfg.OpenAI.Enabled)
		logger.Info("OpenAI message enhancement enabled", "model", cfg.OpenAI.Model)
	}

	// Initialize contact submitter
	var contacter *contact.Submitter
	if cfg.Contact.Enabled {
		contacter = contact.NewSubmitter(
			cfg.IS24.Cookie,
			cfg.Message.SenderName,
			cfg.Message.SenderEmail,
			cfg.Message.SenderPhone,
			cfg.Contact.ChromePath,
			humanBehavior,
		)
		logger.Info("auto-contact ready (controlled via Telegram)")
	}

	// Create scheduler
	sched := scheduler.NewScheduler(
		cfg,
		repo,
		is24Client,
		filterEngine,
		notifier,
		msgGenerator,
		enhancer,
		contacter,
		logger,
	)

	// Connect bot controller to scheduler
	sched.SetAutoContactCallback(botController.IsAutoContactEnabled)
	sched.SetTestModeCallback(botController.IsTestModeEnabled)
	sched.SetQuietHoursCallback(botController.IsQuietHoursEnabled)

	// Set stats callback for bot controller
	botController.SetCallbacks(
		func() string {
			profiles, _ := repo.GetActiveSearchProfiles(context.Background())
			return fmt.Sprintf("<b>Aktive Suchprofile:</b> %d", len(profiles))
		},
		func() string {
			total, contacted, notified := sched.GetStats(context.Background())
			return fmt.Sprintf(`📊 <b>Statistiken</b>

<b>Wohnungen gefunden:</b> %d
<b>Benachrichtigt:</b> %d
<b>Kontaktiert:</b> %d`, total, notified, contacted)
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

	// Get profile count for startup notification
	profiles, _ := repo.GetActiveSearchProfiles(ctx)
	if notifier.IsEnabled() {
		startupMsg := fmt.Sprintf(`🚀 <b>ImmoBot gestartet</b>

<b>Modus:</b> ⏸ Beobachtungsmodus
<b>Aktive Suchprofile:</b> %d
<b>Poll-Intervall:</b> %s

Nutze /contact_on um Auto-Kontakt zu aktivieren.
Nutze /help für alle Befehle.`, len(profiles), cfg.PollInterval)

		notifier.SendRawMessage(ctx, startupMsg)
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

		// Wait for shutdown
		<-ctx.Done()
		logger.Info("shutdown complete")
	}
}
