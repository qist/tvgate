import { getRuntimeLogLevel } from "../../lib/runtime-config";
import {
  AUDIO_ROUTE_WAIT_MS,
  isNativeAudioAvailable,
  resolveAudioPathOverride,
  setAudioRoute,
  waitForNativeAudioProbe,
  warmupNativeAudioProbe,
} from "../audio/audio-router";
import { defaultConfig, type PlayerConfig } from "../config";
import { createMSEPlaybackController } from "../mse/playback-controller";
import { createVideoRenderPipeline, type VideoRenderPipeline } from "../render";
import type { LiveSessionAnchor, MSEPlaybackController, PlaybackBackend } from "../types";
import Log from "../utils/logger";
import { createPlaybackEventEmitter, resolveSegmentUrls } from "./backend-utils";

const TAG = "MSEBackend";

function resolveConfig(config?: Partial<PlayerConfig>): PlayerConfig {
  const fullConfig: PlayerConfig = { ...defaultConfig, ...config };
  fullConfig.logLevel = config?.logLevel ?? getRuntimeLogLevel() ?? fullConfig.logLevel;
  Log.setLogLevel(fullConfig.logLevel);
  fullConfig.logLevel = Log.LOG_LEVEL;

  // Resolve WASM URLs to absolute so they work inside inline blob workers.
  const wasmDecoders: PlayerConfig["wasmDecoders"] = { ...fullConfig.wasmDecoders };
  if (wasmDecoders.mp2) {
    wasmDecoders.mp2 = new URL(wasmDecoders.mp2, document.baseURI).href;
  }
  // AC-3/E-AC-3：默认仍走软解，但会由 audio-router 用**内置片段真实解码**判定平台是否
  // 真的能硬解（Chromium 的 isTypeSupported 在无 Dolby 解码器时也会返回 true，不能采信）。
  // 判定为可用时在 loadSegments 阶段去掉软解开关（见 resolveAudioOverride）。任何探测
  // 失败/超时都回落软解，不会让设备失去声音。
  // 手工覆盖：?audioPath=hw 强制原生；?audioPath=sw 强制软解（A/B 对比与故障回退）。
  const override = resolveAudioPathOverride();
  if (wasmDecoders.ac3) {
    if (override === "hw") {
      delete wasmDecoders.ac3;
      setAudioRoute({ path: "native", reason: "forced by ?audioPath=hw" });
    } else {
      wasmDecoders.ac3 = new URL(wasmDecoders.ac3, document.baseURI).href;
      if (override === "sw") {
        setAudioRoute({ path: "software", reason: "forced by ?audioPath=sw" });
      }
    }
  } else {
    // 没配软解 wasm：AC-3/E-AC-3 只能交平台原生解码（AAC/MP3 等与路径无关）。
    setAudioRoute({ path: "native", reason: "no software decoder configured" });
  }
  fullConfig.wasmDecoders = wasmDecoders;

  return fullConfig;
}

export function createMSEPlaybackBackend(video: HTMLVideoElement, config?: Partial<PlayerConfig>): PlaybackBackend {
  const fullConfig = resolveConfig(config);
  let destroyed = false;
  const events = createPlaybackEventEmitter();

  // 提前在隐藏元素里探测平台是否真能硬解 AC-3/E-AC-3：结论在 loadSegments 时取用，
  // 探测与起播并行，不在关键路径上。仅当"配了软解 wasm 且未被 URL 手工指定"时才需要。
  if (fullConfig.wasmDecoders.ac3 && resolveAudioPathOverride() === null) {
    void warmupNativeAudioProbe();
  }

  /**
   * 自动模式下取一次探测结论：确认原生可用则去掉 AC-3 软解开关，让音频走 MSE 硬解。
   * 探测未就绪（超时）时返回空覆盖，即本次先用软解 —— 只可能"少优化"，不会没声音。
   */
  async function resolveAudioOverride(): Promise<Partial<PlayerConfig>> {
    if (!fullConfig.wasmDecoders.ac3 || resolveAudioPathOverride() !== null) {
      return {};
    }
    const ready = await waitForNativeAudioProbe();
    if (!ready) {
      Log.w(TAG, "Audio route probe not ready within the wait window; using software decode this session");
      setAudioRoute({
        path: "software",
        reason: `probe not ready within ${AUDIO_ROUTE_WAIT_MS}ms; software decode this session`,
      });
      return {};
    }
    if (!isNativeAudioAvailable()) {
      setAudioRoute({ path: "software", reason: "probe: platform cannot decode AC-3/E-AC-3 natively" });
      return {};
    }
    Log.i(TAG, "Native AC-3/E-AC-3 decode available; software decode disabled for this session");
    setAudioRoute({ path: "native", reason: "probe: native AC-3/E-AC-3 decode confirmed" });
    const next: PlayerConfig["wasmDecoders"] = {};
    if (fullConfig.wasmDecoders.mp2) {
      next.mp2 = fullConfig.wasmDecoders.mp2;
    }
    return { wasmDecoders: next };
  }

  let renderPipeline: VideoRenderPipeline | null = null;
  if (fullConfig.renderCanvas) {
    renderPipeline = createVideoRenderPipeline(video, fullConfig.renderCanvas, (state) =>
      events.emit("render-state-change", state),
    );
    renderPipeline.setAutoDeinterlaceEnabled(fullConfig.autoDeinterlace);
    renderPipeline.setPictureEnhancementEnabled(fullConfig.pictureEnhancement);
  }

  let controller: MSEPlaybackController | null = null;

  const onTimeUpdate = () => events.emit("time-update", video.currentTime);
  const onEnded = () => events.emit("ended");
  const onCanPlay = (event: Event) => events.emit("playback-state-change", "canplay", event.timeStamp);
  const onPlaying = (event: Event) => events.emit("playback-state-change", "playing", event.timeStamp);
  const onPause = (event: Event) => events.emit("playback-state-change", "paused", event.timeStamp);
  const onWaiting = (event: Event) => events.emit("playback-state-change", "waiting", event.timeStamp);
  const onVolumeChange = () => events.emit("volume-change", video.volume, video.muted);
  video.addEventListener("timeupdate", onTimeUpdate);
  video.addEventListener("ended", onEnded);
  video.addEventListener("canplay", onCanPlay);
  video.addEventListener("playing", onPlaying);
  video.addEventListener("pause", onPause);
  video.addEventListener("waiting", onWaiting);
  video.addEventListener("volumechange", onVolumeChange);

  function getController(audioOverride?: Partial<PlayerConfig>): MSEPlaybackController {
    if (!controller) {
      // DOM elements are not structured-cloneable, so keep the canvas out of the worker config.
      controller = createMSEPlaybackController(
        video,
        { ...fullConfig, ...audioOverride, renderCanvas: undefined },
        events.getHandlers("seek-needed"),
      );
      controller.onError = (error) => events.emit("error", error);
      controller.onLiveStateChange = (isLive) => events.emit("live-state-change", isLive);
      controller.onAudioSuspended = () => events.emit("audio-suspended");
      controller.onAudioStats = (stats) => events.emit("audio-stats", stats);
      controller.onMediaInfo = (info) => {
        renderPipeline?.setScanType(info.video?.scanType);
        events.emit("media-info", info);
      };
    }
    return controller;
  }

  return {
    kind: "mse",
    mediaElement: video,

    loadSegments(segments) {
      if (destroyed || !segments.length) return;
      renderPipeline?.reset();
      const urls = resolveSegmentUrls(segments);
      if (controller) {
        controller.loadSegments(urls);
        return;
      }
      // 首次装载：等一次音频路径探测结论（上限 500ms，通常已由预热完成）。
      // 探测本身已 fail-safe，这里再兜一层：任何异常都必须继续装载，不能因为探测
      // 失败就把起播卡死。
      void resolveAudioOverride()
        .catch(() => ({}) as Partial<PlayerConfig>)
        .then((audioOverride) => {
          if (destroyed) return;
          if (controller) {
            controller.loadSegments(urls);
            return;
          }
          getController(audioOverride).loadSegments(urls);
        });
    },

    play: () => video.play(),
    pause: () => video.pause(),
    setVolume: (volume) => {
      video.volume = volume;
    },
    setMuted: (muted) => {
      video.muted = muted;
    },

    getState: () => ({
      currentTime: video.currentTime,
      duration: video.duration,
      paused: video.paused,
      playbackRate: video.playbackRate || 1,
      volume: video.volume,
      muted: video.muted,
    }),

    seek: (seconds) => controller?.seek(seconds),
    goLive: (targetMseSeconds) => controller?.goLive(targetMseSeconds),
    setLiveSessionAnchor: (anchor: LiveSessionAnchor) => controller?.setLiveSessionAnchor(anchor),
    setLiveSync: (enabled) => controller?.setLiveSync(enabled),
    setAutoDeinterlace: (enabled) => renderPipeline?.setAutoDeinterlaceEnabled(enabled),
    setPictureEnhancement: (enabled) => renderPipeline?.setPictureEnhancementEnabled(enabled),

    stop() {
      if (destroyed) return;
      renderPipeline?.reset();
      controller?.suspend();
    },

    destroy() {
      if (destroyed) return;
      destroyed = true;
      video.removeEventListener("timeupdate", onTimeUpdate);
      video.removeEventListener("ended", onEnded);
      video.removeEventListener("canplay", onCanPlay);
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("pause", onPause);
      video.removeEventListener("waiting", onWaiting);
      video.removeEventListener("volumechange", onVolumeChange);
      renderPipeline?.destroy();
      renderPipeline = null;
      controller?.destroy();
      controller = null;
    },

    on(event, handler) {
      events.on(event, handler);
    },

    off(event, handler) {
      events.off(event, handler);
    },
  };
}

export function isMSEPlaybackSupported(): boolean {
  const avcMime = 'video/mp4; codecs="avc1.42E01E,mp4a.40.2"';
  const mse = (self as unknown as Record<string, unknown>).MediaSource as
    | { isTypeSupported?: (type: string) => boolean }
    | undefined;
  const managedMse = (self as unknown as Record<string, unknown>).ManagedMediaSource as
    | { isTypeSupported?: (type: string) => boolean }
    | undefined;
  try {
    return !!(mse?.isTypeSupported?.(avcMime) || managedMse?.isTypeSupported?.(avcMime));
  } catch {
    // 低版本 WebView 的 MSE 半实现可能在探测时抛异常 → 视为不支持，回退 native
    return false;
  }
}
