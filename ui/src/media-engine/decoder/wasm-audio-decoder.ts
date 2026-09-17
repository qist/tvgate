/**
 * WASM 音频解码器（clean-room 实现）。
 * 在 DecoderBridge 之上提供流式封装，核心行为：**不完整帧跨调用保留**——
 * 每次把「上次残余 + 本次输入」交给 bridge，把未消费的尾部留到下次（设计 §5.9）。
 */

import type { AudioDecoder, DecodedAudio, DecoderBridge, DecoderCodec, WasmDecoderConfig } from "./types";

/** 单次最大喂入字节，避免把超大缓冲整块交给 WASM。 */
const MAX_FEED_BYTES = 64 * 1024;
/** 残余缓冲上限，超过则丢弃（防止异常码流导致内存无界增长）。 */
const MAX_CARRY_BYTES = 512 * 1024;

export class WasmAudioDecoder implements AudioDecoder {
  private bridge: DecoderBridge;
  private carry: Uint8Array = new Uint8Array(0);
  private ready = false;
  private destroyed = false;

  constructor(
    readonly codec: DecoderCodec,
    bridge: DecoderBridge,
  ) {
    this.bridge = bridge;
  }

  async init(): Promise<void> {
    if (this.destroyed) return;
    await this.bridge.init();
    this.ready = true;
  }

  get isReady(): boolean {
    return this.ready;
  }

  decode(chunk: Uint8Array): DecodedAudio[] {
    if (!this.ready || this.destroyed || chunk.length === 0) return [];

    // 拼接残余 + 新数据
    const input = this.carry.length > 0 ? concatBytes(this.carry, chunk) : chunk;
    const out: DecodedAudio[] = [];

    let offset = 0;
    while (offset < input.length) {
      const slice = input.subarray(offset, Math.min(offset + MAX_FEED_BYTES, input.length));
      let result: { audio: DecodedAudio | null; consumed: number };
      try {
        result = this.bridge.decode(slice);
      } catch {
        // 解码异常：丢弃本段，避免死循环
        offset = input.length;
        break;
      }
      if (result.audio && result.audio.samplesPerChannel > 0) out.push(result.audio);
      if (result.consumed <= 0) break; // 数据不足以解出一帧，等待更多数据
      offset += result.consumed;
    }

    // 未消费的尾部作为不完整帧保留
    const remain = input.length - offset;
    this.carry = remain > 0 && remain <= MAX_CARRY_BYTES ? new Uint8Array(input.subarray(offset)) : new Uint8Array(0);

    return out;
  }

  flush(): DecodedAudio[] {
    if (!this.ready || this.destroyed) return [];
    const out: DecodedAudio[] = [];
    try {
      const r = this.bridge.decode(new Uint8Array(0));
      if (r.audio && r.audio.samplesPerChannel > 0) out.push(r.audio);
    } catch {
      /* 忽略冲刷异常 */
    }
    this.carry = new Uint8Array(0);
    this.bridge.reset();
    return out;
  }

  destroy(): void {
    this.destroyed = true;
    this.carry = new Uint8Array(0);
    this.bridge.destroy();
    this.ready = false;
  }
}

/**
 * 解码器注册表：按 codec 建解码器。
 * 行为（设计 §5.15）：**解码器初始化失败 → 禁用音频、视频继续**，不阻断播放。
 */
export class DecoderRegistry {
  private readonly wasmUrls: Partial<Record<DecoderCodec, string>>;
  private readonly instances = new Map<DecoderCodec, WasmAudioDecoder>();

  constructor(
    private readonly factory: { create(codec: DecoderCodec, wasmUrl: string): DecoderBridge },
    config: WasmDecoderConfig,
  ) {
    this.wasmUrls = config.wasmDecoders;
  }

  /** 该 codec 是否配置了 WASM 后端。 */
  supports(codec: DecoderCodec): boolean {
    return !!this.wasmUrls[codec];
  }

  /**
   * 取得（或创建）解码器。
   * 返回 null 表示不可用（未配置或初始化失败）——上层应据此禁用该音轨但继续播放视频。
   */
  async acquire(codec: DecoderCodec): Promise<WasmAudioDecoder | null> {
    const existing = this.instances.get(codec);
    if (existing) return existing.isReady ? existing : null;

    const url = this.wasmUrls[codec];
    if (!url) return null;

    let decoder: WasmAudioDecoder;
    try {
      decoder = new WasmAudioDecoder(codec, this.factory.create(codec, url));
      await decoder.init();
    } catch {
      // 初始化/建桥失败：不缓存失败实例，交由上层降级（禁用该音轨，视频继续）
      return null;
    }
    this.instances.set(codec, decoder);
    return decoder;
  }

  release(codec: DecoderCodec): void {
    const d = this.instances.get(codec);
    if (!d) return;
    d.destroy();
    this.instances.delete(codec);
  }

  destroy(): void {
    for (const d of this.instances.values()) d.destroy();
    this.instances.clear();
  }
}

function concatBytes(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}
