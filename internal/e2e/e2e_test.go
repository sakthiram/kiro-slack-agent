//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// Environment variable names
	envE2ETest        = "E2E_TEST"
	envTestChannelID  = "TEST_CHANNEL_ID"
	envBotUserID      = "BOT_USER_ID"
	envSlackBotToken  = "SLACK_BOT_TOKEN"
	envSlackAppToken  = "SLACK_APP_TOKEN"

	// Test timeouts
	botResponseTimeout = 120 * time.Second
	pollInterval       = 2 * time.Second
)

// e2eTestEnv holds environment configuration for E2E tests
type e2eTestEnv struct {
	channelID string
	botUserID string
	api       *slack.Client
}

// setupE2E checks for E2E_TEST=true and returns test environment
func setupE2E(t *testing.T) *e2eTestEnv {
	if os.Getenv(envE2ETest) != "true" {
		t.Skip("Skipping E2E test: E2E_TEST is not set to 'true'")
	}

	// Check required environment variables
	channelID := os.Getenv(envTestChannelID)
	if channelID == "" {
		t.Fatalf("E2E test requires %s environment variable (e.g., C1234567890)", envTestChannelID)
	}

	botUserID := os.Getenv(envBotUserID)
	if botUserID == "" {
		t.Fatalf("E2E test requires %s environment variable (e.g., U1234567890)", envBotUserID)
	}

	botToken := os.Getenv(envSlackBotToken)
	if botToken == "" {
		t.Fatalf("E2E test requires %s environment variable", envSlackBotToken)
	}
	if !strings.HasPrefix(botToken, "xoxb-") {
		t.Fatalf("%s must start with 'xoxb-'", envSlackBotToken)
	}

	appToken := os.Getenv(envSlackAppToken)
	if appToken == "" {
		t.Fatalf("E2E test requires %s environment variable", envSlackAppToken)
	}
	if !strings.HasPrefix(appToken, "xapp-") {
		t.Fatalf("%s must start with 'xapp-'", envSlackAppToken)
	}

	// Create Slack API client
	api := slack.New(botToken)

	t.Logf("E2E Test Environment:")
	t.Logf("  Channel ID: %s", channelID)
	t.Logf("  Bot User ID: %s", botUserID)

	return &e2eTestEnv{
		channelID: channelID,
		botUserID: botUserID,
		api:       api,
	}
}

// postMention posts a message mentioning the bot
func (env *e2eTestEnv) postMention(t *testing.T, text string) (string, string) {
	ctx := context.Background()

	// Format message with bot mention
	mentionText := fmt.Sprintf("<@%s> %s", env.botUserID, text)

	// Post message to channel
	_, timestamp, err := env.api.PostMessageContext(ctx, env.channelID,
		slack.MsgOptionText(mentionText, false),
		slack.MsgOptionDisableLinkUnfurl(),
	)
	require.NoError(t, err, "Failed to post mention message")

	t.Logf("Posted mention message (ts=%s): %s", timestamp, mentionText)
	return timestamp, timestamp // messageTS, threadTS (root message)
}

// postThreadReply posts a reply in a thread
func (env *e2eTestEnv) postThreadReply(t *testing.T, threadTS, text string) string {
	ctx := context.Background()

	// Post message in thread
	_, timestamp, err := env.api.PostMessageContext(ctx, env.channelID,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
		slack.MsgOptionDisableLinkUnfurl(),
	)
	require.NoError(t, err, "Failed to post thread reply")

	t.Logf("Posted thread reply (ts=%s, thread=%s): %s", timestamp, threadTS, text)
	return timestamp
}

// waitForBotReply waits for the bot to reply in a thread
func (env *e2eTestEnv) waitForBotReply(t *testing.T, threadTS, afterTS string) *slack.Message {
	ctx, cancel := context.WithTimeout(context.Background(), botResponseTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	t.Logf("Waiting for bot reply (thread=%s, after=%s)...", threadTS, afterTS)

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for bot reply after %v", botResponseTimeout)
			return nil
		case <-ticker.C:
			// Get conversation replies
			replies, _, _, err := env.api.GetConversationReplies(&slack.GetConversationRepliesParameters{
				ChannelID: env.channelID,
				Timestamp: threadTS,
			})
			if err != nil {
				t.Logf("Warning: Failed to get conversation replies: %v", err)
				continue
			}

			// Look for bot message after afterTS
			for _, msg := range replies {
				// Skip if message is before our trigger
				if msg.Timestamp <= afterTS {
					continue
				}

				// Check if this is from the bot
				if msg.User == env.botUserID || msg.BotID != "" {
					t.Logf("Found bot reply (ts=%s): %s", msg.Timestamp, msg.Text)
					return &msg
				}
			}

			t.Logf("No bot reply yet, continuing to poll...")
		}
	}
}

// waitForBotUpdate waits for the bot to update a specific message
func (env *e2eTestEnv) waitForBotUpdate(t *testing.T, channelID, messageTS string, minUpdates int) []*slack.Message {
	ctx, cancel := context.WithTimeout(context.Background(), botResponseTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	t.Logf("Waiting for bot to update message (ts=%s, min=%d updates)...", messageTS, minUpdates)

	var updates []*slack.Message
	lastText := ""
	updateCount := 0

	for {
		select {
		case <-ctx.Done():
			t.Logf("Timeout reached, found %d updates", updateCount)
			return updates
		case <-ticker.C:
			// Get message history
			params := &slack.GetConversationHistoryParameters{
				ChannelID: channelID,
				Latest:    messageTS,
				Inclusive: true,
				Limit:     1,
			}

			history, err := env.api.GetConversationHistory(params)
			if err != nil {
				t.Logf("Warning: Failed to get conversation history: %v", err)
				continue
			}

			if len(history.Messages) > 0 {
				msg := history.Messages[0]
				if msg.Timestamp == messageTS && msg.Text != lastText {
					t.Logf("Message update %d: %s", updateCount+1, msg.Text)
					lastText = msg.Text
					updateCount++
					updates = append(updates, &msg)

					// Check if we have enough updates and message looks complete
					if updateCount >= minUpdates && !strings.Contains(msg.Text, "Thinking") {
						return updates
					}
				}
			}
		}
	}
}

// cleanupMessages deletes test messages from the channel
func (env *e2eTestEnv) cleanupMessages(t *testing.T, timestamps ...string) {
	ctx := context.Background()

	for _, ts := range timestamps {
		if ts == "" {
			continue
		}

		_, _, err := env.api.DeleteMessageContext(ctx, env.channelID, ts)
		if err != nil {
			t.Logf("Warning: Failed to cleanup message %s: %v", ts, err)
		} else {
			t.Logf("Cleaned up message: %s", ts)
		}
	}
}

// TestE2E_MentionAndReply tests basic bot mention and reply functionality
func TestE2E_MentionAndReply(t *testing.T) {
	env := setupE2E(t)

	// Post a mention to the bot
	testMessage := "Hello! This is an E2E test. Please respond with a simple greeting."
	messageTS, threadTS := env.postMention(t, testMessage)
	defer env.cleanupMessages(t, threadTS) // Clean up thread

	// Wait for bot to reply
	reply := env.waitForBotReply(t, threadTS, messageTS)
	require.NotNil(t, reply, "Bot should reply to mention")

	// Verify reply has content
	assert.NotEmpty(t, reply.Text, "Bot reply should not be empty")
	assert.Greater(t, len(reply.Text), 10, "Bot reply should be substantive")

	t.Logf("Bot replied successfully with %d characters", len(reply.Text))
}

// TestE2E_ThreadContinuation tests that bot continues conversation in threads
func TestE2E_ThreadContinuation(t *testing.T) {
	env := setupE2E(t)

	// First message - start a thread
	firstMessage := "Let's have a conversation. What's 2 plus 2?"
	messageTS, threadTS := env.postMention(t, firstMessage)
	defer env.cleanupMessages(t, threadTS)

	// Wait for first reply
	firstReply := env.waitForBotReply(t, threadTS, messageTS)
	require.NotNil(t, firstReply, "Bot should reply to first message")
	firstReplyTS := firstReply.Timestamp

	t.Logf("First reply received: %s", firstReply.Text)

	// Give bot a moment to finish processing
	time.Sleep(3 * time.Second)

	// Second message - continue in thread (no mention needed)
	secondMessage := "Great! Now what's 10 minus 5?"
	secondMessageTS := env.postThreadReply(t, threadTS, secondMessage)

	// Wait for second reply
	secondReply := env.waitForBotReply(t, threadTS, secondMessageTS)
	require.NotNil(t, secondReply, "Bot should reply to thread continuation")

	// Verify both replies exist and are different
	assert.NotEqual(t, firstReplyTS, secondReply.Timestamp, "Second reply should be a new message")
	assert.NotEmpty(t, secondReply.Text, "Second reply should not be empty")

	t.Logf("Second reply received: %s", secondReply.Text)
	t.Logf("Thread continuation test passed")
}

// TestE2E_StreamingUpdates tests that bot streams progressive updates
func TestE2E_StreamingUpdates(t *testing.T) {
	env := setupE2E(t)

	// Post a question that should take some time to answer
	testMessage := "Write a short paragraph about the importance of testing in software development."
	messageTS, threadTS := env.postMention(t, testMessage)
	defer env.cleanupMessages(t, threadTS)

	// Wait for first reply (bot posts initial message)
	reply := env.waitForBotReply(t, threadTS, messageTS)
	require.NotNil(t, reply, "Bot should post initial message")
	replyTS := reply.Timestamp

	t.Logf("Initial message: %s", reply.Text)

	// Wait a bit and check for updates to the same message
	time.Sleep(5 * time.Second)

	// Get updated message
	updates := env.waitForBotUpdate(t, env.channelID, replyTS, 2)

	// Note: Streaming behavior depends on bot implementation
	// At minimum, we should see the initial and final message
	assert.NotEmpty(t, updates, "Should see message updates")

	if len(updates) > 0 {
		finalUpdate := updates[len(updates)-1]
		t.Logf("Final message (%d updates): %s", len(updates), finalUpdate.Text)
		assert.Greater(t, len(finalUpdate.Text), 50, "Final response should be substantive")
	}
}

// TestE2E_MultipleThreads tests bot handling multiple concurrent threads
func TestE2E_MultipleThreads(t *testing.T) {
	env := setupE2E(t)

	// Create two separate threads
	msg1 := "Thread 1: What is the capital of France?"
	messageTS1, threadTS1 := env.postMention(t, msg1)
	defer env.cleanupMessages(t, threadTS1)

	// Small delay to ensure messages are distinct
	time.Sleep(1 * time.Second)

	msg2 := "Thread 2: What is 7 times 8?"
	messageTS2, threadTS2 := env.postMention(t, msg2)
	defer env.cleanupMessages(t, threadTS2)

	// Verify threads are different
	require.NotEqual(t, threadTS1, threadTS2, "Should create separate threads")

	// Wait for replies in both threads
	reply1 := env.waitForBotReply(t, threadTS1, messageTS1)
	reply2 := env.waitForBotReply(t, threadTS2, messageTS2)

	// Both should get responses
	require.NotNil(t, reply1, "Bot should reply to thread 1")
	require.NotNil(t, reply2, "Bot should reply to thread 2")

	// Replies should be in their respective threads
	assert.NotEmpty(t, reply1.Text, "Thread 1 reply should not be empty")
	assert.NotEmpty(t, reply2.Text, "Thread 2 reply should not be empty")

	t.Logf("Thread 1 reply: %s", reply1.Text)
	t.Logf("Thread 2 reply: %s", reply2.Text)
}

// TestE2E_ErrorHandling tests bot behavior with invalid requests
func TestE2E_ErrorHandling(t *testing.T) {
	env := setupE2E(t)

	// Post a potentially problematic message
	testMessage := "" // Empty message
	messageTS, threadTS := env.postMention(t, testMessage)
	defer env.cleanupMessages(t, threadTS)

	// Bot should still respond (even if to say it doesn't understand)
	reply := env.waitForBotReply(t, threadTS, messageTS)

	// We expect some kind of response
	require.NotNil(t, reply, "Bot should respond even to empty/invalid messages")
	assert.NotEmpty(t, reply.Text, "Bot should provide some response text")

	t.Logf("Bot error handling response: %s", reply.Text)
}

// TestE2E_LongConversation tests extended conversation with multiple turns
func TestE2E_LongConversation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping long conversation test in short mode")
	}

	env := setupE2E(t)

	// Start conversation
	msg1 := "Let's count from 1 to 3. Start by saying 'one'."
	messageTS1, threadTS := env.postMention(t, msg1)
	defer env.cleanupMessages(t, threadTS)

	reply1 := env.waitForBotReply(t, threadTS, messageTS1)
	require.NotNil(t, reply1, "Should get first reply")
	lastReplyTS := reply1.Timestamp

	// Continue conversation
	messages := []string{
		"Good! Now say 'two'.",
		"Excellent! Now say 'three'.",
		"Perfect! What was the first number we said?",
	}

	for i, msg := range messages {
		time.Sleep(3 * time.Second) // Give bot time between messages

		msgTS := env.postThreadReply(t, threadTS, msg)
		reply := env.waitForBotReply(t, threadTS, msgTS)

		require.NotNil(t, reply, "Should get reply %d", i+2)
		assert.NotEqual(t, lastReplyTS, reply.Timestamp, "Should be a new message")
		lastReplyTS = reply.Timestamp

		t.Logf("Turn %d reply: %s", i+2, reply.Text)
	}

	t.Logf("Long conversation test completed successfully")
}

// TestE2E_SpecialCharacters tests bot handling of special characters
func TestE2E_SpecialCharacters(t *testing.T) {
	env := setupE2E(t)

	// Message with special characters
	testMessage := "Can you echo these symbols: @#$%^&*() and emojis: 🚀 🎉 ✨"
	messageTS, threadTS := env.postMention(t, testMessage)
	defer env.cleanupMessages(t, threadTS)

	// Wait for bot to reply
	reply := env.waitForBotReply(t, threadTS, messageTS)
	require.NotNil(t, reply, "Bot should handle special characters")

	assert.NotEmpty(t, reply.Text, "Bot should respond to special characters")

	t.Logf("Special character handling reply: %s", reply.Text)
}

// TestE2E_CodeBlock tests bot handling of code blocks
func TestE2E_CodeBlock(t *testing.T) {
	env := setupE2E(t)

	// Message with code block
	testMessage := "Explain this code:\n```python\ndef hello():\n    print('Hello, World!')\n```"
	messageTS, threadTS := env.postMention(t, testMessage)
	defer env.cleanupMessages(t, threadTS)

	// Wait for bot to reply
	reply := env.waitForBotReply(t, threadTS, messageTS)
	require.NotNil(t, reply, "Bot should handle code blocks")

	assert.NotEmpty(t, reply.Text, "Bot should respond to code blocks")
	assert.Greater(t, len(reply.Text), 20, "Bot should provide meaningful explanation")

	t.Logf("Code block handling reply: %s", reply.Text)
}
