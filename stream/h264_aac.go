package stream

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asticode/go-astits"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/pion/rtp"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer"
)

const (
	videoPID = 256
	audioPID = 257
	// videoStep = 90000 / 25 // 25fps，步进3600
)

var bufPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 188*512) // TS包一般188字节，按需调整
	},
}

type channelWriter struct {
	ch chan []byte
}

func (w *channelWriter) Write(p []byte) (n int, err error) {
	buf := bufPool.Get().([]byte)
	if cap(buf) < len(p) {
		buf = make([]byte, len(p))
	}
	buf = buf[:len(p)]
	copy(buf, p)
	select {
	case w.ch <- buf:
		return len(p), nil
	default:
		atomic.AddInt64(&dropCount, 1)
		bufPool.Put(buf) // 回收
		return 0, nil
	}
}

func buildADTSHeader(cfg *mpeg4audio.Config, aacLen int) []byte {
	profile := byte(cfg.Type - 1)
	sampleRateIndex := byte(4)
	switch cfg.SampleRate {
	case 96000:
		sampleRateIndex = 0
	case 88200:
		sampleRateIndex = 1
	case 64000:
		sampleRateIndex = 2
	case 48000:
		sampleRateIndex = 3
	case 44100:
		sampleRateIndex = 4
	case 32000:
		sampleRateIndex = 5
	case 24000:
		sampleRateIndex = 6
	case 22050:
		sampleRateIndex = 7
	case 16000:
		sampleRateIndex = 8
	case 12000:
		sampleRateIndex = 9
	case 11025:
		sampleRateIndex = 10
	case 8000:
		sampleRateIndex = 11
	case 7350:
		sampleRateIndex = 12
	}
	channels := byte(cfg.ChannelCount)
	adtsLen := aacLen + 7 // aacLen为AAC帧数据长度
	return []byte{
		0xFF, 0xF1,
		((profile & 0x3) << 6) | ((sampleRateIndex & 0xF) << 2) | ((channels >> 2) & 0x1),
		((channels & 0x3) << 6) | byte((adtsLen>>11)&0x3),
		byte((adtsLen >> 3) & 0xFF),
		byte(((adtsLen & 0x7) << 5) | 0x1F),
		0xFC,
	}
}

func HandleH264AacStream(
	ctx context.Context,
	w http.ResponseWriter,
	client *gortsplib.Client,
	videoMedia *description.Media,
	videoFormat *format.H264,
	audioMedia *description.Media,
	audioFormat *format.MPEG4Audio,
	r *http.Request,
	rtspURL string,
) error {

	tsChan := make(chan []byte, 262144)
	mux := astits.NewMuxer(context.Background(), &channelWriter{ch: tsChan})
	mux.SetPCRPID(videoPID)
	mux.AddElementaryStream(astits.PMTElementaryStream{
		ElementaryPID: videoPID,
		StreamType:    astits.StreamTypeH264Video,
	})
	if audioFormat != nil {
		mux.AddElementaryStream(astits.PMTElementaryStream{
			ElementaryPID: audioPID,
			StreamType:    astits.StreamTypeAACAudio,
		})
	}

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mux.WriteTables()
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dc := atomic.SwapInt64(&dropCount, 0)
				if dc > 0 {
					logger.LogPrintf("⚠️ TS packet dropped (buffer full): %d in last 5s\n", dc)
				}
			}
		}
	}()

	var videoPTS int64
	var audioPTS float64
	var audioInit bool

	// 视频
	vDecoder, _ := videoFormat.CreateDecoder()

	var videoDropCount int64
	var (
		lastVideoTS   uint32
		firstVideoPkt = true
	)
	naluWaitCount := 0
	client.OnPacketRTP(videoMedia, videoFormat, func(pkt *rtp.Packet) {
		nalus, err := vDecoder.Decode(pkt)
		if err != nil || len(nalus) == 0 {
			naluWaitCount++
			// if naluWaitCount%30 == 0 { // 每30次打印一次，防止刷屏
			// 	logger.LogPrintf("Video decode error: %v", err)
			// }
			atomic.AddInt64(&videoDropCount, 1)
			return
		}
		naluWaitCount = 0 // 解码成功，重置计数
		if firstVideoPkt {
			lastVideoTS = pkt.Timestamp
			videoPTS = 0
			firstVideoPkt = false
		} else {
			step := int64(pkt.Timestamp - lastVideoTS)
			videoPTS += step
			lastVideoTS = pkt.Timestamp
		}
		// logger.LogPrintf("Video frame, videoPTS=%d", videoPTS)
		buf := &bytes.Buffer{}
		for _, nalu := range nalus {
			buf.Write([]byte{0x00, 0x00, 0x00, 0x01})
			buf.Write(nalu)
		}
		mux.WriteData(&astits.MuxerData{
			PID: videoPID,
			PES: &astits.PESData{
				Header: &astits.PESHeader{
					OptionalHeader: &astits.PESOptionalHeader{
						MarkerBits:             2,
						PTSDTSIndicator:        astits.PTSDTSIndicatorOnlyPTS,
						PTS:                    &astits.ClockReference{Base: videoPTS},
						DataAlignmentIndicator: true,
					},
				},
				Data: buf.Bytes(),
			},
		})
	})

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			<-ticker.C
			dc := atomic.SwapInt64(&videoDropCount, 0)
			if dc > 0 {
				// logger.LogPrintf("⚠️ Video frame decode failed: %d in last 5s", dc)
			}
		}
	}()

	// 音频
	if audioFormat != nil {
		aDecoder, _ := audioFormat.CreateDecoder()
		client.OnPacketRTP(audioMedia, audioFormat, func(pkt *rtp.Packet) {
			aus, err := aDecoder.Decode(pkt)
			if err != nil || len(aus) == 0 {
				return
			}
			for _, au := range aus {
				if len(au) == 0 {
					logger.LogPrintf("⚠️ Skip empty AU")
					continue
				}
				adts := buildADTSHeader(audioFormat.Config, len(au))
				data := append(adts, au...)
				if !audioInit {
					audioPTS = float64(videoPTS)
					audioInit = true
				}
				_, err := mux.WriteData(&astits.MuxerData{
					PID: audioPID,
					PES: &astits.PESData{
						Header: &astits.PESHeader{
							OptionalHeader: &astits.PESOptionalHeader{
								MarkerBits:             2,
								PTSDTSIndicator:        astits.PTSDTSIndicatorOnlyPTS,
								PTS:                    &astits.ClockReference{Base: int64(audioPTS)},
								DataAlignmentIndicator: true,
							},
						},
						Data: data,
					},
				})
				if err == nil {
					audioPTS += float64(1024*90000) / float64(audioFormat.Config.SampleRate)
				}
				// logger.LogPrintf("AAC AU len=%d", len(au))
			}
		})
	}

	_, err := client.Play(nil)
	if err != nil {
		http.Error(w, "RTSP play error: "+err.Error(), 500)
		return err
	}

	logger.LogRequestAndResponse(r, rtspURL, &http.Response{StatusCode: http.StatusOK})
	w.Header().Set("Content-Type", "video/mp2t")
	flusher, _ := w.(http.Flusher)

	go func() {
		<-ctx.Done()
		client.Close()
		close(tsChan)
	}()

	preBuffer := make([][]byte, 0, 4096)
	preBufferDuration := 1 * time.Second
	preBufferStart := time.Now()
	buffering := true

	for buffering {
		select {
		case pkt := <-tsChan:
			preBuffer = append(preBuffer, pkt)
			if time.Since(preBufferStart) > preBufferDuration {
				buffering = false
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// 先推送预缓冲内容
	for _, pkt := range preBuffer {
		_, err := w.Write(pkt)
		if err != nil {
			logger.LogPrintf("Write error: %v", err)
			return ctx.Err()
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	bufSize := buffer.GetOptimalBufferSize("video/mp2t", r.URL.Path)
	buf := buffer.GetBuffer(bufSize)
	defer buffer.PutBuffer(bufSize, buf)

	for {
		select {
		case pkt, ok := <-tsChan:
			if !ok {
				return nil // 正常退出，不再推送数据
			}
			n := copy(buf, pkt)
			if n > 0 {
				_, err := w.Write(buf[:n])
				if err != nil {
					logger.LogPrintf("Write error: %v", err)
					return ctx.Err()
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

}
