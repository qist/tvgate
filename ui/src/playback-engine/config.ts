/** Playback backend configuration options. All fields are optional when passed to a backend factory. */
export interface PlayerConfig {
  /** Chase live stream latency by changing playbackRate. @default true */
  liveSync: boolean;
  /** Maximum acceptable buffer latency in seconds. Requires `liveSync: true`. @default 1.2 */
  liveSyncMaxLatency: number;
  /** Target latency in seconds to chase when latency exceeds `liveSyncMaxLatency`. Requires `liveSync: true`. @default 0.8 */
  liveSyncTargetLatency: number;
  /** PlaybackRate (clamped to [1, 2]) used for latency chasing. Requires `liveSync: true`. @default 1.2 */
  liveSyncPlaybackRate: number;

  /** URLs to WASM decoder files, keyed by codec. Omit to disable software decoding for that codec.
   *  e.g. `{ mp2: "/assets/avcodec_audio.wasm", ac3: "/assets/avcodec_audio.wasm" }` —
   *  同一个模块同时提供 MP2 / AC-3 / E-AC-3 解码。
   *  `ac3` 在浏览器 MSE 无法原生解码 ac-3/ec-3 时启用软解（逐帧解码后由 PCM 播放器输出）。 */
  wasmDecoders: { mp2?: string; ac3?: string };

  /**
   * 音画偏移的标定常量（毫秒，正 = 延后音频，负 = 提前音频）。
   *
   * 为什么必须存在：软解音频经 WebAudio 出声，视频经 MSE/合成器上屏，两条管线各自
   * 的延迟里有一部分**在 DOM 里无法测量**（媒体元素音频输出延迟、合成器上屏延迟、
   * 设备缓冲）。测量漂移用的基准若不含这一项，控制环会一路"收敛"到错的位置——
   * 表现为日志里 drift≈0 但眼睛看到固定错位。所以它是一个**每台设备标定一次**的
   * 常量，而不是可以被闭环算出来的量。
   *
   * 标定方法：播放一个人脸播报频道，调此值到口型对上。
   * @default 0
   */
  audioSyncOffsetMs: number;

  /** Max backward buffer duration in seconds. Cleanup triggers when buffer exceeds this. @default 180 */
  bufferCleanupMaxBackward: number;
  /** Min backward buffer to retain after cleanup in seconds. @default 120 */
  bufferCleanupMinBackward: number;

  /** Referrer policy for HTTP requests. Applied to each segment's `referrerPolicy` field. */
  referrerPolicy: string | undefined;
  /** Additional headers to add to HTTP requests. */
  headers: Record<string, string> | undefined;
  /** Frontend log level: 0=FATAL, 1=ERROR, 2=WARN, 3=INFO, 4=DEBUG/VERBOSE. */
  logLevel: number | undefined;

  /** Overlay canvas the player draws WebGL-rendered frames onto. Omit to disable WebGL video rendering.
   *  The player never touches the canvas' style/visibility — drive that from the
   *  `render-state-change` event. */
  renderCanvas: HTMLCanvasElement | undefined;
  /** Automatic bwdif deinterlacing enabled when video metadata declares interlaced scan. @default true */
  autoDeinterlace: boolean;
  /** Lightweight WebGL picture enhancement enabled inside the render gate. @default true */
  pictureEnhancement: boolean;
}

export const defaultConfig: PlayerConfig = {
  liveSync: true,
  liveSyncMaxLatency: 3,
  liveSyncTargetLatency: 1.5,
  liveSyncPlaybackRate: 1.2,

  wasmDecoders: {},

  audioSyncOffsetMs: 0,

  bufferCleanupMaxBackward: 180,
  bufferCleanupMinBackward: 120,

  referrerPolicy: undefined,
  headers: undefined,
  logLevel: undefined,

  renderCanvas: undefined,
  autoDeinterlace: true,
  pictureEnhancement: true,
};

export function createDefaultConfig(): PlayerConfig {
  return { ...defaultConfig };
}
