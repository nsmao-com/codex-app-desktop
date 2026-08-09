package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GrokAPISession is a NiceCodex-owned chat stored under the app settings dir.
// Used when settings.GrokBackend == "api" (direct xAI / OpenAI-compatible HTTP).
type GrokAPISession struct {
	ID        string        `json:"id"`
	Workspace string        `json:"workspace"`
	Name      string        `json:"name"`
	Preview   string        `json:"preview"`
	Model     string        `json:"model"`
	Effort    string        `json:"effort"`
	CreatedAt int64         `json:"createdAt"`
	UpdatedAt int64         `json:"updatedAt"`
	Messages  []GrokMessage `json:"messages"`
}

// grokPendingApproval is reserved for future tool/approval prompts in API mode.
type grokPendingApproval struct {
	SessionID string
	TurnID    string
	CreatedAt int64
}

func grokAPISessionsPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "grok-api-sessions.json")
}

func loadGrokAPISessions(settingsPath string) map[string]*GrokAPISession {
	result := make(map[string]*GrokAPISession)
	if err := readGrokJSONFile(grokAPISessionsPath(settingsPath), &result); err != nil {
		return make(map[string]*GrokAPISession)
	}
	if result == nil {
		return make(map[string]*GrokAPISession)
	}
	return result
}

func (s *AppService) persistGrokAPISessionsLocked() error {
	path := grokAPISessionsPath(s.settingsPath)
	payload, err := json.MarshalIndent(s.grokAPISessions, "", "  ")
	if err != nil {
		return err
	}
	return writeGrokJSONFile(path, payload)
}

func grokAPIKeyConfigured() bool {
	// Env or any non-empty configured key path (detect uses env; full settings checked at send time).
	return envGrokAPIKey() != ""
}

func (s *AppService) grokAPIKeyConfiguredWithSettings() bool {
	return strings.TrimSpace(resolveGrokAPIKey(s.Settings())) != ""
}

func envGrokAPIKey() string {
	for _, key := range []string{"XAI_API_KEY", "GROK_API_KEY"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func resolveGrokAPIEndpoint(settings UserSettings) (string, error) {
	baseURL := resolveGrokAPIBaseURL(settings)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("Grok API Base URL is invalid")
	}
	host := strings.ToLower(parsed.Hostname())
	localHTTP := parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
	if parsed.Scheme != "https" && !localHTTP {
		return "", errors.New("Grok API Base URL must use HTTPS (HTTP is only allowed for localhost)")
	}
	return strings.TrimRight(baseURL, "/") + "/chat/completions", nil
}

func resolveGrokAPIKey(settings UserSettings) string {
	if value := strings.TrimSpace(settings.GrokAPIKey); value != "" {
		return value
	}
	return envGrokAPIKey()
}

func resolveGrokAPIBaseURL(settings UserSettings) string {
	if value := strings.TrimSpace(settings.GrokAPIBaseURL); value != "" {
		return strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("XAI_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("GROK_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return "https://api.x.ai/v1"
}

func (s *AppService) listGrokAPISessions(workspace, search string) []GrokSessionSummary {
	query := strings.ToLower(strings.TrimSpace(search))
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]GrokSessionSummary, 0, len(s.grokAPISessions))
	for _, session := range s.grokAPISessions {
		if session == nil {
			continue
		}
		if workspace != "" && !samePath(session.Workspace, workspace) {
			continue
		}
		haystack := strings.ToLower(session.Name + "\n" + session.Preview)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		result = append(result, GrokSessionSummary{
			ID:        session.ID,
			Backend:   grokBackendAPI,
			Workspace: session.Workspace,
			Name:      session.Name,
			Preview:   session.Preview,
			Model:     session.Model,
			Effort:    session.Effort,
			CreatedAt: session.CreatedAt,
			UpdatedAt: session.UpdatedAt,
		})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	return result
}

func (s *AppService) readGrokAPISession(sessionID string) (GrokSessionDetail, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return GrokSessionDetail{}, errors.New("Grok session id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.grokAPISessions[sessionID]
	if session == nil {
		return GrokSessionDetail{}, errors.New("Grok API session was not found")
	}
	messages := append([]GrokMessage(nil), session.Messages...)
	return GrokSessionDetail{
		Summary: GrokSessionSummary{
			ID: session.ID, Backend: grokBackendAPI, Workspace: session.Workspace,
			Name: session.Name, Preview: session.Preview, Model: session.Model, Effort: session.Effort,
			CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
		},
		Messages: messages,
	}, nil
}

func (s *AppService) deleteGrokAPISession(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("Grok session id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grokAPISessions[sessionID] == nil {
		return errors.New("Grok API session was not found")
	}
	previous := s.grokAPISessions[sessionID]
	delete(s.grokAPISessions, sessionID)
	if err := s.persistGrokAPISessionsLocked(); err != nil {
		s.grokAPISessions[sessionID] = previous
		return err
	}
	return nil
}

func (s *AppService) ensureGrokAPISessionLocked(request GrokSendRequest) *GrokAPISession {
	now := time.Now().Unix()
	session := s.grokAPISessions[request.SessionID]
	if session == nil {
		name := request.Text
		if len([]rune(name)) > 48 {
			name = string([]rune(name)[:48])
		}
		if strings.TrimSpace(name) == "" {
			name = "New Grok task"
		}
		session = &GrokAPISession{
			ID:        request.SessionID,
			Workspace: request.Workspace,
			Name:      name,
			Preview:   request.Text,
			Model:     request.Model,
			Effort:    request.Effort,
			CreatedAt: now,
			UpdatedAt: now,
			Messages:  make([]GrokMessage, 0, 8),
		}
		s.grokAPISessions[request.SessionID] = session
	}
	return session
}

func grokAPIMessageContent(message GrokMessage) (any, error) {
	text := strings.TrimSpace(message.Text)
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") || len(message.Images) == 0 {
		if text == "" {
			return nil, nil
		}
		return text, nil
	}
	content := make([]map[string]any, 0, len(message.Images)+1)
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, path := range message.Images {
		payload, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read Grok image attachment: %w", err)
		}
		if len(payload) == 0 || len(payload) > 20*1024*1024 {
			return nil, errors.New("Grok image attachment must be between 1 byte and 20 MB")
		}
		mimeType := http.DetectContentType(payload)
		switch mimeType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			return nil, errors.New("unsupported Grok image attachment format")
		}
		dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(payload)
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURL},
		})
	}
	if len(content) == 0 {
		return nil, nil
	}
	return content, nil
}

func (s *AppService) runGrokAPITurn(ctx context.Context, turnID string, request GrokSendRequest) (map[string]any, error) {
	settings := s.Settings()
	apiKey := resolveGrokAPIKey(settings)
	if apiKey == "" {
		return nil, errors.New("Grok API key missing — set it in Settings → Grok configuration, or export XAI_API_KEY / GROK_API_KEY")
	}
	endpoint, err := resolveGrokAPIEndpoint(settings)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = strings.TrimSpace(settings.GrokAPIModel)
	}
	if model == "" {
		model = "grok-4.5"
	}

	// Persist user message + build chat history for the API.
	s.mu.Lock()
	session := s.ensureGrokAPISessionLocked(request)
	session.Model = model
	session.Effort = normalizeGrokEffort(request.Effort)
	session.UpdatedAt = time.Now().Unix()
	session.Preview = request.Text
	if strings.TrimSpace(session.Name) == "" || session.Name == "New Grok task" {
		name := request.Text
		if len([]rune(name)) > 48 {
			name = string([]rune(name)[:48])
		}
		if name != "" {
			session.Name = name
		}
	}
	userMsg := GrokMessage{
		ID:        fmt.Sprintf("%s-user-%d", turnID, time.Now().UnixNano()),
		Role:      "user",
		Text:      request.Text,
		Images:    append([]string(nil), request.Images...),
		Status:    "completed",
		CreatedAt: time.Now().Unix(),
	}
	session.Messages = append(session.Messages, userMsg)
	requestSucceeded := false
	defer func() {
		if requestSucceeded {
			return
		}
		s.mu.Lock()
		if current := s.grokAPISessions[request.SessionID]; current != nil {
			for index := len(current.Messages) - 1; index >= 0; index-- {
				if current.Messages[index].ID == userMsg.ID {
					current.Messages[index].Status = "failed"
					current.UpdatedAt = time.Now().Unix()
					break
				}
			}
			_ = s.persistGrokAPISessionsLocked()
		}
		s.mu.Unlock()
	}()
	history := make([]map[string]any, 0, len(session.Messages))
	for _, message := range session.Messages {
		if strings.EqualFold(strings.TrimSpace(message.Status), "failed") {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "assistant" && role != "system" {
			continue
		}
		apiMessage := message
		if message.ID != userMsg.ID {
			apiMessage.Images = nil
		}
		content, err := grokAPIMessageContent(apiMessage)
		if err != nil {
			_ = s.persistGrokAPISessionsLocked()
			s.mu.Unlock()
			return nil, err
		}
		if content == nil {
			continue
		}
		history = append(history, map[string]any{"role": role, "content": content})
	}
	if err := s.persistGrokAPISessionsLocked(); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("persist Grok API session: %w", err)
	}
	s.mu.Unlock()

	body := map[string]any{
		"model":    model,
		"messages": history,
		"stream":   true,
		// Ask providers that support it to attach a final usage chunk.
		"stream_options": map[string]any{"include_usage": true},
	}
	if effort := normalizeGrokEffort(request.Effort); effort != "" {
		body["reasoning_effort"] = effort
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		message := strings.TrimSpace(string(raw))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("Grok API HTTP %d: %s", resp.StatusCode, truncateRunes(message, 800))
	}

	var assistant strings.Builder
	var usage map[string]any
	var streamSequence uint64
	stream := newExternalStreamCoalescer(func(_ string, delta string) {
		assistant.WriteString(delta)
		streamSequence++
		s.emitGrokEvent("text.delta", grokBackendAPI, request.SessionID, turnID, map[string]any{
			"delta": delta, "text": assistant.String(), "mode": "replace", "sequence": streamSequence,
		})
	})
	defer stream.Flush()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return usage, context.Canceled
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if next := normalizeTokenUsageMap(chunk["usage"]); next != nil {
			usage = next
		} else if next := normalizeTokenUsageMap(chunk); next != nil && chunk["usage"] == nil {
			// Some gateways put usage fields at the root of the final SSE frame.
			if anyToFloat(chunk["prompt_tokens"]) > 0 || anyToFloat(chunk["completion_tokens"]) > 0 {
				usage = next
			}
		}
		delta := extractOpenAIStreamDelta(chunk)
		if delta == "" {
			continue
		}
		stream.Push("text", delta)
	}
	stream.Flush()
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return usage, context.Canceled
		}
		return usage, err
	}

	finalText := strings.TrimSpace(assistant.String())
	if finalText == "" {
		return usage, errors.New("Grok API returned an empty response")
	}

	// Fallback estimate when the provider omitted stream usage.
	if usage == nil {
		usage = estimateTokenUsage(request.Text, finalText)
	}

	s.mu.Lock()
	if session := s.grokAPISessions[request.SessionID]; session != nil {
		assistantMessage := GrokMessage{
			ID:        fmt.Sprintf("%s-assistant-%d", turnID, time.Now().UnixNano()),
			Role:      "assistant",
			Text:      finalText,
			Status:    "completed",
			CreatedAt: time.Now().Unix(),
		}
		session.Messages = append(session.Messages, assistantMessage)
		session.UpdatedAt = time.Now().Unix()
		session.Preview = finalText
		if err := s.persistGrokAPISessionsLocked(); err != nil {
			session.Messages = session.Messages[:len(session.Messages)-1]
			s.mu.Unlock()
			return usage, fmt.Errorf("persist Grok API response: %w", err)
		}
	}
	s.mu.Unlock()
	requestSucceeded = true
	return usage, nil
}

// Rough UTF-8 heuristic when the API omits usage (~4 chars/token).
func estimateTokenUsage(prompt, completion string) map[string]any {
	input := int64((len([]rune(prompt)) + 3) / 4)
	output := int64((len([]rune(completion)) + 3) / 4)
	if input <= 0 && output <= 0 {
		return nil
	}
	return map[string]any{
		"inputTokens":           input,
		"cachedInputTokens":     int64(0),
		"outputTokens":          output,
		"reasoningOutputTokens": int64(0),
		"totalTokens":           input + output,
	}
}

func extractOpenAIStreamDelta(chunk map[string]any) string {
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	first, _ := choices[0].(map[string]any)
	if first == nil {
		return ""
	}
	delta, _ := first["delta"].(map[string]any)
	if delta != nil {
		if text, ok := delta["content"].(string); ok {
			return text
		}
	}
	// Non-stream fallback shape.
	message, _ := first["message"].(map[string]any)
	if message != nil {
		if text, ok := message["content"].(string); ok {
			return text
		}
	}
	return ""
}
