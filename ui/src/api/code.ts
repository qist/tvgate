import { resolveBase } from "./base";

export interface CodeItem {
  name: string;
  isDir: boolean;
  size: number;
}

/** 代码管理器根目录（0 = PHP docroot，其后为 php.allowed_roots） */
export interface CodeRoot {
  index: number;
  label: string;
  path: string;
}

const base = () => resolveBase();

// 当前选中的根目录索引：所有接口都带上 root，后端只接受已配置的索引
let rootIndex = 0;
export function setRootIndex(i: number) {
  rootIndex = Number.isFinite(i) && i >= 0 ? i : 0;
}
export function getRootIndex(): number {
  return rootIndex;
}

/** 拼接 root 参数 */
const rq = (params = "") => (params ? `root=${rootIndex}&${params}` : `root=${rootIndex}`);

export async function roots(): Promise<CodeRoot[]> {
  try {
    const r = await fetch(`${base()}api/code/roots`, { credentials: "same-origin" });
    if (!r.ok) return [];
    const d = await r.json();
    return Array.isArray(d?.roots) ? (d.roots as CodeRoot[]) : [];
  } catch {
    return [];
  }
}

export async function list(dir: string): Promise<CodeItem[]> {
  try {
    const r = await fetch(`${base()}api/code/list?${rq(`dir=${encodeURIComponent(dir)}`)}`, { credentials: "same-origin" });
    const d = await r.json();
    return (d && d.items) || [];
  } catch {
    return [];
  }
}

export async function read(path: string): Promise<string> {
  const r = await fetch(`${base()}api/code/read?${rq(`path=${encodeURIComponent(path)}`)}`, { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  const d = await r.json();
  return d.content ?? "";
}

// save / create / delete 均为文本或表单 POST
async function textPost(path: string, body?: string): Promise<void> {
  const r = await fetch(`${base()}api/code/${path}`, { method: "POST", credentials: "same-origin", body });
  if (!r.ok) throw new Error(await r.text());
}

export function saveFile(path: string, content: string): Promise<void> {
  return textPost(`save?${rq(`path=${encodeURIComponent(path)}`)}`, content);
}

export function createFile(path: string, content = "", overwrite = false): Promise<void> {
  const extra = `path=${encodeURIComponent(path)}&type=file${overwrite ? "&overwrite=true" : ""}`;
  return textPost(`new?${rq(extra)}`, content);
}

export function createDir(path: string, overwrite = false): Promise<void> {
  const extra = `path=${encodeURIComponent(path)}&type=dir${overwrite ? "&overwrite=true" : ""}`;
  return textPost(`new?${rq(extra)}`);
}

export function rename(oldPath: string, newName: string): Promise<void> {
  return textPost(`rename?${rq(`path=${encodeURIComponent(oldPath)}&newname=${encodeURIComponent(newName)}`)}`);
}

export function unzip(zipPath: string, dir?: string): Promise<void> {
  const extra = `path=${encodeURIComponent(zipPath)}${dir ? `&dir=${encodeURIComponent(dir)}` : ""}`;
  return textPost(`unzip?${rq(extra)}`);
}

export function deleteFile(path: string): Promise<void> {
  return textPost(`delete?${rq(`path=${encodeURIComponent(path)}`)}`);
}

export function downloadUrl(path: string): string {
  return `${base()}api/code/download?${rq(`path=${encodeURIComponent(path)}`)}`;
}

export interface PhpIssue {
  level: "error" | "warning";
  message: string;
  line: number;
}

/** PHP 语法检测（后端纯文本级扫描，检测编辑器当前内容） */
export async function check(content: string): Promise<{ ok: boolean; issues: PhpIssue[] }> {
  const r = await fetch(`${base()}api/code/check`, { method: "POST", credentials: "same-origin", body: content });
  if (!r.ok) throw new Error(await r.text());
  const d = await r.json();
  return { ok: !!d.ok, issues: d.issues || [] };
}

export interface UploadResult {
  /** 成功落盘的文件名列表（后端逐文件统计，部分失败时 HTTP 仍为 200）。 */
  uploaded: string[];
  failed: string[];
  /** 覆盖同名旧文件时已自动备份的文件名（备份为 <name>.bak.<时间戳>）。 */
  backedUp: string[];
}

export async function uploadFiles(dir: string, files: File[]): Promise<UploadResult> {
  const fd = new FormData();
  fd.append("dir", dir);
  files.forEach((f) => fd.append("file", f));
  const r = await fetch(`${base()}api/code/upload?${rq()}`, { method: "POST", credentials: "same-origin", body: fd });
  if (!r.ok) throw new Error(await r.text());
  const d = await r.json().catch(() => ({}));
  return {
    uploaded: Array.isArray(d.uploaded) ? d.uploaded : [],
    failed: Array.isArray(d.failed) ? d.failed : [],
    backedUp: Array.isArray(d.backed_up) ? d.backed_up : [],
  };
}
