/**
 * violet 色阶改为**由 CSS 变量驱动**：`--color-violet-N` 由 `player-theme-*`（6 套配色）与
 * `:root` 兜底（默认翠绿）给出，三元组格式供 `<alpha-value>` 使用。
 * 这样 `bg-violet-500/24`、`text-violet-700` 等 161 处工具类会随配色风格真正换色
 * （此前 Tailwind 把它们编译成写死的紫色，主题里的变量没人消费）。
 */
const VIOLET_SHADES = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950];
const violet = Object.fromEntries(
  VIOLET_SHADES.map((shade) => [shade, `rgb(var(--color-violet-${shade}) / <alpha-value>)`]),
);

/** @type {import('tailwindcss').Config} */
export default {
  // 与 index.css 中 .dark 类的应用范围对齐（.dark 挂在 html/body，整棵树后代生效）。
  darkMode: "class",
  content: ["./index.html", "./player.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      fontFamily: {
        sans: [
          "Inter Variable",
          "SF Pro Display",
          "SF Pro Text",
          "Segoe UI Variable",
          "Segoe UI",
          "PingFang SC",
          "Microsoft YaHei",
          "ui-sans-serif",
          "system-ui",
          "sans-serif",
        ],
        mono: ["SFMono-Regular", "Cascadia Code", "Roboto Mono", "ui-monospace", "monospace"],
      },
      // 语义色：沿用 shadcn 变量（:root/.dark 中以逗号 hsl 定义），保证 Android 8 可解析。
      colors: {
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        primary: {
          DEFAULT: "hsl(var(--primary))",
          foreground: "hsl(var(--primary-foreground))",
        },
        secondary: {
          DEFAULT: "hsl(var(--secondary))",
          foreground: "hsl(var(--secondary-foreground))",
        },
        destructive: {
          DEFAULT: "hsl(var(--destructive))",
          foreground: "hsl(var(--destructive-foreground))",
        },
        muted: {
          DEFAULT: "hsl(var(--muted))",
          foreground: "hsl(var(--muted-foreground))",
        },
        accent: {
          DEFAULT: "hsl(var(--accent))",
          foreground: "hsl(var(--accent-foreground))",
        },
        popover: {
          DEFAULT: "hsl(var(--popover))",
          foreground: "hsl(var(--popover-foreground))",
        },
        card: {
          DEFAULT: "hsl(var(--card))",
          foreground: "hsl(var(--card-foreground))",
        },
        // 播放器配色风格接管的主色阶（见上方 VIOLET_SHADES 说明）
        violet,
      },
      borderRadius: {
        sm: "0.5rem",
        DEFAULT: "0.625rem",
        lg: "0.875rem",
        xl: "1rem",
      },
      keyframes: {
        "zoom-fade-out": {
          "0%": { transform: "scale(1)", opacity: "1" },
          "100%": { transform: "scale(1.4)", opacity: "0" },
        },
      },
      animation: {
        "zoom-fade-out": "zoom-fade-out 0.5s ease-out forwards",
      },
    },
  },
  plugins: [],
};
