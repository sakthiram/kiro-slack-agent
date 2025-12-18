package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	Slack     SlackConfig     `mapstructure:"slack"`
	Kiro      KiroConfig      `mapstructure:"kiro"`
	Session   SessionConfig   `mapstructure:"session"`
	Streaming StreamingConfig `mapstructure:"streaming"`
	Web       WebConfig       `mapstructure:"web"`
	Logging   LoggingConfig   `mapstructure:"logging"`
}

// SlackConfig holds Slack-specific configuration.
type SlackConfig struct {
	BotToken  string `mapstructure:"bot_token"`  // xoxb-... (required)
	AppToken  string `mapstructure:"app_token"`  // xapp-... (required)
	DebugMode bool   `mapstructure:"debug_mode"` // default: false
}

// KiroConfig holds Kiro CLI configuration.
type KiroConfig struct {
	BinaryPath      string        `mapstructure:"binary_path"`      // default: kiro-cli
	SessionBasePath string        `mapstructure:"session_base_path"` // default: /tmp/kiro-sessions
	StartupTimeout  time.Duration `mapstructure:"startup_timeout"`  // default: 30s
	ResponseTimeout time.Duration `mapstructure:"response_timeout"` // default: 120s
	MaxRetries      int           `mapstructure:"max_retries"`      // default: 1
}

// SessionConfig holds session management configuration.
type SessionConfig struct {
	IdleTimeout      time.Duration `mapstructure:"idle_timeout"`       // default: 30m
	MaxSessionsTotal int           `mapstructure:"max_sessions_total"` // default: 100
	MaxSessionsUser  int           `mapstructure:"max_sessions_user"`  // default: 5
	DatabasePath     string        `mapstructure:"database_path"`      // default: /tmp/kiro-agent/sessions.db
}

// StreamingConfig holds streaming output configuration.
type StreamingConfig struct {
	UpdateInterval time.Duration `mapstructure:"update_interval"` // default: 500ms
}

// WebConfig holds web interface configuration.
type WebConfig struct {
	Enabled              bool   `mapstructure:"enabled"`                 // default: false
	ListenAddr           string `mapstructure:"listen_addr"`             // default: :8080
	StaticPath           string `mapstructure:"static_path"`             // default: ./web/static
	MaxObserversPerSession int  `mapstructure:"max_observers_per_session"` // default: 10
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`  // debug, info, warn, error
	Format string `mapstructure:"format"` // json, console
}

// Load reads configuration from file and environment variables.
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Read config file if provided
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./configs")
		v.AddConfigPath(".")
	}

	// Environment variable support
	v.SetEnvPrefix("KIRO_AGENT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Read config file (ignore if not found)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate required fields
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	// Slack defaults
	v.SetDefault("slack.debug_mode", false)

	// Kiro defaults
	v.SetDefault("kiro.binary_path", "kiro-cli")
	v.SetDefault("kiro.session_base_path", "/tmp/kiro-sessions")
	v.SetDefault("kiro.startup_timeout", 30*time.Second)
	v.SetDefault("kiro.response_timeout", 120*time.Second)
	v.SetDefault("kiro.max_retries", 1)

	// Session defaults
	v.SetDefault("session.idle_timeout", 30*time.Minute)
	v.SetDefault("session.max_sessions_total", 100)
	v.SetDefault("session.max_sessions_user", 5)
	v.SetDefault("session.database_path", "/tmp/kiro-agent/sessions.db")

	// Streaming defaults
	v.SetDefault("streaming.update_interval", 500*time.Millisecond)

	// Web defaults
	v.SetDefault("web.enabled", false)
	v.SetDefault("web.listen_addr", ":8080")
	v.SetDefault("web.static_path", "./web/static")
	v.SetDefault("web.max_observers_per_session", 10)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
}

func validate(cfg *Config) error {
	if cfg.Slack.BotToken == "" {
		return fmt.Errorf("slack.bot_token is required")
	}
	if cfg.Slack.AppToken == "" {
		return fmt.Errorf("slack.app_token is required")
	}

	// Validate token prefixes
	if !strings.HasPrefix(cfg.Slack.BotToken, "xoxb-") {
		return fmt.Errorf("slack.bot_token must start with 'xoxb-'")
	}
	if !strings.HasPrefix(cfg.Slack.AppToken, "xapp-") {
		return fmt.Errorf("slack.app_token must start with 'xapp-'")
	}

	// Validate logging level
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[cfg.Logging.Level] {
		return fmt.Errorf("logging.level must be one of: debug, info, warn, error")
	}

	// Validate logging format
	validFormats := map[string]bool{"json": true, "console": true}
	if !validFormats[cfg.Logging.Format] {
		return fmt.Errorf("logging.format must be one of: json, console")
	}

	return nil
}
