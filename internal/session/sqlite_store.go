package session

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    thread_ts TEXT NOT NULL,
    user_id TEXT NOT NULL,
    kiro_session_dir TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    last_activity_at DATETIME NOT NULL,
    status INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_channel_id ON sessions(channel_id);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_sessions_last_activity ON sessions(last_activity_at);
`

// SQLiteStore implements SessionStore using SQLite.
type SQLiteStore struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewSQLiteStore creates a new SQLite-backed session store.
func NewSQLiteStore(dbPath string, logger *zap.Logger) (*SQLiteStore, error) {
	// Create directory if it doesn't exist (skip for in-memory)
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Initialize schema
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return &SQLiteStore{
		db:     db,
		logger: logger,
	}, nil
}

// Get retrieves a session by ID.
func (s *SQLiteStore) Get(ctx context.Context, id SessionID) (*Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, channel_id, thread_ts, user_id, kiro_session_dir,
		       created_at, last_activity_at, status
		FROM sessions WHERE id = ?
	`, string(id))

	session, err := s.scanSession(row)
	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return session, nil
}

// Save creates or updates a session.
func (s *SQLiteStore) Save(ctx context.Context, session *Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, channel_id, thread_ts, user_id, kiro_session_dir,
		                      created_at, last_activity_at, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		    last_activity_at = excluded.last_activity_at,
		    status = excluded.status
	`,
		string(session.ID),
		session.ChannelID,
		session.ThreadTS,
		session.UserID,
		session.KiroSessionDir,
		session.CreatedAt,
		session.LastActivityAt,
		int(session.Status),
	)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}
	return nil
}

// Delete removes a session.
func (s *SQLiteStore) Delete(ctx context.Context, id SessionID) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", string(id))
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// List returns all sessions.
func (s *SQLiteStore) List(ctx context.Context) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, user_id, kiro_session_dir,
		       created_at, last_activity_at, status
		FROM sessions
		ORDER BY last_activity_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// ListByUser returns sessions for a specific user.
func (s *SQLiteStore) ListByUser(ctx context.Context, userID string) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, user_id, kiro_session_dir,
		       created_at, last_activity_at, status
		FROM sessions WHERE user_id = ?
		ORDER BY last_activity_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions by user: %w", err)
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// ListByChannel returns sessions for a specific channel.
func (s *SQLiteStore) ListByChannel(ctx context.Context, channelID string) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, user_id, kiro_session_dir,
		       created_at, last_activity_at, status
		FROM sessions WHERE channel_id = ?
		ORDER BY last_activity_at DESC
	`, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions by channel: %w", err)
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// ListIdle returns sessions idle longer than the specified time.
func (s *SQLiteStore) ListIdle(ctx context.Context, idleSince time.Time) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, user_id, kiro_session_dir,
		       created_at, last_activity_at, status
		FROM sessions WHERE last_activity_at < ?
		ORDER BY last_activity_at ASC
	`, idleSince)
	if err != nil {
		return nil, fmt.Errorf("failed to list idle sessions: %w", err)
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// ListByStatus returns sessions with the specified status.
func (s *SQLiteStore) ListByStatus(ctx context.Context, status SessionStatus) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, thread_ts, user_id, kiro_session_dir,
		       created_at, last_activity_at, status
		FROM sessions WHERE status = ?
		ORDER BY last_activity_at DESC
	`, int(status))
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions by status: %w", err)
	}
	defer rows.Close()

	return s.scanSessions(rows)
}

// Count returns the total number of sessions.
func (s *SQLiteStore) Count(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count sessions: %w", err)
	}
	return count, nil
}

// CountByUser returns the number of sessions for a specific user.
func (s *SQLiteStore) CountByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE user_id = ?", userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count user sessions: %w", err)
	}
	return count, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// scanSession scans a single session from a row.
func (s *SQLiteStore) scanSession(row *sql.Row) (*Session, error) {
	var session Session
	var id string
	var status int

	err := row.Scan(
		&id,
		&session.ChannelID,
		&session.ThreadTS,
		&session.UserID,
		&session.KiroSessionDir,
		&session.CreatedAt,
		&session.LastActivityAt,
		&status,
	)
	if err != nil {
		return nil, err
	}

	session.ID = SessionID(id)
	session.Status = SessionStatus(status)
	return &session, nil
}

// scanSessions scans multiple sessions from rows.
func (s *SQLiteStore) scanSessions(rows *sql.Rows) ([]*Session, error) {
	var sessions []*Session

	for rows.Next() {
		var session Session
		var id string
		var status int

		err := rows.Scan(
			&id,
			&session.ChannelID,
			&session.ThreadTS,
			&session.UserID,
			&session.KiroSessionDir,
			&session.CreatedAt,
			&session.LastActivityAt,
			&status,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}

		session.ID = SessionID(id)
		session.Status = SessionStatus(status)
		sessions = append(sessions, &session)
	}

	return sessions, rows.Err()
}
