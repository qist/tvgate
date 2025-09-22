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

// New allocates a RingBuffer.
func New(size uint64) (*RingBuffer, error) {
	if (size & (size - 1)) != 0 {
		return nil, fmt.Errorf("size must be a power of two")
	}

	r := &RingBuffer{
		size:   size,
		buffer: make([]interface{}, size),
	}
	r.cond = sync.NewCond(&r.mutex)
	return r, nil
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
// PullWithContext blocks until data is available, buffer is closed, or ctx is done.
func (r *RingBuffer) PullWithContext(ctx context.Context) (interface{}, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	for r.buffer[r.readIndex] == nil && !r.closed {
		select {
		case <-ctx.Done():
			return nil, false
		default:
		}
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
