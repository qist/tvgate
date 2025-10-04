package publisher

// Config represents the publisher configuration
type Config struct {
	Path    string              `yaml:"path"`
	Streams map[string]*Stream  `yaml:",inline,omitempty"`
}

// Stream represents a single stream configuration
type Stream struct {
	BufferSize    int            `yaml:"buffer_size,omitempty"`
	Protocol      string         `yaml:"protocol"`
	Enabled       bool           `yaml:"enabled"`
	StreamKey     StreamKey      `yaml:"streamkey,omitempty"`
	Stream        StreamConfig   `yaml:"stream"`
	FFmpegOptions *FFmpegOptions `yaml:"ffmpeg_options,omitempty"`
}

// StreamKey represents the stream key configuration
type StreamKey struct {
	Type   string `yaml:"type"`   // "random" or "fixed"
	Value  string `yaml:"value"`  // for fixed type
	Length int    `yaml:"length"` // for random type
}

// FFmpegOptions represents flexible ffmpeg options configuration
type FFmpegOptions struct {
	GlobalArgs     []string       `yaml:"global_args,omitempty"`      // 全局参数
	InputPreArgs   []string       `yaml:"input_pre_args,omitempty"`   // 输入前参数
	InputPostArgs  []string       `yaml:"input_post_args,omitempty"`  // 输入后参数
	Filters        *FilterOptions `yaml:"filters,omitempty"`          // 滤镜配置
	VideoCodec     string         `yaml:"video_codec,omitempty"`      // 视频编码器
	AudioCodec     string         `yaml:"audio_codec,omitempty"`      // 音频编码器
	VideoBitrate   string         `yaml:"video_bitrate,omitempty"`    // 视频码率
	AudioBitrate   string         `yaml:"audio_bitrate,omitempty"`    // 音频码率
	Preset         string         `yaml:"preset,omitempty"`           // 编码预设
	CRF            int            `yaml:"crf,omitempty"`              // CRF值
	OutputFormat   string         `yaml:"output_format,omitempty"`    // 封装格式
	OutputPreArgs  []string       `yaml:"output_pre_args,omitempty"`  // 输出前参数
	OutputPostArgs []string       `yaml:"output_post_args,omitempty"` // 输出后参数
	CustomArgs     []string       `yaml:"custom_args,omitempty"`      // 自定义参数
}

// FilterOptions represents video and audio filter configurations
type FilterOptions struct {
	VideoFilters []string `yaml:"video_filters,omitempty"` // 视频滤镜链
	AudioFilters []string `yaml:"audio_filters,omitempty"` // 音频滤镜链
}

// StreamConfig represents stream source configuration
type StreamConfig struct {
	Source        Source      `yaml:"source"`
	LocalPlayUrls PlayUrls    `yaml:"local_play_urls"`
	Mode          string      `yaml:"mode"` // "primary-backup" or "all"
	Receivers     Receivers   `yaml:"receivers"`
}

// Source represents the source stream configuration
type Source struct {
	Type      string            `yaml:"type,omitempty"`
	URL       string            `yaml:"url"`
	BackupURL string            `yaml:"backup_url,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
}

// PlayUrls represents play URLs for different protocols
type PlayUrls struct {
	Flv string `yaml:"flv,omitempty"`
	Hls string `yaml:"hls,omitempty"`
}

// Receivers represents either a primary-backup or all receiver configuration
type Receivers struct {
	Primary *Receiver `yaml:"primary,omitempty"`
	Backup  *Receiver `yaml:"backup,omitempty"`
	All     []Receiver `yaml:"all,omitempty"`
}

// Receiver represents a receiver configuration
type Receiver struct {
	PushURL       string   `yaml:"push_url"`
	PlayUrls      PlayUrls `yaml:"play_urls"`
	PushPreArgs   []string `yaml:"push_pre_args,omitempty"`   // 推流前参数
	PushPostArgs  []string `yaml:"push_post_args,omitempty"`  // 推流后参数
}