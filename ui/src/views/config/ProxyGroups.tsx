import { useCallback, useEffect, useMemo, useState } from "react";
import { Plus, Pencil, Trash2, X, GripVertical, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { listProxyGroups, saveProxyGroups, type Proxy, type ProxyGroup } from "@/api/proxygroups";

interface Entry {
  name: string;
  g: ProxyGroup;
}

const emptyProxy = (): Proxy => ({ name: "", type: "http", server: "", port: 8080, udp: false, username: "", password: "", headers: {} });

const emptyGroup = (): ProxyGroup => ({
  proxies: [],
  domains: [],
  ipv6: false,
  interval: "180s",
  loadbalance: "round-robin",
  max_retries: 1,
  retry_delay: "1s",
  max_rt: "200ms",
});

export function ProxyGroupsPage() {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [editing, setEditing] = useState<Set<number>>(new Set());
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => {
    const map = await listProxyGroups();
    setEntries(Object.entries(map).map(([name, g]) => ({ name, g })));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const set = (i: number, patch: Partial<ProxyGroup>) =>
    setEntries((prev) => prev.map((e, idx) => (idx === i ? { ...e, g: { ...e.g, ...patch } } : e)));

  const addGroup = () => {
    // 新组插到最前面并直接进入编辑，避免组多时滚到底部才能添加
    setEntries((prev) => [{ name: "", g: emptyGroup() }, ...prev]);
    setEditing((prev) => new Set([0, ...[...prev].map((i) => i + 1)]));
    window.scrollTo({ top: 0, behavior: "smooth" });
  };

  const remove = (i: number) => {
    setEntries((prev) => prev.filter((_, idx) => idx !== i));
    setEditing((prev) => {
      const n = new Set(prev);
      n.delete(i);
      return n;
    });
  };

  const [pendingDelete, setPendingDelete] = useState<number | null>(null);
  const askDelete = (i: number) => setPendingDelete(i);

  const save = async () => {
    const map: Record<string, ProxyGroup> = {};
    for (const e of entries) {
      if (!e.name.trim()) continue;
      // 只提交配置字段，剥离运行时 stats
      const { stats: _stats, ...cfg } = e.g;
      map[e.name.trim()] = cfg;
    }
    try {
      await saveProxyGroups(map);
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setEditing(new Set());
      // 代理组走"写文件 + 热重载（debounce ~5s）"，等完成后再拉取以同步 stats 等派生字段
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">代理组</h1>
        <Button onClick={addGroup}>
          <Plus className="mr-1 h-4 w-4" /> 添加代理组
        </Button>
      </div>

      {notice && (
        <div
          className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"
            }`}
        >
          {notice.msg}
        </div>
      )}

      {entries.map((e, i) =>
        editing.has(i) ? (
          <GroupEditCard key={i} entry={e} onChange={(p) => set(i, p)} onName={(n) => setEntries((prev) => prev.map((x, idx) => (idx === i ? { ...x, name: n } : x)))} onSave={save} onCancel={() => setEditing((prev) => { const n = new Set(prev); n.delete(i); return n; })} onDelete={() => askDelete(i)} />
        ) : (
          <GroupViewCard key={i} entry={e} onEdit={() => setEditing((prev) => new Set(prev).add(i))} onDelete={() => askDelete(i)} />
        ),
      )}

      {pendingDelete !== null && (
        <ConfirmDialog
          title="确认删除代理组"
          description={`确定删除「${entries[pendingDelete]?.name || "未命名代理组"}」吗？删除后需点击保存才会生效。`}
          onConfirm={() => {
            remove(pendingDelete);
            setPendingDelete(null);
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}

      {entries.length > 0 && (
        <div className="flex gap-2">
          <Button onClick={save}>保存全部配置</Button>
          <Button variant="secondary" onClick={refresh}>重置</Button>
        </div>
      )}
    </div>
  );
}

function GroupViewCard({ entry, onEdit, onDelete }: { entry: Entry; onEdit: () => void; onDelete: () => void }) {
  const g = entry.g;
  const proxyStats = g.stats?.ProxyStats || {};
  const aliveCount = g.proxies.filter((p) => {
    const st = proxyStats[p.name || p.server];
    return st && st.LastCheck && st.Alive;
  }).length;
  const unknownCount = g.proxies.filter((p) => {
    const st = proxyStats[p.name || p.server];
    return !st || !st.LastCheck;
  }).length;
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base">{entry.name || "(未命名组)"}</CardTitle>
          <Badge variant="secondary">{g.proxies.length} 代理</Badge>
          <Badge variant={aliveCount > 0 ? "default" : "outline"} className={aliveCount > 0 ? "bg-green-600 text-white" : unknownCount === g.proxies.length && g.proxies.length > 0 ? "bg-muted text-muted-foreground" : ""}>
            {aliveCount}/{g.proxies.length} 在线{unknownCount > 0 ? ` · ${unknownCount} 未测` : ""}
          </Badge>
          {g.loadbalance && <Badge variant="outline">{g.loadbalance}</Badge>}
        </div>
        <div className="flex gap-1.5">
          <Button variant="outline" size="sm" onClick={onEdit}><Pencil className="h-4 w-4" /></Button>
          <Button variant="destructive" size="sm" onClick={onDelete}><Trash2 className="h-4 w-4" /></Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {/* 节点状态列表 */}
        {g.proxies.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[420px] text-xs">
              <thead>
                <tr className="text-left text-muted-foreground">
                  <th className="pb-1 pr-3 font-medium">节点</th>
                  <th className="pb-1 pr-3 font-medium">类型</th>
                  <th className="pb-1 pr-3 font-medium">地址</th>
                  <th className="pb-1 pr-3 font-medium">状态</th>
                  <th className="pb-1 pr-3 font-medium">延迟</th>
                  <th className="pb-1 font-medium">连续失败</th>
                </tr>
              </thead>
              <tbody>
                {g.proxies.map((p, pi) => {
                  const st = proxyStats[p.name || p.server];
                  // 从未测速（无 LastCheck）视为"未测"，不算离线
                  const checked = !!st?.LastCheck;
                  const alive = st?.Alive;
                  return (
                    <tr key={pi} className="border-t border-border/60">
                      <td className="py-1.5 pr-3 font-medium">{p.name || "(未命名)"}</td>
                      <td className="py-1.5 pr-3 text-muted-foreground">{p.type}</td>
                      <td className="py-1.5 pr-3 font-mono text-muted-foreground">{p.server}:{p.port}</td>
                      <td className="py-1.5 pr-3">
                        <span className="inline-flex items-center gap-1">
                          <span className={`inline-block h-2 w-2 rounded-full ${checked ? (alive ? "bg-green-500" : "bg-red-500") : "bg-muted-foreground/40"}`} />
                          <span className="text-muted-foreground">{checked ? (alive ? "在线" : "离线") : "未测"}</span>
                        </span>
                      </td>
                      <td className="py-1.5 pr-3 font-mono">{st?.ResponseTime ? `${fmtDur(st.ResponseTime)}` : "—"}</td>
                      <td className="py-1.5 font-mono">{st ? String(st.FailCount ?? 0) : "—"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        <p className="text-muted-foreground">域名：{g.domains.join("，") || "无"}</p>
        <p className="text-muted-foreground">
          ipv6={String(g.ipv6)} · interval={g.interval} · retries={g.max_retries} · delay={g.retry_delay} · max_rt={g.max_rt}
        </p>
      </CardContent>
    </Card>
  );
}

/** 时长（纳秒）→ 可读 */
function fmtDur(ns: number): string {
  if (ns < 1e6) return `${Math.round(ns / 1e3)}μs`;
  if (ns < 1e9) return `${(ns / 1e6).toFixed(1)}ms`;
  return `${(ns / 1e9).toFixed(2)}s`;
}

function GroupEditCard({
  entry,
  onChange,
  onName,
  onSave,
  onCancel,
  onDelete,
}: {
  entry: Entry;
  onChange: (p: Partial<ProxyGroup>) => void;
  onName: (n: string) => void;
  onSave: () => void;
  onCancel: () => void;
  onDelete: () => void;
}) {
  const g = entry.g;
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-2">
        <CardTitle className="text-base">编辑代理组</CardTitle>
        <div className="flex gap-1.5">
          <Button size="sm" onClick={onSave}><Save className="mr-1 h-4 w-4" />保存</Button>
          <Button variant="outline" size="sm" onClick={onCancel}><X className="mr-1 h-4 w-4" />取消</Button>
          <Button variant="destructive" size="sm" onClick={onDelete}><Trash2 className="h-4 w-4" /></Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        <Field label="组名">
          <Input value={entry.name} onChange={(e) => onName(e.target.value)} placeholder="代理组名称（唯一标识）" />
        </Field>

        {/* 代理列表 */}
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label>代理列表</Label>
            <Button size="sm" variant="secondary" onClick={() => onChange({ proxies: [...g.proxies, emptyProxy()] })}>
              <Plus className="h-4 w-4" /> 添加代理
            </Button>
          </div>
          {g.proxies.map((p, pi) => (
            <ProxyEditor
              key={pi}
              proxy={p}
              onChange={(patch) => onChange({ proxies: g.proxies.map((x, idx) => (idx === pi ? { ...x, ...patch } : x)) })}
              onRemove={() => onChange({ proxies: g.proxies.filter((_, idx) => idx !== pi) })}
            />
          ))}
        </div>

        {/* 域名列表 */}
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label>域名规则</Label>
            <Button size="sm" variant="secondary" onClick={() => onChange({ domains: [...g.domains, ""] })}>
              <Plus className="h-4 w-4" /> 添加域名
            </Button>
          </div>
          {g.domains.map((d, di) => (
            <div key={di} className="flex items-center gap-2">
              <Input
                value={d}
                className="font-mono"
                onChange={(e) => onChange({ domains: g.domains.map((x, idx) => (idx === di ? e.target.value : x)) })}
                placeholder="例如: .example.com 或 1.2.3.4"
              />
              <Button size="icon" variant="ghost" onClick={() => onChange({ domains: g.domains.filter((_, idx) => idx !== di) })}>
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
        </div>

        {/* 组参数 */}
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="检查间隔">
            <Input value={g.interval} onChange={(e) => onChange({ interval: e.target.value })} placeholder="180s" />
          </Field>
          <Field label="负载均衡">
            <Input value={g.loadbalance} onChange={(e) => onChange({ loadbalance: e.target.value })} placeholder="round-robin" />
          </Field>
          <Field label="最多重试">
            <Input type="number" value={g.max_retries} onChange={(e) => onChange({ max_retries: +e.target.value || 0 })} />
          </Field>
          <Field label="重试延迟">
            <Input value={g.retry_delay} onChange={(e) => onChange({ retry_delay: e.target.value })} placeholder="1s" />
          </Field>
          <Field label="最大响应时间">
            <Input value={g.max_rt} onChange={(e) => onChange({ max_rt: e.target.value })} placeholder="200ms" />
          </Field>
          <Field label="启用 IPv6">
            <div className="pt-2">
              <Switch checked={g.ipv6} onCheckedChange={(v) => onChange({ ipv6: v })} />
            </div>
          </Field>
        </div>
      </CardContent>
    </Card>
  );
}

function ProxyEditor({
  proxy,
  onChange,
  onRemove,
}: {
  proxy: Proxy;
  onChange: (p: Partial<Proxy>) => void;
  onRemove: () => void;
}) {
  const headers = proxy.headers || {};
  const setHeaders = (map: Record<string, string>) => onChange({ headers: map });
  return (
    <div className="rounded-lg border bg-muted/30 p-3 space-y-3">
      {/* 名称 / 服务器地址 / 端口 用网格布局，避免 w-full 与定宽类冲突导致输入框塌缩 */}
      <div className="grid grid-cols-[auto_minmax(120px,1fr)_minmax(180px,1.6fr)_88px_auto] items-center gap-2">
        <GripVertical className="h-4 w-4 shrink-0 text-muted-foreground" />
        <div>
          <Input value={proxy.name} placeholder="代理名称" onChange={(e) => onChange({ name: e.target.value })} />
        </div>
        <div>
          <Input value={proxy.server} className="font-mono" placeholder="服务器地址（IP 或域名）" onChange={(e) => onChange({ server: e.target.value })} />
        </div>
        <div>
          <Input type="number" value={proxy.port} placeholder="端口" onChange={(e) => onChange({ port: +e.target.value || 0 })} />
        </div>
        <Button size="icon" variant="ghost" onClick={onRemove}><Trash2 className="h-4 w-4" /></Button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <select className="h-9 rounded-[var(--radius)] border bg-background px-2 text-sm" value={proxy.type} onChange={(e) => onChange({ type: e.target.value })}>
          {/* 仅保留后端支持的协议：http/https/socks5/socks4 */}
          {["http", "https", "socks5", "socks4"].map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
        <label className="flex items-center gap-1.5 text-sm">
          <Switch checked={proxy.udp} onCheckedChange={(v) => onChange({ udp: v })} /> UDP
        </label>
        <div className="min-w-[180px] flex-1">
          <Input value={proxy.username} placeholder="用户名（可选）" onChange={(e) => onChange({ username: e.target.value })} />
        </div>
        <div className="min-w-[180px] flex-1">
          <Input type="password" value={proxy.password} placeholder="密码（可选）" onChange={(e) => onChange({ password: e.target.value })} />
        </div>
      </div>
      <HeadersEditor headers={headers} onChange={setHeaders} />
    </div>
  );
}

function HeadersEditor({ headers, onChange }: { headers: Record<string, string>; onChange: (m: Record<string, string>) => void }) {
  const keys = useMemo(() => Object.keys(headers), [headers]);
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">Headers</span>
        <Button size="sm" variant="ghost" onClick={() => onChange({ ...headers, "": "" })}>
          <Plus className="h-3.5 w-3.5" />
        </Button>
      </div>
      {keys.map((k) => (
        <div key={k} className="flex items-center gap-2">
          <Input className="font-mono" value={k} placeholder="键" onChange={(e) => { const nm = { ...headers }; delete nm[k]; nm[e.target.value] = headers[k] ?? ""; onChange(nm); }} />
          <Input className="font-mono" value={headers[k]} placeholder="值" onChange={(e) => onChange({ ...headers, [k]: e.target.value })} />
          <Button size="icon" variant="ghost" onClick={() => { const nm = { ...headers }; delete nm[k]; onChange(nm); }}>
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      ))}
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}