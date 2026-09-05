import { useCallback, useEffect, useState } from "react";
import { Link2, Tv, Save } from "lucide-react";
import { AsyncActionButton } from "@/components/config/async-action-button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { getPlayer, savePlayer, type PlayerConfig } from "@/api/player";

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

export function PlayerPage() {
  const [cfg, setCfg] = useState<PlayerConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const [copied, setCopied] = useState(false);
  // 独立播放入口外链（跟随当前访问的 host:port）。只展示 /pp——
  // 它是不暴露后台路径的公开地址，可直接分享给电视/手机/其他播放器。
  const externalLink = `${window.location.origin}/pp`;

  const copyExternal = async () => {
    // HTTP 局域网环境无 navigator.clipboard（非安全上下文），退化为 execCommand
    try {
      if (navigator.clipboard) {
        await navigator.clipboard.writeText(externalLink);
      } else {
        const ta = document.createElement("textarea");
        ta.value = externalLink;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setNotice({ type: "err", msg: `复制失败，请手动复制：${externalLink}` });
      setTimeout(() => setNotice(null), 6000);
    }
  };

  const refresh = useCallback(async () => setCfg(await getPlayer()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;

  const save = async () => {
    if (cfg.enabled && !cfg.subscription.trim()) {
      setNotice({ type: "err", msg: "启用时必须填写订阅源" });
      setTimeout(() => setNotice(null), 4000);
      return;
    }
    try {
      await savePlayer({ ...cfg, subscription: cfg.subscription.trim(), epg: cfg.epg.trim(), logo: cfg.logo.trim(), logo_dir: cfg.logo_dir.trim(), update_interval: cfg.update_interval.trim(), ua: cfg.ua.trim() });
      setNotice({ type: "ok", msg: "配置保存成功，热加载将自动刷新" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">H5 播放器配置</h1>
        <div className="flex flex-wrap items-center gap-2">
          <AsyncActionButton variant="secondary" action={refresh} busyText="加载中…">重新加载</AsyncActionButton>
          <AsyncActionButton action={save} busyText="保存中…"><Save className="mr-1 h-4 w-4" />保存配置</AsyncActionButton>
          <button
            type="button"
            onClick={copyExternal}
            className="inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-sm transition-colors"
            title="复制独立播放入口外链（可分享给电视/手机，不含后台路径）"
          >
            <Link2 className="h-4 w-4" aria-hidden="true" />
            {copied ? "已复制" : "复制外链"}
          </button>
          <a
            href="player"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 rounded-lg border border-violet-500/30 bg-violet-500/10 px-3 py-1.5 text-violet-700 text-sm transition-colors hover:bg-violet-500/20 dark:border-violet-300/30 dark:bg-violet-300/10 dark:text-violet-200 dark:hover:bg-violet-300/20"
            title="在新标签页打开 H5 播放页"
          >
            <Tv className="h-4 w-4" aria-hidden="true" />
            打开播放页观看
          </a>
        </div>
      </div>
      {cfg.enabled && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-violet-500/15 bg-violet-500/5 px-3 py-2 text-xs dark:border-violet-300/15 dark:bg-violet-300/5">
          <span className="text-muted-foreground">外链（分享给电视/手机，不含后台路径）：</span>
          <code className="font-mono text-violet-700 dark:text-violet-200">{externalLink}</code>
          <code className="text-muted-foreground">/pp/&lt;频道key&gt;</code>
        </div>
      )}
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}
      <Card>
        <CardHeader><CardTitle className="text-base">基本配置</CardTitle></CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-2">
            <Switch checked={cfg.enabled} onCheckedChange={(v) => setCfg({ ...cfg, enabled: v })} />
            <span className="text-sm">启用播放器模块</span>
          </div>
          <div className="flex items-center gap-2">
            <Switch checked={cfg.android_autoplay} onCheckedChange={(v) => setCfg({ ...cfg, android_autoplay: v })} />
            <span className="text-sm">安卓设备启动进入播放页</span>
          </div>
          <p className="text-xs text-muted-foreground">YAML 标记位（player.android_autoplay）：安卓客户端 App 读取该标记自行控制启动是否进入播放页；未配置默认不进入，显式开启后才自动进入。本服务不做任何行为控制。</p>
          <p className="text-xs text-muted-foreground">开启后挂载 /api/player/channels、/player/&lt;key&gt;、/api/player/epg 与播放页 /web/player；/pp/ 为独立播放页入口（不跳转、不暴露后台路径）</p>
          <Field label="订阅源" hint="M3U 或 逗号TXT；此地址 = 允许拉取的源白名单，真实流地址不外露">
            <Input
              className="font-mono"
              value={cfg.subscription}
              onChange={(e) => setCfg({ ...cfg, subscription: e.target.value })}
              placeholder="https://&lt;your-domain&gt;/sub.m3u 或本地文件路径"
            />
            <div className="mt-2 space-y-1.5 rounded-lg border border-violet-900/10 bg-violet-50/40 p-3 text-xs leading-5 text-muted-foreground dark:border-violet-100/10 dark:bg-violet-300/5">
              <p className="font-medium text-foreground">支持的订阅源地址写法：</p>
              <ul className="list-inside list-disc space-y-0.5">
                <li><code className="font-mono text-violet-700 dark:text-violet-200">https://… / http://…</code> — 远程订阅 URL</li>
                <li><code className="font-mono text-violet-700 dark:text-violet-200">/opt/tvgate/tv.txt</code> — 本地绝对路径</li>
                <li><code className="font-mono text-violet-700 dark:text-violet-200">file:///opt/tvgate/tv.txt</code> — file:// 前缀本地路径</li>
                <li><code className="font-mono text-violet-700 dark:text-violet-200">php://sub/tv.txt</code> — 相对 PHP docroot（也可 http://&lt;host&gt;/php/sub.php?id=x）</li>
                <li><code className="font-mono text-violet-700 dark:text-violet-200">tv.txt</code> / <code className="font-mono text-violet-700 dark:text-violet-200">sub</code> — 裸相对路径，基准为 docroot</li>
              </ul>
              <p className="pt-1">
                以上写法均可指向<b className="text-foreground">目录</b>（如 <code className="font-mono">/www/tv/</code>、<code className="font-mono">php://tv/</code>）：
                递归收集其中 <code className="font-mono">.txt</code> / <code className="font-mono">.m3u</code> / <code className="font-mono">.m3u8</code>
                （跳过隐藏文件），按路径名排序逐文件解析后合并，同 URL 自动去重，单文件上限 64MB。
                内容以 <code className="font-mono">#EXTM3U</code> 开头按 M3U 解析，否则按逗号 TXT 解析（详见 <code className="font-mono">doc/PLAYER.md</code>）。
              </p>
            </div>
          </Field>
          <Field label="txt 订阅的 EPG 模板（可选）" hint="含 {name}=频道名、{date}=日期；M3U 订阅用 x-tvg-url 的 XMLTV，无需填此项">
            <Input
              className="font-mono"
              value={cfg.epg}
              onChange={(e) => setCfg({ ...cfg, epg: e.target.value })}
              placeholder="https://epg.&lt;your-domain&gt;/?ch={name}&date={date}"
            />
          </Field>
          <Field label="台标模板（可选，M3U/txt 无 tvg-logo 时兜底）" hint="含 {name}=频道名；M3U 自带 tvg-logo 优先">
            <Input
              className="font-mono"
              value={cfg.logo}
              onChange={(e) => setCfg({ ...cfg, logo: e.target.value })}
              placeholder="https://logo.&lt;your-domain&gt;/{name}.png"
            />
          </Field>
          <Field label="本地台标目录（可选）" hint="填此目录则频道 logo 取 &lt;频道名&gt;.png（经 /player/logo/ 服务），优先于上方模板">
            <Input
              className="font-mono"
              value={cfg.logo_dir}
              onChange={(e) => setCfg({ ...cfg, logo_dir: e.target.value })}
              placeholder="/opt/TVLogo"
            />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="订阅刷新间隔" hint="如 2h / 30m，留空用默认 2h">
              <Input value={cfg.update_interval} onChange={(e) => setCfg({ ...cfg, update_interval: e.target.value })} placeholder="2h" />
            </Field>
            <Field label="默认 User-Agent（可选）" hint="请求上游（m3u8/分片）用；频道在 txt 里带 ua=xxx 则优先生效，否则用此默认。留空用内置浏览器 UA">
              <Input className="font-mono" value={cfg.ua} onChange={(e) => setCfg({ ...cfg, ua: e.target.value })} placeholder="okhttp/3.8.1" />
            </Field>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}