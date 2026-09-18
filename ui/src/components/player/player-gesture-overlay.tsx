/**
 * 触控手势的视觉回响。
 *
 * 为什么"指示器内容"和"透明度"分离：手势数据归零时卡片先淡出（200ms，
 * 与卡片自身 opacity 过渡时长对齐），淡出期间仍渲染最后一次的内容——
 * 若立即卸载，内容会先消失再淡出空卡片，看起来像闪了一下。
 * 纯装饰（aria-hidden）：音量读数会随滑动高频刷新，进 live region 只会刷屏打扰读屏用户。
 */
import { ChevronDown, ChevronUp, FastForward, Rewind, Volume1, Volume2, VolumeX } from "lucide-react";
import { useEffect, useState } from "react";
import type { PlayerGestureIndicator } from "../../hooks/use-player-touch-gestures";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { Locale } from "../../lib/locale";
import { PLAYER_OVERLAY_SURFACE_CLASS } from "./classnames";
import { PlayerSelectedGlassLayers } from "./player-selected-glass-layers";

/** 淡出等待时长：必须不短于卡片 opacity 过渡（200ms），否则来不及淡完就卸载。 */
const HIDE_DELAY_MS = 200;

/** 指示器图标：三档尺寸对应常规/桌面/矮容器（视频区高度 ≤320px）。 */
const GESTURE_ICON_CLASS =
  "h-7 w-7 shrink-0 text-violet-100 drop-shadow-[0_0_14px_rgba(var(--pg-rgb),0.5)] md:h-9 md:w-9 [@container_video_(max-height:_320px)]:h-6 [@container_video_(max-height:_320px)]:w-6 md:[@container_video_(max-height:_320px)]:h-6 md:[@container_video_(max-height:_320px)]:w-6";

/** 手势卡片本体样式（透明度由外部按 indicator 是否在场动态追加）。 */
const GESTURE_CARD_CLASS =
  "player-performance-motion relative flex max-w-full items-center gap-2 rounded-xl px-3 py-2 transition-opacity duration-200 md:gap-3 md:px-4 md:py-3 [@container_video_(max-height:_320px)]:gap-1.5 [@container_video_(max-height:_320px)]:rounded-lg [@container_video_(max-height:_320px)]:px-2 [@container_video_(max-height:_320px)]:py-1.5";

/** 频道类手势的负载形状，从联合类型中窄化出来，避免每个消费者重复 Extract。 */
type ChannelGesture = Extract<PlayerGestureIndicator, { kind: "channel" }>;

/** 快进/快退读数：统一为 `+m:ss` / `-m:ss`，秒位补零保证宽度稳定。 */
function describeSeekOffset(deltaSeconds: number): string {
  const wholeSeconds = Math.round(deltaSeconds);
  // 以取整后的值定符号：-0.4s 取整为 -0，与 0 同侧按 "+" 渲染
  const sign = wholeSeconds < 0 ? "-" : "+";
  const magnitude = Math.abs(wholeSeconds);
  const minutes = Math.floor(magnitude / 60);
  const seconds = magnitude % 60;
  return `${sign}${minutes}:${String(seconds).padStart(2, "0")}`;
}

function VolumeReadout({ volume }: { volume: number }) {
  const percent = Math.round(volume * 100);
  // 三档图标贴合常见遥控器的音量语义：静音 / 低 / 正常
  const VolumeGlyph = volume <= 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2;
  return (
    <>
      <VolumeGlyph className={GESTURE_ICON_CLASS} />
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

function ChannelReadout({ hint, fallbackText }: { hint: ChannelGesture; fallbackText: string }) {
  const { direction, target } = hint;
  const Chevron = direction === "prev" ? ChevronUp : ChevronDown;
  return (
    <>
      <Chevron className={GESTURE_ICON_CLASS} />
      {target ? (
        <>
          <span className="shrink-0 rounded-md bg-violet-100/10 px-1.5 py-0.5 font-semibold text-violet-50/65 text-xs tabular-nums ring-1 ring-violet-100/10 md:text-sm">
            {target.number ?? ""}
          </span>
          <span className="max-w-[40vw] truncate font-bold text-sm text-white md:text-lg">{target.name}</span>
        </>
      ) : (
        // 拿不到相邻频道信息时退化为纯方向提示，手势反馈不能缺席
        <span className="font-bold text-sm text-white md:text-lg">{fallbackText}</span>
      )}
    </>
  );
}

function SeekReadout({ deltaSeconds }: { deltaSeconds: number }) {
  return (
    <>
      {deltaSeconds < 0 ? <Rewind className={GESTURE_ICON_CLASS} /> : <FastForward className={GESTURE_ICON_CLASS} />}
      <span className="font-bold text-base text-white tabular-nums md:text-xl">{describeSeekOffset(deltaSeconds)}</span>
    </>
  );
}

export function PlayerGestureIndicatorOverlay({ indicator, locale }: { indicator: PlayerGestureIndicator | null; locale: Locale }) {
  const t = usePlayerTranslation(locale);
  const [shown, setShown] = useState(indicator);

  useEffect(() => {
    // 数据已归零：延迟卸载内容，给淡出过渡留出完整时长
    if (!indicator) {
      const timeoutId = window.setTimeout(() => setShown(null), HIDE_DELAY_MS);
      return () => window.clearTimeout(timeoutId);
    }
    // 新手势一到立即上屏，不等待；同时 implicitly 取消尚未触发的旧隐藏计时器
    setShown(indicator);
  }, [indicator]);

  // 注意透明度跟随 indicator（原始 prop）而非 shown：淡出动画期间 shown 仍保留内容
  const surfaceClass = [PLAYER_OVERLAY_SURFACE_CLASS, GESTURE_CARD_CLASS, indicator ? "opacity-100" : "opacity-0"].join(" ");

  return (
    <div aria-hidden="true" className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center p-4">
      <div className={surfaceClass}>
        <PlayerSelectedGlassLayers />
        <div className="relative z-10 flex min-w-0 items-center gap-2 md:gap-3">
          {shown?.kind === "volume" && <VolumeReadout volume={shown.volume} />}
          {shown?.kind === "channel" && (
            <ChannelReadout
              hint={shown}
              fallbackText={shown.direction === "prev" ? t("previousChannel") : t("nextChannel")}
            />
          )}
          {shown?.kind === "seek" && <SeekReadout deltaSeconds={shown.deltaSeconds} />}
        </div>
      </div>
    </div>
  );
}
