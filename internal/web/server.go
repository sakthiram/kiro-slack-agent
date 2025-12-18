package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

// Server handles HTTP and WebSocket connections for the web interface.
type Server struct {
	config    *config.WebConfig
	registry  *ObserverRegistry
	sessions  *session.Manager
	router    *http.ServeMux
	logger    *zap.Logger
	server    *http.Server
	listener  net.Listener
	auth      *AuthMiddleware
	mu        sync.Mutex
	started   bool
}

// NewServer creates a new web server.
func NewServer(
	cfg *config.WebConfig,
	registry *ObserverRegistry,
	sessions *session.Manager,
	logger *zap.Logger,
) (*Server, error) {
	// Create auth middleware
	auth, err := NewAuthMiddleware(cfg.AuthToken, cfg.AuthEnabled, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth middleware: %w", err)
	}

	s := &Server{
		config:   cfg,
		registry: registry,
		sessions: sessions,
		router:   http.NewServeMux(),
		logger:   logger,
		auth:     auth,
	}

	// Register routes
	s.registerRoutes()

	return s, nil
}

// AuthToken returns the authentication token for the web server.
// This is useful for logging at startup so users know the token.
func (s *Server) AuthToken() string {
	if s.auth != nil {
		return s.auth.Token()
	}
	return ""
}

// AuthEnabled returns whether authentication is enabled.
func (s *Server) AuthEnabled() bool {
	if s.auth != nil {
		return s.auth.Enabled()
	}
	return false
}

// registerRoutes sets up all HTTP endpoints.
func (s *Server) registerRoutes() {
	// Static file serving (public - serves the login page)
	if s.config.StaticPath != "" {
		fs := http.FileServer(http.Dir(s.config.StaticPath))
		s.router.Handle("/static/", http.StripPrefix("/static/", fs))
		s.router.HandleFunc("/", s.handleIndex)
	}

	// Health endpoint (public - for monitoring)
	s.router.HandleFunc("/api/health", s.handleHealth)

	// Protected REST API endpoints
	s.router.HandleFunc("/api/sessions", s.auth.Wrap(s.handleListSessions))
	s.router.HandleFunc("/api/sessions/", s.auth.Wrap(s.handleSessionDetails))

	// Protected WebSocket endpoint
	s.router.HandleFunc("/ws/sessions/", s.auth.Wrap(s.handleWebSocket))
}

// Start begins the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("server already started")
	}

	// Create listener
	listener, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	s.listener = listener
	s.server = &http.Server{
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.started = true

	// Start server in goroutine
	go func() {
		s.logger.Info("web server started",
			zap.String("address", s.listener.Addr().String()),
		)

		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error("server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	s.started = false

	if s.server != nil {
		if err := s.server.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown server: %w", err)
		}
	}

	s.logger.Info("web server stopped")
	return nil
}

// handleIndex serves the main index.html page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	indexPath := filepath.Join(s.config.StaticPath, "index.html")
	http.ServeFile(w, r, indexPath)
}

// handleHealth returns server health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// SessionListItem represents a session in the list view.
type SessionListItem struct {
	ID             string `json:"id"`
	ChannelID      string `json:"channel_id"`
	UserID         string `json:"user_id"`
	Status         string `json:"status"`
	CreatedAt      int64  `json:"created_at"`
	LastActivityAt int64  `json:"last_activity_at"`
	ObserverCount  int    `json:"observer_count"`
}

// handleListSessions returns a list of all active sessions.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	sessions, err := s.sessions.List(ctx)
	if err != nil {
		s.logger.Error("failed to list sessions", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Convert to response format
	items := make([]SessionListItem, 0, len(sessions))
	for _, sess := range sessions {
		items = append(items, SessionListItem{
			ID:             string(sess.ID),
			ChannelID:      sess.ChannelID,
			UserID:         sess.UserID,
			Status:         sess.Status.String(),
			CreatedAt:      sess.CreatedAt.Unix(),
			LastActivityAt: sess.LastActivityAt.Unix(),
			ObserverCount:  s.registry.ObserverCount(sess.ID),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": items,
		"total":    len(items),
	})
}

// SessionDetails represents detailed information about a session.
type SessionDetails struct {
	ID             string `json:"id"`
	ChannelID      string `json:"channel_id"`
	ThreadTS       string `json:"thread_ts"`
	UserID         string `json:"user_id"`
	KiroSessionDir string `json:"kiro_session_dir"`
	Status         string `json:"status"`
	CreatedAt      int64  `json:"created_at"`
	LastActivityAt int64  `json:"last_activity_at"`
	ObserverCount  int    `json:"observer_count"`
}

// handleSessionDetails returns detailed information about a specific session.
func (s *Server) handleSessionDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract session ID from path: /api/sessions/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	sessionID := session.SessionID(path)

	if sessionID == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	sess, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		s.logger.Error("failed to get session", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	details := SessionDetails{
		ID:             string(sess.ID),
		ChannelID:      sess.ChannelID,
		ThreadTS:       sess.ThreadTS,
		UserID:         sess.UserID,
		KiroSessionDir: sess.KiroSessionDir,
		Status:         sess.Status.String(),
		CreatedAt:      sess.CreatedAt.Unix(),
		LastActivityAt: sess.LastActivityAt.Unix(),
		ObserverCount:  s.registry.ObserverCount(sess.ID),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

// handleWebSocket upgrades the connection to WebSocket for streaming session output.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: /ws/sessions/{id}/stream
	path := strings.TrimPrefix(r.URL.Path, "/ws/sessions/")
	path = strings.TrimSuffix(path, "/stream")
	sessionID := session.SessionID(path)

	if sessionID == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	// Verify session exists
	ctx := r.Context()
	sess, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		s.logger.Error("failed to get session", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("failed to upgrade connection", zap.Error(err))
		return
	}

	// Configure WebSocket connection with ping/pong
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		return nil
	})
	// Set initial read deadline
	conn.SetReadDeadline(time.Now().Add(70 * time.Second))

	// Register observer
	observer, err := s.registry.Register(sess.ID, conn)
	if err != nil {
		s.logger.Error("failed to register observer", zap.Error(err))
		conn.Close()
		return
	}

	// Send initial session info
	initialMsg := map[string]interface{}{
		"type":      "init",
		"session":   sess.ID,
		"status":    sess.Status.String(),
		"timestamp": time.Now().Unix(),
	}
	if data, err := json.Marshal(initialMsg); err == nil {
		observer.send <- data
	}

	// Start send loop
	go observer.SendLoop(ctx)

	// Start ping ticker
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// Handle connection cleanup
	go func() {
		defer s.registry.Unregister(observer)

		// Read pump to detect disconnection and handle pings
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	// Send periodic pings
	go func() {
		for {
			select {
			case <-pingTicker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	s.logger.Info("websocket connection established",
		zap.String("session_id", string(sess.ID)),
		zap.String("remote_addr", r.RemoteAddr),
	)
}

// Addr returns the server's listening address (useful for testing).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}
