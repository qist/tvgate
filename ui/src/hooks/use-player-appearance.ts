import { useEffect, useMemo } from "react";
import { PLAYER_APPEARANCES, type PlayerAppearance } from "../types/ui";
import { usePersistedEnum } from "./use-persisted-enum";

const STORAGE_KEY = "tvgate-player-appearance";

function getDefaultPlayerAppearance(): PlayerAppearance {
  if (typeof document === "undefined") return "fancy";
  return document.documentElement.dataset.performanceTier === "constrained" ? "simple" : "fancy";
}

export function usePlayerAppearance() {
  const [appearance, setAppearance] = usePersistedEnum<PlayerAppearance>(
    STORAGE_KEY,
    getDefaultPlayerAppearance(),
    PLAYER_APPEARANCES,
  );

  useEffect(() => {
    const root = document.documentElement;
    // simple 为扁平性能模式；ocean/emerald/sunset 为 fancy 基础上的整体配色风格
    root.classList.toggle("player-theme-simple", appearance === "simple");
    root.classList.toggle("player-theme-ocean", appearance === "ocean");
    root.classList.toggle("player-theme-emerald", appearance === "emerald");
    root.classList.toggle("player-theme-sunset", appearance === "sunset");
  }, [appearance]);

  return useMemo(() => ({ appearance, setAppearance }), [appearance, setAppearance]);
}
