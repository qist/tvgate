/**
 * 播放控制与直播同步（MSE 直播播放控制）。
 *
 * 关键（曾致「起播数秒抖动」的根因）：
 * 1. 直播同步**只调整 playbackRate，绝不 seek**。lag 过大时若 seek 到缓冲末端，
 *    会让播放头贴着缓冲边、反复跳转 → 起播几秒内持续抖动。
 * 2. 直播边取「最后一个缓冲区间的末端」，**不可**用只增不减的高水位：HLS 起播会
 *    快速拉取多个分片使缓冲末端猛涨，用高水位会误判为「严重落后」而反复追速/跳转。
 * 3. 直播边下溢（waiting 且已追到缓冲末端）时**退避**：抬高延迟下限，避免持续贴着
 *    直播边反复欠载。
 */

import type { BufferedRange } from "./media-source-controller";
import type { SourceMode } from "../types";

export interface PlaybackControllerCallbacks {
  onRateChange?(rate: number): void;
  onLiveStateChange?(isLive: boolean, lagSeconds: number): void;
  /** 已跳过一段缓冲缺口（上游缺失内容导致的空洞），gapSeconds 为缺口时长。 */
  onGapSkip?(gapSeconds: number): void;
}

export interface PlaybackControllerOptions {
  sourceMode?: SourceMode;
  /** 是否启用直播同步（跟随直播边）。 */
  liveSync?: boolean;
  /** 触发追速的最大可接受延迟（秒）。 */
  maxLatency?: number;
  /** 追速目标延迟（秒）。 */
  targetLatency?: number;
  /** 追速用的 playbackRate（夹在 [1,2]）。 */
  chaseRate?: number;
  /** 判定「在直播边」的容差（秒）。 */
  tolerance?: number;
  /**
   * 直播边延迟提供者（连续直播的 live edge 延迟）。
   * 必须区分源模式：
   * - continuous-live-ts：最后一个缓冲区间末端 - 播放位置（缓冲即直播边）。
   * - hls：用 liveSessionAnchor 按墙钟外推直播边 - 播放位置。**绝不可**用
   *   buffer.end——HLS 按 10s 分片拉流，下载完一个分片 buffer.end 猛涨 10s，
   *   latency 会在 0~10s 间波动，把「分片下载」误判成「严重落后」→ 持续 1.2x
   *   追速 → 软解音频被迫加速（speed 1.4x）→ 漂移/毛刺/几分钟后没声音。
   * 缺省回退到 buffer.end（仅 continuous-live-ts 语义正确）。
   */
  liveEdgeLatency?: () => number | null;
}

const LIVE_STATE_TOLERANCE = 3;
/** 触发追速的最大可接受延迟（秒）。 */
const DEFAULT_MAX_LATENCY = 3;
/** 追速目标延迟（秒）。 */
const DEFAULT_TARGET_LATENCY = 1.5;
/** 追速用的 playbackRate（夹在 [1,2]）。 */
const DEFAULT_CHASE_RATE = 1.2;
/** 直播边下溢退避步长（秒）。 */
const UNDERRUN_BACKOFF_STEP = 1;
/** 退避上限（秒）。 */
const UNDERRUN_BACKOFF_MAX = 6;
/**
 * 追速迟滞（秒，迟滞窗口）。
 * 追速只会在 latency 超过 max + backoff + 本迟滞时（重新）开始，而停止点
 * 在远低于它的 target。形成死区：latency 在边界附近抖动（HLS 拉流/append
 * 不均，3-4s 常态）不再反复切换 1 ↔ 1.2——否则视频持续 1.2x，软解音频被迫
 * 跟随加速，消耗快于 worker 解码供给 → 音频链欠载 → 播放几分钟后没声音。
 */
const CHASE_HYSTERESIS = 1.0;

// ==================== 缓冲缺口跳过（gap skip） ====================
/**
 * 上游（CDN）在整点切换时会永久缺失约 3 个分片（≈30s 内容），MSE 缓冲因此出现空洞：
 * 播放头播到空洞前会一直 stall，不跳过就只能等停顿看门狗重载整条流（黑屏数十秒）。
 * 原生 HLS 播放器有 gapController 专门处理这种空洞，此处对齐之。
 */
/** 播放头距当前缓冲区间末端小于该值（秒）时才允许跳（避免正常播放中被误跳）。 */
const GAP_SKIP_TRIGGER_AHEAD_SEC = 0.35;
/** 小于该值的空洞视为正常缓冲空隙，不跳。 */
const GAP_SKIP_MIN_SEC = 0.5;
/** 大于该值的空洞不自动跳（可能是源整体断层/换段，交上层处理）。 */
const GAP_SKIP_MAX_SEC = 300;
/** 缺口之后至少要有这么多可播数据才跳（否则跳过去也会立刻 stall）。 */
const GAP_SKIP_MIN_TARGET_SEC = 0.3;
/** 跳到缺口后的微小前移，避免落在区间边界上。 */
const GAP_SKIP_EPSILON_SEC = 0.02;

export class PlaybackController {
  private currentRate = 1;
  private isLive = false;
  private extraLatency = 0;
  private readonly maxLatency: number;
  private readonly targetLatency: number;
  private readonly chaseRate: number;
  private readonly tolerance: number;
  private readonly mode: SourceMode;
  private liveSyncEnabled: boolean;
  private readonly liveEdgeLatencyProvider: (() => number | null) | undefined;
  private readonly onWaitingBound: () => void;

  constructor(
    private readonly video: HTMLVideoElement,
    private readonly callbacks: PlaybackControllerCallbacks = {},
    options: PlaybackControllerOptions = {},
  ) {
    this.mode = options.sourceMode ?? "continuous-live-ts";
    this.liveSyncEnabled = options.liveSync ?? true;
    this.maxLatency = options.maxLatency ?? DEFAULT_MAX_LATENCY;
    this.targetLatency = options.targetLatency ?? DEFAULT_TARGET_LATENCY;
    this.chaseRate = options.chaseRate ?? DEFAULT_CHASE_RATE;
    this.tolerance = options.tolerance ?? LIVE_STATE_TOLERANCE;
    this.liveEdgeLatencyProvider = options.liveEdgeLatency;
    // 直播边下溢退避：挂在 waiting 事件上
    this.onWaitingBound = () => this.onWaiting();
    this.video.addEventListener("waiting", this.onWaitingBound);
  }

  get sourceMode(): SourceMode {
    return this.mode;
  }

  /** 落后直播边的秒数（缓冲末端 - 播放位置）。 */
  get lagBehindLiveEdge(): number {
    const end = lastBufferedEnd(this.video);
    return end === null ? 0 : Math.max(0, end - this.video.currentTime);
  }

  get rate(): number {
    return this.currentRate;
  }

  /** 兼容旧接口：直播边改为直接读 video.buffered，无需外部喂区间。 */
  notifyBuffered(_ranges: BufferedRange[]): void {
    /* 直播边取自 video.buffered（各 SourceBuffer 的交集即实际可播区间） */
  }

  /** 启用/停用直播同步。 */
  setupLiveSync(enabled: boolean): void {
    this.liveSyncEnabled = enabled;
    if (!enabled) this.applyRate(1);
  }

  /**
   * 周期调用：按直播边延迟微调速率（**只调 rate，不 seek**）。
   * 唯一例外是「缓冲缺口跳过」：缺口是上游缺失内容造成的空洞，播放头必须前跳过去，
   * 否则会永久 stall，直到上层停顿看门狗重载整条流（黑屏数十秒）。
   */
  tick(): void {
    // 缺口检查对所有模式都做（点播/回看同样不该卡在缺口前）
    this.maybeSkipBufferGap();
    if (!this.liveSyncEnabled || this.mode === "static-ts-list") {
      this.applyRate(1);
      return;
    }

    const latency = this.liveEdgeLatency();
    if (latency === null) return;

    const range = lastBufferedRange(this.video);
    const isLive = range
      ? this.video.currentTime >= Math.max(range.start, range.end - this.targetLatency) - this.tolerance &&
        this.video.currentTime <= range.end + this.tolerance
      : false;
    if (isLive !== this.isLive) {
      this.isLive = isLive;
      this.callbacks.onLiveStateChange?.(isLive, latency);
    }

    if (latency > this.maxLatency + this.extraLatency + CHASE_HYSTERESIS) {
      // 落后过多（超过 max+backoff+迟滞）：加速追赶（不 seek）。
      // 迟滞避免 latency 抖动反复触发 1↔1.2。
      this.applyRate(Math.min(2, Math.max(1, this.chaseRate)));
    } else if (latency <= this.targetLatency + this.extraLatency) {
      this.applyRate(1);
      // 已恢复到目标延迟内 → 撤销退避
      if (this.extraLatency > 0 && latency <= this.targetLatency) {
        this.extraLatency = 0;
      }
    }
  }

  /**
   * 缓冲缺口跳过：播放头所在缓冲区间之后存在空洞（上游整点缺失分片等），且空洞之后已有
   * 可播数据时，直接前跳到空洞之后——把「卡在空洞前数十秒」变成「瞬间跳过缺失内容」。
   * 只在播放头已接近当前区间末端时触发，正常播放不会误跳。
   * @returns 是否已触发跳转
   */
  private maybeSkipBufferGap(): boolean {
    const video = this.video;
    if (video.seeking) return false;
    const b = video.buffered;
    if (b.length < 2) return false;
    const t = video.currentTime;
    let idx = -1;
    for (let i = 0; i < b.length; i++) {
      if (t >= b.start(i) && t <= b.end(i)) {
        idx = i;
        break;
      }
    }
    if (idx < 0 || idx + 1 >= b.length) return false;
    const currentEnd = b.end(idx);
    const nextStart = b.start(idx + 1);
    const gap = nextStart - currentEnd;
    if (currentEnd - t > GAP_SKIP_TRIGGER_AHEAD_SEC) return false; // 还没播到空洞跟前
    if (gap < GAP_SKIP_MIN_SEC || gap > GAP_SKIP_MAX_SEC) return false;
    if (b.end(idx + 1) - nextStart < GAP_SKIP_MIN_TARGET_SEC) return false; // 空洞后数据太少，跳过去也会再 stall
    const target = nextStart + GAP_SKIP_EPSILON_SEC;
    // eslint-disable-next-line no-console
    console.warn(
      `[MSE] 缓冲缺口 ${gap.toFixed(2)}s（${currentEnd.toFixed(2)} → ${nextStart.toFixed(2)}）→ 跳过缺失段`,
    );
    this.callbacks.onGapSkip?.(gap);
    try {
      video.currentTime = target;
    } catch {
      /* 某些 UA 在 readyState 不足时会抛错，忽略 */
    }
    return true;
  }

  /**
   * 直播边下溢退避：播放头已追到缓冲末端仍欠载（waiting）时，抬高延迟下限并回落速率。
   * 避免持续贴着直播边反复欠载（起播阶段尤其明显）。
   */
  private onWaiting(): void {
    if (!this.liveSyncEnabled || this.mode === "static-ts-list") return;
    // Seek / goLive 常带 waiting，此时仍有缓冲，不算欠载
    if (this.video.seeking) return;
    // 缺口优先：播放头停在空洞前（stall）→ 立即跳过缺失段，而非走下面的直播边退避
    if (this.maybeSkipBufferGap()) return;

    const latency = this.liveEdgeLatency();
    if (latency === null) return;

    const ahead = forwardBufferAhead(this.video);
    const atLiveEdge = latency < 0.5 && ahead < 0.5;
    if (!atLiveEdge) return;

    this.applyRate(1);
    if (this.extraLatency < UNDERRUN_BACKOFF_MAX) {
      this.extraLatency = Math.min(this.extraLatency + UNDERRUN_BACKOFF_STEP, UNDERRUN_BACKOFF_MAX);
    }
  }

  /** 直播边延迟：优先用注入的提供者（hls 用 liveSessionAnchor），否则最后一个
   *  缓冲区间末端 - 播放位置（仅 continuous-live-ts 语义正确）。 */
  private liveEdgeLatency(): number | null {
    if (this.liveEdgeLatencyProvider) return this.liveEdgeLatencyProvider();
    const range = lastBufferedRange(this.video);
    if (!range) return null;
    return range.end - this.video.currentTime;
  }

  private applyRate(rate: number): void {
    if (Math.abs(rate - this.currentRate) < 0.001) return;
    this.currentRate = rate;
    try {
      this.video.playbackRate = rate;
    } catch {
      /* 某些 UA 在 readyState 不足时会抛错，忽略 */
    }
    this.callbacks.onRateChange?.(rate);
  }

  /** 切源/重建时重置会话状态。 */
  reset(): void {
    this.isLive = false;
    this.extraLatency = 0;
    this.applyRate(1);
  }

  destroy(): void {
    this.video.removeEventListener("waiting", this.onWaitingBound);
  }
}

function lastBufferedRange(video: HTMLMediaElement): { start: number; end: number } | null {
  const b = video.buffered;
  if (b.length === 0) return null;
  const i = b.length - 1;
  return { start: b.start(i), end: b.end(i) };
}

function lastBufferedEnd(video: HTMLMediaElement): number | null {
  const r = lastBufferedRange(video);
  return r ? r.end : null;
}

/** 当前播放位置所在缓冲区间内、其后的可用秒数。 */
function forwardBufferAhead(video: HTMLMediaElement): number {
  const t = video.currentTime;
  const b = video.buffered;
  for (let i = 0; i < b.length; i++) {
    if (t >= b.start(i) && t <= b.end(i)) return b.end(i) - t;
  }
  return 0;
}
