/**
 * 浏览器/设备能力检测。
 * 设计 §5.15：保留能力分支（isLGWebOS / MSE 支持检测），低版本设备回退 native 播放。
 */

/** LG webOS（电视浏览器）——其 MSE/ManagedMediaSource 支持不完整，通常回退 native。 */
export function isLGWebOS(): boolean {
  if (typeof navigator === "undefined") return false;
  return /web0s|webos|lgbrowser/i.test(navigator.userAgent);
}

/**
 * MSE 可用性探测串矩阵。
 *
 * 单串探测（Baseline H.264 + AAC）过严：部分浏览器/定制内核只对某些档位的 codec 串
 * 返回 true（例如只认 Main/High、或只认视频轨），会把其实可用的环境误判为"不支持"，
 * 从而静默降级到原生播放——原生路径没有解封装，媒体信息徽标（分辨率/帧率/编码）就
 * 永远不出来（用户实测：小米浏览器整排徽标缺失、部分台黑屏）。
 * 只要任一「带 codec 串」的探测通过，就按支持处理。
 */
const MSE_PROBE_MIMES = [
  'video/mp4; codecs="avc1.42E01E,mp4a.40.2"',
  'video/mp4; codecs="avc1.4D401E,mp4a.40.2"',
  'video/mp4; codecs="avc1.640028,mp4a.40.2"',
  'video/mp4; codecs="avc1.42E01E"',
  'video/mp4; codecs="avc1.4D401E"',
];

/** 是否可用 MSE 播放（含 ManagedMediaSource）。 */
export function isMSEPlaybackSupported(): boolean {
  const scope = (typeof self !== "undefined" ? self : ({} as Record<string, unknown>)) as Record<string, unknown>;
  const mse = scope.MediaSource as { isTypeSupported?: (t: string) => boolean } | undefined;
  const managed = scope.ManagedMediaSource as { isTypeSupported?: (t: string) => boolean } | undefined;
  if (!mse?.isTypeSupported && !managed?.isTypeSupported) return false;
  try {
    return MSE_PROBE_MIMES.some((mime) => !!(mse?.isTypeSupported?.(mime) || managed?.isTypeSupported?.(mime)));
  } catch {
    // 低版本 WebView 的 MSE 半实现可能在探测时抛异常 → 视为不支持
    return false;
  }
}

/** 逐串列出 MSE 探测结果（诊断浮层用：一眼看出是探测被判死还是流本身的问题）。 */
export function probeMSESupport(): Array<{ mime: string; supported: boolean }> {
  const scope = (typeof self !== "undefined" ? self : ({} as Record<string, unknown>)) as Record<string, unknown>;
  const mse = scope.MediaSource as { isTypeSupported?: (t: string) => boolean } | undefined;
  const managed = scope.ManagedMediaSource as { isTypeSupported?: (t: string) => boolean } | undefined;
  return MSE_PROBE_MIMES.map((mime) => {
    try {
      return { mime, supported: !!(mse?.isTypeSupported?.(mime) || managed?.isTypeSupported?.(mime)) };
    } catch {
      return { mime, supported: false };
    }
  });
}

