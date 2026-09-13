/*
 * PCM Audio Player
 *
 * 播放软解 PCM（MP2 / AC-3 / E-AC-3），与 video 元素的时钟对齐。
 *
 * **排程 / 锚定 / 漂移纠偏全部由共用同步内核 `audio-sync-core.ts` 负责** —— 与
 * ac3-lab 跑的是同一份实现（lab 是这套策略的验证基准）。本文件只保留"播放引擎特有"
 * 的部分：
 *
 *   - 设备输出接线：AudioContext / gainNode / iOS 静音开关的 MediaStream 旁路
 *   - 自动播放解锁与 AudioContext 生命周期（suspended → 丢链，避免恢复时喷旧音频）
 *   - 源 PTS 跳变检测（AC-3 源流 PES 持续重叠会让 demuxer 偶尔误校准）
 *   - "音频整体排不出去"时的兜底：请 worker 重钉时间轴 / 上报不可恢复
 *   - 诊断日志
 *
 * 内核负责的三条硬不变量（锚定只"跳"不"重放"、锚定前视频时钟必须确实在推进、
 * ctx 时间 ↔ 媒体时间按实际排出去的内容记账）与纠偏策略，见 `audio-sync-core.ts`
 * 与 `doc/PLAYER-SYNC-REDESIGN.md`。
 */

import { isIOS } from "../../lib/platform";
import type { PlayerConfig } from "../config";
import Log from "../utils/logger";
import { AudioSyncCore } from "./audio-sync-core";
import { WasmStretcher } from "./wasm-stretcher";

const TAG = "PCMAudioPlayer";

/**
 * Page-level one-shot autoplay gate for Web Audio.
 * Set when playback has started (any codec), the click-to-resume prompt was
 * already shown, or AudioContext.resume() succeeded — suppresses re-prompting
 * on later channel switches that create a new AudioContext.
 */
let playbackUnlocked = false;

/** Call when video playback has been allowed by a user gesture or successful play(). */
export function markPlaybackUnlocked(): void {
  playbackUnlocked = true;
}

/** 已排程音频相对图时间的前瞻窗口（秒）。窗口越小，纠偏响应越快。 */
const SCHEDULE_AHEAD_SEC = 0.6;
/** 页面隐藏时定时器被节流（移动端 1s，桌面最长 1/min），前瞻窗口必须放大。 */
const BACKGROUND_SCHEDULE_AHEAD_SEC = 6.0;
/** 未排程队列长度保险丝（纯内存保护）。AC-3 每 chunk 32ms，预缓冲源合法地会一次喂进 ~60s。 */
const MAX_QUEUE_CHUNKS = 3000;
/** 持续排不出去多久后上报不可恢复（交给上层重建会话）。 */
const RESYNC_FAILED_AFTER_MS = 15000;
/** re-anchor 后判定"旧时间轴残留 PCM"的领先阈值：超过锚点这么多秒的块直接丢弃。 */
const REANCHOR_STALE_LEAD_SEC = 2.5;
/** 控制环周期（毫秒）。 */
const CONTROL_INTERVAL_MS = 250;
/** 每多少次控制 tick 打印一行漂移诊断（约 60s）。 */
const DRIFT_LOG_TICKS = 240;

/**
 * 软解音频输出器。对外接口保持稳定（init / attachVideo / detachVideo / feed / play /
 * pause / stop / setVolume / setMuted / destroy / confirmVideoAnchor + 4 个回调）。
 */
export class PCMAudioPlayer {
  private readonly config: PlayerConfig;
  private context: AudioContext | null = null;
  private gainNode: GainNode | null = null;
  private volume = 1;
  private muted = false;

  private videoElement: HTMLVideoElement | null = null;
  private audioElement: HTMLAudioElement | null = null;
  private mediaStreamDestination: MediaStreamAudioDestinationNode | null = null;

  /** 排程/锚定/漂移纠偏都在内核里（与 ac3-lab 共用同一份实现）。 */
  private core: AudioSyncCore | null = null;

  private pageHidden = false;

  // ---- 排不出去时的兜底 ----
  private blockedSince: number | null = null;
  private reportedUnrecoverable = false;
  private autoplaySuspendedNotified = false;

  /** re-anchor 后、新时间轴样本到达前：丢弃仍落在旧未来时间轴的残留 PCM（worker 异步回传竞态）。 */
  private awaitingNewTimeline = false;
  private reanchorAtSec = 0;

  // ---- 诊断 ----
  private driftLogCounter = 0;

  private controlTimer: ReturnType<typeof setInterval> | null = null;
  private boundOnVisibilityChange: (() => void) | null = null;
  private boundOnVideoSeeking: (() => void) | null = null;
  private boundOnVideoSeeked: (() => void) | null = null;
  private boundOnVideoPlay: (() => void) | null = null;
  private boundOnVideoPause: (() => void) | null = null;
  private boundOnVolumeChange: (() => void) | null = null;
  private boundOnTimeUpdate: (() => void) | null = null;
  private boundOnRateChange: (() => void) | null = null;

  /** Autoplay was blocked / the audio session was lost; the app may prompt. */
  onSuspended: (() => void) | null = null;
  /** Audio could not re-anchor to a live video clock (session should be rebuilt). */
  onResyncFailed: (() => void) | null = null;
  /** Audio never established a usable timeline with the video. */
  onStartupSyncFailed: (() => void) | null = null;
  /**
   * 音频时间轴整体落在播放头之后（源 PTS 与 MSE 视频轴不同源），请 worker 把后续
   * PCM 重钉到给定视频时间上。
   */
  onVideoAnchorRequested: ((videoTimeSec: number) => void) | null = null;

  constructor(config: PlayerConfig) {
    this.config = config;
  }

  // ==================== Lifecycle ====================

  async init(): Promise<void> {
    if (this.context) {
      return;
    }

    this.context = new AudioContext();
    this.gainNode = this.context.createGain();

    if (isIOS()) {
      try {
        // iOS 静音开关会掐掉 WebAudio 的默认输出，绕一路 MediaStream 进 <audio>。
        this.mediaStreamDestination = this.context.createMediaStreamDestination();
        this.gainNode.connect(this.mediaStreamDestination);

        this.audioElement = document.createElement("audio");
        this.audioElement.srcObject = this.mediaStreamDestination.stream;
        this.audioElement.autoplay = true;
        this.audioElement.setAttribute("playsinline", "");
        this.audioElement.setAttribute("webkit-playsinline", "");

        Log.v(TAG, "iOS detected: using MediaStream bypass for Silent Mode");
      } catch (_e) {
        Log.w(TAG, "Failed to create MediaStream destination, falling back to default output");
        this.gainNode.connect(this.context.destination);
      }
    } else {
      this.gainNode.connect(this.context.destination);
    }

    this.updateGain();

    this.context.onstatechange = () => {
      const state = this.context?.state as string | undefined;
      Log.v(TAG, `AudioContext state changed to: ${state}`);
      if (state === "running") {
        playbackUnlocked = true;
        this.autoplaySuspendedNotified = false;
        // 从我们自己的 suspend()（pause）恢复：队列里的样本会重新落点。
        this.core?.pump();
        return;
      }
      // suspended / interrupted：丢掉已排好的链与队列，避免恢复时把陈旧音频一次性喷出来。
      this.core?.resetChain();
    };

    Log.v(TAG, `AudioContext initialized, sampleRate: ${this.context.sampleRate}, state: ${this.context.state}`);
  }

  attachVideo(video: HTMLVideoElement): void {
    this.videoElement = video;

    // 同步初始音量状态
    this.setVolume(video.volume);
    this.setMuted(video.muted);

    // 换元素/换会话：旧链与旧队列的时间轴已失效，直接换一个新的内核实例。
    this.core?.destroy();
    this.core = this.context
      ? new AudioSyncCore({
          ctx: this.context,
          video,
          destination: this.gainNode ?? undefined,
          onLog: (message) => Log.v(TAG, message),
          getRate: () => this.videoRate(),
          getScheduleAheadSec: () => (this.pageHidden ? BACKGROUND_SCHEDULE_AHEAD_SEC : SCHEDULE_AHEAD_SEC),
          maxQueueChunks: MAX_QUEUE_CHUNKS,
          // 恢复 lab 同款的"漂移硬重锚"作为兜底网（内核默认 REANCHOR_DRIFT_SEC）。
          // 之前为躲 AC-3 PTS 重叠接缝而关掉它，但关掉后音频轴在 overlap 自纠时
          // 会持续往前漂、又没有任何其它纠偏把它拉回视频轴 → 画音不同步。
          // 真正的 per-异常重基由 worker 的 setAudioVideoAnchor 完成（见 pipeline.ts），
          // 这里只兜底"缓慢残余漂移"，阈值远小于段内 overlap 累积量，不会频繁接缝。
          onSchedulingBlocked: () => this.noteSchedulingBlocked(),
          onSchedulingResumed: () => this.clearSchedulingBlocked(),
          // 追延迟/微调改为"保音高的时域伸缩"，不再用 AudioBufferSourceNode.playbackRate
          // （那是重采样，1.2x 会升高约 3 个半音）。拿不到 wasm 时内核会自动退回
          // playbackRate 跟随 —— 变调但能同步，不会没声音。
          stretcherFactory: (sampleRate, channels) => this.createStretcher(sampleRate, channels),
        })
      : null;
    this.core?.setCalibrationMs(this.config.audioSyncOffsetMs);
    this.core?.start();

    this.boundOnVideoSeeking = () => this.onVideoSeeking();
    this.boundOnVideoSeeked = () => this.onVideoSeeked();
    this.boundOnVideoPlay = () => void this.play();
    this.boundOnVideoPause = () => this.pause();
    this.boundOnVolumeChange = () => {
      this.setVolume(video.volume);
      this.setMuted(video.muted);
    };
    this.boundOnTimeUpdate = () => this.controlTick();
    this.boundOnRateChange = () => this.onVideoRateChange();

    video.addEventListener("seeking", this.boundOnVideoSeeking);
    video.addEventListener("seeked", this.boundOnVideoSeeked);
    video.addEventListener("play", this.boundOnVideoPlay);
    video.addEventListener("pause", this.boundOnVideoPause);
    video.addEventListener("volumechange", this.boundOnVolumeChange);
    video.addEventListener("timeupdate", this.boundOnTimeUpdate);
    video.addEventListener("ratechange", this.boundOnRateChange);

    this.boundOnVisibilityChange = () => this.onVisibilityChange();
    document.addEventListener("visibilitychange", this.boundOnVisibilityChange);
    this.pageHidden = document.visibilityState === "hidden";

    this.controlTimer = setInterval(() => this.controlTick(), CONTROL_INTERVAL_MS);
  }

  detachVideo(): void {
    this.core?.destroy();
    this.core = null;

    if (this.controlTimer) {
      clearInterval(this.controlTimer);
      this.controlTimer = null;
    }
    if (this.boundOnVisibilityChange) {
      document.removeEventListener("visibilitychange", this.boundOnVisibilityChange);
      this.boundOnVisibilityChange = null;
    }
    if (this.videoElement) {
      if (this.boundOnVideoSeeking) this.videoElement.removeEventListener("seeking", this.boundOnVideoSeeking);
      if (this.boundOnVideoSeeked) this.videoElement.removeEventListener("seeked", this.boundOnVideoSeeked);
      if (this.boundOnVideoPlay) this.videoElement.removeEventListener("play", this.boundOnVideoPlay);
      if (this.boundOnVideoPause) this.videoElement.removeEventListener("pause", this.boundOnVideoPause);
      if (this.boundOnVolumeChange) this.videoElement.removeEventListener("volumechange", this.boundOnVolumeChange);
      if (this.boundOnTimeUpdate) this.videoElement.removeEventListener("timeupdate", this.boundOnTimeUpdate);
      if (this.boundOnRateChange) this.videoElement.removeEventListener("ratechange", this.boundOnRateChange);
    }
    this.boundOnVideoSeeking = null;
    this.boundOnVideoSeeked = null;
    this.boundOnVideoPlay = null;
    this.boundOnVideoPause = null;
    this.boundOnVolumeChange = null;
    this.boundOnTimeUpdate = null;
    this.boundOnRateChange = null;
    this.clearSchedulingBlocked();
    this.videoElement = null;
  }

  // ==================== Input ====================

  /** `time` is normalized to the MSE timeline (same space as video.currentTime). */
  feed(samples: Float32Array, channels: number, sampleRate: number, time: number): void {
    const core = this.core;
    if (!this.context || !core) {
      Log.w(TAG, "AudioContext/core not ready, dropping audio");
      return;
    }

    // re-anchor 竞态：worker 重钉时间轴的命令是异步的，re-anchor 前已用 postMessage 发出的
    // 旧时间轴 PCM 会"后到"主线程、排在新样本前面，从而被 backward-jump 误判而丢声。这里在
    // 收到新时间轴样本前，先丢弃仍明显领先锚点的残留块。
    if (this.awaitingNewTimeline) {
      if (time > this.reanchorAtSec + REANCHOR_STALE_LEAD_SEC) {
        return;
      }
      this.awaitingNewTimeline = false;
    }

    const samplesPerChannel = Math.floor(samples.length / channels);
    if (samplesPerChannel === 0 || channels <= 0 || sampleRate <= 0) {
      return;
    }

    const duration = samplesPerChannel / sampleRate;

    // 跳变检测 / 丢陈旧头 / 队列保险丝都由 AudioSyncCore.enqueue() 内部统一处理。
    // 与 lab 对齐：lab 也是直接 enqueue，不在外层做额外检测。
    // 之前在这里做的 forward/backward jump 检测会与内核的跳变处理互相干扰，
    // 特别是在 discontinuity 后新时间轴的合法 PCM 被误判为 "forward jump" 而丢弃 → 静音。
    core.enqueue({ pcm: samples, channels, sampleRate, timeSec: time, durationSec: duration });
  }

  // ==================== Control loop ====================

  private controlTick(): void {
    const core = this.core;
    if (!core || !this.context) {
      return;
    }

    // 排程 + "漂移过大就硬重锚"的纠偏策略都在内核里（与 ac3-lab 共用同一份）。
    // 页面隐藏时定时器被节流、排程窗口放大，此时不重锚。
    core.controlTick(!this.pageHidden);

    if (++this.driftLogCounter >= DRIFT_LOG_TICKS) {
      this.driftLogCounter = 0;
      const drift = core.driftSec();
      const stats = core.stats();
      const driftText = drift === null ? "n/a" : `${(drift * 1000).toFixed(1)}ms`;
      Log.v(
        TAG,
        `A/V drift=${driftText}, avOut=${(core.getOutputLatencySec() * 1000).toFixed(0)}ms, ` +
          `vidLead=${(core.getDisplayLeadSec() * 1000).toFixed(0)}ms, ` +
          `rate=${this.videoRate().toFixed(2)}, queue=${stats.queueSec.toFixed(2)}s, ` +
          `reanchor/drop/underrun=${stats.reanchors}/${stats.droppedStale}/${stats.underruns}`,
      );
    }
  }

  /**
   * 音频整体落在播放头之前（源 PTS 跑在 MSE 视频前面）时的兜底。
   * Lab 没有 this 问题——它在 MSE 层用 waitForBufferRoom 限速，音频不会跑太前面。
   * Player 的 worker 没有 MSE 限速，PCM 一路灌进来 → 被 MAX_SCHEDULE_LEAD_SEC 挡住。
   *
   * 策略：不 reanchor，也不 trim 队列。队列里的 chunk 是合法的未来音频，
   * 视频时钟在走就会自然追上来，pump 届时会锚定并消费。
   * 旧实现在这里调 requestReanchor → 清队 → worker 重锚 → confirmVideoAnchor →
   * 又 reanchor……形成"始终重新对齐"的循环。更早的实现在这里调 trimAudioLead，
   * 结果"进来一个丢一个"→ 永远静音。
   */
  private noteSchedulingBlocked(): void {
    const now = performance.now();
    if (this.blockedSince === null) {
      this.blockedSince = now;
      return;
    }
    const blockedMs = now - this.blockedSince;

    // 不 requestReanchor，也不 trim 队列：视频时钟在走就会追上来。
    // 只在长时间（15s）排不出去且从未成功排过的情况下上报不可恢复。

    const hasScheduled = (this.core?.stats().scheduledChunks ?? 0) > 0;
    if (hasScheduled && blockedMs > RESYNC_FAILED_AFTER_MS && !this.reportedUnrecoverable) {
      this.reportedUnrecoverable = true;
      Log.e(TAG, `Audio stalled for ${Math.round(blockedMs)}ms behind an unreachable playhead`);
      this.onResyncFailed?.();
    }
  }

  private clearSchedulingBlocked(): void {
    this.blockedSince = null;
    this.reportedUnrecoverable = false;
  }

  private onVisibilityChange(): void {
    this.pageHidden = document.visibilityState === "hidden";
    if (!this.pageHidden) {
      this.core?.pump();
    }
  }

  // ==================== Video events ====================

  private onVideoSeeking(): void {
    // 新位置的时间轴与旧 PCM 无关，直接丢链丢队列，等 seek 后的样本重新落点。
    this.awaitingNewTimeline = true;
    this.reanchorAtSec = this.core?.visibleVideoTime() ?? 0;
    this.core?.resetChain();
    this.driftLogCounter = 0;
  }

  private onVideoSeeked(): void {
    // seek 后新 PCM 到达前的时间轴是空的；强制 reanchor 确保新样本按当前位置落点。
    this.awaitingNewTimeline = true;
    this.reanchorAtSec = this.core?.visibleVideoTime() ?? 0;
    this.core?.reanchor("video-seeked", true);
  }

  /**
   * live-sync 改变 video.playbackRate 时的处理，**取决于走哪条变速路径**：
   *
   *  - 伸缩级可用：新产出的输出会自动带上新 ratio，已排好的 span 最多 0.6s 就播完，
   *    残余漂移由微调环吸收。此时**不要**硬重锚 —— 每次变速都重锚会在每次追速开始时
   *    留下一次可听见的接缝（丢 ~52ms 预填 + ~48ms 尾部）。
   *  - 退回 playbackRate 跟随：旧 span（旧速率）与新 span（新速率）之间有撕裂空洞，
   *    链式排程会逐步断裂导致持续错位，必须立刻按屏上帧重建链。
   */
  private onVideoRateChange(): void {
    if (this.core?.isStretching()) {
      return;
    }
    this.core?.reanchor("rate-change", true);
  }

  async play(): Promise<void> {
    if (this.context && this.context.state !== "running") {
      try {
        await this.context.resume();
        playbackUnlocked = true;
        // onstatechange 会接着 pump
      } catch (_e) {
        Log.w(TAG, "Failed to resume AudioContext on play()");
        this.notifyAutoplayBlocked();
      }
    } else {
      this.core?.pump();
    }

    if (this.audioElement) {
      try {
        await this.audioElement.play();
      } catch (_e) {
        Log.w(TAG, "Failed to play audio element");
      }
    }
  }

  pause(): void {
    this.core?.resetChain();

    if (this.context?.state === "running") {
      this.context.suspend();
    }

    if (this.audioElement) {
      this.audioElement.pause();
    }
  }

  stop(): void {
    this.core?.resetChain();
    this.driftLogCounter = 0;
    this.clearSchedulingBlocked();
  }

  /** worker 后续 PCM 已重钉到新时间轴：此时队列已在请求处清空，只需在新锚点排程。 */
  confirmVideoAnchor(videoTimeSec: number): void {
    Log.v(TAG, `Audio anchor confirmed at ${videoTimeSec.toFixed(3)}s; rebuilding chain`);
    // worker 刚确认重钉：仍可能有旧时间轴残留 PCM 在途，先按新锚点丢弃，直到收到新样本。
    this.awaitingNewTimeline = true;
    this.reanchorAtSec = videoTimeSec;
    this.core?.reanchor("video-anchor", true);
  }

  private notifyAutoplayBlocked(): void {
    if (playbackUnlocked || this.autoplaySuspendedNotified) {
      return;
    }
    this.autoplaySuspendedNotified = true;
    Log.w(TAG, "AudioContext is suspended (autoplay blocked); waiting for a user gesture");
    this.onSuspended?.();
  }

  // ==================== Volume ====================

  setVolume(volume: number): void {
    this.volume = Math.max(0, Math.min(1, volume));
    this.updateGain();
  }

  setMuted(muted: boolean): void {
    this.muted = muted;
    this.updateGain();
  }

  private updateGain(): void {
    if (this.gainNode && this.context) {
      const g = this.gainNode.gain;
      g.cancelScheduledValues(this.context.currentTime);
      g.value = this.muted ? 0 : this.volume;
    }
    if (this.audioElement) {
      this.audioElement.volume = this.muted ? 0 : this.volume;
    }
  }

  /** live-sync 追延迟会改 video.playbackRate；内核让伸缩器按同一速率（保音高）跟随。 */
  private videoRate(): number {
    const rate = this.videoElement?.playbackRate ?? 1;
    return Math.min(2, Math.max(0.5, rate || 1));
  }

  /**
   * WSOLA 伸缩器的惰性工厂（内核在拿到第一块样本时才调用）。
   *
   * 用统一 wasm（同一个二进制同时提供 MP2/AC-3/E-AC-3 解码与 `wsola_*`）。拿不到就返回
   * null，内核退回"链式 1:1 + playbackRate 跟随"——**这条回退路径必须保留**，否则老设备
   * 在 wasm 加载失败时会彻底没声音。
   */
  private async createStretcher(sampleRate: number, channels: number): Promise<WasmStretcher | null> {
    const wasmUrl = this.config.wasmDecoders.ac3 ?? this.config.wasmDecoders.mp2;
    if (!wasmUrl) {
      return null;
    }
    try {
      return await WasmStretcher.create(wasmUrl, sampleRate, channels);
    } catch (error) {
      Log.w(TAG, `WSOLA stretcher unavailable, falling back to playbackRate catch-up: ${String(error)}`);
      return null;
    }
  }

  // ==================== Teardown ====================

  async destroy(): Promise<void> {
    this.core?.destroy();
    this.core = null;
    this.detachVideo();

    if (this.audioElement) {
      this.audioElement.pause();
      this.audioElement.srcObject = null;
      this.audioElement = null;
    }

    if (this.mediaStreamDestination) {
      this.mediaStreamDestination.disconnect();
      this.mediaStreamDestination = null;
    }

    if (this.gainNode) {
      this.gainNode.disconnect();
      this.gainNode = null;
    }

    if (this.context) {
      this.context.onstatechange = null;
      await this.context.close();
      this.context = null;
    }
  }
}
