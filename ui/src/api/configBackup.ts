import { resolveBase } from "./base";
import { ApiError } from "./http";

const base = () => resolveBase() + "config/backup";

export async function list(): Promise<string[]> {
  const r = await fetch(`${base()}/list`, { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  const d = await r.json();
  return (d && d.backups) || [];
}

export async function remove(file: string): Promise<void> {
  const r = await fetch(`${base()}/delete?file=${encodeURIComponent(file)}`, {
    method: "POST",
    credentials: "same-origin",
  });
  if (!r.ok) throw new Error(await r.text());
}

export async function restore(file: string): Promise<void> {
  const r = await fetch(`${base()}/restore?file=${encodeURIComponent(file)}`, {
    method: "POST",
    credentials: "same-origin",
  });
  if (!r.ok) throw await ApiError.from(r);
}

export async function batchDelete(files: string[]): Promise<string> {
  const fd = new FormData();
  files.forEach((f) => fd.append("files", encodeURIComponent(f)));
  const r = await fetch(`${base()}/batch-delete`, {
    method: "POST",
    credentials: "same-origin",
    body: fd,
  });
  const text = await r.text();
  if (!r.ok) throw new Error(text);
  return text;
}

export function downloadUrl(file: string): string {
  return `${base()}/download?file=${encodeURIComponent(file)}`;
}

/** 手动备份当前配置 → { message, created } */
export async function create(): Promise<{ message: string; created: boolean }> {
  const r = await fetch(`${base()}/create`, { method: "POST", credentials: "same-origin" });
  const text = await r.text();
  if (!r.ok) throw new Error(text);
  try {
    return JSON.parse(text) as { message: string; created: boolean };
  } catch {
    return { message: text, created: true };
  }
}

/** 清理备份：每个文件保留最近 keep 份 → { message, deleted } */
export async function cleanup(keep: number): Promise<{ message: string; deleted: number }> {
  const r = await fetch(`${base()}/cleanup`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ keep }),
  });
  const text = await r.text();
  if (!r.ok) throw new Error(text);
  try {
    return JSON.parse(text) as { message: string; deleted: number };
  } catch {
    return { message: text, deleted: 0 };
  }
}