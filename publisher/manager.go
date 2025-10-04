package publisher

import (
	"context"
	"io/ioutil"
	"log"
	"strings"
	"sync"
	"time"
	"gopkg.in/yaml.v3"
	"github.com/qist/tvgate/config"
)

// Manager manages all publisher streams
type Manager struct {
	config *Config
	streams map[string]*StreamManager
	mutex   sync.RWMutex
	// 添加定时器相关字段
	expirationChecker *time.Ticker
	done              chan struct{}
}

// StreamManager manages a single stream
type StreamManager struct {
	name         string
	stream       *Stream
	streamKey    string
	oldStreamKey string // 从URL中提取的旧密钥
	createdAt    time.Time
	cancel       context.CancelFunc
	ctx          context.Context
	mutex        sync.RWMutex
	running      bool
}

// NewManager creates a new publisher manager
func NewManager(config *Config) *Manager {
	log.Printf("Creating new publisher manager with %d streams", len(config.Streams))
	return &Manager{
		config:  config,
		streams: make(map[string]*StreamManager),
		done:    make(chan struct{}), // 初始化done通道
	}
}

// Start starts all enabled streams
func (m *Manager) Start() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	log.Printf("Starting publisher manager")

	// 启动过期检查器
	m.startExpirationChecker()

	if m.config.Streams == nil {
		log.Println("No streams configured")
		return nil
	}

	log.Printf("Found %d streams in config", len(m.config.Streams))

	for name, stream := range m.config.Streams {
		log.Printf("Processing stream: %s, enabled: %t", name, stream.Enabled)
		if !stream.Enabled {
			log.Printf("Stream %s is disabled, skipping", name)
			continue
		}

		// Create context for this stream
		ctx, cancel := context.WithCancel(context.Background())
		
		// 以配置文件中的streamkey.value为准，只在密钥过期或为空时才生成新密钥
		var streamKey string
		var createdAt time.Time
		needUpdateConfig := false
		
		if stream.StreamKey.Value != "" {
			// 如果配置中有stream key值，使用配置文件中的值和时间
			streamKey = stream.StreamKey.Value
			createdAt = stream.StreamKey.CreatedAt
			
			// 检查创建时间是否有效
			if createdAt.IsZero() {
				// 如果创建时间为零值，使用当前时间作为创建时间
				createdAt = time.Now()
				log.Printf("Stream key creation time is zero, using current time: %v", createdAt)
			}
			
			// 检查密钥是否过期
			if stream.CheckStreamKeyExpiration(streamKey, createdAt) {
				// 已过期，生成新的stream key
				log.Printf("Existing stream key for %s expired, generating new key", name)
				var err error
				streamKey, err = stream.GenerateStreamKey()
				if err != nil {
					log.Printf("Failed to generate stream key for %s: %v", name, err)
					cancel()
					continue
				}
				createdAt = time.Now()
				needUpdateConfig = true
			} else {
				log.Printf("Using existing stream key for %s: %s", name, streamKey)
			}
		} else {
			// 没有stream key，生成新的
			log.Printf("No existing stream key for %s, generating new key", name)
			var err error
			streamKey, err = stream.GenerateStreamKey()
			if err != nil {
				log.Printf("Failed to generate stream key for %s: %v", name, err)
				cancel()
				continue
			}
			createdAt = time.Now()
			needUpdateConfig = true
		}
		
		// 从URL中提取旧密钥，用于替换
		oldStreamKey := ""
		if stream.Stream.Receivers.All != nil && len(stream.Stream.Receivers.All) > 0 {
			// 从第一个receiver的push_url中提取旧密钥
			if stream.Stream.Receivers.All[0].PushURL != "" {
				oldStreamKey = m.extractStreamKeyFromURL(stream.Stream.Receivers.All[0].PushURL)
				log.Printf("Extracted old stream key from push_url for %s: %s", name, oldStreamKey)
			}
		} else if stream.Stream.Receivers.Primary != nil {
			// 从primary receiver的push_url中提取旧密钥
			if stream.Stream.Receivers.Primary.PushURL != "" {
				oldStreamKey = m.extractStreamKeyFromURL(stream.Stream.Receivers.Primary.PushURL)
				log.Printf("Extracted old stream key from primary push_url for %s: %s", name, oldStreamKey)
			}
		}
		
		streamManager := &StreamManager{
			name:         name,
			stream:       stream,
			streamKey:    streamKey,
			oldStreamKey: oldStreamKey,
			createdAt:    createdAt,
			cancel:       cancel,
			ctx:          ctx,
			running:      true,
		}

		m.streams[name] = streamManager
		
		// 只有在生成了新密钥时才更新配置文件
		if needUpdateConfig {
			if err := streamManager.updateConfigFile(streamKey); err != nil {
				log.Printf("Failed to update config file for %s: %v", name, err)
			} else {
				log.Printf("Successfully updated config file for %s with stream key", name)
			}
		} else {
			log.Printf("Using existing valid stream key for %s, not updating config file", name)
		}
		
		go streamManager.startStreaming()
	}

	log.Printf("Publisher manager started with %d active streams", len(m.streams))
	return nil
}

// startExpirationChecker 启动过期检查器
func (m *Manager) startExpirationChecker() {
	// 每分钟检查一次密钥是否过期
	m.expirationChecker = time.NewTicker(1 * time.Minute)
	go func() {
		for {
			select {
			case <-m.expirationChecker.C:
				m.checkStreamKeyExpiration()
			case <-m.done:
				return
			}
		}
	}()
}

// checkStreamKeyExpiration 检查所有流的密钥是否过期
func (m *Manager) checkStreamKeyExpiration() {
	m.mutex.RLock()
	streams := make([]*StreamManager, 0, len(m.streams))
	for _, stream := range m.streams {
		streams = append(streams, stream)
	}
	m.mutex.RUnlock()
	
	// 检查每个流的密钥是否过期
	for _, stream := range streams {
		stream.mutex.RLock()
		streamKey := stream.streamKey
		createdAt := stream.createdAt
		stream.mutex.RUnlock()
		
		log.Printf("Checking if stream key expired for %s (created at: %v)", stream.name, createdAt)
		if stream.stream.CheckStreamKeyExpiration(streamKey, createdAt) {
			log.Printf("Stream key for %s expired, generating new key", stream.name)
			// 更新stream key
			newStreamKey, err := stream.stream.UpdateStreamKey()
			if err != nil {
				log.Printf("Failed to generate new stream key for %s: %v", stream.name, err)
			} else {
				stream.mutex.Lock()
				oldKey := stream.streamKey
				stream.oldStreamKey = oldKey // 保存旧密钥
				stream.streamKey = newStreamKey
				stream.createdAt = time.Now()
				stream.mutex.Unlock()
				log.Printf("Generated new stream key for %s: %s (was: %s)", stream.name, newStreamKey, oldKey)
				
				// 回写配置到YAML文件
				log.Printf("Updating config file for %s with new stream key", stream.name)
				if err := stream.updateConfigFile(newStreamKey); err != nil {
					log.Printf("Failed to update config file for %s: %v", stream.name, err)
				} else {
					log.Printf("Successfully updated config file for %s", stream.name)
				}
				
				// 重新构建命令并重启推流
				stream.restartStreaming()
			}
		}
	}
}

// Stop stops the publisher manager and all streams
func (m *Manager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	log.Printf("Stopping publisher manager with %d streams", len(m.streams))
	
	// 停止过期检查器
	if m.expirationChecker != nil {
		m.expirationChecker.Stop()
	}
	close(m.done)
	
	for _, streamManager := range m.streams {
		streamManager.Stop()
	}
	log.Println("Publisher manager stopped")
}

// StopAll stops all streams
func (m *Manager) StopAll() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	log.Printf("Stopping all streams")
	
	for name, streamManager := range m.streams {
		log.Printf("Stopping stream: %s", name)
		streamManager.Stop()
	}
	
	log.Printf("All streams stopped")
}

// startStreaming starts the streaming process for a single stream
func (sm *StreamManager) startStreaming() {
	log.Printf("Starting stream %s with key %s", sm.name, sm.streamKey)
	
	// Build FFmpeg command
	ffmpegCmd := sm.stream.BuildFFmpegCommand()
	
	// Get receivers
	receivers := sm.stream.Stream.GetReceivers()
	log.Printf("Stream %s has %d receivers", sm.name, len(receivers))
	
	// Start streaming for each receiver
	var wg sync.WaitGroup
	for i, receiver := range receivers {
		wg.Add(1)
		go func(index int, r Receiver) {
			defer wg.Done()
			cmd := r.BuildFFmpegPushCommand(ffmpegCmd, sm.streamKey)
			log.Printf("Full FFmpeg command for stream %s receiver %d: ffmpeg %s", sm.name, index+1, strings.Join(cmd, " "))
			sm.runFFmpegStream(cmd, index+1)
		}(i, receiver)
	}
	
	wg.Wait()
	log.Printf("Stream %s finished", sm.name)
}

// runFFmpegStream runs the ffmpeg stream with restart capability
func (sm *StreamManager) runFFmpegStream(cmd []string, receiverIndex int) {
	log.Printf("Starting FFmpeg stream routine for %s receiver %d", sm.name, receiverIndex)
	
	for {
		// 检查stream manager是否仍在运行
		if !sm.isRunning() {
			log.Printf("Stream %s receiver %d stopped", sm.name, receiverIndex)
			return
		}
		
		select {
		case <-sm.ctx.Done():
			log.Printf("Stream %s receiver %d context cancelled", sm.name, receiverIndex)
			return
		default:
			log.Printf("Starting FFmpeg stream for %s receiver %d", sm.name, receiverIndex)
			
			// Execute FFmpeg
			err := sm.stream.ExecuteFFmpeg(sm.ctx, cmd)
			if err != nil {
				log.Printf("FFmpeg stream for %s receiver %d failed: %v", sm.name, receiverIndex, err)
				
				// 检查是否应该继续
				if !sm.isRunning() {
					log.Printf("Stream %s receiver %d stopped after error", sm.name, receiverIndex)
					return
				}
				
				// 检查stream key是否过期
				sm.mutex.RLock()
				streamKey := sm.streamKey
				createdAt := sm.createdAt
				sm.mutex.RUnlock()
				
				log.Printf("Checking if stream key expired for %s (created at: %v)", sm.name, createdAt)
				if sm.stream.CheckStreamKeyExpiration(streamKey, createdAt) {
					log.Printf("Stream key for %s expired, generating new key", sm.name)
					// 更新stream key
					newStreamKey, err := sm.stream.UpdateStreamKey()
					if err != nil {
						log.Printf("Failed to generate new stream key for %s: %v", sm.name, err)
					} else {
						sm.mutex.Lock()
						oldKey := sm.streamKey
						sm.oldStreamKey = oldKey // 保存旧密钥
						sm.streamKey = newStreamKey
						sm.createdAt = time.Now()
						sm.mutex.Unlock()
						log.Printf("Generated new stream key for %s: %s (was: %s)", sm.name, newStreamKey, oldKey)
						
						// 回写配置到YAML文件
						log.Printf("Updating config file for %s with new stream key", sm.name)
						if err := sm.updateConfigFile(newStreamKey); err != nil {
							log.Printf("Failed to update config file for %s: %v", sm.name, err)
						} else {
							log.Printf("Successfully updated config file for %s", sm.name)
						}
						
						// 重新构建命令
						ffmpegCmd := sm.stream.BuildFFmpegCommand()
						receivers := sm.stream.Stream.GetReceivers()
						if receiverIndex <= len(receivers) {
							cmd = receivers[receiverIndex-1].BuildFFmpegPushCommand(ffmpegCmd, newStreamKey)
							log.Printf("Rebuilt FFmpeg command with new stream key for %s receiver %d", sm.name, receiverIndex)
							log.Printf("New command: ffmpeg %s", strings.Join(cmd, " "))
						} else {
							log.Printf("Receiver index %d out of range, cannot rebuild command", receiverIndex)
						}
						
						// 继续循环使用新密钥进行推流
						continue
					}
				} else {
					log.Printf("Stream key for %s not expired, will retry in 5 seconds", sm.name)
				}
				
				// 等待后重启
				select {
				case <-sm.ctx.Done():
					log.Printf("Stream %s receiver %d stopped during wait", sm.name, receiverIndex)
					return
				case <-time.After(5 * time.Second):
					// 继续循环重启
					log.Printf("Retrying stream %s receiver %d after 5 second delay", sm.name, receiverIndex)
				}
			} else {
				log.Printf("FFmpeg stream for %s receiver %d finished normally", sm.name, receiverIndex)
				return
			}
		}
	}
}

// restartStreaming 重启推流
func (sm *StreamManager) restartStreaming() {
	log.Printf("Restarting streaming for %s", sm.name)
	
	// 停止当前推流
	sm.Stop()
	
	// 等待一小段时间确保推流已停止
	time.Sleep(100 * time.Millisecond)
	
	// 重新创建context
	sm.mutex.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	sm.cancel = cancel
	sm.ctx = ctx
	sm.running = true
	sm.mutex.Unlock()
	
	// 启动新的推流
	go sm.startStreaming()
	
	log.Printf("Restarted streaming for %s", sm.name)
}

// isRunning checks if the stream manager is still running
func (sm *StreamManager) isRunning() bool {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.running
}

// Stop stops the stream manager
func (sm *StreamManager) Stop() {
	log.Printf("Stopping stream manager for stream: %s", sm.name)
	
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	if sm.running {
		sm.running = false
		sm.cancel()
	}
}

// GetStreamKey returns the current stream key
func (sm *StreamManager) GetStreamKey() string {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.streamKey
}

// UpdateStreamKey generates and updates the stream key
func (sm *StreamManager) UpdateStreamKey() (string, error) {
	newStreamKey, err := sm.stream.UpdateStreamKey()
	if err != nil {
		return "", err
	}
	
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	sm.streamKey = newStreamKey
	sm.createdAt = time.Now()
	return newStreamKey, nil
}

// updateConfigFile updates the stream key in the config file
func (sm *StreamManager) updateConfigFile(newStreamKey string) error {
	log.Printf("Attempting to update config file for stream %s with new key %s", sm.name, newStreamKey)
	
	// 读取当前配置文件
	configData, err := ioutil.ReadFile(*config.ConfigFilePath)
	if err != nil {
		log.Printf("Failed to read config file: %v", err)
		return err
	}

	// 解析YAML
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(configData, &cfg); err != nil {
		log.Printf("Failed to unmarshal config file: %v", err)
		return err
	}

	// 更新publisher配置中的stream key
	if publisher, ok := cfg["publisher"].(map[string]interface{}); ok {
		log.Printf("Found publisher config")
		if stream, ok := publisher[sm.name].(map[string]interface{}); ok {
			log.Printf("Found stream config for %s", sm.name)
			
			// 更新streamkey的value字段
			if streamKey, ok := stream["streamkey"].(map[string]interface{}); ok {
				oldValue := streamKey["value"]
				streamKey["value"] = newStreamKey
				// 仅更新生成时间，不修改expiration参数
				streamKey["generated"] = time.Now().Format(time.RFC3339)
				// 更新创建时间
				streamKey["created_at"] = sm.createdAt.Format(time.RFC3339)
				log.Printf("Updated streamkey for %s: old_value=%v, new_value=%s", sm.name, oldValue, newStreamKey)
			} else {
				log.Printf("Failed to find streamkey config for %s", sm.name)
			}
			
			// 更新stream中的source headers
			if streamData, ok := stream["stream"].(map[string]interface{}); ok {
				log.Printf("Found stream data for %s", sm.name)
				
				// 更新local_play_urls
				if localPlayURLs, ok := streamData["local_play_urls"].(map[string]interface{}); ok {
					for format, url := range localPlayURLs {
						if urlStr, ok := url.(string); ok {
							// 替换旧的密钥为新的密钥
							urlStr = sm.replaceStreamKeyInURL(urlStr, newStreamKey)
							localPlayURLs[format] = urlStr
							log.Printf("Updated local_play_urls.%s to: %s", format, urlStr)
						}
					}
				}
				
				// 更新stream中的receivers的push_url
				if receivers, ok := streamData["receivers"].(map[string]interface{}); ok {
					log.Printf("Found receivers config")
					// 处理primary-backup模式
					if primary, ok := receivers["primary"].(map[string]interface{}); ok {
						if pushURL, ok := primary["push_url"].(string); ok {
							// 替换旧的密钥为新的密钥
							pushURL = sm.replaceStreamKeyInURL(pushURL, newStreamKey)
							primary["push_url"] = pushURL
							log.Printf("Updated primary push_url to: %s", pushURL)
						}
						
						// 更新primary play_urls
						if playURLs, ok := primary["play_urls"].(map[string]interface{}); ok {
							for format, url := range playURLs {
								if urlStr, ok := url.(string); ok {
									// 替换旧的密钥为新的密钥
									urlStr = sm.replaceStreamKeyInURL(urlStr, newStreamKey)
									playURLs[format] = urlStr
									log.Printf("Updated primary play_urls.%s to: %s", format, urlStr)
								}
							}
						}
					}
					
					if backup, ok := receivers["backup"].(map[string]interface{}); ok {
						if pushURL, ok := backup["push_url"].(string); ok {
							// 替换旧的密钥为新的密钥
							pushURL = sm.replaceStreamKeyInURL(pushURL, newStreamKey)
							backup["push_url"] = pushURL
							log.Printf("Updated backup push_url to: %s", pushURL)
						}
						
						// 更新backup play_urls
						if playURLs, ok := backup["play_urls"].(map[string]interface{}); ok {
							for format, url := range playURLs {
								if urlStr, ok := url.(string); ok {
									// 替换旧的密钥为新的密钥
									urlStr = sm.replaceStreamKeyInURL(urlStr, newStreamKey)
									playURLs[format] = urlStr
									log.Printf("Updated backup play_urls.%s to: %s", format, urlStr)
								}
							}
						}
					}
				} else if receivers, ok := streamData["receivers"].([]interface{}); ok {
					// 处理all模式
					log.Printf("Found all receivers config with %d receivers", len(receivers))
					for i, receiver := range receivers {
						if rec, ok := receiver.(map[string]interface{}); ok {
							if pushURL, ok := rec["push_url"].(string); ok {
								// 替换旧的密钥为新的密钥
								pushURL = sm.replaceStreamKeyInURL(pushURL, newStreamKey)
								rec["push_url"] = pushURL
								log.Printf("Updated receiver %d push_url to: %s", i, pushURL)
							}
							
							// 更新receiver play_urls
							if playURLs, ok := rec["play_urls"].(map[string]interface{}); ok {
								for format, url := range playURLs {
									if urlStr, ok := url.(string); ok {
										// 替换旧的密钥为新的密钥
										urlStr = sm.replaceStreamKeyInURL(urlStr, newStreamKey)
										playURLs[format] = urlStr
										log.Printf("Updated receiver %d play_urls.%s to: %s", i, format, urlStr)
									}
								}
							}
						}
					}
				} else {
					log.Printf("No receivers found in stream data")
				}
			} else {
				log.Printf("Failed to find stream data for %s", sm.name)
			}
		} else {
			log.Printf("Stream %s not found in publisher config", sm.name)
		}
	} else {
		log.Printf("Publisher config not found")
	}

	// 重新序列化为YAML
	log.Printf("Serializing updated config")
	newConfigData, err := yaml.Marshal(cfg)
	if err != nil {
		log.Printf("Failed to marshal updated config: %v", err)
		return err
	}

	// 写入文件
	log.Printf("Writing updated config to file: %s", *config.ConfigFilePath)
	if err := ioutil.WriteFile(*config.ConfigFilePath, newConfigData, 0644); err != nil {
		log.Printf("Failed to write config file: %v", err)
		return err
	}

	log.Printf("Successfully updated stream key in config file for stream %s", sm.name)
	return nil
}

// replaceStreamKeyInURL replaces the old stream key with the new one in a URL
func (sm *StreamManager) replaceStreamKeyInURL(urlStr, newKey string) string {
	// 获取旧密钥，优先使用配置文件中的值
	oldKey := sm.stream.StreamKey.Value
	if oldKey == "" {
		oldKey = sm.oldStreamKey
	}
	
	// 如果URL以.flv结尾，处理.flv扩展名
	if strings.HasSuffix(urlStr, ".flv") {
		// 检查是否存在旧密钥
		if oldKey != "" && strings.Contains(urlStr, "/"+oldKey+".flv") {
			// 替换旧密钥为新密钥
			urlStr = strings.Replace(urlStr, "/"+oldKey+".flv", "/"+newKey+".flv", 1)
		} else if !strings.HasSuffix(urlStr, "/"+newKey+".flv") {
			// 如果没有旧密钥，直接添加新密钥
			urlStr = strings.TrimSuffix(urlStr, ".flv")
			if !strings.HasSuffix(urlStr, "/") {
				urlStr += "/"
			}
			urlStr += newKey + ".flv"
		}
		return urlStr
	}
	
	// 如果URL以.m3u8结尾，处理.m3u8扩展名
	if strings.HasSuffix(urlStr, "index.m3u8") {
		// 检查是否存在旧密钥
		if oldKey != "" && strings.Contains(urlStr, "/"+oldKey+"/index.m3u8") {
			// 替换旧密钥为新密钥
			urlStr = strings.Replace(urlStr, "/"+oldKey+"/index.m3u8", "/"+newKey+"/index.m3u8", 1)
		} else if !strings.HasSuffix(urlStr, "/"+newKey+"/index.m3u8") {
			// 如果没有旧密钥，直接添加新密钥
			urlStr = strings.TrimSuffix(urlStr, "/index.m3u8")
			if !strings.HasSuffix(urlStr, "/") {
				urlStr += "/"
			}
			urlStr += newKey + "/index.m3u8"
		}
		return urlStr
	}
	
	// 处理以/结尾的URL，直接添加key
	if strings.HasSuffix(urlStr, "/") {
		urlStr += newKey
		return urlStr
	}
	
	// 处理包含密钥的路径格式 (如: /path/oldkey/oldkey)
	if oldKey != "" {
		// 检查URL是否包含旧密钥的路径模式
		oldKeyPattern := "/" + oldKey + "/" + oldKey
		newKeyPattern := "/" + newKey + "/" + newKey
		if strings.Contains(urlStr, oldKeyPattern) {
			// 替换旧密钥模式为新密钥模式
			urlStr = strings.Replace(urlStr, oldKeyPattern, newKeyPattern, 1)
			return urlStr
		}
		
		// 检查URL是否以旧密钥结尾
		if strings.HasSuffix(urlStr, "/"+oldKey) {
			// 替换旧密钥为新密钥
			urlStr = strings.TrimSuffix(urlStr, oldKey) + newKey
		} else if !strings.HasSuffix(urlStr, "/"+newKey) {
			// 如果没有旧密钥，直接添加新密钥
			if !strings.HasSuffix(urlStr, "/") {
				urlStr += "/"
			}
			urlStr += newKey
		}
	} else if !strings.HasSuffix(urlStr, "/"+newKey) {
		// 如果没有旧密钥，直接添加新密钥
		if !strings.HasSuffix(urlStr, "/") {
			urlStr += "/"
		}
		urlStr += newKey
	}
	
	return urlStr
}

// extractStreamKeyFromURL 从URL中提取流密钥
func (m *Manager) extractStreamKeyFromURL(urlStr string) string {
	if urlStr == "" {
		return ""
	}
	
	// 处理RTMP URL
	if strings.HasPrefix(urlStr, "rtmp://") {
		// 从路径中提取密钥
		parts := strings.Split(urlStr, "/")
		if len(parts) > 0 {
			// 密钥通常是最后一个部分
			lastPart := parts[len(parts)-1]
			// 如果包含查询参数，去掉查询参数
			if strings.Contains(lastPart, "?") {
				lastPart = strings.Split(lastPart, "?")[0]
			}
			return lastPart
		}
	}
	
	// 处理HTTP URL
	if strings.Contains(urlStr, "http://") || strings.Contains(urlStr, "https://") {
		// 从路径中提取密钥
		parts := strings.Split(urlStr, "/")
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
			} else if strings.HasSuffix(lastPart, "/index.m3u8") {
				lastPart = strings.TrimSuffix(lastPart, "/index.m3u8")
			}
			return lastPart
		}
	}
	
	return ""
}
