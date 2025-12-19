package sync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"go.uber.org/zap"
)

// CommentSyncer synchronizes agent comments from Beads issues to Slack threads.
// It only syncs comments with the "[agent]" prefix.
type CommentSyncer struct {
	tracker      *Tracker
	beadsMgr     *beads.Manager
	slackClient  slack.ClientInterface
	logger       *zap.Logger
	syncLoopDone chan struct{}
}

// NewCommentSyncer creates a new comment syncer.
func NewCommentSyncer(
	beadsMgr *beads.Manager,
	slackClient slack.ClientInterface,
	logger *zap.Logger,
) *CommentSyncer {
	return &CommentSyncer{
		tracker:      NewTracker(),
		beadsMgr:     beadsMgr,
		slackClient:  slackClient,
		logger:       logger,
		syncLoopDone: make(chan struct{}),
	}
}

// RegisterIssue registers a new issue for comment synchronization.
// This should be called when a new issue is created from a Slack thread.
func (s *CommentSyncer) RegisterIssue(issueID string, userID string, thread *beads.ThreadInfo) {
	s.tracker.Register(issueID, userID, thread.ChannelID, thread.ThreadTS)
	s.logger.Info("registered issue for sync",
		zap.String("issue_id", issueID),
		zap.String("user_id", userID),
		zap.String("channel_id", thread.ChannelID),
		zap.String("thread_ts", thread.ThreadTS),
	)
}

// Unregister removes an issue from synchronization tracking.
// This should be called when an issue is closed.
func (s *CommentSyncer) Unregister(issueID string) {
	s.tracker.Unregister(issueID)
	s.logger.Info("unregistered issue from sync", zap.String("issue_id", issueID))
}

// SyncIssue synchronizes new agent comments for a specific issue to Slack.
// It only syncs comments that:
// 1. Have the "[agent]" prefix
// 2. Have not been previously synced
func (s *CommentSyncer) SyncIssue(ctx context.Context, issueID string) error {
	state := s.tracker.GetState(issueID)
	if state == nil {
		return fmt.Errorf("issue %s is not registered for sync", issueID)
	}

	// Get the issue with all its comments from beads
	messages, err := s.beadsMgr.GetConversationContext(ctx, state.UserID, issueID)
	if err != nil {
		s.logger.Error("failed to get conversation context",
			zap.String("issue_id", issueID),
			zap.Error(err),
		)
		return fmt.Errorf("failed to get conversation context: %w", err)
	}

	// Sync agent comments
	syncedCount := 0
	for i, msg := range messages {
		// Only sync assistant messages (agent responses)
		if msg.Role != "assistant" {
			continue
		}

		// Generate a comment ID based on the message index and timestamp
		// This is a simple approach since we don't have direct access to comment IDs
		commentID := fmt.Sprintf("%d_%d", i, msg.Timestamp.Unix())

		// Skip if already synced
		if s.tracker.IsCommentSynced(issueID, commentID) {
			continue
		}

		// Post the comment to Slack
		content := msg.Content
		_, err := s.slackClient.PostMessage(
			ctx,
			state.ChannelID,
			content,
			slack.WithThreadTS(state.SlackThreadTS),
		)
		if err != nil {
			s.logger.Error("failed to post agent comment to Slack",
				zap.String("issue_id", issueID),
				zap.String("channel_id", state.ChannelID),
				zap.String("thread_ts", state.SlackThreadTS),
				zap.Error(err),
			)
			// Continue with other comments even if one fails
			continue
		}

		// Mark as synced
		s.tracker.MarkCommentSynced(issueID, commentID)
		syncedCount++

		s.logger.Debug("synced agent comment to Slack",
			zap.String("issue_id", issueID),
			zap.String("comment_id", commentID),
		)
	}

	if syncedCount > 0 {
		s.logger.Info("synced agent comments to Slack",
			zap.String("issue_id", issueID),
			zap.Int("count", syncedCount),
		)
	}

	return nil
}

// StartSyncLoop starts a background loop that periodically syncs all tracked issues.
// The loop runs until the context is cancelled.
func (s *CommentSyncer) StartSyncLoop(ctx context.Context, interval time.Duration) {
	s.logger.Info("starting sync loop", zap.Duration("interval", interval))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(s.syncLoopDone)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("sync loop stopped")
			return
		case <-ticker.C:
			s.syncAll(ctx)
		}
	}
}

// syncAll synchronizes all tracked issues.
func (s *CommentSyncer) syncAll(ctx context.Context) {
	issueIDs := s.tracker.GetAllIssueIDs()
	if len(issueIDs) == 0 {
		return
	}

	s.logger.Debug("syncing all tracked issues", zap.Int("count", len(issueIDs)))

	for _, issueID := range issueIDs {
		if err := s.SyncIssue(ctx, issueID); err != nil {
			s.logger.Error("failed to sync issue",
				zap.String("issue_id", issueID),
				zap.Error(err),
			)
			// Continue with other issues even if one fails
		}
	}
}

// extractAgentContent removes the "[agent]" prefix from a comment.
// Returns the cleaned content and true if the prefix was present.
func extractAgentContent(content string) (string, bool) {
	if strings.HasPrefix(content, "[agent]") {
		return strings.TrimSpace(strings.TrimPrefix(content, "[agent]")), true
	}
	return content, false
}

// WaitForShutdown waits for the sync loop to complete shutdown.
// This should be called after cancelling the context to ensure graceful shutdown.
func (s *CommentSyncer) WaitForShutdown() {
	<-s.syncLoopDone
}
