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
	return &Manager{
		sessionsBasePath:   cfg.SessionsBasePath,
		issuePrefix:        cfg.IssuePrefix,
		contextMaxMessages: cfg.ContextMaxMessages,
		logger:             logger,
		initialized:        make(map[string]bool),
	}
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

	// Check if .beads directory exists
	if _, err := os.Stat(beadsDir); err == nil {
		m.initialized[userID] = true
		return userDir, nil
	}

	// Create user directory
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create user directory: %w", err)
	}

	// Initialize beads with bd init
	cmd := exec.CommandContext(ctx, "bd", "init", "--prefix", m.issuePrefix+"-"+userID)
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

// FindThreadIssue finds an existing issue for a thread by labels.
// Returns nil if no issue is found.
func (m *Manager) FindThreadIssue(ctx context.Context, userID string, thread *ThreadInfo) (*Issue, error) {
	userDir := m.GetUserDir(userID)

	// Use bd list with label filter to find the issue
	// bd list --label thread:<ts> --json
	cmd := exec.CommandContext(ctx, "bd", "list",
		"--label", "thread:"+thread.ThreadTS,
		"--json",
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

	return &issues[0], nil
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
	cmd := exec.CommandContext(ctx, "bd", "create",
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
	cmd := exec.CommandContext(ctx, "bd", "comment", issueID, comment)
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
	cmd := exec.CommandContext(ctx, "bd", "show", issueID, "--json")
	cmd.Dir = userDir

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}

	var issue Issue
	if err := json.Unmarshal(output, &issue); err != nil {
		return nil, fmt.Errorf("failed to parse issue: %w", err)
	}

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

// parseComment extracts role and content from a comment.
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

	cmd := exec.CommandContext(ctx, "bd", "close", issueID, "--reason", reason)
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
