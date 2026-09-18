/**
 * 播放器取词 hook：把"当前 locale + key → 文案"封装成稳定引用的函数交给组件。
 *
 * 为什么必须缓存函数引用：返回的取词函数会被十几个子组件当作 prop 消费，
 * 若每次渲染都换新引用，会击穿下游 memo，造成播放器整棵组件树无谓重渲，
 * 这对正在解码播放视频的页面是不可接受的性能损耗。
 * 因此这里以 locale 作为唯一依赖：locale 不变则复用旧函数，locale 切换才重建。
 */
import { useCallback } from "react";
import type { Locale } from "../lib/locale";
import type { TranslationKey } from "../i18n/player";
import { translate } from "../i18n/player";

/** 按给定 locale 组装一个取词函数；抽出工厂是为了让"构造"与"缓存"两件事互不纠缠 */
function createTranslator(activeLocale: Locale) {
  return (key: TranslationKey) => translate(activeLocale, key);
}

export function usePlayerTranslation(locale: Locale) {
  // 每次渲染都会先造一个新 translator，但仅当 locale 变化时才被 useCallback 采用
  return useCallback(createTranslator(locale), [locale]);
}
