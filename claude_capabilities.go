package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ClaudeMCPServerView is an MCP server discovered from Claude Code config.
type ClaudeMCPServerView struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Command   string `json:"command"`
	Args      string `json:"args"`
	Transport string `json:"transport"`
	URL       string `json:"url"`
	Scope     string `json:"scope"` // user | project
	Source    string `json:"source"`
}

// ClaudeSkillView is a Claude skill (SKILL.md) under ~/.claude/skills or project.
type ClaudeSkillView struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Scope       string `json:"scope"` // user | project | plugin
}

// ClaudePluginView is an installed Claude Code plugin.
type ClaudePluginView struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Scope   string `json:"scope"`
	Path    string `json:"path"`
}

// ClaudeAgentView is a custom agent under ~/.claude/agents or project.
type ClaudeAgentView struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Scope       string `json:"scope"`
}

// ClaudeCommandView is a slash command / skill alias under commands/.
type ClaudeCommandView struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Scope string `json:"scope"`
}

// ClaudeHookView summarizes configured hooks from settings.json.
type ClaudeHookView struct {
	Event   string `json:"event"`
	Command string `json:"command"`
	Source  string `json:"source"`
}

// ClaudeSettingsSummary is a safe subset of ~/.claude/settings.json for the UI.
type ClaudeSettingsSummary struct {
	Path              string   `json:"path"`
	Exists            bool     `json:"exists"`
	Model             string   `json:"model"`
	PermissionMode    string   `json:"permissionMode"`
	AllowRules        int      `json:"allowRules"`
	DenyRules         int      `json:"denyRules"`
	EnvKeys           []string `json:"envKeys"`
	BaseURL           string   `json:"baseURL"`
	SkipDangerPrompt  bool     `json:"skipDangerPrompt"`
	HasStatusLine     bool     `json:"hasStatusLine"`
	RawPermissionMode string   `json:"rawPermissionMode"`
}

// ClaudeCapabilitiesCatalog powers the Claude capability center (aligned with ~/.claude layout).
type ClaudeCapabilitiesCatalog struct {
	Runtime             ClaudeRuntimeStatus     `json:"runtime"`
	ConfigPath          string                  `json:"configPath"`
	ClaudeHome          string                  `json:"claudeHome"`
	ClaudeJSONPath      string                  `json:"claudeJsonPath"`
	Settings            ClaudeSettingsSummary   `json:"settings"`
	MCP                 []ClaudeMCPServerView   `json:"mcp"`
	Skills              []ClaudeSkillView       `json:"skills"`
	Plugins             []ClaudePluginView      `json:"plugins"`
	Agents              []ClaudeAgentView       `json:"agents"`
	Commands            []ClaudeCommandView     `json:"commands"`
	Hooks               []ClaudeHookView        `json:"hooks"`
	GlobalInstructions  GlobalInstructionsInfo  `json:"globalInstructions"`
	ProjectInstructions ProjectInstructionsInfo `json:"projectInstructions"`
}

func (s *AppService) ReadClaudeCapabilities() ClaudeCapabilitiesCatalog {
	home := resolveClaudeHome()
	workspace := strings.TrimSpace(s.Settings().ClaudeWorkspace)
	settingsPath := filepath.Join(home, "settings.json")
	claudeJSON := resolveClaudeJSONPath()

	catalog := ClaudeCapabilitiesCatalog{
		Runtime:             detectClaudeRuntime(),
		ConfigPath:          settingsPath,
		ClaudeHome:          home,
		ClaudeJSONPath:      claudeJSON,
		Settings:            summarizeClaudeSettings(settingsPath),
		MCP:                 listClaudeMCPServers(home, workspace, claudeJSON),
		Skills:              listClaudeSkills(home, workspace),
		Plugins:             listClaudePlugins(home),
		Agents:              listClaudeAgents(home, workspace),
		Commands:            listClaudeCommands(home, workspace),
		Hooks:               listClaudeHooks(settingsPath, workspace),
		GlobalInstructions:  s.ReadClaudeGlobalInstructions(),
		ProjectInstructions: s.ReadClaudeProjectInstructions(),
	}
	if catalog.MCP == nil {
		catalog.MCP = []ClaudeMCPServerView{}
	}
	if catalog.Skills == nil {
		catalog.Skills = []ClaudeSkillView{}
	}
	if catalog.Plugins == nil {
		catalog.Plugins = []ClaudePluginView{}
	}
	if catalog.Agents == nil {
		catalog.Agents = []ClaudeAgentView{}
	}
	if catalog.Commands == nil {
		catalog.Commands = []ClaudeCommandView{}
	}
	if catalog.Hooks == nil {
		catalog.Hooks = []ClaudeHookView{}
	}
	if catalog.Settings.EnvKeys == nil {
		catalog.Settings.EnvKeys = []string{}
	}
	return catalog
}

func resolveClaudeJSONPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	// Official Claude Code user state often lives at ~/.claude.json (next to ~/.claude/).
	return filepath.Join(home, ".claude.json")
}

func summarizeClaudeSettings(path string) ClaudeSettingsSummary {
	summary := ClaudeSettingsSummary{Path: path, EnvKeys: []string{}}
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) == 0 {
		return summary
	}
	summary.Exists = true
	var raw map[string]any
	if json.Unmarshal(payload, &raw) != nil {
		return summary
	}
	summary.Model = firstString(raw, "model", "defaultModel")
	if perm, ok := raw["permissions"].(map[string]any); ok {
		if mode := firstString(perm, "defaultMode", "mode", "permissionMode"); mode != "" {
			summary.PermissionMode = mode
			summary.RawPermissionMode = mode
		}
		if allow, ok := perm["allow"].([]any); ok {
			summary.AllowRules = len(allow)
		}
		if deny, ok := perm["deny"].([]any); ok {
			summary.DenyRules = len(deny)
		}
	}
	if mode := firstString(raw, "permissionMode", "defaultPermissionMode"); mode != "" && summary.PermissionMode == "" {
		summary.PermissionMode = mode
		summary.RawPermissionMode = mode
	}
	if env, ok := raw["env"].(map[string]any); ok {
		keys := make([]string, 0, len(env))
		for key, value := range env {
			keys = append(keys, key)
			upper := strings.ToUpper(key)
			if upper == "ANTHROPIC_BASE_URL" || upper == "ANTHROPIC_API_BASE" {
				if text, ok := value.(string); ok {
					summary.BaseURL = strings.TrimSpace(text)
				}
			}
		}
		sort.Strings(keys)
		// Never return secret values — keys only.
		summary.EnvKeys = keys
	}
	if v, ok := raw["skipDangerousModePermissionPrompt"].(bool); ok {
		summary.SkipDangerPrompt = v
	}
	if _, ok := raw["statusLine"]; ok {
		summary.HasStatusLine = true
	}
	return summary
}

func listClaudeMCPServers(home, workspace, claudeJSON string) []ClaudeMCPServerView {
	result := make([]ClaudeMCPServerView, 0)
	seen := map[string]struct{}{}
	addFromMap := func(servers map[string]any, scope, source string) {
		for name, value := range servers {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			view := ClaudeMCPServerView{
				Name:    name,
				Enabled: true,
				Scope:   scope,
				Source:  source,
			}
			if cfg, ok := value.(map[string]any); ok {
				view.Command = firstString(cfg, "command")
				view.URL = firstString(cfg, "url", "serverUrl")
				view.Transport = firstString(cfg, "type", "transport")
				if view.Transport == "" {
					if view.URL != "" {
						view.Transport = "http"
					} else if view.Command != "" {
						view.Transport = "stdio"
					}
				}
				if args, ok := cfg["args"].([]any); ok {
					parts := make([]string, 0, len(args))
					for _, arg := range args {
						if text, ok := arg.(string); ok {
							parts = append(parts, text)
						}
					}
					view.Args = strings.Join(parts, " ")
				}
				if enabled, ok := cfg["enabled"].(bool); ok {
					view.Enabled = enabled
				}
				if disabled, ok := cfg["disabled"].(bool); ok && disabled {
					view.Enabled = false
				}
			}
			result = append(result, view)
		}
	}
	// User-level: ~/.claude.json (primary for many installs) then settings.json.
	if raw := readJSONMap(claudeJSON); raw != nil {
		if servers, ok := raw["mcpServers"].(map[string]any); ok {
			addFromMap(servers, "user", claudeJSON)
		}
	}
	settingsPath := filepath.Join(home, "settings.json")
	if raw := readJSONMap(settingsPath); raw != nil {
		if servers, ok := raw["mcpServers"].(map[string]any); ok {
			addFromMap(servers, "user", settingsPath)
		}
	}
	// Project-level: .mcp.json and .claude/settings.json
	if workspace != "" {
		for _, rel := range []string{".mcp.json", filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "settings.local.json")} {
			path := filepath.Join(workspace, rel)
			raw := readJSONMap(path)
			if raw == nil {
				continue
			}
			if servers, ok := raw["mcpServers"].(map[string]any); ok {
				addFromMap(servers, "project", path)
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func listClaudeSkills(home, workspace string) []ClaudeSkillView {
	result := make([]ClaudeSkillView, 0)
	seen := map[string]struct{}{}
	addDir := func(root, scope string) {
		if strings.TrimSpace(root) == "" {
			return
		}
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			if !strings.EqualFold(entry.Name(), "SKILL.md") {
				return nil
			}
			dir := filepath.Dir(path)
			name := filepath.Base(dir)
			key := strings.ToLower(name + "|" + scope)
			if _, exists := seen[key]; exists {
				return nil
			}
			seen[key] = struct{}{}
			display, desc := peekSkillMeta(path)
			if display == "" {
				display = name
			}
			result = append(result, ClaudeSkillView{
				Name: name, DisplayName: display, Description: desc, Path: path, Scope: scope,
			})
			return nil
		})
	}
	addDir(filepath.Join(home, "skills"), "user")
	// Plugin-bundled skills cache
	addDir(filepath.Join(home, "plugins", "cache"), "plugin")
	if workspace != "" {
		addDir(filepath.Join(workspace, ".claude", "skills"), "project")
		addDir(filepath.Join(workspace, ".agents", "skills"), "project")
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func listClaudePlugins(home string) []ClaudePluginView {
	path := filepath.Join(home, "plugins", "installed_plugins.json")
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) == 0 {
		// Fallback: list cache directories
		return listClaudePluginsFromCache(home)
	}
	var root map[string]any
	if json.Unmarshal(payload, &root) != nil {
		return listClaudePluginsFromCache(home)
	}
	plugins, _ := root["plugins"].(map[string]any)
	if plugins == nil {
		return listClaudePluginsFromCache(home)
	}
	result := make([]ClaudePluginView, 0, len(plugins))
	for name, value := range plugins {
		view := ClaudePluginView{Name: name}
		// value is usually an array of install records
		switch typed := value.(type) {
		case []any:
			if len(typed) > 0 {
				if rec, ok := typed[0].(map[string]any); ok {
					view.Version = firstString(rec, "version")
					view.Scope = firstString(rec, "scope")
					view.Path = firstString(rec, "installPath")
				}
			}
		case map[string]any:
			view.Version = firstString(typed, "version")
			view.Scope = firstString(typed, "scope")
			view.Path = firstString(typed, "installPath", "path")
		}
		if view.Path == "" {
			view.Path = filepath.Join(home, "plugins", "cache")
		}
		result = append(result, view)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func listClaudePluginsFromCache(home string) []ClaudePluginView {
	root := filepath.Join(home, "plugins", "cache")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	result := make([]ClaudePluginView, 0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		// marketplace folder → plugin folders
		marketPath := filepath.Join(root, entry.Name())
		sub, err := os.ReadDir(marketPath)
		if err != nil {
			result = append(result, ClaudePluginView{Name: entry.Name(), Path: marketPath, Scope: "cache"})
			continue
		}
		for _, plugin := range sub {
			if !plugin.IsDir() || strings.HasPrefix(plugin.Name(), ".") {
				continue
			}
			result = append(result, ClaudePluginView{
				Name:  plugin.Name() + "@" + entry.Name(),
				Path:  filepath.Join(marketPath, plugin.Name()),
				Scope: "cache",
			})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func listClaudeAgents(home, workspace string) []ClaudeAgentView {
	result := make([]ClaudeAgentView, 0)
	seen := map[string]struct{}{}
	addDir := func(root, scope string) {
		if strings.TrimSpace(root) == "" {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			path := filepath.Join(root, name)
			if entry.IsDir() {
				// Prefer agent.md / AGENT.md inside folder
				for _, candidate := range []string{"agent.md", "AGENT.md", "AGENTS.md", name + ".md"} {
					try := filepath.Join(path, candidate)
					if _, err := os.Stat(try); err == nil {
						path = try
						break
					}
				}
			} else if !strings.HasSuffix(strings.ToLower(name), ".md") && !strings.HasSuffix(strings.ToLower(name), ".json") {
				continue
			}
			base := strings.TrimSuffix(name, filepath.Ext(name))
			if entry.IsDir() {
				base = name
			}
			key := strings.ToLower(base + "|" + scope)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			display, desc := peekMarkdownMeta(path)
			if display == "" {
				display = base
			}
			result = append(result, ClaudeAgentView{
				Name: base, DisplayName: display, Description: desc, Path: path, Scope: scope,
			})
		}
	}
	addDir(filepath.Join(home, "agents"), "user")
	if workspace != "" {
		addDir(filepath.Join(workspace, ".claude", "agents"), "project")
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func listClaudeCommands(home, workspace string) []ClaudeCommandView {
	result := make([]ClaudeCommandView, 0)
	seen := map[string]struct{}{}
	addDir := func(root, scope string) {
		if strings.TrimSpace(root) == "" {
			return
		}
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			lower := strings.ToLower(entry.Name())
			if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".toml") && !strings.HasSuffix(lower, ".json") {
				return nil
			}
			name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			key := strings.ToLower(name + "|" + scope)
			if _, exists := seen[key]; exists {
				return nil
			}
			seen[key] = struct{}{}
			result = append(result, ClaudeCommandView{Name: name, Path: path, Scope: scope})
			return nil
		})
	}
	addDir(filepath.Join(home, "commands"), "user")
	if workspace != "" {
		addDir(filepath.Join(workspace, ".claude", "commands"), "project")
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func listClaudeHooks(settingsPath, workspace string) []ClaudeHookView {
	result := make([]ClaudeHookView, 0)
	collect := func(path string) {
		raw := readJSONMap(path)
		if raw == nil {
			return
		}
		hooks, ok := raw["hooks"].(map[string]any)
		if !ok {
			return
		}
		for event, value := range hooks {
			switch typed := value.(type) {
			case []any:
				for _, item := range typed {
					cmd := extractHookCommand(item)
					if cmd == "" {
						continue
					}
					result = append(result, ClaudeHookView{Event: event, Command: cmd, Source: path})
				}
			case map[string]any:
				if cmd := extractHookCommand(typed); cmd != "" {
					result = append(result, ClaudeHookView{Event: event, Command: cmd, Source: path})
				}
			case string:
				if strings.TrimSpace(typed) != "" {
					result = append(result, ClaudeHookView{Event: event, Command: typed, Source: path})
				}
			}
		}
	}
	collect(settingsPath)
	if workspace != "" {
		collect(filepath.Join(workspace, ".claude", "settings.json"))
		collect(filepath.Join(workspace, ".claude", "settings.local.json"))
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Event != result[j].Event {
			return result[i].Event < result[j].Event
		}
		return result[i].Command < result[j].Command
	})
	return result
}

func extractHookCommand(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		if cmd := firstString(typed, "command", "cmd", "run"); cmd != "" {
			return cmd
		}
		// Nested matcher → hooks arrays (Claude hooks schema variants)
		if hooks, ok := typed["hooks"].([]any); ok {
			for _, hook := range hooks {
				if cmd := extractHookCommand(hook); cmd != "" {
					return cmd
				}
			}
		}
	}
	return ""
}

func readJSONMap(path string) map[string]any {
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) == 0 || len(payload) > 8*1024*1024 {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(payload, &raw) != nil {
		return nil
	}
	return raw
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := m[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func peekMarkdownMeta(path string) (displayName, description string) {
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) == 0 {
		return "", ""
	}
	text := string(payload)
	if strings.HasPrefix(text, "---") {
		parts := strings.SplitN(text, "---", 3)
		if len(parts) >= 3 {
			for _, line := range strings.Split(parts[1], "\n") {
				line = strings.TrimSpace(line)
				lower := strings.ToLower(line)
				if strings.HasPrefix(lower, "name:") {
					displayName = strings.Trim(strings.TrimSpace(line[5:]), `"'`)
				}
				if strings.HasPrefix(lower, "description:") {
					description = strings.Trim(strings.TrimSpace(line[len("description:"):]), `"'`)
				}
			}
			text = parts[2]
		}
	}
	if displayName == "" || description == "" {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "#") && displayName == "" {
				displayName = strings.TrimSpace(strings.TrimPrefix(line, "#"))
				continue
			}
			if description == "" && !strings.HasPrefix(line, "#") {
				description = line
				break
			}
		}
	}
	if len(description) > 160 {
		description = description[:160] + "…"
	}
	return displayName, description
}
