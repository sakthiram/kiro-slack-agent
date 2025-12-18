package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/kiro"
	"github.com/sakthiram/kiro-slack-agent/internal/logging"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	"github.com/sakthiram/kiro-slack-agent/internal/streaming"
	"github.com/sakthiram/kiro-slack-agent/internal/web"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	// 1. Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	// 2. Setup logging
	logger, err := logging.NewLogger(&cfg.Logging)
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}
	defer logger.Sync()

	logger.Info("starting kiro-slack-agent",
		zap.String("kiro_binary", cfg.Kiro.BinaryPath),
		zap.String("session_base", cfg.Kiro.SessionBasePath),
	)

	// 3. Initialize SQLite session store
	store, err := session.NewSQLiteStore(cfg.Session.DatabasePath, logger)
	if err != nil {
		logger.Fatal("failed to create session store", zap.Error(err))
	}
	defer store.Close()

	// 4. Initialize session manager
	sessionMgr := session.NewManager(store, &cfg.Session, cfg.Kiro.SessionBasePath, logger)
	sessionMgr.Start() // Start cleanup goroutine
	defer sessionMgr.Stop()

	// 5. Create Slack client
	slackClient, err := slack.NewClient(
		cfg.Slack.BotToken,
		cfg.Slack.AppToken,
		cfg.Slack.DebugMode,
		logger,
	)
	if err != nil {
		logger.Fatal("failed to create slack client", zap.Error(err))
	}

	// 6. Create web observer registry
	observerRegistry := web.NewObserverRegistry(cfg.Web.MaxObserversPerSession, logger)

	// 7. Track active Kiro bridges per session (now using ObservableProcess)
	bridges := &bridgeCache{
		bridges:  make(map[session.SessionID]*kiro.ObservableProcess),
		logger:   logger,
		registry: observerRegistry,
	}

	// 8. Create web server (if enabled)
	var webServer *web.Server
	if cfg.Web.Enabled {
		webServer = web.NewServer(&cfg.Web, observerRegistry, sessionMgr, logger)
		ctx := context.Background()
		if err := webServer.Start(ctx); err != nil {
			logger.Error("failed to start web server", zap.Error(err))
		} else {
			logger.Info("web server started", zap.String("address", webServer.Addr()))
		}
	}

	// 9. Create message handler that wires everything together
	messageHandler := func(ctx context.Context, msg *slack.MessageEvent) error {
		return processMessage(ctx, msg, cfg, slackClient, sessionMgr, bridges, logger)
	}

	// 10. Create Slack handler
	handler := slack.NewHandler(slackClient, messageHandler, logger)

	// 11. Setup Socket Mode
	api := slack.NewSlackAPI(cfg.Slack.BotToken, cfg.Slack.AppToken, cfg.Slack.DebugMode)
	socketClient := slack.NewSocketModeClient(api, cfg.Slack.DebugMode)
	handler.RegisterHandlers(socketClient)

	// 12. Handle shutdown gracefully
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutdown signal received")
		cancel()

		// Close all bridges
		bridges.CloseAll()

		// Stop web server if running
		if webServer != nil {
			shutdownCtx := context.Background()
			if err := webServer.Stop(shutdownCtx); err != nil {
				logger.Error("failed to stop web server", zap.Error(err))
			}
		}
	}()

	// 13. Run Socket Mode
	logger.Info("connected, listening for events...")
	errChan := make(chan error, 1)
	slack.StartSocketMode(socketClient, errChan)

	select {
	case <-ctx.Done():
		logger.Info("shutting down...")
	case err := <-errChan:
		logger.Fatal("socket mode error", zap.Error(err))
	}
}

// bridgeCache manages active Kiro bridges per session.
// Now uses ObservableProcess to enable web observer broadcasting.
type bridgeCache struct {
	mu       sync.RWMutex
	bridges  map[session.SessionID]*kiro.ObservableProcess
	logger   *zap.Logger
	registry *web.ObserverRegistry
}

func (c *bridgeCache) Get(id session.SessionID) (*kiro.ObservableProcess, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.bridges[id]
	return b, ok
}

func (c *bridgeCache) Set(id session.SessionID, bridge *kiro.ObservableProcess) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bridges[id] = bridge

	// Start goroutine to broadcast observable output to web observers
	go c.broadcastToWebObservers(id, bridge)
}

func (c *bridgeCache) Delete(id session.SessionID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.bridges, id)
}

func (c *bridgeCache) CloseAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, bridge := range c.bridges {
		if err := bridge.Close(); err != nil {
			c.logger.Warn("failed to close bridge",
				zap.String("session_id", string(id)),
				zap.Error(err))
		}
	}
	c.bridges = make(map[session.SessionID]*kiro.ObservableProcess)
}

// broadcastToWebObservers creates an observer on the ObservableProcess
// and forwards all output to the web observer registry for WebSocket clients.
func (c *bridgeCache) broadcastToWebObservers(sessionID session.SessionID, bridge *kiro.ObservableProcess) {
	// Generate unique observer ID for this bridge connection
	observerID := "bridge-" + string(sessionID)

	// Register as observer on the observable process
	outputChan := bridge.AddObserver(observerID)

	c.logger.Debug("started broadcasting to web observers",
		zap.String("session_id", string(sessionID)),
		zap.String("observer_id", observerID),
	)

	// Forward all output from observable process to web observers
	for data := range outputChan {
		c.registry.Broadcast(sessionID, data)
	}

	// Clean up when done
	bridge.RemoveObserver(observerID)
	c.logger.Debug("stopped broadcasting to web observers",
		zap.String("session_id", string(sessionID)),
		zap.String("observer_id", observerID),
	)
}

// processMessage handles a message from Slack.
func processMessage(
	ctx context.Context,
	msg *slack.MessageEvent,
	cfg *config.Config,
	slackClient *slack.Client,
	sessionMgr *session.Manager,
	bridges *bridgeCache,
	logger *zap.Logger,
) error {
	logger = logger.With(
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
	sess, isNew, err := sessionMgr.GetOrCreate(ctx, msg.ChannelID, threadTS, msg.UserID)
	if err != nil {
		logger.Error("failed to get/create session", zap.Error(err))
		// Post error to user
		slackClient.PostMessage(ctx, msg.ChannelID, ":x: Error: Unable to create session. Please try again.",
			slack.WithThreadTS(threadTS))
		return err
	}

	// Update session status to processing
	sessionMgr.UpdateStatus(ctx, sess.ID, session.SessionStatusProcessing)
	defer sessionMgr.UpdateStatus(ctx, sess.ID, session.SessionStatusActive)

	// Create streamer for this response
	streamer := streaming.NewStreamer(slackClient, &cfg.Streaming, logger)

	// Start streaming response
	_, err = streamer.Start(ctx, msg.ChannelID, threadTS)
	if err != nil {
		logger.Error("failed to start streamer", zap.Error(err))
		return err
	}

	// Get or create Kiro bridge (now using ObservableProcess)
	bridge, ok := bridges.Get(sess.ID)
	if !ok || !bridge.IsRunning() {
		// Create ObservableProcess which wraps Process and enables broadcasting
		observable := kiro.NewObservableProcess(sess.KiroSessionDir, &cfg.Kiro, logger)

		// Wrap with retry bridge for resilience
		var retryBridge kiro.Bridge = kiro.NewRetryBridge(observable, cfg.Kiro.MaxRetries, logger)

		if err := retryBridge.Start(ctx); err != nil {
			logger.Error("failed to start Kiro", zap.Error(err))
			streamer.Error(ctx, err)
			return err
		}

		// Store the observable process (not the retry wrapper) so we can add observers
		bridges.Set(sess.ID, observable)
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
		bridges.Delete(sess.ID)
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
