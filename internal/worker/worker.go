package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/queue"
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

	// Build result object
	result := &queue.TaskResult{
		IssueID:     task.IssueID,
		CompletedAt: time.Time{},
		Duration:    0,
	}

	// React: swap 👀 → ⏳ to indicate processing started
	if task.ThreadInfo != nil && task.ThreadInfo.MessageTS != "" {
		w.syncer.ReactTo(ctx, task.ThreadInfo.ChannelID, task.ThreadInfo.MessageTS, "hourglass_flowing_sand", "eyes")
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

	// Determine final emoji based on success/retry state
	if task.ThreadInfo != nil && task.ThreadInfo.MessageTS != "" {
		ch := task.ThreadInfo.ChannelID
		ts := task.ThreadInfo.MessageTS

		if result.Success {
			// ⏳ → ✅
			w.syncer.ReactTo(ctx, ch, ts, "white_check_mark", "hourglass_flowing_sand")
		} else if task.Retries < task.MaxRetries {
			// Retrying: ⏳ → 🔁 (first retry) or add retry count emoji
			w.syncer.ReactTo(ctx, ch, ts, "repeat", "hourglass_flowing_sand")
			if task.Retries > 0 {
				// Add number emoji for retry count (2️⃣, 3️⃣, etc.)
				// Remove previous count first
				if prev := retryCountEmoji(task.Retries); prev != "" {
					w.syncer.ReactTo(ctx, ch, ts, "", prev)
				}
				if cur := retryCountEmoji(task.Retries + 1); cur != "" {
					w.syncer.ReactTo(ctx, ch, ts, cur, "")
				}
			}
		} else {
			// All retries exhausted: ⏳/🔁 → ❌
			w.syncer.ReactTo(ctx, ch, ts, "x", "hourglass_flowing_sand")
			w.syncer.ReactTo(ctx, ch, ts, "", "repeat")
			// Clean up any retry count emoji
			for i := 2; i <= task.MaxRetries+1; i++ {
				if e := retryCountEmoji(i); e != "" {
					w.syncer.ReactTo(ctx, ch, ts, "", e)
				}
			}
		}
	}

	// Retry logic (after Complete so pending map is cleared for re-add)
	if !result.Success && task.Retries < task.MaxRetries {
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

// retryCountEmoji returns the Slack number emoji for a retry count (2-9).
// Returns empty string for counts outside that range.
func retryCountEmoji(n int) string {
	names := []string{"", "", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	if n >= 2 && n < len(names) {
		return names[n]
	}
	return ""
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
