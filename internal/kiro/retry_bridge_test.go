package kiro

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockBridge is a test mock for the Bridge interface.
type mockBridge struct {
	started        bool
	running        bool
	closed         bool
	messages       []string
	responses      []string
	startErr       error
	sendErr        error
	closeErr       error
	sendCallCount  int
	failUntilCount int // Fail sendMessage until this many attempts
}

func (m *mockBridge) Start(ctx context.Context) error {
	if m.startErr != nil {
		return m.startErr
	}
	m.started = true
	m.running = true
	return nil
}

func (m *mockBridge) SendMessage(ctx context.Context, message string, handler ResponseHandler) error {
	m.sendCallCount++
	m.messages = append(m.messages, message)

	// Simulate failures if configured
	if m.failUntilCount > 0 && m.sendCallCount <= m.failUntilCount {
		return m.sendErr
	}

	// Call handler with responses
	for i, resp := range m.responses {
		isComplete := i == len(m.responses)-1
		handler(resp, isComplete)
	}
	return nil
}

func (m *mockBridge) IsRunning() bool {
	return m.running
}

func (m *mockBridge) Close() error {
	if m.closeErr != nil {
		return m.closeErr
	}
	m.running = false
	m.closed = true
	return nil
}

func TestNewRetryBridge(t *testing.T) {
	logger := zap.NewNop()
	inner := &mockBridge{}
	retry := NewRetryBridge(inner, 3, logger)

	assert.NotNil(t, retry)
	assert.Equal(t, 3, retry.maxRetries)
}

func TestRetryBridge_Start(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	t.Run("success", func(t *testing.T) {
		inner := &mockBridge{}
		retry := NewRetryBridge(inner, 3, logger)

		err := retry.Start(ctx)
		require.NoError(t, err)
		assert.True(t, inner.started)
	})

	t.Run("error", func(t *testing.T) {
		inner := &mockBridge{startErr: errors.New("start failed")}
		retry := NewRetryBridge(inner, 3, logger)

		err := retry.Start(ctx)
		assert.Error(t, err)
	})
}

func TestRetryBridge_SendMessage_Success(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	inner := &mockBridge{responses: []string{"Hello", "World"}}
	retry := NewRetryBridge(inner, 3, logger)

	var received []string
	err := retry.SendMessage(ctx, "test", func(chunk string, isComplete bool) {
		received = append(received, chunk)
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"Hello", "World"}, received)
	assert.Equal(t, 1, inner.sendCallCount)
}

func TestRetryBridge_SendMessage_RetrySuccess(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	// Fail first 2 attempts, succeed on 3rd
	inner := &mockBridge{
		responses:      []string{"Success"},
		sendErr:        errors.New("temporary failure"),
		failUntilCount: 2,
	}
	retry := NewRetryBridge(inner, 3, logger)

	var response string
	err := retry.SendMessage(ctx, "test", func(chunk string, isComplete bool) {
		response = chunk
	})

	require.NoError(t, err)
	assert.Equal(t, "Success", response)
	assert.Equal(t, 3, inner.sendCallCount) // 2 failures + 1 success
}

func TestRetryBridge_SendMessage_AllFail(t *testing.T) {
	ctx := context.Background()
	logger := zap.NewNop()

	// Fail all attempts
	inner := &mockBridge{
		sendErr:        errors.New("persistent failure"),
		failUntilCount: 10, // More than maxRetries
	}
	retry := NewRetryBridge(inner, 2, logger)

	err := retry.SendMessage(ctx, "test", func(chunk string, isComplete bool) {})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "after 3 attempts")
	assert.Equal(t, 3, inner.sendCallCount) // 0 + maxRetries(2) + 1 = 3 attempts
}

func TestRetryBridge_IsRunning(t *testing.T) {
	logger := zap.NewNop()

	inner := &mockBridge{running: true}
	retry := NewRetryBridge(inner, 3, logger)

	assert.True(t, retry.IsRunning())

	inner.running = false
	assert.False(t, retry.IsRunning())
}

func TestRetryBridge_Close(t *testing.T) {
	logger := zap.NewNop()

	t.Run("success", func(t *testing.T) {
		inner := &mockBridge{running: true}
		retry := NewRetryBridge(inner, 3, logger)

		err := retry.Close()
		require.NoError(t, err)
		assert.True(t, inner.closed)
		assert.False(t, inner.running)
	})

	t.Run("error", func(t *testing.T) {
		inner := &mockBridge{closeErr: errors.New("close failed")}
		retry := NewRetryBridge(inner, 3, logger)

		err := retry.Close()
		assert.Error(t, err)
	})
}

func TestRetryBridge_ImplementsBridge(t *testing.T) {
	logger := zap.NewNop()
	inner := &mockBridge{}
	var _ Bridge = NewRetryBridge(inner, 3, logger)
}
