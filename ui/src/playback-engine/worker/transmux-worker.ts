import type { PlayerConfig } from "../config";
import type { PlayerSegment } from "../types";
import Log from "../utils/logger";
import type { WorkerCommand, WorkerEvent } from "./messages";
import Pipeline, { type PipelineCallbacks } from "./pipeline";

let pipeline: Pipeline | null = null;
let gen = 0;

// --- Separate-audio init gating ---
// On WebKit-family engines the SourceBuffer set is locked once the first init
// segment is parsed; a later addSourceBuffer throws QuotaExceededError and the
// stream plays muted. With separate EXT-X-MEDIA audio the audio init usually
// arrives after video media segments, so hold back ALL media segments until
// every expected init has been posted (or the audio track is given up).
let separateAudio = false;
let initsSent = { video: false, audio: false };
let heldMedia: WorkerEvent[] = [];

function resetGating(): void {
  separateAudio = false;
  initsSent = { video: false, audio: false };
  heldMedia = [];
}

function releaseHeldMedia(): void {
  if (heldMedia.length === 0) return;
  const held = heldMedia;
  heldMedia = [];
  for (const msg of held) {
    post(msg, "data" in msg ? [msg.data as ArrayBuffer] : undefined);
  }
}

function mediaGateOpen(): boolean {
  return !separateAudio || (initsSent.video && initsSent.audio);
}

function post(msg: WorkerEvent, transfer?: Transferable[]): void {
  if (transfer) {
    (self as unknown as { postMessage(msg: unknown, transfer: Transferable[]): void }).postMessage(msg, transfer);
  } else {
    (self as unknown as { postMessage(msg: unknown): void }).postMessage(msg);
  }
}

function createPipeline(segments: PlayerSegment[], config: PlayerConfig): Pipeline {
  const callbacks: PipelineCallbacks = {
    onInitSegment(type, initSegment) {
      const data = initSegment.data as ArrayBuffer;
      if (type === "video" || type === "audio") {
        initsSent[type] = true;
      }
      post(
        {
          type: "init-segment",
          track: type as "video" | "audio",
          data,
          codec: initSegment.codec ?? "",
          container: initSegment.container,
          gen,
        },
        [data],
      );
      releaseHeldMedia();
    },
    onMediaSegment(type, mediaSegment) {
      const data = mediaSegment.data as ArrayBuffer;
      const msg: WorkerEvent = {
        type: "media-segment",
        track: type as "video" | "audio",
        data,
        timestampOffset: mediaSegment.timestampOffset,
        gen,
      };
      if (!mediaGateOpen()) {
        heldMedia.push(msg);
        return;
      }
      post(msg, [data]);
    },
    onLoadingComplete() {
      releaseHeldMedia();
      post({ type: "complete", gen });
    },
    onIOError(type, info) {
      post({ type: "error", category: "io", detail: type, info: info.msg, code: info.code, url: info.url, gen });
    },
    onDemuxError(type, info) {
      post({ type: "error", category: "demux", detail: type, info, gen });
    },
    onHlsInfo(info) {
      separateAudio = info.separateAudio;
      if (!separateAudio) {
        releaseHeldMedia();
      }
      post({ type: "hls-info", live: info.live, totalDuration: info.totalDuration, separateAudio: info.separateAudio, gen });
    },
    onAudioDisabled() {
      separateAudio = false;
      releaseHeldMedia();
      post({ type: "audio-disabled", gen });
    },
    onMediaInfo(info) {
      const msg: WorkerEvent = { type: "media-info", info, gen };
      if (!mediaGateOpen()) {
        // media-info would make the main thread flush its pending init batch,
        // appending the video init alone — exactly what must not happen.
        heldMedia.push(msg);
        return;
      }
      post(msg);
    },
    onPCMAudioData(pcm, channels, sampleRate, time) {
      const buffer = pcm.buffer as ArrayBuffer;
      post({ type: "pcm-audio-data", pcm: buffer, channels: channels, sampleRate: sampleRate, time: time, gen }, [buffer]);
    },
  };

  return new Pipeline(segments, config, callbacks);
}

self.addEventListener("message", (e: MessageEvent) => {
  const cmd = e.data as WorkerCommand;

  switch (cmd.type) {
    case "init":
      gen = cmd.gen;
      resetGating();
      Log.setLogLevel(cmd.config.logLevel);
      pipeline = createPipeline(cmd.segments, cmd.config);
      break;
    case "start":
      pipeline?.start();
      break;
    case "load-segments":
      gen = cmd.gen;
      resetGating();
      pipeline?.loadSegments(cmd.segments);
      break;
    case "pause":
      pipeline?.pause();
      break;
    case "resume":
      pipeline?.resume();
      break;
    case "reset":
      if (pipeline) {
        pipeline.destroy();
        pipeline = null;
      }
      break;
    case "destroy":
      if (pipeline) {
        pipeline.destroy();
        pipeline = null;
      }
      (self as unknown as { postMessage(msg: unknown): void }).postMessage({ type: "destroyed" });
      break;
  }
});
