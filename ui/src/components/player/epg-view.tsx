/**
 * EPG 节目单视图（clean-room 重写）。
 * 按日期分组展示某频道的节目；高亮正在播放 / 可回看节目；点击节目回调（直播中则点 "现在"）。
 * 当前/正在播放节目变化时自动滚动居中；点击节目后跳过的滚动由 nextScrollBehaviorRef 控制。
 */
import { Circle, History } from "lucide-react";
import { memo, type RefObject, useCallback, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { EPGData } from "../../lib/epg-parser";
import type { Locale } from "../../lib/locale";
import type { EPGProgram } from "../../types/player";
import {
  PLAYER_EPG_LIST_ITEM_CLASS,
  PLAYER_LIST_SURFACE_BASE_CLASS,
  PLAYER_LIST_SURFACE_DEFAULT_CLASS,
  PLAYER_LIST_SURFACE_HOVER_CLASS,
  PLAYER_LIST_SURFACE_SELECTED_CLASS,
} from "./classnames";

interface EPGViewProps {
  channelId: string | null | undefined;
  epgData: EPGData;
  onProgramSelect: (programStart: Date, programEnd: Date) => void;
  locale: Locale;
  supportsCatchup: boolean;
  currentPlayingProgram: EPGProgram | null;
}

export const nextScrollBehaviorRef: RefObject<"smooth" | "instant" | "skip"> = { current: "instant" };

interface EPGProgramItemProps {
  currentProgramRef: RefObject<HTMLButtonElement | null>;
  handleProgramClick: (programStart: Date, programEnd: Date) => void;
  isPast: boolean;
  locale: Locale;
  onAir: boolean;
  playing: boolean;
  program: EPGProgram;
  supportsCatchup: boolean;
}

const EPGProgramItem = memo(function EPGProgramItem({
  currentProgramRef,
  handleProgramClick,
  isPast,
  locale,
  onAir,
  playing,
  program,
  supportsCatchup,
}: EPGProgramItemProps) {
  const t = usePlayerTranslation(locale);
  const formatTime = (date: Date) => date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  const durationMinutes = Math.round((program.end.getTime() - program.start.getTime()) / 60000);

  const clickable = (isPast && supportsCatchup) || onAir;

  return (
    <button
      type="button"
      ref={playing ? currentProgramRef : null}
      className={[
        PLAYER_LIST_SURFACE_BASE_CLASS,
        PLAYER_EPG_LIST_ITEM_CLASS,
        "flex w-full text-left",
        playing ? PLAYER_LIST_SURFACE_SELECTED_CLASS : PLAYER_LIST_SURFACE_DEFAULT_CLASS,
        clickable && !playing && PLAYER_LIST_SURFACE_HOVER_CLASS,
        clickable && "cursor-pointer",
      ].join(" ")}
      onClick={() => {
        if (isPast && supportsCatchup) {
          handleProgramClick(program.start, program.end);
        } else if (onAir) {
          const now = new Date();
          handleProgramClick(now, now);
        }
      }}
    >
      <div className="relative z-10 flex items-center gap-2 p-2 md:gap-2.5 md:p-2.5">
        <div className="flex shrink-0">
          {playing ? (
            <div
              className="h-8 w-0.5 rounded-full bg-[linear-gradient(to_bottom,var(--pg-grad-a),var(--pg-grad-c))] md:h-10"
              title={t("nowPlaying")}
            />
          ) : isPast && supportsCatchup ? (
            <div className="h-8 w-0.5 rounded-full bg-slate-400/25 dark:bg-violet-100/18 md:h-10" title={t("replay")} />
          ) : (
            <div className="h-8 w-0.5 rounded-full bg-transparent md:h-10" />
          )}
        </div>

        <div className="flex w-[4.75rem] shrink-0 flex-col items-end md:w-[5.25rem]">
          <span
            className={[
              "whitespace-nowrap font-semibold text-xs tabular-nums leading-tight md:text-sm",
              playing && "text-violet-700 dark:text-violet-200",
            ].join(" ")}
          >
            {formatTime(program.start)}
          </span>
          <span className="whitespace-nowrap text-[10px] text-slate-500 tabular-nums leading-4 dark:text-slate-400 md:text-xs">
            {durationMinutes}
            {t("minutes")}
          </span>
        </div>

        <div className="min-w-0 flex-1 overflow-hidden">
          <div className="line-clamp-2 break-words font-semibold text-sm leading-tight tracking-[0.005em] md:text-base">
            {program.title || t("excellentProgram")}
          </div>
        </div>

        <div className="flex h-8 md:h-10 w-3 md:w-4 shrink-0 items-center justify-center">
          {onAir && (
            <span title={t("onAir")}>
              <Circle className="h-2.5 w-2.5 fill-current text-violet-500 drop-shadow-[0_0_5px_rgba(var(--pg-rgb),0.65)] md:h-3 md:w-3" />
            </span>
          )}
          {isPast && supportsCatchup && (
            <span title={t("replay")}>
              <History className="h-3 w-3 text-slate-400 dark:text-violet-100/45 md:h-3.5 md:w-3.5" />
            </span>
          )}
        </div>
      </div>
    </button>
  );
});

interface EPGProgramListProps {
  currentPlayingProgram: EPGProgram | null;
  currentProgramRef: RefObject<HTMLButtonElement | null>;
  currentTime: Date;
  handleProgramClick: (programStart: Date, programEnd: Date) => void;
  locale: Locale;
  programsByDate: Map<string, EPGProgram[]>;
  supportsCatchup: boolean;
}

const EPGProgramList = memo(function EPGProgramList({
  currentPlayingProgram,
  currentProgramRef,
  currentTime,
  handleProgramClick,
  locale,
  programsByDate,
  supportsCatchup,
}: EPGProgramListProps) {
  const t = usePlayerTranslation(locale);

  const formatRelativeDate = (date: Date) => {
    const today = new Date(date.getFullYear(), date.getMonth(), date.getDate());
    const daysDiff = Math.floor((date.getTime() - today.getTime()) / (1000 * 60 * 60 * 24));
    switch (daysDiff) {
      case 0:
        return t("today");
      case -1:
        return t("yesterday");
      case -2:
        return t("dayBeforeYesterday");
      case 1:
        return t("tomorrow");
      default:
        return date.toLocaleDateString(locale === "zh-Hans" || locale === "zh-Hant" ? "zh-CN" : "en-US", {
          month: "short",
          day: "numeric",
        });
    }
  };

  return Array.from(programsByDate.entries()).map(([dateKey, programs]) => {
    const date = new Date(dateKey);
    return (
      <div key={dateKey} className="relative">
        <div className="player-performance-epg-header sticky top-0 z-10 border-violet-950/10 border-b bg-white/85 px-3 py-1.5 backdrop-blur-xl dark:border-violet-100/10 dark:bg-slate-950/80 md:px-4 md:py-2">
          <h3 className="font-semibold text-violet-800 text-xs tracking-wide dark:text-violet-100 md:text-sm">
            {formatRelativeDate(date)}
          </h3>
        </div>
        <div className="px-2 py-2">
          <div className="space-y-2">
            {programs.map((program) => (
              <EPGProgramItem
                key={program.id}
                currentProgramRef={currentProgramRef}
                handleProgramClick={handleProgramClick}
                isPast={program.end <= currentTime}
                locale={locale}
                onAir={program.start <= currentTime && program.end > currentTime}
                playing={currentPlayingProgram?.id === program.id}
                program={program}
                supportsCatchup={supportsCatchup}
              />
            ))}
          </div>
        </div>
      </div>
    );
  });
});

function EPGViewComponent({ channelId, epgData, onProgramSelect, locale, supportsCatchup, currentPlayingProgram }: EPGViewProps) {
  const t = usePlayerTranslation(locale);
  const currentProgramRef = useRef<HTMLButtonElement>(null);
  const [currentTime, setCurrentTime] = useState(() => new Date());
  const deferredCurrentTime = useDeferredValue(currentTime);

  useEffect(() => {
    const interval = window.setInterval(() => setCurrentTime(new Date()), 1000);
    return () => window.clearInterval(interval);
  }, []);

  const { programsByDate, channelPrograms } = useMemo(() => {
    if (!channelId) return { programsByDate: new Map<string, EPGProgram[]>(), channelPrograms: [] as EPGProgram[] };
    const programs = epgData[channelId];
    if (!programs || programs.length === 0) return { programsByDate: new Map<string, EPGProgram[]>(), channelPrograms: [] };

    const grouped = new Map<string, EPGProgram[]>();
    for (const program of programs) {
      const dateKey = new Date(program.start.getFullYear(), program.start.getMonth(), program.start.getDate()).toISOString();
      const bucket = grouped.get(dateKey);
      if (bucket) bucket.push(program);
      else grouped.set(dateKey, [program]);
    }
    return { programsByDate: grouped, channelPrograms: programs };
  }, [channelId, epgData]);

  useLayoutEffect(() => {
    window.setTimeout(() => {
      nextScrollBehaviorRef.current = "smooth";
    }, 0);
    if (!currentPlayingProgram || !channelId || !channelPrograms.length) return;
    const requested = nextScrollBehaviorRef.current;
    if (requested === "skip") return;
    const behavior =
      requested === "smooth" && window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : requested;
    currentProgramRef.current?.scrollIntoView({ behavior, block: "center" });
  }, [currentPlayingProgram, channelId, channelPrograms]);

  const handleProgramClick = useCallback(
    (programStart: Date, programEnd: Date) => {
      nextScrollBehaviorRef.current = "skip";
      onProgramSelect(programStart, programEnd);
    },
    [onProgramSelect],
  );

  if (!channelId || channelPrograms.length === 0) {
    return (
      <div className="flex h-full items-center justify-center bg-transparent px-6 text-center text-slate-500 text-sm leading-6 dark:text-slate-400">
        {t("noEpgAvailable")}
      </div>
    );
  }

  return (
    <div className="h-full overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="relative">
        <EPGProgramList
          currentPlayingProgram={currentPlayingProgram}
          currentProgramRef={currentProgramRef}
          currentTime={deferredCurrentTime}
          handleProgramClick={handleProgramClick}
          locale={locale}
          programsByDate={programsByDate}
          supportsCatchup={supportsCatchup}
        />
      </div>
    </div>
  );
}

export const EPGView = memo(EPGViewComponent);
