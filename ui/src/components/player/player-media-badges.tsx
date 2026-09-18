/**
 * 媒体信息徽章条。
 *
 * 为什么不用面板透明度档位：徽章是叠加在画面上的辅助信息，固定高透明
 * （.player-performance-media-badge）就能保证可读，跟着档位走反而会忽明忽暗。
 * 列表不裁切、自由换行，宁可占两行也不让某个徽章被截断。
 */
import type { ReactNode } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { Locale } from "../../lib/locale";
import { identifyAudioCodec, identifyVideoCodec } from "../../media-engine/media-codecs";
import type { PlayerMediaInfo, PlayerRenderState } from "../../media-engine";
import { Badge } from "../ui/badge";

interface PlayerMediaBadgesProps {
  mediaInfo: PlayerMediaInfo | null;
  locale: Locale;
  renderState: PlayerRenderState;
}

/** 一枚徽章的完整描述：key 同时充当列表项标识，tooltip 走原生 title。 */
interface MediaBadgeDescriptor {
  key: string;
  value: ReactNode;
  tooltip: string;
}

/**
 * 引擎归一化后的编码标识 → 展示名的对照表。
 * 表中查不到（未收录的编码）一律不渲染徽章，而不是显示原始字符串。
 */
const VIDEO_CODEC_LABELS: Partial<Record<string, string>> = {
  h264: "H.264",
  hevc: "HEVC",
  vp9: "VP9",
  av1: "AV1",
};

const AUDIO_CODEC_LABELS: Partial<Record<string, string>> = {
  aac: "AAC",
  ac3: "AC-3",
  eac3: "E-AC-3",
  mp2: "MP2",
  mp3: "MP3",
  opus: "Opus",
};

const DYNAMIC_RANGE_LABELS: Partial<Record<string, string>> = {
  sdr: "SDR",
  hdr10: "HDR10",
  hlg: "HLG",
};

/** 数值类字段的统一有效性判定：缺省、非有限数、非正数都视为"没有这个信息"。 */
function isUsableCount(input: number | undefined): input is number {
  return input !== undefined && Number.isFinite(input) && input > 0;
}

function lookupLabel(table: Partial<Record<string, string>>, family: string | undefined): string | null {
  return (family && table[family]) ?? null;
}

function describeVideoCodec(codec: string | undefined): string | null {
  return lookupLabel(VIDEO_CODEC_LABELS, identifyVideoCodec(codec));
}

function describeAudioCodec(codec: string | undefined): string | null {
  return lookupLabel(AUDIO_CODEC_LABELS, identifyAudioCodec(codec));
}

function describeDynamicRange(range: string | undefined): string | null {
  return lookupLabel(DYNAMIC_RANGE_LABELS, range);
}

/** 分辨率：够宽或够高即 4K；否则按扫描类型给 1080p/1080i 这类约定俗成的写法。 */
function describeResolution(height: number | undefined, width: number | undefined, scanType: string | undefined): string | null {
  if (!isUsableCount(height)) return null;
  const wideEnoughForUhd = width !== undefined && Number.isFinite(width) && width >= 3840;
  const tallEnoughForUhd = Math.round(height) >= 2160;
  if (wideEnoughForUhd || tallEnoughForUhd) return "4K";
  const scanSuffix = scanType === "interlaced" ? "i" : "p";
  return `${Math.round(height)}${scanSuffix}`;
}

function describeFrameRate(frameRate: number | undefined, doubled: boolean): string | null {
  if (!isUsableCount(frameRate)) return null;
  // 倍频去隔行时帧率翻倍展示，保留两位小数以容纳 23.976 这类整数化前的值
  const shown = doubled ? frameRate * 2 : frameRate;
  return `${Math.round(shown * 100) / 100} FPS`;
}

/** 码率：超过兆级用 Mbps（两位小数），其余用 Kbps（一位小数），单位随量级切换。 */
function describeBitrate(bitsPerSecond: number | undefined): string | null {
  const KILOBIT = 1_000;
  const MEGABIT = 1_000_000;
  if (!isUsableCount(bitsPerSecond)) return null;
  if (bitsPerSecond >= MEGABIT) {
    return `${Math.round((bitsPerSecond / MEGABIT) * 100) / 100} Mbps`;
  }
  return `${Math.round((bitsPerSecond / KILOBIT) * 10) / 10} Kbps`;
}

/** 声道：常见布局用约定名，其余显示整数声道数。 */
function describeChannels(channelCount: number | undefined, translate: (key: string) => string): string | null {
  if (!isUsableCount(channelCount)) return null;
  if (channelCount === 1) return translate("mediaInfoSingleChannel");
  if (channelCount === 2) return translate("mediaInfoStereoSound");
  if (channelCount === 6) return "5.1";
  if (channelCount === 8) return "7.1";
  return `${Math.round(channelCount)} ${translate("mediaInfoChannelCount")}`;
}

export function PlayerMediaBadges({ mediaInfo, locale, renderState }: PlayerMediaBadgesProps) {
  const t = usePlayerTranslation(locale);
  if (!mediaInfo) return null;

  const { video, audio, bitrate } = mediaInfo;

  const resolution = describeResolution(video?.height, video?.width, video?.scanType);
  const frameRate = describeFrameRate(video?.frameRate, renderState.deinterlacing);
  const videoCodec = describeVideoCodec(video?.codec);
  const audioCodec = describeAudioCodec(audio?.codec);
  const audioChannels = describeChannels(audio?.channelCount, t);
  const dynamicRange = describeDynamicRange(video?.dynamicRange);
  const bitrateText = describeBitrate(bitrate?.bitsPerSecond);
  // 码率可能来自流声明（advertised）或实测（measured），来源不同可信度不同，tooltip 里注明
  const bitrateSourceLabel =
    bitrate?.source === "advertised" ? t("mediaInfoNominalBitrate") : t("mediaInfoObservedBitrate");

  // 声明顺序即徽章展示顺序：按"画质 → 编码 → 声音"的信息优先级排列
  const badgeSpecs: Array<{ key: string; text: string | null; label: string }> = [
    { key: "resolution", text: resolution, label: t("mediaInfoDimensions") },
    { key: "frame-rate", text: frameRate, label: t("mediaInfoFps") },
    { key: "video-codec", text: videoCodec, label: t("mediaInfoVideoCoding") },
    { key: "audio-codec", text: audioCodec, label: t("mediaInfoAudioCoding") },
    { key: "audio-channels", text: audioChannels, label: t("mediaInfoSoundChannels") },
    { key: "dynamic-range", text: dynamicRange, label: t("mediaInfoHdrRange") },
    { key: "bitrate", text: bitrateText, label: bitrateSourceLabel },
  ];

  const badges: MediaBadgeDescriptor[] = [];
  for (const spec of badgeSpecs) {
    if (!spec.text) continue;
    badges.push({ key: spec.key, value: spec.text, tooltip: `${spec.label}: ${spec.text}` });
  }
  if (!badges.length) return null;

  return (
    <ul
      className="m-0 flex w-full min-w-0 list-none flex-wrap content-start items-center gap-x-1 gap-y-1 p-0"
      aria-label={t("mediaInfoLabel")}
    >
      {badges.map((badge) => (
        <li key={badge.key} className="flex h-5 shrink-0 items-center leading-none">
          <Badge
            variant="outline"
            size="compact"
            className="player-performance-media-badge"
            title={badge.tooltip}
          >
            {badge.value}
          </Badge>
        </li>
      ))}
    </ul>
  );
}
