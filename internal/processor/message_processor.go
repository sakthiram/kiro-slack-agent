package processor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/kiro"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"github.com/sakthiram/kiro-slack-agent/internal/streaming"
	"go.uber.org/zap"
)

// BeadsManager provides an interface for beads context management.
type BeadsManager interface {
	EnsureUserDir(ctx context.Context, userID string) (string, error)
	GetUserDir(userID string) string
	FindThreadIssue(ctx context.Context, userID string, thread *beads.ThreadInfo) (*beads.Issue, error)
	CreateThreadIssue(ctx context.Context, userID string, thread *beads.ThreadInfo, message string) (*beads.Issue, error)
	UpdateThreadIssue(ctx context.Context, userID, issueID, role, message string) error
	GetConversationContext(ctx context.Context, userID, issueID string) ([]beads.Message, error)
}

// MessageProcessor handles processing of incoming Slack messages.
// Creates a fresh Kiro process for each message and kills it after response.
// Context is reconstructed from beads on each message - no persistent sessions needed.
type MessageProcessor struct {
	slackClient slack.ClientInterface
	beadsMgr    BeadsManager
	cfg         *config.Config
	logger      *zap.Logger
}

// NewMessageProcessor creates a new MessageProcessor with the given dependencies.
func NewMessageProcessor(
	slackClient slack.ClientInterface,
	beadsMgr BeadsManager,
	cfg *config.Config,
	logger *zap.Logger,
) *MessageProcessor {
	return &MessageProcessor{
		slackClient: slackClient,
		beadsMgr:    beadsMgr,
		cfg:         cfg,
		logger:      logger,
	}
}

// ProcessMessage handles a message from Slack.
// Creates a fresh Kiro process, sends message with context, and kills process after response.
func (p *MessageProcessor) ProcessMessage(
	ctx context.Context,
	msg *slack.MessageEvent,
) error {
	logger := p.logger.With(
		zap.String("channel_id", msg.ChannelID),
		zap.String("thread_ts", msg.ThreadTS),
		zap.String("user_id", msg.UserID),
	)

	// Determine thread TS (use message TS if no thread - this becomes the thread root)
	threadTS := msg.ThreadTS
	if threadTS == "" {
		threadTS = msg.MessageTS
	}

	// 1. Ensure user's beads directory exists (runs bd init if needed)
	userDir, err := p.beadsMgr.EnsureUserDir(ctx, msg.UserID)
	if err != nil {
		logger.Error("failed to ensure user directory", zap.Error(err))
		p.slackClient.PostMessage(ctx, msg.ChannelID, ":x: Error: Unable to initialize user session. Please try again.",
			slack.WithThreadTS(threadTS))
		return err
	}

	// 2. Build thread info for labeling
	thread := &beads.ThreadInfo{
		ChannelID: msg.ChannelID,
		ThreadTS:  threadTS,
		UserID:    msg.UserID,
	}

	// 3. Find or create bd issue for this thread
	issue, err := p.beadsMgr.FindThreadIssue(ctx, msg.UserID, thread)
	if err != nil {
		logger.Error("failed to find thread issue", zap.Error(err))
	}

	var issueID string
	if issue == nil {
		// Create new issue with labels
		newIssue, err := p.beadsMgr.CreateThreadIssue(ctx, msg.UserID, thread, msg.Text)
		if err != nil {
			logger.Error("failed to create thread issue", zap.Error(err))
			// Continue without issue tracking - not fatal
		} else {
			issueID = newIssue.ID
		}
	} else {
		issueID = issue.ID
		// Update existing issue with new user message
		if err := p.beadsMgr.UpdateThreadIssue(ctx, msg.UserID, issueID, "user", msg.Text); err != nil {
			logger.Warn("failed to update thread issue", zap.Error(err))
		}
	}

	// 4. Get conversation context from beads
	var contextMessages []beads.Message
	if issueID != "" {
		contextMessages, err = p.beadsMgr.GetConversationContext(ctx, msg.UserID, issueID)
		if err != nil {
			logger.Warn("failed to get conversation context", zap.Error(err))
		}
	}

	// 5. Create streamer for this response
	streamer := streaming.NewStreamer(p.slackClient, &p.cfg.Streaming, logger)

	// Start streaming response
	_, err = streamer.Start(ctx, msg.ChannelID, threadTS)
	if err != nil {
		logger.Error("failed to start streamer", zap.Error(err))
		return err
	}

	// 6. Create fresh Kiro process for this message
	bridge := kiro.NewProcess(userDir, &p.cfg.Kiro, logger)

	// Wrap with retry bridge for resilience
	var retryBridge kiro.Bridge = kiro.NewRetryBridge(bridge, p.cfg.Kiro.MaxRetries, logger)

	if err := retryBridge.Start(ctx); err != nil {
		logger.Error("failed to start Kiro", zap.Error(err))
		streamer.Error(ctx, err)
		return err
	}

	// IMPORTANT: Always close the bridge when done - no persistent sessions
	defer func() {
		logger.Debug("closing Kiro process after response")
		bridge.Close()
	}()

	logger.Info("started fresh Kiro process", zap.String("user_dir", userDir))

	// 7. Build contextual prompt with conversation history
	contextualMessage := buildContextualPrompt(contextMessages, msg.Text)

	// 8. Send message to Kiro and stream response
	var finalResponse string
	err = bridge.SendMessage(ctx, contextualMessage, func(chunk string, isComplete bool) {
		finalResponse = chunk
		if !isComplete {
			streamer.Update(ctx, chunk)
		}
	})

	if err != nil {
		logger.Error("Kiro error", zap.Error(err))
		streamer.Error(ctx, err)
		return err
	}

	// 9. Store response in beads issue
	if issueID != "" {
		if err := p.beadsMgr.UpdateThreadIssue(ctx, msg.UserID, issueID, "assistant", finalResponse); err != nil {
			logger.Warn("failed to store response in beads", zap.Error(err))
		}
	}

	// 10. Complete streaming with final response
	if err := streamer.Complete(ctx, finalResponse); err != nil {
		logger.Error("failed to complete streamer", zap.Error(err))
		return err
	}

	return nil
}

// buildContextualPrompt builds a prompt with conversation history context.
func buildContextualPrompt(messages []beads.Message, currentMessage string) string {
	if len(messages) == 0 {
		return currentMessage
	}

	var sb strings.Builder

	// Add conversation history
	sb.WriteString("Previous conversation context:\n")
	sb.WriteString("---\n")
	for _, msg := range messages {
		role := "User"
		if msg.Role == "assistant" {
			role = "Assistant"
		}
		// Truncate long messages in context
		content := msg.Content
		if len(content) > 500 {
			content = content[:497] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n\n", role, content))
	}
	sb.WriteString("---\n\n")

	// Add current message
	sb.WriteString("Current message:\n")
	sb.WriteString(currentMessage)

	return sb.String()
}
