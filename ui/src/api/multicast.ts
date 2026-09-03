import { api } from "./http";

export interface MulticastConfig {
  multicast_ifaces: string[];
  mcast_rejoin_interval: string;
  fcc_type: string;
  fcc_cache_size: number;
  fcc_listen_port_min: number;
  fcc_listen_port_max: number;
  upstream_interface: string;
  upstream_interface_fcc: string;
}

const empty = (): MulticastConfig => ({
  multicast_ifaces: [],
  mcast_rejoin_interval: "",
  fcc_type: "",
  fcc_cache_size: 16384,
  fcc_listen_port_min: 40000,
  fcc_listen_port_max: 50000,
  upstream_interface: "",
  upstream_interface_fcc: "",
});

export async function getMulticast(): Promise<MulticastConfig> {
  try {
    const data = await api.get<Partial<MulticastConfig>>("config/multicast");
    return {
      ...empty(),
      ...data,
      // 旧后端在未配置组播段时返回 null，会覆盖上面的默认空数组
      multicast_ifaces: Array.isArray(data.multicast_ifaces) ? data.multicast_ifaces : [],
    } as MulticastConfig;
  } catch {
    return empty();
  }
}

export async function saveMulticast(cfg: MulticastConfig): Promise<void> {
  await api.post("config/save-multicast", cfg);
}