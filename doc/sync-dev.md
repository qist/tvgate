# TVGate 仓库同步模块 — 开发文档

> 目标：让 TVGate（Android / Windows / Linux，均无 `.git`）从**私有 GitHub / GitLab 仓库**定时拉取**整个 `www/tvbox` 目录**（TVBox 配置 / 直播源 / 爬虫插件等混合内容），一处维护、多端同步。
>
> 已确定的关键决策：
> - **部署形态**：内嵌进每个 TVGate（各自直连 GitHub/GitLab，无中央节点）
> - **触发方式**：定时轮询（无需公网回调）
> - **同步范围**：`www/tvbox` 整个目录（json / txt / m3u / jar / py / js / php 混合，**不限于 PHP**；不碰 config.yaml 等本地配置）

---

## 1. 需求与目标

| 项 | 说明 |
|---|---|
| 源 | 私有 GitHub / GitLab 仓库（内容即 tvbox 目录的源） |
| 目标 | 各端 TVGate 的 docroot 子目录 `tvbox`（即 `local_path: tvbox`） |
| 同步方向 | 单向：仓库 → 本地（本地 Web 编辑器改动不反向推送） |
| 数据量 | 混合内容，含 6MB 级 m3u / jar 二进制，必须增量 |
| 无依赖 | 设备端不依赖 git 二进制（用 Go HTTP 直连 API） |

**实际部署目标（安卓 `www/tvbox` 现状）**：
- TVBox 订阅配置 `.json`（0707/0821/367/9918…）、直播源 `.txt`/`.m3u`（listx.m3u 约 6MB）、爬虫插件 `jar/`（spider.jar 等二进制）、`py/`、`js/`、图标 `favicon.ico`、README、`.gitignore` 等。
- 全部为需要同步的内容，**默认全量同步**（`only_php: false`）。

**非目标**：
- 不反向同步（本地 → 仓库）
- 不同步 `config.yaml` 等本地配置
- 不做版本历史/分支管理（只跟单一分支）

---

## 2. 总体架构

```
GitHub / GitLab（私有 repo，www/ 脚本）
   │  PAT（只读，存在各端 config.yaml）
   ▼
TVGate sync 模块（内嵌，每端一个）
   ├─ 定时轮询（time.Ticker，间隔可配）
   ├─ GitHub/GitLab API：拉目录树 → 对比 manifest → 只下载变更
   ├─ 校验：simplePHPCheck 语法检测
   ├─ 备份：覆盖/删除前 .bak.<时间戳>
   └─ 应用：原子 rename，失败回滚
   ▼
docroot（www/ 目录）→ PHP 模块正常加载
```

**为什么不直接用 git clone**：设备端无 `.git`、无 git 二进制；Go HTTP 直连 API 即可拿到目录树与文件内容，天然支持增量（树里的 blob sha 就是对比依据）。

---

## 3. 配置设计

### 3.1 config.yaml 新增段

```yaml
# 仓库同步（支持多仓库，每个条目独立同步到各自 local_path；见 §6.4/§8）
sync:
  - name: tvbox               # 标识（用于日志区分多仓库，可空）
    enabled: false            # 是否启用
    type: github              # github | gitlab
    repo: owner/repo          # 仓库标识（GitLab 可为 group/project）
    branch: main              # 同步分支
    token: ""                 # PAT（GitHub: ghp_xxx；GitLab: glpat_xxx）。可留空仅用于公开仓库
    interval: 60s             # 轮询间隔（最小 10s）
    repo_path: .              # 仓库内源子目录（"." = 仓库根；tvbox 内容在仓库根时用 ".")
    local_path: tvbox         # 本地目标：以 php docroot 为锚点；"." = docroot 根，"tvbox" = docroot/tvbox
    only_php: false           # 是否只同步 .php/.phtml/.php3/.php4/.inc（tvbox 是混合内容，默认 false 全量）
    backup: true              # 覆盖/删除前备份为 .bak.<时间戳>
    delete: false             # 远端已删除的文件，本地是否也删除（false 则保留）
    protect: []               # 本地保护清单（相对 local_path，支持目录前缀）：永不覆盖、永不删除（见 §6.4 孤立文件处理）
    timeout: 15s              # 单次 API/下载请求超时
  # - name: php                # 可继续添加更多仓库条目，每个条目独立同步循环
  #   enabled: false
  #   type: github
  #   repo: owner/php-scripts
  #   local_path: www/scripts
  #   ...
```

> **实际部署示例（对应安卓 `www/tvbox`）**：仓库根存 tvbox 全部内容（json/txt/m3u/jar/py/js…），则 `repo_path: .`、`local_path: tvbox`，同步后即得到 `docroot/tvbox/*`。

> **local_path 锚点规则（重要）**：
> - 本地目标**必须以 php docroot 为锚点**解析：实际目录 = `filepath.Join(resolvedDocRoot, local_path)`。
> - 允许：`"."`（= docroot 根目录）、子目录如 `"sub"` / `"a/b"`（= docroot/sub、docroot/a/b）。
> - 不允许：绝对路径、含 `..` 的路径（防穿越）。
> - `local_path` 为空时等价于 `"."`（同步到 docroot 根）。
> - 仓库内 `repo_path` 子树 → 映射到该本地目录（`repo_path` 下的相对路径原样落到 local_path 下）。

> **GitHub 加速配置（沿用现有 `github` 段）**：
> - 同步模块**复用项目已有的 `github` 加速配置**，不新增加速字段。
> - 当 `github.enabled=true` 且配置了 `url` / `backup_urls` 时，GitHub API 请求走加速地址：`buildURL(加速地址, https://api.github.com/...)`（与 `updater/github_updater.go` 的 `buildURL` 一致），主地址失败依次尝试备用，最后兜底官方地址。
> - 注意：私有仓库带 PAT 时，加速代理需能透传 `Authorization` 头；若加速地址不通/不透传，会自动回落到官方 `api.github.com`。
> - `github.timeout` 可复用为同步 GitHub 请求的超时参考，`sync.timeout` 优先。

### 3.2 Go 结构体（`config/config.go` 新增）

```go
// SyncConfig 仓库同步配置（单个仓库条目；Config.Sync 为 []SyncConfig 支持多仓库）
type SyncConfig struct {
	Name      string        `yaml:"name"`       // 标识（用于日志区分多仓库，可空）
	Enabled   bool          `yaml:"enabled"`
	Type      string        `yaml:"type"`      // github | gitlab
	Repo      string        `yaml:"repo"`      // owner/repo
	Branch    string        `yaml:"branch"`
	Token     string        `yaml:"token"`
	Interval  time.Duration `yaml:"interval"`
	RepoPath  string        `yaml:"repo_path"`  // 仓库内源子目录
	LocalPath string        `yaml:"local_path"` // 本地目标，以 php docroot 为锚点；"." = docroot 根
	OnlyPHP   bool          `yaml:"only_php"`
	Backup    *bool         `yaml:"backup"`    // 覆盖/删除前备份为 .bak.<时间戳>（默认 true，指针以区分未配置）
	Delete    *bool         `yaml:"delete"`    // 远端已删除的文件，本地是否也删除（默认 false 保留）
	Protect   []string      `yaml:"protect"`   // 本地保护清单（相对 local_path，支持目录前缀），永不覆盖/删除（§6.4）
	Timeout   time.Duration `yaml:"timeout"`
}
```

- 在 `Config` 结构体加入 `Sync []SyncConfig \`yaml:"sync"\``（**列表 = 支持多仓库**，每个 enabled 条目各自启动一个同步循环，互不影响）。
- 默认值（逐条目）：`interval=60s`、`local_path="tvbox"`、`only_php=false`、`backup=true`、`delete=false`、`protect=[]`（空 = 不保护任何路径）、`timeout=15s`。
- `local_path` 解析时机：同步启动时结合 `resolvedDocRoot` 计算一次并校验在 docroot 内。
- **多仓库约束**：manifest 存于各自 `localRoot`（`docroot/local_path`），因此多个仓库条目应使用**互不相同的 `local_path`**，避免 manifest 互相覆盖导致误删对方文件。
- `protect` 路径以 `localRoot`（`docroot/local_path`）为锚点解析，支持目录前缀（如 `"private/"` 表示整个目录）；解析后必须仍以 `localRoot` 为前缀（防 `../` 穿越，见 §7）。
- 热加载：现有 `reload: 5` 机制下，配置变化后应**停止旧协程、按新配置重启**所有同步循环（见 §8）。

---

## 4. 模块结构（新增 `sync/` 包）

```
sync/
  sync.go      # SyncManager：配置加载、主循环、轮询调度、退避重试
  client.go    # 统一接口 RepoClient：Tree() / Fetch(path) / Base64
  github.go    # GitHubClient：git/trees + git/blobs（Bearer PAT）
  gitlab.go    # GitLabClient：repository/tree + files/raw（PRIVATE-TOKEN）
  manifest.go  # manifest 读写：{localRoot}/.manifest.json（localRoot = docroot + local_path）
  apply.go     # 应用变更：校验、备份、原子替换、删除
  log.go       # 日志（复用 logger 包）
```

**接口设计**（`client.go`）：

```go
// FileNode 仓库树中的一个文件节点
type FileNode struct {
	Path string // 仓库内相对路径
	SHA  string // blob sha（GitHub）或 id（GitLab），作为变更依据
	Mode string // 类型
}

// RepoClient 统一仓库访问接口
type RepoClient interface {
	Tree(branch, prefix string) ([]FileNode, error) // 递归目录树
	Fetch(path, ref string) ([]byte, error)          // 取文件内容（GitHub 自动 base64 解码）
	RepoID() string                                  // 仓库标识（用于 manifest 记录源）
}
```

---

## 5. 私有仓库 API 细节

### 5.1 GitHub（`github.go`）

| 用途 | 请求 |
|---|---|
| 目录树 | `GET https://api.github.com/repos/{owner}/{repo}/git/trees/{branch}?recursive=1` |
| 文件内容 | 树节点含 `url`：`GET /repos/{o}/{r}/git/blobs/{sha}`，返回 `{"content":"<base64>","encoding":"base64"}` |

- 认证：`Authorization: Bearer {PAT}`，fine-grained token 需 `Contents: Read`。
- **加速**：请求 URL 用 `buildURL(github.URL, apiUrl)` 组装；`github.enabled=true` 时依次尝试主/备用加速地址，再兜底官方 `api.github.com`。加速地址对 `git/blobs` 的 base64 内容同样适用。
- 响应体较大（树可能几十 KB），`recursive=1` 一次拿全量；按 `repo_path` 前缀（`repo_path` 配置）过滤出目标目录，过滤后**去掉 `repo_path/` 前缀**，得到相对路径。
- 只取 `"type":"blob"` 节点。
- 未认证限额 60 次/小时；带 token 5000 次/小时，轮询足够。
- `Fetch` 里 `base64.StdEncoding.DecodeString(content)` 解码。

### 5.2 GitLab（`gitlab.go`）

| 用途 | 请求 |
|---|---|
| 目录树 | `GET /api/v4/projects/{urlencoded_path}/repository/tree?ref={branch}&recursive=true&per_page=100`（分页） |
| 文件内容 | `GET /api/v4/projects/{id}/repository/files/{urlencoded_path}/raw?ref={branch}` |

- 认证：`PRIVATE-TOKEN: {PAT}`，权限 `read_repository`。
- `projects/{id}`：id 是 URL 编码的 `group/project`。
- tree 分页（GitLab 默认 20/页，`per_page` 最大 100，需循环 `X-Next-Page`）。

### 5.3 公共仓库（可选）

`token` 留空时走公开 API（GitHub 无需认证即可读树/内容；GitLab 公开项目同理）。开发文档默认按私有处理。

---

## 6. 同步算法（增量）

### 6.1 manifest 格式（`{docroot}/.manifest.json`）

```json
{
  "repo": "owner/repo",
  "branch": "main",
  "generated_at": 1788070000,
  "files": {
    "www/bjgitv.php": "a1b2c3...",
    "www/mklive.php": "d4e5f6..."
  }
}
```

- `files`：`path → sha`（GitHub blob sha；GitLab 可用 blob id）。`path` 为**相对 local_path 根目录**的路径（去掉了 `repo_path` 前缀）。
- manifest 存同步目标根（`{localRoot}/.manifest.json`，`localRoot = filepath.Join(resolvedDocRoot, local_path)`），同步本身不把它当脚本。

### 6.2 每次轮询流程

```
1. Tree(branch, repo_path)   → remoteFiles: {relPath: sha}（去掉 repo_path 前缀）
2. 过滤 only_php（默认 false 不过滤） → 仅开启时保留 .php/.phtml/.php3/.php4/.inc
3. 读本地 manifest             → localFiles: {relPath: sha}
4. 计算差异：
   - toUpdate = remote 中 sha != local 的文件（含新增）
   - toDelete = local 有而 remote 无的文件（仅当 delete=true）
4.5 剔除 protect 保护清单 → toUpdate / toDelete 中落在 protect 内的路径一律跳过（永不覆盖、永不删除）
5. 对 toUpdate 逐个：Fetch(path) → 应用（见 §7），落盘到 docroot 锚定的 local_path 下
6. 对 toDelete：先备份 → 删除
6.5 统计孤立文件（local 有而 remote 无，且不在 protect 内）→ 记日志并列出路径（见 §6.4）
7. 更新 manifest（只记录本次同步结果，文件未变则 sha 不变）
8. 记录日志：X 更新 / Y 新增 / Z 删除 / W 跳过 / I 保留（protect）
```

- **跳过逻辑**：`local.sha == remote.sha` → 不下载不覆盖（这也是"本地 Web 编辑器改动过但恰好 sha 一致则不覆盖"的天然保护；不一致时以远端为准）。
- manifest 不存在/损坏 → 视为首次同步，全量对比后重建。

### 6.3 首次全量 / 之后增量

- **首次同步**：本地无 manifest（或 manifest 损坏/源仓库变化）→ 视作首次，`toUpdate` = 远端全部目标文件，**全量拉取**后写入 manifest。
- **后续同步**：基于 manifest 的 sha 对比，**只拉增量**（新增/变更文件），未变化的跳过，无需额外配置。
- 逻辑上"首次全量"是"无 manifest → 全量对比"的自然结果，无需 `full_first` 开关。

### 6.4 孤立文件（local-only）处理

**定义**：本地 `localRoot` 下存在、但远端仓库树中不存在的文件 = **孤立文件**（本地私有文件）。典型例子：安卓设备 `www/tvbox/tv.txt`（设备自建/手改的直播源列表，不会进仓库）。

**为什么需要专门处理**：`delete: true` 时这类文件会被当成"远端已删除"而误删；`delete: false` 时虽不删，但无法区分"应删除的废弃文件"与"需保留的私有文件"。

**处理规则**：
1. **`protect` 保护清单**（`sync.protect`，相对 `local_path` 的路径列表，支持目录前缀）：
   - 清单内路径：同步**永不覆盖、永不删除**（`delete: true` 也跳过），完全由本地管理。
   - 典型配置：`protect: ["tv.txt", "private/"]`（`private/` = 整个目录）。
2. **非 protect 的孤立文件**：
   - `delete: true` → 按现有逻辑"先 `.bak` 备份再删除"。
   - `delete: false` → 保留。
3. **孤立文件报告**：每次轮询统计"本地有而远端无"的文件清单并写日志（含路径），便于用 adb 或 Web 编辑器核对、把确认的私有文件加入 `protect`。首次同步前建议先用 `adb shell ls -R` 盘点设备现有私有文件再配置 `protect`。
4. **安全**：`protect` 路径解析后必须仍以 `localRoot` 为前缀（复用 §7 防穿越校验）。

**与 `local_win` 的关系**：`protect` 保护"本地有、远端无"的私有文件（设备私有/手改）；`local_win`（§8 提及，后续可选）解决"同名文件本地编辑 vs 远端"的覆盖优先级，两者互补、不冲突。

---

## 7. 应用与安全（`apply.go`）

每个变更文件的落盘流程（关键：**先校验、后覆盖、失败回滚**）：

```
0. localRoot = filepath.Join(resolvedDocRoot, local_path)   // docroot 锚定，仅计算一次
1. 下载内容到内存 []byte
2. 语法校验：仅当文件是 PHP（.php/.phtml/.php3/.php4/.inc）时 simplePHPCheck(content)
   - 有 error 级问题 → 拒绝覆盖，记日志，跳过该文件（其余文件继续）
   - 仅 warning → 放行
   - 非 PHP（json/txt/m3u/jar/py/js…）不做文本校验，直接同步（jar 是二进制）
3. 写临时文件：{localRoot}/.{name}.sync.tmp
4. （backup=true）若目标已存在：copy 为 {name}.bak.<YYYYMMDD-HHMMSS>
5. 原子替换：os.Rename(tmp, target)
6. 目录自动创建（os.MkdirAll，按树中相对路径在 localRoot 下建子目录）
7. 删除（delete=true 且远端已删，且不在 protect 保护清单内）：先 .bak 备份再 os.Remove
```

**安全约束**：
- 目标绝对路径 = `filepath.Join(localRoot, relPath)`，且解析后必须仍以 `resolvedDocRoot` 为前缀（防 `../` 穿越，复用 PHP 模块的路径防护思路，`filepath.Clean` + `strings.HasPrefix`）。
- 临时文件写完后 rename，避免半写文件被 PHP 模块读到。
- 校验失败不中断整批，逐文件独立，尽量多同步成功。
- `.bak` 命名与 Web 编辑器一致（`{name}.bak.{时间戳}`），可被 Web 备份中心识别。

---

## 8. 生命周期 / 热加载

- `main.go` 初始化：`php.Init` 之后（需拿到 docroot）启动 `sync.Start(cfg)`。
- `Start` 内部：
  - `context` 管理，停止时 `cancel()`。
  - `time.Ticker` + 首次立即执行一次。
  - 轮询失败：指数退避 `3s → 15s → 60s → 5min` 上限，成功恢复原间隔。
- 配置热加载（`reload: 5`）：检测 `sync` 段变化 → `Stop()` 旧实例 → 按新配置 `Start()`。
- 与 Web 编辑器共存：同步写文件会经过 `.bak` 备份，不破坏编辑器的备份管理；编辑器的改动下次同步按 sha 对比（不一致以远端为准，可后续加 `local_win` 选项）。

---

## 9. 集成点汇总

| 集成点 | 位置 | 说明 |
|---|---|---|
| Config | `config/config.go` | 新增 `SyncConfig` + `Config.Sync` |
| docroot | `php` 模块 `docRoot` / `resolvedDocRoot` | 同步目标目录复用 PHP docroot（含 `~`/相对路径解析），`local_path` 以其为锚点 |
| GitHub 加速 | `config.Github`（`updater/github_updater.go` 的 `buildURL`） | `github.enabled` 时 GitHub API 走主/备用加速地址，兜底官方 |
| 语法校验 | `web/handlecode.go:690 simplePHPCheck(src) []phpIssue` | 覆盖前校验 PHP 合法性 |
| 备份 | Web 编辑器 `.bak.<时间戳>` 约定 | 保持一致，备份中心可管理 |
| 日志 | `logger` 包 | 同步结果、跳过、失败明细 |
| 热加载 | `reload: 5` | sync / github 配置变化重启协程 |
| HTTP 客户端 | 复用项目 HTTP client（DNS/代理能力） | 走 TVGate 的网络能力 |

---

## 10. 测试计划

**单元测试**：
- `github.go` / `gitlab.go`：用 `httptest` 模拟 API 响应（树/内容/base64/分页）。
- `manifest.go`：读写、损坏重建、sha 对比。
- `apply.go`：临时文件/备份/原子替换/校验拒绝/路径穿越。

**集成测试（本地起公开小仓库或 mock）**：
1. 首次全量同步到空 docroot。
2. 改一个文件 → 轮询只更新该文件，其余跳过。
3. 新增/删除文件 → 正确增删（delete=true 时）。
4. PHP 语法错误文件 → 拒绝覆盖，保留旧文件，日志记录。
5. token 失效 / 网络断 → 指数退避，恢复后继续。
6. 与 Web 编辑器同改一个文件 → 下次同步以远端覆盖（或配置 local_win）。
7. Android 设备部署后实际拉取验证。

**验收标准**：私有仓库改动提交后，各端在 `interval` 时间内自动同步，无 git 依赖，无 PHP 语法破坏。

---

## 11. 实施步骤（建议顺序）

1. `config.go`：`SyncConfig` + 默认值 + 热加载识别。
2. `sync/client.go` + `github.go`：能拉目录树与文件内容（先 GitHub）。
3. `sync/manifest.go`：manifest 读写。
4. `sync/apply.go`：校验/备份/原子替换。
5. `sync/sync.go`：主循环 + 退避 + 首次同步。
6. `gitlab.go`：补齐 GitLab 支持。
7. 接入 `main.go` + 热加载 + 日志。
8. 单元/集成测试 + Android 实机验证。
9. 更新 README / CHANGELOG。
