package stream

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/ipv4"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

const (
	RTP_VERSION = 2
	P_MPGA      = 14
	P_MPGV      = 32
	NULL_PID    = 0x1FFF
	PAT_PID     = 0x0000
	PMT_PID     = 0x1000

	// FCC类型
	FCC_TYPE_TELECOM = 0
	FCC_TYPE_HUAWEI  = 1

	// FCC格式类型
	FCC_FMT_TELECOM_REQ  = 2 // 电信请求
	FCC_FMT_TELECOM_RESP = 3 // 电信响应
	FCC_FMT_TELECOM_SYNC = 4 // 电信同步
	FCC_FMT_TELECOM_TERM = 5 // 电信终止

	FCC_FMT_HUAWEI_REQ  = 5  // 华为请求
	FCC_FMT_HUAWEI_RESP = 6  // 华为响应
	FCC_FMT_HUAWEI_NAT  = 12 // 华为NAT穿越
	FCC_FMT_HUAWEI_SYNC = 8  // 华为同步
	FCC_FMT_HUAWEI_TERM = 9  // 华为终止

	// FCC状态
	FCC_STATE_INIT = iota
	FCC_STATE_REQUESTED
	FCC_STATE_UNICAST_PENDING
	FCC_STATE_UNICAST_ACTIVE
	FCC_STATE_MCAST_REQUESTED
	FCC_STATE_MCAST_ACTIVE
	FCC_STATE_ERROR

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
	ch        chan []byte
	connID    string
	dropCount uint64 // 客户端丢包计数
	// lastFrame []byte // 客户端最后一帧，用于重发
}

type StreamHub struct {
	Mu               sync.RWMutex
	Clients          map[string]hubClient
	isPlaying        bool
	pktBuffer        *RingBuffer
	patBuffer        []byte
	pmtBuffer        []byte
	isClosedFlag     int32
	Closed           chan struct{}
	notify           chan struct{}
	SrcIP            net.IP
	SrcPort          int
	multicastSrcIP   net.IP
	multicastSrcPort int
	Method           string

	// 原有缓冲区和连接相关字段
	BufPool     *sync.Pool
	LastFrame   []byte
	CacheBuffer *RingBuffer
	AddrList    []string
	PacketCount uint64
	DropCount   uint64
	state       int // 0: stopped, 1: playing, 2: error
	stateCond   *sync.Cond
	OnEmpty     func(h *StreamHub) // 当客户端数量为0时触发

	// UDP连接相关字段
	UdpConns       []*net.UDPConn
	rtpBuffer      []byte
	rejoinTimer    *time.Timer   // 重新加入组播组的定时器
	rejoinInterval time.Duration // 重新加入组播组的时间间隔
	ifaces         []string      // 指定的网络接口

	// 客户端管理通道
	AddCh    chan hubClient
	RemoveCh chan string

	// RTP包处理相关字段
	lastCCMap      map[int]byte            // PID -> TS包中的CC字段
	rtpSequenceMap map[uint32]*rtpSeqEntry // SSRC -> RTP序列号信息

	// FCC相关字段
	fccEnabled        bool
	fccType           int
	fccState          int
	fccCacheSize      int
	fccPortMin        int
	fccPortMax        int
	fccStartSeq       uint16
	fccTermSeq        uint16
	fccTermSent       bool
	fccSyncTimer      *time.Timer
	fccServerAddr     *net.UDPAddr
	fccUnicastConn    *net.UDPConn
	fccUnicastPort    int
	fccUnicastBufPool *sync.Pool
	fccPendingCount   int32

	// 统一使用零拷贝缓冲区管理和状态转换的字段
	fccPendingListHead *BufferRef
	fccPendingListTail *BufferRef

	// 添加客户端状态更新通道
	clientStateChan chan int
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

		// FCC相关初始化
		fccEnabled:        false, // 默认不启用，通过URL参数控制
		fccType:           fccType,
		fccCacheSize:      fccCacheSize,
		fccPortMin:        fccPortMin,
		fccPortMax:        fccPortMax,
		fccState:          FCC_STATE_INIT,
		fccUnicastBufPool: &sync.Pool{New: func() any { return make([]byte, 64*1024) }},
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
// 启动 UDPConn readLoop
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
func hexdumpPreview(buf []byte, n int) string {
	if len(buf) > n {
		buf = buf[:n]
	}
	return hex.EncodeToString(buf)
}

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
func (h *StreamHub) broadcast(data []byte) {
	// 检查是否是FCC多播过渡阶段
	if h.IsFccEnabled() {
		h.Mu.RLock()
		inTransition := h.fccState == FCC_STATE_MCAST_REQUESTED
		h.Mu.RUnlock()

		if inTransition {
			h.handleMcastDataDuringTransition(data)
			return
		}
	}

	// 检查是否是PAT或PMT包
	pid := ((uint16(data[1]) & 0x1f) << 8) | uint16(data[2])

	h.Mu.Lock()
	defer h.Mu.Unlock()

	if pid == PAT_PID {
		// 保存PAT包用于FCC
		if h.patBuffer == nil {
			h.patBuffer = patBufferPool.Get().([]byte)
		}
		copy(h.patBuffer, data)
	} else if pid == PMT_PID {
		// 保存PMT包用于FCC
		if h.pmtBuffer == nil {
			h.pmtBuffer = pmtBufferPool.Get().([]byte)
		}
		copy(h.pmtBuffer, data)
	}

	// 如果FCC处于活动状态，将数据包添加到FCC缓冲区
	if h.fccEnabled && h.fccState != FCC_STATE_MCAST_ACTIVE {
		// 使用零拷贝链表存储数据
		bufRef := NewBufferRef(data)
		bufRef.Get() // 增加引用计数
		if h.fccPendingListHead == nil {
			h.fccPendingListHead = bufRef
			h.fccPendingListTail = bufRef
		} else {
			h.fccPendingListTail.next = bufRef
			h.fccPendingListTail = bufRef
		}
	}

	// 发送数据给所有客户端
	for _, c := range h.Clients {
		select {
		case c.ch <- data:
		case <-time.After(100 * time.Millisecond):
			// 如果发送超时，则断开客户端连接
			// 注意：这里不能直接调用Close，因为hubClient没有Close方法
		}
	}
}

// 零拷贝引用广播，发送完成后归还池
func (h *StreamHub) broadcastRef(bufRef *BufferRef) {
	// 检查是否是FCC多播过渡阶段
	if h.IsFccEnabled() {
		h.Mu.RLock()
		inTransition := h.fccState == FCC_STATE_MCAST_REQUESTED
		h.Mu.RUnlock()
		if inTransition {
			h.handleMcastDataDuringTransition(bufRef.data)
			bufRef.Put()
			return
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
}

// ====================
// 客户端管理循环
// ====================
func (h *StreamHub) run() {
	for {
		select {
		case client := <-h.AddCh:
			h.Mu.Lock()
			h.Clients[client.connID] = client
			curCount := len(h.Clients)
			h.Mu.Unlock()
			go h.sendInitial(client.ch)
			logger.LogPrintf("➕ 客户端加入，当前客户端数量=%d", curCount)

		case connID := <-h.RemoveCh:
			var clientToClose *hubClient
			var curCount int
			var shouldCloseHub bool

			h.Mu.Lock()
			if client, ok := h.Clients[connID]; ok {
				clientToClose = &client
				delete(h.Clients, connID)
				curCount = len(h.Clients)
				logger.LogPrintf("➖ 客户端离开，当前客户端数量=%d", curCount)

				// 如果没有客户端了，准备关闭Hub
				if curCount == 0 {
					shouldCloseHub = true
				}
			}
			h.Mu.Unlock()

			// 在锁外关闭客户端channel
			if clientToClose != nil && clientToClose.ch != nil {
				close(clientToClose.ch)
			}

			// 如果没有客户端了，异步关闭Hub
			if shouldCloseHub {
				// 只有在启用FCC时才清理FCC连接
				if h.fccEnabled {
					h.cleanupFCC()
				}

				// 在单独的goroutine中关闭以避免死锁
				go h.Close()
				if h.OnEmpty != nil {
					h.OnEmpty(h) // 自动删除 hub
				}
				return
			}

		case <-h.Closed:
			h.Mu.Lock()
			// 安全地关闭所有客户端通道
			for _, client := range h.Clients {
				if client.ch != nil {
					close(client.ch)
				}
			}
			h.Clients = nil
			h.Mu.Unlock()
			return
		}
	}
}

// ====================
// 新客户端发送初始化帧
// FCC / 非 FCC 统一入口
// ====================
func (h *StreamHub) sendInitial(ch chan []byte) {
	// ---------- 读取 FCC 状态（最小锁粒度） ----------

	h.Mu.Lock()
	fccEnabled := h.fccEnabled
	currentState := h.fccState
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
		go h.sendPacketsNonBlocking(ch, cachedFrames)
		return
	}

	// ---------- FCC 模式 ----------
	h.Mu.Lock()

	var packets [][]byte

	// PAT / PMT 优先
	if h.patBuffer != nil {
		packets = append(packets, h.patBuffer)
	}
	if h.pmtBuffer != nil {
		packets = append(packets, h.pmtBuffer)
	}

	switch currentState {

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
	}

	h.Mu.Unlock()

	// 异步非阻塞发送
	go h.sendPacketsNonBlocking(ch, packets)
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
	select {
	case <-h.Closed:
		http.Error(w, "Stream hub closed", http.StatusServiceUnavailable)
		return
	default:
	}

	connID := r.Header.Get("X-ConnID")
	if connID == "" {
		connID = strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	// 增加缓冲区大小
	ch := make(chan []byte, 4096)
	h.AddCh <- hubClient{ch: ch, connID: connID}

	// 检查是否启用了FCC
	h.Mu.Lock()
	fccEnabled := h.fccEnabled
	h.Mu.Unlock()

	// 客户端端口，用于FCC请求
	clientPort := 0
	remoteAddr := r.RemoteAddr
	host, port, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		if portNum, err := strconv.Atoi(port); err == nil {
			clientPort = portNum
		}
	}

	// 如果没有成功获取客户端端口，且启用了FCC，则使用配置的端口范围作为默认值
	if clientPort == 0 && fccEnabled {
		h.Mu.RLock()
		portMin, portMax := h.fccPortMin, h.fccPortMax
		h.Mu.RUnlock()

		if portMin > 0 && portMax > 0 && portMin <= portMax {
			clientPort = portMin
		}
	}

	// 如果启用了FCC，发送FCC请求
	fccInitialized := false
	fccTerminationSent := make(chan struct{}) // 用于确保FCC终止包只发送一次
	if fccEnabled {
		// 初始化FCC连接（如果尚未初始化）
		fccInitialized = h.initFCCConnection()
		if fccInitialized {
			go func() {
				defer close(fccTerminationSent) // 标记FCC终止包已处理

				for _, addr := range h.AddrList {
					udpAddr, err := net.ResolveUDPAddr("udp", addr)
					if err != nil {
						continue
					}

					// 发送FCC请求
					err = h.sendFCCRequest(udpAddr, h.fccUnicastPort)
					if err != nil {
						logger.LogPrintf("FCC请求发送失败: %v", err)
					} else {
						h.SetFccState(FCC_STATE_REQUESTED)
						logger.LogPrintf("FCC请求已发送到 %s 用于客户端 %s", addr, host)
					}
				}
			}()
		}
	}

	defer func() {
		h.RemoveCh <- connID

		// 只有在FCC已初始化的情况下才发送终止包
		if fccEnabled && fccInitialized {
			// 等待FCC请求完成后再发送终止包
			go func() {
				// 等待FCC请求goroutine完成，设置超时防止无限等待
				select {
				case <-fccTerminationSent: // 等待FCC请求goroutine完成
				case <-time.After(5 * time.Second): // 最多等待5秒
					logger.LogPrintf("等待FCC请求完成超时")
				}

				seqNum := uint16(0) // 在实际应用中应该获取最后一个序列号
				for _, addr := range h.AddrList {
					udpAddr, err := net.ResolveUDPAddr("udp", addr)
					if err != nil {
						continue
					}

					err = h.sendFCCTermination(udpAddr, seqNum)
					if err != nil {
						logger.LogPrintf("FCC终止包发送失败: %v", err)
					} else {
						logger.LogPrintf("FCC终止包已发送到 %s", addr)
					}
				}
			}()
		}

		// 注意：不在这里调用cleanupFCC()，而是在所有客户端都断开时调用
	}()

	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("ContentFeatures.DLNA.ORG", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000")
	w.Header().Set("TransferMode.DLNA.ORG", "Streaming")
	w.Header().Set("Content-Type", contentType)

	userAgent := r.Header.Get("User-Agent")
	switch {
	case strings.Contains(userAgent, "VLC"):
		w.Header().Del("Transfer-Encoding")
		w.Header().Set("Accept-Ranges", "none")
	default:
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Accept-Ranges", "none")
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	bufferedBytes := 0
	const maxBufferSize = 128 * 1024 // 128KB缓冲区
	flushTicker := time.NewTicker(50 * time.Millisecond)
	defer flushTicker.Stop()
	activeTicker := time.NewTicker(5 * time.Second)
	defer activeTicker.Stop()

	if !h.WaitForPlaying(ctx) {
		return
	}

	// 检查客户端是否已经断开连接
	clientDisconnected := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(clientDisconnected)
	}()

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			n, err := w.Write(data)
			if err != nil {
				return
			}
			bufferedBytes += n
			if bufferedBytes >= maxBufferSize {
				flusher.Flush()
				bufferedBytes = 0
			}
		case <-flushTicker.C:
			if bufferedBytes > 0 {
				flusher.Flush()
				bufferedBytes = 0
			}
		case <-activeTicker.C:
			if updateActive != nil {
				updateActive()
			}
		case <-clientDisconnected:
			// 客户端断开连接，退出循环
			return
		case <-h.Closed:
			return
		}
	}
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

// isClosed 检查hub是否已关闭
func (h *StreamHub) isClosed() bool {
	select {
	case <-h.Closed:
		return true
	default:
		return false
	}
}

// SetFccState 设置FCC状态并记录状态转换日志
func (h *StreamHub) SetFccState(state int) {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	h.fccSetState(state, "SetFccState")
}
