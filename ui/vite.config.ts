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
      // 播放器页面数据与拉流同源代理
      "/api/player": "http://127.0.0.1:8888",
      "/player": "http://127.0.0.1:8888",
    },
  },
  build: {
    outDir: resolve(__dirname, "../web/dist"),
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      // 双入口：管理后台 index.html + H5 播放器 player.html
      input: {
        index: resolve(__dirname, "index.html"),
        player: resolve(__dirname, "player.html"),
      },
      output: {
        manualChunks: {
          "vendor-react": ["react", "react-dom", "react-dom/client"],
          "vendor-router": ["react-router-dom"],
          "vendor-forms": ["react-hook-form", "zod", "@hookform/resolvers"],
          "vendor-ui": ["class-variance-authority", "clsx", "lucide-react"],
        },
      },
    },
  },
}));
