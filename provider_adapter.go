package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	ID        string   `json:"id"`
	UserText  string   `json:"userText"`
	Images    []string `json:"images"`
	AgentText string   `json:"agentText"`
	// Items preserves the provider event order (assistant text segments and
	// tools). Older sessions omit this field and continue to render AgentText.
	Items       []map[string]any `json:"items,omitempty"`
	Status      string           `json:"status"`
	Error       string           `json:"error,omitempty"`
	StartedAt   int64            `json:"startedAt"`
	CompletedAt int64            `json:"completedAt"`
	DurationMS  int64            `json:"durationMs"`
	Usage       map[string]any   `json:"usage,omitempty"`
}

type externalRun struct {
	turnID       string
	clientTurnID string
	cancel       context.CancelFunc
}

const (
	externalStreamFlushInterval = 48 * time.Millisecond
	externalStreamFlushBytes    = 2 * 1024
	externalStreamSnapshotBytes = 1_000_000
)

// externalStreamCoalescer preserves CLI stdout order while reducing the number
// of fine-grained events crossing the Wails bridge.
type externalStreamCoalescer struct {
	onStream  func(kind, chunk string)
	kind      string
	buffer    strings.Builder
	lastFlush time.Time
}

// openCodeTextDeduper handles the snapshot-shaped text parts emitted by
// `opencode run --format json`. A part may be reported more than once, and
// later reports can contain the complete text rather than a delta.
type openCodeTextDeduper struct {
	parts     map[string]string
	anonymous string
}

type antigravityLiveStepUpdate struct {
	kind            string
	delta           string
	previous        string
	next            string
	segmentSnapshot string
	replace         bool
}

type antigravityLiveStepAssembler struct {
	steps       map[string]string
	activeKind  string
	segmentText string
	ordinal     uint64
}

// agy 1.1.27 emits thinking token counts but omits thinking text on stdout.
// Read only newly appended native transcript records, never a previous turn.
// Summaries become available when the native planner commits a step, not token
// by token. Keep them before that step's response/tools whenever stdout arrives.
type antigravityThoughtCursor struct {
	path     string
	offset   int64
	thoughts map[int]string
	emitted  map[int]bool
	bytes    int
}

func newAntigravityThoughtCursor(sessionID string) *antigravityThoughtCursor {
	c := &antigravityThoughtCursor{thoughts: make(map[int]string), emitted: make(map[int]bool)}
	c.path = findAntigravityNativeSessionFile(resolveGeminiHome(), sessionID)
	if info, err := os.Stat(c.path); err == nil {
		c.offset = info.Size()
	}
	return c
}

func (c *antigravityThoughtCursor) read(sessionID string, step map[string]any) string {
	index, err := strconv.Atoi(antigravityEventStepIndex(step))
	if err != nil || c.bytes >= externalStreamSnapshotBytes {
		return ""
	}
	if c.path == "" {
		c.path = findAntigravityNativeSessionFile(resolveGeminiHome(), sessionID)
	}
	file, err := os.Open(c.path)
	if err != nil {
		return ""
	}
	defer file.Close()
	if _, err := file.Seek(c.offset, io.SeekStart); err != nil {
		return ""
	}
	reader := bufio.NewReader(io.LimitReader(file, 8*1024*1024))
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// An incomplete last JSONL record is retried on the next event.
			break
		}
		c.offset += int64(len(line))
		var record map[string]any
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		key, err := strconv.Atoi(antigravityEventStepIndex(record))
		text := firstMapString(record, "thinking", "reasoning")
		if err == nil && text != "" && !c.emitted[key] && len(c.thoughts) < 512 && c.bytes+len(text) <= externalStreamSnapshotBytes {
			c.bytes += len(text) - len(c.thoughts[key])
			c.thoughts[key] = text
		}
	}
	keys := make([]int, 0, len(c.thoughts))
	for key := range c.thoughts {
		if key <= index && !c.emitted[key] {
			keys = append(keys, key)
		}
	}
	sort.Ints(keys)
	var text strings.Builder
	for _, key := range keys {
		if text.Len() > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(c.thoughts[key])
		c.emitted[key] = true
		delete(c.thoughts, key)
	}
	return text.String()
}

func newAntigravityLiveStepAssembler() *antigravityLiveStepAssembler {
	return &antigravityLiveStepAssembler{steps: make(map[string]string)}
}

func (a *antigravityLiveStepAssembler) Barrier() {
	if a == nil {
		return
	}
	a.activeKind = ""
	a.segmentText = ""
}

func (a *antigravityLiveStepAssembler) Next(event map[string]any, kind, chunk string) antigravityLiveStepUpdate {
	if a == nil || chunk == "" || (kind != "text" && kind != "thought") {
		return antigravityLiveStepUpdate{}
	}
	a.ordinal++
	if a.activeKind != kind {
		a.activeKind = kind
		a.segmentText = ""
	}
	fallback := fmt.Sprintf("event:%d", a.ordinal)
	key := kind + "\x00" + antigravityEventStepKey(event, fallback)
	previous := a.steps[key]
	next, delta, replace := reconcileAntigravityEventText(previous, chunk, event)
	if next == previous || (delta == "" && !replace) {
		return antigravityLiveStepUpdate{}
	}
	a.steps[key] = next
	if replace {
		if strings.HasSuffix(a.segmentText, previous) {
			a.segmentText = strings.TrimSuffix(a.segmentText, previous) + next
		} else {
			a.segmentText = next
		}
	} else {
		a.segmentText += delta
	}
	return antigravityLiveStepUpdate{
		kind: kind, delta: delta, previous: previous, next: next,
		segmentSnapshot: a.segmentText, replace: replace,
	}
}

func newOpenCodeTextDeduper() *openCodeTextDeduper {
	return &openCodeTextDeduper{parts: make(map[string]string)}
}

func (d *openCodeTextDeduper) next(event map[string]any, text string) string {
	if d == nil || strings.TrimSpace(text) == "" {
		return ""
	}
	id := openCodeEventPartID(event)
	if id == "" {
		previous := d.anonymous
		d.anonymous = text
		return openCodeTextSuffix(previous, text)
	}
	previous := d.parts[id]
	d.parts[id] = text
	return openCodeTextSuffix(previous, text)
}

func openCodeTextSuffix(previous, next string) string {
	if previous == "" {
		return next
	}
	if next == previous || strings.HasPrefix(previous, next) {
		return ""
	}
	if strings.HasPrefix(next, previous) {
		return next[len(previous):]
	}
	// Divergent snapshot: the part content was replaced, not appended. Emit only
	// the suffix after the longest common prefix so already-streamed text is
	// never duplicated; the part's stored text still advances to the new base.
	common := 0
	max := len(previous)
	if len(next) < max {
		max = len(next)
	}
	for common < max && previous[common] == next[common] {
		common++
	}
	return next[common:]
}

func newExternalStreamCoalescer(onStream func(kind, chunk string)) *externalStreamCoalescer {
	return &externalStreamCoalescer{onStream: onStream}
}

func (c *externalStreamCoalescer) Push(kind, chunk string) {
	if c == nil || c.onStream == nil || chunk == "" {
		return
	}
	if kind == "replace" || kind == "thought_replace" {
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

func appendBoundedExternalStreamText(builder *strings.Builder, value string) {
	if builder == nil || value == "" {
		return
	}
	remaining := externalStreamSnapshotBytes - builder.Len()
	if remaining <= 0 {
		return
	}
	if len(value) <= remaining {
		builder.WriteString(value)
		return
	}
	end := remaining
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	if end > 0 {
		builder.WriteString(value[:end])
	}
}

func replaceExternalBuilderSuffix(builder *strings.Builder, previous, next string) bool {
	if builder == nil {
		return false
	}
	current := builder.String()
	if !strings.HasSuffix(current, previous) {
		return false
	}
	builder.Reset()
	builder.WriteString(strings.TrimSuffix(current, previous))
	builder.WriteString(next)
	return true
}

func externalProviderKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "__claude__", "claude-cli":
		return "claude"
	case "__gemini__", "gemini-cli":
		return "gemini"
	case "__grok__", "grok-cli":
		return "grok"
	case "__opencode__", "opencode-cli":
		return "opencode"
	default:
		return ""
	}
}

func externalProviderID(kind string) string {
	if kind == "claude" || kind == "gemini" || kind == "grok" || kind == "opencode" {
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
	if s.sessions == nil {
		s.sessions = make(map[string]*SessionRecord)
	}
	changed := false
	pendingAllocation := false
	for _, pendingID := range s.pendingCodexSessions {
		if pending := s.sessions[pendingID]; pending != nil &&
			!pending.Archived && !isExternalSession(pending) &&
			samePath(pending.Workspace, workspace) && strings.TrimSpace(pending.BackendRef) == "" {
			pendingAllocation = true
			break
		}
	}

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
			// thread/list can race the first thread/start before its backend id is
			// written to the NiceCodex UUID. Importing a raw Codex-id mirror in that
			// window creates two sidebar rows for one conversation. The allocation
			// response/event will bind the UUID; a later list refresh can then update it.
			if pendingAllocation {
				continue
			}
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
	return s.listSessionsForWorkspaceRuntimeFiltered(
		workspace,
		search,
		workMode,
		false,
		normalizeRuntime(s.Settings().ActiveRuntime),
	)
}

func (s *AppService) listArchivedSessionsForWorkspace(workspace, search, workMode string) map[string]any {
	return s.listSessionsForWorkspaceRuntimeFiltered(
		workspace,
		search,
		workMode,
		true,
		normalizeRuntime(s.Settings().ActiveRuntime),
	)
}

func (s *AppService) listSessionsForWorkspaceRuntimeFiltered(
	workspace string,
	search string,
	workMode string,
	archivedOnly bool,
	runtimeID string,
) map[string]any {
	workMode = normalizeWorkMode(workMode)
	activeRuntime := normalizeRuntime(runtimeID)
	s.mu.Lock()
	candidates := make([]*SessionRecord, 0, len(s.sessions))
	for _, record := range s.sessions {
		if record == nil || !samePath(record.Workspace, workspace) {
			continue
		}
		if record.Archived != archivedOnly {
			continue
		}
		// Keep each runtime's conversation index isolated. Gemini/OpenCode use the
		// Codex-compatible timeline store but must never leak into Codex/Claude/Grok.
		provider := strings.ToLower(strings.TrimSpace(record.Provider))
		if provider == "" {
			provider = externalProviderKind(record.ProviderID)
		}
		if isExternalSession(record) && provider != activeRuntime {
			continue
		}
		if !isExternalSession(record) && activeRuntime != "codex" {
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
	items := []any{map[string]any{"id": turn.ID + ":user", "type": "userMessage", "status": "completed", "content": content}}
	if len(turn.Items) > 0 {
		for _, item := range turn.Items {
			if item == nil {
				continue
			}
			items = append(items, cloneExternalTimelineItem(item, turn.Status))
		}
	}
	if len(items) == 1 {
		items = append(items, map[string]any{"id": turn.ID + ":agent", "type": "agentMessage", "status": turn.Status, "text": turn.AgentText})
	}
	if turn.Error != "" {
		// Keep a terminal provider failure in the transcript. A queued follower can
		// replace the global feedback immediately, but the failed turn still needs
		// to explain why it stopped after the session is switched or reopened.
		items = append(items, map[string]any{
			"id": turn.ID + ":error", "type": "agentMessage", "status": "failed", "text": turn.Error,
		})
	}
	result := map[string]any{
		"id": turn.ID, "status": turn.Status, "items": items,
		"startedAt": turn.StartedAt, "completedAt": turn.CompletedAt, "durationMs": turn.DurationMS,
	}
	if len(turn.Usage) > 0 {
		result["usage"] = turn.Usage
	}
	if turn.Error != "" {
		result["error"] = map[string]any{"message": turn.Error}
	}
	return result
}

func cloneExternalTimelineItem(item map[string]any, turnStatus string) map[string]any {
	clone := make(map[string]any, len(item)+1)
	for key, value := range item {
		clone[key] = value
	}
	if strings.TrimSpace(firstMapString(clone, "status")) == "" {
		clone["status"] = turnStatus
	}
	return clone
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
	switch provider {
	case "claude":
		turnSettings.Model, turnSettings.Effort = settings.ClaudeModel, settings.ClaudeEffort
	case "gemini":
		turnSettings.Model, turnSettings.Effort = settings.GeminiModel, settings.GeminiEffort
	case "grok":
		turnSettings.Model, turnSettings.Effort = settings.GrokBuildModel, settings.GrokEffort
	case "opencode":
		turnSettings.Model, turnSettings.Effort = settings.OpenCodeModel, settings.OpenCodeEffort
	}
	if record.Model != "" {
		turnSettings.Model = record.Model
	}
	if record.Effort != "" {
		turnSettings.Effort = record.Effort
	}
	turnID := "external-turn-" + newUUID()
	var eventSequence uint64
	emit := func(method string, data any) {
		if payload, ok := data.(map[string]any); ok {
			eventSequence++
			payload["externalEventSequence"] = eventSequence
			payload["externalEventTurnId"] = turnID
		}
		s.emitExternalNotification(method, data)
	}
	started := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.externalRuns == nil {
		s.externalRuns = make(map[string]*externalRun)
	}
	if s.externalRuns[threadID] != nil {
		s.mu.Unlock()
		cancel()
		return nil, errors.New("an external provider turn is already running for this session")
	}
	s.externalRuns[threadID] = &externalRun{turnID: turnID, cancel: cancel}
	s.mu.Unlock()
	emit("thread/status/changed", map[string]any{"threadId": threadID, "status": map[string]any{"type": "active"}})
	emit("turn/started", map[string]any{
		"threadId": threadID,
		"turn":     map[string]any{"id": turnID, "status": "inProgress", "startedAt": started.Unix()},
	})
	// Publish the accepted user row with the provider turn id. The frontend
	// replaces its optimistic local row by matching the text, which also repairs
	// a queue-admission race where a temporary "provider already running" retry
	// could remove that local row before the accepted retry began streaming.
	userContent := []any{map[string]any{"type": "text", "text": strings.TrimSpace(text)}}
	for _, path := range images {
		userContent = append(userContent, map[string]any{"type": "localImage", "path": path})
	}
	emit("item/completed", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"item": map[string]any{
			"id": turnID + ":user", "type": "userMessage", "status": "completed", "content": userContent,
		},
	})

	// External CLIs can interleave assistant text, reasoning and tools. Keep a
	// separate timeline item for every text/reasoning segment so a later tool is
	// rendered at its actual position instead of being appended after the whole
	// assistant response.
	var timelineItems []map[string]any
	timelineIndexes := make(map[string]int)
	segmentNumber := 0
	activeAgentID := ""
	activeAgentText := strings.Builder{}
	activeAgentStreamText := strings.Builder{}
	activeAgentStreamSequence := uint64(0)
	activeReasoningID := ""
	activeReasoningText := strings.Builder{}
	activeReasoningStreamText := strings.Builder{}
	activeReasoningStreamSequence := uint64(0)
	emitAgentDelta := func(itemID, delta string, replace bool) {
		if replace {
			activeAgentStreamText.Reset()
			appendBoundedExternalStreamText(&activeAgentStreamText, activeAgentText.String())
		} else {
			appendBoundedExternalStreamText(&activeAgentStreamText, delta)
		}
		activeAgentStreamSequence++
		emit("item/agentMessage/delta", map[string]any{
			"threadId": threadID, "turnId": turnID, "itemId": itemID, "delta": delta,
			"streamSequence": activeAgentStreamSequence, "streamText": activeAgentStreamText.String(), "streamMode": "replace",
		})
	}
	emitReasoningDelta := func(itemID, delta string, replace bool) {
		if replace {
			activeReasoningStreamText.Reset()
			appendBoundedExternalStreamText(&activeReasoningStreamText, activeReasoningText.String())
		} else {
			appendBoundedExternalStreamText(&activeReasoningStreamText, delta)
		}
		activeReasoningStreamSequence++
		emit("item/reasoning/summaryTextDelta", map[string]any{
			"threadId": threadID, "turnId": turnID, "itemId": itemID, "delta": delta,
			"streamSequence": activeReasoningStreamSequence, "streamText": activeReasoningStreamText.String(), "streamMode": "replace",
		})
	}
	recordTimelineItem := func(item map[string]any) {
		if item == nil {
			return
		}
		id := strings.TrimSpace(firstMapString(item, "id", "itemId"))
		if id == "" {
			return
		}
		if index, exists := timelineIndexes[id]; exists {
			timelineItems[index] = cloneExternalTimelineItem(item, "inProgress")
			return
		}
		timelineIndexes[id] = len(timelineItems)
		timelineItems = append(timelineItems, cloneExternalTimelineItem(item, "inProgress"))
	}
	ensureAgentSegment := func() string {
		if activeAgentID != "" {
			return activeAgentID
		}
		if activeReasoningID != "" {
			item := map[string]any{"id": activeReasoningID, "type": "reasoning", "status": "completed", "summary": activeReasoningText.String()}
			recordTimelineItem(item)
			emit("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": item, "replace": true})
			activeReasoningID = ""
			activeReasoningText.Reset()
		}
		activeAgentID = fmt.Sprintf("%s:agent:%d", turnID, segmentNumber)
		segmentNumber++
		activeAgentText.Reset()
		activeAgentStreamText.Reset()
		activeAgentStreamSequence = 0
		item := map[string]any{"id": activeAgentID, "type": "agentMessage", "status": "inProgress", "text": ""}
		recordTimelineItem(item)
		emit("item/started", map[string]any{"threadId": threadID, "turnId": turnID, "item": item})
		return activeAgentID
	}
	ensureReasoningSegment := func() string {
		if activeReasoningID != "" {
			return activeReasoningID
		}
		if activeAgentID != "" {
			item := map[string]any{"id": activeAgentID, "type": "agentMessage", "status": "completed", "text": activeAgentText.String()}
			recordTimelineItem(item)
			emit("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": item, "replace": true})
			activeAgentID = ""
			activeAgentText.Reset()
		}
		activeReasoningID = fmt.Sprintf("%s:reasoning:%d", turnID, segmentNumber)
		segmentNumber++
		activeReasoningText.Reset()
		activeReasoningStreamText.Reset()
		activeReasoningStreamSequence = 0
		item := map[string]any{"id": activeReasoningID, "type": "reasoning", "status": "inProgress", "summary": ""}
		recordTimelineItem(item)
		emit("item/started", map[string]any{"threadId": threadID, "turnId": turnID, "item": item})
		return activeReasoningID
	}
	completeSegments := func(status string) {
		if activeReasoningID != "" {
			item := map[string]any{"id": activeReasoningID, "type": "reasoning", "status": status, "summary": activeReasoningText.String()}
			recordTimelineItem(item)
			emit("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": item, "replace": true})
			activeReasoningID = ""
			activeReasoningText.Reset()
		}
		if activeAgentID != "" {
			item := map[string]any{"id": activeAgentID, "type": "agentMessage", "status": status, "text": activeAgentText.String()}
			recordTimelineItem(item)
			emit("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": item, "replace": true})
			activeAgentID = ""
			activeAgentText.Reset()
		}
	}

	// Keep the visible user turn unchanged while giving native CLIs the same
	// session objective that Codex receives through developer instructions.
	providerText := sessionGoalPrompt(record, text)
	output, sessionID, usage, runErr := s.executeExternalTurn(ctx, provider, record.BackendRef, workspace, turnSettings, providerText, images, func(kind, delta string) {
		if kind == "tool" || kind == "compact" {
			completeSegments("completed")
			if kind == "compact" {
				item := map[string]any{
					"id": turnID + ":compact", "type": "contextCompaction", "status": "completed",
				}
				recordTimelineItem(item)
				emit("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": item, "runtime": provider})
				return
			}
			items := decodeExternalToolTimelineItems(delta)
			for _, item := range items {
				recordTimelineItem(item)
			}
			if len(items) > 0 {
				s.emitExternalToolNotification(threadID, turnID, provider, delta, emit)
			}
			return
		}
		if kind == "session" {
			s.bindExternalSession(threadID, provider, delta)
			return
		}
		if kind == "thought" || kind == "thought_replace" {
			itemID := ensureReasoningSegment()
			if kind == "thought_replace" {
				activeReasoningText.Reset()
				activeReasoningText.WriteString(delta)
				emitReasoningDelta(itemID, delta, true)
			} else {
				activeReasoningText.WriteString(delta)
				emitReasoningDelta(itemID, delta, false)
			}
			return
		}
		if kind == "replace" {
			itemID := ensureAgentSegment()
			activeAgentText.Reset()
			activeAgentText.WriteString(delta)
			item := map[string]any{"id": itemID, "type": "agentMessage", "status": "inProgress", "text": delta}
			recordTimelineItem(item)
			emitAgentDelta(itemID, delta, true)
			return
		}
		if kind != "" && kind != "text" {
			return
		}
		itemID := ensureAgentSegment()
		activeAgentText.WriteString(delta)
		emitAgentDelta(itemID, delta, false)
	})
	// Gemini/OpenCode can omit usage on stdout. OpenCode also emits one usage
	// record per tool step, while this stream loop only retains the latest map.
	// Their native stores are authoritative, but only for messages written since
	// this turn began; using a whole-session snapshot would duplicate old turns.
	home, _ := os.UserHomeDir()
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gemini":
		if nativeUsage := collectGeminiSessionUsage(resolveGeminiHome(), sessionID, started.UnixMilli()); nativeUsage != nil {
			usage = nativeUsage
		}
	case "opencode":
		if nativeUsage := collectOpenCodeSessionUsage(home, sessionID, started.UnixMilli()); nativeUsage != nil {
			usage = nativeUsage
		}
	}
	// Some CLI versions expose the native session only in their final result.
	// Bind it while this run is still the authoritative owner so a native-history
	// refresh cannot import a second row between process exit and final persistence.
	if sessionID != "" {
		s.bindExternalSession(threadID, provider, sessionID)
	}
	cancel()
	s.mu.Lock()
	currentRun := s.externalRuns[threadID]
	finishedRunStillOwnsThread := currentRun != nil && currentRun.turnID == turnID
	if finishedRunStillOwnsThread {
		delete(s.externalRuns, threadID)
	}
	s.mu.Unlock()

	status := "completed"
	errorText := ""
	if errors.Is(runErr, context.Canceled) {
		status = "interrupted"
	} else if runErr != nil {
		status = "failed"
		errorText = runErr.Error()
	}
	// A provider may emit a tool start/update without a final result. Once the
	// process has reached a terminal turn state, close those rows explicitly so
	// the UI cannot keep showing a tool as running until the session is reopened.
	for index, item := range timelineItems {
		if item == nil || firstMapString(item, "type") != "dynamicToolCall" {
			continue
		}
		itemStatus := strings.ToLower(strings.TrimSpace(firstMapString(item, "status")))
		if itemStatus != "" && itemStatus != "inprogress" && itemStatus != "running" && itemStatus != "pending" {
			continue
		}
		closed := cloneExternalTimelineItem(item, status)
		closed["status"] = status
		closed["success"] = status == "completed"
		timelineItems[index] = closed
		encoded, _ := json.Marshal(closed)
		s.emitExternalToolNotification(threadID, turnID, provider, string(encoded), emit)
	}
	// A provider may only send a final full snapshot (or omit text events
	// entirely). Reconcile it into the current segment before closing items.
	// The active segment has not yet been copied back into timelineItems, so it
	// must be included explicitly. Otherwise a fully streamed answer is mistaken
	// for a missing suffix and appended a second time at process exit.
	if output != "" {
		rendered := ""
		for _, item := range timelineItems {
			if firstMapString(item, "type") == "agentMessage" {
				if firstMapString(item, "id", "itemId") == activeAgentID {
					rendered += activeAgentText.String()
				} else {
					rendered += firstMapString(item, "text")
				}
			}
		}
		if rendered != output {
			switch {
			case rendered == "":
				itemID := ensureAgentSegment()
				activeAgentText.Reset()
				activeAgentText.WriteString(output)
				item := map[string]any{"id": itemID, "type": "agentMessage", "status": "inProgress", "text": output}
				recordTimelineItem(item)
				emitAgentDelta(itemID, output, true)
			case strings.HasPrefix(output, rendered):
				itemID := ensureAgentSegment()
				suffix := strings.TrimPrefix(output, rendered)
				if suffix != "" {
					activeAgentText.WriteString(suffix)
					emitAgentDelta(itemID, suffix, false)
				}
			default:
				// A terminal provider snapshot can revise text across multiple segments.
				// Clear superseded rows and place the authoritative answer in the last
				// agent row without disturbing tools between those rows.
				targetID := activeAgentID
				if targetID == "" {
					for index := len(timelineItems) - 1; index >= 0; index-- {
						if firstMapString(timelineItems[index], "type") == "agentMessage" {
							targetID = firstMapString(timelineItems[index], "id", "itemId")
							break
						}
					}
				}
				if targetID == "" {
					targetID = ensureAgentSegment()
				}
				for index, existing := range timelineItems {
					if firstMapString(existing, "type") != "agentMessage" {
						continue
					}
					itemID := firstMapString(existing, "id", "itemId")
					text := ""
					if itemID == targetID {
						text = output
					}
					itemStatus := status
					if itemID == activeAgentID {
						itemStatus = "inProgress"
					}
					item := map[string]any{"id": itemID, "type": "agentMessage", "status": itemStatus, "text": text}
					timelineItems[index] = cloneExternalTimelineItem(item, itemStatus)
					if itemID == activeAgentID {
						activeAgentText.Reset()
						activeAgentText.WriteString(output)
						emitAgentDelta(itemID, output, true)
					} else {
						emit("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": item, "replace": true})
					}
				}
			}
		}
	}
	completeSegments(status)
	if len(timelineItems) == 0 && output != "" {
		item := map[string]any{"id": turnID + ":agent:0", "type": "agentMessage", "status": status, "text": output}
		recordTimelineItem(item)
		emit("item/completed", map[string]any{"threadId": threadID, "turnId": turnID, "item": item, "replace": true})
	}
	completed := time.Now()
	if normalizedProvider := strings.ToLower(strings.TrimSpace(provider)); normalizedProvider == "gemini" || normalizedProvider == "opencode" {
		// The native session database may contain the authoritative usage even
		// when a CLI stream omits its final usage event.
		s.invalidateExternalUsageCache(normalizedProvider)
		s.invalidateNativeHistoryCache(normalizedProvider, sessionID)
		s.invalidateNativeSessionSync(normalizedProvider, workspace)
	}
	if b := breakdownFromUsageMap(usage); b.valid() {
		// Attribute usage to the active native runtime. The Codex bucket is only
		// the fallback for legacy sessions without an external provider ID.
		runtime := "codex"
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "grok":
			runtime = "grok"
		case "claude":
			runtime = "claude"
		case "gemini":
			runtime = "gemini"
		case "opencode":
			runtime = "opencode"
		}
		s.persistTurnUsage(runtime, threadID, turnID, b, completed)
		tokenUsage := map[string]any{
			"last":  usage,
			"total": usage,
		}
		if contextTokens, source := normalizedUsageContext(runtime, usage); contextTokens > 0 {
			tokenUsage["contextTokens"] = contextTokens
			tokenUsage["contextUsageSource"] = source
		}
		emit("thread/tokenUsage/updated", map[string]any{
			"threadId":   threadID,
			"turnId":     turnID,
			"runtime":    runtime,
			"tokenUsage": tokenUsage,
		})
	}
	turn := externalTurn{
		ID: turnID, UserText: strings.TrimSpace(text), Images: append([]string(nil), images...),
		AgentText: output, Items: timelineItems, Status: status, Error: errorText,
		StartedAt: started.Unix(), CompletedAt: completed.Unix(), DurationMS: completed.Sub(started).Milliseconds(),
		Usage: usage,
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

	turnResult := externalTurnMap(turn)
	emit("turn/completed", map[string]any{"threadId": threadID, "turn": turnResult})
	if finishedRunStillOwnsThread {
		emit("thread/status/changed", map[string]any{"threadId": threadID, "status": map[string]any{"type": "idle"}})
	}
	if nameChanged {
		emit("thread/name/updated", map[string]any{"threadId": threadID, "name": truncateRunes(turn.UserText, 56)})
	}
	return map[string]any{"turn": turnResult}, nil
}

// emitExternalToolNotification maps native CLI tool lifecycle events onto the
// app-server item protocol already consumed by the Codex timeline store. The
// same item id is reused for running/completed updates, so a long tool call is
// updated in place instead of being appended again at the end of the turn.
func (s *AppService) emitExternalToolNotification(threadID, turnID, provider, encoded string, emit func(string, any)) {
	for _, tool := range decodeExternalToolTimelineItems(encoded) {
		itemID := strings.TrimSpace(firstMapString(tool, "id", "itemId", "callId"))
		if itemID == "" {
			continue
		}
		status := strings.TrimSpace(firstMapString(tool, "status"))
		if status == "" {
			status = "inProgress"
		}
		item := cloneExternalTimelineItem(tool, status)
		item["id"] = itemID
		item["type"] = "dynamicToolCall"
		item["status"] = status
		method := "item/started"
		if status == "completed" || status == "failed" || status == "interrupted" {
			method = "item/completed"
		}
		emit(method, map[string]any{
			"threadId": threadID,
			"turnId":   turnID,
			"item":     item,
			"runtime":  normalizeExternalRuntime(provider),
		})
	}
}

func decodeExternalToolTimelineItem(encoded string) (map[string]any, bool) {
	var item map[string]any
	if json.Unmarshal([]byte(encoded), &item) != nil || item == nil {
		return nil, false
	}
	if strings.TrimSpace(firstMapString(item, "id", "itemId", "callId")) == "" {
		return nil, false
	}
	return item, true
}

func decodeExternalToolTimelineItems(encoded string) []map[string]any {
	var raw any
	if json.Unmarshal([]byte(encoded), &raw) != nil || raw == nil {
		return nil
	}
	items := make([]map[string]any, 0, 1)
	switch value := raw.(type) {
	case map[string]any:
		if item, ok := decodeExternalToolTimelineItemMap(value); ok {
			items = append(items, item)
		}
	case []any:
		for _, entry := range value {
			if itemMap, ok := entry.(map[string]any); ok {
				if item, valid := decodeExternalToolTimelineItemMap(itemMap); valid {
					items = append(items, item)
				}
			}
		}
	}
	return items
}

func decodeExternalToolTimelineItemMap(item map[string]any) (map[string]any, bool) {
	if item == nil || strings.TrimSpace(firstMapString(item, "id", "itemId", "callId")) == "" {
		return nil, false
	}
	return item, true
}

var geminiExternalEnvironmentKeys = []string{
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"GOOGLE_GEMINI_BASE_URL",
	"GOOGLE_GEMINI_API_VERSION",
}

// sanitizedExternalProcessEnv repairs provider-specific child environments.
// Antigravity no longer loads the legacy ~/.gemini/.env file itself, while
// Grok on Windows must not inherit MSYS markers in a console-less process.
func sanitizedExternalProcessEnv(provider string) []string {
	if provider == "gemini" {
		env := os.Environ()
		changed := false
		for _, name := range geminiExternalEnvironmentKeys {
			if strings.TrimSpace(os.Getenv(name)) != "" {
				continue
			}
			value := codex.PersistentEnvironmentValue(name)
			if strings.TrimSpace(value) == "" {
				value = readEnvValue(filepath.Join(resolveGeminiHome(), ".env"), name)
			}
			if strings.TrimSpace(value) == "" {
				continue
			}
			env = replaceEnvironmentValue(env, name, value)
			changed = true
		}
		if changed {
			return env
		}
		return nil
	}
	if provider != "grok" || runtime.GOOS != "windows" {
		return nil
	}
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if upper == "HOME" || upper == "MSYSTEM" || strings.HasPrefix(upper, "MSYS") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// grokTurnInactivityLimit bounds how long a Grok turn may stay silent on both
// the stdout stream and its own events file before the watchdog kills it.
const grokTurnInactivityLimit = 10 * time.Minute

func watchGrokTurnInactivity(stop <-chan struct{}, command *exec.Cmd, eventsPath string, lastOutputTime *atomic.Int64) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			idle := time.Since(time.Unix(0, lastOutputTime.Load()))
			if idle < grokTurnInactivityLimit {
				continue
			}
			if eventsPath != "" {
				if info, err := os.Stat(eventsPath); err == nil && time.Since(info.ModTime()) < grokTurnInactivityLimit {
					// The CLI is still writing lifecycle events even though the
					// stdout stream is quiet — treat it as alive.
					lastOutputTime.Store(info.ModTime().UnixNano())
					continue
				}
			}
			if command.Process != nil {
				_ = command.Process.Kill()
			}
			return
		}
	}
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
	var nativeThoughts *antigravityThoughtCursor
	if provider == "gemini" && isAntigravityExecutable(executable) {
		nativeThoughts = newAntigravityThoughtCursor(sessionID)
	}
	args, generatedSessionID := externalCommandArgsForExecutable(provider, executable, sessionID, workspace, settings, prompt)
	if sessionID == "" {
		sessionID = generatedSessionID
	}
	commandPath, commandArgs, resolveErr := providerCommand(executable, args)
	if resolveErr != nil {
		return "", sessionID, nil, resolveErr
	}
	command := exec.CommandContext(ctx, commandPath, commandArgs...)
	command.Dir = workspace
	command.Env = sanitizedExternalProcessEnv(provider)
	grokEventsPath, grokEventsOffset := grokTurnEventsCursor(provider, sessionID)
	grokTurnStartedAt := time.Now().UTC().Add(-2 * time.Second)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", sessionID, nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return "", sessionID, nil, err
	}
	cleanup, err := startManagedBackgroundProcess(ctx, command)
	if err != nil {
		return "", sessionID, nil, err
	}
	defer cleanup()
	// Grok's run_terminal_command can deadlock during its Windows shell bootstrap
	// (no console + MSYS-style env), leaving the CLI silent forever while the
	// turn stays "running". Kill the process after prolonged silence on both the
	// stdout stream and the CLI's own events file so the turn can fail visibly
	// instead of hanging until a manual interrupt.
	var lastOutputTime atomic.Int64
	lastOutputTime.Store(time.Now().UnixNano())
	stopGrokWatchdog := make(chan struct{})
	defer close(stopGrokWatchdog)
	if provider == "grok" {
		go watchGrokTurnInactivity(stopGrokWatchdog, command, grokEventsPath, &lastOutputTime)
	}
	var grokTerminalOutcome <-chan string
	if provider == "grok" {
		grokTerminalOutcome = monitorGrokTurnEnd(
			ctx, sessionID, grokEventsPath, grokEventsOffset, grokTurnStartedAt, cleanup,
		)
	}
	if onStream != nil && sessionID != "" {
		// The process now owns this concrete native id. Publish it before reading
		// stdout so session switches cannot outrun lifecycle/event routing.
		onStream("session", sessionID)
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
	openCodeText := newOpenCodeTextDeduper()
	antigravityText := newAntigravityLiveStepAssembler()
	antigravityConversationID := ""
	antigravityTrajectoryID := ""
	// Claude stream-json has two live channels:
	//  1) stream_event content_block_delta (true increments) → append
	//  2) type=assistant partial messages (--include-partial-messages) → full snapshots
	// The first text channel seen owns the turn. Mixing snapshots and increments
	// produces duplicated or reordered output on proxy-backed Claude runtimes.
	claudeSnapshotFallback := ""
	claudeTextSource := ""
	sawGrokTerminal := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		lastOutputTime.Store(time.Now().UnixNano())
		line := scanner.Bytes()
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil || event == nil {
			continue
		}
		isAntigravityEvent := provider == "gemini" && (antigravityStepPayload(event) != nil || antigravityResultPayload(event) != nil || antigravityInitPayload(event) != nil)
		antigravityChildEvent := false
		if isAntigravityEvent {
			eventConversation := antigravityEventConversationID(event)
			if antigravityConversationID == "" && eventConversation != "" {
				antigravityConversationID = eventConversation
			}
			eventTrajectory := antigravityEventTrajectoryID(event)
			if antigravityTrajectoryID == "" && eventTrajectory != "" && antigravityIsUserEvent(event) {
				antigravityTrajectoryID = eventTrajectory
			}
			antigravityChildEvent = antigravityEventBelongsToChild(event, antigravityConversationID, antigravityTrajectoryID)
		}
		chunk, nextSessionID, final, kind := parseExternalEvent(provider, event)
		if antigravityChildEvent && (kind == "text" || kind == "thought") {
			chunk = ""
			kind = ""
		}
		grokTerminal := provider == "grok" && final
		if grokTerminal {
			sawGrokTerminal = true
		}
		if nextSessionID != "" {
			if sessionID == "" && onStream != nil {
				onStream("session", nextSessionID)
			}
			sessionID = nextSessionID
		}
		if nativeThoughts != nil && isAntigravityEvent && !antigravityChildEvent {
			if kind == "thought" || kind == "thought_replace" {
				// If a newer CLI supplies live thinking, it remains authoritative.
				nativeThoughts = nil
			} else if antigravityStepPayload(event) != nil {
				if thought := nativeThoughts.read(sessionID, event); thought != "" {
					stream.Push("thought", thought)
					stream.Flush()
				}
			}
		}
		// Always try to capture spend fields — Grok end events often have empty text
		// so kind/final alone is not enough; also catch mid-stream usage if present.
		if next := extractExternalUsage(event); next != nil && !antigravityChildEvent {
			usage = next
		}
		if kind == "tool" || kind == "compact" {
			// Flush text accumulated before structured activity first. Without this
			// barrier a coalesced text delta can cross the bridge after a tool event.
			stream.Flush()
			antigravityText.Barrier()
			if onStream != nil {
				onStream(kind, chunk)
			}
			continue
		}
		if kind == "error" {
			if chunk != "" {
				streamErr = chunk
			} else if streamErr == "" {
				streamErr = "provider stream error"
			}
			if grokTerminal {
				break
			}
			continue
		}
		if isAntigravityEvent && antigravityStepPayload(event) != nil && (kind == "text" || kind == "thought") {
			update := antigravityText.Next(event, kind, chunk)
			if update.kind == "" {
				continue
			}
			if update.kind == "thought" {
				if update.replace {
					stream.Push("thought_replace", update.segmentSnapshot)
				} else {
					stream.Push("thought", update.delta)
				}
				continue
			}
			if update.replace {
				if !replaceExternalBuilderSuffix(&output, update.previous, update.next) {
					output.WriteString(update.delta)
				}
				stream.Push("replace", update.segmentSnapshot)
			} else {
				output.WriteString(update.delta)
				stream.Push("text", update.delta)
			}
			emitted = true
			continue
		}
		if kind == "thought" {
			stream.Push("thought", chunk)
			continue
		}
		if provider == "opencode" && kind == "text" {
			chunk = openCodeText.next(event, chunk)
		}
		if chunk == "" {
			if grokTerminal {
				break
			}
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
		if isAntigravityEvent && final && chunk != "" {
			// result.response is the authoritative full turn snapshot. The caller
			// reconciles it against timeline segments after the pending stream flush.
			output.Reset()
			output.WriteString(chunk)
			emitted = true
			continue
		}
		if final && emitted {
			// Non-Claude final payloads may supersede truncated delta fragments.
			if len(chunk) > output.Len() {
				output.Reset()
				output.WriteString(chunk)
			}
			if grokTerminal {
				break
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
		if grokTerminal {
			break
		}
	}
	// Grok's official `end` event is a turn boundary. Some CLI builds leave their
	// MCP children alive afterwards, so waiting for process EOF would lock queues.
	if sawGrokTerminal {
		cleanup()
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
	historyTerminalOutcome := ""
	if grokTerminalOutcome != nil {
		select {
		case historyTerminalOutcome = <-grokTerminalOutcome:
		default:
		}
	}
	grokTerminated := sawGrokTerminal || historyTerminalOutcome != ""
	if ctx.Err() != nil {
		return output.String(), sessionID, usage, context.Canceled
	}
	if streamErr != "" {
		return output.String(), sessionID, usage, errors.New(truncateRunes(streamErr, 1000))
	}
	// Closing Grok's process tree after a confirmed terminal event may make the
	// stdout pipe report a platform-specific read error instead of a clean EOF.
	if scanErr != nil && !grokTerminated {
		return output.String(), sessionID, usage, scanErr
	}
	if historyTerminalOutcome != "" && historyTerminalOutcome != "completed" {
		return output.String(), sessionID, usage, fmt.Errorf("Grok turn ended with status %s", historyTerminalOutcome)
	}
	if waitErr != nil && !grokTerminated {
		if stderrText != "" {
			return output.String(), sessionID, usage, errors.New(truncateRunes(stderrText, 1000))
		}
		return output.String(), sessionID, usage, waitErr
	}
	return output.String(), sessionID, usage, nil
}

func grokTurnEventsCursor(provider, sessionID string) (string, int64) {
	if provider != "grok" || strings.TrimSpace(sessionID) == "" {
		return "", 0
	}
	session, err := findGrokNativeSession(sessionID)
	if err != nil {
		return "", 0
	}
	path := filepath.Join(session.Dir, "events.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return path, 0
	}
	return path, info.Size()
}

func monitorGrokTurnEnd(
	ctx context.Context,
	sessionID, initialPath string,
	initialOffset int64,
	startedAt time.Time,
	cleanup func(),
) <-chan string {
	result := make(chan string, 1)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		path := initialPath
		offset := initialOffset
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if path == "" {
					session, err := findGrokNativeSession(sessionID)
					if err != nil {
						continue
					}
					path = filepath.Join(session.Dir, "events.jsonl")
					offset = 0
				}
				outcome, nextOffset := readGrokTurnEnd(path, offset, startedAt)
				offset = nextOffset
				if outcome == "" {
					continue
				}
				result <- outcome
				cleanup()
				return
			}
		}
	}()
	return result
}

func readGrokTurnEnd(path string, offset int64, startedAt time.Time) (string, int64) {
	file, err := os.Open(path)
	if err != nil {
		return "", offset
	}
	defer file.Close()
	if info, statErr := file.Stat(); statErr == nil && info.Size() < offset {
		offset = 0
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return "", offset
	}
	payload, err := io.ReadAll(io.LimitReader(file, 512*1024))
	if err != nil || len(payload) == 0 {
		return "", offset
	}
	lastNewline := bytes.LastIndexByte(payload, '\n')
	if lastNewline < 0 {
		return "", offset
	}
	complete := payload[:lastNewline+1]
	nextOffset := offset + int64(len(complete))
	for _, line := range bytes.Split(complete, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event map[string]any
		if json.Unmarshal(line, &event) != nil || !strings.EqualFold(firstMapString(event, "type"), "turn_ended") {
			continue
		}
		if rawTime := strings.TrimSpace(firstMapString(event, "ts", "timestamp")); rawTime != "" {
			if eventTime, parseErr := time.Parse(time.RFC3339Nano, rawTime); parseErr == nil && eventTime.Before(startedAt) {
				continue
			}
		}
		outcome := strings.ToLower(strings.TrimSpace(firstMapString(event, "outcome", "status")))
		if outcome == "" {
			outcome = "completed"
		}
		return outcome, nextOffset
	}
	return "", nextOffset
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
	// Gemini may have both legacy `gemini` and replacement `agy` binaries. Always
	// re-resolve this provider so a newly installed agy is preferred over a stale
	// cached runtime entry.
	if provider == "gemini" {
		if executable := findGeminiExecutable(); executable != "" {
			return executable
		}
	}
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
	executable := ""
	if provider == "gemini" {
		executable = findGeminiExecutable()
	}
	return externalCommandArgsForExecutable(provider, executable, sessionID, workspace, settings, prompt)
}

func externalCommandArgsForExecutable(provider, executable, sessionID, workspace string, settings UserSettings, prompt string) ([]string, string) {
	antigravity := provider == "gemini" && isAntigravityExecutable(executable)
	generatedSessionID := sessionID
	if generatedSessionID == "" && (provider == "claude" || provider == "grok" || (provider == "gemini" && !antigravity)) {
		generatedSessionID = newUUID()
	}
	model := strings.TrimSpace(settings.Model)
	effort := strings.ToLower(strings.TrimSpace(settings.Effort))
	switch provider {
	case "claude":
		args := []string{"-p", prompt, "--output-format", "stream-json", "--include-partial-messages", "--verbose"}
		args = appendClaudeCompatibilityArgs(args, model)
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
		args := []string{"-p", prompt, "--output-format", "stream-json"}
		if antigravity {
			// agy may otherwise attach its default scratch project even when the
			// process cwd is correct. Register the selected workspace explicitly.
			if strings.TrimSpace(workspace) != "" {
				args = append(args, "--add-dir", workspace)
			}
			// Antigravity owns the conversation id and emits it in the init
			// event. Passing a locally generated id makes `--conversation`
			// point at a non-existent native transcript.
			if sessionID != "" {
				args = append(args, "--conversation", sessionID)
			}
		} else {
			args = append(args, "--skip-trust")
			if sessionID != "" {
				args = append(args, "--resume", sessionID)
			} else {
				args = append(args, "--session-id", generatedSessionID)
			}
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		if antigravity {
			// Antigravity 1.1.25 requires an explicit variant whenever --model is
			// provided. Migrate the former Gemini "auto" value at dispatch too so
			// already-open sessions cannot fail before settings are persisted.
			if model != "" && !isExternalEffort(effort, "low", "medium", "high") {
				effort = "high"
			}
			return append(args, antigravityPermissionArgs(settings.GeminiSandbox, settings.GeminiApprovalPolicy, effort)...), generatedSessionID
		}
		return append(args, geminiPermissionArgs(settings.GeminiSandbox, settings.GeminiApprovalPolicy)...), generatedSessionID
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
		if isExternalEffort(effort, "low", "medium", "high", "xhigh") {
			args = append(args, "--reasoning-effort", effort)
		}
		if !settings.GrokWebSearch {
			args = append(args, "--disable-web-search")
		}
		if !settings.GrokXSearch {
			args = append(args, "--disallowed-tools", "x_keyword_search,x_semantic_search")
		}
		return append(args, grokPermissionArgs(settings)...), generatedSessionID
	case "opencode":
		// OpenCode's non-interactive runner emits one JSON event per line and owns
		// the native session id. `-s` resumes it; a new run creates the id.
		args := []string{"run", prompt, "--format", "json", "--dir", workspace}
		if sessionID != "" {
			args = append(args, "--session", sessionID)
		}
		if model != "" && !strings.Contains(model, "/") && strings.TrimSpace(settings.OpenCodeProvider) != "" {
			model = strings.TrimSpace(settings.OpenCodeProvider) + "/" + model
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		if effort != "" && effort != "auto" {
			args = append(args, "--variant", effort)
		}
		if settings.OpenCodeSandbox == "danger-full-access" && settings.OpenCodeApprovalPolicy == "never" {
			args = append(args, "--auto")
		}
		return args, generatedSessionID
	default:
		return nil, generatedSessionID
	}
}

func appendClaudeCompatibilityArgs(args []string, model string) []string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || model == "sonnet" || model == "opus" || model == "haiku" || model == "fable" || strings.HasPrefix(model, "claude-") {
		return args
	}
	return append(args,
		"--system-prompt", "You are a coding assistant working in the current workspace. Follow the user's request and use the available tools when needed. Do not modify files unless the user explicitly asks.",
		"--disable-slash-commands",
		"--strict-mcp-config",
		"--tools", "Read,Glob,Grep,Bash,Edit,Write",
	)
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
	if settings.ClaudeSandbox == "danger-full-access" && settings.ClaudeApprovalPolicy == "never" {
		return []string{"--dangerously-skip-permissions"}
	}
	if settings.ClaudeSandbox == "read-only" {
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

func geminiPermissionArgs(sandbox, approvalPolicy string) []string {
	if sandbox == "danger-full-access" && approvalPolicy == "never" {
		return []string{"--approval-mode", "yolo"}
	}
	if sandbox == "read-only" {
		return []string{"--approval-mode", "plan"}
	}
	return []string{"--approval-mode", "default"}
}

// antigravityPermissionArgs maps the shared safety controls to Antigravity's
// headless flags. Antigravity defaults to its interactive safety policy; only an
// explicit full-access/never combination enables the documented bypass flag.
func antigravityPermissionArgs(sandbox, approvalPolicy, effort string) []string {
	args := make([]string, 0, 3)
	if sandbox == "danger-full-access" && approvalPolicy == "never" {
		args = append(args, "--dangerously-skip-permissions")
	} else if sandbox == "read-only" {
		// `--sandbox` keeps tool execution inside Antigravity's read-only
		// sandbox. Do not pass a value: current and older agy releases expose it
		// as a boolean switch.
		args = append(args, "--sandbox")
	}
	if isExternalEffort(effort, "low", "medium", "high") {
		args = append(args, "--effort", effort)
	}
	return args
}

func grokPermissionArgs(settings UserSettings) []string {
	profile := "workspace"
	switch settings.GrokSandbox {
	case "read-only":
		profile = "read-only"
	case "danger-full-access":
		profile = "off"
	}
	args := []string{"--sandbox", profile}
	if settings.GrokApprovalPolicy == "never" {
		return append(args, "--yolo")
	}
	// Headless runs cannot display an approval dialog. Default mode safely denies
	// a tool that would require confirmation instead of silently elevating it.
	return append(args, "--permission-mode", "default")
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
func extractExternalUsage(event map[string]any) map[string]any {
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
	// Antigravity wraps usage in step_update; result.usage is the terminal
	// snapshot while step_update.usage may be an intermediate cumulative value.
	if raw == nil {
		for _, key := range []string{"step_update", "stepUpdate"} {
			if step, ok := event[key].(map[string]any); ok {
				raw = step["usage"]
				if raw == nil {
					if nested, ok := step["result"].(map[string]any); ok {
						raw = nested["usage"]
					}
				}
				if raw != nil {
					break
				}
			}
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
	// Gemini stream-json and OpenCode events commonly expose usage as
	// `tokens`, `usageMetadata`, or `stats` instead of the generic `usage` key.
	// Normalize those native envelopes before the provider-specific parser drops
	// the event as a non-text notification.
	if raw == nil {
		for _, key := range []string{"tokens", "usageMetadata", "usage_metadata", "tokenUsage", "token_usage", "stats"} {
			if nested, ok := event[key]; ok {
				raw = nested
				break
			}
		}
	}
	if raw == nil {
		for _, containerKey := range []string{"part", "message", "data", "result", "state"} {
			container, ok := event[containerKey].(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"tokens", "usageMetadata", "usage_metadata", "tokenUsage", "token_usage", "stats"} {
				if nested, exists := container[key]; exists {
					raw = nested
					break
				}
			}
			if raw != nil {
				break
			}
			for _, nestedKey := range []string{"data", "state", "part", "message"} {
				nested, ok := container[nestedKey].(map[string]any)
				if !ok {
					continue
				}
				for _, key := range []string{"tokens", "usage", "usageMetadata", "usage_metadata", "tokenUsage", "token_usage", "stats"} {
					if value, exists := nested[key]; exists {
						raw = value
						break
					}
				}
				if raw != nil {
					break
				}
			}
			if raw != nil {
				break
			}
		}
	}
	return normalizeTokenUsageMap(raw)
}

func normalizeTokenUsageMap(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok || raw == nil {
		return nil
	}
	// OpenCode and some Gemini bridges wrap the actual counters in a `tokens`
	// object. Unwrap those envelopes before reading the provider-specific field
	// aliases below; otherwise a valid step_finish event is silently discarded.
	for _, key := range []string{"tokens", "usageMetadata", "usage_metadata", "tokenUsage", "token_usage", "stats"} {
		if nested, exists := raw[key].(map[string]any); exists && nested != nil {
			raw = nested
			break
		}
	}

	// Detect source shape before normalizing.
	// Headless uses snake_case input_tokens (uncached) + cache_read_input_tokens.
	// ACP / session updates use inputTokens (often FULL) + cachedReadTokens.
	_, hasSnakeInput := raw["input_tokens"]
	_, hasSnakeCache := raw["cache_read_input_tokens"]
	_, hasAntigravityCache := raw["cache_read_tokens"]
	_, hasAntigravityThinking := raw["thinking_tokens"]
	isAntigravityUsage := hasSnakeInput && (hasAntigravityCache || hasAntigravityThinking)
	if !hasSnakeCache {
		hasSnakeCache = hasAntigravityCache
	}
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
	if inputRaw <= 0 {
		inputRaw = anyToFloat(raw["promptTokenCount"])
	}
	if inputRaw <= 0 {
		inputRaw = anyToFloat(raw["input"])
	}

	// Cache field names across Grok headless / Grok session / Codex rollout:
	// cache_read_input_tokens | cachedReadTokens | cached_input_tokens | cachedInputTokens
	cached := anyToFloat(raw["cache_read_input_tokens"])
	if cached <= 0 {
		cached = anyToFloat(raw["cached_input_tokens"]) // Codex token_count
	}
	if cached <= 0 {
		cached = anyToFloat(raw["cache_read_tokens"]) // Antigravity stream-json
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
	if cached <= 0 {
		cached = anyToFloat(raw["cachedContentTokenCount"])
	}
	if cached <= 0 {
		cached = anyToFloat(raw["cached"])
	}
	if cached <= 0 {
		cached = anyToFloat(raw["cacheRead"])
	}
	if cached <= 0 {
		if cache, ok := raw["cache"].(map[string]any); ok {
			cached = anyToFloat(cache["read"])
			if cached <= 0 {
				cached = anyToFloat(cache["cached"])
			}
		}
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
	if output <= 0 {
		output = anyToFloat(raw["candidatesTokenCount"])
	}
	if output <= 0 {
		output = anyToFloat(raw["output"])
	}

	if details, ok := raw["prompt_tokens_details"].(map[string]any); ok && details != nil {
		if cached <= 0 {
			cached = anyToFloat(details["cached_tokens"])
		}
		if cached <= 0 {
			cached = anyToFloat(details["cachedTokens"])
		}
	}
	if details, ok := raw["promptTokensDetails"].(map[string]any); ok && details != nil && cached <= 0 {
		cached = anyToFloat(details["cached_tokens"])
		if cached <= 0 {
			cached = anyToFloat(details["cachedTokens"])
		}
	}

	reasoning := anyToFloat(raw["reasoning_tokens"])
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["reasoningTokens"])
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["thinking_tokens"]) // Antigravity stream-json
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["thinkingTokens"])
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["reasoningOutputTokens"])
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["reasoning_output_tokens"])
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["thoughtsTokenCount"])
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["thoughts"])
	}
	if reasoning <= 0 {
		reasoning = anyToFloat(raw["reasoning"])
	}
	if details, ok := raw["completion_tokens_details"].(map[string]any); ok && details != nil && reasoning <= 0 {
		reasoning = anyToFloat(details["reasoning_tokens"])
		if reasoning <= 0 {
			reasoning = anyToFloat(details["reasoningTokens"])
		}
	}
	if details, ok := raw["completionTokensDetails"].(map[string]any); ok && details != nil && reasoning <= 0 {
		reasoning = anyToFloat(details["reasoning_tokens"])
		if reasoning <= 0 {
			reasoning = anyToFloat(details["reasoningTokens"])
		}
	}

	total := anyToFloat(raw["total_tokens"])
	if total <= 0 {
		total = anyToFloat(raw["totalTokens"])
	}
	if total <= 0 {
		total = anyToFloat(raw["totalTokenCount"])
	}
	if total <= 0 {
		total = anyToFloat(raw["total"])
	}

	// Normalize input to *uncached* tokens for a consistent UI.
	// - Grok headless: input_tokens is uncached; total = uncached + cache + output.
	// - Codex token_count / Grok ACP: input*_tokens is FULL prompt; total ≈ fullInput + output.
	input := inputRaw
	inputIsFull := false
	if isAntigravityUsage && cached > 0 && inputRaw >= cached {
		inputIsFull = true
	} else if cached > 0 && inputRaw >= cached && total > 0 {
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
	if isAntigravityUsage && reasoning > 0 && output > 0 {
		// Antigravity reports thinking_tokens as a subset of output_tokens.
		// Split them for the UI without changing the provider-reported total.
		output -= reasoning
		if output < 0 {
			output = 0
		}
	}

	// Prefer reported total; otherwise compose.
	if total <= 0 {
		if inputIsFull {
			total = inputRaw + output
		} else {
			total = input + cached + output + reasoning
		}
	}
	if total <= 0 && (input > 0 || cached > 0 || output > 0 || reasoning > 0) {
		total = input + cached + output + reasoning
	}
	if total <= 0 && input <= 0 && output <= 0 && cached <= 0 && reasoning <= 0 {
		return nil
	}
	result := map[string]any{
		"inputTokens":           int64(input),
		"cachedInputTokens":     int64(cached),
		"outputTokens":          int64(output),
		"reasoningOutputTokens": int64(reasoning),
		"totalTokens":           int64(total),
	}
	if modelCalls := firstPositiveInt64(raw, "modelCalls", "model_calls"); modelCalls > 0 {
		result["modelCalls"] = modelCalls
	}
	return result
}

func normalizedUsageContext(provider string, usage map[string]any) (int64, string) {
	if usage == nil {
		return 0, ""
	}
	if contextTokens := int64FromAny(usage["contextTokens"]); contextTokens > 0 {
		source := strings.TrimSpace(stringFromAny(usage["contextUsageSource"]))
		if source == "" {
			source = "provider"
		}
		return contextTokens, source
	}
	promptTokens := int64FromAny(usage["inputTokens"]) + int64FromAny(usage["cachedInputTokens"])
	if promptTokens <= 0 {
		return 0, ""
	}
	if estimated, _ := boolFromConfig(usage["estimated"]); estimated {
		return promptTokens, "estimated-text"
	}
	if provider == "grok" {
		if calls := int64FromAny(usage["modelCalls"]); calls > 1 {
			return (promptTokens + calls - 1) / calls, "estimated-turn-average"
		}
	}
	return promptTokens, "provider"
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

// externalEventSessionID accepts both top-level ids and the nested `part` /
// `data` envelopes emitted by OpenCode's JSON runner. The native id is the
// stable key used to join the CLI turn with the NiceCodex sidebar record.
func externalEventSessionID(event map[string]any) string {
	var visit func(any, int) string
	visit = func(value any, depth int) string {
		if depth > 3 || value == nil {
			return ""
		}
		if raw, ok := value.(string); ok && strings.TrimSpace(raw) != "" {
			var nested map[string]any
			if json.Unmarshal([]byte(raw), &nested) == nil {
				return visit(nested, depth+1)
			}
		}
		if record, ok := value.(map[string]any); ok {
			if id := firstMapString(record, "session_id", "sessionId", "sessionID", "conversation_id", "conversationId", "conversationID"); id != "" {
				return id
			}
			for _, key := range []string{"part", "data", "message", "result", "state", "event", "step_update", "stepUpdate", "init", "subagent_info", "subagentInfo"} {
				if id := visit(record[key], depth+1); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return visit(event, 0)
}

func openCodeEventPartID(event map[string]any) string {
	if event == nil {
		return ""
	}
	if id := firstMapString(event, "partId", "part_id"); id != "" {
		return id
	}
	for _, key := range []string{"part", "data", "message", "result"} {
		if nested, ok := event[key].(map[string]any); ok {
			if id := firstMapString(nested, "id", "partId", "part_id"); id != "" {
				return id
			}
		}
	}
	if eventType := strings.ToLower(firstMapString(event, "type", "event")); eventType == "text" || strings.Contains(eventType, "text_delta") {
		return firstMapString(event, "id")
	}
	return ""
}

// bindExternalSession records the native provider id as soon as the CLI emits
// it. This closes the window in which native history sync could import a
// second mirror for the same first turn.
func (s *AppService) bindExternalSession(threadID, provider, backendRef string) {
	threadID = strings.TrimSpace(threadID)
	provider = normalizeExternalRuntime(provider)
	backendRef = strings.TrimSpace(backendRef)
	if threadID == "" || provider == "" || backendRef == "" {
		return
	}
	s.mu.Lock()
	record := s.sessions[threadID]
	if record == nil || !isExternalSession(record) || normalizeExternalRuntime(record.Provider) != provider {
		s.mu.Unlock()
		return
	}
	current := strings.TrimSpace(record.BackendRef)
	// This callback comes from the CLI process owned by threadID and is therefore
	// authoritative while that run is registered. It may repair an older
	// heuristic sync that attached the wrong native id.
	authoritative := s.externalRuns[threadID] != nil
	if current != "" && current != backendRef && !authoritative {
		s.mu.Unlock()
		return
	}
	changed := current != backendRef
	record.BackendRef = backendRef
	record.Provider = provider
	record.ProviderID = externalProviderID(provider)
	for id, other := range s.sessions {
		if other == nil || id == threadID || normalizeExternalRuntime(other.Provider) != provider || !samePath(other.Workspace, record.Workspace) || strings.TrimSpace(other.BackendRef) != backendRef {
			continue
		}
		if other.Native {
			delete(s.sessions, id)
		} else if authoritative {
			// One native conversation can have only one NiceCodex owner. The
			// running callback proves the current owner; leave the stale local row
			// available as a fresh session instead of letting two panes share context.
			other.BackendRef = ""
			other.UpdatedAt = time.Now().Unix()
		} else {
			continue
		}
		changed = true
	}
	if changed {
		record.UpdatedAt = time.Now().Unix()
		s.persistSessionsLocked()
	}
	s.mu.Unlock()
}

// parseExternalEvent returns (chunk, sessionID, final, kind).
// kind is "text" | "thought" | "tool" | "error" | "".
func parseExternalEvent(provider string, event map[string]any) (string, string, bool, string) {
	sessionID := externalEventSessionID(event)
	eventType := strings.ToLower(firstMapString(event, "type", "event"))
	eventType = strings.ReplaceAll(eventType, "-", "_")
	if tools := parseExternalToolEvents(provider, event); len(tools) > 0 {
		var encoded []byte
		if len(tools) == 1 {
			encoded, _ = json.Marshal(tools[0])
		} else {
			encoded, _ = json.Marshal(tools)
		}
		return string(encoded), sessionID, false, "tool"
	}
	if provider == "claude" {
		if eventType == "system" && strings.EqualFold(strings.TrimSpace(firstMapString(event, "subtype")), "compact_boundary") {
			return "compact", sessionID, false, "compact"
		}
		// Claude Code stream-json — Anthropic-native AND proxy backends (GPT / GLM / etc.):
		//   {"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"…"}}}
		//   {"type":"assistant","message":{"content":[…]}}           // partial/full snapshots
		//   {"type":"result","result":"…"}
		//   OpenAI-style: {"choices":[{"delta":{"content":"…"}}]} / {"choices":[{"message":{"content":"…"}}]}
		//   Generic: {"type":"text","text"|"data":"…"} / {"type":"content","content":"…"}
		//   {"type":"message","role":"assistant","content":"…" | […]}
		if text, kind, ok := claudeOpenAIStyleDelta(event); ok {
			return text, sessionID, false, kind
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
		// Gemini CLI and Antigravity CLI both use NDJSON, but Antigravity wraps
		// incremental output in `step_update` and finishes with a nested result.
		// Keep the legacy Gemini shapes below while accepting both envelopes.
		if eventType == "init" {
			// The conversation id is extracted by externalEventSessionID above;
			// init itself carries no assistant text.
			return "", sessionID, false, ""
		}
		// Native Antigravity transcripts (1.1.x) use flat records such as
		// PLANNER_RESPONSE/MODEL_RESPONSE rather than step_update envelopes.
		// A planner record is normally a complete snapshot; retain empty records
		// as non-terminal so a following ERROR_MESSAGE can still attach to the
		// same user turn.
		flatType := strings.ReplaceAll(eventType, ".", "_")
		switch flatType {
		case "planner_response", "model_response", "assistant_response", "model_output", "assistant_output":
			text := antigravityEventText(event)
			if text == "" {
				text = firstMapString(event, "content", "text", "response", "output", "message")
			}
			if text == "" {
				for _, key := range []string{"content", "response", "output", "message"} {
					if value := textFromExternalValue(event[key]); value != "" {
						text = value
						break
					}
				}
			}
			if text == "" {
				return "", sessionID, false, ""
			}
			// DONE closes this planner step, not the user turn. A turn can contain
			// many planner responses separated by tools before its final answer.
			return text, sessionID, false, "text"
		}
		if eventType == "step_update" || eventType == "stepupdate" {
			step, _ := event["step_update"].(map[string]any)
			if step == nil {
				step, _ = event["stepUpdate"].(map[string]any)
			}
			if step == nil {
				step = event
			}
			stepType := strings.ToLower(strings.ReplaceAll(firstMapString(step, "step_type", "stepType", "type", "kind"), "-", "_"))
			if strings.Contains(stepType, "thinking") || strings.Contains(stepType, "reasoning") {
				return firstMapString(step, "text_delta", "text", "delta", "content", "thinking", "reasoning"), sessionID, false, "thought"
			}
			if strings.Contains(stepType, "agent_response") || strings.Contains(stepType, "response") || strings.Contains(stepType, "text") || stepType == "" {
				text := firstMapString(step, "text_delta", "text", "delta", "content", "response")
				if text == "" {
					text = textFromExternalValue(step["text_delta"])
				}
				if text == "" {
					text = textFromExternalValue(step["content"])
				}
				return text, sessionID, false, "text"
			}
			return "", sessionID, false, ""
		}
		if eventType == "error" || flatType == "error_message" {
			severity := strings.ToLower(firstMapString(event, "severity"))
			if severity == "warning" || severity == "info" {
				return "", sessionID, false, ""
			}
			message := firstMapString(event, "message", "error", "text", "content")
			if message == "" {
				if nested, ok := event["error"].(map[string]any); ok {
					message = firstMapString(nested, "message", "text", "detail")
				}
			}
			if message == "" {
				message = antigravityEventText(event)
			}
			if message == "" {
				message = "Gemini/Antigravity stream error"
			}
			return message, sessionID, true, "error"
		}
		if eventType == "message" && strings.EqualFold(firstMapString(event, "role"), "assistant") {
			return textFromExternalValue(event["content"]), sessionID, false, "text"
		}
		if eventType == "result" {
			result, _ := event["result"].(map[string]any)
			status := strings.ToLower(firstMapString(event, "status"))
			if status == "" {
				status = strings.ToLower(firstMapString(result, "status", "state"))
			}
			if status == "error" || status == "failed" {
				message := firstMapString(event, "message")
				if message == "" {
					message = firstMapString(result, "message", "error", "detail")
				}
				if message == "" {
					if errObj, ok := event["error"].(map[string]any); ok {
						message = firstMapString(errObj, "message", "type")
					}
				}
				if message == "" {
					message = "Gemini/Antigravity turn failed"
				}
				return message, sessionID, true, "error"
			}
			text := textFromExternalValue(event["response"])
			if text == "" {
				text = textFromExternalValue(result["response"])
			}
			if text == "" {
				text = firstMapString(event, "text", "content")
			}
			if text == "" {
				text = firstMapString(result, "text", "content", "output")
			}
			return text, sessionID, true, "text"
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
	if provider == "opencode" {
		// OpenCode JSON events vary slightly between releases. Accept the stable
		// text/reasoning/result shapes and generic nested message deltas.
		switch eventType {
		case "text", "text_delta", "message", "message_delta", "assistant", "content", "delta", "response.delta", "output_text.delta":
			text := firstMapString(event, "text", "content", "data", "delta", "output", "message")
			if text == "" {
				text = textFromExternalValue(event["data"])
			}
			if text == "" {
				text = textFromExternalValue(event["message"])
			}
			if text == "" {
				text = textFromExternalValue(event["part"])
			}
			return text, sessionID, false, "text"
		case "reasoning", "thinking", "thought":
			text := firstMapString(event, "text", "content", "data", "reasoning", "thinking")
			if text == "" {
				text = textFromExternalValue(event["part"])
			}
			return text, sessionID, false, "thought"
		case "step_finish", "finish", "result", "completed", "done":
			text := firstMapString(event, "text", "result", "content", "data", "output")
			if text == "" {
				text = textFromExternalValue(event["result"])
			}
			return text, sessionID, true, "text"
		case "error":
			message := firstMapString(event, "message", "error", "data", "text")
			if message == "" {
				message = "OpenCode stream error"
			}
			return message, sessionID, true, "error"
		}
		if strings.Contains(eventType, "delta") {
			return textFromExternalValue(event["delta"]), sessionID, false, "text"
		}
		return "", sessionID, false, ""
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

// parseExternalToolEvents accepts one native event that may contain more than
// one structured activity. Antigravity reports delegated agents as an array;
// the rest of the providers normally emit one tool item per line.
func parseExternalToolEvents(provider string, event map[string]any) []map[string]any {
	if provider == "gemini" {
		items := make([]map[string]any, 0, 2)
		appendSubagents := func(info map[string]any) {
			if info == nil {
				return
			}
			raw := info["subagents"]
			if raw == nil {
				raw = info["subAgents"]
			}
			if raw == nil {
				raw = info["agents"]
			}
			if values, ok := raw.([]any); ok {
				for _, value := range values {
					if agent, ok := value.(map[string]any); ok {
						if item, valid := antigravitySubagentItem(agent, event); valid {
							items = append(items, item)
						}
					}
				}
				return
			}
			if item, valid := antigravitySubagentItem(info, event); valid {
				items = append(items, item)
			}
		}
		step, _ := event["step_update"].(map[string]any)
		if step == nil {
			step, _ = event["stepUpdate"].(map[string]any)
		}
		if info, ok := firstNestedMap(step, "subagent_info", "subagentInfo", "sub_agent_info"); ok {
			appendSubagents(info)
		}
		if info, ok := firstNestedMap(event, "subagent_info", "subagentInfo", "sub_agent_info"); ok {
			appendSubagents(info)
		}
		if len(items) > 0 {
			return items
		}
	}
	if item, ok := parseExternalToolEvent(provider, event); ok {
		return []map[string]any{item}
	}
	return nil
}

// parseExternalToolEvent accepts the JSON event variants emitted by Gemini CLI
// and OpenCode's `run --format json`. Both have changed field nesting between
// releases, so the parser intentionally looks through part/data/state/tool
// envelopes instead of relying on one exact version.
func parseExternalToolEvent(provider string, event map[string]any) (map[string]any, bool) {
	if provider != "gemini" && provider != "opencode" && provider != "grok" {
		return nil, false
	}
	eventType := strings.ToLower(firstMapString(event, "type", "event", "kind"))
	eventType = strings.ReplaceAll(eventType, "-", "_")
	if provider == "gemini" {
		// Antigravity nests tool and child-agent lifecycle data under a
		// `step_update` envelope. Normalize those records into the same shape used
		// by the existing dynamic-tool timeline and sub-agent sidebar.
		step, _ := event["step_update"].(map[string]any)
		if step == nil {
			step, _ = event["stepUpdate"].(map[string]any)
		}
		if step != nil {
			if info, ok := firstNestedMap(step, "subagent_info", "subagentInfo", "sub_agent_info"); ok {
				if item, valid := antigravitySubagentItem(info, event); valid {
					return item, true
				}
			}
			if info, ok := firstNestedMap(step, "tool_info", "toolInfo", "tool_call", "toolCall"); ok {
				event = mergeExternalEventWithToolInfo(event, info)
				eventType = "tool"
			} else {
				stepType := strings.ToLower(strings.ReplaceAll(firstMapString(step, "step_type", "stepType", "type", "kind"), "-", "_"))
				if strings.Contains(stepType, "tool") || strings.Contains(stepType, "function") {
					event = mergeExternalEventWithToolInfo(event, step)
					eventType = "tool"
				}
			}
		}
		if info, ok := firstNestedMap(event, "subagent_info", "subagentInfo", "sub_agent_info"); ok {
			if item, valid := antigravitySubagentItem(info, event); valid {
				return item, true
			}
		}
	}
	if !strings.Contains(eventType, "tool") && !strings.Contains(eventType, "function") {
		// OpenCode puts the lifecycle type on `part` for some releases.
		part, _ := event["part"].(map[string]any)
		partType := strings.ToLower(firstMapString(part, "type", "kind"))
		if !strings.Contains(partType, "tool") && !strings.Contains(partType, "function") {
			return nil, false
		}
	}
	part, _ := event["part"].(map[string]any)
	data, _ := event["data"].(map[string]any)
	tool, _ := event["tool"].(map[string]any)
	if tool == nil {
		tool, _ = event["toolCall"].(map[string]any)
	}
	if tool == nil {
		tool, _ = event["tool_call"].(map[string]any)
	}
	if tool == nil {
		tool = part
	}
	if tool == nil {
		tool = data
	}
	if tool == nil {
		tool = event
	}
	state, _ := tool["state"].(map[string]any)
	if state == nil {
		state, _ = event["state"].(map[string]any)
	}
	id := firstMapString(tool, "callID", "callId", "toolCallId", "tool_call_id", "tool_id", "id")
	if id == "" {
		id = firstMapString(event, "callID", "callId", "toolCallId", "tool_call_id", "tool_id", "itemId", "id")
	}
	name := firstMapString(tool, "tool", "toolName", "tool_name", "name", "title")
	if name == "" {
		name = firstMapString(event, "tool", "toolName", "tool_name", "name", "title")
	}
	if name == "" {
		name = "tool"
	}
	if id == "" {
		return nil, false
	}
	status := strings.ToLower(firstMapString(state, "status", "phase"))
	if status == "" {
		status = strings.ToLower(firstMapString(tool, "status", "phase"))
	}
	if status == "" {
		status = strings.ToLower(firstMapString(event, "status", "phase"))
	}
	if status == "" {
		status = "inProgress"
	}
	switch status {
	case "running", "pending", "started", "in_progress", "inprogress", "executing":
		status = "inProgress"
	case "success", "succeeded", "done", "complete", "completed", "finished":
		status = "completed"
	case "error", "failed", "failure":
		status = "failed"
	case "cancelled", "canceled", "interrupted":
		status = "interrupted"
	default:
		status = "inProgress"
	}
	input := state["input"]
	if input == nil {
		input = state["arguments"]
	}
	if input == nil {
		input = tool["rawInput"]
	}
	if input == nil {
		input = tool["input"]
	}
	if input == nil {
		input = tool["arguments"]
	}
	if input == nil {
		input = tool["args"]
	}
	if input == nil {
		input = tool["parameters"]
	}
	if input == nil {
		input = event["parameters"]
	}
	output := state["output"]
	if output == nil {
		output = state["result"]
	}
	if output == nil {
		output = tool["rawOutput"]
	}
	if output == nil {
		output = tool["output"]
	}
	if output == nil {
		output = tool["result"]
	}
	if output == nil {
		output = event["output"]
	}
	if output == nil {
		output = event["content"]
	}
	item := map[string]any{
		"id":           id,
		"type":         "dynamicToolCall",
		"tool":         name,
		"status":       status,
		"arguments":    input,
		"contentItems": externalToolContentItems(output),
	}
	if status == "completed" {
		item["success"] = true
	} else if status == "failed" {
		item["success"] = false
	}
	return item, true
}

func firstNestedMap(value map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		if nested, ok := value[key].(map[string]any); ok && nested != nil {
			return nested, true
		}
	}
	return nil, false
}

func mergeExternalEventWithToolInfo(event, info map[string]any) map[string]any {
	merged := make(map[string]any, len(event)+len(info)+2)
	for key, value := range event {
		merged[key] = value
	}
	for key, value := range info {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}
	merged["type"] = "tool"
	merged["tool"] = info
	// Antigravity's tool_info does not always carry a call id. Derive one from
	// immutable step metadata (never the output, which changes between ACTIVE and
	// DONE) so lifecycle updates merge into one timeline row.
	if id := firstMapString(info, "call_id", "callId", "callID", "id", "tool_call_id"); id != "" {
		merged["id"] = id
	} else if id := firstMapString(event, "call_id", "callId", "callID", "id", "tool_call_id"); id != "" {
		merged["id"] = id
	} else {
		merged["id"] = antigravityStableToolID(event, info)
	}
	if state := antigravityStepState(event); state != "" {
		merged["status"] = state
	}
	return merged
}

func antigravityStepState(event map[string]any) string {
	step, _ := event["step_update"].(map[string]any)
	if step == nil {
		step, _ = event["stepUpdate"].(map[string]any)
	}
	if step == nil {
		step = event
	}
	return firstMapString(step, "state", "status", "phase", "outcome")
}

func antigravityStableToolID(event, info map[string]any) string {
	conversation := firstMapString(event, "conversation_id", "conversationId", "conversationID", "session_id", "sessionId")
	step, _ := event["step_update"].(map[string]any)
	if step == nil {
		step, _ = event["stepUpdate"].(map[string]any)
	}
	if step != nil {
		if conversation == "" {
			conversation = firstMapString(step, "conversation_id", "conversationId", "conversationID", "session_id", "sessionId")
		}
		index := antigravityEventStepIndex(event)
		if conversation != "" && index != "" {
			return conversation + ":step:" + index
		}
	}
	name := firstMapString(info, "name", "tool_name", "toolName", "tool")
	parameters, _ := json.Marshal(info["parameters"])
	seed := conversation + "\x00" + name + "\x00" + string(parameters)
	sum := sha256.Sum256([]byte(seed))
	return "agy-tool-" + fmt.Sprintf("%x", sum[:8])
}

func firstNonNilExternalValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func antigravitySubagentItem(info, event map[string]any) (map[string]any, bool) {
	id := firstMapString(info, "subagent_id", "subagentId", "child_agent_id", "childAgentId", "agent_id", "agentId", "conversation_id", "conversationId", "id", "session_id", "sessionId")
	if id == "" {
		// Do not fall back to the parent conversation id. A missing child id
		// should be ignored rather than collapsing the parent turn into a fake
		// subagent row.
		id = firstMapString(event, "subagent_id", "subagentId", "child_agent_id", "childAgentId", "agent_id", "agentId")
	}
	if id == "" {
		return nil, false
	}
	name := firstMapString(info, "agent_name", "agentName", "role", "name", "display_name", "displayName", "type_name", "typeName", "type")
	if name == "" {
		name = "subagent"
	}
	status := strings.ToLower(firstMapString(info, "status", "state", "phase", "outcome"))
	if status == "" {
		status = strings.ToLower(antigravityStepState(event))
	}
	switch status {
	case "", "queued", "pending", "waiting":
		status = "inProgress"
	case "active", "running", "started", "in_progress", "inprogress":
		status = "inProgress"
	case "done", "success", "succeeded", "completed", "complete", "finished":
		status = "completed"
	case "failed", "failure", "error":
		status = "failed"
	case "cancelled", "canceled", "interrupted":
		status = "interrupted"
	default:
		status = "inProgress"
	}
	detail := firstMapString(info, "task", "prompt", "description", "detail", "text", "message", "role", "type_name", "typeName")
	if detail == "" {
		detail = textFromExternalValue(info["input"])
	}
	if detail == "" {
		detail = firstMapString(info, "log_uri", "logUri")
	}
	output := info["output"]
	if output == nil {
		output = info["result"]
	}
	item := map[string]any{
		"id":           id,
		"type":         "dynamicToolCall",
		"tool":         "subagent",
		"subagentId":   id,
		"agentName":    name,
		"status":       status,
		"detail":       detail,
		"arguments":    firstNonNilExternalValue(info["input"], info["workspace_uris"], info["workspaceUris"]),
		"contentItems": externalToolContentItems(output),
	}
	if parent := firstMapString(info, "parent_agent_id", "parentAgentId", "parent_conversation_id", "parentConversationId"); parent != "" {
		item["parentAgentId"] = parent
	}
	if status == "completed" {
		item["success"] = true
	} else if status == "failed" {
		item["success"] = false
	}
	return item, true
}

func externalToolContentItems(value any) []any {
	if value == nil {
		return []any{}
	}
	if text, ok := value.(string); ok {
		if strings.TrimSpace(text) == "" {
			return []any{}
		}
		return []any{map[string]any{"type": "text", "text": text}}
	}
	if values, ok := value.([]any); ok {
		return values
	}
	return []any{value}
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
func claudeOpenAIStyleDelta(event map[string]any) (string, string, bool) {
	// {"choices":[{"delta":{"content":"x"}}]}
	// {"choices":[{"delta":{"content":[{"type":"text","text":"x"}]}}]}
	// {"choices":[{"delta":{"reasoning_content":"…"}}]}
	// {"choices":[{"message":{"content":"full"}}]}  → treat as non-delta (caller handles assistant)
	choices, ok := event["choices"].([]any)
	if !ok || len(choices) == 0 {
		// {"delta":{"content":"x"}} flattened
		if delta, ok := event["delta"].(map[string]any); ok {
			if t := firstMapString(delta, "reasoning_content", "reasoning", "thinking"); t != "" {
				return t, "thought", true
			}
			if t := claudeExtractDeltaContent(delta); t != "" {
				return t, "text", true
			}
		}
		return "", "", false
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return "", "", false
	}
	if delta, ok := choice["delta"].(map[string]any); ok {
		if t := firstMapString(delta, "reasoning_content", "reasoning", "thinking"); t != "" {
			return t, "thought", true
		}
		if t := claudeExtractDeltaContent(delta); t != "" {
			return t, "text", true
		}
	}
	return "", "", false
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

func (s *AppService) externalSessionIsRunning(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.externalRuns[threadID] != nil
}

func (s *AppService) deleteNativeExternalSession(record *SessionRecord) error {
	if record == nil {
		return nil
	}
	provider := normalizeExternalRuntime(record.Provider)
	backendRef := strings.TrimSpace(record.BackendRef)
	if backendRef == "" || (provider != "gemini" && provider != "opencode") {
		return nil
	}
	if provider == "gemini" {
		root := resolveGeminiHome()
		path := findGeminiNativeSessionFile(root, backendRef)
		if path == "" {
			return nil
		}
		absoluteRoot, rootErr := filepath.Abs(root)
		absolutePath, pathErr := filepath.Abs(path)
		if rootErr != nil || pathErr != nil {
			return errors.New("resolve Gemini session path")
		}
		relative, relErr := filepath.Rel(absoluteRoot, absolutePath)
		if relErr != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return errors.New("Gemini session path is outside its native history directory")
		}
		if err := os.Remove(absolutePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete Gemini session: %w", err)
		}
		s.invalidateNativeHistoryCache(provider, backendRef)
		s.invalidateNativeSessionSync(provider, record.Workspace)
		return nil
	}

	executable := s.externalExecutable(provider)
	if executable == "" {
		return errors.New("OpenCode CLI executable was not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commandPath, commandArgs, resolveErr := providerCommand(executable, []string{"session", "delete", backendRef})
	if resolveErr != nil {
		return resolveErr
	}
	command := exec.CommandContext(ctx, commandPath, commandArgs...)
	command.Dir = record.Workspace
	output, err := runManagedCombinedOutput(ctx, command)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("delete OpenCode session: %s", truncateRunes(message, 1000))
	}
	s.invalidateNativeHistoryCache(provider, backendRef)
	s.invalidateNativeSessionSync(provider, record.Workspace)
	return nil
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

func stablePendingSessionID(scope, clientID, workspace string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(scope) + "\x00" + workspaceKey(workspace) + "\x00" + strings.TrimSpace(clientID)))
	value := digest[:16]
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
