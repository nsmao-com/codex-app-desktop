package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nice_codex_desktop/internal/codex"
)

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

func providerContextPolicy(provider AgentProviderRuntime, settings UserSettings) ProviderContextPolicyView {
	selected := strings.TrimSpace(selectedProviderModel(settings, provider.Kind))
	var contextWindow int64
	isFallback := false
	for _, model := range provider.Models {
		if selected != "" && !strings.EqualFold(model.Model, selected) {
			continue
		}
		contextWindow = model.ContextWindow
		isFallback = strings.Contains(strings.ToLower(model.Description), "fallback") || strings.Contains(strings.ToLower(model.Description), "alias")
		break
	}
	if contextWindow == 0 && len(provider.Models) > 0 {
		for _, model := range provider.Models {
			if model.IsDefault {
				contextWindow = model.ContextWindow
				isFallback = strings.Contains(strings.ToLower(model.Description), "fallback") || strings.Contains(strings.ToLower(model.Description), "alias")
				break
			}
		}
	}
	policy := ProviderContextPolicyView{
		Tokens: contextWindow, Source: "unknown", IsFallback: isFallback,
		CompactStrategy: "unsupported",
	}
	if contextWindow > 0 {
		policy.Source = "native-catalog"
		if isFallback {
			policy.Source = "fallback"
		}
	}
	switch provider.Kind {
	case "codex":
		if settings.CodexContextWindow > 0 {
			policy.Tokens = settings.CodexContextWindow
			policy.Source = "nicecodex-override"
			policy.IsFallback = false
		}
		policy.Writable = true
		policy.AutoCompactSupported = true
		policy.ThresholdConfigurable = true
		policy.AutoCompactThreshold = settings.CodexAutoCompactThreshold
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
	case "claude":
		policy.CompactStrategy = "native"
		policy.CompactAvailable = true
	case "gemini", "opencode":
		policy.CompactStrategy = "local-history"
	}
	return policy
}

func providerConfigurationView(provider AgentProviderRuntime, settings UserSettings) ProviderConfigurationView {
	view := ProviderConfigurationView{
		Runtime: provider, ConfigPath: providerConfigPath(provider.Kind),
		ModelSource: "runtime-catalog", ApplyLevel: "next-turn",
		CanReload: true, CanRestart: true, SupportsModel: len(provider.Models) > 0,
		SupportsEffort: len(provider.ReasoningEfforts) > 0,
		Context:        providerContextPolicy(provider, settings), Warnings: []string{},
	}
	switch provider.Kind {
	case "codex":
		view.ApplyLevel = "reconnect"
		view.RestartRequired = true
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
		view.Warnings = append(view.Warnings, "Gemini CLI does not expose a stable reasoning-effort flag; the saved effort preference is not sent.")
	case "opencode":
		view.PermissionModes = []string{"auto"}
		view.Warnings = append(view.Warnings, "OpenCode permission controls are limited to the native --auto mapping.")
	}
	return view
}

func (s *AppService) CheckProviderConfiguration(providerID string, force bool) (ProviderConfigurationView, error) {
	providerID = normalizeProviderID(providerID)
	if providerID == "" {
		return ProviderConfigurationView{}, errors.New("unsupported provider")
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
		RestartRequired: configuration.RestartRequired, Warnings: configuration.Warnings,
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

func (s *AppService) UpdateProviderContextPolicy(providerID string, tokens, threshold int64) (ProviderApplyResult, error) {
	providerID = normalizeProviderID(providerID)
	if providerID != "codex" {
		return ProviderApplyResult{}, errors.New("this provider context policy is read-only")
	}
	tokens, threshold = normalizeCodexContextSettings(tokens, threshold)
	settings := s.Settings()
	settings.CodexContextWindow = tokens
	settings.CodexAutoCompactThreshold = threshold
	if err := writeSettings(s.settingsPath, settings); err != nil {
		return ProviderApplyResult{}, err
	}
	s.mu.Lock()
	s.settings = settings
	s.mu.Unlock()
	configuration, err := s.CheckProviderConfiguration(providerID, false)
	if err != nil {
		return ProviderApplyResult{}, err
	}
	return ProviderApplyResult{
		ProviderID: providerID, ApplyLevel: "immediate", RestartRequired: false,
		Configuration: configuration, Warnings: configuration.Warnings,
	}, nil
}

func (s *AppService) RestartProvider(providerID string) (ProviderApplyResult, error) {
	providerID = normalizeProviderID(providerID)
	if providerID == "" {
		return ProviderApplyResult{}, errors.New("unsupported provider")
	}
	if s.providerHasActiveWork(providerID) {
		return ProviderApplyResult{}, errors.New("stop the running provider turn before restarting")
	}
	if providerID == "codex" {
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
