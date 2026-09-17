export const SUPPORTED_LOCALES = ["en", "zh-Hans", "zh-Hant"] as const;
export type Locale = (typeof SUPPORTED_LOCALES)[number];
