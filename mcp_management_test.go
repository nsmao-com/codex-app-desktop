package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The single-server upsert/remove APIs must preserve unrelated keys and
// unrelated servers in the native Gemini/OpenCode config files. Paths are
// redirected into a temp home via GEMINI_CLI_HOME / OPENCODE_CONFIG.
func TestUpsertAndRemoveExternalMCPServer(t *testing.T) {
	dir := t.TempDir()
	geminiHome := filepath.Join(dir, "gemini")
	opencodeDir := filepath.Join(dir, "opencode")
	if err := os.MkdirAll(geminiHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(opencodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_CLI_HOME", geminiHome)
	t.Setenv("OPENCODE_CONFIG", filepath.Join(opencodeDir, "opencode.json"))

	// Seed the config selected by the runtime on this machine. Developers may
	// already have Antigravity installed, while CI normally exercises legacy
	// Gemini; both paths must preserve unrelated settings and servers.
	geminiPath := externalRuntimeConfigPath("gemini", "global", "")
	if err := os.MkdirAll(filepath.Dir(geminiPath), 0o755); err != nil {
		t.Fatal(err)
	}
	usingAntigravity := geminiUsesAntigravity(geminiHome)
	seed := map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"keep-me": map[string]any{"type": "stdio", "command": "keep-cmd"},
		},
	}
	seeded, _ := json.Marshal(seed)
	if err := os.WriteFile(geminiPath, seeded, 0o644); err != nil {
		t.Fatal(err)
	}

	service := &AppService{}
	input := MCPServerInput{
		Name:      "added-server",
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "server-pkg"},
		Env:       map[string]string{"KEY": "value"},
	}
	if err := service.UpsertExternalMCPServer("gemini", "global", input); err != nil {
		t.Fatalf("upsert gemini: %v", err)
	}

	var after map[string]any
	payload, err := os.ReadFile(geminiPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	if after["theme"] != "dark" {
		t.Fatalf("unrelated top-level key lost: %v", after)
	}
	servers, _ := after["mcpServers"].(map[string]any)
	if servers["keep-me"] == nil {
		t.Fatalf("existing server keep-me was dropped: %v", servers)
	}
	added, _ := servers["added-server"].(map[string]any)
	if added == nil || added["command"] != "npx" {
		t.Fatalf("added server missing or malformed: %v", added)
	}
	if args, _ := added["args"].([]any); len(args) != 2 {
		t.Fatalf("args not persisted: %v", added)
	}

	if err := service.RemoveExternalMCPServer("gemini", "global", "added-server"); err != nil {
		t.Fatalf("remove gemini: %v", err)
	}
	payload, _ = os.ReadFile(geminiPath)
	after = map[string]any{}
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatal(err)
	}
	servers, _ = after["mcpServers"].(map[string]any)
	if _, exists := servers["added-server"]; exists {
		t.Fatalf("added-server still present after removal: %v", servers)
	}
	if servers["keep-me"] == nil {
		t.Fatalf("keep-me lost after removal: %v", servers)
	}
	if after["theme"] != "dark" {
		t.Fatalf("theme lost after removal: %v", after)
	}

	// Removing the last server deletes the key but keeps other config intact.
	if err := service.RemoveExternalMCPServer("gemini", "global", "keep-me"); err != nil {
		t.Fatalf("remove keep-me: %v", err)
	}
	payload, _ = os.ReadFile(geminiPath)
	after = map[string]any{}
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatal(err)
	}
	if _, exists := after["mcpServers"]; exists {
		t.Fatalf("empty mcpServers key should be removed: %v", after)
	}
	if after["theme"] != "dark" {
		t.Fatalf("theme lost after final removal: %v", after)
	}

	// OpenCode uses the "mcp" key and a fresh file.
	if err := service.UpsertExternalMCPServer("opencode", "global", input); err != nil {
		t.Fatalf("upsert opencode: %v", err)
	}
	payload, _ = os.ReadFile(filepath.Join(opencodeDir, "opencode.json"))
	var openCodeConfig map[string]any
	if err := json.Unmarshal(payload, &openCodeConfig); err != nil {
		t.Fatal(err)
	}
	servers, _ = openCodeConfig["mcp"].(map[string]any)
	if servers == nil || servers["added-server"] == nil {
		t.Fatalf("opencode server missing: %v", openCodeConfig)
	}

	// Removing an unknown server fails clearly instead of wiping the file.
	if err := service.RemoveExternalMCPServer("opencode", "global", "nope"); err == nil {
		t.Fatal("expected error removing unknown server")
	}
	payload, _ = os.ReadFile(filepath.Join(opencodeDir, "opencode.json"))
	openCodeConfig = map[string]any{}
	if err := json.Unmarshal(payload, &openCodeConfig); err != nil {
		t.Fatal(err)
	}
	servers, _ = openCodeConfig["mcp"].(map[string]any)
	if servers == nil || servers["added-server"] == nil {
		t.Fatalf("config wiped by failed removal: %v", openCodeConfig)
	}

	// Remote transport writes a URL server.
	remote := MCPServerInput{Name: "remote-server", Transport: "http", Command: "https://example.com/mcp"}
	if err := service.UpsertExternalMCPServer("gemini", "global", remote); err != nil {
		t.Fatalf("upsert remote: %v", err)
	}
	payload, _ = os.ReadFile(geminiPath)
	after = map[string]any{}
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatal(err)
	}
	servers, _ = after["mcpServers"].(map[string]any)
	entry, _ := servers["remote-server"].(map[string]any)
	urlKey := "url"
	if usingAntigravity {
		urlKey = "serverUrl"
	}
	if entry == nil || entry[urlKey] != "https://example.com/mcp" {
		t.Fatalf("remote server malformed: %v", entry)
	}
}

func TestMCPServerInputValidation(t *testing.T) {
	if _, err := validateMCPServerName("has space"); err == nil {
		t.Fatal("whitespace name must be rejected")
	}
	if _, err := validateMCPServerName(""); err == nil {
		t.Fatal("empty name must be rejected")
	}
	if _, err := (MCPServerInput{Name: "x", Transport: "stdio"}).serverObject(); err == nil {
		t.Fatal("stdio without command must be rejected")
	}
	if _, err := (MCPServerInput{Name: "x", Transport: "http", Command: "not-a-url"}).serverObject(); err == nil {
		t.Fatal("http without URL must be rejected")
	}
	object, err := (MCPServerInput{Name: "x", Transport: "http", Command: "https://ok/mcp"}).serverObject()
	if err != nil || object["url"] != "https://ok/mcp" {
		t.Fatalf("valid http input rejected: %v %v", object, err)
	}
}
