package slack

import (
	"context"
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func TestWithThreadTS(t *testing.T) {
	cfg := &messageConfig{}
	opt := WithThreadTS("1234567890.123456")
	opt(cfg)
	assert.Equal(t, "1234567890.123456", cfg.threadTS)
}

func TestWithBlocks(t *testing.T) {
	cfg := &messageConfig{}
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "test", false, false),
			nil,
			nil,
		),
	}
	opt := WithBlocks(blocks)
	opt(cfg)
	assert.Len(t, cfg.blocks, 1)
}

// Note: Full client tests require mocking the Slack API.
// The ClientInterface allows for easy mocking in tests that use the client.

// MockSlackClient implements ClientInterface for testing.
type MockSlackClient struct {
	PostMessageFunc    func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error)
	UpdateMessageFunc  func(ctx context.Context, channelID, ts, text string) error
	AddReactionFunc    func(ctx context.Context, channelID, ts, emoji string) error
	RemoveReactionFunc func(ctx context.Context, channelID, ts, emoji string) error
	BotUserID          string
}

func (m *MockSlackClient) PostMessage(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
	if m.PostMessageFunc != nil {
		return m.PostMessageFunc(ctx, channelID, text, opts...)
	}
	return "1234567890.123456", nil
}

func (m *MockSlackClient) UpdateMessage(ctx context.Context, channelID, ts, text string) error {
	if m.UpdateMessageFunc != nil {
		return m.UpdateMessageFunc(ctx, channelID, ts, text)
	}
	return nil
}

func (m *MockSlackClient) AddReaction(ctx context.Context, channelID, ts, emoji string) error {
	if m.AddReactionFunc != nil {
		return m.AddReactionFunc(ctx, channelID, ts, emoji)
	}
	return nil
}

func (m *MockSlackClient) RemoveReaction(ctx context.Context, channelID, ts, emoji string) error {
	if m.RemoveReactionFunc != nil {
		return m.RemoveReactionFunc(ctx, channelID, ts, emoji)
	}
	return nil
}

func (m *MockSlackClient) GetBotUserID() string {
	return m.BotUserID
}

func TestMockClient_ImplementsInterface(t *testing.T) {
	var _ ClientInterface = &MockSlackClient{}
}

func TestMockClient_PostMessage(t *testing.T) {
	ctx := context.Background()
	called := false
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			called = true
			assert.Equal(t, "C123", channelID)
			assert.Equal(t, "hello", text)
			return "ts123", nil
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.NoError(t, err)
	assert.Equal(t, "ts123", ts)
	assert.True(t, called)
}

func TestMockClient_UpdateMessage(t *testing.T) {
	ctx := context.Background()
	called := false
	mock := &MockSlackClient{
		UpdateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			called = true
			return nil
		},
	}

	err := mock.UpdateMessage(ctx, "C123", "ts123", "updated")
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMockClient_AddReaction(t *testing.T) {
	ctx := context.Background()
	called := false
	mock := &MockSlackClient{
		AddReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			called = true
			assert.Equal(t, "eyes", emoji)
			return nil
		},
	}

	err := mock.AddReaction(ctx, "C123", "ts123", "eyes")
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMockClient_RemoveReaction(t *testing.T) {
	ctx := context.Background()
	called := false
	mock := &MockSlackClient{
		RemoveReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			called = true
			return nil
		},
	}

	err := mock.RemoveReaction(ctx, "C123", "ts123", "eyes")
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMockClient_GetBotUserID(t *testing.T) {
	mock := &MockSlackClient{BotUserID: "UBOT123"}
	assert.Equal(t, "UBOT123", mock.GetBotUserID())
}

func TestMockClient_DefaultBehavior(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{}

	// Defaults return no error
	ts, err := mock.PostMessage(ctx, "C123", "test")
	assert.NoError(t, err)
	assert.NotEmpty(t, ts)

	err = mock.UpdateMessage(ctx, "C123", "ts", "text")
	assert.NoError(t, err)

	err = mock.AddReaction(ctx, "C123", "ts", "emoji")
	assert.NoError(t, err)

	err = mock.RemoveReaction(ctx, "C123", "ts", "emoji")
	assert.NoError(t, err)
}
