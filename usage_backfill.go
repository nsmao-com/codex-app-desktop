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
// session rollouts (token_count.total_token_usage deltas). The native rollout
// history is the source of truth, so an existing stale aggregate is replaced too.
func (s *AppService) backfillLocalUsageFromRollouts() bool {
	home := resolveCodexHome()
	if strings.TrimSpace(home) == "" {
		return false
	}
	hits := scanCodexRolloutTokenUsage(home)
	if len(hits) == 0 {
		return false
	}

	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	usage := s.localUsageLocked()
	changed := resetRuntimeUsage(usage, "codex")
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
	s.scheduleLocalUsagePersistLocked()
	return true
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
		pendingLast       tokenBreakdown
		pendingCumulative tokenBreakdown
		cumulativeBase    tokenBreakdown
		pendingAt         time.Time
		hasPending        bool
		hasCumulative     bool
		hits              []rolloutTokenHit
		lineNo            int
	)

	scanner := bufio.NewScanner(file)
	// Tool-heavy turns can serialize large event rows. Keep the scanner ceiling
	// above realistic rollout lines so one oversized row cannot truncate a file.
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
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
				lastBreakdown := breakdownFromUsageMap(last)
				if !lastBreakdown.valid() {
					// Direct snake_case parse for older shapes.
					lastBreakdown = tokenBreakdown{
						Input:     int64(anyToFloat(last["input_tokens"])),
						Cached:    int64(anyToFloat(last["cached_input_tokens"])),
						Output:    int64(anyToFloat(last["output_tokens"])),
						Reasoning: int64(anyToFloat(last["reasoning_output_tokens"])),
						Total:     int64(anyToFloat(last["total_tokens"])),
					}
					lastBreakdown.normalize()
				}

				total := asStringKeyMap(info["total_token_usage"])
				if len(total) == 0 {
					total = asStringKeyMap(info["totalTokenUsage"])
				}
				cumulative := breakdownFromUsageMap(total)
				if !cumulative.valid() {
					cumulative = tokenBreakdown{
						Input:     int64(anyToFloat(total["input_tokens"])),
						Cached:    int64(anyToFloat(total["cached_input_tokens"])),
						Output:    int64(anyToFloat(total["output_tokens"])),
						Reasoning: int64(anyToFloat(total["reasoning_output_tokens"])),
						Total:     int64(anyToFloat(total["total_tokens"])),
					}
					cumulative.normalize()
				}
				if lastBreakdown.valid() || cumulative.valid() {
					pendingLast = lastBreakdown
					pendingCumulative = cumulative
					pendingAt = ts
					hasPending = true
					hasCumulative = cumulative.valid()
				}
			case "task_complete":
				if !hasPending {
					continue
				}
				breakdown := rolloutTurnBreakdown(pendingCumulative, cumulativeBase, pendingLast, hasCumulative)
				if hasCumulative {
					cumulativeBase = pendingCumulative
				}
				if !breakdown.valid() {
					hasPending = false
					continue
				}
				turnID := strings.TrimSpace(stringFromAny(payload["turn_id"]))
				if turnID == "" {
					turnID = strings.TrimSpace(stringFromAny(payload["turnId"]))
				}
				at := ts
				if at.IsZero() {
					at = pendingAt
				}
				if strings.TrimSpace(sessionID) == "" {
					sessionID = sessionIDFromRolloutPath(path)
				}
				if turnID == "" {
					turnID = "codex-turn-" + sessionID + "-" + strconv.Itoa(lineNo)
				}
				hits = append(hits, rolloutTokenHit{
					SessionID: sessionID,
					TurnID:    turnID,
					Breakdown: breakdown,
					At:        at,
				})
				hasPending = false
				pendingLast = tokenBreakdown{}
				pendingCumulative = tokenBreakdown{}
				hasCumulative = false
			}
		}
	}

	// Flush trailing token_count without task_complete.
	if hasPending {
		breakdown := rolloutTurnBreakdown(pendingCumulative, cumulativeBase, pendingLast, hasCumulative)
		if !breakdown.valid() {
			return hits
		}
		if strings.TrimSpace(sessionID) == "" {
			sessionID = sessionIDFromRolloutPath(path)
		}
		hits = append(hits, rolloutTokenHit{
			SessionID: sessionID,
			TurnID:    "codex-flush-" + sessionID + "-" + strconv.Itoa(lineNo),
			Breakdown: breakdown,
			At:        pendingAt,
		})
	}
	return hits
}

// A Codex task can invoke the model many times. last_token_usage only describes
// the final invocation, while total_token_usage is cumulative for the rollout.
// The task's real spend is therefore the delta since the previous task boundary.
func rolloutTurnBreakdown(current, previous, fallback tokenBreakdown, cumulative bool) tokenBreakdown {
	if !cumulative || !current.valid() {
		return fallback
	}
	// A provider/process reset can make cumulative counters decrease. In that
	// uncommon case the per-call value is safer than inventing a negative delta.
	if current.Total < previous.Total || current.Input < previous.Input {
		return fallback
	}
	delta := tokenBreakdown{
		Input:     current.Input - previous.Input,
		Cached:    max(int64(0), current.Cached-previous.Cached),
		Output:    max(int64(0), current.Output-previous.Output),
		Reasoning: max(int64(0), current.Reasoning-previous.Reasoning),
		Total:     current.Total - previous.Total,
	}
	if delta.valid() {
		return delta
	}
	return fallback
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
