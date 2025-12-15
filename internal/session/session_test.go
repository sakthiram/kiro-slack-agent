package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSessionStatus_String(t *testing.T) {
	tests := []struct {
		status SessionStatus
		want   string
	}{
		{SessionStatusActive, "active"},
		{SessionStatusProcessing, "processing"},
		{SessionStatusIdle, "idle"},
		{SessionStatusClosed, "closed"},
		{SessionStatus(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.String())
		})
	}
}

func TestNewSession(t *testing.T) {
	session := NewSession("C123", "1234567890.123456", "U456", "/tmp/kiro/session1")

	assert.Equal(t, SessionID("1234567890.123456"), session.ID)
	assert.Equal(t, "C123", session.ChannelID)
	assert.Equal(t, "1234567890.123456", session.ThreadTS)
	assert.Equal(t, "U456", session.UserID)
	assert.Equal(t, "/tmp/kiro/session1", session.KiroSessionDir)
	assert.Equal(t, SessionStatusActive, session.Status)
	assert.False(t, session.CreatedAt.IsZero())
	assert.False(t, session.LastActivityAt.IsZero())
}

func TestSession_UpdateActivity(t *testing.T) {
	session := NewSession("C123", "1234567890.123456", "U456", "/tmp/kiro/session1")
	originalTime := session.LastActivityAt

	// Wait a tiny bit to ensure time difference
	time.Sleep(time.Millisecond)
	session.UpdateActivity()

	assert.True(t, session.LastActivityAt.After(originalTime))
}

func TestSession_IsIdle(t *testing.T) {
	session := NewSession("C123", "1234567890.123456", "U456", "/tmp/kiro/session1")

	// Just created, should not be idle
	assert.False(t, session.IsIdle(time.Second))

	// Simulate old activity
	session.LastActivityAt = time.Now().Add(-2 * time.Minute)

	// Should be idle if timeout is 1 minute
	assert.True(t, session.IsIdle(time.Minute))

	// Should not be idle if timeout is 5 minutes
	assert.False(t, session.IsIdle(5*time.Minute))
}
