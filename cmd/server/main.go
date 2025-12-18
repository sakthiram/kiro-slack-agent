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
	"github.com/sakthiram/kiro-slack-agent/internal/processor"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
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
		var err error
		webServer, err = web.NewServer(&cfg.Web, observerRegistry, sessionMgr, logger)
		if err != nil {
			logger.Fatal("failed to create web server", zap.Error(err))
		}
		ctx := context.Background()
		if err := webServer.Start(ctx); err != nil {
			logger.Error("failed to start web server", zap.Error(err))
		} else {
			logger.Info("web server started",
				zap.String("address", webServer.Addr()),
				zap.Bool("auth_enabled", webServer.AuthEnabled()),
			)
			if webServer.AuthEnabled() {
				logger.Info("web observer authentication enabled",
					zap.String("token", webServer.AuthToken()),
					zap.String("usage", "Pass token via 'Authorization: Bearer <token>' header or '?token=<token>' query param"),
				)
			}
		}
	}

	// 9. Create message processor
	messageProcessor := processor.NewMessageProcessor(slackClient, sessionMgr, bridges, cfg, logger)

	// 10. Create message handler that wires everything together
	messageHandler := func(ctx context.Context, msg *slack.MessageEvent) error {
		return messageProcessor.ProcessMessage(ctx, msg)
	}

	// 11. Create Slack handler
	handler := slack.NewHandler(slackClient, messageHandler, logger)

	// 12. Setup Socket Mode
	api := slack.NewSlackAPI(cfg.Slack.BotToken, cfg.Slack.AppToken, cfg.Slack.DebugMode)
	socketClient := slack.NewSocketModeClient(api, cfg.Slack.DebugMode)
	handler.RegisterHandlers(socketClient)

	// 13. Handle shutdown gracefully
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

	// 14. Run Socket Mode
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
// Implements web.BridgeProvider interface for WebSocket handler access.
// Implements processor.BridgeCache interface for MessageProcessor.
type bridgeCache struct {
	mu       sync.RWMutex
	bridges  map[session.SessionID]*kiro.ObservableProcess
	logger   *zap.Logger
	registry *web.ObserverRegistry
}

// Ensure bridgeCache implements BridgeProvider
var _ web.BridgeProvider = (*bridgeCache)(nil)

// Ensure bridgeCache implements processor.BridgeCache
var _ processor.BridgeCache = (*bridgeCache)(nil)

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

// BridgeProvider interface implementation (for web.BridgeProvider)

// GetScrollback returns the current scrollback buffer for a session.
func (c *bridgeCache) GetScrollback(sessionID session.SessionID) []byte {
	c.mu.RLock()
	bridge, ok := c.bridges[sessionID]
	c.mu.RUnlock()

	if !ok || bridge == nil {
		return nil
	}
	return bridge.GetScrollback()
}

// AddObserver registers an observer for a session and returns a channel
// that receives output data.
func (c *bridgeCache) AddObserver(sessionID session.SessionID, observerID string) <-chan []byte {
	c.mu.RLock()
	bridge, ok := c.bridges[sessionID]
	c.mu.RUnlock()

	if !ok || bridge == nil {
		return nil
	}
	return bridge.AddObserver(observerID)
}

// RemoveObserver unregisters an observer for a session.
func (c *bridgeCache) RemoveObserver(sessionID session.SessionID, observerID string) {
	c.mu.RLock()
	bridge, ok := c.bridges[sessionID]
	c.mu.RUnlock()

	if ok && bridge != nil {
		bridge.RemoveObserver(observerID)
	}
}

// HasBridge returns true if a session has an active bridge.
func (c *bridgeCache) HasBridge(sessionID session.SessionID) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	bridge, ok := c.bridges[sessionID]
	return ok && bridge != nil
}
