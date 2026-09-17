/**
 * 播放器明暗主题 hook。
 * 支持 auto/light/dark；auto 跟随系统 prefers-color-scheme。把 `dark` 类与 color-scheme 挂到 <html>。
 */
import { useEffect, useMemo } from "react";
import { THEME_MODES, type ThemeMode } from "../types/ui";
import { usePersistedEnum } from "./use-persisted-enum";

const DEFAULT_THEME: ThemeMode = "auto";

function prefersDark(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
  );
}

function applyTheme(mode: ThemeMode, systemDark: boolean): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  if (mode === "dark" || (mode === "auto" && systemDark)) {
    root.classList.add("dark");
    root.style.colorScheme = "dark";
  } else {
    root.classList.remove("dark");
    root.style.colorScheme = "light";
  }
}

export function useTheme(storageKey: string) {
  const [theme, setTheme] = usePersistedEnum<ThemeMode>(storageKey, DEFAULT_THEME, THEME_MODES);

  useEffect(() => {
    applyTheme(theme, prefersDark());
  }, [theme]);

  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const handleChange = (event: MediaQueryListEvent) => {
      if (theme === "auto") applyTheme("auto", event.matches);
    };
    media.addEventListener("change", handleChange);
    return () => media.removeEventListener("change", handleChange);
  }, [theme]);

  return useMemo(() => ({ theme, setTheme }), [theme, setTheme]);
}
