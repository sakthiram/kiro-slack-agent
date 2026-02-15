package beads

import (
	"strings"
	"time"
)

// Issue represents a beads issue tracking a Slack thread conversation.
type Issue struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Status       string       `json:"status"`
	Priority     int          `json:"priority"`
	Type         string       `json:"issue_type"` // bd uses "issue_type" not "type"
	ParentID     string       `json:"parent_id,omitempty"`
	Labels       []string     `json:"labels"`
	Comments     []Comment    `json:"comments"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// Dependency represents a dependency relationship from bd list/show.
type Dependency struct {
	IssueID     string `json:"issue_id,omitempty"`      // from bd list
	DependsOnID string `json:"depends_on_id,omitempty"` // from bd list
	Type        string `json:"type,omitempty"`           // from bd list
	// Fields from bd show (embedded issue)
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	DepType string `json:"dependency_type,omitempty"` // from bd show
}

// BlockerID returns the blocker issue ID regardless of source format.
func (d Dependency) BlockerID() string {
	if d.DependsOnID != "" {
		return d.DependsOnID
	}
	return d.ID
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
	if t.MessageTS != "" {
		labels = append(labels, "msg:"+t.MessageTS)
	}
	return labels
}

// LabelValue extracts the value from a label with the given prefix in a label list.
func LabelValue(labels []string, prefix string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			return l[len(prefix):]
		}
	}
	return ""
}

// HasLabel checks if a label with the given prefix exists.
func HasLabel(labels []string, prefix string) bool {
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}
