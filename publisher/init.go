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

	// Create the publisher config
	publisherCfg := &Config{
		Path:    cfg.Path,
		Streams: make(map[string]*Stream),
	}

	// Convert streams - 处理扁平结构
	logger.LogPrintf("Converting config with %d streams", len(cfg.Streams))
	for name, streamItem := range cfg.Streams {
		logger.LogPrintf("Processing stream: %s", name)
		logger.LogPrintf("Stream protocol: %s, enabled: %t", streamItem.Protocol, streamItem.Enabled)
		
		// 生成streamKey
		streamKey := streamItem.StreamKey.Value
		if streamKey == "" && streamItem.StreamKey.Type == "random" {
			// 简单生成一个随机key用于测试
			streamKey = "test_stream_key"
		}
		
		stream := &Stream{
			BufferSize: streamItem.BufferSize,
			Protocol:   streamItem.Protocol,
			Enabled:    streamItem.Enabled,
			StreamKey:  StreamKey{ // 使用配置中的streamkey
				Type:       streamItem.StreamKey.Type,
				Value:      streamItem.StreamKey.Value,
				Length:     streamItem.StreamKey.Length,
				Expiration: streamItem.StreamKey.Expiration, // 添加Expiration字段
			},
			Stream: StreamConfig{
				Source: Source{
					Type:      streamItem.Stream.Source.Type,
					URL:       streamItem.Stream.Source.URL,
					BackupURL: streamItem.Stream.Source.BackupURL,
					Headers:   streamItem.Stream.Source.Headers,
				},
				LocalPlayUrls: PlayUrls{
					Flv: streamItem.Stream.LocalPlayUrls.Flv,
					Hls: streamItem.Stream.LocalPlayUrls.Hls,
				},
				Mode: streamItem.Stream.Mode,
				Receivers: Receivers{
					Primary: convertReceiver(streamItem.Stream.Receivers.Primary),
					Backup:  convertReceiver(streamItem.Stream.Receivers.Backup),
					All:     convertReceivers(streamItem.Stream.Receivers.All),
				},
			},
		}

		// Convert FFmpeg options
		if streamItem.FFmpegOptions != nil {
			stream.FFmpegOptions = &FFmpegOptions{
				GlobalArgs:     streamItem.FFmpegOptions.GlobalArgs,
				InputPreArgs:   streamItem.FFmpegOptions.InputPreArgs,
				InputPostArgs:  streamItem.FFmpegOptions.InputPostArgs,
				VideoCodec:     streamItem.FFmpegOptions.VideoCodec,
				AudioCodec:     streamItem.FFmpegOptions.AudioCodec,
				VideoBitrate:   streamItem.FFmpegOptions.VideoBitrate,
				AudioBitrate:   streamItem.FFmpegOptions.AudioBitrate,
				Preset:         streamItem.FFmpegOptions.Preset,
				CRF:            streamItem.FFmpegOptions.CRF,
				OutputFormat:   streamItem.FFmpegOptions.OutputFormat,
				OutputPreArgs:  streamItem.FFmpegOptions.OutputPreArgs,
				OutputPostArgs: streamItem.FFmpegOptions.OutputPostArgs,
				CustomArgs:     streamItem.FFmpegOptions.CustomArgs,
			}

			// Convert filter options
			if streamItem.FFmpegOptions.Filters != nil {
				stream.FFmpegOptions.Filters = &FilterOptions{
					VideoFilters: streamItem.FFmpegOptions.Filters.VideoFilters,
					AudioFilters: streamItem.FFmpegOptions.Filters.AudioFilters,
				}
			}
		}

		publisherCfg.Streams[name] = stream
		logger.LogPrintf("Added stream %s to publisher config", name)
	}

	logger.LogPrintf("Converted config has %d streams", len(publisherCfg.Streams))
	return publisherCfg
}

// convertReceiver converts a receiver item to a receiver
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
		PushPreArgs:  item.PushPreArgs,
		PushPostArgs: item.PushPostArgs,
	}
}

// convertReceivers converts receiver items to receivers
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
			PushPreArgs:  item.PushPreArgs,
			PushPostArgs: item.PushPostArgs,
		}
	}
	return receivers
}