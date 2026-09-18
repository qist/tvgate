/**
 * 播放页壳层。
 *
 * 职责边界：这里只负责"编排"——把 TVGate 服务端 API（频道目录 / EPG / 回看签发）
 * 的数据装配成 React 状态，再把状态分发给媒体引擎与各子组件。解码、MSE、缓冲
 * 等真正的播放逻辑全部在 ../../media-engine 及子组件内，本文件不碰。
 *
 * 安全模型：流地址一律是服务端签发的受控短地址（/player/<key>），真实源不出服务端；
 * 页面通过 ?my_token= 携带访问令牌，所有请求统一补签。
 */
import "../lib/polyfills"; // 旧 WebView 的 API 缺口必须最先补齐，晚于任何业务代码都会来不及
import { clsx } from "clsx";
import { AlertTriangle, ListChecks, RefreshCw } from "lucide-react";
import { Activity, StrictMode, startTransition, useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { ChannelList, nextScrollBehaviorRef as channelListNextScrollBehaviorRef } from "../components/player/channel-list";
import { ChannelBrowser } from "../components/player/channel-browser";
import { EPGView, nextScrollBehaviorRef as epgViewNextScrollBehaviorRef } from "../components/player/epg-view";
import { PlaybackTimeProvider } from "../components/player/playback-time-context";
import { SettingsDropdown } from "../components/player/settings-dropdown";
import { VideoPlayer } from "../components/player/video-player";
import { Button, buttonVariants } from "../components/ui/button";
import { Card } from "../components/ui/card";
import { usePersistedEnum } from "../hooks/use-persisted-enum";
import { usePlayerAppearance } from "../hooks/use-player-appearance";
import { usePlayerPanelAlpha } from "../hooks/use-player-panel-alpha";
import { usePlayerTranslation } from "../hooks/use-player-translation";
import { useTheme } from "../hooks/use-player-theme";
import { isDocumentPictureInPictureSupported } from "../lib/document-picture-in-picture";
import {
  type EPGData,
  fillEPGGaps,
  getCurrentProgram,
  getEPGChannelId,
  mapPrograms,
  type RawEPGProgram,
} from "../lib/epg-parser";
import type { Locale } from "../lib/locale";
import type { Channel, M3UMetadata, Source } from "../types/player";
import { isLGWebOS } from "../lib/platform";
import { findDeepLinkChannel, syncChannelDeepLink } from "../lib/player-deep-link";
import {
  getAudioChannelMode,
  getAutoDeinterlace,
  getLastChannelId,
  getLastSourceIndex,
  getPictureEnhancement,
  getSeamlessSwitch,
  getSidebarVisible,
  saveAudioChannelMode,
  saveAutoDeinterlace,
  saveLastChannelId,
  saveLastSourceIndex,
  savePictureEnhancement,
  saveSeamlessSwitch,
} from "../lib/player-storage";
import { getPlaybackBackendKind, type PlayerSegment } from "../media-engine";
import { mseToWallClock, NEAR_LIVE_EDGE_MS } from "../media-engine/timeline";
import { PICTURE_IN_PICTURE_MODES, type PictureInPictureMode } from "../types/ui";

// ---------------------------------------------------------------------------
// 行为常量：这些数值是被 UI 时序 / 后端能力隐式约定的，调整前先确认对端行为。
// ---------------------------------------------------------------------------

/** EPG 重试冷却：拉取失败或结果为空后，冷却到期才允许再试（一次失败不放弃整场）。 */
const EPG_FETCH_COOLDOWN_MS = 60_000;
/** EPG 周期刷新：与后端 EPG 源刷新节奏同量级，长时间挂机时"正在播出"不漂移、跨天能跟上。 */
const EPG_PERIODIC_REFRESH_MS = 15 * 60 * 1000;
/**
 * 可见频道 EPG 预取预算（条数）。
 * xml 源是服务端查表，代价低可以放宽；template 源每个频道都要服务端去外网代拉，必须保守。
 */
const EPG_PREFETCH_BUDGET_XML = 40;
const EPG_PREFETCH_BUDGET_TEMPLATE = 12;
/** 预取错峰间隔：把一批请求摊开，避免可见区一次性打出请求风暴。 */
const EPG_PREFETCH_GAP_MS = 120;
/** 窄屏断点：低于该宽度时侧栏切换为整屏 Tab 形态。 */
const COMPACT_LAYOUT_MAX_WIDTH_PX = 768;
/** 视口变化防抖：手机浏览器工具栏收展会连发 resize，合并后再判定。 */
const VIEWPORT_DEBOUNCE_MS = 120;
/** 首屏揭示动画的停留时长：频道就绪后再让加载遮罩按动画节奏退场。 */
const REVEAL_HOLD_MS = 500;
/** 这些传输协议的源由服务端代收回看；udp/rtp 等组播源没有服务端回看能力。 */
const CATCHUP_SERVER_SCHEMES: readonly string[] = ["http", "https", "php", "rtsp"];
/** 从播放地址解析不出可读频道名时的兜底展示名。 */
const FALLBACK_LIVE_CHANNEL_NAME = "直播";
/** 自动切线路提示的驻留时长：盖住换流的加载窗口即可，不挡后续画面。 */
const FAILOVER_HINT_MS = 3_500;

// ---------------------------------------------------------------------------
// 服务端数据模型：/api/player/channels 的原始载荷。
// ---------------------------------------------------------------------------

interface ServerChannelRecord {
  key: string;
  name: string;
  group?: string;
  scheme?: string; // udp / rtp / rtsp / http / https
  tvgId?: string;
  tvgName?: string;
  tvgLogo?: string;
  epgType?: string;
  /** 组内聚合的线路表（后端归一化同名频道产出）；缺失时按单线路（key）兜底。 */
  lines?: { key: string; tag?: string; scheme?: string }[];
}

interface ServerChannelsEnvelope {
  list?: ServerChannelRecord[];
  epgSource?: { kind?: string; template?: string; logo?: string };
}

// ---------------------------------------------------------------------------
// 访问令牌：页面 URL 携带 ?my_token=，模块加载时读取一次并补签到所有出站请求上。
// ---------------------------------------------------------------------------

function readAccessToken(): string {
  try {
    return new URLSearchParams(window.location.search).get("my_token") ?? "";
  } catch {
    return "";
  }
}

const ACCESS_TOKEN = readAccessToken();

function withAccessToken(url: string): string {
  if (!ACCESS_TOKEN) return url;
  return url + (url.includes("?") ? "&" : "?") + "my_token=" + encodeURIComponent(ACCESS_TOKEN);
}

// ---------------------------------------------------------------------------
// 时间与格式化工具。
// ---------------------------------------------------------------------------

function padTwo(value: number): string {
  return (value < 10 ? "0" : "") + value;
}

/** 当天日期，格式 YYYYMMDD（EPG 查询参数用）。 */
function formatTodayAsStamp(): string {
  const now = new Date();
  return `${now.getFullYear()}${padTwo(now.getMonth() + 1)}${padTwo(now.getDate())}`;
}

// ---------------------------------------------------------------------------
// 独立直播入口：`?live=<URL 编码后的播放地址>`（推流发布页本地 FLV/HLS 用它跳转）。
// 只接受同源地址（发布流的 /<path>/play/<name>.flv|m3u8），跨源一律拒绝，防止开放代理。
// ---------------------------------------------------------------------------

function readDirectLiveUrl(): string | null {
  try {
    const rawParam = new URLSearchParams(window.location.search).get("live");
    if (!rawParam) return null;
    const resolved = new URL(rawParam, window.location.origin);
    if (resolved.origin !== window.location.origin) return null;
    return resolved.href;
  } catch {
    return null;
  }
}

/** 从播放地址反推频道名（/live/play/cctv1.flv → cctv1），仅用于独立直播入口。 */
function channelLabelFromStreamUrl(url: string): string {
  try {
    const lastSegment = new URL(url).pathname.split("/").filter(Boolean).pop() || "";
    return lastSegment.replace(/\.(?:flv|m3u8)$/i, "") || FALLBACK_LIVE_CHANNEL_NAME;
  } catch {
    return FALLBACK_LIVE_CHANNEL_NAME;
  }
}

// ---------------------------------------------------------------------------
// 频道目录装配：服务端短地址 → 播放器 Channel 模型。
// ---------------------------------------------------------------------------

/**
 * 常规入口：/api/player/channels 载荷 → 频道列表 + 分组集合。
 * 每条记录按线路表装配受控短地址源（/player/<线路key>）；可代收回看的协议逐线路
 * 标记 server 时移。频道 id 用首线路 key（后端的稳定身份 key，EPG/回看/深链按它查询）。
 */
function buildCatalogFromServerPayload(records: ServerChannelRecord[]): { channels: Channel[]; groups: string[] } {
  const channels: Channel[] = [];
  const groupNames = new Set<string>();
  for (const record of records) {
    if (!record?.key || !record.name) continue;
    const lines = record.lines?.length ? record.lines : [{ key: record.key }];
    const sources = lines.map((line) => {
      const source: Source = { url: withAccessToken(`/player/${line.key}`), alias: line.tag || undefined };
      if (CATCHUP_SERVER_SCHEMES.includes(line.scheme ?? record.scheme ?? "")) {
        // timeshift 与 timeshiftTemplate 必须成对赋值：回看能力探测以"二者同时存在"为准
        source.timeshift = "server";
        source.timeshiftTemplate = "server";
      }
      return source;
    });
    if (record.group) groupNames.add(record.group);
    channels.push({
      id: record.key,
      name: record.name,
      logo: record.tvgLogo || undefined,
      groups: record.group ? [record.group] : [],
      // 频道号按订阅序位从 1 编起，给用户一个可输入的台号，替代裸短哈希 id
      number: channels.length + 1,
      epgId: record.tvgId || undefined,
      epgName: record.tvgName || undefined,
      sources,
    });
  }
  return { channels, groups: [...groupNames] };
}

/** 独立直播入口没有服务端目录，单独装配成单频道"目录"。 */
function buildDirectLiveCatalog(url: string): { channels: Channel[]; groups: string[] } {
  const label = channelLabelFromStreamUrl(url);
  const directChannel: Channel = {
    id: `direct-${label}`,
    name: label,
    groups: [],
    number: 1,
    sources: [{ url: withAccessToken(url) }],
  };
  return { channels: [directChannel], groups: [] };
}

/**
 * EPG 数据键：与 lib/epg-parser 的 getEPGChannelId 回退顺序严格一致（epgId → epgName → name），
 * 两边一旦不一致，节目单就会静默匹配不到频道。
 */
function epgKeyOfChannel(channel: Channel): string {
  return channel.epgId || channel.epgName || channel.name;
}

/** 失败页排查清单：圆点 + 文案逐条渲染；条目 key 直接复用文案串（清单是静态常量，无重排）。 */
function FailureChecklist({ hints }: { hints: string[] }) {
  return (
    <ul className="mt-3 space-y-2 text-sm leading-5 text-muted-foreground">
      {hints.map((hint) => (
        <li key={hint} className="flex min-w-0 gap-2">
          <span
            aria-hidden="true"
            className="mt-2 h-1.5 w-1.5 shrink-0 rounded-full bg-violet-500 shadow-[0_0_8px_rgba(var(--pg-rgb),0.45)]"
          />
          <span className="min-w-0 break-words">{hint}</span>
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------------
// 屏幕方向：全屏时尽量锁横屏（手机/平板上的 TV 观感）。
// ---------------------------------------------------------------------------

type OrientationWithLock = ScreenOrientation & { lock?: (orientation: "landscape") => Promise<void> };

async function tryLockLandscapeOrientation(): Promise<boolean> {
  const orientation = screen.orientation as OrientationWithLock | undefined;
  if (!orientation?.lock) return false;
  try {
    await orientation.lock("landscape");
    return true;
  } catch {
    return false;
  }
}

function releaseOrientationLock(): void {
  try {
    screen.orientation?.unlock();
  } catch {
    // 退出全屏的瞬间锁可能已被系统释放，这里的异常属于正常竞争，忽略。
  }
}

/**
 * 侧栏安全区内边距方向：横屏且逆时针旋转（angle≠90）时，刘海/听筒在右侧，
 * 侧栏内容需要用 safe-area-inset-right 垫开。
 */
function computePanelInsetSide(): boolean {
  const { angle, type } = screen.orientation;
  if (!type.startsWith("landscape")) return true;
  return angle !== 90;
}

/** 环形步进：在 [0, length) 内前后移动一格，首尾相接（频道上下台、上/下一条共用）。 */
function shiftedIndex(index: number, length: number, step: -1 | 1): number {
  return (index + step + length) % length;
}

// ---------------------------------------------------------------------------
// Tailwind 类常量：超长渐变/毛玻璃串集中存放，保持最终输出的类字符串逐字不变。
// ---------------------------------------------------------------------------

const PAGE_SHELL_CLASSES =
  "player-performance-page-background player-performance-scope player-viewport-height relative flex flex-col bg-[radial-gradient(circle_at_92%_8%,rgba(var(--pg-rgb),0.15),transparent_28%),radial-gradient(circle_at_72%_92%,rgba(var(--pg-rgb-2),0.13),transparent_32%),linear-gradient(145deg,var(--pg-bg-a),var(--pg-bg-b))] dark:bg-[radial-gradient(circle_at_88%_10%,rgba(var(--pg-rgb),0.1),transparent_30%),radial-gradient(circle_at_70%_88%,rgba(var(--pg-rgb-2),0.12),transparent_34%),linear-gradient(145deg,var(--pg-bg-dark-a),var(--pg-bg-dark-b))]";

const DOCK_COLUMN_CLASSES = "relative flex min-h-0 flex-1 flex-col overflow-hidden";

const VIDEO_STAGE_CLASSES = "w-full sticky md:absolute md:inset-0";

const DOCK_PANEL_CLASSES =
  "player-performance-dock flex w-full flex-1 flex-col overflow-hidden border-violet-950/10 border-t bg-white/80 pl-[env(safe-area-inset-left)] backdrop-blur-2xl backdrop-saturate-150 dark:border-violet-100/10 dark:bg-slate-950/80 dark:backdrop-brightness-[0.6] md:absolute md:inset-y-0 md:left-0 md:z-20 md:h-full md:flex-none md:border-t-0 md:border-r md:pt-[env(safe-area-inset-top)] md:pr-0";

const MOBILE_TAB_BAR_CLASSES =
  "player-performance-panel-background flex shrink-0 items-center border-violet-950/10 border-b bg-white/44 shadow-[0_8px_24px_rgba(var(--pg-rgb),0.045)] backdrop-blur-xl dark:border-violet-100/10 dark:bg-[linear-gradient(90deg,var(--pg-bg-dark-a),var(--pg-bg-dark-b))]";

const MOBILE_TAB_BASE_CLASSES =
  "player-performance-motion min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap border-b-2 px-3 py-2 text-center font-semibold text-xs leading-5 tracking-[0.01em] transition-[color,background-color,border-color,box-shadow] md:px-4 md:py-3 md:text-sm";

const MOBILE_TAB_ACTIVE_CLASSES =
  "border-violet-500 bg-[linear-gradient(to_top,rgba(var(--pg-rgb),0.12),transparent)] text-violet-700 shadow-[inset_0_-1px_0_rgba(var(--pg-rgb),0.18)] dark:border-violet-300 dark:text-violet-200";

const MOBILE_TAB_IDLE_CLASSES =
  "cursor-pointer border-transparent text-slate-500 hover:bg-violet-400/5 hover:text-violet-700 dark:text-slate-400 dark:hover:text-violet-100";

const BOOT_VEIL_CLASSES =
  "player-performance-page-background player-performance-motion absolute inset-0 z-50 flex items-center justify-center bg-[radial-gradient(circle_at_center,rgba(var(--pg-rgb),0.16),transparent_28%),radial-gradient(circle_at_65%_60%,rgba(var(--pg-rgb-2),0.14),transparent_35%),linear-gradient(145deg,var(--pg-bg-a),var(--pg-bg-b))] pt-[max(1rem,env(safe-area-inset-top))] pr-[max(1rem,env(safe-area-inset-right))] pb-[max(1rem,env(safe-area-inset-bottom))] pl-[max(1rem,env(safe-area-inset-left))] dark:bg-[radial-gradient(circle_at_center,rgba(var(--pg-rgb),0.11),transparent_30%),radial-gradient(circle_at_65%_60%,rgba(var(--pg-rgb-2),0.12),transparent_38%),linear-gradient(145deg,var(--pg-bg-dark-a),var(--pg-bg-dark-b))]";

const BOOT_SPINNER_CLASSES =
  "player-performance-loading-spinner mx-auto h-12 w-12 animate-spin rounded-full border-4 border-violet-950/10 border-t-violet-500 border-r-(--pg-grad-b) shadow-[0_0_28px_rgba(var(--pg-rgb),0.22)] dark:border-violet-100/10 dark:border-t-violet-300 dark:border-r-(--pg-grad-b)";

const FAILURE_PAGE_SHELL_CLASSES =
  "player-performance-page-background player-performance-scope player-viewport-height overflow-y-auto bg-[radial-gradient(circle_at_18%_14%,rgba(var(--pg-rgb),0.16),transparent_28%),radial-gradient(circle_at_84%_82%,rgba(var(--pg-rgb-2),0.16),transparent_32%),linear-gradient(145deg,var(--pg-bg-a),var(--pg-bg-b))] dark:bg-[radial-gradient(circle_at_18%_14%,rgba(var(--pg-rgb),0.1),transparent_30%),radial-gradient(circle_at_84%_82%,rgba(var(--pg-rgb-2),0.13),transparent_34%),linear-gradient(145deg,var(--pg-bg-dark-a),var(--pg-bg-dark-b))]";

const FAILURE_PAGE_INNER_CLASSES = "mx-auto flex min-h-full w-[calc(100%-2rem)] max-w-5xl items-center py-8 sm:w-[calc(100%-3rem)]";

const FAILURE_CARD_CLASSES =
  "player-performance-panel-background min-w-0 w-full overflow-hidden rounded-3xl border-violet-900/10 bg-white/72 shadow-[0_28px_80px_rgba(var(--pg-rgb),0.16),inset_0_1px_0_rgba(255,255,255,0.85)] backdrop-blur-2xl dark:border-violet-100/12 dark:bg-[linear-gradient(145deg,rgba(2,6,23,0.9),rgba(15,23,42,0.82))] dark:shadow-[0_30px_90px_rgba(2,6,16,0.62),inset_0_1px_0_rgba(255,255,255,0.08)]";

/** 可见频道的节目单是否值得（重新）预取：从未成功、不在飞、且已过失败冷却。 */
function shouldPrefetchSchedule(
  channel: Channel,
  readyKeys: Set<string>,
  busyKeys: Set<string>,
  lastAttempts: Map<string, number>,
): boolean {
  const key = epgKeyOfChannel(channel);
  return (
    !!key &&
    !readyKeys.has(key) &&
    !busyKeys.has(key) &&
    Date.now() - (lastAttempts.get(key) ?? 0) >= EPG_FETCH_COOLDOWN_MS
  );
}

// ---------------------------------------------------------------------------
// 页面组件：状态编排 + 布局。
// ---------------------------------------------------------------------------

function PlayerScreen() {
  // 引擎与平台能力在页面生命周期内不变，直接模块级求值即可。
  const engineBackend = getPlaybackBackendKind();
  const supportsVideoProcessing = engineBackend === "mse";
  const canSwitchSeamlessly = !isLGWebOS();
  const supportsDocPiP = isDocumentPictureInPictureSupported();

  const { theme, setTheme } = useTheme("tvgate-player-theme");
  const { appearance, setAppearance } = usePlayerAppearance();
  const { panelAlpha, setPanelAlpha } = usePlayerPanelAlpha();
  const [pictureInPictureMode, setPictureInPictureMode] = usePersistedEnum<PictureInPictureMode>(
    "tvgate-player-picture-in-picture-mode",
    "document",
    PICTURE_IN_PICTURE_MODES,
  );
  const pageLocale: Locale = "zh-Hans";
  const t = usePlayerTranslation(pageLocale);

  // ---- 频道目录与 EPG 簿记 -------------------------------------------------
  const [catalog, setCatalog] = useState<M3UMetadata | null>(null);
  const [epgSchedules, setEpgSchedules] = useState<EPGData>({});
  /** 只有真正拿到节目单（成功）才记入；失败/空结果走冷却重试，绝不写这个集合。 */
  const epgReadyKeysRef = useRef<Set<string>>(new Set());
  /** 在飞的 EPG 请求键，去重用。 */
  const epgBusyKeysRef = useRef<Set<string>>(new Set());
  /** 每个频道最近一次尝试时间戳，失败/空结果后的冷却依据。 */
  const epgLastAttemptRef = useRef<Map<string, number>>(new Map());
  /** 后端下发的 EPG 源配置（kind/template/logo）：预取限流与"未配置"提示都看它。 */
  const [epgBackendConfig, setEpgBackendConfig] = useState<{ kind?: string; template?: string; logo?: string } | null>(null);

  // ---- 播放目标与流形态 ----------------------------------------------------
  const [playingChannel, setPlayingChannel] = useState<Channel | null>(null);
  /** 浏览器浮层里正在预览（悬停/聚焦）的频道，决定节目单栏显示谁的内容。 */
  const [browsingChannel, setBrowsingChannel] = useState<Channel | null>(null);
  const [streamMode, setStreamMode] = useState<"live" | "catchup">("live");
  const [engineSegments, setEngineSegments] = useState<PlayerSegment[]>([]);
  const [pageError, setPageError] = useState<string | null>(null);
  const [isBooting, setIsBooting] = useState(true);
  const [isVeilLeaving, setIsVeilLeaving] = useState(false);
  const [isPanelShown, setIsPanelShown] = useState(() => getSidebarVisible());
  /** 浮层"节目单"栏是否展开：决定浮层是两列窄条还是三列宽板。 */
  const [isDockEpgExpanded, setIsDockEpgExpanded] = useState(false);
  /** Tab 高亮立即响应；实际挂载的视图走 startTransition 延后，让切换不卡输入。 */
  const [focusedPanelView, setFocusedPanelView] = useState<"channels" | "epg">("channels");
  const [mountedPanelView, setMountedPanelView] = useState<"channels" | "epg">("channels");
  const [isFullscreenActive, setIsFullscreenActive] = useState(false);
  const [isCompactLayout, setIsCompactLayout] = useState(() => window.innerWidth < COMPACT_LAYOUT_MAX_WIDTH_PX);
  const [insetPanelRight, setInsetPanelRight] = useState(computePanelInsetSide);
  /** 上述两个视口状态的即时镜像：resize 回调里先比对，值真变了才付钱重渲染。 */
  const isCompactLayoutRef = useRef(isCompactLayout);
  const insetPanelRightRef = useRef(insetPanelRight);
  const [seamlessEnabled, setSeamlessEnabled] = useState(() => (canSwitchSeamlessly ? getSeamlessSwitch() : false));
  const [deinterlaceEnabled, setDeinterlaceEnabled] = useState(() =>
    supportsVideoProcessing ? getAutoDeinterlace() : false,
  );
  const [enhancementEnabled, setEnhancementEnabled] = useState(() =>
    supportsVideoProcessing ? getPictureEnhancement() : false,
  );
  /** 软解音频声道偏好（本地记忆）：mono = 左右混叠成单声道。 */
  const [audioTrackMode, setAudioTrackModeState] = useState<"stereo" | "mono">(() => getAudioChannelMode());
  const pageStageRef = useRef<HTMLDivElement>(null);
  const pseudoFullscreenRef = useRef(false);

  // ---- 回看/直播的时间锚 ---------------------------------------------------
  /** 当前流起点对应的墙钟时间：live 模式 = 开始播放时刻；回看 = 节目起点。 */
  const [streamWallClockBase, setStreamWallClockBase] = useState<Date>(() => new Date());
  const [pinnedToLiveEdge, setPinnedToLiveEdge] = useState(true);
  const [catchupStreamUrl, setCatchupStreamUrl] = useState<string | null>(null);
  /** 回看请求代次：每次新的播放意图自增，旧请求回来发现代次不符就丢弃，防止"旧答案覆盖新流"。 */
  const catchupEpochRef = useRef(0);

  // ---- 播放时钟 ------------------------------------------------------------
  const [playbackClock, setPlaybackClock] = useState(0);
  const deferredPlaybackClock = useDeferredValue(playbackClock);
  const livePlaybackClockRef = useRef(0);
  const lastTickedSecondRef = useRef(0);

  // ---- 多源切换 ------------------------------------------------------------
  const [sourceTrackIndex, setSourceTrackIndex] = useState(0);
  const currentSourceTrack = playingChannel?.sources[sourceTrackIndex] ?? playingChannel?.sources[0];

  // ---- 全屏状态同步 --------------------------------------------------------
  useEffect(() => {
    const handleFullscreenChange = () => {
      const isDocumentFullscreen = !!document.fullscreenElement;
      // 模拟全屏（方向锁代偿）期间，浏览器偶发的 fullscreenchange 不代表真的退出了全屏
      if (!isDocumentFullscreen && pseudoFullscreenRef.current) return;
      setIsFullscreenActive(isDocumentFullscreen);
      if (!isDocumentFullscreen) {
        releaseOrientationLock();
        setIsPanelShown(true);
      }
    };
    document.addEventListener("fullscreenchange", handleFullscreenChange);
    return () => document.removeEventListener("fullscreenchange", handleFullscreenChange);
  }, []);

  // ---- 视口变化（防抖 + 值比对）--------------------------------------------
  useEffect(() => {
    // 手机浏览器工具栏收起/展开（进出全屏常见）会连发 resize；
    // 值没变就绝不 setState，否则整页反复重排，表现为画面抽动/闪屏。
    // 高度变化不影响内宽，绝大多数 resize 在这里被直接丢弃，只有真跨断点才更新。
    let debounceHandle = 0;
    const commitViewport = () => {
      debounceHandle = 0;
      const nextCompact = window.innerWidth < COMPACT_LAYOUT_MAX_WIDTH_PX;
      const nextInset = computePanelInsetSide();
      if (nextCompact === isCompactLayoutRef.current && nextInset === insetPanelRightRef.current) return;
      isCompactLayoutRef.current = nextCompact;
      insetPanelRightRef.current = nextInset;
      startTransition(() => {
        setIsCompactLayout(nextCompact);
        setInsetPanelRight(nextInset);
      });
    };
    const scheduleViewportCommit = () => {
      if (debounceHandle) window.clearTimeout(debounceHandle);
      debounceHandle = window.setTimeout(commitViewport, VIEWPORT_DEBOUNCE_MS);
    };
    window.addEventListener("resize", scheduleViewportCommit);
    screen.orientation.addEventListener("change", scheduleViewportCommit);
    return () => {
      if (debounceHandle) window.clearTimeout(debounceHandle);
      window.removeEventListener("resize", scheduleViewportCommit);
      screen.orientation.removeEventListener("change", scheduleViewportCommit);
    };
  }, []);

  // ---- 流形态引擎片段：直播 / 回看互斥切换 ----------------------------------
  useEffect(() => {
    if (!currentSourceTrack || !pinnedToLiveEdge) return;
    setStreamMode("live");
    setEngineSegments((previous) => {
      const liveOnly: PlayerSegment[] = [{ url: currentSourceTrack.url, duration: 0 }];
      if (previous.length === 1 && previous[0].url === liveOnly[0].url) return previous;
      return liveOnly;
    });
  }, [playingChannel, currentSourceTrack, sourceTrackIndex, pinnedToLiveEdge]);

  useEffect(() => {
    if (pinnedToLiveEdge || !catchupStreamUrl) return;
    setStreamMode("catchup");
    setEngineSegments((previous) => {
      if (previous.length === 1 && previous[0].url === catchupStreamUrl) return previous;
      return [{ url: catchupStreamUrl, duration: 0 }];
    });
  }, [catchupStreamUrl, pinnedToLiveEdge]);

  const resetPlaybackClock = useCallback(() => {
    livePlaybackClockRef.current = 0;
    lastTickedSecondRef.current = 0;
    setPlaybackClock(0);
  }, []);

  // ---- 选片（节目单/进度条 seek）--------------------------------------------
  const performSeek = useCallback(
    (seekTime: Date, goingLive: boolean, targetChannel?: Channel) => {
      const destination = targetChannel ?? playingChannel;
      resetPlaybackClock();
      if (goingLive) {
        catchupEpochRef.current += 1;
        setCatchupStreamUrl(null);
        setStreamWallClockBase(new Date());
        setPinnedToLiveEdge(true);
        setStreamMode("live");
        return;
      }
      if (!destination) return;

      const epoch = ++catchupEpochRef.current;
      // 回看时间参数用 Unix 秒（无时区歧义），playseek 的 YmdHis 串由服务端换算
      const fromSec = Math.floor(seekTime.getTime() / 1000);
      const toSec = Math.floor(Date.now() / 1000);
      setStreamWallClockBase(seekTime);
      setPinnedToLiveEdge(false);
      setStreamMode("catchup");
      fetch(
        withAccessToken(
          `/api/player/catchup?key=${encodeURIComponent(destination.id)}&from=${fromSec}&to=${toSec}`,
        ),
      )
        .then((res) => {
          if (!res.ok) throw new Error(`catchup HTTP ${res.status}`);
          return res.json() as Promise<{ play?: string }>;
        })
        .then((payload) => {
          if (epoch !== catchupEpochRef.current) return;
          if (payload?.play) setCatchupStreamUrl(withAccessToken(payload.play));
        })
        .catch(() => {
          // 回看签发失败：直接落回直播，别让用户停在黑屏上
          if (epoch !== catchupEpochRef.current) return;
          setCatchupStreamUrl(null);
          setStreamWallClockBase(new Date());
          setPinnedToLiveEdge(true);
          setStreamMode("live");
        });
    },
    [playingChannel, resetPlaybackClock],
  );

  /** 手机端节目单（整屏 Tab）里选节目：选完回到频道 Tab，等于自动收起节目单。 */
  const pickProgramForPlayback = useCallback(
    (programStart: Date, programEnd: Date) => {
      const goingLive = programEnd.getTime() >= Date.now() - NEAR_LIVE_EDGE_MS;
      performSeek(programStart, goingLive);
      // 桌面/大屏：选完节目自动收起侧栏（交互设计：点屏幕唤出 → 选好即收）
      if (!isCompactLayout) setIsPanelShown(false);
      setFocusedPanelView("channels");
      startTransition(() => setMountedPanelView("channels"));
    },
    [performSeek, isCompactLayout],
  );

  /** 播放中切清晰度/线路：live 直接重开；回看要把已播进度换算回墙钟，切完续播。 */
  const switchSourceTrack = useCallback(
    (nextIndex: number) => {
      if (streamMode === "live") {
        catchupEpochRef.current += 1;
        setCatchupStreamUrl(null);
        setPinnedToLiveEdge(true);
        setStreamWallClockBase(new Date());
      } else {
        setStreamWallClockBase(mseToWallClock(livePlaybackClockRef.current, streamWallClockBase));
      }
      resetPlaybackClock();
      setSourceTrackIndex(nextIndex);
    },
    [streamMode, resetPlaybackClock, streamWallClockBase],
  );

  /** 起播成功即记忆该频道的源选择，下次进台直接用同一源。 */
  const rememberSourceOnStart = useCallback(() => {
    if (playingChannel) saveLastSourceIndex(playingChannel.id, sourceTrackIndex);
  }, [playingChannel, sourceTrackIndex]);

  /** 进台：清时钟、作废旧回看代次、回直播边缘，再按上次记忆挑源。 */
  const tuneToChannel = useCallback(
    (channel: Channel) => {
      resetPlaybackClock();
      catchupEpochRef.current += 1;
      setCatchupStreamUrl(null);
      setPlayingChannel(channel);
      const rememberedIndex = getLastSourceIndex(channel.id);
      setSourceTrackIndex(rememberedIndex < channel.sources.length ? rememberedIndex : 0);
      setPinnedToLiveEdge(true);
      setStreamWallClockBase(new Date());
    },
    [resetPlaybackClock],
  );

  /** 桌面浮层节目单里选节目：必要时先切到目标频道再 seek。 */
  const pickProgramFromBrowser = useCallback(
    (programStart: Date, programEnd: Date) => {
      const target = browsingChannel ?? playingChannel;
      if (!target) return;
      if (target.id !== playingChannel?.id) tuneToChannel(target);
      const goingLive = programEnd.getTime() >= Date.now() - NEAR_LIVE_EDGE_MS;
      performSeek(programStart, goingLive, target);
    },
    [browsingChannel, playingChannel, tuneToChannel, performSeek],
  );

  // ---- 观看进度的持久化与深链同步 ------------------------------------------
  useEffect(() => {
    if (playingChannel && streamMode === "live") saveLastChannelId(playingChannel.id);
  }, [playingChannel, streamMode]);

  useEffect(() => {
    if (playingChannel && catalog) syncChannelDeepLink(playingChannel, catalog.channels);
  }, [playingChannel, catalog]);

  useEffect(() => {
    if (!catalog) return;
    const onHashChange = () => {
      const linked = findDeepLinkChannel(catalog.channels);
      if (linked && linked.id !== playingChannel?.id) tuneToChannel(linked);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [catalog, playingChannel, tuneToChannel]);

  // ---- EPG 拉取 ------------------------------------------------------------
  /**
   * 拉单个频道的节目单（服务端已解析 XMLTV / 按模板代拉，前端从不直连 EPG 源）。
   * 判定标准：只有拿到真实节目才算"已加载"；失败/空结果只记尝试时间，冷却后允许重试。
   */
  const fetchChannelSchedule = useCallback((channel: Channel, opts?: { force?: boolean }) => {
    const epgKey = epgKeyOfChannel(channel);
    const isBusy = epgBusyKeysRef.current.has(epgKey);
    const isReady = epgReadyKeysRef.current.has(epgKey);
    const isCoolingDown = Date.now() - (epgLastAttemptRef.current.get(epgKey) ?? 0) < EPG_FETCH_COOLDOWN_MS;
    if (!epgKey || isBusy) return;
    if (!opts?.force && (isReady || isCoolingDown)) return;
    // 尝试时间必须在发起请求前落账：并发触发（当前频道 + 预取）时靠它去重冷却
    epgLastAttemptRef.current.set(epgKey, Date.now());
    epgBusyKeysRef.current.add(epgKey);

    const query =
      `date=${formatTodayAsStamp()}` +
      `&key=${encodeURIComponent(channel.id)}`;
    fetch(withAccessToken(`/api/player/epg?${query}`))
      .then((res) => (res.ok ? (res.json() as Promise<{ programs?: RawEPGProgram[] }>) : Promise.reject(new Error("epgFailed"))))
      .then((payload) => {
        const programs = mapPrograms(payload.programs);
        if (!programs.length) throw new Error("epgEmpty");
        epgReadyKeysRef.current.add(epgKey);
        startTransition(() => {
          setEpgSchedules((previous) => ({ ...previous, [epgKey]: programs }));
        });
      })
      .catch(() => {
        // 失败/为空：不写"已加载"，冷却后可重试；界面上继续显示缝隙填充占位
      })
      .finally(() => {
        epgBusyKeysRef.current.delete(epgKey);
      });
  }, []);

  // 浏览/播放目标一旦存在就不再回退为 null，预览态保持稳定
  useEffect(() => {
    if (playingChannel) setBrowsingChannel((previous) => previous ?? playingChannel);
  }, [playingChannel]);

  useEffect(() => {
    if (!playingChannel) return;
    fetchChannelSchedule(playingChannel);
  }, [playingChannel, fetchChannelSchedule]);

  useEffect(() => {
    if (!browsingChannel) return;
    fetchChannelSchedule(browsingChannel);
  }, [browsingChannel, fetchChannelSchedule]);

  /**
   * 可见频道批量预取：让节目单栏与列表行显示真实节目，而不是满屏"精彩节目"占位。
   * 预算按后端 EPG 源类型收紧（template 源每次都要服务端外网代拉），并逐条错峰发起。
   */
  const prefetchSchedulesForVisible = useCallback(
    (visible: Channel[]) => {
      const budget = epgBackendConfig?.kind === "template" ? EPG_PREFETCH_BUDGET_TEMPLATE : EPG_PREFETCH_BUDGET_XML;
      visible
        .slice(0, budget)
        .filter((channel) =>
          shouldPrefetchSchedule(channel, epgReadyKeysRef.current, epgBusyKeysRef.current, epgLastAttemptRef.current),
        )
        .forEach((channel, order) => {
          window.setTimeout(() => fetchChannelSchedule(channel), order * EPG_PREFETCH_GAP_MS);
        });
    },
    [epgBackendConfig?.kind, fetchChannelSchedule],
  );

  /**
   * 周期刷新：清空"已加载"与冷却账本后强制重取当前/预览频道，
   * 让跨天与节目推进都能在长播放会话里跟上。
   */
  useEffect(() => {
    const timer = window.setInterval(() => {
      epgReadyKeysRef.current = new Set();
      epgLastAttemptRef.current = new Map();
      if (playingChannel) fetchChannelSchedule(playingChannel, { force: true });
      if (browsingChannel) fetchChannelSchedule(browsingChannel, { force: true });
    }, EPG_PERIODIC_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [playingChannel, browsingChannel, fetchChannelSchedule]);

  /** 后端是否配了 EPG 源：没配时节目单栏直接给提示，别让用户误以为前端坏了。 */
  const hasEpgBackend = epgBackendConfig?.kind === "xml" || epgBackendConfig?.kind === "template";

  // ---- 播放时钟 / 外观 / 面板交互 ------------------------------------------
  const trackPlaybackClock = useCallback((time: number) => {
    livePlaybackClockRef.current = time;
    const wholeSecond = Math.floor(time);
    if (wholeSecond === lastTickedSecondRef.current) return;
    lastTickedSecondRef.current = wholeSecond;
    setPlaybackClock(time);
  }, []);

  const applyThemeChoice = useCallback(
    (nextTheme: Parameters<typeof setTheme>[0]) => startTransition(() => setTheme(nextTheme)),
    [setTheme],
  );

  const applyAppearanceChoice = useCallback(
    (nextAppearance: Parameters<typeof setAppearance>[0]) => startTransition(() => setAppearance(nextAppearance)),
    [setAppearance],
  );

  const switchPanelView = useCallback((view: "channels" | "epg") => {
    // 切换瞬间把目标列表的滚动行为钉成 instant，避免新视图带着旧滚动位置做平滑动画
    (view === "channels" ? channelListNextScrollBehaviorRef : epgViewNextScrollBehaviorRef).current = "instant";
    setFocusedPanelView(view);
    startTransition(() => setMountedPanelView(view));
  }, []);

  /** 台号跳转 / 上下台：都基于订阅序位，首尾环形相接。 */
  const stepChannel = useCallback(
    (target: "prev" | "next" | number) => {
      const playlist = catalog?.channels;
      if (!playlist?.length) return;
      if (typeof target === "number") {
        const byNumber = playlist[target - 1];
        if (byNumber) tuneToChannel(byNumber);
        return;
      }
      if (!playingChannel) return;
      const currentIndex = playlist.indexOf(playingChannel);
      const step = target === "prev" ? -1 : 1;
      tuneToChannel(playlist[shiftedIndex(currentIndex, playlist.length, step)]);
    },
    [catalog, playingChannel, tuneToChannel],
  );

  const [previousInOrder, nextInOrder] = useMemo<[Channel | null, Channel | null]>(() => {
    const playlist = catalog?.channels;
    if (!playlist?.length || !playingChannel) return [null, null];
    const currentIndex = playlist.indexOf(playingChannel);
    if (currentIndex < 0) return [null, null];
    return [
      playlist[shiftedIndex(currentIndex, playlist.length, -1)],
      playlist[shiftedIndex(currentIndex, playlist.length, 1)],
    ];
  }, [catalog, playingChannel]);

  // 独立直播入口只看初始 URL，挂载后不再变
  const directLiveEntry = useMemo(() => readDirectLiveUrl(), []);

  // ---- 首屏目录装载 --------------------------------------------------------
  const bootstrapCatalog = useCallback(async () => {
    try {
      setIsBooting(true);
      setPageError(null);
      epgReadyKeysRef.current = new Set();
      epgLastAttemptRef.current = new Map();
      epgBusyKeysRef.current = new Set();

      let built: { channels: Channel[]; groups: string[] };
      if (directLiveEntry) {
        built = buildDirectLiveCatalog(directLiveEntry);
      } else {
        const response = await fetch(withAccessToken("/api/player/channels"));
        if (!response.ok) throw new Error("failedToLoadPlaylist");
        const data = (await response.json()) as ServerChannelsEnvelope;
        built = buildCatalogFromServerPayload(data.list ?? []);
        if (built.channels.length === 0) throw new Error("emptyPlaylist");
        setEpgBackendConfig(data.epgSource ?? null);
      }

      setCatalog({ channels: built.channels, groups: built.groups });

      const lastChannelId = getLastChannelId();
      // 深链 > 上次观看 > 第一个频道；独立直播入口永远锁定它唯一的频道
      const deepLinked = directLiveEntry ? null : findDeepLinkChannel(built.channels);
      const initialChannel = directLiveEntry
        ? built.channels[0]
        : deepLinked ?? built.channels.find((channel) => channel.id === lastChannelId) ?? built.channels[0];
      tuneToChannel(initialChannel);

      // 先铺一层缝隙填充数据，列表骨架立即可渲染，真实节目单到了再覆盖
      setEpgSchedules(fillEPGGaps({}, built.channels));
      setIsVeilLeaving(true);
      window.setTimeout(() => setIsBooting(false), REVEAL_HOLD_MS);
    } catch (err) {
      setPageError(err instanceof Error ? err.message : "failedToLoadPlaylist");
      setIsBooting(false);
    }
  }, [tuneToChannel, directLiveEntry]);

  useEffect(() => {
    bootstrapCatalog();
  }, [bootstrapCatalog]);

  // ---- 派生数据：当前正在播出的节目（供 OSD 与节目单栏高亮）-----------------
  const nowPlayingProgram = useMemo(() => {
    if (!playingChannel) return null;
    const epgChannelId = getEPGChannelId(playingChannel, epgSchedules);
    if (!epgChannelId) return null;
    const absoluteTime = mseToWallClock(deferredPlaybackClock, streamWallClockBase);
    return getCurrentProgram(epgChannelId, epgSchedules, absoluteTime);
  }, [playingChannel, epgSchedules, streamWallClockBase, deferredPlaybackClock]);

  const reportPlaybackError = useCallback((message: string) => setPageError(message), []);

  // ---- 自动切线路（故障转移）-----------------------------------------------
  // 组件内的 notice 会被新流的 ingest 立即清掉，瞬态提示只能在页层挂。
  const [failoverHint, setFailoverHint] = useState<string | null>(null);
  const failoverHintTimerRef = useRef(0);
  const showFailoverHint = useCallback((text: string) => {
    setFailoverHint(text);
    if (failoverHintTimerRef.current) window.clearTimeout(failoverHintTimerRef.current);
    failoverHintTimerRef.current = window.setTimeout(() => setFailoverHint(null), FAILOVER_HINT_MS);
  }, []);
  /** 播放器自动故障转移：提示一句，再按既有换源语义切换（live 重开 / 回看续播）。 */
  const handleSourceFailover = useCallback(
    (nextIndex: number) => {
      showFailoverHint(t("sourceFallback"));
      switchSourceTrack(nextIndex);
    },
    [showFailoverHint, switchSourceTrack, t],
  );

  // ---- 全屏（真实全屏优先，方向锁/桌面环境退化成模拟全屏）--------------------
  const toggleImmersiveView = useCallback(async (): Promise<boolean> => {
    const stage = pageStageRef.current;
    if (!stage) return false;

    if (document.fullscreenElement) {
      try {
        await document.exitFullscreen();
        releaseOrientationLock();
        setIsPanelShown(true);
        return true;
      } catch {
        return false;
      }
    }

    if (pseudoFullscreenRef.current) {
      pseudoFullscreenRef.current = false;
      releaseOrientationLock();
      setIsFullscreenActive(false);
      setIsPanelShown(true);
      return true;
    }

    const enterPseudoFullscreen = () => {
      pseudoFullscreenRef.current = true;
      setIsFullscreenActive(true);
      setIsPanelShown(false);
      return true;
    };

    try {
      await stage.requestFullscreen();
      await tryLockLandscapeOrientation();
      setIsFullscreenActive(true);
      setIsPanelShown(false);
      return true;
    } catch {
      // requestFullscreen 被拒：能锁横屏就当"伪全屏"用；桌面端不锁也接受；
      // 手机上两者都不可得才算失败（用户留在原布局）。
      if (await tryLockLandscapeOrientation()) return enterPseudoFullscreen();
      if (!isCompactLayout) return enterPseudoFullscreen();
      return false;
    }
  }, [isCompactLayout]);

  const applySeamlessPreference = useCallback(
    (enabled: boolean) => {
      if (!canSwitchSeamlessly) return;
      setSeamlessEnabled(enabled);
      saveSeamlessSwitch(enabled);
    },
    [canSwitchSeamlessly],
  );

  const applyDeinterlacePreference = useCallback((enabled: boolean) => {
    setDeinterlaceEnabled(enabled);
    saveAutoDeinterlace(enabled);
  }, []);

  const applyEnhancementPreference = useCallback((enabled: boolean) => {
    setEnhancementEnabled(enabled);
    savePictureEnhancement(enabled);
  }, []);

  const applyAudioModePreference = useCallback((mode: "stereo" | "mono") => {
    setAudioTrackModeState(mode);
    saveAudioChannelMode(mode);
  }, []);

  const flipPanelVisibility = useCallback(() => {
    setIsPanelShown((previous) => !previous);
  }, []);

  /**
   * 点播放画面：桌面端切换侧栏（设计：点屏幕唤出选台 → 选好自动收起）。
   * 移动端侧栏是常驻整屏 Tab，不参与这个手势。
   */
  const handleStageTap = useCallback(() => {
    if (isCompactLayout) return;
    setIsPanelShown((previous) => !previous);
  }, [isCompactLayout]);

  /** 列表选台：切流即自动收起侧栏（"选好就藏"）。 */
  const chooseChannelThenCollapse = useCallback(
    (channel: Channel) => {
      tuneToChannel(channel);
      if (!isCompactLayout) setIsPanelShown(false);
    },
    [isCompactLayout, tuneToChannel],
  );

  // ---- 设置下拉（memo 化：设置面板重，避免无关播放状态更新连带重渲染）--------
  const settingsWidget = useMemo(() => {
    return (
      <div className="shrink-0">
        <SettingsDropdown
          locale={pageLocale}
          theme={theme}
          onThemeChange={applyThemeChoice}
          appearance={appearance}
          onAppearanceChange={applyAppearanceChoice}
          panelAlpha={panelAlpha}
          onPanelAlphaChange={setPanelAlpha}
          pictureInPictureMode={pictureInPictureMode}
          onPictureInPictureModeChange={setPictureInPictureMode}
          showPictureInPictureMode={supportsDocPiP}
          seamlessSwitch={seamlessEnabled}
          onSeamlessSwitchChange={applySeamlessPreference}
          showSeamlessSwitch={canSwitchSeamlessly}
          autoDeinterlace={deinterlaceEnabled}
          onAutoDeinterlaceChange={applyDeinterlacePreference}
          pictureEnhancement={enhancementEnabled}
          onPictureEnhancementChange={applyEnhancementPreference}
          audioChannelMode={audioTrackMode}
          onAudioChannelModeChange={applyAudioModePreference}
          showVideoProcessing={supportsVideoProcessing}
        />
      </div>
    );
  }, [
    pageLocale,
    theme,
    appearance,
    panelAlpha,
    pictureInPictureMode,
    seamlessEnabled,
    deinterlaceEnabled,
    enhancementEnabled,
    audioTrackMode,
    applyThemeChoice,
    applyAppearanceChoice,
    setPictureInPictureMode,
    applySeamlessPreference,
    applyDeinterlacePreference,
    applyEnhancementPreference,
    applyAudioModePreference,
    canSwitchSeamlessly,
    supportsDocPiP,
    supportsVideoProcessing,
  ]);

  // ---- 渲染：正常播放布局（目录装载失败时才走错误页）------------------------
  const shouldRenderFailurePage = Boolean(pageError && !catalog);
  if (!shouldRenderFailurePage) {
    // 浮层是否上屏：移动端常驻（除非手机全屏）；桌面端跟随手动开关
    const isDockPanelMounted = (isPanelShown || isCompactLayout) && !(isFullscreenActive && isCompactLayout);

    return (
      <div ref={pageStageRef} className={PAGE_SHELL_CLASSES}>
        <title>{t("title")}</title>

        <div
          className={clsx(
            DOCK_COLUMN_CLASSES,
            !isCompactLayout && isPanelShown && "player-performance-dock-open",
            !isCompactLayout && (isDockEpgExpanded ? "player-performance-dock-expanded" : "player-performance-dock-compact"),
          )}
        >
          <div
            className={clsx(
              VIDEO_STAGE_CLASSES,
              // 手机非全屏：高度由 16:9 比例给出。视频舞台是"尺寸容器"（container-type:size，
              // 见 styles/index.css），自身高度不再计入父级；这里若仍靠内容撑高，外层会塌成 0 高，
              // 下方面板（flex-1）会顶到屏幕最上沿、把画面整个盖住。
              !isFullscreenActive && "aspect-video md:aspect-auto",
              // 手机全屏时视频区铺满整屏（flex-1 拿到确定高度）；平时按内容高（16:9 横条）
              isFullscreenActive ? "min-h-0 flex-1" : "shrink-0",
            )}
          >
            <PlaybackTimeProvider value={playbackClock}>
              <VideoPlayer
                channel={playingChannel}
                segments={engineSegments}
                playMode={streamMode}
                onError={reportPlaybackError}
                locale={pageLocale}
                currentProgram={nowPlayingProgram}
                onSeek={performSeek}
                onStreamStartTimeChange={setStreamWallClockBase}
                streamStartTime={streamWallClockBase}
                onCurrentVideoTimeChange={trackPlaybackClock}
                onChannelNavigate={stepChannel}
                prevChannel={previousInOrder}
                nextChannel={nextInOrder}
                showSidebar={isPanelShown}
                onToggleSidebar={flipPanelVisibility}
                onSurfaceClick={handleStageTap}
                isFullscreen={isFullscreenActive}
                onFullscreenToggle={toggleImmersiveView}
                seamlessSwitch={canSwitchSeamlessly && seamlessEnabled}
                autoDeinterlace={deinterlaceEnabled}
                pictureEnhancement={enhancementEnabled}
                audioChannelMode={audioTrackMode}
                pictureInPictureMode={pictureInPictureMode}
                activeSourceIndex={sourceTrackIndex}
                onSourceChange={switchSourceTrack}
                onSourceFailover={handleSourceFailover}
                onPlaybackStarted={rememberSourceOnStart}
              />
            </PlaybackTimeProvider>
            {/* 自动切线路提示：绝对定位锚定视频舞台（sticky/absolute 均为定位上下文） */}
            {failoverHint && (
              <div className="pointer-events-none absolute inset-x-0 bottom-14 z-30 flex justify-center md:bottom-16">
                <div className="rounded-full bg-slate-950/85 px-4 py-1.5 text-xs font-medium text-violet-50 shadow-lg backdrop-blur md:text-sm">
                  {failoverHint}
                </div>
              </div>
            )}
          </div>

          <div
            className={clsx(
              // 浮层平面化（TV 播放器风格）：单层半透明底 + 一条右缘细线，不再叠主题渐变与投影
              DOCK_PANEL_CLASSES,
              insetPanelRight && "pr-[env(safe-area-inset-right)]",
              isDockPanelMounted ? "" : "hidden",
            )}
          >
            {!isCompactLayout ? (
              <ChannelBrowser
                channels={catalog?.channels ?? []}
                groups={catalog?.groups ?? []}
                currentChannel={playingChannel}
                previewChannel={browsingChannel}
                onPreviewChannelChange={setBrowsingChannel}
                onChannelSelect={chooseChannelThenCollapse}
                onProgramSelect={pickProgramFromBrowser}
                locale={pageLocale}
                settingsSlot={settingsWidget}
                epgData={epgSchedules}
                currentPlayingProgram={nowPlayingProgram}
                supportsCatchup={!!browsingChannel?.sources.some((source) => source.timeshift && source.timeshiftTemplate)}
                onEpgOpenChange={setIsDockEpgExpanded}
                onVisibleChannelsChange={prefetchSchedulesForVisible}
                epgConfigured={hasEpgBackend}
                panelVisible={isDockPanelMounted}
              />
            ) : (
              <>
                <div className={MOBILE_TAB_BAR_CLASSES}>
                  {(["channels", "epg"] as const).map((view) => (
                    <button
                      type="button"
                      key={view}
                      onClick={() => switchPanelView(view)}
                      className={clsx(
                        MOBILE_TAB_BASE_CLASSES,
                        focusedPanelView === view ? MOBILE_TAB_ACTIVE_CLASSES : MOBILE_TAB_IDLE_CLASSES,
                      )}
                    >
                      {view === "channels" ? `${t("channels")} (${catalog?.channels.length || 0})` : t("programGuide")}
                    </button>
                  ))}
                </div>

                <div className="flex-1 overflow-hidden">
                  <Activity mode={mountedPanelView === "channels" ? "visible" : "hidden"}>
                    <ChannelList
                      channels={catalog?.channels}
                      groups={catalog?.groups}
                      currentChannel={playingChannel}
                      onChannelSelect={chooseChannelThenCollapse}
                      locale={pageLocale}
                      settingsSlot={settingsWidget}
                      epgData={epgSchedules}
                      panelVisible={!isFullscreenActive}
                    />
                  </Activity>
                  <Activity mode={mountedPanelView === "epg" ? "visible" : "hidden"}>
                    <EPGView
                      channelId={playingChannel ? getEPGChannelId(playingChannel, epgSchedules) ?? null : null}
                      epgData={epgSchedules}
                      onProgramSelect={pickProgramForPlayback}
                      locale={pageLocale}
                      supportsCatchup={!!playingChannel?.sources.some((source) => source.timeshift && source.timeshiftTemplate)}
                      currentPlayingProgram={nowPlayingProgram}
                    />
                  </Activity>
                </div>
              </>
            )}
          </div>
        </div>

        {isBooting && (
          <div className={clsx(BOOT_VEIL_CLASSES, isVeilLeaving && "animate-zoom-fade-out")}>
            <div className="text-center space-y-4">
              <div className={BOOT_SPINNER_CLASSES} />
            </div>
          </div>
        )}
      </div>
    );
  }

  // ---- 渲染：目录装载失败页 -------------------------------------------------
  const failureChecklist = [t("playlistErrorHintReachable"), t("playlistErrorHintFormat")];
  const failureCause = pageError ? t(pageError) : null;

  return (
    <div className={FAILURE_PAGE_SHELL_CLASSES}>
      <title>{t("title")}</title>
      <div className={FAILURE_PAGE_INNER_CLASSES}>
        <Card className={FAILURE_CARD_CLASSES}>
          <div className="grid min-w-0 md:grid-cols-[minmax(0,1fr)_18rem]">
            <div className="min-w-0 p-6 sm:p-8 md:p-10">
              <div className="mb-5 flex h-12 w-12 items-center justify-center rounded-2xl border border-rose-300/20 bg-[linear-gradient(145deg,rgba(251,113,133,0.16),rgba(var(--pg-rgb-2),0.12))] text-rose-500 shadow-[0_12px_28px_rgba(225,29,72,0.12)] dark:text-rose-300">
                <AlertTriangle className="h-6 w-6" aria-hidden="true" />
              </div>

              <div className="font-semibold text-violet-700 text-sm dark:text-violet-200">{t("playlistLoadEyebrow")}</div>
              <h1 className="mt-2 text-balance font-semibold text-2xl text-foreground leading-tight tracking-tight sm:text-3xl">
                {t("playlistLoadTitle")}
              </h1>
              <p className="mt-3 max-w-2xl text-pretty break-words text-sm leading-6 text-muted-foreground sm:text-base">
                {t("playlistLoadDescription")}
              </p>

              <div className="mt-6 min-w-0 rounded-2xl border border-violet-900/10 bg-violet-50/45 p-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.65)] dark:border-violet-100/10 dark:bg-violet-300/6">
                <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                  <ListChecks className="h-4 w-4 text-violet-600 dark:text-violet-300" aria-hidden="true" />
                  {t("playlistErrorChecklist")}
                </div>
                {/* 清单项渲染收口到独立组件：条目结构（圆点 + 文案）只有这一处使用 */}
                <FailureChecklist hints={failureChecklist} />
              </div>

              <div className="mt-6 flex flex-col items-stretch gap-3 sm:flex-row sm:items-center">
                <Button
                  onClick={bootstrapCatalog}
                  type="button"
                  variant="outline"
                  className="w-full gap-2 rounded-xl border-primary/20 bg-violet-700 bg-[linear-gradient(135deg,var(--pg-grad-a),var(--pg-grad-c))] text-white shadow-[0_10px_28px_rgba(var(--pg-rgb),0.24)] transition-[color,background-color,border-color] hover:border-primary/30 hover:bg-[linear-gradient(135deg,var(--pg-grad-a),var(--pg-grad-c))] hover:text-white sm:w-auto"
                >
                  <RefreshCw className="h-4 w-4" aria-hidden="true" />
                  {t("retry")}
                </Button>
                <a
                  href="#/"
                  className={buttonVariants({
                    variant: "outline",
                    className:
                      "w-full gap-2 rounded-xl border-violet-900/12 bg-white/55 text-violet-800 shadow-sm hover:bg-violet-50 dark:border-violet-100/15 dark:bg-slate-950/35 dark:text-violet-100 dark:hover:bg-violet-300/10 sm:w-auto",
                  })}
                >
                  {t("playlistEndpoint")}
                </a>
              </div>
            </div>

            <div className="min-w-0 border-violet-900/10 border-t bg-[linear-gradient(145deg,rgba(255,255,255,0.5),rgba(248,250,252,0.6))] p-6 dark:border-violet-100/10 dark:bg-[linear-gradient(145deg,rgba(15,23,42,0.24),rgba(15,23,42,0.34))] md:border-t-0 md:border-l md:p-8">
              <div className="text-sm font-semibold text-foreground">{t("playlistEndpoint")}</div>
              <div className="mt-3 break-all rounded-xl border border-violet-900/10 bg-white/55 px-3 py-2 font-mono text-foreground text-sm leading-5 shadow-inner dark:border-violet-100/10 dark:bg-slate-950/42">
                {withAccessToken("/api/player/channels")}
              </div>
              <div className="mt-6 text-sm font-semibold text-foreground">{t("technicalDetails")}</div>
              <p className="mt-2 break-words text-sm leading-6 text-muted-foreground">{failureCause}</p>
            </div>
          </div>
        </Card>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root") as HTMLElement).render(
  <StrictMode>
    <PlayerScreen />
  </StrictMode>,
);
