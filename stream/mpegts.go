package stream

import (
	"context"
	"net/http"
	// "time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"

	"github.com/qist/tvgate/logger"
)

// HandleMpegtsStream 高并发优化版
func HandleMpegtsStream(
	ctx context.Context,
	w http.ResponseWriter,
	client *gortsplib.Client,
	videoMedia *description.Media,
	mpegtsFormat *format.MPEGTS,
	r *http.Request,
	rtspURL string,
	hub *StreamHubs,
	updateActive func(),
) error {

	// 创建客户端 ring buffer，支持自动扩容
	clientChan := NewOptimalStreamRingBuffer("video/mp2t", r.URL)
	hub.AddClient(clientChan)
	defer hub.RemoveClient(clientChan)

	// 决定是否启动 RTSP 播放
	shouldPlay := false
	hub.mu.Lock()
	if hub.isClosed {
		hub.mu.Unlock()
		clientChan.Close()
		return nil
	}
	if hub.state == StateStopped || hub.state == StateError {
		shouldPlay = true
		hub.state = StatePlaying
		hub.rtspClient = client
	}
	hub.mu.Unlock()

	// 异步启动 RTSP 播放
	if shouldPlay {
		go func() {
			var packetLossCount int64
			client.OnPacketRTP(videoMedia, mpegtsFormat, func(pkt *rtp.Packet) {
				// 丢包统计
				if pkt.Header.Marker {
					if packetLossCount > 0 {
						logger.LogPrintf("%d RTP packets lost", packetLossCount)
						packetLossCount = 0
					}
				} else {
					packetLossCount++
				}

				// 批量广播，Hub 内部对象池复用 byte slice
				hub.Broadcast(pkt.Payload)

				if updateActive != nil {
					updateActive()
				}
			})

			_, err := client.Play(nil)
			if err != nil {
				logger.LogPrintf("RTSP play error: %v", err)
				hub.SetError(err)
				return
			}

			hub.SetPlaying()
		}()
	} else {
		// 等待 hub 播放状态
		if !hub.WaitForPlaying(ctx) {
			return nil
		}
	}

	// 设置 HTTP 响应头
	w.Header().Set("Content-Type", "video/mp2t")
	flusher, hasFlusher := w.(http.Flusher)

	// 推流循环，支持高并发客户端
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// 获取数据
			pkt := clientChan.Pop()
			if pkt == nil {
				logger.LogPrintf("clientChan closed, ending connection")
				return nil
			}

			// 写入 HTTP 响应
			_, err := w.Write(pkt)
			if err != nil {
				logger.LogPrintf("Write error: %v", err)
				return err
			}

			// 如果支持 Flusher，立即 flush
			if hasFlusher {
				flusher.Flush()
			}

			// 更新客户端活跃状态
			if updateActive != nil {
				updateActive()
			}
		}
	}
}
