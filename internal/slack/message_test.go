package slack

import (
	"testing"

	"github.com/slack-go/slack/slackevents"
	"github.com/stretchr/testify/assert"
)

func TestCleanMentionText(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		botUserID string
		want      string
	}{
		{
			name:      "simple mention at start",
			text:      "<@U123BOT> hello world",
			botUserID: "U123BOT",
			want:      "hello world",
		},
		{
			name:      "mention with display name",
			text:      "<@U123BOT|botname> hello world",
			botUserID: "U123BOT",
			want:      "hello world",
		},
		{
			name:      "mention in middle",
			text:      "hey <@U123BOT> hello world",
			botUserID: "U123BOT",
			want:      "hey  hello world",
		},
		{
			name:      "no mention",
			text:      "hello world",
			botUserID: "U123BOT",
			want:      "hello world",
		},
		{
			name:      "different user mention preserved",
			text:      "<@U999OTHER> hello <@U123BOT> world",
			botUserID: "U123BOT",
			want:      "<@U999OTHER> hello  world",
		},
		{
			name:      "only mention",
			text:      "<@U123BOT>",
			botUserID: "U123BOT",
			want:      "",
		},
		{
			name:      "empty text",
			text:      "",
			botUserID: "U123BOT",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanMentionText(tt.text, tt.botUserID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractMentions(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "single mention",
			text: "hello <@U123> world",
			want: []string{"U123"},
		},
		{
			name: "multiple mentions",
			text: "<@U123> and <@U456> are here",
			want: []string{"U123", "U456"},
		},
		{
			name: "mention with display name",
			text: "<@U123|john> hello",
			want: []string{"U123"},
		},
		{
			name: "duplicate mentions",
			text: "<@U123> hello <@U123>",
			want: []string{"U123"},
		},
		{
			name: "no mentions",
			text: "hello world",
			want: []string{},
		},
		{
			name: "empty text",
			text: "",
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMentions(tt.text)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTruncateMessage(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		maxLen int
		want   string
	}{
		{
			name:   "short text unchanged",
			text:   "hello",
			maxLen: 100,
			want:   "hello",
		},
		{
			name:   "exact length unchanged",
			text:   "hello",
			maxLen: 5,
			want:   "hello",
		},
		{
			name:   "truncates long text",
			text:   "hello world this is a long message",
			maxLen: 15,
			want:   "hello world...",
		},
		{
			name:   "uses default max when 0",
			text:   "hello",
			maxLen: 0,
			want:   "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateMessage(tt.text, tt.maxLen)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseAppMention(t *testing.T) {
	event := &slackevents.AppMentionEvent{
		Channel:         "C123",
		User:            "U456",
		Text:            "<@UBOT> hello world",
		TimeStamp:       "1234567890.123456",
		ThreadTimeStamp: "1234567890.000000",
	}

	msg := ParseAppMention(event, "UBOT")

	assert.Equal(t, "C123", msg.ChannelID)
	assert.Equal(t, "U456", msg.UserID)
	assert.Equal(t, "hello world", msg.Text)
	assert.Equal(t, "<@UBOT> hello world", msg.RawText)
	assert.Equal(t, "1234567890.000000", msg.ThreadTS)
	assert.Equal(t, "1234567890.123456", msg.MessageTS)
	assert.False(t, msg.IsDM)
	assert.True(t, msg.IsMention)
}

func TestParseDirectMessage(t *testing.T) {
	event := &slackevents.MessageEvent{
		Channel:         "D123",
		User:            "U456",
		Text:            "hello world",
		TimeStamp:       "1234567890.123456",
		ThreadTimeStamp: "",
	}

	msg := ParseDirectMessage(event)

	assert.Equal(t, "D123", msg.ChannelID)
	assert.Equal(t, "U456", msg.UserID)
	assert.Equal(t, "hello world", msg.Text)
	assert.Equal(t, "hello world", msg.RawText)
	assert.Empty(t, msg.ThreadTS)
	assert.Equal(t, "1234567890.123456", msg.MessageTS)
	assert.True(t, msg.IsDM)
	assert.False(t, msg.IsMention)
}

func TestIsBotMessage(t *testing.T) {
	tests := []struct {
		name  string
		event *slackevents.MessageEvent
		want  bool
	}{
		{
			name:  "regular user message",
			event: &slackevents.MessageEvent{User: "U123"},
			want:  false,
		},
		{
			name:  "bot message with BotID",
			event: &slackevents.MessageEvent{BotID: "B123"},
			want:  true,
		},
		{
			name:  "bot message subtype",
			event: &slackevents.MessageEvent{SubType: "bot_message"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBotMessage(tt.event)
			assert.Equal(t, tt.want, got)
		})
	}
}
