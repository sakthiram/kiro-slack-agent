package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

	// Build the command
	cmd := exec.CommandContext(ctx, r.binaryPath,
		"chat",
		"--trust-all-tools",
		"--no-interactive",
		"--wrap", "never",
		prompt,
	)

	// Set working directory
	cmd.Dir = workDir

	// Set environment variables
	// TERM=dumb ensures no fancy terminal features are attempted
	cmd.Env = append(os.Environ(), "TERM=dumb")

	// Capture stdout and stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		r.logger.Error("kiro-cli execution failed",
			zap.String("work_dir", workDir),
			zap.Error(err),
			zap.String("output", string(output)),
		)
		return "", fmt.Errorf("kiro-cli execution failed: %w (output: %s)", err, string(output))
	}

	response := strings.TrimSpace(string(output))

	r.logger.Debug("kiro-cli execution completed",
		zap.String("work_dir", workDir),
		zap.Int("response_length", len(response)),
	)

	return response, nil
}
