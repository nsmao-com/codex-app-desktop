package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// TranslateMessage sends only the explicitly selected text to Google Cloud.
// Keys are request-scoped (or provided by the environment), never logged/stored.
func (s *AppService) TranslateMessage(text, target, apiKey string) (string, error) {
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 30000 {
		return "", errors.New("请选择需要翻译的文本；单次最多 30,000 个字符")
	}
	allowed := map[string]bool{"zh-CN": true, "zh-TW": true, "en": true, "ja": true, "ko": true, "fr": true, "de": true, "es": true, "pt": true, "ru": true, "ar": true}
	if !allowed[target] {
		return "", errors.New("请选择支持的目标语言")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("GOOGLE_TRANSLATE_API_KEY"))
	}
	if apiKey == "" {
		return "", errors.New("请填写 Google Cloud Translation API Key，或设置 GOOGLE_TRANSLATE_API_KEY 环境变量")
	}
	if len(apiKey) > 512 || strings.ContainsAny(apiKey, "\r\n") {
		return "", errors.New("翻译 API Key 格式无效")
	}
	payload, err := json.Marshal(map[string]any{"q": text, "target": target, "format": "text"})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://translation.googleapis.com/language/translate/v2", bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("无法创建翻译请求")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", apiKey)
	client := &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("无法连接 Google 翻译，请检查网络或代理后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Google 翻译请求失败（HTTP %d）；请检查 API Key、Cloud Translation 权限与额度", response.StatusCode)
	}
	var result struct {
		Data struct {
			Translations []struct {
				Text string `json:"translatedText"`
			} `json:"translations"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&result); err != nil || len(result.Data.Translations) == 0 {
		return "", errors.New("Google 未返回有效译文，请稍后重试")
	}
	return html.UnescapeString(result.Data.Translations[0].Text), nil
}
