package status

import "fmt"

// FormatMessage builds a status message for a task.
// emoji: ⏳, ✅, ❌, 🔁, ⛔️
func FormatMessage(emoji, issueID, description string, counts *Counts) string {
	desc := Truncate(description, 160)
	msg := fmt.Sprintf("%s Task: `%s`\n> %s", emoji, issueID, desc)
	if counts != nil {
		msg += fmt.Sprintf("\n> 👀 %d  ⏳ %d  ✅ %d", counts.Open, counts.InProgress, counts.Done)
	}
	return msg
}

// FormatBlocked builds a status message for a blocked task.
func FormatBlocked(issueID, description string, blockerIDs []string) string {
	desc := Truncate(description, 160)
	msg := fmt.Sprintf("⛔️ Task blocked: `%s`\n> %s", issueID, desc)
	if len(blockerIDs) > 0 {
		msg += fmt.Sprintf("\n> ⛔️ waiting on: `%s`", blockerIDs[0])
		for _, id := range blockerIDs[1:] {
			msg += fmt.Sprintf(", `%s`", id)
		}
	}
	return msg
}

// Counts holds task counts for a thread.
type Counts struct {
	Open       int
	InProgress int
	Done       int
}

// Truncate truncates a string to maxLen, adding "..." if truncated.
// Tries to break at a word boundary.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find last space before maxLen
	cut := maxLen - 3
	for i := cut; i > cut-30 && i > 0; i-- {
		if s[i] == ' ' {
			return s[:i] + "..."
		}
	}
	return s[:cut] + "..."
}
