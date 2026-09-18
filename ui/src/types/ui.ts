/**
 * 播放器与后台共用的枚举契约表。
 *
 * 这里的每个字符串字面量都会"出网"，属于跨端契约：
 *  - 一部分作为 localStorage 持久化值（界面风格、面板透明度档位）；
 *  - 一部分作为 i18n 文案键（各 *LABEL_KEYS），对应 `i18n/player.ts` 里的条目；
 *  - 一部分以类名后缀形式参与样式拼接（如 `player-theme-<name>`、`panel-alpha-<value>`）。
 * 因此任何取值调整都必须同步评审持久化读取方、i18n 字典与样式表，
 * 否则会报废老用户的本地设置或丢失文案。
 */

/** 深浅主题三态：auto 跟随系统偏好，light/dark 为强制指定。 */
export const THEME_MODES = ["auto", "light", "dark"] as const;
export type ThemeMode = (typeof THEME_MODES)[number];

export const THEME_LABEL_KEYS = {
  auto: "themeAuto",
  light: "themeLight",
  dark: "themeDark",
} as const satisfies Record<ThemeMode, string>;

/**
 * 播放器界面风格（换肤）候选：本项目自有的 6 套配色。
 *
 * 机制是把 `player-theme-<name>` 类挂到 `<html>`，由样式表重定义色阶变量实现整站换色；
 * 播放页与后台共用同一存储键。早期从上游继承的 fancy / simple 两套风格已整体移除，
 * 首帧引导脚本（index.html / player.html）与 lib/appearance.ts 的候选表必须与此处保持一致。
 */
export const PLAYER_APPEARANCES = [
  "ocean",
  "emerald",
  "sunset",
  "rose",
  "amber",
  "slate",
] as const;
export type PlayerAppearance = (typeof PLAYER_APPEARANCES)[number];

export const PLAYER_APPEARANCE_LABEL_KEYS = {
  ocean: "appearanceOcean",
  emerald: "appearanceEmerald",
  sunset: "appearanceSunset",
  rose: "appearanceRose",
  amber: "appearanceAmber",
  slate: "appearanceSlate",
} as const satisfies Record<PlayerAppearance, string>;

/**
 * 节目单 / 侧栏面板的不透明度档位。
 *
 * 取值是"百分比不透明度"字符串：100 代表维持默认的完整不透明（此时不挂任何
 * `panel-alpha-*` 类），40 为最通透一档。用字符串而非数字，是因为这些值最终
 * 直接作为类名后缀与 localStorage 值使用，保持原样可免去运行期转换。
 */
export const PLAYER_PANEL_ALPHAS = ["100", "85", "70", "55", "40"] as const;
export type PlayerPanelAlpha = (typeof PLAYER_PANEL_ALPHAS)[number];

export const PLAYER_PANEL_ALPHA_LABEL_KEYS = {
  "100": "panelAlpha100",
  "85": "panelAlpha85",
  "70": "panelAlpha70",
  "55": "panelAlpha55",
  "40": "panelAlpha40",
} as const satisfies Record<PlayerPanelAlpha, string>;

/**
 * 画中画的两种形态：
 *  - document：Document Picture-in-Picture，可保留完整控制界面；
 *  - video：浏览器原生视频画中画，仅有极简画面。
 */
export const PICTURE_IN_PICTURE_MODES = ["document", "video"] as const;
export type PictureInPictureMode = (typeof PICTURE_IN_PICTURE_MODES)[number];

export const PICTURE_IN_PICTURE_MODE_LABEL_KEYS = {
  document: "pictureInPictureModeFull",
  video: "pictureInPictureModeSimple",
} as const satisfies Record<PictureInPictureMode, string>;
