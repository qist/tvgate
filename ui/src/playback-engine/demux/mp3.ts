export class MP3Data {
  object_type!: number;
  sample_rate!: number;
  channel_count!: number;

  data!: Uint8Array;
}

/** MPEG-4 Audio Object Type：Layer I/II/III（与 ts-demuxer 的 MP3Data.object_type 同语义）。 */
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

/** 单个 MPEG 音频帧（Layer I/II/III，MPEG-1/2/2.5）的头部字段与帧体。 */
export class MP3Frame {
  /** MPEG 版本：1 = MPEG-1，2 = MPEG-2，25 = MPEG-2.5。 */
  version!: 1 | 2 | 25;
  /** 层：1 = Layer I，2 = Layer II，3 = Layer III。 */
  layer!: 1 | 2 | 3;
  bitrate!: number;
  sampling_frequency!: number;
  channel_mode!: number;
  channel_count!: number;
  /** 本帧采样数（Layer I = 384；Layer II = 1152；Layer III = MPEG-1 1152 / MPEG-2 与 2.5 576）。 */
  samples_per_frame!: number;
  /** 帧长（字节，含头部与 padding）。 */
  frame_size!: number;
  /** MPEG-4 Audio Object Type（32/33/34）。 */
  object_type!: number;
  data!: Uint8Array;
}

/**
 * 解析 `offset` 处的 MPEG 音频帧头（4 字节），无效返回 null。
 *
 * 只做静态头部校验（版本/层/bitrate/sampling rate 合法），不检查帧体是否完整 ——
 * 帧长由头部自身给出，完整性由调用方按 `frame_size` 判定（跨 PES 的半帧留作 carry）。
 */
export function parseMPEGAudioFrameHeader(data: Uint8Array, offset: number): MP3Frame | null {
  if (offset < 0 || offset + 4 > data.byteLength) {
    return null;
  }

  // 11-bit sync：0xFFE0 起
  if (data[offset] !== 0xff || (data[offset + 1] & 0xe0) !== 0xe0) {
    return null;
  }

  const version_bits = (data[offset + 1] >>> 3) & 0x03;
  const layer_bits = (data[offset + 1] >>> 1) & 0x03;
  const bitrate_index = (data[offset + 2] >>> 4) & 0x0f;
  const sampling_freq_index = (data[offset + 2] >>> 2) & 0x03;
  const padding = (data[offset + 2] >>> 1) & 0x01;
  const channel_mode = (data[offset + 3] >>> 6) & 0x03;

  if (version_bits === 1 || layer_bits === 0) {
    return null; // reserved
  }
  if (bitrate_index === 0 || bitrate_index === 0x0f) {
    return null; // free-format / invalid：无法推算帧长
  }
  if (sampling_freq_index === 0x03) {
    return null; // reserved
  }

  const version: 1 | 2 | 25 = version_bits === 3 ? 1 : version_bits === 2 ? 2 : 25;
  const layer: 1 | 2 | 3 = layer_bits === 3 ? 1 : layer_bits === 2 ? 2 : 3;

  const sampling_frequency =
    version === 1
      ? MPEG1_SAMPLE_RATES[sampling_freq_index]
      : version === 2
        ? MPEG2_SAMPLE_RATES[sampling_freq_index]
        : MPEG25_SAMPLE_RATES[sampling_freq_index];
  if (!sampling_frequency) {
    return null;
  }

  const bitrate_table = version === 1 ? MPEG1_BITRATES_KBPS : MPEG2_BITRATES_KBPS;
  const bitrate_kbps = bitrate_table[layer - 1][bitrate_index];
  if (!bitrate_kbps) {
    return null;
  }
  const bitrate = bitrate_kbps * 1000;

  let samples_per_frame: number;
  let frame_size: number;
  if (layer === 1) {
    samples_per_frame = 384;
    frame_size = (Math.floor((12 * bitrate) / sampling_frequency) + padding) * 4;
  } else if (layer === 2) {
    samples_per_frame = 1152;
    frame_size = Math.floor((144 * bitrate) / sampling_frequency) + padding;
  } else {
    // Layer III：MPEG-1 为 1152 采样/帧，MPEG-2 与 2.5 为 576
    samples_per_frame = version === 1 ? 1152 : 576;
    const coefficient = version === 1 ? 144 : 72;
    frame_size = Math.floor((coefficient * bitrate) / sampling_frequency) + padding;
  }

  if (frame_size < 4) {
    return null;
  }

  const frame = new MP3Frame();
  frame.version = version;
  frame.layer = layer;
  frame.bitrate = bitrate;
  frame.sampling_frequency = sampling_frequency;
  frame.channel_mode = channel_mode;
  frame.channel_count = channel_mode !== 3 ? 2 : 1;
  frame.samples_per_frame = samples_per_frame;
  frame.frame_size = frame_size;
  frame.object_type =
    layer === 1
      ? MPEG_AUDIO_OBJECT_TYPE_LAYER_1
      : layer === 2
        ? MPEG_AUDIO_OBJECT_TYPE_LAYER_2
        : MPEG_AUDIO_OBJECT_TYPE_LAYER_3;
  frame.data = data.subarray(offset, Math.min(offset + frame_size, data.byteLength));
  return frame;
}

/** 跨 PES 残余的 carry 上限（字节）：单帧最长 < 2KB，超出即视为垃圾流，丢弃避免无界增长。 */
const MP3_CARRY_MAX = 8192;

/**
 * MPEG 音频帧切分器（与 `AC3Parser` / `EAC3Parser` 同形）。
 *
 * 按 syncword 扫描并依头部给出的帧长切帧；帧体跨 payload 时把残余留作 incomplete data，
 * 由调用方拼到下一个 PES payload 前面（保证**每帧都是完整帧**再送 wasm 解码）。
 */
export class MP3Parser {
  private data_!: Uint8Array;
  private current_frame_offset_!: number;
  /** 最后一个完整帧的结束偏移：跨 PES 的残余从它开始留，保证帧头/帧体都不丢。 */
  private last_frame_end_offset_ = 0;
  /** 残余处已看到合法帧头、但帧体未收全（跨 PES 的半帧）。 */
  private has_last_incomplete_data = false;
  private eof_flag_ = false;

  public constructor(data: Uint8Array) {
    this.data_ = data;
    this.current_frame_offset_ = this.findNextFrameOffset(0);
  }

  private findNextFrameOffset(frame_offset: number): number {
    let i = Math.max(0, frame_offset);
    const data = this.data_;

    while (i + 4 <= data.byteLength) {
      if (parseMPEGAudioFrameHeader(data, i) !== null) {
        return i;
      }
      i++;
    }

    this.eof_flag_ = true;
    return data.byteLength;
  }

  public readNextMP3Frame(): MP3Frame | null {
    const data = this.data_;
    let frame: MP3Frame | null = null;

    while (frame == null) {
      if (this.eof_flag_) {
        break;
      }

      const frame_offset = this.current_frame_offset_;
      const parsed = parseMPEGAudioFrameHeader(data, frame_offset);
      if (parsed === null) {
        // findNextFrameOffset 之后不应发生；防御：跳过 1 字节继续找
        this.current_frame_offset_ = this.findNextFrameOffset(frame_offset + 1);
        continue;
      }

      if (frame_offset + parsed.frame_size > data.byteLength) {
        // 帧体未收全（跨 PES）：留作 incomplete data，等下一个 payload 拼完
        this.eof_flag_ = true;
        this.has_last_incomplete_data = true;
        break;
      }

      this.current_frame_offset_ = this.findNextFrameOffset(frame_offset + parsed.frame_size);
      this.last_frame_end_offset_ = frame_offset + parsed.frame_size;
      frame = parsed;
    }

    return frame;
  }

  /**
   * 上一个完整帧之后的残余（跨 PES 的半帧，含只剩 1~3 字节帧头的切点）。
   *
   * 判定用 `last_frame_end_offset_` 而不是 `current_frame_offset_`：尾段里找不到合法帧头时
   * 后者会被推到 payload 末尾，那 1~3 字节的帧头碎片就会丢，跨 PES 的那一帧被整帧丢弃。
   * 只有"残余处确实是一个帧的开头"才留（帧头已可见但帧体未收全 / 仅剩帧头 1~3 字节）；
   * 其余（payload 起点就在帧中间、帧间垃圾）不留，避免垃圾被无界地带进后续 payload。
   */
  public getIncompleteData(): Uint8Array | null {
    const start = this.last_frame_end_offset_;
    const leftover = this.data_.byteLength - start;
    if (leftover <= 0 || leftover > MP3_CARRY_MAX) {
      return null;
    }
    if (this.has_last_incomplete_data) {
      return this.data_.subarray(start);
    }
    // 帧头被切在 1~3 字节：等下一个 payload 补完帧头
    if (leftover < 4 && this.data_[start] === 0xff && (leftover === 1 || (this.data_[start + 1] & 0xe0) === 0xe0)) {
      return this.data_.subarray(start);
    }
    return null;
  }
}
