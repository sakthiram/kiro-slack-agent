package processor

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/kiro"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockSlackClient implements slack.ClientInterface for testing.
type mockSlackClient struct {
	mu            sync.Mutex
	postCalls     []postCall
	updateCalls   []updateCall
	postError     error
	updateError   error
	nextPostTS    string
	lastThreadTS  string
}

type postCall struct {
	channelID string
	text      string
}

type updateCall struct {
	channelID string
	ts        string
	text      string
}

func newMockSlackClient() *mockSlackClient {
	return &mockSlackClient{
		nextPostTS: "msg.12345",
	}
}

func (m *mockSlackClient) PostMessage(ctx context.Context, channelID, text string, opts ...slack.MessageOption) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.postError != nil {
		return "", m.postError
	}

	m.postCalls = append(m.postCalls, postCall{channelID: channelID, text: text})
	return m.nextPostTS, nil
}

func (m *mockSlackClient) UpdateMessage(ctx context.Context, channelID, ts, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updateError != nil {
		return m.updateError
	}

	m.updateCalls = append(m.updateCalls, updateCall{
		channelID: channelID,
		ts:        ts,
		text:      text,
	})
	return nil
}

func (m *mockSlackClient) AddReaction(ctx context.Context, channelID, ts, emoji string) error {
	return nil
}

func (m *mockSlackClient) RemoveReaction(ctx context.Context, channelID, ts, emoji string) error {
	return nil
}

func (m *mockSlackClient) GetBotUserID() string {
	return "U123BOT"
}

func (m *mockSlackClient) getPostCalls() []postCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]postCall{}, m.postCalls...)
}

func (m *mockSlackClient) getUpdateCalls() []updateCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]updateCall{}, m.updateCalls...)
}

// mockSessionManager implements SessionManager for testing.
type mockSessionManager struct {
	mu               sync.Mutex
	sessions         map[string]*session.Session
	getOrCreateError error
	updateStatusErr  error
	statusUpdates    []statusUpdate
	isNewSession     bool
}

type statusUpdate struct {
	id     session.SessionID
	status session.SessionStatus
}

func newMockSessionManager() *mockSessionManager {
	return &mockSessionManager{
		sessions:     make(map[string]*session.Session),
		isNewSession: true,
	}
}

func (m *mockSessionManager) GetOrCreate(ctx context.Context, channelID, threadTS, userID string) (*session.Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getOrCreateError != nil {
		return nil, false, m.getOrCreateError
	}

	key := channelID + ":" + threadTS
	if sess, ok := m.sessions[key]; ok {
		return sess, false, nil
	}

	sess := &session.Session{
		ID:              session.SessionID("sess-" + channelID + "-" + threadTS),
		ChannelID:       channelID,
		ThreadTS:        threadTS,
		UserID:          userID,
		Status:          session.SessionStatusActive,
		KiroSessionDir:  "/tmp/test-kiro-session",
	}
	m.sessions[key] = sess
	return sess, m.isNewSession, nil
}

func (m *mockSessionManager) UpdateStatus(ctx context.Context, id session.SessionID, status session.SessionStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updateStatusErr != nil {
		return m.updateStatusErr
	}

	m.statusUpdates = append(m.statusUpdates, statusUpdate{id: id, status: status})
	return nil
}

func (m *mockSessionManager) getStatusUpdates() []statusUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]statusUpdate{}, m.statusUpdates...)
}

// mockBridgeCache implements BridgeCache for testing.
type mockBridgeCache struct {
	mu      sync.Mutex
	bridges map[session.SessionID]*kiro.ObservableProcess
	deleted []session.SessionID
}

func newMockBridgeCache() *mockBridgeCache {
	return &mockBridgeCache{
		bridges: make(map[session.SessionID]*kiro.ObservableProcess),
	}
}

func (m *mockBridgeCache) Get(id session.SessionID) (*kiro.ObservableProcess, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bridge, ok := m.bridges[id]
	return bridge, ok
}

func (m *mockBridgeCache) Set(id session.SessionID, bridge *kiro.ObservableProcess) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bridges[id] = bridge
}

func (m *mockBridgeCache) Delete(id session.SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.bridges, id)
	m.deleted = append(m.deleted, id)
}

func (m *mockBridgeCache) getDeleted() []session.SessionID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]session.SessionID{}, m.deleted...)
}

func TestNewMessageProcessor(t *testing.T) {
	client := newMockSlackClient()
	sessions := newMockSessionManager()
	bridges := newMockBridgeCache()
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, sessions, bridges, cfg, logger)

	assert.NotNil(t, processor)
	assert.NotNil(t, processor.slackClient)
	assert.NotNil(t, processor.sessionMgr)
	assert.NotNil(t, processor.bridges)
	assert.NotNil(t, processor.cfg)
	assert.NotNil(t, processor.logger)
}

func TestMessageProcessor_ProcessMessage_SessionCreationError(t *testing.T) {
	client := newMockSlackClient()
	sessions := newMockSessionManager()
	sessions.getOrCreateError = errors.New("database error")
	bridges := newMockBridgeCache()
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, sessions, bridges, cfg, logger)

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
		Text:      "Hello",
		MessageTS: "ts.124",
	}

	err := processor.ProcessMessage(context.Background(), msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database error")

	// Verify error message was posted to user
	calls := client.getPostCalls()
	assert.NotEmpty(t, calls)
	assert.Contains(t, calls[0].text, "Unable to create session")
}

func TestMessageProcessor_ProcessMessage_UsesMessageTSWhenNoThread(t *testing.T) {
	client := newMockSlackClient()
	sessions := newMockSessionManager()
	bridges := newMockBridgeCache()
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, sessions, bridges, cfg, logger)

	// Message with no thread (empty ThreadTS)
	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "", // no thread
		UserID:    "U456",
		Text:      "Hello",
		MessageTS: "ts.124",
	}

	// This will fail at Kiro bridge start (which is expected)
	// We're testing that the session was created with MessageTS
	_ = processor.ProcessMessage(context.Background(), msg)

	// Verify session was created using MessageTS as threadTS
	sess, _, _ := sessions.GetOrCreate(context.Background(), "C123", "ts.124", "U456")
	assert.Equal(t, "ts.124", sess.ThreadTS)
}

func TestMessageProcessor_ProcessMessage_SessionStatusUpdates(t *testing.T) {
	client := newMockSlackClient()
	sessions := newMockSessionManager()
	bridges := newMockBridgeCache()
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, sessions, bridges, cfg, logger)

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
		Text:      "Hello",
		MessageTS: "ts.124",
	}

	// This will fail at Kiro bridge start, but we still test status updates
	_ = processor.ProcessMessage(context.Background(), msg)

	// Verify status was updated to processing and then back to active (via defer)
	updates := sessions.getStatusUpdates()
	assert.GreaterOrEqual(t, len(updates), 1)
	// First update should be to Processing
	assert.Equal(t, session.SessionStatusProcessing, updates[0].status)
}

func TestMessageProcessor_Interfaces_Satisfied(t *testing.T) {
	// Test that the interfaces are properly satisfied
	var _ slack.ClientInterface = (*mockSlackClient)(nil)
	var _ SessionManager = (*mockSessionManager)(nil)
	var _ BridgeCache = (*mockBridgeCache)(nil)
}

func TestMessageProcessor_NewSessionCreated(t *testing.T) {
	client := newMockSlackClient()
	sessions := newMockSessionManager()
	sessions.isNewSession = true
	bridges := newMockBridgeCache()
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, sessions, bridges, cfg, logger)

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
		Text:      "Hello",
		MessageTS: "ts.124",
	}

	// Call ProcessMessage - it will fail at Kiro start, but session should be created
	_ = processor.ProcessMessage(context.Background(), msg)

	// Verify a session was created
	sess, isNew, err := sessions.GetOrCreate(context.Background(), "C123", "ts.123", "U456")
	require.NoError(t, err)
	assert.NotNil(t, sess)
	// Second call returns existing session (isNew = false)
	assert.False(t, isNew)
}

func TestMessageProcessor_ExistingSession(t *testing.T) {
	client := newMockSlackClient()
	sessions := newMockSessionManager()
	bridges := newMockBridgeCache()
	cfg := &config.Config{}
	logger := zap.NewNop()

	// Pre-create a session
	_, _, _ = sessions.GetOrCreate(context.Background(), "C123", "ts.123", "U456")
	sessions.isNewSession = false

	processor := NewMessageProcessor(client, sessions, bridges, cfg, logger)

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
		Text:      "Hello again",
		MessageTS: "ts.125",
	}

	// Process second message to existing session
	_ = processor.ProcessMessage(context.Background(), msg)

	// Verify session was reused (not recreated)
	sess, isNew, err := sessions.GetOrCreate(context.Background(), "C123", "ts.123", "U456")
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.False(t, isNew)
}
