package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type MemoryFileInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Preview string `json:"preview"`
}

type MemoriesOverview struct {
	Root            string           `json:"root"`
	Enabled         bool             `json:"enabled"`
	Generate        bool             `json:"generate"`
	Use             bool             `json:"use"`
	Files           []MemoryFileInfo `json:"files"`
	SummaryPreview  string           `json:"summaryPreview"`
}

type SessionMemoriesRequest struct {
	SessionID        string `json:"sessionId"`
	UseMemories      *bool  `json:"useMemories"`
	GenerateMemories *bool  `json:"generateMemories"`
}

func (s *AppService) ListLocalMemories() (MemoriesOverview, error) {
	flags := readCodexFeatureFlags()
	root := filepath.Join(resolveCodexHome(), "memories")
	overview := MemoriesOverview{
		Root:     root,
		Enabled:  flags.MemoriesEnabled,
		Generate: flags.MemoriesGenerate,
		Use:      flags.MemoriesUse,
		Files:    []MemoryFileInfo{},
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return overview, nil
		}
		return overview, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".md") {
			continue
		}
		info, infoErr := entry.Info()
		path := filepath.Join(root, name)
		item := MemoryFileInfo{Name: name, Path: path}
		if infoErr == nil {
			item.Size = info.Size()
		}
		if payload, readErr := os.ReadFile(path); readErr == nil {
			preview := strings.TrimSpace(string(payload))
			preview = strings.ReplaceAll(preview, "\r\n", "\n")
			if len(preview) > 280 {
				preview = preview[:280] + "…"
			}
			item.Preview = preview
			if strings.EqualFold(name, "memory_summary.md") || strings.EqualFold(name, "MEMORY.md") {
				overview.SummaryPreview = preview
			}
		}
		overview.Files = append(overview.Files, item)
		if len(overview.Files) >= 24 {
			break
		}
	}
	return overview, nil
}

func (s *AppService) OpenMemoriesFolder() error {
	root := filepath.Join(resolveCodexHome(), "memories")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return openPathInOS(root)
}

func (s *AppService) UpdateSessionMemories(request SessionMemoriesRequest) error {
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	if strings.HasPrefix(sessionID, "pending-thread-") {
		return nil
	}
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil || record.Archived || !samePath(record.Workspace, workspace) {
		return errors.New("session not found in the current workspace")
	}
	if request.UseMemories != nil {
		value := *request.UseMemories
		record.UseMemories = &value
	}
	if request.GenerateMemories != nil {
		value := *request.GenerateMemories
		record.GenerateMemories = &value
	}
	record.UpdatedAt = time.Now().Unix()
	s.persistSessionsLocked()
	return nil
}

func sessionMemoryGuidance(record *SessionRecord) string {
	if record == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if record.UseMemories != nil && !*record.UseMemories {
		parts = append(parts, "For this chat only: do not use or rely on previously stored user memories.")
	}
	if record.GenerateMemories != nil && !*record.GenerateMemories {
		parts = append(parts, "For this chat only: do not use this conversation as an input for generating future memories.")
	}
	return strings.Join(parts, " ")
}
