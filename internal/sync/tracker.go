package sync

import (
	"sync"
)

// SyncState tracks the synchronization state for a single issue.
// It maintains which comments have been synced to Slack for this issue.
type SyncState struct {
	IssueID          string
	UserID           string
	SlackThreadTS    string
	ChannelID        string
	SyncedCommentIDs map[string]bool
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
		IssueID:          issueID,
		UserID:           userID,
		SlackThreadTS:    threadTS,
		ChannelID:        channelID,
		SyncedCommentIDs: make(map[string]bool),
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

// MarkCommentSynced marks a comment as synchronized for a given issue.
// Returns false if the issue is not being tracked.
func (t *Tracker) MarkCommentSynced(issueID, commentID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, exists := t.states[issueID]
	if !exists {
		return false
	}

	state.SyncedCommentIDs[commentID] = true
	return true
}

// IsCommentSynced checks if a comment has been synced for a given issue.
// Returns false if the issue is not being tracked or comment is not synced.
func (t *Tracker) IsCommentSynced(issueID, commentID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, exists := t.states[issueID]
	if !exists {
		return false
	}

	return state.SyncedCommentIDs[commentID]
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
