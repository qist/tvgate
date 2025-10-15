package publisher

import (
	// "log"
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

// convertConfig converts the config types to publisher types
func convertConfig(cfg *config.PublisherConfig) *Config {
	if cfg == nil {
		return nil
	}

	// 创建 publisher 配置
	publisherCfg := &Config{
		Path:    cfg.Path,
		Streams: make(map[string]*Stream),
	}

	logger.LogPrintf("Converting config with %d streams", len(cfg.Streams))
	for name, streamItem := range cfg.Streams {
		logger.LogPrintf("Processing stream: %s", name)
		logger.LogPrintf("Stream protocol: %s, enabled: %t", streamItem.Protocol, streamItem.Enabled)

		// 生成 streamKey
		streamKey := streamItem.StreamKey.Value
		if streamKey == "" && streamItem.StreamKey.Type == "random" {
			streamKey = "test_stream_key" // 简单随机 key
		}

		// 转换 LocalPlayUrls
		var localPlayUrls []PlayOutput
		for _, output := range streamItem.Stream.LocalPlayUrls {
			localPlayUrls = append(localPlayUrls, PlayOutput{
				Protocol:      output.Protocol,
				Enabled:       output.Enabled,
				FFmpegOptions: convertFFmpegOptions(output.FFmpegOptions),
			})
		}

		stream := &Stream{
			BufferSize: streamItem.BufferSize,
			Protocol:   streamItem.Protocol,
			Enabled:    streamItem.Enabled,
			StreamKey: StreamKey{
				Type:       streamItem.StreamKey.Type,
				Value:      streamItem.StreamKey.Value,
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
		}

		publisherCfg.Streams[name] = stream
		logger.LogPrintf("Added stream %s to publisher config", name)
	}

	logger.LogPrintf("Converted config has %d streams", len(publisherCfg.Streams))
	return publisherCfg
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
