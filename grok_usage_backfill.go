package main

import (
	"bufio"
	"bytes"
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

	s.usageMu.Lock()
	usage := s.localUsageLocked()
	// Only backfill when the grok bucket has no breakdown yet (empty or total-only legacy).
	bucket := usage.ensureRuntime("grok")
	hasBreakdown := bucket.LifetimeInput > 0 || bucket.LifetimeCached > 0 || bucket.LifetimeOutput > 0 || bucket.LifetimeReasoning > 0
	if hasBreakdown && bucket.LifetimeTokens > 0 {
		s.usageMu.Unlock()
		return false
	}
	s.usageMu.Unlock()

	hits := scanGrokSessionTurnUsage(root)
	if len(hits) == 0 {
		return false
	}

	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	usage = s.localUsageLocked()
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
	s.scheduleLocalUsagePersistLocked()
	return true
}

type grokTurnUsageHit struct {
	SessionID string
	TurnID    string
	Breakdown tokenBreakdown
	At        time.Time
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
	sessionDir, totalTurns, cacheCurrent := s.cachedGrokUsageSource(sessionID)
	if !cacheCurrent {
		// The caller hydrates usage immediately after reading history. If that read
		// overlapped a transcript write, defer backfill instead of attaching correct
		// token values to stale turn indexes.
		return []map[string]any{}, nil
	}
	path := filepath.Join(sessionDir, "updates.jsonl")
	// updates.jsonl can reach hundreds of MB because it also contains tool and
	// stream traffic. The timeline only needs recent visible turns, so scan from
	// EOF and stop once several history pages are covered.
	usageLimit := conversationHistoryPageTurns * 4
	if totalTurns > 0 && totalTurns < usageLimit {
		usageLimit = totalTurns
	}
	hits := parseGrokUpdatesUsageTail(path, usageLimit)
	indexOffset := totalTurns - len(hits)
	if indexOffset < 0 {
		indexOffset = 0
	}
	out := make([]map[string]any, 0, len(hits))
	for i, hit := range hits {
		out = append(out, map[string]any{
			"index":     indexOffset + i + 1,
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
		if hit, ok := parseGrokUsageLine(scanner.Bytes(), path); ok {
			result = append(result, hit)
		}
	}
	return result
}

func parseGrokUpdatesUsageTail(path string, limit int) []grokTurnUsageHit {
	if limit <= 0 {
		return nil
	}
	newestFirst := make([]grokTurnUsageHit, 0, limit)
	_ = visitJSONLLinesReverse(path, 64*1024*1024, func(line []byte) bool {
		if hit, ok := parseGrokUsageLine(line, path); ok {
			newestFirst = append(newestFirst, hit)
		}
		return len(newestFirst) >= limit
	})
	for left, right := 0, len(newestFirst)-1; left < right; left, right = left+1, right-1 {
		newestFirst[left], newestFirst[right] = newestFirst[right], newestFirst[left]
	}
	return newestFirst
}

func visitJSONLLinesReverse(path string, maxBytes int64, visit func(line []byte) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}

	const chunkSize int64 = 256 * 1024
	position := info.Size()
	var scanned int64
	var carry []byte
	for position > 0 {
		readSize := chunkSize
		if position < readSize {
			readSize = position
		}
		position -= readSize
		scanned += readSize
		chunk := make([]byte, int(readSize))
		if _, err := file.ReadAt(chunk, position); err != nil {
			return err
		}
		data := make([]byte, 0, len(chunk)+len(carry))
		data = append(data, chunk...)
		data = append(data, carry...)
		parts := bytes.Split(data, []byte{'\n'})
		carry = append(carry[:0], parts[0]...)
		for index := len(parts) - 1; index >= 1; index-- {
			line := bytes.TrimSuffix(parts[index], []byte{'\r'})
			if len(line) > 0 && visit(line) {
				return nil
			}
		}
		if maxBytes > 0 && scanned >= maxBytes && position > 0 {
			return nil
		}
	}
	if len(carry) > 0 {
		visit(bytes.TrimSuffix(carry, []byte{'\r'}))
	}
	return nil
}

func parseGrokUsageLine(line []byte, path string) (grokTurnUsageHit, bool) {
	if !bytes.Contains(line, []byte("turn_completed")) && !bytes.Contains(line, []byte("usage")) {
		return grokTurnUsageHit{}, false
	}
	var event map[string]any
	if json.Unmarshal(line, &event) != nil {
		return grokTurnUsageHit{}, false
	}
	params, _ := event["params"].(map[string]any)
	if params == nil {
		return grokTurnUsageHit{}, false
	}
	update, _ := params["update"].(map[string]any)
	if update == nil {
		return grokTurnUsageHit{}, false
	}
	kind := strings.ToLower(firstMapString(update, "sessionUpdate", "session_update", "type"))
	if kind != "" && kind != "turn_completed" && kind != "turn-completed" && kind != "agent_turn_complete" {
		if update["usage"] == nil || (!strings.Contains(kind, "complete") && !strings.Contains(kind, "end")) {
			return grokTurnUsageHit{}, false
		}
	}
	normalized := normalizeTokenUsageMap(update["usage"])
	if normalized == nil {
		return grokTurnUsageHit{}, false
	}
	breakdown := breakdownFromUsageMap(normalized)
	if !breakdown.valid() {
		return grokTurnUsageHit{}, false
	}
	sessionID := firstMapString(params, "sessionId", "session_id")
	if sessionID == "" {
		sessionID = filepath.Base(filepath.Dir(path))
	}
	turnID := firstMapString(update, "prompt_id", "promptId", "turnId", "turn_id")
	if turnID == "" {
		if meta, ok := params["_meta"].(map[string]any); ok {
			turnID = firstMapString(meta, "eventId", "event_id")
		}
	}
	if turnID == "" {
		shortID := strings.ReplaceAll(sessionID, ":", "")
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		turnID = "usage-" + localDayKey(time.Now()) + "-" + shortID
	}
	at := time.Now()
	if ts := anyToFloat(event["timestamp"]); ts > 1_000_000_000 {
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
	return grokTurnUsageHit{
		SessionID: sessionID,
		TurnID:    turnID,
		Breakdown: breakdown,
		At:        at,
	}, true
}
