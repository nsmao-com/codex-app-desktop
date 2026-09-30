package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// LocalStorageIssue describes a failed app-owned file operation without exposing
// file contents. Read failures block writes until the next application launch.
type LocalStorageIssue struct {
	Key       string `json:"key"`
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Message   string `json:"message"`
	CanRetry  bool   `json:"canRetry"`
}

type LocalStorageHealth struct {
	Revision uint64              `json:"revision"`
	Issues   []LocalStorageIssue `json:"issues"`
}

func (s *AppService) LocalStorageStatus() LocalStorageHealth {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.localStorageStatusLocked()
}

func (s *AppService) localStorageStatusLocked() LocalStorageHealth {
	issues := make([]LocalStorageIssue, 0, len(s.storageIssues))
	for _, issue := range s.storageIssues {
		issues = append(issues, issue)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Key < issues[j].Key })
	return LocalStorageHealth{Revision: s.storageRevision, Issues: issues}
}

func (s *AppService) recordLocalStorageResult(key, path, operation string, err error) {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	if s.storageIssues == nil {
		s.storageIssues = make(map[string]LocalStorageIssue)
	}
	previous, exists := s.storageIssues[key]
	// A failed read must not be cleared by a later write of fallback data.
	if exists && previous.Operation == "read" {
		return
	}
	if err == nil {
		if !exists {
			return
		}
		delete(s.storageIssues, key)
	} else {
		issue := LocalStorageIssue{
			Key: key, Path: path, Operation: operation, Message: err.Error(),
			CanRetry: operation == "write" && key != "settings",
		}
		if exists && previous == issue {
			return
		}
		s.storageIssues[key] = issue
	}
	s.storageRevision++
	if s.app != nil {
		s.app.Event.Emit("nice:storage", s.localStorageStatusLocked())
	}
}

func (s *AppService) localStorageReadError(key string) error {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	if issue, exists := s.storageIssues[key]; exists && issue.Operation == "read" {
		return fmt.Errorf("cannot save unread local data; repair %s and restart NiceCodex: %s", issue.Path, issue.Message)
	}
	return nil
}

func (s *AppService) readLocalJSON(key, path string, target any) error {
	payload, err := readProviderFileRecoverable(path)
	if os.IsNotExist(err) {
		err = nil
	} else if err == nil {
		err = json.Unmarshal(payload, target)
	}
	s.recordLocalStorageResult(key, path, "read", err)
	return err
}

func (s *AppService) writeLocalJSON(key, path string, value any) error {
	if err := s.localStorageReadError(key); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err == nil {
		err = writeProviderFileAtomic(path, payload)
	}
	s.recordLocalStorageResult(key, path, "write", err)
	return err
}

func (s *AppService) persistSettingsLocked(settings UserSettings) error {
	return s.writeLocalJSON("settings", s.settingsPath, settings)
}

// Retry only retained in-memory data. Preference saves commit memory after disk
// succeeds, so their caller must retry the original form to preserve its edits.
func (s *AppService) RetryLocalStorageWrites() LocalStorageHealth {
	for _, issue := range s.LocalStorageStatus().Issues {
		if !issue.CanRetry {
			continue
		}
		if issue.Key == "usage" {
			s.flushLocalUsage()
			continue
		}
		s.mu.Lock()
		switch issue.Key {
		case "sessions":
			_ = s.persistSessionsLocked()
		case "claude":
			_ = s.persistClaudeSessionsLocked()
		case "grok":
			_ = s.persistGrokAPISessionsLocked()
		}
		s.mu.Unlock()
	}
	return s.LocalStorageStatus()
}
