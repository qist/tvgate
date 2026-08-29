//go:build android || tzdata

// logger/tzdata_embed.go
// 内嵌 IANA 时区库（time/tzdata）。
// - 安卓：无系统 /usr/share/zoneinfo，必须内嵌，供 time.LoadLocation 兜底
//   （phpgo 的 date() 等函数依赖它）
// - 其他平台（Windows/macOS/普通 Linux/官方 Docker 镜像已装 tzdata）：
//   原生可拿到时区，默认不内嵌以减小二进制体积；
//   若部署在无 zoneinfo 的精简镜像，可用 -tags tzdata 强制内嵌。
package logger

import _ "time/tzdata"
