package stream

import (
	"bytes"
	"context"
	"net/http"
	// "sync/atomic"
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
)

// var dropCount int64

// ringBufferWriter 用于将 TS 数据写入 StreamRingBuffer
type ringBufferWriter struct {
	rb *StreamRingBuffer
}

func (w *ringBufferWriter) Write(p []byte) (int, error) {
	w.rb.Push(p)
	return len(p), nil
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
	adtsLen := aacLen + 7
	return []byte{
		0xFF, 0xF1,
		((profile & 0x3) << 6) | ((sampleRateIndex & 0xF) << 2) | ((channels >> 2) & 0x1),
		((channels & 0x3) << 6) | byte((adtsLen>>11)&0x3),
		byte((adtsLen >> 3) & 0xFF),
		byte(((adtsLen & 0x7) << 5) | 0x1F),
		0xFC,
	}
}

// HandleH264AacStream 推送 H264+AAC 转 TS 流
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
	hub *StreamHubs,
	updateActive func(),
) error {
	// 创建客户端 RingBuffer
	clientChan := NewOptimalStreamRingBuffer("video/mp2t", r.URL)
	hub.AddClient(clientChan)

	// 检查是否需要启动播放
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
		hub.lastError = nil
	}
	hub.mu.Unlock()
	defer hub.RemoveClient(clientChan)


	if shouldPlay {
		go func() {
			mux := astits.NewMuxer(context.Background(), &ringBufferWriter{rb: clientChan})
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

			// 视频
			vDecoder, _ := videoFormat.CreateDecoder()
			var videoPTS int64
			firstVideoPkt := true
			client.OnPacketRTP(videoMedia, videoFormat, func(pkt *rtp.Packet) {
				nalus, err := vDecoder.Decode(pkt)
				if err != nil || len(nalus) == 0 {
					return
				}
				if firstVideoPkt {
					videoPTS = 0
					firstVideoPkt = false
				} else {
					videoPTS += int64(pkt.Timestamp)
				}
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
				if updateActive != nil {
					updateActive()
				}
			})

			// 音频
			if audioFormat != nil {
				aDecoder, _ := audioFormat.CreateDecoder()
				var audioPTS float64
				audioInit := false
				client.OnPacketRTP(audioMedia, audioFormat, func(pkt *rtp.Packet) {
					aus, err := aDecoder.Decode(pkt)
					if err != nil || len(aus) == 0 {
						return
					}
					for _, au := range aus {
						adts := buildADTSHeader(audioFormat.Config, len(au))
						data := append(adts, au...)
						if !audioInit {
							audioPTS = float64(videoPTS)
							audioInit = true
						}
						mux.WriteData(&astits.MuxerData{
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
						audioPTS += float64(1024*90000) / float64(audioFormat.Config.SampleRate)
					}
					if updateActive != nil {
						updateActive()
					}
				})
			}

			_, err := client.Play(nil)
			if err != nil {
				hub.SetError(err)
				return
			}
			hub.SetPlaying()
		}()
	}

	// 等待流播放
	if !hub.WaitForPlaying(ctx) {
		return nil
	}

	// HTTP 推流
	w.Header().Set("Content-Type", "video/mp2t")
	flusher, _ := w.(http.Flusher)

	// 预缓冲
	preBuffer := make([][]byte, 0, 4096)
	preBufferStart := time.Now()
	preBufferDuration := 1 * time.Second
	for time.Since(preBufferStart) < preBufferDuration {
		pkt := clientChan.Pop()
		if pkt == nil {
			logger.LogPrintf("clientChan closed, ending connection")
			return nil
		}
		preBuffer = append(preBuffer, pkt)
	}

	for _, pkt := range preBuffer {
		w.Write(pkt)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 后续数据
	bufSize := buffer.GetOptimalBufferSize("video/mp2t", r.URL.Path)
	buf := buffer.GetBuffer(bufSize)
	defer buffer.PutBuffer(bufSize, buf)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			pkt := clientChan.Pop()
			if pkt == nil {
				logger.LogPrintf("clientChan closed, ending connection")
				return nil
			}
			n := copy(buf, pkt)
			if n > 0 {
				w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
				if updateActive != nil {
					updateActive()
				}
			}
		}
	}
}
