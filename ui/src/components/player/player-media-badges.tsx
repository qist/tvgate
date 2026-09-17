/**
 * 媒体信息徽章（clean-room 重写）。
 * 把引擎上报的 PlayerMediaInfo 格式化为可读徽章：分辨率/帧率/视频编码/音频编码/声道/动态范围/码率。
 * 徽标本身是**固定高透明**样式（`.player-performance-media-badge`），不参与"面板透明度"档位；
 * 列表不裁切（自由换行），保证所有徽章都完整可见。
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

interface MediaBadgeValue {
  key: string;
  value: ReactNode;
  tooltip: string;
}

function formatVideoCodec(codec: string | undefined): string | null {
  switch (identifyVideoCodec(codec)) {
    case "h264":
      return "H.264";
    case "hevc":
      return "HEVC";
    case "vp9":
      return "VP9";
    case "av1":
      return "AV1";
    default:
      return null;
  }
}

function formatAudioCodec(codec: string | undefined): string | null {
  switch (identifyAudioCodec(codec)) {
    case "aac":
      return "AAC";
    case "ac3":
      return "AC-3";
    case "eac3":
      return "E-AC-3";
    case "mp2":
      return "MP2";
    case "mp3":
      return "MP3";
    case "opus":
      return "Opus";
    default:
      return null;
  }
}

function formatResolution(height: number | undefined, width: number | undefined, scanType: string | undefined): string | null {
  if (!height || !Number.isFinite(height) || height <= 0) return null;
  const isUhd = (width !== undefined && Number.isFinite(width) && width >= 3840) || Math.round(height) >= 2160;
  if (isUhd) return "4K";
  const scan = scanType === "interlaced" ? "i" : "p";
  return `${Math.round(height)}${scan}`;
}

function formatFrameRate(frameRate: number | undefined, doubled: boolean): string | null {
  if (!frameRate || !Number.isFinite(frameRate) || frameRate <= 0) return null;
  const shown = doubled ? frameRate * 2 : frameRate;
  return `${Math.round(shown * 100) / 100} FPS`;
}

function formatDynamicRange(range: string | undefined): string | null {
  if (range === "sdr") return "SDR";
  if (range === "hdr10") return "HDR10";
  if (range === "hlg") return "HLG";
  return null;
}

function formatBitrate(bitsPerSecond: number | undefined): string | null {
  if (!bitsPerSecond || !Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0) return null;
  if (bitsPerSecond >= 1_000_000) {
    return `${Math.round((bitsPerSecond / 1_000_000) * 100) / 100} Mbps`;
  }
  return `${Math.round((bitsPerSecond / 1_000) * 10) / 10} Kbps`;
}

function formatChannels(channelCount: number | undefined, t: (key: string) => string): string | null {
  if (!channelCount || !Number.isFinite(channelCount) || channelCount <= 0) return null;
  if (channelCount === 1) return t("mediaInfoMono");
  if (channelCount === 2) return t("mediaInfoStereo");
  if (channelCount === 6) return "5.1";
  if (channelCount === 8) return "7.1";
  return `${Math.round(channelCount)} ${t("mediaInfoChannels")}`;
}

export function PlayerMediaBadges({ mediaInfo, locale, renderState }: PlayerMediaBadgesProps) {
  const t = usePlayerTranslation(locale);
  if (!mediaInfo) return null;

  const video = mediaInfo.video;
  const audio = mediaInfo.audio;

  const resolution = formatResolution(video?.height, video?.width, video?.scanType);
  const frameRate = formatFrameRate(video?.frameRate, renderState.deinterlacing);
  const videoCodec = formatVideoCodec(video?.codec);
  const audioCodec = formatAudioCodec(audio?.codec);
  const audioChannels = formatChannels(audio?.channelCount, t);
  const dynamicRange = formatDynamicRange(video?.dynamicRange);
  const bitrate = formatBitrate(mediaInfo.bitrate?.bitsPerSecond);
  const bitrateSourceLabel =
    mediaInfo.bitrate?.source === "advertised" ? t("mediaInfoAdvertisedBitrate") : t("mediaInfoMeasuredBitrate");

  const candidates: Array<MediaBadgeValue | null> = [
    resolution ? { key: "resolution", value: resolution, tooltip: `${t("mediaInfoResolution")}: ${resolution}` } : null,
    frameRate ? { key: "frame-rate", value: frameRate, tooltip: `${t("mediaInfoFrameRate")}: ${frameRate}` } : null,
    videoCodec
      ? { key: "video-codec", value: videoCodec, tooltip: `${t("mediaInfoVideoCodec")}: ${videoCodec}` }
      : null,
    audioCodec
      ? { key: "audio-codec", value: audioCodec, tooltip: `${t("mediaInfoAudioCodec")}: ${audioCodec}` }
      : null,
    audioChannels
      ? { key: "audio-channels", value: audioChannels, tooltip: `${t("mediaInfoAudioChannels")}: ${audioChannels}` }
      : null,
    dynamicRange
      ? { key: "dynamic-range", value: dynamicRange, tooltip: `${t("mediaInfoDynamicRange")}: ${dynamicRange}` }
      : null,
    bitrate ? { key: "bitrate", value: bitrate, tooltip: `${bitrateSourceLabel}: ${bitrate}` } : null,
  ];

  const badges = candidates.filter((b): b is MediaBadgeValue => b !== null);
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
