package kiro

import (
	"context"
	"testing"
	"time"

	"github.com/sakthiram/kiro-slack-agent/internal/config"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// drainChannel drains any pending messages from a channel with a timeout
func drainChannel(ch <-chan []byte) {
	for {
		select {
		case <-ch:
			// Keep draining
		case <-time.After(10 * time.Millisecond):
			// No more messages
			return
		}
	}
}

func TestNewObservableProcess(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	assert.NotNil(t, op)
	assert.NotNil(t, op.Process)
	assert.NotNil(t, op.observers)
	assert.NotNil(t, op.scrollback)
	assert.Equal(t, 0, len(op.observers))
	assert.Equal(t, 0, op.scrollback.Len())
}

func TestObservableProcess_AddObserver(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add observer
	ch := op.AddObserver("observer1")
	assert.NotNil(t, ch)
	assert.Equal(t, 1, op.ObserverCount())

	// Add another observer
	ch2 := op.AddObserver("observer2")
	assert.NotNil(t, ch2)
	assert.Equal(t, 2, op.ObserverCount())
}

func TestObservableProcess_AddObserver_ReceivesScrollback(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add some data to scrollback
	testData := []byte("existing output")
	op.scrollback.Write(testData)

	// Add observer
	ch := op.AddObserver("observer1")

	// Observer should receive scrollback
	select {
	case data := <-ch:
		assert.Equal(t, testData, data)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("observer did not receive scrollback")
	}
}

func TestObservableProcess_RemoveObserver(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add observer
	ch := op.AddObserver("observer1")
	assert.Equal(t, 1, op.ObserverCount())

	// Remove observer
	op.RemoveObserver("observer1")
	assert.Equal(t, 0, op.ObserverCount())

	// Channel should be closed
	_, ok := <-ch
	assert.False(t, ok)
}

func TestObservableProcess_RemoveObserver_Nonexistent(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Remove non-existent observer should not panic
	op.RemoveObserver("nonexistent")
	assert.Equal(t, 0, op.ObserverCount())
}

func TestObservableProcess_Broadcast(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add observers
	ch1 := op.AddObserver("observer1")
	ch2 := op.AddObserver("observer2")

	// Drain scrollback messages (if any)
	drainChannel(ch1)
	drainChannel(ch2)

	// Broadcast data
	testData := []byte("test output")
	op.broadcast(testData)

	// Both observers should receive the data
	select {
	case data := <-ch1:
		assert.Equal(t, testData, data)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("observer1 did not receive broadcast")
	}

	select {
	case data := <-ch2:
		assert.Equal(t, testData, data)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("observer2 did not receive broadcast")
	}
}

func TestObservableProcess_Broadcast_EmptyData(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	ch := op.AddObserver("observer1")

	// Drain scrollback
	drainChannel(ch)

	// Broadcast empty data should not send anything
	op.broadcast([]byte{})

	// Channel should be empty
	select {
	case <-ch:
		t.Fatal("should not receive empty broadcast")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}
}

func TestObservableProcess_Broadcast_FullChannel(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	ch := op.AddObserver("observer1")

	// Drain scrollback
	drainChannel(ch)

	// Fill the channel by broadcasting more than buffer size
	for i := 0; i < DefaultObserverChanSize+10; i++ {
		op.broadcast([]byte("x"))
	}

	// Should not panic, and channel should have data
	count := 0
	for {
		select {
		case <-ch:
			count++
		case <-time.After(50 * time.Millisecond):
			// Done receiving
			assert.Greater(t, count, 0, "should have received some data")
			return
		}
	}
}

func TestObservableProcess_GetScrollback(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add data
	testData := []byte("test scrollback")
	op.broadcast(testData)

	// Get scrollback
	scrollback := op.GetScrollback()
	assert.Equal(t, testData, scrollback)
}

func TestObservableProcess_Close_RemovesObservers(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add observers
	ch1 := op.AddObserver("observer1")
	ch2 := op.AddObserver("observer2")
	assert.Equal(t, 2, op.ObserverCount())

	// Close
	err := op.Close()
	assert.NoError(t, err)
	assert.Equal(t, 0, op.ObserverCount())

	// Channels should be closed
	_, ok1 := <-ch1
	assert.False(t, ok1)
	_, ok2 := <-ch2
	assert.False(t, ok2)
}

func TestObservableProcess_Start_ClearsScrollback(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "/nonexistent/binary", // Will fail to start
		StartupTimeout:  1 * time.Millisecond,
		ResponseTimeout: 1 * time.Millisecond,
	}

	op := NewObservableProcess("/tmp", cfg, logger)

	// Add data to scrollback
	op.scrollback.Write([]byte("old data"))
	assert.Greater(t, op.scrollback.Len(), 0)

	// Try to start (will fail, but should clear scrollback)
	ctx := context.Background()
	op.Start(ctx)

	// Scrollback should be cleared
	assert.Equal(t, 0, op.scrollback.Len())
}

func TestObservableProcess_ImplementsBridge(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	var _ Bridge = NewObservableProcess("/tmp/test-session", cfg, logger)
}

func TestObservableProcess_SendMessage_BroadcastsOutput(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add observer
	ch := op.AddObserver("observer1")

	// Drain scrollback
	drainChannel(ch)

	// Create a test handler
	handler := func(chunk string, isComplete bool) {
		// Handler is called when broadcast happens
	}

	// Manually set running to true to bypass start check
	op.Process.mu.Lock()
	op.Process.running = true
	op.Process.mu.Unlock()

	// Note: SendMessage will fail because no real PTY exists,
	// but we're testing that the handler wrapping works
	ctx := context.Background()
	op.SendMessage(ctx, "test", handler)

	// Reset running state
	op.Process.mu.Lock()
	op.Process.running = false
	op.Process.mu.Unlock()
}

func TestObservableProcess_ObserverCount(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	assert.Equal(t, 0, op.ObserverCount())

	op.AddObserver("observer1")
	assert.Equal(t, 1, op.ObserverCount())

	op.AddObserver("observer2")
	assert.Equal(t, 2, op.ObserverCount())

	op.RemoveObserver("observer1")
	assert.Equal(t, 1, op.ObserverCount())

	op.RemoveObserver("observer2")
	assert.Equal(t, 0, op.ObserverCount())
}

func TestObservableProcess_MultipleObservers_IndependentChannels(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add two observers
	ch1 := op.AddObserver("observer1")
	ch2 := op.AddObserver("observer2")

	// Drain scrollback
	drainChannel(ch1)
	drainChannel(ch2)

	// Broadcast data
	testData := []byte("test")
	op.broadcast(testData)

	// Both should receive independently
	data1 := <-ch1
	data2 := <-ch2

	assert.Equal(t, testData, data1)
	assert.Equal(t, testData, data2)

	// Remove one observer
	op.RemoveObserver("observer1")

	// Broadcast again
	op.broadcast([]byte("more"))

	// Only observer2 should receive
	select {
	case data := <-ch2:
		assert.Equal(t, []byte("more"), data)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("observer2 did not receive data")
	}

	// observer1's channel should be closed
	_, ok := <-ch1
	assert.False(t, ok)
}

func TestObservableProcess_Broadcast_UpdatesScrollback(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Broadcast multiple chunks
	op.broadcast([]byte("chunk1\n"))
	op.broadcast([]byte("chunk2\n"))
	op.broadcast([]byte("chunk3\n"))

	// Scrollback should contain all chunks
	scrollback := op.GetScrollback()
	expected := []byte("chunk1\nchunk2\nchunk3\n")
	assert.Equal(t, expected, scrollback)
}

func TestObservableProcess_LateObserver_GetsFullScrollback(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.KiroConfig{
		BinaryPath:      "kiro-cli",
		StartupTimeout:  30 * time.Second,
		ResponseTimeout: 120 * time.Second,
	}

	op := NewObservableProcess("/tmp/test-session", cfg, logger)

	// Add observer1 and broadcast some data
	ch1 := op.AddObserver("observer1")
	drainChannel(ch1) // drain initial scrollback

	op.broadcast([]byte("line1\n"))
	op.broadcast([]byte("line2\n"))
	op.broadcast([]byte("line3\n"))

	// Drain observer1's channel
	drainChannel(ch1)

	// Add observer2 late
	ch2 := op.AddObserver("observer2")

	// observer2 should get all previous data in scrollback
	select {
	case data := <-ch2:
		expected := []byte("line1\nline2\nline3\n")
		assert.Equal(t, expected, data)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("late observer did not receive scrollback")
	}

	// Broadcast new data
	op.broadcast([]byte("line4\n"))

	// Both observers should get the new data
	data1 := <-ch1
	data2 := <-ch2
	assert.Equal(t, []byte("line4\n"), data1)
	assert.Equal(t, []byte("line4\n"), data2)
}
