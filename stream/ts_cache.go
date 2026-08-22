package stream

import (
	"bufio"
	"container/list"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	tsync "github.com/qist/tvgate/utils/sync"
)

type tsCacheItem struct {
	key    string
	mutex  sync.RWMutex
	waitCh chan struct{} // 用于通知有新数据到达

	chunks [][]byte
	bytes  int64
	err    error

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
	wg          tsync.WaitGroup
}

var ErrCacheClosed = errors.New("cache item closed")

var GlobalTSCache *TSCache

var tsCacheOnce sync.Once

func InitTSCacheFromConfig() {
	tsCacheOnce.Do(func() {
		config.CfgMu.RLock()
		tsCfg := config.Cfg.TS
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
	cache.wg.Go(cache.cleanupLoop)

	return cache
}

func (c *TSCache) Get(key string) (*tsCacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

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
			return c.createItem(key), true
		}
		it.expireAt = time.Now().Add(c.ttl)
		it.accessAt = time.Now()
		c.ll.MoveToFront(it.element)
		return it, false
	}

	return c.createItem(key), true
}

func safeCloseSignal(ch chan struct{}) {
	if ch == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	close(ch)
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

// WriteChunkWithByteTracking 向缓存项写入数据块，并跟踪字节计数到父缓存
func (c *TSCache) WriteChunkWithByteTracking(item *tsCacheItem, data []byte) {
	// 检查数据是否为nil
	if data == nil || item == nil {
		return
	}

	var dataLen int64

	item.mutex.Lock()
	if item.closed {
		item.mutex.Unlock()
		return
	}

	cp := make([]byte, len(data))
	copy(cp, data)
	item.chunks = append(item.chunks, cp)
	item.bytes += int64(len(cp))
	dataLen = int64(len(cp))
	item.mutex.Unlock()

	// 通知等待的读取者有新数据
	func() {
		defer func() {
			_ = recover()
		}()
		select {
		case item.waitCh <- struct{}{}:
		default:
			// 如果通道已满，说明已经有通知在队列中，无需重复发送
		}
	}()

	// 更新缓存的字节计数并清理旧数据
	c.mu.Lock()
	c.curBytes += dataLen

	// 检查是否超过最大字节数，如果超过则触发清理
	cleanupCount := 0
	maxCleanup := 10 // 限制最大清理次数，避免长时间阻塞
	for c.curBytes > c.maxBytes && c.ll.Back() != nil && cleanupCount < maxCleanup {
		// 查找最不活跃的缓存项并移除
		leastActiveElement := c.findLeastActiveItem()
		if leastActiveElement == nil {
			break
		}
		leastActiveItem := leastActiveElement.Value.(*tsCacheItem)
		c.removeItem(leastActiveItem)
		cleanupCount++
	}
	c.mu.Unlock()
}

// 计算缓存项的总字节数
func (c *tsCacheItem) calculateTotalBytes() int64 {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.bytes
}

func (c *tsCacheItem) ReadAll(dst io.Writer, done <-chan struct{}) error {
	// 批量写出缓冲：将多个 chunk 攒成一块再交给底层 Writer，
	// 避免每个 chunk 都单独触发一次 TLS 记录边界与一次 write() 系统调用。
	// 针对 CPU 热点 crypto/tls.writeRecordLocked / Syscall6 的关键优化。
	bw := bufio.NewWriterSize(dst, 256*1024)
	defer bw.Flush()
	flusher, _ := dst.(http.Flusher)

	seq := 1
	waitTimer := time.NewTimer(5 * time.Second)
	defer waitTimer.Stop()

	const (
		maxFlushBytes = 256 * 1024
		maxFlushDelay = 200 * time.Millisecond
	)
	buffered := 0
	lastFlush := time.Now()

	// flush 把攒批的数据刷到底层，并触发 http.Flusher 推送到客户端
	flush := func() error {
		if buffered == 0 {
			return nil
		}
		if err := bw.Flush(); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		buffered = 0
		lastFlush = time.Now()
		return nil
	}

	for {
		c.mutex.RLock()
		if seq <= len(c.chunks) {
			data := c.chunks[seq-1]
			c.mutex.RUnlock()
			if len(data) > 0 {
				n, err := bw.Write(data)
				if err != nil {
					return err
				}
				if n < len(data) {
					return io.ErrShortWrite
				}
				buffered += n
			}
			seq++
			// 攒够阈值或超时再 flush，减少 TLS 记录与 syscall（直播/边下边播延迟可接受）
			if buffered >= maxFlushBytes || time.Since(lastFlush) >= maxFlushDelay {
				if err := flush(); err != nil {
					return err
				}
			}
			continue
		}

		closed := c.closed
		retErr := c.err
		c.mutex.RUnlock()

		if closed {
			// 结尾把剩余数据刷出
			if err := flush(); err != nil {
				return err
			}
			return retErr
		}

		if !waitTimer.Stop() {
			select {
			case <-waitTimer.C:
			default:
			}
		}
		waitTimer.Reset(5 * time.Second)
		select {
		case _, ok := <-c.waitCh:
			if !ok {
				// waitCh 已关闭，缓存可能已 Seal/Close
				// 刷出残留数据后退出，避免无限空转
				_ = flush()
				return nil
			}
		case <-done:
			if err := flush(); err != nil {
				return err
			}
			return nil
		case <-waitTimer.C:
			continue
		}
	}
}

func (c *tsCacheItem) Seal(err error) {
	c.mutex.Lock()
	if c.closed {
		c.mutex.Unlock()
		return
	}
	c.closed = true
	c.err = err
	ch := c.waitCh
	c.mutex.Unlock()
	safeCloseSignal(ch)
}

func (c *tsCacheItem) Close() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.closed {
		c.closed = true
		if c.err == nil {
			c.err = ErrCacheClosed
		}
		safeCloseSignal(c.waitCh)
	}

	c.chunks = nil
	c.bytes = 0
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
	// 由于我们使用 MoveToFront，列表尾部就是最久未使用的项
	return c.ll.Back()
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
	tsCfg := config.Cfg.TS
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
		c.wg.Wait()
	}

	for e := c.ll.Front(); e != nil; {
		next := e.Next()
		item := e.Value.(*tsCacheItem)

		itemBytes := item.calculateTotalBytes()
		item.Close()
		delete(c.items, item.key)
		c.ll.Remove(e)
		c.curBytes -= itemBytes

		e = next
	}

	c.curBytes = 0
	logger.LogPrintf("TS 缓存关闭并已清理完成")
}
