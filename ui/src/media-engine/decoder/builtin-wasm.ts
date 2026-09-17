/**
 * media-engine 内置软解 WASM 资产（构建产物，封装 + FFmpeg libavcodec）。
 * 一套模块覆盖 mp2/mp3/ac3/eac3/aac；由 Vite 以 ?url 作为独立资源打包（不内联）。
 * 允许配置覆盖（PlayerConfig.wasmDecoders），此处仅提供缺省，令 media-engine 自持可用。
 */

import builtinWasmUrl from "../wasm/avcodec_audio.wasm?url";
import type { WasmDecoderConfig } from "./types";

/** 缺省 wasmDecoders：五类 codec 指向同一统一模块。 */
export const builtinWasmDecoders: WasmDecoderConfig["wasmDecoders"] = {
  mp2: builtinWasmUrl,
  mp3: builtinWasmUrl,
  ac3: builtinWasmUrl,
  eac3: builtinWasmUrl,
  aac: builtinWasmUrl,
};
