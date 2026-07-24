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

const (
	externalStreamFlushInterval = 32 * time.Millisecond
	externalStreamFlushBytes    = 512
)

// externalStreamCoalescer preserves CLI stdout order while reducing the number
// of fine-grained events crossing the Wails bridge.
type externalStreamCoalescer struct {
	onStream  func(kind, chunk string)
	kind      string
	buffer    strings.Builder
	lastFlush time.Time
}

func newExternalStreamCoalescer(onStream func(kind, chunk string)) *externalStreamCoalescer {
	return &externalStreamCoalescer{onStream: onStream}
}

func (c *externalStreamCoalescer) Push(kind, chunk string) {
	if c == nil || c.onStream == nil || chunk == "" {
		return
	}
	if kind == "replace" {
		if c.kind != "" && c.kind != kind {
			c.Flush()
		}
		c.kind = kind
		c.buffer.Reset()
		c.buffer.WriteString(chunk)
		if time.Since(c.lastFlush) >= externalStreamFlushInterval {
			c.Flush()
		}
		return
	}
	if c.kind != "" && c.kind != kind {
		c.Flush()
	}
	c.kind = kind
	c.buffer.WriteString(chunk)
	if c.buffer.Len() >= externalStreamFlushBytes || strings.Contains(chunk, "\n") || time.Since(c.lastFlush) >= externalStreamFlushInterval {
		c.Flush()
	}
}

func (c *externalStreamCoalescer) Flush() {
	if c == nil || c.buffer.Len() == 0 {
		return
	}
	kind := c.kind
	chunk := c.buffer.String()
	c.buffer.Reset()
	c.kind = ""
	c.lastFlush = time.Now()
	if c.onStream != nil {
		c.onStream(kind, chunk)
	}
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

	output, sessionID, usage, runErr := s.executeExternalTurn(ctx, provider, record.BackendRef, workspace, turnSettings, text, images, func(kind, delta string) {
		if kind != "" && kind != "text" {
			return
		}
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
	if b := breakdownFromUsageMap(usage); b.valid() {
		// External agents launched from the Codex workbench still attribute to codex
		// unless the provider itself is grok/claude dual-runtime.
		runtime := "codex"
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "grok":
			runtime = "grok"
		case "claude":
			runtime = "claude"
		}
		s.persistTurnUsage(runtime, threadID, turnID, b, completed)
		s.emitExternalNotification("thread/tokenUsage/updated", map[string]any{
			"threadId": threadID,
			"turnId":   turnID,
			"tokenUsage": map[string]any{
				"last":  usage,
				"total": usage,
			},
		})
	}
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
	onStream func(kind, chunk string),
) (string, string, map[string]any, error) {
	executable := s.externalExecutable(provider)
	if executable == "" {
		return "", sessionID, nil, fmt.Errorf("%s CLI executable was not found", provider)
	}
	prompt := externalPrompt(text, images)
	args, generatedSessionID := externalCommandArgs(provider, sessionID, workspace, settings, prompt)
	if sessionID == "" {
		sessionID = generatedSessionID
	}
	if onStream != nil && sessionID != "" {
		// Expose the concrete native session before the process starts so runtimes can
		// observe provider-owned tool history without mixing it into the text stream.
		onStream("session", sessionID)
	}
	commandPath, commandArgs := providerCommand(executable, args)
	command := exec.CommandContext(ctx, commandPath, commandArgs...)
	command.Dir = workspace
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", sessionID, nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return "", sessionID, nil, err
	}
	if err := command.Start(); err != nil {
		return "", sessionID, nil, err
	}
	stderrResult := make(chan []byte, 1)
	go func() {
		payload, _ := io.ReadAll(io.LimitReader(stderr, 256*1024))
		stderrResult <- payload
	}()

	var output strings.Builder
	var usage map[string]any
	var streamErr string
	emitted := false
	stream := newExternalStreamCoalescer(onStream)
	// Claude stream-json has two live channels:
	//  1) stream_event content_block_delta (true increments) → append
	//  2) type=assistant partial messages (--include-partial-messages) → full snapshots
	// The first text channel seen owns the turn. Mixing snapshots and increments
	// produces duplicated or reordered output on proxy-backed Claude runtimes.
	claudeSnapshotFallback := ""
	claudeTextSource := ""
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		chunk, nextSessionID, final, kind := parseExternalEvent(provider, line)
		if nextSessionID != "" {
			sessionID = nextSessionID
		}
		// Always try to capture spend fields — Grok end events often have empty text
		// so kind/final alone is not enough; also catch mid-stream usage if present.
		if next := extractExternalUsage(line); next != nil {
			usage = next
		}
		if kind == "error" {
			if chunk != "" {
				streamErr = chunk
			} else if streamErr == "" {
				streamErr = "provider stream error"
			}
			continue
		}
		if kind == "thought" {
			stream.Push("thought", chunk)
			continue
		}
		if chunk == "" {
			continue
		}
		if provider == "claude" && final {
			claudeSnapshotFallback = mergeExternalSnapshot(claudeSnapshotFallback, chunk)
			if claudeTextSource == "" {
				claudeTextSource = "snapshot"
			}
			if claudeTextSource != "snapshot" {
				continue
			}
			output.Reset()
			output.WriteString(claudeSnapshotFallback)
			emitted = true
			stream.Push("replace", output.String())
			continue
		}
		if final && emitted {
			// Non-Claude final payloads may supersede truncated delta fragments.
			if len(chunk) > output.Len() {
				output.Reset()
				output.WriteString(chunk)
			}
			continue
		}
		// Incremental text only (Claude stream_event deltas, Grok text, …).
		if provider == "claude" {
			if claudeTextSource == "" {
				claudeTextSource = "delta"
			}
			if claudeTextSource != "delta" {
				continue
			}
		}
		output.WriteString(chunk)
		emitted = true
		stream.Push("text", chunk)
	}
	stream.Flush()
	// No live content at all — fall back to last full assistant/result snapshot.
	if provider == "claude" && !emitted && claudeSnapshotFallback != "" {
		output.Reset()
		output.WriteString(claudeSnapshotFallback)
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	stderrText := strings.TrimSpace(string(<-stderrResult))
	if ctx.Err() != nil {
		return output.String(), sessionID, usage, context.Canceled
	}
	if streamErr != "" {
		return output.String(), sessionID, usage, errors.New(truncateRunes(streamErr, 1000))
	}
	if scanErr != nil {
		return output.String(), sessionID, usage, scanErr
	}
	if waitErr != nil {
		if stderrText != "" {
			return output.String(), sessionID, usage, errors.New(truncateRunes(stderrText, 1000))
		}
		return output.String(), sessionID, usage, waitErr
	}
	return output.String(), sessionID, usage, nil
}

func mergeExternalSnapshot(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" || strings.Contains(current, next) {
		return current
	}
	if strings.HasPrefix(next, current) || strings.Contains(next, current) {
		return next
	}
	return current + "\n\n" + next
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

// claudePermissionArgs maps NiceCodex permission controls to official Claude Code flags.
// Official --permission-mode choices (CLI 2.x):
//
//	acceptEdits | auto | bypassPermissions | manual | dontAsk | plan
//
// Headless -p sessions cannot answer interactive prompts, so we avoid plain "manual"
// for the default "ask" profile and use acceptEdits (auto-approve file edits).
func claudePermissionArgs(settings UserSettings) []string {
	// Explicit mode wins when set (Claude-native setting).
	if mode := normalizeClaudePermissionMode(settings.ClaudePermissionMode); mode != "" {
		if mode == "bypassPermissions" {
			return []string{"--dangerously-skip-permissions"}
		}
		return []string{"--permission-mode", mode}
	}
	// Legacy sandbox + approval mapping (composer ask / auto / strict).
	if settings.Sandbox == "danger-full-access" && settings.ApprovalPolicy == "never" {
		return []string{"--dangerously-skip-permissions"}
	}
	if settings.Sandbox == "read-only" {
		return []string{"--permission-mode", "plan"}
	}
	// workspace-write + on-request → acceptEdits (workable in print/stream-json)
	return []string{"--permission-mode", "acceptEdits"}
}

func normalizeClaudePermissionMode(value string) string {
	switch strings.TrimSpace(value) {
	case "acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan":
		return strings.TrimSpace(value)
	default:
		return ""
	}
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
	// Headless `-p` cannot answer interactive prompts — a tool that would ask is
	// cancelled and often ends the agentic loop early (looks like a mid-turn disconnect).
	// Only `bypassPermissions` / `--yolo` actually enable always-approve via CLI flags;
	// `acceptEdits`/`plan` on --permission-mode are accepted but do not enable those policies.
	// See ~/.grok/docs/user-guide/14-headless-mode.md and 22-permissions-and-safety.md.
	if settings.Sandbox == "read-only" {
		return []string{"--permission-mode", "default"}
	}
	// Desktop workbench already scoped the workspace — auto-approve tools unattended.
	return []string{"--yolo"}
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

// extractExternalUsage normalizes spend fields into a stable Codex-like shape:
//
//	inputTokens          = uncached prompt tokens
//	cachedInputTokens    = cache hits
//	outputTokens         = completion tokens
//	reasoningOutputTokens= thinking tokens
//	totalTokens          = billed total when provided
//
// Sources (verified against live CLI + ~/.grok sessions):
//   - headless streaming-json end:
//     {"type":"end","usage":{"input_tokens","cache_read_input_tokens","output_tokens","reasoning_tokens","total_tokens"}}
//   - session updates.jsonl turn_completed:
//     usage: {inputTokens, cachedReadTokens, outputTokens, reasoningTokens, totalTokens}
//     (ACP inputTokens is FULL prompt incl. cache; headless input_tokens is uncached-only)
func extractExternalUsage(line []byte) map[string]any {
	var event map[string]any
	if err := json.Unmarshal(line, &event); err != nil || event == nil {
		return nil
	}
	raw := event["usage"]
	// Claude Code stream-json / transcripts: usage often lives on message.usage
	// for type=assistant, or top-level usage on type=result.
	if raw == nil {
		if msg, ok := event["message"].(map[string]any); ok {
			raw = msg["usage"]
		}
	}
	if raw == nil {
		if nested, ok := event["result"].(map[string]any); ok {
			raw = nested["usage"]
		}
	}
	if raw == nil {
		if nested, ok := event["data"].(map[string]any); ok {
			raw = nested["usage"]
		}
	}
	// Nested session/update envelopes (updates.jsonl).
	if raw == nil {
		if params, ok := event["params"].(map[string]any); ok {
			if update, ok := params["update"].(map[string]any); ok {
				raw = update["usage"]
			}
			if raw == nil {
				raw = params["usage"]
			}
		}
	}
	if raw == nil {
		if update, ok := event["update"].(map[string]any); ok {
			raw = update["usage"]
		}
	}
	// OpenAI-compatible stream final chunk often has usage at the root.
	if raw == nil {
		if _, hasPrompt := event["prompt_tokens"]; hasPrompt {
			raw = event
		} else if _, hasPrompt := event["promptTokens"]; hasPrompt {
			raw = event
		} else if _, hasInput := event["input_tokens"]; hasInput {
			raw = event
		} else if _, hasInput := event["inputTokens"]; hasInput {
			raw = event
		}
	}
	return normalizeTokenUsageMap(raw)
}

func normalizeTokenUsageMap(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok || raw == nil {
		return nil
	}

	// Detect source shape before normalizing.
	// Headless uses snake_case input_tokens (uncached) + cache_read_input_tokens.
	// ACP / session updates use inputTokens (often FULL) + cachedReadTokens.
	_, hasSnakeInput := raw["input_tokens"]
	_, hasSnakeCache := raw["cache_read_input_tokens"]
	_, hasCachedRead := raw["cachedReadTokens"]
	_, hasCamelInput := raw["inputTokens"]

	inputRaw := anyToFloat(raw["input_tokens"])
	if inputRaw <= 0 {
		inputRaw = anyToFloat(raw["inputTokens"])
	}
	if inputRaw <= 0 {
		inputRaw = anyToFloat(raw["prompt_tokens"])
	}
	if inputRaw <= 0 {
		inputRaw = anyToFloat(raw["promptTokens"])
	}

	// Cache field names across Grok headless / Grok session / Codex rollout:
	// cache_read_input_tokens | cachedReadTokens | cached_input_tokens | cachedInputTokens
	cached := anyToFloat(raw["cache_read_input_tokens"])
	if cached <= 0 {
		cached = anyToFloat(raw["cached_input_tokens"]) // Codex token_count
	}
	if cached <= 0 {
		cached = anyToFloat(raw["cachedReadTokens"]) // Grok updates.jsonl
	}
	if cached <= 0 {
		cached = anyToFloat(raw["cacheReadInputTokens"])
	}
	if cached <= 0 {
		cached = anyToFloat(raw["cachedInputTokens"])
	}
	if cached <= 0 {
		cached = anyToFloat(raw["cached_tokens"])
	}
	// Claude prompt caching reports newly-written prompt tokens separately from
	// cache reads. Both occupy the active request context.
	cacheCreation := anyToFloat(raw["cache_creation_input_tokens"])
	if cacheCreation <= 0 {
		cacheCreation = anyToFloat(raw["cacheCreationInputTokens"])
	}
	cached += cacheCreation

	output := anyToFloat(raw["output_tokens"])
	if output <= 0 {
		output = anyToFloat(raw["outputTokens"])
	}
	if output <= 0 {
		output = anyToFloat(raw["completion_tokens"])
	}
	if output <= 0 {
		output = anyToFloat(raw["completionTokens"])
	}

	reasoning := anyToFloat(raw["reasoning_tokens"])
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["reasoningTokens"])
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["reasoningOutputTokens"])
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["reasoning_output_tokens"])
	}

	total := anyToFloat(raw["total_tokens"])
	if total <= 0 {
		total = anyToFloat(raw["totalTokens"])
	}

	// Normalize input to *uncached* tokens for a consistent UI.
	// - Grok headless: input_tokens is uncached; total = uncached + cache + output.
	// - Codex token_count / Grok ACP: input*_tokens is FULL prompt; total ≈ fullInput + output.
	input := inputRaw
	inputIsFull := false
	if cached > 0 && inputRaw >= cached && total > 0 {
		if almostEqualFloat(total, inputRaw+output, 2) {
			// full input + output == total  → input includes cache (Codex + Grok ACP)
			inputIsFull = true
		} else if hasCachedRead && hasCamelInput && !hasSnakeInput {
			inputIsFull = true
		}
	}
	// Headless check: if total ≈ uncached + cache + output, keep input as uncached.
	if inputIsFull && hasSnakeInput && hasSnakeCache && almostEqualFloat(total, inputRaw+cached+output, 2) {
		// Ambiguous; prefer headless uncached semantics when both formulas match poorly.
		// Only keep full when the uncached formula does NOT fit.
		if almostEqualFloat(total, inputRaw+cached+output, 2) && !almostEqualFloat(total, inputRaw+output, 2) {
			inputIsFull = false
		}
	}
	if inputIsFull {
		input = inputRaw - cached
		if input < 0 {
			input = 0
		}
	}

	// Prefer reported total; otherwise compose.
	if total <= 0 {
		if inputIsFull {
			total = inputRaw + output
		} else {
			total = input + cached + output
		}
	}
	if total <= 0 && (input > 0 || cached > 0 || output > 0 || reasoning > 0) {
		total = input + cached + output
	}
	if total <= 0 && input <= 0 && output <= 0 && cached <= 0 && reasoning <= 0 {
		return nil
	}
	return map[string]any{
		"inputTokens":           int64(input),
		"cachedInputTokens":     int64(cached),
		"outputTokens":          int64(output),
		"reasoningOutputTokens": int64(reasoning),
		"totalTokens":           int64(total),
	}
}

func almostEqualFloat(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func tokenTotalFromUsage(usage map[string]any) int64 {
	if usage == nil {
		return 0
	}
	total := int64(anyToFloat(usage["totalTokens"]))
	if total > 0 {
		return total
	}
	return int64(anyToFloat(usage["inputTokens"])) +
		int64(anyToFloat(usage["cachedInputTokens"])) +
		int64(anyToFloat(usage["outputTokens"])) +
		int64(anyToFloat(usage["reasoningOutputTokens"]))
}

// parseExternalEvent returns (chunk, sessionID, final, kind).
// kind is "text" | "thought" | "".
func parseExternalEvent(provider string, line []byte) (string, string, bool, string) {
	var event map[string]any
	if err := json.Unmarshal(line, &event); err != nil {
		return "", "", false, ""
	}
	sessionID := firstMapString(event, "session_id", "sessionId")
	eventType := strings.ToLower(firstMapString(event, "type", "event"))
	if provider == "claude" {
		// Claude Code stream-json — Anthropic-native AND proxy backends (GPT / GLM / etc.):
		//   {"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"…"}}}
		//   {"type":"assistant","message":{"content":[…]}}           // partial/full snapshots
		//   {"type":"result","result":"…"}
		//   OpenAI-style: {"choices":[{"delta":{"content":"…"}}]} / {"choices":[{"message":{"content":"…"}}]}
		//   Generic: {"type":"text","text"|"data":"…"} / {"type":"content","content":"…"}
		//   {"type":"message","role":"assistant","content":"…" | […]}
		if text, ok := claudeOpenAIStyleDelta(event); ok {
			return text, sessionID, false, "text"
		}
		if eventType == "stream_event" {
			streamEvent, _ := event["event"].(map[string]any)
			if streamEvent == nil {
				return "", sessionID, false, ""
			}
			innerType := strings.ToLower(firstMapString(streamEvent, "type"))
			// content_block_delta is the normal Anthropic path; some proxies omit inner type
			// and put text directly on event/delta.
			if innerType == "content_block_delta" || innerType == "" {
				if delta, _ := streamEvent["delta"].(map[string]any); delta != nil {
					deltaType := strings.ToLower(firstMapString(delta, "type"))
					switch {
					case deltaType == "input_json_delta":
						return "", sessionID, false, ""
					case strings.Contains(deltaType, "thinking") || strings.Contains(deltaType, "reasoning"):
						return firstMapString(delta, "thinking", "text", "reasoning", "content"), sessionID, false, "thought"
					case deltaType == "text_delta" || strings.Contains(deltaType, "text") || deltaType == "" || deltaType == "input_text":
						if t := firstMapString(delta, "text", "content"); t != "" {
							return t, sessionID, false, "text"
						}
					}
				}
				// Proxy may put text on the stream event itself.
				if t := firstMapString(streamEvent, "text", "content", "data"); t != "" {
					return t, sessionID, false, "text"
				}
			}
			return "", sessionID, false, ""
		}
		if eventType == "assistant" || (eventType == "message" && strings.EqualFold(firstMapString(event, "role"), "assistant")) {
			message, _ := event["message"].(map[string]any)
			var text string
			if message != nil {
				text = textFromExternalValue(message["content"])
				if text == "" {
					text = textFromClaudeContentBlocks(message["content"], false)
				}
			}
			if text == "" {
				text = textFromExternalValue(event["content"])
			}
			if text == "" {
				text = firstMapString(event, "text", "data", "result")
			}
			// final=true → snapshot / replace path (not live-appended).
			return text, sessionID, true, "text"
		}
		if eventType == "result" || eventType == "final" || eventType == "completed" {
			text := firstMapString(event, "result", "text", "content", "data")
			if text == "" {
				text = textFromExternalValue(event["result"])
			}
			if text == "" {
				text = textFromExternalValue(event["content"])
			}
			return text, sessionID, true, "text"
		}
		// Top-level content_block_delta (some wrappers flatten stream_event).
		if inner := strings.ToLower(eventType); inner == "content_block_delta" || strings.HasSuffix(inner, "content_block_delta") {
			delta, _ := event["delta"].(map[string]any)
			deltaType := strings.ToLower(firstMapString(delta, "type"))
			if strings.Contains(deltaType, "thinking") {
				return firstMapString(delta, "thinking", "text"), sessionID, false, "thought"
			}
			if deltaType == "text_delta" || strings.Contains(deltaType, "text") || deltaType == "" {
				return firstMapString(delta, "text", "content"), sessionID, false, "text"
			}
			return "", sessionID, false, ""
		}
		// Generic incremental types used by proxies / GLM-style gateways.
		switch eventType {
		case "text", "content", "delta", "response.delta", "response_delta", "output_text.delta", "output_text_delta":
			text := firstMapString(event, "text", "content", "data", "delta", "output")
			if text == "" {
				text = textFromExternalValue(event["data"])
			}
			if text == "" {
				text = textFromExternalValue(event["delta"])
			}
			if text == "" {
				text = textFromExternalValue(event["content"])
			}
			return text, sessionID, false, "text"
		case "thought", "reasoning", "thinking":
			text := firstMapString(event, "text", "content", "data", "thinking", "reasoning")
			if text == "" {
				text = textFromExternalValue(event["data"])
			}
			return text, sessionID, false, "thought"
		}
		// OpenAI chat.completion.chunk without going through helper (already tried).
		return "", sessionID, false, ""
	}
	if provider == "gemini" {
		if eventType == "message" && strings.EqualFold(firstMapString(event, "role"), "assistant") {
			return textFromExternalValue(event["content"]), sessionID, false, "text"
		}
		if eventType == "result" {
			return textFromExternalValue(event["response"]), sessionID, true, "text"
		}
		return "", sessionID, false, ""
	}
	// Official Grok Build headless streaming-json (verified against CLI sample):
	//   {"type":"thought","data":"The"}
	//   {"type":"text","data":"hi"}
	//   {"type":"end","sessionId":"...","stopReason":"EndTurn"}
	// See ~/.grok/docs/user-guide/14-headless-mode.md
	if provider == "grok" {
		switch eventType {
		case "text", "assistant_delta", "message_delta", "content_block_delta":
			text := firstMapString(event, "data", "text", "delta", "content")
			if text == "" {
				text = textFromExternalValue(event["data"])
			}
			if text == "" {
				text = textFromExternalValue(event["delta"])
			}
			return text, sessionID, false, "text"
		case "thought", "reasoning", "thinking":
			text := firstMapString(event, "data", "text", "delta", "content")
			if text == "" {
				text = textFromExternalValue(event["data"])
			}
			return text, sessionID, false, "thought"
		case "end", "result", "final", "completed":
			// End usually has no full text; keep accumulated deltas.
			text := firstMapString(event, "text", "result", "data", "content")
			if text == "" {
				text = textFromExternalValue(event["result"])
			}
			return text, sessionID, true, "text"
		case "error":
			msg := firstMapString(event, "message", "error", "data", "text")
			if msg == "" {
				msg = textFromExternalValue(event["message"])
			}
			if msg == "" {
				msg = "Grok stream error"
			}
			return msg, sessionID, true, "error"
		default:
			return "", sessionID, false, ""
		}
	}
	if strings.Contains(eventType, "delta") {
		return textFromExternalValue(event["delta"]), sessionID, false, "text"
	}
	if eventType == "assistant" || eventType == "message" {
		return textFromExternalValue(event["content"]), sessionID, false, "text"
	}
	if eventType == "result" || eventType == "final" || eventType == "completed" {
		return textFromExternalValue(event["result"]), sessionID, true, "text"
	}
	if eventType == "text" {
		text := firstMapString(event, "data", "text")
		if text == "" {
			text = textFromExternalValue(event["data"])
		}
		return text, sessionID, false, "text"
	}
	if eventType == "thought" || eventType == "reasoning" {
		text := firstMapString(event, "data", "text")
		if text == "" {
			text = textFromExternalValue(event["data"])
		}
		return text, sessionID, false, "thought"
	}
	return "", sessionID, false, ""
}

func firstMapString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

// claudeOpenAIStyleDelta extracts incremental text from OpenAI-compatible chunks
// that some Claude Code proxies (GPT / GLM / custom gateways) emit inside stream-json.
// Returns ok=false when the line is not an OpenAI-style delta.
func claudeOpenAIStyleDelta(event map[string]any) (string, bool) {
	// {"choices":[{"delta":{"content":"x"}}]}
	// {"choices":[{"delta":{"content":[{"type":"text","text":"x"}]}}]}
	// {"choices":[{"message":{"content":"full"}}]}  → treat as non-delta (caller handles assistant)
	choices, ok := event["choices"].([]any)
	if !ok || len(choices) == 0 {
		// {"delta":{"content":"x"}} flattened
		if delta, ok := event["delta"].(map[string]any); ok {
			if t := claudeExtractDeltaContent(delta); t != "" {
				return t, true
			}
		}
		return "", false
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return "", false
	}
	if delta, ok := choice["delta"].(map[string]any); ok {
		if t := claudeExtractDeltaContent(delta); t != "" {
			return t, true
		}
		// reasoning_content used by some reasoning models
		if t := firstMapString(delta, "reasoning_content", "reasoning"); t != "" {
			return t, true
		}
	}
	return "", false
}

func claudeExtractDeltaContent(delta map[string]any) string {
	if t := firstMapString(delta, "content", "text", "output_text"); t != "" {
		return t
	}
	// content as array of parts
	if text := textFromExternalValue(delta["content"]); text != "" {
		return text
	}
	if text := textFromClaudeContentBlocks(delta["content"], false); text != "" {
		return text
	}
	return ""
}

// textFromClaudeContentBlocks extracts text (or optional thinking) from Claude message content arrays.
func textFromClaudeContentBlocks(value any, thinking bool) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		blockType := strings.ToLower(firstMapString(block, "type"))
		if thinking {
			if blockType == "thinking" || blockType == "reasoning" {
				builder.WriteString(firstMapString(block, "thinking", "text"))
			}
			continue
		}
		if blockType == "text" || blockType == "" {
			builder.WriteString(firstMapString(block, "text"))
		}
	}
	return builder.String()
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
