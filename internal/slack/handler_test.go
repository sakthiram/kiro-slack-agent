package slack

import (
	"context"
	"testing"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
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
	handler.SetSyncMode(true)

	ev := &slackevents.AppMentionEvent{
		Channel:         "C123",
		User:            "U456",
		Text:            "<@UBOT123> hello",
		TimeStamp:       "1234567890.123456",
		ThreadTimeStamp: "",
	}

	handler.handleAppMention(ev)

	// With sync mode, handler is called synchronously
	assert.True(t, handlerCalled)
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

func TestNewSocketModeClient_WithDebug(t *testing.T) {
	api := NewSlackAPI("xoxb-test-token", "xapp-test-token", true)
	socketClient := NewSocketModeClient(api, true)
	assert.NotNil(t, socketClient)
}

func TestNewHandler(t *testing.T) {
	logger := zap.NewNop()
	client := &Client{botUserID: "UBOT123"}
	handler := NewHandler(client, func(ctx context.Context, msg *MessageEvent) error {
		return nil
	}, logger)

	assert.NotNil(t, handler)
	assert.NotNil(t, handler.messageHandler)
	assert.Equal(t, client, handler.client)
}

func TestHandler_HandleMessage_ValidDM(t *testing.T) {
	logger := zap.NewNop()

	handlerCalled := false
	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			handlerCalled = true
			assert.Equal(t, "D123", msg.ChannelID)
			assert.Equal(t, "U456", msg.UserID)
			assert.Equal(t, "hello", msg.Text)
			assert.True(t, msg.IsDM)
			return nil
		},
		logger: logger,
	}
	handler.SetSyncMode(true)

	ev := &slackevents.MessageEvent{
		Channel:   "D123",
		User:      "U456",
		Text:      "hello",
		TimeStamp: "1234567890.123456",
	}

	handler.handleMessage(ev)

	// With sync mode, handler is called synchronously
	assert.True(t, handlerCalled)
}

func TestHandler_HandleMessage_EmptyChannel(t *testing.T) {
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

	// Empty channel should be ignored
	ev := &slackevents.MessageEvent{
		Channel: "",
		User:    "U456",
		Text:    "hello",
	}

	handler.handleMessage(ev)
	assert.False(t, handlerCalled)
}

func TestHandler_HandleCallbackEvent_AppMention(t *testing.T) {
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
	handler.SetSyncMode(true)

	event := slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent,
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "app_mention",
			Data: &slackevents.AppMentionEvent{
				Channel:   "C123",
				User:      "U456",
				Text:      "<@UBOT123> hello",
				TimeStamp: "1234567890.123456",
			},
		},
	}

	// Should not panic
	handler.handleCallbackEvent(event)
	assert.True(t, handlerCalled)
}

func TestHandler_HandleCallbackEvent_Message(t *testing.T) {
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
	handler.SetSyncMode(true)

	event := slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent,
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "message",
			Data: &slackevents.MessageEvent{
				Channel:   "D123",
				User:      "U456",
				Text:      "hello",
				TimeStamp: "1234567890.123456",
			},
		},
	}

	// Should not panic
	handler.handleCallbackEvent(event)
	assert.True(t, handlerCalled)
}

func TestHandler_HandleCallbackEvent_UnknownType(t *testing.T) {
	logger := zap.NewNop()

	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			return nil
		},
		logger: logger,
	}

	// Unknown inner event type
	event := slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent,
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Type: "unknown_type",
			Data: nil,
		},
	}

	// Should not panic
	handler.handleCallbackEvent(event)
}

func TestHandler_HandleEvent_AppMention(t *testing.T) {
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
	handler.SetSyncMode(true)

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "app_mention",
				Data: &slackevents.AppMentionEvent{
					Channel:         "C123",
					User:            "U456",
					Text:            "<@UBOT123> hello",
					TimeStamp:       "1234567890.123456",
					ThreadTimeStamp: "",
				},
			},
		},
	}

	err := handler.HandleEvent(evt, nil)
	assert.NoError(t, err)
	assert.True(t, handlerCalled)
}

func TestHandler_HandleEvent_DirectMessage(t *testing.T) {
	logger := zap.NewNop()

	handlerCalled := false
	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			handlerCalled = true
			assert.Equal(t, "D123", msg.ChannelID)
			assert.Equal(t, "U456", msg.UserID)
			assert.Equal(t, "hello", msg.Text)
			assert.True(t, msg.IsDM)
			return nil
		},
		logger: logger,
	}
	handler.SetSyncMode(true)

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "message",
				Data: &slackevents.MessageEvent{
					Channel:   "D123",
					User:      "U456",
					Text:      "hello",
					TimeStamp: "1234567890.123456",
				},
			},
		},
	}

	err := handler.HandleEvent(evt, nil)
	assert.NoError(t, err)
	assert.True(t, handlerCalled)
}

func TestHandler_HandleEvent_ConnectionEvents(t *testing.T) {
	logger := zap.NewNop()

	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			return nil
		},
		logger: logger,
	}

	tests := []struct {
		name      string
		eventType socketmode.EventType
	}{
		{"connecting", socketmode.EventTypeConnecting},
		{"connected", socketmode.EventTypeConnected},
		{"hello", socketmode.EventTypeHello},
		{"connection_error", socketmode.EventTypeConnectionError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := socketmode.Event{
				Type: tt.eventType,
			}

			err := handler.HandleEvent(evt, nil)
			assert.NoError(t, err)
		})
	}
}

func TestHandler_HandleEvent_UnknownEventType(t *testing.T) {
	logger := zap.NewNop()

	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			return nil
		},
		logger: logger,
	}

	evt := socketmode.Event{
		Type: socketmode.EventType("unknown_type"),
	}

	err := handler.HandleEvent(evt, nil)
	assert.NoError(t, err) // Should not error, just log
}

func TestHandler_HandleEvent_InvalidEventData(t *testing.T) {
	logger := zap.NewNop()

	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			return nil
		},
		logger: logger,
	}

	// EventTypeEventsAPI with invalid data type
	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: "invalid_data_type", // Not an EventsAPIEvent
	}

	err := handler.HandleEvent(evt, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to cast event to EventsAPIEvent")
}

func TestHandler_HandleEvent_BotMessage_Ignored(t *testing.T) {
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

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "message",
				Data: &slackevents.MessageEvent{
					Channel: "D123",
					User:    "U456",
					BotID:   "B789", // Bot message
					Text:    "bot message",
				},
			},
		},
	}

	err := handler.HandleEvent(evt, nil)
	assert.NoError(t, err)
	assert.False(t, handlerCalled, "handler should not be called for bot messages")
}

func TestHandler_HandleEvent_NonDM_Ignored(t *testing.T) {
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

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "message",
				Data: &slackevents.MessageEvent{
					Channel: "C123", // Channel, not DM
					User:    "U456",
					Text:    "channel message",
				},
			},
		},
	}

	err := handler.HandleEvent(evt, nil)
	assert.NoError(t, err)
	assert.False(t, handlerCalled, "handler should not be called for non-DM messages")
}

func TestHandler_SetSyncMode(t *testing.T) {
	logger := zap.NewNop()

	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			return nil
		},
		logger: logger,
	}

	// Test that sync mode is disabled by default
	assert.False(t, handler.syncMode)

	// Test enabling sync mode
	handler.SetSyncMode(true)
	assert.True(t, handler.syncMode)

	// Test disabling sync mode
	handler.SetSyncMode(false)
	assert.False(t, handler.syncMode)
}

func TestHandler_SyncMode_AppMention(t *testing.T) {
	logger := zap.NewNop()

	handlerCalled := false
	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			handlerCalled = true
			assert.Equal(t, "C123", msg.ChannelID)
			return nil
		},
		logger: logger,
	}
	handler.SetSyncMode(true)

	ev := &slackevents.AppMentionEvent{
		Channel:   "C123",
		User:      "U456",
		Text:      "<@UBOT123> hello",
		TimeStamp: "1234567890.123456",
	}

	handler.handleAppMention(ev)

	// With sync mode enabled, handler should be called immediately
	assert.True(t, handlerCalled)
}

func TestHandler_SyncMode_DirectMessage(t *testing.T) {
	logger := zap.NewNop()

	handlerCalled := false
	handler := &Handler{
		client: &Client{botUserID: "UBOT123"},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			handlerCalled = true
			assert.Equal(t, "D123", msg.ChannelID)
			return nil
		},
		logger: logger,
	}
	handler.SetSyncMode(true)

	ev := &slackevents.MessageEvent{
		Channel:   "D123",
		User:      "U456",
		Text:      "hello",
		TimeStamp: "1234567890.123456",
	}

	handler.handleMessage(ev)

	// With sync mode enabled, handler should be called immediately
	assert.True(t, handlerCalled)
}
