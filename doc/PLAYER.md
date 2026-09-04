# H5 播放器（player）

TVGate 内置 H5 播放器模块：服务端解析 IPTV 订阅（M3U 或逗号 TXT），为每个频道生成**不透明 key**（源地址哈希）对外发布，真实源地址与抓流 UA 全程只存在于服务器侧，浏览器/前端不可见。支持直播、EPG 节目单、回看（源具备 catchup 时）、换台与画中画等能力，自研播放引擎（MSE + wasm 转封装）随 SPA 构建，单二进制即可提供服务。

模块支持配置热加载：修改 `player` 段后由配置重载自动生效，挂载/摘除路由无需重启。Web 后台「播放器」页提供可视化配置（订阅源、EPG/台标模板、刷新间隔、默认 UA）。

## 配置段

```yaml
player:
  enabled: true                    # 是否启用播放器模块（热加载，挂载/摘除路由无需重启）
  subscription: tv.txt             # 订阅源：HTTP(S) URL 或本地文件/目录（写法见下文）
  epg: ""                          # TXT 订阅的 EPG 模板，含 {name}/{date} 占位符；M3U 订阅无需此项
  logo: ""                         # TXT 订阅的台标模板，含 {name} 占位符；M3U 自带 tvg-logo 时优先
  logo_dir: ""                     # 本地台标目录（如 /opt/TVLogo）：取 <频道名>.png，优先于上方模板
  update_interval: 2h              # 订阅定时刷新间隔
  ua: ""                           # 默认抓流 UA；频道行带 ua=xxx 时优先
  android_autoplay: false          # 安卓设备启动进入播放页标记位（供客户端 App 读取，见下文）
```

## 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `enabled` | bool | `false` | 是否启用播放器模块，热加载生效 |
| `subscription` | string | `""` | 订阅源：HTTP(S) URL 或本地路径；可指向单个文件或目录（目录=递归收集 `.txt` / `.m3u` / `.m3u8` 合并解析，跳过隐藏文件，按路径排序保证合并顺序稳定，单文件上限 64MB） |
| `epg` | string | `""` | 逗号 TXT 订阅的 EPG 模板，含 `{name}` / `{date}` 占位符；M3U 订阅走头行 `x-tvg-url`，无需此项 |
| `logo` | string | `""` | 逗号 TXT 订阅的台标模板，含 `{name}` 占位符；M3U 自带 `tvg-logo` 时优先生效 |
| `logo_dir` | string | `""` | 本地台标目录（如 `/opt/TVLogo`），频道台标取该目录下 `<频道名>.png`，经 `/player/logo/` 服务；优先于 `logo` 模板 |
| `update_interval` | duration | `2h` | 订阅定时刷新间隔（如 `30m` / `2h`） |
| `ua` | string | `""` | 默认抓流 User-Agent；频道未指定 `ua=` 时请求上游使用（部分源限制浏览器 UA） |
| `android_autoplay` | bool | `false` | **纯标记位**：安卓设备启动是否进入播放页。本服务不做任何行为控制，由安卓客户端 App 读取该标记后自行决定启动行为；Web 后台「播放器」页可可视化编辑 |

## 订阅源地址写法

`subscription` 支持以下写法，均可指向**单个文件**或**目录**：

| 写法 | 说明 |
|---|---|
| `https://...` / `http://...` | 远程订阅 URL（固定浏览器 UA 抓取） |
| `/opt/tvgate/tv.txt` | 本地绝对路径 |
| `file:///opt/tvgate/tv.txt` | `file://` 前缀本地路径 |
| `php://sub/tv.txt` | 相对 docroot（php 模块脚本目录）；也可写目录如 `php://sub` |
| `tv.txt` / `sub` | 裸相对路径，基准为 docroot |

## 订阅格式

按内容自动识别：以 `#EXTM3U` 开头按 M3U 解析，否则按逗号 TXT 解析。频道 URL 必须为支持的前缀（`http://` `https://` `udp://` `rtp://` `rtsp://` `php://`），否则该行跳过。

### M3U（.m3u / .m3u8）

```m3u
#EXTM3U x-tvg-url="https://epg.example.com/epg.xml.gz"
#EXTINF:-1 tvg-id="CCTV1" tvg-name="CCTV1" tvg-logo="https://logo.example.com/CCTV1.png" group-title="央视" ua="okhttp/3.8.1",CCTV1
http://source.example.com/cctv1.m3u8
```

| 位置 | 字段 | 说明 |
|---|---|---|
| 头行 | `x-tvg-url=` / `url-tvg=` | XMLTV EPG 地址（`.gz` 自动解压，服务端定时下载解析） |
| EXTINF | `tvg-id="..."` | EPG 匹配 ID |
| EXTINF | `tvg-name="..."` | EPG 匹配名（缺省回落 `tvg-id`） |
| EXTINF | `tvg-logo="..."` | 台标地址 |
| EXTINF | `group-title="..."` | 分组名 |
| EXTINF | `ua="..."` | 该频道抓流 UA |
| EXTINF | 最后一个逗号后 | 频道显示名 |

`#EXTINF` 后第一个非 `#` 行为该频道 URL；其余 `#` 行忽略（`#EXTVLCOPT` 等不解析，UA 用 `ua=` 属性）。

### 逗号 TXT（.txt）

```txt
央视,#genre#
ua=okhttp/3.8.1
CCTV1,http://source.example.com/cctv1.m3u8
CCTV2,http://source.example.com/cctv2.m3u8,ua=Mozilla/5.0
epg=https://epg.example.com/?ch={name}&date={date}
logo=https://logo.example.com/{name}.png
```

| 行格式 | 说明 |
|---|---|
| `分类,#genre#` | 声明分组，作用于后续频道（行首 `#` 可省略） |
| `ua=xxx` | 组/文件级默认 UA，作用于后续所有频道；`ua=`（空值）恢复 `player.ua` 默认；再次出现覆盖 |
| `名称,URL` | 频道行（URL 取最后一个逗号之后，名称可含逗号） |
| `名称,URL,ua=xxx` | 频道级 UA，优先于组级 `ua=` |
| `epg=模板或地址` | 含 `{`（如 `{name}`/`{date}`）按模板逐频道请求 EPG；`http` 开头且不含 `{` 视为整份 XMLTV 地址 |
| `logo=模板` | 台标模板，需含 `{name}` |

## 频道源协议

| 前缀 | 说明 |
|---|---|
| `http://` `https://` | 直连或代理拉流，302 跳转服务端自动跟随并重写 |
| `udp://` `rtp://` `rtsp://` | 组播/单播转 HTTP 播放 |
| `php://xxx.php?id=...` | docroot 脚本由内嵌 phpgo **内部执行**（不走 HTTP 回环）：302 Location 解析为真实源后续走 http 链路；m3u8 输出自动重写分片 |

## 访问入口

| 路径 | 说明 |
|---|---|
| `/web/player` | SPA 播放页（频道列表 / EPG / 回看 / 设置） |
| `/pp`、`/pp/<key>` | 独立播放页入口（旧版地址保留）：直接服务播放页，**不跳转后台路径**，`/pp/<key>` 转为 `/pp#<key>` 深链 |
| `/api/player/channels` | 频道列表 API（含不透明 key、分组、台标） |
| `/api/player/epg?ch=<tvg-id>&date=YYYY-MM-DD` | EPG 节目单 API |
| `/api/player/catchup?key=<key>&start=<YmdHis>&end=<YmdHis>` | 回看 API（基于 EPG 节目单起止时间） |
| `/player/<key>` | 播放流入口；HLS 分片走 `/player/<key>/<token>` 短路径 |
| `/player/logo/` | 台标服务（`logo_dir` 本地台标经此输出） |

非白名单 key 的请求返回 `403 Forbidden`。

> **布局**：桌面/电视端频道列表与节目单在视频**左侧**（可折叠），移动端在视频下方；界面固定简体中文，支持触摸与遥控器（方向键/数字换台）操作。

## 不透明 key 机制

服务端为每个频道生成稳定不透明 key：**源地址 md5 前 8 位 + sha1 前 4 位 = 12 位十六进制**（冲突时追加递增后缀）。浏览器与前端 API 只能看到 key，真实源地址、抓流 UA 均不下发。

- 播放流入口 `/player/<key>`：key 在管理器中无记录（即不在订阅白名单内）直接返回 `403`。
- HLS m3u8 重写：子分片以**短 token**（URL 的 sha1 前 10 位十六进制）登记为 `/player/<key>/<token>` 短路径，分片真实地址同样不外露。
- 子分片仅允许与原源同 scheme+host 的相对路径解析（拒绝 scheme 注入），防止被当作开放代理。

## EPG 节目单

- **M3U 订阅**：EPG 走头行 `x-tvg-url=` / `url-tvg=` 指定的 XMLTV 地址，服务端定时下载解析，`.gz` 自动解压；频道按 `tvg-id`（缺省回落 `tvg-name`）匹配节目。
- **TXT 订阅**：EPG 走 `player.epg` 模板或订阅内 `epg=` 行，按 `{name}` / `{date}` 占位符逐频道请求；`http` 开头且不含 `{` 时视为整份 XMLTV 地址。

查询接口：`/api/player/epg?ch=<tvg-id>&date=YYYY-MM-DD`。

## 回看（catchup）

`http/https` 源自动支持回看，订阅内无需额外声明。流程：播放页依据 EPG 节目单选择节目 → 请求 `/api/player/catchup?key=<key>&start=<YmdHis>&end=<YmdHis>` → 服务端在频道源地址上拼接 `playseek=<start>-<end>`（起止时间格式 `YYYYmmddHHMMSS`，时差由源侧处理）→ 登记短 token 后返回 `/player/<key>/<token>` 播放地址。

对含 `/PLTV/` 段的中国移动 OTT 源，回看自动将 `PLTV` 替换为 `TVOD`（时移服务器路径），如 `ott.example.com/PLTV/.../index.m3u8` → `ott.example.com/TVOD/.../index.m3u8`；源地址已含 `?` 时以 `&` 追加 `playseek` 参数。

## 本地台标目录

配置 `logo_dir` 后（如 `/opt/TVLogo`），频道台标优先取该目录下 **`<频道名>.png`**，经 `/player/logo/` 路径对外服务；未命中时回落到 M3U `tvg-logo` 或 TXT `logo=` / `player.logo` 模板。适合将台标包放到本地、避免外链失效的场景。

## 安卓客户端启动标记（android_autoplay）

`player.android_autoplay` 是**纯标记位**：仅表示「安卓设备启动是否进入播放页」，供安卓客户端 App 启动时读取。

- 本服务与 H5 播放页**不做任何行为控制**，播放页对所有设备照常可访问（含远程），是否进入播放页由客户端 App 自行判断（如关闭时 App 启动后不打开播放地址，停留在自带的信息启动页）。
- 修改方式：直接改配置文件，或 Web 后台「播放器」页的「安卓设备启动进入播放页」开关（保存写入 YAML，热加载生效）。
- 未配置时播放器各接口行为不变；该标记不影响 `/player/<key>`、`/web/player` 等任何访问权限。

## 安全设计

- **源白名单 = 唯一可播放清单**：订阅内容即允许播放的频道清单，仅订阅内的频道可经播放器访问；白名单外 key 一律 `403`。
- **真实源不外露**：前端只见不透明 key 与短 token，源地址与 UA 全程留存在服务器侧。
- **分片子路径受控**：HLS 分片仅接受同源相对路径，杜绝开放代理风险。
- **可叠加全局鉴权**：启用 `global_auth` 后，播放器各接口与 `/player/<key>` 拉流同样要求携带有效 token（见 `doc/GLOBALAUTH.md`）。

## 示例

```yaml
player:
  enabled: true
  subscription: https://sub.example.com/tv.m3u      # 远程订阅；也可写 /opt/tvgate/tv.txt、php://sub 等
  epg: https://epg.example.com/?ch={name}&date={date}
  logo: https://logo.example.com/{name}.png
  logo_dir: /opt/TVLogo                             # 本地台标目录：<频道名>.png，优先于 logo 模板
  update_interval: 30m
  ua: okhttp/3.8.12                                 # 部分 IPTV 源拒绝浏览器 UA 时按需设置
  android_autoplay: false                           # 安卓启动进入播放页标记位（App 读取，服务端不控制）
```

## 注意事项

- 订阅按内容自动识别 M3U / TXT，文件扩展名仅作参考；目录订阅会合并全部 `.txt` / `.m3u` / `.m3u8`，单文件上限 64MB。
- TXT 订阅的组级 `ua=` 作用于其后的所有频道，注意书写顺序；`ua=`（空值）可恢复 `player.ua` 默认。
- `epg` / `logo` 模板仅对逗号 TXT 订阅生效；M3U 订阅的 EPG/台标以头行与 `tvg-*` 属性为准。
- 回看依赖源站支持 `playseek` 参数；非 `http/https` 源（udp/rtp/rtsp/php）不支持 catchup，请求会返回 `400`。
- EPG 起止时间来自节目单，EPG 数据缺失或频道 `tvg-id` 不匹配时无法发起回看。
- 本地台标文件名需与频道显示名完全一致（区分大小写），否则回落模板地址。
