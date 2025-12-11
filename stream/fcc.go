package stream

import (
	"errors"
	"net"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
)

// FCCPacket represents a packet in the FCC buffer
type FCCPacket struct {
	Data      []byte
	Timestamp time.Time
	Sequence  uint16
}

// FCCBuffer manages fast channel change buffers
type FCCBuffer struct {
	Mu           sync.RWMutex
	Buffer       []*FCCPacket
	Size         int
	MaxSize      int
	LastSequence uint16
	Closed       chan struct{}
}

// NewFCCBuffer creates a new FCC buffer with specified size
func NewFCCBuffer(maxSize int) *FCCBuffer {
	return &FCCBuffer{
		Buffer:  make([]*FCCPacket, 0),
		MaxSize: maxSize,
		Size:    0,
		Closed:  make(chan struct{}),
	}
}

// AddPacket adds a new packet to the FCC buffer
func (f *FCCBuffer) AddPacket(data []byte, sequence uint16) {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	select {
	case <-f.Closed:
		return
	default:
	}

	packet := &FCCPacket{
		Data:      append([]byte(nil), data...), // 创建数据副本
		Timestamp: time.Now(),
		Sequence:  sequence,
	}

	// 添加到缓冲区
	if f.Size < f.MaxSize {
		f.Buffer = append(f.Buffer, packet)
		f.Size++
	} else {
		// 循环缓冲区，替换最旧的数据
		f.Buffer = append(f.Buffer[1:], packet)
	}

	f.LastSequence = sequence
}

// GetPacketsSince returns packets since a given timestamp for fast channel change
func (f *FCCBuffer) GetPacketsSince(since time.Time) []*FCCPacket {
	f.Mu.RLock()
	defer f.Mu.RUnlock()

	select {
	case <-f.Closed:
		return nil
	default:
	}

	result := make([]*FCCPacket, 0)
	for _, packet := range f.Buffer {
		if packet.Timestamp.After(since) {
			result = append(result, packet)
		}
	}

	return result
}

// GetLatestPackets returns the latest N packets
func (f *FCCBuffer) GetLatestPackets(count int) []*FCCPacket {
	f.Mu.RLock()
	defer f.Mu.RUnlock()

	select {
	case <-f.Closed:
		return nil
	default:
	}

	if count >= f.Size {
		// 返回所有数据的副本
		result := make([]*FCCPacket, f.Size)
		for i, packet := range f.Buffer {
			result[i] = &FCCPacket{
				Data:      append([]byte(nil), packet.Data...),
				Timestamp: packet.Timestamp,
				Sequence:  packet.Sequence,
			}
		}
		return result
	}

	// 返回最新的N个包
	startIndex := f.Size - count
	result := make([]*FCCPacket, count)
	for i, packet := range f.Buffer[startIndex:] {
		result[i] = &FCCPacket{
			Data:      append([]byte(nil), packet.Data...),
			Timestamp: packet.Timestamp,
			Sequence:  packet.Sequence,
		}
	}
	return result
}

// Clear clears the FCC buffer
func (f *FCCBuffer) Clear() {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	f.Buffer = f.Buffer[:0]
	f.Size = 0
	f.LastSequence = 0
}

// Close closes the FCC buffer
func (f *FCCBuffer) Close() {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	select {
	case <-f.Closed:
		return
	default:
		close(f.Closed)
	}

	f.Buffer = nil
	f.Size = 0
}

// FCCManager manages FCC functionality for multicast streams
type FCCManager struct {
	Mu      sync.RWMutex
	Buffers map[string]*FCCBuffer // key = stream identifier
	Closed  chan struct{}
}

// NewFCCManager creates a new FCC manager
func NewFCCManager() *FCCManager {
	manager := &FCCManager{
		Buffers: make(map[string]*FCCBuffer),
		Closed:  make(chan struct{}),
	}

	// 启动定期清理过期缓冲区的goroutine
	go manager.cleanupExpiredBuffers()

	return manager
}

// GetOrCreateBuffer gets or creates an FCC buffer for a stream
func (f *FCCManager) GetOrCreateBuffer(streamID string, bufferSize int) *FCCBuffer {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	select {
	case <-f.Closed:
		return nil
	default:
	}

	buffer, exists := f.Buffers[streamID]
	if !exists {
		buffer = NewFCCBuffer(bufferSize)
		f.Buffers[streamID] = buffer
	}

	return buffer
}

// GetBuffer gets an existing FCC buffer for a stream
func (f *FCCManager) GetBuffer(streamID string) (*FCCBuffer, bool) {
	f.Mu.RLock()
	defer f.Mu.RUnlock()

	select {
	case <-f.Closed:
		return nil, false
	default:
	}

	buffer, exists := f.Buffers[streamID]
	return buffer, exists
}

// RemoveBuffer removes an FCC buffer for a stream
func (f *FCCManager) RemoveBuffer(streamID string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	select {
	case <-f.Closed:
		return
	default:
	}

	if buffer, exists := f.Buffers[streamID]; exists {
		buffer.Close()
		delete(f.Buffers, streamID)
	}
}

// cleanupExpiredBuffers periodically cleans up expired buffers
func (f *FCCManager) cleanupExpiredBuffers() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			f.cleanupExpired()
		case <-f.Closed:
			return
		}
	}
}

// cleanupExpired cleans up expired buffers (not accessed for more than 5 minutes)
func (f *FCCManager) cleanupExpired() {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	now := time.Now()
	for streamID, buffer := range f.Buffers {
		// 如果缓冲区为空或者长时间未更新，则清理
		if buffer.Size == 0 || (buffer.Size > 0 && now.Sub(buffer.Buffer[buffer.Size-1].Timestamp) > 5*time.Minute) {
			buffer.Close()
			delete(f.Buffers, streamID)
		}
	}
}

// Close shuts down the FCC manager
func (f *FCCManager) Close() {
	f.Mu.Lock()
	defer f.Mu.Unlock()

	select {
	case <-f.Closed:
		return
	default:
		close(f.Closed)
	}

	// 关闭所有缓冲区
	for _, buffer := range f.Buffers {
		buffer.Close()
	}
	f.Buffers = nil
}

// FCCStreamHub extends StreamHub with FCC capabilities
type FCCStreamHub struct {
	*StreamHub
	FCCManager *FCCManager
	StreamID   string
}

// NewFCCStreamHub creates a new StreamHub with FCC capabilities
func NewFCCStreamHub(streamID string, addrs []string, ifaces []string) (*FCCStreamHub, error) {
	streamHub, err := NewStreamHub(addrs, ifaces)
	if err != nil {
		return nil, err
	}

	fccHub := &FCCStreamHub{
		StreamHub:  streamHub,
		FCCManager: NewFCCManager(),
		StreamID:   streamID,
	}

	return fccHub, nil
}

const FCCBufferDuration = time.Millisecond * 600

func calcFCCBufferSize(bitrate int) int {
	// bitrate: bits per second
	if bitrate <= 0 {
		return 500 // fallback
	}
	packetsPerSec := bitrate / 8 / 1316
	size := packetsPerSec * int(FCCBufferDuration/time.Second)
	if size < 200 {
		size = 200
	}
	if size > 2000 {
		size = 2000
	}
	return size
}

// ProcessWithFCC processes incoming data with FCC support
func (f *FCCStreamHub) ProcessWithFCC(data []byte, sequence uint16) []byte {
	// 首先处理RTP包
	processedData := f.processRTPPacket(data)

	// 如果处理后的数据有效，则添加到FCC缓冲区
	if processedData != nil && len(processedData) > 0 {
		// 获取或创建此流的FCC缓冲区 (默认缓冲区大小为500个包)
		bufferSize := calcFCCBufferSize(0)
		buffer := f.FCCManager.GetOrCreateBuffer(f.StreamID, bufferSize)
		if buffer != nil {
			buffer.AddPacket(processedData, sequence)
		}
	}

	return processedData
}

// GetFCCData retrieves FCC data for fast channel change
func (f *FCCStreamHub) GetFCCData(since time.Time) []*FCCPacket {
	buffer, exists := f.FCCManager.GetBuffer(f.StreamID)
	if !exists || buffer == nil {
		return nil
	}

	return buffer.GetPacketsSince(since)
}

// GetLatestFCCData retrieves the latest FCC data
func (f *FCCStreamHub) GetLatestFCCData(count int) []*FCCPacket {
	buffer, exists := f.FCCManager.GetBuffer(f.StreamID)
	if !exists || buffer == nil {
		return nil
	}

	return buffer.GetLatestPackets(count)
}

// readLoopWithFCC overrides the readLoop to add FCC processing
func (f *FCCStreamHub) readLoopWithFCC(conn *net.UDPConn, hubAddr string) {
	if conn == nil {
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", hubAddr)
	dstIP := udpAddr.IP.String()
	pconn := ipv4.NewPacketConn(conn)
	_ = pconn.SetControlMessage(ipv4.FlagDst, true)

	var sequenceCounter uint16

	for {
		select {
		case <-f.Closed:
			return
		default:
		}

		buf := f.BufPool.Get().([]byte)
		n, cm, _, err := pconn.ReadFrom(buf)
		if err != nil {
			f.BufPool.Put(buf)
			if !errors.Is(err, net.ErrClosed) {
			}
			return
		}

		if cm != nil && cm.Dst.String() != dstIP {
			f.BufPool.Put(buf)
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		f.BufPool.Put(buf)

		f.Mu.RLock()
		closed := f.state == StateStoppeds || f.CacheBuffer == nil
		f.Mu.RUnlock()
		if closed {
			return
		}

		// 增加序列号
		sequenceCounter++

		// 使用FCC处理数据
		processedData := f.ProcessWithFCC(data, sequenceCounter)

		// 广播处理后的数据
		f.broadcast(processedData)
	}
}

// StartFCCReadLoops starts read loops with FCC support
func (f *FCCStreamHub) StartFCCReadLoops() {
	for idx, conn := range f.UdpConns {
		hubAddr := f.AddrList[idx%len(f.AddrList)]
		go f.readLoopWithFCC(conn, hubAddr)
	}
}
