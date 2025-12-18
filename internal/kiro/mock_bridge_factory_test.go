package kiro

import (
	"context"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
)

// MockBridgeFactory is a mock implementation of BridgeFactory for testing.
// It allows tests to inject custom bridge behavior without starting real Kiro processes.
type MockBridgeFactory struct {
	CreateBridgeFunc func(sessionDir string, cfg *config.KiroConfig, logger *zap.Logger) (*ObservableProcess, error)
}

// CreateBridge calls the mock function if set, otherwise returns a default ObservableProcess.
func (m *MockBridgeFactory) CreateBridge(sessionDir string, cfg *config.KiroConfig, logger *zap.Logger) (*ObservableProcess, error) {
	if m.CreateBridgeFunc != nil {
		return m.CreateBridgeFunc(sessionDir, cfg, logger)
	}

	// By default, create a real ObservableProcess
	// Tests can provide their own CreateBridgeFunc to customize behavior
	return NewObservableProcess(sessionDir, cfg, logger), nil
}

// MockProcess is a mock implementation of Process for testing.
type MockProcess struct {
	running          bool
	startFunc        func(ctx context.Context) error
	sendMessageFunc  func(ctx context.Context, message string, handler ResponseHandler) error
	closeFunc        func() error
}

// NewMockProcess creates a new mock process.
func NewMockProcess() *MockProcess {
	return &MockProcess{
		running: false,
		startFunc: func(ctx context.Context) error {
			return nil
		},
		sendMessageFunc: func(ctx context.Context, message string, handler ResponseHandler) error {
			// Simulate a response
			if handler != nil {
				handler("Mock response: "+message, true)
			}
			return nil
		},
		closeFunc: func() error {
			return nil
		},
	}
}

// Start simulates starting the process.
func (m *MockProcess) Start(ctx context.Context) error {
	if m.startFunc != nil {
		err := m.startFunc(ctx)
		if err == nil {
			m.running = true
		}
		return err
	}
	m.running = true
	return nil
}

// SendMessage simulates sending a message.
func (m *MockProcess) SendMessage(ctx context.Context, message string, handler ResponseHandler) error {
	if m.sendMessageFunc != nil {
		return m.sendMessageFunc(ctx, message, handler)
	}
	if handler != nil {
		handler("Mock response: "+message, true)
	}
	return nil
}

// IsRunning returns whether the mock process is running.
func (m *MockProcess) IsRunning() bool {
	return m.running
}

// Close simulates closing the process.
func (m *MockProcess) Close() error {
	if m.closeFunc != nil {
		err := m.closeFunc()
		if err == nil {
			m.running = false
		}
		return err
	}
	m.running = false
	return nil
}

// Ensure MockBridgeFactory implements BridgeFactory.
var _ BridgeFactory = (*MockBridgeFactory)(nil)

// Ensure MockProcess implements Bridge.
var _ Bridge = (*MockProcess)(nil)
