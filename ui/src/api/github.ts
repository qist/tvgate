import { resolveBase } from "./base";

export interface GithubConfig {
  enabled: boolean;
  url: string;
  backup_urls: string[];
  timeout: string;
  retry: number;
}

export interface Release {
  tag_name: string;
}

export interface GithubStatus {
  state?: string;
  message?: string;
  target_version?: string;
  version?: string;
  /** 当前平台是否支持在线升级（Android APK 内置 so / Windows 为 false） */
  updatable?: boolean;
}

const base = () => resolveBase();

export async function loadConfig(): Promise<GithubConfig> {
  const r = await fetch(base() + "api/github/config", { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  const data = await r.json();
  return {
    ...data,
    // 旧后端在未配置时返回 null，会覆盖默认空数组导致前端 .map 崩溃
    backup_urls: Array.isArray(data?.backup_urls) ? data.backup_urls : [],
  };
}

export async function saveConfig(cfg: GithubConfig): Promise<void> {
  const r = await fetch(base() + "api/github/config/save", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify(cfg),
  });
  if (!r.ok) throw new Error(await r.text());
}

export async function releases(): Promise<Release[]> {
  const r = await fetch(base() + "github/releases", { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  const data = await r.json();
  return Array.isArray(data) ? data : [];
}

export async function triggerUpdate(version: string): Promise<void> {
  const r = await fetch(base() + "github/update", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify({ version }),
  });
  if (!r.ok) throw new Error(await r.text());
}

export async function status(): Promise<GithubStatus> {
  const r = await fetch(base() + "github/status", { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}