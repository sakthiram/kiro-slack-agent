package web

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sakthiram/kiro-slack-agent/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestNewObserverRegistry(t *testing.T) {
	logger := zaptest.NewLogger(t)
	maxObservers := 10

	registry := NewObserverRegistry(maxObservers, logger)

	assert.NotNil(t, registry)
	assert.Equal(t, maxObservers, registry.maxObservers)
	assert.NotNil(t, registry.observers)
	assert.Empty(t, registry.observers)
}

func TestObserverRegistry_Register(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(2, logger) // Max 2 observers

	sessionID := session.SessionID("test-session")

	// Create mock WebSocket connections
	conn1 := &websocket.Conn{}
	conn2 := &websocket.Conn{}
	conn3 := &websocket.Conn{}

	t.Run("register first observer", func(t *testing.T) {
		observer, err := registry.Register(sessionID, conn1)
		require.NoError(t, err)
		assert.NotNil(t, observer)
		assert.Equal(t, sessionID, observer.sessionID)
		assert.Equal(t, conn1, observer.conn)
		assert.NotNil(t, observer.send)
		assert.NotNil(t, observer.done)
		assert.Equal(t, 1, registry.ObserverCount(sessionID))
	})

	t.Run("register second observer", func(t *testing.T) {
		observer, err := registry.Register(sessionID, conn2)
		require.NoError(t, err)
		assert.NotNil(t, observer)
		assert.Equal(t, 2, registry.ObserverCount(sessionID))
	})

	t.Run("register third observer fails (max reached)", func(t *testing.T) {
		observer, err := registry.Register(sessionID, conn3)
		assert.Error(t, err)
		assert.Nil(t, observer)
		assert.Contains(t, err.Error(), "max observers")
		assert.Equal(t, 2, registry.ObserverCount(sessionID))
	})
}

func TestObserverRegistry_Unregister(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(5, logger)

	sessionID := session.SessionID("test-session")
	conn1 := &websocket.Conn{}
	conn2 := &websocket.Conn{}

	// Register two observers
	observer1, err := registry.Register(sessionID, conn1)
	require.NoError(t, err)

	observer2, err := registry.Register(sessionID, conn2)
	require.NoError(t, err)

	assert.Equal(t, 2, registry.ObserverCount(sessionID))

	t.Run("unregister first observer", func(t *testing.T) {
		registry.Unregister(observer1)
		assert.Equal(t, 1, registry.ObserverCount(sessionID))

		// Verify done channel is closed
		select {
		case <-observer1.done:
			// Channel is closed, expected
		default:
			t.Fatal("done channel should be closed")
		}
	})

	t.Run("unregister second observer cleans up session", func(t *testing.T) {
		registry.Unregister(observer2)
		assert.Equal(t, 0, registry.ObserverCount(sessionID))

		// Session should be removed from map
		registry.mu.RLock()
		_, exists := registry.observers[sessionID]
		registry.mu.RUnlock()
		assert.False(t, exists)
	})
}

func TestObserverRegistry_Broadcast(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(5, logger)

	sessionID := session.SessionID("test-session")
	conn1 := &websocket.Conn{}
	conn2 := &websocket.Conn{}

	// Register two observers
	observer1, err := registry.Register(sessionID, conn1)
	require.NoError(t, err)

	observer2, err := registry.Register(sessionID, conn2)
	require.NoError(t, err)

	t.Run("broadcast to all observers", func(t *testing.T) {
		testData := []byte("test message")
		registry.Broadcast(sessionID, testData)

		// Both observers should receive the message
		select {
		case msg := <-observer1.send:
			assert.Equal(t, testData, msg)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("observer1 did not receive message")
		}

		select {
		case msg := <-observer2.send:
			assert.Equal(t, testData, msg)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("observer2 did not receive message")
		}
	})

	t.Run("broadcast to non-existent session", func(t *testing.T) {
		// Should not panic
		registry.Broadcast(session.SessionID("non-existent"), []byte("test"))
	})
}

func TestObserverRegistry_ObserverCount(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(5, logger)

	sessionID1 := session.SessionID("session-1")
	sessionID2 := session.SessionID("session-2")

	// Initially zero
	assert.Equal(t, 0, registry.ObserverCount(sessionID1))
	assert.Equal(t, 0, registry.ObserverCount(sessionID2))

	// Register observers for session 1
	conn1 := &websocket.Conn{}
	conn2 := &websocket.Conn{}
	registry.Register(sessionID1, conn1)
	registry.Register(sessionID1, conn2)

	assert.Equal(t, 2, registry.ObserverCount(sessionID1))
	assert.Equal(t, 0, registry.ObserverCount(sessionID2))

	// Register observer for session 2
	conn3 := &websocket.Conn{}
	registry.Register(sessionID2, conn3)

	assert.Equal(t, 2, registry.ObserverCount(sessionID1))
	assert.Equal(t, 1, registry.ObserverCount(sessionID2))
}

func TestObserver_SendLoop(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(5, logger)

	sessionID := session.SessionID("test-session")

	// Note: We can't fully test SendLoop without a real WebSocket connection,
	// but we can test the channel behavior

	t.Run("send channel has correct capacity", func(t *testing.T) {
		conn := &websocket.Conn{}
		observer, err := registry.Register(sessionID, conn)
		require.NoError(t, err)

		// Channel should have capacity of 256
		assert.Equal(t, 256, cap(observer.send))
	})

	t.Run("done channel can be closed", func(t *testing.T) {
		conn := &websocket.Conn{}
		observer, err := registry.Register(sessionID, conn)
		require.NoError(t, err)

		// Unregister should close done channel
		registry.Unregister(observer)

		select {
		case <-observer.done:
			// Expected
		default:
			t.Fatal("done channel should be closed")
		}
	})
}

func TestObserverRegistry_ConcurrentAccess(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(100, logger)

	sessionID := session.SessionID("test-session")
	done := make(chan struct{})

	// Concurrent registrations
	go func() {
		for i := 0; i < 50; i++ {
			conn := &websocket.Conn{}
			registry.Register(sessionID, conn)
		}
		done <- struct{}{}
	}()

	// Concurrent broadcasts
	go func() {
		for i := 0; i < 50; i++ {
			registry.Broadcast(sessionID, []byte("test"))
		}
		done <- struct{}{}
	}()

	// Concurrent counts
	go func() {
		for i := 0; i < 50; i++ {
			registry.ObserverCount(sessionID)
		}
		done <- struct{}{}
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Should not panic and count should be valid
	count := registry.ObserverCount(sessionID)
	assert.GreaterOrEqual(t, count, 0)
	assert.LessOrEqual(t, count, 50)
}

func TestObserverRegistry_MultipleSessionIsolation(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(5, logger)

	session1 := session.SessionID("session-1")
	session2 := session.SessionID("session-2")

	// Register observers for both sessions
	conn1 := &websocket.Conn{}
	conn2 := &websocket.Conn{}

	observer1, err := registry.Register(session1, conn1)
	require.NoError(t, err)

	observer2, err := registry.Register(session2, conn2)
	require.NoError(t, err)

	// Broadcast to session 1
	testData1 := []byte("message for session 1")
	registry.Broadcast(session1, testData1)

	// Only observer1 should receive it
	select {
	case msg := <-observer1.send:
		assert.Equal(t, testData1, msg)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("observer1 did not receive message")
	}

	// observer2 should not receive it
	select {
	case <-observer2.send:
		t.Fatal("observer2 should not receive message for session 1")
	case <-time.After(100 * time.Millisecond):
		// Expected
	}

	// Broadcast to session 2
	testData2 := []byte("message for session 2")
	registry.Broadcast(session2, testData2)

	// Only observer2 should receive it
	select {
	case msg := <-observer2.send:
		assert.Equal(t, testData2, msg)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("observer2 did not receive message")
	}

	// observer1 should not receive it
	select {
	case <-observer1.send:
		t.Fatal("observer1 should not receive message for session 2")
	case <-time.After(100 * time.Millisecond):
		// Expected
	}
}

func TestObserver_SendLoopCancellation(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(5, logger)

	sessionID := session.SessionID("test-session")

	// Use nil connection to test cancellation without actual WebSocket
	observer, err := registry.Register(sessionID, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	// Start SendLoop (should handle nil connection gracefully)
	sendLoopDone := make(chan struct{})
	go func() {
		observer.SendLoop(ctx)
		close(sendLoopDone)
	}()

	// Cancel context
	cancel()

	// SendLoop should exit
	select {
	case <-sendLoopDone:
		// Expected
	case <-time.After(1 * time.Second):
		t.Fatal("SendLoop did not exit on context cancellation")
	}
}

func TestObserverRegistry_BroadcastChannelFull(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistry(5, logger)

	sessionID := session.SessionID("test-session")
	conn := &websocket.Conn{}

	observer, err := registry.Register(sessionID, conn)
	require.NoError(t, err)

	// Fill the channel to capacity (256)
	for i := 0; i < 256; i++ {
		observer.send <- []byte("test")
	}

	// Next broadcast should skip this observer (channel full)
	// Should not block or panic
	registry.Broadcast(sessionID, []byte("overflow message"))

	// Channel should still be at capacity
	assert.Equal(t, 256, len(observer.send))
}
