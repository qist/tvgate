import { describe, it, expect } from "vitest";
import { AC3Parser, EAC3Parser } from "./ac3";

/** 构造一个合法 AC-3 同步帧：syncword 0x0B77 + 帧头（含采样率/帧长码）+ 其余填 0。 */
function makeAc3Frame(srCode: number, fsCode: number, frameSize: number): Uint8Array {
  const buf = new Uint8Array(frameSize);
  buf[0] = 0x0b;
  buf[1] = 0x77;
  // byte[4] 高 2 位 = sampling_rate_code，低 6 位 = frame_size_code
  buf[4] = ((srCode & 0x03) << 6) | (fsCode & 0x3f);
  return buf;
}

/**
 * 在 AC-3 帧里写入 BSI（bsid/bsmod/acmod/可选混音位/lfeon），用于声道数回归。
 * 位序：byte5 = bsid(5)|bsmod(3)；byte6 = acmod(3)|[cmixlev(2)]|[surmixlev(2)]|[dsurmod(2)]|lfeon(1)。
 */
function writeAc3Bsi(frame: Uint8Array, acmod: number, lfeon: 0 | 1, bsid = 8): void {
  frame[5] = ((bsid & 0x1f) << 3) | 0; // bsmod = 0
  if (acmod === 7) {
    // 3/2：cmixlev + surmixlev + lfeon 恰好填满一个字节
    frame[6] = (0b111 << 5) | (0 << 3) | (0 << 1) | lfeon;
  } else if (acmod === 2) {
    // 2/0：dsurmod(2) + lfeon(1)
    frame[6] = (0b010 << 5) | (0 << 3) | (lfeon << 2);
  } else {
    // 1/0 等：仅 lfeon
    frame[6] = (acmod << 5) | (lfeon << 4);
  }
}

describe("AC3Parser", () => {
  it("按帧切分完整比特流，返回采样率与帧长", () => {
    // sr=48000(srCode=0) fsCode=20 → 384 字 → 768 字节
    const a = makeAc3Frame(0, 20, 768);
    const b = makeAc3Frame(0, 20, 768);
    const joined = new Uint8Array(768 * 2);
    joined.set(a, 0);
    joined.set(b, 768);

    const p = new AC3Parser(joined);
    const f1 = p.readNextFrame();
    expect(f1).not.toBeNull();
    expect(f1!.frameSize).toBe(768);
    expect(f1!.samplingFrequency).toBe(48000);
    const f2 = p.readNextFrame();
    expect(f2!.frameSize).toBe(768);
    expect(p.readNextFrame()).toBeNull();
    expect(p.getIncompleteData()).toBeNull();
  });

  it("解析声道数：2/0 立体声 → 2；3/2 + LFE → 6（5.1）", () => {
    // bsid ≤ 10 → AC-3 语法
    const stereo = makeAc3Frame(0, 20, 768);
    writeAc3Bsi(stereo, 2, 0);
    expect(new AC3Parser(stereo).readNextFrame()!.channels).toBe(2);

    const surround = makeAc3Frame(0, 20, 768);
    writeAc3Bsi(surround, 7, 1);
    expect(new AC3Parser(surround).readNextFrame()!.channels).toBe(6);

    const mono = makeAc3Frame(0, 20, 768);
    writeAc3Bsi(mono, 1, 0);
    expect(new AC3Parser(mono).readNextFrame()!.channels).toBe(1);
  });

  it("BSI 指示 bsid>10（实为 E-AC-3 语法）时不误报声道数", () => {
    const frame = makeAc3Frame(0, 20, 768);
    writeAc3Bsi(frame, 7, 1, 16);
    expect(new AC3Parser(frame).readNextFrame()!.channels).toBeUndefined();
  });

  it("跳过非法同步字并暴露跨 PES 半帧残尾", () => {
    const frame = makeAc3Frame(0, 19, 768);
    const partial = frame.subarray(0, 400); // 截断：不足一帧
    const p = new AC3Parser(partial);
    expect(p.readNextFrame()).toBeNull();
    const inc = p.getIncompleteData();
    expect(inc).not.toBeNull();
    expect(inc!.length).toBe(400);
  });

  it("未知采样率码时跳到下一同步字不卡死", () => {
    const frame = makeAc3Frame(0, 19, 768);
    frame[4] = (7 << 6) | 19; // srCode=7 非法
    const p = new AC3Parser(frame);
    expect(p.readNextFrame()).toBeNull();
  });
});

describe("EAC3Parser", () => {
  /** 构造一个合法 E-AC-3 同步帧：syncword + 头（fscod/num-blocks/帧长）+ 其余填 0。
   * 注意 E-AC-3 规范：fscod===3（半采样率）时 **无** numblkscod 字段，
   * 代之以 fscod2（half-rate 码），块数固定为 6；此时 blkOrHalf 作为 half-rate 码。 */
  function makeEac3Frame(frameSize: number, srCode: number, blkOrHalf: number): Uint8Array {
    const buf = new Uint8Array(frameSize);
    buf[0] = 0x0b;
    buf[1] = 0x77;
    const frCode = (frameSize / 2) - 1; // 11-bit frame_size_code，frameSize=(frCode+1)*2
    buf[2] = (frCode >> 8) & 0x07; // 高 3 位落在 byte[2] 低 3 位（stream_type/sub_stream_id 为 0）
    buf[3] = frCode & 0xff; // 低 8 位
    buf[4] = ((srCode & 0x03) << 6) | ((blkOrHalf & 0x03) << 4);
    return buf;
  }

  it("按帧切分并返回采样率/块数", () => {
    // sr=48000(srCode=0) numBlksCode=3(→6 blocks=1536 样本) frameSize=256
    const frame = makeEac3Frame(256, 0, 3);
    const p = new EAC3Parser(frame);
    const f = p.readNextFrame();
    expect(f).not.toBeNull();
    expect(f!.samplingFrequency).toBe(48000);
    expect(f!.numBlks).toBe(6);
    expect(p.readNextFrame()).toBeNull();
    expect(p.getIncompleteData()).toBeNull();
  });

  it("解析声道数：acmod=7 + LFE → 6（5.1），acmod=2 → 2", () => {
    const surround = makeEac3Frame(256, 0, 3);
    surround[4] = (surround[4] & 0xf0) | (7 << 1) | 1; // acmod(3) + lfeon(1)
    expect(new EAC3Parser(surround).readNextFrame()!.channels).toBe(6);

    const stereo = makeEac3Frame(256, 0, 3);
    stereo[4] = (stereo[4] & 0xf0) | (2 << 1) | 0;
    expect(new EAC3Parser(stereo).readNextFrame()!.channels).toBe(2);
  });

  it("半采样率（srCode=3）解析正确", () => {
    // srCode=3 → half rate：byte[4] 高 2 位=0b11，半采样率码=0(→24000)，块数固定 6
    const frame = makeEac3Frame(256, 3, 0);
    const p = new EAC3Parser(frame);
    const f = p.readNextFrame();
    expect(f).not.toBeNull();
    expect([24000, 22050, 16000]).toContain(f!.samplingFrequency);
    expect(f!.numBlks).toBe(6);
  });
});

