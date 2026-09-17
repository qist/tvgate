/**
 * WSOLA 时间拉伸器的 WASM 封装（本项目自研。
 *
 * 拉伸内核（`wsola_*` 导出）编译在统一软解 wasm（`wasm/avcodec_audio.wasm`）里，
 * 本文件在主线程**再实例化一次**同一个模块：拉伸必须紧邻 video 时钟运行，
 * 不能借用 worker 里的解码实例（跨线程调用会增加不可控的抖动）。
 *
 * 用途：软解 PCM（MP2/MP3/AC-3/E-AC-3/AAC）要跟随 `video.playbackRate` 做
 * live-sync 追赶，并吸收音视频两套时钟的微小漂移。WSOLA 保音高，因此
 * 10%~35% 的临时速度偏差在听感上是"节奏略快/略慢"，不会变调。
 *
 * C 侧契约（见 `wasm/wsola.c` 文件头）：`position` 是"已产出输出所消耗的输入帧
 * 位置"，调用方据此把输出精确映射回流时间——ratio 中途变化也不影响该映射。
 */
import { EngineError } from "../utils/exception";

/** 主线程拉伸器接口（`WasmTimeStretcher` 是唯一实现；纯 TS 回退见 time-stretch.ts）。 */
export interface TimeStretcher {
  readonly sampleRate: number;
  readonly channels: number;
  /** 已产出输出对应的输入位置（帧，可含小数）。 */
  readonly position: number;
  /** 设置目标速度比（>1 加快、<1 放慢；内核夹到 [0.5, 2]）。 */
  setRatio(ratio: number): void;
  /** 喂入交错 PCM，取回拉伸后的交错 PCM（可能为空，表示输入尚不足一个合成周期）。 */
  process(input: Float32Array): Float32Array;
  reset(): void;
  destroy(): void;
}

/** `wsola_*` 的 wasm 导出（由 wasm/wsola.c 编译进统一模块）。 */
interface WsolaExports {
  memory: WebAssembly.Memory;
  _initialize: () => void;
  malloc: (bytes: number) => number;
  free: (ptr: number) => void;
  wsola_create: (sampleRate: number, channels: number) => number;
  wsola_destroy: (ptr: number) => void;
  wsola_reset: (ptr: number) => void;
  wsola_set_ratio: (ptr: number, ratio: number) => void;
  wsola_position: (ptr: number) => number;
  wsola_process: (ptr: number, input: number, inFrames: number, output: number, outCapacity: number) => number;
}

const EMPTY_PCM = new Float32Array(0);

/** 每个 wasm 文件只实例化一次；并发 create() 共享同一个 Promise。 */
const wasmModules = new Map<string, Promise<WsolaExports>>();

function loadWsolaModule(wasmUrl: string): Promise<WsolaExports> {
  let pending = wasmModules.get(wasmUrl);
  if (!pending) {
    pending = instantiateWsolaModule(wasmUrl);
    wasmModules.set(wasmUrl, pending);
    // 加载失败不缓存失败态，允许下一次重试（网络抖动/临时 404）。
    pending.catch(() => wasmModules.delete(wasmUrl));
  }
  return pending;
}

async function instantiateWsolaModule(wasmUrl: string): Promise<WsolaExports> {
  const imports = {
    env: {
      // Emscripten 的 ALLOW_MEMORY_GROWTH 通知钩子；内存增长本身由 wasm 内部处理。
      emscripten_notify_memory_growth: () => {},
    },
    // 统一 wasm（FFmpeg libavcodec）链接了 wasi 打桩；解码/WSOLA 路径不会触发这些调用，
    // 这里统一回 ENOSYS(=52) 即可（clock_time_get 需返回 0 而非负数）。
    wasi_snapshot_preview1: new Proxy(
      {},
      {
        get: (_target, prop) => (prop === "clock_time_get" ? () => 0 : () => 52),
      },
    ),
  };

  let instance: WebAssembly.Instance;
  try {
    ({ instance } = await WebAssembly.instantiateStreaming(fetch(wasmUrl), imports));
  } catch {
    // 某些静态服务器不给 .wasm 正确的 Content-Type（application/wasm），
    // 此时 instantiateStreaming 直接拒绝；退化为先取字节再实例化。
    const response = await fetch(wasmUrl);
    if (!response.ok) {
      throw new EngineError("state", `WSOLA wasm 加载失败：HTTP ${response.status}`);
    }
    ({ instance } = await WebAssembly.instantiate(await response.arrayBuffer(), imports));
  }

  const exports = instance.exports as unknown as WsolaExports;
  // Emscripten 独立模式（无 JS 胶水）的模块初始化入口。
  exports._initialize();
  return exports;
}

export class WasmTimeStretcher implements TimeStretcher {
  private readonly wasm: WsolaExports;
  private readonly channels_: number;
  private readonly sampleRateValue: number;
  private handle: number;
  private destroyed = false;

  /** 输入侧暂存（malloc）：容量按需增长，避免每帧分配。 */
  private inputPtr = 0;
  private inputCapacityFrames = 0;
  /** 输出侧暂存（malloc）：容量 = 2×输入 + 余量（见 process()）。 */
  private outputPtr = 0;
  private outputCapacityFrames = 0;

  private constructor(wasm: WsolaExports, handle: number, sampleRate: number, channels: number) {
    this.wasm = wasm;
    this.handle = handle;
    this.sampleRateValue = sampleRate;
    this.channels_ = channels;
  }

  static async create(wasmUrl: string, sampleRate: number, channels: number): Promise<WasmTimeStretcher> {
    const wasm = await loadWsolaModule(wasmUrl);
    const handle = wasm.wsola_create(sampleRate, channels);
    if (!handle) {
      throw new EngineError("argument", `wsola_create 失败（sampleRate=${sampleRate}, channels=${channels}）`);
    }
    return new WasmTimeStretcher(wasm, handle, sampleRate, channels);
  }

  get sampleRate(): number {
    return this.sampleRateValue;
  }

  get channels(): number {
    return this.channels_;
  }

  get position(): number {
    return this.destroyed ? 0 : this.wasm.wsola_position(this.handle);
  }

  setRatio(ratio: number): void {
    if (!this.destroyed) {
      this.wasm.wsola_set_ratio(this.handle, ratio);
    }
  }

  process(input: Float32Array): Float32Array {
    if (this.destroyed) {
      return EMPTY_PCM;
    }
    const channels = this.channels_;
    const inputFrames = Math.floor(input.length / channels);
    if (inputFrames <= 0) {
      return EMPTY_PCM;
    }

    this.reserveInput(inputFrames);
    // 视图必须在 malloc 之后创建：内存增长会让旧视图失效。
    new Float32Array(this.wasm.memory.buffer, this.inputPtr, inputFrames * channels).set(
      input.subarray(0, inputFrames * channels),
    );

    // 输出上界：ratio 最小 0.5（时长最多 ×2），再加一个合成周期的内部滞留量。
    this.reserveOutput(inputFrames * 2 + 8192);

    const outputFrames = this.wasm.wsola_process(
      this.handle,
      this.inputPtr,
      inputFrames,
      this.outputPtr,
      this.outputCapacityFrames,
    );
    if (outputFrames <= 0) {
      return EMPTY_PCM;
    }

    // 从 wasm 线性内存拷出（返回的数组会长期持有，必须与 wasm 内存解耦）。
    const view = new Float32Array(this.wasm.memory.buffer, this.outputPtr, outputFrames * channels);
    return view.slice();
  }

  reset(): void {
    if (!this.destroyed) {
      this.wasm.wsola_reset(this.handle);
    }
  }

  destroy(): void {
    if (this.destroyed) {
      return;
    }
    this.destroyed = true;
    if (this.handle) {
      this.wasm.wsola_destroy(this.handle);
      this.handle = 0;
    }
    if (this.inputPtr) {
      this.wasm.free(this.inputPtr);
      this.inputPtr = 0;
      this.inputCapacityFrames = 0;
    }
    if (this.outputPtr) {
      this.wasm.free(this.outputPtr);
      this.outputPtr = 0;
      this.outputCapacityFrames = 0;
    }
  }

  private reserveInput(frames: number): void {
    if (frames <= this.inputCapacityFrames) {
      return;
    }
    if (this.inputPtr) {
      this.wasm.free(this.inputPtr);
      this.inputPtr = 0;
    }
    this.inputCapacityFrames = Math.max(frames, 4096);
    this.inputPtr = this.wasm.malloc(this.inputCapacityFrames * this.channels_ * 4);
    if (!this.inputPtr) {
      throw new EngineError("state", "WSOLA 输入缓冲分配失败");
    }
  }

  private reserveOutput(frames: number): void {
    if (frames <= this.outputCapacityFrames) {
      return;
    }
    if (this.outputPtr) {
      this.wasm.free(this.outputPtr);
      this.outputPtr = 0;
    }
    this.outputCapacityFrames = frames;
    this.outputPtr = this.wasm.malloc(this.outputCapacityFrames * this.channels_ * 4);
    if (!this.outputPtr) {
      throw new EngineError("state", "WSOLA 输出缓冲分配失败");
    }
  }
}
