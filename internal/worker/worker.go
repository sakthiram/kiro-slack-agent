package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/queue"
	"github.com/sakthiram/kiro-slack-agent/internal/status"
	syncpkg "github.com/sakthiram/kiro-slack-agent/internal/sync"
	"go.uber.org/zap"
)

// Worker processes tasks from the task queue.
// Each worker runs in its own goroutine and processes tasks independently.
type Worker struct {
	id          int
	queue       *queue.TaskQueue
	runner      *KiroRunner
	beadsMgr    *beads.Manager
	syncer      *syncpkg.CommentSyncer
	taskTimeout time.Duration
	logger      *zap.Logger
	stopChan    chan struct{}
	doneChan    chan struct{}
	currentTask *queue.TaskWork // tracks in-flight task for shutdown cleanup
	taskCancel  context.CancelFunc // cancels the current task's context
	mu          sync.Mutex
}

// NewWorker creates a new worker instance.
func NewWorker(
	id int,
	taskQueue *queue.TaskQueue,
	runner *KiroRunner,
	beadsMgr *beads.Manager,
	syncer *syncpkg.CommentSyncer,
	cfg *config.WorkerConfig,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		id:          id,
		queue:       taskQueue,
		runner:      runner,
		beadsMgr:    beadsMgr,
		syncer:      syncer,
		taskTimeout: cfg.TaskTimeout,
		logger:      logger.With(zap.Int("worker_id", id)),
		stopChan:    make(chan struct{}),
		doneChan:    make(chan struct{}),
	}
}

// Start begins processing tasks from the queue.
// Runs in a loop until Stop() is called or context is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("worker started")
	defer func() {
		w.resetCurrentTask()
		close(w.doneChan)
	}()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("worker stopped by context cancellation")
			return
		case <-w.stopChan:
			w.logger.Info("worker stopped")
			return
		default:
			// Try to dequeue a task
			task, err := w.queue.Dequeue(ctx)
			if err != nil {
				// Context cancelled or queue closed
				if ctx.Err() != nil {
					w.logger.Info("worker stopping: context cancelled")
					return
				}
				// Queue closed
				w.logger.Info("worker stopping: queue closed")
				return
			}

			// Process the task
			w.processTask(ctx, task)
		}
	}
}

// Stop signals the worker to stop processing tasks.
func (w *Worker) Stop() {
	close(w.stopChan)
}

// WaitForShutdown waits for the worker to complete shutdown.
func (w *Worker) WaitForShutdown() {
	<-w.doneChan
}

// resetCurrentTask resets any in-progress task back to open on shutdown.
// Uses a fresh context since the parent context is already cancelled.
func (w *Worker) resetCurrentTask() {
	w.mu.Lock()
	task := w.currentTask
	w.mu.Unlock()

	if task == nil {
		return
	}

	w.logger.Info("resetting interrupted task to open",
		zap.String("issue_id", task.IssueID),
		zap.String("user_id", task.UserID),
	)

	// Use a short-lived independent context — the parent is already cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := w.beadsMgr.ReopenIssue(ctx, task.UserID, task.IssueID); err != nil {
		w.logger.Error("failed to reset task to open",
			zap.String("issue_id", task.IssueID),
			zap.Error(err),
		)
	}
}

// processTask processes a single task.
func (w *Worker) processTask(ctx context.Context, task *queue.TaskWork) {
	w.mu.Lock()
	w.currentTask = task
	w.mu.Unlock()
	defer func() {
		// Only clear currentTask if we completed normally (not interrupted by shutdown).
		// On shutdown, resetCurrentTask will handle resetting the beads status.
		if ctx.Err() == nil {
			w.mu.Lock()
			w.currentTask = nil
			w.mu.Unlock()
		}
	}()

	startTime := time.Now()

	w.logger.Info("processing task",
		zap.String("issue_id", task.IssueID),
		zap.String("user_id", task.UserID),
		zap.Int("retry", task.Retries),
	)

	// Create a timeout context for this task
	taskCtx, cancel := context.WithTimeout(ctx, w.taskTimeout)
	defer cancel()

	w.mu.Lock()
	w.taskCancel = cancel
	w.mu.Unlock()

	// Build result object
	result := &queue.TaskResult{
		IssueID:     task.IssueID,
		CompletedAt: time.Time{},
		Duration:    0,
	}

	// Get issue details for the status message
	issue, issueErr := w.beadsMgr.GetIssue(ctx, task.UserID, task.IssueID)

	// Post or update the started status message
	startedTS := ""
	if task.ThreadInfo != nil && task.ThreadInfo.ChannelID != "" && task.ThreadInfo.ThreadTS != "" {
		// Check if there's already a started message (retry/re-queue)
		if issue != nil {
			startedTS = beads.LabelValue(issue.Labels, "started:")
		}

		// Get thread counts
		open, inProg, done, _ := w.beadsMgr.GetThreadTaskCounts(ctx, task.UserID, task.ThreadInfo.ThreadTS)
		counts := &status.Counts{Open: open, InProgress: inProg, Done: done}
		desc := ""
		if issue != nil {
			desc = issue.Description
		}
		msg := status.FormatMessage("⏳", task.IssueID, desc, counts)

		if startedTS != "" {
			// Update existing started message
			if err := w.syncer.UpdateMessage(ctx, task.ThreadInfo.ChannelID, startedTS, msg); err != nil {
				w.logger.Warn("failed to update started message", zap.String("issue_id", task.IssueID), zap.Error(err))
			}
		} else {
			// Post new started message in thread
			ts, err := w.syncer.PostInThread(ctx, task.ThreadInfo.ChannelID, task.ThreadInfo.ThreadTS, msg)
			if err == nil && ts != "" {
				startedTS = ts
				// Store the started message TS as a label
				_ = w.beadsMgr.AddLabel(ctx, task.UserID, task.IssueID, "started:"+ts)
			}
		}

		// Remove 👀 from user's original message if present
		if task.ThreadInfo.MessageTS != "" {
			_ = w.syncer.RemoveReaction(ctx, task.ThreadInfo.ChannelID, task.ThreadInfo.MessageTS, "eyes")
		}
	}

	// Process the task and update result
	err := w.processTaskInternal(taskCtx, task, result)

	// Set completion time and duration
	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startTime)

	if err != nil {
		w.logger.Error("task processing failed",
			zap.String("issue_id", task.IssueID),
			zap.Error(err),
			zap.Duration("duration", result.Duration),
		)
		result.Success = false
		result.Error = err
	} else {
		w.logger.Info("task completed successfully",
			zap.String("issue_id", task.IssueID),
			zap.Duration("duration", result.Duration),
		)
		result.Success = true
	}

	// Record the result — must happen before retry Add() to clear pending map
	w.queue.Complete(result)

	// Update started message with final status
	if startedTS != "" && task.ThreadInfo != nil {
		ch := task.ThreadInfo.ChannelID
		open, inProg, done, _ := w.beadsMgr.GetThreadTaskCounts(ctx, task.UserID, task.ThreadInfo.ThreadTS)
		counts := &status.Counts{Open: open, InProgress: inProg, Done: done}
		desc := ""
		if issueErr == nil && issue != nil {
			desc = issue.Description
		}

		if result.Success {
			msg := status.FormatMessage("✅", task.IssueID, desc, counts)
			if err := w.syncer.UpdateMessage(ctx, ch, startedTS, msg); err != nil {
				w.logger.Warn("failed to update final status", zap.String("issue_id", task.IssueID), zap.Error(err))
			}
		} else if task.Retries < task.MaxRetries {
			emoji := fmt.Sprintf("🔁%s", retryCountEmoji(task.Retries+1))
			msg := status.FormatMessage(emoji, task.IssueID, desc, counts)
			if err := w.syncer.UpdateMessage(ctx, ch, startedTS, msg); err != nil {
				w.logger.Warn("failed to update retry status", zap.String("issue_id", task.IssueID), zap.Error(err))
			}
		} else {
			msg := status.FormatMessage("❌", task.IssueID, desc, counts)
			if err := w.syncer.UpdateMessage(ctx, ch, startedTS, msg); err != nil {
				w.logger.Warn("failed to update failure status", zap.String("issue_id", task.IssueID), zap.Error(err))
			}
		}
	}

	// Retry logic (after Complete so pending map is cleared for re-add)
	// Don't retry if task was cancelled (context error) or human-blocked
	if !result.Success && task.Retries < task.MaxRetries && ctx.Err() == nil {
		// Re-check issue labels — task may have been human:blocked while running
		if freshIssue, err := w.beadsMgr.GetIssue(ctx, task.UserID, task.IssueID); err == nil && freshIssue != nil {
			if beads.HasLabel(freshIssue.Labels, "human:blocked") {
				w.logger.Info("skipping retry: task is human-blocked",
					zap.String("issue_id", task.IssueID),
				)
				return
			}
		}

		w.logger.Info("retrying task",
			zap.String("issue_id", task.IssueID),
			zap.Int("retry_attempt", task.Retries+1),
			zap.Int("max_retries", task.MaxRetries),
		)
		task.Retries++
		if addErr := w.queue.Add(ctx, task); addErr != nil {
			w.logger.Error("failed to re-add task for retry",
				zap.String("issue_id", task.IssueID),
				zap.Error(addErr),
			)
		}
	}
}



// retryCountEmoji returns a number emoji string for retry count (1-9).
func retryCountEmoji(n int) string {
	emojis := []string{"", "1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣"}
	if n >= 1 && n < len(emojis) {
		return emojis[n]
	}
	return ""
}

// cancelIfRunning cancels the current task if it matches the given issue ID.
// Returns true if the task was found and cancelled.
func (w *Worker) cancelIfRunning(issueID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentTask != nil && w.currentTask.IssueID == issueID && w.taskCancel != nil {
		w.logger.Info("cancelling task", zap.String("issue_id", issueID))
		w.taskCancel()
		return true
	}
	return false
}

// processTaskInternal handles the actual task processing logic.
func (w *Worker) processTaskInternal(ctx context.Context, task *queue.TaskWork, result *queue.TaskResult) error {
	// Get the user's working directory
	userDir := w.beadsMgr.GetUserDir(task.UserID)

	// Get the issue details to extract thread label for history context
	issue, err := w.beadsMgr.GetIssue(ctx, task.UserID, task.IssueID)
	if err != nil {
		return fmt.Errorf("failed to get issue details: %w", err)
	}

	// Extract labels for conversation context and chaining
	threadTS := extractLabelValue(issue.Labels, "thread:")
	channelID := extractLabelValue(issue.Labels, "channel:")
	userID := extractLabelValue(issue.Labels, "user:")

	// Get the issue details to build the prompt
	messages, err := w.beadsMgr.GetConversationContext(ctx, task.UserID, task.IssueID)
	if err != nil {
		return fmt.Errorf("failed to get conversation context: %w", err)
	}

	// Build the prompt from the latest message (the task description)
	if len(messages) == 0 {
		// No messages - this is an agent-created task without user input
		// Close it gracefully instead of retrying forever
		w.logger.Warn("closing task with no messages (agent-created)",
			zap.String("issue_id", task.IssueID),
		)
		if err := w.beadsMgr.CloseIssue(ctx, task.UserID, task.IssueID,
			"Auto-closed: no user message found (agent-created task)"); err != nil {
			w.logger.Error("failed to close orphan task",
				zap.String("issue_id", task.IssueID),
				zap.Error(err),
			)
		}
		return nil // Success - task handled, no retry needed
	}

	// Use the last user message as the prompt
	var prompt string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			// Fetch compact thread history if this is part of a thread
			var threadHistory string
			if threadTS != "" {
				history, _, err := w.beadsMgr.GetThreadHistory(ctx, task.UserID, threadTS, 5)
				if err != nil {
					w.logger.Warn("failed to get thread history", zap.Error(err))
				}
				threadHistory = history
			}
			prompt = buildAgentPrompt(task.IssueID, messages[i].Content, threadTS, channelID, userID, threadHistory)
			break
		}
	}

	if prompt == "" {
		return fmt.Errorf("no user message found for issue %s", task.IssueID)
	}

	w.logger.Debug("running kiro agent",
		zap.String("issue_id", task.IssueID),
		zap.String("user_dir", userDir),
		zap.Int("prompt_length", len(prompt)),
	)

	// Run the kiro agent
	response, err := w.runner.Run(ctx, userDir, prompt, task.IssueID)
	if err != nil {
		return fmt.Errorf("kiro runner failed: %w", err)
	}

	result.Response = response

	// NOTE: We don't automatically add the response as a comment.
	// The agent is responsible for adding its own comment via:
	//   bd comment <issue_id> "[agent] <final answer>"
	// Only [agent] prefixed comments get synced to Slack.

	// Register task for sync if it has thread info and isn't already registered
	// (agent-created tasks won't be pre-registered like user-created ones)
	if task.ThreadInfo != nil && !w.syncer.IsRegistered(task.IssueID) {
		w.syncer.RegisterIssue(task.IssueID, task.UserID, task.ThreadInfo)
	}

	// Trigger comment synchronization to Slack
	w.logger.Debug("triggering comment sync",
		zap.String("issue_id", task.IssueID),
	)

	if err := w.syncer.SyncIssue(ctx, task.IssueID); err != nil {
		// Log the error but don't fail the task
		w.logger.Warn("failed to sync comments to Slack",
			zap.String("issue_id", task.IssueID),
			zap.Error(err),
		)
	}

	return nil
}

// buildAgentPrompt creates a comprehensive prompt for the kiro agent with bd instructions.
func buildAgentPrompt(issueID, userMessage, threadTS, channelID, userID, threadHistory string) string {
	// Build thread history section
	threadHistorySection := ""
	if threadHistory != "" {
		threadHistorySection = fmt.Sprintf(`
## Conversation History
%s
For full history: bd list --all --label thread:%s --json --allow-stale --no-daemon
`, threadHistory, threadTS)
	} else if threadTS != "" {
		threadHistorySection = fmt.Sprintf(`
## Conversation History

This task is part of an ongoing conversation thread. To get context from previous work:

bd list --all --label thread:%s --json --allow-stale --no-daemon

Review closed issues' comments to understand what was previously discussed.
`, threadTS)
	}

	// Build labels string for new issues
	labelsSection := ""
	if threadTS != "" && channelID != "" && userID != "" {
		labelsSection = fmt.Sprintf(`
## Creating Sub-Tasks

If you need to create new issues/tasks, **ALWAYS add labels and description** to chain them to this conversation:

bd create "task title" -t task -d "description" -l "thread:%s,channel:%s,user:%s" --no-daemon

## Dependencies

If a new task BLOCKS this task from completing:
1. Create the blocking task with labels (as shown above)
2. Add blocker: bd dep add %s <new-task-id> --type blocks --no-daemon
3. Reopen this task: bd update %s --status open --no-daemon
4. Comment: bd comment %s "[agent] ⏸️ Blocked by <new-task-id>: <reason>" --no-daemon
5. Do NOT close this task - it will be re-processed after the blocker is resolved
`, threadTS, channelID, userID, issueID, issueID, issueID)
	}

	return fmt.Sprintf(`You are a task agent working in a project that uses bd (beads) for issue tracking.
The bd CLI is installed at /opt/homebrew/bin/bd. Use it for all task management.

Start by initializing: bd init --stealth --no-daemon

## Context
Channel: %s | Thread: %s | User: %s

## Claim This Task
bd update %s --status in_progress --no-daemon
%s%s
## Current Request (Issue: %s)
%s

## How to Respond

Your response to the user is ONLY what you write via bd comment with [agent] prefix.
Everything else you do (tool calls, thinking, exploration) is NOT sent to Slack.

**When you have your final answer:**

bd comment %s "[agent] <your concise answer here>" --no-daemon

**DO NOT** include:
- Your exploration/thinking process
- Tool call outputs
- ANSI codes or terminal formatting
- Anything over 2000 characters

**When done (and no blockers), close the issue:**
bd close %s --reason "brief summary" --no-daemon`, channelID, threadTS, userID, issueID, threadHistorySection, labelsSection, issueID, userMessage, issueID, issueID)
}

// extractLabelValue extracts the value from a label with the given prefix.
// For example, extractLabelValue(["thread:123", "channel:C456"], "thread:") returns "123".
func extractLabelValue(labels []string, prefix string) string {
	for _, label := range labels {
		if len(label) > len(prefix) && label[:len(prefix)] == prefix {
			return label[len(prefix):]
		}
	}
	return ""
}
