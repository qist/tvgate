/**
 * 后端事件派发器。
 * 类型安全的极简 emitter：单个监听器抛错不中断派发。
 */

import type { PlayerEventMap } from "./types";

type AnyHandler = (...args: never[]) => void;

export class BackendEventEmitter {
  private readonly handlers = new Map<keyof PlayerEventMap, Set<AnyHandler>>();

  on<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void {
    let set = this.handlers.get(event);
    if (!set) {
      set = new Set();
      this.handlers.set(event, set);
    }
    set.add(handler as AnyHandler);
  }

  off<K extends keyof PlayerEventMap>(event: K, handler: PlayerEventMap[K]): void {
    this.handlers.get(event)?.delete(handler as AnyHandler);
  }

  emit<K extends keyof PlayerEventMap>(event: K, ...args: Parameters<PlayerEventMap[K]>): void {
    const set = this.handlers.get(event);
    if (!set) return;
    for (const h of [...set]) {
      try {
        (h as (...a: Parameters<PlayerEventMap[K]>) => void)(...args);
      } catch {
        /* 单个监听器异常不应中断派发 */
      }
    }
  }

  clear(): void {
    this.handlers.clear();
  }
}
