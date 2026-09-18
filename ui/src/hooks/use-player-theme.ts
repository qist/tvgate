/**
 * 播放器明暗主题 hook。
 *
 * 为什么直接改 <html> 而不是包一层主题容器：全站样式（Tailwind dark: 前缀、
 * 原生滚动条、表单控件的配色）都以根元素上的 dark 类和 color-scheme 为开关，
 * 挂在更深的容器上覆盖不到这些。auto 档表示"跟随系统"，用户没有明确表态时不替用户做主。
 */
import { useEffect, useMemo } from "react";
import { THEME_MODES, type ThemeMode } from "../types/ui";
import { usePersistedEnum } from "./use-persisted-enum";

const FALLBACK_THEME: ThemeMode = "auto";
const PREFERS_DARK_QUERY = "(prefers-color-scheme: dark)";

/** 读一次系统偏好。SSR 或无 matchMedia 的环境按"浅色"处理，属安全默认。 */
function readSystemDarkPreference(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia(PREFERS_DARK_QUERY).matches
  );
}

/** 把最终生效的主题写到根元素：类给样式选择器用，color-scheme 给浏览器原生部件用。 */
function syncDocumentTheme(mode: ThemeMode, systemPrefersDark: boolean): void {
  if (typeof document === "undefined") return;
  const resolvedDark = mode === "dark" || (mode === "auto" && systemPrefersDark);
  const root = document.documentElement;
  root.classList.toggle("dark", resolvedDark);
  root.style.colorScheme = resolvedDark ? "dark" : "light";
}

export function useTheme(storageKey: string) {
  const [theme, setTheme] = usePersistedEnum<ThemeMode>(storageKey, FALLBACK_THEME, THEME_MODES);

  // 档位一变立即重刷根元素；系统偏好读取一次即可，无需监听
  useEffect(() => {
    syncDocumentTheme(theme, readSystemDarkPreference());
  }, [theme]);

  // 只有 auto 档需要持续跟随系统切换。effect 依赖 theme：非 auto 档也要重建订阅，
  // 好让回调闭包拿到最新档位，避免"auto 时切走、系统再变时错误生效"的竞态。
  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    const media = window.matchMedia(PREFERS_DARK_QUERY);
    const handleSystemChange = (event: MediaQueryListEvent) => {
      if (theme === "auto") syncDocumentTheme("auto", event.matches);
    };
    media.addEventListener("change", handleSystemChange);
    return () => media.removeEventListener("change", handleSystemChange);
  }, [theme]);

  return useMemo(() => ({ theme, setTheme }), [theme, setTheme]);
}
