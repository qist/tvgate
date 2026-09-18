/**
 * fMP4 remuxer。
 * 依据 ISO/IEC 14496-12 公开规范重新实现，把 demux 样本聚合成 MSE 可用的 fMP4 片段。
 * 行为（见引擎设计 §5.5）：按批聚合（时长/字节阈值 + 关键帧边界）→ 由 mp4-generator 生成
 * init(ftyp+moov) 与 media(moof+mdat) 段；media 段携带 timestampOffset。
 */

import { generateInitSegment, generateMediaSegment, type Fmp4TrackInit } from "../formats/mp4-generator";
import { buildEsds } from "../formats/aac";
import { AAC_SAMPLE_RATES, getSilentFrame } from "./aac-silent";

export interface RemuxTrackConfig extends Fmp4TrackInit {}

export interface RemuxInputSample {
  trackId: number;
  data: Uint8Array;
  dts: number; // 单位：该轨 timescale
  cts: number; // pts - dts
  isKeyframe: boolean;
}

export interface InitSegmentPayload {
  codec: string; // 单轨 codec，如 "avc1.4d401f" / "mp4a.40.2"
  container: string;
  data: Uint8Array;
  kind: "video" | "audio"; // 对应独立 SourceBuffer 的 track 名
}

export interface MediaSegmentPayload {
  data: Uint8Array;
  timestampOffset: number; // 秒；默认 0（tfdt 已写绝对 dts）
  startDts: number;
  duration: number; // 秒（按该轨 timescale 折算）
  kind: "video" | "audio";
  trackIds: number[];
}

export interface RemuxerCallbacks {
  onInitSegment?(segment: InitSegmentPayload): void;
  onMediaSegment?(segment: MediaSegmentPayload): void;
}

interface QueuedSample extends RemuxInputSample {}

export interface Fmp4RemuxerOptions {
  /** 单段目标时长（秒），到达即成段。 */
  targetDuration?: number;
  /** 单段字节上限，超过即成段。 */
  maxBytes?: number;
  /** 起播宽限期（毫秒），用于等待迟到音轨建完 SourceBuffer。 */
  startupGraceMs?: number;
}

/**
 * 成段阈值（250ms / 512KB）。
 * 曾用 2s / 2MB——首个 media 段要攒满 2 秒视频才发出，起播显著变慢、
 * 直播追边也恒定落后约 2 秒，是「播放起不来/起播慢」的主因之一。
 */
const DEFAULT_TARGET_DURATION = 0.25;
const DEFAULT_MAX_BYTES = 512 * 1024;
/**
 * 起播宽限期（毫秒）：首个 media 段必须晚于所有 SourceBuffer 创建。
 * Chromium 在媒体引擎初始化（首个 media 段 append）之后调用 addSourceBuffer 会抛
 * QuotaExceededError（"has reached the limit of SourceBuffer objects"）。
 * 正常 A/V 流音频首帧随视频交织、几十毫秒内即注册，门控走「两轨 init 齐备」立即放行，不受此值影响；
 * 此宽限只兜底「音频轨迟迟不注册」的流（如音频晚到数个分片、纯视频流），到点仍须放行以免卡死。
 * 曾设 1500ms，被"音频晚于首段 video flush"的流击穿（addSourceBuffer 报已达上限）；提到 8000ms。
 */
const DEFAULT_STARTUP_GRACE_MS = 8000;
/** 外部基准（视频基准）最长等待（ms）：超时则音频用自身基准放行，避免音轨被拖死。 */
const EXTERNAL_BASE_MAX_WAIT_MS = 1500;

export class Fmp4Remuxer {
  private readonly targetDuration: number;
  private readonly maxBytes: number;
  private readonly trackConfigs = new Map<number, RemuxTrackConfig>();
  private readonly queues = new Map<number, QueuedSample[]>();
  private readonly lastDuration = new Map<number, number>();
  /** 每个 kind 首个样本的媒体时间（秒），用于确定统一时间基。 */
  private readonly firstSampleSec = new Map<string, number>();
  /** 是否已发出过视频段：首个段必须关键帧开头，其后都是同一队列的顺序续切。 */
  private videoStarted = false;
  /**
   * **统一**时间基（秒）：所有轨共用，优先取首个视频样本，无视频时取首个音频样本。
   * 视频锚点优先且音频共用同一锚点；若按 kind 各自归零，
   * 会抹掉 (audioFirstPts − videoFirstPts) 的固定偏移 → 音画恒定错位。
   */
  private unifiedBaseSec: number | null = null;
  /** 已发射 init 的轨 id（每轨独立 SourceBuffer，各发各的 init）。 */
  private readonly emittedInit = new Set<number>();
  /** 首个 init 发射时刻，用于起播宽限判定；null 表示尚未发射任何 init。 */
  private firstInitAt: number | null = null;
  private readonly startupGraceMs: number;
  /** PMT 是否声明了进 MSE 的 AAC 音轨：null=未知；false=纯视频（首个 init 即可放行）；true=须等音轨 init。 */
  private audioExpected: boolean | null = null;
  /**
   * PMT 是否声明了视频轨（来自 onStreamLayout，早于任何样本）：
   * 统一时间基**只认视频**，所以音频先到也不能抢锁（见 flush 的加锁段）。
   * 音频轨的 init 常先于视频轨发出（音频首帧先解析出 esds），只按"是否已注册视频轨"判断，
   * 会在视频轨注册之前让音频把基准锁成自己的首个样本 —— 实测视频时间轴因此整体后移 7.6s。
   */
  private videoExpected = true;
  /** 外部时间基准（见 setExternalBase）；null = 用本流水线首样本。 */
  private externalBaseSec: number | null = null;
  /** 基准待定模式（见 awaitExternalBase）：首个 media 段等外部基准（或超时放行）。 */
  private awaitingExternalBase = false;
  /**
   * 静音 AAC 假音轨 id（C2）：muxed TS 的软解音轨（AC-3/E-AC-3/MP2）给 MSE 挂一条占位音频轨，
   * 使 video 元素"有音轨"，后台标签页不被 UA 判为无音频播放而冻结。null = 未启用（纯视频 MSE）。
   */
  private silentAudioTrackId: number | null = null;
  private silentAudioSampleRate = 48000;
  private silentAudioUnit: Uint8Array | null = null;
  /** 静音帧时间轴续接状态（ms 域）与时长取整残差（跨段累积，避免长期漂移）。 */
  private silentAudioLastDtsMs: number | null = null;
  private silentAudioDurationResidual = 0;

  constructor(
    private readonly callbacks: RemuxerCallbacks = {},
    options: Fmp4RemuxerOptions = {},
  ) {
    this.targetDuration = options.targetDuration ?? DEFAULT_TARGET_DURATION;
    this.maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
    this.startupGraceMs = options.startupGraceMs ?? DEFAULT_STARTUP_GRACE_MS;
  }

  addTrack(config: RemuxTrackConfig): void {
    if (this.trackConfigs.has(config.id)) return;
    this.trackConfigs.set(config.id, config);
    if (!this.queues.has(config.id)) this.queues.set(config.id, []);
    // demuxer 仅在 codecPrivate（avcC/esds）就绪后 via onTracks 上报该轨，
    // 故此处 config 已携带合法 codecPrivate。每轨拥有独立 SourceBuffer，
    // 轨就绪即发射其 init，无需等齐所有轨（避免音频首帧晚到而错过首段 flush，
    // 导致音频 init 永不发射、media 被后端静默丢弃）。
    this.emitInitIfNeeded();
  }

  addSample(sample: RemuxInputSample): void {
    if (!this.trackConfigs.has(sample.trackId)) return;
    let q = this.queues.get(sample.trackId);
    if (!q) {
      q = [];
      this.queues.set(sample.trackId, q);
    }
    q.push(sample);
    // 记录该 kind 首个样本的媒体时间（秒），供统一时间基择基准。
    // **只记音频**：视频的基准必须是「首个实际发得出去的视频样本」= 首个关键帧（见 flush 的视频分支）——
    // 首个到达的视频样本可能落在第一个 IDR 之前、永远不会被成段，拿它当基准会让视频时间轴
    // 凭空往后挪数秒（实测 7.8s），播放头停在空洞前，只能靠停摆看门狗跳进去才出画。
    const cfg = this.trackConfigs.get(sample.trackId);
    if (cfg && cfg.kind === "audio" && !this.firstSampleSec.has(cfg.kind)) {
      this.firstSampleSec.set(cfg.kind, sample.dts / (cfg.timescale || 1));
    }
    // 时长达标、或驱动轨出现「下个关键帧」（GOP 边界）时成段。后者确保视频段以关键帧开头。
    if (this.isBatchReady() || this.driverHasKeyframeBoundary()) this.flush();
  }

  /** 成段条件：驱动轨（优先视频）时长达标，或累计字节超限。 */
  private isBatchReady(): boolean {
    const driver = this.driverTrackId();
    if (driver === undefined) return false;
    const q = this.queues.get(driver);
    if (!q || q.length === 0) return false;
    const cfg = this.trackConfigs.get(driver)!;
    return this.batchReadyForQueue(q, cfg.timescale || 1);
  }

  /** 驱动轨队列中是否出现了「下一个关键帧」（index>0），用于 GOP 对齐成段。 */
  private driverHasKeyframeBoundary(): boolean {
    const driver = this.driverTrackId();
    if (driver === undefined) return false;
    const q = this.queues.get(driver);
    return q ? firstKeyframeIndex(q) !== undefined : false;
  }

  /**
   * 选本次要发出的视频段；undefined = 还不到切的时候（继续攒）。
   *
   * ① 首个视频段必须「以关键帧开头」：起播/换源/seek 的第一批数据常从 GOP 中间开始
   *    （分片边界与 IDR 不对齐），这些样本缺参考帧、解不出来；更不能当"当前 GOP 前缀"
   *    整段发出去 —— 那会造出一个非关键帧开头的首段（实测被浏览器拒收/花屏），并把视频
   *    时间轴起点推到数秒之后（播放头卡在空洞前，只能等停摆看门狗跳进来才出画）。
   *    故先丢掉前缀，等关键帧到位再切。
   * ② 之后按目标时长/字节继续切（**不必等下一个关键帧**）：等 IDR 意味着 10s GOP 的源上
   *    首帧要等到第二个分片（实测"下载完两个分片才开播"）、直播延迟也被 GOP 长度绑架。
   *    续切段可以落在 GOP 中间 —— 解码器已经在跑，顺序续切即可（LL-HLS/CMAF 分片同理）。
   * ③ 无周期 IDR 的流由安全上限兜底整发，避免队列无限堆积。
   */
  private selectVideoRun(
    q: QueuedSample[],
    ts: number,
    force: boolean,
  ): { runSamples: QueuedSample[]; consume: number } | undefined {
    const totalDur = q[q.length - 1].dts - q[0].dts;
    const safetyMax = this.targetDuration * ts * 50;
    if (!this.videoStarted && !q[0].isKeyframe) {
      const firstKey = keyframeIndexFrom(q, 0);
      if (firstKey > 0) {
        q.splice(0, firstKey); // 丢弃不可解码的前缀：后面从关键帧开头续切
      } else if (firstKey < 0 && (force || totalDur >= safetyMax)) {
        this.markVideoStarted(q[0], ts);
        return { runSamples: q.slice(), consume: q.length };
      } else {
        return undefined; // 还没等到关键帧：继续攒
      }
    }
    const cut = this.videoCutIndex(q, ts, force);
    if (cut !== undefined) {
      this.markVideoStarted(q[0], ts);
      return { runSamples: q.slice(0, cut), consume: cut };
    }
    if (force || totalDur >= safetyMax) {
      this.markVideoStarted(q[0], ts);
      return { runSamples: q.slice(), consume: q.length };
    }
    return undefined;
  }

  /** 记录视频轨首个"发得出去"的样本时间：统一时间基（= 首个发出的视频样本）就用它。 */
  private markVideoStarted(first: QueuedSample, ts: number): void {
    this.videoStarted = true;
    if (!this.firstSampleSec.has("video")) this.firstSampleSec.set("video", first.dts / (ts || 1));
  }

  /**
   * 视频切点：返回本段应消耗的样本数（undefined = 还不到切的时候）。
   * 关键帧边界优先（短 GOP 源的成段行为与旧实现一致：一段 = 一个 GOP）；没有关键帧可等时
   * 按目标时长/字节切在任意样本边界 —— 首段以关键帧开头（解码入口）之后，后续段只是同一
   * 队列的顺序续切：解码时间戳连续（buildRun 给末样本取上一段时长，下一段恰从该处接着），
   * 浏览器按 moof 顺序续解码，不需要每个段都以 IDR 开头（LL-HLS/CMAF 分片同理）。
   */
  private videoCutIndex(q: QueuedSample[], ts: number, force: boolean): number | undefined {
    const minDur = this.targetDuration * ts;
    let bytes = 0;
    for (let i = 1; i < q.length; i++) {
      bytes += q[i - 1].data.length;
      if (q[i].isKeyframe) return i; // GOP 边界：优先切
      if (force || q[i - 1].dts - q[0].dts >= minDur || bytes >= this.maxBytes) return i;
    }
    return undefined;
  }

  /** PMT 是否声明了视频轨（早于任何样本）：统一时间基只等视频锚定（见 videoExpected）。 */
  setVideoExpected(hasVideo: boolean): void {
    this.videoExpected = hasVideo;
  }

  /** 队列成段条件：该轨累计时长达标，或全局累计字节超限。 */
  private batchReadyForQueue(q: QueuedSample[], ts: number): boolean {
    if (q.length === 0) return false;
    const duration = q[q.length - 1].dts - q[0].dts;
    if (duration >= this.targetDuration * ts) return true;
    let bytes = 0;
    for (const list of this.queues.values()) {
      for (const s of list) bytes += s.data.length;
    }
    return bytes >= this.maxBytes;
  }

  private driverTrackId(): number | undefined {
    for (const cfg of this.trackConfigs.values()) {
      if (cfg.kind === "video") return cfg.id;
    }
    const first = this.trackConfigs.values().next();
    return first.done ? undefined : first.value.id;
  }

  /** 为所有已注册且 codecPrivate 就绪的轨发射 init（每轨独立 SourceBuffer）。 */
  private emitInitIfNeeded(): void {
    for (const c of this.trackConfigs.values()) {
      if (this.emittedInit.has(c.id)) continue;
      // 视频需 avcC、音频需 esds 才能生成合法 init；未就绪则跳过，等其就绪后再发射
      if (c.kind === "video" && c.codecPrivate.length === 0) continue;
      if (c.kind === "audio" && c.codecPrivate.length === 0) continue;
      if (this.firstInitAt === null) this.firstInitAt = Date.now();
      this.callbacks.onInitSegment?.({
        codec: c.codec,
        // 音频轨必须声明 audio/mp4 容器（此前两轨都写 video/mp4，mime 语义错误）
        container: c.kind === "audio" ? "audio/mp4" : "video/mp4",
        data: generateInitSegment([c]),
        kind: c.kind,
      });
      this.emittedInit.add(c.id);
    }
  }

  /**
   * 挂一条**静音 AAC 假音轨**（C2）：软解音频的真实声音走 WebAudio（PCMAudioPlayer），
   * MSE 里只留一条静音 AAC —— 让 video 元素"有音轨"，后台标签页不被 UA 判为"无音频播放"
   * 而冻结（后台音画不同步的根因）。静音帧按视频时间戳生成（见 `emitSilentAudioForVideoRun`）。
   *
   * 无对应声道数的静音帧时不挂（保持纯视频，退化为旧行为）。
   */
  setSilentAudioTrack(config: { id: number; sampleRate: number; channels: number }): void {
    if (this.silentAudioTrackId !== null) return;
    const sampleRate = config.sampleRate > 0 ? config.sampleRate : 48000;
    const channels = config.channels > 0 ? config.channels : 2;
    const unit = getSilentFrame("mp4a.40.2", channels);
    if (!unit) return;
    this.silentAudioTrackId = config.id;
    this.silentAudioSampleRate = sampleRate;
    this.silentAudioUnit = unit;
    // AAC-LC AudioSpecificConfig：objectType=2(LC) + 采样率索引 + 声道配置
    const idx = AAC_SAMPLE_RATES.indexOf(sampleRate);
    const fi = idx >= 0 ? idx : 3; // 缺省 48000
    const asc = new Uint8Array([(2 << 3) | ((fi & 0x0f) >>> 1), ((fi & 0x01) << 7) | ((channels & 0x0f) << 3)]);
    this.addTrack({
      id: config.id,
      kind: "audio",
      codec: "mp4a.40.2",
      timescale: 1000,
      sampleRate,
      channels,
      codecPrivate: buildEsds(asc),
    });
  }

  /** PMT 已声明无 AAC 音轨（纯视频）时，首个 video init 即可放行。 */
  setAudioExpected(hasMseAudio: boolean): void {

    this.audioExpected = hasMseAudio;
  }

  /**
   * 外部时间基准（声画分流的音频流水线专用）：把本流水线的样本时间轴锚到**视频的基准**上。
   *
   * 两条独立流水线各自「减自己的首样本 dts」会让音频失去与视频的对应：视频首样本的
   * 显示时间 = cts（B 帧重排序延迟，实测 ~40ms），音频首样本 = 0 —— 听感就是
   * **声音比画面快 ~40~51ms**（实测声画不同步的原因）。把音频基准改为视频基准后，
   * 音频首样本输出 = 音频绝对时间 - 视频基准（实测 +51ms）≈ 与视频对齐（残差 ~11ms，
   * 为两流片首的物理差，属正确值）。
   *
   * 必须在首个 flush（锁定 unifiedBaseSec）之前调用；已锁定后调用无效。
   */
  setExternalBase(baseSec: number): void {
    if (this.unifiedBaseSec !== null) return;
    this.externalBaseSec = baseSec;
    // 等待期可能已攒下样本（门控拦住未发）：基准就绪后立即尝试成段
    if (this.awaitingExternalBase) this.flush();
  }

  /**
   * 标记"外部基准待定"（声画分流音频流水线）：**init 照常发射**（hold/起播门不受影响），
   * 但首个 media 段推迟到 setExternalBase 到达或超时——避免为了拿视频基准而拖慢音频链
   * 启动（音频 init 本来与 base 无关，先发能让 hold 立刻满足、视频秒起播）。
   */
  awaitExternalBase(): void {
    this.awaitingExternalBase = true;
  }

  /** 首个样本的绝对秒（kind: "video" | "audio"）；未收到该轨样本时为 null。 */
  getFirstSampleSec(kind: "video" | "audio"): number | null {
    const v = this.firstSampleSec.get(kind);
    return v === undefined ? null : v;
  }

  /**
   * 起播门控：首个 media 段是否允许发出。
   * Chromium 一旦 append 首个 init 便锁死 SourceBuffer 数量（后续 addSourceBuffer 抛
   * QuotaExceededError），故 media 必须晚于所有 SourceBuffer 创建。纯视频（PMT 无 AAC）
   * 首个 video init 后即放行；AAC 流须等音频轨 init 齐备；未知用宽限期兜底。
   */
  private mediaGateOpen(): boolean {
    if (this.firstInitAt === null) return false;
    const configs = [...this.trackConfigs.values()];
    const allInitEmitted = configs.length > 0 && configs.every((c) => this.emittedInit.has(c.id));
    const hasVideo = configs.some((c) => c.kind === "video");
    // 基准待定（声画分流音频流水线）：首个 media 段等视频基准到达，上限
    // EXTERNAL_BASE_MAX_WAIT_MS；init 不受此门影响（照常发射，hold 与起播门不被拖慢）。
    if (
      this.awaitingExternalBase &&
      this.externalBaseSec === null &&
      Date.now() - this.firstInitAt < EXTERNAL_BASE_MAX_WAIT_MS
    ) {
      return false;
    }
    // 纯音频轨（声画分流的独立音轨流水线）：无视频可等。门控的初衷是保证「首个 init
    // append 前所有 SourceBuffer 都已创建」，本流水线只产 audio SB，其 init 齐即已满足；
    // 若仍落到 8s 宽限期兜底，音频首段会被凭空压后 8 秒（实测"声音出来慢"的确切原因）。
    if (!hasVideo) return allInitEmitted;
    if (this.audioExpected === false) return hasVideo; // 纯视频：无需等音频
    const hasAudioInit = configs.some((c) => c.kind === "audio" && this.emittedInit.has(c.id));
    if (hasVideo && hasAudioInit && allInitEmitted) return true;
    return Date.now() - this.firstInitAt >= this.startupGraceMs; // 兜底（音频损坏/不来）
  }

  /** 把当前队列成段并回调。force=true 跳过起播门控（收尾/seek 时用，避免丢数据）。 */
  flush(force = false): void {
    // 确保已注册轨的 init 已发出（init 通过 addTrack / 本调用发射，各轨独立）
    this.emitInitIfNeeded();
    // 起播门控未开：保留样本继续攒，等各轨 SourceBuffer 建齐再发首个 media
    if (!force && !this.mediaGateOpen()) return;

    const runs: ReturnType<typeof buildRun>[] = [];
    /** 本次实际成段的轨 + 各自消耗的样本数（只清空已发射前缀；关键帧及之后留给下一段）。 */
    const consumeMap = new Map<number, number>();

    for (const [id, q] of this.queues) {
      if (q.length === 0) continue;
      const cfg = this.trackConfigs.get(id);
      if (!cfg) continue;
      if (!this.emittedInit.has(id)) {
        // 该轨 init 尚未就绪：跳过其 media（避免无 init 的非法 append），但**必须保留队列**，
        // 否则样本被永久丢弃造成缓冲空洞（播放头卡洞里 → 一直"加载中"）。
        continue;
      }
      // 统一时间基尚未锁定（首个视频段还没发出）却声明了视频：音频批次暂不发、保留队列，保 A/V 同步。
      // 判据用「PMT 声明了视频」而不只是「视频轨已注册」——音频轨常常先注册（音频首帧先出 esds），
      // 只看已注册会让音频先抢锁（见 videoExpected）。
      if (
        cfg.kind === "audio" &&
        this.unifiedBaseSec === null &&
        (this.videoExpected || [...this.trackConfigs.values()].some((c) => c.kind === "video"))
      ) {
        continue;
      }
      const ts = cfg.timescale || 1;
      let runSamples: QueuedSample[];
      let consume: number;
      if (cfg.kind === "video") {
        const picked = this.selectVideoRun(q, ts, force);
        if (!picked) continue;
        runSamples = picked.runSamples;
        consume = picked.consume;
      } else {
        if (force || this.batchReadyForQueue(q, ts)) {
          runSamples = q.slice();
          consume = q.length;
          // 统一时间基（= 首个发出的视频样本）之前的音频整段丢掉：它们的归一化时间是负的，
          // 浏览器会丢弃甚至拒收；声音从画面起点开始即可，A/V 仍同一坐标系。
          if (this.unifiedBaseSec !== null) {
            const baseDts = this.unifiedBaseSec * ts;
            if (runSamples[runSamples.length - 1].dts < baseDts) continue;
            while (runSamples.length > 1 && runSamples[0].dts < baseDts) runSamples.shift();
          }
        } else {
          continue;
        }
      }
      const run = buildRun(id, runSamples, this.lastDuration.get(id));
      this.lastDuration.set(id, runSamples[runSamples.length - 1].dts - runSamples[Math.max(0, runSamples.length - 2)].dts || 1);
      runs.push(run);
      consumeMap.set(id, consume);
    }
    if (runs.length === 0) return;

    // 只清空已发射的前缀；未发射的（如下一段的起始关键帧）保留，等下次成段
    for (const [id, consume] of consumeMap) {
      const list = this.queues.get(id);
      if (list) list.splice(0, consume);
    }

    // 每轨独立成段并交付（video / audio 各写入自己的 SourceBuffer）。
    // 时间轴归一化：巨大 PTS（如直播源的 ~37000s）若不处理，缓冲区间远离 0，播放器永远起不来。
    // 首个片段把 buffer.timestampOffset 置为 -base 秒数，使时间轴从 0 开始；后续片段保持同一 offset。
    // 统一时间基只锁定一次：必须以**首个实际发出的视频样本**（= 首个关键帧，见 markVideoStarted）
    // 为锚。若首个 flush 由音频驱动（音频轨先注册/先到样），不能退回音频基址锁定——否则视频 media
    // 时间被算成负/错位，后续所有视频分片被浏览器整体丢弃，缓冲只剩首个关键帧那一段，播放头卡在
    // 洞前被 recoverFromStall 跳过 → 起播「第一帧后卡一下」。
    // 同理也**不能**用「首个到达的视频样本」（可能落在第一个 IDR 之前、永远发不出去）：实测那条
    // 流的视频时间轴因此比音频晚 7.8s，画面要等停摆看门狗把播放头跳进空洞才出来。
    // 故：有视频轨时音频先于视频到达不锁定（音频批次暂留队列，且基准之前的音频整段丢弃，见上）；
    // 仅纯音频流才用首个音频样本。
    if (this.unifiedBaseSec === null) {
      if (this.externalBaseSec !== null) {
        // 声画分流的音频流水线：锚到视频基准（见 setExternalBase），
        // 保证音频输出时间 = 音频绝对时间 - 视频首样本 dts，与视频同一坐标系。
        this.unifiedBaseSec = this.externalBaseSec;
      } else {
        const videoBase = this.firstSampleSec.get("video");
        if (videoBase !== undefined) {
          this.unifiedBaseSec = videoBase;
        } else if (!this.videoExpected && ![...this.trackConfigs.values()].some((c) => c.kind === "video")) {
          // 只有"PMT 明确没有视频轨"时才允许音频自己锚定（纯音频流）
          const audioBase = this.firstSampleSec.get("audio");
          if (audioBase !== undefined) this.unifiedBaseSec = audioBase;
        }
      }
    }

    for (const r of runs) {
      const cfg = this.trackConfigs.get(r.trackId);
      if (!cfg) continue;
      const timescale = cfg.timescale || 1;
      let base = this.unifiedBaseSec;
      if (base === null) {
        // 兜底：尚无任何首样本记录（不应发生），退化为本段起点
        base = r.baseMediaDecodeTime / timescale;
        this.unifiedBaseSec = base;
      }
      const tsOff = -base;
      this.callbacks.onMediaSegment?.({
        data: generateMediaSegment([r]),
        timestampOffset: tsOff,
        startDts: r.baseMediaDecodeTime,
        duration: r.totalDuration / timescale,
        kind: cfg.kind,
        trackIds: [r.trackId],
      });
      // C2：视频段发出后，为其时间窗补一段静音 AAC（MSE 音轨与视频时间轴一致地推进）
      if (cfg.kind === "video" && this.silentAudioTrackId !== null) {
        this.emitSilentAudioForVideoRun(r, timescale, base);
      }
    }
  }

  /**
   * 为视频段的时间窗生成静音 AAC 段：dts 与视频同域（都经 `timestampOffset = -base` 归一化），
   * 帧长 = 1024/sampleRate 秒（带取整残差累积），跨段续接避免缝隙/重叠。
   */
  private emitSilentAudioForVideoRun(
    videoRun: { baseMediaDecodeTime: number; totalDuration: number },
    videoTimescale: number,
    baseSec: number,
  ): void {
    const unit = this.silentAudioUnit;
    const trackId = this.silentAudioTrackId;
    if (!unit || trackId === null) return;
    const cfg = this.trackConfigs.get(trackId);
    if (!cfg || !this.emittedInit.has(trackId)) return; // init 未就绪：本段跳过
    if (videoRun.totalDuration <= 0) return;

    const ts = videoTimescale || 1;
    const startSec = videoRun.baseMediaDecodeTime / ts;
    const endMs = Math.round((startSec + videoRun.totalDuration / ts) * 1000);
    const frameMs = (1024 / this.silentAudioSampleRate) * 1000;

    const firstDtsMs = this.silentAudioLastDtsMs ?? Math.round(startSec * 1000);
    let dtsMs = firstDtsMs;
    let residual = this.silentAudioDurationResidual;
    const samples: { duration: number; data: Uint8Array; isKeyframe: boolean; ctsOffset: number }[] = [];
    while (dtsMs < endMs) {
      const durationWithResidual = frameMs + residual;
      const duration = Math.max(1, Math.round(durationWithResidual));
      residual = durationWithResidual - duration;
      samples.push({ duration, data: unit, isKeyframe: true, ctsOffset: 0 });
      dtsMs += duration;
    }
    if (samples.length === 0) return;

    this.silentAudioLastDtsMs = dtsMs;
    this.silentAudioDurationResidual = residual;

    let total = 0;
    for (const sample of samples) total += sample.duration;
    this.callbacks.onMediaSegment?.({
      data: generateMediaSegment([{ trackId, baseMediaDecodeTime: firstDtsMs, samples }]),
      timestampOffset: -baseSec,
      startDts: firstDtsMs,
      duration: total / (cfg.timescale || 1000),
      kind: "audio",
      trackIds: [trackId],
    });
  }

  destroy(): void {
    this.silentAudioTrackId = null;
    this.silentAudioUnit = null;
    this.silentAudioLastDtsMs = null;
    this.silentAudioDurationResidual = 0;
    this.trackConfigs.clear();
    this.queues.clear();
    this.lastDuration.clear();
    this.firstSampleSec.clear();
    this.unifiedBaseSec = null;
    this.videoStarted = false;
    this.emittedInit.clear();
    this.firstInitAt = null;
  }
}

/** 返回队列中首个关键帧的下标（排除 index 0）；用于把视频段切在 GOP 边界、保证每段以关键帧开头。 */
function firstKeyframeIndex(q: QueuedSample[]): number | undefined {
  for (let i = 1; i < q.length; i++) {
    if (q[i].isKeyframe) return i;
  }
  return undefined;
}

/** 从 from 开始找关键帧下标；找不到返回 -1（首个视频段/换源时用来丢掉不可解码的前缀）。 */
function keyframeIndexFrom(q: QueuedSample[], from: number): number {
  for (let i = Math.max(0, from); i < q.length; i++) {
    if (q[i].isKeyframe) return i;
  }
  return -1;
}

function buildRun(trackId: number, q: QueuedSample[], fallbackDuration?: number) {
  // 末样本时长：先用"上一段留下的时距"，再退到本段最后一个时距，最后才给 1 tick。
  // 直接给 1 tick（旧行为）会在段尾留约一帧的空洞（下一段从"末样本 + 一帧"开始），
  // 浏览器把这 40ms 缺口当不连续，实测会让紧跟其后的续切段被整段丢弃（缓冲停在首段）。
  const lastDelta = q.length > 1 ? q[q.length - 1].dts - q[q.length - 2].dts : undefined;
  const tailDuration = fallbackDuration ?? lastDelta ?? 1;
  const samples = q.map((s, i) => {
    const next = q[i + 1];
    const duration = next ? next.dts - s.dts : tailDuration;
    return {
      duration: Math.max(0, duration),
      data: s.data,
      isKeyframe: s.isKeyframe,
      ctsOffset: s.cts,
    };
  });
  let totalDuration = 0;
  for (const s of samples) totalDuration += s.duration;
  return { trackId, baseMediaDecodeTime: q[0].dts, samples, totalDuration };
}
