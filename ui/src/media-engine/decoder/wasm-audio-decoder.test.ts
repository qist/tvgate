import { describe, it, expect } from "vitest";
import { WasmAudioDecoder, DecoderRegistry } from "./wasm-audio-decoder";
import type { DecoderBridge, DecoderBridgeFactory, DecodedAudio } from "./types";

/** 每 6 字节解一帧的假桥：用于验证「不完整帧跨调用保留」。 */
class FakeBridge implements DecoderBridge {
  seen: Uint8Array[] = [];
  inited = 0;
  resets = 0;
  async init(): Promise<void> {
    this.inited++;
  }
  decode(input: Uint8Array): { audio: DecodedAudio | null; consumed: number } {
    this.seen.push(new Uint8Array(input));
    const take = Math.min(6, input.length);
    if (take < 6) return { audio: null, consumed: 0 };
    return {
      audio: {
        sampleRate: 48000,
        channels: 2,
        samplesPerChannel: take,
        pcm: new Float32Array(take * 2),
      },
      consumed: take,
    };
  }
  reset(): void {
    this.resets++;
  }
  destroy(): void {}
}

describe("WasmAudioDecoder（不完整帧跨调用保留）", () => {
  it("残余保留到下次、再喂入时拼接", async () => {
    const bridge = new FakeBridge();
    const dec = new WasmAudioDecoder("ac3", bridge);
    await dec.init();

    // 4 字节 < 6 → 无输出，残余 4
    expect(dec.decode(new Uint8Array(4))).toEqual([]);
    // 再喂 4 字节 → 拼接为 8 → 消费 6 出 1 帧，残余 2
    const out = dec.decode(new Uint8Array(4));
    expect(out).toHaveLength(1);
    expect(bridge.seen[0]).toHaveLength(4);
    expect(bridge.seen[1]).toHaveLength(8);
    // 残余 2 先被单独喂给 bridge（seen[2]），随后 2+6=8 为下一块（seen[3]）
    expect(dec.decode(new Uint8Array(6))).toHaveLength(1);
    expect(bridge.seen[2]).toHaveLength(2);
    expect(bridge.seen[3]).toHaveLength(8);

    dec.destroy();
    expect(bridge.resets).toBe(0);
  });

  it("flush 冲刷并 reset", async () => {
    const bridge = new FakeBridge();
    const dec = new WasmAudioDecoder("mp2", bridge);
    await dec.init();
    dec.decode(new Uint8Array(4)); // 残余 4
    const out = dec.flush();
    expect(out).toEqual([]);
    expect(bridge.resets).toBe(1);
    dec.destroy();
  });
});

describe("DecoderRegistry", () => {
  it("未配置的 codec 不可用；初始化失败返回 null（降级）", async () => {
    const factory: DecoderBridgeFactory = {
      create: () => {
        throw new Error("no wasm");
      },
    };
    const registry = new DecoderRegistry(factory, { wasmDecoders: { ac3: "/ac3.wasm", eac3: undefined } });
    expect(registry.supports("ac3")).toBe(true);
    expect(registry.supports("mp2")).toBe(false);
    const ac3 = await registry.acquire("ac3");
    expect(ac3).toBeNull(); // init 失败 → null，而非抛出
    const eac3 = await registry.acquire("eac3");
    expect(eac3).toBeNull(); // 未配置 URL
    registry.destroy();
  });
});
