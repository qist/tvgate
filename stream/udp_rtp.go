package stream

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/ipv4"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/monitor"
)

const (
	RTP_VERSION = 2
	P_MPGA      = 14
	P_MPGV      = 32
	NULL_PID    = 0x1FFF
	PAT_PID     = 0x0000
	PMT_PID     = 0x1000

	StateStoppeds = iota
	StatePlayings
	StateErrors
)

var (
	patBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 188)
		},
	}

	pmtBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 188)
		},
	}
)

type rtpSeqEntry struct {
	sequences  []uint16
	lastActive time.Time
}

const (
	rtpSequenceWindow = 200
	rtpSSRCExpire     = 30 * time.Second // 超过30秒未收到包就清理
)

// ====================
// RingBuffer 环形缓冲区
// ====================
type RingBuffer struct {
	buf   [][]byte
	size  int
	start int
	count int
	lock  sync.Mutex
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([][]byte, size),
		size: size,
	}
}

func (r *RingBuffer) Push(item []byte) {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.count < r.size {
		r.buf[(r.start+r.count)%r.size] = item
		r.count++
	} else {
		r.buf[r.start] = item
		r.start = (r.start + 1) % r.size
	}
}

func (r *RingBuffer) GetAll() [][]byte {
	r.lock.Lock()
	defer r.lock.Unlock()

	if r.count == 0 {
		return nil
	}

	result := make([][]byte, r.count)
	for i := 0; i < r.count; i++ {
		result[i] = r.buf[(r.start+i)%r.size]
	}
	return result
}

// Reset clears the ring buffer
func (r *RingBuffer) Reset() {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.start = 0
	r.count = 0
	// 不重新分配内存，而是重置现有缓冲区
	for i := range r.buf {
		r.buf[i] = nil
	}
}

// GetCount 返回当前缓冲区中的元素数量
func (r *RingBuffer) GetCount() int {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.count
}

// 优化版Push，支持预分配和重用
func (r *RingBuffer) PushWithReuse(item []byte) {
	r.lock.Lock()
	defer r.lock.Unlock()

	if r.count < r.size {
		r.buf[(r.start+r.count)%r.size] = item
		r.count++
	} else {
		// 重用已有位置的缓冲区
		r.buf[r.start] = item
		r.start = (r.start + 1) % r.size
	}
}

// ====================
// StreamHub 流处理中心
// ====================
type hubClient struct {
	ch                  chan []byte
	connID              string
	dropCount           uint64       // 客户端丢包计数
	initialData         [][]byte     // 客户端初始数据缓存，用于FCC快速启动
	fccConn             *net.UDPConn // 每个客户端独立的FCC连接
	fccState            int          // 客户端特定的FCC状态
	fccSession          *FccSession  // FCC会话，用于缓存管理
	fccTimeoutTimer     *time.Timer  // 客户端独立的FCC超时定时器
	fccUnicastStartTime time.Time    // 单播开始时间，用于超时检测
	fccStartSeq         uint16       // FCC起始序列号
	fccTermSeq          uint16       // FCC终止序列号
	fccTermSent         bool         // FCC终止包是否已发送
}

// StreamHub represents a multicast/unicast streaming hub
type StreamHub struct {
	Mu             sync.RWMutex
	Clients        map[string]hubClient
	AddCh          chan hubClient
	RemoveCh       chan string
	UdpConns       []*net.UDPConn
	CacheBuffer    *RingBuffer
	Closed         chan struct{}
	BufPool        *sync.Pool
	AddrList       []string
	state          int
	stateCond      *sync.Cond
	lastCCMap      map[int]byte
	rtpSequenceMap map[uint32]*rtpSeqEntry
	ifaces         []string

	// 多播重新加入相关
	rejoinInterval time.Duration
	rejoinTimer    *time.Timer

	// FCC (Fast Channel Change) 相关
	fccEnabled             bool
	fccType                int
	fccCacheSize           int
	fccPortMin, fccPortMax int
	fccState               int
	fccServerAddr          *net.UDPAddr
	fccClientAddr          *net.UDPAddr
	fccConn                *net.UDPConn // FCC连接
	fccUnicastConn         *net.UDPConn
	fccUnicastStartTime    time.Time
	fccStartSeq            uint16
	fccTermSeq             uint16
	fccTermSent            bool
	fccPendingListHead     *BufferRef
	fccPendingListTail     *BufferRef
	fccPendingCount        int32
	fccUnicastBufPool      *sync.Pool
	fccSyncTimer           *time.Timer
	fccTimeoutTimer        *time.Timer
	fccUnicastTimer        *time.Timer
	fccUnicastPort         int
	clientStateChan        chan int
	patBuffer              []byte
	pmtBuffer              []byte
	lastFccDataTime        int64
	rtpBuffer              []byte
	LastFrame              []byte
	OnEmpty                func(*StreamHub)
	multicastAddr          *net.UDPAddr       // 多播地址
	fccResponseCancel      context.CancelFunc // 用于取消FCC响应监听器
	ctx                    context.Context    // StreamHub的上下文
}

// 定义客户端状态常量
const (
	CLIENT_STATE_FCC_INIT = iota
	CLIENT_STATE_FCC_REQUESTED
	CLIENT_STATE_FCC_UNICAST_PENDING
	CLIENT_STATE_FCC_UNICAST_ACTIVE
	CLIENT_STATE_FCC_MCAST_REQUESTED
	CLIENT_STATE_FCC_MCAST_ACTIVE
	CLIENT_STATE_ERROR
)

// ====================
// 创建新 Hub
// ====================
func NewStreamHub(addrs []string, ifaces []string) (*StreamHub, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("至少一个 UDP 地址")
	}

	// 获取FCC配置
	config.CfgMu.RLock()
	fccTypeStr := config.Cfg.Server.FccType
	fccCacheSize := config.Cfg.Server.FccCacheSize
	fccPortMin := config.Cfg.Server.FccListenPortMin
	fccPortMax := config.Cfg.Server.FccListenPortMax

	// 设置默认值
	if fccCacheSize <= 0 {
		fccCacheSize = 16384
	}
	if fccPortMin == 0 {
		fccPortMin = 50000 // 默认端口范围
	}
	if fccPortMax == 0 {
		fccPortMax = 60000
	}

	// 确定FCC类型
	fccType := FCC_TYPE_TELECOM // 默认为电信类型
	switch fccTypeStr {
	case "huawei":
		fccType = FCC_TYPE_HUAWEI
	case "telecom":
		fccType = FCC_TYPE_TELECOM
	}
	config.CfgMu.RUnlock()

	hub := &StreamHub{
		Clients:        make(map[string]hubClient),
		AddCh:          make(chan hubClient, 1024),
		RemoveCh:       make(chan string, 1024),
		UdpConns:       make([]*net.UDPConn, 0, len(addrs)),
		CacheBuffer:    NewRingBuffer(8192), // 默认缓存8192帧
		Closed:         make(chan struct{}),
		BufPool:        &sync.Pool{New: func() any { return make([]byte, 64*1024) }},
		AddrList:       addrs,
		state:          StatePlayings,
		lastCCMap:      make(map[int]byte),
		rtpSequenceMap: make(map[uint32]*rtpSeqEntry),
		ifaces:         ifaces,

		// FCC相关初始化 - 但默认不启用FCC
		fccEnabled:        false, // 只有在收到fcc参数时才启用
		fccType:           fccType,
		fccCacheSize:      fccCacheSize,
		fccPortMin:        fccPortMin,
		fccPortMax:        fccPortMax,
		fccState:          FCC_STATE_INIT,
		fccUnicastBufPool: &sync.Pool{New: func() any { return make([]byte, 64*1024) }},
		lastFccDataTime:   time.Now().UnixNano() / 1e6, // 初始化为当前时间
		ctx:               context.Background(),        // 初始化上下文
	}

	// 设置多播地址，用于构建FCC包
	if len(addrs) > 0 {
		addr, err := net.ResolveUDPAddr("udp", addrs[0])
		if err == nil {
			hub.multicastAddr = addr
		}
	}

	hub.stateCond = sync.NewCond(&hub.Mu)

	// 获取多播重新加入间隔配置
	config.CfgMu.RLock()
	hub.rejoinInterval = config.Cfg.Server.McastRejoinInterval
	config.CfgMu.RUnlock()

	var lastErr error
	for _, addr := range addrs {
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			lastErr = err
			continue
		}

		if len(ifaces) == 0 {
			conn, err := listenMulticast(udpAddr, nil)
			if err != nil {
				lastErr = err
				continue
			}
			hub.UdpConns = append(hub.UdpConns, conn)
		} else {
			for _, name := range ifaces {
				iface, ierr := net.InterfaceByName(name)
				if ierr != nil {
					lastErr = ierr
					continue
				}
				conn, err := listenMulticast(udpAddr, []*net.Interface{iface})
				if err == nil {
					hub.UdpConns = append(hub.UdpConns, conn)
					break
				}
				lastErr = err
			}
		}
	}

	if len(hub.UdpConns) == 0 {
		return nil, fmt.Errorf("所有网卡监听失败: %v", lastErr)
	}

	// 如果配置了重新加入间隔并且大于0，则启动定时器
	if hub.rejoinInterval > 0 {
		hub.rejoinTimer = time.AfterFunc(hub.rejoinInterval, func() {
			hub.rejoinMulticastGroups(addrs)
		})
	}

	// 注意：不再初始化 fccPendingBuf，统一使用 fccPendingListHead/Tail 链表

	go hub.run()
	hub.startReadLoops()
	return hub, nil
}

// ====================
// 多播监听封装
// ====================
func listenMulticast(addr *net.UDPAddr, ifaces []*net.Interface) (*net.UDPConn, error) {
	if addr == nil || addr.IP == nil || !isMulticast(addr.IP) {
		return nil, fmt.Errorf("仅支持多播地址: %v", addr)
	}

	var conn *net.UDPConn
	var lastErr error
	var err error

	if len(ifaces) == 0 {
		conn, err = net.ListenMulticastUDP("udp", nil, addr)
		if err != nil {
			logger.LogPrintf("⚠️ 多播监听失败，尝试回退单播: %v", err)
			conn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return nil, fmt.Errorf("默认接口监听失败: %w", err)
			}
			logger.LogPrintf("🟡 已回退为单播 UDP 监听 %v", addr)
		} else {
			logger.LogPrintf("🟢 监听 %v (全部接口)", addr)
		}
	} else {
		for _, iface := range ifaces {
			if iface == nil {
				continue
			}
			conn, err = net.ListenMulticastUDP("udp", iface, addr)
			if err == nil {
				logger.LogPrintf("🟢 监听 %v@%s 成功", addr, iface.Name)
				break
			}
			lastErr = err
			logger.LogPrintf("⚠️ 监听 %v@%s 失败: %v", addr, iface.Name, err)
		}

		if conn == nil {
			conn, err = net.ListenUDP("udp", addr)
			if err != nil {
				return nil, fmt.Errorf("所有网卡监听失败且单播监听失败: %v (last=%v)", err, lastErr)
			}
			logger.LogPrintf("🟡 所有网卡多播失败，已回退为单播 UDP 监听 %v", addr)
		}
	}
	_ = conn.SetReadBuffer(16 * 1024 * 1024)

	return conn, nil
}

func isMulticast(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] >= 224 && ip4[0] <= 239
}

// ====================
// 启动 startReadLoops
// ====================
func (h *StreamHub) startReadLoops() {
	// 清理之前的读循环（如果有的话）
	// 由于UDP读循环在连接关闭时会自行退出，这里不需要特殊处理

	// 为每个连接启动一个新的读循环
	for idx, conn := range h.UdpConns {
		hubAddr := h.AddrList[idx%len(h.AddrList)]
		go h.readLoop(conn, hubAddr)
	}
}

// ====================
// 启动 UDPConn readLoop
// ====================
func (h *StreamHub) readLoop(conn *net.UDPConn, hubAddr string) {
	if conn == nil {
		return
	}

	udpAddr, _ := net.ResolveUDPAddr("udp", hubAddr)
	dstIP := udpAddr.IP.String()
	pconn := ipv4.NewPacketConn(conn)
	_ = pconn.SetControlMessage(ipv4.FlagDst, true)

	for {
		select {
		case <-h.Closed:
			return
		default:
		}

		buf := h.BufPool.Get().([]byte)
		n, cm, _, err := pconn.ReadFrom(buf)
		if err != nil {
			h.BufPool.Put(buf)
			if !errors.Is(err, net.ErrClosed) {
				logger.LogPrintf("❌ UDP 读取错误: %v", err)
			}
			return
		}

		if cm != nil && cm.Dst.String() != dstIP {
			h.BufPool.Put(buf)
			continue
		}

		inRef := NewPooledBufferRef(buf, buf[:n], h.BufPool)

		h.Mu.RLock()
		closed := h.state == StateStoppeds || h.CacheBuffer == nil
		h.Mu.RUnlock()
		if closed {
			return
		}

		// 处理RTP包（零拷贝引用）
		outRef := h.processRTPPacketRef(inRef)
		if outRef == nil {
			inRef.Put()
			continue
		}
		if outRef != inRef {
			inRef.Put()
		}
		// 广播后归还缓冲
		h.broadcastRef(outRef)
	}
}
// ====================
// RTP处理相关函数
// ====================

// 处理RTP包，返回零拷贝引用
func (h *StreamHub) processRTPPacketRef(inRef *BufferRef) *BufferRef {
	data := inRef.data
	if h.processFCCPacket(data) {
		return nil
	}
	if len(data) >= 188 && data[0] == 0x47 {
		return inRef
	}
	if len(data) < 12 {
		return inRef
	}
	version := (data[0] >> 6) & 0x03
	if version != RTP_VERSION {
		return inRef
	}
	sequence := binary.BigEndian.Uint16(data[2:4])
	ssrc := binary.BigEndian.Uint32(data[8:12])
	h.Mu.Lock()
	if h.rtpSequenceMap == nil {
		h.rtpSequenceMap = make(map[uint32]*rtpSeqEntry)
	}
	entry, ok := h.rtpSequenceMap[ssrc]
	if !ok {
		entry = &rtpSeqEntry{}
		h.rtpSequenceMap[ssrc] = entry
	}
	duplicate := false
	for _, seq := range entry.sequences {
		if seq == sequence {
			duplicate = true
			break
		}
	}
	if duplicate {
		h.Mu.Unlock()
		return nil
	}
	entry.sequences = append(entry.sequences, sequence)
	if len(entry.sequences) > rtpSequenceWindow {
		entry.sequences = entry.sequences[len(entry.sequences)-rtpSequenceWindow:]
	}
	entry.lastActive = time.Now()
	h.cleanupOldSSRCs()
	h.Mu.Unlock()
	startOff, endOff, err := rtpPayloadGet(data)
	if err != nil || startOff >= len(data)-endOff {
		return inRef
	}
	payloadType := data[1] & 0x7F
	if payloadType == P_MPGA || payloadType == P_MPGV {
		if startOff+4 < len(data)-endOff {
			startOff += 4
		}
	}
	payload := data[startOff : len(data)-endOff]
	if len(payload) < 188 || payload[0] != 0x47 || len(payload)%188 != 0 {
		return inRef
	}
	h.Mu.Lock()
	h.rtpBuffer = append(h.rtpBuffer, payload...)
	if len(h.rtpBuffer) < 188 {
		h.Mu.Unlock()
		return nil
	}
	if h.rtpBuffer[0] != 0x47 {
		idx := bytes.IndexByte(h.rtpBuffer, 0x47)
		if idx < 0 {
			h.rtpBuffer = h.rtpBuffer[:0]
			h.Mu.Unlock()
			return nil
		}
		copy(h.rtpBuffer, h.rtpBuffer[idx:])
		h.rtpBuffer = h.rtpBuffer[:len(h.rtpBuffer)-idx]
		if len(h.rtpBuffer) < 188 {
			h.Mu.Unlock()
			return nil
		}
	}
	alignedSize := (len(h.rtpBuffer) / 188) * 188
	chunk := h.rtpBuffer[:alignedSize]
	if alignedSize < len(h.rtpBuffer) {
		copy(h.rtpBuffer, h.rtpBuffer[alignedSize:])
		h.rtpBuffer = h.rtpBuffer[:len(h.rtpBuffer)-alignedSize]
	} else {
		h.rtpBuffer = h.rtpBuffer[:0]
	}
	h.Mu.Unlock()
	poolBuf := h.BufPool.Get().([]byte)
	out := poolBuf[:0]
	h.Mu.RLock()
	fccEnabled := h.fccEnabled
	currentFccState := h.fccState
	h.Mu.RUnlock()
	for i := 0; i < len(chunk); i += 188 {
		ts := chunk[i : i+188]
		if ts[0] != 0x47 {
			continue
		}
		pid := ((int(ts[1]) & 0x1F) << 8) | int(ts[2])
		tsCC := ts[3] & 0x0F
		if fccEnabled {
			if pid == PAT_PID && (ts[1]&0x40) != 0 {
				h.Mu.Lock()
				if h.patBuffer == nil {
					h.patBuffer = patBufferPool.Get().([]byte)
				}
				copy(h.patBuffer, ts)
				h.Mu.Unlock()
			}
			if pid == PMT_PID && (ts[1]&0x40) != 0 {
				h.Mu.Lock()
				if h.pmtBuffer == nil {
					h.pmtBuffer = pmtBufferPool.Get().([]byte)
				}
				copy(h.pmtBuffer, ts)
				h.Mu.Unlock()
			}
		}
		if pid != NULL_PID {
			h.Mu.Lock()
			if last, ok := h.lastCCMap[pid]; ok {
				diff := (int(tsCC) - int(last) + 16) & 0x0F
				if diff > 1 {
					for j := 1; j < diff; j++ {
						out = append(out, makeNullTS()...)
					}
				}
			}
			h.lastCCMap[pid] = tsCC
			h.Mu.Unlock()
		}
		out = append(out, ts...)
	}
	outRef := NewPooledBufferRef(poolBuf, out, h.BufPool)
	if fccEnabled && currentFccState != FCC_STATE_MCAST_ACTIVE && len(out) > 0 {
		outRef.Get()
		h.Mu.Lock()
		if h.fccPendingListHead == nil {
			h.fccPendingListHead = outRef
			h.fccPendingListTail = outRef
		} else {
			h.fccPendingListTail.next = outRef
			h.fccPendingListTail = outRef
		}
		h.Mu.Unlock()
		atomic.AddInt32(&h.fccPendingCount, 1)
		// 基于序列号的切换逻辑，不再调用checkAndSwitchToMulticast
		// 因为切换现在由processFCCMediaBufRef中的序列号检查自动处理
	}
	return outRef
}

// hexdumpPreview 返回前 n 个字节的十六进制预览
// func hexdumpPreview(buf []byte, n int) string {
// 	if len(buf) > n {
// 		buf = buf[:n]
// 	}
// 	return hex.EncodeToString(buf)
// }

func (h *StreamHub) cleanupOldSSRCs() {
	now := time.Now()
	for ssrc, entry := range h.rtpSequenceMap {
		if now.Sub(entry.lastActive) > rtpSSRCExpire {
			delete(h.rtpSequenceMap, ssrc)
		}
	}
}

// rtpPayloadGet 从RTP包中提取有效载荷位置和大小
func rtpPayloadGet(buf []byte) (startOff, endOff int, err error) {
	if len(buf) < 12 {
		return 0, 0, errors.New("buffer too small")
	}

	// RTP版本检查
	version := (buf[0] >> 6) & 0x03
	if version != RTP_VERSION {
		return 0, 0, fmt.Errorf("invalid RTP version=%d", version)
	}

	// 计算头部大小
	cc := buf[0] & 0x0F
	startOff = 12 + (4 * int(cc))

	// 检查扩展头
	x := (buf[0] >> 4) & 0x01
	if x == 1 { // 扩展头存在
		if startOff+4 > len(buf) {
			return 0, 0, errors.New("buffer too small for extension header")
		}
		extLen := int(binary.BigEndian.Uint16(buf[startOff+2 : startOff+4]))
		startOff += 4 + (4 * extLen)
	}

	// 检查填充
	p := (buf[0] >> 5) & 0x01
	if p == 1 { // 填充存在
		if len(buf) > 0 {
			endOff = int(buf[len(buf)-1])
		}
	}

	if startOff+endOff > len(buf) {
		return 0, 0, errors.New("invalid RTP packet structure")
	}

	// 保留兜底逻辑（不打印日志）
	payloadLen := len(buf) - startOff - endOff
	if payloadLen > 0 {
		if buf[startOff] != 0x47 || payloadLen%188 != 0 {
			// 只是检查，不做打印
		}
	}

	return startOff, endOff, nil
}

// 添加一个简单的内存池实现
type BufferPool struct {
	pool sync.Pool
}

func NewBufferPool() *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				// 预分配188字节的TS包缓冲区
				return make([]byte, 188)
			},
		},
	}
}

func (bp *BufferPool) Get() []byte {
	return bp.pool.Get().([]byte)
}

func (bp *BufferPool) Put(buf []byte) {
	bp.pool.Put(buf)
}

// 全局内存池实例
var tsBufferPool = &sync.Pool{
	New: func() interface{} {
		return make([]byte, 188)
	},
}

// 修改makeNullTS函数以使用内存池
func makeNullTS() []byte {
	ts := tsBufferPool.Get().([]byte)
	ts[0] = 0x47
	ts[1] = 0x1F
	ts[2] = 0xFF
	ts[3] = 0x10
	for i := 4; i < 188; i++ {
		ts[i] = 0xFF
	}
	return ts
}

// ====================
// 广播到所有客户端
// ====================

// 零拷贝引用广播，发送完成后归还池
func (h *StreamHub) broadcastRef(bufRef *BufferRef) {
	// 检查是否是FCC多播过渡阶段
	if h.IsFccEnabled() {
		h.Mu.RLock()
		currentState := h.fccState
		h.Mu.RUnlock()
		
		switch currentState {
		case FCC_STATE_MCAST_REQUESTED:
			h.handleMcastDataDuringTransition(bufRef.data)
			bufRef.Put()
			return
		case FCC_STATE_MCAST_ACTIVE:
			// 在多播活动状态下，先处理可能的缓冲数据
			h.fccHandleMcastActive()
			// fallthrough to normal broadcast
		}
	}
	
	data := bufRef.data
	for _, c := range h.Clients {
		select {
		case c.ch <- data:
		case <-time.After(100 * time.Millisecond):
		}
	}
	bufRef.Put()

	// 将TS包写入频道缓存
	h.Mu.RLock()
	if len(data) >= 188 && data[0] == 0x47 && h.IsFccEnabled() { // TS包标识
		for _, addr := range h.AddrList {
			// 提取频道ID（使用地址作为频道标识）
			channelID := addr
			channel := GlobalChannelManager.GetOrCreate(channelID)
			if channel != nil {
				channel.AddTsPacket(data)
			}
		}
	}
	h.Mu.RUnlock()
}

// ====================
// 客户端管理循环
// ====================
func (h *StreamHub) run() {
	// 启动清理器（只在FCC启用时启动）
	h.Mu.RLock()
	fccEnabled := h.fccEnabled
	h.Mu.RUnlock()

	if fccEnabled {
		GlobalChannelManager.StartCleaner()
	}

	for {
		select {
		case client := <-h.AddCh:
			h.Mu.Lock()
			h.Clients[client.connID] = client
			h.Mu.Unlock()

			logger.LogPrintf("客户端加入: %s, 当前客户端数: %d", client.connID, len(h.Clients))

			// 如果启用了FCC，发送缓存数据给新客户端
			h.Mu.RLock()
			addrList := h.AddrList
			h.Mu.RUnlock()

			if client.fccSession != nil && len(addrList) > 0 && fccEnabled {
				// 从频道缓存获取数据
				channelID := addrList[0] // 使用第一个地址作为频道ID
				channel := GlobalChannelManager.GetOrCreate(channelID)
				packets := channel.ReadForSession(client.fccSession)

				// 发送缓存数据给客户端
				if len(packets) > 0 {
					go func(ch chan []byte, pkts [][]byte) {
						for _, pkt := range pkts {
							select {
							case ch <- pkt:
							case <-time.After(5 * time.Second): // 5秒超时
								logger.LogPrintf("发送缓存数据超时，客户端可能已断开: %s", client.connID)
								return
							}
						}
					}(client.ch, packets)
				}
			}

		case connID := <-h.RemoveCh:
			h.Mu.Lock()
			if client, exists := h.Clients[connID]; exists {
				// 关闭客户端通道
				close(client.ch)

				// 如果有FCC连接，清理它
				if client.fccConn != nil {
					client.fccConn.Close()
				}

				// 停止客户端的FCC超时定时器
				if client.fccTimeoutTimer != nil {
					client.fccTimeoutTimer.Stop()
					client.fccTimeoutTimer = nil
				}

				// 从FCC缓存管理器中移除会话
				if client.fccSession != nil && fccEnabled {
					channelID := h.AddrList[0] // 使用第一个地址作为频道ID
					GlobalChannelManager.GetOrCreate(channelID).RemoveSession(connID)
				}

				delete(h.Clients, connID)
				logger.LogPrintf("客户端移除: %s, 当前客户端数: %d", connID, len(h.Clients))

				// 检查是否还有客户端，如果没有则关闭hub
				if len(h.Clients) == 0 {
					logger.LogPrintf("所有客户端已断开，准备关闭频道: %s", h.AddrList[0])
					h.Mu.Unlock() // 先解锁，避免死锁

					// 清理FCC相关资源
					h.cleanupFCC()

					// 关闭hub
					h.Close()
					if h.OnEmpty != nil {
						h.OnEmpty(h) // 自动删除 hub
					}
					return
				}
			}
			h.Mu.Unlock()

		case <-h.Closed:
			// 清理所有客户端
			h.Mu.Lock()
			for connID, client := range h.Clients {
				close(client.ch)
				if client.fccConn != nil {
					client.fccConn.Close()
				}

				// 停止客户端的FCC超时定时器
				if client.fccTimeoutTimer != nil {
					client.fccTimeoutTimer.Stop()
					client.fccTimeoutTimer = nil
				}

				// 从FCC缓存管理器中移除会话
				if client.fccSession != nil && fccEnabled {
					channelID := h.AddrList[0] // 使用第一个地址作为频道ID
					GlobalChannelManager.GetOrCreate(channelID).RemoveSession(connID)
				}
			}
			h.Clients = make(map[string]hubClient)
			h.Mu.Unlock()

			// 清理FCC相关资源
			h.cleanupFCC()

			return
		}
	}
}

// 非阻塞发送初始化帧
// 任意一次发送失败，直接放弃
func (h *StreamHub) sendPacketsNonBlocking(ch chan []byte, packets [][]byte) {
	for _, p := range packets {

		// hub 已关闭，立即退出
		select {
		case <-h.Closed:
			return
		default:
		}

		// 非阻塞发送
		select {
		case ch <- p:
		default:
			// 客户端太慢，直接放弃初始化
			return
		}
	}
}

// ====================
// HTTP 播放
// ====================
func (h *StreamHub) ServeHTTP(w http.ResponseWriter, r *http.Request, contentType string, updateActive func()) {
	clientIP := monitor.GetClientIP(r)
	connID := clientIP + "_" + r.RemoteAddr

	// 检查是否已经关闭
	h.Mu.RLock()
	closed := h.state == StateStoppeds
	h.Mu.RUnlock()
	if closed {
		http.Error(w, "Hub closed", http.StatusServiceUnavailable)
		return
	}

	// 检查URL参数中是否包含fcc参数，决定是否启用FCC
	fccEnabled := r.URL.Query().Get("fcc") != ""

	// 检查FCC服务器地址参数
	fccServerParam := r.URL.Query().Get("fcc")
	var fccServerAddr *net.UDPAddr
	if fccServerParam != "" {
		addr, err := net.ResolveUDPAddr("udp", fccServerParam)
		if err != nil {
			logger.LogPrintf("FCC服务器地址解析失败: %v", err)
			fccEnabled = false
		} else {
			fccServerAddr = addr
		}
	}

	// 创建响应式通道，缓冲区大小适中
	ch := make(chan []byte, 256)

	// 创建客户端结构体
	client := hubClient{
		ch:              ch,
		connID:          connID,
		initialData:     nil,            // 初始化为空，后续填充
		fccState:        FCC_STATE_INIT, // 初始化FCC状态
		fccConn:         nil,            // 初始化为nil，如果启用FCC会创建
		fccSession:      nil,            // 初始化为nil，如果启用FCC会创建
		fccTimeoutTimer: nil,            // 初始化为nil
		fccStartSeq:     0,
		fccTermSeq:      0,
		fccTermSent:     false,
	}

	// 如果启用了FCC，为客户端创建独立的FCC连接
	if fccEnabled && fccServerAddr != nil {
		// 为这个客户端创建独立的FCC连接
		fccConn, err := h.initFCCConnectionForClient()
		if err != nil {
			logger.LogPrintf("客户端FCC连接初始化失败 %s: %v", connID, err)
			fccEnabled = false
		} else {
			client.fccConn = fccConn

			// 添加到FCC缓存管理器
			channelID := h.AddrList[0] // 使用第一个地址作为频道ID
			client.fccSession = GlobalChannelManager.GetOrCreate(channelID).AddSession(connID)

			// 设置客户端FCC状态为REQUESTED
			client.fccState = FCC_STATE_REQUESTED
			client.fccUnicastStartTime = time.Now()

			// 发送FCC请求给这个客户端的服务器
			go func() {
				h.Mu.RLock()
				multicastAddr, err := net.ResolveUDPAddr("udp", h.AddrList[0]) // 使用第一个地址作为多播地址
				h.Mu.RUnlock()

				if err != nil {
					logger.LogPrintf("多播地址解析失败: %v", err)
					return
				}

				err = h.sendFCCRequestWithConn(fccConn, multicastAddr, fccConn.LocalAddr().(*net.UDPAddr).Port, fccServerAddr)
				if err != nil {
					logger.LogPrintf("发送FCC请求失败: %v", err)
					return
				}

				// 启动客户端独立的FCC超时定时器
				client.startClientFCCTimeoutTimer(fccConn, fccServerAddr, multicastAddr)
			}()
		}
	}

	// 添加客户端到hub
	h.AddCh <- client

	// 设置响应头
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", contentType)

	// 等待Hub进入播放状态，使用请求上下文，这样当客户端断开时可以及时返回
	if !h.WaitForPlaying(r.Context()) {
		logger.LogPrintf("等待Hub播放状态超时或失败: %s", connID)
		http.Error(w, "Service timeout", http.StatusServiceUnavailable)
		// 从hub中移除客户端
		h.RemoveCh <- connID
		return
	}

	// HTTP流处理 - 检查是否支持flush
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		// 从hub中移除客户端
		h.RemoveCh <- connID
		return
	}

	// 创建响应写入器
	pr, pw := io.Pipe()

	// 启动goroutine将通道数据写入响应
	go func() {
		defer pw.Close()

		// 发送初始数据
		h.sendInitialToClient(client)

		// 使用for range遍历通道，更简洁高效
		for data := range ch {
			// 写入数据到响应
			if _, err := pw.Write(data); err != nil {
				// 客户端可能已断开连接，记录日志但不panic
				// logger.LogPrintf("写入pipe失败: %v", err)
				return
			}

			// 更新活跃状态
			if updateActive != nil {
				updateActive()
			}
		}
	}()

	// 将pipe的内容复制到响应，并实时刷新
	done := make(chan struct{})
	go func() {
		defer close(done)

		// 使用io.Copy将数据从pipe复制到响应writer
		_, err := io.Copy(w, pr)
		if err != nil {
			// 客户端可能已断开连接，记录日志但不panic
			logger.LogPrintf("响应写入错误: %v", err)
		}
	}()

	// 定期更新活跃状态
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				// 复制完成，退出
				return
			case <-ticker.C:
				// 定期更新活跃状态
				if updateActive != nil {
					updateActive()
				}
			case <-r.Context().Done():
				// 请求结束，退出
				return
			}
		}
	}()

	// 等待响应完成或请求结束
	select {
	case <-done:
		// 复制完成
	case <-r.Context().Done():
		// 请求结束，关闭pipe writer以停止数据写入
		pw.Close()
	}

	// 从hub中移除客户端
	h.RemoveCh <- connID
}

// ====================
// 关闭 Hub
// ====================
func (h *StreamHub) Close() {
	// 先标记为关闭状态，防止新的操作进入
	select {
	case <-h.Closed:
		return // 已经关闭过
	default:
		close(h.Closed)
	}

	h.Mu.Lock()
	// 提前保存需要的信息，然后尽快释放锁
	fccEnabled := h.fccEnabled
	addrList := make([]string, len(h.AddrList))
	copy(addrList, h.AddrList)

	// 停止重新加入定时器（如果存在）
	if h.rejoinTimer != nil {
		h.rejoinTimer.Stop()
		h.rejoinTimer = nil
	}

	// 暂存UDP连接用于稍后关闭
	udpConns := h.UdpConns
	h.UdpConns = nil

	// 暂存客户端连接用于稍后关闭
	clients := h.Clients
	h.Clients = nil

	// 清理各种缓冲区
	if h.CacheBuffer != nil {
		h.CacheBuffer.Reset()
		h.CacheBuffer = nil
	}
	h.LastFrame = nil
	h.rtpBuffer = nil

	// 清理FCC链表缓冲区
	for h.fccPendingListHead != nil {
		bufRef := h.fccPendingListHead
		h.fccPendingListHead = bufRef.next
		bufRef.Put() // 减少引用计数，允许内存回收
	}
	h.fccPendingListTail = nil
	atomic.StoreInt32(&h.fccPendingCount, 0)

	// 清理PAT/PMT缓冲区
	if h.patBuffer != nil {
		patBufferPool.Put(h.patBuffer)
		h.patBuffer = nil
	}
	if h.pmtBuffer != nil {
		pmtBufferPool.Put(h.pmtBuffer)
		h.pmtBuffer = nil
	}

	// 状态更新
	h.state = StateStoppeds
	stateCond := h.stateCond

	h.Mu.Unlock() // 尽快释放主锁

	// 在锁外关闭UDP连接
	for _, conn := range udpConns {
		if conn != nil {
			_ = conn.Close()
		}
	}

	// 在锁外关闭所有客户端channel
	for _, client := range clients {
		if client.ch != nil {
			close(client.ch)
		}
	}

	// 最后发送FCC终止包，在锁外进行
	if fccEnabled {
		seqNum := uint16(0)
		for _, addr := range addrList {
			udpAddr, err := net.ResolveUDPAddr("udp", addr)
			if err != nil {
				continue
			}

			// 使用goroutine避免阻塞
			go func(ua *net.UDPAddr) {
				err := h.sendFCCTermination(ua, seqNum)
				if err != nil {
					logger.LogPrintf("FCC终止包发送失败: %v", err)
				} else {
					logger.LogPrintf("FCC终止包已发送到 %s", ua.String())
				}
			}(udpAddr)
		}
	}

	// 广播状态变更（在所有资源清理后）
	if stateCond != nil {
		stateCond.Broadcast()
	}

	logger.LogPrintf("UDP监听已关闭，端口已释放: %s", addrList[0])
}

// rejoinMulticastGroups 重新加入多播组
func (h *StreamHub) rejoinMulticastGroups(addrs []string) {

	// 直接调用 smoothRejoinMulticast 方法来平滑刷新组播成员关系
	h.smoothRejoinMulticast()

	// 重新安排下一次重新加入（如果是周期性的）
	h.ResetRejoinTimer()
}

// ====================
// 判断 Hub 是否关闭
// ====================
func (h *StreamHub) IsClosed() bool {
	select {
	case <-h.Closed:
		return true
	default:
		return false
	}
}

// ====================
// 等待播放状态
// ====================
func (h *StreamHub) WaitForPlaying(ctx context.Context) bool {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	if h.IsClosed() || h.state == StateErrors {
		return false
	}
	if h.state == StatePlayings {
		return true
	}

	for h.state == StateStoppeds && !h.IsClosed() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.stateCond.Wait()
		}()
		select {
		case <-done:
			if h.state == StateErrors {
				return false
			}
			if h.state == StatePlayings {
				return true
			}
		case <-ctx.Done():
			return false
		}
	}
	return !h.IsClosed() && h.state == StatePlayings
}

// ====================
// MultiChannelHub
// ====================
type MultiChannelHub struct {
	Mu   sync.RWMutex
	Hubs map[string]*StreamHub
}

var GlobalMultiChannelHub = NewMultiChannelHub()

func NewMultiChannelHub() *MultiChannelHub {
	return &MultiChannelHub{
		Hubs: make(map[string]*StreamHub),
	}
}

// MD5(IP:Port@ifaces) 作为 Hub key
func (m *MultiChannelHub) HubKey(udpAddr string, ifaces []string) string {
	// 将UDP地址和接口列表组合成唯一的键
	keyStr := udpAddr
	if len(ifaces) > 0 {
		keyStr += "@" + strings.Join(ifaces, ",")
	}
	h := md5.Sum([]byte(keyStr))
	return hex.EncodeToString(h[:])
}

func (m *MultiChannelHub) GetOrCreateHub(udpAddr string, ifaces []string) (*StreamHub, error) {
	key := m.HubKey(udpAddr, ifaces)

	m.Mu.RLock()
	hub, exists := m.Hubs[key]
	m.Mu.RUnlock()

	if exists && !hub.IsClosed() {
		return hub, nil
	}

	newHub, err := NewStreamHub([]string{udpAddr}, ifaces)
	if err != nil {
		return nil, err
	}

	// 当客户端为0时自动删除 hub
	newHub.OnEmpty = func(h *StreamHub) {
		GlobalMultiChannelHub.RemoveHubEx(h.AddrList[0], ifaces)
	}

	m.Mu.Lock()
	m.Hubs[key] = newHub
	m.Mu.Unlock()
	return newHub, nil
}

func (m *MultiChannelHub) RemoveHub(udpAddr string) {
	m.RemoveHubEx(udpAddr, nil)
}

func (m *MultiChannelHub) RemoveHubEx(udpAddr string, ifaces []string) {
	key := m.HubKey(udpAddr, ifaces)

	m.Mu.Lock()
	hub, ok := m.Hubs[key]
	if !ok {
		m.Mu.Unlock()
		return
	}

	// 先从 map 删除，避免 Close 时有 goroutine 再访问
	delete(m.Hubs, key)
	m.Mu.Unlock()

	// 安全关闭 hub
	hub.Close()
}

// ====================
// 更新 Hub 的接口
// ====================
func (h *StreamHub) UpdateInterfaces(ifaces []string) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	var newConns []*net.UDPConn
	var lastErr error

	for _, addr := range h.AddrList {
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			lastErr = err
			continue
		}

		var conn *net.UDPConn
		for _, name := range ifaces {
			iface, ierr := net.InterfaceByName(name)
			if ierr != nil {
				lastErr = ierr
				continue
			}
			conn, err = listenMulticast(udpAddr, []*net.Interface{iface})
			if err == nil {
				newConns = append(newConns, conn)
				break
			}
			lastErr = err
		}

		// 最后尝试默认接口
		if conn == nil {
			conn, err = listenMulticast(udpAddr, nil)
			if err != nil {
				lastErr = err
				continue
			}
			newConns = append(newConns, conn)
		}
	}

	if len(newConns) == 0 {
		return fmt.Errorf("所有网卡更新失败: %v", lastErr)
	}

	// 替换 UDPConns
	for _, conn := range h.UdpConns {
		_ = conn.Close()
	}
	h.UdpConns = newConns

	// 重新启动 readLoops
	h.startReadLoops()

	logger.LogPrintf("✅ Hub UDPConn 已更新 (仅接口)，网卡=%v", ifaces)

	return nil
}

// ====================
// 客户端迁移到新 Hub
// ====================
func (h *StreamHub) TransferClientsTo(newHub *StreamHub) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	newHub.Mu.Lock()
	defer newHub.Mu.Unlock()

	if newHub.Clients == nil {
		newHub.Clients = make(map[string]hubClient)
	}
	if newHub.CacheBuffer == nil {
		newHub.CacheBuffer = NewRingBuffer(h.CacheBuffer.size)
	}

	// 迁移缓存数据
	for _, f := range h.CacheBuffer.GetAll() {
		newHub.CacheBuffer.Push(f)
	}

	// 迁移客户端
	for connID, client := range h.Clients {
		newHub.Clients[connID] = client

		// 发送最后关键帧序列
		for _, frame := range h.CacheBuffer.GetAll() {
			select {
			case client.ch <- frame:
			default:
			}
		}

		// 再发送最后一帧数据，保证客户端能立即播放
		if len(h.LastFrame) > 0 {
			select {
			case client.ch <- h.LastFrame:
			default:
			}
		}
	}

	h.Clients = make(map[string]hubClient)
	logger.LogPrintf("🔄 客户端已迁移到新Hub，数量=%d", len(newHub.Clients))
}

// SetRejoinInterval 设置重新加入间隔
func (h *StreamHub) SetRejoinInterval(interval time.Duration) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	h.rejoinInterval = interval
}

// GetRejoinInterval 获取重新加入间隔
func (h *StreamHub) GetRejoinInterval() time.Duration {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	return h.rejoinInterval
}

// ResetRejoinTimer 重置重新加入定时器
func (h *StreamHub) ResetRejoinTimer() {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	if h.rejoinTimer != nil && h.rejoinInterval > 0 {
		h.rejoinTimer.Reset(h.rejoinInterval)
	}
}

// UpdateRejoinTimer 更新重新加入定时器
func (h *StreamHub) UpdateRejoinTimer() {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 如果定时器存在，先停止它
	if h.rejoinTimer != nil {
		h.rejoinTimer.Stop()
	}

	// 如果间隔大于0，则重新启动定时器
	if h.rejoinInterval > 0 {
		h.rejoinTimer = time.AfterFunc(h.rejoinInterval, func() {
			h.rejoinMulticastGroups(h.AddrList)
		})
	} else {
		h.rejoinTimer = nil
	}
}

func (h *StreamHub) smoothRejoinMulticast() {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// hub 已关闭就不处理
	select {
	case <-h.Closed:
		return
	default:
	}

	logger.LogPrintf("🔄 平滑刷新 IGMP 组播成员关系: %v", h.AddrList)

	for _, conn := range h.UdpConns {
		if conn == nil {
			continue
		}

		p := ipv4.NewPacketConn(conn)

		for _, addr := range h.AddrList {
			udpAddr, err := net.ResolveUDPAddr("udp", addr)
			if err != nil {
				continue
			}

			groupIP := udpAddr.IP
			if !isMulticast(groupIP) {
				continue
			}

			// 1️⃣ Leave（即使失败也没关系）
			if len(h.ifaces) == 0 {
				_ = p.LeaveGroup(nil, &net.UDPAddr{IP: groupIP})
			} else {
				for _, ifname := range h.ifaces {
					iface, err := net.InterfaceByName(ifname)
					if err != nil {
						continue
					}
					_ = p.LeaveGroup(iface, &net.UDPAddr{IP: groupIP})
				}
			}

			// 2️⃣ Join（触发内核发送 IGMP Report）
			if len(h.ifaces) == 0 {
				if err := p.JoinGroup(nil, &net.UDPAddr{IP: groupIP}); err != nil {
					logger.LogPrintf("⚠️ JoinGroup 失败 %v: %v", groupIP, err)
				}
			} else {
				for _, ifname := range h.ifaces {
					iface, err := net.InterfaceByName(ifname)
					if err != nil {
						continue
					}
					if err := p.JoinGroup(iface, &net.UDPAddr{IP: groupIP}); err != nil {
						logger.LogPrintf(
							"⚠️ JoinGroup %v@%s 失败: %v",
							groupIP, iface.Name, err,
						)
					}
				}
			}
		}
	}

	logger.LogPrintf("✅ IGMP 成员关系已刷新（未中断 socket）")
}


// sendInitialToClient 为特定客户端发送初始数据
func (h *StreamHub) sendInitialToClient(client hubClient) {
	h.Mu.Lock()
	fccEnabled := h.fccEnabled
	currentState := h.fccState
	addrList := h.AddrList // 保存地址列表用于频道查找
	h.Mu.Unlock()

	// ---------- 非 FCC 或 FCC 未激活 ----------
	if !fccEnabled ||
		(currentState != FCC_STATE_UNICAST_ACTIVE &&
			currentState != FCC_STATE_MCAST_REQUESTED &&
			currentState != FCC_STATE_MCAST_ACTIVE) {

		// 获取缓存快照
		h.Mu.Lock()
		cachedFrames := h.CacheBuffer.GetAll()
		h.Mu.Unlock()

		// 异步非阻塞发送
		go h.sendPacketsNonBlocking(client.ch, cachedFrames)
		return
	}

	// ---------- FCC 模式 ----------
	h.Mu.Lock()

	var packets [][]byte

	// PAT / PMT 优先
	h.Mu.RLock()
	if h.patBuffer != nil {
		packets = append(packets, h.patBuffer)
	}
	if h.pmtBuffer != nil {
		packets = append(packets, h.pmtBuffer)
	}
	h.Mu.RUnlock()

	// 检查客户端特定的FCC状态
	clientFccState := client.fccState

	switch clientFccState {
	case FCC_STATE_UNICAST_ACTIVE:
		// 单播 FCC：发送最近 FCC 缓存帧
		// 从链表中获取最近 50 帧
		var frames [][]byte
		h.Mu.RLock()
		count := 0
		for n := h.fccPendingListHead; n != nil; n = n.next {
			count++
			frames = append(frames, n.data)
		}
		h.Mu.RUnlock()
		if len(frames) > 0 {
			start := 0
			if len(frames) > 50 {
				start = len(frames) - 50
			}
			packets = append(packets, frames[start:]...)
		} else {
			cachedFrames := h.CacheBuffer.GetAll()
			packets = append(packets, cachedFrames...)
		}

	case FCC_STATE_MCAST_REQUESTED, FCC_STATE_MCAST_ACTIVE:
		// 多播 FCC：完整 FCC 缓存
		fccFramesAvailable := false
		h.Mu.RLock()
		for n := h.fccPendingListHead; n != nil; n = n.next {
			packets = append(packets, n.data)
			fccFramesAvailable = true
		}
		h.Mu.RUnlock()

		// 补充普通缓存（如果没有FCC帧或者需要更多数据）
		if !fccFramesAvailable || len(packets) < 10 {
			cachedFrames := h.CacheBuffer.GetAll()
			packets = append(packets, cachedFrames...)
		}
	default:
		// 对于其他状态，使用普通缓存
		h.Mu.RLock()
		cachedFrames := h.CacheBuffer.GetAll()
		h.Mu.RUnlock()
		packets = append(packets, cachedFrames...)
	}

	h.Mu.Unlock()

	// 如果启用了FCC，尝试从频道缓存获取数据
	if client.fccSession != nil && len(addrList) > 0 && h.fccEnabled  {
		// 从频道缓存获取数据
		channelID := addrList[0] // 使用第一个地址作为频道ID
		channel := GlobalChannelManager.GetOrCreate(channelID)
		sessionPackets := channel.ReadForSession(client.fccSession)
		if sessionPackets != nil && len(sessionPackets) > 0 {
			// 将频道缓存的数据添加到发送队列开头，以确保快速切换
			packets = append(sessionPackets, packets...)
		}
	}

	// 异步非阻塞发送
	go h.sendPacketsNonBlocking(client.ch, packets)
}

