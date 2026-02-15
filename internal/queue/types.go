package queue

import (
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
)

// TaskWork represents a task to be processed by the worker pool.
type TaskWork struct {
	IssueID    string
	UserID     string
	ThreadInfo *beads.ThreadInfo
	Priority   int
	Retries    int
	MaxRetries int
	CreatedAt  time.Time
}

// TaskResult represents the result of processing a task.
type TaskResult struct {
	IssueID     string
	Success     bool
	Response    string
	Error       error
	Duration    time.Duration
	CompletedAt time.Time
}
