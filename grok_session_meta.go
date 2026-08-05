package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// grokMetaFile holds NiceCodex-local Grok conveniences that the CLI does not
// expose as first-class APIs (archive list, optional name overrides).
type grokMetaFile struct {
	Version  int                           `json:"version"`
	Archived map[string]GrokSessionSummary `json:"archived"`
	// Names maps session id → user-chosen title when summary.json write is unavailable.
	Names map[string]string `json:"names,omitempty"`
}

func grokMetaPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "grok-meta.json")
}

func emptyGrokMeta() *grokMetaFile {
	return &grokMetaFile{
		Version:  1,
		Archived: make(map[string]GrokSessionSummary),
		Names:    make(map[string]string),
	}
}

func readGrokJSONFile(path string, target any) error {
	payload, err := os.ReadFile(path)
	if err == nil {
		if decodeErr := json.Unmarshal(payload, target); decodeErr == nil {
			return nil
		} else {
			err = decodeErr
		}
	}
	backup, backupErr := os.ReadFile(path + ".bak")
	if backupErr == nil {
		if decodeErr := json.Unmarshal(backup, target); decodeErr == nil {
			return nil
		}
	}
	return err
}

func writeGrokJSONFile(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".grok-json-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}

	backupPath := path + ".bak"
	_ = os.Remove(backupPath)
	hadExisting := false
	if _, statErr := os.Stat(path); statErr == nil {
		if err := os.Rename(path, backupPath); err != nil {
			return err
		}
		hadExisting = true
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.Rename(tempPath, path); err != nil {
		if hadExisting {
			_ = os.Rename(backupPath, path)
		}
		return err
	}
	if hadExisting {
		_ = os.Remove(backupPath)
	}
	return nil
}

func loadGrokMeta(settingsPath string) *grokMetaFile {
	result := emptyGrokMeta()
	if err := readGrokJSONFile(grokMetaPath(settingsPath), result); err != nil {
		return emptyGrokMeta()
	}
	if result.Archived == nil {
		result.Archived = make(map[string]GrokSessionSummary)
	}
	if result.Names == nil {
		result.Names = make(map[string]string)
	}
	if result.Version <= 0 {
		result.Version = 1
	}
	return result
}

func persistGrokMeta(settingsPath string, meta *grokMetaFile) error {
	if meta == nil {
		return nil
	}
	payload, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeGrokJSONFile(grokMetaPath(settingsPath), payload)
}

func (s *AppService) withGrokMeta(mutator func(meta *grokMetaFile) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := loadGrokMeta(s.settingsPath)
	if err := mutator(meta); err != nil {
		return err
	}
	return persistGrokMeta(s.settingsPath, meta)
}

func (s *AppService) isGrokSessionArchived(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := loadGrokMeta(s.settingsPath)
	_, ok := meta.Archived[sessionID]
	return ok
}

func (s *AppService) applyGrokLocalName(summary GrokSessionSummary) GrokSessionSummary {
	if strings.TrimSpace(summary.ID) == "" {
		return summary
	}
	s.mu.Lock()
	meta := loadGrokMeta(s.settingsPath)
	name := strings.TrimSpace(meta.Names[summary.ID])
	s.mu.Unlock()
	if name != "" {
		summary.Name = name
	}
	return summary
}

// RenameGrokSession updates the session title (native summary.json + local override).
func (s *AppService) RenameGrokSession(backend, sessionID, name string) (GrokSessionSummary, error) {
	backend = normalizeGrokBackend(backend)
	sessionID = strings.TrimSpace(sessionID)
	name = strings.TrimSpace(name)
	if sessionID == "" {
		return GrokSessionSummary{}, errors.New("Grok session id is required")
	}
	if name == "" {
		return GrokSessionSummary{}, errors.New("session name is required")
	}
	if len([]rune(name)) > 120 {
		name = string([]rune(name)[:120])
	}

	if backend == grokBackendAPI {
		return s.renameGrokAPISession(sessionID, name)
	}

	// Local-only draft sessions (pending-*) live only in the UI until first send.
	if strings.HasPrefix(sessionID, "pending-grok-") {
		summary := GrokSessionSummary{
			ID: sessionID, Backend: backend, Name: name, Preview: name,
			UpdatedAt: time.Now().Unix(),
		}
		if err := s.withGrokMeta(func(meta *grokMetaFile) error {
			meta.Names[sessionID] = name
			return nil
		}); err != nil {
			return GrokSessionSummary{}, err
		}
		return summary, nil
	}

	session, err := findGrokNativeSession(sessionID)
	if err != nil {
		// Still keep a local override so the sidebar can show the chosen name.
		if metaErr := s.withGrokMeta(func(meta *grokMetaFile) error {
			meta.Names[sessionID] = name
			return nil
		}); metaErr != nil {
			return GrokSessionSummary{}, metaErr
		}
		return GrokSessionSummary{
			ID: sessionID, Backend: backend, Name: name, UpdatedAt: time.Now().Unix(),
		}, nil
	}

	if err := writeGrokSessionTitle(session.Dir, name); err != nil {
		return GrokSessionSummary{}, err
	}
	if err := s.withGrokMeta(func(meta *grokMetaFile) error {
		meta.Names[sessionID] = name
		if archived, ok := meta.Archived[sessionID]; ok {
			archived.Name = name
			archived.UpdatedAt = time.Now().Unix()
			meta.Archived[sessionID] = archived
		}
		return nil
	}); err != nil {
		return GrokSessionSummary{}, err
	}
	summary := session.Summary
	summary.Name = name
	summary.UpdatedAt = time.Now().Unix()
	return summary, nil
}

func writeGrokSessionTitle(sessionDir, name string) error {
	path := filepath.Join(sessionDir, "summary.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	prevTitle := firstMapString(raw, "generated_title")
	raw["generated_title"] = name
	// Keep session_summary readable when it was empty or just mirrored the old title.
	if prev := strings.TrimSpace(firstMapString(raw, "session_summary")); prev == "" || prev == prevTitle {
		raw["session_summary"] = name
	}
	raw["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return writeGrokJSONFile(path, out)
}

// ArchiveGrokSession hides a session from the main list (local archive index).
func (s *AppService) ArchiveGrokSession(backend, sessionID string) error {
	backend = normalizeGrokBackend(backend)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("Grok session id is required")
	}
	if s.isGrokSessionRunning(backend, sessionID) {
		return errors.New("stop the running Grok turn before archiving its session")
	}

	var summary GrokSessionSummary
	if backend == grokBackendAPI {
		detail, err := s.readGrokAPISession(sessionID)
		if err != nil {
			return err
		}
		summary = detail.Summary
	} else if strings.HasPrefix(sessionID, "pending-grok-") {
		summary = GrokSessionSummary{
			ID: sessionID, Backend: backend, Name: "New Grok task",
			UpdatedAt: time.Now().Unix(), CreatedAt: time.Now().Unix(),
		}
	} else {
		session, err := findGrokNativeSession(sessionID)
		if err != nil {
			// Allow archiving by id even if the native scan misses it.
			summary = GrokSessionSummary{ID: sessionID, Backend: backend, Name: sessionID, UpdatedAt: time.Now().Unix()}
		} else {
			summary = s.applyGrokLocalName(session.Summary)
		}
	}

	err := s.withGrokMeta(func(meta *grokMetaFile) error {
		if name := strings.TrimSpace(meta.Names[sessionID]); name != "" {
			summary.Name = name
		}
		summary.UpdatedAt = time.Now().Unix()
		meta.Archived[sessionID] = summary
		return nil
	})
	if err == nil {
		s.dropGrokHistoryCache(sessionID)
	}
	return err
}

// UnarchiveGrokSession restores a session to the main list.
func (s *AppService) UnarchiveGrokSession(backend, sessionID string) (GrokSessionSummary, error) {
	backend = normalizeGrokBackend(backend)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return GrokSessionSummary{}, errors.New("Grok session id is required")
	}
	var restored GrokSessionSummary
	err := s.withGrokMeta(func(meta *grokMetaFile) error {
		item, ok := meta.Archived[sessionID]
		if !ok {
			return errors.New("archived Grok session was not found")
		}
		restored = item
		delete(meta.Archived, sessionID)
		return nil
	})
	if err != nil {
		return GrokSessionSummary{}, err
	}
	// Prefer live summary when the session still exists on disk.
	if backend != grokBackendAPI {
		if session, findErr := findGrokNativeSession(sessionID); findErr == nil {
			restored = s.applyGrokLocalName(session.Summary)
		}
	} else if detail, readErr := s.readGrokAPISession(sessionID); readErr == nil {
		restored = detail.Summary
	}
	return restored, nil
}

// ListArchivedGrokSessions returns NiceCodex-local archived Grok sessions.
func (s *AppService) ListArchivedGrokSessions(backend, search string) ([]GrokSessionSummary, error) {
	backend = normalizeGrokBackend(backend)
	query := strings.ToLower(strings.TrimSpace(search))
	s.mu.Lock()
	meta := loadGrokMeta(s.settingsPath)
	s.mu.Unlock()
	result := make([]GrokSessionSummary, 0, len(meta.Archived))
	for id, item := range meta.Archived {
		if item.ID == "" {
			item.ID = id
		}
		if item.Backend == "" {
			item.Backend = backend
		}
		// When listing for a specific backend, skip the other kind.
		if item.Backend != "" && item.Backend != backend && backend != "" {
			// Keep both if backend filter is loose — only filter when item has backend set differently.
			if (backend == grokBackendAPI && item.Backend != grokBackendAPI) ||
				(backend == grokBackendBuild && item.Backend == grokBackendAPI) {
				continue
			}
		}
		if name := strings.TrimSpace(meta.Names[id]); name != "" {
			item.Name = name
		}
		haystack := strings.ToLower(item.Name + "\n" + item.Preview + "\n" + item.Workspace)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].UpdatedAt > result[j].UpdatedAt
	})
	return result, nil
}

func (s *AppService) removeGrokArchiveEntry(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	_ = s.withGrokMeta(func(meta *grokMetaFile) error {
		delete(meta.Archived, sessionID)
		delete(meta.Names, sessionID)
		return nil
	})
}

func (s *AppService) renameGrokAPISession(sessionID, name string) (GrokSessionSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.grokAPISessions[sessionID]
	if session == nil {
		return GrokSessionSummary{}, errors.New("Grok API session was not found")
	}
	previousName := session.Name
	previousUpdatedAt := session.UpdatedAt
	session.Name = name
	session.UpdatedAt = time.Now().Unix()
	if err := s.persistGrokAPISessionsLocked(); err != nil {
		session.Name = previousName
		session.UpdatedAt = previousUpdatedAt
		return GrokSessionSummary{}, err
	}
	return GrokSessionSummary{
		ID: session.ID, Backend: grokBackendAPI, Workspace: session.Workspace,
		Name: session.Name, Preview: session.Preview, Model: session.Model, Effort: session.Effort,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}, nil
}
