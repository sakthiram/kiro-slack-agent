package queue

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"go.uber.org/zap"
)

// BeadsManager interface defines the methods needed by the Poller.
// This interface is based on internal/processor.BeadsManager.
type BeadsManager interface {
	GetUserDir(userID string) string
	EnsureUserDir(ctx context.Context, userID string) (string, error)
	CloseIssue(ctx context.Context, userID, issueID, reason string) error
}

// Poller periodically runs `bd ready` to find tasks ready for processing
// and adds them to the task queue.
type Poller struct {
	queue          *TaskQueue
	beadsMgr       BeadsManager
	sessionsBase   string
	pollInterval   time.Duration
	logger         *zap.Logger
	bdBinaryPath   string
}

// NewPoller creates a new poller instance.
func NewPoller(
	queue *TaskQueue,
	beadsMgr BeadsManager,
	sessionsBase string,
	pollInterval time.Duration,
	logger *zap.Logger,
) *Poller {
	return &Poller{
		queue:        queue,
		beadsMgr:     beadsMgr,
		sessionsBase: sessionsBase,
		pollInterval: pollInterval,
		logger:       logger,
		bdBinaryPath: findBdBinary(),
	}
}

// findBdBinary locates the bd binary, checking common paths.
func findBdBinary() string {
	// First try PATH
	if path, err := exec.LookPath("bd"); err == nil {
		return path
	}
	// Check common homebrew locations
	commonPaths := []string{
		"/opt/homebrew/bin/bd",
		"/usr/local/bin/bd",
	}
	for _, p := range commonPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Fallback to just "bd" and hope it's in PATH
	return "bd"
}

// Start begins the polling loop that runs periodically until context is cancelled.
func (p *Poller) Start(ctx context.Context) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	p.logger.Info("poller started",
		zap.Duration("interval", p.pollInterval),
		zap.String("sessions_base", p.sessionsBase),
	)

	// Run once immediately on start
	p.pollOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("poller stopped")
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

// pollOnce polls all user directories for ready tasks and adds them to the queue.
func (p *Poller) pollOnce(ctx context.Context) {
	userDirs := p.listUserDirs()
	if len(userDirs) == 0 {
		p.logger.Debug("no user directories found")
		return
	}

	p.logger.Debug("polling for ready tasks", zap.Int("user_count", len(userDirs)))

	for _, userID := range userDirs {
		if err := p.pollUserTasks(ctx, userID); err != nil {
			p.logger.Warn("failed to poll user tasks",
				zap.String("user_id", userID),
				zap.Error(err),
			)
		}
	}
}

// listUserDirs returns a list of user IDs by scanning the sessions directory.
func (p *Poller) listUserDirs() []string {
	entries, err := os.ReadDir(p.sessionsBase)
	if err != nil {
		p.logger.Warn("failed to read sessions directory",
			zap.String("path", p.sessionsBase),
			zap.Error(err),
		)
		return nil
	}

	var userDirs []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "." && entry.Name() != ".." {
			// Check if this directory has a .beads subdirectory
			beadsDir := filepath.Join(p.sessionsBase, entry.Name(), ".beads")
			if _, err := os.Stat(beadsDir); err == nil {
				userDirs = append(userDirs, entry.Name())
			}
		}
	}

	return userDirs
}

// pollUserTasks polls a specific user's directory for ready tasks.
func (p *Poller) pollUserTasks(ctx context.Context, userID string) error {
	userDir := p.beadsMgr.GetUserDir(userID)

	// Run bd ready --json to get ready tasks
	cmd := exec.CommandContext(ctx, p.bdBinaryPath, "ready", "--json", "--no-daemon")
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		// Exit code 1 typically means no ready tasks, which is not an error
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}

	// Parse JSON output
	var readyTasks []beads.ReadyTask
	if err := json.Unmarshal(output, &readyTasks); err != nil {
		// Empty output is not an error
		if len(output) == 0 || string(output) == "[]" {
			return nil
		}
		return err
	}

	// Add each ready task to the queue
	for _, task := range readyTasks {
		// Close tasks without description - these are agent-created tasks
		// that have no user message to process
		if task.Description == "" {
			p.logger.Info("closing task without description (agent-created)",
				zap.String("issue_id", task.ID),
			)
			if err := p.beadsMgr.CloseIssue(ctx, userID, task.ID,
				"Auto-closed: no user message found (agent-created task)"); err != nil {
				p.logger.Error("failed to close task without description",
					zap.String("issue_id", task.ID),
					zap.Error(err),
				)
			}
			continue
		}

		work := &TaskWork{
			IssueID:   task.ID,
			UserID:    userID,
			Priority:  task.Priority,
			CreatedAt: time.Now(),
		}

		// Extract ThreadInfo from labels if present
		work.ThreadInfo = extractThreadInfo(task.Labels)

		// Try to add to queue (non-blocking)
		if err := p.queue.Add(ctx, work); err != nil {
			// Log but don't fail - task might already be in queue
			p.logger.Debug("skipped adding task to queue",
				zap.String("user_id", userID),
				zap.String("issue_id", task.ID),
				zap.Error(err),
			)
		} else {
			p.logger.Info("added task to queue",
				zap.String("user_id", userID),
				zap.String("issue_id", task.ID),
				zap.Int("priority", task.Priority),
			)
		}
	}

	return nil
}

// extractThreadInfo extracts ThreadInfo from issue labels.
func extractThreadInfo(labels []string) *beads.ThreadInfo {
	info := &beads.ThreadInfo{}

	for _, label := range labels {
		if len(label) > 7 && label[:7] == "thread:" {
			info.ThreadTS = label[7:]
		} else if len(label) > 8 && label[:8] == "channel:" {
			info.ChannelID = label[8:]
		} else if len(label) > 5 && label[:5] == "user:" {
			info.UserID = label[5:]
		}
	}

	// Return nil if no thread info found
	if info.ThreadTS == "" && info.ChannelID == "" {
		return nil
	}

	return info
}
