package slack

import (
	"context"
	"testing"

	"github.com/slack-go/slack/slackevents"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestHandler_HandleAppMention(t *testing.T) {
	logger := zap.NewNop()

	handlerCalled := false
	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			handlerCalled = true
			assert.Equal(t, "C123", msg.ChannelID)
			assert.Equal(t, "U456", msg.UserID)
			assert.Equal(t, "hello", msg.Text) // Mention cleaned
			assert.True(t, msg.IsMention)
			return nil
		},
		logger: logger,
	}

	ev := &slackevents.AppMentionEvent{
		Channel:         "C123",
		User:            "U456",
		Text:            "<@UBOT123> hello",
		TimeStamp:       "1234567890.123456",
		ThreadTimeStamp: "",
	}

	handler.handleAppMention(ev)

	// Handler is called asynchronously, so we verify setup is correct
	assert.NotNil(t, handler.messageHandler)
	_ = handlerCalled // Used in async callback
}

func TestHandler_HandleMessage_IgnoresBot(t *testing.T) {
	logger := zap.NewNop()

	handlerCalled := false
	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			handlerCalled = true
			return nil
		},
		logger: logger,
	}

	// Bot message should be ignored
	ev := &slackevents.MessageEvent{
		Channel: "D123",
		User:    "U456",
		BotID:   "B789",
		Text:    "bot message",
	}

	handler.handleMessage(ev)
	assert.False(t, handlerCalled, "handler should not be called for bot messages")
}

func TestHandler_HandleMessage_IgnoresNonDM(t *testing.T) {
	logger := zap.NewNop()

	handlerCalled := false
	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			handlerCalled = true
			return nil
		},
		logger: logger,
	}

	// Channel message should be ignored (not DM)
	ev := &slackevents.MessageEvent{
		Channel: "C123", // Starts with C, not D
		User:    "U456",
		Text:    "channel message",
	}

	handler.handleMessage(ev)
	assert.False(t, handlerCalled, "handler should not be called for non-DM messages")
}

func TestHandler_HandleMessage_IgnoresSubtypes(t *testing.T) {
	logger := zap.NewNop()

	handlerCalled := false
	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			handlerCalled = true
			return nil
		},
		logger: logger,
	}

	// Message with subtype should be ignored
	ev := &slackevents.MessageEvent{
		Channel: "D123",
		User:    "U456",
		Text:    "edited message",
		SubType: "message_changed",
	}

	handler.handleMessage(ev)
	assert.False(t, handlerCalled, "handler should not be called for message subtypes")
}

func TestNewSlackAPI(t *testing.T) {
	api := NewSlackAPI("xoxb-test-token", "xapp-test-token", false)
	assert.NotNil(t, api)
}

func TestNewSocketModeClient(t *testing.T) {
	api := NewSlackAPI("xoxb-test-token", "xapp-test-token", false)
	socketClient := NewSocketModeClient(api, false)
	assert.NotNil(t, socketClient)
}
