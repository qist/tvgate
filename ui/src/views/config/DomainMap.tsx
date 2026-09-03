import { useCallback, useEffect, useState } from "react";
import { Plus, Pencil, Trash2, X, KeyRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { KeyValueEditor } from "@/components/form/KeyValueEditor";
import { listDomainMaps, saveDomainMaps, type AuthConfig, type DomainMap, type DomainMapList } from "@/api/domainmap";

const emptyAuth = (): AuthConfig => ({
  tokens_enabled: false,
  token_param_name: "",
  dynamic_tokens: { enable_dynamic: false, dynamic_ttl: "", secret: "", salt: "" },
  static_tokens: { enable_static: false, token: "", expire_hours: "" },
});

const emptyMap = (): DomainMap => ({ name: "", source: "", target: "", protocol: "http", auth: emptyAuth(), client_headers: {}, server_headers: {} });

function authPresent(a: AuthConfig): boolean {
  return a.tokens_enabled || a.token_param_name !== "" || a.dynamic_tokens.enable_dynamic || a.dynamic_tokens.secret !== "" || a.dynamic_tokens.salt !== "" || a.static_tokens.enable_static || a.static_tokens.token !== "";
}

export function DomainMapPage() {
  const [list, setList] = useState<DomainMap[]>([]);
  const [editing, setEditing] = useState<Set<number>>(new Set());
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => setList(await listDomainMaps()), []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const patch = (i: number, p: Partial<DomainMap>) => setList((prev) => prev.map((m, idx) => (idx === i ? { ...m, ...p } : m)));
  const patchAuth = (i: number, a: AuthConfig) => patch(i, { auth: a });

  const add = () => setList((prev) => [...prev, emptyMap()]);
  const remove = (i: number) => {
    setList((prev) => prev.filter((_, idx) => idx !== i));
    setEditing((prev) => {
      const n = new Set(prev);
      n.delete(i);
      return n;
    });
  };

  const save = async () => {
    const data = list
      .filter((m) => m.name.trim() || m.source.trim())
      .map((m) => {
        const out: Record<string, unknown> = { name: m.name.trim(), source: m.source.trim(), target: m.target.trim(), protocol: m.protocol };
        if (m.auth && authPresent(m.auth)) out.auth = m.auth;
        if (m.client_headers && Object.keys(m.client_headers).length) out.client_headers = m.client_headers;
        if (m.server_headers && Object.keys(m.server_headers).length) out.server_headers = m.server_headers;
        return out;
      });
    try {
      await saveDomainMaps(data as unknown as DomainMapList);
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setEditing(new Set());
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">域名映射</h1>
        <Button onClick={add}>
          <Plus className="mr-1 h-4 w-4" /> 添加映射
        </Button>
      </div>

      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}

      {list.map((m, i) =>
        editing.has(i) ? (
          <EditCard key={i} m={m} onChange={(p) => patch(i, p)} onAuth={(a) => patchAuth(i, a)} onCancel={() => setEditing((prev) => { const n = new Set(prev); n.delete(i); return n; })} onDelete={() => remove(i)} />
        ) : (
          <ViewCard key={i} m={m} onEdit={() => setEditing((prev) => new Set(prev).add(i))} onDelete={() => remove(i)} />
        ),
      )}

      {list.length > 0 && (
        <div className="flex gap-2">
          <Button onClick={save}>保存全部配置</Button>
          <Button variant="secondary" onClick={refresh}>重置</Button>
        </div>
      )}
    </div>
  );
}

function ViewCard({ m, onEdit, onDelete }: { m: DomainMap; onEdit: () => void; onDelete: () => void }) {
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base">{m.name || "(未命名)"}</CardTitle>
          {m.protocol && <Badge variant="outline">{m.protocol}</Badge>}
          {m.auth && authPresent(m.auth) && <KeyRound className="h-4 w-4 text-muted-foreground" />}
        </div>
        <div className="flex gap-1.5">
          <Button variant="outline" size="sm" onClick={onEdit}><Pencil className="h-4 w-4" /></Button>
          <Button variant="destructive" size="sm" onClick={onDelete}><Trash2 className="h-4 w-4" /></Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-1 text-sm text-muted-foreground">
        <p>源：{m.source || "—"}</p>
        <p>目标：{m.target || "—"}</p>
        {(m.client_headers && Object.keys(m.client_headers).length > 0) && <p>client_headers：{Object.entries(m.client_headers).map(([k, v]) => `${k}=${v}`).join("，")}</p>}
        {(m.server_headers && Object.keys(m.server_headers).length > 0) && <p>server_headers：{Object.entries(m.server_headers).map(([k, v]) => `${k}=${v}`).join("，")}</p>}
      </CardContent>
    </Card>
  );
}

function EditCard({ m, onChange, onAuth, onCancel, onDelete }: { m: DomainMap; onChange: (p: Partial<DomainMap>) => void; onAuth: (a: AuthConfig) => void; onCancel: () => void; onDelete: () => void }) {
  const auth = m.auth || emptyAuth();
  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-2">
        <CardTitle className="text-base">编辑映射</CardTitle>
        <div className="flex gap-1.5">
          <Button variant="outline" size="sm" onClick={onCancel}><X className="mr-1 h-4 w-4" />取消</Button>
          <Button variant="destructive" size="sm" onClick={onDelete}><Trash2 className="h-4 w-4" /></Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="名称"><Input value={m.name} onChange={(e) => onChange({ name: e.target.value })} placeholder="配置名称" /></Field>
          <Field label="协议">
            <select className="h-9 w-full rounded-[var(--radius)] border bg-background px-2 text-sm" value={m.protocol} onChange={(e) => onChange({ protocol: e.target.value })}>
              {["http", "https"].map((p) => <option key={p} value={p}>{p}</option>)}
            </select>
          </Field>
          <Field label="源域名"><Input className="font-mono" value={m.source} onChange={(e) => onChange({ source: e.target.value })} placeholder="source.example.com" /></Field>
          <Field label="目标域名"><Input className="font-mono" value={m.target} onChange={(e) => onChange({ target: e.target.value })} placeholder="target.example.com" /></Field>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Client Headers">
            <KeyValueEditor value={m.client_headers || {}} onChange={(h) => onChange({ client_headers: h })} />
          </Field>
          <Field label="Server Headers">
            <KeyValueEditor value={m.server_headers || {}} onChange={(h) => onChange({ server_headers: h })} />
          </Field>
        </div>

        <div className="rounded-lg border bg-muted/30 p-3 space-y-3">
          <div className="flex items-center gap-2">
            <Label>认证（Token）</Label>
            <Switch checked={auth.tokens_enabled} onCheckedChange={(v) => onAuth({ ...auth, tokens_enabled: v })} />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Token 参数名"><Input value={auth.token_param_name} onChange={(e) => onAuth({ ...auth, token_param_name: e.target.value })} placeholder="token" /></Field>
          </div>
          <div className="grid gap-3 sm:grid-cols-2 border-t pt-2">
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-sm"><Switch checked={auth.dynamic_tokens.enable_dynamic} onCheckedChange={(v) => onAuth({ ...auth, dynamic_tokens: { ...auth.dynamic_tokens, enable_dynamic: v } })} /> 动态 Token</div>
              <Field label="TTL"><Input value={auth.dynamic_tokens.dynamic_ttl} onChange={(e) => onAuth({ ...auth, dynamic_tokens: { ...auth.dynamic_tokens, dynamic_ttl: e.target.value } })} placeholder="1h" /></Field>
              <Field label="Secret"><Input value={auth.dynamic_tokens.secret} onChange={(e) => onAuth({ ...auth, dynamic_tokens: { ...auth.dynamic_tokens, secret: e.target.value } })} /></Field>
              <Field label="Salt"><Input value={auth.dynamic_tokens.salt} onChange={(e) => onAuth({ ...auth, dynamic_tokens: { ...auth.dynamic_tokens, salt: e.target.value } })} /></Field>
            </div>
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-sm"><Switch checked={auth.static_tokens.enable_static} onCheckedChange={(v) => onAuth({ ...auth, static_tokens: { ...auth.static_tokens, enable_static: v } })} /> 静态 Token</div>
              <Field label="Token"><Input value={auth.static_tokens.token} onChange={(e) => onAuth({ ...auth, static_tokens: { ...auth.static_tokens, token: e.target.value } })} /></Field>
              <Field label="过期时间"><Input value={auth.static_tokens.expire_hours} onChange={(e) => onAuth({ ...auth, static_tokens: { ...auth.static_tokens, expire_hours: e.target.value } })} placeholder="24h" /></Field>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
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