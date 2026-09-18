/**
 * 后端工厂。
 * 设计 §5.12 / §5.15：按设备能力在 MSE 与 native（<video src> 硬解）之间选择。
 * 低版本安卓电视/盒子的 WebView 常缺失或只部分支持 MSE → 回退 native，避免 MSE 链路静默黑屏。
 */

import { createDefaultConfig, type PlayerConfig } from "../config";
import { isLGWebOS, isMSEPlaybackSupported } from "../platform";
import { builtinWasmDecoders } from "../decoder/builtin-wasm";
import { MseBackend } from "./mse-backend";
import { NativeBackend } from "./native-backend";
import type { PlaybackBackend, PlaybackBackendKind } from "./types";

/** 依据能力检测决定用哪套后端。 */
export function getPlaybackBackendKind(): PlaybackBackendKind {
  return isLGWebOS() || !isMSEPlaybackSupported() ? "native" : "mse";
}

export function createMSEPlaybackBackend(
  video: HTMLVideoElement,
  config: Partial<PlayerConfig> = {},
): PlaybackBackend {
  const c = { ...createDefaultConfig(), ...config };
  // 未显式配置软解 URL 时，缺省用内置统一模块（自持，不依赖旧目录资产）
  const wasmDecoders = Object.keys(c.softDecoderUrls).length > 0 ? c.softDecoderUrls : builtinWasmDecoders;
  return new MseBackend(video, {
    liveSync: c.chaseLiveEdge,
    targetLatencySec: c.chaseTargetLagSeconds,
    softDecodeAudio: true,
    audioChannelMode: c.pcmChannelMix,
    wasmDecoders,
    renderCanvas: c.paintCanvas,
    autoDeinterlace: c.deinterlaceAuto,
    pictureEnhancement: c.enhancePicture,
  });
}

export function createNativePlaybackBackend(
  video: HTMLVideoElement,
  config: Partial<PlayerConfig> = {},
): PlaybackBackend {
  const c = { ...createDefaultConfig(), ...config };
  return new NativeBackend(video, { targetLatencySec: c.chaseTargetLagSeconds });
}

/** 按能力自动选择后端（UI 唯一入口）。 */
export function createPlaybackBackend(
  video: HTMLVideoElement,
  config: Partial<PlayerConfig> = {},
): PlaybackBackend {
  return getPlaybackBackendKind() === "native"
    ? createNativePlaybackBackend(video, config)
    : createMSEPlaybackBackend(video, config);
}
