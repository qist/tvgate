/**
 * HLS 源。
 * 行为见引擎设计 §5.6：拉 m3u8 → parseM3U8 解析 multivariant/media playlist →
 * 选 variant（bandwidth/codecs/resolution/frameRate/videoRange）→
 * 直播从 live edge 起播（LIVE_EDGE_SEGMENTS=3）→ 定期刷新 playlist（MAX_REFRESH_FAILURES=5）
 * → onInfo 回报 HlsInfo → 作为 SegmentSource 驱动 pipeline。
 */

import { parseM3U8, type HlsVariant, type HlsMediaRendition } from "../formats/m3u8";
import type { SegmentSource } from "./segment-source";

export interface HlsInfo {
  live: boolean;
  targetDuration: number;
  totalDuration: number;
  segmentCount: number;
  /** 所选 variant 的信息（multivariant 播放列表时才有）。 */
  bandwidth?: number;
  resolution?: { width: number; height: number };
  codecs?: string[];
  frameRate?: number;
  videoRange?: string;
  /** 选定的独立音频 rendition（EXT-X-MEDIA;TYPE=AUDIO）；需软解接入，否则该流会静音。 */
  audioRendition?: { uri: string; groupId?: string; language?: string };
}

export interface HlsSourceCallbacks {
  onInfo?(info: HlsInfo): void;
  onError?(message: string): void;
}

export interface HlsSourceOptions {
  /** 期望的最大码率（选 variant 用）；缺省选带宽最高的可解 variant。 */
  maxBandwidth?: number;
  /** 直播起播时距直播边的分段数。 */
  liveEdgeSegments?: number;
  /** 连续刷新失败上限，超过则停止并报错。 */
  maxRefreshFailures?: number;
  /** 自定义抓取（便于测试注入）。 */
  fetcher?: (url: string) => Promise<string>;
  headers?: Record<string, string>;
}

const LIVE_EDGE_SEGMENTS = 3;
const MAX_REFRESH_FAILURES = 5;

export class HlsSource implements SegmentSource {
  live = false;

  private pending: { seq: number; uri: string }[] = [];
  /** 已入队（或已交付）的最大分片序号：刷新时只取「序号 > 它」的新分片。 */
  private lastQueued = -1;
  /**
   * 已交付给消费方的最大分片序号。批次作废时把入队游标回退到此，使「未交付」的分片
   * （含刚失败的那个）能在下次 refresh 重新入队——否则要等播放列表滑过一整窗
   * （实测整点会因此断流数十秒，并跳过中间若干分片）。
   */
  private lastDelivered = -1;
  /** 是否已完成首次（直播 = live edge 切片）入队。 */
  private started = false;
  private playlistUrl = "";
  private refreshFailures = 0;
  /** 刷新失败是否已上报（成功刷新后复位）：避免整点/断流窗口内反复上报成 UI 错误风暴。 */
  private refreshFailureNotified = false;
  private destroyed = false;
  private info: HlsInfo | null = null;
  private readonly liveEdgeSegments: number;
  private readonly maxRefreshFailures: number;
  private readonly maxBandwidth?: number;

  constructor(
    url: string,
    private readonly callbacks: HlsSourceCallbacks = {},
    options: HlsSourceOptions = {},
  ) {
    this.playlistUrl = url;
    this.liveEdgeSegments = options.liveEdgeSegments ?? LIVE_EDGE_SEGMENTS;
    this.maxRefreshFailures = options.maxRefreshFailures ?? MAX_REFRESH_FAILURES;
    this.maxBandwidth = options.maxBandwidth;
    this.fetcher = options.fetcher;
    this.headers = options.headers ?? {};
  }

  private readonly fetcher?: (url: string) => Promise<string>;
  private readonly headers: Record<string, string>;

  /** 加载并解析播放列表（master 会再拉一次 variant）。 */
  async load(): Promise<HlsInfo | null> {
    const text = await this.fetchPlaylist(this.playlistUrl);
    if (text === null) {
      return null;
    }

    const parsed = parseM3U8(this.playlistUrl, text);
    if (parsed.isMaster) {
      const variant = pickVariant(parsed.variants, this.maxBandwidth);
      if (!variant) {
        this.callbacks.onError?.("multivariant 播放列表中没有可解的 variant");
        return null;
      }
      this.playlistUrl = variant.uri;
      const variantText = await this.fetchPlaylist(variant.uri);
      if (variantText === null) {
        return null;
      }
      const media = parseM3U8(variant.uri, variantText);
      const audioRend = pickAudioRendition(parsed.renditions, variant.audioGroup);
      this.live = !media.endlist;
      this.enqueueSegments(
        media.segments.map((s, i) => ({ seq: (media.mediaSequence ?? 0) + i, uri: s.uri })),
        this.live,
      );
      this.info = {
        live: this.live,
        targetDuration: media.targetDuration ?? 0,
        totalDuration: media.segments.reduce((a, s) => a + s.duration, 0),
        segmentCount: media.segments.length,
        bandwidth: variant.bandwidth,
        resolution: variant.resolution,
        codecs: variant.codecs,
        frameRate: variant.frameRate,
        videoRange: variant.videoRange,
        audioRendition: audioRend
          ? { uri: audioRend.uri!, groupId: audioRend.groupId, language: audioRend.language }
          : undefined,
      };
    } else {
      this.live = !parsed.endlist;
      this.enqueueSegments(
        parsed.segments.map((s, i) => ({ seq: (parsed.mediaSequence ?? 0) + i, uri: s.uri })),
        this.live,
      );
      this.info = {
        live: this.live,
        targetDuration: parsed.targetDuration ?? 0,
        totalDuration: parsed.segments.reduce((a, s) => a + s.duration, 0),
        segmentCount: parsed.segments.length,
      };
    }

    this.callbacks.onInfo?.(this.info);
    return this.info;
  }

  async next(): Promise<string | null> {
    if (this.destroyed) return null;
    const head = this.pending.shift();
    if (head) return this.deliver(head);
    if (!this.live) return null;

    // 直播：刷新一次播放列表。没有新分段时返回 null，由消费方（pipeline）稍后重试；
    // 不在此阻塞等待，否则分段间隔期内 next() 会挂起且无法被测试/调用方控制节奏。
    const got = await this.refresh();
    if (got) {
      const item = this.pending.shift();
      if (item) return this.deliver(item);
    }
    return null;
  }

  /** 交付一个分片并推进「已交付」游标（供批次作废时回退）。 */
  private deliver(item: { seq: number; uri: string }): string {
    if (item.seq > this.lastDelivered) this.lastDelivered = item.seq;
    return item.uri;
  }

  /** 刷新播放列表，把新分段入队；返回是否有新分段。 */
  async refresh(): Promise<boolean> {
    if (this.destroyed) return false;
    const text = await this.fetchPlaylist(this.playlistUrl);
    if (text === null) {
      this.refreshFailures++;
      if (this.refreshFailures >= this.maxRefreshFailures) {
        // **绝不可**在此置 destroyed=true——那会让 next() 永久返回 null，而 pipeline
        // 对 live 源只会每秒空转重试，结果是「拉流静默死亡、永不恢复」。直播应持续重试。
        // 上报只做一次（成功刷新后复位）：整点/断流窗口内反复上报会把「上游暂时不可用」
        // 放大成 UI 播放错误与重连风暴，而此处本就在持续重试、无需上层介入。
        if (!this.refreshFailureNotified) {
          this.refreshFailureNotified = true;
          this.callbacks.onError?.(`播放列表连续刷新失败 ${this.refreshFailures} 次，继续重试`);
        }
        this.refreshFailures = 0;
      }
      return false;
    }
    this.refreshFailures = 0;
    this.refreshFailureNotified = false;

    const media = parseM3U8(this.playlistUrl, text);
    const base = media.mediaSequence ?? 0;
    const fresh: { seq: number; uri: string }[] = [];
    for (let i = 0; i < media.segments.length; i++) {
      const seq = base + i;
      if (seq > this.lastQueued) fresh.push({ seq, uri: media.segments[i].uri });
    }
    if (fresh.length === 0) return false;
    this.enqueueSegments(fresh, true);
    return true;
  }

  /** 入队分段（items 已带序号）。直播首次从距直播边 liveEdgeSegments 段处起播。 */
  private enqueueSegments(items: { seq: number; uri: string }[], live: boolean): void {
    if (items.length === 0) return;
    let batch = items;
    if (live && !this.started) {
      const start = Math.max(0, batch.length - 1 - (this.liveEdgeSegments - 1));
      batch = batch.slice(start);
    }
    if (batch.length === 0) return;
    this.started = true;
    this.pending.push(...batch);
    const maxSeq = batch[batch.length - 1].seq;
    if (maxSeq > this.lastQueued) this.lastQueued = maxSeq;
  }

  private async fetchPlaylist(url: string): Promise<string | null> {
    try {
      if (this.fetcher) return await this.fetcher(url);
      const res = await fetch(url, { headers: this.headers });
      if (!res.ok) {
        this.callbacks.onError?.(`播放列表请求失败: HTTP ${res.status}`);
        return null;
      }
      return await res.text();
    } catch (e) {
      this.callbacks.onError?.(e instanceof Error ? e.message : String(e));
      return null;
    }
  }

  get currentInfo(): HlsInfo | null {
    return this.info;
  }

  /**
   * 空闲轮询间隔（毫秒）：没有新分片时 pipeline 隔多久再问一次播放列表。
   *
   * 播放列表本来就按目标时长滚动（本组江苏移动源 TARGETDURATION=10s / 一片 10 秒），
   * 固定 1 秒轮询纯属浪费：每次都要穿一遍上游（实测同一分片间隔里刷出十几次 playlist
   * 请求，服务端与上游都被无谓地打）。取目标时长的一半（HLS 客户端常见做法），
   * 并夹在 1~5 秒：既不会错过新分片太久，也不会把上游刷爆。
   */
  pollIntervalMs(): number {
    const target = this.info?.targetDuration ?? 0;
    if (!(target > 0)) return 1000;
    return Math.min(5000, Math.max(1000, Math.round((target * 1000) / 2)));
  }

  /**
   * 分段批次失效（如整点切换：源站分片暂时不可用）。
   * 丢弃未消费分段，并把入队游标回退到「已交付」位置：下次 refresh 会把未交付的分片
   * （含刚失败那个）重新入队，而不是死等播放列表滑过一整窗——否则上游恢复后仍要等
   * 数十秒才有新分片可拉（实测整点断流），且中间分片会被永久跳过。
   */
  invalidatePending(): void {
    this.pending.length = 0;
    this.lastQueued = this.lastDelivered;
  }

  destroy(): void {
    this.destroyed = true;
    this.pending.length = 0;
  }
}

/** 选 variant：优先可解 codec，其次带宽最接近且不超过上限。 */
export function pickVariant(variants: HlsVariant[], maxBandwidth?: number): HlsVariant | undefined {
  if (variants.length === 0) return undefined;
  const playable = variants.filter((v) => isPlayable(v.codecs));
  const pool = playable.length > 0 ? playable : variants;
  const sorted = [...pool].sort((a, b) => a.bandwidth - b.bandwidth);
  if (maxBandwidth === undefined) return sorted[sorted.length - 1];
  const under = sorted.filter((v) => v.bandwidth <= maxBandwidth);
  return under.length > 0 ? under[under.length - 1] : sorted[0];
}

/**
 * 选独立音频 rendition（EXT-X-MEDIA;TYPE=AUDIO）。
 * 仅当 variant 声明了 `audioGroup` 时才有意义；在该 group 内按优先级选：
 * DEFAULT+AUTOSELECT → DEFAULT → AUTOSELECT → 首条。
 * 无 uri（纯主轨无独立音频）返回 undefined，调用方据此只播视频（该流静音，但视频可播）。
 */
export function pickAudioRendition(
  renditions: HlsMediaRendition[],
  audioGroup?: string,
): HlsMediaRendition | undefined {
  if (!audioGroup) return undefined;
  const candidates = renditions.filter((r) => r.type === "AUDIO" && r.groupId === audioGroup && r.uri);
  if (candidates.length === 0) return undefined;
  return (
    candidates.find((r) => r.isDefault && r.autoselect) ??
    candidates.find((r) => r.isDefault) ??
    candidates.find((r) => r.autoselect) ??
    candidates[0]
  );
}

function isPlayable(codecs: string[]): boolean {
  if (codecs.length === 0) return true;
  return codecs.every((c) => {
    const base = c.split(".")[0];
    return base === "avc1" || base === "avc3" || base === "mp4a" || base === "hvc1" || base === "hev1";
  });
}
