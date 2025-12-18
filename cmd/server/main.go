package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/logging"
	"github.com/sakthiram/kiro-slack-agent/internal/processor"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
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
		zap.String("sessions_base", cfg.Beads.SessionsBasePath),
	)

	// 3. Initialize beads manager for per-user context tracking
	beadsMgr := beads.NewManager(&cfg.Beads, logger)

	// 4. Create Slack client
	slackClient, err := slack.NewClient(
		cfg.Slack.BotToken,
		cfg.Slack.AppToken,
		cfg.Slack.DebugMode,
		logger,
	)
	if err != nil {
		logger.Fatal("failed to create slack client", zap.Error(err))
	}

	// 5. Create message processor (no bridge cache - fresh process per message)
	messageProcessor := processor.NewMessageProcessor(slackClient, beadsMgr, cfg, logger)

	// 6. Create message handler that wires everything together
	messageHandler := func(ctx context.Context, msg *slack.MessageEvent) error {
		return messageProcessor.ProcessMessage(ctx, msg)
	}

	// 7. Create Slack handler
	handler := slack.NewHandler(slackClient, messageHandler, logger)

	// 8. Setup Socket Mode
	api := slack.NewSlackAPI(cfg.Slack.BotToken, cfg.Slack.AppToken, cfg.Slack.DebugMode)
	socketClient := slack.NewSocketModeClient(api, cfg.Slack.DebugMode)
	handler.RegisterHandlers(socketClient)

	// 9. Handle shutdown gracefully
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutdown signal received")
		cancel()
	}()

	// 10. Run Socket Mode
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
