package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type claudeTurnUsageHit struct {
	SessionID string
	TurnID    string
	Breakdown tokenBreakdown
	At        time.Time
}

// backfillClaudeUsageFromProjects rebuilds the claude runtime bucket from
// ~/.claude/projects/**/*.jsonl assistant message.usage objects.
func (s *AppService) backfillClaudeUsageFromProjects() bool {
	root := claudeProjectsRoot()
	if strings.TrimSpace(root) == "" {
		return false
	}

	hits := scanClaudeProjectTurnUsage(root)
	if len(hits) == 0 {
		return false
	}

	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	usage := s.localUsageLocked()
	changed := resetRuntimeUsage(usage, "claude")
	now := time.Now()
	for _, hit := range hits {
		if applyTurnToUsageDetailed(usage, "claude", hit.SessionID, hit.TurnID, hit.Breakdown, hit.At) {
			changed = true
		}
	}
	if !changed {
		return false
	}
	pruneLocalUsageTurns(usage, now)
	s.scheduleLocalUsagePersistLocked()
	return true
}

func scanClaudeProjectTurnUsage(root string) []claudeTurnUsageHit {
	result := make([]claudeTurnUsageHit, 0, 256)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			return nil
		}
		sessionID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		hits := collectClaudeNativeTurnUsage(path, sessionID)
		if len(hits) > 0 {
			result = append(result, hits...)
		}
		return nil
	})
	return result
}

// ListClaudeSessionTurnUsages restores the most recent request context after
// reopening a native Claude Code transcript.
func (s *AppService) ListClaudeSessionTurnUsages(sessionID string) ([]map[string]any, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("Claude session id is required")
	}
	native, ok := findClaudeNativeSession(sessionID)
	if !ok {
		return []map[string]any{}, nil
	}
	hits := collectClaudeNativeTurnUsage(native.Path, sessionID)
	out := make([]map[string]any, 0, len(hits))
	for i, hit := range hits {
		out = append(out, map[string]any{
			"index":              i + 1,
			"turnId":             hit.TurnID,
			"sessionId":          sessionID,
			"contextTokens":      hit.Breakdown.Input + hit.Breakdown.Cached,
			"contextUsageSource": "native-history",
			"tokenUsage": map[string]any{
				"inputTokens":           hit.Breakdown.Input,
				"cachedInputTokens":     hit.Breakdown.Cached,
				"outputTokens":          hit.Breakdown.Output,
				"reasoningOutputTokens": hit.Breakdown.Reasoning,
				"totalTokens":           hit.Breakdown.Total,
			},
			"at": hit.At.UnixMilli(),
		})
	}
	return out, nil
}
