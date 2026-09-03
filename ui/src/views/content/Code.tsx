import { useCallback, useEffect, useRef, useState } from "react";
import { ArrowUp, Download, FilePlus, Folder, Trash2, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import * as code from "@/api/code";

export function CodePage() {
  const [dir, setDir] = useState("");
  const [items, setItems] = useState<code.CodeItem[]>([]);
  const [current, setCurrent] = useState<string | null>(null); // 当前打开文件相对路径
  const [content, setContent] = useState("");
  const [notice, setNotice] = useState<{ type: "ok" | "err"; msg: string } | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const refresh = useCallback(async () => {
    setItems(await code.list(dir));
  }, [dir]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const openFile = async (name: string) => {
    const p = dir ? `${dir}/${name}` : name;
    try {
      const c = await code.read(p);
      setCurrent(p);
      setContent(c);
      setNotice(null);
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
      setTimeout(() => setNotice(null), 4000);
    }
  };

  const goDir = (name: string) => {
    setDir((d) => (d ? `${d}/${name}` : name));
    setCurrent(null);
    setContent("");
  };

  const parent = () => {
    const i = dir.lastIndexOf("/");
    setDir(i < 0 ? "" : dir.slice(0, i));
    setCurrent(null);
    setContent("");
  };

  const save = async () => {
    if (!current) return;
    try {
      await code.saveFile(current, content);
      setNotice({ type: "ok", msg: `已保存 ${current}` });
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  const create = async () => {
    const name = window.prompt("新文件相对路径（可用斜杠建子目录）:", `${dir ? dir + "/" : ""}new.php`);
    if (!name) return;
    try {
      await code.createFile(name.replace(/^\//, ""));
      if (name.startsWith(dir) || !dir) setCurrent(name.replace(/^\//, ""));
      setNotice({ type: "ok", msg: `已创建 ${name}` });
      refresh();
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  const remove = async (name: string, isDir: boolean) => {
    const p = dir ? `${dir}/${name}` : name;
    if (!window.confirm(`删除 ${isDir ? "目录" : "文件"} ${p}？`)) return;
    try {
      await code.deleteFile(p);
      setNotice({ type: "ok", msg: `已删除 ${p}` });
      refresh();
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  const onUpload = async (files: FileList | null) => {
    if (!files || files.length === 0) return;
    try {
      await code.uploadFiles(dir, files);
      setNotice({ type: "ok", msg: `已上传 ${files.length} 个文件` });
      refresh();
    } catch (e) {
      setNotice({ type: "err", msg: (e as Error).message });
    }
    setTimeout(() => setNotice(null), 4000);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-semibold">代码文件</h1>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={create}>
            <FilePlus className="mr-1 h-4 w-4" /> 新建
          </Button>
          <Button variant="outline" size="sm" onClick={() => fileInputRef.current?.click()}>
            <Upload className="mr-1 h-4 w-4" /> 上传
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
      </div>

      {notice && (
        <div className={`rounded-lg border px-3 py-2 text-sm ${notice.type === "ok" ? "border-primary/30 bg-primary/10 text-primary" : "border-destructive/30 bg-destructive/10 text-destructive"}`}>
          {notice.msg}
        </div>
      )}

      <Card>
        <CardContent className="p-3">
          <div className="mb-2 flex items-center gap-2 text-sm text-muted-foreground">
            <Button size="icon" variant="ghost" disabled={!dir} onClick={parent} title="上级目录">
              <ArrowUp className="h-4 w-4" />
            </Button>
            <span className="font-mono truncate">/{dir}</span>
          </div>
          <div className="grid gap-1 sm:grid-cols-2 lg:grid-cols-3">
            {items.map((it) => (
              <div key={it.name} className="flex items-center gap-2 rounded-lg border px-2 py-1.5 text-sm hover:bg-accent/50">
                <span className="flex-1 min-w-0 flex items-center gap-1.5 cursor-pointer" onClick={() => (it.isDir ? goDir(it.name) : openFile(it.name))}>
                  <Folder className={`h-4 w-4 shrink-0 ${it.isDir ? "text-primary" : "text-muted-foreground"}`} />
                  <span className="truncate font-mono">{it.name}</span>
                </span>
                {!it.isDir && (
                  <a href={code.downloadUrl(dir ? `${dir}/${it.name}` : it.name)} download className="text-muted-foreground hover:text-foreground" title="下载">
                    <Download className="h-4 w-4" />
                  </a>
                )}
                <button className="text-muted-foreground hover:text-destructive" title="删除" onClick={() => remove(it.name, it.isDir)}>
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      {current && (
        <Card>
          <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
            <span className="font-mono text-sm">{current}</span>
            <div className="flex gap-2">
              <Button size="sm" onClick={save}>保存</Button>
              <Button size="sm" variant="outline" onClick={() => setCurrent(null)}>关闭</Button>
            </div>
          </div>
          <textarea
            className="h-[55vh] w-full resize-none bg-background p-3 font-mono text-sm"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            spellCheck={false}
          />
        </Card>
      )}
    </div>
  );
}