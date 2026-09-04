import { useCallback, useEffect, useRef, useState } from "react";
import { Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import * as api from "@/api/sync";
import type { SyncEntry } from "@/api/sync";

function Field({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={className}>
      <Label className="mb-1 block text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}

function Check({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-sm">
      <input type="checkbox" className="accent-[hsl(var(--primary))]" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  );
}

export function SyncPage() {
  const [entries, setEntries] = useState<SyncEntry[]>(() => [api.defaultEntry()]);
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const [branches, setBranches] = useState<Record<number, string[]>>({});
  const [branchLoading, setBranchLoading] = useState<Record<number, boolean>>({});
  const [saving, setSaving] = useState(false);
  const timerRef = useRef<number | null>(null);

  const notify = useCallback((type: "ok" | "err", msg: string) => {
    setNote({ type, msg });
    setTimeout(() => setNote(null), 3500);
  }, []);

  const load = useCallback(async () => {
    try {
      const data = await api.loadConfig();
      const list = Array.isArray(data) && data.length ? data.map((e) => ({ ...api.defaultEntry(), ...e })) : [api.defaultEntry()];
      setEntries(list);
      list.forEach((e, i) => {
        if (e.repo) doFetchBranches(i, e);
      });
    } catch (e) {
      setEntries([api.defaultEntry()]);
      notify("err", "加载配置失败: " + (e as Error).message);
    }
  }, [notify]);

  useEffect(() => {
    load();
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [load]);

  const onChange = (i: number, patch: Partial<SyncEntry>) =>
    setEntries((list) => list.map((e, j) => (j === i ? { ...e, ...patch } : e)));

  const doFetchBranches = async (i: number, e: SyncEntry) => {
    if (!e.repo) return;
    setBranchLoading((s) => ({ ...s, [i]: true }));
    try {
      const list = await api.fetchBranches({ type: e.type, host: e.host, repo: e.repo, token: e.token });
      setBranches((s) => ({ ...s, [i]: list }));
      if (list.length && !list.includes(e.branch)) onChange(i, { branch: list[0] });
    } catch {
      /* 拉取失败保留手动值 */
    } finally {
      setBranchLoading((s) => ({ ...s, [i]: false }));
    }
  };

  const scheduleFetch = (i: number) => {
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => doFetchBranches(i, entries[i]), 600);
  };

  const addEntry = () => setEntries((list) => [...list, api.defaultEntry()]);
  const removeEntry = (i: number) => setEntries((list) => list.filter((_, j) => j !== i));

  const [pendingDelete, setPendingDelete] = useState<number | null>(null);
  const askDelete = (i: number) => setPendingDelete(i);

  const addProtect = (i: number) => onChange(i, { protect: [...(entries[i].protect || []), ""] });
  const setProtect = (i: number, pi: number, v: string) =>
    onChange(i, { protect: entries[i].protect.map((p, j) => (j === pi ? v : p)) });
  const removeProtect = (i: number, pi: number) =>
    onChange(i, { protect: entries[i].protect.filter((_, j) => j !== pi) });

  const save = async () => {
    setSaving(true);
    const clean = entries
      .map((e) => ({
        name: (e.name || "").trim(),
        enabled: !!e.enabled,
        type: (e.type || "github").trim() || "github",
        host: (e.host || "").trim(),
        repo: (e.repo || "").trim(),
        branch: (e.branch || "main").trim() || "main",
        token: e.token || "",
        interval: (e.interval || "60s").trim() || "60s",
        timeout: (e.timeout || "15s").trim() || "15s",
        repo_path: (e.repo_path || ".").trim() || ".",
        local_path: (e.local_path || "tvbox").trim() || "tvbox",
        only_php: !!e.only_php,
        backup: e.backup !== false,
        delete: !!e.delete,
        protect: (e.protect || []).filter((p) => p.trim() !== ""),
      }))
      .filter((e) => e.repo !== "");
    if (clean.length === 0) return notify("err", "请至少填写一个仓库标识");
    try {
      await api.saveConfig(clean);
      notify("ok", "配置保存成功，同步模块将自动重启");
      await load();
    } catch (e) {
      notify("err", "保存失败: " + (e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">仓库同步</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load}>
            <RefreshCw className="mr-1 h-4 w-4" /> 重置
          </Button>
          <Button size="sm" onClick={addEntry}>
            <Plus className="mr-1 h-4 w-4" /> 添加仓库
          </Button>
          <Button size="sm" variant="outline" onClick={save} disabled={saving}>
            <Save className="mr-1 h-4 w-4" /> {saving ? "保存中..." : "保存全部"}
          </Button>
        </div>
      </div>

      {note && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${note.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {note.msg}
        </div>
      )}

      {entries.map((e, i) => (
        <Card key={i}>
          <CardContent className="p-0">
            <div className="flex items-center justify-between border-b px-3 py-2">
              <span className="font-semibold">
                仓库 {i + 1}：{e.name || e.repo || "(未命名)"}
              </span>
              <Button size="sm" variant="ghost" onClick={() => askDelete(i)}>
                <Trash2 className="h-4 w-4" /> 删除
              </Button>
            </div>
            <div className="grid grid-cols-1 gap-3 p-3 md:grid-cols-2">
              <Field label="名称（可选标识）">
                <Input value={e.name} onChange={(ev) => onChange(i, { name: ev.target.value })} placeholder="例如: tvbox" />
              </Field>
              <div className="flex items-end gap-6 pb-1">
                <Check label="启用" checked={!!e.enabled} onChange={(v) => onChange(i, { enabled: v })} />
              </div>
              <Field label="仓库类型">
                <select className="h-9 w-full rounded-[var(--radius)] border border-input bg-background px-2 text-sm" value={e.type} onChange={(ev) => { onChange(i, { type: ev.target.value }); scheduleFetch(i); }}>
                  <option value="github">GitHub</option>
                  <option value="gitlab">GitLab</option>
                  <option value="gitee">Gitee</option>
                </select>
              </Field>
              <Field label="仓库标识（owner/repo）">
                <Input value={e.repo} onChange={(ev) => { onChange(i, { repo: ev.target.value }); scheduleFetch(i); }} placeholder="例如: qist/tvbox" />
              </Field>
              <Field label="同步分支">
                <div className="flex gap-1.5">
                  <select
                    className="h-9 w-full rounded-[var(--radius)] border border-input bg-background px-2 text-sm"
                    value={e.branch}
                    onChange={(ev) => onChange(i, { branch: ev.target.value })}
                  >
                    {(branches[i]?.length ? branches[i] : [e.branch])
                      .filter((v, idx, arr) => arr.indexOf(v) === idx)
                      .map((b) => (
                        <option key={b} value={b}>{b}</option>
                      ))}
                  </select>
                  <Button size="icon" variant="outline" className="h-9 w-9 shrink-0" disabled={!e.repo || branchLoading[i]} onClick={() => doFetchBranches(i, e)} title="刷新分支">
                    <RefreshCw className={`h-4 w-4 ${branchLoading[i] ? "animate-spin" : ""}`} />
                  </Button>
                </div>
              </Field>
              <Field label="自建实例地址（host，可选）" className="md:col-span-2">
                <Input value={e.host} onChange={(ev) => { onChange(i, { host: ev.target.value }); scheduleFetch(i); }} placeholder="自建 GitLab: https://git.内网 或 Gitee: https://gitee.com（留空 = 平台默认）" />
              </Field>
              <Field label="访问令牌（PAT，公开仓库可留空）" className="md:col-span-2">
                <Input value={e.token} onChange={(ev) => { onChange(i, { token: ev.target.value }); scheduleFetch(i); }} placeholder="已保存令牌以 ******** 显示，不回显；填写新值保存后生效" />
              </Field>
              <Field label="轮询间隔">
                <Input value={e.interval} onChange={(ev) => onChange(i, { interval: ev.target.value })} placeholder="例如: 60s（最小 10s）" />
              </Field>
              <Field label="请求超时">
                <Input value={e.timeout} onChange={(ev) => onChange(i, { timeout: ev.target.value })} placeholder="例如: 15s" />
              </Field>
              <Field label="仓库内源目录（repo_path）">
                <Input value={e.repo_path} onChange={(ev) => onChange(i, { repo_path: ev.target.value })} placeholder="例如: .（仓库根）" />
              </Field>
              <Field label="本地目标（local_path）">
                <Input value={e.local_path} onChange={(ev) => onChange(i, { local_path: ev.target.value })} placeholder="例如: tvbox（docroot/tvbox）" />
              </Field>
              <div className="flex flex-wrap items-center gap-x-6 gap-y-2 md:col-span-2">
                <Check label="仅同步 PHP 文件" checked={!!e.only_php} onChange={(v) => onChange(i, { only_php: v })} />
                <Check label="覆盖/删除前备份" checked={e.backup !== false} onChange={(v) => onChange(i, { backup: v })} />
                <Check label="远端删除时本地也删除" checked={!!e.delete} onChange={(v) => onChange(i, { delete: v })} />
              </div>
              <div className="space-y-1 md:col-span-2">
                <Label className="text-xs text-muted-foreground">本地保护清单（protect，永不覆盖/删除）</Label>
                <div className="space-y-1.5">
                  {(e.protect || []).map((p, pi) => (
                    <div key={pi} className="flex gap-1.5">
                      <Input value={p} onChange={(ev) => setProtect(i, pi, ev.target.value)} placeholder="相对 local_path 的路径" />
                      <Button size="icon" variant="ghost" className="h-9 w-9 shrink-0" onClick={() => removeProtect(i, pi)}>
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  ))}
                </div>
                <Button size="sm" variant="outline" onClick={() => addProtect(i)}>
                  <Plus className="mr-1 h-4 w-4" /> 添加保护路径
                </Button>
                <p className="text-xs text-muted-foreground">相对 local_path 的路径列表，支持目录前缀。设备私有文件（如 tv.txt）或整个目录（private/）加入后同步永不覆盖、永不删除。</p>
              </div>
            </div>
          </CardContent>
        </Card>
      ))}

      {pendingDelete !== null && (
        <ConfirmDialog
          title="确认删除仓库"
          description={`确定删除仓库 ${pendingDelete + 1}「${entries[pendingDelete]?.name || entries[pendingDelete]?.repo || "(未命名)"}」吗？删除后需点击保存才会生效。`}
          onConfirm={() => {
            removeEntry(pendingDelete);
            setPendingDelete(null);
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </div>
  );
}