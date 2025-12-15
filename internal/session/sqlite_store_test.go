package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestStore(t *testing.T) *SQLiteStore {
	logger := zap.NewNop()
	store, err := NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSQLiteStore_SaveAndGet(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := NewSession("C123", "1234567890.123456", "U456", "/tmp/kiro/session1")

	// Save
	err := store.Save(ctx, session)
	require.NoError(t, err)

	// Get
	retrieved, err := store.Get(ctx, session.ID)
	require.NoError(t, err)

	assert.Equal(t, session.ID, retrieved.ID)
	assert.Equal(t, session.ChannelID, retrieved.ChannelID)
	assert.Equal(t, session.ThreadTS, retrieved.ThreadTS)
	assert.Equal(t, session.UserID, retrieved.UserID)
	assert.Equal(t, session.KiroSessionDir, retrieved.KiroSessionDir)
	assert.Equal(t, session.Status, retrieved.Status)
}

func TestSQLiteStore_GetNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.Get(ctx, SessionID("nonexistent"))
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestSQLiteStore_Update(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := NewSession("C123", "1234567890.123456", "U456", "/tmp/kiro/session1")
	require.NoError(t, store.Save(ctx, session))

	// Update status and activity
	session.Status = SessionStatusProcessing
	session.LastActivityAt = time.Now().Add(time.Hour)
	require.NoError(t, store.Save(ctx, session))

	// Verify update
	retrieved, err := store.Get(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, SessionStatusProcessing, retrieved.Status)
}

func TestSQLiteStore_Delete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := NewSession("C123", "1234567890.123456", "U456", "/tmp/kiro/session1")
	require.NoError(t, store.Save(ctx, session))

	// Delete
	err := store.Delete(ctx, session.ID)
	require.NoError(t, err)

	// Verify deleted
	_, err = store.Get(ctx, session.ID)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestSQLiteStore_DeleteNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Delete(ctx, SessionID("nonexistent"))
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestSQLiteStore_List(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create multiple sessions
	sessions := []*Session{
		NewSession("C123", "1234567890.111111", "U456", "/tmp/kiro/session1"),
		NewSession("C123", "1234567890.222222", "U456", "/tmp/kiro/session2"),
		NewSession("C789", "1234567890.333333", "U999", "/tmp/kiro/session3"),
	}

	for _, s := range sessions {
		require.NoError(t, store.Save(ctx, s))
	}

	// List all
	all, err := store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestSQLiteStore_ListByUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create sessions for different users
	require.NoError(t, store.Save(ctx, NewSession("C123", "1.1", "U456", "/tmp/1")))
	require.NoError(t, store.Save(ctx, NewSession("C123", "1.2", "U456", "/tmp/2")))
	require.NoError(t, store.Save(ctx, NewSession("C123", "1.3", "U999", "/tmp/3")))

	// List by user
	userSessions, err := store.ListByUser(ctx, "U456")
	require.NoError(t, err)
	assert.Len(t, userSessions, 2)

	for _, s := range userSessions {
		assert.Equal(t, "U456", s.UserID)
	}
}

func TestSQLiteStore_ListByChannel(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Save(ctx, NewSession("C123", "1.1", "U456", "/tmp/1")))
	require.NoError(t, store.Save(ctx, NewSession("C123", "1.2", "U789", "/tmp/2")))
	require.NoError(t, store.Save(ctx, NewSession("C999", "1.3", "U456", "/tmp/3")))

	channelSessions, err := store.ListByChannel(ctx, "C123")
	require.NoError(t, err)
	assert.Len(t, channelSessions, 2)
}

func TestSQLiteStore_ListIdle(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create sessions with different activity times
	recent := NewSession("C123", "1.1", "U456", "/tmp/1")
	require.NoError(t, store.Save(ctx, recent))

	old := NewSession("C123", "1.2", "U456", "/tmp/2")
	old.LastActivityAt = time.Now().Add(-2 * time.Hour)
	require.NoError(t, store.Save(ctx, old))

	// List idle sessions (older than 1 hour)
	idleSince := time.Now().Add(-1 * time.Hour)
	idleSessions, err := store.ListIdle(ctx, idleSince)
	require.NoError(t, err)
	assert.Len(t, idleSessions, 1)
	assert.Equal(t, old.ID, idleSessions[0].ID)
}

func TestSQLiteStore_ListByStatus(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	active := NewSession("C123", "1.1", "U456", "/tmp/1")
	active.Status = SessionStatusActive
	require.NoError(t, store.Save(ctx, active))

	processing := NewSession("C123", "1.2", "U456", "/tmp/2")
	processing.Status = SessionStatusProcessing
	require.NoError(t, store.Save(ctx, processing))

	statusSessions, err := store.ListByStatus(ctx, SessionStatusActive)
	require.NoError(t, err)
	assert.Len(t, statusSessions, 1)
	assert.Equal(t, SessionStatusActive, statusSessions[0].Status)
}

func TestSQLiteStore_Count(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Initial count
	count, err := store.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Add sessions
	require.NoError(t, store.Save(ctx, NewSession("C123", "1.1", "U456", "/tmp/1")))
	require.NoError(t, store.Save(ctx, NewSession("C123", "1.2", "U456", "/tmp/2")))

	count, err = store.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestSQLiteStore_CountByUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Save(ctx, NewSession("C123", "1.1", "U456", "/tmp/1")))
	require.NoError(t, store.Save(ctx, NewSession("C123", "1.2", "U456", "/tmp/2")))
	require.NoError(t, store.Save(ctx, NewSession("C123", "1.3", "U999", "/tmp/3")))

	count, err := store.CountByUser(ctx, "U456")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}
