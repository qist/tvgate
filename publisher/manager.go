package publisher

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
	"github.com/shirou/gopsutil/v3/process"
	"gopkg.in/yaml.v3"
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
	config  *Config
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
	name            string
	stream          *Stream
	streamKey       string
	oldStreamKey    string // 从URL中提取的旧密钥
	createdAt       time.Time
	cancel          context.CancelFunc
	ctx             context.Context
	mutex           sync.RWMutex
	running         bool
	ffmpegProcesses map[int]*FFmpegProcessInfo // 按receiver index存储FFmpeg进程信息
	processesMutex  sync.Mutex                 // 保护ffmpegProcesses访问
	pipeForwarder   *PipeForwarder             // 用于本地播放的管道转发器
}

// NewManager creates a new publisher manager
func NewManager(config *Config) *Manager {
	logger.LogPrintf("Creating new publisher manager with %d streams", len(config.Streams))
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

	logger.LogPrintf("Starting publisher manager")

	// 设置全局manager实例
	SetManager(m)

	// 启动过期检查器
	m.startExpirationChecker()

	if m.config.Streams == nil {
		logger.LogPrintf("No streams configured")
		return nil
	}

	logger.LogPrintf("Found %d streams in config", len(m.config.Streams))

	for name, stream := range m.config.Streams {
		logger.LogPrintf("Processing stream: %s, enabled: %t", name, stream.Enabled)
		if !stream.Enabled {
			logger.LogPrintf("Stream %s is disabled, skipping", name)
			continue
		}

		// Create context for this stream
		ctx, cancel := context.WithCancel(context.Background())

		// 简化streamkey处理逻辑，只支持random类型
		var streamKey string
		var createdAt time.Time
		needUpdateConfig := false

		// 检查配置中的streamkey值是否为空，如果为空则生成新的密钥
		if stream.StreamKey.Value == "" {
			logger.LogPrintf("No existing stream key for %s, generating new key", name)
			var err error
			streamKey, err = stream.GenerateStreamKey()
			if err != nil {
				logger.LogPrintf("Failed to generate stream key for %s: %v", name, err)
				cancel()
				continue
			}
			createdAt = time.Now()
			needUpdateConfig = true
		} else {
			// 使用配置文件中的密钥值
			streamKey = stream.StreamKey.Value
			createdAt = stream.StreamKey.CreatedAt

			// 检查创建时间是否有效
			if createdAt.IsZero() {
				createdAt = time.Now()
				logger.LogPrintf("Stream key creation time is zero, using current time: %v", createdAt)
			}

			// 检查密钥是否过期
			if stream.CheckStreamKeyExpiration(streamKey, createdAt) {
				// 已过期，生成新的stream key
				logger.LogPrintf("Existing stream key for %s expired, generating new key", name)
				var err error
				streamKey, err = stream.GenerateStreamKey()
				if err != nil {
					logger.LogPrintf("Failed to generate stream key for %s: %v", name, err)
					cancel()
					continue
				}
				// 重置创建时间为当前时间
				createdAt = time.Now()
				needUpdateConfig = true
			} else {
				logger.LogPrintf("Using existing stream key for %s: %s", name, streamKey)
			}
		}

		streamManager := &StreamManager{
			name:            name,
			stream:          stream,
			streamKey:       streamKey,
			createdAt:       createdAt,
			cancel:          cancel,
			ctx:             ctx,
			running:         true,
			ffmpegProcesses: make(map[int]*FFmpegProcessInfo),
		}

		// 如果启用了pipe forwarder，则创建并启动它
		if stream.PipeForwarder != nil && stream.PipeForwarder.enabled {
			logger.LogPrintf("Starting pipe forwarder for stream %s", name)

			// 创建PipeForwarder实例
			streamManager.pipeForwarder = NewPipeForwarder(
				streamManager.name,
				stream.PipeForwarder.rtmpURL,
				stream.PipeForwarder.enabled,
				stream.PipeForwarder.needPull,
				stream.PipeForwarder.hub,
			)

			// 构建FFmpeg命令
			ffmpegArgs := stream.BuildFFmpegCommand()

			// 启动PipeForwarder
			if err := streamManager.pipeForwarder.Start(ffmpegArgs); err != nil {
				logger.LogPrintf("Failed to start pipe forwarder for stream %s: %v", name, err)
				return err
			}

			logger.LogPrintf("Pipe forwarder started successfully for stream %s", name)
		}

		m.streams[name] = streamManager
		go streamManager.startStreaming()

		// 如果需要更新配置文件（新生成密钥的情况），则更新配置文件
		if needUpdateConfig {
			logger.LogPrintf("Updating config file for %s with stream key", name)
			if err := streamManager.updateConfigFile(streamKey); err != nil {
				logger.LogPrintf("Failed to update config file for %s: %v", name, err)
			} else {
				logger.LogPrintf("Successfully updated config file for %s", name)
			}
		}
	}

	logger.LogPrintf("Publisher manager started with %d active streams", len(m.streams))
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

		logger.LogPrintf("Checking if stream key expired for %s (created at: %v)", stream.name, createdAt)
		if stream.stream.CheckStreamKeyExpiration(streamKey, createdAt) {
			logger.LogPrintf("Stream key for %s expired, generating new key", stream.name)
			// 更新stream key
			newStreamKey, err := stream.stream.UpdateStreamKey()
			if err != nil {
				logger.LogPrintf("Failed to generate new stream key for %s: %v", stream.name, err)
			} else {
				stream.mutex.Lock()
				stream.streamKey = newStreamKey
				stream.createdAt = time.Now()
				stream.mutex.Unlock()
				logger.LogPrintf("Generated new stream key for %s: %s", stream.name, newStreamKey)

				// 回写配置到YAML文件
				logger.LogPrintf("Updating config file for %s with new stream key", stream.name)
				if err := stream.updateConfigFile(newStreamKey); err != nil {
					logger.LogPrintf("Failed to update config file for %s: %v", stream.name, err)
				} else {
					logger.LogPrintf("Successfully updated config file for %s", stream.name)
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
	var names []string
	for name := range m.streams {
		names = append(names, name)
	}
	logger.LogPrintf("Stopping publisher manager with %d streams: %s",
		len(names), strings.Join(names, ", "))

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

	logger.LogPrintf("Stopping all streams")

	for name, streamManager := range m.streams {
		logger.LogPrintf("Stopping stream: %s", name)
		streamManager.Stop()
	}

	logger.LogPrintf("All streams stopped")
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

	// 检查streamkey配置是否变化
	if oldStream.StreamKey.Type != newStream.StreamKey.Type {
		return true
	}

	if oldStream.StreamKey.Value != newStream.StreamKey.Value {
		return true
	}

	if oldStream.StreamKey.Length != newStream.StreamKey.Length {
		return true
	}

	if oldStream.StreamKey.Expiration != newStream.StreamKey.Expiration {
		return true
	}

	// 如果以上都没有变化，则不需要重启
	return false
}

// startStream starts a single stream
func (m *Manager) startStream(name string, stream *Stream) {
	logger.LogPrintf("Starting stream: %s, enabled: %t", name, stream.Enabled)
	if !stream.Enabled {
		logger.LogPrintf("Stream %s is disabled, skipping", name)
		return
	}

	// 检查流是否已经存在，如果存在则先停止它
	if existingStream, exists := m.streams[name]; exists {
		logger.LogPrintf("Stream %s already exists, stopping it first", name)
		existingStream.Stop()
		delete(m.streams, name)
	}

	// Create context for this stream
	ctx, cancel := context.WithCancel(context.Background())

	// 简化streamkey处理逻辑，只支持random类型
	var streamKey string
	var createdAt time.Time
	needUpdateConfig := false

	// 检查配置中的streamkey值是否为空，如果为空则生成新的密钥
	if stream.StreamKey.Value == "" {
		logger.LogPrintf("No existing stream key for %s, generating new key", name)
		var err error
		streamKey, err = stream.GenerateStreamKey()
		if err != nil {
			logger.LogPrintf("Failed to generate stream key for %s: %v", name, err)
			cancel()
			return
		}
		createdAt = time.Now()
		needUpdateConfig = true
	} else {
		// 使用配置文件中的密钥值
		streamKey = stream.StreamKey.Value
		createdAt = stream.StreamKey.CreatedAt

		// 检查创建时间是否有效
		if createdAt.IsZero() {
			createdAt = time.Now()
			logger.LogPrintf("Stream key creation time is zero, using current time: %v", createdAt)
		}

		// 检查密钥是否过期
		if stream.CheckStreamKeyExpiration(streamKey, createdAt) {
			// 已过期，生成新的stream key
			logger.LogPrintf("Existing stream key for %s expired, generating new key", name)
			var err error
			streamKey, err = stream.GenerateStreamKey()
			if err != nil {
				logger.LogPrintf("Failed to generate stream key for %s: %v", name, err)
				cancel()
				return
			}
			// 重置创建时间为当前时间
			createdAt = time.Now()
			needUpdateConfig = true
		} else {
			logger.LogPrintf("Using existing stream key for %s: %s", name, streamKey)
		}
	}

	streamManager := &StreamManager{
		name:            name,
		stream:          stream,
		streamKey:       streamKey,
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
		logger.LogPrintf("Updating config file for %s with stream key", name)
		if err := streamManager.updateConfigFile(streamKey); err != nil {
			logger.LogPrintf("Failed to update config file for %s: %v", name, err)
		} else {
			logger.LogPrintf("Successfully updated config file for %s", name)
		}
	}
}

// UpdateConfig updates the manager configuration and restarts streams if needed
func (m *Manager) UpdateConfig(newConfig *Config) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	logger.LogPrintf("Updating manager config from %d to %d streams", len(m.config.Streams), len(newConfig.Streams))

	// 保存旧配置用于比较
	oldConfig := m.config
	m.config = newConfig

	// 检查每个流是否需要重启或者启停
	for name, newStream := range newConfig.Streams {
		logger.LogPrintf("Processing stream %s during config update", name)
		if oldStream, exists := oldConfig.Streams[name]; exists {
			logger.LogPrintf("Stream %s already exists, checking if it needs to be restarted", name)
			// 流已存在，检查是否需要重启
			needRestart := m.shouldRestartStream(oldStream, newStream)
			logger.LogPrintf("Stream %s needRestart: %t", name, needRestart)
			if m.streams[name] != nil && needRestart {
				logger.LogPrintf("Stream %s configuration changed, handling appropriately", name)
				// 如果enabled状态发生变化
				if oldStream.Enabled != newStream.Enabled {
					if newStream.Enabled {
						// 从禁用变为启用，启动流
						logger.LogPrintf("Stream %s enabled, starting", name)
						m.startStream(name, newStream)
					} else {
						// 从启用变为禁用，停止流
						logger.LogPrintf("Stream %s disabled, stopping", name)
						m.streams[name].Stop()
						delete(m.streams, name)
					}
				} else {
					// 其他配置变化，重启流
					logger.LogPrintf("Stream %s configuration changed, restarting", name)
					// 停止旧流
					m.streams[name].Stop()
					// 创建新流
					m.startStream(name, newStream)
				}
			} else if m.streams[name] == nil && newStream.Enabled {
				// 流管理器不存在但流应该启用，启动流
				logger.LogPrintf("Stream %s manager not found but stream is enabled, starting", name)
				m.startStream(name, newStream)
			} else {
				logger.LogPrintf("Stream %s no change or not enabled, skipping", name)
			}
		} else {
			// 新增流
			logger.LogPrintf("Adding new stream %s", name)
			m.startStream(name, newStream)
		}
	}

	// 检查是否有流被删除
	for name := range oldConfig.Streams {
		if _, exists := newConfig.Streams[name]; !exists {
			// 流被删除
			if stream, exists := m.streams[name]; exists {
				logger.LogPrintf("Removing stream %s", name)
				stream.Stop()
				delete(m.streams, name)
			}
		}
	}

	logger.LogPrintf("Publisher config updated successfully")
}

// startStreaming starts the streaming process for a single stream
func (sm *StreamManager) startStreaming() {
	logger.LogPrintf("Starting stream %s with key %s", sm.name, sm.streamKey)

	// 确保即使密钥未过期也能正常启动推流
	// 检查密钥是否过期，如果过期则生成新密钥
	sm.mutex.RLock()
	streamKey := sm.streamKey
	createdAt := sm.createdAt
	sm.mutex.RUnlock()

	if sm.stream.CheckStreamKeyExpiration(streamKey, createdAt) {
		logger.LogPrintf("Stream key for %s expired at start, generating new key", sm.name)
		// 更新stream key
		newStreamKey, err := sm.stream.UpdateStreamKey()
		if err != nil {
			logger.LogPrintf("Failed to generate new stream key for %s: %v", sm.name, err)
			return
		}

		sm.mutex.Lock()
		oldKey := sm.streamKey
		sm.oldStreamKey = oldKey // 保存旧密钥
		sm.streamKey = newStreamKey
		sm.createdAt = time.Now()
		sm.mutex.Unlock()
		logger.LogPrintf("Generated new stream key for %s: %s (was: %s)", sm.name, newStreamKey, oldKey)

		// 回写配置到YAML文件
		logger.LogPrintf("Updating config file for %s with new stream key", sm.name)
		if err := sm.updateConfigFile(newStreamKey); err != nil {
			logger.LogPrintf("Failed to update config file for %s: %v", sm.name, err)
		} else {
			logger.LogPrintf("Successfully updated config file for %s", sm.name)
		}
	}

	// Build FFmpeg command
	ffmpegCmd := sm.stream.BuildFFmpegCommand()

	// 检查是否配置了本地播放URL，如果配置了则启动管道转发器
	localPlayUrls := sm.stream.Stream.LocalPlayUrls
	// 只有当flv或hls为true时才启动管道转发器，并且根据具体配置启用对应功能
	if localPlayUrls.Flv || localPlayUrls.Hls {
		receivers := sm.stream.Stream.GetReceivers()
		if len(receivers) == 0 {
			logger.LogPrintf("[%s] No receivers found, skip pipe forwarder", sm.name)
			return
		}

		// 使用当前的 streamKey
		sm.mutex.RLock()
		currentStreamKey := sm.streamKey
		sm.mutex.RUnlock()

		// 根据模式确定如何处理管道转发器
		if sm.stream.Stream.Mode == "all" {
			// 在 all 模式下，为每个接收器创建一个管道转发器
			for i, receiver := range receivers {
				// 构建接收器的 FFmpeg 推流命令以提取 RTMP URL
				receiverCmd := receiver.BuildFFmpegPushCommand(ffmpegCmd, currentStreamKey)
				var rtmpURL string
				if len(receiverCmd) > 0 {
					// RTMP URL通常是命令的最后一个参数
					rtmpURL = receiverCmd[len(receiverCmd)-1]
				}

				// 为每个接收器创建独立的管道转发器
				pipeName := fmt.Sprintf("%s_receiver_%d", sm.name, i+1)
				if i == 0 {
					// 第一个接收器使用主名称
					pipeName = sm.name
					sm.pipeForwarder = NewPipeForwarder(pipeName, rtmpURL, true, true, nil)
					// 在这里可以添加控制HLS启用/禁用的逻辑
					// 根据配置设置HLS启用状态
					if sm.pipeForwarder != nil {
						sm.pipeForwarder.EnableHLS(localPlayUrls.Hls)
					}

					// 启动主管道转发器
					go func() {
						if err := sm.pipeForwarder.Start(ffmpegCmd); err != nil {
							logger.LogPrintf("Failed to start pipe forwarder for stream %s: %v", sm.name, err)
						}
					}()
				} else {
					// 其他接收器创建独立的管道转发器
					pipeForwarder := NewPipeForwarder(pipeName, rtmpURL, true, false, sm.pipeForwarder.hub)

					// 启动额外的管道转发器
					go func(pf *PipeForwarder, name string) {
						if err := pf.Start(ffmpegCmd); err != nil {
							logger.LogPrintf("Failed to start pipe forwarder for stream %s: %v", name, err)
						}
					}(pipeForwarder, pipeName)
				}
			}

			// 不启动传统的推流方式
			return
		} else if sm.stream.Stream.Mode == "primary-backup" {
			// 在 primary-backup 模式下，我们需要为 primary 和 backup 都创建转发器
			// 为 primary 创建 PipeForwarder
			var primaryRTMPURL string
			primaryCmd := receivers[0].BuildFFmpegPushCommand(ffmpegCmd, currentStreamKey)
			if len(primaryCmd) > 0 {
				primaryRTMPURL = primaryCmd[len(primaryCmd)-1]
			}

			sm.pipeForwarder = NewPipeForwarder(sm.name+"_primary", primaryRTMPURL, true, true, nil)
			// 控制HLS启用状态
			// 根据配置设置HLS启用状态
			if sm.pipeForwarder != nil {
				sm.pipeForwarder.EnableHLS(localPlayUrls.Hls)
			}
			// 启动 primary PipeForwarder
			go func() {
				if err := sm.pipeForwarder.Start(ffmpegCmd); err != nil {
					logger.LogPrintf("Failed to start primary pipe forwarder for stream %s: %v", sm.name, err)
				}
			}()

			// 如果有 backup receiver，也为它创建一个独立的 PipeForwarder
			if sm.stream.Stream.Receivers.Backup != nil {
				var backupRTMPURL string
				backupCmd := sm.stream.Stream.Receivers.Backup.BuildFFmpegPushCommand(ffmpegCmd, currentStreamKey)
				if len(backupCmd) > 0 {
					backupRTMPURL = backupCmd[len(backupCmd)-1]
				}

				// 创建 backup PipeForwarder
				backupPipeForwarder := NewPipeForwarder(sm.name+"_backup", backupRTMPURL, true, true, nil)
				// 控制HLS启用状态
				if backupPipeForwarder != nil {
					backupPipeForwarder.EnableHLS(localPlayUrls.Hls)
				}

				// 启动 backup PipeForwarder
				go func() {
					if err := backupPipeForwarder.Start(ffmpegCmd); err != nil {
						logger.LogPrintf("Failed to start backup pipe forwarder for stream %s: %v", sm.name, err)
					}
				}()
			} else if sm.stream.Stream.Source.BackupURL != "" {
				// 如果没有显式配置 backup receiver 但配置了 backup_url，则创建一个使用 backup_url 的 PipeForwarder
				// 构建使用 backup_url 的 FFmpeg 命令
				backupFFmpegCmd := sm.buildFFmpegCommandWithBackup(true)
				var backupRTMPURL string

				// 为 backup 流使用相同的接收器配置
				backupCmd := receivers[0].BuildFFmpegPushCommand(backupFFmpegCmd, currentStreamKey)
				if len(backupCmd) > 0 {
					backupRTMPURL = backupCmd[len(backupCmd)-1]
				}

				// 创建 backup PipeForwarder (当主URL失效时，备用PipeForwarder需要主动拉流)
				backupPipeForwarder := NewPipeForwarder(sm.name+"_backup", backupRTMPURL, true, true, nil)
				// 控制HLS启用状态
				if backupPipeForwarder != nil {
					backupPipeForwarder.EnableHLS(localPlayUrls.Hls)
				}

				// 启动 backup PipeForwarder（但默认不激活，只有在主URL失败时才激活）
				go func() {
					// 先不启动，等待主URL失败后再启动
					logger.LogPrintf("Backup pipe forwarder for stream %s created, waiting for primary failure", sm.name)

					// 监控主URL是否失败，如果失败则启动backup
					ticker := time.NewTicker(10 * time.Second)
					defer ticker.Stop()

					for {
						select {
						case <-sm.ctx.Done():
							return
						case <-ticker.C:
							// 检查主转发器是否运行正常
							if sm.pipeForwarder != nil && !sm.pipeForwarder.IsRunning() {
								logger.LogPrintf("Primary pipe forwarder for stream %s is not running, starting backup", sm.name)
								if err := backupPipeForwarder.Start(backupFFmpegCmd); err != nil {
									logger.LogPrintf("Failed to start backup pipe forwarder for stream %s: %v", sm.name, err)
								} else {
									logger.LogPrintf("Backup pipe forwarder for stream %s started successfully", sm.name)
									// 更新主转发器引用
									sm.pipeForwarder = backupPipeForwarder
									return
								}
							}
						}
					}
				}()
			}

			// 不启动传统的推流方式
			return
		} else {
			// 对于其他模式，保持原有逻辑（只处理第一个接收器）
			var rtmpURL string
			receiverCmd := receivers[0].BuildFFmpegPushCommand(ffmpegCmd, currentStreamKey)
			if len(receiverCmd) > 0 {
				rtmpURL = receiverCmd[len(receiverCmd)-1]
			}

			// 创建PipeForwarder，传入RTMP URL
			sm.pipeForwarder = NewPipeForwarder(sm.name, rtmpURL, true, true, nil)
			// 控制HLS启用状态
			if sm.pipeForwarder != nil {
				sm.pipeForwarder.EnableHLS(localPlayUrls.Hls)
			}

			// 启动管道转发器
			go func() {
				if err := sm.pipeForwarder.Start(ffmpegCmd); err != nil {
					logger.LogPrintf("Failed to start pipe forwarder for stream %s: %v", sm.name, err)
				}
			}()

			// 如果使用PipeForwarder，则不启动传统的推流方式
			return
		}
	}

	// Get receivers
	receivers := sm.stream.Stream.GetReceivers()
	logger.LogPrintf("Stream %s has %d receivers", sm.name, len(receivers))

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
			logger.LogPrintf("Full FFmpeg command for stream %s receiver %d: ffmpeg %s", sm.name, index+1, strings.Join(cmd, " "))
			sm.runFFmpegStream(cmd, index+1)
		}(i, receiver)
	}

	// 对于 primary-backup 模式，额外处理 backup receiver
	if sm.stream.Stream.Mode == "primary-backup" && sm.stream.Stream.Receivers.Backup != nil {
		// 等待一段时间观察 primary 是否正常工作
		go func() {
			// 等待一段时间让 primary 先启动
			time.Sleep(5 * time.Second)

			// 检查 primary 是否正常运行
			if sm.isPrimaryHealthy() {
				logger.LogPrintf("Primary receiver for stream %s is healthy, not starting backup", sm.name)
			} else {
				logger.LogPrintf("Primary receiver for stream %s is unhealthy, starting backup", sm.name)
				wg.Add(1)
				go func() {
					defer wg.Done()
					// 使用当前的streamKey而不是启动时的streamKey
					sm.mutex.RLock()
					currentStreamKey := sm.streamKey
					sm.mutex.RUnlock()

					// 构建 backup receiver 的 FFmpeg 命令
					cmd := sm.stream.Stream.Receivers.Backup.BuildFFmpegPushCommand(ffmpegCmd, currentStreamKey)
					logger.LogPrintf("Full FFmpeg command for stream %s backup receiver: ffmpeg %s", sm.name, strings.Join(cmd, " "))
					// index 为 2，因为 primary 是 1，backup 是 2
					sm.runFFmpegStream(cmd, 2)
				}()
			}
		}()
	}

	// 等待所有接收器完成或上下文取消
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-sm.ctx.Done():
		logger.LogPrintf("Stream %s context cancelled, stopping", sm.name)
	case <-done:
		logger.LogPrintf("Stream %s finished", sm.name)
	}
}

// isPrimaryHealthy 检查 primary receiver 是否健康
func (sm *StreamManager) isPrimaryHealthy() bool {
	// 检查索引为1的 FFmpeg 进程状态（primary receiver）
	manager := GetManager()
	if manager == nil {
		return false
	}

	// 获取 FFmpeg 统计信息
	stats := manager.GetFFmpegStats(sm.name, 1) // primary receiver 是索引 1
	if stats == nil {
		return false
	}

	// 检查进程是否正在运行且没有错误
	return stats.Running && stats.LastError == ""
}

// runFFmpegStream runs the ffmpeg stream with restart capability
func (sm *StreamManager) runFFmpegStream(cmd []string, receiverIndex int) {
	logger.LogPrintf("Starting FFmpeg stream routine for %s receiver %d", sm.name, receiverIndex)

	// 添加一个标志来跟踪是否已经尝试过backup_url
	useBackup := false

	for {
		// 检查stream manager是否仍在运行
		if !sm.isRunning() {
			logger.LogPrintf("Stream %s receiver %d stopped - manager not running", sm.name, receiverIndex)
			// 更新统计信息
			manager := GetManager()
			if manager != nil {
				manager.updateFFmpegStats(sm.name, receiverIndex, 0, false, nil, 0, time.Now())
			}
			return
		}

		select {
		case <-sm.ctx.Done():
			logger.LogPrintf("Stream %s receiver %d context cancelled", sm.name, receiverIndex)
			// 更新统计信息
			manager := GetManager()
			if manager != nil {
				manager.updateFFmpegStats(sm.name, receiverIndex, 0, false, nil, 0, time.Now())
			}
			return
		default:
			logger.LogPrintf("Starting FFmpeg stream for %s receiver %d", sm.name, receiverIndex)

			// Execute FFmpeg with monitoring
			var lastBytes uint64 = 0
			var startTime time.Time

			err := sm.stream.ExecuteFFmpegWithMonitoring(sm.ctx, cmd,
				func(pid int32, proc *process.Process) {
					// Process started callback
					logger.LogPrintf("FFmpeg process started for %s receiver %d with PID %d", sm.name, receiverIndex, pid)
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
					logger.LogPrintf("FFmpeg stream for %s receiver %d transferred %d bytes", sm.name, receiverIndex, bytes)

					// 更新统计信息
					manager := GetManager()
					if manager != nil {
						manager.updateFFmpegStats(sm.name, receiverIndex, 0, true, nil, bytes, time.Now())
					}
				})

			if err != nil {
				logger.LogPrintf("FFmpeg stream for %s receiver %d failed: %v", sm.name, receiverIndex, err)

				// 更新统计信息（错误状态）
				manager := GetManager()
				if manager != nil {
					manager.updateFFmpegStats(sm.name, receiverIndex, 0, false, err, lastBytes, time.Now())
				}

				// 检查是否应该继续
				if !sm.isRunning() {
					logger.LogPrintf("Stream %s receiver %d stopped after error", sm.name, receiverIndex)
					return
				}

				// 如果主URL失败且有backup_url且尚未尝试过backup_url，则切换到backup_url
				if !useBackup && sm.stream.Stream.Source.BackupURL != "" {
					logger.LogPrintf("Switching to backup URL for stream %s receiver %d", sm.name, receiverIndex)
					useBackup = true

					// 重新构建使用backup_url的命令
					backupCmd := sm.buildFFmpegCommandWithBackup(true)
					receivers := sm.stream.Stream.GetReceivers()
					if receiverIndex <= len(receivers) {
						cmd = receivers[receiverIndex-1].BuildFFmpegPushCommand(backupCmd, sm.streamKey)
						logger.LogPrintf("Rebuilt FFmpeg command with backup URL for %s receiver %d", sm.name, receiverIndex)
						logger.LogPrintf("New command: ffmpeg %s", strings.Join(cmd, " "))

						// 继续循环使用backup_url进行推流
						continue
					}
				}

				// 检查stream key是否过期
				sm.mutex.RLock()
				streamKey := sm.streamKey
				createdAt := sm.createdAt
				sm.mutex.RUnlock()

				logger.LogPrintf("Checking if stream key expired for %s (created at: %v)", sm.name, createdAt)
				if sm.stream.CheckStreamKeyExpiration(streamKey, createdAt) {
					logger.LogPrintf("Stream key for %s expired, generating new key", sm.name)
					// 更新stream key
					newStreamKey, err := sm.stream.UpdateStreamKey()
					if err != nil {
						logger.LogPrintf("Failed to generate new stream key for %s: %v", sm.name, err)
					} else {
						sm.mutex.Lock()
						oldKey := sm.streamKey
						sm.oldStreamKey = oldKey // 保存旧密钥
						sm.streamKey = newStreamKey
						sm.createdAt = time.Now()
						sm.mutex.Unlock()
						logger.LogPrintf("Generated new stream key for %s: %s (was: %s)", sm.name, newStreamKey, oldKey)

						// 回写配置到YAML文件
						logger.LogPrintf("Updating config file for %s with new stream key", sm.name)
						if err := sm.updateConfigFile(newStreamKey); err != nil {
							logger.LogPrintf("Failed to update config file for %s: %v", sm.name, err)
						} else {
							logger.LogPrintf("Successfully updated config file for %s", sm.name)
						}

						// 重新构建命令
						ffmpegCmd := sm.stream.BuildFFmpegCommand()
						// 如果之前使用的是backup_url，继续使用backup_url
						if useBackup {
							ffmpegCmd = sm.buildFFmpegCommandWithBackup(true)
						}
						receivers := sm.stream.Stream.GetReceivers()
						if receiverIndex <= len(receivers) {
							cmd = receivers[receiverIndex-1].BuildFFmpegPushCommand(ffmpegCmd, newStreamKey)
							logger.LogPrintf("Rebuilt FFmpeg command with new stream key for %s receiver %d", sm.name, receiverIndex)
							logger.LogPrintf("New command: ffmpeg %s", strings.Join(cmd, " "))
						} else {
							logger.LogPrintf("Receiver index %d out of range, cannot rebuild command", receiverIndex)
						}

						// 重置backup标志，以便在新的流密钥下可以重新尝试backup_url
						useBackup = false

						// 继续循环使用新密钥进行推流
						continue
					}
				} else {
					logger.LogPrintf("Stream key for %s not expired, will retry in 5 seconds", sm.name)
					// 重置backup标志，以便下次可以重新尝试backup_url
					useBackup = false
				}

				// 等待后重启
				select {
				case <-sm.ctx.Done():
					logger.LogPrintf("Stream %s receiver %d stopped during wait", sm.name, receiverIndex)
					return
				case <-time.After(5 * time.Second):
					// 继续循环重启
					logger.LogPrintf("Retrying stream %s receiver %d after 5 second delay", sm.name, receiverIndex)
				}
			} else {
				logger.LogPrintf("FFmpeg stream for %s receiver %d finished normally", sm.name, receiverIndex)
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
					logger.LogPrintf("Stream %s receiver %d stopped after normal finish", sm.name, receiverIndex)
					return
				case <-time.After(1 * time.Second):
					// 继续循环
					logger.LogPrintf("Restarting stream %s receiver %d after normal finish", sm.name, receiverIndex)
					// 重置backup标志，以便下次可以重新尝试backup_url
					useBackup = false
				}
			}
		}
	}
}

// buildFFmpegCommandWithBackup builds the ffmpeg command with primary/backup URL support
// This is a local implementation since the Stream method might not be accessible
func (sm *StreamManager) buildFFmpegCommandWithBackup(useBackup bool) []string {
	s := sm.stream

	// Start with base command - manually build it
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
	var headersBuilder strings.Builder
	hasHeaders := false

	if s.FFmpegOptions != nil && len(s.FFmpegOptions.Headers) > 0 {
		for _, header := range s.FFmpegOptions.Headers {
			headersBuilder.WriteString(header)
			headersBuilder.WriteString("\r\n")
			hasHeaders = true
		}
	} else if s.Stream.Source.Headers != nil && len(s.Stream.Source.Headers) > 0 {
		// 兼容旧的source.headers配置
		for key, value := range s.Stream.Source.Headers {
			headersBuilder.WriteString(key)
			headersBuilder.WriteString(": ")
			headersBuilder.WriteString(value)
			headersBuilder.WriteString("\r\n")
			hasHeaders = true
		}
	}

	if hasHeaders {
		cmd = append(cmd, "-headers", headersBuilder.String())
	}

	// Add source URL - 根据useBackup参数决定使用主URL还是备份URL
	if useBackup && s.Stream.Source.BackupURL != "" {
		cmd = append(cmd, "-i", s.Stream.Source.BackupURL)
		logger.LogPrintf("Stream %s: Using backup URL: %s", sm.name, s.Stream.Source.BackupURL)
	} else {
		cmd = append(cmd, "-i", s.Stream.Source.URL)
		logger.LogPrintf("Stream %s: Using primary URL: %s", sm.name, s.Stream.Source.URL)
	}

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
	videoBitrate := "4M"
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

	// Add custom arguments after input
	if s.FFmpegOptions != nil && len(s.FFmpegOptions.CustomArgs) > 0 {
		cmd = append(cmd, s.FFmpegOptions.CustomArgs...)
	}

	return cmd
}

// restartStreaming 重新启动推流
func (sm *StreamManager) restartStreaming() {
	logger.LogPrintf("Restarting streaming for %s", sm.name)

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

	logger.LogPrintf("Restarted streaming for %s", sm.name)
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

// Stop stops the stream
func (sm *StreamManager) Stop() {
	logger.LogPrintf("Stopping stream manager for %s", sm.name)

	sm.mutex.Lock()
	if !sm.running {
		sm.mutex.Unlock()
		logger.LogPrintf("Stream manager for %s already stopped", sm.name)
		return
	}
	sm.running = false
	sm.mutex.Unlock()

	// Cancel the context to stop all operations
	if sm.cancel != nil {
		sm.cancel()
	}

	// 停止管道转发器
	if sm.pipeForwarder != nil {
		sm.pipeForwarder.Stop()
	}

	logger.LogPrintf("Stream manager for %s stopped", sm.name)
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
	logger.LogPrintf("Attempting to update config file for stream %s with new key %s", sm.name, newStreamKey)

	// 读取当前配置文件
	configData, err := ioutil.ReadFile(*config.ConfigFilePath)
	if err != nil {
		logger.LogPrintf("Failed to read config file: %v", err)
		return err
	}

	// 解析YAML
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(configData, &cfg); err != nil {
		logger.LogPrintf("Failed to unmarshal config file: %v", err)
		return err
	}

	// 更新publisher配置中的stream key
	if publisher, ok := cfg["publisher"].(map[string]interface{}); ok {
		logger.LogPrintf("Found publisher config")
		if stream, ok := publisher[sm.name].(map[string]interface{}); ok {
			logger.LogPrintf("Found stream config for %s", sm.name)

			// 更新streamkey的value字段
			if streamKey, ok := stream["streamkey"].(map[string]interface{}); ok {
				oldValue := streamKey["value"]
				streamKey["value"] = newStreamKey
				// 更新创建时间
				streamKey["created_at"] = sm.createdAt.Format(time.RFC3339)
				logger.LogPrintf("Updated streamkey for %s: old_value=%v, new_value=%s", sm.name, oldValue, newStreamKey)
			} else {
				logger.LogPrintf("Failed to find streamkey config for %s", sm.name)
			}

			// 更新stream中的source headers
			if streamData, ok := stream["stream"].(map[string]interface{}); ok {
				logger.LogPrintf("Found stream data for %s", sm.name)

				// 更新local_play_urls，保持原有的布尔值配置
				if localPlayURLs, ok := streamData["local_play_urls"].(map[string]interface{}); ok {
					// 保持原有的flv和hls配置值（布尔值）
					// 不再需要修改这些值，因为它们已经是正确的布尔值
					logger.LogPrintf("local_play_urls config preserved: flv=%v, hls=%v",
						localPlayURLs["flv"], localPlayURLs["hls"])
				}
			} else {
				logger.LogPrintf("Failed to find stream data for %s", sm.name)
			}
		} else {
			logger.LogPrintf("Stream %s not found in publisher config", sm.name)
		}
	} else {
		logger.LogPrintf("Publisher config not found")
	}

	// 重新序列化为YAML
	logger.LogPrintf("Serializing updated config")
	newConfigData, err := yaml.Marshal(cfg)
	if err != nil {
		logger.LogPrintf("Failed to marshal updated config: %v", err)
		return err
	}

	// 写入文件
	logger.LogPrintf("Writing updated config to file: %s", *config.ConfigFilePath)
	if err := ioutil.WriteFile(*config.ConfigFilePath, newConfigData, 0644); err != nil {
		logger.LogPrintf("Failed to write config file: %v", err)
		return err
	}

	logger.LogPrintf("Successfully updated stream key in config file for stream %s", sm.name)
	return nil
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

// GetFFmpegStats 获取指定流和接收器的FFmpeg统计信息
func (m *Manager) GetFFmpegStats(streamName string, receiverIndex int) *FFmpegProcessStats {
	m.statsMutex.RLock()
	defer m.statsMutex.RUnlock()

	key := fmt.Sprintf("%s_%d", streamName, receiverIndex)
	if stat, exists := m.ffmpegStats[key]; exists {
		// 返回副本以避免并发问题
		statCopy := *stat
		return &statCopy
	}

	return nil
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
