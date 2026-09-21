/**
 * 播放器底部控制条：左侧是时间读数与媒体徽章，中间为节目时间轴（可拖拽 seek），
 * 右侧依次排布播放/静音/音量、直播态指示（或"回到直播"）、换源菜单、全屏与画中画。
 *
 * 实现约定：
 * - 时间轴几何统一出自 useTimelineWindow：有节目单时沿 EPG 时间轴取值；无节目单时
 *   退化为"终点=当下、跨度 3 小时"的滚动窗口；流时钟完全缺失时给零宽窗口兜底——
 *   保证任何数据状态下进度条都能拿到合法的几何值。
 * - 拖拽走 Pointer Events + setPointerCapture：同一时刻只认一个活跃指针；触屏不做
 *   悬停预览（手指滑动会反复弹出 tooltip，体验差）。
 * - 松手才提交 seek，拖动全程只刷新预览：拖动途中不打断解码管线。
 */
import {
  Maximize,
  Minimize,
  Pause,
  PictureInPicture,
  Play,
  History,
  Tv,
  Volume2,
  Volume1,
  VolumeX,
} from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { Locale } from "../../lib/locale";
import { createProgramTimeline, programProgressToWallClock } from "../../lib/program-timeline";
import { getPlaybackBackendKind, type PlayerMediaInfo, type PlayerRenderState } from "../../media-engine";
import { isNearLiveWallClock, type LiveSessionAnchor, mseToWallClock } from "../../media-engine/timeline";
import { Badge } from "../ui/badge";
import type { Channel, EPGProgram } from "../../types/player";
import { PLAYER_CONTROL_BUTTON_CLASS, PLAYER_OVERLAY_SURFACE_CLASS } from "./classnames";
import { usePlaybackTime } from "./playback-time-context";
import { PlayerMediaBadges } from "./player-media-badges";
import { PlayerSelectedGlassLayers } from "./player-selected-glass-layers";

interface ControlsProps {
  channel: Channel;
  currentProgram: EPGProgram | null;
  isLive: boolean;
  onSeek: (seekTime: Date, goingLive?: boolean) => void;
  onScrubbingChange: (isScrubbing: boolean) => void;
  locale: Locale;
  mediaInfo: PlayerMediaInfo | null;
  renderState: PlayerRenderState;
  seekStartTime: Date;
  liveSessionAnchor: LiveSessionAnchor | null;
  isPlaying: boolean;
  onPlayPause: () => void;
  volume: number;
  onVolumeChange: (volume: number) => void;
  canControlVolume: boolean;
  isMuted: boolean;
  onMuteToggle: () => void;
  onFullscreen: () => void;
  isFullscreen: boolean;
  isPiP?: boolean;
  isPiPSupported?: boolean;
  onPiPToggle?: () => void;
  showMediaBadges?: boolean;
  showSidebar?: boolean;
  activeSourceIndex?: number;
  onSourceChange?: (index: number) => void;
  /**
   * 外置条模式：控制条挂在视频区下方的常规文档流里（手机 + 原生解码）。
   * 换线路菜单随之改为「点击开合 + portal 置顶浮层」，选中即收回——
   * 手机没有 hover，且菜单必须逃出任何 overflow 裁剪、永远在最前面。
   */
  docked?: boolean;
}

/** 矮容器（容器高度 ≤320px）下的按钮/图标压缩尺寸，避免控制条挤压时间轴。 */
const DENSE_LAYOUT_BUTTON_CLASS = "[@container_video_(max-height:_320px)]:p-1 md:[@container_video_(max-height:_320px)]:p-1";
const DENSE_LAYOUT_ICON_CLASS =
  "[@container_video_(max-height:_320px)]:h-4 [@container_video_(max-height:_320px)]:w-4 md:[@container_video_(max-height:_320px)]:h-4 md:[@container_video_(max-height:_320px)]:w-4";

/** 圆形操作按钮（播放/静音/全屏/画中画）共用的完整类串。 */
const ROUND_ACTION_BUTTON_CLASS = [PLAYER_CONTROL_BUTTON_CLASS, "cursor-pointer p-1 md:p-2", DENSE_LAYOUT_BUTTON_CLASS].join(" ");
/** 播放/静音一档用的图标尺寸（桌面端更大）。 */
const PLAYBACK_ICON_CLASS = [DENSE_LAYOUT_ICON_CLASS, "h-4 w-4 md:h-7 md:w-7"].join(" ");
/** 全屏/画中画一档用的图标尺寸（略小，视觉重量更轻）。 */
const PANEL_TOGGLE_ICON_CLASS = [DENSE_LAYOUT_ICON_CLASS, "h-4 w-4 md:h-6 md:w-6"].join(" ");

/** 无节目单时的兜底时间窗长度：3 小时（毫秒 / 秒两种单位各一份，供不同换算使用）。 */
const FALLBACK_WINDOW_MS = 3 * 60 * 60 * 1000;
const FALLBACK_WINDOW_SECONDS = 3 * 60 * 60;

/** 墙钟时刻 → 本地 "HH:MM"（可选带秒）。 */
function formatWallClock(date: Date, withSeconds = false) {
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: withSeconds ? "2-digit" : undefined });
}

/** 秒数 → "H:MM:SS"（超一小时）或 "M:SS"。 */
function formatSecondCount(totalSeconds: number) {
  const wholeHours = Math.floor(totalSeconds / 3600);
  const wholeMinutes = Math.floor((totalSeconds % 3600) / 60);
  const wholeSeconds = Math.floor(totalSeconds % 60);
  if (wholeHours > 0) {
    return `${wholeHours}:${wholeMinutes.toString().padStart(2, "0")}:${wholeSeconds.toString().padStart(2, "0")}`;
  }
  return `${wholeMinutes}:${wholeSeconds.toString().padStart(2, "0")}`;
}

/**
 * 按静音/音量档位挑选音量图标：
 * 静音（含音量归零）→ VolumeX；音量低于一半 → Volume1；否则 Volume2。
 */
function pickVolumeGlyph(isEffectivelyMuted: boolean, volume: number) {
  if (isEffectivelyMuted) return VolumeX;
  if (volume < 0.5) return Volume1;
  return Volume2;
}

/**
 * 换源菜单里可点的源列表：直播态下全部可见，
 * 回看态下只保留声明了时移能力的源（否则点了也无法回看）。
 */
function collectSwitchableSources(sources: Channel["sources"], isLive: boolean) {
  const selectable: { source: Channel["sources"][number]; index: number }[] = [];
  sources.forEach((source, index) => {
    if (isLive || (source.timeshift && source.timeshiftTemplate)) selectable.push({ source, index });
  });
  return selectable;
}

interface TimelineWindow {
  windowStart: Date;
  windowEnd: Date;
  windowDurationSeconds: number;
  elapsedSeconds: number;
  progressPercent: number;
  timeline: ReturnType<typeof createProgramTimeline> | null;
}

/**
 * 时间轴窗口换算 hook。三条数据路径互斥：
 * 1) EPG 节目单可用：直接映射 createProgramTimeline 的结果（进度放大为百分比）；
 * 2) 无节目单：以"现在"为窗口终点往回推 3 小时，播放头由 MSE 时钟换算成墙钟后定位，
 *    进度夹取到 [0, 100] 防止时钟漂移把播放头画出窗外；
 * 3) 有节目单但时间轴构造失败（时长非法/时钟未就绪）：退回零宽窗口，
 *    起止点落在节目起止（缺失时退到 seekStartTime），渲染出静止的空进度条。
 */
function useTimelineWindow(program: EPGProgram | null, anchorTime: Date, mediaSeconds: number): TimelineWindow {
  const timeline = useMemo(
    () => (program ? createProgramTimeline(program, anchorTime, mediaSeconds) : null),
    [program, anchorTime, mediaSeconds],
  );

  const fallbackRange = useMemo(() => {
    if (program) return null;
    const windowEnd = new Date();
    return { windowStart: new Date(windowEnd.getTime() - FALLBACK_WINDOW_MS), windowEnd, windowDurationSeconds: FALLBACK_WINDOW_SECONDS };
  }, [program]);

  return useMemo(() => {
    if (timeline) {
      return {
        windowStart: timeline.startTime,
        windowEnd: timeline.endTime,
        windowDurationSeconds: timeline.durationSeconds,
        elapsedSeconds: timeline.positionSeconds,
        progressPercent: timeline.progress * 100,
        timeline,
      };
    }
    if (fallbackRange) {
      const playheadTime = mseToWallClock(mediaSeconds, anchorTime);
      const elapsedSeconds = (playheadTime.getTime() - fallbackRange.windowStart.getTime()) / 1000;
      return {
        ...fallbackRange,
        elapsedSeconds,
        progressPercent: Math.min(100, Math.max(0, (elapsedSeconds / fallbackRange.windowDurationSeconds) * 100)),
        timeline: null,
      };
    }
    return {
      windowStart: program?.beginsAt ?? anchorTime,
      windowEnd: program?.endsAt ?? anchorTime,
      windowDurationSeconds: 0,
      elapsedSeconds: 0,
      progressPercent: 0,
      timeline: null,
    };
  }, [anchorTime, fallbackRange, mediaSeconds, program, timeline]);
}

interface TimelineScrubberProps {
  channel: Channel;
  currentProgram: EPGProgram | null;
  liveSessionAnchor: LiveSessionAnchor | null;
  locale: Locale;
  onScrubbingChange: (isScrubbing: boolean) => void;
  onSeek: (seekTime: Date) => void;
  seekStartTime: Date;
}

/** 时间轴拖拽条：节目信息行 + 进度轨道 + 悬停/拖拽预览。 */
const TimelineScrubber = memo(function TimelineScrubber({
  channel,
  currentProgram,
  liveSessionAnchor,
  locale,
  onScrubbingChange,
  onSeek,
  seekStartTime,
}: TimelineScrubberProps) {
  const t = usePlayerTranslation(locale);
  const mediaSeconds = usePlaybackTime();
  const supportsSeeking = channel.sources.some((source) => source.timeshift && source.timeshiftTemplate);
  const { windowStart, windowEnd, windowDurationSeconds, progressPercent, timeline } = useTimelineWindow(
    currentProgram,
    seekStartTime,
    mediaSeconds,
  );
  const trackRef = useRef<HTMLDivElement>(null);
  const capturedPointerIdRef = useRef<number | null>(null);
  const [scrubPercent, setScrubPercent] = useState<number | null>(null);
  const [hoverPercent, setHoverPercent] = useState<number | null>(null);

  // 轨道百分比 → 墙钟时刻：EPG 路径走节目时间轴换算（自动夹取到节目范围内），
  // 兜底路径按窗口长度做线性插值。
  const resolveTimeAtPercent = useCallback(
    (percent: number): Date => {
      if (timeline) return programProgressToWallClock(timeline, percent / 100);
      return new Date(windowStart.getTime() + (windowDurationSeconds * 1000 * percent) / 100);
    },
    [timeline, windowDurationSeconds, windowStart],
  );

  // 指针横坐标 → 轨道百分比（夹取到 [0, 100]）；轨道未挂载或宽度为 0 时放弃。
  const readPercentFromClientX = useCallback((clientX: number): number | null => {
    const track = trackRef.current;
    if (!track) return null;
    const bounds = track.getBoundingClientRect();
    if (bounds.width === 0) return null;
    return Math.min(Math.max(((clientX - bounds.left) / bounds.width) * 100, 0), 100);
  }, []);

  const beginScrubbing = useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      // 不支持回看、非主指针、已有指针在拖拽中、或鼠标非左键：一律忽略，
      // 避免多指/右键把拖拽状态写坏。
      if (!supportsSeeking || !event.isPrimary || capturedPointerIdRef.current !== null) return;
      if (event.pointerType === "mouse" && event.button !== 0) return;
      const percent = readPercentFromClientX(event.clientX);
      if (percent === null) return;
      event.preventDefault();
      event.currentTarget.setPointerCapture(event.pointerId);
      capturedPointerIdRef.current = event.pointerId;
      setHoverPercent(null);
      setScrubPercent(percent);
      onScrubbingChange(true);
    },
    [onScrubbingChange, readPercentFromClientX, supportsSeeking],
  );

  const trackPointerMove = useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      if (!supportsSeeking) return;
      const percent = readPercentFromClientX(event.clientX);
      if (percent === null) return;
      if (capturedPointerIdRef.current === event.pointerId) {
        event.preventDefault();
        setScrubPercent(percent);
        return;
      }
      // 拖拽中的触屏指针不算悬停；悬停预览只在空闲且非触屏时出现。
      if (capturedPointerIdRef.current === null && event.pointerType !== "touch") {
        setHoverPercent(percent);
      }
    },
    [readPercentFromClientX, supportsSeeking],
  );

  // 复位拖拽状态并同步"非拖拽中"信号；pointerup / pointercancel 共用。
  const resetScrub = useCallback(() => {
    capturedPointerIdRef.current = null;
    setScrubPercent(null);
    onScrubbingChange(false);
  }, [onScrubbingChange]);

  const endScrubbing = useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      if (capturedPointerIdRef.current !== event.pointerId) return;
      event.preventDefault();
      const percent = readPercentFromClientX(event.clientX);
      resetScrub();
      if (event.currentTarget.hasPointerCapture(event.pointerId)) {
        event.currentTarget.releasePointerCapture(event.pointerId);
      }
      if (percent !== null) onSeek(resolveTimeAtPercent(percent));
    },
    [onSeek, readPercentFromClientX, resetScrub, resolveTimeAtPercent],
  );

  const abortScrubbing = useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      if (capturedPointerIdRef.current !== event.pointerId) return;
      resetScrub();
    },
    [resetScrub],
  );

  const clearHoverPreview = useCallback(() => {
    if (capturedPointerIdRef.current === null) setHoverPercent(null);
  }, []);

  const shownPercent = scrubPercent ?? progressPercent;
  const previewPercent = scrubPercent ?? hoverPercent;
  const previewTime = useMemo(
    () => (previewPercent === null ? null : resolveTimeAtPercent(previewPercent)),
    [previewPercent, resolveTimeAtPercent],
  );
  const previewIsLiveEdge = previewTime ? isNearLiveWallClock(previewTime, liveSessionAnchor, seekStartTime) : false;
  const isScrubbing = scrubPercent !== null;
  // 拖拽中且预览点已贴近直播边时，读数直接提示"回到直播"，其余给精确到秒的时刻。
  const trackValueText = isScrubbing && previewIsLiveEdge ? t("goLive") : formatWallClock(resolveTimeAtPercent(shownPercent), true);

  return (
    <>
      {currentProgram && (
        <div className="flex min-w-0 items-center justify-between gap-1 text-xs leading-tight tracking-[0.01em] text-violet-50/80 md:gap-2 md:text-sm md:leading-normal md:[@container_video_(max-height:_320px)]:text-xs md:[@container_video_(max-height:_320px)]:leading-tight [@container_video_(max-height:_220px)]:hidden">
          <div className="min-w-0 flex-1 truncate">
            <span className="font-medium text-violet-100">{formatWallClock(windowStart)}</span>
            <span className="mx-1 text-violet-100/30 md:mx-2">|</span>
            <span className="text-white/90">{currentProgram.title || t("excellentProgram")}</span>
          </div>
          <span className="shrink-0 font-medium tabular-nums">{formatWallClock(windowEnd)}</span>
        </div>
      )}

      <div
        ref={trackRef}
        role="slider"
        tabIndex={supportsSeeking ? 0 : -1}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(shownPercent)}
        aria-valuetext={trackValueText}
        aria-label={t("seekTo")}
        className={[
          "player-performance-progress-track group relative h-1.5 touch-none select-none rounded-full bg-violet-50/15 shadow-[inset_0_1px_3px_rgba(0,0,0,0.45)] ring-1 ring-white/10 transition-[height,box-shadow] duration-150 before:absolute before:-inset-y-3 before:inset-x-0 before:content-[''] md:h-2",
          "[@container_video_(max-height:_320px)]:h-1 md:[@container_video_(max-height:_320px)]:h-1",
          supportsSeeking
            ? "cursor-pointer hover:h-2 hover:shadow-[0_0_20px_rgba(var(--pg-rgb),0.16),inset_0_1px_3px_rgba(0,0,0,0.45)] md:hover:h-3"
            : "cursor-default",
          isScrubbing && "h-2 [@container_video_(max-height:_320px)]:h-2 md:h-3 md:[@container_video_(max-height:_320px)]:h-2",
        ].join(" ")}
        onPointerDown={supportsSeeking ? beginScrubbing : undefined}
        onPointerMove={supportsSeeking ? trackPointerMove : undefined}
        onPointerUp={supportsSeeking ? endScrubbing : undefined}
        onPointerCancel={supportsSeeking ? abortScrubbing : undefined}
        onLostPointerCapture={supportsSeeking ? abortScrubbing : undefined}
        onPointerLeave={supportsSeeking ? clearHoverPreview : undefined}
      >
        <div
          className={[
            "player-performance-progress-fill absolute top-0 left-0 h-full rounded-full bg-[linear-gradient(90deg,var(--pg-grad-a)_0%,var(--pg-grad-b)_52%,var(--pg-grad-c)_100%)] shadow-[0_0_18px_rgba(var(--pg-rgb),0.4)]",
            !isScrubbing && "transition-[width] duration-150",
          ].join(" ")}
          style={{ width: `${shownPercent}%` }}
        />

        {supportsSeeking && previewPercent !== null && (
          <>
            <div
              className="absolute top-0 h-full w-0.5 bg-violet-50/80 shadow-[0_0_8px_rgba(var(--pg-rgb-light),0.7)]"
              style={{ left: `${previewPercent}%` }}
            />
            {previewTime && (
              <div
                className={[
                  PLAYER_OVERLAY_SURFACE_CLASS,
                  "absolute bottom-full mb-4 -translate-x-1/2 whitespace-nowrap rounded-lg px-2.5 py-1 text-xs font-medium text-violet-50 md:mb-2",
                ].join(" ")}
                style={{ left: `clamp(2.5rem, ${previewPercent}%, calc(100% - 2.5rem))` }}
              >
                <PlayerSelectedGlassLayers />
                <span className="relative z-10">{previewIsLiveEdge ? t("goLive") : formatWallClock(previewTime, true)}</span>
              </div>
            )}
          </>
        )}

        <div
          className={[
            "absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white bg-violet-300 shadow-[0_0_16px_rgba(var(--pg-rgb-light),0.75)]",
            isScrubbing ? "h-4 w-4" : "h-2.5 w-2.5 transition-[left,width,height] duration-150 md:h-3 md:w-3",
            supportsSeeking && !isScrubbing && "group-hover:h-3 group-hover:w-3 md:group-hover:h-4 md:group-hover:w-4",
          ].join(" ")}
          style={{ left: `${shownPercent}%` }}
        />
      </div>
    </>
  );
});

/** 控制条左侧的时间读数：有节目单显示"已播/总时长"，无节目单显示当前墙钟（带秒）。 */
const PlaybackClock = memo(function PlaybackClock({
  currentProgram,
  seekStartTime,
}: Pick<ControlsProps, "currentProgram" | "seekStartTime">) {
  const mediaSeconds = usePlaybackTime();
  const { windowStart, windowDurationSeconds, elapsedSeconds } = useTimelineWindow(currentProgram, seekStartTime, mediaSeconds);
  return (
    <div className="hidden whitespace-nowrap text-[11px] leading-none text-violet-50/75 tabular-nums min-[360px]:block md:text-sm md:leading-normal">
      {currentProgram ? (
        <span>
          {formatSecondCount(elapsedSeconds)} / {formatSecondCount(windowDurationSeconds)}
        </span>
      ) : (
        <span className="font-medium">{formatWallClock(new Date(windowStart.getTime() + elapsedSeconds * 1000), true)}</span>
      )}
    </div>
  );
});

// 解构顺序按"数据 → 时间轴 → 播放/音量 → seek → 全屏/PiP → 展示开关 → 线路"分组，仅表意；
// 与 ControlsProps 一一对应，顺序不影响行为。
function PlayerControlsView({
  channel,
  currentProgram,
  locale,
  isLive,
  isPlaying,
  liveSessionAnchor,
  seekStartTime,
  mediaInfo,
  renderState,
  onPlayPause,
  volume,
  isMuted,
  onMuteToggle,
  onVolumeChange,
  canControlVolume,
  onSeek,
  onScrubbingChange,
  onFullscreen,
  isFullscreen,
  isPiP = false,
  isPiPSupported = false,
  onPiPToggle,
  showMediaBadges = true,
  showSidebar = true,
  activeSourceIndex = 0,
  onSourceChange,
  docked = false,
}: ControlsProps) {
  const t = usePlayerTranslation(locale);
  // 原生播放模式（浏览器不支持 MSE 转封装）没有解封装产物，媒体徽标必然为空：
  // 明示模式，避免被当成"徽标坏了"，也便于远程报障时一眼定位。
  const nativePlaybackMode = getPlaybackBackendKind() === "native";

  // 换线路菜单（外置条）：手机没有 hover，改为「点击开合 + portal 置顶浮层」——
  // 菜单挂到 body 上用 fixed 定位，不受任何 overflow 裁剪、永远在最前面；
  // 选中线路立即收回，点菜单/触发钮以外的地方也收回。非外置条维持 hover 弹层。
  const [sourceMenuOpen, setSourceMenuOpen] = useState(false);
  const [sourceMenuPos, setSourceMenuPos] = useState<{ left: number; top: number; width: number } | null>(null);
  const sourceTriggerRef = useRef<HTMLButtonElement | null>(null);
  const sourceMenuRef = useRef<HTMLDivElement | null>(null);
  const placeSourceMenu = useCallback(() => {
    const el = sourceTriggerRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const width = Math.min(300, window.innerWidth - 16);
    const left = Math.min(Math.max(rect.left + rect.width / 2, width / 2 + 8), window.innerWidth - width / 2 - 8);
    setSourceMenuPos({ left, top: Math.min(rect.bottom + 6, window.innerHeight - 56), width });
  }, []);
  useEffect(() => {
    if (!sourceMenuOpen) return;
    placeSourceMenu();
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (sourceMenuRef.current?.contains(target) || sourceTriggerRef.current?.contains(target)) return;
      setSourceMenuOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    window.addEventListener("scroll", placeSourceMenu, true);
    window.addEventListener("resize", placeSourceMenu);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      window.removeEventListener("scroll", placeSourceMenu, true);
      window.removeEventListener("resize", placeSourceMenu);
    };
  }, [sourceMenuOpen, placeSourceMenu]);
  const toggleSourceMenu = useCallback(() => {
    setSourceMenuOpen((open) => {
      if (!open) placeSourceMenu();
      return !open;
    });
  }, [placeSourceMenu]);

  // 换线路菜单项：hover 弹层与置顶浮层共用同一份列表，仅「选中后是否收回」不同。
  const renderSourceItems = (onPick: (index: number) => void) =>
    collectSwitchableSources(channel.sources, isLive).map(({ source, index }) => (
      <button
        type="button"
        key={source.url}
        onClick={() => onPick(index)}
        className={[
          "player-performance-motion relative z-10 block w-full cursor-pointer whitespace-nowrap px-3 py-1.5 text-left text-xs transition-colors md:text-sm",
          index === activeSourceIndex ? "bg-violet-300/10 font-medium text-violet-200" : "text-white/75 hover:bg-violet-200/10 hover:text-violet-50",
        ].join(" ")}
      >
        <span className="flex items-center gap-2">
          {!isLive ? <History className="h-3 w-3" /> : <Tv className="h-3 w-3" />}
          {`${t("source")} ${index + 1}`}
        </span>
      </button>
    ));
  const supportsSeeking = channel.sources.some((source) => source.timeshift && source.timeshiftTemplate);
  // 静音按钮的判定含"音量为 0"：此时静音图标更符合听感。
  const isEffectivelyMuted = isMuted || volume <= 0;
  const hasTimeline = supportsSeeking || Boolean(currentProgram);
  const VolumeGlyph = pickVolumeGlyph(isEffectivelyMuted, volume);
  // 滑杆与渐变填充用的是原始 isMuted（而非"等效静音"）：音量为 0 但未静音时，
  // 滑杆仍显示真实音量位置，拖动即可恢复出声。
  const volumeSliderLevel = isMuted ? 0 : volume;

  return (
    <div
      className={[
        "player-performance-controls-background player-performance-effect player-performance-gradient flex w-full flex-col gap-1 bg-[linear-gradient(to_top,rgba(2,8,23,0.98)_0%,rgba(8,22,51,0.9)_46%,rgba(21,27,69,0.48)_72%,transparent_100%)] pt-4 pr-[max(0.375rem,env(safe-area-inset-right))] pb-1 pl-[max(0.375rem,env(safe-area-inset-left))] md:gap-2 md:pt-9 md:pb-3 md:pl-[max(0.75rem,env(safe-area-inset-left))]",
        hasTimeline && "player-performance-controls-with-timeline",
        showSidebar ? "md:pr-3" : "md:pr-[max(0.75rem,env(safe-area-inset-right))]",
        "[@container_video_(max-height:_320px)]:gap-0.5 [@container_video_(max-height:_320px)]:pt-2 [@container_video_(max-height:_320px)]:pb-0.5 md:[@container_video_(max-height:_320px)]:gap-0.5 md:[@container_video_(max-height:_320px)]:pt-2 md:[@container_video_(max-height:_320px)]:pb-0.5 [@container_video_(max-height:_220px)]:pt-1 md:[@container_video_(max-height:_220px)]:pt-1",
      ].join(" ")}
    >
      {hasTimeline && (
        <TimelineScrubber
          channel={channel}
          currentProgram={currentProgram}
          locale={locale}
          liveSessionAnchor={liveSessionAnchor}
          seekStartTime={seekStartTime}
          onSeek={onSeek}
          onScrubbingChange={onScrubbingChange}
        />
      )}

      <div
        className={[
          "flex min-h-10 min-w-0 items-center justify-between gap-0.5 md:min-h-14 md:gap-1",
          "[@container_video_(max-height:_320px)]:min-h-8 md:[@container_video_(max-height:_320px)]:min-h-8",
        ].join(" ")}
      >
        <div className="flex min-w-0 flex-1 items-center gap-0 sm:gap-1 md:gap-3">
          <button
            type="button"
            onClick={onPlayPause}
            className={ROUND_ACTION_BUTTON_CLASS}
            title={isPlaying ? t("pause") : t("play")}
          >
            {isPlaying ? <Pause className={PLAYBACK_ICON_CLASS} /> : <Play className={PLAYBACK_ICON_CLASS} />}
          </button>

          <div className="group/volume relative flex items-center">
            <button
              type="button"
              onClick={onMuteToggle}
              className={ROUND_ACTION_BUTTON_CLASS}
              title={isEffectivelyMuted ? t("unmute") : t("mute")}
            >
              <VolumeGlyph className={PLAYBACK_ICON_CLASS} />
            </button>

            {canControlVolume && (
              <div
                className={[
                  PLAYER_OVERLAY_SURFACE_CLASS,
                  "player-performance-motion invisible absolute left-1/2 flex -translate-x-1/2 cursor-pointer items-center justify-center rounded-xl px-2 py-2 opacity-0 transition-[opacity,visibility] duration-150 group-hover/volume:visible group-hover/volume:opacity-100 group-focus-within/volume:visible group-focus-within/volume:opacity-100 md:px-3",
                  // 外置条（手机原生播放）时控制条挂在视频区下方，向上弹会被画布拦截无法操作，
                  // 改为向下弹出，与换线路菜单方向一致。
                  docked ? "top-full mt-1" : "bottom-full",
                ].join(" ")}
              >
                <PlayerSelectedGlassLayers compact />
                <input
                  type="range"
                  min="0"
                  max="1"
                  step="0.01"
                  value={volumeSliderLevel}
                  onChange={(event) => onVolumeChange(parseFloat(event.target.value))}
                  className="relative z-10 m-0 block h-16 w-1 cursor-pointer appearance-none bg-transparent [writing-mode:vertical-lr] [direction:rtl] md:h-20"
                  style={{
                    background: `linear-gradient(to top, var(--pg-grad-a) 0%, var(--pg-grad-c) ${volumeSliderLevel * 100}%, rgba(var(--pg-rgb),0.18) ${volumeSliderLevel * 100}%, rgba(var(--pg-rgb),0.18) 100%)`,
                  }}
                />
              </div>
            )}
          </div>

          <PlaybackClock currentProgram={currentProgram} seekStartTime={seekStartTime} />

          {showMediaBadges && (
            <div className="ml-1 mr-1 flex min-w-0 basis-0 flex-1 flex-wrap items-center gap-x-1 gap-y-1 md:ml-2 md:mr-2">
              <PlayerMediaBadges mediaInfo={mediaInfo} locale={locale} renderState={renderState} />
              {nativePlaybackMode && (
                <Badge
                  variant="outline"
                  size="compact"
                  className="player-performance-media-badge"
                  title={t("nativePlaybackHint")}
                >
                  {t("nativePlayback")}
                </Badge>
              )}
            </div>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-0 sm:gap-0.5 md:gap-2">
          {isLive ? (
            <span className="flex items-center gap-1 whitespace-nowrap text-[11px] font-semibold tracking-wide text-white md:gap-1.5 md:text-sm">
              <span className="player-performance-motion h-1.5 w-1.5 animate-pulse rounded-full bg-rose-400 shadow-[0_0_12px_rgba(251,113,133,0.85)] md:h-2 md:w-2" />
              {t("live")}
            </span>
          ) : (
            <button
              type="button"
              onClick={() => onSeek(new Date(), true)}
              className={[
                PLAYER_CONTROL_BUTTON_CLASS,
                "cursor-pointer whitespace-nowrap bg-violet-300/10 px-1.5 py-0.5 text-[11px] font-medium text-violet-50 md:px-2.5 md:py-1.5 md:text-sm",
              ].join(" ")}
            >
              {t("goLive")}
            </button>
          )}

          {onSourceChange && channel.sources.length > 1 && (
            <div className="group/source relative flex items-center focus-within:z-10" tabIndex={-1}>
              <button
                type="button"
                ref={docked ? sourceTriggerRef : undefined}
                aria-expanded={docked ? sourceMenuOpen : undefined}
                aria-haspopup={docked ? "menu" : undefined}
                onClick={docked ? toggleSourceMenu : undefined}
                className={[
                  PLAYER_CONTROL_BUTTON_CLASS,
                  "max-w-14 cursor-pointer truncate px-1.5 py-0.5 text-[11px] font-medium min-[360px]:max-w-20 md:max-w-40 md:px-2.5 md:py-1.5 md:text-sm",
                ].join(" ")}
              >
                {/* 触发钮带出线路位次（如"线路 2/3"），与下拉菜单里的逐条线路对应 */}
                {t("source")} {activeSourceIndex + 1}/{channel.sources.length}
              </button>
              {!docked && (
                <div
                  className={[
                    PLAYER_OVERLAY_SURFACE_CLASS,
                    "player-performance-motion invisible absolute bottom-full left-1/2 -translate-x-1/2 overflow-hidden rounded-xl py-1 opacity-0 transition-[opacity,visibility] duration-150 group-hover/source:visible group-hover/source:opacity-100 group-focus-within/source:visible group-focus-within/source:opacity-100",
                  ].join(" ")}
                >
                  <PlayerSelectedGlassLayers />
                  {renderSourceItems((index) => onSourceChange?.(index))}
                </div>
              )}
            </div>
          )}
          {docked &&
            sourceMenuOpen &&
            sourceMenuPos &&
            createPortal(
              <div
                ref={sourceMenuRef}
                role="menu"
                style={{
                  position: "fixed",
                  left: sourceMenuPos.left,
                  top: sourceMenuPos.top,
                  width: sourceMenuPos.width,
                  transform: "translateX(-50%)",
                  zIndex: 9999,
                }}
                className="overflow-hidden rounded-xl border border-violet-200/25 bg-slate-950/95 py-1 shadow-2xl backdrop-blur-md"
              >
                {renderSourceItems((index) => {
                  onSourceChange?.(index);
                  setSourceMenuOpen(false);
                })}
              </div>,
              document.body,
            )}

          <button
            type="button"
            onClick={onFullscreen}
            className={ROUND_ACTION_BUTTON_CLASS}
            title={isFullscreen ? t("exitFullscreen") : t("fullscreen")}
          >
            {isFullscreen ? <Minimize className={PANEL_TOGGLE_ICON_CLASS} /> : <Maximize className={PANEL_TOGGLE_ICON_CLASS} />}
          </button>

          {onPiPToggle && isPiPSupported && !isPiP && (
            <button
              type="button"
              onClick={onPiPToggle}
              className={ROUND_ACTION_BUTTON_CLASS}
              title={t("pictureInPicture")}
            >
              <PictureInPicture className={PANEL_TOGGLE_ICON_CLASS} />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

export const PlayerControls = memo(PlayerControlsView);
