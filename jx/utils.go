package jx

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// JSONResponse 输出 JSON
func JSONResponse(w http.ResponseWriter, data map[string]interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "JSON序列化失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(jsonData)
}

// parseEpisodes 解析 vod_play_url 成集数列表
func parseEpisodes(vodPlayUrl string) []map[string]string {
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
	return eps
}

// compileRegex 编译正则
func compileRegex(pattern string) *regexp.Regexp {
	re, _ := regexp.Compile(pattern)
	return re
}
