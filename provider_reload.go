package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type RuntimeReloadResult struct {
	Configuration ProviderConfigurationView `json:"configuration"`
	Model         string                    `json:"model"`
	ModelProvider string                    `json:"modelProvider"`
	Effort        string                    `json:"effort"`
	CodexModels   map[string]any            `json:"codexModels"`
}

// ReloadRuntimeConfiguration is an explicit reconnect, unlike the read-only
// configuration inspector. Other CLIs launch fresh processes for each turn.
func (s *AppService) ReloadRuntimeConfiguration(providerID string) (RuntimeReloadResult, error) {
	providerID = normalizeProviderID(providerID)
	if providerID == "" {
		return RuntimeReloadResult{}, errors.New("不支持的服务商")
	}
	s.mu.Lock()
	if s.providerReloading[providerID] || s.providerHasActiveWorkLocked(providerID) {
		s.mu.Unlock()
		return RuntimeReloadResult{}, errors.New("该服务商正在运行任务或重新加载配置，请完成或停止任务后重试")
	}
	if s.providerReloading == nil {
		s.providerReloading = make(map[string]bool)
	}
	s.providerReloading[providerID] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.providerReloading, providerID); s.mu.Unlock() }()
	if err := validateProviderConfigurationFile(providerID); err != nil {
		return RuntimeReloadResult{}, err
	}
	var applied ProviderApplyResult
	var err error
	if providerID == "codex" {
		applied, err = s.RestartProvider(providerID)
	} else {
		applied, err = s.ReloadProviderConfiguration(providerID)
	}
	if err != nil {
		return RuntimeReloadResult{}, err
	}
	result := RuntimeReloadResult{Configuration: applied.Configuration}
	for _, model := range applied.Configuration.Runtime.Models {
		if model.IsDefault {
			result.Model = model.Model
			break
		}
	}
	switch providerID {
	case "codex":
		configuration, err := s.call("config/read", map[string]any{"cwd": s.Settings().Workspace, "includeLayers": false})
		if err != nil {
			return result, err
		}
		config, _ := configuration["config"].(map[string]any)
		result.Model = strings.TrimSpace(firstMapString(config, "model"))
		result.ModelProvider = strings.TrimSpace(firstMapString(config, "model_provider"))
		if result.ModelProvider == "" {
			result.ModelProvider = "openai"
		}
		result.Effort = firstMapString(config, "model_reasoning_effort")
		result.CodexModels, err = s.listModels(true)
		if err != nil {
			return result, err
		}
		models, _ := result.CodexModels["data"].([]any)
		for _, raw := range models {
			model, _ := raw.(map[string]any)
			isDefault, _ := model["isDefault"].(bool)
			id := firstMapString(model, "model", "id")
			if result.Model == "" && isDefault {
				result.Model = id
			}
			if result.Model == id && result.Effort == "" {
				result.Effort = firstMapString(model, "defaultReasoningEffort")
			}
		}
		if result.Model == "" {
			return result, errors.New("Codex 已重启，但未返回配置模型或默认模型，请检查登录和模型权限后重试")
		}
	case "gemini", "opencode":
		catalog, err := s.ReadExternalRuntimeCatalog(providerID, s.currentRuntimeWorkspace(providerID))
		if err != nil {
			return result, err
		}
		result.Configuration.Runtime.Models = catalog.Models
		result.Model = catalog.DefaultModel
	case "claude":
		result.Model, err = claudeModelForReload(s.currentRuntimeWorkspace(providerID))
		if err != nil {
			return result, err
		}
	case "grok":
		if normalizeGrokBackend(s.Settings().GrokBackend) == grokBackendAPI {
			// API backend has no CLI model setting; retain the separately configured API model.
			result.Model = s.Settings().GrokAPIModel
		}
	}
	if providerID != "codex" && result.Model != "" {
		found := false
		for index := range result.Configuration.Runtime.Models {
			model := &result.Configuration.Runtime.Models[index]
			model.IsDefault = strings.EqualFold(model.Model, result.Model)
			found = found || model.IsDefault
		}
		if !found {
			result.Configuration.Runtime.Models = append(result.Configuration.Runtime.Models, AgentProviderModel{
				Model: result.Model, DisplayName: result.Model, IsDefault: true,
				Description: "Current native configuration", ContextWindow: knownProviderContextWindow(providerID, result.Model),
			})
		}
	}
	return result, nil
}

// Read native model settings again instead of retaining NiceCodex's previous
// --model override or a provider-manager catalog entry after an auth switch.
func claudeModelForReload(workspace string) (string, error) {
	paths := []string{filepath.Join(resolveClaudeHome(), "settings.json")}
	if workspace != "" {
		paths = append(paths, filepath.Join(workspace, ".claude", "settings.json"), filepath.Join(workspace, ".claude", "settings.local.json"))
	}
	model, environmentModel := "", ""
	for _, path := range paths {
		config, err := readProviderJSONConfig(path)
		if err != nil {
			return "", err
		}
		if value, ok := config["model"].(string); ok {
			model = strings.TrimSpace(value)
		}
		if value, ok := mapFromAny(config["env"])["ANTHROPIC_MODEL"].(string); ok {
			environmentModel = strings.TrimSpace(value)
		}
	}
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")); value != "" {
		return value, nil
	}
	if environmentModel != "" {
		return environmentModel, nil
	}
	if model != "" {
		return model, nil
	}
	return "default", nil
}
