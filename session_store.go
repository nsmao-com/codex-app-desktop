package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SessionRecord is the NiceCodex-owned conversation index entry.
// Every workbench session uses a NiceCodex UUID as ID.
// Codex app-server thread ids (when allocated) are stored only in BackendRef.
// External CLI sessions also use BackendRef for the provider session id.
type SessionRecord struct {
	ID                string         `json:"id"`
	Workspace         string         `json:"workspace"`
	Provider          string         `json:"provider"`   // "", "claude", "gemini", "grok", or custom codex provider id
	ProviderID        string         `json:"providerId"` // "", "__claude__", "__gemini__", "__grok__", or custom id
	BackendRef        string         `json:"backendRef"` // Codex threadId or CLI session id
	Model             string         `json:"model"`
	Effort            string         `json:"effort"`
	CollaborationMode string         `json:"collaborationMode"`
	// HadPlan is set once the session enters Plan; used to force a Default reset.
	HadPlan bool `json:"hadPlan,omitempty"`
	// CollabResetNonce bumps on each Plan→Default toggle so core emits a fresh
	// collaboration_mode developer message (equality-gated in Codex core).
	CollabResetNonce int `json:"collabResetNonce,omitempty"`
	WorkMode         string `json:"workMode"` // code | cowork
	Name              string         `json:"name"`
	Preview           string         `json:"preview"`
	CreatedAt         int64          `json:"createdAt"`
	UpdatedAt         int64          `json:"updatedAt"`
	Archived          bool           `json:"archived"`
	// Per-chat memory overrides (nil = inherit global config.toml settings).
	UseMemories       *bool          `json:"useMemories,omitempty"`
	GenerateMemories  *bool          `json:"generateMemories,omitempty"`
	Turns             []externalTurn `json:"turns,omitempty"`
}

func sessionsPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "sessions.json")
}

func loadSessions(settingsPath string) map[string]*SessionRecord {
	result := make(map[string]*SessionRecord)
	payload, err := os.ReadFile(sessionsPath(settingsPath))
	if err == nil {
		if err := json.Unmarshal(payload, &result); err != nil {
			result = make(map[string]*SessionRecord)
		}
	}
	// One-time migration from legacy external-threads.json
	legacy := loadExternalThreads(settingsPath)
	changed := false
	for id, record := range legacy {
		if record == nil || id == "" {
			continue
		}
		if existing := result[id]; existing != nil {
			continue
		}
		result[id] = sessionFromExternal(record)
		changed = true
	}
	if changed {
		persistSessionsMap(settingsPath, result)
	}
	return result
}

func sessionFromExternal(record *externalThreadRecord) *SessionRecord {
	providerID := externalProviderID(record.Provider)
	if providerID == "" && record.Provider != "" {
		providerID = record.Provider
	}
	return &SessionRecord{
		ID:         record.ThreadID,
		Workspace:  record.Workspace,
		Provider:   record.Provider,
		ProviderID: providerID,
		BackendRef: record.SessionID,
		Model:      record.Model,
		WorkMode:   "code",
		Name:       record.Name,
		Preview:    record.Preview,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  record.UpdatedAt,
		Archived:   record.Archived,
		Turns:      append([]externalTurn(nil), record.Turns...),
	}
}

func persistSessionsMap(settingsPath string, sessions map[string]*SessionRecord) {
	payload, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return
	}
	path := sessionsPath(settingsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, payload, 0o600)
}

func (s *AppService) persistSessionsLocked() {
	persistSessionsMap(s.settingsPath, s.sessions)
}

func cloneSession(record *SessionRecord) *SessionRecord {
	if record == nil {
		return nil
	}
	clone := *record
	clone.Turns = append([]externalTurn(nil), record.Turns...)
	for index := range clone.Turns {
		clone.Turns[index].Images = append([]string(nil), clone.Turns[index].Images...)
	}
	return &clone
}

func normalizeWorkMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "cowork") {
		return "cowork"
	}
	return "code"
}

func (s *AppService) sessionFor(sessionID, workspace string) *SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil || record.Archived || !samePath(record.Workspace, workspace) {
		return nil
	}
	return cloneSession(record)
}

func (s *AppService) upsertSessionLocked(record *SessionRecord) {
	if record == nil || record.ID == "" {
		return
	}
	if record.WorkMode == "" {
		record.WorkMode = "code"
	}
	if record.Name == "" {
		record.Name = "New task"
	}
	if record.CreatedAt == 0 {
		record.CreatedAt = time.Now().Unix()
	}
	if record.UpdatedAt == 0 {
		record.UpdatedAt = record.CreatedAt
	}
	s.sessions[record.ID] = record
	s.persistSessionsLocked()
}

func (s *AppService) createSessionRecord(workspace, providerKind, providerID, model, effort, collaborationMode, workMode string) *SessionRecord {
	now := time.Now().Unix()
	return &SessionRecord{
		ID:                newUUID(),
		Workspace:         workspace,
		Provider:          providerKind,
		ProviderID:        providerID,
		BackendRef:        "",
		Model:             model,
		Effort:            effort,
		CollaborationMode: collaborationMode,
		WorkMode:          normalizeWorkMode(workMode),
		Name:              "New task",
		CreatedAt:         now,
		UpdatedAt:         now,
		Turns:             []externalTurn{},
	}
}

func (s *AppService) markSessionArchived(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.sessions[sessionID]; record != nil {
		record.Archived = true
		record.UpdatedAt = time.Now().Unix()
		s.persistSessionsLocked()
	}
}

func (s *AppService) markSessionUnarchived(sessionID string) *SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil {
		return nil
	}
	record.Archived = false
	record.UpdatedAt = time.Now().Unix()
	s.persistSessionsLocked()
	return cloneSession(record)
}

func (s *AppService) renameSession(sessionID, name string) *SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil || record.Archived {
		return nil
	}
	record.Name = name
	record.UpdatedAt = time.Now().Unix()
	s.persistSessionsLocked()
	return cloneSession(record)
}

func (s *AppService) deleteSession(sessionID string) *SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil {
		return nil
	}
	delete(s.sessions, sessionID)
	s.persistSessionsLocked()
	return cloneSession(record)
}

func (s *AppService) sessionForAny(sessionID, workspace string) *SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil || !samePath(record.Workspace, workspace) {
		return nil
	}
	return cloneSession(record)
}

func sessionMatchesSearch(record *SessionRecord, search string) bool {
	query := strings.ToLower(strings.TrimSpace(search))
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(record.Name+" "+record.Preview+" "+record.Model), query)
}

func isExternalSession(record *SessionRecord) bool {
	if record == nil {
		return false
	}
	return externalProviderKind(record.ProviderID) != "" || record.Provider == "claude" || record.Provider == "gemini" || record.Provider == "grok"
}

func (s *AppService) sessionThreadMap(record *SessionRecord, includeTurns bool) map[string]any {
	status := "idle"
	if record != nil {
		s.mu.Lock()
		if s.externalRuns[record.ID] != nil {
			status = "active"
		}
		s.mu.Unlock()
	}
	providerID := record.ProviderID
	if providerID == "" && record.Provider != "" {
		providerID = externalProviderID(record.Provider)
		if providerID == "" {
			providerID = record.Provider
		}
	}
	collaborationMode := strings.TrimSpace(record.CollaborationMode)
	if collaborationMode == "" {
		collaborationMode = "default"
	}
	thread := map[string]any{
		"id":                record.ID,
		"name":              record.Name,
		"preview":           record.Preview,
		"cwd":               record.Workspace,
		"createdAt":         record.CreatedAt,
		"updatedAt":         record.UpdatedAt,
		"status":            map[string]any{"type": status},
		"model":             record.Model,
		"modelProvider":     providerID,
		"effort":            record.Effort,
		"collaborationMode": collaborationMode,
		"workMode":          normalizeWorkMode(record.WorkMode),
		"backendRef":        record.BackendRef,
	}
	if record.UseMemories != nil {
		thread["useMemories"] = *record.UseMemories
	}
	if record.GenerateMemories != nil {
		thread["generateMemories"] = *record.GenerateMemories
	}
	if includeTurns && isExternalSession(record) {
		turns := make([]any, 0, len(record.Turns))
		for _, turn := range record.Turns {
			turns = append(turns, externalTurnMap(turn))
		}
		thread["turns"] = turns
	}
	return thread
}

func (s *AppService) sessionResponse(record *SessionRecord) map[string]any {
	providerID := record.ProviderID
	if providerID == "" {
		providerID = externalProviderID(record.Provider)
	}
	return map[string]any{
		"thread":        s.sessionThreadMap(record, true),
		"model":         record.Model,
		"modelProvider": providerID,
		"workMode":      normalizeWorkMode(record.WorkMode),
	}
}
