package beads

import "time"

// Issue represents a beads issue tracking a Slack thread conversation.
type Issue struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	Priority    int       `json:"priority"`
	Type        string    `json:"issue_type"` // bd uses "issue_type" not "type"
	ParentID    string    `json:"parent_id,omitempty"`
	Labels      []string  `json:"labels"`
	Comments    []Comment `json:"comments"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ReadyTask represents a task ready for processing from `bd ready`.
type ReadyTask struct {
	Issue
	UserID string // Extracted from labels for context
}

// Comment represents a comment on an issue.
type Comment struct {
	ID        int       `json:"id"`   // bd uses int for comment IDs
	IssueID   string    `json:"issue_id,omitempty"`
	Author    string    `json:"author"`
	Content   string    `json:"text"` // bd uses "text" not "content"
	CreatedAt time.Time `json:"created_at"`
}

// Message represents a conversation message for context building.
type Message struct {
	Role      string    `json:"role"`    // "user" or "assistant"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// ThreadInfo contains information about a Slack thread for tracking.
type ThreadInfo struct {
	ChannelID string
	ThreadTS  string // Parent thread timestamp (for grouping related issues)
	MessageTS string // Individual message timestamp (for deduplication/deep linking)
	UserID    string
}

// Labels generates the labels for a thread issue.
func (t *ThreadInfo) Labels() []string {
	labels := []string{
		"thread:" + t.ThreadTS,
		"channel:" + t.ChannelID,
		"user:" + t.UserID,
	}
	// Add msg: label for deduplication and deep linking
	if t.MessageTS != "" {
		labels = append(labels, "msg:"+t.MessageTS)
	}
	return labels
}
