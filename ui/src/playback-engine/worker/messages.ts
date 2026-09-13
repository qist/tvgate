import type { PlayerConfig } from "../config";
import type { DemuxErrorDetail, LoaderErrorDetail } from "../errors";
import type { PlayerMediaInfo, PlayerSegment } from "../types";

export type WorkerCommand =
  | { type: "init"; segments: PlayerSegment[]; config: PlayerConfig; gen: number }
  | { type: "start" }
  | { type: "load-segments"; segments: PlayerSegment[]; gen: number }
  | { type: "pause" }
  | { type: "resume" }
  | { type: "reset" }
  | {
    type: "audio-anchor";
    /** Current video position on the MSE timeline (seconds). The worker re-bases
     *  subsequent software-decoded PCM timestamps so they land on this video clock,
     *  closing the gap when the video timeline shifted independently of the audio. */
    videoTimeMs: number;
    gen: number;
  }
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
  /** Audio track discontinuity detected in the worker; main thread should re-anchor the PCM player. */
  | { type: "pcm-audio-discontinuity"; gen: number }
  /** Ordered acknowledgement: later PCM messages use the requested video anchor. */
  | { type: "pcm-audio-anchor"; videoTime: number; gen: number }
  | {
    type: "pcm-audio-data";
    pcm: ArrayBuffer;
    channels: number;
    sampleRate: number;
    /** Start time normalized to the MSE timeline (seconds). */
    time: number;
    gen: number;
  };
