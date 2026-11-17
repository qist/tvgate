import { api } from "./http";

export interface PHPConfig {
  enabled: boolean;
  path: string;
  docroot: string;
  index: string[];
  /** 额外代码目录（多目录）：代码文件管理器可直接浏览；docroot 内指向这些目录的软链接可被 PHP 服务 */
  allowed_roots: string[];
  worker_mode: string;
  workers: number;
  /** docroot 未通过安全校验时的原因（如被设为 / 或系统目录），空串表示正常 */
  docroot_error?: string;
}

const empty = (): PHPConfig => ({ enabled: false, path: "/php/", docroot: "www", index: ["index.php", "index.html"], allowed_roots: [], worker_mode: "", workers: 0 });

export async function getPHP(): Promise<PHPConfig> {
  try {
    const data = await api.get<Partial<PHPConfig>>("config/php");
    return { ...empty(), ...data, index: data.index || [], allowed_roots: data.allowed_roots || [] } as PHPConfig;
  } catch {
    return empty();
  }
}

export async function savePHP(cfg: PHPConfig): Promise<void> {
  await api.post("config/save-php", cfg);
}