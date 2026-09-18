/**
 * 频道列表面板（紧凑 / 移动端布局）。
 * 名称与频道号搜索（命中结果按匹配强度排序）+ 分组筛选（跨会话记忆上次选择）
 * + 当前节目名映射 + "正在播放"行自动滚动居中。
 * 自动滚动由模块级 nextScrollBehaviorRef 与播放页协同：点击选台后跳过一次滚动。
 */
import { ChevronDown, Layers, Search } from "lucide-react";
import { memo, type RefObject, startTransition, useCallback, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import { type EPGData, getCurrentProgram, getEPGChannelId } from "../../lib/epg-parser";
import type { Locale } from "../../lib/locale";
import { getSelectedGroup, saveSelectedGroup } from "../../lib/player-storage";
import type { Channel } from "../../types/player";
import { ChannelListItem } from "./channel-list-item";

/**
 * 模块级滚动指令，供播放页等外部模块直接读写：
 * "instant" = 首次定位用瞬时滚动；"smooth" = 常规换台平滑居中；"skip" = 点击选台后跳过自动滚动。
 */
export const nextScrollBehaviorRef: RefObject<"smooth" | "instant" | "skip"> = { current: "instant" };

/**
 * 搜索命中的强度分级：号位 / ID 完全相等最优（0），其后依次是 ID 前缀（1）、
 * ID 包含（2）、其余（3，含仅名称命中）。值越小排得越靠前。
 */
function channelMatchRank(channel: Channel, searchQuery: string) {
  if (String(channel.number ?? "") === searchQuery) return 0;
  if (channel.id === searchQuery) return 0;
  if (channel.id.startsWith(searchQuery)) return 1;
  if (channel.id.includes(searchQuery)) return 2;
  return 3;
}

/** 按分组 + 关键词筛出可见频道；有关键词时再按匹配强度稳定排序。 */
function pickMatchingChannels(channels: Channel[] | undefined, searchQuery: string, selectedGroup: string | null) {
  if (!channels) return [];
  const matches = channels.filter((channel) => {
    if (selectedGroup && !channel.groups.includes(selectedGroup)) return false;
    if (!searchQuery) return true;
    const normalized = searchQuery.toLowerCase();
    return (
      channel.name.toLowerCase().includes(normalized) ||
      channel.id.includes(searchQuery) ||
      // 频道行展示的是频道号（订阅序位），按号搜索必须能命中
      String(channel.number ?? "").startsWith(searchQuery)
    );
  });

  if (!searchQuery) return matches;
  return matches.sort((a, b) => channelMatchRank(a, searchQuery) - channelMatchRank(b, searchQuery));
}

interface ChannelListProps {
  channels?: Channel[];
  groups?: string[];
  currentChannel: Channel | null;
  onChannelSelect: (channel: Channel) => void;
  locale: Locale;
  settingsSlot?: ReactNode;
  epgData?: EPGData;
  /** 浮层当前是否可见（手机全屏时隐藏）：重新出现时把分组拉回正在播放的频道所在分组。 */
  panelVisible?: boolean;
}

/** 结果网格：拆成独立 memo 组件，让搜索输入等高频更新停在父层、不重刷整列行。 */
interface ChannelRowsGridProps {
  playingChannel: Channel | null;
  playingRowRef: RefObject<HTMLButtonElement | null>;
  nowPlayingTitleByChannelId: Record<string, string>;
  visibleChannels: Channel[];
  visibleListContainsPlayingChannel: boolean;
  onChannelActivate: (channel: Channel) => void;
  locale: Locale;
}

const ChannelRowsGrid = memo(function ChannelRowsGrid({
  playingChannel,
  playingRowRef,
  nowPlayingTitleByChannelId,
  visibleChannels,
  visibleListContainsPlayingChannel,
  onChannelActivate,
  locale,
}: ChannelRowsGridProps) {
  return (
    <div className="grid grid-cols-2 gap-1.5 md:grid-cols-1">
      {visibleChannels.map((channel, index) => (
        <ChannelListItem
          key={channel.id}
          ref={
            (visibleListContainsPlayingChannel ? playingChannel?.id === channel.id : index === 0)
              ? playingRowRef
              : null
          }
          channel={channel}
          isCurrentChannel={channel.id === playingChannel?.id}
          handleChannelClick={onChannelActivate}
          locale={locale}
          currentProgram={nowPlayingTitleByChannelId[channel.id]}
        />
      ))}
    </div>
  );
});

function MobileChannelList({ channels, groups, currentChannel, onChannelSelect, locale, settingsSlot, epgData, panelVisible }: ChannelListProps) {
  const t = usePlayerTranslation(locale);

  const [searchText, setSearchText] = useState("");
  // 分组选择跨会话记忆：初始化只读记忆值，组是否仍存在等分组列表到位后再校验（见下方 effect）。
  const [activeGroup, setActiveGroup] = useState<string | null>(() => getSelectedGroup());
  // 分组网格默认折叠：手机上省纵向空间，遥控器导航的层级也更浅。
  const [groupGridExpanded, setGroupGridExpanded] = useState(false);
  // 指向"正在播放"的行（列表里没有该行则指到第一行），自动滚动以它为锚点。
  const playingRowRef = useRef<HTMLButtonElement>(null);

  /**
   * 分组列表到位后校验记忆值：列表为空（元数据未到 / 订阅为空）时不动选择；
   * 组确实不存在时本次回落"全部"但保留记忆值（订阅短暂缺组不该毁掉偏好）。
   */
  useEffect(() => {
    if (!activeGroup || !groups || groups.length === 0) return;
    if (!groups.includes(activeGroup)) setActiveGroup(null);
  }, [groups, activeGroup]);

  // 搜索与分组都走 deferred 值：输入即时回显，重活的过滤稍微让路。
  const deferredSearchText = useDeferredValue(searchText);
  const deferredActiveGroup = useDeferredValue(activeGroup);

  // "现在"每分钟走一格（transition 包裹降低优先级），驱动当前节目映射随时间翻页。
  const [currentMoment, setCurrentMoment] = useState(() => new Date());
  useEffect(() => {
    const timer = setInterval(() => startTransition(() => setCurrentMoment(new Date())), 60_000);
    return () => clearInterval(timer);
  }, []);

  // EPG 数据可能很大，同样用 deferred 值，避免节目名映射阻塞搜索 / 筛选的即时反馈。
  const deferredEpgData = useDeferredValue(epgData);

  const nowPlayingTitleByChannelId = useMemo(() => {
    const titles: Record<string, string> = {};
    if (!channels || !deferredEpgData) return titles;
    for (const channel of channels) {
      const epgId = getEPGChannelId(channel, deferredEpgData);
      if (!epgId) continue;
      const program = getCurrentProgram(epgId, deferredEpgData, currentMoment);
      if (program?.title) titles[channel.id] = program.title;
    }
    return titles;
  }, [channels, deferredEpgData, currentMoment]);

  const visibleChannels = useMemo(
    () => pickMatchingChannels(channels, deferredSearchText, deferredActiveGroup),
    [channels, deferredSearchText, deferredActiveGroup],
  );

  const visibleListContainsPlayingChannel = useMemo(
    () => Boolean(currentChannel && visibleChannels.some((channel) => channel.id === currentChannel.id)),
    [visibleChannels, currentChannel],
  );

  // 换台后的滚动：先把下一轮指令恢复为"smooth"，再按本次指令执行（"skip" 由点击选台写入）。
  useLayoutEffect(() => {
    window.setTimeout(() => {
      nextScrollBehaviorRef.current = "smooth";
    }, 0);
    if (!currentChannel) return;
    const requested = nextScrollBehaviorRef.current;
    if (requested === "skip") return;
    const behavior = requested === "smooth" && window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : requested;
    playingRowRef.current?.scrollIntoView({ behavior, block: "center" });
  }, [currentChannel]);

  // 过滤结果变化（搜索 / 切分组）后重新定位：必须瞬时滚动，否则视觉上像"漂移"。
  useLayoutEffect(() => {
    if (!visibleChannels.length) return;
    playingRowRef.current?.scrollIntoView({ behavior: "instant", block: "center" });
  }, [visibleChannels]);

  // 点击选台后跳过随后的自动滚动：用户在主动操作，列表通常会随之收起。
  const handleChannelPicked = useCallback(
    (channel: Channel) => {
      nextScrollBehaviorRef.current = "skip";
      onChannelSelect(channel);
    },
    [onChannelSelect],
  );

  // 回车即选首个命中：用未 deferred 的即时值重算，保证结果与输入框当前内容一致。
  const handleSearchKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLInputElement>) => {
      if (event.key === "Enter") {
        const immediateMatches = pickMatchingChannels(channels, searchText, activeGroup);
        if (immediateMatches.length > 0) {
          onChannelSelect(immediateMatches[0]);
          setSearchText("");
          (document.activeElement as HTMLElement)?.blur();
        }
      } else if (event.key === "Escape") {
        (document.activeElement as HTMLElement | null)?.blur();
        setSearchText("");
      }
    },
    [channels, onChannelSelect, searchText, activeGroup],
  );

  const handleSearchTextChange = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    setSearchText(event.target.value);
  }, []);

  /**
   * 点分组只改本次会话的筛选，**不写记忆**：记忆以"正在播放的频道所在分组"为准。
   * 否则"只点过分组、没选台"会把列表钉在那个分组（用户实测反馈：再打开时没跳到播放台所在组）。
   */
  const applyGroupFilter = useCallback((group: string | null) => {
    setActiveGroup(group);
  }, []);

  /**
   * 换台（含首次进入）后，分组跟随**正在播放的频道**：下次打开列表即落在该台所在分组。
   * 只认 currentChannel 变化（不认点分组），所以点分组不会立刻被弹回去。
   */
  const alignGroupWithPlayingChannel = useCallback(() => {
    if (!currentChannel) return;
    const group = currentChannel.groups.find((g) => groups?.includes(g)) ?? currentChannel.groups[0] ?? null;
    if (!group) return;
    setActiveGroup(group);
    saveSelectedGroup(group);
  }, [currentChannel, groups]);

  // 已同步过分组的频道 id：同一频道不重复触发，避免把用户刚点的分组顶掉。
  const groupSyncedChannelIdRef = useRef<string | null>(null);
  useEffect(() => {
    if (!currentChannel) return;
    if (groupSyncedChannelIdRef.current === currentChannel.id) return;
    groupSyncedChannelIdRef.current = currentChannel.id;
    alignGroupWithPlayingChannel();
  }, [currentChannel, alignGroupWithPlayingChannel]);

  /** 浮层重新出现（退出全屏 / 再次打开）时同样拉回正在播放的频道所在分组。 */
  const prevPanelVisibleRef = useRef(false);
  useEffect(() => {
    const visible = Boolean(panelVisible);
    const justReopened = visible && !prevPanelVisibleRef.current;
    prevPanelVisibleRef.current = visible;
    if (justReopened) alignGroupWithPlayingChannel();
  }, [panelVisible, alignGroupWithPlayingChannel]);

  return (
    <div className="flex h-full flex-col bg-transparent">
      <div className="px-2 pt-2 pb-0">
        <div className="flex items-center gap-2">
          <div className="relative min-w-0 flex-1">
            <input
              type="text"
              placeholder={t("searchChannels")}
              value={searchText}
              onChange={handleSearchTextChange}
              onKeyDown={handleSearchKeyDown}
              className="player-performance-input-background player-performance-motion h-8 w-full rounded-xl border border-violet-900/20 bg-white/90 px-3 py-0 pl-8 text-slate-800 text-xs shadow-none transition placeholder:text-slate-500 focus:border-violet-400/70 focus:bg-white/95 focus:outline-none dark:border-violet-100/20 dark:bg-slate-900/90 dark:text-violet-50 dark:placeholder:text-slate-400 md:h-9 md:pl-9 md:text-sm"
            />
            <Search className="absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-violet-600/85 dark:text-violet-300/80 md:h-4 md:w-4" />
          </div>
          {settingsSlot && <div className="shrink-0">{settingsSlot}</div>}
        </div>
      </div>

      {groups && groups.length > 0 && (
        <div className="player-performance-channel-groups mt-2 border-violet-950/10 border-y bg-[linear-gradient(90deg,rgba(255,255,255,0.5),rgba(255,255,255,0.62))] px-2 py-1.5 backdrop-blur-xl dark:border-violet-100/10 dark:bg-[linear-gradient(90deg,rgba(2,6,23,0.6),rgba(2,6,23,0.5))]">
          <button
            type="button"
            onClick={() => setGroupGridExpanded((open) => !open)}
            aria-expanded={groupGridExpanded}
            className="flex h-8 w-full cursor-pointer items-center justify-between rounded-lg px-1.5 text-left font-medium text-slate-600 text-xs transition-colors hover:text-violet-800 focus-visible:border-violet-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-400/60 dark:text-slate-300 dark:hover:text-violet-100 md:text-[13px]"
          >
            <span className="flex min-w-0 items-center gap-1.5">
              <Layers className="h-3.5 w-3.5 shrink-0 text-violet-600 dark:text-violet-300" />
              <span className="shrink-0">{t("channelGroups")}</span>
              <span className="min-w-0 truncate text-slate-400 dark:text-slate-500">· {activeGroup ?? t("allChannels")}</span>
            </span>
            {/* 保持原数组 join 形态：折叠时 false 以字面量落入类串，刻意不清洗以保最终串一致。 */}
            <ChevronDown
              className={[
                "h-4 w-4 shrink-0 text-slate-400 transition-transform duration-200 dark:text-slate-500",
                groupGridExpanded && "rotate-180",
              ].join(" ")}
            />
          </button>
          {groupGridExpanded && (
            <div className="mt-1.5 grid max-h-44 grid-cols-3 gap-1.5 overflow-y-auto [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
              {[null, ...groups].map((group) => (
                <button
                  type="button"
                  key={group ?? "all"}
                  title={group ?? t("allChannels")}
                  onClick={() => {
                    applyGroupFilter(group);
                    setGroupGridExpanded(false);
                  }}
                  onFocus={(event) => event.currentTarget.scrollIntoView({ block: "nearest" })}
                  className={[
                    "player-performance-motion h-8 min-w-0 touch-manipulation overflow-hidden text-ellipsis whitespace-nowrap rounded-full border px-2 text-center font-medium text-xs leading-none transition-[color,background-color,border-color,box-shadow] focus-visible:border-violet-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-400/60 md:h-7",
                    activeGroup === group
                      ? "border-violet-400/30 bg-violet-500/10 text-violet-700 dark:border-violet-300/20 dark:bg-violet-400/14 dark:text-violet-200"
                      : "cursor-pointer border-violet-900/8 bg-white/55 text-slate-500 hover:border-violet-400/30 hover:bg-violet-50/80 hover:text-violet-800 dark:border-violet-100/10 dark:bg-slate-950/35 dark:text-slate-400 dark:hover:bg-violet-300/10 dark:hover:text-violet-100",
                  ].join(" ")}
                >
                  {group ?? t("allChannels")}
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      <div className="flex-1 overflow-y-auto px-2 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
        <ChannelRowsGrid
          playingChannel={currentChannel}
          playingRowRef={playingRowRef}
          nowPlayingTitleByChannelId={nowPlayingTitleByChannelId}
          visibleChannels={visibleChannels}
          visibleListContainsPlayingChannel={visibleListContainsPlayingChannel}
          onChannelActivate={handleChannelPicked}
          locale={locale}
        />
      </div>
    </div>
  );
}

export const ChannelList = memo(MobileChannelList);
