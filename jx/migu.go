package jx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/qist/tvgate/config"
	// "github.com/qist/tvgate/logger"
)

// 初始化注册咪咕
func init() {
	RegisterVideoSource("miguvideo.com", HandleMiGTVLink)
}

// HandleMiGTVLink 处理咪咕视频 URL
func HandleMiGTVLink(w http.ResponseWriter, r *http.Request, link, id string) {
	videoID := extractMiguID(link)
	if videoID == "" {
		JSONResponse(w, map[string]interface{}{"error": "无法解析 assetID"})
		return
	}

	apiURL := fmt.Sprintf("https://program-sc.miguvideo.com/program/v3/cont/content-info/%s", url.QueryEscape(videoID))
	client := &http.Client{Timeout: 10 * time.Second}

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	req.Header.Set("Referer", link)
	req.Header.Set("Origin", "https://www.miguvideo.com")
	req.Header.Set("Connection", "keep-alive")

	resp, err := client.Do(req)
	if err != nil {
		JSONResponse(w, map[string]interface{}{"error": "请求视频信息失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var respData struct {
		Code int `json:"code"`
		Body struct {
			Data struct {
				Name   string `json:"name"`
				Playing struct {
					Name  string `json:"name"`
					Index string `json:"index"`
				} `json:"playing"`
			} `json:"data"`
		} `json:"body"`
	}
	if err := json.Unmarshal(body, &respData); err != nil {
		JSONResponse(w, map[string]interface{}{"error": "解析JSON失败: " + err.Error()})
		return
	}

	if respData.Code != 200 || respData.Body.Data.Name == "" {
		JSONResponse(w, map[string]interface{}{"error": "未获取到有效视频信息"})
		return
	}

	seriesName := respData.Body.Data.Name
	episodeNum := respData.Body.Data.Playing.Index
	if episodeNum == "" {
		episodeNum = "1"
	}

	// logger.LogPrintf("videoID: %s, series_name: %s, episode: %s", videoID, seriesName, episodeNum)

	// 调用统一 HandleRequest 返回数据
	h := NewJXHandler(&config.Cfg.JX)
	h.HandleRequest(w, r, seriesName, episodeNum)
}

// extractMiguID 从咪咕 URL 中提取 assetID
func extractMiguID(link string) string {
	re := regexp.MustCompile(`/p/detail/(\d+)`)
	matches := re.FindStringSubmatch(link)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}
