/**
 * 三栏频道浏览面板（TV 播放器桌面浮层）。
 * 左：分组；中：频道列表；右：节目单（回看）。遥控器方向键在栏内移动焦点并预览频道，
 * 但不换台——只有显式 Enter / 点击才切换播放。栏内按键一律 stopPropagation，
 * 防止被 video-player 的全局键位接管。
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

/** 栏位标记（写在 DOM 的 data-pane 上），键盘导航据此判断焦点落在哪一栏。 */
type PaneName = "categories" | "channels" | "replay" | "epgToggle";

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
  /** 节目单栏开/关上报：父层据此调整浮层宽度（两列窄 / 三列宽）。 */
  onEpgOpenChange?: (open: boolean) => void;
  /** 可见频道变化上报（去抖后）：父层据此预取这些频道的 EPG。 */
  onVisibleChannelsChange?: (channels: Channel[]) => void;
  /** 后端是否配置了 EPG 源：false 时节目单栏直接提示，而不是一栏"精彩节目"占位。 */
  epgConfigured?: boolean;
  /** 浮层（侧栏）当前是否可见：再次打开时把分组拉回"正在播放的频道"所在分组。 */
  panelVisible?: boolean;
}

/** 按分组收窄频道。未选分组时原样返回同一数组引用，避免无谓拷贝。 */
function narrowByGroup(channels: Channel[], selectedGroup: string | null) {
  if (!channels) return [];
  if (!selectedGroup) return channels;
  return channels.filter((channel) => channel.groups.includes(selectedGroup));
}

/** 把焦点落到栏内首个（或指定 data-id 的）按钮；指定项找不到时退回首按钮兜底。 */
function focusPaneButton(paneEl: HTMLElement | null, preferredId?: string) {
  if (!paneEl) return;
  const buttons = Array.from(paneEl.querySelectorAll<HTMLButtonElement>("button"));
  if (!buttons.length) return;
  const preferred = preferredId ? buttons.find((button) => button.dataset.id === preferredId) : undefined;
  (preferred ?? buttons[0]).focus();
}

/** 收集某一栏里的全部按钮，供方向键在栏内步进。 */
function collectPaneButtons(paneEl: HTMLElement | null) {
  return Array.from(paneEl?.querySelectorAll<HTMLButtonElement>("button") ?? []);
}

/** 搜索框内的放大镜图形：内联 SVG，避免为一个小图标增加图标组件依赖。 */
function MagnifierGlyph({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="2" />
      <path d="m20 20-3.5-3.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  );
}

/** 三栏共用的纵向骨架类；各栏只在此基础上附加自己的宽度类。 */
const PANE_FRAME_CLASS =
  "flex min-h-0 flex-1 flex-col overflow-hidden border-violet-950/10 border-r last:border-r-0 dark:border-violet-100/10";

/** 组装栏位类名：骨架 + 宽度（保持与原 join 顺序一致的最终串）。 */
function paneClassName(widthClass: string) {
  return `${PANE_FRAME_CLASS} ${widthClass}`;
}

/** 分组按钮的内容排布类；选中 / 悬停态由 PLAYER_LIST_SURFACE_* 常量叠加。 */
const GROUP_ROW_CLASS =
  "flex h-8 min-w-0 touch-manipulation items-center overflow-hidden text-ellipsis whitespace-nowrap px-3 text-xs font-medium leading-none";

/** 需要吞掉（stopPropagation）的按键：方向 / 确认 / 返回 / 空格，避免漏给播放器全局键位。 */
const SWALLOWED_KEYS = ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Enter", "Escape", " "];
/** 面板真正响应的按键；空格与数字只吞不处理（数字键留给全局换台）。 */
const NAVIGATED_KEYS = ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Enter", "Escape"];

/** 栏位顶部的图标 + 标题条。 */
function PaneTitleBar({ icon, label }: { icon: ReactNode; label: string }) {
  return (
    <div className="flex h-8 shrink-0 items-center gap-1.5 border-violet-950/10 border-b px-3 dark:border-violet-100/10">
      {icon}
      <span className="min-w-0 truncate text-[11px] font-semibold tracking-[0.02em] text-slate-700 dark:text-slate-200">
        {label}
      </span>
    </div>
  );
}

function ThreePaneChannelBrowser({
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
  panelVisible,
}: ChannelBrowserProps) {
  const t = usePlayerTranslation(locale);
  // 分组选择跨会话记忆：初始化只读记忆值，**不在这里校验组是否存在**——
  // 首屏 groups 还是空数组（频道元数据未到），过早校验会把有效选择误清成"全部"。
  const [activeGroup, setActiveGroup] = useState<string | null>(() => getSelectedGroup());
  // "现在"每分钟走一格，驱动节目单高亮当前时段。
  const [currentMoment, setCurrentMoment] = useState(() => new Date());

  const groupPaneRef = useRef<HTMLDivElement>(null);
  const channelPaneRef = useRef<HTMLDivElement>(null);
  const schedulePaneRef = useRef<HTMLDivElement>(null);

  // 节目单栏**默认收起且不持久化**（不记忆上次状态，避免"自己弹出来"）：
  // 常态只有"分组 + 频道"两列；点竖排把手 / 点行内节目 / 遥控器向右才展开；
  // 选中节目后自动收起（见 handleScheduleProgramPicked）。
  const [scheduleOpen, setScheduleOpen] = useState(false);
  // 栏位还没挂载时先记下"要聚焦节目单"的意图，等展开后的 effect 再补聚焦。
  const scheduleFocusPendingRef = useRef(false);

  /** 从频道列表（或竖排把手）进入节目单：未展开就先展开并把聚焦意图留给下一轮 effect。 */
  const revealSchedulePane = useCallback(() => {
    if (!scheduleOpen) {
      scheduleFocusPendingRef.current = true;
      setScheduleOpen(true);
      return;
    }
    focusPaneButton(schedulePaneRef.current);
  }, [scheduleOpen]);

  // 展开完成后的补聚焦：此刻右栏的节目按钮才真实存在于 DOM。
  useEffect(() => {
    if (!scheduleOpen || !scheduleFocusPendingRef.current) return;
    scheduleFocusPendingRef.current = false;
    focusPaneButton(schedulePaneRef.current);
  }, [scheduleOpen]);

  // 开合状态上报父层（父层据此调浮层宽度：两列窄 / 三列宽）。
  useEffect(() => {
    onEpgOpenChange?.(scheduleOpen);
  }, [scheduleOpen, onEpgOpenChange]);

  /**
   * 分组列表就绪后校验记忆值：**列表为空时什么都不做**（元数据未到 / 订阅为空），
   * 否则会把有效记忆误清。组确实不存在时本次回落"全部"，但**保留**记忆值——
   * 订阅短暂缺组（刷新、换源）不该毁掉用户偏好；用户显式选"全部"时才清除记忆。
   */
  useEffect(() => {
    if (!activeGroup || groups.length === 0) return;
    if (!groups.includes(activeGroup)) setActiveGroup(null);
  }, [groups, activeGroup]);

  useEffect(() => {
    const timer = setInterval(() => setCurrentMoment(new Date()), 60_000);
    return () => clearInterval(timer);
  }, []);

  const visibleChannels = useMemo(() => narrowByGroup(channels, activeGroup), [channels, activeGroup]);

  // 可见频道去抖上报（分组 / 列表刷新都会触发）：父层据此预取 EPG，让节目单栏与列表行有真实节目。
  useEffect(() => {
    if (!onVisibleChannelsChange) return;
    const timer = window.setTimeout(() => onVisibleChannelsChange(visibleChannels), 400);
    return () => window.clearTimeout(timer);
  }, [visibleChannels, onVisibleChannelsChange]);

  const previewEpgChannelId = useMemo(
    () => (previewChannel ? getEPGChannelId(previewChannel, epgData) : null),
    [previewChannel, epgData],
  );

  const previewNowPlaying = useMemo(() => {
    if (!previewChannel || !previewEpgChannelId) return null;
    return getCurrentProgram(previewEpgChannelId, epgData, currentMoment);
  }, [previewChannel, previewEpgChannelId, epgData, currentMoment]);

  /**
   * 选中分组（点击/遥控器 OK）：立刻生效，但**不写记忆**——记忆以"正在播放的频道所在分组"为准，
   * 否则"只点过分组、没选台"会把列表钉在那个分组（用户实测反馈：再打开时没跳到播放台所在组）。
   */
  const applyGroupFilter = useCallback((group: string | null) => {
    setActiveGroup(group);
  }, []);

  /**
   * 换台（含首次进入）后分组跟随**正在播放的频道**；只认 currentChannel 变化，
   * 所以上一步点分组不会被立刻弹回去（列表键位/焦点也不会被抢）。
   */
  const alignGroupWithPlayingChannel = useCallback(() => {
    if (!currentChannel) return;
    const group = currentChannel.groups.find((g) => groups.includes(g)) ?? currentChannel.groups[0] ?? null;
    if (!group) return;
    setActiveGroup(group);
    saveSelectedGroup(group);
  }, [currentChannel, groups]);

  // 已同步过分组的频道 id：同一频道不重复触发，避免覆盖用户刚点的分组。
  const groupSyncedChannelIdRef = useRef<string | null>(null);
  useEffect(() => {
    if (!currentChannel) return;
    if (groupSyncedChannelIdRef.current === currentChannel.id) return;
    groupSyncedChannelIdRef.current = currentChannel.id;
    alignGroupWithPlayingChannel();
  }, [currentChannel, alignGroupWithPlayingChannel]);

  /**
   * 再次打开浮层（侧栏）时同样拉回正在播放的频道所在分组：在浮层里点分组浏览、没选台就
   * 收起来，再打开时应看到"正在播的台"所在分组，而不是上次点过的分组（用户实测反馈：
   * 百视通 CCTV4K 在播，收起再打开却停在蜀小果）。
   */
  const prevPanelVisibleRef = useRef(false);
  useEffect(() => {
    const visible = Boolean(panelVisible);
    const justReopened = visible && !prevPanelVisibleRef.current;
    prevPanelVisibleRef.current = visible;
    if (justReopened) alignGroupWithPlayingChannel();
  }, [panelVisible, alignGroupWithPlayingChannel]);

  // 选台 = 换流 + 把预览同步过去；预览本来就是它就跳过，省一次 state 抖动。
  const handleChannelPicked = useCallback(
    (channel: Channel) => {
      onChannelSelect(channel);
      if (channel.id !== previewChannel?.id) onPreviewChannelChange(channel);
    },
    [onChannelSelect, onPreviewChannelChange, previewChannel],
  );

  /** 选中节目（回看 / 回直播）：交给上层切流，并**自动收起**节目单栏（选完即隐藏）。 */
  const handleScheduleProgramPicked = useCallback(
    (programStart: Date, programEnd: Date) => {
      onProgramSelect(programStart, programEnd);
      setScheduleOpen(false);
    },
    [onProgramSelect],
  );

  /** 点击频道行内的"当前节目"：预览该频道并展开节目单栏（不换台）。 */
  const handleInlineProgramClick = useCallback(
    (channel: Channel) => {
      if (channel.id !== previewChannel?.id) onPreviewChannelChange(channel);
      if (!scheduleOpen) setScheduleOpen(true);
    },
    [scheduleOpen, onPreviewChannelChange, previewChannel],
  );

  /**
   * 键盘导航主入口：
   * - 方向键只在"焦点所在栏"内移动，跨栏走左右键；
   * - 频道栏移动焦点即预览（不换台）；
   * - 右栏或把手处向左 = 收起节目单栏并回到预览行；
   * - 非 replay 栏的 Esc 只是让焦点离开当前按钮。
   */
  const handlePaneKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLDivElement>) => {
      const focused = document.activeElement as HTMLElement | null;
      if (!focused) return;
      // 输入框里打字时不参与栏内导航，也不吞键（搜索框有自己的 Enter/Esc 语义）。
      if (focused.tagName === "INPUT" || focused.tagName === "TEXTAREA") return;

      const focusedPane = focused.closest<HTMLElement>("[data-pane]");
      const pane = focusedPane?.dataset.pane as PaneName | undefined;
      if (!pane) return;

      const shouldSwallow = SWALLOWED_KEYS.includes(event.key) || /^[0-9]$/.test(event.key);
      if (shouldSwallow) event.stopPropagation();
      if (!NAVIGATED_KEYS.includes(event.key)) return;
      event.preventDefault();

      // 栏内步进：到头停住；频道栏额外把焦点行同步为预览频道。
      const moveFocusWithin = (offset: number) => {
        if (!focusedPane) return;
        const buttons = collectPaneButtons(focusedPane);
        const index = buttons.indexOf(focused as HTMLButtonElement);
        const base = index < 0 ? 0 : index;
        const next = Math.max(0, Math.min(base + offset, buttons.length - 1));
        const button = buttons[next];
        if (!button) return;
        button.focus();
        if (pane === "channels") {
          const id = button.dataset?.id;
          const channel = visibleChannels.find((entry) => entry.id === id);
          if (channel && channel.id !== previewChannel?.id) onPreviewChannelChange(channel);
        }
      };

      if (event.key === "ArrowDown") {
        moveFocusWithin(1);
      } else if (event.key === "ArrowUp") {
        moveFocusWithin(-1);
      } else if (event.key === "ArrowRight") {
        if (pane === "categories") {
          // 从分组进频道栏：落第一行并预览之。
          focusPaneButton(channelPaneRef.current);
          const first = visibleChannels[0];
          if (first && first.id !== previewChannel?.id) onPreviewChannelChange(first);
        } else if (pane === "channels") {
          // 从频道进节目单：按当前行序位对齐频道，再展开右栏。
          const index = collectPaneButtons(focusedPane).indexOf(focused as HTMLButtonElement);
          const channel = visibleChannels[index] ?? visibleChannels[0];
          if (channel) {
            if (channel.id !== previewChannel?.id) onPreviewChannelChange(channel);
            revealSchedulePane();
          }
        } else if (pane === "epgToggle") {
          revealSchedulePane();
        }
      } else if (event.key === "ArrowLeft") {
        if (pane === "replay" || pane === "epgToggle") {
          // 遥控器向左 = 收起节目单栏（收起第三列），焦点回到预览频道所在行。
          setScheduleOpen(false);
          focusPaneButton(channelPaneRef.current, previewChannel?.id);
        } else if (pane === "channels") {
          focusPaneButton(groupPaneRef.current, activeGroup ?? undefined);
        }
      } else if (event.key === "Escape") {
        if (pane === "replay") {
          setScheduleOpen(false);
          focusPaneButton(channelPaneRef.current, previewChannel?.id);
        } else {
          focused.blur();
        }
      }
    },
    [visibleChannels, onPreviewChannelChange, revealSchedulePane, previewChannel, activeGroup],
  );

  return (
    <div className="flex h-full w-full flex-col bg-transparent" onKeyDown={handlePaneKeyDown}>
      <div className="flex shrink-0 items-center gap-2 px-2 pt-2 pb-0">
        <QuickSearchBox channels={channels} selectedGroup={activeGroup} onChannelSelect={handleChannelPicked} />
        {settingsSlot && <div className="shrink-0">{settingsSlot}</div>}
      </div>

      <div className="flex min-h-0 flex-1">
        <div ref={groupPaneRef} data-pane="categories" className={paneClassName("w-[28%] max-w-44")}>
          <PaneTitleBar
            icon={<Layers className="h-3.5 w-3.5 shrink-0 text-violet-600 dark:text-violet-300" />}
            label={t("channelGroups")}
          />
          <div className="flex-1 overflow-y-auto py-1">
            <div className="flex flex-col">
              {[null, ...groups].map((group) => {
                // null 代表"全部频道"；isPicked 同时驱动选中态与焦点预览。
                const isPicked = (group ?? null) === activeGroup;
                return (
                  <button
                    type="button"
                    key={group ?? "all"}
                    data-id={group ?? "__all__"}
                    title={group ?? t("allChannels")}
                    onClick={() => applyGroupFilter(group)}
                    onFocus={() => {
                      // 焦点即预览：遥控器上下移动分组时，频道列表即时跟随（与点击等价）。
                      if (!isPicked) setActiveGroup(group ?? null);
                    }}
                    className={[
                      PLAYER_LIST_SURFACE_BASE_CLASS,
                      GROUP_ROW_CLASS,
                      isPicked ? PLAYER_LIST_SURFACE_SELECTED_CLASS : PLAYER_LIST_SURFACE_DEFAULT_CLASS,
                      // 保持原数组 join 形态：条件为假时 false 以字面量落入类串，刻意不清洗。
                      !isPicked && PLAYER_LIST_SURFACE_HOVER_CLASS,
                    ].join(" ")}
                  >
                    {group ?? t("allChannels")}
                  </button>
                );
              })}
            </div>
          </div>
        </div>

        <div ref={channelPaneRef} data-pane="channels" className={paneClassName("flex-1")}>
          <PaneTitleBar
            icon={<Tv className="h-3.5 w-3.5 shrink-0 text-violet-600 dark:text-violet-300" />}
            label={`${t("channels")}${visibleChannels.length ? ` (${visibleChannels.length})` : ""}`}
          />
          <div className="flex-1 overflow-y-auto py-1">
            <div className="flex flex-col">
              {visibleChannels.map((channel) => (
                <ChannelListItem
                  key={channel.id}
                  channel={channel}
                  isCurrentChannel={channel.id === currentChannel?.id}
                  handleChannelClick={handleChannelPicked}
                  onProgramClick={handleInlineProgramClick}
                  locale={locale}
                  currentProgram={
                    previewChannel?.id === channel.id
                      ? previewNowPlaying?.title || (previewNowPlaying ? t("excellentProgram") : undefined)
                      : undefined
                  }
                />
              ))}
            </div>
          </div>
        </div>

        {scheduleOpen && (
          <div ref={schedulePaneRef} data-pane="replay" className={paneClassName("w-[42%] max-w-[20rem]")}>
            <PaneTitleBar
              icon={<History className="h-3.5 w-3.5 shrink-0 text-violet-600 dark:text-violet-300" />}
              label={`${t("catchup")}${previewChannel ? ` · ${previewChannel.name}` : ""}`}
            />
            {epgConfigured === false && (
              <div className="shrink-0 px-2.5 py-1 text-[10px] leading-4 text-amber-600 dark:text-amber-300/90">
                {t("epgNotConfigured")}
              </div>
            )}
            <div className="min-h-0 flex-1">
              <EPGView
                channelId={previewEpgChannelId}
                epgData={epgData}
                onProgramSelect={handleScheduleProgramPicked}
                locale={locale}
                supportsCatchup={supportsCatchup}
                currentPlayingProgram={
                  previewChannel?.id === currentChannel?.id ? currentPlayingProgram : previewNowPlaying
                }
              />
            </div>
          </div>
        )}

        {/* 竖排"节目单"把手：展开时贴浮层右缘，收起后留在原位可再次展开 */}
        <button
          type="button"
          data-pane="epgToggle"
          data-id="__epg_toggle__"
          aria-expanded={scheduleOpen}
          aria-label={t("programGuide")}
          title={t("programGuide")}
          onClick={() => setScheduleOpen(!scheduleOpen)}
          className="player-performance-motion flex w-5 shrink-0 cursor-pointer touch-manipulation flex-col items-center justify-center gap-1.5 border-violet-950/10 border-l text-slate-500 transition-colors hover:bg-violet-400/10 hover:text-violet-700 focus-visible:outline-none dark:border-violet-100/10 dark:text-slate-400 dark:hover:bg-violet-300/10 dark:hover:text-violet-100"
        >
          <span className="font-semibold text-[10px] leading-none tracking-[0.18em] [writing-mode:vertical-rl]">
            {t("programGuide")}
          </span>
          {/* 收起显示 ">>"（向右展开），展开显示 "<<"（向左收起） */}
          {scheduleOpen ? (
            <ChevronsLeft className="h-3 w-3 shrink-0" aria-hidden="true" />
          ) : (
            <ChevronsRight className="h-3 w-3 shrink-0" aria-hidden="true" />
          )}
        </button>
      </div>
    </div>
  );
}

/** 搜索框：Enter 选首个命中，Esc 清空退出；命中列表最多 8 条。 */
const QuickSearchBox = memo(function QuickSearchBox({
  channels,
  selectedGroup,
  onChannelSelect,
}: {
  channels: Channel[];
  selectedGroup: string | null;
  onChannelSelect: (channel: Channel) => void;
}) {
  const [searchText, setSearchText] = useState("");
  const searchInputRef = useRef<HTMLInputElement>(null);

  const hits = useMemo(() => {
    const normalized = searchText.trim().toLowerCase();
    if (!normalized) return [];
    return channels
      .filter((channel) => !selectedGroup || channel.groups.includes(selectedGroup))
      .filter(
        (channel) =>
          channel.name.toLowerCase().includes(normalized) ||
          channel.id.includes(searchText.trim()) ||
          // 频道行展示的是频道号（订阅序位），按号搜索必须能命中
          String(channel.number ?? "").startsWith(searchText.trim()),
      )
      .slice(0, 8);
  }, [channels, searchText, selectedGroup]);

  // 命中后的统一收尾：清空输入并还焦点给页面——焦点滞留输入框会让遥控器导航失灵。
  const settleAfterPick = () => {
    setSearchText("");
    (document.activeElement as HTMLElement)?.blur();
  };

  return (
    <div className="relative min-w-0 flex-1">
      <input
        ref={searchInputRef}
        type="text"
        placeholder="搜索频道..."
        value={searchText}
        onChange={(event) => setSearchText(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && hits.length > 0) {
            onChannelSelect(hits[0]);
            settleAfterPick();
          } else if (event.key === "Escape") {
            settleAfterPick();
          }
        }}
        className="player-performance-input-background player-performance-motion h-8 w-full rounded-md border border-violet-900/20 bg-white/85 px-3 py-0 pl-8 text-slate-800 text-xs shadow-none transition placeholder:text-slate-500 focus:border-violet-400/70 focus:bg-white/95 focus:outline-none dark:border-violet-100/20 dark:bg-white/14 dark:text-violet-50 dark:placeholder:text-slate-400 dark:focus:bg-white/20"
      />
      <MagnifierGlyph className="absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-violet-600/85 dark:text-violet-300/80" />
      {hits.length > 0 && (
        <div className="player-performance-overlay-background absolute top-full left-0 z-30 mt-1 max-h-64 w-full overflow-y-auto rounded-md border border-violet-200/40 bg-white/90 p-1 shadow-[0_12px_28px_-16px_rgba(0,0,0,0.55)] backdrop-blur-lg dark:border-violet-100/10 dark:bg-slate-950/88">
          {hits.map((channel) => (
            <button
              type="button"
              key={channel.id}
              onClick={() => {
                onChannelSelect(channel);
                settleAfterPick();
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

export const ChannelBrowser = memo(ThreePaneChannelBrowser);
