import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowUp, Braces, RotateCcw, Save, ShieldCheck, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import * as api from "@/api/yaml";
import { isElevateRequired } from "@/api/elevate";
import { ElevateDialog } from "@/components/ElevateDialog";

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
  const mirrorRef = useRef<HTMLDivElement>(null);
  const findInputRef = useRef<HTMLInputElement>(null);

  // --- 页内查找（Ctrl+F）：输入不跳转，Enter 逐个跳转，全部匹配高亮 ---
  const [findOpen, setFindOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [matchIdx, setMatchIdx] = useState(0);

  const matches = useMemo<[number, number][]>(() => {
    if (!findOpen || !query) return [];
    const out: [number, number][] = [];
    let i = 0;
    while ((i = content.indexOf(query, i)) !== -1) {
      out.push([i, i + query.length]);
      i += 1;
    }
    return out;
  }, [content, query, findOpen]);

  // 输入过程中只更新计数与高亮，不移动光标；Enter/按钮才跳转
  useEffect(() => {
    setMatchIdx(0);
  }, [query]);

  const openFind = useCallback(() => {
    const el = ref.current;
    const sel = el?.value.slice(el.selectionStart, el.selectionEnd) ?? "";
    setFindOpen(true);
    if (sel && !sel.includes("\n")) setQuery(sel);
    // 等输入框渲染后聚焦并全选，方便直接输入覆盖
    window.setTimeout(() => {
      findInputRef.current?.focus();
      findInputRef.current?.select();
    }, 0);
  }, []);

  const scrollToMatch = (start: number) => {
    const el = ref.current;
    if (!el) return;
    const line = content.slice(0, start).split("\n").length - 1;
    const lineHeight = 20; // 与 leading-5 对应
    el.scrollTop = Math.max(0, line * lineHeight - el.clientHeight / 3);
  };

  const jumpTo = useCallback(
    (idx: number) => {
      if (matches.length === 0) return;
      const i = ((idx % matches.length) + matches.length) % matches.length;
      const [s, e] = matches[i];
      const el = ref.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(s, e);
      scrollToMatch(s);
      setMatchIdx(i);
    },
    [matches, content],
  );

  const closeFind = useCallback(() => {
    setFindOpen(false);
    setQuery("");
    ref.current?.focus();
  }, []);

  const syncMirror = () => {
    const el = ref.current;
    const mirror = mirrorRef.current;
    if (!el || !mirror) return;
    mirror.scrollTop = el.scrollTop;
    mirror.scrollLeft = el.scrollLeft;
  };

  const notify = useCallback((type: "ok" | "err", msg: string) => {
    setNote({ type, msg });
    setTimeout(() => setNote(null), 3500);
  }, []);

  // 二次验证：加载/保存遇 403 时弹窗，验证通过后自动续做被拦截的操作
  const [needElevate, setNeedElevate] = useState(false);
  const pendingRef = useRef<(() => void) | null>(null);
  const onElevated = () => {
    setNeedElevate(false);
    const p = pendingRef.current;
    pendingRef.current = null;
    p?.();
  };

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const text = await api.load();
      setContent(text);
      setOriginal(text);
      notify("ok", "配置加载成功");
    } catch (e) {
      if (isElevateRequired(e)) {
        pendingRef.current = load;
        setNeedElevate(true);
      } else {
        notify("err", "加载配置失败: " + (e as Error).message);
      }
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
      if (isElevateRequired(e)) {
        pendingRef.current = save;
        setNeedElevate(true);
      } else {
        notify("err", "保存失败: " + (e as Error).message);
      }
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
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "f") {
      e.preventDefault();
      openFind();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
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

      {needElevate && <ElevateDialog onDone={onElevated} />}

      <Card className="relative h-[70vh] overflow-hidden">
        {/* 查找条：输入只更新计数与高亮，Enter/箭头跳转，Esc 关闭 */}
        {findOpen && (
          <div className="absolute top-2 right-3 z-10 flex items-center gap-1.5 rounded-lg border bg-background/95 px-2 py-1.5 shadow-md backdrop-blur">
            <input
              ref={findInputRef}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  jumpTo(e.shiftKey ? matchIdx - 1 : matchIdx + 1);
                } else if (e.key === "Escape") {
                  e.preventDefault();
                  closeFind();
                }
              }}
              placeholder="查找内容…"
              className="h-7 w-44 rounded-md border bg-background px-2 font-mono text-xs outline-none focus:ring-1 focus:ring-violet-400/60"
            />
            <span className={`min-w-14 text-center text-xs ${query && matches.length === 0 ? "text-destructive" : "text-muted-foreground"}`}>
              {query ? (matches.length ? `${matchIdx + 1}/${matches.length}` : "无匹配") : ""}
            </span>
            <Button variant="ghost" size="icon" className="h-6 w-6" title="上一个 (Shift+Enter)" onClick={() => jumpTo(matchIdx - 1)} disabled={!matches.length}>
              <ArrowUp className="h-3.5 w-3.5" />
            </Button>
            <Button variant="ghost" size="icon" className="h-6 w-6" title="下一个 (Enter)" onClick={() => jumpTo(matchIdx + 1)} disabled={!matches.length}>
              <ArrowDown className="h-3.5 w-3.5" />
            </Button>
            <Button variant="ghost" size="icon" className="h-6 w-6" title="关闭 (Esc)" onClick={closeFind}>
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>
        )}
        {/* 镜像层：与 textarea 完全同字体/内边距/行高，渲染全部匹配高亮（textarea 背景透明透出） */}
        <div ref={mirrorRef} aria-hidden className="pointer-events-none absolute inset-0 overflow-hidden p-3 font-mono text-sm leading-5 whitespace-pre text-transparent">
          {(() => {
            if (!matches.length) return content;
            const parts: React.ReactNode[] = [];
            let last = 0;
            matches.forEach(([s, e], i) => {
              parts.push(content.slice(last, s));
              parts.push(
                <mark key={i} className={i === matchIdx ? "rounded-sm bg-amber-400/60 text-transparent" : "rounded-sm bg-violet-400/30 text-transparent"}>
                  {content.slice(s, e)}
                </mark>,
              );
              last = e;
            });
            parts.push(content.slice(last));
            return parts;
          })()}
        </div>
        <textarea
          ref={ref}
          value={content}
          spellCheck={false}
          wrap="off"
          onChange={(e) => setContent(e.target.value)}
          onKeyDown={onKey}
          onScroll={syncMirror}
          className="absolute inset-0 h-full w-full resize-none overflow-auto bg-transparent p-3 font-mono text-sm leading-5 whitespace-pre text-foreground caret-violet-600 outline-none dark:caret-violet-300"
        />
      </Card>

      <div className="rounded-lg border border-border bg-card p-3 text-xs text-muted-foreground">
        <h3 className="mb-1 text-sm font-semibold">快捷键</h3>
        <ul className="list-inside list-disc space-y-0.5">
          <li><kbd className="rounded border border-input bg-background px-1.5 py-0.5">Ctrl+F</kbd> 查找（选中内容自动带入；Enter 下一个 / Shift+Enter 上一个 / Esc 关闭）</li>
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