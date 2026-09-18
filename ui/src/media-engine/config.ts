/**
 * 播放引擎配置契约。
 * 设计 §5.16：config 为契约，UI 侧依赖不得破坏，故字段与取值保持兼容。
 */

import type { WasmDecoderConfig } from "./decoder/types";

export interface PlayerConfig {
  // ---- 直播追边 ----
  /** 通过微调播放速率追赶直播延迟。 @default true */
  chaseLiveEdge: boolean;
  /** 可接受的最大缓冲滞后（秒）。需 chaseLiveEdge 为 true。 @default 3 */
  chaseMaxLagSeconds: number;
  /** 超过最大滞后时追赶到的目标滞后（秒）。 @default 1.5 */
  chaseTargetLagSeconds: number;
  /** 追赶滞后时使用的播放速率（钳位 [1,2]）。 @default 1.2 */
  chaseRateCap: number;

  /** 各 codec 的 WASM 解码器 URL；省略某个 codec 即禁用该 codec 的软解。 */
  softDecoderUrls: WasmDecoderConfig["wasmDecoders"];

  // ---- 向后缓冲修剪 ----
  /** 向后缓冲上限（秒），超过触发清理。 @default 180 */
  pruneBackwardCeilingSeconds: number;
  /** 清理后保留的最小向后缓冲（秒）。 @default 120 */
  pruneBackwardFloorSeconds: number;

  /**
   * 软解音频（MP2 / AC-3 / E-AC-3）声道输出模式：
   *   - "stereo"（默认）：解码结果原样透传（左右声道各自独立）。
   *   - "mono"：左右声道合成单声道 (L+R)/2。部分源左右声道内容分离
   *     （如 L=对白 R=音乐），手机端只能听到单边 → 合成后两边内容都能听到。
   * @default "stereo"
   */
  pcmChannelMix: "stereo" | "mono";

  // ---- 拉流请求与日志 ----
  /** 分段请求的 referrerPolicy。 */
  fetchReferrerPolicy: string | undefined;
  /** 附加请求头。 */
  fetchHeaders: Record<string, string> | undefined;
  /** 日志级别：0=FATAL, 1=ERROR, 2=WARN, 3=INFO, 4=DEBUG。 */
  logVerbosity: number | undefined;

  // ---- 画面后处理 ----
  /** WebGL 渲染画布；省略即禁用 WebGL 视频渲染。
   *  引擎不会改动画布的 style/visibility——由 `painter-state` 事件驱动。 */
  paintCanvas: HTMLCanvasElement | undefined;
  /** 视频元数据声明为隔行时自动去隔行。 @default true */
  deinterlaceAuto: boolean;
  /** 在渲染门限内启用轻量画质增强。 @default true */
  enhancePicture: boolean;
}

/**
 * 出厂默认值收口在一个工厂里内联给出，按"直播追边 → 缓冲修剪 → 拉流 → 画面"的
 * 主题分组排列，便于对照评审；defaultConfig 即模块加载时产出的那份缺省实例。
 */
export function createDefaultConfig(): PlayerConfig {
  return {
    chaseLiveEdge: true,
    chaseMaxLagSeconds: 3,
    chaseTargetLagSeconds: 1.5,
    chaseRateCap: 1.2,

    softDecoderUrls: {},

    pruneBackwardCeilingSeconds: 180,
    pruneBackwardFloorSeconds: 120,

    pcmChannelMix: "stereo",

    fetchReferrerPolicy: undefined,
    fetchHeaders: undefined,
    logVerbosity: undefined,

    paintCanvas: undefined,
    deinterlaceAuto: true,
    enhancePicture: true,
  };
}

export const defaultConfig: PlayerConfig = createDefaultConfig();
