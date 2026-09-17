import { describe, it, expect } from "vitest";
import { PcmTimeline } from "./pcm-timeline";

/** 造一段交错 PCM：每声道 n 样本，长度 = n*channels。 */
function pcm(n: number, channels = 2): Float32Array {
  return new Float32Array(n * channels).fill(1);
}

const SR = 48000;

describe("PcmTimeline（对齐原始 mapPcmTimestamp 的 drop/trim/bridge）", () => {
  it("首块整块在视频锚点前 → 丢弃", () => {
    const tl = new PcmTimeline();
    // 时长 0.1s，起点 -0.5s → 终点 -0.4s < 0
    const r = tl.map(-0.5, pcm(4800), SR, 2);
    expect(r).toBeNull();
  });

  it("首块跨锚点 → 裁前面、time 夹到 0", () => {
    const tl = new PcmTimeline();
    // 时长 0.2s，起点 -0.1s → 终点 +0.1s；应裁掉前 0.1s
    const r = tl.map(-0.1, pcm(9600), SR, 2);
    expect(r).not.toBeNull();
    expect(r!.time).toBe(0);
    // 0.1s = 4800 样本/声道，剩余 4800*2 浮点
    expect(r!.pcm.length).toBe(4800 * 2);
  });

  it("首块已在锚点之后 → 原样输出", () => {
    const tl = new PcmTimeline();
    const r = tl.map(0.3, pcm(4800), SR, 2);
    expect(r).not.toBeNull();
    expect(r!.time).toBeCloseTo(0.3, 6);
    expect(r!.pcm.length).toBe(4800 * 2);
  });

  it("后续块出现空洞 → bridge（输出端点不回退、不留缝）", () => {
    const tl = new PcmTimeline();
    const a = tl.map(0.0, pcm(4800), SR, 2); // 0 → 0.1
    expect(a!.time).toBe(0);
    // 下一块起点 0.5（中间缺 0.1~0.5）→ 输出应紧接上一块端点 0.1，不回退
    const b = tl.map(0.5, pcm(4800), SR, 2);
    expect(b!.time).toBeCloseTo(0.1, 6);
  });

  it("后续块重叠（distance<0）→ 输出端点按 distance 前移，不出现负 time", () => {
    const tl = new PcmTimeline();
    tl.map(0.0, pcm(4800), SR, 2); // 0 → 0.1
    // 下一块起点 0.05（重叠）→ 输出 = 0.1 + (0.05-0.1) = 0.05 ≥ 0
    const b = tl.map(0.05, pcm(4800), SR, 2);
    expect(b!.time).toBeGreaterThanOrEqual(0);
    expect(b!.time).toBeCloseTo(0.05, 6);
  });

  it("连续块保持背靠背、time 恒 ≥ 0", () => {
    const tl = new PcmTimeline();
    let prev = -0.05;
    for (let i = 0; i < 5; i++) {
      const r = tl.map(prev, pcm(4800), SR, 2);
      if (r) expect(r.time).toBeGreaterThanOrEqual(0);
      prev += 0.1;
    }
  });
});
