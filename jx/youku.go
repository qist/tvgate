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
	// "github.com/qist/tvgate/logger"
)

// 初始化注册优酷
func init() {
	RegisterVideoSource("youku.com", HandleYoukuLink)
}

// HandleYoukuLink 处理优酷播放地址
func HandleYoukuLink(w http.ResponseWriter, r *http.Request, link, id string) {
	videoID := extractYoukuID(link)
	if videoID == "" {
		JSONResponse(w, map[string]interface{}{"error": "无法解析 video_id"})
		return
	}

	// 构造 API 请求 URL
	apiURL := fmt.Sprintf("https://openapi.youku.com/v2/videos/show_basic.json?video_id=%s&client_id=53e6cc67237fc59a", url.QueryEscape(videoID))
	client := &http.Client{Timeout: 10 * time.Second}

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	req.Header.Set("Referer", link)
	req.Header.Set("Origin", "https://www.youku.com")
	req.Header.Set("Connection", "keep-alive")

	resp, err := client.Do(req)
	if err != nil {
		JSONResponse(w, map[string]interface{}{"error": "请求视频信息失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var infoData struct {
		Title string `json:"title"`
		Link  string `json:"link"`
	}
	if err := json.Unmarshal(body, &infoData); err != nil {
		JSONResponse(w, map[string]interface{}{"error": "解析JSON失败: " + err.Error()})
		return
	}

	if infoData.Title == "" {
		JSONResponse(w, map[string]interface{}{"error": "未获取到有效视频信息"})
		return
	}

	seriesName, episode := parseYoukuTitle(infoData.Title)

	// logger.LogPrintf("videoID: %s, series_name: %s, episode: %s", videoID, seriesName, episode)

	// 调用统一 HandleRequest 返回数据
	h := NewJXHandler(&config.Cfg.JX)
	h.HandleRequest(w, r, seriesName, episode)
}

// extractYoukuID 从优酷 URL 中提取 video_id
func extractYoukuID(link string) string {
	re := regexp.MustCompile(`id_([a-zA-Z0-9=]+)\.html`)
	matches := re.FindStringSubmatch(link)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// parseYoukuTitle 从 title 中提取集数，例如 "七根心简 03" -> seriesName="七根心简", episode="3"
func parseYoukuTitle(title string) (seriesName, episode string) {
	parts := strings.Fields(title)
	if len(parts) >= 2 {
		seriesName = strings.Join(parts[:len(parts)-1], " ")
		epStr := parts[len(parts)-1]
		// 删除前导 0
		epNum, err := strconv.Atoi(epStr)
		if err == nil {
			episode = strconv.Itoa(epNum)
		} else {
			episode = epStr
		}
	} else {
		seriesName = title
		episode = "1"
	}
	return
}
