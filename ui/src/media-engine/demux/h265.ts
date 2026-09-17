/**
 * H.265/HEVC 的 Annex-B NAL 扫描与 hvcC 组装（本项目自研，clean-room）。
 *
 * 规范依据：
 *   - ITU-T H.265 §7.3.1.2：NAL unit header（forbidden_zero_bit 1 + nal_unit_type 6 +
 *     nuh_layer_id 6 + nuh_temporal_id_plus1 3），即头两字节；
 *   - ITU-T H.265 Annex B：起始码分隔的字节流（扫描逻辑见 `annexb.ts`）；
 *   - ISO/IEC 14496-15 §8.3.3.1：HEVCDecoderConfigurationRecord（hvcC box 载荷）。
 */
import { findAnnexBStartCodeOffset } from "./annexb";
import { type HevcPpsInfo, type HevcSpsInfo, parseHevcPps, parseHevcSps, parseHevcVps } from "./h265-parser";

/** 本项目用到的 NAL 单元类型（ITU-T H.265 Table 7-1）。 */
export enum HevcNaluType {
  /** 随机接入的关键帧（IDR_W_RADL / IDR_N_LP / CRA）。 */
  IdrWRadl = 19,
  IdrNLp = 20,
  Cra = 21,
  Vps = 32,
  Sps = 33,
  Pps = 34,
  Aud = 35,
}

/** 一条 NAL 单元（`data` 含 2 字节 NAL 头）。 */
export interface HevcNalu {
  type: HevcNaluType;
  data: Uint8Array;
}

/** 起始码之后的 NAL 头偏移：4 字节起始码（00 00 00 01）还是 3 字节（00 00 01）。 */
function naluHeaderOffset(data: Uint8Array, startCodeOffset: number): number {
  const fourByte =
    startCodeOffset + 3 < data.byteLength &&
    data[startCodeOffset] === 0x00 &&
    data[startCodeOffset + 1] === 0x00 &&
    data[startCodeOffset + 2] === 0x00 &&
    data[startCodeOffset + 3] === 0x01;
  return startCodeOffset + (fourByte ? 4 : 3);
}

/**
 * Annex-B 字节流上的 NAL 读取器：每次 `readNalu()` 返回一条 NAL（含头两字节），
 * 流尾返回 null。forbidden_zero_bit 非 0 的单元按损坏丢弃（规范要求该位恒为 0）。
 */
export class HevcAnnexBReader {
  private readonly data: Uint8Array;
  /** 下一条起始码的位置（= 已读到末尾时等于 data.byteLength）。 */
  private cursor: number;

  constructor(data: Uint8Array) {
    this.data = data;
    this.cursor = findAnnexBStartCodeOffset(data, 0);
  }

  readNalu(): HevcNalu | null {
    const data = this.data;
    while (this.cursor < data.byteLength) {
      const header = naluHeaderOffset(data, this.cursor);
      const next = findAnnexBStartCodeOffset(data, header);
      this.cursor = next;
      if (header >= next) {
        continue; // 空 NAL（相邻起始码）：没有可读内容
      }
      if ((data[header] & 0x80) !== 0) {
        continue; // forbidden_zero_bit 非 0：数据损坏
      }
      const type = ((data[header] >> 1) & 0x3f) as HevcNaluType;
      return { type, data: data.subarray(header, next) };
    }
    return null;
  }
}

/** hvcC（HEVCDecoderConfigurationRecord）内容参数。 */
export interface HvcCParams {
  pti: HevcSpsInfo["pti"];
  minSpatialSegmentationIdc: number;
  parallelismType: HevcPpsInfo["parallelismType"];
  chromaFormatIdc: number;
  bitDepthLumaMinus8: number;
  bitDepthChromaMinus8: number;
  constantFrameRate: number;
  numTemporalLayers: number;
  temporalIdNested: boolean;
}

/** 写入 4 字节大端长度 + 4 字节类型（box 头）。 */
function writeBoxHeader(out: Uint8Array, offset: number, size: number, type: string): void {
  out[offset] = (size >>> 24) & 0xff;
  out[offset + 1] = (size >>> 16) & 0xff;
  out[offset + 2] = (size >>> 8) & 0xff;
  out[offset + 3] = size & 0xff;
  for (let i = 0; i < 4; i++) {
    out[offset + 4 + i] = type.charCodeAt(i);
  }
}

/**
 * 组装 HEVCDecoderConfigurationRecord（ISO/IEC 14496-15 §8.3.3.1）。
 * 记录尾部按 VPS → SPS → PPS 顺序写入 NAL 数组，长度前缀长度固定 4 字节（lengthSizeMinusOne=3），
 * 与样本里 NAL 长度前缀的宽度一致。
 */
export function buildHvcCRecord(params: HvcCParams, nalus: Array<[HevcNaluType, Uint8Array]>): Uint8Array {
  const { pti } = params;
  // 固定 23 字节 + 每个数组 3 字节头 + 每个 NAL 2 字节长度 + 数据
  let size = 23;
  for (const [, nalu] of nalus) {
    size += 3 + 2 + nalu.byteLength;
  }

  const out = new Uint8Array(size);
  let p = 0;
  out[p++] = 1; // configurationVersion
  out[p++] = ((pti.profileSpace & 0x03) << 6) | ((pti.tierFlag ? 1 : 0) << 5) | (pti.profileIdc & 0x1f);
  out[p++] = pti.compatibilityFlags[0];
  out[p++] = pti.compatibilityFlags[1];
  out[p++] = pti.compatibilityFlags[2];
  out[p++] = pti.compatibilityFlags[3];
  out[p++] = pti.constraintFlags[0];
  out[p++] = pti.constraintFlags[1];
  out[p++] = pti.constraintFlags[2];
  out[p++] = pti.constraintFlags[3];
  out[p++] = pti.constraintFlags[4];
  out[p++] = pti.constraintFlags[5];
  out[p++] = pti.levelIdc;
  out[p++] = 0xf0 | ((params.minSpatialSegmentationIdc >> 8) & 0x0f);
  out[p++] = params.minSpatialSegmentationIdc & 0xff;
  out[p++] = 0xfc | (params.parallelismType & 0x03);
  out[p++] = 0xfc | (params.chromaFormatIdc & 0x03);
  out[p++] = 0xf8 | (params.bitDepthLumaMinus8 & 0x07);
  out[p++] = 0xf8 | (params.bitDepthChromaMinus8 & 0x07);
  out[p++] = 0; // avgFrameRate（16 bit，未知 = 0）
  out[p++] = 0;
  // constantFrameRate(2) | numTemporalLayers(3) | temporalIdNested(1) | lengthSizeMinusOne(2)
  out[p++] =
    ((params.constantFrameRate & 0x03) << 6) |
    ((params.numTemporalLayers & 0x07) << 3) |
    ((params.temporalIdNested ? 1 : 0) << 2) |
    3;
  out[p++] = nalus.length; // numOfArrays

  for (const [type, nalu] of nalus) {
    out[p++] = 0x80 | type; // array_completeness = 1
    out[p++] = 0; // numNalus 高字节
    out[p++] = 1; // numNalus = 1
    out[p++] = (nalu.byteLength >> 8) & 0xff;
    out[p++] = nalu.byteLength & 0xff;
    out.set(nalu, p);
    p += nalu.byteLength;
  }

  return out;
}

/**
 * 由 VPS/SPS/PPS 构造 hvcC **box**（含 8 字节 box 头），供 fMP4 init 的 codecPrivate 使用。
 *
 * 为什么必须带 box 头：`formats/mp4-generator.ts` 把它原样塞进 `hvc1` sample entry 的
 * codecPrivate 字段；只给裸记录会让 Chrome 解析 init 段失败 → SourceBuffer error。
 * （与 `avc.ts` 的 avcC、`aac.ts` 的 esds 同一约定。）
 */
export function buildHvcC(vps: Uint8Array, sps: Uint8Array, pps: Uint8Array): Uint8Array {
  const vpsInfo = parseHevcVps(vps);
  const spsInfo = parseHevcSps(sps);
  const ppsInfo = parseHevcPps(pps);

  const record = buildHvcCRecord(
    {
      pti: spsInfo.pti,
      minSpatialSegmentationIdc: spsInfo.minSpatialSegmentationIdc,
      parallelismType: ppsInfo.parallelismType,
      chromaFormatIdc: spsInfo.chromaFormatIdc,
      bitDepthLumaMinus8: spsInfo.bitDepthLumaMinus8,
      bitDepthChromaMinus8: spsInfo.bitDepthChromaMinus8,
      // 0 = 未知：上游流未声明恒定帧率，不编造该信息。
      constantFrameRate: 0,
      numTemporalLayers: vpsInfo.numTemporalLayers,
      temporalIdNested: vpsInfo.temporalIdNested,
    },
    [
      [HevcNaluType.Vps, vps],
      [HevcNaluType.Sps, sps],
      [HevcNaluType.Pps, pps],
    ],
  );

  const box = new Uint8Array(record.byteLength + 8);
  writeBoxHeader(box, 0, box.byteLength, "hvcC");
  box.set(record, 8);
  return box;
}
