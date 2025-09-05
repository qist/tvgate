package jx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

type JXHandler struct {
	Config *config.JXConfig
	Logf   func(format string, v ...interface{})
}

func NewJXHandler(cfg *config.JXConfig, logf func(format string, v ...interface{})) *JXHandler {
	return &JXHandler{
		Config: cfg,
		Logf:   logf,
	}
}

// Handler 入口
func (h *JXHandler) Handle(w http.ResponseWriter, r *http.Request) {
	jxParam := r.URL.Query().Get("jx")
	idParam := r.URL.Query().Get("id")
	if idParam == "" {
		idParam = h.Config.DefaultID
	}

	// h.Logf("处理 jx 请求: %s, id: %s", jxParam, idParam)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Server", "TVGate")

	if jxParam == "" {
		h.jsonResponse(w, map[string]interface{}{"error": "jx 参数不能为空"})
		return
	}

	if strings.HasPrefix(jxParam, "http://") || strings.HasPrefix(jxParam, "https://") {
		h.handleIQiyiLink(w, r, jxParam, idParam)
		return
	}

	h.handleRequest(w, r, jxParam, idParam)
}

// 按权重轮询主/备 API
func (h *JXHandler) handleRequest(w http.ResponseWriter, r *http.Request, name, id string) {
	if len(h.Config.APIGroups) == 0 {
		h.jsonResponse(w, map[string]interface{}{"error": "未配置视频 API"})
		return
	}

	// 按权重构建请求列表，主 API 优先
	type apiRequest struct {
		api     *config.VideoAPIGroupConfig
		baseURL string
		fullURL string
	}
	var requests []apiRequest
	for _, api := range h.Config.APIGroups {
		for _, baseURL := range api.Endpoints {
			fullURL := fmt.Sprintf(api.QueryTemplate, strings.TrimRight(baseURL, "/"), url.QueryEscape(name))
			requests = append(requests, apiRequest{api: api, baseURL: baseURL, fullURL: fullURL})
		}
	}

	for _, req := range requests {
		// h.Logf("查询 URL: %s", req.fullURL)
		client := &http.Client{Timeout: req.api.Timeout}

		var respBody []byte
		success := false
		for attempt := 0; attempt < req.api.MaxRetries; attempt++ {
			resp, err := client.Get(req.fullURL)
			if err != nil {
				if attempt < req.api.MaxRetries-1 {
					time.Sleep(time.Second)
					continue
				} else {
					h.Logf("请求失败: %v", err)
					break
				}
			}
			respBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			success = true
			break
		}
		if !success {
			continue
		}

		var respData map[string]interface{}
		if err := json.Unmarshal(respBody, &respData); err != nil {
			h.Logf("解析JSON失败: %v", err)
			continue
		}

		listData, ok := respData["list"].([]interface{})
		if !ok || len(listData) == 0 {
			// h.Logf("API 返回空数据: %s", req.fullURL)
			continue // <-- 空数据就尝试下一个 API
		}

		// 成功获取到数据，构建返回
		var result []map[string]interface{}
		var playurlString string
		for _, item := range listData {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			vodName, _ := itemMap[req.api.Mapping["name"]].(string)
			vodPlayUrl, _ := itemMap[req.api.Mapping["playurl"]].(string)

			var eps []map[string]string
			parts := strings.Split(vodPlayUrl, "$$$")
			secondPart := vodPlayUrl
			if len(parts) > 1 {
				secondPart = parts[1]
			}
			for _, ep := range strings.Split(secondPart, "#") {
				if strings.Contains(ep, "$") {
					epParts := strings.Split(ep, "$")
					if len(epParts) >= 2 {
						eps = append(eps, map[string]string{
							"name": strings.TrimSpace(epParts[0]),
							"url":  strings.TrimSpace(epParts[1]),
						})
					}
				}
			}

			result = append(result, map[string]interface{}{
				"name": vodName,
				"source": map[string]interface{}{
					"eps": eps,
				},
			})

			if strings.Contains(vodName, name) && playurlString == "" && len(eps) > 0 {
				epIndex := 0
				if id != "" {
					if idx, err := strconv.Atoi(id); err == nil {
						epIndex = idx - 1
					}
				}
				if epIndex >= 0 && epIndex < len(eps) {
					playurlString = eps[epIndex]["url"]
				} else {
					playurlString = eps[0]["url"]
				}
			}
		}

		finalResp := map[string]interface{}{
			"From_id":     id,
			"From_title":  name,
			"From_source": req.baseURL,
			"data":        result,
		}
		if playurlString != "" {
			finalResp["url"] = playurlString
		}
		h.jsonResponse(w, finalResp)
		logger.LogRequestAndResponse(r, req.fullURL, &http.Response{StatusCode: http.StatusOK}) // 日志记录
		return // 成功返回，不再尝试其它 API
	}
	
	// 所有 API 都失败或返回空数据
	h.jsonResponse(w, map[string]interface{}{"error": "未获取到有效数据"})
}

// 处理爱奇艺 URL
func (h *JXHandler) handleIQiyiLink(w http.ResponseWriter, r *http.Request, link, id string) {
	api, ok := config.Cfg.JX.APIGroups["iqiyi"]
	if !ok {
		h.jsonResponse(w, map[string]interface{}{"error": "未配置 iqiyi API"})
		return
	}

	client := &http.Client{Timeout: api.Timeout}
	req, _ := http.NewRequest("GET", link, nil)
	for k, v := range api.Headers {
		req.Header.Set(k, v)
	}

	// 记录请求日志
	// h.Logf("处理爱奇艺链接: %s", link)

	resp, err := client.Do(req)
	if err != nil {
		h.jsonResponse(w, map[string]interface{}{"error": "请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	re := regexp.MustCompile(api.TVIdRegex)
	matches := re.FindStringSubmatch(html)
	var tvId string
	if len(matches) > 1 {
		tvId = strings.Trim(matches[1], "\"'")
	}

	if tvId == "" {
		h.jsonResponse(w, map[string]interface{}{"error": "未获取到 tvId"})
		return
	}

	infoURL := fmt.Sprintf(api.InfoURLTemplate, tvId)
	infoReq, _ := http.NewRequest("GET", infoURL, nil)
	for k, v := range api.Headers {
		infoReq.Header.Set(k, v)
	}
	infoResp, err := client.Do(infoReq)
	if err != nil {
		h.jsonResponse(w, map[string]interface{}{"error": "获取视频信息失败: " + err.Error()})
		return
	}
	defer infoResp.Body.Close()
	infoBody, _ := io.ReadAll(infoResp.Body)

	// 记录获取到的视频信息
	// h.Logf("获取到爱奇艺视频信息，tvId: %s", tvId)

	var infoData map[string]interface{}
	if err := json.Unmarshal(infoBody, &infoData); err != nil {
		h.jsonResponse(w, map[string]interface{}{"error": "解析视频信息失败: " + err.Error()})
		return
	}

	data, ok := infoData["data"].(map[string]interface{})
	if !ok {
		h.jsonResponse(w, map[string]interface{}{"error": "未找到 data 字段"})
		return
	}

	albumName, _ := data["albumName"].(string)
	if orderVal, exists := data["order"]; exists {
		switch v := orderVal.(type) {
		case string:
			id = v
		case float64:
			id = strconv.FormatInt(int64(v), 10)
		}
	} else if id == "" {
		id = config.Cfg.JX.DefaultID
	}

	if albumName != "" {
		h.handleRequest(w, r, albumName, id)
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"url":         link,
		"tvId":        tvId,
		"content_len": len(html),
		"status_code": resp.StatusCode,
	})
	// logger.LogRequestAndResponse(r, req.fullURL, &http.Response{StatusCode: http.StatusOK}) // 日志记录
}

// 输出 JSON
func (h *JXHandler) jsonResponse(w http.ResponseWriter, data map[string]interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "JSON序列化失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(jsonData)
}
