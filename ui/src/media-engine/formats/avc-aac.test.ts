import { describe, it, expect } from "vitest";
import {
  BitReader,
  splitAnnexB,
  annexBToAvcc,
  findStartCode,
  nalUnitType,
  buildAvcC,
  NAL_TYPE_SPS,
  NAL_TYPE_PPS,
} from "./avc";
import { parseAdtsFrame, buildAudioSpecificConfig, buildEsds } from "./aac";
import { parseAudioObjectType } from "./mp4-box";

describe("AnnexB", () => {
  it("拆分起始码流为 NALU", () => {
    const sps = [0x67, 0x64, 0x00, 0x1e];
    const pps = [0x68, 0xce, 0x3c, 0x80];
    const stream = new Uint8Array([0, 0, 0, 1, ...sps, 0, 0, 1, ...pps, 0, 0, 1, 0x65, 1, 2]);
    const nalus = splitAnnexB(stream);
    expect(nalus).toHaveLength(3);
    expect(nalUnitType(nalus[0])).toBe(NAL_TYPE_SPS);
    expect(nalUnitType(nalus[1])).toBe(NAL_TYPE_PPS);
    expect(nalUnitType(nalus[2])).toBe(5); // IDR
  });

  it("AnnexB → 4 字节长度前缀（AVCC）", () => {
    const stream = new Uint8Array([0, 0, 1, 0x65, 1, 2, 3, 0, 0, 1, 0x41, 9]);
    const avcc = annexBToAvcc(stream);
    // 两个 NALU（4+2 字节）各 +4 前缀
    expect(avcc.length).toBe(4 + 2 + 8);
    expect(avcc[0]).toBe(0); // 长度高字节
    expect(avcc[3]).toBe(4); // 第一 NALU 长度 4
    expect(avcc[4]).toBe(0x65);
    // 第二 NALU 长度前缀 = 2（前缀2 位于偏移 8..11）
    expect((avcc[8] << 24) | (avcc[9] << 16) | (avcc[10] << 8) | avcc[11]).toBe(2);
    expect(avcc[12]).toBe(0x41);
  });

  it("findStartCode 支持 3/4 字节", () => {
    expect(findStartCode(new Uint8Array([0, 0, 1, 5]), 0)).toEqual({ start: 0, length: 3 });
    expect(findStartCode(new Uint8Array([9, 0, 0, 0, 1, 5]), 0)).toEqual({ start: 1, length: 4 });
  });
});

describe("BitReader / 指数哥伦布", () => {
  // 指数哥伦布 ue：0→'1'，1→'010'，2→'011'，3→'00100'
  // se：k=1 → codeNum 1 → '010'；k=-1 → codeNum 2 → '011'
  it("ue 解码已知向量", () => {
    // '1' + '010' + '011' = 0b1010011 → 填充后 [0xA6, 0x00]
    const br = new BitReader(new Uint8Array([0xa6, 0x00]));
    expect(br.ue()).toBe(0);
    expect(br.ue()).toBe(1);
    expect(br.ue()).toBe(2);
  });
  it("se 解码正负", () => {
    // '010' + '011' = 0b010011 → [0x4C, 0x00]
    const br = new BitReader(new Uint8Array([0x4c, 0x00]));
    expect(br.se()).toBe(1);
    expect(br.se()).toBe(-1);
  });
  it("bit() 逐位读取", () => {
    const br = new BitReader(new Uint8Array([0b1011_0000]));
    expect([br.bit(), br.bit(), br.bit(), br.bit()]).toEqual([1, 0, 1, 1]);
  });
});

describe("buildAvcC", () => {
  it("输出合法 avcC box（含 SPS/PPS 数量与长度）", () => {
    const sps = new Uint8Array([0x67, 0x64, 0x00, 0x1e, 0xac, 0xd9]);
    const pps = new Uint8Array([0x68, 0xce, 0x3c, 0x80]);
    const avcc = buildAvcC([sps], [pps]);
    expect(String.fromCharCode(avcc[4], avcc[5], avcc[6], avcc[7])).toBe("avcC");
    // configurationVersion = 1
    expect(avcc[8]).toBe(1);
    // SPS count
    expect(avcc[13] & 0x1f).toBe(1);
    // SPS 长度字段 = 6
    expect((avcc[14] << 8) | avcc[15]).toBe(sps.length);
    // PPS count
    const ppsStart = 16 + sps.length;
    expect(avcc[ppsStart]).toBe(1);
    expect((avcc[ppsStart + 1] << 8) | avcc[ppsStart + 2]).toBe(pps.length);
    // 总长度正确
    expect(avcc.length).toBe(8 + 6 + (2 + sps.length) + 1 + (2 + pps.length));
  });
});

describe("AAC ADTS", () => {
  // 48000 Hz 双声道 AAC-LC，帧长 28 的 7 字节头
  const adts = new Uint8Array([0xff, 0xf1, 0x4c, 0x80, 0x03, 0x80, 0x00, ...new Array(21).fill(0xaa)]);

  it("解析 ADTS 头", () => {
    const f = parseAdtsFrame(adts, 0)!;
    expect(f.profile).toBe(1); // AAC-LC
    expect(f.samplingFrequencyIndex).toBe(3);
    expect(f.sampleRate).toBe(48000);
    expect(f.channelConfig).toBe(2);
    expect(f.channels).toBe(2);
    expect(f.frameLength).toBe(28);
    expect(f.headerLength).toBe(7);
  });

  it("AudioSpecificConfig 布局（Chrome 走 4 字节 HE-AAC 信令）", () => {
    const asc = buildAudioSpecificConfig(3, 2);
    // Chrome/非 Firefox+Android：声明 AOT=5（HE-AAC 信令），占 4 字节
    expect(asc.length).toBe(4);
    expect(asc[0] >> 3).toBe(5);
    // 采样率索引 3
    expect(((asc[0] & 0x07) << 1) | (asc[1] >> 7)).toBe(3);
    // 声道 2
    expect((asc[1] >> 3) & 0x0f).toBe(2);
    // 扩展段强制回 LC-AAC（extended AOT=2）
    expect(asc[2] & 0x0c).toBe(0x08);
  });

  it("buildEsds 描述符链可解析出 AudioObjectType=5", () => {
    const esds = buildEsds(buildAudioSpecificConfig(3, 2));
    expect(String.fromCharCode(esds[4], esds[5], esds[6], esds[7])).toBe("esds");
    // 生产解析器按描述符层级下钻，应还原 AOT=5（HE-AAC 信令）
    expect(parseAudioObjectType(esds)).toBe(5);
  });
});
