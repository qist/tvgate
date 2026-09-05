export const THEME_MODES = ["auto", "light", "dark"] as const;
export type ThemeMode = (typeof THEME_MODES)[number];

export const THEME_LABEL_KEYS = {
  auto: "themeAuto",
  light: "themeLight",
  dark: "themeDark",
} as const satisfies Record<ThemeMode, string>;

export const PLAYER_APPEARANCES = ["fancy", "simple", "ocean", "emerald", "sunset"] as const;
export type PlayerAppearance = (typeof PLAYER_APPEARANCES)[number];

export const PLAYER_APPEARANCE_LABEL_KEYS = {
  fancy: "appearanceFancy",
  simple: "appearanceSimple",
  ocean: "appearanceOcean",
  emerald: "appearanceEmerald",
  sunset: "appearanceSunset",
} as const satisfies Record<PlayerAppearance, string>;

/** 节目单/侧栏面板透明度档位（百分比不透明度，100 = 现状默认） */
export const PLAYER_PANEL_ALPHAS = ["100", "85", "70", "55"] as const;
export type PlayerPanelAlpha = (typeof PLAYER_PANEL_ALPHAS)[number];

export const PLAYER_PANEL_ALPHA_LABEL_KEYS = {
  "100": "panelAlpha100",
  "85": "panelAlpha85",
  "70": "panelAlpha70",
  "55": "panelAlpha55",
} as const satisfies Record<PlayerPanelAlpha, string>;

export const PICTURE_IN_PICTURE_MODES = ["document", "video"] as const;
export type PictureInPictureMode = (typeof PICTURE_IN_PICTURE_MODES)[number];

export const PICTURE_IN_PICTURE_MODE_LABEL_KEYS = {
  document: "pictureInPictureModeFull",
  video: "pictureInPictureModeSimple",
} as const satisfies Record<PictureInPictureMode, string>;

export type ConnectionState = "connected" | "disconnected" | "reconnecting";

export const BANDWIDTH_UNITS = ["bits", "bytes"] as const;
export type BandwidthUnit = (typeof BANDWIDTH_UNITS)[number];
