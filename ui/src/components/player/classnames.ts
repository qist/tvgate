export const PLAYER_OVERLAY_SURFACE_CLASS =
  "player-performance-overlay-background player-performance-effect player-performance-gradient isolate overflow-hidden border border-violet-200/55 bg-slate-900/30 shadow-[0_18px_36px_-18px_rgba(0,0,0,0.82),0_0_28px_-12px_rgba(var(--pg-rgb),0.62),inset_0_1px_0_rgba(255,255,255,0.17),inset_0_-1px_0_rgba(var(--pg-rgb),0.14)] backdrop-blur-md backdrop-saturate-150";

export const PLAYER_CONTROL_BUTTON_CLASS =
  "player-performance-effect player-performance-motion rounded-full border border-transparent text-white transition-[color,background-color,border-color,box-shadow,transform] duration-200 motion-reduce:transition-none hover:border-violet-100/20 hover:bg-violet-300/15 hover:text-violet-50 hover:shadow-[0_0_24px_rgba(var(--pg-rgb),0.16)] motion-safe:active:scale-95";

/**
 * 侧边栏列表行（平面化，TV 播放器风格）：行本身**没有卡片底盘**——背景/边框/圆角/投影
 * 全部由 CSS（.player-performance-list-surface-*，见 styles/index.css）统一给出：
 * 默认透明 + 一条细分隔线；当前行 = 左侧强调条 + 极淡填充；悬停 = 极淡wash。
 * 这样"面板透明度"档位只需改 CSS 变量，不必在每个组件里重复。**只用于浮层内列表。**
 */
export const PLAYER_LIST_SURFACE_BASE_CLASS =
  "player-performance-effect player-performance-motion relative isolate w-full text-left transition-[color,background-color] duration-150 ease-out motion-reduce:transition-none";

export const PLAYER_CHANNEL_LIST_ITEM_CLASS =
  "[content-visibility:auto] [contain-intrinsic-block-size:auto_2.25rem] md:[contain-intrinsic-block-size:auto_2.5rem]";

export const PLAYER_EPG_LIST_ITEM_CLASS =
  "[content-visibility:auto] [contain-intrinsic-block-size:auto_3rem] md:[contain-intrinsic-block-size:auto_3.75rem]";

export const PLAYER_LIST_SURFACE_SELECTED_CLASS = "player-performance-list-surface-selected";

export const PLAYER_LIST_SURFACE_DEFAULT_CLASS = "player-performance-list-surface-default";

export const PLAYER_LIST_SURFACE_HOVER_CLASS = "player-performance-list-surface-hover";

export const PLAYER_SELECTED_GLASS_LAYER_CLASS =
  "player-performance-decoration player-performance-motion pointer-events-none absolute inset-0 z-0 bg-[linear-gradient(135deg,rgba(var(--pg-rgb-light),0.18)_0%,rgba(var(--pg-rgb),0.13)_42%,rgba(var(--pg-rgb-2),0.2)_100%)] opacity-0 transition-opacity duration-300 ease-out motion-reduce:transition-none";

export const PLAYER_SELECTED_TOP_HIGHLIGHT_CLASS =
  "player-performance-decoration player-performance-motion pointer-events-none absolute inset-x-3 top-0 z-20 h-px rounded-full bg-[linear-gradient(90deg,transparent_0%,rgba(var(--pg-rgb-light),0.42)_14%,rgba(var(--pg-rgb-lighter),0.96)_48%,rgba(var(--pg-rgb-light),0.68)_78%,transparent_100%)] opacity-0 shadow-[0_0_8px_rgba(var(--pg-rgb-light),0.46)] transition-opacity duration-300 ease-out motion-reduce:transition-none";

export const PLAYER_SELECTED_COMPACT_TOP_HIGHLIGHT_CLASS =
  "player-performance-decoration player-performance-motion pointer-events-none absolute inset-x-1 top-px z-20 h-5 rounded-t-xl bg-[radial-gradient(ellipse_at_top,rgba(var(--pg-rgb-lighter),0.3)_0%,rgba(var(--pg-rgb-light),0.11)_42%,transparent_76%)] opacity-0 transition-opacity duration-300 ease-out motion-reduce:transition-none";
