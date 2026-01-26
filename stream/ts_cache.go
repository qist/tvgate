package stream

import (
	"container/list"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

type tsCacheChunk struct {
	data []byte
	next *tsCacheChunk
}

type tsCacheItem struct {
	key      string
	head     *tsCacheChunk
	tail     *tsCacheChunk
	mutex    sync.RWMutex
	waitCh   chan struct{} // 用于通知有新数据到达
	expireAt time.Time
	element  *list.Element
	closed   bool
	accessAt time.Time // 添加最后访问时间，用于跟踪活跃度
}

type TSCache struct {
	mu sync.RWMutex

	maxBytes int64
	curBytes int64

	ttl time.Duration

	ll    *list.List
	items map[string]*tsCacheItem

	sf singleflight.Group

	// 控制清理 goroutine 的通道
	cleanupDone chan struct{}
}

var GlobalTSCache *TSCache

var tsCacheOnce sync.Once

func InitTSCacheFromConfig() {
	tsCacheOnce.Do(func() {
		config.CfgMu.RLock()
		tsCfg := config.Cfg.Server.TS
		config.CfgMu.RUnlock()

		// 🔑 开关判断
		if !*tsCfg.Enable {
			logger.LogPrintf("TS缓存未启用")
			GlobalTSCache = nil
			return
		}

		cacheSize := int64(tsCfg.CacheSize) << 20
		cacheTTL := tsCfg.CacheTTL

		logger.LogPrintf(
			"TS缓存初始化: %dMB, TTL=%v",
			cacheSize>>20,
			cacheTTL,
		)

		GlobalTSCache = NewTSCache(cacheSize, cacheTTL)
	})
}

func NewTSCache(maxBytes int64, ttl time.Duration) *TSCache {
	cache := &TSCache{
		maxBytes:    maxBytes,
		ttl:         ttl,
		ll:          list.New(),
		items:       make(map[string]*tsCacheItem),
		cleanupDone: make(chan struct{}),
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
		// 更新访问时间
		it.accessAt = time.Now()
		c.ll.MoveToFront(it.element)
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
		// 更新访问时间
		it.accessAt = time.Now()
		c.ll.MoveToFront(it.element)
		return it, false
	}

	return c.createItem(key), true
}

// WriteChunkToItem 将数据块写入指定的缓存项，并管理缓存大小
func (c *TSCache) WriteChunkToItem(item *tsCacheItem, data []byte) {
	// 检查数据是否为nil
	if data == nil || item == nil {
		return
	}

	item.mutex.Lock()
	defer item.mutex.Unlock()

	if item.closed {
		return
	}

	// 创建新的块
	newChunk := &tsCacheChunk{
		data: make([]byte, len(data)),
	}
	copy(newChunk.data, data)

	if item.tail == nil {
		item.head = newChunk
		item.tail = newChunk
	} else {
		item.tail.next = newChunk
		item.tail = newChunk
	}

	// 通知等待的读取者有新数据
	select {
	case item.waitCh <- struct{}{}:
	default:
		// 如果通道已满，说明已经有通知在队列中，无需重复发送
	}

	// 更新缓存的字节计数
	c.mu.Lock()
	c.curBytes += int64(len(data))

	// 检查是否超过最大字节数，如果超过则触发清理
	for c.curBytes > c.maxBytes && c.ll.Back() != nil {
		// 查找最不活跃的缓存项并移除
		leastActiveElement := c.findLeastActiveItem()
		if leastActiveElement != nil {
			leastActiveItem := leastActiveElement.Value.(*tsCacheItem)
			// 计算并减去该缓存项的字节数
			itemBytes := leastActiveItem.calculateTotalBytes()
			c.curBytes -= itemBytes
			c.removeItem(leastActiveItem)
		}
	}
	c.mu.Unlock()
}

func (c *TSCache) createItem(key string) *tsCacheItem {
	it := &tsCacheItem{
		key:      key,
		waitCh:   make(chan struct{}, 1), // 非阻塞的单值通道
		expireAt: time.Now().Add(c.ttl),
		accessAt: time.Now(), // 设置初始访问时间
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

// WriteChunkWithByteTracking 向缓存项写入数据块，并跟踪字节计数到父缓存
func (c *TSCache) WriteChunkWithByteTracking(item *tsCacheItem, data []byte) {
	// 检查数据是否为nil
	if data == nil || item == nil {
		return
	}

	item.mutex.Lock()
	defer item.mutex.Unlock()

	if item.closed {
		return
	}

	// 创建新的块
	newChunk := &tsCacheChunk{
		data: make([]byte, len(data)),
	}
	copy(newChunk.data, data)

	if item.tail == nil {
		item.head = newChunk
		item.tail = newChunk
	} else {
		item.tail.next = newChunk
		item.tail = newChunk
	}

	// 更新缓存的字节计数
	c.mu.Lock()
	c.curBytes += int64(len(data))

	// 检查是否超过最大字节数，如果超过则触发清理
	for c.curBytes > c.maxBytes && c.ll.Back() != nil {
		// 查找最不活跃的缓存项并移除
		leastActiveElement := c.findLeastActiveItem()
		if leastActiveElement != nil {
			leastActiveItem := leastActiveElement.Value.(*tsCacheItem)
			// 计算并减去该缓存项的字节数
			itemBytes := leastActiveItem.calculateTotalBytes()
			c.curBytes -= itemBytes
			c.removeItem(leastActiveItem)
		}
	}
	c.mu.Unlock()

	// 通知等待的读取者有新数据
	select {
	case item.waitCh <- struct{}{}:
	default:
		// 如果通道已满，说明已经有通知在队列中，无需重复发送
	}
}

// 计算缓存项的总字节数
func (c *tsCacheItem) calculateTotalBytes() int64 {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	var total int64
	current := c.head
	for current != nil {
		total += int64(len(current.data))
		current = current.next
	}
	return total
}

func (c *tsCacheItem) ReadAll(dst io.Writer, done <-chan struct{}) error {
	var current *tsCacheChunk

	for {
		c.mutex.RLock()

		if current == nil {
			current = c.head
		}

		for current != nil {
			data := current.data
			next := current.next
			c.mutex.RUnlock()

			if len(data) > 0 {
				n, err := dst.Write(data)
				if err != nil {
					return err
				}
				if n < len(data) {
					return io.ErrShortWrite
				}
				if f, ok := dst.(http.Flusher); ok {
					f.Flush()
				}
			}

			current = next
			c.mutex.RLock()
		}

		if c.closed {
			c.mutex.RUnlock()
			return nil
		}

		c.mutex.RUnlock()

		select {
		case _, ok := <-c.waitCh:
			if !ok {
				return nil
			}
		case <-done:
			return nil
		}
	}
}

func (c *tsCacheItem) Close() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.closed {
		return
	}
	c.closed = true
	close(c.waitCh)

	// 清理数据块链表，防止内存泄露
	current := c.head
	for current != nil {
		next := current.next
		current.data = nil
		current.next = nil
		current = next
	}
	c.head = nil
	c.tail = nil
}

func (c *TSCache) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
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
		case <-c.cleanupDone:
			return
		}
	}
}

// findLeastActiveItem 查找最不活跃的缓存项（最长时间未访问的项）
func (c *TSCache) findLeastActiveItem() *list.Element {
	var leastActive *list.Element
	earliestTime := time.Now()

	for e := c.ll.Back(); e != nil; e = e.Prev() {
		it := e.Value.(*tsCacheItem)
		if it.accessAt.Before(earliestTime) {
			earliestTime = it.accessAt
			leastActive = e
		}
	}

	return leastActive
}

func (c *TSCache) removeItem(it *tsCacheItem) {
	delete(c.items, it.key)
	c.ll.Remove(it.element)

	// 减少缓存中的字节数
	itemBytes := it.calculateTotalBytes()
	c.curBytes -= itemBytes

	// 正确关闭缓存项，释放资源
	it.Close()
}

func (c *TSCache) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if it, ok := c.items[key]; ok {
		c.removeItem(it)
	}
}

func InitOrUpdateTSCacheFromConfig() {
	config.CfgMu.RLock()
	tsCfg := config.Cfg.Server.TS
	config.CfgMu.RUnlock()

	// 🔴 关闭语义 - 检查 Enable 指针是否为 nil 或为 false
	enable := false // 默认关闭
	if tsCfg.Enable != nil {
		enable = *tsCfg.Enable
	}

	if !enable || tsCfg.CacheSize <= 0 {
		if GlobalTSCache != nil {
			GlobalTSCache.Close()
			GlobalTSCache = nil
			logger.LogPrintf("TS缓存已关闭")
		}
		return
	}

	newMaxBytes := int64(tsCfg.CacheSize) << 20
	newTTL := tsCfg.CacheTTL

	// 🟢 创建
	if GlobalTSCache == nil {
		GlobalTSCache = NewTSCache(newMaxBytes, newTTL)
		logger.LogPrintf(
			"TS缓存创建: %dMB TTL=%v",
			tsCfg.CacheSize,
			newTTL,
		)
		return
	}

	// 🟡 更新
	GlobalTSCache.UpdateConfig(newMaxBytes, newTTL)
}

// UpdateConfig 更新缓存配置
func (c *TSCache) UpdateConfig(newMaxBytes int64, newTTL time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 更新缓存大小限制
	c.maxBytes = newMaxBytes

	// 更新TTL
	c.ttl = newTTL

	// 如果新限制更小，清理超出的部分
	if c.curBytes > c.maxBytes {
		for c.curBytes > c.maxBytes && c.ll.Back() != nil {
			// 查找最不活跃的缓存项并移除
			leastActiveElement := c.findLeastActiveItem()
			if leastActiveElement != nil {
				leastActiveItem := leastActiveElement.Value.(*tsCacheItem)
				itemBytes := leastActiveItem.calculateTotalBytes()
				c.curBytes -= itemBytes
				c.removeItem(leastActiveItem)
			}
		}
	}
}

func (c *TSCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 关闭清理 goroutine
	if c.cleanupDone != nil {
		close(c.cleanupDone)
		c.cleanupDone = nil
	}

	for e := c.ll.Front(); e != nil; {
		next := e.Next()
		item := e.Value.(*tsCacheItem)

		// 直接关闭缓存项，而不是通过removeItem以避免潜在死锁
		item.Close()

		// 从映射中删除
		delete(c.items, item.key)
		// 从链表中移除
		c.ll.Remove(e)

		// 减少缓存中的字节数
		itemBytes := item.calculateTotalBytes()
		c.curBytes -= itemBytes

		e = next
	}

	c.curBytes = 0
	logger.LogPrintf("TS 缓存关闭并已清理完成")
}
