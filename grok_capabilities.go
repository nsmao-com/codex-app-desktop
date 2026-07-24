package main

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GrokMCPServerView is a server entry from ~/.grok/config.toml [mcp_servers.*].
type GrokMCPServerView struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Command string `json:"command"`
	Args    string `json:"args"`
	Transport string `json:"transport"`
	URL     string `json:"url"`
}

// GrokSkillView is a discovered Grok skill (SKILL.md).
type GrokSkillView struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Scope       string `json:"scope"` // user | project
}

// GrokPluginView is an installed Grok plugin package.
type GrokPluginView struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// GrokCapabilitiesCatalog is the Grok-mode capability center payload.
type GrokCapabilitiesCatalog struct {
	Runtime   GrokRuntimeStatus  `json:"runtime"`
	ConfigPath string            `json:"configPath"`
	GrokHome  string             `json:"grokHome"`
	MCP       []GrokMCPServerView `json:"mcp"`
	Skills    []GrokSkillView    `json:"skills"`
	Plugins   []GrokPluginView   `json:"plugins"`
	GlobalInstructions GlobalInstructionsInfo `json:"globalInstructions"`
	ProjectInstructions ProjectInstructionsInfo `json:"projectInstructions"`
}

func (s *AppService) ReadGrokCapabilities() GrokCapabilitiesCatalog {
	home := resolveGrokHome()
	configPath := filepath.Join(home, "config.toml")
	catalog := GrokCapabilitiesCatalog{
		Runtime:    detectGrokRuntime(),
		ConfigPath: configPath,
		GrokHome:   home,
		MCP:        listGrokMCPServers(configPath),
		Skills:     listGrokSkills(home, s.Settings().GrokWorkspace),
		Plugins:    listGrokPlugins(home),
		GlobalInstructions: s.ReadGrokGlobalInstructions(),
		ProjectInstructions: s.ReadGrokProjectInstructions(),
	}
	if catalog.MCP == nil {
		catalog.MCP = []GrokMCPServerView{}
	}
	if catalog.Skills == nil {
		catalog.Skills = []GrokSkillView{}
	}
	if catalog.Plugins == nil {
		catalog.Plugins = []GrokPluginView{}
	}
	return catalog
}

// ReadGrokGlobalInstructions returns personal Grok rules (~/.grok/AGENTS.md).
// Official Grok Build loads home-level AGENTS*.md and ~/.grok/rules/*.md.
func (s *AppService) ReadGrokGlobalInstructions() GlobalInstructionsInfo {
	home := resolveGrokHome()
	if home == "" {
		return GlobalInstructionsInfo{}
	}
	path, source, content, exists, emptyFile := resolveGrokHomeAgentsDoc(home)
	return GlobalInstructionsInfo{
		Content: content, Path: path, Source: source,
		Exists: exists, EmptyFile: emptyFile, Available: true,
	}
}

func (s *AppService) SaveGrokGlobalInstructions(content string) (GlobalInstructionsInfo, error) {
	home := resolveGrokHome()
	if home == "" {
		return GlobalInstructionsInfo{}, os.ErrNotExist
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return GlobalInstructionsInfo{}, err
	}
	path := filepath.Join(home, "AGENTS.md")
	trimmed := sanitizeCustomInstructions(content)
	if err := os.WriteFile(path, []byte(trimmed), 0o600); err != nil {
		return GlobalInstructionsInfo{}, err
	}
	return s.ReadGrokGlobalInstructions(), nil
}

// ReadGrokProjectInstructions returns AGENTS.md for the active Grok workspace.
func (s *AppService) ReadGrokProjectInstructions() ProjectInstructionsInfo {
	workspace := strings.TrimSpace(s.Settings().GrokWorkspace)
	if workspace == "" {
		return ProjectInstructionsInfo{}
	}
	clean, err := validateWorkspace(workspace)
	if err != nil {
		return ProjectInstructionsInfo{}
	}
	path, source, content, exists, emptyFile := resolveAgentsDoc(clean)
	return ProjectInstructionsInfo{
		Content: content, Workspace: clean, WorkspaceName: filepath.Base(clean),
		Path: path, Source: source, Exists: exists, EmptyFile: emptyFile, Available: true,
	}
}

func (s *AppService) SaveGrokProjectInstructions(content string) (ProjectInstructionsInfo, error) {
	workspace := strings.TrimSpace(s.Settings().GrokWorkspace)
	if workspace == "" {
		return ProjectInstructionsInfo{}, errors.New("no Grok workspace is selected")
	}
	clean, err := validateWorkspace(workspace)
	if err != nil {
		return ProjectInstructionsInfo{}, err
	}
	if _, err := writeAgentsDoc(clean, content); err != nil {
		return ProjectInstructionsInfo{}, err
	}
	return s.ReadGrokProjectInstructions(), nil
}

func resolveGrokHomeAgentsDoc(home string) (path, source, content string, exists, emptyFile bool) {
	candidates := []string{
		filepath.Join(home, "AGENTS.md"),
		filepath.Join(home, "AGENTS.override.md"),
		filepath.Join(home, "Agents.md"),
		filepath.Join(home, "AGENT.md"),
	}
	for _, candidate := range candidates {
		payload, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		text := string(payload)
		return candidate, filepath.Base(candidate), text, true, strings.TrimSpace(text) == ""
	}
	// Prefer writing AGENTS.md next save.
	return filepath.Join(home, "AGENTS.md"), "AGENTS.md", "", false, false
}

func listGrokMCPServers(configPath string) []GrokMCPServerView {
	file, err := os.Open(configPath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var result []GrokMCPServerView
	var current *GrokMCPServerView
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if current != nil && current.Name != "" {
				result = append(result, *current)
			}
			current = nil
			header := strings.TrimSpace(line[1 : len(line)-1])
			if strings.HasPrefix(header, "mcp_servers.") {
				name := strings.TrimPrefix(header, "mcp_servers.")
				name = strings.Trim(name, `"'`)
				current = &GrokMCPServerView{Name: name, Enabled: true}
			}
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := splitTOMLAssignment(line)
		if !ok {
			continue
		}
		switch key {
		case "enabled":
			current.Enabled = value == "true" || value == "1"
		case "command":
			current.Command = unquoteTOML(value)
		case "args":
			current.Args = strings.TrimSpace(value)
		case "url":
			current.URL = unquoteTOML(value)
		case "transport":
			current.Transport = unquoteTOML(value)
		}
	}
	if current != nil && current.Name != "" {
		result = append(result, *current)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func splitTOMLAssignment(line string) (key, value string, ok bool) {
	// Strip inline comments carefully for simple cases.
	if idx := strings.Index(line, " #"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	eq := strings.Index(line, "=")
	if eq <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:eq])
	value = strings.TrimSpace(line[eq+1:])
	return key, value, key != ""
}

func unquoteTOML(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func listGrokSkills(home, workspace string) []GrokSkillView {
	result := make([]GrokSkillView, 0)
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
			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				return nil
			}
			seen[key] = struct{}{}
			display, desc := peekSkillMeta(path)
			if display == "" {
				display = name
			}
			result = append(result, GrokSkillView{
				Name: name, DisplayName: display, Description: desc, Path: path, Scope: scope,
			})
			return nil
		})
	}
	addDir(filepath.Join(home, "skills"), "user")
	addDir(filepath.Join(home, "bundled", "skills"), "bundled")
	if workspace != "" {
		addDir(filepath.Join(workspace, ".grok", "skills"), "project")
		addDir(filepath.Join(workspace, ".agents", "skills"), "project")
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func peekSkillMeta(path string) (displayName, description string) {
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) == 0 {
		return "", ""
	}
	text := string(payload)
	// Optional YAML front matter.
	if strings.HasPrefix(text, "---") {
		parts := strings.SplitN(text, "---", 3)
		if len(parts) >= 3 {
			for _, line := range strings.Split(parts[1], "\n") {
				line = strings.TrimSpace(line)
				lower := strings.ToLower(line)
				if strings.HasPrefix(lower, "name:") {
					displayName = strings.TrimSpace(line[5:])
					displayName = strings.Trim(displayName, `"'`)
				}
				if strings.HasPrefix(lower, "description:") {
					description = strings.TrimSpace(line[len("description:"):])
					description = strings.Trim(description, `"'`)
				}
			}
			text = parts[2]
		}
	}
	if description == "" {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				if strings.HasPrefix(line, "#") && displayName == "" {
					displayName = strings.TrimSpace(strings.TrimPrefix(line, "#"))
				}
				continue
			}
			description = line
			break
		}
	}
	if len(description) > 160 {
		description = description[:160] + "…"
	}
	return displayName, description
}

func listGrokPlugins(home string) []GrokPluginView {
	root := filepath.Join(home, "installed-plugins")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	result := make([]GrokPluginView, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		result = append(result, GrokPluginView{Name: name, Path: filepath.Join(root, name)})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func (s *AppService) OpenGrokConfigFile() error {
	home := resolveGrokHome()
	if home == "" {
		return errors.New("Grok home not found")
	}
	path := filepath.Join(home, "config.toml")
	if _, err := os.Stat(path); err != nil {
		// Create a minimal stub so the user can edit.
		_ = os.MkdirAll(home, 0o700)
		_ = os.WriteFile(path, []byte("# Grok Build configuration\n# See ~/.grok/docs/user-guide/05-configuration.md\n"), 0o600)
	}
	return s.OpenLocalPath(path)
}

func (s *AppService) OpenGrokHome() error {
	home := resolveGrokHome()
	if home == "" {
		return errors.New("Grok home not found")
	}
	_ = os.MkdirAll(home, 0o700)
	return s.OpenLocalPath(home)
}
