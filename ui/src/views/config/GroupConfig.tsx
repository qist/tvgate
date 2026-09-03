import { useCallback, useRef, useState } from "react";
import { Braces, RotateCcw, Save, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import * as api from "@/api/group";

function formatYaml(text: string): string {
  return text
    .split("\n")
    .map((line) => {
      const m = line.match(/^(\s+)(.*)$/);
      if (!m) return line;
      const indent = m[1].replace(/\t/g, "  ");
      const spaces = (indent.match(/ /g) || []).length;
      const n = Math.ceil(spaces / 2) * 2;
      return " ".repeat(n) + m[2];
    })
    .join("\n");
}

export function GroupConfigPage() {
  const [configType, setConfigType] = useState("jx");
  const [groupName, setGroupName] = useState("");
  const [content, setContent] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const ref = useRef<HTMLTextAreaElement>(null);

  const notify = useCallback((type: "ok" | "err", msg: string) => {
    setNote({ type, msg });
    setTimeout(() => setNote(null), 3500);
  }, []);

  const load = useCallback(async () => {
    if (!configType || !groupName.trim()) {
      setNote({ type: "err", msg: "请填写配置节点和组名" });
      setTimeout(() => setNote(null), 3000);
      return false;
    }
    setBusy(true);
    try {
      const text = await api.load(configType, groupName.trim());
      setContent(text);
      setLoaded(true);
      notify("ok", "配置加载成功");
      return true;
    } catch (e) {
      notify("err", "加载失败: " + (e as Error).message);
      return false;
    } finally {
      setBusy(false);
    }
  }, [configType, groupName, notify]);

  const save = async () => {
    if (!loaded) return;
    if (!content.trim()) return notify("err", "配置内容不能为空");
    if (!configType || !groupName.trim()) return notify("err", "请填写配置节点和组名");
    setBusy(true);
    try {
      const data = await api.save(configType, groupName.trim(), content);
      const r = api.parseStatus(data, "配置保存成功");
      notify(r.ok ? "ok" : "err", r.msg);
    } catch (e) {
      notify("err", "保存失败: " + (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const validate = async () => {
    if (!content.trim()) return notify("err", "配置内容不能为空");
    setBusy(true);
    try {
      const data = await api.validate(content);
      const r = api.parseStatus(data, "YAML 格式验证通过");
      notify(r.ok ? "ok" : "err", r.msg);
    } catch (e) {
      notify("err", "验证失败: " + (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const format = () => {
    setContent(formatYaml(content));
    notify("ok", "已格式化（简化版，保留注释）");
  };

  const onKey = (e: React.KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      save();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "r") {
      e.preventDefault();
      load();
    } else if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === "v") {
      e.preventDefault();
      validate();
    } else if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === "f") {
      e.preventDefault();
      format();
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">组配置</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load} disabled={busy}>
            <RotateCcw className="mr-1 h-4 w-4" /> 加载
          </Button>
          <Button variant="outline" size="sm" onClick={format} disabled={!loaded}>
            <Braces className="mr-1 h-4 w-4" /> 格式化
          </Button>
          <Button variant="outline" size="sm" onClick={validate} disabled={!loaded || busy}>
            <ShieldCheck className="mr-1 h-4 w-4" /> 验证
          </Button>
          <Button size="sm" onClick={save} disabled={!loaded || busy}>
            <Save className="mr-1 h-4 w-4" /> 保存
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap items-end gap-3 rounded-lg border border-border bg-card p-3">
        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">配置节点</Label>
          <select className="h-9 rounded-[var(--radius)] border border-input bg-background px-2 text-sm" value={configType} onChange={(e) => { setConfigType(e.target.value); setLoaded(false); }}>
            {api.GROUP_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
        </div>
        <div className="min-w-[160px] flex-1 space-y-1">
          <Label className="text-xs text-muted-foreground">组名</Label>
          <Input value={groupName} onChange={(e) => { setGroupName(e.target.value); setLoaded(false); }} placeholder="例如 jx → api_groups 下的组名；proxygroups → 组名" />
        </div>
        <p className="w-full text-xs text-muted-foreground">选择节点与组后点击「加载」编辑该组配置。jx 的组位于 <code>api_groups</code> 下，proxygroups 的组为顶层映射。快捷键：Ctrl+S 保存 / Ctrl+R 加载 / Ctrl+Shift+V 验证 / Ctrl+Shift+F 格式化。</p>
      </div>

      {note && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${note.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {note.msg}
        </div>
      )}

      <Card className="overflow-hidden">
        <textarea
          ref={ref}
          value={content}
          spellCheck={false}
          disabled={!loaded}
          onChange={(e) => setContent(e.target.value)}
          onKeyDown={onKey}
          placeholder={loaded ? "" : "点击「加载」读取组配置…"}
          className="h-[62vh] w-full resize-none bg-background p-3 font-mono text-sm text-foreground outline-none"
        />
      </Card>
    </div>
  );
}