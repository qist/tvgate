/**
 * 播放器专用类名登记表。
 *
 * 为什么集中在此：播放器各浮层的视觉由"自定义 CSS 类（.player-performance-*，实现见
 * styles/index.css）+ Tailwind 原子类"叠加而成。组件只引用这里的常量、不自行拼串，
 * 才能保证同一表面在视频层、手势层、频道/节目单列表等多个入口长得一致；
 * "面板透明度"等全局档位也只需调 CSS 变量，不用逐个组件改样式。
 *
 * 注意：以下字符串会被原样拼进 className，属于行为契约，逐字不可变。
 * 分组：浮层容器与交互控件 → 浮层内列表行 → 选中行装饰层。
 */

/* ---------------- 浮层容器与交互控件 ---------------- */

/**
 * 玻璃浮层底盘：模糊、渐变、描边与多层内/外投影全部由自定义 CSS 承担，
 * 组件侧只负责把这个类挂上，透明度档位在 CSS 层切换即可。
 */
export const PLAYER_OVERLAY_SURFACE_CLASS =
  "player-performance-overlay-background player-performance-effect player-performance-gradient isolate overflow-hidden border border-violet-200/55 bg-slate-900/30 shadow-[0_18px_36px_-18px_rgba(0,0,0,0.82),0_0_28px_-12px_rgba(var(--pg-rgb),0.62),inset_0_1px_0_rgba(255,255,255,0.17),inset_0_-1px_0_rgba(var(--pg-rgb),0.14)] backdrop-blur-md backdrop-saturate-150";

/** 控件与列表行共用的两个标记类：一个启用视觉特效，一个接入统一动效开关。 */
const SURFACE_EFFECT_PREFIX = "player-performance-effect player-performance-motion";

/**
 * 圆形图标按钮：基础态透明描边，悬停时泛起淡紫光晕、按下轻微缩放；
 * transition-none 只对"减少动效"用户关闭，避免悬停反馈跳变。
 */
export const PLAYER_CONTROL_BUTTON_CLASS =
  `${SURFACE_EFFECT_PREFIX} rounded-full border border-transparent text-white transition-[color,background-color,border-color,box-shadow,transform] duration-200 motion-reduce:transition-none hover:border-violet-100/20 hover:bg-violet-300/15 hover:text-violet-50 hover:shadow-[0_0_24px_rgba(var(--pg-rgb),0.16)] motion-safe:active:scale-95`;

/* ---------------- 浮层内列表行 ---------------- */

/**
 * 列表行骨架：行自身不带任何底色/边框/圆角/投影，外观完全交给
 * .player-performance-list-surface-* 按默认/选中/悬停三态给出（见下方三个挂钩）。
 * 这样长列表只需换一个修饰类就能整体换肤；仅限浮层内列表使用，
 * 其他布局对行高的假设不同，不要复用。
 */
export const PLAYER_LIST_SURFACE_BASE_CLASS =
  `${SURFACE_EFFECT_PREFIX} relative isolate w-full text-left transition-[color,background-color] duration-150 ease-out motion-reduce:transition-none`;

/**
 * 长列表渲染优化：跳过视口外行的绘制，并给出行高的块尺寸预估，
 * 预估值错了滚动条会跳动，所以按行型（频道行 / 节目单行）分别标定。
 */
export const PLAYER_CHANNEL_LIST_ITEM_CLASS =
  "[content-visibility:auto] [contain-intrinsic-block-size:auto_2.25rem] md:[contain-intrinsic-block-size:auto_2.5rem]";

/**
 * 频道卡片（移动端两列网格）的块尺寸预估：卡片比行高得多（台标在上 + 两行文字），
 * 沿用行高预估会让长列表的滚动条乱跳，所以另立一份。
 */
export const PLAYER_CHANNEL_CARD_ITEM_CLASS =
  "[content-visibility:auto] [contain-intrinsic-block-size:auto_7rem]";

/**
 * 卡片三态挂钩：与列表行同一套配色语义（当前项 = 主色淡填充 + 左侧强调条，
 * 悬停 = 极淡 wash），但卡片是独立块，允许圆角且不画行分隔线。
 * 真正外观同样在 CSS 里（.player-performance-channel-card-*），风格只由 --pg-* 驱动。
 */
export const PLAYER_CHANNEL_CARD_SELECTED_CLASS = "player-performance-channel-card-selected";

export const PLAYER_CHANNEL_CARD_DEFAULT_CLASS = "player-performance-channel-card-default";

export const PLAYER_CHANNEL_CARD_HOVER_CLASS = "player-performance-channel-card-hover";

export const PLAYER_EPG_LIST_ITEM_CLASS =
  "[content-visibility:auto] [contain-intrinsic-block-size:auto_3rem] md:[contain-intrinsic-block-size:auto_3.75rem]";

/**
 * 列表行三个状态挂钩：真正的外观在 CSS 里，这里单独导出是为了让调用方
 * 能按业务状态（当前播放 / 可悬停）自由组合，而不是把状态逻辑沉进 CSS。
 */
export const PLAYER_LIST_SURFACE_SELECTED_CLASS = "player-performance-list-surface-selected";

export const PLAYER_LIST_SURFACE_DEFAULT_CLASS = "player-performance-list-surface-default";

export const PLAYER_LIST_SURFACE_HOVER_CLASS = "player-performance-list-surface-hover";

/* ---------------- 选中行装饰层 ---------------- */

/**
 * 选中行装饰层公共段：所有层都是纯装饰（pointer-events-none，绝不挡交互），
 * 透明度从 0 出发由调用方点亮，淡入淡出统一 300ms 并尊重动效偏好。
 */
const DECORATION_PREFIX = "player-performance-decoration player-performance-motion pointer-events-none";
const DECORATION_FADE_SUFFIX = "transition-opacity duration-300 ease-out motion-reduce:transition-none";

/** 整行铺满的斜向渐变光层，位于内容之下（z-0）。 */
export const PLAYER_SELECTED_GLASS_LAYER_CLASS =
  `${DECORATION_PREFIX} absolute inset-0 z-0 bg-[linear-gradient(135deg,rgba(var(--pg-rgb-light),0.18)_0%,rgba(var(--pg-rgb),0.13)_42%,rgba(var(--pg-rgb-2),0.2)_100%)] opacity-0 ${DECORATION_FADE_SUFFIX}`;

/** 常规密度下的顶部高光线：一条 1px 的渐变亮线，压在内容之上（z-20）。 */
export const PLAYER_SELECTED_TOP_HIGHLIGHT_CLASS =
  `${DECORATION_PREFIX} absolute inset-x-3 top-0 z-20 h-px rounded-full bg-[linear-gradient(90deg,transparent_0%,rgba(var(--pg-rgb-light),0.42)_14%,rgba(var(--pg-rgb-lighter),0.96)_48%,rgba(var(--pg-rgb-light),0.68)_78%,transparent_100%)] opacity-0 shadow-[0_0_8px_rgba(var(--pg-rgb-light),0.46)] ${DECORATION_FADE_SUFFIX}`;

/** 紧凑密度下的顶部高光：改为短内边距的径向光晕，避免细线在低行高下不可见。 */
export const PLAYER_SELECTED_COMPACT_TOP_HIGHLIGHT_CLASS =
  `${DECORATION_PREFIX} absolute inset-x-1 top-px z-20 h-5 rounded-t-xl bg-[radial-gradient(ellipse_at_top,rgba(var(--pg-rgb-lighter),0.3)_0%,rgba(var(--pg-rgb-light),0.11)_42%,transparent_76%)] opacity-0 ${DECORATION_FADE_SUFFIX}`;
