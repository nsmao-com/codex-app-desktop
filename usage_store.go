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

const localUsageVersion = 4
const localUsageTurnRetentionDays = 60
const localUsagePersistDelay = 400 * time.Millisecond

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
	LifetimeInput     int64                    `json:"lifetimeInputTokens"`
	LifetimeCached    int64                    `json:"lifetimeCachedInputTokens"`
	LifetimeOutput    int64                    `json:"lifetimeOutputTokens"`
	LifetimeReasoning int64                    `json:"lifetimeReasoningTokens"`
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

// localUsageFile version 3 stores spend per runtime and canonicalizes external turn ownership.
// Legacy v1 fields (top-level lifetimeTokens/days) are migrated into byRuntime.codex on load.
type localUsageFile struct {
	Version         int                            `json:"version"`
	LifetimeTokens  int64                          `json:"lifetimeTokens,omitempty"` // legacy v1
	Days            map[string]int64               `json:"days,omitempty"`           // legacy v1
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
		Version:   localUsageVersion,
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
	if migrateLocalUsage(result) {
		// Persist the one-time repair immediately so a restart cannot reintroduce
		// the legacy runtime mismatch or stale aggregate buckets.
		persistLocalUsage(settingsPath, result)
	}
	return result
}

func migrateLocalUsage(usage *localUsageFile) bool {
	if usage == nil {
		return false
	}
	changed := false
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
		changed = true
	}
	if usage.Version < 2 {
		usage.Version = 2
		changed = true
	}
	// Older builds could write an external turn into a Codex bucket while keeping
	// the external prefix in the map key. Canonicalize both the key and runtime
	// before rebuilding aggregate buckets.
	if usage.Version < localUsageVersion {
		originalTurns := usage.Turns
		canonical := make(map[string]localTurnUsage, len(usage.Turns))
		for key, turn := range usage.Turns {
			runtime := usageRuntimeForTurn(key, turn)
			turn.Runtime = runtime
			if turn.Tokens <= 0 {
				turn.Tokens = turn.Input + turn.Cached + turn.Output + turn.Reasoning
			}
			canonicalKey := turnUsageKey(runtime, turn.ThreadID, turn.TurnID)
			if strings.TrimSpace(turn.ThreadID) == "" || strings.TrimSpace(turn.TurnID) == "" {
				continue
			}
			if previous, exists := canonical[canonicalKey]; !exists || usageTurnShouldReplace(previous, turn) {
				canonical[canonicalKey] = turn
			}
			if canonicalKey != key || normalizeUsageRuntime(turn.Runtime) != normalizeUsageRuntime(usage.Turns[key].Runtime) {
				changed = true
			}
		}
		if len(canonical) != len(usage.Turns) {
			changed = true
		}
		usage.Turns = canonical
		if rebuildUsageBucketsFromTurns(usage, originalTurns) {
			changed = true
		}
		usage.Version = localUsageVersion
		changed = true
	}
	// Normalize remaining turn fields for files already at the current version.
	for key, turn := range usage.Turns {
		runtime := normalizeUsageRuntime(turn.Runtime)
		if turn.Runtime != runtime {
			turn.Runtime = runtime
			usage.Turns[key] = turn
			changed = true
		}
		if turn.Tokens <= 0 {
			turn.Tokens = turn.Input + turn.Cached + turn.Output + turn.Reasoning
			usage.Turns[key] = turn
			changed = true
		}
	}
	for _, bucket := range usage.ByRuntime {
		if bucket == nil {
			continue
		}
		if bucket.Days == nil {
			bucket.Days = make(map[string]localDayStats)
			changed = true
		}
	}
	// Aggregate buckets can outlive the retained turn index, but they must never
	// be smaller than the turns that are still present. Older builds could keep a
	// cloud/total-only lifetime while later backfill populated detailed turns,
	// leaving Codex lifetime totals far below the visible breakdown.
	if repairUsageBucketsFromTurns(usage) {
		changed = true
	}
	return changed
}

func repairUsageBucketsFromTurns(usage *localUsageFile) bool {
	if usage == nil || len(usage.Turns) == 0 {
		return false
	}
	expected := make(map[string]*localRuntimeBucket)
	for _, turn := range usage.Turns {
		runtime := normalizeUsageRuntime(turn.Runtime)
		bucket := expected[runtime]
		if bucket == nil {
			bucket = emptyRuntimeBucket()
			expected[runtime] = bucket
		}
		addUsageTurnContribution(bucket, turn)
	}

	changed := false
	for runtime, minimum := range expected {
		bucket := usage.ensureRuntime(runtime)
		if bucket.LifetimeTokens < minimum.LifetimeTokens {
			bucket.LifetimeTokens = minimum.LifetimeTokens
			changed = true
		}
		if bucket.LifetimeInput < minimum.LifetimeInput {
			bucket.LifetimeInput = minimum.LifetimeInput
			changed = true
		}
		if bucket.LifetimeCached < minimum.LifetimeCached {
			bucket.LifetimeCached = minimum.LifetimeCached
			changed = true
		}
		if bucket.LifetimeOutput < minimum.LifetimeOutput {
			bucket.LifetimeOutput = minimum.LifetimeOutput
			changed = true
		}
		if bucket.LifetimeReasoning < minimum.LifetimeReasoning {
			bucket.LifetimeReasoning = minimum.LifetimeReasoning
			changed = true
		}
		for day, minimumDay := range minimum.Days {
			current := bucket.Days[day]
			next := current
			if next.Tokens < minimumDay.Tokens {
				next.Tokens = minimumDay.Tokens
			}
			if next.Input < minimumDay.Input {
				next.Input = minimumDay.Input
			}
			if next.Cached < minimumDay.Cached {
				next.Cached = minimumDay.Cached
			}
			if next.Output < minimumDay.Output {
				next.Output = minimumDay.Output
			}
			if next.Reasoning < minimumDay.Reasoning {
				next.Reasoning = minimumDay.Reasoning
			}
			if next != current {
				bucket.Days[day] = next
				changed = true
			}
		}
	}
	return changed
}

func usageRuntimeForTurn(key string, turn localTurnUsage) string {
	prefix := ""
	if index := strings.IndexByte(key, ':'); index > 0 {
		prefix = strings.TrimSpace(key[:index])
	}
	if runtime := normalizeUsageRuntime(prefix); strings.EqualFold(prefix, runtime) && runtime != "codex" {
		return runtime
	}
	turnID := strings.ToLower(strings.TrimSpace(turn.TurnID))
	switch {
	case strings.HasPrefix(turnID, "claude-turn-"):
		return "claude"
	case strings.HasPrefix(turnID, "grok-turn-"):
		return "grok"
	case strings.HasPrefix(turnID, "gemini-turn-"):
		return "gemini"
	case strings.HasPrefix(turnID, "opencode-turn-"):
		return "opencode"
	default:
		return normalizeUsageRuntime(turn.Runtime)
	}
}

func usageTurnShouldReplace(previous, next localTurnUsage) bool {
	if next.UpdatedAt != previous.UpdatedAt {
		return next.UpdatedAt > previous.UpdatedAt
	}
	return next.Tokens >= previous.Tokens
}

// rebuildUsageBucketsFromTurns removes every retained legacy turn from its
// original bucket, then adds the canonical set back. Totals older than the
// 60-day turn index remain untouched.
func rebuildUsageBucketsFromTurns(usage *localUsageFile, originalTurns map[string]localTurnUsage) bool {
	if usage == nil {
		return false
	}
	for _, turn := range originalTurns {
		removeUsageTurnContribution(usage.ensureRuntime(turn.Runtime), turn)
	}
	for _, turn := range usage.Turns {
		addUsageTurnContribution(usage.ensureRuntime(turn.Runtime), turn)
	}
	return len(originalTurns) > 0 || len(usage.Turns) > 0
}

func removeUsageTurnContribution(bucket *localRuntimeBucket, turn localTurnUsage) {
	if bucket == nil {
		return
	}
	bucket.LifetimeTokens -= turn.Tokens
	bucket.LifetimeInput -= turn.Input
	bucket.LifetimeCached -= turn.Cached
	bucket.LifetimeOutput -= turn.Output
	bucket.LifetimeReasoning -= turn.Reasoning
	if strings.TrimSpace(turn.Day) != "" {
		day := bucket.Days[turn.Day]
		day.Tokens -= turn.Tokens
		day.Input -= turn.Input
		day.Cached -= turn.Cached
		day.Output -= turn.Output
		day.Reasoning -= turn.Reasoning
		if day.Tokens <= 0 && day.Input <= 0 && day.Cached <= 0 && day.Output <= 0 && day.Reasoning <= 0 {
			delete(bucket.Days, turn.Day)
		} else {
			bucket.Days[turn.Day] = clampDayStats(day)
		}
	}
	clampRuntimeBucket(bucket)
}

func addUsageTurnContribution(bucket *localRuntimeBucket, turn localTurnUsage) {
	if bucket == nil {
		return
	}
	b := tokenBreakdown{
		Input: turn.Input, Cached: turn.Cached, Output: turn.Output,
		Reasoning: turn.Reasoning, Total: turn.Tokens,
	}
	b.normalize()
	if !b.valid() {
		return
	}
	bucket.LifetimeTokens += b.Total
	bucket.LifetimeInput += b.Input
	bucket.LifetimeCached += b.Cached
	bucket.LifetimeOutput += b.Output
	bucket.LifetimeReasoning += b.Reasoning
	if strings.TrimSpace(turn.Day) != "" {
		day := bucket.Days[turn.Day]
		day.Tokens += b.Total
		day.Input += b.Input
		day.Cached += b.Cached
		day.Output += b.Output
		day.Reasoning += b.Reasoning
		bucket.Days[turn.Day] = day
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

// resetRuntimeUsage removes the cached aggregate and turn index for one runtime
// before rebuilding it from that runtime's native history. Native sources are
// authoritative for their own totals; retaining an older bucket would preserve
// stale or duplicated usage forever.
func resetRuntimeUsage(usage *localUsageFile, runtime string) bool {
	if usage == nil {
		return false
	}
	runtime = normalizeUsageRuntime(runtime)
	changed := false
	for key, turn := range usage.Turns {
		if normalizeUsageRuntime(turn.Runtime) != runtime {
			continue
		}
		delete(usage.Turns, key)
		changed = true
	}
	if bucket := usage.ByRuntime[runtime]; bucket != nil {
		changed = changed || bucket.LifetimeTokens > 0 || bucket.LifetimeInput > 0 ||
			bucket.LifetimeCached > 0 || bucket.LifetimeOutput > 0 || bucket.LifetimeReasoning > 0 || len(bucket.Days) > 0
	}
	if usage.ByRuntime == nil {
		usage.ByRuntime = make(map[string]*localRuntimeBucket)
	}
	usage.ByRuntime[runtime] = emptyRuntimeBucket()
	return changed
}

func normalizeUsageRuntime(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "grok":
		return "grok"
	case "claude":
		return "claude"
	case "gemini":
		return "gemini"
	case "opencode", "open-code":
		return "opencode"
	default:
		return "codex"
	}
}

func persistLocalUsage(settingsPath string, usage *localUsageFile) {
	if usage == nil {
		return
	}
	usage.Version = localUsageVersion
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

	s.usageMu.Lock()
	defer s.usageMu.Unlock()

	usage := s.localUsageLocked()
	if !applyTurnToUsageDetailed(usage, runtime, threadID, turnID, b, at) {
		return
	}
	pruneLocalUsageTurns(usage, at)
	s.scheduleLocalUsagePersistLocked()
}

func (s *AppService) localUsageLocked() *localUsageFile {
	if s.usageCache == nil {
		s.usageCache = loadLocalUsage(s.settingsPath)
	}
	return s.usageCache
}

func (s *AppService) scheduleLocalUsagePersistLocked() {
	if s.usageFlushTimer != nil {
		s.usageFlushTimer.Stop()
	}
	s.usageFlushGen++
	generation := s.usageFlushGen
	s.usageFlushTimer = time.AfterFunc(localUsagePersistDelay, func() {
		s.flushLocalUsageGeneration(generation)
	})
}

func (s *AppService) flushLocalUsage() {
	s.usageMu.Lock()
	s.usageFlushGen++
	if s.usageFlushTimer != nil {
		s.usageFlushTimer.Stop()
		s.usageFlushTimer = nil
	}
	snapshot := cloneLocalUsage(s.usageCache)
	s.usageMu.Unlock()
	if snapshot != nil {
		s.usagePersistMu.Lock()
		persistLocalUsage(s.settingsPath, snapshot)
		s.usagePersistMu.Unlock()
	}
}

func (s *AppService) flushLocalUsageGeneration(generation uint64) {
	s.usageMu.Lock()
	if generation != s.usageFlushGen {
		s.usageMu.Unlock()
		return
	}
	s.usageFlushTimer = nil
	snapshot := cloneLocalUsage(s.usageCache)
	s.usageMu.Unlock()
	if snapshot != nil {
		s.usagePersistMu.Lock()
		s.usageMu.Lock()
		stillCurrent := generation == s.usageFlushGen
		s.usageMu.Unlock()
		if stillCurrent {
			persistLocalUsage(s.settingsPath, snapshot)
		}
		s.usagePersistMu.Unlock()
	}
}

func cloneLocalUsage(source *localUsageFile) *localUsageFile {
	if source == nil {
		return nil
	}
	clone := &localUsageFile{
		Version:         source.Version,
		LifetimeTokens:  source.LifetimeTokens,
		SeededFromCloud: source.SeededFromCloud,
		Days:            make(map[string]int64, len(source.Days)),
		Turns:           make(map[string]localTurnUsage, len(source.Turns)),
		ByRuntime:       make(map[string]*localRuntimeBucket, len(source.ByRuntime)),
	}
	for day, tokens := range source.Days {
		clone.Days[day] = tokens
	}
	for key, turn := range source.Turns {
		clone.Turns[key] = turn
	}
	for runtime, bucket := range source.ByRuntime {
		if bucket == nil {
			clone.ByRuntime[runtime] = nil
			continue
		}
		bucketClone := *bucket
		bucketClone.Days = make(map[string]localDayStats, len(bucket.Days))
		for day, stats := range bucket.Days {
			bucketClone.Days[day] = stats
		}
		clone.ByRuntime[runtime] = &bucketClone
	}
	return clone
}

func (s *AppService) shouldRunUsageBackfill(runtime string) bool {
	runtime = normalizeUsageRuntime(runtime)
	now := time.Now()
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if s.usageBackfillAt == nil {
		s.usageBackfillAt = make(map[string]time.Time)
	}
	if last := s.usageBackfillAt[runtime]; !last.IsZero() && now.Sub(last) < 5*time.Minute {
		return false
	}
	s.usageBackfillAt[runtime] = now
	return true
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
	// Rollout backfill uses the native Codex session id while live Wails events
	// may already be remapped to a NiceCodex UUID. The turn id is authoritative
	// and globally unique, so reuse an existing runtime+turn entry instead of
	// counting the same request twice under two thread ids.
	if _, exists := usage.Turns[key]; !exists {
		for existingKey, existing := range usage.Turns {
			if normalizeUsageRuntime(existing.Runtime) == runtime && strings.TrimSpace(existing.TurnID) == turnID {
				key = existingKey
				break
			}
		}
	}
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

	storedThreadID := threadID
	if hadPrev && strings.TrimSpace(prev.ThreadID) != "" {
		storedThreadID = prev.ThreadID
	}
	usage.Turns[key] = localTurnUsage{
		Runtime:   runtime,
		ThreadID:  storedThreadID,
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
	s.usageMu.Lock()
	usage := s.localUsageLocked()
	response := buildLocalUsageResponse(usage, runtime)
	s.usageMu.Unlock()
	return response
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

	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	usage := s.localUsageLocked()
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
	s.scheduleLocalUsagePersistLocked()
	return true
}

func asStringKeyMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		if typed == nil {
			return map[string]any{}
		}
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
		tokenUsage, _ = data["usage"].(map[string]any)
	}
	if tokenUsage == nil {
		// Some app-server builds put the counters directly on the notification
		// payload. normalizeTokenUsageMap ignores unrelated lifecycle fields.
		tokenUsage = data
	}
	// Prefer per-turn "last" so session cumulative "total" never double-counts.
	last, _ := tokenUsage["last"].(map[string]any)
	if last == nil {
		last, _ = tokenUsage["latest"].(map[string]any)
	}
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
