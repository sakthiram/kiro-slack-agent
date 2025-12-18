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

// Close closes the observer's channels
func (o *ObserverV2) Close() {
	close(o.Done)
	close(o.SendChan)
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
	observers := r.sessions[sessionID]
	r.mu.RUnlock()

	if len(observers) == 0 {
		return
	}

	r.logger.Debug("broadcasting to observers",
		zap.String("session_id", sessionID),
		zap.Int("observer_count", len(observers)),
		zap.Int("data_size", len(data)))

	// Broadcast to all observers (non-blocking)
	for observerID, observer := range observers {
		select {
		case observer.SendChan <- data:
			// Successfully sent
		default:
			// Channel full, skip this observer to avoid blocking
			r.logger.Warn("observer channel full, skipping message",
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
