//go:build !pprof
// +build !pprof

package server

import "net/http"

// registerPprofMux 发行版空实现：pprof 端点不注册，零开销不暴露。
func registerPprofMux(mux *http.ServeMux) {}
