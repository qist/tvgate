import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";

/** 通用确认弹窗（默认删除确认样式）：卡片删除/危险操作前确认，防止误点 */
export function ConfirmDialog({
  title,
  description,
  confirmText = "删除",
  variant = "destructive",
  onConfirm,
  onClose,
}: {
  title: string;
  description?: string;
  confirmText?: string;
  variant?: "default" | "destructive";
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div
        className="w-full max-w-sm rounded-[var(--radius-lg)] border bg-card p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-1 flex items-center gap-2">
          <AlertTriangle className="h-4 w-4 text-destructive" aria-hidden="true" />
          <h2 className="text-base font-semibold">{title}</h2>
        </div>
        {description && <p className="mb-4 text-sm text-muted-foreground">{description}</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>
            取消
          </Button>
          <Button variant={variant} size="sm" onClick={onConfirm}>
            {confirmText}
          </Button>
        </div>
      </div>
    </div>
  );
}
