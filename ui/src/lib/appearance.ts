/**
 * 界面风格（6 套配色：ocean/emerald/sunset/rose/amber/slate）的公共存取逻辑。
 *
 * 换肤机制：把 `player-theme-<name>` 类挂到 `<html>`，由 CSS（`styles/index.css`）重定义
 * `--pg-*` 与 Tailwind 的 `--color-violet-*` 色阶变量，从而整站换色——**播放页与后台共用同一份
 * 样式表与同一个存储键**，因此后台选完风格，播放页（同一浏览器）也是同一套配色。
 *
 * 两侧入口都必须在首帧前应用一次（`player.html` / `index.html` 的早期引导脚本），避免闪色。
 */
import { PLAYER_APPEARANCES, type PlayerAppearance } from "@/types/ui";

/** 界面风格存储键（后台与播放页共用）。 */
export const APPEARANCE_STORAGE_KEY = "tvgate-player-appearance";

/** 默认风格 = 翡翠（必须与 `ui/player.html` 引导脚本、播放页 hook 保持一致）。 */
export const DEFAULT_APPEARANCE: PlayerAppearance = "emerald";

/** 中文标签（后台无 i18n，直接用中文；播放页用自己的三语 i18n）。 */
export const APPEARANCE_LABELS: Record<PlayerAppearance, string> = {
  ocean: "深海",
  emerald: "翡翠",
  sunset: "落日",
  rose: "玫红",
  amber: "琥珀",
  slate: "石墨",
};

/** 读取已保存的风格；非法/不可用时回落到默认值。 */
export function readStoredAppearance(): PlayerAppearance {
  try {
    const raw = localStorage.getItem(APPEARANCE_STORAGE_KEY);
    if (raw && (PLAYER_APPEARANCES as readonly string[]).includes(raw)) {
      return raw as PlayerAppearance;
    }
  } catch {
    /* localStorage 不可用（隐私模式等）→ 用默认值 */
  }
  return DEFAULT_APPEARANCE;
}

/** 把 `player-theme-<name>` 挂到 <html>（同时只保留一个风格类）。 */
export function applyAppearance(appearance: PlayerAppearance): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  for (const name of PLAYER_APPEARANCES) {
    root.classList.toggle(`player-theme-${name}`, name === appearance);
  }
}

/** 保存风格（失败不影响当前会话内的显示）。 */
export function saveAppearance(appearance: PlayerAppearance): void {
  try {
    localStorage.setItem(APPEARANCE_STORAGE_KEY, appearance);
  } catch {
    /* ignore */
  }
}

/** 当前风格对应的主色（用于色点预览；CSS 变量随风格变化）。 */
export const APPEARANCE_ACCENT_CSS = "rgb(var(--pg-rgb))";
