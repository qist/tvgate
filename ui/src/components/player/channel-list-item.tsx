/**
 * 频道列表的单行条目（TV 播放器扁平风格）；紧凑布局下同一份数据可渲染成"卡片"
 * （台标在上、文字在下，layout="card"）。
 * 底色 / 分隔线 / 选中强调全部交给全局 CSS（.player-performance-list-surface-* 行式、
 * .player-performance-channel-card-* 卡片式）统一给出，这里只负责内容排布：
 * 频道号、台标、频道名（+ 当前节目）与回看/线路标识。
 * 被频道浏览（三栏 = 行）与频道列表（移动端两列网格 = 卡片）两个面板复用；
 * ref 指向"正在播放"的那一条，供滚动定位。
 */
import { History } from "lucide-react";
import { forwardRef, memo, useCallback } from "react";
import { usePlayerTranslation } from "../../hooks/use-player-translation";
import type { Locale } from "../../lib/locale";
import type { Channel } from "../../types/player";
import { ChannelLogo } from "./channel-logo";
import {
  PLAYER_CHANNEL_CARD_DEFAULT_CLASS,
  PLAYER_CHANNEL_CARD_HOVER_CLASS,
  PLAYER_CHANNEL_CARD_ITEM_CLASS,
  PLAYER_CHANNEL_CARD_SELECTED_CLASS,
  PLAYER_CHANNEL_LIST_ITEM_CLASS,
  PLAYER_LIST_SURFACE_BASE_CLASS,
  PLAYER_LIST_SURFACE_DEFAULT_CLASS,
  PLAYER_LIST_SURFACE_HOVER_CLASS,
  PLAYER_LIST_SURFACE_SELECTED_CLASS,
} from "./classnames";

/** 排布：row = 一行式（三栏频道浏览）；card = 卡片式（移动端两列网格）。 */
type ChannelRowLayout = "row" | "card";

/* ---------------- 无台标时的首字徽章 ----------------
 * 订阅普遍不带 tvgLogo，列表整片纯文字、辨识度全靠读字——观感平淡的主因。
 * 无台标时渲染一个「频道名首字符」的柔和渐变徽章占台标位：色相对名稳定
 * （同名频道永远同色），五组低饱和渐变都与 --pg 主题同族，不与选中态抢视觉。
 */

/** 低饱和渐变族（浅色深字 / 深色浅字），与紫罗兰主题同族、互不刺眼。 */
const INITIAL_TONES = [
  "from-violet-500/22 to-purple-500/16 text-violet-700 dark:text-violet-100",
  "from-sky-500/20 to-indigo-500/16 text-sky-700 dark:text-sky-100",
  "from-fuchsia-500/20 to-pink-500/14 text-fuchsia-700 dark:text-fuchsia-100",
  "from-teal-500/18 to-emerald-500/14 text-teal-700 dark:text-teal-100",
  "from-amber-500/22 to-orange-500/14 text-amber-700 dark:text-amber-100",
];

function initialOf(name: string): string {
  const trimmed = name.trim().toUpperCase();
  // 品牌前缀剥离：CCTV/CGTN 这类台，数字后缀才是辨识主体（CCTV1→1、CCTV5+→5+、
  // CCTV4欧洲→4欧），否则 CCTV 霸屏的列表里一整列 "C" 毫无区分度。
  const branded = /^(CCTV|CGTN|CETV|CHC|IPTV|BRT)\s*[-–—]?\s*(\d\S*)$/.exec(trimmed);
  if (branded) return branded[2].slice(0, 2);
  // 其余取首个「有效字符」：中文取首字（凤凰卫视→凤），西文取首字母。
  const first = trimmed.match(/[\u4e00-\u9fffA-Z0-9]/);
  return first?.[0] ?? "?";
}

function toneOf(name: string): string {
  // 名字哈希 → 稳定选色：同一频道刷新/换组后颜色不变，列表整体色彩分布均匀。
  let hash = 0;
  for (let i = 0; i < name.length; i++) hash = (hash * 31 + name.charCodeAt(i)) >>> 0;
  return INITIAL_TONES[hash % INITIAL_TONES.length];
}

function ChannelInitialBadge({ name, large }: { name: string; large?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={[
        "relative z-10 flex shrink-0 items-center justify-center rounded-md bg-gradient-to-br font-semibold ring-1 ring-slate-900/5 dark:ring-white/10",
        large ? "h-10 w-14 rounded-lg text-lg" : "h-7 w-9 text-sm",
        toneOf(name),
      ].join(" ")}
    >
      {initialOf(name)}
    </span>
  );
}

interface ChannelRowProps {
  channel: Channel;
  isCurrentChannel: boolean;
  handleChannelClick: (channel: Channel) => void;
  /** 点击行内"当前节目"（不换台）：用于展开节目单栏。 */
  onProgramClick?: (channel: Channel) => void;
  locale: Locale;
  currentProgram?: string;
  /** 缺省 row：保持三栏浏览的既有排布。 */
  layout?: ChannelRowLayout;
}

const ChannelRow = forwardRef<HTMLButtonElement, ChannelRowProps>(
  ({ channel, isCurrentChannel, handleChannelClick, onProgramClick, locale, currentProgram, layout = "row" }, ref) => {
    const t = usePlayerTranslation(locale);
    const isCard = layout === "card";
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

    /* 台标：不垫底色，直接吃 PNG 自身的透明度（多数源是白字透明图，深色面板上最好看）；
       浅色面板上靠一层很淡的深色描边光晕保可读（drop-shadow 只作用在不透明像素，不会画方块边）。
       卡片式的框吃满卡宽（object-contain 下由高度定尺寸），所以方图/长条图都比行式更大；
       行式的框刻意做成 3:2：方图（凤凰卫视这类）被高度卡住、长条图（CCTV 300×118）被宽度卡住，
       两个方向同时放，两类图都能吃到。
       lazy 是因为手机端一行上千个频道，只在可视区附近才真正取图（否则首屏上千个请求
       挤占连接池，排队失败还会被误判成加载失败）；单图失败由 ChannelLogo 内部自愈。 */
    const logo = channel.logo ? (
      <ChannelLogo
        src={channel.logo}
        alt={channel.name}
        lazy
        className={
          isCard
            ? // 固定 44×80 的台标位并居中：方图/长条图各占各的，但视觉体重一致
              // （之前吃满卡宽时，CCTV 长条横向铺开到 100px、凤凰卫视只有 40px，一列里忽大忽小最显乱）
              "relative z-10 mx-auto flex h-11 w-20 shrink-0 items-center justify-center"
            : "relative z-10 flex h-8 w-12 shrink-0 items-center justify-center px-0.5 md:h-9 md:w-14 md:px-1"
        }
        imgClassName="h-full w-full object-contain opacity-95 [filter:drop-shadow(0_1px_1px_rgba(2,6,23,0.45))_drop-shadow(0_0_2px_rgba(2,6,23,0.28))]"
      />
    ) : (
      // 无台标：首字徽章占位（尺寸对齐台标位），列表不再是一片纯文字
      isCard ? (
        <span className="relative z-10 mx-auto flex h-11 w-20 shrink-0 items-center justify-center">
          <ChannelInitialBadge name={channel.name} large />
        </span>
      ) : (
        <span className="relative z-10 flex h-8 w-12 shrink-0 items-center justify-center px-0.5 md:h-9 md:w-14 md:px-1">
          <ChannelInitialBadge name={channel.name} />
        </span>
      )
    );

    // 状态徽标（回看 / 线路数）：行式跟在名字后，卡片式钉在右上角。
    const statusBadges = (
      <>
        {isCurrentChannel && (
          // 「正在播放」脉冲点：红色在任意主题色下都表义"直播中"，与选中态主色不混淆
          <span className="relative flex h-2 w-2 shrink-0">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-rose-400 opacity-60 motion-reduce:hidden" />
            <span className="relative inline-flex h-2 w-2 rounded-full bg-rose-500" />
          </span>
        )}
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
      </>
    );

    // 副行：分组 +（可选）正在播出的节目；点节目只预览并展开节目单，不换台。
    const metaLine = (
      <div
        className={
          isCard
            ? "mt-0.5 w-full truncate text-center text-[10px] leading-4 text-slate-500 dark:text-slate-400/80"
            : "mt-0.5 truncate text-[10px] leading-4 text-slate-500 dark:text-slate-400/80 md:text-[11px]"
        }
      >
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
    );

    // 名字配色只由"是否当前频道"决定，两种排布共用（卡片式额外 line-clamp 两行）。
    const nameToneClass = isCurrentChannel
      ? "font-semibold text-[15px] text-slate-900 md:text-base dark:text-violet-50"
      : "font-medium text-sm text-slate-700 md:text-[15px] dark:text-slate-200";

    // 类名 token 是逐字契约：Array.join 不过滤 false，未命中分支会原样留下 "false" 占位——
    // 别改成 clsx 之类的假值剔除写法，那会改变最终进入 DOM 的 class 串。
    const itemClass = isCard ? PLAYER_CHANNEL_CARD_ITEM_CLASS : PLAYER_CHANNEL_LIST_ITEM_CLASS;
    const surfaceClass = isCurrentChannel
      ? isCard
        ? PLAYER_CHANNEL_CARD_SELECTED_CLASS
        : PLAYER_LIST_SURFACE_SELECTED_CLASS
      : isCard
        ? PLAYER_CHANNEL_CARD_DEFAULT_CLASS
        : PLAYER_LIST_SURFACE_DEFAULT_CLASS;
    const hoverClass = !isCurrentChannel && (isCard ? PLAYER_CHANNEL_CARD_HOVER_CLASS : PLAYER_LIST_SURFACE_HOVER_CLASS);

    return (
      <button
        type="button"
        ref={ref}
        data-id={channel.id}
        onClick={handleRowClick}
        className={[
          PLAYER_LIST_SURFACE_BASE_CLASS,
          itemClass,
          isCard
            ? "flex cursor-pointer touch-manipulation flex-col rounded-xl px-2 pt-1.5 pb-2 focus-visible:outline-none"
            : "group flex cursor-pointer touch-manipulation items-center gap-2.5 py-1.5 pr-2 pl-3.5 focus-visible:outline-none md:gap-3",
          surfaceClass,
          hoverClass,
        ].join(" ")}
      >
        {isCard ? (
          <>
            {/* 号位与徽标钉在角上，名字独占整行：两列网格里这是"不再被挤断"的关键 */}
            <span className="absolute top-1.5 left-2 z-10 font-medium text-[10px] tabular-nums leading-none text-slate-400 transition-colors duration-200 dark:text-slate-500">
              {channel.number ?? ""}
            </span>
            {(hasTimeshiftSource || lineCount > 1) && (
              <span className="absolute top-1.5 right-1.5 z-10 flex items-center gap-1">{statusBadges}</span>
            )}
            {logo}
            {/* 贴片式：台标 + 名字 + 副行全部居中，只有角上的号位/徽标靠边——
                锚点统一才不会"号在左、图在中、字在左"地各说各话 */}
            <div className={["mt-1 line-clamp-2 w-full text-center leading-tight", nameToneClass].join(" ")}>
              {channel.name}
            </div>
            {metaLine}
          </>
        ) : (
          <>
            <span
              className={[
                "relative z-10 w-6 shrink-0 text-right font-medium text-[11px] tabular-nums leading-none transition-colors duration-200 md:text-xs",
                isCurrentChannel ? "text-violet-600 dark:text-violet-200" : "text-slate-400 dark:text-slate-500",
              ].join(" ")}
            >
              {channel.number ?? ""}
            </span>
            {logo}
            <div className="relative z-10 min-w-0 flex-1 overflow-hidden">
              <div className="flex items-center gap-1.5">
                <div title={channel.name} className={["min-w-0 flex-1 truncate leading-tight", nameToneClass].join(" ")}>
                  {channel.name}
                </div>
                {statusBadges}
              </div>
              {metaLine}
            </div>
          </>
        )}
      </button>
    );
  },
);

export const ChannelListItem = memo(ChannelRow);
