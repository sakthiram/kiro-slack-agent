package beads

import "time"

// Issue represents a beads issue tracking a Slack thread conversation.
type Issue struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	Labels      []string  `json:"labels"`
	Comments    []Comment `json:"comments"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Comment represents a comment on an issue.
type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
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
	ThreadTS  string
	UserID    string
}

// Labels generates the labels for a thread issue.
func (t *ThreadInfo) Labels() []string {
	return []string{
		"thread:" + t.ThreadTS,
		"channel:" + t.ChannelID,
		"user:" + t.UserID,
	}
}
