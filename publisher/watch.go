package publisher

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/config/load"
)

// WatchConfigFile 监控配置文件变化并重新加载publisher配置
func WatchConfigFile(configPath string) {
	if configPath == "" {
		return
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		log.Printf("Failed to get absolute path for config file: %v", err)
		return
	}

	// 创建文件监控器
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("Failed to create file watcher: %v", err)
		return
	}
	defer watcher.Close()

	// 添加配置文件到监控
	err = watcher.Add(absPath)
	if err != nil {
		log.Printf("Failed to add config file to watcher: %v", err)
		return
	}

	log.Printf("Started watching config file: %s", absPath)

	// 获取初始修改时间
	fileInfo, err := os.Stat(absPath)
	var lastModifiedTime time.Time
	if err == nil {
		lastModifiedTime = fileInfo.ModTime()
	} else {
		lastModifiedTime = time.Now()
		log.Printf("Failed to get initial file info, using current time: %v", err)
	}

	// 监控循环
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			
			// 只关注写入事件
			if event.Op&fsnotify.Write == fsnotify.Write {
				// 防抖处理
				time.Sleep(100 * time.Millisecond)
				
				// 检查文件修改时间
				info, err := os.Stat(absPath)
				if err != nil {
					log.Printf("Failed to get file info: %v", err)
					continue
				}
				
				// 确保是新的修改
				if !info.ModTime().After(lastModifiedTime) {
					continue
				}
				
				lastModifiedTime = info.ModTime()
				log.Printf("Config file updated: %s", event.Name)
				
				// 重新加载配置
				if err := load.LoadConfig(configPath); err != nil {
					log.Printf("Failed to reload config: %v", err)
					continue
				}
				
				// 更新publisher配置
				UpdatePublisherConfig()
			}
			
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("File watcher error: %v", err)
		}
	}
}

// UpdatePublisherConfig 更新publisher配置
func UpdatePublisherConfig() {
	manager := GetManager()
	if manager == nil {
		log.Printf("Publisher manager not initialized")
		return
	}
	
	// 转换新的配置
	newConfig := convertConfig(config.Cfg.Publisher)
	if newConfig == nil {
		log.Printf("Failed to convert new config")
		return
	}
	
	log.Printf("Updating publisher config with %d streams", len(newConfig.Streams))
	
	// 更新manager配置
	manager.UpdateConfig(newConfig)
}