package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration
type Config struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	DatabasePath string        `yaml:"database_path"`
	LogLevel     string        `yaml:"log_level"`

	IS24       IS24Config       `yaml:"is24"`
	Telegram   TelegramConfig   `yaml:"telegram"`
	WhatsApp   WhatsAppConfig   `yaml:"whatsapp"`
	OpenAI     OpenAIConfig     `yaml:"openai"`
	Email      EmailConfig      `yaml:"email"`
	Contact    ContactConfig    `yaml:"contact"`
	Message    MessageConfig    `yaml:"message"`
	QuietHours QuietHoursConfig `yaml:"quiet_hours"`
	Web        WebConfig        `yaml:"web"`
	Backup     BackupConfig     `yaml:"backup"`

	// DefaultCampaign / Campaigns enable per-search-profile personalization:
	// a search profile's category selects a campaign (message template, AI
	// prompt, contact profile). Empty category → DefaultCampaign.
	DefaultCampaign string              `yaml:"default_campaign"`
	Campaigns       map[string]Campaign `yaml:"campaigns"`
}

// Campaign bundles the message template, AI prompt and applicant profile used
// for one search strategy (e.g. "single" vs "wg"). Empty fields fall back to
// the global Message/Contact settings.
type Campaign struct {
	MessageTemplatePath string         `yaml:"message_template_path"`
	AIPrompt            string         `yaml:"ai_prompt"`
	Contact             ContactProfile `yaml:"contact_profile"`
}

// BackupConfig controls the periodic sqlite "VACUUM INTO" snapshot of the
// listings/sent_messages database. Output files are atomic single-file copies;
// retention is enforced by mtime.
type BackupConfig struct {
	Enabled       bool          `yaml:"enabled"`
	Interval      time.Duration `yaml:"interval"`       // e.g. 24h
	RetentionDays int           `yaml:"retention_days"` // 0 = keep newest only
	Dir           string        `yaml:"dir"`            // e.g. "data/backups"
}

// WebConfig for the local web dashboard.
type WebConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"` // listen address, default 127.0.0.1:8080 (localhost only)
}

// WhatsAppConfig for WhatsApp control via whatsmeow (linked device).
type WhatsAppConfig struct {
	Enabled     bool   `yaml:"enabled"`
	StorePath   string `yaml:"store_path"`   // whatsmeow session DB, e.g. "data/whatsapp.db"
	TargetPhone string `yaml:"target_phone"` // digits only, e.g. "4915123456789" — receives notifications and is the only authorized commander
	LogLevel    string `yaml:"log_level"`    // whatsmeow log level: "INFO", "DEBUG", ...
}

// QuietHoursConfig for defining when the bot should not send messages
type QuietHoursConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Start    string `yaml:"start"`    // e.g. "22:00"
	End      string `yaml:"end"`      // e.g. "07:00"
	Timezone string `yaml:"timezone"` // e.g. "Europe/Berlin"
}

// IS24Config for ImmobilienScout24 settings
type IS24Config struct {
	Cookie               string        `yaml:"cookie"`
	MaxRequestsPerMinute int           `yaml:"max_requests_per_minute"`
	MinDelay             time.Duration `yaml:"min_delay"`
	MaxDelay             time.Duration `yaml:"max_delay"`
	UserAgents           []string      `yaml:"user_agents"`
	Proxy                ProxyConfig   `yaml:"proxy"`
}

// ProxyConfig routes Chrome traffic through an upstream HTTP/SOCKS proxy
// (e.g. Shifter, IPRoyal, Smartproxy residential). URL is the scheme+host+port
// only (e.g. "http://gw.example.com:7000"); credentials are answered via the
// Chrome DevTools fetch domain because Chrome ignores user:pass in
// --proxy-server.
type ProxyConfig struct {
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// BandwidthCapMB stops new browser fetches once this many MB have been
	// consumed in the current calendar month. 0 = unlimited (track only).
	BandwidthCapMB int `yaml:"bandwidth_cap_mb"`
}

// Enabled reports whether a proxy URL is configured.
func (p ProxyConfig) Enabled() bool { return strings.TrimSpace(p.URL) != "" }

// RequiresAuth reports whether credentials should be answered via the fetch
// auth handler.
func (p ProxyConfig) RequiresAuth() bool {
	return p.Enabled() && strings.TrimSpace(p.Username) != ""
}

// TelegramConfig for Telegram bot settings
type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   int64  `yaml:"chat_id"`
	Enabled  bool   `yaml:"enabled"`
}

// OpenAIConfig for GPT message enhancement
type OpenAIConfig struct {
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
	Enabled bool   `yaml:"enabled"`
}

// EmailConfig for IMAP monitoring of IS24-related provider replies.
type EmailConfig struct {
	Enabled  bool          `yaml:"enabled"`
	IMAPHost string        `yaml:"imap_host"` // host:port, implicit TLS, e.g. "imap.gmail.com:993"
	Username string        `yaml:"username"`
	Password string        `yaml:"password"` // app password
	Mailbox  string        `yaml:"mailbox"`  // default "INBOX"
	Lookback time.Duration `yaml:"lookback"` // coarse SINCE window, default 72h
	Senders  []string      `yaml:"senders"`  // From-substring filters; empty → built-in IS24 defaults
}

// ContactConfig for auto-contact settings
type ContactConfig struct {
	Enabled     bool           `yaml:"enabled"`
	TypeDelay   time.Duration  `yaml:"type_delay"`
	ActionDelay time.Duration  `yaml:"action_delay"`
	ChromePath  string         `yaml:"chrome_path"`
	Profile     ContactProfile `yaml:"profile"`
}

// ContactProfile contains applicant information for IS24 forms
type ContactProfile struct {
	Salutation    string `yaml:"salutation"` // FEMALE or MALE
	FirstName     string `yaml:"first_name"`
	LastName      string `yaml:"last_name"`
	Email         string `yaml:"email"`
	Phone         string `yaml:"phone"`
	Street        string `yaml:"street"`
	HouseNumber   string `yaml:"house_number"`
	PostalCode    string `yaml:"postal_code"`
	City          string `yaml:"city"`
	Adults        int    `yaml:"adults"`
	Children      int    `yaml:"children"`
	Pets          bool   `yaml:"pets"`
	Income        int    `yaml:"income"`       // Monthly net household income
	MoveInDate    string `yaml:"move_in_date"` // e.g. "flexibel" or date
	Employment    string `yaml:"employment"`   // e.g. "Unbefristet"
	RentArrears   bool   `yaml:"rent_arrears"` // Mietrückstände
	Insolvency    bool   `yaml:"insolvency"`   // Insolvenzverfahren
	Smoker        bool   `yaml:"smoker"`
	CommercialUse bool   `yaml:"commercial_use"`
}

// MessageConfig for contact message templates
type MessageConfig struct {
	TemplatePath string `yaml:"template_path"`
	SenderName   string `yaml:"sender_name"`
	SenderEmail  string `yaml:"sender_email"`
	SenderPhone  string `yaml:"sender_phone"`
}

// DefaultConfig returns configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		PollInterval: 30 * time.Minute,
		DatabasePath: "data/immobot.db",
		LogLevel:     "info",
		IS24: IS24Config{
			MaxRequestsPerMinute: 10,
			MinDelay:             2 * time.Second,
			MaxDelay:             8 * time.Second,
			UserAgents: []string{
				"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
				"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
				"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
				"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
				"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
			},
		},
		Telegram: TelegramConfig{
			Enabled: false,
		},
		WhatsApp: WhatsAppConfig{
			Enabled:   false,
			StorePath: "data/whatsapp.db",
			LogLevel:  "INFO",
		},
		OpenAI: OpenAIConfig{
			Model:   "gpt-4o-mini",
			Enabled: false,
		},
		Email: EmailConfig{
			Enabled:  false,
			Mailbox:  "INBOX",
			Lookback: 72 * time.Hour,
		},
		Contact: ContactConfig{
			Enabled:     false,
			TypeDelay:   50 * time.Millisecond,
			ActionDelay: 1 * time.Second,
		},
		Message: MessageConfig{
			TemplatePath: "configs/message_template.txt",
		},
		QuietHours: QuietHoursConfig{
			Enabled:  true, // Enabled by default
			Start:    "22:00",
			End:      "07:00",
			Timezone: "Europe/Berlin",
		},
		Web: WebConfig{
			Enabled: false,
			Addr:    "127.0.0.1:8080",
		},
		Backup: BackupConfig{
			Enabled:       true,
			Interval:      24 * time.Hour,
			RetentionDays: 7,
			Dir:           "data/backups",
		},
	}
}

// Load reads configuration from YAML file and environment variables
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	// Read YAML file. A missing config is treated as an error so typos don't
	// silently fall back to built-in defaults.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config file %q does not exist", path)
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	// Override with environment variables
	if v := os.Getenv("IS24_COOKIE"); v != "" {
		cfg.IS24.Cookie = v
	}
	applyEnvString("IS24_PROXY_URL", &cfg.IS24.Proxy.URL)
	applyEnvString("IS24_PROXY_USER", &cfg.IS24.Proxy.Username)
	applyEnvString("IS24_PROXY_PASS", &cfg.IS24.Proxy.Password)
	if err := applyEnvInt("IS24_PROXY_BANDWIDTH_CAP_MB", &cfg.IS24.Proxy.BandwidthCapMB); err != nil {
		return nil, err
	}
	if err := applyEnvBool("TELEGRAM_ENABLED", &cfg.Telegram.Enabled); err != nil {
		return nil, err
	}
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
		cfg.Telegram.BotToken = v
	}
	if v := os.Getenv("TELEGRAM_CHAT_ID"); v != "" {
		chatID, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid TELEGRAM_CHAT_ID: %w", err)
		}
		cfg.Telegram.ChatID = chatID
	}
	if err := applyEnvBool("OPENAI_ENABLED", &cfg.OpenAI.Enabled); err != nil {
		return nil, err
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.OpenAI.APIKey = v
	}
	if v := os.Getenv("OPENAI_MODEL"); v != "" {
		cfg.OpenAI.Model = v
	}
	if err := applyEnvBool("WHATSAPP_ENABLED", &cfg.WhatsApp.Enabled); err != nil {
		return nil, err
	}
	if v := os.Getenv("WHATSAPP_TARGET_PHONE"); v != "" {
		cfg.WhatsApp.TargetPhone = v
	}
	if v := os.Getenv("WHATSAPP_STORE_PATH"); v != "" {
		cfg.WhatsApp.StorePath = v
	}
	if v := os.Getenv("WHATSAPP_LOG_LEVEL"); v != "" {
		cfg.WhatsApp.LogLevel = v
	}
	if err := applyEnvBool("WEB_ENABLED", &cfg.Web.Enabled); err != nil {
		return nil, err
	}
	applyEnvString("WEB_ADDR", &cfg.Web.Addr)
	if err := applyEnvBool("CONTACT_ENABLED", &cfg.Contact.Enabled); err != nil {
		return nil, err
	}
	if v := os.Getenv("CONTACT_CHROME_PATH"); v != "" {
		cfg.Contact.ChromePath = v
	}
	if err := applyContactProfileEnv(&cfg.Contact.Profile); err != nil {
		return nil, err
	}
	if v := os.Getenv("DATABASE_PATH"); v != "" {
		cfg.DatabasePath = v
	}

	if err := applyEnvBool("EMAIL_ENABLED", &cfg.Email.Enabled); err != nil {
		return nil, err
	}
	applyEnvString("EMAIL_IMAP_HOST", &cfg.Email.IMAPHost)
	applyEnvString("EMAIL_USERNAME", &cfg.Email.Username)
	applyEnvString("EMAIL_PASSWORD", &cfg.Email.Password)
	applyEnvString("EMAIL_MAILBOX", &cfg.Email.Mailbox)

	if err := applyEnvBool("BACKUP_ENABLED", &cfg.Backup.Enabled); err != nil {
		return nil, err
	}
	if v := os.Getenv("BACKUP_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("BACKUP_INTERVAL: %w", err)
		}
		cfg.Backup.Interval = d
	}
	if v := os.Getenv("BACKUP_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("BACKUP_RETENTION_DAYS: %w", err)
		}
		cfg.Backup.RetentionDays = n
	}
	applyEnvString("BACKUP_DIR", &cfg.Backup.Dir)

	// Backward compatibility: with no campaigns configured, synthesize a single
	// "default" campaign from the global message/contact settings so existing
	// configs keep working unchanged.
	if len(cfg.Campaigns) == 0 {
		cfg.Campaigns = map[string]Campaign{
			"default": {
				MessageTemplatePath: cfg.Message.TemplatePath,
				Contact:             cfg.Contact.Profile,
			},
		}
		if cfg.DefaultCampaign == "" {
			cfg.DefaultCampaign = "default"
		}
	}

	return cfg, nil
}

// ResolveCampaign returns the campaign for the given category, falling back to
// DefaultCampaign and then to the global message/contact settings. Empty
// per-campaign fields are filled from the globals.
func (c *Config) ResolveCampaign(category string) Campaign {
	if camp, ok := c.Campaigns[category]; ok {
		return c.fillCampaign(camp)
	}
	if camp, ok := c.Campaigns[c.DefaultCampaign]; ok {
		return c.fillCampaign(camp)
	}
	return Campaign{
		MessageTemplatePath: c.Message.TemplatePath,
		Contact:             c.Contact.Profile,
	}
}

// HasCampaign reports whether a campaign with the given name is configured.
func (c *Config) HasCampaign(name string) bool {
	_, ok := c.Campaigns[name]
	return ok
}

func (c *Config) fillCampaign(camp Campaign) Campaign {
	if camp.MessageTemplatePath == "" {
		camp.MessageTemplatePath = c.Message.TemplatePath
	}
	// Field-by-field merge so a campaign can override JUST the fields it cares
	// about (e.g. wg sets adults=2) instead of having to duplicate the full
	// applicant profile. Zero values on the campaign side fall back to the
	// global. Booleans deliberately fall through unchanged — there's no
	// "unset" sentinel for a bool, so if you need a campaign that flips a
	// bool, set it explicitly in the campaign block.
	camp.Contact = mergeContactProfile(c.Contact.Profile, camp.Contact)
	return camp
}

// mergeContactProfile overlays `override` onto `base`, returning `base`'s
// value for any field that is at its zero value on the override side. Each
// string compared to "", each int compared to 0. Bool fields are NOT merged
// for the reason above — they pass through from override verbatim.
func mergeContactProfile(base, override ContactProfile) ContactProfile {
	merged := base

	if override.Salutation != "" {
		merged.Salutation = override.Salutation
	}
	if override.FirstName != "" {
		merged.FirstName = override.FirstName
	}
	if override.LastName != "" {
		merged.LastName = override.LastName
	}
	if override.Email != "" {
		merged.Email = override.Email
	}
	if override.Phone != "" {
		merged.Phone = override.Phone
	}
	if override.Street != "" {
		merged.Street = override.Street
	}
	if override.HouseNumber != "" {
		merged.HouseNumber = override.HouseNumber
	}
	if override.PostalCode != "" {
		merged.PostalCode = override.PostalCode
	}
	if override.City != "" {
		merged.City = override.City
	}
	if override.MoveInDate != "" {
		merged.MoveInDate = override.MoveInDate
	}
	if override.Employment != "" {
		merged.Employment = override.Employment
	}
	if override.Adults != 0 {
		merged.Adults = override.Adults
	}
	if override.Children != 0 {
		merged.Children = override.Children
	}
	if override.Income != 0 {
		merged.Income = override.Income
	}
	return merged
}

func applyEnvBool(name string, target *bool) error {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return nil
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		*target = true
	case "0", "false", "no", "n", "off":
		*target = false
	default:
		return fmt.Errorf("invalid %s: expected boolean, got %q", name, v)
	}
	return nil
}

func applyContactProfileEnv(p *ContactProfile) error {
	applyEnvString("CONTACT_SALUTATION", &p.Salutation)
	applyEnvString("CONTACT_FIRST_NAME", &p.FirstName)
	applyEnvString("CONTACT_LAST_NAME", &p.LastName)
	applyEnvString("CONTACT_EMAIL", &p.Email)
	applyEnvString("CONTACT_PHONE", &p.Phone)
	applyEnvString("CONTACT_STREET", &p.Street)
	applyEnvString("CONTACT_HOUSE_NUMBER", &p.HouseNumber)
	applyEnvString("CONTACT_POSTAL_CODE", &p.PostalCode)
	applyEnvString("CONTACT_CITY", &p.City)
	applyEnvString("CONTACT_MOVE_IN_DATE", &p.MoveInDate)
	applyEnvString("CONTACT_EMPLOYMENT", &p.Employment)
	if err := applyEnvInt("CONTACT_ADULTS", &p.Adults); err != nil {
		return err
	}
	if err := applyEnvInt("CONTACT_CHILDREN", &p.Children); err != nil {
		return err
	}
	if err := applyEnvInt("CONTACT_INCOME", &p.Income); err != nil {
		return err
	}
	if err := applyEnvBool("CONTACT_PETS", &p.Pets); err != nil {
		return err
	}
	if err := applyEnvBool("CONTACT_RENT_ARREARS", &p.RentArrears); err != nil {
		return err
	}
	if err := applyEnvBool("CONTACT_INSOLVENCY", &p.Insolvency); err != nil {
		return err
	}
	if err := applyEnvBool("CONTACT_SMOKER", &p.Smoker); err != nil {
		return err
	}
	return applyEnvBool("CONTACT_COMMERCIAL_USE", &p.CommercialUse)
}

func applyEnvString(name string, target *string) {
	if v := os.Getenv(name); v != "" {
		*target = v
	}
}

func applyEnvInt(name string, target *int) error {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", name, err)
	}
	*target = n
	return nil
}

// Validate checks if required configuration is present
func (c *Config) Validate() error {
	var problems []string

	if c.PollInterval <= 0 {
		problems = append(problems, "poll_interval must be greater than 0")
	}
	if strings.TrimSpace(c.DatabasePath) == "" {
		problems = append(problems, "database_path is required")
	}
	if strings.TrimSpace(c.IS24.Cookie) == "" {
		problems = append(problems, "is24.cookie or IS24_COOKIE is required")
	}
	if c.IS24.MaxRequestsPerMinute <= 0 {
		problems = append(problems, "is24.max_requests_per_minute must be greater than 0")
	}
	if c.IS24.MinDelay < 0 || c.IS24.MaxDelay < 0 {
		problems = append(problems, "is24 delays must be non-negative")
	}
	if c.IS24.MaxDelay < c.IS24.MinDelay {
		problems = append(problems, "is24.max_delay must be greater than or equal to min_delay")
	}

	if c.Telegram.Enabled {
		if strings.TrimSpace(c.Telegram.BotToken) == "" {
			problems = append(problems, "telegram.bot_token or TELEGRAM_BOT_TOKEN is required when telegram.enabled is true")
		}
		if c.Telegram.ChatID == 0 {
			problems = append(problems, "telegram.chat_id or TELEGRAM_CHAT_ID is required when telegram.enabled is true")
		}
	}
	if c.WhatsApp.Enabled && strings.TrimSpace(c.WhatsApp.TargetPhone) == "" {
		problems = append(problems, "whatsapp.target_phone or WHATSAPP_TARGET_PHONE is required when whatsapp.enabled is true")
	}
	if c.OpenAI.Enabled {
		if strings.TrimSpace(c.OpenAI.APIKey) == "" {
			problems = append(problems, "openai.api_key or OPENAI_API_KEY is required when openai.enabled is true")
		}
		if strings.TrimSpace(c.OpenAI.Model) == "" {
			problems = append(problems, "openai.model is required when openai.enabled is true")
		}
	}
	if c.Email.Enabled {
		if strings.TrimSpace(c.Email.IMAPHost) == "" {
			problems = append(problems, "email.imap_host or EMAIL_IMAP_HOST is required when email.enabled is true")
		}
		if strings.TrimSpace(c.Email.Username) == "" {
			problems = append(problems, "email.username or EMAIL_USERNAME is required when email.enabled is true")
		}
		if strings.TrimSpace(c.Email.Password) == "" {
			problems = append(problems, "email.password or EMAIL_PASSWORD is required when email.enabled is true")
		}
		if !c.OpenAI.Enabled {
			problems = append(problems, "openai must be enabled when email.enabled is true (classification requires it)")
		}
	}
	if c.Contact.Enabled {
		p := c.Contact.Profile
		required := map[string]string{
			"contact.profile.first_name or CONTACT_FIRST_NAME": p.FirstName,
			"contact.profile.last_name or CONTACT_LAST_NAME":   p.LastName,
			"contact.profile.email or CONTACT_EMAIL":           p.Email,
		}
		for label, value := range required {
			if strings.TrimSpace(value) == "" {
				problems = append(problems, label+" is required when contact.enabled is true")
			}
		}
		if p.Adults <= 0 {
			problems = append(problems, "contact.profile.adults or CONTACT_ADULTS must be greater than 0 when contact.enabled is true")
		}
		if c.Contact.TypeDelay < 0 || c.Contact.ActionDelay < 0 {
			problems = append(problems, "contact delays must be non-negative")
		}
	}
	if len(c.Campaigns) > 0 {
		if strings.TrimSpace(c.DefaultCampaign) == "" {
			problems = append(problems, "default_campaign is required when campaigns are configured")
		} else if !c.HasCampaign(c.DefaultCampaign) {
			problems = append(problems, "default_campaign must reference a configured campaign")
		}
	}
	if c.Message.TemplatePath == "" && len(c.Campaigns) == 0 {
		problems = append(problems, "message.template_path is required")
	}
	if !validClock(c.QuietHours.Start) {
		problems = append(problems, "quiet_hours.start must use HH:MM")
	}
	if !validClock(c.QuietHours.End) {
		problems = append(problems, "quiet_hours.end must use HH:MM")
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

// IsQuietTime checks if the current time is within quiet hours
func (c *Config) IsQuietTime() bool {
	if !c.QuietHours.Enabled {
		return false
	}
	return c.IsWithinQuietHours()
}

// IsWithinQuietHours checks the configured quiet-hours window regardless of the
// enabled flag. Runtime command overrides use this to turn quiet hours on even
// when the static config default is off.
func (c *Config) IsWithinQuietHours() bool {
	// Load timezone
	loc, err := time.LoadLocation(c.QuietHours.Timezone)
	if err != nil {
		loc = time.Local
	}

	now := time.Now().In(loc)
	currentMinutes := now.Hour()*60 + now.Minute()

	// Parse start time
	startHour, startMin := parseTimeString(c.QuietHours.Start)
	startMinutes := startHour*60 + startMin

	// Parse end time
	endHour, endMin := parseTimeString(c.QuietHours.End)
	endMinutes := endHour*60 + endMin

	// Handle overnight quiet hours (e.g., 22:00 - 07:00)
	if startMinutes > endMinutes {
		// Quiet time spans midnight
		return currentMinutes >= startMinutes || currentMinutes < endMinutes
	}

	// Same-day quiet hours (e.g., 12:00 - 14:00)
	return currentMinutes >= startMinutes && currentMinutes < endMinutes
}

// parseTimeString parses "HH:MM" format and returns hour and minute
func parseTimeString(s string) (int, int) {
	var hour, min int
	for i, part := range splitTime(s) {
		n := 0
		for _, c := range part {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if i == 0 {
			hour = n
		} else {
			min = n
		}
	}
	return hour, min
}

func splitTime(s string) []string {
	var parts []string
	var current string
	for _, c := range s {
		if c == ':' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	parts = append(parts, current)
	return parts
}

func validClock(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return false
	}
	min, err := strconv.Atoi(parts[1])
	return err == nil && min >= 0 && min <= 59
}
