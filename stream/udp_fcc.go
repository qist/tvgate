package stream

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qist/tvgate/logger"
)

// BufferRef 用于零拷贝缓冲区管理
type BufferRef struct {
	data     []byte
	next     *BufferRef
	refCount int32
}

// Get 增加引用计数
func (b *BufferRef) Get() {
	atomic.AddInt32(&b.refCount, 1)
}

// Put 减少引用计数，当引用计数为0时可以回收内存
func (b *BufferRef) Put() {
	if atomic.AddInt32(&b.refCount, -1) == 0 {
		// 当引用计数为0时，可以将data放回内存池或直接丢弃
		b.data = nil
		b.next = nil
	}
}

// NewBufferRef 创建新的BufferRef实例
func NewBufferRef(data []byte) *BufferRef {
	return &BufferRef{
		data:     data,
		next:     nil,
		refCount: 1,
	}
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
			h.fccSetState(FCC_STATE_UNICAST_PENDING, "收到服务器响应 (FMT 3)")
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

		h.fccSetState(FCC_STATE_MCAST_REQUESTED, "收到同步通知 (FMT 4)，准备切换到组播")
		logger.LogPrintf("FCC (电信): 收到同步通知 (FMT 4)，准备切换到组播")

		// 启动同步超时计时器
		if h.fccSyncTimer != nil {
			h.fccSyncTimer.Stop()
		}
		h.fccSyncTimer = time.AfterFunc(5*time.Second, func() {
			h.Mu.Lock()
			if h.fccState == FCC_STATE_MCAST_REQUESTED {
				h.fccSetState(FCC_STATE_MCAST_ACTIVE, "同步超时，强制切换到组播")
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
			h.fccSetState(FCC_STATE_UNICAST_PENDING, "收到服务器响应 (FMT 6)")
			logger.LogPrintf("FCC (华为): 收到服务器响应 (FMT 6)")

			// 检查是否需要NAT穿越
			if len(data) >= 32 {
				flag := binary.BigEndian.Uint32(data[28:32])
				if flag&0x01000000 != 0 {
					h.fccSetState(FCC_STATE_UNICAST_ACTIVE, "需要NAT穿越")
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
			h.fccSetState(FCC_STATE_MCAST_REQUESTED, "收到同步通知 (FMT 8)，准备切换到组播")
			logger.LogPrintf("FCC (华为): 收到同步通知 (FMT 8)，准备切换到组播")

			// 启动同步超时计时器
			if h.fccSyncTimer != nil {
				h.fccSyncTimer.Stop()
			}
			h.fccSyncTimer = time.AfterFunc(5*time.Second, func() {
				h.Mu.Lock()
				if h.fccState == FCC_STATE_MCAST_REQUESTED {
					h.fccSetState(FCC_STATE_MCAST_ACTIVE, "同步超时，强制切换到组播")
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
			h.fccSetState(FCC_STATE_UNICAST_ACTIVE, "收到NAT穿越包 (FMT 12)")
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
	dnsServers := []string{"223.5.5.5:80", "223.6.6.6:80","8.8.8.8:80", "8.8.4.4:80"}

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
		
		// 初始化链表缓冲区
		h.fccPendingListHead = nil
		h.fccPendingListTail = nil
		
		// 初始化客户端状态更新通道
		if h.clientStateChan == nil {
			h.clientStateChan = make(chan int, 10) // 缓冲10个状态更新
		}
		
		h.fccSetState(FCC_STATE_INIT, "FCC启用")
		h.fccStartSeq = 0
		h.fccTermSeq = 0
		h.fccTermSent = false

		// 启动一个定时器，如果FCC在5秒内没有进展，则自动切换到多播模式
		go func() {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()

			select {
			case <-timer.C:
				h.Mu.Lock()
				if h.fccEnabled && h.fccState == FCC_STATE_INIT && !h.isClosed() {
					h.fccSetState(FCC_STATE_MCAST_ACTIVE, "初始化超时，直接切换到多播模式")
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
		
		// 清理链表缓冲区
		for h.fccPendingListHead != nil {
			bufRef := h.fccPendingListHead
			h.fccPendingListHead = bufRef.next
			bufRef.Put() // 减少引用计数
		}
		h.fccPendingListTail = nil
		
		h.fccSetState(FCC_STATE_INIT, "FCC禁用")
		h.fccStartSeq = 0
		h.fccTermSeq = 0
		h.fccTermSent = false

		// 清理PAT/PMT缓冲区
		if h.patBuffer != nil {
			patBufferPool.Put(h.patBuffer)
			h.patBuffer = nil
		}
		if h.pmtBuffer != nil {
			pmtBufferPool.Put(h.pmtBuffer)
			h.pmtBuffer = nil
		}
		
		// 停止并清理定时器
		if h.fccSyncTimer != nil {
			h.fccSyncTimer.Stop()
			h.fccSyncTimer = nil
		}
		
		// 关闭客户端状态更新通道
		if h.clientStateChan != nil {
			close(h.clientStateChan)
			h.clientStateChan = nil
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


// processFCCMediaData 处理FCC媒体数据
func (h *StreamHub) processFCCMediaData(data []byte) {
	h.Mu.Lock()
	
	// 如果是第一个单播数据包，切换到单播活动状态
	if h.fccState == FCC_STATE_UNICAST_PENDING {
		h.fccSetState(FCC_STATE_UNICAST_ACTIVE, "收到第一个单播数据包")
		logger.LogPrintf("FCC: 收到第一个单播数据包，切换到单播活动状态")
		
		// 获取起始序列号
		if len(data) >= 12 {
			h.fccStartSeq = binary.BigEndian.Uint16(data[2:4])
			logger.LogPrintf("FCC: 起始序列号为 %d", h.fccStartSeq)
		}
	}
	
	// 如果处于单播活动状态，处理数据
	if h.fccState == FCC_STATE_UNICAST_ACTIVE {
		// 将数据添加到FCC缓冲区（使用零拷贝链表）
		if len(data) > 0 {
			// 使用工厂函数创建BufferRef，确保正确的引用计数
			bufRef := NewBufferRef(data)
			if h.fccPendingListHead == nil {
				h.fccPendingListHead = bufRef
				h.fccPendingListTail = bufRef
			} else {
				h.fccPendingListTail.next = bufRef
				h.fccPendingListTail = bufRef
			}
		}
	}
	
	h.Mu.Unlock()
}

// prepareSwitchToMulticast 准备切换到多播模式
func (h *StreamHub) prepareSwitchToMulticast() {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 只有在单播活动状态下才能切换到多播请求状态
	if h.fccState != FCC_STATE_UNICAST_ACTIVE {
		return
	}

	h.fccSetState(FCC_STATE_MCAST_REQUESTED, "准备切换到多播模式")
	logger.LogPrintf("FCC: 准备切换到多播模式")

	// 设置终止序列号（起始序列号+缓冲区大小）
	h.fccTermSeq = h.fccStartSeq + uint16(h.fccCacheSize)
	logger.LogPrintf("FCC: 终止序列号设置为 %d (起始序列号 %d + 缓冲区大小 %d)", 
		h.fccTermSeq, h.fccStartSeq, h.fccCacheSize)

	// 发送FCC终止包
	if h.fccServerAddr != nil {
		go func() {
			err := h.sendFCCTermination(h.fccServerAddr, h.fccTermSeq)
			if err != nil {
				logger.LogPrintf("FCC: 发送终止包失败: %v", err)
			} else {
				h.Mu.Lock()
				h.fccTermSent = true
				h.Mu.Unlock()
				logger.LogPrintf("FCC: 终止包已发送，终止序列号 %d", h.fccTermSeq)
			}
		}()
	}

	// 启动同步超时计时器
	if h.fccSyncTimer != nil {
		h.fccSyncTimer.Stop()
	}
	h.fccSyncTimer = time.AfterFunc(5*time.Second, func() {
		h.Mu.Lock()
		if h.fccState == FCC_STATE_MCAST_REQUESTED {
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "同步超时，强制切换到多播")
			logger.LogPrintf("FCC: 同步超时，强制切换到多播模式")
		}
		h.Mu.Unlock()
	})
}


// handleMcastDataDuringTransition 处理多播过渡期间的数据
func (h *StreamHub) handleMcastDataDuringTransition(data []byte) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 如果已经处于多播活动状态，则直接处理
	if h.fccState == FCC_STATE_MCAST_ACTIVE {
		return
	}

	// 解析RTP包中的序列号
	if len(data) < 12 {
		return
	}

	// 获取当前序列号
	sequence := binary.BigEndian.Uint16(data[2:4])

	// 检查是否达到了终止序列号
	if h.fccTermSent && sequence >= h.fccTermSeq && h.fccState == FCC_STATE_MCAST_REQUESTED {
		// 切换到多播活动状态
		h.fccSetState(FCC_STATE_MCAST_ACTIVE, fmt.Sprintf("达到终止序列号 %d", sequence))
		logger.LogPrintf("FCC: 达到终止序列号 %d，切换到多播活动状态", sequence)

		// 停止定时器
		if h.fccSyncTimer != nil {
			h.fccSyncTimer.Stop()
			h.fccSyncTimer = nil
		}
	}
	
	// 将数据添加到FCC缓冲区（使用零拷贝链表）
	if len(data) > 0 {
		// 使用工厂函数创建BufferRef，确保正确的引用计数
		bufRef := NewBufferRef(data)
		if h.fccPendingListHead == nil {
			h.fccPendingListHead = bufRef
			h.fccPendingListTail = bufRef
		} else {
			h.fccPendingListTail.next = bufRef
			h.fccPendingListTail = bufRef
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

// fccSetState 统一的状态转换函数
func (h *StreamHub) fccSetState(newState int, reason string) bool {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	if h.fccState == newState {
		return false // 状态未改变
	}

	// 记录状态转换
	logger.LogPrintf("FCC: 状态从 %d 转换到 %d，原因: %s", h.fccState, newState, reason)
	
	h.fccState = newState

	// 如果有客户端状态更新通道，则发送状态更新
	if h.clientStateChan != nil {
		clientState := h.mapFccToClientState(newState)
		select {
		case h.clientStateChan <- clientState:
		default:
			// 非阻塞发送，如果通道满了就丢弃
		}
	}

	// 特殊状态处理
	switch newState {
	case FCC_STATE_MCAST_ACTIVE:
		// 清理FCC资源
		if h.fccPendingBuf != nil {
			h.fccPendingBuf.Reset()
		}
		
		// 清理链表缓冲区
		for h.fccPendingListHead != nil {
			bufRef := h.fccPendingListHead
			h.fccPendingListHead = bufRef.next
			bufRef.Put() // 减少引用计数
		}
		h.fccPendingListTail = nil
		
		// 停止并清理定时器
		if h.fccSyncTimer != nil {
			h.fccSyncTimer.Stop()
			h.fccSyncTimer = nil
		}
		
		h.fccStartSeq = 0
		h.fccTermSeq = 0
		h.fccTermSent = false
	}

	return true // 状态已改变
}

// mapFccToClientState 将FCC状态映射到客户端状态
func (h *StreamHub) mapFccToClientState(fccState int) int {
	fccToClientState := map[int]int{
		FCC_STATE_INIT:             CLIENT_STATE_FCC_INIT,
		FCC_STATE_REQUESTED:        CLIENT_STATE_FCC_REQUESTED,
		FCC_STATE_UNICAST_PENDING:  CLIENT_STATE_FCC_UNICAST_PENDING,
		FCC_STATE_UNICAST_ACTIVE:   CLIENT_STATE_FCC_UNICAST_ACTIVE,
		FCC_STATE_MCAST_REQUESTED:  CLIENT_STATE_FCC_MCAST_REQUESTED,
		FCC_STATE_MCAST_ACTIVE:     CLIENT_STATE_FCC_MCAST_ACTIVE,
		FCC_STATE_ERROR:            CLIENT_STATE_ERROR,
	}
	
	if clientState, ok := fccToClientState[fccState]; ok {
		return clientState
	}
	return CLIENT_STATE_ERROR
}
