package stream

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asticode/go-astits"
	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/pion/rtp"

	"github.com/qist/tvgate/logger"
	"github.com/qist/tvgate/utils/buffer/ringbuffer"
)

const (
	videoPID = 256
	audioPID = 257
	// videoStep = 90000 / 25 // 25fps，步进3600
)

var dropCount int64

var bufPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 188*512) // TS包一般188字节，按需调整
	},
}

type channelWriter struct {
	rb *ringbuffer.RingBuffer
}

func (w *channelWriter) Write(p []byte) (n int, err error) {
	buf := bufPool.Get().([]byte)
	if cap(buf) < len(p) {
		buf = make([]byte, len(p))
	}
	buf = buf[:len(p)]
	copy(buf, p)

	if !w.rb.Push(buf) {
		atomic.AddInt64(&dropCount, 1)
		bufPool.Put(buf)
		if atomic.LoadInt64(&dropCount)%10000 == 0 {
			logger.LogPrintf("⚠️ TS packet dropped due to full buffer.")
		}
		return 0, nil
	}
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
	hub *StreamHubs,
	updateActive func(),
) error {
	// 创建客户端通道
	clientChan, err := ringbuffer.New(2048) // 增加缓冲区大小以适应更高的吞吐量
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
			if currentClient != nil {
				// 仅在客户端匹配时才关闭
				if currentClient == client {
					currentClient.Close()
					hub.mu.Lock()
					// 清除RTSP客户端引用
					if hub.rtspClient == currentClient {
						hub.rtspClient = nil
					}
					hub.mu.Unlock()
				}
			}
		}
	}()

	// 如果需要启动播放
	if shouldPlay {
		go func() {
			// 增加TS通道缓冲区大小，从262144增加到1048576
			tsChan := make(chan []byte, 1048576)
			mux := astits.NewMuxer(context.Background(), &channelWriter{rb: clientChan})
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
				// monitor.AddAppInboundBytes(uint64(len(pkt.Payload)))
				nalus, err := vDecoder.Decode(pkt)
				if err != nil || len(nalus) == 0 {
					naluWaitCount++
					atomic.AddInt64(&videoDropCount, 1)
					// 添加日志记录，每100次丢包记录一次
					if naluWaitCount%100 == 0 {
						logger.LogPrintf("%d RTP packets lost", naluWaitCount)
						naluWaitCount = 0 // 重置计数
					}
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
				// 更新活跃时间
				// if updateActive != nil {
				// 	updateActive()
				// }
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
				var audioDropCount int64 // 添加音频丢包计数器
				client.OnPacketRTP(audioMedia, audioFormat, func(pkt *rtp.Packet) {
					// ⚡ 入流量统计
					// monitor.AddAppInboundBytes(uint64(len(pkt.Payload)))
					aus, err := aDecoder.Decode(pkt)
					if err != nil || len(aus) == 0 {
						audioDropCount++
						// 添加日志记录，每10次丢包记录一次
						if audioDropCount%10 == 0 {
							logger.LogPrintf("%d RTP packets lost", audioDropCount)
							audioDropCount = 0 // 重置计数
						}
						return
					}
					// 重置计数
					if audioDropCount > 0 {
						audioDropCount = 0
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
					}
					// 更新活跃时间
					// if updateActive != nil {
					// 	updateActive()
					// }
				})

			}

			_, err := client.Play(nil)
			if err != nil {
				logger.LogPrintf("RTSP play error: %v", err)
				// 不再关闭整个hub，而是设置流状态为错误
				hub.SetError(err)
				return
			}

			// 标记流为正在播放状态
			hub.SetPlaying()

			// 向所有客户端广播数据
			for pkt := range tsChan {
				hub.Broadcast(pkt)
				// ⚡ 出流量统计
				// monitor.AddAppOutboundBytes(uint64(len(pkt)))
				// 更新活跃时间
				// if updateActive != nil {
				// 	updateActive()
				// }
			}
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

	flushTicker := time.NewTicker(200 * time.Millisecond)
	defer flushTicker.Stop()

	activeTicker := time.NewTicker(5 * time.Second)
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

			payload := data.([]byte)
			if len(payload) == 0 {
				bufPool.Put(payload[:cap(payload)])
				continue
			}

			n, err := w.Write(payload)
			bufPool.Put(payload[:cap(payload)])

			if err != nil {
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
