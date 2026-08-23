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
		return filepath.Join(resolveGeminiHome(), "settings.json")
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
	// An explicit model that is absent from the native catalog has an unknown
	// window. Falling back to an unrelated default would present a precise but
	// incorrect context percentage for custom gateways and model aliases.
	if selected != "" {
		return AgentProviderModel{}, false
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
		thresholdScope := readTOMLString(text, "", "model_auto_compact_token_limit_scope")
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
		policy.ThresholdStep = 1
		policy.ThresholdScopeSupported = true
		policy.ThresholdScopeOptions = []string{"total", "body_after_prefix"}
		if validCodexAutoCompactScope(thresholdScope) && thresholdScope != "" {
			policy.ThresholdScopeConfigured = true
			policy.ThresholdScope = thresholdScope
		}
		policy.Description = "Codex reads model_context_window, model_auto_compact_token_limit, and its counting scope from config.toml; reconnect the app-server after changes."
	case "claude":
		config := readProviderJSONMap(path)
		env := mapFromAny(config["env"])
		autoCompact, hasAutoCompact := boolFromConfig(config["autoCompactEnabled"])
		if !hasAutoCompact {
			autoCompact = true
		}
		threshold, hasThreshold := integerFromConfig(config["autoCompactWindow"])
		if override, ok := integerFromConfig(env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]); ok && override > 0 {
			threshold = override
			hasThreshold = true
		}
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
		policy.AutoCompactSupported = true
		policy.AutoCompactEnabled = autoCompact
		policy.AutoCompactToggleable = true
		policy.ThresholdConfigurable = true
		policy.ThresholdConfigured = hasThreshold && threshold >= 100_000
		policy.AutoCompactThreshold = threshold
		policy.ThresholdMinimum = 100_000
		policy.ThresholdMaximum = 1_000_000
		policy.ThresholdStep = 10_000
		policy.Description = "Claude model context is fixed upstream. autoCompactEnabled and autoCompactWindow are native settings; Nice Codex starts a fresh Claude process on the next turn."
	case "grok":
		selected := strings.TrimSpace(selectedProviderModel(settings, "grok"))
		if selected == "" {
			if model, ok := selectedCatalogModel(provider, settings); ok {
				selected = model.Model
			}
		}
		selectedWindow := int64(0)
		selectedWindowFromCatalog := false
		for _, model := range provider.Models {
			if strings.EqualFold(model.Model, selected) {
				selectedWindow = model.ContextWindow
				selectedWindowFromCatalog = selectedWindow > 0
				break
			}
		}
		if selectedWindow <= 0 {
			selectedWindow = knownProviderContextWindow("grok", selected)
		}
		if selectedWindow > 0 {
			policy.Tokens = selectedWindow
			policy.Source = "official-model-table"
			if selectedWindowFromCatalog {
				policy.Source = "native-catalog"
			}
			policy.IsFallback = false
		}
		if normalizeGrokBackend(settings.GrokBackend) == grokBackendAPI {
			policy.Description = "Grok API context is fixed by the selected upstream model and cannot be overridden by Nice Codex."
			break
		}
		text, _ := readProviderText(path)
		threshold, hasThreshold := readTOMLInteger(text, "session", "auto_compact_threshold_percent")
		if !hasThreshold {
			if modelThreshold, ok := readGrokModelAutoCompactThreshold(filepath.Join(resolveGrokHome(), "models_cache.json"), selected); ok {
				threshold = modelThreshold
			} else {
				threshold = 85
			}
		}
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
		policy.AutoCompactSupported = true
		policy.AutoCompactEnabled = true
		policy.ThresholdConfigurable = true
		policy.ThresholdConfigured = hasThreshold
		policy.AutoCompactThreshold = threshold
		policy.ThresholdUnit = "percent"
		policy.ThresholdMinimum = 0
		policy.ThresholdMaximum = 100
		policy.ThresholdStep = 1
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
		if selectedWindow := knownProviderContextWindow("gemini", selectedProviderModel(settings, "gemini")); selectedWindow > 0 {
			policy.Tokens = selectedWindow
			policy.Source = "gemini-cli-fixed"
			policy.IsFallback = false
		}
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
		policy.ThresholdMaximum = 100
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
			reserved = 0
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
		policy.ThresholdMinimum = 0
		policy.ThresholdMaximum = 1_000_000
		policy.ThresholdStep = 1
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
		policy.Description = "OpenCode compaction.auto, compaction.prune, and reserved tokens are native settings. Leaving reserved empty keeps OpenCode's model-dependent default; limit.context is editable only for declared models."
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
		if text, err := readProviderText(providerConfigPath("codex")); err == nil {
			if scope := readTOMLString(text, "", "model_auto_compact_token_limit_scope"); scope != "" && !validCodexAutoCompactScope(scope) {
				view.Warnings = append(view.Warnings, "model_auto_compact_token_limit_scope is invalid; Codex accepts only total or body_after_prefix.")
			}
		}
	case "claude":
		view.PermissionModes = []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}
		config := readProviderJSONMap(providerConfigPath("claude"))
		env := mapFromAny(config["env"])
		if _, ok := integerFromConfig(env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"]); ok {
			view.Warnings = append(view.Warnings, "CLAUDE_CODE_MAX_CONTEXT_TOKENS is a gateway/model-ID correction, not a general model context limit; the displayed window remains fixed by the resolved model.")
		}
		if _, ok := integerFromConfig(env["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"]); ok {
			view.Warnings = append(view.Warnings, "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE is present and may force earlier compaction than autoCompactWindow.")
		}
		if _, ok := integerFromConfig(env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]); ok {
			view.Warnings = append(view.Warnings, "CLAUDE_CODE_AUTO_COMPACT_WINDOW overrides the top-level autoCompactWindow; Nice Codex keeps both values synchronized when saving.")
		}
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

func (s *AppService) UpdateProviderContextPolicy(providerID string, tokens, threshold int64, autoCompactEnabled, pruneEnabled bool, thresholdScope string) (ProviderApplyResult, error) {
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
	// The UI uses -1 to distinguish an empty override from an explicit zero,
	// which is a valid Grok percentage and OpenCode reserved-token value.
	thresholdUnset := threshold == -1
	if thresholdUnset {
		threshold = 0
	}
	if tokens < 0 || threshold < 0 {
		return ProviderApplyResult{}, errors.New("context values cannot be negative")
	}
	path := current.ConfigPath

	switch providerID {
	case "codex":
		thresholdScope = strings.TrimSpace(thresholdScope)
		if !validCodexAutoCompactScope(thresholdScope) {
			return ProviderApplyResult{}, errors.New("Codex auto-compact scope must be total or body_after_prefix")
		}
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
		quotedScope := ""
		if thresholdScope != "" {
			quotedScope = strconv.Quote(thresholdScope)
		}
		text = upsertTOMLScalar(text, "", "model_auto_compact_token_limit_scope", quotedScope)
		if err := writeTextFileAtomic(path, text); err != nil {
			return ProviderApplyResult{}, err
		}
	case "claude":
		if threshold > 0 && (threshold < 100_000 || threshold > 1_000_000) {
			return ProviderApplyResult{}, errors.New("Claude auto-compact window must be between 100000 and 1000000 tokens")
		}
		config := readProviderJSONMap(path)
		config["autoCompactEnabled"] = autoCompactEnabled
		if threshold > 0 {
			config["autoCompactWindow"] = threshold
		} else {
			delete(config, "autoCompactWindow")
		}
		if env := mapFromAny(config["env"]); env != nil {
			if _, exists := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; exists {
				if threshold > 0 {
					env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.FormatInt(threshold, 10)
				} else {
					delete(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW")
				}
			}
			delete(env, "DISABLE_COMPACT")
		}
		if err := writeProviderJSONMap(path, config); err != nil {
			return ProviderApplyResult{}, err
		}
	case "grok":
		if normalizeGrokBackend(s.Settings().GrokBackend) == grokBackendAPI {
			return ProviderApplyResult{}, errors.New("Grok API context and compaction are controlled by the upstream model")
		}
		if threshold < 0 || threshold > 100 {
			return ProviderApplyResult{}, errors.New("Grok auto-compact percentage must be between 0 and 100")
		}
		text, err := readProviderText(path)
		if err != nil {
			return ProviderApplyResult{}, err
		}
		thresholdLiteral := strconv.FormatInt(threshold, 10)
		if thresholdUnset {
			thresholdLiteral = ""
		}
		text = upsertTOMLScalar(text, "session", "auto_compact_threshold_percent", thresholdLiteral)
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
		if !thresholdUnset && (threshold < 1 || threshold > 100) {
			return ProviderApplyResult{}, errors.New("Gemini compression percentage must be between 1 and 100")
		}
		config := readProviderJSONMap(path)
		model := ensureMap(config, "model")
		if thresholdUnset {
			delete(model, "compressionThreshold")
		} else {
			model["compressionThreshold"] = float64(threshold) / 100
		}
		if err := writeProviderJSONMap(path, config); err != nil {
			return ProviderApplyResult{}, err
		}
	case "opencode":
		if threshold < 0 {
			return ProviderApplyResult{}, errors.New("OpenCode reserved context cannot be negative")
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
		if threshold > 0 {
			compaction["reserved"] = threshold
		} else {
			delete(compaction, "reserved")
		}
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

func validCodexAutoCompactScope(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "total", "body_after_prefix":
		return true
	default:
		return false
	}
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

func readGrokModelAutoCompactThreshold(path, model string) (int64, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return 0, false
	}
	models := mapFromAny(readProviderJSONMap(path)["models"])
	for key, raw := range models {
		if !strings.EqualFold(strings.TrimSpace(key), model) {
			continue
		}
		info := mapFromAny(mapFromAny(raw)["info"])
		threshold, ok := integerFromConfig(info["auto_compact_threshold_percent"])
		if ok && threshold >= 0 && threshold <= 100 {
			return threshold, true
		}
		return 0, false
	}
	return 0, false
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
