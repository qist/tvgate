/**
 * EPG 节目单视图。
 *
 * 职责：把某频道的节目按"本地日"分组渲染成时间线，行内标注正在播出 / 可回看状态，
 * 点击行向上层回传播放请求（直播行点击语义为"回到现在"）。数据来源本组件不感知，
 * EPGData 的结构与生成见 lib/epg-parser。
 *
 * 两个值得单独说明的机制：
 * 1. 滚动意图单例（nextScrollBehaviorRef）：频道列表、节目单、页面容器分属不同组件子树，
 *    而"下一次自动滚动该怎么滚"是跨组件的事件时序约定（点节目要抑制居中、切台后要平滑居中），
 *    用组件 state 表达会把渲染时序与事件时序搅在一起，所以用模块级 ref 作共享信箱。
 * 2. 延迟时钟：每秒跳动的 now 驱动整张列表重算"已结束/播出中"，属于低优先级更新；
 *    用 useDeferredValue 包一层后，用户交互渲染不与秒跳重渲争抢优先级。
 */
import { Circle, History } from "lucide-react";
import { memo, useCallback, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState, type RefObject } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { EPGData } from "../../lib/epg-parser";
import type { Locale } from "../../lib/locale";
import type { EPGProgram } from "../../types/player";
// 列表表面样式 token 收口在 classnames：单行引入，避免多行命名清单喧宾夺主
import { PLAYER_EPG_LIST_ITEM_CLASS, PLAYER_LIST_SURFACE_BASE_CLASS, PLAYER_LIST_SURFACE_DEFAULT_CLASS, PLAYER_LIST_SURFACE_HOVER_CLASS, PLAYER_LIST_SURFACE_SELECTED_CLASS } from "./classnames";

/**
 * 跨组件共享的"下一次自动滚动"意图（频道列表 / 节目单 / 页面容器三方读写）。
 * 不需要触发渲染、且 effect 读取时必须立即可见，因此是导出的模块级 ref 而非 context/state。
 */
export const nextScrollBehaviorRef: RefObject<"smooth" | "instant" | "skip"> = { current: "instant" };

/** 时钟步长：着色判定只需分钟级精度，1 秒一跳在流畅与及时之间取平衡。 */
const CLOCK_TICK_MS = 1_000;

/** 自动滚动后把意图复位为 smooth 的延迟：0ms 即可，只需保证排在本次 effect 读取之后执行。 */
const SCROLL_RESET_DELAY_MS = 0;

const MS_PER_MINUTE = 60_000;
const MS_PER_DAY = 86_400_000;

/** 无节目单时的整面板占位样式：居中一行提示，占满容器高度，避免右侧留白显得像渲染故障。 */
const EMPTY_EPG_HINT_CLASS =
  "flex h-full items-center justify-center bg-transparent px-6 text-center text-slate-500 text-sm leading-6 dark:text-slate-400";

interface EpgPanelProps {
  channelId: string | null | undefined;
  epgData: EPGData;
  onProgramSelect: (programStart: Date, programEnd: Date) => void;
  locale: Locale;
  supportsCatchup: boolean;
  currentPlayingProgram: EPGProgram | null;
}

/** 无数据时的统一占位分组：与正常分组同构（空 Map + 空数组），让下游渲染分支保持单一。 */
function emptyGrouping(): { programsByDay: Map<string, EPGProgram[]>; channelSchedule: EPGProgram[] } {
  return { programsByDay: new Map<string, EPGProgram[]>(), channelSchedule: [] };
}

/**
 * 按节目开始时刻的"本地日"分组。日期键取本地零点的 ISO 串：选 ISO 而非手拼 "Y-M-D"，
 * 是因为它能被 new Date() 无损还原（列表头还要再解析一次用于展示）。
 * 分组保持输入顺序（Map 插入序），排序策略交给数据源，这里不做二次调整。
 */
function groupProgramsByLocalDay(schedule: EPGProgram[]): Map<string, EPGProgram[]> {
  const byDay = new Map<string, EPGProgram[]>();
  for (const entry of schedule) {
    const localMidnight = new Date(entry.beginsAt.getFullYear(), entry.beginsAt.getMonth(), entry.beginsAt.getDate());
    const dayKey = localMidnight.toISOString();
    const bucket = byDay.get(dayKey);
    if (bucket) bucket.push(entry);
    else byDay.set(dayKey, [entry]);
  }
  return byDay;
}

/**
 * 把剩余的滚动意图翻译成 scrollIntoView 的实际行为。
 * 入参不含 "skip"——调用方必须在进入这里之前就把 skip 短路掉。
 * 只有 smooth 需要征询系统"减少动态效果"偏好，instant 本身就是无动画语义。
 */
function resolveAutoScrollBehavior(intent: "smooth" | "instant"): "smooth" | "instant" {
  if (intent === "instant") return intent;
  const prefersReducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  return prefersReducedMotion ? "instant" : "smooth";
}

interface ProgramRowProps {
  /** 正在播出的那一行的 DOM ref：自动滚动靠它定位居中目标。 */
  playingRowRef: RefObject<HTMLButtonElement | null>;
  onProgramPicked: (programStart: Date, programEnd: Date) => void;
  /** 节目已播完（endsAt <= 当前时刻）：配合回看能力决定能否点播回放。 */
  alreadyEnded: boolean;
  locale: Locale;
  /** 正在直播（beginsAt <= 当前时刻 < endsAt）。 */
  onAirNow: boolean;
  /** 该行就是全局"正在播放"的节目：承担 ref 挂载与选中态样式。 */
  isNowPlaying: boolean;
  entry: EPGProgram;
  catchupEnabled: boolean;
}

const ProgramRow = memo(function ProgramRow({
  playingRowRef,
  onProgramPicked,
  alreadyEnded,
  locale,
  onAirNow,
  isNowPlaying,
  entry,
  catchupEnabled,
}: ProgramRowProps) {
  const t = usePlayerTranslation(locale);
  const formatClock = (moment: Date) => moment.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  const durationInMinutes = Math.round((entry.endsAt.getTime() - entry.beginsAt.getTime()) / MS_PER_MINUTE);

  // 可点播 = （已播完 && 支持回看）|| 正在直播
  const seekable = (alreadyEnded && catchupEnabled) || onAirNow;

  // 类名 token 数组是逐字契约：Array.join 不过滤 false，未命中分支会原样留下 "false" 占位——
  // 别改成 clsx 之类的假值剔除写法，那会改变最终进入 DOM 的 class 串。
  const rowClassTokens = [
    PLAYER_LIST_SURFACE_BASE_CLASS,
    PLAYER_EPG_LIST_ITEM_CLASS,
    "flex w-full text-left",
    isNowPlaying ? PLAYER_LIST_SURFACE_SELECTED_CLASS : PLAYER_LIST_SURFACE_DEFAULT_CLASS,
    seekable && !isNowPlaying && PLAYER_LIST_SURFACE_HOVER_CLASS,
    seekable && "cursor-pointer",
  ];

  // 行首细条：直播=品牌渐变、可回看=中性灰、其余=透明占位；三者同尺寸，保证各列纵向对齐。
  const stateBar = isNowPlaying ? (
    <div
      className="h-8 w-0.5 rounded-full bg-[linear-gradient(to_bottom,var(--pg-grad-a),var(--pg-grad-c))] md:h-10"
      title={t("nowPlaying")}
    />
  ) : alreadyEnded && catchupEnabled ? (
    <div className="h-8 w-0.5 rounded-full bg-slate-400/25 dark:bg-violet-100/18 md:h-10" title={t("replay")} />
  ) : (
    <div className="h-8 w-0.5 rounded-full bg-transparent md:h-10" />
  );

  const timeLabelClass = [
    "whitespace-nowrap font-semibold text-xs tabular-nums leading-tight md:text-sm",
    isNowPlaying && "text-violet-700 dark:text-violet-200",
  ].join(" ");

  return (
    <button
      type="button"
      ref={isNowPlaying ? playingRowRef : null}
      className={rowClassTokens.join(" ")}
      onClick={() => {
        if (alreadyEnded && catchupEnabled) {
          onProgramPicked(entry.beginsAt, entry.endsAt);
        } else if (onAirNow) {
          // 直播行：以"此刻"作为起止点回传（同一实例），上层据此跳到直播流当前时间
          const playFrom = new Date();
          onProgramPicked(playFrom, playFrom);
        }
      }}
    >
      <div className="relative z-10 flex items-center gap-2 p-2 md:gap-2.5 md:p-2.5">
        <div className="flex shrink-0">{stateBar}</div>

        <div className="flex w-[4.75rem] shrink-0 flex-col items-end md:w-[5.25rem]">
          <span className={timeLabelClass}>{formatClock(entry.beginsAt)}</span>
          <span className="whitespace-nowrap text-[10px] text-slate-500 tabular-nums leading-4 dark:text-slate-400 md:text-xs">
            {durationInMinutes}
            {t("minutes")}
          </span>
        </div>

        <div className="min-w-0 flex-1 overflow-hidden">
          <div className="line-clamp-2 break-words font-semibold text-sm leading-tight tracking-[0.005em] md:text-base">
            {entry.title || t("excellentProgram")}
          </div>
        </div>

        <div className="flex h-8 md:h-10 w-3 md:w-4 shrink-0 items-center justify-center">
          {onAirNow && (
            <span title={t("onAir")}>
              <Circle className="h-2.5 w-2.5 fill-current text-violet-500 drop-shadow-[0_0_5px_rgba(var(--pg-rgb),0.65)] md:h-3 md:w-3" />
            </span>
          )}
          {alreadyEnded && catchupEnabled && (
            <span title={t("replay")}>
              <History className="h-3 w-3 text-slate-400 dark:text-violet-100/45 md:h-3.5 md:w-3.5" />
            </span>
          )}
        </div>
      </div>
    </button>
  );
});

interface ProgramDayTimelineProps {
  nowPlayingProgram: EPGProgram | null;
  playingRowRef: RefObject<HTMLButtonElement | null>;
  /** 判定"已结束/播出中"的时刻；来自父组件的 deferred 时钟。 */
  asOfTime: Date;
  onProgramPicked: (programStart: Date, programEnd: Date) => void;
  locale: Locale;
  programsByDay: Map<string, EPGProgram[]>;
  catchupEnabled: boolean;
}

const ProgramDayTimeline = memo(function ProgramDayTimeline({
  nowPlayingProgram,
  playingRowRef,
  asOfTime,
  onProgramPicked,
  locale,
  programsByDay,
  catchupEnabled,
}: ProgramDayTimelineProps) {
  const t = usePlayerTranslation(locale);

  // 日期头标签：与今天相差 ±2 天内用相对说法，否则退回本地化短日期。
  // 偏移量按"day 自身当日本地零点"计算，常规情况恒为 0（总命中"今天"）；
  // 其余分支保留给跨日/DST 边界，是既有对外行为，不在此收窄语义。
  const describeDayLabel = (day: Date) => {
    const dayOffset = Math.floor(
      (day.getTime() - new Date(day.getFullYear(), day.getMonth(), day.getDate()).getTime()) / MS_PER_DAY,
    );
    switch (dayOffset) {
      case -2:
        return t("dayBeforeYesterday");
      case -1:
        return t("yesterday");
      case 0:
        return t("today");
      case 1:
        return t("tomorrow");
      default: {
        const calendarLocale = locale === "zh-Hans" || locale === "zh-Hant" ? "zh-CN" : "en-US";
        return day.toLocaleDateString(calendarLocale, { month: "short", day: "numeric" });
      }
    }
  };

  return Array.from(programsByDay.entries()).map(([dayKey, dayPrograms]) => {
    const day = new Date(dayKey);
    return (
      <div key={dayKey} className="relative">
        <div className="player-performance-epg-header sticky top-0 z-10 border-violet-950/10 border-b bg-white/85 px-3 py-1.5 backdrop-blur-xl dark:border-violet-100/10 dark:bg-slate-950/80 md:px-4 md:py-2">
          <h3 className="font-semibold text-violet-800 text-xs tracking-wide dark:text-violet-100 md:text-sm">
            {describeDayLabel(day)}
          </h3>
        </div>
        <div className="px-2 py-2">
          <div className="space-y-2">
            {dayPrograms.map((entry) => (
              <ProgramRow
                key={entry.id}
                playingRowRef={playingRowRef}
                onProgramPicked={onProgramPicked}
                alreadyEnded={entry.endsAt <= asOfTime}
                locale={locale}
                onAirNow={entry.beginsAt <= asOfTime && entry.endsAt > asOfTime}
                isNowPlaying={nowPlayingProgram?.id === entry.id}
                entry={entry}
                catchupEnabled={catchupEnabled}
              />
            ))}
          </div>
        </div>
      </div>
    );
  });
});

function EpgPanel({ channelId, epgData, onProgramSelect, locale, supportsCatchup, currentPlayingProgram }: EpgPanelProps) {
  const t = usePlayerTranslation(locale);
  const playingRowRef = useRef<HTMLButtonElement>(null);
  const [wallClock, setWallClock] = useState(() => new Date());
  // 秒跳时钟只服务着色与回看判定：走 deferred 低优先级，不抢占用户交互渲染
  const deferredWallClock = useDeferredValue(wallClock);

  useEffect(() => {
    const ticker = window.setInterval(() => setWallClock(new Date()), CLOCK_TICK_MS);
    return () => window.clearInterval(ticker);
  }, []);

  const { programsByDay, channelSchedule } = useMemo(() => {
    if (!channelId) return emptyGrouping();
    const schedule = epgData[channelId];
    if (!schedule || schedule.length === 0) return emptyGrouping();
    // channelSchedule 保留原始数组引用（不复制）：长度判定与身份稳定性都依赖它
    return { programsByDay: groupProgramsByLocalDay(schedule), channelSchedule: schedule };
  }, [channelId, epgData]);

  useLayoutEffect(() => {
    // 先排复位、再读意图：setTimeout 在宏任务里执行，晚于本次同步读取，
    // 因此读到的是本次交互留下的值（点击→skip、切台→smooth/instant）；
    // 复位只影响下一次自然触发的自动滚动，让它回到平滑居中的默认行为。
    window.setTimeout(() => {
      nextScrollBehaviorRef.current = "smooth";
    }, SCROLL_RESET_DELAY_MS);
    if (!currentPlayingProgram || !channelId || channelSchedule.length === 0) return;
    const intent = nextScrollBehaviorRef.current;
    if (intent === "skip") return;
    playingRowRef.current?.scrollIntoView({ behavior: resolveAutoScrollBehavior(intent), block: "center" });
  }, [currentPlayingProgram, channelId, channelSchedule]);

  // 点击节目后要抑制自动居中：容器马上要因换台/跳转而滚动，自动居中会和用户抢滚动位置
  const handleProgramPicked = useCallback(
    (programStart: Date, programEnd: Date) => {
      nextScrollBehaviorRef.current = "skip";
      onProgramSelect(programStart, programEnd);
    },
    [onProgramSelect],
  );

  // "无节目单"是渲染分支与滚动 effect 共用的判定：收口成派生量，避免两处条件漂移
  const isPanelEmpty = !channelId || channelSchedule.length === 0;

  if (isPanelEmpty) {
    return <div className={EMPTY_EPG_HINT_CLASS}>{t("noEpgAvailable")}</div>;
  }

  return (
    <div className="h-full overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      {/* relative 包裹层：为 sticky 日期头提供包含块，滚动时日期头贴住列表可视区顶部 */}
      <div className="relative">
        <ProgramDayTimeline
          nowPlayingProgram={currentPlayingProgram}
          playingRowRef={playingRowRef}
          asOfTime={deferredWallClock}
          onProgramPicked={handleProgramPicked}
          locale={locale}
          programsByDay={programsByDay}
          catchupEnabled={supportsCatchup}
        />
      </div>
    </div>
  );
}

export const EPGView = memo(EpgPanel);
