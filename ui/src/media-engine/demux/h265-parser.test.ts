/**
 * H.265 参数集解析 / hvcC 组装回归（真实素材，ffmpeg 自产）。
 *
 * 素材：`testdata/test-h265.flv`（Enhanced-RTMP H.265，320x180@15fps，Main profile 8bit 4:2:0）。
 * 断言口径：
 *   - SPS 解析出的 codec 串 / 尺寸 / 色度 / 位深 / PTL 字段 / 帧率必须与素材一致；
 *   - hvcC 记录的固定头逐字节校验（这是 Chrome 解析 init 段的输入，错一个字节就播不了）；
 *   - Annex-B NAL 读取器：3/4 字节起始码、forbidden_zero_bit 丢弃、payload 切分。
 */
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { FlvDemuxer } from "./flv-demuxer";
import { buildHvcC, HevcAnnexBReader, HevcNaluType } from "./h265";
import { parseHevcPps, parseHevcSps, parseHevcVps } from "./h265-parser";

/** 跑一遍 FLV 素材，取视频轨的 hvcC box（含 8 字节 box 头）。 */
function fixtureHvcC(): Uint8Array {
  const data = new Uint8Array(readFileSync(new URL("./testdata/test-h265.flv", import.meta.url)));
  let codecPrivate: Uint8Array | undefined;
  const demuxer = new FlvDemuxer({
    onTracks: (tracks) => {
      for (const track of tracks) {
        if (track.kind === "video" && track.codecPrivate) {
          codecPrivate = track.codecPrivate;
        }
      }
    },
    onSamples: () => {},
  });
  demuxer.push(data);
  if (!codecPrivate) {
    throw new Error("素材未产出 hvcC");
  }
  return codecPrivate;
}

/** 从 hvcC 记录里取出指定类型的 NAL（含 2 字节 NAL 头）。 */
function hvcCNalu(box: Uint8Array, wantType: number): Uint8Array {
  let p = 8 + 23; // box 头 + record 固定部分
  const numArrays = box[8 + 22];
  for (let a = 0; a < numArrays; a++) {
    const type = box[p] & 0x3f;
    const numNalus = (box[p + 1] << 8) | box[p + 2];
    p += 3;
    for (let n = 0; n < numNalus; n++) {
      const length = (box[p] << 8) | box[p + 1];
      const nalu = box.subarray(p + 2, p + 2 + length);
      p += 2 + length;
      if (type === wantType) {
        return nalu;
      }
    }
  }
  throw new Error(`hvcC 中找不到类型为 ${wantType} 的 NAL`);
}

const box = fixtureHvcC();
const VPS = hvcCNalu(box, HevcNaluType.Vps);
const SPS = hvcCNalu(box, HevcNaluType.Sps);
const PPS = hvcCNalu(box, HevcNaluType.Pps);

describe("H.265 参数集解析（真实素材）", () => {
  it("VPS：时域层数与嵌套标志", () => {
    expect(parseHevcVps(VPS)).toEqual({ numTemporalLayers: 1, temporalIdNested: true });
  });

  it("SPS：codec 串 / 尺寸 / 色度位深 / PTL / 帧率", () => {
    const info = parseHevcSps(SPS);
    expect(info.codec).toBe("hvc1.1.1.L60.B0");
    expect(info.size).toEqual({ width: 320, height: 180 });
    expect(info.displaySize).toEqual({ width: 320, height: 180 });
    expect(info.sar).toEqual({ width: 1, height: 1 });
    expect(info.chromaFormatIdc).toBe(1);
    expect(info.chromaFormatName).toBe("4:2:0");
    expect(info.bitDepth).toBe(8);
    expect(info.bitDepthLumaMinus8).toBe(0);
    expect(info.bitDepthChromaMinus8).toBe(0);
    expect(info.profileName).toBe("Main");
    expect(info.levelName).toBe("2.0");
    expect(info.interlaced).toBe(false);
    expect(info.minSpatialSegmentationIdc).toBe(0);
    expect(info.pti).toEqual({
      profileSpace: 0,
      tierFlag: false,
      profileIdc: 1,
      compatibilityFlags: [96, 0, 0, 0],
      constraintFlags: [144, 0, 0, 0, 0, 0],
      levelIdc: 60,
    });
    // VUI timing：time_scale 15 / num_units_in_tick 1 → 15fps
    expect(info.frameRate).toEqual({ fixed: false, fps: 15, numerator: 15, denominator: 1 });
  });

  it("PPS：并行类型 = 波前（entropy sync）", () => {
    expect(parseHevcPps(PPS)).toEqual({ parallelismType: 3 });
  });
});

describe("hvcC 组装", () => {
  it("固定头逐字节正确（configurationVersion/PTL/色度位深/数组计数）", () => {
    const boxed = buildHvcC(VPS, SPS, PPS);
    expect(boxed.byteLength).toBe(120);
    expect(String.fromCharCode(boxed[4], boxed[5], boxed[6], boxed[7])).toBe("hvcC");
    const record = boxed.subarray(8);
    expect(Array.from(record.subarray(0, 23))).toEqual([
      1, // configurationVersion
      1, // profile_space(0) | tier(0) | profile_idc(1)
      96,
      0,
      0,
      0, // general_profile_compatibility_flags
      144,
      0,
      0,
      0,
      0,
      0, // general_constraint_indicator_flags
      60, // general_level_idc
      240,
      0, // min_spatial_segmentation_idc = 0（0xf0 | 高 4 位）
      255, // parallelismType = 3
      253, // chromaFormatIdc = 1
      248, // bitDepthLumaMinus8 = 0
      248, // bitDepthChromaMinus8 = 0
      0,
      0, // avgFrameRate = 0（未知）
      15, // constantFrameRate(0) | numTemporalLayers(1) | temporalIdNested(1) | lengthSizeMinusOne(3)
      3, // numOfArrays
    ]);
  });

  it("尾部按 VPS→SPS→PPS 写入，长度前缀与数据一致", () => {
    const record = buildHvcC(VPS, SPS, PPS).subarray(8);
    let p = 23;
    for (const [type, nalu] of [
      [HevcNaluType.Vps, VPS],
      [HevcNaluType.Sps, SPS],
      [HevcNaluType.Pps, PPS],
    ] as const) {
      expect(record[p]).toBe(0x80 | type); // array_completeness = 1
      expect(record[p + 1]).toBe(0);
      expect(record[p + 2]).toBe(1); // numNalus = 1
      expect((record[p + 3] << 8) | record[p + 4]).toBe(nalu.byteLength);
      expect(Array.from(record.subarray(p + 5, p + 5 + nalu.byteLength))).toEqual(Array.from(nalu));
      p += 5 + nalu.byteLength;
    }
    expect(p).toBe(record.byteLength);
  });
});

describe("Annex-B NAL 读取器", () => {
  it("3/4 字节起始码都能识别，payload 不含起始码", () => {
    const three = new Uint8Array([0, 0, 1, 0x40, 0x01, 0xaa, 0xbb]);
    const four = new Uint8Array([0, 0, 0, 1, 0x42, 0x01, 0xcc]);
    const merged = new Uint8Array(three.byteLength + four.byteLength);
    merged.set(three);
    merged.set(four, three.byteLength);

    const reader = new HevcAnnexBReader(merged);
    const first = reader.readNalu();
    expect(first?.type).toBe(HevcNaluType.Vps); // (0x40 >> 1) & 0x3f = 32
    expect(Array.from(first!.data)).toEqual([0x40, 0x01, 0xaa, 0xbb]);
    const second = reader.readNalu();
    expect(second?.type).toBe(HevcNaluType.Sps); // (0x42 >> 1) & 0x3f = 33
    expect(Array.from(second!.data)).toEqual([0x42, 0x01, 0xcc]);
    expect(reader.readNalu()).toBeNull();
  });

  it("forbidden_zero_bit 为 1 的单元被丢弃", () => {
    const bad = new Uint8Array([0, 0, 1, 0xc0, 0x01, 0xff]); // 0xc0 → forbidden=1
    const good = new Uint8Array([0, 0, 1, 0x40, 0x01, 0xaa]);
    const merged = new Uint8Array(bad.byteLength + good.byteLength);
    merged.set(bad);
    merged.set(good, bad.byteLength);

    const reader = new HevcAnnexBReader(merged);
    expect(reader.readNalu()?.type).toBe(HevcNaluType.Vps);
    expect(reader.readNalu()).toBeNull();
  });

  it("空输入与无起始码输入返回 null", () => {
    expect(new HevcAnnexBReader(new Uint8Array(0)).readNalu()).toBeNull();
    expect(new HevcAnnexBReader(new Uint8Array([1, 2, 3, 4])).readNalu()).toBeNull();
  });
});
