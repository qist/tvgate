import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Copy,
  MonitorPlay,
  Plus,
  RefreshCw,
  Save,
  Trash2,
  X,
} from "lucide-react";
import { FFmpegEditor } from "./FFmpegEditor";
import type {
  FFmpegStatus,
  PlayOutput,
  ReceiverItem,
  StreamItem,
  StreamStatus,
} from "@/api/publisher";
import * as api from "@/api/publisher";

function getNested(obj: any, path: string): any {
  return path.split(".").reduce((acc: any, k) => (acc && acc[k] !== undefined ? acc[k] : undefined), obj);
}
function deepClone<T>(o: T): T {
  return JSON.parse(JSON.stringify(o));
}
function ensureObject(parent: any, key: string): any {
  if (!parent[key] || typeof parent[key] !== "object" || Array.isArray(parent[key])) parent[key] = {};
  return parent[key];
}
function localPlays(arr: any): PlayOutput[] {
  return Array.isArray(arr) ? arr.filter((x) => x && typeof x === "object" && !Array.isArray(x) && typeof x.protocol === "string") : [];
}

function defaultStream(): StreamItem {
  return {
    buffer_size: 0,
    protocol: "rtmp",
    enabled: true,
    streamkey: { type: "random", length: 16 },
    stream: {
      source: { type: "", url: "", backup_url: "" },
      local_play_urls: [],
      mode: "primary-backup",
      receivers: { primary: { push_url: "" }, backup: { push_url: "" } },
    },
  };
}

const fmtBitrate = (bps?: number) => {
  if (!bps || bps <= 0) return "-";
  const k = bps / 1000;
  if (k < 1000) return k.toFixed(0) + " Kbps";
  const m = k / 1000;
  if (m < 1000) return m.toFixed(2) + " Mbps";
  return (m / 1000).toFixed(2) + " Gbps";
};
const fmtBytes = (b?: number) => {
  if (!b || b <= 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = b;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return v.toFixed(i === 0 ? 0 : 2) + " " + units[i];
};
const fmtDur = (seconds?: number) => {
  if (!seconds || seconds <= 0) return "-";
  const s = Math.floor(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = s % 60;
  if (h > 0) return `${h}h ${m}m ${ss}s`;
  if (m > 0) return `${m}m ${ss}s`;
  return `${ss}s`;
};

export function PublisherPage() {
  const [cfg, setCfg] = useState<Record<string, any>>({});
  const [stats, setStats] = useState<StreamStatus[]>([]);
  const [ffmpeg, setFfmpeg] = useState<FFmpegStatus | null>(null);
  const [path, setPath] = useState("");
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const [editing, setEditing] = useState<{ name: string; isNew: boolean } | null>(null);
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const [draft, setDraft] = useState<StreamItem>(defaultStream());

  const notify = useCallback((type: "ok" | "err", msg: string) => {
    setNote({ type, msg });
    setTimeout(() => setNote(null), 3500);
  }, []);

  const refresh = useCallback(async () => {
    try {
      const [c, ff] = await Promise.all([api.loadConfig(), api.loadFFmpegStatus()]);
      setCfg(c);
      if (!(c && c.stream) && typeof c?.path === "string") {
        setPath(c.path);
      } else {
        setPath(typeof c?.path === "string" ? c.path : "");
      }
      setFfmpeg(ff);
    } catch (e) {
      notify("err", "加载失败: " + (e as Error).message);
    }
  }, [notify]);

  useEffect(() => {
    refresh();
    const t = setInterval(async () => {
      try {
        const d = await api.loadStats();
        setStats((d.streams as StreamStatus[]) || []);
      } catch {
        /* ignore */
      }
    }, 2000);
    return () => clearInterval(t);
  }, [refresh]);

  const streamNames = useMemo(() => Object.keys(cfg).filter((k) => k !== "path").sort(), [cfg]);
  const statsMap = useMemo(() => Object.fromEntries(stats.map((s) => [s.name, s])), [stats]);

  const saveAll = async (nextCfg?: Record<string, any>) => {
    try {
      await api.saveConfig(nextCfg || cfg);
      notify("ok", "配置已保存");
      await refresh();
    } catch (e) {
      notify("err", "保存失败: " + (e as Error).message);
    }
  };

  const savePath = () => {
    saveAll({ ...cfg, path });
  };

  const toggle = async (name: string) => {
    const item = cfg[name];
    if (!item) return;
    if (!window.confirm(item.enabled ? "确定要关闭推流吗？" : "确定要开启推流吗？")) return;
    const next = { ...cfg, [name]: { ...item, enabled: !item.enabled } };
    await saveAll(next);
  };

  const remove = (name: string) => {
    setPendingDelete(name);
  };

  const confirmRemove = async () => {
    const name = pendingDelete;
    setPendingDelete(null);
    if (!name) return;
    const next = { ...cfg };
    delete next[name];
    await saveAll(next);
  };

  const openEdit = (name?: string) => {
    if (name) {
      setEditing({ name, isNew: false });
      setDraft(deepClone(cfg[name]));
    } else {
      setEditing({ name: "", isNew: true });
      setDraft(defaultStream());
    }
  };

  const saveStream = async () => {
    if (!editing) return;
    let name = editing.name;
    if (editing.isNew) {
      name = (name || "").trim();
      if (!name) return notify("err", "请输入推流名称");
      if (!/^[a-zA-Z0-9_-]+$/.test(name)) return notify("err", "推流名称只能包含字母、数字、下划线和连字符");
      if (cfg[name]) return notify("err", "已存在同名推流: " + name);
    }
    if (!getNested(draft, "stream.source.url")) return notify("err", "源地址 URL 不能为空");
    const next = { ...cfg, [name]: deepClone(draft) } as Record<string, any>;
    await saveAll(next);
    setEditing(null);
  };

  const statFor = (name: string) => statsMap[name];
  // 播放地址前缀跟随配置的 Publisher Path（路由按它挂载），不能硬编码
  const pubBase = (() => {
    const p = (path || "").trim();
    const norm = p.startsWith("/") ? p : "/" + p;
    return norm.endsWith("/") ? norm : norm + "/";
  })();

  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const copyPlayUrl = async (key: string, url: string) => {
    try {
      if (navigator.clipboard) {
        await navigator.clipboard.writeText(url);
      } else {
        const ta = document.createElement("textarea");
        ta.value = url;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
      setCopiedKey(key);
      setTimeout(() => setCopiedKey((k) => (k === key ? null : k)), 2000);
    } catch {
      notify("err", "复制失败，请手动复制地址");
    }
  };

  // 同源播放：跳转独立播放入口（/pp）并注入 live 参数，播放器页直接用浏览器播放该 FLV/HLS。
  // 只在 /pp 公开入口打开，不经过 web.path，也不泄露后台路径。
  const playDirect = (url: string) => {
    window.open(window.location.origin + "/pp?live=" + encodeURIComponent(url), "_blank", "noopener");
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">推流发布</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={refresh}>
            <RefreshCw className="mr-1 h-4 w-4" /> 刷新
          </Button>
          <Button variant="outline" size="sm" onClick={savePath}>
            <Save className="mr-1 h-4 w-4" /> 保存路径
          </Button>
          <Button size="sm" onClick={() => openEdit(undefined)}>
            <Plus className="mr-1 h-4 w-4" /> 新增推流
          </Button>
        </div>
      </div>

      {ffmpeg && !ffmpeg.installed && (
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-600 dark:text-amber-400">
          <strong>未检测到 FFmpeg</strong>：推流相关功能无法正常工作
          {ffmpeg.error && <div className="text-xs opacity-80">错误：{ffmpeg.error}</div>}
          {ffmpeg.hint && <div className="text-xs opacity-80">{ffmpeg.hint}</div>}
        </div>
      )}
      {ffmpeg && ffmpeg.installed && (
        <div className="rounded-lg border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600 dark:text-emerald-400">
          FFmpeg 已安装{ffmpeg.version ? "：" + ffmpeg.version : ""}
          {ffmpeg.path ? "（" + ffmpeg.path + "）" : ""}
        </div>
      )}

      {note && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${note.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {note.msg}
        </div>
      )}

      <Card>
        <CardContent className="space-y-3 p-3">
          <div>
            <Label>Publisher Path</Label>
            <Input value={path} onChange={(e) => setPath(e.target.value)} placeholder="例如 /live/ 或自定义路径" />
            <p className="mt-1 text-xs text-muted-foreground">修改后点击“保存路径”。推流状态和统计会自动刷新。</p>
          </div>
        </CardContent>
      </Card>

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {streamNames.length === 0 && (
          <Card>
            <CardContent className="flex flex-col items-center gap-2 py-8 text-muted-foreground">
              <MonitorPlay className="h-8 w-8" />
              <span className="text-sm">未配置推流，点击“新增推流”创建</span>
            </CardContent>
          </Card>
        )}
        {streamNames.map((name) => {
          const item = cfg[name];
          const st = statFor(name);
          const p = st?.primary;
          const flvItem = localPlays(getNested(item, "stream.local_play_urls")).find((x) => x.protocol === "flv");
          const hlsItem = localPlays(getNested(item, "stream.local_play_urls")).find((x) => x.protocol === "hls");
          // 本地播放地址用节点名（服务端按名称提供 /<path>/play/<名称>.flv|m3u8）；
          // streamkey 仅用于远端 RTMP 推流目标
          const playName = name;
          const flvRaw = flvItem?.enabled ? window.location.origin + pubBase + "play/" + playName + ".flv" : null;
          const hlsRaw = hlsItem?.enabled ? window.location.origin + pubBase + "play/" + playName + ".m3u8" : null;
          return (
            <Card key={name}>
              <CardContent className="p-0">
                <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
                  <span className="truncate font-semibold" title={name}>
                    {name}
                  </span>
                  <Badge variant={item.enabled ? "default" : "outline"}>{item.enabled ? "已启用" : "已关闭"}</Badge>
                </div>
                <dl className="space-y-1 px-3 py-2 text-xs">
                  <div className="flex gap-2">
                    <dt className="w-20 shrink-0 text-muted-foreground">协议</dt>
                    <dd className="truncate">{item.protocol || "-"}</dd>
                  </div>
                  <div className="flex gap-2">
                    <dt className="w-20 shrink-0 text-muted-foreground">源地址</dt>
                    <dd className="flex min-w-0 items-center gap-1.5">
                      <span className="truncate" title={getNested(item, "stream.source.url") || "-"}>
                        {getNested(item, "stream.source.url") || "-"}
                      </span>
                      {getNested(item, "stream.source.url") && (
                        <button
                          type="button"
                          onClick={() => copyPlayUrl(name + ":src", getNested(item, "stream.source.url"))}
                          className="inline-flex shrink-0 cursor-pointer items-center gap-0.5 rounded border px-1.5 py-0.5 text-[11px] transition-colors"
                          title="复制源地址：外部播放器（VLC 等）可直连此地址，不经 TVGate 转发流量"
                        >
                          <Copy className="h-3 w-3" aria-hidden="true" />
                          {copiedKey === name + ":src" ? "已复制" : "直连"}
                        </button>
                      )}
                    </dd>
                  </div>
                  <div className="flex gap-2">
                    <dt className="w-20 shrink-0 text-muted-foreground">主推地址</dt>
                    <dd className="truncate" title={getNested(item, "stream.receivers.primary.push_url") || "-"}>
                      {getNested(item, "stream.receivers.primary.push_url") || "-"}
                    </dd>
                  </div>
                  {flvItem?.enabled && flvRaw && (
                    <div className="flex gap-2">
                      <dt className="w-20 shrink-0 text-muted-foreground">本地 FLV</dt>
                      <dd className="flex min-w-0 items-center gap-1.5">
                        <a
                          className="truncate text-primary hover:underline"
                          href={window.location.origin + "/pp?live=" + encodeURIComponent(flvRaw)}
                          target="_blank"
                          rel="noreferrer"
                          title="点击用浏览器播放（同源，不经 TVGate 转发）"
                        >
                          {flvRaw}
                        </a>
                        <button
                          type="button"
                          onClick={() => playDirect(flvRaw)}
                          className="inline-flex shrink-0 cursor-pointer items-center gap-0.5 rounded border border-primary/30 px-1.5 py-0.5 text-primary text-[11px] transition-colors hover:bg-primary/10"
                          title="点击用浏览器播放（同源，不经 TVGate 转发）"
                        >
                          <MonitorPlay className="h-3 w-3" aria-hidden="true" />
                          播放
                        </button>
                        <button
                          type="button"
                          onClick={() => copyPlayUrl(name + ":flv", flvRaw)}
                          className="inline-flex shrink-0 cursor-pointer items-center gap-0.5 rounded border border-violet-500/25 px-1.5 py-0.5 text-violet-600 text-[11px] transition-colors hover:bg-violet-500/10 dark:border-violet-300/25 dark:text-violet-300 dark:hover:bg-violet-300/10"
                          title="复制 FLV 地址到剪贴板（可粘贴到 VLC/播放器）"
                        >
                          <Copy className="h-3 w-3" aria-hidden="true" />
                          {copiedKey === name + ":flv" ? "已复制" : "复制"}
                        </button>
                      </dd>
                    </div>
                  )}
                  {hlsItem?.enabled && hlsRaw && (
                    <div className="flex gap-2">
                      <dt className="w-20 shrink-0 text-muted-foreground">本地 HLS</dt>
                      <dd className="flex min-w-0 items-center gap-1.5">
                        <a
                          className="truncate text-primary hover:underline"
                          href={window.location.origin + "/pp?live=" + encodeURIComponent(hlsRaw)}
                          target="_blank"
                          rel="noreferrer"
                          title="点击用浏览器播放（同源，不经 TVGate 转发）"
                        >
                          {hlsRaw}
                        </a>
                        <button
                          type="button"
                          onClick={() => playDirect(hlsRaw)}
                          className="inline-flex shrink-0 cursor-pointer items-center gap-0.5 rounded border border-primary/30 px-1.5 py-0.5 text-primary text-[11px] transition-colors hover:bg-primary/10"
                          title="点击用浏览器播放（同源，不经 TVGate 转发）"
                        >
                          <MonitorPlay className="h-3 w-3" aria-hidden="true" />
                          播放
                        </button>
                        <button
                          type="button"
                          onClick={() => copyPlayUrl(name + ":hls", hlsRaw)}
                          className="inline-flex shrink-0 cursor-pointer items-center gap-0.5 rounded border border-violet-500/25 px-1.5 py-0.5 text-violet-600 text-[11px] transition-colors hover:bg-violet-500/10 dark:border-violet-300/25 dark:text-violet-300 dark:hover:bg-violet-300/10"
                          title="复制 HLS 地址到剪贴板（可粘贴到 VLC/播放器）"
                        >
                          <Copy className="h-3 w-3" aria-hidden="true" />
                          {copiedKey === name + ":hls" ? "已复制" : "复制"}
                        </button>
                      </dd>
                    </div>
                  )}
                  <div className="flex gap-2">
                    <dt className="w-20 shrink-0 text-muted-foreground">运行状态</dt>
                    <dd>{!st ? "-" : !st.has_manager ? "Publisher 未初始化" : !p ? (item.enabled ? "等待启动" : "未启用") : p.running ? `运行中 (PID ${p.pid})` : "未运行"}</dd>
                  </div>
                  {p && (
                    <>
                      <div className="flex gap-2">
                        <dt className="w-20 shrink-0 text-muted-foreground">码率</dt>
                        <dd>{fmtBitrate(p.current_bitrate)} (avg {fmtBitrate(p.avg_bitrate)})</dd>
                      </div>
                      <div className="flex gap-2">
                        <dt className="w-20 shrink-0 text-muted-foreground">CPU</dt>
                        <dd>{(p.cpu_percent || 0).toFixed(1)} %</dd>
                        <dt className="w-20 shrink-0 text-muted-foreground">内存</dt>
                        <dd>{fmtBytes(p.memory_rss)}</dd>
                      </div>
                      <div className="flex gap-2">
                        <dt className="w-20 shrink-0 text-muted-foreground">累计输出</dt>
                        <dd>{fmtBytes(p.bytes_transferred)}</dd>
                        <dt className="w-20 shrink-0 text-muted-foreground">时长</dt>
                        <dd>{fmtDur(p.duration)}</dd>
                      </div>
                      <div className="flex gap-2">
                        <dt className="w-20 shrink-0 text-muted-foreground">重启次数</dt>
                        <dd>{p.restarts || 0}</dd>
                      </div>
                    </>
                  )}
                </dl>
                {p?.last_error && (
                  <div className="mx-3 mb-2 rounded border border-destructive/40 bg-destructive/10 px-2 py-1 text-xs text-destructive">{p.last_error}</div>
                )}
                <div className="flex flex-wrap gap-2 border-t px-3 py-2">
                  <Button size="sm" variant={item.enabled ? "destructive" : "default"} onClick={() => toggle(name)}>
                    {item.enabled ? "关闭推流" : "开启推流"}
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => openEdit(name)}>
                    编辑
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => remove(name)}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      {editing && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={() => setEditing(null)}>
          <div className="flex max-h-[90vh] w-full max-w-3xl flex-col overflow-hidden rounded-xl border border-border bg-background" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center justify-between border-b px-4 py-3">
              <h3 className="font-semibold">{editing.isNew ? "新增推流" : "编辑推流：" + editing.name}</h3>
              <Button size="icon" variant="ghost" onClick={() => setEditing(null)}>
                <X className="h-4 w-4" />
              </Button>
            </div>
            <div className="flex-1 space-y-4 overflow-y-auto p-4">
              <StreamForm draft={draft} setDraft={setDraft} />
            </div>
            <div className="flex justify-end gap-2 border-t px-4 py-3">
              {!editing.isNew && (
                <Button variant="destructive" onClick={() => {
                  const n = editing.name;
                  setEditing(null);
                  remove(n);
                }}>
                  <Trash2 className="mr-1 h-4 w-4" /> 删除推流
                </Button>
              )}
              <Button onClick={saveStream}>
                <Save className="mr-1 h-4 w-4" /> 保存
              </Button>
            </div>
          </div>
        </div>
      )}

      {pendingDelete !== null && (
        <ConfirmDialog
          title="确认删除推流"
          description={`确定删除推流「${pendingDelete}」吗？删除后不可恢复（会写入配置文件备份）。`}
          onConfirm={confirmRemove}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </div>
  );
}

function Field({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={className}>
      <Label className="mb-1 block text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}

function StreamForm({ draft, setDraft }: { draft: StreamItem; setDraft: React.Dispatch<React.SetStateAction<StreamItem>> }) {
  const stream = ensureObject(draft, "stream");
  const source = ensureObject(stream, "source");
  const sk = draft.streamkey || { type: "" };
  const plays = localPlays(stream.local_play_urls);
  const flv = plays.find((x) => x.protocol === "flv");
  const hls = plays.find((x) => x.protocol === "hls");

  const set = (patch: Partial<StreamItem>) => setDraft((d) => ({ ...d, ...patch }));
  const setStream = (patch: Partial<typeof stream>) => setDraft((d) => ({ ...d, stream: { ...d.stream, ...patch } }));
  const setSource = (patch: Partial<typeof source>) => setStream({ source: { ...stream.source, ...patch } });

  // local play urls 更新辅助
  const upsertPlay = (protocol: string, patch: Partial<PlayOutput>) => {
    const list = localPlays(stream.local_play_urls);
    const idx = list.findIndex((x) => x.protocol === protocol);
    if (idx >= 0) list[idx] = { ...list[idx], protocol, ...patch };
    else list.push({ protocol, enabled: true, ...patch } as PlayOutput);
    setStream({ local_play_urls: list });
  };
  const removePlay = (protocol: string) => {
    setStream({ local_play_urls: localPlays(stream.local_play_urls).filter((x) => x.protocol !== protocol) });
  };

  const setSk = (patch: Partial<typeof sk>) => {
    if (!sk.type) return;
    set({ streamkey: { ...sk, ...patch } });
  };

  const receivers = ensureObject(stream, "receivers");
  const setReceiver = (key: "primary" | "backup", pushUrl: string) => {
    const cur = receivers[key];
    const item: ReceiverItem = cur && typeof cur === "object" ? { ...cur, push_url: pushUrl } : { push_url: pushUrl };
    setStream({ receivers: { ...receivers, [key]: item } });
  };
  const setAllReceivers = (list: ReceiverItem[]) => setStream({ receivers: { ...receivers, all: list } });

  const allList: ReceiverItem[] = Array.isArray(receivers.all) ? receivers.all.map((x: any) => (typeof x === "object" && x ? { ...x } : { push_url: "" })) : [];

  // TS 模板选项
  const tsTpl = hls?.ts_filename_template || "";
  const tsPreset = ["", "name_index", "epoch_hls", "camera_hls", "epoch_dash", "numeric", "date_underscore", "date_T"].includes(tsTpl);
  const tsOptions = ["name_index", "epoch_hls", "camera_hls", "epoch_dash", "numeric", "date_underscore", "date_T"];

  return (
    <>
      <section className="rounded-lg border border-border p-3">
        <h4 className="mb-2 text-sm font-semibold">基础</h4>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Field label="协议">
            <select className="h-9 w-full rounded-[var(--radius)] border border-input bg-background px-2 text-sm" value={draft.protocol} onChange={(e) => set({ protocol: e.target.value })}>
              {["rtmp", "http", "https", "srt", "udp"].map((p) => (
                <option key={p} value={p}>{p}</option>
              ))}
            </select>
          </Field>
          <Field label="模式">
            <select className="h-9 w-full rounded-[var(--radius)] border border-input bg-background px-2 text-sm" value={stream.mode || "primary-backup"} onChange={(e) => setStream({ mode: e.target.value })}>
              <option value="primary-backup">primary-backup（主推/备推）</option>
              <option value="all">all（同时推多个）</option>
              <option value="local-only">local-only（不推送，仅本地播放/录制）</option>
            </select>
          </Field>
          <Field label="源地址 URL">
            <Input value={source.url || ""} onChange={(e) => setSource({ url: e.target.value })} placeholder="例如 http://... 或 udp://..." />
          </Field>
          <Field label="备用源 URL（可选）">
            <Input value={source.backup_url || ""} onChange={(e) => setSource({ backup_url: e.target.value })} placeholder="例如 http://..." />
          </Field>
          <div className="flex items-end gap-3 pb-1">
            <Label className="text-xs text-muted-foreground">启用推流</Label>
            <Switch checked={!!draft.enabled} onCheckedChange={(v) => set({ enabled: v })} />
          </div>
          <Field label="Buffer Size">
            <Input type="number" value={draft.buffer_size ?? 0} onChange={(e) => set({ buffer_size: Number(e.target.value || 0) })} />
          </Field>
        </div>
      </section>

      <section className="rounded-lg border border-border p-3">
        <h4 className="mb-2 text-sm font-semibold">StreamKey</h4>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Field label="类型">
          <select className="h-9 w-full rounded-[var(--radius)] border border-input bg-background px-2 text-sm" value={sk.type || ""} onChange={(e) => {
              const t = e.target.value;
              const next: any = t ? { type: t, ...(t === "fixed" ? { value: sk.value } : t === "random" ? { length: sk.length, expiration: sk.expiration } : {}) } : {};
              set({ streamkey: next });
            }}>
              <option value="">空（本地直播）</option>
              <option value="random">random</option>
              <option value="fixed">fixed</option>
              <option value="external">external</option>
            </select>
          </Field>
          {sk.type === "fixed" && (
            <Field label="固定值">
              <Input value={sk.value || ""} onChange={(e) => setSk({ value: e.target.value })} placeholder="固定 streamkey 值" />
            </Field>
          )}
          {sk.type === "random" && (
            <>
              <Field label="随机长度">
                <Input type="number" min={0} value={sk.length ?? ""} onChange={(e) => setSk({ length: Number(e.target.value || 0) })} placeholder="例如 16" />
              </Field>
              <Field label="过期时间（可选）">
                <Input value={sk.expiration || ""} onChange={(e) => setSk({ expiration: e.target.value })} placeholder="例如 24h" />
              </Field>
            </>
          )}
          {sk.type === "external" && (
            <div className="rounded bg-amber-500/10 px-3 py-2 text-xs text-amber-600 md:col-span-2">
              external 直播可用根据直播前端来提示
            </div>
          )}
          {!sk.type && (
            <div className="rounded bg-emerald-500/10 px-3 py-2 text-xs text-emerald-600 md:col-span-2">
              当前为本地直播模式。如果开启了本地 FLV/HLS，请在列表页查看播放地址。
            </div>
          )}
        </div>
      </section>

      <section className="space-y-3 rounded-lg border border-border p-3">
        <h4 className="mb-1 text-sm font-semibold">FFmpegOptions（源）</h4>
        <FFmpegEditor title="源 FFmpegOptions" value={source.ffmpeg_options} onChange={(v) => setSource({ ffmpeg_options: v })} />
      </section>

      <section className="rounded-lg border border-border p-3">
        <h4 className="mb-2 text-sm font-semibold">本地播放 / 录制</h4>
        <div className="grid grid-cols-1 gap-3">
          <div className="flex flex-wrap items-center gap-x-8 gap-y-3">
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" className="accent-[hsl(var(--primary))]" checked={!!flv?.enabled} onChange={(e) => (e.target.checked ? upsertPlay("flv", { enabled: true, protocol: "flv" }) : removePlay("flv"))} />
              开启本地 FLV 播放
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" className="accent-[hsl(var(--primary))]" checked={!!hls?.enabled} onChange={(e) => (e.target.checked ? upsertPlay("hls", { enabled: true, protocol: "hls" }) : removePlay("hls"))} />
              开启本地 HLS（可用于录制/回放）
            </label>
            <Switch className="hidden" checked={false} onCheckedChange={() => undefined} aria-hidden />
          </div>

          {flv?.enabled && (
            <FFmpegEditor title="自定义 FLV FFmpegOptions" value={flv.flv_ffmpeg_options} onChange={(v) => upsertPlay("flv", { flv_ffmpeg_options: v })} />
          )}
          {hls?.enabled && (
            <FFmpegEditor title="自定义 HLS FFmpegOptions" value={hls.hls_ffmpeg_options} onChange={(v) => upsertPlay("hls", { hls_ffmpeg_options: v })} />
          )}
          {hls?.enabled && (
            <>
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <Field label="HLS 片段时长（秒）">
                  <Input type="number" min={1} value={(hls as any).hls_segment_duration ?? ""} onChange={(e) => upsertPlay("hls", { hls_segment_duration: Number(e.target.value || 0) })} placeholder="例如 2" />
                </Field>
                <Field label="HLS 片段数量">
                  <Input type="number" min={1} value={(hls as any).hls_segment_count ?? ""} onChange={(e) => upsertPlay("hls", { hls_segment_count: Number(e.target.value || 0) })} placeholder="例如 6" />
                </Field>
              </div>
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <Field label="HLS 存储路径（可选）">
                  <Input value={(hls as any).hls_path || ""} onChange={(e) => upsertPlay("hls", { hls_path: e.target.value })} placeholder="例如 ./hls 或 /data/hls" />
                </Field>
                <label className="flex items-end gap-2 pb-2 text-sm">
                  <input type="checkbox" className="accent-[hsl(var(--primary))]" checked={!!(hls as any).hls_enable_playback} onChange={(e) => upsertPlay("hls", { hls_enable_playback: e.target.checked })} />
                  开启回放（保留 TS）
                </label>
              </div>
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <Field label="TS 保留时长（例如 24h / 7d，可选）">
                  <Input value={(hls as any).hls_retention_days || ""} onChange={(e) => upsertPlay("hls", { hls_retention_days: e.target.value })} placeholder="例如 24h" />
                </Field>
                <Field label="TS 文件名模板（可选）">
                  {tsTpl && !tsPreset ? (
                    <div className="flex gap-1">
                      <Input value={tsTpl} onChange={(e) => upsertPlay("hls", { ts_filename_template: e.target.value })} placeholder="{stream}/{date}/{seq}.ts" />
                      <Button variant="outline" type="button" onClick={() => upsertPlay("hls", { ts_filename_template: "" })} className="h-9 shrink-0">
                        <X className="h-4 w-4" />
                      </Button>
                    </div>
                  ) : (
                    <select className="h-9 w-full rounded-[var(--radius)] border border-input bg-background px-2 text-sm" value={tsTpl} onChange={(e) => upsertPlay("hls", { ts_filename_template: e.target.value ? e.target.value : undefined })}>
                      <option value="">默认 (name_index)</option>
                      {tsOptions.map((t) => (
                        <option key={t} value={t}>{t}</option>
                      ))}
                    </select>
                  )}
                </Field>
              </div>
            </>
          )}
        </div>
      </section>

      {(stream.mode === "primary-backup" || !stream.mode) && (
        <section className="space-y-3 rounded-lg border border-border p-3">
          <h4 className="mb-1 text-sm font-semibold">接收端（主/备）</h4>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Field label="Primary Push URL">
              <Input value={receivers.primary?.push_url || ""} onChange={(e) => setReceiver("primary", e.target.value)} placeholder="例如 rtmp://host/app" />
            </Field>
            <Field label="Backup Push URL（可选）">
              <Input value={receivers.backup?.push_url || ""} onChange={(e) => setReceiver("backup", e.target.value)} placeholder="例如 rtmp://host/app" />
            </Field>
          </div>
          <FFmpegEditor key="primary" title="Primary 自定义 FFmpegOptions" value={receivers.primary?.ffmpeg_options} onChange={(v) => setStream({ receivers: { ...receivers, primary: { push_url: receivers.primary?.push_url || "", ffmpeg_options: v } } })} />
          <FFmpegEditor key="backup" title="Backup 自定义 FFmpegOptions" value={receivers.backup?.ffmpeg_options} onChange={(v) => setStream({ receivers: { ...receivers, backup: { push_url: receivers.backup?.push_url || "", ffmpeg_options: v } } })} />
        </section>
      )}

      {stream.mode === "all" && (
        <section className="space-y-3 rounded-lg border border-border p-3">
          <div className="flex items-center justify-between">
            <h4 className="text-sm font-semibold">接收端（同时推多个）</h4>
            <Button size="sm" variant="outline" onClick={() => setAllReceivers([...allList, { push_url: "" }])}>
              <Plus className="mr-1 h-4 w-4" /> 新增接收端
            </Button>
          </div>
          {allList.length === 0 && <p className="text-xs text-muted-foreground">暂无接收端，点“新增接收端”添加</p>}
          {allList.map((r, i) => (
            <div key={i} className="space-y-2 rounded border border-border p-2">
              <Input value={r.push_url} placeholder="Push URL" onChange={(e) => setAllReceivers(allList.map((x, j) => (j === i ? { ...x, push_url: e.target.value } : x)))} />
              <FFmpegEditor key={i} title={`接收端 ${i + 1} 自定义 FFmpegOptions`} value={r.ffmpeg_options} onChange={(v) => setAllReceivers(allList.map((x, j) => (j === i ? { ...x, ffmpeg_options: v } : x)))} />
              <Button size="sm" variant="destructive" onClick={() => setAllReceivers(allList.filter((_, j) => j !== i))}>
                <Trash2 className="mr-1 h-4 w-4" /> 移除
              </Button>
            </div>
          ))}
        </section>
      )}
    </>
  );
}