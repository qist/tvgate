import { useCallback, useEffect, useRef, useState } from "react";
import { Braces, RotateCcw, Save, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import * as api from "@/api/yaml";

function formatYaml(text: string): string {
  // 简化格式化：制表符转 2 空格，缩进统一为 2 的倍数（保留注释与原始结构）
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

export function YAMLEditorPage() {
  const [content, setContent] = useState("");
  const [original, setOriginal] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const ref = useRef<HTMLTextAreaElement>(null);

  const notify = useCallback((type: "ok" | "err", msg: string) => {
    setNote({ type, msg });
    setTimeout(() => setNote(null), 3500);
  }, []);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const text = await api.load();
      setContent(text);
      setOriginal(text);
      notify("ok", "配置加载成功");
    } catch (e) {
      notify("err", "加载配置失败: " + (e as Error).message);
    } finally {
      setBusy(false);
    }
  }, [notify]);

  useEffect(() => {
    load();
  }, [load]);

  const save = async () => {
    if (!content.trim()) return notify("err", "配置内容不能为空");
    setBusy(true);
    try {
      const data = await api.save(content);
      const r = api.parseStatus(data, "配置保存成功，点击重新加载生效");
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

  // 注释/取消注释所选行；无选择则作用于当前可见的全部内容所在行块
  const toggleComment = () => {
    const el = ref.current;
    if (!el) return;
    const start = el.selectionStart;
    const end = el.selectionEnd;
    const lines = (s: number, e: number) => {
      const all = content.split("\n");
      const firstLine = content.slice(0, s).split("\n").length - 1;
      const lastLine = content.slice(0, e).split("\n").length - 1;
      // 计算首/末行的起始字符索引
      const lineIdx: number[] = [];
      for (let i = firstLine; i <= lastLine; i++) lineIdx.push(i);
      return { all, lineIdx };
    };
    const { all, lineIdx } = lines(start, Math.max(start, end));
    // 判断该块是否全部已注释
    const already = lineIdx.every((i) => /^\s*#/.test(all[i]));
    const next = all.map((ln, i) => {
      if (!lineIdx.includes(i)) return ln;
      if (already) return ln.replace(/^(\s*)#/, "$1");
      return "  " + ln.replace(/^(\s*)/, "#$1");
    });
    setContent(next.join("\n"));
  };

  const onKey = (e: React.KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      save();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "r") {
      e.preventDefault();
      load();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "q") {
      e.preventDefault();
      toggleComment();
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
        <h1 className="text-xl font-semibold">YAML 编辑器</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={load} disabled={busy}>
            <RotateCcw className="mr-1 h-4 w-4" /> 重新加载
          </Button>
          <Button variant="outline" size="sm" onClick={format}>
            <Braces className="mr-1 h-4 w-4" /> 格式化
          </Button>
          <Button variant="outline" size="sm" onClick={validate} disabled={busy}>
            <ShieldCheck className="mr-1 h-4 w-4" /> 验证
          </Button>
          <Button size="sm" onClick={save} disabled={busy}>
            <Save className="mr-1 h-4 w-4" /> 保存
          </Button>
        </div>
      </div>

      <div className="rounded-lg border bg-card px-3 py-2 text-sm text-muted-foreground">
        强调配置来源：当前运行实例的配置文件。{content !== original ? "（存在未保存修改）" : "无未保存修改"}
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
          onChange={(e) => setContent(e.target.value)}
          onKeyDown={onKey}
          className="h-[62vh] w-full resize-none bg-background p-3 font-mono text-sm text-foreground outline-none"
        />
      </Card>

      <div className="rounded-lg border border-border bg-card p-3 text-xs text-muted-foreground">
        <h3 className="mb-1 text-sm font-semibold">快捷键</h3>
        <ul className="list-inside list-disc space-y-0.5">
          <li><kbd className="rounded border border-input bg-background px-1.5 py-0.5">Ctrl+S</kbd> 保存配置</li>
          <li><kbd className="rounded border border-input bg-background px-1.5 py-0.5">Ctrl+R</kbd> 重新加载配置</li>
          <li><kbd className="rounded border border-input bg-background px-1.5 py-0.5">Ctrl+Q</kbd> 注释/取消注释</li>
          <li><kbd className="rounded border border-input bg-background px-1.5 py-0.5">Ctrl+Shift+V</kbd> 验证 YAML 格式</li>
          <li><kbd className="rounded border border-input bg-background px-1.5 py-0.5">Ctrl+Shift+F</kbd> 格式化 YAML</li>
        </ul>
        <p className="mt-2">注意：编辑配置前请先备份，错误的配置可能导致服务异常。</p>
      </div>
    </div>
  );
}