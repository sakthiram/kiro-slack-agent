package kiro

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// RetryBridge wraps a Bridge with retry logic.
type RetryBridge struct {
	bridge     Bridge
	maxRetries int
	logger     *zap.Logger
}

// NewRetryBridge creates a bridge that retries on failure.
func NewRetryBridge(bridge Bridge, maxRetries int, logger *zap.Logger) *RetryBridge {
	return &RetryBridge{
		bridge:     bridge,
		maxRetries: maxRetries,
		logger:     logger,
	}
}

// Start initializes the Kiro process.
func (r *RetryBridge) Start(ctx context.Context) error {
	return r.bridge.Start(ctx)
}

// SendMessage sends a message with retry on failure.
func (r *RetryBridge) SendMessage(ctx context.Context, message string, handler ResponseHandler) error {
	var lastErr error
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		err := r.bridge.SendMessage(ctx, message, handler)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < r.maxRetries {
			r.logger.Warn("kiro failed, retrying",
				zap.Int("attempt", attempt+1),
				zap.Int("max_retries", r.maxRetries),
				zap.Error(err))
		}
	}
	return fmt.Errorf("kiro failed after %d attempts: %w", r.maxRetries+1, lastErr)
}

// IsRunning checks if the process is alive.
func (r *RetryBridge) IsRunning() bool {
	return r.bridge.IsRunning()
}

// Close terminates the Kiro process.
func (r *RetryBridge) Close() error {
	return r.bridge.Close()
}
