/**
 * 转封装流水线。
 * 编排「加载 → demux → remux → 对外交付」，行为见引擎设计 §5.2：
 * 通过 PipelineCallbacks 向外交付 onInitSegment / onMediaSegment / onMediaInfo /
 * onLoadingComplete / onIOError / onDemuxError。
 * 分段源：continuous-live-ts（连续直播单流）与 static-ts-list（点播/回看的分段列表），
 * 二者统一为「顺序拉取 URL 列表并喂给同一个 demuxer」——直播即列表长度为 1 且不结束。
 */

import {
  FetchStreamLoader,
  IOController,
  LIVE_DATA_TIMEOUT_MS,
  type DataSource,
  type LoaderErrorInfo,
  type ResumeMode,
} from "../io/fetch-loader";
import { FlvDemuxer } from "../demux/flv-demuxer";
import { TsDemuxer, type TrackInfo, type TsDemuxerCallbacks } from "../demux/ts-demuxer";
import type { SegmentSource } from "../hls/segment-source";
import {
  Fmp4Remuxer,
  type InitSegmentPayload,
  type MediaSegmentPayload,
} from "../remux/fmp4-remuxer";

import type { SourceMode } from "../types";
import type { PlayerMediaInfo } from "../backends/types";

export interface SoftAudioChunk {
  trackId: number;
  codec: string;
  data: Uint8Array;
  /** 样本起始 MSE 媒体时间（秒）。 */
  time: number;
}

export interface PipelineCallbacks {
  onInitSegment?(segment: InitSegmentPayload): void;
  onMediaSegment?(segment: MediaSegmentPayload): void;
  /** PMT 声明的轨道布局：下游用于在任何 init append 前建齐 SourceBuffer / 判定纯视频。 */
  onStreamLayout?(layout: { video: boolean; mseAudio: boolean }): void;
  onMediaInfo?(info: PlayerMediaInfo): void;
  /** 需要软解的音频（ac3/eac3/mp2/mp3）原始样本。 */
  onSoftAudioData?(chunk: SoftAudioChunk): void;
  onLoadingComplete?(): void;
  onIOError?(info: LoaderErrorInfo): void;
  onDemuxError?(message: string): void;
}

export interface PipelineConfig {
  /** 待顺序拉取的分段/流 URL 列表；直播通常为 1 个不结束的 URL。 */
  urls: string[];
  /** 动态分段源（HLS）。提供时优先于 urls，可持续产出新分段。 */
  source?: SegmentSource;
  dataSource?: Omit<DataSource, "url">;
  sourceMode?: SourceMode;
  resumeMode?: ResumeMode;
  /** remux 成段阈值（秒 / 字节）。 */
  targetDuration?: number;
  maxBytes?: number;
  /** IO 缓冲聚合阈值（字节）。 */
  bufferThreshold?: number;
  /** 加载错误重试次数。 */
  maxRetries?: number;
  /**
   * 需要软解的音频 codec 集：这些音轨不进 MSE（MSE 无法解码），改为经 onSoftAudioData 透传。
   * 缺省覆盖 ac3/eac3/mp2/mp3（AAC 走 MSE 原生）。
   */
  softDecodeCodecs?: string[];
  /**
   * 分离音轨（EXT-X-MEDIA 独立音频 playlist / 声画分流 variant）：音频走独立通路。
   * 软解编码（ac3/mp2 等）走独立软解通路，base 钉到「音频首样本 dts」；
   * passthrough 编码（AAC 等）走 MSE 直通（本流水线作为第二条发布者）。
   */
  separateAudio?: boolean;
  /** 仅音频流水线：不向 MSE remuxer 加任何轨（不产生 MSE init/media），只走软解。 */
  audioOnly?: boolean;
  /**
   * 抑制本流水线的全部音频输出（软解与 MSE 均不发）。
   * 声画分流场景下主视频流水线必须置位：其 TS 内可能仍带与独立音轨同内容的音频，
   * 若不抑制，两路音频会撞进同一个 audio SourceBuffer（MSE 拒收）或叠音。
   */
  suppressAudio?: boolean;
}

const DEFAULT_MAX_RETRIES = 2;

/**
 * 缓冲领先上限（ms）：MSE 缓冲末端领先播放头超过该值就不继续拉流/解码（仅直播生效）。
 * 30s：分片稀疏/抖动的卡源下可撑 3 片，断流/慢源时卡顿频率降约 3 倍；代价是内存
 * （4K ~20Mbps 时 30s ≈ 75MB）与断流恢复后的追延迟时间。
 */
const LEAD_BUFFER_AHEAD_MS = 30000;
/** 主线程 clock 消息过期（ms）：切后台心跳被节流时视为时基过期，直接放行，绝不死锁。 */
const CLOCK_STALE_MS = 1000;
/** 缓冲领先门轮询间隔（ms）。 */
const LEAD_GATE_POLL_MS = 100;
/**
 * 领先门最长连续暂停（ms）：暂停超时立即放行一次拉取。
 * 领先门本意是「限内存」，但上游切小时/抖动窗口内长时间停拉会让缓冲被"饿死"
 * （实测整点会停拉 38 秒、播放中断）；强制周期性放行可保证缓冲持续前进。
 */
const LEAD_PAUSE_MAX_MS = 8000;
/**
 * 软解 PCM 轴"脱轴判定"边界（秒，相对播放头）：领先超过 AHEAD 必为轴分离（超出播放器
 * 最大缓冲领先量）；落后超过 BEHIND 说明 PCM 已跟不上播放头（会被当过期丢成静音）。
 * 界内一律保留时间轴（小幅抖动由源 PTS 连续语义吸收），只有真脱轴才重钉。
 */
const PCM_REANCHOR_AHEAD_SEC = 36;
const PCM_REANCHOR_BEHIND_SEC = 3;
/** 直播流自愈重连上限与指数退避（仅连续流直播源；4xx 除 429 为确定性失败，不重试）。 */
const MAX_LIVE_RELOADS = 5;
const RETRY_BASE_DELAY_MS = 500;
const RETRY_MAX_DELAY_MS = 8000;
/**
 * 分段源（HLS）分片加载的短重试次数：分片是「可替换」的——失败即由 start() 丢弃整批旧分段
 * 并刷新播放列表，改用新序列续播（原生 HLS 播放器的标准行为）。绝不在此长退避：
 * 否则整点切换（旧序列分片 404、新分片未就绪）时会逐个啃完旧分片、耗尽重试预算并上报播放错误。
 */
const SEGMENT_MAX_ATTEMPTS = 1;
/** 分片批次失效后、重新请求播放列表前的短等待（ms）。 */
const SEGMENT_REFRESH_DELAY_MS = 300;

/** 帧率估计窗口：至少 12 帧才出值，窗口滚动上限 40 帧（≈1.6s@25fps）。 */
const FPS_MIN_SAMPLES = 12;
const FPS_WINDOW_SIZE = 40;
/** 帧率变化小于该值不重新发布媒体信息（避免末位抖动导致徽标反复刷新）。 */
const FPS_PUBLISH_EPSILON = 0.05;

/**
 * 加载失败是否值得重试：网络异常(-1)/5xx/429 可重试；4xx（除 429）是确定性失败，
 * 重试只会拖慢上层按位置重建（VOD/回看尤其如此）。
 */
export function isRetryableIoError(code: number): boolean {
  return !(code >= 400 && code < 500 && code !== 429);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** 只把 update 中有值的字段并入 current（undefined 不覆盖已有值）。 */
function mergeDefined<T extends object>(current: T | undefined, update: T): T {
  const merged = { ...(current ?? {}) } as Record<string, unknown>;
  for (const [key, value] of Object.entries(update)) {
    if (value !== undefined) merged[key] = value;
  }
  return merged as T;
}

export class TransmuxPipeline {
  private loader: FetchStreamLoader | null = null;
  private readonly ioController: IOController;
  /**
   * 数据源解复用器：首块字节探测决定（FLV / MPEG-TS），两者输出协议同形。
   * 见 feedDemuxer()。HTTP-FLV（直播）与连续 TS 直链都走静态 URL 列表这条路径。
   */
  private demuxer: TsDemuxer | FlvDemuxer | null = null;
  /** 探测期暂存（首块不足 3 字节时等更多数据再判）。 */
  private demuxProbeBuffer: Uint8Array | null = null;
  private readonly demuxerCallbacks: TsDemuxerCallbacks;
  private readonly remuxer: Fmp4Remuxer;
  private stopped = false;
  private paused = false;
  private resumeWaiters: (() => void)[] = [];
  private readonly maxRetries: number;
  private readonly urls: string[];
  private readonly softDecodeCodecs: Set<string>;
  private readonly separateAudio: boolean;
  private readonly suppressAudio: boolean;
  private readonly audioOnly: boolean;
  private readonly trackCodecs = new Map<number, string>();
  private readonly trackTimescales = new Map<number, number>();
  /** 累积的媒体信息：音视频常分两批发布，必须按轨合并而非整体替换。 */
  private mediaInfo: PlayerMediaInfo = {};
  /** 视频 DTS 增量窗口（估计帧率用；见 noteVideoSample）。 */
  private videoDtsDeltas: number[] = [];
  private lastVideoDtsSec: number | null = null;
  private serializedMediaInfo = "{}";
  /**
   * MSE 视频轨时间轴基准（首个视频样本 dts → 秒）。软解音频（mp2/ac3…）不经 MSE，
   * 其 PCM time 必须减掉该基准才能与 video.currentTime（0 基准）同空间；
   * 否则直播巨大 PTS 会让 PCM 播放器漂移恒超阈值 → 反复硬重同步（卡顿）/对不上。
   */
  private pcmTimeBaseSec: number | null = null;
  /**
   * 基准未定前暂存的软解音频块。MP2/ac3 的 PES 常**先于视频样本**到达，此时基准未知；
   * 若直接用 `base=0` 归一化，会带上绝对 PTS（上万秒）→ PCM 排程与 0 基准的
   * video.currentTime 严重错位 → 无声。故先暂存，待首个视频样本确定基准后统一补发。
   */
  private pendingSoftAudio: {
    trackId: number;
    codec: string;
    data: Uint8Array;
    dts: number;
    timescale: number;
  }[] = [];
  /** 暂存上限：纯音频流（无视频样本可定基准）时以最早一块的 dts 兜底锚定。 */
  private readonly maxPendingSoftAudio = 120;
  /** 主线程 250ms 上报的播放头（ms）；-1 = 未知。缓冲领先门据此限速。 */
  private playheadCurrentMs = -1;
  /** 主线程上报的 MSE 缓冲末端（ms）；-1 = 未知。 */
  private playheadBufferedEndMs = -1;
  /** 最近一次 clock 到达时刻（performance.now()）；时基过期即放行，避免死锁。 */
  private lastClockArrivalMs = -1;
  /** 页面隐藏：缓冲领先门无条件放行（后台播放以音频 free-run 为主导）。 */
  private pageHidden = false;
  /** 最近一块软解音频的输出时间（秒）；C9c 脱轴判定用（含 0 也要区分"未产出过"）。 */
  private lastSoftAudioTimeSec: number | null = null;
  /**
   * 主音轨 = PMT 声明顺序里的第一路音频轨。多音轨流（如 AC-3 主轨 + MP2 备轨）里，
   * "播哪条声音"与"徽标显示哪个编码"都必须以它为准：轨道是多批发布的，若每次取
   * "本批第一条音频"，后发布的备轨会把已设好的媒体信息覆盖掉（实测徽标从 AC-3 变 MP2）。
   */
  private primaryAudioTrackId: number | null = null;
  /** 是否已给 MSE 挂静音 AAC 假音轨（C2）。 */
  private silentAudioRegistered = false;
  /** 最近一次加载器错误（直播场景先抑制上报，等重试预算耗尽或确定性失败再上报）。 */
  private lastIoError: LoaderErrorInfo | null = null;

  constructor(
    private readonly config: PipelineConfig,
    private readonly callbacks: PipelineCallbacks = {},
  ) {
    this.urls = config.urls.length > 0 ? config.urls : [];
    this.maxRetries = config.maxRetries ?? DEFAULT_MAX_RETRIES;
    this.softDecodeCodecs = new Set(config.softDecodeCodecs ?? ["ac3", "eac3", "mp2", "mp3"]);
    this.separateAudio = config.separateAudio ?? false;
    this.suppressAudio = config.suppressAudio ?? false;
    this.audioOnly = config.audioOnly ?? false;

    this.ioController = new IOController(
      (chunk) => this.feedDemuxer(chunk),
      { bufferThreshold: config.bufferThreshold },
    );

    this.remuxer = new Fmp4Remuxer(
      {
        onInitSegment: (seg) => this.callbacks.onInitSegment?.(seg),
        onMediaSegment: (seg) => this.callbacks.onMediaSegment?.(seg),
      },
      { targetDuration: config.targetDuration, maxBytes: config.maxBytes },
    );

    this.demuxerCallbacks = {
      onTracks: (tracks) => this.handleTracks(tracks),
      onSamples: (samples) => this.handleSamples(samples),
      onError: (msg) => this.callbacks.onDemuxError?.(msg),
      onStreamLayout: (layout) => {
        // C2：muxed TS + 软解音轨 → 给 MSE 挂一条静音 AAC 假音轨（video 有音轨 → 后台
        // 不被 UA 冻结）。布局必须**在任何 init append 前**就把 audio 轨算上：Chromium
        // 一旦 append 首个 init 便锁死 SourceBuffer 数量，后到的 audio 缓冲会抛
        // QuotaExceededError（音频轨直接作废）。
        const silentAudio =
          !this.audioOnly && !this.separateAudio && !this.suppressAudio && layout.softAudio === true;
        // 抑制音频的流水线不期待任何音频缓冲（其 TS 内的音频不参与输出）
        const mseAudio = this.suppressAudio
          ? false
          : layout.mseAudio || silentAudio || this.silentAudioRegistered;
        // 通知下游（提前建缓冲/放行 hold）+ remuxer（纯视频立即放行起播门控）
        this.callbacks.onStreamLayout?.({ video: layout.video, mseAudio });
        this.remuxer.setAudioExpected(mseAudio);
      },
    };
  }

  /**
   * 首块探测 FLV / MPEG-TS 并惰性创建解复用器。
   * FLV 魔数 "FLV"（HTTP-FLV 直播）→ FlvDemuxer；其余一律按连续 MPEG-TS（TsDemuxer）。
   */
  private feedDemuxer(chunk: Uint8Array): void {
    if (this.demuxer) {
      this.demuxer.push(chunk);
      return;
    }
    let data = chunk;
    if (this.demuxProbeBuffer) {
      const merged = new Uint8Array(this.demuxProbeBuffer.byteLength + chunk.byteLength);
      merged.set(this.demuxProbeBuffer, 0);
      merged.set(chunk, this.demuxProbeBuffer.byteLength);
      data = merged;
    }
    const probe = FlvDemuxer.probe(data);
    if (probe.needMoreData) {
      this.demuxProbeBuffer = data;
      return;
    }
    this.demuxProbeBuffer = null;
    this.demuxer = probe.match ? new FlvDemuxer(this.demuxerCallbacks) : new TsDemuxer(this.demuxerCallbacks);
    this.demuxer.push(data);
  }

  /** 开始顺序拉取并转封装。优先使用动态分段源（HLS），否则用静态 URL 列表。 */
  async start(): Promise<void> {
    this.stopped = false;
    this.resetMediaInfo(); // 新流开始：清掉上一路累积的媒体信息
    this.pcmTimeBaseSec = null; // 新流：重新锚定软解音频时间基准
    this.pendingSoftAudio = [];
    this.primaryAudioTrackId = null; // 新流：重新锁定主音轨
    if (this.config.source) {
      while (!this.stopped) {
        // 缓冲领先门：直播下缓冲领先播放头超限时在此等待（hidden/时基过期自行放行）
        if (!(await this.waitForBufferLead())) return;
        const url = await this.config.source.next();
        if (this.stopped) break;
        if (!url) {
          // 直播没有新分段：分段间隔内轮询重试，而非结束（否则会触发 endOfStream/重载）
          if (this.config.source.live) {
            await sleep(1000);
            continue;
          }
          break; // VOD 已播完
        }
        const ok = await this.loadUrl(url);
        if (this.stopped) break;
        if (!ok && this.config.source.live) {
          // 分片不可用（如整点切换：旧序列分片被删、新分片尚未就绪）：丢弃该批次的其余旧分段，
          // 短等后重新请求播放列表 → 用新序列的分片续播。MSE 缓冲/时间轴不重建，恢复即无缝接上。
          // 这正是原生 HLS 播放器处理 segment 404 的方式（reload playlist，而非死啃旧分片）。
          this.config.source.invalidatePending?.();
          await sleep(SEGMENT_REFRESH_DELAY_MS);
        }
      }
    } else {
      for (const url of this.urls) {
        if (this.stopped) break;
        if (!(await this.waitForBufferLead())) return;
        await this.loadUrl(url);
      }
    }
    if (this.stopped) return;
    this.ioController.flush();
    this.remuxer.flush(true); // 收尾强制成段：跳过起播门控，避免残留样本丢失
    // 直播源不会真正结束，不应调用 endOfStream（否则会触发重载并导致缓冲被移除/报错）
    if (!this.config.source?.live) this.callbacks.onLoadingComplete?.();
  }

  /** 拉取一个 URL（分片或连续流）。返回是否成功拉到内容（false 表示该 URL 不可用/被掐断）。 */
  private async loadUrl(url: string): Promise<boolean> {
    let attempt = 0;
    let range: { from: number; to?: number } | undefined;
    // 分段源（HLS）：分片是「可替换」的——失败即由 start() 丢弃整批旧分段并刷新播放列表
    // （见 SEGMENT_MAX_ATTEMPTS 注释），这里只做 1 次短重试覆盖瞬时抖动，绝不长退避；
    // 连续流（直连 TS/FLV）：直播自愈重连（更大预算 + 退避 + 无数据看门狗）；点播/回看维持原行为。
    const segmented = this.config.source !== undefined;
    const live = this.isLivePlayback();
    const maxAttempts = segmented ? SEGMENT_MAX_ATTEMPTS : live ? MAX_LIVE_RELOADS : this.maxRetries;

    while (attempt <= maxAttempts && !this.stopped) {
      const loader = new FetchStreamLoader(
        { url, ...this.config.dataSource },
        {
          onDataArrival: (chunk, byteStart) => this.ioController.onDataArrival(chunk, byteStart),
          onError: (info) => {
            // 自身 stop/destroy 引起的 abort 不计入致命 IO 错误，否则会触发无意义的重试风暴
            if (this.stopped) return;
            // 直播：传输层失败先抑制上报（由下方退避重连自愈），只在确定性失败/预算耗尽后上报；
            // 分段源：分片失败属「批次失效」，由 start() 刷新播放列表消化，同样不上报。
            if (live) {
              this.lastIoError = info;
              return;
            }
            this.callbacks.onIOError?.(info);
          },
        },
        { resumeMode: this.config.resumeMode, dataWatchdogMs: live ? LIVE_DATA_TIMEOUT_MS : 0 },
      );
      this.loader = loader;
      try {
        await loader.open(range);
      } catch {
        if (this.stopped) return false; // 停止导致的异常直接忽略
      }

      if (loader.isCompleted) return true;
      if (this.stopped) return false;

      // 缓冲满被暂停：等待恢复后从续传起点继续。
      // **关键：暂停导致的中断不是失败，绝不能消耗重试预算**——缓冲满在直播中很常见
      // （水位/QuotaExceeded 反复触发），若每次暂停都 attempt++，几次之后 loadUrl 就退出，
      // start() 随即 flush + onLoadingComplete → 后端 endOfStream() → 永久卡死。
      if (this.paused) {
        await this.waitResume();
        if (this.stopped) return false;
        range = loader.getResumeRange();
        if (!range) {
          // restart 模式：丢弃已缓冲数据，从头再来
          this.ioController.reset();
          range = undefined;
        }
        continue;
      }

      // 直播重连：源 PTS 纪元不可预知 → 按需重钉软解 PCM 轴（C9c）
      this.reanchorPcmIfNeeded();

      // 可重试性判定：4xx（除 429）是确定性失败，重试只会拖慢上层按位置重建
      const code = this.lastIoError?.code ?? -1;
      if (!isRetryableIoError(code)) {
        // 分片 404 等确定性失败不上报：分片失效由 start() 刷新播放列表消化；
        // 若整个源真失效，播放列表刷新失败会成为 UI 信号（HlsSource 只上报一次）。
        if (!segmented && live && this.lastIoError) this.callbacks.onIOError?.(this.lastIoError);
        return false;
      }

      // 未完成（错误/断流/无数据看门狗掐断）：按 resumeMode 决定续传起点并计入一次重试
      range = loader.getResumeRange();
      if (!range) {
        // restart 模式：丢弃已缓冲数据，从头再来
        this.ioController.reset();
        range = undefined;
      }
      attempt++;
      if (!segmented && live && attempt <= maxAttempts) {
        const delay = Math.min(RETRY_BASE_DELAY_MS * 2 ** (attempt - 1), RETRY_MAX_DELAY_MS);
        // eslint-disable-next-line no-console
        console.warn(
          `[PIPELINE] 直播流中断（code=${code}${loader.dataStalled ? " 无数据看门狗" : ""}）→ ` +
            `${delay}ms 后重连 ${attempt}/${maxAttempts}`,
        );
        await sleep(delay);
        if (this.stopped) return false;
      }
    }
    // 预算耗尽：把此前抑制的错误上报一次（触发上层会话重建）；分段源不上报（同上）
    if (!this.stopped && !segmented && live && this.lastIoError) this.callbacks.onIOError?.(this.lastIoError);
    return false;
  }

  private handleTracks(tracks: TrackInfo[]): void {
    for (const t of tracks) {
      this.trackCodecs.set(t.id, t.codec);
      this.trackTimescales.set(t.id, t.timescale);
      // 抑制音频（声画分流的主视频流水线）：音频轨完全跳过（不进 remuxer、不软解、
      // 不建静音假轨），声音统一由独立音轨流水线负责，避免两路撞同一 SourceBuffer。
      if (this.suppressAudio && t.kind === "audio") continue;
      // 主音轨 = 首个注册的音频轨（PMT 声明顺序 = 广播方的主次顺序）：在此一次性锁定，
      // 后续备轨（如并存的 MP2）发布时不再参与媒体信息与出声选择。
      if (t.kind === "audio" && this.primaryAudioTrackId === null) {
        this.primaryAudioTrackId = t.id;
      }
      // audio-only 软解：不向 MSE remuxer 加任何轨（避免误发 MSE init/media 干扰主视频门控）
      if (this.audioOnly) continue;
      // 软解音轨不进 MSE（MSE 无法解码 ac3/eac3/mp2/mp3），真实声音由 WebAudio 输出；
      // muxed TS 路径额外给 MSE 挂一条静音 AAC 假音轨（见 onStreamLayout 注释）。
      if (this.softDecodeCodecs.has(t.codec)) {
        if (!this.audioOnly && !this.separateAudio) {
          this.remuxer.setSilentAudioTrack({
            id: t.id,
            sampleRate: t.sampleRate ?? 48000,
            channels: t.channels ?? 2,
          });
          this.silentAudioRegistered = true;
        }
        continue;
      }
      // passthrough 音频（AAC 等）同样只注册主音轨：IPTV 多伴音流可并存 6 路 AAC，
      // MSE 的一个 audio SourceBuffer 只能绑定一个 init（track_ID），多条音轨的
      // init/media 段混入同一 SB 会被浏览器整段拒收（实测 VIDERR audio SB error）。
      // 未注册轨的样本由 remuxer.addSample 丢弃，声音与徽标都只认主音轨。
      if (t.kind === "audio" && t.id !== this.primaryAudioTrackId) continue;
      this.remuxer.addTrack({
        id: t.id,
        kind: t.kind,
        codec: t.codec,
        timescale: t.timescale,
        width: t.width,
        height: t.height,
        channels: t.channels,
        sampleRate: t.sampleRate,
        codecPrivate: t.codecPrivate,
      });
    }
    const video = tracks.find((t) => t.kind === "video");
    // 音频只认主音轨：备轨批次（如并存 MP2）不得覆盖主轨的 codec 与声道数，
    // 否则徽标会从 AC-3/5.1 被改写成 MP2/立体声（声音实际仍走主轨，造成表里不一）。
    const audio =
      this.primaryAudioTrackId !== null ? tracks.find((t) => t.id === this.primaryAudioTrackId) : undefined;
    // 音视频通常分两批发布（音频须等首帧 ADTS 才能构造 esds），此处按轨合并到已累积的
    // mediaInfo，否则后一批会整体覆盖前一批、导致另一半徽章（如"音频编码"）消失。
    this.mergeMediaInfo({
      video: video
        ? { codec: video.codec, width: video.width, height: video.height, scanType: video.scanType }
        : undefined,
      audio: audio ? { codec: audio.codec, channelCount: audio.channels, sampleRate: audio.sampleRate } : undefined,
    });
  }

  /** 对外发布媒体信息（内容未变化则不重复刷新）。 */
  private publishMediaInfo(next: PlayerMediaInfo): void {
    const serialized = JSON.stringify(next);
    if (serialized === this.serializedMediaInfo) return;
    this.mediaInfo = next;
    this.serializedMediaInfo = serialized;
    this.callbacks.onMediaInfo?.(next);
  }

  /** 把本次更新并入累积的 mediaInfo（undefined 字段不覆盖已有值）。 */
  private mergeMediaInfo(update: PlayerMediaInfo): void {
    this.publishMediaInfo({
      ...this.mediaInfo,
      video: update.video ? mergeDefined(this.mediaInfo.video, update.video) : this.mediaInfo.video,
      audio: update.audio ? mergeDefined(this.mediaInfo.audio, update.audio) : this.mediaInfo.audio,
      bitrate: update.bitrate ?? this.mediaInfo.bitrate,
    });
  }

  /**
   * 用视频样本 DTS 增量估计帧率（相邻 DTS 差恒等于帧长，与 B 帧/解码序无关）。
   *
   * - 取窗口内增量的**中位数**，抵抗个别抖动；窗口不足时不上报（宁可慢也要稳）；
   * - 出现不连续（seek、断流重连、时间戳跳变）即作废当前窗口重新积累；
   * - 该值只用于媒体信息展示（"25 FPS"徽标），不参与任何同步计算。
   */
  private noteVideoSample(dtsSec: number): void {
    const prev = this.lastVideoDtsSec;
    this.lastVideoDtsSec = dtsSec;
    if (prev === null) {
      return;
    }
    const delta = dtsSec - prev;
    if (!(delta > 0.002) || delta > 0.5) {
      this.videoDtsDeltas = [];
      return;
    }
    this.videoDtsDeltas.push(delta);
    if (this.videoDtsDeltas.length > FPS_WINDOW_SIZE) {
      this.videoDtsDeltas.shift();
    }
    if (this.videoDtsDeltas.length < FPS_MIN_SAMPLES) {
      return;
    }
    const sorted = [...this.videoDtsDeltas].sort((a, b) => a - b);
    const median = sorted[sorted.length >> 1];
    const fps = Math.round((1 / median) * 100) / 100;
    if (Math.abs(fps - (this.mediaInfo.video?.frameRate ?? 0)) < FPS_PUBLISH_EPSILON) {
      return;
    }
    this.mergeMediaInfo({ video: { frameRate: fps } });
  }

  private resetMediaInfo(): void {
    this.mediaInfo = {};
    this.serializedMediaInfo = "{}";
    this.videoDtsDeltas = [];
    this.lastVideoDtsSec = null;
  }

  private handleSamples(samples: {
    trackId: number;
    kind: "video" | "audio";
    data: Uint8Array;
    dts: number;
    cts: number;
    isKeyframe: boolean;
  }[]): void {
    for (const s of samples) {
      const codec = this.trackCodecs.get(s.trackId);
      const timescale = this.trackTimescales.get(s.trackId) ?? 90000;
      // 抑制音频（声画分流的主视频流水线）：音频样本一律丢弃（软解/MSE 均不发）
      if (this.suppressAudio && s.kind === "audio") continue;
      if (this.audioOnly && !(codec && this.softDecodeCodecs.has(codec))) {
        // audio-only 流水线只处理需软解的音轨；其余（如 AAC 分离音轨）本期不接入 MSE，丢弃并告警
        // eslint-disable-next-line no-console
        console.warn(`[PIPELINE] audio-only 流水线忽略非软解音轨 codec=${codec ?? "?"}（本期不接入 MSE）`);
        continue;
      }
      if (codec && this.softDecodeCodecs.has(codec)) {
        // 多音轨流（例如 AC-3 主轨与 MP2 备轨并存）只播**第一条**出声：两条 PCM 同时灌进
        // 同一个 PCM 播放器会叠音/杂音；其余软解轨完全忽略（也不参与时间轴基准）。
        // 兜底：主音轨未由轨信息确定时（如纯分离音频路径）以首个软解样本的轨为准
        if (this.primaryAudioTrackId === null) {
          this.primaryAudioTrackId = s.trackId;
        }
        if (s.trackId !== this.primaryAudioTrackId) continue;
        // 软解音频时间须与 MSE 视频同基准（0 起）：减视频首样本基准秒。
        // 直播巨大 PTS 若直传会让 PCM 播放器漂移测量恒超阈值 → 反复硬重同步（卡顿）/无声。
        if (this.pcmTimeBaseSec === null) {
          if (this.separateAudio) {
            // 分离音轨：base 钉到音频首样本 dts，起播 time=0（与视频各自 0 起对齐）；
            // 跨时钟漂移由 PCM 播放器漂移环/硬重同步兜底。不暂存、不依赖视频首样本。
            this.pcmTimeBaseSec = s.dts / timescale;
            this.emitSoftAudio(s.trackId, codec, s.data, 0);
            continue;
          }
          // 基准未定（首个视频样本未到）：先暂存，待基准确定后统一归一化补发
          this.pendingSoftAudio.push({ trackId: s.trackId, codec, data: s.data, dts: s.dts, timescale });
          if (this.pendingSoftAudio.length > this.maxPendingSoftAudio) {
            // 纯音频流（无视频样本可定基准）：以最早一块的 dts 兜底锚定，避免永久无声
            const first = this.pendingSoftAudio[0];
            this.pcmTimeBaseSec = first.dts / first.timescale;
            this.flushPendingSoftAudio();
          }
          continue;
        }
        this.emitSoftAudio(s.trackId, codec, s.data, s.dts / timescale - this.pcmTimeBaseSec);
        continue;
      }
      // 记录 MSE 视频轨时间轴基准（首个样本 dts → 秒），供软解音频归一化
      if (s.kind === "video") {
        if (this.pcmTimeBaseSec === null) {
          this.pcmTimeBaseSec = s.dts / timescale;
          this.flushPendingSoftAudio();
        }
        this.noteVideoSample(s.dts / timescale);
      }
      this.remuxer.addSample({
        trackId: s.trackId,
        data: s.data,
        dts: s.dts,
        cts: s.cts,
        isKeyframe: s.isKeyframe,
      });
    }
  }

  /**
   * 发射一块软解音频。time 已归一化到 MSE 时间轴（减视频首样本基准），可为负
   * （MP2/ac3 的 PES 常先于首个视频 IDR 到达）。负值不在此夹 0——夹 0 会把锚点前
   * 的音频全堆到 time=0，造成 A/V 偏移且 AudioContext 晚初始化时会测得巨大初始漂移
   * → 起播硬重同步（卡顿）。交由 worker 的 PcmTimeline 按原始语义丢弃/裁剪锚点前音频，
   * 保证首个进 PCM 播放器的 time 恰为 0（与 video 同起点）。
   */
  private emitSoftAudio(trackId: number, codec: string, data: Uint8Array, mediaTime: number): void {
    this.lastSoftAudioTimeSec = mediaTime;
    this.callbacks.onSoftAudioData?.({
      trackId,
      codec,
      data,
      time: mediaTime,
    });
  }

  /** 基准确定后补发暂存的软解音频块。 */
  private flushPendingSoftAudio(): void {
    const base = this.pcmTimeBaseSec;
    if (base === null || this.pendingSoftAudio.length === 0) return;
    const pending = this.pendingSoftAudio;
    this.pendingSoftAudio = [];
    for (const p of pending) {
      this.emitSoftAudio(p.trackId, p.codec, p.data, p.dts / p.timescale - base);
    }
  }

  /**
   * 主线程上报播放头 + MSE 缓冲末端 + 页面可见性（缓冲领先门用）。
   * 缺失/过期时门直接放行，绝不让流控把播放卡死。
   */
  /**
   * 声画分流的音频流水线：把本流水线的时间基准锚到**视频基准**（透传 remuxer，见其文档）。
   * 必须在首个媒体段成段（基准锁定）之前调用。
   */
  setExternalBase(baseSec: number): void {
    this.remuxer.setExternalBase(baseSec);
  }

  /** 标记外部基准待定（见 remuxer.awaitExternalBase）：init 先发、媒体段等基准。 */
  awaitExternalBase(): void {
    this.remuxer.awaitExternalBase();
  }

  /** 本流水线首个视频样本的绝对秒（供音频流水线取视频基准）；未到时为 null。 */
  getFirstVideoSampleSec(): number | null {
    return this.remuxer.getFirstSampleSec("video");
  }

  setClock(currentTimeMs: number, bufferedEndMs: number, hidden: boolean): void {
    this.playheadCurrentMs = currentTimeMs;
    this.playheadBufferedEndMs = bufferedEndMs;
    this.lastClockArrivalMs = performance.now();
    this.pageHidden = hidden;
  }

  /** 是否直播播放：仅直播应用缓冲领先门（点播/回看按位置拉取，无领先概念）。 */
  private isLivePlayback(): boolean {
    return this.config.sourceMode === "continuous-live-ts" || this.config.source?.live === true;
  }

  /**
   * 缓冲领先门：MSE 缓冲末端领先播放头超过
   * `LEAD_BUFFER_AHEAD_MS` 时暂停拉流/解码，直到播放头追上来。仅直播生效，且依赖主线程
   * 经 `clock` 命令周期上报的播放头；时基过期或播放头未知时直接放行，绝不死锁、绝不卡播放。
   *
   * 页面隐藏时**无条件放行**：后台定时器被节流、播放头几乎不走，缓冲永远"满"，门会永久
   * 闭合 → 拉流/解封装/软解全停 → 音频零产出（后台静音）；后台语义是音频 free-run，
   * 多拉的部分由 MSE 侧背压吸收。
   */
  private async waitForBufferLead(): Promise<boolean> {
    if (!this.isLivePlayback()) return true;
    if (this.pageHidden) return true;
    if (this.playheadCurrentMs < 0 || this.playheadBufferedEndMs < 0) return true;
    if (performance.now() - this.lastClockArrivalMs > CLOCK_STALE_MS) return true;

    let pausedSince: number | null = null;
    while (!this.stopped && this.isLivePlayback()) {
      if (this.pageHidden) return true;
      if (this.playheadCurrentMs < 0 || this.playheadBufferedEndMs < 0) return true;
      if (performance.now() - this.lastClockArrivalMs > CLOCK_STALE_MS) return true;
      if (this.playheadBufferedEndMs - this.playheadCurrentMs <= LEAD_BUFFER_AHEAD_MS) return true;
      // 暂停超时：强制放行一次拉取（见 LEAD_PAUSE_MAX_MS 注释），避免上游恢复后仍长时间无新数据。
      if (pausedSince === null) pausedSince = performance.now();
      if (performance.now() - pausedSince >= LEAD_PAUSE_MAX_MS) return true;
      await sleep(LEAD_GATE_POLL_MS);
    }
    return !this.stopped;
  }

  /**
   * 软解 PCM 轴脱轴自愈（C9c）：直播 TS 重连/断流恢复后源 PTS 纪元不可预知（上游整体重启
   * 会归零、断流填洞会整体前跳）。若 PCM 带着旧轴继续"内容连续"推进，就会与新视频轴分离 →
   * 缓冲领先门压死（静音）或音画持续错位且无法自愈。
   *
   * 判据（预测的下一块 PCM 时间 − 播放头）见 `PCM_REANCHOR_AHEAD_SEC/BEHIND_SEC`；
   * 界内保留（小幅抖动由源 PTS 连续语义吸收，避免每次抖动都丢弃已解码 PCM）。
   */
  private reanchorPcmIfNeeded(): void {
    const predicted = this.lastSoftAudioTimeSec;
    const playheadSec = this.playheadCurrentMs >= 0 ? this.playheadCurrentMs / 1000 : null;
    if (predicted !== null && playheadSec !== null) {
      const deltaSec = predicted - playheadSec;
      if (deltaSec <= PCM_REANCHOR_AHEAD_SEC && deltaSec >= -PCM_REANCHOR_BEHIND_SEC) {
        return; // 界内：保留时间轴
      }
      // eslint-disable-next-line no-console
      console.warn(`[PIPELINE] PCM 轴偏离播放头 ${(deltaSec * 1000).toFixed(0)}ms → 重钉 PCM 时间轴`);
    }
    // 重钉：清 PCM 基准与暂存块，下一帧按新基准重新锚定（有视频轨时钉视频首样本；
    // 分离音轨钉音频首样本）。worker 侧软解状态由其锚点阈值/PcmTimeline 自行吸收。
    this.pcmTimeBaseSec = null;
    this.pendingSoftAudio = [];
    this.lastSoftAudioTimeSec = null;
  }

  /** 暂停拉取（缓冲满时由 MSE 水位回调驱动）。 */
  pause(): void {
    if (this.paused) return;
    this.paused = true;
    this.loader?.abort();
  }

  /** 恢复拉取。 */
  resume(): void {
    if (!this.paused) return;
    this.paused = false;
    const waiters = this.resumeWaiters;
    this.resumeWaiters = [];
    for (const w of waiters) w();
  }

  private waitResume(): Promise<void> {
    if (!this.paused) return Promise.resolve();
    return new Promise((resolve) => this.resumeWaiters.push(resolve));
  }

  /** 强制把当前缓冲成段（如 seek / 切源前调用）。 */
  flush(): void {
    this.ioController.flush();
    this.remuxer.flush(true);
  }

  stop(): void {
    this.stopped = true;
    this.loader?.abort();
    this.loader = null;
  }

  destroy(): void {
    this.stop();
    this.remuxer.destroy();
    this.demuxer?.reset();
    this.demuxer = null;
    this.demuxProbeBuffer = null;
    this.ioController.reset();
  }
}
