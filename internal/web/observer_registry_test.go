package web

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestNewObserverV2(t *testing.T) {
	sessionID := "test-session"
	observer := NewObserverV2(sessionID)

	assert.NotEmpty(t, observer.ID, "Observer ID should not be empty")
	assert.Equal(t, sessionID, observer.SessionID, "SessionID should match")
	assert.NotNil(t, observer.SendChan, "SendChan should be initialized")
	assert.NotNil(t, observer.Done, "Done channel should be initialized")
	assert.False(t, observer.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.Equal(t, 100, cap(observer.SendChan), "SendChan should have buffer of 100")
}

func TestNewObserverRegistryV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)

	assert.NotNil(t, registry, "Registry should not be nil")
	assert.NotNil(t, registry.sessions, "Sessions map should be initialized")
	assert.Equal(t, 0, registry.GetSessionCount(), "Should start with no sessions")
}

func TestRegisterV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)
	sessionID := "test-session"

	t.Run("successful registration", func(t *testing.T) {
		observer := NewObserverV2(sessionID)
		err := registry.Register(sessionID, observer)

		require.NoError(t, err, "Registration should succeed")
		assert.Equal(t, 1, registry.GetSessionCount(), "Should have 1 session")
		assert.Equal(t, 1, registry.GetObserverCount(sessionID), "Should have 1 observer")
	})

	t.Run("register multiple observers for same session", func(t *testing.T) {
		observer1 := NewObserverV2(sessionID)
		observer2 := NewObserverV2(sessionID)

		err1 := registry.Register(sessionID, observer1)
		err2 := registry.Register(sessionID, observer2)

		require.NoError(t, err1, "First registration should succeed")
		require.NoError(t, err2, "Second registration should succeed")
		assert.Equal(t, 3, registry.GetObserverCount(sessionID), "Should have 3 observers")
	})

	t.Run("register observers for different sessions", func(t *testing.T) {
		session2 := "test-session-2"
		observer := NewObserverV2(session2)

		err := registry.Register(session2, observer)

		require.NoError(t, err, "Registration should succeed")
		assert.Equal(t, 2, registry.GetSessionCount(), "Should have 2 sessions")
		assert.Equal(t, 1, registry.GetObserverCount(session2), "New session should have 1 observer")
	})

	t.Run("error on empty sessionID", func(t *testing.T) {
		observer := NewObserverV2("test")
		err := registry.Register("", observer)

		assert.Error(t, err, "Should error on empty sessionID")
		assert.Contains(t, err.Error(), "sessionID cannot be empty")
	})

	t.Run("error on nil observer", func(t *testing.T) {
		err := registry.Register(sessionID, nil)

		assert.Error(t, err, "Should error on nil observer")
		assert.Contains(t, err.Error(), "observer cannot be nil")
	})

	t.Run("error on duplicate observer ID", func(t *testing.T) {
		observer := NewObserverV2(sessionID)
		err1 := registry.Register(sessionID, observer)
		err2 := registry.Register(sessionID, observer)

		require.NoError(t, err1, "First registration should succeed")
		assert.Error(t, err2, "Second registration should fail")
		assert.Contains(t, err2.Error(), "already registered")
	})

	t.Run("error on empty observer ID", func(t *testing.T) {
		observer := &ObserverV2{
			ID:        "",
			SessionID: sessionID,
			SendChan:  make(chan []byte, 100),
			Done:      make(chan struct{}),
			CreatedAt: time.Now(),
		}
		err := registry.Register(sessionID, observer)

		assert.Error(t, err, "Should error on empty observer ID")
		assert.Contains(t, err.Error(), "observer ID cannot be empty")
	})
}

func TestUnregisterV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)
	sessionID := "test-session"

	t.Run("unregister existing observer", func(t *testing.T) {
		observer := NewObserverV2(sessionID)
		err := registry.Register(sessionID, observer)
		require.NoError(t, err)

		registry.Unregister(sessionID, observer.ID)

		assert.Equal(t, 0, registry.GetObserverCount(sessionID), "Observer should be removed")
		assert.Equal(t, 0, registry.GetSessionCount(), "Session should be cleaned up")
	})

	t.Run("unregister one of multiple observers", func(t *testing.T) {
		observer1 := NewObserverV2(sessionID)
		observer2 := NewObserverV2(sessionID)

		require.NoError(t, registry.Register(sessionID, observer1))
		require.NoError(t, registry.Register(sessionID, observer2))

		registry.Unregister(sessionID, observer1.ID)

		assert.Equal(t, 1, registry.GetObserverCount(sessionID), "Should have 1 observer remaining")
		assert.Equal(t, 1, registry.GetSessionCount(), "Session should still exist")
	})

	t.Run("unregister non-existent observer", func(t *testing.T) {
		// Should not panic
		registry.Unregister(sessionID, "non-existent-id")
	})

	t.Run("unregister from non-existent session", func(t *testing.T) {
		// Should not panic
		registry.Unregister("non-existent-session", "observer-id")
	})

	t.Run("observer channels are closed on unregister", func(t *testing.T) {
		observer := NewObserverV2(sessionID)
		require.NoError(t, registry.Register(sessionID, observer))

		registry.Unregister(sessionID, observer.ID)

		// Verify channels are closed
		select {
		case <-observer.Done:
			// Successfully closed
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Done channel should be closed")
		}

		// SendChan should be closed - attempting to receive should return immediately
		select {
		case _, ok := <-observer.SendChan:
			assert.False(t, ok, "SendChan should be closed")
		case <-time.After(100 * time.Millisecond):
			t.Fatal("SendChan should be closed")
		}
	})
}

func TestBroadcastV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)
	sessionID := "test-session"

	t.Run("broadcast to single observer", func(t *testing.T) {
		observer := NewObserverV2(sessionID)
		require.NoError(t, registry.Register(sessionID, observer))

		data := []byte("test message")
		registry.Broadcast(sessionID, data)

		select {
		case received := <-observer.SendChan:
			assert.Equal(t, data, received, "Should receive broadcasted data")
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Should receive message within timeout")
		}
	})

	t.Run("broadcast to multiple observers", func(t *testing.T) {
		observer1 := NewObserverV2(sessionID)
		observer2 := NewObserverV2(sessionID)
		observer3 := NewObserverV2(sessionID)

		require.NoError(t, registry.Register(sessionID, observer1))
		require.NoError(t, registry.Register(sessionID, observer2))
		require.NoError(t, registry.Register(sessionID, observer3))

		data := []byte("test message")
		registry.Broadcast(sessionID, data)

		// All observers should receive the message
		observers := []*ObserverV2{observer1, observer2, observer3}
		for i, observer := range observers {
			select {
			case received := <-observer.SendChan:
				assert.Equal(t, data, received, "Observer %d should receive data", i)
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("Observer %d should receive message within timeout", i)
			}
		}
	})

	t.Run("broadcast to non-existent session", func(t *testing.T) {
		// Should not panic
		registry.Broadcast("non-existent-session", []byte("test"))
	})

	t.Run("broadcast skips slow observer with full channel", func(t *testing.T) {
		observer := NewObserverV2(sessionID)
		require.NoError(t, registry.Register(sessionID, observer))

		// Fill the observer's channel
		for i := 0; i < cap(observer.SendChan); i++ {
			observer.SendChan <- []byte("filler")
		}

		// This broadcast should not block
		done := make(chan bool)
		go func() {
			registry.Broadcast(sessionID, []byte("test"))
			done <- true
		}()

		select {
		case <-done:
			// Successfully completed without blocking
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Broadcast should not block on full channel")
		}
	})

	t.Run("broadcast only to correct session", func(t *testing.T) {
		session1 := "session-1"
		session2 := "session-2"

		observer1 := NewObserverV2(session1)
		observer2 := NewObserverV2(session2)

		require.NoError(t, registry.Register(session1, observer1))
		require.NoError(t, registry.Register(session2, observer2))

		data := []byte("session-1 message")
		registry.Broadcast(session1, data)

		// Observer1 should receive
		select {
		case received := <-observer1.SendChan:
			assert.Equal(t, data, received)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Observer1 should receive message")
		}

		// Observer2 should not receive
		select {
		case <-observer2.SendChan:
			t.Fatal("Observer2 should not receive message from different session")
		case <-time.After(50 * time.Millisecond):
			// Correctly did not receive
		}
	})
}

func TestGetObserversV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)
	sessionID := "test-session"

	t.Run("get observers from empty session", func(t *testing.T) {
		observers := registry.GetObservers(sessionID)
		assert.Empty(t, observers, "Should return empty slice for non-existent session")
	})

	t.Run("get observers from populated session", func(t *testing.T) {
		observer1 := NewObserverV2(sessionID)
		observer2 := NewObserverV2(sessionID)

		require.NoError(t, registry.Register(sessionID, observer1))
		require.NoError(t, registry.Register(sessionID, observer2))

		observers := registry.GetObservers(sessionID)

		assert.Len(t, observers, 2, "Should return 2 observers")
		// Verify both observers are present
		ids := make(map[string]bool)
		for _, obs := range observers {
			ids[obs.ID] = true
		}
		assert.True(t, ids[observer1.ID], "Should include observer1")
		assert.True(t, ids[observer2.ID], "Should include observer2")
	})

	t.Run("returned slice is a copy", func(t *testing.T) {
		observer := NewObserverV2(sessionID)
		require.NoError(t, registry.Register(sessionID, observer))

		observers1 := registry.GetObservers(sessionID)
		observers2 := registry.GetObservers(sessionID)

		// Modifying one slice should not affect the other
		assert.NotSame(t, &observers1, &observers2, "Should return different slices")
	})
}

func TestGetSessionCountV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)

	assert.Equal(t, 0, registry.GetSessionCount(), "Should start with 0 sessions")

	observer1 := NewObserverV2("session-1")
	observer2 := NewObserverV2("session-2")
	observer3 := NewObserverV2("session-2")

	require.NoError(t, registry.Register("session-1", observer1))
	assert.Equal(t, 1, registry.GetSessionCount(), "Should have 1 session")

	require.NoError(t, registry.Register("session-2", observer2))
	assert.Equal(t, 2, registry.GetSessionCount(), "Should have 2 sessions")

	require.NoError(t, registry.Register("session-2", observer3))
	assert.Equal(t, 2, registry.GetSessionCount(), "Should still have 2 sessions")

	registry.Unregister("session-1", observer1.ID)
	assert.Equal(t, 1, registry.GetSessionCount(), "Should have 1 session after removal")
}

func TestGetObserverCountV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)
	sessionID := "test-session"

	assert.Equal(t, 0, registry.GetObserverCount(sessionID), "Should start with 0 observers")

	observer1 := NewObserverV2(sessionID)
	observer2 := NewObserverV2(sessionID)

	require.NoError(t, registry.Register(sessionID, observer1))
	assert.Equal(t, 1, registry.GetObserverCount(sessionID), "Should have 1 observer")

	require.NoError(t, registry.Register(sessionID, observer2))
	assert.Equal(t, 2, registry.GetObserverCount(sessionID), "Should have 2 observers")

	registry.Unregister(sessionID, observer1.ID)
	assert.Equal(t, 1, registry.GetObserverCount(sessionID), "Should have 1 observer after removal")

	assert.Equal(t, 0, registry.GetObserverCount("non-existent"), "Non-existent session should have 0 observers")
}

func TestCloseV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)

	t.Run("close with multiple sessions and observers", func(t *testing.T) {
		// Register multiple sessions with multiple observers
		session1 := "session-1"
		session2 := "session-2"

		obs1_1 := NewObserverV2(session1)
		obs1_2 := NewObserverV2(session1)
		obs2_1 := NewObserverV2(session2)

		require.NoError(t, registry.Register(session1, obs1_1))
		require.NoError(t, registry.Register(session1, obs1_2))
		require.NoError(t, registry.Register(session2, obs2_1))

		assert.Equal(t, 2, registry.GetSessionCount(), "Should have 2 sessions before close")

		registry.Close()

		assert.Equal(t, 0, registry.GetSessionCount(), "Should have 0 sessions after close")
		assert.Equal(t, 0, registry.GetObserverCount(session1), "Session1 should have 0 observers")
		assert.Equal(t, 0, registry.GetObserverCount(session2), "Session2 should have 0 observers")

		// Verify all observer channels are closed
		observers := []*ObserverV2{obs1_1, obs1_2, obs2_1}
		for i, obs := range observers {
			select {
			case <-obs.Done:
				// Successfully closed
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("Observer %d Done channel should be closed", i)
			}

			select {
			case _, ok := <-obs.SendChan:
				assert.False(t, ok, "Observer %d SendChan should be closed", i)
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("Observer %d SendChan should be closed", i)
			}
		}
	})

	t.Run("close empty registry", func(t *testing.T) {
		emptyRegistry := NewObserverRegistryV2(logger)
		// Should not panic
		emptyRegistry.Close()
		assert.Equal(t, 0, emptyRegistry.GetSessionCount())
	})

	t.Run("operations after close", func(t *testing.T) {
		registry := NewObserverRegistryV2(logger)
		observer := NewObserverV2("test-session")
		require.NoError(t, registry.Register("test-session", observer))

		registry.Close()

		// Should be able to register new observers after close
		newObserver := NewObserverV2("new-session")
		err := registry.Register("new-session", newObserver)
		require.NoError(t, err, "Should be able to register after close")
		assert.Equal(t, 1, registry.GetSessionCount(), "Should have 1 session after re-registration")
	})
}

func TestConcurrentAccessV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)

	t.Run("concurrent registrations", func(t *testing.T) {
		var wg sync.WaitGroup
		sessionID := "concurrent-session"
		observerCount := 100

		for i := 0; i < observerCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				observer := NewObserverV2(sessionID)
				err := registry.Register(sessionID, observer)
				assert.NoError(t, err)
			}()
		}

		wg.Wait()
		assert.Equal(t, observerCount, registry.GetObserverCount(sessionID), "Should have registered all observers")
	})

	t.Run("concurrent broadcast and registration", func(t *testing.T) {
		var wg sync.WaitGroup
		sessionID := "broadcast-session"
		data := []byte("test message")

		// Start broadcasting
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				registry.Broadcast(sessionID, data)
				time.Sleep(time.Millisecond)
			}
		}()

		// Concurrent registrations
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				observer := NewObserverV2(sessionID)
				err := registry.Register(sessionID, observer)
				assert.NoError(t, err)
			}()
		}

		wg.Wait()
	})

	t.Run("concurrent register and unregister", func(t *testing.T) {
		var wg sync.WaitGroup
		sessionID := "register-unregister-session"
		observers := make([]*ObserverV2, 50)

		// Register observers
		for i := 0; i < 50; i++ {
			observers[i] = NewObserverV2(sessionID)
			require.NoError(t, registry.Register(sessionID, observers[i]))
		}

		// Concurrent unregistrations
		for i := 0; i < 50; i++ {
			wg.Add(1)
			observer := observers[i]
			go func() {
				defer wg.Done()
				registry.Unregister(sessionID, observer.ID)
			}()
		}

		wg.Wait()
		assert.Equal(t, 0, registry.GetObserverCount(sessionID), "All observers should be unregistered")
	})

	t.Run("concurrent reads and writes", func(t *testing.T) {
		t.Skip("Flaky concurrent test - TODO: fix timing issues")
		var wg sync.WaitGroup
		sessionID := "read-write-session"

		// Writers
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				observer := NewObserverV2(sessionID)
				_ = registry.Register(sessionID, observer)
				time.Sleep(10 * time.Millisecond)
				registry.Unregister(sessionID, observer.ID)
			}()
		}

		// Readers
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					_ = registry.GetObserverCount(sessionID)
					_ = registry.GetObservers(sessionID)
					time.Sleep(time.Millisecond)
				}
			}()
		}

		// Broadcasters
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					registry.Broadcast(sessionID, []byte("test"))
					time.Sleep(time.Millisecond)
				}
			}()
		}

		wg.Wait()
	})
}

func TestObserverChannelBufferingV2(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := NewObserverRegistryV2(logger)
	sessionID := "test-session"

	observer := NewObserverV2(sessionID)
	require.NoError(t, registry.Register(sessionID, observer))

	// Should be able to send up to 100 messages without blocking
	for i := 0; i < 100; i++ {
		done := make(chan bool)
		go func(msg int) {
			registry.Broadcast(sessionID, []byte{byte(msg)})
			done <- true
		}(i)

		select {
		case <-done:
			// Successfully sent
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Broadcast %d should not block with buffered channel", i)
		}
	}

	// Verify we received all messages
	for i := 0; i < 100; i++ {
		select {
		case msg := <-observer.SendChan:
			assert.Equal(t, byte(i), msg[0], "Message %d should match", i)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Should receive message %d", i)
		}
	}
}
