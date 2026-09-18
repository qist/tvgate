import { api } from "./http";

export interface PlayerConfig {
  enabled: boolean;
  /** 主订阅源：单个源（也支持换行/逗号/分号分隔的多个源） */
  subscription: string;
  /** 多订阅源：追加在主订阅源之后，逐项解析合并（同源去重；单个源失败只跳过自身） */
  subscriptions: string[];
  /** EPG 来源：模板（含 {name}/{date}）或固定 XMLTV 地址；也支持换行/分号/逗号分隔的多个 */
  epg: string;
  /** 多 EPG 来源：追加在 epg 之后，按序合并（同类型来源节目单合并，不同类型互补补齐） */
  epgs: string[];
  logo: string;
  logo_dir: string;
  /** Go time.Duration 字符串（如 2h / 30m），空串表示默认 2h */
  update_interval: string;
  ua: string;
  /** YAML 标记位：安卓设备启动是否进入播放页（客户端 App 读取该标记自行控制） */
  android_autoplay: boolean;
}

export async function getPlayer(): Promise<PlayerConfig> {
  const data = await api.get<Partial<PlayerConfig> & { update_interval?: string }>("config/player");
  const interval = data.update_interval || "";
  return {
    enabled: data.enabled === true,
    subscription: data.subscription || "",
    subscriptions: Array.isArray(data.subscriptions)
      ? data.subscriptions.filter((s): s is string => typeof s === "string" && s.trim() !== "")
      : [],
    epg: data.epg || "",
    epgs: Array.isArray(data.epgs)
      ? data.epgs.filter((s): s is string => typeof s === "string" && s.trim() !== "")
      : [],
    logo: data.logo || "",
    logo_dir: data.logo_dir || "",
    // 默认 2h 时后端返回 "2h0m0s"，按旧版行为显示为空（代表默认）
    update_interval: interval === "2h0m0s" ? "" : interval,
    ua: data.ua || "",
    // 后端未配置时返回 null/undefined，按关闭显示；显式 true 才显示开启
    android_autoplay: data.android_autoplay === true,
  };
}

export async function savePlayer(cfg: PlayerConfig): Promise<void> {
  await api.post("config/save-player", cfg);
}