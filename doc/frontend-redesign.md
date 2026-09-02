# TVGate 管理后台前端重写 · 设计文档

> 状态：**待评审**
> 日期：2026-08-31
> 适用范围：`web/` 目录下全部管理后台页面（28 个模板 + 5 个 JS + 3 个 CSS），不涉及代理/流媒体核心逻辑

---

## 0. 结论先行（TL;DR）

| 项 | 结论 |
|---|---|
| **推荐技术栈** | **Vue 3.5 + TypeScript + Vite 7 + Naive UI + Pinia + Vue Router 4**，构建产物 `go:embed` 进现有单二进制 |
| **迁移方式** | **渐进式（Strangler Fig）4 期迁移，不做一次性重写**；旧模板随 Phase 2 逐页删除，Phase 3 清理残余，无 `web.ui` 开关 |
| **最先做的一步** | **Phase 0：设计系统 + 布局壳（AppShell）**，把 28 个旧页面套上统一导航与主题 —— 成本最低、当天可见效 |
| **后端改造量** | 小。现有 ~60 个 JSON API 可直接复用，只需新增 `/web/api/v1/*` 命名空间与 SPA fallback 路由，业务逻辑不动 |
| **风险** | 中低。最大风险是**配置表单迁移**（node/proxygroups/publisher 三页合计 8000+ 行），对策是最后迁移 + 双轨并行 |
| **预估工期** | 4 期共 **18~25 人日**（含自测），每期均可独立上线 |

---

## 1. 现状诊断

### 1.1 技术现状

| 维度 | 现状 |
|---|---|
| 渲染方式 | Go `html/template` 服务端渲染，MPA，每次翻页整页刷新 |
| 模板 | `web/templates/*.html` 共 28 个，通过 `//go:embed templates/*` 编入二进制 |
| 静态资源 | `web/static/`（common.css 713 行 / mobile.css 561 行 / version.css 93 行 / js-yaml.min.js 39KB / CodeMirror 5 本地 vendor / theme.js / system-stats.js / version-upgrade.js），通过 `//go:embed all:static/*` 编入 |
| 路由 | `web/config_handler.go:RegisterRoutes()` 集中注册约 60 个路由（25 个页面路由 + 35 个 JSON API） |
| 鉴权 | Cookie 会话（`cookieAuth` 中间件 + `isAuthenticated`），非安全方法额外校验 Origin/Referer 防 CSRF |
| 配置保存 | 读 `config.yaml` 为 `yaml.Node` → 替换对应节点 → Marshal → 先写 `.backup.<时间戳>` 再写回（**这套逻辑非常稳，不要动**） |
| 实时数据 | 日志走 SSE（`web/api/logs/stream`）；系统状态走 `/web/api/v1/status` 轮询（替代原独立 `/status` 页的 system-stats.js） |
| 构建 | 纯 Go，无 Node 参与；二进制 ~19MB，跨平台 20 个目标（含 mips/mipsle/armv5/s390x/riscv64） |

### 1.2 「不好看」的量化证据

对 `web/templates` 全量扫描结果：

| 问题 | 数据 | 后果 |
|---|---|---|
| **无统一布局** | 28 个模板中**只有 3 个**（`index/node/editor`）引入 `sidebar.html`，25 个页面没有导航 | 页面之间互相"不像一个产品"，用户只能靠浏览器后退 |
| **CSS 全部内联且重复** | 27 个模板各自带 `<style>`，内联 CSS 合计 **约 5000 行**；`node_editor.html` 单页 635 行、`index.html` 503 行 | 改一个按钮圆角要改 27 个文件 |
| **硬编码颜色** | **191 处**十六进制色值（`node_editor` 47、`index` 23、`code` 22……） | 暗色主题下出现 `#eee` 边框、`#f9f9f9` 白底 tooltip，深浅主题撕裂 |
| **设计变量体系断裂** | `index.html` 用了 7 处 `var(--win11-text)`，但 `common.css` 只定义了 `--win11-text-primary/secondary/tertiary`，**`--win11-text` 根本不存在** | 标题/正文颜色回退到继承值，深浅主题下对比度失控 |
| **主题切换靠重排 hack** | `theme.js:29-33` 遍历 `document.querySelectorAll('*')` 逐个重置 `style.display` 强制重排 | `node_editor`（3195 行 DOM）切换主题时明显卡顿 |
| **固定像素布局** | `.clients-table th:nth-child(1){width:400px}`、`.storage-table td:nth-child(2){max-width:200px}` | 移动端横向溢出，只能靠 `mobile.css` 逐条打补丁，补丁越打越乱 |
| **交互原始** | 无组件库；模态框/Toast/确认框每个页面各写一套；表格无排序/分页/虚拟滚动 | 复杂编辑器（`proxygroups_editor` 1542 行、`jx_editor` 1308 行）维护成本极高 |
| **依赖老旧** | CodeMirror 5（非 6）、手抄的 `js-yaml.min.js` 39KB 未压缩直接 vendor | 无法按需加载，编辑器首屏就要下载全部 |

### 1.3 根因

> **不是"配色不好看"，而是 "没有设计系统 + 没有组件复用 + 没有构建流程" 这三件事同时缺失。**

只换配色会立刻回退到同样的混乱（因为没人能维护 27 份内联 CSS）。所以必须引入构建流程与组件化，否则重写没有意义。

---

## 2. 目标与非目标

### 目标

1. **视觉统一**：一套 design tokens 驱动全站，深浅主题零硬编码色值。
2. **组件化**：表格/表单/弹窗/Toast/空状态/骨架屏统一组件，杜绝每页重写。
3. **可维护**：单页代码量下降 70%+，`node_editor` 从 3195 行拆分至若干 `<200` 行组件。
4. **移动端可用**：响应式断点 + 抽屉式导航，手机/安卓 Termux 下完整可用（现有用户群含安卓部署）。
5. **单页体验**：SPA 路由，翻页不白屏；日志/监控沿用 SSE + 轮询但集中到一个 store。
6. **零运维变化**：仍是**单二进制**、`CGO_ENABLED=0`、**离线可用**（不依赖任何 CDN）、体积增幅可控。
7. **视觉重新设计（美观）**：全站统一样式、深浅主题、现代观感，对"不漂亮"的现状做整体视觉重设计；**全部页面/模块（含代理、推流、解析、PHP 代码等）都可采用新的组件库与布局**进行美观化重设计。
8. **展示用卡片形式 + 现有参数全部保留**：配置/编辑页的展示统一采用**卡片式布局**（统一 Card/区块卡片、卡片式列表/表格、统一 Tooltip/弹窗/Toast）；**重设计只改变视觉与排版，编辑表单的字段/配置项必须与现有实现保持一致，现有参数一个都不能少**（可优化分组/交互，但不得删减、改名任何配置项）。

### 非目标

- ❌ 不重写 Go 后端业务逻辑（代理/组播/推流/解析/同步/配置读写全部保持）
- ❌ 不引入外部 CDN 资源（设备常在家庭内网/无外网）
- ❌ 不做多语言（保持简体中文单语，避免 i18n 基建成本）
- ❌ 不做 SSO / OAuth / 多用户权限体系（沿用现有单用户 Cookie 鉴权）
- ❌ 不改 `config.yaml` 的 schema（配置保存走现有 yaml.Node 逻辑）

---

## 3. 硬约束（不可突破的技术边界）

| 约束 | 说明 | 对选型的影响 |
|---|---|---|
| **单二进制 + embed** | 现状 `//go:embed templates/*` 与 `all:static/*`；Makefile 20 个平台目标 | 前端产物必须先 `vite build` 成静态文件 → 输出到 `web/dist/` → Go embed。**构建链要加 Node 步骤，但只加在 CI 与发布流程，运行时零依赖** |
| **离线部署** | 常见部署环境无外网（软路由/家庭内网） | **禁止** Tailwind CDN、Google Fonts、unpkg 等运行时外链；字体用系统栈 |
| **低端设备** | 支持 mips/mipsle/armv5/s390x，内存可能仅 128MB | 必须 code-splitting + 路由懒加载；首屏 JS 目标 < 150KB gzip；禁止全量 ECharts |
| **Cookie 鉴权 + CSRF** | `cookieAuth` + SameSite=Strict + Origin/Referer 校验 | SPA 请求统一 `credentials: 'same-origin'`；**不能改成 Authorization Header 无 Cookie 方案**（会破坏现有 CSRF 纵深防御） |
| **Web 路径可配置** | `web.path` 默认 `/web/`，可改（如 `/admin/`） | 前端 base 必须运行时注入，不能写死；Go 侧渲染 `index.html` 时把 `webPath` 写进 `<script>window.__TVGATE_BASE__=...</script>` |
| **Go 1.25+ / 无 CGO** | `go.mod` 声明 go 1.25.6 | 前端选型与 Go 版本无关，但 `go:embed` 目录不能含 `.gitignore` 掉的空目录 |
| **体积预算** | 现二进制 ~19MB | 允许 +1.5~2MB（未压缩 dist），即 ~21MB，可接受 |
| **编辑风格红线** | **全站可重设计为卡片式美观布局；现有参数一个都不能少** | 视觉/布局/深浅主题做现代重设计，**全部页面/模块可用新的组件库与卡片式展示**；重设计只动视觉与排版——**编辑表单的字段/配置项与现有实现保持一致，不得删减、改名、改语义任何现有参数**，仅优化分组/交互/排版 |

---

## 4. 技术选型

### 4.1 候选方案对比

| 方案 | 复杂表单能力 | 暗色主题 | 产物体积(gzip) | 离线 | 与 embed 集成 | 迁移成本 | 综合 |
|---|---|---|---|---|---|---|---|
| **A. 现状修补**（Go template + 统一 CSS 变量） | ❌ 差 | ⚠️ 靠人肉 | 0（无新增） | ✅ | ✅ 无变化 | 低 | ⭐⭐ 治标不治本，3 个月后又乱 |
| **B. Go template + HTMX + Alpine** | ⚠️ 中 | ⚠️ 需自建 | ~40KB | ✅ | ✅ 无变化 | 中 | ⭐⭐⭐ 渐进友好，但复杂表单（动态增删行/嵌套代理组）仍会失控 |
| **C. Go + templ + Tailwind（本地编译）** | ⚠️ 中 | ⚠️ 需自建 | ~30KB | ✅ | ⚠️ 需引入 templ 代码生成到 Go 构建链 | 高 | ⭐⭐⭐ Go 味最浓，但组件库生态≈0，表格/表单要全手写 |
| **D. ✅ Vue3 + Naive UI** | ✅ 强 | ✅ 开箱 | 120~180KB | ✅ | ✅ 纯静态产物 | 中 | ⭐⭐⭐⭐⭐ **推荐** |
| **E. React 18 + Ant Design** | ✅ 强 | ⚠️ 一般（需大量 token 覆写） | 300~450KB | ✅ | ✅ 纯静态产物 | 中 | ⭐⭐⭐⭐ antd 暗色主题质量不如 Naive，体积翻倍，低端设备吃力 |
| **F. Svelte 5 + shadcn-svelte** | ✅ 强 | ✅ 好 | 60~100KB | ✅ | ✅ 纯静态产物 | 中 | ⭐⭐⭐⭐ 体积最优，但中文资料与组件完备度弱于 Naive，招人/维护成本高 |

**选择 D 的核心理由：**

1. **复杂表单是本项目的主体**：`node_editor`(3195)、`publisher_editor`(2274)、`proxygroups_editor`(1542)、`jx_editor`(1308)、`domainmap_editor`(1051) 合计近万行，全是"动态数组 + 嵌套对象 + 条件显隐"的表单。这类需求只有成熟组件库的 `Form/FormItem + 动态校验 + 表格内联编辑` 能扛住，方案 A/B/C 会重演今天的困境。
2. **Naive UI 是唯一"暗色主题一等公民"的中文生态组件库**：`darkTheme` + `themeOverrides` 直接对接 design tokens，不需要像 antd 那样覆写几百个 Less 变量。本项目后台默认深色，这点权重很高。
3. **体积可控**：Naive UI tree-shaking 良好，实测同类后台首屏 gzip 120~180KB，配合路由懒加载完全可在 mips 设备运行。
4. **Vue 3 单文件组件 + TS**：把 3195 行 HTML 拆成 20 个 `<200` 行组件，可读性收益最大；`defineModel`/`script setup` 对表单绑定极为友好。
5. **纯静态产物，与 Go embed 零摩擦**：`vite build` → `web/dist/` → `//go:embed all:dist`，不引入任何 Go 侧代码生成。

### 4.2 推荐技术栈清单

| 层 | 选型 | 版本 | 用途 | 备注 |
|---|---|---|---|---|
| 构建 | **Vite** | 7.x | dev server + 生产构建 | dev 用 `server.proxy` 代理到 Go，保持同源 Cookie |
| 语言 | **TypeScript** | 5.7+ | strict 模式 | API 类型从 Go 结构体半自动生成（见 §5.5） |
| 框架 | **Vue 3** | 3.5+ | `<script setup>` + Composition API | |
| 组件库 | **Naive UI** | 2.40+ | 表单/表格/弹窗/导航/反馈 | 暗色 `darkTheme` + 自定义 `themeOverrides` |
| 路由 | **Vue Router** | 4.x | history 模式，base 运行时注入 | 路由级懒加载 |
| 状态 | **Pinia** | 2.x | 系统状态/日志/配置草稿 | 替代现有散落的全局 var |
| 请求 | **自封装 fetch wrapper** | — | 统一 base、credentials、错误Toast、401 跳转 | ~80 行，零依赖（不引 axios） |
| 编辑器 | **CodeMirror 6** | 6.x | YAML / PHP / ini 语法高亮 + lint | 替换现有 CodeMirror 5；按需 import 语言包 |
| 图表 | **uPlot** 或自绘 SVG | uPlot 1.6 | 实时流量/CPU 曲线 | uPlot ~45KB min，比 ECharts(300KB+) 小一个数量级；环形进度条自绘 SVG（~30 行） |
| 图标 | **@vicons/lucide** | — | 按需引入 | tree-shaking，避免全量图标包 |
| 工具 | **@vueuse/core** | 12.x | `useLocalStorage`/`useIntervalFn`/`useDark` | 体积按函数摇树 |
| 日期 | **date-fns**（按需） | 4.x | 日志时间格式化 | 仅 `format`，避免 dayjs locale 冗余 |
| 代码规范 | ESLint + Prettier + vue-tsc | — | CI 卡口 | |

### 4.3 明确不用的东西

- ❌ **Tailwind CDN**（离线不可用；且本项目 90% 是组件库覆盖的场景，Tailwind 增量价值低）
  - *若团队强烈偏好，可本地编译 Tailwind v4 作为「布局/间距」补充，与 Naive UI 共存，但需配置 `prefix` 避免类名冲突 —— 列为待决事项*
- ❌ **ECharts 全量包**（300KB+，低端设备首屏灾难；实时曲线用 uPlot 足够）
- ❌ **Ant Design**（暗色主题需大量覆写，体积约为 Naive 的 2 倍）
- ❌ **任何外部 CDN / 在线字体**（离线部署是硬约束；字体用 `-apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif`）

---

## 5. 目标架构

### 5.1 目录结构（新增部分）

```
/opt/tvgate/
├── ui/                          # 【新增】前端工程（不参与 Go 编译）
│   ├── package.json
│   ├── vite.config.ts           # proxy → http://127.0.0.1:8080；outDir → ../web/dist
│   ├── tsconfig.json
│   ├── index.html               # 含 window.__TVGATE_BASE__ 占位，构建时被 Go 覆写
│   └── src/
│       ├── main.ts
│       ├── App.vue
│       ├── router/              # 路由表 + 守卫（401 → login）
│       ├── layouts/
│       │   ├── AppShell.vue     # 侧栏 + 顶栏 + 内容区 + 移动端抽屉
│       │   └── BlankLayout.vue  # login / 全屏日志
│       ├── styles/
│       │   ├── tokens.css       # ★ design tokens（唯一色彩真源）
│       │   └── global.css
│       ├── api/                 # 每个后端 API 一个模块 + TS 类型
│       │   ├── http.ts          # fetch wrapper（base/credentials/错误/401）
│       │   ├── system.ts        # /web/api/v1/status（系统状态聚合，替代独立 /status 页）
│       │   ├── config.ts        # config/* 各段
│       │   ├── code.ts  backup.ts  logs.ts  sync.ts  github.ts  dns.ts ...
│       ├── stores/              # system / logs / ui(主题) / configDraft
│       ├── components/
│       │   ├── common/          # PageHeader / Card / EmptyState / StatCard / RingGauge / ConfirmButton
│       │   ├── form/            # KeyValueEditor / ListEditor / DurationInput / YamlEditor
│       │   └── table/           # DataTable（排序/分页/空态/加载）
│       └── views/               # 按 §7 信息架构分组
│           ├── overview/  config/  content/  ops/  system/
│
├── web/
│   ├── dist/                    # 【新增】vite build 产物（gitignore？见 §5.2）
│   ├── spa.go                   # 【新增】SPA 路由注册 + index.html 注入 + fallback
│   ├── templates/               # 【保留】Phase 3 完成前继续存在
│   ├── static/                  # 【保留】v1 页面资源，Phase 3 后仅保留 codemirror
│   └── config_handler.go        # 【改造】RegisterRoutes 末尾调用 registerSPARoutes
```

### 5.2 构建与 embed 流程（**已落地** ✅）

**已拍板：`web/dist` 不进 Git，由 CI 生成；本地用 `make web-ui` 或手动 `npm run build`。**

> 用户决策原文：前端源码入口 `ui/`；编译产物由 Git/CI 生成；开发环境本地手动生成或写 Makefile 编译；其它依赖安装（用 `npm ci`）由工具链负责；旧前端不再保留（升级即纯 SPA）。

**当前实现**：

| 项 | 做法 |
|---|---|
| 源码入口 | `ui/`（Vite 7 + Vue 3 + TS），`vite.config.ts` 中 `base:'./'`、`build.outDir:'../web/dist'` |
| 产物 | `web/dist/`（index.html + assets/），由 `go:embed all:dist` 编入单二进制 |
| 进 Git 吗 | **不进**。`.gitignore` 忽略 `web/dist/assets/`、`*.js`、`*.css` 等；仅保留 `web/dist/index.html`（占位提示页）+ `.gitkeep`，使 `go:embed` 永远非空，`clone` 后可直接 `go build` |
| 本地构建 | `make web-ui`（缺 `node_modules` 时先 `npm install`，再 `npm run build`；产物不重建用 `web/dist/.built` 时间戳戳）／或 `make ui-install` 仅安装依赖 |
| CI | `release.yml` 加 `actions/setup-node@v4` + `make web-ui`；`docker.yml` 的 `Dockerfile` 增加 `node:20-alpine` 构建阶段，`COPY --from=ui /ui/dist /app/web/dist` 覆盖占位 |
| 纯 Go 逃生 | 无 Node 时 `make web-ui` 静默跳过（用占位页）；或 `make go-only` 仅编译 Go |

`Makefile` 现状（节选）：

```makefile
UI_DIR     := ui
DIST_STAMP := web/dist/.built
web-ui: $(DIST_STAMP)
$(DIST_STAMP): $(UI_DIR)/package-lock.json
	@command -v npm >/dev/null 2>&1 || { echo "⚠️ 未检测到 npm，跳过前端构建"; exit 0; }
	@test -d $(UI_DIR)/node_modules || (cd $(UI_DIR) && npm install)
	cd $(UI_DIR) && npm run build
	@touch $(DIST_STAMP)
all: web-ui $(OUT_DIR)/TVGate-linux-64 ...   # 各平台二进制依赖前端先构建
```

**Go 侧 embed**（`web/spa.go`，已落地）：

```go
//go:embed all:dist
var distFS embed.FS
```

> 验证：`go build ./...` 通过；产物字符串（如 `TVGate 管理后台`）已出现在二进制中。

### 5.3 路由与 Go 侧契约（**已落地** ✅）

现状 mux 是 `http.ServeMux`，注册了精确路径（`webPath+"node"`）与前缀路径（`webPath+"static/"`）。SPA 用 **hash 路由**（`createWebHashHistory`），因此**不需要服务端 history fallback**，只需服务 `/web/` 入口与 `/web/assets/*`。

**命名空间（最终）**：

```
/web/                       → SPA index.html（精确 /web/ + /web 兜底）
/web/assets/*               → 带 hash 的静态资源（Cache-Control: immutable）
/web/api/v1/**              → 【新】规范化 JSON API（前端只调这里）
/web/api/logs/stream        → SSE（沿用）
/web/login  /web/logout     → 认证（沿用）
/web/<旧页面精确路径>        → 【过渡期】旧模板 handler 仍保留，直到 Phase 3 删除
```

**实现**（`web/spa.go:registerSPARoutes(mux)`）：

1. `mux.Handle(webPath+"assets/", ...)` 子树前缀，资源 `Cache-Control: public, max-age=31536000, immutable`。
2. `mux.HandleFunc(webPath, serveSPA)` + `mux.HandleFunc(TrimSuffix(webPath,"/"), serveSPA)` 精确匹配 SPA 入口。
3. `serveSPA`：未启用 Web → 404；未认证 → 重定向 `/web/login`；否则返回 `dist/index.html`。
4. **不引入 `config.Web.UI` 开关**（用户已决定旧前端不保留）。旧的 `*_editor` handler 在过渡期仍注册，但**新导航（SPA 侧栏）不指向它们**；Phase 3 全量对齐后物理删除 `web/templates/*` 与对应 handler。

**base 推导**：前端用 `vite base:'./'`（相对路径），SPA 挂在任意 `web.path` 下均自洽；API 基址由前端运行时 `location.pathname` 推导（`src/api/http.ts`），**无需服务端注入变量**。

### 5.4 鉴权 / CSRF / 错误处理

| 项 | 方案 |
|---|---|
| 认证 | 完全沿用现有 Cookie 机制。SPA 所有请求 `credentials: 'same-origin'` |
| 401 处理 | fetch wrapper 拦截 401/302 → `router.push('/login?redirect=...')`；登录后跳回 |
| CSRF | 后端 `isSameOrigin` 校验 Origin/Referer。SPA 同源请求天然携带，**无需改动后端**；Vite dev 环境通过 `server.proxy` 保证同源（**不要用 CORS 跨域直连**，否则 Origin 校验会拒） |
| 错误展示 | 后端现状返回 `text/plain`（如 "配置保存成功"）或裸 JSON。wrapper 统一：`Content-Type: application/json` → 取 body；否则 toast 展示纯文本 |
| SSE | 沿用 `web/api/logs/stream`；SPA 用 `EventSource`（同源） |

### 5.5 API 契约规范化

**现状问题**：API 路径风格不统一，混用两种前缀：

- `<webPath>config/<段>` 与 `<webPath>config/save-<段>`（http/server/ts/multicast/web/reload/php/log/group/domainmap/proxygroups/publisher/global-auth/jx/server-monitor）
- `<webPath>api/<模块>/...`（sync、github、dns、code、backup、publisher/stats、logs/stream）

**迁移策略（低风险）**：不删旧路径，在 Go 侧**新增一组 `/web/api/v1/*` 别名指向同一 handler**，前端只调新路径。Phase 3 收尾时再决定是否下线旧路径（考虑第三方脚本兼容，建议长期保留别名）。

**建议的统一响应封装**（新增，仅用于 v1 前缀）：

```go
type APIResp struct {
    Code int         `json:"code"`           // 0 = 成功
    Msg  string      `json:"msg,omitempty"`  // 错误/成功文案
    Data interface{} `json:"data,omitempty"`
}
```

**新增 API（现状缺失、v2 需要）**：

| 新增接口 | 用途 | 后端数据来源 |
|---|---|---|
| `GET /web/api/v1/meta` | 一次返回 webPath / version / os / arch / uptime / hasDomainMap / hasProxyGroups / hasJX 等"侧栏与能力开关"信息 | 现存于 `config_handler.go:handleWeb` 的 data map |
| `GET /web/api/v1/status` | 系统状态聚合（CPU/内存/磁盘/运行时长/活跃连接/流量），**SPA 内唯一状态源** | 复用 `monitor.GlobalTrafficStats` / `monitor.ActiveClients`；原独立 `/status` 页（HTML + `?format=json`）**废弃**，数据并入此接口 |
| `GET /web/api/v1/config/raw` | 返回原始 YAML 文本（供 YAML 编辑器） | `os.ReadFile(*config.ConfigFilePath)` |
| `POST /web/api/v1/config/raw` | 保存整份 YAML（含校验 + 备份 + 热重载） | 复用 `handleConfigSave` 逻辑 |

> **TS 类型来源**：在 `ui/scripts/gen-types.mjs` 中解析 `config/*.go` 结构体生成 `src/api/types.d.ts`（或先手写，Phase 2 再自动化）。不引入 `go2ts` 依赖，避免构建链复杂化。

---

## 6. 设计系统（Design System）

### 6.1 Design Tokens（`ui/src/styles/tokens.css` —— 唯一色彩真源）

现状 `--win11-*` 变量体系断裂（`--win11-text` 未定义），新体系命名扁平化，且**同时给 CSS 与 Naive UI 使用**：

```css
:root {
  /* 语义色（light） */
  --bg-base:      #f5f6f8;   /* 页面底色 */
  --bg-surface:   #ffffff;   /* 卡片 */
  --bg-elevated:  #ffffff;   /* 弹层 */
  --bg-hover:     rgba(0,0,0,.04);
  --border:       #e3e5e8;
  --text-1:       #1f2329;   /* 主文本 */
  --text-2:       #646a73;   /* 次要 */
  --text-3:       #8f959e;   /* 占位/禁用 */
  --accent:       #2b6cb0;
  --success:      #18a058;
  --warning:      #f0a020;
  --danger:       #d03050;

  /* 尺寸 */
  --radius-sm: 4px; --radius: 8px; --radius-lg: 12px;
  --space-1: 4px; --space-2: 8px; --space-3: 12px; --space-4: 16px; --space-6: 24px;
  --header-h: 56px; --sidebar-w: 232px; --sidebar-w-collapsed: 64px;
  --font: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC",
          "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, "Cascadia Code", Consolas, monospace;
}

[data-theme="dark"] {
  --bg-base:     #14161a;
  --bg-surface:  #1c1f24;
  --bg-elevated: #23272e;
  --bg-hover:    rgba(255,255,255,.06);
  --border:      #2f343b;
  --text-1:      #e6e8eb;
  --text-2:      #a0a6ad;
  --text-3:      #6b7280;
  --accent:      #4a9eff;
  --success:     #36ad6a;
  --warning:     #f2c94c;
  --danger:      #e88080;
}
```

**Naive UI 对接**：`themeOverrides` 直接引用 CSS 变量：

```ts
const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: 'var(--accent)',
    bodyColor: 'var(--bg-base)',
    cardColor: 'var(--bg-surface)',
    textColorBase: 'var(--text-1)',
    borderRadius: 'var(--radius)',
    fontFamily: 'var(--font)',
  },
}
```

**铁律（写入 ESLint 规则 + CR checklist）**：
- 业务代码（`.vue` 的 `<style>`、`global.css`）**禁止出现十六进制色值**，一律 `var(--xxx)`。
- 主题切换只改 `<html data-theme>`，**绝不允许**遍历 DOM 强制重排（修掉 `theme.js` 的 `querySelectorAll('*')` 性能问题）。

### 6.2 关键组件规范

| 组件 | 规范 |
|---|---|
| `AppShell` | 左侧固定导航（可折叠/记忆状态）+ 顶栏（面包屑 / 主题切换 / 用户菜单 / 版本徽标）。`< 992px` 自动转抽屉（`n-drawer`），顶栏出现汉堡按钮 |
| `PageHeader` | 标题 + 描述 + 右侧操作区（保存/重置/帮助链接）。统一 24px 下边距 |
| `Card` | 复用 `n-card`，`size="small"`，统一 `--radius` 与 `--space-4` 内边距；标题 15px/600 |
| `DataTable` | `n-data-table` 封装：统一 `size="small"`、斑马纹关闭、hover 高亮、空状态 `EmptyState`、加载用 `n-spin` 包裹。**禁止固定像素列宽**，改用 `min-width` + `ellipsis` + `tooltip`（修掉现有 `400px` IP 列） |
| `FormPage` | 表单页统一"区块分组 + 右上保存条"，保存中 loading、成功后 `n-message` + 脏值提示；离开页面前拦截（未保存提醒） |
| `ListEditor` | 动态数组编辑（节点/代理组/订阅源通用）：支持增/删/复制/拖拽排序/上移下移。这是消灭 `node_editor` 3195 行的核心组件 |
| `KeyValueEditor` | 通用 KV（HTTP 头、参数映射） |
| `DurationInput` | `30s / 5m / 1h` 时长输入（后端用 `time.ParseDuration`，前端需校验格式） |
| `YamlEditor` | CodeMirror 6 封装，带 YAML lint（js-yaml 解析报错高亮） |
| `RingGauge` | 纯 SVG 环形进度（CPU/内存/磁盘），替代现有 4 段重复 SVG 代码 |
| `EmptyState / ErrorState` | 统一插画位（用内联 SVG，不外链）+ 文案 + 主操作 |

### 6.3 响应式断点

| 断点 | 布局 |
|---|---|
| `≥1440px` | 侧栏展开 232px，内容区最大宽 1440px 居中 |
| `992~1439px` | 侧栏展开 |
| `768~991px` | 侧栏自动折叠为图标态 |
| `<768px` | 侧栏转抽屉；表格转卡片流（`n-data-table` 的 `scroll-x` + 关键列优先）；表单单列；保存条吸底 |

---

## 7. 信息架构重构

现状 28 个页面平铺在侧栏（且 25 个页面根本没有侧栏）。重组成 5 组 + 折叠子菜单：

| 分组 | 页面（对应现有路由） | 优先级 |
|---|---|---|
| **概览** | 系统状态仪表盘（含实时监控图表、活跃连接，数据源 `/web/api/v1/status`）| P0 |
| **配置** | 节点配置（`node-editor`）、组配置（`group-editor`）、代理组（`proxygroups-editor`）、域名映射（`domainmap-editor`）、全局认证（`global-auth-editor`）、视频解析（`jx-editor`）、推流发布（`publisher-editor`）、组播（`multicast-editor`）、TS 缓存（`ts-editor`） | P1（工作量最大） |
| **服务** | 服务器（`server-editor`）、HTTP（`http-editor`）、DNS（`dns`）、PHP（`php-editor`）、重载（`reload-editor`）、Web 自身（`web-editor`） | P2 |
| **已废弃** | 独立 `/status` 监控页：`monitor.path` 配置项 + `server-monitor-editor` 配置页 + `system-stats.js` —— 状态进入 SPA，不再单独暴露 | — |
| **内容** | 代码文件管理（`code`）、仓库同步（`sync-editor`）、GitHub 升级（`github-editor`） | P2 |
| **运维** | 实时日志（`logs` + `log-editor`）、配置备份（`config/backup`）、备份中心（`backup`） | P1 |
| **工具** | YAML 编辑器（`editor`） | P2 |

> **展示原则**：全部页面/模块均可使用新的卡片式美观布局（统一 Card/区块卡片）；重设计只改动视觉与排版，**编辑表单的现有参数全部保留、一个不少**。  <!-- 决策 #12/#13 -->

**导航改造要点**：
- 侧栏支持二级菜单折叠 + 当前项高亮（现状完全没有"我在哪"的指示）
- 顶栏加全局搜索（`Ctrl+K`，按页面名/配置项跳转）—— 28 个页面必须可搜
- 每个配置页面右上角放"查看对应 YAML 片段"入口，与 YAML 编辑器打通

---

## 8. 迁移路线（4 期，每期可独立上线；旧前端在 Phase 3 物理删除，无回滚开关）

### Phase 0：工程 + 设计系统 + SPA 入口（**已落地** ✅，3~4 人日）

**目标**：搭好可开发的前端工程与构建链路，让 `/web/` 直接挂载新 SPA（hash 路由），旧模板暂时并存作为过渡。

1. ✅ 新建 `ui/` 工程（Vite 7 + Vue3 + TS + Naive UI），定义 §6 tokens。
2. ✅ Go 侧新增 `web/spa.go`（`registerSPARoutes`）：`/web/` 与 `/web/assets/*` 服务 SPA；旧 `/web/<页面>` 精确路由仍保留（过渡期）。
3. ✅ 产出 `AppShell`（侧栏/顶栏/主题切换/抽屉雏形）、`Dashboard` 占位页、`api/http.ts`、`stores/ui.ts`（主题同步后端 `sync-theme`）。
4. ⏳ 修掉 `theme.js` 的 `querySelectorAll('*')` 重排 hack（等旧前端下线时一并移除）。
5. ✅ `Makefile` 加 `web-ui` / `ui-install` / `go-only`；`Dockerfile` 加 Node 构建阶段；`release.yml` 加 Node 步骤；`.gitignore` 忽略产物。

> 现状：`/web/` 已返回 SPA 概览占位页；未认证访问会重定向登录。旧 28 个模板仍可通过各自 URL 访问，待 Phase 2 逐个被新页取代。

### Phase 1：只读页面迁移（4~6 人日）

迁入 SPA（**注意：不再单独设计 `/status` 前端**，状态统一在登录后的 SPA 内查看）：系统状态仪表盘（含实时监控图表 + 活跃连接）、实时日志（SSE）、备份列表、代码文件管理（只读部分）。

- 建立 `api/http.ts` wrapper、`stores/system.ts`（轮询 `/web/api/v1/status` + SSE 统一管理，替代原独立 `/status` 页的 system-stats.js）
- 建立 `DataTable / EmptyState / StatCard / RingGauge` 基础组件
- 新增 `GET /web/api/v1/meta` 与 `/web/api/v1/status`（**原 `/status` 独立页废弃，数据并入此接口**）

**验收**：SPA 内仪表盘/监控/日志/备份功能与旧版等价，且无需离开后台即可查看系统状态。

> **现状（2026-08-31，部分已落地 ✅）**：
> - 后端 `web/api_v1.go` 已新增 `GET /web/api/v1/meta`（侧栏/能力开关）与 `GET /web/api/v1/status`（CPU/内存/磁盘/负载/网络/运行时长/活跃连接/代理组，统一 `APIResp{code,msg,data}` 封装），经 `RegisterRoutes` 注册。
> - **旧无认证公开 `/status` 路由已在 `server/http.go` 移除**（决策 #11：状态全部内部化，不保留任何无认证端点）。`monitor.HandleMonitor` 不再被路由引用。
> - 前端已落地：概览仪表盘（`views/overview/Dashboard.vue`，RingGauge + StatCard + TrendChart + 分区/网卡/活跃连接/代理组表）、实时日志 SSE（`views/ops/Logs.vue`）、配置备份列表（`views/ops/Backup.vue`）、代码只读浏览（`views/content/Code.vue`）；基础组件 `RingGauge / StatCard / EmptyState / TrendChart` 就位；`api/http.ts` 增加 `api.v1()` 解包；`stores/system.ts` 轮询。
> - `ui/` 构建产物已生成至 `web/dist/`，`go build ./...` 通过，`vite build` 首屏 vendor 拆分（vue 41KB + naive 150KB gzip，仪表盘入口 chunk <7KB gzip）。

### Phase 2：配置表单迁移（**主体工作量**，8~12 人日）

按复杂度从低到高（每迁完一页，旧 handler 即可删除该路由）：

| 顺序 | 页面 | 行数 | 关键组件 |
|---|---|---|---|
| 1 | http / ts / reload / web / php / server-monitor | 各 200~650 | `FormPage` |
| 2 | multicast / group / server / dns / global-auth | 各 300~700 | `FormPage` + `ListEditor` |
| 3 | domainmap / jx | 1051 / 1308 | `ListEditor` + 条件显隐 |
| 4 | proxygroups | 1542 | 嵌套 `ListEditor` |
| 5 | publisher | 2274 | 嵌套 `ListEditor` + FFmpeg 状态轮询 |
| 6 | **node**（最后做） | 3195 | `ListEditor` + 批量操作 + 导入导出 |

**纪律**：迁移一页 → 删除对应旧 handler 与模板 → 跑一遍"改→存→重载→读回比对" → 才进入下一页。

> **现状（2026-08-31，Phase 2 起步 ✅）**：
> - 后端 `web/api_v1.go` 新增 `GET /web/api/v1/config/raw`（读整份 YAML）与 `POST /web/api/v1/config/raw`（保存）。保存**复用既有范式**：`yaml.Node` 语法校验 → 解析为 `config.Config` 结构校验 → `.backup.<时间戳>` 备份 → 写回（失败回滚）；文件写入由 `watch` 模块热重载。统一 `APIResp` 封装。
> - 前端新增 `views/config/Editor.vue`（整份 YAML 编辑器：分区跳转 chips、未保存标记、保存/重新加载、保存前校验），`api/config.ts` 增加 `getRawConfig/saveRawConfig`；`http.ts` 增加 `api.raw()`（纯文本 POST，不 JSON 化，避免 YAML 被转义）；`App.vue` 挂 `NMessageProvider/NDialogProvider`；侧栏「配置」分组接入 `/config`。
> - 该整份编辑器覆盖所有配置区（auth / code / server / node / proxy / domainmap / jx / publisher…），是结构化表单落地前最低风险、全覆盖的 Phase 2 起点（与文档 §5.3 规划的 `config/raw` 端点一致）。
> - **验证**：`go build ./...` + `vite build` 通过；运行时冒烟（admin/admin，:8888）：GET 200 返回 YAML；POST 非法 YAML 被拒（code:2，未写盘）；POST 合法内容 code:0 且生成 `.backup` 备份、触发热重载。
> - 下一步：按复杂度从低到高（code/auth → server/monitor → domainmap/jx → proxygroups → publisher → node）逐区迁移为**结构化表单**，每迁完一区删对应旧 handler/模板。

> **进度（2026-08-31，Phase 2 批 1 ✅）**：
> - 前端新增 `yaml`(eemeli) 依赖，用 `parseDocument` 操作 YAML AST：**结构化表单只改写目标区段，其余区段注释与结构完整保留**（已 Node 模拟验证：`# 监控配置` 等未改动注释仍在，编辑区 `server.port`/`php.index` 正确更新）。仍复用 `config/raw` 端点，无新后端。
> - 新增 `views/config/BasicForm.vue`（基础配置结构化表单：server + server.tls + monitor + web + php，NForm/NGrid/NSwitch/NInputNumber，含 `php.index` 多行文本、tls 仅在原配置存在时才写回）。`views/config/ConfigTabs.vue` 作为 `/config` 路由容器，含「原始 YAML」(嵌入 `Editor.vue`) 与「基础配置」两个 Tab；路由 `config` 指向 `ConfigTabs`。
> - 验证：`vue-tsc` + `vite build` 通过；Node 模拟 `yaml` 改写→字符串化→校验注释保留与字段更新均正确。`config/raw` 后端 YAML 解析/结构校验此前已运行时验证通过。
> - **取舍**：被改写的区段（server/monitor/web/php）自身行内注释会随重写出新而丢失，未改动区段注释保留——符合「结构化编辑」预期，后续如需逐字段保注释可改为节点级编辑（成本更高）。
> - 下一步：批 2 —— 把 `auth`(global_auth) / `code`(CodeFiles) 等迁为结构化表单（ListEditor），并逐区删除对应旧 handler/模板。

> **进度（2026-08-31，Phase 2 批 2 ✅）**：
> - 复用 `useConfigDoc` composable（加载/解析/局部区段写回，仍走 `config/raw`）。新增 `views/config/AuthForm.vue`（`global_auth`：tokens_enabled / token_param_name / 动态 token 子对象 / 静态 token 子对象）、`views/config/NetworkForm.vue`（`dns`：servers 列表 + timeout + max_conns；`multicast`：multicast_ifaces 列表 + fcc_* + upstream_*；`ts`：enable + cache_size + cache_ttl）。字符串列表用 `NDynamicInput`（ListEditor 基元）。
> - `ConfigTabs.vue` 增加「鉴权」「网络」两个 Tab。
> - 验证：`vue-tsc` + `vite build` 通过；Node 模拟 `global_auth/dns/multicast/ts` 写回→重解析：`tokens_enabled:true`、`dns.servers:["223.5.5.5","8.8.8.8"]`、`ts.cache_ttl` 为字符串 `"2m"`（Go 可解析为 Duration）、`multicast.ifaces:["eth0"]` 均正确，且未改动区段（如 `# 监控配置`）注释保留。
> - 注：`code` 编辑器（`web/handlecode.go`）是**文件型**（按目录读写脚本文件），非 config.yaml 区段，不适合 YAML 结构化表单，已从批 2 移除，归到文件型编辑（需独立端点，后续另行处理）。
> - 旧 `*_editor` handler/模板**暂未删除**（严格纪律应在每页迁完后删除，但缺浏览器冒烟测试环境；待 SPA 表单经浏览器实测后再统一删除，见 Phase 3 收尾）。
> - 下一步批 3：把 `domainmap` / `jx` 迁为结构化表单（嵌套 ListEditor + 条件显隐）。

> **进度（2026-08-31，Phase 2 批 3 ✅）**：
> - 新增 `ui/src/components/KeyValueEditor.vue`（可复用键值对编辑器：NDynamicInput + 双 NInput，用于 `client_headers`/`server_headers`/`filters` 的 map ↔ `[{key,value}]` 互转）。
> - 新增 `views/config/DomainMapForm.vue`（`domainmap` 列表：`NDynamicInput` 每项一张卡；含 name/source/target/protocol 选择器、嵌套 `auth`（动态/静态 token 子对象，**条件显隐**：仅 `tokens_enabled` 时展开）、`client_headers`/`server_headers` 用 KeyValueEditor）。
> - 新增 `views/config/JXForm.vue`（`jx`：path/default_id + `api_groups` 映射以「组名(key) + 嵌套字段」列表编辑；endpoints 子列表、filters 用 KeyValueEditor；primary/fallback 开关、weight/max_retries 数值）。
> - `ConfigTabs.vue` 增加「域名映射」「视频解析」两个 Tab（现共 6 个）。
> - 验证：`vue-tsc` + `vite build` 通过；Node 模拟 `domainmap`/`jx` 写回→重解析：`client_headers.X=1`、`auth.static_tokens.token=abc`、`api_groups.g1.endpoints=["e1"]`、`filters.k=v`、`primary=true` 均正确，未改动区段注释保留。
> - 坑：`.vue` 不在 Vite/TS 别名解析扩展列表，`@/components/X` 扩展名省略会报模块找不到，已改为显式 `@/components/KeyValueEditor.vue` 导入。
> - 下一步批 4：把 `proxygroups` 迁为嵌套 ListEditor（map[name]→proxies 列表 + domains + 负载/重试等）。

> **进度（2026-08-31，Phase 2 批 4 ✅）**：
> - 新增 `views/config/ProxyGroupsForm.vue`（`proxygroups`：`map[name]→` 代理组，每组含 `proxies` 嵌套列表（每项 name/type 选择器/server/port/udp/username/password/headers 用 KeyValueEditor）、`domains` 字符串列表、ipv6 开关、interval/loadbalance/retry_delay/max_rt（Duration 字符串）、max_retries 数值）。`ConfigTabs.vue` 增加「代理组」Tab（现共 7 个）。
> - 验证：`vue-tsc` + `vite build` 通过；Node 模拟 `proxygroups` 写回→重解析：proxies 嵌套列表（含 udp=true、headers.X=1）、domains、ipv6/max_retries/interval/max_rt 均正确，未改动区段注释保留。
> - 注：`NDynamicInput` 默认 slot 作用域变量为 `value`/`index`（非自定义名），内层嵌套时用了 `#="{ value: p, index: pi }"` 重命名以区分外层。
> - 下一步批 5：把 `publisher` 迁为嵌套 ListEditor + FFmpeg 状态轮询（StreamData/ReceiverItem/FFmpegOptions 较深嵌套）。

> **进度（2026-08-31，Phase 2 批 5 ✅）**：
> - `useConfigDoc` 增加 `stripEmpty`：写回前深层剔除 `null/undefined/空字符串/空数组/空对象`，避免向 YAML 写入会让 Go 反序列化失败（如 `crf: null` 对 int 字段）或产生噪声的空值；空字符串/空数组按"默认即空"安全丢弃，但保留 `false`/`0` 等显式值。
> - 新增 `ui/src/components/FFmpegOptionsEditor.vue`（可复用 FFmpegOptions 编辑器：编码/码率/CRF/GOP/pix_fmt + stream_copy/use_re_flag 开关 + 6 类 args 列表 + filters 视频/音频滤镜列表），`defineModel` 共享同一响应式对象，父级原地变异即生效。
> - 新增 `views/config/PublisherForm.vue`（`publisher`：path + `streams` 内联 map；每条流含 buffer_size/protocol/enabled/streamkey + `stream`(source 含 FFmpegOptions + local_play_urls 嵌套列表（每项含 flv/hls 两个 FFmpegOptions + hls_*）+ mode + receivers(primary/backup 可选卡片 + all 列表，每个 ReceiverItem 含 FFmpegOptions)）。`ConfigTabs.vue` 增加「推流」Tab（现共 8 个）。
> - 验证：`vue-tsc` + `vite build` 通过；Node 模拟 publisher 写回→`stripEmpty`→重解析：有意义值（`video_codec: libx264`、`protocol: rtmp`）保留，空 `streamkey`/空 `source.type`/整段空 `receivers`/空 `flv_ffmpeg_options` 均被剔除，未改动区段注释保留。
> - 注意：`publisher.Streams` 是 `yaml:",inline"`，流名作为 publisher 映射的直接 key（非 `streams:` 子键）；表单按 `pub[name]=streamObj` 写回。
> - 下一步批 6（最后、最大）：把 `node` 迁为嵌套 ListEditor + 批量操作 + 导入导出。

> **进度（2026-08-31，Phase 2 批 6 ✅）**：
> - **重要修正**：经核对 `web/config_handler.go` 与全仓 Go 代码，`node` **没有**固定的 `NodeItemConfig` 结构体（早期文档的 `NodeConfig{Nodes map[string]*NodeItemConfig}` 设想不成立）。`node` 实为**通用顶层配置节点编辑器**（旧版 `web/templates/node_editor.html` 可编辑 config.yaml 任意顶层 key，后端 `handleNodeConfig`/`handleConfigSaveNode` 按 `node` 查询参数对顶层 key 做节点级增改删）。因此批 6 落地为「配置节点管理器」而非虚构字段表单。
> - 新增 `views/config/NodeForm.vue`：列出全部顶层区段；每个区段内联 YAML 编辑（`NInput` textarea），支持「应用本节点」(doc.set) → 「保存全部」(saveRawConfig)；支持重命名(delete+set)、新增、删除；批量勾选 → 批量删除；导入（粘贴 YAML 映射，逐 key set）/ 导出选中（复制到剪贴板）。复用 `api/config.ts` 的 `getRawConfig`/`saveRawConfig` + `yaml` 包。
> - `ConfigTabs.vue` 增加「配置节点」Tab（现共 9 个）。
> - 验证：`vue-tsc` + `vite build` 通过；Node 模拟：列顶层 key → 编辑 `web`(port→9999) → 重命名 → `saveRawConfig(doc.toString())`：改后字段生效、未改动区段（如 `# 监控配置`）注释保留、`server.port` 保留。
> - **Phase 2 全部结构化标签页已完成**（基础/鉴权/网络/域名映射/视频解析/代理组/推流/配置节点 + 原始 YAML）。下一步进入 Phase 3：逐页删除对应的旧 `*_editor` handler 与 `web/templates/*`、清理 vendor、更新 README（删除前建议先做一次浏览器冒烟实测）。

> **进度（2026-08-31，Phase 3 下线旧编辑器 ✅ 主体）**：
> - 调研结论：SPA（`web/spa.go` embed `ui/dist`）已挂载于 `/web/`，为当前主页；旧 `web/templates/index.html` 仪表盘已不再被路由（orphaned）。旧 `config_handler.go` + 各 `handle*.go` 同时服务「已被 SPA 取代的配置区段编辑器」与「SPA 尚未覆盖的功能」（code 文件管理、backup 备份、github、sync、logs）。
> - 删除已被 SPA 取代的 **19 个旧编辑器模板**：editor/node/node_editor/group_editor/domainmap_editor/proxygroups_editor/global_auth_editor/jx_editor/publisher_editor/server_editor/server_monitor_editor/multicast_editor/ts_editor/web_editor/reload_editor/http_editor/php_editor/dns_editor/log_editor。
> - 移除 `config_handler.go` 中对应的 **配置区段路由注册**（editor/node/config/*/*-editor/dns 等），保留 logs/github/sync/code/backup 相关路由。
> - `go build ./...` 通过（无悬空引用）。
> - README 新增「Web 管理界面（SPA）」一节，标注保留的独立页面。
> - **保留项（SPA 暂未覆盖，不可删）**：`/web/code`、`/web/config/backup`、GitHub（`/web/github` + `/api/github/*`）、Sync（`/web/sync-editor` + `/api/sync/*`）、实时日志（`/web/logs` + `/api/logs/stream`）、登录/auth。
> - **遗留（建议冒烟后处理）**：被移除路由的 handler 函数（handleEditor/handleNode/handleConfig/handle*Editor/handle*Config 等）现为孤儿代码仍可编译，下一步可在确认无跨功能调用后删除；codemirror vendor 仍被保留的 code 编辑器使用，暂不清理。
> - **下一步**：浏览器冒烟实测（重点：SPA 各标签页改→存→热重载、保留页面 code/backup/github/sync/logs 正常）；确认后删除孤儿 handler 函数并视情况清理 vendor。

> **进度（2026-08-31，Phase 3 全量迁移 — github/sync/logs/仪表盘 已迁）**：
> - 重要发现：SPA 远不止 Config 标签页——`router/index.ts` 早已定义 Dashboard/Logs/Backup/Code/Config/Login 视图；`ui/src/views/*` 下 Dashboard(完整)、Logs(完整 SSE)、Backup(只读列表)、Code(只读预览)、Login(占位) 均已存在并编译通过。真正缺的是 **github / sync** 两个视图。
> - 新增 `api/github.ts`、`api/sync.ts`（契约来自 `handlegithub.go`/`handlesync.go` 的 JSON 端点）。
> - 新增 `views/ops/Github.vue`（enabled/url/backup_urls/timeout/retry）、`views/ops/Sync.vue`（NDynamicInput 仓库列表，含获取分支）；接入 router + AppShell 导航。
> - 删除旧 github/sync 页面：handler `handleGithubEditor`/`handleSyncEditor` + 模板 `github_editor.html`/`sync_editor.html` + 路由 `/web/github-editor`、`/web/github`、`/web/sync-editor`；保留 JSON API（`/api/github/*`、`/api/sync/*`）。
> - 删除旧「实时日志」页面（SPA `/logs` 已覆盖）：`handleLogViewer` + `log_viewer.html` + `/web/logs` 路由；保留 SSE `/api/logs/stream`。
> - 删除旧仪表盘（`handleHome`/`handleNode` 已不在路由，SPA Dashboard 覆盖）：`index.html`/`sidebar.html` 模板删除（orphaned Go handler 仍引用，运行时无害，待清理）。
> - `go build ./...` + `vue-tsc` + `vite build` 均通过。
> - **仍未全迁的旧页面（SPA 对应视图尚不完整，直接删会丢功能）**：
>   - `/web/code` + `handleCodeEditor` + `code.html`：SPA `Code.vue` 目前只读预览，缺编辑/保存/上传/校验。
>   - `/web/backup`、`/web/config/backup` + `handleBackupPage`/`handleConfigBackupPage` + `backup.html`/`config_backup.html`：SPA `Backup.vue` 只读列表，缺恢复/删除/下载/清理。
>   - `/web/login` + `handleLogin`(GET 页)：SPA `Login.vue` 仅为占位，未对接 `/web/login` POST。
> - **下一步**：补全 SPA `Code.vue`(CRUD)、`Backup.vue`(恢复/删除/下载/清理)、`Login.vue`(对接登录) 后，再删对应旧页面 handler+模板；随后清理孤儿 handler 函数 + codemirror vendor + 更新 README。

### Phase 3：收尾与下线（2~3 人日）

- 删除全部剩余 `web/templates/*`（除被新前端复用的资源）与 `web/static/common.css / mobile.css / version.css / theme.js / system-stats.js / version-upgrade.js`
- 旧 `*_editor` handler 一并移除（已随 Phase 2 逐页删除，此处清理残余）
- `static/js/codemirror`：若 Phase 2 已迁到 CodeMirror 6（npm 包）则删除 vendor
- 更新 README、CONTRIBUTING（构建前端说明）

---

## 9. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| **构建链引入 Node，破坏"纯 Go 单命令构建"体验** | 中 | `Makefile` 的 `web-ui` 在无 npm 时**静默跳过**并使用占位页（不会让 `go build` 失败）；`make all` 显式依赖 `web-ui`；CI 加 Node 步骤 |
| **`web/dist` 缺失导致 `go:embed` 编译失败** | 高（会阻塞所有 Go 开发） | 仓库内提交占位 `web/dist/index.html` + `.gitkeep`，保证 clone 后可直接 `go build`；写进 CONTRIBUTING |
| **配置表单迁移引入回归，用户配置被写坏** | **极高** | ① 后端保存逻辑**完全不动**（已有 `.backup.<时间戳>` 双保险）；② 前端保存前先调 `config/validate`；③ 每迁一页即删对应旧 handler，删前可临时保留应急；④ 每个表单页迁移后跑一遍"改→存→重载→读回比对" |
| **低端设备（mips/armv5）SPA 首屏慢** | 中 | 路由懒加载 + Naive UI **按需引入**（unplugin-vue-components 自动注册，避免全量打包）；首屏目标 < 150KB gzip；为超大表单页单独 chunk |
| **Vite dev proxy 下 Cookie/CSRF 问题** | 低 | 必须走 `server.proxy` 同源代理；**禁止**用 CORS 跨域直连（会被 `isSameOrigin` 拒） |
| **成员不熟悉 Vue 3 / TS** | 低~中 | Naive UI 中文文档完善；先做 Phase 0/1 建立范式，再铺开 |
| **工期失控（8000+ 行表单）** | 中 | 严格按 §8 Phase 2 的复杂度顺序，且**每页独立可回滚**；若时间不足，可停在"非核心配置页仍在 v1"的混合态长期运行 |
| **二进制体积增长** | 低 | 预算 +1.5~2MB（19MB → ~21MB），可接受；构建后 CI 输出体积对比 |

---

## 10. 工作量估算与里程碑

| 阶段 | 内容 | 人日 | 累计 | 可交付状态 |
|---|---|---|---|---|
| Phase 0 | 工程搭建 + 设计系统 + SPA 入口 + 构建链路 | 3~4 | 4 | **`/web/` 已挂新 SPA 概览页，旧站并存过渡**（已落地） |
| Phase 1 | 只读页 + API 规范化 + 基础组件 | 4~6 | 10 | SPA 下仪表盘/日志/备份可用 |
| Phase 2 | 配置表单迁移（6 批，逐页删旧） | 8~12 | 22 | SPA 功能对齐旧版 |
| Phase 3 | 收尾、物理删除旧前端、文档 | 2~3 | 25 | 完成 |

> Phase 0（3~4 人日）已落地：现在 `/web/` 就能看到新版概览占位，开发期零业务风险。

---

## 11. 验收标准

- [ ] 全站**零硬编码色值**（ESLint/CI 卡口：`.vue` 与业务 CSS 中 `/#[0-9a-fA-F]{3,8}/` 命中数为 0）
- [ ] 深浅主题切换**无闪烁、无 DOM 全量重排**，`node-editor` 级别大页切换 < 16ms
- [ ] 全部 28 个页面具备统一侧栏导航 + 当前位置高亮
- [ ] 375px 宽度下无横向滚动；表格不溢出；侧栏转抽屉
- [ ] 首屏 JS < 150KB gzip（仪表盘路由）
- [ ] 全站请求统一携带 `credentials: 'same-origin'`，后端 CSRF 校验 100% 通过
- [ ] `CGO_ENABLED=0 make linux-64` 产物仍为单文件静态二进制，`web/dist` 由 `make web-ui`/CI 注入，体积增幅 < 2MB
- [ ] 旧 `web/templates/*` 与 `web/static/*` 已物理删除，仅保留 SPA 所需资源
- [ ] 配置保存回归：任一表单"修改→保存→重载→读回"字段完全一致，且生成 `.backup.*` 文件

---

## 12. 已拍板决策（用户确认）

| # | 决策项 | 结论 |
|---|---|---|
| 1 | 技术栈 | **Vue 3.5 + TS + Vite 7 + Naive UI + Pinia + Vue Router 4**（hash 路由） |
| 2 | Tailwind | **不引入**（Naive UI + 少量 CSS 变量即可） |
| 3 | 产物进 Git | **不进**；`web/dist` 由 CI（`make web-ui`）生成，仓库仅占位 `index.html` + `.gitkeep` |
| 4 | 图表 | **uPlot**（实时曲线）+ **自绘 SVG**（环形仪表）；不用 ECharts 全量 |
| 5 | 移动端优先级 | **高**（安卓 Termux / 手机浏览器完整可用） |
| 6 | 起始范围 | **Phase 0 已落地**（工程 + SPA 入口 + 构建链路），后续按 Phase 1→3 推进 |
| 7 | CodeMirror | 旧版 5 在 Phase 2 迁到 **CodeMirror 6**（npm 包） |
| 8 | 旧前端去留 | **不保留** `web.ui` 开关；升级即纯 SPA。旧 `web/templates`/`web/static` 在 Phase 3 物理删除（过渡期并存，逐页替换） |
| 9 | 依赖安装 | `npm ci`（CI 复现）/ `make ui-install`（本地）；Node ≥ 20 |
| 10 | 构建触发 | `make all` 依赖 `web-ui`；CI `release.yml`+`Dockerfile` 已加 Node 步骤 |
| 11 | 独立 `/status` 监控页 | **废弃**：状态数据统一走登录后的 SPA（`/web/api/v1/status`，需认证）；`monitor.path` 配置项随之弃用，`server-monitor-editor` 配置页与 `system-stats.js` 在 Phase 3 删除；不单独设计状态前端，**不对外部暴露任何无认证只读/探活端点（如 `/api/healthz`），状态全部内部化** |
| 12 | 前端风格 | **全站视觉重新设计做美观，全部页面/模块都可用新的组件库与布局**；重设计只改变视觉与排版 |
| 13 | 组件与展示 | **展示统一用卡片形式**（统一 Card/区块卡片、卡片式列表/表格、统一弹窗与 Toast），不用像素固定布局；**现有参数全部保留**——编辑表单的字段/配置项与现有实现一致，不得删减/改名/改语义任何现有参数 |

---

## 附：现状关键文件索引

| 文件 | 行数 | 作用 |
|---|---|---|
| `web/config_handler.go` | 1897 | 路由注册（L181-319）、`renderTemplate`(L160)、`cookieAuth`(L102)、CSRF(L120-157)、首页/功能面板数据装配(L322-459, L496+) |
| `web/handlecode.go` | 976 | 代码文件管理器（含 symlink 穿越防护） |
| `web/templates/node_editor.html` | 3195 | 节点配置（最大页面） |
| `web/templates/publisher_editor.html` | 2274 | 推流发布配置 |
| `web/templates/proxygroups_editor.html` | 1542 | 代理组配置 |
| `web/templates/jx_editor.html` | 1308 | 视频解析配置 |
| `web/templates/domainmap_editor.html` | 1051 | 域名映射配置 |
| `web/templates/code.html` | 1323 | 代码文件管理前端 |
| `web/static/common.css` | 713 | Win11 风格变量与基础样式（变量体系断裂处） |
| `web/static/mobile.css` | 561 | 移动端补丁 |
| `web/static/js/theme.js` | 55 | 主题切换（含全量重排 hack，L29-33） |
| `web/static/js/system-stats.js` | 320 | 系统状态轮询与渲染 |
| `web/log_stream.go` | 212 | 日志 SSE |
| `config/config.go` | — | `Web` 配置结构（L80-85，需新增 `ui` 字段） |
| `Makefile` / `Dockerfile` | — | 需增加 Node 构建步骤 |
