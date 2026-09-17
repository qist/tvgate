/**
 * 播放器皮肤（界面风格）hook。
 * 与后台共用同一套实现（`use-appearance`）：同一个存储键 + 同一批 `player-theme-*` 类，
 * 所以在同一浏览器里，后台选完风格播放页也是同一套配色，反之亦然。
 */
import { DEFAULT_APPEARANCE, useAppearance } from "./use-appearance";

export { DEFAULT_APPEARANCE };

export function usePlayerAppearance() {
  return useAppearance();
}
