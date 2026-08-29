package stream

import (
	"bytes"
	"testing"
)

func tsPacket(pid uint16, pusi bool, payload []byte, cont byte) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	if pusi {
		pkt[1] = 0x40
	}
	pkt[1] |= byte(pid>>8) & 0x1F
	pkt[2] = byte(pid)
	pkt[3] = 0x10 | (cont & 0x0F) // 仅负载，无适应字段
	copy(pkt[4:], payload)
	return pkt
}

// patPayload 构造 PAT section（不含 pointer_field）。
func patPayload(pmtPID uint16) []byte {
	// table_id(0x00) section_length(13) tsid(2) ver(1) secnum(1) lastsec(1)
	// + program_number(1) + pmtPID + CRC(4)
	return []byte{
		0x00, 0x00, 0x0D,
		0x00, 0x01,
		0xC1,
		0x00, 0x00,
		0x00, 0x01,
		0xE0 | byte(pmtPID>>8), byte(pmtPID),
		0x00, 0x00, 0x00, 0x00,
	}
}

// pmtPayload 构造 PMT section（不含 pointer_field），含一路 H.264 视频。
func pmtPayload(videoPID uint16) []byte {
	// table_id(0x02) section_length(18) program_number(1) ver(1) secnum(1) lastsec(1)
	// pcr_pid(2) prog_info_len(0) + ES(stream_type=0x1B, pid, es_info_len=0) + CRC(4)
	return []byte{
		0x02, 0x00, 0x12,
		0x00, 0x01,
		0xC1,
		0x00, 0x00,
		0xE0, 0x01,
		0xF0, 0x00,
		0x1B,
		0xE0 | byte(videoPID>>8), byte(videoPID),
		0xF0, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
}

// videoPES 构造一个视频 PES（含 PES 头 + 4 字节起始码 + 给定 NAL 类型）。
func videoPES(nalType byte) []byte {
	nal := []byte{0x00, 0x00, 0x00, 0x01, nalType, 0x88, 0x84}
	pes := []byte{0x00, 0x00, 0x01, 0xE0, byte(len(nal) >> 8), byte(len(nal))}
	pes = append(pes, 0x80, 0x00, 0x00) // flags、flags2、PES_header_data_length
	return append(pes, nal...)
}

// buildSegment 构造测试 TS 段：PAT + PMT + 关键帧前的 P 帧 + IDR 关键帧 + 后续数据。
func buildSegment() (seg []byte, kf []byte) {
	const (
		pmtPID   = 0x100
		videoPID = 0x101
	)
	seg = append(seg, tsPacket(0x0000, true, append([]byte{0}, patPayload(pmtPID)...), 0)...)
	seg = append(seg, tsPacket(pmtPID, true, append([]byte{0}, pmtPayload(videoPID)...), 0)...)
	seg = append(seg, tsPacket(videoPID, true, videoPES(1), 1)...) // P 帧（非关键帧）
	kf = tsPacket(videoPID, true, videoPES(5), 2)                   // IDR 关键帧
	seg = append(seg, kf...)
	seg = append(seg, tsPacket(videoPID, false, []byte{0x11, 0x22, 0x33}, 3)...)
	return seg, kf
}

func TestTSKeyframeScanner(t *testing.T) {
	seg, kf := buildSegment()

	// 分块喂入（模拟网络分块，跨块拼包）
	s := new(tsKeyframeScanner)
	var out, tables []byte
	step := 100
	for i := 0; i < len(seg); i += step {
		end := i + step
		if end > len(seg) {
			end = len(seg)
		}
		if s.Feed(seg[i:end]) {
			tables, out = s.Flush()
			break
		}
	}

	if len(out) == 0 {
		t.Fatal("未定位到关键帧")
	}
	if !bytes.Equal(out[:188], kf) {
		t.Fatalf("缓存起点不是关键帧包: %x...", out[:188])
	}
	if len(tables) != 188*2 {
		t.Fatalf("节目表应为 PAT+PMT 共 %d 字节, got %d", 188*2, len(tables))
	}
	// 后续数据也在输出中（关键帧包之后的内容完整保留）
	if len(out) <= 188 {
		t.Fatalf("关键帧后的数据丢失: len=%d", len(out))
	}
}

func TestTSKeyframeScannerNoKeyframe(t *testing.T) {
	seg, _ := buildSegment()
	// 去掉关键帧包及其后续，只留 PAT+PMT+P 帧 → 无关键帧，应从头返回全部
	seg = seg[:188*3]

	s := new(tsKeyframeScanner)
	var done bool
	for i := 0; i < len(seg); i += 100 {
		end := i + 100
		if end > len(seg) {
			end = len(seg)
		}
		if s.Feed(seg[i:end]) {
			done = true
			break
		}
	}
	if done {
		t.Fatal("无关键帧时 Feed 不应提前判定完成")
	}
	// 流结束：直接 Flush，应从头缓存全部数据，节目表仍在
	tables, out := s.Flush()
	if !bytes.Equal(out, seg) {
		t.Fatalf("无关键帧时应返回全部数据, got %d bytes, want %d", len(out), len(seg))
	}
	if len(tables) != 188*2 {
		t.Fatalf("节目表应保留, got %d", len(tables))
	}
}

func TestTSKeyframeScannerFeedOnce(t *testing.T) {
	// 一次性喂入整个段，也应正确找到关键帧
	seg, kf := buildSegment()
	s := new(tsKeyframeScanner)
	if !s.Feed(seg) {
		t.Fatal("一次性喂入应能定位关键帧")
	}
	_, out := s.Flush()
	if len(out) == 0 || !bytes.Equal(out[:188], kf) {
		t.Fatalf("一次性喂入结果错误: %x...", out)
	}
}
