//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
)

// TestHelper provides utility methods for E2E tests
type TestHelper struct {
	api       *slack.Client
	channelID string
	botUserID string
	t         *testing.T
}

// NewTestHelper creates a new test helper
func NewTestHelper(t *testing.T, api *slack.Client, channelID, botUserID string) *TestHelper {
	return &TestHelper{
		api:       api,
		channelID: channelID,
		botUserID: botUserID,
		t:         t,
	}
}

// PostMessage posts a message to the test channel
func (h *TestHelper) PostMessage(text string, opts ...slack.MsgOption) string {
	ctx := context.Background()

	options := []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionDisableLinkUnfurl(),
	}
	options = append(options, opts...)

	_, timestamp, err := h.api.PostMessageContext(ctx, h.channelID, options...)
	require.NoError(h.t, err, "Failed to post message")

	h.t.Logf("Posted message (ts=%s): %s", timestamp, text)
	return timestamp
}

// PostMention posts a message mentioning the bot
func (h *TestHelper) PostMention(text string, opts ...slack.MsgOption) (messageTS, threadTS string) {
	mentionText := fmt.Sprintf("<@%s> %s", h.botUserID, text)
	ts := h.PostMessage(mentionText, opts...)
	return ts, ts
}

// PostThreadReply posts a reply in a thread
func (h *TestHelper) PostThreadReply(threadTS, text string) string {
	return h.PostMessage(text, slack.MsgOptionTS(threadTS))
}

// WaitForReply waits for any reply in a thread after a specific timestamp
func (h *TestHelper) WaitForReply(threadTS, afterTS string, timeout time.Duration) *slack.Message {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	h.t.Logf("Waiting for reply in thread %s after %s...", threadTS, afterTS)

	for {
		select {
		case <-ctx.Done():
			h.t.Fatalf("Timeout waiting for reply after %v", timeout)
			return nil
		case <-ticker.C:
			replies, _, _, err := h.api.GetConversationReplies(&slack.GetConversationRepliesParameters{
				ChannelID: h.channelID,
				Timestamp: threadTS,
			})
			if err != nil {
				h.t.Logf("Warning: Failed to get conversation replies: %v", err)
				continue
			}

			for _, msg := range replies {
				if msg.Timestamp > afterTS && (msg.User == h.botUserID || msg.BotID != "") {
					h.t.Logf("Found reply (ts=%s): %s", msg.Timestamp, truncateText(msg.Text, 100))
					return &msg
				}
			}
		}
	}
}

// WaitForBotReply waits specifically for the bot to reply
func (h *TestHelper) WaitForBotReply(threadTS, afterTS string) *slack.Message {
	return h.WaitForReply(threadTS, afterTS, 120*time.Second)
}

// GetMessage retrieves a specific message by timestamp
func (h *TestHelper) GetMessage(messageTS string) (*slack.Message, error) {
	ctx := context.Background()

	params := &slack.GetConversationHistoryParameters{
		ChannelID: h.channelID,
		Latest:    messageTS,
		Inclusive: true,
		Limit:     1,
	}

	history, err := h.api.GetConversationHistoryContext(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}

	if len(history.Messages) == 0 {
		return nil, fmt.Errorf("message not found")
	}

	return &history.Messages[0], nil
}

// DeleteMessage deletes a message
func (h *TestHelper) DeleteMessage(messageTS string) error {
	ctx := context.Background()

	_, _, err := h.api.DeleteMessageContext(ctx, h.channelID, messageTS)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	h.t.Logf("Deleted message: %s", messageTS)
	return nil
}

// CleanupMessages deletes multiple messages
func (h *TestHelper) CleanupMessages(timestamps ...string) {
	for _, ts := range timestamps {
		if ts == "" {
			continue
		}

		if err := h.DeleteMessage(ts); err != nil {
			h.t.Logf("Warning: Failed to cleanup message %s: %v", ts, err)
		}
	}
}

// GetThreadReplies retrieves all replies in a thread
func (h *TestHelper) GetThreadReplies(threadTS string) ([]slack.Message, error) {
	ctx := context.Background()

	replies, _, _, err := h.api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
		ChannelID: h.channelID,
		Timestamp: threadTS,
	})

	return replies, err
}

// CountBotReplies counts how many replies from the bot exist in a thread
func (h *TestHelper) CountBotReplies(threadTS string) int {
	replies, err := h.GetThreadReplies(threadTS)
	if err != nil {
		h.t.Logf("Warning: Failed to get thread replies: %v", err)
		return 0
	}

	count := 0
	for _, msg := range replies {
		if msg.User == h.botUserID || msg.BotID != "" {
			count++
		}
	}

	return count
}

// WaitForMessageUpdate waits for a message to be updated
func (h *TestHelper) WaitForMessageUpdate(messageTS string, minUpdates int, timeout time.Duration) []string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var texts []string
	lastText := ""
	updateCount := 0

	h.t.Logf("Waiting for message %s to be updated (min=%d)...", messageTS, minUpdates)

	for {
		select {
		case <-ctx.Done():
			h.t.Logf("Timeout reached, found %d updates", updateCount)
			return texts
		case <-ticker.C:
			msg, err := h.GetMessage(messageTS)
			if err != nil {
				h.t.Logf("Warning: Failed to get message: %v", err)
				continue
			}

			if msg.Text != lastText {
				h.t.Logf("Update %d: %s", updateCount+1, truncateText(msg.Text, 100))
				lastText = msg.Text
				texts = append(texts, msg.Text)
				updateCount++

				// Check if we have enough updates and message looks complete
				if updateCount >= minUpdates && !strings.Contains(msg.Text, "Thinking") {
					return texts
				}
			}
		}
	}
}

// AssertBotReplied asserts that the bot replied to a message
func (h *TestHelper) AssertBotReplied(threadTS, afterTS string) *slack.Message {
	reply := h.WaitForBotReply(threadTS, afterTS)
	require.NotNil(h.t, reply, "Bot should have replied")
	require.NotEmpty(h.t, reply.Text, "Bot reply should not be empty")
	return reply
}

// AssertMessageContains asserts that a message contains specific text
func (h *TestHelper) AssertMessageContains(msg *slack.Message, substr string) {
	require.Contains(h.t, strings.ToLower(msg.Text), strings.ToLower(substr),
		"Message should contain '%s'", substr)
}

// AssertMessageNotEmpty asserts that a message is not empty
func (h *TestHelper) AssertMessageNotEmpty(msg *slack.Message) {
	require.NotEmpty(h.t, msg.Text, "Message should not be empty")
	require.Greater(h.t, len(msg.Text), 10, "Message should be substantive")
}

// WaitForCondition waits for a condition to be true
func (h *TestHelper) WaitForCondition(condition func() bool, timeout time.Duration, description string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	h.t.Logf("Waiting for condition: %s", description)

	for {
		select {
		case <-ctx.Done():
			h.t.Logf("Timeout waiting for condition: %s", description)
			return false
		case <-ticker.C:
			if condition() {
				h.t.Logf("Condition met: %s", description)
				return true
			}
		}
	}
}

// GetChannelInfo retrieves channel information
func (h *TestHelper) GetChannelInfo() (*slack.Channel, error) {
	ctx := context.Background()
	return h.api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
		ChannelID: h.channelID,
	})
}

// GetBotInfo retrieves bot user information
func (h *TestHelper) GetBotInfo() (*slack.User, error) {
	ctx := context.Background()
	return h.api.GetUserInfoContext(ctx, h.botUserID)
}

// Sleep pauses execution with logging
func (h *TestHelper) Sleep(duration time.Duration, reason string) {
	h.t.Logf("Sleeping for %v: %s", duration, reason)
	time.Sleep(duration)
}

// truncateText truncates text for logging
func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

// RetryWithBackoff retries an operation with exponential backoff
func (h *TestHelper) RetryWithBackoff(operation func() error, maxRetries int, initialDelay time.Duration) error {
	delay := initialDelay
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		err := operation()
		if err == nil {
			return nil
		}

		lastErr = err
		h.t.Logf("Retry %d/%d failed: %v. Waiting %v...", i+1, maxRetries, err, delay)
		time.Sleep(delay)
		delay *= 2
	}

	return fmt.Errorf("operation failed after %d retries: %w", maxRetries, lastErr)
}

// VerifyThreadStructure verifies thread has expected structure
func (h *TestHelper) VerifyThreadStructure(threadTS string, expectedMinReplies int) {
	replies, err := h.GetThreadReplies(threadTS)
	require.NoError(h.t, err, "Should be able to get thread replies")
	require.GreaterOrEqual(h.t, len(replies), expectedMinReplies+1, // +1 for root message
		"Thread should have at least %d replies", expectedMinReplies)

	h.t.Logf("Thread structure: %d total messages", len(replies))
	for i, msg := range replies {
		sender := "user"
		if msg.User == h.botUserID || msg.BotID != "" {
			sender = "bot"
		}
		h.t.Logf("  [%d] %s (ts=%s): %s", i, sender, msg.Timestamp, truncateText(msg.Text, 80))
	}
}
