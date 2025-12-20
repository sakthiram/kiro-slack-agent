package worker

import (
	"context"
	"fmt"
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
	defer close(w.doneChan)

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

// processTask processes a single task.
func (w *Worker) processTask(ctx context.Context, task *queue.TaskWork) {
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

		// Retry logic
		if task.Retries < task.MaxRetries {
			w.logger.Info("retrying task",
				zap.String("issue_id", task.IssueID),
				zap.Int("retry_attempt", task.Retries+1),
				zap.Int("max_retries", task.MaxRetries),
			)
			task.Retries++
			// Re-add to queue for retry
			if addErr := w.queue.Add(ctx, task); addErr != nil {
				w.logger.Error("failed to re-add task for retry",
					zap.String("issue_id", task.IssueID),
					zap.Error(addErr),
				)
			}
		}
	} else {
		w.logger.Info("task completed successfully",
			zap.String("issue_id", task.IssueID),
			zap.Duration("duration", result.Duration),
		)
		result.Success = true
	}

	// Record the result
	w.queue.Complete(result)
}

// processTaskInternal handles the actual task processing logic.
func (w *Worker) processTaskInternal(ctx context.Context, task *queue.TaskWork, result *queue.TaskResult) error {
	// Get the user's working directory
	userDir := w.beadsMgr.GetUserDir(task.UserID)

	// Get the issue details to build the prompt
	messages, err := w.beadsMgr.GetConversationContext(ctx, task.UserID, task.IssueID)
	if err != nil {
		return fmt.Errorf("failed to get conversation context: %w", err)
	}

	// Build the prompt from the latest message (the task description)
	if len(messages) == 0 {
		return fmt.Errorf("no messages found for issue %s", task.IssueID)
	}

	// Use the last user message as the prompt
	var prompt string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			// Build enhanced prompt with bd instructions and task context
			prompt = buildAgentPrompt(task.IssueID, messages[i].Content)
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
	response, err := w.runner.Run(ctx, userDir, prompt)
	if err != nil {
		return fmt.Errorf("kiro runner failed: %w", err)
	}

	result.Response = response

	// NOTE: We don't automatically add the response as a comment.
	// The agent is responsible for adding its own comment via:
	//   bd comment <issue_id> "[agent] <final answer>"
	// Only [agent] prefixed comments get synced to Slack.

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
func buildAgentPrompt(issueID, userMessage string) string {
	return fmt.Sprintf(`BEFORE ANYTHING ELSE: run 'bd onboard' and follow the instructions.

## Your Task (Issue: %s)
%s

## CRITICAL: How to Respond

Your response to the user is ONLY what you write via bd comment with [agent] prefix.
Everything else you do (tool calls, thinking, exploration) is NOT sent to Slack.

**When you have your final answer, run this command:**

bd comment %s "[agent] <your concise answer here>"

Example of a GOOD response:
bd comment %s "[agent] *Analysis Complete*

• Found 42 privacy events in the data
• 30 with privacy ON, 12 with privacy OFF
• Query saved to /tmp/results.csv"

**DO NOT** include:
- Your exploration/thinking process
- Tool call outputs
- ANSI codes or terminal formatting
- Anything over 2000 characters

**When done, close the issue:**
bd close %s --reason "brief summary"`, issueID, userMessage, issueID, issueID, issueID)
}
