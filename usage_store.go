package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const localUsageTurnRetentionDays = 60

type localTurnUsage struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	Tokens    int64  `json:"tokens"`
	Day       string `json:"day"`
	UpdatedAt int64  `json:"updatedAt"`
}

type localUsageFile struct {
	Version         int                       `json:"version"`
	LifetimeTokens   int64                     `json:"lifetimeTokens"`
	Days            map[string]int64          `json:"days"`
	Turns           map[string]localTurnUsage `json:"turns"`
	SeededFromCloud bool                      `json:"seededFromCloud,omitempty"`
}

func usagePath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "usage.json")
}

func emptyLocalUsage() *localUsageFile {
	return &localUsageFile{
		Version: 1,
		Days:    make(map[string]int64),
		Turns:   make(map[string]localTurnUsage),
	}
}

func loadLocalUsage(settingsPath string) *localUsageFile {
	result := emptyLocalUsage()
	payload, err := os.ReadFile(usagePath(settingsPath))
	if err != nil {
		return result
	}
	if err := json.Unmarshal(payload, result); err != nil {
		return emptyLocalUsage()
	}
	if result.Days == nil {
		result.Days = make(map[string]int64)
	}
	if result.Turns == nil {
		result.Turns = make(map[string]localTurnUsage)
	}
	if result.Version <= 0 {
		result.Version = 1
	}
	return result
}

func persistLocalUsage(settingsPath string, usage *localUsageFile) {
	if usage == nil {
		return
	}
	payload, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return
	}
	path := usagePath(settingsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, payload, 0o600)
}

func localDayKey(at time.Time) string {
	return at.In(time.Local).Format("2006-01-02")
}

func turnUsageKey(threadID, turnID string) string {
	return strings.TrimSpace(threadID) + ":" + strings.TrimSpace(turnID)
}

func localUsageIsEmpty(usage *localUsageFile) bool {
	if usage == nil {
		return true
	}
	if usage.LifetimeTokens > 0 || len(usage.Turns) > 0 {
		return false
	}
	for _, tokens := range usage.Days {
		if tokens > 0 {
			return false
		}
	}
	return true
}

func (s *AppService) recordLocalTurnUsage(threadID, turnID string, tokens int64, at time.Time) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" || tokens <= 0 {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	usage := loadLocalUsage(s.settingsPath)
	if !applyTurnToUsage(usage, threadID, turnID, tokens, at) {
		return
	}
	pruneLocalUsageTurns(usage, at)
	persistLocalUsage(s.settingsPath, usage)
}

func applyTurnToUsage(usage *localUsageFile, threadID, turnID string, tokens int64, at time.Time) bool {
	if usage == nil || tokens <= 0 {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	day := localDayKey(at)
	key := turnUsageKey(threadID, turnID)

	prev, hadPrev := usage.Turns[key]
	prevTokens := int64(0)
	prevDay := ""
	if hadPrev {
		prevTokens = prev.Tokens
		prevDay = prev.Day
	}
	if hadPrev && prevTokens == tokens && prevDay == day {
		return false
	}

	if hadPrev && prevDay != "" {
		usage.Days[prevDay] = usage.Days[prevDay] - prevTokens
		if usage.Days[prevDay] <= 0 {
			delete(usage.Days, prevDay)
		}
		usage.LifetimeTokens -= prevTokens
	}

	usage.Days[day] = usage.Days[day] + tokens
	usage.LifetimeTokens += tokens
	if usage.LifetimeTokens < 0 {
		usage.LifetimeTokens = 0
	}
	usage.Turns[key] = localTurnUsage{
		ThreadID:  threadID,
		TurnID:    turnID,
		Tokens:    tokens,
		Day:       day,
		UpdatedAt: at.Unix(),
	}
	return true
}

func pruneLocalUsageTurns(usage *localUsageFile, now time.Time) {
	if usage == nil || len(usage.Turns) == 0 {
		return
	}
	cutoff := now.AddDate(0, 0, -localUsageTurnRetentionDays).Unix()
	for key, turn := range usage.Turns {
		if turn.UpdatedAt > 0 && turn.UpdatedAt < cutoff {
			delete(usage.Turns, key)
		}
	}
}

func (s *AppService) localUsageSummary() map[string]any {
	s.mu.Lock()
	usage := loadLocalUsage(s.settingsPath)
	s.mu.Unlock()
	return buildLocalUsageResponse(usage)
}

func (s *AppService) seedLocalUsageFromCloud(cloud map[string]any) bool {
	if cloud == nil {
		return false
	}
	summary := asStringKeyMap(cloud["summary"])
	buckets := asAnySlice(cloud["dailyUsageBuckets"])
	if len(buckets) == 0 {
		buckets = asAnySlice(cloud["daily_usage_buckets"])
	}
	lifetime := int64(anyToFloat(summary["lifetimeTokens"]))
	if lifetime <= 0 {
		lifetime = int64(anyToFloat(summary["lifetime_tokens"]))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	usage := loadLocalUsage(s.settingsPath)
	if !localUsageIsEmpty(usage) {
		return false
	}

	changed := false
	for _, item := range buckets {
		record := asStringKeyMap(item)
		if len(record) == 0 {
			continue
		}
		day, _ := record["startDate"].(string)
		if day == "" {
			day, _ = record["start_date"].(string)
		}
		day = strings.TrimSpace(day)
		if len(day) >= 10 {
			day = day[:10]
		}
		tokens := int64(anyToFloat(record["tokens"]))
		if day == "" || tokens <= 0 {
			continue
		}
		usage.Days[day] = tokens
		changed = true
	}
	if lifetime > 0 {
		usage.LifetimeTokens = lifetime
		changed = true
	} else if len(usage.Days) > 0 {
		var sum int64
		for _, tokens := range usage.Days {
			sum += tokens
		}
		usage.LifetimeTokens = sum
		changed = sum > 0
	}
	if !changed {
		return false
	}
	usage.SeededFromCloud = true
	persistLocalUsage(s.settingsPath, usage)
	return true
}

func asStringKeyMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	// Some RPC decoders yield map[string]interface{} aliases already covered above.
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var next map[string]any
	if err := json.Unmarshal(raw, &next); err != nil || next == nil {
		return map[string]any{}
	}
	return next
}

func asAnySlice(value any) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		return typed
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var out []any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil
		}
		return out
	}
}


func buildLocalUsageResponse(usage *localUsageFile) map[string]any {
	if usage == nil {
		usage = emptyLocalUsage()
	}

	type dayBucket struct {
		day    string
		tokens int64
	}
	buckets := make([]dayBucket, 0, len(usage.Days))
	var peak int64
	for day, tokens := range usage.Days {
		if tokens <= 0 || strings.TrimSpace(day) == "" {
			continue
		}
		buckets = append(buckets, dayBucket{day: day, tokens: tokens})
		if tokens > peak {
			peak = tokens
		}
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].day > buckets[j].day })

	daily := make([]map[string]any, 0, len(buckets))
	for _, item := range buckets {
		daily = append(daily, map[string]any{
			"startDate": item.day,
			"tokens":    item.tokens,
		})
	}

	currentStreak, longestStreak := computeUsageStreaks(usage.Days, time.Now())
	lifetime := usage.LifetimeTokens
	if lifetime < 0 {
		lifetime = 0
	}

	return map[string]any{
		"summary": map[string]any{
			"lifetimeTokens":       lifetime,
			"peakDailyTokens":      peak,
			"currentStreakDays":    currentStreak,
			"longestStreakDays":    longestStreak,
			"longestRunningTurnSec": nil,
		},
		"dailyUsageBuckets": daily,
		"source":            "local",
	}
}

func computeUsageStreaks(days map[string]int64, now time.Time) (current, longest int) {
	if len(days) == 0 {
		return 0, 0
	}
	active := make(map[string]struct{}, len(days))
	for day, tokens := range days {
		if tokens > 0 && strings.TrimSpace(day) != "" {
			active[day] = struct{}{}
		}
	}
	if len(active) == 0 {
		return 0, 0
	}

	sorted := make([]string, 0, len(active))
	for day := range active {
		sorted = append(sorted, day)
	}
	sort.Strings(sorted)

	run := 1
	longest = 1
	for i := 1; i < len(sorted); i++ {
		prev, errPrev := time.ParseInLocation("2006-01-02", sorted[i-1], time.Local)
		cur, errCur := time.ParseInLocation("2006-01-02", sorted[i], time.Local)
		if errPrev != nil || errCur != nil {
			run = 1
			continue
		}
		if int(math.Round(cur.Sub(prev).Hours()/24)) == 1 {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 1
		}
	}

	today := localDayKey(now)
	yesterday := localDayKey(now.AddDate(0, 0, -1))
	start := ""
	if _, ok := active[today]; ok {
		start = today
	} else if _, ok := active[yesterday]; ok {
		start = yesterday
	}
	if start == "" {
		return 0, longest
	}
	cursor, err := time.ParseInLocation("2006-01-02", start, time.Local)
	if err != nil {
		return 0, longest
	}
	for {
		key := localDayKey(cursor)
		if _, ok := active[key]; !ok {
			break
		}
		current++
		cursor = cursor.AddDate(0, 0, -1)
	}
	if current > longest {
		longest = current
	}
	return current, longest
}

func extractTurnTokens(data map[string]any) (threadID, turnID string, tokens int64, ok bool) {
	if data == nil {
		return "", "", 0, false
	}
	threadID, _ = data["threadId"].(string)
	if threadID == "" {
		if thread, ok := data["thread"].(map[string]any); ok {
			threadID, _ = thread["id"].(string)
		}
	}
	turnID, _ = data["turnId"].(string)
	if turnID == "" {
		if turn, ok := data["turn"].(map[string]any); ok {
			turnID, _ = turn["id"].(string)
		}
	}
	tokenUsage, _ := data["tokenUsage"].(map[string]any)
	if tokenUsage == nil {
		tokenUsage, _ = data["token_usage"].(map[string]any)
	}
	if tokenUsage == nil {
		return threadID, turnID, 0, false
	}
	// Prefer per-turn "last" so session cumulative "total" never double-counts.
	last, _ := tokenUsage["last"].(map[string]any)
	if last == nil {
		last = tokenUsage
	}
	tokens = int64(anyToFloat(last["totalTokens"]))
	if tokens <= 0 {
		tokens = int64(anyToFloat(last["total_tokens"]))
	}
	if tokens <= 0 {
		input := int64(anyToFloat(last["inputTokens"]))
		if input <= 0 {
			input = int64(anyToFloat(last["input_tokens"]))
		}
		output := int64(anyToFloat(last["outputTokens"]))
		if output <= 0 {
			output = int64(anyToFloat(last["output_tokens"]))
		}
		reasoning := int64(anyToFloat(last["reasoningOutputTokens"]))
		if reasoning <= 0 {
			reasoning = int64(anyToFloat(last["reasoning_output_tokens"]))
		}
		tokens = input + output + reasoning
	}
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" || tokens <= 0 {
		return threadID, turnID, tokens, false
	}
	return threadID, turnID, tokens, true
}

func anyToFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case uint64:
		return float64(typed)
	case uint32:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0
		}
		return parsed
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0
		}
		trimmed = strings.TrimSuffix(trimmed, "n")
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return parsed
		}
		return 0
	default:
		return 0
	}
}
