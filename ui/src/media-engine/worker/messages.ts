/**
 * Worker 消息协议。
 * 设计 §5.13：backends 经 postMessage 与 worker 通信；消息集保持稳定，便于实现演进。
 * 大数据（init/media segment、PCM）以 transferable 方式传递，避免拷贝阻塞主线程。
 */

import type { SourceMode } from "../types";
import type { PlayerError, PlayerMediaInfo, PcmWorkerStats } from "../backends/types";
import type { WasmDecoderConfig } from "../decoder/types";

export type WorkerCommand =
  | {
      type: "load";
      urls: string[];
      sourceMode?: SourceMode;
      resumeMode?: "range" | "restart";
      targetDuration?: number;
      maxBytes?: number;
      bufferThreshold?: number;
      /** 软解 codec 集（缺省 ac3/eac3/mp2/mp3）。 */
      softDecodeCodecs?: string[];
      /** 软解 WASM 后端（codec → url）；缺省用内置。 */
      wasmDecoders?: WasmDecoderConfig["wasmDecoders"];
      /** 是否启用软解。 */
      softDecodeAudio?: boolean;
      /** 设备可用的 PCM 输出声道数（0/缺省 = 直通源声道；2 = 强制 2.0；用于 5.1 设备兼容）。 */
      pcmOutputChannels?: number;
    }
  | {
      type: "clock";
      /** 播放头当前时间（ms）。 */
      currentTimeMs: number;
      /** MSE 缓冲末端（ms）；-1 = 未知。worker 不得让缓冲领先播放头太多。 */
      bufferedEndMs: number;
      /** 发送时的页面可见性：hidden ⇒ 缓冲领先门必须放行（后台音频 free-run）。 */
      hidden: boolean;
    }
  | { type: "pause" }
  | { type: "resume" }
  | { type: "stop" }
  | { type: "destroy" };

export type WorkerEvent =
  | { type: "ready" }
  | { type: "init-segment"; codec: string; container: string; data: ArrayBuffer; kind: "video" | "audio" }
  | {
      type: "media-segment";
      data: ArrayBuffer;
      timestampOffset: number;
      startDts: number;
      duration: number;
      kind: "video" | "audio";
      trackIds: number[];
    }
  | { type: "media-info"; info: PlayerMediaInfo }
  /** PMT 声明的轨道布局：主线程据此在 append 前建齐 SourceBuffer。 */
  | { type: "stream-layout"; layout: { video: boolean; mseAudio: boolean } }
  /** 软解后的交错 PCM（已在 worker 内归一化到 MSE 时间轴）。 */
  | { type: "pcm"; pcm: Float32Array; channels: number; sampleRate: number; time: number }
  | { type: "loading-complete" }
  | { type: "pcm-audio-stats"; stats: PcmWorkerStats }
  | { type: "error"; error: PlayerError };
