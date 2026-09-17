/**
 * 播放页。
 * 数据层全部走 TVGate 服务端 API（频道 / EPG / 回看均由服务端签发受控短地址，真实源不出服务端）；
 * 引擎契约来自 ../../media-engine。本文件只做状态编排与布局，不含任何解码/MSE 逻辑。
 */
import "../lib/polyfills"; // 旧 WebView 兼容 polyfill，必须在业务代码前
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
  parseEpgTime,
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
// TVGate 数据层：频道列表 / EPG / 回看全部走服务端 API，真实源地址不出服务端。
// ---------------------------------------------------------------------------

interface TvgateChannelPayload {
  key: string;
  name: string;
  group?: string;
  scheme?: string; // udp / rtp / rtsp / http / https
  tvg_id?: string;
  tvg_name?: string;
  tvg_logo?: string;
  epg_type?: string;
}

interface TvgateChannelsResponse {
  channels?: TvgateChannelPayload[];
  epg?: { type?: string; template?: string; logo?: string };
}

/** 从页面 URL 取全局访问 token（my_token），透传给所有 API 与流请求。 */
function getMyToken(): string {
  try {
    return new URLSearchParams(window.location.search).get("my_token") ?? "";
  } catch {
    return "";
  }
}

const MY_TOKEN = getMyToken();

function withToken(url: string): string {
  if (!MY_TOKEN) return url;
  return url + (url.includes("?") ? "&" : "?") + "my_token=" + encodeURIComponent(MY_TOKEN);
}

function pad2(n: number): string {
  return (n < 10 ? "0" : "") + n;
}

function todayYmd(): string {
  const d = new Date();
  return `${d.getFullYear()}${pad2(d.getMonth() + 1)}${pad2(d.getDate())}`;
}

/** EPG 重试冷却：取失败/空结果后到点可重试（一次失败不再整场放弃）。 */
const EPG_RETRY_COOLDOWN_MS = 60_000;
/** EPG 定时刷新间隔：与后端 EPG 源刷新同量级，长时间播放时"正在播出"不漂移。 */
const EPG_REFRESH_MS = 15 * 60 * 1000;
/** 可见频道预取上限：xml 源是服务端查表（快）可多取；template 源要逐频道外网拉取（保守）。 */
const EPG_PREFETCH_LIMIT_XML = 40;
const EPG_PREFETCH_LIMIT_TEMPLATE = 12;
const EPG_PREFETCH_STAGGER_MS = 120;

function toYmdHis(value: string): string {
  const d = parseEpgTime(value);
  if (!d) return "";
  return (
    `${d.getFullYear()}${pad2(d.getMonth() + 1)}${pad2(d.getDate())}` +
    `${pad2(d.getHours())}${pad2(d.getMinutes())}${pad2(d.getSeconds())}`
  );
}

/**
 * 独立直播入口：`?live=<URL 编码后的播放地址>`（推流发布页本地 FLV/HLS 用它跳转）。
 * 只接受同源地址（发布流的 /<path>/play/<name>.flv|m3u8），拒绝跨源地址。
 */
function getLiveDirectUrl(): string | null {
  try {
    const raw = new URLSearchParams(window.location.search).get("live");
    if (!raw) return null;
    const url = new URL(raw, window.location.origin);
    if (url.origin !== window.location.origin) return null;
    return url.href;
  } catch {
    return null;
  }
}

/** 从播放地址取频道名（/live/play/cctv1.flv → cctv1）。 */
function channelNameFromUrl(url: string): string {
  try {
    const name = new URL(url).pathname.split("/").filter(Boolean).pop() || "";
    return name.replace(/\.(?:flv|m3u8)$/i, "") || "直播";
  } catch {
    return "直播";
  }
}

/** 服务端 /api/player/channels → 播放器 Channel 模型。源地址为受控短地址 /player/<key>。 */
function mapChannels(payload: TvgateChannelPayload[]): { channels: Channel[]; groups: string[] } {
  const channels: Channel[] = [];
  const groupSet = new Set<string>();
  for (const c of payload) {
    if (!c?.key || !c.name) continue;
    const source: Source = { url: withToken(`/player/${c.key}`), label: c.scheme || undefined };
    if (c.scheme === "http" || c.scheme === "https" || c.scheme === "php" || c.scheme === "rtsp") {
      source.catchup = "server";
      source.catchupSource = "server";
    }
    const groups = c.group ? [c.group] : [];
    if (c.group) groupSet.add(c.group);
    channels.push({
      id: c.key,
      name: c.name,
      logo: c.tvg_logo || undefined,
      groups,
      // 频道号 = 订阅里的序位（1 起）：界面展示用，替代裸短哈希 id
      number: channels.length + 1,
      tvgId: c.tvg_id || undefined,
      tvgName: c.tvg_name || undefined,
      sources: [source],
    });
  }
  return { channels, groups: [...groupSet] };
}

/** EPGData 键与 getEPGChannelId 的回退逻辑保持一致：tvgId → tvgName → name。 */
function epgIdForChannel(channel: Channel): string {
  return channel.tvgId || channel.tvgName || channel.name;
}

type LockableScreenOrientation = ScreenOrientation & { lock?: (orientation: "landscape") => Promise<void> };

async function lockScreenToLandscape(): Promise<boolean> {
  const orientation = screen.orientation as LockableScreenOrientation | undefined;
  if (!orientation?.lock) return false;
  try {
    await orientation.lock("landscape");
    return true;
  } catch {
    return false;
  }
}

function unlockScreenOrientation(): void {
  try {
    screen.orientation?.unlock();
  } catch {
    // 全屏结束时 orientation 可能已被解锁。
  }
}

function shouldInsetSidebarRight(): boolean {
  const { angle, type } = screen.orientation;
  if (!type.startsWith("landscape")) return true;
  return angle !== 90;
}

function PlayerPage() {
  const playbackBackendKind = getPlaybackBackendKind();
  const supportsMSEVideoProcessing = playbackBackendKind === "mse";
  const supportsSeamlessSwitch = !isLGWebOS();
  const supportsDocumentPictureInPicture = isDocumentPictureInPictureSupported();
  const { theme, setTheme } = useTheme("tvgate-player-theme");
  const { appearance, setAppearance } = usePlayerAppearance();
  const { panelAlpha, setPanelAlpha } = usePlayerPanelAlpha();
  const [pictureInPictureMode, setPictureInPictureMode] = usePersistedEnum<PictureInPictureMode>(
    "tvgate-player-picture-in-picture-mode",
    "document",
    PICTURE_IN_PICTURE_MODES,
  );
  const locale: Locale = "zh-Hans";
  const t = usePlayerTranslation(locale);

  const [metadata, setMetadata] = useState<M3UMetadata | null>(null);
  const [epgData, setEpgData] = useState<EPGData>({});
  /** 已拿到真实节目（成功）的频道键——只有成功才写入，失败走冷却重试。 */
  const epgLoadedRef = useRef<Set<string>>(new Set());
  /** 正在飞的 EPG 请求（去重）。 */
  const epgInflightRef = useRef<Set<string>>(new Set());
  /** 每个频道的上次尝试时间（失败/空结果后的冷却用）。 */
  const epgAttemptAtRef = useRef<Map<string, number>>(new Map());
  /** 后端下发的 EPG 源（type/template/logo）：预取限流与"未配置"提示都基于它。 */
  const [epgSource, setEpgSource] = useState<{ type?: string; template?: string; logo?: string } | null>(null);
  const [currentChannel, setCurrentChannel] = useState<Channel | null>(null);
  const [previewChannel, setPreviewChannel] = useState<Channel | null>(null);
  const [playMode, setPlayMode] = useState<"live" | "catchup">("live");
  const [playbackSegments, setPlaybackSegments] = useState<PlayerSegment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isRevealing, setIsRevealing] = useState(false);
  const [showSidebar, setShowSidebar] = useState(() => getSidebarVisible());
  /** 浮层内"节目单"栏是否展开：决定浮层宽度（收起=两列窄 / 展开=三列宽）。 */
  const [dockEpgOpen, setDockEpgOpen] = useState(false);
  const [selectedSidebarView, setSelectedSidebarView] = useState<"channels" | "epg">("channels");
  const [renderedSidebarView, setRenderedSidebarView] = useState<"channels" | "epg">("channels");
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [isMobile, setIsMobile] = useState(() => window.innerWidth < 768);
  const [insetSidebarRight, setInsetSidebarRight] = useState(shouldInsetSidebarRight);
  /** 与上面两个状态同步的即时值：resize 里用它判断"值真的变了"才重渲染。 */
  const isMobileRef = useRef(isMobile);
  const insetSidebarRightRef = useRef(insetSidebarRight);
  const [seamlessSwitch, setSeamlessSwitch] = useState(() => (supportsSeamlessSwitch ? getSeamlessSwitch() : false));
  const [autoDeinterlace, setAutoDeinterlace] = useState(() =>
    supportsMSEVideoProcessing ? getAutoDeinterlace() : false,
  );
  const [pictureEnhancement, setPictureEnhancement] = useState(() =>
    supportsMSEVideoProcessing ? getPictureEnhancement() : false,
  );
  /** 软解音频声道模式（本地记忆）：mono = 左右合成单声道。 */
  const [audioChannelMode, setAudioChannelModeState] = useState<"stereo" | "mono">(() => getAudioChannelMode());
  const pageContainerRef = useRef<HTMLDivElement>(null);
  const isSimulatedFullscreenRef = useRef(false);

  const [streamStartTime, setStreamStartTime] = useState<Date>(() => new Date());
  const [seekAtLiveEdge, setSeekAtLiveEdge] = useState(true);
  const [catchupUrl, setCatchupUrl] = useState<string | null>(null);
  const catchupSeqRef = useRef(0);

  const [currentVideoTime, setCurrentVideoTime] = useState(0);
  const deferredCurrentVideoTime = useDeferredValue(currentVideoTime);
  const currentVideoTimeRef = useRef(0);
  const currentVideoSecondRef = useRef(0);

  const [activeSourceIndex, setActiveSourceIndex] = useState(0);
  const activeSource = currentChannel?.sources[activeSourceIndex] ?? currentChannel?.sources[0];

  useEffect(() => {
    const handleFullscreenChange = () => {
      const isDocumentFullscreen = !!document.fullscreenElement;
      if (!isDocumentFullscreen && isSimulatedFullscreenRef.current) return;
      setIsFullscreen(isDocumentFullscreen);
      if (!isDocumentFullscreen) {
        unlockScreenOrientation();
        setShowSidebar(true);
      }
    };
    document.addEventListener("fullscreenchange", handleFullscreenChange);
    return () => document.removeEventListener("fullscreenchange", handleFullscreenChange);
  }, []);

  useEffect(() => {
    // 手机浏览器工具栏收缩/展开（进出全屏常见）会连发 resize：
    // 值没变就绝不 setState——否则整页反复重排，表现为画面抽动/闪屏。
    // 高度变化不影响内宽，所以这类 resize 基本会被直接跳过；只在真跨断点时更新。
    let timer = 0;
    const applyViewport = () => {
      timer = 0;
      const nextMobile = window.innerWidth < 768;
      const nextInset = shouldInsetSidebarRight();
      if (nextMobile === isMobileRef.current && nextInset === insetSidebarRightRef.current) return;
      isMobileRef.current = nextMobile;
      insetSidebarRightRef.current = nextInset;
      startTransition(() => {
        setIsMobile(nextMobile);
        setInsetSidebarRight(nextInset);
      });
    };
    const handleViewportChange = () => {
      if (timer) window.clearTimeout(timer);
      timer = window.setTimeout(applyViewport, 120);
    };
    window.addEventListener("resize", handleViewportChange);
    screen.orientation.addEventListener("change", handleViewportChange);
    return () => {
      if (timer) window.clearTimeout(timer);
      window.removeEventListener("resize", handleViewportChange);
      screen.orientation.removeEventListener("change", handleViewportChange);
    };
  }, []);

  useEffect(() => {
    if (!activeSource || !seekAtLiveEdge) return;
    setPlayMode("live");
    setPlaybackSegments((prev) => {
      const next: PlayerSegment[] = [{ url: activeSource.url, duration: 0 }];
      if (prev.length === 1 && prev[0].url === next[0].url) return prev;
      return next;
    });
  }, [currentChannel, activeSource, activeSourceIndex, seekAtLiveEdge]);

  useEffect(() => {
    if (seekAtLiveEdge || !catchupUrl) return;
    setPlayMode("catchup");
    setPlaybackSegments((prev) => {
      if (prev.length === 1 && prev[0].url === catchupUrl) return prev;
      return [{ url: catchupUrl, duration: 0 }];
    });
  }, [catchupUrl, seekAtLiveEdge]);

  const resetCurrentVideoTime = useCallback(() => {
    currentVideoTimeRef.current = 0;
    currentVideoSecondRef.current = 0;
    setCurrentVideoTime(0);
  }, []);

  const handleVideoSeek = useCallback(
    (seekTime: Date, goingLive: boolean, channel?: Channel) => {
      const targetChannel = channel ?? currentChannel;
      resetCurrentVideoTime();
      if (goingLive) {
        catchupSeqRef.current += 1;
        setCatchupUrl(null);
        setStreamStartTime(new Date());
        setSeekAtLiveEdge(true);
        setPlayMode("live");
        return;
      }
      if (!targetChannel) return;

      const seq = ++catchupSeqRef.current;
      const start = toYmdHis(seekTime.toISOString());
      const end = toYmdHis(new Date().toISOString());
      setStreamStartTime(seekTime);
      setSeekAtLiveEdge(false);
      setPlayMode("catchup");
      fetch(withToken(`/api/player/catchup?key=${encodeURIComponent(targetChannel.id)}&start=${start}&end=${end}`))
        .then((res) => {
          if (!res.ok) throw new Error(`catchup HTTP ${res.status}`);
          return res.json() as Promise<{ url?: string }>;
        })
        .then((data) => {
          if (seq !== catchupSeqRef.current) return;
          if (data?.url) setCatchupUrl(withToken(data.url));
        })
        .catch(() => {
          if (seq !== catchupSeqRef.current) return;
          setCatchupUrl(null);
          setStreamStartTime(new Date());
          setSeekAtLiveEdge(true);
          setPlayMode("live");
        });
    },
    [currentChannel, resetCurrentVideoTime],
  );

  const handleProgramSelect = useCallback(
    (programStart: Date, programEnd: Date) => {
      const goingLive = programEnd.getTime() >= Date.now() - NEAR_LIVE_EDGE_MS;
      handleVideoSeek(programStart, goingLive);
      // 桌面/大屏：选完节目自动隐藏侧边栏（设计：点屏幕出现 → 选好收起）
      if (!isMobile) setShowSidebar(false);
      // 移动端：节目单是整屏 Tab，选完自动切回频道列表（等于收起节目单）
      setSelectedSidebarView("channels");
      startTransition(() => setRenderedSidebarView("channels"));
    },
    [handleVideoSeek, isMobile],
  );

  const handleSourceChange = useCallback(
    (sourceIndex: number) => {
      if (playMode === "live") {
        catchupSeqRef.current += 1;
        setCatchupUrl(null);
        setSeekAtLiveEdge(true);
        setStreamStartTime(new Date());
      } else {
        setStreamStartTime(mseToWallClock(currentVideoTimeRef.current, streamStartTime));
      }
      resetCurrentVideoTime();
      setActiveSourceIndex(sourceIndex);
    },
    [playMode, resetCurrentVideoTime, streamStartTime],
  );

  const handlePlaybackStarted = useCallback(() => {
    if (currentChannel) saveLastSourceIndex(currentChannel.id, activeSourceIndex);
  }, [currentChannel, activeSourceIndex]);

  const selectChannel = useCallback(
    (channel: Channel) => {
      resetCurrentVideoTime();
      catchupSeqRef.current += 1;
      setCatchupUrl(null);
      setCurrentChannel(channel);
      const lastSource = getLastSourceIndex(channel.id);
      setActiveSourceIndex(lastSource < channel.sources.length ? lastSource : 0);
      setSeekAtLiveEdge(true);
      setStreamStartTime(new Date());
    },
    [resetCurrentVideoTime],
  );

  const handleReplaySelect = useCallback(
    (programStart: Date, programEnd: Date) => {
      const target = previewChannel ?? currentChannel;
      if (!target) return;
      if (target.id !== currentChannel?.id) selectChannel(target);
      const goingLive = programEnd.getTime() >= Date.now() - NEAR_LIVE_EDGE_MS;
      handleVideoSeek(programStart, goingLive, target);
    },
    [previewChannel, currentChannel, selectChannel, handleVideoSeek],
  );

  useEffect(() => {
    if (currentChannel && playMode === "live") saveLastChannelId(currentChannel.id);
  }, [currentChannel, playMode]);

  useEffect(() => {
    if (currentChannel && metadata) syncChannelDeepLink(currentChannel, metadata.channels);
  }, [currentChannel, metadata]);

  useEffect(() => {
    if (!metadata) return;
    const onHashChange = () => {
      const channel = findDeepLinkChannel(metadata.channels);
      if (channel && channel.id !== currentChannel?.id) selectChannel(channel);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [metadata, currentChannel, selectChannel]);

  /**
   * 拉单个频道的节目单（服务端已解析 XMLTV / 按模板代拉，前端不直连 EPG 源）。
   * 关键：只有**拿到真实节目**才算"已加载"；失败/空结果只记尝试时间，冷却后可重试。
   */
  const loadEpgForChannel = useCallback((channel: Channel, opts?: { force?: boolean }) => {
    const epgId = epgIdForChannel(channel);
    if (!epgId || epgInflightRef.current.has(epgId)) return;
    if (!opts?.force) {
      if (epgLoadedRef.current.has(epgId)) return;
      const last = epgAttemptAtRef.current.get(epgId) ?? 0;
      if (Date.now() - last < EPG_RETRY_COOLDOWN_MS) return;
    }
    epgAttemptAtRef.current.set(epgId, Date.now());
    epgInflightRef.current.add(epgId);

    const q =
      `date=${todayYmd()}` +
      `&ch=${encodeURIComponent(channel.tvgId ?? "")}` +
      `&name=${encodeURIComponent(channel.name)}`;
    fetch(withToken(`/api/player/epg?${q}`))
      .then((res) => (res.ok ? (res.json() as Promise<{ programs?: RawEPGProgram[] }>) : Promise.reject(new Error("epgFailed"))))
      .then((data) => {
        const progs = mapPrograms(data.programs);
        if (!progs.length) throw new Error("epgEmpty");
        epgLoadedRef.current.add(epgId);
        startTransition(() => {
          setEpgData((prev) => ({ ...prev, [epgId]: progs }));
        });
      })
      .catch(() => {
        // 失败/为空：不写"已加载"，退避后可重试；界面保留缝隙填充占位。
      })
      .finally(() => {
        epgInflightRef.current.delete(epgId);
      });
  }, []);

  useEffect(() => {
    if (currentChannel) setPreviewChannel((prev) => prev ?? currentChannel);
  }, [currentChannel]);

  useEffect(() => {
    if (!currentChannel) return;
    loadEpgForChannel(currentChannel);
  }, [currentChannel, loadEpgForChannel]);

  useEffect(() => {
    if (!previewChannel) return;
    loadEpgForChannel(previewChannel);
  }, [previewChannel, loadEpgForChannel]);

  /**
   * 可见频道批量预取：让节目单栏与列表行显示真实节目，而不是只有"精彩节目"占位。
   * 按后端 EPG 源类型限流，并错峰发起（template 源每次都要服务端外网拉取，故上限更低）。
   */
  const handleVisibleChannelsChange = useCallback(
    (visible: Channel[]) => {
      const limit = epgSource?.type === "template" ? EPG_PREFETCH_LIMIT_TEMPLATE : EPG_PREFETCH_LIMIT_XML;
      const pending = visible.slice(0, limit).filter((channel) => {
        const id = epgIdForChannel(channel);
        return (
          !!id &&
          !epgLoadedRef.current.has(id) &&
          !epgInflightRef.current.has(id) &&
          Date.now() - (epgAttemptAtRef.current.get(id) ?? 0) >= EPG_RETRY_COOLDOWN_MS
        );
      });
      pending.forEach((channel, index) => {
        window.setTimeout(() => loadEpgForChannel(channel), index * EPG_PREFETCH_STAGGER_MS);
      });
    },
    [epgSource?.type, loadEpgForChannel],
  );

  /** 定时刷新：清掉"已加载"标记后重取当前/预览频道（跨天、节目推进都能跟上）。 */
  useEffect(() => {
    const timer = window.setInterval(() => {
      epgLoadedRef.current = new Set();
      epgAttemptAtRef.current = new Map();
      if (currentChannel) loadEpgForChannel(currentChannel, { force: true });
      if (previewChannel) loadEpgForChannel(previewChannel, { force: true });
    }, EPG_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [currentChannel, previewChannel, loadEpgForChannel]);

  /** 后端是否配置了 EPG 源：未配置时节目单栏直接给提示，避免误判成"前端坏了"。 */
  const epgConfigured = epgSource?.type === "xml" || epgSource?.type === "template";

  const handleCurrentVideoTimeChange = useCallback((time: number) => {
    currentVideoTimeRef.current = time;
    const currentSecond = Math.floor(time);
    if (currentSecond === currentVideoSecondRef.current) return;
    currentVideoSecondRef.current = currentSecond;
    setCurrentVideoTime(time);
  }, []);

  const handleThemeChange = useCallback(
    (nextTheme: Parameters<typeof setTheme>[0]) => startTransition(() => setTheme(nextTheme)),
    [setTheme],
  );

  const handleAppearanceChange = useCallback(
    (nextAppearance: Parameters<typeof setAppearance>[0]) => startTransition(() => setAppearance(nextAppearance)),
    [setAppearance],
  );

  const handleSidebarViewChange = useCallback((view: "channels" | "epg") => {
    (view === "channels" ? channelListNextScrollBehaviorRef : epgViewNextScrollBehaviorRef).current = "instant";
    setSelectedSidebarView(view);
    startTransition(() => setRenderedSidebarView(view));
  }, []);

  const handleChannelNavigate = useCallback(
    (target: "prev" | "next" | number) => {
      if (!metadata?.channels.length) return;
      if (target === "prev" || target === "next") {
        if (!currentChannel) return;
        const currentIndex = metadata.channels.indexOf(currentChannel);
        let nextIndex = 0;
        if (target === "prev") {
          nextIndex = currentIndex > 0 ? currentIndex - 1 : metadata.channels.length - 1;
        } else {
          nextIndex = currentIndex < metadata.channels.length - 1 ? currentIndex + 1 : 0;
        }
        selectChannel(metadata.channels[nextIndex]);
      } else {
        const channel = metadata.channels[target - 1];
        if (channel) selectChannel(channel);
      }
    },
    [metadata, currentChannel, selectChannel],
  );

  const [prevChannel, nextChannel] = useMemo<[Channel | null, Channel | null]>(() => {
    const channels = metadata?.channels;
    if (!channels?.length || !currentChannel) return [null, null];
    const currentIndex = channels.indexOf(currentChannel);
    if (currentIndex < 0) return [null, null];
    return [
      channels[currentIndex > 0 ? currentIndex - 1 : channels.length - 1],
      channels[currentIndex < channels.length - 1 ? currentIndex + 1 : 0],
    ];
  }, [metadata, currentChannel]);

  const directLiveUrl = useMemo(getLiveDirectUrl, []);

  const loadPlaylist = useCallback(async () => {
    try {
      setIsLoading(true);
      setError(null);
      epgLoadedRef.current = new Set();
      epgAttemptAtRef.current = new Map();
      epgInflightRef.current = new Set();

      let mapped: { channels: Channel[]; groups: string[] };
      if (directLiveUrl) {
        const name = channelNameFromUrl(directLiveUrl);
        const liveChannel: Channel = {
          id: `direct-${name}`,
          name,
          groups: [],
          number: 1,
          sources: [{ url: withToken(directLiveUrl) }],
        };
        mapped = { channels: [liveChannel], groups: [] };
      } else {
        const response = await fetch(withToken("/api/player/channels"));
        if (!response.ok) throw new Error("failedToLoadPlaylist");
        const data = (await response.json()) as TvgateChannelsResponse;
        mapped = mapChannels(data.channels ?? []);
        if (mapped.channels.length === 0) throw new Error("emptyPlaylist");
        setEpgSource(data.epg ?? null);
      }

      setMetadata({ channels: mapped.channels, groups: mapped.groups });

      const deepLinkChannel = directLiveUrl ? mapped.channels[0] : findDeepLinkChannel(mapped.channels);
      const lastChannelId = getLastChannelId();
      const channelToSelect =
        directLiveUrl
          ? mapped.channels[0]
          : deepLinkChannel ?? mapped.channels.find((channel) => channel.id === lastChannelId) ?? mapped.channels[0];
      selectChannel(channelToSelect);

      setEpgData(fillEPGGaps({}, mapped.channels));
      setIsRevealing(true);
      window.setTimeout(() => setIsLoading(false), 500);
    } catch (err) {
      setError(err instanceof Error ? err.message : "failedToLoadPlaylist");
      setIsLoading(false);
    }
  }, [selectChannel, directLiveUrl]);

  useEffect(() => {
    loadPlaylist();
  }, [loadPlaylist]);

  const currentVideoProgram = useMemo(() => {
    if (!currentChannel) return null;
    const epgChannelId = getEPGChannelId(currentChannel, epgData);
    if (!epgChannelId) return null;
    const absoluteTime = mseToWallClock(deferredCurrentVideoTime, streamStartTime);
    return getCurrentProgram(epgChannelId, epgData, absoluteTime);
  }, [currentChannel, epgData, streamStartTime, deferredCurrentVideoTime]);

  const handleVideoError = useCallback((err: string) => setError(err), []);

  const handleFullscreenToggle = useCallback(async (): Promise<boolean> => {
    const pageContainer = pageContainerRef.current;
    if (!pageContainer) return false;

    if (document.fullscreenElement) {
      try {
        await document.exitFullscreen();
        unlockScreenOrientation();
        setShowSidebar(true);
        return true;
      } catch {
        return false;
      }
    }

    if (isSimulatedFullscreenRef.current) {
      isSimulatedFullscreenRef.current = false;
      unlockScreenOrientation();
      setIsFullscreen(false);
      setShowSidebar(true);
      return true;
    }

    try {
      await pageContainer.requestFullscreen();
      await lockScreenToLandscape();
      setIsFullscreen(true);
      setShowSidebar(false);
      return true;
    } catch {
      if (await lockScreenToLandscape()) {
        isSimulatedFullscreenRef.current = true;
        setIsFullscreen(true);
        setShowSidebar(false);
        return true;
      }
      if (!isMobile) {
        isSimulatedFullscreenRef.current = true;
        setIsFullscreen(true);
        setShowSidebar(false);
        return true;
      }
      return false;
    }
  }, [isMobile]);

  const handleSeamlessSwitchChange = useCallback(
    (enabled: boolean) => {
      if (!supportsSeamlessSwitch) return;
      setSeamlessSwitch(enabled);
      saveSeamlessSwitch(enabled);
    },
    [supportsSeamlessSwitch],
  );

  const handleAutoDeinterlaceChange = useCallback((enabled: boolean) => {
    setAutoDeinterlace(enabled);
    saveAutoDeinterlace(enabled);
  }, []);

  const handlePictureEnhancementChange = useCallback((enabled: boolean) => {
    setPictureEnhancement(enabled);
    savePictureEnhancement(enabled);
  }, []);

  const handleAudioChannelModeChange = useCallback((mode: "stereo" | "mono") => {
    setAudioChannelModeState(mode);
    saveAudioChannelMode(mode);
  }, []);

  const handleToggleSidebar = useCallback(() => {
    setShowSidebar((prev) => !prev);
  }, []);

  /**
   * 点击播放画面：切换侧边栏（设计：点屏幕出侧边栏选节目 → 选完自动隐藏）。
   * 移动端侧栏是整屏 Tab（始终可用），不参与该手势。
   */
  const handleSurfaceClick = useCallback(() => {
    if (isMobile) return;
    setShowSidebar((prev) => !prev);
  }, [isMobile]);

  /** 选台：切流后自动收起侧边栏（"选好就自动隐藏"）。 */
  const handleChannelSelectAndHide = useCallback(
    (channel: Parameters<typeof selectChannel>[0]) => {
      selectChannel(channel);
      if (!isMobile) setShowSidebar(false);
    },
    [isMobile, selectChannel],
  );

  const settingsSlot = useMemo(() => {
    return (
      <div className="shrink-0">
        <SettingsDropdown
          locale={locale}
          theme={theme}
          onThemeChange={handleThemeChange}
          appearance={appearance}
          onAppearanceChange={handleAppearanceChange}
          panelAlpha={panelAlpha}
          onPanelAlphaChange={setPanelAlpha}
          pictureInPictureMode={pictureInPictureMode}
          onPictureInPictureModeChange={setPictureInPictureMode}
          showPictureInPictureMode={supportsDocumentPictureInPicture}
          seamlessSwitch={seamlessSwitch}
          onSeamlessSwitchChange={handleSeamlessSwitchChange}
          showSeamlessSwitch={supportsSeamlessSwitch}
          autoDeinterlace={autoDeinterlace}
          onAutoDeinterlaceChange={handleAutoDeinterlaceChange}
          pictureEnhancement={pictureEnhancement}
          onPictureEnhancementChange={handlePictureEnhancementChange}
          audioChannelMode={audioChannelMode}
          onAudioChannelModeChange={handleAudioChannelModeChange}
          showVideoProcessing={supportsMSEVideoProcessing}
        />
      </div>
    );
  }, [
    locale,
    theme,
    appearance,
    panelAlpha,
    pictureInPictureMode,
    seamlessSwitch,
    autoDeinterlace,
    pictureEnhancement,
    audioChannelMode,
    handleThemeChange,
    handleAppearanceChange,
    setPictureInPictureMode,
    handleSeamlessSwitchChange,
    handleAutoDeinterlaceChange,
    handlePictureEnhancementChange,
    handleAudioChannelModeChange,
    supportsSeamlessSwitch,
    supportsDocumentPictureInPicture,
    supportsMSEVideoProcessing,
  ]);

  const hasPlaylistLoadError = Boolean(error && !metadata);
  if (!hasPlaylistLoadError) {
    return (
      <div
        ref={pageContainerRef}
        className="player-performance-page-background player-performance-scope player-viewport-height relative flex flex-col bg-[radial-gradient(circle_at_92%_8%,rgba(var(--pg-rgb),0.15),transparent_28%),radial-gradient(circle_at_72%_92%,rgba(var(--pg-rgb-2),0.13),transparent_32%),linear-gradient(145deg,var(--pg-bg-a),var(--pg-bg-b))] dark:bg-[radial-gradient(circle_at_88%_10%,rgba(var(--pg-rgb),0.1),transparent_30%),radial-gradient(circle_at_70%_88%,rgba(var(--pg-rgb-2),0.12),transparent_34%),linear-gradient(145deg,var(--pg-bg-dark-a),var(--pg-bg-dark-b))]"
      >
        <title>{t("title")}</title>

        <div
          className={clsx(
            "relative flex min-h-0 flex-1 flex-col overflow-hidden",
            !isMobile && showSidebar && "player-performance-dock-open",
            !isMobile && (dockEpgOpen ? "player-performance-dock-expanded" : "player-performance-dock-compact"),
          )}
        >
          <div
            className={clsx(
              "w-full sticky md:absolute md:inset-0",
              // 手机全屏时视频区铺满整屏（flex-1 拿到确定高度）；平时按内容高（16:9 横条）
              isFullscreen ? "min-h-0 flex-1" : "shrink-0",
            )}
          >
            <PlaybackTimeProvider value={currentVideoTime}>
              <VideoPlayer
                channel={currentChannel}
                segments={playbackSegments}
                playMode={playMode}
                onError={handleVideoError}
                locale={locale}
                currentProgram={currentVideoProgram}
                onSeek={handleVideoSeek}
                onStreamStartTimeChange={setStreamStartTime}
                streamStartTime={streamStartTime}
                onCurrentVideoTimeChange={handleCurrentVideoTimeChange}
                onChannelNavigate={handleChannelNavigate}
                prevChannel={prevChannel}
                nextChannel={nextChannel}
                showSidebar={showSidebar}
                onToggleSidebar={handleToggleSidebar}
                onSurfaceClick={handleSurfaceClick}
                isFullscreen={isFullscreen}
                onFullscreenToggle={handleFullscreenToggle}
                seamlessSwitch={supportsSeamlessSwitch && seamlessSwitch}
                autoDeinterlace={autoDeinterlace}
                pictureEnhancement={pictureEnhancement}
                audioChannelMode={audioChannelMode}
                pictureInPictureMode={pictureInPictureMode}
                activeSourceIndex={activeSourceIndex}
                onSourceChange={handleSourceChange}
                onPlaybackStarted={handlePlaybackStarted}
              />
            </PlaybackTimeProvider>
          </div>

          <div
            className={clsx(
              // 浮层平面化（TV 播放器风格）：单层半透明底 + 一条右缘细线，不再叠主题渐变与投影
              "player-performance-dock flex w-full flex-1 flex-col overflow-hidden border-violet-950/10 border-t bg-white/80 pl-[env(safe-area-inset-left)] backdrop-blur-2xl backdrop-saturate-150 dark:border-violet-100/10 dark:bg-slate-950/80 dark:backdrop-brightness-[0.6] md:absolute md:inset-y-0 md:left-0 md:z-20 md:h-full md:flex-none md:border-t-0 md:border-r md:pt-[env(safe-area-inset-top)] md:pr-0",
              insetSidebarRight && "pr-[env(safe-area-inset-right)]",
              (showSidebar || isMobile) && !(isFullscreen && isMobile) ? "" : "hidden",
            )}
          >
            {!isMobile ? (
              <ChannelBrowser
                channels={metadata?.channels ?? []}
                groups={metadata?.groups ?? []}
                currentChannel={currentChannel}
                previewChannel={previewChannel}
                onPreviewChannelChange={setPreviewChannel}
                onChannelSelect={handleChannelSelectAndHide}
                onProgramSelect={handleReplaySelect}
                locale={locale}
                settingsSlot={settingsSlot}
                epgData={epgData}
                currentPlayingProgram={currentVideoProgram}
                supportsCatchup={!!previewChannel?.sources.some((s) => s.catchup && s.catchupSource)}
                onEpgOpenChange={setDockEpgOpen}
                onVisibleChannelsChange={handleVisibleChannelsChange}
                epgConfigured={epgConfigured}
              />
            ) : (
              <>
                <div className="player-performance-panel-background flex shrink-0 items-center border-violet-950/10 border-b bg-white/44 shadow-[0_8px_24px_rgba(var(--pg-rgb),0.045)] backdrop-blur-xl dark:border-violet-100/10 dark:bg-[linear-gradient(90deg,var(--pg-bg-dark-a),var(--pg-bg-dark-b))]">
                  {(["channels", "epg"] as const).map((view) => (
                    <button
                      type="button"
                      key={view}
                      onClick={() => handleSidebarViewChange(view)}
                      className={clsx(
                        "player-performance-motion min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap border-b-2 px-3 py-2 text-center font-semibold text-xs leading-5 tracking-[0.01em] transition-[color,background-color,border-color,box-shadow] md:px-4 md:py-3 md:text-sm",
                        selectedSidebarView === view
                          ? "border-violet-500 bg-[linear-gradient(to_top,rgba(var(--pg-rgb),0.12),transparent)] text-violet-700 shadow-[inset_0_-1px_0_rgba(var(--pg-rgb),0.18)] dark:border-violet-300 dark:text-violet-200"
                          : "cursor-pointer border-transparent text-slate-500 hover:bg-violet-400/5 hover:text-violet-700 dark:text-slate-400 dark:hover:text-violet-100",
                      )}
                    >
                      {view === "channels" ? `${t("channels")} (${metadata?.channels.length || 0})` : t("programGuide")}
                    </button>
                  ))}
                </div>

                <div className="flex-1 overflow-hidden">
                  <Activity mode={renderedSidebarView === "channels" ? "visible" : "hidden"}>
                    <ChannelList
                      channels={metadata?.channels}
                      groups={metadata?.groups}
                      currentChannel={currentChannel}
                      onChannelSelect={handleChannelSelectAndHide}
                      locale={locale}
                      settingsSlot={settingsSlot}
                      epgData={epgData}
                    />
                  </Activity>
                  <Activity mode={renderedSidebarView === "epg" ? "visible" : "hidden"}>
                    <EPGView
                      channelId={currentChannel ? getEPGChannelId(currentChannel, epgData) ?? null : null}
                      epgData={epgData}
                      onProgramSelect={handleProgramSelect}
                      locale={locale}
                      supportsCatchup={!!currentChannel?.sources.some((s) => s.catchup && s.catchupSource)}
                      currentPlayingProgram={currentVideoProgram}
                    />
                  </Activity>
                </div>
              </>
            )}
          </div>
        </div>

        {isLoading && (
          <div
            className={clsx(
              "player-performance-page-background player-performance-motion absolute inset-0 z-50 flex items-center justify-center bg-[radial-gradient(circle_at_center,rgba(var(--pg-rgb),0.16),transparent_28%),radial-gradient(circle_at_65%_60%,rgba(var(--pg-rgb-2),0.14),transparent_35%),linear-gradient(145deg,var(--pg-bg-a),var(--pg-bg-b))] pt-[max(1rem,env(safe-area-inset-top))] pr-[max(1rem,env(safe-area-inset-right))] pb-[max(1rem,env(safe-area-inset-bottom))] pl-[max(1rem,env(safe-area-inset-left))] dark:bg-[radial-gradient(circle_at_center,rgba(var(--pg-rgb),0.11),transparent_30%),radial-gradient(circle_at_65%_60%,rgba(var(--pg-rgb-2),0.12),transparent_38%),linear-gradient(145deg,var(--pg-bg-dark-a),var(--pg-bg-dark-b))]",
              isRevealing && "animate-zoom-fade-out",
            )}
          >
            <div className="text-center space-y-4">
              <div className="player-performance-loading-spinner mx-auto h-12 w-12 animate-spin rounded-full border-4 border-violet-950/10 border-t-violet-500 border-r-(--pg-grad-b) shadow-[0_0_28px_rgba(var(--pg-rgb),0.22)] dark:border-violet-100/10 dark:border-t-violet-300 dark:border-r-(--pg-grad-b)" />
            </div>
          </div>
        )}
      </div>
    );
  }

  const playlistErrorHints = [t("playlistErrorHintReachable"), t("playlistErrorHintFormat")];
  const errorMessage = error ? t(error) : null;

  return (
    <div className="player-performance-page-background player-performance-scope player-viewport-height overflow-y-auto bg-[radial-gradient(circle_at_18%_14%,rgba(var(--pg-rgb),0.16),transparent_28%),radial-gradient(circle_at_84%_82%,rgba(var(--pg-rgb-2),0.16),transparent_32%),linear-gradient(145deg,var(--pg-bg-a),var(--pg-bg-b))] dark:bg-[radial-gradient(circle_at_18%_14%,rgba(var(--pg-rgb),0.1),transparent_30%),radial-gradient(circle_at_84%_82%,rgba(var(--pg-rgb-2),0.13),transparent_34%),linear-gradient(145deg,var(--pg-bg-dark-a),var(--pg-bg-dark-b))]">
      <title>{t("title")}</title>
      <div className="mx-auto flex min-h-full w-[calc(100%-2rem)] max-w-5xl items-center py-8 sm:w-[calc(100%-3rem)]">
        <Card className="player-performance-panel-background min-w-0 w-full overflow-hidden rounded-3xl border-violet-900/10 bg-white/72 shadow-[0_28px_80px_rgba(var(--pg-rgb),0.16),inset_0_1px_0_rgba(255,255,255,0.85)] backdrop-blur-2xl dark:border-violet-100/12 dark:bg-[linear-gradient(145deg,rgba(2,6,23,0.9),rgba(15,23,42,0.82))] dark:shadow-[0_30px_90px_rgba(2,6,16,0.62),inset_0_1px_0_rgba(255,255,255,0.08)]">
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
                <ul className="mt-3 space-y-2 text-sm leading-5 text-muted-foreground">
                  {playlistErrorHints.map((hint) => (
                    <li key={hint} className="flex min-w-0 gap-2">
                      <span
                        className="mt-2 h-1.5 w-1.5 shrink-0 rounded-full bg-violet-500 shadow-[0_0_8px_rgba(var(--pg-rgb),0.45)]"
                        aria-hidden="true"
                      />
                      <span className="min-w-0 break-words">{hint}</span>
                    </li>
                  ))}
                </ul>
              </div>

              <div className="mt-6 flex flex-col items-stretch gap-3 sm:flex-row sm:items-center">
                <Button
                  type="button"
                  variant="outline"
                  onClick={loadPlaylist}
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
                {withToken("/api/player/channels")}
              </div>
              <div className="mt-6 text-sm font-semibold text-foreground">{t("technicalDetails")}</div>
              <p className="mt-2 break-words text-sm leading-6 text-muted-foreground">{errorMessage}</p>
            </div>
          </div>
        </Card>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root") as HTMLElement).render(
  <StrictMode>
    <PlayerPage />
  </StrictMode>,
);
