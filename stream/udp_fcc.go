package stream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

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

// Get 增加引用数
func (b *BufferRef) Get() {
	atomic.AddInt32(&b.refCount, 1)
}

// Put 减少引用数，当引用数为0时可以回收内存
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

	// 更新FCC活动时间
	h.updateFCCActivity()

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
				if flag&0x20000000 != 0 { // 检查NAT穿越标志
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
			h.prepareSwitchToMulticast()
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

// handleMcastDataDuringTransition 处理过渡期间的多播数据
func (h *StreamHub) handleMcastDataDuringTransition(data []byte) {
	if len(data) == 0 {
		return
	}

	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 检查当前状态是否为MCAST_REQUESTED
	if h.fccState != FCC_STATE_MCAST_REQUESTED {
		return
	}

	// 创建新的BufferRef并增加引用计数
	bufRef := NewBufferRef(data)
	bufRef.Get() // 增加引用计数，防止数据被提前释放

	// 将数据添加到等待列表
	if h.fccPendingListHead == nil {
		h.fccPendingListHead = bufRef
		h.fccPendingListTail = bufRef
	} else {
		h.fccPendingListTail.next = bufRef
		h.fccPendingListTail = bufRef
	}

	// 增加等待计数
	atomic.AddInt32(&h.fccPendingCount, 1)

	// 更新FCC活动时间
	now := time.Now().UnixNano() / 1e6
	h.fccLastActivityTime = now
}

// fccHandleMcastActive 处理多播激活状态的数据
func (h *StreamHub) fccHandleMcastActive() {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	// 检查当前状态是否为MCAST_ACTIVE
	if h.fccState != FCC_STATE_MCAST_ACTIVE {
		return
	}

	// 处理等待队列中的数据
	for h.fccPendingListHead != nil {
		bufRef := h.fccPendingListHead
		h.fccPendingListHead = bufRef.next
		h.Mu.Unlock() // 临时解锁，避免长时间持有锁

		// 检测IDR帧
		if h.detectStrictIDRFrame(bufRef.data) {
			logger.LogPrintf("FCC: 检测到IDR帧，加速切换到多播模式")

			// 释放当前bufRef
			bufRef.Put()

			// 立即关闭FCC listener
			h.Mu.Lock()
			var fccConn *net.UDPConn
			if h.fccConn != nil {
				fccConn = h.fccConn
				h.fccConn = nil
			}

			// 立即清空队列
			for h.fccPendingListHead != nil {
				buf := h.fccPendingListHead
				h.fccPendingListHead = buf.next
				buf.Put()
			}
			h.fccPendingListTail = nil
			atomic.StoreInt32(&h.fccPendingCount, 0)
			h.Mu.Unlock()

			if fccConn != nil {
				fccConn.Close()
			}

			// 设置组播状态
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "检测到IDR帧，强制切换到多播模式")

			// 退出循环，不再处理其他缓冲数据
			return
		}

		// 将数据广播给所有客户端
		clients := make([]*hubClient, len(h.Clients))
		i := 0
		for _, c := range h.Clients {
			clients[i] = c
			i++
		}
		h.Mu.Unlock() // 临时解锁，让其他goroutine可以访问hub状态

		for _, c := range clients {
			select {
			case c.ch <- bufRef.data:
			case <-time.After(100 * time.Millisecond):
			}
		}

		// 减少引用计数
		bufRef.Put()

		// 重新获取锁以继续循环
		h.Mu.Lock()
		// 检查循环是否应该继续（状态是否仍然允许）
		if h.fccState != FCC_STATE_MCAST_ACTIVE {
			h.Mu.Unlock()
			return
		}
	}

	// 重置等待计数
	atomic.StoreInt32(&h.fccPendingCount, 0)
	h.fccPendingListTail = nil

	// 更新FCC活动时间
	now := time.Now().UnixNano() / 1e6
	h.fccLastActivityTime = now
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
		// 初始化FCC相关参数
		h.fccState = FCC_STATE_INIT
		h.fccStartSeq = 0
		h.fccTermSeq = 0
		h.fccTermSent = false

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

		// 更新最后FCC数据时间
		h.fccLastActivityTime = time.Now().UnixNano() / 1e6

		// 只有在有服务器地址时才设置为REQUESTED状态，否则保持INIT状态
		if h.fccServerAddr != nil {
			h.fccSetState(FCC_STATE_REQUESTED, "FCC启用并进入请求状态")

			// 在ServeHTTP中会调用initFCCConnection和sendFCCRequest
		}
	} else {
		// 禁用FCC时清理相关资源

		// 停止并清理所有客户端的FCC定时器
		for _, client := range h.Clients {
			client.mu.Lock()
			if client.fccTimeoutTimer != nil {
				client.fccTimeoutTimer.Stop()
				client.fccTimeoutTimer = nil
			}
			if client.fccSyncTimer != nil {
				client.fccSyncTimer.Stop()
				client.fccSyncTimer = nil
			}
			client.mu.Unlock()
		}

		// 清理链表缓冲区
		for h.fccPendingListHead != nil {
			bufRef := h.fccPendingListHead
			h.fccPendingListHead = bufRef.next
			bufRef.Put()
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
		if h.fccTimeoutTimer != nil {
			h.fccTimeoutTimer.Stop()
			h.fccTimeoutTimer = nil
		}
		if h.fccUnicastTimer != nil {
			h.fccUnicastTimer.Stop()
			h.fccUnicastTimer = nil
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
		port := h.fccPortMin + (time.Now().Nanosecond()+i)%portRange
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
		return fmt.Errorf("FCC 连接或服务器地址为空")
	}

	// 使用buildFCCRequestPacket函数构建请求包
	pkt := h.buildFCCRequestPacket(multicastAddr, clientPort)

	_, err := fccConn.WriteToUDP(pkt, fccServerAddr)
	if err != nil {
		logger.LogPrintf("FCC请求发送失败到 %s: %v", fccServerAddr.String(), err)
		return err
	}

	logger.LogPrintf("FCC请求已发送到 %s，端口 %d", fccServerAddr.String(), clientPort)
	logger.LogPrintf("FCC请求已发送到 %s 用于客户端 %s", multicastAddr.String(), multicastAddr.IP.String())

	// 更新FCC活动时间
	h.updateFCCActivity()

	// 启动超时定时器，处理FCC请求的响应超时
	h.startFCCTimeoutTimer()

	return nil
}

// cleanupFCC 清理FCC连接
// 注意：此方法仅在完全关闭hub时调用，不应该在单个客户端断开时调用
func (h *StreamHub) cleanupFCC() {
	h.Mu.Lock()
	defer h.Mu.Unlock()

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

	// 关闭所有UDP连接
	closeConn := func(conn **net.UDPConn) {
		if *conn != nil {
			(*conn).Close()
			*conn = nil
		}
	}
	closeConn(&h.fccConn)
	closeConn(&h.fccUnicastConn)

	// 停止并清除所有定时器
	stopTimer := func(timer **time.Timer) {
		if *timer != nil {
			(*timer).Stop()
			*timer = nil
		}
	}
	stopTimer(&h.fccSyncTimer)
	stopTimer(&h.fccTimeoutTimer)
	stopTimer(&h.fccUnicastTimer)

	// 取消FCC响应监听器
	if h.fccResponseCancel != nil {
		h.fccResponseCancel()
		h.fccResponseCancel = nil
	}

	// 重置FCC状态和相关变量
	h.fccState = FCC_STATE_INIT
	h.fccStartSeq = 0
	h.fccTermSeq = 0
	h.fccTermSent = false
	h.fccUnicastPort = 0
	h.fccUnicastStartTime = time.Time{}
	h.fccLastActivityTime = 0
}

// ClientFccState 存储每个客户端的FCC状态信息
type ClientFccState struct {
	state            int
	startSeq         uint16
	termSeq          uint16
	termSent         bool
	pendingListHead  *BufferRef
	pendingListTail  *BufferRef
	pendingCount     int32
	lastFccDataTime  int64
	unicastStartTime time.Time
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

	// 更新最后活动时间
	now := time.Now().UnixNano() / 1e6
	h.fccLastActivityTime = now

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

// updateFCCActivity 更新FCC最后活动时间
func (h *StreamHub) updateFCCActivity() {
	h.Mu.Lock()
	h.fccLastActivityTime = time.Now().UnixNano() / 1e6
	h.Mu.Unlock()
}

// sendFCCTermination 发送FCC终止包
func (h *StreamHub) sendFCCTermination(fccServerAddr *net.UDPAddr, seqNum uint16) error {
	h.Mu.RLock()
	fccEnabled := h.fccEnabled
	fccType := h.fccType
	fccConn := h.fccConn
	if fccConn == nil {
		for _, c := range h.Clients {
			if c != nil && c.fccConn != nil {
				fccConn = c.fccConn
				break
			}
		}
	}
	h.Mu.RUnlock()

	if !fccEnabled {
		return nil
	}

	if fccConn == nil || fccServerAddr == nil {
		return fmt.Errorf("FCC 连接或服务器地址为空")
	}

	// 构建FCC终止包
	var termPacket []byte
	switch fccType {
	case FCC_TYPE_HUAWEI:
		// 获取多播地址用于构建终止包
		h.Mu.RLock()
		multicastAddr := h.multicastAddr
		h.Mu.RUnlock()

		if multicastAddr != nil {
			termPacket = h.buildHuaweiFCCTermPacket(multicastAddr, seqNum)
		} else {
			return fmt.Errorf("多播地址不可用于华为终止包")
		}
	case FCC_TYPE_TELECOM:
		h.Mu.RLock()
		multicastAddr := h.multicastAddr
		h.Mu.RUnlock()

		if multicastAddr != nil {
			termPacket = h.buildTelecomFCCTermPacket(multicastAddr, seqNum)
		} else {
			return fmt.Errorf("多播地址不可用于电信终止包")
		}
	default:
		return fmt.Errorf("不支持的FCC类型: %d", fccType)
	}

	// 发送三次以确保送达 - 与C语言版本一致
	for i := 0; i < 3; i++ {
		// 检查hub是否已关闭
		select {
		case <-h.Closed:
			return nil
		default:
		}

		_, err := fccConn.WriteToUDP(termPacket, fccServerAddr)
		if err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond) // 矾暂延迟以提高可靠性
	}

	logger.LogPrintf("FCC: 终止包发送成功, 类型: %s, 序号: %d",
		map[int]string{FCC_TYPE_TELECOM: "Telecom", FCC_TYPE_HUAWEI: "Huawei"}[fccType], seqNum)

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

// buildFCCRequestPacket 构建FCC请求包
func (h *StreamHub) buildFCCRequestPacket(multicastAddr *net.UDPAddr, clientPort int) []byte {
	localIP := h.getLocalIPForFCC()

	switch h.fccType {
	case FCC_TYPE_HUAWEI:
		return h.buildHuaweiFCCRequestPacket(multicastAddr, localIP, clientPort)
	case FCC_TYPE_TELECOM:
		fallthrough
	default:
		return h.buildTelecomFCCRequestPacket(multicastAddr, clientPort)
	}
}

// buildTelecomFCCRequestPacket 构建电信FCC请求包
func (h *StreamHub) buildTelecomFCCRequestPacket(multicastAddr *net.UDPAddr, clientPort int) []byte {
	pk := make([]byte, 24)

	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_TELECOM_REQ // Version 2, Padding 0, FMT 2
	pk[1] = 205                        // Type: Generic RTP Feedback (205)
	lenWords := uint16(5)              // Length in 32-bit words minus 1
	binary.BigEndian.PutUint16(pk[2:4], lenWords)
	// pk[4-7]: Sender SSRC = 0 (already zeroed by make)

	// Media source SSRC (4 bytes) - multicast IP address
	ip := multicastAddr.IP.To4()
	if ip != nil {
		binary.BigEndian.PutUint32(pk[8:12], binary.BigEndian.Uint32(ip))
	}

	// FCI - Feedback Control Information
	// pk[12-15]: Version 0, Reserved 3 bytes (already zeroed)
	binary.BigEndian.PutUint16(pk[16:18], uint16(clientPort))         // FCC client port
	binary.BigEndian.PutUint16(pk[18:20], uint16(multicastAddr.Port)) // Mcast group port
	// pk[20-24]: Mcast group IP (already zeroed for test)
	if ip != nil {
		copy(pk[20:24], ip)
	}

	return pk
}

// buildHuaweiFCCRequestPacket 构建华为FCC请求包
func (h *StreamHub) buildHuaweiFCCRequestPacket(multicastAddr *net.UDPAddr, localIP net.IP, clientPort int) []byte {
	pk := make([]byte, 32)

	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_HUAWEI_REQ      // Version 2, Padding 0, FMT 5
	pk[1] = 205                            // Type: Generic RTP Feedback (205)
	binary.BigEndian.PutUint16(pk[2:4], 7) // Length = 8 words - 1 = 7
	// pk[4-7]: Sender SSRC = 0 (already zeroed by make)

	// Media source SSRC (4 bytes) - multicast IP address
	ip := multicastAddr.IP.To4()
	if ip != nil {
		binary.BigEndian.PutUint32(pk[8:12], binary.BigEndian.Uint32(ip))
	}

	// FCI - Feedback Control Information (16 bytes)
	// Local IP address (4 bytes) - network byte order
	if localIP != nil && localIP.To4() != nil {
		binary.BigEndian.PutUint32(pk[20:24], binary.BigEndian.Uint32(localIP.To4()))
	}

	// FCC client port (2 bytes) + Flag (2 bytes)
	binary.BigEndian.PutUint16(pk[24:26], uint16(clientPort))
	binary.BigEndian.PutUint16(pk[26:28], 0x8000)

	// Redirect support flag (4 bytes) - 0x20000000
	binary.BigEndian.PutUint32(pk[28:32], 0x20000000)

	return pk
}

// buildHuaweiFCCTermPacket 构建华为FCC终止包
func (h *StreamHub) buildHuaweiFCCTermPacket(multicastAddr *net.UDPAddr, seqNum uint16) []byte {
	pk := make([]byte, 16)

	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_HUAWEI_TERM     // Version 2, Padding 0, FMT 9
	pk[1] = 205                            // Type: Generic RTP Feedback (205)
	binary.BigEndian.PutUint16(pk[2:4], 3) // Length = 4 words - 1 = 3
	// pk[4-7]: Sender SSRC = 0 (already zeroed by make)

	// Media source SSRC (4 bytes) - multicast IP address
	ip := multicastAddr.IP.To4()
	if ip != nil {
		binary.BigEndian.PutUint32(pk[8:12], binary.BigEndian.Uint32(ip))
	}

	if seqNum > 0 {
		pk[12] = 0x01
		pk[13] = 0x00
		binary.BigEndian.PutUint16(pk[14:16], seqNum)
	} else {
		pk[12] = 0x02
		pk[13] = 0x00
	}

	return pk
}

// buildTelecomFCCTermPacket 构建电信FCC终止包
func (h *StreamHub) buildTelecomFCCTermPacket(multicastAddr *net.UDPAddr, seqNum uint16) []byte {
	pk := make([]byte, 16)

	// RTCP Header (8 bytes)
	pk[0] = 0x80 | FCC_FMT_TELECOM_TERM    // Version 2, Padding 0, FMT 5
	pk[1] = 205                            // Type: Generic RTP Feedback (205)
	binary.BigEndian.PutUint16(pk[2:4], 3) // Length = 4 words - 1 = 3
	// pk[4-7]: Sender SSRC = 0 (already zeroed by make)

	// Media source SSRC (4 bytes) - multicast IP address
	ip := multicastAddr.IP.To4()
	if ip != nil {
		binary.BigEndian.PutUint32(pk[8:12], binary.BigEndian.Uint32(ip))
	}

	pk[12] = 0
	if seqNum == 0 {
		pk[12] = 1
	}
	// pk[13]: Reserved (already zeroed)
	binary.BigEndian.PutUint16(pk[14:16], seqNum) // First multicast packet sequence

	return pk
}

func (h *StreamHub) buildHuaweiFCCNatPacket(sessionID uint32) []byte {
	pk := make([]byte, 8)
	pk[0] = 0x00
	pk[1] = 0x03
	pk[2] = 0x00
	pk[3] = 0x00
	binary.BigEndian.PutUint32(pk[4:8], sessionID)
	return pk
}

func (h *StreamHub) startClientFCCListener(client *hubClient, multicastAddr *net.UDPAddr) {
	if client == nil || client.fccConn == nil {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.LogPrintf("FCC客户端监听 goroutine panic: %v", r)
			}
		}()

		buf := make([]byte, 2048)
		for {
			select {
			case <-h.Closed:
				return
			default:
			}

			_ = client.fccConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, _, err := client.fccConn.ReadFromUDP(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				return
			}
			if n <= 0 {
				continue
			}

			data := make([]byte, n)
			copy(data, buf[:n])

			if !isRTCP205(data) {
				continue
			}

			fmtField := data[0] & 0x1F
			switch h.fccType {
			case FCC_TYPE_HUAWEI:
				h.handleClientHuaweiFCCPacket(client, multicastAddr, fmtField, data)
			default:
				h.handleClientTelecomFCCPacket(client, multicastAddr, fmtField, data)
			}
		}
	}()
}

func (h *StreamHub) handleClientTelecomFCCPacket(client *hubClient, multicastAddr *net.UDPAddr, fmtField byte, data []byte) {
	switch fmtField {
	case FCC_FMT_TELECOM_RESP:
		if len(data) < 20 {
			return
		}

		resultCode := data[12]
		respType := data[13]

		var newSignalPort uint16
		var newMediaPort uint16
		if len(data) >= 18 {
			newSignalPort = binary.BigEndian.Uint16(data[14:16])
			newMediaPort = binary.BigEndian.Uint16(data[16:18])
		}

		var newServerIP net.IP
		if len(data) >= 24 {
			ipv4 := net.IPv4(data[20], data[21], data[22], data[23]).To4()
			if ipv4 != nil && !ipv4.Equal(net.IPv4zero) {
				newServerIP = ipv4
			}
		}

		client.mu.Lock()
		serverAddrBase := client.fccServerAddr
		fccConn := client.fccConn
		redirectCount := client.fccRedirectCount
		client.mu.Unlock()

		if serverAddrBase == nil || fccConn == nil {
			return
		}

		serverAddr := &net.UDPAddr{IP: append(net.IP(nil), serverAddrBase.IP...), Port: serverAddrBase.Port}
		if newSignalPort != 0 && int(newSignalPort) != serverAddr.Port {
			serverAddr.Port = int(newSignalPort)
		}
		if newServerIP != nil {
			serverAddr.IP = newServerIP
		}
		h.Mu.Lock()
		h.fccServerAddr = serverAddr
		h.Mu.Unlock()

		if resultCode != 0 {
			client.mu.Lock()
			client.fccState = FCC_STATE_MCAST_ACTIVE
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC服务器响应错误，降级到组播")
			return
		}

		switch respType {
		case 1:
			client.mu.Lock()
			client.fccState = FCC_STATE_MCAST_ACTIVE
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC无需单播，直接组播")
		case 2:
			client.mu.Lock()
			client.fccState = FCC_STATE_UNICAST_PENDING
			client.fccMediaPort = int(newMediaPort)
			client.fccServerAddr = serverAddr
			mediaPort := client.fccMediaPort
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_UNICAST_PENDING, "FCC服务器接受请求")

			if mediaPort != 0 {
				mediaAddr := &net.UDPAddr{IP: serverAddr.IP, Port: mediaPort}
				for i := 0; i < 3; i++ {
					_, _ = fccConn.WriteToUDP(nil, mediaAddr)
				}
			}
			for i := 0; i < 3; i++ {
				_, _ = fccConn.WriteToUDP(nil, serverAddr)
			}
		case 3:
			redirectCount++
			if redirectCount > 5 {
				client.mu.Lock()
				client.fccRedirectCount = redirectCount
				client.fccState = FCC_STATE_MCAST_ACTIVE
				client.mu.Unlock()
				h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC重定向过多，降级到组播")
				return
			}
			client.mu.Lock()
			client.fccRedirectCount = redirectCount
			client.fccServerAddr = serverAddr
			client.fccState = FCC_STATE_REQUESTED
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_REQUESTED, "FCC服务器重定向，重新请求")
			if multicastAddr != nil {
				_ = h.sendFCCRequestWithConn(fccConn, multicastAddr, fccConn.LocalAddr().(*net.UDPAddr).Port, serverAddr)
				client.startClientFCCTimeoutTimer()
			}
		default:
			client.mu.Lock()
			client.fccState = FCC_STATE_MCAST_ACTIVE
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC响应类型不支持，降级到组播")
		}
	case FCC_FMT_TELECOM_SYNC:
		client.mu.Lock()
		state := client.fccState
		client.mu.Unlock()
		if state == FCC_STATE_MCAST_REQUESTED || state == FCC_STATE_MCAST_ACTIVE {
			return
		}
		client.mu.Lock()
		client.fccState = FCC_STATE_MCAST_REQUESTED
		client.mu.Unlock()
		h.fccSetState(FCC_STATE_MCAST_REQUESTED, "收到同步通知，准备切换组播")
		h.prepareSwitchToMulticast()
	}
}

func (h *StreamHub) handleClientHuaweiFCCPacket(client *hubClient, multicastAddr *net.UDPAddr, fmtField byte, data []byte) {
	switch fmtField {
	case FCC_FMT_HUAWEI_RESP:
		if len(data) < 16 {
			return
		}

		resultCode := data[12]
		respType := uint16(0)
		if len(data) >= 16 {
			respType = binary.BigEndian.Uint16(data[14:16])
		}

		client.mu.Lock()
		serverAddr := client.fccServerAddr
		fccConn := client.fccConn
		redirectCount := client.fccRedirectCount
		client.mu.Unlock()

		if serverAddr == nil || fccConn == nil {
			return
		}

		if resultCode != 1 {
			client.mu.Lock()
			client.fccState = FCC_STATE_MCAST_ACTIVE
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC服务器响应错误，降级到组播")
			return
		}

		switch respType {
		case 1:
			client.mu.Lock()
			client.fccState = FCC_STATE_MCAST_ACTIVE
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC无需单播，直接组播")
		case 2:
			client.mu.Lock()
			client.fccState = FCC_STATE_UNICAST_PENDING
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_UNICAST_PENDING, "FCC服务器接受请求")

			if len(data) >= 36 {
				natFlag := data[24]
				needNat := ((natFlag << 2) >> 7) == 1
				serverPort := binary.BigEndian.Uint16(data[26:28])
				sessionID := binary.BigEndian.Uint32(data[28:32])
				serverIP := net.IPv4(data[32], data[33], data[34], data[35]).To4()
				if serverIP != nil && !serverIP.Equal(net.IPv4zero) {
					client.mu.Lock()
					client.fccServerAddr.IP = serverIP
					serverAddr = client.fccServerAddr
					client.mu.Unlock()
					h.Mu.Lock()
					h.fccServerAddr = client.fccServerAddr
					h.Mu.Unlock()
				}
				if serverPort != 0 {
					client.mu.Lock()
					client.fccMediaPort = int(serverPort)
					client.mu.Unlock()
				}
				client.mu.Lock()
				mediaPort := client.fccMediaPort
				client.mu.Unlock()
				if needNat && sessionID != 0 && mediaPort != 0 {
					client.mu.Lock()
					client.fccHuaweiSessionID = sessionID
					client.mu.Unlock()
					natPkt := h.buildHuaweiFCCNatPacket(sessionID)
					mediaAddr := &net.UDPAddr{IP: append(net.IP(nil), serverAddr.IP...), Port: mediaPort}
					for i := 0; i < 3; i++ {
						_, _ = fccConn.WriteToUDP(natPkt, mediaAddr)
						time.Sleep(10 * time.Millisecond)
					}
				}
			}
		case 3:
			redirectCount++
			if redirectCount > 5 {
				client.mu.Lock()
				client.fccRedirectCount = redirectCount
				client.fccState = FCC_STATE_MCAST_ACTIVE
				client.mu.Unlock()
				h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC重定向过多，降级到组播")
				return
			}
			if len(data) >= 36 {
				serverPort := binary.BigEndian.Uint16(data[26:28])
				serverIP := net.IPv4(data[32], data[33], data[34], data[35]).To4()
				if serverIP != nil && !serverIP.Equal(net.IPv4zero) {
					client.mu.Lock()
					client.fccServerAddr.IP = serverIP
					client.mu.Unlock()
				}
				if serverPort != 0 {
					client.mu.Lock()
					client.fccServerAddr.Port = int(serverPort)
					client.mu.Unlock()
				}
				h.Mu.Lock()
				h.fccServerAddr = client.fccServerAddr
				h.Mu.Unlock()
			}
			client.mu.Lock()
			client.fccRedirectCount = redirectCount
			client.fccState = FCC_STATE_REQUESTED
			serverAddr = client.fccServerAddr
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_REQUESTED, "FCC服务器重定向，重新请求")
			if multicastAddr != nil {
				_ = h.sendFCCRequestWithConn(fccConn, multicastAddr, fccConn.LocalAddr().(*net.UDPAddr).Port, serverAddr)
				client.startClientFCCTimeoutTimer()
			}
		default:
			client.mu.Lock()
			client.fccState = FCC_STATE_MCAST_ACTIVE
			client.mu.Unlock()
			h.fccSetState(FCC_STATE_MCAST_ACTIVE, "FCC响应类型不支持，降级到组播")
		}
	case FCC_FMT_HUAWEI_SYNC:
		client.mu.Lock()
		state := client.fccState
		client.mu.Unlock()
		if state == FCC_STATE_MCAST_REQUESTED || state == FCC_STATE_MCAST_ACTIVE {
			return
		}
		client.mu.Lock()
		client.fccState = FCC_STATE_MCAST_REQUESTED
		client.mu.Unlock()
		h.fccSetState(FCC_STATE_MCAST_REQUESTED, "收到同步通知，准备切换组播")
		h.prepareSwitchToMulticast()
	}
}

// getLocalIPForFCC 获取用于FCC的本地IP地址，支持指定接口
func (h *StreamHub) getLocalIPForFCC() net.IP {
	h.Mu.RLock()
	serverConfig := config.Cfg.Server
	h.Mu.RUnlock()

	// 获取FCC专用接口，如果未配置则使用通用上游接口
	var interfaceName string
	if serverConfig.UpstreamInterfaceFcc != "" {
		interfaceName = serverConfig.UpstreamInterfaceFcc
	} else if serverConfig.UpstreamInterface != "" {
		interfaceName = serverConfig.UpstreamInterface
	}

	if interfaceName != "" {
		// 如果指定了接口，则从该接口获取IP
		iface, err := net.InterfaceByName(interfaceName)
		if err != nil {
			logger.LogPrintf("FCC: 获取接口 %s 失败: %v", interfaceName, err)
			return h.getDefaultLocalIP()
		}

		addrs, err := iface.Addrs()
		if err != nil {
			logger.LogPrintf("FCC: 获取接口 %s 地址失败: %v", interfaceName, err)
			return h.getDefaultLocalIP()
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				if !ipnet.IP.IsLoopback() {
					logger.LogPrintf("FCC: 从接口 %s 使用本地IP: %s", interfaceName, ipnet.IP.String())
					return ipnet.IP
				}
			}
		}
		logger.LogPrintf("FCC: 未在接口 %s 中找到有效的IPv4地址", interfaceName)
	}

	// 如果没有配置特定接口或获取失败，则使用默认方法获取IP
	return h.getDefaultLocalIP()
}

// getDefaultLocalIP 默认获取本地IP的方法
func (h *StreamHub) getDefaultLocalIP() net.IP {
	// 准备多个备选地址，提高获取本地IP的成功率
	dnsServers := []string{"8.8.8.8:80", "8.8.4.4:80", "223.5.5.5:80", "223.6.6.6:80"}

	for _, server := range dnsServers {
		conn, err := net.DialTimeout("udp", server, 2*time.Second)
		if err != nil {
			continue // 当前服务器失败，尝试下一个
		}
		conn.Close() // 使用完后立即关闭连接
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
	logger.LogPrintf("FCC: 无法确定本地IP地址")
	return nil
}

// cleanupFCCResources 清理客户端的FCC相关资源
func (c *hubClient) cleanupFCCResources() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 关闭FCC连接
	if c.fccConn != nil {
		c.fccConn.Close()
		c.fccConn = nil
	}

	// 停止并清理所有定时器
	if c.fccTimeoutTimer != nil {
		c.fccTimeoutTimer.Stop()
		c.fccTimeoutTimer = nil
	}
	if c.fccSyncTimer != nil {
		c.fccSyncTimer.Stop()
		c.fccSyncTimer = nil
	}

	// 重置FCC状态信息
	c.fccState = FCC_STATE_INIT
	c.fccMediaPort = 0
	c.fccRedirectCount = 0
	c.fccHuaweiSessionID = 0
	c.fccServerAddr = nil
}

// startClientFCCTimeoutTimer 为客户端启动独立的FCC超时定时器
func (c *hubClient) startClientFCCTimeoutTimer() {
	// 如果已有定时器在运行，先停止
	c.mu.Lock()
	if c.fccTimeoutTimer != nil {
		c.fccTimeoutTimer.Stop()
		c.fccTimeoutTimer = nil
	}

	// 创建新的定时器 - 使用80ms超时时间
	c.fccTimeoutTimer = time.AfterFunc(FCC_TIMEOUT_SIGNALING_MS*time.Millisecond, func() {
		c.mu.Lock()
		c.fccState = FCC_STATE_MCAST_ACTIVE
		logger.LogPrintf("FCC客户端超时: 服务器无响应，降级到组播播放，客户端: %s", c.connID)
		c.mu.Unlock()

		// 清理客户端FCC资源
		c.cleanupFCCResources()
	})
	c.mu.Unlock()
}

// detectIDRFrame 检测H.264码流中的IDR帧
func (h *StreamHub) detectStrictIDRFrame(data []byte) bool {
	if len(data) < 188 || data[0] != 0x47 {
		// 不是TS包，跳过
		return false
	}

	var videoPID uint16 = 0
	var pmtPID uint16 = 0

	for i := 0; i < len(data); i += 188 {
		if i+188 > len(data) {
			break
		}

		tsPacket := data[i : i+188]
		if tsPacket[0] != 0x47 {
			continue
		}

		// 提取PID
		pid := uint16(tsPacket[1]&0x1F)<<8 | uint16(tsPacket[2])

		// PAT包
		if pid == 0x0000 {
			tableID := tsPacket[5]
			if tableID == 0x00 {
				sectionLength := int((uint16(tsPacket[6])&0x0F)<<8 | uint16(tsPacket[7]))
				if sectionLength > 8 && sectionLength <= 184 {
					for j := 8; j < sectionLength-4; j += 4 {
						programNum := uint16(tsPacket[j])<<8 | uint16(tsPacket[j+1])
						if programNum != 0 {
							pmtPID = (uint16(tsPacket[j+2]&0x1F) << 8) |
								uint16(tsPacket[j+3])
							break
						}
					}
				}
			}
		} else if pid == pmtPID {
			// PMT包
			tableID := tsPacket[5]
			if tableID == 0x02 {
				sectionLength := int((uint16(tsPacket[6])&0x0F)<<8 | uint16(tsPacket[7]))
				if sectionLength > 12 && sectionLength <= 184 {
					infoStart := 12
					infoEnd := sectionLength - 4
					for infoStart < infoEnd {
						streamType := tsPacket[infoStart]
						elementaryPID := (uint16(tsPacket[infoStart+1]&0x1F) << 8) |
							uint16(tsPacket[infoStart+2])
						if streamType == 0x01 || streamType == 0x1B || streamType == 0x24 {
							videoPID = elementaryPID
						}
						infoStart += 5 + int(tsPacket[infoStart+4]&0x0F)<<8 | int(tsPacket[infoStart+5])
					}
				}
			}
		}

		// 检查视频PID
		if videoPID != 0 && pid == videoPID {
			adaptationFieldControl := (tsPacket[3] & 0x30) >> 4
			payloadStart := 4
			if adaptationFieldControl&0x02 != 0 {
				payloadStart = 5 + int(tsPacket[4])
			}
			if payloadStart < len(tsPacket) {
				for j := payloadStart; j < len(tsPacket)-4; j++ {
					if tsPacket[j] == 0x00 && tsPacket[j+1] == 0x00 &&
						tsPacket[j+2] == 0x00 && tsPacket[j+3] == 0x01 {
						if j+4 < len(tsPacket) {
							nalType := tsPacket[j+4] & 0x1F
							// 仅检测IDR帧
							if nalType == 5 {
								logger.LogPrintf("FCC: 检测到严格IDR帧")
								return true
							}
						}
					}
				}
			}
		}
	}

	return false
}

// checkFCCStatus 定期检查FCC状态，实现主动超时释放
func (h *StreamHub) checkFCCStatus() {
	ticker := time.NewTicker(100 * time.Millisecond) // 每100毫秒检查一次
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 检查Hub是否已关闭
			select {
			case <-h.Closed:
				return
			default:
			}

			h.Mu.RLock()
			fccEnabled := h.fccEnabled
			currentState := h.fccState
			h.Mu.RUnlock()

			if !fccEnabled {
				continue
			}

			now := time.Now().UnixNano() / 1e6 // 当前时间（毫秒）

			// 检查FCC超时情况
			switch currentState {
			case FCC_STATE_REQUESTED, FCC_STATE_UNICAST_PENDING:
				// 信令阶段超时检查
				h.Mu.RLock()
				elapsed := now - h.fccLastActivityTime
				h.Mu.RUnlock()

				if elapsed > FCC_TIMEOUT_SIGNALING_MS {
					logger.LogPrintf("FCC: 信令阶段超时 (%d ms)，降级到组播播放", elapsed)
					h.fccSetState(FCC_STATE_MCAST_ACTIVE, "信令阶段超时")
				}

			case FCC_STATE_UNICAST_ACTIVE, FCC_STATE_MCAST_REQUESTED:
				// 单播阶段超时检查 - 检查是否在单播阶段停留太久
				h.Mu.RLock()
				elapsedSinceUnicast := now - h.fccUnicastStartTime.UnixNano()/1e6
				h.Mu.RUnlock()

				// 如果在单播阶段停留超过预设时间（比如5秒），则主动切换到多播
				const FCC_UNICAST_MAX_DURATION = 5000 // 5秒
				if elapsedSinceUnicast > FCC_UNICAST_MAX_DURATION {
					logger.LogPrintf("FCC: 单播阶段停留太久 (%d ms)，主动切换到多播", elapsedSinceUnicast)
					h.fccSetState(FCC_STATE_MCAST_ACTIVE, "单播阶段超时")
				}
			}
		case <-h.Closed:
			return
		}
	}
}
