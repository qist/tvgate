package buffer

import (
	"github.com/qist/tvgate/config"
	"strings"
)

func GetBuffer(size int) []byte {
	if pool, ok := config.BufferPools[size]; ok {
		return pool.Get().([]byte)
	}
	return make([]byte, size)
}

func PutBuffer(size int, buf []byte) {
	if pool, ok := config.BufferPools[size]; ok {
		pool.Put(buf)
	}
}

func GetOptimalBufferSize(contentType, urlPath string) int {
	urlPath = strings.ToLower(urlPath)
	contentType = strings.ToLower(contentType)

	switch {
	case strings.HasSuffix(urlPath, ".ts"), strings.HasSuffix(urlPath, ".flv"):
		return 256 * 1024
	case strings.HasSuffix(urlPath, ".mp4"), strings.HasSuffix(urlPath, ".mkv"),
		strings.HasSuffix(urlPath, ".avi"), strings.HasSuffix(urlPath, ".mov"),
		strings.HasSuffix(urlPath, ".webm"):
		return 512 * 1024
	case strings.HasSuffix(urlPath, ".mp3"), strings.HasSuffix(urlPath, ".aac"),
		strings.HasSuffix(urlPath, ".ogg"), strings.HasSuffix(urlPath, ".wav"):
		return 64 * 1024
	case strings.HasSuffix(urlPath, ".m3u8"):
		return 16 * 1024
	case strings.Contains(urlPath, "/udp/"), strings.Contains(urlPath, "/rtp/"):
		return 16 * 1024
	case strings.HasSuffix(urlPath, ".zip"), strings.HasSuffix(urlPath, ".tar"),
		strings.HasSuffix(urlPath, ".7z"):
		return 512 * 1024
	}

	switch {
	case strings.Contains(contentType, "video/"):
		return 256 * 1024
	case strings.Contains(contentType, "audio/"):
		return 64 * 1024
	case strings.Contains(contentType, "application/octet-stream"):
		return 128 * 1024
	default:
		return 64 * 1024
	}
}
