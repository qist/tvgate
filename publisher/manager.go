package publisher

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// Manager manages all publisher streams
type Manager struct {
	config *Config
	streams map[string]*StreamManager
	mutex   sync.RWMutex
}

// StreamManager manages a single stream
type StreamManager struct {
	name      string
	stream    *Stream
	running   bool
	cancel    context.CancelFunc
	ctx       context.Context
}

// NewManager creates a new publisher manager
func NewManager(config *Config) *Manager {
	log.Printf("Creating new publisher manager with %d streams", len(config.Streams))
	return &Manager{
		config:  config,
		streams: make(map[string]*StreamManager),
	}
}

// Start starts all enabled streams
func (m *Manager) Start() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	log.Printf("Starting publisher manager")

	if m.config.Streams == nil {
		log.Println("No streams configured")
		return nil
	}

	log.Printf("Found %d streams in config", len(m.config.Streams))

	for name, stream := range m.config.Streams {
		log.Printf("Processing stream: %s, enabled: %t", name, stream.Enabled)
		if !stream.Enabled {
			log.Printf("Stream %s is disabled, skipping\n", name)
			continue
		}

		// Create context for this stream
		ctx, cancel := context.WithCancel(context.Background())
		
		streamManager := &StreamManager{
			name:    name,
			stream:  stream,
			cancel:  cancel,
			ctx:     ctx,
		}

		m.streams[name] = streamManager
		go streamManager.startStreaming()
	}

	log.Printf("Publisher manager started with %d active streams", len(m.streams))
	return nil
}

// Stop stops all streams
func (m *Manager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	log.Printf("Stopping publisher manager with %d streams", len(m.streams))
	for _, streamManager := range m.streams {
		streamManager.stopStreaming()
	}
	log.Println("Publisher manager stopped")
}

// startStreaming starts the streaming process for a single stream
func (sm *StreamManager) startStreaming() {
	log.Printf("Starting stream manager for stream: %s", sm.name)
	sm.running = true
	
	streamKey, err := sm.stream.GenerateStreamKey()
	if err != nil {
		log.Printf("Failed to generate stream key for %s: %v\n", sm.name, err)
		return
	}

	log.Printf("Starting stream %s with key %s\n", sm.name, streamKey)
	
	// Build ffmpeg command
	ffmpegCmd := sm.stream.BuildFFmpegCommand()
	log.Printf("FFmpeg base command for stream %s: ffmpeg %s\n", sm.name, strings.Join(ffmpegCmd, " "))
	
	// For each receiver, build and execute the push command
	receivers := sm.stream.Stream.GetReceivers()
	log.Printf("Stream %s has %d receivers", sm.name, len(receivers))
	
	for i, receiver := range receivers {
		if !sm.running {
			break
		}
		
		// Build the full ffmpeg command for this receiver
		fullCmd := receiver.BuildFFmpegPushCommand(ffmpegCmd, streamKey)
		log.Printf("Full FFmpeg command for stream %s receiver %d: ffmpeg %s\n", sm.name, i+1, strings.Join(fullCmd, " "))
		
		// Start streaming in a separate goroutine
		go sm.runFFmpegStream(fullCmd, i+1)
	}
}

// runFFmpegStream runs the ffmpeg stream with restart capability
func (sm *StreamManager) runFFmpegStream(cmd []string, receiverIndex int) {
	log.Printf("Starting FFmpeg stream routine for %s receiver %d", sm.name, receiverIndex)
	
	for {
		select {
		case <-sm.ctx.Done():
			log.Printf("Stream %s receiver %d stopped\n", sm.name, receiverIndex)
			return
		default:
			log.Printf("Starting FFmpeg stream for %s receiver %d\n", sm.name, receiverIndex)
			
			// Execute FFmpeg
			err := sm.stream.ExecuteFFmpeg(sm.ctx, cmd)
			if err != nil {
				log.Printf("FFmpeg stream for %s receiver %d failed: %v\n", sm.name, receiverIndex, err)
				
				// Check if we should continue
				select {
				case <-sm.ctx.Done():
					log.Printf("Stream %s receiver %d stopped\n", sm.name, receiverIndex)
					return
				default:
					// Wait before restarting
					log.Printf("Restarting FFmpeg stream for %s receiver %d in 5 seconds\n", sm.name, receiverIndex)
					time.Sleep(5 * time.Second)
				}
			} else {
				log.Printf("FFmpeg stream for %s receiver %d finished normally\n", sm.name, receiverIndex)
				return
			}
		}
	}
}

// stopStreaming stops the streaming process
func (sm *StreamManager) stopStreaming() {
	log.Printf("Stopping stream manager for stream: %s", sm.name)
	if sm.running {
		log.Printf("Stopping stream %s\n", sm.name)
		sm.running = false
		if sm.cancel != nil {
			sm.cancel()
		}
	}
}