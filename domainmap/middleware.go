package domainmap

import (
	"net/http"
)

// Middleware 域名映射中间件
type Middleware struct {
	mapper *DomainMapper
	next   http.Handler
}

// NewMiddleware 创建新的域名映射中间件
// 注意：在当前实现中，我们使用 Router 来处理域名映射逻辑，
// 这个中间件主要用于需要手动将域名映射功能集成到处理链中的场景
func NewMiddleware(mapper *DomainMapper, next http.Handler) *Middleware {
	return &Middleware{
		mapper: mapper,
		next:   next,
	}
}

// ServeHTTP 实现http.Handler接口
func (m *Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 直接调用下一个处理器，因为域名映射逻辑现在在DomainMapper中完整实现
	m.next.ServeHTTP(w, r)
}