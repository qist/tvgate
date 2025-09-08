package jx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
)

var iqiyiHeaders = map[string]string{
	"User-Agent":   "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.25 Safari/537.36",
	"Referer":      "https://www.iqiyi.com/",
	"Origin":       "https://www.iqiyi.com",
	"Connection":   "keep-alive",
	"Content-Type": "application/json; charset=utf-8",
}

// var iqiyiMapping = map[string]string{
// 	"name":    "vod_name",
// 	"playurl": "vod_play_url",
// }

var iqiyiTVIdRegex = `"tvId":(.*?),`
var iqiyiInfoURLTemplate = "https://pcw-api.iqiyi.com/video/video/baseinfo/%s"

func init() {
	RegisterVideoSource("iqiyi.com", HandleIQiyiLink)
}

// HandleIQiyiLink 处理爱奇艺 URL
func HandleIQiyiLink(w http.ResponseWriter, r *http.Request, link, id string) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", link, nil)
	for k, v := range iqiyiHeaders {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		JSONResponse(w, map[string]interface{}{"error": "请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	tvId := parseTVId(html, iqiyiTVIdRegex)
	if tvId == "" {
		JSONResponse(w, map[string]interface{}{"error": "未获取到 tvId"})
		return
	}

	infoURL := fmt.Sprintf(iqiyiInfoURLTemplate, tvId)
	infoReq, _ := http.NewRequest("GET", infoURL, nil)
	for k, v := range iqiyiHeaders {
		infoReq.Header.Set(k, v)
	}
	infoResp, err := client.Do(infoReq)
	if err != nil {
		JSONResponse(w, map[string]interface{}{"error": "获取视频信息失败: " + err.Error()})
		return
	}
	defer infoResp.Body.Close()
	infoBody, _ := io.ReadAll(infoResp.Body)

	var infoData map[string]interface{}
	if err := json.Unmarshal(infoBody, &infoData); err != nil {
		JSONResponse(w, map[string]interface{}{"error": "解析视频信息失败: " + err.Error()})
		return
	}

	data, ok := infoData["data"].(map[string]interface{})
	if !ok {
		JSONResponse(w, map[string]interface{}{"error": "未找到 data 字段"})
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

	// 使用全局 JX 配置调用 HandleRequest
	if albumName != "" {
		h := NewJXHandler(&config.Cfg.JX) // 取地址
		h.HandleRequest(w, r, albumName, id)
		return
	}

	JSONResponse(w, map[string]interface{}{
		"url":         link,
		"tvId":        tvId,
		"content_len": len(html),
		"status_code": resp.StatusCode,
	})
}

func parseTVId(html, regexStr string) string {
	re := compileRegex(regexStr)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.Trim(matches[1], "\"'")
	}
	return ""
}
