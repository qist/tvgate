package stream

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer"
)

var dropCount int64

func HandleMpegtsStream(
	ctx context.Context,
	w http.ResponseWriter,
	client *gortsplib.Client,
	videoMedia *description.Media,
	mpegtsFormat *format.MPEGTS,
	r *http.Request,
	rtspURL string,
) error {
	tsChan := make(chan []byte, 262144)
	done := make(chan struct{})
	// 直接转发 TS 数据
	client.OnPacketRTP(videoMedia, mpegtsFormat, func(pkt *rtp.Packet) {
		select {
		case <-ctx.Done():
			return // 前端断开，停止推流
		case tsChan <- pkt.Payload:
		default:
			atomic.AddInt64(&dropCount, 1)
		}
	})

	_, err := client.Play(nil)
	if err != nil {
		http.Error(w, "RTSP play error: "+err.Error(), 500)
		return err
	}

	logger.LogRequestAndResponse(r, rtspURL, &http.Response{StatusCode: http.StatusOK})
	w.Header().Set("Content-Type", "video/mp2t")
	flusher, _ := w.(http.Flusher)
	// 2. 播放前预缓冲
	preBuffer := make([][]byte, 0, 2048)
	preBufferDuration := 1 * time.Second
	preBufferStart := time.Now()
	buffering := true

	// 新增：监听 ctx.Done()，前端断开时关闭 RTSP
	go func() {
		<-ctx.Done()
		client.Close()
		close(done)
		close(tsChan) // 通知所有消费端退出
	}()

	for buffering {
		select {
		case pkt := <-tsChan:
			preBuffer = append(preBuffer, pkt)
			if time.Since(preBufferStart) > preBufferDuration {
				buffering = false
			}
		case <-ctx.Done():
			return nil
		}
	}

	// 3. 先推送预缓冲内容
	for _, pkt := range preBuffer {
		_, err := w.Write(pkt)
		if err != nil {
			logger.LogPrintf("Write error: %v", err)
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 4. 正式流式推送
	bufSize := buffer.GetOptimalBufferSize("video/mp2t", r.URL.Path)
	buf := buffer.GetBuffer(bufSize)
	defer buffer.PutBuffer(bufSize, buf)

	for {
		select {
		case pkt, ok := <-tsChan:
			if !ok {
				return nil
			}
			n := copy(buf, pkt)
			if n > 0 {
				_, err := w.Write(buf[:n])
				if err != nil {
					logger.LogPrintf("Write error: %v", err)
					return err
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		case <-ctx.Done():
			return nil
		case <-done:
			return nil
		}
	}
}
