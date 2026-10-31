module github.com/qist/tvgate

go 1.26.0

// CVE-2026-98432: 全局默认关闭 MPTCP（监听与拨号均回落普通 TCP），
// 覆盖标准库及第三方库内部创建的 ListenConfig/Dialer。
godebug multipathtcp=0

require (
	github.com/ameshkov/dnscrypt/v2 v2.4.0
	github.com/andybalholm/brotli v1.2.7
	github.com/asticode/go-astits v1.16.0
	github.com/bluenviron/gortsplib/v5 v5.6.7
	github.com/bluenviron/mediacommon/v2 v2.9.7
	github.com/cloudflare/tableflip v1.2.3
	github.com/dlclark/regexp2 v1.12.0
	github.com/fsnotify/fsnotify v1.10.1
	github.com/jedisct1/go-dnsstamps v0.0.0-20240423203910-07a0735c7774
	github.com/miekg/dns v1.1.73
	github.com/pion/rtp v1.10.6
	github.com/quic-go/quic-go v0.64.1
	github.com/shirou/gopsutil/v3 v3.24.5
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.61.0
	golang.org/x/sync v0.24.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
	h12.io/socks v1.0.3
)

require (
	github.com/AdguardTeam/golibs v0.32.7 // indirect
	github.com/ameshkov/dnsstamps v1.0.3 // indirect
	github.com/asticode/go-astikit v0.30.0 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.19 // indirect
	github.com/pion/sdp/v3 v3.0.20 // indirect
	github.com/pion/srtp/v3 v3.1.3 // indirect
	github.com/pion/transport/v5 v5.1.1 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/tklauser/go-sysconf v0.3.15 // indirect
	github.com/tklauser/numcpus v0.10.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/exp v0.0.0-20250305212735-054e65f0b394 // indirect
)
