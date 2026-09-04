import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";

/** 删除确认弹窗：卡片删除前确认，防止误点立即删除 */
export function ConfirmDialog({
  title,
  description,
  confirmText = "删除",
  onConfirm,
  onClose,
}: {
  title: string;
  description?: string;
  confirmText?: string;
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
          <Button variant="destructive" size="sm" onClick={onConfirm}>
            {confirmText}
          </Button>
        </div>
      </div>
    </div>
  );
}
