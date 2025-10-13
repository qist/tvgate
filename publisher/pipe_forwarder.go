package publisher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qist/tvgate/stream"
	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
)

// PipeForwarder 将 FFmpeg 的输出写入 io.Pipe，然后 Go 程序读取并分发到 HTTP-FLV 客户端和可选 RTMP 推流
type PipeForwarder struct {
	streamName string
	rtmpURL    string
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
	firstTagOnce   sync.Once
	// 用于从hub读取数据的客户端缓冲区
	clientBuffer *ringbuffer.RingBuffer
	
	// HLS支持
	hlsManager *HLSSegmentManager
	
	// PAT/PMT缓存，确保每个HLS片段都包含这些信息
	patPmtBuf bytes.Buffer
}

// NewPipeForwarder 创建新的 PipeForwarder
// 修改构造函数签名并复用传入 hub（若为 nil 则创建新 hub）
func NewPipeForwarder(streamName string, rtmpURL string, enabled bool, needPull bool, hub *stream.StreamHubs) *PipeForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	var h *stream.StreamHubs
	if hub != nil {
		h = hub
	} else {
		h = stream.NewStreamHubs()
	}
	
	// 初始化 HLS 管理器
	segmentPath := filepath.Join("/tmp/hls", streamName)
	os.MkdirAll(segmentPath, 0755)
	
	hlsManager := NewHLSSegmentManager(ctx, streamName, segmentPath, 5) // 5秒片段时长
	hlsManager.SetHub(h)
	hlsManager.SetNeedPull(needPull) // 设置needPull标志
	
	return &PipeForwarder{
		streamName: streamName,
		rtmpURL:    rtmpURL,
		enabled:    enabled,
		needPull:   needPull,
		ctx:        ctx,
		cancel:     cancel,
		hub:        h,
		hlsManager: hlsManager,
	}
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

	// 如果需要拉流，则创建 io.Pipe 并启动 ffmpeg 拉流进程
	if pf.needPull {
		// 创建 io.Pipe
		pf.pipeReader, pf.pipeWriter = io.Pipe()

		// 修改 ffmpeg 命令，确保输出为 pipe:1
		modArgs := pf.modifyFFmpegCommand(ffmpegArgs)

		// 启动 ffmpeg（主进程，输出到 pipe:1）
		pf.ffmpegCmd = exec.CommandContext(pf.ctx, "ffmpeg", modArgs...)
		pf.ffmpegCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		// pf.ffmpegCmd.Stderr = os.Stderr
		pf.ffmpegCmd.Stdout = pf.pipeWriter

		if err := pf.ffmpegCmd.Start(); err != nil {
			// 启动失败，清理 pipe
			_ = pf.pipeReader.Close()
			_ = pf.pipeWriter.Close()
			pf.pipeReader = nil
			pf.pipeWriter = nil
			return fmt.Errorf("failed to start ffmpeg: %v", err)
		}

		logger.LogPrintf("[%s] Started PipeForwarder, ffmpeg pid=%d", pf.streamName, pf.ffmpegCmd.Process.Pid)
		logger.LogPrintf("[%s] Full FFmpeg command: ffmpeg %s", pf.streamName, strings.Join(modArgs, " "))
	} else {
		// 不需要拉流，创建客户端缓冲区从hub读取数据
		var err error
		pf.clientBuffer, err = ringbuffer.New(1024 * 1024)
		if err != nil {
			logger.LogPrintf("[%s] Failed to create client buffer: %v", pf.streamName, err)
			return fmt.Errorf("failed to create client buffer: %v", err)
		}

		logger.LogPrintf("[%s] Started PipeForwarder in forward-only mode", pf.streamName)
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

	result := make([]string, 0, len(args)+4)
	skipNext := false
	// lastWasInput := false

	for i := 0; i < len(args); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		arg := args[i]

		switch arg {
		case "-f", "-flvflags", "-y":
			// 跳过这些选项及其潜在值（-y 没值，-f/-flvflags 后面有值）
			if arg == "-f" || arg == "-flvflags" {
				// 跳过参数和值
				skipNext = true
			}
			// don't append
			// lastWasInput = false
			continue

		case "-i":
			// 保留输入标识和输入地址
			result = append(result, arg)
			if i+1 < len(args) {
				result = append(result, args[i+1])
				skipNext = true
			}
			// lastWasInput = true
			continue

		default:
			// 若不是以 - 开头的项，且前面不是 -i（也就是不是输入），这很可能是输出 URL -> 跳过
			// if !strings.HasPrefix(arg, "-") && !lastWasInput {
			// 	// 跳过疑似输出 URL
			// 	lastWasInput = false
			// 	continue
			// }
			// 否则保留该参数
			result = append(result, arg)
			// lastWasInput = false
		}
	}

	// 强制设置音频编码为 aac（可选，但更兼容浏览器播放），如果用户已在 args 指定则不会重复影响
	// 这里不强行覆盖用户设置，只在没有显式 -c:a 的情况下追加
	hasCA := false
	for i := 0; i < len(result); i++ {
		if result[i] == "-c:a" {
			hasCA = true
			break
		}
	}
	if !hasCA {
		// 采用 aac，44100Hz，双声道
		result = append(result, "-c:a", "aac", "-ar", "44100", "-ac", "2")
	}
	// 追加低延迟参数，保留用户已有设置
	appendIfMissing := func(flag string, vals ...string) {
		for _, a := range result {
			if a == flag {
				return
			}
		}
		result = append(result, append([]string{flag}, vals...)...)
	}

	appendIfMissing("-fflags", "+genpts")
	appendIfMissing("-avioflags", "direct")
	appendIfMissing("-flush_packets", "1")
	appendIfMissing("-flvflags", "no_duration_filesize")
	appendIfMissing("-g", "25")
	appendIfMissing("-keyint_min", "1")
	// 最终输出到 stdout 的 FLV
	result = append(result, "-f", "flv", "pipe:1")
	return result
}

// forwardDataFromPipe 从 pipeReader 读取数据并分发到 hub 与可选 RTMP 推流
func (pf *PipeForwarder) forwardDataFromPipe() {
	var ffmpegPush *exec.Cmd
	var ffIn io.WriteCloser
	var pushWg sync.WaitGroup

	// 如果配置了 rtmpURL，则启用 ffmpegPush：读取 stdin（pipe:0）并推送到 rtmp
	if pf.rtmpURL != "" {
		// 不使用 -re（对 pipe:0 不需要节流）
		ffmpegPush = exec.CommandContext(pf.ctx, "ffmpeg", "-i", "pipe:0", "-c", "copy", "-f", "flv", pf.rtmpURL)
		var err error
		ffIn, err = ffmpegPush.StdinPipe()
		if err != nil {
			logger.LogPrintf("[%s] Failed to create stdin pipe for RTMP push: %v", pf.streamName, err)
			ffIn = nil
		} else {
			// ffmpegPush.Stderr = os.Stderr
			ffmpegPush.Stdout = os.Stdout

			if err := ffmpegPush.Start(); err != nil {
				logger.LogPrintf("[%s] Failed to start RTMP push: %v", pf.streamName, err)
				_ = ffIn.Close()
				ffIn = nil
			} else {
				logger.LogPrintf("[%s] Started RTMP push to %s (pid=%d)", pf.streamName, pf.rtmpURL, ffmpegPush.Process.Pid)
				// 等待 ffmpegPush 退出的 goroutine
				pushWg.Add(1)
				go func() {
					defer pushWg.Done()
					if err := ffmpegPush.Wait(); err != nil && pf.ctx.Err() == nil {
						logger.LogPrintf("[%s] RTMP push ffmpeg exited with error: %v", pf.streamName, err)
					} else {
						logger.LogPrintf("[%s] RTMP push ffmpeg exited normally", pf.streamName)
					}
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
		for {
			select {
			case <-pf.ctx.Done():
				logger.LogPrintf("[%s] context canceled, stopping forwardDataFromPipe", pf.streamName)
				if ffIn != nil {
					_ = ffIn.Close()
					ffIn = nil
				}
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
								_ = ffIn.Close()
								ffIn = nil
							}
						case <-time.After(5 * time.Second):
							logger.LogPrintf("[%s] Timeout writing to RTMP stdin — closing ffIn", pf.streamName)
							_ = ffIn.Close()
							ffIn = nil
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
						if ffIn != nil {
							_ = ffIn.Close()
							ffIn = nil
						}
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
					if ffIn != nil {
						_ = ffIn.Close()
						ffIn = nil
					}
					// 等待推流进程退出
					pushWg.Wait()
					// 移除客户端
					pf.hub.RemoveClient(pf.clientBuffer)
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
									_ = ffIn.Close()
									ffIn = nil
								}
							case <-time.After(5 * time.Second):
								logger.LogPrintf("[%s] Timeout writing to RTMP stdin — closing ffIn", pf.streamName)
								_ = ffIn.Close()
								ffIn = nil
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

// waitWithoutBackupSupport 等待主 ffmpeg 进程退出并清理管道
func (pf *PipeForwarder) waitWithoutBackupSupport() {
	if pf.ffmpegCmd == nil {
		return
	}

	err := pf.ffmpegCmd.Wait()

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
				pf.ffmpegCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	pf.mutex.Unlock()

	// 取消上下文
	pf.cancel()

	// 杀掉 ffmpeg 进程组（如果存在）
	if pf.ffmpegCmd != nil && pf.ffmpegCmd.Process != nil {
		// 使用负 pid 向进程组发送信号（Setpgid 在 Start 时已设置）
		_ = syscall.Kill(-pf.ffmpegCmd.Process.Pid, syscall.SIGKILL)
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

	// 创建客户端 ringbuffer 并注册 hub
	clientBuffer, _ := ringbuffer.New(4 * 1024 * 1024)
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

// parseFLVTags 解析 chunk 中的所有 FLV tag，返回是否包含视频关键帧
func (pf *PipeForwarder) parseFLVTags(data []byte) (isVideo, isKeyFrame bool) {
	pos := 0
	for pos+11 <= len(data) { // 至少要有 tag header
		tagType := data[pos] & 0x1F
		dataSize := int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		tagTotalLen := 11 + dataSize + 4 // 11字节header + 数据 + 4字节 PreviousTagSize

		if pos+tagTotalLen > len(data) {
			break // 不完整 tag
		}

		if tagType == 9 { // 视频
			isVideo = true
			frameByte := data[pos+11]
			frameType := (frameByte & 0xF0) >> 4
			if frameType == 1 { // 关键帧
				isKeyFrame = true
				break // 找到首个关键帧即可
			}
		}
		pos += tagTotalLen
	}
	return
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