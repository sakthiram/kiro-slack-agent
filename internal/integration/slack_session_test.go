package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"github.com/sakthiram/kiro-slack-agent/internal/streaming"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSlackToSession_NewMessage tests creating a new session from a message.
func TestSlackToSession_NewMessage(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	// Setup real SQLite store with in-memory DB
	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	// Setup real session manager
	sessionCfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 100,
		MaxSessionsUser:  5,
	}
	sessionMgr := session.NewManager(store, sessionCfg, t.TempDir(), logger)

	// Mock Slack client
	mockSlack := NewMockSlackClient("UBOT123")

	// Simulate message event
	channelID := "C123456"
	threadTS := "1234567890.123456"
	userID := "U789"

	// Create session from message (this is what the handler would do)
	sess, isNew, err := sessionMgr.GetOrCreate(ctx, channelID, threadTS, userID)
	require.NoError(t, err)
	assert.True(t, isNew)
	assert.NotNil(t, sess)
	assert.Equal(t, channelID, sess.ChannelID)
	assert.Equal(t, threadTS, sess.ThreadTS)
	assert.Equal(t, userID, sess.UserID)
	assert.Equal(t, session.SessionStatusActive, sess.Status)

	// Verify session can be retrieved
	retrieved, err := sessionMgr.Get(ctx, session.SessionID(threadTS))
	require.NoError(t, err)
	assert.Equal(t, sess.ID, retrieved.ID)

	// Verify Slack client is ready
	assert.Equal(t, "UBOT123", mockSlack.GetBotUserID())
}

// TestSlackToSession_ThreadReply tests that thread replies use existing session.
func TestSlackToSession_ThreadReply(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	// Setup real SQLite store
	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	sessionCfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 100,
		MaxSessionsUser:  5,
	}
	sessionMgr := session.NewManager(store, sessionCfg, t.TempDir(), logger)

	channelID := "C123456"
	threadTS := "1234567890.123456"
	userID := "U789"

	// First message creates session
	sess1, isNew1, err := sessionMgr.GetOrCreate(ctx, channelID, threadTS, userID)
	require.NoError(t, err)
	assert.True(t, isNew1)

	// Second message (thread reply) uses existing session
	sess2, isNew2, err := sessionMgr.GetOrCreate(ctx, channelID, threadTS, userID)
	require.NoError(t, err)
	assert.False(t, isNew2, "Thread reply should use existing session")
	assert.Equal(t, sess1.ID, sess2.ID)
}

// TestSlackToSession_SessionLimit tests user session limit enforcement.
func TestSlackToSession_SessionLimit(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	// Setup real SQLite store
	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	// Low limit for testing
	sessionCfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 100,
		MaxSessionsUser:  2, // Only allow 2 sessions per user
	}
	sessionMgr := session.NewManager(store, sessionCfg, t.TempDir(), logger)

	userID := "U789"

	// Create max sessions
	_, _, err = sessionMgr.GetOrCreate(ctx, "C1", "ts1", userID)
	require.NoError(t, err)

	_, _, err = sessionMgr.GetOrCreate(ctx, "C2", "ts2", userID)
	require.NoError(t, err)

	// Third session should fail
	_, _, err = sessionMgr.GetOrCreate(ctx, "C3", "ts3", userID)
	assert.ErrorIs(t, err, session.ErrSessionLimitReached)

	// Different user should still be able to create sessions
	_, isNew, err := sessionMgr.GetOrCreate(ctx, "C4", "ts4", "U_OTHER")
	require.NoError(t, err)
	assert.True(t, isNew)
}

// TestSlackToSession_TotalLimit tests total session limit enforcement.
func TestSlackToSession_TotalLimit(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	// Low total limit
	sessionCfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 3, // Only allow 3 total sessions
		MaxSessionsUser:  10,
	}
	sessionMgr := session.NewManager(store, sessionCfg, t.TempDir(), logger)

	// Create max total sessions (different users)
	_, _, err = sessionMgr.GetOrCreate(ctx, "C1", "ts1", "U1")
	require.NoError(t, err)

	_, _, err = sessionMgr.GetOrCreate(ctx, "C2", "ts2", "U2")
	require.NoError(t, err)

	_, _, err = sessionMgr.GetOrCreate(ctx, "C3", "ts3", "U3")
	require.NoError(t, err)

	// Fourth session should fail regardless of user
	_, _, err = sessionMgr.GetOrCreate(ctx, "C4", "ts4", "U4")
	assert.ErrorIs(t, err, session.ErrSessionLimitReached)
}

// TestSlackToSession_StreamerIntegration tests streamer with mock Slack client.
func TestSlackToSession_StreamerIntegration(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	mockSlack := NewMockSlackClient("UBOT123")
	streamCfg := &config.StreamingConfig{
		UpdateInterval: 50 * time.Millisecond,
	}

	streamer := streaming.NewStreamer(mockSlack, streamCfg, logger)

	// Start streamer
	ts, err := streamer.Start(ctx, "C123", "thread123")
	require.NoError(t, err)
	assert.NotEmpty(t, ts)
	assert.True(t, streamer.IsStarted())

	// Verify initial message was posted
	require.Len(t, mockSlack.PostedMsgs, 1)
	assert.Contains(t, mockSlack.PostedMsgs[0].Text, "Thinking")

	// Complete streamer
	err = streamer.Complete(ctx, "Final response")
	require.NoError(t, err)
	assert.True(t, streamer.IsCompleted())

	// Verify final update
	require.NotEmpty(t, mockSlack.UpdatedMsgs)
	lastUpdate := mockSlack.UpdatedMsgs[len(mockSlack.UpdatedMsgs)-1]
	assert.Equal(t, "Final response", lastUpdate.Text)
}

// TestSlackToSession_StreamerError tests error handling in streamer.
func TestSlackToSession_StreamerError(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	mockSlack := NewMockSlackClient("UBOT123")
	streamCfg := &config.StreamingConfig{
		UpdateInterval: 50 * time.Millisecond,
	}

	streamer := streaming.NewStreamer(mockSlack, streamCfg, logger)

	// Start streamer
	_, err := streamer.Start(ctx, "C123", "thread123")
	require.NoError(t, err)

	// Send error
	testErr := assert.AnError
	err = streamer.Error(ctx, testErr)
	require.NoError(t, err)
	assert.True(t, streamer.IsCompleted())

	// Verify error message was posted
	require.NotEmpty(t, mockSlack.UpdatedMsgs)
	lastUpdate := mockSlack.UpdatedMsgs[len(mockSlack.UpdatedMsgs)-1]
	assert.Contains(t, lastUpdate.Text, "Error")
}

// TestSlackToSession_SessionStatusTransitions tests status changes during processing.
func TestSlackToSession_SessionStatusTransitions(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	sessionCfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 100,
		MaxSessionsUser:  5,
	}
	sessionMgr := session.NewManager(store, sessionCfg, t.TempDir(), logger)

	// Create session
	sess, _, err := sessionMgr.GetOrCreate(ctx, "C1", "ts1", "U1")
	require.NoError(t, err)
	assert.Equal(t, session.SessionStatusActive, sess.Status)

	// Simulate processing
	err = sessionMgr.UpdateStatus(ctx, sess.ID, session.SessionStatusProcessing)
	require.NoError(t, err)

	updated, err := sessionMgr.Get(ctx, sess.ID)
	require.NoError(t, err)
	assert.Equal(t, session.SessionStatusProcessing, updated.Status)

	// Back to active
	err = sessionMgr.UpdateStatus(ctx, sess.ID, session.SessionStatusActive)
	require.NoError(t, err)

	updated, err = sessionMgr.Get(ctx, sess.ID)
	require.NoError(t, err)
	assert.Equal(t, session.SessionStatusActive, updated.Status)
}

// TestSlackToSession_SessionDirectory tests Kiro session directory creation.
func TestSlackToSession_SessionDirectory(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	basePath := t.TempDir()
	sessionCfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 100,
		MaxSessionsUser:  5,
	}
	sessionMgr := session.NewManager(store, sessionCfg, basePath, logger)

	// Create session
	sess, _, err := sessionMgr.GetOrCreate(ctx, "C1", "ts_unique", "U1")
	require.NoError(t, err)

	// Verify directory was created
	assert.Contains(t, sess.KiroSessionDir, "ts_unique")
	assert.DirExists(t, sess.KiroSessionDir)
}

// TestSlackToSession_ConcurrentAccess tests concurrent session operations.
func TestSlackToSession_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	sessionCfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 100,
		MaxSessionsUser:  50,
	}
	sessionMgr := session.NewManager(store, sessionCfg, t.TempDir(), logger)

	threadTS := "concurrent_ts"
	var wg sync.WaitGroup
	results := make(chan bool, 10)

	// Multiple goroutines try to get/create the same session
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, isNew, err := sessionMgr.GetOrCreate(ctx, "C1", threadTS, "U1")
			if err != nil {
				results <- false
				return
			}
			results <- isNew
		}()
	}

	wg.Wait()
	close(results)

	// Exactly one should create, rest should get existing
	newCount := 0
	for isNew := range results {
		if isNew {
			newCount++
		}
	}

	assert.Equal(t, 1, newCount, "Exactly one goroutine should create the session")
}
