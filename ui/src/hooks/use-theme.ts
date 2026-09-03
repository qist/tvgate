import { useCallback, useEffect, useMemo, useState } from "react";

export type ThemeMode = "light" | "dark" | "auto";

const STORAGE_KEY = "tvgate.theme";

function getSystemDark(): boolean {
  return typeof window !== "undefined" && window.matchMedia?.("(prefers-color-scheme: dark)").matches === true;
}

function resolveDark(mode: ThemeMode, systemDark: boolean): boolean {
  return mode === "dark" || (mode === "auto" && systemDark);
}

function loadInitial(): ThemeMode {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw === "light" || raw === "dark" || raw === "auto") return raw;
  } catch {
    /* ignore */
  }
  return "auto";
}

/** 主题 hook：切换 <html class="dark">，记忆到 localStorage；不遍历 DOM。 */
export function useTheme() {
  const [mode, setMode] = useState<ThemeMode>(loadInitial);

  const apply = useCallback((m: ThemeMode, systemDark?: boolean) => {
    const dark = resolveDark(m, systemDark ?? getSystemDark());
    const root = document.documentElement;
    root.classList.toggle("dark", dark);
    root.style.colorScheme = dark ? "dark" : "light";
  }, []);

  useEffect(() => {
    apply(mode);
    localStorage.setItem(STORAGE_KEY, mode);
  }, [mode, apply]);

  // 跟随系统深浅切换（auto 模式）
  useEffect(() => {
    if (mode !== "auto") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => apply("auto", media.matches);
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [mode, apply]);

  return useMemo(() => ({ theme: mode, setTheme: setMode }), [mode]);
}