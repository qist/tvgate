import { resolveBase } from "./base";

export interface SyncEntry {
  name: string;
  enabled: boolean;
  type: string; // github | gitlab | gitee
  host: string;
  repo: string;
  branch: string;
  token: string; // 已保存令牌以掩码占位回显，原样提交即保留
  interval: string;
  timeout: string;
  repo_path: string;
  local_path: string;
  only_php: boolean;
  backup: boolean;
  delete: boolean;
  protect: string[];
}

const base = () => resolveBase() + "api/sync";

export async function loadConfig(): Promise<SyncEntry[]> {
  const r = await fetch(`${base()}/config`, { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  const data = await r.json();
  // 未配置同步段时后端可能返回 null，兜底空数组避免 .map 崩溃
  return Array.isArray(data) ? data : [];
}

export async function saveConfig(entries: SyncEntry[]): Promise<void> {
  const r = await fetch(`${base()}/config/save`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify(entries),
  });
  if (!r.ok) throw new Error(await r.text());
}

export async function fetchBranches(input: { type?: string; host?: string; repo: string; token?: string }): Promise<string[]> {
  const p = new URLSearchParams({
    type: input.type || "github",
    host: input.host || "",
    repo: input.repo,
    token: input.token || "",
  });
  const r = await fetch(`${base()}/branches?${p.toString()}`, { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  const data = await r.json();
  return Array.isArray(data) ? data : [];
}

export function defaultEntry(): SyncEntry {
  return {
    name: "",
    enabled: false,
    type: "github",
    host: "",
    repo: "",
    branch: "main",
    token: "",
    interval: "60s",
    timeout: "15s",
    repo_path: ".",
    local_path: "tvbox",
    only_php: false,
    backup: true,
    delete: false,
    protect: [],
  };
}