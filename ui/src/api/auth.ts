// 鉴权 / 会话状态
import { api } from "./http";

export async function login(username: string, password: string): Promise<void> {
  await api.post("login", { username, password }, { auth: false });
}

export async function logout(): Promise<void> {
  await api.get("logout", { auth: false });
}