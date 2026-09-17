/**
 * 直播时间轴 / 墙钟映射。
 * §10：timeline API 属共享契约，签名与语义须与 UI 依赖保持一致。
 * liveSessionAnchor = 会话起始（墙钟 + 对应 MSE 时间），据此推算「直播边」随时间推进的位置。
 */

export interface LiveSessionAnchor {
  sessionStartMs: number;
  mseAtSessionStart: number;
}

/** MSE 时间轴秒 → 墙钟时间（以流起点 origin 为基准）。 */
export function mseToWallClock(mseSeconds: number, origin: Date): Date {
  return new Date(origin.getTime() + mseSeconds * 1000);
}

/** 墙钟时间 → MSE 时间轴秒。 */
export function wallClockToMse(time: Date, origin: Date): number {
  return (time.getTime() - origin.getTime()) / 1000;
}

/** 当前会话直播边在 MSE 时间轴上的位置（会话起始 + 已过墙钟时长）。 */
export function liveEdgeMse(anchor: LiveSessionAnchor, nowMs = Date.now()): number {
  return anchor.mseAtSessionStart + (nowMs - anchor.sessionStartMs) / 1000;
}

/** 直播边的墙钟时间（EPG / 进度条用）。 */
export function liveEdgeWallClock(anchor: LiveSessionAnchor, origin: Date, nowMs = Date.now()): Date {
  return mseToWallClock(liveEdgeMse(anchor, nowMs), origin);
}

/** 播放位置落后直播边多少秒（MSE 时间轴）。 */
export function lagBehindLiveEdge(
  anchor: LiveSessionAnchor,
  currentTime: number,
  nowMs = Date.now(),
): number {
  return liveEdgeMse(anchor, nowMs) - currentTime;
}

/** Go Live 的 MSE 目标：直播边减去目标延迟。 */
export function goLiveTargetMse(
  anchor: LiveSessionAnchor,
  targetLatencySec: number,
  nowMs = Date.now(),
): number {
  return liveEdgeMse(anchor, nowMs) - targetLatencySec;
}

/** 以当前播放位置为起点建立会话锚点（直播会话开始时调用一次）。 */
export function createLiveSessionAnchor(currentTime: number, nowMs = Date.now()): LiveSessionAnchor {
  return { sessionStartMs: nowMs, mseAtSessionStart: currentTime };
}

/** 判定「接近直播边」的容差（毫秒）。 */
export const NEAR_LIVE_EDGE_MS = 10_000;

/** 给定墙钟 seek 目标是否接近直播边。 */
export function isNearLiveWallClock(
  seekTime: Date,
  anchor: LiveSessionAnchor | null,
  origin: Date,
  nowMs = Date.now(),
): boolean {
  if (!anchor) return seekTime.getTime() >= nowMs - NEAR_LIVE_EDGE_MS;
  return seekTime.getTime() >= liveEdgeWallClock(anchor, origin, nowMs).getTime() - NEAR_LIVE_EDGE_MS;
}
