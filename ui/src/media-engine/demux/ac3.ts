/**
 * AC-3 / E-AC-3 帧解析（clean-room 实现）。
 * 依据 ETSI TS 102 366 / ATSC A/52 公开规范重新实现，复用 demux/exp-golomb 的比特流读取。
 *
 * 用途：把 TS PES 负载里的 AC-3/E-AC-3 比特流按 16-bit 同步字 0x0B77 切分为完整帧，
 * 供软解解码桥逐帧喂入；并暴露采样率/音频块数，便于上层按名义帧长连续推导每帧 PTS。
 *
 * 关键动机（对应 playback-engine 7d69b1c 修复）：某些 Dolby 流在单 PES 含多帧、且 GOP
 * 边界/不连续后 PES PTS 回退或重复。若整 PES 转发，同一 PES 的多帧会被排到同一时间，
 * 导致软解 PCM 时间轴重叠 → AudioContext 卡顿/播放暂停。逐帧切分 + 每帧连续推导 PTS
 * （首帧锚定 PES PTS，其后按 1536/sr 或 256*num_blks/sr 名义帧长累加）可纠正该重叠。
 *
 * 跨 PES 的半帧由 getIncompleteData() 兜底：调用方把残尾拼到下一 PES 头部再解析。
 */

import ExpGolomb from "./exp-golomb";

const AC3_SAMPLING_FREQUENCIES = [48000, 44100, 32000] as const;

// AC-3 帧长表（单位：字 = 2 字节）。行下标 = sampling_rate_code，列下标 = frame_size_code。
const AC3_FRAME_SIZE_WORDS: number[][] = [
  [
    64, 64, 80, 80, 96, 96, 112, 112, 128, 128, 160, 160, 192, 192, 224, 224, 256, 256, 320, 320, 384,
    384, 448, 448, 512, 512, 640, 640, 768, 768, 896, 896, 1024, 1024, 1152, 1152, 1280, 1280,
  ],
  [
    69, 70, 87, 88, 104, 105, 121, 122, 139, 140, 174, 175, 208, 209, 243, 244, 278, 279, 348, 349, 417,
    418, 487, 488, 557, 558, 696, 697, 835, 836, 975, 976, 1114, 1115, 1253, 1254, 1393, 1394,
  ],
  [
    96, 96, 120, 120, 144, 144, 168, 168, 192, 192, 240, 240, 288, 288, 336, 336, 384, 384, 480, 480, 576,
    576, 672, 672, 768, 768, 960, 960, 1152, 1152, 1344, 1344, 1536, 1536, 1728, 1728, 1920, 1920,
  ],
];

export interface Ac3Frame {
  data: Uint8Array;
  samplingFrequency: number;
  frameSize: number; // 字节
  /** 声道数（含 LFE）；BSI 不可解析时为 undefined。 */
  channels?: number;
}

/** acmod → 声道数（不含 LFE；ETSI TS 102 366 Table 4.2 / A/52 Table 5.1）。 */
const AC3_ACMOD_CHANNELS = [2, 1, 2, 3, 3, 4, 4, 5] as const;

/** 由 acmod + lfeon 得到总声道数。 */
function ac3ChannelCount(acmod: number, lfeon: number): number {
  return (AC3_ACMOD_CHANNELS[acmod] ?? 2) + (lfeon ? 1 : 0);
}

/**
 * 从 AC-3 帧的 BSI 取声道数（ETSI TS 102 366 §4.3.3）：
 * bsid(5) bsmod(3) acmod(3) [cmixlev(2)] [surmixlev(2)] [dsurmod(2)] lfeon(1)
 *
 * bsid > 10 表示该帧其实是 E-AC-3 语法（由 EAC3Parser 处理），此处返回 undefined；
 * 数据不足/结构异常同样返回 undefined —— 声道数只影响媒体信息展示，绝不因此中断解析。
 */
function readAc3ChannelCount(frame: Uint8Array): number | undefined {
  try {
    const gb = new ExpGolomb(frame.subarray(5)); // 跳过 5 字节 syncinfo（syncword+crc1+fscod+frmsizecod）
    const bsid = gb.readBits(5);
    if (bsid > 10) {
      gb.destroy();
      return undefined;
    }
    gb.readBits(3); // bsmod
    const acmod = gb.readBits(3);
    if ((acmod & 0x1) !== 0 && acmod !== 0x1) {
      gb.readBits(2); // cmixlev
    }
    if ((acmod & 0x4) !== 0) {
      gb.readBits(2); // surmixlev
    }
    if (acmod === 0x2) {
      gb.readBits(2); // dsurmod
    }
    const lfeon = gb.readBits(1);
    gb.destroy();
    return ac3ChannelCount(acmod, lfeon);
  } catch {
    return undefined;
  }
}

export class AC3Parser {
  private readonly data: Uint8Array;
  private offset: number;
  private eof = false;

  constructor(data: Uint8Array) {
    this.data = data;
    this.offset = this.findSync(0);
  }

  private findSync(start: number): number {
    const d = this.data;
    let i = start;
    while (true) {
      if (i + 7 >= d.length) {
        this.eof = true;
        return d.length;
      }
      const sw = (d[i] << 8) | d[i + 1];
      if (sw === 0x0b77) return i;
      i++;
    }
  }

  /** 取下一完整帧；无则返回 null（eof 时残尾留在 getIncompleteData）。 */
  readNextFrame(): Ac3Frame | null {
    const d = this.data;
    while (!this.eof) {
      const off = this.offset;
      if (off + 7 > d.length) {
        this.eof = true;
        break;
      }
      const srCode = d[off + 4] >> 6;
      const sf = AC3_SAMPLING_FREQUENCIES[srCode];
      const fsCode = d[off + 4] & 0x3f;
      const words = AC3_FRAME_SIZE_WORDS[srCode]?.[fsCode];
      if (sf === undefined || words === undefined) {
        this.offset = this.findSync(off + 2); // 帧头非法：跳到下一同步字
        continue;
      }
      const frameSize = words * 2;
      if (off + frameSize > d.length) {
        this.eof = true; // 数据不足：半帧残尾由 getIncompleteData 返回
        break;
      }
      const frameData = d.subarray(off, off + frameSize);
      this.offset = this.findSync(off + frameSize);
      return { data: frameData, samplingFrequency: sf, frameSize, channels: readAc3ChannelCount(frameData) };
    }
    return null;
  }

  /** 末尾不完整数据（跨 PES 半帧），无则 null。 */
  getIncompleteData(): Uint8Array | null {
    if (!this.eof) return null;
    const tail = this.data.subarray(this.offset);
    return tail.length > 0 ? tail : null;
  }
}

const EAC3_SAMPLING_FREQUENCIES = [48000, 44100, 32000] as const;
const EAC3_HALF_SAMPLING_FREQUENCIES = [24000, 22050, 16000] as const;
const EAC3_BLOCKS_PER_SYNCFRAME = [1, 2, 3, 6] as const;

export interface Eac3Frame {
  data: Uint8Array;
  samplingFrequency: number;
  numBlks: number; // 每同步帧音频块数（每块 256 样本）
  /** 声道数（含 LFE）。 */
  channels?: number;
}

export class EAC3Parser {
  private readonly data: Uint8Array;
  private offset: number;
  private eof = false;

  constructor(data: Uint8Array) {
    this.data = data;
    this.offset = this.findSync(0);
  }

  private findSync(start: number): number {
    const d = this.data;
    let i = start;
    while (true) {
      if (i + 7 >= d.length) {
        this.eof = true;
        return d.length;
      }
      const sw = (d[i] << 8) | d[i + 1];
      if (sw === 0x0b77) return i;
      i++;
    }
  }

  readNextFrame(): Eac3Frame | null {
    const d = this.data;
    while (!this.eof) {
      const off = this.offset;
      if (off + 5 > d.length) {
        this.eof = true;
        break;
      }
      let frame: Eac3Frame | null = null;
      try {
        const gb = new ExpGolomb(d.subarray(off + 2));
        gb.readBits(2); // stream_type
        gb.readBits(3); // sub_stream_id
        const frSizeWords = gb.readBits(11);
        const frameSize = (frSizeWords + 1) * 2;
        const srCode = gb.readBits(2);
        let sf: number | undefined;
        let numBlksCode: number | undefined;
        if (srCode === 0x03) {
          const half = gb.readBits(2);
          sf = EAC3_HALF_SAMPLING_FREQUENCIES[half];
          numBlksCode = 3;
        } else {
          sf = EAC3_SAMPLING_FREQUENCIES[srCode];
          numBlksCode = gb.readBits(2);
        }
        if (
          sf === undefined ||
          numBlksCode === undefined ||
          EAC3_BLOCKS_PER_SYNCFRAME[numBlksCode] === undefined
        ) {
          gb.destroy();
          this.offset = this.findSync(off + 2);
          continue;
        }
        const numBlks = EAC3_BLOCKS_PER_SYNCFRAME[numBlksCode];
        // 紧随 fscod/numblkscod 之后的是 acmod(3) + lfeon(1)（ETSI TS 102 366 §E.1.2.2），
        // 由它们得到真实声道数（媒体信息徽标显示"立体声/5.1"）。
        const acmod = gb.readBits(3);
        const lfeon = gb.readBits(1);
        if (off + frameSize > d.length) {
          this.eof = true;
          gb.destroy();
          break;
        }
        frame = {
          data: d.subarray(off, off + frameSize),
          samplingFrequency: sf,
          numBlks,
          channels: ac3ChannelCount(acmod, lfeon),
        };
        this.offset = this.findSync(off + frameSize);
        gb.destroy();
      } catch {
        // 头域不足或非法：跳到下一同步字重试
        this.offset = this.findSync(off + 2);
        continue;
      }
      if (frame) return frame;
    }
    return null;
  }

  getIncompleteData(): Uint8Array | null {
    if (!this.eof) return null;
    const tail = this.data.subarray(this.offset);
    return tail.length > 0 ? tail : null;
  }
}
