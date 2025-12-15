package streaming

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockSlackClient implements slack.ClientInterface for testing.
type mockSlackClient struct {
	mu             sync.Mutex
	postCalls      []postCall
	updateCalls    []updateCall
	postError      error
	updateError    error
	nextPostTS     string
	lastThreadTS   string // captured from WithThreadTS option
}

type postCall struct {
	channelID string
	text      string
	threadTS  string
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

	call := postCall{channelID: channelID, text: text}
	// Check if there are options (indicates thread reply)
	if len(opts) > 0 {
		call.threadTS = m.lastThreadTS
	}
	m.postCalls = append(m.postCalls, call)
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

func (m *mockSlackClient) setThreadTS(ts string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastThreadTS = ts
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

func TestStreamer_Start(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	ts, err := streamer.Start(context.Background(), "C123", "")
	require.NoError(t, err)
	assert.Equal(t, "msg.12345", ts)

	calls := client.getPostCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "C123", calls[0].channelID)
	assert.Contains(t, calls[0].text, "Thinking")
	assert.Equal(t, "", calls[0].threadTS)

	assert.True(t, streamer.IsStarted())
	assert.False(t, streamer.IsCompleted())
}

func TestStreamer_Start_InThread(t *testing.T) {
	client := newMockSlackClient()
	client.setThreadTS("thread.123")
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	ts, err := streamer.Start(context.Background(), "C123", "thread.123")
	require.NoError(t, err)
	assert.Equal(t, "msg.12345", ts)

	calls := client.getPostCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "thread.123", calls[0].threadTS)
}

func TestStreamer_Start_Error(t *testing.T) {
	client := newMockSlackClient()
	client.postError = errors.New("slack error")
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	_, err := streamer.Start(context.Background(), "C123", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to post initial message")
}

func TestStreamer_Start_AlreadyStarted(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	_, err := streamer.Start(context.Background(), "C123", "")
	require.NoError(t, err)

	_, err = streamer.Start(context.Background(), "C123", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already started")
}

func TestStreamer_Update(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 50 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	_, err := streamer.Start(context.Background(), "C123", "")
	require.NoError(t, err)

	// First update should flush immediately via buffer
	err = streamer.Update(context.Background(), "Hello")
	require.NoError(t, err)

	// Wait for potential debounce
	time.Sleep(80 * time.Millisecond)

	calls := client.getUpdateCalls()
	require.GreaterOrEqual(t, len(calls), 1)
	assert.Contains(t, calls[0].text, "Hello")
	assert.Contains(t, calls[0].text, writingEmoji)
}

func TestStreamer_Update_NotStarted(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	err := streamer.Update(context.Background(), "Hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestStreamer_Complete(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	_, err := streamer.Start(context.Background(), "C123", "")
	require.NoError(t, err)

	err = streamer.Complete(context.Background(), "Final response")
	require.NoError(t, err)

	calls := client.getUpdateCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "Final response", calls[0].text)
	assert.NotContains(t, calls[0].text, writingEmoji)

	assert.True(t, streamer.IsCompleted())
}

func TestStreamer_Complete_NotStarted(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	err := streamer.Complete(context.Background(), "Final")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestStreamer_Complete_Idempotent(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	_, err := streamer.Start(context.Background(), "C123", "")
	require.NoError(t, err)

	err = streamer.Complete(context.Background(), "Final 1")
	require.NoError(t, err)

	err = streamer.Complete(context.Background(), "Final 2")
	require.NoError(t, err) // Should succeed but not update

	calls := client.getUpdateCalls()
	assert.Len(t, calls, 1) // Only one update
}

func TestStreamer_Error(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	_, err := streamer.Start(context.Background(), "C123", "")
	require.NoError(t, err)

	testErr := errors.New("something went wrong")
	err = streamer.Error(context.Background(), testErr)
	require.NoError(t, err)

	calls := client.getUpdateCalls()
	require.Len(t, calls, 1)
	assert.Contains(t, calls[0].text, errorEmoji)
	assert.Contains(t, calls[0].text, "something went wrong")

	assert.True(t, streamer.IsCompleted())
}

func TestStreamer_Error_NotStarted(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	err := streamer.Error(context.Background(), errors.New("test"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestStreamer_FullFlow(t *testing.T) {
	client := newMockSlackClient()
	client.setThreadTS("thread.456")
	cfg := &config.StreamingConfig{UpdateInterval: 50 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	// Start
	_, err := streamer.Start(context.Background(), "C123", "thread.456")
	require.NoError(t, err)

	// Updates
	err = streamer.Update(context.Background(), "Hello")
	require.NoError(t, err)

	err = streamer.Update(context.Background(), "Hello World")
	require.NoError(t, err)

	// Wait for buffer
	time.Sleep(80 * time.Millisecond)

	// Complete
	err = streamer.Complete(context.Background(), "Hello World, done!")
	require.NoError(t, err)

	// Verify
	postCalls := client.getPostCalls()
	require.Len(t, postCalls, 1)
	assert.Contains(t, postCalls[0].text, "Thinking")
	assert.Equal(t, "thread.456", postCalls[0].threadTS)

	updateCalls := client.getUpdateCalls()
	require.GreaterOrEqual(t, len(updateCalls), 1)

	// Last update should be final content without indicator
	lastUpdate := updateCalls[len(updateCalls)-1]
	assert.Equal(t, "Hello World, done!", lastUpdate.text)
}

func TestStreamer_UpdateAfterComplete_Ignored(t *testing.T) {
	client := newMockSlackClient()
	cfg := &config.StreamingConfig{UpdateInterval: 100 * time.Millisecond}
	logger := zap.NewNop()

	streamer := NewStreamer(client, cfg, logger)

	_, err := streamer.Start(context.Background(), "C123", "")
	require.NoError(t, err)

	err = streamer.Complete(context.Background(), "Final")
	require.NoError(t, err)

	// Update after complete should be ignored
	err = streamer.Update(context.Background(), "After complete")
	assert.NoError(t, err)

	calls := client.getUpdateCalls()
	assert.Len(t, calls, 1) // Only the complete call
}
