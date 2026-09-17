/**
 * H.264 / AVC 基本流工具（clean-room 实现）。
 * 依据 ISO/IEC 14496-10（AVC）公开规范重新实现：AnnexB 起始码扫描与 NALU 拆分、
 * AnnexB↔AVCC(length-prefixed) 转换、SPS 解析（分辨率/profile/level）、avcC 构造。
 * 均为无状态纯算法，供 demux/remux 复用。
 */

export const NAL_TYPE_SPS = 7;
export const NAL_TYPE_PPS = 8;
export const NAL_TYPE_IDR = 5;

/** 位读取器（含指数哥伦布解码）。 */
export class BitReader {
  private pos = 0;
  private bitPos = 0;
  constructor(private readonly buf: Uint8Array) {}

  bit(): number {
    const v = (this.buf[this.pos] >> (7 - this.bitPos)) & 0x01;
    this.advance(1);
    return v;
  }

  u(bits: number): number {
    let v = 0;
    for (let i = 0; i < bits; i++) v = (v << 1) | this.bit();
    return v >>> 0;
  }

  /** 无符号指数哥伦布 ue(v)。 */
  ue(): number {
    let zeros = 0;
    while (this.pos < this.buf.length && this.bit() === 0) zeros++;
    if (zeros === 0) return 0;
    return (1 << zeros) - 1 + this.u(zeros);
  }

  /** 有符号指数哥伦布 se(v)。 */
  se(): number {
    const k = this.ue();
    return k % 2 === 1 ? (k + 1) / 2 : -k / 2;
  }

  private advance(n: number): void {
    this.bitPos += n;
    while (this.bitPos >= 8) {
      this.bitPos -= 8;
      this.pos++;
    }
  }

  get eof(): boolean {
    return this.pos >= this.buf.length;
  }
}

/** 去掉 RBSP 里的防竞争字节（00 00 03 → 00 00）。 */
export function removeEmulationPrevention(nal: Uint8Array): Uint8Array {
  const out: number[] = [];
  let zeroCount = 0;
  for (let i = 0; i < nal.length; i++) {
    const b = nal[i];
    if (zeroCount >= 2 && b === 0x03) {
      zeroCount = 0;
      continue;
    }
    out.push(b);
    zeroCount = b === 0x00 ? zeroCount + 1 : 0;
  }
  return new Uint8Array(out);
}

/** 从 offset 起查找 AnnexB 起始码（3 或 4 字节）。返回起始码位置与其长度。 */
export function findStartCode(data: Uint8Array, offset: number): { start: number; length: number } {
  let i = Math.max(0, offset);
  let zeros = 0;
  for (; i < data.length; i++) {
    if (data[i] === 0x00) {
      zeros++;
    } else if (data[i] === 0x01 && zeros >= 2) {
      return { start: i - zeros, length: zeros + 1 };
    } else {
      zeros = 0;
    }
  }
  return { start: -1, length: 0 };
}

/** 拆分 AnnexB 流为 NALU 列表（不含起始码）。 */
export function splitAnnexB(data: Uint8Array): Uint8Array[] {
  const nalus: Uint8Array[] = [];
  let sc = findStartCode(data, 0);
  while (sc.start >= 0) {
    const bodyStart = sc.start + sc.length;
    const next = findStartCode(data, bodyStart);
    if (next.start < 0) {
      nalus.push(data.subarray(bodyStart, data.length));
      break;
    }
    nalus.push(data.subarray(bodyStart, next.start));
    sc = next;
  }
  return nalus;
}

export function nalUnitType(nal: Uint8Array): number {
  return nal[0] & 0x1f;
}

export interface SpsInfo {
  profileIdc: number;
  constraintFlags: number;
  levelIdc: number;
  width: number;
  height: number;
  frameMbsOnly: boolean;
  chromaFormatIdc: number;
}

/** 生成 MSE 所需的完整 AVC codec 串（avc1.PPCCLL）。裸 "avc1" 在 Chrome isTypeSupported 下会返回 false。 */
export function buildAvc1CodecString(info: { profileIdc: number; constraintFlags: number; levelIdc: number }): string {
  const hex = (v: number) => v.toString(16).padStart(2, "0");
  return `avc1.${hex(info.profileIdc)}${hex(info.constraintFlags)}${hex(info.levelIdc)}`;
}

/** 解析 SPS NALU（不含起始码，含 1 字节 NAL 头）。 */
export function parseSps(nal: Uint8Array): SpsInfo | null {
  if (nal.length < 4) return null;
  const rbsp = removeEmulationPrevention(nal.subarray(1));
  const br = new BitReader(rbsp);
  try {
    const profileIdc = br.u(8);
    const constraintFlags = br.u(8);
    const levelIdc = br.u(8);
    br.ue(); // seq_parameter_set_id

    let chromaFormatIdc = 1;
    if (profileIdc === 100 || profileIdc === 110 || profileIdc === 122 || profileIdc === 244 ||
      profileIdc === 44 || profileIdc === 83 || profileIdc === 86 || profileIdc === 118 ||
      profileIdc === 128 || profileIdc === 138 || profileIdc === 139 || profileIdc === 134 ||
      profileIdc === 135) {
      chromaFormatIdc = br.ue();
      if (chromaFormatIdc === 3) br.bit(); // separate_colour_plane_flag
      br.ue(); // bit_depth_luma_minus8
      br.ue(); // bit_depth_chroma_minus8
      br.bit(); // qpprime_y_zero_transform_bypass_flag
      const seqScalingMatrixPresent = br.bit();
      if (seqScalingMatrixPresent) skipScalingLists(br, chromaFormatIdc);
    }

    br.ue(); // log2_max_frame_num_minus4
    const picOrderCntType = br.ue();
    if (picOrderCntType === 0) {
      br.ue(); // log2_max_pic_order_cnt_lsb_minus4
    } else if (picOrderCntType === 1) {
      br.bit(); // delta_pic_order_always_zero_flag
      br.se(); // offset_for_non_ref_pic
      br.se(); // offset_for_top_to_bottom_field
      const cycles = br.ue();
      for (let i = 0; i < cycles; i++) br.se();
    }

    br.ue(); // max_num_ref_frames
    br.bit(); // gaps_in_frame_num_value_allowed_flag
    const picWidthInMbsMinus1 = br.ue();
    const picHeightInMapUnitsMinus1 = br.ue();
    const frameMbsOnly = br.bit() === 1;
    if (!frameMbsOnly) br.bit(); // mb_adaptive_frame_field_flag
    br.bit(); // direct_8x8_inference_flag

    let cropLeft = 0;
    let cropRight = 0;
    let cropTop = 0;
    let cropBottom = 0;
    if (br.bit() === 1) {
      cropLeft = br.ue();
      cropRight = br.ue();
      cropTop = br.ue();
      cropBottom = br.ue();
    }

    const cropUnitX = chromaFormatIdc === 0 || chromaFormatIdc === 3 ? 1 : 2;
    const cropUnitY = (chromaFormatIdc === 1 || chromaFormatIdc === 2 ? 2 : 1) * (frameMbsOnly ? 1 : 2);
    const width = (picWidthInMbsMinus1 + 1) * 16 - (cropLeft + cropRight) * cropUnitX;
    const height = (2 - (frameMbsOnly ? 1 : 0)) * (picHeightInMapUnitsMinus1 + 1) * 16 -
      (cropTop + cropBottom) * cropUnitY;

    return { profileIdc, constraintFlags, levelIdc, width, height, frameMbsOnly, chromaFormatIdc };
  } catch {
    return null;
  }
}

function skipScalingLists(br: BitReader, chromaFormatIdc: number): void {
  const count = chromaFormatIdc !== 3 ? 8 : 12;
  for (let i = 0; i < count; i++) {
    if (br.bit() === 0) continue; // seq_scaling_list_present_flag[i]
    const size = i < 6 ? 16 : 64;
    let lastScale = 8;
    let nextScale = 8;
    for (let j = 0; j < size; j++) {
      if (nextScale !== 0) {
        const delta = br.se();
        nextScale = (lastScale + delta + 256) % 256;
        lastScale = nextScale === 0 ? lastScale : nextScale;
      }
    }
  }
}

/** NALU 列表 → AVCC（4 字节大端长度前缀），供样本负载用。 */
export function avccFromNalus(nalus: Uint8Array[], lengthSize = 4): Uint8Array {
  const parts: Uint8Array[] = [];
  for (const nalu of nalus) {
    const prefix = new Uint8Array(lengthSize);
    let len = nalu.length;
    for (let i = lengthSize - 1; i >= 0; i--) {
      prefix[i] = len & 0xff;
      len >>>= 8;
    }
    parts.push(prefix, nalu);
  }
  let total = 0;
  for (const p of parts) total += p.length;
  const out = new Uint8Array(total);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

/** AnnexB → AVCC：把起始码替换为 4 字节大端长度前缀。 */
export function annexBToAvcc(data: Uint8Array, lengthSize = 4): Uint8Array {
  const parts: Uint8Array[] = [];
  let sc = findStartCode(data, 0);
  while (sc.start >= 0) {
    const bodyStart = sc.start + sc.length;
    const next = findStartCode(data, bodyStart);
    const end = next.start < 0 ? data.length : next.start;
    const nalu = data.subarray(bodyStart, end);
    const prefix = new Uint8Array(lengthSize);
    let len = nalu.length;
    for (let i = lengthSize - 1; i >= 0; i--) {
      prefix[i] = len & 0xff;
      len >>>= 8;
    }
    parts.push(prefix, nalu);
    if (next.start < 0) break;
    sc = next;
  }
  let total = 0;
  for (const p of parts) total += p.length;
  const out = new Uint8Array(total);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

function u32Bytes(v: number): Uint8Array {
  return new Uint8Array([(v >>> 24) & 0xff, (v >>> 16) & 0xff, (v >>> 8) & 0xff, v & 0xff]);
}

function box(type: string, payload: Uint8Array): Uint8Array {
  const t = new Uint8Array(4);
  for (let i = 0; i < 4; i++) t[i] = type.charCodeAt(i);
  const out = new Uint8Array(payload.length + 8);
  out.set(u32Bytes(payload.length + 8), 0);
  out.set(t, 4);
  out.set(payload, 8);
  return out;
}

/** 由 SPS/PPS 构造 avcC box（fMP4 init 的 codecPrivate）。 */
export function buildAvcC(spsList: Uint8Array[], ppsList: Uint8Array[]): Uint8Array {
  const sps = spsList[0];
  const info = sps ? parseSps(sps) : null;
  const profile = info ? info.profileIdc : 0x42;
  const compat = info ? info.constraintFlags : 0x00;
  const level = info ? info.levelIdc : 0x1e;

  const parts: Uint8Array[] = [
    new Uint8Array([1, profile, compat, level, 0xff, 0xe0 | (spsList.length & 0x1f)]),
  ];
  for (const s of spsList) {
    parts.push(new Uint8Array([(s.length >>> 8) & 0xff, s.length & 0xff]), s);
  }
  parts.push(new Uint8Array([ppsList.length & 0xff]));
  for (const p of ppsList) {
    parts.push(new Uint8Array([(p.length >>> 8) & 0xff, p.length & 0xff]), p);
  }

  let total = 0;
  for (const p of parts) total += p.length;
  const payload = new Uint8Array(total);
  let o = 0;
  for (const p of parts) {
    payload.set(p, o);
    o += p.length;
  }
  return box("avcC", payload);
}
