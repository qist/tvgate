package stream

import (
	"sync"
)

// streamRingBuffer 是一个线程安全的环形缓冲区，用于替代 channel 做缓存
// 命名规避 udp，容量由外部指定

type streamRingBuffer struct {
	buf      [][]byte
	cap      int
	readPos  int
	writePos int
	size     int
	mu       sync.Mutex
	cond     *sync.Cond
	closed   bool
}

func NewStreamRingBuffer(capacity int) *streamRingBuffer {
	rb := &streamRingBuffer{
		buf: make([][]byte, capacity),
		cap: capacity,
	}
	rb.cond = sync.NewCond(&rb.mu)
	return rb
}

// Push 写入数据到 ring buffer，满时丢弃数据
func (rb *streamRingBuffer) Push(data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.closed {
		return
	}
	if rb.size == rb.cap {
		// 满了，丢弃数据
		return
	}
	rb.buf[rb.writePos] = data
	rb.writePos = (rb.writePos + 1) % rb.cap
	rb.size++
	rb.cond.Signal()
}

// Pop 读取数据，空时阻塞，关闭后返回 nil
func (rb *streamRingBuffer) Pop() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for rb.size == 0 && !rb.closed {
		rb.cond.Wait()
	}
	if rb.size == 0 && rb.closed {
		return nil
	}
	data := rb.buf[rb.readPos]
	rb.readPos = (rb.readPos + 1) % rb.cap
	rb.size--
	return data
}

// Close 关闭 ring buffer，唤醒所有等待
func (rb *streamRingBuffer) Close() {
	rb.mu.Lock()
	rb.closed = true
	rb.cond.Broadcast()
	rb.mu.Unlock()
}

// IsClosed 判断是否关闭
func (rb *streamRingBuffer) IsClosed() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.closed
}
