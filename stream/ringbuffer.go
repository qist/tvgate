package stream

import (
	"net/url"
	"strings"
	"sync"

	// "github.com/qist/tvgate/utils/buffer"
	"github.com/qist/tvgate/logger"
)

// StreamRingBuffer 是一个线程安全的环形缓冲区
type StreamRingBuffer struct {
	buf      [][]byte
	capacity int
	maxBytes int64 // 最大字节数限制
	readPos  int
	writePos int
	size     int
	bytes    int64 // 当前缓冲区中的字节数
	mu       sync.Mutex
	cond     *sync.Cond
	closed   bool
}

func NewStreamRingBuffer(capacity int, maxBytes int64) *StreamRingBuffer {
	rb := &StreamRingBuffer{
		buf:      make([][]byte, capacity),
		capacity: capacity,
		maxBytes: maxBytes,
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
	
	dataLen := int64(len(data))
	
	// 如果单个数据包超过了最大字节数限制，则不缓存
	if rb.maxBytes > 0 && dataLen > rb.maxBytes {
		return
	}
	
	// 当缓冲区有数据且（缓冲区满或加入新数据后会超内存限制）时，循环删除旧数据
	// 将 rb.size > 0 放在前面作为短路条件，提高判断效率
	for rb.size > 0 && (rb.size >= rb.capacity || (rb.maxBytes > 0 && rb.bytes+dataLen > rb.maxBytes)) {
		// 移除最旧的数据并释放其内存引用
		oldData := rb.buf[rb.readPos]
		rb.bytes -= int64(len(oldData))
		rb.buf[rb.readPos] = nil // 显式置 nil，帮助 GC 回收
		rb.readPos = (rb.readPos + 1) % rb.capacity
		rb.size--
	}
	
	// 添加新数据到写入位置
	rb.buf[rb.writePos] = data
	rb.bytes += dataLen
	rb.writePos = (rb.writePos + 1) % rb.capacity
	rb.size++
	
	// 唤醒一个等待读取的协程
	rb.cond.Signal()
}

// Pop 读取数据，空时阻塞，关闭后返回 nil
func (rb *StreamRingBuffer) Pop() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	
	// 如果缓冲区为空且未关闭，则等待新数据
	for rb.size == 0 && !rb.closed {
		rb.cond.Wait()
	}
	
	// 如果缓冲区为空且已关闭，则返回 nil
	if rb.size == 0 && rb.closed {
		return nil
	}
	
	// 从缓冲区读取数据
	data := rb.buf[rb.readPos]
	rb.buf[rb.readPos] = nil // 释放引用
	rb.bytes -= int64(len(data))
	rb.readPos = (rb.readPos + 1) % rb.capacity
	rb.size--
	
	return data
}

// Close 关闭缓冲区，唤醒所有等待
func (rb *StreamRingBuffer) Close() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.closed {
		return
	}
	rb.closed = true
	
	// 清空缓冲区并释放所有引用
	for i := 0; i < rb.capacity; i++ {
		rb.buf[i] = nil
	}
	rb.bytes = 0
	rb.size = 0
	
	rb.cond.Broadcast()
}

// Size 返回当前缓冲区中的数据包数量
func (rb *StreamRingBuffer) Size() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.size
}

// Bytes 返回当前缓冲区中的字节数
func (rb *StreamRingBuffer) Bytes() int64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.bytes
}

// IsClosed 返回缓冲区是否已关闭
func (rb *StreamRingBuffer) IsClosed() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.closed
}

// NewOptimalStreamRingBuffer 根据内容类型和URL创建最优大小的环形缓冲区
func NewOptimalStreamRingBuffer(contentType string, u *url.URL) *StreamRingBuffer {
	var ringCapacity int
	var maxBytes int64

	switch {
	case strings.Contains(contentType, "video/mp2t"):
		// TS 视频，缓存大一点
		ringCapacity = 512       // 最大 512 个 chunk
		maxBytes = 512 * 1024 * 1024
	case strings.Contains(contentType, "audio/"):
		// 音频，缓存少一点
		ringCapacity = 128
		maxBytes = 128 * 1024 * 1024
	case strings.Contains(contentType, "mpegurl"):
		// m3u8 playlist，缓存小
		ringCapacity = 64
		maxBytes = 32 * 1024 * 1024
	default:
		// 默认值
		ringCapacity = 128
		maxBytes = 64 * 1024 * 1024
	}

	logger.LogPrintf("[RingBuffer] contentType=%s cap=%d maxBytes=%.2fMB",
		contentType, ringCapacity, float64(maxBytes)/(1024*1024))

	return NewStreamRingBuffer(ringCapacity, maxBytes)
}
