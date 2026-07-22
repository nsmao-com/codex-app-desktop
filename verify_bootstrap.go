//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Smoke-checks the same Bootstrap / ListModelProviders contracts the UI uses,
// without needing the Wails window or WebView bindings.
func main() {
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
	service.sessions = loadSessions(service.settingsPath)

	boot := service.Bootstrap()
	providers, err := service.ListModelProviders()
	if err != nil {
		fail("ListModelProviders: %v", err)
	}

	problems := []string{}
	if !boot.Codex.Available {
		problems = append(problems, "Codex CLI not available")
	}
	if len(boot.AgentProviders) != 1 || boot.AgentProviders[0].Kind != "codex" {
		problems = append(problems, fmt.Sprintf("Bootstrap agentProviders=%d want Codex-only", len(boot.AgentProviders)))
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
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
	if len(problems) > 0 {
		os.Exit(1)
	}
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

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
