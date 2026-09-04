# 推流发布（Publisher）配置指南

拉取源直播流，同时提供本地播放（FLV / HLS）与 RTMP 对外推流，支持主备源切换、
多平台同时推流、HLS 录制、时间段回放与 MP4 归档。

所有配置位于 `config.yaml` 顶层 `publisher:` 段，支持后台「推流发布」页在线编辑，
保存后热重载生效。

---

## 一、配置结构总览

```yaml
publisher:
  path: /live                      # 播放地址前缀（播放地址 = http://<host>:<port><path>/play/<流名>.flv|.m3u8）
  <流名>:                           # 流名即卡片名，如 cctv1、camera_01
    enabled: true                  # 是否启用（false 时卡片显示"未启用"，可手动开启推流）
    protocol: ffmpeg               # 处理协议，固定 ffmpeg
    buffer_size: 0                 # 拉流缓冲（字节），0 用默认值
    streamkey:                     # 拉流鉴权 key（可选）
      type: random                 # random / fixed / external
      length: 16                   # random 长度
      # value: mykey               # fixed 时的固定值
      expiration: 24h              # key 有效期
    stream:
      source:
        type: live                 # 源类型（可留空）
        url: http://src.example.com/live/stream.m3u8        # 主源地址
        backup_url: http://bak.example.com/live/stream.m3u8 # 备源地址（主源失败自动切换）
        # ffmpeg_options: {...}    # 拉流独立 ffmpeg 参数（自定义请求头等）
      local_play_urls:             # 本地播放输出（flv / hls）
        - protocol: flv
          enabled: true
          flv_ffmpeg_options: { ... }
        - protocol: hls
          enabled: true
          hls_ffmpeg_options: { ... }
          hls_segment_duration: 5  # 分片时长（秒）
          hls_segment_count: 6     # 直播列表保留分片数
          hls_path: /data/hls      # TS 分片目录（默认 /tmp/hls/<流名>，重启清空）
          hls_enable_playback: true     # 开启时间段回放
          hls_retention_days: 26h       # TS 保留期，留空永久
          ts_filename_template: "{stream}/{date}/{seq}.ts"  # 分片文件名模板
          hls_archive_interval: 24h     # 归档间隔：≥24h 每天一个 MP4；<24h 按 interval 滚动归档；留空不归档
          hls_archive_retention: 0      # 归档 MP4 保留期：留空/0 = 永久
          hls_archive_path: /data/archive   # 归档目录（默认 ./archive）
      mode: primary-backup         # primary-backup（主备轮换）/ all（同时推多端）
      receivers:
        primary:
          push_url: rtmp://push.example.com/live/streamkey1   # 主推地址
        backup:
          push_url: rtmp://pushbak.example.com/live/streamkey1
        # all 模式写法：
        # all:
        #   - push_url: rtmp://a.example.com/live/key1
        #     play_urls: { flv: http://a.example.com/live/ }
        #     ffmpeg_options: { video_codec: libx264, video_bitrate: 8m, preset: ultrafast }
        #   - push_url: rtmp://b.example.com/live/key2
        #     ffmpeg_options: { video_codec: libx264, video_bitrate: 4m, preset: ultrafast }
```

---

## 二、配置模板

> 以下地址均为示例，替换为实际地址即可。
> `{ StreamKey }` 表示随机生成的推流鉴权串，实际为随机字符串。

### 模板 1：监控录像标准（5s 分片 + 回放 + 每日一个 MP4）

监控摄像头 / 需要全天录像回看的场景。每天 00:05 自动把前一天分片合并为
`<归档目录>/<流名>/<流名>-YYYYMMDD.mp4`，TS 只留 26 小时释放空间。

```yaml
publisher:
  path: /live
  camera_01:
    enabled: true
    protocol: ffmpeg
    stream:
      source:
        url: rtsp://cam.example.com:554/stream1
        backup_url: rtsp://cam-bak.example.com:554/stream1
      local_play_urls:
        - protocol: flv
          enabled: true
          flv_ffmpeg_options:
            video_codec: libx264
            video_bitrate: 4m
            audio_codec: aac
            audio_bitrate: 128k
            gop_size: 50
            output_pre_args: [-f, flv]
        - protocol: hls
          enabled: true
          hls_ffmpeg_options:
            output_pre_args: [-f, hls, -hls_time, 5]
          hls_segment_duration: 5
          hls_segment_count: 12
          hls_path: /data/hls/camera_01     # 录像必须指定磁盘目录，/tmp 重启清空
          hls_enable_playback: true
          hls_retention_days: 26h           # TS 留 26h，比一天多 2h 保证归档完整
          ts_filename_template: "{stream}/{date}/{seq}.ts"
          hls_archive_interval: 24h         # 每天 00:05 归档前一天 → 一个 MP4
          hls_archive_retention: 0          # MP4 永久保留；如只留 30 天写 720h
          hls_archive_path: /data/archive
      mode: primary-backup
      receivers:
        primary:
          push_url: rtmp://push.example.com/live/camera01_{ StreamKey }
```

### 模板 2：连续录像 + 滚动归档（每 6 小时一个 MP4）

需要更细粒度录像文件的场景：每 6 小时滚动产出一个
`<流名>-YYYYMMDD-HHMM.mp4`，MP4 保留 30 天，TS 留 12 小时。

```yaml
publisher:
  path: /live
  camera_02:
    enabled: true
    protocol: ffmpeg
    stream:
      source:
        url: rtsp://cam.example.com:554/stream2
      local_play_urls:
        - protocol: flv
          enabled: true
          flv_ffmpeg_options:
            video_codec: copy
            audio_codec: copy
            output_pre_args: [-f, flv]
        - protocol: hls
          enabled: true
          hls_ffmpeg_options:
            output_pre_args: [-f, hls, -hls_time, 10]
          hls_segment_duration: 10
          hls_segment_count: 6
          hls_path: /data/hls/camera_02
          hls_enable_playback: true
          hls_retention_days: 12h
          hls_archive_interval: 6h          # 每 6 小时滚动归档一个 MP4
          hls_archive_retention: 720h       # MP4 保留 30 天
          hls_archive_path: /data/archive
      mode: primary-backup
      receivers:
        primary:
          push_url: rtmp://push.example.com/live/camera02_{ StreamKey }
```

### 模板 3：低延迟直播预览（不录像）

只做本地观看 / 转推，延迟优先。2 秒分片 + 小直播窗口，不开启回放与归档，
分片留在默认 `/tmp`（重启自动清空，不占长期空间）。

```yaml
publisher:
  path: /live
  cctv1:
    enabled: true
    protocol: ffmpeg
    stream:
      source:
        url: http://src.example.com/live/cctv1.m3u8
      local_play_urls:
        - protocol: flv
          enabled: true
          flv_ffmpeg_options:
            video_codec: copy
            audio_codec: copy
            output_pre_args: [-f, flv]
        - protocol: hls
          enabled: true
          hls_ffmpeg_options:
            output_pre_args: [-f, hls, -hls_time, 2]
          hls_segment_duration: 2
          hls_segment_count: 6
      mode: primary-backup
      receivers:
        primary:
          push_url: rtmp://push.example.com/live/cctv1_{ StreamKey }
```

### 模板 4：低存储方案（TS 6 小时 + 每日归档 MP4）

磁盘紧张、只想保留"每天一个 MP4"的场景。TS 只留 6 小时，仍每天归档一次
（注意：归档只覆盖归档时刻往前 24h 内仍在磁盘的分片，TS 保留期短于 24h 时
凌晨归档只含最后 6h 内容；需要完整全天 MP4 请用模板 1 的 26h 保留期）。

```yaml
publisher:
  path: /live
  camera_03:
    enabled: true
    protocol: ffmpeg
    stream:
      source:
        url: rtsp://cam.example.com:554/stream3
      local_play_urls:
        - protocol: flv
          enabled: true
          flv_ffmpeg_options:
            video_codec: copy
            audio_codec: copy
            output_pre_args: [-f, flv]
        - protocol: hls
          enabled: true
          hls_ffmpeg_options:
            output_pre_args: [-f, hls, -hls_time, 10]
          hls_segment_duration: 10
          hls_segment_count: 6
          hls_path: /data/hls/camera_03
          hls_enable_playback: true
          hls_retention_days: 6h            # TS 6 小时后删除
          hls_archive_interval: 24h
          hls_archive_retention: 720h       # MP4 留 30 天
          hls_archive_path: /data/archive
      mode: primary-backup
      receivers:
        primary:
          push_url: rtmp://push.example.com/live/camera03_{ StreamKey }
```

### 模板 5：多平台同时推流（all 模式）

一路源同时推多个 RTMP 平台，每路可设独立编码参数（如主平台高清、
备用平台低码率）。本地 FLV/HLS 播放照常可用。

```yaml
publisher:
  path: /live
  live_show:
    enabled: true
    protocol: ffmpeg
    stream:
      source:
        url: rtmp://src.example.com/live/show
      local_play_urls:
        - protocol: flv
          enabled: true
          flv_ffmpeg_options:
            video_codec: libx264
            video_bitrate: 8m
            preset: ultrafast
            output_pre_args: [-f, flv]
        - protocol: hls
          enabled: true
          hls_ffmpeg_options:
            output_pre_args: [-f, hls, -hls_time, 6]
          hls_segment_duration: 6
          hls_segment_count: 6
      mode: all
      receivers:
        all:
          - push_url: rtmp://a.example.com/live/show_{ StreamKey }
            play_urls:
              flv: http://a.example.com/live/show
            ffmpeg_options:
              video_codec: libx264
              video_bitrate: 8m
              preset: ultrafast
          - push_url: rtmp://b.example.com/live/show_{ StreamKey }
            ffmpeg_options:
              video_codec: libx264
              video_bitrate: 4m
              preset: ultrafast
          - push_url: rtmp://c.example.com/live/show_{ StreamKey }
            ffmpeg_options:
              video_codec: copy
```

### 模板 6：综合场景示例（双流：all 多平台 + 主备回放）

两路典型推流配置，可直接对照修改（地址均为示例）：

**cctv1 — all 模式**：局域网源同时推云端直播平台（8m 高清）和局域网
Nginx-RTMP（4m 低码率），本地 FLV/HLS 直播可用，不录像。

```yaml
publisher:
  path: /live
  cctv1:
    enabled: true
    protocol: ffmpeg
    streamkey:
      type: random
      length: 32
      expiration: 24h
    stream:
      source:
        type: http
        url: http://iptv.example.com/live/source1.php?id=cctv13
        backup_url: http://iptv.example.com/live/source2.php?id=cctv1&cdn=5
        ffmpeg_options:
          input_pre_args: [-re]
      local_play_urls:
        - protocol: flv
          enabled: true
          flv_ffmpeg_options:
            video_codec: libx264
            video_bitrate: 8m
            audio_codec: aac
            audio_bitrate: 128k
            gop_size: 50
            output_pre_args: [-f, flv]
        - protocol: hls
          enabled: true
          hls_ffmpeg_options:
            output_pre_args: [-f, hls, -hls_time, 6]
      mode: all
      receivers:
        all:
          - push_url: rtmp://push.example.com/live/
            play_urls:
              flv: http://play.example.com/live/
            ffmpeg_options:
              video_codec: libx264
              video_bitrate: 8m
              preset: ultrafast
          - push_url: rtmp://192.0.2.186/live/
            play_urls:
              flv: http://192.0.2.186:8080/live/
            ffmpeg_options:
              video_codec: libx264
              video_bitrate: 4m
              preset: ultrafast
```

**cctv2 — primary-backup 模式 + 回放**：主备双源，主推云端平台、备推局域网，
本地 HLS 开启 5 秒分片回放（TS 留 24 小时），`streamkey` 用 fixed 固定值
（拉流鉴权）。

```yaml
publisher:
  path: /live
  cctv2:
    enabled: true
    protocol: ffmpeg
    streamkey:
      type: fixed
      value: your_fixed_stream_key
    stream:
      source:
        type: http
        url: http://iptv.example.com/live/source2.php?id=cctv2&cdn=5
        backup_url: http://iptv.example.com/live/source1.php?id=cctv2
        ffmpeg_options:
          input_pre_args: [-re]
      local_play_urls:
        - protocol: hls
          enabled: true
          hls_segment_duration: 5
          hls_segment_count: 5
          hls_enable_playback: true
          hls_retention_days: 24h
          ts_filename_template: camera_hls
        - protocol: flv
          enabled: true
      mode: primary-backup
      receivers:
        primary:
          push_url: rtmp://push.example.com/live/
          play_urls:
            flv: http://play.example.com/live/
          ffmpeg_options:
            video_codec: libx264
            video_bitrate: 8m
            preset: ultrafast
        backup:
          push_url: rtmp://192.0.2.186/live/
          play_urls:
            flv: http://192.0.2.186:8080/live/
          ffmpeg_options:
            video_codec: libx264
            video_bitrate: 8m
            preset: ultrafast
```

要点说明：

- `streamkey` 作用于**本地拉流鉴权**（访问 `/live/play/<name>.*` 时校验），
  与推流平台的推流码是两回事；不填则本地播放不校验。
- cctv2 的 `ts_filename_template: camera_hls` 表示分片名形如
  `<流名>-YYYYMMDD-HHMMSS-hls.ts`，回放/归档按分片创建时间筛选，与模板名无关。
- 源 `ffmpeg_options.input_pre_args: [-re]` 以源帧速率读取输入，
  适合文件/慢速源，直播源一般可省略。

---

## 三、访问地址

设 `publisher.path: /live`、流名 `cctv1`，服务端口 8888：

| 用途 | 地址 |
|---|---|
| 本地 FLV 播放 | `http://127.0.0.1:8888/live/play/cctv1.flv` |
| 本地 HLS 直播 | `http://127.0.0.1:8888/live/play/cctv1.m3u8` |
| HLS 时间段回放 | `http://127.0.0.1:8888/live/play/cctv1.m3u8?playseek=20260904100000-20260904110000` |
| 独立播放页（可分享） | `http://127.0.0.1:8888/pp?live=<上述地址 URL 编码>` |

- `playseek` 格式为 **14 位本地时间 `YYYYMMDDHHMMSS-YYYYMMDDHHMMSS`**
  （开始-结束，时区按 Asia/Shanghai），仅开启 `hls_enable_playback` 时可用。
- 后台「推流发布」页卡片上有「播放 / 回放 / 复制」按钮，回放面板可选起止时间，
  自动拼好上述地址并在独立播放页打开。

---

## 四、录制与归档说明

| 字段 | 说明 |
|---|---|
| `hls_path` | TS 分片目录。默认 `/tmp/hls/<流名>`，**重启会清空**；录像 / 归档必须指定磁盘目录 |
| `hls_segment_duration` | 分片时长（秒）。监控录像建议 5~10s，低延迟预览 2s |
| `hls_segment_count` | 直播列表保留的分片数（直播窗口） |
| `hls_enable_playback` | 开启后卡片显示「回放」按钮，可按时间段点播历史 TS |
| `hls_retention_days` | TS 保留期（如 `26h`、`12h`），留空永久 |
| `ts_filename_template` | 分片文件名模板，如 `{stream}/{date}/{seq}.ts` |
| `hls_archive_interval` | 归档间隔：`24h` 及以上 = 每天 00:05 归档前一天为一个 MP4；`6h` 等 = 每 6 小时滚动归档；**留空不归档** |
| `hls_archive_retention` | 归档 MP4 保留期（如 `720h` = 30 天），留空 / 0 = 永久 |
| `hls_archive_path` | 归档目录，默认 `./archive`；产出 `<目录>/<流名>/<流名>-YYYYMMDD.mp4`（每日）或 `<流名>-YYYYMMDD-HHMM.mp4`（滚动） |

注意事项：

1. **归档间隔 ≤ TS 保留期**：归档依赖磁盘上的 TS 分片，`hls_retention_days`
   短于归档间隔时，合并出的 MP4 会缺段（如 TS 只留 6h，凌晨归档仅含最后 6h）。
2. 归档使用 ffmpeg `-c copy -faststart` 零转码合并，CPU 占用极低；
   归档失败会删除不完整输出，等下一轮重试。
3. 存储估算参考：8 Mbps ≈ 3.5 GB/小时 ≈ 80 GB/天/路。录某天就删 TS、只留
   MP4 是省空间的主要手段（模板 1）。

---

## 五、运行状态与故障排查

后台「推流发布」页每张卡片实时显示：运行状态（PID）、码率（瞬时/平均）、CPU、
内存、累计输出、运行时长、重启次数。

- **主备切换**：主源失败自动切备源，切换时会重启本地 HLS 与推流进程
  （新流时间戳/编码参数与旧流不连续，不重启会黑屏花屏）。
- **推流失败**：卡片「运行状态」下方会显示 ffmpeg 退出前的最后几行 stderr
  （如 RTMP 握手失败、源 404），按提示检查推流地址与鉴权 key。
- **主动关闭**显示为正常"未运行"，不会出现 `signal: killed` 错误状态。
