package session

import (
	"errors"
	"time"
)

// SessionID uniquely identifies a session (thread timestamp).
type SessionID string

// SessionStatus represents the current state of a session.
type SessionStatus int

const (
	// SessionStatusActive indicates the session is active and ready.
	SessionStatusActive SessionStatus = iota
	// SessionStatusProcessing indicates the session is processing a request.
	SessionStatusProcessing
	// SessionStatusIdle indicates the session has been idle.
	SessionStatusIdle
	// SessionStatusClosed indicates the session has been closed.
	SessionStatusClosed
)

// String returns a human-readable status string.
func (s SessionStatus) String() string {
	switch s {
	case SessionStatusActive:
		return "active"
	case SessionStatusProcessing:
		return "processing"
	case SessionStatusIdle:
		return "idle"
	case SessionStatusClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// Session represents a Kiro agent session tied to a Slack thread.
type Session struct {
	ID             SessionID     // Thread timestamp as ID
	ChannelID      string        // Slack channel
	ThreadTS       string        // Thread timestamp (same as ID)
	UserID         string        // User who started session
	KiroSessionDir string        // Directory for Kiro persistence
	CreatedAt      time.Time     // When session was created
	LastActivityAt time.Time     // Last activity timestamp
	Status         SessionStatus // Current session status
}

// NewSession creates a new session with the given parameters.
func NewSession(channelID, threadTS, userID, kiroDir string) *Session {
	now := time.Now()
	return &Session{
		ID:             SessionID(threadTS),
		ChannelID:      channelID,
		ThreadTS:       threadTS,
		UserID:         userID,
		KiroSessionDir: kiroDir,
		CreatedAt:      now,
		LastActivityAt: now,
		Status:         SessionStatusActive,
	}
}

// UpdateActivity updates the last activity timestamp.
func (s *Session) UpdateActivity() {
	s.LastActivityAt = time.Now()
}

// IsIdle checks if the session has been idle for longer than the given duration.
func (s *Session) IsIdle(idleTimeout time.Duration) bool {
	return time.Since(s.LastActivityAt) > idleTimeout
}

// Errors
var (
	// ErrSessionNotFound is returned when a session doesn't exist.
	ErrSessionNotFound = errors.New("session not found")
	// ErrSessionLimitReached is returned when session limits are exceeded.
	ErrSessionLimitReached = errors.New("session limit reached")
	// ErrSessionClosed is returned when trying to use a closed session.
	ErrSessionClosed = errors.New("session is closed")
)
