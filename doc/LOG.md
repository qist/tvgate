# 日志配置（log）

`log` 段控制 TVGate 的日志输出行为：是否记录日志、输出到标准输出还是文件，以及文件轮转策略。运行日志覆盖配置加载与热重载、代理组测速、仓库同步、定时任务、请求错误等关键事件，是排障的第一手依据；长期运行的服务建议开启文件日志并配置轮转，避免日志把磁盘写满。

日志输出支持两种模式：

- **标准输出模式**（`file: ""`）：日志打印到 stdout，适合 systemd、Docker、`nohup` 重定向等前台/托管运行方式，由 systemd journal 或 Docker 日志驱动统一收集与管理。
- **文件模式**（`file: <路径>`）：日志写入指定文件，按大小自动轮转（基于 lumberjack），可控制备份数量、保留天数与是否压缩。

无论哪种模式，最近的日志同时保留在内存缓冲中，Web 管理后台「实时日志」页面无需读文件即可直接查看；「日志配置」页可在线修改本段并即时生效。配置文件热重载时日志设置会一并重新加载。

## 配置段

```yaml
log:
  enabled: true       # 是否启用日志（false 时所有日志输出被丢弃）
  file: ""            # 日志文件路径；"" = 标准输出，非空如 /var/log/tvgate.log = 文件模式
  maxsize: 10         # 单个日志文件最大大小（MB），超过后触发轮转
  maxbackups: 10      # 保留的轮转备份文件个数
  maxage: 28          # 轮转备份最大保留天数
  compress: true      # 是否 gzip 压缩轮转后的备份文件
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| enabled | bool | false | 是否启用日志；配置文件未写 `log` 段或设为 `false` 时，日志输出被丢弃（等效 `io.Discard`） |
| file | string | "" | 日志输出文件；`""` 表示标准输出（stdout），非空表示写入该文件并启用轮转 |
| maxsize | int | 0（底层按 100MB 兜底） | 单个日志文件最大大小，单位 MB；写满后轮转出新文件 |
| maxbackups | int | 0（不按个数限制） | 最多保留多少个轮转备份文件；超出时删除最旧的 |
| maxage | int | 0（不按天数清理） | 轮转备份最大保留天数，按文件修改时间计算 |
| compress | bool | false | 轮转后的备份文件是否用 gzip 压缩（压缩后文件名追加 `.gz`） |

## 示例

标准输出模式（Docker / systemd 推荐，日志交给系统收集）：

```yaml
log:
  enabled: true
  file: ""
```

文件模式 + 轮转（单文件 100MB，最多 3 个备份、保留 7 天、不压缩）：

```yaml
log:
  enabled: true
  file: /var/log/tvgate.log
  maxsize: 100
  maxbackups: 3
  maxage: 7
  compress: false
```

小设备精简示例（低频写入、压缩节省空间）：

```yaml
log:
  enabled: true
  file: /var/log/tvgate.log
  maxsize: 10
  maxbackups: 2
  maxage: 14
  compress: true
```

## 注意事项

- **`enabled: false` 会静默丢弃全部日志**（包括配置加载、错误提示），排障前先确认该项为 `true`。
- **文件权限**：文件模式要求运行用户对目标目录有写权限；安卓 / Termux 等环境建议写到应用目录或家目录（如 `~/tvgate/tvgate.log`），避免 `/var/log` 不可写。
- **`maxbackups` 与 `maxage` 同时生效**：任一条件命中即删除备份；`maxbackups: 0` 表示不按个数限制（仅受 `maxage` 约束），`maxage: 0` 表示不按天数清理（仅受 `maxbackups` 约束），两者都为 0 且不清理会持续占用磁盘。
- **轮转文件命名**：`/var/log/tvgate.log` 轮转后的备份形如 `/var/log/tvgate-2026-09-04T15-04-05.000.log`，开启压缩后追加 `.gz`；压缩文件同样计入 `maxbackups` 个数。
- **避免双重落盘**：systemd / Docker 部署时用标准输出模式即可；若用 `nohup ... > /var/log/tvgate.log` 重定向，就不要再配置 `file`，否则同一份日志写两处。
- **热生效**：本段支持热重载，也可在 Web 后台「日志配置」页修改；输出目标从 stdout 切到文件（或反向）立即生效，无需重启。
- **Web 实时日志**不依赖 `file` 设置，内存缓冲始终保留最近日志行，文件模式下也可见。
