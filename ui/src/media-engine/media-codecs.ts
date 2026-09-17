/**
 * 编解码族识别（clean-room 实现）。
 * 把 MIME / codec 串（如 "avc1.4d401f"、"mp4a.40.2"）归一到语义族，供 UI 展示徽章。
 * 纯字符串映射，无外部依赖。
 */

export type VideoCodecFamily = "h264" | "hevc" | "vp9" | "av1";
export type AudioCodecFamily = "aac" | "ac3" | "eac3" | "mp2" | "mp3" | "opus";

function normalize(codec: string | undefined): string {
  return (codec ?? "").trim().toLowerCase().replace(/[^a-z0-9]/g, "");
}

export function identifyVideoCodec(codec: string | undefined): VideoCodecFamily | undefined {
  const c = normalize(codec);
  if (!c) return undefined;
  if (c.startsWith("hvc1") || c.startsWith("hev1") || c.startsWith("hevc") || c.startsWith("h265")) return "hevc";
  if (c.startsWith("avc1") || c.startsWith("avc3") || c.startsWith("avc") || c.startsWith("h264")) return "h264";
  if (c.startsWith("vp09") || c.startsWith("vp9")) return "vp9";
  if (c.startsWith("av01") || c.startsWith("av1")) return "av1";
  return undefined;
}

export function identifyAudioCodec(codec: string | undefined): AudioCodecFamily | undefined {
  const c = normalize(codec);
  if (!c) return undefined;
  if (c.startsWith("eac3") || c.startsWith("ec3")) return "eac3";
  if (c.startsWith("ac3")) return "ac3";
  // AAC 的 mp4a 对象类型：40=AAC, 66/67/68 = AAC-HE 系列
  if (c.startsWith("mp4a40") || c.startsWith("mp4a66") || c.startsWith("mp4a67") || c.startsWith("mp4a68") || c.startsWith("aac")) {
    return "aac";
  }
  // MP3 的 mp4a 对象类型：69/6B
  if (c.startsWith("mp4a69") || c.startsWith("mp4a6b") || c.startsWith("mp3")) return "mp3";
  if (c.startsWith("mp2") || c.startsWith("mpga")) return "mp2";
  if (c.startsWith("opus")) return "opus";
  return undefined;
}
