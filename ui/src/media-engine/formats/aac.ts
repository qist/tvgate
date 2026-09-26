/**
 * AAC 基本流工具。
 * 依据 ISO/IEC 13818-7（ADTS）与 ISO/IEC 14496-3（AudioSpecificConfig）公开规范重新实现：
 * ADTS 头解析（采样率/声道/帧长）、AudioSpecificConfig 构造、esds box 构造。
 * 均为无状态纯算法，供 demux/remux 复用。
 */

const SAMPLING_RATES = [
  96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350,
];

export interface AdtsFrameInfo {
  profile: number; // 0 = AAC Main, 1 = AAC LC ...
  samplingFrequencyIndex: number;
  sampleRate: number;
  channelConfig: number;
  channels: number;
  frameLength: number; // 含头部的整帧长度
  headerLength: number; // 7（无 CRC）或 9（有 CRC）
  dataStart: number; // 负载起点（相对传入缓冲）
}

/** 解析单个 ADTS 帧头。 */
export function parseAdtsFrame(data: Uint8Array, offset: number): AdtsFrameInfo | null {
  if (offset + 7 > data.length) return null;
  if (data[offset] !== 0xff || (data[offset + 1] & 0xf0) !== 0xf0) return null;

  const protectionAbsent = (data[offset + 1] & 0x01) === 1;
  const profile = (data[offset + 2] >> 6) & 0x03;
  const samplingFrequencyIndex = (data[offset + 2] >> 2) & 0x0f;
  const channelConfig = ((data[offset + 2] & 0x01) << 2) | ((data[offset + 3] >> 6) & 0x03);
  const frameLength = ((data[offset + 3] & 0x03) << 11) | (data[offset + 4] << 3) | (data[offset + 5] >> 5);
  const headerLength = protectionAbsent ? 7 : 9;

  if (frameLength < headerLength) return null;
  const sampleRate = SAMPLING_RATES[samplingFrequencyIndex];

  return {
    profile,
    samplingFrequencyIndex,
    sampleRate,
    channelConfig,
    channels: channelConfigToCount(channelConfig),
    frameLength,
    headerLength,
    dataStart: offset + headerLength,
  };
}

/**
 * 在缓冲中搜索下一个**可校验的** ADTS 帧头，返回相对偏移（找不到返回 -1）。
 *
 * TS 里 PES 边界与 ADTS 帧边界无关：PES 载荷可能自帧中间开始（上一帧的尾巴）、
 * 也可能止于半帧。只看"载荷第 0 字节是不是 0xFFFx"会把整段丢掉（实测：江苏移动系
 * 流就是这样，整条音轨 0 样本 → 有画面没声音），所以必须扫同步字再逐帧切。
 * 只认同步字会被随机字节误判（概率约 1/2048），故连同采样率索引、声道、帧长一起校验。
 *
 * 链式验证：帧长末端必须也是一个帧头且采样率/声道与本头一致，否则视为假头继续扫。
 * 起因（实测某 4K 轮播源）：AAC 压缩载荷里随机出现 `FF FE 62 69 04 E9` 序列，恰能
 * 通过单头浅校验（声明 16kHz/单声道/帧长 1867），被当首帧锁进 esds —— 而真实帧是
 * 48kHz LC 立体声。esds 声明 16kHz 后 MSE 解码器按错误配置解 48kHz 帧 →
 * "scalefactor bands exceeds limit" 解码失败 → 音频 SB error → 元素 ERROR →
 * 所有 append 被拒 → 起播卡死。真帧链相邻帧头参数恒定，链式验证不会误伤；
 * 末端不足一个帧头（PES 尾半帧）放行，由调用方按半帧留 pending。
 */
export function findAdtsSync(data: Uint8Array, from = 0): number {
  for (let i = Math.max(0, from); i + 7 <= data.length; i++) {
    if (data[i] !== 0xff || (data[i + 1] & 0xf0) !== 0xf0) continue;
    const frame = parseAdtsFrame(data, i);
    if (!frame) continue;
    if (frame.samplingFrequencyIndex > 12) continue; // 13/14 保留、15 为显式频率（本实现不支持）
    if (frame.channelConfig > 7) continue;
    const next = i + frame.frameLength;
    if (next + 7 > data.length) return i; // PES 尾半帧：头完整但帧链出界，交调用方 pending
    const nf = parseAdtsFrame(data, next);
    if (
      !nf ||
      nf.samplingFrequencyIndex !== frame.samplingFrequencyIndex ||
      nf.channelConfig !== frame.channelConfig
    ) {
      continue; // 末端不是参数一致的合法帧头：假同步，继续扫
    }
    return i;
  }
  return -1;
}

function channelConfigToCount(cfg: number): number {
  switch (cfg) {
    case 0:
      return 0; // 由 PCE 决定
    case 1:
      return 1;
    case 2:
      return 2;
    case 3:
      return 3;
    case 4:
      return 4;
    case 5:
      return 5;
    case 6:
      return 6;
    case 7:
      return 8;
    default:
      return 2;
  }
}

/**
 * 解析 init 中要声明的 AOT（音频对象类型）。
 *
 * 重要：Chrome 等浏览器的 MSE AAC 解码器对「裸 LC（2 字节）ASC」存在静默不发声的
 * 已知问题。业界通行规避（flv.js 等 MSE 播放器）：即便实际是 LC-AAC，也把 esds 里的
 * ASC 声明成 HE-AAC（AOT=5）的 4 字节配置，再借扩展段（extended AOT=2）把真实
 * 格式强制回 LC-AAC。Firefox / Android 另有分支，需保持一致。
 */
function resolveAacConfig(
  samplingFrequencyIndex: number,
  channelConfig: number,
): { audioObjectType: number; extensionSamplingIndex: number; size: number } {
  const ua =
    typeof navigator !== "undefined" && typeof navigator.userAgent === "string"
      ? navigator.userAgent.toLowerCase()
      : "";

  if (ua.indexOf("firefox") !== -1) {
    // Firefox：采样率 < 24kHz（索引 >= 6）用 SBR(HE-AAC)，否则 LC
    return samplingFrequencyIndex >= 6
      ? { audioObjectType: 5, extensionSamplingIndex: samplingFrequencyIndex - 3, size: 4 }
      : { audioObjectType: 2, extensionSamplingIndex: samplingFrequencyIndex, size: 2 };
  }
  if (ua.indexOf("android") !== -1) {
    // Android：始终 LC-AAC
    return { audioObjectType: 2, extensionSamplingIndex: samplingFrequencyIndex, size: 2 };
  }

  // Chrome / 其余：一律声明 HE-AAC（AOT=5），扩展段强制回 LC，规避解码器静音
  let audioObjectType = 5;
  let extensionSamplingIndex = samplingFrequencyIndex;
  let size = 4;
  if (samplingFrequencyIndex >= 6) {
    extensionSamplingIndex = samplingFrequencyIndex - 3;
  } else if (channelConfig === 1) {
    // 单声道：直接用 LC-AAC
    audioObjectType = 2;
    size = 2;
    extensionSamplingIndex = samplingFrequencyIndex;
  }
  return { audioObjectType, extensionSamplingIndex, size };
}

/** 构造 AudioSpecificConfig（2 字节 LC 或 4 字节 HE-AAC 信令）。 */
export function buildAudioSpecificConfig(
  samplingFrequencyIndex: number,
  channelConfig: number,
  _profile = 1,
): Uint8Array {
  const { audioObjectType, extensionSamplingIndex, size } = resolveAacConfig(
    samplingFrequencyIndex,
    channelConfig,
  );
  const config = new Uint8Array(size);
  config[0] = (audioObjectType << 3) | ((samplingFrequencyIndex & 0x0f) >> 1);
  config[1] = (samplingFrequencyIndex & 0x0f) << 7;
  config[1] |= (channelConfig & 0x0f) << 3;
  if (audioObjectType === 5) {
    config[1] |= (extensionSamplingIndex & 0x0f) >> 1;
    config[2] = (extensionSamplingIndex & 0x01) << 7;
    config[2] |= 2 << 2; // 扩展段 extended AOT = 2（强制 LC-AAC，无 SBR/PS）
    config[3] = 0;
  }
  return config;
}

/** MSE/fMP4 中声明该 AAC 轨的 codec mimetype（mp4a.40.<AOT>）。 */
export function aacCodecMimeType(samplingFrequencyIndex: number, channelConfig: number): string {
  return `mp4a.40.${resolveAacConfig(samplingFrequencyIndex, channelConfig).audioObjectType}`;
}

function u32Bytes(v: number): Uint8Array {
  return new Uint8Array([(v >>> 24) & 0xff, (v >>> 16) & 0xff, (v >>> 8) & 0xff, v & 0xff]);
}

function box(type: string, payload: Uint8Array): Uint8Array {
  const t = new Uint8Array(4);
  for (let i = 0; i < 4; i++) t[i] = type.charCodeAt(i);
  const out = new Uint8Array(payload.length + 8);
  out.set(u32Bytes(payload.length + 8), 0);
  out.set(t, 4);
  out.set(payload, 8);
  return out;
}

/** 用变长长度写描述符（每个字节 7 位，高位续行）。 */
function descriptorLength(len: number): Uint8Array {
  const bytes: number[] = [];
  let v = len;
  bytes.push(v & 0x7f);
  v >>= 7;
  while (v > 0) {
    bytes.unshift((v & 0x7f) | 0x80);
    v >>= 7;
  }
  return new Uint8Array(bytes);
}

/** 构造 descriptor：tag + 变长 len + body（不引入额外填充字节）。 */
function tag(tagByte: number, body: Uint8Array): Uint8Array {
  const len = descriptorLength(body.length);
  const out = new Uint8Array(1 + len.length + body.length);
  out[0] = tagByte;
  out.set(len, 1);
  out.set(body, 1 + len.length);
  return out;
}

function concatBytes(...parts: Uint8Array[]): Uint8Array {
  let total = 0;
  for (const p of parts) total += p.length;
  const out = new Uint8Array(total);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

/** 由 AudioSpecificConfig 构造 esds box（fMP4 init 的 codecPrivate）。 */
export function buildEsds(audioSpecificConfig: Uint8Array): Uint8Array {
  // DecoderSpecificInfo：tag 0x05 + len + AudioSpecificConfig
  const dsi = tag(0x05, audioSpecificConfig);

  // DecoderConfigDescriptor 固定头：objectType(0x40) + streamType 字节 + bufferSize(3) + maxBitrate(4) + avgBitrate(4)
  const dcdHeader = new Uint8Array([
    0x40, // objectTypeIndication = Audio ISO/IEC 14496-3
    (0x05 << 2) | 0x01, // streamType(AudioStream=0x05, 6bit) << 2 | upStream(0) | reserved(1) = 0x15
    0x00, 0x00, 0x00, // bufferSizeDB
    0x00, 0x00, 0x00, 0x00, // maxBitrate
    0x00, 0x00, 0x00, 0x00, // avgBitrate
  ]);
  const dcd = tag(0x04, concatBytes(dcdHeader, dsi));

  // SLConfigDescriptor：tag 0x06 + len(1) + 0x02
  const sl = new Uint8Array([0x06, 0x01, 0x02]);

  // ES_Descriptor：tag 0x03 + len + es_id(2) + flags(1) + DCD + SL
  const esd = tag(0x03, concatBytes(new Uint8Array([0x00, 0x01, 0x00]), dcd, sl));

  // esds box：version+flags(4) + ES_Descriptor
  const payload = new Uint8Array(4 + esd.length);
  payload.set([0x00, 0x00, 0x00, 0x00], 0);
  payload.set(esd, 4);
  return box("esds", payload);
}
