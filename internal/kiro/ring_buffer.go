package kiro

import "sync"

// RingBuffer implements a circular buffer for storing scrollback data.
// It maintains the last N bytes written, overwriting old data when full.
type RingBuffer struct {
	data     []byte
	capacity int
	start    int    // Start position of valid data
	length   int    // Current length of valid data
	mu       sync.Mutex
}

// NewRingBuffer creates a new ring buffer with the specified capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		data:     make([]byte, capacity),
		capacity: capacity,
		start:    0,
		length:   0,
	}
}

// Write appends data to the ring buffer.
// If the buffer is full, old data is overwritten.
func (rb *RingBuffer) Write(p []byte) (n int, err error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	n = len(p)
	if n == 0 {
		return 0, nil
	}

	// If data is larger than capacity, only keep the last capacity bytes
	if n >= rb.capacity {
		copy(rb.data, p[n-rb.capacity:])
		rb.start = 0
		rb.length = rb.capacity
		return n, nil
	}

	// Calculate write position
	writePos := (rb.start + rb.length) % rb.capacity

	// Write data
	for i := 0; i < n; i++ {
		rb.data[writePos] = p[i]
		writePos = (writePos + 1) % rb.capacity

		if rb.length < rb.capacity {
			rb.length++
		} else {
			// Buffer is full, advance start
			rb.start = (rb.start + 1) % rb.capacity
		}
	}

	return n, nil
}

// Read returns a copy of all data currently in the buffer.
// The data is returned in the order it was written.
func (rb *RingBuffer) Read() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.length == 0 {
		return nil
	}

	result := make([]byte, rb.length)

	if rb.start+rb.length <= rb.capacity {
		// Data is contiguous
		copy(result, rb.data[rb.start:rb.start+rb.length])
	} else {
		// Data wraps around
		firstPart := rb.capacity - rb.start
		copy(result, rb.data[rb.start:])
		copy(result[firstPart:], rb.data[:rb.length-firstPart])
	}

	return result
}

// Len returns the current number of bytes in the buffer.
func (rb *RingBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.length
}

// Clear removes all data from the buffer.
func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.start = 0
	rb.length = 0
}
