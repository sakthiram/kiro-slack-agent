package sync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockBeadsManager implements a mock for beads.Manager interface for testing.
type mockBeadsManager struct {
	mu                        sync.Mutex
	messages                  []beads.Message
	labels                    map[string][]string // issueID -> labels
	getConversationContextErr error
	addLabelErr               error
	addLabelCalls             []addLabelCall
}

type addLabelCall struct {
	userID  string
	issueID string
	label   string
}

func newMockBeadsManager() *mockBeadsManager {
	return &mockBeadsManager{
		labels: make(map[string][]string),
	}
}

func (m *mockBeadsManager) GetConversationContext(ctx context.Context, userID, issueID string) ([]beads.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getConversationContextErr != nil {
		return nil, m.getConversationContextErr
	}

	// Return a copy to avoid race conditions
	result := make([]beads.Message, len(m.messages))
	copy(result, m.messages)
	return result, nil
}

func (m *mockBeadsManager) AddLabel(ctx context.Context, userID, issueID, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.addLabelErr != nil {
		return m.addLabelErr
	}

	m.addLabelCalls = append(m.addLabelCalls, addLabelCall{
		userID:  userID,
		issueID: issueID,
		label:   label,
	})

	if m.labels[issueID] == nil {
		m.labels[issueID] = []string{}
	}
	m.labels[issueID] = append(m.labels[issueID], label)

	return nil
}

func (m *mockBeadsManager) HasLabel(ctx context.Context, userID, issueID, label string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	labels, ok := m.labels[issueID]
	if !ok {
		return false
	}

	for _, l := range labels {
		if l == label {
			return true
		}
	}
	return false
}

func (m *mockBeadsManager) ListUserDirs() []string {
	return []string{}
}

func (m *mockBeadsManager) ListIssuesByStatus(ctx context.Context, userID string, statuses []string) ([]*beads.Issue, error) {
	return []*beads.Issue{}, nil
}

func (m *mockBeadsManager) GetThreadTaskCounts(ctx context.Context, userID, threadTS string) (int, int, int, error) {
	return 0, 0, 0, nil
}

func (m *mockBeadsManager) setMessages(messages []beads.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = messages
}

func (m *mockBeadsManager) getAddLabelCalls() []addLabelCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]addLabelCall{}, m.addLabelCalls...)
}

// mockSlackClient implements slack.ClientInterface for testing.
type mockSlackClient struct {
	mu          sync.Mutex
	postCalls   []postCall
	postError   error
	nextPostTS  string
	threadTS    string
}

type postCall struct {
	channelID string
	text      string
	threadTS  string
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

	call := postCall{channelID: channelID, text: text}
	if m.threadTS != "" {
		call.threadTS = m.threadTS
	}
	m.postCalls = append(m.postCalls, call)
	return m.nextPostTS, nil
}

func (m *mockSlackClient) UpdateMessage(ctx context.Context, channelID, ts, text string) error {
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

func (m *mockSlackClient) setThreadTS(ts string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.threadTS = ts
}

func (m *mockSlackClient) getPostCalls() []postCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]postCall{}, m.postCalls...)
}

// TestSyncIssue_SkipsAlreadySyncedComments tests that comments already synced
// (with labels present) are not synced again.
func TestSyncIssue_SkipsAlreadySyncedComments(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	slackClient.setThreadTS("thread.123")
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue for sync
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Set up messages with assistant comments
	baseTime := time.Now()
	messages := []beads.Message{
		{
			Role:      "user",
			Content:   "Hello",
			Timestamp: baseTime,
		},
		{
			Role:      "assistant",
			Content:   "Hi there!",
			Timestamp: baseTime.Add(1 * time.Second),
		},
		{
			Role:      "assistant",
			Content:   "How can I help?",
			Timestamp: baseTime.Add(2 * time.Second),
		},
	}
	beadsMgr.setMessages(messages)

	// First sync - should sync both assistant comments
	err := syncer.SyncIssue(ctx, "issue-1")
	require.NoError(t, err)

	calls := slackClient.getPostCalls()
	assert.Len(t, calls, 2, "should sync 2 assistant comments")
	assert.Equal(t, "Hi there!", calls[0].text)
	assert.Equal(t, "How can I help?", calls[1].text)
	assert.Equal(t, "thread.123", calls[0].threadTS)

	// Verify labels were added
	labelCalls := beadsMgr.getAddLabelCalls()
	assert.Len(t, labelCalls, 2, "should add 2 sync labels")

	// Second sync - should skip already synced comments
	err = syncer.SyncIssue(ctx, "issue-1")
	require.NoError(t, err)

	calls = slackClient.getPostCalls()
	assert.Len(t, calls, 2, "should not post duplicate messages")
}

// TestSyncIssue_DifferentRolesSameTimestamp tests that comments with the same
// timestamp but different roles generate unique comment IDs.
func TestSyncIssue_DifferentRolesSameTimestamp(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	slackClient.setThreadTS("thread.123")
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Create messages with same timestamp but different roles
	sameTime := time.Now()
	messages := []beads.Message{
		{
			Role:      "user",
			Content:   "Question",
			Timestamp: sameTime,
		},
		{
			Role:      "assistant",
			Content:   "Answer",
			Timestamp: sameTime, // Same timestamp!
		},
	}
	beadsMgr.setMessages(messages)

	// Sync the issue
	err := syncer.SyncIssue(ctx, "issue-1")
	require.NoError(t, err)

	// Should sync the assistant comment
	calls := slackClient.getPostCalls()
	assert.Len(t, calls, 1, "should sync 1 assistant comment")
	assert.Equal(t, "Answer", calls[0].text)

	// Verify comment IDs are based on role + timestamp
	labelCalls := beadsMgr.getAddLabelCalls()
	assert.Len(t, labelCalls, 1)

	// Comment ID format: "<role>_<timestamp_unixnano>"
	expectedCommentID := fmt.Sprintf("assistant_%d", sameTime.UnixNano())
	expectedLabel := "synced:" + expectedCommentID
	assert.Equal(t, expectedLabel, labelCalls[0].label)
}

// TestSyncIssue_StableIDsAcrossCycles tests that adding new messages doesn't
// cause old comments to get new IDs and re-sync.
func TestSyncIssue_StableIDsAcrossCycles(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	slackClient.setThreadTS("thread.123")
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Initial messages
	baseTime := time.Now()
	messages := []beads.Message{
		{
			Role:      "user",
			Content:   "First question",
			Timestamp: baseTime,
		},
		{
			Role:      "assistant",
			Content:   "First answer",
			Timestamp: baseTime.Add(1 * time.Second),
		},
	}
	beadsMgr.setMessages(messages)

	// First sync
	err := syncer.SyncIssue(ctx, "issue-1")
	require.NoError(t, err)

	calls := slackClient.getPostCalls()
	assert.Len(t, calls, 1)
	assert.Equal(t, "First answer", calls[0].text)

	// Capture the label that was added for the first assistant message
	labelCalls := beadsMgr.getAddLabelCalls()
	require.Len(t, labelCalls, 1)
	firstLabel := labelCalls[0].label

	// Add new messages to the conversation
	messages = append(messages, beads.Message{
		Role:      "user",
		Content:   "Second question",
		Timestamp: baseTime.Add(2 * time.Second),
	})
	messages = append(messages, beads.Message{
		Role:      "assistant",
		Content:   "Second answer",
		Timestamp: baseTime.Add(3 * time.Second),
	})
	beadsMgr.setMessages(messages)

	// Second sync - should only sync the new assistant message
	err = syncer.SyncIssue(ctx, "issue-1")
	require.NoError(t, err)

	calls = slackClient.getPostCalls()
	assert.Len(t, calls, 2, "should have 2 total posts")
	assert.Equal(t, "Second answer", calls[1].text, "should sync new message")

	// Verify that the old message kept the same ID
	labelCalls = beadsMgr.getAddLabelCalls()
	assert.Len(t, labelCalls, 2)
	assert.Equal(t, firstLabel, labelCalls[0].label, "first comment ID should remain stable")

	// The new label should be for the second assistant message
	expectedNewCommentID := fmt.Sprintf("assistant_%d", baseTime.Add(3*time.Second).UnixNano())
	expectedNewLabel := "synced:" + expectedNewCommentID
	assert.Equal(t, expectedNewLabel, labelCalls[1].label)
}

// TestSyncIssue_OnlyAssistantComments tests that only messages with
// "assistant" role are synced, while "user" messages are skipped.
func TestSyncIssue_OnlyAssistantComments(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	slackClient.setThreadTS("thread.123")
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Set up messages with mixed roles
	baseTime := time.Now()
	messages := []beads.Message{
		{
			Role:      "user",
			Content:   "User message 1",
			Timestamp: baseTime,
		},
		{
			Role:      "assistant",
			Content:   "Assistant message 1",
			Timestamp: baseTime.Add(1 * time.Second),
		},
		{
			Role:      "user",
			Content:   "User message 2",
			Timestamp: baseTime.Add(2 * time.Second),
		},
		{
			Role:      "assistant",
			Content:   "Assistant message 2",
			Timestamp: baseTime.Add(3 * time.Second),
		},
		{
			Role:      "user",
			Content:   "User message 3",
			Timestamp: baseTime.Add(4 * time.Second),
		},
	}
	beadsMgr.setMessages(messages)

	// Sync the issue
	err := syncer.SyncIssue(ctx, "issue-1")
	require.NoError(t, err)

	// Should only sync assistant messages
	calls := slackClient.getPostCalls()
	assert.Len(t, calls, 2, "should only sync assistant messages")
	assert.Equal(t, "Assistant message 1", calls[0].text)
	assert.Equal(t, "Assistant message 2", calls[1].text)

	// Verify labels were only added for assistant messages
	labelCalls := beadsMgr.getAddLabelCalls()
	assert.Len(t, labelCalls, 2, "should only add labels for assistant messages")
}

// TestSyncIssue_NotRegistered tests that syncing an unregistered issue returns an error.
func TestSyncIssue_NotRegistered(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Try to sync without registering
	err := syncer.SyncIssue(ctx, "issue-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is not registered for sync")
}

// TestSyncIssue_GetConversationContextError tests error handling when getting conversation context fails.
func TestSyncIssue_GetConversationContextError(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Set error
	beadsMgr.getConversationContextErr = errors.New("database error")

	err := syncer.SyncIssue(ctx, "issue-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get conversation context")
}

// TestSyncIssue_SlackPostError tests that sync continues even if posting to Slack fails.
func TestSyncIssue_SlackPostError(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	slackClient.setThreadTS("thread.123")
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Set up messages
	baseTime := time.Now()
	messages := []beads.Message{
		{
			Role:      "assistant",
			Content:   "Message 1",
			Timestamp: baseTime,
		},
		{
			Role:      "assistant",
			Content:   "Message 2",
			Timestamp: baseTime.Add(1 * time.Second),
		},
	}
	beadsMgr.setMessages(messages)

	// Set Slack to return error
	slackClient.postError = errors.New("slack api error")

	// Sync should complete without error (errors are logged but not returned)
	err := syncer.SyncIssue(ctx, "issue-1")
	assert.NoError(t, err)

	// No labels should be added because posting failed
	labelCalls := beadsMgr.getAddLabelCalls()
	assert.Len(t, labelCalls, 0, "should not add labels when posting fails")
}

// TestSyncIssue_AddLabelError tests that label errors are logged but don't stop sync.
func TestSyncIssue_AddLabelError(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	slackClient.setThreadTS("thread.123")
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Set up messages
	baseTime := time.Now()
	messages := []beads.Message{
		{
			Role:      "assistant",
			Content:   "Test message",
			Timestamp: baseTime,
		},
	}
	beadsMgr.setMessages(messages)

	// Set label add to fail
	beadsMgr.addLabelErr = errors.New("label error")

	// Sync should complete without error (label errors are logged but not fatal)
	err := syncer.SyncIssue(ctx, "issue-1")
	assert.NoError(t, err)

	// Message should still be posted
	calls := slackClient.getPostCalls()
	assert.Len(t, calls, 1)
}

// TestSyncIssue_EmptyMessages tests that sync handles empty message lists gracefully.
func TestSyncIssue_EmptyMessages(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Set empty messages
	beadsMgr.setMessages([]beads.Message{})

	err := syncer.SyncIssue(ctx, "issue-1")
	assert.NoError(t, err)

	// No posts should be made
	calls := slackClient.getPostCalls()
	assert.Len(t, calls, 0)
}

// TestRegisterAndUnregister tests the registration and unregistration flow.
func TestRegisterAndUnregister(t *testing.T) {
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Verify issue is tracked
	state := syncer.tracker.GetState("issue-1")
	require.NotNil(t, state)
	assert.Equal(t, "issue-1", state.IssueID)
	assert.Equal(t, "user-1", state.UserID)
	assert.Equal(t, "C123", state.ChannelID)
	assert.Equal(t, "thread.123", state.SlackThreadTS)

	// Unregister issue
	syncer.Unregister("issue-1")

	// Verify issue is no longer tracked
	state = syncer.tracker.GetState("issue-1")
	assert.Nil(t, state)
}

// TestCommentIDFormat tests that comment IDs follow the expected format.
func TestCommentIDFormat(t *testing.T) {
	ctx := context.Background()
	beadsMgr := newMockBeadsManager()
	slackClient := newMockSlackClient()
	slackClient.setThreadTS("thread.123")
	logger := zap.NewNop()

	syncer := NewCommentSyncer(beadsMgr, slackClient, logger)

	// Register issue
	thread := &beads.ThreadInfo{
		ChannelID: "C123",
		ThreadTS:  "thread.123",
		UserID:    "U456",
	}
	syncer.RegisterIssue("issue-1", "user-1", thread)

	// Create a message with known timestamp
	timestamp := time.Unix(1700000000, 0) // Known timestamp for testing
	messages := []beads.Message{
		{
			Role:      "assistant",
			Content:   "Test",
			Timestamp: timestamp,
		},
	}
	beadsMgr.setMessages(messages)

	// Sync
	err := syncer.SyncIssue(ctx, "issue-1")
	require.NoError(t, err)

	// Verify comment ID format: <role>_<timestamp_unixnano>
	labelCalls := beadsMgr.getAddLabelCalls()
	require.Len(t, labelCalls, 1)

	expectedCommentID := fmt.Sprintf("assistant_%d", timestamp.UnixNano())
	expectedLabel := "synced:" + expectedCommentID
	assert.Equal(t, expectedLabel, labelCalls[0].label)
}
