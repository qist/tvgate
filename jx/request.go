package jx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	"github.com/qist/tvgate/logger"
)

// HandleRequest 通用视频 API 查询
func (h *JXHandler) HandleRequest(w http.ResponseWriter, r *http.Request, name, id string) {
	if h.Config == nil || len(h.Config.APIGroups) == 0 {
		JSONResponse(w, map[string]interface{}{"error": "未配置视频 API"})
		return
	}

	// URL 参数 full=1 时输出完整数据
	showData := r.URL.Query().Get("full") == "1"

	type apiRequest struct {
		api     *config.VideoAPIGroupConfig
		baseURL string
		fullURL string
	}

	// 构造 API 请求列表
	var requests []apiRequest
	for _, api := range h.Config.APIGroups {
		for _, baseURL := range api.Endpoints {
			fullURL := fmt.Sprintf(api.QueryTemplate, strings.TrimRight(baseURL, "/"), url.QueryEscape(name))
			requests = append(requests, apiRequest{api: api, baseURL: baseURL, fullURL: fullURL})
		}
	}

	// 遍历 API 组请求
	for _, req := range requests {
		client := &http.Client{Timeout: req.api.Timeout}

		var respBody []byte
		success := false
		for attempt := 0; attempt < req.api.MaxRetries; attempt++ {
			resp, err := client.Get(req.fullURL)
			if err != nil {
				if attempt < req.api.MaxRetries-1 {
					time.Sleep(time.Second)
					continue
				}
				logger.LogPrintf("请求失败: %v", err)
				break
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
			logger.LogPrintf("解析JSON失败: %v", err)
			continue
		}

		listData, ok := respData["list"].([]interface{})
		if !ok || len(listData) == 0 {
			continue
		}

		// 提取 exclude 过滤关键字（逗号分隔）
		var excludeKeywords []string
		if ex, ok := req.api.Filters["exclude"]; ok && ex != "" {
			excludeKeywords = strings.Split(ex, ",")
			for i := range excludeKeywords {
				excludeKeywords[i] = strings.TrimSpace(excludeKeywords[i])
			}
		}

		var result []map[string]interface{}
		var playurlString string
		for _, item := range listData {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			vodName, _ := itemMap["vod_name"].(string)
			vodPlayUrl, _ := itemMap["vod_play_url"].(string)

			// 过滤逻辑
			if len(excludeKeywords) > 0 {
				skip := false
				for _, kw := range excludeKeywords {
					if kw != "" && strings.Contains(vodName, kw) {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
			}

			eps := parseEpisodes(vodPlayUrl)
			result = append(result, map[string]interface{}{
				"name": vodName,
				"source": map[string]interface{}{
					"eps": eps,
				},
			})

			// 自动选择匹配播放地址
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
		}
		if playurlString != "" {
			finalResp["url"] = playurlString
		}
		if showData { // 只有带 full=1 时才输出完整 data
			finalResp["data"] = result
		}

		JSONResponse(w, finalResp)
		logger.LogRequestAndResponse(r, req.fullURL, &http.Response{StatusCode: http.StatusOK})
		return
	}

	// 所有 API 都失败或返回空
	JSONResponse(w, map[string]interface{}{"error": "未获取到有效数据"})
}
