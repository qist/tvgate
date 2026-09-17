/**
 * 后端对外契约类型（与 UI 依赖保持一致，非审计对象，见设计文档 §10）。
 * 重写期间保持签名稳定，以便逐模块增量替换、UI 无感。
 */

import type { LiveSessionAnchor } from "../timeline";
import type { PlayerErrorDetail } from "../errors";

export type { LiveSessionAnchor };

export interface PlayerSegment {
  url: string;
  duration?: number;
}

export interface PlayerError {
  category: "io" | "demux" | "media";
  /** 语义化错误码（见 errors.ts 的 PlayerErrors）。 */
  detail?: PlayerErrorDetail;
  /** 原始错误码（IO 为 HTTP 状态码）。 */
  code?: number;
  info?: string;
  url?: string;
  track?: "video" | "audio";
  codec?: string;
}

export type PlayerVideoScanType = "progressive" | "interlaced";
export type PlayerDynamicRange = "sdr" | "hdr10" | "hlg";

/** 媒体信息（对外契约，嵌套形状与 UI 展示消费一致）。 */
export interface PlayerMediaInfo {
  video?: {
    codec?: string;
    width?: number;
    height?: number;
    scanType?: PlayerVideoScanType;
    frameRate?: number;
    dynamicRange?: PlayerDynamicRange;
  };
  audio?: {
    codec?: string;
    channelCount?: number;
    sampleRate?: number;
  };
  bitrate?: {
    bitsPerSecond: number;
    source: "advertised" | "measured";
  };
}

export interface PlayerRenderState {
  /** WebGL 后处理是否启用。 */
  active: boolean;
  /** 去隔行是否实际生效。 */
  deinterlacing: boolean;
}

/** 软解 PCM 全链路丢弃/计数（诊断用，对齐 playback-engine PcmWorkerStats 的可观测字段）。 */
export interface PcmWorkerStats {
  /** worker 软解层：整块落在视频锚点前被丢弃（等价 remuxDrop）。 */
  remuxDropChunks: number;
  /** worker 软解层：跨锚点块裁掉的样本数（等价 trimDrop）。 */
  trimSamples: number;
  /** 解码器初始化失败次数（acquire 返回 null，未配置或初始化失败）。 */
  decodeInitFailed: number;
  /** 单块解码失败累计（达阈值前，触发自愈重建）。 */
  decodeErrors: number;
  /** 主线程：AudioContext 未就绪时 feed 丢弃的块（等价 pendingOverflowDrops）。 */
  pendingOverflowDrops: number;
}

export interface PlayerEventMap {
  error: (error: PlayerError) => void;
  "seek-needed": (seconds: number) => void;
  "live-state-change": (isLive: boolean) => void;
  /** 自动播放策略阻止音频播放、需用户交互时触发。 */
  "audio-suspended": () => void;
  /** 软解 PCM 全链路丢弃计数（诊断用，便于定位静音/丢帧）。 */
  "audio-stats": (stats: PcmWorkerStats) => void;
  /** 解析/测量到的媒体信息变化时触发。 */
  "media-info": (info: PlayerMediaInfo) => void;
  "render-state-change": (state: PlayerRenderState) => void;
  "playback-state-change": (state: "canplay" | "playing" | "paused" | "waiting", eventTimeStamp: number) => void;
  "volume-change": (volume: number, muted: boolean) => void;
  /** 后端归一化后的播放位置（秒）。 */
  "time-update": (seconds: number) => void;
  /** 最后一段结束时触发。 */
  ended: () => void;
}

export type PlaybackBackendKind = "mse" | "native";

export interface PlaybackBackendState {
  currentTime: number;
  duration: number;
  paused: boolean;
  playbackRate: number;
  volume: number;
  muted: boolean;
}

export interface PlaybackBackend {
  readonly kind: PlaybackBackendKind;
  readonly mediaElement: HTMLVideoElement;
  loadSegments(segments: PlayerSegment[]): void;
  play(): Promise<void>;
  pause(): void;
  setVolume(volume: number): void;
  setMuted(muted: boolean): void;
  /** 运行时切换软解音频声道输出模式（mono = 左右声道合成）。 */
  setAudioChannelMode(mode: "stereo" | "mono"): void;
  getState(): PlaybackBackendState;
  seek(seconds: number): void;
  /** 跳到会话直播边减去目标延迟处（MSE 秒）。 */
  goLive(targetMseSeconds: number): void;
  /** 设置会话直播边锚点。 */
  setLiveSessionAnchor(anchor: LiveSessionAnchor): void;
  setLiveSync(enabled: boolean): void;
  /** 对元数据声明为隔行的视频开启自动 bwdif 去隔行；无 renderCanvas 时为空操作。 */
  setAutoDeinterlace(enabled: boolean): void;
  /** 运行时开关 WebGL 画质增强；未配置 renderCanvas 时为空操作。 */
  setPictureEnhancement(enabled: boolean): void;
  /** 停止当前流并重置 video 元素，但保留可复用资源。 */
  stop(): void;
  destroy(): void;
  on<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void;
  off<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void;
}
