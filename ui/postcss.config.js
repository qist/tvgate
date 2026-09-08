// 兼容 Android 8 WebView（Chromium 62-68）的 CSS 管线：
// - tailwindcss v3：默认调色板用 rgb/hsl，不再输出 oklch / color-mix（Chrome 111+ 才支持）。
// - autoprefixer：补齐 -webkit- 等前缀。
// - postcss-preset-env(color-functional-notation)：把 css 空格+alpha 语法
//   rgb(255 255 255 / 0.68)、hsl(226 44% 97% / 0.4) 转成 Android 8 支持的
//   rgba(255,255,255,0.68)、hsla(226,44%,97%,0.4) 逗号写法。
// - postcss-gap-polyfill：把 gap（Chrome 84- 的 flex 不支持）转成父负半边距 +
//   子半边距的 margin 兜底，避免播放器控件在旧 WebView 上重叠。
import tailwindcss from "tailwindcss";
import autoprefixer from "autoprefixer";
import postcssPresetEnv from "postcss-preset-env";
import gapPolyfill from "./postcss-gap-polyfill.mjs";

export default {
  plugins: [
    tailwindcss(),
    autoprefixer(),
    postcssPresetEnv({
      features: {
        "color-functional-notation": true,
      },
    }),
    gapPolyfill,
  ],
};
