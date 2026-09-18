/**
 * 裸 ADTS AAC 解复用器（音频广播源）。
 *
 * 【为什么需要它】广东移动那批「广播」频道（`/live/gdyd.php?id=...`）的 HLS 分片虽然叫
 * `.hls.ts`，内容其实是**没有 TS 封装的裸 ADTS AAC 流**：首字节就是 ADTS 同步字
 * `FF F1`（ffprobe 也只会认出 `codec_name=aac`、无容器）。而探测逻辑只认 FLV，其余一律
 * 按连续 MPEG-TS 交给 TsDemuxer —— 它找不到 0x47 同步包，于是没有 PMT、没有轨道、
 * **既不 append 也不报错**：实测整条频道 25 秒零 append、`video.buffered` 恒空、
 * readyState=0、连 AudioContext 都没建（听感就是"彻底没声"）。
 *
 * 【形态】整段字节就是一条连续 ADTS 流：
 *   `[ADTS 头 7/9B][AAC 负载]…[ADTS 头][负载]…`
 * 所以这里不做 PES/PMT，只需扫同步字逐帧切分，剥掉 ADTS 头后按**裸 AAC 帧**交付
 * （正是 remuxer 需要的形态），并：
 *  - 首帧确定采样率/声道 → 构造 ASC/esds 并发布音轨；codec 串必须与 esds 声明的 AOT
 *    一致（见 aacCodecMimeType 注释），否则 SourceBuffer 建轨出错；
 *  - ADTS 里**没有任何时间戳**：时间轴按名义帧长（1024 采样）自行累加，且**跨 push／
 *    跨分片连续**（分片边界绝不归零）—— 与其他源的绝对 PTS 语义等价：一条流一条连续
 *    时间轴。首个 media 段的 `timestampOffset` 由 remuxer 反推成 0，故从 0 起算即可。
 */

import { parseAdtsFrame, buildAudioSpecificConfig, buildEsds, aacCodecMimeType, findAdtsSync } from "../formats/aac";
import type { DemuxedSample, TrackInfo, TsDemuxerCallbacks } from "./ts-demuxer";

/** AAC 每帧采样数（名义值）：ADTS 无时间戳，帧长方差靠它累加。 */
const AAC_SAMPLES_PER_FRAME = 1024;

const EMPTY = new Uint8Array(0);

/** 残帧上限（字节）：正常只有"跨 push 的半帧"（几 KB），超过说明流不同步，丢弃避免无界增长。 */
const PENDING_LIMIT = 256 * 1024;

/**
 * 首块是否像一条裸 ADTS 流：起始处连上两个完整帧头才算数。
 *
 * **必须校验第二帧紧跟第一帧**（`offset + frameLength` 处仍是合法帧头）：ADTS 同步字只有
 * 12 位，压缩载荷里随机出现 `FF Fx` 的概率约 1.6%/128B，只看一个帧头会把 TS/FLV 流误判成
 * ADTS（那样视频频道会整条变成"没声没画"）。真 ADTS 流帧帧相连，这个条件恒成立。
 */
export function probeAdtsStream(data: Uint8Array): boolean {
  const offset = findAdtsSync(data, 0);
  if (offset < 0 || offset > 64) return false; // 真流从第 0 字节起；允许少量前导垃圾，不容忍大偏移
  const first = parseAdtsFrame(data, offset);
  if (!first || first.frameLength <= first.headerLength) return false;
  const secondOffset = offset + first.frameLength;
  if (secondOffset + 7 > data.length) return true; // 数据还不够验证第二帧：认（真流马上会补齐）
  return parseAdtsFrame(data, secondOffset) !== null;
}

/**
 * 裸 ADTS AAC 解复用器。
 * 与 TsDemuxer 共用同一份回调契约（见 TsDemuxerCallbacks），上层无需区分二者。
 */
export class AdtsDemuxer {
  private pending: Uint8Array = new Uint8Array(0);
  /** 音轨是否已发布（首帧解析出采样率/声道后才能建 esds）。 */
  private published = false;
  private codec: string | null = null;
  private codecPrivate: Uint8Array | null = null;
  private sampleRate = 0;
  private channels = 0;
  /** 下一样本 dts（单位 = 音频 timescale = 采样率）；跨 push/分片连续。 */
  private nextDts: number | null = null;

  constructor(private readonly callbacks: TsDemuxerCallbacks = {}) {}

  push(chunk: Uint8Array): void {
    const data = this.pending.length > 0 ? concatBytes(this.pending, chunk) : chunk;
    this.pending = new Uint8Array(0);

    let offset = 0;
    const samples: DemuxedSample[] = [];
    while (offset + 7 <= data.length) {
      // 每帧都重新对齐同步字：帧长损坏或前导垃圾时不会一路错位（正常流下一字节就是帧头，等于零成本）
      const sync = findAdtsSync(data, offset);
      if (sync < 0) {
        // 剩下再没有帧头：只留最后 ≤7 字节（帧头可能跨块），其余当垃圾丢掉，避免无界缓存
        offset = Math.max(offset, data.length - 7);
        break;
      }
      offset = sync;
      const frame = parseAdtsFrame(data, offset);
      if (!frame || frame.frameLength < frame.headerLength) {
        offset += 1; // 假同步字：跳过该字节继续找
        continue;
      }
      if (offset + frame.frameLength > data.length) break; // 半帧：留给下一块拼接，否则每块都丢一帧
      this.emitFrame(frame, data.subarray(frame.dataStart, offset + frame.frameLength), samples);
      offset += frame.frameLength;
    }
    // 未消费的尾巴必须留下（块边界极少恰好整除帧长）：丢一次就等于丢一整帧
    // （实测块=7B 时五帧只剩三帧）。只留 ≤7 字节是给"帧头跨块"用的，其余按垃圾丢弃避免无界缓存。
    const tail = offset >= data.length ? EMPTY : data.slice(offset);
    this.pending = tail.length > PENDING_LIMIT ? EMPTY : tail;
    if (samples.length > 0) this.callbacks.onSamples?.(samples);
  }

  private emitFrame(
    frame: NonNullable<ReturnType<typeof parseAdtsFrame>>,
    payload: Uint8Array,
    out: DemuxedSample[],
  ): void {
    // 首帧定基：采样率/声道 → ASC/esds → 发布音轨（+ 布局 = 纯音频）
    if (!this.published) {
      this.published = true;
      this.sampleRate = frame.sampleRate;
      this.channels = frame.channels;
      const asc = buildAudioSpecificConfig(frame.samplingFrequencyIndex, frame.channelConfig, frame.profile);
      this.codecPrivate = buildEsds(asc);
      this.codec = aacCodecMimeType(frame.samplingFrequencyIndex, frame.channelConfig);
      const track: TrackInfo = {
        id: 1,
        kind: "audio",
        codec: this.codec,
        timescale: this.sampleRate,
        channels: this.channels,
        sampleRate: this.sampleRate,
        codecPrivate: this.codecPrivate,
      };
      this.callbacks.onTracks?.([track]);
      // 布局紧随其后上报：MSE 侧据此认定"本条流没有视频轨"，不再等视频 init
      // （否则启动 hold 只会被 2s 兜底放行，纯音频每次起播都要白等）。
      this.callbacks.onStreamLayout?.({ video: false, mseAudio: true, softAudio: false });
    }

    const dts = this.nextDts ?? 0;
    out.push({
      trackId: 1,
      kind: "audio",
      data: payload,
      dts,
      pts: dts,
      cts: 0,
      isKeyframe: true, // 音频每帧均可随机访问
    });
    // 名义帧长递增（避免逐帧换算的累计舍入漂移），与其他解复用器一致
    this.nextDts = dts + AAC_SAMPLES_PER_FRAME;
  }

  /** 新流（切台/重载）时清零：时间轴重新从 0 起算。 */
  reset(): void {
    this.pending = new Uint8Array(0);
    this.published = false;
    this.codec = null;
    this.codecPrivate = null;
    this.sampleRate = 0;
    this.channels = 0;
    this.nextDts = null;
  }
}

function concatBytes(a: Uint8Array, b: Uint8Array): Uint8Array {
  const merged = new Uint8Array(a.byteLength + b.byteLength);
  merged.set(a, 0);
  merged.set(b, a.byteLength);
  return merged;
}
