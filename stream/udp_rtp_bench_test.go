package stream

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// newBenchHub 构造一个仅含基准所需状态的 StreamHub，避免 NewStreamHub 的网络副作用。
func newBenchHub(clients, chSize int) *StreamHub {
	h := &StreamHub{
		Mu:             sync.RWMutex{},
		procMu:         sync.Mutex{},
		Clients:        make(map[string]*hubClient),
		CacheBuffer:    NewRingBuffer(1024),
		Closed:         make(chan struct{}),
		BufPool:        &sync.Pool{New: func() any { return make([]byte, 16*1024) }},
		state:          atomic.Int32{}, // zero = StateStopped
		stateCond:      sync.NewCond(&sync.Mutex{}),
		rtpSequenceMap: make(map[uint32]*rtpSeqEntry),
		AddrList:       []string{"239.0.0.1:1234"},
		ifaces:         []string{},
	}
	for i := 0; i < clients; i++ {
		cl := &hubClient{ch: make(chan *BufferRef, chSize)}
		h.Clients[fmt.Sprint(i)] = cl
	}
	return h
}

func benchBroadcast(b *testing.B, clients, chSize int) {
	h := newBenchHub(clients, chSize)
	bp := h.BufPool
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		backing := bp.Get().([]byte)
		data := backing[:188]
		data[0] = 0x47
		ref := NewPooledBufferRef(backing, data, bp)
		ref.Source = SourceMulticast
		// broadcastRef 自持其引用并在末尾归还；cache/客户端各自持引用
		h.broadcastRef(ref)
	}
}

func BenchmarkBroadcastRef_0Client(b *testing.B) {
	benchBroadcast(b, 0, 1)
}

func BenchmarkBroadcastRef_1Client(b *testing.B) {
	benchBroadcast(b, 1, 64)
}

func BenchmarkBroadcastRef_8Clients(b *testing.B) {
	benchBroadcast(b, 8, 64)
}

// BenchmarkBroadcastRef_Parallel 模拟多路 UDP readLoop 并发广播，量化主锁 RLock 化的并发收益。
func BenchmarkBroadcastRef_Parallel(b *testing.B) {
	h := newBenchHub(8, 64)
	bp := h.BufPool
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			backing := bp.Get().([]byte)
			data := backing[:188]
			data[0] = 0x47
			ref := NewPooledBufferRef(backing, data, bp)
			ref.Source = SourceMulticast
			h.broadcastRef(ref)
		}
	})
}

func BenchmarkProcessRTPPacketRef(b *testing.B) {
	h := newBenchHub(0, 1)
	bp := h.BufPool
	var seq uint16 = 1000
	ssrc := uint32(0x12345678)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		backing := bp.Get().([]byte)
		if cap(backing) < 200 { // 12 字节 RTP 头 + 188 字节 TS 净荷
			backing = make([]byte, 200)
		}
		pkt := backing[:200]
		pkt[0] = 0x80 // RTP v2，无扩展
		pkt[1] = 33   // payloadType=33（MPEG-TS 视频）
		binary.BigEndian.PutUint16(pkt[2:4], seq)
		binary.BigEndian.PutUint32(pkt[8:12], ssrc)
		pkt[12] = 0x47 // 对齐 TS 头

		inRef := NewPooledBufferRef(pkt, pkt, bp)
		inRef.Source = SourceMulticast
		outRef := h.processRTPPacketRef(inRef)
		if outRef == nil {
			// 理论上不会发生（唯一序列号、有效 RTP）
			inRef.Put()
		} else if outRef != inRef {
			inRef.Put()
			outRef.Put()
		} else {
			outRef.Put()
		}
		seq++
	}
}
