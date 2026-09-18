import { type Dispatch, type SetStateAction, useEffect, useState } from "react";

/**
 * localStorage 持久化的枚举状态。
 * 存储值不在 allowed 白名单内时回落到 fallback；fallback 本身不写入存储
 * （保持存储"未设置"语义，改默认值时旧数据自然失效）。
 */
export function usePersistedEnum<T extends string>(
  storageKey: string,
  fallback: T,
  allowed: readonly T[],
): [T, Dispatch<SetStateAction<T>>] {
  const [value, setValue] = useState<T>(() => {
    if (typeof window === "undefined") return fallback;
    const raw = window.localStorage.getItem(storageKey);
    return raw !== null && (allowed as readonly string[]).indexOf(raw) >= 0 ? (raw as T) : fallback;
  });

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (value === fallback) {
      window.localStorage.removeItem(storageKey);
      return;
    }
    if (window.localStorage.getItem(storageKey) !== value) {
      window.localStorage.setItem(storageKey, value);
    }
  }, [value, storageKey, fallback]);

  return [value, setValue];
}
