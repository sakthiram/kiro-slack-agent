package kiro

import (
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
)

// BridgeFactory creates bridge instances for communicating with Kiro.
// This abstraction allows tests to inject mock bridges.
type BridgeFactory interface {
	// CreateBridge creates a new Process instance.
	// The caller is responsible for wrapping it with RetryBridge if needed.
	CreateBridge(sessionDir string, cfg *config.KiroConfig, logger *zap.Logger) (*Process, error)
}

// DefaultBridgeFactory creates real Process instances.
type DefaultBridgeFactory struct{}

// NewDefaultBridgeFactory creates a new default bridge factory.
func NewDefaultBridgeFactory() *DefaultBridgeFactory {
	return &DefaultBridgeFactory{}
}

// CreateBridge creates a new Process instance.
func (f *DefaultBridgeFactory) CreateBridge(sessionDir string, cfg *config.KiroConfig, logger *zap.Logger) (*Process, error) {
	process := NewProcess(sessionDir, cfg, logger)
	return process, nil
}

// Ensure DefaultBridgeFactory implements BridgeFactory.
var _ BridgeFactory = (*DefaultBridgeFactory)(nil)
