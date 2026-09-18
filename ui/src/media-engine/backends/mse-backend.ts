/**
 * MSE 播放后端。
 * 对外实现 PlaybackBackend 契约（设计 §5.12）：串联 TransmuxPipeline（加载→demux→remux）、
 * MediaSourceController（缓冲/流控）、PlaybackController（直播同步）与可选的 PCMAudioPlayer
 * （软解音频）。缓冲满暂停拉取、缓冲可用恢复（§5.3）。
 */

import { MediaSourceController, type BufferedRange } from "../mse/media-source-controller";
import { PlaybackController } from "../mse/playback-controller";
import { TransmuxWorkerClient } from "../worker/worker-client";
import type { DecoderBridgeFactory, WasmDecoderConfig } from "../decoder/types";
import { PCMAudioPlayer, type PCMAudioChunk } from "../audio/pcm-audio-player";
import { VideoRenderer } from "../render/video-renderer";
import { lagBehindLiveEdge, type LiveSessionAnchor } from "../timeline";
import type { SourceMode } from "../types";
import { BackendEventEmitter } from "./event-emitter";
import type {
  PlaybackBackend,
  PlaybackBackendKind,
  PlaybackBackendState,
  PlayerError,
  PlayerEventMap,
  PlayerMediaInfo,
  PlayerSegment,
} from "./types";
import { detectAudioOutputChannels } from "../audio/output-channels";
import { PlayerErrors } from "../errors";

export interface MseBackendOptions {
  sourceMode?: SourceMode;
  /** 软解音频开关：启用时由解码路径调用 enqueuePCM()。 */
  softDecodeAudio?: boolean;
  /** 软解音频声道输出模式（mono = 左右声道合成）；缺省 stereo。 */
  audioChannelMode?: "stereo" | "mono";
  /** 目标直播延迟（秒），goLive 使用。 */
  targetLatencySec?: number;
  /** 是否启用直播同步（跟随直播边）。 */
  liveSync?: boolean;
  /** 传给 pipeline 的成段阈值。 */
  targetDuration?: number;
  maxBytes?: number;
  /** 传给 pipeline 的续传模式。 */
  resumeMode?: "range" | "restart";
  /** WebGL 渲染画布；省略即不做视频后处理。 */
  renderCanvas?: HTMLCanvasElement;
  /** 视频元数据声明为隔行时自动去隔行。 */
  autoDeinterlace?: boolean;
  /** 渲染门限内启用轻量画质增强。 */
  pictureEnhancement?: boolean;
  /** 软解 WASM URL 配置（ac3/eac3/mp2/mp3）。 */
  wasmDecoders?: WasmDecoderConfig["wasmDecoders"];
  /** WASM 胶水工厂（由 FFmpeg-WASI 构建产物提供）。 */
  decoderBridgeFactory?: DecoderBridgeFactory;
}

const TARGET_LATENCY = 6;

export class MseBackend implements PlaybackBackend {
  readonly kind: PlaybackBackendKind = "mse";
  readonly mediaElement: HTMLVideoElement;

  private readonly mse: MediaSourceController;
  private readonly playback: PlaybackController;
  /** 转封装 worker 客户端：demux/remux/HLS/软解都在 worker 线程，主线程只做 MSE append + WebAudio。 */
  private worker: TransmuxWorkerClient | null = null;
  private pcmPlayer: PCMAudioPlayer | null = null;
  private renderer: VideoRenderer | null = null;
  /** 启动 hold 期间是否已收到 audio init（已 addSourceBuffer(audio)）。 */
  /** 各预期轨的 SourceBuffer 是否已建好。必须**全部**建好才放行统一 append，
   *  否则先到的音频 media 会抢先初始化解复用器，后到的视频 init 再建 SourceBuffer
   *  就抛 "引擎已初始化" → 视频轨废掉 + 起播 1.7s 坑。 */
  private videoArmed = false;
  private audioArmed = false;
  /** 来自 onStreamLayout 的预期轨集合；缺省假设有视频（纯音频流由布局修正）。 */
  private expectsVideo = true;
  private expectsAudio = false;
  /** 音频 SB 曾创建失败 → 重建后强制把音频算作预期轨（保证它在任何 append 之前建好）。 */
  private audioExpectedForced = false;
  /** SourceBuffer 上限自愈只做一次，避免异常流反复重建。 */
  private sbLimitHealed = false;
  /** 本流已被判定"编码不受支持"的轨（用于区分单轨降级与整条流不可播）。 */
  private readonly unsupportedTracks = new Set<string>();
  private mseHoldTimer: ReturnType<typeof setTimeout> | null = null;

  private readonly emitter = new BackendEventEmitter();
  private readonly bufferedRanges = new Map<string, BufferedRange[]>();
  private readonly options: MseBackendOptions;
  private anchor: LiveSessionAnchor | null = null;
  private segments: PlayerSegment[] = [];
  private mediaInfo: PlayerMediaInfo | null = null;
  private autoDeinterlace: boolean;
  private pictureEnhancement: boolean;
  /** 软解音频声道输出模式（运行时可切；创建 PCMAudioPlayer 时生效）。 */
  private audioChannelMode: "stereo" | "mono";
  private tickTimer: ReturnType<typeof setInterval> | null = null;
  private destroyed = false;
  /** 因背压（MSE 缓冲真满）暂停过 worker：后台需抬掉，避免后台拉流/软解停摆。 */
  private backpressurePaused = false;
  /** visibilitychange 监听（后台抬掉背压暂停；回前台由背压回调重算）。 */
  private visibilityListener: (() => void) | null = null;
  /** 卡死检测：连续无进展的 tick 数 / 上次有进展时的播放位置。 */
  private stallTicks = 0;
  private lastProgressCt = 0;

  constructor(video: HTMLVideoElement, options: MseBackendOptions = {}) {
    this.mediaElement = video;
    this.options = options;
    this.autoDeinterlace = options.autoDeinterlace ?? false;
    this.pictureEnhancement = options.pictureEnhancement ?? false;
    this.audioChannelMode = options.audioChannelMode ?? "stereo";
    if (options.renderCanvas) {
      this.renderer = new VideoRenderer(options.renderCanvas, video, {
        deinterlace: this.autoDeinterlace,
        enhancement: this.pictureEnhancement,
      });
    }

    this.mse = new MediaSourceController(
      video,
      {
        onBufferFull: () => this.pauseWorkerForBackpressure(),
        onBufferAvailable: () => this.resumeWorkerFromBackpressure(),
        onBufferUpdated: (track, ranges: BufferedRange[]) => {
          this.bufferedRanges.set(track, ranges);
          // 直播边估计以**视频缓冲**为准：声画分流（独立音轨）下音频链片小、处理快，
          // 常领先视频链 1~2 片；若取并集，缓冲末端会被音频虚高十几秒 → live edge/seek
          // 估到视频没有数据的位置 → 视频干等、播放头错乱（实测声画不同步）。
          // 纯音频流（无视频轨）才用并集。
          const videoRanges = this.bufferedRanges.get("video");
          this.playback.notifyBuffered(
            videoRanges && videoRanges.length > 0 ? videoRanges : unionRanges(this.bufferedRanges),
          );
        },
        onStartStreaming: () => this.worker?.resume(),
        // 明确**不**在 endstreaming 时暂停 worker：MSE 层已在 ManagedMediaSource
        // streaming=false 期间自行延迟 append。此处暂停会把上游拉流饿死 → 卡流。
        onError: (info) => this.emitError({ category: "media", info }),
        // 单轨编码不受支持（如无 HEVC）：跳过该轨 + 非阻断告警，绝不走重载恢复
        onTrackUnsupported: (track, mime) => this.handleTrackUnsupported(track, mime),
        // 音频 SourceBuffer 建不出来（引擎已锁）：自愈重建，避免"有画无声"
        onSourceBufferLimit: (track, message) => this.healSourceBufferLimit(track, message),
      },
    );

    this.playback = new PlaybackController(
      video,
      {
        onLiveStateChange: (isLive) => this.emit("live-edge-state", isLive),
      },
      {
        sourceMode: options.sourceMode ?? "continuous-live-ts",
        liveSync: options.liveSync ?? true,
        // 直播边延迟：hls 用 liveSessionAnchor 按墙钟外推（绝不用 buffer.end——
        // HLS 10s 分片下载完 buffer 猛涨，latency 假性巨大 → 持续 1.2x 追速 →
        // 软解音频被迫加速 → 漂移/毛刺/几分钟后没声音）；continuous-live-ts 返回
        // null 让 PlaybackController 回退 buffer.end（缓冲即直播边，语义正确）。
        liveEdgeLatency: () =>
          (this.options.sourceMode ?? "continuous-live-ts") === "hls"
            ? this.anchor
              ? lagBehindLiveEdge(this.anchor, this.mediaElement.currentTime)
              : null
            : null,
      },
    );

    this.attachMediaEvents();
  }

  // ---- 事件 ----

  on<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void {
    this.emitter.on(event, handler);
  }

  off<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void {
    this.emitter.off(event, handler);
  }

  private emit<K extends keyof PlayerEventMap>(event: K, ...args: Parameters<PlayerEventMap[K]>): void {
    this.emitter.emit(event, ...args);
  }

  private emitError(error: PlayerError): void {
    this.emit("error", error);
  }

  private attachMediaEvents(): void {
    const v = this.mediaElement;
    v.addEventListener("timeupdate", () => this.emit("clock-tick", v.currentTime));
    // 无缝换台靠 playing 的 eventTimeStamp 与 UI 侧 pending.startedAt 比较来提交接管；
    // 传 0 会让守卫恒不过 → 新路在后台起播却永不接管、旧路一直占屏（卡住）。
    v.addEventListener("canplay", (e) => this.emit("transport-state", "canplay", e.timeStamp));
    v.addEventListener("playing", (e) => this.emit("transport-state", "playing", e.timeStamp));
    v.addEventListener("waiting", (e) => this.emit("transport-state", "waiting", e.timeStamp));
    v.addEventListener("pause", (e) => this.emit("transport-state", "paused", e.timeStamp));
    v.addEventListener("ended", () => this.emit("ended"));
    // 直播同步会调 video.playbackRate（追速 1.2x）；软解音频必须同步调速，
    // 否则音频恒 1.0x 而视频 1.2x → 每秒漂移 0.2s → 反复硬重同步 → 断音/无声。
    v.addEventListener("ratechange", () => this.pcmPlayer?.setPlaybackRate(v.playbackRate));
    if (this.renderer) {
      // 起停后必须上报渲染状态：否则 canvas 与 <video> 的显隐会停留在旧状态
      v.addEventListener("playing", () => {
        this.renderer?.start();
        this.emitRenderState();
      });
      v.addEventListener("pause", () => {
        this.renderer?.stop();
        this.emitRenderState();
      });
      v.addEventListener("ended", () => {
        this.renderer?.stop();
        this.emitRenderState();
      });
    }
  }

  // ---- 加载 ----

  loadSegments(segments: PlayerSegment[]): void {
    if (this.destroyed) return;
    this.segments = segments;
    this.sbLimitHealed = false;
    this.audioExpectedForced = false;
    this.unsupportedTracks.clear();
    // 切台即切口（两步都必须在 sourceopen 之前同步完成）：
    // 1) 立即停掉旧 worker —— 旧 worker 的 terminate 原本要等 sourceopen 回调
    //    （beginPipeline），而该事件可能滞后数秒；期间旧 worker 仍在拉流并继续软解，
    //    实测切台后旧频道仍被拉取 ~3 秒、且旧音轨持续出声（PCM 被重新灌入播放器）。
    // 2) 清空音频播放器（清 PCM 队列并停掉已排程的 WebAudio 节点）。
    // 已 post 的在途 PCM 由 worker-client 的 disposed 闸门丢弃，双重保险。
    this.worker?.destroy();
    this.worker = null;
    this.pcmPlayer?.flush();
    // 新流/换台：重建 MediaSource，清掉旧频道的 buffered 区间与播放头。
    // 若复用同一 MS，旧 buffered 区间与旧 currentTime 残留会让新流（从 0 起缓冲）
    // 与播放头错位 → "有数据但不开始播放"（故每次 loadSegments 都重建 MSE）。
    this.mse.destroy();
    this.mse.open(() => this.startPipeline());
  }

  private startPipeline(): void {
    this.beginPipeline();
  }

  private beginPipeline(): void {
    // 新流：重建 worker（worker 内重置软解/时间轴状态）；旧 worker 销毁
    this.worker?.destroy();
    this.worker = null;
    this.pcmPlayer?.flush();
    // 启动 hold：音频 SourceBuffer 建齐（或确认纯视频）前不 append 任何数据，
    // 从根上杜绝 Chromium "首个 init 已 append → addSourceBuffer(audio) 抛已达上限"。
    this.startMseHold();
    // 直播时长语义为无限（continuous-live-ts 立即 setDuration(Infinity)）。
    // 有界 duration 会让直播边/seekable 判定失真。
    this.mse.setDuration(Infinity);

    const worker = new TransmuxWorkerClient({
      onStreamLayout: (layout) => {
        // 记录预期轨集合：纯视频/纯音频时，对应轨一见即可放行（见 maybeReleaseHold）
        this.expectsVideo = layout.video;
        this.expectsAudio = layout.mseAudio;
        this.maybeReleaseHold();
      },
      onInitSegment: (seg) => {
        this.mse.appendInit(seg.kind, seg.data, seg.codec, seg.container);
        // 标记该轨缓冲已建，并在「所有预期轨」都就绪后才统一放行 append
        if (seg.kind === "video") this.videoArmed = true;
        if (seg.kind === "audio") this.audioArmed = true;
        this.maybeReleaseHold();
      },
      onMediaSegment: (seg) => {
        this.mse.appendMedia(seg.kind, seg.data, seg.timestampOffset);
        // 注意：不可按段推进 duration —— remuxer 的 startDts 是 90kHz 原始刻度（直播约 3.3e9），
        // 直接当秒设给 MediaSource 会让 duration/seekable 失真到数十亿秒，进而破坏直播边判定。
        // 直播时长语义固定为 Infinity（见 beginPipeline）。
      },
      onMediaInfo: (info) => {
        this.mediaInfo = info;
        // 4K（≥3840×2160）必为逐行扫描：去隔行/增强均无正向意义，反而加重
        // 主线程/GPU 负载（用户实测 4K 频道一直加载）。按分辨率强制降级。
        const vw = info.video?.width ?? 0;
        const vh = info.video?.height ?? 0;
        if (vw >= 3840 || vh >= 2160) {
          this.renderer?.setDeinterlace(false);
          this.renderer?.setEnhancement(false);
        }
        this.emit("track-metadata", info);
      },
      onPCMAudioData: (pcm, channels, sampleRate, time) => {
        const per = deinterleave(pcm, channels, Math.floor(pcm.length / channels));
        this.pcmPlayer?.enqueue({ pcm: per, sampleRate, time });
      },
      onPcmAudioStats: (stats) => this.pcmPlayer?.setPipelineStats(stats),
      onLoadingComplete: () => this.mse.endOfStream(),
      onError: (error) => this.emitError(error),
    });
    this.worker = worker;

    const softEnabled = this.options.softDecodeAudio || Object.keys(this.options.wasmDecoders ?? {}).length > 0;
    // 设备输出声道能力：worker 侧据此让 WASM 直接按目标声道解码（5.1 直通 / 2.0 降混）
    const outputChannels = detectAudioOutputChannels();
    if (softEnabled && !this.pcmPlayer) {
      this.pcmPlayer = new PCMAudioPlayer({
        clock: () => this.mediaElement.currentTime,
        audioChannelMode: this.audioChannelMode,
        outputChannels,
      });
      // 新建的 PCM 播放器自带满音量/未静音初值：立刻套用当前状态，
      // 否则打开频道后软解音频（MP2/AC-3）会在"界面显示静音/音量 0"时照常出声。
      // （字段在 init() 末尾的 updateGain() 生效，故此处在 init 之前设置即可。）
      this.pcmPlayer.setVolume(this.mediaElement.volume);
      this.pcmPlayer.setMuted(this.mediaElement.muted);
      // 绑定 video：waiting/stalled 宽限内冻结纠漂，避免对冻结时钟反复变速/重同步
      this.pcmPlayer.attachVideo(this.mediaElement);
      this.pcmPlayer.onAudioStats = (stats) => this.emit("pcm-pipeline-stats", stats);
      void this.pcmPlayer.init().catch(() => this.emit("audio-gate-blocked"));
    }

    if (this.tickTimer === null) {
      this.tickTimer = setInterval(() => {
        this.playback.tick();
        this.recoverFromStall();
        this.postClock();
      }, 250);
    }
    if (this.visibilityListener === null) {
      this.visibilityListener = () => {
        // 进后台：抬掉前台拿到的背压暂停（worker 缓冲领先门在 hidden 时自行放行），
        // 否则 fetch + 软解停摆 → 后台静音。回前台由下一次背压回调重新暂停。
        if (document.hidden && this.backpressurePaused) this.resumeWorkerFromBackpressure();
      };
      document.addEventListener("visibilitychange", this.visibilityListener);
    }

    // HLS 解析、demux、remux、软解均在 worker 内完成（主线程不阻塞）
    worker.load({
      urls: this.segments.map((s) => s.url),
      sourceMode: this.options.sourceMode ?? "continuous-live-ts",
      resumeMode: this.options.resumeMode,
      targetDuration: this.options.targetDuration,
      maxBytes: this.options.maxBytes,
      softDecodeAudio: softEnabled,
      wasmDecoders: this.options.wasmDecoders,
      pcmOutputChannels: outputChannels,
    });
  }

  /**
   * 卡死自动恢复：直播流难免出现缓冲空洞（丢分片/时间轴跳变），播放头一旦停在
   * 一个永远填不上的洞里就会「一直加载中」。此处在确认长时间无进展后
   * ①跳过空洞到下一个缓冲区间；②若前方有数据仍卡住则轻推一下重新触发解码。
   */
  private recoverFromStall(): void {
    const v = this.mediaElement;
    if (this.destroyed || v.paused || v.seeking || v.ended) {
      this.stallTicks = 0;
      return;
    }
    if (v.readyState >= 3) {
      // HAVE_FUTURE_DATA 及以上：正常
      this.stallTicks = 0;
      this.lastProgressCt = v.currentTime;
      return;
    }
    if (Math.abs(v.currentTime - this.lastProgressCt) > 0.05) {
      this.stallTicks = 0;
      this.lastProgressCt = v.currentTime;
      return;
    }

    this.stallTicks++;
    if (this.stallTicks < 12) return; // 约 3 秒无进展才介入，避免误触发

    this.stallTicks = 0;
    const ct = v.currentTime;
    const b = v.buffered;
    // ① 跳过空洞
    for (let i = 0; i < b.length; i++) {
      if (b.start(i) > ct + 0.3) {
        v.currentTime = b.start(i) + 0.05;
        this.lastProgressCt = v.currentTime;
        return;
      }
    }
    // ② 前方明明有数据却不动：轻推重新触发解码
    if (b.length > 0 && b.end(b.length - 1) > ct + 1) {
      v.currentTime = ct + 0.3;
      this.lastProgressCt = ct + 0.3;
    }
  }

  /** 供软解音频解码路径调用（PCM 时间须已归一化到 MSE 时间轴）。 */
  enqueuePCM(chunk: PCMAudioChunk): void {
    this.pcmPlayer?.enqueue(chunk);
  }

  // ---- 软解音频（ac3/eac3/mp2/mp3 → WASM → PCM） ----

  /**
   * 启动 hold：新流开始时挂起全部 MSE append。Chromium 一旦 append 首个 init/moov 就锁死
   * SourceBuffer 数量，之后 addSourceBuffer(audio) 必抛 QuotaExceededError。因此必须等
   * audio 缓冲也建齐（audio init 到达）或确认纯视频后，才统一放行 append。
   */
  private startMseHold(): void {
    this.videoArmed = false;
    this.audioArmed = false;
    this.expectsVideo = true;
    // 自愈重试时已知"有音频轨"：强制等音频 init 到达再放行 append
    this.expectsAudio = this.audioExpectedForced;
    this.mse.hold();
    if (this.mseHoldTimer) clearTimeout(this.mseHoldTimer);
    // 兜底：某轨声明了却迟迟不来（损坏/极晚）也不永久卡死，超时放行
    // （强制等音频时用更短的兜底，避免真·纯视频流白等 12 秒）
    this.mseHoldTimer = setTimeout(
      () => {
        this.mseHoldTimer = null;
        this.mse.release();
      },
      this.audioExpectedForced ? 4000 : 12000,
    );
  }

  /**
   * SourceBuffer 创建失败 → 自愈一次：整条重建（全新 MediaSource + 重放当前 segments），
   * 重试时 audioExpectedForced=true，音频 SB 会在任何 append 之前建好。
   * **对 audio/video 对称兜底**：muxed 场景后到轨通常是音频；声画分流下相反——
   * 音频 SB 可能先建并 append（引擎初始化），video init 后到时 addSourceBuffer 必抛，
   * 旧逻辑只认 audio，video 撞限会直接成致命错误（实测报"无法创建 video SourceBuffer"）。
   * 已重建过 / 无 segments：不接管，交由控制器原有降级（禁用该轨 + 报错）。
   */
  private healSourceBufferLimit(track: string, message: string): boolean {
    if (this.destroyed || this.sbLimitHealed || this.segments.length === 0) return false;
    this.sbLimitHealed = true;
    this.audioExpectedForced = true;
    console.warn(`[MSE] ${track} SourceBuffer 创建失败，重建媒体源重试一次：${message}`);
    // 不能在自己的 appendInit 调用栈里拆自己：推到下一个任务做
    setTimeout(() => {
      if (this.destroyed) return;
      this.mse.destroy();
      this.worker?.destroy();
      this.worker = null;
      this.pcmPlayer?.flush();
      this.mse.open(() => this.beginPipeline());
    }, 0);
    return true;
  }

  /**
   * 单轨编码不受支持。
   * 例：4K 频道是 HEVC(hvc1)，在无 HEVC 的浏览器里 isTypeSupported 为假——
   * 若把它当整条流的致命错误：弹"播放错误"面板 + 触发重载恢复循环
   * （实测 CCTV4K 深链出现 4 次 Player error，而音频轨其实照播）。
   * 现在：单轨 → 跳过该轨 + CODEC_UNSUPPORTED 告警（播放器显示非阻断提示，继续播另一轨）；
   * 两轨都不支持 → 才升级为致命错误。
   */
  private handleTrackUnsupported(track: string, mime: string): void {
    this.unsupportedTracks.add(track);
    if (this.unsupportedTracks.has("video") && this.unsupportedTracks.has("audio")) {
      this.emitError({ category: "media", info: `不支持的 MIME（全部轨道）: ${mime}` });
      return;
    }
    this.emitError({
      category: "media",
      detail: PlayerErrors.CODEC_UNSUPPORTED,
      info: `不支持的 MIME: ${mime}`,
      track: track === "video" ? "video" : "audio",
    });
  }

  private releaseMseHold(): void {
    if (this.mseHoldTimer) {
      clearTimeout(this.mseHoldTimer);
      this.mseHoldTimer = null;
    }
    this.mse.release();
  }

  /**
   * 仅当「所有预期轨」的 SourceBuffer 都建好才放行统一 append。
   * 这是根除 addSourceBuffer "引擎已初始化" 与起播空洞的关键：
   * 若先到音频轨就放行，音频 media 会抢先初始化解复用器，后到的视频 init 再建
   * SourceBuffer 必失败。
   */
  private maybeReleaseHold(): void {
    const videoExpected = this.expectsVideo;
    const audioExpected = this.expectsAudio;
    // 防御：layout 上报异常（如某视频 stream_type 未认全 → expectations 全 false）时，
    // 只要任一轨的 init 已建缓冲区就放行，避免 hold 永久挂起、append 全排队卡死。
    // 正常情况下 video/audio 期望至少一个为真，不进此分支。
    if (!videoExpected && !audioExpected) {
      if (this.videoArmed || this.audioArmed) this.releaseMseHold();
      return;
    }
    const videoOk = !videoExpected || this.videoArmed;
    const audioOk = !audioExpected || this.audioArmed;
    if (videoOk && audioOk) this.releaseMseHold();
  }

  // ---- 播放控制 ----

  async play(): Promise<void> {
    if (this.pcmPlayer) {
      try {
        await this.pcmPlayer.init();
        this.pcmPlayer.resume();
      } catch {
        this.emit("audio-gate-blocked");
      }
    }
    // play() 的 rejection（NotAllowedError / 中断）必须冒泡给 UI：
    // 无缝换台 pending 失败时 UI 依赖它走硬切换兜底
    await this.mediaElement.play();
  }

  pause(): void {
    this.mediaElement.pause();
    // 显式暂停才挂起软解音频（AudioContext 时钟冻结，链保留）。
    // 不绑 video 的 pause 事件——换台/缓冲/重建 MSE 的自动 pause 不能挂起，
    // 否则 AudioContext 恢复不了 → 永久静音。
    this.pcmPlayer?.suspend();
  }

  setVolume(volume: number): void {
    this.mediaElement.volume = Math.max(0, Math.min(1, volume));
    // 软解音频是独立 WebAudio 通路：音量与静音**都必须**同步给它。
    // 旧写法只传音量、静音期间调音量会把 PCM 增益拉回满 → "显示静音却有声音"。
    if (this.pcmPlayer) {
      this.pcmPlayer.setVolume(this.mediaElement.volume);
      this.pcmPlayer.setMuted(this.mediaElement.muted);
    }
    this.emit("gain-change", this.mediaElement.volume, this.mediaElement.muted);
  }

  setMuted(muted: boolean): void {
    this.mediaElement.muted = muted;
    if (this.pcmPlayer) {
      this.pcmPlayer.setVolume(this.mediaElement.volume);
      this.pcmPlayer.setMuted(muted);
    }
    this.emit("gain-change", this.mediaElement.volume, muted);
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

  /** 可播缓冲末端（无缓冲时 undefined）。声画分流下以视频轨为准：音频链领先会让
   *  元素级并集虚高，把 seek/领先门的目标抬到视频没有数据的位置（声画错乱来源之一）。 */
  private bufferedEnd(): number | undefined {
    const videoRanges = this.bufferedRanges.get("video");
    if (videoRanges && videoRanges.length > 0) return videoRanges[videoRanges.length - 1].end;
    const b = this.mediaElement.buffered;
    return b.length > 0 ? b.end(b.length - 1) : undefined;
  }

  seek(seconds: number): void {
    const v = this.mediaElement;
    // 防跳洞：目标超出缓冲末端时 clamp（直播缓冲不足时 wall-clock 推算的目标
    // 常远超缓冲——seek 到空洞会卡死，实测 goLive 目标 243s vs 缓冲仅 37s）。
    const end = this.bufferedEnd();
    const max =
      end !== undefined ? Math.min(end, Number.isFinite(v.duration) ? v.duration : end) : Number.isFinite(v.duration) ? v.duration : seconds;
    v.currentTime = Math.max(0, Math.min(seconds, max));
  }

  goLive(targetMseSeconds?: number): void {
    // 缺省目标基于实际缓冲末端（continuous-live-ts 的起播目标）：
    // wall-clock 外推的直播边（goLiveTargetMse）在缓冲不足时远超缓冲 → 跳洞卡死。
    // 传入的目标仍由 seek() 兜底 clamp 到缓冲末端。
    const end = this.bufferedEnd();
    const target =
      targetMseSeconds ??
      (end !== undefined ? Math.max(0, end - (this.options.targetLatencySec ?? TARGET_LATENCY)) : undefined);
    if (target === undefined) return;
    this.seek(target);
  }

  setLiveSessionAnchor(anchor: LiveSessionAnchor): void {
    this.anchor = anchor;
  }

  /** 当前落后直播边多少秒（无锚点时返回 0）。 */
  get lag(): number {
    return this.anchor ? lagBehindLiveEdge(this.anchor, this.mediaElement.currentTime) : 0;
  }

  setLiveSync(enabled: boolean): void {
    this.playback.setupLiveSync(enabled);
  }

  /** 运行时切换软解音频声道输出模式（mono = 左右声道合成）；PCM 播放器尚未创建时先记住。 */
  setAudioChannelMode(mode: "stereo" | "mono"): void {
    this.audioChannelMode = mode;
    this.pcmPlayer?.setAudioChannelMode(mode);
  }

  // ==================== Worker 流控 ====================

  /**
   * 背压暂停拉流（MSE 缓冲真满，来自 QuotaExceededError）。**页面隐藏时不暂停**：
   * 后台 UA 会冻结视频、节流定时器，一旦停掉 fetch + 软解，后台播放立即静音
   * （后台语义 = 音频 free-run；worker 侧缓冲领先门在 hidden 时也自行放行）。
   * 只记录状态，等回前台或缓冲腾出后恢复。
   */
  private pauseWorkerForBackpressure(): void {
    this.backpressurePaused = true;
    if (document.hidden) return;
    this.worker?.pause();
  }

  private resumeWorkerFromBackpressure(): void {
    this.backpressurePaused = false;
    this.worker?.resume();
  }

  /** 上报播放头 + MSE 缓冲末端给 worker 的缓冲领先门（250ms 周期）。 */
  private postClock(): void {
    if (!this.worker) return;
    const end = this.bufferedEnd();
    this.worker.clock(this.mediaElement.currentTime * 1000, end === undefined ? -1 : end * 1000, document.hidden);
  }

  setAutoDeinterlace(enabled: boolean): void {
    this.autoDeinterlace = enabled;
    this.renderer?.setDeinterlace(enabled);
    this.emitRenderState();
  }

  setPictureEnhancement(enabled: boolean): void {
    this.pictureEnhancement = enabled;
    this.renderer?.setEnhancement(enabled);
    this.emitRenderState();
  }

  private emitRenderState(): void {
    if (this.renderer) {
      const s = this.renderer.state;
      this.emit("painter-state", {
        active: s.active && s.supported,
        deinterlacing: s.deinterlacing,
      });
      return;
    }
    this.emit("painter-state", { active: false, deinterlacing: false });
  }

  get currentMediaInfo(): PlayerMediaInfo | null {
    return this.mediaInfo;
  }

  /** 软解待解字节数（调试用）。软解已移至 worker，主线程不再持有该状态。 */
  get softDecodePendingBytes(): number {
    return 0;
  }

  get isAutoDeinterlaceEnabled(): boolean {
    return this.autoDeinterlace;
  }

  stop(): void {
    if (this.mseHoldTimer) {
      clearTimeout(this.mseHoldTimer);
      this.mseHoldTimer = null;
    }
    this.worker?.stop();
    this.pcmPlayer?.flush();
    this.playback.reset();
    this.mediaElement.pause();
  }

  destroy(): void {
    this.destroyed = true;
    if (this.tickTimer !== null) {
      clearInterval(this.tickTimer);
      this.tickTimer = null;
    }
    this.worker?.destroy();
    this.worker = null;
    this.pcmPlayer?.destroy();
    this.pcmPlayer = null;
    this.renderer?.destroy();
    this.renderer = null;
    if (this.visibilityListener) {
      document.removeEventListener("visibilitychange", this.visibilityListener);
      this.visibilityListener = null;
    }
    this.playback.destroy();
    this.mse.destroy();
    this.emitter.clear();
  }
}

/** 合并多轨缓冲区间（union），供直播边估计使用。 */
function unionRanges(byTrack: ReadonlyMap<string, BufferedRange[]>): BufferedRange[] {
  const all: { start: number; end: number }[] = [];
  for (const ranges of byTrack.values()) {
    for (const r of ranges) all.push({ start: r.start, end: r.end });
  }
  if (all.length === 0) return [];
  all.sort((a, b) => a.start - b.start);
  const out: BufferedRange[] = [];
  let cur = { start: all[0].start, end: all[0].end };
  for (let i = 1; i < all.length; i++) {
    const r = all[i];
    if (r.start <= cur.end) {
      cur.end = Math.max(cur.end, r.end);
    } else {
      out.push(cur);
      cur = { start: r.start, end: r.end };
    }
  }
  out.push(cur);
  return out;
}

/** 把交错 PCM 拆成每通道一条。 */
function deinterleave(interleaved: Float32Array, channels: number, samplesPerChannel: number): Float32Array[] {
  const out: Float32Array[] = [];
  const n = Math.min(channels, Math.max(1, Math.floor(interleaved.length / Math.max(1, samplesPerChannel))));
  for (let c = 0; c < n; c++) {
    const ch = new Float32Array(samplesPerChannel);
    for (let i = 0; i < samplesPerChannel; i++) ch[i] = interleaved[i * n + c];
    out.push(ch);
  }
  if (out.length === 0) out.push(interleaved);
  return out;
}
