// AC-3 WASM decoder smoke test: decode a real AC-3 elementary stream and
// verify output format / energy. Usage: node test-wasm.mjs <ac3file>
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const dir = dirname(fileURLToPath(import.meta.url));

const { instance } = await WebAssembly.instantiate(
  readFileSync(join(dir, "ac3_decoder.wasm")),
  {
    env: { emscripten_notify_memory_growth: () => { } },
    // ffmpeg libavutil links against wasi stubs; unused in decode paths
    wasi_snapshot_preview1: new Proxy(
      {},
      { get: (_t, prop) => (prop === "clock_time_get" ? () => 0 : () => 52 /* ENOSYS */) },
    ),
  },
);
const ex = instance.exports;
ex._initialize();

const file = process.argv[2];
if (!file) {
  console.error("usage: node test-wasm.mjs <ac3file> [eac3]");
  process.exit(1);
}
const eac3 = process.argv[3] === "eac3";
const ac3 = readFileSync(file);

const dec = ex.ac3_decoder_create(eac3 ? 1 : 0);
if (!dec) {
  console.error("ac3_decoder_create failed");
  process.exit(1);
}
const infoPtr = ex.malloc(32);
const inPtr = ex.malloc(ac3.length);
new Uint8Array(ex.memory.buffer).set(ac3, inPtr);
const maxFrames = Math.ceil(ac3.length / 700) + 4;
const outPtr = ex.malloc(maxFrames * 1536 * 2 * 4);

const t0 = Date.now();
const samples = ex.ac3_decode_payload(dec, inPtr, ac3.length, outPtr, maxFrames * 1536 * 2, infoPtr);
const ms = Date.now() - t0;
const info = new Int32Array(ex.memory.buffer, infoPtr, 8);

console.log(`input ${ac3.length} bytes -> samples/ch=${samples} info=[${Array.from(info).join(", ")}] in ${ms}ms`);
if (samples > 0) {
  const pcm = new Float32Array(ex.memory.buffer, outPtr, samples * 2);
  let peak = 0, sumSq = 0;
  for (let i = 0; i < pcm.length; i++) {
    const a = Math.abs(pcm[i]);
    if (a > peak) peak = a;
    sumSq += pcm[i] * pcm[i];
  }
  const rms = Math.sqrt(sumSq / pcm.length);
  console.log(`peak=${peak.toFixed(4)} rms=${rms.toFixed(4)} (非零即有音频能量)`);
  if (peak > 0.001) {
    console.log("PASS: decoded non-silent PCM");
  } else {
    console.log("FAIL: decoded PCM is silent");
    process.exit(1);
  }
} else {
  console.log("FAIL: no samples decoded");
  process.exit(1);
}
