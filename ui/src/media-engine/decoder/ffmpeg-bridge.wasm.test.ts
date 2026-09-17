/**
 * 真实 WASM 软解回归测试（模拟测试音频 → FfmpegWasmBridge → WasmAudioDecoder）。
 *
 * fixtures（testdata/）：ffmpeg 生成的 1s 48kHz 立体声双音（L=440Hz / R=880Hz），
 * 覆盖统一后端宣称的全部 5 种 codec。自产数据，无上游产物。
 *
 * 回归点（均为实际踩坑）：
 * 1. parser 交出内部缓冲帧时 consumed=0 → 旧行为丢弃该帧（AC-3 768B 分块丢一半）。
 * 2. 定点 mp2/mp3 解码器输出 S16P → 旧行为静默丢弃（MP2 无声）。
 * 3. 流末 parser 内部滞留帧需 EOF flush 取回（末帧丢失）。
 * 4. 任意分块尺寸喂入必须与整块喂入等量恢复（分块无损）。
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { createFfmpegBridgeFactory } from "./ffmpeg-bridge";
import { DecoderRegistry, WasmAudioDecoder } from "./wasm-audio-decoder";
import type { DecodedAudio, DecoderCodec } from "./types";

const WASM_URL_SUFFIX = "avcodec_audio.wasm";
const wasmBytes = readFileSync(new URL(`../wasm/${WASM_URL_SUFFIX}`, import.meta.url));

/** codec → 测试 fixture（均为 48kHz 立体声 1s 双音，自产）。 */
const FIXTURES: Record<DecoderCodec, string> = {
  mp2: "tone-48k-stereo.mp2",
  mp3: "tone-48k-stereo.mp3",
  ac3: "tone-48k-stereo.ac3",
  eac3: "tone-48k-stereo.eac3",
  aac: "tone-48k-stereo.aac",
};
const CODEC_IDS: Record<DecoderCodec, number> = { ac3: 0, eac3: 1, mp2: 2, mp3: 3, aac: 4 };

function fixtureBytes(codec: DecoderCodec): Uint8Array {
  return new Uint8Array(readFileSync(new URL(`./testdata/${FIXTURES[codec]}`, import.meta.url)));
}

/** RMS（均方根）：0.4 振幅正弦 ≈ 0.283，静音 ≈ 0。 */
function rms(pcm: Float32Array): number {
  let sum = 0;
  for (let i = 0; i < pcm.length; i++) sum += pcm[i] * pcm[i];
  return Math.sqrt(sum / Math.max(1, pcm.length));
}

/** 过零率（次/秒）：440Hz 双音 ≈ 880，880Hz ≈ 1760。用于验证声道未串。 */
function zeroCrossingsPerSecond(pcm: Float32Array, sampleRate: number): number {
  let crossings = 0;
  for (let i = 1; i < pcm.length; i++) {
    if ((pcm[i - 1] < 0 && pcm[i] >= 0) || (pcm[i - 1] >= 0 && pcm[i] < 0)) crossings++;
  }
  return (crossings / Math.max(1, pcm.length)) * sampleRate;
}

interface StreamStats {
  sampleRate: number;
  channels: number;
  totalSamplesPerChannel: number;
  batches: number;
  maxRms: number;
}

/** 用项目自身的 WasmAudioDecoder 按 chunkSize 流式喂入并统计。 */
async function streamDecode(codec: DecoderCodec, data: Uint8Array, chunkSize: number): Promise<StreamStats> {
  const factory = createFfmpegBridgeFactory();
  const dec = new WasmAudioDecoder(codec, factory.create(codec, `https://test.local/${WASM_URL_SUFFIX}`));
  await dec.init();
  expect(dec.isReady).toBe(true);

  const stats: StreamStats = { sampleRate: 0, channels: 0, totalSamplesPerChannel: 0, batches: 0, maxRms: 0 };
  for (let off = 0; off < data.length; off += chunkSize) {
    const chunk = data.subarray(off, Math.min(off + chunkSize, data.length));
    for (const a of dec.decode(chunk)) accumulate(a, stats);
  }
  for (const a of dec.flush()) accumulate(a, stats); // EOF flush：取回 parser 滞留末帧
  dec.destroy();
  return stats;
}

function accumulate(a: DecodedAudio, stats: StreamStats): void {
  if (stats.sampleRate === 0) stats.sampleRate = a.sampleRate;
  if (stats.channels === 0) stats.channels = a.channels;
  expect(a.sampleRate).toBe(stats.sampleRate);
  expect(a.channels).toBe(stats.channels);
  stats.totalSamplesPerChannel += a.samplesPerChannel;
  stats.batches++;
  stats.maxRms = Math.max(stats.maxRms, rms(a.pcm));
}

beforeEach(() => {
  // Node vitest 无 HTTP 服务：把 fetch 桩到本地 wasm 字节，
  // 走 FfmpegWasmBridge 真实的 instantiateStreaming 加载路径。
  // 每个用例前重打（beforeAll 只跑一次，桩会被其他用例的清理拆掉）。
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string | URL | Request) => {
      const url = input instanceof Request ? input.url : String(input);
      if (url.endsWith(WASM_URL_SUFFIX)) {
        return new Response(new Uint8Array(wasmBytes), {
          headers: { "content-type": "application/wasm" },
        });
      }
      throw new Error(`测试未预期的 fetch: ${url}`);
    }),
  );
});

describe("FFmpeg WASM 统一软解（真实 avcodec_audio.wasm）", () => {
  for (const codec of Object.keys(FIXTURES) as DecoderCodec[]) {
    it(`${codec}: 解出 48kHz 立体声、时长≈1s、非静音`, async () => {
      const stats = await streamDecode(codec, fixtureBytes(codec), 1 << 20);
      expect(stats.sampleRate).toBe(48000);
      expect(stats.channels).toBe(2);
      // 1s 素材（mp3 因编码器延迟略多；ac3/eac3 = 32×1536 = 1.024s）
      expect(stats.totalSamplesPerChannel / 48000).toBeGreaterThanOrEqual(0.99);
      expect(stats.totalSamplesPerChannel / 48000).toBeLessThanOrEqual(1.1);
      expect(stats.maxRms).toBeGreaterThan(0.2); // 非静音且幅度正确
      expect(stats.maxRms).toBeLessThan(0.36);
    });

    it(`${codec}: 任意分块喂入与整块等量恢复（分块无损回归）`, async () => {
      const data = fixtureBytes(codec);
      const full = await streamDecode(codec, data, 1 << 20);
      // 768B 恰是 AC-3 帧长/MP2 半帧边界，最易踩 parser consumed=0 交帧路径
      for (const chunkSize of [1024, 768, 333]) {
        const stats = await streamDecode(codec, data, chunkSize);
        expect(stats.sampleRate).toBe(48000);
        // 分块喂入恢复量不得少于整块喂入的 99%（carry/丢帧回归）
        expect(stats.totalSamplesPerChannel).toBeGreaterThanOrEqual(Math.floor(full.totalSamplesPerChannel * 0.99));
        expect(stats.maxRms).toBeGreaterThan(0.2);
      }
    });
  }

  it("声道未串：左 440Hz / 右 880Hz 过零率符合预期", async () => {
    // 用整块解码的首批输出，偶数槽为 L、奇数槽为 R
    const factory = createFfmpegBridgeFactory();
    const dec = new WasmAudioDecoder("ac3", factory.create("ac3", `https://test.local/${WASM_URL_SUFFIX}`));
    await dec.init();
    const data = fixtureBytes("ac3");
    const batches = dec.decode(data);
    dec.destroy();
    expect(batches.length).toBeGreaterThan(0);
    const a = batches[0];
    const left = new Float32Array(a.samplesPerChannel);
    const right = new Float32Array(a.samplesPerChannel);
    for (let i = 0; i < a.samplesPerChannel; i++) {
      left[i] = a.pcm[i * 2];
      right[i] = a.pcm[i * 2 + 1];
    }
    // 取样本中段，避免首尾滤波器过渡影响
    const seg = Math.floor(a.samplesPerChannel / 4);
    const mid = (x: Float32Array) => x.subarray(seg, a.samplesPerChannel - seg);
    const lz = zeroCrossingsPerSecond(mid(left), a.sampleRate);
    const rz = zeroCrossingsPerSecond(mid(right), a.sampleRate);
    expect(lz).toBeGreaterThan(440 * 2 * 0.8); // ≈880
    expect(lz).toBeLessThan(440 * 2 * 1.2);
    expect(rz).toBeGreaterThan(880 * 2 * 0.8); // ≈1760
    expect(rz).toBeLessThan(880 * 2 * 1.2);
  });

  it("flush 取回流末 parser 滞留帧（AC-3 整块 + flush = 32 帧）", async () => {
    const factory = createFfmpegBridgeFactory();
    const dec = new WasmAudioDecoder("ac3", factory.create("ac3", `https://test.local/${WASM_URL_SUFFIX}`));
    await dec.init();
    const data = fixtureBytes("ac3"); // 24576B = 32×768B 帧
    let samples = 0;
    for (const a of dec.decode(data)) samples += a.samplesPerChannel;
    // flush 前末帧仍在 parser 内部
    expect(samples / 1536).toBe(31);
    for (const a of dec.flush()) samples += a.samplesPerChannel;
    expect(samples / 1536).toBe(32); // EOF flush 取回末帧
    dec.destroy();
  });

  it("DecoderRegistry + 真实桥：5 codec 全部可用", async () => {
    const registry = new DecoderRegistry(createFfmpegBridgeFactory(), {
      wasmDecoders: Object.fromEntries(
        (Object.keys(CODEC_IDS) as DecoderCodec[]).map((c) => [c, `https://test.local/${WASM_URL_SUFFIX}`]),
      ),
    });
    for (const codec of Object.keys(CODEC_IDS) as DecoderCodec[]) {
      expect(registry.supports(codec)).toBe(true);
      const dec = await registry.acquire(codec);
      expect(dec).not.toBeNull();
      expect(dec!.isReady).toBe(true);
    }
    registry.destroy();
  });

  it("AC-3 5.1 直通：解出 6 声道、逐声道非静音（不再塌成 2.0）", async () => {
    const factory = createFfmpegBridgeFactory();
    const dec = new WasmAudioDecoder("ac3", factory.create("ac3", `https://test.local/${WASM_URL_SUFFIX}`));
    await dec.init();
    const data = new Uint8Array(readFileSync(new URL("./testdata/tone-48k-5.1ch.ac3", import.meta.url)));
    const batches: DecodedAudio[] = [];
    for (let off = 0; off < data.length; off += 768) {
      for (const a of dec.decode(data.subarray(off, Math.min(off + 768, data.length)))) batches.push(a);
    }
    for (const a of dec.flush()) batches.push(a);
    dec.destroy();

    expect(batches.length).toBeGreaterThan(0);
    expect(batches[0].sampleRate).toBe(48000);
    for (const a of batches) expect(a.channels).toBe(6); // 5.1 保留直通（旧行为恒为 2）

    const total = batches.reduce((s, a) => s + a.samplesPerChannel, 0);
    expect(total / 48000).toBeGreaterThanOrEqual(1.9);

    const merged = new Float32Array(total * 6);
    let written = 0;
    for (const a of batches) {
      // ABI 中 samplesPerChannel 是"每声道样本"，故数据长度为 samplesPerChannel × channels
      merged.set(a.pcm.subarray(0, a.samplesPerChannel * 6), written);
      written += a.samplesPerChannel * 6;
    }
    for (let c = 0; c < 6; c++) {
      let sum = 0;
      for (let i = 0; i < total; i++) sum += merged[i * 6 + c] * merged[i * 6 + c];
      const chRms = Math.sqrt(sum / Math.max(1, total));
      expect(chRms).toBeGreaterThan(0.1); // 每个声道都有内容
      expect(chRms).toBeLessThan(0.4);
    }
  });

  it("AC-3 5.1 目标 2.0（设备不支持 5.1）：WASM 直接解出 2 声道且非静音", async () => {
    const factory = createFfmpegBridgeFactory(2); // outChannels=2：解码器内部降混
    const dec = new WasmAudioDecoder("ac3", factory.create("ac3", `https://test.local/${WASM_URL_SUFFIX}`));
    await dec.init();
    const data = new Uint8Array(readFileSync(new URL("./testdata/tone-48k-5.1ch.ac3", import.meta.url)));
    const batches: DecodedAudio[] = [];
    for (let off = 0; off < data.length; off += 768) {
      for (const a of dec.decode(data.subarray(off, Math.min(off + 768, data.length)))) batches.push(a);
    }
    for (const a of dec.flush()) batches.push(a);
    dec.destroy();

    expect(batches.length).toBeGreaterThan(0);
    for (const a of batches) expect(a.channels).toBe(2); // 声明目标 2.0 → 不再出 6 声道
    const total = batches.reduce((s, a) => s + a.samplesPerChannel, 0);
    expect(total / 48000).toBeGreaterThanOrEqual(1.9);
    let sum = 0;
    for (const a of batches) for (let i = 0; i < a.pcm.length; i++) sum += a.pcm[i] * a.pcm[i];
    const overall = Math.sqrt(sum / Math.max(1, batches.reduce((s, a) => s + a.pcm.length, 0)));
    expect(overall).toBeGreaterThan(0.05); // 降混后仍非静音
  });
});
