package stream

import (
	"container/list"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type tsCacheChunk struct {
	data       []byte
	next       *tsCacheChunk
}

type tsCacheItem struct {
	key        string
	head       *tsCacheChunk
	tail       *tsCacheChunk
	mutex      sync.RWMutex
	waitCh     chan struct{} // 用于通知有新数据到达
	expireAt   time.Time
	element    *list.Element
	closed     bool
}

type TSCache struct {
	mu sync.RWMutex

	maxBytes int64
	curBytes int64

	ttl time.Duration

	ll    *list.List
	items map[string]*tsCacheItem

	sf singleflight.Group
}

var GlobalTSCache = NewTSCache(
	512<<20,        // 512MB
	5*time.Minute, // TS TTL
)

func NewTSCache(maxBytes int64, ttl time.Duration) *TSCache {
	cache := &TSCache{
		maxBytes: maxBytes,
		ttl:      ttl,
		ll:       list.New(),
		items:    make(map[string]*tsCacheItem),
	}
	
	// 启动清理过期项目的goroutine
	go cache.cleanupLoop()
	
	return cache
}

func (c *TSCache) Get(key string) (*tsCacheItem, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if it, ok := c.items[key]; ok {
		if time.Now().After(it.expireAt) {
			// 注意：这里不删除项目，因为可能有客户端正在读取
			// 项目将在写入端被标记为过期，或通过后台清理
			return nil, false
		}
		return it, true
	}
	return nil, false
}

func (c *TSCache) GetOrCreate(key string) (*tsCacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if it, ok := c.items[key]; ok {
		if time.Now().After(it.expireAt) {
			c.removeItem(it)
			// 创建新的项目
			return c.createItem(key), true
		}
		// 更新过期时间
		it.expireAt = time.Now().Add(c.ttl)
		c.ll.MoveToFront(it.element)
		return it, false
	}
	
	return c.createItem(key), true
}

func (c *TSCache) createItem(key string) *tsCacheItem {
	it := &tsCacheItem{
		key:      key,
		waitCh:   make(chan struct{}, 1), // 非阻塞的单值通道
		expireAt: time.Now().Add(c.ttl),
	}
	it.element = c.ll.PushFront(it)
	c.items[key] = it
	
	return it
}

func (c *tsCacheItem) WriteChunk(data []byte) {
	// 检查数据是否为nil
	if data == nil {
		return
	}
	
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	if c.closed {
		return
	}
	
	// 创建新的块
	newChunk := &tsCacheChunk{
		data: make([]byte, len(data)),
	}
	copy(newChunk.data, data)
	
	if c.tail == nil {
		c.head = newChunk
		c.tail = newChunk
	} else {
		c.tail.next = newChunk
		c.tail = newChunk
	}
	
	
	// 通知等待的读取者有新数据
	select {
	case c.waitCh <- struct{}{}:
	default:
		// 如果通道已满，说明已经有通知在队列中，无需重复发送
	}
}

func (c *tsCacheItem) ReadAll(dst io.Writer, done <-chan struct{}) error {
	var current *tsCacheChunk
	
	for {
		// 先尝试读取已有的数据
		c.mutex.RLock()
		
		// 从头开始读取，确保新客户端能获取到已有的数据
		if current == nil {
			current = c.head
		}
		
		// 读取所有已有的数据
		for current != nil {
			// 检查current是否为nil，防止并发访问问题
			if current == nil {
				c.mutex.RUnlock()
				break
			}
			
			data := current.data
			next := current.next  // 保存next指针，避免在持有读锁时访问可能被修改的节点
			c.mutex.RUnlock()
			
			if len(data) > 0 {
				n, err := dst.Write(data)
				if err != nil {
					// 客户端连接可能已断开，返回错误
					return err
				}
				// 检查是否只写入了部分数据
				if n < len(data) {
					return io.ErrShortWrite
				}
				
				if f, ok := dst.(http.Flusher); ok {
					f.Flush()
				}
			}
			
			// 移动到下一个块
			current = next
			
			// 重新获取读锁以检查状态
			c.mutex.RLock()
		}
		
		// 检查是否已关闭
		if c.closed {
			c.mutex.RUnlock()
			break // 退出循环，不再等待新数据
		}
		
		c.mutex.RUnlock()
		
		// 等待新数据或完成信号
		select {
		case <-c.waitCh:
			// 有新数据，继续循环
			continue
		case <-done:
			// 收到完成信号，退出
			return nil
		}
	}
	
	return nil
}

func (c *tsCacheItem) Close() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	c.closed = true
	close(c.waitCh)
}

func (c *TSCache) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		
		for e := c.ll.Back(); e != nil; {
			it := e.Value.(*tsCacheItem)
			next := e.Prev()
			
			if now.After(it.expireAt) {
				c.removeItem(it)
			}
			
			e = next
		}
		
		c.mu.Unlock()
	}
}

func (c *TSCache) removeItem(it *tsCacheItem) {
	delete(c.items, it.key)
	c.ll.Remove(it.element)
	
	// 注意：这里我们不计算实际的字节数，因为流式缓存难以精确计算
	// 可以考虑在WriteChunk时跟踪字节计数
}

func (c *TSCache) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if it, ok := c.items[key]; ok {
		c.removeItem(it)
	}
}