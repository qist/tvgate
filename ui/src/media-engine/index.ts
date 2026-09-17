/**
 * media-engine —— 播放引擎（clean-room 自研实现，已全面取代旧 GPL 引擎，旧目录已移除）。
 * 覆盖格式解析 / 片段生成 / 解复用 / 转封装 / 软解 / MSE 播放与音频同步全链路。
 * 对外契约类型（PlayerConfig / PlaybackBackend / HlsInfo …）定义在 backends/types.ts，
 * UI 只依赖本模块导出的公共 API。
 */
export * from "./formats/m3u8";
export * from "./formats/mp4-box";
export * from "./formats/mp4-generator";
export * from "./formats/ts";
export * from "./formats/avc";
export * from "./formats/aac";
export * from "./demux/ts-demuxer";
export * from "./remux/fmp4-remuxer";
export * from "./io/fetch-loader";
export * from "./pipeline/transmux-pipeline";
export * from "./mse/media-source-controller";
export * from "./mse/playback-controller";
export * from "./timeline";
export * from "./backends/types";
export * from "./backends/event-emitter";
export * from "./worker/messages";
export * from "./worker/worker-client";
export * from "./decoder/types";
export * from "./decoder/wasm-audio-decoder";
export * from "./decoder/ffmpeg-bridge";
export * from "./decoder/builtin-wasm";
export * from "./render/video-renderer";
export * from "./hls/segment-source";
export * from "./hls/hls-source";
export * from "./backends/mse-backend";
export * from "./backends/native-backend";
export * from "./audio/time-stretch";
export * from "./audio/pcm-audio-player";
export * from "./config";
export * from "./errors";
export * from "./media-codecs";
export * from "./platform";
export * from "./backends/factory";
export * from "./types";
