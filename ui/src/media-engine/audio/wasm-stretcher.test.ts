/**
 * WSOLA wasm 回归测试（clean-room 内核 `wasm/wsola.c` + 封装 `wasm-stretcher.ts`）。
 *
 * 覆盖点：
 * 1. 直通：ratio ≈ 1 时输出与输入**位精确**相同（零失真路径，音质保真核心）。
 * 2. 变速：ratio = 1.2 输出变短、0.8 输出变长，比例与 1/ratio 一致（±5%）。
 * 3. 多声道：1/2/6 声道可创建（5.1 直通依赖），7 声道按上限拒绝。
 * 4. 生命周期：reset 位置归零且保留 ratio；destroy 后不再产出。
 */
import { describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { WasmTimeStretcher } from "./wasm-stretcher";

const SAMPLE_RATE = 48000;
const WASM_URL = "/wasm/avcodec_audio.wasm";
const wasmBytes = readFileSync(new URL("../wasm/avcodec_audio.wasm", import.meta.url));

/** 生成测试用交错正弦 PCM（各声道幅度递增，便于区分声道）。 */
function sinePcm(frames: number, channels: number, freq = 440): Float32Array {
  const pcm = new Float32Array(frames * channels);
  for (let i = 0; i < frames; i++) {
    const value = 0.5 * Math.sin((2 * Math.PI * freq * i) / SAMPLE_RATE);
    for (let c = 0; c < channels; c++) {
      pcm[i * channels + c] = value * (c + 1);
    }
  }
  return pcm;
}

/** 让封装层从内存字节加载 wasm（node 环境没有静态服务器）。 */
function stubWasmFetch(): void {
  vi.stubGlobal("fetch", async () => new Response(wasmBytes, { headers: { "content-type": "application/wasm" } }));
}

/** 分块喂入并把输出拼接起来，返回总输出帧数。 */
function feedAll(stretcher: WasmTimeStretcher, channels: number, totalFrames: number, chunkFrames = 4096): number {
  const source = sinePcm(totalFrames + chunkFrames, channels);
  let produced = 0;
  for (let fed = 0; fed < totalFrames; fed += chunkFrames) {
    const frames = Math.min(chunkFrames, totalFrames - fed);
    const out = stretcher.process(source.subarray(fed * channels, (fed + frames) * channels));
    produced += out.length / channels;
  }
  return produced;
}

describe("WasmTimeStretcher（WSOLA wasm 封装）", () => {
  it("ratio=1：直通位精确，position 与喂入帧数一致", async () => {
    stubWasmFetch();
    const stretcher = await WasmTimeStretcher.create(WASM_URL, SAMPLE_RATE, 2);
    expect(stretcher.sampleRate).toBe(SAMPLE_RATE);
    expect(stretcher.channels).toBe(2);

    const input = sinePcm(4096, 2);
    const output = stretcher.process(input);

    expect(output.length).toBe(input.length);
    expect(output).toEqual(input);
    expect(stretcher.position).toBeCloseTo(4096, 6);
    stretcher.destroy();
  });

  it("ratio=1.2 输出变短、0.8 输出变长，长度比 = 1/ratio", async () => {
    stubWasmFetch();
    const totalFrames = SAMPLE_RATE * 2;

    const faster = await WasmTimeStretcher.create(WASM_URL, SAMPLE_RATE, 2);
    faster.setRatio(1.2);
    const fasterOut = feedAll(faster, 2, totalFrames);
    faster.destroy();

    const slower = await WasmTimeStretcher.create(WASM_URL, SAMPLE_RATE, 2);
    slower.setRatio(0.8);
    const slowerOut = feedAll(slower, 2, totalFrames);
    slower.destroy();

    expect(fasterOut / totalFrames).toBeGreaterThan(1 / 1.2 - 0.04);
    expect(fasterOut / totalFrames).toBeLessThan(1 / 1.2 + 0.02);
    expect(slowerOut / totalFrames).toBeGreaterThan(1 / 0.8 - 0.04);
    expect(slowerOut / totalFrames).toBeLessThan(1 / 0.8 + 0.02);
    expect(slowerOut).toBeGreaterThan(fasterOut);
  });

  it("支持 1/2/6 声道，7 声道（超出上限）抛错", async () => {
    stubWasmFetch();
    for (const channels of [1, 2, 6]) {
      const stretcher = await WasmTimeStretcher.create(WASM_URL, SAMPLE_RATE, channels);
      const output = stretcher.process(sinePcm(2048, channels));
      expect(output.length / channels).toBeGreaterThan(0);
      stretcher.destroy();
    }
    await expect(WasmTimeStretcher.create(WASM_URL, SAMPLE_RATE, 7)).rejects.toThrow(/wsola_create/);
  });

  it("reset 位置归零并保留 ratio；destroy 后不再产出", async () => {
    stubWasmFetch();
    const stretcher = await WasmTimeStretcher.create(WASM_URL, SAMPLE_RATE, 2);
    stretcher.setRatio(1.2);
    stretcher.process(sinePcm(4096, 2));
    expect(stretcher.position).toBeGreaterThan(0);

    stretcher.reset();
    expect(stretcher.position).toBe(0);

    const afterReset = stretcher.process(sinePcm(99000, 2));
    // ratio 保留 → 输出仍按变速比例（≈1/1.2），不是直通长度。
    expect(afterReset.length / 2).toBeLessThan(99000 * 0.9);

    stretcher.destroy();
    expect(stretcher.process(sinePcm(2048, 2)).length).toBe(0);
    expect(stretcher.position).toBe(0);
  });
});
