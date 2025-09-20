package stream

import (
	"context"
	"net/http"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"

	"github.com/qist/tvgate/logger"
)

// HandleMpegtsStream 处理 MPEG-TS HTTP 推流客户端
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

	// 创建客户端 ring buffer
	clientChan := NewOptimalStreamRingBuffer("video/mp2t", r.URL)
	hub.AddClient(clientChan)
	defer hub.RemoveClient(clientChan)

	// 决定是否需要启动播放
	shouldPlay := false
	hub.mu.Lock()
	if hub.isClosed {
		hub.mu.Unlock()
		clientChan.Close()
		return nil
	}

	if hub.state == 0 || hub.state == 2 {
		shouldPlay = true
		hub.state = 1
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
		if !hub.WaitForPlaying(ctx) {
			return nil
		}
	}

	// 设置 HTTP 响应头
	w.Header().Set("Content-Type", "video/mp2t")
	if flusher, ok := w.(http.Flusher); ok {
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
				pkt := clientChan.Pop()
				if pkt == nil {
					logger.LogPrintf("clientChan closed, ending connection")
					return nil
				}
				_, err := w.Write(pkt)
				if err != nil {
					logger.LogPrintf("Write error: %v", err)
					return err
				}
				flusher.Flush()
				if updateActive != nil {
					updateActive()
				}
			}
		}
	} else {
		// 如果不支持 Flusher
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
				pkt := clientChan.Pop()
				if pkt == nil {
					logger.LogPrintf("clientChan closed, ending connection")
					return nil
				}
				_, err := w.Write(pkt)
				if err != nil {
					logger.LogPrintf("Write error: %v", err)
					return err
				}
				if updateActive != nil {
					updateActive()
				}
			}
		}
	}
}
