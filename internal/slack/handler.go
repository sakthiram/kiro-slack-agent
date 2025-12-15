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

// Handler handles Slack Socket Mode events.
type Handler struct {
	client         *Client
	messageHandler MessageHandler
	logger         *zap.Logger
}

// NewHandler creates a new event handler.
func NewHandler(client *Client, messageHandler MessageHandler, logger *zap.Logger) *Handler {
	return &Handler{
		client:         client,
		messageHandler: messageHandler,
		logger:         logger,
	}
}

// RegisterHandlers sets up Socket Mode event handlers.
func (h *Handler) RegisterHandlers(socketClient *socketmode.Client) {
	go func() {
		for evt := range socketClient.Events {
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				h.handleEventsAPI(evt, socketClient)
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
		}
	}()
}

// handleEventsAPI routes Events API events.
func (h *Handler) handleEventsAPI(evt socketmode.Event, socketClient *socketmode.Client) {
	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		h.logger.Error("failed to cast event to EventsAPIEvent")
		return
	}

	// Acknowledge immediately
	socketClient.Ack(*evt.Request)

	switch eventsAPIEvent.Type {
	case slackevents.CallbackEvent:
		h.handleCallbackEvent(eventsAPIEvent)
	default:
		h.logger.Debug("unhandled events API type", zap.String("type", eventsAPIEvent.Type))
	}
}

// handleCallbackEvent routes callback events.
func (h *Handler) handleCallbackEvent(event slackevents.EventsAPIEvent) {
	innerEvent := event.InnerEvent

	switch ev := innerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		h.handleAppMention(ev)
	case *slackevents.MessageEvent:
		h.handleMessage(ev)
	default:
		h.logger.Debug("unhandled inner event type", zap.String("type", innerEvent.Type))
	}
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

	// Process asynchronously
	go func() {
		ctx := context.Background()
		if err := h.messageHandler(ctx, msg); err != nil {
			logger.Error("failed to process app mention", zap.Error(err))
		}
	}()
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

	logger.Info("received direct message")

	msg := ParseDirectMessage(ev)

	// Process asynchronously
	go func() {
		ctx := context.Background()
		if err := h.messageHandler(ctx, msg); err != nil {
			logger.Error("failed to process direct message", zap.Error(err))
		}
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
