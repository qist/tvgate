/*
 * 音频同步内核（playback-engine 与 ac3-lab **共用同一份实现**）
 *
 * 同步模型：**视频是主时钟**，但要参照"眼睛看到的帧"和"耳朵听到的样本"：
 *
 *   - 在视频时间轴上时间为 T 的样本，我们希望它在"屏幕显示到 T"时被听到。
 *   - AudioContext 图时间 S 排的样本，实际在 S + outputLatency 出声。
 *   - 图时间 S 时屏幕上的帧对应 currentTime(S) − displayLead（displayLead 由 rVFC 实测）。
 *
 *   所以排程起点：S = ctxNow + (T − visibleVideoTime) − outputLatency + calibration
 *
 * 五条不变量（前三条是实测血泪，禁止回退；细节见 doc/PLAYER-SYNC-REDESIGN.md）：
 *
 *  1. **锚定只能"跳"不能"重放"**。链重启时若算出的起点已落在过去，必须用
 *     `start(when, offset)` 跳过本段中已过期的部分；整段过期则丢弃。
 *     反例：`start = max(desired, now+ε)` 会把滞后量写进新链并永久保留
 *     （实测：启动/重锚后恒定 −190ms，且低于重锚阈值 → 永远得不到纠正）。
 *
 *  2. **锚定前视频时钟必须确实在推进**。刚点播放 / MSE 还在缓冲时 `paused` 已为 false
 *     但 `currentTime` 停在起点附近微跳，据此锚定会把随后那段冻结记成"音频超前"
 *     （实测恒定 +63ms）。要求：最近仍在动，且自上次锚定起已真实推进一节。
 *
 *  3. **ctx 时间 ↔ 媒体时间必须按"实际排出去的内容"记账**。每段的 ctx 跨度与 stream
 *     跨度必须同源推导（`ctxSec = contentSec / rate`），否则 heardStreamTime() 的插值
 *     会与真实出声内容分离 —— 表现为"环量到 ≈0 但口型对不上"。
 *
 *  4. **纠偏只有硬重锚一种**（本内核不做速率微调；变速属于上层，见设计文档阶段 2）。
 *     每次重锚对应一次可听见的接缝，可听、可见、可计数。
 *
 *  5. **校准常量只标定、不参与收敛**。恒定偏差在 DOM 里测不到，只能标定。
 */

/** 已排程音频相对图时间的前瞻窗口（秒）默认值。窗口越小，纠偏响应越快。 */
const DEFAULT_SCHEDULE_AHEAD_SEC = 0.6;
/**
 * 仅用于"不能把起点排在已经过去的图时间"（Web Audio 不接受过去时刻）。
 * 它**不参与对齐**：链重启时 start 与 desired 的差会通过 start(when, offset)
 * 从 PCM 里跳掉，因此不会变成恒定的音频延迟。
 */
const SCHEDULE_EPSILON_SEC = 0.01;
/** 视频时钟停住/音频远在播放头之前时，先不排程（等播放头追上来）。 */
const MAX_SCHEDULE_LEAD_SEC = 2;
/** 链重启淡入长度，避免爆音。 */
const FADE_SEC = 0.005;
/** outputLatency 上限钳制，防止异常上报把参照推得过远。 */
const MAX_OUTPUT_LATENCY_SEC = 0.5;
/** 视频显示延迟上限钳制：超过它的 rVFC 样本按 seek/跳变噪声丢弃。 */
const MAX_VIDEO_PRESENTATION_LATENCY_SEC = 0.5;
/** 视频显示延迟的 EMA 平滑系数（设备常量，取小些）。 */
const VIDEO_LEAD_EMA_ALPHA = 0.05;
/** 视频显示延迟生效所需的最少 rVFC 样本数。 */
const VIDEO_LEAD_MIN_SAMPLES = 10;
/** 硬重锚最小间隔，避免抖动。 */
export const REANCHOR_MIN_INTERVAL_MS = 1500;
/**
 * 漂移超过此值就硬重锚 —— 这是内核唯一的纠偏手段（不做速率微调）。
 * 注意：低于此阈值的恒定偏差**不可**靠重锚修正，只能靠 calibration 标定。
 */
export const REANCHOR_DRIFT_SEC = 0.25;
/** 重锚/入队时丢弃"已落后于画面"样本的宽限（保留一点点，避免切到字缝上）。 */
const STALE_KEEP_SEC = 0.05;
/** 判定"视频时钟仍在推进"的静默窗口（毫秒）：超过则认为时钟停住了。 */
const CLOCK_STALE_MS = 150;

/**
 * WSOLA 微调带宽（相对视频速率的比例）。残余漂移的修正只在这个带内进行；
 * 超出带宽的漂移由硬重锚兜底（见设计文档 §3.5）。
 */
const RATIO_TRIM = 0.08;
/** ratio 变化率上限（每秒），避免 WSOLA 交叠淡入跟不上而产生金属声。 */
const RATIO_SLEW_PER_SEC = 0.03;
/**
 * 残余漂移 → ratio 的增益。ratio 每偏离视频速率 δ，内容速率就差 δ 秒/秒，
 * 故取 δ = −gain × drift 即可把 drift 拉回 0（再受带宽与变化率限幅约束）。
 *
 * 这是一个带**死时间**的比例环：死时间 θ ≈ 排程前瞻 0.6s + 一个合成周期 + 控制环采样
 * 0.25s ≈ 0.9s。对"积分对象 + 死时间"必须满足 `K × θ < 1` 才不发散 —— 实测 K=1.5
 * （K×θ≈1.35）会形成 ±8% 满幅极限环、drift 在 ±150ms 间摆动。取 0.6 → K×θ≈0.54。
 */
const RATIO_DRIFT_GAIN = 0.6;
/**
 * 死区：|drift| 小于它就不修正。避免在零点附近来回追，也避免把测量噪声积分进
 * ratio —— 稳态时 ratio 就该恰好停在视频速率上。
 */
const RATIO_DEAD_ZONE_SEC = 0.02;
/**
 * 合成周期预填余量（秒）：`wsola.c` 一个周期需要 `seek + seq + overlap = 10 + 30 + 12 = 52ms`
 * 输入在内才会产出第一段输出（实测 60~100ms，取决于投喂粒度）。
 */
const STRETCHER_PREFILL_SEC = 0.06;
/** 输入流允许的最大向后重叠（秒）：超过即视为时间轴倒退，重置伸缩器。 */
const MAX_INPUT_OVERLAP_SEC = 0.02;

/**
 * 可注入的时域伸缩器（WSOLA）。`WasmStretcher` 天然满足这个形状，可直接传入。
 *
 * 语义（已在 `wsola.c` 与实测中确认，见设计文档 §3.4）：
 *  - `process()` 返回的输出帧数满足 `输出时长 = 输入时长 / ratio`；
 *  - `position` 是**已输出内容在输入流中的绝对位置（输入帧）** —— 这是权威的内容
 *    游标，记账必须用它，不能拿名义 ratio 推算；
 *  - ratio == 1 时逐位直通；但 ratio ≠ 1 时一个合成周期需要预填 52ms 输入，
 *    且**没有 flush 接口**（尾部约 48ms 会被丢弃）。
 */
export interface AudioStretcher {
  readonly sampleRate: number;
  readonly channels: number;
  /** 已输出内容在输入流中的绝对位置（输入帧）。 */
  readonly position: number;
  setRatio(ratio: number): void;
  process(input: Float32Array): Float32Array;
  reset(): void;
  destroy(): void;
}

/** 惰性创建伸缩器；返回 null 表示该环境用不了（此时内核退回链式 1:1 排程）。 */
export type StretcherFactory = (sampleRate: number, channels: number) => Promise<AudioStretcher | null>;

/**
 * 交给 `AudioBufferSourceNode` 的一段 PCM（交错 float32）。
 *
 * 统一描述"直通排程"与"伸缩后排程"两种来源，使排程/锚定逻辑只有一份：
 *  - 直通：`contentPerFrame = 1/sampleRate`，`nodeRate = 视频速率`；
 *  - 伸缩后：`nodeRate = 1`，`contentPerFrame` 由 `position` 增量推出。
 */
interface Segment {
  pcm: Float32Array;
  channels: number;
  sampleRate: number;
  frames: number;
  /** 每输出帧对应的媒体内容时长（秒/帧）。 */
  contentPerFrame: number;
  /** 第 0 帧对应的媒体时间（秒）。 */
  streamStart: number;
  /** 交给节点的 playbackRate（直通=视频速率；伸缩后恒 1）。 */
  nodeRate: number;
  /** 来源：决定"确认排程后"从哪里把它摘掉。 */
  source: "direct" | "stretched";
}

/** 队列中的一段 PCM。`timeSec` 必须与 video.currentTime 在同一时间轴。 */
export interface AudioSyncChunk {
  pcm: Float32Array;
  channels: number;
  sampleRate: number;
  timeSec: number;
  durationSec: number;
}

interface ScheduledSpan {
  node: AudioBufferSourceNode;
  ctxStart: number;
  ctxEnd: number;
  streamStart: number;
  streamEnd: number;
}

export interface AudioSyncStats {
  scheduledChunks: number;
  underruns: number;
  reanchors: number;
  droppedStale: number;
  queueSec: number;
  scheduledAheadSec: number;
}

export interface AudioSyncCoreOptions {
  ctx: AudioContext;
  video: HTMLVideoElement;
  /** 输出节点；省略则直连 `ctx.destination`。 */
  destination?: AudioNode;
  onLog?: (message: string) => void;
  /** 速率提供者：live-sync 跟随视频 playbackRate 时返回它，默认恒 1。 */
  getRate?: () => number;
  /** 排程前瞻窗口（秒）；页面隐藏时可返回更大的值。 */
  getScheduleAheadSec?: () => number;
  /** 页面是否隐藏：后台视频时钟不可信、video 可能被 UA 被动暂停，须按音频自由运行排程。 */
  getPageHidden?: () => boolean;
  /** 未排程队列长度保险丝；<=0 表示不限制。 */
  maxQueueChunks?: number;
  /** 漂移触发硬重锚的阈值（秒）；<=0 关闭该策略。默认 `REANCHOR_DRIFT_SEC`。 */
  reanchorDriftSec?: number;
  /**
   * 可选的 WSOLA 伸缩级。**省略 = 完全保持"链式 1:1 排程"的原有行为**。
   * 传入后：视频速率（`getRate`）由伸缩器以保音高的方式跟随，残余漂移在
   * ±`RATIO_TRIM` 带内无感修正；此时节点 playbackRate 恒为 1。
   */
  stretcherFactory?: StretcherFactory;
  /** 音频长时间排不出去时每次 pump 回调（上层据此重钉 worker / 上报不可恢复）。 */
  onSchedulingBlocked?: (aheadSec: number) => void;
  /** 从"排不出去"恢复为正常排程时回调一次。 */
  onSchedulingResumed?: () => void;
}

export class AudioSyncCore {
  private readonly ctx: AudioContext;
  private readonly video: HTMLVideoElement;
  private readonly gain: GainNode;
  private readonly getRate: () => number;
  private readonly getScheduleAheadSec: () => number;
  private readonly getPageHidden: () => boolean;
  private readonly maxQueueChunks: number;
  private readonly reanchorDriftSec: number;
  private readonly stretcherFactory: StretcherFactory | null;
  private readonly onSchedulingBlocked: ((aheadSec: number) => void) | null;
  private readonly onSchedulingResumed: (() => void) | null;

  private queue: AudioSyncChunk[] = [];
  private spans: ScheduledSpan[] = [];
  private nextStartTime = 0;
  /**
   * 持久轴平移（秒）：源 PTS 轴整体跳变/漂移超前时，把入队标签整体平移回去。
   * 只影响 enqueue 的新样本；resetChain()（seek/换台）清零。
   */
  private axisShiftSec = 0;
  /** 上一次 pump 看到的 video.currentTime，用于判断视频时钟是否在推进。 */
  private lastVideoClockSec = 0;
  /** 视频时钟最后一次发生变化的时间（performance.now）。 */
  private lastClockChangeAtMs = Number.NEGATIVE_INFINITY;
  /** 上一次成功锚定时视频时钟的位置（用于要求锚定前时钟已持续推进一节）。 */
  private lastAnchorClockSec = 0;

  private calibrationSec = 0;
  private displayLeadSec = 0;
  private lastReanchorAt = 0;
  private blocked = false;

  // ---- WSOLA 伸缩级（未注入 stretcherFactory 时全程为 null，行为与"链式 1:1"一致） ----
  private stretcher: AudioStretcher | null = null;
  private stretcherInit: Promise<void> | null = null;
  private stretcherSampleRate = 0;
  private stretcherChannels = 0;
  /** 创建失败一次即永久退回"链式 1:1"，避免每次 pump 重试刷屏。 */
  private stretcherFailed = false;
  /** `position == 0` 对应的媒体时间；reset 后由下一个喂入的 chunk 重新种入。 */
  private fedBaseMediaSec: number | null = null;
  /** 已喂入内容的媒体结束时间（含为时间轴缺口补入的静音）。 */
  private fedMediaEndSec = 0;
  /** 上一次产出输出时对应的 `position`（输入帧）。 */
  private outCursorInFrames = 0;
  /** 已产出、尚未排程的输出段。 */
  private outSegs: Segment[] = [];
  /** 当前生效的伸缩比与其中的漂移修正量（用于变化率限幅）。 */
  private currentRatio = 1;
  private ratioTrim = 0;
  private lastRatioUpdateMs = 0;

  private scheduledChunks = 0;
  private underruns = 0;
  private reanchors = 0;
  private droppedStale = 0;

  private readonly onLog: ((message: string) => void) | null;
  private probeHandle = 0;
  private probeEma = Number.NaN;
  private probeSamples = 0;
  private destroyed = false;

  constructor(options: AudioSyncCoreOptions) {
    this.ctx = options.ctx;
    this.video = options.video;
    this.onLog = options.onLog ?? null;
    this.getRate = options.getRate ?? (() => 1);
    this.getScheduleAheadSec = options.getScheduleAheadSec ?? (() => DEFAULT_SCHEDULE_AHEAD_SEC);
    this.getPageHidden = options.getPageHidden ?? (() => false);
    this.maxQueueChunks = options.maxQueueChunks ?? 0;
    this.reanchorDriftSec = options.reanchorDriftSec ?? REANCHOR_DRIFT_SEC;
    this.stretcherFactory = options.stretcherFactory ?? null;
    this.onSchedulingBlocked = options.onSchedulingBlocked ?? null;
    this.onSchedulingResumed = options.onSchedulingResumed ?? null;

    this.gain = this.ctx.createGain();
    this.gain.gain.value = 1;
    this.gain.connect(options.destination ?? this.ctx.destination);
  }

  /** 启动 rVFC 探针（实测视频显示延迟）。重复调用无副作用。 */
  start(): void {
    if (this.probeHandle || typeof this.video.requestVideoFrameCallback !== "function") {
      return;
    }
    const step = (_now: number, metadata: VideoFrameCallbackMetadata): void => {
      if (this.destroyed) return;
      this.samplePresentationLead(metadata);
      this.probeHandle = this.video.requestVideoFrameCallback(step);
    };
    this.probeHandle = this.video.requestVideoFrameCallback(step);
  }

  // ==================== 校准 / 参照量 ====================

  setCalibrationMs(ms: number): void {
    const next = Number.isFinite(ms) ? ms / 1000 : 0;
    if (Math.abs(next - this.calibrationSec) < 0.001) return;
    this.calibrationSec = next;
    // 对齐常量只在"链重启"时生效；已排好的链不会自己改，所以校准一变就必须重建。
    this.reanchor("calibration", true);
  }

  getCalibrationMs(): number {
    return this.calibrationSec * 1000;
  }

  /** 由 rVFC 实测的"媒体时钟领先屏幕帧"的量（秒）。 */
  setDisplayLead(sec: number): void {
    this.displayLeadSec = Number.isFinite(sec) ? sec : 0;
  }

  getDisplayLeadSec(): number {
    return this.displayLeadSec;
  }

  getOutputLatencySec(): number {
    const latency = this.ctx.outputLatency;
    if (typeof latency === "number" && Number.isFinite(latency) && latency > 0) {
      return Math.min(latency, MAX_OUTPUT_LATENCY_SEC);
    }
    const base = this.ctx.baseLatency;
    return typeof base === "number" && Number.isFinite(base) && base > 0 ? Math.min(base, MAX_OUTPUT_LATENCY_SEC) : 0;
  }

  /** 屏幕上那一帧对应的媒体时间。 */
  visibleVideoTime(): number {
    return this.video.currentTime - this.displayLeadSec;
  }

  /** 此刻"耳朵听到的"音频所处的媒体时间；链空闲/未开始时为 null。 */
  heardStreamTime(): number | null {
    const at = this.ctx.currentTime - this.getOutputLatencySec();
    for (const span of this.spans) {
      if (at >= span.ctxStart && at < span.ctxEnd) {
        const f = (at - span.ctxStart) / (span.ctxEnd - span.ctxStart);
        return span.streamStart + f * (span.streamEnd - span.streamStart);
      }
    }
    return null;
  }

  /** heard − visible：>0 音频领先画面，<0 音频落后画面。链空闲时为 null。 */
  driftSec(): number | null {
    const heard = this.heardStreamTime();
    return heard === null ? null : heard - this.visibleVideoTime();
  }

  isRunning(): boolean {
    return this.ctx.state === "running";
  }

  /** 当前生效的伸缩比（供面板/日志观测；未启用伸缩器或未就绪时恒为 1）。 */
  getStretcherRatio(): number {
    return this.stretcher ? this.currentRatio : 1;
  }

  /**
   * 音频内容游标：**下一个会被听到的内容时间**（秒）。这是排程链的真实进度 ——
   * 每次新排出一段就前移，比"队列头/span 结束时间"更可靠（队头在自动推进时可能
   * 长时间不变，用它做停摆判据会把正常播放误判成停摆）。
   * 与 `nextAudibleStreamSec()` 的区别：后者是"轴平移对齐"的测量点（对齐测量需要
   * 排除已排程但未播出的部分），本方法则是进度观测点。
   */
  contentCursorSec(): number | null {
    const lastSpan = this.spans[this.spans.length - 1];
    if (lastSpan) return lastSpan.streamEnd;
    if (this.outSegs.length > 0) return this.outSegs[this.outSegs.length - 1].streamStart;
    return this.queueHeadSec();
  }

  /**
   * 伸缩级是否可用：注入了工厂**且未创建失败**。
   *
   * 注意不能只看"有没有传工厂"：一旦创建失败（wasm 拿不到、内存不足…），必须整体退回
   * "链式 1:1 + playbackRate 跟随"。否则会既产不出输出、也不走直通 —— 等于永久静音。
   */
  private get stretchEnabled(): boolean {
    return this.stretcherFactory !== null && !this.stretcherFailed;
  }

  /** 伸缩器是否已就绪（未注入工厂、创建中或创建失败时为 false）。 */
  isStretching(): boolean {
    return this.stretcher !== null;
  }

  stats(): AudioSyncStats {
    let queueSec = 0;
    for (const chunk of this.queue) queueSec += chunk.durationSec;
    return {
      scheduledChunks: this.scheduledChunks,
      underruns: this.underruns,
      reanchors: this.reanchors,
      droppedStale: this.droppedStale,
      queueSec,
      scheduledAheadSec: Math.max(0, this.nextStartTime - this.ctx.currentTime),
    };
  }

  // ==================== 入队 / 链管理 ====================

  enqueue(chunk: AudioSyncChunk): void {
    if (this.destroyed || chunk.durationSec <= 0 || chunk.pcm.length === 0) {
      return;
    }
    // 持久轴平移：源轴整体跳变后的所有新样本都带着同一个偏差进来。
    chunk.timeSec += this.axisShiftSec;
    this.queue.push(chunk);
    // 只丢"已经落后于画面、放出来只会是回放"的样本。
    // 绝不能因为队列长就丢前端：队列前端是**下一个该播的**，丢它=丢声音
    // （实测：限速前音频时间戳能跑到播放头前 50s，按长度丢前端会把声音全丢光）。
    const floor = this.visibleVideoTime() - STALE_KEEP_SEC;
    while (this.queue.length > 1) {
      const head = this.queue[0];
      if (head.timeSec + head.durationSec >= floor) break;
      this.queue.shift();
      this.droppedStale++;
    }
    // 不在 enqueue 里做时间维度 trim。
    // Player 的 worker 解码速度远超实时播放（几秒内解完 60s+ 音频），
    // 如果按队头+limit 丢队尾，会丢掉合法的未来音频；等排程追到那些
    // 时间点时队列已空 → 间歇性静音。
    // 正确做法：保留全部未来 chunk，让排程自然消费。内存保护由
    // maxQueueChunks 保险丝负责（3000 chunk ≈ 96s）。
    // Lab 不需要这个，因为它在 feed 层用 waitForBufferRoom 做了限速。
    if (this.maxQueueChunks > 0 && this.queue.length > this.maxQueueChunks) {
      const drop = this.queue.length - this.maxQueueChunks;
      this.queue.splice(this.queue.length - drop, drop);
      this.droppedStale += drop;
      this.log(`queue overflow: dropped ${drop} future chunk(s)`);
    }
    this.pump();
  }

  /**
   * 丢弃落在给定媒体时间之后的未排程 chunk（one-shot 重锚配套）。
   * worker 重钉时间轴后，旧轴已排队 chunk 的标签仍整体超前 Δ 秒；不裁剪的话
   * 调度领先门（MAX_SCHEDULE_LEAD_SEC）会继续 blocked。代价为一次性 ≤Δ 秒静音。
   */
  trimFutureBeyond(sec: number): void {
    const before = this.queue.length;
    if (before === 0) return;
    this.queue = this.queue.filter((chunk) => chunk.timeSec <= sec);
    const dropped = before - this.queue.length;
    if (dropped > 0) {
      this.droppedStale += dropped;
      this.log(`trimFutureBeyond(${sec.toFixed(2)}s): dropped ${dropped} future chunk(s)`);
    }
  }

  /** 队列中第一段的媒体时间；队列为空时返回 null。 */
  queueHeadSec(): number | null {
    return this.queue[0]?.timeSec ?? null;
  }

  /**
   * 下一个将被排程（被听到）的内容时间：伸缩级已产出未排程的输出头 → 已排程链的
   * 内容游标（伸缩输出耗尽时，下一段输出从链尾内容位置继续）→ 未排程队列头（直通）。
   * 链重启（重锚）后首先被排程的就是它，因此是"轴平移对齐"的正确测量点。
   * **不能用 `queueHeadSec()`**：那是伸缩级的输入头，领先实际可听输出一个
   * 预填/待排程量，用它测量会过量平移（实测回前台多移 1.5s，之后 49 个 chunk
   * 被当陈旧丢弃）。
   */
  nextAudibleStreamSec(): number | null {
    if (this.outSegs.length > 0) return this.outSegs[0].streamStart;
    const lastSpan = this.spans[this.spans.length - 1];
    if (lastSpan) return lastSpan.streamEnd;
    return this.queueHeadSec();
  }

  /** 视频时钟是否最近仍在推进（前台对齐/漂移重锚的前置条件：绝不把音频对到冻结的时钟上）。 */
  isVideoClockAdvancing(): boolean {
    return performance.now() - this.lastClockChangeAtMs < CLOCK_STALE_MS;
  }

  /**
   * 把音频轴整体平移 `deltaSec`（负值 = 前移，用于消除"源轴超前"）：
   * 队列内 chunk、伸缩器已产出未排程的输出、伸缩器喂入游标与后续入队标签一并平移。
   * 内容一个字节都不丢、不重启链、无可闻接缝 —— 与 worker 重钉（会清队列+重置解码器、
   * 且绝对钉扎会被在途管线深度带偏）相比，这是同步生效且自校准的自愈手段。
   */
  rebaseAxis(deltaSec: number): void {
    if (this.destroyed || deltaSec === 0) return;
    this.axisShiftSec += deltaSec;
    for (const chunk of this.queue) chunk.timeSec += deltaSec;
    for (const seg of this.outSegs) {
      seg.streamStart += deltaSec;
    }
    if (this.fedBaseMediaSec !== null) this.fedBaseMediaSec += deltaSec;
    this.fedMediaEndSec += deltaSec;
    this.log(
      `rebaseAxis(${(deltaSec * 1000).toFixed(0)}ms): queue=${this.queue.length}, axisShift=${(this.axisShiftSec * 1000).toFixed(0)}ms`,
    );
  }

  /** 丢链 + 丢队列（seek/换台/时间轴重钉时用）。 */
  resetChain(): void {
    this.stopSpans();
    this.queue = [];
    this.blocked = false;
    // 时间轴已切走：持久平移属于旧轴，必须清零。
    this.axisShiftSec = 0;
    // 伸缩器内部状态与"已产出未排程"的输出也属于旧时间轴，一并作废。
    this.resetStretcher();
    // 时间轴已切走：时钟推进闸的"上次锚点"必须跟着重置。否则向后 seek 后
    // `videoClock − lastAnchorClockSec` 为负，锚定会被一直挡住直到播放头重新
    // 越过旧锚点（实测隐患：向后跳 60s 就等于静音 60s）。
    this.lastAnchorClockSec = this.video.currentTime;
  }

  /** 仅停掉已排程的链，保留队列数据（seek 期间用；与 `resetChain()` 的区别是**不丢队列**）。 */
  stopChain(): void {
    this.stopSpans();
  }

  /**
   * seek 专用：按目标媒体时间从**现有队列**重建调度链（v3.2.1 resyncFromBuffer 语义）。
   *
   * 与 `resetChain()` 的关键区别：**不清队列** —— seek 只改变播放位置，已解码的音频数据
   * 依然有效，队列里覆盖目标时间的内容从目标位置重新起锚即可。当前架构此前的 seek 路径是
   * `resetChain()`（清队列）+ `awaitingNewTimeline`（丢弃到达的 PCM），两边一夹就把 seek
   * 后的音频全部丢掉 → `queue=0`、静音。
   *
   * 同步策略（逐帧源 PTS 直标 + 锚定/漂移环）不受本方法影响：它只决定"从队列的哪一块起排"，
   * 锚定仍按 `ctx + (head − visible)` 落点，不存在回退到 v3.2.1 缓冲时间轴的问题。
   *
   * @returns false = 队列里没有覆盖目标时间的内容（等新喂入的数据即可，自身不会静音）。
   */
  resyncFromBuffer(targetSec: number): boolean {
    if (this.destroyed) return false;
    this.stopSpans();
    // 丢掉"整块都已越过目标时间"的前缀（seek 后不会再播的内容），其余全部保留。
    let drop = 0;
    while (drop < this.queue.length && this.queue[drop].timeSec + this.queue[drop].durationSec < targetSec) {
      drop++;
    }
    if (drop > 0) {
      this.queue.splice(0, drop);
      this.droppedStale += drop;
    }
    const head = this.queue[0];
    if (!head) {
      this.log(`resyncFromBuffer(${targetSec.toFixed(2)}s): nothing buffered at/after target; waiting for new data`);
      return false;
    }
    // 缓冲头领先目标过多：不能拿它硬排（desired 会超排程门 → blocked）。保留数据等对齐内容。
    if (head.timeSec > targetSec + MAX_SCHEDULE_LEAD_SEC) {
      this.log(
        `resyncFromBuffer(${targetSec.toFixed(2)}s): buffered head (${head.timeSec.toFixed(2)}s) leads target too far; waiting for aligned data`,
      );
      return false;
    }
    this.log(`resyncFromBuffer(${targetSec.toFixed(2)}s): chain rebuilt (dropped ${drop}, queue=${this.queue.length})`);
    this.pump();
    return true;
  }

  /**
   * 控制环（上层每 ~250ms 调一次）：排程 + 漂移过大时硬重锚。
   *
   * 纠偏策略集中在这里，lab 与 player 共用同一份。`allowReanchor=false` 用于
   * 页面隐藏（定时器被节流、排程窗口放大）等不该重锚的场景。
   */
  controlTick(allowReanchor = true): void {
    this.pump();
    if (!allowReanchor || this.reanchorDriftSec <= 0) return;
    const drift = this.driftSec();
    if (drift !== null && Math.abs(drift) > this.reanchorDriftSec) {
      this.reanchor(`drift ${(drift * 1000).toFixed(0)}ms`);
    }
  }

  /**
   * 硬重锚：丢掉已排程的链，让队列头重新按当前屏上帧时间（+标定）落点。
   * 这是内核唯一的纠偏手段 —— 不做速率微调，所以每次重锚都对应一次可听见的接缝。
   */
  reanchor(reason: string, force = false): void {
    const now = performance.now();
    if (!force && now - this.lastReanchorAt < REANCHOR_MIN_INTERVAL_MS) return;
    this.lastReanchorAt = now;
    this.reanchors++;
    this.stopSpans();

    // 丢掉已经落后于画面、放出来只会是"回放"的样本
    const floor = this.visibleVideoTime() - STALE_KEEP_SEC;
    const before = this.queue.length;
    this.queue = this.queue.filter((chunk) => chunk.timeSec + chunk.durationSec >= floor);
    this.droppedStale += before - this.queue.length;

    this.log(`reanchor(${reason}): droppedStale=${before - this.queue.length} queue=${this.queue.length}`);
    this.pump();
  }

  // ==================== 排程 ====================

  /** 排程：链空闲时按"听到时刻 == 屏幕帧时刻"求起点；否则首尾相接续排。 */
  pump(): void {
    const ctx = this.ctx;
    if (this.destroyed || ctx.state !== "running") return;
    const isHidden = this.getPageHidden();
    // 视频还没开始播（尚未点播放/暂停中）：此刻 video.currentTime 不能作为时间基准，
    // 排出去的音频会在视频一动时变成固定错位。等视频真的在走再排。
    // 后台例外：UA 会暂停 video（软解源无音轨被判无音频），此时音频必须 free-run，
    // 否则 pump 永久不排程 → 切后台立即静音（v3.2.1 后台语义）。
    if (this.video.paused && !isHidden) return;

    // 锚定判据（v3.2.1 同款 —— 当时修好了"1080i MP2 频道切台后无声"）：用**媒体可播
    // 状态**判断视频时钟是否可信，而不是"时钟推进量"。视频数据没来（readyState 低于
    // HAVE_FUTURE_DATA：重负载频道起播慢 / bwdif 软反交错 / 缓冲重建中）时不锚定、等数据；
    // 数据一到立刻锚定。
    //
    // 旧判据（150ms 内时钟有变化 + 自上次锚定推进 ≥0.1s）在"数据已到、但时钟推进极慢或
    // 被反复 rebase"时**永远不满足** → 永久静音（实测：videoClock 卡在 0.017~0.073 缓慢
    // 爬升、音频队列堆到 303 个 chunk 仍一声不出）。链延续不受此判据限制。
    const videoClock = this.video.currentTime;
    const nowMs = performance.now();
    if (Math.abs(videoClock - this.lastVideoClockSec) > 0.0001) {
      this.lastVideoClockSec = videoClock;
      this.lastClockChangeAtMs = nowMs;
    }
    const clockReady =
      !this.video.paused &&
      !this.video.seeking &&
      this.video.readyState >= HTMLMediaElement.HAVE_FUTURE_DATA;

    const videoRate = Math.min(2, Math.max(0.5, this.getRate() || 1));
    const aheadSec = this.getScheduleAheadSec();
    let skippedThisPump = 0;

    // 启用伸缩级时：节点速率恒 1，变速由伸缩器以保音高方式完成（ratio ≈ videoRate）。
    // 伸缩级不可用（未注入 / 创建失败）时退回 playbackRate 跟随 —— 会变调，但能同步。
    const nodeRate = this.stretchEnabled ? 1 : videoRate;
    if (this.stretchEnabled) {
      this.updateStretcherRatio(videoRate);
    }

    while (true) {
      if (this.nextStartTime - ctx.currentTime > aheadSec) break;

      const seg = this.takeSegment(aheadSec, nodeRate);
      if (!seg) break;

      const visible = this.visibleVideoTime();
      // 后台 free-run：没有"屏幕帧时刻"可对齐（video 停住/被 UA 暂停），音频必须
      // 立即续播（内容时间轴自由推进），不能按 (head−visible) 锚定——那会让 desired
      // 随 head 一路前移、触发排程门 → blocked → 周期 rebase 平移大轴，回前台
      // 残留错位（实测 rebase −10s 后回前台 drift=128ms）。回前台由 visibility 恢复
      // 做一次硬 reanchor 对齐视频。
      const desired = isHidden
        ? ctx.currentTime + SCHEDULE_EPSILON_SEC
        : ctx.currentTime + (seg.streamStart - visible) / videoRate - this.getOutputLatencySec() + this.calibrationSec;

      // 视频时钟停住（缓冲/不连续）时，desired 会随源时间轴一路前移：此时宁可不排程、
      // 等播放头追上来，也不要让排程链跑到未来几秒去。
      // 门不得小于排程窗口：页面隐藏时窗口放大到 6s（扛 timer 节流），若仍被 2s 门
      // 压死，后台缓冲永远只有 2s —— 后台节流下缓冲见底即静音（v3.2.1 后台窗口为 6s）。
      if (desired - ctx.currentTime > Math.max(MAX_SCHEDULE_LEAD_SEC, aheadSec)) {
        this.onSchedulingBlocked?.(desired - ctx.currentTime);
        // 不调 trimAudioLead()：队尾的 chunk 是合法的未来音频，丢掉就是静音。
        // 视频时钟在走，追上来后 pump 会自然锚定并消费。
        break;
      }

      let start: number;
      let chainRestart = false;
      if (this.nextStartTime <= ctx.currentTime + SCHEDULE_EPSILON_SEC) {
        // 链已断（首次排程 / 重锚 / underrun）：按视频时钟硬锚定。
        // 后台例外：视频时钟被 UA 节流/停住（软解源无音轨），clockReady 永不满足，
        // 不重排 → 永久静音。free-run 语义：允许按队列头时间续排，超前部分由
        // 排程门（放大到 6s）→ onSchedulingBlocked → maybeRebaseAxis 周期回收。
        if (!clockReady && !isHidden) break;
        const hadChain = this.nextStartTime > 0;
        if (hadChain) this.underruns++;
        start = Math.max(desired, ctx.currentTime + SCHEDULE_EPSILON_SEC);
        chainRestart = true;
        this.lastAnchorClockSec = videoClock;
        this.log(
          `anchor: videoClock=${videoClock.toFixed(3)} head=${seg.streamStart.toFixed(3)} ` +
            `slack=${(start - desired).toFixed(3)}s ${hadChain ? "(chain dead)" : "(first)"}`,
        );
      } else {
        start = this.nextStartTime;
      }

      // 不变量 1：链重启时 start 可能被钳到 now+ε 而落在 desired 之后。若仍从第 0 个
      // 样本起播，等于把"本该已经播过的那段"重放一遍，滞后量会被原样写进新链并永久
      // 保留。正确做法：跳过本段中已过期的部分 —— 跳过量按"内容以视频速率推进"折算。
      const skipCtxSec = chainRestart ? start - desired : 0;
      const skipFrames = Math.max(0, Math.min(Math.round(skipCtxSec * seg.sampleRate * seg.nodeRate), seg.frames));
      if (skipFrames >= seg.frames) {
        // 整段都已过期，放出去只会是回放：丢掉，继续看下一段。
        this.consumeSegment(seg);
        this.droppedStale++;
        skippedThisPump++;
        continue;
      }

      const buffer = ctx.createBuffer(seg.channels, seg.frames, seg.sampleRate);
      for (let ch = 0; ch < seg.channels; ch++) {
        const data = buffer.getChannelData(ch);
        for (let i = 0; i < seg.frames; i++) {
          data[i] = seg.pcm[i * seg.channels + ch];
        }
      }
      if (chainRestart) {
        this.applyFadeIn(buffer, skipFrames);
      }

      // 不变量 3：ctx 跨度与 stream 跨度必须同源推导（skipFrames 同时作用于两者）。
      const offsetSec = skipFrames / seg.sampleRate;
      const contentSec = (seg.frames - skipFrames) * seg.contentPerFrame;
      const ctxSec = (seg.frames - skipFrames) / (seg.sampleRate * seg.nodeRate);

      const node = ctx.createBufferSource();
      node.buffer = buffer;
      if (seg.nodeRate !== 1) {
        node.playbackRate.value = seg.nodeRate;
      }
      node.connect(this.gain);
      node.start(start, offsetSec);

      const streamStart = seg.streamStart + skipFrames * seg.contentPerFrame;
      this.spans.push({
        node,
        ctxStart: start,
        ctxEnd: start + ctxSec,
        streamStart,
        streamEnd: streamStart + contentSec,
      });
      this.nextStartTime = start + ctxSec;
      this.scheduledChunks++;
      this.consumeSegment(seg);
      this.pruneSpans();

      if (this.blocked) {
        this.blocked = false;
        this.onSchedulingResumed?.();
      }
      if (chainRestart && skipFrames > 0) {
        this.log(
          `anchor: skip ${(offsetSec * 1000).toFixed(0)}ms stale PCM ` +
            `(head behind visible by ${(skipCtxSec * 1000).toFixed(0)}ms)`,
        );
      }
    }

    if (skippedThisPump > 0) {
      this.log(`anchor: dropped ${skippedThisPump} fully-stale PCM chunk(s)`);
    }
  }

  /**
   * 取下一个待排程片段（**不消费**，确认排程后由 `consumeSegment` 摘除）。
   *  - 未注入伸缩器：直接取队头 chunk（1:1，交给节点按视频速率播放）；
   *  - 注入伸缩器：先把队头 chunk 喂进伸缩器，再取一段已产出的输出（节点速率恒 1）。
   * 返回 null 表示暂时无可排程内容（等输入 / 等 52ms 预填）。
   */
  private takeSegment(aheadSec: number, nodeRate: number): Segment | null {
    if (!this.stretchEnabled) {
      while (this.queue.length > 0) {
        const chunk = this.queue[0];
        const frames = Math.floor(chunk.pcm.length / chunk.channels);
        if (frames > 0 && chunk.durationSec > 0) {
          return {
            pcm: chunk.pcm,
            channels: chunk.channels,
            sampleRate: chunk.sampleRate,
            frames,
            contentPerFrame: 1 / chunk.sampleRate,
            streamStart: chunk.timeSec,
            nodeRate,
            source: "direct",
          };
        }
        this.queue.shift();
      }
      return null;
    }

    if (this.outSegs.length === 0) {
      this.produceStretched(aheadSec);
    }
    return this.outSegs[0] ?? null;
  }

  private consumeSegment(seg: Segment): void {
    if (seg.source === "direct") {
      this.queue.shift();
    } else if (this.outSegs[0] === seg) {
      this.outSegs.shift();
    }
  }

  // ==================== WSOLA 伸缩级 ====================

  /**
   * 两环速率策略（时间尺度必须分离，否则两环互相追逐）：
   *  - **慢环 = 视频速率**：必须立刻跟随 live-sync 的 `video.playbackRate`。音频不跟，
   *    内容就会以 `r − R` 的速率持续跑偏，任何微调都救不回来。
   *  - **快环 = 残余漂移**：在 ±`RATIO_TRIM` 带内无感修正，并限幅变化率避免金属声。
   */
  private updateStretcherRatio(videoRate: number): void {
    const drift = this.driftSec();
    const now = performance.now();
    const dt =
      this.lastRatioUpdateMs === 0 ? Number.POSITIVE_INFINITY : Math.max(0.001, (now - this.lastRatioUpdateMs) / 1000);
    this.lastRatioUpdateMs = now;

    const wantedTrim =
      drift === null || Math.abs(drift) < RATIO_DEAD_ZONE_SEC
        ? 0
        : Math.max(-RATIO_TRIM, Math.min(RATIO_TRIM, -drift * RATIO_DRIFT_GAIN));
    const step = wantedTrim - this.ratioTrim;
    const maxStep = RATIO_SLEW_PER_SEC * dt;
    this.ratioTrim += Number.isFinite(maxStep) ? Math.max(-maxStep, Math.min(maxStep, step)) : step;

    const ratio = Math.min(2, Math.max(0.5, videoRate * (1 + this.ratioTrim)));
    this.currentRatio = ratio;
    this.stretcher?.setRatio(ratio);
  }

  /** 把队列里的输入喂进伸缩器，直到攒够 `aheadSec` 的输出（或输入不够）。 */
  private produceStretched(aheadSec: number): void {
    const head = this.queue[0];
    if (!head) return;
    const stretcher = this.ensureStretcher(head.sampleRate, head.channels);
    if (!stretcher) {
      // 创建失败时立即切回直通（`ensureStretcher` 里置的 stretcherFailed 会被下一个
      // takeSegment 看到），这里主动再 pump 一次，避免白白空转一个 tick。
      if (this.stretcherFailed) this.pump();
      return;
    }

    if (this.fedBaseMediaSec === null) {
      // 新会话 / 重锚后的第一块输入：`position` 从 0 开始，据此种入内容基准。
      this.fedBaseMediaSec = head.timeSec;
      this.fedMediaEndSec = head.timeSec;
      this.outCursorInFrames = 0;
    }

    const pendingSec = this.pendingOutputSec();
    if (pendingSec >= aheadSec) return;
    // 需要的输入 ≈ 还差的输出 × 速率，再加一个合成周期的预填余量
    const targetMediaSec = this.fedMediaEndSec + (aheadSec - pendingSec) * this.currentRatio + STRETCHER_PREFILL_SEC;
    this.feedUpTo(targetMediaSec, head.sampleRate, head.channels);
  }

  /** 惰性创建伸缩器；参数变化时重建（旧实例作废）。返回 null 表示"还没就绪"。 */
  private ensureStretcher(sampleRate: number, channels: number): AudioStretcher | null {
    if (this.stretcher && this.stretcherSampleRate === sampleRate && this.stretcherChannels === channels) {
      return this.stretcher;
    }
    if (this.stretcherInit || this.stretcherFailed) return null;
    const factory = this.stretcherFactory;
    if (!factory) return null;

    this.stretcher?.destroy();
    this.stretcher = null;
    this.resetStretcher();
    this.stretcherInit = factory(sampleRate, channels)
      .then((created) => {
        this.stretcherInit = null;
        if (!created || this.destroyed) {
          created?.destroy();
          return;
        }
        this.stretcher = created;
        this.stretcherSampleRate = sampleRate;
        this.stretcherChannels = channels;
        created.setRatio(this.currentRatio);
        this.log(`stretcher ready: ${sampleRate}Hz/${channels}ch, ratio=${this.currentRatio.toFixed(3)}`);
        if (!this.destroyed) {
          this.pump();
        }
      })
      .catch((error) => {
        this.stretcherInit = null;
        // 一次失败即永久退回"链式 1:1"（= 历史行为），避免每次 pump 都重试刷屏。
        this.stretcherFailed = true;
        this.log(`stretcher unavailable, falling back to the 1:1 chain: ${String(error)}`);
        // 立刻重排一次，让已入队的样本马上走直通路径，不等下一个控制 tick。
        if (!this.destroyed) {
          this.pump();
        }
      });
    return null;
  }

  /**
   * 把输入喂到 `targetMediaSec`：缺口补静音、小重叠裁掉、大倒退则重置。
   *
   * 注意：这里的正向跳变**必须补静音、不能桥接**。AC-3 走分段重锚（worker 每个分片
   * 把音频首块钉回视频段起点），段边界的标签前跳是刻意的 A/V 同步修正 —— 桥接会把
   * 修正丢掉，音频相对视频持续漂移（音画不同步）。MP2 muxed 路径的校准伪影跳变
   * 会经由此产生排程 blocked，由 pcm-audio-player 的轴平移自愈兜底。
   */
  private feedUpTo(targetMediaSec: number, sampleRate: number, channels: number): void {
    const stretcher = this.stretcher;
    if (!stretcher) return;
    let guard = 0;
    while (this.queue.length > 0 && this.fedMediaEndSec < targetMediaSec && guard++ < 1024) {
      const chunk = this.queue[0];
      if (chunk.sampleRate !== sampleRate || chunk.channels !== channels) return;
      const cursor = this.fedMediaEndSec;

      if (chunk.timeSec > cursor + 0.001) {
        // 时间轴缺口：这段时间轴真的没有音频 → 补静音。补静音同时让「position ↔ 媒体
        // 时间」的映射保持线性；否则缺口会被伸缩器当作连续输入压掉，标签就全歪了。
        const gapSec = Math.min(chunk.timeSec - cursor, targetMediaSec - cursor);
        const gapFrames = Math.max(1, Math.round(gapSec * sampleRate));
        this.publishOutput(stretcher.process(this.silenceBuffer(gapFrames, channels)), channels, sampleRate);
        this.fedMediaEndSec = cursor + gapFrames / sampleRate;
        continue;
      }

      const overlapSec = cursor - chunk.timeSec;
      if (overlapSec >= chunk.durationSec) {
        // 整块内容已播过：整块丢弃（不能喂空 buffer，会把游标虚推一段时长）。
        this.queue.shift();
        continue;
      }
      if (overlapSec > MAX_INPUT_OVERLAP_SEC) {
        // 时间轴大倒退：伸缩器"输入流连续"的前提被破坏，重置并按这一块重新种基准。
        this.log(`input timeline jumped backwards ${(overlapSec * 1000).toFixed(0)}ms; resetting the stretcher`);
        this.resetStretcher();
        this.fedBaseMediaSec = chunk.timeSec;
        this.fedMediaEndSec = chunk.timeSec;
        this.outCursorInFrames = 0;
        continue;
      }

      this.queue.shift();
      const trimmed = overlapSec > 0.0005 ? this.trimLeading(chunk, overlapSec) : chunk.pcm;
      this.publishOutput(stretcher.process(trimmed), channels, sampleRate);
      this.fedMediaEndSec = Math.max(cursor, chunk.timeSec + chunk.durationSec);
    }
  }

  /** 裁掉一块输入里与已喂入内容重叠的前缀（交错 PCM 的 subarray，无拷贝）。 */
  private trimLeading(chunk: AudioSyncChunk, skipSec: number): Float32Array {
    const totalFrames = Math.floor(chunk.pcm.length / chunk.channels);
    const skipFrames = Math.min(Math.max(0, Math.round(skipSec * chunk.sampleRate)), totalFrames);
    return skipFrames > 0 ? chunk.pcm.subarray(skipFrames * chunk.channels) : chunk.pcm;
  }

  /**
   * 收下一段伸缩器输出，按 `position` 增量算出它的内容跨度。
   * 这是记账的唯一真源：**不用名义 ratio 推算**，因此与 ratio 变化无关。
   */
  private publishOutput(out: Float32Array, channels: number, sampleRate: number): void {
    const stretcher = this.stretcher;
    if (!stretcher || out.length === 0) return;
    const frames = Math.floor(out.length / channels);
    if (frames <= 0) return;

    const posAfter = stretcher.position;
    const streamStart = this.mediaForInputFrames(this.outCursorInFrames);
    const streamEnd = this.mediaForInputFrames(posAfter);
    this.outSegs.push({
      pcm: out,
      channels,
      sampleRate,
      frames,
      contentPerFrame: (streamEnd - streamStart) / frames,
      streamStart,
      nodeRate: 1,
      source: "stretched",
    });
    this.outCursorInFrames = posAfter;
  }

  /** `position`（输入帧）→ 媒体时间。前提：喂入的输入在媒体时间轴上连续（缺口已补静音）。 */
  private mediaForInputFrames(inputFrames: number): number {
    const base = this.fedBaseMediaSec ?? 0;
    const sampleRate = this.stretcherSampleRate;
    return sampleRate > 0 ? base + inputFrames / sampleRate : base;
  }

  private pendingOutputSec(): number {
    let seconds = 0;
    for (const seg of this.outSegs) {
      seconds += seg.frames / seg.sampleRate;
    }
    return seconds;
  }

  private silenceBuffer(frames: number, channels: number): Float32Array {
    return new Float32Array(frames * channels);
  }

  /**
   * 丢弃伸缩器内部状态并清空"已产出未排程"的输出。重锚 / seek / 参数变化后必须调用：
   * `wsola_reset()` 会把 `position` 清零，故 `fedBaseMediaSec` 必须由下一块输入重新种入。
   */
  private resetStretcher(): void {
    this.stretcher?.reset();
    this.fedBaseMediaSec = null;
    this.fedMediaEndSec = 0;
    this.outCursorInFrames = 0;
    this.outSegs = [];
    this.ratioTrim = 0;
    this.lastRatioUpdateMs = 0;
  }

  destroy(): void {
    this.destroyed = true;
    this.stretcher?.destroy();
    this.stretcher = null;
    if (this.probeHandle && typeof this.video.cancelVideoFrameCallback === "function") {
      this.video.cancelVideoFrameCallback(this.probeHandle);
    }
    this.probeHandle = 0;
    this.stopSpans();
    this.queue = [];
    this.gain.disconnect();
  }

  // ==================== internal ====================

  private stopSpans(): void {
    const now = this.ctx.currentTime;
    for (const span of this.spans) {
      try {
        span.node.stop(now);
        span.node.disconnect();
      } catch {
        /* already stopped */
      }
    }
    this.spans = [];
    this.nextStartTime = 0;
  }

  private pruneSpans(): void {
    const now = this.ctx.currentTime;
    while (this.spans.length > 0 && this.spans[0].ctxEnd < now - 0.5) {
      try {
        this.spans[0].node.disconnect();
      } catch {
        /* ignore */
      }
      this.spans.shift();
    }
  }

  private applyFadeIn(buffer: AudioBuffer, startFrame = 0): void {
    const fadeFrames = Math.min(Math.floor(FADE_SEC * buffer.sampleRate), buffer.length - startFrame);
    if (fadeFrames <= 0) return;
    for (let ch = 0; ch < buffer.numberOfChannels; ch++) {
      const data = buffer.getChannelData(ch);
      for (let i = 0; i < fadeFrames; i++) {
        data[startFrame + i] *= (i + 1) / fadeFrames;
      }
    }
  }

  /**
   * rVFC 采样："解码时钟领先屏幕帧"的量。rVFC 在帧送达合成器时给出它的 mediaTime，
   * 于是 `currentTime − mediaTime` 就是视频显示延迟。负值（seek/teleport）与离谱值丢弃。
   */
  private samplePresentationLead(metadata: VideoFrameCallbackMetadata): void {
    const mediaTime = metadata.mediaTime;
    if (!Number.isFinite(mediaTime)) return;
    const lead = this.video.currentTime - mediaTime;
    if (!Number.isFinite(lead) || lead < 0 || lead > MAX_VIDEO_PRESENTATION_LATENCY_SEC) return;

    this.probeSamples++;
    this.probeEma = Number.isNaN(this.probeEma) ? lead : this.probeEma + VIDEO_LEAD_EMA_ALPHA * (lead - this.probeEma);
    if (this.probeSamples >= VIDEO_LEAD_MIN_SAMPLES) {
      this.displayLeadSec = this.probeEma;
    }
  }

  private log(message: string): void {
    this.onLog?.(message);
  }
}
