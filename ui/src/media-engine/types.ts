/**
 * media-engine 共享类型。
 * 供 demux / remux / pipeline / mse 共同引用，避免在多个模块重复定义导致导出歧义。
 */

/** 分段源模式。 */
export type SourceMode = "continuous-live-ts" | "static-ts-list" | "hls";
