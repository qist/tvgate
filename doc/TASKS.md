# 定时任务（tasks）

TVGate 内置定时任务模块，按标准 5 段 cron 表达式调度执行命令，行为类似 Linux crontab。`tasks` 为扁平列表，按 `group` 在 Web 后台分组展示，支持可视化增删改、立即执行与状态查看（上次结果 / 耗时 / 输出摘要 / 下次执行时间）。

`command` 支持两种执行方式：系统 shell 命令，或 `php://` 前缀交由内嵌 phpgo 解释器直接执行 docroot 脚本——后者无需系统安装 php，对安卓等无原生 php 的环境友好。

## 配置段

```yaml
tasks:
  - name: 每日备份               # 任务名称（标识用途，可空）
    enabled: true                # 是否启用
    group: 运维                  # 分组（仅用于 Web 列表分类展示）
    cron: "0 4 * * *"            # 标准 5 段：分 时 日 月 周，支持 */n 步长
    command: php://backup/run.php?type=full   # 执行命令，见下文两种方式
    timeout: 60s                 # 单次执行超时（0 = 不限）
    notes: 全量备份 docroot      # 备注（可选）
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `name` | string | 空 | 任务名称，标识用途；为空时以命令片段标识 |
| `enabled` | bool | `false` | 是否启用该任务 |
| `group` | string | 空 | 分组名，仅用于前端列表分类展示（扁平结构） |
| `cron` | string | `0 0 * * *`（每天 0 点） | 标准 5 段 cron 表达式：分 时 日 月 周，支持 `*/n` 步长 |
| `command` | string | 无 | 要执行的命令：系统 shell 命令，或 `php://xxx.php`（内嵌 phpgo 执行 docroot 脚本） |
| `timeout` | duration | `0`（不限） | 单次执行超时，`0` 表示不限制执行时长 |
| `notes` | string | 空 | 备注，仅展示说明 |

## 示例

```yaml
tasks:
  - name: 每日备份
    enabled: true
    group: 运维
    cron: "0 4 * * *"
    command: php://backup/run.php?type=full   # 内嵌 phpgo 执行 docroot/backup/run.php
    timeout: 60s
    notes: 全量备份 docroot

  - name: 清理临时文件
    enabled: false
    group: 运维
    cron: "0 3 * * *"
    command: find /tmp/tvgate_tmp -mtime +7 -delete   # 系统命令（经 sh -c / cmd /C 执行）
    timeout: 30s
    notes: 系统命令示例

  - name: 冒烟测试
    enabled: true
    group: 测试
    cron: "*/5 * * * *"                       # 每 5 分钟一次
    command: echo OK
    timeout: 10s
    notes: 定时任务连通性测试
```

## 注意事项

### 命令执行方式

| 形式 | 说明 |
|---|---|
| 系统命令 | 经系统 shell 执行（Linux `sh -c`，Windows `cmd /C`），如 `/usr/bin/php /path/x.php` |
| `php://xxx.php?key=val` | 内嵌 phpgo 解释器直接执行 docroot 脚本——**无需系统安装 php**（安卓等环境友好）；GET 语义注入 `$_GET`；脚本输出体作为任务输出；脚本不存在或返回 HTTP ≥ 400 判为失败 |

### php:// 内部执行说明

- `php://` 内部执行不走 HTTP 回环、不依赖 IP、不经过鉴权，与 H5 播放器 `php://` 频道源同一条链路
- 路径相对 php 模块 `docroot`，兼容 `php://php/xxx.php` 与 `php://xxx.php` 两种写法

### Web 后台操作

- 「配置 → 定时任务」页可视化增删改任务（名称 / 分组 / cron / 命令 / 超时 / 备注）
- 支持**立即执行**：手动触发一次，无需等待 cron 到点。为防止该接口被当作任意命令执行入口，`POST /api/tasks/run` 只接受后台会话，且命令一律取自**已保存的任务配置**（按任务名 key 查找），请求体里自带的 `command` 不会被执行；未保存的新任务需先保存再执行
- 状态查看：上次执行结果 / 耗时 / 输出摘要 / 下次执行时间

### 安全说明

定时任务的 `command` 等价于 crontab 条目，可执行系统 shell 命令，具备面板运行用户的权限。请确保 Web 后台（`web.path`）仅对可信网络开放，并设置强密码；如只需运行 docroot 下的脚本，建议统一使用 `php://` 形式，避免在任务里写系统命令。

### 危险命令拦截

保存任务与运行任务时会校验 `command`，命中明显的系统级破坏操作会被拒绝（保存返回 400，运行期直接失败并记录日志）：

- 磁盘/分区操作：`mkfs*`、`mke2fs`、`mkdosfs`、`mkswap`、`wipefs`、`fdisk`、`sfdisk`、`parted`、`sgdisk`、`gdisk`、`cfdisk`
- 关机重启：`shutdown`、`reboot`、`halt`、`poweroff`、`kexec`
- 递归删除/改权限根目录或系统目录：`rm -rf /`、`rm -rf /etc`、`chmod -R 777 /`、`chown -R ... /`
- 覆写块设备：`dd of=/dev/sdX`、`cp /dev/zero /dev/sda`、`> /dev/sdX`
- 删除根目录文件：`find / -delete`、`find / -exec rm`
- 从网络下载后直接执行：`curl ... | sh`、`wget ... | bash`
- fork 炸弹、`kill 1`/`killall5`、移动/覆盖系统目录
- 通过 `sh -c`/`bash -c`/`cmd /c`/`eval` 嵌套执行的命令会被递归检查；`-EncodedCommand` 等无法审计的编码载荷直接拒绝
- 任务不允许删除整个 PHP docroot（子目录清理仍允许，如 `rm -rf <docroot>/cache`）

`php://` 命令走内嵌 phpgo、不经过系统 shell，不受上述黑名单限制。黑名单只是兜底，不是沙箱：无法覆盖混淆/编译脚本等所有绕过方式，彻底隔离请使用容器或低权限用户运行。

### 超时说明

phpgo 为进程内同步执行，`timeout` 到期后仅不再等待，无法强杀运行中的脚本；请避免任务脚本自身长时间阻塞。
