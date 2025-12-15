package kiro

import (
	"bufio"
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

	// Build command
	p.cmd = exec.CommandContext(ctx, p.binaryPath)
	p.cmd.Dir = p.sessionDir
	p.cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
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
		p.Close()
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
				p.logger.Debug("startup complete", zap.String("output", result.CleanText))
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

	// Write message to PTY
	_, err := p.pty.Write([]byte(message + "\n"))
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	p.logger.Debug("sent message to kiro", zap.String("message", message))

	// Read response
	return p.readResponse(ctx, handler)
}

// readResponse reads PTY output until completion.
func (p *Process) readResponse(ctx context.Context, handler ResponseHandler) error {
	ctx, cancel := context.WithTimeout(ctx, p.responseTimeout)
	defer cancel()

	reader := bufio.NewReader(p.pty)
	var fullOutput strings.Builder
	lastChunkTime := time.Now()
	noOutputTimeout := 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			// Timeout - send what we have as complete
			if fullOutput.Len() > 0 {
				result := p.parser.Parse([]byte(fullOutput.String()))
				handler(result.CleanText, true)
			}
			return nil
		default:
		}

		// Set read deadline for non-blocking read
		p.pty.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		line, err := reader.ReadString('\n')
		if err != nil {
			if os.IsTimeout(err) {
				// Check for silence timeout (response likely complete)
				if time.Since(lastChunkTime) > noOutputTimeout && fullOutput.Len() > 0 {
					result := p.parser.Parse([]byte(fullOutput.String()))
					if result.HasContent {
						handler(result.CleanText, true)
						return nil
					}
				}
				continue
			}
			if err == io.EOF {
				break
			}
			continue
		}

		lastChunkTime = time.Now()
		fullOutput.WriteString(line)

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

	// Send final output
	if fullOutput.Len() > 0 {
		result := p.parser.Parse([]byte(fullOutput.String()))
		handler(result.CleanText, true)
	}

	return nil
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
