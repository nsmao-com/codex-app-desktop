package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const localUsageTurnRetentionDays = 60

// localDayStats is one calendar day's aggregated spend for a single runtime.
type localDayStats struct {
	Tokens    int64 `json:"tokens"`
	Input     int64 `json:"inputTokens,omitempty"`
	Cached    int64 `json:"cachedInputTokens,omitempty"`
	Output    int64 `json:"outputTokens,omitempty"`
	Reasoning int64 `json:"reasoningOutputTokens,omitempty"`
}

// localRuntimeBucket holds lifetime + daily totals for codex | grok | claude.
type localRuntimeBucket struct {
	LifetimeTokens    int64                    `json:"lifetimeTokens"`
	LifetimeInput      int64                    `json:"lifetimeInputTokens"`
	LifetimeCached     int64                    `json:"lifetimeCachedInputTokens"`
	LifetimeOutput     int64                    `json:"lifetimeOutputTokens"`
	LifetimeReasoning  int64                    `json:"lifetimeReasoningTokens"`
	Days              map[string]localDayStats `json:"days"`
}

type localTurnUsage struct {
	Runtime   string `json:"runtime"`
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	Tokens    int64  `json:"tokens"`
	Input     int64  `json:"inputTokens,omitempty"`
	Cached    int64  `json:"cachedInputTokens,omitempty"`
	Output    int64  `json:"outputTokens,omitempty"`
	Reasoning int64  `json:"reasoningOutputTokens,omitempty"`
	Day       string `json:"day"`
	UpdatedAt int64  `json:"updatedAt"`
}

// localUsageFile version 2 stores spend per runtime so Grok and Codex never share totals.
// Legacy v1 fields (top-level lifetimeTokens/days) are migrated into byRuntime.codex on load.
type localUsageFile struct {
	Version         int                            `json:"version"`
	LifetimeTokens   int64                          `json:"lifetimeTokens,omitempty"` // legacy v1
	Days            map[string]int64               `json:"days,omitempty"`          // legacy v1
	Turns           map[string]localTurnUsage      `json:"turns"`
	ByRuntime       map[string]*localRuntimeBucket `json:"byRuntime"`
	SeededFromCloud bool                           `json:"seededFromCloud,omitempty"`
}

type tokenBreakdown struct {
	Input     int64
	Cached    int64
	Output    int64
	Reasoning int64
	Total     int64
}

func usagePath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "usage.json")
}

func emptyLocalUsage() *localUsageFile {
	return &localUsageFile{
		Version:   2,
		Turns:     make(map[string]localTurnUsage),
		ByRuntime: make(map[string]*localRuntimeBucket),
	}
}

func emptyRuntimeBucket() *localRuntimeBucket {
	return &localRuntimeBucket{Days: make(map[string]localDayStats)}
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
	if result.Turns == nil {
		result.Turns = make(map[string]localTurnUsage)
	}
	if result.ByRuntime == nil {
		result.ByRuntime = make(map[string]*localRuntimeBucket)
	}
	migrateLocalUsage(result)
	return result
}

func migrateLocalUsage(usage *localUsageFile) {
	if usage == nil {
		return
	}
	// Promote legacy v1 top-level days/lifetime into the codex bucket once.
	if usage.Version < 2 || (len(usage.ByRuntime) == 0 && (usage.LifetimeTokens > 0 || len(usage.Days) > 0)) {
		bucket := usage.ensureRuntime("codex")
		if bucket.LifetimeTokens == 0 && usage.LifetimeTokens > 0 {
			bucket.LifetimeTokens = usage.LifetimeTokens
		}
		if len(usage.Days) > 0 {
			for day, tokens := range usage.Days {
				if tokens <= 0 || strings.TrimSpace(day) == "" {
					continue
				}
				prev := bucket.Days[day]
				if prev.Tokens == 0 {
					bucket.Days[day] = localDayStats{Tokens: tokens}
				}
			}
		}
		// Clear legacy fields after migration so they are not double-counted on next write.
		usage.LifetimeTokens = 0
		usage.Days = nil
		usage.Version = 2
	}
	if usage.Version < 2 {
		usage.Version = 2
	}
	// Normalize turn runtimes and re-aggregate buckets from turns when possible.
	for key, turn := range usage.Turns {
		runtime := normalizeUsageRuntime(turn.Runtime)
		if turn.Runtime == "" {
			turn.Runtime = runtime
			usage.Turns[key] = turn
		}
		if turn.Tokens <= 0 {
			turn.Tokens = turn.Input + turn.Cached + turn.Output + turn.Reasoning
			usage.Turns[key] = turn
		}
	}
	for _, bucket := range usage.ByRuntime {
		if bucket == nil {
			continue
		}
		if bucket.Days == nil {
			bucket.Days = make(map[string]localDayStats)
		}
	}
}

func (u *localUsageFile) ensureRuntime(runtime string) *localRuntimeBucket {
	runtime = normalizeUsageRuntime(runtime)
	if u.ByRuntime == nil {
		u.ByRuntime = make(map[string]*localRuntimeBucket)
	}
	bucket := u.ByRuntime[runtime]
	if bucket == nil {
		bucket = emptyRuntimeBucket()
		u.ByRuntime[runtime] = bucket
	}
	if bucket.Days == nil {
		bucket.Days = make(map[string]localDayStats)
	}
	return bucket
}

func normalizeUsageRuntime(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "grok":
		return "grok"
	case "claude":
		return "claude"
	default:
		return "codex"
	}
}

func persistLocalUsage(settingsPath string, usage *localUsageFile) {
	if usage == nil {
		return
	}
	usage.Version = 2
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

func turnUsageKey(runtime, threadID, turnID string) string {
	return normalizeUsageRuntime(runtime) + ":" + strings.TrimSpace(threadID) + ":" + strings.TrimSpace(turnID)
}

func localUsageIsEmpty(usage *localUsageFile) bool {
	if usage == nil {
		return true
	}
	if len(usage.Turns) > 0 {
		return false
	}
	for _, bucket := range usage.ByRuntime {
		if bucket == nil {
			continue
		}
		if bucket.LifetimeTokens > 0 {
			return false
		}
		for _, day := range bucket.Days {
			if day.Tokens > 0 {
				return false
			}
		}
	}
	// Legacy v1 residual
	if usage.LifetimeTokens > 0 {
		return false
	}
	for _, tokens := range usage.Days {
		if tokens > 0 {
			return false
		}
	}
	return true
}

func breakdownFromUsageMap(usage map[string]any) tokenBreakdown {
	if usage == nil {
		return tokenBreakdown{}
	}
	// Prefer already-normalized maps; also accept snake_case.
	normalized := normalizeTokenUsageMap(usage)
	if normalized == nil {
		return tokenBreakdown{}
	}
	b := tokenBreakdown{
		Input:     int64(anyToFloat(normalized["inputTokens"])),
		Cached:    int64(anyToFloat(normalized["cachedInputTokens"])),
		Output:    int64(anyToFloat(normalized["outputTokens"])),
		Reasoning: int64(anyToFloat(normalized["reasoningOutputTokens"])),
		Total:     int64(anyToFloat(normalized["totalTokens"])),
	}
	if b.Total <= 0 {
		b.Total = b.Input + b.Cached + b.Output + b.Reasoning
	}
	return b
}

func (b tokenBreakdown) valid() bool {
	return b.Total > 0 || b.Input > 0 || b.Cached > 0 || b.Output > 0 || b.Reasoning > 0
}

func (b *tokenBreakdown) normalize() {
	if b.Total <= 0 {
		b.Total = b.Input + b.Cached + b.Output + b.Reasoning
	}
}

// recordLocalTurnUsage is the legacy total-only entrypoint (Codex cloud events).
func (s *AppService) recordLocalTurnUsage(threadID, turnID string, tokens int64, at time.Time) {
	s.persistTurnUsage("codex", threadID, turnID, tokenBreakdown{Total: tokens}, at)
}

// persistTurnUsage writes one turn's spend into the runtime-scoped local usage store.
func (s *AppService) persistTurnUsage(runtime, threadID, turnID string, b tokenBreakdown, at time.Time) {
	runtime = normalizeUsageRuntime(runtime)
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	b.normalize()
	if threadID == "" || turnID == "" || !b.valid() {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	usage := loadLocalUsage(s.settingsPath)
	if !applyTurnToUsageDetailed(usage, runtime, threadID, turnID, b, at) {
		return
	}
	pruneLocalUsageTurns(usage, at)
	persistLocalUsage(s.settingsPath, usage)
}

func applyTurnToUsageDetailed(
	usage *localUsageFile,
	runtime, threadID, turnID string,
	b tokenBreakdown,
	at time.Time,
) bool {
	if usage == nil || !b.valid() {
		return false
	}
	runtime = normalizeUsageRuntime(runtime)
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	b.normalize()
	day := localDayKey(at)
	key := turnUsageKey(runtime, threadID, turnID)
	bucket := usage.ensureRuntime(runtime)

	prev, hadPrev := usage.Turns[key]
	if hadPrev &&
		prev.Tokens == b.Total &&
		prev.Input == b.Input &&
		prev.Cached == b.Cached &&
		prev.Output == b.Output &&
		prev.Reasoning == b.Reasoning &&
		prev.Day == day &&
		normalizeUsageRuntime(prev.Runtime) == runtime {
		return false
	}

	// Undo previous contribution for this turn key.
	if hadPrev {
		prevRuntime := normalizeUsageRuntime(prev.Runtime)
		prevBucket := usage.ensureRuntime(prevRuntime)
		if prev.Day != "" {
			prevDay := prevBucket.Days[prev.Day]
			prevDay.Tokens -= prev.Tokens
			prevDay.Input -= prev.Input
			prevDay.Cached -= prev.Cached
			prevDay.Output -= prev.Output
			prevDay.Reasoning -= prev.Reasoning
			if prevDay.Tokens <= 0 && prevDay.Input <= 0 && prevDay.Cached <= 0 && prevDay.Output <= 0 && prevDay.Reasoning <= 0 {
				delete(prevBucket.Days, prev.Day)
			} else {
				prevBucket.Days[prev.Day] = clampDayStats(prevDay)
			}
		}
		prevBucket.LifetimeTokens -= prev.Tokens
		prevBucket.LifetimeInput -= prev.Input
		prevBucket.LifetimeCached -= prev.Cached
		prevBucket.LifetimeOutput -= prev.Output
		prevBucket.LifetimeReasoning -= prev.Reasoning
		clampRuntimeBucket(prevBucket)
	}

	dayStats := bucket.Days[day]
	dayStats.Tokens += b.Total
	dayStats.Input += b.Input
	dayStats.Cached += b.Cached
	dayStats.Output += b.Output
	dayStats.Reasoning += b.Reasoning
	bucket.Days[day] = dayStats

	bucket.LifetimeTokens += b.Total
	bucket.LifetimeInput += b.Input
	bucket.LifetimeCached += b.Cached
	bucket.LifetimeOutput += b.Output
	bucket.LifetimeReasoning += b.Reasoning
	clampRuntimeBucket(bucket)

	usage.Turns[key] = localTurnUsage{
		Runtime:   runtime,
		ThreadID:  threadID,
		TurnID:    turnID,
		Tokens:    b.Total,
		Input:     b.Input,
		Cached:    b.Cached,
		Output:    b.Output,
		Reasoning: b.Reasoning,
		Day:       day,
		UpdatedAt: at.Unix(),
	}
	return true
}

func clampDayStats(day localDayStats) localDayStats {
	if day.Tokens < 0 {
		day.Tokens = 0
	}
	if day.Input < 0 {
		day.Input = 0
	}
	if day.Cached < 0 {
		day.Cached = 0
	}
	if day.Output < 0 {
		day.Output = 0
	}
	if day.Reasoning < 0 {
		day.Reasoning = 0
	}
	return day
}

func clampRuntimeBucket(bucket *localRuntimeBucket) {
	if bucket == nil {
		return
	}
	if bucket.LifetimeTokens < 0 {
		bucket.LifetimeTokens = 0
	}
	if bucket.LifetimeInput < 0 {
		bucket.LifetimeInput = 0
	}
	if bucket.LifetimeCached < 0 {
		bucket.LifetimeCached = 0
	}
	if bucket.LifetimeOutput < 0 {
		bucket.LifetimeOutput = 0
	}
	if bucket.LifetimeReasoning < 0 {
		bucket.LifetimeReasoning = 0
	}
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
	return s.localUsageSummaryFor(normalizeUsageRuntime(s.Settings().ActiveRuntime))
}

func (s *AppService) localUsageSummaryFor(runtime string) map[string]any {
	runtime = normalizeUsageRuntime(runtime)
	s.mu.Lock()
	usage := loadLocalUsage(s.settingsPath)
	s.mu.Unlock()
	return buildLocalUsageResponse(usage, runtime)
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
	// Only seed into the empty codex bucket — never overwrite Grok/Claude.
	codexBucket := usage.ensureRuntime("codex")
	if codexBucket.LifetimeTokens > 0 || len(codexBucket.Days) > 0 {
		return false
	}
	// Also refuse if any codex turns already exist.
	for _, turn := range usage.Turns {
		if normalizeUsageRuntime(turn.Runtime) == "codex" {
			return false
		}
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
		codexBucket.Days[day] = localDayStats{Tokens: tokens}
		changed = true
	}
	if lifetime > 0 {
		codexBucket.LifetimeTokens = lifetime
		changed = true
	} else if len(codexBucket.Days) > 0 {
		var sum int64
		for _, day := range codexBucket.Days {
			sum += day.Tokens
		}
		codexBucket.LifetimeTokens = sum
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

func buildLocalUsageResponse(usage *localUsageFile, runtime string) map[string]any {
	if usage == nil {
		usage = emptyLocalUsage()
	}
	runtime = normalizeUsageRuntime(runtime)
	bucket := usage.ByRuntime[runtime]
	if bucket == nil {
		bucket = emptyRuntimeBucket()
	}

	type dayBucket struct {
		day   string
		stats localDayStats
	}
	items := make([]dayBucket, 0, len(bucket.Days))
	var peak int64
	for day, stats := range bucket.Days {
		if stats.Tokens <= 0 || strings.TrimSpace(day) == "" {
			continue
		}
		items = append(items, dayBucket{day: day, stats: stats})
		if stats.Tokens > peak {
			peak = stats.Tokens
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].day > items[j].day })

	// Build day map for streak helper (token totals only).
	dayTotals := make(map[string]int64, len(bucket.Days))
	for day, stats := range bucket.Days {
		if stats.Tokens > 0 {
			dayTotals[day] = stats.Tokens
		}
	}
	currentStreak, longestStreak := computeUsageStreaks(dayTotals, time.Now())

	daily := make([]map[string]any, 0, len(items))
	for _, item := range items {
		daily = append(daily, map[string]any{
			"startDate":             item.day,
			"tokens":                item.stats.Tokens,
			"inputTokens":           item.stats.Input,
			"cachedInputTokens":     item.stats.Cached,
			"outputTokens":          item.stats.Output,
			"reasoningOutputTokens": item.stats.Reasoning,
		})
	}

	lifetime := bucket.LifetimeTokens
	if lifetime < 0 {
		lifetime = 0
	}

	return map[string]any{
		"summary": map[string]any{
			"lifetimeTokens":            lifetime,
			"lifetimeInputTokens":       maxInt64(0, bucket.LifetimeInput),
			"lifetimeCachedInputTokens": maxInt64(0, bucket.LifetimeCached),
			"lifetimeOutputTokens":      maxInt64(0, bucket.LifetimeOutput),
			"lifetimeReasoningTokens":   maxInt64(0, bucket.LifetimeReasoning),
			"peakDailyTokens":           peak,
			"currentStreakDays":         currentStreak,
			"longestStreakDays":         longestStreak,
			"longestRunningTurnSec":     nil,
		},
		"dailyUsageBuckets": daily,
		"runtime":           runtime,
		"source":            "local",
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
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

	// Longest streak over all active days.
	keys := make([]string, 0, len(active))
	for day := range active {
		keys = append(keys, day)
	}
	sort.Strings(keys)
	run := 1
	longest = 1
	for i := 1; i < len(keys); i++ {
		prev, errPrev := time.ParseInLocation("2006-01-02", keys[i-1], time.Local)
		cur, errCur := time.ParseInLocation("2006-01-02", keys[i], time.Local)
		if errPrev != nil || errCur != nil {
			run = 1
			continue
		}
		if cur.Sub(prev) == 24*time.Hour {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 1
		}
	}

	// Current streak ending today or yesterday.
	cursor := now.In(time.Local)
	todayKey := localDayKey(cursor)
	if _, ok := active[todayKey]; !ok {
		cursor = cursor.AddDate(0, 0, -1)
		if _, ok := active[localDayKey(cursor)]; !ok {
			return 0, longest
		}
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
	threadID, turnID, b, ok := extractTurnTokenBreakdown(data)
	return threadID, turnID, b.Total, ok
}

func extractTurnTokenBreakdown(data map[string]any) (threadID, turnID string, b tokenBreakdown, ok bool) {
	if data == nil {
		return "", "", tokenBreakdown{}, false
	}
	threadID, _ = data["threadId"].(string)
	if threadID == "" {
		if thread, okMap := data["thread"].(map[string]any); okMap {
			threadID, _ = thread["id"].(string)
		}
	}
	turnID, _ = data["turnId"].(string)
	if turnID == "" {
		if turn, okMap := data["turn"].(map[string]any); okMap {
			turnID, _ = turn["id"].(string)
		}
	}
	tokenUsage, _ := data["tokenUsage"].(map[string]any)
	if tokenUsage == nil {
		tokenUsage, _ = data["token_usage"].(map[string]any)
	}
	if tokenUsage == nil {
		return threadID, turnID, tokenBreakdown{}, false
	}
	// Prefer per-turn "last" so session cumulative "total" never double-counts.
	last, _ := tokenUsage["last"].(map[string]any)
	if last == nil {
		last = tokenUsage
	}
	b = breakdownFromUsageMap(last)
	// Older extract path missed cache; include it in total.
	if b.Total <= 0 {
		b.Total = b.Input + b.Cached + b.Output + b.Reasoning
	}
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" || !b.valid() {
		return threadID, turnID, b, false
	}
	return threadID, turnID, b, true
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
		parsed, err := strconvParseFloat(typed)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func strconvParseFloat(value string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(value), 64)
}
