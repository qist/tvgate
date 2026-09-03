import { resolveBase } from "./base";

export interface BackupItem {
  name: string;
  original: string;
  time: string;
  size: number;
}

const base = () => resolveBase() + "api/backup";

async function parseJson(r: Response): Promise<any> {
  const text = await r.text();
  try {
    return JSON.parse(text);
  } catch {
    return { status: "error", message: text };
  }
}

export async function list(): Promise<BackupItem[]> {
  const r = await fetch(`${base()}/list`, { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  const d = await r.json();
  if (d.status !== "success") throw new Error(d.message || "加载失败");
  return d.items || [];
}

export async function restore(path: string): Promise<string> {
  const r = await fetch(`${base()}/restore?path=${encodeURIComponent(path)}`, { method: "POST", credentials: "same-origin" });
  const d = await parseJson(r);
  if (d.status !== "success") throw new Error(d.message || "回滚失败");
  return d.message;
}

export async function remove(path: string): Promise<string> {
  const r = await fetch(`${base()}/delete?path=${encodeURIComponent(path)}`, { method: "POST", credentials: "same-origin" });
  const d = await parseJson(r);
  if (d.status !== "success") throw new Error(d.message || "删除失败");
  return d.message;
}

export async function batchDelete(paths: string[]): Promise<string> {
  const r = await fetch(`${base()}/batch-delete`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify({ paths }),
  });
  const d = await parseJson(r);
  if (d.status !== "success") throw new Error(d.message || "批量删除失败");
  return d.message;
}

export async function cleanup(keep: number): Promise<string> {
  const r = await fetch(`${base()}/cleanup`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify({ keep }),
  });
  const d = await parseJson(r);
  if (d.status !== "success") throw new Error(d.message || "清理失败");
  return d.message;
}

export function downloadUrl(path: string): string {
  return `${base()}/download?path=${encodeURIComponent(path)}`;
}