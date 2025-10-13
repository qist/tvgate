package publisher

import (
	"net/http"
	"regexp"
	"strings"
)

// Handler handles HTTP requests for the publisher
type Handler struct {
	manager *Manager
}

// NewHandler creates a new publisher handler
func NewHandler(manager *Manager) *Handler {
	return &Handler{
		manager: manager,
	}
}

// ServeHTTP handles HTTP requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	// Handle different paths
	switch {
	case strings.HasPrefix(path, "play/"):
		// 提取流ID并提供流服务
		streamPath := strings.TrimPrefix(path, "play/")

		if streamPath == "" {
			http.Error(w, "Stream ID is required", http.StatusBadRequest)
			return
		}

		// 检查是否是HLS请求（以.m3u8或.ts结尾）
		if strings.HasSuffix(streamPath, ".m3u8") || strings.HasSuffix(streamPath, ".ts") {
			// 从路径中提取流名称（去掉.m3u8或.ts后缀）
			var streamID string
			if strings.HasSuffix(streamPath, ".m3u8") {
				streamID = strings.TrimSuffix(streamPath, ".m3u8")
			} else { // .ts
				// 对于.ts文件，需要提取流名称部分
				// 使用正则表达式匹配 "stream_11.ts" 格式，提取 "stream" 部分
				// 匹配模式: 以任意字符开头，后跟下划线和数字，以.ts结尾
				re := regexp.MustCompile(`^(.+)_[0-9]+\.ts$`)
				matches := re.FindStringSubmatch(streamPath)
				if len(matches) >= 2 {
					streamID = matches[1]
				} else {
					// 如果不匹配模式，则使用原来的简单方式
					parts := strings.Split(strings.TrimSuffix(streamPath, ".ts"), "_")
					if len(parts) > 0 {
						streamID = parts[0]
					}
				}
			}

			// 查找流管理器
			h.manager.mutex.RLock()
			streamManager, exists := h.manager.streams[streamID]
			h.manager.mutex.RUnlock()

			if !exists {
				http.Error(w, "Stream not found", http.StatusNotFound)
				return
			}

			// 提供HLS流服务
			if streamManager.pipeForwarder != nil {
				streamManager.pipeForwarder.ServeHLS(w, r)
			} else {
				http.Error(w, "HLS not available", http.StatusServiceUnavailable)
			}
			return
		}

		// 对于非HLS请求，使用流ID查找流管理器
		streamID := streamPath
		if strings.Contains(streamPath, ".") {
			parts := strings.Split(streamPath, ".")
			streamID = parts[0]
		}

		// 查找流管理器
		h.manager.mutex.RLock()
		streamManager, exists := h.manager.streams[streamID]
		h.manager.mutex.RUnlock()

		if !exists {
			http.Error(w, "Stream not found", http.StatusNotFound)
			return
		}

		// 提供FLV流服务（默认）
		streamManager.stream.ServeFLV(w, r, streamManager.name, streamManager.GetStreamKey())
	default:
		http.NotFound(w, r)
	}
}
