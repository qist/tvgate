// 轻量 gap → margin 兜底（兼容 Android 8 WebView / Chrome 62-68）。
// 背景：Tailwind 把 `gap-*` 拆成独立工具类（`.gap-4 { gap: 1rem }`），与 `display:flex`
// 不在同一规则里，通用 polyfill（postcss-gap-properties）因无法判断容器类型而不生效；
// 而旧 WebView 的 flex gap 是 Chrome 84+ 才支持 → 直接忽略 → 控件重叠。
//
// 关键约束：不能无脑把 gap 换成 margin —— 那样会破坏现代浏览器（后台 grid/flex 布局）。
// 做法：保留原始 `gap` 声明（现代浏览器直接用），仅在不支持 flex gap 的浏览器上
// （运行时由 polyfills.ts 给 <html> 加 `.no-flexgap` 类）才用 margin 兜底。
// 用 margin 长写法（margin-top/left/right/bottom）避免 gap-x + gap-y 组合时相互覆盖。

const GAP_PROPS = new Set(["gap", "row-gap", "column-gap", "gap-x", "gap-y"]);

const half = (value) => `calc(${value.trim()} / 2)`;

// 给选择器列表每个分支加 `.no-flexgap ` 前缀（处理逗号多选器）
const withScope = (selector) =>
  selector
    .split(",")
    .map((s) => `.no-flexgap ${s.trim()}`)
    .join(", ");

export default {
  postcssPlugin: "postcss-gap-polyfill",
  Rule(rule) {
    if (rule.selector.includes(".no-flexgap")) return; // 避免对自身生成的规则递归

    const gapDecls = rule.nodes.filter(
      (n) => n.type === "decl" && GAP_PROPS.has(n.prop),
    );
    if (gapDecls.length === 0) return;

    let row = null;
    let col = null;
    for (const d of gapDecls) {
      if (d.prop === "gap") {
        const vals = d.value.trim().split(/\s+/);
        row = vals[0];
        col = vals[1] || vals[0];
      } else if (d.prop === "row-gap" || d.prop === "gap-y") {
        row = d.value.trim();
      } else if (d.prop === "column-gap" || d.prop === "gap-x") {
        col = d.value.trim();
      }
    }
    if (row == null && col == null) return;
    row = row || "0";
    col = col || "0";

    // 原始 `gap` 声明保留不动（现代浏览器原生生效）。

    // 父容器：负半边距抵消子元素半边距，避免边缘额外溢出（仅 .no-flexgap 下）
    const parent = rule.clone();
    parent.selector = withScope(rule.selector);
    parent.nodes = [];
    if (row !== "0") {
      parent.append({ prop: "margin-top", value: `-${half(row)}` });
      parent.append({ prop: "margin-bottom", value: `-${half(row)}` });
    }
    if (col !== "0") {
      parent.append({ prop: "margin-left", value: `-${half(col)}` });
      parent.append({ prop: "margin-right", value: `-${half(col)}` });
    }
    rule.parent.insertAfter(rule, parent);

    // 子元素：半边距，等价于 gap（仅 .no-flexgap 下）
    const child = rule.clone();
    child.selector = withScope(`${rule.selector} > *`);
    child.nodes = [];
    if (row !== "0") {
      child.append({ prop: "margin-top", value: half(row) });
      child.append({ prop: "margin-bottom", value: half(row) });
    }
    if (col !== "0") {
      child.append({ prop: "margin-left", value: half(col) });
      child.append({ prop: "margin-right", value: half(col) });
    }
    rule.parent.insertAfter(parent, child);
  },
};
