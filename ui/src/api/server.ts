import { api } from "./http";

export interface TLSConfig {
  https_port: number;
  certfile: string;
  keyfile: string;
  ssl_protocols: string;
  ssl_ciphers: string;
  ssl_ecdh_curve: string;
  enable_h3: boolean;
}

export interface ServerConfig {
  port: number;
  http_port: number;
  certfile: string;
  keyfile: string;
  ssl_protocols: string;
  ssl_ciphers: string;
  ssl_ecdh_curve: string;
  http_to_https: boolean;
  tls: TLSConfig;
}

const emptyTLS = (): TLSConfig => ({ https_port: 0, certfile: "", keyfile: "", ssl_protocols: "", ssl_ciphers: "", ssl_ecdh_curve: "", enable_h3: false });

const empty = (): ServerConfig => ({
  port: 80,
  http_port: 0,
  certfile: "",
  keyfile: "",
  ssl_protocols: "",
  ssl_ciphers: "",
  ssl_ecdh_curve: "",
  http_to_https: false,
  tls: emptyTLS(),
});

export async function getServer(): Promise<ServerConfig> {
  try {
    const data = await api.get<Partial<ServerConfig>>("config/server");
    return { ...empty(), ...data, tls: { ...emptyTLS(), ...data.tls } } as ServerConfig;
  } catch {
    return empty();
  }
}

export async function saveServer(cfg: ServerConfig): Promise<void> {
  await api.post("config/save-server", cfg);
}