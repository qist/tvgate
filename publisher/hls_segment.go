package publisher

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/stream"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
)

// HLSSegmentManager 管理HLS片段生成和文件管理
type HLSSegmentManager struct {
	streamName      string
	segmentPath     string
	playlistPath    string
	segmentDuration int
	segmentCount    int
	segments        []string
	segNumber       int
	segBaseNumber   int
	dataBuffer      *ringbuffer.RingBuffer
	mutex           sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc

	// 数据缓冲
	dataBuf []byte

	// Hub相关字段
	hub          *stream.StreamHubs
	clientBuffer *ringbuffer.RingBuffer

	// 片段相关字段
	currentSeg *os.File
	segStart   time.Time

	// needPull标志
	needPull bool

	// PAT/PMT信息缓存
	patPmtBuf   []byte
	patPmtMutex sync.Mutex

	// 上次写入时间，用于确保数据及时写入
	lastWriteTime time.Time
}

// NewHLSSegmentManager 创建新的HLS片段管理器
func NewHLSSegmentManager(ctx context.Context, streamName, segmentPath string, segmentDuration int) *HLSSegmentManager {
	playlistPath := filepath.Join(segmentPath, "index.m3u8")

	manager := &HLSSegmentManager{
		streamName:      streamName,
		segmentPath:     segmentPath,
		playlistPath:    playlistPath,
		segmentDuration: segmentDuration,
		segmentCount:    10,
		segments:        make([]string, 0),
		segBaseNumber:   1,
		needPull:        false,
		patPmtBuf:       make([]byte, 0, 188*8),
		lastWriteTime:   time.Now(),
	}

	// 创建数据缓冲区
	manager.dataBuffer, _ = ringbuffer.New(2 * 1024 * 1024) // 2MB缓冲区

	// 设置取消函数和上下文
	if ctx == nil {
		manager.ctx, manager.cancel = context.WithCancel(context.Background())
	} else {
		manager.ctx, manager.cancel = context.WithCancel(ctx)
	}

	return manager
}

// Start 开始HLS片段处理
func (h *HLSSegmentManager) Start() {
	if !h.needPull {
		return
	}

	// 保证目录存在
	if err := os.MkdirAll(h.segmentPath, 0755); err != nil {
		log.Printf("[%s] failed to create segment dir: %v", h.streamName, err)
		return
	}

	// 创建客户端缓冲区
	h.clientBuffer, _ = ringbuffer.New(1024)

	// 注册到hub
	if h.hub != nil {
		h.hub.AddClient(h.clientBuffer)
		go h.processDataLoopFromHub()
	} else {
		go h.processDataLoop()
	}

	go h.cleanupLoop()
	log.Printf("[%s] HLS segment manager started", h.streamName)
}

// Stop 停止HLS片段处理
func (h *HLSSegmentManager) Stop() {
	if !h.needPull {
		return
	}

	h.cancel()

	h.mutex.Lock()
	// 写入并关闭当前片段
	if h.currentSeg != nil {
		if len(h.dataBuf) > 0 {
			_, _ = h.currentSeg.Write(h.dataBuf)
			h.dataBuf = h.dataBuf[:0]
		}
		h.currentSeg.Close()
		h.currentSeg = nil
	}
	h.mutex.Unlock()

	log.Printf("[%s] HLS segment manager stopped", h.streamName)
}

// WriteData 写入数据用于HLS处理
func (h *HLSSegmentManager) WriteData(data []byte) {
	if !h.needPull {
		return
	}

	// 提取 PAT/PMT（持续更新缓存）
	h.extractPATPMT(data)

	h.dataBuffer.Push(data)
}

// extractPATPMT 从数据中提取PAT/PMT信息（持续更新）
func (h *HLSSegmentManager) extractPATPMT(data []byte) {
	h.patPmtMutex.Lock()
	defer h.patPmtMutex.Unlock()

	// 检查是否已经有PAT和PMT
	patFound := false
	pmtFound := false
	
	for i := 0; i <= len(h.patPmtBuf)-188 && len(h.patPmtBuf) >= 188; i++ {
		if h.patPmtBuf[i] == 0x47 {
			pid := ((uint16(h.patPmtBuf[i+1]) & 0x1F) << 8) | uint16(h.patPmtBuf[i+2])
			if pid == 0x0000 {
				patFound = true
			} else if pid >= 0x1000 && pid <= 0x1FFF {
				pmtFound = true
			}
		}
	}
	
	// 如果已经找到PAT和PMT，则不需要再次查找
	if patFound && pmtFound && len(h.patPmtBuf) >= 376 {
		return
	}

	// 扫描 data，找 TS 包（188 字节对齐的包）
	for i := 0; i <= len(data)-188; i++ {
		if data[i] != 0x47 {
			continue
		}
		// 基本 PID 判定
		pid := ((uint16(data[i+1]) & 0x1F) << 8) | uint16(data[i+2])

		// 仅在 payload_unit_start_indicator 设置时收集（更可能是完整表）
		payloadStart := (data[i+1] & 0x40) != 0

		// 查找PAT包
		if payloadStart && pid == 0x0000 && !patFound {
			end := i + 188
			if end <= len(data) {
				// 如果是第一个PAT包，重置缓冲区
				if !patFound {
					h.patPmtBuf = h.patPmtBuf[:0]
				}
				h.patPmtBuf = append(h.patPmtBuf, data[i:end]...)
				patFound = true
			}
		}
		
		// 查找PMT包
		if payloadStart && pid >= 0x1000 && pid <= 0x1FFF && !pmtFound {
			end := i + 188
			if end <= len(data) {
				h.patPmtBuf = append(h.patPmtBuf, data[i:end]...)
				pmtFound = true
			}
		}
		
		// 一旦找到PAT和PMT就停止查找
		if patFound && pmtFound && len(h.patPmtBuf) >= 376 {
			return
		}
	}
}

// processDataLoop 处理数据循环（直接从 dataBuffer）
func (h *HLSSegmentManager) processDataLoop() {
	if !h.needPull {
		return
	}

	for {
		data, ok := h.dataBuffer.PullWithContext(h.ctx)
		if !ok {
			h.mutex.Lock()
			if h.currentSeg != nil {
				if len(h.dataBuf) > 0 {
					_, _ = h.currentSeg.Write(h.dataBuf)
					h.dataBuf = h.dataBuf[:0]
				}
				h.currentSeg.Close()
				h.currentSeg = nil
			}
			h.mutex.Unlock()
			return
		}

		if chunk, ok := data.([]byte); ok {
			h.mutex.Lock()
			h.processChunk(chunk)
			h.mutex.Unlock()
		}
	}
}

// processDataLoopFromHub 从 hub 读取数据处理
func (h *HLSSegmentManager) processDataLoopFromHub() {
	if !h.needPull {
		return
	}

	for {
		data, ok := h.clientBuffer.PullWithContext(h.ctx)
		if !ok {
			h.mutex.Lock()
			if h.currentSeg != nil {
				if len(h.dataBuf) > 0 {
					_, _ = h.currentSeg.Write(h.dataBuf)
					h.dataBuf = h.dataBuf[:0]
				}
				h.currentSeg.Close()
				h.currentSeg = nil
			}
			h.mutex.Unlock()
			return
		}

		if chunk, ok := data.([]byte); ok {
			h.mutex.Lock()
			h.processChunk(chunk)
			h.mutex.Unlock()
		}
	}
}

// processChunk 处理数据块（缓冲并按策略写入当前片段）
func (h *HLSSegmentManager) processChunk(chunk []byte) {
	// 累积到缓冲区
	h.dataBuf = append(h.dataBuf, chunk...)

	// 如果没有片段或已超过片段时长则创建新片段
	if h.currentSeg == nil || time.Since(h.segStart) >= time.Duration(h.segmentDuration)*time.Second {
		h.createNextSegment()
	}

	// 根据数据量或时间触发写入，防止过多内存积累
	if len(h.dataBuf) > 128*1024 || time.Since(h.lastWriteTime) > 150*time.Millisecond {
		if h.currentSeg != nil && len(h.dataBuf) > 0 {
			_, _ = h.currentSeg.Write(h.dataBuf)
			h.dataBuf = h.dataBuf[:0]
			h.lastWriteTime = time.Now()
		}
	}
}

// createNextSegment 创建下一个HLS片段，并在开头写入 PAT/PMT
func (h *HLSSegmentManager) createNextSegment() {
	// 关闭当前片段
	if h.currentSeg != nil {
		// 写入剩余数据
		if len(h.dataBuf) > 0 {
			_, _ = h.currentSeg.Write(h.dataBuf)
			h.dataBuf = h.dataBuf[:0]
		}
		h.currentSeg.Close()
	}

	// 更新片段编号
	h.segNumber++

	// 创建新片段文件，使用流名称作为前缀
	segName := fmt.Sprintf("%s_%d.ts", h.streamName, h.segNumber)
	segPath := filepath.Join(h.segmentPath, segName)

	file, err := os.Create(segPath)
	if err != nil {
		log.Printf("[%s] Failed to create segment file %s: %v", h.streamName, segPath, err)
		h.segNumber--
		return
	}

	h.currentSeg = file
	h.segStart = time.Now()

	// 写入 PAT/PMT 缓存（如果有），写一次以保证播放器能正确解析
	h.patPmtMutex.Lock()
	if len(h.patPmtBuf) >= 188 {
		// 写入PAT/PMT信息到片段开头
		if n, err := h.currentSeg.Write(h.patPmtBuf); err == nil {
			log.Printf("[%s] wrote %d bytes PAT/PMT to %s", h.streamName, n, segName)
		} else {
			log.Printf("[%s] error writing PAT/PMT to %s: %v", h.streamName, segName, err)
		}
	} else {
		log.Printf("[%s] Warning: PAT/PMT buffer too small (%d), segment may be unplayable: %s", h.streamName, len(h.patPmtBuf), segName)
	}
	h.patPmtMutex.Unlock()

	// 更新片段列表和 playlist
	h.segments = append(h.segments, segName)
	h.updatePlaylist()

	// 删除过旧片段并维护序号
	if len(h.segments) > h.segmentCount {
		oldSeg := h.segments[0]
		h.segments = h.segments[1:]
		_ = os.Remove(filepath.Join(h.segmentPath, oldSeg))
		h.segBaseNumber++
	}

	h.lastWriteTime = time.Now()
}

// updatePlaylist 更新HLS播放列表
func (h *HLSSegmentManager) updatePlaylist() {
	if !h.needPull {
		return
	}

	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:3\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", h.segmentDuration+1))
	playlist.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", h.segBaseNumber))

	for _, seg := range h.segments {
		playlist.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n", float64(h.segmentDuration)))
		playlist.WriteString(seg + "\n")
	}

	// 写入 playlist 文件
	if err := os.WriteFile(h.playlistPath, []byte(playlist.String()), 0644); err != nil {
		log.Printf("[%s] Failed to write playlist file: %v", h.streamName, err)
	}
	// 强制更新时间戳，帮助某些缓存策略
	_ = os.Chtimes(h.playlistPath, time.Now(), time.Now())
}

// cleanupLoop 清理过期的片段
func (h *HLSSegmentManager) cleanupLoop() {
	if !h.needPull {
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.cleanupSegments()
		}
	}
}

// cleanupSegments 清理过期片段
func (h *HLSSegmentManager) cleanupSegments() {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for len(h.segments) > h.segmentCount {
		oldSeg := h.segments[0]
		h.segments = h.segments[1:]
		oldSegPath := filepath.Join(h.segmentPath, oldSeg)
		_ = os.Remove(oldSegPath)
		h.segBaseNumber++
	}

	h.updatePlaylist()
}

// ServePlaylist 提供HLS播放列表服务
func (h *HLSSegmentManager) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	if !h.needPull {
		http.Error(w, "HLS not available", http.StatusNotFound)
		return
	}

	data, err := os.ReadFile(h.playlistPath)
	if err != nil {
		http.Error(w, "Playlist not available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(data)
}

// ServeSegment 提供HLS片段服务
func (h *HLSSegmentManager) ServeSegment(w http.ResponseWriter, r *http.Request, segmentName string) {
	if !h.needPull {
		http.Error(w, "HLS not available", http.StatusNotFound)
		return
	}

	segmentPath := filepath.Join(h.segmentPath, segmentName)
	if _, err := os.Stat(segmentPath); os.IsNotExist(err) {
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeFile(w, r, segmentPath)
}

// SetHub 设置hub引用
func (h *HLSSegmentManager) SetHub(hub *stream.StreamHubs) {
	h.hub = hub
}

// SetNeedPull 设置needPull标志
func (h *HLSSegmentManager) SetNeedPull(needPull bool) {
	h.needPull = needPull
}
