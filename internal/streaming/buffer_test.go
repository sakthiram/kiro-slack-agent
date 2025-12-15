package streaming

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuffer_ImmediateFirstFlush(t *testing.T) {
	var flushed string
	var flushCount int
	var mu sync.Mutex

	buffer := NewOutputBuffer(500*time.Millisecond, func(s string) error {
		mu.Lock()
		defer mu.Unlock()
		flushed = s
		flushCount++
		return nil
	})
	defer buffer.Stop()

	err := buffer.Append("Hello")
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, "Hello", flushed, "first append should flush immediately")
	assert.Equal(t, 1, flushCount)
	mu.Unlock()
}

func TestBuffer_Debouncing(t *testing.T) {
	var flushed []string
	var mu sync.Mutex

	buffer := NewOutputBuffer(100*time.Millisecond, func(s string) error {
		mu.Lock()
		defer mu.Unlock()
		flushed = append(flushed, s)
		return nil
	})
	defer buffer.Stop()

	// First append flushes immediately
	err := buffer.Append("a")
	require.NoError(t, err)

	// Rapid appends should be debounced
	err = buffer.Append("ab")
	require.NoError(t, err)
	err = buffer.Append("abc")
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 1, len(flushed), "should only have immediate first flush")
	assert.Equal(t, "a", flushed[0])
	mu.Unlock()

	// Wait for debounce timer to fire
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	assert.Equal(t, 2, len(flushed), "should have debounced flush")
	assert.Equal(t, "abc", flushed[1], "debounced flush should have latest content")
	mu.Unlock()
}

func TestBuffer_Deduplication(t *testing.T) {
	var flushCount int
	var mu sync.Mutex

	buffer := NewOutputBuffer(50*time.Millisecond, func(s string) error {
		mu.Lock()
		defer mu.Unlock()
		flushCount++
		return nil
	})
	defer buffer.Stop()

	// First append
	err := buffer.Append("Hello")
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 1, flushCount)
	mu.Unlock()

	// Wait for interval to pass
	time.Sleep(60 * time.Millisecond)

	// Append same content - should skip
	err = buffer.Append("Hello")
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 1, flushCount, "same content should not re-flush")
	mu.Unlock()

	// Append different content - should flush
	err = buffer.Append("World")
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 2, flushCount, "different content should flush")
	mu.Unlock()
}

func TestBuffer_Stop_CancelsPending(t *testing.T) {
	var flushCount int
	var mu sync.Mutex

	buffer := NewOutputBuffer(100*time.Millisecond, func(s string) error {
		mu.Lock()
		defer mu.Unlock()
		flushCount++
		return nil
	})

	// First flush
	err := buffer.Append("a")
	require.NoError(t, err)

	// Schedule debounced flush
	err = buffer.Append("ab")
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 1, flushCount)
	mu.Unlock()

	// Stop before debounce timer fires
	buffer.Stop()

	// Wait for what would have been the debounce time
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	assert.Equal(t, 1, flushCount, "stop should cancel pending flush")
	mu.Unlock()
}

func TestBuffer_Flush_ForcesImmediate(t *testing.T) {
	var flushed []string
	var mu sync.Mutex

	buffer := NewOutputBuffer(1*time.Hour, func(s string) error {
		mu.Lock()
		defer mu.Unlock()
		flushed = append(flushed, s)
		return nil
	})
	defer buffer.Stop()

	// First flush
	err := buffer.Append("a")
	require.NoError(t, err)

	// This would normally debounce for 1 hour
	err = buffer.Append("ab")
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 1, len(flushed))
	mu.Unlock()

	// Force immediate flush
	err = buffer.Flush()
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 2, len(flushed))
	assert.Equal(t, "ab", flushed[1])
	mu.Unlock()
}

func TestBuffer_Content(t *testing.T) {
	buffer := NewOutputBuffer(100*time.Millisecond, func(s string) error {
		return nil
	})
	defer buffer.Stop()

	assert.Equal(t, "", buffer.Content())

	buffer.Append("Hello")
	assert.Equal(t, "Hello", buffer.Content())

	buffer.Append("Hello World")
	assert.Equal(t, "Hello World", buffer.Content())
}

func TestBuffer_AfterStop_IgnoresAppends(t *testing.T) {
	var flushCount int
	var mu sync.Mutex

	buffer := NewOutputBuffer(100*time.Millisecond, func(s string) error {
		mu.Lock()
		defer mu.Unlock()
		flushCount++
		return nil
	})

	buffer.Append("a")
	buffer.Stop()

	// Append after stop should be ignored
	err := buffer.Append("b")
	assert.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 1, flushCount)
	mu.Unlock()
}

func TestBuffer_ConcurrentAppends(t *testing.T) {
	var flushCount int
	var mu sync.Mutex

	buffer := NewOutputBuffer(50*time.Millisecond, func(s string) error {
		mu.Lock()
		defer mu.Unlock()
		flushCount++
		return nil
	})
	defer buffer.Stop()

	// Concurrent appends should not race
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			buffer.Append("content")
		}(i)
	}
	wg.Wait()

	// Just ensure no panics - exact count depends on timing
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.GreaterOrEqual(t, flushCount, 1)
	mu.Unlock()
}
