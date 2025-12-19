package sync

import (
	"sync"
)

// SyncState tracks the synchronization state for a single issue.
// Note: Synced comments are tracked via beads labels (synced:<comment_id>)
// instead of in-memory maps, making the syncer stateless and persistent.
type SyncState struct {
	IssueID       string
	UserID        string
	SlackThreadTS string
	ChannelID     string
}

// Tracker manages sync state for multiple issues.
// It provides thread-safe operations for tracking which comments
// have been synchronized to Slack.
type Tracker struct {
	states map[string]*SyncState
	mu     sync.RWMutex
}

// NewTracker creates a new sync state tracker.
func NewTracker() *Tracker {
	return &Tracker{
		states: make(map[string]*SyncState),
	}
}

// Register adds a new issue to track for synchronization.
// It initializes the sync state with the provided Slack thread information.
func (t *Tracker) Register(issueID, userID, channelID, threadTS string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.states[issueID] = &SyncState{
		IssueID:       issueID,
		UserID:        userID,
		SlackThreadTS: threadTS,
		ChannelID:     channelID,
	}
}

// Unregister removes an issue from tracking.
// This should be called when an issue is closed or no longer needs syncing.
func (t *Tracker) Unregister(issueID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.states, issueID)
}

// GetState retrieves the sync state for an issue.
// Returns nil if the issue is not being tracked.
func (t *Tracker) GetState(issueID string) *SyncState {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.states[issueID]
}

// MarkCommentSynced is deprecated - comment sync state is now tracked via beads labels.
// Use Manager.AddLabel(ctx, userID, issueID, "synced:<comment_id>") instead.
// Kept for backwards compatibility but does nothing.
func (t *Tracker) MarkCommentSynced(issueID, commentID string) bool {
	return true
}

// IsCommentSynced is deprecated - comment sync state is now tracked via beads labels.
// Use Manager.HasLabel(ctx, userID, issueID, "synced:<comment_id>") instead.
// Kept for backwards compatibility but always returns false.
func (t *Tracker) IsCommentSynced(issueID, commentID string) bool {
	return false
}

// GetAllIssueIDs returns a list of all tracked issue IDs.
// This is useful for iterating over all issues during sync loops.
func (t *Tracker) GetAllIssueIDs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	issueIDs := make([]string, 0, len(t.states))
	for id := range t.states {
		issueIDs = append(issueIDs, id)
	}
	return issueIDs
}
