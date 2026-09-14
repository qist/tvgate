import type { PlayerConfig } from "../config";
import type { DemuxErrorDetail, LoaderErrorDetail } from "../errors";
import type { PlayerMediaInfo, PlayerSegment } from "../types";

/**
 * Software-decoded PCM chain drop/error counters (worker side).
 * All counters are monotonic within a session; used to diagnose "silent frame drop"
 * complaints — every gate that can silently discard audio reports here.
 */
export interface PcmWorkerStats {
  /** _pendingPcm pre-buffer overflow (oldest chunks shifted out while dts base unknown). */
  pendingOverflowDrops: number;
  /** Post-mapping pending queue overflow (oldest chunks shifted out). */
  queueOverflowDrops: number;
  /** Chunks rejected by mapPcmTimestamp (presentation-floor drops). */
  remuxDrop: number;
  /** Chunks partially/fully trimmed by mapPcmTimestamp (trimStartMs > 0). */
  trimDrop: number;
  /** Decode results discarded by the generation guard (channel switch / reset races). */
  genDrops: number;
  /** Decoder WASM init failures (frames arrive but no decoder). */
  decodeInitFailed: number;
  /** Decode results whose samplesBeforeInput was non-zero (parser carry). */
  carryFrames: number;
  /** PCM frames produced by the separate-audio-rendition soft-decode path. */
  renditionFrames: number;
  /** Frames arriving from the non-active source (muxed vs rendition; topologically impossible). */
  dualSourceFrames: number;
}

export type WorkerCommand =
  | { type: "init"; segments: PlayerSegment[]; config: PlayerConfig; gen: number }
  | { type: "start" }
  | { type: "load-segments"; segments: PlayerSegment[]; gen: number }
  | { type: "pause" }
  | { type: "resume" }
  | { type: "reset" }
  | {
      type: "clock";
      /** Main-thread playhead (ms) driving the worker's buffer-lead gate. */
      currentTimeMs: number;
      /** MSE buffered end (ms); -1 when unknown. The worker must not run far ahead of it. */
      bufferedEndMs: number;
      gen: number;
    }
  | { type: "destroy" };

export type WorkerEvent =
  | { type: "init-segment"; track: "video" | "audio"; data: ArrayBuffer; codec: string; container: string; gen: number }
  | { type: "media-info"; info: PlayerMediaInfo; gen: number }
  | { type: "media-segment"; track: "video" | "audio"; data: ArrayBuffer; timestampOffset?: number; gen: number }
  | { type: "complete"; gen: number }
  | {
    type: "error";
    category: "io" | "demux";
    detail: LoaderErrorDetail | DemuxErrorDetail;
    info?: string;
    code?: number;
    url?: string;
    gen: number;
  }
  | { type: "hls-info"; live: boolean; totalDuration: number; separateAudio: boolean; gen: number }
  | { type: "audio-disabled"; gen: number }
  /** Separate-audio rendition is soft-decoded (MP2/AC-3/E-AC-3): no MSE audio init
   *  will ever be posted; the main thread must not wait for it (WebKit init gating). */
  | { type: "audio-rendition-soft-decode"; gen: number }
  /** Audio track discontinuity detected in the worker; main thread should re-anchor the PCM player. */
  | { type: "pcm-audio-discontinuity"; gen: number }
  /** Periodic snapshot of the soft-decoded PCM chain drop counters (throttled, change-only). */
  | { type: "pcm-audio-stats"; stats: PcmWorkerStats; gen: number }
  | {
    type: "pcm-audio-data";
    pcm: ArrayBuffer;
    channels: number;
    sampleRate: number;
    /** Start time normalized to the MSE timeline (seconds). */
    time: number;
    gen: number;
  };
