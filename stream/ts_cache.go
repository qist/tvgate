package stream

import (
	"container/list"
	"io"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type tsCacheItem struct {
	key      string
	chunks   [][]byte   // 分片缓存
	expireAt time.Time
	element  *list.Element
	size     int64      // 总字节数
}

type TSCache struct {
	mu       sync.Mutex
	maxBytes int64
	curBytes int64
	ttl      time.Duration
	ll       *list.List
	items    map[string]*tsCacheItem
	sf       singleflight.Group
}

var GlobalTSCache = NewTSCache(
	512<<20,       // 512MB
	5*time.Minute, // TTL
)

func NewTSCache(maxBytes int64, ttl time.Duration) *TSCache {
	return &TSCache{
		maxBytes: maxBytes,
		ttl:      ttl,
		ll:       list.New(),
		items:    make(map[string]*tsCacheItem),
	}
}

// ----------------- LRU缓存 -----------------

func (c *TSCache) Get(key string) ([][]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[key]
	if !ok || time.Now().After(it.expireAt) {
		if ok {
			c.removeItem(it)
		}
		return nil, false
	}
	c.ll.MoveToFront(it.element)
	return it.chunks, true
}

func (c *TSCache) SetChunks(key string, chunks [][]byte) {
	var total int64
	for _, b := range chunks {
		total += int64(len(b))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if it, ok := c.items[key]; ok {
		c.curBytes -= it.size
		it.chunks = chunks
		it.expireAt = time.Now().Add(c.ttl)
		it.size = total
		c.curBytes += total
		c.ll.MoveToFront(it.element)
	} else {
		it := &tsCacheItem{
			key:      key,
			chunks:   chunks,
			expireAt: time.Now().Add(c.ttl),
			size:     total,
		}
		it.element = c.ll.PushFront(it)
		c.items[key] = it
		c.curBytes += total
		c.evictIfNeeded()
	}
}

func (c *TSCache) evictIfNeeded() {
	for c.curBytes > c.maxBytes {
		e := c.ll.Back()
		if e == nil {
			return
		}
		it := e.Value.(*tsCacheItem)
		c.removeItem(it)
	}
}

func (c *TSCache) removeItem(it *tsCacheItem) {
	delete(c.items, it.key)
	c.ll.Remove(it.element)
	c.curBytes -= it.size
}

// ----------------- 流式接口 -----------------

// FetchOrGetStream 分片缓存 + 边流式写入
// w: 前端 io.Writer
// fetch: 接收 io.Writer，一边写一边返回
func (c *TSCache) FetchOrGetStream(key string, w io.Writer, fetch func(io.Writer) error) error {
	// 1. 先尝试从 cache 拿
	if chunks, ok := c.Get(key); ok {
		for _, chunk := range chunks {
			_, _ = w.Write(chunk)
		}
		return nil
	}

	// 2. singleflight 保证同 key 只 fetch 一次
	v, err, _ := c.sf.Do(key, func() (interface{}, error) {
		// double check
		if chunks, ok := c.Get(key); ok {
			for _, chunk := range chunks {
				_, _ = w.Write(chunk)
			}
			return chunks, nil
		}

		pr, pw := io.Pipe()
		chunks := make([][]byte, 0)
		var fetchErr error
		wg := sync.WaitGroup{}
		wg.Add(1)

		// 异步读取 pipe，边写前端边累积分片
		go func() {
			defer wg.Done()
			buf := make([]byte, 32*1024) // 32KB 分片
			for {
				n, err := pr.Read(buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					chunks = append(chunks, chunk)
					_, _ = w.Write(chunk)
				}
				if err != nil {
					if err == io.EOF {
						return
					}
					fetchErr = err
					return
				}
			}
		}()

		// fetch 写入 pipe
		fetchErr = fetch(pw)
		pw.Close()
		wg.Wait()
		if fetchErr != nil {
			return nil, fetchErr
		}

		// 缓存分片
		c.SetChunks(key, chunks)
		return chunks, nil
	})

	if err != nil {
		return err
	}

	// 已经边流式写入前端，无需再次写
	_ = v.([][]byte)
	return nil
}
