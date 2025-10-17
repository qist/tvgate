package publisher

import (
	// "log"
	"fmt"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	manager *Manager
	handler *Handler
	once    sync.Once
)

// Init initializes the publisher module
func Init() error {
	var initErr error
	once.Do(func() {
		// Check if publisher config exists
		if config.Cfg.Publisher == nil {
			logger.LogPrintf("Publisher config not found, skipping initialization")
			return
		}
		ffmpegPath, err := FindFFmpeg()
		if err != nil {
			logger.LogPrintf("FFmpeg 未找到: %v", err)
		} else {
			logger.LogPrintf("✅ 已检测到 FFmpeg: %s", ffmpegPath)
		}
		// Convert config types
		publisherConfig := convertConfig(config.Cfg.Publisher)

		// Create manager and handler
		manager = NewManager(publisherConfig)
		handler = NewHandler(manager)

		// Start the manager
		if err := manager.Start(); err != nil {
			initErr = err
			return
		}

		// 启动配置文件监控
		if config.ConfigFilePath != nil {
			go WatchConfigFile(*config.ConfigFilePath)
		}

		logger.LogPrintf("Publisher module initialized successfully")
	})

	return initErr
}

// GetHandler returns the publisher HTTP handler
func GetHandler() http.Handler {
	return handler
}

// Stop stops the publisher module
func Stop() {
	if manager != nil {
		manager.Stop()
		logger.LogPrintf("Publisher module stopped")
	}
}

// convertConfig converts config.PublisherConfig to publisher.Config
func convertConfig(cfg *config.PublisherConfig) *Config {
	if cfg == nil {
		return nil
	}

	publisherCfg := &Config{
		Path:    cfg.Path,
		Streams: make(map[string]*Stream),
	}

	logger.LogPrintf("Converting config with %d streams", len(cfg.Streams))
	for name, streamItem := range cfg.Streams {
		logger.LogPrintf("Processing stream: %s", name)

		// 生成 streamKey
		streamKey := streamItem.StreamKey.Value
		if streamKey == "" && streamItem.StreamKey.Type == "random" {
			streamKey = "test_stream_key"
		}

		// Source FFmpegOptions
		sourceOpts := convertFFmpegOptions(streamItem.Stream.Source.FFmpegOptions)

		// 转换 LocalPlayUrls
		localPlayUrls := convertLocalPlayUrls(streamItem.Stream.LocalPlayUrls, sourceOpts)

		// 转换 Receivers
		receivers := convertReceivers(streamItem.Stream.Receivers, sourceOpts)

		// 构建 Stream
		stream := &Stream{
			BufferSize: streamItem.BufferSize,
			Protocol:   streamItem.Protocol,
			Enabled:    streamItem.Enabled,
			StreamKey: StreamKey{
				Type:       streamItem.StreamKey.Type,
				Value:      streamKey,
				Length:     streamItem.StreamKey.Length,
				Expiration: streamItem.StreamKey.Expiration,
			},
			Stream: StreamConfig{
				Source:        Source{Type: streamItem.Stream.Source.Type, URL: streamItem.Stream.Source.URL, BackupURL: streamItem.Stream.Source.BackupURL, FFmpegOptions: sourceOpts},
				LocalPlayUrls: localPlayUrls,
				Mode:          streamItem.Stream.Mode,
				Receivers:     receivers,
			},
		}

		// 添加到 publisherCfg
		publisherCfg.Streams[name] = stream
		logger.LogPrintf("Added stream %s to publisher config", name)

		// 打印最终有效参数
		fmt.Printf("Stream %s Source FFmpegOptions: %+v\n", name, sourceOpts)

		// 打印接收者的FFmpeg选项
		if stream.Stream.Receivers.Primary != nil {
			fmt.Printf("Stream %s Primary Receiver FFmpegOptions: %+v\n", name, stream.Stream.Receivers.Primary.FFmpegOptions)
			// 打印GlobalArgs和InputPreArgs的详细信息
			fmt.Printf("Stream %s Primary Receiver GlobalArgs: %+v\n", name, stream.Stream.Receivers.Primary.FFmpegOptions.GlobalArgs)
			fmt.Printf("Stream %s Primary Receiver InputPreArgs: %+v\n", name, stream.Stream.Receivers.Primary.FFmpegOptions.InputPreArgs)
		}
		if stream.Stream.Receivers.Backup != nil {
			fmt.Printf("Stream %s Backup Receiver FFmpegOptions: %+v\n", name, stream.Stream.Receivers.Backup.FFmpegOptions)
			// 打印GlobalArgs和InputPreArgs的详细信息
			fmt.Printf("Stream %s Backup Receiver GlobalArgs: %+v\n", name, stream.Stream.Receivers.Backup.FFmpegOptions.GlobalArgs)
			fmt.Printf("Stream %s Backup Receiver InputPreArgs: %+v\n", name, stream.Stream.Receivers.Backup.FFmpegOptions.InputPreArgs)
		}
		for i, receiver := range stream.Stream.Receivers.All {
			fmt.Printf("Stream %s All Receiver[%d] FFmpegOptions: %+v\n", name, i, receiver.FFmpegOptions)
			// 打印GlobalArgs和InputPreArgs的详细信息
			fmt.Printf("Stream %s All Receiver[%d] GlobalArgs: %+v\n", name, i, receiver.FFmpegOptions.GlobalArgs)
			fmt.Printf("Stream %s All Receiver[%d] InputPreArgs: %+v\n", name, i, receiver.FFmpegOptions.InputPreArgs)
		}

	}

	return publisherCfg
}

// convertLocalPlayUrls converts PlayOutputItem to PlayOutput and merges FFmpegOptions with source
func convertLocalPlayUrls(outputs []config.PlayOutput, sourceOpts *FFmpegOptions) []PlayOutput {
	if outputs == nil {
		return nil
	}

	res := make([]PlayOutput, len(outputs))
	for i, output := range outputs {
		sourceCopy := copyFFmpegOptions(sourceOpts)

		res[i] = PlayOutput{
			Protocol:           output.Protocol,
			Enabled:            output.Enabled,
			HlsSegmentDuration: output.HlsSegmentDuration,
			HlsSegmentCount:    output.HlsSegmentCount,
			HlsPath:            output.HlsPath,
			HlsEnablePlayback:  output.HlsEnablePlayback,
			HlsRetentionDays:   output.HlsRetentionDays,
			TSFilenameTemplate: output.TSFilenameTemplate,
		}

		switch output.Protocol {
		case "flv":
			res[i].FlvFFmpegOptions = mergeFFmpegOptions(sourceCopy, convertFFmpegOptions(output.FlvFFmpegOptions))
		case "hls":
			res[i].HlsFFmpegOptions = mergeFFmpegOptions(sourceCopy, convertFFmpegOptions(output.HlsFFmpegOptions))
		}
	}
	return res
}

// convertReceivers converts Receivers from config to publisher Receivers, merging FFmpegOptions with source
func convertReceivers(items config.ReceiversData, sourceOpts *FFmpegOptions) Receivers {
	var res Receivers

	if items.Primary != nil {
		res.Primary = &Receiver{
			PushURL:       items.Primary.PushURL,
			PlayUrls:      PlayUrls{Flv: items.Primary.PlayUrls.Flv, Hls: items.Primary.PlayUrls.Hls},
			FFmpegOptions: mergeFFmpegOptions(sourceOpts, convertFFmpegOptions(items.Primary.FFmpegOptions)),
		}
	}

	if items.Backup != nil {
		res.Backup = &Receiver{
			PushURL:       items.Backup.PushURL,
			PlayUrls:      PlayUrls{Flv: items.Backup.PlayUrls.Flv, Hls: items.Backup.PlayUrls.Hls},
			FFmpegOptions: mergeFFmpegOptions(sourceOpts, convertFFmpegOptions(items.Backup.FFmpegOptions)),
		}
	}

	if items.All != nil {
		res.All = make([]Receiver, len(items.All))
		for i, item := range items.All {
			res.All[i] = Receiver{
				PushURL:       item.PushURL,
				PlayUrls:      PlayUrls{Flv: item.PlayUrls.Flv, Hls: item.PlayUrls.Hls},
				FFmpegOptions: mergeFFmpegOptions(sourceOpts, convertFFmpegOptions(item.FFmpegOptions)),
			}
		}
	}

	return res
}

// copyFFmpegOptions creates a deep copy of FFmpegOptions
func copyFFmpegOptions(src *FFmpegOptions) *FFmpegOptions {
	if src == nil {
		return &FFmpegOptions{}
	}

	dest := *src
	if src.Filters != nil {
		filters := *src.Filters
		dest.Filters = &filters
	}

	// slice 类型要新建一份
	if src.GlobalArgs != nil {
		dest.GlobalArgs = append([]string{}, src.GlobalArgs...)
	}
	if src.InputPreArgs != nil {
		dest.InputPreArgs = append([]string{}, src.InputPreArgs...)
	}
	if src.InputPostArgs != nil {
		dest.InputPostArgs = append([]string{}, src.InputPostArgs...)
	}
	if src.OutputPreArgs != nil {
		dest.OutputPreArgs = append([]string{}, src.OutputPreArgs...)
	}
	if src.OutputPostArgs != nil {
		dest.OutputPostArgs = append([]string{}, src.OutputPostArgs...)
	}
	if src.CustomArgs != nil {
		dest.CustomArgs = append([]string{}, src.CustomArgs...)
	}
	if src.Headers != nil {
		dest.Headers = append([]string{}, src.Headers...)
	}

	return &dest
}

// 子级会覆盖前面的全局/上级参数
func mergeFFmpegOptions(opts ...*FFmpegOptions) *FFmpegOptions {
	result := &FFmpegOptions{}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		// slice 类型累加（需要去重处理）
		if opt.GlobalArgs != nil {
			result.GlobalArgs = append(result.GlobalArgs, opt.GlobalArgs...)
			// 去重处理GlobalArgs，特别是-re参数
			result.GlobalArgs = removeDuplicateStrings(result.GlobalArgs)
		}
		if opt.InputPreArgs != nil {
			result.InputPreArgs = append(result.InputPreArgs, opt.InputPreArgs...)
		}
		if opt.InputPostArgs != nil {
			result.InputPostArgs = append(result.InputPostArgs, opt.InputPostArgs...)
		}
		if opt.OutputPreArgs != nil {
			result.OutputPreArgs = append(result.OutputPreArgs, opt.OutputPreArgs...)
		}
		if opt.OutputPostArgs != nil {
			result.OutputPostArgs = append(result.OutputPostArgs, opt.OutputPostArgs...)
		}
		if opt.CustomArgs != nil {
			result.CustomArgs = append(result.CustomArgs, opt.CustomArgs...)
		}
		if opt.Headers != nil {
			result.Headers = append(result.Headers, opt.Headers...)
		}

		// Filters 类型累加
		if opt.Filters != nil {
			if result.Filters == nil {
				result.Filters = &FilterOptions{}
			}
			result.Filters.VideoFilters = append(result.Filters.VideoFilters, opt.Filters.VideoFilters...)
			result.Filters.AudioFilters = append(result.Filters.AudioFilters, opt.Filters.AudioFilters...)
		}

		// scalar 类型覆盖
		if opt.VideoCodec != "" {
			result.VideoCodec = opt.VideoCodec
		}
		if opt.AudioCodec != "" {
			result.AudioCodec = opt.AudioCodec
		}
		if opt.VideoBitrate != "" {
			result.VideoBitrate = opt.VideoBitrate
		}
		if opt.AudioBitrate != "" {
			result.AudioBitrate = opt.AudioBitrate
		}
		if opt.Preset != "" {
			result.Preset = opt.Preset
		}
		if opt.CRF != 0 {
			result.CRF = opt.CRF
		}
		if opt.OutputFormat != "" {
			result.OutputFormat = opt.OutputFormat
		}
		if opt.StreamCopy {
			result.StreamCopy = true
		}
		if opt.UseReFlag {
			result.UseReFlag = true
		}
		if opt.PixFmt != "" {
			result.PixFmt = opt.PixFmt
		}
		if opt.GopSize != 0 {
			result.GopSize = opt.GopSize
		}
		if opt.UserAgent != "" {
			result.UserAgent = opt.UserAgent
		}
	}

	// 对所有参数进行最终的去重处理，特别是对标志类参数
	result.GlobalArgs = RemoveDuplicateFlagArgs(result.GlobalArgs)
	result.InputPreArgs = RemoveDuplicateFlagArgs(result.InputPreArgs)
	result.InputPostArgs = RemoveDuplicateFlagArgs(result.InputPostArgs)
	result.OutputPreArgs = RemoveDuplicateFlagArgs(result.OutputPreArgs)
	result.OutputPostArgs = RemoveDuplicateFlagArgs(result.OutputPostArgs)
	result.CustomArgs = RemoveDuplicateFlagArgs(result.CustomArgs)

	return result
}

// removeDuplicateStrings removes duplicate strings from a slice, keeping the first occurrence
func removeDuplicateStrings(slice []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)

	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

// RemoveDuplicateFlagArgs removes duplicate flag arguments from a slice, keeping the first occurrence
// This is specifically for FFmpeg arguments where flags like "-re" should only appear once
func RemoveDuplicateFlagArgs(slice []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)

	for _, item := range slice {
		// For flag arguments (starting with -), only allow one occurrence
		if len(item) > 0 && item[0] == '-' {
			if !seen[item] {
				seen[item] = true
				result = append(result, item)
			}
		} else {
			// For non-flag arguments, always add them
			result = append(result, item)
		}
	}

	return result
}

// convertFFmpegOptions converts FFmpegOptions from config to publisher FFmpegOptions
func convertFFmpegOptions(src *config.FFmpegOptions) *FFmpegOptions {
	if src == nil {
		return nil
	}

	dest := &FFmpegOptions{
		GlobalArgs:     make([]string, len(src.GlobalArgs)),
		InputPreArgs:   make([]string, len(src.InputPreArgs)),
		InputPostArgs:  make([]string, len(src.InputPostArgs)),
		VideoCodec:     src.VideoCodec,
		AudioCodec:     src.AudioCodec,
		VideoBitrate:   src.VideoBitrate,
		AudioBitrate:   src.AudioBitrate,
		Preset:         src.Preset,
		CRF:            src.CRF,
		OutputFormat:   src.OutputFormat,
		OutputPreArgs:  make([]string, len(src.OutputPreArgs)),
		OutputPostArgs: make([]string, len(src.OutputPostArgs)),
		CustomArgs:     make([]string, len(src.CustomArgs)),
		StreamCopy:     src.StreamCopy,
		UseReFlag:      src.UseReFlag,
		PixFmt:         src.PixFmt,
		GopSize:        src.GopSize,
		UserAgent:      src.UserAgent,
		Headers:        make([]string, len(src.Headers)),
	}

	// Copy slice values
	copy(dest.GlobalArgs, src.GlobalArgs)
	copy(dest.InputPreArgs, src.InputPreArgs)
	copy(dest.InputPostArgs, src.InputPostArgs)
	copy(dest.OutputPreArgs, src.OutputPreArgs)
	copy(dest.OutputPostArgs, src.OutputPostArgs)
	copy(dest.CustomArgs, src.CustomArgs)
	copy(dest.Headers, src.Headers)

	if src.Filters != nil {
		dest.Filters = &FilterOptions{
			VideoFilters: make([]string, len(src.Filters.VideoFilters)),
			AudioFilters: make([]string, len(src.Filters.AudioFilters)),
		}
		copy(dest.Filters.VideoFilters, src.Filters.VideoFilters)
		copy(dest.Filters.AudioFilters, src.Filters.AudioFilters)
	}

	return dest
}

func FindFFmpeg() (string, error) {
	exeName := "ffmpeg"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}

	// 获取当前工作目录
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// 获取当前可执行文件所在的目录
	executablePath, err := os.Executable()
	if err != nil {
		logger.LogPrintf("无法获取当前可执行文件的路径: %v", err)
		return "", err
	}
	executableDir := filepath.Dir(executablePath)

	// ✅ 优先检查这些相对路径（项目目录优先）
	pathsToCheck := []string{
		filepath.Join("ffmpeg", "bin", exeName),
		filepath.Join("bin", exeName),
		exeName, // 当前目录直接有 ffmpeg
	}

	for _, relativePath := range pathsToCheck {
		// 当前工作目录优先
		cwdPath := filepath.Join(cwd, relativePath)
		if _, err := os.Stat(cwdPath); err == nil {
			_ = updatePathEnv(filepath.Dir(cwdPath))
			_ = os.Setenv("FFMPEG_PATH", cwdPath)
			logger.LogPrintf("优先使用本地 ffmpeg: %s", cwdPath)
			return cwdPath, nil
		}

		// 再检查可执行文件目录
		execPath := filepath.Join(executableDir, relativePath)
		if _, err := os.Stat(execPath); err == nil {
			_ = updatePathEnv(filepath.Dir(execPath))
			_ = os.Setenv("FFMPEG_PATH", execPath)
			logger.LogPrintf("优先使用可执行目录下的 ffmpeg: %s", execPath)
			return execPath, nil
		}
	}

	// ✅ 如果本地没有，再查系统 PATH
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		fullPath := filepath.Join(dir, exeName)
		if _, err := os.Stat(fullPath); err == nil {
			_ = os.Setenv("FFMPEG_PATH", fullPath)
			logger.LogPrintf("使用系统 PATH 中的 ffmpeg: %s", fullPath)
			return fullPath, nil
		}
	}

	// ❌ 都没找到
	logger.LogPrintf("未在本地或系统 PATH 中找到 ffmpeg")
	return "", fmt.Errorf("ffmpeg 未找到，请确认 ffmpeg 存在于 ./ffmpeg/bin 或系统 PATH 中")
}

// 辅助函数：追加 ffmpeg 目录到 PATH
func updatePathEnv(dir string) error {
	if dir == "" {
		return nil
	}
	oldPath := os.Getenv("PATH")
	if !strings.Contains(oldPath, dir) {
		newPath := fmt.Sprintf("%s%c%s", dir, os.PathListSeparator, oldPath)
		return os.Setenv("PATH", newPath)
	}
	return nil
}
