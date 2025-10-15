package publisher

import (
	// "log"
	"fmt"
	"net/http"
	"sync"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
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

// 并自动合并全局 FFmpegOptions
func convertConfig(cfg *config.PublisherConfig) *Config {
	if cfg == nil {
		return nil
	}

	// 创建 publisher 配置，保证全局 FFmpegOptions 永不为 nil
	publisherCfg := &Config{
		Path:    cfg.Path,
		Streams: make(map[string]*Stream),
	}

	publisherCfg.FFmpegOptions = convertFFmpegOptions(cfg.FFmpegOptions)
	if publisherCfg.FFmpegOptions == nil {
		publisherCfg.FFmpegOptions = &FFmpegOptions{}
	}

	logger.LogPrintf("Converting config with %d streams", len(cfg.Streams))
	for name, streamItem := range cfg.Streams {
		logger.LogPrintf("Processing stream: %s", name)
		logger.LogPrintf("Stream protocol: %s, enabled: %t", streamItem.Protocol, streamItem.Enabled)

		// 生成 streamKey
		streamKey := streamItem.StreamKey.Value
		if streamKey == "" && streamItem.StreamKey.Type == "random" {
			streamKey = "test_stream_key"
		}

		// 转换 LocalPlayUrls
		localPlayUrls := make([]PlayOutput, len(streamItem.Stream.LocalPlayUrls))
		for i, output := range streamItem.Stream.LocalPlayUrls {
			localPlayUrls[i] = PlayOutput{
				Protocol:      output.Protocol,
				Enabled:       output.Enabled,
				FFmpegOptions: convertFFmpegOptions(output.FFmpegOptions),
			}
		}

		// 转换 Stream
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
				Source: Source{
					Type:          streamItem.Stream.Source.Type,
					URL:           streamItem.Stream.Source.URL,
					BackupURL:     streamItem.Stream.Source.BackupURL,
					FFmpegOptions: convertFFmpegOptions(streamItem.Stream.Source.FFmpegOptions),
				},
				LocalPlayUrls: localPlayUrls,
				Mode:          streamItem.Stream.Mode,
				Receivers: Receivers{
					Primary: convertReceiver(streamItem.Stream.Receivers.Primary),
					Backup:  convertReceiver(streamItem.Stream.Receivers.Backup),
					All:     convertReceivers(streamItem.Stream.Receivers.All),
				},
			},
			FFmpegOptions: convertFFmpegOptions(streamItem.FFmpegOptions),
			ParentConfig:  publisherCfg,
		}

		// ------------------------------
		// 合并 FFmpegOptions（全局覆盖 + 子级覆盖）
		// ------------------------------
		// 先合并 Stream 自身和 Source，得到最终有效的 Stream 参数
		effectiveStreamOpts := mergeFFmpegOptions(
			publisherCfg.FFmpegOptions,
			stream.FFmpegOptions,
			stream.Stream.Source.FFmpegOptions,
		)
		stream.FFmpegOptions = effectiveStreamOpts

		// LocalPlayUrls FFmpegOptions = effectiveStreamOpts -> LocalPlayUrl 子级
		for i := range stream.Stream.LocalPlayUrls {
			stream.Stream.LocalPlayUrls[i].FFmpegOptions = mergeFFmpegOptions(
				effectiveStreamOpts,
				stream.Stream.LocalPlayUrls[i].FFmpegOptions,
			)
		}

		// Receivers FFmpegOptions = effectiveStreamOpts -> Receiver 子级
		if stream.Stream.Receivers.Primary != nil {
			stream.Stream.Receivers.Primary.FFmpegOptions = mergeFFmpegOptions(
				effectiveStreamOpts,
				stream.Stream.Receivers.Primary.FFmpegOptions,
			)
		}
		if stream.Stream.Receivers.Backup != nil {
			stream.Stream.Receivers.Backup.FFmpegOptions = mergeFFmpegOptions(
				effectiveStreamOpts,
				stream.Stream.Receivers.Backup.FFmpegOptions,
			)
		}
		for i := range stream.Stream.Receivers.All {
			stream.Stream.Receivers.All[i].FFmpegOptions = mergeFFmpegOptions(
				effectiveStreamOpts,
				stream.Stream.Receivers.All[i].FFmpegOptions,
			)
		}

		// 添加到 publisherCfg
		publisherCfg.Streams[name] = stream
		logger.LogPrintf("Added stream %s to publisher config", name)

		// 打印最终有效参数
		fmt.Printf("Stream %s effective FFmpegOptions: %+v\n", name, effectiveStreamOpts)
		if stream.Stream.Receivers.Primary != nil {
			fmt.Printf("Primary receiver effective FFmpegOptions: %+v\n", stream.Stream.Receivers.Primary.FFmpegOptions)
		}
	}

	logger.LogPrintf("Converted config has %d streams", len(publisherCfg.Streams))
	return publisherCfg
}


// 子级会覆盖前面的全局/上级参数
func mergeFFmpegOptions(opts ...*FFmpegOptions) *FFmpegOptions {
	result := &FFmpegOptions{}

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		// slice 类型累加
		if opt.GlobalArgs != nil {
			result.GlobalArgs = append(result.GlobalArgs, opt.GlobalArgs...)
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

	return result
}



// convertReceiver converts a receiver item to a new Receiver struct
func convertReceiver(item *config.ReceiverItem) *Receiver {
	if item == nil {
		return nil
	}

	return &Receiver{
		PushURL: item.PushURL,
		PlayUrls: PlayUrls{
			Flv: item.PlayUrls.Flv,
			Hls: item.PlayUrls.Hls,
		},
		PushPreArgs:   item.PushPreArgs,
		PushPostArgs:  item.PushPostArgs,
		FFmpegOptions: convertFFmpegOptions(item.FFmpegOptions),
	}
}

// convertReceivers converts a slice of ReceiverItem to a slice of Receiver
func convertReceivers(items []config.ReceiverItem) []Receiver {
	if items == nil {
		return nil
	}

	receivers := make([]Receiver, len(items))
	for i, item := range items {
		receivers[i] = Receiver{
			PushURL: item.PushURL,
			PlayUrls: PlayUrls{
				Flv: item.PlayUrls.Flv,
				Hls: item.PlayUrls.Hls,
			},
			PushPreArgs:   item.PushPreArgs,
			PushPostArgs:  item.PushPostArgs,
			FFmpegOptions: convertFFmpegOptions(item.FFmpegOptions),
		}
	}
	return receivers
}

// convertFFmpegOptions converts FFmpegOptions from config to publisher FFmpegOptions
func convertFFmpegOptions(src *config.FFmpegOptions) *FFmpegOptions {
	if src == nil {
		return nil
	}

	dest := &FFmpegOptions{
		GlobalArgs:     src.GlobalArgs,
		InputPreArgs:   src.InputPreArgs,
		InputPostArgs:  src.InputPostArgs,
		VideoCodec:     src.VideoCodec,
		AudioCodec:     src.AudioCodec,
		VideoBitrate:   src.VideoBitrate,
		AudioBitrate:   src.AudioBitrate,
		Preset:         src.Preset,
		CRF:            src.CRF,
		OutputFormat:   src.OutputFormat,
		OutputPreArgs:  src.OutputPreArgs,
		OutputPostArgs: src.OutputPostArgs,
		CustomArgs:     src.CustomArgs,
		StreamCopy:     src.StreamCopy,
		UseReFlag:      src.UseReFlag,
		PixFmt:         src.PixFmt,
		GopSize:        src.GopSize,
		UserAgent:      src.UserAgent,
		Headers:        src.Headers,
	}

	if src.Filters != nil {
		dest.Filters = &FilterOptions{
			VideoFilters: src.Filters.VideoFilters,
			AudioFilters: src.Filters.AudioFilters,
		}
	}

	return dest
}
