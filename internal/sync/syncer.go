package sync

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"go.uber.org/zap"
)

// BeadsManager defines the interface for beads operations used by the syncer.
type BeadsManager interface {
	GetConversationContext(ctx context.Context, userID, issueID string) ([]beads.Message, error)
	AddLabel(ctx context.Context, userID, issueID, label string) error
	HasLabel(ctx context.Context, userID, issueID, label string) bool
	ListUserDirs() []string
	ListIssuesByStatus(ctx context.Context, userID string, statuses []string) ([]*beads.Issue, error)
	GetThreadTaskCounts(ctx context.Context, userID, threadTS string) (open, inProgress, closed int, err error)
}

// CommentSyncer synchronizes agent comments from Beads issues to Slack threads.
// It only syncs comments with the "[agent]" prefix.
type CommentSyncer struct {
	tracker      *Tracker
	beadsMgr     BeadsManager
	slackClient  slack.ClientInterface
	logger       *zap.Logger
	syncLoopDone chan struct{}

	// In-memory tracking to prevent race conditions between worker sync and sync loop
	syncedComments map[string]struct{} // key: "issueID:commentID"
	syncMu         sync.Mutex
}

// NewCommentSyncer creates a new comment syncer.
func NewCommentSyncer(
	beadsMgr BeadsManager,
	slackClient slack.ClientInterface,
	logger *zap.Logger,
) *CommentSyncer {
	return &CommentSyncer{
		tracker:        NewTracker(),
		beadsMgr:       beadsMgr,
		slackClient:    slackClient,
		logger:         logger,
		syncLoopDone:   make(chan struct{}),
		syncedComments: make(map[string]struct{}),
	}
}

// ReactTo adds an emoji reaction to a message, removing an old one if specified.
func (s *CommentSyncer) ReactTo(ctx context.Context, channelID, messageTS, emoji, removeEmoji string) {
	if removeEmoji != "" {
		_ = s.slackClient.RemoveReaction(ctx, channelID, messageTS, removeEmoji)
	}
	if emoji != "" {
		if err := s.slackClient.AddReaction(ctx, channelID, messageTS, emoji); err != nil {
			s.logger.Debug("failed to add reaction", zap.String("emoji", emoji), zap.Error(err))
		}
	}
}

// RemoveReaction removes an emoji reaction from a message.
func (s *CommentSyncer) RemoveReaction(ctx context.Context, channelID, messageTS, emoji string) error {
	return s.slackClient.RemoveReaction(ctx, channelID, messageTS, emoji)
}

// PostInThread posts a message in a Slack thread and returns the message TS.
func (s *CommentSyncer) PostInThread(ctx context.Context, channelID, threadTS, text string) (string, error) {
	return s.slackClient.PostMessage(ctx, channelID, text, slack.WithThreadTS(threadTS))
}

// UpdateMessage updates an existing Slack message in place.
func (s *CommentSyncer) UpdateMessage(ctx context.Context, channelID, ts, text string) error {
	return s.slackClient.UpdateMessage(ctx, channelID, ts, text)
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

// IsRegistered checks if an issue is registered for sync.
func (s *CommentSyncer) IsRegistered(issueID string) bool {
	return s.tracker.GetState(issueID) != nil
}

// Unregister removes an issue from synchronization tracking.
// This should be called when an issue is closed.
func (s *CommentSyncer) Unregister(issueID string) {
	s.tracker.Unregister(issueID)
	s.logger.Info("unregistered issue from sync", zap.String("issue_id", issueID))
}

// SyncIssue synchronizes new agent comments for a specific issue to Slack.
// It only syncs comments that:
// 1. Have the "[agent]" prefix (assistant role)
// 2. Have not been previously synced (checked via beads labels: synced:<comment_id>)
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
	for _, msg := range messages {
		// Only sync assistant messages (agent responses)
		if msg.Role != "assistant" {
			continue
		}

		// Generate a stable comment ID based on role and timestamp
		// Using UnixNano() for higher precision and msg.Role for stability
		commentID := fmt.Sprintf("%s_%d", msg.Role, msg.Timestamp.UnixNano())
		syncKey := issueID + ":" + commentID

		// Check in-memory tracking first (prevents race conditions)
		s.syncMu.Lock()
		if _, alreadySynced := s.syncedComments[syncKey]; alreadySynced {
			s.syncMu.Unlock()
			continue // Already synced in-memory
		}
		// Mark as syncing immediately to prevent duplicates
		s.syncedComments[syncKey] = struct{}{}
		s.syncMu.Unlock()

		// Also check beads label (for persistence across restarts)
		syncedLabel := "synced:" + commentID
		if s.beadsMgr.HasLabel(ctx, state.UserID, issueID, syncedLabel) {
			continue // Already synced in beads
		}

		// Post the comment to Slack with task ID footer and thread stats
		// Note: counts may be off by 1 since the current task might not be closed yet
		footer := fmt.Sprintf("\n\n> 🏷️ `%s`", issueID)
		if open, inProg, done, countErr := s.beadsMgr.GetThreadTaskCounts(ctx, state.UserID, state.SlackThreadTS); countErr == nil && (open+inProg+done) > 0 {
			footer += fmt.Sprintf("\n> 👀 %d  ⏳ %d  ✅ %d", open, inProg, done)
		}
		content := msg.Content + footer
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
			// Remove from in-memory tracking on failure so it can be retried
			s.syncMu.Lock()
			delete(s.syncedComments, syncKey)
			s.syncMu.Unlock()
			continue
		}

		// Mark as synced via beads label (for persistence)
		if err := s.beadsMgr.AddLabel(ctx, state.UserID, issueID, syncedLabel); err != nil {
			s.logger.Warn("failed to mark comment as synced",
				zap.String("issue_id", issueID),
				zap.String("comment_id", commentID),
				zap.Error(err))
		}
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
			// Unregister issues that no longer exist in bd (e.g. after migration)
			if strings.Contains(err.Error(), "failed to get issue") {
				s.logger.Warn("unregistering stale issue from sync tracker",
					zap.String("issue_id", issueID),
				)
				s.tracker.Unregister(issueID)
			}
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

// Restore rebuilds sync state from beads on startup.
// Scans all user directories for issues with status in_progress/ready/open
// and re-registers them using their thread:/channel:/user: labels.
func (s *CommentSyncer) Restore(ctx context.Context) error {
	s.logger.Info("restoring sync state from beads")

	// Get all user directories
	userIDs := s.beadsMgr.ListUserDirs()
	if len(userIDs) == 0 {
		s.logger.Info("no user directories found, nothing to restore")
		return nil
	}

	s.logger.Info("scanning user directories for issues", zap.Int("user_count", len(userIDs)))

	// Track statistics
	totalIssues := 0
	restoredIssues := 0
	skippedIssues := 0

	// Scan each user's issues
	for _, userID := range userIDs {
		issues, err := s.beadsMgr.ListIssuesByStatus(ctx, userID, []string{"open", "in_progress", "ready"})
		if err != nil {
			s.logger.Warn("failed to list issues for user",
				zap.String("user_id", userID),
				zap.Error(err),
			)
			continue
		}

		totalIssues += len(issues)

		// Process each issue
		for _, issue := range issues {
			// Extract thread information from labels
			threadTS := extractLabelValue(issue.Labels, "thread:")
			channelID := extractLabelValue(issue.Labels, "channel:")
			labelUserID := extractLabelValue(issue.Labels, "user:")

			// Skip if missing required labels
			if threadTS == "" || channelID == "" || labelUserID == "" {
				s.logger.Debug("skipping issue without required labels",
					zap.String("issue_id", issue.ID),
					zap.String("user_id", userID),
					zap.Bool("has_thread", threadTS != ""),
					zap.Bool("has_channel", channelID != ""),
					zap.Bool("has_user", labelUserID != ""),
				)
				skippedIssues++
				continue
			}

			// Re-register the issue
			threadInfo := &beads.ThreadInfo{
				ChannelID: channelID,
				ThreadTS:  threadTS,
				UserID:    labelUserID,
			}

			s.RegisterIssue(issue.ID, userID, threadInfo)
			restoredIssues++

			s.logger.Debug("restored issue",
				zap.String("issue_id", issue.ID),
				zap.String("user_id", userID),
				zap.String("thread_ts", threadTS),
				zap.String("channel_id", channelID),
			)
		}
	}

	s.logger.Info("restore complete",
		zap.Int("total_issues", totalIssues),
		zap.Int("restored", restoredIssues),
		zap.Int("skipped", skippedIssues),
	)

	return nil
}

// extractLabelValue extracts the value from a label with the given prefix.
// For example, extractLabelValue(["thread:123", "channel:C456"], "thread:") returns "123".
func extractLabelValue(labels []string, prefix string) string {
	for _, label := range labels {
		if strings.HasPrefix(label, prefix) {
			return strings.TrimPrefix(label, prefix)
		}
	}
	return ""
}
