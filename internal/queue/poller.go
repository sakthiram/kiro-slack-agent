package queue

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/status"
	"go.uber.org/zap"
)

// BeadsManager interface defines the methods needed by the Poller.
type BeadsManager interface {
	GetUserDir(userID string) string
	EnsureUserDir(ctx context.Context, userID string) (string, error)
	CloseIssue(ctx context.Context, userID, issueID, reason string) error
	GetBlockedTasks(ctx context.Context, userID, threadTS string) ([]beads.Issue, error)
	GetIssueBlockers(ctx context.Context, userID, issueID string) ([]beads.Dependency, error)
	AddLabel(ctx context.Context, userID, issueID, label string) error
}

// StatusPoster posts and updates status messages in Slack threads.
type StatusPoster interface {
	PostInThread(ctx context.Context, channelID, threadTS, text string) (string, error)
	UpdateMessage(ctx context.Context, channelID, ts, text string) error
}

// Poller periodically runs `bd ready` to find tasks ready for processing
// and adds them to the task queue.
type Poller struct {
	queue        *TaskQueue
	beadsMgr     BeadsManager
	poster       StatusPoster
	sessionsBase string
	pollInterval time.Duration
	agentGrace   time.Duration
	logger       *zap.Logger
	bdBinaryPath string
}

// NewPoller creates a new poller instance.
func NewPoller(
	queue *TaskQueue,
	beadsMgr BeadsManager,
	poster StatusPoster,
	sessionsBase string,
	pollInterval time.Duration,
	agentGrace time.Duration,
	logger *zap.Logger,
) *Poller {
	return &Poller{
		queue:        queue,
		beadsMgr:     beadsMgr,
		poster:       poster,
		sessionsBase: sessionsBase,
		pollInterval: pollInterval,
		agentGrace:   agentGrace,
		logger:       logger,
		bdBinaryPath: findBdBinary(),
	}
}

// findBdBinary locates the bd binary, checking common paths.
func findBdBinary() string {
	if path, err := exec.LookPath("bd"); err == nil {
		return path
	}
	commonPaths := []string{
		"/opt/homebrew/bin/bd",
		"/usr/local/bin/bd",
	}
	for _, p := range commonPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
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

// pollOnce polls all user directories for ready and blocked tasks.
func (p *Poller) pollOnce(ctx context.Context) {
	userDirs := p.listUserDirs()
	if len(userDirs) == 0 {
		return
	}

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
		return nil
	}

	var userDirs []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "." && entry.Name() != ".." {
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
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}

	var readyTasks []beads.ReadyTask
	if err := json.Unmarshal(output, &readyTasks); err != nil {
		if len(output) == 0 || string(output) == "[]" {
			return nil
		}
		return err
	}

	// Track threads we've seen ready tasks for (to check blocked tasks)
	threadsSeen := make(map[string]bool)

	for _, task := range readyTasks {
		// Close tasks without description
		if task.Description == "" {
			p.logger.Info("closing task without description",
				zap.String("issue_id", task.ID),
			)
			_ = p.beadsMgr.CloseIssue(ctx, userID, task.ID,
				"Auto-closed: no user message found")
			continue
		}

		// Grace period for agent-created tasks
		if !beads.HasLabel(task.Labels, "msg:") && time.Since(task.CreatedAt) < p.agentGrace {
			p.logger.Debug("skipping young agent task (grace period)",
				zap.String("issue_id", task.ID),
			)
			continue
		}

		// Skip human-blocked tasks
		if beads.HasLabel(task.Labels, "human:blocked") {
			p.logger.Info("skipping human-blocked task",
				zap.String("issue_id", task.ID),
			)
			continue
		}

		// Track thread for blocked task check
		if ts := beads.LabelValue(task.Labels, "thread:"); ts != "" {
			threadsSeen[ts] = true
		}

		// If task was previously blocked (has started: label), update to queued
		// Only update if not already in queue (first time becoming ready)
		if startedTS := beads.LabelValue(task.Labels, "started:"); startedTS != "" {
			if !p.queue.HasPending(task.ID) {
				ch := beads.LabelValue(task.Labels, "channel:")
				if ch != "" && p.poster != nil {
					msg := status.FormatMessage("👀", task.ID, task.Description, nil)
					_ = p.poster.UpdateMessage(ctx, ch, startedTS, msg)
				}
			}
		}

		work := &TaskWork{
			IssueID:   task.ID,
			UserID:    userID,
			Priority:  task.Priority,
			CreatedAt: time.Now(),
		}
		work.ThreadInfo = extractThreadInfo(task.Labels)

		if err := p.queue.Add(ctx, work); err != nil {
			p.logger.Debug("skipped adding task to queue",
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

	// Post status messages for blocked tasks
	if p.poster != nil {
		for threadTS := range threadsSeen {
			p.postBlockedStatus(ctx, userID, threadTS)
		}
	}

	return nil
}

// postBlockedStatus posts ⛔️ status messages for blocked tasks in a thread.
func (p *Poller) postBlockedStatus(ctx context.Context, userID, threadTS string) {
	blocked, err := p.beadsMgr.GetBlockedTasks(ctx, userID, threadTS)
	if err != nil || len(blocked) == 0 {
		return
	}

	for _, issue := range blocked {
		// Skip if already has a started message
		if beads.HasLabel(issue.Labels, "started:") {
			continue
		}

		ch := beads.LabelValue(issue.Labels, "channel:")
		if ch == "" {
			continue
		}

		// Get blocker info
		var blockerIDs []string
		if blockers, err := p.beadsMgr.GetIssueBlockers(ctx, userID, issue.ID); err == nil {
			for _, b := range blockers {
				blockerIDs = append(blockerIDs, b.BlockerID())
			}
		}

		msg := status.FormatBlocked(issue.ID, issue.Description, blockerIDs)
		ts, err := p.poster.PostInThread(ctx, ch, threadTS, msg)
		if err == nil && ts != "" {
			_ = p.beadsMgr.AddLabel(ctx, userID, issue.ID, "started:"+ts)
			p.logger.Info("posted blocked status",
				zap.String("issue_id", issue.ID),
				zap.Strings("blockers", blockerIDs),
			)
		}
	}
}

// extractThreadInfo extracts ThreadInfo from issue labels.
func extractThreadInfo(labels []string) *beads.ThreadInfo {
	info := &beads.ThreadInfo{
		ThreadTS:  beads.LabelValue(labels, "thread:"),
		ChannelID: beads.LabelValue(labels, "channel:"),
		UserID:    beads.LabelValue(labels, "user:"),
		MessageTS: beads.LabelValue(labels, "msg:"),
	}

	if info.ThreadTS == "" && info.ChannelID == "" {
		return nil
	}
	return info
}

// collectThreads extracts unique thread timestamps from a list of issues.
func collectThreads(issues []beads.Issue) []string {
	seen := make(map[string]bool)
	var threads []string
	for _, issue := range issues {
		if ts := beads.LabelValue(issue.Labels, "thread:"); ts != "" && !seen[ts] {
			seen[ts] = true
			threads = append(threads, ts)
		}
	}
	return threads
}

// hasLabel checks if any label starts with the given prefix.
// Deprecated: use beads.HasLabel instead.
func hasLabel(labels []string, prefix string) bool {
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}
