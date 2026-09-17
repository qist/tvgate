import { clsx } from "clsx";
import * as React from "react";

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {}

const Input = React.forwardRef<HTMLInputElement, InputProps>(({ className, type, ...props }, ref) => (
  <input
    type={type}
    ref={ref}
    className={clsx(
      "flex h-9 w-full rounded-[var(--radius)] border border-input/70 bg-background/70 px-3 text-sm text-foreground backdrop-blur-sm",
      "transition-[color,background-color,border-color,box-shadow] duration-200 motion-reduce:transition-none",
      "placeholder:text-muted-foreground/80 hover:border-primary/30",
      "focus-visible:border-primary/45 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/35",
      "disabled:cursor-not-allowed disabled:opacity-50 dark:bg-secondary/40",
      className,
    )}
    {...props}
  />
));
Input.displayName = "Input";

export { Input };