package kiro

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRingBuffer(t *testing.T) {
	rb := NewRingBuffer(100)
	assert.NotNil(t, rb)
	assert.Equal(t, 100, rb.capacity)
	assert.Equal(t, 0, rb.Len())
}

func TestRingBuffer_Write_Simple(t *testing.T) {
	rb := NewRingBuffer(10)

	n, err := rb.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, 5, rb.Len())
}

func TestRingBuffer_Write_Empty(t *testing.T) {
	rb := NewRingBuffer(10)

	n, err := rb.Write([]byte{})
	assert.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 0, rb.Len())
}

func TestRingBuffer_Write_LargerThanCapacity(t *testing.T) {
	rb := NewRingBuffer(5)

	// Write 10 bytes to a 5 byte buffer
	n, err := rb.Write([]byte("0123456789"))
	assert.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, 5, rb.Len())

	// Should only keep the last 5 bytes
	data := rb.Read()
	assert.Equal(t, []byte("56789"), data)
}

func TestRingBuffer_Write_Wraparound(t *testing.T) {
	rb := NewRingBuffer(5)

	// Fill buffer
	rb.Write([]byte("abcde"))
	assert.Equal(t, 5, rb.Len())

	// Write more to cause wraparound
	rb.Write([]byte("fg"))
	assert.Equal(t, 5, rb.Len())

	// Should have "cdefg"
	data := rb.Read()
	assert.Equal(t, []byte("cdefg"), data)
}

func TestRingBuffer_Write_MultipleWraparounds(t *testing.T) {
	rb := NewRingBuffer(5)

	// Write multiple times
	rb.Write([]byte("abc"))    // "abc"
	rb.Write([]byte("de"))     // "abcde"
	rb.Write([]byte("fgh"))    // "defgh"
	rb.Write([]byte("ij"))     // "fghij"

	assert.Equal(t, 5, rb.Len())
	data := rb.Read()
	assert.Equal(t, []byte("fghij"), data)
}

func TestRingBuffer_Read_Empty(t *testing.T) {
	rb := NewRingBuffer(10)

	data := rb.Read()
	assert.Nil(t, data)
}

func TestRingBuffer_Read_Contiguous(t *testing.T) {
	rb := NewRingBuffer(10)

	rb.Write([]byte("hello"))
	data := rb.Read()
	assert.Equal(t, []byte("hello"), data)
}

func TestRingBuffer_Read_Wrapped(t *testing.T) {
	rb := NewRingBuffer(5)

	rb.Write([]byte("abc"))
	rb.Write([]byte("defgh"))  // Will wrap around

	data := rb.Read()
	assert.Equal(t, []byte("defgh"), data)
}

func TestRingBuffer_Clear(t *testing.T) {
	rb := NewRingBuffer(10)

	rb.Write([]byte("hello"))
	assert.Equal(t, 5, rb.Len())

	rb.Clear()
	assert.Equal(t, 0, rb.Len())

	data := rb.Read()
	assert.Nil(t, data)
}

func TestRingBuffer_ThreadSafety(t *testing.T) {
	rb := NewRingBuffer(100)
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			rb.Write([]byte("test"))
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			rb.Read()
			rb.Len()
		}
		done <- true
	}()

	// Wait for both
	<-done
	<-done
}

func TestRingBuffer_SequentialWrites(t *testing.T) {
	rb := NewRingBuffer(10)

	rb.Write([]byte("12"))
	rb.Write([]byte("34"))
	rb.Write([]byte("56"))

	assert.Equal(t, 6, rb.Len())
	data := rb.Read()
	assert.Equal(t, []byte("123456"), data)
}

func TestRingBuffer_ExactCapacity(t *testing.T) {
	rb := NewRingBuffer(5)

	// Write exactly capacity
	rb.Write([]byte("abcde"))
	assert.Equal(t, 5, rb.Len())

	data := rb.Read()
	assert.Equal(t, []byte("abcde"), data)

	// Write one more byte
	rb.Write([]byte("f"))
	assert.Equal(t, 5, rb.Len())

	data = rb.Read()
	assert.Equal(t, []byte("bcdef"), data)
}

func TestRingBuffer_ReadDoesNotModify(t *testing.T) {
	rb := NewRingBuffer(10)

	rb.Write([]byte("hello"))

	// Multiple reads should return the same data
	data1 := rb.Read()
	data2 := rb.Read()
	assert.Equal(t, data1, data2)

	// And length should stay the same
	assert.Equal(t, 5, rb.Len())
}

func TestRingBuffer_WriteAfterClear(t *testing.T) {
	rb := NewRingBuffer(10)

	rb.Write([]byte("hello"))
	rb.Clear()
	rb.Write([]byte("world"))

	assert.Equal(t, 5, rb.Len())
	data := rb.Read()
	assert.Equal(t, []byte("world"), data)
}

func TestRingBuffer_LargeData(t *testing.T) {
	rb := NewRingBuffer(1024)

	// Write 2KB of data
	largeData := bytes.Repeat([]byte("x"), 2048)
	rb.Write(largeData)

	// Should only keep the last 1KB
	assert.Equal(t, 1024, rb.Len())
	data := rb.Read()
	assert.Equal(t, 1024, len(data))
	assert.True(t, bytes.Equal(largeData[1024:], data))
}
