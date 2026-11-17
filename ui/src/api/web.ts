import { api } from "./http";

export interface WebConfig {
  enabled: boolean;
  username: string;
  /** 现有密码不回显：留空保存表示保持原密码不变 */
  password: string;
  /** 修改密码时必填：当前（旧）密码，用于确认操作人身份 */
  old_password?: string;
  path: string;
  /** 是否已设置密码（用于提示"留空保持不变"） */
  password_set?: boolean;
  /** 当前密码是否为默认/弱口令（弱口令会锁定后台高危操作） */
  password_weak?: boolean;
  password_weak_hint?: string;
}

export async function getWeb(): Promise<WebConfig> {
  try {
    const data = await api.get<Partial<WebConfig>>("config/web");
    return {
      enabled: data.enabled === true,
      username: data.username || "",
      password: "",
      old_password: "",
      path: data.path || "/web/",
      password_set: data.password_set === true,
      password_weak: data.password_weak === true,
      password_weak_hint: data.password_weak_hint || "",
    };
  } catch {
    return {
      enabled: false,
      username: "",
      password: "",
      old_password: "",
      path: "/web/",
      password_set: false,
      password_weak: false,
      password_weak_hint: "",
    };
  }
}

export async function saveWeb(cfg: WebConfig): Promise<void> {
  await api.post("config/save-web", cfg);
}
