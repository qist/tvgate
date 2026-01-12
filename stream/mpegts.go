package stream

import (
	"context"
	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
	"net/http"
	"time"

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
	case 0: // stopped
		shouldPlay = true
		hub.state = 1
		hub.rtspClient = client

	case 2: // error
		shouldPlay = true
		hub.state = 1
		hub.lastError = nil
		hub.rtspClient = client
	}
	hub.mu.Unlock()

	defer func() {
		hub.RemoveClient(clientChan)

		// 检查是否是最后一个客户端
		hub.mu.Lock()
		clientCount := len(hub.clients)
		currentClient := hub.rtspClient
		hub.mu.Unlock()

		if clientCount == 0 {
			hub.SetStopped()
			// 关闭RTSP客户端
			if currentClient != nil && currentClient == client {
				currentClient.Close()
				hub.mu.Lock()
				// 清除RTSP客户端引用
				if hub.rtspClient == currentClient {
					hub.rtspClient = nil
				}
				hub.mu.Unlock()
			}

			// 在关闭RTSP客户端后，确保只关闭一次clientChan
			// 注意：clientChan已经在RemoveClient中被关闭了，这里不需要再次关闭
		}
	}()

	// 如果需要启动播放
	if shouldPlay {

		go func() {
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
		}()
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
		default:
			data, ok := clientChan.PullWithContext(ctx)
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
