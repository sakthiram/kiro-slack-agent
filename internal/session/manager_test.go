package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestManager(t *testing.T) (*Manager, string) {
	logger := zap.NewNop()

	store, err := NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)

	tmpDir := t.TempDir()

	cfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 10,
		MaxSessionsUser:  3,
		DatabasePath:     ":memory:",
	}

	manager := NewManager(store, cfg, tmpDir, logger)
	t.Cleanup(func() {
		manager.Stop()
		store.Close()
	})

	return manager, tmpDir
}

func TestManager_GetOrCreate_NewSession(t *testing.T) {
	manager, tmpDir := newTestManager(t)
	ctx := context.Background()

	session, created, err := manager.GetOrCreate(ctx, "C123", "1234567890.123456", "U456")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, SessionID("1234567890.123456"), session.ID)
	assert.Equal(t, "C123", session.ChannelID)
	assert.Equal(t, "U456", session.UserID)

	// Verify directory was created
	expectedDir := filepath.Join(tmpDir, "1234567890.123456")
	assert.DirExists(t, expectedDir)
}

func TestManager_GetOrCreate_ExistingSession(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	// Create first
	session1, created1, err := manager.GetOrCreate(ctx, "C123", "1234567890.123456", "U456")
	require.NoError(t, err)
	assert.True(t, created1)

	// Get existing
	session2, created2, err := manager.GetOrCreate(ctx, "C123", "1234567890.123456", "U456")
	require.NoError(t, err)
	assert.False(t, created2)
	assert.Equal(t, session1.ID, session2.ID)
}

func TestManager_GetOrCreate_UserLimitReached(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	// Create max sessions for user
	for i := 0; i < 3; i++ {
		_, _, err := manager.GetOrCreate(ctx, "C123", "1."+string(rune('0'+i)), "U456")
		require.NoError(t, err)
	}

	// Fourth session should fail
	_, _, err := manager.GetOrCreate(ctx, "C123", "1.99", "U456")
	assert.ErrorIs(t, err, ErrSessionLimitReached)
}

func TestManager_Close(t *testing.T) {
	manager, tmpDir := newTestManager(t)
	ctx := context.Background()

	// Create session
	session, _, err := manager.GetOrCreate(ctx, "C123", "1234567890.123456", "U456")
	require.NoError(t, err)

	sessionDir := filepath.Join(tmpDir, "1234567890.123456")
	assert.DirExists(t, sessionDir)

	// Close session
	err = manager.Close(ctx, session.ID)
	require.NoError(t, err)

	// Verify session removed
	_, err = manager.Get(ctx, session.ID)
	assert.ErrorIs(t, err, ErrSessionNotFound)

	// Verify directory removed
	assert.NoDirExists(t, sessionDir)
}

func TestManager_UpdateActivity(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	session, _, err := manager.GetOrCreate(ctx, "C123", "1234567890.123456", "U456")
	require.NoError(t, err)

	originalActivity := session.LastActivityAt
	time.Sleep(time.Millisecond)

	err = manager.UpdateActivity(ctx, session.ID)
	require.NoError(t, err)

	updated, err := manager.Get(ctx, session.ID)
	require.NoError(t, err)
	assert.True(t, updated.LastActivityAt.After(originalActivity))
}

func TestManager_UpdateStatus(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	session, _, err := manager.GetOrCreate(ctx, "C123", "1234567890.123456", "U456")
	require.NoError(t, err)
	assert.Equal(t, SessionStatusActive, session.Status)

	err = manager.UpdateStatus(ctx, session.ID, SessionStatusProcessing)
	require.NoError(t, err)

	updated, err := manager.Get(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, SessionStatusProcessing, updated.Status)
}

func TestManager_Cleanup(t *testing.T) {
	logger := zap.NewNop()

	store, err := NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	tmpDir := t.TempDir()

	cfg := &config.SessionConfig{
		IdleTimeout:      time.Millisecond, // Very short for testing
		MaxSessionsTotal: 10,
		MaxSessionsUser:  3,
	}

	manager := NewManager(store, cfg, tmpDir, logger)
	ctx := context.Background()

	// Create session
	session, _, err := manager.GetOrCreate(ctx, "C123", "1234567890.123456", "U456")
	require.NoError(t, err)

	// Wait for idle timeout
	time.Sleep(10 * time.Millisecond)

	// Run cleanup
	err = manager.Cleanup(ctx)
	require.NoError(t, err)

	// Session should be removed
	_, err = manager.Get(ctx, session.ID)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestManager_Cleanup_SkipsProcessing(t *testing.T) {
	logger := zap.NewNop()

	store, err := NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	tmpDir := t.TempDir()

	cfg := &config.SessionConfig{
		IdleTimeout:      time.Millisecond,
		MaxSessionsTotal: 10,
		MaxSessionsUser:  3,
	}

	manager := NewManager(store, cfg, tmpDir, logger)
	ctx := context.Background()

	// Create session and mark as processing
	session, _, err := manager.GetOrCreate(ctx, "C123", "1234567890.123456", "U456")
	require.NoError(t, err)

	err = manager.UpdateStatus(ctx, session.ID, SessionStatusProcessing)
	require.NoError(t, err)

	// Wait for idle timeout
	time.Sleep(10 * time.Millisecond)

	// Run cleanup
	err = manager.Cleanup(ctx)
	require.NoError(t, err)

	// Session should still exist (skipped because processing)
	_, err = manager.Get(ctx, session.ID)
	assert.NoError(t, err)
}

func TestManager_List(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	// Create multiple sessions
	_, _, _ = manager.GetOrCreate(ctx, "C123", "1.1", "U456")
	_, _, _ = manager.GetOrCreate(ctx, "C123", "1.2", "U789")

	sessions, err := manager.List(ctx)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
}

func TestManager_ListByUser(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	_, _, _ = manager.GetOrCreate(ctx, "C123", "1.1", "U456")
	_, _, _ = manager.GetOrCreate(ctx, "C123", "1.2", "U456")
	_, _, _ = manager.GetOrCreate(ctx, "C123", "1.3", "U789")

	sessions, err := manager.ListByUser(ctx, "U456")
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	for _, s := range sessions {
		assert.Equal(t, "U456", s.UserID)
	}
}

func TestManager_DirectoryCreation(t *testing.T) {
	manager, tmpDir := newTestManager(t)
	ctx := context.Background()

	threadTS := "1234567890.123456"
	expectedDir := filepath.Join(tmpDir, threadTS)

	// Before creation, directory shouldn't exist
	_, err := os.Stat(expectedDir)
	assert.True(t, os.IsNotExist(err))

	// Create session
	_, _, err = manager.GetOrCreate(ctx, "C123", threadTS, "U456")
	require.NoError(t, err)

	// Directory should now exist
	info, err := os.Stat(expectedDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestManager_Start_Stop(t *testing.T) {
	logger := zap.NewNop()

	store, err := NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	cfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 10,
		MaxSessionsUser:  3,
	}

	manager := NewManager(store, cfg, t.TempDir(), logger)

	// Start background cleanup
	manager.Start()

	// Small delay to ensure goroutine starts
	time.Sleep(10 * time.Millisecond)

	// Stop should not panic
	manager.Stop()
}

func TestManager_Close_NotFound(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	err := manager.Close(ctx, SessionID("nonexistent"))
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestManager_UpdateActivity_NotFound(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	err := manager.UpdateActivity(ctx, SessionID("nonexistent"))
	assert.Error(t, err)
}

func TestManager_UpdateStatus_NotFound(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()

	err := manager.UpdateStatus(ctx, SessionID("nonexistent"), SessionStatusProcessing)
	assert.Error(t, err)
}

func TestManager_GetOrCreate_TotalLimit(t *testing.T) {
	logger := zap.NewNop()

	store, err := NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	cfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 2, // Low total limit
		MaxSessionsUser:  10,
	}

	manager := NewManager(store, cfg, t.TempDir(), logger)
	ctx := context.Background()

	// Create max total sessions (different users)
	_, _, err = manager.GetOrCreate(ctx, "C123", "1.1", "U1")
	require.NoError(t, err)
	_, _, err = manager.GetOrCreate(ctx, "C123", "1.2", "U2")
	require.NoError(t, err)

	// Third session should fail due to total limit
	_, _, err = manager.GetOrCreate(ctx, "C123", "1.3", "U3")
	assert.ErrorIs(t, err, ErrSessionLimitReached)
}
