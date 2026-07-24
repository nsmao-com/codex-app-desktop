package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type rolloutTokenHit struct {
	SessionID string
	TurnID    string
	Breakdown tokenBreakdown
	At        time.Time
}

// backfillLocalUsageFromRollouts rebuilds the *codex* runtime bucket from ~/.codex
// session rollouts (token_count.last_token_usage). Runs when the codex bucket is
// empty OR has totals but no input/output/cache breakdown (legacy total-only rows).
func (s *AppService) backfillLocalUsageFromRollouts() bool {
	s.mu.Lock()
	usage := loadLocalUsage(s.settingsPath)
	bucket := usage.ensureRuntime("codex")
	needsDetail := bucket.LifetimeInput <= 0 && bucket.LifetimeCached <= 0 &&
		bucket.LifetimeOutput <= 0 && bucket.LifetimeReasoning <= 0
	emptyCodex := bucket.LifetimeTokens <= 0 && len(bucket.Days) == 0
	s.mu.Unlock()
	if !emptyCodex && !needsDetail {
		return false
	}

	home := resolveCodexHome()
	if strings.TrimSpace(home) == "" {
		return false
	}
	hits := scanCodexRolloutTokenUsage(home)
	if len(hits) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	usage = loadLocalUsage(s.settingsPath)
	bucket = usage.ensureRuntime("codex")
	needsDetail = bucket.LifetimeInput <= 0 && bucket.LifetimeCached <= 0 &&
		bucket.LifetimeOutput <= 0 && bucket.LifetimeReasoning <= 0
	emptyCodex = bucket.LifetimeTokens <= 0 && len(bucket.Days) == 0
	if !emptyCodex && !needsDetail {
		return false
	}

	// Drop legacy total-only codex rows so we do not double-count when re-importing.
	if needsDetail && !emptyCodex {
		clearRuntimeUsage(usage, "codex")
	}

	changed := false
	now := time.Now()
	for _, hit := range hits {
		b := hit.Breakdown
		if !b.valid() {
			continue
		}
		if applyTurnToUsageDetailed(usage, "codex", hit.SessionID, hit.TurnID, b, hit.At) {
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

// clearRuntimeUsage removes one runtime's bucket + turns (used before detailed re-import).
func clearRuntimeUsage(usage *localUsageFile, runtime string) {
	if usage == nil {
		return
	}
	runtime = normalizeUsageRuntime(runtime)
	if usage.ByRuntime != nil {
		usage.ByRuntime[runtime] = emptyRuntimeBucket()
	}
	for key, turn := range usage.Turns {
		if normalizeUsageRuntime(turn.Runtime) == runtime || (turn.Runtime == "" && runtime == "codex") {
			delete(usage.Turns, key)
		}
	}
}

func scanCodexRolloutTokenUsage(codexHome string) []rolloutTokenHit {
	roots := []string{
		filepath.Join(codexHome, "sessions"),
		filepath.Join(codexHome, "archived_sessions"),
	}
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	files := make([]fileInfo, 0, 256)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() {
				return nil
			}
			name := strings.ToLower(entry.Name())
			if !strings.HasSuffix(name, ".jsonl") {
				return nil
			}
			info, statErr := entry.Info()
			modTime := time.Time{}
			if statErr == nil && info != nil {
				modTime = info.ModTime()
			}
			files = append(files, fileInfo{path: path, modTime: modTime})
			return nil
		})
	}
	// Newest first, keep a bounded set so the usage popover stays responsive.
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})
	const maxFiles = 120
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}

	hits := make([]rolloutTokenHit, 0, 256)
	for _, item := range files {
		hits = append(hits, parseRolloutTokenHits(item.path)...)
	}
	return hits
}

func parseRolloutTokenHits(path string) []rolloutTokenHit {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	sessionID := sessionIDFromRolloutPath(path)
	var (
		pending   tokenBreakdown
		pendingAt time.Time
		hasPending bool
		hits      []rolloutTokenHit
		lineNo    int
	)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		ts := parseRolloutTimestamp(row["timestamp"])
		rowType, _ := row["type"].(string)
		payload := asStringKeyMap(row["payload"])

		switch rowType {
		case "session_meta":
			if id := strings.TrimSpace(stringFromAny(payload["session_id"])); id != "" {
				sessionID = id
			} else if id := strings.TrimSpace(stringFromAny(payload["id"])); id != "" {
				sessionID = id
			}
		case "event_msg":
			eventType, _ := payload["type"].(string)
			switch eventType {
			case "token_count":
				// Official Codex rollout:
				// info.last_token_usage: {input_tokens, cached_input_tokens, output_tokens,
				//   reasoning_output_tokens, total_tokens}
				info := asStringKeyMap(payload["info"])
				last := asStringKeyMap(info["last_token_usage"])
				if len(last) == 0 {
					last = asStringKeyMap(info["lastTokenUsage"])
				}
				// Fall back to cumulative total_token_usage only if last is missing.
				if len(last) == 0 {
					last = asStringKeyMap(info["total_token_usage"])
				}
				b := breakdownFromUsageMap(last)
				if !b.valid() {
					// Direct snake_case parse for older shapes.
					b = tokenBreakdown{
						Input:     int64(anyToFloat(last["input_tokens"])),
						Cached:    int64(anyToFloat(last["cached_input_tokens"])),
						Output:    int64(anyToFloat(last["output_tokens"])),
						Reasoning: int64(anyToFloat(last["reasoning_output_tokens"])),
						Total:     int64(anyToFloat(last["total_tokens"])),
					}
					b.normalize()
				}
				if b.valid() {
					pending = b
					pendingAt = ts
					hasPending = true
				}
			case "task_complete":
				if !hasPending || !pending.valid() {
					continue
				}
				turnID := strings.TrimSpace(stringFromAny(payload["turn_id"]))
				if turnID == "" {
					turnID = strings.TrimSpace(stringFromAny(payload["turnId"]))
				}
				if turnID == "" {
					turnID = "line-" + strconv.Itoa(lineNo)
				}
				at := ts
				if at.IsZero() {
					at = pendingAt
				}
				if strings.TrimSpace(sessionID) == "" {
					sessionID = sessionIDFromRolloutPath(path)
				}
				hits = append(hits, rolloutTokenHit{
					SessionID: sessionID,
					TurnID:    turnID,
					Breakdown: pending,
					At:        at,
				})
				hasPending = false
				pending = tokenBreakdown{}
			}
		}
	}

	// Flush trailing token_count without task_complete.
	if hasPending && pending.valid() {
		if strings.TrimSpace(sessionID) == "" {
			sessionID = sessionIDFromRolloutPath(path)
		}
		hits = append(hits, rolloutTokenHit{
			SessionID: sessionID,
			TurnID:    "flush-" + strconv.Itoa(lineNo),
			Breakdown: pending,
			At:        pendingAt,
		})
	}
	return hits
}

func sessionIDFromRolloutPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	// rollout-2026-07-22T21-59-58-019f8a20-324f-7d72-a61b-99acb397fd3c
	parts := strings.Split(base, "-")
	if len(parts) >= 5 {
		// uuid starts near the end: 019f8a20-324f-7d72-a61b-99acb397fd3c (5 segments)
		return strings.Join(parts[len(parts)-5:], "-")
	}
	return base
}

func parseRolloutTimestamp(value any) time.Time {
	text := strings.TrimSpace(stringFromAny(value))
	if text == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return parsed.In(time.Local)
	}
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed.In(time.Local)
	}
	return time.Time{}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

