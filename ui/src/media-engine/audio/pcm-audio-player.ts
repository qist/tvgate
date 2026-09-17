/**
 * PCM 软解音频播放内核（WebAudio；不依赖 AudioWorklet —— 需兼容非安全上下文）。
 *
 * 分三层协作：
 *   1. `PcmStreamBuffer`（pcm-stream-buffer.ts）——解码 PCM 的流时间轴：排序、裁齐、
 *      按窗口补货、按播放头回收；
 *   2. `WasmTimeStretcher`（wasm-stretcher.ts）+ 本文件的漂移控制环 —— 保音高变速，
 *      让音频跟随 `video.playbackRate`（直播追赶）并吸收音视频两套时钟的微小漂移；
 *   3. `OutputChain`（output-chain.ts）——在 AudioContext 时钟上背靠背排程，sample-accurate
 *      无缝输出；块的流时间戳只用于把「正在播的内容时间」映射回媒体时间轴，
 *      **不参与定起点**（video.currentTime 的抖动会造成可闻的咔哒声）。
 *
 * 时钟可信度由 `syncState` 管理：
 *   - active：前台且视频时钟可信，跑完整漂移控制与硬重同步；
 *   - background：页面隐藏，视频时钟冻结 —— 音频按 AudioContext 时钟 free-run，不纠偏；
 *   - recovering：回前台/媒体管线重建中 —— 等「时钟复活」的证据（timeupdate / seeked /
 *     可播状态）后做一次确定性锚定。
 *
 * 自愈兜底（正常情况下都不该触发）：
 *   - 静音看门狗（STALL_WATCHDOG_MS）：曾出过声、随后长时间没有可听内容，而视频时钟确实
 *     在推进 → 按播放头强制重建排程链（断流重连后「画面在走、声音不回来」的兜底）；
 *   - 视频 waiting 宽限（STALL_GRACE_MS）：短暂视频卡顿不掐音频，超时才挂起；
 *   - 恢复超时（RECOVERY_GRACE_MS）：回前台后证据事件一直不来 → 上报 onResyncFailed。
 */

import { builtinWasmDecoders } from "../decoder/builtin-wasm";
import { type TimeStretcher, WasmTimeStretcher } from "./wasm-stretcher";
import type { PcmWorkerStats } from "../backends/types";
import { detectAudioOutputChannels, downmixInterleaved } from "./output-channels";
import { OutputChain } from "./output-chain";
import { type BufferedPcm, PcmStreamBuffer, PCM_GAP_SNAP_SEC, pcmDurationSec } from "./pcm-stream-buffer";

const TAG = "PCMAudioPlayer";

/** 轻量日志：verbose/info/warn 默认静默（避免诊断 IO 干扰主线程），仅 error 直通控制台。 */
const Log: {
  v: (message: string) => void;
  i: (message: string) => void;
  w: (message: string) => void;
  e: (message: string) => void;
} = {
  v: () => {},
  i: () => {},
  w: () => {},
  e: (message) => {
    // eslint-disable-next-line no-console
    console.error(`[${TAG}] ${message}`);
  },
};

/** MediaEngine 使用方的配置。 */
export interface PcmPlayerConfig {
  /** 当前视频时间（MSE 时间轴，秒）；attachVideo 后以 videoElement 为准。 */
  clock: () => number;
  /** WSOLA 拉伸 wasm 地址（缺省用内置 avcodec_audio.wasm）。 */
  wasmUrl?: string;
  /** 缓冲回收：相对播放位置允许保留的落后量（秒）。 */
  bufferCleanupMaxBackward?: number;
  bufferCleanupMinBackward?: number;
  /** 声道输出模式：stereo（默认，原样透传）/ mono（左右合成 (L+R)/2，对多声道同样生效）。 */
  audioChannelMode?: "stereo" | "mono";
  /** 设备可用输出声道数（缺省自动探测；≥6 时 5.1 直通，否则降混到 2.0）。 */
  outputChannels?: number;
}

/** 软解 PCM 块（与 media-engine worker 的 onPCMAudioData 对齐）。 */
export interface PCMAudioChunk {
  /** 每通道一条 Float32 PCM。 */
  pcm: Float32Array[];
  sampleRate: number;
  /** 该块起始的 MSE 媒体时间（秒）。 */
  time: number;
}

// ==================== 排程/时钟参数 ====================

/** 已排程音频允许领先 AudioContext 时钟的量（秒）。它同时是「变速生效延迟」的上界：
 *  直播追赶期间速度变化必须在这个窗口内就听出来，故不能大。 */
const SCHEDULE_AHEAD = 0.8;
/** 页面隐藏时的领先量：后台定时器被节流（移动端 1s、桌面最长 1 分钟），
 *  小窗口会立刻 underrun，故后台放宽。 */
const BACKGROUND_SCHEDULE_AHEAD = 6.0;
/** 漂移超过它即视为不可修复的不连续，直接从缓冲重建链。 */
const HARD_RESYNC_THRESHOLD = 1.5;
/** 链重启时首块的淡入时长（秒），消除拼接处的爆音。 */
const FADE_SEC = 0.005;
/** 判定「视频时钟仍在推进」的静默窗口（毫秒）。本引擎按 CONTROL_INTERVAL_MS 采样，
 *  窗口必须大于采样间隔，否则正常推进的时钟会被误判为停住。 */
const CLOCK_STALE_MS = 600;
/** 控制环周期（毫秒）。 */
const CONTROL_INTERVAL_MS = 250;

// ==================== 漂移控制参数 ====================

/** 稳态比例增益：漂移 → 拉伸比修正。 */
const RATIO_DRIFT_GAIN = 0.5;
/** 稳态最大修正量（WSOLA 保音高，10% 的临时速度偏差不可闻）。 */
const RATIO_DRIFT_MAX = 0.1;
/** 直达旁路迟滞：进入/退出阈值（ratio=1 时 WSOLA 走零失真直通）。
 *  进入取半量、退出取全量，避免在边界来回抖动。 */
const BYPASS_ENTER_DRIFT = 0.01;
const BYPASS_EXIT_DRIFT = 0.02;
/** 软同步窗口：起播/重锚后一段时间内允许更强修正，尽快收敛。 */
const SOFT_SYNC_WINDOW_SEC = 3.0;
const SOFT_SYNC_EXIT_DRIFT = 0.08;
const SOFT_SYNC_DRIFT_GAIN = 1.0;
const SOFT_SYNC_LIMIT = 0.35;
/** 漂移测量的 EMA 系数。 */
const DRIFT_EMA_ALPHA = 0.4;
/** 回前台对齐的最小领先量（秒）：低于它链式延续即可自洽，不干预。 */
const FOREGROUND_ALIGN_MIN_LEAD_SEC = 0.5;
/** 诊断日志间隔（控制 tick 数；250ms × 240 ≈ 60s）。 */
const DRIFT_LOG_TICKS = 240;

// ==================== 自愈/缓冲参数 ====================

/**
 * 静音看门狗（音频链停摆自愈）：软解音频曾出过声、随后超过该时长再也没有可听内容，而视频
 * 时钟确实在推进（媒体可播）→ 判定音频链已停摆，按当前播放头强制重建排程链。
 * 为什么需要：排程门与陈旧水位都假设"队列里还有合法未来音频"；断流重连后视频轴前跳、PCM
 * 缺口等场景会让新 PCM 迟迟进不到播放头附近，于是既不排程、也没人纠正 —— 表现为"画面在走、
 * 声音永远不回来"。这是兜底网，正常情况下永不触发。
 */
const SILENT_STALL_MS = 5000;
/** 看门狗最少观察窗口：排程链本身有前瞻，且允许一次正常 underrun，避免误判。 */
const SILENT_STALL_MIN_UPTIME_MS = 3000;
/** 待排程队列上限（防垃圾流无界增长）。 */
const PENDING_LIMIT = 1200;
/** 重锚后一次性灌入排程队列的时长窗口（秒）。 */
const REFILL_WINDOW_SEC = 4.0;
/** 流时间轴缓冲的默认回收参数（可由 PcmPlayerConfig 覆盖）。 */
const DEFAULT_KEEP_BEHIND_SEC = 8;
const DEFAULT_MAX_BACKWARD_SEC = 30;
/** recovering 状态下必须在它之内完成锚定，否则升级 onResyncFailed。 */
const RECOVERY_TIMEOUT_MS = 4000;
/** 视频 waiting/stalled 宽限（毫秒）：宽限内音频照播（视频小卡顿不该掐声音），
 *  超时仍不可播才挂起排程、等恢复事件。 */
const BUFFERING_HOLD_GRACE_MS = 2000;

/** 播放速率与拉伸比共用的允许区间（Chromium 只接受 [1/16, 16]，这里按业务需要收窄）。 */
const RATE_MIN = 0.5;
const RATE_MAX = 2;

/** 数值夹取。 */
function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

/**
 * 由生命周期驱动的同步状态。漂移纠偏与硬重同步**只在 active 下运行**：
 * 页面隐藏或媒体管线重建期间，video.currentTime 不是可信时钟，拿它纠偏会把音频
 * 拖回去重播/跳过（iOS 上"切后台再回来、视频时钟冻结，纠偏环却在 1.5s 阈值上
 * 反复硬重同步"就是这个坑）。
 *
 *  - active     ：前台，视频时钟可信，跑完整漂移控制；
 *  - background ：页面隐藏，音频 free-run，不做任何纠偏；
 *  - recovering ：等待"时钟复活"的证据（可见状态下的 timeupdate / seeked / 可播状态），
 *                 拿到证据后做一次确定性锚定。
 */
type SyncState = "active" | "background" | "recovering";
type ControlOutcome = "skipped" | "updated" | "pumped";

export class PCMAudioPlayer {
  private config: PcmPlayerConfig;
  private audioCtx: AudioContext | null = null;
  private gain: GainNode | null = null;
  /** 排程链（AudioContext 时钟上的背靠背输出，见 output-chain.ts）。 */
  private chain: OutputChain | null = null;
  private volume: number = 1.0;
  private muted: boolean = false;

  private video: HTMLVideoElement | null = null;

  /** 已解码 PCM 的流时间轴（seek / 重锚的唯一数据来源）。 */
  private streamBuffer = new PcmStreamBuffer();
  /** 待送进拉伸器的片段队列（按时间有序）。 */
  private pending: BufferedPcm[] = [];

  // Time stretcher
  private stretcher: TimeStretcher | null = null;
  private stretcherLoading = false;
  private stretcherFailed = false;

  /** 拉伸器输入侧的流时间游标：下一帧待处理的媒体时间；null = 尚未锚定。 */
  private feedCursorSec: number | null = null;
  /** 拉伸器输入位置 0 对应的流时间。 */
  private stretcherBase = 0;
  /** 已排程输出的流时间终点（映射输出的另一端点）。 */
  private outputStreamCursor = 0;

  // Drift control
  private driftEma = 0;
  private driftEmaPrimed = false;
  private softSyncUntil = 0;
  private bypassActive = false;
  private diagTicks = 0;
  /** 静音看门狗：链首次成功排程时刻、最近一次音频内容推进（时刻/游标）与触发次数。 */
  private chainStartedAtMs: number | null = null;
  private lastAudioProgressMs = 0;
  private lastAudioProgressSec = Number.NaN;
  private stallTrips = 0;
  /** worker 侧软解丢弃计数最新快照 + 上一诊断窗口快照（打增量定位"卡顿一下"来自哪层）。 */
  private workerStats: PcmWorkerStats | null = null;
  private prevWorkerStats: PcmWorkerStats | null = null;
  private tickTimer: ReturnType<typeof setInterval> | null = null;


  private buffering = false;
  private seeking = false;
  /** 回前台对齐挂起：visibilitychange 时刻视频时钟往往仍冻结，推迟到时钟恢复推进后执行。 */
  private pendingForegroundAlign = false;
  /** 回前台对齐主动发起的 seek：seeking/seeked 事件不得再清链/重锚（音频正在播 heard 内容）。 */
  private aligningSeek = false;

  // 起播对齐没有独立的启动门：首个 PCM 块入队时按自身时间锚定
  // 调度链，随后由漂移环/WSOLA 收敛；解码远快于实时，PCM 很快追上视频时钟。
  // （原自研启动同步机已退役，见 docs/player-latest-port-design.md C7。）

  /** 进入后台后是否已尝试恢复被 UA 暂停的 video（只对抗一次，避免 play-pause 循环）。 */
  private backgroundResumeAttempted = false;
  /** 上一次控制 tick 采到的视频时钟位置与变化时刻（判定时钟是否仍在推进）。 */
  private lastVideoClockSec = 0;
  private lastClockChangeAtMs = Number.NEGATIVE_INFINITY;

  // Lifecycle-driven sync state (see SyncState docs)
  private clockState: SyncState = "active";
  private recoveryTimeout: ReturnType<typeof setTimeout> | null = null;


  // Video stall grace: `waiting`/`stalled` does NOT pause PCM scheduling
  // immediately — the chain keeps playing through the grace window so a brief
  // video hiccup doesn't mute the sound or click the chain. Only after the
  // grace expires with the video still stuck do we hold (cancel the chain).
  private bufferingHoldTimer: ReturnType<typeof setTimeout> | null = null;
  private stallGraceActive = false;

  /** 事件订阅表：attachVideo 登记的监听都在这里，detachVideo 一次性摘除。 */
  private subscriptions: Array<{ target: EventTarget; type: string; handler: EventListener }> = [];

  /** AudioContext 被自动播放策略拦住（需要用户交互）时通知上层。 */
  onSuspended: (() => void) | null = null;

  /** 软解 PCM 全链路丢弃计数（诊断用，便于定位静音/丢帧）。 */
  onAudioStats: ((stats: PcmWorkerStats) => void) | null = null;

  /** 回后台/系统中断后无法完成重锚（时钟再没回来，或目标已不在时间轴内）时通知上层：
   *  需要重建整条流。 */
  onResyncFailed: (() => void) | null = null;

  /**
   * 自动播放被拦截的通知去重（一次性）。不要退回"页面级 playbackUnlocked 门"：
   * 那种标志在首次解锁后恒为 true，反而会让"切回前台时 AudioContext 仍 suspended"
   * 的情况永远得不到补救。AudioContext 回到 running 时复位。
   */
  private autoplaySuspendedNotified = false;

  /** 主线程诊断计数：AudioContext 未就绪时 feed 丢弃的块（对应 PcmWorkerStats.pendingOverflowDrops）。 */
  private pendingOverflowDrops = 0;

  /** 声道输出模式：mono = 左右声道合成 (L+R)/2（见 PcmPlayerConfig.audioChannelMode）。 */
  private channelMode: "stereo" | "mono";
  /** 设备可用输出声道数（见 output-channels.ts）。 */
  private readonly outputChannels: number;

  constructor(config: PcmPlayerConfig) {
    this.config = config;
    this.channelMode = config.audioChannelMode ?? "stereo";
    this.outputChannels = config.outputChannels ?? detectAudioOutputChannels();
  }

  async init(): Promise<void> {
    if (this.audioCtx) {
      return;
    }

    const ctx = new AudioContext();
    this.audioCtx = ctx;
    this.gain = ctx.createGain();
    this.gain.connect(ctx.destination);
    this.chain = new OutputChain(ctx, this.gain);
    this.applyGain();

    ctx.onstatechange = () => {
      const state = ctx.state as string | undefined;
      Log.v(`AudioContext 状态：${state}`);

      if (state === "interrupted") {
      // WebKit-only: the OS revoked the audio session (backgrounding, but
      // also e.g. an incoming call while the tab stays visible — no
      // visibilitychange in that case). Unambiguous background signal on
      // its own, independent of visibilitychange ordering.
      // 页面隐藏时不丢链不丢队列（后台系统级 suspend 恢复后继续 free-run），
      // 否则切后台立即静音；前台 interrupt 才清链，避免恢复时喷陈旧音频。
      if (document.visibilityState !== "hidden") {
        this.cancelChain();
        this.pending = [];
        this.feedCursorSec = null;
        this.resetDriftState();
      }
      this.setSyncState("background");
      return;
      }

      if (state !== "running") {
        // "suspended": either the pre-first-activation autoplay gate or our
        // own deliberate suspend() in pause() — neither implies backgrounding,
        // so syncState is left untouched. Drop the chain so resume doesn't
        // burst out stale audio. 后台系统级 suspend 例外：保留链与队列继续 free-run。
        if (document.visibilityState !== "hidden") {
          this.cancelChain();
          this.pending = [];
          this.feedCursorSec = null;
          this.resetDriftState();
        }
        return;
      }

      // state === "running"
      this.autoplaySuspendedNotified = false;
      if (this.clockState === "background") {
        // Confirmed a real background period happened (via "interrupted"
        // above, and/or visibilitychange->hidden already set this). Only
        // anchor once the page is visible too — video.currentTime is not
        // proven yet while the media pipeline may still be rebuilding.
        this.setSyncState(document.visibilityState === "hidden" ? "background" : "recovering");
        this.pump();
      } else if (this.canScheduleAudio()) {
        // First activation (autoplay gate lifting) or resume from our own
        // pause() — the video clock was never untrusted; anchor immediately.
        this.resyncFromBuffer(this.video?.currentTime ?? 0);
      }
    };

    Log.v(`AudioContext initialized, sampleRate: ${this.audioCtx.sampleRate}, state: ${this.audioCtx.state}`);
  }

  attachVideo(video: HTMLVideoElement): void {
    this.video = video;
    this.setVolume(video.volume);
    this.setMuted(video.muted);

    // 视频事件全部登记在订阅表里，detachVideo 一次摘干净（不会漏事件/漏摘监听）。
    this.listen(video, "seeking", () => this.onVideoSeeking());
    this.listen(video, "seeked", () => this.onVideoSeeked());
    this.listen(video, "play", () => void this.play());
    this.listen(video, "pause", () => this.onVideoPause());
    this.listen(video, "volumechange", () => {
      this.setVolume(video.volume);
      this.setMuted(video.muted);
    });
    this.listen(video, "timeupdate", () => this.onVideoTimeUpdate());
    this.listen(video, "ratechange", () => this.onVideoRateChange());
    this.listen(video, "waiting", () => this.enterBuffering("waiting"));
    this.listen(video, "stalled", () => {
      if (video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) {
        this.enterBuffering("stalled");
      }
    });
    this.listen(video, "playing", () => this.maybeExitBuffering());
    this.listen(video, "canplay", () => this.maybeExitBuffering());

    this.listen(document, "visibilitychange", () => this.onVisibilityChange());
    if (document.visibilityState === "hidden") {
      this.setSyncState("background");
    }

    // 控制环：漂移纠偏 + 补泵 + 静音看门狗，250ms 一跳。
    this.tickTimer = setInterval(() => this.controlAndPump(), CONTROL_INTERVAL_MS);
  }

  detachVideo(): void {
    if (this.tickTimer) {
      clearInterval(this.tickTimer);
      this.tickTimer = null;
    }
    if (this.recoveryTimeout) {
      clearTimeout(this.recoveryTimeout);
      this.recoveryTimeout = null;
    }
    this.clearStallGrace();
    this.unlistenAll();
    this.video = null;
  }

  /** 登记事件监听（与 unlistenAll 成对，避免 detach 时漏摘）。 */
  private listen(target: EventTarget, type: string, handler: EventListener): void {
    target.addEventListener(type, handler);
    this.subscriptions.push({ target, type, handler });
  }

  private unlistenAll(): void {
    for (const { target, type, handler } of this.subscriptions) {
      target.removeEventListener(type, handler);
    }
    this.subscriptions = [];
  }

  /**
   * video 的 pause 事件。页面隐藏时 UA 会暂停"无音轨"（软解）video —— 这不是用户暂停：
   * 既不能挂起 AudioContext（否则切后台立即静音），也不能清链（后台要 free-run，
   * 丢链只能等新 PCM 重灌）。顺带把被暂停的 video 拉回来一次，让 currentTime 继续推进、
   * 音视频不脱轴；只对抗一次，避免与 UA 策略形成 play/pause 循环。前台 pause 才走完整 pause()。
   */
  private onVideoPause(): void {
    if (document.visibilityState === "hidden") {
      if (!this.backgroundResumeAttempted && this.video) {
        this.backgroundResumeAttempted = true;
        void this.video.play().catch(() => {});
      }
      return;
    }
    this.pause();
  }

  /** timeupdate 只在 currentTime 真的前进时触发 —— 可见状态下它就是"管线复活"的证据。 */
  private onVideoTimeUpdate(): void {
    if (this.clockState === "recovering" && document.visibilityState !== "hidden") {
      this.completeRecovery("timeupdate");
      return;
    }
    this.controlAndPump();
  }

  /** ratechange：立刻把新速率推给拉伸器，别等漂移环慢慢积起来。 */
  private onVideoRateChange(): void {
    // 管线里已按旧速率排好的那部分由漂移环吸收，因此 1 ↔ 1.2 这类直播追赶切换
    // 不会造成可闻中断（ratechange 时若当前不适用纠偏，则保持不 pump 的旧语义）。
    this.controlAndPump(false);
  }

  /**
   * 运行时切换声道输出模式（stereo/mono），立即作用于后续 feed 的 PCM。
   * mono 采用 (L+R)/2 合成：分离声道源（如 L=对白 R=音乐）在手机端只输出单边时，
   * 合成后两边内容都能听到；输出为 1ch，下游内核与 WSOLA 拉伸均支持。
   */
  setAudioChannelMode(mode: "stereo" | "mono"): void {
    if (this.channelMode === mode) {
      return;
    }
    this.channelMode = mode;
    Log.i(`audio channel mode -> ${mode}`);
  }

  /** 喂入交错 PCM；`time` 为该块起始的媒体时间（MSE 时间轴，与 video.currentTime 同域）。 */
  feed(samples: Float32Array, channels: number, sampleRate: number, time: number): void {
    if (!this.audioCtx || !this.gain) {
      this.pendingOverflowDrops++;
      Log.w("AudioContext 尚未就绪，丢弃音频块");
      return;
    }

    // 目标声道数：mono 档位 → 1；否则取设备能力（≥6 直通 5.1，否则 2.0）。
    // 降混放在进缓冲 / WSOLA 之前：下游内核、拉伸器与 AudioBuffer 都只按目标声道工作
    // （设备只能 2.0 时 WASM 已按 2 声道解码，这里是兜底——上游仍送来多声道 PCM 时也能收口）。
    const targetChannels = this.channelMode === "mono" ? 1 : this.outputChannels;
    if (channels > targetChannels) {
      const mixed = downmixInterleaved(samples, channels, targetChannels);
      samples = mixed.samples;
      channels = mixed.channels;
    }

    const frames = Math.floor(samples.length / channels);
    if (frames <= 0) {
      return;
    }
    this.streamBuffer.insert({
      samples,
      channels,
      sampleRate,
      startSec: time,
      endSec: time + frames / sampleRate,
    });
    this.recycleStreamBuffer();

    if (this.canScheduleAudio()) {
      this.pump();
    }
  }

  /**
   * 接收 worker 上报的软解丢弃计数，与本地主线程计数合并后上抛 onAudioStats。
   * worker 不计 pendingOverflowDrops（主线程侧），此处用本地值覆盖。
   */
  setPipelineStats(workerStats: PcmWorkerStats): void {
    this.workerStats = workerStats;
    this.onAudioStats?.({
      ...workerStats,
      pendingOverflowDrops: this.pendingOverflowDrops,
    });
  }

  // ==================== Stretcher ====================

  /** 返回可用于该块的拉伸器；格式不符/尚未就绪时负责（重建）加载，并在就绪后继续 pump。 */
  private ensureStretcher(chunk: BufferedPcm): TimeStretcher | null {
    if (this.stretcher) {
      if (this.stretcher.sampleRate === chunk.sampleRate && this.stretcher.channels === chunk.channels) {
        return this.stretcher;
      }
      // 采样率/声道数变了：重建拉伸器并重新锚定（旧格式的排程链已无意义）。
      Log.v(`音频格式变化：${chunk.sampleRate}Hz/${chunk.channels}ch，重建拉伸器`);
      this.stretcher.destroy();
      this.stretcher = null;
      this.cancelChain(true);
      this.feedCursorSec = null;
    }

    if (this.stretcherFailed || this.stretcherLoading) {
      return null; // 已在加载中或已判定不可用：等结果，不要重复发起
    }
    this.stretcherLoading = true;

    // 拉伸器与解码器共用同一个 wasm（wsola_* 编在里面）：优先用配置的地址，
    // 否则退到内置软解 wasm（AC-3 场景下可能只有 ac3 那份）。
    const wasmUrl = this.config.wasmUrl ?? builtinWasmDecoders.mp2 ?? builtinWasmDecoders.ac3;
    void this.loadStretcher(wasmUrl, chunk.sampleRate, chunk.channels);
    return null;
  }

  /** 异步加载拉伸器；就绪后继续 pump，失败则标记不可用并清空队列。 */
  private async loadStretcher(wasmUrl: string | undefined, sampleRate: number, channels: number): Promise<void> {
    try {
      if (!wasmUrl) {
        throw new Error("未配置解码 wasm 地址");
      }
      const stretcher = await WasmTimeStretcher.create(wasmUrl, sampleRate, channels);
      this.stretcherLoading = false;
      if (!this.audioCtx) {
        // 加载期间播放器已被销毁/重置：直接丢弃，别把实例挂上去。
        stretcher.destroy();
        return;
      }
      this.stretcher = stretcher;
      this.pump();
    } catch (error) {
      this.stretcherLoading = false;
      this.stretcherFailed = true;
      this.pending = [];
      Log.e(`WSOLA 拉伸器不可用，软解音频无法播放：${error}`);
    }
  }

  // ==================== 输入泵 ====================

  /**
   * 把待排程队列送过拉伸器，再把产出背靠背排进 AudioContext 时钟。
   * 节奏由两层控制：① 已排程时长超过 schedule-ahead 窗口就停手；
   * ② 队列空了就从流时间轴按窗口补货。
   */
  private pump(): void {
    const ctx = this.audioCtx;
    const chain = this.chain;
    if (!ctx || !chain || !this.gain || !this.canScheduleAudio()) {
      return;
    }

    if (ctx.state !== "running") {
      // "suspended" 可能只是自动播放策略（首次激活前）；WebKit 的 "interrupted"（iOS 后台）
      // 也落在这里：系统结束中断前 resume() 是空操作，期间不得在冻结的时钟上排程。
      void ctx
        .resume()
        .then(() => {
          if ((ctx.state as string) === "running") {
            this.autoplaySuspendedNotified = false;
            this.pump();
          } else {
            this.notifyAutoplayBlocked();
          }
        })
        .catch(() => this.notifyAutoplayBlocked());
      return;
    }

    // 前瞻窗口：已排程时长超过它就停手（后台放宽，见 BACKGROUND_SCHEDULE_AHEAD）。
    const scheduleAhead = this.clockState === "background" ? BACKGROUND_SCHEDULE_AHEAD : SCHEDULE_AHEAD;

    for (;;) {
      if (this.pending.length === 0) {
        // RECOVERING 且链为空：唯一可用的补货基准是 video.currentTime —— 正是我们还不信任的
        // 那个时钟；等 completeRecovery() 做确定性锚定后再继续。
        if (this.clockState === "recovering" && this.feedCursorSec === null) {
          break;
        }
        this.refillPending(this.feedCursorSec ?? this.video?.currentTime ?? 0);
      }
      if (this.pending.length === 0) {
        break; // 时间轴里也没有更多可用数据
      }
      if (chain.scheduledEndSec - ctx.currentTime >= scheduleAhead) {
        break; // 前瞻窗口已满
      }

      const chunk = this.pending[0];
      const stretcher = this.ensureStretcher(chunk);
      if (!stretcher) {
        break; // 拉伸器加载中：数据留在队列里等就绪
      }

      if (this.feedCursorSec === null) {
        this.anchor(chunk.startSec);
      }
      const cursor = this.feedCursorSec as number;
      const delta = chunk.startSec - cursor;

      let chunkEndSec = chunk.endSec;
      if (delta > PCM_GAP_SNAP_SEC) {
        // 队列里出现预期外的空隙（时间轴本已吸附过，走到这里说明有数据被丢掉）：
        // 以游标为准按块时长顺延，绝不把空隙当静音塞进排程。
        Log.w(`待排程队列出现 ${delta.toFixed(3)}s 空隙，按游标接续`);
        chunkEndSec = cursor + pcmDurationSec(chunk);
      }

      let samples = chunk.samples;
      if (delta < -PCM_GAP_SNAP_SEC) {
        // 重叠：裁掉已被游标覆盖的头部。
        const cutFrames = Math.round((cursor - chunk.startSec) * chunk.sampleRate);
        const totalFrames = Math.floor(samples.length / chunk.channels);
        if (cutFrames >= totalFrames) {
          this.pending.shift();
          continue;
        }
        samples = samples.subarray(cutFrames * chunk.channels);
      }

      this.pending.shift();
      this.feedStretcher(stretcher, samples, chunk.sampleRate);
      this.feedCursorSec = chunkEndSec;
    }
  }

  /** 记录视频时钟推进（供 isVideoClockAdvancing 判定；每控制 tick 采一次）。 */
  private trackVideoClock(): void {
    const video = this.video;
    if (!video) return;
    const t = video.currentTime;
    if (Math.abs(t - this.lastVideoClockSec) > 0.0001) {
      this.lastVideoClockSec = t;
      this.lastClockChangeAtMs = performance.now();
    }
  }

  /** 视频时钟是否最近仍在推进（漂移纠偏/自愈的前置条件：绝不把音频对到冻结的时钟上）。 */
  isVideoClockAdvancing(): boolean {
    return performance.now() - this.lastClockChangeAtMs < CLOCK_STALE_MS;
  }

  /** 已排程音频当前正在播放的内容时间（秒）；链空闲/未起播时 null。 */
  heardStreamTime(): number | null {
    return this.audioStreamTimeNow();
  }

  /** 音频内容游标：下一个会被听到的内容时间（秒）；无内容时 null（停摆判据用）。 */
  contentCursorSec(): number | null {
    const scheduledEnd = this.chain?.lastStreamEndSec ?? null;
    if (scheduledEnd !== null) {
      return scheduledEnd;
    }
    if (this.feedCursorSec !== null) {
      return this.feedCursorSec;
    }
    const head = this.pending[0] ?? this.streamBuffer.first();
    return head ? head.startSec : null;
  }

  /** 下一个将被排程（被听到）的内容时间（秒）。本架构无独立"已产出未排程"队列，
   *  与 contentCursorSec 同源；保留该命名以保持轴平移测量点语义。 */
  nextAudibleStreamSec(): number | null {
    return this.contentCursorSec();
  }

  private notifyAutoplayBlocked(): void {
    if (this.autoplaySuspendedNotified) {
      return;
    }
    this.autoplaySuspendedNotified = true;
    Log.w("AudioContext 被自动播放策略拦截，等待用户交互");
    this.onSuspended?.();
    this.video?.pause();
  }

  // ==================== 同步状态机 ====================

  /** 切换同步状态；进入 recovering 时挂一个"锚定超时"看门狗。 */
  private setSyncState(next: SyncState): void {
    if (this.clockState === next) {
      return;
    }
    Log.v(`同步状态：${this.clockState} → ${next}`);
    this.clockState = next;

    // 任何状态切换先撤掉上一个状态的看门狗，避免跨状态误触发。
    if (this.recoveryTimeout) {
      clearTimeout(this.recoveryTimeout);
      this.recoveryTimeout = null;
    }
    if (next !== "recovering") {
      return;
    }
    // 失败出口：媒体管线在后台彻底死掉时（解码器重建失败、缓冲被清），锚定事件永远
    // 不会到来 —— 一次性定时器只属于 recovering 这一次停留，切出即撤。
    this.recoveryTimeout = setTimeout(() => {
      this.recoveryTimeout = null;
      this.onRecoveryTimeout();
    }, RECOVERY_TIMEOUT_MS);
  }

  private onVisibilityChange(): void {
    if (document.visibilityState === "hidden") {
      // 视频解码即将被挂起，它的时钟不再能作为同步参考：音频改为 free-run
      // （系统级播放仍在推进 AudioContext）。顺带把可能被 UA 暂停的 video 拉回来。
      this.backgroundResumeAttempted = false;
      const video = this.video;
      if (video && video.paused) {
        void video.play().catch(() => {});
      }
      this.setSyncState("background");
      return;
    }

    // 回前台：媒体管线可能正在重建，先不锚定 —— 挂起对齐，等时钟恢复推进后由控制
    // tick 执行一次（visibilitychange 时刻视频时钟通常仍是冻结的）。
    this.pendingForegroundAlign = true;
    this.setSyncState("recovering");

    // 自愈：AudioContext 若仍被自动播放策略挂起，补一次 play() 解冻（不要退回页面级
    // "已解锁"标志——那种标志首次解锁后恒为真，会让这里不再补救 → 视频在播却没声音）。
    const ctx = this.audioCtx;
    if (ctx && ctx.state !== "running") {
      void this.play();
    }

    // 若视频已经在前台正常播放（有可播数据），直接锚定而不空等 timeupdate：
    // 切回前台不等于重新起播，底层管线是完好的，空等只会推迟恢复。
    const video = this.video;
    if (video && !video.paused && video.readyState >= HTMLMediaElement.HAVE_FUTURE_DATA && this.canScheduleAudio()) {
      this.completeRecovery("visible");
    }
  }

  /** 恢复锚定点：video.currentTime 重新可信（由 timeupdate / seeked / 可播状态触发）。 */
  private completeRecovery(reason: string): void {
    const video = this.video;
    this.setSyncState("active");
    if (!video) {
      return;
    }

    // 回前台对齐：后台 free-run 期间音频按 AudioContext 时钟实时推进，video 被 UA
    // 节流/冻结 → 回前台时音频领先视频。恢复方向是**视频追音频**（画面跳到正在听到的
    // 位置），而不是把音频拖回视频。时钟仍冻结时不锚定（挂起标志保留），以免把音频
    // 钉在过时位置。
    if (this.pendingForegroundAlign) {
      if (!this.isVideoClockAdvancing()) {
        return;
      }
      this.alignToVideoOnReturn();
      return;
    }

    // 链在后台存活下来（如 Android：AudioContext 不冻结）且偏差已经很小：
    // 保留这条链、让漂移环继续收敛即可，不必每次切回前台都做一次可闻的重建。
    const audioTime = this.audioStreamTimeNow();
    if (audioTime !== null && Math.abs(audioTime - video.currentTime) < HARD_RESYNC_THRESHOLD) {
      Log.v(`恢复（${reason}）：链完好，漂移 ${(audioTime - video.currentTime).toFixed(3)}s`);
      this.resetDriftState();
      this.softSyncUntil = (this.audioCtx?.currentTime ?? 0) + SOFT_SYNC_WINDOW_SEC;
      return;
    }

    Log.v(`恢复锚定（${reason}）于 ${video.currentTime.toFixed(3)}s`);
    // 重新进入软同步窗口：让残余漂移以 ±35% 的上限快速收敛，而不是卡在稳态 ±10%
    // 磨很多秒。
    this.softSyncUntil = (this.audioCtx?.currentTime ?? 0) + SOFT_SYNC_WINDOW_SEC;
    const anchored = this.resyncFromBuffer(video.currentTime);
    if (!anchored && !this.streamBuffer.empty) {
      // 时间轴里还有数据但已不覆盖视频位置：两侧偏离到无法修复（例如长时间后台）。
      // 需要上层重建整条流。
      Log.w("恢复目标已不在时间轴内，升级重建");
      this.onResyncFailed?.();
    }
    // 时间轴为空：只是数据还没到，pump 会在首个块到达时锚定，不算失败。
  }

  /**
   * 回前台恢复音视频同步（**视频追音频**）。
   *
   * 后台 free-run 期间音频按 AudioContext 时钟实时推进（紧跟直播边缘），video 被 UA
   * 节流/冻结，回前台时音频轴领先视频轴。恢复方向是把**画面跳到正在听到的位置**（heard）：
   * 音频立即在 heard 重建排程（不重播、不静音），live-sync 的延迟随即恢复正常；
   * 反向把音频拖回视频只会让声音倒退重播已听内容。
   *
   * 目标不在 MSE 缓冲内（缓冲被清理/重建）时退回"音频轴对齐视频轴"兜底——至少不静音。
   */
  private alignToVideoOnReturn(): void {
    const video = this.video;
    this.pendingForegroundAlign = false;
    if (!video) return;

    const heard = this.heardStreamTime();
    if (heard === null) {
      // 无正在播的音频链（异常：后台链已断）：按当前视频位置重建即可。
      this.resyncFromBuffer(video.currentTime);
      return;
    }

    const leadSec = heard - video.currentTime;
    if (leadSec < FOREGROUND_ALIGN_MIN_LEAD_SEC) {
      // 偏差很小：链式延续即可自然同步，无需干预。
      return;
    }

    if (!this.isSeekableTarget(video, heard)) {
      Log.w(
        `回前台：正在听到的 ${heard.toFixed(2)}s 不在 MSE 缓冲内 ` +
          `(video ${video.currentTime.toFixed(2)}s)，退回音频重锚`,
      );
      this.resyncFromBuffer(video.currentTime);
      return;
    }

    Log.w(
      `回前台：video 在 ${video.currentTime.toFixed(2)}s，音频已听到 ${heard.toFixed(2)}s ` +
        `→ 视频前跳 ${leadSec.toFixed(2)}s`,
    );
    // 主动 seek 期间抑制常规 seeking/seeked 处理；seeked 后在 heard 位置重建音频链。
    this.aligningSeek = true;
    this.seeking = true;
    video.currentTime = heard;
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

  private onRecoveryTimeout(): void {
    const video = this.video;
    if (!video || video.paused || video.seeking || this.seeking || document.visibilityState === "hidden") {
      // 暂停 / seek / 后台状态下本来就不会有 timeupdate —— 等它们的 play/seeked 事件来锚定。
      return;
    }
    if (!this.hasPlayableVideoData()) {
      // 视频还没有可播数据（重负载频道起播慢 / 软反交错 / 缓冲重建中）：这是"数据没来"
      // 而不是"时钟死了"。继续等 completeRecovery 的证明事件（timeupdate/seeked），
      // 否则会把慢起播误报成需要整场重建。
      if (this.clockState === "recovering") {
        this.recoveryTimeout = setTimeout(() => this.onRecoveryTimeout(), RECOVERY_TIMEOUT_MS);
      }
      return;
    }
    Log.w(`恢复后 ${RECOVERY_TIMEOUT_MS}ms 内视频无推进，升级整场重建`);
    this.onResyncFailed?.();
  }

  // ==================== 可播性判定 ====================

  /** 视频是否"正在可播地推进"：没暂停、没在 seek，且已有足够数据继续播。 */
  private hasPlayableVideoData(): boolean {
    const video = this.video;
    if (!video) {
      return false;
    }
    return !video.paused && !video.seeking && !this.seeking && video.readyState >= HTMLMediaElement.HAVE_FUTURE_DATA;
  }

  /**
   * 现在是否允许往链上排程音频。三种放行：
   *   - 视频可播（正常前台播放）；
   *   - 处于 waiting/stalled 宽限期内：视频只是瞬间卡一下，已排的音频必须继续（卡视频 ≠ 掉音频）；
   *   - 页面隐藏：UA 会暂停"无音轨"（软解）video，后台语义就是音频按 AudioContext 时钟 free-run。
   */
  private canScheduleAudio(): boolean {
    return !this.buffering && (this.hasPlayableVideoData() || this.stallGraceActive || this.isPageHidden());
  }

  /** 页面隐藏 / 后台 free-run 判定：此时视频时钟不可信，音频按自身时钟推进。 */
  private isPageHidden(): boolean {
    return document.visibilityState === "hidden" || this.clockState === "background";
  }

  private resetDriftState(): void {
    this.driftEma = 0;
    this.driftEmaPrimed = false;
    this.bypassActive = false;
  }

  /**
   * Video went into `waiting`/`stalled`. Do NOT pause the PCM chain immediately:
   * the scheduled audio keeps playing (and keeps being scheduled) for a short
   * grace — a brief video hiccup (SEI gap, MSE append in flight) then recovers
   * with the audio still synchronized. Only if the video is still not playable
   * after the grace do we hold the chain and wait for the resume event.
   */
  private enterBuffering(reason: "waiting" | "stalled"): void {
    const video = this.video;
    if (!video || video.paused || video.seeking || this.seeking || this.buffering) {
      return;
    }
    if (this.bufferingHoldTimer !== null) {
      return; // grace already running
    }
    this.stallGraceActive = true;
    Log.v(`Video ${reason}; PCM keeps playing within ${BUFFERING_HOLD_GRACE_MS}ms grace`);
    this.bufferingHoldTimer = setTimeout(() => {
      this.bufferingHoldTimer = null;
      this.stallGraceActive = false;
      if (this.buffering) {
        return;
      }
      Log.v(`Video still not playable after ${BUFFERING_HOLD_GRACE_MS}ms; holding PCM audio scheduling`);
      this.buffering = true;
      this.cancelChain(true);
      this.pending = [];
      this.feedCursorSec = null;
      this.resetDriftState();
    }, BUFFERING_HOLD_GRACE_MS);
  }

  private maybeExitBuffering(): void {
    const video = this.video;
    if (!video || !this.hasPlayableVideoData()) {
      return;
    }

    // Stall recovered within the grace window: the chain was never held.
    if (this.bufferingHoldTimer !== null) {
      clearTimeout(this.bufferingHoldTimer);
      this.bufferingHoldTimer = null;
    }
    this.stallGraceActive = false;

    if (!this.buffering) {
      return;
    }

    this.buffering = false;
    if (this.clockState !== "active") {
      // Video clock not trusted yet; the state machine anchors on its own
      // events (recovery timeupdate / background free-run via pump).
      this.pump();
      return;
    }
    Log.v("Video playback resumed; resyncing PCM audio");
    this.resyncFromBuffer(video.currentTime);
  }

  /** 重新锚定：让给定媒体时间成为拉伸器输入轴与输出轴的共同起点。 */
  private anchor(timeSec: number): void {
    this.stretcher?.reset();
    // 立刻把当前速率前馈给拉伸器：等下一个控制 tick 才设，漂移会先在排程管线里积起来，
    // 视频处于追赶模式时会直接再触发一次硬重同步。
    this.stretcher?.setRatio(clamp(this.video?.playbackRate || 1, RATE_MIN, RATE_MAX));
    this.feedCursorSec = timeSec;
    this.stretcherBase = timeSec;
    this.outputStreamCursor = timeSec;
    this.softSyncUntil = (this.audioCtx?.currentTime ?? 0) + SOFT_SYNC_WINDOW_SEC;
  }

  /** 把一块输入送过拉伸器，产出排进排程链（空产出 = 还凑不满一个合成周期）。 */
  private feedStretcher(stretcher: TimeStretcher, samples: Float32Array, sampleRate: number): void {
    const out = stretcher.process(samples);
    if (out.length === 0) {
      return;
    }
    // 输出末端对应的流时间：拉伸器输入位置（帧）换算成秒后加上输入基点。
    const streamEndSec = this.stretcherBase + stretcher.position / sampleRate;
    // 领先量只在 ACTIVE 有意义（此时视频时钟可信）；其余状态传 null，让链立刻起播。
    const leadSec =
      this.clockState === "active" && this.video
        ? this.outputStreamCursor - this.video.currentTime
        : null;
    this.chain?.append(
      {
        samples: out,
        channels: stretcher.channels,
        sampleRate,
        streamStartSec: this.outputStreamCursor,
        streamEndSec,
      },
      leadSec,
    );
    this.outputStreamCursor = streamEndSec;
  }

  // ==================== 排程链控制（委托 OutputChain） ====================

  /** 当前正在播放的内容时间（媒体时间轴，秒）；链空闲/未起播时 null。 */
  private audioStreamTimeNow(): number | null {
    const ctx = this.audioCtx;
    if (!ctx) {
      return null;
    }
    return this.chain?.playedStreamSec(ctx.currentTime) ?? null;
  }

  /** 停止整条排程链；smooth = 先做一次快速增益下潜再停（避免可闻的咔哒声）。 */
  private cancelChain(smooth = false): void {
    const ctx = this.audioCtx;
    const chain = this.chain;
    if (!chain) {
      return;
    }
    if (smooth && ctx && this.gain && chain.hasScheduled && ctx.state === "running") {
      const now = ctx.currentTime;
      const target = this.muted ? 0 : this.volume;
      const gain = this.gain.gain;
      gain.cancelScheduledValues(now);
      gain.setValueAtTime(target, now);
      gain.linearRampToValueAtTime(0, now + FADE_SEC);
      gain.setValueAtTime(target, now + 0.03);
      chain.stopAt(now + FADE_SEC + 0.001);
      return;
    }
    chain.stopNow();
  }

  // ==================== Drift control ====================

  /** Run drift control and then keep the scheduling window filled exactly
   *  once. A successful resync pumps internally, so do not repeat it.
   *  Ratechange passes false to preserve its old no-op behavior when drift
   *  control is not currently applicable. */
  private controlAndPump(pumpWhenSkipped = true): void {
    this.trackVideoClock();
    // 回前台对齐挂起：等视频时钟恢复推进后执行一次（期间音频保持 free-run 继续播）。
    if (
      this.pendingForegroundAlign &&
      this.clockState === "active" &&
      !this.isPageHidden() &&
      this.isVideoClockAdvancing()
    ) {
      this.alignToVideoOnReturn();
    }
    const result = this.controlTick();
    if (result === "updated" || (result === "skipped" && pumpWhenSkipped)) {
      this.pump();
    }
    this.audioStallWatchdog();
  }

  /**
   * 静音看门狗（C9b）：详情见 SILENT_STALL_MS 注释。推进判定必须用**内容游标**
   * （`contentCursorSec`）——队头/链尾在正常播放时可能长时间不变，用它会把正常播放误判成停摆
   * （误触发的重锚本身就是一次可听的卡顿）。触发动作 = 按当前播放头重建排程链（只丢陈旧内容，
   * 不做任何时间轴平移）。
   */
  private audioStallWatchdog(): void {
    const hasContent = (this.chain?.hasScheduled ?? false) || this.pending.length > 0 || !this.streamBuffer.empty;
    if (hasContent) {
      const cursor = this.contentCursorSec();
      if (cursor !== null && (!Number.isFinite(this.lastAudioProgressSec) || cursor > this.lastAudioProgressSec)) {
        this.lastAudioProgressSec = cursor;
        this.lastAudioProgressMs = performance.now();
      }
      if (this.chainStartedAtMs === null && (this.chain?.hasScheduled ?? false)) {
        this.chainStartedAtMs = performance.now();
        this.lastAudioProgressMs = performance.now();
      }
      return;
    }

    // 从未成功排程过（起播阶段）：交给内核自身的锚定逻辑，不介入。
    if (this.chainStartedAtMs === null) return;
    // 后台 free-run / 回前台待对齐 / 非活跃态：时钟不可信或不应干预。
    if (this.isPageHidden() || this.pendingForegroundAlign || this.clockState !== "active") return;
    if (!this.hasPlayableVideoData() || !this.isVideoClockAdvancing()) return;

    const now = performance.now();
    if (now - this.chainStartedAtMs < SILENT_STALL_MIN_UPTIME_MS) return;
    if (now - this.lastAudioProgressMs < SILENT_STALL_MS) return;

    const stalledMs = now - this.lastAudioProgressMs;
    this.stallTrips++;
    this.lastAudioProgressMs = now;
    Log.w(
      `音频链停摆 ${Math.round(stalledMs)}ms，而视频时钟仍在推进 ` +
        `(video=${(this.video?.currentTime ?? 0).toFixed(2)}s, 第 ${this.stallTrips} 次) ` +
        `→ 按播放头强制重锚`,
    );
    // 按当前播放头重建：陈旧队列被丢弃，后续 PCM 到达时直接锚定到播放头附近。
    this.resyncFromBuffer(this.video?.currentTime ?? 0);
  }

  private controlTick(): ControlOutcome {
    const ctx = this.audioCtx;
    const video = this.video;
    if (!ctx || !video || ctx.state !== "running" || !this.stretcher) {
      return "skipped";
    }
    // Drift control and hard resync are meaningful only when the video clock
    // is trusted and advancing. BACKGROUND/RECOVERING free-run: correcting
    // against a frozen or rebuilding video clock replays audio (the "broken
    // record" loop). During the stall grace the video clock may be frozen too
    // — skip drift so the chain simply drains and keeps pace instead of
    // fighting (or hard-resyncing against) a stopped clock.
    // 时钟未在推进（后台被节流、回前台待对齐、解冻前）时不做漂移纠偏/硬重同步：
    // 对着冻结的时钟纠偏只会砍掉已排好的链（controlTick 的启用条件）。
    if (
      this.clockState !== "active" ||
      this.pendingForegroundAlign ||
      this.stallGraceActive ||
      this.buffering ||
      !this.hasPlayableVideoData() ||
      !this.isVideoClockAdvancing()
    ) {
      return "skipped";
    }

    const audioTime = this.audioStreamTimeNow();
    if (audioTime === null) {
      // 链空闲：若队列已空但时间轴仍覆盖当前位置（例如暂停恢复后），直接从时间轴重建。
      if (this.pending.length === 0 && !(this.chain?.hasScheduled ?? false) && !this.streamBuffer.empty) {
        const target = video.currentTime;
        if (this.streamBuffer.covers(target, 0.1)) {
          return this.resyncFromBuffer(target) ? "pumped" : "skipped";
        }
      }
      return "skipped";
    }

    const drift = audioTime - video.currentTime;
    if (this.driftEmaPrimed) {
      this.driftEma += DRIFT_EMA_ALPHA * (drift - this.driftEma);
    } else {
      this.driftEma = drift;
      this.driftEmaPrimed = true;
    }

    // 音频持续领先视频（例如上游轴阻塞）时，直接从时间轴重建排程链重对齐：
    // 阈值 1.5s 已低于上游 rebase 门的 2s，PCM 一个字节不丢（时间轴仍持有），
    // 因此本引擎不需要额外的「轴平移」原语。
    if (Math.abs(drift) > HARD_RESYNC_THRESHOLD) {
      Log.v(`漂移 ${drift.toFixed(3)}s 超限，硬重同步`);
      return this.resyncFromBuffer(video.currentTime) ? "pumped" : "skipped";
    }

    // 速率匹配：跟随 video.playbackRate，再按漂移做残余修正（细节见 planStretchRatio）。
    const { ratio, rate, softSync } = this.planStretchRatio(ctx.currentTime, video.playbackRate || 1, drift);
    this.stretcher.setRatio(ratio);

    if (++this.diagTicks >= DRIFT_LOG_TICKS) {
      this.diagTicks = 0;
      Log.v(
        `A/V 漂移=${(this.driftEma * 1000).toFixed(1)}ms, rate=${rate}, 拉伸比=${ratio.toFixed(4)}, ` +
          `模式=${this.bypassActive ? "bypass" : softSync ? "soft" : "steady"}, 停摆=${this.stallTrips}`,
      );

      // "播放中声音卡顿一下"的现场诊断：把这一窗口内各丢弃环节的**增量**打成 WARN。
      // 全为零 = 卡顿不来自软解 PCM 链路（转查 MSE/封装）；非零则直接定位到是 worker 侧丢
      // （remux/trim/decodeErr/ovf）还是播放器侧停摆（stall）。
      const stats = this.workerStats;
      if (stats) {
        const prev = this.prevWorkerStats;
        const delta = (get: (s: PcmWorkerStats) => number): number => (prev ? get(stats) - get(prev) : 0);
        const remux = delta((s) => s.remuxDropChunks);
        const trim = delta((s) => s.trimSamples);
        const decodeErr = delta((s) => s.decodeErrors);
        const ovf = delta((s) => s.pendingOverflowDrops);
        if (remux + trim + decodeErr + ovf > 0) {
          Log.w(
            `近 ~60s 音频丢弃：worker[remux=${remux} trim=${trim} decodeErr=${decodeErr} ovf=${ovf}] ` +
              `player[stall=${this.stallTrips}]`,
          );
        }
        this.prevWorkerStats = stats;
      }
    }
    return "updated";
  }

  /**
   * 计算本次控制 tick 要施加的拉伸比（漂移控制的策略层）。
   *
   * 两级策略：
   *   - **软同步**（起播/重锚后的窗口内，或漂移仍大于退出阈值）：直接跟瞬时漂移，允许 ±35% 的
   *     强修正，让偏差尽快收敛；
   *   - **稳态**：跟 EMA 平滑后的漂移，限制在 ±10%——WSOLA 保音高，10% 的临时速度偏差不可闻。
   *
   * 同时维护「直达旁路迟滞」：正常速率且漂移落在死区内时把拉伸比固定为 1（WSOLA 走零失真直通），
   * 进入阈值 1%、已经旁路后退出阈值放宽到 2%，避免在边界反复切换造成可闻的断续感。
   *
   * 约定：漂移为正 = 音频超前 → 拉伸比变小（放慢）。注意本方法会更新 `bypassActive` 迟滞状态。
   */
  private planStretchRatio(
    ctxNowSec: number,
    playbackRate: number,
    driftSec: number,
  ): { ratio: number; rate: number; softSync: boolean } {
    const rate = clamp(playbackRate || 1, RATE_MIN, RATE_MAX);
    const softSync = ctxNowSec < this.softSyncUntil || Math.abs(this.driftEma) > SOFT_SYNC_EXIT_DRIFT;
    const measuredDrift = softSync ? driftSec : this.driftEma;
    const limit = softSync ? SOFT_SYNC_LIMIT : RATIO_DRIFT_MAX;
    const gain = softSync ? SOFT_SYNC_DRIFT_GAIN : RATIO_DRIFT_GAIN;
    const correction = clamp(measuredDrift * gain, -limit, limit);

    const bypassLimit = this.bypassActive ? BYPASS_EXIT_DRIFT : BYPASS_ENTER_DRIFT;
    this.bypassActive =
      playbackRate === 1 &&
      !softSync &&
      Math.abs(driftSec) <= bypassLimit &&
      Math.abs(this.driftEma) <= bypassLimit;

    const ratio = this.bypassActive ? 1 : clamp(rate * (1 - correction), RATE_MIN, RATE_MAX);
    return { ratio, rate, softSync };
  }

  // ==================== 流时间轴缓冲（委托 PcmStreamBuffer） ====================

  /** 把 [startSec, startSec + REFILL_WINDOW_SEC) 的已解码内容灌进待排程队列。 */
  private refillPending(startSec: number): void {
    if (this.pending.length >= PENDING_LIMIT) {
      return;
    }
    const pieces = this.streamBuffer.slice(startSec, REFILL_WINDOW_SEC, PENDING_LIMIT - this.pending.length);
    for (const piece of pieces) {
      this.pending.push(piece);
    }
  }

  /**
   * 按播放头回收过旧缓冲。ACTIVE 用视频时钟；后台 free-run 期间视频时钟被冻结，
   * 改用链上播放头，再退回最新缓冲端点 —— 回收基准若停住，缓冲会无界增长。
   */
  private recycleStreamBuffer(): void {
    const video = this.video;
    if (!video || this.streamBuffer.empty) {
      return;
    }
    const referenceSec =
      this.clockState === "active"
        ? video.currentTime
        : (this.audioStreamTimeNow() ?? this.streamBuffer.last()?.endSec ?? video.currentTime);
    this.streamBuffer.recycle(
      referenceSec,
      this.config.bufferCleanupMinBackward ?? DEFAULT_KEEP_BEHIND_SEC,
      this.config.bufferCleanupMaxBackward ?? DEFAULT_MAX_BACKWARD_SEC,
    );
  }

  // ==================== 重锚 / seek ====================

  /**
   * 以 targetSec 为起点，从流时间轴重建排程链（seek、暂停恢复、硬重同步共用）。
   * 旧轴上已排程的音频被丢弃、时间轴上 targetSec 之前的内容不再排入；
   * targetSec 之后的已解码内容一个字节不丢，衔接无缝隙。
   * 返回 false 表示时间轴没有覆盖该位置（调用方据此决定上报或继续等待新数据）。
   */
  private resyncFromBuffer(targetSec: number): boolean {
    this.cancelChain(true);
    this.pending = [];
    this.feedCursorSec = null;
    this.resetDriftState();

    this.refillPending(targetSec);
    if (this.pending.length === 0) {
      // 注意：时间轴的 indexAround 对落在空隙/两端之外的目标会返回"最近的块"而不是 -1，
      // 所以这里必须以"实际拿到了几块"为准。把「没东西可排」与「不在缓冲内」同等对待，
      // 调用方（尤其 completeRecovery）才能正确升级，而不是静默地一直没声音。
      Log.v(`重锚目标 ${targetSec.toFixed(3)}s 未被时间轴覆盖，等待新数据`);
      return false;
    }
    Log.v(`重锚到 ${targetSec.toFixed(3)}s，灌入 ${this.pending.length} 块`);
    this.pump();
    return true;
  }

  private onVideoSeeking(): void {
    if (this.aligningSeek) {
      // 回前台对齐 seek：音频链正在播 heard 内容、视频跳到 heard，无需清链/重锚。
      return;
    }
    this.buffering = false;
    // seek 期间只停掉已排的链、**不清队列**：等 seeked 后按新位置从缓冲重建
    // 若此处清空队列，会造成"seek 后
    // queue=0、静音"的成因之一。
    this.cancelChain();
    this.clearStallGrace();
    this.seeking = true;
  }

  private onVideoSeeked(): void {
    if (!this.video) return;
    if (this.aligningSeek) {
      // 回前台对齐 seek 完成：在 heard 位置重建音频链（不重播、不静音）。
      this.aligningSeek = false;
      this.seeking = false;
      this.resyncFromBuffer(this.video.currentTime);
      return;
    }
    const targetTime = this.video.currentTime;

    Log.v(`Video seeked to ${targetTime.toFixed(3)}, resyncing audio`);
    this.seeking = false;
    // A completed seek makes video.currentTime authoritative in any state.
    if (this.clockState === "recovering") {
      this.setSyncState(document.visibilityState === "hidden" ? "background" : "active");
    }
    this.resyncFromBuffer(targetTime);
  }

  /** Cancel a pending stall-grace hold (video recovered / stream stopped). */
  private clearStallGrace(): void {
    if (this.bufferingHoldTimer !== null) {
      clearTimeout(this.bufferingHoldTimer);
      this.bufferingHoldTimer = null;
    }
    this.stallGraceActive = false;
  }

  // ==================== Playback Control ====================

  async play(): Promise<void> {
    const ctx = this.audioCtx;
    if (ctx && ctx.state !== "running") {
      try {
        await ctx.resume();
        this.autoplaySuspendedNotified = false;
        // 后续由 onstatechange 驱动（后台 free-run 或恢复重锚）。
      } catch {
        Log.w("play(): AudioContext.resume() 失败");
      }
      return;
    }

    const video = this.video;
    if (video && this.clockState === "active" && this.canScheduleAudio()) {
      this.resyncFromBuffer(video.currentTime);
    }
  }

  /** 暂停：停链、清队列、挂起 AudioContext（video 的 pause 事件会自动走到这里）。 */
  pause(): void {
    this.buffering = false;
    this.clearStallGrace();
    this.pendingForegroundAlign = false;
    this.aligningSeek = false;
    this.chainStartedAtMs = null;
    this.cancelChain();
    this.pending = [];
    this.feedCursorSec = null;
    if (this.audioCtx?.state === "running") {
      void this.audioCtx.suspend();
    }
  }

  /** 停止：清空全链路（时间轴、队列、排程、拉伸器状态），保留 AudioContext 复用。 */
  stop(): void {
    this.cancelChain();
    this.clearStallGrace();

    this.pending = [];
    this.streamBuffer.clear();

    this.buffering = false;
    this.seeking = false;
    this.pendingForegroundAlign = false;
    this.aligningSeek = false;
    this.chainStartedAtMs = null;
    this.feedCursorSec = null;
    this.stretcher?.reset();
    this.stretcherFailed = false;
    this.softSyncUntil = 0;
    this.resetDriftState();
    this.setSyncState(document.visibilityState === "hidden" ? "background" : "active");
  }

  // ==================== 外部（mse-backend）适配 ====================

  /** 解码器交付的 planar PCM → 交错后喂入。 */
  enqueue(chunk: PCMAudioChunk): void {
    const channels = chunk.pcm.length;
    if (channels === 0 || chunk.pcm[0].length === 0) {
      return;
    }
    const frames = chunk.pcm[0].length;
    for (const channel of chunk.pcm) {
      if (channel.length !== frames) {
        return; // 各声道等长才可交错
      }
    }
    const interleaved = new Float32Array(frames * channels);
    for (let ch = 0; ch < channels; ch++) {
      const source = chunk.pcm[ch];
      for (let i = 0; i < frames; i++) {
        interleaved[i * channels + ch] = source[i];
      }
    }
    this.feed(interleaved, channels, chunk.sampleRate, chunk.time);
  }

  /** 显式挂起（mse-backend pause()）：声音随画面停，AudioContext 时钟冻结。 */
  suspend(): void {
    this.pause();
  }

  /** 从挂起/暂停恢复（mse-backend play()）：解冻并按视频位置重新对齐。 */
  resume(): void {
    void this.play();
  }

  /** 播放速率变化通知（video 的 ratechange 已自身订阅；此接口供上层直接调用）。 */
  setPlaybackRate(_rate: number): void {
    this.controlAndPump(false);
  }

  /** 新流/停止：清空全链路（AudioContext 保留复用）。 */
  flush(): void {
    this.stop();
  }

  setVolume(volume: number): void {
    this.volume = Math.max(0, Math.min(1, volume));
    this.applyGain();
  }

  setMuted(muted: boolean): void {
    this.muted = muted;
    this.applyGain();
  }

  /** 把音量/静音落到增益节点（用立刻生效的 value，不走 scheduled 值）。 */
  private applyGain(): void {
    if (!this.gain || !this.audioCtx) {
      return;
    }
    const gain = this.gain.gain;
    gain.cancelScheduledValues(this.audioCtx.currentTime);
    gain.value = this.muted ? 0 : this.volume;
  }

  async destroy(): Promise<void> {
    this.stop();
    this.detachVideo();

    this.stretcher?.destroy();
    this.stretcher = null;

    if (this.gain) {
      this.gain.disconnect();
      this.gain = null;
    }
    this.chain = null;

    if (this.audioCtx) {
      this.audioCtx.onstatechange = null;
      await this.audioCtx.close();
      this.audioCtx = null;
    }
  }
}
