package main

// Native external-runtime catalog support.
//
// Gemini CLI and OpenCode deliberately keep their own configuration, history,
// provider and usage formats. This file is the bridge used by the UI; it never
// maps those values into Codex's model-provider settings.

import (
	"bufio"
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

// externalNativeHistoryPage is the provider-neutral page returned to the
// frontend. Native transcripts stay in their own stores; only this bounded
// slice is converted into the Codex-compatible response shape.
type externalNativeHistoryPage struct {
	Turns []externalTurn
	Start int
	Total int
}

type nativeHistoryCacheEntry struct {
	page      externalNativeHistoryPage
	touchedAt time.Time
}

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

type ExternalUsageDailyBucket struct {
	StartDate             string `json:"startDate"`
	Tokens                int64  `json:"tokens"`
	InputTokens           int64  `json:"inputTokens,omitempty"`
	CachedInputTokens     int64  `json:"cachedInputTokens,omitempty"`
	OutputTokens          int64  `json:"outputTokens,omitempty"`
	ReasoningOutputTokens int64  `json:"reasoningOutputTokens,omitempty"`
}

type ExternalUsageSummary struct {
	RangeDays    int64                      `json:"rangeDays"`
	Sessions     int64                      `json:"sessions"`
	Messages     int64                      `json:"messages"`
	InputTokens  int64                      `json:"inputTokens"`
	OutputTokens int64                      `json:"outputTokens"`
	Reasoning    int64                      `json:"reasoningTokens"`
	CachedTokens int64                      `json:"cachedTokens"`
	TotalTokens  int64                      `json:"totalTokens"`
	Cost         float64                    `json:"cost"`
	ByModel      []ExternalUsageModelView   `json:"byModel"`
	DailyBuckets []ExternalUsageDailyBucket `json:"dailyUsageBuckets"`
	Source       string                     `json:"source"`
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
		if catalog.DefaultModel == "" {
			for _, model := range catalog.Models {
				if model.IsDefault {
					catalog.DefaultModel = model.Model
					break
				}
			}
		}
		if catalog.ActiveProvider == "" {
			catalog.ActiveProvider = providerFromModelReference(catalog.DefaultModel)
		}
		catalog.MCP = readExternalMCPByScope("opencode", catalog.ConfigPath, workspace)
		catalog.GlobalInstructions = readInstructionFile(filepath.Join(catalog.NativeHome, "AGENTS.md"), "opencode-global", "OpenCode global AGENTS.md")
		catalog.ProjectInstructions = readOpenCodeProjectInstructions(workspace)
		catalog.Sessions = listOpenCodeNativeSessions(workspace)
		// The catalog and the usage panel both present native lifetime totals.
		// Applying a 30-day filter here made the two screens disagree.
		catalog.Usage = collectOpenCodeUsage(home, workspace, 0)
		catalog.ReadOnlyNotice = "provider、model、MCP、指令、历史和 usage 均来自 OpenCode 原生配置/数据库。"
	}
	return catalog, nil
}

func (s *AppService) currentRuntimeWorkspace(runtime string) string {
	settings := s.Settings()
	if runtime == "gemini" || runtime == "opencode" {
		return strings.TrimSpace(activeWorkspaceForRuntime(settings))
	}
	return ""
}

// syncNativeExternalSessions imports only lightweight summaries into the local
// sidebar index. Full turns remain in Gemini JSONL/OpenCode SQLite and are
// loaded lazily when a session is opened. A short TTL prevents repeated
// sidebar refreshes from rescanning the native stores.
func (s *AppService) syncNativeExternalSessions(runtime, workspace string) {
	runtime = normalizeExternalRuntime(runtime)
	if runtime == "" {
		return
	}
	key := runtime + "|" + strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(workspace))))
	now := time.Now()
	s.mu.Lock()
	if last := s.nativeSessionsSyncedAt[key]; !last.IsZero() && now.Sub(last) < 15*time.Second {
		s.mu.Unlock()
		return
	}
	s.nativeSessionsSyncedAt[key] = now
	s.mu.Unlock()

	home, _ := os.UserHomeDir()
	var views []ExternalSessionView
	switch runtime {
	case "gemini":
		views = listGeminiNativeSessions(filepath.Join(home, ".gemini"), workspace)
	case "opencode":
		views = listOpenCodeNativeSessions(workspace)
	}
	if len(views) == 0 {
		return
	}

	s.mu.Lock()
	changed := false
	invalidated := make([]string, 0, 4)
	for _, view := range views {
		backendRef := strings.TrimSpace(view.ID)
		if backendRef == "" {
			continue
		}
		viewWorkspace := strings.TrimSpace(view.Workspace)
		if viewWorkspace == "" {
			viewWorkspace = workspace
		}
		viewTitle := strings.TrimSpace(view.Title)
		// A local external session is allocated before the CLI creates its
		// native session. Prefer that pending record over a native mirror so a
		// refresh during the first turn cannot create a second sidebar row.
		var existing *SessionRecord
		var pending *SessionRecord
		var nativeMirror *SessionRecord
		for _, record := range s.sessions {
			if record == nil || record.Provider != runtime || !samePath(record.Workspace, viewWorkspace) {
				continue
			}
			if record.BackendRef == backendRef && !record.Native {
				existing = record
				break
			}
			if record.Native && record.ID == "native-"+runtime+"-"+backendRef {
				nativeMirror = record
			}
			recordName := strings.TrimSpace(record.Name)
			recordPreview := strings.TrimSpace(record.Preview)
			titleMatches := viewTitle != "" && ((recordName != "" && (strings.EqualFold(recordName, viewTitle) || strings.HasPrefix(recordName, viewTitle) || strings.HasPrefix(viewTitle, recordName))) || (recordPreview != "" && (strings.EqualFold(recordPreview, viewTitle) || strings.HasPrefix(recordPreview, viewTitle) || strings.HasPrefix(viewTitle, recordPreview))))
			if !record.Archived && !record.Native && strings.TrimSpace(record.BackendRef) == "" && ((len(record.Turns) == 0 && strings.TrimSpace(record.Preview) == "") || titleMatches) {
				if pending == nil || record.UpdatedAt > pending.UpdatedAt || (record.UpdatedAt == pending.UpdatedAt && record.CreatedAt > pending.CreatedAt) {
					pending = record
				}
			}
		}
		if existing == nil {
			existing = pending
		}
		if existing == nil {
			existing = nativeMirror
		}
		if existing == nil {
			createdAt := view.CreatedAt
			if createdAt <= 0 {
				createdAt = view.UpdatedAt
			}
			if createdAt <= 0 {
				createdAt = now.Unix()
			}
			updatedAt := view.UpdatedAt
			if updatedAt <= 0 {
				updatedAt = createdAt
			}
			name := strings.TrimSpace(view.Title)
			if name == "" {
				name = "New task"
			}
			existing = &SessionRecord{
				ID:        "native-" + runtime + "-" + backendRef,
				Workspace: viewWorkspace, Provider: runtime,
				ProviderID: externalProviderID(runtime), BackendRef: backendRef,
				Model: view.Model, WorkMode: "code", Name: name,
				Preview: view.Preview, CreatedAt: createdAt, UpdatedAt: updatedAt,
				Native: true, Turns: []externalTurn{},
			}
			s.sessions[existing.ID] = existing
			changed = true
		} else if existing == pending && strings.TrimSpace(existing.BackendRef) == "" {
			existing.BackendRef = backendRef
			existing.Provider = runtime
			existing.ProviderID = externalProviderID(runtime)
			if strings.TrimSpace(view.Model) != "" {
				existing.Model = view.Model
			}
			if view.UpdatedAt > existing.UpdatedAt {
				existing.UpdatedAt = view.UpdatedAt
			}
			changed = true
			// Remove any mirror imported before this pending record was bound.
			for id, record := range s.sessions {
				if record == nil || record.ID == existing.ID || !record.Native || record.Provider != runtime || !samePath(record.Workspace, viewWorkspace) || record.BackendRef != backendRef {
					continue
				}
				delete(s.sessions, id)
			}
		} else if !existing.Archived && !existing.Native && existing.BackendRef == backendRef {
			for id, record := range s.sessions {
				if record == nil || record.ID == existing.ID || !record.Native || record.Provider != runtime || !samePath(record.Workspace, viewWorkspace) || record.BackendRef != backendRef {
					continue
				}
				delete(s.sessions, id)
				changed = true
			}
		} else if !existing.Archived && existing.Native {
			name := strings.TrimSpace(view.Title)
			if name == "" {
				name = existing.Name
			}
			updatedAt := view.UpdatedAt
			if updatedAt <= 0 {
				updatedAt = existing.UpdatedAt
			}
			if existing.Name != name || existing.Preview != view.Preview || existing.Model != view.Model || existing.UpdatedAt != updatedAt {
				existing.Name, existing.Preview, existing.Model, existing.UpdatedAt = name, view.Preview, view.Model, updatedAt
				changed = true
				invalidated = append(invalidated, backendRef)
			}
		}
	}
	if changed {
		s.persistSessionsLocked()
	}
	s.mu.Unlock()
	for _, backendRef := range invalidated {
		s.invalidateNativeHistoryCache(runtime, backendRef)
	}
}

func (s *AppService) invalidateNativeHistoryCache(runtime, backendRef string) {
	prefix := normalizeExternalRuntime(runtime) + "\x00" + strings.TrimSpace(backendRef) + "\x00"
	if prefix == "\x00\x00" {
		return
	}
	s.historyMu.Lock()
	for key := range s.nativeHistoryCache {
		if strings.HasPrefix(key, prefix) {
			delete(s.nativeHistoryCache, key)
		}
	}
	s.historyMu.Unlock()
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
	configPath := openCodeConfigPath(home)
	var config map[string]any
	readLimitedJSON(configPath, &config)
	executable := findCommand(commandCandidates("opencode"))
	output := ""
	if executable != "" {
		if value, err := runExternalCLIOutput(executable, nil, []string{"models", "--verbose"}, "", 8*time.Second); err == nil {
			output = value
		}
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
	// Packaged/remote OpenCode installations may not allow the CLI catalog
	// command. Fall back to the native provider.models map so configured
	// providers remain selectable in Settings.
	if provider, ok := config["provider"].(map[string]any); ok {
		for providerID, raw := range provider {
			entry, _ := raw.(map[string]any)
			modelsRaw, _ := entry["models"].(map[string]any)
			for modelID, rawModel := range modelsRaw {
				modelID = strings.TrimSpace(modelID)
				if modelID == "" {
					continue
				}
				model := AgentProviderModel{
					Model:       strings.TrimSpace(providerID) + "/" + modelID,
					ProviderID:  strings.TrimSpace(providerID),
					DisplayName: modelID,
					Description: "OpenCode " + strings.TrimSpace(providerID) + " model",
				}
				if modelConfig, ok := rawModel.(map[string]any); ok {
					if name := strings.TrimSpace(stringFromAny(modelConfig["name"])); name != "" {
						model.DisplayName = name
					}
					if limit, ok := modelConfig["limit"].(map[string]any); ok {
						model.ContextWindow = int64FromAny(limit["context"])
					}
				}
				if strings.TrimSpace(model.ProviderID) != "" && !seen[strings.ToLower(model.Model)] {
					seen[strings.ToLower(model.Model)] = true
					models = append(models, model)
					providerModels[model.ProviderID] = append(providerModels[model.ProviderID], model)
				}
			}
		}
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
	if !hasDefaultModel(models) && len(models) > 0 {
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
	if providersOutput, providerErr := runExternalCLIOutput(executable, nil, []string{"providers", "list"}, "", 5*time.Second); providerErr == nil {
		clean := ansiEscapePattern.ReplaceAllString(providersOutput, "")
		if strings.Contains(strings.ToLower(clean), "open code go") || strings.Contains(strings.ToLower(clean), "opencode go") {
			configuredProviders["opencode-go"] = true
		}
		if strings.Contains(strings.ToLower(clean), "open code") {
			configuredProviders["opencode"] = true
		}
	}
	providerIDs := make([]string, 0, len(providerModels)+len(configuredProviders))
	providerSeen := make(map[string]bool, len(providerModels)+len(configuredProviders))
	for id := range providerModels {
		providerSeen[id] = true
		providerIDs = append(providerIDs, id)
	}
	for id := range configuredProviders {
		if !providerSeen[id] {
			providerIDs = append(providerIDs, id)
		}
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
		providers = append(providers, ExternalProviderView{ID: id, Name: name, Source: "opencode models --verbose / opencode.json", Configured: configuredProviders[id], Authenticated: configuredProviders[id], BaseURL: providerBaseURLs[id], Models: providerModels[id]})
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
	case float32:
		return int64(typed)
	case int64:
		return typed
	case int32:
		return int64(typed)
	case int:
		return int64(typed)
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return int64(^uint64(0) >> 1)
		}
		return int64(typed)
	case uint32:
		return int64(typed)
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		if err == nil {
			return parsed
		}
		if floatValue, floatErr := strconv.ParseFloat(string(typed), 64); floatErr == nil {
			return int64(floatValue)
		}
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return parsed
		}
		if floatValue, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return int64(floatValue)
		}
	}
	return 0
}

func firstPositiveInt64(values map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value := int64FromAny(values[key]); value > 0 {
			return value
		}
	}
	return 0
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
	result := ExternalUsageSummary{RangeDays: days, Source: "OpenCode session database", ByModel: []ExternalUsageModelView{}, DailyBuckets: []ExternalUsageDailyBucket{}}
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
				modelValue := stringFromAny(row["model"])
				providerID, modelID := normalizeOpenCodeModelReference(modelValue)
				item := ExternalUsageModelView{Model: modelValue, Provider: providerID, Sessions: int64FromAny(row["sessions"]), InputTokens: int64FromAny(row["inputTokens"]), OutputTokens: int64FromAny(row["outputTokens"]), Reasoning: int64FromAny(row["reasoningTokens"]), CachedTokens: int64FromAny(row["cachedTokens"]), Cost: float64FromAny(row["cost"])}
				if modelID != "" {
					item.Model = modelID
				}
				item.TotalTokens = item.InputTokens + item.CachedTokens + item.OutputTokens + item.Reasoning
				result.ByModel = append(result.ByModel, item)
			}
		}
	}

	// Session totals are lifetime counters, while the range selector needs real
	// local calendar days. OpenCode stores the per-assistant-message breakdown in
	// message.data.tokens, so aggregate those messages by their creation time.
	dailyWhere := " where json_extract(m.data, '$.role')='assistant'"
	if workspace != "" {
		directory := filepath.ToSlash(filepath.Clean(workspace))
		dailyWhere += " and s.directory='" + strings.ReplaceAll(directory, "'", "''") + "'"
	}
	if days > 0 {
		dailyWhere += " and m.time_created >= " + strconv.FormatInt(time.Now().Add(-time.Duration(days)*24*time.Hour).UnixMilli(), 10)
	}
	dailyQuery := "select m.time_created as timeCreated,m.data,s.model from message m left join session s on s.id=m.session_id" + dailyWhere + " order by m.time_created asc"
	dailyOutput, dailyErr := runOpenCodeDatabaseQuery(home, dailyQuery)
	if dailyErr != nil && executable != "" {
		dailyOutput, dailyErr = runExternalCLIOutput(executable, nil, []string{"db", dailyQuery, "--format", "json"}, workspace, 5*time.Second)
	}
	if dailyErr == nil {
		var rows []map[string]any
		if json.Unmarshal([]byte(dailyOutput), &rows) == nil {
			byDay := make(map[string]*ExternalUsageDailyBucket)
			for _, row := range rows {
				data := asStringKeyMap(parseNativeJSONValue(row["data"]))
				if len(data) == 0 {
					continue
				}
				result.Messages++
				var input, outputTokens, cached, reasoning, total int64
				collectNativeUsage(data, &input, &outputTokens, &cached, &reasoning, &total)
				if total <= 0 {
					total = input + cached + outputTokens + reasoning
				}
				if total <= 0 {
					continue
				}
				at := time.UnixMilli(int64FromAny(row["timeCreated"])).In(time.Local)
				if at.IsZero() || at.Unix() <= 0 {
					continue
				}
				day := at.Format("2006-01-02")
				bucket := byDay[day]
				if bucket == nil {
					bucket = &ExternalUsageDailyBucket{StartDate: day}
					byDay[day] = bucket
				}
				bucket.Tokens += total
				bucket.InputTokens += input
				bucket.CachedInputTokens += cached
				bucket.OutputTokens += outputTokens
				bucket.ReasoningOutputTokens += reasoning
			}
			for _, bucket := range byDay {
				result.DailyBuckets = append(result.DailyBuckets, *bucket)
			}
			sort.Slice(result.DailyBuckets, func(i, j int) bool {
				return result.DailyBuckets[i].StartDate > result.DailyBuckets[j].StartDate
			})
		}
	}
	return result
}

func normalizeOpenCodeModelReference(value string) (provider, model string) {
	value = strings.TrimSpace(value)
	if parsed, ok := mapFromJSON(value); ok {
		provider = strings.TrimSpace(stringFromAny(parsed["providerID"]))
		modelID := strings.TrimSpace(stringFromAny(parsed["id"]))
		if provider != "" && modelID != "" {
			return provider, provider + "/" + modelID
		}
	}
	if index := strings.IndexByte(value, '/'); index > 0 {
		return strings.TrimSpace(value[:index]), value
	}
	return "", value
}

// collectOpenCodeSessionUsage reads usage from assistant messages created by
// the current run. A turn can contain several tool-call assistant messages, so
// keeping only the final stream event undercounts it; reading the whole session
// would instead count previous turns again.
func collectOpenCodeSessionUsage(home, sessionID string, startedAtMillis int64) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	quoted := strings.ReplaceAll(sessionID, "'", "''")
	query := "select data from message where session_id='" + quoted + "'"
	if startedAtMillis > 0 {
		query += " and time_created >= " + strconv.FormatInt(startedAtMillis, 10)
	}
	query += " order by time_created asc"
	output, err := runOpenCodeDatabaseQuery(home, query)
	if err != nil {
		return nil
	}
	var rows []map[string]any
	if json.Unmarshal([]byte(output), &rows) != nil {
		return nil
	}
	var input, outputTokens, cached, reasoning, total int64
	for _, row := range rows {
		data, ok := row["data"].(string)
		if !ok || strings.TrimSpace(data) == "" {
			continue
		}
		var message map[string]any
		if json.Unmarshal([]byte(data), &message) != nil {
			continue
		}
		if !strings.EqualFold(stringFromAny(message["role"]), "assistant") {
			continue
		}
		collectNativeUsage(message, &input, &outputTokens, &cached, &reasoning, &total)
	}
	if input == 0 && outputTokens == 0 && cached == 0 && reasoning == 0 && total == 0 {
		return nil
	}
	if total <= 0 {
		total = input + cached + outputTokens + reasoning
	}
	return map[string]any{
		"inputTokens":           input,
		"cachedInputTokens":     cached,
		"outputTokens":          outputTokens,
		"reasoningOutputTokens": reasoning,
		"totalTokens":           total,
	}
}

func nativeSQLQuote(value string) string {
	return "'" + strings.ReplaceAll(strings.TrimSpace(value), "'", "''") + "'"
}

// readOpenCodeNativeHistoryPage converts only the selected user-turn window
// from SQLite. Messages and parts are queried by timestamp after locating the
// page boundaries, so a huge OpenCode session never crosses the bridge at once.
func readOpenCodeNativeHistoryPage(record *SessionRecord, before int) (externalNativeHistoryPage, error) {
	page := externalNativeHistoryPage{Turns: []externalTurn{}}
	if record == nil || strings.TrimSpace(record.BackendRef) == "" {
		return page, errors.New("OpenCode native session id is missing")
	}
	home, _ := os.UserHomeDir()
	sessionID := nativeSQLQuote(record.BackendRef)
	countOutput, err := runOpenCodeDatabaseQuery(home, "select count(*) as total from message where session_id="+sessionID+" and json_extract(data, '$.role')='user'")
	if err != nil {
		return page, err
	}
	var countRows []map[string]any
	if json.Unmarshal([]byte(countOutput), &countRows) != nil || len(countRows) == 0 {
		return page, nil
	}
	total := int(int64FromAny(countRows[0]["total"]))
	if total < 0 {
		total = 0
	}
	page.Total = total
	end := before
	if end < 0 || end > total {
		end = total
	}
	start := end - conversationHistoryPageTurns
	if start < 0 {
		start = 0
	}
	page.Start = start
	if end <= start {
		return page, nil
	}

	boundaryOutput, err := runOpenCodeDatabaseQuery(home, "select id,time_created,data from message where session_id="+sessionID+" and json_extract(data, '$.role')='user' order by time_created asc,id asc limit "+strconv.Itoa(end-start)+" offset "+strconv.Itoa(start))
	if err != nil {
		return page, err
	}
	var boundaries []map[string]any
	if json.Unmarshal([]byte(boundaryOutput), &boundaries) != nil || len(boundaries) == 0 {
		return page, nil
	}
	lower := int64FromAny(boundaries[0]["time_created"])
	upper := int64(0)
	if end < total {
		upperOutput, upperErr := runOpenCodeDatabaseQuery(home, "select time_created from message where session_id="+sessionID+" and json_extract(data, '$.role')='user' order by time_created asc,id asc limit 1 offset "+strconv.Itoa(end))
		if upperErr == nil {
			var upperRows []map[string]any
			if json.Unmarshal([]byte(upperOutput), &upperRows) == nil && len(upperRows) > 0 {
				upper = int64FromAny(upperRows[0]["time_created"])
			}
		}
	}
	messageQuery := "select id,time_created,data from message where session_id=" + sessionID + " and time_created >= " + strconv.FormatInt(lower, 10)
	if upper > lower {
		messageQuery += " and time_created < " + strconv.FormatInt(upper, 10)
	}
	messageQuery += " order by time_created asc,id asc"
	messageOutput, err := runOpenCodeDatabaseQuery(home, messageQuery)
	if err != nil {
		return page, err
	}
	var messages []map[string]any
	if json.Unmarshal([]byte(messageOutput), &messages) != nil {
		return page, nil
	}
	partQuery := "select id,message_id,time_created,data from part where session_id=" + sessionID + " and time_created >= " + strconv.FormatInt(lower, 10)
	if upper > lower {
		partQuery += " and time_created < " + strconv.FormatInt(upper, 10)
	}
	partQuery += " order by time_created asc,id asc"
	partOutput, partErr := runOpenCodeDatabaseQuery(home, partQuery)
	if partErr != nil {
		return page, partErr
	}
	var parts []map[string]any
	_ = json.Unmarshal([]byte(partOutput), &parts)
	partsByMessage := make(map[string][]map[string]any, len(parts))
	for _, part := range parts {
		messageID := stringFromAny(part["message_id"])
		if messageID != "" {
			partsByMessage[messageID] = append(partsByMessage[messageID], part)
		}
	}
	for _, message := range messages {
		messageID := stringFromAny(message["id"])
		data := asStringKeyMap(parseNativeJSONValue(message["data"]))
		role := strings.ToLower(strings.TrimSpace(stringFromAny(data["role"])))
		if role == "user" {
			started := int64FromAny(message["time_created"]) / 1000
			turn := externalTurn{ID: "opencode-turn-" + messageID, Status: "completed", StartedAt: started}
			page.Turns = append(page.Turns, turn)
		}
		if len(page.Turns) == 0 {
			continue
		}
		turn := &page.Turns[len(page.Turns)-1]
		completedAt := int64FromAny(message["time_created"]) / 1000
		if role == "assistant" {
			mergeNativeUsage(turn, data)
			if completedAt > turn.CompletedAt {
				turn.CompletedAt = completedAt
			}
		}
		for _, part := range partsByMessage[messageID] {
			partData := asStringKeyMap(parseNativeJSONValue(part["data"]))
			partType := strings.ToLower(strings.TrimSpace(stringFromAny(partData["type"])))
			partID := stringFromAny(part["id"])
			if partID == "" {
				partID = messageID + "-part"
			}
			switch partType {
			case "text":
				text := stringFromAny(partData["text"])
				if role == "user" {
					turn.UserText = strings.TrimSpace(strings.TrimSpace(turn.UserText) + "\n" + strings.TrimSpace(text))
				} else if text != "" {
					turn.AgentText += text
					turn.Items = append(turn.Items, map[string]any{"id": partID, "type": "agentMessage", "status": "completed", "text": text})
				}
			case "reasoning":
				text := stringFromAny(partData["text"])
				if text == "" {
					text = stringFromAny(partData["reasoning"])
				}
				if text != "" {
					turn.Items = append(turn.Items, map[string]any{"id": partID, "type": "reasoning", "status": "completed", "summary": text, "content": text})
				}
			case "tool":
				if item, ok := parseExternalToolEvent("opencode", partData); ok {
					failed := strings.EqualFold(firstMapString(item, "status"), "failed")
					item["id"] = partID
					item["status"] = "completed"
					item["success"] = !failed
					turn.Items = append(turn.Items, item)
				}
			}
			partAt := int64FromAny(part["time_created"]) / 1000
			if partAt > turn.CompletedAt && role == "assistant" {
				turn.CompletedAt = partAt
			}
		}
	}
	for index := range page.Turns {
		turn := &page.Turns[index]
		if turn.CompletedAt == 0 {
			turn.CompletedAt = turn.StartedAt
		}
		if turn.CompletedAt >= turn.StartedAt && turn.StartedAt > 0 {
			turn.DurationMS = (turn.CompletedAt - turn.StartedAt) * 1000
		}
		if turn.UserText == "" {
			turn.UserText = "OpenCode session turn"
		}
	}
	return page, nil
}

func parseNativeJSONValue(value any) any {
	if text, ok := value.(string); ok {
		var parsed any
		if json.Unmarshal([]byte(text), &parsed) == nil {
			return parsed
		}
	}
	return value
}

func mergeNativeUsage(turn *externalTurn, value any) {
	if turn == nil {
		return
	}
	var input, output, cached, reasoning, total int64
	collectNativeUsage(value, &input, &output, &cached, &reasoning, &total)
	if input == 0 && output == 0 && cached == 0 && reasoning == 0 && total == 0 {
		return
	}
	usage := asStringKeyMap(turn.Usage)
	usage["inputTokens"] = int64FromAny(usage["inputTokens"]) + input
	usage["cachedInputTokens"] = int64FromAny(usage["cachedInputTokens"]) + cached
	usage["outputTokens"] = int64FromAny(usage["outputTokens"]) + output
	usage["reasoningOutputTokens"] = int64FromAny(usage["reasoningOutputTokens"]) + reasoning
	if total <= 0 {
		total = input + cached + output + reasoning
	}
	usage["totalTokens"] = int64FromAny(usage["totalTokens"]) + total
	turn.Usage = usage
}

func (s *AppService) readNativeExternalHistoryPage(record *SessionRecord, before int) (externalNativeHistoryPage, error) {
	if record == nil {
		return externalNativeHistoryPage{}, errors.New("native session is missing")
	}
	cacheKey := normalizeExternalRuntime(record.Provider) + "\x00" + strings.TrimSpace(record.BackendRef) + "\x00" + strconv.Itoa(before)
	now := time.Now()
	s.historyMu.Lock()
	if cached, ok := s.nativeHistoryCache[cacheKey]; ok && now.Sub(cached.touchedAt) < 20*time.Second {
		page := cached.page
		page.Turns = append([]externalTurn(nil), cached.page.Turns...)
		s.historyMu.Unlock()
		return page, nil
	}
	s.historyMu.Unlock()

	page, err := s.readNativeExternalHistoryPageUncached(record, before)
	if err != nil {
		return page, err
	}
	s.historyMu.Lock()
	if s.nativeHistoryCache == nil {
		s.nativeHistoryCache = make(map[string]nativeHistoryCacheEntry)
	}
	if len(s.nativeHistoryCache) >= conversationHistoryCacheLimit*4 {
		oldestKey := ""
		oldestAt := now
		for key, entry := range s.nativeHistoryCache {
			if oldestKey == "" || entry.touchedAt.Before(oldestAt) {
				oldestKey, oldestAt = key, entry.touchedAt
			}
		}
		delete(s.nativeHistoryCache, oldestKey)
	}
	cachedPage := page
	cachedPage.Turns = append([]externalTurn(nil), page.Turns...)
	s.nativeHistoryCache[cacheKey] = nativeHistoryCacheEntry{page: cachedPage, touchedAt: now}
	s.historyMu.Unlock()
	return page, nil
}

func (s *AppService) readNativeExternalHistoryPageUncached(record *SessionRecord, before int) (externalNativeHistoryPage, error) {
	if record == nil {
		return externalNativeHistoryPage{}, errors.New("native session is missing")
	}
	switch normalizeExternalRuntime(record.Provider) {
	case "opencode":
		return readOpenCodeNativeHistoryPage(record, before)
	case "gemini":
		return readGeminiNativeHistoryPage(record, before)
	default:
		return externalNativeHistoryPage{}, errors.New("unsupported native session provider")
	}
}

func readGeminiNativeHistoryPage(record *SessionRecord, before int) (externalNativeHistoryPage, error) {
	page := externalNativeHistoryPage{Turns: []externalTurn{}}
	home, _ := os.UserHomeDir()
	path := findGeminiNativeSessionFile(filepath.Join(home, ".gemini"), record.BackendRef)
	if path == "" {
		return page, errors.New("Gemini native session file was not found")
	}
	turns, err := loadGeminiNativeTurns(path)
	if err != nil {
		return page, err
	}
	total := len(turns)
	end := before
	if end < 0 || end > total {
		end = total
	}
	start := end - conversationHistoryPageTurns
	if start < 0 {
		start = 0
	}
	page.Total = total
	page.Start = start
	page.Turns = append(page.Turns, turns[start:end]...)
	return page, nil
}

func loadGeminiNativeTurns(path string) ([]externalTurn, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// A single JSONL session can be large, but keep an upper bound so a corrupt
	// file cannot exhaust the desktop process while opening a conversation.
	if len(payload) > 64*1024*1024 {
		return nil, errors.New("Gemini native session is too large to open")
	}
	lines := strings.Split(string(payload), "\n")
	incremental := make([]map[string]any, 0, 64)
	var snapshotMessages []any
	hasIncrementalUser := false
	for _, line := range lines {
		var value map[string]any
		if json.Unmarshal([]byte(line), &value) != nil || value == nil {
			continue
		}
		if snapshot, ok := value["$set"].(map[string]any); ok {
			if messages, exists := snapshot["messages"].([]any); exists {
				snapshotMessages = messages
			}
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(stringFromAny(value["type"])))
		if kind == "user" || kind == "gemini" || kind == "assistant" {
			incremental = append(incremental, value)
			if kind == "user" {
				hasIncrementalUser = true
			}
		}
	}
	if !hasIncrementalUser {
		incremental = make([]map[string]any, 0, len(snapshotMessages))
		for _, raw := range snapshotMessages {
			if message, ok := raw.(map[string]any); ok {
				incremental = append(incremental, message)
			}
		}
	}
	return geminiTurnsFromMessages(incremental), nil
}

func geminiTurnsFromMessages(messages []map[string]any) []externalTurn {
	turns := make([]externalTurn, 0, len(messages)/2)
	toolIndexes := make(map[string]int)
	for index, message := range messages {
		kind := strings.ToLower(strings.TrimSpace(stringFromAny(message["type"])))
		if kind == "assistant" {
			kind = "gemini"
		}
		if kind == "user" {
			text := geminiMessageText(message["content"])
			if isGeminiContextMessage(text) || strings.TrimSpace(text) == "" {
				continue
			}
			turnID := stringFromAny(message["id"])
			if turnID == "" {
				turnID = "message-" + strconv.Itoa(index)
			}
			started := parseTimeSeconds(stringFromAny(message["timestamp"]))
			turns = append(turns, externalTurn{ID: "gemini-turn-" + turnID, UserText: text, Status: "completed", StartedAt: started})
			toolIndexes = make(map[string]int)
			continue
		}
		if kind != "gemini" || len(turns) == 0 {
			continue
		}
		turn := &turns[len(turns)-1]
		completed := parseTimeSeconds(stringFromAny(message["timestamp"]))
		if completed > turn.CompletedAt {
			turn.CompletedAt = completed
		}
		mergeNativeUsage(turn, message)
		for thoughtIndex, thought := range nativeAnySlice(message["thoughts"]) {
			thoughtMap := asStringKeyMap(thought)
			text := stringFromAny(thoughtMap["description"])
			if text == "" {
				text = stringFromAny(thoughtMap["text"])
			}
			if text != "" {
				turn.Items = append(turn.Items, map[string]any{"id": turn.ID + ":reasoning:" + strconv.Itoa(thoughtIndex), "type": "reasoning", "status": "completed", "summary": text, "content": text})
			}
		}
		for contentIndex, raw := range nativeAnySlice(message["content"]) {
			content := asStringKeyMap(raw)
			if text := stringFromAny(content["text"]); text != "" {
				turn.AgentText += text
				turn.Items = append(turn.Items, map[string]any{"id": turn.ID + ":agent:" + strconv.Itoa(contentIndex), "type": "agentMessage", "status": "completed", "text": text})
			}
			if call, ok := content["functionCall"].(map[string]any); ok {
				name := stringFromAny(call["name"])
				if name == "" {
					name = "Gemini function"
				}
				callID := firstMapString(call, "id", "callId", "callID")
				if callID == "" {
					callID = name + ":" + strconv.Itoa(contentIndex)
				}
				item := map[string]any{"id": turn.ID + ":tool:" + callID, "type": "dynamicToolCall", "tool": name, "status": "completed", "arguments": call["args"], "contentItems": []any{}, "success": true}
				turn.Items = append(turn.Items, item)
				toolIndexes[callID] = len(turn.Items) - 1
				toolIndexes[name] = len(turn.Items) - 1
			}
			if response, ok := content["functionResponse"].(map[string]any); ok {
				name := stringFromAny(response["name"])
				callID := firstMapString(response, "id", "callId", "callID")
				itemIndex, exists := toolIndexes[callID]
				if !exists {
					itemIndex, exists = toolIndexes[name]
				}
				if !exists {
					callID = name + ":response:" + strconv.Itoa(contentIndex)
					itemIndex = len(turn.Items)
					turn.Items = append(turn.Items, map[string]any{"id": turn.ID + ":tool:" + callID, "type": "dynamicToolCall", "tool": name, "status": "completed", "arguments": nil, "contentItems": []any{}, "success": true})
				}
				item := turn.Items[itemIndex]
				item["status"] = "completed"
				item["contentItems"] = externalToolContentItems(response["response"])
				if response["error"] != nil {
					item["success"] = false
				}
			}
		}
	}
	for index := range turns {
		turn := &turns[index]
		if turn.CompletedAt == 0 {
			turn.CompletedAt = turn.StartedAt
		}
		if turn.CompletedAt >= turn.StartedAt && turn.StartedAt > 0 {
			turn.DurationMS = (turn.CompletedAt - turn.StartedAt) * 1000
		}
		if turn.UserText == "" {
			turn.UserText = "Gemini session turn"
		}
	}
	return turns
}

func geminiMessageText(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	texts := make([]string, 0, 2)
	for _, raw := range nativeAnySlice(value) {
		entry := asStringKeyMap(raw)
		if text := strings.TrimSpace(stringFromAny(entry["text"])); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

func nativeAnySlice(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}
	return []any{}
}

func findGeminiNativeSessionFile(home, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	root := filepath.Join(home, "tmp")
	entries, _ := os.ReadDir(root)
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(root, project.Name(), "chats"))
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(strings.ToLower(file.Name()), ".jsonl") {
				continue
			}
			path := filepath.Join(root, project.Name(), "chats", file.Name())
			if stringFromAny(readFirstGeminiSessionMeta(path)["sessionId"]) == sessionID {
				return path
			}
		}
	}
	return ""
}

func readFirstGeminiSessionMeta(path string) map[string]any {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	// Session metadata is a small first-line record. The explicit ceiling keeps
	// a corrupt file from allocating without bound while allowing long JSONL
	// transcripts to be located without loading the whole conversation.
	scanner.Buffer(make([]byte, 16*1024), 1024*1024)
	if !scanner.Scan() {
		return nil
	}
	return firstGeminiSessionMeta(scanner.Bytes())
}

// collectGeminiSessionUsage handles Gemini JSONL transcripts whose usage is
// persisted on messages rather than emitted on stdout. Only messages written
// by this run are counted so resumed sessions do not duplicate prior turns.
func collectGeminiSessionUsage(home, sessionID string, startedAtMillis int64) map[string]any {
	path := findGeminiNativeSessionFile(home, sessionID)
	if path == "" {
		return nil
	}
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) > 16*1024*1024 {
		return nil
	}
	var input, output, cached, reasoning, total int64
	var latestSnapshot map[string]any
	for _, line := range strings.Split(string(payload), "\n") {
		var value map[string]any
		if json.Unmarshal([]byte(line), &value) != nil {
			continue
		}
		if snapshot, ok := value["$set"].(map[string]any); ok {
			latestSnapshot = snapshot
			continue
		}
		if !geminiMessageAtOrAfter(value, startedAtMillis) {
			continue
		}
		collectNativeUsage(value, &input, &output, &cached, &reasoning, &total)
	}
	if input == 0 && output == 0 && cached == 0 && reasoning == 0 && total == 0 && latestSnapshot != nil {
		if messages, ok := latestSnapshot["messages"].([]any); ok {
			for _, raw := range messages {
				message, _ := raw.(map[string]any)
				if !geminiMessageAtOrAfter(message, startedAtMillis) {
					continue
				}
				collectNativeUsage(message, &input, &output, &cached, &reasoning, &total)
			}
		}
	}
	if input == 0 && output == 0 && cached == 0 && reasoning == 0 && total == 0 {
		return nil
	}
	if total <= 0 {
		total = input + cached + output + reasoning
	}
	return map[string]any{
		"inputTokens":           input,
		"cachedInputTokens":     cached,
		"outputTokens":          output,
		"reasoningOutputTokens": reasoning,
		"totalTokens":           total,
	}
}

func geminiMessageAtOrAfter(message map[string]any, startedAtMillis int64) bool {
	if startedAtMillis <= 0 {
		return true
	}
	timestamp := strings.TrimSpace(stringFromAny(message["timestamp"]))
	if timestamp == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	return err == nil && parsed.UnixMilli() >= startedAtMillis
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
	// The usage card labels the aggregate as lifetime. Keep the native history
	// scan consistent with that label instead of silently dropping older turns.
	result := ExternalUsageSummary{RangeDays: 0, Source: "Gemini CLI native chat history", ByModel: []ExternalUsageModelView{}}
	cutoff := int64(0)
	byModel := make(map[string]*ExternalUsageModelView)
	addUsage := func(value any, modelHint string) bool {
		var input, output, cached, reasoning, reportedTotal int64
		collectNativeUsage(value, &input, &output, &cached, &reasoning, &reportedTotal)
		if input == 0 && output == 0 && cached == 0 && reasoning == 0 && reportedTotal == 0 {
			return false
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
		model := strings.TrimSpace(modelHint)
		if model == "" {
			model = strings.TrimSpace(firstMapString(asStringKeyMap(value), "model", "modelVersion"))
		}
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
		return true
	}
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
			var latestSnapshot map[string]any
			fileHasUsage := false
			for _, line := range strings.Split(string(payload), "\n") {
				var value map[string]any
				if json.Unmarshal([]byte(line), &value) != nil {
					continue
				}
				// $set is a full document snapshot. Retain only the latest snapshot
				// and use it as a fallback when the file has no incremental token
				// records; counting every snapshot would multiply usage.
				if snapshot, ok := value["$set"].(map[string]any); ok {
					latestSnapshot = snapshot
					continue
				}
				kind := strings.ToLower(stringFromAny(value["type"]))
				if kind == "user" || kind == "gemini" {
					result.Messages++
				}
				if addUsage(value, firstMapString(value, "model", "modelVersion")) {
					fileHasUsage = true
				}
			}
			if !fileHasUsage && latestSnapshot != nil {
				_ = addUsage(latestSnapshot, firstMapString(latestSnapshot, "model", "modelVersion"))
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
					*input += firstPositiveInt64(tokenMap, "input", "input_tokens", "prompt", "prompt_tokens")
					*output += firstPositiveInt64(tokenMap, "output", "output_tokens", "completion", "completion_tokens")
					*reasoning += firstPositiveInt64(tokenMap, "reasoning", "reasoning_tokens", "thoughts", "thoughts_tokens")
					*total += firstPositiveInt64(tokenMap, "total", "total_tokens", "totalTokenCount")
					if cache, ok := tokenMap["cache"].(map[string]any); ok {
						*cached += firstPositiveInt64(cache, "read", "cached", "input", "tokens")
					} else {
						*cached += firstPositiveInt64(tokenMap, "cached", "cached_tokens", "cache_read", "cache_read_input_tokens")
					}
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
