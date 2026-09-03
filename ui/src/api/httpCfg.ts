import { api } from "./http";

export interface HTTPConfig {
  timeout: string;
  connect_timeout: string;
  keepalive: string;
  response_header_timeout: string;
  idle_conn_timeout: string;
  tls_handshake_timeout: string;
  expect_continue_timeout: string;
  max_idle_conns: number;
  max_idle_conns_per_host: number;
  max_conns_per_host: number;
  disable_keepalives: boolean;
  insecure_skip_verify: boolean;
}

const empty = (): HTTPConfig => ({
  timeout: "0s",
  connect_timeout: "10s",
  keepalive: "10s",
  response_header_timeout: "10s",
  idle_conn_timeout: "30s",
  tls_handshake_timeout: "10s",
  expect_continue_timeout: "1s",
  max_idle_conns: 1000,
  max_idle_conns_per_host: 32,
  max_conns_per_host: 64,
  disable_keepalives: false,
  insecure_skip_verify: false,
});

export async function getHTTP(): Promise<HTTPConfig> {
  try {
    const data = await api.get<Partial<HTTPConfig>>("config/http");
    return { ...empty(), ...data } as HTTPConfig;
  } catch {
    return empty();
  }
}

export async function saveHTTP(cfg: HTTPConfig): Promise<void> {
  await api.post("config/save-http", cfg);
}