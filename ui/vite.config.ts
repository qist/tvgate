import { resolve } from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig(() => ({
  base: "./", // 相对路径，挂到任意 web.path 下自洽
  resolve: {
    alias: {
      "@": resolve(__dirname, "src"),
    },
  },
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      // dev 环境走同源代理，保持 Cookie / CSRF 校验
      "/web": "http://127.0.0.1:8888",
    },
  },
  build: {
    outDir: resolve(__dirname, "../web/dist"),
    emptyOutDir: true,
    sourcemap: false,
  },
}));