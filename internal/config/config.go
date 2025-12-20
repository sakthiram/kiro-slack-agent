package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	Slack   SlackConfig   `mapstructure:"slack"`
	Kiro    KiroConfig    `mapstructure:"kiro"`
	Beads   BeadsConfig   `mapstructure:"beads"`
	Worker  WorkerConfig  `mapstructure:"worker"`
	Sync    SyncConfig    `mapstructure:"sync"`
	Logging LoggingConfig `mapstructure:"logging"`
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

// BeadsConfig holds beads issue tracking configuration.
type BeadsConfig struct {
	SessionsBasePath   string `mapstructure:"sessions_base_path"`   // default: /var/kiro-agent/sessions
	IssuePrefix        string `mapstructure:"issue_prefix"`         // default: slack
	ContextMaxMessages int    `mapstructure:"context_max_messages"` // default: 20
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`  // debug, info, warn, error
	Format string `mapstructure:"format"` // json, console
}

// WorkerConfig holds worker pool configuration.
type WorkerConfig struct {
	PoolSize     int           `mapstructure:"pool_size"`     // default: 3
	PollInterval time.Duration `mapstructure:"poll_interval"` // default: 10s
	TaskTimeout  time.Duration `mapstructure:"task_timeout"`  // default: 5m
	MaxRetries   int           `mapstructure:"max_retries"`   // default: 2
	RetryBackoff time.Duration `mapstructure:"retry_backoff"` // default: 30s
}

// SyncConfig holds comment synchronization configuration.
type SyncConfig struct {
	SyncInterval time.Duration `mapstructure:"sync_interval"` // default: 5s
	Enabled      bool          `mapstructure:"enabled"`       // default: true
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

	// Beads defaults
	v.SetDefault("beads.sessions_base_path", "/var/kiro-agent/sessions")
	v.SetDefault("beads.issue_prefix", "slack")
	v.SetDefault("beads.context_max_messages", 20)

	// Worker defaults
	v.SetDefault("worker.pool_size", 3)
	v.SetDefault("worker.poll_interval", 10*time.Second)
	v.SetDefault("worker.task_timeout", 5*time.Minute)
	v.SetDefault("worker.max_retries", 2)
	v.SetDefault("worker.retry_backoff", 30*time.Second)

	// Sync defaults
	v.SetDefault("sync.sync_interval", 5*time.Second)
	v.SetDefault("sync.enabled", true)

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
