package slack

import (
	"regexp"
	"strings"

	"github.com/slack-go/slack/slackevents"
)

const (
	// SlackMessageMaxLength is the maximum length for a Slack message.
	SlackMessageMaxLength = 40000
)

var (
	// mentionRegex matches Slack user mentions: <@U123> or <@U123|displayname>
	mentionRegex = regexp.MustCompile(`<@([A-Z0-9]+)(?:\|[^>]*)?>`)
)

// MessageEvent represents a normalized incoming message.
type MessageEvent struct {
	ChannelID string // Channel where message was sent
	UserID    string // User who sent the message
	Text      string // Cleaned text (bot mentions removed)
	RawText   string // Original text
	ThreadTS  string // Parent thread (empty if root message)
	MessageTS string // This message's timestamp
	IsDM      bool   // Is this a direct message?
	IsMention bool   // Was bot @mentioned?
}

// CleanMentionText removes the bot mention from message text.
// "<@U123BOT> hello world" -> "hello world"
func CleanMentionText(text string, botUserID string) string {
	// Pattern to match the specific bot mention
	pattern := regexp.MustCompile(`<@` + regexp.QuoteMeta(botUserID) + `(?:\|[^>]*)?>`)
	cleaned := pattern.ReplaceAllString(text, "")
	return strings.TrimSpace(cleaned)
}

// ExtractMentions returns all user IDs mentioned in a message.
func ExtractMentions(text string) []string {
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	mentions := make([]string, 0, len(matches))
	seen := make(map[string]bool)

	for _, match := range matches {
		if len(match) > 1 && !seen[match[1]] {
			mentions = append(mentions, match[1])
			seen[match[1]] = true
		}
	}
	return mentions
}

// FormatResponse formats agent response for Slack.
// Handles basic formatting conversions.
func FormatResponse(text string) string {
	// Slack uses its own mrkdwn format which is similar to markdown
	// Most markdown works, but some adjustments may be needed

	// Convert ``` code blocks (already supported in Slack)
	// Convert bold/italic (already compatible)

	return text
}

// TruncateMessage ensures message doesn't exceed Slack limits.
func TruncateMessage(text string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = SlackMessageMaxLength
	}

	if len(text) <= maxLen {
		return text
	}

	// Truncate and add ellipsis
	truncated := text[:maxLen-3]
	// Try to truncate at a word boundary
	if lastSpace := strings.LastIndex(truncated, " "); lastSpace > maxLen-100 {
		truncated = truncated[:lastSpace]
	}
	return truncated + "..."
}

// ParseAppMention extracts MessageEvent from app_mention event.
func ParseAppMention(event *slackevents.AppMentionEvent, botUserID string) *MessageEvent {
	return &MessageEvent{
		ChannelID: event.Channel,
		UserID:    event.User,
		Text:      CleanMentionText(event.Text, botUserID),
		RawText:   event.Text,
		ThreadTS:  event.ThreadTimeStamp,
		MessageTS: event.TimeStamp,
		IsDM:      false,
		IsMention: true,
	}
}

// ParseDirectMessage extracts MessageEvent from message event in DMs.
func ParseDirectMessage(event *slackevents.MessageEvent) *MessageEvent {
	return &MessageEvent{
		ChannelID: event.Channel,
		UserID:    event.User,
		Text:      event.Text,
		RawText:   event.Text,
		ThreadTS:  event.ThreadTimeStamp,
		MessageTS: event.TimeStamp,
		IsDM:      true,
		IsMention: false,
	}
}

// IsBotMessage checks if a message event is from a bot.
func IsBotMessage(event *slackevents.MessageEvent) bool {
	return event.BotID != "" || event.SubType == "bot_message"
}
