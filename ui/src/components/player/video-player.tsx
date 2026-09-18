/**
 * 播放器 UI 主壳（纯编排层）。
 *
 * 本组件不做解码 / MSE / 搬流，只承担三类事情：
 *  1. 装配：按双槽（A/B 各一套 video + canvas）创建 PlaybackBackend 实例并灌入 segments；
 *  2. 编排：把键盘、触控手势、控制条、Media Session（锁屏遥控）等用户意图映射回引擎调用；
 *  3. 兜底：维护双槽无缝换台状态机、两种画中画（Document / video 级）、停摆看门狗，
 *     以及错误的分级自愈（预算内重试 → 换源 → 错误面板）。
 *
 * 播放语义（时间轴换算、追边策略、直播延迟）全部在 ../../media-engine；本文件的常量
 * 只调节"编排节奏"：重试预算、控件隐藏延时、看门狗阈值等。
 */
import {
  useCallback,
  useEffect,
  useEffectEvent,
  useLayoutEffect,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { clsx } from "clsx";
import { CircleAlert, Play, X } from "lucide-react";
import { createPortal } from "react-dom";
import { usePlayerTouchGestures } from "../../hooks/use-player-touch-gestures";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import { createProgramTimeline, programPositionToWallClock } from "../../lib/program-timeline";
import { getMuted, getVolume, saveMuted, saveVolume } from "../../lib/player-storage";
import { isVolumeControlSupported } from "../../lib/platform";
import type { Locale } from "../../lib/locale";
import {
  isAnyPictureInPictureActive,
  isDocumentPictureInPictureBlockedError,
  isPictureInPictureSupported,
  getDocumentPictureInPicture,
  getDocumentPiPWindowOptions,
  setupDocumentPiPWindow,
} from "../../lib/document-picture-in-picture";
import {
  builtinWasmDecoders,
  createPlaybackBackend,
  defaultConfig,
  getPlaybackBackendKind,
  isMSEPlaybackSupported,
  PlayerErrors,
  type PlaybackBackend,
  type PlayerError,
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
import { ChannelLogo } from "./channel-logo";
import { PLAYER_OVERLAY_SURFACE_CLASS } from "./classnames";
import { PlayerControls } from "./player-controls";
import { PlayerGestureIndicatorOverlay } from "./player-gesture-overlay";
import { PlayerSelectedGlassLayers } from "./player-selected-glass-layers";

/* ------------------------------------------------------------------ */
/* 对外 props 契约                                                     */
/* ------------------------------------------------------------------ */

interface PlayerShellProps {
  /** 当前频道；null 表示无频道（显示空态）。 */
  channel: Channel | null;
  /** 待播放分段；引用变化即触发（重）加载。 */
  segments: PlayerSegment[];
  playMode: "live" | "catchup";
  onError?: (error: string) => void;
  locale: Locale;
  currentProgram?: EPGProgram | null;
  onSeek?: (seekTime: Date, goingLive: boolean) => void;
  /** 直播模式下：重新校准 MSE t=0 与墙钟的对应关系。 */
  onStreamStartTimeChange?: (time: Date) => void;
  streamStartTime: Date;
  onCurrentVideoTimeChange: (time: number) => void;
  onChannelNavigate?: (target: "prev" | "next" | number) => void;
  /** 相邻频道，供滑动换台手势预览目标台。 */
  prevChannel?: Channel | null;
  nextChannel?: Channel | null;
  showSidebar?: boolean;
  onToggleSidebar?: () => void;
  /** 单击播放画面（含视频区）时上报：宿主据此实现"点屏幕出/收侧边栏"。 */
  onSurfaceClick?: () => void;
  isFullscreen: boolean;
  onFullscreenToggle?: () => Promise<boolean> | boolean;
  seamlessSwitch?: boolean;
  autoDeinterlace?: boolean;
  pictureEnhancement?: boolean;
  /** 软解音频（MP2/AC-3）的声道输出模式：mono = 左右声道合成为单声道。 */
  audioChannelMode?: "stereo" | "mono";
  pictureInPictureMode?: PictureInPictureMode;
  activeSourceIndex?: number;
  onSourceChange?: (index: number) => void;
  onPlaybackStarted?: () => void;
}

/** 错误 / 告警面板渲染所需的结构化信息。 */
interface PlaybackFailureView {
  message: string;
  description?: string;
  statusCode?: number;
  statusText?: string;
  requestUrl?: string;
  suggestion?: string;
}

/** 双槽标记：A 为初始槽，B 在无缝换台时承接新流。 */
type SlotTag = "a" | "b";

/** 一次尚未完成的无缝换台：新流已灌入承接槽，等它起播后接管。 */
interface HandoverTicket {
  /** 换台代数：保证迟到的旧事件无法提交过期状态。 */
  generation: number;
  slotTag: SlotTag;
  backend: PlaybackBackend;
  /** 发起时刻（performance.now()）：早于发起时刻的事件不可信。 */
  startedAt: number;
}

/** 校验"待定换台是否仍是等待中的那一次"所需的字段子集。 */
type HandoverTicketIdentity = Pick<HandoverTicket, "generation" | "backend">;

/* ------------------------------------------------------------------ */
/* 编排节奏常量                                                        */
/* ------------------------------------------------------------------ */

/** 同一流上连续错误的重试预算；耗尽则换源，再失败才报错面板。 */
const RETRY_BUDGET = 3;

/** 两槽均空闲（无 WebGL 后处理、未去隔行）时的渲染状态。 */
const IDLE_RENDER_STATE: PlayerRenderState = { active: false, deinterlacing: false };

/** 加载徽标延迟显示时长（毫秒）：秒级起播不该闪一下加载态。 */
const LOADING_BADGE_DELAY_MS = 500;

/** 控件无操作自动隐藏延时（毫秒）。 */
const CONTROLS_IDLE_HIDE_DELAY_MS = 3_000;

/** 持续播放多久视为"稳定"，把试错期重试计数固化为新基线（毫秒）。 */
const PLAYBACK_STABILITY_WINDOW_MS = 30_000;

/** 数字频道号输入的提交等待（毫秒）：超过即按已输号码跳台。 */
const DIGIT_COMMIT_DELAY_MS = 1_000;

/** 系统媒体会话位置上报的最小间隔（毫秒）。 */
const POSITION_STATE_THROTTLE_MS = 1_000;

/** 键盘 / 锁屏遥控的相对步进秒数（对方未给出 seekOffset 时）。 */
const SEEK_STEP_SECONDS = 5;

// ---- 停摆看门狗（网络异常兜底） ----
/** 单次采样里播放时钟变化小于该值（秒）即视为"没动"。 */
const STALL_CLOCK_EPSILON_SECONDS = 0.25;
/** 播放时钟停止推进多久判定为停摆（毫秒）；正常直播每 1~2s 必有推进。 */
const STALL_THRESHOLD_MS = 12_000;
/** 看门狗轮询间隔（毫秒）。 */
const STALL_PATROL_INTERVAL_MS = 2_000;
/** 连续重建的退避基值与上限（毫秒）：30s 起步指数翻倍，封顶 180s。 */
const STALL_BACKOFF_BASE_MS = 30_000;
const STALL_BACKOFF_CAP_MS = 180_000;
/** 判定"还有救"的最小前向缓冲（秒）：够这一口就补播，而不是重建。 */
const MIN_BUFFERED_AHEAD_TO_RESUME_SECONDS = 0.3;
/** 判定"直播已经落后到该重拉"时，在引擎最大滞后之上额外放宽的宽限量（秒）。 */
const LIVE_LAG_GRACE_SECONDS = 5;

/* ------------------------------------------------------------------ */
/* 纯工具函数                                                          */
/* ------------------------------------------------------------------ */

/** 从播放头所在的缓冲区间里，算出还能不中断往前播多少秒；播放头落在空洞/区间外则为 0。 */
function bufferedSecondsAheadOfPlayhead(element: HTMLVideoElement): number {
  const playhead = element.currentTime;
  const ranges = element.buffered;
  for (let i = 0; i < ranges.length; i++) {
    if (playhead < ranges.start(i) || playhead > ranges.end(i)) continue;
    return ranges.end(i) - playhead;
  }
  return 0;
}

/** 双槽中另一块：无缝换台时新流总是灌到"非当前槽"。 */
function siblingSlotOf(slot: SlotTag): SlotTag {
  return slot === "a" ? "b" : "a";
}

/**
 * `play()` 因换台 / 重灌 segments 被打断（AbortError: interrupted / new load request）
 * 是正常副产物，按"无事发生"处理；其余错误不算此类。
 */
function isPlayLoadAbortedError(err: unknown): boolean {
  if (!(err instanceof Error)) return false;
  const message = err.message.toLowerCase();
  return err.name === "AbortError" && (message.includes("interrupted") || message.includes("new load request"));
}

/** 作为 `.catch()` 处理器使用：打断类错误吞掉，其余原样上抛。 */
function dropIfPlayLoadAborted(err: unknown): void {
  if (!isPlayLoadAbortedError(err)) throw err;
}

/** 个别浏览器对 MediaSession action"有声明、无实现"，注册失败必须吞掉。 */
function applyMediaSessionAction(
  mediaSession: MediaSession,
  action: MediaSessionAction | "enterpictureinpicture",
  handler: MediaSessionActionHandler | null,
): void {
  try {
    mediaSession.setActionHandler(action as MediaSessionAction, handler);
  } catch {
    // 该 action 未实现，忽略。
  }
}

/**
 * 组装锁屏（Media Session）"正在播放"卡片。
 * 未选台返回 null（调用方据此整体清空卡片）；节目标题为空串时，标题回退到频道名、
 * 艺术家回退到分组列表，避免锁屏上出现空字段。
 */
function buildNowPlayingMetadata(channel: Channel | null, program: EPGProgram | null): MediaMetadata | null {
  if (!channel) return null;
  return new MediaMetadata({
    title: program?.title || channel.name,
    artist: program?.title ? channel.name : channel.groups.join(" / "),
    artwork: channel.logo ? [{ src: channel.logo }] : [],
  });
}

/** 展示用 URL 解码；畸形转义序列会导致 decodeURI 抛错，此时退回原文。 */
function safelyDecodeUri(url: string): string {
  try {
    return decodeURI(url);
  } catch {
    return url;
  }
}

/** 拼接面向开发者的技术性错误细节（详情行 / 诊断日志共用）。 */
function composeTechnicalErrorText(playerError: PlayerError): string {
  const parts: string[] = playerError.detail ? [playerError.detail] : [];
  if (playerError.codec) parts.push(`${playerError.track ?? "media"} codec=${playerError.codec}`);
  parts.push(playerError.info ?? "");
  if (playerError.code !== undefined && playerError.code !== -1) parts.push(`code=${playerError.code}`);
  if (playerError.url) parts.push(safelyDecodeUri(playerError.url));
  return parts.filter((value) => value !== undefined && value !== "").join(": ");
}

/**
 * 判定一个 DOM 事件发生在哪份文档里：播放器可能整体搬进 Document PiP 窗口，
 * 同一个 handler 会同时收到主文档与小窗文档的事件，归属错了快捷键就会漏掉或重复。
 */
function ownerDocumentOfEvent(event: Event): Document {
  const source = event.target as (Node & { ownerDocument?: Document | null; document?: Document | null }) | null;
  // 合成事件可能没有 target：只能视为发生在当前全局文档。
  if (!source) return document;

  // nodeType 9 = DOCUMENT_NODE：target 本身就是某份文档（不判 instanceof，跨窗口的
  // 文档不属于当前 realm）。
  if (source.nodeType === 9) return source as Document;

  // 普通节点带 ownerDocument；window / PiP 全局对象则以 `document` 属性持有其文档。
  if (source.ownerDocument) return source.ownerDocument;
  const viaGlobal = source.document;
  if (viaGlobal) return viaGlobal;
  return document;
}

/** 焦点位于文本输入类控件时，方向键 / 空格等快捷键必须让位给输入本身。 */
function isTextEntryTarget(target: EventTarget | null): boolean {
  if (!target || !("tagName" in target)) return false;
  const tag = String((target as { tagName?: unknown }).tagName).toUpperCase();
  const editable = (target as { isContentEditable?: boolean }).isContentEditable === true;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || editable;
}

/** "焦点在页面本身"（没有任何控件持有焦点）：全局按键语义此时才生效。 */
function isFocusOnPageBody(targetDocument: Document): boolean {
  const activeElement = targetDocument.activeElement;
  return !activeElement || activeElement === targetDocument.body;
}

/** 把焦点交还给页面（body），让随后的按键回到"控制播放器"语义。 */
function releaseFocusedElement(targetDocument: Document): void {
  const activeElement = targetDocument.activeElement;
  if (!activeElement || activeElement === targetDocument.body) return;
  const blur = (activeElement as { blur?: () => void }).blur;
  if (blur) blur.call(activeElement);
}

/* ------------------------------------------------------------------ */
/* 小组件：左上角时钟 + 加载徽标                                        */
/* ------------------------------------------------------------------ */

/**
 * 外层负责显隐，徽标本身只管内容。
 * 时钟按"先补齐到下一个整分、再进入分钟级 interval"的节奏刷新：首屏时间准确，
 * 且重渲染频率被压到每分钟一次。
 */
function ClockAndLoadingBadge({
  badgeVisible,
  badgeLoading,
  badgeText,
}: {
  badgeVisible: boolean;
  badgeLoading: boolean;
  badgeText: string;
}) {
  const [clockNow, setClockNow] = useState(() => new Date());

  useEffect(() => {
    const tick = () => setClockNow(new Date());
    tick();
    const msUntilNextMinute = 60_000 - (Date.now() % 60_000);
    let minuteIntervalId = 0;
    const alignTimeoutId = window.setTimeout(() => {
      tick();
      minuteIntervalId = window.setInterval(tick, 60_000);
    }, msUntilNextMinute);
    return () => {
      window.clearTimeout(alignTimeoutId);
      if (minuteIntervalId) window.clearInterval(minuteIntervalId);
    };
  }, []);

  return (
    <div
      className={clsx(
        PLAYER_OVERLAY_SURFACE_CLASS,
        "player-performance-motion absolute top-4 left-4 z-10 max-w-[calc(100%-2rem)] rounded-xl px-2 py-1.5 transition-opacity duration-300 md:top-8 md:left-8 md:px-3 md:py-2 [@container_video_(max-height:_320px)]:top-2 [@container_video_(max-height:_320px)]:left-2 [@container_video_(max-height:_320px)]:rounded-lg [@container_video_(max-height:_320px)]:px-1.5 [@container_video_(max-height:_320px)]:py-1 md:[@container_video_(max-height:_320px)]:top-2 md:[@container_video_(max-height:_320px)]:left-2 md:[@container_video_(max-height:_320px)]:px-1.5 md:[@container_video_(max-height:_320px)]:py-1 [@container_video_(max-height:_220px)]:top-1 [@container_video_(max-height:_220px)]:left-1 md:[@container_video_(max-height:_220px)]:top-1 md:[@container_video_(max-height:_220px)]:left-1",
        badgeVisible ? "opacity-100" : "opacity-0 pointer-events-none",
      )}
    >
      <PlayerSelectedGlassLayers />
      <div className="relative z-10 flex min-w-0 items-center gap-1.5 md:gap-2 [@container_video_(max-height:_320px)]:gap-1 md:[@container_video_(max-height:_320px)]:gap-1">
        <span className="shrink-0 font-medium text-xs text-violet-50 tabular-nums drop-shadow-sm md:text-base md:[@container_video_(max-height:_320px)]:text-xs">
          {clockNow.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
        </span>
        {badgeLoading && (
          <>
            <span className="shrink-0 text-violet-100/35 text-xs md:text-sm md:[@container_video_(max-height:_320px)]:text-xs" aria-hidden="true">
              ·
            </span>
            <div className="relative h-3 w-3 shrink-0 md:h-3.5 md:w-3.5 md:[@container_video_(max-height:_320px)]:h-3 md:[@container_video_(max-height:_320px)]:w-3">
              <div className="absolute inset-0 rounded-full border border-violet-100/25" />
              <div className="player-performance-loading-spinner absolute inset-0 animate-spin rounded-full border border-violet-200 border-t-transparent shadow-[0_0_8px_rgba(var(--pg-rgb-light),0.5)]" />
            </div>
            <span className="min-w-0 truncate text-violet-50/70 text-xs md:text-sm md:[@container_video_(max-height:_320px)]:text-xs">
              {badgeText}
            </span>
          </>
        )}
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* 主组件                                                              */
/* ------------------------------------------------------------------ */

function VideoPlayerShell({
  channel,
  segments,
  playMode,
  locale,
  currentProgram = null,
  onSeek,
  onStreamStartTimeChange,
  streamStartTime,
  onCurrentVideoTimeChange,
  onChannelNavigate,
  onError,
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
}: PlayerShellProps) {
  const tr = usePlayerTranslation(locale);
  const backendKind = getPlaybackBackendKind();
  const playheadSecondsRef = useRef(0);
  const catchupAvailable = Boolean(channel?.sources.some((source) => source.timeshift && source.timeshiftTemplate));
  const mediaSessionSeekEnabled = Boolean(currentProgram) && catchupAvailable;
  const volumeControlAvailable = isVolumeControlSupported();
  const mediaSessionZapEnabled = Boolean(channel && onChannelNavigate);

  // 双槽 DOM 与实例：video/canvas 各两份、backend 各一个；activeSlotRef 指向当前播出槽。
  const dockHostRef = useRef<HTMLDivElement>(null);
  const surfaceRef = useRef<HTMLDivElement>(null);
  const pipWindowRef = useRef<Window | null>(null);
  const unmountStartedRef = useRef(false);
  // 门户宿主：平时挂在播放器舞台里；进入 Document PiP 时会被整体搬进 PiP 窗口。
  const [portalHostElement] = useState(() => {
    const host = document.createElement("div");
    host.style.display = "contents";
    return host;
  });
  const videoARef = useRef<HTMLVideoElement>(null);
  const videoBRef = useRef<HTMLVideoElement>(null);
  const canvasARef = useRef<HTMLCanvasElement>(null);
  const canvasBRef = useRef<HTMLCanvasElement>(null);
  const backendARef = useRef<PlaybackBackend | null>(null);
  const backendBRef = useRef<PlaybackBackend | null>(null);
  const activeSlotRef = useRef<SlotTag>("a");
  const [displayedSlot, setDisplayedSlot] = useState<SlotTag>("a");
  const [mediaInfoBySlot, setMediaInfoBySlot] = useState<Record<SlotTag, PlayerMediaInfo | null>>({ a: null, b: null });
  const handoverGenerationRef = useRef(0);
  const pendingHandoverRef = useRef<HandoverTicket | null>(null);
  // 无缝过渡期"临时静音"的隔离位：切台时对实例做的临时静音**不得**写回应用级音量状态，
  // 否则会污染 mutedState（含持久化），并被 commitHandover 带给新实例 → 新台无声。
  const handoverMutedSlotRef = useRef<SlotTag | null>(null);
  const handoverMutedSnapshotRef = useRef<boolean | null>(null);
  const everPlayedRef = useRef(false);
  const lastStreamIdentityRef = useRef<{ channelId: string; sourceIndex: number } | null>(null);
  const suppressNextLoadRef = useRef(false);

  const videoRefOf = (slot: SlotTag) => (slot === "a" ? videoARef : videoBRef);
  const canvasRefOf = (slot: SlotTag) => (slot === "a" ? canvasARef : canvasBRef);
  const backendRefOf = (slot: SlotTag) => (slot === "a" ? backendARef : backendBRef);

  const [renderStateBySlot, setRenderStateBySlot] = useState<Record<SlotTag, PlayerRenderState>>({
    a: IDLE_RENDER_STATE,
    b: IDLE_RENDER_STATE,
  });
  const applyRenderStateIfChanged = (slot: SlotTag, next: PlayerRenderState) =>
    setRenderStateBySlot((previous) =>
      previous[slot].active === next.active && previous[slot].deinterlacing === next.deinterlacing
        ? previous
        : { ...previous, [slot]: next },
    );
  const slotPaintActive = { a: renderStateBySlot.a.active, b: renderStateBySlot.b.active };

  const currentSlotId = () => activeSlotRef.current;
  const activeVideoElement = () => videoRefOf(currentSlotId()).current;
  const activeBackend = () => backendRefOf(currentSlotId()).current;

  const [isLoadingStream, setIsLoadingStream] = useState(false);
  const [showSpinner, setShowSpinner] = useState(false);
  const spinnerDelayRef = useRef<number>(0);
  const [failure, setFailure] = useState<PlaybackFailureView | null>(() =>
    backendKind === "native" || isMSEPlaybackSupported() ? null : { message: tr("mseNotSupported") },
  );
  const [notice, setNotice] = useState<PlaybackFailureView | null>(null);
  const [volumeLevel, setVolumeLevel] = useState(() => getVolume());
  const [mutedState, setMutedState] = useState(() => getMuted());
  const [isPlaybackActive, setPlaybackActive] = useState(false);
  const [sessionAnchor, setSessionAnchor] = useState<LiveSessionAnchor | null>(null);
  const inLiveMode = playMode === "live";
  const [requiresGesture, setRequiresGesture] = useState(false);
  const [controlsVisible, setControlsVisible] = useState(true);
  const [inPip, setInPip] = useState(false);
  const [inDocumentPip, setInDocumentPip] = useState(false);
  const controlsHideTimerRef = useRef<number>(0);
  const [attemptCount, setAttemptCount] = useState(0);
  const [attemptFloor, setAttemptFloor] = useState(0);
  const recoverySeekFlagRef = useRef(false);
  const playbackStabilityTimerRef = useRef<number>(0);
  const autoplayIntentRef = useRef(true);
  const pausedByUserRef = useRef(false);
  const anchorCalibratedRef = useRef(false);
  // ---- 停摆看门狗状态 ----
  /** 最近一次采样到的播放时钟位置（停摆判定基准）。 */
  const stallLastClockRef = useRef(0);
  /** 播放时钟最后一次推进的时刻（毫秒）。 */
  const stallSinceRef = useRef(0);
  /** 上一次介入（补播/重建）的时刻（毫秒）。 */
  const stallLastActionRef = useRef(0);
  /** 连续介入次数（退避用）；时钟恢复推进即清零。 */
  const stallStrikeRef = useRef(0);
  /** 上次采样的解码帧数：区分"元素没动"与"解码卡死"。 */
  const stallDecodeSampleRef = useRef(-1);
  const positionStateSyncedAtRef = useRef(0);

  const [channelDigits, setChannelDigits] = useState("");
  const digitCommitTimerRef = useRef<number>(0);

  // 加载徽标延迟 500ms 才出现；卸载/加载结束时清掉挂着的定时器。
  useEffect(() => {
    if (isLoadingStream) {
      spinnerDelayRef.current = window.setTimeout(() => setShowSpinner(true), LOADING_BADGE_DELAY_MS);
    } else {
      setShowSpinner(false);
    }
    return () => {
      if (spinnerDelayRef.current) {
        window.clearTimeout(spinnerDelayRef.current);
        spinnerDelayRef.current = 0;
      }
    };
  }, [isLoadingStream]);

  /** 相对步进（±N 秒）：仅对支持回看的流开放；步进不改变"是否在播"的用户意图。 */
  const nudgePlayhead = useEffectEvent((deltaSeconds: number) => {
    if (!catchupAvailable) return;
    const backend = activeBackend();
    if (!backend) return;
    const state = backend.getState();
    autoplayIntentRef.current = !state.paused;
    if (playMode === "live") backend.setLiveSync(false);
    backend.seek(state.currentTime + deltaSeconds);
  });

  /** 用当前播放位置反推墙钟起点并同步引擎与应用层：直播模式时间轴的校准入口。 */
  const rebaseSessionClock = useEffectEvent((backend: PlaybackBackend) => {
    const currentTime = backend.getState().currentTime;
    const wallClockOrigin = new Date(Date.now() - currentTime * 1000);
    const anchor = createLiveSessionAnchor(currentTime);
    setSessionAnchor(anchor);
    onStreamStartTimeChange?.(wallClockOrigin);
    activeBackend()?.setLiveSessionAnchor(anchor);
  });

  const seekLiveToWallClock = useEffectEvent((seekTime: Date) => {
    const targetMse = wallClockToMse(seekTime, streamStartTime);
    activeBackend()?.seek(targetMse);
  });

  const jumpToLiveEdge = useEffectEvent(() => {
    if (!sessionAnchor) return;
    const targetMse = goLiveTargetMse(sessionAnchor, defaultConfig.chaseTargetLagSeconds);
    activeBackend()?.goLive(targetMse);
    activeBackend()?.setLiveSync(true);
  });

  const withinLiveWindow = useEffectEvent(
    (seekTime: Date): boolean => isNearLiveWallClock(seekTime, sessionAnchor, streamStartTime),
  );

  const performSeek = useEffectEvent((seekTime: Date, goingLiveHint?: boolean) => {
    const backend = activeBackend();
    if (!backend) return;
    const goingLive = goingLiveHint ?? withinLiveWindow(seekTime);

    if (goingLive) {
      pausedByUserRef.current = false;
      if (playMode === "live") {
        jumpToLiveEdge();
        backend.play().catch(dropIfPlayLoadAborted);
        return;
      }
      autoplayIntentRef.current = !backend.getState().paused;
      onSeek?.(new Date(), true);
      return;
    }

    autoplayIntentRef.current = !backend.getState().paused;
    if (playMode === "live") {
      backend.setLiveSync(false);
      seekLiveToWallClock(seekTime);
      return;
    }

    // 回看：目标点在本流起点之后直接 seek；之前则交给上层换段/换台。
    const seekSeconds = (seekTime.getTime() - streamStartTime.getTime()) / 1000;
    if (seekSeconds >= 0) backend.seek(seekSeconds);
    else onSeek?.(seekTime, false);
  });

  const flipPlayback = useEffectEvent(() => {
    const backend = activeBackend();
    if (!backend) return;
    if (backend.getState().paused) {
      pausedByUserRef.current = false;
      backend.play().catch(dropIfPlayLoadAborted);
    } else {
      pausedByUserRef.current = true;
      backend.pause();
    }
  });

  const armControlsHideTimer = useCallback(() => {
    if (controlsHideTimerRef.current) window.clearTimeout(controlsHideTimerRef.current);
    controlsHideTimerRef.current = window.setTimeout(() => setControlsVisible(false), CONTROLS_IDLE_HIDE_DELAY_MS);
  }, []);

  const revealControls = useCallback(() => {
    setControlsVisible(true);
    armControlsHideTimer();
  }, [armControlsHideTimer]);

  // 拖动进度条期间冻结自动隐藏；拖完（isScrubbing=false）才重新计时。
  const applyScrubbingState = useCallback(
    (isScrubbing: boolean) => {
      if (controlsHideTimerRef.current) {
        window.clearTimeout(controlsHideTimerRef.current);
        controlsHideTimerRef.current = 0;
      }
      setControlsVisible(true);
      if (!isScrubbing) armControlsHideTimer();
    },
    [armControlsHideTimer],
  );

  const concealControls = useCallback(() => {
    if (controlsHideTimerRef.current) {
      window.clearTimeout(controlsHideTimerRef.current);
      controlsHideTimerRef.current = 0;
    }
    setControlsVisible(false);
  }, []);

  // 鼠标 / 触控笔在画面上移动视为"用户在看"：保持控件可见并重置空闲计时，移出即收起。
  // 触摸没有 hover，走点击路径。
  const onPointerOverSurface = useCallback(
    (event: ReactPointerEvent) => {
      if (event.pointerType === "touch") return;
      revealControls();
    },
    [revealControls],
  );

  const onPointerExitSurface = useCallback(
    (event: ReactPointerEvent) => {
      if (event.pointerType === "touch") return;
      concealControls();
    },
    [concealControls],
  );

  useEffect(() => {
    revealControls();
    return () => {
      if (controlsHideTimerRef.current) window.clearTimeout(controlsHideTimerRef.current);
    };
  }, [revealControls]);

  useLayoutEffect(() => {
    if (inDocumentPip) return;
    const dock = dockHostRef.current;
    if (!dock) return;
    dock.append(portalHostElement);
    return () => {
      if (portalHostElement.parentNode === dock) dock.removeChild(portalHostElement);
    };
  }, [inDocumentPip, portalHostElement]);

  /** PiP 窗口关闭（pagehide）或退出后：门户宿主搬回页面，并按真实状态复位两个 PiP 状态位。 */
  const returnPlayerFromPipWindow = useEffectEvent(() => {
    if (unmountStartedRef.current) return;
    const dock = dockHostRef.current;
    if (dock && portalHostElement.parentNode !== dock) dock.append(portalHostElement);
    pipWindowRef.current = null;
    setInDocumentPip(false);
    setInPip(Boolean(document.pictureInPictureElement));
  });

  useEffect(() => {
    return () => {
      unmountStartedRef.current = true;
      const pipWindow = pipWindowRef.current;
      pipWindowRef.current = null;
      pipWindow?.close();
      if (portalHostElement.parentNode) portalHostElement.parentNode.removeChild(portalHostElement);
    };
  }, [portalHostElement]);

  /**
   * 丢弃待定换台记录。若过渡期曾对实例临时静音，这里按**过渡前快照**恢复其声音
   * （快照即当时的用户意图；组件 mutedState 可能已被其它路径更新），随后清隔离位，
   * 让音量事件恢复正常写回。
   */
  const discardPendingHandover = useEffectEvent(() => {
    pendingHandoverRef.current = null;
    const silencedSlot = handoverMutedSlotRef.current;
    if (silencedSlot !== null) {
      backendRefOf(silencedSlot).current?.setMuted(handoverMutedSnapshotRef.current ?? mutedState);
      handoverMutedSlotRef.current = null;
      handoverMutedSnapshotRef.current = null;
    }
  });

  /** 把编排层配置（直播追边、会话锚点）同步到指定实例。 */
  const syncBackendRuntimeConfig = useEffectEvent((backend: PlaybackBackend) => {
    backend.setLiveSync(playMode === "live");
    if (sessionAnchor && anchorCalibratedRef.current) backend.setLiveSessionAnchor(sessionAnchor);
  });

  const teardownSlot = useEffectEvent((slot: SlotTag) => {
    backendRefOf(slot).current?.destroy();
    backendRefOf(slot).current = null;
    applyRenderStateIfChanged(slot, IDLE_RENDER_STATE);
  });

  /** 终止进行中的无缝换台：先停掉承接槽上可能已加载的新流，再清记录。 */
  const abortPendingHandover = useEffectEvent(() => {
    const pending = pendingHandoverRef.current;
    if (!pending) return;
    backendRefOf(pending.slotTag).current?.stop();
    discardPendingHandover();
  });

  /** 仅当槽上仍挂着我们认得的实例时才 stop，避免误杀后来者。 */
  const stopBackendIfStillMounted = useEffectEvent((slot: SlotTag, backend: PlaybackBackend) => {
    if (backendRefOf(slot).current !== backend) return;
    backend.stop();
  });

  /** 提交接管：新槽转正并按过渡前快照接管音量，旧槽随后退出。 */
  const commitHandover = useEffectEvent((newSlot: SlotTag) => {
    const oldSlot = currentSlotId();
    const oldBackend = backendRefOf(oldSlot).current;
    // 音量/静音取过渡前的**快照**（当时的用户意图）：既不能从旧实例 state 读、
    // 也不能直接用可能已被其它路径改过的组件状态，否则新实例会被误静音（实测"新台静音"）。
    const savedVolume = volumeLevel;
    const savedMuted = handoverMutedSnapshotRef.current ?? mutedState;

    const newBackend = backendRefOf(newSlot).current;
    if (newBackend) {
      newBackend.setVolume(savedVolume);
      newBackend.setMuted(savedMuted);
      syncBackendRuntimeConfig(newBackend);
    }
    // 过渡结束：旧实例即将 stop，清隔离位让音量事件恢复正常写回。
    handoverMutedSlotRef.current = null;
    handoverMutedSnapshotRef.current = null;

    activeSlotRef.current = newSlot;
    setDisplayedSlot(newSlot);
    setIsLoadingStream(false);

    if (oldSlot !== newSlot && oldBackend) stopBackendIfStillMounted(oldSlot, oldBackend);
  });

  /** 事件到达时若代数、实例、时间戳全部匹配则提交换台；否则视为过时事件忽略。 */
  const maybeCommitHandover = useEffectEvent(
    (slot: SlotTag, eventTimeStamp?: number, expected?: HandoverTicketIdentity): boolean => {
      const pending = pendingHandoverRef.current;
      if (!pending || pending.slotTag !== slot) return false;
      if (pending.generation !== handoverGenerationRef.current) return false;
      if (backendRefOf(slot).current !== pending.backend) return false;
      if (expected && (pending.generation !== expected.generation || pending.backend !== expected.backend)) return false;
      if (eventTimeStamp !== undefined && eventTimeStamp < pending.startedAt) return false;
      discardPendingHandover();
      commitHandover(slot);
      return true;
    },
  );

  const handoverStillValid = useEffectEvent((slot: SlotTag, expected?: HandoverTicketIdentity): boolean => {
    if (!expected) return true;
    const pending = pendingHandoverRef.current;
    return (
      pending?.slotTag === slot &&
      pending.generation === expected.generation &&
      pending.backend === expected.backend &&
      backendRefOf(slot).current === expected.backend
    );
  });

  const handoverForSlot = useEffectEvent((slot: SlotTag): HandoverTicket | undefined => {
    const pending = pendingHandoverRef.current;
    return pending?.slotTag === slot ? pending : undefined;
  });

  /**
   * 无缝换台失败（新流报错 / 起播被拒 / video 元素 error）时的退路：
   * 放弃双槽，把本次 segments 直接在原槽硬加载。
   */
  const demoteToHardReload = useEffectEvent(
    (slot: SlotTag, eventTimeStamp?: number, expected?: HandoverTicketIdentity): boolean => {
      const pending = pendingHandoverRef.current;
      if (!pending || pending.slotTag !== slot) return false;
      if (pending.generation !== handoverGenerationRef.current) return false;
      if (backendRefOf(slot).current !== pending.backend) return false;
      if (expected && (pending.generation !== expected.generation || pending.backend !== expected.backend)) return false;
      if (eventTimeStamp !== undefined && eventTimeStamp < pending.startedAt) return false;
      pending.backend.stop();
      discardPendingHandover();
      ingestSegments(segments, true);
      return true;
    },
  );

  const segmentsForRetry = useEffectEvent((): PlayerSegment[] => segments);

  /**
   * 引擎错误的统一恢复入口，优先级：
   *   无缝换台失败 → 硬切换；
   *   预算内 → 原地重试（回直播头 / 当前位置）；
   *   预算耗尽 → 换源；
   *   无源可换 → 错误面板。
   */
  const recoverFromBackendError = useEffectEvent((playerError: PlayerError, slot: SlotTag) => {
    console.error("Player error:", JSON.stringify(playerError));
    setNotice(null);

    if (pendingHandoverRef.current?.slotTag === slot) {
      demoteToHardReload(slot);
      return;
    }

    const technicalText = composeTechnicalErrorText(playerError);
    const isHttpStatusFailure = playerError.category === "io" && playerError.detail === PlayerErrors.HTTP_STATUS_CODE_INVALID;
    const isUpstreamFailure =
      isHttpStatusFailure || (playerError.category === "io" && playerError.detail === PlayerErrors.REQUEST_FAILED);
    const isCodecFailure = playerError.detail === PlayerErrors.CODEC_UNSUPPORTED;

    // 解码层：把 video.error 的原始信息透出；PIPELINE_ERROR_DECODE 记为可重试。
    let messageText = technicalText || tr("playbackError");
    let failureView: PlaybackFailureView = { message: messageText };
    let isDecodeRetry = false;

    if (
      playerError.category === "media" &&
      playerError.detail === PlayerErrors.MEDIA_MSE_ERROR &&
      playerError.info?.includes("HTMLMediaElement.error")
    ) {
      const video = videoRefOf(slot).current;
      const elementMessage = video?.error?.message;
      if (elementMessage?.includes("PIPELINE_ERROR_DECODE")) isDecodeRetry = true;
      if (elementMessage && !messageText.includes(elementMessage)) messageText += `: ${elementMessage}`;
    }

    if (isUpstreamFailure) {
      const statusText = [playerError.code, playerError.info]
        .filter((value) => value !== undefined && value !== "" && value !== -1)
        .join(" ");
      messageText = `${tr("upstreamRequestFailed")}${
        isHttpStatusFailure && statusText ? `: HTTP ${statusText}` : ""
      }${playerError.url ? ` (${playerError.url})` : ""}`;
      failureView = {
        message: tr("upstreamRequestFailed"),
        description: tr("upstreamRequestFailedDescription"),
        statusCode: isHttpStatusFailure ? playerError.code : undefined,
        statusText: isHttpStatusFailure ? playerError.info : undefined,
        requestUrl: playerError.url ? safelyDecodeUri(playerError.url) : undefined,
        suggestion: tr("upstreamRequestFailedSuggestion"),
      };
    } else if (isCodecFailure) {
      messageText = tr("codecError");
      failureView = { message: messageText, description: technicalText };
    } else {
      failureView = { message: messageText };
    }

    if (attemptCount < attemptFloor + RETRY_BUDGET) {
      setAttemptCount(attemptCount + 1);
      if (isDecodeRetry) setAttemptFloor(attemptFloor + 1);
      recoverySeekFlagRef.current = true;
      if (onSeek) {
        if (playMode === "live") onSeek(new Date(), true);
        else onSeek(mseToWallClock(playheadSecondsRef.current, streamStartTime), false);
      }
      if (playMode === "catchup") suppressNextLoadRef.current = true;
      queueRetryReload(segmentsForRetry());
      return;
    }

    if (channel && onSourceChange && activeSourceIndex + 1 < channel.sources.length) {
      onSourceChange(activeSourceIndex + 1);
      return;
    }

    setFailure(failureView);
    onError?.(messageText);
    setIsLoadingStream(false);
  });

  /**
   * 引擎错误的第一道分流：单轨编码不支持（音频 AC-3/MP2、视频 HEVC/4K 等）只弹
   * 非阻断告警、靠另一轨继续播；**不能**进恢复流程——那会弹错误面板并反复重载。
   */
  const onBackendError = useEffectEvent((playerError: PlayerError, slot: SlotTag) => {
    if (playerError.detail === PlayerErrors.CODEC_UNSUPPORTED) {
      console.error("Player codec warning:", JSON.stringify(playerError));
      setNotice({
        message: playerError.track === "video" ? tr("videoCodecError") : tr("audioCodecError"),
        description: composeTechnicalErrorText(playerError),
      });
      return;
    }
    recoverFromBackendError(playerError, slot);
  });

  // segments 引用变化（换台 / 重试重灌）时，清掉与"旧流"绑定的记忆：播放位置、
  // 墙钟校准、会话锚点。错误恢复导致的重灌（recoverySeekFlagRef 已置位）不重置重试
  // 计数；真正换流才重新给满重试预算。
  const [lastSeenSegments, setLastSeenSegments] = useState(segments);
  if (segments !== lastSeenSegments) {
    setLastSeenSegments(segments);
    playheadSecondsRef.current = 0;
    anchorCalibratedRef.current = false;
    setSessionAnchor(null);

    const isStreamIdentityChange =
      channel != null &&
      lastStreamIdentityRef.current != null &&
      (channel.id !== lastStreamIdentityRef.current.channelId || activeSourceIndex !== lastStreamIdentityRef.current.sourceIndex);

    if (recoverySeekFlagRef.current && !isStreamIdentityChange) recoverySeekFlagRef.current = false;
    else {
      setAttemptCount(0);
      setAttemptFloor(0);
      recoverySeekFlagRef.current = false;
    }
  }

  /** 引擎自己完不成的 seek（如回看需要上层换流）时转交上层。 */
  const onEngineSeekRequest = useEffectEvent((seconds: number) => {
    const backend = activeBackend();
    autoplayIntentRef.current = !backend?.getState().paused;
    const seekTime = mseToWallClock(seconds, streamStartTime);
    onSeek?.(seekTime, isNearLiveWallClock(seekTime, sessionAnchor, streamStartTime));
  });

  const onAudioBlocked = useEffectEvent(() => setRequiresGesture(true));

  /** 为指定槽创建（或复用）后端实例并接好全部事件；同槽同类型实例直接复用。 */
  const buildBackendForSlot = useEffectEvent((slot: SlotTag): PlaybackBackend | null => {
    const video = videoRefOf(slot).current;
    if (!video || (backendKind === "mse" && !isMSEPlaybackSupported())) return null;

    const existing = backendRefOf(slot).current;
    if (existing) {
      if (existing.kind !== backendKind) {
        existing.destroy();
        backendRefOf(slot).current = null;
      } else {
        return existing;
      }
    }

    const backend = createPlaybackBackend(video, {
      softDecoderUrls: builtinWasmDecoders,
      paintCanvas: canvasRefOf(slot).current ?? undefined,
      deinterlaceAuto: autoDeinterlace,
      enhancePicture: pictureEnhancement,
      pcmChannelMix: audioChannelMode,
    });
    backend.setVolume(volumeLevel);
    backend.setMuted(mutedState);
    // 事件回调一律以"实例仍挂在槽上"为闸门：已被替换/销毁的实例不得再驱动 UI。
    backend.on("error", (e) => {
      if (backendRefOf(slot).current === backend) onBackendError(e, slot);
    });
    backend.on("seek-request", (seconds) => {
      if (backendRefOf(slot).current === backend) onEngineSeekRequest(seconds);
    });
    backend.on("live-edge-state", (live) => {
      if (backendRefOf(slot).current !== backend) return;
      // 直播中用户暂停后不追边：引擎报"离开直播态"且当前处于暂停，就解除追边。
      if (slot === currentSlotId() && !live && backend.getState().paused && playMode === "live") backend.setLiveSync(false);
    });
    backend.on("audio-gate-blocked", () => {
      if (backendRefOf(slot).current === backend) onAudioBlocked();
    });
    backend.on("painter-state", (renderState) => {
      if (backendRefOf(slot).current === backend) applyRenderStateIfChanged(slot, renderState);
    });
    backend.on("track-metadata", (mediaInfo) => {
      if (backendRefOf(slot).current === backend)
        setMediaInfoBySlot((previous) => ({ ...previous, [slot]: mediaInfo }));
    });
    backend.on("clock-tick", (time) => {
      if (backendRefOf(slot).current !== backend || slot !== currentSlotId()) return;
      playheadSecondsRef.current = time;
      onCurrentVideoTimeChange(time);
      publishPositionState();
    });
    backend.on("ended", () => {
      if (backendRefOf(slot).current === backend && slot === currentSlotId()) onStreamEnded();
    });
    backend.on("transport-state", (state, eventTimeStamp) => {
      if (backendRefOf(slot).current !== backend) return;
      // 四种传输态各自由独立的槽位处理器消化；switch 只是分发，不携带额外语义。
      switch (state) {
        case "canplay":
          onSlotCanPlay(slot);
          break;
        case "waiting":
          onSlotWaiting(slot);
          break;
        case "playing":
          onSlotPlaying(slot, eventTimeStamp);
          break;
        case "paused":
          onSlotPaused(slot);
          break;
      }
    });
    backend.on("gain-change", (nextVolume, nextMuted) => {
      if (backendRefOf(slot).current !== backend || slot !== currentSlotId()) return;
      // 过渡期临时静音（内部过渡动作而非用户意图）产生的上报一律忽略：写回会污染
      // mutedState 与持久化（下次打开仍静音），并被 commitHandover 带给新实例。
      if (handoverMutedSlotRef.current === slot && nextMuted) return;
      setVolumeLevel(nextVolume);
      setMutedState(nextMuted);
      saveVolume(nextVolume);
      saveMuted(nextMuted);
    });
    syncBackendRuntimeConfig(backend);
    backendRefOf(slot).current = backend;
    return backend;
  });

  /**
   * 起播（含自动播放被拒的兜底）。slot/expected 仅在无缝换台期使用：
   * 起播失败且该次换台仍有效时，退回硬加载。
   */
  const startPlaybackWithFallback = useEffectEvent((slot?: SlotTag, expected?: HandoverTicketIdentity) => {
    pausedByUserRef.current = false;
    const backend = slot ? backendRefOf(slot).current : activeBackend();
    if (!backend) return;
    const playPromise = backend.play();
    if (playPromise) {
      playPromise
        .catch((err: Error) => {
          if (isPlayLoadAbortedError(err)) return;
          if (slot && !handoverStillValid(slot, expected)) return;
          if (slot) {
            demoteToHardReload(slot, undefined, expected);
          } else if (err.name === "NotAllowedError" || err.message.includes("user didn't interact")) {
            setRequiresGesture(true);
          }
        })
        .finally(() => {
          if (!slot || slot === currentSlotId() || handoverStillValid(slot, expected)) setIsLoadingStream(false);
        });
    }
  });

  /** 原槽硬加载：先按用户意图复位静音（实例可能刚从"过渡期静音"复用）→ 清媒体信息 → 灌段 → 起播。 */
  const reloadInPlace = useEffectEvent((backend: PlaybackBackend, slot: SlotTag, newSegments: PlayerSegment[]) => {
    backend.setMuted(mutedState);
    setMediaInfoBySlot((previous) => ({ ...previous, [slot]: null }));
    backend.loadSegments(newSegments);
    if (autoplayIntentRef.current) startPlaybackWithFallback();
    else setIsLoadingStream(false);
  });

  /**
   * 向系统媒体会话上报"节目内位置"（锁屏进度条）。
   * 有节目时间轴且支持回看 → 上报节目时长/位置；否则按无限直播流上报。
   * 引擎拒绝（位置越界等）时降级为清空位置状态。
   */
  const publishPositionState = useEffectEvent((force = false) => {
    if (!("mediaSession" in navigator) || !navigator.mediaSession.setPositionState) return;
    if (!channel) {
      navigator.mediaSession.setPositionState();
      return;
    }
    const now = Date.now();
    if (!force && now - positionStateSyncedAtRef.current < POSITION_STATE_THROTTLE_MS) return;
    const backend = activeBackend();
    if (!backend) return;
    positionStateSyncedAtRef.current = now;
    const transport = backend.getState();

    // 有节目时间轴 → 上报"节目内位置"；时间轴缺席或流不支持回看 → 按无限直播流上报。
    const timeline = currentProgram ? createProgramTimeline(currentProgram, streamStartTime, transport.currentTime) : null;
    const catchupEnabled = channel.sources.some(({ timeshift, timeshiftTemplate }) => Boolean(timeshift && timeshiftTemplate));

    try {
      if (timeline && catchupEnabled) {
        navigator.mediaSession.setPositionState({
          duration: timeline.durationSeconds,
          position: timeline.positionSeconds,
          playbackRate: transport.playbackRate,
        });
      } else {
        navigator.mediaSession.setPositionState({
          duration: Infinity,
          position: Math.max(0, transport.currentTime),
          playbackRate: transport.playbackRate,
        });
      }
    } catch {
      navigator.mediaSession.setPositionState();
    }
  });

  const onRemotePlay = useEffectEvent(() => {
    pausedByUserRef.current = false;
    activeBackend()?.play().catch(dropIfPlayLoadAborted);
  });

  const onRemotePause = useEffectEvent(() => {
    pausedByUserRef.current = true;
    activeBackend()?.pause();
  });

  const onRemoteSeekBackward = useEffectEvent(
    (details: MediaSessionActionDetails) => nudgePlayhead(-(details.seekOffset ?? SEEK_STEP_SECONDS)),
  );

  const onRemoteSeekForward = useEffectEvent(
    (details: MediaSessionActionDetails) => nudgePlayhead(details.seekOffset ?? SEEK_STEP_SECONDS),
  );

  const onRemoteSeekTo = useEffectEvent((details: MediaSessionActionDetails) => {
    if (!currentProgram || details.seekTime === undefined) return;
    const programTimeline = createProgramTimeline(currentProgram, streamStartTime, playheadSecondsRef.current);
    if (!programTimeline) return;
    performSeek(programPositionToWallClock(programTimeline, details.seekTime));
  });

  const onRemotePreviousTrack = useEffectEvent(() => onChannelNavigate?.("prev"));
  const onRemoteNextTrack = useEffectEvent(() => onChannelNavigate?.("next"));

  // 锁屏元数据：有节目 → 标题=节目名、艺术家=频道名；无节目 → 标题=频道名、艺术家=分组。
  // 卡片内容拼装收口到 buildNowPlayingMetadata，effect 只负责"探测支持 + 写入/清空"。
  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    navigator.mediaSession.metadata = buildNowPlayingMetadata(channel, currentProgram);
  }, [channel, currentProgram]);

  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    navigator.mediaSession.playbackState = channel ? (isPlaybackActive ? "playing" : "paused") : "none";
  }, [channel, isPlaybackActive]);

  // 节目时间轴 / 播放槽 / 播放态变化时，锁屏进度立即重算（绕过节流）。
  useEffect(() => {
    publishPositionState(true);
    // biome-ignore lint/correctness/useExhaustiveDependencies: 时间线/槽状态变化时立即同步媒体会话
  }, [channel, currentProgram, playMode, activeSourceIndex, displayedSlot, isPlaybackActive]);

  // 卸载时把系统媒体会话彻底清空，避免"幽灵"锁屏卡片残留。
  useEffect(() => {
    return () => {
      if (!("mediaSession" in navigator) || !navigator.mediaSession.setPositionState) return;
      navigator.mediaSession.metadata = null;
      navigator.mediaSession.playbackState = "none";
      navigator.mediaSession.setPositionState();
    };
  }, []);

  /** segments 的统一入口：空列表忽略；否则清稳定计时、显示加载态，再按情形选路。 */
  const ingestSegments = useEffectEvent((newSegments: PlayerSegment[], forceHardSwitch = false) => {
    if (!newSegments.length) return;

    const activeSlot = currentSlotId();
    const activeBackendInstance = backendRefOf(activeSlot).current ?? buildBackendForSlot(activeSlot);
    if (!activeBackendInstance) return;

    if (playbackStabilityTimerRef.current) {
      window.clearTimeout(playbackStabilityTimerRef.current);
      playbackStabilityTimerRef.current = 0;
    }

    revealControls();
    setIsLoadingStream(true);
    setFailure(null);
    setNotice(null);

    const isStreamSwitch =
      channel != null &&
      lastStreamIdentityRef.current != null &&
      (channel.id !== lastStreamIdentityRef.current.channelId || activeSourceIndex !== lastStreamIdentityRef.current.sourceIndex);
    const activeVideo = videoRefOf(activeSlot).current;
    const activeState = activeBackendInstance.getState();
    const canHandoverSeamlessly =
      !forceHardSwitch &&
      seamlessSwitch &&
      !isAnyPictureInPictureActive() &&
      everPlayedRef.current &&
      isStreamSwitch &&
      playMode === "live" &&
      autoplayIntentRef.current &&
      !activeState.paused;

    if (channel) lastStreamIdentityRef.current = { channelId: channel.id, sourceIndex: activeSourceIndex };

    if (!canHandoverSeamlessly) {
      abortPendingHandover();
      reloadInPlace(activeBackendInstance, activeSlot, newSegments);
      return;
    }

    discardPendingHandover();
    handoverGenerationRef.current += 1;
    const generation = handoverGenerationRef.current;

    const pendingSlot = siblingSlotOf(activeSlot);
    backendRefOf(pendingSlot).current?.stop();

    const pendingBackend = buildBackendForSlot(pendingSlot);
    const pendingVideo = videoRefOf(pendingSlot).current;
    if (!pendingBackend || !pendingVideo) {
      reloadInPlace(activeBackendInstance, activeSlot, newSegments);
      return;
    }

    if (activeVideo) {
      pendingBackend.setVolume(activeState.volume);
      pendingBackend.setMuted(true);
    }
    // 无缝过渡期旧实例保持"画面+声音"正常播放：先静音旧实例会造成"还没切过去就哑了"
    // 的断音（用户实测反馈）。声音的切换点交给 commitHandover —— 新实例 playing（新画面
    // 出现）时同步 stop 旧实例，画面与声音一起切。过渡期偏长（旧声残留）的根因是新流
    // 起播速度，而非静音时机。隔离位 handoverMutedSlotRef 保留：音量事件与恢复路径对
    // "过渡期内部静音"已有防护，未来若再引入临时静音不会污染应用状态。

    const handover: HandoverTicket = { generation, slotTag: pendingSlot, backend: pendingBackend, startedAt: performance.now() };
    pendingHandoverRef.current = handover;
    setMediaInfoBySlot((previous) => ({ ...previous, [pendingSlot]: null }));
    pendingBackend.loadSegments(newSegments);

    if (autoplayIntentRef.current) startPlaybackWithFallback(pendingSlot, handover);
    else setIsLoadingStream(false);
  });

  /** 错误恢复专用重灌：绕过换流判断，直接走 ingestSegments。 */
  const queueRetryReload = useEffectEvent((newSegments: PlayerSegment[]) => ingestSegments(newSegments));

  // 卸载：丢弃换台记录并销毁两个槽。
  useEffect(() => {
    return () => {
      discardPendingHandover();
      teardownSlot("a");
      teardownSlot("b");
    };
  }, []);

  useEffect(() => {
    backendARef.current?.setAutoDeinterlace(autoDeinterlace);
    backendBRef.current?.setAutoDeinterlace(autoDeinterlace);
  }, [autoDeinterlace]);

  useEffect(() => {
    backendARef.current?.setPictureEnhancement(pictureEnhancement);
    backendBRef.current?.setPictureEnhancement(pictureEnhancement);
  }, [pictureEnhancement]);

  // 声道合成模式运行时可切，无需重建实例。
  useEffect(() => {
    backendARef.current?.setAudioChannelMode(audioChannelMode);
    backendBRef.current?.setAudioChannelMode(audioChannelMode);
  }, [audioChannelMode]);

  // 无缝换台能力被关闭时，立刻终止任何进行中的换台。
  useEffect(() => {
    if (!seamlessSwitch) abortPendingHandover();
  }, [seamlessSwitch]);

  useEffect(() => {
    const liveSync = playMode === "live";
    backendARef.current?.setLiveSync(liveSync);
    backendBRef.current?.setLiveSync(liveSync);
  }, [playMode]);

  useEffect(() => {
    if (!sessionAnchor) return;
    backendARef.current?.setLiveSessionAnchor(sessionAnchor);
    backendBRef.current?.setLiveSessionAnchor(sessionAnchor);
  }, [sessionAnchor]);

  useEffect(() => {
    if (suppressNextLoadRef.current) {
      suppressNextLoadRef.current = false;
      return;
    }
    ingestSegments(segments);
  }, [segments]);

  const previousPlayModeRef = useRef(playMode);
  useEffect(() => {
    // 回看 → 直播：强制硬切回直播头（显式清 suppress，保证真的回到边缘）。
    if (previousPlayModeRef.current === "catchup" && playMode === "live") {
      suppressNextLoadRef.current = false;
      ingestSegments(segments, true);
    }
    previousPlayModeRef.current = playMode;
  }, [playMode, segments]);

  const onSlotCanPlay = useEffectEvent((slot: SlotTag) => {
    if (slot !== currentSlotId() && pendingHandoverRef.current?.slotTag !== slot) return;
    setIsLoadingStream(false);
  });

  const onSlotWaiting = useEffectEvent((slot: SlotTag) => {
    if (slot !== currentSlotId() && pendingHandoverRef.current?.slotTag !== slot) return;
    setIsLoadingStream(true);
    if (playbackStabilityTimerRef.current) {
      window.clearTimeout(playbackStabilityTimerRef.current);
      playbackStabilityTimerRef.current = 0;
    }
  });

  const onSlotPlaying = useEffectEvent((slot: SlotTag, eventTimeStamp: number) => {
    const pending = handoverForSlot(slot);
    if (pending) maybeCommitHandover(slot, eventTimeStamp, pending);
    if (slot !== currentSlotId()) return;

    everPlayedRef.current = true;
    setIsLoadingStream(false);
    setPlaybackActive(true);
    onPlaybackStarted?.();

    const backend = backendRefOf(slot).current;
    if (playMode === "live" && backend && !anchorCalibratedRef.current) {
      anchorCalibratedRef.current = true;
      rebaseSessionClock(backend);
    }

    // 持续播放满 PLAYBACK_STABILITY_WINDOW_MS，把试错期攒下的重试计数固化为新基线，
    // 后续错误重新拥有完整预算。
    if (playbackStabilityTimerRef.current) window.clearTimeout(playbackStabilityTimerRef.current);
    playbackStabilityTimerRef.current = window.setTimeout(() => {
      if (attemptCount > attemptFloor) setAttemptFloor(attemptCount);
    }, PLAYBACK_STABILITY_WINDOW_MS);
  });

  const onSlotPaused = useEffectEvent((slot: SlotTag) => {
    if (slot !== currentSlotId()) return;
    setPlaybackActive(false);
    if (playbackStabilityTimerRef.current) {
      window.clearTimeout(playbackStabilityTimerRef.current);
      playbackStabilityTimerRef.current = 0;
    }
  });

  const onSlotTimelineJump = useEffectEvent((slot: SlotTag) => {
    if (slot !== currentSlotId()) return;
    publishPositionState(true);
  });

  const onStreamEnded = useEffectEvent(() => {
    const backend = activeBackend();
    const duration = backend?.getState().duration;
    if (onSeek && duration && Number.isFinite(duration)) {
      const seekTime = mseToWallClock(duration, streamStartTime);
      onSeek(seekTime, true);
    }
  });

  const onSlotEnterPip = useEffectEvent((slot: SlotTag) => {
    if (slot !== currentSlotId()) return;
    setInPip(true);
  });

  // video 级 PiP 的关闭可能意味着切去了 Document PiP：不要贸然把状态打回"无 PiP"。
  const onSlotLeavePip = useEffectEvent(() => setInPip(inDocumentPip || Boolean(document.pictureInPictureElement)));

  /** 页面回到前台的自愈：媒体死了 / 大幅滞后直播 → 重拉；否则能播就接着播。 */
  const onPageVisibleAgain = useEffectEvent(() => {
    if (document.visibilityState !== "visible") return;
    const video = activeVideoElement();
    const backend = activeBackend();
    if (!video || !backend || failure || requiresGesture) return;
    if (isAnyPictureInPictureActive()) return;
    if (pausedByUserRef.current) return;

    const mediaDead = video.error !== null;
    const behindLiveMs = Date.now() - mseToWallClock(playheadSecondsRef.current, streamStartTime).getTime();
    const staleLiveMs = (defaultConfig.chaseMaxLagSeconds + LIVE_LAG_GRACE_SECONDS) * 1000;

    if (playMode === "live" && (mediaDead || behindLiveMs > staleLiveMs)) {
      autoplayIntentRef.current = true;
      onSeek?.(new Date(), true);
      return;
    }

    if (mediaDead) {
      autoplayIntentRef.current = true;
      const seekTime = mseToWallClock(playheadSecondsRef.current, streamStartTime);
      onSeek?.(seekTime, isNearLiveWallClock(seekTime, sessionAnchor, streamStartTime));
      return;
    }

    if (backend.getState().paused) {
      backend.play().catch((err: Error) => {
        if (isPlayLoadAbortedError(err)) return;
        if (err.name === "NotAllowedError") setRequiresGesture(true);
      });
    }
  });

  useEffect(() => {
    const handler = () => onPageVisibleAgain();
    document.addEventListener("visibilitychange", handler);
    return () => document.removeEventListener("visibilitychange", handler);
  }, []);

  /**
   * 停摆看门狗：兜住传输层重连也解决不了的整体停摆（如网络切换后旧连接静默挂死）。
   * 两种处置：
   *  - 缓冲里还有数据却推不动（被 UA 挂起等）→ 补播一次，不重建（重建会白丢缓冲）；
   *  - 缓冲已耗尽（断流）→ 复用错误恢复路径，按直播边缘 / 当前位置重建会话。
   *
   * 时间轴自身的自愈由传输层（fetch-loader 自动重连 + 直播无数据看门狗）与 worker
   * （重连后重钉时间轴）承担；本层只承诺"永不永久卡死"。判定阈值保守，连续介入按
   * 30s→60s→120s→180s 退避，避免弱网下的重建风暴。
   */
  const patrolStallWatchdog = useEffectEvent(() => {
    if (document.visibilityState !== "visible") return;
    if (pausedByUserRef.current || requiresGesture || failure) return;
    if (isAnyPictureInPictureActive()) return;
    const video = activeVideoElement();
    const backend = activeBackend();
    if (!video || !backend) return;
    if (backend.getState().paused || video.seeking) return;

    const now = performance.now();
    const clockNow = video.currentTime;
    // 时钟在动：一切正常，重置采样并清空连击计数。
    if (Math.abs(clockNow - stallLastClockRef.current) > STALL_CLOCK_EPSILON_SECONDS) {
      stallLastClockRef.current = clockNow;
      stallSinceRef.current = now;
      stallStrikeRef.current = 0;
      return;
    }
    // 尚未出首帧（换台/重建冷启动）：交给既有起播超时判定，不累计停摆时长。
    if (video.readyState < HTMLMediaElement.HAVE_CURRENT_DATA) {
      stallSinceRef.current = now;
      return;
    }

    const stalledFor = now - stallSinceRef.current;
    if (stalledFor < STALL_THRESHOLD_MS) return;
    // 连续介入按指数退避冷却：弱网下不反复重建会话（30s → 60s → 120s → 180s）。
    const cooldown = Math.min(
      STALL_BACKOFF_BASE_MS * 2 ** Math.max(0, stallStrikeRef.current - 1),
      STALL_BACKOFF_CAP_MS,
    );
    if (now - stallLastActionRef.current < cooldown) return;

    stallLastClockRef.current = clockNow;
    stallSinceRef.current = now;
    stallLastActionRef.current = now;

    const state = backend.getState();
    const webkitProbe = video as HTMLVideoElement & { webkitDecodedFrameCount?: number };
    const decodeStuck =
      webkitProbe.webkitDecodedFrameCount !== undefined && webkitProbe.webkitDecodedFrameCount === stallDecodeSampleRef.current;
    stallDecodeSampleRef.current = webkitProbe.webkitDecodedFrameCount ?? -1;

    if (bufferedSecondsAheadOfPlayhead(video) > MIN_BUFFERED_AHEAD_TO_RESUME_SECONDS && !decodeStuck) {
      // 数据在手却停着：补播一次即可，重建会白丢已缓冲内容。
      console.warn(`Playback stalled ${Math.round(stalledFor)}ms with data buffered; resuming playback`);
      backend.play().catch(() => {});
      return;
    }

    stallStrikeRef.current += 1;
    console.warn(
      `Playback stalled ${Math.round(stalledFor)}ms with no usable buffer (readyState=${video.readyState}, ` +
        `paused=${state.paused}, decodeStuck=${decodeStuck}); rebuilding the stream`,
    );
    autoplayIntentRef.current = true;
    if (playMode === "live") {
      onSeek?.(new Date(), true);
    } else {
      const seekTime = mseToWallClock(playheadSecondsRef.current, streamStartTime);
      onSeek?.(seekTime, isNearLiveWallClock(seekTime, sessionAnchor, streamStartTime));
    }
  });

  useEffect(() => {
    const patrolTimer = window.setInterval(() => patrolStallWatchdog(), STALL_PATROL_INTERVAL_MS);
    return () => window.clearInterval(patrolTimer);
  }, []);

  /** 静音键语义：音量为 0 时先恢复满音量再取消静音；否则按当前静音态取反。 */
  const flipMute = useEffectEvent(() => {
    const backend = activeBackend();
    if (!backend) return;
    const state = backend.getState();
    if (state.volume <= 0) {
      backend.setVolume(1);
      backend.setMuted(false);
      return;
    }
    backend.setMuted(!state.muted);
  });

  /**
   * 全局快捷键（含 Document PiP 窗口）。
   * 数字键进入"频道号输入"：继续输入则拼接，1 秒无输入按已输号码跳台，Enter 立即提交。
   * 其余按键在文本输入焦点处一律放行。
   */
  const dispatchHotkey = useEffectEvent((e: KeyboardEvent) => {
    const eventDocument = ownerDocumentOfEvent(e);
    if (isTextEntryTarget(e.target)) return;

    if (/^[0-9]$/.test(e.key)) {
      e.preventDefault();
      revealControls();
      if (digitCommitTimerRef.current) window.clearTimeout(digitCommitTimerRef.current);
      const nextDigits = channelDigits + e.key;
      digitCommitTimerRef.current = window.setTimeout(() => {
        onChannelNavigate?.(parseInt(nextDigits, 10));
        setChannelDigits("");
        digitCommitTimerRef.current = 0;
      }, DIGIT_COMMIT_DELAY_MS);
      setChannelDigits(nextDigits);
      return;
    }

    switch (e.key) {
      case " ":
        // 空格仅在焦点不在具体控件上时才作播放/暂停，避免按钮被"二次触发"。
        if (!isFocusOnPageBody(eventDocument)) break;
        e.preventDefault();
        flipPlayback();
        break;
      case "m":
      case "M":
        e.preventDefault();
        flipMute();
        break;
      case "f":
      case "F":
        e.preventDefault();
        onFullscreenToggle?.();
        break;
      case "ArrowUp":
      case "PageDown":
      case "ChannelDown":
        e.preventDefault();
        releaseFocusedElement(eventDocument);
        onChannelNavigate?.("prev");
        break;
      case "ArrowDown":
      case "PageUp":
      case "ChannelUp":
        e.preventDefault();
        releaseFocusedElement(eventDocument);
        onChannelNavigate?.("next");
        break;
      case "ArrowLeft":
        e.preventDefault();
        releaseFocusedElement(eventDocument);
        nudgePlayhead(-SEEK_STEP_SECONDS);
        break;
      case "ArrowRight":
        e.preventDefault();
        releaseFocusedElement(eventDocument);
        nudgePlayhead(SEEK_STEP_SECONDS);
        break;
      case "Enter":
        if (channelDigits) {
          e.preventDefault();
          if (digitCommitTimerRef.current) {
            window.clearTimeout(digitCommitTimerRef.current);
            digitCommitTimerRef.current = 0;
          }
          onChannelNavigate?.(parseInt(channelDigits, 10));
          setChannelDigits("");
        } else if (isFocusOnPageBody(eventDocument)) {
          e.preventDefault();
          onToggleSidebar?.();
        }
        break;
      case "Escape":
        e.preventDefault();
        // 优先级：先让控件交还焦点 → 清未提交的号码 → 再做控件显隐切换。
        if (!isFocusOnPageBody(eventDocument)) {
          releaseFocusedElement(eventDocument);
        } else if (channelDigits) {
          setChannelDigits("");
          if (digitCommitTimerRef.current) {
            window.clearTimeout(digitCommitTimerRef.current);
            digitCommitTimerRef.current = 0;
          }
        } else if (controlsVisible) {
          concealControls();
        } else {
          revealControls();
        }
        break;
    }
  });

  const onVideoElementError = useEffectEvent((slot: SlotTag, eventTimeStamp: number) => {
    demoteToHardReload(slot, eventTimeStamp);
  });

  // video 元素级事件：seek/rate 变化刷新锁屏进度；PiP 进出维护状态位；
  // 元素 error（区别于引擎 error）用于把待定的无缝换台降级为硬加载。
  useEffect(() => {
    const attachSlot = (slot: SlotTag) => {
      const video = videoRefOf(slot).current;
      if (!video) return () => {};
      const listeners: Array<[string, EventListener]> = [
        ["seeked", () => onSlotTimelineJump(slot)],
        ["ratechange", () => onSlotTimelineJump(slot)],
        ["enterpictureinpicture", () => onSlotEnterPip(slot)],
        ["leavepictureinpicture", () => onSlotLeavePip()],
        ["error", (event) => onVideoElementError(slot, event.timeStamp)],
      ];
      for (const [event, listener] of listeners) video.addEventListener(event, listener);
      return () => {
        for (const [event, listener] of listeners) video.removeEventListener(event, listener);
      };
    };

    const detachA = attachSlot("a");
    const detachB = attachSlot("b");

    return () => {
      detachA();
      detachB();
      if (playbackStabilityTimerRef.current) {
        window.clearTimeout(playbackStabilityTimerRef.current);
        playbackStabilityTimerRef.current = 0;
      }
      if (digitCommitTimerRef.current) {
        window.clearTimeout(digitCommitTimerRef.current);
        digitCommitTimerRef.current = 0;
      }
    };
  }, []);

  // 快捷键监听跟随播放器所在窗口：进入 Document PiP 后，PiP 窗口也要能收按键。
  useEffect(() => {
    const pipWindow = inDocumentPip ? pipWindowRef.current : null;
    const targetWindows = pipWindow && pipWindow !== window ? [window, pipWindow] : [window];
    for (const targetWindow of targetWindows) targetWindow.addEventListener("keydown", dispatchHotkey);
    return () => {
      for (const targetWindow of targetWindows) targetWindow.removeEventListener("keydown", dispatchHotkey);
    };
  }, [inDocumentPip]);

  const changeVolume = useEffectEvent((newVolume: number) => {
    const backend = activeBackend();
    if (backend) {
      backend.setVolume(newVolume);
      // 主动拖音量即"想听"：从静音态上调音量时顺带解除静音。
      if (backend.getState().muted && newVolume > 0) backend.setMuted(false);
    }
  });

  const { indicator: gestureIndicator, consumeSuppressedClick, gestureHandlers } = usePlayerTouchGestures({
    enabled: Boolean(channel) && !failure && !requiresGesture,
    enableSeekGesture: catchupAvailable,
    enableVolumeGesture: volumeControlAvailable,
    volume: volumeLevel,
    isMuted: mutedState,
    prevChannel,
    nextChannel,
    onVolumeChange: changeVolume,
    onChannelNavigate,
    onRelativeSeek: nudgePlayhead,
    onTogglePlayPause: flipPlayback,
    onShowControls: revealControls,
  });

  // 点击画面（或其命中代理层）：切换控件显隐；手势滑动产生的合成点击在此被拦截。
  const onSurfaceActivated = useCallback(
    (event: ReactMouseEvent) => {
      const target = event.target as HTMLElement;
      if (target !== event.currentTarget && target.tagName !== "VIDEO" && !("playerSurfaceHit" in target.dataset)) return;
      if (consumeSuppressedClick()) return;
      if (controlsVisible) concealControls();
      else revealControls();
      onSurfaceClick?.();
    },
    [controlsVisible, concealControls, revealControls, consumeSuppressedClick, onSurfaceClick],
  );

  /** 关闭任何形式的 PiP；返回是否确实关闭了什么。 */
  const closePip = useEffectEvent(async (): Promise<boolean> => {
    const documentPictureInPicture = getDocumentPictureInPicture();
    const pipWindow = documentPictureInPicture?.window ?? pipWindowRef.current;
    if (pipWindow) {
      returnPlayerFromPipWindow();
      pipWindow.close();
      return true;
    }
    if (document.pictureInPictureElement) {
      await document.exitPictureInPicture();
      return true;
    }
    return false;
  });

  /** 全屏入口：先退 PiP（互斥）；iOS 上走 webkit 的元素级全屏，失败再走标准全屏。 */
  const toggleScreenMode = useEffectEvent(async () => {
    const isIOS = /iPhone|iPod/.test(navigator.userAgent);
    await closePip();
    const video = activeVideoElement();
    if (isIOS && video) {
      const iosVideo = video as HTMLVideoElement & { webkitSupportsFullscreen?: boolean; webkitEnterFullscreen?: () => void };
      if (iosVideo.webkitSupportsFullscreen && iosVideo.webkitEnterFullscreen) {
        try {
          iosVideo.webkitEnterFullscreen();
          return;
        } catch {
          // 继续走标准全屏方案。
        }
      }
    }
    await onFullscreenToggle?.();
  });

  const openVideoElementPip = useEffectEvent(async (video: HTMLVideoElement) => {
    if (!document.pictureInPictureEnabled || !video.requestPictureInPicture) return;
    await video.requestPictureInPicture();
  });

  /** 进入画中画：优先 Document PiP（整个 UI 搬进小窗）；被策略拦截则退回 video 级 PiP。 */
  const openPip = useEffectEvent(async () => {
    if (isAnyPictureInPictureActive()) return;
    const backend = activeBackend();
    if (!backend) return;
    const video = backend.mediaElement;
    let openedPipWindow: Window | null = null;

    try {
      const documentPictureInPicture = pictureInPictureMode === "document" ? getDocumentPictureInPicture() : null;
      if (documentPictureInPicture) {
        const surfaceElement = surfaceRef.current;
        if (!surfaceElement) return;
        const pipWindowOptions = getDocumentPiPWindowOptions(surfaceElement);
        let pipWindow: Window;
        try {
          pipWindow = await documentPictureInPicture.requestWindow(pipWindowOptions);
        } catch (err) {
          if (isDocumentPictureInPictureBlockedError(err)) {
            await openVideoElementPip(video);
            return;
          }
          throw err;
        }
        openedPipWindow = pipWindow;
        pipWindowRef.current = pipWindow;
        setupDocumentPiPWindow(pipWindow);
        pipWindow.addEventListener("pagehide", () => returnPlayerFromPipWindow(), { once: true });
        setInDocumentPip(true);
        setInPip(true);
        revealControls();
        pipWindow.document.body.append(portalHostElement);
        return;
      }
      await openVideoElementPip(video);
    } catch (err) {
      const pipWindow = openedPipWindow ?? pipWindowRef.current;
      returnPlayerFromPipWindow();
      pipWindow?.close();
      console.error("Picture-in-Picture error:", err);
    }
  });

  const flipPip = useEffectEvent(async () => {
    if (await closePip()) return;
    await openPip();
  });

  const onRemoteEnterPip = useEffectEvent(() => void openPip());

  // 锁屏/系统媒体面板按键 → 组件内处理器；能力不满足的 action 显式置 null（清掉旧监听）。
  useEffect(() => {
    if (!("mediaSession" in navigator)) return;
    const mediaSession = navigator.mediaSession;
    applyMediaSessionAction(mediaSession, "play", onRemotePlay);
    applyMediaSessionAction(mediaSession, "pause", onRemotePause);
    applyMediaSessionAction(mediaSession, "previoustrack", mediaSessionZapEnabled ? onRemotePreviousTrack : null);
    applyMediaSessionAction(mediaSession, "nexttrack", mediaSessionZapEnabled ? onRemoteNextTrack : null);
    applyMediaSessionAction(mediaSession, "seekbackward", mediaSessionSeekEnabled ? onRemoteSeekBackward : null);
    applyMediaSessionAction(mediaSession, "seekforward", mediaSessionSeekEnabled ? onRemoteSeekForward : null);
    applyMediaSessionAction(mediaSession, "seekto", mediaSessionSeekEnabled ? onRemoteSeekTo : null);
    applyMediaSessionAction(mediaSession, "enterpictureinpicture", isPictureInPictureSupported() ? onRemoteEnterPip : null);
    return () => {
      applyMediaSessionAction(mediaSession, "play", null);
      applyMediaSessionAction(mediaSession, "pause", null);
      applyMediaSessionAction(mediaSession, "previoustrack", null);
      applyMediaSessionAction(mediaSession, "nexttrack", null);
      applyMediaSessionAction(mediaSession, "seekbackward", null);
      applyMediaSessionAction(mediaSession, "seekforward", null);
      applyMediaSessionAction(mediaSession, "seekto", null);
      applyMediaSessionAction(mediaSession, "enterpictureinpicture", null);
    };
  }, [mediaSessionZapEnabled, mediaSessionSeekEnabled]);

  /** 自动播放被拦后，用户点击/按键的解锁入口：乐观置为播放态，失败再进错误面板。 */
  const resumeByUserGesture = useEffectEvent(() => {
    const backend = activeBackend();
    if (!backend) return;
    setRequiresGesture(false);
    setPlaybackActive(true);
    pausedByUserRef.current = false;
    backend.play().catch((err: Error) => {
      if (isPlayLoadAbortedError(err)) return;
      console.error("Play error after user interaction:", err);
      setFailure({ message: `${tr("failedToPlay")}: ${err.message}` });
      onError?.(`${tr("failedToPlay")}: ${err.message}`);
    });
  });

  // 解锁监听要覆盖两份文档：主文档 + Document PiP 窗口（若有）。
  useEffect(() => {
    if (!requiresGesture) return;
    const handler = () => resumeByUserGesture();
    const pipDocument = inDocumentPip ? pipWindowRef.current?.document : null;
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
  }, [requiresGesture, inDocumentPip]);

  /* ---------------------------------------------------------------- */
  /* 渲染                                                              */
  /* ---------------------------------------------------------------- */

  const isVideoWindowPip = inPip && !inDocumentPip;

  // 左上角徽标文案：多源台标注当前源（显式别名优先，否则"源 N"）；重试中追加进度。
  const sourceLabelPrefix =
    channel && channel.sources.length > 1
      ? `[${channel.sources[activeSourceIndex]?.alias || `${tr("source")} ${activeSourceIndex + 1}`}] `
      : "";
  const retryProgressSuffix = attemptCount - attemptFloor > 0 ? ` (${attemptCount - attemptFloor}/${RETRY_BUDGET})` : "";
  const topLeftBadgeText = `${sourceLabelPrefix}${tr("loadingVideo")}${retryProgressSuffix}`;

  // 双槽按"非显示槽在下、显示槽在上"的顺序渲染：换台瞬间新画面天然盖住旧画面。
  const videoStage = (
    <div className="absolute inset-0 overflow-hidden">
      {(displayedSlot === "a" ? (["b", "a"] as const) : (["a", "b"] as const)).map((slot) => (
        <div key={slot} className="contents">
          <video
            ref={videoRefOf(slot)}
            className={clsx(
              // object-contain 让任意源比例居中留边展示：不拉伸变形，也不影响外层布局尺寸。
              "absolute inset-0 size-full min-h-0 min-w-0 object-contain",
              slot !== displayedSlot && "opacity-0 pointer-events-none",
              slot === displayedSlot && slotPaintActive[slot] && !isVideoWindowPip && "opacity-0",
            )}
            playsInline
            webkit-playsinline="true"
            x5-playsinline="true"
          />
          <canvas
            ref={canvasRefOf(slot)}
            className={clsx(
              "pointer-events-none absolute inset-0 size-full min-h-0 min-w-0 object-contain",
              (isVideoWindowPip || slot !== displayedSlot || !slotPaintActive[slot]) && "hidden",
            )}
          />
        </div>
      ))}
    </div>
  );

  // 手势命中层：盖住全画面但让出控件（z-[1]），触控手势回调都从这里进来。
  const gestureHitLayer = !requiresGesture && !failure && (
    <div aria-hidden="true" data-player-surface-hit="" className="absolute inset-0 z-[1] touch-none select-none" {...gestureHandlers} />
  );

  const topLeftStatusBadge = !requiresGesture && !failure && (
    <ClockAndLoadingBadge
      badgeVisible={controlsVisible || showSpinner}
      badgeLoading={showSpinner}
      badgeText={topLeftBadgeText}
    />
  );

  const channelIdentityCard = channel && (
    <div
      className={clsx(
        "player-performance-motion absolute top-4 right-4 z-10 flex flex-col items-end gap-2 transition-opacity duration-300 md:top-8 md:right-8 md:gap-3 [@container_video_(max-height:_320px)]:top-2 [@container_video_(max-height:_320px)]:right-2 [@container_video_(max-height:_320px)]:gap-1 md:[@container_video_(max-height:_320px)]:top-2 md:[@container_video_(max-height:_320px)]:right-2 md:[@container_video_(max-height:_320px)]:gap-1 [@container_video_(max-height:_220px)]:top-1 [@container_video_(max-height:_220px)]:right-1 md:[@container_video_(max-height:_220px)]:top-1 md:[@container_video_(max-height:_220px)]:right-1",
        controlsVisible ? "opacity-100" : "opacity-0 pointer-events-none",
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
          /* 台标无论画面多矮都缩小保留、绝不隐藏——它是"正在看什么"的第一识别信息；
             加载失败可在组件内部自愈（见 channel-logo.tsx）。 */
          <ChannelLogo
            src={channel.logo}
            alt={channel.name}
            imgClassName="relative z-10 h-8 w-20 object-contain drop-shadow-[0_0_14px_rgba(var(--pg-rgb-light),0.2)] md:h-14 md:w-36 [@container_video_(max-height:_320px)]:h-6 [@container_video_(max-height:_320px)]:w-16 md:[@container_video_(max-height:_320px)]:h-6 md:[@container_video_(max-height:_320px)]:w-16 [@container_video_(max-height:_220px)]:h-5 [@container_video_(max-height:_220px)]:w-12 md:[@container_video_(max-height:_220px)]:h-5 md:[@container_video_(max-height:_220px)]:w-12"
          />
        )}
        <div className="relative z-10 flex w-full min-w-0 items-center justify-center">
          <div className="flex min-w-0 items-center gap-1.5 md:gap-2 [@container_video_(max-height:_320px)]:gap-1 md:[@container_video_(max-height:_320px)]:gap-1">
            <span
              className={clsx(
                "player-performance-motion shrink-0 rounded-md px-1 py-0.5 font-semibold text-[10px] transition-[color,background-color,box-shadow,scale] duration-300 md:px-1.5 md:text-xs md:[@container_video_(max-height:_320px)]:px-1 md:[@container_video_(max-height:_320px)]:text-[10px]",
                channelDigits
                  ? "scale-110 bg-violet-600 bg-[linear-gradient(135deg,var(--pg-grad-a),var(--pg-grad-c))] text-white shadow-[0_0_20px_rgba(var(--pg-rgb),0.45)] ring-2 ring-violet-200/40"
                  : "bg-violet-100/10 text-violet-50/65 ring-1 ring-violet-100/10",
              )}
            >
              {/* 正在输入时显示输入中的数字；空闲时显示订阅序位作为频道号。 */}
              {channelDigits || (channel.number ?? "")}
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
  );

  // 自动播放被浏览器拦截：整屏"点击起播"门；点击或任意按键（见上方文档监听）都会解锁。
  const resumePlaybackGate = requiresGesture && (
    <button
      type="button"
      className="player-performance-overlay-background player-performance-motion absolute inset-0 z-10 flex cursor-pointer items-center justify-center border-none bg-[radial-gradient(circle_at_center,rgba(18,50,91,0.78),rgba(2,6,23,0.94)_68%)] p-4 transition-[filter,background-color] backdrop-blur-[2px] hover:brightness-110"
      onClick={resumeByUserGesture}
    >
      <div className="flex flex-col items-center gap-4 text-white">
        <Play className="h-20 w-20 fill-violet-100/20 text-violet-100 opacity-95 drop-shadow-[0_0_24px_rgba(var(--pg-rgb),0.55)]" />
        <div className="max-w-lg px-2 text-center">
          <div className="mb-2 font-semibold text-2xl tracking-tight text-violet-50">{tr("clickToPlay")}</div>
          <div className="text-pretty text-violet-50/65 text-sm leading-5">{tr("autoplayBlocked")}</div>
        </div>
      </div>
    </button>
  );

  const warningBanner = notice && !failure && (
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
            <div className="font-medium text-amber-50 text-sm md:text-base">{notice.message}</div>
            {notice.description && <div className="mt-1 break-words font-mono text-amber-50/65 text-xs leading-relaxed">{notice.description}</div>}
          </div>
          <button
            type="button"
            className="player-performance-motion -m-1 shrink-0 cursor-pointer rounded-lg p-1.5 text-amber-100/65 transition-colors hover:bg-white/10 hover:text-amber-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-amber-200/70"
            aria-label={tr("dismiss")}
            title={tr("dismiss")}
            onClick={() => setNotice(null)}
          >
            <X className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>
  );

  const errorPanel = failure && (
    <div className="player-performance-error-backdrop player-performance-overlay-background absolute inset-0 z-10 flex items-center justify-center bg-[radial-gradient(circle_at_center,rgba(76,20,55,0.46),rgba(2,6,23,0.96)_72%)] p-3 backdrop-blur-[3px] md:p-4">
      <div
        className={clsx(
          PLAYER_OVERLAY_SURFACE_CLASS,
          "player-performance-error-background player-performance-overlay-background max-h-full w-full max-w-xl overflow-y-auto rounded-2xl border-rose-300/25 bg-[linear-gradient(145deg,rgba(52,18,50,0.82),rgba(12,22,51,0.8))] p-4 text-white shadow-[0_20px_60px_rgba(43,5,32,0.58)] [@media(max-height:360px)]:p-2.5 md:p-5",
        )}
      >
        <div className="flex items-center gap-2 font-semibold text-lg text-rose-100">
          <CircleAlert className="h-5 w-5 shrink-0" aria-hidden="true" />
          {tr("playbackError")}
        </div>
        <div className="mt-2 break-words font-medium text-pretty text-rose-50 text-sm leading-relaxed">{failure.message}</div>
        {failure.description && (
          <div className="mt-1 break-words text-pretty text-rose-50/70 text-xs leading-relaxed [@media(max-height:360px)]:hidden md:text-sm">
            {failure.description}
          </div>
        )}
        {(failure.statusCode !== undefined || failure.requestUrl) && (
          <div className="mt-3 grid gap-2 text-xs [@media(max-height:360px)]:mt-2 [@media(max-height:360px)]:gap-1 md:text-sm">
            {failure.statusCode !== undefined && (
              <div className="grid grid-cols-[auto_1fr] items-baseline gap-3 rounded-lg bg-black/20 px-3 py-2 [@media(max-height:360px)]:py-1.5">
                <span className="text-rose-100/55">{tr("httpStatus")}</span>
                <span className="min-w-0 font-mono text-rose-50">
                  {failure.statusCode}
                  {failure.statusText ? ` ${failure.statusText}` : ""}
                </span>
              </div>
            )}
            {failure.requestUrl && (
              <div className="rounded-lg bg-black/20 px-3 py-2 [@media(max-height:360px)]:grid [@media(max-height:360px)]:grid-cols-[auto_1fr] [@media(max-height:360px)]:items-baseline [@media(max-height:360px)]:gap-3 [@media(max-height:360px)]:py-1.5">
                <div className="mb-1 text-rose-100/55 [@media(max-height:360px)]:mb-0">{tr("requestUrl")}</div>
                <div className="min-w-0 whitespace-normal break-all font-mono text-rose-50" title={failure.requestUrl}>
                  {failure.requestUrl}
                </div>
              </div>
            )}
          </div>
        )}
        {failure.suggestion && (
          <div className="mt-3 rounded-lg border border-amber-200/15 bg-amber-100/8 px-3 py-2 text-xs leading-relaxed [@media(max-height:360px)]:mt-2 [@media(max-height:360px)]:py-1 md:text-sm">
            <div className="font-medium text-amber-100">{tr("suggestedAction")}</div>
            <div className="mt-0.5 text-amber-50/70">{failure.suggestion}</div>
          </div>
        )}
      </div>
    </div>
  );

  const controlsToolbar = channel && !failure && !requiresGesture && (
    <div
      role="toolbar"
      className={clsx(
        "player-performance-controls-position player-performance-motion absolute bottom-0 left-[calc(0px_-_env(safe-area-inset-left))] right-[calc(0px_-_env(safe-area-inset-right))] z-10 transition-opacity duration-300",
        showSidebar && "md:left-0",
        controlsVisible
          ? "opacity-100"
          : "opacity-0 pointer-events-none has-focus-visible:opacity-100 has-focus-visible:pointer-events-auto",
      )}
    >
      <PlayerControls
        channel={channel}
        currentProgram={currentProgram}
        isLive={inLiveMode}
        onSeek={performSeek}
        onScrubbingChange={applyScrubbingState}
        locale={locale}
        mediaInfo={mediaInfoBySlot[displayedSlot]}
        renderState={renderStateBySlot[displayedSlot]}
        seekStartTime={streamStartTime}
        liveSessionAnchor={sessionAnchor}
        isPlaying={isPlaybackActive}
        onPlayPause={flipPlayback}
        volume={volumeLevel}
        onVolumeChange={changeVolume}
        canControlVolume={volumeControlAvailable}
        isMuted={mutedState}
        onMuteToggle={flipMute}
        onFullscreen={toggleScreenMode}
        isFullscreen={isFullscreen}
        showSidebar={showSidebar}
        isPiP={inPip}
        isPiPSupported={isPictureInPictureSupported()}
        onPiPToggle={flipPip}
        showMediaBadges={!inDocumentPip}
        activeSourceIndex={activeSourceIndex}
        onSourceChange={onSourceChange}
      />
    </div>
  );

  const gestureFeedbackLayer = channel && !failure && !requiresGesture && (
    <PlayerGestureIndicatorOverlay indicator={gestureIndicator} locale={locale} />
  );

  const playerSurface = (
    <div
      role="application"
      ref={surfaceRef}
      className={clsx(
        // 基类不写 aspect-*：三种布局分支各自给出尺寸语义，避免基类与分支类在
        // Tailwind 输出顺序上互相覆盖（全屏时会铺不满）。
        "player-performance-video-background dark @container-size/video relative flex min-h-0 items-center justify-center bg-[radial-gradient(circle_at_50%_35%,#102044_0%,#070516_58%,#01030a_100%)]",
        // 全屏（含画中画）时舞台铺满可视区：手机端不再是一条 16:9 横条，浏览器工具栏
        // 伸缩引起的高度变化也不会让画面上下跳动。
        inDocumentPip
          ? "h-screen min-h-screen aspect-auto"
          : isFullscreen
            ? "h-full w-full min-h-0 aspect-auto"
            // 手机端舞台高度由自身 16:9 比例推出（宽度确定，容器化后仍能算出高度）；
            // 外层 pages/player.tsx 的 16:9 容器负责把同样高度"报"给页面布局。
            : "aspect-video w-full md:aspect-auto md:h-full",
        !controlsVisible && "cursor-none",
      )}
      onPointerEnter={onPointerOverSurface}
      onPointerMove={onPointerOverSurface}
      onPointerLeave={onPointerExitSurface}
      onClick={onSurfaceActivated}
    >
      {/*
        视频呈现层尺寸恒定（铺满舞台），源画面比例一律交给 object-contain 内部消化：
        源比例变化（整屏广告 / 4:3 老片）不会牵动外层盒子尺寸——若按容器比例在
        w-full / h-full 间翻转，比例临界处会来回切换，正是"抽动/闪屏"的来源。
      */}
      {videoStage}
      {gestureHitLayer}
      {topLeftStatusBadge}
      {channelIdentityCard}
      {resumePlaybackGate}
      {warningBanner}
      {errorPanel}
      {controlsToolbar}
      {gestureFeedbackLayer}
    </div>
  );

  return (
    <div
      className={clsx(
        "player-performance-video-background relative w-full bg-[radial-gradient(circle_at_50%_35%,#102044_0%,#070516_58%,#01030a_100%)] pt-[env(safe-area-inset-top)] pr-[env(safe-area-inset-right)] pl-[env(safe-area-inset-left)] md:h-full",
        // 移动端全屏：本层也要有确定高度，舞台的 h-full 才有参照（否则高度塌成 0）。
        isFullscreen && "h-full",
        showSidebar && "md:pl-0",
      )}
    >
      <div ref={dockHostRef} className="contents">
        {inDocumentPip && (
          <div className="@container-size/video relative flex aspect-video w-full min-h-0 items-center justify-center bg-[radial-gradient(circle_at_center,#102044_0%,#070516_62%,#01030a_100%)] px-4 text-center font-medium text-violet-50/65 text-sm md:aspect-auto md:h-full md:text-base">
            {tr("playingInPictureInPicture")}
          </div>
        )}
      </div>
      {createPortal(playerSurface, portalHostElement)}
    </div>
  );
}

export { VideoPlayerShell as VideoPlayer };
