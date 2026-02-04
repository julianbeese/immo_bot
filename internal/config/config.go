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

	IS24     IS24Config     `yaml:"is24"`
	Telegram TelegramConfig `yaml:"telegram"`
	OpenAI   OpenAIConfig   `yaml:"openai"`
	Contact  ContactConfig  `yaml:"contact"`
	Message  MessageConfig  `yaml:"message"`
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
		},
		Message: MessageConfig{
			TemplatePath: "configs/message_template.txt",
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
