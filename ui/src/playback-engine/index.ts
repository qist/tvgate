import { isLGWebOS } from "../lib/platform";
import { createMSEPlaybackBackend, isMSEPlaybackSupported } from "./backends/mse-playback-backend";
import { createNativePlaybackBackend } from "./backends/native-playback-backend";
import type { PlayerConfig } from "./config";
import type { PlaybackBackend } from "./types";

export { createMSEPlaybackBackend, isMSEPlaybackSupported } from "./backends/mse-playback-backend";
export { createNativePlaybackBackend } from "./backends/native-playback-backend";
export type { PlayerConfig } from "./config";
export { defaultConfig } from "./config";
export type { PlayerErrorDetail } from "./errors";
export { PlayerErrors } from "./errors";
export type {
  LiveSessionAnchor,
  PlaybackBackend,
  PlaybackBackendKind,
  PlaybackBackendState,
  PlayerDynamicRange,
  PlayerError,
  PlayerEventMap,
  PlayerMediaInfo,
  PlayerRenderState,
  PlayerSegment,
  PlayerVideoScanType,
} from "./types";

export function getPlaybackBackendKind(): "mse" | "native" {
  // 低版本安卓电视/盒子的 WebView 常缺失或只部分支持 MSE：此时回退 native
  // 播放（<video src> 直播 HLS/HTTP 流），避免 MSE 链路静默黑屏。
  return isLGWebOS() || !isMSEPlaybackSupported() ? "native" : "mse";
}

export function createPlaybackBackend(video: HTMLVideoElement, config?: Partial<PlayerConfig>): PlaybackBackend {
  return getPlaybackBackendKind() === "native"
    ? createNativePlaybackBackend(video, config)
    : createMSEPlaybackBackend(video, config);
}
