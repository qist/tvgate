/**
 * 音频输出声道能力探测 + 降混（设备不支持 5.1 时主动降到 2.0）。
 *
 * 背景：软解 WASM 现在保留源声道（AC-3/E-AC-3 5.1 直通出 6ch）。设备/浏览器只能输出
 * 立体声时，只依赖 WebAudio 的 destination 混音不可控（老 WebView 对 6ch AudioBuffer
 * 支持未知），因此：
 *   - 主线程探测 `AudioContext.destination.maxChannelCount`：≥6 → 按 6 声道（5.1 直通）；
 *     其余（含能力不可知的老 WebView）→ 按 2 声道；
 *   - 该值两处使用：① 传给 worker → WASM 解码时直接按目标声道数输出（省 2/3 PCM 拷贝与
 *     WSOLA 开销）；② PCM 播放器兜底降混（若上游仍送来多于目标声道的 PCM）。
 * 降混系数按 Web Audio 规范的 speaker 规则：L' = L + 0.707·C + 0.707·SL，R' 同理，LFE 丢弃。
 */

/** 假定布局（与 libavcodec 输出/WebAudio 6 声道解释一致）：2ch = L R；6ch = L R C LFE SL SR。 */
const CH_L = 0;
const CH_R = 1;
const CH_C = 2;
const CH_SL = 4;
const CH_SR = 5;
/** 中置/环绕折入系数（−3dB）。 */
const FOLD_GAIN = Math.SQRT1_2;
/** 目标声道数 >= 此值即视为可放 5.1。 */
const SURROUND_CAPABLE_CHANNELS = 6;

let cachedOutputChannels: number | null = null;

function clamp1(value: number): number {
  return value > 1 ? 1 : value < -1 ? -1 : value;
}

/**
 * 设备可用输出声道数：≥6 视为可放 5.1；其余（含能力不可知）按 2.0。
 * 结果进程内缓存（AudioContext 的 destination 能力不会变）。探测失败不抛错。
 */
export function detectAudioOutputChannels(): number {
  if (cachedOutputChannels !== null) return cachedOutputChannels;
  cachedOutputChannels = 2; // 保守缺省：能力不可知时按 2.0，兼容性优先
  try {
    const scope = globalThis as unknown as {
      AudioContext?: new () => AudioContext;
      webkitAudioContext?: new () => AudioContext;
    };
    const Ctor = scope.AudioContext ?? scope.webkitAudioContext;
    if (Ctor) {
      const ctx = new Ctor();
      const max = (ctx.destination as AudioDestinationNode | undefined)?.maxChannelCount ?? 0;
      if (Number.isFinite(max) && max >= SURROUND_CAPABLE_CHANNELS) {
        cachedOutputChannels = SURROUND_CAPABLE_CHANNELS;
      }
      try {
        void ctx.close();
      } catch {
        /* 老实现可能没有 close() */
      }
    }
  } catch {
    /* 探测失败 → 保持 2.0 */
  }
  return cachedOutputChannels;
}

/** 仅供测试：清空探测缓存。 */
export function resetAudioOutputChannelsCache(): void {
  cachedOutputChannels = null;
}

/**
 * 交织 PCM 降混到 targetChannels（仅当目标更少时才新建缓冲）。
 * 覆盖 6→2 / 6→1（5.1，LFE 丢弃）、4→2（quad）、2→1（左右合成）与兜底取前 N 声道。
 */
export function downmixInterleaved(
  samples: Float32Array,
  channels: number,
  targetChannels: number,
): { samples: Float32Array; channels: number } {
  if (targetChannels <= 0 || targetChannels >= channels) return { samples, channels };
  const frames = Math.floor(samples.length / channels);
  if (frames === 0) return { samples: new Float32Array(0), channels: targetChannels };
  const out = new Float32Array(frames * targetChannels);

  for (let i = 0; i < frames; i++) {
    const base = i * channels;
    if (channels === 6 && targetChannels === 2) {
      const l = samples[base + CH_L] + FOLD_GAIN * samples[base + CH_C] + FOLD_GAIN * samples[base + CH_SL];
      const r = samples[base + CH_R] + FOLD_GAIN * samples[base + CH_C] + FOLD_GAIN * samples[base + CH_SR];
      out[i * 2] = clamp1(l);
      out[i * 2 + 1] = clamp1(r);
    } else if (channels === 6 && targetChannels === 1) {
      const l = samples[base + CH_L] + FOLD_GAIN * samples[base + CH_C] + FOLD_GAIN * samples[base + CH_SL];
      const r = samples[base + CH_R] + FOLD_GAIN * samples[base + CH_C] + FOLD_GAIN * samples[base + CH_SR];
      out[i] = clamp1((l + r) * 0.5);
    } else if (channels === 4 && targetChannels === 2) {
      out[i * 2] = clamp1(samples[base] + FOLD_GAIN * samples[base + 2]);
      out[i * 2 + 1] = clamp1(samples[base + 1] + FOLD_GAIN * samples[base + 3]);
    } else if (channels === 2 && targetChannels === 1) {
      out[i] = (samples[base + CH_L] + samples[base + CH_R]) * 0.5;
    } else {
      // 未知布局不做臆测性混音：取前 targetChannels 个声道
      for (let c = 0; c < targetChannels; c++) out[i * targetChannels + c] = samples[base + c];
    }
  }
  return { samples: out, channels: targetChannels };
}
