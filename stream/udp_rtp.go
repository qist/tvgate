package stream

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/net/ipv4"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"sync/atomic"

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
	Mu          sync.RWMutex
	Clients     map[string]hubClient // key = connID
	AddCh       chan hubClient
	RemoveCh    chan string
	UdpConns    []*net.UDPConn
	Closed      chan struct{}
	BufPool     *sync.Pool
	LastFrame   []byte
	CacheBuffer *RingBuffer
	AddrList    []string
	PacketCount uint64
	DropCount   uint64
	state       int // 0: stopped, 1: playing, 2: error
	stateCond   *sync.Cond
	OnEmpty     func(h *StreamHub) // 当客户端数量为0时触发
	// lastPAT     []byte
	// lastPMT     []byte
	// patSent     bool
	// lastRTPSequence uint16
	// lastRTPSSRC     uint32
	rtpBuffer      []byte // RTP拼接缓存
	lastCCMap      map[int]byte
	rtpSequenceMap map[uint32]*rtpSeqEntry

	// 多播重新加入相关字段
	rejoinTimer    *time.Timer
	rejoinInterval time.Duration
	ifaces         []string

	// FCC相关字段
	fccEnabled    bool         // 是否启用FCC功能
	fccType       int          // FCC类型 (电信/华为)
	fccCacheSize  int          // FCC缓存大小
	fccPortMin    int          // FCC监听端口范围最小值
	fccPortMax    int          // FCC监听端口范围最大值
	fccState      int          // FCC状态
	fccPendingBuf *RingBuffer  // 用于存储等待切换到多播的数据包
	patBuffer     []byte       // 存储最新的PAT包
	pmtBuffer     []byte       // 存储最新的PMT包
	fccServerAddr *net.UDPAddr // FCC服务器地址

	// 新增FCC相关字段
	fccUnicastConn *net.UDPConn // FCC单播连接
	fccUnicastPort int          // FCC单播端口
	fccSyncTimer   *time.Timer  // FCC同步超时计时器
}

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
		fccEnabled:   false, // 默认不启用，通过URL参数控制
		fccType:      fccType,
		fccCacheSize: fccCacheSize,
		fccPortMin:   fccPortMin,
		fccPortMax:   fccPortMax,
		fccState:     FCC_STATE_INIT,
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

	// 注意：即使没有启用FCC，我们也初始化FCC缓冲区，以备将来使用
	// 但只有在实际启用FCC时才使用它
	hub.fccPendingBuf = NewRingBuffer(hub.fccCacheSize)

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

		data := make([]byte, n)
		copy(data, buf[:n])
		h.BufPool.Put(buf)

		h.Mu.RLock()
		closed := h.state == StateStoppeds || h.CacheBuffer == nil
		h.Mu.RUnlock()
		if closed {
			return
		}

		// 处理RTP包，提取有效载荷
		processedData := h.processRTPPacket(data)
		if processedData == nil {
			continue
		}

		// 广播，不进行任何视频分析
		h.broadcast(processedData)
	}
}

// ====================
// RTP处理相关函数
// ====================

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

// processFCCPacket 处理FCC相关数据包
func (h *StreamHub) processFCCPacket(data []byte) bool {
	if !h.fccEnabled || len(data) < 8 {
		return false
	}

	// 检查是否为RTCP包
	if data[1] != 205 { // RTCP包类型205 (Generic RTP Feedback)
		return false
	}

	// 获取FMT字段 (第一个字节的低5位)
	fmtField := data[0] & 0x1F

	// 根据FCC类型处理不同的FMT
	switch h.fccType {
	case FCC_TYPE_HUAWEI:
		return h.processHuaweiFCCPacket(fmtField, data)
	case FCC_TYPE_TELECOM:
		fallthrough
	default:
		return h.processTelecomFCCPacket(fmtField, data)
	}
}

// processTelecomFCCPacket 处理电信FCC数据包
func (h *StreamHub) processTelecomFCCPacket(fmtField byte, data []byte) bool {
	switch fmtField {
	case FCC_FMT_TELECOM_RESP: // FMT 3 - 服务器响应
		h.Mu.Lock()
		if h.fccState == FCC_STATE_REQUESTED {
			h.fccState = FCC_STATE_UNICAST_PENDING
			logger.LogPrintf("FCC (电信): 收到服务器响应 (FMT 3)")
		}
		h.Mu.Unlock()
		return true

	case FCC_FMT_TELECOM_SYNC: // FMT 4 - 同步通知
		h.Mu.Lock()
		// Ignore if already using mcast stream
		if h.fccState == FCC_STATE_MCAST_REQUESTED || h.fccState == FCC_STATE_MCAST_ACTIVE {
			h.Mu.Unlock()
			return true
		}
		
		h.fccState = FCC_STATE_MCAST_REQUESTED
		logger.LogPrintf("FCC (电信): 收到同步通知 (FMT 4)，准备切换到组播")

		// 启动同步超时计时器
		if h.fccSyncTimer != nil {
			h.fccSyncTimer.Stop()
		}
		h.fccSyncTimer = time.AfterFunc(5*time.Second, func() {
			h.Mu.Lock()
			if h.fccState == FCC_STATE_MCAST_REQUESTED {
				h.fccState = FCC_STATE_MCAST_ACTIVE
				logger.LogPrintf("FCC (电信): 同步超时，强制切换到组播")
			}
			h.Mu.Unlock()
		})
		h.Mu.Unlock()
		return true

	default:
		return false
	}
}

// processHuaweiFCCPacket 处理华为FCC数据包
func (h *StreamHub) processHuaweiFCCPacket(fmtField byte, data []byte) bool {
	switch fmtField {
	case FCC_FMT_HUAWEI_RESP: // FMT 6 - 服务器响应
		h.Mu.Lock()
		if h.fccState == FCC_STATE_REQUESTED {
			h.fccState = FCC_STATE_UNICAST_PENDING
			logger.LogPrintf("FCC (华为): 收到服务器响应 (FMT 6)")

			// 检查是否需要NAT穿越
			if len(data) >= 32 {
				flag := binary.BigEndian.Uint32(data[28:32])
				if flag&0x01000000 != 0 {
					h.fccState = FCC_STATE_UNICAST_ACTIVE
					logger.LogPrintf("FCC (华为): 需要NAT穿越")
				}
			}
		}
		h.Mu.Unlock()
		return true

	case FCC_FMT_HUAWEI_SYNC: // FMT 8 - 同步通知
		h.Mu.Lock()
		// Ignore if already using mcast stream
		if h.fccState == FCC_STATE_MCAST_REQUESTED || h.fccState == FCC_STATE_MCAST_ACTIVE {
			h.Mu.Unlock()
			return true
		}
		
		if h.fccState == FCC_STATE_UNICAST_ACTIVE {
			h.fccState = FCC_STATE_MCAST_REQUESTED
			logger.LogPrintf("FCC (华为): 收到同步通知 (FMT 8)，准备切换到组播")

			// 启动同步超时计时器
			if h.fccSyncTimer != nil {
				h.fccSyncTimer.Stop()
			}
			h.fccSyncTimer = time.AfterFunc(5*time.Second, func() {
				h.Mu.Lock()
				if h.fccState == FCC_STATE_MCAST_REQUESTED {
					h.fccState = FCC_STATE_MCAST_ACTIVE
					logger.LogPrintf("FCC (华为): 同步超时，强制切换到组播")
				}
				h.Mu.Unlock()
			})
		}
		h.Mu.Unlock()
		return true

	case FCC_FMT_HUAWEI_NAT: // FMT 12 - NAT穿越包
		h.Mu.Lock()
		if h.fccState == FCC_STATE_UNICAST_PENDING {
			h.fccState = FCC_STATE_UNICAST_ACTIVE
			logger.LogPrintf("FCC (华为): 收到NAT穿越包 (FMT 12)")
		}
		h.Mu.Unlock()
		return true

	default:
		return false
	}
}

// 添加PAT/PMT缓冲区池以减少内存分配

// checkAndSwitchToMulticast 检查是否可以切换到多播并执行切换
func (h *StreamHub) checkAndSwitchToMulticast() {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	
	// 检查FCC缓冲区是否已满，如果满了则切换到多播模式
	if h.fccPendingBuf != nil && h.fccPendingBuf.GetCount() >= int(float64(h.fccCacheSize)*0.8) {
		// 缓冲区使用率达到80%，准备切换到多播
		if h.fccState == FCC_STATE_UNICAST_ACTIVE {
			h.fccState = FCC_STATE_MCAST_REQUESTED
			logger.LogPrintf("FCC: 缓冲区接近满载，准备切换到多播模式")
			
			// 启动切换定时器
			if h.fccSyncTimer != nil {
				h.fccSyncTimer.Stop()
			}
			h.fccSyncTimer = time.AfterFunc(3*time.Second, func() {
				h.Mu.Lock()
				if h.fccState == FCC_STATE_MCAST_REQUESTED {
					h.fccState = FCC_STATE_MCAST_ACTIVE
					logger.LogPrintf("FCC: 自动切换到多播模式")
				}
				h.Mu.Unlock()
			})
		}
	}
	
	if h.patBuffer == nil || h.pmtBuffer == nil {
		return
	}

	// 获取并清空等待缓冲区
	pendingData := h.fccPendingBuf.GetAll()
	h.fccPendingBuf.Reset()

	// 重新广播所有缓存的数据（包含最新的PAT/PMT）
	for _, data := range pendingData {
		h.broadcast(data)
	}

	// 更新状态为多播活跃
	h.fccState = FCC_STATE_MCAST_ACTIVE
	logger.LogPrintf("FCC: 已成功切换到组播模式")
}

// 改进的 processRTPPacket 函数
func (h *StreamHub) processRTPPacket(data []byte) []byte {
	// 首先检查是否为FCC控制包
	if h.processFCCPacket(data) {
		// 如果是FCC控制包，不需要进一步处理
		return nil
	}

	// 已经是完整 TS 包直接返回（兼容非 RTP 流）
	if len(data) >= 188 && data[0] == 0x47 {
		return data
	}

	// RTP Header 最小长度检查
	if len(data) < 12 {
		return data
	}

	version := (data[0] >> 6) & 0x03
	if version != RTP_VERSION {
		return data
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

	// 去重检查
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

	// 提取 RTP Payload
	startOff, endOff, err := rtpPayloadGet(data)
	if err != nil || startOff >= len(data)-endOff {
		return data // ✅ 兜底逻辑，返回原始数据
	}

	payloadType := data[1] & 0x7F
	if payloadType == P_MPGA || payloadType == P_MPGV {
		if startOff+4 < len(data)-endOff {
			startOff += 4
		}
	}

	payload := data[startOff : len(data)-endOff]

	// ✅ 兜底检查，必须对齐 188
	if len(payload) < 188 || payload[0] != 0x47 || len(payload)%188 != 0 {
		return data
	}

	// 拼接缓存，处理分片 - 使用无锁方式优化
	h.Mu.Lock()
	h.rtpBuffer = append(h.rtpBuffer, payload...)
	if len(h.rtpBuffer) < 188 {
		h.Mu.Unlock()
		return nil
	}

	if h.rtpBuffer[0] != 0x47 {
		idx := bytes.IndexByte(h.rtpBuffer, 0x47)
		if idx < 0 {
			h.rtpBuffer = h.rtpBuffer[:0] // 重用底层数组
			h.Mu.Unlock()
			return nil
		}
		// 重用底层数组，避免重新分配
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
		// 重用底层数组，避免重新分配
		copy(h.rtpBuffer, h.rtpBuffer[alignedSize:])
		h.rtpBuffer = h.rtpBuffer[:len(h.rtpBuffer)-alignedSize]
	} else {
		h.rtpBuffer = h.rtpBuffer[:0] // 重用底层数组
	}
	h.Mu.Unlock()

	// 使用预分配的缓冲区来减少内存分配
	out := make([]byte, 0, alignedSize)
	
	// 预先获取FCC启用状态，减少重复锁定
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
		
		// 如果启用了FCC，检测并保存PAT/PMT包
		if fccEnabled {
			// 检测PAT包(PID = 0x0000)并保存最新的一份
			if pid == PAT_PID && (ts[1]&0x40) != 0 { // payload_unit_start_indicator位为1
				h.Mu.Lock()
				if h.patBuffer == nil {
					h.patBuffer = make([]byte, 188)
				}
				copy(h.patBuffer, ts)
				h.Mu.Unlock()
			}

			// 检测PMT包(PID = 0x1000，通常是这个值)并保存最新的一份
			if pid == PMT_PID && (ts[1]&0x40) != 0 { // payload_unit_start_indicator位为1
				h.Mu.Lock()
				if h.pmtBuffer == nil {
					h.pmtBuffer = make([]byte, 188)
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

	// 如果启用了FCC，将处理后的数据也放入FCC缓冲区
	if fccEnabled && currentFccState != FCC_STATE_MCAST_ACTIVE {
		h.Mu.Lock()
		if h.fccPendingBuf != nil && len(out) > 0 {
			h.fccPendingBuf.PushWithReuse(out)

			// 根据当前FCC状态更新状态机
			// 只有在特定状态下才更新状态
			if currentFccState == FCC_STATE_UNICAST_PENDING {
				h.fccState = FCC_STATE_UNICAST_ACTIVE
			}
			
			// 检查是否需要切换到多播
			// 只在特定条件下检查切换，避免频繁调用
			if h.fccState == FCC_STATE_UNICAST_ACTIVE && 
			   h.fccPendingBuf.GetCount() >= int(float64(h.fccCacheSize)*0.5) {
				h.Mu.Unlock()
				h.checkAndSwitchToMulticast()
			} else {
				h.Mu.Unlock()
			}
		} else {
			h.Mu.Unlock()
		}
	}

	return out
}

// ====================
// 广播到所有客户端
// ====================
func (h *StreamHub) broadcast(data []byte) {
	// ---------- 快速校验 ----------
	h.Mu.RLock()
	if h.Closed == nil || h.CacheBuffer == nil || h.Clients == nil {
		h.Mu.RUnlock()
		return
	}

	// 读取只读状态
	fccEnabled := h.fccEnabled
	state := h.state

	// 拿 client 快照（推荐你后续换成 slice）
	clients := make([]hubClient, 0, len(h.Clients))
	for _, c := range h.Clients {
		clients = append(clients, c)
	}
	h.Mu.RUnlock()

	// ---------- 数据面（不加锁或最小锁） ----------
	// ⚠️ 如果 data 可能复用，这里必须 copy
	frame := data

	// 使用原子操作更新统计数据，减少锁竞争
	atomic.AddUint64(&h.PacketCount, 1)
	
	h.Mu.Lock()
	h.LastFrame = frame
	h.CacheBuffer.PushWithReuse(frame)

	// 播放状态切换
	if state != StatePlayings {
		h.state = StatePlayings
		h.stateCond.Broadcast()
	}
	h.Mu.Unlock()

	// ---------- FCC 控制面（轻量） ----------
	if fccEnabled {
		h.Mu.Lock()
		// 如果在多播活跃状态下，忽略单播数据包的处理
		if h.fccState == FCC_STATE_MCAST_ACTIVE {
			// 已经切换到多播模式，正常处理数据
		} else if h.fccState == FCC_STATE_MCAST_REQUESTED {
			// 在多播请求状态下，准备切换
			h.fccState = FCC_STATE_MCAST_ACTIVE
			logger.LogPrintf("FCC: 已切换到多播模式")
			
			if h.fccSyncTimer != nil {
				h.fccSyncTimer.Stop()
				h.fccSyncTimer = nil
			}
		}
		h.Mu.Unlock()
	}

	// ---------- 广播（完全无锁） ----------
	for _, client := range clients {
		select {
		case client.ch <- frame:
		default:
			h.handleClientDrop(client)
		}
	}
}

func (h *StreamHub) handleClientDrop(c hubClient) {
	// 客户端级 drop 计数（推荐）
	c.dropCount++

	// 每 100 次 drop，尝试恢复
	if c.dropCount%100 != 0 {
		return
	}

	// 丢弃一个旧帧
	select {
	case <-c.ch:
	default:
	}

	// 重发最后一帧
	h.Mu.RLock()
	lastFrame := h.LastFrame
	h.Mu.RUnlock()
	
	if lastFrame != nil {
		// 创建副本以避免数据竞争
		frameCopy := make([]byte, len(lastFrame))
		copy(frameCopy, lastFrame)
		
		select {
		case c.ch <- frameCopy:
		default:
		}
	}
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
		if h.fccPendingBuf != nil {
			fccFrames := h.fccPendingBuf.GetAll()
			if len(fccFrames) > 0 {
				start := 0
				if len(fccFrames) > 50 {
					start = len(fccFrames) - 50
				}
				packets = append(packets, fccFrames[start:]...)
			} else {
				// 如果没有FCC帧，尝试使用常规缓存帧作为后备
				cachedFrames := h.CacheBuffer.GetAll()
				packets = append(packets, cachedFrames...)
			}
		} else {
			// 如果没有FCC缓存，使用常规缓存作为后备
			cachedFrames := h.CacheBuffer.GetAll()
			packets = append(packets, cachedFrames...)
		}

	case FCC_STATE_MCAST_REQUESTED, FCC_STATE_MCAST_ACTIVE:
		// 多播 FCC：完整 FCC 缓存
		fccFramesAvailable := false
		if h.fccPendingBuf != nil {
			fccFrames := h.fccPendingBuf.GetAll()
			if len(fccFrames) > 0 {
				fccFramesAvailable = true
				packets = append(packets, fccFrames...)
			}
		}

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
	if h.fccPendingBuf != nil {
		h.fccPendingBuf.Reset()
		h.fccPendingBuf = nil
	}
	
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

// buildFCCRequestPacket 构建FCC请求包
func (h *StreamHub) buildFCCRequestPacket(multicastAddr *net.UDPAddr, clientPort int) []byte {
	localIP := getLocalIP()

	switch h.fccType {
	case FCC_TYPE_HUAWEI:
		return h.buildHuaweiFCCRequestPacket(multicastAddr, localIP, clientPort)
	case FCC_TYPE_TELECOM:
		fallthrough
	default:
		return h.buildTelecomFCCRequestPacket(multicastAddr, clientPort)
	}
}

// buildTelecomFCCRequestPacket 构建电信FCC请求包 (FMT 2)
func (h *StreamHub) buildTelecomFCCRequestPacket(multicastAddr *net.UDPAddr, clientPort int) []byte {
	pk := make([]byte, 24)

	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_TELECOM_REQ     // Version 2, Padding 0, FMT 2
	pk[1] = 205                            // Type: Generic RTP Feedback (205)
	binary.BigEndian.PutUint16(pk[2:4], 5) // Length = 6 words - 1 = 5

	// Media source SSRC (4 bytes) - multicast IP address
	ssrc := binary.BigEndian.Uint32(multicastAddr.IP.To4())
	binary.BigEndian.PutUint32(pk[8:12], ssrc)

	// FCI - Feedback Control Information
	binary.BigEndian.PutUint16(pk[16:18], uint16(clientPort))         // FCC client port
	binary.BigEndian.PutUint16(pk[18:20], uint16(multicastAddr.Port)) // Mcast group port
	copy(pk[20:24], multicastAddr.IP.To4())                           // Mcast group IP

	return pk
}

// buildHuaweiFCCRequestPacket 构建华为FCC请求包 (FMT 5)
func (h *StreamHub) buildHuaweiFCCRequestPacket(multicastAddr *net.UDPAddr, localIP net.IP, clientPort int) []byte {
	pk := make([]byte, 32)

	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_HUAWEI_REQ      // V=2, P=0, FMT=5
	pk[1] = 205                            // PT=205 (Generic RTP Feedback)
	binary.BigEndian.PutUint16(pk[2:4], 7) // Length = 8 words - 1 = 7

	// Media Source SSRC (4 bytes) - multicast IP address
	ssrc := binary.BigEndian.Uint32(multicastAddr.IP.To4())
	binary.BigEndian.PutUint32(pk[8:12], ssrc)

	// FCI - Feedback Control Information (16 bytes)
	// Local IP address (4 bytes) - network byte order
	if localIP != nil && localIP.To4() != nil {
		copy(pk[20:24], localIP.To4())
	}

	// FCC client port (2 bytes) + Flag (2 bytes)
	binary.BigEndian.PutUint16(pk[24:26], uint16(clientPort))
	binary.BigEndian.PutUint16(pk[26:28], 0x8000)

	// Redirect support flag (4 bytes) - 0x20000000
	binary.BigEndian.PutUint32(pk[28:32], 0x20000000)

	return pk
}

// buildFCCTermPacket 构建FCC终止包
func (h *StreamHub) buildFCCTermPacket(multicastAddr *net.UDPAddr, seqNum uint16) []byte {
	switch h.fccType {
	case FCC_TYPE_HUAWEI:
		return h.buildHuaweiFCCTermPacket(multicastAddr, seqNum)
	case FCC_TYPE_TELECOM:
		fallthrough
	default:
		return h.buildTelecomFCCTermPacket(multicastAddr, seqNum)
	}
}

// buildTelecomFCCTermPacket 构建电信FCC终止包 (FMT 5)
func (h *StreamHub) buildTelecomFCCTermPacket(multicastAddr *net.UDPAddr, seqNum uint16) []byte {
	pk := make([]byte, 16)

	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_TELECOM_TERM    // Version 2, Padding 0, FMT 5
	pk[1] = 205                            // Type: Generic RTP Feedback (205)
	binary.BigEndian.PutUint16(pk[2:4], 3) // Length = 4 words - 1 = 3

	// Media source SSRC (4 bytes) - multicast IP address
	ssrc := binary.BigEndian.Uint32(multicastAddr.IP.To4())
	binary.BigEndian.PutUint32(pk[8:12], ssrc)

	// FCI - Feedback Control Information
	if seqNum > 0 {
		pk[12] = 0                                    // Status: normal stop
		binary.BigEndian.PutUint16(pk[14:16], seqNum) // First multicast packet sequence
	} else {
		pk[12] = 1 // Status: force stop
	}

	return pk
}

// buildHuaweiFCCTermPacket 构建华为FCC终止包 (FMT 9)
func (h *StreamHub) buildHuaweiFCCTermPacket(multicastAddr *net.UDPAddr, seqNum uint16) []byte {
	pk := make([]byte, 16)

	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_HUAWEI_TERM     // V=2, P=0, FMT=9
	pk[1] = 205                            // PT=205 (Generic RTP Feedback)
	binary.BigEndian.PutUint16(pk[2:4], 3) // Length = 4 words - 1 = 3

	// Media Source SSRC (4 bytes) - multicast IP address
	ssrc := binary.BigEndian.Uint32(multicastAddr.IP.To4())
	binary.BigEndian.PutUint32(pk[8:12], ssrc)

	// FCI - Status byte and sequence number (4 bytes)
	if seqNum > 0 {
		pk[12] = 0x01                                 // Status: joined multicast successfully
		binary.BigEndian.PutUint16(pk[14:16], seqNum) // First multicast sequence number
	} else {
		pk[12] = 0x00 // Status: normal termination
	}

	return pk
}

// getLocalIP 获取本地IP地址
func getLocalIP() net.IP {
	// 准备多个备选地址，提高获取本地IP的成功率
	dnsServers := []string{"8.8.8.8:80", "8.8.4.4:80", "223.5.5.5:80", "223.6.6.6:80"}

	for _, server := range dnsServers {
		conn, err := net.DialTimeout("udp", server, 2*time.Second)
		if err != nil {
			continue // 当前服务器失败，尝试下一个
		}
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)
		return localAddr.IP
	}

	// 如果通过连接外部服务器无法获取本地IP，则尝试通过网络接口获取
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			// 跳过本地回环接口
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}

			// 跳过禁用的接口
			if iface.Flags&net.FlagUp == 0 {
				continue
			}

			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}

			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						return ipnet.IP
					}
				}
			}
		}
	}

	// 所有方法都失败，返回nil
	return nil
}

// SetFccType 设置FCC类型
func (h *StreamHub) SetFccType(fccType string) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	switch fccType {
	case "huawei":
		h.fccType = FCC_TYPE_HUAWEI
	case "telecom":
		h.fccType = FCC_TYPE_TELECOM
	default:
		h.fccType = FCC_TYPE_TELECOM // 默认为电信类型
	}
}

// GetFccType 获取FCC类型
func (h *StreamHub) GetFccType() string {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	switch h.fccType {
	case FCC_TYPE_HUAWEI:
		return "huawei"
	case FCC_TYPE_TELECOM:
		return "telecom"
	default:
		return "telecom"
	}
}

// EnableFCC 启用或禁用FCC功能
func (h *StreamHub) EnableFCC(enabled bool) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	if h.fccEnabled == enabled {
		return
	}

	h.fccEnabled = enabled
	if enabled {
		if h.fccPendingBuf == nil {
			h.fccPendingBuf = NewRingBuffer(h.fccCacheSize)
		}
		h.fccState = FCC_STATE_INIT
		
		// 启动一个定时器，如果FCC在5秒内没有进展，则自动切换到多播模式
		go func() {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			
			select {
			case <-timer.C:
				h.Mu.Lock()
				if h.fccEnabled && h.fccState == FCC_STATE_INIT && !h.isClosed() {
					h.fccState = FCC_STATE_MCAST_ACTIVE
					logger.LogPrintf("FCC: 初始化超时，直接切换到多播模式")
				}
				h.Mu.Unlock()
			case <-h.Closed:
				// 如果hub关闭则退出
				return
			}
		}()
	} else {
		// 禁用FCC时清理相关资源
		if h.fccPendingBuf != nil {
			h.fccPendingBuf.Reset()
		}
		h.fccState = FCC_STATE_INIT
		
		// 清理PAT/PMT缓冲区
		if h.patBuffer != nil {
			patBufferPool.Put(h.patBuffer)
			h.patBuffer = nil
		}
		if h.pmtBuffer != nil {
			pmtBufferPool.Put(h.pmtBuffer)
			h.pmtBuffer = nil
		}
	}
}

// SetFccParams 设置FCC参数
func (h *StreamHub) SetFccParams(cacheSize, portMin, portMax int) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	h.fccCacheSize = cacheSize
	h.fccPortMin = portMin
	h.fccPortMax = portMax

	if h.fccEnabled && h.fccPendingBuf != nil {
		// 重建缓冲区以适应新的大小
		h.fccPendingBuf = NewRingBuffer(cacheSize)
	}
}

// SetFccState 设置FCC状态并记录状态转换日志
func (h *StreamHub) SetFccState(state int) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	oldState := h.fccState
	h.fccState = state

	// 记录状态转换日志
	stateNames := map[int]string{
		FCC_STATE_INIT:            "INIT",
		FCC_STATE_REQUESTED:       "REQUESTED",
		FCC_STATE_UNICAST_PENDING: "UNICAST_PENDING",
		FCC_STATE_UNICAST_ACTIVE:  "UNICAST_ACTIVE",
		FCC_STATE_MCAST_REQUESTED: "MCAST_REQUESTED",
		FCC_STATE_MCAST_ACTIVE:    "MCAST_ACTIVE",
		FCC_STATE_ERROR:           "ERROR",
	}

	logger.LogPrintf("FCC状态转换: %s -> %s", stateNames[oldState], stateNames[state])
}

// GetFccState 获取FCC状态
func (h *StreamHub) GetFccState() int {
	h.Mu.RLock()
	defer h.Mu.RUnlock()
	return h.fccState
}

// IsFccEnabled 检查FCC是否启用
func (h *StreamHub) IsFccEnabled() bool {
	h.Mu.RLock()
	defer h.Mu.RUnlock()
	return h.fccEnabled
}

// GetFccCacheSize 获取FCC缓存大小
func (h *StreamHub) GetFccCacheSize() int {
	h.Mu.RLock()
	defer h.Mu.RUnlock()
	return h.fccCacheSize
}

// GetFccPortMin 获取FCC监听端口最小值
func (h *StreamHub) GetFccPortMin() int {
	h.Mu.RLock()
	defer h.Mu.RUnlock()
	return h.fccPortMin
}

// GetFccPortMax 获取FCC监听端口最大值
func (h *StreamHub) GetFccPortMax() int {
	h.Mu.RLock()
	defer h.Mu.RUnlock()
	return h.fccPortMax
}

// sendFCCRequest 发送FCC请求包
func (h *StreamHub) sendFCCRequest(multicastAddr *net.UDPAddr, clientPort int) error {
	h.Mu.RLock()
	fccEnabled := h.fccEnabled
	h.Mu.RUnlock()

	if !fccEnabled {
		return nil
	}

	// 构建FCC请求包
	requestPacket := h.buildFCCRequestPacket(multicastAddr, clientPort)

	// 使用指定的FCC服务器地址或者默认使用组播地址
	targetAddr := h.GetFccServerAddr()
	if targetAddr == nil {
		targetAddr = multicastAddr
	}

	// 创建FCC UDP连接
	fccConn, err := net.DialUDP("udp", nil, targetAddr)
	if err != nil {
		return err
	}
	defer fccConn.Close()

	// 发送三次以确保送达
	for i := 0; i < 3; i++ {
		// 检查hub是否已关闭
		if h.isClosed() {
			return nil
		}
		
		_, err = fccConn.Write(requestPacket)
		if err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}

	return nil
}

// sendFCCTermination 发送FCC终止包
func (h *StreamHub) sendFCCTermination(multicastAddr *net.UDPAddr, seqNum uint16) error {
	h.Mu.RLock()
	fccEnabled := h.fccEnabled
	h.Mu.RUnlock()

	if !fccEnabled {
		return nil
	}

	// 构建FCC终止包
	termPacket := h.buildFCCTermPacket(multicastAddr, seqNum)

	// 使用指定的FCC服务器地址或者默认使用组播地址
	targetAddr := h.GetFccServerAddr()
	if targetAddr == nil {
		targetAddr = multicastAddr
	}

	// 创建FCC UDP连接
	fccConn, err := net.DialUDP("udp", nil, targetAddr)
	if err != nil {
		return err
	}
	defer fccConn.Close()

	// 发送三次以确保送达
	for i := 0; i < 3; i++ {
		// 检查hub是否已关闭
		if h.isClosed() {
			return nil
		}
		
		_, err = fccConn.Write(termPacket)
		if err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}

	return nil
}

// SetFccServerAddr 设置FCC服务器地址
func (h *StreamHub) SetFccServerAddr(addr string) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}

	h.fccServerAddr = udpAddr
	return nil
}

// GetFccServerAddr 获取FCC服务器地址
func (h *StreamHub) GetFccServerAddr() *net.UDPAddr {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	return h.fccServerAddr
}

// initFCCConnection 初始化FCC单播连接
func (h *StreamHub) initFCCConnection() bool {
	h.Mu.Lock()
	
	// 如果已经初始化过了，直接返回
	if h.fccUnicastConn != nil {
		h.Mu.Unlock()
		return true
	}
	
	// 检查hub是否已关闭
	if h.isClosed() {
		h.Mu.Unlock()
		return false
	}

	// 创建监听端口（在配置范围内选择一个可用端口）
	portMin := h.fccPortMin
	portMax := h.fccPortMax

	// 如果没有配置端口范围，则使用随机端口
	if portMin <= 0 || portMax <= 0 || portMin > portMax {
		portMin = 1024
		portMax = 65535
	}
	
	// 临时解锁以执行可能耗时的操作
	h.Mu.Unlock()

	// 尝试绑定端口
	for attempts := 0; attempts < 10; attempts++ {
		port := portMin
		if portMax > portMin {
			port = portMin + rand.Intn(portMax-portMin+1)
		}

		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}

		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			continue
		}

		// 重新加锁以更新连接状态
		h.Mu.Lock()
		// 双重检查，确保在获得锁期间没有其他goroutine初始化连接
		if h.fccUnicastConn != nil {
			h.Mu.Unlock()
			conn.Close() // 关闭刚刚创建的连接
			return true
		}
		
		// 检查hub是否在尝试建立连接期间被关闭
		if h.isClosed() {
			h.Mu.Unlock()
			conn.Close()
			return false
		}
		
		h.fccUnicastConn = conn
		h.fccUnicastPort = port

		// 启动FCC单播数据接收goroutine
		go h.receiveFCCUnicastData()

		logger.LogPrintf("FCC单播连接已初始化，监听端口: %d", port)
		h.Mu.Unlock()
		return true
	}

	logger.LogPrintf("FCC单播连接初始化失败")
	return false
}

// receiveFCCUnicastData 接收FCC单播数据
func (h *StreamHub) receiveFCCUnicastData() {
	// 使用固定大小的缓冲区池来减少内存分配
	const readBufferSize = 64 * 1024 // 64KB缓冲区
	bufferPool := &sync.Pool{
		New: func() interface{} {
			return make([]byte, readBufferSize)
		},
	}

	for {
		h.Mu.RLock()
		conn := h.fccUnicastConn
		h.Mu.RUnlock()
		
		// 检查hub是否已关闭
		if h.isClosed() || conn == nil {
			return
		}

		// 从池中获取缓冲区
		buf := bufferPool.Get().([]byte)
		
		n, err := conn.Read(buf)
		if err != nil {
			// 将缓冲区返回到池中
			bufferPool.Put(buf)
			
			// 检查是否是关闭错误
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			// 检查hub是否已关闭
			if h.isClosed() {
				return
			}
			continue
		}

		if n > 0 {
			// 直接使用切片，避免额外的内存分配
			h.handleFCCUnicastData(buf[:n])
		}
		
		// 将缓冲区返回到池中
		bufferPool.Put(buf)
	}
}

// handleFCCUnicastData 处理FCC单播数据
func (h *StreamHub) handleFCCUnicastData(data []byte) {
	h.Mu.Lock()
	currentState := h.fccState
	h.Mu.Unlock()

	switch currentState {
	case FCC_STATE_REQUESTED:
		// 在REQUESTED状态下，期望收到服务器响应
		if h.processFCCPacket(data) {
			// 如果是FCC控制包，processFCCPacket会处理状态转换
			return
		}
		// 如果不是控制包，当作媒体数据处理
		fallthrough

	case FCC_STATE_UNICAST_PENDING, FCC_STATE_UNICAST_ACTIVE:
		// 处理单播媒体数据
		h.processFCCMediaData(data)

	case FCC_STATE_MCAST_REQUESTED:
		// 处理可能的同步确认包
		h.processFCCPacket(data)
		
		// 同时处理媒体数据
		h.processFCCMediaData(data)
		
		// 检查是否应该终止FCC并切换到多播
		h.checkFCCSwitchCondition()

	case FCC_STATE_MCAST_ACTIVE:
		// 已经切换到多播模式，忽略单播数据
		return

	default:
		// 其他状态下忽略单播数据
		return
	}
}

// checkFCCSwitchCondition 检查FCC切换条件
func (h *StreamHub) checkFCCSwitchCondition() {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	
	// 如果已经发送了终止消息并且达到了终止序列号，则切换到多播模式
	if h.fccState == FCC_STATE_MCAST_REQUESTED {
		// 在Go实现中，我们简化处理，直接切换到多播模式
		h.fccState = FCC_STATE_MCAST_ACTIVE
		logger.LogPrintf("FCC: 切换到多播模式")
		
		if h.fccSyncTimer != nil {
			h.fccSyncTimer.Stop()
			h.fccSyncTimer = nil
		}
	}
}

// 添加一个带缓冲的引用计数包装器，实现类似C版本的零拷贝效果
type BufferRef struct {
	data []byte
	refs int32
	mu   sync.Mutex
}

func NewBufferRef(data []byte) *BufferRef {
	return &BufferRef{
		data: data,
		refs: 1,
	}
}

func (b *BufferRef) AddRef() {
	atomic.AddInt32(&b.refs, 1)
}

func (b *BufferRef) Release() {
	if atomic.AddInt32(&b.refs, -1) == 0 {
		// 可以在这里将缓冲区返回到内存池以供重用
		// 这里为了简化省略实际的内存池实现
		b.data = nil
	}
}

func (b *BufferRef) GetData() []byte {
	return b.data
}

// processFCCMediaData 处理FCC媒体数据
func (h *StreamHub) processFCCMediaData(data []byte) {
	// 处理RTP包并提取TS数据
	processedData := h.processRTPPacket(data)
	if processedData != nil && len(processedData) > 0 {
		// 广播数据到客户端
		h.broadcast(processedData)
	}
}
func (h *StreamHub) broadcastWithRef(bufferRef *BufferRef) {
	// 快速校验
	h.Mu.RLock()
	if h.Closed == nil || h.Clients == nil {
		h.Mu.RUnlock()
		return
	}

	// 获取客户端快照
	clients := make([]hubClient, 0, len(h.Clients))
	for _, c := range h.Clients {
		clients = append(clients, c)
	}
	h.Mu.RUnlock()

	// 增加引用计数
	bufferRef.AddRef()
	data := bufferRef.GetData()
	
	// 更新统计信息 (使用原子操作减少锁竞争)
	atomic.AddUint64(&h.PacketCount, 1)
	
	h.Mu.Lock()
	h.LastFrame = data
	if h.CacheBuffer != nil {
		h.CacheBuffer.Push(data)
	}
	h.Mu.Unlock()

	// 广播到所有客户端
	for _, client := range clients {
		select {
		case client.ch <- data:
		default:
			// 不处理丢包，保持最快速度
		}
	}
}

// cleanupFCC 清理FCC连接
// 注意：此方法仅在完全关闭hub时调用，不应该在单个客户端断开时调用
func (h *StreamHub) cleanupFCC() {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 停止同步计时器
	if h.fccSyncTimer != nil {
		h.fccSyncTimer.Stop()
		h.fccSyncTimer = nil
	}

	// 关闭FCC单播连接
	if h.fccUnicastConn != nil {
		// 异步关闭连接以避免阻塞
		go func(conn *net.UDPConn) {
			conn.Close()
		}(h.fccUnicastConn)
		h.fccUnicastConn = nil
	}

	// 重置FCC状态
	h.fccState = FCC_STATE_INIT
	h.fccUnicastPort = 0

	// 清理缓冲区并将缓冲区返回到相应的池中
	if h.patBuffer != nil {
		patBufferPool.Put(h.patBuffer)
		h.patBuffer = nil
	}
	if h.pmtBuffer != nil {
		pmtBufferPool.Put(h.pmtBuffer)
		h.pmtBuffer = nil
	}
	if h.fccPendingBuf != nil {
		h.fccPendingBuf.Reset()
		h.fccPendingBuf = nil
	}
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
