package publisher

import (
	"net/http"
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
				// 对于.m3u8文件，去除后缀即为流ID
				// 支持格式如: cctv2/index.m3u8 -> cctv2
				streamID = strings.TrimSuffix(streamPath, ".m3u8")
				if strings.Contains(streamID, "/") {
					streamID = strings.Split(streamID, "/")[0]
				}
			} else { // .ts
				// 对于.ts文件，使用路径中的目录名作为流ID
				// 支持格式如: cctv2/xxx.ts -> cctv2
				if strings.Contains(streamPath, "/") {
					streamID = strings.Split(streamPath, "/")[0]
				} else {
					// 文件名形如 <流名>_<seq>.ts 或 <流名>-<strftime>.ts（模板不同分隔符不同），
					// 按已配置的流名做最长前缀匹配；查不到再按分隔符兜底拆分
					base := strings.TrimSuffix(streamPath, ".ts")
					streamID = matchStreamIDByPrefix(base)
				}
			}

			// 获取流管理器并提供HLS服务
			streamHub := GetStreamHub(streamID)
			if streamHub != nil {
				streamHub.ServeHLS(w, r)
				return
			}

			// 如果没有找到StreamHub，尝试直接查找PipeForwarder
			manager := GetManager()
			if manager != nil {
				manager.mutex.RLock()
				streamManager, exists := manager.streams[streamID]
				manager.mutex.RUnlock()

				if exists && streamManager != nil && streamManager.pipeForwarder != nil {
					streamManager.pipeForwarder.ServeHLS(w, r)
					return
				}
			}

			// 分片文件名不含流名的模板（epoch_hls/epoch_dash/numeric/date_* 等
			// strftime 格式）：无法从文件名推断流，遍历各流 HLS 目录反查
			segmentName := streamPath
			if idx := strings.LastIndex(streamPath, "/"); idx >= 0 {
				segmentName = streamPath[idx+1:]
			}
			if streamHubManager.ServeSegmentByName(w, r, segmentName) {
				return
			}

			http.Error(w, "Stream not found", http.StatusNotFound)
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

// matchStreamIDByPrefix 按已配置的流名对 TS 分片文件名做最长前缀匹配。
// 分片文件名格式随 ts_filename_template 不同而不同（如 <name>_295.ts、
// <name>-20260904-134845-hls.ts），分隔符不固定，不能简单按某个分隔符拆。
// 匹配不到时回退到按 '_' 拆分（兼容旧逻辑）。
func matchStreamIDByPrefix(base string) string {
	manager := GetManager()
	if manager != nil {
		manager.mutex.RLock()
		best := ""
		for name := range manager.streams {
			if base == name || strings.HasPrefix(base, name+"_") || strings.HasPrefix(base, name+"-") {
				if len(name) > len(best) {
					best = name
				}
			}
		}
		manager.mutex.RUnlock()
		if best != "" {
			return best
		}
	}

	// 兜底：按 '_' 拆（旧 name_index 模板 <name>_<seq>.ts）
	if idx := strings.Index(base, "_"); idx > 0 {
		return base[:idx]
	}
	return base
}
