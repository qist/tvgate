/**
 * 选中行的玻璃质感装饰。
 *
 * 为什么是两个 span 而不是一个：渐变光层要铺满整行，顶部高光只贴一条边，
 * 视觉上分层叠放，DOM 上也得是两个独立元素；且两片默认 opacity-0，
 * 靠 visible 切换 opacity 类来淡入淡出，而不是条件卸载——卸载会让 300ms 过渡直接消失。
 * 纯装饰层必须 aria-hidden，避免被读屏软件当成内容朗读。
 */
import { clsx } from "clsx";
import {
  PLAYER_SELECTED_COMPACT_TOP_HIGHLIGHT_CLASS,
  PLAYER_SELECTED_GLASS_LAYER_CLASS,
  PLAYER_SELECTED_TOP_HIGHLIGHT_CLASS,
} from "./classnames";

type PlayerSelectedGlassLayersProps = { compact?: boolean; visible?: boolean };

export function PlayerSelectedGlassLayers(props: PlayerSelectedGlassLayersProps) {
  const { compact = false, visible = true } = props;

  const topHighlightClassName = compact
    ? PLAYER_SELECTED_COMPACT_TOP_HIGHLIGHT_CLASS
    : PLAYER_SELECTED_TOP_HIGHLIGHT_CLASS;
  // clsx 会丢弃 false 段：不点亮时只留下各层自带的 opacity-0 初始态
  const litClassName = visible && "opacity-100";

  return (
    <>
      <span aria-hidden className={clsx(PLAYER_SELECTED_GLASS_LAYER_CLASS, litClassName)} />
      <span aria-hidden className={clsx(topHighlightClassName, litClassName)} />
    </>
  );
}
