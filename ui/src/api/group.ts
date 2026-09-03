import { resolveBase } from "./base";

const base = () => resolveBase() + "config";

export async function load(configType: string, groupName: string): Promise<string> {
  const r = await fetch(`${base()}/group?config=${encodeURIComponent(configType)}&group=${encodeURIComponent(groupName)}`, {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
    credentials: "same-origin",
  });
  if (!r.ok) throw new Error(await r.text());
  return r.text();
}

export async function save(configType: string, groupName: string, content: string): Promise<string> {
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), 10000);
  try {
    const r = await fetch(`${base()}/save-group?config=${encodeURIComponent(configType)}&group=${encodeURIComponent(groupName)}`, {
      method: "POST",
      headers: { "Content-Type": "text/plain; charset=utf-8" },
      credentials: "same-origin",
      body: content,
      signal: ctrl.signal,
    });
    if (!r.ok) throw new Error(await r.text());
    return r.text();
  } finally {
    clearTimeout(t);
  }
}

export async function validate(content: string): Promise<string> {
  const r = await fetch(`${base()}/validate`, {
    method: "POST",
    headers: { "Content-Type": "text/plain; charset=utf-8" },
    credentials: "same-origin",
    body: content,
  });
  if (!r.ok) throw new Error(await r.text());
  return r.text();
}

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

export const GROUP_TYPES = ["jx", "proxygroups"];