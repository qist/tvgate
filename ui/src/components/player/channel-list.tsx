/**
 * 频道列表。
 * 搜索（按名称/频道号，结果按匹配强度排序）+ 分组筛选（记忆上次选择）+ 当前节目映射 + 自动滚动居中。
 */
import { ChevronDown, Layers, Search } from "lucide-react";
import { memo, type RefObject, startTransition, useCallback, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import { type EPGData, getCurrentProgram, getEPGChannelId } from "../../lib/epg-parser";
import type { Locale } from "../../lib/locale";
import { getSelectedGroup, saveSelectedGroup } from "../../lib/player-storage";
import type { Channel } from "../../types/player";
import { ChannelListItem } from "./channel-list-item";

export const nextScrollBehaviorRef: RefObject<"smooth" | "instant" | "skip"> = { current: "instant" };

function filterChannels(channels: Channel[] | undefined, searchQuery: string, selectedGroup: string | null) {
  if (!channels) return [];
  const result = channels.filter((channel) => {
    if (selectedGroup && !channel.groups.includes(selectedGroup)) return false;
    if (!searchQuery) return true;
    const normalized = searchQuery.toLowerCase();
    return (
      channel.name.toLowerCase().includes(normalized) ||
      channel.id.includes(searchQuery) ||
      // 频道行显示的是频道号（订阅序位），按号搜索要能命中
      String(channel.number ?? "").startsWith(searchQuery)
    );
  });

  if (!searchQuery) return result;
  return result.sort((a, b) => {
    const score = (channel: Channel) => {
      if (String(channel.number ?? "") === searchQuery) return 0;
      if (channel.id === searchQuery) return 0;
      if (channel.id.startsWith(searchQuery)) return 1;
      if (channel.id.includes(searchQuery)) return 2;
      return 3;
    };
    return score(a) - score(b);
  });
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

interface ChannelListResultsProps {
  currentChannel: Channel | null;
  currentChannelRef: RefObject<HTMLButtonElement | null>;
  currentProgramMap: Record<string, string>;
  filteredChannels: Channel[];
  filteredChannelsHasCurrentChannel: boolean;
  handleChannelClick: (channel: Channel) => void;
  locale: Locale;
}

const ChannelListResults = memo(function ChannelListResults({
  currentChannel,
  currentChannelRef,
  currentProgramMap,
  filteredChannels,
  filteredChannelsHasCurrentChannel,
  handleChannelClick,
  locale,
}: ChannelListResultsProps) {
  return (
    <div className="grid grid-cols-2 gap-1.5 md:grid-cols-1">
      {filteredChannels.map((channel, index) => (
        <ChannelListItem
          key={channel.id}
          ref={
            (filteredChannelsHasCurrentChannel ? currentChannel?.id === channel.id : index === 0)
              ? currentChannelRef
              : null
          }
          channel={channel}
          isCurrentChannel={channel.id === currentChannel?.id}
          handleChannelClick={handleChannelClick}
          locale={locale}
          currentProgram={currentProgramMap[channel.id]}
        />
      ))}
    </div>
  );
});

function ChannelListComponent({ channels, groups, currentChannel, onChannelSelect, locale, settingsSlot, epgData, panelVisible }: ChannelListProps) {
  const t = usePlayerTranslation(locale);

  const [searchQuery, setSearchQuery] = useState("");
  // 选中的分组跨会话记忆：初始化只读记忆值，组是否存在等分组列表到位后再校验（见下方 effect）。
  const [selectedGroup, setSelectedGroup] = useState<string | null>(() => getSelectedGroup());
  /** 分组网格默认折叠（手机更省空间，也更适配遥控器导航）。 */
  const [groupsOpen, setGroupsOpen] = useState(false);
  const currentChannelRef = useRef<HTMLButtonElement>(null);

  /**
   * 分组列表到位后校验记忆值：列表为空（元数据未到 / 订阅为空）时不动选择；
   * 组确实不存在时本次回落"全部"但保留记忆值（订阅短暂缺组不该毁掉偏好）。
   */
  useEffect(() => {
    if (!selectedGroup || !groups || groups.length === 0) return;
    if (!groups.includes(selectedGroup)) setSelectedGroup(null);
  }, [groups, selectedGroup]);

  const deferredSearchQuery = useDeferredValue(searchQuery);
  const deferredSelectedGroup = useDeferredValue(selectedGroup);

  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = setInterval(() => startTransition(() => setNow(new Date())), 60_000);
    return () => clearInterval(timer);
  }, []);

  const deferredEpgData = useDeferredValue(epgData);

  const currentProgramMap = useMemo(() => {
    const map: Record<string, string> = {};
    if (!channels || !deferredEpgData) return map;
    for (const channel of channels) {
      const epgId = getEPGChannelId(channel, deferredEpgData);
      if (!epgId) continue;
      const program = getCurrentProgram(epgId, deferredEpgData, now);
      if (program?.title) map[channel.id] = program.title;
    }
    return map;
  }, [channels, deferredEpgData, now]);

  const filteredChannels = useMemo(
    () => filterChannels(channels, deferredSearchQuery, deferredSelectedGroup),
    [channels, deferredSearchQuery, deferredSelectedGroup],
  );

  const filteredChannelsHasCurrentChannel = useMemo(
    () => Boolean(currentChannel && filteredChannels.some((channel) => channel.id === currentChannel.id)),
    [filteredChannels, currentChannel],
  );

  useLayoutEffect(() => {
    window.setTimeout(() => {
      nextScrollBehaviorRef.current = "smooth";
    }, 0);
    if (!currentChannel) return;
    const requested = nextScrollBehaviorRef.current;
    if (requested === "skip") return;
    const behavior = requested === "smooth" && window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : requested;
    currentChannelRef.current?.scrollIntoView({ behavior, block: "center" });
  }, [currentChannel]);

  useLayoutEffect(() => {
    if (!filteredChannels.length) return;
    currentChannelRef.current?.scrollIntoView({ behavior: "instant", block: "center" });
  }, [filteredChannels]);

  const handleChannelClick = useCallback(
    (channel: Channel) => {
      nextScrollBehaviorRef.current = "skip";
      onChannelSelect(channel);
    },
    [onChannelSelect],
  );

  const handleSearchKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLInputElement>) => {
      if (event.key === "Enter") {
        const immediateResults = filterChannels(channels, searchQuery, selectedGroup);
        if (immediateResults.length > 0) {
          onChannelSelect(immediateResults[0]);
          setSearchQuery("");
          (document.activeElement as HTMLElement)?.blur();
        }
      } else if (event.key === "Escape") {
        (document.activeElement as HTMLElement | null)?.blur();
        setSearchQuery("");
      }
    },
    [channels, onChannelSelect, searchQuery, selectedGroup],
  );

  const handleSearchInputChange = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    setSearchQuery(event.target.value);
  }, []);

  /**
   * 点分组只改本次会话的筛选，**不写记忆**：记忆以"正在播放的频道所在分组"为准。
   * 否则"只点过分组、没选台"会把列表钉在那个分组（用户实测反馈：再打开时没跳到播放台所在组）。
   */
  const handleGroupSelect = useCallback((group: string | null) => {
    setSelectedGroup(group);
  }, []);

  /**
   * 换台（含首次进入）后，分组跟随**正在播放的频道**：下次打开列表即落在该台所在分组。
   * 只认 currentChannel 变化（不认点分组），所以点分组不会立刻被弹回去。
   */
  const syncGroupToPlayingChannel = useCallback(() => {
    if (!currentChannel) return;
    const group = currentChannel.groups.find((g) => groups?.includes(g)) ?? currentChannel.groups[0] ?? null;
    if (!group) return;
    setSelectedGroup(group);
    saveSelectedGroup(group);
  }, [currentChannel, groups]);

  const syncedChannelRef = useRef<string | null>(null);
  useEffect(() => {
    if (!currentChannel) return;
    if (syncedChannelRef.current === currentChannel.id) return;
    syncedChannelRef.current = currentChannel.id;
    syncGroupToPlayingChannel();
  }, [currentChannel, syncGroupToPlayingChannel]);

  /** 浮层重新出现（退出全屏 / 再次打开）时同样拉回正在播放的频道所在分组。 */
  const wasPanelVisibleRef = useRef(false);
  useEffect(() => {
    const visible = Boolean(panelVisible);
    const justOpened = visible && !wasPanelVisibleRef.current;
    wasPanelVisibleRef.current = visible;
    if (justOpened) syncGroupToPlayingChannel();
  }, [panelVisible, syncGroupToPlayingChannel]);

  return (
    <div className="flex h-full flex-col bg-transparent">
      <div className="px-2 pt-2 pb-0">
        <div className="flex items-center gap-2">
          <div className="relative min-w-0 flex-1">
            <input
              type="text"
              placeholder={t("searchChannels")}
              value={searchQuery}
              onChange={handleSearchInputChange}
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
            onClick={() => setGroupsOpen((open) => !open)}
            aria-expanded={groupsOpen}
            className="flex h-8 w-full cursor-pointer items-center justify-between rounded-lg px-1.5 text-left font-medium text-slate-600 text-xs transition-colors hover:text-violet-800 focus-visible:border-violet-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-400/60 dark:text-slate-300 dark:hover:text-violet-100 md:text-[13px]"
          >
            <span className="flex min-w-0 items-center gap-1.5">
              <Layers className="h-3.5 w-3.5 shrink-0 text-violet-600 dark:text-violet-300" />
              <span className="shrink-0">{t("channelGroups")}</span>
              <span className="min-w-0 truncate text-slate-400 dark:text-slate-500">· {selectedGroup ?? t("allChannels")}</span>
            </span>
            <ChevronDown
              className={[
                "h-4 w-4 shrink-0 text-slate-400 transition-transform duration-200 dark:text-slate-500",
                groupsOpen && "rotate-180",
              ].join(" ")}
            />
          </button>
          {groupsOpen && (
            <div className="mt-1.5 grid max-h-44 grid-cols-3 gap-1.5 overflow-y-auto [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
              {[null, ...groups].map((group) => (
                <button
                  type="button"
                  key={group ?? "all"}
                  onClick={() => {
                    handleGroupSelect(group);
                    setGroupsOpen(false);
                  }}
                  onFocus={(event) => event.currentTarget.scrollIntoView({ block: "nearest" })}
                  title={group ?? t("allChannels")}
                  className={[
                    "player-performance-motion h-8 min-w-0 touch-manipulation overflow-hidden text-ellipsis whitespace-nowrap rounded-full border px-2 text-center font-medium text-xs leading-none transition-[color,background-color,border-color,box-shadow] focus-visible:border-violet-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-400/60 md:h-7",
                    selectedGroup === group
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
        <ChannelListResults
          currentChannel={currentChannel}
          currentChannelRef={currentChannelRef}
          currentProgramMap={currentProgramMap}
          filteredChannels={filteredChannels}
          filteredChannelsHasCurrentChannel={filteredChannelsHasCurrentChannel}
          handleChannelClick={handleChannelClick}
          locale={locale}
        />
      </div>
    </div>
  );
}

export const ChannelList = memo(ChannelListComponent);
