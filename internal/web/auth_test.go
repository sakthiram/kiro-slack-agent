package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestNewAuthMiddleware(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("with provided token", func(t *testing.T) {
		token := "my-secret-token"
		auth, err := NewAuthMiddleware(token, true, logger)
		require.NoError(t, err)
		assert.Equal(t, token, auth.Token())
		assert.True(t, auth.Enabled())
	})

	t.Run("auto-generates token when empty", func(t *testing.T) {
		auth, err := NewAuthMiddleware("", true, logger)
		require.NoError(t, err)
		assert.NotEmpty(t, auth.Token())
		assert.Equal(t, AuthTokenLength*2, len(auth.Token())) // hex encoded
		assert.True(t, auth.Enabled())
	})

	t.Run("disabled auth", func(t *testing.T) {
		auth, err := NewAuthMiddleware("", false, logger)
		require.NoError(t, err)
		assert.Empty(t, auth.Token())
		assert.False(t, auth.Enabled())
	})
}

func TestAuthMiddleware_ValidateRequest(t *testing.T) {
	logger := zaptest.NewLogger(t)
	token := "test-auth-token-12345"
	auth, err := NewAuthMiddleware(token, true, logger)
	require.NoError(t, err)

	tests := []struct {
		name     string
		setup    func(*http.Request)
		expected bool
	}{
		{
			name: "valid bearer token in header",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+token)
			},
			expected: true,
		},
		{
			name: "valid token directly in header",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", token)
			},
			expected: true,
		},
		{
			name: "valid token in query param",
			setup: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("token", token)
				r.URL.RawQuery = q.Encode()
			},
			expected: true,
		},
		{
			name: "invalid bearer token",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer wrong-token")
			},
			expected: false,
		},
		{
			name: "invalid query token",
			setup: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("token", "wrong-token")
				r.URL.RawQuery = q.Encode()
			},
			expected: false,
		},
		{
			name:     "no token provided",
			setup:    func(r *http.Request) {},
			expected: false,
		},
		{
			name: "empty bearer prefix",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer ")
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
			tt.setup(req)
			assert.Equal(t, tt.expected, auth.ValidateRequest(req))
		})
	}
}

func TestAuthMiddleware_ValidateRequest_Disabled(t *testing.T) {
	logger := zaptest.NewLogger(t)
	auth, err := NewAuthMiddleware("", false, logger)
	require.NoError(t, err)

	// When auth is disabled, all requests should pass
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	assert.True(t, auth.ValidateRequest(req))
}

func TestAuthMiddleware_Wrap(t *testing.T) {
	logger := zaptest.NewLogger(t)
	token := "test-auth-token"
	auth, err := NewAuthMiddleware(token, true, logger)
	require.NoError(t, err)

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}

	wrapped := auth.Wrap(handler)

	t.Run("authorized request succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		wrapped(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "success", w.Body.String())
	})

	t.Run("unauthorized request fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
		w := httptest.NewRecorder()

		wrapped(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("query param auth succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sessions?token="+token, nil)
		w := httptest.NewRecorder()

		wrapped(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestAuthMiddleware_Wrap_Disabled(t *testing.T) {
	logger := zaptest.NewLogger(t)
	auth, err := NewAuthMiddleware("", false, logger)
	require.NoError(t, err)

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}

	wrapped := auth.Wrap(handler)

	// When auth is disabled, should pass through directly
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	wrapped(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "success", w.Body.String())
}

func TestGenerateToken(t *testing.T) {
	token1, err := GenerateToken()
	require.NoError(t, err)
	assert.Equal(t, AuthTokenLength*2, len(token1)) // hex encoding doubles length

	token2, err := GenerateToken()
	require.NoError(t, err)
	assert.NotEqual(t, token1, token2) // Should be unique
}

func TestServerAuthIntegration(t *testing.T) {
	logger := zaptest.NewLogger(t)
	token := "integration-test-token"

	t.Run("with auth enabled", func(t *testing.T) {
		cfg := &config.WebConfig{
			Enabled:                true,
			ListenAddr:             "127.0.0.1:0",
			StaticPath:             "",
			MaxObserversPerSession: 5,
			AuthEnabled:            true,
			AuthToken:              token,
		}

		registry := NewObserverRegistry(cfg.MaxObserversPerSession, logger)
		sessions := createTestSessionManager(t)
		server, err := NewServer(cfg, registry, sessions, logger)
		require.NoError(t, err)

		assert.True(t, server.AuthEnabled())
		assert.Equal(t, token, server.AuthToken())

		ctx := context.Background()
		err = server.Start(ctx)
		require.NoError(t, err)
		defer server.Stop(ctx)

		// Health endpoint should be public
		healthURL := fmt.Sprintf("http://%s/api/health", server.Addr())
		resp, err := http.Get(healthURL)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()

		// Sessions endpoint should require auth
		sessionsURL := fmt.Sprintf("http://%s/api/sessions", server.Addr())
		resp, err = http.Get(sessionsURL)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		resp.Body.Close()

		// Sessions endpoint with token should succeed
		req, err := http.NewRequest(http.MethodGet, sessionsURL, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("with auth disabled", func(t *testing.T) {
		cfg := &config.WebConfig{
			Enabled:                true,
			ListenAddr:             "127.0.0.1:0",
			StaticPath:             "",
			MaxObserversPerSession: 5,
			AuthEnabled:            false,
		}

		registry := NewObserverRegistry(cfg.MaxObserversPerSession, logger)
		sessions := createTestSessionManager(t)
		server, err := NewServer(cfg, registry, sessions, logger)
		require.NoError(t, err)

		assert.False(t, server.AuthEnabled())

		ctx := context.Background()
		err = server.Start(ctx)
		require.NoError(t, err)
		defer server.Stop(ctx)

		// Sessions endpoint should be accessible without auth
		sessionsURL := fmt.Sprintf("http://%s/api/sessions", server.Addr())
		resp, err := http.Get(sessionsURL)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	})
}

func TestWebSocketAuth(t *testing.T) {
	logger := zaptest.NewLogger(t)
	token := "ws-test-token"

	cfg := &config.WebConfig{
		Enabled:                true,
		ListenAddr:             "127.0.0.1:0",
		StaticPath:             "",
		MaxObserversPerSession: 5,
		AuthEnabled:            true,
		AuthToken:              token,
	}

	registry := NewObserverRegistry(cfg.MaxObserversPerSession, logger)
	sessions := createTestSessionManager(t)
	server, err := NewServer(cfg, registry, sessions, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop(ctx)

	// Create a test session
	sess, _, err := sessions.GetOrCreate(ctx, "C123", "ts1", "U123")
	require.NoError(t, err)
	require.NotNil(t, sess)

	t.Run("WebSocket without auth fails", func(t *testing.T) {
		wsURL := fmt.Sprintf("ws://%s/ws/sessions/ts1/stream", server.Addr())
		_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		assert.Error(t, err)
		if resp != nil {
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		}
	})

	t.Run("WebSocket with token query param succeeds", func(t *testing.T) {
		wsURL := fmt.Sprintf("ws://%s/ws/sessions/ts1/stream?token=%s", server.Addr(), token)
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer ws.Close()

		// Should receive init message
		ws.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, msg, err := ws.ReadMessage()
		require.NoError(t, err)

		var initMsg map[string]interface{}
		err = json.Unmarshal(msg, &initMsg)
		require.NoError(t, err)
		assert.Equal(t, "init", initMsg["type"])
	})

	t.Run("WebSocket with bearer header succeeds", func(t *testing.T) {
		wsURL := fmt.Sprintf("ws://%s/ws/sessions/ts1/stream", server.Addr())
		header := http.Header{}
		header.Set("Authorization", "Bearer "+token)
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		require.NoError(t, err)
		defer ws.Close()

		// Should receive init message
		ws.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, _, err = ws.ReadMessage()
		require.NoError(t, err)
	})
}
