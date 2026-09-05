import { useCallback, useEffect, useRef, useState } from "react";
import { Plus, RefreshCw, Rocket, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import * as api from "@/api/github";
import type { GithubConfig, GithubStatus, Release } from "@/api/github";

export function GithubPage() {
  const [cfg, setCfg] = useState<GithubConfig>({ enabled: false, url: "", backup_urls: [], timeout: "15s", retry: 3 });
  const [releases, setReleases] = useState<Release[]>([]);
  const [upStatus, setUpStatus] = useState<GithubStatus | null>(null);
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const [saving, setSaving] = useState(false);
  // 升级流程提示：confirmVer=待确认目标版本；upgrading=已触发、等待结果的目标版本
  const [confirmVer, setConfirmVer] = useState<string | null>(null);
  const [upgrading, setUpgrading] = useState<string | null>(null);
  const preUpgradeVersionRef = useRef<string>("");

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
  }, [refresh]);

  // 升级状态轮询：失败/成功主动提示（升级会重启服务，重启后由新进程上报 idle）
  // 成功判定：版本号变化，或达到目标版本（兼容同版本重装——版本号不变的升级）
  useEffect(() => {
    if (!upgrading) return;
    const t = setInterval(() => {
      api.status().then((st) => {
        setUpStatus(st);
        if (st.state === "error" || st.state === "panic") {
          notify("err", `升级失败：${st.message || st.state}`);
          setUpgrading(null);
        } else if (st.state === "idle" && (st.version === upgrading || (st.version && st.version !== preUpgradeVersionRef.current))) {
          notify("ok", `升级成功，当前版本 ${st.version}`);
          setUpgrading(null);
          void refresh();
        }
      }).catch(() => undefined); // 升级重启间隙连接失败属正常，等新进程起来
    }, 1500);
    return () => clearInterval(t);
  }, [upgrading, notify, refresh]);

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
    setConfirmVer(null);
    try {
      preUpgradeVersionRef.current = upStatus?.version || "";
      await api.triggerUpdate(version);
      setUpgrading(version);
      notify("ok", `开始升级到 ${version}，过程中服务会短暂重启，请勿关闭页面`);
    } catch (e) {
      notify("err", "升级请求失败: " + (e as Error).message);
    }
  };

  const statusText = upStatus?.state || "idle";
  const statusState = upStatus?.state;
  // 升级状态机步骤（与后端 SetStatus 调用序一致），用于进度条展示
  const UPGRADE_STEPS: [string, string][] = [
    ["starting", "启动升级"],
    ["downloading", "下载新版本"],
    ["backing_up", "备份当前程序"],
    ["unzipping", "解压新版本"],
    ["restarting", "重启服务"],
  ];
  const curStepIdx = (() => {
    if (!upgrading) return -1;
    if (statusState === "running") return 0;
    const i = UPGRADE_STEPS.findIndex(([s]) => s === statusState);
    return i === -1 ? 0 : i;
  })();
  const upgradeDone = statusState === "idle" && !!upgrading && (!!upStatus?.version && (upStatus.version === upgrading || upStatus.version !== preUpgradeVersionRef.current));
  const upgradePct = upgradeDone ? 100 : Math.round(((curStepIdx + 1) / UPGRADE_STEPS.length) * 100);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">GitHub 加速配置</h1>
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

      {/* 升级进度条：状态机步骤 + 百分比 */}
      {upgrading && (
        <Card>
          <CardContent className="space-y-3 p-4">
            <div className="flex items-center justify-between">
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <Rocket className="h-4 w-4 animate-pulse text-violet-600 dark:text-violet-300" />
                正在升级到 <span className="font-mono">{upgrading}</span>
              </h3>
              <span className="font-mono text-sm text-violet-700 dark:text-violet-200">{upgradePct}%</span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-violet-500/10">
              <div
                className={`h-full rounded-full transition-all duration-500 ${upgradeDone ? "bg-green-500" : "bg-violet-600"}`}
                style={{ width: `${upgradePct}%` }}
              />
            </div>
            <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
              {UPGRADE_STEPS.map(([s, label], i) => (
                <span key={s} className={i < curStepIdx || upgradeDone ? "text-green-600 dark:text-green-400" : i === curStepIdx ? "font-medium text-violet-700 dark:text-violet-200" : "text-muted-foreground/60"}>
                  {i < curStepIdx || upgradeDone ? "✓ " : i === curStepIdx ? "● " : "○ "}
                  {label}
                </span>
              ))}
              {upgradeDone && <span className="font-medium text-green-600 dark:text-green-400">✓ 升级成功</span>}
              {(statusState === "error" || statusState === "panic") && <span className="font-medium text-destructive">✗ 升级失败</span>}
            </div>
            {upStatus?.message && <p className="text-xs text-muted-foreground">{upStatus.message}</p>}
            <p className="text-xs text-muted-foreground/70">升级过程中服务会短暂重启，页面自动恢复，请勿关闭。</p>
          </CardContent>
        </Card>
      )}

      {confirmVer !== null && (
        <ConfirmDialog
          title="确认升级版本"
          description={`确定要升级到版本 ${confirmVer} 吗？升级过程中服务会短暂重启（下载 → 备份 → 解压 → 重启），期间播放与管理界面会短暂不可用。`}
          confirmText="确定升级"
          variant="default"
          onConfirm={() => doUpdate(confirmVer)}
          onClose={() => setConfirmVer(null)}
        />
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
        <p className="border-b px-4 py-2 text-xs text-muted-foreground">
          此加速配置同时作用于：<span className="text-foreground">仓库同步</span>（「仓库同步」页拉取 GitHub/Gitee/GitLab 仓库内容）与
          <span className="text-foreground">版本升级</span>（下方拉取发布版本并下载升级包）。
        </p>
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

      {/* 版本升级：Android APK 内置 so / Windows 不支持在线升级，整卡隐藏并提示走对应更新流程 */}
      {upStatus?.updatable === false ? (
        <Card>
          <CardContent className="p-4">
            <h3 className="font-semibold">版本升级</h3>
            <p className="mt-1 text-sm text-muted-foreground">
              当前平台不支持在线升级：APK 内置版本请使用 APK 自身的更新流程（无法更新内置的 so），Windows 版请下载安装包覆盖安装。
            </p>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="p-4">
            <div className="mb-2 flex items-center justify-between">
              <h3 className="font-semibold">版本升级</h3>
              <Button variant="outline" size="sm" onClick={refresh}>
                <RefreshCw className="mr-1 h-4 w-4" /> 检查更新
              </Button>
            </div>
            {releases.length === 0 ? (
              <p className="text-sm text-muted-foreground">未获取到发布版本{upStatus?.state === "error" ? "（" + upStatus.message + "）" : ""}，请检查上方加速配置或稍后重试。</p>
            ) : (
              <ul className="divide-y">
                {releases.map((r) => (
                  <li key={r.tag_name} className="flex items-center justify-between gap-2 py-2">
                    <span className="font-mono text-sm">{r.tag_name}</span>
                    <Button size="sm" disabled={!!upgrading} onClick={() => setConfirmVer(r.tag_name)}>
                      <Rocket className="mr-1 h-4 w-4" /> {upgrading === r.tag_name ? "升级中…" : "升级到此版本"}
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}