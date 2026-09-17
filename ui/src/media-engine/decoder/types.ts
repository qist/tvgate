/**
 * 音频软解契约（clean-room 实现）。
 * 依据引擎设计 §5.9 / §6.1：Web Worker 内用 WASM 解码；**WASM URL 由 config 提供**，
 * 库不自带打包。重写方向为统一到 ffmpeg libavcodec-WASI 后端，覆盖 mp2/ac3/eac3/aac。
 *
 * 本文件只定义契约与流式封装所需的最小接口，具体解码能力由 WASM 侧的 bridge 提供。
 */

/** 解码后的 PCM（交错排布）。 */
export interface DecodedAudio {
  sampleRate: number;
  channels: number;
  samplesPerChannel: number;
  pcm: Float32Array;
  /** 本批输出中起始于本次输入之前的样本数/声道（跨输入拼帧；用于把标签退回帧起点）。 */
  samplesBeforeInput?: number;
}

export type DecoderCodec = "mp2" | "mp3" | "ac3" | "eac3" | "aac";

/**
 * WASM 胶水层需实现的最小桥接契约。
 * FFmpeg(libavcodec)-WASI 构建产物应提供符合该形状的工厂。
 */
export interface DecoderBridge {
  /** 初始化解码器（加载 wasm、建 codec context）。 */
  init(): Promise<void>;
  /**
   * 喂入数据并解码。
   * - `consumed` 为已消费字节数；小于 `input.length` 的部分即「不完整帧」，由调用方保留至下次。
   * - `audio` 为本次解码出的 PCM，数据不足时为 null。
   */
  decode(input: Uint8Array): { audio: DecodedAudio | null; consumed: number };
  /** 丢弃内部状态（seek / 切源）。 */
  reset(): void;
  destroy(): void;
}

/** 由 WASM 胶水模块导出的工厂：按 codec 建桥。 */
export interface DecoderBridgeFactory {
  create(codec: DecoderCodec, wasmUrl: string): DecoderBridge;
}

export interface AudioDecoder {
  init(): Promise<void>;
  /** 推入一段码流，返回本次可得到的 PCM 列表（可能为空）。 */
  decode(chunk: Uint8Array): DecodedAudio[];
  /** 冲刷残余（流结束 / seek 前）。 */
  flush(): DecodedAudio[];
  destroy(): void;
}

export interface WasmDecoderConfig {
  /** codec → wasm 二进制 URL。未提供对应 codec 时，该 codec 无法软解。 */
  wasmDecoders: Partial<Record<DecoderCodec, string>>;
}
