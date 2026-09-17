/**
 * 触控手势视觉回响（clean-room 重写）。
 * 把最近一次手势（音量 / 换台 / 快进快退）以装饰层呈现；指示器消失后留 200ms 淡出，避免内容闪烁。
 * 纯装饰（aria-hidden）：音量读数随指针频繁变化，不应进 live region 骚扰读屏软件。
 */
import { ChevronDown, ChevronUp, FastForward, Rewind, Volume1, Volume2, VolumeX } from "lucide-react";
import { useEffect, useState } from "react";
import type { PlayerGestureIndicator } from "../../hooks/use-player-touch-gestures";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { Locale } from "../../lib/locale";
import { PLAYER_OVERLAY_SURFACE_CLASS } from "./classnames";
import { PlayerSelectedGlassLayers } from "./player-selected-glass-layers";

/** 与卡片 opacity-200 过渡对齐，保证淡出期间内容不闪。 */
const FADE_OUT_MS = 200;

const ICON_CLASS =
  "h-7 w-7 shrink-0 text-violet-100 drop-shadow-[0_0_14px_rgba(var(--pg-rgb),0.5)] md:h-9 md:w-9 [@container_video_(max-height:_320px)]:h-6 [@container_video_(max-height:_320px)]:w-6 md:[@container_video_(max-height:_320px)]:h-6 md:[@container_video_(max-height:_320px)]:w-6";

function formatSeekDelta(deltaSeconds: number): string {
  const rounded = Math.round(deltaSeconds);
  const sign = rounded < 0 ? "-" : "+";
  const absolute = Math.abs(rounded);
  const minutes = Math.floor(absolute / 60);
  const seconds = absolute % 60;
  return `${sign}${minutes}:${String(seconds).padStart(2, "0")}`;
}

function VolumeIndicator({ volume }: { volume: number }) {
  const percent = Math.round(volume * 100);
  const Icon = volume <= 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2;
  return (
    <>
      <Icon className={ICON_CLASS} />
      <div className="h-1.5 w-28 overflow-hidden rounded-full bg-violet-50/15 shadow-[inset_0_1px_3px_rgba(0,0,0,0.45)] ring-1 ring-white/10 md:w-40">
        <div
          className="player-performance-progress-fill h-full rounded-full bg-[linear-gradient(90deg,var(--pg-grad-a)_0%,var(--pg-grad-b)_52%,var(--pg-grad-c)_100%)] shadow-[0_0_18px_rgba(var(--pg-rgb),0.4)]"
          style={{ width: `${percent}%` }}
        />
      </div>
      <span className="w-10 shrink-0 text-right font-semibold text-violet-50 text-sm tabular-nums md:text-base">
        {percent}%
      </span>
    </>
  );
}

function ChannelIndicator({
  indicator,
  fallbackLabel,
}: {
  indicator: Extract<PlayerGestureIndicator, { kind: "channel" }>;
  fallbackLabel: string;
}) {
  const { direction, target } = indicator;
  const Chevron = direction === "prev" ? ChevronUp : ChevronDown;
  return (
    <>
      <Chevron className={ICON_CLASS} />
      {target ? (
        <>
          <span className="shrink-0 rounded-md bg-violet-100/10 px-1.5 py-0.5 font-semibold text-violet-50/65 text-xs tabular-nums ring-1 ring-violet-100/10 md:text-sm">
            {target.number ?? ""}
          </span>
          <span className="max-w-[40vw] truncate font-bold text-sm text-white md:text-lg">{target.name}</span>
        </>
      ) : (
        <span className="font-bold text-sm text-white md:text-lg">{fallbackLabel}</span>
      )}
    </>
  );
}

export function PlayerGestureIndicatorOverlay({ indicator, locale }: { indicator: PlayerGestureIndicator | null; locale: Locale }) {
  const t = usePlayerTranslation(locale);
  const [shown, setShown] = useState(indicator);

  useEffect(() => {
    if (indicator) {
      setShown(indicator);
      return;
    }
    const timeoutId = window.setTimeout(() => setShown(null), FADE_OUT_MS);
    return () => window.clearTimeout(timeoutId);
  }, [indicator]);

  return (
    <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center p-4">
      <div
        className={[
          PLAYER_OVERLAY_SURFACE_CLASS,
          "player-performance-motion relative flex max-w-full items-center gap-2 rounded-xl px-3 py-2 transition-opacity duration-200 md:gap-3 md:px-4 md:py-3 [@container_video_(max-height:_320px)]:gap-1.5 [@container_video_(max-height:_320px)]:rounded-lg [@container_video_(max-height:_320px)]:px-2 [@container_video_(max-height:_320px)]:py-1.5",
          indicator ? "opacity-100" : "opacity-0",
        ].join(" ")}
      >
        <PlayerSelectedGlassLayers />
        <div className="relative z-10 flex min-w-0 items-center gap-2 md:gap-3">
          {shown?.kind === "volume" && <VolumeIndicator volume={shown.volume} />}
          {shown?.kind === "channel" && (
            <ChannelIndicator
              indicator={shown}
              fallbackLabel={shown.direction === "prev" ? t("previousChannel") : t("nextChannel")}
            />
          )}
          {shown?.kind === "seek" && (
            <>
              {shown.deltaSeconds < 0 ? <Rewind className={ICON_CLASS} /> : <FastForward className={ICON_CLASS} />}
              <span className="font-bold text-base text-white tabular-nums md:text-xl">{formatSeekDelta(shown.deltaSeconds)}</span>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
