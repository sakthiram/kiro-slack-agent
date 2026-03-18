package processor

import (
	"context"
	"fmt"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/status"
	"go.uber.org/zap"
)

// WorkerPool defines the interface for cancelling running tasks.
type WorkerPool interface {
	CancelTask(issueID string) bool
	ResetTask(issueID string)
	BlockTask(issueID string)
	UnblockTask(issueID string)
	ForceQueue(issueID, userID string, threadInfo *beads.ThreadInfo)
}

// StatusPoster posts and updates status messages in Slack threads.
type StatusPoster interface {
	PostInThread(ctx context.Context, channelID, threadTS, text string) (string, error)
	UpdateMessage(ctx context.Context, channelID, ts, text string) error
}

// TaskController handles reaction-based task control and feedback routing.
type TaskController struct {
	beadsMgr BeadsManager
	pool     WorkerPool
	poster   StatusPoster
	logger   *zap.Logger
}

// NewTaskController creates a new TaskController.
func NewTaskController(
	beadsMgr BeadsManager,
	pool WorkerPool,
	poster StatusPoster,
	logger *zap.Logger,
) *TaskController {
	return &TaskController{
		beadsMgr: beadsMgr,
		pool:     pool,
		poster:   poster,
		logger:   logger,
	}
}

// HandleReaction processes emoji reactions added on bot messages.
// ⏸️ = human block, 👍 = human unblock/resume.
func (tc *TaskController) HandleReaction(ctx context.Context, userID, channelID, msgTS, reaction string) {
	issue := tc.findByStartedTS(ctx, msgTS)
	if issue == nil {
		return
	}

	switch reaction {
	case "double_vertical_bar": // ⏸️
		tc.humanBlock(ctx, userID, channelID, issue)
	case "+1", "thumbsup":
		tc.humanForceRun(ctx, userID, channelID, issue)
	case "checkered_flag": // 🏁
		tc.humanClose(ctx, userID, channelID, issue)
	}
}

// HandleReactionRemoved processes emoji reactions removed from bot messages.
// Removing ⏸️ = resume task.
func (tc *TaskController) HandleReactionRemoved(ctx context.Context, userID, channelID, msgTS, reaction string) {
	if reaction != "double_vertical_bar" {
		return
	}
	issue := tc.findByStartedTS(ctx, msgTS)
	if issue == nil {
		return
	}
	tc.humanUnblock(ctx, userID, channelID, issue)
}

// HandleFeedback adds user feedback to a task, kills the agent, and reopens for re-queue.
func (tc *TaskController) HandleFeedback(ctx context.Context, userID, channelID, threadTS, taskID, feedback string) error {
	// Find the task owner (might differ from the reactor)
	ownerID, issue := tc.findByID(ctx, taskID)
	if issue == nil {
		return nil // not found, let caller fall through to new task
	}

	// Validate the reply is in the same thread as the target issue.
	// Without this, a user quoting a task ID from thread A while replying
	// in thread B would route feedback to the wrong issue.
	if issueThread := beads.LabelValue(issue.Labels, "thread:"); issueThread != "" && issueThread != threadTS {
		tc.logger.Warn("feedback thread mismatch, ignoring",
			zap.String("issue_id", issue.ID),
			zap.String("issue_thread", issueThread),
			zap.String("reply_thread", threadTS),
		)
		return fmt.Errorf("thread mismatch: reply in %s but issue belongs to %s", threadTS, issueThread)
	}

	tc.logger.Info("feedback on task",
		zap.String("issue_id", issue.ID),
		zap.Int("feedback_len", len(feedback)),
	)

	_ = tc.beadsMgr.AddUserComment(ctx, ownerID, issue.ID, feedback)
	tc.pool.CancelTask(issue.ID)
	tc.pool.ResetTask(issue.ID)

	if issue.Status != "open" {
		_ = tc.beadsMgr.ReopenIssue(ctx, ownerID, issue.ID)
	}

	// Update started message to show it's being retried with feedback
	if startedTS := beads.LabelValue(issue.Labels, "started:"); startedTS != "" {
		msg := status.FormatMessage("💬", issue.ID, issue.Description, nil)
		_ = tc.poster.UpdateMessage(ctx, channelID, startedTS, msg)
	}

	return nil
}

func (tc *TaskController) humanBlock(ctx context.Context, userID, channelID string, issue *beads.Issue) {
	tc.logger.Info("human block", zap.String("issue_id", issue.ID))

	// Block in memory FIRST — prevents retry race
	tc.pool.BlockTask(issue.ID)

	ownerID := tc.ownerOf(issue)
	_ = tc.beadsMgr.AddLabel(ctx, ownerID, issue.ID, "human:blocked")
	tc.pool.CancelTask(issue.ID)
	_ = tc.beadsMgr.ReopenIssue(ctx, ownerID, issue.ID)

	if startedTS := beads.LabelValue(issue.Labels, "started:"); startedTS != "" {
		msg := status.FormatMessage("⏸️", issue.ID, issue.Description, nil)
		_ = tc.poster.UpdateMessage(ctx, channelID, startedTS, msg)
	}
}


func (tc *TaskController) humanClose(ctx context.Context, userID, channelID string, issue *beads.Issue) {
	tc.logger.Info("human close", zap.String("issue_id", issue.ID))

	tc.pool.BlockTask(issue.ID)
	tc.pool.CancelTask(issue.ID)

	ownerID := tc.ownerOf(issue)
	_ = tc.beadsMgr.CloseIssue(ctx, ownerID, issue.ID, "Closed by user")

	if startedTS := beads.LabelValue(issue.Labels, "started:"); startedTS != "" {
		msg := status.FormatMessage("🏁", issue.ID, issue.Description, nil)
		_ = tc.poster.UpdateMessage(ctx, channelID, startedTS, msg)
	}
}

func (tc *TaskController) humanUnblock(ctx context.Context, userID, channelID string, issue *beads.Issue) {
	tc.logger.Info("human unblock", zap.String("issue_id", issue.ID))

	ownerID := tc.ownerOf(issue)
	_ = tc.beadsMgr.RemoveLabel(ctx, ownerID, issue.ID, "human:blocked")
	tc.pool.ResetTask(issue.ID)
	tc.pool.UnblockTask(issue.ID)

	// Reopen if it was closed/in_progress
	if issue.Status != "open" {
		_ = tc.beadsMgr.ReopenIssue(ctx, ownerID, issue.ID)
	}

	if startedTS := beads.LabelValue(issue.Labels, "started:"); startedTS != "" {
		msg := status.FormatMessage("👀", issue.ID, issue.Description, nil)
		_ = tc.poster.UpdateMessage(ctx, channelID, startedTS, msg)
	}
}

func (tc *TaskController) humanForceRun(ctx context.Context, userID, channelID string, issue *beads.Issue) {
	tc.logger.Info("human force run", zap.String("issue_id", issue.ID))

	ownerID := tc.ownerOf(issue)
	_ = tc.beadsMgr.RemoveLabel(ctx, ownerID, issue.ID, "human:blocked")
	tc.pool.UnblockTask(issue.ID)
	tc.pool.ResetTask(issue.ID)

	if issue.Status != "open" {
		_ = tc.beadsMgr.ReopenIssue(ctx, ownerID, issue.ID)
	}

	// Force-queue: bypass bd ready, add directly to queue
	threadInfo := &beads.ThreadInfo{
		ChannelID: beads.LabelValue(issue.Labels, "channel:"),
		ThreadTS:  beads.LabelValue(issue.Labels, "thread:"),
		MessageTS: beads.LabelValue(issue.Labels, "msg:"),
		UserID:    beads.LabelValue(issue.Labels, "user:"),
	}
	tc.pool.ForceQueue(issue.ID, ownerID, threadInfo)

	if startedTS := beads.LabelValue(issue.Labels, "started:"); startedTS != "" {
		msg := status.FormatMessage("👍", issue.ID, issue.Description, nil)
		_ = tc.poster.UpdateMessage(ctx, channelID, startedTS, msg)
	}
}


// findByStartedTS finds an issue across all users by its started: label.
func (tc *TaskController) findByStartedTS(ctx context.Context, msgTS string) *beads.Issue {
	for _, uid := range tc.beadsMgr.ListUserDirs() {
		issue, _ := tc.beadsMgr.FindIssueByStartedTS(ctx, uid, msgTS)
		if issue != nil {
			return issue
		}
	}
	return nil
}

// findByID finds an issue across all users by its full ID.
func (tc *TaskController) findByID(ctx context.Context, taskID string) (string, *beads.Issue) {
	for _, uid := range tc.beadsMgr.ListUserDirs() {
		issue, err := tc.beadsMgr.GetIssue(ctx, uid, taskID)
		if err == nil && issue != nil {
			return uid, issue
		}
	}
	return "", nil
}

// ownerOf extracts the user ID from issue labels.
func (tc *TaskController) ownerOf(issue *beads.Issue) string {
	return beads.LabelValue(issue.Labels, "user:")
}
