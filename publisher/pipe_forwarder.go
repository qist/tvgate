package publisher

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	
	"github.com/qist/tvgate/stream"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
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
	hub          *stream.StreamHubs
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
		hub:        stream.NewStreamHubs(),
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

	// 创建FIFO
	if err := pf.createPipe(); err != nil {
		return fmt.Errorf("failed to create pipe: %v", err)
	}

	// 修改FFmpeg命令
	ffmpegCmd := pf.modifyFFmpegCommand(ffmpegArgs)

	// 启动FFmpeg写入管道
	pf.ffmpegCmd = exec.CommandContext(pf.ctx, "ffmpeg", ffmpegCmd...)
	pf.ffmpegCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pf.ffmpegCmd.Stderr = os.Stderr
	pf.ffmpegCmd.Stdout = os.Stdout

	if err := pf.ffmpegCmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	pf.isRunning = true
	log.Printf("Started PipeForwarder for stream %s, writing to pipe: %s", pf.streamName, pf.pipePath)
	log.Printf("Full FFmpeg command: ffmpeg %s", strings.Join(ffmpegCmd, " "))

	// 等待管道写入FLV头部
	go pf.forwardDataFromPipe()

	if pf.onStarted != nil {
		go pf.onStarted()
	}

	// 等待FFmpeg进程结束
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

	if pf.ffmpegCmd != nil && pf.ffmpegCmd.Process != nil {
		syscall.Kill(-pf.ffmpegCmd.Process.Pid, syscall.SIGKILL)
	}

	pf.hub.Close()

	if pf.onStopped != nil {
		pf.onStopped()
	}

	log.Printf("Stopped PipeForwarder for stream %s", pf.streamName)
}

func (pf *PipeForwarder) IsRunning() bool {
	pf.mutex.Lock()
	defer pf.mutex.Unlock()
	return pf.isRunning
}

// createPipe 创建FIFO
func (pf *PipeForwarder) createPipe() error {
	if _, err := os.Stat(pf.pipePath); err == nil {
		os.Remove(pf.pipePath)
	} else if !os.IsNotExist(err) {
		return err
	}
	return syscall.Mkfifo(pf.pipePath, 0666)
}

// modifyFFmpegCommand 修改FFmpeg命令以输出到管道
func (pf *PipeForwarder) modifyFFmpegCommand(args []string) []string {
	// 如果参数为空，直接返回
	if len(args) == 0 {
		return args
	}
	
	// 创建结果切片
	result := make([]string, 0, len(args)+5)
	
	// 添加-y参数（覆盖输出文件）
	result = append(result, "-y")
	
	// 添加除最后一个参数外的所有参数（移除原始输出URL）
	// 同时查找并移除任何现有的-f参数对和-flvflags参数对
	for i := 0; i < len(args)-1; i++ {
		// 跳过-f参数及其后续参数
		if args[i] == "-f" {
			i++ // 跳过下一个参数
			continue
		}
		// 跳过-flvflags参数及其后续参数
		if args[i] == "-flvflags" {
			i++ // 跳过下一个参数
			continue
		}
		// 跳过-fflags参数及其后续参数（避免重复）
		if args[i] == "-fflags" {
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

// forwardDataFromPipe 从管道读取并分发
func (pf *PipeForwarder) forwardDataFromPipe() {
	var ffmpegPush *exec.Cmd
	var ffIn io.WriteCloser

	if pf.rtmpURL != "" {
		ffmpegPush = exec.CommandContext(pf.ctx, "ffmpeg", "-re", "-i", "pipe:0", "-c", "copy", "-f", "flv", pf.rtmpURL)
		var err error
		ffIn, err = ffmpegPush.StdinPipe()
		if err != nil {
			log.Printf("Failed to create stdin pipe: %v", err)
			return
		}
		ffmpegPush.Stderr = os.Stderr
		ffmpegPush.Stdout = os.Stdout
		if err := ffmpegPush.Start(); err != nil {
			log.Printf("Failed to start RTMP push: %v", err)
			return
		}
	}

	pipe, err := os.OpenFile(pf.pipePath, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		log.Printf("Failed to open pipe: %v", err)
		return
	}
	defer pipe.Close()

	// 设置hub为播放状态
	pf.hub.SetPlaying()

	buf := make([]byte, 8192)
	for {
		select {
		case <-pf.ctx.Done():
			return
		default:
			n, err := pipe.Read(buf)
			if n > 0 {
				// 创建数据副本以避免竞态条件
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				
				// 将数据写入RTMP推流（如果配置了RTMP URL）
				if ffIn != nil {
					_, _ = ffIn.Write(chunk)
				}
				
				// 将数据广播到hub
				pf.hub.Broadcast(chunk)
			}
			if err != nil {
				if err != io.EOF && pf.ctx.Err() == nil {
					log.Printf("Pipe read error: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
}

// wait 等待FFmpeg结束
func (pf *PipeForwarder) wait() {
	if pf.ffmpegCmd == nil {
		return
	}
	err := pf.ffmpegCmd.Wait()
	pf.mutex.Lock()
	pf.isRunning = false
	pf.mutex.Unlock()
	if err != nil && pf.ctx.Err() != context.Canceled {
		log.Printf("FFmpeg exited with error: %v", err)
	} else {
		log.Printf("FFmpeg exited normally")
	}
	if pf.onStopped != nil {
		pf.onStopped()
	}
}


// broadcast 函数已移除，由 stream.StreamHubs 替代

// ServeFLV 提供HTTP FLV流
func (pf *PipeForwarder) ServeFLV(w http.ResponseWriter, r *http.Request) {
	if !pf.enabled {
		http.Error(w, "Pipe forwarder disabled", http.StatusNotFound)
		return
	}
	if _, err := os.Stat(pf.pipePath); os.IsNotExist(err) {
		http.Error(w, "Pipe not ready", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Connection", "close")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)

	// 创建一个新的ring buffer作为客户端通道
	clientBuffer, err := ringbuffer.New(1024 * 1024) // 1MB缓冲区
	if err != nil {
		log.Printf("Failed to create ring buffer: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// 注册客户端到hub
	pf.hub.AddClient(clientBuffer)

	// 确保在函数退出时清理客户端
	defer func() {
		pf.hub.RemoveClient(clientBuffer)
	}()

	// 从ring buffer读取数据并发送到HTTP响应
	for {
		select {
		case <-r.Context().Done():
			return
		case <-pf.ctx.Done():
			return
		default:
			data, ok := clientBuffer.PullWithContext(r.Context())
			if !ok {
				// buffer已关闭或客户端断开连接
				return
			}

			// 将FFmpeg输出的原始FLV数据写入HTTP响应
			if _, err := w.Write(data.([]byte)); err != nil {
				return
			}

			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}
