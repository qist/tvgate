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

func HandleMpegtsStream(
	ctx context.Context,
	w http.ResponseWriter,
	client *gortsplib.Client,
	videoMedia *description.Media,
	mpegtsFormat *format.MPEGTS,
	r *http.Request,
	rtspURL string,
	hub *StreamHubs,
) error {
	clientChan := make(chan []byte, 1024)
	hub.AddClient(clientChan)
	defer func() {
		hub.RemoveClient(clientChan)
	}()

	// 如果是第一个客户端，启动后端流
	if hub.ClientCount() == 1 {
		go func() {
			client.OnPacketRTP(videoMedia, mpegtsFormat, func(pkt *rtp.Packet) {
				hub.Broadcast(pkt.Payload)
			})

			_, err := client.Play(nil)
			if err != nil {
				logger.LogPrintf("RTSP play error: %v", err)
				hub.Close()
				return
			}
		}()
	}

	// 向客户端推送数据
	logger.LogRequestAndResponse(r, rtspURL, &http.Response{StatusCode: http.StatusOK})
	w.Header().Set("Content-Type", "video/mp2t")
	flusher, _ := w.(http.Flusher)
	for {
		select {
		case pkt, ok := <-clientChan:
			if !ok {
				return nil
			}
			_, err := w.Write(pkt)
			if err != nil {
				logger.LogPrintf("Write error: %v", err)
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-ctx.Done():
			return nil
		}
	}
}
