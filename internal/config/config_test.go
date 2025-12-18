package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_WithValidConfig(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
slack:
  bot_token: "xoxb-test-token"
  app_token: "xapp-test-token"
  debug_mode: true
kiro:
  binary_path: "/usr/local/bin/kiro"
  startup_timeout: "60s"
logging:
  level: "debug"
  format: "console"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Equal(t, "xoxb-test-token", cfg.Slack.BotToken)
	assert.Equal(t, "xapp-test-token", cfg.Slack.AppToken)
	assert.True(t, cfg.Slack.DebugMode)
	assert.Equal(t, "/usr/local/bin/kiro", cfg.Kiro.BinaryPath)
	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "console", cfg.Logging.Format)
}

func TestLoad_MissingBotToken(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
slack:
  app_token: "xapp-test-token"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	_, err = Load(configPath)
	assert.ErrorContains(t, err, "slack.bot_token is required")
}

func TestLoad_InvalidBotTokenPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
slack:
  bot_token: "invalid-token"
  app_token: "xapp-test-token"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	_, err = Load(configPath)
	assert.ErrorContains(t, err, "slack.bot_token must start with 'xoxb-'")
}

func TestLoad_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
slack:
  bot_token: "xoxb-file-token"
  app_token: "xapp-file-token"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Set env var to override
	os.Setenv("KIRO_AGENT_SLACK_BOT_TOKEN", "xoxb-env-token")
	defer os.Unsetenv("KIRO_AGENT_SLACK_BOT_TOKEN")

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Equal(t, "xoxb-env-token", cfg.Slack.BotToken)
}

func TestLoad_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
slack:
  bot_token: "xoxb-test-token"
  app_token: "xapp-test-token"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// Check defaults
	assert.Equal(t, "kiro-cli", cfg.Kiro.BinaryPath)
	assert.Equal(t, "/tmp/kiro-sessions", cfg.Kiro.SessionBasePath)
	assert.Equal(t, 100, cfg.Session.MaxSessionsTotal)
	assert.Equal(t, 5, cfg.Session.MaxSessionsUser)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)

	// Check web defaults
	assert.False(t, cfg.Web.Enabled)
	assert.Equal(t, ":8080", cfg.Web.ListenAddr)
	assert.Equal(t, "./web/static", cfg.Web.StaticPath)
	assert.Equal(t, 10, cfg.Web.MaxObserversPerSession)
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
slack:
  bot_token: "xoxb-test-token"
  app_token: "xapp-test-token"
logging:
  level: "invalid"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	_, err = Load(configPath)
	assert.ErrorContains(t, err, "logging.level must be one of")
}
