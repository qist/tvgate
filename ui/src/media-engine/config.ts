/**
 * 播放引擎配置契约（clean-room 实现）。
 * 设计 §5.16：config 为契约，UI 侧依赖不得破坏，故字段与取值保持兼容。
 */

import type { WasmDecoderConfig } from "./decoder/types";

export interface PlayerConfig {
  /** 通过微调播放速率追赶直播延迟。 @default true */
  liveSync: boolean;
  /** 可接受的最大缓冲延迟（秒）。需 liveSync 为 true。 @default 3 */
  liveSyncMaxLatency: number;
  /** 超过最大延迟时追赶到的目标延迟（秒）。 @default 1.5 */
  liveSyncTargetLatency: number;
  /** 追赶延迟时使用的播放速率（钳位 [1,2]）。 @default 1.2 */
  liveSyncPlaybackRate: number;

  /** 各 codec 的 WASM 解码器 URL；省略即禁用该 codec 的软解。 */
  wasmDecoders: WasmDecoderConfig["wasmDecoders"];

  /** 向后缓冲上限（秒），超过触发清理。 @default 180 */
  bufferCleanupMaxBackward: number;
  /** 清理后保留的最小向后缓冲（秒）。 @default 120 */
  bufferCleanupMinBackward: number;

  /**
   * 软解音频（MP2 / AC-3 / E-AC-3）声道输出模式：
   *   - "stereo"（默认）：解码结果原样透传（左右声道各自独立）。
   *   - "mono"：左右声道合成单声道 (L+R)/2。部分源左右声道内容分离
   *     （如 L=对白 R=音乐），手机端只能听到单边 → 合成后两边内容都能听到。
   * @default "stereo"
   */
  audioChannelMode: "stereo" | "mono";

  /** 分段请求的 referrerPolicy。 */
  referrerPolicy: string | undefined;
  /** 附加请求头。 */
  headers: Record<string, string> | undefined;
  /** 日志级别：0=FATAL, 1=ERROR, 2=WARN, 3=INFO, 4=DEBUG。 */
  logLevel: number | undefined;

  /** WebGL 渲染画布；省略即禁用 WebGL 视频渲染。
   *  引擎不会改动画布的 style/visibility——由 `render-state-change` 事件驱动。 */
  renderCanvas: HTMLCanvasElement | undefined;
  /** 视频元数据声明为隔行时自动去隔行。 @default true */
  autoDeinterlace: boolean;
  /** 在渲染门限内启用轻量画质增强。 @default true */
  pictureEnhancement: boolean;
}

export const defaultConfig: PlayerConfig = {
  liveSync: true,
  liveSyncMaxLatency: 3,
  liveSyncTargetLatency: 1.5,
  liveSyncPlaybackRate: 1.2,

  wasmDecoders: {},

  bufferCleanupMaxBackward: 180,
  bufferCleanupMinBackward: 120,

  audioChannelMode: "stereo",

  referrerPolicy: undefined,
  headers: undefined,
  logLevel: undefined,

  renderCanvas: undefined,
  autoDeinterlace: true,
  pictureEnhancement: true,
};

export function createDefaultConfig(): PlayerConfig {
  return { ...defaultConfig };
}
