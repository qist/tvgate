package stream

import (
	"bytes"
	"testing"
)

func flvCaptureTag(tagType byte, ts uint32, body []byte) []byte {
	tag := make([]byte, flvTagHeaderLen+len(body)+4)
	tag[0] = tagType
	tag[1] = byte(len(body) >> 16)
	tag[2] = byte(len(body) >> 8)
	tag[3] = byte(len(body))
	tag[4] = byte(ts >> 16)
	tag[5] = byte(ts >> 8)
	tag[6] = byte(ts)
	tag[7] = byte(ts >> 24)
	copy(tag[flvTagHeaderLen:], body)
	return tag
}

// TestFLVHeaderCaptureChunkBoundaries：读块边界不落在 tag 边界上也能捕获齐三样，
// 且捕获到的都是规范的完整 tag/文件头。旧实现按「整个读块」做前缀匹配，真实读块
// 几乎必然跨 tag → 把整块当 flvHeader 重放（H5 解复用乱套）或配置永远抓不到。
func TestFLVHeaderCaptureChunkBoundaries(t *testing.T) {
	header := []byte{'F', 'L', 'V', 1, 5, 0, 0, 0, 9, 0, 0, 0, 0}
	videoCfg := flvCaptureTag(9, 0, []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x64, 0x00, 0x1f}) // AVC sequence header
	audioCfg := flvCaptureTag(8, 0, []byte{0xaf, 0x00, 0x12, 0x10})                               // AAC sequence header
	keyframe := flvCaptureTag(9, 66, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0xaa, 0xbb})
	streamData := bytes.Join([][]byte{header, videoCfg, audioCfg, keyframe}, nil)

	c := &flvHeaderCapture{}
	var gotHeader, gotVideo, gotAudio []byte
	// 故意按 7 字节小块喂：不对齐文件头（13B）也不对齐任何 tag 边界
	for i := 0; i < len(streamData); i += 7 {
		end := i + 7
		if end > len(streamData) {
			end = len(streamData)
		}
		h, v, a := c.feed(streamData[i:end])
		if h != nil {
			gotHeader = h
		}
		if v != nil {
			gotVideo = v
		}
		if a != nil {
			gotAudio = a
		}
	}
	if !bytes.Equal(gotHeader, header) {
		t.Fatalf("文件头捕获不符: got %d 字节 want %d", len(gotHeader), len(header))
	}
	if !bytes.Equal(gotVideo, videoCfg) {
		t.Fatalf("AVC 序列头捕获不符: got %d 字节 want %d", len(gotVideo), len(videoCfg))
	}
	if !bytes.Equal(gotAudio, audioCfg) {
		t.Fatalf("AAC 序列头捕获不符: got %d 字节 want %d", len(gotAudio), len(audioCfg))
	}
	// 三样齐了即短路：后续数据不再解析、不再产出
	if _, v, a := c.feed(keyframe); v != nil || a != nil {
		t.Fatalf("捕获齐后不应再产出")
	}
}

// TestFLVHeaderCaptureNonFLV：非 FLV 输入（误入的 TS/HTML 等）直接放弃捕获且不再恢复。
func TestFLVHeaderCaptureNonFLV(t *testing.T) {
	c := &flvHeaderCapture{}
	if _, v, a := c.feed([]byte("<html>not a stream</html>")); v != nil || a != nil {
		t.Fatalf("非 FLV 不应捕获")
	}
	if h, v, a := c.feed([]byte{'F', 'L', 'V', 1, 5, 0, 0, 0, 9, 0, 0, 0, 0}); h != nil || v != nil || a != nil {
		t.Fatalf("终止后不应再捕获")
	}
}
