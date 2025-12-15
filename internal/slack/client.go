package slack

import (
	"context"
	"fmt"

	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// Client wraps slack-go for cleaner API and testability.
type Client struct {
	api       *slack.Client
	botUserID string
	logger    *zap.Logger
}

// ClientInterface defines the Slack client methods for testing.
type ClientInterface interface {
	PostMessage(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error)
	UpdateMessage(ctx context.Context, channelID, ts, text string) error
	AddReaction(ctx context.Context, channelID, ts, emoji string) error
	RemoveReaction(ctx context.Context, channelID, ts, emoji string) error
	GetBotUserID() string
}

// messageConfig holds message configuration.
type messageConfig struct {
	threadTS string
	blocks   []slack.Block
}

// MessageOption configures message sending.
type MessageOption func(*messageConfig)

// WithThreadTS sets the thread timestamp to reply in a thread.
func WithThreadTS(ts string) MessageOption {
	return func(cfg *messageConfig) {
		cfg.threadTS = ts
	}
}

// WithBlocks sets Slack blocks for rich formatting.
func WithBlocks(blocks []slack.Block) MessageOption {
	return func(cfg *messageConfig) {
		cfg.blocks = blocks
	}
}

// NewClient creates a new Slack client.
func NewClient(botToken, appToken string, debug bool, logger *zap.Logger) (*Client, error) {
	api := slack.New(botToken, slack.OptionDebug(debug))

	// Get bot user ID via auth.test
	authResp, err := api.AuthTest()
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate with Slack: %w", err)
	}

	return &Client{
		api:       api,
		botUserID: authResp.UserID,
		logger:    logger,
	}, nil
}

// PostMessage sends a new message, optionally in a thread.
func (c *Client) PostMessage(ctx context.Context, channelID, text string, opts ...MessageOption) (string, error) {
	cfg := &messageConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	msgOpts := []slack.MsgOption{
		slack.MsgOptionText(text, false),
	}

	if cfg.threadTS != "" {
		msgOpts = append(msgOpts, slack.MsgOptionTS(cfg.threadTS))
	}

	if len(cfg.blocks) > 0 {
		msgOpts = append(msgOpts, slack.MsgOptionBlocks(cfg.blocks...))
	}

	_, ts, err := c.api.PostMessageContext(ctx, channelID, msgOpts...)
	if err != nil {
		c.logger.Error("failed to post message",
			zap.String("channel_id", channelID),
			zap.Error(err))
		return "", fmt.Errorf("failed to post message: %w", err)
	}

	return ts, nil
}

// UpdateMessage updates an existing message (for streaming).
func (c *Client) UpdateMessage(ctx context.Context, channelID, ts, text string) error {
	_, _, _, err := c.api.UpdateMessageContext(ctx, channelID, ts,
		slack.MsgOptionText(text, false))
	if err != nil {
		c.logger.Error("failed to update message",
			zap.String("channel_id", channelID),
			zap.String("ts", ts),
			zap.Error(err))
		return fmt.Errorf("failed to update message: %w", err)
	}
	return nil
}

// AddReaction adds an emoji reaction to a message.
func (c *Client) AddReaction(ctx context.Context, channelID, ts, emoji string) error {
	err := c.api.AddReactionContext(ctx, emoji, slack.ItemRef{
		Channel:   channelID,
		Timestamp: ts,
	})
	if err != nil {
		// Ignore "already_reacted" errors
		if err.Error() != "already_reacted" {
			c.logger.Error("failed to add reaction",
				zap.String("channel_id", channelID),
				zap.String("ts", ts),
				zap.String("emoji", emoji),
				zap.Error(err))
			return fmt.Errorf("failed to add reaction: %w", err)
		}
	}
	return nil
}

// RemoveReaction removes an emoji reaction from a message.
func (c *Client) RemoveReaction(ctx context.Context, channelID, ts, emoji string) error {
	err := c.api.RemoveReactionContext(ctx, emoji, slack.ItemRef{
		Channel:   channelID,
		Timestamp: ts,
	})
	if err != nil {
		// Ignore "no_reaction" errors
		if err.Error() != "no_reaction" {
			c.logger.Error("failed to remove reaction",
				zap.String("channel_id", channelID),
				zap.String("ts", ts),
				zap.String("emoji", emoji),
				zap.Error(err))
			return fmt.Errorf("failed to remove reaction: %w", err)
		}
	}
	return nil
}

// GetBotUserID returns the bot's user ID for mention detection.
func (c *Client) GetBotUserID() string {
	return c.botUserID
}
