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
}

const base = () => resolveBase();

export async function loadConfig(): Promise<GithubConfig> {
  const r = await fetch(base() + "api/github/config", { credentials: "same-origin" });
  if (!r.ok) throw new Error(await r.text());
  return r.json();
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
  return r.json();
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