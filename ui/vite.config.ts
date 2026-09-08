import legacy from "@vitejs/plugin-legacy";
import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig(() => ({
  base: "./", // 相对路径，挂到任意 web.path 下自洽
  resolve: {
    alias: {
      "@": resolve(__dirname, "src"),
    },
  },
  // Tailwind 经 postcss.config.js 处理（已降级到 v3 以兼容 Android 8 WebView 的
  // oklch/color-mix 缺失；并叠加 postcss-gap-properties / postcss-preset-env 兜底
  // flex gap 与 CSS Color 4 语法）。
  plugins: [
    react(),
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
    // modern 包语法目标必须压到 Android 8 WebView（Chromium 62-68）能解析的级别。
    // 默认 chrome64/es2020 会保留 `?.`/`??`（Chrome 80+ 才支持）等语法；而 Android 8
    // 的 WebView 虽然支持 `<script type=module>`、却不支持 es2020 语法，于是它去跑
    // modern 包→遇 `?.` 直接 SyntaxError→整包不执行→白屏"打不开"，且 legacy 的 nomodule
    // 降级包因浏览器支持 module 而永远不会执行。压到 chrome49 与 legacy targets 对齐，
    // 消除这段缝隙；不支持 module 的更老 WebView 仍走 nomodule SystemJS 降级包。
    target: ["chrome49"],
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
