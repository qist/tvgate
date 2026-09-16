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
import type { PcmWorkerStats } from "../worker/messages";
import Log from "../utils/logger";
import { AudioSyncCore } from "./audio-sync-core";
import { WasmStretcher } from "./wasm-stretcher";

const TAG = "PCMAudioPlayer";

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
/** "音频超前"持续多久才允许做一次轴平移自愈（毫秒）。 */
const SUSTAINED_BLOCK_MS = 3000;
/** 两次轴平移自愈的最小间隔（毫秒），杜绝测量噪声导致的来回平移。 */
const REBASE_MIN_INTERVAL_MS = 10_000;
/** 音频领先播放头超过该秒数才认为需要轴平移自愈。 */
const MIN_AHEAD_SEC_FOR_REBASE = 2.0;
/** blocked 期间视频时钟至少推进这么多秒才判定为"音频超前"（排除缓冲停摆误判）。 */
const MIN_VIDEO_ADVANCE_SEC = 0.5;
/** 回前台对齐的最小领先量（秒）：低于它音频链继续播即可，无需干预。 */
const FOREGROUND_ALIGN_MIN_LEAD_SEC = 0.5;
/**
 * 静音看门狗：软解音频曾经出过声、随后超过该时长再也没有可听内容，而视频时钟
 * 确实在推进（readyState 已到 HAVE_FUTURE_DATA）→ 判定音频链已停摆，强制按当前
 * 播放头重建排程链。
 *
 * 为什么需要：内核的排程门（desired 超出 2s 即 blocked）与 reanchor 的陈旧水位
 * 都假设"队列里还有合法未来音频"。网络断流重连后视频轴前跳、PCM 缺口（worker
 * 补静音/跳过）等场景会让新 PCM 迟迟进不到播放头附近，于是既不排程、也没人纠正，
 * 表现为"画面在走、声音永远不回来"。这是兜底网，正常情况下永不触发。
 */
const SILENT_STALL_MS = 5000;
/** 看门狗最少观察窗口：排程链本身有 ~0.6s 前瞻，且允许一次正常 underrun，避免误判。 */
const SILENT_STALL_MIN_UPTIME_MS = 3000;

/**
 * 软解音频输出器。对外接口保持稳定（init / attachVideo / detachVideo / feed / play /
 * pause / setVolume / setMuted / destroy / confirmVideoAnchor + 3 个回调）。
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
  /** 进入后台后是否已尝试恢复被 UA 暂停的 video（只对抗一次，避免 play-pause 循环）。 */
  private backgroundResumeAttempted = false;

  // ---- 静音看门狗（音频链停摆自愈）----
  /** 挂载后首次成功排程的时刻（毫秒）；用于让看门狗避开正常的启动/缓冲期。 */
  private chainStartedAtMs: number | null = null;
  /** 最近一次音频内容真实推进的时刻；停摆判定以它为基准。 */
  private lastAudioProgressMs = 0;
  private lastAudioProgressSec = Number.NaN;
  private silentWatchdogTrips = 0;

  /** 声道输出模式：mono = 左右合成 (L+R)/2（解决分离声道源手机端只能听到单边）。 */
  private channelMode: "stereo" | "mono" = "stereo";

  // ---- 排不出去时的兜底 ----
  private blockedSince: number | null = null;
  /** blocked 开始时的视频时钟，用于判定 blocked 期间视频是否确实在推进。 */
  private blockedVideoClockSec = 0;
  private reportedUnrecoverable = false;
  private autoplaySuspendedNotified = false;

  // ---- 轴平移自愈（音频轴持续超前时的主修复手段）----
  private lastRebaseAt = -Infinity;
  private rebaseCount = 0;

  /** re-anchor 后、新时间轴样本到达前：丢弃仍落在旧未来时间轴的残留 PCM（worker 异步回传竞态）。 */
  private awaitingNewTimeline = false;
  private reanchorAtSec = 0;

  /**
   * 回前台对齐挂起标志：visibilitychange 时刻视频时钟往往仍冻结（后台被 UA 节流、
   * 回前台后 MSE 还在缓冲），立即对齐会把音频钉在过时位置。推迟到 controlTick
   * 确认时钟恢复推进后执行（alignToVideoOnReturn）。
   */
  private pendingForegroundAlign = false;
  /** 回前台对齐 seek：seek 后 seeking/seeked 事件不清链不重锚（音频正在播 heard 内容）。 */
  private aligningSeek = false;

  // ---- 诊断 ----
  private driftLogCounter = 0;
  /** worker 侧软解 PCM 链路丢弃计数（经 pcm-audio-stats 消息更新）。 */
  private pipelineStats: PcmWorkerStats | null = null;
  /** 上一诊断窗口的快照：用于把丢弃计数打成**增量**（定位"卡顿一下"来自哪个环节）。 */
  private lastPipelineStatsForDelta: PcmWorkerStats | null = null;
  private lastDroppedStaleForDelta = 0;
  private lastUnderrunsForDelta = 0;

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
  /** worker 汇报的软解 PCM 链路丢弃计数（最新快照）。 */
  onAudioStats: ((stats: PcmWorkerStats) => void) | null = null;

  constructor(config: PlayerConfig) {
    this.config = config;
    this.channelMode = config.audioChannelMode ?? "stereo";
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
        this.autoplaySuspendedNotified = false;
        // 从我们自己的 suspend()（pause）恢复：队列里的样本会重新落点。
        this.core?.pump();
        return;
      }
      // suspended / interrupted：前台丢链避免恢复时喷陈旧音频。后台的系统级
      // suspend（页面隐藏时 UA 直接挂起 WebAudio）必须保留链与队列——否则
      // 切后台立即静音，回前台也无队列可恢复（free-run 语义，见上）。
      if (document.visibilityState === "hidden") return;
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
          // 页面隐藏**或回前台恢复待执行**时保持 free-run：回前台瞬间视频时钟仍冻结
          // （后台被 UA 节流/解码未恢复），若立即切回"按视频时钟排程"，排程门会把
          // 音频压死（等待期静音）。pendingForegroundAlign 期间沿用后台窗口，直到
          // alignToVideoOnReturn 在时钟恢复推进后对齐一次到位。
          getScheduleAheadSec: () =>
            this.pageHidden || this.pendingForegroundAlign ? BACKGROUND_SCHEDULE_AHEAD_SEC : SCHEDULE_AHEAD_SEC,
          getPageHidden: () => this.pageHidden || this.pendingForegroundAlign,
          maxQueueChunks: MAX_QUEUE_CHUNKS,
          // 恢复 lab 同款的"漂移硬重锚"作为兜底网（内核默认 REANCHOR_DRIFT_SEC）。
          // 之前为躲 AC-3 PTS 重叠接缝而关掉它，但关掉后音频轴在 overlap 自纠时
          // 会持续往前漂、又没有任何其它纠偏把它拉回视频轴 → 画音不同步。
          // 真正的"轴整体跳变"自愈由 onSchedulingBlocked → maybeRebaseAxis 完成，
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
    // 新内核 = 新会话：看门狗基准与诊断增量一并归零，避免拿上一会话的数据误判。
    this.chainStartedAtMs = null;
    this.lastAudioProgressSec = Number.NaN;
    this.lastAudioProgressMs = performance.now();
    this.lastPipelineStatsForDelta = null;
    this.lastDroppedStaleForDelta = 0;
    this.lastUnderrunsForDelta = 0;

    this.boundOnVideoSeeking = () => this.onVideoSeeking();
    this.boundOnVideoSeeked = () => this.onVideoSeeked();
    this.boundOnVideoPlay = () => void this.play();
    this.boundOnVideoPause = () => {
      // 后台浏览器会暂停 video（软解源 video 无音轨，被判"无音频播放"），该暂停是
      // 被动节流而非用户意图——若照常 pause() 会 resetChain + suspend AudioContext，
      // 软解音频立即静音（AAC 走 video 内嵌音频不受影响）。后台必须忽略，让音频
      // free-run，回前台由 visibilitychange 恢复。
      if (document.visibilityState === "hidden") {
        // 顺带把被 UA 暂停的 video 恢复播放：让 currentTime 继续推进，音视频时间轴
        // 保持同步，否则音频 free-run 分离（drift 持续增大），回前台大幅错位/倒退。
        // 只对抗一次，避免与 UA 策略形成 play-pause 循环。
        if (!this.backgroundResumeAttempted && this.videoElement) {
          this.backgroundResumeAttempted = true;
          this.videoElement.play().catch(() => {});
        }
        return;
      }
      this.pause();
    };
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

  /** 运行时切换声道输出模式（stereo/mono），立即作用于后续 feed 的 PCM。 */
  setAudioChannelMode(mode: "stereo" | "mono"): void {
    if (this.channelMode === mode) {
      return;
    }
    this.channelMode = mode;
    Log.i(TAG, `audio channel mode -> ${mode}`);
  }

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

    // 单声道合成：左右声道相加取均值。分离声道源（L=对白 R=音乐）手机端只
    // 输出单边时，合成后两边内容都能听到。合成输出 1ch，下游内核/WSOLA 均支持。
    if (this.channelMode === "mono" && channels === 2) {
      const mono = new Float32Array(samplesPerChannel);
      for (let i = 0; i < samplesPerChannel; i++) {
        mono[i] = (samples[i * 2] + samples[i * 2 + 1]) * 0.5;
      }
      samples = mono;
      channels = 1;
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

    // 漂移硬重锚要求视频时钟确实在推进：视频冻结期间（后台 free-run、回前台恢复
    // 待执行、解冻前）drift 会随音频链推进自然拉大，此时重锚只会砍掉后台预排的
    // 音频链（最多 6s 前瞻），等视频恢复推进后由下面的回前台对齐统一处理。
    core.controlTick(!this.pageHidden && !this.pendingForegroundAlign && core.isVideoClockAdvancing());

    this.audioStallWatchdog(core);

    // 回前台对齐：推迟到确认视频时钟恢复推进。等待期音频保持 free-run 继续播，
    // 覆盖视频解冻的间隙；时钟一走就执行对齐（seek video 到 heard，音频不中断）。
    if (this.pendingForegroundAlign && !this.pageHidden && core.isVideoClockAdvancing()) {
      this.pendingForegroundAlign = false;
      this.alignToVideoOnReturn();
    }

    if (++this.driftLogCounter >= DRIFT_LOG_TICKS) {
      this.driftLogCounter = 0;
      const drift = core.driftSec();
      const stats = core.stats();
      const driftText = drift === null ? "n/a" : `${(drift * 1000).toFixed(1)}ms`;
      const pipe = this.pipelineStats;
      const pipeText = pipe
        ? `, pipe[drop=${pipe.remuxDrop} trim=${pipe.trimDrop} ovf=${pipe.pendingOverflowDrops + pipe.queueOverflowDrops}` +
          ` gen=${pipe.genDrops} carry=${pipe.carryFrames} rend=${pipe.renditionFrames}]`
        : "";
      Log.v(
        TAG,
        `A/V drift=${driftText}, avOut=${(core.getOutputLatencySec() * 1000).toFixed(0)}ms, ` +
          `vidLead=${(core.getDisplayLeadSec() * 1000).toFixed(0)}ms, ` +
          `rate=${this.videoRate().toFixed(2)}, queue=${stats.queueSec.toFixed(2)}s, ` +
          `reanchor/drop/underrun=${stats.reanchors}/${stats.droppedStale}/${stats.underruns}` +
          `, rebase=${this.rebaseCount}, stall=${this.silentWatchdogTrips}${pipeText}`,
      );

      // "播放中声音卡顿一下"的现场诊断：把这一窗口内各丢弃环节的**增量**打成 WARN。
      // 各环节都为零 = 卡顿不来自软解 PCM 链路（查 MSE/bridging）；非零则直接定位
      // 是 worker 侧丢（remux/trim/ovf）还是主线程侧丢（drop=过期、under=欠载）。
      if (pipe) {
        const prev = this.lastPipelineStatsForDelta;
        const d = (get: (s: PcmWorkerStats) => number): number => (prev ? get(pipe) - get(prev) : 0);
        const pipedDrops =
          d((s) => s.remuxDrop) +
          d((s) => s.trimDrop) +
          d((s) => s.pendingOverflowDrops) +
          d((s) => s.queueOverflowDrops) +
          d((s) => s.genDrops);
        const staleDelta = stats.droppedStale - this.lastDroppedStaleForDelta;
        const underrunDelta = stats.underruns - this.lastUnderrunsForDelta;
        if (pipedDrops > 0 || staleDelta > 0 || underrunDelta > 0) {
          Log.w(
            TAG,
            `Audio drops in last ~60s: worker[remux=${d((s) => s.remuxDrop)} trim=${d((s) => s.trimDrop)} ` +
              `ovf=${d((s) => s.pendingOverflowDrops) + d((s) => s.queueOverflowDrops)} ` +
              `gen=${d((s) => s.genDrops)} carry=${d((s) => s.carryFrames)}] ` +
              `player[stale=${staleDelta} underrun=${underrunDelta} reanchor=${stats.reanchors} ` +
              `stall=${this.silentWatchdogTrips}]`,
          );
        }
        this.lastPipelineStatsForDelta = pipe;
      }
      this.lastDroppedStaleForDelta = stats.droppedStale;
      this.lastUnderrunsForDelta = stats.underruns;
    }
  }

  /** worker 推送的软解 PCM 丢弃计数快照；同时转发给上层（audio-stats 事件）。 */
  setPipelineStats(stats: PcmWorkerStats): void {
    this.pipelineStats = stats;
    this.onAudioStats?.(stats);
  }

  /**
   * 静音看门狗：音频曾经出过声、随后长时间没有任何可听内容，而视频时钟确实在推进
   * ——说明音频链与视频轴脱节（重连后 PCM 缺口、轴错位、worker 停摆等），此时
   * `controlTick` 的漂移环看不到任何东西（drift 为 null、队列为空），不会有任何
   * 自愈动作。兜底动作是按当前屏上帧时间强制重建排程链，让后续到达的 PCM 直接
   * 锚定到播放头。正常情况下（链路健康）永不触发。
   */
  private audioStallWatchdog(core: AudioSyncCore): void {
    const stats = core.stats();
    // 有可听内容（已排程 / 已产出待排 / 队列有待排）→ 记录推进并退出。
    if (stats.scheduledAheadSec > 0 || stats.queueSec > 0) {
      // 用内核的**内容游标**判定推进：每次新排出/产出/入队的内容都会让它前移。
      // 不能用队头或"最后一个 span 的结束时间"——它们在自动推进时可能长时间不变，
      // 会把正常播放误判成停摆（误触发的重锚本身就是一次可听的卡顿）。
      const cursor = core.contentCursorSec();
      if (cursor !== null && (!Number.isFinite(this.lastAudioProgressSec) || cursor > this.lastAudioProgressSec)) {
        this.lastAudioProgressSec = cursor;
        this.lastAudioProgressMs = performance.now();
      }
      if (this.chainStartedAtMs === null && stats.scheduledChunks > 0) {
        this.chainStartedAtMs = performance.now();
      }
      return;
    }

    // 从未成功排程过（起播阶段）→ 交给内核自身的锚定逻辑，不介入。
    if (this.chainStartedAtMs === null) return;
    // 后台 free-run / 回前台待对齐 / 用户暂停：时钟不可信或不应干预。
    if (this.pageHidden || this.pendingForegroundAlign || this.awaitingNewTimeline) return;
    const video = this.videoElement;
    if (!video || video.paused || video.seeking) return;
    if (video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) return;
    if (!core.isVideoClockAdvancing()) return;

    const now = performance.now();
    if (now - this.chainStartedAtMs < SILENT_STALL_MIN_UPTIME_MS) return;
    if (now - this.lastAudioProgressMs < SILENT_STALL_MS) return;

    this.silentWatchdogTrips++;
    Log.w(
      TAG,
      `Audio chain stalled ${Math.round(now - this.lastAudioProgressMs)}ms with the video clock advancing ` +
        `(video=${video.currentTime.toFixed(2)}s, trip ${this.silentWatchdogTrips}) → force re-anchor at the playhead`,
    );
    this.lastAudioProgressMs = now;
    // 按当前屏上帧时间重建：内核会丢掉落后于画面的陈旧队列，后续 PCM 到达时
    // 直接锚定到播放头附近。只丢内容、不做任何时间轴平移。
    core.reanchor("silent-stall", true);
  }

  /**
   * 音频整体落在播放头之前（源 PTS 跑在 MSE 视频前面）时的兜底。
   * Lab 没有 this 问题——它在 MSE 层用 waitForBufferRoom 限速，音频不会跑太前面。
   * Player 的 worker 没有 MSE 限速，PCM 一路灌进来 → 被 MAX_SCHEDULE_LEAD_SEC 挡住。
   *
   * 策略：默认不 reanchor，也不 trim 队列。队列里的 chunk 是合法的未来音频，
   * 视频时钟在走就会自然追上来，pump 届时会锚定并消费。
   * 旧实现在这里调 requestReanchor → 清队 → worker 重锚 → confirmVideoAnchor →
   * 又 reanchor……形成"始终重新对齐"的循环。更早的实现在这里调 trimAudioLead，
   * 结果"进来一个丢一个"→ 永远静音。
   *
   * 唯一例外：音频**持续大幅**超前且视频时钟确实在推进（源 PTS 轴整体跳变/漂移，
   * MP2 overlap 自纠与 E-AC-3 4K 都是这个失败模式）时，做**一次轴平移自愈**：
   * 把整条音频轴（队列 + 后续入队标签）前移实测超前量。内容不丢、无接缝、
   * 同步生效 —— 绝不做 worker 绝对钉扎（在途管线深度会让钉扎点落后视频轴数秒，
   * 之后所有 PCM 被当过期丢弃 → 永久静音）。
   */
  private noteSchedulingBlocked(): void {
    const now = performance.now();
    if (this.blockedSince === null) {
      this.blockedSince = now;
      this.blockedVideoClockSec = this.videoElement?.currentTime ?? 0;
      return;
    }
    const blockedMs = now - this.blockedSince;

    this.maybeRebaseAxis(blockedMs, now);

    const hasScheduled = (this.core?.stats().scheduledChunks ?? 0) > 0;
    if (hasScheduled && blockedMs > RESYNC_FAILED_AFTER_MS && !this.reportedUnrecoverable) {
      // 视频时钟停住 = 播放器在缓冲，不是音频"不可达"；等视频恢复推进后 pump 会自然
      // 锚定。只有视频确实在推进而音频仍持续排不出去，才判定会话不可重建。
      const video = this.videoElement;
      const videoAdvanced = (video?.currentTime ?? 0) - this.blockedVideoClockSec >= MIN_VIDEO_ADVANCE_SEC;
      if (!videoAdvanced) {
        return;
      }
      this.reportedUnrecoverable = true;
      Log.e(TAG, `Audio stalled for ${Math.round(blockedMs)}ms behind an unreachable playhead`);
      this.onResyncFailed?.();
    }
  }

  /**
   * 轴平移自愈：音频轴持续超前视频轴 ≥2s（blocked ≥3s）时，把音频轴前移实测超前量。
   * 10s 冷却防噪声；轴平移不依赖 worker 往返，立即生效。若超前持续再生（源持续漂移），
   * 下一轮 blocked 会再次触发，15s 兜底仍在最后把关。
   *
   * 页面隐藏时例外：后台 timer 节流会让视频时钟长时间不走（MSE 供流循环同样被节流），
   * "视频推进"前置条件永远不满足 → 巨大 lead 持续 blocked 却永不自愈 → 后台静音死锁
   * （实测：非标准源 lead=117s，后台 150s 无声，回前台才 rebase）。后台视频时钟不可信，
   * 唯一正确的动作就是把音频轴拉回视频轴附近自由续播（等价 v3.2.1 的后台 free-run）。
   */
  private maybeRebaseAxis(blockedMs: number, now: number): void {
    const core = this.core;
    if (!core || this.awaitingNewTimeline) return;
    // 回前台对齐挂起期间不 rebase：此时偏差大是后台节流导致，正确动作是
    // alignToVideoOnReturn 把 video seek 到 heard（画面追音频）；rebase 抢跑会
    // 让声音倒退重播已听内容，与 seek 对齐打架。
    if (this.pageHidden || this.pendingForegroundAlign) return;
    if (now - this.lastRebaseAt < REBASE_MIN_INTERVAL_MS) return;
    if (blockedMs < SUSTAINED_BLOCK_MS) return;
    const video = this.videoElement;
    if (!video) return;
    if (video.currentTime - this.blockedVideoClockSec < MIN_VIDEO_ADVANCE_SEC) return;
    const nextSec = core.nextAudibleStreamSec();
    if (nextSec === null) return;
    const leadSec = nextSec - core.visibleVideoTime();
    if (leadSec < MIN_AHEAD_SEC_FOR_REBASE) return;

    this.applyRebase(leadSec, `blocked ${Math.round(blockedMs)}ms with video advancing`);
  }

  /**
   * 应用一次轴平移自愈（排程 blocked 级联触发，可能的后台 free-run 兜底）。
   * 记录 10s 冷却（防连续平移）并递增诊断计数。
   */
  private applyRebase(leadSec: number, reason: string): void {
    const core = this.core;
    if (!core) return;
    this.lastRebaseAt = performance.now();
    this.rebaseCount++;
    Log.w(
      TAG,
      `rebase axis (${reason}): audio lead ${leadSec.toFixed(2)}s → shift ${(-leadSec).toFixed(2)}s ` +
        `(rebase ${this.rebaseCount})`,
    );
    core.rebaseAxis(-leadSec);
  }

  private clearSchedulingBlocked(): void {
    this.blockedSince = null;
    this.blockedVideoClockSec = 0;
    this.reportedUnrecoverable = false;
  }

  private onVisibilityChange(): void {
    this.pageHidden = document.visibilityState === "hidden";
    if (this.pageHidden) {
      // 进入后台：恢复被 UA 暂停的无音轨 video，保持 currentTime 推进（音视频同步）。
      this.backgroundResumeAttempted = false;
      this.pendingForegroundAlign = false;
      if (this.videoElement?.paused) {
        this.videoElement.play().catch(() => {});
      }
      return;
    }
    // 回前台自愈：后台切台会新建 AudioContext，而 hidden 下 UA 直接给 suspended；
    // 若当时的 resume() 失败被吞（见 notifyAutoplayBlocked），视频仍在播而音频永远
    // 静音 —— 且没人会再调 play()。回前台时补一次恢复。
    if (this.context && this.context.state !== "running") {
      void this.play();
      return;
    }
    // 后台 free-run 期间音频轴与视频轴已解耦。此处**不对齐也不 reanchor**：
    // visibilitychange 时刻视频时钟通常仍冻结，立即 reanchor 会清掉正在播的音频链
    // （切前台静音），立即 rebase 会让声音倒退重播已听内容。挂起对齐，由
    // controlTick 在时钟恢复推进后执行（alignToVideoOnReturn：视频追音频）。
    this.pendingForegroundAlign = true;
  }

  /**
   * 回前台恢复音视频同步（在视频时钟恢复推进后由 controlTick 调用）。
   *
   * 后台 free-run 期间音频按 ctx 时钟**实时**推进（紧跟直播边缘），而 video 被 UA
   * 节流/冻结（4K HEVC 后台解码受限），回前台时音频轴领先视频轴（实测 1.1~3.7s）。
   * 恢复方向是**视频追音频**：
   *  - 把 video seek 到"正在听到的位置"（heard）——画面跳到后台听到的内容处，
   *    音频立即在 heard 重建排程（不重播、不静音），live-sync 的 latency 随即
   *    恢复正常（不再 1.2x 加速追）；
   *  - 反向 rebase 音频轴只会让声音倒退重播已听内容（实测 rebase −2.9s 后内容错位）。
   */
  private alignToVideoOnReturn(): void {
    const core = this.core;
    if (!core || this.awaitingNewTimeline) return;
    const video = this.videoElement;
    if (!video) return;
    const heard = core.heardStreamTime();
    if (heard === null) {
      // 无正在播的音频链（异常：后台链已断）：按当前视频位置重建即可。
      core.reanchor("visibility-visible", true);
      return;
    }
    const visible = core.visibleVideoTime();
    const leadSec = heard - visible;
    // 小偏差：音频链继续播，pump 链式延续即可自然同步，无需干预。
    if (leadSec < FOREGROUND_ALIGN_MIN_LEAD_SEC) return;

    if (this.isSeekableTarget(video, heard)) {
      // 目标在 MSE 缓冲内：画面跳到正在听到的位置。音频链**立即重建到 heard**
      // （丢弃 free-run 预排的超前残留 + 重锚），而不是保留旧链——seek 处理
      // 延迟期间音频还会继续超前，保留旧链会残留大 drift → 下一 controlTick
      // 又 drift 重锚反复清链（实测 seek 后 513ms → reanchor 静音）。
      Log.w(
        TAG,
        `Foreground return: video at ${visible.toFixed(2)}s, audio heard at ${heard.toFixed(2)}s → seek video forward ${leadSec.toFixed(2)}s`,
      );
      this.aligningSeek = true;
      this.awaitingNewTimeline = true;
      this.reanchorAtSec = heard;
      video.currentTime = heard;
      this.core?.trimFutureBeyond(heard + REANCHOR_STALE_LEAD_SEC);
      this.core?.reanchor("video-anchor", true);
      return;
    }

    // 缓冲未覆盖 heard（极端：缓冲被清理/重建）：退回"音频轴对齐视频轴"兜底，
    // 至少不静音；内容重播是缓冲不足时的次优解。平移量以"即将播出的内容"为准。
    const nextSec = core.nextAudibleStreamSec();
    const rebaseLead = nextSec === null ? leadSec : nextSec - visible;
    this.applyRebase(rebaseLead, "foreground return (no buffer)");
    core.reanchor("visibility-visible", true);
  }

  /** 目标媒体时间是否落在当前 MSE 缓冲内（留 100ms 余量，避免 seek 到边界卡 waiting）。 */
  private isSeekableTarget(video: HTMLVideoElement, targetSec: number): boolean {
    const buffered = video.buffered;
    for (let i = 0; i < buffered.length; i++) {
      if (targetSec >= buffered.start(i) && targetSec <= buffered.end(i) - 0.1) {
        return true;
      }
    }
    return false;
  }

  // ==================== Video events ====================

  private onVideoSeeking(): void {
    // 回前台对齐 seek：**保留 aligningSeek 标志**（onVideoSeeked 还要靠它判断）。
    if (this.aligningSeek) {
      return;
    }
    // seek 期间只停掉已排的链、**不清队列** —— 音频数据保留，等 seeked 后从缓冲里按
    // 新位置续排（v3.2.1 的 resyncFromBuffer 语义）。旧实现在这里 resetChain() 清空
    // 整个队列，是"seek 后 queue=0、静音"的一半原因（另一半是 awaitNewTimeline 丢弃窗）。
    this.core?.stopChain();
    this.driftLogCounter = 0;
  }

  private onVideoSeeked(): void {
    if (this.aligningSeek) {
      // 回前台对齐 seek：音频链正在播 heard 内容，视频跳到 heard，无需重建。
      this.aligningSeek = false;
      return;
    }
    // 按新位置从缓冲重建链：只丢"已被 seek 越过"的前缀，其余全部保留；缓冲未覆盖时
    // 等新喂入的数据即可（自身不会静音）。不再开启 awaitNewTimeline 丢弃窗 ——
    // 旧实现会把 seek 后到达的 PCM 全丢成 queue=0，直到视频时钟重新越过锚点。
    const target = this.core?.visibleVideoTime() ?? 0;
    this.core?.resyncFromBuffer(target);
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

  /** worker 后续 PCM 已重钉到新时间轴：此时队列已在请求处清空，只需在新锚点排程。 */
  confirmVideoAnchor(videoTimeSec: number): void {
    Log.v(TAG, `Audio anchor confirmed at ${videoTimeSec.toFixed(3)}s; rebuilding chain`);
    // worker 刚确认重钉：仍可能有旧时间轴残留 PCM 在途，先按新锚点丢弃，直到收到新样本。
    this.awaitingNewTimeline = true;
    this.reanchorAtSec = videoTimeSec;
    this.core?.reanchor("video-anchor", true);
    // 旧轴已排队 chunk 的标签整体超前 Δ 秒：不裁剪的话调度领先门会继续 blocked。
    // 代价为一次性 ≤Δ 秒静音。
    this.core?.trimFutureBeyond(videoTimeSec + REANCHOR_STALE_LEAD_SEC);
  }

  private notifyAutoplayBlocked(): void {
    // 不能用 playbackUnlocked 挡：后台切台新建的 AudioContext 在 hidden 下 resume
    // 可能失败，而 playbackUnlocked 是页面级"曾经解锁过"的标志 —— 挡掉后这次失败
    // 永远不会上报，表现为"视频在播、声音没了"。这里只做去重（恢复 running 时复位）。
    if (this.autoplaySuspendedNotified) {
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
