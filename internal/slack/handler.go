package slack

import (
	"context"
	"fmt"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"go.uber.org/zap"
)

// MessageHandler is the callback for processing messages.
type MessageHandler func(ctx context.Context, msg *MessageEvent) error

// FeatureProcessor handles async feature and task processing.
type FeatureProcessor interface {
	ProcessMainPost(ctx context.Context, msg *MessageEvent) error
	ProcessThreadReply(ctx context.Context, msg *MessageEvent) error
}

// TaskController handles reaction-based task control and feedback.
type TaskController interface {
	HandleReaction(ctx context.Context, userID, channelID, msgTS, reaction string)
	HandleReactionRemoved(ctx context.Context, userID, channelID, msgTS, reaction string)
	HandleFeedback(ctx context.Context, userID, channelID, threadTS, taskID, feedback string) error
}

// Handler handles Slack Socket Mode events.
type Handler struct {
	client           *Client
	messageHandler   MessageHandler
	featureProcessor FeatureProcessor
	taskController   TaskController
	logger           *zap.Logger
	syncMode         bool
}

// NewHandler creates a new event handler.
// The messageHandler parameter is deprecated - use NewHandlerWithFeatureProcessor instead.
func NewHandler(client *Client, messageHandler MessageHandler, logger *zap.Logger) *Handler {
	return &Handler{
		client:         client,
		messageHandler: messageHandler,
		logger:         logger,
	}
}

// NewHandlerWithFeatureProcessor creates a new event handler with async feature processing.
func NewHandlerWithFeatureProcessor(client *Client, featureProcessor FeatureProcessor, taskController TaskController, logger *zap.Logger) *Handler {
	return &Handler{
		client:           client,
		featureProcessor: featureProcessor,
		taskController:   taskController,
		logger:           logger,
	}
}

// SetSyncMode enables or disables synchronous message processing.
// When enabled, messages are processed synchronously (blocking) instead of async.
// This is useful for tests that need deterministic behavior.
func (h *Handler) SetSyncMode(enabled bool) {
	h.syncMode = enabled
}

// RegisterHandlers sets up Socket Mode event handlers.
func (h *Handler) RegisterHandlers(socketClient *socketmode.Client) {
	go func() {
		for evt := range socketClient.Events {
			// Acknowledge if needed and handle the event
			if err := h.HandleEvent(evt, socketClient); err != nil {
				h.logger.Error("failed to handle event", zap.Error(err), zap.String("type", string(evt.Type)))
			}
		}
	}()
}

// HandleEvent processes a single Socket Mode event synchronously.
// This method is exported for testing purposes.
// The socketClient parameter is optional and only used for acknowledging Events API events.
func (h *Handler) HandleEvent(evt socketmode.Event, socketClient *socketmode.Client) error {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		return h.handleEventsAPIEvent(evt, socketClient)
	case socketmode.EventTypeConnectionError:
		h.logger.Error("socket mode connection error", zap.Any("event", evt))
	case socketmode.EventTypeConnecting:
		h.logger.Info("connecting to Slack...")
	case socketmode.EventTypeConnected:
		h.logger.Info("connected to Slack")
	case socketmode.EventTypeHello:
		h.logger.Debug("received hello from Slack")
	default:
		h.logger.Debug("unhandled event type", zap.String("type", string(evt.Type)))
	}
	return nil
}

// handleEventsAPI routes Events API events (legacy, kept for backward compatibility).
func (h *Handler) handleEventsAPI(evt socketmode.Event, socketClient *socketmode.Client) {
	_ = h.handleEventsAPIEvent(evt, socketClient)
}

// handleEventsAPIEvent routes Events API events and returns any error.
func (h *Handler) handleEventsAPIEvent(evt socketmode.Event, socketClient *socketmode.Client) error {
	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return fmt.Errorf("failed to cast event to EventsAPIEvent")
	}

	// Acknowledge immediately if socketClient is provided
	if socketClient != nil && evt.Request != nil {
		socketClient.Ack(*evt.Request)
	}

	return h.handleCallbackEventSync(eventsAPIEvent)
}

// handleCallbackEvent routes callback events (legacy, kept for backward compatibility).
func (h *Handler) handleCallbackEvent(event slackevents.EventsAPIEvent) {
	_ = h.handleCallbackEventSync(event)
}

// handleCallbackEventSync routes callback events and returns any error.
func (h *Handler) handleCallbackEventSync(event slackevents.EventsAPIEvent) error {
	innerEvent := event.InnerEvent

	switch event.Type {
	case slackevents.CallbackEvent:
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			h.handleAppMention(ev)
		case *slackevents.MessageEvent:
			h.handleMessage(ev)
		case *slackevents.ReactionAddedEvent:
			h.handleReaction(ev)
		case *slackevents.ReactionRemovedEvent:
			h.handleReactionRemoved(ev)
		default:
			h.logger.Debug("unhandled inner event type", zap.String("type", innerEvent.Type))
		}
	default:
		h.logger.Debug("unhandled events API type", zap.String("type", event.Type))
	}
	return nil
}

// handleAppMention processes @mention events.
func (h *Handler) handleAppMention(ev *slackevents.AppMentionEvent) {
	logger := h.logger.With(
		zap.String("channel_id", ev.Channel),
		zap.String("user_id", ev.User),
		zap.String("message_ts", ev.TimeStamp),
	)

	logger.Info("received app mention")

	msg := ParseAppMention(ev, h.client.GetBotUserID())

	// Route to feature processor if available
	if h.featureProcessor != nil {
		h.handleWithFeatureProcessor(msg, logger)
		return
	}

	// Fallback to legacy message handler
	if h.messageHandler == nil {
		logger.Warn("no message handler or feature processor configured")
		return
	}

	if h.syncMode {
		// Process synchronously for tests
		ctx := context.Background()
		if err := h.messageHandler(ctx, msg); err != nil {
			logger.Error("failed to process app mention", zap.Error(err))
		}
	} else {
		// Process asynchronously
		go func() {
			ctx := context.Background()
			if err := h.messageHandler(ctx, msg); err != nil {
				logger.Error("failed to process app mention", zap.Error(err))
			}
		}()
	}
}

// handleMessage processes direct message events.
func (h *Handler) handleMessage(ev *slackevents.MessageEvent) {
	// Ignore bot messages to prevent loops
	if IsBotMessage(ev) {
		return
	}

	// Ignore message subtypes (edited, deleted, etc.) except for regular messages
	if ev.SubType != "" {
		return
	}

	// Only handle DMs (channels starting with D)
	if len(ev.Channel) == 0 || ev.Channel[0] != 'D' {
		return
	}

	logger := h.logger.With(
		zap.String("channel_id", ev.Channel),
		zap.String("user_id", ev.User),
		zap.String("message_ts", ev.TimeStamp),
	)

	logger.Info("received direct message",
		zap.String("thread_ts", ev.ThreadTimeStamp),
	)

	msg := ParseDirectMessage(ev)

	// Route to feature processor if available
	if h.featureProcessor != nil {
		h.handleWithFeatureProcessor(msg, logger)
		return
	}

	// Fallback to legacy message handler
	if h.messageHandler == nil {
		logger.Warn("no message handler or feature processor configured")
		return
	}

	if h.syncMode {
		// Process synchronously for tests
		ctx := context.Background()
		if err := h.messageHandler(ctx, msg); err != nil {
			logger.Error("failed to process direct message", zap.Error(err))
		}
	} else {
		// Process asynchronously
		go func() {
			ctx := context.Background()
			if err := h.messageHandler(ctx, msg); err != nil {
				logger.Error("failed to process direct message", zap.Error(err))
			}
		}()
	}
}

// handleWithFeatureProcessor routes messages to the async feature processor.
func (h *Handler) handleWithFeatureProcessor(msg *MessageEvent, logger *zap.Logger) {
	ctx := context.Background()

	// Determine if this is a main post or thread reply
	isMainPost := msg.ThreadTS == "" || msg.ThreadTS == msg.MessageTS

	logger.Debug("routing message",
		zap.String("thread_ts", msg.ThreadTS),
		zap.String("message_ts", msg.MessageTS),
		zap.Bool("is_dm", msg.IsDM),
		zap.Bool("is_main_post", isMainPost),
	)

	// Process async in goroutine
	go func() {
		var err error

		if isMainPost {
			logger.Info("routing to ProcessMainPost")
			err = h.featureProcessor.ProcessMainPost(ctx, msg)
		} else {
			// Check if this is feedback on an existing task (text starts with `taskID`)
			if h.taskController != nil {
				if taskID := extractFeedbackTarget(msg.Text); taskID != "" {
					// Strip the task ID prefix from the feedback text
					feedback := stripTaskPrefix(msg.Text, taskID)
					logger.Info("routing as feedback",
						zap.String("task_id", taskID),
						zap.String("feedback", feedback),
					)
					err = h.taskController.HandleFeedback(ctx, msg.UserID, msg.ChannelID, msg.ThreadTS, taskID, feedback)
					if err == nil {
						return // feedback handled, no 👀 needed
					}
					// Fall through to create new task if feedback routing failed
					logger.Warn("feedback routing failed, creating new task", zap.Error(err))
				}
			}

			logger.Info("routing to ProcessThreadReply")
			err = h.featureProcessor.ProcessThreadReply(ctx, msg)
		}

		if err != nil {
			logger.Error("feature processor error", zap.Error(err), zap.Bool("is_main_post", isMainPost))
			_ = h.client.AddReaction(ctx, msg.ChannelID, msg.MessageTS, "x")
			return
		}

		// React to user's message to acknowledge
		_ = h.client.AddReaction(ctx, msg.ChannelID, msg.MessageTS, "eyes")

		logger.Info("feature processing initiated", zap.Bool("is_main_post", isMainPost))
	}()
}

// NewSocketModeClient creates a new Socket Mode client.
// Note: The appToken should be passed to slack.New() with slack.OptionAppLevelToken().
func NewSocketModeClient(api *slack.Client, debug bool) *socketmode.Client {
	opts := []socketmode.Option{}
	if debug {
		opts = append(opts, socketmode.OptionDebug(debug))
	}
	return socketmode.New(api, opts...)
}

// NewSlackAPI creates a new Slack API client configured for Socket Mode.
func NewSlackAPI(botToken, appToken string, debug bool) *slack.Client {
	opts := []slack.Option{
		slack.OptionAppLevelToken(appToken),
	}
	if debug {
		opts = append(opts, slack.OptionDebug(debug))
	}
	return slack.New(botToken, opts...)
}

// RunSocketMode starts the Socket Mode client and blocks.
func RunSocketMode(socketClient *socketmode.Client) error {
	return socketClient.Run()
}

// StartSocketMode starts the Socket Mode client in a goroutine.
func StartSocketMode(socketClient *socketmode.Client, errChan chan<- error) {
	go func() {
		if err := socketClient.Run(); err != nil {
			errChan <- fmt.Errorf("socket mode error: %w", err)
		}
	}()
}

// handleReaction processes emoji reaction events for task control.
func (h *Handler) handleReaction(ev *slackevents.ReactionAddedEvent) {
	if h.taskController == nil {
		return
	}

	// Only handle reactions on messages
	if ev.Item.Type != "message" {
		return
	}

	// Only handle ⏸️ and 👍
	switch ev.Reaction {
	case "double_vertical_bar", "+1", "thumbsup":
		// valid human control reactions
	default:
		return
	}

	logger := h.logger.With(
		zap.String("reaction", ev.Reaction),
		zap.String("channel", ev.Item.Channel),
		zap.String("user", ev.User),
	)

	logger.Info("handling reaction event")

	go func() {
		ctx := context.Background()
		msgTS := ev.Item.Timestamp
		if msgTS == "" {
			return
		}
		h.taskController.HandleReaction(ctx, ev.User, ev.Item.Channel, msgTS, ev.Reaction)
	}()
}


// handleReactionRemoved processes emoji reaction removal events.
// Removing ⏸️ resumes a paused task.
func (h *Handler) handleReactionRemoved(ev *slackevents.ReactionRemovedEvent) {
	if h.taskController == nil || ev.Item.Type != "message" || ev.Reaction != "double_vertical_bar" {
		return
	}

	go func() {
		ctx := context.Background()
		msgTS := ev.Item.Timestamp
		if msgTS == "" {
			return
		}
		h.taskController.HandleReactionRemoved(ctx, ev.User, ev.Item.Channel, msgTS, ev.Reaction)
	}()
}

// extractFeedbackTarget checks if the message starts with a backtick-wrapped full task ID.
// Only matches at the start of the message. The ID must contain a '-' (full ID format).
// Example: "`slackW0175971WA3-6ai` fix the build" → "slackW0175971WA3-6ai"
// Counter-example: "check `slackW0175971WA3-6ai` status" → "" (not at start)
func extractFeedbackTarget(text string) string {
	if len(text) < 3 || text[0] != '`' {
		return ""
	}
	end := 1
	for end < len(text) && text[end] != '`' {
		end++
	}
	if end >= len(text) {
		return ""
	}
	id := text[1:end]
	// Must be a full task ID (contains '-')
	for _, c := range id {
		if c == '-' {
			return id
		}
	}
	return ""
}

// stripTaskPrefix removes the task ID prefix from feedback text.
func stripTaskPrefix(text, taskID string) string {
	prefix := "`" + taskID + "`"
	if len(text) > len(prefix) {
		rest := text[len(prefix):]
		// Strip leading whitespace and common separators
		for len(rest) > 0 && (rest[0] == ' ' || rest[0] == ':' || rest[0] == '-') {
			rest = rest[1:]
		}
		if rest != "" {
			return rest
		}
	}
	return text
}
