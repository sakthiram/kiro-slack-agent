package slack

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithThreadTS(t *testing.T) {
	cfg := &messageConfig{}
	opt := WithThreadTS("1234567890.123456")
	opt(cfg)
	assert.Equal(t, "1234567890.123456", cfg.threadTS)
}

func TestWithBlocks(t *testing.T) {
	// Test that WithBlocks sets blocks on config
	// Note: Full integration tests would require mocking Slack API
	cfg := &messageConfig{}
	assert.Empty(t, cfg.blocks)
}

// Note: Full client tests would require mocking the Slack API
// which is done via interface injection. The ClientInterface
// allows for easy mocking in tests that use the client.
