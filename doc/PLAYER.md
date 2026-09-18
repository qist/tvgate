# H5 播放器（player）

TVGate 内置 H5 播放器模块：服务端解析 IPTV 订阅（M3U 或逗号 TXT），为每个频道生成**不透明 key**（源地址哈希）对外发布，真实源地址与抓流 UA 全程只存在于服务器侧，浏览器/前端不可见。支持直播、EPG 节目单、回看（源具备 catchup 时）、换台与画中画等能力，自研播放引擎（MSE + wasm 转封装）随 SPA 构建，单二进制即可提供服务。

模块支持配置热加载：修改 `player` 段后由配置重载自动生效，挂载/摘除路由无需重启；改 `subscription` / `subscriptions` / `epg` / `epgs` 会连带重新拉取订阅与 EPG（无需重启）。Web 后台「播放器」页提供可视化配置（订阅源与多订阅源、EPG 来源与多 EPG 来源、台标模板、刷新间隔、默认 UA，并展示可对外提供的标准 EPG 接口地址）。

## 配置段

```yaml
player:
  enabled: true                    # 是否启用播放器模块（热加载，挂载/摘除路由无需重启）
  subscription: tv.txt             # 订阅源：HTTP(S) URL 或本地文件/目录（写法见下文）；也可写多个源（换行/逗号/分号分隔）
  subscriptions: []                # 追加订阅源（可选，按序在 subscription 之后合并解析，同源去重）
  epg: ""                          # EPG 模板或固定 XMLTV 地址（xml/xml.gz）；也可写多个来源（换行/分号/逗号分隔）
  epgs: []                         # 追加 EPG 来源（可选，按序在 epg 之后参与合并，同址去重）
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
| `subscription` | string | `""` | 订阅源：HTTP(S) URL 或本地路径；可指向单个文件或目录（目录=递归收集 `.txt` / `.m3u` / `.m3u8` 合并解析，跳过隐藏文件，按路径排序保证合并顺序稳定，单文件上限 64MB）。可写**多个源**（换行 / 逗号 / 分号分隔，等价于 `subscriptions`）。HTTP(S) 源自动跟随 301/302（http → https 升级、域名搬迁等） |
| `subscriptions` | list | `[]` | **追加订阅源**（多源合并）：在 `subscription` 之后按序解析，同一地址只解析一次；单个源失败只跳过该源，全部失败时保留上一次的频道表。元素写法与 `subscription` 完全相同（URL / 本地路径 / 目录 / `file://` / `php://`） |
| `epg` | string | `""` | 频道 EPG 来源。两种形态：**模板**（含 `{name}` / `{date}` 占位符，按频道逐条请求）或 **固定 XMLTV 地址**（`http(s)` 开头且不含 `{`，如 `https://xxx/epg.xml.gz`，整份节目单，`.gz` 自动解压、按频道名匹配，301/302 自动跟随）。可写**多个来源**（换行 / 分号分隔；逗号也支持，但仅当每段都像来源时才算分隔符，避免误切 URL 查询串） |
| `epgs` | list | `[]` | **追加 EPG 来源**（多源合并）：在 `epg` 之后按序参与合并，同一地址只算一次。元素写法与 `epg` 完全相同。合并规则见「EPG 节目单 → 多来源合并」 |
| `logo` | string | `""` | 逗号 TXT 订阅的台标模板，含 `{name}` 占位符；M3U 自带 `tvg-logo` 时优先生效 |
| `logo_dir` | string | `""` | 本地台标目录（如 `/opt/TVLogo`），频道台标取该目录下 `<频道名>.png`，经 `/player/logo/` 服务；优先于 `logo` 模板 |
| `update_interval` | duration | `2h` | 订阅定时刷新间隔（如 `30m` / `2h`） |
| `ua` | string | `""` | 默认抓流 User-Agent；频道未指定 `ua=` 时请求上游使用（部分源限制浏览器 UA） |
| `android_autoplay` | bool | `false` | **纯标记位**：安卓设备启动是否进入播放页。本服务不做任何行为控制，由安卓客户端 App 读取该标记后自行决定启动行为；Web 后台「播放器」页可可视化编辑 |

## 订阅源地址写法

`subscription` 支持以下写法，均可指向**单个文件**或**目录**：

| 写法 | 说明 |
|---|---|
| `https://...` / `http://...` | 远程订阅 URL（固定浏览器 UA 抓取）；**301/302 自动跟随**（最多 5 跳，含 http → https 升级、域名/CDN 搬迁；仅接受 http/https，不会跟到 `file://`） |
| `/opt/tvgate/tv.txt` | 本地绝对路径 |
| `file:///opt/tvgate/tv.txt` | `file://` 前缀本地路径 |
| `php://sub/tv.txt` | 相对 docroot（php 模块脚本目录）；也可写目录如 `php://sub` |
| `tv.txt` / `sub` | 裸相对路径，基准为 docroot |

**多订阅源合并**：`subscription` 可用换行 / 逗号 / 分号分隔写多个源，或用 `subscriptions` 列表追加。解析顺序为「先 `subscription`，再 `subscriptions`」，**同一地址只解析一次**；每个源独立拉取，单个源失败只跳过它自己（其余源照常加载），全部源都失败时保留上一次的频道表。后台「播放器」页的多订阅源输入框一行一个源。

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
| `/api/player/channels` | 频道列表 API：`{"list":[{key,name,group,scheme,tvgId,tvgName,tvgLogo,epgType}],"epgSource":{kind,template,logo}}` |
| `/api/player/epg?key=<key>&date=YYYYMMDD` | EPG 节目单 API（播放器内部用法）：频道用不透明 `key`（与 `/player/<key>` 同源）定位，服务端内部换算 tvg-id/频道名查源；`{"programs":[{from,to,title}],"name":..,"date":..}`（`from`/`to` 为 XMLTV 原样时间串）。`date` 可省略（默认今天）且容忍 `YYYY-MM-DD` / `YYYY/MM/DD` / `YYYYMMDD`；key 未登记返回 `403`，`key`/`ch` 都缺返回 `400` |
| `/api/player/epg?ch=<频道名或tvg-id>&date=YYYYMMDD` | EPG 节目单 API（**对外标准用法**）：按频道名（或 XMLTV channel id）查询，**不要求**是本机订阅里的频道，因此可把本机当 EPG 源提供给其它播放器/系统（`name=` 为同义参数）。受全局 token 保护（同其它 `/api`） |
| `/api/player/catchup?key=<key>&from=<unix秒>&to=<unix秒>` | 回看 API（基于 EPG 节目单起止时间）：时间参数用 Unix 秒，服务端换算为源侧 `playseek` 的 `YmdHis` 串，返回 `{"play":"/player/<key>/<token>"}` |
| `/player/<key>` | 播放流入口；HLS 分片走 `/player/<key>/<token>` 短路径 |
| `/player/logo/` | 台标服务（`logo_dir` 本地台标经此输出） |

非白名单 key 的请求返回 `403 Forbidden`。

> **布局**：桌面/电视端频道列表与节目单在视频**左侧**（可折叠），移动端在视频下方；频道列表支持分组筛选：**分组跟随正在播放的频道**（换台/重进页面自动落在该台所在分组，并跨会话记忆该分组；只点分组不选台不会改记忆）。支持触摸与遥控器（方向键/数字换台）操作。界面文案当前固定简体中文（词条已备简体/繁體/English 三套，见 `ui/src/i18n/player.ts`，暂未开放切换）。

## 不透明 key 机制

服务端为每个频道生成稳定不透明 key：**源地址 md5 前 8 位 + sha1 前 4 位 = 12 位十六进制**（冲突时追加递增后缀）。浏览器与前端 API 只能看到 key，真实源地址、抓流 UA 均不下发。

- 播放流入口 `/player/<key>`：key 在管理器中无记录（即不在订阅白名单内）直接返回 `403`。
- HLS m3u8 重写：子分片以**短 token**（URL 的 sha1 前 10 位十六进制）登记为 `/player/<key>/<token>` 短路径，分片真实地址同样不外露。
- 子分片仅允许与原源同 scheme+host 的相对路径解析（拒绝 scheme 注入），防止被当作开放代理。

## EPG 节目单

- **M3U 订阅**：EPG 走头行 `x-tvg-url=` / `url-tvg=` 指定的 XMLTV 地址，服务端定时下载解析，`.gz` 自动解压；频道按 `tvg-id`（缺省回落 `tvg-name`）匹配节目。
- **TXT 订阅**：EPG 走 `player.epg` 模板或订阅内 `epg=` 行，按 `{name}` / `{date}` 占位符逐频道请求；`http` 开头且不含 `{` 时视为整份 XMLTV 地址。

查询接口：

| 用法 | 地址 | 说明 |
|---|---|---|
| 播放器内部 | `/api/player/epg?key=<频道key>&date=YYYY-MM-DD` | 不透明 key 定位，服务端换算 tvg-id/频道名 |
| **对外标准** | `/api/player/epg?ch=<频道名>&date=YYYYMMDD` | 按频道名（或 XMLTV channel id）查，不要求是本机订阅频道；`name=` 同义。可原样给别的播放器当 EPG 源：`epg=http://<本机>/api/player/epg?ch={name}&date={date}` |

响应 `{"programs":[{from,to,title}],"name":<查询名>,"date":<YYYYMMDD>}`。

### 多来源合并

EPG 来源可配多个（订阅内嵌来源 → `player.epg` → `player.epgs`，**按书写顺序即优先级**）：

- **整份 XMLTV（xml 型）**：全部拉到服务端解析并存多份数据，查询时逐个来源解析该频道后**按开始时间并集**，同一时段（`start` 相同）取靠前来源那条；某个来源拉取/解析失败或内容为空只少它那份数据，其余来源照常生效，全部失败才保留上一次成功的数据。来源 301/302 跳转自动跟随（如 `http://epg.51zmt.top:8000/e1.xml.gz` → CDN 域名）。
- **模板（template 型）**：按序逐个请求（填 `{name}` / `{date}`）后同样按时间并集。
- **跨类型互补**：主类型查不到该频道节目时，用另一类型补齐——整份 XMLTV 里没有的频道会用模板源补，模板源查空则回落整份 XMLTV。不同服务商覆盖的频道不同，这比"整体切换"更实用；同一频道在两个来源里 channel id 不同、但 display-name 一致时照样能对上并合并。

```yaml
player:
  subscription: tv.m3u
  epg: https://a.example.com/?ch={name}&date={date}   # 模板源（首选）
  epgs:
    - http://epg.51zmt.top:8000/e1.xml.gz             # 整份 XMLTV：与模板源互补
    - https://b.example.com/e2.xml.gz                 # 再叠加一份，优先级更低
```

> 日志佐证：服务启动/刷新后打印 `✅ [player] EPG 解析完成: <N> 频道(<M> 份来源合并), 源: <生效URL列表>`；某来源无可用节目会打印 `⚠️ [player] EPG 来源无可用节目(已跳过): <URL>`。

## 回看（catchup）

`http/https` 源自动支持回看，订阅内无需额外声明。流程：播放页依据 EPG 节目单选择节目 → 请求 `/api/player/catchup?key=<key>&from=<unix秒>&to=<unix秒>` → 服务端把 Unix 秒换算为 `YmdHis` 本地时间串，在频道源地址上拼接 `playseek=<start>-<end>`（时差由源侧处理）→ 登记短 token 后返回 `{"play":"/player/<key>/<token>"}` 播放地址。

对含 `/PLTV/` 段的中国移动 OTT 源，回看自动将 `PLTV` 替换为 `TVOD`（时移服务器路径），如 `ott.example.com/PLTV/.../index.m3u8` → `ott.example.com/TVOD/.../index.m3u8`；源地址已含 `?` 时以 `&` 追加 `playseek` 参数。

## 本地台标目录

配置 `logo_dir` 后（如 `/opt/TVLogo`），频道台标优先取该目录下 **`<频道名>.png`**，经 `/player/logo/` 路径对外服务；未命中时回落到 M3U `tvg-logo` 或 TXT `logo=` / `player.logo` 模板。适合将台标包放到本地、避免外链失效的场景。

## 安卓客户端启动标记（android_autoplay）

`player.android_autoplay` 是**纯标记位**：仅表示「安卓设备启动是否进入播放页」，供安卓客户端 App 启动时读取。

- 本服务与 H5 播放页**不做任何行为控制**，播放页对所有设备照常可访问（含远程），是否进入播放页由客户端 App 自行判断（如关闭时 App 启动后不打开播放地址，停留在自带的信息启动页）。
- 修改方式：直接改配置文件，或 Web 后台「播放器」页的「安卓设备启动进入播放页」开关（保存写入 YAML，热加载生效）。
- 未配置时播放器各接口行为不变；该标记不影响 `/player/<key>`、`/web/player` 等任何访问权限。

## 播放引擎与前端能力

播放页由自研引擎驱动（源码 `ui/src/media-engine/`，随 SPA 构建进二进制）：

| 能力 | 说明 |
|---|---|
| 传输 | 直连与 HLS 分片拉取，`http/https` 源自动跟随 302；内建自愈重连（断流 / 网络切换、指数退避、直播无数据看门狗） |
| 解封装 | MPEG-TS、FLV（H.264 / H.265 + AAC / MP2 / MP3 / AC-3 / E-AC-3） |
| 视频 | MSE 转封装为 fMP4；不支持 MSE 的终端回落原生 `<video>`；可选 WebGL 画质增强与自动去隔行 |
| 音频 | AAC 直通；MP2 / MP3 / AC-3 / E-AC-3 走 wasm 软解（WebAudio 输出），含 WSOLA 变速、PCM 重钉与静音看门狗 |
| 同步 | 以视频输出时间轴为基准，漂移超阈值才重建（不轻易 seek）；页面切到后台时音频自由续播 |

**媒体信息徽标**（播放页右上角，全部取自实际流而非声明值）：

| 徽标 | 取值 | 数据来源 |
|---|---|---|
| 分辨率 | `4K`、`1080p`、`1080i`… | 视频轨宽高 + 扫描方式（H.264 `frame_mbs_only_flag`；H.265 VUI / PTL 约束位） |
| 帧率 | `25 FPS` | 视频样本相邻 DTS 差的中位数估计；开启去隔行时显示 ×2 |
| 视频编码 | `H.264` / `HEVC` / `VP9` / `AV1` | 轨道 codec 串 |
| 音频编码 | `AAC` / `MP2` / `AC-3` / `E-AC-3` / `MP3` / `Opus` | 轨道 codec 串 |
| 声道 | `立体声` / `5.1` / `7.1`… | AAC=ADTS 首帧；AC-3=BSI `acmod+lfeon`；E-AC-3=`acmod+lfeon`；MP2/MP3=帧头 `channelMode` |
| 动态范围 | `SDR` / `HDR10` / `HLG` | 视频轨元数据 |
| 码率 | `3.2 Mbps` | 声明值或实测值（悬停可见来源） |

**界面风格**：播放页与 Web 后台共用同一设置（同一浏览器内保持一致），共 6 套配色：

| 值 | 名称 | 主色 |
|---|---|---|
| `ocean` | 深海 | 天蓝 |
| `emerald` | 翡翠 | 翠绿（**默认**） |
| `sunset` | 落日 | 橙红 |
| `rose` | 玫红 | 玫瑰 |
| `amber` | 琥珀 | 琥珀金 |
| `slate` | 石墨 | 中性灰蓝 |

切换入口：播放页「设置 → 界面风格」，或 Web 后台右上角的风格选择器（登录页右上角同）。选择只保存在浏览器 `localStorage`（键 `tvgate-player-appearance`），服务端不参与，也不影响其他设备/访客。

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
  subscriptions:                                    # 可选：追加订阅源（多源合并，同源去重）
    - /opt/tvgate/tv2.txt
    - php://sub
  epg: https://epg.example.com/?ch={name}&date={date} # 模板源；也可写整份 XMLTV 地址或多个来源
  epgs:                                             # 可选：追加 EPG 来源（多源合并，同址去重）
    - http://epg.51zmt.top:8000/e1.xml.gz
  logo: https://logo.example.com/{name}.png
  logo_dir: /opt/TVLogo                             # 本地台标目录：<频道名>.png，优先于 logo 模板
  update_interval: 30m
  ua: okhttp/3.8.12                                 # 部分 IPTV 源拒绝浏览器 UA 时按需设置
  android_autoplay: false                           # 安卓启动进入播放页标记位（未配置默认不进入；显式 true 才自动进入。App 读取，服务端不控制）
```

## 注意事项

- 订阅按内容自动识别 M3U / TXT，文件扩展名仅作参考；目录订阅会合并全部 `.txt` / `.m3u` / `.m3u8`，单文件上限 64MB。
- 多订阅源（`subscription` 内分隔多个 / `subscriptions`）按序合并、同源去重；单源失败只跳过它自己，**全部失败时保留上一次的频道表**，不会把频道清空。
- 订阅源与 EPG 来源的 HTTP(S) 拉取都**跟随 301/302**（最多 5 跳，相对 `Location` 按当前地址解析；只接受 http/https）。源站把 `http` 升级到 `https`、换 CDN 域名都不用改配置；每次跟随会打印 `↪️ [player] 重定向跟随: <原地址> → <新地址>`。
- 整份 XMLTV 来源（`epg` / `epgs` 填固定 `xml` / `xml.gz` 地址）与订阅共用同一刷新时钟，每个刷新周期随订阅一起重拉全部来源并合并；改 `player` 段后热加载即重新拉取，无需重启。
- 界面风格是**浏览器本地设置**（`localStorage`），与服务器配置无关；换设备/换浏览器需重新选择。
- TXT 订阅的组级 `ua=` 作用于其后的所有频道，注意书写顺序；`ua=`（空值）可恢复 `player.ua` 默认。
- `epg` 模板仅对逗号 TXT 订阅生效；M3U 订阅的 EPG 以头行 `x-tvg-url` 为准。`player.epg` / `player.epgs` 填**固定 XMLTV 地址**时对两种订阅都生效：与订阅内嵌 XMLTV 一起拉取、按频道合并，任一来源失效不影响其它来源（见「EPG 节目单 → 多来源合并」）。
- `logo` 模板仅对逗号 TXT 订阅生效；M3U 订阅的台标以 `tvg-logo` 属性为准。
- 回看依赖源站支持 `playseek` 参数；非 `http/https` 源（udp/rtp/rtsp/php）不支持 catchup，请求会返回 `400`。
- EPG 起止时间来自节目单，EPG 数据缺失或频道 `tvg-id` 不匹配时无法发起回看。
- 本地台标文件名需与频道显示名完全一致（区分大小写），否则回落模板地址。
