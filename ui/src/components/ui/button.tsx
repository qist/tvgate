import { cva, type VariantProps } from "class-variance-authority";
import { clsx } from "clsx";
import * as React from "react";

const buttonVariants = cva(
  "inline-flex select-none items-center justify-center gap-1.5 whitespace-nowrap rounded-[var(--radius)] border border-transparent text-sm font-semibold tracking-[0.005em] transition-[color,background-color,border-color,box-shadow,transform,filter] duration-200 outline-none motion-reduce:transition-none focus-visible:ring-2 focus-visible:ring-ring/45 focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-45",
  {
    variants: {
      variant: {
        // 主按钮：轻微的纵向渐变 + 顶部内高光替代"厚阴影"，避免塑料感；悬停只提亮不位移太多
        default:
          "border-primary/30 bg-[linear-gradient(180deg,hsl(var(--primary)/0.93),hsl(var(--primary)))] text-primary-foreground shadow-[inset_0_1px_0_hsl(0_0%_100%/0.2),0_8px_20px_-12px_hsl(var(--primary)/0.8)] hover:brightness-[1.06] hover:shadow-[inset_0_1px_0_hsl(0_0%_100%/0.24),0_12px_24px_-12px_hsl(var(--primary)/0.9)] active:brightness-[0.95] motion-safe:hover:-translate-y-0.5 motion-safe:active:translate-y-0",
        destructive:
          "border-destructive/30 bg-[linear-gradient(180deg,hsl(var(--destructive)/0.93),hsl(var(--destructive)))] text-destructive-foreground shadow-[inset_0_1px_0_hsl(0_0%_100%/0.18),0_8px_20px_-12px_hsl(var(--destructive)/0.75)] hover:brightness-[1.05] active:brightness-[0.94] motion-safe:hover:-translate-y-0.5 motion-safe:active:translate-y-0",
        outline:
          "border-input/80 bg-background/60 text-foreground backdrop-blur-sm hover:border-primary/40 hover:bg-accent/80 hover:text-accent-foreground",
        secondary:
          "border-border/60 bg-secondary/80 text-secondary-foreground backdrop-blur-sm hover:border-primary/25 hover:bg-secondary",
        ghost: "text-foreground hover:bg-accent/80 hover:text-accent-foreground active:bg-accent/80",
        link: "text-primary underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 px-4 py-2",
        sm: "h-8 rounded-lg px-3 text-[13px]",
        lg: "h-10 rounded-xl px-6",
        icon: "h-9 w-9",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, ...props }, ref) => (
  <button
    className={clsx("cursor-pointer disabled:cursor-not-allowed", buttonVariants({ variant, size, className }))}
    ref={ref}
    {...props}
  />
));
Button.displayName = "Button";

export { Button, buttonVariants };
