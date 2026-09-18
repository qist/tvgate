/**
 * 频道列表的单行条目（TV 播放器扁平风格）。
 * 行底色 / 分隔线 / 选中强调全部交给全局 CSS（.player-performance-list-surface-*）统一给出，
 * 这里只负责内容排布：频道号、台标、频道名（+ 当前节目）与回看标识。
 * 被频道浏览与频道列表两个面板复用；ref 指向"正在播放"的行，供滚动定位。
 */
import { History } from "lucide-react";
import { forwardRef, memo, useCallback } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { Locale } from "../../lib/locale";
import type { Channel } from "../../types/player";
import { ChannelLogo } from "./channel-logo";
import {
  PLAYER_CHANNEL_LIST_ITEM_CLASS,
  PLAYER_LIST_SURFACE_BASE_CLASS,
  PLAYER_LIST_SURFACE_DEFAULT_CLASS,
  PLAYER_LIST_SURFACE_HOVER_CLASS,
  PLAYER_LIST_SURFACE_SELECTED_CLASS,
} from "./classnames";

interface ChannelRowProps {
  channel: Channel;
  isCurrentChannel: boolean;
  handleChannelClick: (channel: Channel) => void;
  /** 点击行内"当前节目"（不换台）：用于展开节目单栏。 */
  onProgramClick?: (channel: Channel) => void;
  locale: Locale;
  currentProgram?: string;
}

const ChannelRow = forwardRef<HTMLButtonElement, ChannelRowProps>(
  ({ channel, isCurrentChannel, handleChannelClick, onProgramClick, locale, currentProgram }, ref) => {
    const t = usePlayerTranslation(locale);
    // 第二行的分组摘要：直接以 " / " 连接，不改写 Channel 数据本身。
    const groupSummary = channel.groups.join(" / ");

    // 行点击转交给父层；依赖 channel 是为了让换台后的新行拿到最新条目。
    const handleRowClick = useCallback(() => {
      handleChannelClick(channel);
    }, [handleChannelClick, channel]);

    // 回看能力逐源探测：任一源同时声明时移类型与时移模板才亮标。
    const hasTimeshiftSource = channel.sources.some((source) => source.timeshift && source.timeshiftTemplate);
    // 多线路（组内聚合同名频道产出）：行内给出 ×N 计数，详情进播放页换源菜单看。
    const lineCount = channel.sources.length;

    return (
      <button
        type="button"
        ref={ref}
        data-id={channel.id}
        onClick={handleRowClick}
        className={[
          PLAYER_LIST_SURFACE_BASE_CLASS,
          PLAYER_CHANNEL_LIST_ITEM_CLASS,
          "group flex cursor-pointer touch-manipulation items-center gap-2.5 py-1.5 pr-2 pl-3.5 focus-visible:outline-none md:gap-3",
          isCurrentChannel ? PLAYER_LIST_SURFACE_SELECTED_CLASS : PLAYER_LIST_SURFACE_DEFAULT_CLASS,
          // 保持原数组 join 形态：条件为假时 false 会以字面量落入类串，这里刻意不清洗，
          // 以保证最终 className 字符串与重写前逐字符一致。
          !isCurrentChannel && PLAYER_LIST_SURFACE_HOVER_CLASS,
        ].join(" ")}
      >
        <span
          className={[
            "relative z-10 w-6 shrink-0 text-right font-medium text-[11px] tabular-nums leading-none transition-colors duration-200 md:text-xs",
            isCurrentChannel ? "text-violet-600 dark:text-violet-200" : "text-slate-400 dark:text-slate-500",
          ].join(" ")}
        >
          {channel.number ?? ""}
        </span>
        {channel.logo && (
          /* 台标垫一块近黑底色（无边框/投影）：台标资源多为白色透明 PNG，
             直接放在浅色面板上会"隐形"。lazy 是因为手机端一行上千个频道，
             只在可视区附近才真正取图（否则首屏上千个请求挤占连接池，排队失败还会被
             误判成加载失败）；单图失败由 ChannelLogo 内部自愈，见 channel-logo.tsx。 */
          <ChannelLogo
            src={channel.logo}
            alt={channel.name}
            lazy
            className="relative z-10 flex h-6 w-10 shrink-0 items-center justify-center overflow-hidden rounded bg-slate-900/85 px-0.5 md:h-7 md:w-14 md:px-1 dark:bg-slate-900/60"
            imgClassName="h-full w-full object-contain opacity-95"
          />
        )}
        <div className="relative z-10 min-w-0 flex-1 overflow-hidden">
          <div className="flex items-center gap-1.5">
            <div
              title={channel.name}
              className={[
                "min-w-0 flex-1 truncate leading-tight",
                isCurrentChannel
                  ? "font-semibold text-[15px] text-slate-900 md:text-base dark:text-violet-50"
                  : "font-medium text-sm text-slate-700 md:text-[15px] dark:text-slate-200",
              ].join(" ")}
            >
              {channel.name}
            </div>
            {hasTimeshiftSource && (
              // 回看徽标只靠 title 提示、不占文字位：避免挤压频道名的可用宽度。
              <span title={t("catchupSupported")}>
                <History className="h-3 w-3 shrink-0 text-slate-400 dark:text-slate-500 md:h-3.5 md:w-3.5" />
              </span>
            )}
            {lineCount > 1 && (
              // 线路数徽标：同样只靠 title 提示，进播放页后的换源菜单才是完整列表。
              <span
                title={`${t("source")} ×${lineCount}`}
                className="shrink-0 rounded bg-slate-400/15 px-1 py-px text-[10px] font-medium leading-3.5 text-slate-500 tabular-nums dark:bg-slate-400/10 dark:text-slate-400"
              >
                ×{lineCount}
              </span>
            )}
          </div>
          <div className="mt-0.5 truncate text-[10px] leading-4 text-slate-500 dark:text-slate-400/80 md:text-[11px]">
            {groupSummary}
            {currentProgram && (
              <>
                {groupSummary && <span className="mx-1 opacity-60">·</span>}
                {onProgramClick ? (
                  // stopPropagation：这里只做"预览 + 展开节目单"，不能触发行点击换台；
                  // tabIndex=-1 让它退出 Tab / 遥控器焦点序，避免打断方向键导航。
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
      </button>
    );
  },
);

export const ChannelListItem = memo(ChannelRow);
