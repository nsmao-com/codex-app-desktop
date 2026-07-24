package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// backfillGrokUsageFromSessions rebuilds the grok runtime bucket from local
// ~/.grok session updates.jsonl turn_completed usage objects.
//
// Session shape (verified):
//
//	{"method":"_x.ai/session/update","params":{"sessionId":"...","update":{
//	  "sessionUpdate":"turn_completed","prompt_id":"...","usage":{
//	    "inputTokens":..,"cachedReadTokens":..,"outputTokens":..,"reasoningTokens":..,"totalTokens":..
//	  }}}}
func (s *AppService) backfillGrokUsageFromSessions() bool {
	root := filepath.Join(resolveGrokHome(), "sessions")
	if strings.TrimSpace(root) == "" {
		return false
	}

	s.mu.Lock()
	usage := loadLocalUsage(s.settingsPath)
	// Only backfill when the grok bucket has no breakdown yet (empty or total-only legacy).
	bucket := usage.ensureRuntime("grok")
	hasBreakdown := bucket.LifetimeInput > 0 || bucket.LifetimeCached > 0 || bucket.LifetimeOutput > 0 || bucket.LifetimeReasoning > 0
	if hasBreakdown && bucket.LifetimeTokens > 0 {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()

	hits := scanGrokSessionTurnUsage(root)
	if len(hits) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	usage = loadLocalUsage(s.settingsPath)
	// Re-check under lock.
	bucket = usage.ensureRuntime("grok")
	hasBreakdown = bucket.LifetimeInput > 0 || bucket.LifetimeCached > 0 || bucket.LifetimeOutput > 0 || bucket.LifetimeReasoning > 0
	if hasBreakdown && bucket.LifetimeTokens > 0 {
		return false
	}

	changed := false
	now := time.Now()
	for _, hit := range hits {
		if applyTurnToUsageDetailed(usage, "grok", hit.SessionID, hit.TurnID, hit.Breakdown, hit.At) {
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

type grokTurnUsageHit struct {
	SessionID  string
	TurnID     string
	Breakdown  tokenBreakdown
	At         time.Time
}

func scanGrokSessionTurnUsage(root string) []grokTurnUsageHit {
	result := make([]grokTurnUsageHit, 0, 128)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if !strings.EqualFold(entry.Name(), "updates.jsonl") {
			return nil
		}
		hits := parseGrokUpdatesUsage(path)
		if len(hits) > 0 {
			result = append(result, hits...)
		}
		return nil
	})
	return result
}

// ListGrokSessionTurnUsages returns per-turn token breakdown for one Grok session
// (from updates.jsonl turn_completed). Used to populate the chat message footer.
func (s *AppService) ListGrokSessionTurnUsages(sessionID string) ([]map[string]any, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("Grok session id is required")
	}
	session, err := findGrokNativeSession(sessionID)
	if err != nil {
		return []map[string]any{}, nil
	}
	path := filepath.Join(session.Dir, "updates.jsonl")
	hits := parseGrokUpdatesUsage(path)
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

func parseGrokUpdatesUsage(path string) []grokTurnUsageHit {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	result := make([]grokTurnUsageHit, 0, 16)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		// Fast reject.
		if !strings.Contains(string(line), "turn_completed") && !strings.Contains(string(line), "usage") {
			continue
		}
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		params, _ := event["params"].(map[string]any)
		if params == nil {
			continue
		}
		update, _ := params["update"].(map[string]any)
		if update == nil {
			continue
		}
		kind := strings.ToLower(firstMapString(update, "sessionUpdate", "session_update", "type"))
		if kind != "" && kind != "turn_completed" && kind != "turn-completed" && kind != "agent_turn_complete" {
			// Still accept if usage is present on other completion-like updates.
			if update["usage"] == nil {
				continue
			}
			if !strings.Contains(kind, "complete") && !strings.Contains(kind, "end") {
				continue
			}
		}
		usageRaw := update["usage"]
		if usageRaw == nil {
			continue
		}
		normalized := normalizeTokenUsageMap(usageRaw)
		if normalized == nil {
			continue
		}
		b := breakdownFromUsageMap(normalized)
		if !b.valid() {
			continue
		}
		sessionID := firstMapString(params, "sessionId", "session_id")
		if sessionID == "" {
			// Fallback: parent folder name is often the session uuid.
			sessionID = filepath.Base(filepath.Dir(path))
		}
		turnID := firstMapString(update, "prompt_id", "promptId", "turnId", "turn_id")
		if turnID == "" {
			if meta, ok := params["_meta"].(map[string]any); ok {
				turnID = firstMapString(meta, "eventId", "event_id")
			}
		}
		if turnID == "" {
			turnID = "usage-" + localDayKey(time.Now()) + "-" + strings.ReplaceAll(sessionID, ":", "")[:8]
		}
		at := time.Now()
		if ts := anyToFloat(event["timestamp"]); ts > 1_000_000_000 {
			// seconds or millis
			if ts > 1_000_000_000_000 {
				at = time.UnixMilli(int64(ts))
			} else {
				at = time.Unix(int64(ts), 0)
			}
		} else if meta, ok := params["_meta"].(map[string]any); ok {
			if ms := anyToFloat(meta["agentTimestampMs"]); ms > 0 {
				at = time.UnixMilli(int64(ms))
			}
		}
		result = append(result, grokTurnUsageHit{
			SessionID: sessionID,
			TurnID:    turnID,
			Breakdown: b,
			At:        at,
		})
	}
	return result
}
