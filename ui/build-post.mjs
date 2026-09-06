// 构建后处理：
//  - 生成 dist/assets 下全部静态产物的预压缩 .gz（Go 侧静态服务命中时
//    Content-Encoding: gzip 直接返回，实测 wasm 520KB→218KB / js 大电池减半）。
//    已压缩格式（woff2 等）gz 更大时跳过。
//  - 恢复 web/dist 占位（Vite emptyOutDir 会清空产物目录，.gitkeep 用于保证
//    clone 后 go:embed all:dist 非空、可直接 go build）
import { gzipSync } from "node:zlib";
import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const distDir = resolve(dirname(fileURLToPath(import.meta.url)), "../web/dist");

function gzipDir(dir) {
  const entries = readdirSync(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      gzipDir(full);
      continue;
    }
    if (entry.name.endsWith(".gz")) continue;
    const { size } = statSync(full);
    const gz = gzipSync(readFileSync(full), { level: 9 });
    if (gz.byteLength < size) {
      writeFileSync(full + ".gz", gz);
    }
  }
}

const assetsDir = join(distDir, "assets");
try {
  gzipDir(assetsDir);
} catch (error) {
  console.warn("gzip 产物生成失败（跳过）：", error);
}

writeFileSync(join(distDir, ".gitkeep"), "");