/**
 * 浏览器/设备能力检测。
 * 设计 §5.15：保留能力分支（isLGWebOS / MSE 支持检测），低版本设备回退 native 播放。
 */

/** LG webOS（电视浏览器）——其 MSE/ManagedMediaSource 支持不完整，通常回退 native。 */
export function isLGWebOS(): boolean {
  if (typeof navigator === "undefined") return false;
  return /web0s|webos|lgbrowser/i.test(navigator.userAgent);
}

/** 是否可用 MSE 播放（含 ManagedMediaSource）。 */
export function isMSEPlaybackSupported(): boolean {
  const avcMime = 'video/mp4; codecs="avc1.42E01E,mp4a.40.2"';
  const scope = (typeof self !== "undefined" ? self : ({} as Record<string, unknown>)) as Record<string, unknown>;
  const mse = scope.MediaSource as { isTypeSupported?: (t: string) => boolean } | undefined;
  const managed = scope.ManagedMediaSource as { isTypeSupported?: (t: string) => boolean } | undefined;
  try {
    return !!(mse?.isTypeSupported?.(avcMime) || managed?.isTypeSupported?.(avcMime));
  } catch {
    // 低版本 WebView 的 MSE 半实现可能在探测时抛异常 → 视为不支持
    return false;
  }
}

