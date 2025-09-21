package stream

import (
	"context"
	"net/http"
	// "net/url"
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
		bufPool.Put(buf) // 回收
		// 添加日志记录，当缓冲区满时记录详细信息
		if atomic.LoadInt64(&dropCount)%10000 == 0 { // 每10000次记录一次，避免日志过多
			logger.LogPrintf("⚠️ TS packet dropped due to full buffer.")
		}
		return 0, nil
	}
	return len(p), nil
}

func buildADTSHeader(cfg mpeg4audio.Config, aacLen int) []byte {
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

// HandleH264AacStream 处理 H264+AAC HTTP 推流客户端
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
	// logger.LogPrintf("DEBUG: HandleH264AacStream started for URL: %s", rtspURL)

	// 创建客户端 ring buffer
	clientChan, err := ringbuffer.New(8 * 1024 * 1024)
	if err != nil {
		return err
	}
	hub.AddClient(clientChan)
	defer func() {
		hub.RemoveClient(clientChan)
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
		hub.lastError = nil
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
			ticker := time.NewTicker(300 * time.Millisecond)
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

		var videoFrame []byte
		var videoPTS time.Duration
		var audioPTS time.Duration
		var lastVideoSeq uint16
		var videoLossCount int64
		var videoInitialized bool

		// 处理视频数据
		client.OnPacketRTP(videoMedia, videoFormat, func(pkt *rtp.Packet) {
			// 检查RTP负载是否为空
			if len(pkt.Payload) == 0 {
				// logger.LogPrintf("DEBUG: Empty video RTP payload received")
				return
			}

			// 更准确的视频RTP包丢失检测
			if videoInitialized {
				expectedSeq := lastVideoSeq + 1
				if pkt.Header.SequenceNumber < lastVideoSeq {
					// 序列号回绕
					if lastVideoSeq > 0xFF00 && pkt.Header.SequenceNumber < 0x100 {
						// 正常回绕
					} else {
						// 异常情况，重置并丢弃当前累积的帧数据
						videoInitialized = false
						videoFrame = nil // 丢弃不完整的帧
					}
				} else {
					if pkt.Header.SequenceNumber > expectedSeq {
						lost := int64(pkt.Header.SequenceNumber - expectedSeq)
						videoLossCount += lost
						if lost > 10 {
							logger.LogPrintf("Video RTP packets lost: %d, expected: %d, got: %d",
								lost, expectedSeq, pkt.Header.SequenceNumber)
						}
						// 如果丢包严重，丢弃当前累积的帧数据
						if lost > 100 {
							videoFrame = nil // 丢弃不完整的帧
						}
					}
				}
			} else {
				videoInitialized = true
				videoFrame = nil // 确保从一个干净的状态开始
			}
			lastVideoSeq = pkt.Header.SequenceNumber

			// 更新PTS
			videoPTS = time.Duration(pkt.Timestamp) * time.Second / 90000 // H264通常使用90kHz时钟

			// 累积视频帧
			videoFrame = append(videoFrame, pkt.Payload...)

			// 检查是否是帧结束标记
			if pkt.Marker {
				if len(videoFrame) == 0 {
					// logger.LogPrintf("DEBUG: Empty video frame to write")
					return
				}

				// 写入完整帧到MPEG-TS复用器
				_, err := mux.WriteData(&astits.MuxerData{
					PID: videoPID,
					PES: &astits.PESData{
						Header: &astits.PESHeader{
							OptionalHeader: &astits.PESOptionalHeader{
								PTS: &astits.ClockReference{
									Base:      int64(videoPTS) * 90, // 转换为90kHz时钟
									Extension: 0,
								},
							},
							StreamID: 224, // 视频流ID
						},
						Data: videoFrame,
					},
				})
				if err != nil {
					logger.LogPrintf("Muxer write video error: %v", err)
				}

				// 重置视频帧缓冲区
				videoFrame = nil
			}
		})

		// 处理音频数据
		if audioFormat != nil && audioMedia != nil {
			client.OnPacketRTP(audioMedia, audioFormat, func(pkt *rtp.Packet) {
				// AAC数据通常直接在RTP包中
				aacData := pkt.Payload
				if len(aacData) == 0 {
					// logger.LogPrintf("DEBUG: Empty audio RTP payload received")
					return
				}

				// 更新PTS
				audioPTS = time.Duration(pkt.Timestamp) * time.Second / time.Duration(audioFormat.ClockRate())

				// 构建ADTS头
				adtsHeader := buildADTSHeader(*audioFormat.Config, len(aacData))

				// 合并ADTS头和AAC数据
				frameWithADTS := make([]byte, len(adtsHeader)+len(aacData))
				copy(frameWithADTS, adtsHeader)
				copy(frameWithADTS[len(adtsHeader):], aacData)

				// 写入完整帧到MPEG-TS复用器
				_, err := mux.WriteData(&astits.MuxerData{
					PID: audioPID,
					PES: &astits.PESData{
						Header: &astits.PESHeader{
							OptionalHeader: &astits.PESOptionalHeader{
								PTS: &astits.ClockReference{
									Base:      int64(audioPTS) * 90, // 转换为90kHz时钟
									Extension: 0,
								},
							},
							StreamID: 192, // 音频流ID
						},
						Data: frameWithADTS,
					},
				})
				if err != nil {
					logger.LogPrintf("Muxer write audio error: %v", err)
				}
			})
		}

		// 开始播放
		_, err := client.Play(nil)
		if err != nil {
			logger.LogPrintf("RTSP play error: %v", err)
			hub.SetError(err)
			return err
		}

		hub.SetPlaying()
	} else {
		// 等待播放启动完成
		if !hub.WaitForPlaying(ctx) {
			return nil
		}
	}
	logger.LogRequestAndResponse(r, rtspURL, &http.Response{StatusCode: http.StatusOK})
	// 设置 HTTP 响应头
	w.Header().Set("Content-Type", "video/mp2t")
	flusher, _ := w.(http.Flusher)

	// 写入 HTTP 流
	for {
		select {
		case <-ctx.Done():
			// logger.LogPrintf("DEBUG: Context done, ending H264+AAC stream for URL: %s", rtspURL)
			return nil
		default:
			data, ok := clientChan.Pull()
			if !ok {
				logger.LogPrintf("clientChan closed, ending connection")
				return nil
			}

			payload := data.([]byte)
			if len(payload) == 0 {
				// logger.LogPrintf("DEBUG: Empty payload pulled from ringbuffer for H264+AAC")
				bufPool.Put(payload[:cap(payload)])
				continue
			}

			n, err := w.Write(payload)
			if err != nil {
				logger.LogPrintf("Write error: %v", err)
				// 确保在返回前归还缓冲区到池中
				bufPool.Put(payload[:cap(payload)])
				return err
			}

			if n != len(payload) {
				// logger.LogPrintf("DEBUG: Incomplete write for H264+AAC. Expected: %d, Written: %d", len(payload), n)
				// 即使写入不完整，也应回收缓冲区，避免内存泄漏
				bufPool.Put(payload[:cap(payload)])
				continue
			}

			if flusher != nil {
				flusher.Flush()
			}

			if updateActive != nil {
				updateActive()
			}

			// 成功写入后回收缓冲区
			bufPool.Put(payload[:cap(payload)])
		}
	}
}
