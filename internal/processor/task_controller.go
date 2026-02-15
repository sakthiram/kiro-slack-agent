package processor

import (
	"context"
	"fmt"
	"regexp"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/status"
	"go.uber.org/zap"
)

var taskIDRegex = regexp.MustCompile("`([^`]+)`")

// WorkerPool defines the interface for cancelling running tasks.
type WorkerPool interface {
	CancelTask(issueID string) bool
}

// StatusPoster posts and updates status messages in Slack threads.
type StatusPoster interface {
	PostInThread(ctx context.Context, channelID, threadTS, text string) (string, error)
	UpdateMessage(ctx context.Context, channelID, ts, text string) error
	ReactTo(ctx context.Context, channelID, messageTS, emoji, removeEmoji string)
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

// HandleReaction processes emoji reactions on bot messages.
// ❌ = cancel task, 🔄 = retry task.
func (tc *TaskController) HandleReaction(ctx context.Context, userID, channelID, msgTS, reaction string) {
	// Find the task associated with this message via started: label
	issue, err := tc.findTaskByMsgTS(ctx, userID, channelID, msgTS)
	if err != nil || issue == nil {
		tc.logger.Debug("no task found for reaction",
			zap.String("msg_ts", msgTS),
			zap.String("reaction", reaction),
		)
		return
	}

	switch reaction {
	case "x", "heavy_multiplication_x":
		tc.cancelTask(ctx, userID, channelID, issue, msgTS)
	case "arrows_counterclockwise":
		tc.retryTask(ctx, userID, channelID, issue, msgTS)
	}
}

// HandleFeedback processes a user reply as feedback on a specific task.
func (tc *TaskController) HandleFeedback(ctx context.Context, userID, channelID, threadTS, taskID, feedback string) error {
	// Find the task by ID across all users
	var issue *beads.Issue
	for _, uid := range tc.beadsMgr.ListUserDirs() {
		found, err := tc.beadsMgr.FindIssueByStartedTS(ctx, uid, "")
		if err != nil {
			continue
		}
		// Try to find by task ID directly
		if found != nil && found.ID == taskID {
			issue = found
			userID = uid
			break
		}
	}

	// If not found by started TS, the taskID might be a short ID — search by listing
	if issue == nil {
		for _, uid := range tc.beadsMgr.ListUserDirs() {
			issues, err := tc.beadsMgr.ListIssuesByStatus(ctx, uid, []string{"open", "in_progress"})
			if err != nil {
				continue
			}
			for _, iss := range issues {
				if iss.ID == taskID || shortID(iss.ID) == taskID {
					issue = iss
					userID = uid
					break
				}
			}
			if issue != nil {
				break
			}
		}
	}

	if issue == nil {
		return fmt.Errorf("task %s not found", taskID)
	}

	tc.logger.Info("adding feedback to task",
		zap.String("issue_id", issue.ID),
		zap.String("feedback", feedback[:min(len(feedback), 80)]),
	)

	// Add feedback as user comment
	if err := tc.beadsMgr.AddUserComment(ctx, userID, issue.ID, feedback); err != nil {
		return fmt.Errorf("failed to add feedback: %w", err)
	}

	// Kill running agent if any
	tc.pool.CancelTask(issue.ID)

	// Reopen if closed or in_progress
	if issue.Status == "closed" || issue.Status == "in_progress" {
		_ = tc.beadsMgr.ReopenIssue(ctx, userID, issue.ID)
	}

	// React 💬 on the started message
	startedTS := beads.LabelValue(issue.Labels, "started:")
	if startedTS != "" {
		tc.poster.ReactTo(ctx, channelID, startedTS, "speech_balloon", "")
	}

	return nil
}

// IsBotMessage checks if a message TS corresponds to a bot-posted status or agent message.
func (tc *TaskController) IsBotMessage(ctx context.Context, userID, channelID, threadTS, msgTS string) bool {
	issue, _ := tc.findTaskByMsgTS(ctx, userID, channelID, msgTS)
	return issue != nil
}

// shortID extracts the short suffix from a full issue ID.
// e.g., "slackW0175971WA3-pda.3" → "pda.3"
func shortID(fullID string) string {
	if idx := lastIndex(fullID, '-'); idx >= 0 {
		return fullID[idx+1:]
	}
	return fullID
}

func lastIndex(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func (tc *TaskController) cancelTask(ctx context.Context, userID, channelID string, issue *beads.Issue, msgTS string) {
	tc.logger.Info("cancelling task via reaction",
		zap.String("issue_id", issue.ID),
	)

	// Kill running agent
	tc.pool.CancelTask(issue.ID)

	// Close the task
	_ = tc.beadsMgr.CloseIssue(ctx, userID, issue.ID, "Cancelled by user")

	// Update started message
	startedTS := beads.LabelValue(issue.Labels, "started:")
	if startedTS != "" {
		msg := status.FormatMessage("❌", issue.ID, issue.Description, nil)
		_ = tc.poster.UpdateMessage(ctx, channelID, startedTS, msg)
	}
}

func (tc *TaskController) retryTask(ctx context.Context, userID, channelID string, issue *beads.Issue, msgTS string) {
	tc.logger.Info("retrying task via reaction",
		zap.String("issue_id", issue.ID),
	)

	// Kill running agent
	tc.pool.CancelTask(issue.ID)

	// Reopen task so poller picks it up
	if issue.Status != "open" {
		_ = tc.beadsMgr.ReopenIssue(ctx, userID, issue.ID)
	}

	// Update started message
	startedTS := beads.LabelValue(issue.Labels, "started:")
	if startedTS != "" {
		msg := status.FormatMessage("🔄", issue.ID, issue.Description, nil)
		_ = tc.poster.UpdateMessage(ctx, channelID, startedTS, msg)
	}
}

// findTaskByMsgTS finds a task by checking if the msgTS matches a started: label
// in any issue for the thread in that channel.
func (tc *TaskController) findTaskByMsgTS(ctx context.Context, userID, channelID, msgTS string) (*beads.Issue, error) {
	userDirs := tc.beadsMgr.ListUserDirs()
	for _, uid := range userDirs {
		issue, err := tc.beadsMgr.FindIssueByStartedTS(ctx, uid, msgTS)
		if err == nil && issue != nil {
			return issue, nil
		}
	}
	return nil, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
