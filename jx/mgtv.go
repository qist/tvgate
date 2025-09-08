package jx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	// "strconv"
	// "strings"
	"time"

	"github.com/qist/tvgate/config"
	// "github.com/qist/tvgate/logger"
)

// 初始化注册芒果 TV
func init() {
	RegisterVideoSource("mgtv.com", HandleMGTVLink)
}

// HandleMGTVLink 处理芒果 TV 播放地址
func HandleMGTVLink(w http.ResponseWriter, r *http.Request, link, id string) {
	videoID, cid := extractMGTVIDs(link)
	if videoID == "" || cid == "" {
		JSONResponse(w, map[string]interface{}{"error": "无法解析 video_id 或 cid"})
		return
	}

	apiURL := fmt.Sprintf("https://pcweb.api.mgtv.com/player/vinfo?video_id=%s&cid=%s", url.QueryEscape(videoID), url.QueryEscape(cid))
	client := &http.Client{Timeout: 10 * time.Second}

	req, _ := http.NewRequest("GET", apiURL, nil)
	// 添加请求头
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	req.Header.Set("Referer", link)
	req.Header.Set("Origin", "https://www.mgtv.com")
	req.Header.Set("Connection", "keep-alive")

	resp, err := client.Do(req)
	if err != nil {
		JSONResponse(w, map[string]interface{}{"error": "请求视频信息失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var infoData struct {
		Data struct {
			ClipName string `json:"clip_name"` // 片名
			T2       string `json:"t2"`        // 集数，例如 "长风少年词 第2集"
			URL      string `json:"url"`       // 播放页 URL
		} `json:"data"`
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &infoData); err != nil {
		JSONResponse(w, map[string]interface{}{"error": "解析JSON失败: " + err.Error()})
		return
	}
	if infoData.Code != 200 {
		JSONResponse(w, map[string]interface{}{"error": "未获取到有效视频信息"})
		return
	}

	// 从 t2 提取集数
	episode := extractEpisode(infoData.Data.T2)
	if episode == "" {
		episode = "1" // 默认集数
	}

	seriesName := infoData.Data.ClipName
	if seriesName == "" {
		seriesName = infoData.Data.T2
	}

	// logger.LogPrintf("videoID: %s, cid: %s, series_name: %s, episode: %s", videoID, cid, seriesName, episode)

	// 调用统一 HandleRequest 返回数据
	h := NewJXHandler(&config.Cfg.JX)
	h.HandleRequest(w, r, seriesName, episode)
}

// extractMGTVIDs 从播放 URL 中提取 video_id 和 cid
func extractMGTVIDs(link string) (videoID, cid string) {
	re := regexp.MustCompile(`/b/(\d+)/(\d+)\.html`)
	matches := re.FindStringSubmatch(link)
	if len(matches) >= 3 {
		cid = matches[1]
		videoID = matches[2]
	}
	return
}

// extractEpisode 从 t2 字段中提取集数，例如 "长风少年词 第2集" -> "2"
func extractEpisode(t2 string) string {
	re := regexp.MustCompile(`第(\d+)集`)
	matches := re.FindStringSubmatch(t2)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}
