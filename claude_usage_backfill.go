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

	s.mu.Lock()
	usage := loadLocalUsage(s.settingsPath)
	bucket := usage.ensureRuntime("claude")
	hasBreakdown := bucket.LifetimeInput > 0 || bucket.LifetimeCached > 0 || bucket.LifetimeOutput > 0 || bucket.LifetimeReasoning > 0
	if hasBreakdown && bucket.LifetimeTokens > 0 {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()

	hits := scanClaudeProjectTurnUsage(root)
	if len(hits) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	usage = loadLocalUsage(s.settingsPath)
	bucket = usage.ensureRuntime("claude")
	hasBreakdown = bucket.LifetimeInput > 0 || bucket.LifetimeCached > 0 || bucket.LifetimeOutput > 0 || bucket.LifetimeReasoning > 0
	if hasBreakdown && bucket.LifetimeTokens > 0 {
		return false
	}

	changed := false
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
	persistLocalUsage(s.settingsPath, usage)
	return true
}

func scanClaudeProjectTurnUsage(root string) []claudeTurnUsageHit {
	result := make([]claudeTurnUsageHit, 0, 256)
	const maxFiles = 200
	files := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			return nil
		}
		if files >= maxFiles {
			return filepath.SkipAll
		}
		files++
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
			"index":     i + 1,
			"turnId":    hit.TurnID,
			"sessionId": sessionID,
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
