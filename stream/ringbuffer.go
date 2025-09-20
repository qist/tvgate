package stream

import (
	"sync"
)

// StreamRingBuffer 是一个线程安全的环形缓冲区
type StreamRingBuffer struct {
	buf      [][]byte
	capacity int
	readPos  int
	writePos int
	size     int
	mu       sync.Mutex
	cond     *sync.Cond
	closed   bool
}

func NewStreamRingBuffer(capacity int) *StreamRingBuffer {
	rb := &StreamRingBuffer{
		buf:      make([][]byte, capacity),
		capacity: capacity,
	}
	rb.cond = sync.NewCond(&rb.mu)
	return rb
}

// Push 写入数据，满时覆盖最旧数据
func (rb *StreamRingBuffer) Push(data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.closed {
		return
	}
	rb.buf[rb.writePos] = data
	rb.writePos = (rb.writePos + 1) % rb.capacity
	if rb.size < rb.capacity {
		rb.size++
	} else {
		rb.readPos = (rb.readPos + 1) % rb.capacity
	}
	rb.cond.Signal()
}

// Pop 读取数据，空时阻塞，关闭后返回 nil
func (rb *StreamRingBuffer) Pop() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for rb.size == 0 && !rb.closed {
		rb.cond.Wait()
	}
	if rb.size == 0 && rb.closed {
		return nil
	}
	data := rb.buf[rb.readPos]
	rb.readPos = (rb.readPos + 1) % rb.capacity
	rb.size--
	return data
}

// Close 关闭缓冲区，唤醒所有等待
func (rb *StreamRingBuffer) Close() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.closed = true
	rb.cond.Broadcast()
}
