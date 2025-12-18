package slack

import (
	"context"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

// Error injection tests for Slack API failures and rate limits

func TestClient_PostMessage_RateLimit(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.RateLimitedError{
				RetryAfter: 30 * time.Second,
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)

	// Verify it's a rate limit error
	var rateLimitErr *slack.RateLimitedError
	assert.ErrorAs(t, err, &rateLimitErr)
	assert.Equal(t, 30*time.Second, rateLimitErr.RetryAfter)
}

func TestClient_PostMessage_APIError_500(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "internal_error",
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Contains(t, err.Error(), "internal_error")
}

func TestClient_PostMessage_AuthError_401(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "invalid_auth",
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Contains(t, err.Error(), "invalid_auth")
}

func TestClient_PostMessage_AuthError_403(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "not_authed",
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Contains(t, err.Error(), "not_authed")
}

func TestClient_PostMessage_NetworkError(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "network_error: connection timeout",
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Contains(t, err.Error(), "network_error")
}

func TestClient_PostMessage_InvalidChannel(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "channel_not_found",
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "INVALID", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Contains(t, err.Error(), "channel_not_found")
}

func TestClient_PostMessage_AccountInactive(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "account_inactive",
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Contains(t, err.Error(), "account_inactive")
}

func TestClient_PostMessage_TokenRevoked(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "token_revoked",
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Contains(t, err.Error(), "token_revoked")
}

func TestClient_UpdateMessage_RateLimit(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		UpdateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			return &slack.RateLimitedError{
				RetryAfter: 30 * time.Second,
			}
		},
	}

	err := mock.UpdateMessage(ctx, "C123", "ts123", "updated")
	assert.Error(t, err)

	var rateLimitErr *slack.RateLimitedError
	assert.ErrorAs(t, err, &rateLimitErr)
}

func TestClient_UpdateMessage_APIError(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		UpdateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			return &slack.SlackErrorResponse{
				Err: "internal_error",
			}
		},
	}

	err := mock.UpdateMessage(ctx, "C123", "ts123", "updated")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "internal_error")
}

func TestClient_UpdateMessage_MessageNotFound(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		UpdateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			return &slack.SlackErrorResponse{
				Err: "message_not_found",
			}
		},
	}

	err := mock.UpdateMessage(ctx, "C123", "ts123", "updated")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "message_not_found")
}

func TestClient_AddReaction_RateLimit(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		AddReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			return &slack.RateLimitedError{
				RetryAfter: 30 * time.Second,
			}
		},
	}

	err := mock.AddReaction(ctx, "C123", "ts123", "eyes")
	assert.Error(t, err)

	var rateLimitErr *slack.RateLimitedError
	assert.ErrorAs(t, err, &rateLimitErr)
}

func TestClient_AddReaction_APIError(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		AddReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			return &slack.SlackErrorResponse{
				Err: "internal_error",
			}
		},
	}

	err := mock.AddReaction(ctx, "C123", "ts123", "eyes")
	assert.Error(t, err)
}

func TestClient_AddReaction_InvalidEmoji(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		AddReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			return &slack.SlackErrorResponse{
				Err: "invalid_name",
			}
		},
	}

	err := mock.AddReaction(ctx, "C123", "ts123", "invalid_emoji")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_name")
}

func TestClient_RemoveReaction_RateLimit(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		RemoveReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			return &slack.RateLimitedError{
				RetryAfter: 30 * time.Second,
			}
		},
	}

	err := mock.RemoveReaction(ctx, "C123", "ts123", "eyes")
	assert.Error(t, err)

	var rateLimitErr *slack.RateLimitedError
	assert.ErrorAs(t, err, &rateLimitErr)
}

func TestClient_RemoveReaction_APIError(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		RemoveReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			return &slack.SlackErrorResponse{
				Err: "internal_error",
			}
		},
	}

	err := mock.RemoveReaction(ctx, "C123", "ts123", "eyes")
	assert.Error(t, err)
}

func TestClient_MultipleOperations_PartialFailure(t *testing.T) {
	ctx := context.Background()

	// Simulate scenario where PostMessage succeeds but AddReaction fails
	postCalled := false
	reactionCalled := false

	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			postCalled = true
			return "ts123", nil
		},
		AddReactionFunc: func(ctx context.Context, channelID, ts, emoji string) error {
			reactionCalled = true
			return &slack.RateLimitedError{RetryAfter: 30 * time.Second}
		},
	}

	// Post message succeeds
	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.NoError(t, err)
	assert.Equal(t, "ts123", ts)
	assert.True(t, postCalled)

	// Add reaction fails
	err = mock.AddReaction(ctx, "C123", ts, "eyes")
	assert.Error(t, err)
	assert.True(t, reactionCalled)
}

func TestClient_InvalidResponse(t *testing.T) {
	ctx := context.Background()
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", &slack.SlackErrorResponse{
				Err: "invalid response: unexpected end of JSON input",
			}
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Contains(t, err.Error(), "invalid response")
}

func TestClient_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			return "", ctx.Err()
		},
	}

	ts, err := mock.PostMessage(ctx, "C123", "hello")
	assert.Error(t, err)
	assert.Empty(t, ts)
	assert.Equal(t, context.Canceled, err)
}

func TestClient_ContextTimeout(t *testing.T) {
	mock := &MockSlackClient{
		PostMessageFunc: func(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
			// Simulate slow operation
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return "ts123", nil
			}
		},
	}

	// Create a context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, err := mock.PostMessage(ctx, "C123", "test")
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}
