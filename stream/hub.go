package stream

import (
	"context"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
	"github.com/qist/tvgate/utils/mem"
	tsync "github.com/qist/tvgate/utils/sync"
)

const (
	StateStopped = iota
	StatePlaying
	StateError
)

// 客户端快照 slice 池，减少 Broadcast 中的频繁分配
var clientSlicePool = sync.Pool{
	New: func() any {
		s := make([]*ringbuffer.RingBuffer, 0, 16)
		return &s
	},
}

type StreamHubs struct {
	mu        sync.RWMutex // 使用读写锁提高并发性能
	clients   map[*ringbuffer.RingBuffer]struct{}
	isClosed  bool
	key       string
	idleGen   uint64
	idleTimer *time.Timer
	// 添加生命周期管理
	ctx    context.Context
	cancel context.CancelFunc
	wg     tsync.WaitGroup
	// 添加流状态管理
	state     int           // 0: stopped, 1: playing, 2: error
	stateCh   chan struct{} // 状态变更通知通道（close+replace 实现广播）
	lastError error
	// 添加RTSP客户端引用
	rtspClient  *gortsplib.Client
	videoMedia  *description.Media
	videoFormat interface{}
	audioMedia  *description.Media
	audioFormat *format.MPEG4Audio
	setupMu     sync.Mutex // 用于同步初始化过程
}

func NewStreamHubs() *StreamHubs {
	ctx, cancel := context.WithCancel(config.ServerCtx)
	hub := &StreamHubs{
		clients: make(map[*ringbuffer.RingBuffer]struct{}),
		state:   StateStopped,
		ctx:     ctx,
		cancel:  cancel,
	}
	hub.stateCh = make(chan struct{})
	return hub
}

func (hub *StreamHubs) AddClient(ch *ringbuffer.RingBuffer) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.isClosed {
		ch.Close()
		return
	}
	hub.cancelIdleCloseLocked()
	hub.clients[ch] = struct{}{}
}

func (hub *StreamHubs) RemoveClient(ch *ringbuffer.RingBuffer) {
	var rtspToClose *gortsplib.Client

	hub.mu.Lock()
	// 检查channel是否还在clients映射中
	if _, exists := hub.clients[ch]; exists {
		delete(hub.clients, ch)
		// 先 Clear 主动排空数据引用，再 Close
		ch.Clear()
		ch.Close()
	}
	if !hub.isClosed && len(hub.clients) == 0 {
		if hub.rtspClient != nil {
			rtspToClose = hub.rtspClient
			hub.rtspClient = nil
		}
		hub.state = StateStopped
		hub.scheduleIdleCloseLocked()
	}
	hub.mu.Unlock()

	if rtspToClose != nil {
		rtspToClose.Close()
	}
	// 如果channel不存在于clients映射中，说明已经被Broadcast方法移除并关闭了
}

func (hub *StreamHubs) Broadcast(data []byte) {
	hub.mu.RLock() // 使用读锁，提高并发性能
	clientCount := len(hub.clients)
	if clientCount == 0 {
		hub.mu.RUnlock()
		return
	}
	// 从池中获取快照 slice，减少分配
	clientsPtr := clientSlicePool.Get().(*[]*ringbuffer.RingBuffer)
	clients := (*clientsPtr)[:0]
	for ch := range hub.clients {
		clients = append(clients, ch)
	}
	hub.mu.RUnlock()

	// 只复制一次，所有客户端共享同一份只读副本
	// data 来自 astits muxer 的内部 buffer，下次 WriteTables 会覆盖，所以必须复制
	shared := make([]byte, len(data))
	copy(shared, data)

	hub.pushToClients(clients, shared)

	// 归还快照 slice 到池
	*clientsPtr = clients
	clientSlicePool.Put(clientsPtr)
}

// BroadcastNoCopy 直接转发数据，不复制
// 用于 mpegts 直通模式：pkt.Payload 来自 gortsplib，每包独立分配，不会被复用
func (hub *StreamHubs) BroadcastNoCopy(data []byte) {
	hub.mu.RLock()
	clientCount := len(hub.clients)
	if clientCount == 0 {
		hub.mu.RUnlock()
		return
	}
	clientsPtr := clientSlicePool.Get().(*[]*ringbuffer.RingBuffer)
	clients := (*clientsPtr)[:0]
	for ch := range hub.clients {
		clients = append(clients, ch)
	}
	hub.mu.RUnlock()

	hub.pushToClients(clients, data)

	*clientsPtr = clients
	clientSlicePool.Put(clientsPtr)
}

// pushToClients 将数据推送到所有客户端。
// Push 仅在缓冲已关闭时返回 false；关闭的客户端由 RemoveClient 负责清理，
// 这里无需阻塞重试，直接跳过即可，避免拖慢整个 hub 的生产者。
func (hub *StreamHubs) pushToClients(clients []*ringbuffer.RingBuffer, data []byte) {
	for _, ch := range clients {
		ch.Push(data)
	}
}

// removeClientIfNotExist 从客户端列表中移除已不存在的客户端
func (hub *StreamHubs) removeClientIfNotExist(ch *ringbuffer.RingBuffer) {
	var rtspToClose *gortsplib.Client

	hub.mu.Lock()
	// 再次确认客户端是否还在列表中
	if _, exists := hub.clients[ch]; exists {
		delete(hub.clients, ch)
		ch.Clear()
		ch.Close()
	}
	if !hub.isClosed && len(hub.clients) == 0 {
		if hub.rtspClient != nil {
			rtspToClose = hub.rtspClient
			hub.rtspClient = nil
		}
		hub.state = StateStopped
		hub.scheduleIdleCloseLocked()
	}
	hub.mu.Unlock()

	if rtspToClose != nil {
		rtspToClose.Close()
	}
}

func (hub *StreamHubs) ClientCount() int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.clients)
}

func (hub *StreamHubs) Close() {
	hub.mu.Lock()
	if hub.isClosed {
		hub.mu.Unlock()
		return
	}
	hub.isClosed = true
	hub.cancelIdleCloseLocked()
	hub.state = StateStopped
	hub.notifyState()

	// 清理媒体信息
	hub.videoMedia = nil
	hub.videoFormat = nil
	hub.audioMedia = nil
	hub.audioFormat = nil
	hub.lastError = nil

	// 关闭RTSP客户端
	if hub.rtspClient != nil {
		hub.rtspClient.Close()
		hub.rtspClient = nil
	}

	for ch := range hub.clients {
		ch.Clear()
		ch.Close()
	}
	hub.clients = nil

	// 清理 idle timer 引用
	if hub.idleTimer != nil {
		hub.idleTimer.Stop()
		hub.idleTimer = nil
	}

	// 取消上下文并等待 goroutine
	if hub.cancel != nil {
		hub.cancel()
	}
	hub.mu.Unlock()

	hub.wg.Wait()

	// 仅在堆显著偏高时才做一次普通 GC。
	// 注意：不调用 debug.FreeOSMemory()，它会清空 sync.Pool 并归还内存给 OS，
	// 反而导致后续分配变慢、GC 更频繁；也不要每次关闭都强制 GC，否则高频
	// 频道启停会反复触发 GC 抬高 CPU。交由运行时自适应 GC 处理常态回收。
	mem.FreeMemoryIfHigh(256)
}

func (hub *StreamHubs) scheduleIdleCloseLocked() {
	hub.idleGen++
	gen := hub.idleGen
	key := hub.key
	if key == "" {
		return
	}
	if hub.idleTimer != nil {
		hub.idleTimer.Stop()
	}
	hub.idleTimer = time.AfterFunc(10*time.Second, func() {
		hub.mu.RLock()
		closed := hub.isClosed
		empty := len(hub.clients) == 0
		sameGen := hub.idleGen == gen
		hub.mu.RUnlock()
		if closed || !empty || !sameGen {
			return
		}
		RemoveHub(key)
	})
}

func (hub *StreamHubs) cancelIdleCloseLocked() {
	hub.idleGen++
	if hub.idleTimer != nil {
		hub.idleTimer.Stop()
		hub.idleTimer = nil
	}
}

// GetContext 获取 hub 的上下文
func (hub *StreamHubs) GetContext() context.Context {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.ctx
}

// AddWG 为 hub 添加等待组计数
func (hub *StreamHubs) AddWG(n int) {
	hub.wg.Add(n)
}

// DoneWG 减少 hub 等待组计数
func (hub *StreamHubs) DoneWG() {
	hub.wg.Done()
}

// Go 启动一个协程并自动管理 WaitGroup 的计数
func (hub *StreamHubs) Go(f func()) {
	hub.wg.Go(f)
}

// notifyState 通知所有等待者状态已变更。使用 close+replace 实现广播。
// 调用方必须已持有 hub.mu 写锁。
func (hub *StreamHubs) notifyState() {
	close(hub.stateCh)
	hub.stateCh = make(chan struct{})
}

// 新增方法：设置流为播放状态
func (hub *StreamHubs) SetPlaying() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.state = 1
	hub.lastError = nil
	hub.notifyState()
}

// 新增方法：设置流为停止状态
func (hub *StreamHubs) SetStopped() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.state = 0
	hub.notifyState()
}

// SetError 设置流为错误状态
func (hub *StreamHubs) SetError(err error) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.state = StateError
	hub.lastError = err
	hub.notifyState()

	// 清除媒体信息
	hub.videoMedia = nil
	hub.videoFormat = nil
	hub.audioMedia = nil
	hub.audioFormat = nil

	// 如果有 RTSP 客户端，也将其关闭
	if hub.rtspClient != nil {
		hub.rtspClient.Close()
		hub.rtspClient = nil
	}

	// 关闭所有现有客户端通道，让他们重新连接
	for ch := range hub.clients {
		ch.Clear()
		ch.Close()
	}
	hub.clients = make(map[*ringbuffer.RingBuffer]struct{})
}

// 新增方法：获取最后的错误
func (hub *StreamHubs) GetLastError() error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.lastError
}

// 新增方法：等待流变为播放状态
func (hub *StreamHubs) WaitForPlaying(ctx context.Context) bool {
	for {
		hub.mu.RLock()
		if hub.isClosed || hub.state == StateError {
			hub.mu.RUnlock()
			return false
		}
		if hub.state == StatePlaying {
			hub.mu.RUnlock()
			return true
		}
		// 快照当前通知通道；后续状态变更会关闭它，从而唤醒本次等待。
		ch := hub.stateCh
		hub.mu.RUnlock()

		// 等待状态变更或 context 取消（无需为每次等待额外分配 goroutine）
		select {
		case <-ctx.Done():
			return false
		case <-ch:
			// 状态可能已变更，回到循环顶部重新检查
		}
	}
}

// 新增方法：设置RTSP客户端
func (hub *StreamHubs) SetRtspClient(client *gortsplib.Client) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.rtspClient = client
}

// 新增方法：获取RTSP客户端
func (hub *StreamHubs) GetRtspClient() *gortsplib.Client {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.rtspClient
}

// 新增方法：检查RTSP客户端是否存在
func (hub *StreamHubs) HasRtspClient() bool {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.rtspClient != nil
}

// SetMediaInfo stores the video media and format for reuse
func (h *StreamHubs) SetMediaInfo(media *description.Media, format interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 直接保存媒体信息，不进行类型转换
	h.videoMedia = media
	// 保存原始格式接口，后续通过类型断言使用
	h.videoFormat = format
}

// GetMediaInfo retrieves stored video media and format
func (h *StreamHubs) GetMediaInfo() (*description.Media, interface{}, *description.Media, *format.MPEG4Audio) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.videoMedia, h.videoFormat, h.audioMedia, h.audioFormat
}

// SetAudioMediaInfo stores the audio media and format for reuse
func (h *StreamHubs) SetAudioMediaInfo(media *description.Media, format *format.MPEG4Audio) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.audioMedia = media
	h.audioFormat = format
}

// ClearMediaInfo 清除媒体信息
func (h *StreamHubs) ClearMediaInfo() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.videoMedia = nil
	h.videoFormat = nil
	h.audioMedia = nil
	h.audioFormat = nil
}

// GetSetupLock 获取初始化锁
func (h *StreamHubs) GetSetupLock() {
	h.setupMu.Lock()
}

// ReleaseSetupLock 释放初始化锁
func (h *StreamHubs) ReleaseSetupLock() {
	h.setupMu.Unlock()
}
