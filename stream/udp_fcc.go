package stream

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)


// makeTelecomFCCPacket 创建电信格式的FCC包
func makeTelecomFCCPacket(fmtType int, seqNum uint16, srcPort, dstPort uint16) []byte {
	buf := make([]byte, 24)
	binary.BigEndian.PutUint32(buf[0:4], 0x5A5A5A5A) // 魔数
	binary.BigEndian.PutUint32(buf[4:8], 0x00000001) // 版本和长度
	buf[8] = byte(fmtType)                           // 格式类型
	buf[9] = 0                                       // 保留
	binary.BigEndian.PutUint16(buf[10:12], seqNum)   // 序列号
	binary.BigEndian.PutUint16(buf[12:14], srcPort)  // 源端口
	binary.BigEndian.PutUint16(buf[14:16], dstPort)  // 目标端口
	// 其余部分可以为零或填充其他必要信息
	return buf
}

// makeHuaweiFCCPacket 创建华为格式的FCC包
func makeHuaweiFCCPacket(fmtType int, seqNum uint16, srcPort, dstPort uint16) []byte {
	// 简化版华为FCC包，实际格式可能更复杂
	buf := make([]byte, 20)
	copy(buf[0:4], []byte("HWCN"))
	binary.BigEndian.PutUint16(buf[4:6], uint16(fmtType))
	binary.BigEndian.PutUint16(buf[6:8], 20) // 包长度
	binary.BigEndian.PutUint32(buf[8:12], 0) // 保留
	binary.BigEndian.PutUint16(buf[12:14], seqNum)
	binary.BigEndian.PutUint16(buf[14:16], srcPort)
	binary.BigEndian.PutUint16(buf[16:18], dstPort)
	// 其余部分可以为零或填充其他必要信息
	return buf
}

func isRTCP205(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	
	// 检查版本号：(data[0] >> 6) & 0x03 必须等于2
	v := (data[0] >> 6) & 0x03
	if v != 2 {
		return false
	}
	
	// 验证PT字段：必须为205 (Generic RTP Feedback)
	if data[1] != 205 {
		return false
	}
	
	// 校验长度字段：由length field计算出的包长度
	length := int(binary.BigEndian.Uint16(data[2:4]))
	expected := 4 * (length + 1)
	
	// 验证计算出的包长度是否在合理范围内
	if expected < 8 || expected > len(data) {
		return false
	}
	
	return true
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

	// 根据FCC类型处理不同的FMTL
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
		// 如果已经使用多播流则忽略
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
		// 如果已经使用多播流则忽略
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

		// 如果fccCacheSize等参数没有初始化，则从配置加载
		if h.fccCacheSize == 0 {
			config.CfgMu.RLock()
			h.fccCacheSize = config.Cfg.Server.FccCacheSize
			h.fccPortMin = config.Cfg.Server.FccListenPortMin
			h.fccPortMax = config.Cfg.Server.FccListenPortMax
			h.fccType = FCC_TYPE_TELECOM // 默认为电信类型
			fccTypeStr := config.Cfg.Server.FccType
			config.CfgMu.RUnlock()
			
			// 设置默认值
			if h.fccCacheSize <= 0 {
				h.fccCacheSize = 16384
			}
			if h.fccPortMin == 0 {
				h.fccPortMin = 50000
			}
			if h.fccPortMax == 0 {
				h.fccPortMax = 60000
			}
			
			// 确定FCC类型
			switch fccTypeStr {
			case "huawei":
				h.fccType = FCC_TYPE_HUAWEI
			case "telecom":
				h.fccType = FCC_TYPE_TELECOM
			}
		}

		// 初始化链表缓冲区（保留用于向后兼容）
		h.fccPendingListHead = nil
		h.fccPendingListTail = nil
		atomic.StoreInt32(&h.fccPendingCount, 0)

		// 初始化客户端状态更新通道
		if h.clientStateChan == nil {
			h.clientStateChan = make(chan int, 10) // 缓冲10个状态更新
		}

		// 更新最后FCC数据时间
		h.lastFccDataTime = time.Now().UnixNano() / 1e6

		// 只有在有服务器地址时才设置为REQUESTED状态，否则保持INIT状态
		if h.fccServerAddr != nil {
			h.fccSetState(FCC_STATE_REQUESTED, "FCC启用并进入请求状态")

			// 在ServeHTTP中会调用initFCCConnection和sendFCCRequest
		}
	} else {
		// 禁用FCC时清理相关资源

		// 清理链表缓冲区（保留用于向后兼容）
		for h.fccPendingListHead != nil {
			bufRef := h.fccPendingListHead
			h.fccPendingListHead = bufRef.next
			bufRef.Put() // 减少引用计数
		}
		h.fccPendingListTail = nil
		atomic.StoreInt32(&h.fccPendingCount, 0)

		h.fccSetState(FCC_STATE_INIT, "FCC禁用")

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
	
	stateName := fccStateToString(h.fccState)
	
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
func (h *StreamHub) SetFccServerAddr(addr *net.UDPAddr) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	h.fccServerAddr = addr

	// 如果FCC已启用且没有处于请求状态，则设置为请求状态
	if h.fccEnabled && h.fccState == FCC_STATE_INIT {
		h.fccSetState(FCC_STATE_REQUESTED, "FCC服务器地址设置，进入请求状态")

		// 在ServeHTTP中会调用initFCCConnection和sendFCCRequest
	}
}

// GetFccServerAddr 获取FCC服务器地址
func (h *StreamHub) GetFccServerAddr() *net.UDPAddr {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	return h.fccServerAddr
}

// initFCCConnectionForClient 为客户端初始化FCC连接
func (h *StreamHub) initFCCConnectionForClient() (*net.UDPConn, error) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 随机选择端口范围内的端口
	portRange := h.fccPortMax - h.fccPortMin + 1
	if portRange <= 0 {
		return nil, fmt.Errorf("无效的FCC端口范围: min=%d, max=%d", h.fccPortMin, h.fccPortMax)
	}

	// 尝试创建UDP连接
	var conn *net.UDPConn
	var err error
	maxAttempts := 100 // 最大尝试次数

	for i := 0; i < maxAttempts; i++ {
		// 随机选择端口
		port := h.fccPortMin + (time.Now().Nanosecond() + i) % portRange
		addr := &net.UDPAddr{Port: port}

		conn, err = net.ListenUDP("udp", addr)
		if err == nil {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("无法创建FCC连接: %v", err)
	}

	logger.LogPrintf("FCC单播连接已初始化，监听端口: %d", conn.LocalAddr().(*net.UDPAddr).Port)
	return conn, nil
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

// sendFCCRequestWithConn 发送FCC请求使用客户端自己的连接
func (h *StreamHub) sendFCCRequestWithConn(fccConn *net.UDPConn, multicastAddr *net.UDPAddr, clientPort int, fccServerAddr *net.UDPAddr) error {
	if fccConn == nil || fccServerAddr == nil {
		return fmt.Errorf("fcc connection or server address is nil")
	}

	var pkt []byte
	h.Mu.RLock()
	fccType := h.fccType
	fccCacheSize := h.fccCacheSize
	h.Mu.RUnlock()

	switch fccType {
	case FCC_TYPE_TELECOM:
		pkt = makeTelecomFCCRequest(multicastAddr.IP, uint16(multicastAddr.Port), uint16(clientPort), uint16(fccCacheSize))
	case FCC_TYPE_HUAWEI:
		pkt = makeHuaweiFCCRequest(multicastAddr.IP, uint16(multicastAddr.Port), uint16(clientPort), uint16(fccCacheSize))
	default:
		return fmt.Errorf("unsupported fcc type: %d", fccType)
	}

	_, err := fccConn.WriteToUDP(pkt, fccServerAddr)
	if err != nil {
		logger.LogPrintf("FCC请求发送失败到 %s: %v", fccServerAddr.String(), err)
		return err
	}

	logger.LogPrintf("FCC请求已发送到 %s，端口 %d", fccServerAddr.String(), clientPort)
	logger.LogPrintf("FCC请求已发送到 %s 用于客户端 %s", multicastAddr.String(), multicastAddr.IP.String())

	// 启动超时定时器，处理FCC请求的响应超时
	h.startFCCTimeoutTimer()

	return nil
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

// ClientFccState 存储每个客户端的FCC状态信息
type ClientFccState struct {
	state             int
	startSeq          uint16
	termSeq           uint16
	termSent          bool
	pendingListHead   *BufferRef
	pendingListTail   *BufferRef
	pendingCount      int32
	lastFccDataTime   int64
	unicastStartTime  time.Time
}

// NewClientFccState 创建新的客户端FCC状态
func NewClientFccState() *ClientFccState {
	return &ClientFccState{
		state:           FCC_STATE_INIT,
		startSeq:        0,
		termSeq:         0,
		termSent:        false,
		pendingListHead: nil,
		pendingListTail: nil,
		pendingCount:    0,
		lastFccDataTime: time.Now().UnixNano() / 1e6,
	}
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

// fccStateToString 将FCC状态转换为字符串
func fccStateToString(state int) string {
	switch state {
	case FCC_STATE_INIT:
		return "INIT"
	case FCC_STATE_REQUESTED:
		return "REQUESTED"
	case FCC_STATE_UNICAST_PENDING:
		return "UNICAST_PENDING"
	case FCC_STATE_UNICAST_ACTIVE:
		return "UNICAST_ACTIVE"
	case FCC_STATE_MCAST_REQUESTED:
		return "MCAST_REQUESTED"
	case FCC_STATE_MCAST_ACTIVE:
		return "MCAST_ACTIVE"
	default:
		return "UNKNOWN"
	}
}

// makeTelecomFCCRequest 构建电信FCC请求包
func makeTelecomFCCRequest(multicastIP net.IP, multicastPort uint16, clientPort uint16, cacheSize uint16) []byte {
	pk := make([]byte, 24)
	
	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_TELECOM_REQ // Version 2, Padding 0, FMT 2
	pk[1] = 205                       // Type: Generic RTP Feedback (205)
	lenWords := uint16(5)              // Length in 32-bit words minus 1
	binary.BigEndian.PutUint16(pk[2:4], lenWords)
	// pk[4-7]: Sender SSRC = 0 (already zeroed by make)

	// Media source SSRC (4 bytes) - multicast IP address
	ip := multicastIP.To4()
	if ip != nil {
		binary.BigEndian.PutUint32(pk[8:12], binary.LittleEndian.Uint32(ip))
	}

	// FCI - Feedback Control Information
	// pk[12-15]: Version 0, Reserved 3 bytes (already zeroed)
	binary.BigEndian.PutUint16(pk[16:18], clientPort) // FCC client port
	binary.BigEndian.PutUint16(pk[18:20], multicastPort) // Mcast group port
	// pk[20-24]: Mcast group IP (already zeroed for test)
	if ip != nil {
		copy(pk[20:24], ip)
	}

	return pk
}

// makeHuaweiFCCRequest 构建华为FCC请求包
func makeHuaweiFCCRequest(multicastIP net.IP, multicastPort uint16, clientPort uint16, cacheSize uint16) []byte {
	pk := make([]byte, 32)
	
	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_HUAWEI_REQ // Version 2, Padding 0, FMT 5
	pk[1] = 205                       // Type: Generic RTP Feedback (205)
	binary.BigEndian.PutUint16(pk[2:4], 7) // Length = 8 words - 1 = 7
	// pk[4-7]: Sender SSRC = 0 (already zeroed by make)

	// Media source SSRC (4 bytes) - multicast IP address
	ip := multicastIP.To4()
	if ip != nil {
		binary.BigEndian.PutUint32(pk[8:12], binary.LittleEndian.Uint32(ip))
	}

	// FCI - Status byte and sequence number (4 bytes)
	pk[12] = 0x00                                    // Status: initializing
	binary.BigEndian.PutUint16(pk[14:16], 0)        // Sequence number
	// Additional fields for Huawei FCC
	binary.BigEndian.PutUint32(pk[16:20], 0)        // Local IP placeholder
	binary.BigEndian.PutUint16(pk[20:22], clientPort) // Client port
	// pk[22-24]: reserved
	binary.BigEndian.PutUint16(pk[24:26], multicastPort) // Multicast port
	// pk[26-32]: reserved

	return pk
}
