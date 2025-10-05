package publisher

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"os/exec"
	"strings"
	"syscall"
	"time"
	
	"github.com/shirou/gopsutil/v3/process"
)

// GenerateStreamKey generates a stream key based on the configuration
func (s *Stream) GenerateStreamKey() (string, error) {
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
		if sc.Receivers.Backup != nil {
			receivers = append(receivers, *sc.Receivers.Backup)
		}
		return receivers
	case "all":
		return sc.Receivers.All
	default:
		// Default to empty slice
		return []Receiver{}
	}
}

// BuildFFmpegCommand builds the ffmpeg command based on the configuration
func (s *Stream) BuildFFmpegCommand() []string {
	var cmd []string
	
	// Add global arguments - 默认参数
	if s.FFmpegOptions == nil || len(s.FFmpegOptions.GlobalArgs) == 0 {
		// 默认全局参数
		cmd = append(cmd, "-re", "-fflags", "+genpts")
	} else if s.FFmpegOptions != nil && len(s.FFmpegOptions.GlobalArgs) > 0 {
		cmd = append(cmd, s.FFmpegOptions.GlobalArgs...)
	}
	
	// Add input pre arguments - 默认输入前参数
	if s.FFmpegOptions != nil && len(s.FFmpegOptions.InputPreArgs) > 0 {
		cmd = append(cmd, s.FFmpegOptions.InputPreArgs...)
	} else {
		// 默认输入前参数
		switch {
		case strings.Contains(s.Stream.Source.URL, "rtsp://"):
			// RTSP流的默认参数
			cmd = append(cmd, "-rtsp_transport", "tcp")
		}
	}
	
	// Add User-Agent if configured in FFmpegOptions
	if s.FFmpegOptions != nil && s.FFmpegOptions.UserAgent != "" {
		cmd = append(cmd, "-user_agent", s.FFmpegOptions.UserAgent)
	}
	
	// Add custom headers
	if s.FFmpegOptions != nil && len(s.FFmpegOptions.Headers) > 0 {
		for _, header := range s.FFmpegOptions.Headers {
			cmd = append(cmd, "-headers", header+"\r\n")
		}
	} else if s.Stream.Source.Headers != nil && len(s.Stream.Source.Headers) > 0 {
		// 兼容旧的source.headers配置
		var headersBuilder strings.Builder
		for key, value := range s.Stream.Source.Headers {
			headersBuilder.WriteString(key)
			headersBuilder.WriteString(": ")
			headersBuilder.WriteString(value)
			headersBuilder.WriteString("\r\n")
		}
		if headersBuilder.Len() > 0 {
			cmd = append(cmd, "-headers", headersBuilder.String())
		}
	}
	
	// Add source URL
	cmd = append(cmd, "-i", s.Stream.Source.URL)
	
	// Add input post arguments
	if s.FFmpegOptions != nil && len(s.FFmpegOptions.InputPostArgs) > 0 {
		cmd = append(cmd, s.FFmpegOptions.InputPostArgs...)
	}
	
	// Add filter arguments
	if s.FFmpegOptions != nil && s.FFmpegOptions.Filters != nil {
		if len(s.FFmpegOptions.Filters.VideoFilters) > 0 {
			cmd = append(cmd, "-vf", strings.Join(s.FFmpegOptions.Filters.VideoFilters, ","))
		}
		if len(s.FFmpegOptions.Filters.AudioFilters) > 0 {
			cmd = append(cmd, "-af", strings.Join(s.FFmpegOptions.Filters.AudioFilters, ","))
		}
	}
	
	// Add video codec - 默认视频编码器
	videoCodec := "copy"
	if s.FFmpegOptions != nil && s.FFmpegOptions.VideoCodec != "" {
		videoCodec = s.FFmpegOptions.VideoCodec
	}
	cmd = append(cmd, "-c:v", videoCodec)
	
	// Add audio codec - 默认音频编码器
	audioCodec := "copy"
	if s.FFmpegOptions != nil && s.FFmpegOptions.AudioCodec != "" {
		audioCodec = s.FFmpegOptions.AudioCodec
	}
	cmd = append(cmd, "-c:a", audioCodec)
	
	// Add video bitrate - 默认视频码率
	videoBitrate := "2M"
	if s.FFmpegOptions != nil && s.FFmpegOptions.VideoBitrate != "" {
		videoBitrate = s.FFmpegOptions.VideoBitrate
	}
	cmd = append(cmd, "-b:v", videoBitrate)
	
	// Add audio bitrate - 默认音频码率
	audioBitrate := "128k"
	if s.FFmpegOptions != nil && s.FFmpegOptions.AudioBitrate != "" {
		audioBitrate = s.FFmpegOptions.AudioBitrate
	}
	cmd = append(cmd, "-b:a", audioBitrate)
	
	// Add preset - 默认编码预设
	preset := "ultrafast"
	if s.FFmpegOptions != nil && s.FFmpegOptions.Preset != "" {
		preset = s.FFmpegOptions.Preset
	}
	cmd = append(cmd, "-preset", preset)
	
	// Add CRF
	if s.FFmpegOptions != nil && s.FFmpegOptions.CRF > 0 {
		cmd = append(cmd, "-crf", fmt.Sprintf("%d", s.FFmpegOptions.CRF))
	}
	
	// Add output format - 默认输出格式
	outputFormat := "flv"
	if s.FFmpegOptions != nil && s.FFmpegOptions.OutputFormat != "" {
		outputFormat = s.FFmpegOptions.OutputFormat
	}
	cmd = append(cmd, "-f", outputFormat)
	
	// Add output pre arguments
	if s.FFmpegOptions != nil && len(s.FFmpegOptions.OutputPreArgs) > 0 {
		cmd = append(cmd, s.FFmpegOptions.OutputPreArgs...)
	}
	
	// Add custom arguments
	if s.FFmpegOptions != nil && len(s.FFmpegOptions.CustomArgs) > 0 {
		cmd = append(cmd, s.FFmpegOptions.CustomArgs...)
	}
	
	return cmd
}

// BuildFFmpegPushCommand builds the ffmpeg push command for a receiver
func (r *Receiver) BuildFFmpegPushCommand(baseCmd []string, streamKey string) []string {
	cmd := make([]string, len(baseCmd))
	copy(cmd, baseCmd)
	
	// Add push pre arguments
	if len(r.PushPreArgs) > 0 {
		cmd = append(cmd, r.PushPreArgs...)
	}
	
	// Add push URL with stream key
	pushURL := r.PushURL
	// 如果URL中已经包含密钥，则替换它
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
	
	cmd = append(cmd, pushURL)
	
	// Add push post arguments
	if len(r.PushPostArgs) > 0 {
		cmd = append(cmd, r.PushPostArgs...)
	}
	
	return cmd
}

// ExecuteFFmpeg executes the ffmpeg command
func (s *Stream) ExecuteFFmpeg(ctx context.Context, args []string) error {
	// Create the command
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	
	// Set process group ID to allow killing child processes
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	
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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	
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
	go func() {
		done <- cmd.Wait()
	}()
	
	// Monitor the process if we have callbacks
	if (onStarted != nil || onStatsUpdate != nil) && cmd.Process != nil {
		pid := int32(cmd.Process.Pid)
		go s.monitorFFmpegProcess(ctx, pid, onStatsUpdate)
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
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
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
		log.Printf("Failed to get process %d: %v", pid, err)
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

// CheckStreamKeyExpiration 检查stream key是否过期
func (s *Stream) CheckStreamKeyExpiration(streamKey string, createdAt time.Time) bool {
	log.Printf("Checking stream key expiration: type=%s, value=%s, expiration=%s, created=%v", 
		s.StreamKey.Type, streamKey, s.StreamKey.Expiration, createdAt)
	
	// 如果是固定密钥，永不过期
	if s.StreamKey.Type == "fixed" && s.StreamKey.Value != "" {
		log.Printf("Fixed stream key, never expires")
		return false
	}
	
	// 检查创建时间是否有效
	if createdAt.IsZero() {
		log.Printf("Stream key creation time is zero, treating as expired")
		return true
	}
	
	// 如果配置了过期时间，则检查是否过期
	if s.StreamKey.Expiration != "" {
		// 解析时间字符串
		duration, err := time.ParseDuration(s.StreamKey.Expiration)
		if err != nil {
			// 如果解析失败，使用默认24小时
			log.Printf("Failed to parse expiration duration '%s', using default 24h: %v", s.StreamKey.Expiration, err)
			duration = 24 * time.Hour
		}
		expired := time.Since(createdAt) > duration
		log.Printf("Stream key age: %v, expiration: %v, expired: %t", time.Since(createdAt), duration, expired)
		return expired
	}
	
	// 默认24小时过期
	expired := time.Since(createdAt) > 24*time.Hour
	log.Printf("Using default 24h expiration, expired: %t", expired)
	return expired
}

// UpdateStreamKey 更新stream key
func (s *Stream) UpdateStreamKey() (string, error) {
	return s.GenerateStreamKey()
}

