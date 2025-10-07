package publisher

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
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
	case path == "" || path == "index.html":
		h.serveIndex(w, r)
	case strings.HasPrefix(path, "play/"):
		// 提取流ID并提供FLV流服务
		streamID := strings.TrimPrefix(path, "play/")
		if streamID == "" {
			http.Error(w, "Stream ID is required", http.StatusBadRequest)
			return
		}
		
		// 查找流管理器
		h.manager.mutex.RLock()
		streamManager, exists := h.manager.streams[streamID]
		h.manager.mutex.RUnlock()
		
		if !exists {
			http.Error(w, "Stream not found", http.StatusNotFound)
			return
		}
		
		// 检查管道转发器是否存在
		if streamManager.pipeForwarder == nil {
			http.Error(w, "FLV streaming not available for this stream", http.StatusNotFound)
			return
		}
		
		// 提供FLV流服务
		streamManager.pipeForwarder.ServeFLV(w, r)
	case strings.HasPrefix(path, "api/"):
		h.serveAPI(w, r, path)
	default:
		http.NotFound(w, r)
	}
}

// serveIndex serves the index page
func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	// Get stream information
	streams := make(map[string]interface{})
	
	h.manager.mutex.RLock()
	for name, streamManager := range h.manager.streams {
		stream := streamManager.stream
		streamKey := streamManager.GetStreamKey()
		
		// 构建本地播放URL
		localPlayURLs := make(map[string]string)
		if stream.Stream.LocalPlayUrls.Flv != "" {
			localPlayURLs["flv"] = stream.BuildLocalPlayURL(stream.Stream.LocalPlayUrls.Flv, streamKey, "flv")
		}
		if stream.Stream.LocalPlayUrls.Hls != "" {
			localPlayURLs["hls"] = stream.BuildLocalPlayURL(stream.Stream.LocalPlayUrls.Hls, streamKey, "hls")
		}
		
		// 构建接收端播放URL
		receiverPlayURLs := make(map[string]map[string]string)
		receivers := stream.Stream.GetReceivers()
		for i, receiver := range receivers {
			receiverPlayURLs[fmt.Sprintf("receiver_%d", i+1)] = make(map[string]string)
			if receiver.PlayUrls.Flv != "" {
				receiverPlayURLs[fmt.Sprintf("receiver_%d", i+1)]["flv"] = receiver.BuildReceiverPlayURL(receiver.PlayUrls.Flv, streamKey, "flv")
			}
			if receiver.PlayUrls.Hls != "" {
				receiverPlayURLs[fmt.Sprintf("receiver_%d", i+1)]["hls"] = receiver.BuildReceiverPlayURL(receiver.PlayUrls.Hls, streamKey, "hls")
			}
		}
		
		streams[name] = map[string]interface{}{
			"streamKey":        streamKey,
			"localPlayURLs":    localPlayURLs,
			"receiverPlayURLs": receiverPlayURLs,
			"enabled":          stream.Enabled,
			"protocol":         stream.Protocol,
		}
	}
	h.manager.mutex.RUnlock()
	
	// Render the index page
	data := map[string]interface{}{
		"streams": streams,
	}
	
	// Simple HTML template
	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <title>TVGate Publisher</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .stream { border: 1px solid #ccc; margin: 10px 0; padding: 10px; }
        .stream-name { font-weight: bold; font-size: 1.2em; }
        .urls { margin: 10px 0; }
        .url { margin: 5px 0; }
    </style>
</head>
<body>
    <h1>TVGate Publisher</h1>
    {{range $name, $stream := .streams}}
    <div class="stream">
        <div class="stream-name">{{$name}}</div>
        <div>Stream Key: {{$stream.streamKey}}</div>
        <div>Enabled: {{$stream.enabled}}</div>
        <div>Protocol: {{$stream.protocol}}</div>
        
        <div class="urls">
            <h3>Local Play URLs:</h3>
            {{range $protocol, $url := $stream.localPlayURLs}}
            <div class="url">{{$protocol}}: <a href="{{$url}}">{{$url}}</a></div>
            {{end}}
        </div>
        
        <div class="urls">
            <h3>Receiver Play URLs:</h3>
            {{range $receiver, $urls := $stream.receiverPlayURLs}}
            <div>
                <strong>{{$receiver}}:</strong>
                {{range $protocol, $url := $urls}}
                <div class="url">{{$protocol}}: <a href="{{$url}}">{{$url}}</a></div>
                {{end}}
            </div>
            {{end}}
        </div>
    </div>
    {{else}}
    <p>No streams configured</p>
    {{end}}
</body>
</html>
`
	
	t, err := template.New("index").Parse(tmpl)
	if err != nil {
		http.Error(w, "Failed to parse template", http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		log.Printf("Failed to execute template: %v", err)
	}
}

// servePlay serves the play page
func (h *Handler) servePlay(w http.ResponseWriter, r *http.Request, path string) {
	// Extract stream ID
	streamID := strings.TrimPrefix(path, "play/")
	if streamID == "" {
		http.Error(w, "Stream ID is required", http.StatusBadRequest)
		return
	}
	
	// Find the stream
	h.manager.mutex.RLock()
	streamManager, exists := h.manager.streams[streamID]
	h.manager.mutex.RUnlock()
	
	if !exists {
		http.Error(w, "Stream not found", http.StatusNotFound)
		return
	}
	
	// Get stream key
	streamKey := streamManager.GetStreamKey()
	stream := streamManager.stream
	
	// 构建播放URL
	playURLs := make(map[string]string)
	if stream.Stream.LocalPlayUrls.Flv != "" {
		playURLs["flv"] = stream.BuildLocalPlayURL(stream.Stream.LocalPlayUrls.Flv, streamKey, "flv")
	}
	if stream.Stream.LocalPlayUrls.Hls != "" {
		playURLs["hls"] = stream.BuildLocalPlayURL(stream.Stream.LocalPlayUrls.Hls, streamKey, "hls")
	}
	
	// Simple play page
	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <title>Play Stream - {{.streamID}}</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .player { margin: 20px 0; }
        .url { margin: 10px 0; }
    </style>
</head>
<body>
    <h1>Play Stream: {{.streamID}}</h1>
    <div>Stream Key: {{.streamKey}}</div>
    
    {{range $protocol, $url := .playURLs}}
    <div class="player">
        <h2>{{$protocol | ToUpper}} Player</h2>
        <div class="url"><a href="{{$url}}">{{$url}}</a></div>
        {{if eq $protocol "flv"}}
        <video controls width="640" height="360">
            <source src="{{$url}}" type="video/x-flv">
            Your browser does not support FLV video.
        </video>
        {{else if eq $protocol "hls"}}
        <video controls width="640" height="360">
            <source src="{{$url}}" type="application/x-mpegURL">
            Your browser does not support HLS video.
        </video>
        {{end}}
    </div>
    {{end}}
</body>
</html>
`
	
	t, err := template.New("play").Parse(tmpl)
	if err != nil {
		http.Error(w, "Failed to parse template", http.StatusInternalServerError)
		return
	}
	
	data := map[string]interface{}{
		"streamID":  streamID,
		"streamKey": streamKey,
		"playURLs":  playURLs,
	}
	
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		log.Printf("Failed to execute template: %v", err)
	}
}

// serveAPI serves the API endpoints
func (h *Handler) serveAPI(w http.ResponseWriter, r *http.Request, path string) {
	switch strings.TrimPrefix(path, "api/") {
	case "streams":
		h.serveStreamsAPI(w, r)
	default:
		http.Error(w, "API endpoint not found", http.StatusNotFound)
	}
}

// serveStreamsAPI serves the streams API
func (h *Handler) serveStreamsAPI(w http.ResponseWriter, r *http.Request) {
	streams := make(map[string]interface{})
	
	h.manager.mutex.RLock()
	for name, streamManager := range h.manager.streams {
		stream := streamManager.stream
		streamKey := streamManager.GetStreamKey()
		
		// 构建本地播放URL
		localPlayURLs := make(map[string]string)
		if stream.Stream.LocalPlayUrls.Flv != "" {
			localPlayURLs["flv"] = stream.BuildLocalPlayURL(stream.Stream.LocalPlayUrls.Flv, streamKey, "flv")
		}
		if stream.Stream.LocalPlayUrls.Hls != "" {
			localPlayURLs["hls"] = stream.BuildLocalPlayURL(stream.Stream.LocalPlayUrls.Hls, streamKey, "hls")
		}
		
		// 构建接收端播放URL
		receiverPlayURLs := make(map[string]map[string]string)
		receivers := stream.Stream.GetReceivers()
		for i, receiver := range receivers {
			receiverPlayURLs[fmt.Sprintf("receiver_%d", i+1)] = make(map[string]string)
			if receiver.PlayUrls.Flv != "" {
				receiverPlayURLs[fmt.Sprintf("receiver_%d", i+1)]["flv"] = receiver.BuildReceiverPlayURL(receiver.PlayUrls.Flv, streamKey, "flv")
			}
			if receiver.PlayUrls.Hls != "" {
				receiverPlayURLs[fmt.Sprintf("receiver_%d", i+1)]["hls"] = receiver.BuildReceiverPlayURL(receiver.PlayUrls.Hls, streamKey, "hls")
			}
		}
		
		streams[name] = map[string]interface{}{
			"streamKey":        streamKey,
			"localPlayURLs":    localPlayURLs,
			"receiverPlayURLs": receiverPlayURLs,
			"enabled":          stream.Enabled,
			"protocol":         stream.Protocol,
		}
	}
	h.manager.mutex.RUnlock()
	
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(streams); err != nil {
		log.Printf("Failed to encode JSON: %v", err)
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
}