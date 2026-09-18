import { clsx } from "clsx";
import * as React from "react";

/*
 * 表格族组件：横向滚动容器 + 语义化表格结构。
 * 行分隔线用 [&_tr] 选择器下发，行自身无需感知表格上下文。
 */

const Table = React.forwardRef<HTMLTableElement, React.HTMLAttributes<HTMLTableElement>>(
  ({ className, ...rest }, ref) => (
    <div className="relative w-full overflow-auto">
      <table ref={ref} className={clsx("w-full caption-bottom text-sm", className)} {...rest} />
    </div>
  ),
);
Table.displayName = "Table";

const TableHeader = React.forwardRef<HTMLTableSectionElement, React.HTMLAttributes<HTMLTableSectionElement>>(
  ({ className, ...rest }, ref) => (
    <thead ref={ref} className={clsx("bg-muted/45", "[&_tr]:border-b [&_tr]:border-border/60", className)} {...rest} />
  ),
);
TableHeader.displayName = "TableHeader";

const TableBody = React.forwardRef<HTMLTableSectionElement, React.HTMLAttributes<HTMLTableSectionElement>>(
  ({ className, ...rest }, ref) => (
    <tbody ref={ref} className={clsx("[&_tr:last-child]:border-0", className)} {...rest} />
  ),
);
TableBody.displayName = "TableBody";

const TableFooter = React.forwardRef<HTMLTableSectionElement, React.HTMLAttributes<HTMLTableSectionElement>>(
  ({ className, ...rest }, ref) => (
    <tfoot ref={ref} className={clsx("bg-primary", "font-medium text-primary-foreground", className)} {...rest} />
  ),
);
TableFooter.displayName = "TableFooter";

const TableRow = React.forwardRef<HTMLTableRowElement, React.HTMLAttributes<HTMLTableRowElement>>(
  ({ className, ...rest }, ref) => (
    <tr
      ref={ref}
      className={clsx(
        "border-b border-border/40 transition-colors",
        "hover:bg-primary/5",
        "data-[state=selected]:bg-primary/8",
        className,
      )}
      {...rest}
    />
  ),
);
TableRow.displayName = "TableRow";

const TableHead = React.forwardRef<HTMLTableCellElement, React.ThHTMLAttributes<HTMLTableCellElement>>(
  ({ className, ...rest }, ref) => (
    <th
      ref={ref}
      className={clsx(
        "h-10 px-4 text-left align-middle",
        "text-[11px] font-semibold uppercase tracking-[0.08em]",
        "text-muted-foreground",
        className,
      )}
      {...rest}
    />
  ),
);
TableHead.displayName = "TableHead";

const TableCell = React.forwardRef<HTMLTableCellElement, React.TdHTMLAttributes<HTMLTableCellElement>>(
  ({ className, ...rest }, ref) => (
    <td ref={ref} className={clsx("p-4 align-top tabular-nums", className)} {...rest} />
  ),
);
TableCell.displayName = "TableCell";

const TableCaption = React.forwardRef<HTMLTableCaptionElement, React.HTMLAttributes<HTMLTableCaptionElement>>(
  ({ className, ...rest }, ref) => (
    <caption ref={ref} className={clsx("mt-4 text-sm text-muted-foreground", className)} {...rest} />
  ),
);
TableCaption.displayName = "TableCaption";

export { Table, TableBody, TableCaption, TableCell, TableFooter, TableHead, TableHeader, TableRow };
