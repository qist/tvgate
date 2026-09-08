// 轻量 gap → margin 兜底（兼容 Android 8 WebView / Chrome 62-68）。
// 背景：Tailwind 把 `gap-*` 拆成独立工具类（`.gap-4 { gap: 1rem }`），与 `display:flex`
// 不在同一规则里，通用 polyfill（postcss-gap-properties）因无法判断容器类型而不生效；
// 而旧 WebView 的 flex gap 是 Chrome 84+ 才支持 → 直接忽略 → 控件重叠。
// 这里把每条含 gap/row-gap/column-gap/gap-x/gap-y 的规则改写成：
//   父容器负半边距 + 直接子元素半边距，等价于 gap，且对所有浏览器都生效。
// 用 margin 长写法（margin-top/left/right/bottom）避免 gap-x + gap-y 组合时相互覆盖。

const GAP_PROPS = new Set(["gap", "row-gap", "column-gap", "gap-x", "gap-y"]);

const half = (value) => `calc(${value.trim()} / 2)`;

export default {
  postcssPlugin: "postcss-gap-polyfill",
  Rule(rule) {
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
      d.remove();
    }
    if (row == null && col == null) return;
    row = row || "0";
    col = col || "0";

    // 父容器：负半边距抵消子元素半边距，避免边缘额外溢出
    if (row !== "0") {
      rule.prepend({ prop: "margin-top", value: `-${half(row)}` });
      rule.prepend({ prop: "margin-bottom", value: `-${half(row)}` });
    }
    if (col !== "0") {
      rule.prepend({ prop: "margin-left", value: `-${half(col)}` });
      rule.prepend({ prop: "margin-right", value: `-${half(col)}` });
    }

    // 子元素：半边距，等价于 gap
    const child = rule.clone();
    child.selector = `${rule.selector} > *`;
    child.nodes = [];
    if (row !== "0") {
      child.append({ prop: "margin-top", value: half(row) });
      child.append({ prop: "margin-bottom", value: half(row) });
    }
    if (col !== "0") {
      child.append({ prop: "margin-left", value: half(col) });
      child.append({ prop: "margin-right", value: half(col) });
    }
    rule.parent.insertAfter(rule, child);
  },
};
