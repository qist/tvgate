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
	"github.com/shirou/gopsutil/v3/process"
)

var (
	globalManager *Manager
	managerMutex  sync.RWMutex
)

// SetManager sets the global manager instance
func SetManager(manager *Manager) {
	managerMutex.Lock()
	defer managerMutex.Unlock()
	globalManager = manager
}

// GetManager gets the global manager instance
func GetManager() *Manager {
	managerMutex.RLock()
	defer managerMutex.RUnlock()
	return globalManager
}

// Manager manages all publisher streams
type Manager struct {
	config *Config
	streams map[string]*StreamManager
	mutex   sync.RWMutex
	// 添加定时器相关字段
	expirationChecker *time.Ticker
	done              chan struct{}
	
	// FFmpeg进程统计
	ffmpegStats map[string]*FFmpegProcessStats
	statsMutex  sync.RWMutex
}

// FFmpegProcessInfo holds information about an FFmpeg process
type FFmpegProcessInfo struct {
	Process   *process.Process
	LastTime  time.Time
	LastBytes uint64 // 上次统计的字节数
}

// StreamManager manages a single stream
type StreamManager struct {
	name              string
	stream            *Stream
	streamKey         string
	oldStreamKey      string // 从URL中提取的旧密钥
	createdAt         time.Time
	cancel            context.CancelFunc
	ctx               context.Context
	mutex             sync.RWMutex
	running           bool
	ffmpegProcesses   map[int]*FFmpegProcessInfo // 按receiver index存储FFmpeg进程信息
	processesMutex    sync.Mutex                 // 保护ffmpegProcesses访问
}

// NewManager creates a new publisher manager
func NewManager(config *Config) *Manager {
	log.Printf("Creating new publisher manager with %d streams", len(config.Streams))
	return &Manager{
		config:      config,
		streams:     make(map[string]*StreamManager),
		ffmpegStats: make(map[string]*FFmpegProcessStats),
		done:        make(chan struct{}), // 初始化done通道
	}
}

// Start starts all enabled streams
func (m *Manager) Start() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	log.Printf("Starting publisher manager")
	
	// 设置全局manager实例
	SetManager(m)

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
				// 重置创建时间为当前时间
				createdAt = time.Now()
				needUpdateConfig = true
			} else {
				log.Printf("Using existing stream key for %s: %s", name, streamKey)
				// 即使密钥未过期，也要确保createdAt有效
				if createdAt.IsZero() {
					createdAt = time.Now()
				}
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
			name:            name,
			stream:          stream,
			streamKey:       streamKey,
			oldStreamKey:    oldStreamKey,
			createdAt:       createdAt,
			cancel:          cancel,
			ctx:             ctx,
			running:         true,
			ffmpegProcesses: make(map[int]*FFmpegProcessInfo),
		}

		m.streams[name] = streamManager
		go streamManager.startStreaming()
		
		// 如果需要更新配置文件（新生成密钥的情况），则更新配置文件
		if needUpdateConfig {
			log.Printf("Updating config file for %s with new stream key", name)
			if err := streamManager.updateConfigFile(streamKey); err != nil {
				log.Printf("Failed to update config file for %s: %v", name, err)
			} else {
				log.Printf("Successfully updated config file for %s", name)
			}
		}
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

// UpdateConfig updates the manager configuration and restarts streams if needed
func (m *Manager) UpdateConfig(newConfig *Config) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	log.Printf("Updating manager config from %d to %d streams", len(m.config.Streams), len(newConfig.Streams))
	
	// 保存旧配置用于比较
	oldConfig := m.config
	m.config = newConfig
	
	// 检查每个流是否需要重启
	for name, newStream := range newConfig.Streams {
		if oldStream, exists := oldConfig.Streams[name]; exists {
			// 流已存在，检查是否需要重启
			if m.streams[name] != nil && m.shouldRestartStream(oldStream, newStream) {
				log.Printf("Stream %s configuration changed, restarting", name)
				// 停止旧流
				m.streams[name].Stop()
				// 创建新流
				m.startStream(name, newStream)
			}
		} else {
			// 新增流
			log.Printf("Adding new stream %s", name)
			m.startStream(name, newStream)
		}
	}
	
	// 检查是否有流被删除
	for name := range oldConfig.Streams {
		if _, exists := newConfig.Streams[name]; !exists {
			// 流被删除
			if stream, exists := m.streams[name]; exists {
				log.Printf("Removing stream %s", name)
				stream.Stop()
				delete(m.streams, name)
			}
		}
	}
	
	log.Printf("Publisher config updated successfully")
}

// shouldRestartStream checks if a stream needs to be restarted based on configuration changes
func (m *Manager) shouldRestartStream(oldStream, newStream *Stream) bool {
	// 检查基本配置是否变化
	if oldStream.Enabled != newStream.Enabled {
		return true
	}
	
	if oldStream.Protocol != newStream.Protocol {
		return true
	}
	
	// 检查源URL是否变化
	if oldStream.Stream.Source.URL != newStream.Stream.Source.URL {
		return true
	}
	
	if oldStream.Stream.Source.BackupURL != newStream.Stream.Source.BackupURL {
		return true
	}
	
	// 检查接收者配置是否变化
	if len(oldStream.Stream.Receivers.All) != len(newStream.Stream.Receivers.All) {
		return true
	}
	
	for i, oldReceiver := range oldStream.Stream.Receivers.All {
		if i >= len(newStream.Stream.Receivers.All) {
			return true
		}
		newReceiver := newStream.Stream.Receivers.All[i]
		if oldReceiver.PushURL != newReceiver.PushURL {
			return true
		}
	}
	
	// 检查推流URL是否变化
	if oldStream.Stream.LocalPlayUrls.Flv != newStream.Stream.LocalPlayUrls.Flv {
		return true
	}
	
	if oldStream.Stream.LocalPlayUrls.Hls != newStream.Stream.LocalPlayUrls.Hls {
		return true
	}
	
	// 如果以上都没有变化，则不需要重启
	return false
}

// startStream starts a single stream
func (m *Manager) startStream(name string, stream *Stream) {
	log.Printf("Starting stream: %s, enabled: %t", name, stream.Enabled)
	if !stream.Enabled {
		log.Printf("Stream %s is disabled, skipping", name)
		return
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
				return
			}
			// 重置创建时间为当前时间
			createdAt = time.Now()
			needUpdateConfig = true
		} else {
			log.Printf("Using existing stream key for %s: %s", name, streamKey)
			// 即使密钥未过期，也要确保createdAt有效
			if createdAt.IsZero() {
				createdAt = time.Now()
			}
		}
	} else {
		// 没有stream key，生成新的
		log.Printf("No existing stream key for %s, generating new key", name)
		var err error
		streamKey, err = stream.GenerateStreamKey()
		if err != nil {
			log.Printf("Failed to generate stream key for %s: %v", name, err)
			cancel()
			return
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
		name:            name,
		stream:          stream,
		streamKey:       streamKey,
		oldStreamKey:    oldStreamKey,
		createdAt:       createdAt,
		cancel:          cancel,
		ctx:             ctx,
		running:         true,
		ffmpegProcesses: make(map[int]*FFmpegProcessInfo),
	}

	m.streams[name] = streamManager
	go streamManager.startStreaming()
	
	// 如果需要更新配置文件（新生成密钥的情况），则更新配置文件
	if needUpdateConfig {
		log.Printf("Updating config file for %s with new stream key", name)
		if err := streamManager.updateConfigFile(streamKey); err != nil {
			log.Printf("Failed to update config file for %s: %v", name, err)
		} else {
			log.Printf("Successfully updated config file for %s", name)
		}
	}
}

// startStreaming starts the streaming process for a single stream
func (sm *StreamManager) startStreaming() {
	log.Printf("Starting stream %s with key %s", sm.name, sm.streamKey)
	
	// 确保即使密钥未过期也能正常启动推流
	// 检查密钥是否过期，如果过期则生成新密钥
	sm.mutex.RLock()
	streamKey := sm.streamKey
	createdAt := sm.createdAt
	sm.mutex.RUnlock()
	
	if sm.stream.CheckStreamKeyExpiration(streamKey, createdAt) {
		log.Printf("Stream key for %s expired at start, generating new key", sm.name)
		// 更新stream key
		newStreamKey, err := sm.stream.UpdateStreamKey()
		if err != nil {
			log.Printf("Failed to generate new stream key for %s: %v", sm.name, err)
			return
		}
		
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
	}
	
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
			// 使用当前的streamKey而不是启动时的streamKey
			sm.mutex.RLock()
			currentStreamKey := sm.streamKey
			sm.mutex.RUnlock()
			
			cmd := r.BuildFFmpegPushCommand(ffmpegCmd, currentStreamKey)
			log.Printf("Full FFmpeg command for stream %s receiver %d: ffmpeg %s", sm.name, index+1, strings.Join(cmd, " "))
			sm.runFFmpegStream(cmd, index+1)
		}(i, receiver)
	}
	
	// 等待所有接收器完成或上下文取消
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-sm.ctx.Done():
		log.Printf("Stream %s context cancelled, stopping", sm.name)
	case <-done:
		log.Printf("Stream %s finished", sm.name)
	}
}

// runFFmpegStream runs the ffmpeg stream with restart capability
func (sm *StreamManager) runFFmpegStream(cmd []string, receiverIndex int) {
	log.Printf("Starting FFmpeg stream routine for %s receiver %d", sm.name, receiverIndex)
	
	for {
		// 检查stream manager是否仍在运行
		if !sm.isRunning() {
			log.Printf("Stream %s receiver %d stopped - manager not running", sm.name, receiverIndex)
			// 更新统计信息
			manager := GetManager()
			if manager != nil {
				manager.updateFFmpegStats(sm.name, receiverIndex, 0, false, nil, 0, time.Now())
			}
			return
		}
		
		select {
		case <-sm.ctx.Done():
			log.Printf("Stream %s receiver %d context cancelled", sm.name, receiverIndex)
			// 更新统计信息
			manager := GetManager()
			if manager != nil {
				manager.updateFFmpegStats(sm.name, receiverIndex, 0, false, nil, 0, time.Now())
			}
			return
		default:
			log.Printf("Starting FFmpeg stream for %s receiver %d", sm.name, receiverIndex)
			
			// Execute FFmpeg with monitoring
			var lastBytes uint64 = 0
			var startTime time.Time
			
			err := sm.stream.ExecuteFFmpegWithMonitoring(sm.ctx, cmd, 
				func(pid int32, proc *process.Process) {
					// Process started callback
					log.Printf("FFmpeg process started for %s receiver %d with PID %d", sm.name, receiverIndex, pid)
					// Update process information
					sm.updateFFmpegProcessInfo(receiverIndex, proc)
					
					// 记录开始时间
					startTime = time.Now()
					
					// 更新统计信息
					manager := GetManager()
					if manager != nil {
						manager.updateFFmpegStats(sm.name, receiverIndex, pid, true, nil, lastBytes, startTime)
					}
				},
				func(bytes uint64) {
					// Stats update callback
					lastBytes = bytes
					log.Printf("FFmpeg stream for %s receiver %d transferred %d bytes", sm.name, receiverIndex, bytes)
					
					// 更新统计信息
					manager := GetManager()
					if manager != nil {
						manager.updateFFmpegStats(sm.name, receiverIndex, 0, true, nil, bytes, time.Now())
					}
				})
				
			if err != nil {
				log.Printf("FFmpeg stream for %s receiver %d failed: %v", sm.name, receiverIndex, err)
				
				// 更新统计信息（错误状态）
				manager := GetManager()
				if manager != nil {
					manager.updateFFmpegStats(sm.name, receiverIndex, 0, false, err, lastBytes, time.Now())
				}
				
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
				// 更新统计信息（正常结束）
				manager := GetManager()
				if manager != nil {
					manager.updateFFmpegStats(sm.name, receiverIndex, 0, false, nil, 0, time.Now())
				}
				
				// 检查是否应该继续运行
				if !sm.isRunning() {
					return
				}
				
				// 等待一段时间后继续
				select {
				case <-sm.ctx.Done():
					log.Printf("Stream %s receiver %d stopped after normal finish", sm.name, receiverIndex)
					return
				case <-time.After(1 * time.Second):
					// 继续循环
					log.Printf("Restarting stream %s receiver %d after normal finish", sm.name, receiverIndex)
				}
			}
		}
	}
}

// restartStreaming 重新启动推流
func (sm *StreamManager) restartStreaming() {
	log.Printf("Restarting streaming for %s", sm.name)
	
	// 先停止当前推流
	sm.Stop()
	
	// 等待一小段时间确保旧的推流完全停止
	time.Sleep(100 * time.Millisecond)
	
	// 重新创建context
	sm.mutex.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	sm.cancel = cancel
	sm.ctx = ctx
	sm.running = true // 确保设置running状态为true
	sm.mutex.Unlock()
	
	// 启动新的推流
	go sm.startStreaming()
	
	log.Printf("Restarted streaming for %s", sm.name)
}

// isRunning checks if the stream manager is still running
func (sm *StreamManager) isRunning() bool {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	select {
	case <-sm.ctx.Done():
		return false
	default:
		return sm.running
	}
}

// Stop stops the stream manager
func (sm *StreamManager) Stop() {
	log.Printf("Stopping stream manager for stream: %s", sm.name)
	
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	if sm.running {
		sm.running = false
		if sm.cancel != nil {
			sm.cancel()
		}
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
					
					// 处理all模式
					if allReceivers, ok := receivers["all"].([]interface{}); ok {
						log.Printf("Found all receivers config with %d receivers", len(allReceivers))
						for i, receiver := range allReceivers {
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
					}
				} else if receivers, ok := streamData["receivers"].([]interface{}); ok {
					// 处理直接数组格式的receivers（兼容旧格式）
					log.Printf("Found direct receivers array config with %d receivers", len(receivers))
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
	
	log.Printf("Replacing stream key in URL: %s, oldKey: %s, newKey: %s", urlStr, oldKey, newKey)
	
	// 如果旧密钥为空，则直接在URL末尾添加新密钥
	if oldKey == "" {
		// 处理各种URL格式
		if strings.HasSuffix(urlStr, ".flv") {
			urlStr = strings.TrimSuffix(urlStr, ".flv")
			if !strings.HasSuffix(urlStr, "/") {
				urlStr += "/"
			}
			urlStr += newKey + ".flv"
		} else if strings.HasSuffix(urlStr, ".m3u8") {
			urlStr = strings.TrimSuffix(urlStr, ".m3u8")
			if !strings.HasSuffix(urlStr, "/") {
				urlStr += "/"
			}
			urlStr += newKey + ".m3u8"
		} else if strings.HasSuffix(urlStr, "index.m3u8") {
			urlStr = strings.TrimSuffix(urlStr, "index.m3u8")
			if !strings.HasSuffix(urlStr, "/") {
				urlStr += "/"
			}
			urlStr += newKey + "/index.m3u8"
		} else {
			// 普通路径格式
			if !strings.HasSuffix(urlStr, "/") {
				urlStr += "/"
			}
			urlStr += newKey
		}
		log.Printf("Updated URL with new key: %s", urlStr)
		return urlStr
	}
	
	// 如果URL中同时包含旧密钥和新密钥，说明之前替换出错，需要先移除新密钥
	if strings.Contains(urlStr, "/"+oldKey+"/") && strings.Contains(urlStr, "/"+newKey+"/") {
		// 移除URL中的新密钥部分
		urlStr = strings.Replace(urlStr, "/"+newKey, "", -1)
	} else if strings.Contains(urlStr, "/"+oldKey) && strings.Contains(urlStr, "/"+newKey) {
		// 另一种形式的重复密钥，移除新密钥
		urlStr = strings.Replace(urlStr, "/"+newKey, "", -1)
	}
	
	// 处理.flv扩展名
	if strings.HasSuffix(urlStr, ".flv") {
		if strings.Contains(urlStr, "/"+oldKey+".flv") {
			urlStr = strings.Replace(urlStr, "/"+oldKey+".flv", "/"+newKey+".flv", 1)
		} else {
			// 如果没有找到旧密钥格式，尝试替换URL中的旧密钥
			urlStr = strings.Replace(urlStr, "/"+oldKey, "/"+newKey, 1)
		}
		log.Printf("Updated .flv URL: %s", urlStr)
		return urlStr
	}
	
	// 处理.m3u8扩展名
	if strings.HasSuffix(urlStr, ".m3u8") {
		if strings.Contains(urlStr, "/"+oldKey+".m3u8") {
			urlStr = strings.Replace(urlStr, "/"+oldKey+".m3u8", "/"+newKey+".m3u8", 1)
		} else {
			// 如果没有找到旧密钥格式，尝试替换URL中的旧密钥
			urlStr = strings.Replace(urlStr, "/"+oldKey, "/"+newKey, 1)
		}
		log.Printf("Updated .m3u8 URL: %s", urlStr)
		return urlStr
	}
	
	// 处理index.m3u8扩展名
	if strings.HasSuffix(urlStr, "index.m3u8") {
		if strings.Contains(urlStr, "/"+oldKey+"/index.m3u8") {
			urlStr = strings.Replace(urlStr, "/"+oldKey+"/index.m3u8", "/"+newKey+"/index.m3u8", 1)
		} else {
			// 如果没有找到旧密钥格式，尝试替换URL中的旧密钥
			urlStr = strings.Replace(urlStr, "/"+oldKey, "/"+newKey, 1)
		}
		log.Printf("Updated index.m3u8 URL: %s", urlStr)
		return urlStr
	}
	
	// 处理路径格式 (如: /path/oldKey)
	if strings.Contains(urlStr, "/"+oldKey+"/") {
		urlStr = strings.Replace(urlStr, "/"+oldKey+"/", "/"+newKey+"/", 1)
	} else if strings.HasSuffix(urlStr, "/"+oldKey) {
		urlStr = strings.TrimSuffix(urlStr, "/"+oldKey) + "/" + newKey
	} else {
		// 最后尝试直接替换旧密钥
		urlStr = strings.Replace(urlStr, "/"+oldKey, "/"+newKey, 1)
	}
	
	log.Printf("Updated general URL: %s", urlStr)
	return urlStr
}

// updateFFmpegStats updates the FFmpeg process statistics
func (m *Manager) updateFFmpegStats(streamName string, receiverIndex int, pid int32, running bool, err error, bytesTransferred uint64, currentTime time.Time) {
	key := streamName + "_" + string(rune(receiverIndex+'0'))
	
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()
	
	stat, exists := m.ffmpegStats[key]
	if !exists {
		stat = &FFmpegProcessStats{
			StreamName:    streamName,
			ReceiverIndex: receiverIndex,
			PID:           pid,
			StartTime:     time.Now(),
		}
		m.ffmpegStats[key] = stat
	}
	
	stat.LastUpdate = time.Now()
	stat.Running = running
	stat.BytesTransferred = bytesTransferred
	
	// 计算运行时长
	stat.Duration = time.Since(stat.StartTime).Seconds()
	
	// 计算平均码率
	if stat.Duration > 0 {
		stat.AvgBitrate = uint64(float64(stat.BytesTransferred*8) / stat.Duration)
	}
	
	// 计算当前码率（基于上次更新）
	if exists && !stat.LastUpdate.IsZero() {
		timeDiff := time.Since(stat.LastUpdate).Seconds()
		bytesDiff := bytesTransferred - stat.BytesTransferred
		if timeDiff > 0 {
			stat.CurrentBitrate = uint64(float64(bytesDiff*8) / timeDiff)
		}
	}
	
	// 获取进程的CPU和内存使用情况
	if pid > 0 {
		proc, err := process.NewProcess(pid)
		if err == nil {
			// 获取CPU使用率
			cpuPercent, err := proc.CPUPercent()
			if err == nil {
				stat.CPUPercent = cpuPercent
			}
			
			// 获取内存使用情况
			memInfo, err := proc.MemoryInfo()
			if err == nil {
				stat.MemoryRSS = memInfo.RSS
			}
		}
	}
	
	if err != nil {
		stat.LastError = err.Error()
	} else {
		stat.LastError = ""
	}
}

// updateFFmpegProcessInfo 更新FFmpeg进程信息
func (sm *StreamManager) updateFFmpegProcessInfo(receiverIndex int, proc *process.Process) {
	sm.processesMutex.Lock()
	defer sm.processesMutex.Unlock()

	if sm.ffmpegProcesses == nil {
		sm.ffmpegProcesses = make(map[int]*FFmpegProcessInfo)
	}
	
	if sm.ffmpegProcesses[receiverIndex] == nil {
		sm.ffmpegProcesses[receiverIndex] = &FFmpegProcessInfo{}
	}
	
	sm.ffmpegProcesses[receiverIndex].Process = proc
	sm.ffmpegProcesses[receiverIndex].LastTime = time.Now()
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
