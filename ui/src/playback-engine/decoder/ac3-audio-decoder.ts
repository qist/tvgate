/*
 * AC-3 / E-AC-3 Decoder (Dolby Digital / DD+)
 *
 * WASM wrapper for the FFmpeg-based ac3_decoder — directly calls WASM exports
 * without the Emscripten JS glue (same pattern as MpegAudioDecoder).
 *
 * Decodes whole PES payloads: the WASM side loops over all complete frames
 * (0x0B77 syncword + frame length) and keeps trailing partial frames in an
 * internal carry buffer, so frames split across PES packets are handled
 * transparently. Output is interleaved stereo float32 PCM — the 5.1→stereo
 * downmix (incl. dialnorm) happens inside the ffmpeg decoder via
 * request_channel_layout.
 */

// Maximum interleaved samples per frame (1536 samples × 2 channels)
const MAX_INTERLEAVED_PER_FRAME = 1536 * 2;

// Smallest valid AC-3 frame (~32kbps @ 48kHz is 64*2=128 bytes)
const MIN_FRAME_BYTES = 128;

// Carry buffer size on the WASM side (E-AC-3 frames can reach ~4KB)
const CARRY_MAX = 4096;

// Info array layout (8 × i32):
// [samplesPerChannel, sampleRate, channels, frames, carryBytes, consumedBytes, samplesBeforeInput, errors]
const INFO_SAMPLES = 0;
const INFO_SAMPLE_RATE = 1;
const INFO_CHANNELS = 2;
const INFO_SAMPLES_BEFORE_INPUT = 6;
const INFO_I32_COUNT = 8;

export interface DecodedAudio {
  /** Interleaved stereo float32 PCM. */
  pcm: Float32Array;
  samplesPerChannel: number;
  sampleRate: number;
  channels: number;
  /** Samples/ch in this decoded output that came from frames carried over from before the current PES payload. */
  samplesBeforeInput: number;
}

function createWasmImports() {
  return {
    env: {
      emscripten_notify_memory_growth: () => { },
    },
    // ffmpeg libavutil links against wasi stubs (fd_*/clock_time_get);
    // they are never exercised on the decode path, so stub them out.
    wasi_snapshot_preview1: new Proxy(
      {},
      {
        get: (_target, prop) => {
          void _target;
          return prop === "clock_time_get" ? () => 0 : () => 52 /* ENOSYS */;
        },
      },
    ),
  };
}

let cachedWasmUrl: string | null = null;
let cachedWasmInstance: WebAssembly.Instance | null = null;

export class Ac3AudioDecoder {
  private exports: Record<string, CallableFunction> | null = null;
  private memoryRef: { memory: WebAssembly.Memory | null } = { memory: null };
  private decoderPtr = 0;
  private inputPtr = 0;
  private outputPtr = 0;
  private infoPtr = 0;
  private inputBufSize = 0;
  private outputBufFloats = 0;

  private _ready: Promise<void>;
  private _isReady = false;

  constructor(wasmUrl: string, eac3 = false) {
    this._ready = this.init(wasmUrl, eac3);
  }

  get ready(): Promise<void> {
    return this._ready;
  }

  get isReady(): boolean {
    return this._isReady;
  }

  private async init(wasmUrl: string, eac3: boolean): Promise<void> {
    if (!cachedWasmInstance || cachedWasmUrl !== wasmUrl) {
      const imports = createWasmImports();
      const { instance } = await WebAssembly.instantiateStreaming(fetch(wasmUrl), imports);
      const ex = instance.exports as Record<string, WebAssembly.Global | WebAssembly.Memory | CallableFunction>;
      // Standalone WASM reactor initialization, once per cached instance.
      (ex._initialize as CallableFunction)();
      cachedWasmInstance = instance;
      cachedWasmUrl = wasmUrl;
    }

    const instance = cachedWasmInstance;
    const ex = instance.exports as Record<string, WebAssembly.Global | WebAssembly.Memory | CallableFunction>;

    this.memoryRef.memory = ex.memory as WebAssembly.Memory;
    this.exports = ex as unknown as Record<string, CallableFunction>;

    const create = ex.ac3_decoder_create as (eac3: number) => number;
    this.decoderPtr = create(eac3 ? 1 : 0);
    if (!this.decoderPtr) {
      throw new Error("Failed to create AC-3 decoder");
    }

    const malloc = ex.malloc as (size: number) => number;
    this.infoPtr = malloc(INFO_I32_COUNT * 4);

    this._isReady = true;
  }

  decode(input: Uint8Array): DecodedAudio | null {
    if (!this._isReady || !this.exports || !this.memoryRef.memory) return null;

    const malloc = this.exports.malloc as (size: number) => number;
    const free = this.exports.free as (ptr: number) => void;

    // Grow input buffer if needed
    if (input.length > this.inputBufSize) {
      if (this.inputPtr) free(this.inputPtr);
      this.inputBufSize = Math.max(input.length, 4096);
      this.inputPtr = malloc(this.inputBufSize);
    }

    // Grow output buffer to hold every frame the payload could contain
    const maxFrames = Math.floor((CARRY_MAX + input.length) / MIN_FRAME_BYTES) + 2;
    const neededFloats = maxFrames * MAX_INTERLEAVED_PER_FRAME;
    if (neededFloats > this.outputBufFloats) {
      if (this.outputPtr) free(this.outputPtr);
      this.outputBufFloats = neededFloats;
      this.outputPtr = malloc(neededFloats * 4);
    }

    // Copy input into WASM memory
    const heap = new Uint8Array(this.memoryRef.memory.buffer);
    heap.set(input, this.inputPtr);

    const decodeFn = this.exports.ac3_decode_payload as (
      dec: number,
      inp: number,
      inpSz: number,
      out: number,
      outCap: number,
      info: number,
    ) => number;
    const samples = decodeFn(
      this.decoderPtr,
      this.inputPtr,
      input.length,
      this.outputPtr,
      this.outputBufFloats,
      this.infoPtr,
    );
    if (samples <= 0) return null;

    // Read info from WASM memory (may have changed due to memory growth)
    const i32 = new Int32Array(this.memoryRef.memory.buffer);
    const infoBase = this.infoPtr >> 2;
    const samplesPerChannel = i32[infoBase + INFO_SAMPLES];
    const sampleRate = i32[infoBase + INFO_SAMPLE_RATE];
    const channels = i32[infoBase + INFO_CHANNELS];
    const samplesBeforeInput = i32[infoBase + INFO_SAMPLES_BEFORE_INPUT];

    // Copy float32 PCM out of WASM memory
    const totalFloats = samplesPerChannel * channels;
    const view = new Float32Array(this.memoryRef.memory.buffer, this.outputPtr, totalFloats);
    const pcm = new Float32Array(totalFloats);
    pcm.set(view);

    return { pcm, samplesPerChannel, sampleRate, channels, samplesBeforeInput };
  }

  /** Reset decoder state (call on stream switch to avoid stale mdct state) */
  reset(): void {
    if (!this._isReady || !this.exports) return;
    (this.exports.ac3_decoder_reset as (dec: number) => void)(this.decoderPtr);
  }

  destroy(): void {
    if (!this.exports) return;
    const free = this.exports.free as (ptr: number) => void;
    const destroyFn = this.exports.ac3_decoder_destroy as (dec: number) => void;

    if (this.decoderPtr) {
      destroyFn(this.decoderPtr);
      this.decoderPtr = 0;
    }
    if (this.inputPtr) {
      free(this.inputPtr);
      this.inputPtr = 0;
    }
    if (this.outputPtr) {
      free(this.outputPtr);
      this.outputPtr = 0;
    }
    if (this.infoPtr) {
      free(this.infoPtr);
      this.infoPtr = 0;
    }
    this.exports = null;
    this.memoryRef.memory = null;
    this._isReady = false;
  }
}
