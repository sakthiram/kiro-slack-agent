package slack

import (
	"context"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// Error injection tests for handler with Slack API failures

func TestHandler_MessageEvent_SlackAPIFailure_PostMessage(t *testing.T) {
	logger := zap.NewNop()

	done := make(chan struct{})
	var receivedError error

	mockClient := &MockSlackClient{
		BotUserID: "UBOT123",
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "internal_error",
			}
		},
	}

	handler := &Handler{
		client: &Client{botUserID: mockClient.BotUserID},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			// Try to post a message which will fail
			_, err := mockClient.PostMessage(ctx, msg.ChannelID, "response")
			receivedError = err
			close(done)
			return err
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
					Channel:   "D123",
					User:      "U456",
					Text:      "hello",
					TimeStamp: "1234567890.123456",
				},
			},
		},
	}

	err := handler.HandleEvent(evt, nil)
	assert.NoError(t, err) // Handler itself doesn't error

	// Wait for async handler
	select {
	case <-done:
		assert.Error(t, receivedError)
		assert.Contains(t, receivedError.Error(), "internal_error")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not complete in time")
	}
}

func TestHandler_MessageEvent_SlackAPIFailure_RateLimit(t *testing.T) {
	logger := zap.NewNop()

	done := make(chan struct{})
	var receivedError error

	mockClient := &MockSlackClient{
		BotUserID: "UBOT123",
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.RateLimitedError{
				RetryAfter: 30 * time.Second,
			}
		},
	}

	handler := &Handler{
		client: &Client{botUserID: mockClient.BotUserID},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			// Try to post a message which will be rate limited
			_, err := mockClient.PostMessage(ctx, msg.ChannelID, "response")
			receivedError = err
			close(done)
			return err
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

	// Wait for async handler
	select {
	case <-done:
		assert.Error(t, receivedError)
		var rateLimitErr *slack.RateLimitedError
		assert.ErrorAs(t, receivedError, &rateLimitErr)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not complete in time")
	}
}

func TestHandler_MessageEvent_SlackAPIFailure_Auth(t *testing.T) {
	logger := zap.NewNop()

	done := make(chan struct{})
	var receivedError error

	mockClient := &MockSlackClient{
		BotUserID: "UBOT123",
		AddReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			return &slack.SlackErrorResponse{
				Err: "invalid_auth",
			}
		},
	}

	handler := &Handler{
		client: &Client{botUserID: mockClient.BotUserID},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			// Try to add a reaction which will fail with auth error
			err := mockClient.AddReaction(ctx, msg.ChannelID, msg.MessageTS, "eyes")
			receivedError = err
			close(done)
			return err
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

	// Wait for async handler
	select {
	case <-done:
		assert.Error(t, receivedError)
		assert.Contains(t, receivedError.Error(), "invalid_auth")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not complete in time")
	}
}

func TestHandler_MessageEvent_NetworkError(t *testing.T) {
	logger := zap.NewNop()

	done := make(chan struct{})
	var receivedError error

	mockClient := &MockSlackClient{
		BotUserID: "UBOT123",
		UpdateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			return &slack.SlackErrorResponse{
				Err: "network_error: connection timeout",
			}
		},
	}

	handler := &Handler{
		client: &Client{botUserID: mockClient.BotUserID},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			// Try to update a message which will fail with network error
			err := mockClient.UpdateMessage(ctx, msg.ChannelID, msg.MessageTS, "updated")
			receivedError = err
			close(done)
			return err
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

	// Wait for async handler
	select {
	case <-done:
		assert.Error(t, receivedError)
		assert.Contains(t, receivedError.Error(), "network_error")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not complete in time")
	}
}

func TestHandler_AppMention_SlackAPIFailure(t *testing.T) {
	logger := zap.NewNop()

	done := make(chan struct{})
	var receivedError error

	mockClient := &MockSlackClient{
		BotUserID: "UBOT123",
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "channel_not_found",
			}
		},
	}

	handler := &Handler{
		client: &Client{botUserID: mockClient.BotUserID},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			// Try to post a message which will fail
			_, err := mockClient.PostMessage(ctx, msg.ChannelID, "response")
			receivedError = err
			close(done)
			return err
		},
		logger: logger,
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
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
		},
	}

	err := handler.HandleEvent(evt, nil)
	assert.NoError(t, err)

	// Wait for async handler
	select {
	case <-done:
		assert.Error(t, receivedError)
		assert.Contains(t, receivedError.Error(), "channel_not_found")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not complete in time")
	}
}

func TestHandler_MessageEvent_MultipleOperations_PartialFailure(t *testing.T) {
	logger := zap.NewNop()

	done := make(chan struct{})
	postCalled := false
	reactionCalled := false
	var receivedError error

	mockClient := &MockSlackClient{
		BotUserID: "UBOT123",
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			postCalled = true
			return "ts123", nil
		},
		AddReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			reactionCalled = true
			return &slack.SlackErrorResponse{
				Err: "too_many_reactions",
			}
		},
	}

	handler := &Handler{
		client: &Client{botUserID: mockClient.BotUserID},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			// Post message succeeds
			ts, err := mockClient.PostMessage(ctx, msg.ChannelID, "response")
			if err != nil {
				receivedError = err
				close(done)
				return err
			}

			// Add reaction fails
			err = mockClient.AddReaction(ctx, msg.ChannelID, ts, "eyes")
			receivedError = err
			close(done)
			return err
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

	// Wait for async handler
	select {
	case <-done:
		assert.True(t, postCalled)
		assert.True(t, reactionCalled)
		assert.Error(t, receivedError)
		assert.Contains(t, receivedError.Error(), "too_many_reactions")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not complete in time")
	}
}

func TestHandler_MessageEvent_ContextCancellation(t *testing.T) {
	logger := zap.NewNop()

	done := make(chan struct{})
	var receivedError error

	mockClient := &MockSlackClient{
		BotUserID: "UBOT123",
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", ctx.Err()
		},
	}

	handler := &Handler{
		client: &Client{botUserID: mockClient.BotUserID},
		messageHandler: func(ctx context.Context, msg *MessageEvent) error {
			// Cancel the context immediately
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel()

			// Try to post a message with cancelled context
			_, err := mockClient.PostMessage(cancelCtx, msg.ChannelID, "response")
			receivedError = err
			close(done)
			return err
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

	// Wait for async handler
	select {
	case <-done:
		assert.Error(t, receivedError)
		assert.Equal(t, context.Canceled, receivedError)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not complete in time")
	}
}
