package stream

import (
	"context"
	"net/http"
	// "time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
)

// HandleMpegtsStream 将 RTSP MPEGTS 流转发给 HTTP 客户端
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
	// logger.LogPrintf("DEBUG: HandleMpegtsStream started for URL: %s", rtspURL)

	// 创建客户端 ring buffer
	clientChan, err := ringbuffer.New(1024)
	if err != nil {
		return err
	}
	hub.AddClient(clientChan)
	defer func() {
		hub.RemoveClient(clientChan)
		clientChan.Close()
	}()

	// 检查是否需要启动播放
	shouldPlay := false
	hub.mu.Lock()
	if hub.isClosed {
		hub.mu.Unlock()
		return nil
	}
	
	// 判断是否需要启动播放
	if hub.state == StateStopped {
		shouldPlay = true
		hub.state = StatePlaying // 标记为播放中以防止重复启动
		// 设置RTSP客户端
		hub.rtspClient = client
	} else if hub.state == StateError {
		shouldPlay = true
		hub.state = StatePlaying // 标记为播放中以防止重复启动
		hub.lastError = nil      // 清除最后的错误
		// 设置新的RTSP客户端
		hub.rtspClient = client
	}
	hub.mu.Unlock()

	// 如果需要启动播放
	if shouldPlay {
		// var lastSequenceNumber uint16
		// var packetLossCount int64
		// var initialized bool

		client.OnPacketRTP(videoMedia, mpegtsFormat, func(pkt *rtp.Packet) {
			// 检查RTP负载是否为空
			if len(pkt.Payload) == 0 {
				// logger.LogPrintf("DEBUG: Empty RTP payload, skipping")
				return
			}

			// 更准确的RTP包丢失检测
			// if initialized {
			// 	// 计算预期的序列号
			// 	expectedSequence := lastSequenceNumber + 1
				
			// 	// 检查序列号是否回绕
			// 	if pkt.Header.SequenceNumber < lastSequenceNumber {
			// 		// 序列号回绕情况处理
			// 		if lastSequenceNumber > 0xFF00 && pkt.Header.SequenceNumber < 0x100 {
			// 			// 正常的回绕
			// 		} else {
			// 			// 异常情况，重置跟踪
			// 			initialized = false
			// 		}
			// 	} else {
			// 		// 检查是否有丢包
			// 		if pkt.Header.SequenceNumber > expectedSequence {
			// 			lostPackets := int64(pkt.Header.SequenceNumber - expectedSequence)
			// 			packetLossCount += lostPackets
						
			// 			// 只有当丢包数量较大时才记录日志，避免频繁日志
			// 			if lostPackets > 10 {
			// 				logger.LogPrintf("RTP packets lost: %d, expected: %d, got: %d", 
			// 					lostPackets, expectedSequence, pkt.Header.SequenceNumber)
			// 			}
			// 		}
			// 	}
			// } else {
			// 	initialized = true
			// }
			
			// 更新最后收到的序列号
			// lastSequenceNumber = pkt.Header.SequenceNumber

			hub.Broadcast(pkt.Payload)
		})

		_, err := client.Play(nil)
		if err != nil {
			logger.LogPrintf("RTSP play error: %v", err)
			// 不再关闭整个hub，而是设置流状态为错误
			hub.SetError(err)
			return err
		}

		// 标记流为正在播放状态
		hub.SetPlaying()
	} else {
		// 等待流状态变为播放中
		if !hub.WaitForPlaying(ctx) {
			return nil
		}
	}

	// 设置 HTTP 响应头
	logger.LogRequestAndResponse(r, rtspURL, &http.Response{StatusCode: http.StatusOK})
	w.Header().Set("Content-Type", "video/mp2t")

	flusher, _ := w.(http.Flusher)

	// 写入 HTTP 流
	for {
		select {
		case <-ctx.Done():
			// logger.LogPrintf("DEBUG: HTTP context done, closing client connection for URL: %s", rtspURL)
			return nil
		default:
			data, ok := clientChan.Pull()
			if !ok {
				// logger.LogPrintf("DEBUG: clientChan closed, ending connection for URL: %s", rtspURL)
				return nil
			}

			payload, ok := data.([]byte)
			if !ok || len(payload) == 0 {
				// logger.LogPrintf("DEBUG: Invalid or empty payload pulled from ringbuffer")
				// 即使是空数据也继续处理
				continue
			}

			n, err := w.Write(payload)
			if err != nil {
				logger.LogPrintf("ERROR: HTTP write failed: %v", err)
				return err
			}
			if n != len(payload) {
				// logger.LogPrintf("DEBUG: Incomplete write. Expected: %d, Written: %d", len(payload), n)
			}

			if flusher != nil {
				flusher.Flush()
			}

			if updateActive != nil {
				updateActive()
			}
		}
	}
}