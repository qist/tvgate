/**
 * Native 播放后端。
 * 直接把 URL 交给 <video>，由浏览器/TV 自身硬解播放——低端设备上的轻量路径
 * （对应长期记忆中"TV 要原生支持"的方案①）。不做转封装、无 WebGL 后处理，
 * 因此 setAutoDeinterlace / setPictureEnhancement 为空操作。
 * 对外实现与 MSE 后端相同的 PlaybackBackend 契约。
 */

import type { LiveSessionAnchor } from "../timeline";
import { BackendEventEmitter } from "./event-emitter";
import type {
  PlaybackBackend,
  PlaybackBackendKind,
  PlaybackBackendState,
  PlayerEventMap,
  PlayerRenderState,
  PlayerSegment,
} from "./types";

export interface NativeBackendOptions {
  /** goLive 时的目标延迟（秒）。 */
  targetLatencySec?: number;
  /** 媒体类型，用于 <source> 提示（如 "video/mp4"、"application/x-mpegURL"）。 */
  mimeType?: string;
}

const TARGET_LATENCY = 6;

export class NativeBackend implements PlaybackBackend {
  readonly kind: PlaybackBackendKind = "native";
  readonly mediaElement: HTMLVideoElement;

  private readonly emitter = new BackendEventEmitter();
  private readonly options: NativeBackendOptions;
  private anchor: LiveSessionAnchor | null = null;
  private destroyed = false;
  /** 当前是否挂着一条流（stop() 后为 false）：断流后的实例不得再被 play() 复活。 */
  private streamLoaded = false;

  constructor(video: HTMLVideoElement, options: NativeBackendOptions = {}) {
    this.mediaElement = video;
    this.options = options;
    this.attachMediaEvents();
  }

  private attachMediaEvents(): void {
    const v = this.mediaElement;
    v.addEventListener("timeupdate", () => this.emitter.emit("clock-tick", v.currentTime));
    // 换台接管依赖 playing 事件的 eventTimeStamp 时序守卫，必须传真实值（同 mse-backend）
    v.addEventListener("canplay", (e) => this.emitter.emit("transport-state", "canplay", e.timeStamp));
    v.addEventListener("playing", (e) => this.emitter.emit("transport-state", "playing", e.timeStamp));
    v.addEventListener("waiting", (e) => this.emitter.emit("transport-state", "waiting", e.timeStamp));
    v.addEventListener("pause", (e) => this.emitter.emit("transport-state", "paused", e.timeStamp));
    v.addEventListener("ended", () => this.emitter.emit("ended"));
    v.addEventListener("error", () =>
      this.emitter.emit("error", { category: "media", info: "原生播放失败" }),
    );
  }

  on<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void {
    this.emitter.on(event, handler);
  }

  off<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void {
    this.emitter.off(event, handler);
  }

  loadSegments(segments: PlayerSegment[]): void {
    if (this.destroyed || segments.length === 0) return;
    this.streamLoaded = true;
    const v = this.mediaElement;
    // 原生后端只支持单 URL 直播；多分段时取首段
    const url = segments[0].url;
    if (this.options.mimeType) {
      v.innerHTML = "";
      const source = document.createElement("source");
      source.src = url;
      source.type = this.options.mimeType;
      v.appendChild(source);
    } else {
      v.src = url;
    }
    v.load();
  }

  async play(): Promise<void> {
    // 已断流（换台过渡期的旧实例）：什么都不做，绝不让旧台被 play() 复活，
    // 也避免"无源 play()"抛 NotSupportedError 触发无谓的错误恢复。
    if (!this.streamLoaded) return;
    // rejection（NotAllowedError/中断）冒泡给 UI，由 UI 决定硬切换兜底或提示用户交互
    await this.mediaElement.play();
  }

  pause(): void {
    this.mediaElement.pause();
  }

  setVolume(volume: number): void {
    this.mediaElement.volume = Math.max(0, Math.min(1, volume));
    this.emitter.emit("gain-change", this.mediaElement.volume, this.mediaElement.muted);
  }

  setMuted(muted: boolean): void {
    this.mediaElement.muted = muted;
    this.emitter.emit("gain-change", this.mediaElement.volume, muted);
  }

  getState(): PlaybackBackendState {
    const v = this.mediaElement;
    return {
      currentTime: v.currentTime,
      duration: Number.isFinite(v.duration) ? v.duration : 0,
      paused: v.paused,
      playbackRate: v.playbackRate,
      volume: v.volume,
      muted: v.muted,
    };
  }

  seek(seconds: number): void {
    const v = this.mediaElement;
    const max = Number.isFinite(v.duration) ? v.duration : seconds;
    v.currentTime = Math.max(0, Math.min(seconds, max));
  }

  goLive(targetMseSeconds?: number): void {
    if (targetMseSeconds !== undefined) {
      this.seek(targetMseSeconds);
      return;
    }
    // 无显式目标时，跳到可寻址范围末端减去目标延迟
    const v = this.mediaElement;
    if (!Number.isFinite(v.duration)) return;
    this.seek(v.duration - (this.options.targetLatencySec ?? TARGET_LATENCY));
  }

  setLiveSessionAnchor(anchor: LiveSessionAnchor): void {
    this.anchor = anchor;
  }

  get sessionAnchor(): LiveSessionAnchor | null {
    return this.anchor;
  }

  setLiveSync(_enabled: boolean): void {
    // 原生播放由浏览器自行管理缓冲与直播边，不做速率干预
  }

  setAudioChannelMode(_mode: "stereo" | "mono"): void {
    // 原生后端无软解 PCM 链路（浏览器直接解码），单声道合成不适用。
  }

  setAutoDeinterlace(_enabled: boolean): void {
    this.emitter.emit("painter-state", { active: false, deinterlacing: false } as PlayerRenderState);
  }

  setPictureEnhancement(_enabled: boolean): void {
    this.emitter.emit("painter-state", { active: false, deinterlacing: false } as PlayerRenderState);
  }

  stop(): void {
    this.streamLoaded = false;
    this.mediaElement.pause();
    this.mediaElement.removeAttribute("src");
    this.mediaElement.innerHTML = "";
    this.mediaElement.load();
  }

  destroy(): void {
    this.destroyed = true;
    this.stop();
    this.emitter.clear();
  }
}
