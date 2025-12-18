package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// generateID generates a unique ID using crypto/rand
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based ID if crypto/rand fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ObserverV2 represents a connected web client watching a session
// This is a refactored version with explicit IDs and timestamps
type ObserverV2 struct {
	ID        string
	SessionID string
	SendChan  chan []byte
	Done      chan struct{}
	CreatedAt time.Time
	closeOnce sync.Once
}

// NewObserverV2 creates a new ObserverV2 with a unique ID
func NewObserverV2(sessionID string) *ObserverV2 {
	return &ObserverV2{
		ID:        generateID(),
		SessionID: sessionID,
		SendChan:  make(chan []byte, 100), // Buffered channel for 100 messages
		Done:      make(chan struct{}),
		CreatedAt: time.Now(),
	}
}

// Close closes the observer's Done channel to signal closure
// SendChan is not closed to avoid race conditions with concurrent sends
// Consumers should check Done channel before reading from SendChan
func (o *ObserverV2) Close() {
	o.closeOnce.Do(func() {
		close(o.Done)
		// Note: We intentionally do NOT close SendChan here to avoid races
		// with concurrent Broadcast operations. The channel will be garbage
		// collected when the observer is no longer referenced.
	})
}

// Send attempts to send data to the observer's channel non-blocking
// Returns true if sent, false if channel is full or closed
func (o *ObserverV2) Send(data []byte) bool {
	select {
	case o.SendChan <- data:
		return true
	case <-o.Done:
		// Observer is closed
		return false
	default:
		// Channel full
		return false
	}
}

// ObserverRegistryV2 manages all observer connections across sessions
// This is a refactored version with improved API and explicit observer IDs
type ObserverRegistryV2 struct {
	sessions map[string]map[string]*ObserverV2 // sessionID -> observerID -> Observer
	mu       sync.RWMutex
	logger   *zap.Logger
}

// NewObserverRegistryV2 creates a new observer registry
func NewObserverRegistryV2(logger *zap.Logger) *ObserverRegistryV2 {
	return &ObserverRegistryV2{
		sessions: make(map[string]map[string]*ObserverV2),
		logger:   logger,
	}
}

// Register adds an observer to the registry for a specific session
func (r *ObserverRegistryV2) Register(sessionID string, observer *ObserverV2) error {
	if sessionID == "" {
		return fmt.Errorf("sessionID cannot be empty")
	}
	if observer == nil {
		return fmt.Errorf("observer cannot be nil")
	}
	if observer.ID == "" {
		return fmt.Errorf("observer ID cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Initialize session map if it doesn't exist
	if r.sessions[sessionID] == nil {
		r.sessions[sessionID] = make(map[string]*ObserverV2)
	}

	// Check if observer already exists
	if _, exists := r.sessions[sessionID][observer.ID]; exists {
		return fmt.Errorf("observer %s already registered for session %s", observer.ID, sessionID)
	}

	r.sessions[sessionID][observer.ID] = observer
	r.logger.Info("observer registered",
		zap.String("session_id", sessionID),
		zap.String("observer_id", observer.ID),
		zap.Int("total_observers", len(r.sessions[sessionID])))

	return nil
}

// Unregister removes an observer from the registry
func (r *ObserverRegistryV2) Unregister(sessionID, observerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	observers, exists := r.sessions[sessionID]
	if !exists {
		return
	}

	observer, exists := observers[observerID]
	if !exists {
		return
	}

	// Close the observer's channels
	observer.Close()

	// Remove from map
	delete(observers, observerID)

	// Clean up empty session map
	if len(observers) == 0 {
		delete(r.sessions, sessionID)
	}

	r.logger.Info("observer unregistered",
		zap.String("session_id", sessionID),
		zap.String("observer_id", observerID),
		zap.Int("remaining_observers", len(observers)))
}

// Broadcast sends data to all observers of a session (non-blocking)
func (r *ObserverRegistryV2) Broadcast(sessionID string, data []byte) {
	r.mu.RLock()
	sessionObservers := r.sessions[sessionID]
	if len(sessionObservers) == 0 {
		r.mu.RUnlock()
		return
	}

	// Make a copy of the observers map to avoid holding the lock during broadcast
	observers := make(map[string]*ObserverV2, len(sessionObservers))
	for id, obs := range sessionObservers {
		observers[id] = obs
	}
	r.mu.RUnlock()

	r.logger.Debug("broadcasting to observers",
		zap.String("session_id", sessionID),
		zap.Int("observer_count", len(observers)),
		zap.Int("data_size", len(data)))

	// Broadcast to all observers (non-blocking)
	for observerID, observer := range observers {
		if !observer.Send(data) {
			// Failed to send - observer is either closed or channel is full
			r.logger.Warn("failed to send to observer",
				zap.String("session_id", sessionID),
				zap.String("observer_id", observerID))
		}
	}
}

// GetObservers returns a copy of all observers for a session
func (r *ObserverRegistryV2) GetObservers(sessionID string) []*ObserverV2 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	observers := r.sessions[sessionID]
	if observers == nil {
		return []*ObserverV2{}
	}

	// Return a copy to avoid race conditions
	result := make([]*ObserverV2, 0, len(observers))
	for _, observer := range observers {
		result = append(result, observer)
	}

	return result
}

// GetSessionCount returns the total number of sessions being observed
func (r *ObserverRegistryV2) GetSessionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.sessions)
}

// GetObserverCount returns the number of observers for a specific session
func (r *ObserverRegistryV2) GetObserverCount(sessionID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	observers := r.sessions[sessionID]
	if observers == nil {
		return 0
	}

	return len(observers)
}

// Close performs clean shutdown of all observers
func (r *ObserverRegistryV2) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Info("closing observer registry",
		zap.Int("session_count", len(r.sessions)))

	// Close all observers
	for sessionID, observers := range r.sessions {
		for observerID, observer := range observers {
			observer.Close()
			r.logger.Debug("closed observer",
				zap.String("session_id", sessionID),
				zap.String("observer_id", observerID))
		}
	}

	// Clear all sessions
	r.sessions = make(map[string]map[string]*ObserverV2)

	r.logger.Info("observer registry closed")
}
