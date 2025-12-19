package processor

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
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

// mockBeadsManager implements BeadsManager for testing.
type mockBeadsManager struct {
	mu                  sync.Mutex
	userDirs            map[string]string
	issues              map[string]*beads.Issue
	comments            map[string][]string
	ensureUserDirError  error
	findIssueError      error
	createIssueError    error
	updateIssueError    error
	getContextError     error
	contextMessages     []beads.Message
}

func newMockBeadsManager() *mockBeadsManager {
	return &mockBeadsManager{
		userDirs: make(map[string]string),
		issues:   make(map[string]*beads.Issue),
		comments: make(map[string][]string),
	}
}

func (m *mockBeadsManager) EnsureUserDir(ctx context.Context, userID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ensureUserDirError != nil {
		return "", m.ensureUserDirError
	}

	dir := "/tmp/test-sessions/" + userID
	m.userDirs[userID] = dir
	return dir, nil
}

func (m *mockBeadsManager) GetUserDir(userID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return "/tmp/test-sessions/" + userID
}

func (m *mockBeadsManager) FindThreadIssue(ctx context.Context, userID string, thread *beads.ThreadInfo) (*beads.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.findIssueError != nil {
		return nil, m.findIssueError
	}

	key := userID + ":" + thread.ThreadTS
	if issue, ok := m.issues[key]; ok {
		return issue, nil
	}
	return nil, nil
}

func (m *mockBeadsManager) CreateThreadIssue(ctx context.Context, userID string, thread *beads.ThreadInfo, message string) (*beads.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.createIssueError != nil {
		return nil, m.createIssueError
	}

	key := userID + ":" + thread.ThreadTS
	issue := &beads.Issue{
		ID:          "issue-" + thread.ThreadTS,
		Title:       message,
		Description: message,
		Labels:      thread.Labels(),
	}
	m.issues[key] = issue
	return issue, nil
}

func (m *mockBeadsManager) UpdateThreadIssue(ctx context.Context, userID, issueID, role, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updateIssueError != nil {
		return m.updateIssueError
	}

	key := userID + ":" + issueID
	m.comments[key] = append(m.comments[key], "["+role+"] "+message)
	return nil
}

func (m *mockBeadsManager) GetConversationContext(ctx context.Context, userID, issueID string) ([]beads.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getContextError != nil {
		return nil, m.getContextError
	}

	return m.contextMessages, nil
}

func (m *mockBeadsManager) CreateFeature(ctx context.Context, userID string, thread *beads.ThreadInfo, title, description string) (*beads.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.createIssueError != nil {
		return nil, m.createIssueError
	}

	key := userID + ":" + thread.ThreadTS
	issue := &beads.Issue{
		ID:          "feature-" + thread.ThreadTS,
		Title:       title,
		Description: description,
		Type:        "feature",
		Labels:      thread.Labels(),
	}
	m.issues[key] = issue
	return issue, nil
}

func (m *mockBeadsManager) CreateTask(ctx context.Context, userID, parentID string, thread *beads.ThreadInfo, title string) (*beads.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.createIssueError != nil {
		return nil, m.createIssueError
	}

	key := userID + ":" + thread.ThreadTS
	issue := &beads.Issue{
		ID:       "task-" + thread.ThreadTS,
		Title:    title,
		Type:     "task",
		ParentID: parentID,
		Labels:   thread.Labels(),
	}
	m.issues[key] = issue
	return issue, nil
}

func TestNewMessageProcessor(t *testing.T) {
	client := newMockSlackClient()
	beadsMgr := newMockBeadsManager()
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, beadsMgr, cfg, logger)

	assert.NotNil(t, processor)
	assert.NotNil(t, processor.slackClient)
	assert.NotNil(t, processor.beadsMgr)
	assert.NotNil(t, processor.cfg)
	assert.NotNil(t, processor.logger)
}

func TestMessageProcessor_ProcessMessage_UserDirError(t *testing.T) {
	client := newMockSlackClient()
	beadsMgr := newMockBeadsManager()
	beadsMgr.ensureUserDirError = errors.New("failed to init beads")
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, beadsMgr, cfg, logger)

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
		Text:      "Hello",
		MessageTS: "ts.124",
	}

	err := processor.ProcessMessage(context.Background(), msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to init beads")

	// Verify error message was posted to user
	calls := client.getPostCalls()
	assert.NotEmpty(t, calls)
	assert.Contains(t, calls[0].text, "Unable to initialize user session")
}

func TestMessageProcessor_ProcessMessage_UsesMessageTSWhenNoThread(t *testing.T) {
	client := newMockSlackClient()
	beadsMgr := newMockBeadsManager()
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, beadsMgr, cfg, logger)

	// Message with no thread (empty ThreadTS)
	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "", // no thread
		UserID:    "U456",
		Text:      "Hello",
		MessageTS: "ts.124",
	}

	// This will fail at Kiro bridge start (which is expected)
	// We're testing that the issue is created with MessageTS as thread
	_ = processor.ProcessMessage(context.Background(), msg)

	// Verify issue was created with MessageTS as threadTS
	issue, _ := beadsMgr.FindThreadIssue(context.Background(), "U456", &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "ts.124",
		UserID:    "U456",
	})
	assert.NotNil(t, issue)
}

func TestMessageProcessor_ProcessMessage_CreatesIssueForNewThread(t *testing.T) {
	client := newMockSlackClient()
	beadsMgr := newMockBeadsManager()
	cfg := &config.Config{}
	logger := zap.NewNop()

	processor := NewMessageProcessor(client, beadsMgr, cfg, logger)

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
		Text:      "Hello",
		MessageTS: "ts.124",
	}

	// Call ProcessMessage - it will fail at Kiro start, but issue should be created
	_ = processor.ProcessMessage(context.Background(), msg)

	// Verify an issue was created
	issue, _ := beadsMgr.FindThreadIssue(context.Background(), "U456", &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
	})
	assert.NotNil(t, issue)
	assert.Equal(t, "Hello", issue.Title)
}

func TestMessageProcessor_ProcessMessage_UpdatesExistingIssue(t *testing.T) {
	client := newMockSlackClient()
	beadsMgr := newMockBeadsManager()
	cfg := &config.Config{}
	logger := zap.NewNop()

	// Pre-create an issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
	}
	_, _ = beadsMgr.CreateThreadIssue(context.Background(), "U456", thread, "First message")

	processor := NewMessageProcessor(client, beadsMgr, cfg, logger)

	msg := &slack.MessageEvent{
		ChannelID: "C123",
		ThreadTS:  "ts.123",
		UserID:    "U456",
		Text:      "Second message",
		MessageTS: "ts.125",
	}

	// Process second message to existing thread
	_ = processor.ProcessMessage(context.Background(), msg)

	// Verify issue was updated with new comment
	comments := beadsMgr.comments["U456:issue-ts.123"]
	assert.Contains(t, comments, "[user] Second message")
}

func TestMessageProcessor_Interfaces_Satisfied(t *testing.T) {
	// Test that the interfaces are properly satisfied
	var _ slack.ClientInterface = (*mockSlackClient)(nil)
	var _ BeadsManager = (*mockBeadsManager)(nil)
}

func TestBuildContextualPrompt_EmptyMessages(t *testing.T) {
	result := buildContextualPrompt(nil, "Current message")
	assert.Equal(t, "Current message", result)

	result = buildContextualPrompt([]beads.Message{}, "Current message")
	assert.Equal(t, "Current message", result)
}

func TestBuildContextualPrompt_WithHistory(t *testing.T) {
	messages := []beads.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	result := buildContextualPrompt(messages, "How are you?")

	assert.Contains(t, result, "Previous conversation context:")
	assert.Contains(t, result, "User: Hello")
	assert.Contains(t, result, "Assistant: Hi there!")
	assert.Contains(t, result, "Current message:")
	assert.Contains(t, result, "How are you?")
}

func TestBuildContextualPrompt_TruncatesLongMessages(t *testing.T) {
	longMessage := string(make([]byte, 600)) // 600 chars
	messages := []beads.Message{
		{Role: "user", Content: longMessage},
	}

	result := buildContextualPrompt(messages, "Current")

	// Should be truncated to 500 chars + "..."
	assert.Contains(t, result, "...")
	assert.Less(t, len(result), len(longMessage)+100)
}
