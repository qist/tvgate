package publisher

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
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
	// Remove the prefix to get the actual path
	path := strings.TrimPrefix(r.URL.Path, "/publisher")
	
	// Handle different paths
	if path == "/play" || path == "/play/" {
		h.handleListStreams(w, r)
		return
	}
	
	// Try to match a stream
	if h.handleStreamPlay(w, r, path) {
		return
	}
	
	// If no match, return 404
	http.NotFound(w, r)
}

// handleListStreams lists all streams
func (h *Handler) handleListStreams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	
	var html strings.Builder
	html.WriteString("<html><head><title>Publisher Streams</title></head><body>")
	html.WriteString("<h1>Available Streams</h1>")
	html.WriteString("<ul>")
	
	h.manager.mutex.RLock()
	for name, streamManager := range h.manager.streams {
		if streamManager.stream.Enabled {
			streamKey, _ := streamManager.stream.GenerateStreamKey()
			html.WriteString(fmt.Sprintf("<li><a href=\"/publisher/play/%s\">%s</a> (key: %s)</li>", name, name, streamKey))
		}
	}
	h.manager.mutex.RUnlock()
	
	html.WriteString("</ul>")
	html.WriteString("</body></html>")
	
	_, err := w.Write([]byte(html.String()))
	if err != nil {
		log.Printf("Error writing response: %v", err)
	}
}

// handleStreamPlay handles playing a specific stream
func (h *Handler) handleStreamPlay(w http.ResponseWriter, r *http.Request, path string) bool {
	// Extract stream name from path
	// Expected format: /play/{streamName} or /play/{streamName}/flv or /play/{streamName}/hls
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return false
	}
	
	streamName := parts[1]
	
	h.manager.mutex.RLock()
	streamManager, exists := h.manager.streams[streamName]
	h.manager.mutex.RUnlock()
	
	if !exists || !streamManager.stream.Enabled {
		return false
	}
	
	// Generate stream key
	streamKey, err := streamManager.stream.GenerateStreamKey()
	if err != nil {
		http.Error(w, "Failed to generate stream key", http.StatusInternalServerError)
		return true
	}
	
	// Determine the type of request
	if len(parts) >= 3 {
		switch parts[2] {
		case "flv":
			// Redirect to FLV stream URL
			flvURL := streamManager.stream.Stream.LocalPlayUrls.Flv
			if flvURL != "" {
				if !strings.HasSuffix(flvURL, "/") {
					flvURL = flvURL + "/"
				}
				http.Redirect(w, r, flvURL+streamKey+".flv", http.StatusFound)
				return true
			}
		case "hls":
			// Redirect to HLS stream URL
			hlsURL := streamManager.stream.Stream.LocalPlayUrls.Hls
			if hlsURL != "" {
				if !strings.HasSuffix(hlsURL, "/") {
					hlsURL = hlsURL + "/"
				}
				http.Redirect(w, r, hlsURL+streamKey+".m3u8", http.StatusFound)
				return true
			}
		}
	}
	
	// Default stream page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	
	var html strings.Builder
	html.WriteString("<html><head><title>Stream: " + streamName + "</title></head><body>")
	html.WriteString("<h1>Stream: " + streamName + "</h1>")
	html.WriteString("<p>Stream Key: " + streamKey + "</p>")
	
	// Add links to available formats
	flvURL := streamManager.stream.Stream.LocalPlayUrls.Flv
	hlsURL := streamManager.stream.Stream.LocalPlayUrls.Hls
	
	if flvURL != "" {
		html.WriteString(fmt.Sprintf("<p><a href=\"/publisher/play/%s/flv\">Play FLV</a></p>", streamName))
	}
	
	if hlsURL != "" {
		html.WriteString(fmt.Sprintf("<p><a href=\"/publisher/play/%s/hls\">Play HLS</a></p>", streamName))
	}
	
	html.WriteString("</body></html>")
	
	_, err = w.Write([]byte(html.String()))
	if err != nil {
		log.Printf("Error writing response: %v", err)
	}
	
	return true
}

// MatchPath checks if the given path matches any configured stream paths
func (h *Handler) MatchPath(requestPath string) (string, string, bool) {
	// Check if the request path starts with the publisher path
	if !strings.HasPrefix(requestPath, "/live") {
		return "", "", false
	}
	
	// Extract the stream key from the path
	// Expected format: /live/{streamKey} or /live/{streamKey}.flv or /live/{streamKey}.m3u8
	trimmedPath := strings.TrimPrefix(requestPath, "/live")
	trimmedPath = strings.TrimPrefix(trimmedPath, "/")
	
	// Handle different formats
	ext := filepath.Ext(trimmedPath)
	streamKey := strings.TrimSuffix(trimmedPath, ext)
	
	// Look for a stream with this key
	h.manager.mutex.RLock()
	defer h.manager.mutex.RUnlock()
	
	for name, streamManager := range h.manager.streams {
		if !streamManager.stream.Enabled {
			continue
		}
		
		generatedKey, err := streamManager.stream.GenerateStreamKey()
		if err != nil {
			continue
		}
		
		if generatedKey == streamKey {
			return name, ext, true
		}
	}
	
	return "", "", false
}