/**
 * 频道列表单项。
 * 平面行（TV 播放器风格）：频道号 / 名称（+ 当前节目）/ 回看徽标 / logo；行底盘与选中强调
 * 全部由 CSS（.player-performance-list-surface-*）给出，组件里不再叠卡片与光层。
 */
import { History } from "lucide-react";
import { forwardRef, memo, useCallback } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { Locale } from "../../lib/locale";
import type { Channel } from "../../types/player";
import {
  PLAYER_CHANNEL_LIST_ITEM_CLASS,
  PLAYER_LIST_SURFACE_BASE_CLASS,
  PLAYER_LIST_SURFACE_DEFAULT_CLASS,
  PLAYER_LIST_SURFACE_HOVER_CLASS,
  PLAYER_LIST_SURFACE_SELECTED_CLASS,
} from "./classnames";

interface ChannelListItemProps {
  channel: Channel;
  isCurrentChannel: boolean;
  handleChannelClick: (channel: Channel) => void;
  /** 点击行内"当前节目"（不换台）：用于展开节目单栏。 */
  onProgramClick?: (channel: Channel) => void;
  locale: Locale;
  currentProgram?: string;
}

const ChannelListItemComponent = forwardRef<HTMLButtonElement, ChannelListItemProps>(
  ({ channel, isCurrentChannel, handleChannelClick, onProgramClick, locale, currentProgram }, ref) => {
    const t = usePlayerTranslation(locale);
    const groupLabel = channel.groups.join(" / ");

    const handleClick = useCallback(() => {
      handleChannelClick(channel);
    }, [handleChannelClick, channel]);

    const supportsCatchup = channel.sources.some((source) => source.catchup && source.catchupSource);

    return (
      <button
        type="button"
        ref={ref}
        data-id={channel.id}
        className={[
          PLAYER_LIST_SURFACE_BASE_CLASS,
          PLAYER_CHANNEL_LIST_ITEM_CLASS,
          "group flex cursor-pointer touch-manipulation items-center gap-2.5 py-1.5 pr-2 pl-3.5 focus-visible:outline-none md:gap-3",
          isCurrentChannel ? PLAYER_LIST_SURFACE_SELECTED_CLASS : PLAYER_LIST_SURFACE_DEFAULT_CLASS,
          !isCurrentChannel && PLAYER_LIST_SURFACE_HOVER_CLASS,
        ].join(" ")}
        onClick={handleClick}
      >
        <span
          className={[
            "relative z-10 w-6 shrink-0 text-right font-medium text-[11px] tabular-nums leading-none transition-colors duration-200 md:text-xs",
            isCurrentChannel ? "text-violet-600 dark:text-violet-200" : "text-slate-400 dark:text-slate-500",
          ].join(" ")}
        >
          {channel.number ?? ""}
        </span>
        <div className="relative z-10 min-w-0 flex-1 overflow-hidden">
          <div className="flex items-center gap-1.5">
            <div
              className={[
                "min-w-0 flex-1 truncate leading-tight",
                isCurrentChannel
                  ? "font-semibold text-[15px] text-slate-900 md:text-base dark:text-violet-50"
                  : "font-medium text-sm text-slate-700 md:text-[15px] dark:text-slate-200",
              ].join(" ")}
              title={channel.name}
            >
              {channel.name}
            </div>
            {supportsCatchup && (
              <span title={t("catchupSupported")}>
                <History className="h-3 w-3 shrink-0 text-slate-400 dark:text-slate-500 md:h-3.5 md:w-3.5" />
              </span>
            )}
          </div>
          <div className="mt-0.5 truncate text-[10px] leading-4 text-slate-500 dark:text-slate-400/80 md:text-[11px]">
            {groupLabel}
            {currentProgram && (
              <>
                {groupLabel && <span className="mx-1 opacity-60">·</span>}
                {onProgramClick ? (
                  <span
                    role="button"
                    tabIndex={-1}
                    title={t("programGuide")}
                    onClick={(event) => {
                      event.stopPropagation();
                      onProgramClick(channel);
                    }}
                    className="cursor-pointer underline-offset-2 hover:text-violet-700 hover:underline dark:hover:text-violet-200"
                  >
                    {currentProgram}
                  </span>
                ) : (
                  <span>{currentProgram}</span>
                )}
              </>
            )}
          </div>
        </div>
        {channel.logo && (
          /* 台标用一块极简深色底（无边框/投影）：台标多为白色透明 PNG，浅色面板上否则看不见 */
          <div className="relative z-10 flex h-6 w-10 shrink-0 items-center justify-center overflow-hidden rounded bg-slate-900/85 px-0.5 md:h-7 md:w-14 md:px-1 dark:bg-slate-900/60">
            <img
              src={channel.logo}
              alt={channel.name}
              referrerPolicy="no-referrer"
              className="h-full w-full object-contain opacity-95"
              onError={(event) => {
                (event.target as HTMLImageElement).style.display = "none";
              }}
            />
          </div>
        )}
      </button>
    );
  },
);

export const ChannelListItem = memo(ChannelListItemComponent);
