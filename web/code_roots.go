package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/qist/tvgate/config"
)

// codeRootEntry 代码文件管理器可浏览的根目录。
type codeRootEntry struct {
	Index int    `json:"index"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

// codeRoots 返回代码管理器可用的根目录：0 = PHP docroot，其后为 php.allowed_roots。
func (h *ConfigHandler) codeRoots() []codeRootEntry {
	config.CfgMu.RLock()
	docroot := config.Cfg.PHP.DocRoot
	extra := append([]string(nil), config.Cfg.PHP.AllowedRoots...)
	config.CfgMu.RUnlock()

	roots := make([]codeRootEntry, 0, 1+len(extra))
	if strings.TrimSpace(docroot) != "" {
		roots = append(roots, codeRootEntry{Index: 0, Label: "PHP docroot", Path: docroot})
	}
	for _, p := range extra {
		if strings.TrimSpace(p) == "" {
			continue
		}
		// 手工改配置加入的不安全目录直接忽略（保存接口已拦截，这里兜底）
		if err := config.ValidateDocRoot(p); err != nil {
			continue
		}
		roots = append(roots, codeRootEntry{
			Index: len(roots),
			Label: fmt.Sprintf("附加目录 %d", len(roots)),
			Path:  p,
		})
	}
	return roots
}

// codeRootForRequest 解析 ?root= 索引（缺省 0）并返回对应根目录。
// 只接受已配置的根目录索引，避免通过参数传入任意绝对路径。
func (h *ConfigHandler) codeRootForRequest(r *http.Request) (string, error) {
	roots := h.codeRoots()
	if len(roots) == 0 {
		return "", fmt.Errorf("未配置代码根目录")
	}
	idx := 0
	if v := strings.TrimSpace(r.URL.Query().Get("root")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n >= len(roots) {
			return "", fmt.Errorf("非法的 root 参数")
		}
		idx = n
	}
	return roots[idx].Path, nil
}

// handleCodeRoots 返回可选根目录列表（代码页根目录下拉框用）。
func (h *ConfigHandler) handleCodeRoots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"roots":  h.codeRoots(),
	})
}
