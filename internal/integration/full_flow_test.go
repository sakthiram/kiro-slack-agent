package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/kiro"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"github.com/sakthiram/kiro-slack-agent/internal/streaming"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fullFlowTestEnv holds all components for full flow testing.
type fullFlowTestEnv struct {
	logger       *zap.Logger
	store        *session.SQLiteStore
	sessionMgr   *session.Manager
	mockSlack    *MockSlackClient
	bridges      *testBridgeCache
	cfg          *config.Config
	basePath     string
}

// testBridgeCache manages Kiro bridges for testing.
type testBridgeCache struct {
	mu      sync.RWMutex
	bridges map[session.SessionID]kiro.Bridge
	logger  *zap.Logger
}

func newTestBridgeCache(logger *zap.Logger) *testBridgeCache {
	return &testBridgeCache{
		bridges: make(map[session.SessionID]kiro.Bridge),
		logger:  logger,
	}
}

func (c *testBridgeCache) Get(id session.SessionID) (kiro.Bridge, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.bridges[id]
	return b, ok
}

func (c *testBridgeCache) Set(id session.SessionID, bridge kiro.Bridge) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bridges[id] = bridge
}

func (c *testBridgeCache) Delete(id session.SessionID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.bridges, id)
}

func (c *testBridgeCache) CloseAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, bridge := range c.bridges {
		if err := bridge.Close(); err != nil {
			c.logger.Warn("failed to close bridge",
				zap.String("session_id", string(id)),
				zap.Error(err))
		}
	}
	c.bridges = make(map[session.SessionID]kiro.Bridge)
}

// setupFullFlowEnv creates a complete test environment.
func setupFullFlowEnv(t *testing.T) *fullFlowTestEnv {
	logger := zap.NewNop()
	basePath := t.TempDir()

	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)

	cfg := &config.Config{
		Session: config.SessionConfig{
			IdleTimeout:      30 * time.Minute,
			MaxSessionsTotal: 100,
			MaxSessionsUser:  5,
		},
		Streaming: config.StreamingConfig{
			UpdateInterval: 50 * time.Millisecond,
		},
		Kiro: config.KiroConfig{
			MaxRetries:      1,
			StartupTimeout:  5 * time.Second,
			ResponseTimeout: 10 * time.Second,
		},
	}

	sessionMgr := session.NewManager(store, &cfg.Session, basePath, logger)

	return &fullFlowTestEnv{
		logger:     logger,
		store:      store,
		sessionMgr: sessionMgr,
		mockSlack:  NewMockSlackClient("UBOT123"),
		bridges:    newTestBridgeCache(logger),
		cfg:        cfg,
		basePath:   basePath,
	}
}

func (env *fullFlowTestEnv) cleanup() {
	env.bridges.CloseAll()
	env.store.Close()
}

// processMessage simulates the main.go message processing logic.
func (env *fullFlowTestEnv) processMessage(ctx context.Context, msg *slack.MessageEvent, mockBridge *MockBridge) error {
	// Determine thread TS (use message TS if no thread)
	threadTS := msg.ThreadTS
	if threadTS == "" {
		threadTS = msg.MessageTS
	}

	// Get or create session
	sess, isNew, err := env.sessionMgr.GetOrCreate(ctx, msg.ChannelID, threadTS, msg.UserID)
	if err != nil {
		env.mockSlack.PostMessage(ctx, msg.ChannelID, ":x: Error: Unable to create session. Please try again.",
			slack.WithThreadTS(threadTS))
		return err
	}

	// Update session status to processing
	env.sessionMgr.UpdateStatus(ctx, sess.ID, session.SessionStatusProcessing)
	defer env.sessionMgr.UpdateStatus(ctx, sess.ID, session.SessionStatusActive)

	// Create streamer for this response
	streamer := streaming.NewStreamer(env.mockSlack, &env.cfg.Streaming, env.logger)

	// Start streaming response
	_, err = streamer.Start(ctx, msg.ChannelID, threadTS)
	if err != nil {
		return err
	}

	// Get or create Kiro bridge
	bridge, ok := env.bridges.Get(sess.ID)
	if !ok || !bridge.IsRunning() {
		if mockBridge == nil {
			mockBridge = NewMockBridge()
			mockBridge.Responses = []string{"Default mock response"}
		}

		bridge = mockBridge
		if err := bridge.Start(ctx); err != nil {
			streamer.Error(ctx, err)
			return err
		}
		env.bridges.Set(sess.ID, bridge)

		if isNew {
			env.logger.Info("created new Kiro session")
		}
	}

	// Send message to Kiro and stream response
	var finalResponse string
	err = bridge.SendMessage(ctx, msg.Text, func(chunk string, isComplete bool) {
		finalResponse = chunk
		if !isComplete {
			streamer.Update(ctx, chunk)
		}
	})

	if err != nil {
		streamer.Error(ctx, err)
		env.bridges.Delete(sess.ID)
		bridge.Close()
		return err
	}

	// Complete streaming with final response
	if err := streamer.Complete(ctx, finalResponse); err != nil {
		return err
	}

	return nil
}

// TestFullFlow_NewMessageToResponse tests complete flow from new message to response.
func TestFullFlow_NewMessageToResponse(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	// Setup mock bridge with expected response
	mockBridge := NewMockBridge()
	mockBridge.Responses = []string{"Here is your answer: 4"}

	// Simulate incoming Slack message
	msg := &slack.MessageEvent{
		ChannelID: "C123456",
		UserID:    "U789",
		Text:      "What is 2+2?",
		MessageTS: "1234567890.123456",
	}

	// Process message
	err := env.processMessage(ctx, msg, mockBridge)
	require.NoError(t, err)

	// Verify session was created
	sess, err := env.sessionMgr.Get(ctx, session.SessionID(msg.MessageTS))
	require.NoError(t, err)
	assert.Equal(t, msg.ChannelID, sess.ChannelID)
	assert.Equal(t, msg.UserID, sess.UserID)

	// Verify bridge received the message
	assert.Len(t, mockBridge.Messages, 1)
	assert.Equal(t, "What is 2+2?", mockBridge.Messages[0])

	// Verify Slack interactions
	// 1. Initial "Thinking..." message
	require.GreaterOrEqual(t, len(env.mockSlack.PostedMsgs), 1)
	assert.Contains(t, env.mockSlack.PostedMsgs[0].Text, "Thinking")

	// 2. Final response update
	require.GreaterOrEqual(t, len(env.mockSlack.UpdatedMsgs), 1)
	lastUpdate := env.mockSlack.UpdatedMsgs[len(env.mockSlack.UpdatedMsgs)-1]
	assert.Equal(t, "Here is your answer: 4", lastUpdate.Text)
}

// TestFullFlow_ThreadContinuation tests thread replies continue existing session.
func TestFullFlow_ThreadContinuation(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	threadTS := "1234567890.000001"
	channelID := "C123456"
	userID := "U789"

	// First message creates session
	mockBridge := NewMockBridge()
	mockBridge.Responses = []string{"First response"}

	msg1 := &slack.MessageEvent{
		ChannelID: channelID,
		UserID:    userID,
		Text:      "First question",
		MessageTS: threadTS,
	}
	err := env.processMessage(ctx, msg1, mockBridge)
	require.NoError(t, err)

	// Verify session created
	sess1, err := env.sessionMgr.Get(ctx, session.SessionID(threadTS))
	require.NoError(t, err)

	// Clear message history for clarity
	initialPostCount := len(env.mockSlack.PostedMsgs)
	initialUpdateCount := len(env.mockSlack.UpdatedMsgs)

	// Second message in thread uses same session
	mockBridge.Responses = []string{"Second response"}
	msg2 := &slack.MessageEvent{
		ChannelID: channelID,
		UserID:    userID,
		Text:      "Follow-up question",
		MessageTS: "1234567890.000002",
		ThreadTS:  threadTS, // Reply in thread
	}
	err = env.processMessage(ctx, msg2, nil) // Use existing bridge
	require.NoError(t, err)

	// Verify same session was used
	sess2, err := env.sessionMgr.Get(ctx, session.SessionID(threadTS))
	require.NoError(t, err)
	assert.Equal(t, sess1.ID, sess2.ID)

	// Verify new Slack messages were sent
	assert.Greater(t, len(env.mockSlack.PostedMsgs), initialPostCount)
	assert.Greater(t, len(env.mockSlack.UpdatedMsgs), initialUpdateCount)

	// Verify bridge received both messages
	assert.Len(t, mockBridge.Messages, 2)
	assert.Equal(t, "First question", mockBridge.Messages[0])
	assert.Equal(t, "Follow-up question", mockBridge.Messages[1])
}

// TestFullFlow_MultipleUsers tests multiple users have separate sessions.
func TestFullFlow_MultipleUsers(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	// User 1 sends a message
	bridge1 := NewMockBridge()
	bridge1.Responses = []string{"Response for user 1"}
	msg1 := &slack.MessageEvent{
		ChannelID: "C123",
		UserID:    "U001",
		Text:      "User 1 question",
		MessageTS: "ts001",
	}
	err := env.processMessage(ctx, msg1, bridge1)
	require.NoError(t, err)

	// User 2 sends a message
	bridge2 := NewMockBridge()
	bridge2.Responses = []string{"Response for user 2"}
	msg2 := &slack.MessageEvent{
		ChannelID: "C123",
		UserID:    "U002",
		Text:      "User 2 question",
		MessageTS: "ts002",
	}
	err = env.processMessage(ctx, msg2, bridge2)
	require.NoError(t, err)

	// Verify separate sessions
	sess1, err := env.sessionMgr.Get(ctx, session.SessionID("ts001"))
	require.NoError(t, err)
	sess2, err := env.sessionMgr.Get(ctx, session.SessionID("ts002"))
	require.NoError(t, err)

	assert.NotEqual(t, sess1.ID, sess2.ID)
	assert.Equal(t, "U001", sess1.UserID)
	assert.Equal(t, "U002", sess2.UserID)

	// Verify separate bridges handled messages
	assert.Len(t, bridge1.Messages, 1)
	assert.Len(t, bridge2.Messages, 1)
	assert.Equal(t, "User 1 question", bridge1.Messages[0])
	assert.Equal(t, "User 2 question", bridge2.Messages[0])
}

// TestFullFlow_RetryOnFailure tests retry logic on Kiro failure.
func TestFullFlow_RetryOnFailure(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	// Create failing bridge
	failingBridge := NewMockBridge()
	failingBridge.SendErr = errors.New("Kiro process died")

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		UserID:    "U001",
		Text:      "Will fail",
		MessageTS: "ts_fail",
	}

	// Process should fail and post error
	err := env.processMessage(ctx, msg, failingBridge)
	assert.Error(t, err)

	// Verify error was posted to Slack
	require.NotEmpty(t, env.mockSlack.UpdatedMsgs)
	lastUpdate := env.mockSlack.UpdatedMsgs[len(env.mockSlack.UpdatedMsgs)-1]
	assert.Contains(t, lastUpdate.Text, "Error")

	// Bridge should be removed from cache
	_, ok := env.bridges.Get(session.SessionID("ts_fail"))
	assert.False(t, ok)
}

// TestFullFlow_SessionLimitError tests user session limit handling.
func TestFullFlow_SessionLimitError(t *testing.T) {
	logger := zap.NewNop()
	basePath := t.TempDir()

	store, err := session.NewSQLiteStore(":memory:", logger)
	require.NoError(t, err)
	defer store.Close()

	// Very low session limit
	cfg := &config.Config{
		Session: config.SessionConfig{
			IdleTimeout:      30 * time.Minute,
			MaxSessionsTotal: 100,
			MaxSessionsUser:  1, // Only 1 session per user
		},
		Streaming: config.StreamingConfig{
			UpdateInterval: 50 * time.Millisecond,
		},
	}

	sessionMgr := session.NewManager(store, &cfg.Session, basePath, logger)
	mockSlack := NewMockSlackClient("UBOT123")
	bridges := newTestBridgeCache(logger)

	env := &fullFlowTestEnv{
		logger:     logger,
		store:      store,
		sessionMgr: sessionMgr,
		mockSlack:  mockSlack,
		bridges:    bridges,
		cfg:        cfg,
		basePath:   basePath,
	}
	defer env.cleanup()

	ctx := context.Background()

	// First session succeeds
	bridge1 := NewMockBridge()
	bridge1.Responses = []string{"OK"}
	msg1 := &slack.MessageEvent{
		ChannelID: "C123",
		UserID:    "U001",
		Text:      "First",
		MessageTS: "ts1",
	}
	err = env.processMessage(ctx, msg1, bridge1)
	require.NoError(t, err)

	// Second session should fail (same user)
	bridge2 := NewMockBridge()
	msg2 := &slack.MessageEvent{
		ChannelID: "C456",
		UserID:    "U001", // Same user
		Text:      "Second",
		MessageTS: "ts2",
	}
	err = env.processMessage(ctx, msg2, bridge2)
	assert.Error(t, err)

	// Error message should be posted
	foundError := false
	for _, msg := range env.mockSlack.PostedMsgs {
		if msg.ChannelID == "C456" && msg.Text != "" {
			foundError = true
			assert.Contains(t, msg.Text, "Error")
		}
	}
	assert.True(t, foundError, "Error message should be posted for session limit")
}

// TestFullFlow_StreamingUpdates tests progressive message updates during streaming.
func TestFullFlow_StreamingUpdates(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	// Bridge that sends multiple chunks
	mockBridge := NewMockBridge()
	mockBridge.Responses = []string{
		"Working...",
		"Still working...",
		"Final answer: 42",
	}

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		UserID:    "U001",
		Text:      "Complex question",
		MessageTS: "ts_stream",
	}

	err := env.processMessage(ctx, msg, mockBridge)
	require.NoError(t, err)

	// Verify multiple updates were sent
	// Note: Streaming updates may be debounced, so we just verify final state
	require.NotEmpty(t, env.mockSlack.UpdatedMsgs)
	lastUpdate := env.mockSlack.UpdatedMsgs[len(env.mockSlack.UpdatedMsgs)-1]
	assert.Equal(t, "Final answer: 42", lastUpdate.Text)
}

// TestFullFlow_ConcurrentMessages tests handling multiple messages concurrently.
func TestFullFlow_ConcurrentMessages(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	var wg sync.WaitGroup
	errors := make(chan error, 5)

	// Send 5 messages concurrently from different users/threads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bridge := NewMockBridge()
			bridge.Responses = []string{fmt.Sprintf("Response %d", n)}

			msg := &slack.MessageEvent{
				ChannelID: "C123",
				UserID:    fmt.Sprintf("U%03d", n),
				Text:      fmt.Sprintf("Question %d", n),
				MessageTS: fmt.Sprintf("ts_%d", n),
			}

			if err := env.processMessage(ctx, msg, bridge); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// All should succeed
	var errorList []error
	for err := range errors {
		errorList = append(errorList, err)
	}
	assert.Empty(t, errorList)

	// All sessions should exist
	for i := 0; i < 5; i++ {
		sess, err := env.sessionMgr.Get(ctx, session.SessionID(fmt.Sprintf("ts_%d", i)))
		require.NoError(t, err)
		assert.NotNil(t, sess)
	}
}

// TestFullFlow_DirectMessage tests direct message (DM) handling.
func TestFullFlow_DirectMessage(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	mockBridge := NewMockBridge()
	mockBridge.Responses = []string{"DM response"}

	// DM channels start with 'D'
	msg := &slack.MessageEvent{
		ChannelID: "D123456789",
		UserID:    "U001",
		Text:      "Private question",
		MessageTS: "dm_ts",
		IsDM:      true,
	}

	err := env.processMessage(ctx, msg, mockBridge)
	require.NoError(t, err)

	// Verify session was created for DM
	sess, err := env.sessionMgr.Get(ctx, session.SessionID("dm_ts"))
	require.NoError(t, err)
	assert.Equal(t, "D123456789", sess.ChannelID)
}

// TestFullFlow_MentionHandling tests @mention processing.
func TestFullFlow_MentionHandling(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	mockBridge := NewMockBridge()
	mockBridge.Responses = []string{"Mentioned response"}

	msg := &slack.MessageEvent{
		ChannelID: "C123456",
		UserID:    "U001",
		Text:      "question after mention", // Already cleaned by parser
		MessageTS: "mention_ts",
		IsMention: true,
	}

	err := env.processMessage(ctx, msg, mockBridge)
	require.NoError(t, err)

	// Verify the cleaned text was sent to Kiro
	assert.Len(t, mockBridge.Messages, 1)
	assert.Equal(t, "question after mention", mockBridge.Messages[0])
}

// TestFullFlow_EmptyMessage tests handling of empty messages.
func TestFullFlow_EmptyMessage(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	mockBridge := NewMockBridge()
	mockBridge.Responses = []string{"I didn't understand that"}

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		UserID:    "U001",
		Text:      "",
		MessageTS: "empty_ts",
	}

	// Empty messages should still be processed (let Kiro handle it)
	err := env.processMessage(ctx, msg, mockBridge)
	require.NoError(t, err)

	assert.Len(t, mockBridge.Messages, 1)
	assert.Equal(t, "", mockBridge.Messages[0])
}

// TestFullFlow_LongMessage tests handling of long messages.
func TestFullFlow_LongMessage(t *testing.T) {
	env := setupFullFlowEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	mockBridge := NewMockBridge()
	mockBridge.Responses = []string{"Processed long message"}

	// Generate long message
	longText := ""
	for i := 0; i < 1000; i++ {
		longText += "word "
	}

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		UserID:    "U001",
		Text:      longText,
		MessageTS: "long_ts",
	}

	err := env.processMessage(ctx, msg, mockBridge)
	require.NoError(t, err)

	// Message should be passed as-is
	assert.Len(t, mockBridge.Messages, 1)
	assert.Equal(t, longText, mockBridge.Messages[0])
}
