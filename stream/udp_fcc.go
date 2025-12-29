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

func isRTCP205(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	v := (data[0] >> 6) & 0x03
	if v != 2 {
		return false
	}
	if data[1] != 205 {
		return false
	}
	length := int(binary.BigEndian.Uint16(data[2:4]))
	expected := 4 * (length + 1)
	if expected < 8 {
		return false
	}
	if len(data) < expected {
		return false
	}
	return true
}

func seqAfter(a, b uint16) bool {
	return int16(a-b) > 0
}

// BufferRef 用于零拷贝缓冲区管理
type BufferRef struct {
	data     []byte
	backing  []byte
	pool     *sync.Pool
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
		if b.pool != nil && b.backing != nil {
			b.pool.Put(b.backing)
		}
		b.data = nil
		b.backing = nil
		b.pool = nil
		b.next = nil
	}
}

// NewBufferRef 创建新的BufferRef实例
func NewBufferRef(data []byte) *BufferRef {
	return &BufferRef{
		data:     data,
		next:     nil,
		refCount: 0,
	}
}

// NewPooledBufferRef 创建绑定池的BufferRef实例（用于真零拷贝）
func NewPooledBufferRef(backing []byte, view []byte, pool *sync.Pool) *BufferRef {
	return &BufferRef{
		data:     view,
		backing:  backing,
		pool:     pool,
		next:     nil,
		refCount: 0,
	}
}

// processFCCPacket 处理FCC相关数据包
func (h *StreamHub) processFCCPacket(data []byte) bool {
	if !h.fccEnabled || len(data) < 8 {
		return false
	}

	if !isRTCP205(data) {
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
		h.Mu.RLock()
		should := h.fccState == FCC_STATE_REQUESTED
		h.Mu.RUnlock()
		if should {
			h.fccSetState(FCC_STATE_UNICAST_PENDING, "收到服务器响应 (FMT 3)")
			logger.LogPrintf("FCC (电信): 收到服务器响应 (FMT 3)")
		}
		return true

	case FCC_FMT_TELECOM_SYNC: // FMT 4 - 同步通知
		h.Mu.RLock()
		inMcast := h.fccState == FCC_STATE_MCAST_REQUESTED || h.fccState == FCC_STATE_MCAST_ACTIVE
		h.Mu.RUnlock()
		if inMcast {
			return true
		}
		h.fccSetState(FCC_STATE_MCAST_REQUESTED, "收到同步通知 (FMT 4)，准备切换到组播")
		logger.LogPrintf("FCC (电信): 收到同步通知 (FMT 4)，准备切换到组播")

		// 调用prepareSwitchToMulticast来准备切换到多播模式
		h.prepareSwitchToMulticast()
		return true

	default:
		return false
	}
}

// processHuaweiFCCPacket 处理华为FCC数据包
func (h *StreamHub) processHuaweiFCCPacket(fmtField byte, data []byte) bool {
	switch fmtField {
	case FCC_FMT_HUAWEI_RESP: // FMT 6 - 服务器响应
		h.Mu.RLock()
		should := h.fccState == FCC_STATE_REQUESTED
		h.Mu.RUnlock()
		if should {
			h.fccSetState(FCC_STATE_UNICAST_PENDING, "收到服务器响应 (FMT 6)")
			logger.LogPrintf("FCC (华为): 收到服务器响应 (FMT 6)")

			if len(data) >= 32 {
				flag := binary.BigEndian.Uint32(data[28:32])
				if flag&0x01000000 != 0 {
					h.fccSetState(FCC_STATE_UNICAST_ACTIVE, "需要NAT穿越")
					logger.LogPrintf("FCC (华为): 需要NAT穿越")
				}
			}
		}
		return true

	case FCC_FMT_HUAWEI_SYNC: // FMT 8 - 同步通知
		h.Mu.RLock()
		inMcast := h.fccState == FCC_STATE_MCAST_REQUESTED || h.fccState == FCC_STATE_MCAST_ACTIVE
		active := h.fccState == FCC_STATE_UNICAST_ACTIVE
		h.Mu.RUnlock()
		if inMcast {
			return true
		}
		if active {
			h.fccSetState(FCC_STATE_MCAST_REQUESTED, "收到同步通知 (FMT 8)，准备切换到组播")
			logger.LogPrintf("FCC (华为): 收到同步通知 (FMT 8)，准备切换到组播")
			h.prepareSwitchToMulticast()
			return true
		}
		return true

	case FCC_FMT_HUAWEI_NAT: // FMT 12 - NAT穿越包
		h.Mu.RLock()
		should := h.fccState == FCC_STATE_UNICAST_PENDING
		h.Mu.RUnlock()
		if should {
			h.fccSetState(FCC_STATE_UNICAST_ACTIVE, "收到NAT穿越包 (FMT 12)")
			logger.LogPrintf("FCC (华为): 收到NAT穿越包 (FMT 12)")
		}
		return true

	default:
		return false
	}
}

// 添加PAT/PMT缓冲区池以减少内存分配

// processFCCMediaBufRef 使用BufferRef（真零拷贝）处理FCC媒体数据
func (h *StreamHub) processFCCMediaBufRef(bufRef *BufferRef) {
    if bufRef == nil || bufRef.data == nil {
        logger.LogPrintf("FCC: 接收到空数据包")
        return
    }
    
    data := bufRef.data
    h.Mu.Lock()
    pending := h.fccState == FCC_STATE_UNICAST_PENDING
    if pending && len(data) >= 12 {
        h.fccStartSeq = binary.BigEndian.Uint16(data[2:4])
        logger.LogPrintf("FCC: 起始序列号为 %d", h.fccStartSeq)
    }
    h.Mu.Unlock()

    // 提取RTP有效载荷为TS帧视图（零拷贝）
    startOff, endOff, err := rtpPayloadGet(bufRef.data)
    if err == nil && startOff <= len(bufRef.data) && endOff <= len(bufRef.data) && startOff+endOff <= len(bufRef.data) {
        bufRef.data = bufRef.data[startOff : len(bufRef.data)-endOff]
    }

    if pending {
        h.fccSetState(FCC_STATE_UNICAST_ACTIVE, "收到第一个单播数据包")
        logger.LogPrintf("FCC: 收到第一个单播数据包，切换到单播活动状态")
    }

    h.Mu.Lock()
    // 如果处于单播活动状态，处理数据
    if h.fccState == FCC_STATE_UNICAST_ACTIVE {
        if len(data) > 0 {
            // 不重复增加引用，直接入链表
            if h.fccPendingListHead == nil {
                h.fccPendingListHead = bufRef
                h.fccPendingListTail = bufRef
            } else {
                h.fccPendingListTail.next = bufRef
                h.fccPendingListTail = bufRef
            }
            atomic.AddInt32(&h.fccPendingCount, 1)
        }

        // 检查是否应该终止FCC并切换到多播
        if len(data) >= 12 {
            currentSeq := binary.BigEndian.Uint16(data[2:4])

            if h.fccTermSent && seqAfter(currentSeq, h.fccTermSeq) {
                h.fccSetState(FCC_STATE_MCAST_ACTIVE, fmt.Sprintf("达到终止序列号 %d", currentSeq))
                logger.LogPrintf("FCC: 达到终止序列号 %d，切换到多播活动状态", currentSeq)
                if h.fccSyncTimer != nil {
                    h.fccSyncTimer.Stop()
                    h.fccSyncTimer = nil
                }
            }
        }
    }

    h.Mu.Unlock()
    // 检查是否需要切换到多播（统一调用）
    h.checkAndSwitchToMulticast()
}


// checkAndSwitchToMulticast 检查是否可以切换到多播并执行切换
func (h *StreamHub) checkAndSwitchToMulticast() {
	// 基于 BufferRef 链表统一判断与切换
	h.Mu.RLock()
	state := h.fccState
	pat := h.patBuffer
	pmt := h.pmtBuffer
	h.Mu.RUnlock()
	count := int(atomic.LoadInt32(&h.fccPendingCount))

	if state != FCC_STATE_UNICAST_ACTIVE {
		return
	}

	// 阈值：达到缓存大小的80%
	if count < int(float64(h.fccCacheSize)*0.8) {
		return
	}

	// 切换到请求状态
	h.fccSetState(FCC_STATE_MCAST_REQUESTED, "缓冲区接近满载，准备切换到多播模式")
	logger.LogPrintf("FCC: 缓冲区接近满载，准备切换到多播模式")

	// 启动切换定时器
	if h.fccSyncTimer != nil {
		h.fccSyncTimer.Stop()
	}
	h.fccSyncTimer = time.AfterFunc(3*time.Second, func() {
		h.Mu.RLock()
		inReq := h.fccState == FCC_STATE_MCAST_REQUESTED
		h.Mu.RUnlock()
		if inReq {
			// 先重发PAT/PMT
			if pat != nil {
				h.broadcast(pat)
			}
			if pmt != nil {
				h.broadcast(pmt)
			}
			// 分离链表
			var head *BufferRef
			h.Mu.Lock()
			head = h.fccPendingListHead
			h.fccPendingListHead = nil
			h.fccPendingListTail = nil
			h.Mu.Unlock()
			
			// 遍历链表，发送数据，类似C语言的处理方式
			for n := head; n != nil; n = n.next {
				h.broadcast(n.data)
				n.Put() // 减少引用计数
			}
			atomic.StoreInt32(&h.fccPendingCount, 0)
			// 切换到多播活跃
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "自动切换到多播模式")
			logger.LogPrintf("FCC: 自动切换到多播模式")
		}
	})
}

// prepareSwitchToMulticast 准备切换到多播模式，更接近C语言的实现
func (h *StreamHub) prepareSwitchToMulticast() {
	h.Mu.Lock()

	// 只有在单播活动状态下才能切换到多播请求状态
	if h.fccState != FCC_STATE_UNICAST_ACTIVE {
		h.Mu.Unlock()
		return
	}

	h.Mu.Unlock()
	h.fccSetState(FCC_STATE_MCAST_REQUESTED, "准备切换到多播模式")
	logger.LogPrintf("FCC: 准备切换到多播模式")

	// 设置终止序列号（起始序列号+缓冲区大小）
	h.Mu.Lock()
	h.fccTermSeq = h.fccStartSeq + uint16(h.fccCacheSize)
	logger.LogPrintf("FCC: 终止序列号设置为 %d (起始序列号 %d + 缓冲区大小 %d)",
		h.fccTermSeq, h.fccStartSeq, h.fccCacheSize)
	h.Mu.Unlock()

	// 发送FCC终止包
	if h.fccServerAddr != nil {
		go func() {
			// 使用当前序列号发送终止包，更接近C语言的实现
			h.Mu.Lock()
			currentSeq := h.fccTermSeq
			h.Mu.Unlock()
			
			err := h.sendFCCTermination(h.fccServerAddr, currentSeq)
			if err != nil {
				logger.LogPrintf("FCC: 发送终止包失败: %v", err)
			} else {
				h.Mu.Lock()
				h.fccTermSent = true
				h.Mu.Unlock()
				logger.LogPrintf("FCC: 终止包已发送，终止序列号 %d", currentSeq)
			}
		}()
	}

	// 启动同步超时计时器
	if h.fccSyncTimer != nil {
		h.fccSyncTimer.Stop()
	}
	h.fccSyncTimer = time.AfterFunc(5*time.Second, func() {
		h.Mu.RLock()
		inReq := h.fccState == FCC_STATE_MCAST_REQUESTED
		h.Mu.RUnlock()
		if inReq {
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "同步超时，强制切换到多播")
			logger.LogPrintf("FCC: 同步超时，强制切换到多播模式")
		}
	})
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
		// 初始化状态和参数

		// 初始化链表缓冲区
		h.fccPendingListHead = nil
		h.fccPendingListTail = nil
		atomic.StoreInt32(&h.fccPendingCount, 0)

		// 初始化客户端状态更新通道
		if h.clientStateChan == nil {
			h.clientStateChan = make(chan int, 10) // 缓冲10个状态更新
		}

		h.fccSetState(FCC_STATE_REQUESTED, "FCC启用并进入请求状态")
		h.fccStartSeq = 0
		h.fccTermSeq = 0
		h.fccTermSent = false

		// 启动一个定时器，如果FCC在5秒内没有进展，则自动切换到多播模式
		go func() {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()

			select {
			case <-timer.C:
				h.Mu.RLock()
				enabled := h.fccEnabled
				initState := h.fccState == FCC_STATE_INIT
				h.Mu.RUnlock()
				if enabled && initState && !h.isClosed() {
					h.fccSetState(FCC_STATE_MCAST_ACTIVE, "初始化超时，直接切换到多播模式")
					logger.LogPrintf("FCC: 初始化超时，直接切换到多播模式")
				}
			case <-h.Closed:
				// 如果hub关闭则退出
				return
			}
		}()
	} else {
		// 禁用FCC时清理相关资源

		// 清理链表缓冲区
		for h.fccPendingListHead != nil {
			bufRef := h.fccPendingListHead
			h.fccPendingListHead = bufRef.next
			bufRef.Put() // 减少引用计数
		}
		h.fccPendingListTail = nil
		atomic.StoreInt32(&h.fccPendingCount, 0)

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

	// 参数更新后无需重建缓冲区，统一使用链表BufferRef
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
	// 使用Hub级别的固定大小缓冲区池进行真零拷贝

	for {
		h.Mu.RLock()
		conn := h.fccUnicastConn
		h.Mu.RUnlock()

		// 检查hub是否已关闭
		if h.isClosed() || conn == nil {
			return
		}

		// 从池中获取缓冲区
		buf := h.fccUnicastBufPool.Get().([]byte)

		n, err := conn.Read(buf)
		if err != nil {
			// 将缓冲区返回到池中
			h.fccUnicastBufPool.Put(buf)

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
			bufRef := NewPooledBufferRef(buf, buf[:n], h.fccUnicastBufPool)
			bufRef.Get()
			h.handleFCCUnicastRef(bufRef)
		}

		// 将缓冲区返回到池中
		// 注意：成功读取的数据由BufferRef管理生命周期，
		// 不能在此处归还到池中。
	}
}

// handleFCCUnicastRef 处理FCC单播数据（真零拷贝版本）
func (h *StreamHub) handleFCCUnicastRef(bufRef *BufferRef) {
	h.Mu.Lock()
	currentState := h.fccState
	h.Mu.Unlock()

	switch currentState {
	case FCC_STATE_REQUESTED:
		if h.processFCCPacket(bufRef.data) {
			bufRef.Put()
			return
		}
		fallthrough

	case FCC_STATE_UNICAST_PENDING, FCC_STATE_UNICAST_ACTIVE:
		h.processFCCMediaBufRef(bufRef)

	case FCC_STATE_MCAST_REQUESTED:
		h.processFCCPacket(bufRef.data)
		h.processFCCMediaBufRef(bufRef)

	case FCC_STATE_MCAST_ACTIVE:
		bufRef.Put()
		return

	default:
		bufRef.Put()
		return
	}
}

// handleMcastDataDuringTransition 处理多播过渡期间的数据
func (h *StreamHub) handleMcastDataDuringTransition(data []byte) {
    if len(data) < 12 {
        logger.LogPrintf("FCC: 接收到过短的RTP包，长度: %d", len(data))
        return
    }

    h.Mu.Lock()

    // 如果已经处于多播活动状态，则直接处理
    if h.fccState == FCC_STATE_MCAST_ACTIVE {
        h.Mu.Unlock()
        return
    }

    // 解析RTP包中的序列号
    sequence := binary.BigEndian.Uint16(data[2:4])

    // 如果还没有发送终止消息，则发送
    if !h.fccTermSent {
        // 发送FCC终止包
        if h.fccServerAddr != nil {
            go func() {
                err := h.sendFCCTermination(h.fccServerAddr, sequence)
                if err != nil {
                    logger.LogPrintf("FCC: 发送终止包失败: %v", err)
                } else {
                    h.Mu.Lock()
                    h.fccTermSent = true
                    h.fccTermSeq = sequence // 记录终止序列号
                    h.Mu.Unlock()
                    logger.LogPrintf("FCC: 终止包已发送，序列号 %d", sequence)
                }
            }()
        }
    }

    // 检查是否达到了终止序列号
    shouldSwitch := h.fccTermSent && seqAfter(sequence, h.fccTermSeq) && h.fccState == FCC_STATE_MCAST_REQUESTED
    h.Mu.Unlock()
    if shouldSwitch {
        // 切换到多播活动状态
        h.fccSetState(FCC_STATE_MCAST_ACTIVE, fmt.Sprintf("达到终止序列号 %d", sequence))
        logger.LogPrintf("FCC: 达到终止序列号 %d，切换到多播活动状态", sequence)

        // 停止定时器
        h.Mu.Lock()
        if h.fccSyncTimer != nil {
            h.fccSyncTimer.Stop()
            h.fccSyncTimer = nil
        }
        h.Mu.Unlock()
        
        // 重发并清理链表缓存，采用类似C语言的处理方式，确保数据只发送一次
        var head *BufferRef
        h.Mu.Lock()
        head = h.fccPendingListHead
        h.fccPendingListHead = nil
        h.fccPendingListTail = nil
        h.Mu.Unlock()
        
        // 遍历链表，将数据发送到客户端，确保每个数据块只发送一次
        for n := head; n != nil; {
            if n.data != nil {
                h.broadcast(n.data)
            }
            next := n.next
            n.Put() // 减少引用计数，允许内存回收
            n = next
        }
        atomic.StoreInt32(&h.fccPendingCount, 0)
    }

    // 将数据添加到FCC缓冲区（使用零拷贝链表）
    if len(data) > 0 {
        // 创建BufferRef来管理数据生命周期，类似C语言的buffer_ref_t
        bufRef := NewBufferRef(data)
        bufRef.Get() // 增加引用计数，防止数据被提前释放
        
        h.Mu.Lock()
        // 添加到链表尾部，确保数据顺序
        if h.fccPendingListHead == nil {
            h.fccPendingListHead = bufRef
            h.fccPendingListTail = bufRef
        } else {
            h.fccPendingListTail.next = bufRef
            h.fccPendingListTail = bufRef
        }
        h.Mu.Unlock()
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
	// 在锁外进行状态重置，避免持锁调用状态机
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
	h.Mu.Unlock()
	// 清理链表缓冲区
	for h.fccPendingListHead != nil {
		bufRef := h.fccPendingListHead
		h.fccPendingListHead = bufRef.next
		bufRef.Put()
	}
	h.fccPendingListTail = nil
	atomic.StoreInt32(&h.fccPendingCount, 0)
	h.fccSetState(FCC_STATE_INIT, "cleanupFCC")
	h.Mu.Lock()
}

// fccSetState 设置FCC状态，遵循外部加锁原则
// 注意：调用方必须确保已持有h.Mu锁
func (h *StreamHub) fccSetState(newState int, reason string) {
	if h.fccState == newState {
		return
	}
	
	oldState := h.fccState
	h.fccState = newState
	
	// 记录状态转换日志
	stateNames := []string{
		"INIT", "REQUESTED", "UNICAST_PENDING",
		"UNICAST_ACTIVE", "MCAST_REQUESTED", "MCAST_ACTIVE", "ERROR",
	}
	
	if newState >= 0 && newState < len(stateNames) {
		logger.LogPrintf("FCC State: %s -> %s (%s)", stateNames[oldState], stateNames[newState], reason)
	} else {
		logger.LogPrintf("FCC State: %d -> %d (%s)", oldState, newState, reason)
	}
}

