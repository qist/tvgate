import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowUp,
  Box,
  Download,
  FileCode,
  FilePlus,
  FileText,
  Folder,
  FolderPlus,
  Package,
  Pencil,
  RefreshCw,
  Replace,
  Save,
  Search,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import * as code from "@/api/code";
import type { CodeItem } from "@/api/code";

// ================= 通用工具 =================

const fmtSize = (n: number): string => {
  if (n < 1024) return n + " B";
  if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
  return (n / 1048576).toFixed(1) + " MB";
};

const BIN_EXTS = ["zip", "jar", "apk", "exe", "dll", "so", "bin", "7z", "gz", "tar", "rar", "png", "jpg", "jpeg", "gif", "webp", "ico", "bmp", "woff", "woff2", "ttf"];
const isZip = (name: string) => (name.toLowerCase().split(".").pop() || "") === "zip";
const isBinary = (name: string) => BIN_EXTS.includes(name.toLowerCase().split(".").pop() || "");

/** 按扩展名选择注释形式：line=行注释前缀；block=块注释包裹 */
function commentStyle(name: string): { line: string } | { line: string; block: [string, string] } {
  const ext = name.toLowerCase().split(".").pop() || "";
  if (ext === "css") return { line: "//", block: ["/*", "*/"] };
  if (["html", "htm", "xml", "svg", "vue"].includes(ext)) return { line: "//", block: ["<!--", "-->"] };
  if (["py", "sh", "bash", "yml", "yaml", "toml", "ini", "conf", "rb"].includes(ext)) return { line: "#" };
  if (["sql", "lua"].includes(ext)) return { line: "--" };
  return { line: "//" };
}

interface Match {
  idx: number;
  len: number;
}

function findMatches(text: string, needle: string, caseFold: boolean, isRegex: boolean): Match[] {
  if (!needle) return [];
  if (isRegex) {
    let re: RegExp;
    try {
      re = new RegExp(needle, caseFold ? "gi" : "g");
    } catch {
      return [];
    }
    const out: Match[] = [];
    let m: RegExpExecArray | null;
    while ((m = re.exec(text)) !== null) {
      out.push({ idx: m.index, len: m[0].length || 1 });
      if (!m[0]) re.lastIndex++;
    }
    return out;
  }
  const hay = caseFold ? text.toLowerCase() : text;
  const pin = caseFold ? needle.toLowerCase() : needle;
  if (!pin) return [];
  const out: Match[] = [];
  let i = 0;
  while ((i = hay.indexOf(pin, i)) !== -1) {
    out.push({ idx: i, len: needle.length });
    i += needle.length;
  }
  return out;
}

// ================= 页面 =================

export function CodePage() {
  const [dir, setDir] = useState("");
  const [items, setItems] = useState<CodeItem[]>([]);
  const [search, setSearch] = useState("");
  const [current, setCurrent] = useState<string | null>(null); // 当前打开文件相对路径
  const [content, setContent] = useState("");
  const [dirty, setDirty] = useState(false);
  const [notice, setNotice] = useState<{ type: "ok" | "err" | "warn"; msg: string } | null>(null);
  const [busy, setBusy] = useState("");
  const [findOpen, setFindOpen] = useState<null | { focusReplace: boolean }>(null);
  const [replOpen, setReplOpen] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);

  const notify = useCallback((type: "ok" | "err" | "warn", msg: string) => {
    setNotice({ type, msg });
    if (type !== "warn") setTimeout(() => setNotice(null), 5000);
  }, []);

  const refresh = useCallback(async () => {
    try {
      setItems(await code.list(dir));
    } catch (e) {
      notify("err", "加载目录失败: " + (e as Error).message);
    }
  }, [dir, notify]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const filtered = useMemo(() => {
    const kw = search.toLowerCase().trim();
    const list = kw ? items.filter((it) => it.name.toLowerCase().includes(kw)) : items;
    return [...list].sort((a, b) => (a.isDir !== b.isDir ? (a.isDir ? -1 : 1) : a.name > b.name ? 1 : -1));
  }, [items, search]);

  const openFile = async (rel: string) => {
    try {
      const c = await code.read(rel);
      setCurrent(rel);
      setContent(c);
      setDirty(false);
      setNotice(null);
    } catch (e) {
      notify("err", "打开失败: " + (e as Error).message);
    }
  };

  const openItem = (it: CodeItem) => {
    const rel = dir ? `${dir}/${it.name}` : it.name;
    if (it.isDir) {
      setDir(rel);
      setSearch("");
      setCurrent(null);
      setContent("");
    } else if (isZip(it.name)) {
      notify("warn", `已选中 ZIP「${it.name}」，点击行内「解压」按钮解压到当前目录（覆盖模式）`);
    } else if (isBinary(it.name)) {
      notify("warn", `「${it.name}」为二进制文件，不可编辑，可下载或删除`);
    } else {
      openFile(rel);
    }
  };

  const goUp = () => {
    const i = dir.lastIndexOf("/");
    setDir(i < 0 ? "" : dir.slice(0, i));
    setCurrent(null);
    setContent("");
  };

  const goRoot = () => {
    setDir("");
    setCurrent(null);
    setContent("");
  };

  const closeEditor = () => {
    if (dirty && !window.confirm("有未保存的修改，确定关闭？")) return;
    setCurrent(null);
    setContent("");
    setDirty(false);
  };

  const save = async () => {
    if (!current) return;
    try {
      await code.saveFile(current, content);
      setDirty(false);
      notify("ok", `已保存 ${current}`);
    } catch (e) {
      notify("err", "保存失败: " + (e as Error).message);
    }
  };

  const create = async (type: "file" | "dir") => {
    const name = window.prompt(type === "file" ? "新文件相对路径（可用斜杠建子目录）:" : "新目录相对路径:", `${dir ? dir + "/" : ""}new${type === "file" ? ".php" : ""}`);
    if (!name) return;
    try {
      if (type === "file") await code.createFile(name.replace(/^\//, ""));
      else await code.createDir(name.replace(/^\//, ""));
      notify("ok", `已创建 ${name}`);
      refresh();
    } catch (e) {
      notify("err", (e as Error).message);
    }
  };

  const doRename = async (it: CodeItem) => {
    const rel = dir ? `${dir}/${it.name}` : it.name;
    const newName = window.prompt("重命名为:", it.name);
    if (!newName || newName === it.name) return;
    try {
      await code.rename(rel, newName);
      if (current === rel) setCurrent(dir ? `${dir}/${newName}` : newName);
      notify("ok", "已重命名");
      refresh();
    } catch (e) {
      notify("err", (e as Error).message);
    }
  };

  const remove = async (it: CodeItem) => {
    const rel = dir ? `${dir}/${it.name}` : it.name;
    if (!window.confirm(`删除 ${it.isDir ? "目录" : "文件"} ${rel}？`)) return;
    try {
      await code.deleteFile(rel);
      if (current === rel) {
        setCurrent(null);
        setContent("");
      }
      notify("ok", `已删除 ${rel}`);
      refresh();
    } catch (e) {
      notify("err", (e as Error).message);
    }
  };

  const doUnzip = async (it: CodeItem) => {
    const rel = dir ? `${dir}/${it.name}` : it.name;
    if (!window.confirm(`解压 ${rel} 到当前目录（覆盖模式）？`)) return;
    setBusy("解压中…");
    try {
      await code.unzip(rel, dir);
      notify("ok", "解压完成");
      refresh();
    } catch (e) {
      notify("err", "解压失败: " + (e as Error).message);
    } finally {
      setBusy("");
    }
  };

  const onUpload = async (files: FileList | null) => {
    if (!files || files.length === 0) return;
    setBusy("上传中…");
    try {
      await code.uploadFiles(dir, files);
      notify("ok", `已上传 ${files.length} 个文件`);
      refresh();
    } catch (e) {
      notify("err", "上传失败: " + (e as Error).message);
    } finally {
      setBusy("");
    }
  };

  // 注释/取消注释（Ctrl+Q / Ctrl+/）
  const toggleComment = () => {
    const ta = taRef.current;
    if (!ta || !current) return;
    const style = commentStyle(current);
    const { value, selectionStart, selectionEnd } = ta;
    if (selectionEnd > selectionStart && "block" in style) {
      // 有选区：块注释包裹 / 解除
      const [open, close] = style.block;
      const sel = value.slice(selectionStart, selectionEnd);
      let out: string, selStart: number, selEnd: number;
      if (sel.startsWith(open) && sel.endsWith(close)) {
        out = sel.slice(open.length, sel.length - close.length).replace(/^ | $/g, "");
        setContent(value.slice(0, selectionStart) + out + value.slice(selectionEnd));
        selStart = selectionStart;
        selEnd = selectionStart + out.length;
      } else {
        out = `${open} ${sel} ${close}`;
        setContent(value.slice(0, selectionStart) + out + value.slice(selectionEnd));
        selStart = selectionStart;
        selEnd = selectionStart + out.length;
      }
      requestAnimationFrame(() => {
        ta.focus();
        ta.setSelectionRange(selStart, selEnd);
      });
      return;
    }
    // 无选区/行注释：对选区覆盖的行逐行切换
    const prefix = style.line;
    const startLine = value.lastIndexOf("\n", selectionStart - 1) + 1;
    let endLine = value.indexOf("\n", selectionEnd);
    if (endLine === -1) endLine = value.length;
    const lines = value.slice(startLine, endLine).split("\n");
    const code0Lines = lines.filter((l) => l.trim());
    const allCommented = code0Lines.length > 0 && code0Lines.every((l) => l.trimStart().startsWith(prefix));
    const out = lines
      .map((l) => {
        if (!l.trim()) return l;
        if (allCommented) {
          const i = l.indexOf(prefix);
          return l.slice(0, i) + l.slice(i + prefix.length).replace(/^ /, "");
        }
        return prefix + " " + l;
      })
      .join("\n");
    setContent(value.slice(0, startLine) + out + value.slice(endLine));
    requestAnimationFrame(() => {
      ta.focus();
      ta.setSelectionRange(startLine, startLine + out.length);
    });
  };

  // 快捷键
  const onEditorKeyDown = (e: React.KeyboardEvent) => {
    const mod = e.ctrlKey || e.metaKey;
    if (!mod) {
      if (e.key === "Escape" && findOpen) setFindOpen(null);
      return;
    }
    const k = e.key.toLowerCase();
    if (k === "s") {
      e.preventDefault();
      save();
    } else if (k === "f") {
      e.preventDefault();
      setFindOpen({ focusReplace: false });
    } else if (k === "h") {
      e.preventDefault();
      setFindOpen({ focusReplace: true });
    } else if (k === "q" || e.key === "/") {
      e.preventDefault();
      toggleComment();
    }
  };

  // 面包屑
  const crumbs = useMemo(() => {
    const parts = dir ? dir.split("/") : [];
    return [{ name: "", label: "docroot" }, ...parts.map((p, i) => ({ name: parts.slice(0, i + 1).join("/"), label: p }))];
  }, [dir]);

  return (
    <div className="flex flex-col gap-3 lg:h-[calc(100vh-7.5rem)] lg:flex-row">
      {/* 左栏：文件树 */}
      <Card className="flex max-h-[42vh] w-full shrink-0 flex-col p-0 lg:max-h-none lg:w-72">
        <div className="flex items-center gap-1 border-b px-2 py-1.5">
          <Button size="icon" variant="ghost" className="h-7 w-7" disabled={!dir} onClick={goUp} title="返回上级">
            <ArrowUp className="h-4 w-4" />
          </Button>
          <Button size="icon" variant="ghost" className="h-7 w-7" onClick={refresh} title="刷新">
            <RefreshCw className="h-4 w-4" />
          </Button>
          <div className="relative ml-auto">
            <Search className="pointer-events-none absolute left-1.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              className="h-7 w-32 rounded border bg-background pl-7 text-xs"
              placeholder="过滤文件…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </div>
        <div className="flex items-center gap-1 border-b px-2 py-1.5">
          <Button variant="outline" size="sm" className="h-7 flex-1 text-xs" onClick={() => create("file")}>
            <FilePlus className="h-3.5 w-3.5" /> 新建文件
          </Button>
          <Button variant="outline" size="sm" className="h-7 flex-1 text-xs" onClick={() => create("dir")}>
            <FolderPlus className="h-3.5 w-3.5" /> 新建目录
          </Button>
          <Button variant="outline" size="sm" className="h-7 flex-1 text-xs" onClick={() => fileInputRef.current?.click()}>
            <Upload className="h-3.5 w-3.5" /> 上传
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => {
              onUpload(e.target.files);
              e.target.value = "";
            }}
          />
        </div>
        {/* 面包屑路径 */}
        <div className="flex flex-wrap items-center gap-0.5 border-b px-2 py-1 text-xs text-muted-foreground">
          {crumbs.map((c, i) => (
            <span key={c.name} className="flex items-center gap-0.5">
              {i > 0 && <span>/</span>}
              <button className="hover:text-foreground hover:underline" onClick={() => (c.name === "" ? goRoot() : (setDir(c.name), setCurrent(null)))}>
                {c.label}
              </button>
            </span>
          ))}
        </div>
        <div className="min-h-0 flex-1 overflow-auto p-1">
          {filtered.length === 0 && <p className="py-6 text-center text-xs text-muted-foreground">空目录或无匹配文件</p>}
          {filtered.map((it) => {
            const rel = dir ? `${dir}/${it.name}` : it.name;
            return (
              <div
                key={it.name}
                className={`group flex items-center gap-1 rounded px-1.5 py-1 text-sm hover:bg-accent/50 ${current === rel ? "bg-accent" : ""}`}
              >
                <button className="flex min-w-0 flex-1 items-center gap-1.5 text-left" onClick={() => openItem(it)} title={it.isDir ? "进入目录" : "打开文件"}>
                  {it.isDir ? (
                    <Folder className="h-4 w-4 shrink-0 text-primary" />
                  ) : isZip(it.name) ? (
                    <Package className="h-4 w-4 shrink-0 text-amber-500" />
                  ) : (
                    <FileText className="h-4 w-4 shrink-0 text-muted-foreground" />
                  )}
                  <span className="truncate font-mono text-xs">{it.name}</span>
                  {!it.isDir && <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{fmtSize(it.size)}</span>}
                </button>
                <span className="hidden shrink-0 items-center gap-0.5 group-hover:flex">
                  {isZip(it.name) && (
                    <button className="rounded p-0.5 text-muted-foreground hover:text-foreground" title="解压到当前目录" onClick={() => doUnzip(it)}>
                      <Box className="h-3.5 w-3.5" />
                    </button>
                  )}
                  {!it.isDir && (
                    <a
                      className="rounded p-0.5 text-muted-foreground hover:text-foreground"
                      href={code.downloadUrl(rel)}
                      download={it.name}
                      title="下载"
                    >
                      <Download className="h-3.5 w-3.5" />
                    </a>
                  )}
                  <button className="rounded p-0.5 text-muted-foreground hover:text-foreground" title="重命名" onClick={() => doRename(it)}>
                    <Pencil className="h-3.5 w-3.5" />
                  </button>
                  <button className="rounded p-0.5 text-muted-foreground hover:text-destructive" title="删除" onClick={() => remove(it)}>
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </span>
              </div>
            );
          })}
        </div>
      </Card>

      {/* 右栏：浏览 / 编辑 */}
      <Card className="flex min-h-[50vh] min-w-0 flex-1 flex-col p-0 lg:min-h-0">
        {current ? (
          <>
            <div className="flex flex-wrap items-center justify-between gap-2 border-b px-2 py-1.5">
              <span className="truncate font-mono text-xs text-muted-foreground">{current}{dirty ? " ●" : ""}</span>
              <div className="flex shrink-0 flex-wrap gap-1">
                <Button size="sm" className="h-7 text-xs" onClick={save}>
                  <Save className="h-3.5 w-3.5" /> 保存
                </Button>
                <Button size="sm" variant="outline" className="h-7 text-xs" onClick={() => setFindOpen({ focusReplace: false })} title="查找/替换 (Ctrl+F / Ctrl+H)">
                  <Search className="h-3.5 w-3.5" /> 查找替换
                </Button>
                <Button size="sm" variant="outline" className="h-7 text-xs" onClick={toggleComment} title="注释/取消注释 (Ctrl+Q 或 Ctrl+/)">
                  <FileCode className="h-3.5 w-3.5" /> 注释
                </Button>
                <Button size="sm" variant="outline" className="h-7 text-xs" onClick={() => setReplOpen(true)} title="批量替换当前目录及子目录">
                  <Replace className="h-3.5 w-3.5" /> 批量替换
                </Button>
                <a
                  className="inline-flex h-7 items-center gap-1 rounded-md border bg-background px-2 text-xs hover:bg-accent"
                  href={code.downloadUrl(current)}
                  download={current.split("/").pop()}
                >
                  <Download className="h-3.5 w-3.5" /> 下载
                </a>
                <Button size="sm" variant="secondary" className="h-7 text-xs" onClick={closeEditor}>
                  <X className="h-3.5 w-3.5" /> 关闭
                </Button>
              </div>
            </div>
            <textarea
              ref={taRef}
              className="min-h-0 w-full flex-1 resize-none bg-background p-3 font-mono text-sm leading-relaxed"
              value={content}
              onChange={(e) => {
                setContent(e.target.value);
                setDirty(true);
              }}
              onKeyDown={onEditorKeyDown}
              spellCheck={false}
            />
          </>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col">
            <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
              <span className="font-mono text-xs text-muted-foreground">/{dir}</span>
              <Button size="sm" variant="outline" className="h-7 text-xs" onClick={() => setReplOpen(true)} title="批量替换当前目录及子目录的文本文件">
                <Replace className="h-3.5 w-3.5" /> 当前目录批量替换
              </Button>
            </div>
            <div className="min-h-0 flex-1 overflow-auto p-3">
              <p className="mb-2 text-xs text-muted-foreground">当前文件夹的子目录（点击进入）：</p>
              {items.filter((it) => it.isDir).length === 0 && <p className="text-sm text-muted-foreground">（无子目录）</p>}
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
                {items
                  .filter((it) => it.isDir)
                  .map((it) => {
                    const rel = dir ? `${dir}/${it.name}` : it.name;
                    return (
                      <button
                        key={it.name}
                        className="flex items-center gap-2 rounded-lg border px-3 py-2.5 text-left text-sm hover:bg-accent/50"
                        onClick={() => {
                          setDir(rel);
                          setSearch("");
                        }}
                      >
                        <Folder className="h-5 w-5 shrink-0 text-primary" />
                        <span className="truncate font-mono text-xs">{it.name}</span>
                      </button>
                    );
                  })}
              </div>
              <p className="mb-2 mt-5 text-xs text-muted-foreground">当前文件夹的文件（点击打开编辑）：</p>
              {items.filter((it) => !it.isDir).length === 0 && <p className="text-sm text-muted-foreground">（无文件）</p>}
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
                {items
                  .filter((it) => !it.isDir)
                  .map((it) => {
                    return (
                      <button
                        key={it.name}
                        className="flex items-center gap-2 rounded-lg border px-3 py-2.5 text-left text-sm hover:bg-accent/50"
                        onClick={() => openItem(it)}
                      >
                        {isZip(it.name) ? <Package className="h-5 w-5 shrink-0 text-amber-500" /> : <FileText className="h-5 w-5 shrink-0 text-muted-foreground" />}
                        <span className="truncate font-mono text-xs">{it.name}</span>
                        <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{fmtSize(it.size)}</span>
                      </button>
                    );
                  })}
              </div>
              <p className="mt-5 text-xs text-muted-foreground">
                提示：左侧文件树可返回上级 / 过滤 / 新建 / 上传；打开文件后支持 Ctrl+S 保存、Ctrl+F 查找、Ctrl+H 替换、Ctrl+Q 注释切换。
              </p>
            </div>
          </div>
        )}
      </Card>

      {busy && (
        <div className="pointer-events-none fixed inset-x-0 top-16 z-50 flex justify-center">
          <div className="rounded-full border bg-background px-4 py-1.5 text-sm shadow-lg">{busy}</div>
        </div>
      )}

      {notice && (
        <div
          className={`fixed inset-x-0 top-16 z-40 mx-auto w-fit max-w-[90vw] rounded-lg border px-3 py-2 text-sm shadow-lg ${
            notice.type === "ok"
              ? "border-primary/30 bg-primary/10 text-primary"
              : notice.type === "warn"
                ? "border-amber-500/40 bg-amber-500/10 text-amber-600"
                : "border-destructive/30 bg-destructive/10 text-destructive"
          }`}
        >
          <pre className="max-h-60 overflow-auto whitespace-pre-wrap font-sans">{notice.msg}</pre>
        </div>
      )}

      {findOpen && current && (
        <FindReplace
          content={content}
          taRef={taRef}
          focusReplace={findOpen.focusReplace}
          onContent={(c) => {
            setContent(c);
            setDirty(true);
          }}
          onClose={() => setFindOpen(null)}
        />
      )}

      {replOpen && (
        <BatchReplace
          dir={dir}
          currentPath={current}
          onContent={(c) => {
            setContent(c);
            setDirty(true);
          }}
          onClose={() => setReplOpen(false)}
          onDone={(msg) => {
            notify("ok", msg);
            refresh();
          }}
          onErr={(msg) => notify("err", msg)}
          onBusy={setBusy}
        />
      )}
    </div>
  );
}

// ================= 当前文件查找/替换 =================

function FindReplace({
  content,
  taRef,
  focusReplace,
  onContent,
  onClose,
}: {
  content: string;
  taRef: React.RefObject<HTMLTextAreaElement | null>;
  focusReplace: boolean;
  onContent: (c: string) => void;
  onClose: () => void;
}) {
  const [find, setFind] = useState("");
  const [rep, setRep] = useState("");
  const [matchCase, setMatchCase] = useState(false);
  const [isRegex, setIsRegex] = useState(false);
  const [cur, setCur] = useState(0);
  const replaceRef = useRef<HTMLInputElement>(null);

  const matches = useMemo(() => findMatches(content, find, !matchCase, isRegex), [content, find, matchCase, isRegex]);

  useEffect(() => {
    setCur(0);
  }, [find, matchCase, isRegex]);

  useEffect(() => {
    if (focusReplace) replaceRef.current?.focus();
  }, [focusReplace]);

  // 当前匹配同步到 textarea 选区
  useEffect(() => {
    const ta = taRef.current;
    if (!ta || matches.length === 0) return;
    const m = matches[cur % matches.length];
    ta.focus();
    ta.setSelectionRange(m.idx, m.idx + m.len);
  }, [cur, matches, taRef]);

  const goto = (delta: number) => {
    if (!matches.length) return;
    setCur((c) => (c + delta + matches.length) % matches.length);
  };

  const replaceOne = () => {
    const ta = taRef.current;
    if (!ta || !matches.length) return;
    const m = matches[cur % matches.length];
    const before = content.slice(0, m.idx);
    const after = content.slice(m.idx + m.len);
    onContent(before + rep + after);
    setCur((c) => c % Math.max(1, matches.length - 1));
  };

  const replaceAll = () => {
    if (!matches.length) return;
    let out: string;
    if (isRegex) {
      try {
        out = content.replace(new RegExp(find, matchCase ? "g" : "gi"), rep);
      } catch {
        return;
      }
    } else if (matchCase) {
      out = content.split(find).join(rep);
    } else {
      // 忽略大小写逐个替换（按匹配位置倒序替换避免偏移）
      out = content;
      for (let i = matches.length - 1; i >= 0; i--) {
        const m = matches[i];
        out = out.slice(0, m.idx) + rep + out.slice(m.idx + m.len);
      }
    }
    onContent(out);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onClose();
    if (e.key === "Enter") {
      e.preventDefault();
      if (e.shiftKey) goto(-1);
      else goto(1);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/30 p-4 pt-20" onClick={onClose}>
      <div className="w-full max-w-md rounded-lg border bg-card p-4 shadow-lg" onClick={(e) => e.stopPropagation()}>
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-sm font-semibold">查找 / 替换</h3>
          <button className="text-muted-foreground hover:text-foreground" onClick={onClose}>
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="space-y-2">
          <div className="flex gap-2">
            <input
              autoFocus={!focusReplace}
              className="h-8 flex-1 rounded border bg-background px-2 font-mono text-sm"
              placeholder="查找…"
              value={find}
              onChange={(e) => setFind(e.target.value)}
              onKeyDown={onKeyDown}
            />
            <span className="flex w-20 shrink-0 items-center justify-center text-xs text-muted-foreground">
              {find ? (matches.length ? `${cur % matches.length + 1}/${matches.length}` : "无结果") : ""}
            </span>
          </div>
          <div className="flex gap-2">
            <input
              ref={replaceRef}
              className="h-8 flex-1 rounded border bg-background px-2 font-mono text-sm"
              placeholder="替换为…（留空则删除匹配）"
              value={rep}
              onChange={(e) => setRep(e.target.value)}
              onKeyDown={onKeyDown}
            />
            <Button size="sm" variant="outline" className="h-8 text-xs" disabled={!matches.length} onClick={replaceOne}>
              替换
            </Button>
            <Button size="sm" variant="outline" className="h-8 text-xs" disabled={!matches.length} onClick={replaceAll}>
              全部替换
            </Button>
          </div>
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <div className="flex gap-3">
              <label className="flex items-center gap-1">
                <input type="checkbox" checked={!matchCase} onChange={(e) => setMatchCase(!e.target.checked)} /> 忽略大小写
              </label>
              <label className="flex items-center gap-1">
                <input type="checkbox" checked={isRegex} onChange={(e) => setIsRegex(e.target.checked)} /> 正则
              </label>
            </div>
            <span>Enter=下一个 Shift+Enter=上一个 Esc=关闭</span>
          </div>
        </div>
      </div>
    </div>
  );
}

// ================= 目录批量替换 =================

interface ReplFile {
  path: string;
  size: number;
}

async function gatherFiles(dir: string, out: ReplFile[]) {
  const list = await code.list(dir);
  for (const it of list) {
    const full = dir ? `${dir}/${it.name}` : it.name;
    if (it.isDir) await gatherFiles(full, out);
    else out.push({ path: full, size: it.size || 0 });
  }
}

function replaceText(s: string, from: string, to: string, caseSensitive: boolean): { out: string; count: number } {
  if (caseSensitive) {
    const parts = s.split(from);
    return { out: parts.join(to), count: parts.length - 1 };
  }
  const lo = s.toLowerCase();
  const pin = from.toLowerCase();
  if (!pin) return { out: s, count: 0 };
  let count = 0;
  let out = "";
  let i = 0;
  for (;;) {
    const k = lo.indexOf(pin, i);
    if (k === -1) {
      out += s.slice(i);
      break;
    }
    out += s.slice(i, k) + to;
    i = k + from.length;
    count++;
  }
  return { out, count };
}

function BatchReplace({
  dir,
  currentPath,
  onContent,
  onClose,
  onDone,
  onErr,
  onBusy,
}: {
  dir: string;
  currentPath: string | null;
  onContent: (c: string) => void;
  onClose: () => void;
  onDone: (msg: string) => void;
  onErr: (msg: string) => void;
  onBusy: (s: string) => void;
}) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [useRegex, setUseRegex] = useState(false);
  const [caseSensitive, setCaseSensitive] = useState(false);

  const start = async () => {
    if (!from.trim()) {
      onErr("请输入查找内容");
      return;
    }
    let re: RegExp | null = null;
    if (useRegex) {
      try {
        re = new RegExp(from, caseSensitive ? "g" : "gi");
      } catch (e) {
        onErr("正则错误：" + (e as Error).message);
        return;
      }
    }
    if (!window.confirm(`将在「${dir || "docroot 全目录"}」及其子目录的文本文件中执行替换，建议先备份。继续？`)) return;
    onClose();
    onBusy("正在扫描文件…");
    try {
      const all: ReplFile[] = [];
      await gatherFiles(dir, all);
      const modified: string[] = [];
      const skipped: string[] = [];
      let replaced = 0;
      for (let i = 0; i < all.length; i++) {
        const f = all[i];
        const base = f.path.split("/").pop() || "";
        if (f.path.includes(".bak.") || base.startsWith(".")) {
          skipped.push(f.path);
          continue;
        }
        if (f.size > 5 * 1024 * 1024) {
          skipped.push(f.path);
          continue;
        }
        onBusy(`正在替换 ${i + 1}/${all.length} …`);
        let content: string;
        try {
          content = await code.read(f.path);
        } catch {
          skipped.push(f.path);
          continue;
        }
        if (content.includes("\0")) {
          skipped.push(f.path);
          continue;
        }
        let out: string;
        let count: number;
        if (re) {
          const m = content.match(re);
          count = m ? m.length : 0;
          if (!count) continue;
          out = content.replace(re, to);
        } else {
          const r = replaceText(content, from, to, caseSensitive);
          count = r.count;
          if (!count) continue;
          out = r.out;
        }
        try {
          await code.saveFile(f.path, out);
          modified.push(f.path);
          replaced += count;
          if (f.path === currentPath) onContent(out); // 当前打开的文件同步更新
        } catch {
          skipped.push(f.path);
        }
      }
      let msg = `✅ 批量替换完成：${modified.length} 个文件，共 ${replaced} 处` + (skipped.length ? `，跳过 ${skipped.length} 个` : "");
      if (modified.length) {
        msg += "\n" + modified.slice(0, 20).map((x) => "  • " + x).join("\n");
        if (modified.length > 20) msg += `\n  … 共 ${modified.length} 个`;
      }
      onDone(msg);
    } catch (e) {
      onErr("批量替换失败: " + (e as Error).message);
    } finally {
      onBusy("");
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4" onClick={onClose}>
      <div className="w-full max-w-md rounded-lg border bg-card p-4 shadow-lg" onClick={(e) => e.stopPropagation()}>
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-sm font-semibold">批量替换内容</h3>
          <button className="text-muted-foreground hover:text-foreground" onClick={onClose}>
            <X className="h-4 w-4" />
          </button>
        </div>
        <p className="mb-3 text-xs text-muted-foreground">范围：{dir ? `/${dir}` : "docroot（全目录）"} 及其子目录的文本文件（跳过隐藏文件、.bak. 备份与 &gt;5MB 文件）</p>
        <div className="space-y-2">
          <input
            autoFocus
            className="h-8 w-full rounded border bg-background px-2 font-mono text-sm"
            placeholder="查找内容…"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
          <input
            className="h-8 w-full rounded border bg-background px-2 font-mono text-sm"
            placeholder="替换为…"
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
          <div className="flex gap-4 text-xs text-muted-foreground">
            <label className="flex items-center gap-1">
              <input type="checkbox" checked={useRegex} onChange={(e) => setUseRegex(e.target.checked)} /> 正则表达式
            </label>
            <label className="flex items-center gap-1">
              <input type="checkbox" checked={caseSensitive} onChange={(e) => setCaseSensitive(e.target.checked)} /> 区分大小写
            </label>
          </div>
          <div className="flex justify-end gap-2 pt-1">
            <Button size="sm" variant="secondary" onClick={onClose}>
              取消
            </Button>
            <Button size="sm" onClick={start}>
              开始替换
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
