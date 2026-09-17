/**
 * 选中项玻璃质感装饰层。
 * 纯装饰（aria-hidden）：选中频道行上的渐变光层 + 顶部高光线，由 visible 控制淡入淡出。
 */
import { clsx } from "clsx";
import {
  PLAYER_SELECTED_COMPACT_TOP_HIGHLIGHT_CLASS,
  PLAYER_SELECTED_GLASS_LAYER_CLASS,
  PLAYER_SELECTED_TOP_HIGHLIGHT_CLASS,
} from "./classnames";

type PlayerSelectedGlassLayersProps = { compact?: boolean; visible?: boolean };

export function PlayerSelectedGlassLayers({ compact = false, visible = true }: PlayerSelectedGlassLayersProps) {
  const topHighlightClass = compact
    ? PLAYER_SELECTED_COMPACT_TOP_HIGHLIGHT_CLASS
    : PLAYER_SELECTED_TOP_HIGHLIGHT_CLASS;
  return (
    <>
      <span aria-hidden className={clsx(PLAYER_SELECTED_GLASS_LAYER_CLASS, visible && "opacity-100")} />
      <span aria-hidden className={clsx(topHighlightClass, visible && "opacity-100")} />
    </>
  );
}
