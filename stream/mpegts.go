package stream

import (
	"context"
	"net/http"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
	// "github.com/qist/tvgate/monitor"
)

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

	clientChan, err := ringbuffer.New(2048)
	if err != nil {
		return err
	}

	hub.AddClient(clientChan)

	// 检查是否需要启动播放或者恢复播放
	shouldPlay := false

	// 等待获取锁以检查当前状态
	hub.mu.Lock()
	if hub.isClosed {
		hub.mu.Unlock()
		clientChan.Close()
		return nil
	}

	// 判断是否需要启动播放
	switch hub.state {
	case StateStopped: // stopped
		shouldPlay = true
		hub.state = StatePlaying
		hub.rtspClient = client

	case StateError: // error
		shouldPlay = true
		hub.state = StatePlaying
		hub.lastError = nil
		hub.rtspClient = client
	}
	hub.mu.Unlock()

	defer func() {
		hub.RemoveClient(clientChan)

		// 检查是否是最后一个客户端，如果是则移除 hub
		if hub.ClientCount() == 0 {
			RemoveHubIfEmpty(rtspURL, hub)
		}
	}()

	// 如果需要启动播放
	if shouldPlay {
		hub.Go(func() {
			var packetLossCount int64 // 添加丢包计数器
			client.OnPacketRTP(videoMedia, mpegtsFormat, func(pkt *rtp.Packet) {
				// 检查是否有丢包
				if pkt.Header.Marker {

					// 如果有丢包记录，输出日志
					if packetLossCount > 0 {
						logger.LogPrintf("%d RTP packets lost", packetLossCount)
						packetLossCount = 0 // 重置计数
					}
				} else {
					// 增加丢包计数
					packetLossCount++
				}

				hub.Broadcast(pkt.Payload)
				// ⚡ 每次收到 RTP 包时更新活跃时间
				// if updateActive != nil {
				// 	updateActive()
				// }
				// ⚡ 统计入流量
				// monitor.AddAppInboundBytes(uint64(len(pkt.Payload)))
			})

			_, err := client.Play(nil)
			if err != nil {
				logger.LogPrintf("RTSP play error: %v", err)
				// 不再关闭整个hub，而是设置流状态为错误
				hub.SetError(err)
				return
			}

			// 标记流为正在播放状态
			hub.SetPlaying()
		})
	} else {
		// 等待流状态变为播放中
		if !hub.WaitForPlaying(ctx) {
			return nil
		}
	}

	// 向客户端推送数据
	logger.LogRequestAndResponse(r, rtspURL, &http.Response{StatusCode: http.StatusOK})
	w.Header().Set("Content-Type", "video/mp2t")
	flusher, _ := w.(http.Flusher)

	flushTicker := time.NewTicker(200 * time.Millisecond) // 定时 flush
	defer flushTicker.Stop()

	activeTicker := time.NewTicker(5 * time.Second) // 定时更新活跃
	defer activeTicker.Stop()

	bufferedBytes := 0
	const maxBufferSize = 128 * 1024 // 128KB缓冲区
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-flushTicker.C:
			if flusher != nil && bufferedBytes > 0 {
				flusher.Flush()
				bufferedBytes = 0
			}
		case <-activeTicker.C:
			if updateActive != nil {
				updateActive()
			}
		case data, ok := <-clientChan.Chan():
			if !ok {
				return nil
			}

			payload, ok := data.([]byte)
			if !ok || len(payload) == 0 {
				continue
			}

			n, err := w.Write(payload)
			if err != nil {
				logger.LogPrintf("Write error: %v", err)
				return err
			}
			bufferedBytes += n
			if bufferedBytes >= maxBufferSize {
				if flusher != nil {
					flusher.Flush()
					bufferedBytes = 0
				}
			}
		}
	}
}
