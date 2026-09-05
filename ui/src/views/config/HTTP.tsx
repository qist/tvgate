import { useCallback, useEffect, useState } from "react";
import { AsyncActionButton } from "@/components/config/async-action-button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { getHTTP, saveHTTP, type HTTPConfig } from "@/api/httpCfg";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function HTTPPage() {
  const [cfg, setCfg] = useState<HTTPConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setCfg(await getHTTP()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;
  const patch = (p: Partial<HTTPConfig>) => setCfg({ ...cfg, ...p });

  const save = async () => {
    try {
      await saveHTTP(cfg);
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  const dur = (k: keyof HTTPConfig, label: string) => (
    <Field label={label}>
      <Input value={cfg[k] as string} onChange={(e) => patch({ [k]: e.target.value } as Partial<HTTPConfig>)} placeholder="例如: 10s" />
    </Field>
  );
  const num = (k: keyof HTTPConfig, label: string) => (
    <Field label={label}>
      <Input type="number" value={cfg[k] as number} onChange={(e) => patch({ [k]: +e.target.value || 0 } as Partial<HTTPConfig>)} />
    </Field>
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">HTTP</h1>
      </div>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}
      <Card>
        <CardHeader><CardTitle className="text-base">超时设置</CardTitle></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          {dur("timeout", "整体超时（0=不限）")}
          {dur("connect_timeout", "连接超时")}
          {dur("keepalive", "Keep-Alive")}
          {dur("response_header_timeout", "响应头超时")}
          {dur("idle_conn_timeout", "空闲连接超时")}
          {dur("tls_handshake_timeout", "TLS 握手超时")}
          {dur("expect_continue_timeout", "Expect-Continue 超时")}
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle className="text-base">连接池</CardTitle></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          {num("max_idle_conns", "最大空闲连接")}
          {num("max_idle_conns_per_host", "单主机最大空闲连接")}
          {num("max_conns_per_host", "单主机最大连接")}
          <div className="flex items-end gap-4">
            <label className="flex items-center gap-1.5 text-sm"><Switch checked={cfg.disable_keepalives} onCheckedChange={(v) => patch({ disable_keepalives: v })} /> 禁用 Keep-Alive</label>
            <label className="flex items-center gap-1.5 text-sm"><Switch checked={cfg.insecure_skip_verify} onCheckedChange={(v) => patch({ insecure_skip_verify: v })} /> 跳过 TLS 校验</label>
          </div>
        </CardContent>
      </Card>
      <div className="flex gap-2">
        <AsyncActionButton action={save} busyText="保存中…">保存</AsyncActionButton>
        <AsyncActionButton variant="secondary" action={refresh} busyText="加载中…">重新加载</AsyncActionButton>
      </div>
    </div>
  );
}