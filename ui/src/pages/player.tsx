import { clsx } from "clsx";
import { AlertTriangle, ListChecks, RefreshCw } from "lucide-react";
import {
  Activity,
  StrictMode,
  startTransition,
  useCallback,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createRoot } from "react-dom/client";
import {
  ChannelList,
  nextScrollBehaviorRef as channelListNextScrollBehaviorRef,
} from "../components/player/channel-list";
import { EPGView, nextScrollBehaviorRef as epgViewNextScrollBehaviorRef } from "../components/player/epg-view";
import { PlaybackTimeProvider } from "../components/player/playback-time-context";
import { SettingsDropdown } from "../components/player/settings-dropdown";
import { VideoPlayer } from "../components/player/video-player";
import { Button, buttonVariants } from "../components/ui/button";
import { Card } from "../components/ui/card";
import { useLocale } from "../hooks/use-locale";
import { usePersistedEnum } from "../hooks/use-persisted-enum";
import { usePlayerAppearance } from "../hooks/use-player-appearance";
import { usePlayerTranslation } from "../hooks/use-player-translation";
import { useTheme } from "../hooks/use-player-theme";
import { isDocumentPictureInPictureSupported } from "../lib/document-picture-in-picture";
import { type EPGData, fillEPGGaps, getCurrentProgram, getEPGChannelId } from "../lib/epg-parser";
import type { Locale } from "../lib/locale";
import type { Channel, EPGProgram, M3UMetadata, Source } from "../types/player";
import { isLGWebOS } from "../lib/platform";
import { findDeepLinkChannel, syncChannelDeepLink } from "../lib/player-deep-link";
import {
  getAutoDeinterlace,
  getLastChannelId,
  getLastSourceIndex,
  getPictureEnhancement,
  getSeamlessSwitch,
  getSidebarVisible,
  saveAutoDeinterlace,
  saveLastChannelId,
  saveLastSourceIndex,
  savePictureEnhancement,
  saveSeamlessSwitch,
  saveSidebarVisible,
} from "../lib/player-storage";
import { getPlaybackBackendKind, type PlayerSegment } from "../playback-engine";
import { mseToWallClock, NEAR_LIVE_EDGE_MS } from "../playback-engine/timeline/wall-clock";
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

interface TvgateProgram {
  start: string;
  stop: string;
  title: string;
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

/**
 * 任意 EPG 时间 → Date：兼容 XMLTV（"20260901112900 +0800"）、
 * 纯数字（YYYYMMDDHHMMSS / YYYYMMDDHHMM）、ISO（"2026-09-01T11:29:00"）。
 * 与服务端/旧版语义一致：按本地时区解释。
 */
function parseEpgTime(value: string): Date | null {
  const s = (value ?? "").trim();
  if (!s) return null;

  if (s.length === 5 && s.includes(":")) {
    const [h, m] = s.split(":");
    const d = new Date();
    d.setHours(Number(h), Number(m), 0, 0);
    return d;
  }

  if (/^\d{8}(?:\d{2}){0,3}$/.test(s)) {
    const y = Number(s.slice(0, 4));
    const mo = Number(s.slice(4, 6)) - 1;
    const da = Number(s.slice(6, 8));
    const h = Number(s.slice(8, 10) || 0);
    const mi = Number(s.slice(10, 12) || 0);
    const se = Number(s.slice(12, 14) || 0);
    return new Date(y, mo, da, h, mi, se);
  }

  const normalized = s.includes("T") ? s : s.replace(" ", "T");
  const parsed = new Date(normalized);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

function toYmdHis(value: string): string {
  const d = parseEpgTime(value);
  if (!d) return "";
  return (
    `${d.getFullYear()}${pad2(d.getMonth() + 1)}${pad2(d.getDate())}` +
    `${pad2(d.getHours())}${pad2(d.getMinutes())}${pad2(d.getSeconds())}`
  );
}

/** 服务端 /api/player/channels → 播放器 Channel 模型。源地址为受控短地址 /player/<key>。 */
function mapChannels(payload: TvgateChannelPayload[]): { channels: Channel[]; groups: string[] } {
  const channels: Channel[] = [];
  const groupSet = new Set<string>();
  for (const c of payload) {
    if (!c?.key || !c.name) continue;
    const source: Source = { url: withToken(`/player/${c.key}`), label: c.scheme || undefined };
    // http(s) 源由服务端提供 catchup（/api/player/catchup），打标记供 UI 与 EPG 缝隙填充识别。
    if (c.scheme === "http" || c.scheme === "https") {
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

function mapPrograms(programs: TvgateProgram[] | undefined): EPGProgram[] {
  const out: EPGProgram[] = [];
  for (const p of programs ?? []) {
    const start = parseEpgTime(p.start ?? "");
    if (!start) continue;
    const end = parseEpgTime(p.stop ?? p.start ?? "");
    if (!end || end.getTime() <= start.getTime()) continue;
    out.push({ id: `epg-${start.getTime()}`, title: p.title || "", start, end });
  }
  return out;
}

type LockableScreenOrientation = ScreenOrientation & {
  lock?: (orientation: "landscape") => Promise<void>;
};

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
    // The orientation may already have been unlocked when fullscreen ended.
  }
}

function shouldInsetSidebarRight(): boolean {
  const { angle, type } = screen.orientation;
  if (!type.startsWith("landscape")) return true;

  // At 90°, the sidebar's right edge is on the device-bottom side and may
  // overlap the smaller system area. Preserve the inset at 270° and for other
  // angles, including naturally landscape devices.
  return angle !== 90;
}

function PlayerPage() {
  const playbackBackendKind = getPlaybackBackendKind();
  const supportsMSEVideoProcessing = playbackBackendKind === "mse";
  const supportsSeamlessSwitch = !isLGWebOS();
  const supportsDocumentPictureInPicture = isDocumentPictureInPictureSupported();
  const { locale, setLocale } = useLocale("tvgate-player-locale");
  const { theme, setTheme } = useTheme("tvgate-player-theme");
  const { appearance, setAppearance } = usePlayerAppearance();
  const [pictureInPictureMode, setPictureInPictureMode] = usePersistedEnum<PictureInPictureMode>(
    "tvgate-player-picture-in-picture-mode",
    "document",
    PICTURE_IN_PICTURE_MODES,
  );
  const t = usePlayerTranslation(locale);

  const [metadata, setMetadata] = useState<M3UMetadata | null>(null);
  const [epgData, setEpgData] = useState<EPGData>({});
  const epgLoadedRef = useRef<Set<string>>(new Set());
  const [currentChannel, setCurrentChannel] = useState<Channel | null>(null);
  const [playMode, setPlayMode] = useState<"live" | "catchup">("live");
  const [playbackSegments, setPlaybackSegments] = useState<PlayerSegment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isRevealing, setIsRevealing] = useState(false);
  const [showSidebar, setShowSidebar] = useState(() => getSidebarVisible());
  const [selectedSidebarView, setSelectedSidebarView] = useState<"channels" | "epg">("channels");
  const [renderedSidebarView, setRenderedSidebarView] = useState<"channels" | "epg">("channels");
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [isMobile, setIsMobile] = useState(() => window.innerWidth < 768);
  const [insetSidebarRight, setInsetSidebarRight] = useState(shouldInsetSidebarRight);
  const [seamlessSwitch, setSeamlessSwitch] = useState(() => (supportsSeamlessSwitch ? getSeamlessSwitch() : false));
  const [autoDeinterlace, setAutoDeinterlace] = useState(() =>
    supportsMSEVideoProcessing ? getAutoDeinterlace() : false,
  );
  const [pictureEnhancement, setPictureEnhancement] = useState(() =>
    supportsMSEVideoProcessing ? getPictureEnhancement() : false,
  );
  const pageContainerRef = useRef<HTMLDivElement>(null);
  const isSimulatedFullscreenRef = useRef(false);

  // Track stream start time - the absolute time position when current stream started
  // For live mode: now (no seeking)
  // For catchup mode: the time user seeked to (start of catchup stream)
  const [streamStartTime, setStreamStartTime] = useState<Date>(() => new Date());
  /** Whether the latest seek targets the session live edge (vs catchup). */
  const [seekAtLiveEdge, setSeekAtLiveEdge] = useState(true);
  /** 回看流地址（服务端 /api/player/catchup 签发的受控短地址）。 */
  const [catchupUrl, setCatchupUrl] = useState<string | null>(null);
  const catchupSeqRef = useRef(0);

  // Track current video playback time in seconds (relative to stream start)
  const [currentVideoTime, setCurrentVideoTime] = useState(0);
  const deferredCurrentVideoTime = useDeferredValue(currentVideoTime);
  const currentVideoTimeRef = useRef(0);
  const currentVideoSecondRef = useRef(0);

  // Track active source index for multi-source channels
  const [activeSourceIndex, setActiveSourceIndex] = useState(0);

  // Get the active source's URL
  const activeSource = currentChannel?.sources[activeSourceIndex] ?? currentChannel?.sources[0];

  // Track fullscreen state
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
    return () => {
      document.removeEventListener("fullscreenchange", handleFullscreenChange);
    };
  }, []);

  // Track responsive layout and which physical edge is on the sidebar's right.
  useEffect(() => {
    const handleViewportChange = () => {
      startTransition(() => {
        setIsMobile(window.innerWidth < 768);
        setInsetSidebarRight(shouldInsetSidebarRight());
      });
    };

    window.addEventListener("resize", handleViewportChange);
    screen.orientation.addEventListener("change", handleViewportChange);
    return () => {
      window.removeEventListener("resize", handleViewportChange);
      screen.orientation.removeEventListener("change", handleViewportChange);
    };
  }, []);

  // Live playback: single zero-duration segment; the engine sniffs the content
  // (raw TS stream or HLS playlist) automatically.
  useEffect(() => {
    if (!activeSource || !seekAtLiveEdge) return;

    setPlayMode("live");
    setPlaybackSegments((prev) => {
      const next: PlayerSegment[] = [{ url: activeSource.url, duration: 0 }];
      if (prev.length === 1 && prev[0].url === next[0].url) {
        return prev;
      }
      return next;
    });
  }, [currentChannel, activeSource, activeSourceIndex, seekAtLiveEdge]);

  // Catchup playback: server-issued VOD playlist URL (starts at the seek target).
  useEffect(() => {
    if (seekAtLiveEdge || !catchupUrl) return;

    setPlayMode("catchup");
    setPlaybackSegments((prev) => {
      if (prev.length === 1 && prev[0].url === catchupUrl) {
        return prev;
      }
      return [{ url: catchupUrl, duration: 0 }];
    });
  }, [catchupUrl, seekAtLiveEdge]);

  const resetCurrentVideoTime = useCallback(() => {
    currentVideoTimeRef.current = 0;
    currentVideoSecondRef.current = 0;
    setCurrentVideoTime(0);
  }, []);

  const handleVideoSeek = useCallback(
    (seekTime: Date, goingLive: boolean) => {
      resetCurrentVideoTime();
      if (goingLive) {
        catchupSeqRef.current += 1;
        setCatchupUrl(null);
        setStreamStartTime(new Date());
        setSeekAtLiveEdge(true);
        // 立即切徽标，不等流切换完成
        setPlayMode("live");
        return;
      }
      if (!currentChannel) return;

      // TVGate catchup: ask the server for a playlist that starts at the target time.
      const seq = ++catchupSeqRef.current;
      const start = toYmdHis(seekTime.toISOString());
      const end = toYmdHis(new Date().toISOString());
      setStreamStartTime(seekTime);
      setSeekAtLiveEdge(false);
      // 立即切徽标：回看 URL 由服务端异步签发，期间也应显示「返回直播」
      setPlayMode("catchup");
      fetch(withToken(`/api/player/catchup?key=${encodeURIComponent(currentChannel.id)}&start=${start}&end=${end}`))
        .then((res) => {
          if (!res.ok) throw new Error(`catchup HTTP ${res.status}`);
          return res.json() as Promise<{ url?: string }>;
        })
        .then((data) => {
          if (seq !== catchupSeqRef.current) return;
          if (data?.url) {
            setCatchupUrl(withToken(data.url));
          }
        })
        .catch(() => {
          if (seq !== catchupSeqRef.current) return;
          // 回看失败：回到直播
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
    },
    [handleVideoSeek],
  );

  const handleSourceChange = useCallback(
    (sourceIndex: number) => {
      if (playMode === "live") {
        catchupSeqRef.current += 1;
        setCatchupUrl(null);
        setSeekAtLiveEdge(true);
        setStreamStartTime(new Date());
      } else {
        // Preserve current playback position when switching source in catchup mode
        setStreamStartTime(mseToWallClock(currentVideoTimeRef.current, streamStartTime));
      }
      resetCurrentVideoTime();
      setActiveSourceIndex(sourceIndex);
    },
    [playMode, resetCurrentVideoTime, streamStartTime],
  );

  const handlePlaybackStarted = useCallback(() => {
    if (currentChannel) {
      saveLastSourceIndex(currentChannel.id, activeSourceIndex);
    }
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

  // Save last played channel when in live mode
  useEffect(() => {
    if (currentChannel && playMode === "live") {
      saveLastChannelId(currentChannel.id);
    }
  }, [currentChannel, playMode]);

  // Keep the address bar shareable: rewrite the URL to #<name> (or #<id> if the name is ambiguous).
  useEffect(() => {
    if (currentChannel && metadata) {
      syncChannelDeepLink(currentChannel, metadata.channels);
    }
  }, [currentChannel, metadata]);

  useEffect(() => {
    if (!metadata) return;

    const onHashChange = () => {
      const channel = findDeepLinkChannel(metadata.channels);
      if (channel && channel.id !== currentChannel?.id) {
        selectChannel(channel);
      }
    };

    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [metadata, currentChannel, selectChannel]);

  // Fetch EPG for the selected channel (server-side fetch/parse, today only).
  useEffect(() => {
    if (!currentChannel) return;
    const epgId = epgIdForChannel(currentChannel);
    if (!epgId || epgLoadedRef.current.has(epgId)) return;
    epgLoadedRef.current.add(epgId);

    const q =
      `date=${todayYmd()}` +
      `&ch=${encodeURIComponent(currentChannel.tvgId ?? "")}` +
      `&name=${encodeURIComponent(currentChannel.name)}`;
    fetch(withToken(`/api/player/epg?${q}`))
      .then((res) => (res.ok ? (res.json() as Promise<{ programs?: TvgateProgram[] }>) : Promise.reject(new Error("epgFailed"))))
      .then((data) => {
        const progs = mapPrograms(data.programs);
        if (!progs.length) return;
        startTransition(() => {
          setEpgData((prev) => ({ ...prev, [epgId]: progs }));
        });
      })
      .catch(() => {
        // EPG 不可用：保留缝隙填充占位
      });
  }, [currentChannel]);

  const handleCurrentVideoTimeChange = useCallback((time: number) => {
    currentVideoTimeRef.current = time;
    const currentSecond = Math.floor(time);
    if (currentSecond === currentVideoSecondRef.current) return;
    currentVideoSecondRef.current = currentSecond;
    setCurrentVideoTime(time);
  }, []);

  const handleLocaleChange = useCallback(
    (nextLocale: Locale) => {
      startTransition(() => setLocale(nextLocale));
    },
    [setLocale],
  );

  const handleThemeChange = useCallback(
    (nextTheme: Parameters<typeof setTheme>[0]) => {
      startTransition(() => setTheme(nextTheme));
    },
    [setTheme],
  );

  const handleAppearanceChange = useCallback(
    (nextAppearance: Parameters<typeof setAppearance>[0]) => {
      startTransition(() => setAppearance(nextAppearance));
    },
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
          // Wrap around to last channel if at first channel
          nextIndex = currentIndex > 0 ? currentIndex - 1 : metadata.channels.length - 1;
        } else {
          // Wrap around to first channel if at last channel
          nextIndex = currentIndex < metadata.channels.length - 1 ? currentIndex + 1 : 0;
        }
        selectChannel(metadata.channels[nextIndex]);
      } else {
        const channel = metadata.channels[target - 1];
        if (channel) {
          selectChannel(channel);
        }
      }
    },
    [metadata, currentChannel, selectChannel],
  );

  // Neighbours of the current channel, so the player can preview the target of a
  // swipe-to-zap gesture. Wraps around exactly like handleChannelNavigate.
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

  const loadPlaylist = useCallback(async () => {
    try {
      setIsLoading(true);
      setError(null);
      epgLoadedRef.current = new Set();

      const response = await fetch(withToken("/api/player/channels"));
      if (!response.ok) {
        throw new Error("failedToLoadPlaylist");
      }

      const data = (await response.json()) as TvgateChannelsResponse;
      const mapped = mapChannels(data.channels ?? []);

      if (mapped.channels.length === 0) {
        throw new Error("emptyPlaylist");
      }

      setMetadata({ channels: mapped.channels, groups: mapped.groups });

      const deepLinkChannel = findDeepLinkChannel(mapped.channels);
      const lastChannelId = getLastChannelId();
      const channelToSelect =
        deepLinkChannel ?? mapped.channels.find((channel) => channel.id === lastChannelId) ?? mapped.channels[0];
      selectChannel(channelToSelect);

      // Show empty-EPG fallback immediately so startup is not blocked by EPG fetching.
      // Catchup-capable channels get 2-hour gap-fill programs until real data arrives.
      setEpgData(fillEPGGaps({}, mapped.channels));

      // Trigger reveal animation
      setIsRevealing(true);
      window.setTimeout(() => {
        setIsLoading(false);
      }, 500); // Match animate-zoom-fade-out duration
    } catch (err) {
      setError(err instanceof Error ? err.message : "failedToLoadPlaylist");
      setIsLoading(false);
    }
  }, [selectChannel]);

  // Load playlist on mount
  useEffect(() => {
    loadPlaylist();
  }, [loadPlaylist]);

  // Get current program for the video player
  // Use tvgId / tvgName / name with fallback logic for EPG matching
  // Use streamStartTime + currentVideoTime to determine the actual time position
  const currentVideoProgram = useMemo(() => {
    if (!currentChannel) return null;

    // Get EPG channel ID using fallback logic (tvgId -> tvgName -> name)
    const epgChannelId = getEPGChannelId(currentChannel, epgData);
    if (!epgChannelId) return null;

    // Calculate absolute time based on stream start + current video position
    const absoluteTime = mseToWallClock(deferredCurrentVideoTime, streamStartTime);
    return getCurrentProgram(epgChannelId, epgData, absoluteTime);
  }, [currentChannel, epgData, streamStartTime, deferredCurrentVideoTime]);

  const handleVideoError = useCallback((err: string) => {
    setError(err);
  }, []);

  // Handle fullscreen toggle
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

  const handleToggleSidebar = useCallback(() => {
    setShowSidebar((prev) => {
      const newState = !prev;
      saveSidebarVisible(newState);
      return newState;
    });
  }, []);

  const settingsSlot = useMemo(() => {
    return (
      <div className="shrink-0">
        <SettingsDropdown
          locale={locale}
          onLocaleChange={handleLocaleChange}
          theme={theme}
          onThemeChange={handleThemeChange}
          appearance={appearance}
          onAppearanceChange={handleAppearanceChange}
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
          showVideoProcessing={supportsMSEVideoProcessing}
        />
      </div>
    );
  }, [
    locale,
    theme,
    appearance,
    pictureInPictureMode,
    seamlessSwitch,
    autoDeinterlace,
    pictureEnhancement,
    handleLocaleChange,
    handleThemeChange,
    handleAppearanceChange,
    setPictureInPictureMode,
    handleSeamlessSwitchChange,
    handleAutoDeinterlaceChange,
    handlePictureEnhancementChange,
    supportsSeamlessSwitch,
    supportsDocumentPictureInPicture,
    supportsMSEVideoProcessing,
  ]);

  const hasPlaylistLoadError = Boolean(error && !metadata);
  if (!hasPlaylistLoadError) {
    return (
      <div
        ref={pageContainerRef}
        className="player-performance-page-background player-performance-scope player-viewport-height relative flex flex-col bg-[radial-gradient(circle_at_92%_8%,rgba(139,92,246,0.15),transparent_28%),radial-gradient(circle_at_72%_92%,rgba(217,70,239,0.13),transparent_32%),linear-gradient(145deg,#fbfaff,#f1edff)] dark:bg-[radial-gradient(circle_at_88%_10%,rgba(139,92,246,0.1),transparent_30%),radial-gradient(circle_at_70%_88%,rgba(217,70,239,0.12),transparent_34%),linear-gradient(145deg,#070516,#0d0a26)]"
      >
        <title>{t("title")}</title>

        {/* Main Content */}
        <div className="flex flex-col md:flex-row flex-1 overflow-hidden">
          {/* Video Player - Mobile: fixed aspect ratio at top, Desktop: fills left side */}
          <div className="w-full sticky md:static md:flex-1 shrink-0">
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
                isFullscreen={isFullscreen}
                onFullscreenToggle={handleFullscreenToggle}
                seamlessSwitch={supportsSeamlessSwitch && seamlessSwitch}
                autoDeinterlace={autoDeinterlace}
                pictureEnhancement={pictureEnhancement}
                pictureInPictureMode={pictureInPictureMode}
                activeSourceIndex={activeSourceIndex}
                onSourceChange={handleSourceChange}
                onPlaybackStarted={handlePlaybackStarted}
              />
            </PlaybackTimeProvider>
          </div>

          {/* Sidebar - Mobile: always visible (below video, hidden in fullscreen), Desktop: toggle-able side panel (visible in fullscreen) */}
          <div
            className={clsx(
              "player-performance-panel-background flex w-full flex-1 flex-col overflow-hidden border-violet-950/10 border-t bg-white/68 pl-[env(safe-area-inset-left)] shadow-[-14px_0_40px_rgba(91,33,182,0.06)] backdrop-blur-2xl dark:border-violet-100/10 dark:bg-[linear-gradient(160deg,rgba(10,7,26,0.96),rgba(23,16,53,0.92))] dark:shadow-[-18px_0_48px_rgba(9,4,26,0.28)] md:w-[21rem] lg:w-[22rem] md:flex-initial md:border-t-0 md:border-l md:pt-[env(safe-area-inset-top)] md:pl-0",
              insetSidebarRight && "pr-[env(safe-area-inset-right)]",
              (showSidebar || isMobile) && !(isFullscreen && isMobile) ? "" : "hidden",
            )}
          >
            {/* Sidebar Tabs */}
            <div className="player-performance-panel-background flex shrink-0 items-center border-violet-950/10 border-b bg-white/44 shadow-[0_8px_24px_rgba(91,33,182,0.045)] backdrop-blur-xl dark:border-violet-100/10 dark:bg-[linear-gradient(90deg,#1b1533,#2b2149)]">
              {(["channels", "epg"] as const).map((view) => (
                <button
                  type="button"
                  key={view}
                  onClick={() => handleSidebarViewChange(view)}
                  className={clsx(
                    "player-performance-motion min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap border-b-2 px-3 py-2 text-center font-semibold text-xs leading-5 tracking-[0.01em] transition-[color,background-color,border-color,box-shadow] md:px-4 md:py-3 md:text-sm",
                    selectedSidebarView === view
                      ? "border-violet-500 bg-[linear-gradient(to_top,rgba(139,92,246,0.12),transparent)] text-violet-700 shadow-[inset_0_-1px_0_rgba(139,92,246,0.18)] dark:border-violet-300 dark:text-violet-200"
                      : "cursor-pointer border-transparent text-slate-500 hover:bg-violet-400/5 hover:text-violet-700 dark:text-slate-400 dark:hover:text-violet-100",
                  )}
                >
                  {view === "channels" ? `${t("channels")} (${metadata?.channels.length || 0})` : t("programGuide")}
                </button>
              ))}
            </div>

            {/* Sidebar Content */}
            <div className="flex-1 overflow-hidden">
              <Activity mode={renderedSidebarView === "channels" ? "visible" : "hidden"}>
                <ChannelList
                  channels={metadata?.channels}
                  groups={metadata?.groups}
                  currentChannel={currentChannel}
                  onChannelSelect={selectChannel}
                  locale={locale}
                  settingsSlot={settingsSlot}
                  epgData={epgData}
                />
              </Activity>
              <Activity mode={renderedSidebarView === "epg" ? "visible" : "hidden"}>
                <EPGView
                  channelId={currentChannel ? getEPGChannelId(currentChannel, epgData) : null}
                  epgData={epgData}
                  onProgramSelect={handleProgramSelect}
                  locale={locale}
                  supportsCatchup={!!currentChannel?.sources.some((s) => s.catchup && s.catchupSource)}
                  currentPlayingProgram={currentVideoProgram}
                />
              </Activity>
            </div>
          </div>
        </div>

        {/* Loading overlay shares the player viewport to avoid iOS standalone fixed-position gaps. */}
        {isLoading && (
          <div
            className={clsx(
              "player-performance-page-background player-performance-motion absolute inset-0 z-50 flex items-center justify-center bg-[radial-gradient(circle_at_center,rgba(139,92,246,0.16),transparent_28%),radial-gradient(circle_at_65%_60%,rgba(217,70,239,0.14),transparent_35%),linear-gradient(145deg,#fbfaff,#f1edff)] pt-[max(1rem,env(safe-area-inset-top))] pr-[max(1rem,env(safe-area-inset-right))] pb-[max(1rem,env(safe-area-inset-bottom))] pl-[max(1rem,env(safe-area-inset-left))] dark:bg-[radial-gradient(circle_at_center,rgba(139,92,246,0.11),transparent_30%),radial-gradient(circle_at_65%_60%,rgba(217,70,239,0.12),transparent_38%),linear-gradient(145deg,#070516,#0d0a26)]",
              isRevealing && "animate-zoom-fade-out",
            )}
          >
            <div className="text-center space-y-4">
              {/* Loading spinner */}
              <div className="player-performance-loading-spinner mx-auto h-12 w-12 animate-spin rounded-full border-4 border-violet-950/10 border-t-violet-500 border-r-fuchsia-500 shadow-[0_0_28px_rgba(139,92,246,0.22)] dark:border-violet-100/10 dark:border-t-violet-300 dark:border-r-fuchsia-400" />
            </div>
          </div>
        )}
      </div>
    );
  }

  const playlistErrorHints = [t("playlistErrorHintReachable"), t("playlistErrorHintFormat")];
  const errorMessage = error ? t(error) : null;

  return (
    <div className="player-performance-page-background player-performance-scope player-viewport-height overflow-y-auto bg-[radial-gradient(circle_at_18%_14%,rgba(139,92,246,0.16),transparent_28%),radial-gradient(circle_at_84%_82%,rgba(217,70,239,0.16),transparent_32%),linear-gradient(145deg,#fbfaff,#f1edff)] dark:bg-[radial-gradient(circle_at_18%_14%,rgba(139,92,246,0.1),transparent_30%),radial-gradient(circle_at_84%_82%,rgba(217,70,239,0.13),transparent_34%),linear-gradient(145deg,#070516,#0d0a26)]">
      <title>{t("title")}</title>
      <div className="mx-auto flex min-h-full w-[calc(100%-2rem)] max-w-5xl items-center py-8 sm:w-[calc(100%-3rem)]">
        <Card className="player-performance-panel-background min-w-0 w-full overflow-hidden rounded-3xl border-violet-900/10 bg-white/72 shadow-[0_28px_80px_rgba(91,33,182,0.16),inset_0_1px_0_rgba(255,255,255,0.85)] backdrop-blur-2xl dark:border-violet-100/12 dark:bg-[linear-gradient(145deg,rgba(16,10,40,0.9),rgba(32,22,84,0.82))] dark:shadow-[0_30px_90px_rgba(9,4,26,0.62),inset_0_1px_0_rgba(255,255,255,0.08)]">
          <div className="grid min-w-0 md:grid-cols-[minmax(0,1fr)_18rem]">
            <div className="min-w-0 p-6 sm:p-8 md:p-10">
              <div className="mb-5 flex h-12 w-12 items-center justify-center rounded-2xl border border-rose-300/20 bg-[linear-gradient(145deg,rgba(251,113,133,0.16),rgba(217,70,239,0.12))] text-rose-500 shadow-[0_12px_28px_rgba(225,29,72,0.12)] dark:text-rose-300">
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
                        className="mt-2 h-1.5 w-1.5 shrink-0 rounded-full bg-violet-500 shadow-[0_0_8px_rgba(139,92,246,0.45)]"
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
                  className="w-full gap-2 rounded-xl border-primary/20 bg-violet-700 bg-[linear-gradient(135deg,#0e7490,#4338ca)] text-white shadow-[0_10px_28px_rgba(124,58,237,0.24)] transition-[color,background-color,border-color] hover:border-primary/30 hover:bg-violet-700 hover:bg-[linear-gradient(135deg,#0e7490,#4338ca)] hover:text-white sm:w-auto"
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

            <div className="min-w-0 border-violet-900/10 border-t bg-[linear-gradient(145deg,rgba(224,242,254,0.42),rgba(238,242,255,0.58))] p-6 dark:border-violet-100/10 dark:bg-[linear-gradient(145deg,rgba(38,16,78,0.22),rgba(40,18,92,0.3))] md:border-t-0 md:border-l md:p-8">
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

// Mount the app
createRoot(document.getElementById("root") as HTMLElement).render(
  <StrictMode>
    <PlayerPage />
  </StrictMode>,
);
