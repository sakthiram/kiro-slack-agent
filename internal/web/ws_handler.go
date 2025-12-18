package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"go.uber.org/zap"
)

// BridgeProvider provides access to session bridges for WebSocket handlers.
// This allows the web layer to access ObservableProcess instances to get
// scrollback and observe output without directly depending on the kiro package.
type BridgeProvider interface {
	// GetScrollback returns the current scrollback buffer for a session.
	// Returns nil if the session has no active bridge.
	GetScrollback(sessionID session.SessionID) []byte

	// AddObserver registers an observer for a session and returns a channel
	// that receives output data. Returns nil if session has no active bridge.
	AddObserver(sessionID session.SessionID, observerID string) <-chan []byte

	// RemoveObserver unregisters an observer for a session.
	RemoveObserver(sessionID session.SessionID, observerID string)

	// HasBridge returns true if a session has an active bridge.
	HasBridge(sessionID session.SessionID) bool
}

// WSMessage represents a WebSocket protocol message.
type WSMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Data      string `json:"data,omitempty"`
	Message   string `json:"message,omitempty"`
	Status    string `json:"status,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

// Message types
const (
	MsgTypeAttach     = "attach"
	MsgTypeDetach     = "detach"
	MsgTypePing       = "ping"
	MsgTypeAttached   = "attached"
	MsgTypeScrollback = "scrollback"
	MsgTypeOutput     = "output"
	MsgTypePong       = "pong"
	MsgTypeError      = "error"
	MsgTypeDetached   = "detached"
)

// WSClient represents a connected WebSocket client.
type WSClient struct {
	conn         *websocket.Conn
	send         chan []byte
	sessionID    session.SessionID
	observerID   string
	outputChan   <-chan []byte
	attached     bool
	done         chan struct{}
	closeOnce    sync.Once
	mu           sync.Mutex
}

// WSHandler handles WebSocket connections for session observation.
type WSHandler struct {
	bridges   BridgeProvider
	sessions  *session.Manager
	registry  *ObserverRegistry
	logger    *zap.Logger
	clients   map[*WSClient]bool
	clientsMu sync.RWMutex
}

// NewWSHandler creates a new WebSocket handler.
func NewWSHandler(
	bridges BridgeProvider,
	sessions *session.Manager,
	registry *ObserverRegistry,
	logger *zap.Logger,
) *WSHandler {
	return &WSHandler{
		bridges:  bridges,
		sessions: sessions,
		registry: registry,
		logger:   logger,
		clients:  make(map[*WSClient]bool),
	}
}

// HandleConnection handles a new WebSocket connection.
func (h *WSHandler) HandleConnection(ctx context.Context, conn *websocket.Conn) {
	client := &WSClient{
		conn: conn,
		send: make(chan []byte, 256),
		done: make(chan struct{}),
	}

	h.clientsMu.Lock()
	h.clients[client] = true
	h.clientsMu.Unlock()

	defer func() {
		h.clientsMu.Lock()
		delete(h.clients, client)
		h.clientsMu.Unlock()
		h.detachClient(client)
		client.close()
	}()

	// Start send pump
	go h.sendPump(ctx, client)

	// Handle incoming messages
	h.readPump(ctx, client)
}

// readPump handles incoming messages from a WebSocket client.
func (h *WSHandler) readPump(ctx context.Context, client *WSClient) {
	defer client.close()

	// Set initial read deadline
	client.conn.SetReadDeadline(time.Now().Add(70 * time.Second))

	// Set pong handler
	client.conn.SetPongHandler(func(string) error {
		client.conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		return nil
	})

	for {
		select {
		case <-ctx.Done():
			return
		case <-client.done:
			return
		default:
		}

		_, msgData, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Debug("websocket read error", zap.Error(err))
			}
			return
		}

		var msg WSMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			h.sendError(client, "invalid message format")
			continue
		}

		h.handleMessage(ctx, client, &msg)
	}
}

// sendPump handles outgoing messages to a WebSocket client.
func (h *WSHandler) sendPump(ctx context.Context, client *WSClient) {
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case data, ok := <-client.send:
			if !ok {
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				h.logger.Debug("websocket write error", zap.Error(err))
				return
			}

		case <-pingTicker.C:
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
				return
			}

		case <-ctx.Done():
			return

		case <-client.done:
			return
		}
	}
}

// handleMessage processes a WebSocket protocol message.
func (h *WSHandler) handleMessage(ctx context.Context, client *WSClient, msg *WSMessage) {
	switch msg.Type {
	case MsgTypeAttach:
		h.handleAttach(ctx, client, msg.SessionID)
	case MsgTypeDetach:
		h.handleDetach(client)
	case MsgTypePing:
		h.handlePing(client)
	default:
		h.sendError(client, "unknown message type: "+msg.Type)
	}
}

// handleAttach attaches a client to a session's output stream.
func (h *WSHandler) handleAttach(ctx context.Context, client *WSClient, sessionIDStr string) {
	if sessionIDStr == "" {
		h.sendError(client, "session_id required")
		return
	}

	sessionID := session.SessionID(sessionIDStr)

	// Verify session exists
	sess, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		h.sendError(client, "session not found: "+sessionIDStr)
		return
	}

	// Detach from previous session if attached
	h.detachClient(client)

	client.mu.Lock()
	client.sessionID = sessionID
	client.observerID = generateID()
	client.attached = true
	client.mu.Unlock()

	h.logger.Info("client attached to session",
		zap.String("session_id", sessionIDStr),
		zap.String("observer_id", client.observerID),
	)

	// Send attached confirmation
	h.sendMessage(client, &WSMessage{
		Type:      MsgTypeAttached,
		SessionID: sessionIDStr,
		Status:    sess.Status.String(),
		Timestamp: time.Now().Unix(),
	})

	// Send scrollback if bridge is available
	if h.bridges != nil && h.bridges.HasBridge(sessionID) {
		scrollback := h.bridges.GetScrollback(sessionID)
		if len(scrollback) > 0 {
			h.sendMessage(client, &WSMessage{
				Type:      MsgTypeScrollback,
				SessionID: sessionIDStr,
				Data:      base64.StdEncoding.EncodeToString(scrollback),
				Timestamp: time.Now().Unix(),
			})
		}

		// Add observer to bridge for live output
		outputChan := h.bridges.AddObserver(sessionID, client.observerID)
		if outputChan != nil {
			client.mu.Lock()
			client.outputChan = outputChan
			client.mu.Unlock()

			// Start output forwarding goroutine
			go h.forwardOutput(ctx, client)
		}
	}

	// Also register with the observer registry for broadcast compatibility
	if h.registry != nil {
		h.registry.Register(sessionID, client.conn)
	}
}

// handleDetach detaches a client from the current session.
func (h *WSHandler) handleDetach(client *WSClient) {
	h.detachClient(client)

	h.sendMessage(client, &WSMessage{
		Type:      MsgTypeDetached,
		Timestamp: time.Now().Unix(),
	})
}

// handlePing responds to a ping message.
func (h *WSHandler) handlePing(client *WSClient) {
	h.sendMessage(client, &WSMessage{
		Type:      MsgTypePong,
		Timestamp: time.Now().Unix(),
	})
}

// detachClient detaches a client from its current session.
func (h *WSHandler) detachClient(client *WSClient) {
	client.mu.Lock()
	if !client.attached {
		client.mu.Unlock()
		return
	}

	sessionID := client.sessionID
	observerID := client.observerID
	client.attached = false
	client.sessionID = ""
	client.outputChan = nil
	client.mu.Unlock()

	// Remove from bridge observers
	if h.bridges != nil && observerID != "" {
		h.bridges.RemoveObserver(sessionID, observerID)
	}

	h.logger.Debug("client detached from session",
		zap.String("session_id", string(sessionID)),
		zap.String("observer_id", observerID),
	)
}

// forwardOutput forwards output from the bridge to the WebSocket client.
func (h *WSHandler) forwardOutput(ctx context.Context, client *WSClient) {
	client.mu.Lock()
	outputChan := client.outputChan
	sessionID := client.sessionID
	client.mu.Unlock()

	if outputChan == nil {
		return
	}

	for {
		select {
		case data, ok := <-outputChan:
			if !ok {
				return
			}

			client.mu.Lock()
			attached := client.attached
			client.mu.Unlock()

			if !attached {
				return
			}

			h.sendMessage(client, &WSMessage{
				Type:      MsgTypeOutput,
				SessionID: string(sessionID),
				Data:      base64.StdEncoding.EncodeToString(data),
				Timestamp: time.Now().Unix(),
			})

		case <-ctx.Done():
			return

		case <-client.done:
			return
		}
	}
}

// sendMessage sends a message to a WebSocket client.
func (h *WSHandler) sendMessage(client *WSClient, msg *WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("failed to marshal message", zap.Error(err))
		return
	}

	select {
	case client.send <- data:
	default:
		h.logger.Warn("client send channel full")
	}
}

// sendError sends an error message to a WebSocket client.
func (h *WSHandler) sendError(client *WSClient, message string) {
	h.sendMessage(client, &WSMessage{
		Type:      MsgTypeError,
		Message:   message,
		Timestamp: time.Now().Unix(),
	})
}

// close closes a client connection.
func (c *WSClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			c.conn.Close()
		}
	})
}

// Close performs graceful shutdown of the WebSocket handler.
func (h *WSHandler) Close() {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()

	for client := range h.clients {
		h.detachClient(client)
		client.close()
	}
	h.clients = make(map[*WSClient]bool)
}
