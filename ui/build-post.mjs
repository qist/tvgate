// 构建后恢复 web/dist 占位（Vite emptyOutDir 会清空产物目录，
// .gitkeep 用于保证 clone 后 go:embed all:dist 非空、可直接 go build）
import { writeFileSync } from "node:fs";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const distDir = resolve(dirname(fileURLToPath(import.meta.url)), "../web/dist");
writeFileSync(join(distDir, ".gitkeep"), "");