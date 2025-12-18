package web

import (
	"context"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"go.uber.org/zap"
)

// Observer represents a WebSocket connection observing a session.
type Observer struct {
	conn      *websocket.Conn
	sessionID session.SessionID
	send      chan []byte
	done      chan struct{}
}

// ObserverRegistry manages WebSocket connections for session observation.
type ObserverRegistry struct {
	mu           sync.RWMutex
	observers    map[session.SessionID][]*Observer
	maxObservers int
	logger       *zap.Logger
}

// NewObserverRegistry creates a new observer registry.
func NewObserverRegistry(maxObservers int, logger *zap.Logger) *ObserverRegistry {
	return &ObserverRegistry{
		observers:    make(map[session.SessionID][]*Observer),
		maxObservers: maxObservers,
		logger:       logger,
	}
}

// Register adds a new observer for a session.
func (r *ObserverRegistry) Register(sessionID session.SessionID, conn *websocket.Conn) (*Observer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if session has room for more observers
	observers := r.observers[sessionID]
	if len(observers) >= r.maxObservers {
		return nil, fmt.Errorf("max observers (%d) reached for session", r.maxObservers)
	}

	// Create new observer
	observer := &Observer{
		conn:      conn,
		sessionID: sessionID,
		send:      make(chan []byte, 256),
		done:      make(chan struct{}),
	}

	// Add to registry
	r.observers[sessionID] = append(observers, observer)

	r.logger.Info("registered observer",
		zap.String("session_id", string(sessionID)),
		zap.Int("total_observers", len(r.observers[sessionID])),
	)

	return observer, nil
}

// Unregister removes an observer from a session.
func (r *ObserverRegistry) Unregister(observer *Observer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	observers := r.observers[observer.sessionID]
	for i, obs := range observers {
		if obs == observer {
			// Remove observer from slice
			r.observers[observer.sessionID] = append(observers[:i], observers[i+1:]...)
			close(observer.done)
			break
		}
	}

	// Clean up empty sessions
	if len(r.observers[observer.sessionID]) == 0 {
		delete(r.observers, observer.sessionID)
	}

	r.logger.Info("unregistered observer",
		zap.String("session_id", string(observer.sessionID)),
		zap.Int("remaining_observers", len(r.observers[observer.sessionID])),
	)
}

// Broadcast sends data to all observers of a session.
func (r *ObserverRegistry) Broadcast(sessionID session.SessionID, data []byte) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	observers := r.observers[sessionID]
	for _, observer := range observers {
		select {
		case observer.send <- data:
		default:
			// Channel full, skip this observer
			r.logger.Warn("observer send channel full",
				zap.String("session_id", string(sessionID)),
			)
		}
	}
}

// ObserverCount returns the number of observers for a session.
func (r *ObserverRegistry) ObserverCount(sessionID session.SessionID) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.observers[sessionID])
}

// SendLoop handles sending messages to the WebSocket connection.
func (o *Observer) SendLoop(ctx context.Context) {
	defer func() {
		if o.conn != nil {
			o.conn.Close()
		}
	}()

	for {
		select {
		case data := <-o.send:
			if o.conn != nil {
				if err := o.conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}
			}
		case <-o.done:
			return
		case <-ctx.Done():
			return
		}
	}
}
