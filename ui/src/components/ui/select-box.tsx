import { clsx } from "clsx";
import { ChevronDown } from "lucide-react";
import type { SelectHTMLAttributes } from "react";

export interface SelectBoxProps extends SelectHTMLAttributes<HTMLSelectElement> {
  containerClassName?: string;
  variant?: "default" | "sm";
}

/** 原生 select 的装饰壳：自绘下拉箭头（跟随 focus 旋转/变色），尺寸两档。 */
export function SelectBox({
  containerClassName = "min-w-[120px]",
  className,
  children,
  variant = "default",
  ...rest
}: SelectBoxProps) {
  const compact = variant === "sm";
  return (
    <div
      className={clsx(
        "relative inline-flex items-center justify-end",
        compact ? "py-0" : "py-1",
        containerClassName,
      )}
    >
      <select
        className={clsx(
          "peer w-full cursor-pointer appearance-none",
          "border border-border/50 bg-background/70 font-semibold text-foreground shadow-none backdrop-blur-sm",
          "transition-[color,background-color,border-color,box-shadow] motion-reduce:transition-none",
          "hover:border-primary/35 hover:bg-background/80",
          "focus-visible:border-primary/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/35",
          "disabled:cursor-not-allowed disabled:opacity-50",
          "dark:bg-secondary/50",
          compact ? "h-8 rounded-lg px-2.5 pr-8 text-xs" : "h-9 rounded-[var(--radius)] px-3 pr-10 text-sm",
          className,
        )}
        {...rest}
      >
        {children}
      </select>
      <ChevronDown
        className={clsx(
          "pointer-events-none absolute text-muted-foreground",
          "transition-transform duration-200 peer-focus:rotate-180 peer-focus:text-primary",
          compact ? "right-2.5 h-3.5 w-3.5" : "right-3 h-4 w-4",
        )}
      />
    </div>
  );
}
