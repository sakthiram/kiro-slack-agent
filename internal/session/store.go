package session

import (
	"context"
	"time"
)

// SessionStore defines the interface for session persistence.
type SessionStore interface {
	// Get retrieves a session by ID, returns ErrSessionNotFound if not exists.
	Get(ctx context.Context, id SessionID) (*Session, error)

	// Save creates or updates a session.
	Save(ctx context.Context, session *Session) error

	// Delete removes a session.
	Delete(ctx context.Context, id SessionID) error

	// List returns all sessions.
	List(ctx context.Context) ([]*Session, error)

	// ListByUser returns sessions for a specific user.
	ListByUser(ctx context.Context, userID string) ([]*Session, error)

	// ListByChannel returns sessions for a specific channel.
	ListByChannel(ctx context.Context, channelID string) ([]*Session, error)

	// ListIdle returns sessions idle longer than the specified time.
	ListIdle(ctx context.Context, idleSince time.Time) ([]*Session, error)

	// ListByStatus returns sessions with the specified status.
	ListByStatus(ctx context.Context, status SessionStatus) ([]*Session, error)

	// Count returns the total number of sessions.
	Count(ctx context.Context) (int, error)

	// CountByUser returns the number of sessions for a specific user.
	CountByUser(ctx context.Context, userID string) (int, error)

	// Close closes the store and releases resources.
	Close() error
}
