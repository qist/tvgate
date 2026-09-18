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
    width?: number;
    height?: number;
    scanType?: PlayerVideoScanType;
    frameRate?: number;
    dynamicRange?: PlayerDynamicRange;
    codec?: string;
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

/** 软解 PCM 全链路的丢弃/失败计数（诊断用；worker 与主线程各自累计后合并上报）。 */
export interface PcmWorkerStats {
  /** worker 软解层：整块早于视频锚点而被整块丢弃的块数。 */
  behindAnchorDrops: number;
  /** worker 软解层：块跨越锚点时被裁掉的头部样本数。 */
  trimmedAtAnchor: number;
  /** 解码器实例创建失败的次数（未配置 URL 或初始化失败）。 */
  decoderCreateFailures: number;
  /** 单块解码失败的累计数（达阈值会触发解码器自愈重建）。 */
  decoderWorkFailures: number;
  /** 主线程侧：AudioContext 尚未就绪时 feed 直接丢弃的块数。 */
  audioGateDrops: number;
}

export interface PlayerEventMap {
  error: (error: PlayerError) => void;
  /** 引擎自己完成不了的 seek（如回看目标需要上层换流）时转交上层。 */
  "seek-request": (seconds: number) => void;
  /** 是否正贴着直播边（进出直播态都会上报）。 */
  "live-edge-state": (isLive: boolean) => void;
  /** 自动播放策略拦下了音频输出，需要一次用户交互解锁。 */
  "audio-gate-blocked": () => void;
  /** 软解 PCM 全链路丢弃计数（诊断用，便于定位静音/丢帧）。 */
  "pcm-pipeline-stats": (stats: PcmWorkerStats) => void;
  /** 解析/测量到的媒体信息变化时触发。 */
  "track-metadata": (info: PlayerMediaInfo) => void;
  /** WebGL 后处理状态变化。 */
  "painter-state": (state: PlayerRenderState) => void;
  /** 传输层状态机（canplay/playing/paused/waiting）；时间戳供换台时序守卫使用。 */
  "transport-state": (state: "canplay" | "playing" | "paused" | "waiting", eventTimeStamp: number) => void;
  /** 音量/静音变化（实例内部改动，如换台接管时新实例恢复用户音量）。 */
  "gain-change": (volume: number, muted: boolean) => void;
  /** 后端归一化后的播放位置（秒）。 */
  "clock-tick": (seconds: number) => void;
  /** 最后一段结束时触发。 */
  ended: () => void;
}

export type PlaybackBackendKind = "mse" | "native";

/** 后端瞬时状态快照（UI 读它做编排决策；不订阅事件时的同步通道）。 */
export interface PlaybackBackendState {
  paused: boolean;
  playbackRate: number;
  volume: number;
  muted: boolean;
  currentTime: number;
  duration: number;
}

/**
 * 双后端（MSE / native <video>）的统一门面。
 * 方法按"状态读取 → 装载 → 传输控制 → 音量 → 直播/画面 → 生命周期"分组。
 */
export interface PlaybackBackend {
  readonly kind: PlaybackBackendKind;
  readonly mediaElement: HTMLVideoElement;
  getState(): PlaybackBackendState;
  loadSegments(segments: PlayerSegment[]): void;
  play(): Promise<void>;
  pause(): void;
  seek(seconds: number): void;
  setVolume(volume: number): void;
  setMuted(muted: boolean): void;
  /** 运行时切换软解音频声道输出模式（mono = 左右声道合成）。 */
  setAudioChannelMode(mode: "stereo" | "mono"): void;
  /** 跳到会话直播边减去目标延迟处（MSE 秒）。 */
  goLive(targetMseSeconds: number): void;
  /** 设置会话直播边锚点。 */
  setLiveSessionAnchor(anchor: LiveSessionAnchor): void;
  setLiveSync(enabled: boolean): void;
  /** 对元数据声明为隔行的视频开启自动 bwdif 去隔行；无 renderCanvas 时为空操作。 */
  setAutoDeinterlace(enabled: boolean): void;
  /** 运行时开关 WebGL 画质增强；未配置 renderCanvas 时为空操作。 */
  setPictureEnhancement(enabled: boolean): void;
  /** 停止当前流：作废拉流/解码管线、拆掉音频链并暂停画面（保留元素与 MSE 供下次重灌）。 */
  stop(): void;
  destroy(): void;
  on<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void;
  off<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void;
}
