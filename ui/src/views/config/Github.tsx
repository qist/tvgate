import { useCallback, useEffect, useState } from "react";
import { Plus, RefreshCw, Rocket, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import * as api from "@/api/github";
import type { GithubConfig, GithubStatus, Release } from "@/api/github";

export function GithubPage() {
  const [cfg, setCfg] = useState<GithubConfig>({ enabled: false, url: "", backup_urls: [], timeout: "15s", retry: 3 });
  const [releases, setReleases] = useState<Release[]>([]);
  const [upStatus, setUpStatus] = useState<GithubStatus | null>(null);
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const [saving, setSaving] = useState(false);

  const notify = useCallback((type: "ok" | "err", msg: string) => {
    setNote({ type, msg });
    setTimeout(() => setNote(null), 3500);
  }, []);

  const refresh = useCallback(async () => {
    try {
      const [c, rls, st] = await Promise.all([api.loadConfig(), api.releases().catch(() => [] as Release[]), api.status().catch(() => null)]);
      setCfg((prev) => ({ ...prev, ...c }));
      setReleases(rls);
      setUpStatus(st);
    } catch (e) {
      notify("err", "加载失败: " + (e as Error).message);
    }
  }, [notify]);

  useEffect(() => {
    refresh();
    const t = setInterval(() => {
      api.status().then(setUpStatus).catch(() => undefined);
    }, 2000);
    return () => clearInterval(t);
  }, [refresh]);

  const set = (patch: Partial<GithubConfig>) => setCfg((c) => ({ ...c, ...patch }));
  const setBackup = (i: number, v: string) => set({ backup_urls: cfg.backup_urls.map((u, j) => (j === i ? v : u)) });
  const addBackup = () => set({ backup_urls: [...cfg.backup_urls, ""] });
  const removeBackup = (i: number) => set({ backup_urls: cfg.backup_urls.filter((_, j) => j !== i) });

  const save = async () => {
    setSaving(true);
    try {
      await api.saveConfig({ ...cfg, backup_urls: cfg.backup_urls.filter((u) => u.trim() !== "") });
      notify("ok", "配置保存成功");
      await refresh();
    } catch (e) {
      notify("err", "保存失败: " + (e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const doUpdate = async (version: string) => {
    if (!window.confirm(`确定要升级到版本 ${version} 吗？升级过程会重启服务。`)) return;
    try {
      await api.triggerUpdate(version);
      notify("ok", "开始升级，请关注升级状态");
    } catch (e) {
      notify("err", "升级请求失败: " + (e as Error).message);
    }
  };

  const statusText = upStatus?.state || "idle";
  const statusState = upStatus?.state;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">GitHub 升级</h1>
        <div className="flex items-center gap-2">
          <Badge variant={statusState === "running" ? "default" : statusState === "error" || statusState === "panic" ? "destructive" : "outline"}>
            {statusText}
          </Badge>
          {upStatus?.message && <span className="text-xs text-muted-foreground">{upStatus.message}</span>}
          {upStatus?.version && <span className="text-xs text-muted-foreground">当前版本: {upStatus.version}</span>}
        </div>
      </div>

      {note && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${note.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {note.msg}
        </div>
      )}

      <Card>
        <CardContent className="flex items-center justify-between gap-2 border-b px-4 py-3">
          <h3 className="font-semibold">加速配置</h3>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={refresh}>
              <RefreshCw className="mr-1 h-4 w-4" /> 刷新
            </Button>
            <Button size="sm" onClick={save} disabled={saving}>
              <Save className="mr-1 h-4 w-4" /> {saving ? "保存中..." : "保存配置"}
            </Button>
          </div>
        </CardContent>
        <div className="grid grid-cols-1 gap-3 p-4 md:grid-cols-2">
          <div className="flex items-end gap-3 pb-1">
            <Label className="text-sm">启用 GitHub 加速</Label>
            <input type="checkbox" className="h-4 w-4 accent-[hsl(var(--primary))]" checked={cfg.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
          </div>
          <div className="space-y-1">
            <Label className="text-xs text-muted-foreground">主加速地址</Label>
            <Input value={cfg.url} onChange={(e) => set({ url: e.target.value })} placeholder="例如: https://hk.gh-proxy.com" />
          </div>
          <div className="space-y-1 md:col-span-2">
            <Label className="text-xs text-muted-foreground">备用加速地址</Label>
            <div className="space-y-1.5">
              {cfg.backup_urls.map((u, i) => (
                <div key={i} className="flex gap-1.5">
                  <Input value={u} onChange={(e) => setBackup(i, e.target.value)} placeholder="例如: https://ghproxy.com" />
                  <Button size="icon" variant="ghost" className="h-9 w-9 shrink-0" onClick={() => removeBackup(i)}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              ))}
            </div>
            <Button size="sm" variant="outline" onClick={addBackup}>
              <Plus className="mr-1 h-4 w-4" /> 添加备用地址
            </Button>
          </div>
          <div className="space-y-1">
            <Label className="text-xs text-muted-foreground">超时时间</Label>
            <Input value={cfg.timeout} onChange={(e) => set({ timeout: e.target.value })} placeholder="例如: 15s" />
          </div>
          <div className="space-y-1">
            <Label className="text-xs text-muted-foreground">重试次数</Label>
            <Input type="number" min={0} max={10} value={cfg.retry} onChange={(e) => set({ retry: Number(e.target.value || 0) })} />
          </div>
        </div>
      </Card>

      <Card>
        <CardContent className="p-4">
          <div className="mb-2 flex items-center justify-between">
            <h3 className="font-semibold">版本发布</h3>
            <Button variant="outline" size="sm" onClick={refresh}>
              <RefreshCw className="mr-1 h-4 w-4" /> 检查更新
            </Button>
          </div>
          {releases.length === 0 ? (
            <p className="text-sm text-muted-foreground">未获取到发布版本{upStatus?.state === "error" ? "（" + upStatus.message + "）" : ""}，请检查加速配置或稍后重试。</p>
          ) : (
            <ul className="divide-y">
              {releases.map((r) => (
                <li key={r.tag_name} className="flex items-center justify-between gap-2 py-2">
                  <span className="font-mono text-sm">{r.tag_name}</span>
                  <Button size="sm" disabled={statusState === "running"} onClick={() => doUpdate(r.tag_name)}>
                    <Rocket className="mr-1 h-4 w-4" /> 升级到此版本
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}