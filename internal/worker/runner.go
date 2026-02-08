package worker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
)

// KiroRunner handles non-interactive execution of kiro-cli for task processing.
type KiroRunner struct {
	binaryPath      string
	responseTimeout time.Duration
	logger          *zap.Logger
}

// NewKiroRunner creates a new KiroRunner instance.
func NewKiroRunner(cfg *config.KiroConfig, logger *zap.Logger) *KiroRunner {
	return &KiroRunner{
		binaryPath:      cfg.BinaryPath,
		responseTimeout: cfg.ResponseTimeout,
		logger:          logger,
	}
}

// Run executes kiro-cli in non-interactive mode and returns the response.
// The command is run with --trust-all-tools, --no-interactive, and --wrap never flags.
// It kills the entire process group on timeout to prevent orphaned child processes.
func (r *KiroRunner) Run(ctx context.Context, workDir, prompt string) (string, error) {
	// Create a timeout context if not already set
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.responseTimeout)
		defer cancel()
	}

	r.logger.Debug("running kiro-cli",
		zap.String("work_dir", workDir),
		zap.String("binary", r.binaryPath),
		zap.Duration("timeout", r.responseTimeout),
	)

	cmd := exec.Command(r.binaryPath,
		"chat",
		"--trust-all-tools",
		"--no-interactive",
		"--wrap", "never",
		prompt,
	)
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "TERM=dumb")

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start kiro-cli: %w", err)
	}

	// Watch for context cancellation and kill the entire process group
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			r.logger.Error("kiro-cli execution failed",
				zap.String("work_dir", workDir),
				zap.Error(err),
				zap.String("output", buf.String()),
			)
			return "", fmt.Errorf("kiro-cli execution failed: %w (output: %s)", err, buf.String())
		}
	case <-ctx.Done():
		// Kill entire process group (negative PID)
		pgid := cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done // wait for direct child to exit

		// On macOS, grandchild processes may linger in stopped state.
		// Retry kill to ensure full cleanup.
		for i := 0; i < 3; i++ {
			if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
				break // process group no longer exists
			}
			time.Sleep(100 * time.Millisecond)
		}

		r.logger.Warn("kiro-cli killed by timeout",
			zap.String("work_dir", workDir),
			zap.String("output", buf.String()),
		)
		return "", fmt.Errorf("kiro-cli timed out: %w", ctx.Err())
	}

	response := strings.TrimSpace(buf.String())

	r.logger.Debug("kiro-cli execution completed",
		zap.String("work_dir", workDir),
		zap.Int("response_length", len(response)),
	)

	return response, nil
}
