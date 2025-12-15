package logging

import (
	"testing"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogger_JSON(t *testing.T) {
	cfg := &config.LoggingConfig{
		Level:  "info",
		Format: "json",
	}

	logger, err := NewLogger(cfg)
	require.NoError(t, err)
	assert.NotNil(t, logger)

	// Verify it can log without panic
	logger.Info("test message")
}

func TestNewLogger_Console(t *testing.T) {
	cfg := &config.LoggingConfig{
		Level:  "debug",
		Format: "console",
	}

	logger, err := NewLogger(cfg)
	require.NoError(t, err)
	assert.NotNil(t, logger)

	// Verify it can log without panic
	logger.Debug("test message")
}

func TestWithSessionID(t *testing.T) {
	cfg := &config.LoggingConfig{Level: "info", Format: "json"}
	logger, _ := NewLogger(cfg)

	sessionLogger := WithSessionID(logger, "test-session-123")
	assert.NotNil(t, sessionLogger)
}

func TestWithUserID(t *testing.T) {
	cfg := &config.LoggingConfig{Level: "info", Format: "json"}
	logger, _ := NewLogger(cfg)

	userLogger := WithUserID(logger, "U12345")
	assert.NotNil(t, userLogger)
}

func TestWithChannelID(t *testing.T) {
	cfg := &config.LoggingConfig{Level: "info", Format: "json"}
	logger, _ := NewLogger(cfg)

	channelLogger := WithChannelID(logger, "C12345")
	assert.NotNil(t, channelLogger)
}

func TestWithContext(t *testing.T) {
	cfg := &config.LoggingConfig{Level: "info", Format: "json"}
	logger, _ := NewLogger(cfg)

	contextLogger := WithContext(logger, "session-1", "U123", "C456")
	assert.NotNil(t, contextLogger)
}
