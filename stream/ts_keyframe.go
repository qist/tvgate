package stream

import "bytes"

// maxKeyframeWaitBytes 关键帧扫描缓冲上限：超过仍未找到 IDR 则放弃扫描，从头下发。
const maxKeyframeWaitBytes = 4 << 20 // 4MB

// tsKeyframeScanner 轻量 TS 段扫描器：定位首个 H.264/HEVC IDR（关键帧）所在 TS 包的起始位置。
//
// 设计要点（对应"是否浪费 CPU/内存"的考量）：
//   - 每个缓存段只在下载时扫一遍，结果按段摊销，不逐客户端重扫；
//   - 仅扫描视频 PID 的 PUSI 包（帧起始）及其后 1 个续包，命中即停（GOP 起点的
//     AUD/SPS/PPS/IDR 起始码基本都在头 1-2 个 TS 包内）；
//   - 关键帧前的数据只在扫描期间暂存（上限 maxKeyframeWaitBytes），找到后丢弃，
//     仅保留首个 PAT/PMT（约 376B）用于下发前置。
type tsKeyframeScanner struct {
	pmtPID   uint16
	videoPID uint16
	isHEVC   bool

	pre        []byte // 已缓冲未决数据（关键帧前，或含关键帧）
	consumed   int    // 已完成整包扫描的字节位置（避免重复扫描）
	synced     bool   // 是否已定位同步字节
	keyframeAt int    // 关键帧 TS 包在 pre 中的起始偏移
	found      bool
	done       bool

	tables  []byte // 首个 PAT+PMT
	havePAT bool
	havePMT bool

	scanNext bool   // 下一个视频包继续扫（承接跨包起始码）
	tail     []byte // 上一个视频包负载末尾最多 3 字节
}

// Feed 送入一段下载数据。返回 true 表示扫描已出结果（找到关键帧或超限放弃），
// 之后必须调用 Flush() 取出缓存起点数据，且不再调用 Feed。
func (s *tsKeyframeScanner) Feed(chunk []byte) bool {
	if s.done {
		return true
	}
	if len(chunk) > 0 {
		s.pre = append(s.pre, chunk...)
	}
	if len(s.pre) > maxKeyframeWaitBytes {
		s.done = true
		return true
	}

	off := s.consumed
	if !s.synced {
		idx := bytes.IndexByte(s.pre, 0x47)
		if idx < 0 {
			return false
		}
		off = idx
		s.synced = true
	}

	for off+188 <= len(s.pre) {
		if s.scanPacket(s.pre[off : off+188]) {
			s.keyframeAt = off
			s.found = true
			s.done = true
			return true
		}
		off += 188
	}
	s.consumed = off
	return false
}

// Flush 返回应前置的节目表（PAT+PMT）与应作为缓存起点的数据。
// - 找到关键帧：从关键帧 TS 包起返回，此前前缀丢弃
// - 未找到/放弃：返回全部已缓冲数据（从头下发）
func (s *tsKeyframeScanner) Flush() (tables, data []byte) {
	s.done = true
	start := 0
	if s.found {
		start = s.keyframeAt
	}
	if start >= len(s.pre) {
		return s.tables, nil
	}
	return s.tables, s.pre[start:]
}

// scanPacket 解析单个 188B TS 包，返回是否发现关键帧。
func (s *tsKeyframeScanner) scanPacket(pkt []byte) bool {
	if pkt[0] != 0x47 {
		return false
	}
	pid := (uint16(pkt[1]&0x1F) << 8) | uint16(pkt[2])
	pusi := pkt[1]&0x40 != 0

	payloadStart := 4
	if pkt[3]&0x20 != 0 { // adaptation_field_control 含适应字段
		payloadStart = 5 + int(pkt[4])
	}
	if payloadStart >= 188 {
		return false
	}

	switch {
	case pid == 0x0000: // PAT
		if pusi {
			s.parsePAT(pkt, payloadStart)
		}
	case s.pmtPID != 0 && pid == s.pmtPID: // PMT
		if pusi {
			s.parsePMT(pkt, payloadStart)
		}
	case s.videoPID != 0 && pid == s.videoPID:
		if pusi {
			// 帧起始包：扫描本包负载（PES 头后紧邻 AUD/SPS/PPS/IDR 起始码）
			if s.scanNalIDR(pkt[payloadStart:188]) {
				return true
			}
			// 记下负载末尾，下一个视频包可能承接起始码
			s.tail = last3(pkt[payloadStart:188])
			s.scanNext = true
		} else if s.scanNext {
			// 拼接上个视频包末尾与当前包负载，避免跨包起始码漏检
			buf := make([]byte, 0, len(s.tail)+188-payloadStart)
			buf = append(buf, s.tail...)
			buf = append(buf, pkt[payloadStart:188]...)
			s.scanNext = false
			return s.scanNalIDR(buf)
		}
	default:
		s.scanNext = false
	}
	return false
}

func last3(b []byte) []byte {
	if len(b) <= 3 {
		return b
	}
	return b[len(b)-3:]
}

// parsePAT 解析 PAT 获取 PMT PID，并保留首个 PAT 包。
func (s *tsKeyframeScanner) parsePAT(pkt []byte, start int) {
	if !s.havePAT {
		s.tables = append(s.tables, pkt...)
		s.havePAT = true
	}
	p := start + 1 // pointer_field
	if p+8 >= 188 {
		return
	}
	sectionLen := (int(pkt[p+1]&0x0F) << 8) | int(pkt[p+2])
	end := p + 3 + sectionLen - 4 // 去掉 CRC
	if end > 188 {
		end = 188
	}
	for q := p + 8; q+4 <= end; q += 4 {
		prog := (uint16(pkt[q]) << 8) | uint16(pkt[q+1])
		pid := (uint16(pkt[q+2]&0x1F) << 8) | uint16(pkt[q+3])
		if prog != 0 { // 非 NIT 即 PMT PID
			s.pmtPID = pid
			return
		}
	}
}

// parsePMT 解析 PMT 获取视频 PID，并保留首个 PMT 包。
func (s *tsKeyframeScanner) parsePMT(pkt []byte, start int) {
	if !s.havePMT {
		s.tables = append(s.tables, pkt...)
		s.havePMT = true
	}
	p := start + 1 // pointer_field
	if p+12 >= 188 {
		return
	}
	sectionLen := (int(pkt[p+1]&0x0F) << 8) | int(pkt[p+2])
	progInfoLen := (int(pkt[p+10]&0x0F) << 8) | int(pkt[p+11])
	end := p + 3 + sectionLen - 4
	if end > 188 {
		end = 188
	}
	for q := p + 12 + progInfoLen; q+5 <= end; {
		streamType := pkt[q]
		pid := (uint16(pkt[q+1]&0x1F) << 8) | uint16(pkt[q+2])
		esInfoLen := (int(pkt[q+3]&0x0F) << 8) | int(pkt[q+4])
		if (streamType == 0x1B || streamType == 0x24) && s.videoPID == 0 {
			s.videoPID = pid
			s.isHEVC = streamType == 0x24
		}
		q += 5 + esInfoLen
	}
}

// scanNalIDR 在数据中寻找 Annex B 起始码并判断首个 NAL 是否为 IDR。
// 注：视频 PUSI 包的负载以 PES 头 00 00 01 开头，其后的 stream_id(0xE0)
// 不会被误判为 IDR（H.264 type=0 / HEVC type=112）。
func (s *tsKeyframeScanner) scanNalIDR(buf []byte) bool {
	i := 0
	for i+2 < len(buf) {
		if buf[i] != 0 || buf[i+1] != 0 {
			i++
			continue
		}
		if buf[i+2] == 1 { // 3 字节起始码
			if i+3 < len(buf) && s.nalIDR(buf[i+3]) {
				return true
			}
			i += 3
			continue
		}
		if i+3 < len(buf) && buf[i+3] == 1 { // 4 字节起始码 00 00 00 01
			if i+4 < len(buf) && s.nalIDR(buf[i+4]) {
				return true
			}
			i += 4
			continue
		}
		i++
	}
	return false
}

// nalIDR 判断 NAL 头字节是否为 IDR（关键帧）。
func (s *tsKeyframeScanner) nalIDR(b byte) bool {
	if s.isHEVC {
		t := (b >> 1) & 0x3F
		return t == 19 || t == 20 // IDR_W_RADL / IDR_N_LP
	}
	return b&0x1F == 5
}
