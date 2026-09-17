/**
 * 播放器面板透明度 hook。
 * 持久化透明度档位，把 `panel-alpha-<value>` 类挂到 <html>，由 CSS 变量驱动亚克力通透度。
 */
import { useEffect, useMemo } from "react";
import { PLAYER_PANEL_ALPHAS, type PlayerPanelAlpha } from "../types/ui";
import { usePersistedEnum } from "./use-persisted-enum";

const STORAGE_KEY = "tvgate-player-panel-alpha";

export function usePlayerPanelAlpha() {
  const [panelAlpha, setPanelAlpha] = usePersistedEnum<PlayerPanelAlpha>(
    STORAGE_KEY,
    "100",
    PLAYER_PANEL_ALPHAS,
  );

  useEffect(() => {
    const root = document.documentElement;
    const active: Record<string, boolean> = {
      "panel-alpha-85": panelAlpha === "85",
      "panel-alpha-70": panelAlpha === "70",
      "panel-alpha-55": panelAlpha === "55",
      "panel-alpha-40": panelAlpha === "40",
    };
    for (const cls of Object.keys(active)) {
      root.classList.toggle(cls, active[cls]);
    }
  }, [panelAlpha]);

  return useMemo(() => ({ panelAlpha, setPanelAlpha }), [panelAlpha, setPanelAlpha]);
}
