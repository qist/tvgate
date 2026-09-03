import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { getServer, saveServer, type ServerConfig } from "@/api/server";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function ServerPage() {
  const [cfg, setCfg] = useState<ServerConfig | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setCfg(await getServer()), []);
  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;

  const setTLS = (p: Partial<ServerConfig["tls"]>) => setCfg({ ...cfg, tls: { ...cfg.tls, ...p } });

  const save = async () => {
    try {
      await saveServer(cfg);
      setNotice({ type: "ok", msg: "配置保存成功，若端口/证书变更将重启服务" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">服务器</h1>
      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}

      <Card>
        <CardHeader><CardTitle className="text-base">基础</CardTitle></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          <Field label="端口 (port)"><Input type="number" value={cfg.port} onChange={(e) => setCfg({ ...cfg, port: +e.target.value || 0 })} /></Field>
          <Field label="HTTP 端口 (http_port)"><Input type="number" value={cfg.http_port} onChange={(e) => setCfg({ ...cfg, http_port: +e.target.value || 0 })} /></Field>
          <Field label="证书文件"><Input className="font-mono" value={cfg.certfile} onChange={(e) => setCfg({ ...cfg, certfile: e.target.value })} /></Field>
          <Field label="私钥文件"><Input className="font-mono" value={cfg.keyfile} onChange={(e) => setCfg({ ...cfg, keyfile: e.target.value })} /></Field>
          <Field label="SSL 协议"><Input value={cfg.ssl_protocols} onChange={(e) => setCfg({ ...cfg, ssl_protocols: e.target.value })} /></Field>
          <Field label="SSL 密码套件"><Input value={cfg.ssl_ciphers} onChange={(e) => setCfg({ ...cfg, ssl_ciphers: e.target.value })} /></Field>
          <Field label="SSL ECDH 曲线"><Input value={cfg.ssl_ecdh_curve} onChange={(e) => setCfg({ ...cfg, ssl_ecdh_curve: e.target.value })} /></Field>
          <div className="flex items-end">
            <label className="flex items-center gap-1.5 text-sm"><Switch checked={cfg.http_to_https} onCheckedChange={(v) => setCfg({ ...cfg, http_to_https: v })} /> HTTP 跳转 HTTPS</label>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle className="text-base">TLS</CardTitle></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          <Field label="HTTPS 端口"><Input type="number" value={cfg.tls.https_port} onChange={(e) => setTLS({ https_port: +e.target.value || 0 })} /></Field>
          <Field label="证书文件"><Input className="font-mono" value={cfg.tls.certfile} onChange={(e) => setTLS({ certfile: e.target.value })} /></Field>
          <Field label="私钥文件"><Input className="font-mono" value={cfg.tls.keyfile} onChange={(e) => setTLS({ keyfile: e.target.value })} /></Field>
          <Field label="SSL 协议"><Input value={cfg.tls.ssl_protocols} onChange={(e) => setTLS({ ssl_protocols: e.target.value })} /></Field>
          <Field label="SSL 密码套件"><Input value={cfg.tls.ssl_ciphers} onChange={(e) => setTLS({ ssl_ciphers: e.target.value })} /></Field>
          <Field label="SSL ECDH 曲线"><Input value={cfg.tls.ssl_ecdh_curve} onChange={(e) => setTLS({ ssl_ecdh_curve: e.target.value })} /></Field>
          <div className="flex items-end">
            <label className="flex items-center gap-1.5 text-sm"><Switch checked={cfg.tls.enable_h3} onCheckedChange={(v) => setTLS({ enable_h3: v })} /> 启用 HTTP/3</label>
          </div>
        </CardContent>
      </Card>

      <div className="flex gap-2">
        <Button onClick={save}>保存</Button>
        <Button variant="secondary" onClick={refresh}>重置</Button>
      </div>
    </div>
  );
}