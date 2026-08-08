package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"nice_codex_desktop/internal/codex"
)

var windowsProviderShimTargetPattern = regexp.MustCompile(`(?i)%(?:dp0%|~dp0)[\\/]+([^"\r\n]+?\.(?:exe|js|cjs|mjs))`)
var windowsPowerShellShimTargetPattern = regexp.MustCompile(`(?i)\$basedir[\\/]+([^"\r\n]+?\.(?:exe|js|cjs|mjs))`)

const providerProbeCacheTTL = 30 * time.Second

type providerProbeCacheEntry struct {
	provider  AgentProviderRuntime
	expiresAt time.Time
}

var providerProbeCache = struct {
	sync.Mutex
	entries map[string]providerProbeCacheEntry
}{entries: make(map[string]providerProbeCacheEntry)}

type AgentProviderRuntime struct {
	ID               string                         `json:"id"`
	Name             string                         `json:"name"`
	Kind             string                         `json:"kind"`
	Installed        bool                           `json:"installed"`
	Healthy          bool                           `json:"healthy"`
	RuntimeReady     bool                           `json:"runtimeReady"`
	Version          string                         `json:"version"`
	Executable       string                         `json:"executable"`
	Status           string                         `json:"status"`
	Message          string                         `json:"message"`
	Capabilities     []string                       `json:"capabilities"`
	Models           []AgentProviderModel           `json:"models"`
	ReasoningEfforts []AgentProviderReasoningEffort `json:"reasoningEfforts"`
	// Providers is populated by runtimes that expose their own provider catalog
	// (currently OpenCode). It is deliberately separate from Codex's provider
	// field so external CLIs keep their native provider/model identity.
	Providers []ExternalProviderView `json:"providers,omitempty"`
}

type AgentProviderModel struct {
	Model         string `json:"model"`
	ProviderID    string `json:"providerId,omitempty"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	IsDefault     bool   `json:"isDefault"`
	ContextWindow int64  `json:"contextWindow"`
}

type AgentProviderReasoningEffort struct {
	Effort      string `json:"effort"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	IsDefault   bool   `json:"isDefault"`
}

type claudeSettingsFile struct {
	Env             map[string]any    `json:"env"`
	Model           string            `json:"model"`
	AvailableModels []string          `json:"availableModels"`
	ModelOverrides  map[string]string `json:"modelOverrides"`
}

type claudeProviderFile struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

type providerProbe struct {
	id           string
	name         string
	commands     []string
	capabilities []string
	healthArgs   []string
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)
var tomlModelPattern = regexp.MustCompile(`(?m)^\s*(?:model|default_model)\s*=\s*["']([^"']+)["']`)
var geminiModelPattern = regexp.MustCompile(`gemini-[A-Za-z0-9._-]+`)

func detectAgentProviders(codexDetection codex.Detection) []AgentProviderRuntime {
	// Product runtimes: Codex, Claude Code, Grok, Gemini CLI, and OpenCode.
	codexProvider := codexAgentProvider(codexDetection)
	claudeProbe := providerProbe{
		id:           "claude",
		name:         "Claude Code",
		commands:     commandCandidates("claude"),
		capabilities: []string{"cli", "streaming", "tools", "mcp"},
		healthArgs:   []string{"auth", "status", "--json"},
	}
	// Both CLIs may take several seconds through Windows npm shims. Probe them in
	// parallel so explicit refresh latency is bounded by the slower provider.
	grokStatusCh := make(chan GrokRuntimeStatus, 1)
	go func() { grokStatusCh <- detectGrokRuntime() }()
	claudeProvider := cachedRunProviderProbe(claudeProbe)
	geminiProvider := cachedRunProviderProbe(providerProbe{
		id: "gemini", name: "Gemini CLI", commands: commandCandidates("gemini"),
		capabilities: []string{"cli", "streaming", "tools", "mcp"}, healthArgs: []string{"--version"},
	})
	opencodeProvider := cachedRunProviderProbe(providerProbe{
		id: "opencode", name: "OpenCode", commands: commandCandidates("opencode"),
		capabilities: []string{"cli", "streaming", "tools", "mcp", "sessions"}, healthArgs: []string{"--version"},
	})
	// Fallback health probe when `auth status --json` is unavailable on older CLIs.
	if claudeProvider.Installed && claudeProvider.Status == "configuration-error" {
		if _, err := runProbeCommand(claudeProvider.Executable, []string{"--version"}, 3*time.Second); err == nil {
			claudeProvider.Healthy = true
			claudeProvider.RuntimeReady = true
			claudeProvider.Status = providerStatus(true, true, true)
			claudeProvider.Message = "Claude Code installed"
		}
	}
	if claudeProvider.Installed && claudeProvider.Message == "" {
		claudeProvider.Message = "Claude Code CLI"
	}
	if !claudeProvider.Installed {
		claudeProvider.Message = "Install Claude Code CLI (claude) to use this runtime"
	}
	grokProvider := grokAgentProvider(<-grokStatusCh)
	return []AgentProviderRuntime{codexProvider, claudeProvider, grokProvider, geminiProvider, opencodeProvider}
}

// detectAgentProvidersQuick is safe for Bootstrap: it performs path/config
// discovery only. Authentication/version probes remain lazy provider actions.
func detectAgentProvidersQuick(codexDetection codex.Detection, grokStatus GrokRuntimeStatus) []AgentProviderRuntime {
	claudeModels, claudeEfforts := discoverProviderCatalog("claude")
	claudeExecutable := findCommand(commandCandidates("claude"))
	claudeAvailable := claudeExecutable != ""
	claudeMessage := "Install Claude Code CLI (claude) to use this runtime"
	if claudeAvailable {
		claudeMessage = "Claude Code installed; authentication is checked when opened"
	}
	claudeProvider := AgentProviderRuntime{
		ID:               "claude",
		Name:             "Claude Code",
		Kind:             "claude",
		Installed:        claudeAvailable,
		Healthy:          claudeAvailable,
		RuntimeReady:     claudeAvailable,
		Executable:       claudeExecutable,
		Status:           providerStatus(claudeAvailable, claudeAvailable, true),
		Message:          claudeMessage,
		Capabilities:     []string{"cli", "streaming", "tools", "mcp"},
		Models:           claudeModels,
		ReasoningEfforts: claudeEfforts,
	}
	geminiModels, geminiEfforts := discoverProviderCatalog("gemini")
	geminiExecutable := findCommand(commandCandidates("gemini"))
	geminiProvider := AgentProviderRuntime{
		ID: "gemini", Name: "Gemini CLI", Kind: "gemini", Installed: geminiExecutable != "",
		Healthy: geminiExecutable != "", RuntimeReady: geminiExecutable != "", Executable: geminiExecutable,
		Status:  providerStatus(geminiExecutable != "", geminiExecutable != "", true),
		Message: "Install Gemini CLI (gemini) to use this runtime", Capabilities: []string{"cli", "streaming", "tools", "mcp"},
		Models: geminiModels, ReasoningEfforts: geminiEfforts,
	}
	if geminiExecutable != "" {
		geminiProvider.Message = "Gemini CLI installed; authentication is checked when opened"
	}
	opencodeModels, opencodeEfforts := discoverProviderCatalog("opencode")
	opencodeExecutable := findCommand(commandCandidates("opencode"))
	opencodeProvider := AgentProviderRuntime{
		ID: "opencode", Name: "OpenCode", Kind: "opencode", Installed: opencodeExecutable != "",
		Healthy: opencodeExecutable != "", RuntimeReady: opencodeExecutable != "", Executable: opencodeExecutable,
		Status:  providerStatus(opencodeExecutable != "", opencodeExecutable != "", true),
		Message: "Install OpenCode CLI (opencode) to use this runtime", Capabilities: []string{"cli", "streaming", "tools", "mcp", "sessions"},
		Models: opencodeModels, ReasoningEfforts: opencodeEfforts,
	}
	if home, err := os.UserHomeDir(); err == nil && opencodeExecutable != "" {
		_, _, opencodeProvider.Providers = discoverOpenCodeCatalog(home)
	}
	if opencodeExecutable != "" {
		opencodeProvider.Message = "OpenCode CLI installed; provider credentials are managed by OpenCode"
	}
	return []AgentProviderRuntime{
		codexAgentProvider(codexDetection),
		claudeProvider,
		grokAgentProvider(grokStatus),
		geminiProvider,
		opencodeProvider,
	}
}

func codexAgentProvider(codexDetection codex.Detection) AgentProviderRuntime {
	return AgentProviderRuntime{
		ID:           "codex",
		Name:         "Codex",
		Kind:         "codex",
		Installed:    codexDetection.Available,
		Healthy:      codexDetection.Available,
		RuntimeReady: codexDetection.Available,
		Version:      codexDetection.Version,
		Executable:   codexDetection.Binary,
		Status:       providerStatus(codexDetection.Available, codexDetection.Available, true),
		Capabilities: []string{"app-server", "streaming", "reasoning", "tools", "mcp", "skills", "diff", "resume"},
	}
}

func grokAgentProvider(grokStatus GrokRuntimeStatus) AgentProviderRuntime {
	grokModels, grokEfforts := discoverProviderCatalog("grok")
	grokReady := grokStatus.BuildAvailable || grokStatus.APIConfigured
	return AgentProviderRuntime{
		ID:               "grok",
		Name:             "Grok",
		Kind:             "grok",
		Installed:        grokStatus.BuildAvailable || grokStatus.APIConfigured,
		Healthy:          grokReady,
		RuntimeReady:     grokReady,
		Version:          grokStatus.BuildVersion,
		Executable:       grokStatus.BuildExecutable,
		Status:           providerStatus(grokStatus.BuildAvailable || grokStatus.APIConfigured, grokReady, true),
		Message:          grokProviderMessage(grokStatus),
		Capabilities:     []string{"build-cli", "api", "streaming", "reasoning", "tools"},
		Models:           grokModels,
		ReasoningEfforts: grokEfforts,
	}
}

func cachedRunProviderProbe(probe providerProbe) AgentProviderRuntime {
	now := time.Now()
	providerProbeCache.Lock()
	if cached, ok := providerProbeCache.entries[probe.id]; ok && now.Before(cached.expiresAt) {
		providerProbeCache.Unlock()
		return cached.provider
	}
	providerProbeCache.Unlock()

	provider := runProviderProbe(probe)
	providerProbeCache.Lock()
	providerProbeCache.entries[probe.id] = providerProbeCacheEntry{
		provider:  provider,
		expiresAt: time.Now().Add(providerProbeCacheTTL),
	}
	providerProbeCache.Unlock()
	return provider
}

func grokProviderMessage(status GrokRuntimeStatus) string {
	parts := make([]string, 0, 2)
	if status.BuildAvailable {
		if status.BuildAuthenticated {
			parts = append(parts, "Grok Build ready")
		} else {
			parts = append(parts, "Grok Build installed (auth may be required)")
		}
	}
	if status.APIConfigured {
		parts = append(parts, "Grok API key configured")
	}
	if len(parts) == 0 {
		return "Install Grok Build CLI or configure a Grok API key"
	}
	return strings.Join(parts, " · ")
}

func runProviderProbe(probe providerProbe) AgentProviderRuntime {
	models, reasoningEfforts := discoverProviderCatalog(probe.id)
	result := AgentProviderRuntime{
		ID: probe.id, Name: probe.name, Kind: probe.id, Status: "not-installed",
		Capabilities: append([]string(nil), probe.capabilities...), Models: models, ReasoningEfforts: reasoningEfforts,
	}
	if probe.id == "opencode" {
		if home, err := os.UserHomeDir(); err == nil {
			_, _, result.Providers = discoverOpenCodeCatalog(home)
		}
	}
	// Keep workbench labels fixed to the supported local runtimes.
	// Do not append third-party proxy nicknames onto Claude Code.
	executable := findCommand(probe.commands)
	if executable == "" {
		result.Message = "CLI executable was not found in PATH"
		return result
	}
	result.Installed = true
	result.Executable = executable
	versionOutput, _ := runProbeCommand(executable, []string{"--version"}, 2500*time.Millisecond)
	result.Version = firstOutputLine(versionOutput)

	output, err := runProbeCommand(executable, probe.healthArgs, 4*time.Second)
	if err != nil || containsConfigurationError(output) {
		result.Status = "configuration-error"
		result.Message = conciseProbeError(output, err)
		return result
	}
	if probe.id == "claude" && !strings.Contains(strings.ReplaceAll(output, " ", ""), `"loggedIn":true`) {
		result.Status = "authentication-required"
		result.Message = "Claude Code is installed but is not signed in"
		return result
	}
	result.Healthy = true
	result.RuntimeReady = true
	result.Status = "ready"
	result.Message = "CLI is healthy and ready for Nice Codex conversations"
	return result
}

func discoverProviderCatalog(kind string) ([]AgentProviderModel, []AgentProviderReasoningEffort) {
	home, err := os.UserHomeDir()
	if err != nil {
		return fallbackProviderModels(kind), fallbackReasoningEfforts(kind)
	}
	models := make([]AgentProviderModel, 0, 8)
	seen := make(map[string]int)
	addModel := func(model, displayName, description string, isDefault bool, contextWindow int64) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		key := strings.ToLower(model)
		if index, ok := seen[key]; ok {
			if contextWindow > 0 && models[index].ContextWindow <= 0 {
				models[index].ContextWindow = contextWindow
			}
			if isDefault {
				for i := range models {
					models[i].IsDefault = false
				}
				models[index].IsDefault = true
			}
			return
		}
		if isDefault {
			for i := range models {
				models[i].IsDefault = false
			}
		}
		if displayName == "" {
			displayName = model
		}
		seen[key] = len(models)
		models = append(models, AgentProviderModel{
			Model: model, DisplayName: displayName, Description: description, IsDefault: isDefault,
			ContextWindow: contextWindow,
		})
	}

	switch kind {
	case "claude":
		for _, model := range discoverClaudeModels(home) {
			addModel(model.Model, model.DisplayName, model.Description, model.IsDefault, model.ContextWindow)
		}
	case "gemini":
		if model := readEnvValue(filepath.Join(home, ".gemini", ".env"), "GEMINI_MODEL"); model != "" {
			addModel(model, model, "Configured by GEMINI_MODEL", true, 0)
		}
		for _, model := range readJSONModelValues(filepath.Join(home, ".gemini", "settings.json")) {
			addModel(model, model, "Discovered in Gemini CLI configuration", len(models) == 0, 0)
		}
		for _, model := range readModelIDsFromDirectory(filepath.Join(home, ".gemini"), geminiModelPattern) {
			addModel(model, model, "Discovered in local Gemini CLI data", len(models) == 0, 0)
		}
	case "opencode":
		if discovered, discoveredEfforts, _ := discoverOpenCodeCatalog(home); len(discovered) > 0 {
			for _, model := range discovered {
				addModel(model.Model, model.DisplayName, model.Description, model.IsDefault, model.ContextWindow)
				if index, ok := seen[strings.ToLower(model.Model)]; ok {
					models[index].ProviderID = model.ProviderID
				}
			}
			if len(discoveredEfforts) > 0 {
				return models, discoveredEfforts
			}
		} else {
			for _, model := range readJSONModelValues(
				filepath.Join(home, ".config", "opencode", "opencode.json"),
				filepath.Join(home, ".config", "opencode", "config.json"),
			) {
				addModel(model, model, "Discovered in OpenCode configuration", len(models) == 0, 0)
			}
		}
	case "grok":
		configured := readTOMLModel(filepath.Join(home, ".grok", "config.toml"))
		if configured != "" {
			addModel(configured, configured, "Configured in Grok Build", true, 0)
		}
		cacheModels, cacheEfforts := readGrokModelCache(filepath.Join(home, ".grok", "models_cache.json"))
		for _, model := range cacheModels {
			addModel(model.Model, model.DisplayName, model.Description, model.IsDefault || len(models) == 0, model.ContextWindow)
		}
		if len(models) == 0 {
			for _, model := range fallbackProviderModels(kind) {
				addModel(model.Model, model.DisplayName, model.Description, model.IsDefault, model.ContextWindow)
			}
		}
		for i := range models {
			if models[i].ContextWindow <= 0 {
				models[i].ContextWindow = knownProviderContextWindow("grok", models[i].Model)
			}
		}
		if len(cacheEfforts) > 0 {
			return models, cacheEfforts
		}
	}
	if len(models) == 0 {
		for _, model := range fallbackProviderModels(kind) {
			addModel(model.Model, model.DisplayName, model.Description, model.IsDefault, model.ContextWindow)
		}
	}
	return models, fallbackReasoningEfforts(kind)
}

func fallbackProviderModels(kind string) []AgentProviderModel {
	switch kind {
	case "claude":
		// Official Claude Code short aliases (--model sonnet|opus|haiku|fable).
		// Description always states the typical resolved model id for the workbench UI.
		return []AgentProviderModel{
			{Model: "sonnet", DisplayName: "Claude Sonnet", Description: "alias `sonnet` → latest Sonnet (e.g. claude-sonnet-4-6)", IsDefault: true, ContextWindow: 1_000_000},
			{Model: "opus", DisplayName: "Claude Opus", Description: "alias `opus` → latest Opus (e.g. claude-opus-4-6 / claude-opus-4-8)", ContextWindow: 1_000_000},
			{Model: "haiku", DisplayName: "Claude Haiku", Description: "alias `haiku` → latest Haiku (e.g. claude-haiku-4-5)", ContextWindow: 200_000},
			{Model: "fable", DisplayName: "Claude Fable", Description: "alias `fable` → latest Fable (e.g. claude-fable-5)", ContextWindow: 1_000_000},
		}
	case "gemini":
		return []AgentProviderModel{
			{Model: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", Description: "High-quality Gemini model", IsDefault: true},
			{Model: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", Description: "Fast Gemini model"},
		}
	case "grok":
		return []AgentProviderModel{{Model: "grok-4.5", DisplayName: "Grok 4.5", Description: "Grok Build frontier model", IsDefault: true, ContextWindow: 500_000}}
	case "opencode":
		return []AgentProviderModel{{Model: "anthropic/claude-sonnet-4-6", DisplayName: "Claude Sonnet", Description: "OpenCode provider/model", IsDefault: true}}
	default:
		return nil
	}
}

// knownProviderContextWindow is the fallback for catalogs that do not expose
// context-window metadata. Grok cache metadata still takes priority when present.
func knownProviderContextWindow(kind, model string) int64 {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return 0
	}
	switch kind {
	case "grok":
		if lower == "grok-4.5" {
			return 500_000
		}
	case "claude":
		switch lower {
		case "sonnet", "opus", "fable":
			return 1_000_000
		case "haiku":
			return 200_000
		}
		if strings.Contains(lower, "haiku") {
			return 200_000
		}
		if strings.Contains(lower, "fable-5") || strings.Contains(lower, "fable.5") {
			return 1_000_000
		}
		if strings.Contains(lower, "sonnet-5") || strings.Contains(lower, "sonnet.5") ||
			strings.Contains(lower, "sonnet-4-6") || strings.Contains(lower, "sonnet-4.6") {
			return 1_000_000
		}
		if strings.Contains(lower, "opus-5") || strings.Contains(lower, "opus.5") ||
			strings.Contains(lower, "opus-4-6") || strings.Contains(lower, "opus-4.6") ||
			strings.Contains(lower, "opus-4-7") || strings.Contains(lower, "opus-4.7") ||
			strings.Contains(lower, "opus-4-8") || strings.Contains(lower, "opus-4.8") {
			return 1_000_000
		}
		if strings.Contains(lower, "claude") || strings.Contains(lower, "sonnet") || strings.Contains(lower, "opus") {
			return 200_000
		}
	}
	return 0
}

func fallbackReasoningEfforts(kind string) []AgentProviderReasoningEffort {
	switch kind {
	case "claude":
		return []AgentProviderReasoningEffort{
			{Effort: "high", DisplayName: "High", Description: "Deep reasoning for complex implementation work", IsDefault: true},
			{Effort: "medium", DisplayName: "Medium", Description: "Balanced reasoning and response speed"},
			{Effort: "low", DisplayName: "Low", Description: "Faster responses with lighter reasoning"},
			{Effort: "xhigh", DisplayName: "Extra high", Description: "Extended reasoning for especially difficult work"},
			{Effort: "max", DisplayName: "Max", Description: "Maximum effort when supported by the selected Claude model"},
		}
	case "opencode":
		return []AgentProviderReasoningEffort{
			{Effort: "high", DisplayName: "High", Description: "Provider variant for deeper reasoning", IsDefault: true},
			{Effort: "medium", DisplayName: "Medium", Description: "Balanced provider variant"},
			{Effort: "low", DisplayName: "Low", Description: "Faster provider variant"},
			{Effort: "max", DisplayName: "Max", Description: "Maximum provider variant"},
		}
	case "gemini":
		return []AgentProviderReasoningEffort{{
			Effort: "auto", DisplayName: "Auto", Description: "Gemini chooses the thinking budget for the selected model", IsDefault: true,
		}}
	case "grok":
		return []AgentProviderReasoningEffort{
			{Effort: "high", DisplayName: "High", Description: "Highest implementation quality with extensive reasoning", IsDefault: true},
			{Effort: "medium", DisplayName: "Medium", Description: "Balanced effort with standard implementation and testing"},
			{Effort: "low", DisplayName: "Low", Description: "Quick implementations with lighter reasoning"},
		}
	default:
		return nil
	}
}

func readJSONModelValues(paths ...string) []string {
	result := make([]string, 0, 4)
	seen := make(map[string]struct{})
	var collect func(any)
	collect = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, nested := range typed {
				normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
				if normalized == "model" || normalized == "default_model" || normalized == "model_name" {
					if model, ok := nested.(string); ok {
						model = strings.TrimSpace(model)
						if model != "" {
							if _, exists := seen[strings.ToLower(model)]; !exists {
								seen[strings.ToLower(model)] = struct{}{}
								result = append(result, model)
							}
						}
					}
				}
				collect(nested)
			}
		case []any:
			for _, nested := range typed {
				collect(nested)
			}
		}
	}
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil || len(payload) > 4*1024*1024 {
			continue
		}
		var value any
		if json.Unmarshal(payload, &value) == nil {
			collect(value)
		}
	}
	return result
}

func discoverClaudeModels(home string) []AgentProviderModel {
	settings := readClaudeSettings(filepath.Join(home, ".claude", "settings.json"))
	provider := readClaudeProvider(filepath.Join(home, ".claude", "providers.json"))
	configuredEnv := func(key string) string {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
		value, _ := settings.Env[key].(string)
		return strings.TrimSpace(value)
	}

	type aliasConfig struct {
		alias       string
		family      string
		model       string
		displayName string
		description string
	}
	// Always seed official short aliases so the composer never shows bare IDs without mapping.
	// Env / settings.json can override the resolved target model id.
	aliases := make([]aliasConfig, 0, 4)
	for _, definition := range []struct {
		alias     string
		family    string
		prefix    string
		defaultID string
	}{
		{alias: "sonnet", family: "Sonnet", prefix: "ANTHROPIC_DEFAULT_SONNET_MODEL", defaultID: "claude-sonnet-4-6"},
		{alias: "opus", family: "Opus", prefix: "ANTHROPIC_DEFAULT_OPUS_MODEL", defaultID: "claude-opus-4-6"},
		{alias: "haiku", family: "Haiku", prefix: "ANTHROPIC_DEFAULT_HAIKU_MODEL", defaultID: "claude-haiku-4-5"},
		{alias: "fable", family: "Fable", prefix: "ANTHROPIC_DEFAULT_FABLE_MODEL", defaultID: "claude-fable-5"},
	} {
		model := configuredEnv(definition.prefix)
		name := configuredEnv(definition.prefix + "_NAME")
		description := configuredEnv(definition.prefix + "_DESCRIPTION")
		if model == "" {
			model = definition.defaultID
		}
		// Keep labels clean for the workbench. Proxy nicknames like "gpt-5.6-sol"
		// belong in the description, never in the primary model title.
		displayName := "Claude " + definition.family
		if description == "" {
			description = "alias `" + definition.alias + "` → " + model
			if name != "" && !strings.EqualFold(name, model) {
				description += " (" + name + ")"
			}
		}
		aliases = append(aliases, aliasConfig{
			alias: definition.alias, family: definition.family, model: model,
			displayName: displayName, description: description,
		})
	}

	effectiveModel := configuredEnv("ANTHROPIC_MODEL")
	defaultDescription := "Effective Claude Code default from ANTHROPIC_MODEL"
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(settings.Model)
		defaultDescription = "Effective Claude Code default from settings.json"
	}
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(provider.Model)
		defaultDescription = "Default model from the configured Claude provider"
	}

	models := make([]AgentProviderModel, 0, 10)
	seen := make(map[string]int)
	addModel := func(model, displayName, description string, isDefault bool) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		key := strings.ToLower(model)
		if index, exists := seen[key]; exists {
			if displayName != "" && models[index].DisplayName == models[index].Model {
				models[index].DisplayName = displayName
			}
			if description != "" && models[index].Description == "" {
				models[index].Description = description
			}
			if isDefault {
				for i := range models {
					models[i].IsDefault = false
				}
				models[index].IsDefault = true
			}
			return
		}
		if isDefault {
			for i := range models {
				models[i].IsDefault = false
			}
		}
		if displayName == "" {
			displayName = model
		}
		seen[key] = len(models)
		models = append(models, AgentProviderModel{
			Model: model, DisplayName: displayName, Description: description, IsDefault: isDefault,
			ContextWindow: knownProviderContextWindow("claude", model),
		})
	}
	presentation := func(model string) (string, string) {
		for _, alias := range aliases {
			if strings.EqualFold(model, alias.alias) {
				return alias.displayName, alias.description
			}
			if alias.model != "" && strings.EqualFold(model, alias.model) {
				return alias.displayName, alias.description
			}
		}
		return claudeFamilyDisplayName(model), ""
	}
	keepClaudeModel := func(model string) bool {
		lower := strings.ToLower(strings.TrimSpace(model))
		if lower == "" {
			return false
		}
		// Never keep Codex-proxy mashups like "gpt-5.6-sol · claude-opus-4-8".
		if strings.ContainsAny(lower, "·•|") || strings.Contains(lower, " gpt") {
			return false
		}
		if lower == "sonnet" || lower == "opus" || lower == "haiku" || lower == "fable" {
			return true
		}
		if strings.Contains(lower, "claude") || strings.Contains(lower, "sonnet") || strings.Contains(lower, "opus") || strings.Contains(lower, "haiku") || strings.Contains(lower, "fable") {
			// Drop OpenAI-style proxy nicknames that pollute the Claude Code catalog.
			if strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4") || strings.Contains(lower, "codex") {
				return false
			}
			return true
		}
		return false
	}

	coveredByAlias := func(model string) bool {
		lower := strings.ToLower(strings.TrimSpace(model))
		for _, alias := range aliases {
			if strings.EqualFold(alias.alias, model) || (alias.model != "" && strings.EqualFold(alias.model, model)) {
				return true
			}
			family := strings.ToLower(alias.family)
			if family != "" && strings.Contains(lower, family) {
				return true
			}
		}
		return false
	}

	// Prefer clean Claude Code aliases first.
	for _, alias := range aliases {
		addModel(alias.alias, alias.displayName, alias.description, strings.EqualFold(alias.alias, effectiveModel) || (alias.model != "" && strings.EqualFold(alias.model, effectiveModel)))
		if index, ok := seen[strings.ToLower(alias.alias)]; ok {
			models[index].ContextWindow = knownProviderContextWindow("claude", alias.model)
		}
	}
	if effectiveModel != "" && keepClaudeModel(effectiveModel) {
		if coveredByAlias(effectiveModel) {
			// Prefer the short Claude Code alias instead of duplicating full model IDs.
			for _, alias := range aliases {
				if strings.EqualFold(alias.alias, effectiveModel) || (alias.model != "" && strings.EqualFold(alias.model, effectiveModel)) || strings.Contains(strings.ToLower(effectiveModel), strings.ToLower(alias.family)) {
					addModel(alias.alias, alias.displayName, alias.description, true)
					break
				}
			}
		} else {
			displayName, _ := presentation(effectiveModel)
			addModel(effectiveModel, displayName, defaultDescription, true)
		}
	}
	for _, model := range settings.AvailableModels {
		if !keepClaudeModel(model) || coveredByAlias(model) {
			continue
		}
		displayName, description := presentation(model)
		if description == "" {
			description = "Allowed by Claude Code availableModels configuration"
		}
		addModel(model, displayName, description, strings.EqualFold(model, effectiveModel))
	}
	if settings.Model != "" && keepClaudeModel(settings.Model) && !coveredByAlias(settings.Model) {
		displayName, description := presentation(settings.Model)
		if description == "" {
			description = "Configured in Claude Code settings.json"
		}
		addModel(settings.Model, displayName, description, strings.EqualFold(settings.Model, effectiveModel))
	}
	if provider.Model != "" && keepClaudeModel(provider.Model) && !coveredByAlias(provider.Model) {
		addModel(provider.Model, claudeFamilyDisplayName(provider.Model), "Configured by the local Claude provider", strings.EqualFold(provider.Model, effectiveModel))
	}
	overrideModels := make([]string, 0, len(settings.ModelOverrides))
	for model := range settings.ModelOverrides {
		overrideModels = append(overrideModels, model)
	}
	sort.Strings(overrideModels)
	for _, model := range overrideModels {
		if !keepClaudeModel(model) || coveredByAlias(model) {
			continue
		}
		target := strings.TrimSpace(settings.ModelOverrides[model])
		description := "Configured in Claude Code modelOverrides"
		if target != "" {
			description += " → " + target
		}
		addModel(model, claudeFamilyDisplayName(model), description, strings.EqualFold(model, effectiveModel))
	}
	if len(models) == 0 {
		return fallbackProviderModels("claude")
	}
	return models
}

func claudeFamilyDisplayName(model string) string {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "fable"):
		return "Claude Fable"
	case strings.Contains(lower, "opus"):
		return "Claude Opus"
	case strings.Contains(lower, "sonnet"):
		return "Claude Sonnet"
	case strings.Contains(lower, "haiku"):
		return "Claude Haiku"
	default:
		return model
	}
}

func readClaudeSettings(path string) claudeSettingsFile {
	var settings claudeSettingsFile
	readLimitedJSON(path, &settings)
	return settings
}

func readClaudeProvider(path string) claudeProviderFile {
	var provider claudeProviderFile
	readLimitedJSON(path, &provider)
	return provider
}

func readLimitedJSON(path string, target any) bool {
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) > 4*1024*1024 {
		return false
	}
	return json.Unmarshal(payload, target) == nil
}

func readEnvValue(path, key string) string {
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) > 1024*1024 {
		return ""
	}
	prefix := strings.ToUpper(key) + "="
	for _, line := range strings.Split(string(payload), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(trimmed), prefix) {
			continue
		}
		value := strings.TrimSpace(trimmed[len(prefix):])
		return strings.Trim(value, "\"'")
	}
	return ""
}

func readTOMLModel(path string) string {
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) > 4*1024*1024 {
		return ""
	}
	match := tomlModelPattern.FindStringSubmatch(string(payload))
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func readModelIDsFromDirectory(root string, pattern *regexp.Regexp) []string {
	result := make([]string, 0, 8)
	seen := make(map[string]struct{})
	filesRead := 0
	var bytesRead int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if path != root && (name == "node_modules" || name == ".git" || name == "cache") {
				return filepath.SkipDir
			}
			return nil
		}
		if filesRead >= 300 || bytesRead >= 16*1024*1024 {
			return filepath.SkipAll
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".json" && extension != ".jsonl" && extension != ".env" {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 2*1024*1024 {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		filesRead++
		bytesRead += int64(len(payload))
		for _, match := range pattern.FindAllString(string(payload), -1) {
			model := strings.TrimRight(match, ".")
			lower := strings.ToLower(model)
			if strings.Contains(lower, "api-key") || strings.HasSuffix(lower, "-key") {
				continue
			}
			if _, exists := seen[lower]; exists {
				continue
			}
			seen[lower] = struct{}{}
			result = append(result, model)
		}
		return nil
	})
	sort.Strings(result)
	return result
}

func readGrokModelCache(path string) ([]AgentProviderModel, []AgentProviderReasoningEffort) {
	var cache struct {
		Models map[string]struct {
			Info struct {
				ID                      string `json:"id"`
				Model                   string `json:"model"`
				Name                    string `json:"name"`
				Description             string `json:"description"`
				ReasoningEffort         string `json:"reasoning_effort"`
				ContextWindow           int64  `json:"context_window"`
				ContextWindowCamel      int64  `json:"contextWindow"`
				MaxContextLength        int64  `json:"max_context_length"`
				SupportsReasoningEffort bool   `json:"supports_reasoning_effort"`
				ReasoningEfforts        []struct {
					ID          string `json:"id"`
					Value       string `json:"value"`
					Label       string `json:"label"`
					Description string `json:"description"`
					Default     bool   `json:"default"`
				} `json:"reasoning_efforts"`
			} `json:"info"`
		} `json:"models"`
	}
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) > 4*1024*1024 || json.Unmarshal(payload, &cache) != nil {
		return nil, nil
	}
	keys := make([]string, 0, len(cache.Models))
	for key := range cache.Models {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	models := make([]AgentProviderModel, 0, len(keys))
	efforts := make([]AgentProviderReasoningEffort, 0, 4)
	seenEfforts := make(map[string]struct{})
	for index, key := range keys {
		info := cache.Models[key].Info
		model := strings.TrimSpace(info.Model)
		if model == "" {
			model = strings.TrimSpace(info.ID)
		}
		if model == "" {
			model = key
		}
		name := strings.TrimSpace(info.Name)
		if name == "" {
			name = model
		}
		contextWindow := info.ContextWindow
		if contextWindow <= 0 {
			contextWindow = info.ContextWindowCamel
		}
		if contextWindow <= 0 {
			contextWindow = info.MaxContextLength
		}
		if contextWindow <= 0 {
			contextWindow = knownProviderContextWindow("grok", model)
		}
		models = append(models, AgentProviderModel{
			Model: model, DisplayName: name, Description: info.Description, IsDefault: index == 0,
			ContextWindow: contextWindow,
		})
		if !info.SupportsReasoningEffort {
			continue
		}
		for _, option := range info.ReasoningEfforts {
			effort := strings.TrimSpace(option.Value)
			if effort == "" {
				effort = strings.TrimSpace(option.ID)
			}
			if effort == "" {
				continue
			}
			if _, exists := seenEfforts[effort]; exists {
				continue
			}
			seenEfforts[effort] = struct{}{}
			efforts = append(efforts, AgentProviderReasoningEffort{
				Effort: effort, DisplayName: option.Label, Description: option.Description,
				IsDefault: option.Default || effort == info.ReasoningEffort,
			})
		}
	}
	return models, efforts
}

func commandCandidates(name string) []string {
	if runtime.GOOS == "windows" {
		return []string{name + ".exe", name + ".cmd", name + ".bat", name + ".ps1", name}
	}
	return []string{name}
}

// knownCLIRoots returns directories that ship CLI binaries but may be missing from a
// GUI process PATH (Windows Explorer / macOS Finder / Dock launches).
func knownCLIRoots() []string {
	roots := make([]string, 0, 24)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots,
			filepath.Join(home, ".grok", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".yarn", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".local", "share", "pnpm"),
			filepath.Join(home, "Library", "pnpm"), // macOS pnpm
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".local", "share", "mise", "shims"),
		)
		// nvm latest node bin
		nvmRoot := filepath.Join(home, ".nvm", "versions", "node")
		if entries, err := os.ReadDir(nvmRoot); err == nil {
			var best string
			for _, entry := range entries {
				if entry.IsDir() && (best == "" || entry.Name() > best) {
					best = entry.Name()
				}
			}
			if best != "" {
				roots = append(roots, filepath.Join(nvmRoot, best, "bin"))
			}
		}
	}
	// Windows user app roots
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		roots = append(roots,
			filepath.Join(local, "Programs"),
			filepath.Join(local, "Programs", "nodejs"),
			filepath.Join(local, "pnpm"),
			filepath.Join(local, "Yarn", "bin"),
		)
	}
	// A Wails process launched from Explorer does not inherit the interactive
	// shell PATH. Node's MSI installer places node.exe here by default.
	for _, programFiles := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if strings.TrimSpace(programFiles) != "" {
			roots = append(roots, filepath.Join(programFiles, "nodejs"))
		}
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		roots = append(roots, filepath.Join(appData, "npm"))
	}
	// macOS / Linux system package managers
	roots = append(roots,
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/home/linuxbrew/.linuxbrew/bin",
		"/snap/bin",
	)
	if grokHome := strings.TrimSpace(os.Getenv("GROK_HOME")); grokHome != "" {
		roots = append(roots, filepath.Join(grokHome, "bin"))
	}
	if pnpmHome := strings.TrimSpace(os.Getenv("PNPM_HOME")); pnpmHome != "" {
		roots = append(roots, pnpmHome)
	}
	return roots
}

func findCommand(candidates []string) string {
	// Ensure PATH includes platform-specific Node/CLI roots before LookPath.
	codex.EnrichPathForLookups()

	// Walk every PATH entry. exec.LookPath only returns the first match, which is
	// wrong when an earlier pnpm global stub (postinstall not run) shadows a later
	// working npm install of the same CLI.
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		for _, candidate := range candidates {
			full := filepath.Join(dir, candidate)
			info, err := os.Stat(full)
			if err != nil || info.IsDir() || !usableCommandPath(full) {
				continue
			}
			if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
				continue
			}
			absolute, absoluteErr := filepath.Abs(full)
			if absoluteErr == nil {
				return absolute
			}
			return full
		}
	}

	// Fallback for names that only resolve via PATHEXT / relative LookPath.
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err == nil && usableCommandPath(path) {
			absolute, absoluteErr := filepath.Abs(path)
			if absoluteErr == nil {
				return absolute
			}
			return path
		}
	}

	// GUI apps often miss user PATH entries (Windows npm, macOS Homebrew/nvm, ~/.grok/bin).
	for _, root := range knownCLIRoots() {
		for _, candidate := range candidates {
			full := filepath.Join(root, candidate)
			info, err := os.Stat(full)
			if err != nil || info.IsDir() || !usableCommandPath(full) {
				continue
			}
			// Unix: require executable bit when present.
			if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
				continue
			}
			absolute, absoluteErr := filepath.Abs(full)
			if absoluteErr == nil {
				return absolute
			}
			return full
		}
	}
	return ""
}

func usableCommandPath(path string) bool {
	if runtime.GOOS != "windows" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".exe" || ext == "" {
		return isWindowsExecutable(path)
	}
	if ext != ".cmd" && ext != ".bat" && ext != ".ps1" {
		return true
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pattern := windowsProviderShimTargetPattern
	if ext == ".ps1" {
		pattern = windowsPowerShellShimTargetPattern
	}
	matches := pattern.FindAllSubmatch(content, -1)
	for index := len(matches) - 1; index >= 0; index-- {
		if len(matches[index]) < 2 {
			continue
		}
		relative := filepath.FromSlash(strings.ReplaceAll(string(matches[index][1]), `\`, "/"))
		target := filepath.Clean(filepath.Join(filepath.Dir(path), relative))
		if strings.EqualFold(filepath.Ext(target), ".exe") {
			return isWindowsExecutable(target)
		}
		if info, statErr := os.Stat(target); statErr == nil && !info.IsDir() {
			return true
		}
	}
	// Non-package-manager batch/PowerShell scripts may not contain a relative
	// target. Keep supporting them and let the normal probe report any error.
	return len(matches) == 0
}

func isWindowsExecutable(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var header [2]byte
	if _, err := file.Read(header[:]); err != nil {
		return false
	}
	return header[0] == 'M' && header[1] == 'Z'
}

func runProbeCommand(executable string, args []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command, commandArgs, resolveErr := providerCommand(executable, args)
	if resolveErr != nil {
		return "", resolveErr
	}
	cmd := exec.CommandContext(ctx, command, commandArgs...)
	output, err := runManagedCombinedOutput(ctx, cmd)
	value := strings.TrimSpace(ansiEscapePattern.ReplaceAllString(string(output), ""))
	if len(value) > 4096 {
		value = value[:4096]
	}
	if ctx.Err() != nil {
		return value, ctx.Err()
	}
	return value, err
}

func providerCommand(executable string, args []string) (string, []string, error) {
	if runtime.GOOS != "windows" {
		return executable, args, nil
	}
	switch strings.ToLower(filepath.Ext(executable)) {
	case ".cmd", ".bat":
		return resolveWindowsProviderShim(executable, args)
	case ".ps1":
		command := findCommand([]string{"powershell.exe", "pwsh.exe"})
		if command == "" {
			return "", nil, errors.New("PowerShell is required to run this provider")
		}
		return command, append([]string{
			"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", executable,
		}, args...), nil
	default:
		return executable, args, nil
	}
}

func resolveWindowsProviderShim(executable string, args []string) (string, []string, error) {
	content, err := os.ReadFile(executable)
	if err != nil {
		return "", nil, err
	}
	matches := windowsProviderShimTargetPattern.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return "", nil, fmt.Errorf("unsupported Windows command shim: %s", filepath.Base(executable))
	}
	// npm/pnpm shims often mention an optional local node.exe before the actual
	// package entrypoint. Walk backwards so the invoked package target wins.
	for index := len(matches) - 1; index >= 0; index-- {
		match := matches[index]
		if len(match) < 2 {
			continue
		}
		relative := filepath.FromSlash(strings.ReplaceAll(string(match[1]), `\`, `/`))
		target := filepath.Clean(filepath.Join(filepath.Dir(executable), relative))
		if info, statErr := os.Stat(target); statErr != nil || info.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(target)) {
		case ".js", ".cjs", ".mjs":
			node := findCommand([]string{"node.exe", "node"})
			if node == "" {
				return "", nil, errors.New("Node.js is required to run this provider")
			}
			return node, append([]string{target}, args...), nil
		default:
			return target, args, nil
		}
	}
	return "", nil, fmt.Errorf("provider command target was not found: %s", filepath.Base(executable))
}

func containsConfigurationError(output string) bool {
	value := strings.ToLower(output)
	return strings.Contains(value, "invalid configuration") || strings.Contains(value, "please fix the configuration")
}

func conciseProbeError(output string, err error) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(line), "invalid configuration") {
			return line
		}
	}
	if err != nil {
		return err.Error()
	}
	if output != "" {
		first, _, _ := strings.Cut(output, "\n")
		return strings.TrimSpace(first)
	}
	return "CLI health check failed"
}

func firstOutputLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 120 {
			return line[:120]
		}
		return line
	}
	return ""
}

func providerStatus(installed bool, healthy bool, runtimeReady bool) string {
	switch {
	case !installed:
		return "not-installed"
	case runtimeReady:
		return "ready"
	case healthy:
		return "adapter-pending"
	default:
		return "configuration-error"
	}
}
