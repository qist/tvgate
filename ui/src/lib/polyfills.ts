/**
 * 旧 WebView 兼容 polyfill（安卓 8 海信电视等 Chromium 57+ 基线缺口）。
 * 必须在业务代码之前 import：main.tsx / player.tsx 顶部第一行引入。
 * 只补齐缺失的 API，已存在的不覆盖（避免破坏原生行为）。
 */

function defineMethod<T>(target: unknown, name: PropertyKey, impl: T): void {
  const proto = target as Record<PropertyKey, unknown>;
  if (proto && typeof proto === "object" && !(name in proto)) {
    Object.defineProperty(proto, name, {
      value: impl,
      writable: true,
      configurable: true,
      enumerable: false,
    });
  }
}

/* --- globalThis（Chromium 71-） --- */
if (typeof globalThis === "undefined") {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (globalThis as any) = typeof self !== "undefined" ? self : typeof window !== "undefined" ? window : {};
}

/* --- AbortController（Chromium 66-，fetch 拉流依赖） --- */
if (typeof (AbortController as unknown) === "undefined") {
  const AC = class {
    signal: { aborted: boolean; addEventListener(): void; removeEventListener(): void } = {
      aborted: false,
      addEventListener() { },
      removeEventListener() { },
    };
    abort(): void {
      this.signal.aborted = true;
    }
  };
  (globalThis as unknown as Record<string, unknown>).AbortController = AC;
}

/* --- Object.fromEntries（Chromium 73-） --- */
if (typeof (Object as { fromEntries?: unknown }).fromEntries === "undefined") {
  Object.defineProperty(Object, "fromEntries", {
    value: function fromEntries<K extends PropertyKey, V>(entries: Iterable<readonly [K, V]>): Record<K, V> {
      const out = {} as Record<K, V>;
      for (const [k, v] of entries) out[k] = v;
      return out;
    },
    writable: true,
    configurable: true,
  });
}

/* --- Object.hasOwn（Chromium 93-） --- */
if (typeof (Object as { hasOwn?: unknown }).hasOwn === "undefined") {
  Object.defineProperty(Object, "hasOwn", {
    value: function hasOwn(target: object, prop: PropertyKey): boolean {
      return Object.prototype.hasOwnProperty.call(target, prop);
    },
    writable: true,
    configurable: true,
  });
}

/* --- Array.prototype.at（Chromium 92-） --- */
if (typeof (Array.prototype as { at?: unknown }).at === "undefined") {
  defineMethod(Array.prototype, "at", function at(this: unknown[], index: number): unknown {
    const arr = this;
    const n = index < 0 ? arr.length + index : index;
    return n >= 0 && n < arr.length ? arr[n] : undefined;
  });
}

/* --- Array.prototype.findLast / findLastIndex（Chromium 97-） --- */
if (typeof (Array.prototype as { findLast?: unknown }).findLast === "undefined") {
  defineMethod(Array.prototype, "findLast", function findLast<T>(
    this: T[],
    predicate: (value: T, index: number, obj: T[]) => boolean,
    thisArg?: unknown,
  ): T | undefined {
    for (let i = this.length - 1; i >= 0; i--) {
      if (predicate.call(thisArg, this[i], i, this)) return this[i];
    }
    return undefined;
  });
}
if (typeof (Array.prototype as { findLastIndex?: unknown }).findLastIndex === "undefined") {
  defineMethod(Array.prototype, "findLastIndex", function findLastIndex<T>(
    this: T[],
    predicate: (value: T, index: number, obj: T[]) => boolean,
    thisArg?: unknown,
  ): number {
    for (let i = this.length - 1; i >= 0; i--) {
      if (predicate.call(thisArg, this[i], i, this)) return i;
    }
    return -1;
  });
}

/* --- Array.prototype.flat / flatMap（Chromium 69-） --- */
if (typeof (Array.prototype as { flat?: unknown }).flat === "undefined") {
  defineMethod(Array.prototype, "flat", function flat(this: unknown[], depth = 1): unknown[] {
    const out: unknown[] = [];
    const walk = (arr: unknown[], d: number): void => {
      for (const item of arr) {
        if (Array.isArray(item) && d > 0) walk(item, d - 1);
        else out.push(item);
      }
    };
    walk(this, Math.max(0, depth));
    return out;
  });
}
if (typeof (Array.prototype as { flatMap?: unknown }).flatMap === "undefined") {
  defineMethod(Array.prototype, "flatMap", function flatMap<T, U>(
    this: T[],
    mapper: (value: T, index: number, obj: T[]) => U | readonly U[],
    thisArg?: unknown,
  ): U[] {
    const out: U[] = [];
    for (let i = 0; i < this.length; i++) {
      const mapped = mapper.call(thisArg, this[i], i, this);
      if (Array.isArray(mapped)) {
        for (const item of mapped) out.push(item as U);
      } else {
        out.push(mapped as U);
      }
    }
    return out;
  });
}

/* --- String.prototype.replaceAll（Chromium 85-） --- */
if (typeof (String.prototype as { replaceAll?: unknown }).replaceAll === "undefined") {
  defineMethod(String.prototype, "replaceAll", function replaceAll(
    this: string,
    search: string | RegExp,
    replace: string | ((substring: string, ...args: unknown[]) => string),
  ): string {
    if (search instanceof RegExp) {
      if (!search.global) {
        throw new TypeError("replaceAll: non-global RegExp");
      }
      return this.replace(search, replace as string);
    }
    return this.split(search).join(replace as string);
  });
}

/* --- String.prototype.trimStart / trimEnd（Chromium 66-） --- */
if (typeof (String.prototype as { trimStart?: unknown }).trimStart === "undefined") {
  defineMethod(String.prototype, "trimStart", function trimStart(this: string): string {
    return this.replace(/^\s+/, "");
  });
  defineMethod(String.prototype, "trimEnd", function trimEnd(this: string): string {
    return this.replace(/\s+$/, "");
  });
}

/* --- ResizeObserver（Chromium 64-）：no-op 兜底，避免渲染器构造崩溃 --- */
if (typeof (ResizeObserver as unknown) === "undefined") {
  class RO {
    constructor(_callback: ResizeObserverCallback) { }
    observe(): void { }
    unobserve(): void { }
    disconnect(): void { }
  }
  (globalThis as unknown as Record<string, unknown>).ResizeObserver = RO;
}
