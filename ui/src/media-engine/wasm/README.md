# media-engine WASM 音频解码

统一软解后端：FFmpeg libavcodec（LGPL-2.1+，无 GPL 组件），一套 WASM 覆盖
**MP2 / MP3 / AC-3 / E-AC-3 / AAC**，取代历史 minimp3(MP2) + ac3-only 的分裂布局。

## 文件
- `avcodec_audio.c` —— 干净房间自写的封装（clean-room）：公开 libavcodec
  `send_packet/receive_frame` 与 `av_parser_parse2`（用各 codec 的 parser 切帧，
  无需手写帧表），内置 carry 保留跨 payload 的不完整帧。
- `wsola.c` —— WSOLA 时间拉伸（`wsola_*` 导出），**本项目自研 clean-room 实现**：
  环形输入缓冲 + 前缀能量归一化互相关选帧 + 升余弦交叉淡化，`|ratio-1|<1e-3` 直通位精确。
  供主线程 PCM 播放器做 live-sync 变速与漂移吸收（与 JS 封装 `audio/wasm-stretcher.ts` 配套，
  ABI 契约见该文件头）。
- `Makefile` —— 构建脚本（emcc → 独立 `.wasm`，无 JS 胶水）。
- 产出：`avcodec_audio.wasm`。

## 构建
按 Makefile 头部注释先裁剪构建 FFmpeg（`--enable-decoder=mp2,mp3,ac3,eac3,aac` +
对应 parser），然后：

    make FFMPEG_LIB_DIR=/opt/ffmpeg-media

> 状态：`avcodec_audio.wasm`（约 746KB）已按此配方用 Emscripten 3.1.64 在
> `/opt/ffmpeg-media`（FFmpeg n7.1.1 裁剪，`--enable-decoder=mp2,mp3,ac3,eac3,aac`
> + `--enable-parser=mpegaudio,ac3,eac3,aac`）构建完成并随仓库交付。
> 环境装配/重建见上方 FFmpeg 步骤；emcc 命令见 Makefile。
> 链接期需补 `__secs_to_zone` musl 打桩（emscripten 独立模式 libc 裁剪，见
> `avcodec_audio.c` 末尾）。
>
> **解码已在开发期用自产 fixture 验证**（48kHz 立体声双音 1s/10s）：5 codec
> 整块与 1KB/768B/333B 分块喂入均无损恢复、0 解码错误、RMS/声道过零率正确。
> 该离线 harness 与 fixture **未随仓库交付**（本地文件，非 .gitignore 排除）；
> 端到端回归可直接用 `ac3-lab`（`ui/ac3-lab.html`，同一份 `audio-sync-core` 与
> 同一条软解链路）播软解频道观察 drift/underrun/重锚。
> 踩坑记录（`avcodec_audio.c` 内注释已逐条锁定）：
> ① FFmpeg parser 会在返回 `consumed=0` 的同时交出其内部缓冲的完整帧（帧恰好
>    结束于本次输入起点）——封装必须在检查 `pkt_size` **之后**才因无进展 break，
>    否则隔帧丢失（AC-3 768B 分块实测丢一半）；
> ② 裁剪构建的 mp2/mp3 默认解码器为**定点版**，输出 S16P——封装须做 s16→float
>    转换，否则帧被静默丢弃（MP2 无声）；
> ③ 流末帧会被 parser 收进内部缓冲（对调用方报告已消耗），只有 `input_len=0`
>    的 EOF flush 调用能取回，且 carry=0 时也必须执行 flush。
>
> 本机重建补充：`/opt/emsdk/upstream` 的 `emscripten_config` 有损坏符号，需自备
> config（LLVM_ROOT 指 `upstream/bin`，NODE_JS 指本机 node）并
> `export EM_CONFIG=<config> PATH=/opt/emsdk/upstream/emscripten:/opt/emsdk/upstream/bin:$PATH`，
> 然后 `make FFMPEG_LIB_DIR=/opt/ffmpeg-media`。

## ABI（导出）
    me_decoder_create(codec_id)     codec_id: 0=ac3 1=eac3 2=mp2 3=mp3 4=aac
    me_decoder_destroy(ptr)
    me_decoder_reset(ptr)
    me_max_samples_per_frame()
    me_decode_payload(ptr, in, inLen, outF32, outCapFloats, info[8]) -> samples
    wsola_create(sampleRate, channels) / wsola_* (本地增强，见 wsola.c)
输出为立体声交织 float32；AC-3/E-AC-3 内部经 "downmix" 选项做 5.1→stereo。
info[6] 为本批输出中起始于本次输入之前（跨 payload 拼帧）的样本数/声道，
供上层按 PTS 精确外推（历史 ac3_decoder_* ABI 同款语义）。

调用约定：软解路径（MP2/AC-3/E-AC-3）在 `demux/ts-demuxer.ts` 已按帧头切分，
**一帧一调**（跨 PES 的半帧由 demuxer 的 carry 拼完再送）；WASM 侧 parser 可能压住
一帧再交出，`info[6]` 会如实上报，上层 `worker/pipeline.ts` 用它把标签回退到帧起点，
逐帧标签因此仍然精确。整段 payload 喂入同样可用（parser + carry 自洽），
两种喂法共用同一份 info 语义。

TS 侧加载器：`../../decoder/avcodec-audio-decoder.ts`（独立 WASM import 打桩 +
me_* ABI 驱动，同一模块同时供 Worker 解码与主线程 wsola 拉伸）。

## 5.1 直通（本地实现，2026-09-16）

上游封装在 `me_decoder_create` 里对 AC-3/E-AC-3 设 `downmix=stereo`，并恒定 `out_ch = 2`
（多声道在 WASM 内被提前塌成 2.0）。本项目改为**保留源声道直通**：

- **默认**不设置 `downmix` AVOption（5.1 直通）；仅当宿主显式声明目标 2.0 时才设
  （设备只能立体声时，由解码器内部做标准降混，比取前两声道正确）。
- ABI：`me_decoder_create(codec_id, out_channels)` —— `out_channels = 0` 直通源声道（默认），
  `2` 强制 2.0（AC-3/E-AC-3 走内部 downmix），其余按 `min(源声道, out_channels)` 截断。
  主线程 `detectAudioOutputChannels()` 读 `AudioContext.destination.maxChannelCount`
  （≥6 → 6，其余含能力不可知 → 2），经 worker `load.pcmOutputChannels` 传到解码器；
  PCM 播放器另有一层兜底降混（`audio/output-channels.ts` 的 `downmixInterleaved`）。
- 输出声道数 = 源声道数，上限 `ME_MAX_OUT_CHANNELS = 6`（5.1 = 6ch；7.1 取前 6 声道）；
  `info[2]` 上报实际输出声道数，同一 payload 由首个解码帧固定，避免跨帧错位。
- `wsola.c`：`MAX_CHANNELS` 2 → 6（否则 6 声道时 `wsola_create` 失败 → 软解音频整体不可用）。
- `decoder/ffmpeg-bridge.ts`：`MAX_INTERLEAVED_PER_FRAME` 2048×2 → 2048×6（输出缓冲按 6 声道估容）。
- 回归用例：`decoder/ffmpeg-bridge.wasm.test.ts`「AC-3 5.1 直通」+ 夹具
  `decoder/testdata/tone-48k-5.1ch.ac3`（6 声道 48kHz 2s，各声道 440/554/659/60/1046/1318Hz）。

上游其余改动已随本次同步并入：`info[6] = samplesBeforeInput`、溢出判据单位修正
（`(*total_samples + nb) * out_ch`）、`me_reset` 重置 `sample_rate`/`out_channels`、
`__secs_to_zone` 标 weak。重建方式见 `Makefile` 顶部注释（需可用 `EM_CONFIG` + `/opt/ffmpeg-media` 裁剪静态库）。

## 许可义务
FFmpeg 为 LGPL：以独立 .wasm 资源分发（等同动态链接），分发须附 FFmpeg 源码获取途径
（见 Makefile 头部注释）。本封装仅调用其公共 API。
