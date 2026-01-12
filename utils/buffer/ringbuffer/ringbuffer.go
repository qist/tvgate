package ringbuffer

import (
	"context"
	"fmt"
	"sync"
)

// RingBuffer is a ring buffer.
type RingBuffer struct {
	size       uint64
	mutex      sync.Mutex
	cond       *sync.Cond
	buffer     []interface{}
	readIndex  uint64
	writeIndex uint64
	closed     bool
	dataChan   chan interface{}
	mask       uint64
}

// Chan returns a channel that can be used in select statements
func (r *RingBuffer) Chan() <-chan interface{} {
	return r.dataChan
}

// type ctxWrapper struct {
// 	ctx context.Context
// }
// type DoneContext interface {
// 	IsDone() bool
// }

// func (c *ctxWrapper) IsDone() bool {
// 	select {
// 	case <-c.ctx.Done():
// 		return true
// 	default:
// 		return false
// 	}
// }

// New creates a new ring buffer with the given size.
func New(size uint64) (*RingBuffer, error) {
	if size == 0 {
		return nil, fmt.Errorf("size must be positive")
	}

	// make sure size is power of 2
	if (size & (size - 1)) != 0 {
		return nil, fmt.Errorf("size must be a power of 2")
	}

	rb := &RingBuffer{
		buffer:     make([]interface{}, size),
		size:       size,
		mask:       size - 1,
		dataChan:   make(chan interface{}, 8192), // 大幅增加channel缓冲区大小
	}
	rb.cond = sync.NewCond(&rb.mutex)
	return rb, nil
}

// Close makes Pull() return false.
func (r *RingBuffer) Close() {
	r.mutex.Lock()
	r.closed = true
	for i := uint64(0); i < r.size; i++ {
		r.buffer[i] = nil
	}
	r.mutex.Unlock()
	r.cond.Broadcast()
	close(r.dataChan)
}

// Reset restores Pull() behavior after a Close().
func (r *RingBuffer) Reset() {
	r.mutex.Lock()
	for i := uint64(0); i < r.size; i++ {
		r.buffer[i] = nil
	}
	r.writeIndex = 0
	r.readIndex = 0
	r.closed = false
	r.mutex.Unlock()
	r.cond.Broadcast()
}

// Push pushes data at the end of the buffer, overwriting oldest if full.
func (r *RingBuffer) Push(data interface{}) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.buffer[r.writeIndex] != nil {
		// overwrite oldest
		r.readIndex = (r.readIndex + 1) % r.size
	}

	r.buffer[r.writeIndex] = data
	r.writeIndex = (r.writeIndex + 1) % r.size

	// 发送到内部channel，使用非阻塞方式，但增加缓冲区大小来减少丢包
	select {
	case r.dataChan <- data:
	default:
		// 如果channel满了，跳过，但ring buffer本身仍保存数据
		// 这种情况应该很少发生，因为缓冲区已经增大
	}

	r.cond.Signal()
	return true
}

// Pull blocks until data is available or buffer is closed.
func (r *RingBuffer) Pull() (interface{}, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	for r.buffer[r.readIndex] == nil && !r.closed {
		r.cond.Wait()
	}

	if r.closed {
		return nil, false
	}

	data := r.buffer[r.readIndex]
	r.buffer[r.readIndex] = nil
	r.readIndex = (r.readIndex + 1) % r.size
	return data, true
}

// PullWithContext blocks until data is available, buffer is closed, or ctx is done.
func (r *RingBuffer) PullWithContext(ctx context.Context) (interface{}, bool) {
    r.mutex.Lock()
    defer r.mutex.Unlock()

    // 检查初始 Context 状态
    if err := ctx.Err(); err != nil {
        return nil, false
    }

    for r.buffer[r.readIndex] == nil && !r.closed {
        // 注意：这里无法在 Wait 过程中响应 Context 取消
        r.cond.Wait() 

        // 唤醒后立即检查 Context 和 Closed 状态
        if ctx.Err() != nil || r.closed {
            return nil, false
        }
    }

    // 再次确认缓冲区是否有数据（防止因 Closed 被唤醒但实际无数据）
    if r.buffer[r.readIndex] == nil {
        return nil, false
    }

    data := r.buffer[r.readIndex]
    r.buffer[r.readIndex] = nil
    r.readIndex = (r.readIndex + 1) % r.size
    return data, true
}