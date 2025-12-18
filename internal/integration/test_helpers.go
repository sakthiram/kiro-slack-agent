package integration

import (
	"context"
	"sync"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/kiro"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
)

// MockSlackClient implements slack.ClientInterface for testing.
type MockSlackClient struct {
	mu            sync.Mutex
	PostedMsgs    []PostedMessage
	UpdatedMsgs   []UpdatedMessage
	AddedReacts   []Reaction
	RemovedReacts []Reaction
	BotUserID     string
	PostMsgErr    error
	UpdateMsgErr  error
	msgTsCounter  int
}

// PostedMessage records a posted message.
type PostedMessage struct {
	ChannelID string
	Text      string
	ThreadTS  string
}

// UpdatedMessage records an updated message.
type UpdatedMessage struct {
	ChannelID string
	TS        string
	Text      string
}

// Reaction records a reaction operation.
type Reaction struct {
	ChannelID string
	TS        string
	Emoji     string
}

// NewMockSlackClient creates a new mock Slack client.
func NewMockSlackClient(botUserID string) *MockSlackClient {
	return &MockSlackClient{
		BotUserID:     botUserID,
		PostedMsgs:    make([]PostedMessage, 0),
		UpdatedMsgs:   make([]UpdatedMessage, 0),
		AddedReacts:   make([]Reaction, 0),
		RemovedReacts: make([]Reaction, 0),
	}
}

func (m *MockSlackClient) PostMessage(ctx context.Context, channelID, text string, opts ...slack.MessageOption) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.PostMsgErr != nil {
		return "", m.PostMsgErr
	}

	m.PostedMsgs = append(m.PostedMsgs, PostedMessage{
		ChannelID: channelID,
		Text:      text,
	})

	m.msgTsCounter++
	return generateTS(m.msgTsCounter), nil
}

func (m *MockSlackClient) UpdateMessage(ctx context.Context, channelID, ts, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.UpdateMsgErr != nil {
		return m.UpdateMsgErr
	}

	m.UpdatedMsgs = append(m.UpdatedMsgs, UpdatedMessage{
		ChannelID: channelID,
		TS:        ts,
		Text:      text,
	})
	return nil
}

func (m *MockSlackClient) AddReaction(ctx context.Context, channelID, ts, emoji string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.AddedReacts = append(m.AddedReacts, Reaction{
		ChannelID: channelID,
		TS:        ts,
		Emoji:     emoji,
	})
	return nil
}

func (m *MockSlackClient) RemoveReaction(ctx context.Context, channelID, ts, emoji string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RemovedReacts = append(m.RemovedReacts, Reaction{
		ChannelID: channelID,
		TS:        ts,
		Emoji:     emoji,
	})
	return nil
}

func (m *MockSlackClient) GetBotUserID() string {
	return m.BotUserID
}

func generateTS(n int) string {
	return "1234567890." + string(rune('0'+n%10)) + "00000"
}

// MockBridge implements kiro.Bridge for testing.
type MockBridge struct {
	mu              sync.Mutex
	Started         bool
	Closed          bool
	Running         bool
	Messages        []string
	Responses       []string
	StartErr        error
	SendErr         error
	ResponseDelay   time.Duration
	ResponseHandler kiro.ResponseHandler
}

// NewMockBridge creates a new mock Kiro bridge.
func NewMockBridge() *MockBridge {
	return &MockBridge{
		Running:   true,
		Messages:  make([]string, 0),
		Responses: []string{"Default response"},
	}
}

func (m *MockBridge) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.StartErr != nil {
		return m.StartErr
	}
	m.Started = true
	m.Running = true
	return nil
}

func (m *MockBridge) SendMessage(ctx context.Context, message string, handler kiro.ResponseHandler) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.SendErr != nil {
		return m.SendErr
	}
	m.Messages = append(m.Messages, message)

	// Simulate response delay
	if m.ResponseDelay > 0 {
		time.Sleep(m.ResponseDelay)
	}

	// Send responses
	for i, response := range m.Responses {
		isComplete := i == len(m.Responses)-1
		handler(response, isComplete)
	}
	return nil
}

func (m *MockBridge) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Running
}

func (m *MockBridge) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Running = false
	m.Closed = true
	return nil
}

// Ensure MockBridge implements kiro.Bridge
var _ kiro.Bridge = (*MockBridge)(nil)
