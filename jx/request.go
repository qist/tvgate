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

	showData := r.URL.Query().Get("full") == "1"

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

		// 提取 exclude 过滤关键字
		var excludeKeywords []string
		if ex, ok := req.api.Filters["exclude"]; ok && ex != "" {
			excludeKeywords = strings.Split(ex, ",")
			for i := range excludeKeywords {
				excludeKeywords[i] = strings.TrimSpace(excludeKeywords[i])
			}
		}

		result, playurlString := processVideoList(listData, name, id, excludeKeywords)

		finalResp := map[string]interface{}{
			"From_id":     id,
			"From_title":  name,
			"From_source": req.baseURL,
		}
		if playurlString != "" {
			finalResp["url"] = playurlString
		}
		if showData {
			finalResp["data"] = result
		}

		JSONResponse(w, finalResp)
		logger.LogRequestAndResponse(r, req.fullURL, &http.Response{StatusCode: http.StatusOK})
		return
	}

	JSONResponse(w, map[string]interface{}{"error": "未获取到有效数据"})
}

// -------------------- 辅助函数 --------------------

// processVideoList 处理视频列表，返回完整结果和播放地址
func processVideoList(listData []interface{}, name, id string, excludeKeywords []string) ([]map[string]interface{}, string) {
	var result []map[string]interface{}
	var playurlString, partialMatchPlayurlString string

	getEpisodeIndex := func(id string, max int) int {
		idx := 0
		if id != "" {
			if parsed, err := strconv.Atoi(id); err == nil {
				idx = parsed - 1
			}
		}
		if idx < 0 || idx >= max {
			idx = 0
		}
		return idx
	}

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
		if len(eps) == 0 {
			continue
		}

		result = append(result, map[string]interface{}{
			"name": vodName,
			"source": map[string]interface{}{
				"eps": eps,
			},
		})

		// 完全匹配优先
		if vodName == name && playurlString == "" {
			epIndex := getEpisodeIndex(id, len(eps))
			playurlString = eps[epIndex]["url"]
		} else if strings.Contains(vodName, name) && partialMatchPlayurlString == "" {
			// 部分匹配备用
			epIndex := getEpisodeIndex(id, len(eps))
			partialMatchPlayurlString = eps[epIndex]["url"]
		}
	}

	// 如果没有完全匹配但有部分匹配，使用部分匹配结果
	if playurlString == "" && partialMatchPlayurlString != "" {
		playurlString = partialMatchPlayurlString
	}

	return result, playurlString
}
