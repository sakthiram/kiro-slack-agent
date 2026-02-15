package worker

import (
	"context"
	"sync"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/beads"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/queue"
	syncpkg "github.com/sakthiram/kiro-slack-agent/internal/sync"
	"go.uber.org/zap"
)

// WorkerPool manages a pool of workers that process tasks concurrently.
type WorkerPool struct {
	workers    []*Worker
	queue      *queue.TaskQueue
	beadsMgr   *beads.Manager
	syncer     *syncpkg.CommentSyncer
	cfg        *config.WorkerConfig
	kiroCfg    *config.KiroConfig
	logger     *zap.Logger
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	startTime  time.Time
	tasksDone  int
	tasksFailed int
	mu         sync.RWMutex
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(
	taskQueue *queue.TaskQueue,
	beadsMgr *beads.Manager,
	syncer *syncpkg.CommentSyncer,
	workerCfg *config.WorkerConfig,
	kiroCfg *config.KiroConfig,
	logger *zap.Logger,
) *WorkerPool {
	return &WorkerPool{
		workers:  make([]*Worker, 0, workerCfg.PoolSize),
		queue:    taskQueue,
		beadsMgr: beadsMgr,
		syncer:   syncer,
		cfg:      workerCfg,
		kiroCfg:  kiroCfg,
		logger:   logger,
	}
}

// Start launches all workers in the pool.
// Each worker runs in its own goroutine and begins processing tasks.
func (p *WorkerPool) Start(ctx context.Context) {
	p.logger.Info("starting worker pool",
		zap.Int("pool_size", p.cfg.PoolSize),
		zap.Duration("task_timeout", p.cfg.TaskTimeout),
	)

	p.startTime = time.Now()

	// Create a cancellable context for all workers
	ctx, p.cancel = context.WithCancel(ctx)

	// Create and start workers
	for i := 0; i < p.cfg.PoolSize; i++ {
		// Create a KiroRunner for each worker
		runner := NewKiroRunner(p.kiroCfg, p.cfg, p.logger)

		// Create the worker
		worker := NewWorker(
			i+1,
			p.queue,
			runner,
			p.beadsMgr,
			p.syncer,
			p.cfg,
			p.logger,
		)

		p.workers = append(p.workers, worker)

		// Start the worker in a goroutine
		p.wg.Add(1)
		go func(w *Worker) {
			defer p.wg.Done()
			w.Start(ctx)
		}(worker)

		p.logger.Info("worker started", zap.Int("worker_id", i+1))
	}

	// Start result monitoring goroutine
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.monitorResults(ctx)
	}()

	p.logger.Info("worker pool started",
		zap.Int("workers", len(p.workers)),
	)
}

// Stop gracefully shuts down all workers in the pool.
// Waits for all workers to complete their current tasks.
func (p *WorkerPool) Stop() {
	p.logger.Info("stopping worker pool")

	// Cancel the context to signal all workers to stop
	if p.cancel != nil {
		p.cancel()
	}

	// Wait for all workers to finish
	p.wg.Wait()

	p.logger.Info("worker pool stopped",
		zap.Duration("uptime", time.Since(p.startTime)),
		zap.Int("tasks_completed", p.tasksDone),
		zap.Int("tasks_failed", p.tasksFailed),
	)
}

// Stats returns current statistics about the worker pool.
type PoolStats struct {
	WorkerCount  int
	QueuePending int
	TasksDone    int
	TasksFailed  int
	Uptime       time.Duration
}

// Stats returns current statistics about the worker pool.
func (p *WorkerPool) Stats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	uptime := time.Duration(0)
	if !p.startTime.IsZero() {
		uptime = time.Since(p.startTime)
	}

	return PoolStats{
		WorkerCount:  len(p.workers),
		QueuePending: p.queue.Pending(),
		TasksDone:    p.tasksDone,
		TasksFailed:  p.tasksFailed,
		Uptime:       uptime,
	}
}

// monitorResults monitors task results and updates statistics.
func (p *WorkerPool) monitorResults(ctx context.Context) {
	results := p.queue.Results()

	for {
		select {
		case <-ctx.Done():
			return
		case result, ok := <-results:
			if !ok {
				// Results channel closed
				return
			}

			p.mu.Lock()
			if result.Success {
				p.tasksDone++
				p.logger.Info("task completed",
					zap.String("issue_id", result.IssueID),
					zap.Duration("duration", result.Duration),
				)
			} else {
				p.tasksFailed++
				p.logger.Error("task failed",
					zap.String("issue_id", result.IssueID),
					zap.Error(result.Error),
					zap.Duration("duration", result.Duration),
				)
			}
			p.mu.Unlock()
		}
	}
}

// IsRunning returns true if the worker pool is currently running.
func (p *WorkerPool) IsRunning() bool {
	return p.cancel != nil
}

// CancelTask finds and kills the agent process for a specific task.
// Returns true if a running task was found and cancelled.
func (p *WorkerPool) CancelTask(issueID string) bool {
	for _, w := range p.workers {
		if w.cancelIfRunning(issueID) {
			return true
		}
	}
	return false
}

// ResetTask clears the completed attempt count for a task.
func (p *WorkerPool) ResetTask(issueID string) {
	p.queue.ResetTask(issueID)
}

// BlockTask marks a task as human-blocked in the queue.
func (p *WorkerPool) BlockTask(issueID string) {
	p.queue.BlockTask(issueID)
}

// UnblockTask removes the human-blocked mark from the queue.
func (p *WorkerPool) UnblockTask(issueID string) {
	p.queue.UnblockTask(issueID)
}
