import type { Locale } from "../lib/locale";

type TranslationDict = Record<string, string>;

// 英文基准词典：组件一律以字符串 key 取词。key 若要改名，必须同步本文件三张语言表
// 与全部 t() 调用点，否则漏改处会在界面上直接暴露 key 原文。
const base: TranslationDict = {
  // 页面标题与头部区域
  title: "TVGate Player",
  error: "Error",
  retry: "Retry",

  // 头部操作
  goLive: "Back to Live",

  // 侧边栏页签
  channels: "Channels",
  programGuide: "Schedule",

  // 频道列表
  searchChannels: "Search for a channel...",
  allChannels: "All",
  channelGroups: "Groups",
  catchup: "Catchup",
  catchupSupported: "Supports catchup",

  // EPG 视图
  noEpgAvailable: "There is no EPG data for this channel yet",
  epgNotConfigured: "The server has no EPG source set up (player.epg is empty)",
  onAir: "On Air",
  replay: "Replay",
  nowPlaying: "Now Playing",
  excellentProgram: "Featured program",

  // 视频播放器
  selectChannelToWatch: "Pick a channel to start viewing",
  loadingVideo: "Loading...",
  audioOnlyChannel: "Audio-only channel",
  playbackError: "Playback issue",
  clickToPlay: "Click here to play",
  autoplayBlocked: "The browser blocks autoplay until you interact with the page",
  playingInPictureInPicture: "Playback continues in the Picture-in-Picture window",

  // 错误类文案
  failedToLoadPlaylist: "Could not load the playlist",
  emptyPlaylist: "This playlist contains no channels that can be played",
  playlistLoadEyebrow: "M3U playlist",
  playlistLoadTitle: "The playlist hasn't finished loading",
  playlistLoadDescription:
    "Channel data comes from the /api/player/channels endpoint, which currently returns no usable list. Review your player subscription settings, then try again.",
  playlistErrorChecklist: "Things to check in your M3U setup",
  playlistErrorHintReachable: "Verify that TVGate is able to reach the external M3U address.",
  playlistErrorHintFormat: "Check that the #EXTINF entries and channel links in the playlist are well-formed.",
  m3uIntegrationGuide: "Open the M3U configuration guide",
  playlistEndpoint: "Playlist API endpoint",
  technicalDetails: "Technical information",
  noCatchupSupport: "Catchup playback is unavailable on this channel",
  noRewindSupport: "Rewinding is not available for this channel",
  codecError: "The stream uses a video/audio codec this browser is unable to decode.",
  audioCodecError: "The audio codec is not supported here, so the video keeps playing in silence.",
  videoCodecError: "The video codec (e.g. HEVC/4K) is unsupported, so only the audio track is played.",
  dismiss: "Dismiss",
  mseNotSupported: "MSE (Media Source Extensions) is missing in this browser",
  failedToPlay: "Playback failed",
  upstreamRequestFailed: "Failed to fetch the upstream stream",
  upstreamRequestFailedDescription:
    "TVGate could not deliver this stream to the player — either the server is down or it responded with a non-successful HTTP status.",
  httpStatus: "HTTP status",
  requestUrl: "Request URL",
  suggestedAction: "Suggested checks",
  upstreamRequestFailedSuggestion:
    "First confirm the upstream service responds and that the channel URL and its credentials are valid; then go through the TVGate logs and try again.",

  // 时移按钮（数值与单位为固定写法）
  rewind30m: "-30m",
  rewind1h: "-1h",
  rewind3h: "-3h",

  // 时间单位（供读屏与无障碍场景）
  minutes: "min",

  // 进度条
  live: "LIVE",
  seekTo: "Jump to a position in the stream",

  // 播放器控制按钮
  play: "Play",
  pause: "Pause",
  mute: "Mute",
  unmute: "Unmute",
  previousChannel: "Go to previous channel",
  nextChannel: "Go to next channel",
  fullscreen: "Fullscreen",
  exitFullscreen: "Leave fullscreen",
  pictureInPicture: "Picture in Picture",

  // 相对日期
  today: "Today",
  yesterday: "Yesterday",
  tomorrow: "Tomorrow",
  dayBeforeYesterday: "2 days ago",

  // 媒体信息（键序与中文表刻意不同：各语言表按各自阅读习惯维护，键集合保持一致）
  mediaInfoLabel: "Stream info",
  nativePlayback: "Native playback",
  nativePlaybackHint:
    "This browser cannot run the built-in remux pipeline (MSE), so the browser's own player is used — resolution / frame-rate / codec info is unavailable.",
  mediaInfoChannelCount: "channels",
  mediaInfoSingleChannel: "Mono",
  mediaInfoStereoSound: "Stereo",
  mediaInfoSoundChannels: "Audio channels",
  mediaInfoVideoCoding: "Video codec",
  mediaInfoDimensions: "Resolution",
  mediaInfoFps: "Frame rate",
  mediaInfoHdrRange: "Dynamic range",
  mediaInfoAudioCoding: "Audio codec",
  mediaInfoNominalBitrate: "Declared bitrate",
  mediaInfoObservedBitrate: "Observed bitrate",

  // 线路切换
  source: "Source",
  sourceFallback: "Switching to the next source...",

  // 设置面板
  settings: "Settings",
  language: "Language",
  theme: "Theme",
  themeAuto: "Auto",
  themeLight: "Light",
  themeDark: "Dark",
  appearance: "Look and feel",
  appearanceOcean: "Ocean",
  appearanceEmerald: "Emerald",
  appearanceSunset: "Sunset",
  appearanceRose: "Rose",
  appearanceAmber: "Amber",
  appearanceSlate: "Slate",
  panelAlpha40: "40%",
  panelAlpha: "Panel background opacity",
  panelAlpha100: "Opaque",
  panelAlpha85: "85%",
  panelAlpha70: "70%",
  panelAlpha55: "55%",
  pictureInPictureMode: "PiP mode",
  pictureInPictureModeFull: "Full",
  pictureInPictureModeSimple: "Compact",
  seamlessSwitch: "Seamless channel change",
  resolutionLimitHint: "The settings below only take effect at ≤1080p",
  deinterlace: "Auto deinterlace",
  pictureEnhancement: "Picture enhancements",
  audioChannelMode: "Audio output",
  audioChannelModeStereo: "Stereo",
  audioChannelModeMono: "Downmix to mono",
};

const zhHans: TranslationDict = {
  // 页面标题与头部区域
  title: "TVGate 播放器",
  error: "错误",
  retry: "重试",

  // 头部操作
  goLive: "回到直播",

  // 侧边栏页签
  channels: "频道",
  programGuide: "节目指南",

  // 频道列表
  searchChannels: "输入关键词搜索频道...",
  allChannels: "全部",
  channelGroups: "分组",
  catchup: "回看",
  catchupSupported: "可回看",

  // EPG 视图
  noEpgAvailable: "该频道暂时没有 EPG 数据",
  epgNotConfigured: "服务器上没有配置 EPG 来源（player.epg 为空）",
  onAir: "直播中",
  replay: "回放",
  nowPlaying: "正在播放",
  excellentProgram: "精选节目",

  // 视频播放器
  selectChannelToWatch: "先挑一个频道，即可开始观看",
  loadingVideo: "加载中...",
  audioOnlyChannel: "纯音频频道（广播）",
  playbackError: "播放出现问题",
  clickToPlay: "点击开始播放",
  autoplayBlocked: "浏览器限制了自动播放，请先与页面进行交互",
  playingInPictureInPicture: "画面已在画中画小窗中继续播放",

  // 错误类文案
  failedToLoadPlaylist: "播放列表加载失败",
  emptyPlaylist: "这份播放列表里找不到可播放的频道",
  playlistLoadEyebrow: "M3U 播放列表",
  playlistLoadTitle: "播放列表尚未就绪",
  playlistLoadDescription:
    "频道数据来自 /api/player/channels 这个接口，目前没有返回可用列表。请先核对播放器订阅设置，再重新尝试。",
  playlistErrorChecklist: "M3U 配置排查清单",
  playlistErrorHintReachable: "确认 TVGate 能够访问外部的 M3U 地址。",
  playlistErrorHintFormat: "检查列表里的 #EXTINF 条目与频道链接是否格式正确。",
  m3uIntegrationGuide: "打开 M3U 接入指南",
  playlistEndpoint: "播放列表接口",
  technicalDetails: "技术细节",
  noCatchupSupport: "该频道无法进行回看",
  noRewindSupport: "该频道无法进行时移",
  codecError: "视频/音频编码不受支持，当前浏览器无法解码这条流。",
  audioCodecError: "音频编码不被当前浏览器支持，画面将继续播放但没有声音。",
  videoCodecError: "当前浏览器不支持该视频编码（如 HEVC/4K），因此只会输出声音。",
  dismiss: "关闭",
  mseNotSupported: "当前浏览器缺少 MSE (媒体源扩展) 支持",
  failedToPlay: "播放失败",
  upstreamRequestFailed: "拉取上游视频流失败",
  upstreamRequestFailedDescription:
    "TVGate 未能把这条视频流送到播放器，原因可能是服务端不可用，或返回了非正常的 HTTP 响应。",
  httpStatus: "HTTP 状态",
  requestUrl: "请求地址",
  suggestedAction: "排查建议",
  upstreamRequestFailedSuggestion:
    "先确认上游服务可以访问，并核对频道地址与鉴权参数；再到 TVGate 日志里查看上游转发报错，处理后再重新尝试。",

  // 时移按钮（数值与单位为固定写法）
  rewind30m: "-30分钟",
  rewind1h: "-1小时",
  rewind3h: "-3小时",

  // 时间单位
  minutes: "分钟",

  // 相对日期（与时间单位同族，移到一起便于对照维护）
  today: "今天",
  yesterday: "昨天",
  tomorrow: "明天",
  dayBeforeYesterday: "前天",

  // 进度条
  live: "直播",
  seekTo: "跳转到指定时间点",

  // 播放器控制按钮
  play: "播放",
  pause: "暂停",
  mute: "静音",
  unmute: "取消静音",
  previousChannel: "切换到上一频道",
  nextChannel: "切换到下一频道",
  fullscreen: "全屏",
  exitFullscreen: "退出全屏",
  pictureInPicture: "画中画",

  // 媒体信息（键序与英文表刻意不同；键集合与 base 保持一致）
  mediaInfoLabel: "流信息",
  nativePlayback: "原生播放",
  nativePlaybackHint:
    "当前浏览器不支持内置转封装（MSE），已改用浏览器原生播放：分辨率 / 帧率 / 编码信息不可用。",
  mediaInfoVideoCoding: "视频编码",
  mediaInfoAudioCoding: "音频编码",
  mediaInfoDimensions: "分辨率",
  mediaInfoFps: "帧率",
  mediaInfoHdrRange: "动态范围",
  mediaInfoSoundChannels: "音频声道",
  mediaInfoSingleChannel: "单声道",
  mediaInfoStereoSound: "立体声",
  mediaInfoChannelCount: "声道",
  mediaInfoNominalBitrate: "标称码率",
  mediaInfoObservedBitrate: "实测码率",

  // 线路切换
  source: "线路",
  sourceFallback: "正在切换到下一条线路...",

  // 设置面板
  settings: "设置",
  language: "语言",
  theme: "主题",
  themeAuto: "自动",
  themeLight: "浅色",
  themeDark: "深色",
  appearance: "外观风格",
  appearanceOcean: "深海",
  appearanceEmerald: "翡翠",
  appearanceSunset: "落日",
  appearanceRose: "玫红",
  appearanceAmber: "琥珀",
  appearanceSlate: "石墨",
  panelAlpha40: "40%",
  panelAlpha: "面板不透明度",
  panelAlpha100: "不透明",
  panelAlpha85: "85%",
  panelAlpha70: "70%",
  panelAlpha55: "55%",
  pictureInPictureMode: "画中画模式",
  pictureInPictureModeFull: "完整",
  pictureInPictureModeSimple: "简洁",
  seamlessSwitch: "无感换台",
  resolutionLimitHint: "以下设置只对 ≤1080p 的场景生效",
  deinterlace: "自动去隔行",
  pictureEnhancement: "画面增强",
  audioChannelMode: "声音输出",
  audioChannelModeStereo: "立体声",
  audioChannelModeMono: "合成为单声道",
};

// 繁體中文（偏港台用語習慣）
const zhHant: TranslationDict = {
  // 頁面標題與頭部區域
  title: "TVGate - 播放器",
  error: "錯誤",
  retry: "重試",

  // 頭部操作
  goLive: "回到直播",

  // 側邊欄頁籤
  channels: "頻道",
  programGuide: "節目指南",

  // 頻道列表
  searchChannels: "輸入關鍵字搜尋頻道...",
  allChannels: "全部",
  channelGroups: "分組",
  catchup: "回看",
  catchupSupported: "可回看",

  // EPG 視圖
  noEpgAvailable: "該頻道暫時沒有 EPG 資料",
  epgNotConfigured: "伺服器上沒有設定 EPG 來源（player.epg 為空）",
  onAir: "直播中",
  replay: "重播",
  nowPlaying: "正在播放",
  excellentProgram: "精選節目",

  // 視訊播放器
  selectChannelToWatch: "先挑一個頻道，即可開始觀看",
  loadingVideo: "載入中...",
  audioOnlyChannel: "純音訊頻道（廣播）",
  playbackError: "播放出現問題",
  clickToPlay: "點擊開始播放",
  autoplayBlocked: "瀏覽器限制了自動播放，請先與頁面互動",
  playingInPictureInPicture: "畫面已在畫中畫小窗中繼續播放",

  // 錯誤類文案
  failedToLoadPlaylist: "播放列表載入失敗",
  emptyPlaylist: "這份播放列表裡找不到可播放的頻道",
  playlistLoadEyebrow: "M3U 播放列表",
  playlistLoadTitle: "播放列表還未就緒",
  playlistLoadDescription:
    "頻道資料來自 /api/player/channels 端點，目前沒有回傳可用列表。請先核對播放器訂閱設定，再重新嘗試。",
  playlistErrorChecklist: "M3U 設定排查清單",
  playlistErrorHintReachable: "確認 TVGate 能夠存取外部的 M3U 位址。",
  playlistErrorHintFormat: "檢查列表裡的 #EXTINF 條目與頻道連結是否格式正確。",
  m3uIntegrationGuide: "開啟 M3U 接入指南",
  playlistEndpoint: "播放列表端點",
  technicalDetails: "技術細節",
  noCatchupSupport: "該頻道無法進行回看",
  noRewindSupport: "該頻道無法進行時移",
  codecError: "視訊/音訊編碼不受支援，目前瀏覽器無法解碼這條串流。",
  audioCodecError: "音訊編碼不被目前瀏覽器支援，畫面將繼續播放但沒有聲音。",
  videoCodecError: "目前瀏覽器不支援該視訊編碼（如 HEVC/4K），因此只會輸出聲音。",
  dismiss: "關閉",
  mseNotSupported: "目前瀏覽器缺少 MSE (媒體來源擴充) 支援",
  failedToPlay: "播放失敗",
  upstreamRequestFailed: "拉取上游串流失敗",
  upstreamRequestFailedDescription:
    "TVGate 無法把這條串流送到播放器，原因可能是伺服端不可用，或回傳了非正常的 HTTP 回應。",
  httpStatus: "HTTP 狀態",
  requestUrl: "請求位址",
  suggestedAction: "排查建議",
  upstreamRequestFailedSuggestion:
    "先確認上游服務可以存取，並核對頻道位址與驗證參數；再到 TVGate 日誌裡查看上游轉發錯誤，處理後再重新嘗試。",

  // 時移按鈕（數值與單位為固定寫法）
  rewind30m: "-30分鐘",
  rewind1h: "-1小時",
  rewind3h: "-3小時",

  // 時間單位
  minutes: "分鐘",

  // 相對日期（與時間單位同族，移到一起便於對照維護）
  today: "今天",
  yesterday: "昨天",
  tomorrow: "明天",
  dayBeforeYesterday: "前天",

  // 進度條
  live: "直播",
  seekTo: "跳轉到指定時間點",

  // 播放器控制按鈕
  play: "播放",
  pause: "暫停",
  mute: "靜音",
  unmute: "取消靜音",
  previousChannel: "切換到上一頻道",
  nextChannel: "切換到下一頻道",
  fullscreen: "全螢幕",
  exitFullscreen: "退出全螢幕",
  pictureInPicture: "畫中畫",

  // 線路切換
  source: "線路",
  sourceFallback: "正在切換到下一條線路...",

  // 媒體資訊（鍵序另成一種排布：與英文/簡體表均不同，鍵集合保持一致）
  mediaInfoLabel: "串流資訊",
  nativePlayback: "原生播放",
  nativePlaybackHint:
    "目前瀏覽器不支援內建轉封裝（MSE），已改用瀏覽器原生播放：解析度 / 幀率 / 編碼資訊不可用。",
  mediaInfoFps: "幀率",
  mediaInfoDimensions: "解像度",
  mediaInfoHdrRange: "動態範圍",
  mediaInfoVideoCoding: "視訊編碼",
  mediaInfoAudioCoding: "音訊編碼",
  mediaInfoSoundChannels: "音訊聲道",
  mediaInfoSingleChannel: "單聲道",
  mediaInfoStereoSound: "立體聲",
  mediaInfoChannelCount: "聲道",
  mediaInfoNominalBitrate: "標稱碼率",
  mediaInfoObservedBitrate: "實測碼率",

  // 設定面板
  settings: "設定",
  language: "語言",
  theme: "主題",
  themeAuto: "自動",
  themeLight: "淺色",
  themeDark: "深色",
  appearance: "外觀風格",
  appearanceOcean: "深海",
  appearanceEmerald: "翡翠",
  appearanceSunset: "落日",
  appearanceRose: "玫紅",
  appearanceAmber: "琥珀",
  appearanceSlate: "石墨",
  panelAlpha40: "40%",
  panelAlpha: "面板不透明度",
  panelAlpha100: "不透明",
  panelAlpha85: "85%",
  panelAlpha70: "70%",
  panelAlpha55: "55%",
  pictureInPictureMode: "畫中畫模式",
  pictureInPictureModeFull: "完整",
  pictureInPictureModeSimple: "簡潔",
  seamlessSwitch: "無感換台",
  resolutionLimitHint: "以下設定只對 ≤1080p 的情境生效",
  deinterlace: "自動去交錯",
  pictureEnhancement: "畫面增強",
  audioChannelMode: "聲音輸出",
  audioChannelModeStereo: "立體聲",
  audioChannelModeMono: "合成為單聲道",
};

/** 组装某语言的最终词典：先铺英文基准，再按语言覆盖——漏译词自动回落英文而非空白。 */
function mergeWithBase(overrides: TranslationDict): TranslationDict {
  return { ...base, ...overrides };
}

export const translations: Record<Locale, TranslationDict> = {
  en: base,
  "zh-Hans": mergeWithBase(zhHans),
  "zh-Hant": mergeWithBase(zhHant),
};

/** 全部可用文案 key 的联合；以 base 为准，让 t() 的入参在编译期就非法 key 报错。 */
export type TranslationKey = keyof typeof base;

export function translate(locale: Locale, key: TranslationKey): string {
  // 三级回落：当前语言 → 英文基准 → key 本身；兜底保证界面永远有字符可渲染
  const dict = translations[locale];
  return dict[key] ?? base[key] ?? key;
}
