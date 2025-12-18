package kiro

import (
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
)

// BridgeFactory creates bridge instances for communicating with Kiro.
// This abstraction allows tests to inject mock bridges.
type BridgeFactory interface {
	// CreateBridge creates a new ObservableProcess instance.
	// The caller is responsible for wrapping it with RetryBridge if needed.
	CreateBridge(sessionDir string, cfg *config.KiroConfig, logger *zap.Logger) (*ObservableProcess, error)
}

// DefaultBridgeFactory creates real ObservableProcess instances.
type DefaultBridgeFactory struct{}

// NewDefaultBridgeFactory creates a new default bridge factory.
func NewDefaultBridgeFactory() *DefaultBridgeFactory {
	return &DefaultBridgeFactory{}
}

// CreateBridge creates a new ObservableProcess instance.
func (f *DefaultBridgeFactory) CreateBridge(sessionDir string, cfg *config.KiroConfig, logger *zap.Logger) (*ObservableProcess, error) {
	// Create ObservableProcess which wraps Process and enables broadcasting
	observable := NewObservableProcess(sessionDir, cfg, logger)
	return observable, nil
}

// Ensure DefaultBridgeFactory implements BridgeFactory.
var _ BridgeFactory = (*DefaultBridgeFactory)(nil)
