import { useCallback, useEffect, useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import { AsyncActionButton } from "@/components/config/async-action-button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { getGlobalAuth, saveGlobalAuth, CREDENTIAL_MASK, type AuthConfig } from "@/api/globalauth";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

function SecretInput({ value, masked, onToggle, onChange }: { value: string; masked: boolean; onToggle: () => void; onChange: (v: string) => void }) {
  return (
    <div className="relative">
      <Input
        type={masked ? "text" : "text"}
        className="pr-9 font-mono"
        value={masked ? CREDENTIAL_MASK : value}
        placeholder="留空不修改"
        onChange={(e) => {
          onChange(e.target.value);
        }}
      />
      {value ? (
        <button
          type="button"
          className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground"
          onClick={onToggle}
          title={masked ? "显示" : "隐藏"}
        >
          {masked ? <Eye className="h-4 w-4" /> : <EyeOff className="h-4 w-4" />}
        </button>
      ) : null}
    </div>
  );
}

export function GlobalAuthPage() {
  const [cfg, setCfg] = useState<AuthConfig | null>(null);
  const [masked, setMasked] = useState({ secret: true, salt: true, token: true });
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => {
    const c = await getGlobalAuth();
    setCfg(c);
    // 有真实值则默认打码
    setMasked({ secret: c.dynamic_tokens.secret !== "", salt: c.dynamic_tokens.salt !== "", token: c.static_tokens.token !== "" });
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  if (!cfg) return <div className="text-sm text-muted-foreground">加载中…</div>;

  const dt = cfg.dynamic_tokens;
  const st = cfg.static_tokens;

  const buildSubmit = (): AuthConfig => ({
    tokens_enabled: cfg.tokens_enabled,
    token_param_name: cfg.token_param_name,
    dynamic_tokens: {
      enable_dynamic: dt.enable_dynamic,
      dynamic_ttl: dt.dynamic_ttl,
      secret: masked.secret ? CREDENTIAL_MASK : dt.secret,
      salt: masked.salt ? CREDENTIAL_MASK : dt.salt,
    },
    static_tokens: {
      enable_static: st.enable_static,
      token: masked.token ? CREDENTIAL_MASK : st.token,
      expire_hours: st.expire_hours,
    },
  });

  const save = async () => {
    try {
      await saveGlobalAuth(buildSubmit());
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  const setDt = (p: Partial<typeof dt>) => setCfg({ ...cfg, dynamic_tokens: { ...dt, ...p } });
  const setSt = (p: Partial<typeof st>) => setCfg({ ...cfg, static_tokens: { ...st, ...p } });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">全局认证</h1>
      </div>

      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Token 全局认证设置</CardTitle>
        </CardHeader>
        <CardContent className="space-y-5">
          <div className="flex items-center gap-2">
            <Switch checked={cfg.tokens_enabled} onCheckedChange={(v) => setCfg({ ...cfg, tokens_enabled: v })} />
            <span className="text-sm">启用 Token 认证</span>
          </div>
          <Field label="Token 参数名">
            <Input value={cfg.token_param_name} onChange={(e) => setCfg({ ...cfg, token_param_name: e.target.value })} placeholder="token" />
          </Field>

          <div className="grid gap-4 sm:grid-cols-2 border-t pt-4">
            {/* 动态 Token */}
            <div className="space-y-3 rounded-lg border bg-muted/30 p-3">
              <div className="flex items-center gap-2">
                <Switch checked={dt.enable_dynamic} onCheckedChange={(v) => setDt({ enable_dynamic: v })} />
                <span className="text-sm font-medium">动态 Token</span>
              </div>
              <Field label="有效期（例: 1h）">
                <Input value={dt.dynamic_ttl} onChange={(e) => setDt({ dynamic_ttl: e.target.value })} placeholder="1h" />
              </Field>
              <Field label="Secret">
                <SecretInput value={dt.secret} masked={masked.secret} onChange={(v) => { setDt({ secret: v }); setMasked((m) => ({ ...m, secret: false })); }} onToggle={() => setMasked((m) => ({ ...m, secret: !m.secret }))} />
              </Field>
              <Field label="Salt">
                <SecretInput value={dt.salt} masked={masked.salt} onChange={(v) => { setDt({ salt: v }); setMasked((m) => ({ ...m, salt: false })); }} onToggle={() => setMasked((m) => ({ ...m, salt: !m.salt }))} />
              </Field>
            </div>

            {/* 静态 Token */}
            <div className="space-y-3 rounded-lg border bg-muted/30 p-3">
              <div className="flex items-center gap-2">
                <Switch checked={st.enable_static} onCheckedChange={(v) => setSt({ enable_static: v })} />
                <span className="text-sm font-medium">静态 Token</span>
              </div>
              <Field label="Token">
                <SecretInput value={st.token} masked={masked.token} onChange={(v) => { setSt({ token: v }); setMasked((m) => ({ ...m, token: false })); }} onToggle={() => setMasked((m) => ({ ...m, token: !m.token }))} />
              </Field>
              <Field label="过期时间（例: 24h）">
                <Input value={st.expire_hours} onChange={(e) => setSt({ expire_hours: e.target.value })} placeholder="24h" />
              </Field>
            </div>
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