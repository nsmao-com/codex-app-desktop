//go:build ignore
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"nice_codex_desktop/internal/codex"
)

func main() {
	detection := codex.Detect()
	providers := detectAgentProviders(detection)

	type row struct {
		Kind         string `json:"kind"`
		Name         string `json:"name"`
		RuntimeReady bool   `json:"runtimeReady"`
		Status       string `json:"status"`
		ModelCount   int    `json:"modelCount"`
		Models       []string `json:"models"`
		BadModels    []string `json:"badModels"`
	}

	out := make([]row, 0, len(providers))
	problems := make([]string, 0)
	expectedOrder := []string{"codex", "claude", "gemini", "grok"}
	byKind := map[string]AgentProviderRuntime{}
	for _, provider := range providers {
		byKind[provider.Kind] = provider
	}
	for _, kind := range expectedOrder {
		provider, ok := byKind[kind]
		if !ok {
			problems = append(problems, "missing runtime: "+kind)
			continue
		}
		name := map[string]string{
			"codex": "Codex", "claude": "Claude Code", "gemini": "Gemini", "grok": "Grok",
		}[kind]
		if provider.Name != name && !(kind == "claude" && provider.Name == "Claude Code") {
			// Accept exact workbench labels only.
			if provider.Name != name {
				problems = append(problems, fmt.Sprintf("%s label=%q want %q", kind, provider.Name, name))
			}
		}
		models := make([]string, 0, len(provider.Models))
		bad := make([]string, 0)
		for _, model := range provider.Models {
			label := model.DisplayName
			if label == "" {
				label = model.Model
			}
			models = append(models, label)
			joined := strings.ToLower(model.Model + " " + model.DisplayName)
			if strings.Contains(joined, "·") || strings.Contains(joined, "gpt-") && strings.Contains(joined, "claude") {
				bad = append(bad, label)
			}
			if kind == "codex" && (strings.Contains(joined, "claude") || strings.Contains(joined, "gemini") || strings.Contains(joined, "grok")) {
				bad = append(bad, label)
			}
			if kind == "claude" && (strings.HasPrefix(strings.ToLower(model.Model), "gpt-") || strings.Contains(joined, "·")) {
				bad = append(bad, label)
			}
		}
		if len(bad) > 0 {
			problems = append(problems, fmt.Sprintf("%s has mixed models: %v", kind, bad))
		}
		out = append(out, row{
			Kind: kind, Name: provider.Name, RuntimeReady: provider.RuntimeReady,
			Status: provider.Status, ModelCount: len(provider.Models), Models: models, BadModels: bad,
		})
	}

	result := map[string]any{
		"codexAvailable": detection.Available,
		"providers":      out,
		"problems":       problems,
		"ok":             len(problems) == 0,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
	if len(problems) > 0 {
		os.Exit(1)
	}
}
