import { cva, type VariantProps } from "class-variance-authority";
import { clsx } from "clsx";
import type * as React from "react";

const badgeVariants = cva(
  "inline-flex items-center whitespace-nowrap rounded-full border border-transparent transition-[color,background-color,border-color,box-shadow] motion-reduce:transition-none focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2",
  {
    variants: {
      variant: {
        default:
          // 文字色走 --primary-text（随界面风格变化，浅/深模式各自可读），不再写死紫色
          "border-primary/25 bg-primary/12 text-[hsl(var(--primary-text))] hover:bg-primary/18",
        secondary: "border-border/70 bg-secondary/80 text-secondary-foreground hover:bg-secondary",
        destructive:
          "border-destructive/25 bg-destructive/12 text-[hsl(354_76%_34%)] hover:bg-destructive/18 dark:text-[hsl(350_100%_88%)]",
        outline: "border-border/65 bg-background/35 text-foreground backdrop-blur-sm",
      },
      size: {
        default: "px-2.5 py-1 text-[11px] font-semibold tracking-wide shadow-[0_4px_12px_-10px_rgba(15,23,42,0.35)]",
        compact: "h-5 px-1.5 text-[9px] leading-none font-medium tracking-normal shadow-none md:text-[10px]",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

export interface BadgeProps extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, size, ...props }: BadgeProps) {
  return <div className={clsx(badgeVariants({ variant, size }), className)} {...props} />;
}

export { Badge, badgeVariants };
