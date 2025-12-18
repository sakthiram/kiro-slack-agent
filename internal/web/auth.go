package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"

	"go.uber.org/zap"
)

const (
	// AuthHeaderName is the HTTP header for bearer token authentication.
	AuthHeaderName = "Authorization"
	// AuthQueryParam is the query parameter for token authentication.
	AuthQueryParam = "token"
	// AuthTokenLength is the number of bytes for generated tokens (32 bytes = 64 hex chars).
	AuthTokenLength = 32
)

// AuthMiddleware provides token-based authentication for HTTP handlers.
type AuthMiddleware struct {
	token   string
	enabled bool
	logger  *zap.Logger
}

// NewAuthMiddleware creates a new authentication middleware.
// If token is empty and enabled is true, a secure random token is generated.
func NewAuthMiddleware(token string, enabled bool, logger *zap.Logger) (*AuthMiddleware, error) {
	m := &AuthMiddleware{
		token:   token,
		enabled: enabled,
		logger:  logger,
	}

	if enabled && token == "" {
		generatedToken, err := GenerateToken()
		if err != nil {
			return nil, err
		}
		m.token = generatedToken
		logger.Info("generated web observer auth token",
			zap.String("token", m.token),
			zap.String("usage", "Pass via 'Authorization: Bearer <token>' header or '?token=<token>' query param"),
		)
	}

	return m, nil
}

// Token returns the current authentication token.
func (m *AuthMiddleware) Token() string {
	return m.token
}

// Enabled returns whether authentication is enabled.
func (m *AuthMiddleware) Enabled() bool {
	return m.enabled
}

// Wrap wraps an HTTP handler with authentication checking.
// If authentication is disabled, the handler is returned unchanged.
func (m *AuthMiddleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	if !m.enabled {
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if !m.ValidateRequest(r) {
			m.logger.Debug("authentication failed",
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("path", r.URL.Path),
			)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ValidateRequest checks if the request contains a valid authentication token.
// It checks the Authorization header (Bearer token) and the token query parameter.
func (m *AuthMiddleware) ValidateRequest(r *http.Request) bool {
	if !m.enabled {
		return true
	}

	// Check Authorization header
	authHeader := r.Header.Get(AuthHeaderName)
	if authHeader != "" {
		// Support "Bearer <token>" format
		const bearerPrefix = "Bearer "
		if len(authHeader) > len(bearerPrefix) && authHeader[:len(bearerPrefix)] == bearerPrefix {
			token := authHeader[len(bearerPrefix):]
			if m.validateToken(token) {
				return true
			}
		}
		// Also support just the token directly in the header
		if m.validateToken(authHeader) {
			return true
		}
	}

	// Check query parameter
	queryToken := r.URL.Query().Get(AuthQueryParam)
	if queryToken != "" && m.validateToken(queryToken) {
		return true
	}

	return false
}

// validateToken performs constant-time comparison of tokens.
func (m *AuthMiddleware) validateToken(providedToken string) bool {
	return subtle.ConstantTimeCompare([]byte(m.token), []byte(providedToken)) == 1
}

// GenerateToken creates a cryptographically secure random token.
func GenerateToken() (string, error) {
	bytes := make([]byte, AuthTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
