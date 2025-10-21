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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
)

// HLSSegmentManager 管理每个流的 HLS 输出（通过 hub -> FFmpeg 切片）
type HLSSegmentManager struct {
	streamName      string
	segmentPath     string // 输出目录，例如 /tmp/hls/<streamName>
	playlistPath    string // index.m3u8 的完整路径
	segmentDuration int
	segmentCount    int
	needPull        bool
	ffmpegOptions   *FFmpegOptions // 添加 FFmpeg 选项支持，用于配置HLS输出参数

	// 回放与保留配置
	enablePlayback     bool   // 若为 true，由 Go 渲染 m3u8 并由 Go 管理段删除
	retentionDays      int    // 保留 TS 的天数，<=0 表示不按天删除
	tsFilenameTemplate string // 模板，支持 {name} 和 {seq}，若为空使用默认 "%s_%03d.ts"

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
func NewHLSSegmentManager(parentCtx context.Context, streamName, baseDir string, segmentDuration int, ffmpegOptions *FFmpegOptions) *HLSSegmentManager {
	// 规范化 baseDir 路径
	baseDir = filepath.Clean(baseDir)

	var segmentPath string
	baseDirBase := filepath.Base(baseDir)

	// 检查 baseDir 是否已经以 streamName 结尾
	if baseDirBase == streamName {
		// 如果 baseDir 最后一级目录就是 streamName，则直接使用
		segmentPath = baseDir
	} else {
		// 否则追加 streamName
		segmentPath = filepath.Join(baseDir, streamName)
	}

	// 确保 segmentPath 也被规范化
	segmentPath = filepath.Clean(segmentPath)
	playlistPath := filepath.Join(segmentPath, "index.m3u8")
	ctx, cancel := context.WithCancel(parentCtx)

	return &HLSSegmentManager{
		streamName:      streamName,
		segmentPath:     segmentPath,
		playlistPath:    playlistPath,
		segmentDuration: segmentDuration,
		enablePlayback:  true, // 默认开启回放模式
		retentionDays:   7,    // 默认不按时间删除
		segmentCount:    5,    // 默认保留 5 个片段，可调整
		needPull:        true, // 默认为 true，后续会根据实际配置调整
		ffmpegOptions:   ffmpegOptions,
		ctx:             ctx,
		cancel:          cancel,
		// 默认 TS 文件名模板为 name_index：{name}_{seq}.ts（例如 cctv1_239.ts）
		tsFilenameTemplate: "name_index",
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

// SetPlayback 设置回放模式（由 Go 渲染 m3u8 并管理删除）
func (h *HLSSegmentManager) SetPlayback(enable bool) {
	h.enablePlayback = enable
}

// SetRetentionDays 设置 TS 保留天数（<=0 表示不按时间删除）
func (h *HLSSegmentManager) SetRetentionDays(days int) {
	h.retentionDays = days
}

// SetTsFilenameTemplate 设置 TS 文件名模板，支持 {name} 和 {seq}
// 例如: "{name}-{seq}.hls.ts" 或 "20251021T{seq}.ts"
// 若模板为空，使用默认 "%s_%%03d.ts"（即 cctv1_239.ts 格式）
func (h *HLSSegmentManager) SetTsFilenameTemplate(tpl string) {
	h.tsFilenameTemplate = tpl
}

// cleanupSegments 根据 retentionDays 清理旧的 TS 文件
func (h *HLSSegmentManager) cleanupSegments() {
	if h.retentionDays <= 0 {
		return
	}
	entries, err := os.ReadDir(h.segmentPath)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(h.retentionDays) * 24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".ts") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(h.segmentPath, name))
			logger.LogPrintf("[%s] removed old segment: %s", h.streamName, name)
		}
	}
}

// generatePlaylist 由 Go 渲染 index.m3u8（回放模式开启时）
func (h *HLSSegmentManager) generatePlaylist() {
	entries, err := os.ReadDir(h.segmentPath)
	if err != nil {
		return
	}
	type tsFile struct {
		name string
		mt   time.Time
	}
	var tsFiles []tsFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".ts") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		tsFiles = append(tsFiles, tsFile{name: e.Name(), mt: info.ModTime()})
	}
	if len(tsFiles) == 0 {
		// 如果没有 ts，更新 mtime 保持可用性
		_ = os.Chtimes(h.playlistPath, time.Now(), time.Now())
		return
	}
	sort.Slice(tsFiles, func(i, j int) bool {
		return tsFiles[i].mt.Before(tsFiles[j].mt)
	})
	// 取最后 N 段
	start := 0
	if len(tsFiles) > h.segmentCount {
		start = len(tsFiles) - h.segmentCount
	}
	selected := tsFiles[start:]

	// 尝试解析 media sequence 从第一个文件名提取数字（fallback 0）
	mediaSeq := 0
	// Go regexp 不支持 lookahead (?=...), 改为捕获尾部数字再匹配 ".ts"
	re := regexp.MustCompile(`(\d+)\.ts$`)
	if matches := re.FindStringSubmatch(selected[0].name); len(matches) >= 2 {
		fmt.Sscanf(matches[1], "%d", &mediaSeq)
	}

	// 生成 m3u8 内容
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", h.segmentDuration))
	b.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", mediaSeq))
	for _, f := range selected {
		// 使用固定 duration（近似）
		b.WriteString(fmt.Sprintf("#EXTINF:%d,\n", h.segmentDuration))
		b.WriteString(fmt.Sprintf("%s\n", f.name))
	}

	tmpPath := h.playlistPath + ".tmp"
	_ = os.WriteFile(tmpPath, []byte(b.String()), 0644)
	_ = os.Rename(tmpPath, h.playlistPath)
	_ = os.Chtimes(h.playlistPath, time.Now(), time.Now())
}

// updatePlaylist 更新 playlist 文件 mtime 或由 Go 渲染 m3u8（回放模式）
func (h *HLSSegmentManager) updatePlaylist() {
	if h.enablePlayback {
		h.generatePlaylist()
	} else {
		if _, err := os.Stat(h.playlistPath); err != nil {
			return
		}
		_ = os.Chtimes(h.playlistPath, time.Now(), time.Now())
	}
}

// Start 启动输出目录、注册 hub（若有）、并启动 FFmpeg 进程
func (h *HLSSegmentManager) Start() error {
	// 不再检查 needPull 标志，因为即使在转发模式下，HLS 也需要从 hub 获取数据
	// if !h.needPull {
	// 	return fmt.Errorf("needPull disabled")
	// }

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

	// FFmpeg 输出路径（标准格式或模板）
	segPattern := h.BuildSegmentFilenameTemplate()
	m3u8Path := h.playlistPath

	// 构建基础参数
	args := []string{
		"-f", "flv",
		"-i", "pipe:0",
	}

	// 添加自定义 FFmpeg 选项
	if h.ffmpegOptions != nil {
		// 添加视频编码器设置
		if h.ffmpegOptions.VideoCodec != "" {
			args = append(args, "-c:v", h.ffmpegOptions.VideoCodec)
		} else {
			args = append(args, "-c:v", "copy")
		}

		// 添加音频编码器设置
		if h.ffmpegOptions.AudioCodec != "" {
			args = append(args, "-c:a", h.ffmpegOptions.AudioCodec)
		} else {
			args = append(args, "-c:a", "copy")
		}

		// 添加视频码率
		if h.ffmpegOptions.VideoBitrate != "" {
			args = append(args, "-b:v", h.ffmpegOptions.VideoBitrate)
		}

		// 添加音频码率
		if h.ffmpegOptions.AudioBitrate != "" {
			args = append(args, "-b:a", h.ffmpegOptions.AudioBitrate)
		}

		// 添加预设
		if h.ffmpegOptions.Preset != "" {
			args = append(args, "-preset", h.ffmpegOptions.Preset)
		}

		// 添加 CRF
		if h.ffmpegOptions.CRF > 0 {
			args = append(args, "-crf", fmt.Sprintf("%d", h.ffmpegOptions.CRF))
		}

		// 添加像素格式
		if h.ffmpegOptions.PixFmt != "" {
			args = append(args, "-pix_fmt", h.ffmpegOptions.PixFmt)
		}

		// 添加 GOP 大小
		if h.ffmpegOptions.GopSize > 0 {
			args = append(args, "-g", fmt.Sprintf("%d", h.ffmpegOptions.GopSize))
		}

		// 添加输出前参数（这些参数会放在 -f hls 之前）
		if len(h.ffmpegOptions.OutputPreArgs) > 0 {
			args = append(args, h.ffmpegOptions.OutputPreArgs...)
		}
	} else {
		// 默认参数
		args = append(args, "-c:v", "copy", "-c:a", "copy")
	}

	// 添加 HLS 相关参数
	hlsFlags := "append_list"
	// 如果不是由 Go 回放管理，允许 delete_segments 交给 ffmpeg
	if !h.enablePlayback {
		hlsFlags = "delete_segments+append_list"
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", h.segmentDuration),
		"-hls_list_size", fmt.Sprintf("%d", h.segmentCount),
		"-hls_flags", hlsFlags,
		"-hls_segment_filename", segPattern,
		m3u8Path,
	)

	// 添加输出后参数
	if h.ffmpegOptions != nil && len(h.ffmpegOptions.OutputPostArgs) > 0 {
		args = append(args, h.ffmpegOptions.OutputPostArgs...)
	}

	cmd := exec.CommandContext(h.ctx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	setSysProcAttr(cmd.SysProcAttr)
	stdin, err := cmd.StdinPipe()
	// logger.LogPrintf("[%s] RTMP push command: ffmpeg %s", h.streamName, strings.Join(args, " "))
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
						// 检查上下文是否已取消
						if h.ctx.Err() != nil {
							return
						}

						h.mutex.Lock()
						ffmpegIn := h.ffmpegIn
						h.mutex.Unlock()

						// 检查ffmpegIn是否有效
						if ffmpegIn == nil {
							return
						}

						writeDone := make(chan error, 1)
						go func(d []byte) {
							_, err := ffmpegIn.Write(d)
							writeDone <- err
						}(data)

						select {
						case <-h.ctx.Done():
							return
						case err := <-writeDone:
							if err != nil {
								logger.LogPrintf("[%s] write to ffmpeg stdin error: %v", h.streamName, err)
								// 不直接调用h.Stop()，而是取消上下文让其他goroutine自行退出
								h.cancel()
								return
							}
						case <-time.After(5 * time.Second):
							logger.LogPrintf("[%s] timeout writing to ffmpeg stdin", h.streamName)
							// 不直接调用h.Stop()，而是取消上下文让其他goroutine自行退出
							h.cancel()
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
				h.cleanupSegments()
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

	// 等待所有goroutine完成，设置超时
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 正常完成
	case <-time.After(5 * time.Second):
		// 超时，强制清理
		logger.LogPrintf("[%s] HLS manager stop timeout, forcing cleanup", h.streamName)
	}

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

	// 清理clientBuffer
	if h.clientBuffer != nil {
		h.clientBuffer.Close()
		h.clientBuffer = nil
	}
	h.mutex.Unlock()

	logger.LogPrintf("[%s] HLS manager stopped", h.streamName)
	return nil
}

// parseTimestampFromFilename 尝试从文件名解析时间，支持多种格式。
// 返回解析到的时间与 ok 标志。若解析失败返回零时间和 false。
func parseTimestampFromFilename(name string) (time.Time, bool) {
	// 先尝试基于内置模板快速判断
	tpl := MatchFilenameTemplate(name)

	// 去掉扩展
	base := name
	if idx := strings.LastIndex(base, "."); idx > 0 {
		base = base[:idx]
	}

	switch tpl {
	case "date_underscore":
		// YYYYMMDD_HHMMSS
		reUnd := regexp.MustCompile(`(\d{8}_\d{6})`)
		if m := reUnd.FindStringSubmatch(base); len(m) >= 2 {
			s := strings.ReplaceAll(m[1], "_", "")
			if t, err := time.ParseInLocation("20060102150405", s, time.Local); err == nil {
				return t, true
			}
		}
	case "date_T":
		// YYYYMMDDTHHMMSS
		reT := regexp.MustCompile(`(\d{8}T\d{6})`)
		if m := reT.FindStringSubmatch(base); len(m) >= 2 {
			s := strings.ReplaceAll(m[1], "T", "")
			if t, err := time.ParseInLocation("20060102150405", s, time.Local); err == nil {
				return t, true
			}
		}
	case "epoch_hls", "epoch_dash", "numeric", "camera_hls":
		// 尝试从文件名中提取 10 位 epoch（优先）
		reEpoch := regexp.MustCompile(`\b(\d{10})\b`)
		if m := reEpoch.FindStringSubmatch(base); len(m) >= 2 {
			if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				return time.Unix(sec, 0), true
			}
		}
		// numeric 类型有可能就是 epoch（上面已处理），否则不解析为时间
	case "name_index":
		// name_index（例如 cctv1_239.ts）一般不是时间戳，尝试从文件 mtime 读取（上层会回退）
		return time.Time{}, false
	default:
		// 未命中模板或其它情况，按已有规则逐项尝试
		// 常见格式： YYYYMMDDhhmmss (14) / YYYYMMDD_HHMMSS / YYYYMMDDTHHMMSS
		re14 := regexp.MustCompile(`(\d{14})`)
		if m := re14.FindStringSubmatch(base); len(m) >= 2 {
			if t, err := time.ParseInLocation("20060102150405", m[1], time.Local); err == nil {
				return t, true
			}
		}
		reUnd := regexp.MustCompile(`(\d{8}_\d{6})`)
		if m := reUnd.FindStringSubmatch(base); len(m) >= 2 {
			s := strings.ReplaceAll(m[1], "_", "")
			if t, err := time.ParseInLocation("20060102150405", s, time.Local); err == nil {
				return t, true
			}
		}
		reT := regexp.MustCompile(`(\d{8}T\d{6})`)
		if m := reT.FindStringSubmatch(base); len(m) >= 2 {
			s := strings.ReplaceAll(m[1], "T", "")
			if t, err := time.ParseInLocation("20060102150405", s, time.Local); err == nil {
				return t, true
			}
		}
		reEpoch := regexp.MustCompile(`\b(\d{10})\b`)
		if m := reEpoch.FindStringSubmatch(base); len(m) >= 2 {
			if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				return time.Unix(sec, 0), true
			}
		}
	}

	return time.Time{}, false
}

// getTSFilesInRange 返回指定时间范围内的 ts 文件（按时间升序），时间来源优先取文件名解析时间，否则用 ModTime
func (h *HLSSegmentManager) getTSFilesInRange(start, end time.Time) ([]os.DirEntry, []time.Time, error) {
	entries, err := os.ReadDir(h.segmentPath)
	if err != nil {
		return nil, nil, err
	}
	type eWithTime struct {
		ent os.DirEntry
		t   time.Time
	}
	var list []eWithTime
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".ts") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		var t time.Time
		if pt, ok := parseTimestampFromFilename(name); ok {
			t = pt
		} else {
			t = info.ModTime()
		}
		// 包含端点： >= start && <= end
		if (t.Equal(start) || t.After(start)) && (t.Equal(end) || t.Before(end)) {
			list = append(list, eWithTime{ent: e, t: t})
		}
	}
	// 按时间升序排序
	sort.Slice(list, func(i, j int) bool { return list[i].t.Before(list[j].t) })
	ents := make([]os.DirEntry, 0, len(list))
	times := make([]time.Time, 0, len(list))
	for _, it := range list {
		ents = append(ents, it.ent)
		times = append(times, it.t)
	}
	return ents, times, nil
}

// buildPlaylistBytesFromEntries 根据筛选到的 ts 文件生成 m3u8 内容（返回字节）
func (h *HLSSegmentManager) buildPlaylistBytesFromEntries(ents []os.DirEntry) []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", h.segmentDuration))
	// media-sequence 设为 0（回放范围内按文件升序列出）
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	for _, e := range ents {
		// 使用固定 duration 近似
		b.WriteString(fmt.Sprintf("#EXTINF:%d,\n", h.segmentDuration))
		b.WriteString(fmt.Sprintf("%s\n", e.Name()))
	}
	return []byte(b.String())
}

// ServePlaylist 返回 m3u8
func (h *HLSSegmentManager) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	// 移除 needPull 检查，让所有模式都可以提供HLS服务
	// if !h.needPull {
	// 	http.Error(w, "HLS not available", http.StatusNotFound)
	// 	return
	// }
	
	// 检查文件是否存在
	if _, err := os.Stat(h.playlistPath); os.IsNotExist(err) {
		http.Error(w, "Playlist not available", http.StatusNotFound)
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
	// 移除 needPull 检查，让所有模式都可以提供HLS服务
	// if !h.needPull {
	// 	http.Error(w, "HLS not available", http.StatusNotFound)
	// 	return
	// }
	
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
// func (h *HLSSegmentManager) updatePlaylist() {
// 	if _, err := os.Stat(h.playlistPath); err != nil {
// 		return
// 	}
// 	_ = os.Chtimes(h.playlistPath, time.Now(), time.Now())
// }

// FilenameTemplate 定义了文件名模板及其匹配规则
type FilenameTemplate struct {
	Name        string
	Regex       *regexp.Regexp
	Example     string
	Description string
}

var (
	cameraHLSRegex      = regexp.MustCompile(`^([A-Za-z0-9]+)([A-Za-z]+)(\d+)-(\d+)-(\d+)-hls\.ts$`)
	epochHLSRegex       = regexp.MustCompile(`^\d{10}-\d+-\d+\.hls\.ts$`)
	epochDashRegex      = regexp.MustCompile(`^\d{10}-\d+-\d+(?:\.hls)?\.ts$`)
	numericRegex        = regexp.MustCompile(`^\d+\.ts$`)
	dateUnderscoreRegex = regexp.MustCompile(`^\d{8}_\d{6}\.ts$`)
	dateTRegex          = regexp.MustCompile(`^\d{8}T\d{6}\.ts$`)
	nameIndexRegex      = regexp.MustCompile(`^[A-Za-z0-9_]+_\d+\.ts$`)
)

var BuiltinFilenameTemplates = []FilenameTemplate{
	{Name: "epoch_hls", Regex: epochHLSRegex, Example: "1761015675-1-1746907626.hls.ts", Description: "10位 epoch 开头并带 .hls.ts"},
	{Name: "camera_hls", Regex: cameraHLSRegex, Example: "CCTV1gqh265-10737903-1-hls.ts", Description: "前缀 + 字母标识 + 编码数字 + -id1-id2-hls.ts"},
	{Name: "epoch_dash", Regex: epochDashRegex, Example: "1761016086-1-2716160.hls.ts", Description: "10位 epoch 开头，dash 分段，可能带 .hls.ts"},
	{Name: "numeric", Regex: numericRegex, Example: "40401764.ts", Description: "纯数字文件名（可能为 epoch）"},
	{Name: "date_underscore", Regex: dateUnderscoreRegex, Example: "20251021_110956.ts", Description: "YYYYMMDD_HHMMSS 格式"},
	{Name: "date_T", Regex: dateTRegex, Example: "20251021T111057.ts", Description: "YYYYMMDDTHHMMSS 格式"},
	{Name: "name_index", Regex: nameIndexRegex, Example: "cctv1_239.ts", Description: "name_index 格式（下划线+索引）"},
}

// MatchFilenameTemplate 返回匹配到的内置模板名，找不到返回空字符串
func MatchFilenameTemplate(fname string) string {
	for _, t := range BuiltinFilenameTemplates {
		if t.Regex.MatchString(fname) {
			return t.Name
		}
	}
	return ""
}

// ParseCameraHLSFilename 解析 camera_hls 格式文件名，返回 prefix, codecTag, codecDigits, id1, id2, ok
func ParseCameraHLSFilename(fname string) (prefix, codecTag, codecDigits, id1, id2 string, ok bool) {
	if m := cameraHLSRegex.FindStringSubmatch(fname); len(m) >= 6 {
		return m[1], m[2], m[3], m[4], m[5], true
	}
	return "", "", "", "", "", false
}

func (h *HLSSegmentManager) BuildSegmentFilenameTemplate() string {
	os.MkdirAll(h.segmentPath, 0755)

	now := time.Now()
	baseUnix := now.Unix() // 会话固定时间戳
	var pattern string

	switch h.tsFilenameTemplate {
	case "epoch_hls":
		// 例: 1761015675-1-%d.hls.ts
		pattern = fmt.Sprintf("%d-1-%%d.hls.ts",baseUnix)

	case "camera_hls":
		// 例: CCTV1gqh265-10737903-%d-hls.ts
		pattern = fmt.Sprintf("%sgqh265-10737903-%%d-hls.ts", strings.ToUpper(h.streamName))

	case "epoch_dash":
		// 例: 1761016086-1-%d.hls.ts
		pattern = fmt.Sprintf("%d-1-%%d.hls.ts", now.Unix())

	case "numeric":
		// 例: 1761016086%d.ts
		pattern = fmt.Sprintf("%d%%d.ts", now.Unix())

	case "date_underscore":
		// ✅ 日期每天变化：20251021_%d.ts、20251022_%d.ts
		// 只用年月日，不带时分秒
		pattern = fmt.Sprintf("%s_%%d.ts", now.Format("20060102"))

	case "date_T":
		// ✅ 日期每天变化：20251021T%d.ts
		pattern = fmt.Sprintf("%sT%%d.ts", now.Format("20060102"))

	case "name_index":
		pattern = fmt.Sprintf("%s_%%03d.ts", h.streamName)

	default:
		pattern = fmt.Sprintf("%s_%%03d.ts", h.streamName)
	}

	return filepath.ToSlash(filepath.Join(h.segmentPath, pattern))
}
