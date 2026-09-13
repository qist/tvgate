/*
 * 音频输出路径路由：决定 AC-3 / E-AC-3 走「原生 MSE 硬解」还是「WASM 软解 + WebAudio」。
 *
 * 为什么必须真实探测，而不能信 `MediaSource.isTypeSupported`：
 *   Chromium 在平台没有 Dolby 解码器时依然会对 `ec-3` / `ac-3` 返回 true（见
 *   `mse-playback-backend.ts` 里的历史注释）。若据此走原生，`addSourceBuffer` 甚至
 *   也可能照样通过，但播放永远没有声音 —— 用户看到的就是"视频继续无声播放"。
 *   历史上为了避免这个坑，代码选择了「只要配了 wasm 就一律软解」，代价是**有能力
 *   硬解的设备也白烧 CPU/电量**。
 *
 * 这里改用一段内置的 1 秒探测片段**真实解码一遍**，以"是否真的产出了已解码音频
 * 字节"作为唯一判据：
 *   L1  `isTypeSupported`        —— 只用于提前否掉明显不支持的平台（它会说谎）
 *   L2  内置片段真实解码 + 查字节数 —— 唯一可信信号
 *   L4  `?audioPath=sw|hw`       —— 逃生开关（见 resolveAudioPathOverride）
 *
 * 设计约束（fail-safe）：**任何异常、超时、拿不到可信信号，一律判定为"不支持原生"**，
 * 也就是回落到软解（= 现状）。因此本模块只可能"少优化"，不可能让设备失去声音。
 */

import Log from "../utils/logger";
import ac3ProbeUrl from "./assets/audio-probe-ac3.mp4?url";
import eac3ProbeUrl from "./assets/audio-probe-eac3.mp4?url";

const TAG = "AudioRouter";

/**
 * 本次会话音频到底走哪条路 —— 这是"硬解还是软解"的唯一权威结论。
 * 由 mse-playback-backend 在每个决策点调用 setAudioRoute 写入，面板/日志/屏显都读它。
 */
export interface AudioRouteInfo {
  /** native = 交平台原生解码（MSE）；software = WASM 软解 + WebAudio。 */
  path: "native" | "software";
  /** 结论来源，用来说明"为什么是它"（forced / probe / no software decoder…）。 */
  reason: string;
}

let currentRoute: AudioRouteInfo | null = null;

/** 屏显开关：`?showAudioRoute=1`。真机（电视）没有 console，只能靠屏显判断。 */
function isRouteOsdEnabled(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  const value = new URLSearchParams(window.location.search).get("showAudioRoute");
  return value === "1" || value === "true";
}

let routeOsd: HTMLElement | null = null;

function paintRouteOsd(info: AudioRouteInfo): void {
  if (!isRouteOsdEnabled() || typeof document === "undefined" || !document.body) {
    return;
  }
  if (!routeOsd) {
    routeOsd = document.createElement("div");
    routeOsd.style.cssText =
      "position:fixed;left:8px;top:8px;z-index:2147483647;pointer-events:none;padding:4px 8px;" +
      "border-radius:6px;background:rgba(0,0,0,.72);color:#fff;font:600 13px/1.4 monospace;white-space:pre";
    document.body.appendChild(routeOsd);
  }
  routeOsd.textContent = `${info.path === "native" ? "AUDIO: HW" : "AUDIO: SW"}\n${info.reason}`;
}

/**
 * 记录本次会话的音频路径结论。
 * 结论变化时打一条 INFO 日志（同一结论重复设置只更新状态），并刷新屏显。
 */
export function setAudioRoute(info: AudioRouteInfo): void {
  const previous = currentRoute;
  currentRoute = info;
  paintRouteOsd(info);
  if (previous?.path === info.path && previous.reason === info.reason) {
    return;
  }
  Log.i(TAG, `audio route: ${info.path === "native" ? "HARDWARE" : "SOFTWARE"} decode — ${info.reason}`);
}

/** 读取当前音频路径结论（尚未决定时为 null）。 */
export function getAudioRouteInfo(): AudioRouteInfo | null {
  return currentRoute;
}

/**
 * 探测片段播放窗口（毫秒）：这段时间内必须观测到已解码音频字节。
 * 实测（桌面 Chrome + AAC 片段）200ms 即出现首批解码字节，取 400ms 给慢速电视盒留余量。
 */
const PROBE_WATCH_MS = 400;
/** 单个 codec 的硬超时（毫秒）：超时一律判定为不支持。 */
const PROBE_TIMEOUT_MS = 3000;
/**
 * loadSegments 等待探测结果的上限（毫秒）：超过就本次先用软解，不阻塞起播。
 * 探测在 backend 创建时已并行预热，频道切换前的取流往返通常足够让它完成；
 * 万一没赶上，本会话走软解（= 历史行为），不影响任何设备发声。
 */
export const AUDIO_ROUTE_WAIT_MS = 400;

export type NativeAudioCodec = "ac3" | "eac3";

const PROBE_SOURCES: Record<NativeAudioCodec, { url: string; mime: string }> = {
  ac3: { url: ac3ProbeUrl, mime: 'audio/mp4; codecs="ac-3"' },
  eac3: { url: eac3ProbeUrl, mime: 'audio/mp4; codecs="ec-3"' },
};

/** 探测结论缓存：设备能力在一次会话内视为稳定。 */
const results = new Map<NativeAudioCodec, boolean>();

/**
 * Chromium 私有属性：媒体元素累计已解码的音频字节数。
 * 这是"平台确实在解码音频"的唯一直接证据；非 Chromium 返回 null。
 */
function decodedAudioBytes(element: HTMLVideoElement): number | null {
  const value = (element as unknown as { webkitAudioDecodedByteCount?: number }).webkitAudioDecodedByteCount;
  return typeof value === "number" ? value : null;
}

function mediaSourceCtor(): typeof MediaSource | undefined {
  const scope = globalThis as unknown as {
    ManagedMediaSource?: typeof MediaSource;
    MediaSource?: typeof MediaSource;
  };
  return scope.ManagedMediaSource ?? scope.MediaSource;
}

/** 探测结果 + 人类可读的判据（判据会进日志，真机排查时需要知道"为什么"。） */
interface ProbeOutcome {
  ok: boolean;
  detail: string;
}

/** 真实解码一段内置片段，返回平台是否确实解出了音频。 */
async function probeCodec(codec: NativeAudioCodec): Promise<ProbeOutcome> {
  const source = PROBE_SOURCES[codec];
  const Ctor = mediaSourceCtor();
  if (!Ctor || typeof document === "undefined" || !document.body) {
    return { ok: false, detail: "no MediaSource / document unavailable" };
  }
  // L1：快速排除（会说谎，只用于提前否掉明显不支持的平台）
  try {
    if (!Ctor.isTypeSupported(source.mime)) {
      return { ok: false, detail: "isTypeSupported=false" };
    }
  } catch (error) {
    return { ok: false, detail: `isTypeSupported threw: ${String(error)}` };
  }

  const element = document.createElement("video");
  // 必须"在文档里且参与渲染"：display:none 会让媒体流水线不推进解码。
  element.muted = true;
  element.playsInline = true;
  element.setAttribute("muted", "");
  element.style.cssText = "position:fixed;left:-10000px;top:0;width:1px;height:1px;opacity:0;pointer-events:none";
  document.body.appendChild(element);

  const mediaSource = new Ctor();
  let objectUrl = "";

  try {
    // 必须**先挂 sourceopen 监听、再设 src**：事件可能在随后的 await 期间就已触发，
    // 之后再 addEventListener 会永远等不到（实测表现为 sourceopen timeout →
    // 真机上支持硬解的设备被误判为不支持，功能静默失效）。
    const opened = new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("sourceopen timeout")), PROBE_TIMEOUT_MS);
      const done = () => {
        clearTimeout(timer);
        resolve();
      };
      if (mediaSource.readyState === "open") {
        done();
        return;
      }
      mediaSource.addEventListener("sourceopen", done, { once: true });
    });

    const dataPromise = fetch(source.url, { cache: "force-cache" }).then((response) => {
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      return response.arrayBuffer();
    });

    objectUrl = URL.createObjectURL(mediaSource);
    element.src = objectUrl;

    await opened;
    const data = await dataPromise;

    // 真实的 addSourceBuffer：平台不接受这个 codec 时会在这里抛出。
    const sourceBuffer = mediaSource.addSourceBuffer(source.mime);

    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("append timeout")), PROBE_TIMEOUT_MS);
      sourceBuffer.addEventListener(
        "error",
        () => {
          clearTimeout(timer);
          reject(new Error("SourceBuffer error"));
        },
        { once: true },
      );
      sourceBuffer.addEventListener(
        "updateend",
        () => {
          clearTimeout(timer);
          try {
            mediaSource.endOfStream();
          } catch {
            /* 已经结束 */
          }
          resolve();
        },
        { once: true },
      );
      sourceBuffer.appendBuffer(data);
    });

    await element.play().catch(() => undefined);
    await new Promise((resolve) => setTimeout(resolve, PROBE_WATCH_MS));

    const bytes = decodedAudioBytes(element);
    if (bytes === null) {
      // 拿不到可信信号（非 Chromium）：fail-safe 回落软解。
      return { ok: false, detail: "isTypeSupported=true decodedBytes=unavailable (non-Chromium)" };
    }
    return { ok: bytes > 0, detail: `isTypeSupported=true decodedBytes=${bytes}` };
  } catch (error) {
    return { ok: false, detail: `probe threw: ${String(error)}` };
  } finally {
    try {
      element.pause();
    } catch {
      /* ignore */
    }
    element.removeAttribute("src");
    element.remove();
    if (objectUrl) {
      URL.revokeObjectURL(objectUrl);
    }
    if (mediaSource.readyState === "open") {
      try {
        mediaSource.endOfStream();
      } catch {
        /* ignore */
      }
    }
  }
}

/** 探测单个 codec（带缓存）。任何异常/超时都返回 false。 */
export function detectNativeAudio(codec: NativeAudioCodec): Promise<boolean> {
  const cached = results.get(codec);
  if (cached !== undefined) {
    return Promise.resolve(cached);
  }
  return probeCodec(codec).then(({ ok, detail }) => {
    results.set(codec, ok);
    Log.i(TAG, `${codec} native decode: ${ok ? "available" : "unavailable (software decode)"} [${detail}]`);
    return ok;
  });
}

let warmup: Promise<void> | null = null;

/** 预热：并行探测两个 codec，供 loadSegments 同步取用。可重复调用。 */
export function warmupNativeAudioProbe(): Promise<void> {
  if (!warmup) {
    warmup = Promise.all([detectNativeAudio("ac3"), detectNativeAudio("eac3")]).then(() => undefined);
  }
  return warmup;
}

/**
 * 等待探测结果，但最多等 `AUDIO_ROUTE_WAIT_MS`：用于不阻塞起播。
 * 返回 `true` 表示探测已完成（结论可取），`false` 表示超时（本次先用软解）。
 */
export async function waitForNativeAudioProbe(maxWaitMs = AUDIO_ROUTE_WAIT_MS): Promise<boolean> {
  const started = warmupNativeAudioProbe();
  const timeout = Symbol("probe-timeout");
  const result = await Promise.race([
    started.then(() => true as const),
    new Promise<typeof timeout>((resolve) => setTimeout(() => resolve(timeout), maxWaitMs)),
  ]);
  return result === true;
}

/**
 * 同步读取结论：**两个 codec 都必须确认原生可用**才返回 true。
 *
 * 为什么要求两个都过：worker 侧只有一个 `wasmDecoders.ac3` 开关同时控制 AC-3 与
 * E-AC-3 的软解（`ts-demuxer.ts` 的两处软解判定共用它）。源流可能是其中任意一种，
 * 只过一半而放开开关，会让另一种 codec 的频道彻底没声音。宁可不优化。
 */
export function isNativeAudioAvailable(): boolean {
  return results.get("ac3") === true && results.get("eac3") === true;
}

/** 测试/调试用：清空缓存。 */
export function resetAudioRouteCache(): void {
  results.clear();
  warmup = null;
}

/**
 * `?audioPath=` 逃生开关。
 *   `sw` → 强制软解（= 历史行为，用于 A/B 对比与故障回退）
 *   `hw` → 强制原生（跳过探测；若平台其实不能解码会没声音，由操作者负责）
 * 未指定返回 null（= 走自动探测）。
 * 兼容旧参数 `?preferNativeAc3=1`（等价于 hw）。
 */
export function resolveAudioPathOverride(): "sw" | "hw" | null {
  if (typeof window === "undefined") {
    return null;
  }
  const params = new URLSearchParams(window.location.search);
  const value = params.get("audioPath");
  if (value === "sw" || value === "hw") {
    return value;
  }
  if (params.get("preferNativeAc3") === "1") {
    return "hw";
  }
  return null;
}
