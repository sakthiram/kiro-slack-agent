package beads

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
)

// bdBinaryPath is the full path to the bd command.
// Needed because Go's exec.CommandContext may not inherit shell PATH.
var bdBinaryPath = findBdBinary()

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

// bdCmd creates an exec.Cmd for bd with --no-daemon appended automatically.
func bdCmd(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bdBinaryPath, append(args, "--no-daemon")...)
}

// Manager handles per-user beads database operations for tracking Slack thread conversations.
type Manager struct {
	sessionsBasePath   string
	issuePrefix        string
	contextMaxMessages int
	logger             *zap.Logger
	mu                 sync.RWMutex
	initialized        map[string]bool // track which users have been initialized
}

// NewManager creates a new beads manager.
func NewManager(cfg *config.BeadsConfig, logger *zap.Logger) *Manager {
	logger.Info("beads manager initialized",
		zap.String("bd_binary", bdBinaryPath),
		zap.String("sessions_base", cfg.SessionsBasePath),
		zap.String("issue_prefix", cfg.IssuePrefix),
	)
	return &Manager{
		sessionsBasePath:   cfg.SessionsBasePath,
		issuePrefix:        cfg.IssuePrefix,
		contextMaxMessages: cfg.ContextMaxMessages,
		logger:             logger,
		initialized:        make(map[string]bool),
	}
}

// SyncDB runs `bd sync --import-only` to ensure the database is in sync with JSONL.
// This should be called before operations that might fail due to stale db.
func (m *Manager) SyncDB(ctx context.Context, userID string) error {
	userDir := m.GetUserDir(userID)
	return m.syncDBUnlocked(ctx, userDir)
}

// syncDBUnlocked runs sync without acquiring locks (for internal use when lock is held)
func (m *Manager) syncDBUnlocked(ctx context.Context, userDir string) error {
	cmd := bdCmd(ctx, "sync", "--import-only")
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Warn("bd sync failed",
			zap.String("user_dir", userDir),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("bd sync failed: %w", err)
	}

	return nil
}

// EnsureUserDir initializes beads for a user if not already done.
// Creates sessions/<user_id>/.beads/ with bd init.
// Returns the user's session directory path.
func (m *Manager) EnsureUserDir(ctx context.Context, userID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	userDir := filepath.Join(m.sessionsBasePath, userID)
	beadsDir := filepath.Join(userDir, ".beads")

	// Check if already initialized in this session
	if m.initialized[userID] {
		return userDir, nil
	}

	// Check if .beads directory exists AND is properly initialized
	if _, err := os.Stat(beadsDir); err == nil {
		// Verify beads has issue_prefix configured (required for creating issues)
		checkCmd := bdCmd(ctx, "config", "get", "issue_prefix")
		checkCmd.Dir = userDir
		if checkOutput, checkErr := checkCmd.CombinedOutput(); checkErr == nil && len(strings.TrimSpace(string(checkOutput))) > 0 {
			// Beads is properly initialized with prefix, sync and mark as initialized
			_ = m.syncDBUnlocked(ctx, userDir) // Best effort
			m.initialized[userID] = true
			return userDir, nil
		} else {
			// .beads exists but missing issue_prefix - remove and reinit
			m.logger.Warn("beads directory exists but missing issue_prefix, reinitializing",
				zap.String("user_id", userID),
				zap.String("output", string(checkOutput)),
			)
			if removeErr := os.RemoveAll(beadsDir); removeErr != nil {
				m.logger.Error("failed to remove corrupted beads directory",
					zap.String("user_id", userID),
					zap.Error(removeErr),
				)
				return "", fmt.Errorf("failed to remove corrupted beads directory: %w", removeErr)
			}
		}
	}

	// Create user directory
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create user directory: %w", err)
	}

	// Initialize beads with bd init
	cmd := bdCmd(ctx, "init", "--stealth", "--prefix", m.issuePrefix+userID)
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("failed to initialize beads",
			zap.String("user_id", userID),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return "", fmt.Errorf("failed to initialize beads: %w", err)
	}

	m.initialized[userID] = true
	m.logger.Info("initialized beads for user",
		zap.String("user_id", userID),
		zap.String("user_dir", userDir),
	)

	return userDir, nil
}

// GetUserDir returns the user's session directory path without initializing.
func (m *Manager) GetUserDir(userID string) string {
	return filepath.Join(m.sessionsBasePath, userID)
}

// FindThreadIssue finds the root feature issue for a thread by labels.
// Returns the feature-type issue (the original thread root), falling back to
// the oldest issue if no feature is found. Returns nil if no issue is found.
func (m *Manager) FindThreadIssue(ctx context.Context, userID string, thread *ThreadInfo) (*Issue, error) {
	userDir := m.GetUserDir(userID)

	// Use bd list with label filter to find the issue
	cmd := bdCmd(ctx, "list",
		"--all",
		"--label", "thread:"+thread.ThreadTS,
		"--json", "--allow-stale",
	)
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		// No issues found is not an error
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return nil, nil // No issues found
			}
		}
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}

	// Parse JSON output
	var issues []Issue
	if err := json.Unmarshal(output, &issues); err != nil {
		// Try parsing as empty result
		if strings.TrimSpace(string(output)) == "[]" || strings.TrimSpace(string(output)) == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to parse issues: %w", err)
	}

	if len(issues) == 0 {
		return nil, nil
	}

	// Prefer the root feature issue — the one created by ProcessMainPost.
	// This avoids parenting human thread replies under agent-created subtasks,
	// which would cause transitive blocking from the agent's dependency graph.
	for i := range issues {
		if issues[i].Type == "feature" {
			return &issues[i], nil
		}
	}

	// Fallback: return the oldest issue (first created) as a best guess for root
	oldest := &issues[0]
	for i := 1; i < len(issues); i++ {
		if issues[i].CreatedAt.Before(oldest.CreatedAt) {
			oldest = &issues[i]
		}
	}
	return oldest, nil
}

// CreateThreadIssue creates a bd issue to track a Slack thread conversation.
// Labels: thread:<thread_ts>, channel:<channel_id>, user:<user_id>
func (m *Manager) CreateThreadIssue(ctx context.Context, userID string, thread *ThreadInfo, message string) (*Issue, error) {
	userDir := m.GetUserDir(userID)

	// Truncate message for title if too long
	title := message
	if len(title) > 80 {
		title = title[:77] + "..."
	}

	// Build labels argument
	labels := strings.Join(thread.Labels(), ",")

	// Create issue with bd create
	cmd := bdCmd(ctx, "create",
		title,
		"-t", "task",
		"-p", "2",
		"-l", labels,
		"--json",
	)
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		m.logger.Error("failed to create issue",
			zap.String("user_id", userID),
			zap.String("thread_ts", thread.ThreadTS),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}

	// Parse JSON output to get issue ID
	var issue Issue
	if err := json.Unmarshal(output, &issue); err != nil {
		// Try to extract ID from output string
		m.logger.Debug("created issue", zap.String("output", string(output)))
		issue.ID = strings.TrimSpace(string(output))
	}

	m.logger.Info("created thread issue",
		zap.String("user_id", userID),
		zap.String("thread_ts", thread.ThreadTS),
		zap.String("issue_id", issue.ID),
	)

	return &issue, nil
}

// UpdateThreadIssue appends a message to an existing thread issue using bd comment.
func (m *Manager) UpdateThreadIssue(ctx context.Context, userID, issueID, role, message string) error {
	userDir := m.GetUserDir(userID)

	// Format the comment with role prefix
	comment := fmt.Sprintf("[%s] %s", role, message)

	// Add comment with bd comment
	cmd := bdCmd(ctx, "comment", issueID, comment)
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("failed to update issue",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to update issue: %w", err)
	}

	return nil
}

// GetConversationContext retrieves previous messages from a thread issue.
// Returns messages in chronological order for context building.
func (m *Manager) GetConversationContext(ctx context.Context, userID, issueID string) ([]Message, error) {
	userDir := m.GetUserDir(userID)

	// Get issue details with comments using bd show
	cmd := bdCmd(ctx, "show", issueID, "--json", "--allow-stale")
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}

	// bd show --json returns an array with a single element
	var issues []Issue
	if err := json.Unmarshal(output, &issues); err != nil {
		return nil, fmt.Errorf("failed to parse issue: %w", err)
	}

	if len(issues) == 0 {
		return nil, fmt.Errorf("issue not found: %s", issueID)
	}

	issue := issues[0]

	// Convert comments to messages
	var messages []Message

	// Add initial message from issue title/description
	if issue.Description != "" {
		messages = append(messages, Message{
			Role:      "user",
			Content:   issue.Description,
			Timestamp: issue.CreatedAt,
		})
	}

	// Add comments as messages
	for _, comment := range issue.Comments {
		msg := parseComment(comment)
		messages = append(messages, msg)
	}

	// Limit to max messages
	if len(messages) > m.contextMaxMessages {
		messages = messages[len(messages)-m.contextMaxMessages:]
	}

	return messages, nil
}

// GetThreadHistory returns a compact summary of recent turns in a thread.
// Returns the last N user/agent exchange pairs, with agent responses truncated.
func (m *Manager) GetThreadHistory(ctx context.Context, userID, threadTS string, maxTurns int) (string, int, error) {
	userDir := m.GetUserDir(userID)

	cmd := bdCmd(ctx, "list", "--all", "--label", "thread:"+threadTS, "--json", "--allow-stale")
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", 0, nil
		}
		return "", 0, err
	}

	var issues []Issue
	if err := json.Unmarshal(output, &issues); err != nil {
		return "", 0, nil
	}

	if len(issues) == 0 {
		return "", 0, nil
	}

	// Collect turns: each issue = one turn (user request + agent response)
	type turn struct {
		user  string
		agent string
	}
	var turns []turn

	for _, issue := range issues {
		t := turn{user: issue.Description}
		for _, c := range issue.Comments {
			if strings.HasPrefix(c.Content, "[agent]") {
				t.agent = strings.TrimSpace(strings.TrimPrefix(c.Content, "[agent]"))
			}
		}
		if t.user != "" {
			turns = append(turns, t)
		}
	}

	totalTurns := len(turns)
	if totalTurns <= 1 {
		return "", totalTurns, nil // no history to show for first turn
	}

	// Exclude the last turn (that's the current request)
	history := turns[:totalTurns-1]

	// Take only the last N turns
	if len(history) > maxTurns {
		history = history[len(history)-maxTurns:]
	}

	// Format compact summary
	var sb strings.Builder
	shown := len(history)
	total := totalTurns - 1 // exclude current
	if shown < total {
		fmt.Fprintf(&sb, "(%d of %d prior turns shown, oldest omitted)\n", shown, total)
	}
	for i, t := range history {
		turnNum := total - shown + i + 1
		fmt.Fprintf(&sb, "[Turn %d] User: %s\n", turnNum, t.user)
		if t.agent != "" {
			agentText := t.agent
			if len(agentText) > 200 {
				agentText = agentText[:200] + "... (truncated)"
			}
			fmt.Fprintf(&sb, "[Turn %d] Agent: %s\n", turnNum, agentText)
		}
	}

	return sb.String(), totalTurns, nil
}
// Comments are formatted as "[role] content".
func parseComment(comment Comment) Message {
	content := comment.Content
	role := "user" // default

	// Check for role prefix
	if strings.HasPrefix(content, "[user]") {
		role = "user"
		content = strings.TrimPrefix(content, "[user]")
	} else if strings.HasPrefix(content, "[assistant]") {
		role = "assistant"
		content = strings.TrimPrefix(content, "[assistant]")
	} else if strings.HasPrefix(content, "[agent]") {
		role = "assistant"
		content = strings.TrimPrefix(content, "[agent]")
	}

	return Message{
		Role:      role,
		Content:   strings.TrimSpace(content),
		Timestamp: comment.CreatedAt,
	}
}

// CloseThreadIssue closes a thread issue when the conversation is complete.
func (m *Manager) CloseThreadIssue(ctx context.Context, userID, issueID, reason string) error {
	userDir := m.GetUserDir(userID)

	cmd := bdCmd(ctx, "close", issueID, "--reason", reason)
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("failed to close issue",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to close issue: %w", err)
	}

	return nil
}

// ListUserDirs lists all user session directories by scanning sessionsBasePath.
// Returns a list of user IDs that have been initialized.
func (m *Manager) ListUserDirs() []string {
	entries, err := os.ReadDir(m.sessionsBasePath)
	if err != nil {
		m.logger.Error("failed to read sessions directory",
			zap.String("path", m.sessionsBasePath),
			zap.Error(err),
		)
		return []string{}
	}

	var userIDs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if .beads directory exists for this user
		beadsDir := filepath.Join(m.sessionsBasePath, entry.Name(), ".beads")
		if _, err := os.Stat(beadsDir); err == nil {
			userIDs = append(userIDs, entry.Name())
		}
	}

	return userIDs
}

// GetReadyTasks runs `bd ready --json` in the user directory and returns tasks ready for processing.
func (m *Manager) GetReadyTasks(ctx context.Context, userID string) ([]ReadyTask, error) {
	userDir := m.GetUserDir(userID)

	// Use --allow-stale to prevent failures when db is out of sync
	cmd := bdCmd(ctx, "ready", "--json", "--allow-stale")
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		// No ready tasks is not an error
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return []ReadyTask{}, nil
			}
		}
		return nil, fmt.Errorf("failed to get ready tasks: %w", err)
	}

	// Parse JSON output
	var issues []Issue
	if err := json.Unmarshal(output, &issues); err != nil {
		// Try parsing as empty result
		if strings.TrimSpace(string(output)) == "[]" || strings.TrimSpace(string(output)) == "" {
			return []ReadyTask{}, nil
		}
		return nil, fmt.Errorf("failed to parse ready tasks: %w", err)
	}

	// Convert to ReadyTask
	tasks := make([]ReadyTask, len(issues))
	for i, issue := range issues {
		tasks[i] = ReadyTask{
			Issue:  issue,
			UserID: userID,
		}
	}

	return tasks, nil
}

// UpdateTaskStatus runs `bd update <id> --status <status>` to update a task's status.
func (m *Manager) UpdateTaskStatus(ctx context.Context, userID, issueID, status string) error {
	userDir := m.GetUserDir(userID)

	cmd := bdCmd(ctx, "update", issueID, "--status", status)
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("failed to update task status",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.String("status", status),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to update task status: %w", err)
	}

	return nil
}

// CreateFeature runs `bd create -t feature` to create a new feature issue.
func (m *Manager) CreateFeature(ctx context.Context, userID string, thread *ThreadInfo, title, desc string) (*Issue, error) {
	userDir := m.GetUserDir(userID)

	// Build labels argument
	labels := strings.Join(thread.Labels(), ",")

	// Create feature with bd create
	args := []string{"create", title, "-t", "feature", "--json"}
	if desc != "" {
		args = append(args, "-d", desc)
	}
	if labels != "" {
		args = append(args, "-l", labels)
	}

	cmd := bdCmd(ctx, args...)
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		m.logger.Error("failed to create feature",
			zap.String("user_id", userID),
			zap.String("title", title),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to create feature: %w", err)
	}

	// Parse JSON output to get issue
	var issue Issue
	if err := json.Unmarshal(output, &issue); err != nil {
		// Try to extract ID from output string
		m.logger.Debug("created feature", zap.String("output", string(output)))
		issue.ID = strings.TrimSpace(string(output))
	}

	m.logger.Info("created feature",
		zap.String("user_id", userID),
		zap.String("issue_id", issue.ID),
		zap.String("title", title),
	)

	return &issue, nil
}

// CreateTask runs `bd create -t task --parent <parentID>` to create a new task under a parent feature.
func (m *Manager) CreateTask(ctx context.Context, userID, parentID string, thread *ThreadInfo, title, desc string) (*Issue, error) {
	userDir := m.GetUserDir(userID)

	// Build labels argument
	labels := strings.Join(thread.Labels(), ",")

	// Create task with bd create
	args := []string{"create", title, "-t", "task", "--parent", parentID, "--json"}
	if desc != "" {
		args = append(args, "-d", desc)
	}
	if labels != "" {
		args = append(args, "-l", labels)
	}

	cmd := bdCmd(ctx, args...)
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		m.logger.Error("failed to create task",
			zap.String("user_id", userID),
			zap.String("parent_id", parentID),
			zap.String("title", title),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Parse JSON output to get issue
	var issue Issue
	if err := json.Unmarshal(output, &issue); err != nil {
		// Try to extract ID from output string
		m.logger.Debug("created task", zap.String("output", string(output)))
		issue.ID = strings.TrimSpace(string(output))
	}

	m.logger.Info("created task",
		zap.String("user_id", userID),
		zap.String("parent_id", parentID),
		zap.String("issue_id", issue.ID),
		zap.String("title", title),
	)

	return &issue, nil
}

// AddAgentComment runs `bd comment` with [agent] prefix to add an agent comment to an issue.
func (m *Manager) AddAgentComment(ctx context.Context, userID, issueID, content string) error {
	userDir := m.GetUserDir(userID)

	// Format the comment with [agent] prefix
	comment := fmt.Sprintf("[agent] %s", content)

	// Add comment with bd comment
	cmd := bdCmd(ctx, "comment", issueID, comment)
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("failed to add agent comment",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to add agent comment: %w", err)
	}

	return nil
}

// CloseIssue runs `bd close <id> --reason <reason>` to close an issue.
func (m *Manager) CloseIssue(ctx context.Context, userID, issueID, reason string) error {
	userDir := m.GetUserDir(userID)

	cmd := bdCmd(ctx, "close", issueID, "--reason", reason)
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("failed to close issue",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to close issue: %w", err)
	}

	return nil
}

// ReopenIssue runs `bd update <id> --status open` to reset an in-progress task.
// Used during graceful shutdown to ensure interrupted tasks are re-queued on restart.
func (m *Manager) ReopenIssue(ctx context.Context, userID, issueID string) error {
	userDir := m.GetUserDir(userID)

	cmd := bdCmd(ctx, "update", issueID, "--status", "open", "--no-daemon")
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("failed to reopen issue",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to reopen issue: %w", err)
	}

	return nil
}

// GetThreadTaskCounts returns open, in-progress, and closed task counts for a thread.
func (m *Manager) GetThreadTaskCounts(ctx context.Context, userID, threadTS string) (open, inProgress, closed int, err error) {
	userDir := m.GetUserDir(userID)

	cmd := bdCmd(ctx, "list", "--all", "--label", "thread:"+threadTS, "--json", "--allow-stale", "--no-daemon")
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, nil // no issues is fine
	}

	var issues []Issue
	if err := json.Unmarshal(output, &issues); err != nil {
		return 0, 0, 0, nil
	}

	for _, issue := range issues {
		switch issue.Status {
		case "open":
			open++
		case "in_progress":
			inProgress++
		case "closed":
			closed++
		}
	}
	return open, inProgress, closed, nil
}

// GetIssue runs `bd show <id> --json` to retrieve an issue with all its details.
func (m *Manager) GetIssue(ctx context.Context, userID, issueID string) (*Issue, error) {
	userDir := m.GetUserDir(userID)

	cmd := bdCmd(ctx, "show", issueID, "--json", "--allow-stale")
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		m.logger.Error("failed to get issue",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}

	// bd show --json returns an array with a single element
	var issues []Issue
	if err := json.Unmarshal(output, &issues); err != nil {
		return nil, fmt.Errorf("failed to parse issue: %w", err)
	}

	if len(issues) == 0 {
		return nil, fmt.Errorf("issue not found: %s", issueID)
	}

	return &issues[0], nil
}

// AddLabel runs `bd update <id> --add-label <label>` to add a label to an issue.
// Used to mark comments as synced: synced:<comment_id>
func (m *Manager) AddLabel(ctx context.Context, userID, issueID, label string) error {
	userDir := m.GetUserDir(userID)

	cmd := bdCmd(ctx, "update", issueID, "--add-label", label)
	cmd.Dir = userDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("failed to add label",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.String("label", label),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return fmt.Errorf("failed to add label: %w", err)
	}

	return nil
}

// HasLabel runs `bd show <id> --json` and checks if label exists in Labels array.
// Used to check if comment already synced.
func (m *Manager) HasLabel(ctx context.Context, userID, issueID, label string) bool {
	issue, err := m.GetIssue(ctx, userID, issueID)
	if err != nil {
		m.logger.Error("failed to check label",
			zap.String("user_id", userID),
			zap.String("issue_id", issueID),
			zap.String("label", label),
			zap.Error(err),
		)
		return false
	}

	// Check if label exists in the issue's labels
	for _, l := range issue.Labels {
		if l == label {
			return true
		}
	}

	return false
}

// ListIssuesByStatus runs `bd list --status <status1>,<status2> --json` to get issues with given statuses.
// Used on startup to find issues that need re-registration (e.g., "open", "in_progress", "ready").
func (m *Manager) ListIssuesByStatus(ctx context.Context, userID string, statuses []string) ([]*Issue, error) {
	userDir := m.GetUserDir(userID)

	// Build comma-separated status list
	statusList := strings.Join(statuses, ",")

	cmd := bdCmd(ctx, "list", "--status", statusList, "--json", "--allow-stale")
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		// No issues found is not an error
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return []*Issue{}, nil
			}
		}
		m.logger.Error("failed to list issues by status",
			zap.String("user_id", userID),
			zap.Strings("statuses", statuses),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to list issues by status: %w", err)
	}

	// Parse JSON output
	var issues []Issue
	if err := json.Unmarshal(output, &issues); err != nil {
		// Try parsing as empty result
		if strings.TrimSpace(string(output)) == "[]" || strings.TrimSpace(string(output)) == "" {
			return []*Issue{}, nil
		}
		m.logger.Error("failed to parse issues",
			zap.String("user_id", userID),
			zap.String("output", string(output)),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to parse issues: %w", err)
	}

	// Convert to pointer slice
	result := make([]*Issue, len(issues))
	for i := range issues {
		result[i] = &issues[i]
	}

	m.logger.Info("listed issues by status",
		zap.String("user_id", userID),
		zap.Strings("statuses", statuses),
		zap.Int("count", len(result)),
	)

	return result, nil
}
