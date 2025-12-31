package stream

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/qist/tvgate/logger"
)

/*
===========================
TS Ring Buffer
===========================
*/

type TsRingBuffer struct {
	mu      sync.RWMutex
	buffer  [][]byte
	size    int
	head    int
	count   int
	baseSeq int64
}

func NewTsRingBuffer(size int) *TsRingBuffer {
	if size <= 0 {
		size = 16384
	}
	return &TsRingBuffer{
		buffer: make([][]byte, size),
		size:   size,
	}
}

func (rb *TsRingBuffer) Write(pkt []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	cp := make([]byte, len(pkt))
	copy(cp, pkt)

	if rb.count < rb.size {
		pos := (rb.head + rb.count) % rb.size
		rb.buffer[pos] = cp
		rb.count++
	} else {
		rb.buffer[rb.head] = cp
		rb.head = (rb.head + 1) % rb.size
		rb.baseSeq++
	}
}

func (rb *TsRingBuffer) ReadFrom(seq int64) (out [][]byte, nextSeq int64) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return nil, seq
	}

	if seq < rb.baseSeq {
		seq = rb.baseSeq
	}

	end := rb.baseSeq + int64(rb.count)
	if seq >= end {
		return nil, seq
	}

	start := int(seq - rb.baseSeq)
	out = make([][]byte, 0, rb.count-start)

	for i := start; i < rb.count; i++ {
		pos := (rb.head + i) % rb.size
		if rb.buffer[pos] != nil {
			out = append(out, rb.buffer[pos])
		}
	}
	return out, end
}

func (rb *TsRingBuffer) TailSeq() int64 {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.baseSeq + int64(rb.count)
}

/*
===========================
FCC Session
===========================
*/

type FccSession struct {
	ConnID     string
	ChannelID  string
	ReadSeq    int64
	LastActive time.Time
}

func NewFccSession(connID, ch string, startSeq int64) *FccSession {
	now := time.Now()
	return &FccSession{
		ConnID:     connID,
		ChannelID:  ch,
		ReadSeq:    startSeq,
		LastActive: now,
	}
}

func (s *FccSession) Touch() {
	s.LastActive = time.Now()
}

/*
===========================
Multicast Channel
===========================
*/

type MulticastChannel struct {
	ID       string
	Cache    *TsRingBuffer
	Sessions map[string]*FccSession

	mu       sync.RWMutex
	refCount int32
}

func NewMulticastChannel(id string, cacheSize int) *MulticastChannel {
	return &MulticastChannel{
		ID:       id,
		Cache:    NewTsRingBuffer(cacheSize),
		Sessions: make(map[string]*FccSession),
	}
}

func (mc *MulticastChannel) AddSession(connID string) *FccSession {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	startSeq := mc.Cache.TailSeq()
	s := NewFccSession(connID, mc.ID, startSeq)
	mc.Sessions[connID] = s
	atomic.AddInt32(&mc.refCount, 1)

	logger.LogPrintf("[FCC] join channel=%s conn=%s seq=%d",
		mc.ID, connID, startSeq)
	return s
}

func (mc *MulticastChannel) RemoveSession(connID string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if _, ok := mc.Sessions[connID]; ok {
		delete(mc.Sessions, connID)
		atomic.AddInt32(&mc.refCount, -1)
		logger.LogPrintf("[FCC] leave channel=%s conn=%s", mc.ID, connID)
	}
}

func (mc *MulticastChannel) AddTsPacket(pkt []byte) {
	mc.Cache.Write(pkt)
}

func (mc *MulticastChannel) ReadForSession(s *FccSession) [][]byte {
	data, next := mc.Cache.ReadFrom(s.ReadSeq)
	if len(data) > 0 {
		s.ReadSeq = next
		s.Touch()
	}
	return data
}

func (mc *MulticastChannel) RefCount() int32 {
	return atomic.LoadInt32(&mc.refCount)
}
