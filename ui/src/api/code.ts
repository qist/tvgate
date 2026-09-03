import { resolveBase } from "./base";

export interface CodeItem {
  name: string;
  isDir: boolean;
  size: number;
}

const base = () => resolveBase();

export async function list(dir: string): Promise<CodeItem[]> {
  try {
    const r = await fetch(`${base()}api/code/list?dir=${encodeURIComponent(dir)}`, { credentials: "same-origin" });
    const d = await r.json();
    return (d && d.items) || [];
  } catch {
    return [];
  }
}

export async function read(path: string): Promise<string> {
  const r = await fetch(`${base()}api/code/read?path=${encodeURIComponent(path)}`, { credentials: "same-origin" });
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
  return textPost(`save?path=${encodeURIComponent(path)}`, content);
}

export function createFile(path: string, content = ""): Promise<void> {
  return textPost(`new?path=${encodeURIComponent(path)}&type=file`, content);
}

export function createDir(path: string): Promise<void> {
  return textPost(`new?path=${encodeURIComponent(path)}&type=dir`);
}

export function rename(oldPath: string, newName: string): Promise<void> {
  return textPost(`rename?path=${encodeURIComponent(oldPath)}&newname=${encodeURIComponent(newName)}`);
}

export function unzip(zipPath: string, dir?: string): Promise<void> {
  const q = `unzip?path=${encodeURIComponent(zipPath)}${dir ? `&dir=${encodeURIComponent(dir)}` : ""}`;
  return textPost(q);
}

export function deleteFile(path: string): Promise<void> {
  return textPost(`delete?path=${encodeURIComponent(path)}`);
}

export function downloadUrl(path: string): string {
  return `${base()}api/code/download?path=${encodeURIComponent(path)}`;
}

export async function uploadFiles(dir: string, files: FileList): Promise<void> {
  const fd = new FormData();
  fd.append("dir", dir);
  Array.from(files).forEach((f) => fd.append("file", f));
  const r = await fetch(`${base()}api/code/upload`, { method: "POST", credentials: "same-origin", body: fd });
  if (!r.ok) throw new Error(await r.text());
}