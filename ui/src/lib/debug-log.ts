/**
 * 播放器调试日志开关（主线程侧）。
 * 与诊断浮层同一开关语义：URL ?dbg=1 / localStorage['tvgate-player-debug']='1' /
 * 控制台 __tvdbg(true) 实时切换。
 * 未开启时诊断性日志一律不输出，避免线上 console 噪音；error 级日志不受影响。
 */

let override: boolean | null = null;

export function isPlayerDebugEnabled(): boolean {
  if (override !== null) return override;
  try {
    if (typeof window === "undefined") return false;
    if (new URL(window.location.href).searchParams.get("dbg") === "1") return true;
    return window.localStorage.getItem("tvgate-player-debug") === "1";
  } catch {
    return false;
  }
}

/** __tvdbg(true/false) 同步切换诊断日志（与诊断浮层共用一个入口）。 */
export function setPlayerDebugOverride(on: boolean): void {
  override = on ? true : null;
}

/** 诊断性日志：仅在调试开关开启时输出。 */
export function debugWarn(...args: unknown[]): void {
  if (isPlayerDebugEnabled()) console.warn(...args);
}
