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
	if len(os.Args) == 2 && os.Args[1] == "--reload" {
		if err := verifyRuntimeReload(); err != nil {
			fail("runtime reload: %v", err)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "--fastctx-command" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[2:], "updates": os.Getenv("FASTCTX_DISABLE_UPDATE_CHECK")})
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--fastctx" {
		if err := verifyFastCtxIntegration(); err != nil {
			fail("fastctx integration: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--models" {
		if err := verifyModelConfiguration(); err != nil {
			fail("model configuration: %v", err)
		}
		return
	}
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

// Isolated contract checks: no real Codex configuration, package installation or MCP server.
func verifyFastCtxIntegration() error {
	passing := "[PASS] Applied state: matched\n[PASS] Installed binary: ready\n[PASS] MCP server contract: ready\n[PASS] AGENTS guidance: current\n[INFO] fastshell: fastshell is disabled.\n"
	cases := []struct {
		name, output, version, managed, want string
		shell                                bool
		err                                  error
	}{
		{name: "applied", output: passing, version: "0.2.6", managed: "0.2.6", want: "applied"},
		{name: "zero exit before apply", output: "[INFO] Applied state: not applied", want: "not_applied"},
		{name: "updated launcher with old managed copy", output: passing, version: "0.2.6", managed: "0.2.5", want: "needs_apply"},
		{name: "unknown future report", output: "All good", want: "needs_attention"},
		{name: "damaged guidance", output: strings.ReplaceAll(passing, "[PASS] AGENTS", "[FAIL] AGENTS"), want: "needs_attention"},
		{name: "timeout", output: passing, err: fmt.Errorf("deadline"), want: "needs_attention"},
		{name: "shell enable pending", output: passing, version: "0.2.6", managed: "0.2.6", shell: true, want: "needs_apply"},
		{name: "shell disable pending", output: strings.ReplaceAll(passing, "fastshell is disabled.", "fastshell is still applied"), version: "0.2.6", managed: "0.2.6", want: "needs_apply"},
		{name: "shell applied", output: strings.ReplaceAll(passing, "[INFO] fastshell: fastshell is disabled.", "[PASS] fastshell: enabled"), version: "0.2.6", managed: "0.2.6", shell: true, want: "applied"},
	}
	for _, item := range cases {
		if got := fastCtxApplicationState(item.output, item.err, item.version, item.managed, item.shell); got != item.want {
			return fmt.Errorf("%s: got %s, want %s", item.name, got, item.want)
		}
	}
	root, err := os.MkdirTemp("", "nicecodex-fastctx-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "config.toml")
	original := "# user preferences\ntier = \"high\"\n[fastshell] # keep this\nenabled = false # keep note\nmax_running_jobs = 8\n[search]\nmax_cpu_cores = 2\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		return err
	}
	for _, enabled := range []bool{true, true, false} {
		if err := updateProviderTextConfig(path, func(text string) (string, error) {
			return upsertTOMLBool(text, "fastshell", "enabled", enabled), nil
		}); err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if readTOMLBool(string(content), "fastshell", "enabled", !enabled) != enabled {
			return fmt.Errorf("shell preference did not persist")
		}
		for _, preserved := range []string{"# user preferences", "tier = \"high\"", "# keep note", "max_running_jobs = 8", "[search]\nmax_cpu_cores = 2"} {
			if !strings.Contains(string(content), preserved) {
				return fmt.Errorf("lost user setting: %s", preserved)
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	service := &AppService{}
	profile := filepath.Join(root, "Codex 中文 path")
	output, err := service.runFastCtxCommand(executable, []string{"--fastctx-command", "status", "--codex-home", profile}, 5*time.Second, nil)
	if err != nil {
		return err
	}
	var command struct {
		Args    []string `json:"args"`
		Updates string   `json:"updates"`
	}
	if err := json.Unmarshal([]byte(output), &command); err != nil {
		return err
	}
	if len(command.Args) != 3 || command.Args[2] != profile || command.Updates != "1" {
		return fmt.Errorf("command arguments or child environment not preserved: %s", output)
	}
	fastCtxMu.Lock()
	_, checkErr := service.CheckFastCtx(false)
	_, syncErr := service.SyncFastCtx(true, false)
	fastCtxMu.Unlock()
	if checkErr == nil || syncErr == nil {
		return fmt.Errorf("concurrent operations were not rejected")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "statusCases": len(cases), "checks": []string{"atomic shell preferences preserve user settings", "Unicode/space profile argument and child environment", "concurrent operation guards"}})
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

func verifyRuntimeReload() error {
	root, err := os.MkdirTemp("", "nicecodex-reload-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	for key, value := range map[string]string{"CLAUDE_CONFIG_DIR": root, "ANTHROPIC_MODEL": ""} {
		previous, existed := os.LookupEnv(key)
		defer func() {
			if existed {
				_ = os.Setenv(key, previous)
			} else {
				_ = os.Unsetenv(key)
			}
		}()
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	path := filepath.Join(root, "settings.json")
	for _, item := range []struct{ config, model string }{
		{`{"model":"gateway-model","env":{"ANTHROPIC_API_KEY":"fixture"}}`, "gateway-model"},
		{`{}`, "default"},
		{`{"model":"claude-sonnet-5-5"}`, "claude-sonnet-5-5"},
		{`{"model":"claude-sonnet-5-5","env":{"ANTHROPIC_MODEL":"claude-opus-5-5"}}`, "claude-opus-5-5"},
	} {
		if err := os.WriteFile(path, []byte(item.config), 0o600); err != nil {
			return err
		}
		got, err := claudeModelForReload("")
		if err != nil || got != item.model {
			return fmt.Errorf("native model reload: got %s, want %s: %v", got, item.model, err)
		}
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		return err
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".claude"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(workspace, ".claude", "settings.local.json"), []byte(`{"model":"claude-opus-5-5"}`), 0o600); err != nil {
		return err
	}
	if got, err := claudeModelForReload(workspace); err != nil || got != "claude-opus-5-5" {
		return fmt.Errorf("project model precedence: %s %v", got, err)
	}
	seen := map[string]bool{}
	for _, model := range discoverClaudeModels(root) {
		if seen[model.Model] {
			return fmt.Errorf("duplicate model: %s", model.Model)
		}
		seen[model.Model] = true
		if model.Model == "claude-opus-5-5" && (model.DisplayName != "Claude Opus 5.5" || model.ContextWindow != 1_000_000) {
			return fmt.Errorf("incorrect Opus metadata")
		}
		if model.Model == "claude-sonnet-5-5" && (model.DisplayName != "Claude Sonnet 5.5" || model.ContextWindow != 1_000_000) {
			return fmt.Errorf("incorrect Sonnet metadata")
		}
	}
	for _, model := range []string{"default", "sonnet", "opus", "claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1"} {
		if !seen[model] {
			return fmt.Errorf("missing model: %s", model)
		}
	}
	for _, provider := range []string{"codex", "claude", "grok", "gemini", "opencode"} {
		service := &AppService{providerReloading: map[string]bool{provider: true}}
		if _, err := service.ReloadRuntimeConfiguration(provider); err == nil {
			return fmt.Errorf("duplicate reload allowed: %s", provider)
		}
		service.providerReloading = nil
		if provider == "codex" {
			service.codexPendingDispatches = map[string]bool{"pending": true}
		} else {
			key := provider + ":active"
			service.externalRuns = map[string]*externalRun{key: {turnID: "active"}}
			service.sessions = map[string]*SessionRecord{key: {Provider: provider}}
		}
		if _, err := service.ReloadRuntimeConfiguration(provider); err == nil {
			return fmt.Errorf("active work was not protected: %s", provider)
		}
	}
	service := &AppService{providerReloading: map[string]bool{"codex": true}}
	if service.claimCodexDispatch("new-thread") {
		return fmt.Errorf("dispatch during Codex restart allowed")
	}
	if err := os.WriteFile(path, []byte(`{invalid`), 0o600); err != nil {
		return err
	}
	if _, err := service.ReloadRuntimeConfiguration("claude"); err == nil {
		return fmt.Errorf("invalid configuration accepted")
	}
	if service.providerReloading["claude"] {
		return fmt.Errorf("failed reload left provider locked")
	}
	service.settingsPath = filepath.Join(root, "app-settings.json")
	service.sessions = map[string]*SessionRecord{
		"codex-fixture":  {ID: "codex-fixture", Provider: "codex", ProviderID: "old-gateway", Model: "old-model"},
		"gemini-fixture": {ID: "gemini-fixture", Provider: "gemini", ProviderID: "__gemini__", Model: "old-model"},
	}
	provider := "openai"
	if err := service.UpdateSessionPreferences(SessionPreferencesRequest{SessionID: "codex-fixture", Model: "gpt-5.4", ModelProvider: &provider, ResetModel: true}); err != nil {
		return err
	}
	if record := service.sessions["codex-fixture"]; record.ProviderID != "openai" || record.Model != "gpt-5.4" {
		return fmt.Errorf("old gateway survived Codex reload")
	}
	if err := service.UpdateSessionPreferences(SessionPreferencesRequest{SessionID: "gemini-fixture", ModelProvider: &provider}); err == nil {
		return fmt.Errorf("external session could be converted into Codex")
	}
	if err := service.UpdateSessionPreferences(SessionPreferencesRequest{SessionID: "gemini-fixture", ResetModel: true}); err != nil {
		return err
	}
	if service.sessions["gemini-fixture"].Model != "" {
		return fmt.Errorf("native default did not clear old model")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": []string{"API model to native account default", "fresh environment and project model", "official 5.5 IDs and 1M metadata", "five-provider active/repeated reload guards", "Codex dispatch guard", "failed reload cleanup"}})
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

// Validate catalog identity and context settings with isolated configuration.
// No native CLI, network request, or user configuration is used.
func verifyModelConfiguration() error {
	root, err := os.MkdirTemp("", "nicecodex-models-")
	if err != nil {
		return err
	}
	if !samePath(filepath.Dir(root), os.TempDir()) || !strings.HasPrefix(filepath.Base(root), "nicecodex-models-") {
		return fmt.Errorf("unexpected verification directory")
	}
	defer os.RemoveAll(root)
	for key, value := range map[string]string{
		"CLAUDE_CONFIG_DIR": root, "ANTHROPIC_MODEL": "", "CLAUDE_CODE_DISABLE_1M_CONTEXT": "",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "", "ANTHROPIC_DEFAULT_OPUS_MODEL": "",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "", "ANTHROPIC_DEFAULT_FABLE_MODEL": "",
	} {
		previous, exists := os.LookupEnv(key)
		defer func() {
			if exists {
				_ = os.Setenv(key, previous)
			} else {
				_ = os.Unsetenv(key)
			}
		}()
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	fixture := `{"model":"claude-sonnet-4-6[1m]","availableModels":["claude-sonnet-4-5","claude-sonnet-4-6","claude-sonnet-4-6[1m]","gateway-claude-a","gateway-claude-b","gpt-proxy"],"modelOverrides":{"team-model":"claude-opus-4-6"}}`
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(fixture), 0o600); err != nil {
		return err
	}
	models := discoverClaudeModels(root)
	seen := map[string]bool{}
	defaults := 0
	for _, model := range models {
		if seen[model.Model] {
			return fmt.Errorf("duplicate model %q", model.Model)
		}
		seen[model.Model] = true
		if model.IsDefault {
			defaults++
			if model.Model != "claude-sonnet-4-6[1m]" {
				return fmt.Errorf("default model was collapsed to %q", model.Model)
			}
		}
	}
	for _, model := range []string{"sonnet", "opus", "claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-4-6[1m]", "gateway-claude-a", "gateway-claude-b", "gpt-proxy", "team-model"} {
		if !seen[model] {
			return fmt.Errorf("model %q disappeared from catalog", model)
		}
	}
	if defaults != 1 {
		return fmt.Errorf("expected exactly one default model")
	}
	for model, expected := range map[string]int64{"sonnet": 200_000, "opus": 200_000, "sonnet[1m]": 1_000_000, "claude-sonnet-4-6": 200_000, "claude-opus-4-6[1m]": 1_000_000, "claude-opus-4-8": 1_000_000, "claude-haiku-4-5": 200_000, "gateway-unknown": 0} {
		if got := knownProviderContextWindow("claude", model); got != expected {
			return fmt.Errorf("%s context = %d, want %d", model, got, expected)
		}
	}
	for _, model := range []string{"sonnet[1m]", "opus[1m]", "claude-opus-4-6[1m]"} {
		if len(appendClaudeCompatibilityArgs([]string{"-p"}, model)) != 1 {
			return fmt.Errorf("native variant %s entered gateway compatibility mode", model)
		}
	}
	provider := AgentProviderRuntime{Kind: "claude", Models: models}
	policy := providerContextPolicy(provider, UserSettings{ClaudeModel: "opus[1m]"})
	if policy.Tokens != 1_000_000 || policy.ThresholdConfigured {
		return fmt.Errorf("1M variant should keep native auto-compaction")
	}
	if err := os.Setenv("CLAUDE_CODE_DISABLE_1M_CONTEXT", "1"); err != nil {
		return err
	}
	policy = providerContextPolicy(provider, UserSettings{ClaudeModel: "opus[1m]"})
	if policy.Tokens != 200_000 || len(providerConfigurationView(provider, UserSettings{ClaudeModel: "opus[1m]"}).Warnings) == 0 {
		return fmt.Errorf("disabled 1M context was not exposed")
	}
	window, threshold := normalizeCodexContextSettings(1_000_000, 900_000)
	if window != 1_000_000 || threshold != 900_000 {
		return fmt.Errorf("Codex preset was changed")
	}
	config := "model = \"custom-a\"\n# keep provider settings\n[model_providers.gateway]\nname = \"gateway\"\n"
	for _, entry := range []struct {
		key   string
		value int64
	}{{"model_context_window", window}, {"model_auto_compact_token_limit", threshold}} {
		config = upsertTOMLScalar(config, "", entry.key, optionalIntegerLiteral(entry.value))
		if got, ok := readTOMLInteger(config, "", entry.key); !ok || got != entry.value {
			return fmt.Errorf("Codex preset round trip failed")
		}
		config = upsertTOMLScalar(config, "", entry.key, "")
		if _, ok := readTOMLInteger(config, "", entry.key); ok {
			return fmt.Errorf("Codex native reset failed")
		}
	}
	if !strings.Contains(config, "# keep provider settings") || !strings.Contains(config, "[model_providers.gateway]") {
		return fmt.Errorf("Codex preset altered provider config")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": []string{"distinct Claude versions and gateway IDs", "stable default model", "native 1M variants and compaction", "1M environment restriction", "Codex preset and reset preserve other settings"}})
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
