package kiro

import (
	"testing"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestDefaultBridgeFactory_CreateBridge tests the default bridge factory.
func TestDefaultBridgeFactory_CreateBridge(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath: "kiro-cli",
		MaxRetries: 1,
	}

	factory := NewDefaultBridgeFactory()
	assert.NotNil(t, factory)

	// Create a temporary directory for the session
	sessionDir := t.TempDir()

	// Create bridge using factory
	bridge, err := factory.CreateBridge(sessionDir, cfg, logger)
	assert.NoError(t, err)
	assert.NotNil(t, bridge)

	// Verify the bridge is an ObservableProcess
	assert.NotNil(t, bridge)

	// Verify observer count is 0 initially
	assert.Equal(t, 0, bridge.ObserverCount())
}

// TestBridgeFactory_Interface verifies that DefaultBridgeFactory implements BridgeFactory.
func TestBridgeFactory_Interface(t *testing.T) {
	var _ BridgeFactory = (*DefaultBridgeFactory)(nil)
}
