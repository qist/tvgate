/**
 * 播放器文案翻译 hook（clean-room 重写）。
 * 把当前 locale 绑定的翻译函数稳定暴露给组件（locale 变化时才重建）。
 */
import { useCallback } from "react";
import type { TranslationKey } from "../i18n/player";
import { translate } from "../i18n/player";
import type { Locale } from "../lib/locale";

export function usePlayerTranslation(locale: Locale) {
  return useCallback((key: TranslationKey) => translate(locale, key), [locale]);
}
