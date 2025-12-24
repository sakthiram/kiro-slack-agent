package processor

import (
	"context"
	"strings"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"go.uber.org/zap"
)

// BeadsManager defines the interface for beads issue management operations.
type BeadsManager interface {
	EnsureUserDir(ctx context.Context, userID string) (string, error)
	CreateFeature(ctx context.Context, userID string, thread *beads.ThreadInfo, title, description string) (*beads.Issue, error)
	CreateTask(ctx context.Context, userID, parentID string, thread *beads.ThreadInfo, title, desc string) (*beads.Issue, error)
	FindThreadIssue(ctx context.Context, userID string, thread *beads.ThreadInfo) (*beads.Issue, error)
	UpdateThreadIssue(ctx context.Context, userID, issueID, role, message string) error
}

// Syncer provides an interface for registering issues for comment synchronization.
type Syncer interface {
	RegisterIssue(issueID string, userID string, thread *beads.ThreadInfo)
}

// FeatureProcessor handles async Slack-Beads architecture for features and tasks.
// Main posts create Feature issues, thread replies create Task issues under the parent.
type FeatureProcessor struct {
	beadsMgr    BeadsManager
	syncer      Syncer
	slackClient slack.ClientInterface
	cfg         *config.Config
	logger      *zap.Logger
}

// NewFeatureProcessor creates a new FeatureProcessor with the given dependencies.
func NewFeatureProcessor(
	beadsMgr BeadsManager,
	syncer Syncer,
	slackClient slack.ClientInterface,
	cfg *config.Config,
	logger *zap.Logger,
) *FeatureProcessor {
	return &FeatureProcessor{
		beadsMgr:    beadsMgr,
		syncer:      syncer,
		slackClient: slackClient,
		cfg:         cfg,
		logger:      logger,
	}
}

// ProcessMainPost handles new channel/DM posts (not thread replies).
// Creates a Feature issue in beads and registers it for sync.
// Returns quickly - async processing happens via worker pool.
func (p *FeatureProcessor) ProcessMainPost(ctx context.Context, msg *slack.MessageEvent) error {
	logger := p.logger.With(
		zap.String("channel_id", msg.ChannelID),
		zap.String("message_ts", msg.MessageTS),
		zap.String("user_id", msg.UserID),
	)

	// Ensure user's beads directory exists
	_, err := p.beadsMgr.EnsureUserDir(ctx, msg.UserID)
	if err != nil {
		logger.Error("failed to ensure user directory", zap.Error(err))
		return err
	}

	// Build thread info - for main posts, thread TS is the message TS
	thread := &beads.ThreadInfo{
		ChannelID: msg.ChannelID,
		ThreadTS:  msg.MessageTS,
		MessageTS: msg.MessageTS, // Same as ThreadTS for main posts
		UserID:    msg.UserID,
	}

	// Extract title from message
	title := extractTitle(msg.Text)

	// Create Feature issue with description
	feature, err := p.beadsMgr.CreateFeature(ctx, msg.UserID, thread, title, msg.Text)
	if err != nil {
		logger.Error("failed to create feature", zap.Error(err))
		return err
	}

	logger.Info("created feature issue",
		zap.String("issue_id", feature.ID),
		zap.String("title", title),
	)

	// Add user message as comment
	if err := p.beadsMgr.UpdateThreadIssue(ctx, msg.UserID, feature.ID, "user", msg.Text); err != nil {
		logger.Warn("failed to add user comment", zap.Error(err))
		// Non-fatal - continue
	}

	// Register issue for comment sync
	p.syncer.RegisterIssue(feature.ID, msg.UserID, thread)

	logger.Debug("registered feature for sync", zap.String("issue_id", feature.ID))

	return nil
}

// ProcessThreadReply handles thread replies.
// Creates a Task issue under the parent Feature and registers it for sync.
// Returns quickly - async processing happens via worker pool.
func (p *FeatureProcessor) ProcessThreadReply(ctx context.Context, msg *slack.MessageEvent) error {
	logger := p.logger.With(
		zap.String("channel_id", msg.ChannelID),
		zap.String("thread_ts", msg.ThreadTS),
		zap.String("message_ts", msg.MessageTS),
		zap.String("user_id", msg.UserID),
	)

	// Ensure user's beads directory exists
	_, err := p.beadsMgr.EnsureUserDir(ctx, msg.UserID)
	if err != nil {
		logger.Error("failed to ensure user directory", zap.Error(err))
		return err
	}

	// Build parent thread info to find the Feature issue
	parentThread := &beads.ThreadInfo{
		ChannelID: msg.ChannelID,
		ThreadTS:  msg.ThreadTS, // Parent thread TS
		UserID:    msg.UserID,
	}

	// Find parent Feature issue
	parentIssue, err := p.beadsMgr.FindThreadIssue(ctx, msg.UserID, parentThread)
	if err != nil {
		logger.Error("failed to find parent feature", zap.Error(err))
		return err
	}

	if parentIssue == nil {
		logger.Warn("parent feature not found - creating new feature for orphaned thread reply")
		// Create a feature for this orphaned thread reply
		return p.ProcessMainPost(ctx, msg)
	}

	// Build thread info for this reply (using parent thread TS for grouping)
	replyThread := &beads.ThreadInfo{
		ChannelID: msg.ChannelID,
		ThreadTS:  msg.ThreadTS,  // Parent thread TS for grouping
		MessageTS: msg.MessageTS, // This reply's TS for deduplication/deep linking
		UserID:    msg.UserID,
	}

	// Extract title from message
	title := extractTitle(msg.Text)

	// Create Task issue under parent Feature (with msg.Text as description)
	task, err := p.beadsMgr.CreateTask(ctx, msg.UserID, parentIssue.ID, replyThread, title, msg.Text)
	if err != nil {
		logger.Error("failed to create task",
			zap.String("parent_id", parentIssue.ID),
			zap.Error(err),
		)
		return err
	}

	logger.Info("created task issue",
		zap.String("issue_id", task.ID),
		zap.String("parent_id", parentIssue.ID),
		zap.String("title", title),
	)

	// Add user message as comment
	if err := p.beadsMgr.UpdateThreadIssue(ctx, msg.UserID, task.ID, "user", msg.Text); err != nil {
		logger.Warn("failed to add user comment", zap.Error(err))
		// Non-fatal - continue
	}

	// Register task for comment sync
	p.syncer.RegisterIssue(task.ID, msg.UserID, replyThread)

	logger.Debug("registered task for sync", zap.String("issue_id", task.ID))

	return nil
}

// extractTitle extracts a title from message text.
// Takes the first line or truncates to 80 characters maximum.
func extractTitle(text string) string {
	// Get first line
	lines := strings.SplitN(text, "\n", 2)
	title := strings.TrimSpace(lines[0])

	// Truncate if too long
	if len(title) > 80 {
		title = title[:77] + "..."
	}

	// If empty, use a default
	if title == "" {
		title = "New conversation"
	}

	return title
}

// isThreadReply returns true if the message is a thread reply (not the thread root).
func isThreadReply(msg *slack.MessageEvent) bool {
	return msg.ThreadTS != "" && msg.ThreadTS != msg.MessageTS
}
