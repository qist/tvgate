/**
 * 设备输出声道能力探测 + 降混（5.1 → 2.0 / 1.0）。
 * 降混系数按 Web Audio 规范：L' = L + 0.707·C + 0.707·SL，R' 同理，LFE 丢弃。
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { detectAudioOutputChannels, downmixInterleaved, resetAudioOutputChannelsCache } from "./output-channels";

const FOLD = Math.SQRT1_2;

function stubAudioContext(maxChannelCount: number | undefined) {
  class FakeAudioContext {
    destination = { maxChannelCount } as AudioDestinationNode;
    close() {
      return Promise.resolve();
    }
  }
  vi.stubGlobal("AudioContext", FakeAudioContext);
}

afterEach(() => {
  vi.unstubAllGlobals();
  resetAudioOutputChannelsCache();
});

describe("detectAudioOutputChannels", () => {
  it("maxChannelCount ≥ 6 → 6（可放 5.1）", () => {
    stubAudioContext(6);
    expect(detectAudioOutputChannels()).toBe(6);
  });

  it("maxChannelCount = 2 → 2", () => {
    stubAudioContext(2);
    expect(detectAudioOutputChannels()).toBe(2);
  });

  it("能力不可知（无 maxChannelCount / 无 AudioContext / 抛错）→ 保守 2", () => {
    stubAudioContext(undefined);
    expect(detectAudioOutputChannels()).toBe(2);

    resetAudioOutputChannelsCache();
    vi.stubGlobal("AudioContext", undefined);
    expect(detectAudioOutputChannels()).toBe(2);

    resetAudioOutputChannelsCache();
    vi.stubGlobal(
      "AudioContext",
      class {
        constructor() {
          throw new Error("blocked");
        }
      },
    );
    expect(detectAudioOutputChannels()).toBe(2);
  });
});

describe("downmixInterleaved", () => {
  it("5.1 → 2.0：C/SL/SR 按 −3dB 折入，LFE 丢弃", () => {
    // L R C LFE SL SR
    const input = new Float32Array([0.5, 0.25, 0.2, 0.9, 0.1, 0.05]);
    const out = downmixInterleaved(input, 6, 2);
    expect(out.channels).toBe(2);
    expect(out.samples[0]).toBeCloseTo(0.5 + FOLD * 0.2 + FOLD * 0.1, 5);
    expect(out.samples[1]).toBeCloseTo(0.25 + FOLD * 0.2 + FOLD * 0.05, 5);
    // LFE（0.9）不得出现在输出里
    expect(out.samples[0]).toBeLessThan(0.9);
  });

  it("5.1 → 2.0：折入后超幅被限幅到 ±1", () => {
    const out = downmixInterleaved(new Float32Array([1, 1, 1, 0, 1, 1]), 6, 2);
    expect(out.samples[0]).toBe(1);
    expect(out.samples[1]).toBe(1);
    const neg = downmixInterleaved(new Float32Array([-1, -1, -1, 0, -1, -1]), 6, 2);
    expect(neg.samples[0]).toBe(-1);
  });

  it("5.1 → 1.0（mono 档位对多声道同样生效）", () => {
    const input = new Float32Array([0.6, 0.2, 0.4, 0.9, 0.1, 0.1]);
    const out = downmixInterleaved(input, 6, 1);
    expect(out.channels).toBe(1);
    const l = 0.6 + FOLD * 0.4 + FOLD * 0.1;
    const r = 0.2 + FOLD * 0.4 + FOLD * 0.1;
    expect(out.samples[0]).toBeCloseTo((l + r) * 0.5, 5);
  });

  it("2 → 1：左右合成取均值", () => {
    const out = downmixInterleaved(new Float32Array([0.4, -0.2]), 2, 1);
    expect(out.channels).toBe(1);
    expect(out.samples[0]).toBeCloseTo(0.1, 6);
  });

  it("目标声道数 >= 源声道数时原样返回（不动 5.1 直通）", () => {
    const input = new Float32Array([0.1, 0.2, 0.3, 0.4, 0.5, 0.6]);
    expect(downmixInterleaved(input, 6, 6).samples).toBe(input);
    expect(downmixInterleaved(input, 6, 0).samples).toBe(input);
  });

  it("多帧按帧交错降混（不是整块重排）", () => {
    // 两帧：帧 0 = L0.5 R0.5 C0 LFE0 SL0 SR0；帧 1 = L1 R1 全 0
    const input = new Float32Array([0.5, 0.5, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0]);
    const out = downmixInterleaved(input, 6, 2);
    expect(out.samples.length).toBe(4);
    expect(out.samples[0]).toBeCloseTo(0.5, 5);
    expect(out.samples[1]).toBeCloseTo(0.5, 5);
    expect(out.samples[2]).toBeCloseTo(1, 5);
    expect(out.samples[3]).toBeCloseTo(1, 5);
  });
});
