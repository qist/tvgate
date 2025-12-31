package stream

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	
	"github.com/qist/tvgate/logger"
)

// makeTelecomFCCPacket 创建电信格式的FCC包
func makeTelecomFCCPacket(fmtType int, seqNum uint16, srcPort, dstPort uint16) []byte {
	buf := make([]byte, 24)
	binary.BigEndian.PutUint32(buf[0:4], 0x5A5A5A5A) // Magic Number
	binary.BigEndian.PutUint32(buf[4:8], 0x00000001) // Version & Length
	buf[8] = byte(fmtType)                           // Format Type
	buf[9] = 0                                       // Reserved
	binary.BigEndian.PutUint16(buf[10:12], seqNum)   // Sequence Number
	binary.BigEndian.PutUint16(buf[12:14], srcPort)  // Source Port
	binary.BigEndian.PutUint16(buf[14:16], dstPort)  // Dest Port
	// The rest can be zero or filled with other necessary info
	return buf
}

// makeHuaweiFCCPacket 创建华为格式的FCC包
func makeHuaweiFCCPacket(fmtType int, seqNum uint16, srcPort, dstPort uint16) []byte {
	// 简化版华为FCC包，实际格式可能更复杂
	buf := make([]byte, 20)
	copy(buf[0:4], []byte("HWCN"))
	binary.BigEndian.PutUint16(buf[4:6], uint16(fmtType))
	binary.BigEndian.PutUint16(buf[6:8], 20) // Packet Length
	binary.BigEndian.PutUint32(buf[8:12], 0) // Reserved
	binary.BigEndian.PutUint16(buf[12:14], seqNum)
	binary.BigEndian.PutUint16(buf[14:16], srcPort)
	binary.BigEndian.PutUint16(buf[16:18], dstPort)
	// The rest can be zero or filled with other necessary info
	return buf
}

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
			h.Mu.Unlock()
			
			// 发送终止包并切换到多播
			h.sendFCCSwitchToMulticast()
			return true
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
		h.Mu.Unlock()
		
		// 调用prepareSwitchToMulticast来准备切换到多播模式
		h.prepareSwitchToMulticast()
		return true

	default:
		return false
	}
}

// processFCCMediaBufRef 处理FCC媒体数据，实现更精确的状态切换逻辑
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
        
        // 启动单播超时切换定时器 - 对应C代码中的FCC_TIMEOUT_UNICAST_SEC
        h.startFCCUnicastTimeoutTimer()
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
                // 停止单播超时定时器
                h.stopFCCUnicastTimeoutTimer()
            }
        }
    }

    h.Mu.Unlock()
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
		bufRef := NewBufferRef(data)
		bufRef.Get() // 增加引用计数，防止数据被提前释放
		h.Mu.Lock()
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


// prepareSwitchToMulticast 准备切换到多播模式
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

	// 启动同步超时计时器 - 仅作为备用机制，不是主要切换方式
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
	pk[0] = 0x80 | 5                       // Version 2, Padding 0, FMT 5
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
	pk[0] = 0x80 | 9                       // V=2, P=0, FMT=9
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
		// 初始化状态和参数，但不直接设置为REQUESTED状态
		// 状态转换将在ServeHTTP中根据是否有服务器地址来决定

		// 初始化链表缓冲区
		h.fccPendingListHead = nil
		h.fccPendingListTail = nil
		atomic.StoreInt32(&h.fccPendingCount, 0)

		// 初始化客户端状态更新通道
		if h.clientStateChan == nil {
			h.clientStateChan = make(chan int, 10) // 缓冲10个状态更新
		}

		// 只有在有服务器地址时才设置为REQUESTED状态，否则保持INIT状态
		if h.fccServerAddr != nil {
			h.fccSetState(FCC_STATE_REQUESTED, "FCC启用并进入请求状态")
			h.fccStartSeq = 0
			h.fccTermSeq = 0
			h.fccTermSent = false

			// 在ServeHTTP中会调用initFCCConnection和sendFCCRequest
		}
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

// IsFccActive 检查FCC是否处于活动状态（已切换到多播）
func (h *StreamHub) IsFccActive() bool {
	h.Mu.RLock()
	defer h.Mu.RUnlock()
	return h.fccState == FCC_STATE_MCAST_ACTIVE
}

// GetFccStateInfo 获取FCC状态信息
func (h *StreamHub) GetFccStateInfo() map[string]interface{} {
	h.Mu.RLock()
	defer h.Mu.RUnlock()
	
	stateNames := map[int]string{
		FCC_STATE_INIT:          "INIT",
		FCC_STATE_REQUESTED:     "REQUESTED",
		FCC_STATE_UNICAST_PENDING: "UNICAST_PENDING",
		FCC_STATE_UNICAST_ACTIVE:  "UNICAST_ACTIVE",
		FCC_STATE_MCAST_REQUESTED: "MCAST_REQUESTED",
		FCC_STATE_MCAST_ACTIVE:    "MCAST_ACTIVE",
		FCC_STATE_ERROR:           "ERROR",
	}
	
	stateName, exists := stateNames[h.fccState]
	if !exists {
		stateName = fmt.Sprintf("UNKNOWN(%d)", h.fccState)
	}
	
	return map[string]interface{}{
		"enabled":     h.fccEnabled,
		"state":       h.fccState,
		"state_name":  stateName,
		"fcc_type":    h.fccType,
		"cache_size":  h.fccCacheSize,
		"start_seq":   h.fccStartSeq,
		"term_seq":    h.fccTermSeq,
		"term_sent":   h.fccTermSent,
		"pending_cnt": atomic.LoadInt32(&h.fccPendingCount),
		"server_addr": h.fccServerAddr,
	}
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
	defer h.Mu.Unlock()

	// 如果FCC连接已存在，直接返回成功
	if h.fccUnicastConn != nil {
		return true
	}

	// 创建UDP地址用于监听FCC单播数据
	addr, err := net.ResolveUDPAddr("udp", ":0") // 使用系统分配的随机端口
	if err != nil {
		logger.LogPrintf("FCC: 解析UDP地址失败: %v", err)
		return false
	}

	// 创建UDP连接
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		logger.LogPrintf("FCC: 创建UDP连接失败: %v", err)
		return false
	}

	// 获取分配的端口
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	h.fccUnicastPort = localAddr.Port

	// 保存连接
	h.fccUnicastConn = conn

	logger.LogPrintf("FCC单播连接已初始化，监听端口: %d", h.fccUnicastPort)
	
	// 启动FCC超时检测定时器
	h.startFCCTimeoutTimer()
	
	return true
}

// startFCCTimeoutTimer 启动FCC请求超时检测定时器
func (h *StreamHub) startFCCTimeoutTimer() {
	// 如果已有定时器在运行，先停止
	if h.fccTimeoutTimer != nil {
		h.fccTimeoutTimer.Stop()
		h.fccTimeoutTimer = nil
	}

	// 创建新的定时器 - 使用C代码中的80ms超时时间
	h.fccTimeoutTimer = time.AfterFunc(80*time.Millisecond, func() {
		h.Mu.Lock()
		currentState := h.fccState
		// 在超时后停止定时器，避免重复触发
		if h.fccTimeoutTimer != nil {
			h.fccTimeoutTimer.Stop()
			h.fccTimeoutTimer = nil
		}
		h.Mu.Unlock()
		
		// 如果在超时时间内仍处于REQUESTED状态，说明没有收到服务器响应
		if currentState == FCC_STATE_REQUESTED {
			logger.LogPrintf("FCC: 服务器无响应，超时(80ms)未收到响应 - 服务器地址可能无效或网络不通")
			
			// 更新状态为MCAST_ACTIVE，降级到组播播放，而不是错误状态
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC服务器无响应，降级到组播播放")
		}
	})
}

// sendFCCRequest 发送FCC请求
func (h *StreamHub) sendFCCRequest(targetAddr *net.UDPAddr, unicastPort int) error {
	h.Mu.RLock()
	fccType := h.fccType
	fccUnicastConn := h.fccUnicastConn
	fccServerAddr := h.fccServerAddr
	h.Mu.RUnlock()

	if fccUnicastConn == nil || fccServerAddr == nil {
		return fmt.Errorf("fcc connection not initialized")
	}

	var pkt []byte
	switch fccType {
	case FCC_TYPE_TELECOM:
		pkt = makeTelecomFCCPacket(FCC_FMT_TELECOM_REQ, 0, uint16(unicastPort), uint16(h.fccUnicastPort))
	case FCC_TYPE_HUAWEI:
		pkt = makeHuaweiFCCPacket(FCC_FMT_HUAWEI_REQ, 0, uint16(unicastPort), uint16(h.fccUnicastPort))
	default:
		return fmt.Errorf("unsupported fcc type: %d", fccType)
	}

	// 发送请求
	_, err := fccUnicastConn.WriteToUDP(pkt, fccServerAddr)
	if err != nil {
		logger.LogPrintf("FCC请求发送失败: %v", err)
		return err
	}

	// 记录发送成功日志，包含目标服务器地址信息
	logger.LogPrintf("FCC请求已发送到 %s，端口 %d", fccServerAddr.String(), unicastPort)
	logger.LogPrintf("FCC请求已发送到 %s 用于客户端 %s", fccServerAddr.String(), targetAddr.IP.String())
	return nil
}

// processFCCUnicastData 处理FCC单播数据
func (h *StreamHub) processFCCUnicastData() {
	h.Mu.RLock()
	conn := h.fccUnicastConn
	h.Mu.RUnlock()

	if conn == nil {
		logger.LogPrintf("FCC: processFCCUnicastData被调用但连接未初始化")
		return
	}

	logger.LogPrintf("FCC: 开始处理单播数据，监听端口 %d", h.fccUnicastPort)

	// 从单播连接读取数据
	buf := make([]byte, 1600) // 足够大的缓冲区以处理单个数据包
	for {
		// 检查Hub是否已关闭
		select {
		case <-h.Closed:
			logger.LogPrintf("FCC: Hub已关闭，停止处理单播数据")
			return
		default:
		}

		// 读取单播数据
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") {
				logger.LogPrintf("FCC: 读取单播数据失败: %v", err)
			}
			return
		}

		logger.LogPrintf("FCC: 从 %s 接收到 %d 字节数据", addr.String(), n)

		// 复制数据以避免覆盖
		data := make([]byte, n)
		copy(data, buf[:n])

		// 检查FCC是否仍然启用
		h.Mu.RLock()
		fccEnabled := h.fccEnabled
		h.Mu.RUnlock()

		if !fccEnabled {
			continue // 如果FCC已禁用，跳过处理
		}

		// 处理接收到的FCC单播数据
		h.processFCCMediaData(data)
		
		// 检查是否为FCC响应包，如果是则更新状态
		h.handleFCCResponse(data, addr)
	}
}

// processFCCMediaData 处理FCC媒体数据，接收字节数组作为参数
func (h *StreamHub) processFCCMediaData(data []byte) {
	// 将字节数组转换为BufferRef以便使用现有的处理逻辑
	// 创建一个临时的BufferRef，不使用池（因为数据是直接传入的）
	bufRef := &BufferRef{
		data:     data,
		backing:  nil, // 不使用池
		pool:     nil, // 不使用池
		next:     nil,
		refCount: 1,
	}
	h.processFCCMediaBufRef(bufRef)
}

// handleFCCResponse 处理FCC响应包并更新状态
func (h *StreamHub) handleFCCResponse(data []byte, addr *net.UDPAddr) {
	// 检查数据包是否为有效的FCC响应
	if len(data) < 4 {
		return
	}

	// 检查FCC响应标识
	isFCCResponse := false
	fccType := FCC_TYPE_TELECOM // 默认为电信类型
	
	// 检查是否为电信格式响应
	if len(data) >= 24 && 
		binary.BigEndian.Uint32(data[0:4]) == 0x5A5A5A5A && 
		data[8] >= FCC_FMT_TELECOM_RESP && data[8] <= FCC_FMT_TELECOM_SYNC {
		isFCCResponse = true
		fccType = FCC_TYPE_TELECOM
	} else if len(data) >= 20 && 
		string(data[0:4]) == "HWCN" && 
		binary.BigEndian.Uint16(data[4:6]) >= FCC_FMT_HUAWEI_RESP && 
		binary.BigEndian.Uint16(data[4:6]) <= FCC_FMT_HUAWEI_SYNC {
		isFCCResponse = true
		fccType = FCC_TYPE_HUAWEI
	}

	if isFCCResponse {
		h.Mu.Lock()
		// 更新FCC服务器地址
		h.fccServerAddr = addr
		h.fccType = fccType
		h.Mu.Unlock()

		// 根据响应类型更新状态
		var responseType string
		if fccType == FCC_TYPE_TELECOM {
			responseType = fmt.Sprintf("电信格式响应 (类型: %d)", data[8])
		} else {
			responseType = fmt.Sprintf("华为格式响应 (类型: %d)", binary.BigEndian.Uint16(data[4:6]))
		}
		
		logger.LogPrintf("FCC: 收到有效服务器响应 from %s, 类型: %s", addr.String(), responseType)
		
		// 检查当前状态是否为REQUESTED，如果是则更新为UNICAST_PENDING（等待第一个单播数据包）
		h.Mu.RLock()
		currentState := h.fccState
		h.Mu.RUnlock()
		
		if currentState == FCC_STATE_REQUESTED {
			h.fccSetState(FCC_STATE_UNICAST_PENDING, fmt.Sprintf("收到服务器响应，等待第一个单播数据包 - 服务器: %s", addr.String()))
		}
	}
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

// fccSetState 设置FCC状态并记录状态转换日志
func (h *StreamHub) fccSetState(state int, reason string) {
	h.Mu.Lock()
	prevState := h.fccState
	h.fccState = state
	
	// 如果状态变为非请求状态，停止超时定时器
	if state != FCC_STATE_REQUESTED && h.fccTimeoutTimer != nil {
		h.fccTimeoutTimer.Stop()
		h.fccTimeoutTimer = nil
	}
	h.Mu.Unlock()

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

	prevName := stateNames[prevState]
	currentName := stateNames[state]
	if prevName == "" {
		prevName = "UNKNOWN"
	}
	if currentName == "" {
		currentName = "UNKNOWN"
	}

	logger.LogPrintf("FCC State: %s -> %s (%s)", prevName, currentName, reason)
}

// sendFCCSwitchToMulticast 发送FCC切换到多播的请求
func (h *StreamHub) sendFCCSwitchToMulticast() {
	// 发送FCC终止包
	if h.fccServerAddr != nil {
		go func() {
			h.Mu.Lock()
			currentSeq := h.fccStartSeq + uint16(h.fccCacheSize)
			h.fccTermSeq = currentSeq
			h.fccTermSent = true
			h.Mu.Unlock()
			
			err := h.sendFCCTermination(h.fccServerAddr, currentSeq)
			if err != nil {
				logger.LogPrintf("FCC: 发送终止包失败: %v", err)
			} else {
				logger.LogPrintf("FCC: 终止包已发送，终止序列号 %d", currentSeq)
			}
		}()
	}

	// 切换到多播活动状态
	h.fccSetState(FCC_STATE_MCAST_ACTIVE, "发送终止包后切换到多播")
	logger.LogPrintf("FCC: 已切换到多播活动状态")

}

// sendFCCTermination 发送FCC终止包
func (h *StreamHub) sendFCCTermination(targetAddr *net.UDPAddr, seqNum uint16) error {
	h.Mu.RLock()
	fccType := h.fccType
	fccUnicastConn := h.fccUnicastConn
	fccServerAddr := h.fccServerAddr
	h.Mu.RUnlock()

	if fccUnicastConn == nil || fccServerAddr == nil {
		return fmt.Errorf("fcc connection not initialized")
	}

	var pkt []byte
	switch fccType {
	case FCC_TYPE_TELECOM:
		pkt = makeTelecomFCCPacket(FCC_FMT_TELECOM_TERM, seqNum, 0, 0)
	case FCC_TYPE_HUAWEI:
		pkt = makeHuaweiFCCPacket(FCC_FMT_HUAWEI_TERM, seqNum, 0, 0)
	default:
		return fmt.Errorf("unsupported fcc type: %d", fccType)
	}

	_, err := fccUnicastConn.WriteToUDP(pkt, fccServerAddr)
	if err != nil {
		logger.LogPrintf("FCC终止包发送失败到 %s: %v", targetAddr.String(), err)
		return err
	}

	logger.LogPrintf("FCC终止包已发送到 %s，序列号 %d", targetAddr.String(), seqNum)
	return nil
}

// startFCCUnicastTimeoutTimer 启动FCC单播超时切换定时器
func (h *StreamHub) startFCCUnicastTimeoutTimer() {
    // 停止现有定时器
    h.stopFCCUnicastTimeoutTimer()
    
    // 启动新的1秒超时定时器，对应C代码中的FCC_TIMEOUT_UNICAST_SEC
    h.fccUnicastTimer = time.AfterFunc(time.Second, func() {
        h.Mu.Lock()
        defer h.Mu.Unlock()
        
        // 如果仍在UNICAST_ACTIVE状态，切换到MCAST_ACTIVE
        if h.fccState == FCC_STATE_UNICAST_ACTIVE {
            logger.LogPrintf("FCC: 单播超时(1秒)，切换到多播活动状态")
            h.fccSetState(FCC_STATE_MCAST_ACTIVE, "单播超时，切换到多播活动状态")
            
            if h.fccSyncTimer != nil {
                h.fccSyncTimer.Stop()
                h.fccSyncTimer = nil
            }
        }
    })
}

// stopFCCUnicastTimeoutTimer 停止FCC单播超时切换定时器
func (h *StreamHub) stopFCCUnicastTimeoutTimer() {
    if h.fccUnicastTimer != nil {
        h.fccUnicastTimer.Stop()
        h.fccUnicastTimer = nil
    }
}
