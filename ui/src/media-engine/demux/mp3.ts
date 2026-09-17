/**
 * MPEG-1/2/2.5 音频（Layer I/II/III）帧解析（clean-room 实现，依据 ISO/IEC 11172-3 / 13818-3 公开规范）。
 *
 * 用途（对应 docs/player-latest-port-design.md C6）：把 PES 负载按帧头给出的帧长**逐帧切分**，
 * 供软解桥一帧一调；帧体跨 payload 时把残尾留作 incomplete data，由调用方拼到下一个 payload
 * 前面（保证进解码器的始终是完整帧）。逐帧 PTS 由调用方（ts-demuxer）按名义帧长连续推导。
 */

/** MPEG-4 Audio Object Type：Layer I/II/III（与 ts-demuxer 的音频元数据同语义）。 */
const MPEG_AUDIO_OBJECT_TYPE_LAYER_1 = 32;
const MPEG_AUDIO_OBJECT_TYPE_LAYER_2 = 33;
const MPEG_AUDIO_OBJECT_TYPE_LAYER_3 = 34;

/** bitrate 表单位 kbps；index 0 = free（无法推算帧长）、15 = invalid。 */
const MPEG1_BITRATES_KBPS = [
  // Layer I
  [0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448],
  // Layer II
  [0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384],
  // Layer III
  [0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320],
] as const;

const MPEG2_BITRATES_KBPS = [
  // Layer I
  [0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256],
  // Layer II / III
  [0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160],
  [0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160],
] as const;

const MPEG1_SAMPLE_RATES = [44100, 48000, 32000] as const;
const MPEG2_SAMPLE_RATES = [22050, 24000, 16000] as const;
const MPEG25_SAMPLE_RATES = [11025, 12000, 8000] as const;

/** 单个 MPEG 音频帧（Layer I/II/III，MPEG-1/2/2.5）。 */
export interface MP3Frame {
  /** MPEG 版本：1 = MPEG-1，2 = MPEG-2，25 = MPEG-2.5。 */
  version: 1 | 2 | 25;
  /** 层：1 = Layer I，2 = Layer II，3 = Layer III。 */
  layer: 1 | 2 | 3;
  bitrate: number;
  samplingFrequency: number;
  channelMode: number;
  channelCount: number;
  /** 本帧采样数（Layer I = 384；Layer II = 1152；Layer III = MPEG-1 1152 / MPEG-2 与 2.5 576）。 */
  samplesPerFrame: number;
  /** 帧长（字节，含头部与 padding）。 */
  frameSize: number;
  /** MPEG-4 Audio Object Type（32/33/34）。 */
  objectType: number;
  data: Uint8Array;
}

/**
 * 解析 `offset` 处的 MPEG 音频帧头（4 字节），无效返回 null。
 *
 * 只做静态头部校验（版本/层/bitrate/sampling rate 合法），不检查帧体是否完整 —— 帧长由头部
 * 自身给出，完整性由调用方按 `frameSize` 判定（跨 PES 的半帧留作 carry）。
 */
export function parseMPEGAudioFrameHeader(data: Uint8Array, offset: number): MP3Frame | null {
  if (offset < 0 || offset + 4 > data.byteLength) {
    return null;
  }

  // 11-bit sync：0xFFE0 起
  if (data[offset] !== 0xff || (data[offset + 1] & 0xe0) !== 0xe0) {
    return null;
  }

  const versionBits = (data[offset + 1] >>> 3) & 0x03;
  const layerBits = (data[offset + 1] >>> 1) & 0x03;
  const bitrateIndex = (data[offset + 2] >>> 4) & 0x0f;
  const samplingFreqIndex = (data[offset + 2] >>> 2) & 0x03;
  const padding = (data[offset + 2] >>> 1) & 0x01;
  const channelMode = (data[offset + 3] >>> 6) & 0x03;

  if (versionBits === 1 || layerBits === 0) {
    return null; // reserved
  }
  if (bitrateIndex === 0 || bitrateIndex === 0x0f) {
    return null; // free-format / invalid：无法推算帧长
  }
  if (samplingFreqIndex === 0x03) {
    return null; // reserved
  }

  const version: 1 | 2 | 25 = versionBits === 3 ? 1 : versionBits === 2 ? 2 : 25;
  const layer: 1 | 2 | 3 = layerBits === 3 ? 1 : layerBits === 2 ? 2 : 3;

  const samplingFrequency =
    version === 1
      ? MPEG1_SAMPLE_RATES[samplingFreqIndex]
      : version === 2
        ? MPEG2_SAMPLE_RATES[samplingFreqIndex]
        : MPEG25_SAMPLE_RATES[samplingFreqIndex];
  if (!samplingFrequency) {
    return null;
  }

  const bitrateTable = version === 1 ? MPEG1_BITRATES_KBPS : MPEG2_BITRATES_KBPS;
  const bitrateKbps = bitrateTable[layer - 1][bitrateIndex];
  if (!bitrateKbps) {
    return null;
  }
  const bitrate = bitrateKbps * 1000;

  let samplesPerFrame: number;
  let frameSize: number;
  if (layer === 1) {
    samplesPerFrame = 384;
    frameSize = (Math.floor((12 * bitrate) / samplingFrequency) + padding) * 4;
  } else if (layer === 2) {
    samplesPerFrame = 1152;
    frameSize = Math.floor((144 * bitrate) / samplingFrequency) + padding;
  } else {
    // Layer III：MPEG-1 为 1152 采样/帧，MPEG-2 与 2.5 为 576
    samplesPerFrame = version === 1 ? 1152 : 576;
    const coefficient = version === 1 ? 144 : 72;
    frameSize = Math.floor((coefficient * bitrate) / samplingFrequency) + padding;
  }

  if (frameSize < 4) {
    return null;
  }

  return {
    version,
    layer,
    bitrate,
    samplingFrequency,
    channelMode,
    channelCount: channelMode !== 3 ? 2 : 1,
    samplesPerFrame,
    frameSize,
    objectType:
      layer === 1
        ? MPEG_AUDIO_OBJECT_TYPE_LAYER_1
        : layer === 2
          ? MPEG_AUDIO_OBJECT_TYPE_LAYER_2
          : MPEG_AUDIO_OBJECT_TYPE_LAYER_3,
    data: data.subarray(offset, Math.min(offset + frameSize, data.byteLength)),
  };
}

/** 跨 PES 残余的 carry 上限（字节）：单帧最长 < 2KB，超出即视为垃圾流，丢弃避免无界增长。 */
const MP3_CARRY_MAX = 8192;

/**
 * MPEG 音频帧切分器（与 `AC3Parser` / `EAC3Parser` 同形）。
 *
 * 按 syncword 扫描并依头部给出的帧长切帧；帧体跨 payload 时把残余留作 incomplete data，
 * 由调用方拼到下一个 PES payload 前面（保证**每帧都是完整帧**再送解码器）。
 */
export class MP3Parser {
  private readonly data: Uint8Array;
  private frameOffset: number;
  /** 最后一个完整帧的结束偏移：跨 PES 的残余从它开始留，保证帧头/帧体都不丢。 */
  private lastFrameEndOffset = 0;
  /** 残余处已看到合法帧头、但帧体未收全（跨 PES 的半帧）。 */
  private hasIncompleteData = false;
  private eof = false;

  constructor(data: Uint8Array) {
    this.data = data;
    this.frameOffset = this.findNextFrameOffset(0);
  }

  private findNextFrameOffset(from: number): number {
    const data = this.data;
    let i = Math.max(0, from);
    while (i + 4 <= data.byteLength) {
      if (parseMPEGAudioFrameHeader(data, i) !== null) {
        return i;
      }
      i++;
    }
    this.eof = true;
    return data.byteLength;
  }

  /** 取下一完整帧；帧体未收全时返回 null（残尾由 getIncompleteData 返回）。 */
  readNextFrame(): MP3Frame | null {
    const data = this.data;
    let frame: MP3Frame | null = null;

    while (frame === null) {
      if (this.eof) break;

      const offset = this.frameOffset;
      const parsed = parseMPEGAudioFrameHeader(data, offset);
      if (parsed === null) {
        // findNextFrameOffset 之后不应发生；防御：跳过 1 字节继续找
        this.frameOffset = this.findNextFrameOffset(offset + 1);
        continue;
      }

      if (offset + parsed.frameSize > data.byteLength) {
        // 帧体未收全（跨 PES）：留作 incomplete data，等下一个 payload 拼完
        this.eof = true;
        this.hasIncompleteData = true;
        break;
      }

      this.frameOffset = this.findNextFrameOffset(offset + parsed.frameSize);
      this.lastFrameEndOffset = offset + parsed.frameSize;
      frame = parsed;
    }

    return frame;
  }

  /**
   * 上一个完整帧之后的残余（跨 PES 的半帧，含只剩 1~3 字节帧头的切点）。
   *
   * 判定用 `lastFrameEndOffset` 而不是当前扫描偏移：尾段里找不到合法帧头时后者会被推到
   * payload 末尾，那 1~3 字节的帧头碎片就会丢，跨 PES 的那一帧被整帧丢弃。
   * 只有"残余处确实是一个帧的开头"才留（帧头可见但帧体未收全 / 仅剩帧头 1~3 字节）；
   * 其余（payload 起点就在帧中间、帧间垃圾）不留，避免垃圾被无界地带进后续 payload。
   */
  getIncompleteData(): Uint8Array | null {
    const start = this.lastFrameEndOffset;
    const leftover = this.data.byteLength - start;
    if (leftover <= 0 || leftover > MP3_CARRY_MAX) {
      return null;
    }
    if (this.hasIncompleteData) {
      return this.data.subarray(start);
    }
    // 帧头被切在 1~3 字节：等下一个 payload 补完帧头
    if (leftover < 4 && this.data[start] === 0xff && (leftover === 1 || (this.data[start + 1] & 0xe0) === 0xe0)) {
      return this.data.subarray(start);
    }
    return null;
  }
}
