/**
 * 界面风格 hook（播放页与后台共用）。
 * 读写 localStorage 并把 `player-theme-<name>` 挂到 <html>，由 CSS 变量整站换色。
 */
import { useCallback, useEffect, useState } from "react";
import {
  applyAppearance,
  DEFAULT_APPEARANCE,
  readStoredAppearance,
  saveAppearance,
} from "../lib/appearance";
import type { PlayerAppearance } from "../types/ui";

export { DEFAULT_APPEARANCE };

export function useAppearance() {
  const [appearance, setAppearanceState] = useState<PlayerAppearance>(
    () => readStoredAppearance() ?? DEFAULT_APPEARANCE,
  );

  // 首帧后再兜一次：引导脚本已挂过类，这里保证 SSR/异常路径下也一致
  useEffect(() => {
    applyAppearance(appearance);
  }, [appearance]);

  // 多标签页同步：另一个标签改了风格，本页跟着换
  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key !== null && event.key !== "tvgate-player-appearance") return;
      const next = readStoredAppearance();
      applyAppearance(next);
      setAppearanceState(next);
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const setAppearance = useCallback((next: PlayerAppearance) => {
    applyAppearance(next);
    saveAppearance(next);
    setAppearanceState(next);
  }, []);

  return { appearance, setAppearance };
}
