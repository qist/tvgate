import { useEffect, useMemo } from "react";
import { PLAYER_PANEL_ALPHAS, type PlayerPanelAlpha } from "../types/ui";
import { usePersistedEnum } from "./use-persisted-enum";

const STORAGE_KEY = "tvgate-player-panel-alpha";

/** 节目单/侧栏面板透明度（Win11 亚克力质感，"100" 保持原默认）。 */
export function usePlayerPanelAlpha() {
  const [panelAlpha, setPanelAlpha] = usePersistedEnum<PlayerPanelAlpha>(STORAGE_KEY, "100", PLAYER_PANEL_ALPHAS);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.toggle("panel-alpha-85", panelAlpha === "85");
    root.classList.toggle("panel-alpha-70", panelAlpha === "70");
    root.classList.toggle("panel-alpha-55", panelAlpha === "55");
  }, [panelAlpha]);

  return useMemo(() => ({ panelAlpha, setPanelAlpha }), [panelAlpha, setPanelAlpha]);
}
