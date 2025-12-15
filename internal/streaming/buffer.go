package streaming

import (
	"sync"
	"time"
)

// OutputBuffer buffers content and flushes with debouncing.
// First content flushes immediately, subsequent rapid updates are debounced.
type OutputBuffer struct {
	content     string        // Current accumulated content
	lastContent string        // Last flushed content (for deduplication)
	lastFlush   time.Time     // Time of last flush
	minInterval time.Duration // Minimum time between flushes
	onFlush     func(string) error
	mu          sync.Mutex
	flushTimer  *time.Timer
	stopped     bool
}

// NewOutputBuffer creates a buffer that calls onFlush with debounced updates.
// minInterval is the minimum time between flush calls (e.g., 500ms).
func NewOutputBuffer(minInterval time.Duration, onFlush func(string) error) *OutputBuffer {
	return &OutputBuffer{
		minInterval: minInterval,
		onFlush:     onFlush,
	}
}

// Append sets the current content (replaces previous - expects full content each time).
// If enough time has passed since last flush, flushes immediately.
// Otherwise, schedules a debounced flush.
func (b *OutputBuffer) Append(content string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopped {
		return nil
	}

	b.content = content

	// First flush or enough time passed - flush immediately
	if b.lastFlush.IsZero() || time.Since(b.lastFlush) >= b.minInterval {
		return b.flushLocked()
	}

	// Schedule debounced flush
	b.scheduleFlushLocked()
	return nil
}

// flushLocked sends current content to onFlush callback.
// Must be called with lock held.
func (b *OutputBuffer) flushLocked() error {
	// Cancel any pending timer
	if b.flushTimer != nil {
		b.flushTimer.Stop()
		b.flushTimer = nil
	}

	// Skip if content unchanged (deduplication)
	if b.content == b.lastContent {
		return nil
	}

	// Call callback
	if err := b.onFlush(b.content); err != nil {
		return err
	}

	b.lastContent = b.content
	b.lastFlush = time.Now()
	return nil
}

// scheduleFlushLocked schedules a flush after minInterval from last flush.
// Must be called with lock held.
func (b *OutputBuffer) scheduleFlushLocked() {
	// Cancel existing timer
	if b.flushTimer != nil {
		b.flushTimer.Stop()
	}

	// Calculate delay: time until minInterval since last flush
	delay := b.minInterval - time.Since(b.lastFlush)
	if delay < 0 {
		delay = 0
	}

	b.flushTimer = time.AfterFunc(delay, func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		if b.stopped {
			return
		}

		b.flushLocked()
	})
}

// Flush forces an immediate flush of current content.
func (b *OutputBuffer) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopped {
		return nil
	}

	return b.flushLocked()
}

// Stop cancels any pending flush and stops the buffer.
func (b *OutputBuffer) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopped = true
	if b.flushTimer != nil {
		b.flushTimer.Stop()
		b.flushTimer = nil
	}
}

// Content returns the current buffered content.
func (b *OutputBuffer) Content() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.content
}
