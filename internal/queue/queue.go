package queue

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TaskQueue manages a queue of tasks to be processed by workers.
// Uses Go channels for task distribution and tracks pending tasks to avoid duplicates.
type TaskQueue struct {
	tasks      chan *TaskWork
	results    chan *TaskResult
	pending    map[string]*TaskWork // Track pending tasks by IssueID
	completed  map[string]int       // Track completed tasks and their attempt count
	blocked    map[string]bool      // Human-blocked tasks (in-memory, instant)
	mu         sync.RWMutex
	closed     bool
	maxRetries int
}

// NewTaskQueue creates a new task queue with the specified capacity.
func NewTaskQueue(capacity int, maxRetries int) *TaskQueue {
	return &TaskQueue{
		tasks:      make(chan *TaskWork, capacity),
		results:    make(chan *TaskResult, capacity),
		pending:    make(map[string]*TaskWork),
		completed:  make(map[string]int),
		blocked:    make(map[string]bool),
		maxRetries: maxRetries,
	}
}

// Add adds a task to the queue.
// Returns an error if the queue is full, closed, or if the task is a duplicate.
// Non-blocking operation.
func (q *TaskQueue) Add(ctx context.Context, work *TaskWork) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return fmt.Errorf("queue is closed")
	}

	// Check for duplicate task
	if _, exists := q.pending[work.IssueID]; exists {
		return fmt.Errorf("task %s already in queue", work.IssueID)
	}

	// Check if task has exhausted retries (poller re-adding a completed task)
	if attempts, ok := q.completed[work.IssueID]; ok && attempts > q.maxRetries {
		return fmt.Errorf("task %s exhausted retries (%d)", work.IssueID, attempts)
	}

	// Check if task is human-blocked
	if q.blocked[work.IssueID] {
		return fmt.Errorf("task %s is human-blocked", work.IssueID)
	}

	// Set max retries if not already set
	if work.MaxRetries == 0 {
		work.MaxRetries = q.maxRetries
	}

	// Set created time if not already set
	if work.CreatedAt.IsZero() {
		work.CreatedAt = time.Now()
	}

	// Try to add to channel (non-blocking)
	select {
	case q.tasks <- work:
		q.pending[work.IssueID] = work
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("queue is full")
	}
}

// Dequeue retrieves the next task from the queue.
// Blocks until a task is available or context is cancelled.
func (q *TaskQueue) Dequeue(ctx context.Context) (*TaskWork, error) {
	select {
	case task := <-q.tasks:
		return task, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Complete marks a task as completed and records the result.
// Removes the task from the pending map and tracks attempt count.
func (q *TaskQueue) Complete(result *TaskResult) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Remove from pending
	delete(q.pending, result.IssueID)

	// Track attempt count
	q.completed[result.IssueID]++

	// Send result (non-blocking)
	select {
	case q.results <- result:
	default:
	}
}

// Results returns a read-only channel for consuming task results.
func (q *TaskQueue) Results() <-chan *TaskResult {
	return q.results
}

// Pending returns the number of tasks currently in the queue or being processed.
func (q *TaskQueue) Pending() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.pending)
}

// HasPending returns true if a task is currently in the queue or being processed.
func (q *TaskQueue) HasPending(issueID string) bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	_, exists := q.pending[issueID]
	return exists
}

// ResetTask clears the completed attempt count for a task.
// Used when a user explicitly retries a task (feedback/👍) to allow fresh attempts.
func (q *TaskQueue) ResetTask(issueID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.completed, issueID)
}

// BlockTask marks a task as human-blocked (in-memory, instant).
func (q *TaskQueue) BlockTask(issueID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.blocked[issueID] = true
}

// UnblockTask removes the human-blocked mark.
func (q *TaskQueue) UnblockTask(issueID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.blocked, issueID)
}

// IsBlocked returns true if a task is human-blocked.
func (q *TaskQueue) IsBlocked(issueID string) bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.blocked[issueID]
}

// Close closes the queue and prevents new tasks from being added.
// Existing tasks in the channel will still be processed.
func (q *TaskQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.closed {
		q.closed = true
		close(q.tasks)
		close(q.results)
	}
}

// IsClosed returns true if the queue has been closed.
func (q *TaskQueue) IsClosed() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.closed
}
