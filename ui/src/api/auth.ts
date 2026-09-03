// 鉴权 / 会话状态
import { api } from "./http";

export interface SessionInfo {
  authenticated: boolean;
  username?: string;
}

export async function checkAuth(): Promise<boolean> {
  try {
    const res = await api.get<{ authenticated?: boolean }>("auth-status", { auth: false });
    return res?.authenticated !== false;
  } catch {
    return false;
  }
}

export async function login(username: string, password: string): Promise<void> {
  await api.post("login", { username, password }, { auth: false });
}

export async function logout(): Promise<void> {
  await api.get("logout", { auth: false });
}