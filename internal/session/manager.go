package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
)

const (
	cleanupInterval = 5 * time.Minute
)

// Manager handles session lifecycle with automatic cleanup.
type Manager struct {
	store           SessionStore
	config          *config.SessionConfig
	kiroBasePath    string
	mu              sync.RWMutex
	logger          *zap.Logger
	stopCleanup     chan struct{}
	cleanupInterval time.Duration
}

// NewManager creates a new session manager.
func NewManager(
	store SessionStore,
	cfg *config.SessionConfig,
	kiroBasePath string,
	logger *zap.Logger,
) *Manager {
	return &Manager{
		store:           store,
		config:          cfg,
		kiroBasePath:    kiroBasePath,
		logger:          logger,
		stopCleanup:     make(chan struct{}),
		cleanupInterval: cleanupInterval,
	}
}

// GetOrCreate retrieves an existing session or creates a new one.
// Returns the session, whether it was newly created, and any error.
func (m *Manager) GetOrCreate(ctx context.Context, channelID, threadTS, userID string) (*Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionID := SessionID(threadTS)

	// Try to get existing session
	existing, err := m.store.Get(ctx, sessionID)
	if err == nil {
		// Update activity and return existing session
		existing.UpdateActivity()
		if err := m.store.Save(ctx, existing); err != nil {
			m.logger.Warn("failed to update session activity", zap.Error(err))
		}
		return existing, false, nil
	}

	if err != ErrSessionNotFound {
		return nil, false, fmt.Errorf("failed to get session: %w", err)
	}

	// Check user session limit
	userCount, err := m.store.CountByUser(ctx, userID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to count user sessions: %w", err)
	}

	if userCount >= m.config.MaxSessionsUser {
		return nil, false, ErrSessionLimitReached
	}

	// Check total session limit
	totalCount, err := m.store.Count(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to count total sessions: %w", err)
	}

	if totalCount >= m.config.MaxSessionsTotal {
		return nil, false, ErrSessionLimitReached
	}

	// Create new session directory
	sessionDir := filepath.Join(m.kiroBasePath, threadTS)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, false, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Create new session
	session := NewSession(channelID, threadTS, userID, sessionDir)

	if err := m.store.Save(ctx, session); err != nil {
		// Cleanup directory on failure
		os.RemoveAll(sessionDir)
		return nil, false, fmt.Errorf("failed to save session: %w", err)
	}

	m.logger.Info("created new session",
		zap.String("session_id", threadTS),
		zap.String("user_id", userID),
		zap.String("channel_id", channelID),
	)

	return session, true, nil
}

// Get retrieves a session by ID.
func (m *Manager) Get(ctx context.Context, id SessionID) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.Get(ctx, id)
}

// UpdateActivity marks a session as active.
func (m *Manager) UpdateActivity(ctx context.Context, id SessionID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}

	session.UpdateActivity()
	return m.store.Save(ctx, session)
}

// UpdateStatus changes the session status.
func (m *Manager) UpdateStatus(ctx context.Context, id SessionID, status SessionStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}

	session.Status = status
	session.UpdateActivity()
	return m.store.Save(ctx, session)
}

// Close terminates a session and cleans up resources.
func (m *Manager) Close(ctx context.Context, id SessionID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}

	// Remove session directory
	if session.KiroSessionDir != "" {
		if err := os.RemoveAll(session.KiroSessionDir); err != nil {
			m.logger.Warn("failed to remove session directory",
				zap.String("session_id", string(id)),
				zap.String("dir", session.KiroSessionDir),
				zap.Error(err),
			)
		}
	}

	// Delete from store
	if err := m.store.Delete(ctx, id); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	m.logger.Info("closed session",
		zap.String("session_id", string(id)),
		zap.String("user_id", session.UserID),
	)

	return nil
}

// Cleanup removes stale sessions.
func (m *Manager) Cleanup(ctx context.Context) error {
	idleSince := time.Now().Add(-m.config.IdleTimeout)

	sessions, err := m.store.ListIdle(ctx, idleSince)
	if err != nil {
		return fmt.Errorf("failed to list idle sessions: %w", err)
	}

	var closedCount int
	for _, session := range sessions {
		// Skip sessions currently processing
		if session.Status == SessionStatusProcessing {
			continue
		}

		if err := m.Close(ctx, session.ID); err != nil {
			m.logger.Warn("failed to close idle session",
				zap.String("session_id", string(session.ID)),
				zap.Error(err),
			)
			continue
		}
		closedCount++
	}

	if closedCount > 0 {
		m.logger.Info("cleanup completed",
			zap.Int("closed_sessions", closedCount),
		)
	}

	return nil
}

// Start begins the cleanup goroutine.
func (m *Manager) Start() {
	go m.cleanupLoop()
}

// Stop halts the cleanup goroutine.
func (m *Manager) Stop() {
	close(m.stopCleanup)
}

// cleanupLoop periodically runs cleanup.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx := context.Background()
			if err := m.Cleanup(ctx); err != nil {
				m.logger.Error("cleanup failed", zap.Error(err))
			}
		case <-m.stopCleanup:
			return
		}
	}
}

// List returns all sessions.
func (m *Manager) List(ctx context.Context) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.List(ctx)
}

// ListByUser returns sessions for a specific user.
func (m *Manager) ListByUser(ctx context.Context, userID string) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.ListByUser(ctx, userID)
}
