//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Smoke-checks the same Bootstrap / ListModelProviders contracts the UI uses,
// without needing the Wails window or WebView bindings.
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--local-files" {
		if err := verifyLocalFiles(); err != nil {
			fail("local files: %v", err)
		}
		return
	}
	service := &AppService{
		settings:         defaultSettings(),
		settingsPath:     resolveSettingsPath(),
		allowedThreads:   map[string]string{},
		allowedImages:    map[string]struct{}{},
		terminalSessions: map[string]*terminalSession{},
		sessions:         map[string]*SessionRecord{},
		externalRuns:     map[string]*externalRun{},
	}
	if loaded, err := readSettings(service.settingsPath); err == nil {
		service.settings = loaded
	}
	service.sessions = service.loadSessions()

	boot := service.Bootstrap()
	providers, err := service.ListModelProviders()
	if err != nil {
		fail("ListModelProviders: %v", err)
	}

	problems := []string{}
	if !boot.Codex.Available {
		problems = append(problems, "Codex CLI not available")
	}
	if len(boot.AgentProviders) < 1 || boot.AgentProviders[0].Kind != "codex" {
		problems = append(problems, fmt.Sprintf("Bootstrap agentProviders=%d want Codex first", len(boot.AgentProviders)))
	}

	data, _ := providers["data"].([]any)
	if len(data) != 1 {
		problems = append(problems, fmt.Sprintf("ListModelProviders count=%d want 1", len(data)))
	}
	names := make([]string, 0, 1)
	for _, item := range data {
		record, _ := item.(map[string]any)
		name, _ := record["name"].(string)
		kind, _ := record["kind"].(string)
		configured, _ := record["configured"].(bool)
		names = append(names, name)
		if name != "Codex" || kind != "codex" {
			problems = append(problems, "unexpected provider: "+name+"/"+kind)
		}
		if !configured {
			problems = append(problems, kind+" not configured/ready")
		}
	}
	if strings.Join(names, ",") != "Codex" {
		problems = append(problems, "provider labels="+strings.Join(names, ","))
	}

	// Codex-only: workbench modelProvider must be empty.
	mp := strings.TrimSpace(boot.Settings.ModelProvider)
	if mp != "" {
		problems = append(problems, "settings.modelProvider should be empty for Codex-only, got="+mp)
	}

	result := map[string]any{
		"ok":             len(problems) == 0,
		"problems":       problems,
		"providers":      names,
		"workspace":      boot.Settings.Workspace,
		"modelProvider":  boot.Settings.ModelProvider,
		"model":          boot.Settings.Model,
		"sessions":       len(service.sessions),
		"agentProviders": summarizeAgents(boot.AgentProviders),
	}
	home, _ := os.UserHomeDir()
	codexHits := scanCodexRolloutTokenUsage(resolveCodexHome())
	codexUsage := tokenBreakdown{}
	for _, hit := range codexHits {
		codexUsage.Input += hit.Breakdown.Input
		codexUsage.Cached += hit.Breakdown.Cached
		codexUsage.Output += hit.Breakdown.Output
		codexUsage.Reasoning += hit.Breakdown.Reasoning
		codexUsage.Total += hit.Breakdown.Total
	}
	result["usageProbe"] = map[string]any{
		"codex":    codexUsage,
		"gemini":   collectGeminiUsage(resolveGeminiHome(), ""),
		"opencode": collectOpenCodeUsage(home, "", 0),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
	if len(problems) > 0 {
		os.Exit(1)
	}
}

func summarizeAgents(providers []AgentProviderRuntime) []map[string]any {
	out := make([]map[string]any, 0, len(providers))
	for _, provider := range providers {
		models := make([]string, 0, len(provider.Models))
		for _, model := range provider.Models {
			label := model.DisplayName
			if label == "" {
				label = model.Model
			}
			models = append(models, label)
		}
		out = append(out, map[string]any{
			"kind": provider.Kind, "name": provider.Name, "ready": provider.RuntimeReady,
			"status": provider.Status, "models": models,
		})
	}
	return out
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

// Isolated verification for app-owned files and instruction editing. Use the
// platform's GoFiles from `go list`, replace main.go with this existing script,
// then run with --local-files. No Wails window or native CLI is started.
func verifyLocalFiles() error {
	root, err := os.MkdirTemp("", "nicecodex-local-files-")
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(root)
	if err != nil || !samePath(filepath.Dir(absolute), os.TempDir()) || !strings.HasPrefix(filepath.Base(absolute), "nicecodex-local-files-") {
		return fmt.Errorf("unexpected verification directory: %s", root)
	}
	defer os.RemoveAll(absolute)
	checks := []string{}
	skipped := []string{}
	write := func(path, content string) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0o600)
	}
	expectFile := func(path, content string) error {
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(payload) != content {
			return fmt.Errorf("unexpected contents in %s", filepath.Base(path))
		}
		return nil
	}
	project := filepath.Join(root, "project")
	agents := filepath.Join(project, "AGENTS.md")
	source := agentsInstructionSource(project, false)
	original := strings.Repeat("中文🙂", 6000) + "\r\n  keep whitespace  \n"
	if err := write(agents, original); err != nil {
		return err
	}
	loaded := source.read()
	if !loaded.Available || loaded.Content != original {
		return fmt.Errorf("large UTF-8 instructions were not read intact")
	}
	saved, err := source.save(InstructionsSaveRequest{Content: original + "更新", Revision: loaded.Revision})
	if err != nil {
		return err
	}
	if err := expectFile(agents, original+"更新"); err != nil {
		return err
	}
	checks = append(checks, "UTF-8 over 16 KB and whitespace round trip")
	if err := write(agents, "external edit"); err != nil {
		return err
	}
	if _, err := source.save(InstructionsSaveRequest{Content: "stale draft", Revision: saved.Revision}); err == nil || !strings.Contains(err.Error(), "INSTRUCTIONS_CHANGED") {
		return fmt.Errorf("stale revision was not rejected: %v", err)
	}
	if err := expectFile(agents, "external edit"); err != nil {
		return err
	}
	other := agentsInstructionSource(filepath.Join(root, "other-project"), false)
	if _, err := other.save(InstructionsSaveRequest{Content: "wrong project", Revision: source.read().Revision}); err == nil {
		return fmt.Errorf("workspace switch was not rejected")
	}
	checks = append(checks, "external edit and workspace conflict protection")
	loaded = source.read()
	override := filepath.Join(project, "AGENTS.override.md")
	if err := write(override, "higher priority"); err != nil {
		return err
	}
	if _, err := source.save(InstructionsSaveRequest{Content: "stale selection", Revision: loaded.Revision}); err == nil {
		return fmt.Errorf("new override did not invalidate revision")
	}
	if _, err := source.save(InstructionsSaveRequest{Content: "", Revision: source.read().Revision}); err != nil {
		return err
	}
	if err := expectFile(override, ""); err != nil {
		return err
	}
	if err := expectFile(agents, "external edit"); err != nil {
		return err
	}
	checks = append(checks, "override selection and clearing preserve fallback file")
	claudeRoot := filepath.Join(root, "claude")
	claudePath := filepath.Join(claudeRoot, "CLAUDE.local.md")
	if err := write(claudePath, "local rules"); err != nil {
		return err
	}
	claude := claudeInstructionSource(claudeRoot, false)
	if _, err := claude.save(InstructionsSaveRequest{Content: "updated local rules", Revision: claude.read().Revision}); err != nil {
		return err
	}
	if err := expectFile(claudePath, "updated local rules"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(claudeRoot, "CLAUDE.md")); !os.IsNotExist(err) {
		return fmt.Errorf("Claude save created the wrong instruction file")
	}
	checks = append(checks, "Claude local fallback is the actual write target")
	loaded = source.read()
	if _, err := source.save(InstructionsSaveRequest{Content: strings.Repeat("a", maxInstructionFileBytes+1), Revision: loaded.Revision}); err == nil {
		return fmt.Errorf("oversized save was accepted")
	}
	if err := expectFile(agents, "external edit"); err != nil {
		return err
	}
	if err := write(agents, strings.Repeat("a", maxInstructionFileBytes+1)); err != nil {
		return err
	}
	if info := source.read(); info.Available || !strings.Contains(info.ReadError, "INSTRUCTIONS_TOO_LARGE") {
		return fmt.Errorf("oversized read is editable")
	}
	if err := write(agents, string([]byte{0xff, 0xfe})); err != nil {
		return err
	}
	if info := source.read(); info.Available || !strings.Contains(info.ReadError, "INSTRUCTIONS_ENCODING") {
		return fmt.Errorf("invalid UTF-8 is editable")
	}
	checks = append(checks, "size and encoding failures preserve files")
	if err := write(agents, "before staging"); err != nil {
		return err
	}
	providerFileMu.Lock()
	err = writeProviderFileAtomicLocked(agents, []byte("staged draft"), 0o600, func() error {
		if err := os.WriteFile(agents, []byte("edit during staging"), 0o600); err != nil {
			return err
		}
		return fmt.Errorf("INSTRUCTIONS_CHANGED")
	})
	providerFileMu.Unlock()
	if err == nil {
		return fmt.Errorf("before-replace rejection was ignored")
	}
	if err := expectFile(agents, "edit during staging"); err != nil {
		return err
	}
	checks = append(checks, "pre-replacement rejection leaves external content intact")
	link := filepath.Join(root, "linked", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return err
	}
	if err := os.Symlink(agents, link); err != nil {
		skipped = append(skipped, "symlink creation unavailable on this host")
	} else {
		linked := agentsInstructionSource(filepath.Dir(link), false)
		if _, err := linked.save(InstructionsSaveRequest{Content: "linked content", Revision: linked.read().Revision}); err != nil {
			return err
		}
		stat, err := os.Lstat(link)
		if err != nil || stat.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("save replaced the symlink")
		}
		if err := expectFile(agents, "linked content"); err != nil {
			return err
		}
		if err := os.Rename(agents, agents+".nicecodex-backup"); err != nil {
			return err
		}
		if restored := linked.read(); !restored.Available || restored.Content != "linked content" {
			return fmt.Errorf("symlink target backup was not recovered")
		}
		checks = append(checks, "symlink preserved and target backup recovered")
	}
	service := &AppService{settingsPath: filepath.Join(root, "corrupt", "settings.json")}
	paths := []string{service.settingsPath, sessionsPath(service.settingsPath), claudeSessionsPath(service.settingsPath), grokAPISessionsPath(service.settingsPath), usagePath(service.settingsPath)}
	for _, path := range paths {
		if err := write(path, "{broken"); err != nil {
			return err
		}
	}
	var settings UserSettings
	_ = service.readLocalJSON("settings", service.settingsPath, &settings)
	service.sessions = service.loadSessions()
	service.claudeSessions = service.loadClaudeSessions()
	service.grokAPISessions = service.loadGrokAPISessions()
	service.usageCache = service.loadLocalUsage()
	if service.persistSettingsLocked(defaultSettings()) == nil || service.persistSessionsLocked() == nil || service.persistClaudeSessionsLocked() == nil || service.persistGrokAPISessionsLocked() == nil || service.persistLocalUsage(service.usageCache) == nil {
		return fmt.Errorf("unread local data could be overwritten")
	}
	if len(service.RetryLocalStorageWrites().Issues) != 5 {
		return fmt.Errorf("read failures were cleared by retry")
	}
	for _, path := range paths {
		if err := expectFile(path, "{broken"); err != nil {
			return err
		}
	}
	checks = append(checks, "all five unread stores refuse overwrite and retry")
	blocker := filepath.Join(root, "blocked-directory")
	if err := write(blocker, "blocker"); err != nil {
		return err
	}
	retry := &AppService{settingsPath: filepath.Join(blocker, "settings.json"), sessions: map[string]*SessionRecord{}}
	if retry.persistSessionsLocked() == nil {
		return fmt.Errorf("write failure was not returned")
	}
	if issues := retry.LocalStorageStatus().Issues; len(issues) != 1 || !issues[0].CanRetry {
		return fmt.Errorf("write failure was not exposed")
	}
	retry.sessions["new"] = &SessionRecord{ID: "new", Name: "latest in-memory state"}
	if err := os.Remove(blocker); err != nil {
		return err
	}
	if len(retry.RetryLocalStorageWrites().Issues) != 0 {
		return fmt.Errorf("retry did not recover")
	}
	payload, err := os.ReadFile(sessionsPath(retry.settingsPath))
	if err != nil || !strings.Contains(string(payload), "latest in-memory state") {
		return fmt.Errorf("retry did not save current memory")
	}
	checks = append(checks, "write failure exposed and current memory recovered")
	jsoncPath := filepath.Join(root, "native", "opencode.jsonc")
	jsoncOriginal := "{\n  // Keep this comment and the large native identifier.\n  \"nativeId\": 9007199254740993,\n  \"compaction\": {\"auto\": true, \"reserved\": 1000},\n  \"mcp\": {\"keep\": {\"type\": \"local\", \"command\": [\"node\", \"server.js\"], \"oauth\": {\"scope\": \"read\"}}}\n}\n"
	if err := write(jsoncPath, jsoncOriginal); err != nil {
		return err
	}
	if err := updateProviderJSONConfig(jsoncPath, func(config map[string]any) error {
		compaction, err := providerJSONObject(config, "compaction")
		if err != nil {
			return err
		}
		compaction["reserved"] = json.Number("2000")
		return nil
	}); err != nil {
		return err
	}
	jsoncPayload, err := os.ReadFile(jsoncPath)
	if err != nil || !strings.Contains(string(jsoncPayload), "Keep this comment") || !strings.Contains(string(jsoncPayload), "9007199254740993") || !strings.Contains(string(jsoncPayload), "\"reserved\": 2000") {
		return fmt.Errorf("JSONC patch did not preserve comments or native number: %v", err)
	}
	if err := write(jsoncPath, "{broken"); err != nil {
		return err
	}
	if err := updateProviderJSONConfig(jsoncPath, func(config map[string]any) error { config["unsafe"] = true; return nil }); err == nil {
		return fmt.Errorf("broken JSONC was treated as empty configuration")
	}
	if err := expectFile(jsoncPath, "{broken"); err != nil {
		return err
	}
	checks = append(checks, "JSONC comments, large numbers and corrupt-file protection")
	store := newScheduledTaskStore(filepath.Join(root, "scheduler", "settings.json"))
	if err := store.load(); err != nil {
		return err
	}
	task, err := store.upsert(ScheduledTask{Title: "fixture", Prompt: "fixture", Workspace: project, Enabled: true, IntervalMin: 5})
	if err != nil {
		return err
	}
	claimed, ok, err := store.claim(task.ID, "fixture-session", task.NextRunAt)
	if err != nil || !ok {
		return fmt.Errorf("task could not be claimed: %v", err)
	}
	if _, ok, err := store.claim(task.ID, "duplicate", task.NextRunAt+3600); err != nil || ok {
		return fmt.Errorf("active task could be claimed twice")
	}
	claimed.Enabled = false
	if _, err := store.upsert(claimed); err != nil {
		return err
	}
	claimed.Prompt = "changed while running"
	if _, err := store.upsert(claimed); err == nil {
		return fmt.Errorf("active task could be edited")
	}
	if err := store.delete(task.ID); err == nil {
		return fmt.Errorf("active task could be deleted")
	}
	if len(store.due(time.Now().Add(24*time.Hour).Unix())) != 0 {
		return fmt.Errorf("active task became due")
	}
	originalPath := store.path
	if err := write(filepath.Join(root, "finish-blocker"), "blocker"); err != nil {
		return err
	}
	store.path = filepath.Join(root, "finish-blocker", "tasks.json")
	if store.finish(task.ID, "fixture-session", nil) == nil || len(store.active()) != 1 {
		return fmt.Errorf("failed finish lost task ownership")
	}
	store.path = originalPath
	if err := store.finish(task.ID, "fixture-session", nil); err != nil {
		return err
	}
	if len(store.active()) != 0 {
		return fmt.Errorf("finished task still active")
	}
	checks = append(checks, "scheduled claim, mutation guards and finish failure ownership")
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": checks, "skipped": skipped})
}
