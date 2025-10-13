package publisher

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	// "sort"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
	"strings"
	"sync"
	"syscall"
	"time"
)

// HLSSegmentManager 管理每个流的 HLS 输出（通过 hub -> FFmpeg 切片）
type HLSSegmentManager struct {
	streamName      string
	segmentPath     string // 输出目录，例如 /tmp/hls/<streamName>
	playlistPath    string // index.m3u8 的完整路径
	segmentDuration int
	segmentCount    int
	needPull        bool

	// hub 相关
	hub          *stream.StreamHubs
	clientBuffer *ringbuffer.RingBuffer

	// ffmpeg 相关
	ffmpegCmd *exec.Cmd
	ffmpegIn  io.WriteCloser

	// 控制与同步
	ctx    context.Context
	cancel context.CancelFunc
	mutex  sync.Mutex
	wg     sync.WaitGroup
}

// NewHLSSegmentManager 创建新的管理器，每个流独立目录
func NewHLSSegmentManager(parentCtx context.Context, streamName, baseDir string, segmentDuration int) *HLSSegmentManager {
	// 🔧 自动防止路径重复，例如 baseDir 已经是 /tmp/hls/cctv1
	var segmentPath string
	if strings.HasSuffix(baseDir, string(os.PathSeparator)+streamName) || filepath.Base(baseDir) == streamName {
		segmentPath = baseDir
	} else {
		segmentPath = filepath.Join(baseDir, streamName)
	}

	playlistPath := filepath.Join(segmentPath, "index.m3u8")
	ctx, cancel := context.WithCancel(parentCtx)

	return &HLSSegmentManager{
		streamName:      streamName,
		segmentPath:     segmentPath,
		playlistPath:    playlistPath,
		segmentDuration: segmentDuration,
		segmentCount:    5, // 默认保留 5 个片段，可调整
		needPull:        true,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// SetHub 设置 hub 引用（可选）
func (h *HLSSegmentManager) SetHub(hub *stream.StreamHubs) {
	h.hub = hub
}

// SetNeedPull 设置 needPull 标志
func (h *HLSSegmentManager) SetNeedPull(need bool) {
	h.needPull = need
}

// Start 启动输出目录、注册 hub（若有）、并启动 FFmpeg 进程
func (h *HLSSegmentManager) Start() error {
	if !h.needPull {
		return fmt.Errorf("needPull disabled")
	}

	// 确保目录存在
	if err := os.MkdirAll(h.segmentPath, 0755); err != nil {
		return fmt.Errorf("failed to create segment dir: %v", err)
	}

	// 如果有 hub，则创建 clientBuffer 并注册
	if h.hub != nil {
		buf, err := ringbuffer.New(2 * 1024 * 1024) // 2MB
		if err != nil {
			return fmt.Errorf("failed to create client buffer: %v", err)
		}
		h.clientBuffer = buf
		h.hub.AddClient(h.clientBuffer)
		logger.LogPrintf("[%s] registered with hub", h.streamName)
	}

	// FFmpeg 输出路径（标准格式）
	segPattern := filepath.Join(h.segmentPath, fmt.Sprintf("%s_%%03d.ts", h.streamName))
	m3u8Path := h.playlistPath

	args := []string{
		"-f", "flv",
		"-i", "pipe:0",
		"-c:v", "copy",
		"-c:a", "copy",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", h.segmentDuration),
		"-hls_list_size", fmt.Sprintf("%d", h.segmentCount),
		"-hls_flags", "delete_segments+append_list",
		"-hls_segment_filename", segPattern,
		m3u8Path,
	}

	cmd := exec.CommandContext(h.ctx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	setSysProcAttr(cmd.SysProcAttr)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get ffmpeg stdin: %v", err)
	}
	// cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	h.mutex.Lock()
	h.ffmpegCmd = cmd
	h.ffmpegIn = stdin
	h.mutex.Unlock()

	// 启动数据推送（来自 hub）
	if h.clientBuffer != nil {
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			for {
				select {
				case <-h.ctx.Done():
					return
				default:
					item, ok := h.clientBuffer.PullWithContext(h.ctx)
					if !ok {
						return
					}
					if data, ok := item.([]byte); ok {
						writeDone := make(chan error, 1)
						go func(d []byte) {
							_, err := h.ffmpegIn.Write(d)
							writeDone <- err
						}(data)

						select {
						case err := <-writeDone:
							if err != nil {
								logger.LogPrintf("[%s] write to ffmpeg stdin error: %v", h.streamName, err)
								_ = h.Stop()
								return
							}
						case <-time.After(5 * time.Second):
							logger.LogPrintf("[%s] timeout writing to ffmpeg stdin", h.streamName)
							_ = h.Stop()
							return
						}
					}
				}
			}
		}()
	}

	// 定期清理任务
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-h.ctx.Done():
				return
			case <-ticker.C:
				// h.cleanupSegments()
				h.updatePlaylist()
			}
		}
	}()

	// log.Printf("[%s] Started HLS manager and ffmpeg (output: %s)", h.streamName, h.segmentPath)
	return nil
}

// Stop 停止管理器并清理
func (h *HLSSegmentManager) Stop() error {
	h.cancel()

	h.mutex.Lock()
	if h.ffmpegIn != nil {
		_ = h.ffmpegIn.Close()
		h.ffmpegIn = nil
	}
	if h.ffmpegCmd != nil && h.ffmpegCmd.Process != nil {
		_ = h.ffmpegCmd.Process.Signal(syscall.SIGTERM)
		waitCh := make(chan struct{})
		go func() {
			h.ffmpegCmd.Wait()
			close(waitCh)
		}()
		select {
		case <-waitCh:
		case <-time.After(1 * time.Second):
			_ = killProcess(h.ffmpegCmd.Process.Pid)
		}
		h.ffmpegCmd = nil
	}
	h.mutex.Unlock()

	h.wg.Wait()
	// log.Printf("[%s] HLS manager stopped", h.streamName)
	return nil
}

// ServePlaylist 返回 m3u8
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

// ServeSegment 提供 ts 文件
func (h *HLSSegmentManager) ServeSegment(w http.ResponseWriter, r *http.Request, segmentName string) {
	if !h.needPull {
		http.Error(w, "HLS not available", http.StatusNotFound)
		return
	}
	segmentPath := filepath.Join(h.segmentPath, segmentName)
	if _, err := os.Stat(segmentPath); os.IsNotExist(err) {
		log.Printf("[%s] Segment not found: %s", h.streamName, segmentPath)
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeFile(w, r, segmentPath)
	// log.Printf("[%s] Served segment: %s", h.streamName, segmentName)
}

// updatePlaylist 更新 playlist 文件 mtime
func (h *HLSSegmentManager) updatePlaylist() {
	if _, err := os.Stat(h.playlistPath); err != nil {
		return
	}
	_ = os.Chtimes(h.playlistPath, time.Now(), time.Now())
}
