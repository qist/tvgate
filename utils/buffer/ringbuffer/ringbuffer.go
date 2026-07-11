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
	chanClosed bool
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
		buffer:   make([]interface{}, size),
		size:     size,
		mask:     size - 1,
		dataChan: make(chan interface{}, size), // channel 容量与 ring buffer 一致
	}
	rb.cond = sync.NewCond(&rb.mutex)
	return rb, nil
}

// Close makes Pull() return false.
func (r *RingBuffer) Close() {
	r.mutex.Lock()
	if r.closed {
		r.mutex.Unlock()
		return
	}
	r.closed = true
	for i := uint64(0); i < r.size; i++ {
		r.buffer[i] = nil
	}
	if !r.chanClosed {
		// 排空 dataChan 中残留的数据引用，避免内存泄漏
		// close 后消费者会收到零值并退出，但 channel 中已缓冲的 interface{} 引用不会被 GC
		for len(r.dataChan) > 0 {
			<-r.dataChan
		}
		close(r.dataChan)
		r.chanClosed = true
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
	if r.chanClosed {
		chanCap := cap(r.dataChan)
		if chanCap == 0 {
			chanCap = int(r.size)
		}
		r.dataChan = make(chan interface{}, chanCap)
		r.chanClosed = false
	}
	r.closed = false
	r.mutex.Unlock()
	r.cond.Broadcast()
}

// Push pushes data at the end of the buffer, overwriting oldest if full.
func (r *RingBuffer) Push(data interface{}) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.closed {
		return false
	}

	if r.buffer[r.writeIndex] != nil {
		// overwrite oldest
		r.readIndex = (r.readIndex + 1) % r.size
	}

	r.buffer[r.writeIndex] = data
	r.writeIndex = (r.writeIndex + 1) % r.size

	// 发送到内部channel，使用非阻塞方式
	if !r.chanClosed {
		func() {
			defer func() {
				if recover() != nil {
					r.chanClosed = true
				}
			}()
			select {
			case r.dataChan <- data:
			default:
				// channel满了，丢弃最旧的数据腾出空间，避免旧引用堆积
				select {
				case <-r.dataChan:
					r.dataChan <- data
				default:
				}
			}
		}()
	}

	r.cond.Signal()
	return true
}

// Clear 排空 buffer 和 dataChan 中的所有数据引用，不关闭 RingBuffer。
// 用于客户端断开连接后立即释放内存。
func (r *RingBuffer) Clear() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	for i := uint64(0); i < r.size; i++ {
		r.buffer[i] = nil
	}
	r.readIndex = 0
	r.writeIndex = 0

	if !r.chanClosed {
		for len(r.dataChan) > 0 {
			<-r.dataChan
		}
	}
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

	// 启动 goroutine 监听 context 取消，确保 Wait() 不会永久阻塞
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			r.cond.Broadcast()
		case <-done:
		}
	}()
	defer close(done)

	for r.buffer[r.readIndex] == nil && !r.closed {
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
