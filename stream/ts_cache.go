package stream

import (
	"container/list"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)
type tsCacheItem struct {
	key        string
	data       []byte
	expireAt   time.Time
	element    *list.Element
}

type TSCache struct {
	mu sync.Mutex

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
	return &TSCache{
		maxBytes: maxBytes,
		ttl:      ttl,
		ll:       list.New(),
		items:    make(map[string]*tsCacheItem),
	}
}

func (c *TSCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if it, ok := c.items[key]; ok {
		if time.Now().After(it.expireAt) {
			c.removeItem(it)
			return nil, false
		}
		c.ll.MoveToFront(it.element)
		return it.data, true
	}
	return nil, false
}

func (c *TSCache) Set(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if it, ok := c.items[key]; ok {
		c.curBytes -= int64(len(it.data))
		it.data = data
		it.expireAt = time.Now().Add(c.ttl)
		c.curBytes += int64(len(data))
		c.ll.MoveToFront(it.element)
		return
	}

	it := &tsCacheItem{
		key:      key,
		data:     data,
		expireAt: time.Now().Add(c.ttl),
	}
	it.element = c.ll.PushFront(it)
	c.items[key] = it
	c.curBytes += int64(len(data))

	c.evictIfNeeded()
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
	c.curBytes -= int64(len(it.data))
}

func (c *TSCache) FetchOrGet(
	key string,
	fetch func() ([]byte, error),
) ([]byte, error) {

	// 1. 先查 cache
	if data, ok := c.Get(key); ok {
		return data, nil
	}

	// 2. singleflight
	v, err, _ := c.sf.Do(key, func() (interface{}, error) {

		// double check
		if data, ok := c.Get(key); ok {
			return data, nil
		}

		data, err := fetch()
		if err != nil {
			return nil, err
		}

		c.Set(key, data)
		return data, nil
	})

	if err != nil {
		return nil, err
	}

	return v.([]byte), nil
}
