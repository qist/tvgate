import { resolveBase } from "./base";
import { ApiError } from "./http";

const base = () => resolveBase();

async function textOf(r: Response): Promise<string> {
  return r.text();
}

export async function load(): Promise<string> {
  const r = await fetch(base() + "config", {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
    credentials: "same-origin",
  });
  if (!r.ok) throw await ApiError.from(r);
  return textOf(r);
}

export async function save(content: string): Promise<string> {
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), 10000);
  try {
    const r = await fetch(base() + "config/save", {
      method: "POST",
      headers: { "Content-Type": "text/plain; charset=utf-8" },
      credentials: "same-origin",
      body: content,
      signal: ctrl.signal,
    });
    if (!r.ok) throw await ApiError.from(r);
    return textOf(r);
  } finally {
    clearTimeout(t);
  }
}

export async function validate(content: string): Promise<string> {
  const r = await fetch(base() + "config/validate", {
    method: "POST",
    headers: { "Content-Type": "text/plain; charset=utf-8" },
    credentials: "same-origin",
    body: content,
  });
  if (!r.ok) throw await ApiError.from(r);
  return textOf(r);
}

// 解析后端返回，兼容 JSON {status,message} 与纯文本
export function parseStatus(data: string, fallbackOk: string): { ok: boolean; msg: string } {
  try {
    const j = JSON.parse(data);
    if (j && typeof j === "object" && "status" in j) {
      return { ok: j.status === "success", msg: j.message || (j.status === "success" ? fallbackOk : "操作失败") };
    }
  } catch {
    /* 非 JSON，按纯文本成功处理 */
  }
  return { ok: true, msg: fallbackOk };
}