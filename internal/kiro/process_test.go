package kiro

import (
	"context"
	"testing"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNewProcess(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	process := NewProcess("/tmp/test-session", cfg, logger)

	assert.NotNil(t, process)
	assert.Equal(t, "/tmp/test-session", process.sessionDir)
	assert.Equal(t, "kiro-cli", process.binaryPath)
	assert.Equal(t, 30*time.Second, process.startupTimeout)
	assert.Equal(t, 120*time.Second, process.responseTimeout)
	assert.NotNil(t, process.parser)
	assert.False(t, process.running)
}

func TestProcess_IsRunning_NotStarted(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	process := NewProcess("/tmp/test-session", cfg, logger)

	assert.False(t, process.IsRunning())
}

func TestProcess_Close_NotRunning(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	process := NewProcess("/tmp/test-session", cfg, logger)

	// Close when not running should succeed
	err := process.Close()
	assert.NoError(t, err)
}

func TestProcess_SendMessage_NotRunning(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	process := NewProcess("/tmp/test-session", cfg, logger)
	ctx := context.Background()

	err := process.SendMessage(ctx, "test", func(chunk string, isComplete bool) {})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "process not running")
}

func TestProcess_Start_InvalidBinary(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "/nonexistent/binary",
		StartupTimeout:  1 * time.Second,
		ResponseTimeout: 1 * time.Second,
	}

	process := NewProcess("/tmp/test-session", cfg, logger)
	ctx := context.Background()

	err := process.Start(ctx)
	assert.Error(t, err)
}

func TestProcess_ImplementsBridge(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	var _ Bridge = NewProcess("/tmp/test-session", cfg, logger)
}

func TestProcess_CloseIdempotent(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	process := NewProcess("/tmp/test-session", cfg, logger)

	// Multiple closes should be safe
	err := process.Close()
	assert.NoError(t, err)
	err = process.Close()
	assert.NoError(t, err)
}

func TestProcess_Start_AlreadyRunning(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "echo", // Use echo as a simple command that exists
		StartupTimeout:  100 * time.Millisecond,
		ResponseTimeout: 100 * time.Millisecond,
	}

	process := NewProcess("/tmp", cfg, logger)
	ctx := context.Background()

	// Manually set running to true to test the check
	process.mu.Lock()
	process.running = true
	process.mu.Unlock()

	// Start should return early when already running
	err := process.Start(ctx)
	assert.NoError(t, err)
}
