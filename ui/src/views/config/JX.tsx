import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { AsyncActionButton } from "@/components/config/async-action-button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { KeyValueEditor } from "@/components/form/KeyValueEditor";
import { getJX, saveJX, emptyGroup, type ApiGroup, type JXConfig } from "@/api/jx";

interface GroupEntry {
  name: string;
  g: ApiGroup;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

export function JXPage() {
  const [path, setPath] = useState("");
  const [defaultId, setDefaultId] = useState("");
  const [groups, setGroups] = useState<GroupEntry[]>([]);
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);

  const refresh = useCallback(async () => {
    const jx = await getJX();
    setPath(jx.path);
    setDefaultId(jx.default_id);
    setGroups(Object.entries(jx.api_groups).map(([name, g]) => ({ name, g })));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const patchGroup = (i: number, p: Partial<ApiGroup>) =>
    setGroups((prev) => prev.map((e, idx) => (idx === i ? { ...e, g: { ...e.g, ...p } } : e)));

  const save = async () => {
    const api_groups: Record<string, ApiGroup> = {};
    for (const e of groups) {
      const name = e.name.trim();
      if (!name) continue;
      api_groups[name] = {
        ...e.g,
        endpoints: e.g.endpoints.filter((x) => x.trim() !== ""),
        filters: Object.fromEntries(Object.entries(e.g.filters).filter(([k]) => k.trim() !== "" || (e.g.filters[k] ?? "") !== "")),
      };
    }
    try {
      await saveJX({ path, default_id: defaultId, api_groups } as JXConfig);
      setNotice({ type: "ok", msg: "配置保存成功，热重载生效中" });
      setTimeout(refresh, 6500);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">视频解析 (JX)</h1>
      </div>

      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}

      <Card>
        <CardHeader><CardTitle className="text-base">基础设置</CardTitle></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          <Field label="路径"><Input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/jx/" /></Field>
          <Field label="默认视频 ID"><Input value={defaultId} onChange={(e) => setDefaultId(e.target.value)} placeholder="默认 ID" /></Field>
        </CardContent>
      </Card>

      <div className="flex items-center justify-between">
        <h2 className="text-base font-semibold">API 组（{groups.length}）</h2>
        <Button size="sm" onClick={() => setGroups((prev) => [...prev, { name: "", g: emptyGroup() }])}>
          <Plus className="mr-1 h-4 w-4" /> 添加组
        </Button>
      </div>

      {groups.map((e, i) => {
        const g = e.g;
        return (
          <Card key={i}>
            <CardHeader className="flex-row items-center justify-between gap-2">
              <Label className="sr-only">组名</Label>
              <Input className="max-w-xs font-mono" value={e.name} onChange={(ev) => setGroups((prev) => prev.map((x, idx) => (idx === i ? { ...x, name: ev.target.value } : x)))} placeholder="组名（唯一标识）" />
              <Button variant="ghost" size="icon" onClick={() => setGroups((prev) => prev.filter((_, idx) => idx !== i))}>
                <Trash2 className="h-4 w-4" />
              </Button>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <Label>Endpoints</Label>
                  <Button size="sm" variant="secondary" onClick={() => patchGroup(i, { endpoints: [...g.endpoints, ""] })}>
                    <Plus className="h-4 w-4" /> 添加
                  </Button>
                </div>
                {g.endpoints.map((ep, ei) => (
                  <div key={ei} className="flex items-center gap-2">
                    <Input className="flex-1 font-mono" value={ep} onChange={(ev) => patchGroup(i, { endpoints: g.endpoints.map((x, xi) => (xi === ei ? ev.target.value : x)) })} placeholder="解析接口" />
                    <Button size="icon" variant="ghost" onClick={() => patchGroup(i, { endpoints: g.endpoints.filter((_, xi) => xi !== ei) })}>
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
              </div>

              <div className="grid gap-3 sm:grid-cols-3">
                <Field label="查询模板"><Input className="font-mono" value={g.query_template} onChange={(ev) => patchGroup(i, { query_template: ev.target.value })} placeholder="?url={id}" /></Field>
                <Field label="超时"><Input value={g.timeout} onChange={(ev) => patchGroup(i, { timeout: ev.target.value })} placeholder="5s" /></Field>
                <Field label="权重"><Input type="number" value={g.weight} onChange={(ev) => patchGroup(i, { weight: +ev.target.value || 0 })} /></Field>
                <Field label="最大重试"><Input type="number" value={g.max_retries} onChange={(ev) => patchGroup(i, { max_retries: +ev.target.value || 0 })} /></Field>
                <div className="flex items-end gap-4">
                  <label className="flex items-center gap-1.5 text-sm"><Switch checked={g.primary} onCheckedChange={(v) => patchGroup(i, { primary: v })} /> 主 API</label>
                  <label className="flex items-center gap-1.5 text-sm"><Switch checked={g.fallback} onCheckedChange={(v) => patchGroup(i, { fallback: v })} /> 备用</label>
                </div>
              </div>

              <Field label="过滤条件 (Filters)">
                <KeyValueEditor value={g.filters} onChange={(f) => patchGroup(i, { filters: f })} />
              </Field>
            </CardContent>
          </Card>
        );
      })}

      <div className="flex gap-2">
        <AsyncActionButton action={save} busyText="保存中…">保存</AsyncActionButton>
        <AsyncActionButton variant="secondary" action={refresh} busyText="加载中…">重新加载</AsyncActionButton>
      </div>
    </div>
  );
}