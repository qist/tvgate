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
}

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

	segmentPath := filepath.Join("/tmp/hls", baseStreamName)
	os.MkdirAll(segmentPath, 0755)

	hlsManager := NewHLSSegmentManager(ctx, baseStreamName, segmentPath, 5) // 5秒片段时长
	hlsManager.SetHub(h)
	hlsManager.SetNeedPull(needPull) // 设置needPull标志

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
	pf.hlsManager.Start()

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
		return []string{"-f", "flv", "pipe:1"}
	}

	// 获取流管理器以访问 source 和 local_play_urls 配置
	manager := GetManager()
	var sourceOptions *FFmpegOptions
	var flvOptions *FFmpegOptions

	// 尝试获取流管理器配置
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

		// 获取流配置
		manager.mutex.RLock()
		streamManager, exists := manager.streams[streamName]
		manager.mutex.RUnlock()

		if exists {
			// 获取 source 的 ffmpeg_options
			sourceOptions = streamManager.stream.Stream.Source.FFmpegOptions

			// 获取 flv 的 ffmpeg_options
			for _, playURL := range streamManager.stream.Stream.LocalPlayUrls {
				if playURL.Protocol == "flv" && playURL.Enabled {
					flvOptions = playURL.FlvFFmpegOptions
					break
				}
			}
		}
	}

	// 合并参数
	mergedArgs := pf.mergeFFmpegOptions(args, sourceOptions, flvOptions)

	// 强制设置音频编码为 aac（可选，但更兼容浏览器播放），如果用户已在 args 指定则不会重复影响
	// 这里不强行覆盖用户设置，只在没有显式 -c:a 的情况下追加
	// hasCA := false
	// for i := 0; i < len(mergedArgs); i++ {
	// 	if mergedArgs[i] == "-c:a" {
	// 		hasCA = true
	// 		break
	// 	}
	// }
	// if !hasCA {
	// 	// 采用 aac，44100Hz，双声道
	// 	mergedArgs = append(mergedArgs, "-c:a", "aac", "-ar", "44100", "-ac", "2")
	// }

	// 追加低延迟参数，保留用户已有设置
	appendIfMissing := func(flag string, vals ...string) {
		for _, a := range mergedArgs {
			if a == flag {
				return
			}
		}
		mergedArgs = append(mergedArgs, append([]string{flag}, vals...)...)
	}

	appendIfMissing("-fflags", "+genpts")
	appendIfMissing("-avioflags", "direct")
	appendIfMissing("-flush_packets", "1")
	appendIfMissing("-flvflags", "no_duration_filesize")
	appendIfMissing("-keyint_min", "1")
	// 最终输出到 stdout 的 FLV
	mergedArgs = append(mergedArgs, "-f", "flv", "pipe:1")
	return mergedArgs
}

// mergeFFmpegOptions 合并 FFmpeg 选项，支持去重
func (pf *PipeForwarder) mergeFFmpegOptions(baseArgs []string, sourceOptions, flvOptions *FFmpegOptions) []string {
	// 创建一个映射来跟踪已设置的选项，用于去重
	optionSet := make(map[string]bool)
	result := make([]string, 0, len(baseArgs)+20) // 预分配容量

	// 先处理基础参数
	i := 0
	for i < len(baseArgs) {
		arg := baseArgs[i]

		if len(arg) > 0 && arg[0] == '-' {
			// 这是一个选项参数
			optionSet[arg] = true

			// 如果这个选项需要值，则处理值
			if i+1 < len(baseArgs) && (arg == "-c" || arg == "-codec" || arg == "-vcodec" ||
				arg == "-acodec" || arg == "-b:v" || arg == "-b:a" || arg == "-s" ||
				arg == "-r" || arg == "-preset" || arg == "-crf" || arg == "-pix_fmt" ||
				arg == "-vf" || arg == "-af" || arg == "-ac" || arg == "-ar" || arg == "-g" ||
				arg == "-keyint_min" || arg == "-f" || arg == "-flvflags") {
				// 添加参数和值到结果中
				result = append(result, arg, baseArgs[i+1])
				// 标记参数值，防止重复
				if len(baseArgs[i+1]) > 0 && baseArgs[i+1][0] != '-' {
					optionSet[arg+"_value"] = true
				}
				i += 2
				continue
			}
		}

		// 添加非选项参数或者不需要值的选项
		result = append(result, arg)
		i++
	}

	// 添加 source ffmpeg 选项（如果存在）
	var optionsToUse []*FFmpegOptions
	if sourceOptions != nil {
		optionsToUse = append(optionsToUse, sourceOptions)
	}

	// FLV配置优先于source配置
	if flvOptions != nil {
		optionsToUse = append(optionsToUse, flvOptions)
	}

	// 按顺序处理所有选项（后面的选项会覆盖前面的选项）
	for _, options := range optionsToUse {
		// 处理 input_pre_args
		if len(options.InputPreArgs) > 0 {
			for _, arg := range options.InputPreArgs {
				// 简单去重逻辑 - 检查参数是否已经存在
				if !optionSet[arg] {
					result = append(result, arg)
					optionSet[arg] = true
				}
			}
		}

		// 处理视频编解码器 (-c:v 和 -vcodec 是等价的)
		if options.VideoCodec != "" {
			// 先移除可能已存在的 -vcodec 和 -c:v 参数
			result = removeArg(result, "-vcodec")
			result = removeArg(result, "-c:v")
			delete(optionSet, "-vcodec")
			delete(optionSet, "-c:v")
			delete(optionSet, "-vcodec_value")
			delete(optionSet, "-c:v_value")

			result = append(result, "-vcodec", options.VideoCodec)
			optionSet["-vcodec"] = true
			optionSet["-c:v"] = true
			optionSet["-vcodec_value"] = true
			optionSet["-c:v_value"] = true
		}

		// 处理音频编解码器 (-c:a 和 -acodec 是等价的)
		if options.AudioCodec != "" {
			// 先移除可能已存在的 -acodec 和 -c:a 参数
			result = removeArg(result, "-acodec")
			result = removeArg(result, "-c:a")
			delete(optionSet, "-acodec")
			delete(optionSet, "-c:a")
			delete(optionSet, "-acodec_value")
			delete(optionSet, "-c:a_value")

			result = append(result, "-acodec", options.AudioCodec)
			optionSet["-acodec"] = true
			optionSet["-c:a"] = true
			optionSet["-acodec_value"] = true
			optionSet["-c:a_value"] = true
		}

		// 处理视频码率
		if options.VideoBitrate != "" {
			// 先移除可能已存在的 -b:v 参数
			result = removeArg(result, "-b:v")
			delete(optionSet, "-b:v")
			delete(optionSet, "-b:v_value")

			result = append(result, "-b:v", options.VideoBitrate)
			optionSet["-b:v"] = true
			optionSet["-b:v_value"] = true
		}

		// 处理音频码率
		if options.AudioBitrate != "" {
			// 先移除可能已存在的 -b:a 参数
			result = removeArg(result, "-b:a")
			delete(optionSet, "-b:a")
			delete(optionSet, "-b:a_value")

			result = append(result, "-b:a", options.AudioBitrate)
			optionSet["-b:a"] = true
			optionSet["-b:a_value"] = true
		}

		// 处理预设
		if options.Preset != "" {
			// 先移除可能已存在的 -preset 参数
			result = removeArg(result, "-preset")
			delete(optionSet, "-preset")
			delete(optionSet, "-preset_value")

			result = append(result, "-preset", options.Preset)
			optionSet["-preset"] = true
			optionSet["-preset_value"] = true
		}

		// 处理 CRF
		if options.CRF > 0 {
			// 先移除可能已存在的 -crf 参数
			result = removeArg(result, "-crf")
			delete(optionSet, "-crf")
			delete(optionSet, "-crf_value")

			result = append(result, "-crf", fmt.Sprintf("%d", options.CRF))
			optionSet["-crf"] = true
			optionSet["-crf_value"] = true
		}

		// 处理像素格式
		if options.PixFmt != "" {
			// 先移除可能已存在的 -pix_fmt 参数
			result = removeArg(result, "-pix_fmt")
			delete(optionSet, "-pix_fmt")
			delete(optionSet, "-pix_fmt_value")

			result = append(result, "-pix_fmt", options.PixFmt)
			optionSet["-pix_fmt"] = true
			optionSet["-pix_fmt_value"] = true
		}

		// 处理 GOP 大小
		if options.GopSize > 0 {
			// 先移除可能已存在的 -g 参数
			result = removeArg(result, "-g")
			delete(optionSet, "-g")
			delete(optionSet, "-g_value")

			result = append(result, "-g", fmt.Sprintf("%d", options.GopSize))
			optionSet["-g"] = true
			optionSet["-g_value"] = true
		}

		// 处理 output_pre_args
		if len(options.OutputPreArgs) > 0 {
			for _, arg := range options.OutputPreArgs {
				// 特殊处理 -f 参数
				if arg == "-f" {
					// 移除已存在的 -f 参数
					result = removeArg(result, "-f")
					delete(optionSet, "-f")
					delete(optionSet, "-f_value")
				}
				// 添加参数（允许覆盖）
				result = append(result, arg)
				optionSet[arg] = true
			}
		}
	}

	return result
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
	// var ffmpegPush *exec.Cmd
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
		var pushCommands [][]string

		// 其他模式，使用第一个接收器的配置
		// 检查并更新过期的 stream key

		receiver := findReceiverForStream(receivers, pf.streamName, pf.rtmpURL)
		var ffmpegOpts *FFmpegOptions

		if receiver != nil && receiver.FFmpegOptions != nil {
			ffmpegOpts = receiver.FFmpegOptions
			// logger.LogPrintf("[receiver] Matched %s -> %s, opts: %+v", pf.streamName, receiver.PushURL, ffmpegOpts)
		} else {
			// logger.LogPrintf("[receiver] No match for %s (%s), using default opts", pf.streamName, pf.rtmpURL)
			ffmpegOpts = &FFmpegOptions{}
		}

		cmd := pf.buildPushCommandWithReceiverOptions(ffmpegOpts, pf.rtmpURL)
		pushCommands = append(pushCommands, cmd)
		// fmt.Printf("Stream %s xxxxxxxx %s 999999: %+v\n", pf.streamName, pf.rtmpURL, receivers[0].FFmpegOptions)
		// 为每个推流命令创建一个推流进程（通常只有一个）
		for i, pushCmd := range pushCommands {
			// 创建推流命令
			ffmpegPush := exec.CommandContext(pf.ctx, "ffmpeg", pushCmd...)
			var err error

			// 为每个推流进程创建独立的 stdin pipe
			var pushStdin io.WriteCloser
			pushStdin, err = ffmpegPush.StdinPipe()
			if err != nil {
				logger.LogPrintf("[%s] Failed to create stdin pipe for RTMP push: %v", pf.streamName, err)
				continue
			}

			// ffmpegPush.Stderr = os.Stderr
			ffmpegPush.Stdout = os.Stdout

			if err := ffmpegPush.Start(); err != nil {
				logger.LogPrintf("[%s] Failed to start RTMP push: %v", pf.streamName, err)
				_ = pushStdin.Close()
				continue
			} else {
				// ✅ 保存到 pf.ffmpegPush 以供 IsPushRunning() 检测（仅保存第一个）
				if i == 0 { // 第一个推流进程作为主进程
					pf.ffmpegLock.Lock()
					pf.ffmpegPush = ffmpegPush
					pf.ffmpegLock.Unlock()

					// 将第一个推流进程的 stdin pipe 赋值给 ffIn
					ffIn = pushStdin
				} else {
					// 为非第一个推流进程创建完全独立的数据流路径
					go func(stdin io.WriteCloser) {
						defer stdin.Close()
						buf := make([]byte, 32*1024)

						// 为每个非主推流进程创建独立的 pipe reader
						pipeReader, pipeWriter := io.Pipe()

						// 启动数据复制 goroutine
						go func() {
							defer pipeWriter.Close()
							mainBuf := make([]byte, 32*1024)
							for {
								select {
								case <-pf.ctx.Done():
									return
								default:
									n, err := pf.pipeReader.Read(mainBuf)
									if n > 0 {
										_, werr := pipeWriter.Write(mainBuf[:n])
										if werr != nil {
											if werr == io.ErrClosedPipe || strings.Contains(werr.Error(), "file already closed") {
												return
											}
											logger.LogPrintf("[%s] Error writing to backup RTMP pipe: %v", pf.streamName, werr)
											return
										}
									}
									if err != nil {
										if err == io.EOF || err == io.ErrClosedPipe ||
											strings.Contains(err.Error(), "file already closed") ||
											strings.Contains(err.Error(), "read/write on closed pipe") {
											return
										}
										if err != io.EOF {
											logger.LogPrintf("[%s] Error reading from main pipe for backup push: %v", pf.streamName, err)
										}
										return
									}
								}
							}
						}()

						// 从独立的 pipe reader 读取数据并写入到推流进程的 stdin
						for {
							select {
							case <-pf.ctx.Done():
								return
							default:
								n, err := pipeReader.Read(buf)
								if n > 0 {
									_, werr := stdin.Write(buf[:n])
									if werr != nil {
										if werr == io.ErrClosedPipe || strings.Contains(werr.Error(), "file already closed") ||
											strings.Contains(werr.Error(), "broken pipe") {
											return
										}
										logger.LogPrintf("[%s] Error writing to backup RTMP stdin: %v", pf.streamName, werr)
										return
									}
								}
								if err != nil {
									if err == io.EOF || err == io.ErrClosedPipe ||
										strings.Contains(err.Error(), "file already closed") ||
										strings.Contains(err.Error(), "read/write on closed pipe") {
										return
									}
									if err != io.EOF {
										logger.LogPrintf("[%s] Error reading from backup pipe: %v", pf.streamName, err)
									}
									return
								}
							}
						}
					}(pushStdin)
				}

				logger.LogPrintf("[%s] Started RTMP push (pid=%d)", pf.streamName, ffmpegPush.Process.Pid)
				// 等待 ffmpegPush 退出的 goroutine
				pushWg.Add(1)
				go func(cmd *exec.Cmd, stdin io.WriteCloser) {
					defer pushWg.Done()
					if err := cmd.Wait(); err != nil && pf.ctx.Err() == nil {
						// 注意：这里不清理 pf.ffmpegPush，因为可能有多个推流进程
						logger.LogPrintf("[%s] RTMP push ffmpeg exited with error: %v", pf.streamName, err)
					} else {
						logger.LogPrintf("[%s] RTMP push ffmpeg exited normally", pf.streamName)
					}
					// 关闭 stdin pipe
					_ = stdin.Close()
				}(ffmpegPush, pushStdin)
			}
		}
	} else {
		logger.LogPrintf("[%s] No receivers found, skipping RTMP push", pf.streamName)
	}

	// 标记 hub 为播放状态
	pf.hub.SetPlaying()

	// 根据 needPull 参数决定数据源
	if pf.needPull {
		// 从管道读取数据（主拉流实例）
		buf := make([]byte, 32*1024)
		for {
			select {
			case <-pf.ctx.Done():
				logger.LogPrintf("[%s] context canceled, stopping forwardDataFromPipe", pf.streamName)
				pf.ffInLock.Lock()
				if ffIn != nil {
					_ = ffIn.Close()
					ffIn = nil
				}
				pf.ffInLock.Unlock()
				// 等待推流进程退出
				pushWg.Wait()
				return
			default:
				n, err := pf.pipeReader.Read(buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])

					// 在 header 未捕获时缓存前段数据
					pf.headerMutex.Lock()
					if !pf.headerCaptured {
						// 确保缓冲区不超过4KB
						if pf.headerBuf.Len() < 4*1024 {
							pf.headerBuf.Write(chunk)
						}

						// 当缓冲区数据足够且以"FLV"开头时，标记header已捕获
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
								logger.LogPrintf("[%s] Error writing to RTMP stdin: %v — closing ffIn", pf.streamName, werr)
								pf.ffInLock.Lock()
								if ffIn != nil {
									_ = ffIn.Close()
									ffIn = nil
								}
								pf.ffInLock.Unlock()
							}
						case <-time.After(5 * time.Second):
							logger.LogPrintf("[%s] Timeout writing to RTMP stdin — closing ffIn", pf.streamName)
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
					// isVideo, isKeyFrame := pf.parseFLVTags(chunk)
					// pf.hlsManager.WriteData(chunk)
					// pf.WriteChunk(chunk, isVideo, isKeyFrame)
				}

				if err != nil {
					if err == io.EOF {
						logger.LogPrintf("[%s] pipe EOF reached", pf.streamName)
						pf.ffInLock.Lock()
						if ffIn != nil {
							_ = ffIn.Close()
							ffIn = nil
						}
						pf.ffInLock.Unlock()
						// 等待可选的推流进程退出
						pushWg.Wait()
						return
					}
					// 记录并短暂休眠，继续循环以便响应 ctx.Done()
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
			// 注册客户端到 hub
			pf.hub.AddClient(pf.clientBuffer)

			// 从 hub 读取数据并推流
			for {
				select {
				case <-pf.ctx.Done():
					logger.LogPrintf("[%s] context canceled, stopping forwardDataFromPipe (forward-only mode)", pf.streamName)
					pf.ffInLock.Lock()
					if ffIn != nil {
						_ = ffIn.Close()
						ffIn = nil
					}
					pf.ffInLock.Unlock()
					// 等待所有推流进程完全退出
					pushWg.Wait()
					// 从 hub 移除客户端
					pf.hub.RemoveClient(pf.clientBuffer)
					// 关闭并清理客户端缓冲区
					if pf.clientBuffer != nil {
						pf.clientBuffer.Close()
						pf.clientBuffer = nil
					}
					return
				default:
					data, ok := pf.clientBuffer.PullWithContext(pf.ctx)
					if !ok {
						// buffer 关闭或上下文取消
						if ffIn != nil {
							_ = ffIn.Close()
							ffIn = nil
						}
						// 等待推流进程退出
						pushWg.Wait()
						// 移除客户端
						pf.hub.RemoveClient(pf.clientBuffer)
						return
					}

					// PullWithContext 返回 interface{}，我们假定是 []byte
					if chunk, ok := data.([]byte); ok {
						// 写入 RTMP 推流进程 stdin（如果有）
						if ffIn != nil {
							wDone := make(chan error, 1)
							go func() {
								_, werr := ffIn.Write(chunk)
								wDone <- werr
							}()

							select {
							case werr := <-wDone:
								if werr != nil {
									logger.LogPrintf("[%s] Error writing to RTMP stdin: %v — closing ffIn", pf.streamName, werr)
									pf.ffInLock.Lock()
									if ffIn != nil {
										_ = ffIn.Close()
										ffIn = nil
									}
									pf.ffInLock.Unlock()
								}
							case <-time.After(5 * time.Second):
								logger.LogPrintf("[%s] Timeout writing to RTMP stdin — closing ffIn", pf.streamName)
								pf.ffInLock.Lock()
								if ffIn != nil {
									_ = ffIn.Close()
									ffIn = nil
								}
								pf.ffInLock.Unlock()
							}
						}
						// pf.hlsManager.WriteData(chunk)
						// isVideo, isKeyFrame := pf.parseFLVTags(chunk)
						// pf.WriteChunk(chunk, isVideo, isKeyFrame)
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
func (pf *PipeForwarder) Stop() {
	pf.mutex.Lock()
	if !pf.isRunning {
		pf.mutex.Unlock()
		return
	}
	pf.isRunning = false
	// 更新状态
	pf.stats.Running = false
	pf.stats.LastUpdate = time.Now()
	pf.mutex.Unlock()

	// 取消上下文
	pf.cancel()

	// 杀掉 ffmpeg 进程组（如果存在）
	if pf.ffmpegCmd != nil && pf.ffmpegCmd.Process != nil {
		// 使用负 pid 向进程组发送信号（Setpgid 在 Start 时已设置）
		_ = killProcess(-pf.ffmpegCmd.Process.Pid)
	}

	// 关闭 pipe
	if pf.pipeWriter != nil {
		_ = pf.pipeWriter.Close()
		pf.pipeWriter = nil
	}
	if pf.pipeReader != nil {
		_ = pf.pipeReader.Close()
		pf.pipeReader = nil
	}

	// 关闭客户端缓冲区
	if pf.clientBuffer != nil {
		pf.clientBuffer.Close()
		pf.clientBuffer = nil
	}

	// 关闭 hub，清理客户端
	pf.hub.Close()

	// 停止HLS管理器
	pf.hlsManager.Stop()

	// 回调
	if pf.onStopped != nil {
		pf.onStopped()
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

// ServeFLV 提供 HTTP-FLV 播放（每个连接创建一个 ring buffer 客户端）
func (pf *PipeForwarder) ServeFLV(w http.ResponseWriter, r *http.Request) {
	if !pf.enabled {
		http.Error(w, "Pipe forwarder disabled", http.StatusNotFound)
		return
	}

	// 设置响应头
	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Connection", "close")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Transfer-Encoding", "chunked")

	// 创建客户端 ringbuffer 并注册 hub
	clientBuffer, err := ringbuffer.New(4 * 1024 * 1024)
	if err != nil {
		logger.LogPrintf("Failed to create ring buffer for client: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// 立即发送响应头
	w.WriteHeader(http.StatusOK)

	pf.headerMutex.Lock()
	if pf.headerBuf.Len() > 0 {
		w.Write(pf.headerBuf.Bytes())
	} else {
		// 备用标准 header
		w.Write([]byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00})
	}

	// 发送首帧关键帧
	if pf.firstTagBuf.Len() > 0 {
		w.Write(pf.firstTagBuf.Bytes())
	}
	pf.headerMutex.Unlock()

	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}

	pf.hub.AddClient(clientBuffer)
	defer pf.hub.RemoveClient(clientBuffer)

	// 正常拉取后续数据
	sendBuffer := make([]byte, 0, 32*1024)
	bufferSize := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-pf.ctx.Done():
			return
		default:
			data, ok := clientBuffer.PullWithContext(r.Context())
			if !ok {
				return
			}
			chunk, ok := data.([]byte)
			if !ok || len(chunk) == 0 {
				continue
			}

			sendBuffer = append(sendBuffer, chunk...)
			bufferSize += len(chunk)
			if bufferSize >= 32*1024 {
				w.Write(sendBuffer)
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
				sendBuffer = sendBuffer[:0]
				bufferSize = 0
			}
		}
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
	if !pf.enabled {
		http.Error(w, "Pipe forwarder disabled", http.StatusNotFound)
		return
	}

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

func (pf *PipeForwarder) monitorPrimaryPush(primary, backup *PipeForwarder, ffmpegCmd []string) {
	// 获取流管理器
	manager := GetManager()
	if manager == nil {
		return
	}

	// 检查是否是特定接收器的PipeForwarder（名称包含_receiver_）
	// 如果是，则不执行主备切换逻辑
	if strings.Contains(pf.streamName, "_receiver_") {
		return
	}

	// 提取流名称
	streamName := pf.streamName
	if strings.Contains(streamName, "_receiver_") {
		parts := strings.Split(streamName, "_receiver_")
		streamName = parts[0]
	} else if strings.HasSuffix(streamName, "_primary") {
		streamName = strings.TrimSuffix(streamName, "_primary")
	} else if strings.HasSuffix(streamName, "_backup") {
		streamName = strings.TrimSuffix(streamName, "_backup")
	}

	// 获取流管理器
	manager.mutex.RLock()
	streamManager, exists := manager.streams[streamName]
	manager.mutex.RUnlock()

	// 检查是否是 primary-backup 模式
	if !exists || streamManager == nil || streamManager.stream.Stream.Mode != "primary-backup" {
		return
	}

	go func() {
		time.Sleep(5 * time.Second)
		for {
			select {
			case <-pf.ctx.Done():
				return
			default:
				if !primary.IsPushRunning() {
					logger.LogPrintf("[%s] Primary push stopped, switching to backup", pf.streamName)

					// 使用备份接收器的配置构建推流命令
					if streamManager.stream.Stream.Receivers.Backup != nil {
						// 设置备份推流的 URL
						backup.rtmpURL = streamManager.stream.Stream.Receivers.Backup.PushURL
						// 构建使用备份接收器配置的推流命令
						ffmpegCmd = backup.buildPushCommandWithReceiverOptions(
							streamManager.stream.Stream.Receivers.Backup.FFmpegOptions,
							streamManager.stream.Stream.Receivers.Backup.PushURL,
						)
					}

					if err := backup.Start(ffmpegCmd); err != nil {
						logger.LogPrintf("[%s] Failed to start backup push: %v", pf.streamName, err)
					} else {
						logger.LogPrintf("[%s] Backup push started successfully", pf.streamName)
					}
					return
				}
				time.Sleep(30 * time.Second)
			}
		}
	}()
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
