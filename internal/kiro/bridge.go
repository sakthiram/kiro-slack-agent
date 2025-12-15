package kiro

import "context"

// ResponseHandler is called with response chunks as they stream in.
type ResponseHandler func(chunk string, isComplete bool)

// Bridge defines the interface for communicating with Kiro.
type Bridge interface {
	// Start initializes the Kiro process.
	Start(ctx context.Context) error

	// SendMessage sends a message and streams the response.
	SendMessage(ctx context.Context, message string, handler ResponseHandler) error

	// IsRunning checks if the process is alive.
	IsRunning() bool

	// Close terminates the Kiro process.
	Close() error
}
