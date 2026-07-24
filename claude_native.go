package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Claude Code stores per-project transcripts at:
//
//	~/.claude/projects/<sanitized-cwd>/<session-uuid>.jsonl
//
// Path encoding (verified on Windows): non-alphanumeric runes become '-'.
// Example: D:\2024Dev\2026\llm_api → D--2024Dev-2026-llm-api

type claudeNativeSession struct {
	Summary ClaudeSessionSummary
	Path    string // absolute jsonl path
}

func claudeProjectSlug(workspace string) string {
	clean := filepath.Clean(strings.TrimSpace(workspace))
	if clean == "" {
		return ""
	}
	// Prefer absolute form so drive letter is stable on Windows.
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	var b strings.Builder
	b.Grow(len(clean))
	for _, r := range clean {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func claudeProjectsRoot() string {
	home := resolveClaudeHome()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "projects")
}

// scanClaudeNativeSessions lists Claude Code transcript sessions.
// When workspace is non-empty, that project dir is preferred first, then other projects.
func scanClaudeNativeSessions(workspace string) []claudeNativeSession {
	root := claudeProjectsRoot()
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	preferSlug := claudeProjectSlug(workspace)
	type projectHit struct {
		dir  string
		slug string
	}
	projects := make([]projectHit, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		projects = append(projects, projectHit{dir: filepath.Join(root, entry.Name()), slug: entry.Name()})
	}
	// Prefer matching workspace project first for fresher UI, still scan all.
	sort.SliceStable(projects, func(i, j int) bool {
		iMatch := preferSlug != "" && projects[i].slug == preferSlug
		jMatch := preferSlug != "" && projects[j].slug == preferSlug
		if iMatch != jMatch {
			return iMatch
		}
		return projects[i].slug < projects[j].slug
	})

	result := make([]claudeNativeSession, 0, 64)
	const maxSessions = 400
	for _, project := range projects {
		if len(result) >= maxSessions {
			break
		}
		files, err := os.ReadDir(project.dir)
		if err != nil {
			continue
		}
		for _, file := range files {
			if len(result) >= maxSessions {
				break
			}
			if file.IsDir() || !strings.HasSuffix(strings.ToLower(file.Name()), ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
			if id == "" {
				continue
			}
			path := filepath.Join(project.dir, file.Name())
			info, err := file.Info()
			if err != nil {
				continue
			}
			summary := peekClaudeNativeSummary(path, id, info.ModTime())
			// Prefer real cwd from transcript; for the active project dir fall back to
			// the requested workspace so the sidebar groups like Codex/Grok.
			if summary.Workspace == "" && preferSlug != "" && project.slug == preferSlug && strings.TrimSpace(workspace) != "" {
				summary.Workspace = workspace
			}
			// Never use the encoded slug (D--2024Dev-...) as workspace — it breaks grouping.
			if summary.Workspace == "" {
				continue
			}
			result = append(result, claudeNativeSession{Summary: summary, Path: path})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Summary.UpdatedAt > result[j].Summary.UpdatedAt
	})
	return result
}

func findClaudeNativeSession(sessionID string) (claudeNativeSession, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return claudeNativeSession{}, false
	}
	root := claudeProjectsRoot()
	if root == "" {
		return claudeNativeSession{}, false
	}
	// Fast path: walk project dirs looking for <id>.jsonl
	var found claudeNativeSession
	ok := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if !strings.EqualFold(entry.Name(), sessionID+".jsonl") {
			return nil
		}
		info, infoErr := entry.Info()
		mod := time.Now()
		if infoErr == nil {
			mod = info.ModTime()
		}
		summary := peekClaudeNativeSummary(path, sessionID, mod)
		found = claudeNativeSession{Summary: summary, Path: path}
		ok = true
		return filepath.SkipAll
	})
	return found, ok
}

// peekClaudeNativeSummary reads only the first portion of a transcript for list rows.
func peekClaudeNativeSummary(path, id string, modTime time.Time) ClaudeSessionSummary {
	summary := ClaudeSessionSummary{
		ID:        id,
		Name:      "Claude session",
		UpdatedAt: modTime.Unix(),
		CreatedAt: modTime.Unix(),
	}
	file, err := os.Open(path)
	if err != nil {
		return summary
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	// Read up to ~80 lines for title/cwd/model — enough for early metadata.
	const maxLines = 80
	lineNo := 0
	var firstUser string
	var title string
	for scanner.Scan() {
		lineNo++
		if lineNo > maxLines {
			break
		}
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		eventType := strings.ToLower(firstMapString(raw, "type"))
		if cwd := firstMapString(raw, "cwd"); cwd != "" && summary.Workspace == "" {
			// Normalize so D:\a\b and D:/a/b group together in the sidebar.
			summary.Workspace = filepath.Clean(cwd)
		}
		if sessionID := firstMapString(raw, "sessionId", "session_id"); sessionID != "" {
			summary.ID = sessionID
		}
		if ts := parseClaudeTimestamp(raw["timestamp"]); ts > 0 && (summary.CreatedAt == modTime.Unix() || ts < summary.CreatedAt) {
			summary.CreatedAt = ts
		}
		switch eventType {
		case "ai-title", "title":
			if t := firstMapString(raw, "title", "name", "content"); t != "" {
				title = t
			}
		case "user":
			if firstUser != "" {
				continue
			}
			text := claudeMessageText(raw)
			if text == "" {
				continue
			}
			// Skip huge system dumps.
			if len(text) > 4000 && strings.Contains(text, "CLAUDE.md") {
				continue
			}
			firstUser = text
			if summary.Model == "" {
				if msg, ok := raw["message"].(map[string]any); ok {
					summary.Model = firstMapString(msg, "model")
				}
			}
		case "assistant":
			if msg, ok := raw["message"].(map[string]any); ok {
				if model := firstMapString(msg, "model"); model != "" {
					summary.Model = model
				}
			}
		}
	}
	if title != "" {
		summary.Name = truncateRunes(title, 56)
	} else if firstUser != "" {
		summary.Name = truncateRunes(firstUser, 56)
		summary.Preview = truncateRunes(firstUser, 120)
	}
	if summary.Preview == "" && firstUser != "" {
		summary.Preview = truncateRunes(firstUser, 120)
	}
	return summary
}

func readClaudeNativeMessages(path string) ([]ClaudeMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	messages := make([]ClaudeMessage, 0, 64)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	const maxMessages = 800
	for scanner.Scan() {
		if len(messages) >= maxMessages {
			break
		}
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		eventType := strings.ToLower(firstMapString(raw, "type"))
		// Skip sidechains / queue noise.
		if isTruthy(raw["isSidechain"]) {
			continue
		}
		created := parseClaudeTimestamp(raw["timestamp"])
		id := firstMapString(raw, "uuid", "id")
		switch eventType {
		case "user":
			// Skip non-human synthetic prompts when possible.
			if origin, ok := raw["origin"].(map[string]any); ok {
				kind := strings.ToLower(firstMapString(origin, "kind"))
				if kind != "" && kind != "human" && kind != "user" {
					continue
				}
			}
			text := claudeMessageText(raw)
			if text == "" || isClaudeNoiseUserText(text) {
				continue
			}
			messages = append(messages, ClaudeMessage{
				ID: id, Role: "user", Text: text, Status: "completed", CreatedAt: created,
			})
		case "assistant":
			msg, _ := raw["message"].(map[string]any)
			// Prefer visible text; include thinking as separate reasoning row when present.
			thinking := claudeThinkingText(msg)
			if thinking != "" {
				messages = append(messages, ClaudeMessage{
					ID: id + ":thinking", Role: "reasoning", Text: thinking, Status: "completed", CreatedAt: created,
				})
			}
			text := claudeAssistantText(msg)
			if text == "" && thinking == "" {
				// Tool-only assistant turn — surface tool names.
				if tools := claudeToolNames(msg); tools != "" {
					messages = append(messages, ClaudeMessage{
						ID: id, Role: "assistant", Text: tools, ToolName: "tools", Status: "completed", CreatedAt: created,
					})
				}
				continue
			}
			if text != "" {
				messages = append(messages, ClaudeMessage{
					ID: id, Role: "assistant", Text: text, Status: "completed", CreatedAt: created,
				})
			}
		}
	}
	return messages, scanner.Err()
}

// readClaudeCurrentTurnActivity preserves assistant/reasoning/tool order after
// the latest human prompt. Tool-result user blocks update their tool row in place
// and are never treated as new conversation turns.
func readClaudeCurrentTurnActivity(path, prompt, turnID string) ([]ClaudeMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	activity := make([]ClaudeMessage, 0, 16)
	pending := make(map[string][]int)
	occurrences := make(map[string]int)
	_ = prompt
	insideTurn := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil || isTruthy(raw["isSidechain"]) {
			continue
		}
		eventType := strings.ToLower(firstMapString(raw, "type"))
		message, _ := raw["message"].(map[string]any)
		blocks, _ := message["content"].([]any)

		if eventType == "user" {
			hasToolResult := false
			for _, item := range blocks {
				block, ok := item.(map[string]any)
				if !ok || !strings.EqualFold(firstMapString(block, "type"), "tool_result") {
					continue
				}
				hasToolResult = true
				if !insideTurn {
					continue
				}
				callID := firstMapString(block, "tool_use_id", "toolUseId", "id")
				queue := pending[callID]
				if len(queue) == 0 {
					continue
				}
				index := queue[0]
				pending[callID] = queue[1:]
				if index < 0 || index >= len(activity) {
					continue
				}
				output := strings.TrimSpace(textFromExternalValue(block["content"]))
				if output == "" {
					output = strings.TrimSpace(stringifyClaudeToolValue(block["content"]))
				}
				status := "completed"
				if isTruthy(block["is_error"]) {
					status = "failed"
				}
				activity[index].Text = output
				activity[index].Status = status
				if created := parseClaudeTimestamp(raw["timestamp"]); created > 0 {
					activity[index].CreatedAt = created
				}
			}
			if hasToolResult {
				continue
			}
			text := strings.TrimSpace(claudeMessageText(raw))
			if text == "" || isClaudeNoiseUserText(text) {
				continue
			}
			insideTurn = true
			activity = activity[:0]
			pending = make(map[string][]int)
			occurrences = make(map[string]int)
			continue
		}

		if eventType != "assistant" || !insideTurn {
			continue
		}
		created := parseClaudeTimestamp(raw["timestamp"])
		rawID := firstMapString(raw, "uuid", "id")
		if len(blocks) == 0 {
			if text := claudeAssistantText(message); text != "" {
				activity = append(activity, ClaudeMessage{
					ID:        claudeActivityMessageID(turnID, rawID, 0, "text"),
					Role:      "assistant",
					Text:      text,
					Status:    "completed",
					CreatedAt: created,
				})
			}
			continue
		}
		for blockIndex, item := range blocks {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			blockType := strings.ToLower(firstMapString(block, "type"))
			switch blockType {
			case "text", "":
				text := strings.TrimSpace(firstMapString(block, "text", "content"))
				if text != "" {
					activity = append(activity, ClaudeMessage{
						ID:        claudeActivityMessageID(turnID, rawID, blockIndex, "text"),
						Role:      "assistant",
						Text:      text,
						Status:    "completed",
						CreatedAt: created,
					})
				}
				continue
			case "thinking", "reasoning":
				text := strings.TrimSpace(firstMapString(block, "thinking", "reasoning", "text"))
				if text != "" {
					activity = append(activity, ClaudeMessage{
						ID:        claudeActivityMessageID(turnID, rawID, blockIndex, "reasoning"),
						Role:      "reasoning",
						Text:      text,
						Status:    "completed",
						CreatedAt: created,
					})
				}
				continue
			case "tool_use":
				// handled below
			default:
				continue
			}
			name := strings.TrimSpace(firstMapString(block, "name"))
			// Claude proxy streams may emit an unnamed internal companion block for
			// every real tool call. It has no user-facing identity, so do not duplicate it.
			if name == "" {
				continue
			}
			callID := strings.TrimSpace(firstMapString(block, "id", "tool_use_id", "toolUseId"))
			if callID == "" {
				callID = fmt.Sprintf("tool-%d", len(activity)+1)
			}
			occurrences[callID]++
			stableID := strings.ReplaceAll(callID, ":", "-")
			if occurrences[callID] > 1 {
				stableID = fmt.Sprintf("%s-%d", stableID, occurrences[callID])
			}
			input := strings.TrimSpace(stringifyClaudeToolValue(block["input"]))
			activity = append(activity, ClaudeMessage{
				ID:        turnID + ":tool-" + stableID,
				Role:      "tool",
				Text:      input,
				ToolName:  name,
				Status:    "inProgress",
				CreatedAt: created,
			})
			pending[callID] = append(pending[callID], len(activity)-1)
		}
	}
	return activity, scanner.Err()
}

func claudeActivityMessageID(turnID, rawID string, blockIndex int, kind string) string {
	stable := strings.TrimSpace(rawID)
	if stable == "" {
		stable = fmt.Sprintf("row-%d", blockIndex+1)
	}
	stable = strings.NewReplacer(":", "-", "/", "-", "\\", "-").Replace(stable)
	return fmt.Sprintf("%s:%s-%s-%d", turnID, kind, stable, blockIndex)
}

func stringifyClaudeToolValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(payload)
}

func claudeMessageText(raw map[string]any) string {
	if msg, ok := raw["message"].(map[string]any); ok {
		if text := strings.TrimSpace(textFromExternalValue(msg["content"])); text != "" {
			return text
		}
		if text := firstMapString(msg, "text", "content"); text != "" {
			return text
		}
	}
	if text := firstMapString(raw, "content", "text"); text != "" {
		return text
	}
	return strings.TrimSpace(textFromExternalValue(raw["content"]))
}

func claudeAssistantText(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	if text := textFromClaudeContentBlocks(msg["content"], false); text != "" {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(textFromExternalValue(msg["content"]))
}

func claudeThinkingText(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	return strings.TrimSpace(textFromClaudeContentBlocks(msg["content"], true))
}

func claudeToolNames(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	items, ok := msg["content"].([]any)
	if !ok {
		return ""
	}
	names := make([]string, 0, 4)
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(firstMapString(block, "type")) != "tool_use" {
			continue
		}
		name := firstMapString(block, "name")
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "Used tools: " + strings.Join(names, ", ")
}

func isClaudeNoiseUserText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	// Meta / skill dumps that show up as user-role rows in transcripts.
	if strings.HasPrefix(lower, "<command-") || strings.HasPrefix(lower, "<local-command") {
		return true
	}
	if strings.Contains(lower, "skill_listing") || strings.Contains(lower, "agent_listing") {
		return true
	}
	return false
}

func isTruthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true" || typed == "1"
	case float64:
		return typed != 0
	default:
		return false
	}
}

func parseClaudeTimestamp(value any) int64 {
	switch typed := value.(type) {
	case float64:
		// ms vs seconds
		if typed > 1e12 {
			return int64(typed / 1000)
		}
		if typed > 1e9 {
			return int64(typed)
		}
		return int64(typed)
	case json.Number:
		n, _ := typed.Float64()
		return parseClaudeTimestamp(n)
	case string:
		if typed == "" {
			return 0
		}
		if n, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return n.Unix()
		}
		if n, err := time.Parse(time.RFC3339, typed); err == nil {
			return n.Unix()
		}
	}
	return 0
}

// sumClaudeNativeUsage walks a transcript and aggregates assistant message.usage blocks.
func sumClaudeNativeUsage(path string) (tokenBreakdown, int) {
	file, err := os.Open(path)
	if err != nil {
		return tokenBreakdown{}, 0
	}
	defer file.Close()
	var total tokenBreakdown
	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		if strings.ToLower(firstMapString(raw, "type")) != "assistant" {
			continue
		}
		msg, _ := raw["message"].(map[string]any)
		if msg == nil {
			continue
		}
		usage := normalizeTokenUsageMap(msg["usage"])
		if usage == nil {
			continue
		}
		b := breakdownFromUsageMap(usage)
		if !b.valid() {
			continue
		}
		total.Input += b.Input
		total.Cached += b.Cached
		total.Output += b.Output
		total.Reasoning += b.Reasoning
		total.Total += b.Total
		count++
	}
	return total, count
}

// collectClaudeNativeTurnUsage returns per-assistant-message usage hits for backfill.
func collectClaudeNativeTurnUsage(path, sessionID string) []claudeTurnUsageHit {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	hits := make([]claudeTurnUsageHit, 0, 16)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	index := 0
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		if strings.ToLower(firstMapString(raw, "type")) != "assistant" {
			continue
		}
		msg, _ := raw["message"].(map[string]any)
		if msg == nil {
			continue
		}
		usage := normalizeTokenUsageMap(msg["usage"])
		if usage == nil {
			continue
		}
		b := breakdownFromUsageMap(usage)
		if !b.valid() {
			continue
		}
		index++
		turnID := firstMapString(raw, "uuid", "id")
		if turnID == "" {
			turnID = firstMapString(msg, "id")
		}
		if turnID == "" {
			turnID = "claude-turn-" + itoa(index)
		}
		at := time.Unix(parseClaudeTimestamp(raw["timestamp"]), 0)
		if at.IsZero() || at.Unix() <= 0 {
			at = time.Now()
		}
		hits = append(hits, claudeTurnUsageHit{
			SessionID: sessionID,
			TurnID:    turnID,
			Breakdown: b,
			At:        at,
		})
	}
	return hits
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
