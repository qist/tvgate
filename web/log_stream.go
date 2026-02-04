package web

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// handleLogViewer renders the realtime log viewer page.
func (h *ConfigHandler) handleLogViewer(w http.ResponseWriter, r *http.Request) {
	webPath := h.getWebPath()
	data := map[string]interface{}{
		"title":   "TVGate 实时日志",
		"webPath": webPath,
	}
	if err := h.renderTemplate(w, r, "log_viewer", "templates/log_viewer.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleLogStream streams logs via Server-Sent Events.
func (h *ConfigHandler) handleLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	config.CfgMu.RLock()
	logCfg := config.Cfg.Log
	config.CfgMu.RUnlock()

	if !logCfg.Enabled {
		writeSSEEvent(w, "status", "日志已关闭")
		flusher.Flush()
		return
	}

	if strings.TrimSpace(logCfg.File) != "" {
		if err := streamFileLogs(r.Context(), w, flusher, logCfg.File); err != nil {
			writeSSEEvent(w, "status", err.Error())
			flusher.Flush()
		}
		return
	}

	streamMemoryLogs(r.Context(), w, flusher)
}

func streamMemoryLogs(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) {
	lines := logger.GetBufferSnapshot()
	for _, line := range lines {
		writeSSEData(w, line)
	}
	flusher.Flush()

	ch, cancel := logger.Subscribe()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			writeSSEData(w, line)
			flusher.Flush()
		}
	}
}

func streamFileLogs(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("日志文件无法打开: %v", err)
	}
	defer file.Close()

	lines, offset, err := readTailLines(file, 200, 64*1024)
	if err != nil {
		return fmt.Errorf("读取日志失败: %v", err)
	}
	for _, line := range lines {
		writeSSEData(w, line)
	}
	flusher.Flush()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	pending := ""
	reader := bufio.NewReader(file)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			stat, err := file.Stat()
			if err != nil {
				return fmt.Errorf("读取日志状态失败: %v", err)
			}

			if stat.Size() < offset {
				offset = 0
				pending = ""
			}
			if stat.Size() == offset {
				continue
			}

			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				return fmt.Errorf("定位日志失败: %v", err)
			}
			reader.Reset(file)

			for {
				buf := make([]byte, 32*1024)
				n, readErr := reader.Read(buf)
				if n > 0 {
					offset += int64(n)
					pending = emitLines(w, flusher, pending+string(buf[:n]))
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					return fmt.Errorf("读取日志失败: %v", readErr)
				}
			}
		}
	}
}

func readTailLines(file *os.File, maxLines int, maxBytes int64) ([]string, int64, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := stat.Size()
	offset := int64(0)
	if size > maxBytes {
		offset = size - maxBytes
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return normalizeLines(lines), size, nil
}

func emitLines(w http.ResponseWriter, flusher http.Flusher, content string) string {
	if content == "" {
		return ""
	}
	parts := strings.Split(content, "\n")
	if len(parts) == 1 {
		return content
	}
	for i := 0; i < len(parts)-1; i++ {
		writeSSEData(w, parts[i])
	}
	flusher.Flush()
	return parts[len(parts)-1]
}

func normalizeLines(lines []string) []string {
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, "\r")
	}
	return lines
}

func writeSSEData(w http.ResponseWriter, line string) {
	if line == "" {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", strings.TrimRight(line, "\r"))
}

func writeSSEEvent(w http.ResponseWriter, event, message string) {
	fmt.Fprintf(w, "event: %s\n", event)
	fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(message, "\n", " "))
}
