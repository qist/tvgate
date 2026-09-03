import { useCallback, useEffect, useRef, useState } from "react";
import { Download, RefreshCw, RotateCcw, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "./Checkbox";
import * as api from "@/api/configBackup";
import { isElevated } from "@/api/elevate";
import { ElevateDialog } from "@/components/ElevateDialog";

export function ConfigBackupPage() {
  const [backups, setBackups] = useState<string[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const notify = useCallback((type: "ok" | "err", msg: string) => {
    setNote({ type, msg });
    setTimeout(() => setNote(null), 3500);
  }, []);

  const load = useCallback(async () => {
    try {
      const list = await api.list();
      setBackups(list);
      setSelected(new Set());
    } catch (e) {
      notify("err", "加载列表失败: " + (e as Error).message);
    }
  }, [notify]);

  useEffect(() => {
    load();
  }, [load]);

  const allSelected = backups.length > 0 && backups.every((b) => selected.has(b));
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(backups));

  // 二次验证：下载/还原前校验，未授权弹窗、通过后续做
  const [needElevate, setNeedElevate] = useState(false);
  const pendingRef = useRef<(() => void) | null>(null);
  const ensureThen = async (fn: () => void) => {
    if (await isElevated()) fn();
    else {
      pendingRef.current = fn;
      setNeedElevate(true);
    }
  };
  const onElevated = () => {
    setNeedElevate(false);
    const p = pendingRef.current;
    pendingRef.current = null;
    p?.();
  };

  const doRestore = async (f: string) => {
    await ensureThen(async () => {
      if (!window.confirm("⚠️ 警告：确认将该备份还原到当前配置吗？\n这将覆盖当前配置，建议先手动备份当前配置。")) return;
      try {
        await api.restore(f);
        notify("ok", "还原成功！请手动刷新页面以确保配置生效。");
        load();
      } catch (e) {
        notify("err", "还原失败: " + (e as Error).message);
      }
    });
  };

  const doDownload = (f: string) => {
    ensureThen(() => {
      const a = document.createElement("a");
      a.href = api.downloadUrl(f);
      a.download = fileName(f);
      a.click();
    });
  };

  const doDelete = async (f: string) => {
    if (!window.confirm("确认删除该备份吗？此操作不可恢复！")) return;
    try {
      await api.remove(f);
      notify("ok", "删除成功");
      load();
    } catch (e) {
      notify("err", "删除失败: " + (e as Error).message);
    }
  };

  const doBatchDelete = async () => {
    if (selected.size === 0) return;
    if (!window.confirm(`确认删除选中的 ${selected.size} 个备份吗？此操作不可恢复！`)) return;
    try {
      const msg = await api.batchDelete(Array.from(selected));
      notify("ok", msg);
      load();
    } catch (e) {
      notify("err", "批量删除失败: " + (e as Error).message);
    }
  };

  const fileName = (f: string) => f.split(/[\\/]/).pop() || f;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">配置备份</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className="mr-1 h-4 w-4" /> 刷新列表
          </Button>
          <Button size="sm" variant="destructive" onClick={doBatchDelete} disabled={selected.size === 0}>
            <Trash2 className="mr-1 h-4 w-4" /> 批量删除
          </Button>
        </div>
      </div>

      {note && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${note.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {note.msg}
        </div>
      )}

      {needElevate && <ElevateDialog onDone={onElevated} onClose={() => setNeedElevate(false)} />}

      <Card>
        <CardContent className="p-0">
          <div className="flex items-center gap-3 border-b px-3 py-2 text-sm text-muted-foreground">
            <Checkbox checked={allSelected} onChange={toggleAll} />
            <span className="flex-1">文件路径</span>
            <span className="w-40 text-right">操作</span>
          </div>
          {backups.length === 0 ? (
            <div className="p-6 text-center text-sm text-muted-foreground">暂无备份文件</div>
          ) : (
            backups.map((f) => (
              <div key={f} className="flex items-center gap-3 border-b px-3 py-2 text-sm last:border-0 hover:bg-accent/40">
                <Checkbox checked={selected.has(f)} onChange={(v) => setSelected((prev) => {
                  const next = new Set(prev);
                  if (v) next.add(f);
                  else next.delete(f);
                  return next;
                })} />
                <div className="min-w-0 flex-1">
                  <div className="truncate font-mono text-xs text-muted-foreground">{f}</div>
                  <div className="truncate text-foreground">{fileName(f)}</div>
                </div>
                <div className="flex w-40 justify-end gap-1">
                  <Button size="sm" onClick={() => doRestore(f)}>
                    <RotateCcw className="mr-1 h-4 w-4" /> 还原
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => doDownload(f)} title="下载">
                    <Download className="h-4 w-4" />
                  </Button>
                  <Button size="icon" variant="ghost" onClick={() => doDelete(f)}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            ))
          )}
        </CardContent>
      </Card>
    </div>
  );
}