package kiro

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"go.uber.org/zap"
)

// Process manages a Kiro CLI instance via PTY.
type Process struct {
	cmd             *exec.Cmd
	pty             *os.File
	sessionDir      string
	binaryPath      string
	startupTimeout  time.Duration
	responseTimeout time.Duration
	mu              sync.Mutex
	running         bool
	parser          *Parser
	logger          *zap.Logger
}

// NewProcess creates a new Kiro process manager.
func NewProcess(sessionDir string, cfg *config.KiroConfig, logger *zap.Logger) *Process {
	return &Process{
		sessionDir:      sessionDir,
		binaryPath:      cfg.BinaryPath,
		startupTimeout:  cfg.StartupTimeout,
		responseTimeout: cfg.ResponseTimeout,
		parser:          NewParser(),
		logger:          logger,
	}
}

// Start initializes the Kiro CLI process with PTY.
func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	// Build command - use "chat" subcommand for interactive mode with auto-approval
	p.cmd = exec.CommandContext(ctx, p.binaryPath, "chat", "--trust-all-tools")
	p.cmd.Dir = p.sessionDir

	// Get Q_TERM version from environment or use kiro-cli version
	qTermVersion := os.Getenv("Q_TERM")
	if qTermVersion == "" {
		// Try to get version from kiro-cli --version
		if out, err := exec.Command(p.binaryPath, "--version").Output(); err == nil {
			// Output is like "kiro-cli 1.22.0"
			parts := strings.Fields(string(out))
			if len(parts) >= 2 {
				qTermVersion = parts[1]
			}
		}
		if qTermVersion == "" {
			qTermVersion = "1.22.0" // fallback
		}
	}

	p.cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		// Kiro CLI terminal integration - pretend we're in a kiro terminal
		"Q_TERM="+qTermVersion,
	)

	// Start with PTY
	var err error
	p.pty, err = pty.StartWithSize(p.cmd, &pty.Winsize{
		Rows: 40,
		Cols: 120,
	})
	if err != nil {
		return fmt.Errorf("failed to start PTY: %w", err)
	}

	p.running = true
	p.logger.Info("kiro process started",
		zap.String("session_dir", p.sessionDir),
		zap.Int("pid", p.cmd.Process.Pid),
	)

	// Wait for startup (initial prompt)
	if err := p.waitForStartup(ctx); err != nil {
		p.closeLocked()
		return fmt.Errorf("failed to wait for startup: %w", err)
	}

	return nil
}

// waitForStartup waits for the initial prompt.
func (p *Process) waitForStartup(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.startupTimeout)
	defer cancel()

	// Read output until we see a prompt
	buf := make([]byte, 4096)
	var output strings.Builder

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("startup timeout: %w", ctx.Err())
		default:
		}

		// Set read deadline
		p.pty.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		n, err := p.pty.Read(buf)
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			if err == io.EOF {
				return fmt.Errorf("process exited during startup")
			}
			// Ignore temporary errors
			continue
		}

		if n > 0 {
			output.Write(buf[:n])
			result := p.parser.Parse([]byte(output.String()))
			if result.IsComplete {
				return nil
			}
		}
	}
}

// SendMessage sends a message and streams the response.
func (p *Process) SendMessage(ctx context.Context, message string, handler ResponseHandler) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return fmt.Errorf("process not running")
	}

	// Write message to PTY - use \r (carriage return) for PTY input, not \n
	_, err := p.pty.Write([]byte(message + "\r"))
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	// Read response
	return p.readResponse(ctx, handler)
}

// readResult holds the result of a PTY read operation.
type readResult struct {
	n   int
	err error
}

// readResponse reads PTY output until completion.
func (p *Process) readResponse(ctx context.Context, handler ResponseHandler) error {
	ctx, cancel := context.WithTimeout(ctx, p.responseTimeout)
	defer cancel()

	var fullOutput strings.Builder
	noOutputTimeout := 5 * time.Second

	// Channel for PTY read results
	readCh := make(chan readResult, 1)

	for {
		// Start a read in a goroutine
		buf := make([]byte, 4096)
		go func() {
			n, err := p.pty.Read(buf)
			readCh <- readResult{n: n, err: err}
		}()

		// Wait for read, context, or silence timeout
		silenceTimer := time.NewTimer(noOutputTimeout)
		select {
		case <-ctx.Done():
			silenceTimer.Stop()
			if fullOutput.Len() > 0 {
				result := p.parser.Parse([]byte(fullOutput.String()))
				handler(result.CleanText, true)
			}
			return nil

		case <-silenceTimer.C:
			// Silence timeout - no data for 5 seconds
			if fullOutput.Len() > 0 {
				result := p.parser.Parse([]byte(fullOutput.String()))
				if result.HasContent {
					handler(result.CleanText, true)
					return nil
				}
			}
			// Continue waiting if no content yet

		case res := <-readCh:
			silenceTimer.Stop()
			if res.err != nil {
				if res.err == io.EOF {
					if fullOutput.Len() > 0 {
						result := p.parser.Parse([]byte(fullOutput.String()))
						handler(result.CleanText, true)
					}
					return nil
				}
				continue
			}

			if res.n > 0 {
				fullOutput.Write(buf[:res.n])

				// Check if response is complete
				result := p.parser.Parse([]byte(fullOutput.String()))
				if result.IsComplete {
					handler(result.CleanText, true)
					return nil
				}

				// Send intermediate chunk
				if result.HasContent {
					handler(result.CleanText, false)
				}
			}
		}
	}
}

// IsRunning checks if the process is alive.
func (p *Process) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// Close terminates the Kiro process gracefully.
func (p *Process) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

// closeLocked terminates the process without acquiring the mutex.
// Caller must hold p.mu.
func (p *Process) closeLocked() error {
	if !p.running {
		return nil
	}

	// Try graceful exit first
	if p.pty != nil {
		p.pty.Write([]byte("/exit\n"))
		time.Sleep(500 * time.Millisecond)
	}

	// Close PTY
	if p.pty != nil {
		p.pty.Close()
	}

	// Kill process if still running
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
		p.cmd.Wait()
	}

	p.running = false
	p.logger.Info("kiro process closed", zap.String("session_dir", p.sessionDir))

	return nil
}

// Ensure Process implements Bridge.
var _ Bridge = (*Process)(nil)
