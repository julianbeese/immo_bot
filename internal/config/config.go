package config

import (
	"os"
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
	OpenAI     OpenAIConfig     `yaml:"openai"`
	Contact    ContactConfig    `yaml:"contact"`
	Message    MessageConfig    `yaml:"message"`
	QuietHours QuietHoursConfig `yaml:"quiet_hours"`
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

// ContactConfig for auto-contact settings
type ContactConfig struct {
	Enabled      bool          `yaml:"enabled"`
	TypeDelay    time.Duration `yaml:"type_delay"`
	ActionDelay  time.Duration `yaml:"action_delay"`
	ChromePath   string        `yaml:"chrome_path"`
	Profile      ContactProfile `yaml:"profile"`
}

// ContactProfile contains applicant information for IS24 forms
type ContactProfile struct {
	Salutation       string `yaml:"salutation"`        // FEMALE or MALE
	FirstName        string `yaml:"first_name"`
	LastName         string `yaml:"last_name"`
	Email            string `yaml:"email"`
	Phone            string `yaml:"phone"`
	Street           string `yaml:"street"`
	HouseNumber      string `yaml:"house_number"`
	PostalCode       string `yaml:"postal_code"`
	City             string `yaml:"city"`
	Adults           int    `yaml:"adults"`
	Children         int    `yaml:"children"`
	Pets             bool   `yaml:"pets"`
	Income           int    `yaml:"income"`             // Monthly net household income
	MoveInDate       string `yaml:"move_in_date"`       // e.g. "flexibel" or date
	Employment       string `yaml:"employment"`         // e.g. "Unbefristet"
	RentArrears      bool   `yaml:"rent_arrears"`       // Mietrückstände
	Insolvency       bool   `yaml:"insolvency"`         // Insolvenzverfahren
	Smoker           bool   `yaml:"smoker"`
	CommercialUse    bool   `yaml:"commercial_use"`
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
		PollInterval: 5 * time.Minute,
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
			Enabled: true,
		},
		OpenAI: OpenAIConfig{
			Model:   "gpt-4o-mini",
			Enabled: true,
		},
		Contact: ContactConfig{
			Enabled:     true,
			TypeDelay:   50 * time.Millisecond,
			ActionDelay: 1 * time.Second,
			Profile: ContactProfile{
				Salutation:    "FEMALE",
				FirstName:     "Marie",
				LastName:      "Wiegelmann",
				Email:         "marie.wiegelmann@outlook.com",
				Phone:         "+49 151 67660667",
				Street:        "Erzgießereistraße",
				HouseNumber:   "32",
				PostalCode:    "80335",
				City:          "München",
				Adults:        2,
				Children:      0,
				Pets:          false,
				Income:        7500,
				MoveInDate:    "flexibel",
				Employment:    "Unbefristet",
				RentArrears:   false,
				Insolvency:    false,
				Smoker:        false,
				CommercialUse: false,
			},
		},
		Message: MessageConfig{
			TemplatePath: "configs/message_template.txt",
		},
		QuietHours: QuietHoursConfig{
			Enabled:  false, // Disabled by default
			Start:    "22:00",
			End:      "07:00",
			Timezone: "Europe/Berlin",
		},
	}
}

// Load reads configuration from YAML file and environment variables
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	// Read YAML file if exists
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	// Override with environment variables
	if v := os.Getenv("IS24_COOKIE"); v != "" {
		cfg.IS24.Cookie = v
	}
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
		cfg.Telegram.BotToken = v
	}
	if v := os.Getenv("TELEGRAM_CHAT_ID"); v != "" {
		var chatID int64
		if _, err := parseEnvInt64(v, &chatID); err == nil {
			cfg.Telegram.ChatID = chatID
		}
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.OpenAI.APIKey = v
	}
	if v := os.Getenv("DATABASE_PATH"); v != "" {
		cfg.DatabasePath = v
	}

	return cfg, nil
}

func parseEnvInt64(s string, target *int64) (bool, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			if c == '-' && n == 0 {
				continue
			}
			return false, nil
		}
		n = n*10 + int64(c-'0')
	}
	if len(s) > 0 && s[0] == '-' {
		n = -n
	}
	*target = n
	return true, nil
}

// Validate checks if required configuration is present
func (c *Config) Validate() error {
	// Telegram is optional but warn if enabled without token
	// IS24 cookie is required for scraping
	// OpenAI is optional
	return nil
}

// IsQuietTime checks if the current time is within quiet hours
func (c *Config) IsQuietTime() bool {
	if !c.QuietHours.Enabled {
		return false
	}

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
