/**
 * 视频播放器整合层（clean-room 重写）。
 * 仅做 UI / 编排：把 segments 交给 PlaybackBackend、把用户操作映射回引擎，并负责
 * 双槽无缝换台、画中画（Document / 传统）、媒体会话（锁屏控制）、触控手势、错误处理与恢复。
 * 引擎契约来自 ../../media-engine 公共 API，本文件不内联任何解码/MSE 逻辑。
 */
import { clsx } from "clsx";
import { CircleAlert, Play, X } from "lucide-react";
import {
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useEffectEvent,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { usePlayerTouchGestures } from "../../hooks/use-player-touch-gestures";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import {
  getDocumentPictureInPicture,
  getDocumentPiPWindowOptions,
  isAnyPictureInPictureActive,
  isDocumentPictureInPictureBlockedError,
  isPictureInPictureSupported,
  setupDocumentPiPWindow,
} from "../../lib/document-picture-in-picture";
import type { Locale } from "../../lib/locale";
import { isVolumeControlSupported } from "../../lib/platform";
import { getMuted, getVolume, saveMuted, saveVolume } from "../../lib/player-storage";
import { createProgramTimeline, programPositionToWallClock } from "../../lib/program-timeline";
import {
  builtinWasmDecoders,
  createPlaybackBackend,
  defaultConfig,
  getPlaybackBackendKind,
  isMSEPlaybackSupported,
  type PlaybackBackend,
  type PlayerError,
  PlayerErrors,
  type PlayerMediaInfo,
  type PlayerRenderState,
  type PlayerSegment,
} from "../../media-engine";
import {
  createLiveSessionAnchor,
  goLiveTargetMse,
  isNearLiveWallClock,
  type LiveSessionAnchor,
  mseToWallClock,
  wallClockToMse,
} from "../../media-engine/timeline";
import type { Channel, EPGProgram } from "../../types/player";
import type { PictureInPictureMode } from "../../types/ui";
import { PLAYER_OVERLAY_SURFACE_CLASS } from "./classnames";
import { PlayerControls } from "./player-controls";
import { PlayerGestureIndicatorOverlay } from "./player-gesture-overlay";
import { PlayerSelectedGlassLayers } from "./player-selected-glass-layers";

interface VideoPlayerProps {
  channel: Channel | null;
  segments: PlayerSegment[];
  playMode: "live" | "catchup";
  onError?: (error: string) => void;
  locale: Locale;
  currentProgram?: EPGProgram | null;
  onSeek?: (seekTime: Date, goingLive: boolean) => void;
  /** Recalibrate MSE t=0 → wall-clock mapping (live mode). */
  onStreamStartTimeChange?: (time: Date) => void;
  streamStartTime: Date;
  onCurrentVideoTimeChange: (time: number) => void;
  onChannelNavigate?: (target: "prev" | "next" | number) => void;
  /** Neighbours of the current channel, used to preview the target of a swipe-to-zap gesture. */
  prevChannel?: Channel | null;
  nextChannel?: Channel | null;
  showSidebar?: boolean;
  onToggleSidebar?: () => void;
  /** 点击播放画面（含单击视频区）：用于"点屏幕出/收侧边栏"。 */
  onSurfaceClick?: () => void;
  isFullscreen: boolean;
  onFullscreenToggle?: () => Promise<boolean> | boolean;
  seamlessSwitch?: boolean;
  autoDeinterlace?: boolean;
  pictureEnhancement?: boolean;
  /** 软解音频（MP2/AC-3）声道输出模式：mono = 左右合成单声道。 */
  audioChannelMode?: "stereo" | "mono";
  pictureInPictureMode?: PictureInPictureMode;
  activeSourceIndex?: number;
  onSourceChange?: (index: number) => void;
  onPlaybackStarted?: () => void;
}

interface PlaybackErrorDisplay {
  message: string;
  description?: string;
  statusCode?: number;
  statusText?: string;
  requestUrl?: string;
  suggestion?: string;
}

const MAX_RETRIES = 3;
const INACTIVE_RENDER_STATE: PlayerRenderState = { active: false, deinterlacing: false };

type SlotId = "a" | "b";

type PendingTransition = { gen: number; slotId: SlotId; player: PlaybackBackend; startedAt: number };

function otherSlot(id: SlotId): SlotId {
  return id === "a" ? "b" : "a";
}

function isInterruptedPlayError(err: unknown): boolean {
  if (!(err instanceof Error)) return false;
  const message = err.message.toLowerCase();
  return err.name === "AbortError" && (message.includes("interrupted") || message.includes("new load request"));
}

function ignoreInterruptedPlayError(err: unknown): void {
  if (!isInterruptedPlayError(err)) throw err;
}

function setMediaSessionAction(
  mediaSession: MediaSession,
  action: MediaSessionAction | "enterpictureinpicture",
  handler: MediaSessionActionHandler | null,
): void {
  try {
    mediaSession.setActionHandler(action as MediaSessionAction, handler);
  } catch {
    // 部分浏览器暴露了 Media Session 但未实现全部 action。
  }
}

function decodeRequestUrl(url: string): string {
  try {
    return decodeURI(url);
  } catch {
    return url;
  }
}

function formatTechnicalPlayerError(playerError: PlayerError): string {
  const parts: string[] = playerError.detail ? [playerError.detail] : [];
  if (playerError.codec) parts.push(`${playerError.track ?? "media"} codec=${playerError.codec}`);
  parts.push(playerError.info ?? "");
  if (playerError.code !== undefined && playerError.code !== -1) parts.push(`code=${playerError.code}`);
  if (playerError.url) parts.push(decodeRequestUrl(playerError.url));
  return parts.filter((value) => value !== undefined && value !== "").join(": ");
}

function getEventDocument(event: Event): Document {
  const target = event.target;
  if (target && "ownerDocument" in target) {
    const ownerDocument = (target as { ownerDocument?: Document | null }).ownerDocument;
    if (ownerDocument) return ownerDocument;
  }
  if (target && "document" in target) {
    const targetDocument = (target as { document?: Document | null }).document;
    if (targetDocument) return targetDocument;
  }
  if (target && "nodeType" in target && (target as { nodeType?: number }).nodeType === 9) {
    return target as Document;
  }
  return document;
}

function isEditableKeyboardTarget(target: EventTarget | null): boolean {
  if (!target || !("tagName" in target)) return false;
  const tagName = String((target as { tagName?: unknown }).tagName).toUpperCase();
  return (
    tagName === "INPUT" ||
    tagName === "TEXTAREA" ||
    tagName === "SELECT" ||
    !!(target as { isContentEditable?: boolean }).isContentEditable
  );
}

function isDocumentBodyActive(targetDocument: Document): boolean {
  const activeElement = targetDocument.activeElement;
  return !activeElement || activeElement === targetDocument.body;
}

function blurActiveElement(targetDocument: Document): void {
  const activeElement = targetDocument.activeElement;
  if (!activeElement || activeElement === targetDocument.body) return;
  const blur = (activeElement as { blur?: () => void }).blur;
  if (blur) blur.call(activeElement);
}

/** 左上角：时钟 + 加载指示（缓动显示以防快加载闪烁）。 */
function PlayerTopLeftOverlay({ visible, loading, loadingText }: { visible: boolean; loading: boolean; loadingText: string }) {
  const [time, setTime] = useState(() => new Date());

  useEffect(() => {
    const tick = () => setTime(new Date());
    tick();
    const msUntilNextMinute = 60_000 - (Date.now() % 60_000);
    let intervalId = 0;
    const timeoutId = window.setTimeout(() => {
      tick();
      intervalId = window.setInterval(tick, 60_000);
    }, msUntilNextMinute);
    return () => {
      window.clearTimeout(timeoutId);
      if (intervalId) window.clearInterval(intervalId);
    };
  }, []);

  return (
    <div
      className={clsx(
        PLAYER_OVERLAY_SURFACE_CLASS,
        "player-performance-motion absolute top-4 left-4 z-10 max-w-[calc(100%-2rem)] rounded-xl px-2 py-1.5 transition-opacity duration-300 md:top-8 md:left-8 md:px-3 md:py-2 [@container_video_(max-height:_320px)]:top-2 [@container_video_(max-height:_320px)]:left-2 [@container_video_(max-height:_320px)]:rounded-lg [@container_video_(max-height:_320px)]:px-1.5 [@container_video_(max-height:_320px)]:py-1 md:[@container_video_(max-height:_320px)]:top-2 md:[@container_video_(max-height:_320px)]:left-2 md:[@container_video_(max-height:_320px)]:px-1.5 md:[@container_video_(max-height:_320px)]:py-1 [@container_video_(max-height:_220px)]:top-1 [@container_video_(max-height:_220px)]:left-1 md:[@container_video_(max-height:_220px)]:top-1 md:[@container_video_(max-height:_220px)]:left-1",
        visible ? "opacity-100" : "opacity-0 pointer-events-none",
      )}
    >
      <PlayerSelectedGlassLayers />
      <div className="relative z-10 flex min-w-0 items-center gap-1.5 md:gap-2 [@container_video_(max-height:_320px)]:gap-1 md:[@container_video_(max-height:_320px)]:gap-1">
        <span className="shrink-0 font-medium text-xs text-violet-50 tabular-nums drop-shadow-sm md:text-base md:[@container_video_(max-height:_320px)]:text-xs">
          {time.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
        </span>
        {loading && (
          <>
            <span className="shrink-0 text-violet-100/35 text-xs md:text-sm md:[@container_video_(max-height:_320px)]:text-xs" aria-hidden="true">
              ·
            </span>
            <div className="relative h-3 w-3 shrink-0 md:h-3.5 md:w-3.5 md:[@container_video_(max-height:_320px)]:h-3 md:[@container_video_(max-height:_320px)]:w-3">
              <div className="absolute inset-0 rounded-full border border-violet-100/25" />
              <div className="player-performance-loading-spinner absolute inset-0 animate-spin rounded-full border border-violet-200 border-t-transparent shadow-[0_0_8px_rgba(var(--pg-rgb-light),0.5)]" />
            </div>
            <span className="min-w-0 truncate text-violet-50/70 text-xs md:text-sm md:[@container_video_(max-height:_320px)]:text-xs">
              {loadingText}
            </span>
          </>
        )}
      </div>
    </div>
  );
}

function VideoPlayerComponent({
  channel,
  segments,
  onError,
  locale,
  playMode,
  currentProgram = null,
  onSeek,
  onStreamStartTimeChange,
  streamStartTime,
  onCurrentVideoTimeChange,
  onChannelNavigate,
  prevChannel = null,
  nextChannel = null,
  showSidebar = false,
  onToggleSidebar,
  onSurfaceClick,
  isFullscreen,
  onFullscreenToggle,
  seamlessSwitch = true,
  autoDeinterlace = true,
  pictureEnhancement = true,
  audioChannelMode = "stereo",
  pictureInPictureMode = "document",
  activeSourceIndex = 0,
  onSourceChange,
  onPlaybackStarted,
}: VideoPlayerProps) {
  const t = usePlayerTranslation(locale);
  const playbackBackendKind = getPlaybackBackendKind();
  const currentVideoTimeRef = useRef(0);
  const isCatchupSupported = Boolean(channel?.sources.some((source) => source.catchup && source.catchupSource));
  const canSeekProgramInMediaSession = Boolean(currentProgram) && isCatchupSupported;
  const canControlVolume = isVolumeControlSupported();
  const canNavigateChannelsInMediaSession = Boolean(channel && onChannelNavigate);

  const playerDockRef = useRef<HTMLDivElement>(null);
  const playerSurfaceRef = useRef<HTMLDivElement>(null);
  const documentPiPWindowRef = useRef<Window | null>(null);
  const isUnmountingRef = useRef(false);
  const [playerPortalHost] = useState(() => {
    const host = document.createElement("div");
    host.style.display = "contents";
    return host;
  });
  const slotAVideoRef = useRef<HTMLVideoElement>(null);
  const slotBVideoRef = useRef<HTMLVideoElement>(null);
  const slotACanvasRef = useRef<HTMLCanvasElement>(null);
  const slotBCanvasRef = useRef<HTMLCanvasElement>(null);
  const slotAPlayerRef = useRef<PlaybackBackend | null>(null);
  const slotBPlayerRef = useRef<PlaybackBackend | null>(null);
  const activeSlotIdRef = useRef<SlotId>("a");
  const [visibleSlotId, setVisibleSlotId] = useState<SlotId>("a");
  const [slotMediaInfo, setSlotMediaInfo] = useState<Record<SlotId, PlayerMediaInfo | null>>({ a: null, b: null });
  const transitionGenRef = useRef(0);
  const pendingTransitionRef = useRef<PendingTransition | null>(null);
  const hasStartedPlaybackRef = useRef(false);
  const prevStreamRef = useRef<{ channelId: string; sourceIndex: number } | null>(null);
  const skipNextSegmentsLoadRef = useRef(false);

  const slotVideoRef = (id: SlotId) => (id === "a" ? slotAVideoRef : slotBVideoRef);
  const slotCanvasRef = (id: SlotId) => (id === "a" ? slotACanvasRef : slotBCanvasRef);
  const slotPlayerRef = (id: SlotId) => (id === "a" ? slotAPlayerRef : slotBPlayerRef);

  const [slotRenderStates, setSlotRenderStates] = useState<Record<SlotId, PlayerRenderState>>({
    a: INACTIVE_RENDER_STATE,
    b: INACTIVE_RENDER_STATE,
  });
  const setSlotRenderState = (slotId: SlotId, renderState: PlayerRenderState) =>
    setSlotRenderStates((previous) =>
      previous[slotId].active === renderState.active && previous[slotId].deinterlacing === renderState.deinterlacing
        ? previous
        : { ...previous, [slotId]: renderState },
    );
  const renderActiveSlots = { a: slotRenderStates.a.active, b: slotRenderStates.b.active };

  const getActiveSlotId = () => activeSlotIdRef.current;
  const getActiveVideo = () => slotVideoRef(getActiveSlotId()).current;
  const getActivePlayer = () => slotPlayerRef(getActiveSlotId()).current;

  const [isLoading, setIsLoading] = useState(false);
  const [showLoading, setShowLoading] = useState(false);
  const loadingTimeoutRef = useRef<number>(0);
  const [error, setError] = useState<PlaybackErrorDisplay | null>(() =>
    playbackBackendKind === "native" || isMSEPlaybackSupported() ? null : { message: t("mseNotSupported") },
  );
  const [warning, setWarning] = useState<PlaybackErrorDisplay | null>(null);
  const [volume, setVolume] = useState(() => getVolume());
  const [isMuted, setIsMuted] = useState(() => getMuted());
  const [isPlaying, setIsPlaying] = useState(false);
  const [liveSessionAnchor, setLiveSessionAnchor] = useState<LiveSessionAnchor | null>(null);
  const isLive = playMode === "live";
  const [needsUserInteraction, setNeedsUserInteraction] = useState(false);
  const [showControls, setShowControls] = useState(true);
  const [isPiP, setIsPiP] = useState(false);
  const [isDocumentPiP, setIsDocumentPiP] = useState(false);
  const hideControlsTimeoutRef = useRef<number>(0);
  const [retryCount, setRetryCount] = useState(0);
  const [retryBaseline, setRetryBaseline] = useState(0);
  const isRetrySeekRef = useRef(false);
  const stablePlaybackTimeoutRef = useRef<number>(0);
  const shouldAutoPlayRef = useRef(true);
  const userPausedRef = useRef(false);
  const wallClockCalibratedRef = useRef(false);
  const mediaSessionPositionUpdatedAtRef = useRef(0);

  const [digitBuffer, setDigitBuffer] = useState("");
  const digitTimeoutRef = useRef<number>(0);

  useEffect(() => {
    if (isLoading) {
      loadingTimeoutRef.current = window.setTimeout(() => setShowLoading(true), 500);
    } else {
      setShowLoading(false);
    }
    return () => {
      if (loadingTimeoutRef.current) {
        window.clearTimeout(loadingTimeoutRef.current);
        loadingTimeoutRef.current = 0;
      }
    };
  }, [isLoading]);

  const handleRelativeSeek = useEffectEvent((deltaSeconds: number) => {
    if (!isCatchupSupported) return;
    const activePlayer = getActivePlayer();
    if (!activePlayer) return;
    const state = activePlayer.getState();
    shouldAutoPlayRef.current = !state.paused;
    if (playMode === "live") activePlayer.setLiveSync(false);
    activePlayer.seek(state.currentTime + deltaSeconds);
  });

  const calibrateLiveSession = useEffectEvent((player: PlaybackBackend) => {
    const currentTime = player.getState().currentTime;
    const origin = new Date(Date.now() - currentTime * 1000);
    const anchor = createLiveSessionAnchor(currentTime);
    setLiveSessionAnchor(anchor);
    onStreamStartTimeChange?.(origin);
    getActivePlayer()?.setLiveSessionAnchor(anchor);
  });

  const seekLiveByWallClock = useEffectEvent((seekTime: Date) => {
    const targetMse = wallClockToMse(seekTime, streamStartTime);
    getActivePlayer()?.seek(targetMse);
  });

  const goLiveToSessionEdge = useEffectEvent(() => {
    if (!liveSessionAnchor) return;
    const targetMse = goLiveTargetMse(liveSessionAnchor, defaultConfig.liveSyncTargetLatency);
    getActivePlayer()?.goLive(targetMse);
    getActivePlayer()?.setLiveSync(true);
  });

  const isNearLiveEdge = useEffectEvent((seekTime: Date): boolean => isNearLiveWallClock(seekTime, liveSessionAnchor, streamStartTime));

  const handleSeek = useEffectEvent((seekTime: Date, goingLiveHint?: boolean) => {
    const activePlayer = getActivePlayer();
    if (!activePlayer) return;
    const goingLive = goingLiveHint ?? isNearLiveEdge(seekTime);

    if (goingLive) {
      userPausedRef.current = false;
      if (playMode === "live") {
        goLiveToSessionEdge();
        activePlayer.play().catch(ignoreInterruptedPlayError);
        return;
      }
      shouldAutoPlayRef.current = !activePlayer.getState().paused;
      onSeek?.(new Date(), true);
      return;
    }

    shouldAutoPlayRef.current = !activePlayer.getState().paused;
    if (playMode === "live") {
      activePlayer.setLiveSync(false);
      seekLiveByWallClock(seekTime);
      return;
    }

    const seekSeconds = (seekTime.getTime() - streamStartTime.getTime()) / 1000;
    if (seekSeconds >= 0) activePlayer.seek(seekSeconds);
    else onSeek?.(seekTime, false);
  });

  const togglePlayPause = useEffectEvent(() => {
    const player = getActivePlayer();
    if (!player) return;
    if (player.getState().paused) {
      userPausedRef.current = false;
      player.play().catch(ignoreInterruptedPlayError);
    } else {
      userPausedRef.current = true;
      player.pause();
    }
  });

  const resetControlsTimer = useCallback(() => {
    if (hideControlsTimeoutRef.current) window.clearTimeout(hideControlsTimeoutRef.current);
    hideControlsTimeoutRef.current = window.setTimeout(() => setShowControls(false), 3000);
  }, []);

  const showControlsImmediately = useCallback(() => {
    setShowControls(true);
    resetControlsTimer();
  }, [resetControlsTimer]);

  const handleScrubbingChange = useCallback(
    (isScrubbing: boolean) => {
      if (hideControlsTimeoutRef.current) {
        window.clearTimeout(hideControlsTimeoutRef.current);
        hideControlsTimeoutRef.current = 0;
      }
      setShowControls(true);
      if (!isScrubbing) resetControlsTimer();
    },
    [resetControlsTimer],
  );

  const hideControlsImmediately = useCallback(() => {
    if (hideControlsTimeoutRef.current) {
      window.clearTimeout(hideControlsTimeoutRef.current);
      hideControlsTimeoutRef.current = 0;
    }
    setShowControls(false);
  }, []);

  // 指针悬停（鼠标 / 笔）：显示控件并重置 3s 空闲计时；离开则隐藏。触控无悬停，交给点击处理。
  const handlePointerHover = useCallback(
    (event: ReactPointerEvent) => {
      if (event.pointerType === "touch") return;
      showControlsImmediately();
    },
    [showControlsImmediately],
  );

  const handlePointerLeave = useCallback(
    (event: ReactPointerEvent) => {
      if (event.pointerType === "touch") return;
      hideControlsImmediately();
    },
    [hideControlsImmediately],
  );

  useEffect(() => {
    resetControlsTimer();
    return () => {
      if (hideControlsTimeoutRef.current) window.clearTimeout(hideControlsTimeoutRef.current);
    };
  }, [resetControlsTimer]);

  useLayoutEffect(() => {
    if (isDocumentPiP) return;
    const dock = playerDockRef.current;
    if (!dock) return;
    dock.append(playerPortalHost);
    return () => {
      if (playerPortalHost.parentNode === dock) dock.removeChild(playerPortalHost);
    };
  }, [isDocumentPiP, playerPortalHost]);

  const restoreDocumentPiPPlayer = useEffectEvent(() => {
    if (isUnmountingRef.current) return;
    const dock = playerDockRef.current;
    if (dock && playerPortalHost.parentNode !== dock) dock.append(playerPortalHost);
    documentPiPWindowRef.current = null;
    setIsDocumentPiP(false);
    setIsPiP(Boolean(document.pictureInPictureElement));
  });

  useEffect(() => {
    return () => {
      isUnmountingRef.current = true;
      const pipWindow = documentPiPWindowRef.current;
      documentPiPWindowRef.current = null;
      pipWindow?.close();
      if (playerPortalHost.parentNode) playerPortalHost.parentNode.removeChild(playerPortalHost);
    };
  }, [playerPortalHost]);

  const cancelPendingTransition = useEffectEvent(() => {
    pendingTransitionRef.current = null;
  });

  const applyPlayerSettings = useEffectEvent((player: PlaybackBackend) => {
    player.setLiveSync(playMode === "live");
    if (liveSessionAnchor && wallClockCalibratedRef.current) player.setLiveSessionAnchor(liveSessionAnchor);
  });

  const destroySlot = useEffectEvent((slotId: SlotId) => {
    slotPlayerRef(slotId).current?.destroy();
    slotPlayerRef(slotId).current = null;
    setSlotRenderState(slotId, INACTIVE_RENDER_STATE);
  });

  const stopPendingTransition = useEffectEvent(() => {
    const pending = pendingTransitionRef.current;
    if (!pending) return;
    slotPlayerRef(pending.slotId).current?.stop();
    cancelPendingTransition();
  });

  const stopSlotIfPlayerStillMatches = useEffectEvent((slotId: SlotId, player: PlaybackBackend) => {
    if (slotPlayerRef(slotId).current !== player) return;
    player.stop();
  });

  const completeTransition = useEffectEvent((newActiveId: SlotId) => {
    const oldActiveId = getActiveSlotId();
    const oldPlayer = slotPlayerRef(oldActiveId).current;
    const oldState = oldPlayer?.getState();
    const savedVolume = oldState?.volume ?? volume;
    const savedMuted = oldState?.muted ?? isMuted;

    const newPlayer = slotPlayerRef(newActiveId).current;
    if (newPlayer) {
      newPlayer.setVolume(savedVolume);
      newPlayer.setMuted(savedMuted);
      applyPlayerSettings(newPlayer);
    }

    activeSlotIdRef.current = newActiveId;
    setVisibleSlotId(newActiveId);
    setIsLoading(false);

    if (oldActiveId !== newActiveId && oldPlayer) stopSlotIfPlayerStillMatches(oldActiveId, oldPlayer);
  });

  const completePendingSwitchIfNeeded = useEffectEvent(
    (slotId: SlotId, eventTimeStamp?: number, expected?: Pick<PendingTransition, "gen" | "player">): boolean => {
      const pending = pendingTransitionRef.current;
      if (!pending || pending.slotId !== slotId) return false;
      if (pending.gen !== transitionGenRef.current) return false;
      if (slotPlayerRef(slotId).current !== pending.player) return false;
      if (expected && (pending.gen !== expected.gen || pending.player !== expected.player)) return false;
      if (eventTimeStamp !== undefined && eventTimeStamp < pending.startedAt) return false;
      cancelPendingTransition();
      completeTransition(slotId);
      return true;
    },
  );

  const isPendingTransitionExpected = useEffectEvent(
    (slotId: SlotId, expected?: Pick<PendingTransition, "gen" | "player">): boolean => {
      if (!expected) return true;
      const pending = pendingTransitionRef.current;
      return (
        pending?.slotId === slotId &&
        pending.gen === expected.gen &&
        pending.player === expected.player &&
        slotPlayerRef(slotId).current === expected.player
      );
    },
  );

  const currentPendingTransition = useEffectEvent((slotId: SlotId): PendingTransition | undefined => {
    const pending = pendingTransitionRef.current;
    return pending?.slotId === slotId ? pending : undefined;
  });

  const fallbackPendingSwitchToHardSwitch = useEffectEvent(
    (slotId: SlotId, eventTimeStamp?: number, expected?: Pick<PendingTransition, "gen" | "player">): boolean => {
      const pending = pendingTransitionRef.current;
      if (!pending || pending.slotId !== slotId) return false;
      if (pending.gen !== transitionGenRef.current) return false;
      if (slotPlayerRef(slotId).current !== pending.player) return false;
      if (expected && (pending.gen !== expected.gen || pending.player !== expected.player)) return false;
      if (eventTimeStamp !== undefined && eventTimeStamp < pending.startedAt) return false;
      pending.player.stop();
      cancelPendingTransition();
      handleLoadSegments(segments, true);
      return true;
    },
  );

  const getRetrySegments = useEffectEvent((): PlayerSegment[] => segments);

  const runPlayerErrorRecovery = useEffectEvent((playerError: PlayerError, slotId: SlotId) => {
    console.error("Player error:", JSON.stringify(playerError));
    setWarning(null);

    const isPendingTransition = pendingTransitionRef.current?.slotId === slotId;
    if (isPendingTransition) {
      fallbackPendingSwitchToHardSwitch(slotId);
      return;
    }

    const technicalErrorMessage = formatTechnicalPlayerError(playerError);
    let errorMessage = technicalErrorMessage || t("playbackError");
    let errorDisplay: PlaybackErrorDisplay = { message: errorMessage };
    let decodingErrorRetry = false;
    const isHttpStatusError = playerError.category === "io" && playerError.detail === PlayerErrors.HTTP_STATUS_CODE_INVALID;
    const isUpstreamRequestError =
      isHttpStatusError || (playerError.category === "io" && playerError.detail === PlayerErrors.REQUEST_FAILED);
    const isCodecUnsupported = playerError.detail === PlayerErrors.CODEC_UNSUPPORTED;

    if (playerError.category === "media") {
      if (playerError.detail === PlayerErrors.MEDIA_MSE_ERROR) {
        const video = slotVideoRef(slotId).current;
        if (playerError.info?.includes("HTMLMediaElement.error")) {
          if (video?.error?.message?.includes("PIPELINE_ERROR_DECODE")) decodingErrorRetry = true;
          if (video?.error?.message && !errorMessage.includes(video.error.message)) errorMessage += `: ${video.error.message}`;
        }
      }
    } else if (playerError.category === "io") {
      if (isUpstreamRequestError) {
        const status = [playerError.code, playerError.info]
          .filter((value) => value !== undefined && value !== "" && value !== -1)
          .join(" ");
        errorMessage = `${t("upstreamRequestFailed")}${
          isHttpStatusError && status ? `: HTTP ${status}` : ""
        }${playerError.url ? ` (${playerError.url})` : ""}`;
        errorDisplay = {
          message: t("upstreamRequestFailed"),
          description: t("upstreamRequestFailedDescription"),
          statusCode: isHttpStatusError ? playerError.code : undefined,
          statusText: isHttpStatusError ? playerError.info : undefined,
          requestUrl: playerError.url ? decodeRequestUrl(playerError.url) : undefined,
          suggestion: t("upstreamRequestFailedSuggestion"),
        };
      }
    }

    if (isCodecUnsupported) {
      errorMessage = t("codecError");
      errorDisplay = { message: errorMessage, description: technicalErrorMessage };
    } else if (!isUpstreamRequestError) {
      errorDisplay = { message: errorMessage };
    }

    if (retryCount < retryBaseline + MAX_RETRIES) {
      setRetryCount(retryCount + 1);
      if (decodingErrorRetry) setRetryBaseline(retryBaseline + 1);
      isRetrySeekRef.current = true;
      if (onSeek) {
        if (playMode === "live") onSeek(new Date(), true);
        else onSeek(mseToWallClock(currentVideoTimeRef.current, streamStartTime), false);
      }
      if (playMode === "catchup") skipNextSegmentsLoadRef.current = true;
      scheduleRetryReload(getRetrySegments());
      return;
    }

    if (channel && onSourceChange && activeSourceIndex + 1 < channel.sources.length) {
      onSourceChange(activeSourceIndex + 1);
      return;
    }

    setError(errorDisplay);
    onError?.(errorMessage);
    setIsLoading(false);
  });

  const handlePlayerError = useEffectEvent((playerError: PlayerError, slotId: SlotId) => {
    // 单轨编码不受支持（音频轨 AC-3/MP2，或视频轨 HEVC/4K）：非阻断告警，继续播另一轨。
    // 关键：**不能**走 runPlayerErrorRecovery —— 那会弹错误面板并反复重载。
    if (playerError.detail === PlayerErrors.CODEC_UNSUPPORTED) {
      console.error("Player codec warning:", JSON.stringify(playerError));
      setWarning({
        message: playerError.track === "video" ? t("videoCodecError") : t("audioCodecError"),
        description: formatTechnicalPlayerError(playerError),
      });
      return;
    }
    runPlayerErrorRecovery(playerError, slotId);
  });

  const [prevSegments, setPrevSegments] = useState(segments);
  if (segments !== prevSegments) {
    setPrevSegments(segments);
    currentVideoTimeRef.current = 0;
    wallClockCalibratedRef.current = false;
    setLiveSessionAnchor(null);

    const isStreamChange =
      channel != null &&
      prevStreamRef.current != null &&
      (channel.id !== prevStreamRef.current.channelId || activeSourceIndex !== prevStreamRef.current.sourceIndex);

    if (isRetrySeekRef.current && !isStreamChange) isRetrySeekRef.current = false;
    else {
      setRetryCount(0);
      setRetryBaseline(0);
      isRetrySeekRef.current = false;
    }
  }

  const handleSeekNeeded = useEffectEvent((seconds: number) => {
    const player = getActivePlayer();
    shouldAutoPlayRef.current = !player?.getState().paused;
    const seekTime = mseToWallClock(seconds, streamStartTime);
    onSeek?.(seekTime, isNearLiveWallClock(seekTime, liveSessionAnchor, streamStartTime));
  });

  const handleAudioSuspended = useEffectEvent(() => setNeedsUserInteraction(true));

  const createPlayerForSlot = useEffectEvent((slotId: SlotId): PlaybackBackend | null => {
    const video = slotVideoRef(slotId).current;
    if (!video || (playbackBackendKind === "mse" && !isMSEPlaybackSupported())) return null;

    const existing = slotPlayerRef(slotId).current;
    if (existing) {
      if (existing.kind !== playbackBackendKind) {
        existing.destroy();
        slotPlayerRef(slotId).current = null;
      } else {
        return existing;
      }
    }

    const player = createPlaybackBackend(video, {
      wasmDecoders: builtinWasmDecoders,
      renderCanvas: slotCanvasRef(slotId).current ?? undefined,
      autoDeinterlace,
      pictureEnhancement,
      audioChannelMode,
    });
    player.setVolume(volume);
    player.setMuted(isMuted);
    player.on("error", (e) => {
      if (slotPlayerRef(slotId).current === player) handlePlayerError(e, slotId);
    });
    player.on("seek-needed", (seconds) => {
      if (slotPlayerRef(slotId).current === player) handleSeekNeeded(seconds);
    });
    player.on("live-state-change", (live) => {
      if (slotPlayerRef(slotId).current !== player) return;
      if (slotId === getActiveSlotId() && !live && player.getState().paused && playMode === "live") player.setLiveSync(false);
    });
    player.on("audio-suspended", () => {
      if (slotPlayerRef(slotId).current === player) handleAudioSuspended();
    });
    player.on("render-state-change", (renderState) => {
      if (slotPlayerRef(slotId).current === player) setSlotRenderState(slotId, renderState);
    });
    player.on("media-info", (mediaInfo) => {
      if (slotPlayerRef(slotId).current === player)
        setSlotMediaInfo((previous) => ({ ...previous, [slotId]: mediaInfo }));
    });
    player.on("time-update", (time) => {
      if (slotPlayerRef(slotId).current !== player || slotId !== getActiveSlotId()) return;
      currentVideoTimeRef.current = time;
      onCurrentVideoTimeChange(time);
      updateMediaSessionPosition();
    });
    player.on("ended", () => {
      if (slotPlayerRef(slotId).current === player && slotId === getActiveSlotId()) handlePlaybackEnded();
    });
    player.on("playback-state-change", (state, eventTimeStamp) => {
      if (slotPlayerRef(slotId).current !== player) return;
      if (state === "canplay") handleVideoCanPlay(slotId);
      if (state === "waiting") handleVideoWaiting(slotId);
      if (state === "playing") handleVideoPlaying(slotId, eventTimeStamp);
      if (state === "paused") handleVideoPause(slotId);
    });
    player.on("volume-change", (nextVolume, nextMuted) => {
      if (slotPlayerRef(slotId).current !== player || slotId !== getActiveSlotId()) return;
      setVolume(nextVolume);
      setIsMuted(nextMuted);
      saveVolume(nextVolume);
      saveMuted(nextMuted);
    });
    applyPlayerSettings(player);
    slotPlayerRef(slotId).current = player;
    return player;
  });

  const playVideoWithAutoplayFallback = useEffectEvent(
    (slotId?: SlotId, expected?: Pick<PendingTransition, "gen" | "player">) => {
      userPausedRef.current = false;
      const player = slotId ? slotPlayerRef(slotId).current : getActivePlayer();
      if (!player) return;
      const playPromise = player.play();
      if (playPromise) {
        playPromise
          .catch((err: Error) => {
            if (isInterruptedPlayError(err)) return;
            if (slotId && !isPendingTransitionExpected(slotId, expected)) return;
            if (slotId) {
              fallbackPendingSwitchToHardSwitch(slotId, undefined, expected);
            } else if (err.name === "NotAllowedError" || err.message.includes("user didn't interact")) {
              setNeedsUserInteraction(true);
            }
          })
          .finally(() => {
            if (!slotId || slotId === getActiveSlotId() || isPendingTransitionExpected(slotId, expected)) setIsLoading(false);
          });
      }
    },
  );

  const loadActiveSlotSegments = useEffectEvent((player: PlaybackBackend, slotId: SlotId, newSegments: PlayerSegment[]) => {
    setSlotMediaInfo((previous) => ({ ...previous, [slotId]: null }));
    player.loadSegments(newSegments);
    if (shouldAutoPlayRef.current) playVideoWithAutoplayFallback();
    else setIsLoading(false);
  });

  const updateMediaSessionPosition = useEffectEvent((force = false) => {
    if (!("mediaSession" in navigator) || !navigator.mediaSession.setPositionState) return;
    if (!channel) {
      navigator.mediaSession.setPositionState();
      return;
    }
    const now = Date.now();
    if (!force && now - mediaSessionPositionUpdatedAtRef.current < 1000) return;
    const player = getActivePlayer();
    if (!player) return;
    const state = player.getState();
    mediaSessionPositionUpdatedAtRef.current = now;

    const timeline = currentProgram ? createProgramTimeline(currentProgram, streamStartTime, state.currentTime) : null;
    const supportsCatchup = channel.sources.some((source) => source.catchup && source.catchupSource);

    try {
      if (timeline && supportsCatchup) {
        navigator.mediaSession.setPositionState({
          duration: timeline.durationSeconds,
          position: timeline.positionSeconds,
          playbackRate: state.playbackRate,
        });
      } else {
        navigator.mediaSession.setPositionState({
          duration: Infinity,
          position: Math.max(0, state.currentTime),
          playbackRate: state.playbackRate,
        });
      }
    } catch {
      navigator.mediaSession.setPositionState();
    }
  });

  const handleMediaSessionPlay = useEffectEvent(() => {
    userPausedRef.current = false;
    getActivePlayer()?.play().catch(ignoreInterruptedPlayError);
  });

  const handleMediaSessionPause = useEffectEvent(() => {
    userPausedRef.current = true;
    getActivePlayer()?.pause();
  });

  const handleMediaSessionSeekBackward = useEffectEvent((details: MediaSessionActionDetails) =>
    handleRelativeSeek(-(details.seekOffset ?? 5)),
  );

  const handleMediaSessionSeekForward = useEffectEvent((details: MediaSessionActionDetails) =>
    handleRelativeSeek(details.seekOffset ?? 5),
  );

  const handleMediaSessionSeekTo = useEffectEvent((details: MediaSessionActionDetails) => {
    if (!currentProgram || details.seekTime === undefined) return;
    const programTimeline = createProgramTimeline(currentProgram, streamStartTime, currentVideoTimeRef.current);
    if (!programTimeline) return;
    handleSeek(programPositionToWallClock(programTimeline, details.seekTime));
  });

  const handleMediaSessionPreviousTrack = useEffectEvent(() => onChannelNavigate?.("prev"));
  const handleMediaSessionNextTrack = useEffectEvent(() => onChannelNavigate?.("next"));

  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    if (!channel) {
      navigator.mediaSession.metadata = null;
      return;
    }
    const groupLabel = channel.groups.join(" / ");
    navigator.mediaSession.metadata = new MediaMetadata({
      title: currentProgram?.title || channel.name,
      artist: currentProgram?.title ? channel.name : groupLabel,
      artwork: channel.logo ? [{ src: channel.logo }] : [],
    });
  }, [channel, currentProgram]);

  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    navigator.mediaSession.playbackState = channel ? (isPlaying ? "playing" : "paused") : "none";
  }, [channel, isPlaying]);

  useEffect(() => {
    updateMediaSessionPosition(true);
    // biome-ignore lint/correctness/useExhaustiveDependencies: 时间线/槽状态变化时立即同步媒体会话
  }, [channel, currentProgram, playMode, activeSourceIndex, visibleSlotId, isPlaying]);

  useEffect(
    () => () => {
      if (!("mediaSession" in navigator) || !navigator.mediaSession.setPositionState) return;
      navigator.mediaSession.metadata = null;
      navigator.mediaSession.playbackState = "none";
      navigator.mediaSession.setPositionState();
    },
    [],
  );

  const handleLoadSegments = useEffectEvent((newSegments: PlayerSegment[], forceHardSwitch = false) => {
    if (!newSegments.length) return;

    const activeId = getActiveSlotId();
    const activePlayer = slotPlayerRef(activeId).current ?? createPlayerForSlot(activeId);
    if (!activePlayer) return;

    if (stablePlaybackTimeoutRef.current) {
      window.clearTimeout(stablePlaybackTimeoutRef.current);
      stablePlaybackTimeoutRef.current = 0;
    }

    showControlsImmediately();
    setIsLoading(true);
    setError(null);
    setWarning(null);

    const isStreamSwitch =
      channel != null &&
      prevStreamRef.current != null &&
      (channel.id !== prevStreamRef.current.channelId || activeSourceIndex !== prevStreamRef.current.sourceIndex);
    const activeVideo = slotVideoRef(activeId).current;
    const activeState = activePlayer.getState();
    const useSeamlessSwitch =
      !forceHardSwitch &&
      seamlessSwitch &&
      !isAnyPictureInPictureActive() &&
      hasStartedPlaybackRef.current &&
      isStreamSwitch &&
      playMode === "live" &&
      shouldAutoPlayRef.current &&
      !activeState.paused;

    if (channel) prevStreamRef.current = { channelId: channel.id, sourceIndex: activeSourceIndex };

    if (!useSeamlessSwitch) {
      stopPendingTransition();
      loadActiveSlotSegments(activePlayer, activeId, newSegments);
      return;
    }

    cancelPendingTransition();
    transitionGenRef.current++;
    const gen = transitionGenRef.current;

    const pendingId = otherSlot(activeId);
    slotPlayerRef(pendingId).current?.stop();

    const pendingPlayer = createPlayerForSlot(pendingId);
    const pendingVideo = slotVideoRef(pendingId).current;
    if (!pendingPlayer || !pendingVideo) {
      loadActiveSlotSegments(activePlayer, activeId, newSegments);
      return;
    }

    if (activeVideo) {
      pendingPlayer.setVolume(activeState.volume);
      pendingPlayer.setMuted(true);
    }

    const pendingTransition = { gen, slotId: pendingId, player: pendingPlayer, startedAt: performance.now() };
    pendingTransitionRef.current = pendingTransition;
    setSlotMediaInfo((previous) => ({ ...previous, [pendingId]: null }));
    pendingPlayer.loadSegments(newSegments);

    if (shouldAutoPlayRef.current) playVideoWithAutoplayFallback(pendingId, pendingTransition);
    else setIsLoading(false);
  });

  const scheduleRetryReload = useEffectEvent((newSegments: PlayerSegment[]) => handleLoadSegments(newSegments));

  useEffect(() => {
    return () => {
      cancelPendingTransition();
      destroySlot("a");
      destroySlot("b");
    };
  }, []);

  useEffect(() => {
    slotAPlayerRef.current?.setAutoDeinterlace(autoDeinterlace);
    slotBPlayerRef.current?.setAutoDeinterlace(autoDeinterlace);
  }, [autoDeinterlace]);

  useEffect(() => {
    slotAPlayerRef.current?.setPictureEnhancement(pictureEnhancement);
    slotBPlayerRef.current?.setPictureEnhancement(pictureEnhancement);
  }, [pictureEnhancement]);

  // 运行时切换声道模式（无需重建播放器）
  useEffect(() => {
    slotAPlayerRef.current?.setAudioChannelMode(audioChannelMode);
    slotBPlayerRef.current?.setAudioChannelMode(audioChannelMode);
  }, [audioChannelMode]);

  useEffect(() => {
    if (!seamlessSwitch) stopPendingTransition();
  }, [seamlessSwitch]);

  useEffect(() => {
    const liveSync = playMode === "live";
    slotAPlayerRef.current?.setLiveSync(liveSync);
    slotBPlayerRef.current?.setLiveSync(liveSync);
  }, [playMode]);

  useEffect(() => {
    if (!liveSessionAnchor) return;
    slotAPlayerRef.current?.setLiveSessionAnchor(liveSessionAnchor);
    slotBPlayerRef.current?.setLiveSessionAnchor(liveSessionAnchor);
  }, [liveSessionAnchor]);

  useEffect(() => {
    if (skipNextSegmentsLoadRef.current) {
      skipNextSegmentsLoadRef.current = false;
      return;
    }
    handleLoadSegments(segments);
  }, [segments]);

  const prevPlayModeRef = useRef(playMode);
  useEffect(() => {
    if (prevPlayModeRef.current === "catchup" && playMode === "live") {
      skipNextSegmentsLoadRef.current = false;
      handleLoadSegments(segments, true);
    }
    prevPlayModeRef.current = playMode;
  }, [playMode, segments]);

  const handleVideoCanPlay = useEffectEvent((slotId: SlotId) => {
    if (slotId !== getActiveSlotId() && pendingTransitionRef.current?.slotId !== slotId) return;
    setIsLoading(false);
  });

  const handleVideoWaiting = useEffectEvent((slotId: SlotId) => {
    if (slotId !== getActiveSlotId() && pendingTransitionRef.current?.slotId !== slotId) return;
    setIsLoading(true);
    if (stablePlaybackTimeoutRef.current) {
      window.clearTimeout(stablePlaybackTimeoutRef.current);
      stablePlaybackTimeoutRef.current = 0;
    }
  });

  const handleVideoPlaying = useEffectEvent((slotId: SlotId, eventTimeStamp: number) => {
    const pending = currentPendingTransition(slotId);
    if (pending) completePendingSwitchIfNeeded(slotId, eventTimeStamp, pending);
    if (slotId !== getActiveSlotId()) return;

    hasStartedPlaybackRef.current = true;
    setIsLoading(false);
    setIsPlaying(true);
    onPlaybackStarted?.();

    const player = slotPlayerRef(slotId).current;
    if (playMode === "live" && player && !wallClockCalibratedRef.current) {
      wallClockCalibratedRef.current = true;
      calibrateLiveSession(player);
    }

    if (stablePlaybackTimeoutRef.current) window.clearTimeout(stablePlaybackTimeoutRef.current);
    stablePlaybackTimeoutRef.current = window.setTimeout(() => {
      if (retryCount > retryBaseline) setRetryBaseline(retryCount);
    }, 30_000);
  });

  const handleVideoPause = useEffectEvent((slotId: SlotId) => {
    if (slotId !== getActiveSlotId()) return;
    setIsPlaying(false);
    if (stablePlaybackTimeoutRef.current) {
      window.clearTimeout(stablePlaybackTimeoutRef.current);
      stablePlaybackTimeoutRef.current = 0;
    }
  });

  const handleVideoTimelineChange = useEffectEvent((slotId: SlotId) => {
    if (slotId !== getActiveSlotId()) return;
    updateMediaSessionPosition(true);
  });

  const handlePlaybackEnded = useEffectEvent(() => {
    const player = getActivePlayer();
    const duration = player?.getState().duration;
    if (onSeek && duration && Number.isFinite(duration)) {
      const seekTime = mseToWallClock(duration, streamStartTime);
      onSeek(seekTime, true);
    }
  });

  const handleVideoEnterPiP = useEffectEvent((slotId: SlotId) => {
    if (slotId !== getActiveSlotId()) return;
    setIsPiP(true);
  });

  const handleVideoLeavePiP = useEffectEvent(() => setIsPiP(isDocumentPiP || Boolean(document.pictureInPictureElement)));

  const handleVisibilityChange = useEffectEvent(() => {
    if (document.visibilityState !== "visible") return;
    const video = getActiveVideo();
    const activePlayer = getActivePlayer();
    if (!video || !activePlayer || error || needsUserInteraction) return;
    if (isAnyPictureInPictureActive()) return;
    if (userPausedRef.current) return;

    const mediaDead = video.error !== null;
    const behindLiveMs = Date.now() - mseToWallClock(currentVideoTimeRef.current, streamStartTime).getTime();
    const staleLiveMs = (defaultConfig.liveSyncMaxLatency + 5) * 1000;

    if (playMode === "live" && (mediaDead || behindLiveMs > staleLiveMs)) {
      shouldAutoPlayRef.current = true;
      onSeek?.(new Date(), true);
      return;
    }

    if (mediaDead) {
      shouldAutoPlayRef.current = true;
      const seekTime = mseToWallClock(currentVideoTimeRef.current, streamStartTime);
      onSeek?.(seekTime, isNearLiveWallClock(seekTime, liveSessionAnchor, streamStartTime));
      return;
    }

    if (activePlayer.getState().paused) {
      activePlayer.play().catch((err: Error) => {
        if (isInterruptedPlayError(err)) return;
        if (err.name === "NotAllowedError") setNeedsUserInteraction(true);
      });
    }
  });

  useEffect(() => {
    const handler = () => handleVisibilityChange();
    document.addEventListener("visibilitychange", handler);
    return () => document.removeEventListener("visibilitychange", handler);
  }, []);

  const handleMuteToggle = useEffectEvent(() => {
    const player = getActivePlayer();
    if (!player) return;
    const state = player.getState();
    if (state.volume <= 0) {
      player.setVolume(1);
      player.setMuted(false);
      return;
    }
    player.setMuted(!state.muted);
  });

  const handleKeyDown = useEffectEvent((e: KeyboardEvent) => {
    const eventDocument = getEventDocument(e);
    if (isEditableKeyboardTarget(e.target)) return;

    const isNumberKey = /^[0-9]$/.test(e.key);
    if (isNumberKey) {
      e.preventDefault();
      showControlsImmediately();
      if (digitTimeoutRef.current) window.clearTimeout(digitTimeoutRef.current);
      const newBuffer = digitBuffer + e.key;
      digitTimeoutRef.current = window.setTimeout(() => {
        onChannelNavigate?.(parseInt(newBuffer, 10));
        setDigitBuffer("");
        digitTimeoutRef.current = 0;
      }, 1000);
      setDigitBuffer(newBuffer);
      return;
    }

    switch (e.key) {
      case "Enter":
        if (digitBuffer) {
          e.preventDefault();
          if (digitTimeoutRef.current) {
            window.clearTimeout(digitTimeoutRef.current);
            digitTimeoutRef.current = 0;
          }
          onChannelNavigate?.(parseInt(digitBuffer, 10));
          setDigitBuffer("");
        } else if (isDocumentBodyActive(eventDocument)) {
          e.preventDefault();
          onToggleSidebar?.();
        }
        break;
      case "Escape":
        e.preventDefault();
        if (!isDocumentBodyActive(eventDocument)) {
          blurActiveElement(eventDocument);
        } else if (digitBuffer) {
          setDigitBuffer("");
          if (digitTimeoutRef.current) {
            window.clearTimeout(digitTimeoutRef.current);
            digitTimeoutRef.current = 0;
          }
        } else if (showControls) {
          hideControlsImmediately();
        } else {
          showControlsImmediately();
        }
        break;
      case "ArrowUp":
      case "PageDown":
      case "ChannelDown":
        e.preventDefault();
        blurActiveElement(eventDocument);
        onChannelNavigate?.("prev");
        break;
      case "ArrowDown":
      case "PageUp":
      case "ChannelUp":
        e.preventDefault();
        blurActiveElement(eventDocument);
        onChannelNavigate?.("next");
        break;
      case "ArrowLeft":
        e.preventDefault();
        blurActiveElement(eventDocument);
        handleRelativeSeek(-5);
        break;
      case "ArrowRight":
        e.preventDefault();
        blurActiveElement(eventDocument);
        handleRelativeSeek(5);
        break;
      case " ":
        if (!isDocumentBodyActive(eventDocument)) break;
        e.preventDefault();
        togglePlayPause();
        break;
      case "m":
      case "M":
        e.preventDefault();
        handleMuteToggle();
        break;
      case "f":
      case "F":
        e.preventDefault();
        onFullscreenToggle?.();
        break;
    }
  });

  const handleVideoElementError = useEffectEvent((slotId: SlotId, eventTimeStamp: number) => {
    fallbackPendingSwitchToHardSwitch(slotId, eventTimeStamp);
  });

  useEffect(() => {
    const attachSlot = (slotId: SlotId) => {
      const video = (slotId === "a" ? slotAVideoRef : slotBVideoRef).current;
      if (!video) return () => {};
      const listeners: Array<[string, EventListener]> = [
        ["seeked", () => handleVideoTimelineChange(slotId)],
        ["ratechange", () => handleVideoTimelineChange(slotId)],
        ["enterpictureinpicture", () => handleVideoEnterPiP(slotId)],
        ["leavepictureinpicture", () => handleVideoLeavePiP()],
        ["error", (event) => handleVideoElementError(slotId, event.timeStamp)],
      ];
      for (const [event, listener] of listeners) video.addEventListener(event, listener);
      return () => {
        for (const [event, listener] of listeners) video.removeEventListener(event, listener);
      };
    };

    const cleanupA = attachSlot("a");
    const cleanupB = attachSlot("b");

    return () => {
      cleanupA();
      cleanupB();
      if (stablePlaybackTimeoutRef.current) {
        window.clearTimeout(stablePlaybackTimeoutRef.current);
        stablePlaybackTimeoutRef.current = 0;
      }
      if (digitTimeoutRef.current) {
        window.clearTimeout(digitTimeoutRef.current);
        digitTimeoutRef.current = 0;
      }
    };
  }, []);

  useEffect(() => {
    const pipWindow = isDocumentPiP ? documentPiPWindowRef.current : null;
    const targetWindows = pipWindow && pipWindow !== window ? [window, pipWindow] : [window];
    for (const targetWindow of targetWindows) targetWindow.addEventListener("keydown", handleKeyDown);
    return () => {
      for (const targetWindow of targetWindows) targetWindow.removeEventListener("keydown", handleKeyDown);
    };
  }, [isDocumentPiP]);

  const handleVolumeChange = useEffectEvent((newVolume: number) => {
    const player = getActivePlayer();
    if (player) {
      player.setVolume(newVolume);
      if (player.getState().muted && newVolume > 0) player.setMuted(false);
    }
  });

  const { indicator: gestureIndicator, consumeSuppressedClick, gestureHandlers } = usePlayerTouchGestures({
    enabled: Boolean(channel) && !error && !needsUserInteraction,
    enableSeekGesture: isCatchupSupported,
    enableVolumeGesture: canControlVolume,
    volume,
    isMuted,
    prevChannel,
    nextChannel,
    onVolumeChange: handleVolumeChange,
    onChannelNavigate,
    onRelativeSeek: handleRelativeSeek,
    onTogglePlayPause: togglePlayPause,
    onShowControls: showControlsImmediately,
  });

  const handleSurfaceClick = useCallback(
    (event: ReactMouseEvent) => {
      const target = event.target as HTMLElement;
      if (target !== event.currentTarget && target.tagName !== "VIDEO" && !("playerSurfaceHit" in target.dataset)) return;
      if (consumeSuppressedClick()) return;
      if (showControls) hideControlsImmediately();
      else showControlsImmediately();
      onSurfaceClick?.();
    },
    [showControls, hideControlsImmediately, showControlsImmediately, consumeSuppressedClick, onSurfaceClick],
  );

  const exitPictureInPicture = useEffectEvent(async (): Promise<boolean> => {
    const documentPictureInPicture = getDocumentPictureInPicture();
    const pipWindow = documentPictureInPicture?.window ?? documentPiPWindowRef.current;
    if (pipWindow) {
      restoreDocumentPiPPlayer();
      pipWindow.close();
      return true;
    }
    if (document.pictureInPictureElement) {
      await document.exitPictureInPicture();
      return true;
    }
    return false;
  });

  const handleFullscreen = useEffectEvent(async () => {
    const isIOS = /iPhone|iPod/.test(navigator.userAgent);
    await exitPictureInPicture();
    const video = getActiveVideo();
    if (isIOS && video) {
      const iosVideo = video as HTMLVideoElement & { webkitSupportsFullscreen?: boolean; webkitEnterFullscreen?: () => void };
      if (iosVideo.webkitSupportsFullscreen && iosVideo.webkitEnterFullscreen) {
        try {
          iosVideo.webkitEnterFullscreen();
          return;
        } catch {
          // 回退到标准全屏 / 旋屏锁定方案。
        }
      }
    }
    await onFullscreenToggle?.();
  });

  const requestVideoPictureInPicture = useEffectEvent(async (video: HTMLVideoElement) => {
    if (!document.pictureInPictureEnabled || !video.requestPictureInPicture) return;
    await video.requestPictureInPicture();
  });

  const enterPictureInPicture = useEffectEvent(async () => {
    if (isAnyPictureInPictureActive()) return;
    const player = getActivePlayer();
    if (!player) return;
    const video = player.mediaElement;
    let openedDocumentPiPWindow: Window | null = null;

    try {
      const documentPictureInPicture = pictureInPictureMode === "document" ? getDocumentPictureInPicture() : null;
      if (documentPictureInPicture) {
        const playerElement = playerSurfaceRef.current;
        if (!playerElement) return;
        const pipWindowOptions = getDocumentPiPWindowOptions(playerElement);
        let pipWindow: Window;
        try {
          pipWindow = await documentPictureInPicture.requestWindow(pipWindowOptions);
        } catch (err) {
          if (isDocumentPictureInPictureBlockedError(err)) {
            await requestVideoPictureInPicture(video);
            return;
          }
          throw err;
        }
        openedDocumentPiPWindow = pipWindow;
        documentPiPWindowRef.current = pipWindow;
        setupDocumentPiPWindow(pipWindow);
        pipWindow.addEventListener("pagehide", () => restoreDocumentPiPPlayer(), { once: true });
        setIsDocumentPiP(true);
        setIsPiP(true);
        showControlsImmediately();
        pipWindow.document.body.append(playerPortalHost);
        return;
      }
      await requestVideoPictureInPicture(video);
    } catch (err) {
      const pipWindow = openedDocumentPiPWindow ?? documentPiPWindowRef.current;
      restoreDocumentPiPPlayer();
      pipWindow?.close();
      console.error("Picture-in-Picture error:", err);
    }
  });

  const handlePiPToggle = useEffectEvent(async () => {
    if (await exitPictureInPicture()) return;
    await enterPictureInPicture();
  });

  const handleMediaSessionEnterPictureInPicture = useEffectEvent(() => void enterPictureInPicture());

  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    const mediaSession = navigator.mediaSession;
    setMediaSessionAction(mediaSession, "play", handleMediaSessionPlay);
    setMediaSessionAction(mediaSession, "pause", handleMediaSessionPause);
    setMediaSessionAction(mediaSession, "previoustrack", canNavigateChannelsInMediaSession ? handleMediaSessionPreviousTrack : null);
    setMediaSessionAction(mediaSession, "nexttrack", canNavigateChannelsInMediaSession ? handleMediaSessionNextTrack : null);
    setMediaSessionAction(mediaSession, "seekbackward", canSeekProgramInMediaSession ? handleMediaSessionSeekBackward : null);
    setMediaSessionAction(mediaSession, "seekforward", canSeekProgramInMediaSession ? handleMediaSessionSeekForward : null);
    setMediaSessionAction(mediaSession, "seekto", canSeekProgramInMediaSession ? handleMediaSessionSeekTo : null);
    setMediaSessionAction(mediaSession, "enterpictureinpicture", isPictureInPictureSupported() ? handleMediaSessionEnterPictureInPicture : null);
    return () => {
      setMediaSessionAction(mediaSession, "play", null);
      setMediaSessionAction(mediaSession, "pause", null);
      setMediaSessionAction(mediaSession, "previoustrack", null);
      setMediaSessionAction(mediaSession, "nexttrack", null);
      setMediaSessionAction(mediaSession, "seekbackward", null);
      setMediaSessionAction(mediaSession, "seekforward", null);
      setMediaSessionAction(mediaSession, "seekto", null);
      setMediaSessionAction(mediaSession, "enterpictureinpicture", null);
    };
  }, [canNavigateChannelsInMediaSession, canSeekProgramInMediaSession]);

  const handleUserInteraction = useEffectEvent(() => {
    const player = getActivePlayer();
    if (!player) return;
    setNeedsUserInteraction(false);
    setIsPlaying(true);
    userPausedRef.current = false;
    player.play().catch((err: Error) => {
      if (isInterruptedPlayError(err)) return;
      console.error("Play error after user interaction:", err);
      setError({ message: `${t("failedToPlay")}: ${err.message}` });
      onError?.(`${t("failedToPlay")}: ${err.message}`);
    });
  });

  useEffect(() => {
    if (!needsUserInteraction) return;
    const handler = () => handleUserInteraction();
    const pipDocument = isDocumentPiP ? documentPiPWindowRef.current?.document : null;
    const targetDocuments = pipDocument && pipDocument !== document ? [document, pipDocument] : [document];
    for (const targetDocument of targetDocuments) {
      targetDocument.addEventListener("click", handler);
      targetDocument.addEventListener("keydown", handler);
    }
    return () => {
      for (const targetDocument of targetDocuments) {
        targetDocument.removeEventListener("click", handler);
        targetDocument.removeEventListener("keydown", handler);
      }
    };
  }, [needsUserInteraction, isDocumentPiP]);

  const isVideoPiP = isPiP && !isDocumentPiP;
  const playerSurface = (
    <div
      role="application"
      ref={playerSurfaceRef}
      className={clsx(
        // 不用把 aspect-video 写在基类里：与下面分支的 aspect-auto 同权重，
        // Tailwind 生成顺序会让 aspect-video 覆盖它（全屏就铺不满了）。
        "player-performance-video-background dark @container-size/video relative flex min-h-0 items-center justify-center bg-[radial-gradient(circle_at_50%_35%,#102044_0%,#070516_58%,#01030a_100%)]",
        // 全屏（含画中画）时舞台铺满可视区：手机端不再是一条 16:9 横条，
        // 屏幕高度随浏览器工具栏变化时画面也不会跟着上下移动。
        isDocumentPiP
          ? "h-screen min-h-screen aspect-auto"
          : isFullscreen
            ? "h-full w-full min-h-0 aspect-auto"
            : "aspect-video w-full md:aspect-auto md:h-full",
        !showControls && "cursor-none",
      )}
      onPointerEnter={handlePointerHover}
      onPointerMove={handlePointerHover}
      onPointerLeave={handlePointerLeave}
      onClick={handleSurfaceClick}
    >
      {/*
        视频呈现区：**尺寸恒定**（填满舞台），源画面比例一律交给 object-contain 内部消化。
        这样源比例变化（整屏广告 / 16:9 剧集 / 4:3 老片）不会引起外层盒子尺寸变化——
        旧实现按容器比例在 w-full / h-full 间翻转，比例临界时来回切换就是"抽动/闪屏"的来源。
      */}
      <div className="absolute inset-0 overflow-hidden">
        {(visibleSlotId === "a" ? (["b", "a"] as const) : (["a", "b"] as const)).map((slotId) => (
          <div key={slotId} className="contents">
            <video
              ref={slotId === "a" ? slotAVideoRef : slotBVideoRef}
              className={clsx(
                // object-contain：任何源比例都居中留边、不拉伸，也不改变布局
                "absolute inset-0 size-full min-h-0 min-w-0 object-contain",
                visibleSlotId !== slotId && "opacity-0 pointer-events-none",
                visibleSlotId === slotId && renderActiveSlots[slotId] && !isVideoPiP && "opacity-0",
              )}
              playsInline
              webkit-playsinline="true"
              x5-playsinline="true"
            />
            <canvas
              ref={slotId === "a" ? slotACanvasRef : slotBCanvasRef}
              className={clsx(
                "pointer-events-none absolute inset-0 size-full min-h-0 min-w-0 object-contain",
                (isVideoPiP || visibleSlotId !== slotId || !renderActiveSlots[slotId]) && "hidden",
              )}
            />
          </div>
        ))}
      </div>

      {!needsUserInteraction && !error && (
        <div aria-hidden="true" data-player-surface-hit="" className="absolute inset-0 z-[1] touch-none select-none" {...gestureHandlers} />
      )}

      {!needsUserInteraction && !error && (
        <PlayerTopLeftOverlay
          visible={showControls || showLoading}
          loading={showLoading}
          loadingText={`${channel && channel.sources.length > 1 ? `[${channel.sources[activeSourceIndex]?.label || `${t("source")} ${activeSourceIndex + 1}`}] ` : ""}${t("loadingVideo")}${retryCount - retryBaseline > 0 ? ` (${retryCount - retryBaseline}/${MAX_RETRIES})` : ""}`}
        />
      )}

      {channel && (
        <div
          className={clsx(
            "player-performance-motion absolute top-4 right-4 z-10 flex flex-col items-end gap-2 transition-opacity duration-300 md:top-8 md:right-8 md:gap-3 [@container_video_(max-height:_320px)]:top-2 [@container_video_(max-height:_320px)]:right-2 [@container_video_(max-height:_320px)]:gap-1 md:[@container_video_(max-height:_320px)]:top-2 md:[@container_video_(max-height:_320px)]:right-2 md:[@container_video_(max-height:_320px)]:gap-1 [@container_video_(max-height:_220px)]:top-1 [@container_video_(max-height:_220px)]:right-1 md:[@container_video_(max-height:_220px)]:top-1 md:[@container_video_(max-height:_220px)]:right-1",
            showControls ? "opacity-100" : "opacity-0 pointer-events-none",
          )}
        >
          <div
            className={clsx(
              PLAYER_OVERLAY_SURFACE_CLASS,
              "relative flex max-w-[calc(100vw-2rem)] flex-col items-center justify-center gap-1.5 overflow-hidden rounded-xl px-2 py-1.5 md:max-w-none md:gap-2 md:px-3 md:py-2 [@container_video_(max-height:_320px)]:gap-1 [@container_video_(max-height:_320px)]:rounded-lg [@container_video_(max-height:_320px)]:px-1.5 [@container_video_(max-height:_320px)]:py-1 md:[@container_video_(max-height:_320px)]:gap-1 md:[@container_video_(max-height:_320px)]:px-1.5 md:[@container_video_(max-height:_320px)]:py-1",
            )}
          >
            <PlayerSelectedGlassLayers />
            {channel.logo && (
              <img
                src={channel.logo}
                alt={channel.name}
                referrerPolicy="no-referrer"
                className="relative z-10 h-8 w-20 object-contain drop-shadow-[0_0_14px_rgba(var(--pg-rgb-light),0.2)] md:h-14 md:w-36 [@container_video_(max-height:_320px)]:h-6 [@container_video_(max-height:_320px)]:w-16 md:[@container_video_(max-height:_320px)]:h-6 md:[@container_video_(max-height:_320px)]:w-16 [@container_video_(max-height:_220px)]:hidden"
                onError={(e) => {
                  (e.target as HTMLImageElement).style.display = "none";
                }}
              />
            )}
            <div className="relative z-10 flex w-full min-w-0 items-center justify-center">
              <div className="flex min-w-0 items-center gap-1.5 md:gap-2 [@container_video_(max-height:_320px)]:gap-1 md:[@container_video_(max-height:_320px)]:gap-1">
                <span
                  className={clsx(
                    "player-performance-motion shrink-0 rounded-md px-1 py-0.5 font-semibold text-[10px] transition-[color,background-color,box-shadow,scale] duration-300 md:px-1.5 md:text-xs md:[@container_video_(max-height:_320px)]:px-1 md:[@container_video_(max-height:_320px)]:text-[10px]",
                    digitBuffer
                      ? "scale-110 bg-violet-600 bg-[linear-gradient(135deg,var(--pg-grad-a),var(--pg-grad-c))] text-white shadow-[0_0_20px_rgba(var(--pg-rgb),0.45)] ring-2 ring-violet-200/40"
                      : "bg-violet-100/10 text-violet-50/65 ring-1 ring-violet-100/10",
                  )}
                >
                  {/* 频道号（订阅序位）；不再显示短哈希 id（与上游一致） */}
                  {digitBuffer || (channel.number ?? "")}
                </span>
                <h2 className="truncate font-bold text-white text-xs tracking-[0.01em] md:text-base md:[@container_video_(max-height:_320px)]:text-xs">
                  {channel.name}
                </h2>
                {channel.groups.length > 0 && (
                  <>
                    <span className="hidden text-violet-100/35 text-xs sm:inline md:text-sm [@container_video_(max-height:_320px)]:hidden md:[@container_video_(max-height:_320px)]:hidden">
                      ·
                    </span>
                    <div className="hidden truncate text-violet-50/65 text-xs sm:block md:text-sm [@container_video_(max-height:_320px)]:hidden md:[@container_video_(max-height:_320px)]:hidden">
                      {channel.groups.join(" / ")}
                    </div>
                  </>
                )}
              </div>
            </div>
          </div>
        </div>
      )}

      {needsUserInteraction && (
        <button
          type="button"
          className="player-performance-overlay-background player-performance-motion absolute inset-0 z-10 flex cursor-pointer items-center justify-center border-none bg-[radial-gradient(circle_at_center,rgba(18,50,91,0.78),rgba(2,6,23,0.94)_68%)] p-4 transition-[filter,background-color] backdrop-blur-[2px] hover:brightness-110"
          onClick={handleUserInteraction}
        >
          <div className="flex flex-col items-center gap-4 text-white">
            <Play className="h-20 w-20 fill-violet-100/20 text-violet-100 opacity-95 drop-shadow-[0_0_24px_rgba(var(--pg-rgb),0.55)]" />
            <div className="max-w-lg px-2 text-center">
              <div className="mb-2 font-semibold text-2xl tracking-tight text-violet-50">{t("clickToPlay")}</div>
              <div className="text-pretty text-violet-50/65 text-sm leading-5">{t("autoplayBlocked")}</div>
            </div>
          </div>
        </button>
      )}

      {warning && !error && (
        <div className="pointer-events-none absolute inset-x-0 top-3 z-20 flex justify-center px-3 md:top-5 md:px-5">
          <div
            role="alert"
            className={clsx(
              PLAYER_OVERLAY_SURFACE_CLASS,
              "player-performance-warning-background pointer-events-auto w-full max-w-xl rounded-xl border-amber-200/25 bg-[linear-gradient(145deg,rgba(66,43,12,0.92),rgba(27,24,35,0.92))] p-3 text-white shadow-[0_16px_48px_rgba(24,13,2,0.48)] backdrop-blur-md md:p-4",
            )}
          >
            <div className="flex items-start gap-3">
              <CircleAlert className="mt-0.5 h-5 w-5 shrink-0 text-amber-200" aria-hidden="true" />
              <div className="min-w-0 flex-1">
                <div className="font-medium text-amber-50 text-sm md:text-base">{warning.message}</div>
                {warning.description && <div className="mt-1 break-words font-mono text-amber-50/65 text-xs leading-relaxed">{warning.description}</div>}
              </div>
              <button
                type="button"
                className="player-performance-motion -m-1 shrink-0 cursor-pointer rounded-lg p-1.5 text-amber-100/65 transition-colors hover:bg-white/10 hover:text-amber-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-amber-200/70"
                aria-label={t("dismiss")}
                title={t("dismiss")}
                onClick={() => setWarning(null)}
              >
                <X className="h-4 w-4" aria-hidden="true" />
              </button>
            </div>
          </div>
        </div>
      )}

      {error && (
        <div className="player-performance-error-backdrop player-performance-overlay-background absolute inset-0 z-10 flex items-center justify-center bg-[radial-gradient(circle_at_center,rgba(76,20,55,0.46),rgba(2,6,23,0.96)_72%)] p-3 backdrop-blur-[3px] md:p-4">
          <div
            className={clsx(
              PLAYER_OVERLAY_SURFACE_CLASS,
              "player-performance-error-background player-performance-overlay-background max-h-full w-full max-w-xl overflow-y-auto rounded-2xl border-rose-300/25 bg-[linear-gradient(145deg,rgba(52,18,50,0.82),rgba(12,22,51,0.8))] p-4 text-white shadow-[0_20px_60px_rgba(43,5,32,0.58)] [@media(max-height:360px)]:p-2.5 md:p-5",
            )}
          >
            <div className="flex items-center gap-2 font-semibold text-lg text-rose-100">
              <CircleAlert className="h-5 w-5 shrink-0" aria-hidden="true" />
              {t("playbackError")}
            </div>
            <div className="mt-2 break-words font-medium text-pretty text-rose-50 text-sm leading-relaxed">{error.message}</div>
            {error.description && (
              <div className="mt-1 break-words text-pretty text-rose-50/70 text-xs leading-relaxed [@media(max-height:360px)]:hidden md:text-sm">
                {error.description}
              </div>
            )}
            {(error.statusCode !== undefined || error.requestUrl) && (
              <div className="mt-3 grid gap-2 text-xs [@media(max-height:360px)]:mt-2 [@media(max-height:360px)]:gap-1 md:text-sm">
                {error.statusCode !== undefined && (
                  <div className="grid grid-cols-[auto_1fr] items-baseline gap-3 rounded-lg bg-black/20 px-3 py-2 [@media(max-height:360px)]:py-1.5">
                    <span className="text-rose-100/55">{t("httpStatus")}</span>
                    <span className="min-w-0 font-mono text-rose-50">
                      {error.statusCode}
                      {error.statusText ? ` ${error.statusText}` : ""}
                    </span>
                  </div>
                )}
                {error.requestUrl && (
                  <div className="rounded-lg bg-black/20 px-3 py-2 [@media(max-height:360px)]:grid [@media(max-height:360px)]:grid-cols-[auto_1fr] [@media(max-height:360px)]:items-baseline [@media(max-height:360px)]:gap-3 [@media(max-height:360px)]:py-1.5">
                    <div className="mb-1 text-rose-100/55 [@media(max-height:360px)]:mb-0">{t("requestUrl")}</div>
                    <div className="min-w-0 whitespace-normal break-all font-mono text-rose-50" title={error.requestUrl}>
                      {error.requestUrl}
                    </div>
                  </div>
                )}
              </div>
            )}
            {error.suggestion && (
              <div className="mt-3 rounded-lg border border-amber-200/15 bg-amber-100/8 px-3 py-2 text-xs leading-relaxed [@media(max-height:360px)]:mt-2 [@media(max-height:360px)]:py-1 md:text-sm">
                <div className="font-medium text-amber-100">{t("suggestedAction")}</div>
                <div className="mt-0.5 text-amber-50/70">{error.suggestion}</div>
              </div>
            )}
          </div>
        </div>
      )}

      {channel && !error && !needsUserInteraction && (
        <div
          role="toolbar"
          className={clsx(
            "player-performance-controls-position player-performance-motion absolute bottom-0 left-[calc(0px_-_env(safe-area-inset-left))] right-[calc(0px_-_env(safe-area-inset-right))] z-10 transition-opacity duration-300",
            showSidebar && "md:left-0",
            showControls
              ? "opacity-100"
              : "opacity-0 pointer-events-none has-focus-visible:opacity-100 has-focus-visible:pointer-events-auto",
          )}
        >
          <PlayerControls
            channel={channel}
            currentProgram={currentProgram}
            isLive={isLive}
            onSeek={handleSeek}
            onScrubbingChange={handleScrubbingChange}
            locale={locale}
            mediaInfo={slotMediaInfo[visibleSlotId]}
            renderState={slotRenderStates[visibleSlotId]}
            seekStartTime={streamStartTime}
            liveSessionAnchor={liveSessionAnchor}
            isPlaying={isPlaying}
            onPlayPause={togglePlayPause}
            volume={volume}
            onVolumeChange={handleVolumeChange}
            canControlVolume={canControlVolume}
            isMuted={isMuted}
            onMuteToggle={handleMuteToggle}
            onFullscreen={handleFullscreen}
            isFullscreen={isFullscreen}
            showSidebar={showSidebar}
            isPiP={isPiP}
            isPiPSupported={isPictureInPictureSupported()}
            onPiPToggle={handlePiPToggle}
            showMediaBadges={!isDocumentPiP}
            activeSourceIndex={activeSourceIndex}
            onSourceChange={onSourceChange}
          />
        </div>
      )}

      {channel && !error && !needsUserInteraction && <PlayerGestureIndicatorOverlay indicator={gestureIndicator} locale={locale} />}
    </div>
  );

  return (
    <div
      className={clsx(
        "player-performance-video-background relative w-full bg-[radial-gradient(circle_at_50%_35%,#102044_0%,#070516_58%,#01030a_100%)] pt-[env(safe-area-inset-top)] pr-[env(safe-area-inset-right)] pl-[env(safe-area-inset-left)] md:h-full",
        // 手机全屏：本层也要有确定高度，里面的舞台 h-full 才有参照（否则塌成 0）
        isFullscreen && "h-full",
        showSidebar && "md:pl-0",
      )}
    >
      <div ref={playerDockRef} className="contents">
        {isDocumentPiP && (
          <div className="@container-size/video relative flex aspect-video w-full min-h-0 items-center justify-center bg-[radial-gradient(circle_at_center,#102044_0%,#070516_62%,#01030a_100%)] px-4 text-center font-medium text-violet-50/65 text-sm md:aspect-auto md:h-full md:text-base">
            {t("playingInPictureInPicture")}
          </div>
        )}
      </div>
      {createPortal(playerSurface, playerPortalHost)}
    </div>
  );
}

export { VideoPlayerComponent as VideoPlayer };
