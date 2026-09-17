import { describe, it, expect } from "vitest";
import { MP3Parser, parseMPEGAudioFrameHeader } from "./mp3";

/** 构造一个 MPEG 音频帧：11-bit sync + 版本/层/bitrate/采样率/声道（其余填 0）。 */
function makeFrame(opts: {
  versionBits: number; // 3=MPEG-1, 2=MPEG-2, 0=MPEG-2.5, 1=reserved
  layerBits: number; // 3=Layer I, 2=Layer II, 1=Layer III, 0=reserved
  bitrateIndex: number;
  srIndex: number;
  frameSize: number;
  padding?: number;
}): Uint8Array {
  const buf = new Uint8Array(opts.frameSize);
  buf[0] = 0xff;
  buf[1] = 0xe0 | ((opts.versionBits & 0x03) << 3) | ((opts.layerBits & 0x03) << 1) | 0x01; // protection_bit=1
  buf[2] = ((opts.bitrateIndex & 0x0f) << 4) | ((opts.srIndex & 0x03) << 2) | ((opts.padding ?? 0) << 1);
  buf[3] = 0x00; // 立体声
  return buf;
}

/** MPEG-1 Layer II 48kHz 128kbps → 帧长 384B、1152 采样/帧。 */
const MP2_FRAME = () => makeFrame({ versionBits: 3, layerBits: 2, bitrateIndex: 8, srIndex: 1, frameSize: 384 });

describe("parseMPEGAudioFrameHeader", () => {
  it("解析 MPEG-1 Layer II 帧头（帧长/采样率/采样数/声道/objectType）", () => {
    const f = parseMPEGAudioFrameHeader(MP2_FRAME(), 0);
    expect(f).not.toBeNull();
    expect(f!.version).toBe(1);
    expect(f!.layer).toBe(2);
    expect(f!.samplingFrequency).toBe(48000);
    expect(f!.samplesPerFrame).toBe(1152);
    expect(f!.frameSize).toBe(384);
    expect(f!.channelCount).toBe(2);
    expect(f!.objectType).toBe(33); // Layer II
  });

  it("解析 MPEG-2 Layer III（576 采样/帧，objectType=34）", () => {
    // MPEG-2 Layer III bitrate 表 index 8 = 64kbps；采样率 index 0 = 22050 → floor(72*64000/22050)=208
    const f = parseMPEGAudioFrameHeader(
      makeFrame({ versionBits: 2, layerBits: 1, bitrateIndex: 8, srIndex: 0, frameSize: 208 }),
      0,
    );
    expect(f).not.toBeNull();
    expect(f!.version).toBe(2);
    expect(f!.samplesPerFrame).toBe(576);
    expect(f!.samplingFrequency).toBe(22050);
    expect(f!.frameSize).toBe(208);
    expect(f!.objectType).toBe(34); // Layer III
  });

  it("非法头一律返回 null：reserved 版本/层、free-format、保留采样率、越界", () => {
    // reserved 版本（bits=1）
    expect(parseMPEGAudioFrameHeader(makeFrame({ versionBits: 1, layerBits: 2, bitrateIndex: 8, srIndex: 1, frameSize: 384 }), 0)).toBeNull();
    // reserved 层（bits=0）
    expect(parseMPEGAudioFrameHeader(makeFrame({ versionBits: 3, layerBits: 0, bitrateIndex: 8, srIndex: 1, frameSize: 384 }), 0)).toBeNull();
    // free-format（bitrate index 0）→ 无法推算帧长
    expect(parseMPEGAudioFrameHeader(makeFrame({ versionBits: 3, layerBits: 2, bitrateIndex: 0, srIndex: 1, frameSize: 384 }), 0)).toBeNull();
    // invalid bitrate（index 15）
    expect(parseMPEGAudioFrameHeader(makeFrame({ versionBits: 3, layerBits: 2, bitrateIndex: 15, srIndex: 1, frameSize: 384 }), 0)).toBeNull();
    // 保留采样率（index 3）
    expect(parseMPEGAudioFrameHeader(makeFrame({ versionBits: 3, layerBits: 2, bitrateIndex: 8, srIndex: 3, frameSize: 384 }), 0)).toBeNull();
    // 不足 4 字节
    expect(parseMPEGAudioFrameHeader(new Uint8Array([0xff, 0xe0, 0x90]), 0)).toBeNull();
  });
});

describe("MP3Parser 逐帧切分（C6：一帧一调 + 跨 PES carry）", () => {
  it("连续多帧逐帧切出，每帧长度正确", () => {
    const joined = new Uint8Array(384 * 3);
    for (let i = 0; i < 3; i++) joined.set(MP2_FRAME(), i * 384);

    const p = new MP3Parser(joined);
    for (let i = 0; i < 3; i++) {
      const f = p.readNextFrame();
      expect(f).not.toBeNull();
      expect(f!.frameSize).toBe(384);
      expect(f!.data.byteLength).toBe(384);
      expect(f!.data[0]).toBe(0xff);
    }
    expect(p.readNextFrame()).toBeNull();
    expect(p.getIncompleteData()).toBeNull();
  });

  it("跳过帧间垃圾/坏帧（非同步字节、reserved 头），仍能切出后续合法帧", () => {
    const junk = new Uint8Array([0x00, 0x11, 0x22, 0x33, 0x44]);
    const bad = makeFrame({ versionBits: 1, layerBits: 2, bitrateIndex: 8, srIndex: 1, frameSize: 384 }); // reserved 版本
    const good = MP2_FRAME();
    const joined = new Uint8Array(junk.length + bad.length + good.length);
    joined.set(junk, 0);
    joined.set(bad, junk.length);
    joined.set(good, junk.length + bad.length);

    const p = new MP3Parser(joined);
    const f = p.readNextFrame();
    expect(f).not.toBeNull();
    expect(f!.data.byteLength).toBe(384);
    // 找到的应是 junk+bad 之后那帧（首字节 0xff）
    expect(f!.data[0]).toBe(0xff);
  });

  it("帧体未收全（跨 PES 半帧）：不产出帧，残尾由 getIncompleteData 返回", () => {
    const frame = MP2_FRAME();
    const p = new MP3Parser(frame.subarray(0, 200));

    expect(p.readNextFrame()).toBeNull();
    const inc = p.getIncompleteData();
    expect(inc).not.toBeNull();
    expect(inc!.byteLength).toBe(200);
  });

  it("帧头被切在 1~3 字节：残尾保留，供下个 payload 补完帧头", () => {
    const frame = MP2_FRAME();
    const joined = new Uint8Array(384 + 2);
    joined.set(frame, 0);
    joined[384] = 0xff; // 下一帧只剩 2 字节帧头
    joined[385] = 0xe0;

    const p = new MP3Parser(joined);
    expect(p.readNextFrame()).not.toBeNull();
    const inc = p.getIncompleteData();
    expect(inc).not.toBeNull();
    expect(inc!.byteLength).toBe(2);
  });

  it("垃圾流（无任何合法帧头）不产出帧，且残尾超上限时丢弃（防无界增长）", () => {
    const junk = new Uint8Array(9000);
    junk.fill(0x5a);
    const p = new MP3Parser(junk);
    expect(p.readNextFrame()).toBeNull();
    expect(p.getIncompleteData()).toBeNull(); // > MP3_CARRY_MAX(8192) → 丢弃
  });

  it("跨 payload 拼接后可切出完整帧（carry 语义）", () => {
    const frame = MP2_FRAME();
    const part1 = frame.subarray(0, 200);
    const part2 = frame.subarray(200);

    const p1 = new MP3Parser(part1);
    expect(p1.readNextFrame()).toBeNull();
    const carry = p1.getIncompleteData()!;

    const joined = new Uint8Array(carry.length + part2.length);
    joined.set(carry, 0);
    joined.set(part2, carry.length);
    const p2 = new MP3Parser(joined);
    const f = p2.readNextFrame();
    expect(f).not.toBeNull();
    expect(f!.frameSize).toBe(384);
  });
});
