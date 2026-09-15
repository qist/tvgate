import type { PlayerConfig } from "../config";
import { createDefaultConfig } from "../config";
import { type SoftAudioCodec, WorkerAudioDecoder } from "../decoder/worker-audio-decoder";
import TSDemuxer from "../demux/ts-demuxer";
import { type DemuxErrorDetail, type LoaderErrorDetail, PlayerErrors } from "../errors";
import FLVDemuxer, { type FLVProbeResult } from "../demux/flv-demuxer";
import {
  containsMoov,
  getSegmentStartTime,
  type InitSegmentTrackInfo,
  parseInitSegment,
  probeFmp4,
  splitInitFromSegment,
} from "../hls/fmp4";
import { type HlsInfo, HlsRequestError, HlsSource } from "../hls/hls-source";
import FetchLoader, { type LoaderErrorInfo } from "../io/fetch-loader";
import { identifyAudioCodec, identifyVideoCodec } from "../media-codecs";
import MP4Remuxer, { type PcmTimestampMapping } from "../remux/mp4-remuxer";
import type { PlayerDynamicRange, PlayerMediaInfo, PlayerSegment } from "../types";
import Log from "../utils/logger";
import type { PcmWorkerStats } from "./messages";
import {
  ContinuousLiveSegmentSource,
  type SegmentMeta,
  type SegmentSource,
  StaticSegmentSource,
} from "./segment-source";

export interface PipelineCallbacks {
  onInitSegment: (
    type: string,
    initSegment: {
      type: string;
      container: string;
      codec?: string;
      data?: ArrayBuffer;
      [key: string]: unknown;
    },
  ) => void;
  onMediaSegment: (
    type: string,
    mediaSegment: {
      type: string;
      data?: ArrayBuffer;
      timestampOffset?: number;
      [key: string]: unknown;
    },
  ) => void;
  onLoadingComplete: () => void;
  onIOError: (type: LoaderErrorDetail, info: LoaderErrorInfo) => void;
  onDemuxError: (type: DemuxErrorDetail, info: string) => void;
  onHlsInfo: (info: HlsInfo) => void;
  onMediaInfo: (info: PlayerMediaInfo) => void;
  /** The separate audio rendition was given up; main thread may resume video-only flow. */
  onAudioDisabled: () => void;
  /** `time` is normalized to the MSE timeline (seconds, same space as video.currentTime). */
  onPCMAudioData: (pcm: Float32Array, channels: number, sampleRate: number, time: number) => void;
  /** Audio track discontinuity detected: main thread should re-anchor the PCM player. */
  onPCMAudioDiscontinuity: () => void;
  /** Separate-audio rendition runs soft decode: no MSE audio init will ever be sent. */
  onAudioRenditionSoftDecode: () => void;
}

class LoadError extends Error {
  constructor(
    public errorType: LoaderErrorDetail,
    public info: LoaderErrorInfo,
  ) {
    super(info.msg);
  }
}

const HLS_URL_RE = /\.m3u8?($|\?)/i;
/** Sentinel rejection value for intentionally cancelled segment loads. */
const CANCELLED = Symbol("cancelled");
type SourceMode = "continuous-live-ts" | "static-ts-list" | "hls";
const PCR_TIMESCALE = 90000;
const BITRATE_MEDIA_WINDOW_MS = 5000;
const BITRATE_STABLE_AFTER_MS = 500;
const BITRATE_UPDATE_INTERVAL_MS = 1000;
const BITRATE_MINIMUM_SAMPLES = 2;
const SEGMENT_BITRATE_SAMPLE_COUNT = 5;
// 直播边缘分片可能已被上游 CDN 驱逐（404）：跳过并等播放列表刷新追上，
// 连续失败达到该上限才判定流不可用（每次成功加载后清零）。
const MAX_LIVE_SEGMENT_SKIPS = 8;

/**
 * 音频 PTS 重新锚定阈值（ms）。部分源（如 E-AC-3 4K）的复用器每到 ~10s 边界会
 * 把音频 PTS 相位整体提前固定量（实测 256ms、1024ms、以及 ~10048ms 的大周期跳变），
 * 但音频内容本身连续。该恒定阈值必须大于这些周期跳变，否则会每 N 秒触发一次
 * re-锚 + bridging、周期性压缩音频时间轴，长时间累积成"声音慢慢跑前面"并造成
 * 起播 PTS 周期 jumps 使 A/V 起始即错位。只对真正的切台/节目级不连续
 * （通常 > 20s）才重锚。
 */
const AUDIO_PTS_REANCHOR_THRESHOLD_MS = 20000;
/** 自由时钟(诊断用)与源 PTS 偏差达到该值就打印一次，用于定位"声音慢慢跑前面"。 */
const AUDIO_PTS_DIVERGENCE_LOG_MS = 30;

/**
 * 缓冲领先上限（ms）：视频 MSE 缓冲末尾领先播放头超过该值就不继续解/解码下一段，
 * 对齐 ac3-lab 的 `waitForBufferRoom`（10s），防止软解 PCM 时间轴跑到播放头前太远。
 */
const LEAD_BUFFER_AHEAD_MS = 10000;
/** 超过该时长没收到主线程 clock 消息（如切后台心跳被节流）则视为时基过期，停止等待避免死锁。 */
const CLOCK_STALE_MS = 1000;
/** 缓冲领先门轮询间隔（ms）。 */
const LEAD_GATE_POLL_MS = 100;

type MediaInfoVideo = NonNullable<PlayerMediaInfo["video"]>;
type MediaInfoAudio = NonNullable<PlayerMediaInfo["audio"]>;

/** 简单的异步延迟（让出事件循环以接收 clock/pause/reset 等消息）。 */
function sleep(ms: number): Promise<void> {
  return new Promise<void>((resolve) => {
    setTimeout(resolve, ms);
  });
}

function mergeDefinedProperties<T extends object>(current: T | undefined, update: T): T {
  const merged = { ...(current ?? {}) } as Record<string, unknown>;
  for (const [property, value] of Object.entries(update)) {
    if (value !== undefined) {
      merged[property] = value;
    }
  }
  return merged as T;
}

function dynamicRangeFromTransfer(transferCharacteristics: unknown): PlayerDynamicRange | undefined {
  if (transferCharacteristics === 16) return "hdr10";
  if (transferCharacteristics === 18) return "hlg";
  if ([1, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 17].includes(transferCharacteristics as number)) {
    return "sdr";
  }
  return undefined;
}

function dynamicRangeFromHlsVideoRange(videoRange: string | undefined): PlayerDynamicRange | undefined {
  switch (videoRange?.toUpperCase()) {
    case "PQ":
      return "hdr10";
    case "HLG":
      return "hlg";
    case "SDR":
      return "sdr";
    default:
      return undefined;
  }
}

/** Copy a Uint8Array view into a standalone (transferable) ArrayBuffer. */
function toArrayBuffer(view: Uint8Array): ArrayBuffer {
  if (view.byteOffset === 0 && view.byteLength === view.buffer.byteLength) {
    return view.buffer as ArrayBuffer;
  }
  return view.slice().buffer as ArrayBuffer;
}

class Pipeline {
  private readonly TAG = "Pipeline";

  private _config: PlayerConfig;
  private _callbacks: PipelineCallbacks;

  private _initialSegments: PlayerSegment[];

  /** Increments to invalidate the currently running load loop. */
  private _runId = 0;

  private _source: SegmentSource | null = null;
  private _hlsSource: HlsSource | null = null;
  private _sourceMode: SourceMode = "static-ts-list";

  private _demuxer: TSDemuxer | FLVDemuxer | null = null;
  private _remuxer: MP4Remuxer | null = null;
  /** Separate pair for HLS audio renditions (EXT-X-MEDIA), fed by track:"audio" segments. */
  private _audioDemuxer: TSDemuxer | null = null;
  private _audioRemuxer: MP4Remuxer | null = null;
  private _ioctl: FetchLoader | null = null;
  /** Settles the in-flight segment load promise (so a cancelled loop can exit). */
  private _cancelLoad: (() => void) | null = null;

  /** 直播模式连续跳过的分片数（成功加载一次即清零） */
  private _liveSegmentSkips = 0;

  private _paused = false;
  private _resumeGate: (() => void) | null = null;
  /** dts offset (ms) to apply when the remuxer is next created (HLS discontinuity / seek). */
  private _pendingDtsOffsetMs = 0;

  // --- fMP4 passthrough state ---
  private _fmp4Mode = false;
  private _fmp4InitSent = false;
  private _fmp4Chunks: Uint8Array[] = [];
  private _lastInitUrl: string | null = null;
  private _fmp4Timescales = new Map<number, number>();
  private _fmp4TimestampOffsetWarningLogged = false;

  // --- player-facing media metadata ---
  private _mediaInfo: PlayerMediaInfo = {};
  private _serializedMediaInfo = "{}";
  private _hasActualVideoInfo = false;
  private _hasActualAudioInfo = false;
  private _advertisedBitrate: number | undefined;
  private _lastHlsInfo: HlsInfo | undefined;
  private _tsBitrateSamples: Array<{ pcrBase: number; bytePosition: number }> = [];
  private _segmentBitrateSamples: number[] = [];
  private _currentSegmentBytes = 0;
  private _lastBitrateUpdatePcr = 0;
  private _measuredBitrateStable = false;

  private _workerAudioDecoder: WorkerAudioDecoder | null = null;
  private _workerAudioDecoderInitPromise: Promise<boolean> | null = null;

  // --- MP2 software decode timing state ---
  /** PTS anchor (ms) for sample-count extrapolation across PES packets. */
  private _audioAnchorPtsMs: number | null = null;
  private _audioSamplesSinceAnchor = 0;
  private _audioSampleRate = 0;

  /** 上次打印"自由时钟 vs 源 PTS"偏差的时间戳（performance.now()），避免刷屏。 */
  private _lastPtsDivergenceLogAt = 0;
  /** PCM decoded before the remuxer dts base is known (flushed once available). */
  private _pendingPcm: Array<{
    pcm: Float32Array;
    channels: number;
    sampleRate: number;
    ptsMs: number;
    durationMs: number;
  }> = [];
  /** Incremented on audio timing resets to invalidate decode callbacks queued before the reset. */
  private _audioGen = 0;

  // --- 分离音轨（EXT-X-MEDIA）软解 ---
  private _audioRenditionDecoder: WorkerAudioDecoder | null = null;
  private _audioRenditionDecoderCodec: SoftAudioCodec | null = null;
  private _audioRenditionDecoderInitPromise: Promise<boolean> | null = null;
  /** 首个软解 rendition 帧的源 PTS（ms）；rendition 源轴与视频轴不同纪元，后续帧全部相对它重基。 */
  private _renditionBasePtsMs: number | null = null;
  /** 首个 rendition 原始帧已证实该音轨走软解（无 MSE audio init）；只向主线程通告一次。 */
  private _renditionSoftDecodeAnnounced = false;
  /** 软解 PCM 来源（muxed / rendition，拓扑上互斥；同时出现即计数告警）。 */
  private _pcmSource: "muxed" | "rendition" | null = null;

  // --- 软解 PCM 链路丢弃计数（pcm-audio-stats 周期上报）---
  private _pcmStats: PcmWorkerStats = {
    pendingOverflowDrops: 0,
    queueOverflowDrops: 0,
    remuxDrop: 0,
    trimDrop: 0,
    genDrops: 0,
    decodeInitFailed: 0,
    carryFrames: 0,
    renditionFrames: 0,
    dualSourceFrames: 0,
  };
 
  // --- 缓冲领先限速（对齐 ac3-lab 的 waitForBufferRoom）---
  /** 当前播放头时间（ms），由主线程经 clock 消息周期上报。 */
  private _playheadCurrentMs = -1;
  /** 视频 MSE 缓冲末尾（ms）；-1 表示未知。 */
  private _playheadBufferedEndMs = -1;
  /** 最近一次 clock 消息到达时刻（performance.now()），用于判定时基是否新鲜。 */
  private _lastClockArrivalMs = -1;
  /** 页面隐藏标志（随 clock 消息更新）。后台时缓冲领先门必须放行 —— 后台播放以音频为主导。 */
  private _pageHidden = false;

  constructor(segments: PlayerSegment[], config: PlayerConfig, callbacks: PipelineCallbacks) {
    this._callbacks = callbacks;
    this._config = { ...createDefaultConfig(), ...config };
    this._initialSegments = segments;
  }

  start(): void {
    this._load(this._initialSegments);
  }

  loadSegments(newSegments: PlayerSegment[]): void {
    this._load(newSegments);
  }

  pause(): void {
    this._paused = true;
    // Continuous live TS streams pause mid-fetch and resume with a fresh request.
    if (this._sourceMode === "continuous-live-ts") {
      this._ioctl?.pause();
    }
  }

  resume(): void {
    this._paused = false;
    if (this._sourceMode === "continuous-live-ts") {
      this._ioctl?.resume();
    }
    this._resumeGate?.();
    this._resumeGate = null;
  }

  destroy(): void {
    this._runId++;
    this._teardown();
    if (this._workerAudioDecoder) {
      this._workerAudioDecoder.destroy();
      this._workerAudioDecoder = null;
    }
    this._workerAudioDecoderInitPromise = null;
    this._workerDecoderCodec = null;
    if (this._audioRenditionDecoder) {
      this._audioRenditionDecoder.destroy();
      this._audioRenditionDecoder = null;
    }
    this._audioRenditionDecoderInitPromise = null;
    this._audioRenditionDecoderCodec = null;
  }

  // ---- Private methods ----

  private _publishMediaInfo(nextMediaInfo: PlayerMediaInfo): void {
    const serialized = JSON.stringify(nextMediaInfo);
    if (serialized === this._serializedMediaInfo) return;

    this._mediaInfo = nextMediaInfo;
    this._serializedMediaInfo = serialized;
    this._callbacks.onMediaInfo({
      ...nextMediaInfo,
      video: nextMediaInfo.video ? { ...nextMediaInfo.video } : undefined,
      audio: nextMediaInfo.audio ? { ...nextMediaInfo.audio } : undefined,
      bitrate: nextMediaInfo.bitrate ? { ...nextMediaInfo.bitrate } : undefined,
    });
  }

  private _mergeMediaInfo(update: PlayerMediaInfo): void {
    this._publishMediaInfo({
      ...this._mediaInfo,
      video: update.video
        ? mergeDefinedProperties(this._mediaInfo.video, update.video as MediaInfoVideo)
        : this._mediaInfo.video,
      audio: update.audio
        ? mergeDefinedProperties(this._mediaInfo.audio, update.audio as MediaInfoAudio)
        : this._mediaInfo.audio,
      bitrate: update.bitrate ?? this._mediaInfo.bitrate,
    });
  }

  private _replaceBitrate(bitrate: PlayerMediaInfo["bitrate"]): void {
    const nextMediaInfo = { ...this._mediaInfo };
    if (bitrate) {
      nextMediaInfo.bitrate = bitrate;
    } else {
      delete nextMediaInfo.bitrate;
    }
    this._publishMediaInfo(nextMediaInfo);
  }

  private _resetMediaInfo(): void {
    this._mediaInfo = {};
    this._serializedMediaInfo = "{}";
    this._hasActualVideoInfo = false;
    this._hasActualAudioInfo = false;
    this._advertisedBitrate = undefined;
    this._lastHlsInfo = undefined;
    this._tsBitrateSamples = [];
    this._segmentBitrateSamples = [];
    this._currentSegmentBytes = 0;
    this._lastBitrateUpdatePcr = 0;
    this._measuredBitrateStable = false;
    // A new generation must clear the previous stream's badges immediately.
    this._callbacks.onMediaInfo({});
  }

  private _resetBitrateMeasurement(): void {
    this._tsBitrateSamples = [];
    this._segmentBitrateSamples = [];
    this._currentSegmentBytes = 0;
    this._lastBitrateUpdatePcr = 0;
    this._measuredBitrateStable = false;
    this._replaceBitrate(
      this._advertisedBitrate ? { bitsPerSecond: this._advertisedBitrate, source: "advertised" } : undefined,
    );
  }

  private _resetPeriodMediaInfo(): void {
    this._hasActualVideoInfo = false;
    this._hasActualAudioInfo = false;
    this._publishMediaInfo(this._mediaInfo.bitrate ? { bitrate: { ...this._mediaInfo.bitrate } } : {});
    if (this._lastHlsInfo) {
      this._applyHlsInfo(this._lastHlsInfo);
    }
  }

  private _recordInputBytes(bytes: number): void {
    if (bytes <= 0) return;

    // Segmented fMP4 has no PCR, so retain the complete-segment fallback.
    if (this._sourceMode !== "continuous-live-ts") {
      this._currentSegmentBytes += bytes;
    }
  }

  private _recordTsPcr(pcrBase: number, bytePosition: number, discontinuity: boolean): void {
    if (this._sourceMode === "hls" || !Number.isFinite(pcrBase) || !Number.isFinite(bytePosition)) return;

    const previousSample = this._tsBitrateSamples.at(-1);
    if (
      discontinuity ||
      (previousSample !== undefined &&
        (pcrBase <= previousSample.pcrBase || bytePosition <= previousSample.bytePosition))
    ) {
      this._tsBitrateSamples = [];
      this._lastBitrateUpdatePcr = 0;
    }

    this._tsBitrateSamples.push({ pcrBase, bytePosition });
    const windowStart = pcrBase - (BITRATE_MEDIA_WINDOW_MS * PCR_TIMESCALE) / 1000;
    while (this._tsBitrateSamples.length > 1 && this._tsBitrateSamples[1].pcrBase <= windowStart) {
      this._tsBitrateSamples.shift();
    }

    const firstSample = this._tsBitrateSamples[0];
    const lastSample = this._tsBitrateSamples[this._tsBitrateSamples.length - 1];
    if (!firstSample || !lastSample) return;
    const elapsedPcr = lastSample.pcrBase - firstSample.pcrBase;
    const elapsedMediaMilliseconds = (elapsedPcr * 1000) / PCR_TIMESCALE;
    const estimateStable =
      elapsedMediaMilliseconds >= BITRATE_STABLE_AFTER_MS && this._tsBitrateSamples.length >= BITRATE_MINIMUM_SAMPLES;
    const updateTooSoon =
      this._lastBitrateUpdatePcr > 0 &&
      ((pcrBase - this._lastBitrateUpdatePcr) * 1000) / PCR_TIMESCALE < BITRATE_UPDATE_INTERVAL_MS;
    if (!estimateStable || updateTooSoon) return;

    const totalBytes = lastSample.bytePosition - firstSample.bytePosition;
    // PCR advances with the media timeline, so server-side delivery bursts do
    // not inflate this transport-stream bitrate as wall-clock sampling would.
    const bitsPerSecond = Math.round((totalBytes * 8 * PCR_TIMESCALE) / elapsedPcr / 1000) * 1000;
    if (!Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0) return;

    this._lastBitrateUpdatePcr = pcrBase;
    this._measuredBitrateStable = true;
    this._replaceBitrate({ bitsPerSecond, source: "measured" });
  }

  private _publishSegmentBitrate(durationSeconds: number): void {
    if (durationSeconds <= 0 || this._currentSegmentBytes <= 0) return;

    const sampleBitsPerSecond = (this._currentSegmentBytes * 8) / durationSeconds;
    if (!Number.isFinite(sampleBitsPerSecond) || sampleBitsPerSecond <= 0) return;

    this._segmentBitrateSamples.push(sampleBitsPerSecond);
    if (this._segmentBitrateSamples.length > SEGMENT_BITRATE_SAMPLE_COUNT) {
      this._segmentBitrateSamples.shift();
    }
    const averageBitsPerSecond =
      this._segmentBitrateSamples.reduce((sum, bitsPerSecond) => sum + bitsPerSecond, 0) /
      this._segmentBitrateSamples.length;
    const roundedBitsPerSecond = Math.round(averageBitsPerSecond / 1000) * 1000;
    if (!Number.isFinite(roundedBitsPerSecond) || roundedBitsPerSecond <= 0) return;

    this._measuredBitrateStable = true;
    this._replaceBitrate({ bitsPerSecond: roundedBitsPerSecond, source: "measured" });
  }

  private _handleHlsInfo(info: HlsInfo): void {
    this._lastHlsInfo = info;
    this._applyHlsInfo(info);
  }

  private _applyHlsInfo(info: HlsInfo): void {
    const codecHints =
      info.codecs
        ?.split(",")
        .map((codec) => codec.trim())
        .filter(Boolean) ?? [];
    const videoCodec = codecHints.find((codec) => identifyVideoCodec(codec) !== undefined);
    const audioCodec = codecHints.find((codec) => identifyAudioCodec(codec) !== undefined);

    const update: PlayerMediaInfo = {};
    if (!this._hasActualVideoInfo) {
      const videoHints: MediaInfoVideo = {
        codec: videoCodec,
        width: info.resolution?.width,
        height: info.resolution?.height,
        frameRate: info.frameRate,
        dynamicRange: dynamicRangeFromHlsVideoRange(info.videoRange),
      };
      if (Object.values(videoHints).some((value) => value !== undefined)) {
        update.video = videoHints;
      }
    }
    if (!this._hasActualAudioInfo && audioCodec) {
      update.audio = { codec: audioCodec };
    }

    const advertisedBitrate = info.averageBandwidth ?? info.bandwidth;
    if (advertisedBitrate && advertisedBitrate > 0) {
      this._advertisedBitrate = advertisedBitrate;
      if (!this._measuredBitrateStable) {
        update.bitrate = { bitsPerSecond: advertisedBitrate, source: "advertised" };
      }
    }
    this._mergeMediaInfo(update);
  }

  private _handleTsTrackMetadata(type: string, metadata: unknown): void {
    if (!metadata || typeof metadata !== "object") return;
    const trackMetadata = metadata as Record<string, unknown>;

    if (type === "video") {
      const frameRateMetadata = trackMetadata.frameRate;
      const frameRate =
        frameRateMetadata && typeof frameRateMetadata === "object"
          ? (frameRateMetadata as Record<string, unknown>).fps
          : frameRateMetadata;
      this._hasActualVideoInfo = true;
      this._mergeMediaInfo({
        video: {
          codec: typeof trackMetadata.codec === "string" ? trackMetadata.codec : undefined,
          width: typeof trackMetadata.presentWidth === "number" ? trackMetadata.presentWidth : undefined,
          height: typeof trackMetadata.presentHeight === "number" ? trackMetadata.presentHeight : undefined,
          scanType:
            trackMetadata.mayBeInterlaced === true
              ? "interlaced"
              : trackMetadata.mayBeInterlaced === false
                ? "progressive"
                : undefined,
          frameRate: typeof frameRate === "number" && frameRate > 0 ? frameRate : undefined,
          dynamicRange: dynamicRangeFromTransfer(trackMetadata.transferCharacteristics),
        },
      });
      return;
    }

    if (type === "audio") {
      const sourceCodec = trackMetadata.sourceCodec ?? trackMetadata.originalCodec ?? trackMetadata.codec;
      this._hasActualAudioInfo = true;
      this._mergeMediaInfo({
        audio: {
          codec: typeof sourceCodec === "string" ? sourceCodec : undefined,
          channelCount: typeof trackMetadata.channelCount === "number" ? trackMetadata.channelCount : undefined,
        },
      });
    }
  }

  private _handleFmp4TrackMetadata(track: InitSegmentTrackInfo): void {
    if (track.type === "video") {
      this._hasActualVideoInfo = true;
      this._mergeMediaInfo({
        video: {
          codec: track.codec,
          width: track.width,
          height: track.height,
          scanType: track.scanType,
          dynamicRange: dynamicRangeFromTransfer(track.transferCharacteristics),
        },
      });
    } else {
      this._hasActualAudioInfo = true;
      this._mergeMediaInfo({ audio: { codec: track.codec, channelCount: track.channelCount } });
    }
  }

  private _load(segments: PlayerSegment[]): void {
    this._runId++;
    this._teardown();
    this._resetMediaInfo();

    // Reset WASM audio decoder state (clear stale mdct/qmf + carry from previous stream)
    this._workerAudioDecoder?.reset();
    this._resetAudioTiming();

    const firstSegment = segments[0];
    if (!firstSegment) return;
    const url = firstSegment.url;
    const isHls = segments.length === 1 && HLS_URL_RE.test(url);
    const isContinuousLiveTs = segments.length === 1 && !isHls && (firstSegment.duration ?? 0) === 0;

    this._sourceMode = isHls ? "hls" : isContinuousLiveTs ? "continuous-live-ts" : "static-ts-list";

    if (isHls) {
      // Fast path: known playlist URL, skip the content-type detection round-trip
      this._startHls(url);
    } else if (isContinuousLiveTs) {
      this._source = new ContinuousLiveSegmentSource(firstSegment);
      void this._run(this._runId);
    } else {
      this._source = new StaticSegmentSource(segments);
      void this._run(this._runId);
    }
  }

  private _startHls(url: string, preloaded?: { text: string; url: string }): void {
    this._sourceMode = "hls";
    const hls = new HlsSource(url, this._config, preloaded);
    hls.onInfo = (info) => {
      this._callbacks.onHlsInfo(info);
      this._handleHlsInfo(info);
    };
    hls.onAudioDisabled = () => this._callbacks.onAudioDisabled();
    this._hlsSource = hls;
    this._source = hls;
    void this._run(this._runId);
  }

  /** Stop all loading and demux/remux state, keeping the worker reusable. */
  private _teardown(): void {
    this._abortCurrentLoad();
    this._source?.destroy();
    this._source = null;
    this._hlsSource = null;
    if (this._demuxer) {
      this._demuxer.destroy();
      this._demuxer = null;
    }
    if (this._remuxer) {
      this._remuxer.destroy();
      this._remuxer = null;
    }
    if (this._audioDemuxer) {
      this._audioDemuxer.destroy();
      this._audioDemuxer = null;
    }
    if (this._audioRemuxer) {
      this._audioRemuxer.destroy();
      this._audioRemuxer = null;
    }
    this._renditionBasePtsMs = null;
    this._renditionSoftDecodeAnnounced = false;
    this._pcmSource = null;
    this._pendingDtsOffsetMs = 0;
    this._fmp4Mode = false;
    this._fmp4InitSent = false;
    this._fmp4Chunks = [];
    this._lastInitUrl = null;
    this._fmp4Timescales = new Map();
    this._fmp4TimestampOffsetWarningLogged = false;
    this._paused = false;
    this._sourceMode = "static-ts-list";
    this._resumeGate?.();
    this._resumeGate = null;
  }

  private _abortCurrentLoad(): void {
    if (this._ioctl) {
      this._ioctl.destroy();
      this._ioctl = null;
    }
    this._cancelLoad?.();
    this._cancelLoad = null;
  }

  /** Block until unpaused or this run is superseded (seek / reload). */
  private async _waitIfPaused(runId: number): Promise<boolean> {
    while (this._paused && this._runId === runId) {
      await new Promise<void>((resolve) => {
        this._resumeGate = resolve;
      });
    }
    return this._runId === runId;
  }

  /**
   * 缓冲领先限速门（对齐 ac3-lab 的 `waitForBufferRoom`）：当 MSE 缓冲末尾领先播放头超过
   * LEAD_BUFFER_AHEAD_MS 时暂停拉流/解码，直到播放头追上来。仅直播生效，且依赖主线程经
   * `clock` 命令周期上报的播放头；时基过期或播放头未知时直接放行，绝不死锁、绝不卡播放。
   *
   * 页面隐藏时**无条件放行**：后台 timer 节流让播放头几乎不走，缓冲永远"满"，门会永久
   * 闭合 → 拉流/解封装/软解全停 → 音频零产出（后台静音；CLOCK_STALE_MS=1000 的过期放行
   * 与 Chrome 后台 1s timer 节流同量级，靠它兜底不可靠）。后台播放语义是音频 free-run
   * （v3.2.1 同款），视频供流多拉的部分由 MSE 侧 backpressure 吸收。
   */
  private async _waitForBufferLead(runId: number): Promise<boolean> {
    if (!this._isLivePlayback()) return true;
    if (this._pageHidden) return true;
    if (this._playheadCurrentMs < 0 || this._playheadBufferedEndMs < 0) return true;
    if (performance.now() - this._lastClockArrivalMs > CLOCK_STALE_MS) return true;

    while (this._runId === runId && this._isLivePlayback()) {
      if (this._pageHidden) return true;
      if (this._paused) {
        if (!(await this._waitIfPaused(runId))) return false;
      }
      if (this._playheadCurrentMs < 0 || this._playheadBufferedEndMs < 0) return true;
      if (performance.now() - this._lastClockArrivalMs > CLOCK_STALE_MS) return true;
      if (this._playheadBufferedEndMs - this._playheadCurrentMs <= LEAD_BUFFER_AHEAD_MS) return true;
      await sleep(LEAD_GATE_POLL_MS);
    }
    return this._runId === runId;
  }

  // ---- Load loop ----

  private async _run(runId: number): Promise<void> {
    const source = this._source;
    if (!source) return;

    while (this._runId === runId) {
      if (!(await this._waitIfPaused(runId))) return;
      if (!(await this._waitForBufferLead(runId))) return;

      let meta: SegmentMeta | null;
      try {
        meta = await source.next();
      } catch (e) {
        if (this._runId === runId) {
          const error = e as Error;
          Log.e(this.TAG, `Segment source failed: ${error.message}`);
          if (e instanceof HlsRequestError) {
            this._callbacks.onIOError(
              e.code !== undefined ? PlayerErrors.HTTP_STATUS_CODE_INVALID : PlayerErrors.REQUEST_FAILED,
              {
                code: e.code ?? -1,
                msg: e.statusText || e.message,
                url: e.url,
              },
            );
          } else {
            this._callbacks.onIOError(PlayerErrors.EXCEPTION, { code: -1, msg: error.message });
          }
        }
        return;
      }
      if (this._runId !== runId) return;

      if (!meta) {
        this._demuxer?.flushSegmentBoundary();
        this._remuxer?.flushStashedSamples();
        this._callbacks.onLoadingComplete();
        return;
      }

      try {
        const isAudioTrack = meta.track === "audio";
        if (meta.resetRemuxer) {
          const initSegmentChanged = meta.initUrl !== undefined && meta.initUrl !== this._lastInitUrl;
          if (isAudioTrack) {
            this._resetAudioTransmux();
          } else {
            this._resetTransmux(meta.start, !this._fmp4Mode || initSegmentChanged);
          }
        }
        if (!isAudioTrack && meta.initUrl && meta.initUrl !== this._lastInitUrl) {
          if (!(await this._waitIfPaused(runId))) return;
          await this._loadFmp4Init(meta.initUrl, runId);
          if (this._runId !== runId) return;
          this._lastInitUrl = meta.initUrl;
        }
        if (!(await this._waitIfPaused(runId))) return;
        // Per-segment A/V sync correction: re-anchor the audio remuxer's next
        // output batch at this segment's mapped playlist position (see
        // setAudioSegmentStartTarget). No-op when the remuxer is about to be
        // recreated — the fresh instance anchors via setDtsBaseOffset.
        if (isAudioTrack) {
          this._audioRemuxer?.setAudioSegmentStartTarget(meta.start * 1000);
        }
        // Audio rendition bytes accumulate into the next video segment's bitrate sample
        if (!isAudioTrack) {
          this._currentSegmentBytes = 0;
        }
        await this._loadSegment(meta);
        if (this._runId !== runId) return;
        this._liveSegmentSkips = 0;
        if (!isAudioTrack && (this._sourceMode === "hls" || this._fmp4Mode)) {
          this._publishSegmentBitrate(meta.duration);
        }

        if (this._fmp4Mode && !isAudioTrack) {
          this._flushFmp4Segment(meta);
        } else if (this._hlsSource) {
          // Keep each HLS TS input segment as a hard batching boundary. Codec
          // metadata remains reusable, but all complete media samples must be
          // emitted before loading the next playlist segment.
          if (isAudioTrack) {
            this._audioDemuxer?.flushSegmentBoundary();
            this._audioRemuxer?.flushStashedSamples();
          } else {
            this._demuxer?.flushSegmentBoundary();
            this._remuxer?.flushStashedSamples();
          }
        } else if (!isAudioTrack) {
          this._finishTsInputBoundary();
        }
      } catch (e) {
        if (this._runId !== runId || e === CANCELLED) return;
        if (this._isLiveSource()) {
          // 直播窗口：无论 CDN 404 还是解封装偶发异常，都不应永久打死播放循环。
          // 计入同一跳过预算，达到上限再放弃，避免单帧错误造成"播几帧就停"。
          this._liveSegmentSkips++;
          Log.w(
            this.TAG,
            `Live segment failed (${this._liveSegmentSkips}/${MAX_LIVE_SEGMENT_SKIPS}): ` +
              `${e instanceof LoadError ? `code=${e.info.code} msg=${e.info.msg}` : (e as Error).message}`,
          );
          if (this._liveSegmentSkips < MAX_LIVE_SEGMENT_SKIPS) {
            continue;
          }
          Log.e(this.TAG, `Live segment failures exceeded ${MAX_LIVE_SEGMENT_SKIPS}, giving up`);
        }
        if (e instanceof LoadError) {
          Log.e(this.TAG, `IOException: type = ${e.errorType}, code = ${e.info.code}, msg = ${e.info.msg}`);
          this._callbacks.onIOError(e.errorType, e.info);
        } else {
          Log.e(this.TAG, `Segment load failed: ${(e as Error).message}`);
          this._callbacks.onIOError(PlayerErrors.EXCEPTION, { code: -1, msg: (e as Error).message });
        }
        return;
      }
    }
  }

  /** Destroy demuxer + remuxer so the next segment re-anchors the output timeline at `startSeconds`. */
  private _resetTransmux(startSeconds: number, resetPeriodMediaInfo: boolean): void {
    this._resetBitrateMeasurement();
    if (resetPeriodMediaInfo) {
      this._resetPeriodMediaInfo();
    }
    if (this._demuxer) {
      this._demuxer.destroy();
      this._demuxer = null;
    }
    if (this._remuxer) {
      this._remuxer.destroy();
      this._remuxer = null;
    }
    this._pendingDtsOffsetMs = startSeconds * 1000;
    // The output timeline restarts: stale carry bytes and the PTS anchor are invalid
    this._workerAudioDecoder?.reset();
    this._resetAudioTiming();
  }

  /**
   * Reset the separate-audio-rendition pair. The pair is recreated lazily on the
   * next audio segment, anchored at that segment's playlist position.
   */
  private _resetAudioTransmux(): void {
    if (this._audioDemuxer) {
      this._audioDemuxer.destroy();
      this._audioDemuxer = null;
    }
    if (this._audioRemuxer) {
      this._audioRemuxer.destroy();
      this._audioRemuxer = null;
    }
    // Fresh remuxer epoch: relative-PTS rebase restarts from the next rendition
    // frame (the recreated remuxer re-anchors via setAudioSegmentStartTarget).
    this._renditionBasePtsMs = null;
  }

  private _shouldAnchorSegment(meta: SegmentMeta): boolean {
    return meta.resetRemuxer || !this._hlsSource;
  }

  private _finishTsInputBoundary(): void {
    // Flush stashed samples at every TS segment boundary so the next segment's first
    // remux batch is not mixed with the previous segment's tail.
    this._demuxer?.flushSegmentBoundary();
    this._remuxer?.flushStashedSamples();
    this._workerAudioDecoder?.reset();
    this._resetAudioTiming();
  }

  private _prepareContinuousLiveTsRestart(meta: SegmentMeta, ioctl: FetchLoader): void {
    if (this._sourceMode !== "continuous-live-ts") {
      return;
    }

    this._finishTsInputBoundary();
    this._fmp4Mode = false;
    this._fmp4Chunks = [];
    ioctl.onDataArrival = (data, byteStart) => this._onInputChunk(meta, data, byteStart);
  }

  private _resetAudioTiming(): void {
    this._audioGen++;
    this._audioAnchorPtsMs = null;
    this._audioSamplesSinceAnchor = 0;
    this._audioSampleRate = 0;
    this._pendingPcm = [];
  }

  /**
   * audio 轨不连续：重置 PCM 时间轴，并把软解主路径的下一帧 PCM 钉到当前视频时间。
   *
   * 分片 404/502 恢复后（上游整点切目录等），视频 remuxer 会把源 PTS 跳变 bridge 到
   * 输出时间轴（实测 29.5s）；而 PCM 标签直接用源 PTS（mapPcmTimestamp 直映射），
   * 若不钉回视频时间轴，PCM 会领先视频 29.5s → 排程门压死 → 永久静音。
   * setAudioSegmentStartTarget 只在 PCM 时间轴 unanchored（刚 reset）时生效，
   * 首帧强制输出到当前视频时间，之后按源 PTS 连续 bridging —— 与视频同轴。
   */
  private _resetPcmOnAudioDiscontinuity(): void {
    this._remuxer?.resetPcmTiming();
    this._workerAudioDecoder?.reset();
    this._resetAudioTiming();
    if (this._config.wasmDecoders.mp2 || this._config.wasmDecoders.ac3) {
      this._remuxer?.setAudioSegmentStartTarget(Math.max(0, this._playheadCurrentMs));
    }
    this._callbacks.onPCMAudioDiscontinuity();
  }

  /** 直播源判定：HLS 播放列表为 live 窗口时，分片可能在上游过期，可跳过等待刷新 */
  private _isLiveSource(): boolean {
    return this._sourceMode === "hls" && (this._hlsSource?.isLive ?? false);
  }

  /** 直播播放判定：缓冲领先限速门只对直播生效（VOD/回看的快进预取不受限）。 */
  private _isLivePlayback(): boolean {
    return this._sourceMode === "continuous-live-ts" || this._isLiveSource();
  }

  private _loadSegment(meta: SegmentMeta): Promise<void> {
    const ioctl = new FetchLoader(
      {
        url: meta.url,
        cors: true,
        withCredentials: false,
        referrerPolicy: this._config.referrerPolicy as ReferrerPolicy | undefined,
      },
      this._config,
      undefined,
      { resumeMode: this._sourceMode === "continuous-live-ts" ? "restart" : "range" },
    );
    this._ioctl = ioctl;

    return new Promise<void>((resolve, reject) => {
      this._cancelLoad = () => reject(CANCELLED);

      ioctl.onError = (type, info) => reject(new LoadError(type, info));
      ioctl.onSeeked = () => this._remuxer?.insertDiscontinuity();
      ioctl.onRestarted = () => this._prepareContinuousLiveTsRestart(meta, ioctl);
      ioctl.onComplete = () => resolve();
      ioctl.onHLSDetected = (text, url) => {
        // Playlist served from a non-.m3u8 URL: switch the pipeline to the HLS source,
        // reusing the playlist content we already downloaded
        this._runId++;
        reject(CANCELLED);
        this._startHls(meta.url, { text, url });
      };
      ioctl.onDataArrival = (data, byteStart) => this._onInputChunk(meta, data, byteStart);
      ioctl.open();
    }).finally(() => {
      ioctl.destroy();
      if (this._ioctl === ioctl) {
        this._ioctl = null;
        this._cancelLoad = null;
      }
    });
  }

  /** First-chunk handler: probe the container format, then hand off to the right path. */
  private _onInputChunk(meta: SegmentMeta, data: Uint8Array, byteStart: number): number {
    this._recordInputBytes(data.byteLength);
    return this._onProbeChunk(meta, data, byteStart);
  }

  private _onProbeChunk(meta: SegmentMeta, data: Uint8Array, byteStart: number): number {
    if (meta.track === "audio") {
      return this._onAudioProbeChunk(meta, data, byteStart);
    }

    if (this._fmp4Mode) {
      return this._onFmp4Chunk(data);
    }

    const probeData = TSDemuxer.probe(data);
    if (probeData.match) {
      this._setupTSDemuxerRemuxer(probeData, meta);
      if (this._ioctl && this._demuxer) {
        const demuxer = this._demuxer;
        this._ioctl.onDataArrival = (chunk, chunkByteStart) => {
          this._recordInputBytes(chunk.byteLength);
          return demuxer.parseChunks(chunk, chunkByteStart);
        };
      }
      return this._demuxer?.parseChunks(data, byteStart) ?? 0;
    }

    // HTTP-FLV（直播，如推流发布的本地 FLV 地址）：直接走 FLV demuxer
    const flvProbe = FLVDemuxer.probe(data);
    if (flvProbe.match) {
      this._setupFLVDemuxerRemuxer(flvProbe, meta);
      if (this._ioctl && this._demuxer) {
        const demuxer = this._demuxer;
        this._ioctl.onDataArrival = (chunk, chunkByteStart) => {
          this._recordInputBytes(chunk.byteLength);
          return demuxer.parseChunks(chunk, chunkByteStart);
        };
      }
      return this._demuxer?.parseChunks(data, byteStart) ?? 0;
    }

    if (probeFmp4(data)) {
      this._fmp4Mode = true;
      if (this._ioctl) {
        this._ioctl.onDataArrival = (chunk) => {
          this._recordInputBytes(chunk.byteLength);
          return this._onFmp4Chunk(chunk);
        };
      }
      return this._onFmp4Chunk(data);
    }

    if (!probeData.needMoreData) {
      Log.e(this.TAG, "Unsupported media type (neither MPEG-TS nor fMP4)");
      Promise.resolve().then(() => this._abortCurrentLoad());
      this._callbacks.onDemuxError(PlayerErrors.FORMAT_UNSUPPORTED, "Unsupported media type!");
    }
    return 0;
  }

  // ---- MPEG-TS path ----

  private _setupTSDemuxerRemuxer(probeData: unknown, meta: SegmentMeta): void {
    const shouldAnchor = this._shouldAnchorSegment(meta);
    const canReuseHls = this._hlsSource !== null && !shouldAnchor && this._demuxer !== null && this._remuxer !== null;
    const canReuseTsInputBoundary = this._sourceMode !== "hls" && this._demuxer !== null && this._remuxer !== null;
    const canReuse = canReuseHls || canReuseTsInputBoundary;
    if (canReuse) {
      (this._demuxer as TSDemuxer).resetSegmentBoundary(probeData as ConstructorParameters<typeof TSDemuxer>[0], {
        resetAudioParserState: canReuseTsInputBoundary,
        // HLS 复用保留跨段解封装状态（ac3-lab 同款连续喂流）：跨段拆开的 PES 补完
        // 而非丢弃，源在段边界的 CC 不连续照常检测 → insertDiscontinuity 干净重锚。
        // 非 HLS TS 直链路径维持旧行为（清空 + normalization）。
        preserveStreamState: canReuseHls,
      });
      this._remuxer?.setTsSegmentContinuityNormalization(canReuseTsInputBoundary);
      return;
    }

    if (this._demuxer) {
      this._demuxer.destroy();
    }
    const demuxer = new TSDemuxer(probeData as ConstructorParameters<typeof TSDemuxer>[0], {
      waitForInitialVideoKeyframe: shouldAnchor || !this._demuxer || !this._remuxer,
    });
    this._demuxer = demuxer;

    if (!this._remuxer) {
      this._remuxer = new MP4Remuxer();
      if (this._pendingDtsOffsetMs !== 0) {
        this._remuxer.setDtsBaseOffset(this._pendingDtsOffsetMs);
        this._pendingDtsOffsetMs = 0;
      }
    }
    this._remuxer.setTsSegmentContinuityNormalization(false);

    demuxer.onError = this._onDemuxException.bind(this);
    demuxer.timestampBase = 0;
    demuxer.onTrackDiscontinuity = (track) => {
      if (track === "video") {
        this._remuxer?.flushStashedSamples();
        this._remuxer?.insertDiscontinuity();
      }
      if (track === "audio") {
        this._resetPcmOnAudioDiscontinuity();
      } else {
        this._workerAudioDecoder?.reset();
        this._resetAudioTiming();
      }
    };
    demuxer.onPcr = (pcrBase, bytePosition, discontinuity) => {
      this._recordTsPcr(pcrBase, bytePosition, discontinuity);
    };

    // Set up software audio decode: MP2 只要有 wasm 就软解；AC-3/E-AC-3 还需要
    // demuxer 的软解开关（由 mse-playback-backend 决定是否保留 wasmDecoders.ac3）。
    if (this._config.wasmDecoders.mp2 || this._config.wasmDecoders.ac3) {
      demuxer.onRawAudioData = (frame) => {
        this._handleRawAudioFrame(frame);
      };
      // 主 muxed TS 软解：让 remuxer 附带一条静音 AAC 假音轨（video 有音轨 → 后台
      // 标签页不被 UA 暂停，解决后台音画不同步）。独立音频 rendition 路径不设。
      demuxer.silentAudioTrack = true;
    }
    if (this._config.wasmDecoders.ac3) {
      demuxer.ac3SoftDecode = true;
    }

    this._remuxer.bindDataSource(
      demuxer as unknown as {
        onDataAvailable: (...args: unknown[]) => void;
        onTrackMetadata: (...args: unknown[]) => void;
      },
    );
    const remuxTrackMetadata = demuxer.onTrackMetadata;
    demuxer.onTrackMetadata = (type, metadata) => {
      this._handleTsTrackMetadata(type, metadata);
      remuxTrackMetadata?.(type, metadata);
    };

    this._remuxer.onInitSegment = (type, initSegment) => {
      this._callbacks.onInitSegment(type, initSegment as unknown as Parameters<PipelineCallbacks["onInitSegment"]>[1]);
    };
    this._remuxer.onMediaSegment = (type, mediaSegment) => {
      this._callbacks.onMediaSegment(
        type,
        mediaSegment as unknown as Parameters<PipelineCallbacks["onMediaSegment"]>[1],
      );
    };
  }

  private _onDemuxException(type: DemuxErrorDetail, info: string): void {
    Log.e(this.TAG, `DemuxException: type = ${type}, info = ${info}`);
    this._callbacks.onDemuxError(type, info);
  }

  // ---- HTTP-FLV（直播）路径 ----

  private _setupFLVDemuxerRemuxer(probeData: FLVProbeResult, _meta: SegmentMeta): void {
    if (this._demuxer) {
      this._demuxer.destroy();
    }
    const demuxer = new FLVDemuxer(probeData, {
      waitForInitialVideoKeyframe: true,
    });
    this._demuxer = demuxer;

    if (!this._remuxer) {
      this._remuxer = new MP4Remuxer();
      if (this._pendingDtsOffsetMs !== 0) {
        this._remuxer.setDtsBaseOffset(this._pendingDtsOffsetMs);
        this._pendingDtsOffsetMs = 0;
      }
    }
    this._remuxer.setTsSegmentContinuityNormalization(false);

    demuxer.onError = this._onDemuxException.bind(this);
    demuxer.timestampBase = 0;
    demuxer.onTrackDiscontinuity = (track) => {
      if (track === "video") {
        this._remuxer?.flushStashedSamples();
        this._remuxer?.insertDiscontinuity();
      }
      if (track === "audio") {
        this._resetPcmOnAudioDiscontinuity();
      } else {
        this._workerAudioDecoder?.reset();
        this._resetAudioTiming();
      }
    };

    this._remuxer.bindDataSource(
      demuxer as unknown as {
        onDataAvailable: (...args: unknown[]) => void;
        onTrackMetadata: (...args: unknown[]) => void;
      },
    );
    const remuxTrackMetadata = demuxer.onTrackMetadata;
    demuxer.onTrackMetadata = (type, metadata) => {
      this._handleTsTrackMetadata(type, metadata);
      remuxTrackMetadata?.(type, metadata);
    };

    this._remuxer.onInitSegment = (type, initSegment) => {
      this._callbacks.onInitSegment(type, initSegment as unknown as Parameters<PipelineCallbacks["onInitSegment"]>[1]);
    };
    this._remuxer.onMediaSegment = (type, mediaSegment) => {
      this._callbacks.onMediaSegment(
        type,
        mediaSegment as unknown as Parameters<PipelineCallbacks["onMediaSegment"]>[1],
      );
    };
  }

  private _onAudioProbeChunk(meta: SegmentMeta, data: Uint8Array, byteStart: number): number {
    const probeData = TSDemuxer.probe(data);
    if (probeData.match) {
      this._setupAudioDemuxerRemuxer(probeData, meta);
      if (this._ioctl && this._audioDemuxer) {
        const demuxer = this._audioDemuxer;
        this._ioctl.onDataArrival = (chunk, chunkByteStart) => {
          this._recordInputBytes(chunk.byteLength);
          return demuxer.parseChunks(chunk, chunkByteStart);
        };
      }
      return this._audioDemuxer?.parseChunks(data, byteStart) ?? 0;
    }

    // fMP4 or unknown audio rendition container: not supported on the separate path
    if (!probeData.needMoreData) {
      Log.w(this.TAG, `Audio rendition segment is not MPEG-TS (${meta.url}); disabling separate audio track`);
      this._hlsSource?.disableAudio();
      this._resetAudioTransmux();
      return data.byteLength; // consume the rest of this segment without demuxing
    }
    return 0;
  }

  private _setupAudioDemuxerRemuxer(probeData: unknown, meta: SegmentMeta): void {
    const canReuse = !meta.resetRemuxer && this._audioDemuxer !== null && this._audioRemuxer !== null;
    if (canReuse) {
      // 保留跨段解封装状态（ac3-lab 同款连续喂流）：跨段拆开的音频帧由解析器/WASM
      // 携带态补完，不再每段丢弃；段边界不连续照常走 onTrackDiscontinuity。
      this._audioDemuxer?.resetSegmentBoundary(probeData as ConstructorParameters<typeof TSDemuxer>[0], {
        preserveStreamState: true,
      });
      return;
    }

    if (this._audioDemuxer) {
      this._audioDemuxer.destroy();
    }
    const demuxer = new TSDemuxer(probeData as ConstructorParameters<typeof TSDemuxer>[0], {
      waitForInitialVideoKeyframe: false,
    });
    this._audioDemuxer = demuxer;

    if (!this._audioRemuxer) {
      this._audioRemuxer = new MP4Remuxer();
      // Anchor the audio rendition's first sample at the playlist position of this
      // segment; this aligns it with the video timeline even when the renditions
      // use different PTS epochs.
      this._audioRemuxer.setDtsBaseOffset(meta.start * 1000);
      // mapPcmTimestamp consumes relative PTS (first frame = 0). Without a pinned
      // base the rendition remuxer never runs _calculateDtsBase (no samples ever
      // enter its audio track) and _dtsBase stays Infinity → every chunk dropped.
      this._audioRemuxer.setPcmSourceBase(0);
    }
    this._audioRemuxer.setTsSegmentContinuityNormalization(false);

    demuxer.onError = this._onDemuxException.bind(this);
    demuxer.timestampBase = 0;
    // Separate-audio rendition: same soft-decode gate as the muxed path. When the
    // rendition is MP2/AC-3/E-AC-3 the demuxer emits raw frames here instead of
    // feeding the audio track, so the _audioRemuxer never produces MSE audio
    // (metadata arrives with softwareDecodeOnly → no audio init either).
    if (this._config.wasmDecoders.mp2 || this._config.wasmDecoders.ac3) {
      demuxer.onRawAudioData = (frame) => {
        this._handleAudioRenditionRawFrame(frame);
      };
    }
    if (this._config.wasmDecoders.ac3) {
      demuxer.ac3SoftDecode = true;
    }
    demuxer.onTrackDiscontinuity = (track) => {
      if (track === "audio") {
        this._audioRemuxer?.flushStashedSamples();
        this._audioRemuxer?.insertDiscontinuity();
      }
    };

    this._audioRemuxer.bindDataSource(
      demuxer as unknown as {
        onDataAvailable: (...args: unknown[]) => void;
        onTrackMetadata: (...args: unknown[]) => void;
      },
    );
    const remuxTrackMetadata = demuxer.onTrackMetadata;
    demuxer.onTrackMetadata = (type, metadata) => {
      this._handleTsTrackMetadata(type, metadata);
      remuxTrackMetadata?.(type, metadata);
    };

    this._audioRemuxer.onInitSegment = (type, initSegment) => {
      this._callbacks.onInitSegment(type, initSegment as unknown as Parameters<PipelineCallbacks["onInitSegment"]>[1]);
    };
    this._audioRemuxer.onMediaSegment = (type, mediaSegment) => {
      this._callbacks.onMediaSegment(
        type,
        mediaSegment as unknown as Parameters<PipelineCallbacks["onMediaSegment"]>[1],
      );
    };
  }

  // ---- fMP4 passthrough path ----

  private async _loadFmp4Init(initUrl: string, runId: number): Promise<void> {
    this._fmp4Mode = true;
    let response: Response;
    try {
      response = await fetch(initUrl, {
        headers: this._config.headers,
        referrerPolicy: (this._config.referrerPolicy as ReferrerPolicy | undefined) ?? "no-referrer-when-downgrade",
      });
    } catch (error) {
      throw new LoadError(PlayerErrors.REQUEST_FAILED, {
        code: -1,
        msg: error instanceof Error ? error.message : String(error),
        url: initUrl,
      });
    }
    if (this._runId !== runId) return;
    if (!response.ok) {
      throw new LoadError(PlayerErrors.HTTP_STATUS_CODE_INVALID, {
        code: response.status,
        msg: response.statusText,
        url: response.url || initUrl,
      });
    }
    let data: Uint8Array;
    try {
      data = new Uint8Array(await response.arrayBuffer());
    } catch (error) {
      throw new LoadError(PlayerErrors.REQUEST_FAILED, {
        code: -1,
        msg: error instanceof Error ? error.message : String(error),
        url: response.url || initUrl,
      });
    }
    // Superseded mid-fetch (seek/reload/destroy): don't append a stale init segment
    if (this._runId !== runId) return;
    this._sendFmp4Init(data);
  }

  private _sendFmp4Init(data: Uint8Array): void {
    const initInfo = parseInitSegment(data);
    this._fmp4Timescales = initInfo.timescales;
    this._fmp4TimestampOffsetWarningLogged = false;
    for (const track of initInfo.tracks) {
      this._handleFmp4TrackMetadata(track);
    }
    const codec = initInfo.codecs.join(",") || this._hlsSource?.info.codecs || "";
    this._callbacks.onInitSegment("video", {
      type: "video",
      container: "video/mp4",
      codec,
      data: toArrayBuffer(data),
    });
    this._fmp4InitSent = true;
  }

  private _warnFmp4TimestampOffsetUnavailable(reason: string): void {
    if (this._fmp4TimestampOffsetWarningLogged) {
      return;
    }
    this._fmp4TimestampOffsetWarningLogged = true;
    Log.w(this.TAG, `fMP4 timestampOffset unavailable: ${reason}; appending media with original tfdt`);
  }

  private _getFmp4TimestampOffset(meta: SegmentMeta, media: Uint8Array): number | undefined {
    if (this._fmp4Timescales.size === 0) {
      this._warnFmp4TimestampOffsetUnavailable("init segment timescales missing");
      return undefined;
    }

    const segmentStart = getSegmentStartTime(media, this._fmp4Timescales);
    if (segmentStart === null) {
      this._warnFmp4TimestampOffsetUnavailable("media segment has no tfdt");
      return undefined;
    }

    const timestampOffset = (meta.start - segmentStart) * 1000;
    return Math.abs(timestampOffset) < 0.001 ? 0 : timestampOffset;
  }

  private _onFmp4Chunk(data: Uint8Array): number {
    this._fmp4Chunks.push(data);
    return data.byteLength;
  }

  /** Forward a fully buffered fMP4 segment to MSE (extracting the init part on first use). */
  private _flushFmp4Segment(meta: SegmentMeta): void {
    if (this._fmp4Chunks.length === 0) {
      return;
    }
    const total = this._fmp4Chunks.reduce((sum, c) => sum + c.byteLength, 0);
    const segment = new Uint8Array(total);
    let offset = 0;
    for (const chunk of this._fmp4Chunks) {
      segment.set(chunk, offset);
      offset += chunk.byteLength;
    }
    this._fmp4Chunks = [];

    let media: Uint8Array = segment;
    if (!this._fmp4InitSent) {
      if (!containsMoov(segment)) {
        this._callbacks.onDemuxError(PlayerErrors.FORMAT_ERROR, "fMP4 stream has no initialization segment (moov)");
        return;
      }
      const parts = splitInitFromSegment(segment);
      this._sendFmp4Init(parts.init);
      media = parts.media;
    }

    if (media.byteLength > 0) {
      this._pendingDtsOffsetMs = 0;
      this._callbacks.onMediaSegment("video", {
        type: "video",
        data: toArrayBuffer(media),
        timestampOffset: this._getFmp4TimestampOffset(meta, media),
      });
    }
  }

  // ---- Software audio decode (MP2 / AC-3 / E-AC-3) ----

  private _workerDecoderCodec: SoftAudioCodec | null = null;

  /** Feed the main-thread playhead + MSE buffered end consumed by the buffer-lead gate. */
  setClock(currentTimeMs: number, bufferedEndMs: number, hidden: boolean): void {
    this._playheadCurrentMs = currentTimeMs;
    this._playheadBufferedEndMs = bufferedEndMs;
    this._lastClockArrivalMs = performance.now();
    this._pageHidden = hidden;
  }

  private _handleRawAudioFrame(frame: { codec: SoftAudioCodec; data: Uint8Array; pts: number }): void {
    // Lazily create (or re-create on codec change) the WorkerAudioDecoder
    if (this._workerAudioDecoder && this._workerDecoderCodec !== frame.codec) {
      this._workerAudioDecoder.destroy();
      this._workerAudioDecoder = null;
      this._workerAudioDecoderInitPromise = null;
    }
    if (!this._workerAudioDecoder) {
      const url = frame.codec === "mp2" ? this._config.wasmDecoders.mp2 : this._config.wasmDecoders.ac3;
      if (!url) return;
      this._workerAudioDecoder = new WorkerAudioDecoder(url, frame.codec);
      this._workerDecoderCodec = frame.codec;
      this._workerAudioDecoderInitPromise = this._workerAudioDecoder.initDecoder();
    }

    // Queue decode after init completes; gen guard drops frames queued before a reset
    const gen = this._audioGen;
    this._workerAudioDecoderInitPromise?.then((ready) => {
      if (!ready) {
        this._pcmStats.decodeInitFailed++;
        return;
      }
      if (!this._workerAudioDecoder || gen !== this._audioGen) {
        this._pcmStats.genDrops++;
        return;
      }

      const result = this._workerAudioDecoder.decode(frame.data);
      if (!result) return;
      this._notePcmSource("muxed");

      // 标签**直接采用源给出的逐帧 PTS**（demuxer 本来就是按帧送出 data+pts），
      // 与 ac3-lab 完全一致：一帧进、一帧出，1:1，不需要任何外推。
      //
      // 旧实现把它换成"锚点 + 累计解码样本数"的自由时钟，且只在 |差|>20s 时才重锚，
      // 于是任何"源 PTS 合法地跑得比解码样本数快"的流都会静默累积成一个恒定超前
      // （旧注释所谓"长时间累积成'声音慢慢跑前面'"）；而下游 mapPcmTimestamp 又会
      // 把输出时间轴按样本连续拼接，导致漂移环量到的标签永远自洽(≈0ms)、眼睛却看到
      // 声音持续跑在前面的错位。ac3-lab 用源 PTS 原值，508s 实测 drift p50=-0.2ms。
      const sr = result.sampleRate;
      const carriedSamples = Math.min(Math.max(0, result.samplesBeforeInput), result.samplesPerChannel);
      if (result.samplesBeforeInput > 0) {
        this._pcmStats.carryFrames++;
      }
      const labelPtsMs = frame.pts - (carriedSamples / sr) * 1000;

      // 自由时钟仅保留作诊断：它与源 PTS 的偏差就是旧实现静默吃掉的音画超前量。
      if (this._audioAnchorPtsMs === null || this._audioSampleRate !== sr) {
        this._audioAnchorPtsMs = labelPtsMs;
        this._audioSamplesSinceAnchor = 0;
        this._audioSampleRate = sr;
      } else {
        const freeMs = this._audioAnchorPtsMs + (this._audioSamplesSinceAnchor / sr) * 1000;
        const deltaMs = labelPtsMs - freeMs;
        if (Math.abs(deltaMs) > AUDIO_PTS_REANCHOR_THRESHOLD_MS) {
          // 真正的切台/节目级不连续：让诊断时钟也跟上，避免偏差无限增长。
          this._audioAnchorPtsMs = labelPtsMs;
          this._audioSamplesSinceAnchor = 0;
        } else if (
          Math.abs(deltaMs) >= AUDIO_PTS_DIVERGENCE_LOG_MS &&
          performance.now() - this._lastPtsDivergenceLogAt >= 10000
        ) {
          this._lastPtsDivergenceLogAt = performance.now();
          Log.i(
            this.TAG,
            `PCM label vs free clock: signalled=${labelPtsMs.toFixed(1)}ms free=${freeMs.toFixed(1)}ms ` +
            `delta=${deltaMs.toFixed(1)}ms → using signalled PTS`,
          );
        }
      }
      this._audioSamplesSinceAnchor += result.samplesPerChannel;

      this._emitPcm(result.pcm, result.channels, sr, labelPtsMs);
    });
  }

  /**
   * Normalize PCM timestamps to the MSE timeline using the remuxer's dts base
   * (the exact mapping used for video), then forward to the main thread.
   * PCM decoded before the first remux (dts base unknown) is queued.
   */
  private _emitPcm(pcm: Float32Array, channels: number, sampleRate: number, ptsMs: number): void {
    const durationMs = (Math.floor(pcm.length / channels) / sampleRate) * 1000;
    this._pendingPcm.push({ pcm, channels, sampleRate, ptsMs, durationMs });

    if (this._remuxer?.getTimestampBase() === undefined) {
      // Bound the queue: ~25s of audio at one payload per ~72ms is plenty
      if (this._pendingPcm.length > 512) {
        this._pendingPcm.shift();
        this._pcmStats.pendingOverflowDrops++;
      }
      return;
    }

    const pending = this._pendingPcm;
    this._pendingPcm = [];

    for (let i = 0; i < pending.length; i++) {
      const item = pending[i];
      const mapping = this._remuxer?.mapPcmTimestamp(item.ptsMs, item.durationMs);
      if (mapping === undefined) {
        this._pendingPcm.push(...pending.slice(i));
        if (this._pendingPcm.length > 512) {
          this._pendingPcm.splice(0, this._pendingPcm.length - 512);
          this._pcmStats.queueOverflowDrops++;
        }
        break;
      }
      if (mapping.action === "drop") {
        this._pcmStats.remuxDrop++;
        continue;
      }
      if (mapping.trimStartMs > 0) {
        this._pcmStats.trimDrop++;
      }
      this._deliverMappedPcm(item, mapping);
    }
  }

  /** Deliver a mapped PCM chunk to the main thread, honoring trimStartMs. */
  private _deliverMappedPcm(
    item: { pcm: Float32Array; channels: number; sampleRate: number },
    mapping: Extract<PcmTimestampMapping, { action: "emit" }>,
  ): void {
    let pcm = item.pcm;
    if (mapping.trimStartMs > 0) {
      const cutFrames = Math.round((mapping.trimStartMs / 1000) * item.sampleRate);
      const totalFrames = Math.floor(pcm.length / item.channels);
      if (cutFrames >= totalFrames) {
        return;
      }
      if (cutFrames > 0) {
        pcm = pcm.slice(cutFrames * item.channels);
      }
    }
    this._callbacks.onPCMAudioData(pcm, item.channels, item.sampleRate, mapping.time);
  }

  /** Count PCM frames by source; muxed/rendition are topologically mutually exclusive. */
  private _notePcmSource(source: "muxed" | "rendition"): void {
    if (this._pcmSource === null) {
      this._pcmSource = source;
      return;
    }
    if (this._pcmSource !== source) {
      this._pcmStats.dualSourceFrames++;
      if (this._pcmStats.dualSourceFrames === 1) {
        Log.w(this.TAG, `PCM source conflict: ${this._pcmSource} and ${source} both producing audio`);
      }
    }
  }

  /** Latest soft-decoded PCM chain drop counters (posted to the main thread periodically). */
  getPcmStats(): PcmWorkerStats {
    return { ...this._pcmStats };
  }

  /**
   * Separate-audio-rendition soft-decode path (mirror of _handleRawAudioFrame).
   * Renditions ride their own source PTS epoch, so decoded labels are re-based
   * relative to the first frame and mapped through the dedicated _audioRemuxer
   * (base pinned to 0 via setPcmSourceBase; the segment's playlist position anchors
   * the timeline only while unanchored — afterwards it rides the source PTS
   * sample-contiguously and drift is corrected against the video clock, ac3-lab
   * semantics — see _setupAudioDemuxerRemuxer / setAudioSegmentStartTarget).
   */
  private _handleAudioRenditionRawFrame(frame: { codec: SoftAudioCodec; data: Uint8Array; pts: number }): void {
    // The first raw rendition frame proves this rendition is soft-decoded
    // (MP2/AC-3/E-AC-3): no MSE audio init/media will ever be produced. Tell the
    // main thread (and the worker's init gate) immediately so held video flows.
    if (!this._renditionSoftDecodeAnnounced) {
      this._renditionSoftDecodeAnnounced = true;
      this._callbacks.onAudioRenditionSoftDecode();
    }
    if (this._audioRenditionDecoder && this._audioRenditionDecoderCodec !== frame.codec) {
      this._audioRenditionDecoder.destroy();
      this._audioRenditionDecoder = null;
      this._audioRenditionDecoderInitPromise = null;
    }
    if (!this._audioRenditionDecoder) {
      const url = frame.codec === "mp2" ? this._config.wasmDecoders.mp2 : this._config.wasmDecoders.ac3;
      if (!url) return;
      this._audioRenditionDecoder = new WorkerAudioDecoder(url, frame.codec);
      this._audioRenditionDecoderCodec = frame.codec;
      this._audioRenditionDecoderInitPromise = this._audioRenditionDecoder.initDecoder();
    }

    const gen = this._audioGen;
    this._audioRenditionDecoderInitPromise?.then((ready) => {
      if (!ready) {
        this._pcmStats.decodeInitFailed++;
        return;
      }
      if (!this._audioRenditionDecoder || gen !== this._audioGen) {
        this._pcmStats.genDrops++;
        return;
      }

      const result = this._audioRenditionDecoder.decode(frame.data);
      if (!result) return;
      this._notePcmSource("rendition");
      this._pcmStats.renditionFrames++;

      const sr = result.sampleRate;
      const carriedSamples = Math.min(Math.max(0, result.samplesBeforeInput), result.samplesPerChannel);
      if (result.samplesBeforeInput > 0) {
        this._pcmStats.carryFrames++;
      }
      const labelPtsMs = frame.pts - (carriedSamples / sr) * 1000;
      if (this._renditionBasePtsMs === null) {
        this._renditionBasePtsMs = labelPtsMs;
      }
      const relPtsMs = labelPtsMs - this._renditionBasePtsMs;
      const durationMs = (Math.floor(result.pcm.length / result.channels) / sr) * 1000;

      // setPcmSourceBase(0) makes the mapping always available (dts base pinned at
      // setup), so no pending queue is needed on this path.
      const mapping = this._audioRemuxer?.mapPcmTimestamp(relPtsMs, durationMs);
      if (mapping === undefined) {
        return;
      }
      if (mapping.action === "drop") {
        this._pcmStats.remuxDrop++;
        return;
      }
      if (mapping.trimStartMs > 0) {
        this._pcmStats.trimDrop++;
      }
      this._deliverMappedPcm({ pcm: result.pcm, channels: result.channels, sampleRate: sr }, mapping);
    });
  }
}

export default Pipeline;
