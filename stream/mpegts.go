package stream

import (
	"context"
	"net/http"
	"sync"
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
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			hub.RemoveClient(clientChan)
			if hub.ClientCount() == 0 {
				RemoveHubIfEmpty(rtspURL, hub)
			}
		})
	}

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
	hub.stateCond.Broadcast() // 通知等待的客户端状态变化
	hub.mu.Unlock()

	defer cleanup()

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
	rc := http.NewResponseController(w)

	go func() {
		<-ctx.Done()
		cleanup()
	}()

	// 使用时间戳替代定时器，减少 CPU 开销
	const (
		maxFlushBytes   = 32 * 1024
		maxFlushDelay   = 200 * time.Millisecond
		activeInterval  = 5 * time.Second
	)
	var (
		bufferedBytes     = 0
		lastFlush         = time.Now()
		lastActiveUpdate  = time.Now()
	)

	for {
		// 先尝试获取数据，阻塞直到有数据或 context 取消
		data, ok := clientChan.PullWithContext(ctx)
		if !ok {
			return nil
		}

		// 检查上下文取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		payload, ok := data.([]byte)
		if !ok || len(payload) == 0 {
			continue
		}

		// 设置写入超时
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		n, err := w.Write(payload)
		if err != nil {
			logger.LogPrintf("Write error: %v", err)
			return err
		}
		bufferedBytes += n

		// 基于时间和数据量的 flush（避免定时器开销）
		now := time.Now()
		if flusher != nil && bufferedBytes > 0 {
			if bufferedBytes >= maxFlushBytes || now.Sub(lastFlush) >= maxFlushDelay {
				flusher.Flush()
				bufferedBytes = 0
				lastFlush = now
			}
		}

		// 基于时间的活跃更新（避免定时器开销）
		if updateActive != nil && now.Sub(lastActiveUpdate) >= activeInterval {
			updateActive()
			lastActiveUpdate = now
		}
	}
}
