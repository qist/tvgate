package ringbuffer

import (
	"context"
	"fmt"
	"sync"
)

// RingBuffer 基于单一有类型 channel 实现的环形缓冲。
//
// 旧实现同时维护一个 []interface{} 环形数组和一个 chan interface{}：
// Push 时把数据引用写入两份容器，消费端却只读其中一份，另一份成为纯死重并滞留
// 旧引用；此外 []byte 装箱成 interface{} 在每次 Push/send 时都会产生一次堆分配。
// 这里改为单一的 chan []byte，既消除双容器冗余，也去掉 interface{} 装箱开销。
type RingBuffer struct {
	size   uint64
	mutex  sync.Mutex
	closed bool

	dataChan   chan []byte
	chanClosed bool
}

// Chan 返回可在 select 语句中使用的 channel。
func (r *RingBuffer) Chan() <-chan []byte {
	return r.dataChan
}

// New 创建指定大小的环形缓冲，size 必须是 2 的幂。
func New(size uint64) (*RingBuffer, error) {
	if size == 0 {
		return nil, fmt.Errorf("size must be positive")
	}

	// make sure size is power of 2
	if (size & (size - 1)) != 0 {
		return nil, fmt.Errorf("size must be a power of 2")
	}

	return &RingBuffer{
		size:     size,
		dataChan: make(chan []byte, size),
	}, nil
}

// Close 关闭缓冲，使 Pull/Chan 的读取方收到结束信号。
func (r *RingBuffer) Close() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.closed {
		return
	}
	r.closed = true
	if !r.chanClosed {
		r.drain()
		close(r.dataChan)
		r.chanClosed = true
	}
}

// Reset 在 Close 之后恢复缓冲，使缓冲可继续写入和读取。
func (r *RingBuffer) Reset() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.closed = false
	if r.chanClosed {
		r.dataChan = make(chan []byte, r.size)
		r.chanClosed = false
	}
}

// Push 在缓冲尾部写入数据；满时丢弃最旧数据，保证慢消费者不会阻塞生产者。
// 仅在缓冲已关闭时返回 false。
func (r *RingBuffer) Push(data []byte) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.closed {
		return false
	}

	select {
	case r.dataChan <- data:
		return true
	default:
	}

	// channel 已满：丢弃最旧元素，为最新数据腾出空间。
	// 生产者持锁串行写入，消费者只会取走元素，因此此处发送必然有空间，不会阻塞。
	select {
	case <-r.dataChan:
	default:
	}

	r.dataChan <- data
	return true
}

// Clear 排空缓冲中残留的数据引用，但不关闭缓冲。
// 用于客户端断开连接后立即释放内存。
func (r *RingBuffer) Clear() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if !r.chanClosed {
		r.drain()
	}
}

// drain 非阻塞排空 channel 中已缓冲的元素，避免滞留数据引用。
func (r *RingBuffer) drain() {
	for {
		select {
		case <-r.dataChan:
		default:
			return
		}
	}
}

// Pull 阻塞直到有数据或缓冲被关闭。
func (r *RingBuffer) Pull() ([]byte, bool) {
	data, ok := <-r.dataChan
	return data, ok
}

// PullWithContext 阻塞直到有数据、缓冲关闭或 ctx 完成。
func (r *RingBuffer) PullWithContext(ctx context.Context) ([]byte, bool) {
	if ctx == nil {
		return r.Pull()
	}
	select {
	case <-ctx.Done():
		return nil, false
	case data, ok := <-r.dataChan:
		return data, ok
	}
}
