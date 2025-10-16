package publisher

import (
	"context"
	"fmt"
	"os"
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
	streamStarted   bool                       // 标记流是否已经启动
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
			streamStarted:   false, // 初始化流启动状态为false
			ffmpegProcesses: make(map[int]*FFmpegProcessInfo),
		}

		// 如果启用了pipe forwarder，则创建并启动它
		if stream.PipeForwarder != nil && stream.PipeForwarder.enabled {
			logger.LogPrintf("Starting pipe forwarder for stream %s", name)

			// 创建PipeForwarder实例
			streamManager.pipeForwarder = NewPipeForwarder(
				streamManager.name,
				stream.PipeForwarder.sourceURL,
				stream.PipeForwarder.backupURL,
				stream.PipeForwarder.hlsEnabled,
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

		// 启动流 - 在初始化时总是启动流，避免配置更新时重复启动
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
	logger.LogPrintf("Publisher manager stopped")
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
	// 基本配置变化
	if oldStream.Enabled != newStream.Enabled || oldStream.Protocol != newStream.Protocol {
		return true
	}

	// 源 URL 变化
	if oldStream.Stream.Source.URL != newStream.Stream.Source.URL ||
		oldStream.Stream.Source.BackupURL != newStream.Stream.Source.BackupURL {
		return true
	}

	// 接收者变化
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

	// 检查 LocalPlayUrls 变化
	if len(oldStream.Stream.LocalPlayUrls) != len(newStream.Stream.LocalPlayUrls) {
		return true
	}
	for i, oldOutput := range oldStream.Stream.LocalPlayUrls {
		// 检查协议和启用状态变化
		if i >= len(newStream.Stream.LocalPlayUrls) {
			return true
		}
		newOutput := newStream.Stream.LocalPlayUrls[i]
		if oldOutput.Protocol != newOutput.Protocol || oldOutput.Enabled != newOutput.Enabled {
			return true
		}
		// 可选：检查 FFmpegOptions 变化（根据需要判断关键字段）
		// 根据协议类型检查对应的FFmpegOptions
		var oldOptions, newOptions *FFmpegOptions
		switch oldOutput.Protocol {
		case "flv":
			oldOptions = oldOutput.FlvFFmpegOptions
			newOptions = newOutput.FlvFFmpegOptions
		case "hls":
			oldOptions = oldOutput.HlsFFmpegOptions
			newOptions = newOutput.HlsFFmpegOptions
		}
		
		if !ffmpegOptionsEqual(oldOptions, newOptions) {
			return true
		}
	}

	// StreamKey 变化
	if oldStream.StreamKey.Type != newStream.StreamKey.Type ||
		oldStream.StreamKey.Value != newStream.StreamKey.Value ||
		oldStream.StreamKey.Length != newStream.StreamKey.Length ||
		oldStream.StreamKey.Expiration != newStream.StreamKey.Expiration {
		return true
	}

	return false
}

// 判断 FFmpegOptions 是否相等（可根据实际字段扩展）
func ffmpegOptionsEqual(a, b *FFmpegOptions) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.OutputFormat != b.OutputFormat {
		return false
	}
	if len(a.OutputPreArgs) != len(b.OutputPreArgs) || len(a.OutputPostArgs) != len(b.OutputPostArgs) {
		return false
	}
	for i := range a.OutputPreArgs {
		if a.OutputPreArgs[i] != b.OutputPreArgs[i] {
			return false
		}
	}
	for i := range a.OutputPostArgs {
		if a.OutputPostArgs[i] != b.OutputPostArgs[i] {
			return false
		}
	}
	// 可继续扩展其他关键字段
	return true
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
		// 等待一段时间确保流完全停止
		time.Sleep(500 * time.Millisecond)
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
		streamStarted:   false, // 初始化为false
		ffmpegProcesses: make(map[int]*FFmpegProcessInfo),
	}

	m.streams[name] = streamManager

	// 启动流 - 在配置更新时总是启动流
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

// UpdateConfig updates the manager configuration
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
					delete(m.streams, name)
					// 等待一段时间确保流完全停止
					time.Sleep(1 * time.Second)
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
    sm.mutex.Lock()
    if sm.streamStarted {
        sm.mutex.Unlock()
        logger.LogPrintf("Stream %s already started, skipping", sm.name)
        return
    }
    sm.streamStarted = true
    sm.mutex.Unlock()

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
    enableFLV, enableHLS := false, false
    for _, output := range sm.stream.Stream.LocalPlayUrls {
        if output.Protocol == "flv" && output.Enabled {
            enableFLV = true
        }
        if output.Protocol == "hls" && output.Enabled {
            enableHLS = true
        }
    }

    // 如果启用了本地播放，则启动管道转发器
    if enableFLV || enableHLS {
        // 创建PipeForwarder，传入源URL和备份URL
        sm.pipeForwarder = NewPipeForwarder(
            sm.name,
            sm.stream.Stream.Source.URL,
            sm.stream.Stream.Source.BackupURL,
            enableHLS,
        )
        
        // 启动管道转发器
        go func() {
            if err := sm.pipeForwarder.Start(ffmpegCmd); err != nil {
                logger.LogPrintf("Failed to start pipe forwarder for stream %s: %v", sm.name, err)
            }
        }()
        
        // 如果使用PipeForwarder，则不启动传统的推流方式
        return
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

            // 为每个接收者构建基础命令，使用接收者的FFmpeg选项
            baseCmd := sm.stream.BuildFFmpegCommandForReceiver(&r)
            cmd := r.BuildFFmpegPushCommand(baseCmd, currentStreamKey)
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

	// 获取合并后的FFmpegOptions（如果在receiver上下文中）
	// 注意：这个方法在StreamManager中调用，我们可能需要使用特定的FFmpegOptions
	var ffmpegOptions *FFmpegOptions
	
	// 优先使用第一个接收器的FFmpegOptions，如果没有接收器则使用源的FFmpegOptions
	receivers := s.Stream.GetReceivers()
	if len(receivers) > 0 && receivers[0].FFmpegOptions != nil {
		ffmpegOptions = receivers[0].FFmpegOptions
		logger.LogPrintf("Using receiver's FFmpeg options for stream %s", sm.name)
	} else {
		// 默认使用源FFmpegOptions
		ffmpegOptions = s.Stream.Source.FFmpegOptions
		logger.LogPrintf("Using source's FFmpeg options for stream %s", sm.name)
	}

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

	// Add custom headers
	hasHeaders := false
	var headersBuilder strings.Builder
	if ffmpegOptions != nil && len(ffmpegOptions.Headers) > 0 {
		hasHeaders = true
		for i, h := range ffmpegOptions.Headers {
			if i > 0 {
				headersBuilder.WriteString("\r\n")
			}
			headersBuilder.WriteString(h)
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

	// Remove duplicate flags to prevent duplication
	cmd = RemoveDuplicateFlagArgs(cmd)

	return cmd
}

// restartStreaming 重新启动推流
func (sm *StreamManager) restartStreaming() {
	logger.LogPrintf("Restarting streaming for %s", sm.name)

	// 先停止当前推流
	sm.Stop()

	// 等待一段时间确保旧的推流完全停止
	time.Sleep(1 * time.Second)

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

	// 等待一小段时间确保所有goroutine都已退出
	time.Sleep(100 * time.Millisecond)

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
	configData, err := os.ReadFile(*config.ConfigFilePath)
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
	if err := os.WriteFile(*config.ConfigFilePath, newConfigData, 0644); err != nil {
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
