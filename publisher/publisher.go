package publisher

import (
	"context"
	"fmt"

	// "log"
	"net/http"
	"strings"
	"time"

	"crypto/rand"
	"math/big"

	"os/exec"
	"syscall"

	"github.com/qist/tvgate/logger"
	tsync "github.com/qist/tvgate/utils/sync"
	"github.com/shirou/gopsutil/v3/process"
)

// GenerateStreamKey generates a stream key based on the configuration
func (s *Stream) GenerateStreamKey() (string, error) {
	// 如果是external类型，则不生成stream key，直接返回空字符串
	if s.StreamKey.Type == "external" {
		return "", nil
	}

	// 如果已经配置了固定的stream key值，直接使用它
	if s.StreamKey.Type == "fixed" && s.StreamKey.Value != "" {
		return s.StreamKey.Value, nil
	}

	// 如果是随机类型或者没有指定类型但有长度配置
	if s.StreamKey.Type == "random" || (s.StreamKey.Type == "" && s.StreamKey.Length > 0) {
		length := s.StreamKey.Length
		if length <= 0 {
			length = 16 // 默认长度
		}
		return generateRandomString(length)
	}

	// 如果没有配置streamkey，则生成默认的随机密钥
	if s.StreamKey.Type == "" && s.StreamKey.Value == "" && s.StreamKey.Length == 0 {
		return generateRandomString(16)
	}

	// 其他情况使用配置的值
	if s.StreamKey.Value != "" {
		return s.StreamKey.Value, nil
	}

	// 默认生成随机密钥
	return generateRandomString(16)
}

// generateRandomString generates a random string of specified length
func generateRandomString(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}
	return string(result), nil
}

// GetReceivers returns the receivers based on the mode
func (sc *StreamConfig) GetReceivers() []Receiver {
	switch sc.Mode {
	case "primary-backup":
		var receivers []Receiver
		if sc.Receivers.Primary != nil {
			receivers = append(receivers, *sc.Receivers.Primary)
		}
		// 只有在明确需要 backup 时才添加 backup receiver
		// 这将在实际检测到 primary 失效时在运行时动态添加
		return receivers
	case "all":
		return sc.Receivers.All
	default:
		// Default to empty slice
		return []Receiver{}
	}
}

// BuildFFmpegCommandForReceiver builds the ffmpeg command using the receiver's FFmpeg options
func (s *Stream) BuildFFmpegCommandForReceiver(receiver *Receiver) []string {
	// 使用接收者的FFmpeg选项
	return s.BuildFFmpegCommandWithOptions(receiver.FFmpegOptions)
}

// BuildFFmpegCommand builds the ffmpeg command based on the configuration
func (s *Stream) BuildFFmpegCommand() []string {
	// 默认使用源FFmpegOptions
	return s.BuildFFmpegCommandWithOptions(s.Stream.Source.FFmpegOptions)
}

// BuildFFmpegCommandWithOptions builds the ffmpeg command with specific FFmpeg options
func (s *Stream) BuildFFmpegCommandWithOptions(ffmpegOptions *FFmpegOptions) []string {
	var cmd []string

	// 处理全局参数和-re标志
	if ffmpegOptions == nil || len(ffmpegOptions.GlobalArgs) == 0 {
		// 使用默认全局参数（包含-re）
		cmd = append(cmd, "-re", "-fflags", "+genpts")
	} else {
		// 使用配置的全局参数
		cmd = append(cmd, ffmpegOptions.GlobalArgs...)

		// 如果UseReFlag为true但全局参数中没有-re，则添加
		if ffmpegOptions.UseReFlag {
			// 检查是否已经包含-re
			hasRe := false
			for _, arg := range ffmpegOptions.GlobalArgs {
				if arg == "-re" {
					hasRe = true
					break
				}
			}
			if !hasRe {
				cmd = append(cmd, "-re")
			}
		}
	}

	// Add input pre arguments - 默认输入前参数
	if ffmpegOptions != nil && len(ffmpegOptions.InputPreArgs) > 0 {
		cmd = append(cmd, ffmpegOptions.InputPreArgs...)
	} else {
		// 默认输入前参数
		switch {
		case strings.Contains(s.Stream.Source.URL, "rtsp://"):
			// RTSP流的默认参数
			cmd = append(cmd, "-rtsp_transport", "tcp")
		case strings.Contains(s.Stream.Source.URL, "http://") || strings.Contains(s.Stream.Source.URL, "https://"):
			// HTTP流的默认参数
			cmd = append(cmd, "-user_agent", "TVGate/1.0")
		}
	}

	// Add User-Agent if configured in FFmpegOptions
	if ffmpegOptions != nil && ffmpegOptions.UserAgent != "" {
		cmd = append(cmd, "-user_agent", ffmpegOptions.UserAgent)
	}

	if ffmpegOptions != nil && len(ffmpegOptions.Headers) > 0 {
		headers := ""
		for _, h := range ffmpegOptions.Headers {
			headers += h + "\r\n"
		}
		cmd = append(cmd, "-headers", headers)
	}

	// Add source URL
	cmd = append(cmd, "-i", s.Stream.Source.URL)

	// Add input post arguments
	if ffmpegOptions != nil && len(ffmpegOptions.InputPostArgs) > 0 {
		cmd = append(cmd, ffmpegOptions.InputPostArgs...)
	}

	// Add filter arguments
	if ffmpegOptions != nil && ffmpegOptions.Filters != nil {
		if len(ffmpegOptions.Filters.VideoFilters) > 0 {
			cmd = append(cmd, "-vf", strings.Join(ffmpegOptions.Filters.VideoFilters, ","))
		}
		if len(ffmpegOptions.Filters.AudioFilters) > 0 {
			cmd = append(cmd, "-af", strings.Join(ffmpegOptions.Filters.AudioFilters, ","))
		}
	}

	// Add video codec - 默认视频编码器
	videoCodec := "libx264"
	if ffmpegOptions != nil && ffmpegOptions.VideoCodec != "" {
		videoCodec = ffmpegOptions.VideoCodec
	}
	// 如果使用copy模式，确保不添加其他视频参数
	if videoCodec != "copy" {
		cmd = append(cmd, "-c:v", videoCodec)
	} else {
		cmd = append(cmd, "-c:v", "copy")
	}

	// Add audio codec - 默认音频编码器
	audioCodec := "aac"
	if ffmpegOptions != nil && ffmpegOptions.AudioCodec != "" {
		audioCodec = ffmpegOptions.AudioCodec
	}
	// 如果使用copy模式，确保不添加其他音频参数
	if audioCodec != "copy" {
		cmd = append(cmd, "-c:a", audioCodec)
	} else {
		cmd = append(cmd, "-c:a", "copy")
	}

	// Only add video bitrate if not using copy codec
	if videoCodec != "copy" {
		videoBitrate := "4M"
		if ffmpegOptions != nil && ffmpegOptions.VideoBitrate != "" {
			videoBitrate = ffmpegOptions.VideoBitrate
		}
		cmd = append(cmd, "-b:v", videoBitrate)
	}

	// Only add audio bitrate if not using copy codec
	if audioCodec != "copy" {
		audioBitrate := "128k"
		if ffmpegOptions != nil && ffmpegOptions.AudioBitrate != "" {
			audioBitrate = ffmpegOptions.AudioBitrate
		}
		cmd = append(cmd, "-b:a", audioBitrate)
	}

	// Add preset - 默认编码预设 (only if not using copy)
	if videoCodec != "copy" {
		preset := "ultrafast"
		if ffmpegOptions != nil && ffmpegOptions.Preset != "" {
			preset = ffmpegOptions.Preset
		}
		cmd = append(cmd, "-preset", preset)
	}

	// Add CRF (only if not using copy)
	if videoCodec != "copy" && ffmpegOptions != nil && ffmpegOptions.CRF > 0 {
		cmd = append(cmd, "-crf", fmt.Sprintf("%d", ffmpegOptions.CRF))
	}

	// Add pixel format if specified
	if ffmpegOptions != nil && ffmpegOptions.PixFmt != "" {
		cmd = append(cmd, "-pix_fmt", ffmpegOptions.PixFmt)
	}

	// Add GOP size if specified
	if ffmpegOptions != nil && ffmpegOptions.GopSize > 0 {
		cmd = append(cmd, "-g", fmt.Sprintf("%d", ffmpegOptions.GopSize))
	}

	// Add output format - 默认输出格式
	outputFormat := "flv"
	if ffmpegOptions != nil && ffmpegOptions.OutputFormat != "" {
		outputFormat = ffmpegOptions.OutputFormat
	}
	cmd = append(cmd, "-f", outputFormat)

	// Add output pre arguments
	if ffmpegOptions != nil && len(ffmpegOptions.OutputPreArgs) > 0 {
		cmd = append(cmd, ffmpegOptions.OutputPreArgs...)
	}

	// Add custom arguments after input
	if ffmpegOptions != nil && len(ffmpegOptions.CustomArgs) > 0 {
		cmd = append(cmd, ffmpegOptions.CustomArgs...)
	}

	return cmd
}

// BuildFFmpegPushCommand builds the ffmpeg push command by appending push arguments to the base command
func (r *Receiver) BuildFFmpegPushCommand(baseCmd []string, streamKey string) []string {
	// Create a copy of the base command
	cmd := make([]string, len(baseCmd))
	copy(cmd, baseCmd)

	// Remove duplicate flags from the base command to prevent duplication
	cmd = RemoveDuplicateFlagArgs(cmd)

	// Add push pre arguments
	if len(r.PushPreArgs) > 0 {
		tempCmd := append(cmd, r.PushPreArgs...)
		cmd = RemoveDuplicateFlagArgs(tempCmd)
	}

	// Add push URL with stream key
	pushURL := r.PushURL
	// 如果URL中已经包含密钥，则替换它
	// 但如果是external类型，则不进行替换
	if streamKey != "" {
		// 从URL中提取可能的旧密钥
		oldKey := ""
		// 处理RTMP URL
		if strings.HasPrefix(pushURL, "rtmp://") {
			// 从路径中提取密钥
			parts := strings.Split(pushURL, "/")
			if len(parts) > 0 {
				// 密钥通常是最后一个部分
				lastPart := parts[len(parts)-1]
				// 如果包含查询参数，去掉查询参数
				if strings.Contains(lastPart, "?") {
					lastPart = strings.Split(lastPart, "?")[0]
				}
				oldKey = lastPart
			}
		}

		// 处理HTTP URL
		if strings.Contains(pushURL, "http://") || strings.Contains(pushURL, "https://") {
			// 从路径中提取密钥
			parts := strings.Split(pushURL, "/")
			if len(parts) > 0 {
				// 密钥通常是最后一个部分（去除可能的文件扩展名）
				lastPart := parts[len(parts)-1]
				// 如果包含查询参数，去掉查询参数
				if strings.Contains(lastPart, "?") {
					lastPart = strings.Split(lastPart, "?")[0]
				}
				// 如果以.flv或.m3u8结尾，去掉扩展名
				if strings.HasSuffix(lastPart, ".flv") {
					lastPart = strings.TrimSuffix(lastPart, ".flv")
				} else if strings.HasSuffix(lastPart, ".m3u8") {
					lastPart = strings.TrimSuffix(lastPart, ".m3u8")
				}
				oldKey = lastPart
			}
		}

		// 如果找到了旧密钥，则替换它
		if oldKey != "" {
			// 替换URL中的旧密钥为新密钥
			if strings.HasSuffix(pushURL, oldKey) {
				pushURL = strings.TrimSuffix(pushURL, oldKey) + streamKey
			} else if strings.Contains(pushURL, "/"+oldKey+"/") {
				// 处理路径中包含密钥的情况 (如: /path/oldkey/oldkey)
				pushURL = strings.Replace(pushURL, "/"+oldKey+"/"+oldKey, "/"+streamKey+"/"+streamKey, 1)
			} else if strings.Contains(pushURL, "/"+oldKey+".flv") {
				pushURL = strings.Replace(pushURL, "/"+oldKey+".flv", "/"+streamKey+".flv", 1)
			} else if strings.Contains(pushURL, "/"+oldKey+".m3u8") {
				pushURL = strings.Replace(pushURL, "/"+oldKey+".m3u8", "/"+streamKey+".m3u8", 1)
			} else {
				// 如果无法匹配特定模式，则在末尾添加新密钥
				if !strings.HasSuffix(pushURL, "/") {
					pushURL = pushURL + "/" + streamKey
				} else {
					pushURL = pushURL + streamKey
				}
			}
		} else {
			// 如果没有找到旧密钥，则在末尾添加新密钥
			if !strings.HasSuffix(pushURL, "/") {
				pushURL = pushURL + "/" + streamKey
			} else {
				pushURL = pushURL + streamKey
			}
		}
	}
	// 如果streamKey为空（external类型），则使用原始pushURL

	cmd = append(cmd, pushURL)

	// Add push post arguments
	if len(r.PushPostArgs) > 0 {
		tempCmd := append(cmd, r.PushPostArgs...)
		cmd = RemoveDuplicateFlagArgs(tempCmd)
	}

	// Final pass to remove any duplicate flags
	cmd = RemoveDuplicateFlagArgs(cmd)

	return cmd
}

// ExecuteFFmpeg executes the ffmpeg command
func (s *Stream) ExecuteFFmpeg(ctx context.Context, args []string) error {
	// Create the command
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// Set process group ID to allow killing child processes
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	setSysProcAttr(cmd.SysProcAttr)
	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg execution failed: %v", err)
	}

	return nil
}

// ExecuteFFmpegWithMonitoring executes the ffmpeg command with monitoring capabilities
func (s *Stream) ExecuteFFmpegWithMonitoring(ctx context.Context, args []string, onStarted func(int32, *process.Process), onStatsUpdate func(uint64)) error {
	// Create the command
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// Set process group ID to allow killing child processes
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	setSysProcAttr(cmd.SysProcAttr)

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	// If we have a callback for when the process starts, call it
	if onStarted != nil && cmd.Process != nil {
		// Wrap the process with gopsutil
		proc, err := process.NewProcess(int32(cmd.Process.Pid))
		if err == nil {
			onStarted(int32(cmd.Process.Pid), proc)
		}
	}

	// Channel to signal when the process has finished
	done := make(chan error, 1)
	var wg tsync.WaitGroup
	wg.Go(func() {
		done <- cmd.Wait()
	})

	// Monitor the process if we have callbacks
	if (onStarted != nil || onStatsUpdate != nil) && cmd.Process != nil {
		pid := int32(cmd.Process.Pid)
		wg.Go(func() {
			s.monitorFFmpegProcess(ctx, pid, onStatsUpdate)
		})
	}

	// Wait for the command to finish or context to be cancelled
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("ffmpeg execution failed: %v", err)
		}
		return nil
	case <-ctx.Done():
		// Kill the process group when context is cancelled
		if cmd.Process != nil {
			// Kill the entire process group
			killProcess(-cmd.Process.Pid)
		}
		return ctx.Err()
	}
}

// monitorFFmpegProcess monitors an FFmpeg process and provides stats updates
func (s *Stream) monitorFFmpegProcess(ctx context.Context, pid int32, onStatsUpdate func(uint64)) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastIOCounters *process.IOCountersStat

	// Get process object
	proc, err := process.NewProcess(pid)
	if err != nil {
		logger.LogPrintf("Failed to get process %d: %v", pid, err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get IO counters to track bytes transferred
			ioCounters, err := proc.IOCounters()
			if err != nil {
				// Process might have exited
				return
			}

			// Calculate bytes transferred since last check
			var bytesTransferred uint64
			if lastIOCounters != nil {
				// Sum of read and write bytes
				bytesTransferred = (ioCounters.ReadBytes - lastIOCounters.ReadBytes) +
					(ioCounters.WriteBytes - lastIOCounters.WriteBytes)
			}

			lastIOCounters = ioCounters

			// Call the stats update callback if provided
			if onStatsUpdate != nil {
				onStatsUpdate(bytesTransferred)
			}
		}
	}
}

// BuildLocalPlayURL 构建本地播放URL，根据协议类型添加streamkey和扩展名
func (s *Stream) BuildLocalPlayURL(baseURL string, streamKey string, protocol string) string {
	if baseURL == "" {
		return ""
	}

	// 确保URL以/结尾
	if !strings.HasSuffix(baseURL, "/") {
		baseURL = baseURL + "/"
	}

	switch protocol {
	case "flv":
		return baseURL + streamKey + ".flv"
	case "hls":
		// 可以是 streamkey.m3u8 或 streamkey/index.m3u8
		return baseURL + streamKey + ".m3u8"
	default:
		return baseURL + streamKey
	}
}

// BuildReceiverPlayURL 构建接收端播放URL
func (r *Receiver) BuildReceiverPlayURL(baseURL string, streamKey string, protocol string) string {
	if baseURL == "" {
		return ""
	}

	// 确保URL以/结尾
	if !strings.HasSuffix(baseURL, "/") {
		baseURL = baseURL + "/"
	}

	switch protocol {
	case "flv":
		return baseURL + streamKey + ".flv"
	case "hls":
		// 可以是 streamkey.m3u8 或 streamkey/index.m3u8
		// 这里我们使用更常见的格式
		if strings.HasSuffix(baseURL, "/index.m3u8") {
			// 如果已经配置了完整的路径
			return baseURL
		}
		return baseURL + streamKey + "/index.m3u8"
	default:
		return baseURL + streamKey
	}
}

// CheckStreamKeyExpiration checks if a stream key has expired
func (s *Stream) CheckStreamKeyExpiration(streamKey string, createdAt time.Time) bool {
	if s.StreamKey.Type == "external" || s.StreamKey.Type == "fixed" {
		logger.LogPrintf("Stream key type is %s, never expires", s.StreamKey.Type)
		return false
	}
	if streamKey == "" {
		logger.LogPrintf("Stream key is empty, considering as expired")
		return true
	}

	// 如果CreatedAt为零值，设置为当前时间
	if createdAt.IsZero() {
		logger.LogPrintf("CreatedAt is zero, setting to current time")
		createdAt = time.Now()
	}

	// 检查是否配置了过期时间
	if s.StreamKey.Expiration != "" && s.StreamKey.Expiration != "0" {
		// 解析过期时间
		expiration, err := time.ParseDuration(s.StreamKey.Expiration)
		if err != nil {
			logger.LogPrintf("Failed to parse expiration duration '%s': %v, using default 24h", s.StreamKey.Expiration, err)
			expiration = 24 * time.Hour
		}

		// 检查是否过期
		expired := time.Since(createdAt) > expiration
		logger.LogPrintf("Stream key created at %v, expiration %v, expired: %t", createdAt, expiration, expired)
		return expired
	}

	// 默认24小时过期
	expired := time.Since(createdAt) > 24*time.Hour
	logger.LogPrintf("Using default 24h expiration, expired: %t", expired)
	return expired
}

// UpdateStreamKey 更新stream key
func (s *Stream) UpdateStreamKey() (string, error) {
	return s.GenerateStreamKey()
}

// ServeFLV serves FLV stream via HTTP by reading from the pipe

func (s *Stream) ServeFLV(w http.ResponseWriter, r *http.Request, streamName, streamKey string) {
	manager := GetManager()
	if manager == nil {
		http.Error(w, "Publisher manager not available", http.StatusServiceUnavailable)
		return
	}

	// 每次请求都动态获取最新流管理器实例
	manager.mutex.RLock()
	_, exists := manager.streams[streamName]
	manager.mutex.RUnlock()

	if !exists {
		http.Error(w, "Stream not found", http.StatusNotFound)
		return
	}

	// 使用StreamHub提供服务，实现解耦
	streamHub := GetStreamHub(streamName)
	streamHub.ServeFLV(w, r)
}
