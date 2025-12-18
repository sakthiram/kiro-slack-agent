package processor

import (
	"context"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/kiro"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"github.com/sakthiram/kiro-slack-agent/internal/streaming"
	"go.uber.org/zap"
)

// BridgeCache provides an interface for managing Kiro bridges.
// This allows MessageProcessor to work with bridges without depending on the concrete implementation.
type BridgeCache interface {
	Get(id session.SessionID) (*kiro.ObservableProcess, bool)
	Set(id session.SessionID, bridge *kiro.ObservableProcess)
	Delete(id session.SessionID)
}

// SessionManager provides an interface for managing sessions.
// This allows MessageProcessor to work with sessions without depending on the concrete implementation.
type SessionManager interface {
	GetOrCreate(ctx context.Context, channelID, threadTS, userID string) (*session.Session, bool, error)
	UpdateStatus(ctx context.Context, id session.SessionID, status session.SessionStatus) error
}

// MessageProcessor handles processing of incoming Slack messages.
// It encapsulates all dependencies needed for message processing and provides
// a testable interface.
type MessageProcessor struct {
	slackClient slack.ClientInterface
	sessionMgr  SessionManager
	bridges     BridgeCache
	cfg         *config.Config
	logger      *zap.Logger
}

// NewMessageProcessor creates a new MessageProcessor with the given dependencies.
func NewMessageProcessor(
	slackClient slack.ClientInterface,
	sessionMgr SessionManager,
	bridges BridgeCache,
	cfg *config.Config,
	logger *zap.Logger,
) *MessageProcessor {
	return &MessageProcessor{
		slackClient: slackClient,
		sessionMgr:  sessionMgr,
		bridges:     bridges,
		cfg:         cfg,
		logger:      logger,
	}
}

// ProcessMessage handles a message from Slack.
func (p *MessageProcessor) ProcessMessage(
	ctx context.Context,
	msg *slack.MessageEvent,
) error {
	logger := p.logger.With(
		zap.String("channel_id", msg.ChannelID),
		zap.String("thread_ts", msg.ThreadTS),
		zap.String("user_id", msg.UserID),
	)

	// Determine thread TS (use message TS if no thread)
	threadTS := msg.ThreadTS
	if threadTS == "" {
		threadTS = msg.MessageTS
	}

	// Get or create session
	sess, isNew, err := p.sessionMgr.GetOrCreate(ctx, msg.ChannelID, threadTS, msg.UserID)
	if err != nil {
		logger.Error("failed to get/create session", zap.Error(err))
		// Post error to user
		p.slackClient.PostMessage(ctx, msg.ChannelID, ":x: Error: Unable to create session. Please try again.",
			slack.WithThreadTS(threadTS))
		return err
	}

	// Update session status to processing
	p.sessionMgr.UpdateStatus(ctx, sess.ID, session.SessionStatusProcessing)
	defer p.sessionMgr.UpdateStatus(ctx, sess.ID, session.SessionStatusActive)

	// Create streamer for this response
	streamer := streaming.NewStreamer(p.slackClient, &p.cfg.Streaming, logger)

	// Start streaming response
	_, err = streamer.Start(ctx, msg.ChannelID, threadTS)
	if err != nil {
		logger.Error("failed to start streamer", zap.Error(err))
		return err
	}

	// Get or create Kiro bridge (now using ObservableProcess)
	bridge, ok := p.bridges.Get(sess.ID)
	if !ok || !bridge.IsRunning() {
		// Create ObservableProcess which wraps Process and enables broadcasting
		observable := kiro.NewObservableProcess(sess.KiroSessionDir, &p.cfg.Kiro, logger)

		// Wrap with retry bridge for resilience
		var retryBridge kiro.Bridge = kiro.NewRetryBridge(observable, p.cfg.Kiro.MaxRetries, logger)

		if err := retryBridge.Start(ctx); err != nil {
			logger.Error("failed to start Kiro", zap.Error(err))
			streamer.Error(ctx, err)
			return err
		}

		// Store the observable process (not the retry wrapper) so we can add observers
		p.bridges.Set(sess.ID, observable)
		bridge = observable

		if isNew {
			logger.Info("created new Kiro session")
		}
	}

	// Send message to Kiro and stream response
	var finalResponse string
	err = bridge.SendMessage(ctx, msg.Text, func(chunk string, isComplete bool) {
		finalResponse = chunk
		if !isComplete {
			streamer.Update(ctx, chunk)
		}
	})

	if err != nil {
		logger.Error("Kiro error", zap.Error(err))
		streamer.Error(ctx, err)

		// Remove failed bridge
		p.bridges.Delete(sess.ID)
		bridge.Close()
		return err
	}

	// Complete streaming with final response
	if err := streamer.Complete(ctx, finalResponse); err != nil {
		logger.Error("failed to complete streamer", zap.Error(err))
		return err
	}

	return nil
}
