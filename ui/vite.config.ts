import legacy from "@vitejs/plugin-legacy";
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
  plugins: [
    react(),
    tailwindcss(),
    // 安卓 8 海信电视等旧 WebView（Chromium 57-60）不支持原生 ES Module，
    // `<script type="module">` 会整段不执行 → 页面全黑。legacy 插件为无 module
    // 的浏览器生成 SystemJS 降级包（nomodule 脚本），Babel + core-js 把语法和
    // API 一并补到 chrome 49 基线；modernPolyfills 给现代包也注入 core-js。
    legacy({
      targets: ["chrome >= 49"],
      modernPolyfills: true,
    }),
  ],
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
    // modern 目标由 legacy 插件接管（默认 chrome64/es2020，含 core-js polyfill）；
    // 旧 WebView 走 nomodule SystemJS 降级包。产物经 go:embed 编入单二进制。
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
