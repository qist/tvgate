import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Brush, Download, FolderCode, RefreshCw, RotateCcw, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Checkbox } from "./Checkbox";
import * as api from "@/api/backupCenter";
import type { BackupItem } from "@/api/backupCenter";
import { isElevated } from "@/api/elevate";
import { ElevateDialog } from "@/components/ElevateDialog";

function fmtSize(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
  return (n / 1048576).toFixed(1) + " MB";
}

export function BackupCenterPage() {
  const navigate = useNavigate();
  const [items, setItems] = useState<BackupItem[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [cleanupOpen, setCleanupOpen] = useState(false);
  const [keep, setKeep] = useState(3);
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const notify = useCallback((type: "ok" | "err", msg: string) => {
    setNote({ type, msg });
    setTimeout(() => setNote(null), 3500);
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const list = await api.list();
      setItems(list);
      setSelected(new Set());
    } catch (e) {
      notify("err", "加载失败: " + (e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [notify]);

  useEffect(() => {
    load();
  }, [load]);

  const allSelected = items.length > 0 && items.every((i) => selected.has(i.name));
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(items.map((i) => i.name)));

  // 二次验证：下载/恢复前校验，未授权弹窗、通过后续做
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

  const doRestore = async (it: BackupItem) => {
    await ensureThen(async () => {
      if (!window.confirm("确定回滚此备份？当前文件会被覆盖（会自动产生新备份）。")) return;
      try {
        const msg = await api.restore(it.name);
        notify("ok", msg);
        load();
      } catch (e) {
        notify("err", "回滚失败: " + (e as Error).message);
      }
    });
  };

  const doDownload = (it: BackupItem) => {
    ensureThen(() => {
      const a = document.createElement("a");
      a.href = api.downloadUrl(it.name);
      a.download = it.name;
      a.click();
    });
  };

  const doDelete = async (it: BackupItem) => {
    if (!window.confirm("确定删除此备份文件？")) return;
    try {
      const msg = await api.remove(it.name);
      notify("ok", msg);
      load();
    } catch (e) {
      notify("err", "删除失败: " + (e as Error).message);
    }
  };

  const doBatchDelete = async () => {
    if (selected.size === 0) return;
    if (!window.confirm(`确定删除选中的 ${selected.size} 个备份文件？`)) return;
    try {
      const msg = await api.batchDelete(Array.from(selected));
      notify("ok", msg);
      load();
    } catch (e) {
      notify("err", "批量删除失败: " + (e as Error).message);
    }
  };

  const doCleanup = async () => {
    if (!window.confirm(`确定清理？每个文件仅保留最新 ${keep} 个备份，其余全部删除。`)) return;
    setCleanupOpen(false);
    try {
      const msg = await api.cleanup(keep);
      notify("ok", msg);
      load();
    } catch (e) {
      notify("err", "清理失败: " + (e as Error).message);
    }
  };

  const selectAll = () => setSelected(items.length > 0 && selected.size === items.length ? new Set() : new Set(items.map((i) => i.name)));

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">备份中心</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => navigate("/code")} title="打开代码文件管理">
            <FolderCode className="mr-1 h-4 w-4" /> 代码文件
          </Button>
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className={`mr-1 h-4 w-4 ${loading ? "animate-spin" : ""}`} /> 刷新
          </Button>
          <Button variant="outline" size="sm" onClick={selectAll}>
            全选
          </Button>
          <Button size="sm" variant="destructive" onClick={doBatchDelete} disabled={selected.size === 0}>
            <Trash2 className="mr-1 h-4 w-4" /> 批量删除
          </Button>
          <Button size="sm" variant="outline" onClick={() => setCleanupOpen(true)}>
            <Brush className="mr-1 h-4 w-4" /> 清理旧备份
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
            <span className="flex-1">原始文件</span>
            <span className="w-32">备份时间</span>
            <span className="w-20 text-right">大小</span>
            <span className="w-44 text-right">操作</span>
          </div>
          {items.length === 0 ? (
            <div className="p-6 text-center text-sm text-muted-foreground">暂无备份文件</div>
          ) : (
            items.map((it) => (
              <div key={it.name} className="flex items-center gap-3 border-b px-3 py-2 text-sm last:border-0 hover:bg-accent/40">
                <Checkbox checked={selected.has(it.name)} onChange={(v) => setSelected((prev) => {
                  const next = new Set(prev);
                  if (v) next.add(it.name);
                  else next.delete(it.name);
                  return next;
                })} />
                <div className="min-w-0 flex-1 truncate" title={it.name}>{it.original}</div>
                <div className="w-32 shrink-0 text-xs text-muted-foreground">{it.time}</div>
                <div className="w-20 shrink-0 text-right text-xs text-muted-foreground">{fmtSize(it.size)}</div>
                <div className="flex w-44 shrink-0 justify-end gap-1">
                  <Button size="sm" onClick={() => doRestore(it)}>
                    <RotateCcw className="mr-1 h-4 w-4" /> 回滚
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => doDownload(it)} title="下载">
                    <Download className="h-4 w-4" />
                  </Button>
                  <Button size="icon" variant="ghost" onClick={() => doDelete(it)}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            ))
          )}
        </CardContent>
      </Card>

      {cleanupOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={() => setCleanupOpen(false)}>
          <div className="w-full max-w-sm rounded-xl border border-border bg-background p-4" onClick={(e) => e.stopPropagation()}>
            <h3 className="mb-3 text-base font-semibold">🧹 清理旧备份</h3>
            <label className="mb-1 block text-sm">每个文件保留最新几个备份（其余删除）：</label>
            <Input type="number" min={0} max={99} value={keep} onChange={(e) => setKeep(Number(e.target.value || 0))} />
            <div className="mt-4 flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setCleanupOpen(false)}>取消</Button>
              <Button size="sm" onClick={doCleanup}>确定清理</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}