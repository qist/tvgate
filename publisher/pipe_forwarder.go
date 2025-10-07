package publisher

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"strings"
)

// PipeForwarder 实现将FFmpeg输出到FIFO，然后Go进程读取FIFO并分发到RTMP和HTTP
type PipeForwarder struct {
	pipePath     string
	streamName   string
	rtmpURL      string
	enabled      bool
	ffmpegCmd    *exec.Cmd
	ctx          context.Context
	cancel       context.CancelFunc
	mutex        sync.Mutex
	isRunning    bool
	onStarted    func()
	onStopped    func()
	clients      map[chan []byte]struct{}
	clientsMutex sync.RWMutex
}

// NewPipeForwarder 创建一个新的PipeForwarder实例
func NewPipeForwarder(pipePath, streamName string, rtmpURL string, enabled bool) *PipeForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	return &PipeForwarder{
		pipePath:   pipePath,
		streamName: streamName,
		rtmpURL:    rtmpURL,
		enabled:    enabled,
		ctx:        ctx,
		cancel:     cancel,
		clients:    make(map[chan []byte]struct{}),
	}
}

// SetCallbacks 设置启动和停止的回调函数
func (pf *PipeForwarder) SetCallbacks(onStarted, onStopped func()) {
	pf.onStarted = onStarted
	pf.onStopped = onStopped
}

// Start 启动管道转发器
func (pf *PipeForwarder) Start(ffmpegArgs []string) error {
	pf.mutex.Lock()
	defer pf.mutex.Unlock()

	if !pf.enabled {
		log.Printf("PipeForwarder for stream %s is disabled", pf.streamName)
		return nil
	}

	if pf.isRunning {
		log.Printf("PipeForwarder for stream %s is already running", pf.streamName)
		return nil
	}

	// 确保管道文件存在
	if err := pf.createPipe(); err != nil {
		return fmt.Errorf("failed to create pipe: %v", err)
	}

	// 修改FFmpeg命令以输出到管道
	ffmpegCmd := pf.modifyFFmpegCommand(ffmpegArgs)

	// 创建FFmpeg命令
	pf.ffmpegCmd = exec.CommandContext(pf.ctx, "ffmpeg", ffmpegCmd...)

	// 设置进程组ID以便能够杀死子进程
	pf.ffmpegCmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// 启动FFmpeg命令
	if err := pf.ffmpegCmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg pipe forwarder: %v", err)
	}

	pf.isRunning = true
	log.Printf("Started PipeForwarder for stream %s, writing to pipe: %s", pf.streamName, pf.pipePath)
	log.Printf("Full FFmpeg command for stream %s pipe forwarder: ffmpeg %s", pf.streamName, strings.Join(ffmpegCmd, " "))

	// 启动从管道读取数据并分发的goroutine
	go pf.forwardDataFromPipe()

	// 调用启动回调
	if pf.onStarted != nil {
		go pf.onStarted()
	}

	// 启动goroutine等待FFmpeg命令完成
	go pf.wait()

	return nil
}

// Stop 停止管道转发器
func (pf *PipeForwarder) Stop() {
	pf.mutex.Lock()
	defer pf.mutex.Unlock()

	if !pf.isRunning {
		return
	}

	pf.isRunning = false
	pf.cancel()

	// 杀死FFmpeg进程组
	if pf.ffmpegCmd != nil && pf.ffmpegCmd.Process != nil {
		syscall.Kill(-pf.ffmpegCmd.Process.Pid, syscall.SIGKILL)
	}

	// 关闭所有客户端通道
	pf.clientsMutex.Lock()
	for ch := range pf.clients {
		close(ch)
	}
	pf.clients = make(map[chan []byte]struct{})
	pf.clientsMutex.Unlock()

	// 调用停止回调
	if pf.onStopped != nil {
		pf.onStopped()
	}

	log.Printf("Stopped PipeForwarder for stream %s", pf.streamName)
}

// IsRunning 检查管道转发器是否正在运行
func (pf *PipeForwarder) IsRunning() bool {
	pf.mutex.Lock()
	defer pf.mutex.Unlock()
	return pf.isRunning
}

// createPipe 创建命名管道
func (pf *PipeForwarder) createPipe() error {
	// 检查管道是否已经存在
	if _, err := os.Stat(pf.pipePath); err == nil {
		// 管道已存在，先删除
		if err := os.Remove(pf.pipePath); err != nil {
			log.Printf("Warning: failed to remove existing pipe %s: %v", pf.pipePath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat pipe path: %v", err)
	}

	// 创建命名管道
	if err := syscall.Mkfifo(pf.pipePath, 0666); err != nil {
		return fmt.Errorf("failed to create fifo %s: %v", pf.pipePath, err)
	}

	return nil
}

// modifyFFmpegCommand 修改FFmpeg命令以输出到管道
func (pf *PipeForwarder) modifyFFmpegCommand(args []string) []string {
	if len(args) == 0 {
		return args
	}

	// 创建结果切片
	result := make([]string, 0, len(args)+3)
	
	// 添加-y参数（覆盖输出文件）
	result = append(result, "-y")
	
	// 添加除最后一个参数外的所有参数（移除原始输出URL）
	// 同时查找并移除任何现有的-f参数对
	for i := 0; i < len(args)-1; i++ {
		// 跳过-f参数及其后续参数
		if args[i] == "-f" {
			i++ // 跳过下一个参数
			continue
		}
		result = append(result, args[i])
	}
	
	// 添加-f flv参数（确保只添加一次）
	result = append(result, "-f", "flv")
	
	// 添加管道路径作为输出
	result = append(result, pf.pipePath)
	
	return result
}

// startsWithDash 检查字符串是否以破折号开头
func startsWithDash(s string) bool {
	return len(s) > 0 && s[0] == '-'
}

// wait 等待FFmpeg进程完成
func (pf *PipeForwarder) wait() {
	if pf.ffmpegCmd == nil {
		return
	}

	// 等待命令完成
	err := pf.ffmpegCmd.Wait()

	pf.mutex.Lock()
	pf.isRunning = false
	pf.mutex.Unlock()

	if err != nil {
		// 检查是否是由于取消引起的错误
		if pf.ctx.Err() == context.Canceled {
			log.Printf("PipeForwarder for stream %s was cancelled", pf.streamName)
		} else {
			log.Printf("PipeForwarder for stream %s failed: %v", pf.streamName, err)
		}
	} else {
		log.Printf("PipeForwarder for stream %s completed normally", pf.streamName)
	}

	// 调用停止回调
	if pf.onStopped != nil {
		pf.onStopped()
	}
}

// forwardDataFromPipe 从管道读取数据并分发到RTMP和HTTP客户端
func (pf *PipeForwarder) forwardDataFromPipe() {
	// 启动FFmpeg推送到RTMP服务器
	var ffmpegPush *exec.Cmd
	var ffIn io.WriteCloser
	
	if pf.rtmpURL != "" {
		ffmpegPush = exec.CommandContext(pf.ctx, "ffmpeg",
			"-re",
			"-i", "pipe:0",
			"-c", "copy",
			"-f", "flv",
			pf.rtmpURL,
		)
		ffmpegPush.Stderr = nil
		var err error
		ffIn, err = ffmpegPush.StdinPipe()
		if err != nil {
			log.Printf("Failed to create stdin pipe for RTMP push: %v", err)
			return
		}

		if err := ffmpegPush.Start(); err != nil {
			log.Printf("Failed to start RTMP push: %v", err)
			ffIn.Close()
			return
		}
	}

	// 启动数据分发协程
	go func() {
		defer func() {
			if ffmpegPush != nil {
				ffmpegPush.Wait()
			}
			if ffIn != nil {
				ffIn.Close()
			}
		}()

		// 在协程内部打开管道进行读取，确保在正确的上下文中管理
		pipe, err := os.OpenFile(pf.pipePath, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			log.Printf("Failed to open pipe %s for reading: %v", pf.pipePath, err)
			return
		}
		defer pipe.Close()

		buf := make([]byte, 4096)
		for {
			select {
			case <-pf.ctx.Done():
				return
			default:
				n, err := pipe.Read(buf)
				if err != nil {
					if err == io.EOF {
						break
					}
					// 只有在不是因为上下文取消导致的错误才记录
					if pf.ctx.Err() == nil {
						log.Printf("Read pipe error: %v", err)
					}
					break
				}
				if n > 0 {
					chunk := buf[:n]

					// 写入 RTMP 推流（如果配置了RTMP URL）
					if ffIn != nil {
						_, _ = ffIn.Write(chunk)
					}

					// 广播给本地所有播放客户端
					pf.clientsMutex.RLock()
					for ch := range pf.clients {
						select {
						case ch <- chunk:
						default:
						}
					}
					pf.clientsMutex.RUnlock()
				}
			}
		}
		log.Printf("Pipe forwarding stopped for stream %s.", pf.streamName)
	}()
}

// ServeFLV 通过HTTP提供FLV流媒体服务
func (pf *PipeForwarder) ServeFLV(w http.ResponseWriter, r *http.Request) {
	log.Printf("ServeFLV: Starting to serve FLV stream for pipe: %s", pf.pipePath)
	
	if !pf.enabled {
		log.Printf("ServeFLV: Pipe forwarder is disabled for pipe: %s", pf.pipePath)
		http.Error(w, "Pipe forwarder is disabled", http.StatusNotFound)
		return
	}

	// 检查管道文件是否存在
	if _, err := os.Stat(pf.pipePath); os.IsNotExist(err) {
		log.Printf("ServeFLV: Stream pipe not available at path: %s", pf.pipePath)
		http.Error(w, "Stream pipe not available", http.StatusServiceUnavailable)
		return
	}

	// 设置适当的FLV流媒体头
	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Connection", "close")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	
	// 通知客户端流开始
	w.WriteHeader(http.StatusOK)
	log.Printf("ServeFLV: HTTP headers sent, starting data transfer for pipe: %s", pf.pipePath)
	
	// 发送FLV头部信息
	// FLV头部: FLV signature (3 bytes) + version (1 byte) + flags (1 byte) + header size (4 bytes) + previous tag size (4 bytes)
	flvHeader := []byte{
		'F', 'L', 'V', // Signature
		0x01,          // Version
		0x05,          // Flags: audio + video
		0x00, 0x00, 0x00, 0x09, // Header size (9 bytes)
		0x00, 0x00, 0x00, 0x00, // Previous tag size (0 for first tag)
	}
	
	if _, err := w.Write(flvHeader); err != nil {
		log.Printf("ServeFLV: Failed to write FLV header: %v", err)
		return
	}
	
	// 确保头部数据被发送出去
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	
	// 创建客户端通道
	clientChan := make(chan []byte, 128)
	
	// 注册客户端
	pf.clientsMutex.Lock()
	pf.clients[clientChan] = struct{}{}
	pf.clientsMutex.Unlock()
	
	log.Printf("ServeFLV: Client registered, pipe: %s, total clients: %d", pf.pipePath, len(pf.clients))
	
	// 确保在函数退出时清理客户端
	defer func() {
		pf.clientsMutex.Lock()
		delete(pf.clients, clientChan)
		pf.clientsMutex.Unlock()
		close(clientChan)
		log.Printf("ServeFLV: Client unregistered, pipe: %s, remaining clients: %d", pf.pipePath, len(pf.clients))
	}()

	// 推送实时数据给客户端
	chunkCount := 0
	for {
		select {
		case chunk, ok := <-clientChan:
			if !ok {
				// 通道已关闭
				log.Printf("ServeFLV: Client channel closed for pipe: %s", pf.pipePath)
				return
			}
			
			chunkCount++
			_, err := w.Write(chunk)
			if err != nil {
				// 客户端断开连接
				log.Printf("ServeFLV: Client disconnected, pipe: %s, chunks sent: %d, error: %v", pf.pipePath, chunkCount, err)
				return
			}
			
			// 确保数据被发送出去
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			
			// 每100个块记录一次日志，避免日志过多
			if chunkCount%100 == 0 {
				log.Printf("ServeFLV: Sent %d chunks, current chunk size: %d bytes, pipe: %s", chunkCount, len(chunk), pf.pipePath)
			}
		case <-r.Context().Done():
			// 客户端请求已完成
			log.Printf("ServeFLV: Client request done, pipe: %s, chunks sent: %d", pf.pipePath, chunkCount)
			return
		case <-pf.ctx.Done():
			// PipeForwarder已停止
			log.Printf("ServeFLV: PipeForwarder stopped, pipe: %s, chunks sent: %d", pf.pipePath, chunkCount)
			return
		}
	}
}