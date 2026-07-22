package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nice_codex_desktop/internal/codex"
)

type externalThreadRecord struct {
	ThreadID  string         `json:"threadId"`
	Provider  string         `json:"provider"`
	Workspace string         `json:"workspace"`
	SessionID string         `json:"sessionId"`
	Model     string         `json:"model"`
	Name      string         `json:"name"`
	Preview   string         `json:"preview"`
	CreatedAt int64          `json:"createdAt"`
	UpdatedAt int64          `json:"updatedAt"`
	Archived  bool           `json:"archived"`
	Turns     []externalTurn `json:"turns"`
}

type externalTurn struct {
	ID          string   `json:"id"`
	UserText    string   `json:"userText"`
	Images      []string `json:"images"`
	AgentText   string   `json:"agentText"`
	Status      string   `json:"status"`
	Error       string   `json:"error,omitempty"`
	StartedAt   int64    `json:"startedAt"`
	CompletedAt int64    `json:"completedAt"`
	DurationMS  int64    `json:"durationMs"`
}

type externalRun struct {
	turnID string
	cancel context.CancelFunc
}

func externalProviderKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "__claude__", "claude-cli":
		return "claude"
	case "__gemini__", "gemini-cli":
		return "gemini"
	case "__grok__", "grok-cli":
		return "grok"
	default:
		return ""
	}
}

func externalProviderID(kind string) string {
	if kind == "claude" || kind == "gemini" || kind == "grok" {
		return "__" + kind + "__"
	}
	return ""
}

func loadExternalThreads(settingsPath string) map[string]*externalThreadRecord {
	result := make(map[string]*externalThreadRecord)
	payload, err := os.ReadFile(externalThreadsPath(settingsPath))
	if err != nil {
		return result
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return make(map[string]*externalThreadRecord)
	}
	return result
}

func externalThreadsPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "external-threads.json")
}

func (s *AppService) syncCodexThreadsIntoSessions(response map[string]any, workspace, workMode string) {
	// Callers pass the current Code/Cowork tab, but imported Codex history must
	// always land in "code" so switching tabs cannot hide / remap sessions.
	_ = normalizeWorkMode(workMode)
	data, _ := response["data"].([]any)
	if len(data) == 0 {
		return
	}
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false

	// Prefer NiceCodex-owned UUID sessions (id != backendRef) over raw Codex-id mirrors.
	findByBackend := func(backendID string) *SessionRecord {
		var mirror *SessionRecord
		for _, record := range s.sessions {
			if record == nil || isExternalSession(record) || !samePath(record.Workspace, workspace) {
				continue
			}
			if record.BackendRef == backendID || record.ID == backendID {
				if record.ID != backendID {
					return record
				}
				mirror = record
			}
		}
		return mirror
	}

	for _, value := range data {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id, _ := entry["id"].(string)
		if id == "" {
			continue
		}
		// Skip if this id is already an external NiceCodex session.
		if existing := s.sessions[id]; existing != nil && isExternalSession(existing) {
			continue
		}
		name, _ := entry["name"].(string)
		preview, _ := entry["preview"].(string)
		model, _ := entry["model"].(string)
		providerID, _ := entry["modelProvider"].(string)
		// Codex custom providers (e.g. "custom") are still Codex sessions.
		if externalProviderKind(providerID) != "" {
			continue
		}
		createdAt := int64(numericMapValue(entry, "createdAt"))
		updatedAt := int64(numericMapValue(entry, "updatedAt"))
		if createdAt == 0 {
			createdAt = now
		}
		if updatedAt == 0 {
			updatedAt = createdAt
		}
		displayName := strings.TrimSpace(name)
		if displayName == "" {
			displayName = strings.TrimSpace(preview)
		}
		if displayName == "" {
			displayName = "New task"
		}
		existing := findByBackend(id)
		if existing == nil {
			// Imported Codex history defaults to code mode so Cowork tab sync
			// does not permanently hide sessions under the wrong work mode.
			s.sessions[id] = &SessionRecord{
				ID: id, Workspace: workspace, Provider: "", ProviderID: providerID,
				BackendRef: id, Model: model, WorkMode: "code",
				Name: displayName, Preview: preview, CreatedAt: createdAt, UpdatedAt: updatedAt,
			}
			changed = true
			continue
		}
		if displayName != "" && (existing.Name == "" || existing.Name == "New task" || (name != "" && existing.Name != name)) {
			existing.Name = displayName
			changed = true
		}
		if existing.Preview != preview && preview != "" {
			existing.Preview = preview
			changed = true
		}
		if model != "" && existing.Model != model {
			existing.Model = model
			changed = true
		}
		if providerID != "" && existing.ProviderID != providerID {
			existing.ProviderID = providerID
			changed = true
		}
		if updatedAt > existing.UpdatedAt {
			existing.UpdatedAt = updatedAt
			changed = true
		}
		existing.Workspace = workspace
		existing.BackendRef = id
		existing.Archived = false
		// Keep the original workMode so Code / Cowork tabs stay separated.
		if strings.TrimSpace(existing.WorkMode) == "" {
			existing.WorkMode = "code"
			changed = true
		}
	}

	// Drop Codex-id mirrors when a NiceCodex UUID session already owns the same backendRef.
	for sid, record := range s.sessions {
		if record == nil || isExternalSession(record) || !samePath(record.Workspace, workspace) {
			continue
		}
		if record.ID == "" || record.BackendRef == "" || record.ID != record.BackendRef {
			continue
		}
		for _, other := range s.sessions {
			if other == nil || other.ID == record.ID || isExternalSession(other) {
				continue
			}
			if other.BackendRef == record.BackendRef && other.ID != other.BackendRef {
				delete(s.sessions, sid)
				changed = true
				break
			}
		}
	}

	if changed {
		s.persistSessionsLocked()
	}
}

func (s *AppService) listSessionsForWorkspace(workspace, search, workMode string) map[string]any {
	return s.listSessionsForWorkspaceFiltered(workspace, search, workMode, false)
}

func (s *AppService) listArchivedSessionsForWorkspace(workspace, search, workMode string) map[string]any {
	return s.listSessionsForWorkspaceFiltered(workspace, search, workMode, true)
}

func (s *AppService) listSessionsForWorkspaceFiltered(workspace, search, workMode string, archivedOnly bool) map[string]any {
	workMode = normalizeWorkMode(workMode)
	s.mu.Lock()
	candidates := make([]*SessionRecord, 0, len(s.sessions))
	for _, record := range s.sessions {
		if record == nil || !samePath(record.Workspace, workspace) {
			continue
		}
		if record.Archived != archivedOnly {
			continue
		}
		// Codex-only workbench: hide legacy Claude/Gemini/Grok sessions.
		if isExternalSession(record) {
			continue
		}
		if normalizeWorkMode(record.WorkMode) != workMode {
			continue
		}
		if !sessionMatchesSearch(record, search) {
			continue
		}
		candidates = append(candidates, cloneSession(record))
	}
	s.mu.Unlock()

	items := make([]any, 0, len(candidates))
	for _, record := range candidates {
		item := s.sessionThreadMap(record, false)
		item["archived"] = record.Archived
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return numericMapValue(items[left], "updatedAt") > numericMapValue(items[right], "updatedAt")
	})
	return map[string]any{"data": items}
}

func (s *AppService) codexBackendID(sessionID, workspace string) string {
	if session := s.sessionFor(sessionID, workspace); session != nil && !isExternalSession(session) {
		if ref := strings.TrimSpace(session.BackendRef); ref != "" {
			return ref
		}
	}
	return sessionID
}

func (s *AppService) forkExternalSession(source *SessionRecord) (map[string]any, error) {
	if source == nil {
		return nil, errors.New("session not found")
	}
	now := time.Now().Unix()
	forked := cloneSession(source)
	forked.ID = newUUID()
	forked.BackendRef = "" // new CLI session on next turn
	forked.Name = source.Name + " (fork)"
	forked.CreatedAt = now
	forked.UpdatedAt = now
	forked.Archived = false
	s.mu.Lock()
	s.upsertSessionLocked(forked)
	s.mu.Unlock()
	s.rememberThread(forked.ID, forked.Workspace)
	return s.sessionResponse(forked), nil
}

func (s *AppService) compactExternalSession(source *SessionRecord) error {
	if source == nil {
		return errors.New("session not found")
	}
	if len(source.Turns) == 0 {
		return nil
	}
	var summary strings.Builder
	summary.WriteString("Conversation summary (compacted):\n")
	for _, turn := range source.Turns {
		if text := strings.TrimSpace(turn.UserText); text != "" {
			summary.WriteString("- User: ")
			summary.WriteString(truncateRunes(text, 240))
			summary.WriteByte('\n')
		}
		if text := strings.TrimSpace(turn.AgentText); text != "" {
			summary.WriteString("- Agent: ")
			summary.WriteString(truncateRunes(text, 240))
			summary.WriteByte('\n')
		}
	}
	now := time.Now().Unix()
	compacted := externalTurn{
		ID: "external-turn-" + newUUID(), UserText: "Compact conversation history",
		AgentText: summary.String(), Status: "completed",
		StartedAt: now, CompletedAt: now,
	}
	s.mu.Lock()
	stored := s.sessions[source.ID]
	if stored == nil {
		s.mu.Unlock()
		return errors.New("session not found")
	}
	stored.Turns = []externalTurn{compacted}
	stored.Preview = truncateRunes(summary.String(), 120)
	stored.UpdatedAt = now
	s.persistSessionsLocked()
	s.mu.Unlock()
	s.emitExternalNotification("thread/tokenUsage/updated", map[string]any{"threadId": source.ID})
	return nil
}

func (s *AppService) rollbackExternalSession(source *SessionRecord, numTurns int) (map[string]any, error) {
	if source == nil {
		return nil, errors.New("session not found")
	}
	if numTurns < 1 {
		return nil, errors.New("rollback turn count must be at least 1")
	}
	s.mu.Lock()
	stored := s.sessions[source.ID]
	if stored == nil {
		s.mu.Unlock()
		return nil, errors.New("session not found")
	}
	if numTurns >= len(stored.Turns) {
		stored.Turns = []externalTurn{}
	} else {
		stored.Turns = stored.Turns[:len(stored.Turns)-numTurns]
	}
	stored.UpdatedAt = time.Now().Unix()
	if len(stored.Turns) > 0 {
		stored.Preview = stored.Turns[len(stored.Turns)-1].UserText
	} else {
		stored.Preview = ""
	}
	clone := cloneSession(stored)
	s.persistSessionsLocked()
	s.mu.Unlock()
	return s.sessionResponse(clone), nil
}

func numericMapValue(value any, key string) float64 {
	entry, _ := value.(map[string]any)
	switch number := entry[key].(type) {
	case float64:
		return number
	case int64:
		return float64(number)
	case int:
		return float64(number)
	default:
		return 0
	}
}

func externalTurnMap(turn externalTurn) map[string]any {
	content := []any{map[string]any{"type": "text", "text": turn.UserText}}
	for _, path := range turn.Images {
		content = append(content, map[string]any{"type": "localImage", "path": path})
	}
	items := []any{
		map[string]any{"id": turn.ID + ":user", "type": "userMessage", "status": "completed", "content": content},
		map[string]any{"id": turn.ID + ":agent", "type": "agentMessage", "status": turn.Status, "text": turn.AgentText},
	}
	result := map[string]any{
		"id": turn.ID, "status": turn.Status, "items": items,
		"startedAt": turn.StartedAt, "completedAt": turn.CompletedAt, "durationMs": turn.DurationMS,
	}
	if turn.Error != "" {
		result["error"] = map[string]any{"message": turn.Error}
	}
	return result
}

func (s *AppService) runExternalTurn(threadID, provider, workspace string, settings UserSettings, text string, images []string) (map[string]any, error) {
	if _, err := s.buildUserInput(text, images); err != nil {
		return nil, err
	}
	record := s.sessionFor(threadID, workspace)
	if record == nil || !isExternalSession(record) {
		return nil, errors.New("external provider conversation is unavailable")
	}
	// Prefer session-locked model/effort over global defaults.
	turnSettings := settings
	if record.Model != "" {
		turnSettings.Model = record.Model
	}
	if record.Effort != "" {
		turnSettings.Effort = record.Effort
	}
	turnID := "external-turn-" + newUUID()
	itemID := turnID + ":agent"
	started := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if prev := s.externalRuns[threadID]; prev != nil && prev.cancel != nil {
		prev.cancel()
	}
	s.externalRuns[threadID] = &externalRun{turnID: turnID, cancel: cancel}
	s.mu.Unlock()
	s.emitExternalNotification("thread/status/changed", map[string]any{"threadId": threadID, "status": map[string]any{"type": "active"}})
	s.emitExternalNotification("turn/started", map[string]any{
		"threadId": threadID,
		"turn":     map[string]any{"id": turnID, "status": "inProgress", "startedAt": started.Unix()},
	})
	s.emitExternalNotification("item/started", map[string]any{
		"threadId": threadID, "turnId": turnID,
		"item": map[string]any{"id": itemID, "type": "agentMessage", "status": "inProgress", "text": ""},
	})

	output, sessionID, runErr := s.executeExternalTurn(ctx, provider, record.BackendRef, workspace, turnSettings, text, images, func(delta string) {
		s.emitExternalNotification("item/agentMessage/delta", map[string]any{
			"threadId": threadID, "turnId": turnID, "itemId": itemID, "delta": delta,
		})
	})
	cancel()
	s.mu.Lock()
	delete(s.externalRuns, threadID)
	s.mu.Unlock()

	status := "completed"
	errorText := ""
	if errors.Is(runErr, context.Canceled) {
		status = "interrupted"
	} else if runErr != nil {
		status = "failed"
		errorText = runErr.Error()
	}
	completed := time.Now()
	turn := externalTurn{
		ID: turnID, UserText: strings.TrimSpace(text), Images: append([]string(nil), images...),
		AgentText: output, Status: status, Error: errorText,
		StartedAt: started.Unix(), CompletedAt: completed.Unix(), DurationMS: completed.Sub(started).Milliseconds(),
	}
	nameChanged := false
	s.mu.Lock()
	stored := s.sessions[threadID]
	if stored != nil {
		stored.Provider = provider
		stored.ProviderID = externalProviderID(provider)
		if turnSettings.Model != "" {
			stored.Model = turnSettings.Model
		}
		if sessionID != "" {
			stored.BackendRef = sessionID
		}
		stored.Turns = append(stored.Turns, turn)
		stored.UpdatedAt = completed.Unix()
		if stored.Preview == "" {
			stored.Preview = turn.UserText
			stored.Name = truncateRunes(turn.UserText, 56)
			nameChanged = true
		}
		s.persistSessionsLocked()
	}
	s.mu.Unlock()

	s.emitExternalNotification("item/completed", map[string]any{
		"threadId": threadID, "turnId": turnID,
		"item":        map[string]any{"id": itemID, "type": "agentMessage", "status": status, "text": output},
		"startedAtMs": started.UnixMilli(), "completedAtMs": completed.UnixMilli(),
	})
	turnResult := externalTurnMap(turn)
	s.emitExternalNotification("turn/completed", map[string]any{"threadId": threadID, "turn": turnResult})
	s.emitExternalNotification("thread/status/changed", map[string]any{"threadId": threadID, "status": map[string]any{"type": "idle"}})
	if nameChanged {
		s.emitExternalNotification("thread/name/updated", map[string]any{"threadId": threadID, "name": truncateRunes(turn.UserText, 56)})
	}
	return map[string]any{"turn": turnResult}, nil
}

func (s *AppService) executeExternalTurn(
	ctx context.Context,
	provider, sessionID, workspace string,
	settings UserSettings,
	text string,
	images []string,
	onDelta func(string),
) (string, string, error) {
	executable := s.externalExecutable(provider)
	if executable == "" {
		return "", sessionID, fmt.Errorf("%s CLI executable was not found", provider)
	}
	prompt := externalPrompt(text, images)
	args, generatedSessionID := externalCommandArgs(provider, sessionID, workspace, settings, prompt)
	if sessionID == "" {
		sessionID = generatedSessionID
	}
	commandPath, commandArgs := providerCommand(executable, args)
	command := exec.CommandContext(ctx, commandPath, commandArgs...)
	command.Dir = workspace
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", sessionID, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return "", sessionID, err
	}
	if err := command.Start(); err != nil {
		return "", sessionID, err
	}
	stderrResult := make(chan []byte, 1)
	go func() {
		payload, _ := io.ReadAll(io.LimitReader(stderr, 256*1024))
		stderrResult <- payload
	}()

	var output strings.Builder
	emitted := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		chunk, nextSessionID, final := parseExternalEvent(provider, scanner.Bytes())
		if nextSessionID != "" {
			sessionID = nextSessionID
		}
		if chunk == "" {
			continue
		}
		if final && emitted {
			// Final stream-json payload can supersede truncated delta fragments.
			if len(chunk) > output.Len() {
				output.Reset()
				output.WriteString(chunk)
			}
			continue
		}
		output.WriteString(chunk)
		emitted = true
		onDelta(chunk)
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	stderrText := strings.TrimSpace(string(<-stderrResult))
	if ctx.Err() != nil {
		return output.String(), sessionID, context.Canceled
	}
	if scanErr != nil {
		return output.String(), sessionID, scanErr
	}
	if waitErr != nil {
		if stderrText != "" {
			return output.String(), sessionID, errors.New(truncateRunes(stderrText, 1000))
		}
		return output.String(), sessionID, waitErr
	}
	return output.String(), sessionID, nil
}

func (s *AppService) externalExecutable(provider string) string {
	s.mu.Lock()
	for _, runtime := range s.agentProviders {
		if runtime.Kind == provider && runtime.RuntimeReady {
			executable := runtime.Executable
			s.mu.Unlock()
			return executable
		}
	}
	s.mu.Unlock()
	return findCommand(commandCandidates(provider))
}

func externalCommandArgs(provider, sessionID, workspace string, settings UserSettings, prompt string) ([]string, string) {
	generatedSessionID := sessionID
	if generatedSessionID == "" && (provider == "claude" || provider == "gemini" || provider == "grok") {
		generatedSessionID = newUUID()
	}
	model := strings.TrimSpace(settings.Model)
	effort := strings.ToLower(strings.TrimSpace(settings.Effort))
	switch provider {
	case "claude":
		args := []string{"-p", prompt, "--output-format", "stream-json", "--include-partial-messages", "--verbose"}
		if sessionID != "" {
			args = append(args, "--resume", sessionID)
		} else {
			args = append(args, "--session-id", generatedSessionID)
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		if isExternalEffort(effort, "low", "medium", "high", "xhigh", "max") {
			args = append(args, "--effort", effort)
		}
		return append(args, claudePermissionArgs(settings)...), generatedSessionID
	case "gemini":
		args := []string{"-p", prompt, "--output-format", "stream-json", "--skip-trust"}
		if sessionID != "" {
			args = append(args, "--resume", sessionID)
		} else {
			args = append(args, "--session-id", generatedSessionID)
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		return append(args, geminiPermissionArgs(settings)...), generatedSessionID
	case "grok":
		args := []string{"--single", prompt, "--output-format", "streaming-json", "--cwd", workspace}
		if sessionID != "" {
			args = append(args, "--resume", sessionID)
		} else {
			args = append(args, "--session-id", generatedSessionID)
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		if isExternalEffort(effort, "low", "medium", "high") {
			args = append(args, "--reasoning-effort", effort)
		}
		return append(args, grokPermissionArgs(settings)...), generatedSessionID
	default:
		return nil, generatedSessionID
	}
}

func isExternalEffort(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func claudePermissionArgs(settings UserSettings) []string {
	if settings.Sandbox == "danger-full-access" && settings.ApprovalPolicy == "never" {
		return []string{"--dangerously-skip-permissions"}
	}
	if settings.Sandbox == "read-only" {
		return []string{"--permission-mode", "plan"}
	}
	return []string{"--permission-mode", "manual"}
}

func geminiPermissionArgs(settings UserSettings) []string {
	if settings.Sandbox == "danger-full-access" && settings.ApprovalPolicy == "never" {
		return []string{"--approval-mode", "yolo"}
	}
	if settings.Sandbox == "read-only" {
		return []string{"--approval-mode", "plan"}
	}
	return []string{"--approval-mode", "default"}
}

func grokPermissionArgs(settings UserSettings) []string {
	mode := "default"
	if settings.Sandbox == "danger-full-access" && settings.ApprovalPolicy == "never" {
		mode = "bypassPermissions"
	} else if settings.Sandbox == "read-only" {
		mode = "plan"
	}
	return []string{"--permission-mode", mode}
}

func externalPrompt(text string, images []string) string {
	message := strings.TrimSpace(text)
	if len(images) == 0 {
		return message
	}
	var builder strings.Builder
	builder.WriteString(message)
	builder.WriteString("\n\nLocal image attachments available to inspect:\n")
	for _, path := range images {
		builder.WriteString("- ")
		builder.WriteString(path)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func parseExternalEvent(provider string, line []byte) (string, string, bool) {
	var event map[string]any
	if err := json.Unmarshal(line, &event); err != nil {
		return "", "", false
	}
	sessionID := firstMapString(event, "session_id", "sessionId")
	eventType := strings.ToLower(firstMapString(event, "type", "event"))
	if provider == "claude" {
		if eventType == "stream_event" {
			streamEvent, _ := event["event"].(map[string]any)
			delta, _ := streamEvent["delta"].(map[string]any)
			return firstMapString(delta, "text"), sessionID, false
		}
		if eventType == "assistant" {
			message, _ := event["message"].(map[string]any)
			return textFromExternalValue(message["content"]), sessionID, true
		}
		if eventType == "result" {
			return firstMapString(event, "result"), sessionID, true
		}
		return "", sessionID, false
	}
	if provider == "gemini" {
		if eventType == "message" && strings.EqualFold(firstMapString(event, "role"), "assistant") {
			return textFromExternalValue(event["content"]), sessionID, false
		}
		if eventType == "result" {
			return textFromExternalValue(event["response"]), sessionID, true
		}
		return "", sessionID, false
	}
	if strings.Contains(eventType, "delta") {
		return textFromExternalValue(event["delta"]), sessionID, false
	}
	if eventType == "assistant" || eventType == "message" {
		return textFromExternalValue(event["content"]), sessionID, false
	}
	if eventType == "result" || eventType == "final" || eventType == "completed" {
		return textFromExternalValue(event["result"]), sessionID, true
	}
	return "", sessionID, false
}

func firstMapString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func textFromExternalValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var builder strings.Builder
		for _, item := range typed {
			builder.WriteString(textFromExternalValue(item))
		}
		return builder.String()
	case map[string]any:
		for _, key := range []string{"text", "content", "delta", "output", "response", "result"} {
			if text := textFromExternalValue(typed[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func (s *AppService) interruptExternalTurn(threadID, turnID string) bool {
	s.mu.Lock()
	run := s.externalRuns[threadID]
	if run == nil || run.turnID != turnID {
		s.mu.Unlock()
		return false
	}
	cancel := run.cancel
	s.mu.Unlock()
	cancel()
	return true
}

func (s *AppService) cancelExternalRuns() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.externalRuns))
	for _, run := range s.externalRuns {
		cancels = append(cancels, run.cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}


func (s *AppService) emitExternalNotification(method string, data any) {
	s.app.Event.Emit("codex:event", codex.Event{Type: "notification", Method: method, Data: data})
}

func newUUID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		now := uint64(time.Now().UnixNano())
		return fmt.Sprintf("%08x-%04x-4000-8000-%012x", uint32(now>>32), uint16(now), now&0xffffffffffff)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
