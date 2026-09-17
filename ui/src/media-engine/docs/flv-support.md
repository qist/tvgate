# HTTP-FLV 支持（FLV demuxer 移植）

> 2026-09-16。触发事件：旧 GPL 引擎 `ui/src/playback-engine/` 整体退役时，把其中的
> `demux/flv-demuxer.ts` 一起删掉，而 media-engine 没有对应实现 → **FLV 直链/FLV 上游全部播不了**。
> 本文记录恢复方案与支持边界。

## 来源与许可
- 原实现（681 行）位于 `playback-engine/demux/flv-demuxer.ts`，参考实现 `/opt/rtp2httpd/web-ui/src/playback-engine/demux/` 中
  **并不存在该文件** → 属**本项目自研**，非 GPL 派生，可直接移植。
- 移植版 `demux/flv-demuxer.ts` 按 media-engine 的**输出契约重写接线**（原实现输出的是旧引擎的
  `onTrackMetadata/onDataAvailable` 协议），FLV tag 解析 / 时间戳归一化保持等价行为。

## 支持范围
| 项 | 支持 | 说明 |
|---|---|---|
| 视频 H.264 | ✅ | legacy codecId 7；`codecPrivate` = avcC（含 box 头），样本 = length-prefixed VCL NALU，B 帧 CTS 保留 |
| 视频 H.265 | ✅ **新增** | Enhanced-RTMP：tag 头 bit7 置位 + fourcc `hvc1`/`hev1`/`hvc2`；packetType 0（SequenceStart→hvcC）/1（CodedFrames，带 CTS）/3（CodedFramesX，无 CTS，兼容 Annex-B 兜底）|
| 音频 AAC | ✅ | soundFormat 10；序列头即 AudioSpecificConfig → `codecPrivate` = esds；样本 = **裸 AAC 帧**（无 ADTS），timescale = 采样率 |
| 音频 MP3 | ✅ | soundFormat 2；软解轨道（codec `mp3`，timescale 90kHz），逐帧切分 + 跨 tag 残帧拼接 |
| script tag（onMetaData）| 跳过 | 不解析时长/码率（起播不依赖）|
| AV1（fourcc `av01`）/ packetType 4、5 | ❌ | 需要时再补 |

> 旧实现只支持 H.264（其它 codecId 只打日志）；**FLV 里的 H.265 是本次新增能力**。

## 接线（pipeline 首块探测）
- `pipeline/transmux-pipeline.ts`：解复用器改为**惰性创建**——首块字节经 `FlvDemuxer.probe()`
  判定：`"FLV"` 魔数 → `FlvDemuxer`，其余 → `TsDemuxer`（两者回调协议同形，共用 `demuxerCallbacks`）。
  首块不足 3 字节时暂存到 `demuxProbeBuffer` 等更多数据；`destroy()` 置空以便新流重新探测。
- `worker/transmux-worker.ts` 的 `resolveSources()` 无需改动：FLV 不是 m3u8（`sniffHls()` 失败）→
  走静态 URL 列表这条"连续流"路径（与连续 TS 直链一致），直播自愈重连/看门狗照旧生效。
- 轨道布局经 `onStreamLayout` 上报：`video` + `mseAudio`（AAC）/ `softAudio`（MP3，会触发 C2 静音假音轨）。

## 测试
- `demux/flv-demuxer.test.ts`（真实 ffmpeg 素材）：FLV 魔数探测、H.264+AAC 轨道/box/关键帧门控/B 帧 cts、
  H.265(Enhanced-RTMP)+AAC 的 hvcC 与 `hvc1.` codec 串、1KB/7 字节分块喂入与整块等量。
- `pipeline/transmux-pipeline.flv.test.ts`：端到端（fetch 打桩喂 FLV）→ 产出 `avc1` 视频 init 与 `mp4a` 音频 init + 媒体段。
- 素材：`demux/testdata/test-h264.flv`、`test-h265.flv`（testsrc2 320x180@15fps 1.5s + sine 48kHz，自产）。

重建素材（如需）：
```bash
ffmpeg -f lavfi -i "testsrc2=size=320x180:rate=15:duration=1.5" -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=1.5" \
  -c:v libx264 -profile:v main -bf 2 -pix_fmt yuv420p -g 15 -c:a aac -b:a 96k -f flv test-h264.flv
# H.265 走 Enhanced-RTMP：不要加 -tag:v hvc1（ffmpeg flv muxer 会报 "Tag hvc1 incompatible"，默认即可）
ffmpeg -f lavfi -i "testsrc2=size=320x180:rate=15:duration=1.5" -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=1.5" \
  -c:v libx265 -pix_fmt yuv420p -g 15 -c:a aac -b:a 96k -f flv test-h265.flv
```

## 备注
- Go 侧对 FLV 上游仍是**原样透传**（`player/handler.go` 只把 FLV 判为"可用上游"），不做转封装 →
  前端这段 demuxer 是唯一能把 FLV 播出来的地方。
- 发布器 `/<path>/play/<name>.flv` 与 `/<path>/index.m3u8` 两条入口现在都能播（后者本来就走 HLS）。
