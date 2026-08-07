package main

// Native external-runtime catalog support.
//
// Gemini CLI and OpenCode deliberately keep their own configuration, history,
// provider and usage formats. This file is the bridge used by the UI; it never
// maps those values into Codex's model-provider settings.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var openCodeCatalogCache = struct {
	sync.Mutex
	expiresAt time.Time
	models    []AgentProviderModel
	efforts   []AgentProviderReasoningEffort
	providers []ExternalProviderView
}{}

type ExternalProviderView struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Source        string               `json:"source"`
	Configured    bool                 `json:"configured"`
	Authenticated bool                 `json:"authenticated"`
	BaseURL       string               `json:"baseUrl,omitempty"`
	Models        []AgentProviderModel `json:"models"`
}

type ExternalMCPServerView struct {
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Type       string `json:"type"`
	Command    string `json:"command"`
	Args       string `json:"args"`
	URL        string `json:"url"`
	Transport  string `json:"transport"`
	ConfigPath string `json:"configPath"`
	Source     string `json:"source"`
}

type ExternalSessionView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Preview   string `json:"preview"`
	Workspace string `json:"workspace"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Native    bool   `json:"native"`
}

type ExternalUsageModelView struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Sessions     int64   `json:"sessions"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	Reasoning    int64   `json:"reasoningTokens"`
	CachedTokens int64   `json:"cachedTokens"`
	TotalTokens  int64   `json:"totalTokens"`
	Cost         float64 `json:"cost"`
}

type ExternalUsageSummary struct {
	RangeDays    int64                    `json:"rangeDays"`
	Sessions     int64                    `json:"sessions"`
	Messages     int64                    `json:"messages"`
	InputTokens  int64                    `json:"inputTokens"`
	OutputTokens int64                    `json:"outputTokens"`
	Reasoning    int64                    `json:"reasoningTokens"`
	CachedTokens int64                    `json:"cachedTokens"`
	TotalTokens  int64                    `json:"totalTokens"`
	Cost         float64                  `json:"cost"`
	ByModel      []ExternalUsageModelView `json:"byModel"`
	Source       string                   `json:"source"`
}

type ExternalRuntimeCatalog struct {
	Runtime             string                  `json:"runtime"`
	Workspace           string                  `json:"workspace"`
	NativeHome          string                  `json:"nativeHome"`
	ConfigPath          string                  `json:"configPath"`
	ActiveProvider      string                  `json:"activeProvider"`
	DefaultModel        string                  `json:"defaultModel"`
	ProviderSource      string                  `json:"providerSource"`
	Providers           []ExternalProviderView  `json:"providers"`
	Models              []AgentProviderModel    `json:"models"`
	MCP                 []ExternalMCPServerView `json:"mcp"`
	GlobalInstructions  GlobalInstructionsInfo  `json:"globalInstructions"`
	ProjectInstructions ProjectInstructionsInfo `json:"projectInstructions"`
	ConfigInstructions  string                  `json:"configInstructions,omitempty"`
	Sessions            []ExternalSessionView   `json:"sessions"`
	Usage               ExternalUsageSummary    `json:"usage"`
	ReadOnlyNotice      string                  `json:"readOnlyNotice,omitempty"`
}

type ExternalInstructionsSaveRequest struct {
	Runtime   string `json:"runtime"`
	Workspace string `json:"workspace"`
	Scope     string `json:"scope"` // global | project
	Content   string `json:"content"`
}

type ExternalMCPJSONSaveRequest struct {
	Runtime   string `json:"runtime"`
	Workspace string `json:"workspace"`
	Scope     string `json:"scope"` // global | project
	JSON      string `json:"json"`
}

func normalizeExternalRuntime(runtime string) string {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "gemini", "gemini-cli":
		return "gemini"
	case "opencode", "opencode-cli", "open-code":
		return "opencode"
	default:
		return ""
	}
}

func (s *AppService) ReadExternalRuntimeCatalog(runtime, workspace string) (ExternalRuntimeCatalog, error) {
	return s.readExternalRuntimeCatalog(runtime, workspace, true)
}

// readExternalRuntimeCatalog lets callers explicitly control whether an empty
// workspace should fall back to the active project. The public catalog keeps
// that fallback for project-scoped history/configuration.
func (s *AppService) readExternalRuntimeCatalog(runtime, workspace string, useCurrentWorkspace bool) (ExternalRuntimeCatalog, error) {
	runtime = normalizeExternalRuntime(runtime)
	if runtime == "" {
		return ExternalRuntimeCatalog{}, errors.New("unsupported external runtime")
	}
	if useCurrentWorkspace && strings.TrimSpace(workspace) == "" {
		workspace = s.currentRuntimeWorkspace(runtime)
	}
	workspace = strings.TrimSpace(workspace)
	if workspace != "" {
		clean, err := validateWorkspace(workspace)
		if err != nil {
			return ExternalRuntimeCatalog{}, err
		}
		workspace = clean
	}
	home, _ := os.UserHomeDir()
	catalog := ExternalRuntimeCatalog{
		Runtime: runtime, Workspace: workspace,
		Providers: []ExternalProviderView{}, Models: []AgentProviderModel{},
		MCP: []ExternalMCPServerView{}, Sessions: []ExternalSessionView{},
		Usage: ExternalUsageSummary{RangeDays: 30, ByModel: []ExternalUsageModelView{}},
	}
	switch runtime {
	case "gemini":
		catalog.NativeHome = filepath.Join(home, ".gemini")
		catalog.ConfigPath = filepath.Join(catalog.NativeHome, "settings.json")
		catalog.ProviderSource = "Gemini CLI settings.json / environment"
		catalog.MCP = readExternalMCPByScope("gemini", catalog.ConfigPath, workspace)
		catalog.GlobalInstructions = readInstructionFile(filepath.Join(catalog.NativeHome, "GEMINI.md"), "gemini-global", "Gemini CLI global GEMINI.md")
		catalog.ProjectInstructions = readProjectInstruction(workspace, "GEMINI.md", "gemini-project", "Gemini CLI project GEMINI.md")
		catalog.Models, _ = discoverProviderCatalog("gemini")
		catalog.DefaultModel = firstGeminiModel(catalog.Models)
		catalog.ActiveProvider = geminiActiveProvider(catalog.ConfigPath)
		catalog.Providers = []ExternalProviderView{{
			ID: "gemini", Name: "Gemini API / OAuth", Source: catalog.ProviderSource,
			Configured: geminiConfigured(catalog.ConfigPath, home), Authenticated: geminiAuthenticated(catalog.ConfigPath, home),
			Models: catalog.Models,
		}}
		catalog.Sessions = listGeminiNativeSessions(catalog.NativeHome, workspace)
		catalog.Usage = collectGeminiUsage(catalog.NativeHome, workspace)
		catalog.ReadOnlyNotice = "模型、认证、MCP 和指令由 Gemini CLI 原生配置加载。"
	case "opencode":
		catalog.NativeHome = openCodeConfigDir(home)
		catalog.ConfigPath = openCodeConfigPath(home)
		catalog.ProviderSource = "OpenCode providers/models CLI and opencode.json"
		catalog.Models, _, catalog.Providers = discoverOpenCodeCatalog(home)
		catalog.ActiveProvider, catalog.DefaultModel, catalog.ConfigInstructions = readOpenCodeConfig(catalog.ConfigPath)
		catalog.MCP = readExternalMCPByScope("opencode", catalog.ConfigPath, workspace)
		catalog.GlobalInstructions = readInstructionFile(filepath.Join(catalog.NativeHome, "AGENTS.md"), "opencode-global", "OpenCode global AGENTS.md")
		catalog.ProjectInstructions = readOpenCodeProjectInstructions(workspace)
		catalog.Sessions = listOpenCodeNativeSessions(workspace)
		catalog.Usage = collectOpenCodeUsage(home, workspace, 30)
		catalog.ReadOnlyNotice = "provider、model、MCP、指令、历史和 usage 均来自 OpenCode 原生配置/数据库。"
	}
	return catalog, nil
}

func (s *AppService) currentRuntimeWorkspace(runtime string) string {
	settings := s.Settings()
	if runtime == "gemini" || runtime == "opencode" {
		return strings.TrimSpace(settings.Workspace)
	}
	return ""
}

func (s *AppService) SaveExternalRuntimeInstructions(request ExternalInstructionsSaveRequest) error {
	runtime := normalizeExternalRuntime(request.Runtime)
	if runtime == "" {
		return errors.New("unsupported external runtime")
	}
	scope := strings.ToLower(strings.TrimSpace(request.Scope))
	if scope != "global" && scope != "project" {
		return errors.New("instruction scope must be global or project")
	}
	home, _ := os.UserHomeDir()
	var path string
	if scope == "global" {
		if runtime == "gemini" {
			path = filepath.Join(home, ".gemini", "GEMINI.md")
		} else {
			path = filepath.Join(openCodeConfigDir(home), "AGENTS.md")
		}
	} else {
		workspace, err := validateWorkspace(strings.TrimSpace(request.Workspace))
		if err != nil {
			return err
		}
		path = externalProjectInstructionPath(runtime, workspace)
	}
	return writeTextFileAtomic(path, request.Content)
}

func (s *AppService) SaveExternalRuntimeMCP(request ExternalMCPJSONSaveRequest) error {
	runtime := normalizeExternalRuntime(request.Runtime)
	if runtime == "" {
		return errors.New("unsupported external runtime")
	}
	scope := strings.ToLower(strings.TrimSpace(request.Scope))
	if scope != "project" {
		scope = "global"
	}
	var root map[string]any
	if strings.TrimSpace(request.JSON) == "" {
		return errors.New("MCP JSON is required")
	}
	if err := json.Unmarshal([]byte(request.JSON), &root); err != nil {
		return fmt.Errorf("invalid MCP JSON: %w", err)
	}
	var servers any
	if runtime == "gemini" {
		servers = root["mcpServers"]
		if servers == nil {
			servers = root
		}
	} else {
		servers = root["mcp"]
		if servers == nil {
			servers = root
		}
	}
	if _, ok := servers.(map[string]any); !ok {
		return errors.New("MCP JSON must be an object keyed by server name")
	}
	workspace := strings.TrimSpace(request.Workspace)
	if scope == "project" {
		var err error
		workspace, err = validateWorkspace(workspace)
		if err != nil {
			return err
		}
	}
	path := externalRuntimeConfigPath(runtime, scope, workspace)
	config := map[string]any{}
	_ = readLimitedJSON(path, &config)
	// Preserve fields that are not represented by the compact editor (for
	// example env, headers and custom transport options). This keeps a normal
	// load -> save cycle from silently deleting native MCP credentials.
	key := "mcp"
	if runtime == "gemini" {
		key = "mcpServers"
	}
	config[key] = mergeExternalMCPServers(config[key], servers)
	payload, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writeTextFileAtomic(path, string(payload)+"\n")
}

func instructionFileName(runtime string) string {
	if runtime == "gemini" {
		return "GEMINI.md"
	}
	return "AGENTS.md"
}

func readInstructionFile(path, source, label string) GlobalInstructionsInfo {
	payload, err := os.ReadFile(path)
	if err != nil {
		return GlobalInstructionsInfo{Path: path, Source: source, Available: true}
	}
	return GlobalInstructionsInfo{Content: string(payload), Path: path, Source: label, Exists: true, EmptyFile: len(strings.TrimSpace(string(payload))) == 0, Available: true}
}

func readProjectInstruction(workspace, name, source, label string) ProjectInstructionsInfo {
	info := ProjectInstructionsInfo{Workspace: workspace, WorkspaceName: filepath.Base(workspace), Source: source, Available: workspace != ""}
	if workspace == "" {
		return info
	}
	path := filepath.Join(workspace, name)
	global := readInstructionFile(path, source, label)
	info.Content, info.Path, info.Source = global.Content, global.Path, global.Source
	info.Exists, info.EmptyFile, info.Available = global.Exists, global.EmptyFile, global.Available
	return info
}

func readOpenCodeProjectInstructions(workspace string) ProjectInstructionsInfo {
	path := externalProjectInstructionPath("opencode", workspace)
	info := readInstructionFile(path, "opencode-project", "OpenCode project AGENTS.md")
	return ProjectInstructionsInfo{
		Content: info.Content, Workspace: workspace, WorkspaceName: filepath.Base(workspace), Path: info.Path,
		Source: info.Source, Exists: info.Exists, EmptyFile: info.EmptyFile, Available: workspace != "",
	}
}

func externalProjectInstructionPath(runtime, workspace string) string {
	if runtime == "opencode" {
		candidates := []string{
			filepath.Join(workspace, ".opencode", "AGENTS.md"),
			filepath.Join(workspace, "AGENTS.md"),
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
		return candidates[0]
	}
	return filepath.Join(workspace, instructionFileName(runtime))
}

func geminiActiveProvider(configPath string) string {
	var config map[string]any
	if !readLimitedJSON(configPath, &config) {
		return "gemini"
	}
	if security, ok := config["security"].(map[string]any); ok {
		if auth, ok := security["auth"].(map[string]any); ok {
			if selected, ok := auth["selectedType"].(string); ok && strings.TrimSpace(selected) != "" {
				return selected
			}
		}
	}
	return "gemini"
}

func geminiConfigured(configPath, home string) bool {
	if readEnvValue(filepath.Join(home, ".gemini", ".env"), "GEMINI_API_KEY") != "" || strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" {
		return true
	}
	var config map[string]any
	if !readLimitedJSON(configPath, &config) {
		return false
	}
	if security, ok := config["security"].(map[string]any); ok {
		if auth, ok := security["auth"].(map[string]any); ok {
			return strings.TrimSpace(stringFromAny(auth["selectedType"])) != ""
		}
	}
	return false
}

func geminiAuthenticated(configPath, home string) bool {
	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" || readEnvValue(filepath.Join(home, ".gemini", ".env"), "GEMINI_API_KEY") != "" {
		return true
	}
	var accounts map[string]any
	return readLimitedJSON(filepath.Join(home, ".gemini", "google_accounts.json"), &accounts) && len(accounts) > 0
}

func firstGeminiModel(models []AgentProviderModel) string {
	for _, model := range models {
		if model.IsDefault {
			return model.Model
		}
	}
	if len(models) > 0 {
		return models[0].Model
	}
	return "gemini-2.5-pro"
}

func readOpenCodeConfig(path string) (activeProvider, defaultModel, instructions string) {
	var config map[string]any
	if !readLimitedJSON(path, &config) {
		return "", "", ""
	}
	if model, ok := config["model"].(string); ok {
		defaultModel = strings.TrimSpace(model)
		activeProvider = providerFromModelReference(defaultModel)
	}
	if activeProvider == "" {
		if provider, ok := config["provider"].(map[string]any); ok {
			keys := make([]string, 0, len(provider))
			for key := range provider {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			// A provider object is a catalog/configuration map, not an active
			// selection. Only use it as a fallback when the native model setting is
			// absent, and prefer a sole configured provider over alphabetical order.
			if len(keys) == 1 {
				activeProvider = keys[0]
			}
		}
	}
	if raw, ok := config["instructions"].([]any); ok {
		parts := make([]string, 0, len(raw))
		for _, item := range raw {
			if value, ok := item.(string); ok {
				parts = append(parts, value)
			}
		}
		instructions = strings.Join(parts, "\n")
	} else if raw, ok := config["instructions"].(string); ok {
		instructions = strings.TrimSpace(raw)
	}
	return activeProvider, defaultModel, instructions
}

func providerFromModelReference(model string) string {
	model = strings.TrimSpace(model)
	if index := strings.IndexByte(model, '/'); index > 0 {
		return strings.TrimSpace(model[:index])
	}
	return ""
}

func readGeminiMCP(path string) []ExternalMCPServerView {
	var config map[string]any
	if !readLimitedJSON(path, &config) {
		return []ExternalMCPServerView{}
	}
	return mcpViewsFromMap(config["mcpServers"], path, "gemini")
}

func readOpenCodeMCP(path string) []ExternalMCPServerView {
	var config map[string]any
	if !readLimitedJSON(path, &config) {
		return []ExternalMCPServerView{}
	}
	return mcpViewsFromMap(config["mcp"], path, "opencode")
}

func externalRuntimeConfigPath(runtime, scope, workspace string) string {
	home, _ := os.UserHomeDir()
	if scope != "project" || strings.TrimSpace(workspace) == "" {
		if runtime == "gemini" {
			return filepath.Join(home, ".gemini", "settings.json")
		}
		return openCodeConfigPath(home)
	}
	if runtime == "gemini" {
		return filepath.Join(workspace, ".gemini", "settings.json")
	}
	for _, candidate := range []string{
		filepath.Join(workspace, ".opencode", "opencode.json"),
		filepath.Join(workspace, "opencode.json"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(workspace, "opencode.json")
}

func openCodeConfigDir(home string) string {
	if configFile := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); configFile != "" {
		return filepath.Dir(configFile)
	}
	if configDir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); configDir != "" {
		return configDir
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

func openCodeConfigPath(home string) string {
	if configFile := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); configFile != "" {
		return configFile
	}
	return filepath.Join(openCodeConfigDir(home), "opencode.json")
}

func readExternalMCPByScope(runtime, globalPath, workspace string) []ExternalMCPServerView {
	read := readGeminiMCP
	if runtime == "opencode" {
		read = readOpenCodeMCP
	}
	global := read(globalPath)
	if strings.TrimSpace(workspace) == "" {
		return global
	}
	projectPath := externalRuntimeConfigPath(runtime, "project", workspace)
	project := read(projectPath)
	if len(project) == 0 {
		return global
	}
	merged := make([]ExternalMCPServerView, 0, len(global)+len(project))
	byName := make(map[string]int, len(global)+len(project))
	for _, item := range global {
		byName[strings.ToLower(item.Name)] = len(merged)
		merged = append(merged, item)
	}
	for _, item := range project {
		key := strings.ToLower(item.Name)
		if index, ok := byName[key]; ok {
			merged[index] = item
		} else {
			byName[key] = len(merged)
			merged = append(merged, item)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return strings.ToLower(merged[i].Name) < strings.ToLower(merged[j].Name) })
	return merged
}

func mcpViewsFromMap(raw any, path, runtime string) []ExternalMCPServerView {
	servers, ok := raw.(map[string]any)
	if !ok {
		return []ExternalMCPServerView{}
	}
	keys := make([]string, 0, len(servers))
	for key := range servers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]ExternalMCPServerView, 0, len(keys))
	for _, name := range keys {
		entry, _ := servers[name].(map[string]any)
		view := ExternalMCPServerView{Name: name, Enabled: true, Type: "local", ConfigPath: path, Source: runtime}
		if value, ok := entry["enabled"].(bool); ok {
			view.Enabled = value
		}
		if value, ok := entry["type"].(string); ok && value != "" {
			view.Type = value
		}
		if value, ok := entry["command"].(string); ok {
			view.Command = value
		}
		if value, ok := entry["url"].(string); ok {
			view.URL = value
		}
		if value, ok := entry["httpUrl"].(string); ok && view.URL == "" {
			view.URL = value
		}
		if command, ok := entry["command"].([]any); ok {
			parts := make([]string, 0, len(command))
			for _, arg := range command {
				if value, ok := arg.(string); ok {
					parts = append(parts, value)
				}
			}
			if len(parts) > 0 {
				view.Command = parts[0]
				if len(parts) > 1 {
					view.Args = strings.Join(parts[1:], " ")
				}
			}
		}
		if rawArgs, ok := entry["args"].([]any); ok && view.Args == "" {
			parts := make([]string, 0, len(rawArgs))
			for _, arg := range rawArgs {
				if value, ok := arg.(string); ok {
					parts = append(parts, value)
				}
			}
			view.Args = strings.Join(parts, " ")
		}
		if transport, ok := entry["transport"].(string); ok {
			view.Transport = transport
		} else if view.URL != "" {
			view.Transport = "http"
		} else {
			view.Transport = "stdio"
		}
		result = append(result, view)
	}
	return result
}

func mergeExternalMCPServers(existingRaw, incomingRaw any) map[string]any {
	existing, _ := existingRaw.(map[string]any)
	incoming, _ := incomingRaw.(map[string]any)
	merged := make(map[string]any, len(incoming))
	for name, raw := range incoming {
		incomingEntry, incomingOK := raw.(map[string]any)
		if !incomingOK {
			merged[name] = raw
			continue
		}
		entry := make(map[string]any, len(incomingEntry))
		if oldEntry, ok := existing[name].(map[string]any); ok {
			for key, value := range oldEntry {
				entry[key] = value
			}
		}
		for key, value := range incomingEntry {
			// The compact editor omits empty optional values. Retain the native
			// value in that case, but honor explicit false/zero values.
			if value == nil {
				continue
			}
			entry[key] = value
		}
		if urlValue, ok := incomingEntry["url"].(string); ok && strings.TrimSpace(urlValue) != "" {
			delete(entry, "command")
			delete(entry, "args")
		} else if commandValue, ok := incomingEntry["command"].(string); ok && strings.TrimSpace(commandValue) != "" {
			delete(entry, "url")
			delete(entry, "httpUrl")
		}
		merged[name] = entry
	}
	return merged
}

func discoverOpenCodeCatalog(home string) ([]AgentProviderModel, []AgentProviderReasoningEffort, []ExternalProviderView) {
	openCodeCatalogCache.Lock()
	if time.Now().Before(openCodeCatalogCache.expiresAt) {
		models := append([]AgentProviderModel(nil), openCodeCatalogCache.models...)
		efforts := append([]AgentProviderReasoningEffort(nil), openCodeCatalogCache.efforts...)
		providers := append([]ExternalProviderView(nil), openCodeCatalogCache.providers...)
		openCodeCatalogCache.Unlock()
		return models, efforts, providers
	}
	openCodeCatalogCache.Unlock()
	executable := findCommand(commandCandidates("opencode"))
	if executable == "" {
		return nil, nil, nil
	}
	output, err := runExternalCLIOutput(executable, nil, []string{"models", "--verbose"}, "", 8*time.Second)
	if err != nil {
		return nil, nil, nil
	}
	var models []AgentProviderModel
	seen := map[string]bool{}
	providerModels := map[string][]AgentProviderModel{}
	for _, raw := range scanJSONObjects(output) {
		var item struct {
			ID         string         `json:"id"`
			ProviderID string         `json:"providerID"`
			Name       string         `json:"name"`
			Status     string         `json:"status"`
			Limit      map[string]any `json:"limit"`
			Variants   map[string]any `json:"variants"`
		}
		if json.Unmarshal([]byte(raw), &item) != nil || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ProviderID) == "" {
			continue
		}
		id := strings.TrimSpace(item.ProviderID) + "/" + strings.TrimSpace(item.ID)
		if seen[strings.ToLower(id)] {
			continue
		}
		seen[strings.ToLower(id)] = true
		contextWindow := int64FromAny(item.Limit["context"])
		model := AgentProviderModel{Model: id, ProviderID: item.ProviderID, DisplayName: item.Name, Description: "OpenCode " + item.ProviderID + " model", ContextWindow: contextWindow}
		models = append(models, model)
		providerModels[item.ProviderID] = append(providerModels[item.ProviderID], model)
	}
	if len(models) == 0 {
		return nil, nil, nil
	}
	active, configuredDefault, _ := readOpenCodeConfig(openCodeConfigPath(home))
	if configuredDefault != "" {
		for i := range models {
			if strings.EqualFold(models[i].Model, configuredDefault) {
				models[i].IsDefault = true
			}
		}
	}
	if !hasDefaultModel(models) {
		for i := range models {
			if active != "" && strings.EqualFold(models[i].ProviderID, active) {
				models[i].IsDefault = true
				break
			}
		}
	}
	if !hasDefaultModel(models) {
		models[0].IsDefault = true
	}
	efforts := make([]AgentProviderReasoningEffort, 0, 8)
	effortSeen := map[string]bool{}
	for _, raw := range scanJSONObjects(output) {
		var item struct {
			Variants map[string]any `json:"variants"`
		}
		if json.Unmarshal([]byte(raw), &item) != nil {
			continue
		}
		for variant := range item.Variants {
			if effortSeen[variant] {
				continue
			}
			effortSeen[variant] = true
			efforts = append(efforts, AgentProviderReasoningEffort{Effort: variant, DisplayName: strings.Title(variant), Description: "Native OpenCode model variant"})
		}
	}
	sort.SliceStable(efforts, func(i, j int) bool { return efforts[i].Effort < efforts[j].Effort })
	if len(efforts) == 0 {
		efforts = fallbackReasoningEfforts("opencode")
	}
	configuredProviders := map[string]bool{}
	providerBaseURLs := map[string]string{}
	var config map[string]any
	if readLimitedJSON(openCodeConfigPath(home), &config) {
		if provider, ok := config["provider"].(map[string]any); ok {
			for id, raw := range provider {
				configuredProviders[id] = true
				if entry, ok := raw.(map[string]any); ok {
					if value, ok := entry["baseURL"].(string); ok {
						providerBaseURLs[id] = sanitizeExternalBaseURL(value)
					}
					if options, ok := entry["options"].(map[string]any); ok {
						if value, ok := options["baseURL"].(string); ok && providerBaseURLs[id] == "" {
							providerBaseURLs[id] = sanitizeExternalBaseURL(value)
						}
					}
				}
			}
		}
	}
	if providersOutput, providerErr := runExternalCLIOutput(executable, nil, []string{"providers", "list"}, "", 5*time.Second); providerErr == nil {
		clean := ansiEscapePattern.ReplaceAllString(providersOutput, "")
		if strings.Contains(strings.ToLower(clean), "open code go") || strings.Contains(strings.ToLower(clean), "opencode go") {
			configuredProviders["opencode-go"] = true
		}
		if strings.Contains(strings.ToLower(clean), "open code") {
			configuredProviders["opencode"] = true
		}
	}
	providerIDs := make([]string, 0, len(providerModels))
	for id := range providerModels {
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)
	providers := make([]ExternalProviderView, 0, len(providerIDs))
	for _, id := range providerIDs {
		name := id
		switch id {
		case "opencode":
			name = "OpenCode Zen"
		case "opencode-go":
			name = "OpenCode Go"
		}
		providers = append(providers, ExternalProviderView{ID: id, Name: name, Source: "opencode models --verbose", Configured: configuredProviders[id], Authenticated: configuredProviders[id], BaseURL: providerBaseURLs[id], Models: providerModels[id]})
	}
	openCodeCatalogCache.Lock()
	openCodeCatalogCache.models = append([]AgentProviderModel(nil), models...)
	openCodeCatalogCache.efforts = append([]AgentProviderReasoningEffort(nil), efforts...)
	openCodeCatalogCache.providers = append([]ExternalProviderView(nil), providers...)
	openCodeCatalogCache.expiresAt = time.Now().Add(2 * time.Minute)
	openCodeCatalogCache.Unlock()
	return models, efforts, providers
}

func sanitizeExternalBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return truncateRunes(value, 240)
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return truncateRunes(parsed.String(), 240)
}

func hasDefaultModel(models []AgentProviderModel) bool {
	for _, model := range models {
		if model.IsDefault {
			return true
		}
	}
	return false
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}

func listOpenCodeNativeSessions(workspace string) []ExternalSessionView {
	query := "select id, directory, title, model, time_created, time_updated from session"
	if workspace != "" {
		directory := filepath.ToSlash(filepath.Clean(workspace))
		query += " where directory='" + strings.ReplaceAll(directory, "'", "''") + "'"
	}
	query += " order by time_updated desc limit 100"
	executable := findCommand(commandCandidates("opencode"))
	output := ""
	home, _ := os.UserHomeDir()
	output, err := runOpenCodeDatabaseQuery(home, query)
	if err != nil && executable != "" {
		output, err = runExternalCLIOutput(executable, nil, []string{"db", query, "--format", "json"}, workspace, 5*time.Second)
	}
	if err != nil {
		return []ExternalSessionView{}
	}
	var rows []map[string]any
	if json.Unmarshal([]byte(output), &rows) != nil {
		return []ExternalSessionView{}
	}
	result := make([]ExternalSessionView, 0, len(rows))
	for _, row := range rows {
		model := stringFromAny(row["model"])
		provider := ""
		if parsed, ok := mapFromJSON(model); ok {
			provider = stringFromAny(parsed["providerID"])
			if id := stringFromAny(parsed["id"]); id != "" && provider != "" {
				model = provider + "/" + id
			}
		}
		result = append(result, ExternalSessionView{ID: stringFromAny(row["id"]), Workspace: stringFromAny(row["directory"]), Title: stringFromAny(row["title"]), Provider: provider, Model: model, CreatedAt: int64FromAny(row["time_created"]) / 1000, UpdatedAt: int64FromAny(row["time_updated"]) / 1000, Native: true})
	}
	return result
}

func collectOpenCodeUsage(home, workspace string, days int64) ExternalUsageSummary {
	result := ExternalUsageSummary{RangeDays: days, Source: "OpenCode session database", ByModel: []ExternalUsageModelView{}}
	where := ""
	if workspace != "" {
		directory := filepath.ToSlash(filepath.Clean(workspace))
		where = " where directory='" + strings.ReplaceAll(directory, "'", "''") + "'"
	}
	if days > 0 {
		condition := "time_updated >= " + strconv.FormatInt(time.Now().Add(-time.Duration(days)*24*time.Hour).UnixMilli(), 10)
		if where == "" {
			where = " where " + condition
		} else {
			where += " and " + condition
		}
	}
	query := "select count(*) as sessions, coalesce(sum(tokens_input),0) as inputTokens, coalesce(sum(tokens_output),0) as outputTokens, coalesce(sum(tokens_reasoning),0) as reasoningTokens, coalesce(sum(tokens_cache_read),0) as cachedTokens, coalesce(sum(cost),0) as cost from session" + where
	executable := findCommand(commandCandidates("opencode"))
	output, err := runOpenCodeDatabaseQuery(home, query)
	if err != nil && executable != "" {
		output, err = runExternalCLIOutput(executable, nil, []string{"db", query, "--format", "json"}, workspace, 5*time.Second)
	}
	if err == nil {
		var rows []map[string]any
		if json.Unmarshal([]byte(output), &rows) == nil && len(rows) > 0 {
			row := rows[0]
			result.Sessions = int64FromAny(row["sessions"])
			result.InputTokens = int64FromAny(row["inputTokens"])
			result.OutputTokens = int64FromAny(row["outputTokens"])
			result.Reasoning = int64FromAny(row["reasoningTokens"])
			result.CachedTokens = int64FromAny(row["cachedTokens"])
			result.TotalTokens = result.InputTokens + result.CachedTokens + result.OutputTokens + result.Reasoning
			result.Cost = float64FromAny(row["cost"])
		}
	}
	modelQuery := "select model, count(*) as sessions, coalesce(sum(tokens_input),0) as inputTokens, coalesce(sum(tokens_output),0) as outputTokens, coalesce(sum(tokens_reasoning),0) as reasoningTokens, coalesce(sum(tokens_cache_read),0) as cachedTokens, coalesce(sum(cost),0) as cost from session" + where + " group by model order by sessions desc"
	modelOutput, modelErr := runOpenCodeDatabaseQuery(home, modelQuery)
	if modelErr != nil && executable != "" {
		modelOutput, modelErr = runExternalCLIOutput(executable, nil, []string{"db", modelQuery, "--format", "json"}, workspace, 5*time.Second)
	}
	if modelErr == nil {
		var rows []map[string]any
		if json.Unmarshal([]byte(modelOutput), &rows) == nil {
			for _, row := range rows {
				item := ExternalUsageModelView{Model: stringFromAny(row["model"]), Sessions: int64FromAny(row["sessions"]), InputTokens: int64FromAny(row["inputTokens"]), OutputTokens: int64FromAny(row["outputTokens"]), Reasoning: int64FromAny(row["reasoningTokens"]), CachedTokens: int64FromAny(row["cachedTokens"]), Cost: float64FromAny(row["cost"])}
				item.TotalTokens = item.InputTokens + item.CachedTokens + item.OutputTokens + item.Reasoning
				if parsed, ok := mapFromJSON(item.Model); ok {
					item.Provider = stringFromAny(parsed["providerID"])
				}
				result.ByModel = append(result.ByModel, item)
			}
		}
	}
	return result
}

func openCodeDatabasePaths(home string) []string {
	paths := make([]string, 0, 6)
	appendPath := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, existing := range paths {
			if samePath(existing, path) {
				return
			}
		}
		paths = append(paths, path)
	}
	if dataDir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); dataDir != "" {
		appendPath(filepath.Join(dataDir, "opencode.db"))
	}
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		appendPath(filepath.Join(dataHome, "opencode", "opencode.db"))
	}
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		appendPath(filepath.Join(local, "opencode", "opencode.db"))
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		appendPath(filepath.Join(appData, "opencode", "opencode.db"))
	}
	appendPath(filepath.Join(home, ".local", "share", "opencode", "opencode.db"))
	appendPath(filepath.Join(home, ".config", "opencode", "opencode.db"))
	return paths
}

// runOpenCodeDatabaseQuery handles installations where the native opencode
// executable is missing or its postinstall wrapper is broken. OpenCode stores
// usage in SQLite; Node 22+ ships node:sqlite and is already a runtime
// prerequisite for the npm/pnpm distribution.
func runOpenCodeDatabaseQuery(home, query string) (string, error) {
	var databasePath string
	for _, candidate := range openCodeDatabasePaths(home) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			databasePath = candidate
			break
		}
	}
	if databasePath == "" {
		return "", errors.New("OpenCode database was not found")
	}
	node := findCommand(commandCandidates("node"))
	if node == "" {
		return "", errors.New("Node.js was not found for OpenCode database access")
	}
	pathJSON, _ := json.Marshal(databasePath)
	queryJSON, _ := json.Marshal(query)
	script := fmt.Sprintf("const {DatabaseSync}=require('node:sqlite');const db=new DatabaseSync(%s,{readOnly:true});const rows=db.prepare(%s).all();console.log(JSON.stringify(rows,(_,v)=>typeof v==='bigint'?Number(v):v));", pathJSON, queryJSON)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	commandPath, commandArgs, err := providerCommand(node, []string{"--no-warnings", "-e", script})
	if err != nil {
		return "", err
	}
	command := execCommandContext(ctx, commandPath, commandArgs...)
	output, err := runManagedCombinedOutput(ctx, command)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

func listGeminiNativeSessions(home, workspace string) []ExternalSessionView {
	result := make([]ExternalSessionView, 0)
	root := filepath.Join(home, "tmp")
	entries, _ := os.ReadDir(root)
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		projectDir := filepath.Join(root, project.Name())
		marker, _ := os.ReadFile(filepath.Join(projectDir, ".project_root"))
		if workspace != "" && !samePath(strings.TrimSpace(string(marker)), workspace) {
			continue
		}
		chatsDir := filepath.Join(projectDir, "chats")
		files, _ := os.ReadDir(chatsDir)
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(strings.ToLower(file.Name()), ".jsonl") {
				continue
			}
			view, ok := parseGeminiSessionFile(filepath.Join(chatsDir, file.Name()), workspace)
			if ok {
				result = append(result, view)
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	if len(result) > 100 {
		result = result[:100]
	}
	return result
}

func parseGeminiSessionFile(path, workspace string) (ExternalSessionView, bool) {
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) > 16*1024*1024 {
		return ExternalSessionView{}, false
	}
	lines := strings.Split(string(payload), "\n")
	var meta map[string]any
	var document map[string]any
	for _, line := range lines {
		var value map[string]any
		if json.Unmarshal([]byte(line), &value) != nil {
			continue
		}
		if meta == nil && value["sessionId"] != nil {
			meta = value
		}
		if document == nil && value["$set"] != nil {
			document = value
		}
	}
	if meta == nil {
		return ExternalSessionView{}, false
	}
	id := stringFromAny(meta["sessionId"])
	if id == "" {
		return ExternalSessionView{}, false
	}
	view := ExternalSessionView{ID: id, Workspace: workspace, Provider: "gemini", Native: true}
	view.CreatedAt = parseTimeSeconds(stringFromAny(meta["startTime"]))
	view.UpdatedAt = parseTimeSeconds(stringFromAny(meta["lastUpdated"]))
	if document != nil {
		if root, ok := document["$set"].(map[string]any); ok {
			if messages, ok := root["messages"].([]any); ok {
				for _, raw := range messages {
					msg, _ := raw.(map[string]any)
					role := stringFromAny(msg["type"])
					text := firstMessageText(msg["content"])
					if text == "" {
						continue
					}
					if view.Model == "" {
						view.Model = firstMapString(msg, "model", "modelVersion")
					}
					// Gemini writes a synthetic context message before the user's
					// first prompt. It is useful to the CLI, but not a history title.
					if isGeminiContextMessage(text) {
						continue
					}
					if role == "user" && view.Title == "" {
						view.Title = truncateRunes(text, 80)
					}
					if view.Preview == "" {
						view.Preview = truncateRunes(text, 160)
					}
				}
			}
		}
	}
	if view.Title == "" {
		view.Title = filepath.Base(path)
	}
	return view, true
}

func collectGeminiUsage(home, workspace string) ExternalUsageSummary {
	result := ExternalUsageSummary{RangeDays: 30, Source: "Gemini CLI native chat history", ByModel: []ExternalUsageModelView{}}
	cutoff := time.Now().Add(-time.Duration(result.RangeDays) * 24 * time.Hour).Unix()
	byModel := make(map[string]*ExternalUsageModelView)
	root := filepath.Join(home, "tmp")
	entries, _ := os.ReadDir(root)
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		projectDir := filepath.Join(root, project.Name())
		marker, _ := os.ReadFile(filepath.Join(projectDir, ".project_root"))
		if workspace != "" && !samePath(strings.TrimSpace(string(marker)), workspace) {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(projectDir, "chats"))
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(strings.ToLower(file.Name()), ".jsonl") {
				continue
			}
			payload, err := os.ReadFile(filepath.Join(projectDir, "chats", file.Name()))
			if err != nil || len(payload) > 16*1024*1024 {
				continue
			}
			meta := firstGeminiSessionMeta(payload)
			if meta == nil {
				continue
			}
			updatedAt := parseTimeSeconds(stringFromAny(meta["lastUpdated"]))
			if updatedAt > 0 && updatedAt < cutoff {
				continue
			}
			result.Sessions++
			for _, line := range strings.Split(string(payload), "\n") {
				var value map[string]any
				if json.Unmarshal([]byte(line), &value) != nil {
					continue
				}
				// $set is a full document snapshot and repeats prior messages;
				// counting it would multiply usage on every write.
				if value["$set"] != nil {
					continue
				}
				kind := strings.ToLower(stringFromAny(value["type"]))
				if kind == "user" || kind == "gemini" {
					result.Messages++
				}
				var input, output, cached, reasoning, reportedTotal int64
				collectNativeUsage(value, &input, &output, &cached, &reasoning, &reportedTotal)
				if input == 0 && output == 0 && cached == 0 && reasoning == 0 {
					continue
				}
				result.InputTokens += input
				result.OutputTokens += output
				result.CachedTokens += cached
				result.Reasoning += reasoning
				total := input + cached + output + reasoning
				if reportedTotal > 0 {
					total = reportedTotal
				}
				result.TotalTokens += total
				model := strings.TrimSpace(firstMapString(value, "model", "modelVersion"))
				if model == "" {
					model = "gemini"
				}
				item := byModel[model]
				if item == nil {
					item = &ExternalUsageModelView{Model: model, Provider: "gemini"}
					byModel[model] = item
				}
				item.InputTokens += input
				item.OutputTokens += output
				item.CachedTokens += cached
				item.Reasoning += reasoning
				item.TotalTokens += total
			}
		}
	}
	for _, item := range byModel {
		result.ByModel = append(result.ByModel, *item)
	}
	sort.SliceStable(result.ByModel, func(i, j int) bool { return result.ByModel[i].TotalTokens > result.ByModel[j].TotalTokens })
	return result
}

func firstGeminiSessionMeta(payload []byte) map[string]any {
	lineEnd := bytes.IndexByte(payload, '\n')
	if lineEnd < 0 {
		lineEnd = len(payload)
	}
	var meta map[string]any
	if json.Unmarshal(payload[:lineEnd], &meta) != nil || meta["sessionId"] == nil {
		return nil
	}
	return meta
}

func collectNativeUsage(value any, input, output, cached, reasoning, total *int64) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if normalized == "tokens" {
				if tokenMap, ok := nested.(map[string]any); ok {
					*input += int64FromAny(tokenMap["input"])
					*output += int64FromAny(tokenMap["output"])
					*cached += int64FromAny(tokenMap["cached"])
					*reasoning += int64FromAny(tokenMap["thoughts"])
				}
				continue
			}
			number := int64FromAny(nested)
			switch normalized {
			case "prompttokencount", "inputtokens", "inputtokencount":
				if number > 0 {
					*input += number
				}
			case "candidatestokencount", "outputtokens", "outputtokencount":
				if number > 0 {
					*output += number
				}
			case "cachedcontenttokencount", "cachedtokens", "cacheread":
				if number > 0 {
					*cached += number
				}
			case "thoughtstokencount", "reasoningtokens", "reasoningtokencount":
				if number > 0 {
					*reasoning += number
				}
			case "totaltokencount", "totaltokens":
				if number > *total {
					*total = number
				}
			}
			collectNativeUsage(nested, input, output, cached, reasoning, total)
		}
	case []any:
		for _, nested := range typed {
			collectNativeUsage(nested, input, output, cached, reasoning, total)
		}
	}
}

func firstMessageText(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	if items, ok := value.([]any); ok {
		for _, item := range items {
			if entry, ok := item.(map[string]any); ok {
				if text := stringFromAny(entry["text"]); text != "" {
					return strings.TrimSpace(text)
				}
			}
		}
	}
	return ""
}

func isGeminiContextMessage(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(text, "<session_context>") && strings.Contains(text, "this is the gemini cli")
}

func parseTimeSeconds(value string) int64 {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
		return parsed.Unix()
	}
	return 0
}

func float64FromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int64:
		return float64(typed)
	case int:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	default:
		return 0
	}
}

func mapFromJSON(value string) (map[string]any, bool) {
	var result map[string]any
	if json.Unmarshal([]byte(value), &result) != nil {
		return nil, false
	}
	return result, true
}

func runExternalCLIOutput(executable string, env []string, args []string, dir string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	commandPath, commandArgs, err := providerCommand(executable, args)
	if err != nil {
		return "", err
	}
	command := execCommandContext(ctx, commandPath, commandArgs...)
	if dir != "" {
		command.Dir = dir
	}
	if len(env) > 0 {
		command.Env = append(os.Environ(), env...)
	}
	// External catalog/history probes must never create a visible console on
	// Windows. Keep the same managed process wrapper used by streaming turns so
	// PowerShell/npm shims inherit CREATE_NO_WINDOW and are cleaned up on timeout.
	output, err := runManagedCombinedOutput(ctx, command)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

// execCommandContext is a tiny indirection that keeps this file easy to read
// while sharing Windows .ps1 shim resolution with the rest of the app.
var execCommandContext = func(ctx context.Context, path string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, path, args...)
}

func scanJSONObjects(output string) []string {
	result := make([]string, 0, 32)
	start, depth := -1, 0
	inString, escaped := false, false
	for index, char := range output {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		if char == '"' {
			inString = true
			continue
		}
		if char == '{' {
			if depth == 0 {
				start = index
			}
			depth++
		} else if char == '}' && depth > 0 {
			depth--
			if depth == 0 && start >= 0 {
				result = append(result, output[start:index+1])
				start = -1
			}
		}
	}
	return result
}

func writeTextFileAtomic(path, content string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("file path is required")
	}
	if len(content) > 8*1024*1024 {
		return errors.New("instruction/configuration file is too large")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nice-codex-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// Windows does not replace an existing file with os.Rename. Move the old
	// file aside first and restore it if the replacement fails.
	backupPath := path + ".nicecodex-backup"
	_ = os.Remove(backupPath)
	hadExisting := false
	if _, statErr := os.Stat(path); statErr == nil {
		if err := os.Rename(path, backupPath); err != nil {
			return err
		}
		hadExisting = true
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if hadExisting {
			_ = os.Rename(backupPath, path)
		}
		return err
	}
	if hadExisting {
		_ = os.Remove(backupPath)
	}
	return nil
}
