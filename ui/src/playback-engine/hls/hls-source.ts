import type { PlayerConfig } from "../config";
import Log from "../utils/logger";
import type { SegmentMeta, SegmentSource } from "../worker/segment-source";
import {
  type HlsAudioRendition,
  type HlsMediaPlaylist,
  type HlsVariant,
  parseM3U8,
} from "./m3u8";

export interface HlsInfo {
  live: boolean;
  targetDuration: number;
  totalDuration: number;
  /** True while a separate EXT-X-MEDIA audio rendition is being followed. */
  separateAudio: boolean;
  /** Selected variant hints from the multivariant playlist, if any. */
  bandwidth?: number;
  averageBandwidth?: number;
  codecs?: string;
  resolution?: { width: number; height: number };
  frameRate?: number;
  videoRange?: string;
}

const TAG = "HlsSource";
/** Start playback this many segments away from the live edge. */
const LIVE_EDGE_SEGMENTS = 3;
const MAX_REFRESH_FAILURES = 5;
/** Consecutive audio playlist failures tolerated before falling back to video-only. */
const MAX_AUDIO_REFRESH_FAILURES = 3;
/** Number of video media-sequence -> start anchors retained for audio timeline mapping. */
const SEQ_ANCHOR_RETENTION = 64;
/** Keep at least this many audio segments queued; below it the refresh loop tops up. */
const AUDIO_REFRESH_AHEAD_SEGMENTS = 3;
/** Idle poll interval (ms) while the audio queue is sufficiently topped up. */
const AUDIO_LOOP_IDLE_MS = 500;

export class HlsRequestError extends Error {
  constructor(
    public readonly url: string,
    public readonly code?: number,
    public readonly statusText?: string,
    message = code !== undefined ? `HTTP ${code}${statusText ? ` ${statusText}` : ""}` : "Request failed",
  ) {
    super(message);
    this.name = "HlsRequestError";
  }
}

/** Audio rendition segment queued on its own playlist timeline before mapping to the output timeline. */
interface AudioSegmentEntry {
  url: string;
  duration: number;
  mediaSequence: number;
  discontinuity: boolean;
  /** Position on the audio rendition's own cumulative timeline, in seconds. */
  rawStart: number;
  programDateTime?: number;
  /** Resolved position on the output (video) timeline; null until an anchor is found. */
  start: number | null;
}

/** SegmentSource driven by an HLS media playlist (with live refresh). */
export class HlsSource implements SegmentSource {
  onInfo: ((info: HlsInfo) => void) | null = null;
  /** Fired when the separate audio track is given up (load failures / undecodable). */
  onAudioDisabled: (() => void) | null = null;

  private url: string;
  private readonly config: PlayerConfig;
  private readonly abort = new AbortController();
  private destroyed = false;

  private live = true;
  private ended = false;
  private targetDuration = 6;
  private totalDuration = 0;
  private selectedVariant: Omit<HlsVariant, "url"> | undefined;

  private segments: SegmentMeta[] = [];
  private nextIndex = 0;
  /** Media sequence number of the next segment to ingest from playlist refreshes. */
  private nextMediaSequence = -1;
  /** Accumulated timeline position for the next appended segment, in seconds. */
  private timelinePos = 0;
  private initialized = false;
  /** Force a remuxer reset on the next returned segment (initial load). */
  private resetPending = true;
  private refreshFailures = 0;
  private lastPlaylistHadNews = true;
  /** Deduplicates async video refresh kicks while audio segments keep flowing. */
  private videoRefreshInFlight = false;
  /** Playlist content already fetched during HLS detection, consumed on the first load. */
  private preloaded: { text: string; url: string } | null;

  // --- Separate audio rendition (EXT-X-MEDIA TYPE=AUDIO) ---
  private audioRendition: HlsAudioRendition | null = null;
  private audioSegments: AudioSegmentEntry[] = [];
  private audioNextIndex = 0;
  private audioNextMediaSequence = -1;
  private audioTimelinePos = 0;
  private audioTargetDuration = 6;
  private audioRefreshFailures = 0;
  private audioPlaylistHadNews = true;
  /** Constant offset mapping the audio playlist timeline onto the output timeline; null until anchored. */
  private audioOffset: number | null = null;
  /** Program date time anchor from the first playback video segment. */
  private videoPdtAnchor: { start: number; pdtMs: number } | null = null;
  /** Media sequence -> output start of video segments, used to map audio segments. */
  private videoSeqStart = new Map<number, number>();

  constructor(url: string, config: PlayerConfig, preloaded?: { text: string; url: string }) {
    this.url = preloaded?.url ?? url;
    this.config = config;
    this.preloaded = preloaded ?? null;
  }

  get info(): HlsInfo {
    return {
      live: this.live,
      targetDuration: this.targetDuration,
      totalDuration: this.totalDuration,
      separateAudio: this.audioEnabled,
      ...this.selectedVariant,
    };
  }

  async next(): Promise<SegmentMeta | null> {
    if (!this.initialized) {
      await this.initialize();
    }

    while (!this.destroyed) {
      this.resolveAudioAnchors();
      this.pruneStaleAudio();

      const videoMeta = this.nextIndex < this.segments.length ? this.segments[this.nextIndex] : null;
      const audioEntry = this.nextResolvedAudioEntry();

      if (!videoMeta && !audioEntry) {
        if (this.ended) {
          return null;
        }
        await this.refresh();
        continue;
      }

      // Video queue drained but audio is still flowing: top the video playlist up
      // in the background instead of blocking (and starving) the audio pipeline.
      if (!videoMeta) {
        this.kickVideoRefresh();
      }

      // Emit whichever track is next on the timeline; prefer video on ties so the
      // video pipeline (remuxer anchor, MediaInfo) is always established first.
      if (videoMeta && (!audioEntry || videoMeta.start <= audioEntry.start)) {
        this.nextIndex++;
        if (this.resetPending) {
          this.resetPending = false;
          return { ...videoMeta, resetRemuxer: true };
        }
        return videoMeta;
      }

      const entry = audioEntry as AudioSegmentEntry & { start: number };
      this.audioNextIndex++;
      return {
        url: entry.url,
        start: entry.start,
        duration: entry.duration,
        resetRemuxer: entry.discontinuity,
        track: "audio",
      };
    }
    return null;
  }

  destroy(): void {
    this.destroyed = true;
    this.abort.abort();
  }

  /** Called by the pipeline when audio rendition segments turn out to be undecodable (e.g. fMP4). */
  disableAudio(): void {
    if (!this.audioEnabled) {
      return;
    }
    Log.w(TAG, "Separate audio track disabled; continuing with video only");
    this.audioRendition = null;
    this.audioSegments = [];
    this.audioOffset = null;
    this.audioRefreshFailures = 0;
    this.onAudioDisabled?.();
  }

  private get audioEnabled(): boolean {
    return this.audioRendition !== null;
  }

  /** True while the media playlist is a live window (segments may be evicted by the CDN before we fetch them). */
  get isLive(): boolean {
    return this.live;
  }

  private async initialize(): Promise<void> {
    const playlist = await this.fetchPlaylist();
    if (playlist === null) {
      throw new Error("HLS playlist load failed");
    }

    this.ingest(playlist);

    if (this.live) {
      // Start near the live edge and rebase the timeline so playback starts at 0
      this.nextIndex = Math.max(0, this.segments.length - LIVE_EDGE_SEGMENTS);
      const base = this.segments[this.nextIndex]?.start ?? 0;
      if (base > 0) {
        this.segments = this.segments.map((s) => ({ ...s, start: s.start - base }));
        this.timelinePos -= base;
      }
    }

    this.recordVideoAnchors();

    if (this.audioEnabled) {
      await this.initializeAudio();
    }

    this.initialized = true;
    this.onInfo?.(this.info);
  }

  /** Capture alignment anchors from the video segments selected for playback. */
  private recordVideoAnchors(): void {
    for (let i = this.nextIndex; i < this.segments.length; i++) {
      const seg = this.segments[i];
      if (seg.mediaSequence !== undefined) {
        this.videoSeqStart.set(seg.mediaSequence, seg.start);
      }
    }
    const firstPlayback = this.segments[this.nextIndex];
    if (firstPlayback?.programDateTime !== undefined) {
      this.videoPdtAnchor = { start: firstPlayback.start, pdtMs: firstPlayback.programDateTime };
    }
  }

  private trimSeqAnchors(): void {
    while (this.videoSeqStart.size > SEQ_ANCHOR_RETENTION) {
      const oldest = this.videoSeqStart.keys().next().value;
      if (oldest === undefined) break;
      this.videoSeqStart.delete(oldest);
    }
  }

  private async initializeAudio(): Promise<void> {
    try {
      const playlist = await this.fetchOnce(this.audioRendition!.url);
      if (playlist.kind !== "media") {
        throw new Error("Audio rendition URL did not return a media playlist");
      }
      this.ingestAudio(playlist);
      this.trimAudioBacklog();
    } catch (e) {
      if (this.destroyed) return;
      Log.w(TAG, `Audio rendition playlist load failed: ${(e as Error).message}`);
      this.disableAudio();
      return;
    }
    // Top up the audio queue independently of the video-driven refresh cycle:
    // the video queue usually holds several segments, during which next() never
    // reaches the refresh path, so audio must not depend on it.
    void this.audioRefreshLoop();
  }

  /**
   * Some CDN audio renditions expose huge windows (hours of backlog). Keeping the
   * whole backlog misaligns the PDT anchor and floods memory — keep only the tail
   * that overlaps the selected video playback window.
   */
  private trimAudioBacklog(): void {
    if (!this.live || this.audioSegments.length === 0) {
      return;
    }
    const firstVideo = this.segments[this.nextIndex];
    let drop = 0;
    if (firstVideo?.programDateTime !== undefined) {
      const cutoff = firstVideo.programDateTime - this.audioTargetDuration * 1000;
      while (drop < this.audioSegments.length) {
        const entry = this.audioSegments[drop];
        if (entry.programDateTime === undefined) break;
        if (entry.programDateTime + entry.duration * 1000 <= cutoff) {
          drop++;
        } else {
          break;
        }
      }
    } else {
      // No PDT anywhere: fall back to keeping the last few segments.
      drop = Math.max(0, this.audioSegments.length - (LIVE_EDGE_SEGMENTS + 1));
    }
    if (drop > 0) {
      this.audioSegments.splice(0, drop);
      Log.v(TAG, `Trimmed ${drop} stale audio backlog segments`);
    }
  }

  /** Background loop keeping the audio segment queue topped up for live streams. */
  private async audioRefreshLoop(): Promise<void> {
    while (!this.destroyed && this.audioEnabled && this.live) {
      const queued = this.audioSegments.length - this.audioNextIndex;
      if (queued < AUDIO_REFRESH_AHEAD_SEGMENTS) {
        await this.refreshAudio();
      } else {
        await this.sleep(AUDIO_LOOP_IDLE_MS);
      }
    }
  }

  private ingestAudio(playlist: HlsMediaPlaylist): void {
    if (playlist.targetDuration > 0) {
      this.audioTargetDuration = playlist.targetDuration;
    }

    let newSegments = 0;
    for (const seg of playlist.segments) {
      if (this.audioNextMediaSequence !== -1 && seg.mediaSequence < this.audioNextMediaSequence) {
        continue;
      }
      const skipped = this.audioNextMediaSequence !== -1 && seg.mediaSequence > this.audioNextMediaSequence;
      if (skipped) {
        Log.w(TAG, `Missed HLS audio segments: expected ${this.audioNextMediaSequence}, got ${seg.mediaSequence}`);
      }

      this.audioSegments.push({
        url: seg.url,
        duration: seg.duration,
        mediaSequence: seg.mediaSequence,
        discontinuity: seg.discontinuity || skipped,
        rawStart: this.audioTimelinePos,
        programDateTime: seg.programDateTime,
        start: null,
      });
      this.audioTimelinePos += seg.duration;
      this.audioNextMediaSequence = seg.mediaSequence + 1;
      newSegments++;
    }

    this.audioPlaylistHadNews = newSegments > 0;
  }

  /** Map queued audio segments onto the output timeline via PDT or media-sequence anchors. */
  private resolveAudioAnchors(): void {
    if (!this.audioEnabled || this.audioSegments.length === 0) {
      return;
    }

    for (const entry of this.audioSegments) {
      if (entry.start !== null) {
        continue;
      }
      let offset: number | null = null;
      if (entry.programDateTime !== undefined && this.videoPdtAnchor) {
        offset = this.videoPdtAnchor.start + (entry.programDateTime - this.videoPdtAnchor.pdtMs) / 1000 - entry.rawStart;
      } else if (this.videoSeqStart.has(entry.mediaSequence)) {
        offset = (this.videoSeqStart.get(entry.mediaSequence) as number) - entry.rawStart;
      }
      if (offset === null) {
        continue;
      }
      if (this.audioOffset === null || Math.abs(offset - this.audioOffset) > 1) {
        if (this.audioOffset !== null) {
          Log.w(TAG, `Audio timeline offset changed: ${this.audioOffset.toFixed(2)}s -> ${offset.toFixed(2)}s`);
        }
        this.audioOffset = offset;
      }
    }

    if (this.audioOffset !== null) {
      for (const entry of this.audioSegments) {
        if (entry.start === null) {
          entry.start = entry.rawStart + this.audioOffset;
        }
      }
    }
  }

  /** Next queued audio segment whose output position is already known. */
  private nextResolvedAudioEntry(): AudioSegmentEntry & { start: number } | null {
    while (this.audioNextIndex < this.audioSegments.length) {
      const entry = this.audioSegments[this.audioNextIndex];
      if (entry.start !== null) {
        return entry as AudioSegmentEntry & { start: number };
      }
      // Unresolved segments: skip them only when they are certainly older than the
      // current video window; otherwise wait for the next video refresh anchor.
      const videoStart = this.segments[this.nextIndex]?.start ?? this.timelinePos;
      const estimatedEnd = entry.rawStart + (this.audioOffset ?? 0) + entry.duration;
      if (estimatedEnd < videoStart) {
        this.audioNextIndex++;
        continue;
      }
      return null;
    }
    return null;
  }

  /** Drop already-consumed audio segments to bound memory. Entries at or after
   *  audioNextIndex are pending output and must never be dropped — the video
   *  queue's pending start runs ahead of the actual playback position, so using
   *  it as a cutoff silently eats unemitted audio segments. */
  private pruneStaleAudio(): void {
    if (!this.audioEnabled || this.audioNextIndex === 0) {
      return;
    }
    this.audioSegments.splice(0, this.audioNextIndex);
    this.audioNextIndex = 0;
  }

  private ingest(playlist: HlsMediaPlaylist): void {
    this.live = playlist.live;
    this.ended = !playlist.live;
    if (playlist.targetDuration > 0) {
      this.targetDuration = playlist.targetDuration;
    }

    let newSegments = 0;
    for (const seg of playlist.segments) {
      if (this.nextMediaSequence !== -1 && seg.mediaSequence < this.nextMediaSequence) {
        continue; // already ingested
      }
      // Detect skipped segments (playlist advanced faster than we refreshed)
      const skipped = this.nextMediaSequence !== -1 && seg.mediaSequence > this.nextMediaSequence;
      if (skipped) {
        Log.w(TAG, `Missed HLS segments: expected sequence ${this.nextMediaSequence}, got ${seg.mediaSequence}`);
      }

      this.segments.push({
        url: seg.url,
        start: this.timelinePos,
        duration: seg.duration,
        resetRemuxer: seg.discontinuity || skipped,
        initUrl: seg.initUrl,
        mediaSequence: seg.mediaSequence,
        programDateTime: seg.programDateTime,
      });
      if (this.initialized) {
        this.videoSeqStart.set(seg.mediaSequence, this.timelinePos);
      }
      this.timelinePos += seg.duration;
      this.nextMediaSequence = seg.mediaSequence + 1;
      newSegments++;

      // Trim consumed history to bound memory on long-running live streams
      if (this.live && this.nextIndex > 64) {
        const drop = this.nextIndex - 32;
        this.segments.splice(0, drop);
        this.nextIndex -= drop;
      }
    }
    this.trimSeqAnchors();

    this.lastPlaylistHadNews = newSegments > 0;
    this.totalDuration = playlist.totalDuration;
  }

  private async refresh(): Promise<void> {
    // Audio is refreshed by its own background loop (audioRefreshLoop).
    await this.refreshVideo();
  }

  /** Fire-and-forget video playlist top-up (deduplicated), used while audio flows. */
  private kickVideoRefresh(): void {
    if (this.videoRefreshInFlight || this.ended || this.destroyed) {
      return;
    }
    this.videoRefreshInFlight = true;
    void this.refreshVideo().finally(() => {
      this.videoRefreshInFlight = false;
    });
  }

  private async refreshVideo(): Promise<void> {
    // Per spec: reload after targetDuration; after an unchanged playlist, retry after half of it
    const delayMs = (this.lastPlaylistHadNews ? this.targetDuration : this.targetDuration / 2) * 1000;
    await this.sleep(delayMs);
    if (this.destroyed) return;

    const playlist = await this.fetchPlaylist();
    if (playlist) {
      this.ingest(playlist);
    }
  }

  private async refreshAudio(): Promise<void> {
    if (!this.audioEnabled) return;

    const delayMs = (this.audioPlaylistHadNews ? this.audioTargetDuration : this.audioTargetDuration / 2) * 1000;
    await this.sleep(delayMs);
    if (this.destroyed) return;

    try {
      const playlist = await this.fetchOnce(this.audioRendition!.url);
      if (playlist.kind !== "media") {
        throw new Error("Audio rendition URL did not return a media playlist");
      }
      this.audioRefreshFailures = 0;
      this.ingestAudio(playlist);
    } catch (e) {
      if (this.destroyed) return;
      this.audioRefreshFailures++;
      Log.w(
        TAG,
        `Audio playlist load failed (${this.audioRefreshFailures}/${MAX_AUDIO_REFRESH_FAILURES}): ${(e as Error).message}`,
      );
      if (this.audioRefreshFailures >= MAX_AUDIO_REFRESH_FAILURES) {
        this.disableAudio();
      }
    }
  }

  /** Fetch and parse the playlist (resolving a multivariant playlist to its best variant + audio rendition). */
  private async fetchPlaylist(): Promise<HlsMediaPlaylist | null> {
    while (!this.destroyed) {
      try {
        const playlist = await this.fetchOnce(this.url);
        if (playlist.kind === "multivariant") {
          const best = [...playlist.variants].sort((a, b) => b.bandwidth - a.bandwidth)[0];
          if (!best) {
            throw new Error("Multivariant playlist contains no variants");
          }
          const { url: _url, ...selectedVariant } = best;
          this.selectedVariant = selectedVariant;
          this.url = best.url;
          if (this.audioRendition === null && best.audioGroupId) {
            this.audioRendition =
              playlist.audioRenditions.find((r) => r.groupId === best.audioGroupId && r.isDefault) ??
              playlist.audioRenditions.find((r) => r.groupId === best.audioGroupId) ??
              null;
          }
          continue; // fetch the selected media playlist
        }
        this.refreshFailures = 0;
        return playlist;
      } catch (e) {
        if (this.destroyed) return null;
        this.refreshFailures++;
        Log.w(TAG, `Playlist load failed (${this.refreshFailures}/${MAX_REFRESH_FAILURES}): ${(e as Error).message}`);
        if (this.refreshFailures >= MAX_REFRESH_FAILURES) {
          throw e;
        }
        await this.sleep((this.targetDuration / 2) * 1000);
      }
    }
    return null;
  }

  private async fetchOnce(url: string) {
    if (this.preloaded) {
      const { text, url: baseUrl } = this.preloaded;
      this.preloaded = null;
      return parseM3U8(text, baseUrl);
    }
    let response: Response;
    try {
      response = await fetch(url, {
        headers: this.config.headers,
        signal: this.abort.signal,
        referrerPolicy: (this.config.referrerPolicy as ReferrerPolicy | undefined) ?? "no-referrer-when-downgrade",
      });
    } catch (error) {
      if (this.abort.signal.aborted) throw error;
      const message = error instanceof Error ? error.message : String(error);
      throw new HlsRequestError(url, undefined, undefined, message);
    }
    if (!response.ok) {
      throw new HlsRequestError(response.url || url, response.status, response.statusText);
    }
    let text: string;
    try {
      text = await response.text();
    } catch (error) {
      if (this.abort.signal.aborted) throw error;
      const message = error instanceof Error ? error.message : String(error);
      throw new HlsRequestError(url, undefined, undefined, message);
    }
    return parseM3U8(text, response.url || url);
  }

  private sleep(ms: number): Promise<void> {
    return new Promise((resolve) => {
      const timer = setTimeout(resolve, ms);
      this.abort.signal.addEventListener(
        "abort",
        () => {
          clearTimeout(timer);
          resolve();
        },
        { once: true },
      );
    });
  }
}
