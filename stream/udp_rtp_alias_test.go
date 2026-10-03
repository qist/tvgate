package stream

import (
	"bytes"
	"encoding/binary"
	"sync"
	"testing"
)

// newTestHub 构造仅含 RTP 处理所需字段的最小 hub（不监听网络）。
func newTestHub() *StreamHub {
	h := &StreamHub{
		BufPool:        &sync.Pool{New: func() any { return make([]byte, 2048) }},
		rtpBuffer:      make([]byte, 0, 64*1024),
		rtpSequenceMap: make(map[uint32]*rtpSeqEntry),
	}
	for i := range h.lastCCArr {
		h.lastCCArr[i] = 0xFF
	}
	return h
}

// makeRTPPacket 构造一个 RTP/MP2T 包：12 字节头 + tsPayloads 个 TS 包，
// 每个 TS 包负载字节填充为 tag，用于区分不同包。
func makeRTPPacket(seq uint16, ssrc uint32, tsPayloads int, tag byte) []byte {
	payload := make([]byte, 0, tsPayloads*188)
	for i := 0; i < tsPayloads; i++ {
		ts := make([]byte, 188)
		ts[0] = 0x47
		pid := 0x100 + i
		ts[1] = byte(pid >> 8)
		ts[2] = byte(pid)
		ts[3] = 0x10
		for k := 4; k < 188; k++ {
			ts[k] = tag
		}
		payload = append(payload, ts...)
	}
	pkt := make([]byte, 12+len(payload))
	pkt[0] = 0x80 // RTP v2
	pkt[1] = 33   // MP2T
	binary.BigEndian.PutUint16(pkt[2:4], seq)
	binary.BigEndian.PutUint32(pkt[8:12], ssrc)
	copy(pkt[12:], payload)
	return pkt
}

// TestNewReadRefNoTruncate 回归：读缓冲以实际长度拷入池化缓冲，
// 大 RTP/jumbo 包（> 池默认 2KB）不得被截断（否则 TS 失步 → 绿屏/0KB）。
func TestNewReadRefNoTruncate(t *testing.T) {
	h := newTestHub()
	data := make([]byte, 5000)
	for i := range data {
		data[i] = byte(i * 7)
	}
	ref := h.newReadRef(data)
	if len(ref.data) != len(data) {
		t.Fatalf("长度被截断: got %d want %d", len(ref.data), len(data))
	}
	if !bytes.Equal(ref.data, data) {
		t.Fatal("读缓冲数据被改写/截断")
	}
	ref.Put()
}

// h.rtpBuffer——否则下一包 append 会覆盖上一包的数据，客户端读到撕裂/错乱 TS
// （表现为花屏/绿屏）。v2.1.4 对 chunk 做了脱离处理，这里对齐该语义。
func TestProcessRTPPacketRefNoAlias(t *testing.T) {
	h := newTestHub()

	ref1 := h.processRTPPacketRef(NewBufferRef(makeRTPPacket(1, 0x1111, 7, 0xA1)))
	if ref1 == nil {
		t.Fatal("ref1 为 nil")
	}
	// 记录第一包内容（1 个 TS 包即可判定）
	if ref1.data[0] != 0x47 {
		t.Fatalf("ref1 不是 TS: 0x%02x", ref1.data[0])
	}
	first := append([]byte(nil), ref1.data...)

	ref2 := h.processRTPPacketRef(NewBufferRef(makeRTPPacket(2, 0x1111, 7, 0xB2)))
	if ref2 == nil {
		t.Fatal("ref2 为 nil")
	}

	if bytes.Equal(ref1.data, ref2.data) {
		t.Fatal("两个不同 RTP 包返回了相同底层数据：缓冲区被复用/别名")
	}
	if !bytes.Equal(ref1.data, first) {
		t.Fatal("处理第二个包后，第一个包的数据被覆盖（别名 bug）")
	}
	// 第一包负载 tag 应为 0xA1
	if ref1.data[4] != 0xA1 {
		t.Fatalf("ref1 数据被第二包污染: got 0x%02x want 0xA1", ref1.data[4])
	}
	if ref2.data[4] != 0xB2 {
		t.Fatalf("ref2 数据不对: got 0x%02x want 0xB2", ref2.data[4])
	}
}
