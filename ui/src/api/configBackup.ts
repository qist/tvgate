import { resolveBase } from "./base";

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
  if (!r.ok) throw new Error(await r.text());
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