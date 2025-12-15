package streaming

import (
	"context"
	"fmt"
	"sync"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"go.uber.org/zap"
)

const (
	thinkingEmoji    = ":thinking_face:"
	writingEmoji     = ":writing_hand:"
	errorEmoji       = ":x:"
	thinkingMessage  = thinkingEmoji + " Thinking..."
)

// UpdaterInterface defines the streaming updater for testing.
type UpdaterInterface interface {
	Start(ctx context.Context, channelID, threadTS string) (string, error)
	Update(ctx context.Context, content string) error
	Complete(ctx context.Context, finalContent string) error
	Error(ctx context.Context, err error) error
}

// Streamer coordinates progressive Slack message updates.
type Streamer struct {
	client      slack.ClientInterface
	config      *config.StreamingConfig
	channelID   string
	threadTS    string
	messageTS   string // The message being updated
	buffer      *OutputBuffer
	mu          sync.Mutex
	started     bool
	completed   bool
	logger      *zap.Logger
}

// NewStreamer creates a new streaming message updater.
func NewStreamer(client slack.ClientInterface, cfg *config.StreamingConfig, logger *zap.Logger) *Streamer {
	return &Streamer{
		client: client,
		config: cfg,
		logger: logger,
	}
}

// Start posts initial placeholder message and prepares for streaming updates.
// Returns the message timestamp for the posted message.
func (s *Streamer) Start(ctx context.Context, channelID, threadTS string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return "", fmt.Errorf("streamer already started")
	}

	s.channelID = channelID
	s.threadTS = threadTS

	// Post initial placeholder message
	var opts []slack.MessageOption
	if threadTS != "" {
		opts = append(opts, slack.WithThreadTS(threadTS))
	}

	ts, err := s.client.PostMessage(ctx, channelID, thinkingMessage, opts...)
	if err != nil {
		return "", fmt.Errorf("failed to post initial message: %w", err)
	}

	s.messageTS = ts
	s.started = true

	// Initialize buffer with update callback
	s.buffer = NewOutputBuffer(s.config.UpdateInterval, func(content string) error {
		return s.doUpdate(content)
	})

	s.logger.Debug("streamer started",
		zap.String("channel_id", channelID),
		zap.String("thread_ts", threadTS),
		zap.String("message_ts", ts))

	return ts, nil
}

// Update accumulates content and triggers buffered updates.
func (s *Streamer) Update(ctx context.Context, content string) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return fmt.Errorf("streamer not started")
	}
	if s.completed {
		s.mu.Unlock()
		return nil // Ignore updates after completion
	}
	buffer := s.buffer
	s.mu.Unlock()

	return buffer.Append(content)
}

// doUpdate is called by buffer to update Slack message with streaming indicator.
func (s *Streamer) doUpdate(content string) error {
	s.mu.Lock()
	channelID := s.channelID
	messageTS := s.messageTS
	completed := s.completed
	s.mu.Unlock()

	if completed {
		return nil
	}

	// Add streaming indicator
	text := content + " " + writingEmoji

	ctx := context.Background()
	if err := s.client.UpdateMessage(ctx, channelID, messageTS, text); err != nil {
		s.logger.Error("failed to update streaming message",
			zap.String("channel_id", channelID),
			zap.String("message_ts", messageTS),
			zap.Error(err))
		return err
	}

	return nil
}

// Complete finalizes the message with final content (no indicator).
func (s *Streamer) Complete(ctx context.Context, finalContent string) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return fmt.Errorf("streamer not started")
	}
	if s.completed {
		s.mu.Unlock()
		return nil
	}

	s.completed = true
	channelID := s.channelID
	messageTS := s.messageTS

	// Stop buffer to cancel pending updates
	if s.buffer != nil {
		s.buffer.Stop()
	}
	s.mu.Unlock()

	// Update with final content (no indicator)
	if err := s.client.UpdateMessage(ctx, channelID, messageTS, finalContent); err != nil {
		s.logger.Error("failed to complete streaming message",
			zap.String("channel_id", channelID),
			zap.String("message_ts", messageTS),
			zap.Error(err))
		return fmt.Errorf("failed to complete message: %w", err)
	}

	s.logger.Debug("streamer completed",
		zap.String("channel_id", channelID),
		zap.String("message_ts", messageTS))

	return nil
}

// Error marks the message as errored.
func (s *Streamer) Error(ctx context.Context, err error) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return fmt.Errorf("streamer not started")
	}
	if s.completed {
		s.mu.Unlock()
		return nil
	}

	s.completed = true
	channelID := s.channelID
	messageTS := s.messageTS

	// Stop buffer
	if s.buffer != nil {
		s.buffer.Stop()
	}
	s.mu.Unlock()

	// Format error message
	errorMsg := fmt.Sprintf("%s Error: %s", errorEmoji, err.Error())

	if updateErr := s.client.UpdateMessage(ctx, channelID, messageTS, errorMsg); updateErr != nil {
		s.logger.Error("failed to update error message",
			zap.String("channel_id", channelID),
			zap.String("message_ts", messageTS),
			zap.Error(updateErr))
		return fmt.Errorf("failed to update error message: %w", updateErr)
	}

	s.logger.Debug("streamer error posted",
		zap.String("channel_id", channelID),
		zap.String("message_ts", messageTS),
		zap.Error(err))

	return nil
}

// MessageTS returns the timestamp of the message being updated.
func (s *Streamer) MessageTS() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageTS
}

// IsStarted returns whether the streamer has been started.
func (s *Streamer) IsStarted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

// IsCompleted returns whether the streamer has completed.
func (s *Streamer) IsCompleted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completed
}
