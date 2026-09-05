import type { PlayerConfig } from "../config";
import Log from "../utils/logger";

const TAG = "LiveSync";

/** Each live-edge underrun raises the latency floor by this much (seconds). */
const UNDERRUN_BACKOFF_STEP = 1;
/** Upper bound for the adaptive latency increase (seconds). */
const UNDERRUN_BACKOFF_MAX = 6;

/** Maximum buffered-gap width the heal step will jump across (seconds). */
const GAP_HEAL_MAX = 1.5;
/** Retry budget per stall for gap healing (covers appends still in flight). */
const GAP_HEAL_RETRIES = 8;
/** Interval between gap-heal retries (ms). */
const GAP_HEAL_RETRY_MS = 600;

/** Forward buffer seconds ahead of currentTime within the containing range. */
function forwardBufferAhead(video: HTMLMediaElement): number {
  const t = video.currentTime;
  const buffered = video.buffered;
  for (let i = 0; i < buffered.length; i++) {
    if (t >= buffered.start(i) && t <= buffered.end(i)) {
      return buffered.end(i) - t;
    }
  }
  return 0;
}

/**
 * Jump the playhead across a hairline gap between buffered ranges. Segment
 * boundaries can carry slightly discontinuous DTS (0.06-0.3s slits, larger
 * after skipped segments); HTMLMediaElement never advances across them and
 * stalls forever even though continuous data follows.
 */
function healBufferedGap(video: HTMLMediaElement): boolean {
  const t = video.currentTime;
  const buffered = video.buffered;
  for (let i = 0; i < buffered.length; i++) {
    if (t < buffered.start(i) || t > buffered.end(i)) {
      continue;
    }
    if (i + 1 >= buffered.length) {
      return false;
    }
    const gap = buffered.start(i + 1) - buffered.end(i);
    if (gap > 0.001 && gap <= GAP_HEAL_MAX) {
      const target = buffered.start(i + 1) + 0.001;
      Log.w(
        TAG,
        `Healing buffered gap: playhead ${t.toFixed(3)} -> ${target.toFixed(3)} (gap ${gap.toFixed(3)}s)`,
      );
      video.currentTime = target;
      return true;
    }
    return false;
  }
  return false;
}

/** Sets up live latency synchronization by adjusting playbackRate on timeupdate events. */
export function setupLiveSync(
  video: HTMLMediaElement,
  config: PlayerConfig,
  getLiveEdgeLatency: () => number | null,
  canAdjustPlaybackRate: () => boolean = () => true,
): () => void {
  if (config.liveSync) {
    Log.v(
      TAG,
      "Live sync enabled, target latency:",
      config.liveSyncTargetLatency,
      "max latency:",
      config.liveSyncMaxLatency,
    );
  }

  let extraLatency = 0;
  let gapHealTimer: ReturnType<typeof setTimeout> | null = null;
  let gapHealTries = 0;

  /** Retarget the gap-heal retry while a stall persists (appends may still be landing). */
  function scheduleGapHeal(): void {
    if (gapHealTries >= GAP_HEAL_RETRIES) return;
    gapHealTries++;
    if (gapHealTimer !== null) clearTimeout(gapHealTimer);
    gapHealTimer = setTimeout(() => {
      gapHealTimer = null;
      if (video.paused || video.seeking || video.readyState >= 3) {
        gapHealTries = 0;
        return;
      }
      if (!healBufferedGap(video)) {
        scheduleGapHeal();
      } else {
        gapHealTries = 0;
      }
    }, GAP_HEAL_RETRY_MS);
  }

  function resetGapHeal(): void {
    gapHealTries = 0;
    if (gapHealTimer !== null) {
      clearTimeout(gapHealTimer);
      gapHealTimer = null;
    }
  }

  function onTimeUpdate(): void {
    if (!config.liveSync) return;
    if (!canAdjustPlaybackRate()) return;

    // Playhead advanced: any pending gap-heal attempt is no longer needed
    resetGapHeal();

    const latency = getLiveEdgeLatency();
    if (latency === null) return;

    if (latency > config.liveSyncMaxLatency + extraLatency) {
      const targetRate = Math.min(2, Math.max(1, config.liveSyncPlaybackRate));
      if (targetRate !== video.playbackRate) {
        Log.v(TAG, `Video playback rate set to ${targetRate}`);
        video.playbackRate = targetRate;
      }
    } else if (latency <= config.liveSyncTargetLatency + extraLatency) {
      if (video.playbackRate !== 1 && video.playbackRate !== 0) {
        video.playbackRate = 1;
        Log.v(TAG, "Video playback rate reset to 1");
      }
      // Recovered — drop adaptive backoff
      if (extraLatency > 0 && latency <= config.liveSyncTargetLatency) {
        extraLatency = 0;
      }
    }
  }

  function onWaiting(): void {
    if (!config.liveSync) return;
    if (!canAdjustPlaybackRate()) return;

    // Seek/Go Live often fires waiting while data is still buffered ahead — not an underrun.
    if (video.seeking) return;

    // Playhead may be stuck on a hairline buffered gap: heal it (with retries,
    // since the chunk bridging the slit may still be appending) instead of
    // stalling forever while continuous data sits in the following range.
    if (gapHealTries === 0 && healBufferedGap(video)) {
      return;
    }
    scheduleGapHeal();

    const lag = getLiveEdgeLatency();
    if (lag === null) return;

    const ahead = forwardBufferAhead(video);
    // Near source-mode live edge AND playhead has caught up with its forward buffer.
    const atLiveEdge = lag < 0.5 && ahead < 0.5;
    if (!atLiveEdge) return;

    if (video.playbackRate !== 1 && video.playbackRate !== 0) {
      video.playbackRate = 1;
    }

    if (extraLatency < UNDERRUN_BACKOFF_MAX) {
      extraLatency = Math.min(extraLatency + UNDERRUN_BACKOFF_STEP, UNDERRUN_BACKOFF_MAX);
    }
    Log.w(
      TAG,
      `Live-edge underrun, raising latency tolerance: target ${(config.liveSyncTargetLatency + extraLatency).toFixed(1)}s, max ${(config.liveSyncMaxLatency + extraLatency).toFixed(1)}s`,
    );
  }

  video.addEventListener("timeupdate", onTimeUpdate);
  video.addEventListener("waiting", onWaiting);

  return () => {
    Log.v(TAG, "Video playback rate reset to 1, live sync disabled");
    video.removeEventListener("timeupdate", onTimeUpdate);
    video.removeEventListener("waiting", onWaiting);
    resetGapHeal();
    video.playbackRate = 1;
  };
}
