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
	"strconv"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/julianbeese/immo_bot/internal/antidetect"
	"github.com/julianbeese/immo_bot/internal/config"
	"github.com/julianbeese/immo_bot/internal/contact"
	"github.com/julianbeese/immo_bot/internal/control"
	"github.com/julianbeese/immo_bot/internal/domain"
	"github.com/julianbeese/immo_bot/internal/filter"
	"github.com/julianbeese/immo_bot/internal/messenger"
	"github.com/julianbeese/immo_bot/internal/notifier"
	"github.com/julianbeese/immo_bot/internal/notifier/telegram"
	"github.com/julianbeese/immo_bot/internal/notifier/whatsapp"
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

	// Shared, transport-neutral control state (contact mode, quiet hours).
	ctrl := control.New()

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
		profile := contact.Profile{
			Salutation:    cfg.Contact.Profile.Salutation,
			FirstName:     cfg.Contact.Profile.FirstName,
			LastName:      cfg.Contact.Profile.LastName,
			Email:         cfg.Contact.Profile.Email,
			Phone:         cfg.Contact.Profile.Phone,
			Street:        cfg.Contact.Profile.Street,
			HouseNumber:   cfg.Contact.Profile.HouseNumber,
			PostalCode:    cfg.Contact.Profile.PostalCode,
			City:          cfg.Contact.Profile.City,
			Adults:        cfg.Contact.Profile.Adults,
			Children:      cfg.Contact.Profile.Children,
			Pets:          cfg.Contact.Profile.Pets,
			Income:        cfg.Contact.Profile.Income,
			MoveInDate:    cfg.Contact.Profile.MoveInDate,
			Employment:    cfg.Contact.Profile.Employment,
			RentArrears:   cfg.Contact.Profile.RentArrears,
			Insolvency:    cfg.Contact.Profile.Insolvency,
			Smoker:        cfg.Contact.Profile.Smoker,
			CommercialUse: cfg.Contact.Profile.CommercialUse,
		}
		contacter = contact.NewSubmitter(
			cfg.IS24.Cookie,
			profile,
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
		notif,
		msgGenerator,
		enhancer,
		contacter,
		logger,
	)

	// Connect shared controller state to scheduler
	sched.SetAutoContactCallback(ctrl.IsAutoContactEnabled)
	sched.SetTestModeCallback(ctrl.IsTestModeEnabled)
	sched.SetQuietHoursCallback(ctrl.IsQuietHoursEnabled)

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
		func(url, name string) string {
			if name == "" {
				name = profileNameFromURL(url)
			}
			// City is left empty: the search_url already scopes the search, and a
			// wrongly-guessed city would filter out every result.
			sp := &domain.SearchProfile{Name: name, SearchURL: url, Active: true}
			if err := repo.CreateSearchProfile(context.Background(), sp); err != nil {
				logger.Error("add profile failed", "error", err)
				return "❌ Profil anlegen fehlgeschlagen: " + err.Error()
			}
			return fmt.Sprintf("✅ *Profil angelegt* (id %d)\n\n*%s*\n🔗 %s", sp.ID, name, url)
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
				sb.WriteString(fmt.Sprintf("\n*%d* — %s", p.ID, p.Name))
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

	// Connect WhatsApp (prints QR on first run) and start its command listener
	if waClient.IsEnabled() {
		if err := waClient.Connect(ctx); err != nil {
			logger.Error("failed to connect WhatsApp", "error", err)
			os.Exit(1)
		}
		defer waClient.Disconnect()
		logger.Info("WhatsApp command listener started")
	}

	// Get profile count for startup notification
	profiles, _ := repo.GetActiveSearchProfiles(ctx)
	if notif.IsEnabled() {
		startupMsg := fmt.Sprintf(`🚀 *ImmoBot gestartet*

*Kontakt:* ✅ Auto-Kontakt aktiv
*Ruhezeiten:* 🌙 An (22:00-07:00)
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
/help - Alle Befehle`, len(profiles), cfg.PollInterval)

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
