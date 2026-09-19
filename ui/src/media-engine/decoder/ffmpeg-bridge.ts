/**
 * FFmpeg(libavcodec)-WASI 解码桥。
 *
 * 驱动「独立 WASM（无 Emscripten JS 胶水）」：
 * - import 契约：env.emscripten_notify_memory_growth 空函数；wasi_snapshot_preview1 打桩
 *   （解码路径不触发，返回 ENOSYS 即可）；实例化后调用一次导出 `_initialize`。
 * - 导出按 ABI 风格自动识别：
 *   • "unified"（media-engine/wasm/avcodec_audio.c，me_decoder_*）：mp2/ac3/eac3/aac 一套。
 *   • "ac3"（既有 ac3_decoder.wasm，ac3_decoder_*）：ac3/eac3。
 *   decode() 语义与 wasm 内部 carry 一致：全部输入收下（consumed=len），残余帧由 wasm 端
 *   自带 carry 保留，出帧时返回 {audio}。对齐 DecoderBridge 契约，供 WasmAudioDecoder 封装。
 */

import type { DecodedAudio, DecoderBridge, DecoderBridgeFactory, DecoderCodec } from "./types";

/** WASM 解码端 info 数组布局（8×i32）。 */
const INFO_SAMPLES = 0;
const INFO_SAMPLE_RATE = 1;
const INFO_CHANNELS = 2;
const INFO_FRAMES = 3;
const INFO_ERRORS = 7;
/** 本批输出中起始于本次输入之前的样本数/声道（跨输入拼帧，供上层把标签退回帧起点）。 */
const INFO_SAMPLES_BEFORE_INPUT = 6;
const INFO_I32_COUNT = 8;

/** 每帧最大交错浮点数上限 = 每声道样本上限 × 最大输出声道数
 * （AAC-HE 2048 / AC-3·E-AC-3 1536 每声道样本；声道上限 6 = 5.1 直通，见 WASM 封装）。 */
const MAX_INTERLEAVED_PER_FRAME = 2048 * 6;
/** 每个解码批次按最小帧长粗估可容纳的帧数上限，用于输出缓冲扩容。 */
const EST_MIN_FRAME_BYTES = 128;
/** wasm 端 carry 上限（对齐 C 封装）。 */
const CARRY_MAX = 8192;

/** 每种 ABI 风格的入口名映射。 */
interface AbiNames {
  create: string;
  destroy: string;
  reset: string;
  decode: string;
}

const ABI_UNIFIED: AbiNames = { create: "me_decoder_create", destroy: "me_decoder_destroy", reset: "me_decoder_reset", decode: "me_decode_payload" };
const ABI_AC3: AbiNames = { create: "ac3_decoder_create", destroy: "ac3_decoder_destroy", reset: "ac3_decoder_reset", decode: "ac3_decode_payload" };

/** 从实例导出里探测 ABI 风格（供同一份 wasm 灵活驱动新旧封装）。 */
export function detectAbiStyle(exports: Record<string, unknown>): "unified" | "ac3" {
  if (typeof exports.me_decoder_create === "function") return "unified";
  return "ac3";
}

/** 构造 wasm 实例化所需 import 对象（与 wasm 编译端约定一致）。 */
export function createWasmImports(): WebAssembly.Imports {
  return {
    env: {
      emscripten_notify_memory_growth: () => {},
    },
    wasi_snapshot_preview1: new Proxy(
      {},
      {
        get: (_t, prop) => (prop === "clock_time_get" ? () => 0 : () => 52 /* ENOSYS */),
      },
    ),
  };
}

class FfmpegWasmBridge implements DecoderBridge {
  private exports: Record<string, CallableFunction> | null = null;
  private memory: WebAssembly.Memory | null = null;
  private decoderPtr = 0;
  private inputPtr = 0;
  private outputPtr = 0;
  private infoPtr = 0;
  private inputCap = 0;
  private outputFloats = 0;
  private abi: AbiNames = ABI_UNIFIED;

  constructor(
    private readonly codec: DecoderCodec,
    private readonly wasmUrl: string,
    private readonly codecId: number,
    /** 目标输出声道数（0 = 直通源声道；2 = 强制 2.0，供设备不支持 5.1 时降混）。 */
    private readonly outChannels = 0,
  ) {}

  async init(): Promise<void> {
    if (this.exports) return;
    const ex = await loadStandaloneModule(this.wasmUrl);
    this.exports = ex as unknown as Record<string, CallableFunction>;
    this.memory = ex.memory as WebAssembly.Memory;
    this.abi = detectAbiStyle(ex) === "unified" ? ABI_UNIFIED : ABI_AC3;

    const create = this.exports[this.abi.create] as (codecOrEac3: number, outChannels: number) => number;
    const isLegacyAc3 = this.abi === ABI_AC3;
    // 统一 ABI：第二参数为目标声道数（0 = 直通）；legacy ac3 ABI 无此参数（多传被 wasm 忽略）
    this.decoderPtr = create(isLegacyAc3 ? (this.codec === "eac3" ? 1 : 0) : this.codecId, this.outChannels);
    if (!this.decoderPtr) throw new Error(`创建 ${this.codec} 解码器失败`);

    const malloc = this.exports.malloc as (n: number) => number;
    this.infoPtr = malloc(INFO_I32_COUNT * 4);
  }

  decode(input: Uint8Array): { audio: DecodedAudio | null; consumed: number } {
    if (!this.exports || !this.memory) return { audio: null, consumed: 0 };
    const ex = this.exports;
    const malloc = ex.malloc as (n: number) => number;
    const free = ex.free as (p: number) => void;

    if (input.length > this.inputCap) {
      if (this.inputPtr) free(this.inputPtr);
      this.inputCap = Math.max(input.length, 4096);
      this.inputPtr = malloc(this.inputCap);
    }

    // 按载荷长度粗估输出帧数上限，扩到够装
    const maxFrames = Math.floor((CARRY_MAX + input.length) / EST_MIN_FRAME_BYTES) + 2;
    const needFloats = maxFrames * MAX_INTERLEAVED_PER_FRAME;
    if (needFloats > this.outputFloats) {
      if (this.outputPtr) free(this.outputPtr);
      this.outputFloats = needFloats;
      this.outputPtr = malloc(needFloats * 4);
    }

    const heap = new Uint8Array(this.memory.buffer);
    heap.set(input, this.inputPtr);

    const decodeFn = ex[this.abi.decode] as (
      dec: number,
      inp: number,
      inSz: number,
      out: number,
      outCap: number,
      info: number,
    ) => number;
    const samples = decodeFn(this.decoderPtr, this.inputPtr, input.length, this.outputPtr, this.outputFloats, this.infoPtr);
    if (samples <= 0) return { audio: null, consumed: input.length };

    const i32 = new Int32Array(this.memory.buffer);
    const base = this.infoPtr >> 2;
    const samplesPerChannel = i32[base + INFO_SAMPLES];
    const sampleRate = i32[base + INFO_SAMPLE_RATE];
    const channels = i32[base + INFO_CHANNELS];
    const frames = i32[base + INFO_FRAMES];
    const errors = i32[base + INFO_ERRORS];
    const samplesBeforeInput = i32[base + INFO_SAMPLES_BEFORE_INPUT];
    void frames;
    void errors;

    const total = samplesPerChannel * channels;
    const view = new Float32Array(this.memory.buffer, this.outputPtr, total);
    const pcm = new Float32Array(total);
    pcm.set(view);

    return {
      audio: { sampleRate, channels, samplesPerChannel, pcm, samplesBeforeInput },
      // wasm 端自带 carry：全部输入已收纳（含残余帧），残余由 C 侧保留
      consumed: input.length,
    };
  }

  reset(): void {
    if (!this.exports || !this.decoderPtr) return;
    (this.exports[this.abi.reset] as (d: number) => void)(this.decoderPtr);
  }

  destroy(): void {
    const ex = this.exports;
    if (!ex) return;
    if (this.decoderPtr) {
      (ex[this.abi.destroy] as (d: number) => void)(this.decoderPtr);
      this.decoderPtr = 0;
    }
    // 三块宿主缓冲一并归还：任何一块为空都跳过，退出前统一置零防重复释放
    const free = ex.free as (p: number) => void;
    for (const field of ["inputPtr", "outputPtr", "infoPtr"] as const) {
      const ptr = this[field];
      if (!ptr) continue;
      free(ptr);
      this[field] = 0;
    }
    this.exports = null;
    this.memory = null;
  }
}

/** 加载独立 WASM：instantiateStreaming 优先，失败退回 arrayBuffer 路径。 */
export async function loadStandaloneModule(url: string): Promise<Record<string, CallableFunction> & { memory: WebAssembly.Memory }> {
  const imports = createWasmImports();
  let instance: WebAssembly.Instance;
  if (typeof WebAssembly.instantiateStreaming === "function") {
    try {
      const result = await WebAssembly.instantiateStreaming(fetch(url), imports);
      instance = result.instance;
    } catch {
      const bytes = await (await fetch(url)).arrayBuffer();
      const result = await WebAssembly.instantiate(bytes, imports);
      instance = result.instance;
    }
  } else {
    const bytes = await (await fetch(url)).arrayBuffer();
    const result = await WebAssembly.instantiate(bytes, imports);
    instance = result.instance;
  }
  const ex = instance.exports as unknown as Record<string, CallableFunction> & { memory: WebAssembly.Memory };
  const initialize = ex._initialize as CallableFunction | undefined;
  if (typeof initialize === "function") initialize();
  return ex;
}

/** 每 codec 在统一封装里的参数（与 media-engine/wasm/avcodec_audio.c 对齐）。 */
const CODEC_IDS: Record<DecoderCodec, number> = { mp2: 2, mp3: 3, ac3: 0, eac3: 1, aac: 4 };

/** 构建桥工厂（wasm URL 由 config 提供，库不自带打包）。 */
export function createFfmpegBridgeFactory(outChannels = 0): DecoderBridgeFactory {
  return {
    create(codec: DecoderCodec, wasmUrl: string): DecoderBridge {
      return new FfmpegWasmBridge(codec, wasmUrl, CODEC_IDS[codec] ?? 2, outChannels);
    },
  };
}
