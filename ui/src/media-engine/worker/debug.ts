/**
 * Worker 线程侧的调试日志开关。
 * worker 里没有 localStorage/URL 上下文，开关由主线程随 load 命令下发
 * （messages.ts load.debug ← isPlayerDebugEnabled()），收到即生效。
 */

let enabled = false;

export function setWorkerDebug(on: boolean): void {
  enabled = on;
}

/** 诊断性日志：仅在主线程下发的调试开关开启时输出。 */
export function workerDebugWarn(...args: unknown[]): void {
  if (enabled) console.warn(...args);
}
