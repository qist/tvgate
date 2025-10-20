package publisher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	// "strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/stream"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
	"github.com/shirou/gopsutil/v3/process"
)

// StreamHub 管理流的生命周期和客户端连接
type StreamHub struct {
	streamName string
	primary    *PipeForwarder
	backup     *PipeForwarder
	hub        *stream.StreamHubs
	hlsManager *HLSSegmentManager // 添加 HLS 管理器

	// 添加context用于管理生命周期
	ctx    context.Context
	cancel context.CancelFunc

	// 添加数据状态跟踪
	dataReceived bool
	dataMutex    sync.RWMutex

	// 添加额外的转发器列表（用于 all 模式）
	extraForwarders []*PipeForwarder
	mutex           sync.RWMutex
}

// StreamHubManager 管理所有流的StreamHub
type StreamHubManager struct {
	hubs  map[string]*StreamHub
	mutex sync.RWMutex
}

var streamHubManager = &StreamHubManager{
	hubs: make(map[string]*StreamHub),
}

// GetStreamHub 获取或创建StreamHub
func GetStreamHub(streamName string) *StreamHub {
	streamHubManager.mutex.Lock()
	defer streamHubManager.mutex.Unlock()

	// 移除后缀获取基础流名称
	baseStreamName := streamName

	if strings.HasSuffix(streamName, "_primary") {
		baseStreamName = strings.TrimSuffix(streamName, "_primary")
	} else if strings.HasSuffix(streamName, "_backup") {
		baseStreamName = strings.TrimSuffix(streamName, "_backup")
	} else if strings.Contains(streamName, "_receiver_") {
		baseStreamName = strings.Split(streamName, "_receiver_")[0]
	}

	if hub, exists := streamHubManager.hubs[baseStreamName]; exists {
		return hub
	}

	// 创建新的StreamHub
	ctx, cancel := context.WithCancel(context.Background())
	newHub := &StreamHub{
		streamName:   baseStreamName,
		hub:          stream.NewStreamHubs(),
		ctx:          ctx,
		cancel:       cancel,
		dataReceived: false,
	}
	streamHubManager.hubs[baseStreamName] = newHub
	return newHub
}

// SetPrimary 设置主推流器
func (sh *StreamHub) SetPrimary(pf *PipeForwarder) {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	sh.primary = pf

	// 确保主推流器使用共享的hub
	if pf != nil && sh.hub != nil {
		pf.hub = sh.hub
	}
}

// GetDataReceived 检查是否已接收到数据
func (sh *StreamHub) GetDataReceived() bool {
	sh.dataMutex.RLock()
	defer sh.dataMutex.RUnlock()
	return sh.dataReceived
}

// SetDataReceived 设置数据接收状态
func (sh *StreamHub) SetDataReceived() {
	sh.dataMutex.Lock()
	defer sh.dataMutex.Unlock()
	sh.dataReceived = true
}

// ResetDataReceived 重置数据接收状态
func (sh *StreamHub) ResetDataReceived() {
	sh.dataMutex.Lock()
	defer sh.dataMutex.Unlock()
	sh.dataReceived = false
}

// AddExtraForwarder 添加额外的转发器（用于 all 模式）
func (sh *StreamHub) AddExtraForwarder(pf *PipeForwarder) {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	sh.extraForwarders = append(sh.extraForwarders, pf)
}

// GetActiveForwarder 获取当前活跃的推流器
func (sh *StreamHub) GetActiveForwarder() *PipeForwarder {
	sh.mutex.RLock()
	defer sh.mutex.RUnlock()

	// 优先使用主推流器
	if sh.primary != nil && sh.primary.IsRunning() {
		return sh.primary
	}

	// 检查额外的转发器（all 模式）
	for _, pf := range sh.extraForwarders {
		if pf != nil && pf.IsRunning() {
			return pf
		}
	}

	// 备用推流器兜底
	if sh.backup != nil && sh.backup.IsRunning() {
		return sh.backup
	}

	// 如果都没有运行，返回主推流器（可能正在重启）
	if sh.primary != nil {
		return sh.primary
	}

	return sh.backup
}

// SetBackup 设置备份推流器
func (sh *StreamHub) SetBackup(pf *PipeForwarder) {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	sh.backup = pf

	// 确保备份推流器使用共享的hub
	if pf != nil && sh.hub != nil {
		pf.hub = sh.hub
	}
}

// GetHub 获取共享的hub
func (sh *StreamHub) GetHub() *stream.StreamHubs {
	return sh.hub
}

// GetContext 获取StreamHub的context
func (sh *StreamHub) GetContext() context.Context {
	return sh.ctx
}

// Close 关闭StreamHub
func (sh *StreamHub) Close() {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	if sh.cancel != nil {
		sh.cancel()
	}

	if sh.hub != nil {
		sh.hub.Close()
	}
}

// SwitchToBackup 通知StreamHub切换到备用推流器
func (sh *StreamHub) SwitchToBackup() {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	logger.LogPrintf("[%s] Attempting to switch to backup forwarder", sh.streamName)

	if sh.backup != nil {
		logger.LogPrintf("[%s] Backup forwarder found, ensuring it's running", sh.streamName)
		// 检查备用推流器是否已经在运行
		if !sh.backup.IsRunning() {
			// 获取流管理器以构建FFmpeg命令
			manager := GetManager()
			if manager != nil {
				manager.mutex.RLock()
				streamManager, exists := manager.streams[sh.streamName]
				manager.mutex.RUnlock()

				if exists {
					// 构建FFmpeg命令（使用主URL，因为备用推流器会处理backup URL）
					ffmpegCmd := streamManager.buildFFmpegCommandWithBackup(false)

					// 启动备用推流器
					if err := sh.backup.Start(ffmpegCmd); err != nil {
						logger.LogPrintf("[%s] Failed to start backup forwarder: %v", sh.streamName, err)
					} else {
						logger.LogPrintf("[%s] Backup forwarder started successfully", sh.streamName)
					}
				} else {
					logger.LogPrintf("[%s] Stream manager not found", sh.streamName)
				}
			} else {
				logger.LogPrintf("[%s] Manager not found", sh.streamName)
			}
		} else {
			logger.LogPrintf("[%s] Backup forwarder is already running", sh.streamName)
		}
	} else {
		logger.LogPrintf("[%s] No backup forwarder configured", sh.streamName)
	}
}

// PipeForwarder 将 FFmpeg 的输出写入 io.Pipe，然后 Go 程序读取并分发到 HTTP-FLV 客户端和可选 RTMP 推流
type PipeForwarder struct {
	streamName string
	rtmpURL    string // RTMP推流URL（如果有）
	enabled    bool
	needPull   bool // 新增标志，标识是否需要拉流

	ffmpegCmd *exec.Cmd

	ctx    context.Context
	cancel context.CancelFunc

	mutex     sync.Mutex
	isRunning bool

	onStarted func()
	onStopped func()

	hub *stream.StreamHubs

	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter

	// header 缓存（用于补发给中途加入的 HTTP-FLV 客户端）
	firstTagBuf    bytes.Buffer
	headerBuf      bytes.Buffer
	headerCaptured bool
	headerMutex    sync.Mutex
	// firstTagOnce   sync.Once
	// 用于从hub读取数据的客户端缓冲区
	clientBuffer *ringbuffer.RingBuffer

	// HLS支持
	hlsManager *HLSSegmentManager
	hlsEnabled bool // 添加这个字段来控制HLS启用状态

	// PAT/PMT缓存，确保每个HLS片段都包含这些信息
	patPmtBuf bytes.Buffer

	ffmpegPush *exec.Cmd

	ffmpegLock sync.Mutex
	// FFmpeg进程状态监控
	stats *FFmpegProcessStats

	// 保护 ffIn 变量的互斥锁
	ffInLock sync.Mutex

	hlsFFmpegOptions *FFmpegOptions
}

// NewPipeForwarder 创建新的 PipeForwarder
// NewPipeForwarder 创建新的 PipeForwarder
func NewPipeForwarder(streamName string, rtmpURL string, enabled bool, needPull bool, hub *stream.StreamHubs) *PipeForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	var h *stream.StreamHubs
	if hub != nil {
		h = hub
	} else {
		h = stream.NewStreamHubs()
	}

	// 初始化 HLS 管理器
	// 使用基础流名称（去除_primary、_backup等后缀）生成HLS路径
	baseStreamName := streamName
	if strings.Contains(baseStreamName, "_") {
		// 提取第一个下划线之前的部分作为基础流名称
		parts := strings.Split(baseStreamName, "_")
		baseStreamName = parts[0]
	}

	// segmentPath := filepath.Join("/tmp/hls", baseStreamName)
	// os.MkdirAll(segmentPath, 0755)

	// 获取 HLS 配置
	var hlsFFmpegOptions *FFmpegOptions
	hlsSegmentDuration := 5 // 默认值
	hlsSegmentCount := 5    // 默认值
	hlsPath := ""           // 默认空，使用默认路径
	
	manager := GetManager()
	if manager != nil {
		manager.mutex.RLock()
		streamManager, exists := manager.streams[baseStreamName]
		manager.mutex.RUnlock()

		if exists {
			// 查找 HLS 配置
			for _, playURL := range streamManager.stream.Stream.LocalPlayUrls {
				if playURL.Protocol == "hls" && playURL.Enabled {
					hlsFFmpegOptions = playURL.HlsFFmpegOptions
					// 使用配置的HLS段时长，如果配置了的话
					if playURL.HlsSegmentDuration > 0 {
						hlsSegmentDuration = playURL.HlsSegmentDuration
					}
					// 使用配置的HLS段数量，如果配置了的话
					if playURL.HlsSegmentCount > 0 {
						hlsSegmentCount = playURL.HlsSegmentCount
					}
					// 使用配置的HLS路径，如果配置了的话
					if playURL.HlsPath != "" {
						hlsPath = playURL.HlsPath
					}
					break
				}
			}
		}
	}

	// 确定HLS段路径
	var segmentPath string
	if hlsPath != "" {
		// 如果配置了HLS路径，则使用配置的路径
		segmentPath = hlsPath
	} else {
		// 否则使用默认路径
		segmentPath = filepath.Join("/tmp/hls", baseStreamName)
	}
	
	// 确保目录存在
	os.MkdirAll(segmentPath, 0755)

	// 创建 HLS 管理器，传递正确的参数包括段时长和段数量
	hlsManager := NewHLSSegmentManager(ctx, baseStreamName, segmentPath, hlsSegmentDuration, hlsFFmpegOptions)
	hlsManager.segmentCount = hlsSegmentCount // 设置段数量
	hlsManager.SetHub(h)
	hlsManager.SetNeedPull(needPull) // 设置needPull标志

	// 获取或创建 StreamHub
	streamHub := GetStreamHub(streamName)
	streamHub.hlsManager = hlsManager // 将 HLS 管理器设置到 StreamHub

	// 创建新的 PipeForwarder 实例
	pipeForwarder := &PipeForwarder{
		streamName: streamName,
		rtmpURL:    rtmpURL,
		enabled:    enabled,
		needPull:   needPull,
		ctx:        ctx,
		cancel:     cancel,
		hub:        h,
		hlsManager: hlsManager,
		hlsEnabled: true, // 默认启用，后续会根据配置调整
	}
	// 初始化 FFmpegProcessStats
	pipeForwarder.stats = &FFmpegProcessStats{
		StreamName:    streamName,
		ReceiverIndex: -1, // -1 表示 PipeForwarder
		StartTime:     time.Now(),
		Running:       false,
		LastError:     "",
	}

	// 根据流名称后缀注册为主或备推流器
	if strings.HasSuffix(streamName, "_primary") {
		streamHub.SetPrimary(pipeForwarder)
	} else if strings.HasSuffix(streamName, "_backup") {
		streamHub.SetBackup(pipeForwarder)
	}

	return pipeForwarder
}

// SetCallbacks 设置启动和停止回调
func (pf *PipeForwarder) SetCallbacks(onStarted, onStopped func()) {
	pf.onStarted = onStarted
	pf.onStopped = onStopped
}

// Start 启动 PipeForwarder 并运行 FFmpeg（args 是原始 ffmpeg 参数切片，不应包含 pipe:1 输出）
func (pf *PipeForwarder) Start(ffmpegArgs []string) error {
	pf.mutex.Lock()
	defer pf.mutex.Unlock()

	if !pf.enabled {
		logger.LogPrintf("[%s] PipeForwarder disabled, skipping start", pf.streamName)
		return nil
	}
	if pf.isRunning {
		logger.LogPrintf("[%s] PipeForwarder already running", pf.streamName)
		return nil
	}

	// 初始化 FFmpegProcessStats
	pf.stats = &FFmpegProcessStats{
		StreamName:    pf.streamName,
		ReceiverIndex: -1, // -1 表示 PipeForwarder
		StartTime:     time.Now(),
		Running:       false,
		LastError:     "",
	}

	// 如果需要拉流，则创建 io.Pipe 并启动 ffmpeg 拉流进程
	if pf.needPull {
		// 创建 io.Pipe
		pf.pipeReader, pf.pipeWriter = io.Pipe()

		// 修改 ffmpeg 命令，确保输出为 pipe:1
		modArgs := pf.modifyFFmpegCommand(ffmpegArgs)

		// 启动 ffmpeg（主进程，输出到 pipe:1）
		pf.ffmpegCmd = exec.CommandContext(pf.ctx, "ffmpeg", modArgs...)
		pf.ffmpegCmd.SysProcAttr = &syscall.SysProcAttr{}
		setSysProcAttr(pf.ffmpegCmd.SysProcAttr)
		// pf.ffmpegCmd.Stderr = os.Stderr
		pf.ffmpegCmd.Stdout = pf.pipeWriter

		if err := pf.ffmpegCmd.Start(); err != nil {
			// 启动失败，清理 pipe
			_ = pf.pipeReader.Close()
			_ = pf.pipeWriter.Close()
			pf.pipeReader = nil
			pf.pipeWriter = nil

			// 更新错误状态
			pf.stats.LastError = err.Error()
			pf.stats.LastUpdate = time.Now()
			return fmt.Errorf("failed to start ffmpeg: %v", err)
		}

		// 更新运行状态
		pf.stats.PID = int32(pf.ffmpegCmd.Process.Pid)
		pf.stats.Running = true
		pf.stats.LastUpdate = time.Now()

		logger.LogPrintf("[%s] Started PipeForwarder, ffmpeg pid=%d", pf.streamName, pf.ffmpegCmd.Process.Pid)
		logger.LogPrintf("[%s] Full FFmpeg command: ffmpeg %s", pf.streamName, strings.Join(modArgs, " "))
	} else {
		// 不需要拉流，创建客户端缓冲区从hub读取数据
		var err error
		pf.clientBuffer, err = ringbuffer.New(1024 * 1024)
		if err != nil {
			logger.LogPrintf("[%s] Failed to create client buffer: %v", pf.streamName, err)

			// 更新错误状态
			pf.stats.LastError = err.Error()
			pf.stats.LastUpdate = time.Now()
			return fmt.Errorf("failed to create client buffer: %v", err)
		}

		logger.LogPrintf("[%s] Started PipeForwarder in forward-only mode", pf.streamName)

		// 设置运行状态（虽然没有 FFmpeg 进程，但我们认为组件是运行的）
		pf.stats.Running = true
		pf.stats.LastUpdate = time.Now()
	}

	pf.isRunning = true

	// 清空 header 缓存状态
	pf.headerMutex.Lock()
	pf.headerBuf.Reset()
	pf.headerCaptured = false
	pf.patPmtBuf.Reset()
	pf.headerMutex.Unlock()

	// 启动 HLS 管理器
	if err := pf.hlsManager.Start(); err != nil {
		logger.LogPrintf("[%s] Warning: Failed to start HLS manager: %v", pf.streamName, err)
	} else {
		logger.LogPrintf("[%s] HLS manager started successfully", pf.streamName)
	}

	// 启动数据转发 goroutine
	go pf.forwardDataFromPipe()

	// 如果需要拉流，启动 wait goroutine，等待 ffmpeg 退出并清理
	if pf.needPull && pf.ffmpegCmd != nil {
		go pf.waitWithBackupSupport(ffmpegArgs)
	}

	// 触发启动回调
	if pf.onStarted != nil {
		go pf.onStarted()
	}

	return nil
}

// modifyFFmpegCommand 修改 ffmpeg 参数，剔除输出 URL、确保输出到 pipe:1，并保证 flv 输出格式
// 说明：期望输入 args 包含 -i ... 等参数，但可能也存在输出 target，本函数会尝试移除输出 target。
func (pf *PipeForwarder) modifyFFmpegCommand(args []string) []string {
	if len(args) == 0 {
		args = []string{}
	}

	// 获取流管理器配置
	manager := GetManager()
	var sourceOptions, flvOptions *FFmpegOptions
	if manager != nil {
		streamName := pf.streamName
		if strings.Contains(streamName, "_receiver_") {
			streamName = strings.Split(streamName, "_receiver_")[0]
		} else if strings.HasSuffix(streamName, "_primary") {
			streamName = strings.TrimSuffix(streamName, "_primary")
		} else if strings.HasSuffix(streamName, "_backup") {
			streamName = strings.TrimSuffix(streamName, "_backup")
		}

		manager.mutex.RLock()
		streamManager, exists := manager.streams[streamName]
		manager.mutex.RUnlock()

		if exists {
			sourceOptions = streamManager.stream.Stream.Source.FFmpegOptions
			for _, playURL := range streamManager.stream.Stream.LocalPlayUrls {
				if playURL.Protocol == "flv" && playURL.Enabled {
					flvOptions = playURL.FlvFFmpegOptions
					break
				}
			}
		}
	}

	mergedArgs := pf.mergeFFmpegOptionsOrdered(args, sourceOptions, flvOptions)

	// 低延迟参数
	lowLatency := [][2]string{
		{"-fflags", "+genpts"},
		{"-avioflags", "direct"},
		{"-flush_packets", "1"},
		{"-flvflags", "no_duration_filesize"},
		{"-keyint_min", "1"},
	}

	existingFlags := make(map[string]bool)
	for i := 0; i < len(mergedArgs); i++ {
		if strings.HasPrefix(mergedArgs[i], "-") {
			existingFlags[mergedArgs[i]] = true
		}
	}

	for _, pair := range lowLatency {
		if !existingFlags[pair[0]] {
			mergedArgs = append(mergedArgs, pair[0], pair[1])
		}
	}

	// 确保输出只追加一次 -f flv pipe:1
	needOutput := true
	for i := 0; i < len(mergedArgs); i++ {
		if mergedArgs[i] == "pipe:1" {
			needOutput = false
			break
		}
	}
	if needOutput {
		mergedArgs = append(mergedArgs, "-f", "flv", "pipe:1")
	}

	return mergedArgs
}

// mergeFFmpegOptionsOrdered 保留顺序的 merge，后来的 options 覆盖前面的值
func (pf *PipeForwarder) mergeFFmpegOptionsOrdered(baseArgs []string, sourceOptions, flvOptions *FFmpegOptions) []string {
	type argVal struct {
		flag string
		val  string
	}

	result := []argVal{}
	argIndex := make(map[string]int) // flag -> 在 result 中的下标，用于覆盖

	add := func(flag, val string) {
		if idx, exists := argIndex[flag]; exists {
			result[idx].val = val // 覆盖
		} else {
			result = append(result, argVal{flag, val})
			argIndex[flag] = len(result) - 1
		}
	}

	// 添加 baseArgs
	for i := 0; i < len(baseArgs); i++ {
		arg := baseArgs[i]
		if strings.HasPrefix(arg, "-") && i+1 < len(baseArgs) && !strings.HasPrefix(baseArgs[i+1], "-") {
			add(arg, baseArgs[i+1])
			i++
		} else {
			add(arg, "")
		}
	}

	// 合并 sourceOptions -> flvOptions
	optionsList := []*FFmpegOptions{}
	if sourceOptions != nil {
		optionsList = append(optionsList, sourceOptions)
	}
	if flvOptions != nil {
		optionsList = append(optionsList, flvOptions)
	}

	for _, opts := range optionsList {
		for _, a := range opts.InputPreArgs {
			add(a, "")
		}
		if opts.VideoCodec != "" {
			add("-c:v", opts.VideoCodec)
		}
		if opts.AudioCodec != "" {
			add("-c:a", opts.AudioCodec)
		}
		if opts.VideoBitrate != "" {
			add("-b:v", opts.VideoBitrate)
		}
		if opts.AudioBitrate != "" {
			add("-b:a", opts.AudioBitrate)
		}
		if opts.Preset != "" {
			add("-preset", opts.Preset)
		}
		if opts.CRF > 0 {
			add("-crf", fmt.Sprintf("%d", opts.CRF))
		}
		if opts.PixFmt != "" {
			add("-pix_fmt", opts.PixFmt)
		}
		if opts.GopSize > 0 {
			add("-g", fmt.Sprintf("%d", opts.GopSize))
		}
		i := 0
		for i < len(opts.OutputPreArgs) {
			if opts.OutputPreArgs[i] == "-f" {
				i += 2 // 跳过 -f 和它的值
				continue
			}
			result = append(result, argVal{flag: opts.OutputPreArgs[i], val: ""})
			i++
		}
	}

	// 重新生成参数列表
	finalArgs := []string{}
	for _, av := range result {
		if av.flag == "-f" {
			continue // 删除所有 -f
		}
		if av.val != "" {
			finalArgs = append(finalArgs, av.flag, av.val)
		} else {
			finalArgs = append(finalArgs, av.flag)
		}
	}
	return finalArgs
}

// removeArg 从参数列表中移除指定的参数及其值
func removeArg(args []string, argToRemove string) []string {
	result := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == argToRemove {
			// 跳过参数和它的值（如果有的话）
			if i+1 < len(args) && len(args[i+1]) > 0 && args[i+1][0] != '-' {
				i += 2 // 跳过参数和值
			} else {
				i += 1 // 只跳过参数
			}
			continue
		}
		result = append(result, arg)
		i++
	}
	return result
}

// forwardDataFromPipe 从 pipeReader 读取数据并分发到 hub 与可选 RTMP 推流
func (pf *PipeForwarder) forwardDataFromPipe() {
	var ffIn io.WriteCloser
	var pushWg sync.WaitGroup

	// 检查是否有配置接收器
	hasReceivers := false
	manager := GetManager()
	var streamManager *StreamManager
	var receivers []Receiver

	if manager != nil {
		// 提取流名称，去掉可能的后缀（如 _receiver_2, _primary, _backup）
		streamName := pf.streamName
		if strings.Contains(streamName, "_receiver_") {
			parts := strings.Split(streamName, "_receiver_")
			streamName = parts[0]
		} else if strings.HasSuffix(streamName, "_primary") {
			streamName = strings.TrimSuffix(streamName, "_primary")
		} else if strings.HasSuffix(streamName, "_backup") {
			streamName = strings.TrimSuffix(streamName, "_backup")
		}

		manager.mutex.RLock()
		var exists bool
		streamManager, exists = manager.streams[streamName]
		manager.mutex.RUnlock()

		// 检查是否配置了接收器
		if exists && streamManager != nil {
			// 获取接收器列表
			receivers = streamManager.stream.Stream.GetReceivers()
			if streamManager.stream.Stream.Mode == "primary-backup" {
				// primary-backup 模式
				if streamManager.stream.Stream.Receivers.Primary != nil ||
					streamManager.stream.Stream.Receivers.Backup != nil {
					hasReceivers = true
				}
			} else if streamManager.stream.Stream.Mode == "all" {
				// all 模式
				if len(streamManager.stream.Stream.Receivers.All) > 0 {
					hasReceivers = true
				}
			}
		}
	}

	// 只有在确实配置了接收器时才启用 RTMP 推流
	if hasReceivers && streamManager != nil {
		// 根据模式和接收器配置构建推流命令
		receiver := findReceiverForStream(receivers, pf.streamName, pf.rtmpURL)
		var ffmpegOpts *FFmpegOptions

		if receiver != nil && receiver.FFmpegOptions != nil {
			ffmpegOpts = receiver.FFmpegOptions
		} else {
			ffmpegOpts = receivers[0].FFmpegOptions
		}

		cmd := pf.buildPushCommandWithReceiverOptions(ffmpegOpts, pf.rtmpURL)

		// 创建推流命令
		pf.ffmpegLock.Lock()
		pf.ffmpegPush = exec.CommandContext(pf.ctx, "ffmpeg", cmd...)
		pf.ffmpegLock.Unlock()

		var err error
		ffIn, err = pf.ffmpegPush.StdinPipe()
		if err != nil {
			logger.LogPrintf("[%s] Failed to create stdin pipe for RTMP push: %v", pf.streamName, err)
			ffIn = nil
		} else {
			pf.ffmpegPush.Stdout = os.Stdout
			// pf.ffmpegPush.Stderr = os.Stderr

			if err := pf.ffmpegPush.Start(); err != nil {
				logger.LogPrintf("[%s] Failed to start RTMP push: %v", pf.streamName, err)
				_ = ffIn.Close()
				ffIn = nil
			} else {
				logger.LogPrintf("[%s] Started RTMP push to %s (pid=%d)",
					pf.streamName, pf.rtmpURL, pf.ffmpegPush.Process.Pid)
				logger.LogPrintf("[%s] RTMP push command: ffmpeg %s", pf.streamName, strings.Join(cmd, " "))

				pushWg.Add(1)
				go func() {
					defer pushWg.Done()
					if err := pf.ffmpegPush.Wait(); err != nil && pf.ctx.Err() == nil {
						logger.LogPrintf("[%s] RTMP push ffmpeg exited with error: %v", pf.streamName, err)
					} else {
						logger.LogPrintf("[%s] RTMP push ffmpeg exited normally", pf.streamName)
					}

					// 推流结束后清理
					pf.ffmpegLock.Lock()
					pf.ffmpegPush = nil
					pf.ffmpegLock.Unlock()
				}()
			}
		}
	}

	// 标记 hub 为播放状态
	pf.hub.SetPlaying()

	// 根据 needPull 参数决定数据源
	if pf.needPull {
		// 从管道读取数据（主拉流实例）
		buf := make([]byte, 32*1024)
		chunkCount := 0
		for {
			select {
			case <-pf.ctx.Done():
				logger.LogPrintf("[%s] context canceled, stopping forwardDataFromPipe, chunks: %d", pf.streamName, chunkCount)
				pf.ffInLock.Lock()
				if ffIn != nil {
					_ = ffIn.Close()
					ffIn = nil
				}
				pf.ffInLock.Unlock()
				pushWg.Wait()
				return
			default:
				n, err := pf.pipeReader.Read(buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					chunkCount++

					// 在 header 未捕获时缓存前段数据
					pf.headerMutex.Lock()
					if !pf.headerCaptured {
						if pf.headerBuf.Len() < 4*1024 {
							pf.headerBuf.Write(chunk)
						}

						b := pf.headerBuf.Bytes()
						if len(b) >= 9 && b[0] == 'F' && b[1] == 'L' && b[2] == 'V' {
							pf.headerCaptured = true
							logger.LogPrintf("[%s] FLV header captured (%d bytes)", pf.streamName, pf.headerBuf.Len())
						}
					}
					pf.headerMutex.Unlock()

					// 写入 RTMP 推流进程 stdin（如果有）
					pf.ffInLock.Lock()
					currentFFIn := ffIn
					pf.ffInLock.Unlock()

					if currentFFIn != nil {
						wDone := make(chan error, 1)
						go func() {
							_, werr := currentFFIn.Write(chunk)
							wDone <- werr
						}()

						select {
						case werr := <-wDone:
							if werr != nil {
								// 检查是否是预期的关闭错误
								if werr == io.ErrClosedPipe || strings.Contains(werr.Error(), "file already closed") ||
									strings.Contains(werr.Error(), "broken pipe") || strings.Contains(werr.Error(), "read/write on closed pipe") {
									logger.LogPrintf("[%s] Expected pipe close error: %v", pf.streamName, werr)
								} else {
									logger.LogPrintf("[%s] Error writing to RTMP stdin: %v", pf.streamName, werr)
								}
								pf.ffInLock.Lock()
								if ffIn != nil {
									_ = ffIn.Close()
									ffIn = nil
								}
								pf.ffInLock.Unlock()
							}
						case <-time.After(5 * time.Second):
							logger.LogPrintf("[%s] Timeout writing to RTMP stdin", pf.streamName)
							pf.ffInLock.Lock()
							if ffIn != nil {
								_ = ffIn.Close()
								ffIn = nil
							}
							pf.ffInLock.Unlock()
						}
					}

					// 广播到 hub（所有已注册客户端）
					pf.hub.Broadcast(chunk)

					// 在 header 未捕获时缓存前段数据（转发模式下也需要捕获头部信息）
					pf.headerMutex.Lock()
					if !pf.headerCaptured {
						if pf.headerBuf.Len() < 4*1024 {
							pf.headerBuf.Write(chunk)
						}

						b := pf.headerBuf.Bytes()
						if len(b) >= 9 && b[0] == 'F' && b[1] == 'L' && b[2] == 'V' {
							pf.headerCaptured = true
							logger.LogPrintf("[%s] FLV header captured (%d bytes)", pf.streamName, pf.headerBuf.Len())
						}
					}
					pf.headerMutex.Unlock()

					// 通知StreamHub已接收到数据
					streamHub := GetStreamHub(pf.streamName)
					if streamHub != nil && chunkCount <= 10 {
						streamHub.SetDataReceived()
						// logger.LogPrintf("[%s] Data received and broadcasted, chunk #%d", pf.streamName, chunkCount)
					}
				}

				if err != nil {
					// 检查上下文是否已取消
					if pf.ctx.Err() != nil {
						logger.LogPrintf("[%s] context canceled, stopping pipe read: %v", pf.streamName, err)
						pf.ffInLock.Lock()
						if ffIn != nil {
							_ = ffIn.Close()
							ffIn = nil
						}
						pf.ffInLock.Unlock()
						pushWg.Wait()
						return
					}

					if err == io.EOF {
						logger.LogPrintf("[%s] pipe EOF reached, chunks: %d", pf.streamName, chunkCount)
						pf.ffInLock.Lock()
						if ffIn != nil {
							_ = ffIn.Close()
							ffIn = nil
						}
						pf.ffInLock.Unlock()
						pushWg.Wait()
						return
					}

					if pf.ctx.Err() == nil {
						logger.LogPrintf("[%s] pipe read error: %v", pf.streamName, err)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		}
	} else {
		// 从 hub 读取数据（转发实例）
		if pf.clientBuffer != nil {
			pf.hub.AddClient(pf.clientBuffer)
			chunkCount := 0

			for {
				select {
				case <-pf.ctx.Done():
					logger.LogPrintf("[%s] context canceled, stopping forwardDataFromPipe (forward-only mode), chunks: %d", pf.streamName, chunkCount)
					pf.ffInLock.Lock()
					if ffIn != nil {
						_ = ffIn.Close()
						ffIn = nil
					}
					pf.ffInLock.Unlock()
					pushWg.Wait()
					pf.hub.RemoveClient(pf.clientBuffer)
					if pf.clientBuffer != nil {
						pf.clientBuffer.Close()
						pf.clientBuffer = nil
					}
					return
				default:
					data, ok := pf.clientBuffer.PullWithContext(pf.ctx)
					if !ok {
						pf.ffInLock.Lock()
						if ffIn != nil {
							_ = ffIn.Close()
							ffIn = nil
						}
						pf.ffInLock.Unlock()
						pushWg.Wait()
						pf.hub.RemoveClient(pf.clientBuffer)
						return
					}

					if chunk, ok := data.([]byte); ok {
						chunkCount++
						// 写入 RTMP 推流进程 stdin（如果有）
						pf.ffInLock.Lock()
						currentFFIn := ffIn
						pf.ffInLock.Unlock()

						if currentFFIn != nil {
							wDone := make(chan error, 1)
							go func() {
								_, werr := currentFFIn.Write(chunk)
								wDone <- werr
							}()

							select {
							case werr := <-wDone:
								if werr != nil {
									// 检查是否是预期的关闭错误
									if werr == io.ErrClosedPipe || strings.Contains(werr.Error(), "file already closed") || 
									   strings.Contains(werr.Error(), "broken pipe") || strings.Contains(werr.Error(), "read/write on closed pipe") {
										logger.LogPrintf("[%s] Expected pipe close error: %v", pf.streamName, werr)
									} else {
										logger.LogPrintf("[%s] Error writing to RTMP stdin: %v", pf.streamName, werr)
									}
									pf.ffInLock.Lock()
									if ffIn != nil {
										_ = ffIn.Close()
										ffIn = nil
									}
									pf.ffInLock.Unlock()
								}
							case <-time.After(5 * time.Second):
								logger.LogPrintf("[%s] Timeout writing to RTMP stdin", pf.streamName)
								pf.ffInLock.Lock()
								if ffIn != nil {
									_ = ffIn.Close()
									ffIn = nil
								}
								pf.ffInLock.Unlock()
							}
						}

						// 广播到 hub（确保数据能被其他客户端接收）
						// pf.hub.Broadcast(chunk)

						// 通知StreamHub已接收到数据
						// streamHub := GetStreamHub(pf.streamName)
						// if streamHub != nil && chunkCount <= 10 {
						// 	streamHub.SetDataReceived()
						// 	logger.LogPrintf("[%s] Forwarded data broadcasted, chunk #%d", pf.streamName, chunkCount)
						// }
					}
				}
			}
		}
	}
}

// buildPushCommandWithReceiverOptions 根据接收器的 FFmpeg 选项构建推流命令
func (pf *PipeForwarder) buildPushCommandWithReceiverOptions(options *FFmpegOptions, pushURL string) []string {
	cmd := []string{"-i", "pipe:0"}

	// 如果是特定接收器的PipeForwarder（名称包含_receiver_），则使用对应的URL
	// isReceiverSpecific := strings.Contains(pf.streamName, "_receiver_")
	// if isReceiverSpecific && pushURL != "" {
	// 	// 使用传入的pushURL
	// 	cmd = append(cmd, "-f", "flv", pushURL)
	// 	return cmd
	// }

	// 如果有配置选项，则使用它们
	if options != nil {
		// 视频编码器
		if options.VideoCodec != "" {
			cmd = append(cmd, "-c:v", options.VideoCodec)
		} else {
			cmd = append(cmd, "-c:v", "copy")
		}

		// 音频编码器
		if options.AudioCodec != "" {
			cmd = append(cmd, "-c:a", options.AudioCodec)
		} else {
			cmd = append(cmd, "-c:a", "copy")
		}

		// 视频码率
		if options.VideoBitrate != "" {
			cmd = append(cmd, "-b:v", options.VideoBitrate)
		}

		// 音频码率
		if options.AudioBitrate != "" {
			cmd = append(cmd, "-b:a", options.AudioBitrate)
		}

		// 预设
		if options.Preset != "" {
			cmd = append(cmd, "-preset", options.Preset)
		}

		// CRF
		if options.CRF > 0 {
			cmd = append(cmd, "-crf", fmt.Sprintf("%d", options.CRF))
		}

		// 像素格式
		if options.PixFmt != "" {
			cmd = append(cmd, "-pix_fmt", options.PixFmt)
		}

		// GOP大小
		if options.GopSize > 0 {
			cmd = append(cmd, "-g", fmt.Sprintf("%d", options.GopSize))
		}

		// 输出前参数
		if len(options.OutputPreArgs) > 0 {
			cmd = append(cmd, options.OutputPreArgs...)
		}

		// 输出格式
		if options.OutputFormat != "" {
			cmd = append(cmd, "-f", options.OutputFormat)
		} else {
			cmd = append(cmd, "-f", "flv")
		}

		// 输出后参数
		if len(options.OutputPostArgs) > 0 {
			cmd = append(cmd, options.OutputPostArgs...)
		}
	} else {
		// 没有配置选项，使用默认的复制模式
		cmd = append(cmd, "-c:v", "copy", "-c:a", "copy", "-f", "flv")
	}

	// 添加推流URL
	if pushURL != "" {
		cmd = append(cmd, pushURL)
	} else if pf.rtmpURL != "" {
		cmd = append(cmd, pf.rtmpURL)
	} else {
		cmd = append(cmd, "rtmp://localhost/live/stream")
	}

	return cmd
}

// waitWithBackupSupport 等待主 ffmpeg 进程退出，如果配置了 backup_url 则尝试切换到备用URL
func (pf *PipeForwarder) waitWithBackupSupport(originalArgs []string) {
	if pf.ffmpegCmd == nil {
		return
	}

	err := pf.ffmpegCmd.Wait()

	// 检查是否存在 backup_url 并且当前不是使用 backup_url 的情况
	// 这里我们需要获取 StreamManager 来检查 backup_url 配置
	manager := GetManager()
	if manager != nil {
		// 提取流名称，去掉可能的后缀（如 _receiver_2, _primary, _backup）
		streamName := pf.streamName
		if strings.Contains(streamName, "_receiver_") {
			parts := strings.Split(streamName, "_receiver_")
			streamName = parts[0]
		} else if strings.HasSuffix(streamName, "_primary") {
			streamName = strings.TrimSuffix(streamName, "_primary")
		} else if strings.HasSuffix(streamName, "_backup") {
			streamName = strings.TrimSuffix(streamName, "_backup")
		}

		manager.mutex.RLock()
		streamManager, exists := manager.streams[streamName]
		manager.mutex.RUnlock()

		if exists && streamManager.stream.Stream.Source.BackupURL != "" {
			// 检查当前是否正在使用 backup_url
			usingBackup := strings.Contains(strings.Join(originalArgs, " "), streamManager.stream.Stream.Source.BackupURL)

			if !usingBackup {
				logger.LogPrintf("[%s] Primary URL failed, switching to backup URL", pf.streamName)

				// 重新构建使用 backup_url 的命令
				backupArgs := streamManager.buildFFmpegCommandWithBackup(true)
				modifiedBackupArgs := pf.modifyFFmpegCommand(backupArgs)

				// 检查上下文是否已取消，如果已取消则尝试通知StreamHub进行切换
				if pf.ctx.Err() != nil {
					logger.LogPrintf("[%s] Context already canceled, attempting StreamHub switch", pf.streamName)
					// 通知StreamHub主推流器已停止，让StreamHub处理备用URL启动
					streamHub := GetStreamHub(pf.streamName)
					if streamHub != nil {
						streamHub.SwitchToBackup()
						logger.LogPrintf("[%s] Notifying StreamHub of primary failure for backup URL switch", pf.streamName)
					}
					goto cleanup
				}

				// 重启 FFmpeg 进程使用 backup_url
				pf.mutex.Lock()
				// 关闭旧的管道
				if pf.pipeWriter != nil {
					_ = pf.pipeWriter.Close()
				}
				if pf.pipeReader != nil {
					_ = pf.pipeReader.Close()
				}

				// 创建新的管道
				pf.pipeReader, pf.pipeWriter = io.Pipe()

				// 启动新的 FFmpeg 进程
				pf.ffmpegCmd = exec.CommandContext(pf.ctx, "ffmpeg", modifiedBackupArgs...)
				pf.ffmpegCmd.SysProcAttr = &syscall.SysProcAttr{}
				setSysProcAttr(pf.ffmpegCmd.SysProcAttr)
				pf.ffmpegCmd.Stdout = pf.pipeWriter

				if startErr := pf.ffmpegCmd.Start(); startErr != nil {
					logger.LogPrintf("[%s] Failed to start ffmpeg with backup URL: %v", pf.streamName, startErr)
					pf.mutex.Unlock()
					goto cleanup
				}

				logger.LogPrintf("[%s] Started PipeForwarder with backup URL, ffmpeg pid=%d", pf.streamName, pf.ffmpegCmd.Process.Pid)
				logger.LogPrintf("[%s] Full FFmpeg command with backup URL: ffmpeg %s", pf.streamName, strings.Join(modifiedBackupArgs, " "))
				pf.mutex.Unlock()

				// 递归调用 waitWithBackupSupport 来处理可能的后续失败
				pf.waitWithBackupSupport(backupArgs)
				return
			} else {
				logger.LogPrintf("[%s] Backup URL also failed", pf.streamName)
			}
		} else if exists && streamManager.stream.Stream.Mode == "primary-backup" {
			// 如果是primary-backup模式但没有backup_url，尝试切换到backup接收器
			logger.LogPrintf("[%s] Primary failed, switching to backup receiver", pf.streamName)

			// 通知StreamHub主推流器已停止
			streamHub := GetStreamHub(pf.streamName)
			if streamHub != nil {
				// 检查是否有备用推流器可用并启动它
				if streamHub.backup != nil {
					logger.LogPrintf("[%s] Backup forwarder found, ensuring it's running", pf.streamName)
					// 如果备用推流器没有运行，则启动它
					if !streamHub.backup.IsRunning() {
						// 检查上下文是否已取消，如果已取消则通知StreamHub处理
						if pf.ctx.Err() != nil {
							logger.LogPrintf("[%s] Context already canceled, notifying StreamHub for backup forwarder switch", pf.streamName)
							streamHub.SwitchToBackup()
						} else {
							// 构建FFmpeg命令
							backupFFmpegCmd := streamManager.buildFFmpegCommandWithBackup(false) // 使用主URL
							// 启动备用推流器
							if err := streamHub.backup.Start(backupFFmpegCmd); err != nil {
								logger.LogPrintf("[%s] Failed to start backup forwarder: %v", pf.streamName, err)
							} else {
								logger.LogPrintf("[%s] Backup forwarder started successfully", pf.streamName)
							}
						}
					} else {
						logger.LogPrintf("[%s] Backup forwarder is already running", pf.streamName)
					}
				} else {
					logger.LogPrintf("[%s] No backup forwarder configured", pf.streamName)
				}

				logger.LogPrintf("[%s] Notifying StreamHub of primary failure", pf.streamName)
			}
		}
	}

cleanup:
	// 置状态并关闭 pipeWriter 以通知读取方 EOF
	pf.mutex.Lock()
	pf.isRunning = false
	if pf.pipeWriter != nil {
		_ = pf.pipeWriter.Close()
		pf.pipeWriter = nil
	}
	pf.mutex.Unlock()

	if err != nil && pf.ctx.Err() == nil {
		logger.LogPrintf("[%s] FFmpeg exited with error: %v", pf.streamName, err)
	} else {
		logger.LogPrintf("[%s] FFmpeg exited normally", pf.streamName)
	}

	// 触发停止回调
	if pf.onStopped != nil {
		pf.onStopped()
	}
}

// Stop 停止 PipeForwarder：取消上下文、尝试杀死 ffmpeg 进程、关闭管道，清理 hub
// Stop 停止 PipeForwarder：取消上下文、尝试杀死 ffmpeg 进程、关闭管道，清理 hub
func (pf *PipeForwarder) Stop() {
	logger.LogPrintf("[%s] Stopping PipeForwarder", pf.streamName)

	pf.mutex.Lock()
	if !pf.isRunning {
		pf.mutex.Unlock()
		logger.LogPrintf("[%s] PipeForwarder already stopped", pf.streamName)
		return
	}
	pf.isRunning = false
	pf.stats.Running = false
	pf.stats.LastUpdate = time.Now()
	pf.mutex.Unlock()

	// 取消上下文
	if pf.cancel != nil {
		logger.LogPrintf("[%s] Canceling context", pf.streamName)
		pf.cancel()
	}

	// 杀掉推流 ffmpeg 进程
	pf.ffmpegLock.Lock()
	if pf.ffmpegPush != nil && pf.ffmpegPush.Process != nil {
		logger.LogPrintf("[%s] Killing ffmpeg push process (pid=%d)", pf.streamName, pf.ffmpegPush.Process.Pid)
		_ = killProcess(-pf.ffmpegPush.Process.Pid)
		// 等待推流进程结束
		go func() {
			if pf.ffmpegPush != nil {
				pf.ffmpegPush.Wait()
			}
		}()
		pf.ffmpegPush = nil
	}
	pf.ffmpegLock.Unlock()

	// 杀掉主拉流 ffmpeg 进程组
	if pf.ffmpegCmd != nil && pf.ffmpegCmd.Process != nil {
		logger.LogPrintf("[%s] Killing main ffmpeg process (pid=%d)", pf.streamName, pf.ffmpegCmd.Process.Pid)
		_ = killProcess(-pf.ffmpegCmd.Process.Pid)
		// 等待主进程结束
		go func() {
			if pf.ffmpegCmd != nil {
				pf.ffmpegCmd.Wait()
			}
		}()
		pf.ffmpegCmd = nil
	}

	// 关闭 pipe
	if pf.pipeWriter != nil {
		logger.LogPrintf("[%s] Closing pipe writer", pf.streamName)
		_ = pf.pipeWriter.Close()
		pf.pipeWriter = nil
	}
	if pf.pipeReader != nil {
		logger.LogPrintf("[%s] Closing pipe reader", pf.streamName)
		_ = pf.pipeReader.Close()
		pf.pipeReader = nil
	}

	// 关闭客户端缓冲区
	if pf.clientBuffer != nil {
		logger.LogPrintf("[%s] Closing client buffer", pf.streamName)
		pf.clientBuffer.Close()
		pf.clientBuffer = nil
	}

	// 关闭 hub，清理客户端（仅当这是独立hub时）
	// 注意：如果使用共享hub，则不要关闭它
	streamHub := GetStreamHub(pf.streamName)
	if streamHub != nil && streamHub.GetHub() != pf.hub {
		if pf.hub != nil {
			logger.LogPrintf("[%s] Closing hub", pf.streamName)
			pf.hub.Close()
		}
	}

	// 停止 HLS 管理器
	if pf.hlsManager != nil {
		logger.LogPrintf("[%s] Stopping HLS manager", pf.streamName)
		pf.hlsManager.Stop()
	}

	// 回调
	if pf.onStopped != nil {
		logger.LogPrintf("[%s] Calling onStopped callback", pf.streamName)
		go pf.onStopped() // 在goroutine中调用，避免阻塞
	}

	logger.LogPrintf("[%s] Stopped PipeForwarder", pf.streamName)
}

// IsRunning 返回当前运行状态
func (pf *PipeForwarder) IsRunning() bool {
	pf.mutex.Lock()
	defer pf.mutex.Unlock()
	return pf.isRunning
}

// IsHealthy 检查 PipeForwarder 是否健康运行
func (pf *PipeForwarder) IsHealthy() bool {
	pf.mutex.Lock()
	defer pf.mutex.Unlock()

	if !pf.enabled {
		return true // 未启用的组件视为健康
	}

	if !pf.isRunning {
		return false
	}

	// 如果启用了但是没有拉流需求，则认为是健康的
	if !pf.needPull {
		return true
	}

	// 如果启用了并且需要拉流，则检查 FFmpeg 进程是否存在
	if pf.ffmpegCmd != nil && pf.ffmpegCmd.Process != nil {
		// 尝试获取进程状态
		process, err := process.NewProcess(int32(pf.ffmpegCmd.Process.Pid))
		if err != nil {
			return false
		}

		// 检查进程是否在运行
		running, err := process.IsRunning()
		if err != nil || !running {
			return false
		}

		// 更新统计信息
		pf.stats.PID = int32(pf.ffmpegCmd.Process.Pid)
		pf.stats.Running = true
		pf.stats.LastUpdate = time.Now()
		return true
	}

	return false
}

// GetStats 返回 FFmpegProcessStats 的副本
func (pf *PipeForwarder) GetStats() *FFmpegProcessStats {
	pf.mutex.Lock()
	defer pf.mutex.Unlock()

	if pf.stats == nil {
		return nil
	}

	// 返回副本以避免并发问题
	stats := *pf.stats
	return &stats
}

// ServeFLV 提供 HTTP-FLV 播放服务（解耦版本）
func (sh *StreamHub) ServeFLV(w http.ResponseWriter, r *http.Request) {
	// 重置数据接收状态以进行新的检测
	sh.ResetDataReceived()

	// 设置响应头
	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Connection", "close")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Transfer-Encoding", "chunked")

	// 创建客户端 ringbuffer 并注册到共享hub
	clientBuffer, err := ringbuffer.New(4 * 1024 * 1024)
	if err != nil {
		logger.LogPrintf("[%s] Failed to create ring buffer for client: %v", sh.streamName, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// 立即发送响应头
	w.WriteHeader(http.StatusOK)

	// 发送FLV头部信息
	sh.sendFLVHeader(w)

	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}

	// 注册客户端到共享hub
	sh.hub.AddClient(clientBuffer)
	defer sh.hub.RemoveClient(clientBuffer)

	// 正常拉取后续数据
	sendBuffer := make([]byte, 0, 32*1024)
	bufferSize := 0
	chunkCount := 0

	logger.LogPrintf("[%s] Starting to serve FLV stream to client via StreamHub", sh.streamName)

	// 添加超时检查，如果长时间没有数据则记录日志
	dataCheckTicker := time.NewTicker(2 * time.Second)
	defer dataCheckTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			// logger.LogPrintf("[%s] HTTP client disconnected, sent %d chunks", sh.streamName, chunkCount)
			return
		case <-sh.GetContext().Done():
			// logger.LogPrintf("[%s] StreamHub context cancelled, sent %d chunks", sh.streamName, chunkCount)
			return
		case <-dataCheckTicker.C:
			if chunkCount == 0 && !sh.GetDataReceived() {
				// logger.LogPrintf("[%s] Warning: No data received after 2 seconds, check source stream", sh.streamName)
				// 检查活跃的推流器状态
				activeForwarder := sh.GetActiveForwarder()
				if activeForwarder != nil {
					// logger.LogPrintf("[%s] Active forwarder running: %t", sh.streamName, activeForwarder.IsRunning())
				}
			}
		default:
			data, ok := clientBuffer.PullWithContext(r.Context())
			if !ok {
				// logger.LogPrintf("[%s] Client buffer closed, sent %d chunks", sh.streamName, chunkCount)
				return
			}

			chunk, ok := data.([]byte)
			if !ok || len(chunk) == 0 {
				continue
			}

			sendBuffer = append(sendBuffer, chunk...)
			bufferSize += len(chunk)
			chunkCount++

			if bufferSize >= 32*1024 {
				_, err := w.Write(sendBuffer)
				if err != nil {
					// logger.LogPrintf("[%s] Error writing to HTTP client: %v, sent %d chunks", sh.streamName, err, chunkCount)
					return
				}

				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}

				sendBuffer = sendBuffer[:0]
				bufferSize = 0

				// 每100个chunks记录一次日志
				// if chunkCount%100 == 0 {
				// logger.LogPrintf("[%s] Sent %d chunks to HTTP client", sh.streamName, chunkCount)
				// }

				// 前10个chunks记录详细信息
				// if chunkCount <= 10 {
				// logger.LogPrintf("[%s] Sent chunk #%d to client, size: %d bytes", sh.streamName, chunkCount, len(chunk))
				// }
			}
		}
	}
}

// sendFLVHeader 发送FLV头部信息
func (sh *StreamHub) sendFLVHeader(w http.ResponseWriter) {
	// 尝试从活跃的推流器获取头部信息
	activeForwarder := sh.GetActiveForwarder()
	if activeForwarder != nil {
		activeForwarder.headerMutex.Lock()
		if activeForwarder.headerBuf.Len() > 0 {
			w.Write(activeForwarder.headerBuf.Bytes())
			logger.LogPrintf("[%s] Sent FLV header (%d bytes) to HTTP client", sh.streamName, activeForwarder.headerBuf.Len())
		} else {
			// 备用标准 header
			header := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}
			w.Write(header)
			logger.LogPrintf("[%s] Sent default FLV header to HTTP client", sh.streamName)
		}

		// 发送首帧关键帧
		if activeForwarder.firstTagBuf.Len() > 0 {
			w.Write(activeForwarder.firstTagBuf.Bytes())
			logger.LogPrintf("[%s] Sent first keyframe (%d bytes) to HTTP client", sh.streamName, activeForwarder.firstTagBuf.Len())
		}
		activeForwarder.headerMutex.Unlock()
	} else {
		// 没有活跃的推流器，发送默认头部
		header := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}
		w.Write(header)
		logger.LogPrintf("[%s] Sent default FLV header to HTTP client (no active forwarder)", sh.streamName)
	}
}

// EnableHLS 控制HLS功能的启用或禁用
func (pf *PipeForwarder) EnableHLS(enable bool) {
	pf.hlsEnabled = enable

	if pf.hlsManager != nil {
		if enable {
			logger.LogPrintf("HLS enabled for stream %s", pf.streamName)
			// 如果需要，可以在这里添加启动HLS处理的逻辑
		} else {
			logger.LogPrintf("HLS disabled for stream %s", pf.streamName)
			// 停止HLS管理器的相关操作
			// 例如清理HLS片段文件等
			pf.hlsManager.Stop()
		}
	} else if enable {
		logger.LogPrintf("Cannot enable HLS for stream %s: HLS manager not initialized", pf.streamName)
	}
}

// SetHLSFFmpegOptions 设置 HLS 的 FFmpeg 选项
func (pf *PipeForwarder) SetHLSFFmpegOptions(options *FFmpegOptions) {
	pf.hlsFFmpegOptions = options
	// 同时更新 HLS 管理器的选项
	if pf.hlsManager != nil {
		pf.hlsManager.ffmpegOptions = options
	}
}

func (pf *PipeForwarder) WriteChunk(chunk []byte, isVideo bool, isKeyFrame bool) {
	pf.headerMutex.Lock()
	defer pf.headerMutex.Unlock()

	// 保存 header
	if !pf.headerCaptured && len(chunk) >= 13 {
		pf.headerBuf.Reset()
		pf.headerBuf.Write(chunk[:13])
		pf.headerCaptured = true
	}

	// 保存首帧关键帧
	if isVideo && isKeyFrame && pf.firstTagBuf.Len() == 0 {
		pf.firstTagBuf.Reset()
		pf.firstTagBuf.Write(chunk)
	}

	// 广播到 hub
	pf.hub.Broadcast(chunk)
}

// ServeHLS 提供HLS播放服务
func (pf *PipeForwarder) ServeHLS(w http.ResponseWriter, r *http.Request) {
	// 检查是否启用
	if !pf.enabled {
		http.Error(w, "Pipe forwarder disabled", http.StatusNotFound)
		return
	}

	// 获取 StreamHub 并通过它提供 HLS 服务
	streamHub := GetStreamHub(pf.streamName)
	if streamHub != nil && streamHub.hlsManager != nil {
		// 通过 StreamHub 提供服务
		streamHub.ServeHLS(w, r)
		return
	}

	// 如果没有 StreamHub，则直接通过 hlsManager 提供服务
	// 解析URL路径确定请求类型
	path := r.URL.Path

	// 如果是播放列表请求
	if strings.HasSuffix(path, ".m3u8") {
		pf.hlsManager.ServePlaylist(w, r)
		return
	}

	// 如果是片段请求
	if strings.HasSuffix(path, ".ts") {
		// 从路径中提取片段名称
		segments := strings.Split(path, "/")
		if len(segments) > 0 {
			segmentName := segments[len(segments)-1]
			pf.hlsManager.ServeSegment(w, r, segmentName)
			return
		}
	}

	// 默认提供播放列表
	pf.hlsManager.ServePlaylist(w, r)
}

// ServeHLS 通过 StreamHub 提供HLS播放服务
func (sh *StreamHub) ServeHLS(w http.ResponseWriter, r *http.Request) {
	if sh.hlsManager == nil {
		http.Error(w, "HLS not available", http.StatusNotFound)
		return
	}

	// 解析URL路径确定请求类型
	path := r.URL.Path

	// 如果是播放列表请求
	if strings.HasSuffix(path, ".m3u8") {
		sh.hlsManager.ServePlaylist(w, r)
		return
	}

	// 如果是片段请求
	if strings.HasSuffix(path, ".ts") {
		// 从路径中提取片段名称
		segments := strings.Split(path, "/")
		if len(segments) > 0 {
			segmentName := segments[len(segments)-1]
			sh.hlsManager.ServeSegment(w, r, segmentName)
			return
		}
	}

	// 默认提供播放列表
	sh.hlsManager.ServePlaylist(w, r)
}

// IsPushRunning 检查 ffmpeg 推流进程是否还在运行
func (pf *PipeForwarder) IsPushRunning() bool {
	pf.ffmpegLock.Lock()
	defer pf.ffmpegLock.Unlock()

	if pf.ffmpegPush == nil {
		// logger.LogPrintf("[%s] IsPushRunning: ffmpegPush is nil", pf.streamName)
		return false
	}

	if pf.ffmpegPush.Process == nil {
		// logger.LogPrintf("[%s] IsPushRunning: ffmpegPush.Process is nil", pf.streamName)
		return false
	}

	err := pf.ffmpegPush.Process.Signal(syscall.Signal(0))
	if err != nil {
		// logger.LogPrintf("[%s] IsPushRunning: process check failed (PID=%d): %v",
		// pf.streamName, pf.ffmpegPush.Process.Pid, err)
		return false
	}

	// logger.LogPrintf("[%s] IsPushRunning: process running (PID=%d)", pf.streamName, pf.ffmpegPush.Process.Pid)
	return true
}

func findReceiverForStream(receivers []Receiver, streamName, rtmpURL string) *Receiver {
	for _, r := range receivers {
		if r.PushURL == "" {
			continue
		}

		// 完全匹配
		if r.PushURL == rtmpURL {
			return &r
		}

		// 尝试解析 URL
		rtmpParsed, err1 := url.Parse(rtmpURL)
		recvParsed, err2 := url.Parse(r.PushURL)
		if err1 != nil || err2 != nil {
			continue
		}

		// ① Host 相同（忽略 schema 差异）
		if rtmpParsed.Host == recvParsed.Host {
			// 如果 receiver.PushURL 是基础路径，且 pf.rtmpURL 以它为前缀
			if strings.HasPrefix(rtmpURL, r.PushURL) {
				return &r
			}

			// 或者 pf.rtmpURL 末尾包含 streamName
			if strings.HasSuffix(rtmpURL, "/"+streamName) {
				return &r
			}
		}

		// ② 如果 host 不同但 URL 前缀部分匹配（比如 http 和 rtmp 同域名）
		if strings.Contains(rtmpParsed.Host, recvParsed.Host) {
			if strings.HasSuffix(rtmpURL, "/"+streamName) {
				return &r
			}
		}
	}
	return nil
}
