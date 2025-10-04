package publisher

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os/exec"
	"strings"
	"syscall"
)

// GenerateStreamKey generates a stream key based on the configuration
func (s *Stream) GenerateStreamKey() (string, error) {
	// 如果没有配置streamkey，则生成默认的随机密钥
	if s.StreamKey.Type == "" && s.StreamKey.Value == "" && s.StreamKey.Length == 0 {
		// 生成默认的随机密钥
		return generateRandomString(16)
	}
	
	switch s.StreamKey.Type {
	case "random":
		length := s.StreamKey.Length
		if length <= 0 {
			length = 16 // 默认长度
		}
		return generateRandomString(length)
	case "fixed":
		return s.StreamKey.Value, nil
	case "":
		// 如果类型为空但有值，则使用固定值
		if s.StreamKey.Value != "" {
			return s.StreamKey.Value, nil
		}
		// 否则生成随机值
		return generateRandomString(16)
	default:
		return "", fmt.Errorf("unknown stream key type: %s", s.StreamKey.Type)
	}
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
		case strings.Contains(s.Stream.Source.URL, "http://") || strings.Contains(s.Stream.Source.URL, "https://"):
			// HTTP流的默认参数
			cmd = append(cmd, "-user_agent", "TVGate/1.0")
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
	videoCodec := "libx264"
	if s.FFmpegOptions != nil && s.FFmpegOptions.VideoCodec != "" {
		videoCodec = s.FFmpegOptions.VideoCodec
	}
	cmd = append(cmd, "-c:v", videoCodec)
	
	// Add audio codec - 默认音频编码器
	audioCodec := "aac"
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
	if streamKey != "" && !strings.HasSuffix(pushURL, "/") {
		pushURL = pushURL + "/" + streamKey
	} else if streamKey != "" {
		pushURL = pushURL + streamKey
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