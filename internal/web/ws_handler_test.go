package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// mockBridgeProvider implements BridgeProvider for testing
type mockBridgeProvider struct {
	mu          sync.RWMutex
	scrollback  map[session.SessionID][]byte
	hasBridge   map[session.SessionID]bool
	observers   map[string]chan []byte
}

func newMockBridgeProvider() *mockBridgeProvider {
	return &mockBridgeProvider{
		scrollback: make(map[session.SessionID][]byte),
		hasBridge:  make(map[session.SessionID]bool),
		observers:  make(map[string]chan []byte),
	}
}

func (m *mockBridgeProvider) GetScrollback(sessionID session.SessionID) []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scrollback[sessionID]
}

func (m *mockBridgeProvider) AddObserver(sessionID session.SessionID, observerID string) <-chan []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasBridge[sessionID] {
		return nil
	}
	ch := make(chan []byte, 100)
	m.observers[observerID] = ch
	return ch
}

func (m *mockBridgeProvider) RemoveObserver(sessionID session.SessionID, observerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.observers[observerID]; ok {
		close(ch)
		delete(m.observers, observerID)
	}
}

func (m *mockBridgeProvider) HasBridge(sessionID session.SessionID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hasBridge[sessionID]
}

// setSessionBridge sets up a mock bridge for a session
func (m *mockBridgeProvider) setSessionBridge(sessionID session.SessionID, scrollback []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hasBridge[sessionID] = true
	m.scrollback[sessionID] = scrollback
}

// sendOutput sends output to all observers
func (m *mockBridgeProvider) sendOutput(data []byte) {
	m.mu.RLock()
	// Copy channels to avoid holding lock during send
	channels := make([]chan []byte, 0, len(m.observers))
	for _, ch := range m.observers {
		channels = append(channels, ch)
	}
	m.mu.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- data:
		default:
		}
	}
}

func TestWSHandler_ProtocolMode(t *testing.T) {
	logger := zaptest.NewLogger(t)
	sessions := createTestSessionManager(t)
	registry := NewObserverRegistry(5, logger)
	bridges := newMockBridgeProvider()

	handler := NewWSHandler(bridges, sessions, registry, logger)

	// Create a test session
	ctx := context.Background()
	sess, _, err := sessions.GetOrCreate(ctx, "C123", "ts1", "U123")
	require.NoError(t, err)

	// Set up bridge for the session
	bridges.setSessionBridge(sess.ID, []byte("scrollback data"))

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	t.Run("attach to valid session", func(t *testing.T) {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer ws.Close()

		// Send attach message
		attachMsg := WSMessage{Type: MsgTypeAttach, SessionID: string(sess.ID)}
		data, _ := json.Marshal(attachMsg)
		err = ws.WriteMessage(websocket.TextMessage, data)
		require.NoError(t, err)

		// Read attached response
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respData, err := ws.ReadMessage()
		require.NoError(t, err)

		var resp WSMessage
		err = json.Unmarshal(respData, &resp)
		require.NoError(t, err)

		assert.Equal(t, MsgTypeAttached, resp.Type)
		assert.Equal(t, string(sess.ID), resp.SessionID)
		assert.Equal(t, "active", resp.Status)

		// Read scrollback
		_, scrollbackData, err := ws.ReadMessage()
		require.NoError(t, err)

		var scrollbackMsg WSMessage
		err = json.Unmarshal(scrollbackData, &scrollbackMsg)
		require.NoError(t, err)

		assert.Equal(t, MsgTypeScrollback, scrollbackMsg.Type)
		decodedScrollback, err := base64.StdEncoding.DecodeString(scrollbackMsg.Data)
		require.NoError(t, err)
		assert.Equal(t, "scrollback data", string(decodedScrollback))
	})

	t.Run("attach to non-existent session fails", func(t *testing.T) {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer ws.Close()

		// Send attach message for non-existent session
		attachMsg := WSMessage{Type: MsgTypeAttach, SessionID: "nonexistent"}
		data, _ := json.Marshal(attachMsg)
		err = ws.WriteMessage(websocket.TextMessage, data)
		require.NoError(t, err)

		// Read error response
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respData, err := ws.ReadMessage()
		require.NoError(t, err)

		var resp WSMessage
		err = json.Unmarshal(respData, &resp)
		require.NoError(t, err)

		assert.Equal(t, MsgTypeError, resp.Type)
		assert.Contains(t, resp.Message, "session not found")
	})

	t.Run("ping pong", func(t *testing.T) {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer ws.Close()

		// Send ping message
		pingMsg := WSMessage{Type: MsgTypePing}
		data, _ := json.Marshal(pingMsg)
		err = ws.WriteMessage(websocket.TextMessage, data)
		require.NoError(t, err)

		// Read pong response
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respData, err := ws.ReadMessage()
		require.NoError(t, err)

		var resp WSMessage
		err = json.Unmarshal(respData, &resp)
		require.NoError(t, err)

		assert.Equal(t, MsgTypePong, resp.Type)
	})

	t.Run("detach from session", func(t *testing.T) {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer ws.Close()

		// Attach first
		attachMsg := WSMessage{Type: MsgTypeAttach, SessionID: string(sess.ID)}
		data, _ := json.Marshal(attachMsg)
		ws.WriteMessage(websocket.TextMessage, data)

		// Skip attached and scrollback messages
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		ws.ReadMessage() // attached
		ws.ReadMessage() // scrollback

		// Send detach
		detachMsg := WSMessage{Type: MsgTypeDetach}
		data, _ = json.Marshal(detachMsg)
		err = ws.WriteMessage(websocket.TextMessage, data)
		require.NoError(t, err)

		// Read detached response
		_, respData, err := ws.ReadMessage()
		require.NoError(t, err)

		var resp WSMessage
		err = json.Unmarshal(respData, &resp)
		require.NoError(t, err)

		assert.Equal(t, MsgTypeDetached, resp.Type)
	})

	t.Run("invalid message format returns error", func(t *testing.T) {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer ws.Close()

		// Send invalid JSON
		err = ws.WriteMessage(websocket.TextMessage, []byte("not json"))
		require.NoError(t, err)

		// Read error response
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respData, err := ws.ReadMessage()
		require.NoError(t, err)

		var resp WSMessage
		err = json.Unmarshal(respData, &resp)
		require.NoError(t, err)

		assert.Equal(t, MsgTypeError, resp.Type)
		assert.Contains(t, resp.Message, "invalid message format")
	})

	t.Run("unknown message type returns error", func(t *testing.T) {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer ws.Close()

		// Send unknown message type
		unknownMsg := WSMessage{Type: "unknown_type"}
		data, _ := json.Marshal(unknownMsg)
		err = ws.WriteMessage(websocket.TextMessage, data)
		require.NoError(t, err)

		// Read error response
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, respData, err := ws.ReadMessage()
		require.NoError(t, err)

		var resp WSMessage
		err = json.Unmarshal(respData, &resp)
		require.NoError(t, err)

		assert.Equal(t, MsgTypeError, resp.Type)
		assert.Contains(t, resp.Message, "unknown message type")
	})
}

func TestWSHandler_OutputForwarding(t *testing.T) {
	logger := zaptest.NewLogger(t)
	sessions := createTestSessionManager(t)
	registry := NewObserverRegistry(5, logger)
	bridges := newMockBridgeProvider()

	handler := NewWSHandler(bridges, sessions, registry, logger)

	// Create a test session
	ctx := context.Background()
	sess, _, err := sessions.GetOrCreate(ctx, "C123", "ts1", "U123")
	require.NoError(t, err)

	// Set up bridge
	bridges.setSessionBridge(sess.ID, nil) // no scrollback

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws.Close()

	// Attach to session
	attachMsg := WSMessage{Type: MsgTypeAttach, SessionID: string(sess.ID)}
	data, _ := json.Marshal(attachMsg)
	ws.WriteMessage(websocket.TextMessage, data)

	// Read attached response
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	ws.ReadMessage()

	// Send output from bridge
	bridges.sendOutput([]byte("hello world"))

	// Read output message
	_, outputData, err := ws.ReadMessage()
	require.NoError(t, err)

	var outputMsg WSMessage
	err = json.Unmarshal(outputData, &outputMsg)
	require.NoError(t, err)

	assert.Equal(t, MsgTypeOutput, outputMsg.Type)
	decodedOutput, err := base64.StdEncoding.DecodeString(outputMsg.Data)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(decodedOutput))
}

func TestWSHandler_NilBridgeProvider(t *testing.T) {
	logger := zaptest.NewLogger(t)
	sessions := createTestSessionManager(t)
	registry := NewObserverRegistry(5, logger)

	// Create handler with nil bridge provider
	handler := NewWSHandler(nil, sessions, registry, logger)

	// Create a test session
	ctx := context.Background()
	sess, _, err := sessions.GetOrCreate(ctx, "C123", "ts1", "U123")
	require.NoError(t, err)

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws.Close()

	// Attach should work even without bridge provider
	attachMsg := WSMessage{Type: MsgTypeAttach, SessionID: string(sess.ID)}
	data, _ := json.Marshal(attachMsg)
	err = ws.WriteMessage(websocket.TextMessage, data)
	require.NoError(t, err)

	// Should still get attached response
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, respData, err := ws.ReadMessage()
	require.NoError(t, err)

	var resp WSMessage
	err = json.Unmarshal(respData, &resp)
	require.NoError(t, err)

	assert.Equal(t, MsgTypeAttached, resp.Type)
	assert.Equal(t, string(sess.ID), resp.SessionID)
}

func TestWSHandler_AttachWithEmptySessionID(t *testing.T) {
	logger := zaptest.NewLogger(t)
	sessions := createTestSessionManager(t)
	registry := NewObserverRegistry(5, logger)

	handler := NewWSHandler(nil, sessions, registry, logger)

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws.Close()

	// Attach with empty session ID
	attachMsg := WSMessage{Type: MsgTypeAttach, SessionID: ""}
	data, _ := json.Marshal(attachMsg)
	err = ws.WriteMessage(websocket.TextMessage, data)
	require.NoError(t, err)

	// Should get error response
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, respData, err := ws.ReadMessage()
	require.NoError(t, err)

	var resp WSMessage
	err = json.Unmarshal(respData, &resp)
	require.NoError(t, err)

	assert.Equal(t, MsgTypeError, resp.Type)
	assert.Contains(t, resp.Message, "session_id required")
}

func TestWSHandler_Close(t *testing.T) {
	logger := zaptest.NewLogger(t)
	sessions := createTestSessionManager(t)
	registry := NewObserverRegistry(5, logger)

	handler := NewWSHandler(nil, sessions, registry, logger)

	// Create a test session
	ctx := context.Background()
	_, _, err := sessions.GetOrCreate(ctx, "C123", "ts1", "U123")
	require.NoError(t, err)

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler.HandleConnection(r.Context(), conn)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect multiple clients
	ws1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws1.Close()

	ws2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer ws2.Close()

	// Give time for connections to be registered
	time.Sleep(50 * time.Millisecond)

	// Close the handler (should disconnect all clients gracefully)
	handler.Close()

	// Handler close should work without error
	// Note: the actual disconnection behavior is timing-dependent
}

// createTestSessionManager creates a session manager for testing
func createTestWSSessionManager(t *testing.T) *session.Manager {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	logger := zaptest.NewLogger(t)

	store, err := session.NewSQLiteStore(dbPath, logger)
	require.NoError(t, err)

	cfg := &config.SessionConfig{
		IdleTimeout:      30 * time.Minute,
		MaxSessionsTotal: 100,
		MaxSessionsUser:  5,
		DatabasePath:     dbPath,
	}

	kiroBasePath := filepath.Join(tmpDir, "kiro-sessions")

	return session.NewManager(store, cfg, kiroBasePath, logger)
}
