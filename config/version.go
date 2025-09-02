package config

import (
	_ "embed"
	"strings"
)

//go:embed version
var versionFile string

// Version 是程序版本号，默认从 version 文件读取
// 可以在编译时通过 -ldflags 覆盖，例如:
// go build -ldflags "-X 'github.com/qist/tvgate/config.Version=v2.0'" .
var Version = ""

func init() {
	// 如果 Version 没被 ldflags 覆盖，则用 embed 文件内容
	if Version == "" {
		Version = strings.TrimSpace(versionFile)
	}
}
