package jx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/qist/tvgate/config"
	// "github.com/qist/tvgate/logger"
)

var qqHeaders = map[string]string{
	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0 Safari/537.36",
	"Referer":    "https://v.qq.com/",
	"Origin":     "https://v.qq.com",
}

var qqVideoIDRegex = regexp.MustCompile(`/([a-zA-Z0-9]+)\.html`)

const qqInfoURLTemplate = "https://union.video.qq.com/fcgi-bin/data?otype=json&tid=682&appid=20001238&appkey=6c03bbe9658448a4&union_platform=1&idlist=%s"

// QQAPIResult 腾讯返回的数据结构
type QQAPIResult struct {
	Results []struct {
		Fields struct {
			SeriesName   string `json:"series_name"`
			CTitleOutput string `json:"c_title_output"`
			Title        string `json:"title"`
			VideoID      string `json:"vid"`
			Duration     string `json:"duration"`
		} `json:"fields"`
	} `json:"results"`
}

func init() {
	RegisterVideoSource("qq.com", HandleQQLink)
}

// HandleQQLink 处理腾讯视频 URL
func HandleQQLink(w http.ResponseWriter, r *http.Request, link, id string) {
    videoID := parseQQVideoID(link)
    if videoID == "" {
        JSONResponse(w, map[string]interface{}{"error": "未解析到视频ID"})
        return
    }

    infoURL := fmt.Sprintf(qqInfoURLTemplate, videoID)
    client := &http.Client{Timeout: 10 * time.Second}

    req, _ := http.NewRequest("GET", infoURL, nil)
    for k, v := range qqHeaders {
        req.Header.Set(k, v)
    }
    resp, err := client.Do(req)
    if err != nil {
        JSONResponse(w, map[string]interface{}{"error": "获取视频信息失败: " + err.Error()})
        return
    }
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    cleanBody := cleanJSONP(string(body))

    var info QQAPIResult
    if err := json.Unmarshal([]byte(cleanBody), &info); err != nil {
        JSONResponse(w, map[string]interface{}{"error": "解析JSON失败: " + err.Error()})
        return
    }

    if len(info.Results) == 0 {
        JSONResponse(w, map[string]interface{}{"error": "未获取到视频信息"})
        return
    }

    fields := info.Results[0].Fields
    seriesName := fields.SeriesName
    if seriesName == "" {
        seriesName = fields.Title // 兜底
    }

    // ✅ 优先用腾讯返回的集数
    if fields.CTitleOutput != "" {
        id = fields.CTitleOutput
    }
    if id == "" {
        id = config.Cfg.JX.DefaultID
    }

    // logger.LogPrintf("QQLink resolved: videoID=%s, series=%s, episode=%s", fields.VideoID, seriesName, id)

    // 调用通用 HandleRequest
    h := NewJXHandler(&config.Cfg.JX)
    h.HandleRequest(w, r, seriesName, id)
}


// 提取视频ID
func parseQQVideoID(link string) string {
	matches := qqVideoIDRegex.FindStringSubmatch(link)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// 去掉 JSONP 包裹
func cleanJSONP(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
