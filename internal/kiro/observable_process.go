package kiro

import (
	"context"
	"sync"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
)

const (
	// DefaultScrollbackSize is the default size for the scrollback buffer (64KB)
	DefaultScrollbackSize = 64 * 1024

	// DefaultObserverChanSize is the buffer size for observer channels
	DefaultObserverChanSize = 100
)

// ObservableProcess wraps Process to broadcast PTY output to multiple observers.
// It allows web clients to "attach" to the PTY output stream similar to tmux attach.
type ObservableProcess struct {
	*Process
	observers  map[string]chan []byte // observerID -> output channel
	scrollback *RingBuffer            // Last N bytes for late joiners
	mu         sync.RWMutex
	logger     *zap.Logger
}

// NewObservableProcess creates a new observable process wrapper.
func NewObservableProcess(sessionDir string, cfg *config.KiroConfig, logger *zap.Logger) *ObservableProcess {
	return &ObservableProcess{
		Process:    NewProcess(sessionDir, cfg, logger),
		observers:  make(map[string]chan []byte),
		scrollback: NewRingBuffer(DefaultScrollbackSize),
		logger:     logger,
	}
}

// AddObserver registers a new observer and returns a channel for receiving output.
// The channel will receive a copy of the scrollback history immediately,
// followed by all future output until RemoveObserver is called.
func (op *ObservableProcess) AddObserver(id string) <-chan []byte {
	op.mu.Lock()
	defer op.mu.Unlock()

	// Create observer channel
	ch := make(chan []byte, DefaultObserverChanSize)
	op.observers[id] = ch

	op.logger.Debug("observer added",
		zap.String("observer_id", id),
		zap.Int("total_observers", len(op.observers)),
	)

	// Send scrollback history to new observer
	scrollbackData := op.scrollback.Read()
	if len(scrollbackData) > 0 {
		// Non-blocking send
		select {
		case ch <- scrollbackData:
		default:
			op.logger.Warn("failed to send scrollback to new observer",
				zap.String("observer_id", id),
			)
		}
	}

	return ch
}

// RemoveObserver unregisters an observer and closes its channel.
func (op *ObservableProcess) RemoveObserver(id string) {
	op.mu.Lock()
	defer op.mu.Unlock()

	if ch, exists := op.observers[id]; exists {
		close(ch)
		delete(op.observers, id)
		op.logger.Debug("observer removed",
			zap.String("observer_id", id),
			zap.Int("total_observers", len(op.observers)),
		)
	}
}

// GetScrollback returns a copy of the current scrollback buffer.
func (op *ObservableProcess) GetScrollback() []byte {
	return op.scrollback.Read()
}

// broadcast sends data to all observers in a non-blocking manner.
// If an observer's channel is full, the data is dropped for that observer.
func (op *ObservableProcess) broadcast(data []byte) {
	if len(data) == 0 {
		return
	}

	op.mu.RLock()
	defer op.mu.RUnlock()

	// Store in scrollback
	op.scrollback.Write(data)

	// Broadcast to all observers
	for id, ch := range op.observers {
		select {
		case ch <- data:
			// Successfully sent
		default:
			// Channel full, drop data
			op.logger.Warn("observer channel full, dropping data",
				zap.String("observer_id", id),
				zap.Int("data_size", len(data)),
			)
		}
	}
}

// SendMessage sends a message and streams the response.
// This override wraps the handler to broadcast output to observers.
func (op *ObservableProcess) SendMessage(ctx context.Context, message string, handler ResponseHandler) error {
	// Wrap the handler to broadcast raw output
	wrappedHandler := func(chunk string, isComplete bool) {
		// Broadcast to observers
		op.broadcast([]byte(chunk))

		// Call original handler
		if handler != nil {
			handler(chunk, isComplete)
		}
	}

	return op.Process.SendMessage(ctx, message, wrappedHandler)
}

// Start initializes the Kiro CLI process with PTY.
// This override clears the scrollback buffer on start.
func (op *ObservableProcess) Start(ctx context.Context) error {
	// Clear scrollback on start
	op.scrollback.Clear()

	return op.Process.Start(ctx)
}

// Close terminates the Kiro process and removes all observers.
func (op *ObservableProcess) Close() error {
	// Remove all observers first
	op.mu.Lock()
	for id := range op.observers {
		if ch, exists := op.observers[id]; exists {
			close(ch)
			delete(op.observers, id)
		}
	}
	op.mu.Unlock()

	// Close the underlying process
	return op.Process.Close()
}

// ObserverCount returns the current number of active observers.
func (op *ObservableProcess) ObserverCount() int {
	op.mu.RLock()
	defer op.mu.RUnlock()
	return len(op.observers)
}

// Ensure ObservableProcess implements Bridge.
var _ Bridge = (*ObservableProcess)(nil)
