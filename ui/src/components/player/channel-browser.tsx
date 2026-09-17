/**
 * 三栏频道浏览器（clean-room 重写）。
 * 左：分组；中：频道列表；右：预览频道回看（EPG）。遥控器方向键在栏内移动并预览，
 * 不切换播放（仅显式 Enter/点击才换台）。栏内按键通过 stopPropagation 屏蔽 video-player 的全局绑定。
 */
import { ChevronsLeft, ChevronsRight, History, Layers, Tv } from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import { type EPGData, getCurrentProgram, getEPGChannelId } from "../../lib/epg-parser";
import type { Locale } from "../../lib/locale";
import { getSelectedGroup, saveSelectedGroup } from "../../lib/player-storage";
import type { Channel, EPGProgram } from "../../types/player";
import {
  PLAYER_LIST_SURFACE_BASE_CLASS,
  PLAYER_LIST_SURFACE_DEFAULT_CLASS,
  PLAYER_LIST_SURFACE_HOVER_CLASS,
  PLAYER_LIST_SURFACE_SELECTED_CLASS,
} from "./classnames";
import { ChannelListItem } from "./channel-list-item";
import { EPGView } from "./epg-view";

type Pane = "categories" | "channels" | "replay" | "epgToggle";

interface ChannelBrowserProps {
  channels: Channel[];
  groups: string[];
  currentChannel: Channel | null;
  previewChannel: Channel | null;
  onPreviewChannelChange: (channel: Channel) => void;
  onChannelSelect: (channel: Channel) => void;
  onProgramSelect: (programStart: Date, programEnd: Date) => void;
  locale: Locale;
  settingsSlot?: ReactNode;
  epgData: EPGData;
  currentPlayingProgram: EPGProgram | null;
  supportsCatchup: boolean;
  /** 节目单栏开合状态上报（父层据此调浮层宽度：两列窄 / 三列宽）。 */
  onEpgOpenChange?: (open: boolean) => void;
  /** 可见频道变化上报：父层据此预取 EPG，让节目单栏与列表行有真实节目（而非只剩占位）。 */
  onVisibleChannelsChange?: (channels: Channel[]) => void;
  /** 后端是否配置了 EPG 源（false 时节目单栏直接提示，而不是一片"精彩节目"占位）。 */
  epgConfigured?: boolean;
}

function filterChannels(channels: Channel[], selectedGroup: string | null) {
  if (!channels) return [];
  if (!selectedGroup) return channels;
  return channels.filter((channel) => channel.groups.includes(selectedGroup));
}

function focusFirstIn(pane: HTMLElement | null, matchId?: string) {
  if (!pane) return;
  const buttons = Array.from(pane.querySelectorAll<HTMLButtonElement>("button"));
  if (!buttons.length) return;
  const match = matchId ? buttons.find((button) => button.dataset.id === matchId) : undefined;
  (match ?? buttons[0]).focus();
}

function Search({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="2" />
      <path d="m20 20-3.5-3.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  );
}

const ChannelBrowserComponent = function ChannelBrowserComponent({
  channels,
  groups,
  currentChannel,
  previewChannel,
  onPreviewChannelChange,
  onChannelSelect,
  onProgramSelect,
  locale,
  settingsSlot,
  epgData,
  currentPlayingProgram,
  supportsCatchup,
  onEpgOpenChange,
  onVisibleChannelsChange,
  epgConfigured,
}: ChannelBrowserProps) {
  const t = usePlayerTranslation(locale);
  // 选中的分组跨会话记忆：初始化只读记忆值，**不在这里校验组是否存在**——
  // 首屏 groups 还是空数组（频道元数据未到），校验会把有效选择误清成"全部"。
  const [selectedGroup, setSelectedGroup] = useState<string | null>(() => getSelectedGroup());
  const [now, setNow] = useState(() => new Date());

  const categoriesPaneRef = useRef<HTMLDivElement>(null);
  const channelsPaneRef = useRef<HTMLDivElement>(null);
  const replayPaneRef = useRef<HTMLDivElement>(null);

  // 节目单栏**默认收起且不持久化**（不记忆上次状态，避免"自己弹出来"）：
  // 常态只有"分组 + 频道"两列；点击竖排把手 / 点频道行内节目 / 遥控器向右才弹出；
  // 选中节目后自动收起（见 handleEpgProgramSelect）。
  const [epgOpen, setEpgOpen] = useState(false);
  const pendingEpgFocusRef = useRef(false);

  /** 从频道列表（或竖排把手）进入节目单：先确保展开，再聚焦到节目项。 */
  const openEpgAndFocus = useCallback(() => {
    if (!epgOpen) {
      pendingEpgFocusRef.current = true;
      setEpgOpen(true);
      return;
    }
    focusFirstIn(replayPaneRef.current);
  }, [epgOpen]);

  useEffect(() => {
    if (!epgOpen || !pendingEpgFocusRef.current) return;
    pendingEpgFocusRef.current = false;
    focusFirstIn(replayPaneRef.current);
  }, [epgOpen]);

  useEffect(() => {
    onEpgOpenChange?.(epgOpen);
  }, [epgOpen, onEpgOpenChange]);

  /**
   * 分组列表就绪后校验记忆值：**列表为空时什么都不做**（元数据未到 / 订阅为空），
   * 否则会把有效记忆误清。组确实不存在时本次回落"全部"，但**保留**记忆值——
   * 订阅短暂缺组（刷新、换源）不该毁掉用户偏好；用户显式选"全部"时才清除记忆。
   */
  useEffect(() => {
    if (!selectedGroup || groups.length === 0) return;
    if (!groups.includes(selectedGroup)) setSelectedGroup(null);
  }, [groups, selectedGroup]);

  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(timer);
  }, []);

  const filteredChannels = useMemo(() => filterChannels(channels, selectedGroup), [channels, selectedGroup]);

  /** 可见频道（分组/搜索/列表刷新）去抖上报，父层据此预取 EPG。 */
  useEffect(() => {
    if (!onVisibleChannelsChange) return;
    const timer = window.setTimeout(() => onVisibleChannelsChange(filteredChannels), 400);
    return () => window.clearTimeout(timer);
  }, [filteredChannels, onVisibleChannelsChange]);

  const previewEpgId = useMemo(
    () => (previewChannel ? getEPGChannelId(previewChannel, epgData) : null),
    [previewChannel, epgData],
  );

  const previewCurrentProgram = useMemo(() => {
    if (!previewChannel || !previewEpgId) return null;
    return getCurrentProgram(previewEpgId, epgData, now);
  }, [previewChannel, previewEpgId, epgData, now]);

  /** 选中分组（点击/遥控器 OK）：立刻生效并写入跨会话记忆。 */
  const handleGroupSelect = useCallback((group: string | null) => {
    setSelectedGroup(group);
    saveSelectedGroup(group);
  }, []);

  const handleChannelActivate = useCallback(
    (channel: Channel) => {
      onChannelSelect(channel);
      if (channel.id !== previewChannel?.id) onPreviewChannelChange(channel);
    },
    [onChannelSelect, onPreviewChannelChange, previewChannel],
  );

  /** 选中节目（回看 / 回直播）：交给上层切流，并**自动收起**节目单栏（选完即隐藏）。 */
  const handleEpgProgramSelect = useCallback(
    (programStart: Date, programEnd: Date) => {
      onProgramSelect(programStart, programEnd);
      setEpgOpen(false);
    },
    [onProgramSelect],
  );

  /** 点击频道行内的"当前节目"：预览该频道并展开节目单栏（不换台）。 */
  const handleProgramOpen = useCallback(
    (channel: Channel) => {
      if (channel.id !== previewChannel?.id) onPreviewChannelChange(channel);
      if (!epgOpen) setEpgOpen(true);
    },
    [epgOpen, onPreviewChannelChange, previewChannel],
  );

  const buttonsIn = (paneEl: HTMLElement | null) =>
    Array.from(paneEl?.querySelectorAll<HTMLButtonElement>("button") ?? []);

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLDivElement>) => {
      const activeEl = document.activeElement as HTMLElement | null;
      if (!activeEl) return;
      if (activeEl.tagName === "INPUT" || activeEl.tagName === "TEXTAREA") return;

      const activePane = activeEl.closest<HTMLElement>("[data-pane]");
      const pane = activePane?.dataset.pane as Pane | undefined;
      if (!pane) return;

      const shouldSuppress =
        event.key === "ArrowUp" ||
        event.key === "ArrowDown" ||
        event.key === "ArrowLeft" ||
        event.key === "ArrowRight" ||
        event.key === "Enter" ||
        event.key === "Escape" ||
        event.key === " " ||
        /^[0-9]$/.test(event.key);
      if (shouldSuppress) event.stopPropagation();
      if (!["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Enter", "Escape"].includes(event.key)) return;
      event.preventDefault();

      const moveWithinPane = (offset: number) => {
        if (!activePane) return;
        const buttons = buttonsIn(activePane);
        const idx = buttons.indexOf(activeEl as HTMLButtonElement);
        const base = idx < 0 ? 0 : idx;
        const next = Math.max(0, Math.min(base + offset, buttons.length - 1));
        const button = buttons[next];
        if (!button) return;
        button.focus();
        if (pane === "channels") {
          const id = button.dataset?.id;
          const channel = filteredChannels.find((entry) => entry.id === id);
          if (channel && channel.id !== previewChannel?.id) onPreviewChannelChange(channel);
        }
      };

      if (event.key === "ArrowDown") {
        moveWithinPane(1);
      } else if (event.key === "ArrowUp") {
        moveWithinPane(-1);
      } else if (event.key === "ArrowRight") {
        if (pane === "categories") {
          focusFirstIn(channelsPaneRef.current);
          const first = filteredChannels[0];
          if (first && first.id !== previewChannel?.id) onPreviewChannelChange(first);
        } else if (pane === "channels") {
          const idx = buttonsIn(activePane).indexOf(activeEl as HTMLButtonElement);
          const channel = filteredChannels[idx] ?? filteredChannels[0];
          if (channel) {
            if (channel.id !== previewChannel?.id) onPreviewChannelChange(channel);
            openEpgAndFocus();
          }
        } else if (pane === "epgToggle") {
          openEpgAndFocus();
        }
      } else if (event.key === "ArrowLeft") {
        if (pane === "replay" || pane === "epgToggle") {
          // 遥控器向左 = 关闭节目单栏（收起第三列）并回到频道列表
          setEpgOpen(false);
          focusFirstIn(channelsPaneRef.current, previewChannel?.id);
        } else if (pane === "channels") {
          focusFirstIn(categoriesPaneRef.current, selectedGroup ?? undefined);
        }
      } else if (event.key === "Escape") {
        if (pane === "replay") {
          setEpgOpen(false);
          focusFirstIn(channelsPaneRef.current, previewChannel?.id);
        } else {
          activeEl.blur();
        }
      }
    },
    [filteredChannels, onPreviewChannelChange, openEpgAndFocus, previewChannel, selectedGroup],
  );

  const paneClass =
    "flex min-h-0 flex-1 flex-col overflow-hidden border-violet-950/10 border-r last:border-r-0 dark:border-violet-100/10";

  return (
    <div className="flex h-full w-full flex-col bg-transparent" onKeyDown={handleKeyDown}>
      <div className="flex shrink-0 items-center gap-2 px-2 pt-2 pb-0">
        <SearchBox channels={channels} selectedGroup={selectedGroup} onChannelSelect={handleChannelActivate} />
        {settingsSlot && <div className="shrink-0">{settingsSlot}</div>}
      </div>

      <div className="flex min-h-0 flex-1">
        <div ref={categoriesPaneRef} data-pane="categories" className={[paneClass, "w-[28%] max-w-44"].join(" ")}>
          <PaneHeader icon={<Layers className="h-3.5 w-3.5 shrink-0 text-violet-500 dark:text-violet-300" />} label={t("channelGroups")} />
          <div className="flex-1 overflow-y-auto py-1">
            <div className="flex flex-col">
              {[null, ...groups].map((group) => (
                <button
                  type="button"
                  key={group ?? "all"}
                  data-id={group ?? "__all__"}
                  onClick={() => handleGroupSelect(group)}
                  onFocus={() => {
                    if ((group ?? null) !== selectedGroup) setSelectedGroup(group ?? null);
                  }}
                  title={group ?? t("allChannels")}
                  className={[
                    PLAYER_LIST_SURFACE_BASE_CLASS,
                    "flex h-8 min-w-0 touch-manipulation items-center overflow-hidden text-ellipsis whitespace-nowrap px-3 text-xs font-medium leading-none",
                    (group ?? null) === selectedGroup ? PLAYER_LIST_SURFACE_SELECTED_CLASS : PLAYER_LIST_SURFACE_DEFAULT_CLASS,
                    (group ?? null) !== selectedGroup && PLAYER_LIST_SURFACE_HOVER_CLASS,
                  ].join(" ")}
                >
                  {group ?? t("allChannels")}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div ref={channelsPaneRef} data-pane="channels" className={[paneClass, "flex-1"].join(" ")}>
          <PaneHeader
            icon={<Tv className="h-3.5 w-3.5 shrink-0 text-violet-500 dark:text-violet-300" />}
            label={`${t("channels")}${filteredChannels.length ? ` (${filteredChannels.length})` : ""}`}
          />
          <div className="flex-1 overflow-y-auto py-1">
            <div className="flex flex-col">
              {filteredChannels.map((channel) => (
                <ChannelListItem
                  key={channel.id}
                  channel={channel}
                  isCurrentChannel={channel.id === currentChannel?.id}
                  handleChannelClick={handleChannelActivate}
                  onProgramClick={handleProgramOpen}
                  locale={locale}
                  currentProgram={
                    previewChannel?.id === channel.id
                      ? previewCurrentProgram?.title || (previewCurrentProgram ? t("excellentProgram") : undefined)
                      : undefined
                  }
                />
              ))}
            </div>
          </div>
        </div>

        {epgOpen && (
          <div ref={replayPaneRef} data-pane="replay" className={[paneClass, "w-[42%] max-w-[20rem]"].join(" ")}>
            <PaneHeader
              icon={<History className="h-3.5 w-3.5 shrink-0 text-violet-500 dark:text-violet-300" />}
              label={`${t("catchup")}${previewChannel ? ` · ${previewChannel.name}` : ""}`}
            />
            {epgConfigured === false && (
              <div className="shrink-0 px-2.5 py-1 text-[10px] leading-4 text-amber-600 dark:text-amber-300/90">
                {t("epgNotConfigured")}
              </div>
            )}
            <div className="min-h-0 flex-1">
              <EPGView
                channelId={previewEpgId}
                epgData={epgData}
                onProgramSelect={handleEpgProgramSelect}
                locale={locale}
                supportsCatchup={supportsCatchup}
                currentPlayingProgram={previewChannel?.id === currentChannel?.id ? currentPlayingProgram : previewCurrentProgram}
              />
            </div>
          </div>
        )}

        {/* 竖排"节目单"把手：展开时位于浮层右缘（照截图），收起后留在同位可再次展开 */}
        <button
          type="button"
          data-pane="epgToggle"
          data-id="__epg_toggle__"
          aria-expanded={epgOpen}
          aria-label={t("programGuide")}
          title={t("programGuide")}
          onClick={() => setEpgOpen(!epgOpen)}
          className="player-performance-motion flex w-5 shrink-0 cursor-pointer touch-manipulation flex-col items-center justify-center gap-1.5 border-violet-950/10 border-l text-slate-500 transition-colors hover:bg-violet-400/10 hover:text-violet-700 focus-visible:outline-none dark:border-violet-100/10 dark:text-slate-400 dark:hover:bg-violet-300/10 dark:hover:text-violet-100"
        >
          <span className="font-semibold text-[10px] leading-none tracking-[0.18em] [writing-mode:vertical-rl]">
            {t("programGuide")}
          </span>
          {/* 收起状态显示 ">>"（点击/遥控器向右展开），展开状态显示 "<<"（向左收起） */}
          {epgOpen ? (
            <ChevronsLeft className="h-3 w-3 shrink-0" aria-hidden="true" />
          ) : (
            <ChevronsRight className="h-3 w-3 shrink-0" aria-hidden="true" />
          )}
        </button>
      </div>
    </div>
  );
};

function PaneHeader({ icon, label }: { icon: ReactNode; label: string }) {
  return (
    <div className="flex h-8 shrink-0 items-center gap-1.5 border-violet-950/10 border-b px-3 dark:border-violet-100/10">
      {icon}
      <span className="min-w-0 truncate text-[11px] font-semibold tracking-[0.02em] text-slate-500 dark:text-slate-400">
        {label}
      </span>
    </div>
  );
}

const SearchBox = memo(function SearchBox({
  channels,
  selectedGroup,
  onChannelSelect,
}: {
  channels: Channel[];
  selectedGroup: string | null;
  onChannelSelect: (channel: Channel) => void;
}) {
  const [query, setQuery] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    return channels
      .filter((channel) => !selectedGroup || channel.groups.includes(selectedGroup))
      .filter(
        (channel) =>
          channel.name.toLowerCase().includes(q) ||
          channel.id.includes(query.trim()) ||
          // 频道行显示的是频道号（订阅序位），按号搜索要能命中
          String(channel.number ?? "").startsWith(query.trim()),
      )
      .slice(0, 8);
  }, [channels, query, selectedGroup]);

  return (
    <div className="relative min-w-0 flex-1">
      <input
        ref={inputRef}
        type="text"
        placeholder="搜索频道..."
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && results.length > 0) {
            onChannelSelect(results[0]);
            setQuery("");
            (document.activeElement as HTMLElement)?.blur();
          } else if (event.key === "Escape") {
            setQuery("");
            (document.activeElement as HTMLElement)?.blur();
          }
        }}
        className="player-performance-input-background player-performance-motion h-8 w-full rounded-md border border-violet-900/10 bg-white/45 px-3 py-0 pl-8 text-slate-800 text-xs shadow-none transition placeholder:text-slate-400 focus:border-violet-400/50 focus:bg-white/70 focus:outline-none dark:border-violet-100/10 dark:bg-white/6 dark:text-violet-50 dark:placeholder:text-slate-500 dark:focus:bg-white/10"
      />
      <Search className="absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-violet-600/65 dark:text-violet-300/55" />
      {results.length > 0 && (
        <div className="player-performance-overlay-background absolute top-full left-0 z-30 mt-1 max-h-64 w-full overflow-y-auto rounded-md border border-violet-200/40 bg-white/90 p-1 shadow-[0_12px_28px_-16px_rgba(0,0,0,0.55)] backdrop-blur-lg dark:border-violet-100/10 dark:bg-slate-950/88">
          {results.map((channel) => (
            <button
              type="button"
              key={channel.id}
              onClick={() => {
                onChannelSelect(channel);
                setQuery("");
                (document.activeElement as HTMLElement)?.blur();
              }}
              className="block w-full truncate rounded-lg px-2 py-1.5 text-left text-xs text-slate-700 hover:bg-violet-50/70 dark:text-slate-200 dark:hover:bg-violet-300/10"
            >
              {channel.name}
            </button>
          ))}
        </div>
      )}
    </div>
  );
});

export const ChannelBrowser = memo(ChannelBrowserComponent);
