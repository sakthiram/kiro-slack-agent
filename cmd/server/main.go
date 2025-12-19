package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/logging"
	"github.com/sakthiram/kiro-slack-agent/internal/processor"
	"github.com/sakthiram/kiro-slack-agent/internal/queue"
	"github.com/sakthiram/kiro-slack-agent/internal/slack"
	syncpkg "github.com/sakthiram/kiro-slack-agent/internal/sync"
	"github.com/sakthiram/kiro-slack-agent/internal/worker"
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

	// 5. Initialize async components
	// Task queue for holding bd issues ready to process
	taskQueue := queue.NewTaskQueue(100, 3) // capacity=100, maxRetries=3

	// Comment syncer for propagating Kiro responses back to Slack
	syncer := syncpkg.NewCommentSyncer(beadsMgr, slackClient, logger)

	// Feature processor routes Slack messages to bd (Feature or Task creation)
	featureProcessor := processor.NewFeatureProcessor(beadsMgr, syncer, slackClient, cfg, logger)

	// Worker pool for executing Kiro on ready tasks
	workerPool := worker.NewWorkerPool(taskQueue, beadsMgr, syncer, &cfg.Worker, &cfg.Kiro, logger)

	// Poller checks for bd issues in "ready" state and enqueues them
	poller := queue.NewPoller(taskQueue, beadsMgr, cfg.Beads.SessionsBasePath, cfg.Worker.PollInterval, logger)

	// 6. Create Slack handler with feature processor
	handler := slack.NewHandlerWithFeatureProcessor(slackClient, featureProcessor, logger)

	// 7. Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 8. Restore syncer state from beads on startup
	if cfg.Sync.Enabled {
		if err := syncer.Restore(ctx); err != nil {
			logger.Error("failed to restore syncer state", zap.Error(err))
			// Don't fail startup, just log the error
		}
	}

	// 9. Start async services before Slack handler
	go workerPool.Start(ctx)
	go poller.Start(ctx)
	if cfg.Sync.Enabled {
		go syncer.StartSyncLoop(ctx, cfg.Sync.SyncInterval)
	}

	logger.Info("async services started",
		zap.Int("worker_pool_size", cfg.Worker.PoolSize),
		zap.Duration("poll_interval", cfg.Worker.PollInterval),
		zap.Bool("sync_enabled", cfg.Sync.Enabled),
	)

	// 10. Setup Socket Mode
	api := slack.NewSlackAPI(cfg.Slack.BotToken, cfg.Slack.AppToken, cfg.Slack.DebugMode)
	socketClient := slack.NewSocketModeClient(api, cfg.Slack.DebugMode)
	handler.RegisterHandlers(socketClient)

	// 11. Handle shutdown gracefully
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutdown signal received")
		cancel()
	}()

	// 12. Run Socket Mode
	logger.Info("connected, listening for events...")
	errChan := make(chan error, 1)
	slack.StartSocketMode(socketClient, errChan)

	select {
	case <-ctx.Done():
		logger.Info("shutting down...")
		// Graceful shutdown: stop worker pool and close task queue
		workerPool.Stop()
		taskQueue.Close()
		// Give services time to cleanup
		time.Sleep(500 * time.Millisecond)
		logger.Info("shutdown complete")
	case err := <-errChan:
		logger.Fatal("socket mode error", zap.Error(err))
	}
}
