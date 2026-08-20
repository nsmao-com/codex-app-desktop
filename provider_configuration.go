package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nice_codex_desktop/internal/codex"
)

const providerConfigurationMaxBytes = 4 * 1024 * 1024

func normalizeProviderID(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex", "claude", "grok", "gemini", "opencode":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func invalidateProviderConfigurationCache(providerID string) {
	providerID = normalizeProviderID(providerID)
	providerProbeCache.Lock()
	if providerID == "" {
		providerProbeCache.entries = make(map[string]providerProbeCacheEntry)
	} else {
		delete(providerProbeCache.entries, providerID)
	}
	providerProbeCache.Unlock()
	if providerID == "" || providerID == "grok" {
		invalidateGrokRuntimeProbeCache()
	}
	if providerID == "" || providerID == "opencode" {
		openCodeCatalogCache.Lock()
		openCodeCatalogCache.expiresAt = time.Time{}
		openCodeCatalogCache.models = nil
		openCodeCatalogCache.efforts = nil
		openCodeCatalogCache.providers = nil
		openCodeCatalogCache.Unlock()
	}
}

func providerConfigPath(providerID string) string {
	home, _ := os.UserHomeDir()
	switch providerID {
	case "codex":
		return codexConfigPath()
	case "claude":
		return filepath.Join(resolveClaudeHome(), "settings.json")
	case "grok":
		return filepath.Join(resolveGrokHome(), "config.toml")
	case "gemini":
		return filepath.Join(home, ".gemini", "settings.json")
	case "opencode":
		return openCodeConfigPath(home)
	default:
		return ""
	}
}

func selectedProviderModel(settings UserSettings, providerID string) string {
	switch providerID {
	case "codex":
		return settings.Model
	case "claude":
		return settings.ClaudeModel
	case "grok":
		if normalizeGrokBackend(settings.GrokBackend) == grokBackendAPI {
			return settings.GrokAPIModel
		}
		return settings.GrokBuildModel
	case "gemini":
		return settings.GeminiModel
	case "opencode":
		return settings.OpenCodeModel
	default:
		return ""
	}
}

func selectedCatalogModel(provider AgentProviderRuntime, settings UserSettings) (AgentProviderModel, bool) {
	selected := strings.TrimSpace(selectedProviderModel(settings, provider.Kind))
	for _, model := range provider.Models {
		if selected != "" && strings.EqualFold(model.Model, selected) {
			return model, true
		}
	}
	for _, model := range provider.Models {
		if model.IsDefault {
			return model, true
		}
	}
	if len(provider.Models) > 0 {
		return provider.Models[0], true
	}
	return AgentProviderModel{}, false
}

func baseProviderContextPolicy(provider AgentProviderRuntime, settings UserSettings) ProviderContextPolicyView {
	model, found := selectedCatalogModel(provider, settings)
	isFallback := found && (strings.Contains(strings.ToLower(model.Description), "fallback") || strings.Contains(strings.ToLower(model.Description), "alias"))
	policy := ProviderContextPolicyView{
		Tokens: model.ContextWindow, Source: "unknown", TokenMode: "model-fixed",
		TokenStep: 1024, IsFallback: isFallback, CompactStrategy: "unsupported",
		ThresholdUnit: "tokens", ThresholdStep: 1024,
	}
	if model.ContextWindow > 0 {
		policy.Source = "native-catalog"
		if isFallback {
			policy.Source = "fallback"
		}
	}
	return policy
}

func providerContextPolicy(provider AgentProviderRuntime, settings UserSettings) ProviderContextPolicyView {
	policy := baseProviderContextPolicy(provider, settings)
	path := providerConfigPath(provider.Kind)

	switch provider.Kind {
	case "codex":
		text, _ := readProviderText(path)
		configuredTokens, hasTokens := readTOMLInteger(text, "", "model_context_window")
		threshold, hasThreshold := readTOMLInteger(text, "", "model_auto_compact_token_limit")
		if hasTokens && configuredTokens > 0 {
			policy.Tokens = configuredTokens
			policy.ConfiguredTokens = configuredTokens
			policy.Source = "codex-config"
			policy.IsFallback = false
		}
		policy.Writable = true
		policy.TokenMode = "native-override"
		policy.TokenMinimum = 16_384
		policy.TokenMaximum = 2_000_000
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
		policy.AutoCompactSupported = true
		policy.AutoCompactEnabled = true
		policy.ThresholdConfigurable = true
		policy.ThresholdConfigured = hasThreshold && threshold > 0
		policy.AutoCompactThreshold = threshold
		policy.ThresholdMinimum = 8_192
		policy.ThresholdMaximum = 2_000_000
		policy.Description = "Codex reads model_context_window and model_auto_compact_token_limit from config.toml; reconnect the app-server after changes."
	case "claude":
		catalogTokens := policy.Tokens
		catalogIsFallback := policy.IsFallback
		config := readProviderJSONMap(path)
		env := mapFromAny(config["env"])
		configuredTokens, hasTokens := integerFromConfig(env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
		threshold, hasThreshold := integerFromConfig(env["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"])
		if hasTokens && configuredTokens > 0 {
			policy.ConfiguredTokens = configuredTokens
			if policy.Tokens == 0 || configuredTokens < policy.Tokens {
				policy.Tokens = configuredTokens
			}
			policy.Source = "claude-settings-env"
			policy.IsFallback = false
		}
		policy.Writable = true
		policy.TokenMode = "client-limit"
		policy.TokenMinimum = 16_384
		policy.TokenMaximum = 1_000_000
		if catalogTokens > 0 && !catalogIsFallback {
			policy.TokenMaximum = catalogTokens
		}
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
		policy.AutoCompactSupported = true
		policy.AutoCompactEnabled = !truthyConfigValue(env["DISABLE_COMPACT"])
		policy.AutoCompactToggleable = true
		policy.ThresholdConfigurable = true
		policy.ThresholdConfigured = hasThreshold && threshold > 0
		policy.AutoCompactThreshold = threshold
		policy.ThresholdUnit = "percent"
		policy.ThresholdMinimum = 1
		policy.ThresholdMaximum = 99
		policy.ThresholdStep = 1
		policy.Description = "Claude model context is fixed upstream. CLAUDE_CODE_MAX_CONTEXT_TOKENS is only a local client cap and cannot expand the model window."
	case "grok":
		if normalizeGrokBackend(settings.GrokBackend) == grokBackendAPI {
			policy.Description = "Grok API context is fixed by the selected upstream model and cannot be overridden by Nice Codex."
			break
		}
		text, _ := readProviderText(path)
		threshold, hasThreshold := readTOMLInteger(text, "session", "auto_compact_threshold_percent")
		if !hasThreshold || threshold <= 0 {
			threshold = 85
		}
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
		policy.AutoCompactSupported = true
		policy.AutoCompactEnabled = true
		policy.ThresholdConfigurable = true
		policy.ThresholdConfigured = hasThreshold
		policy.AutoCompactThreshold = threshold
		policy.ThresholdUnit = "percent"
		policy.ThresholdMinimum = 1
		policy.ThresholdMaximum = 99
		policy.ThresholdStep = 1
		selected := strings.TrimSpace(selectedProviderModel(settings, "grok"))
		if section := findGrokModelSection(text, selected); section != "" {
			policy.Writable = true
			policy.TokenMode = "calculation-limit"
			policy.TokenMinimum = 16_384
			policy.TokenMaximum = 2_000_000
			if configured, ok := readTOMLInteger(text, section, "context_window"); ok && configured > 0 {
				policy.Tokens = configured
				policy.ConfiguredTokens = configured
				policy.Source = "grok-custom-model"
				policy.IsFallback = false
			}
		}
		policy.Description = "Grok Build uses the percentage for native auto-compaction. A custom model context_window only controls compaction calculations; it does not expand the upstream model."
	case "gemini":
		config := readProviderJSONMap(path)
		model := mapFromAny(config["model"])
		threshold := int64(50)
		hasThreshold := false
		if value, ok := floatFromConfig(model["compressionThreshold"]); ok && value > 0 {
			threshold = int64(value*100 + 0.5)
			hasThreshold = true
		}
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
		policy.AutoCompactSupported = true
		policy.AutoCompactEnabled = true
		policy.ThresholdConfigurable = true
		policy.ThresholdConfigured = hasThreshold
		policy.AutoCompactThreshold = threshold
		policy.ThresholdUnit = "percent"
		policy.ThresholdMinimum = 1
		policy.ThresholdMaximum = 99
		policy.ThresholdStep = 1
		policy.Description = "Gemini CLI fixes the context window per model. model.compressionThreshold controls native compression and is loaded by the next CLI process."
	case "opencode":
		config := readProviderJSONMap(path)
		compaction := mapFromAny(config["compaction"])
		autoCompact, hasAuto := boolFromConfig(compaction["auto"])
		if !hasAuto {
			autoCompact = true
		}
		prune, _ := boolFromConfig(compaction["prune"])
		reserved, hasReserved := integerFromConfig(compaction["reserved"])
		if !hasReserved || reserved < 0 {
			reserved = 10_000
		}
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
		policy.AutoCompactSupported = true
		policy.AutoCompactEnabled = autoCompact
		policy.AutoCompactToggleable = true
		policy.ThresholdConfigurable = true
		policy.ThresholdConfigured = hasReserved
		policy.AutoCompactThreshold = reserved
		policy.ThresholdUnit = "reserved-tokens"
		policy.ThresholdMinimum = 1_024
		policy.ThresholdMaximum = 1_000_000
		policy.PruneSupported = true
		policy.PruneEnabled = prune
		selected := strings.TrimSpace(selectedProviderModel(settings, "opencode"))
		if modelConfig := openCodeModelConfig(config, selected); modelConfig != nil {
			policy.Writable = true
			policy.TokenMode = "calculation-limit"
			policy.TokenMinimum = 16_384
			policy.TokenMaximum = 2_000_000
			limit := mapFromAny(modelConfig["limit"])
			if configured, ok := integerFromConfig(limit["context"]); ok && configured > 0 {
				policy.Tokens = configured
				policy.ConfiguredTokens = configured
				policy.Source = "opencode-provider-model"
				policy.IsFallback = false
			}
		}
		if policy.Tokens > policy.ThresholdMinimum+1_024 {
			policy.ThresholdMaximum = policy.Tokens - 1_024
		}
		policy.Description = "OpenCode compaction.auto, compaction.prune, and reserved tokens are native settings. Model limit.context is editable only for models already declared in opencode.json."
	}
	return policy
}

func providerConfigurationView(provider AgentProviderRuntime, settings UserSettings) ProviderConfigurationView {
	view := ProviderConfigurationView{
		Runtime: provider, ConfigPath: providerConfigPath(provider.Kind),
		ModelSource: "runtime-catalog", ApplyLevel: "next-turn",
		CanReload: true, CanRestart: false, SupportsModel: len(provider.Models) > 0,
		SupportsEffort: len(provider.ReasoningEfforts) > 0,
		Context:        providerContextPolicy(provider, settings), Warnings: []string{},
	}
	switch provider.Kind {
	case "codex":
		view.ApplyLevel = "reconnect"
		view.CanRestart = true
		view.PermissionModes = []string{"on-request", "never"}
	case "claude":
		view.PermissionModes = []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}
	case "grok":
		if normalizeGrokBackend(settings.GrokBackend) == grokBackendAPI {
			view.ApplyLevel = "immediate"
			view.PermissionModes = []string{}
		} else {
			view.PermissionModes = []string{"default", "bypassPermissions", "plan"}
		}
	case "gemini":
		view.SupportsEffort = false
		view.PermissionModes = []string{"default", "plan", "yolo"}
		view.Warnings = append(view.Warnings, "Gemini CLI does not expose a stable reasoning-effort flag; Nice Codex does not send one.")
	case "opencode":
		view.PermissionModes = []string{"auto"}
		view.Warnings = append(view.Warnings, "OpenCode permission controls are limited to its native --auto mapping.")
	}
	if provider.Kind != "codex" {
		view.Warnings = append(view.Warnings, "This CLI starts a fresh process for each turn, so saved configuration is read on the next turn; there is no persistent process to restart.")
	}
	return view
}

func validateProviderConfigurationFile(providerID string) error {
	path := providerConfigPath(providerID)
	if strings.TrimSpace(path) == "" {
		return errors.New("provider configuration path is unavailable")
	}
	payload, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(payload) > providerConfigurationMaxBytes {
		return errors.New("provider configuration file is too large")
	}
	if providerID == "claude" || providerID == "gemini" || providerID == "opencode" {
		var root map[string]any
		if err := json.Unmarshal(payload, &root); err != nil {
			return fmt.Errorf("invalid %s JSON configuration: %w", providerID, err)
		}
	}
	return nil
}

func (s *AppService) CheckProviderConfiguration(providerID string, force bool) (ProviderConfigurationView, error) {
	providerID = normalizeProviderID(providerID)
	if providerID == "" {
		return ProviderConfigurationView{}, errors.New("unsupported provider")
	}
	if err := validateProviderConfigurationFile(providerID); err != nil {
		return ProviderConfigurationView{}, err
	}
	if force {
		invalidateProviderConfigurationCache(providerID)
	}
	providers := detectAgentProviders(codex.Detect())
	var selected AgentProviderRuntime
	found := false
	for _, provider := range providers {
		if provider.Kind == providerID {
			selected = provider
			found = true
			break
		}
	}
	if !found {
		return ProviderConfigurationView{}, errors.New("provider configuration was not found")
	}
	s.mu.Lock()
	for index := range s.agentProviders {
		if s.agentProviders[index].Kind == providerID {
			s.agentProviders[index] = selected
		}
	}
	s.mu.Unlock()
	return providerConfigurationView(selected, s.Settings()), nil
}

func (s *AppService) ReloadProviderConfiguration(providerID string) (ProviderApplyResult, error) {
	providerID = normalizeProviderID(providerID)
	if providerID == "" {
		return ProviderApplyResult{}, errors.New("unsupported provider")
	}
	invalidateProviderConfigurationCache(providerID)
	configuration, err := s.CheckProviderConfiguration(providerID, true)
	if err != nil {
		return ProviderApplyResult{}, err
	}
	return ProviderApplyResult{
		ProviderID: providerID, ApplyLevel: configuration.ApplyLevel,
		RestartRequired: false, Warnings: configuration.Warnings,
		Configuration: configuration,
	}, nil
}

func (s *AppService) providerHasActiveWork(providerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if providerID == "codex" {
		return len(s.codexActiveTurns) > 0 || len(s.codexPendingDispatches) > 0
	}
	for key, run := range s.externalRuns {
		if run == nil {
			continue
		}
		if providerID == "claude" && strings.HasPrefix(key, "claude:") {
			return true
		}
		if providerID == "grok" && strings.HasPrefix(key, "grok:") {
			return true
		}
		if session := s.sessions[key]; session != nil && normalizeExternalRuntime(session.Provider) == providerID {
			return true
		}
	}
	return false
}

func (s *AppService) UpdateProviderContextPolicy(providerID string, tokens, threshold int64, autoCompactEnabled, pruneEnabled bool) (ProviderApplyResult, error) {
	providerID = normalizeProviderID(providerID)
	if providerID == "" {
		return ProviderApplyResult{}, errors.New("unsupported provider")
	}
	current, err := s.CheckProviderConfiguration(providerID, false)
	if err != nil {
		return ProviderApplyResult{}, err
	}
	if !current.Context.Writable {
		tokens = 0
	}
	if tokens < 0 || threshold < 0 {
		return ProviderApplyResult{}, errors.New("context values cannot be negative")
	}
	path := current.ConfigPath

	switch providerID {
	case "codex":
		tokens, threshold = normalizeCodexContextSettings(tokens, threshold)
		if tokens == 0 && threshold > 0 {
			if model, ok := selectedCatalogModel(current.Runtime, s.Settings()); ok && model.ContextWindow > 0 && threshold >= model.ContextWindow {
				threshold = model.ContextWindow - 1_024
			}
		}
		text, err := readProviderText(path)
		if err != nil {
			return ProviderApplyResult{}, err
		}
		text = upsertTOMLScalar(text, "", "model_context_window", optionalIntegerLiteral(tokens))
		text = upsertTOMLScalar(text, "", "model_auto_compact_token_limit", optionalIntegerLiteral(threshold))
		if err := writeTextFileAtomic(path, text); err != nil {
			return ProviderApplyResult{}, err
		}
	case "claude":
		if tokens > 0 && tokens < 16_384 {
			return ProviderApplyResult{}, errors.New("Claude client context cap must be at least 16384 tokens")
		}
		if tokens > 0 && current.Context.TokenMaximum > 0 && tokens > current.Context.TokenMaximum {
			return ProviderApplyResult{}, errors.New("Claude client context cap cannot exceed the selected model context window")
		}
		if threshold > 0 && (threshold < 1 || threshold > 99) {
			return ProviderApplyResult{}, errors.New("Claude auto-compact percentage must be between 1 and 99")
		}
		config := readProviderJSONMap(path)
		env := ensureMap(config, "env")
		setOptionalStringInteger(env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS", tokens)
		setOptionalStringInteger(env, "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", threshold)
		if autoCompactEnabled {
			delete(env, "DISABLE_COMPACT")
		} else {
			env["DISABLE_COMPACT"] = "1"
		}
		if err := writeProviderJSONMap(path, config); err != nil {
			return ProviderApplyResult{}, err
		}
	case "grok":
		if normalizeGrokBackend(s.Settings().GrokBackend) == grokBackendAPI {
			return ProviderApplyResult{}, errors.New("Grok API context and compaction are controlled by the upstream model")
		}
		if threshold < 1 || threshold > 99 {
			return ProviderApplyResult{}, errors.New("Grok auto-compact percentage must be between 1 and 99")
		}
		text, err := readProviderText(path)
		if err != nil {
			return ProviderApplyResult{}, err
		}
		text = upsertTOMLScalar(text, "session", "auto_compact_threshold_percent", strconv.FormatInt(threshold, 10))
		if current.Context.Writable {
			section := findGrokModelSection(text, selectedProviderModel(s.Settings(), "grok"))
			if section == "" {
				return ProviderApplyResult{}, errors.New("selected Grok model is not declared as a custom model")
			}
			if tokens > 0 && tokens < 16_384 {
				return ProviderApplyResult{}, errors.New("Grok custom model context must be at least 16384 tokens")
			}
			text = upsertTOMLScalar(text, section, "context_window", optionalIntegerLiteral(tokens))
		}
		if err := writeTextFileAtomic(path, text); err != nil {
			return ProviderApplyResult{}, err
		}
	case "gemini":
		if threshold < 1 || threshold > 99 {
			return ProviderApplyResult{}, errors.New("Gemini compression percentage must be between 1 and 99")
		}
		config := readProviderJSONMap(path)
		model := ensureMap(config, "model")
		model["compressionThreshold"] = float64(threshold) / 100
		if err := writeProviderJSONMap(path, config); err != nil {
			return ProviderApplyResult{}, err
		}
	case "opencode":
		if threshold < 1_024 {
			return ProviderApplyResult{}, errors.New("OpenCode reserved context must be at least 1024 tokens")
		}
		effectiveContext := current.Context.Tokens
		if current.Context.Writable {
			if tokens > 0 {
				effectiveContext = tokens
			} else if model, ok := selectedCatalogModel(current.Runtime, s.Settings()); ok {
				effectiveContext = model.ContextWindow
			}
		}
		if effectiveContext > 0 && threshold >= effectiveContext {
			return ProviderApplyResult{}, errors.New("OpenCode reserved context must be smaller than the selected model context window")
		}
		config := readProviderJSONMap(path)
		compaction := ensureMap(config, "compaction")
		compaction["auto"] = autoCompactEnabled
		compaction["prune"] = pruneEnabled
		compaction["reserved"] = threshold
		if current.Context.Writable {
			model := openCodeModelConfig(config, selectedProviderModel(s.Settings(), "opencode"))
			if model == nil {
				return ProviderApplyResult{}, errors.New("selected OpenCode model is not declared in opencode.json")
			}
			limit := ensureMap(model, "limit")
			if tokens > 0 {
				if tokens < 16_384 {
					return ProviderApplyResult{}, errors.New("OpenCode model context must be at least 16384 tokens")
				}
				limit["context"] = tokens
			} else {
				delete(limit, "context")
			}
		}
		if err := writeProviderJSONMap(path, config); err != nil {
			return ProviderApplyResult{}, err
		}
	}

	invalidateProviderConfigurationCache(providerID)
	configuration, err := s.CheckProviderConfiguration(providerID, true)
	if err != nil {
		return ProviderApplyResult{}, err
	}
	return ProviderApplyResult{
		ProviderID: providerID, ApplyLevel: configuration.ApplyLevel,
		RestartRequired: providerID == "codex", Configuration: configuration,
		Warnings: configuration.Warnings,
	}, nil
}

func (s *AppService) RestartProvider(providerID string) (ProviderApplyResult, error) {
	providerID = normalizeProviderID(providerID)
	if providerID == "" {
		return ProviderApplyResult{}, errors.New("unsupported provider")
	}
	if providerID != "codex" {
		return ProviderApplyResult{}, errors.New("this provider has no persistent process; saved configuration is loaded on the next turn")
	}
	if s.providerHasActiveWork(providerID) {
		return ProviderApplyResult{}, errors.New("stop the running provider turn before restarting")
	}
	workspace := strings.TrimSpace(s.Settings().Workspace)
	if workspace == "" {
		workspace = strings.TrimSpace(s.activeWorkspacePath())
	}
	if err := s.StopCodex(); err != nil {
		return ProviderApplyResult{}, err
	}
	if err := s.StartCodex(workspace); err != nil {
		return ProviderApplyResult{}, err
	}
	invalidateProviderConfigurationCache(providerID)
	configuration, err := s.CheckProviderConfiguration(providerID, true)
	if err != nil {
		return ProviderApplyResult{}, err
	}
	return ProviderApplyResult{
		ProviderID: providerID, ApplyLevel: configuration.ApplyLevel,
		RestartRequired: false, Warnings: configuration.Warnings,
		Configuration: configuration,
	}, nil
}

func readProviderText(path string) (string, error) {
	payload, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(payload) > providerConfigurationMaxBytes {
		return "", errors.New("provider configuration file is too large")
	}
	return string(payload), nil
}

func readProviderJSONMap(path string) map[string]any {
	result := map[string]any{}
	_ = readLimitedJSON(path, &result)
	return result
}

func writeProviderJSONMap(path string, config map[string]any) error {
	payload, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writeTextFileAtomic(path, string(payload)+"\n")
}

func mapFromAny(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func ensureMap(parent map[string]any, key string) map[string]any {
	if current := mapFromAny(parent[key]); current != nil {
		return current
	}
	created := map[string]any{}
	parent[key] = created
	return created
}

func integerFromConfig(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func floatFromConfig(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolFromConfig(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

func truthyConfigValue(value any) bool {
	if parsed, ok := boolFromConfig(value); ok {
		return parsed
	}
	if number, ok := integerFromConfig(value); ok {
		return number != 0
	}
	text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	return text == "yes" || text == "on"
}

func setOptionalStringInteger(target map[string]any, key string, value int64) {
	if value > 0 {
		target[key] = strconv.FormatInt(value, 10)
		return
	}
	delete(target, key)
}

func rootTOMLText(text string) string {
	if index := regexp.MustCompile(`(?m)^\s*\[`).FindStringIndex(text); index != nil {
		return text[:index[0]]
	}
	return text
}

func readTOMLInteger(text, section, key string) (int64, bool) {
	body := rootTOMLText(text)
	if section != "" {
		body = extractTOMLSection(text, section)
		if body == "" {
			return 0, false
		}
	}
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*([0-9][0-9_]*)\s*(?:#.*)?$`)
	match := re.FindStringSubmatch(body)
	if len(match) < 2 {
		return 0, false
	}
	parsed, err := strconv.ParseInt(strings.ReplaceAll(match[1], "_", ""), 10, 64)
	return parsed, err == nil
}

func optionalIntegerLiteral(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func upsertTOMLScalar(text, section, key, literal string) string {
	keyPattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*[^\r\n]*(?:\r?\n|$)`)
	line := key + " = " + literal + "\n"
	if section == "" {
		root := rootTOMLText(text)
		updated := root
		if keyPattern.MatchString(root) {
			if literal == "" {
				updated = keyPattern.ReplaceAllString(root, "")
			} else {
				updated = keyPattern.ReplaceAllString(root, line)
			}
		} else if literal != "" {
			if updated != "" && !strings.HasSuffix(updated, "\n") {
				updated += "\n"
			}
			updated += line
		}
		return updated + text[len(root):]
	}

	header := "[" + section + "]"
	sectionPattern := regexp.MustCompile(`(?ms)(^\s*\[` + regexp.QuoteMeta(section) + `\]\s*\r?\n)(.*?)(^\s*\[|\z)`)
	if sectionPattern.MatchString(text) {
		return sectionPattern.ReplaceAllStringFunc(text, func(block string) string {
			parts := sectionPattern.FindStringSubmatch(block)
			if len(parts) < 4 {
				return block
			}
			body := parts[2]
			if keyPattern.MatchString(body) {
				if literal == "" {
					body = keyPattern.ReplaceAllString(body, "")
				} else {
					body = keyPattern.ReplaceAllString(body, line)
				}
			} else if literal != "" {
				if body != "" && !strings.HasSuffix(body, "\n") {
					body += "\n"
				}
				body += line
			}
			return parts[1] + body + parts[3]
		})
	}
	if literal == "" {
		return text
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return text + header + "\n" + line
}

func findGrokModelSection(text, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	re := regexp.MustCompile(`(?m)^\s*\[model\.([^\]]+)\]\s*$`)
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		key := strings.Trim(strings.TrimSpace(match[1]), `"'`)
		if strings.EqualFold(key, model) {
			return "model." + match[1]
		}
	}
	return ""
}

func openCodeModelConfig(config map[string]any, selected string) map[string]any {
	providerID, modelID, ok := strings.Cut(strings.TrimSpace(selected), "/")
	if !ok || strings.TrimSpace(providerID) == "" || strings.TrimSpace(modelID) == "" {
		return nil
	}
	providers := mapFromAny(config["provider"])
	provider := mapFromAny(providers[providerID])
	models := mapFromAny(provider["models"])
	return mapFromAny(models[modelID])
}
