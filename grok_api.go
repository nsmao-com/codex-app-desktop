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
	ID             string        `json:"id"`
	Workspace      string        `json:"workspace"`
	Name           string        `json:"name"`
	Preview        string        `json:"preview"`
	Model          string        `json:"model"`
	Effort         string        `json:"effort"`
	LastResponseID string        `json:"lastResponseId,omitempty"`
	CreatedAt      int64         `json:"createdAt"`
	UpdatedAt      int64         `json:"updatedAt"`
	Messages       []GrokMessage `json:"messages"`
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

func resolveGrokAPIBase(settings UserSettings) (string, error) {
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
	return strings.TrimRight(baseURL, "/"), nil
}

func resolveGrokAPIEndpoint(settings UserSettings) (string, error) {
	base, err := resolveGrokAPIBase(settings)
	if err != nil {
		return "", err
	}
	return base + "/chat/completions", nil
}

func resolveGrokResponsesEndpoint(settings UserSettings) (string, error) {
	base, err := resolveGrokAPIBase(settings)
	if err != nil {
		return "", err
	}
	return base + "/responses", nil
}

func grokAPIPrefersResponses(settings UserSettings) bool {
	parsed, err := url.Parse(resolveGrokAPIBaseURL(settings))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "api.x.ai"
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
	sort.SliceStable(result, func(i, j int) bool {
		iActive := workspace != "" && samePath(result[i].Workspace, workspace)
		jActive := workspace != "" && samePath(result[j].Workspace, workspace)
		if iActive != jActive {
			return iActive
		}
		return result[i].UpdatedAt > result[j].UpdatedAt
	})
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
		model = "grok-4.6"
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
	previousResponseID := strings.TrimSpace(session.LastResponseID)
	if err := s.persistGrokAPISessionsLocked(); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("persist Grok API session: %w", err)
	}
	s.mu.Unlock()

	var assistant strings.Builder
	var thought strings.Builder
	var usage map[string]any
	var responseID string
	var streamSequence uint64
	var thoughtSequence uint64
	stream := newExternalStreamCoalescer(func(kind, delta string) {
		if kind == "thought" {
			thought.WriteString(delta)
			thoughtSequence++
			s.emitGrokEvent("thought.delta", grokBackendAPI, request.SessionID, turnID, grokClientTurnPayload(request.ClientTurnID, map[string]any{
				"delta": delta, "text": thought.String(), "mode": "replace", "sequence": thoughtSequence,
			}))
			return
		}
		assistant.WriteString(delta)
		streamSequence++
		s.emitGrokEvent("text.delta", grokBackendAPI, request.SessionID, turnID, grokClientTurnPayload(request.ClientTurnID, map[string]any{
			"delta": delta, "text": assistant.String(), "mode": "replace", "sequence": streamSequence,
		}))
	})
	defer stream.Flush()

	streamErr := s.streamGrokAPI(ctx, settings, apiKey, endpoint, model, request.Effort, history, previousResponseID, request.SessionID, &usage, &responseID, stream)
	stream.Flush()
	if streamErr != nil {
		return usage, streamErr
	}

	finalText := strings.TrimSpace(assistant.String())
	finalThought := strings.TrimSpace(thought.String())
	if finalText == "" && finalThought == "" {
		return usage, errors.New("Grok API returned an empty response")
	}

	// Fallback estimate when the provider omitted stream usage.
	if usage == nil {
		usage = estimateTokenUsage(request.Text, finalText)
	}

	s.mu.Lock()
	if session := s.grokAPISessions[request.SessionID]; session != nil {
		now := time.Now().Unix()
		added := 0
		if finalThought != "" {
			session.Messages = append(session.Messages, GrokMessage{
				ID:        fmt.Sprintf("%s-reasoning-%d", turnID, time.Now().UnixNano()),
				Role:      "reasoning",
				Text:      finalThought,
				Status:    "completed",
				CreatedAt: now,
			})
			added++
		}
		if finalText != "" {
			session.Messages = append(session.Messages, GrokMessage{
				ID:        fmt.Sprintf("%s-assistant-%d", turnID, time.Now().UnixNano()),
				Role:      "assistant",
				Text:      finalText,
				Status:    "completed",
				CreatedAt: now,
			})
			added++
		}
		session.UpdatedAt = now
		if responseID != "" {
			session.LastResponseID = responseID
		}
		if finalText != "" {
			session.Preview = finalText
		} else {
			session.Preview = finalThought
		}
		if err := s.persistGrokAPISessionsLocked(); err != nil {
			if added > 0 && len(session.Messages) >= added {
				session.Messages = session.Messages[:len(session.Messages)-added]
			}
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
		"estimated":             true,
	}
}

func extractOpenAIStreamDelta(chunk map[string]any) string {
	text, _ := extractOpenAIStreamParts(chunk)
	return text
}

// extractOpenAIStreamParts reads official OpenAI / xAI chat.completion.chunk deltas.
// xAI SDK and many compatible gateways put thinking in delta.reasoning_content
// while the visible answer stays in delta.content.
func extractOpenAIStreamParts(chunk map[string]any) (text, thought string) {
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return "", ""
	}
	first, _ := choices[0].(map[string]any)
	if first == nil {
		return "", ""
	}
	if delta, _ := first["delta"].(map[string]any); delta != nil {
		return openAIMessageContent(delta["content"]), openAIReasoningContent(delta)
	}
	if message, _ := first["message"].(map[string]any); message != nil {
		return openAIMessageContent(message["content"]), openAIReasoningContent(message)
	}
	return "", ""
}

func openAIMessageContent(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return textFromExternalValue(value)
}

func openAIReasoningContent(payload map[string]any) string {
	if text := firstMapString(payload, "reasoning_content", "reasoning", "thinking"); text != "" {
		return text
	}
	return textFromExternalValue(payload["reasoning_content"])
}

func (s *AppService) streamGrokAPI(
	ctx context.Context,
	settings UserSettings,
	apiKey, chatEndpoint, model, effort string,
	history []map[string]any,
	previousResponseID, sessionID string,
	usage *map[string]any,
	responseID *string,
	stream *externalStreamCoalescer,
) error {
	if grokAPIPrefersResponses(settings) {
		err := s.streamGrokResponses(ctx, settings, apiKey, model, effort, history, previousResponseID, usage, responseID, stream)
		if err == nil {
			return nil
		}
		if grokPreviousResponseExpired(err) && previousResponseID != "" {
			s.clearGrokLastResponseID(sessionID)
			err = s.streamGrokResponses(ctx, settings, apiKey, model, effort, history, "", usage, responseID, stream)
			if err == nil {
				return nil
			}
		}
		if grokShouldRetryChatCompletions(err) {
			return s.streamGrokChatCompletions(ctx, apiKey, chatEndpoint, model, effort, history, usage, stream)
		}
		return err
	}
	return s.streamGrokChatCompletions(ctx, apiKey, chatEndpoint, model, effort, history, usage, stream)
}

func (s *AppService) clearGrokLastResponseID(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.grokAPISessions[sessionID]; session != nil {
		session.LastResponseID = ""
		_ = s.persistGrokAPISessionsLocked()
	}
}

func (s *AppService) streamGrokChatCompletions(
	ctx context.Context,
	apiKey, endpoint, model, effort string,
	history []map[string]any,
	usage *map[string]any,
	stream *externalStreamCoalescer,
) error {
	body := map[string]any{
		"model":          model,
		"messages":       history,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if normalized := normalizeGrokEffort(effort); normalized != "" {
		body["reasoning_effort"] = normalized
	}
	return s.streamGrokHTTP(ctx, apiKey, endpoint, body, usage, nil, stream, false)
}

func (s *AppService) streamGrokResponses(
	ctx context.Context,
	settings UserSettings,
	apiKey, model, effort string,
	history []map[string]any,
	previousResponseID string,
	usage *map[string]any,
	responseID *string,
	stream *externalStreamCoalescer,
) error {
	endpoint, err := resolveGrokResponsesEndpoint(settings)
	if err != nil {
		return err
	}
	input := grokResponsesInput(history)
	if previousResponseID != "" && len(history) > 0 {
		input = grokResponsesInput(history[len(history)-1:])
	}
	body := map[string]any{
		"model":  model,
		"input":  input,
		"stream": true,
	}
	if normalized := normalizeGrokEffort(effort); normalized != "" {
		body["reasoning"] = map[string]any{"effort": normalized}
	}
	if previousResponseID != "" {
		body["previous_response_id"] = previousResponseID
	}
	return s.streamGrokHTTP(ctx, apiKey, endpoint, body, usage, responseID, stream, true)
}

func grokResponsesInput(history []map[string]any) []any {
	out := make([]any, 0, len(history))
	for _, msg := range history {
		role, _ := msg["role"].(string)
		switch typed := msg["content"].(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				continue
			}
			out = append(out, map[string]any{"role": role, "content": typed})
		case []map[string]any:
			parts := make([]any, 0, len(typed))
			for _, part := range typed {
				switch strings.TrimSpace(fmt.Sprint(part["type"])) {
				case "text":
					if text := strings.TrimSpace(fmt.Sprint(part["text"])); text != "" {
						parts = append(parts, map[string]any{"type": "input_text", "text": text})
					}
				case "image_url":
					url := ""
					if img, ok := part["image_url"].(map[string]any); ok {
						url, _ = img["url"].(string)
					}
					if url != "" {
						parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
					}
				}
			}
			if len(parts) == 0 {
				continue
			}
			out = append(out, map[string]any{"role": role, "content": parts})
		default:
			if msg["content"] == nil {
				continue
			}
			out = append(out, msg)
		}
	}
	return out
}

func (s *AppService) streamGrokHTTP(
	ctx context.Context,
	apiKey, endpoint string,
	body map[string]any,
	usage *map[string]any,
	responseID *string,
	stream *externalStreamCoalescer,
	responsesWire bool,
) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		message := strings.TrimSpace(string(raw))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("Grok API HTTP %d: %s", resp.StatusCode, truncateRunes(message, 800))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	eventType := ""
	for scanner.Scan() {
		if ctx.Err() != nil {
			return context.Canceled
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			eventType = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
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
		textDelta, thoughtDelta, nextID, nextUsage := extractGrokStreamParts(eventType, chunk, responsesWire)
		if nextUsage != nil && usage != nil {
			*usage = nextUsage
		}
		if nextID != "" && responseID != nil {
			*responseID = nextID
		}
		if thoughtDelta != "" {
			stream.Push("thought", thoughtDelta)
		}
		if textDelta != "" {
			stream.Push("text", textDelta)
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return context.Canceled
		}
		return err
	}
	return nil
}

func extractGrokStreamParts(eventType string, chunk map[string]any, responsesWire bool) (text, thought, responseID string, usage map[string]any) {
	if next := normalizeTokenUsageMap(chunk["usage"]); next != nil {
		usage = next
	} else if next := normalizeTokenUsageMap(chunk); next != nil && chunk["usage"] == nil {
		if anyToFloat(chunk["prompt_tokens"]) > 0 || anyToFloat(chunk["completion_tokens"]) > 0 {
			usage = next
		}
	}
	if resp, ok := chunk["response"].(map[string]any); ok {
		responseID = firstMapString(resp, "id")
		if next := normalizeTokenUsageMap(resp["usage"]); next != nil {
			usage = next
		}
	}
	if responseID == "" {
		responseID = firstMapString(chunk, "id", "response_id", "responseId")
	}
	if responsesWire {
		typ := strings.ToLower(firstMapString(chunk, "type"))
		if typ == "" {
			typ = strings.ToLower(eventType)
		}
		delta := firstMapString(chunk, "delta", "text")
		if delta == "" {
			delta = textFromExternalValue(chunk["delta"])
		}
		switch {
		case strings.Contains(typ, "reasoning"):
			thought = delta
		case strings.Contains(typ, "output_text"), strings.HasSuffix(typ, "text.delta"), typ == "response.output_text.delta":
			text = delta
		}
		if text == "" && thought == "" {
			text, thought = extractOpenAIStreamParts(chunk)
		}
		return
	}
	text, thought = extractOpenAIStreamParts(chunk)
	return
}

func grokShouldRetryChatCompletions(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "http 404") ||
		strings.Contains(lower, "http 405") ||
		strings.Contains(lower, "unknown path") ||
		strings.Contains(lower, "no such endpoint")
}

func grokPreviousResponseExpired(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "previous_response") ||
		strings.Contains(lower, "response_id") && (strings.Contains(lower, "expired") || strings.Contains(lower, "invalid") || strings.Contains(lower, "not found"))
}
