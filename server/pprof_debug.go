//go:build pprof
// +build pprof

package server

import (
	"net/http"
	"net/http/pprof"
)

// registerPprofMux 开启 /debug/pprof 性能分析端点。
// 仅在 -tags pprof 构建时编译进 binary；发行版不携带此文件的效果，
// 即 pprof 端点完全不存在于内存中，既无开销也无泄露风险。
//
// 使用示例：
//
//	go build -tags pprof -o tvgate_pprof .
//	go tool pprof http://127.0.0.1:8888/debug/pprof/heap
//	go tool pprof http://127.0.0.1:8888/debug/pprof/profile?seconds=30
func registerPprofMux(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}
