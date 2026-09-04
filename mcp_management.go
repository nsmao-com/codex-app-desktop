package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// MCPServerInput is the provider-agnostic MCP server definition used by the
// capability center add/edit dialog. Command holds the stdio launch command or
// the remote URL depending on Transport; JSON, when set, overrides the derived
// object entirely (advanced mode).
type MCPServerInput struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // "stdio" (default) | "http" | "sse"
	Command   string            `json:"command"`   // stdio command or remote URL
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Headers   map[string]string `json:"headers"`
	JSON      string            `json:"json"`
}

func (input MCPServerInput) normalizedTransport() string {
	switch strings.ToLower(strings.TrimSpace(input.Transport)) {
	case "http", "sse":
		return strings.ToLower(strings.TrimSpace(input.Transport))
	default:
		return "stdio"
	}
}

func (input MCPServerInput) serverObject() (map[string]any, error) {
	if payload := strings.TrimSpace(input.JSON); payload != "" {
		var object map[string]any
		if err := json.Unmarshal([]byte(payload), &object); err != nil || object == nil {
			return nil, errors.New("MCP server JSON must be an object")
		}
		return object, nil
	}
	switch transport := input.normalizedTransport(); transport {
	case "http", "sse":
		url := strings.TrimSpace(input.Command)
		if url == "" {
			return nil, errors.New("server URL is required")
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return nil, errors.New("server URL must start with http:// or https://")
		}
		object := map[string]any{"type": transport, "url": url}
		if len(input.Headers) > 0 {
			object["headers"] = input.Headers
		}
		return object, nil
	default:
		command := strings.TrimSpace(input.Command)
		if command == "" {
			return nil, errors.New("server command is required")
		}
		object := map[string]any{"type": "stdio", "command": command}
		if len(input.Args) > 0 {
			object["args"] = input.Args
		}
		if len(input.Env) > 0 {
			object["env"] = input.Env
		}
		return object, nil
	}
}

func validateMCPServerName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("server name is required")
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return "", errors.New("server name must not contain whitespace")
	}
	return name, nil
}

// runProviderManagementCommand executes a provider CLI subcommand with a hard
// timeout and returns its combined output.
func (s *AppService) runProviderManagementCommand(provider string, args []string, timeout time.Duration) (string, error) {
	executable := s.externalExecutable(provider)
	if executable == "" {
		return "", fmt.Errorf("%s CLI executable was not found", provider)
	}
	commandPath, resolvedArgs, resolveErr := providerCommand(executable, args)
	if resolveErr != nil {
		return "", resolveErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, commandPath, resolvedArgs...)
	output, err := runManagedCombinedOutput(ctx, command)
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s CLI failed: %s", provider, text)
	}
	return text, nil
}

// UpsertClaudeMCPServer adds or updates a user-scope MCP server via
// `claude mcp add-json` so the CLI owns the exact settings format.
func (s *AppService) UpsertClaudeMCPServer(input MCPServerInput) error {
	name, err := validateMCPServerName(input.Name)
	if err != nil {
		return err
	}
	object, err := input.serverObject()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(object)
	if err != nil {
		return err
	}
	_, err = s.runProviderManagementCommand("claude", []string{
		"mcp", "add-json", name, string(payload), "-s", "user",
	}, 90*time.Second)
	return err
}

// RemoveClaudeMCPServer removes a user-scope MCP server.
func (s *AppService) RemoveClaudeMCPServer(name string) error {
	name, err := validateMCPServerName(name)
	if err != nil {
		return err
	}
	_, err = s.runProviderManagementCommand("claude", []string{
		"mcp", "remove", name, "-s", "user",
	}, 60*time.Second)
	return err
}

// UpsertGrokMCPServer adds or updates a user-scope MCP server via
// `grok mcp add`. The CLI writes ~/.grok/config.toml itself, which avoids
// hand-editing TOML.
func (s *AppService) UpsertGrokMCPServer(input MCPServerInput) error {
	name, err := validateMCPServerName(input.Name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input.JSON) != "" {
		return errors.New("Grok MCP servers must be defined with command/args/env, not raw JSON")
	}
	transport := input.normalizedTransport()
	args := []string{"mcp", "add"}
	if transport != "stdio" {
		args = append(args, "-t", transport)
	}
	args = append(args, "-s", "user")
	envKeys := make([]string, 0, len(input.Env))
	for key := range input.Env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	for _, key := range envKeys {
		args = append(args, "-e", key+"="+input.Env[key])
	}
	headerKeys := make([]string, 0, len(input.Headers))
	for key := range input.Headers {
		headerKeys = append(headerKeys, key)
	}
	sort.Strings(headerKeys)
	for _, key := range headerKeys {
		args = append(args, "-H", key+": "+input.Headers[key])
	}
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return errors.New("server command or URL is required")
	}
	args = append(args, name)
	if transport == "stdio" {
		args = append(args, "--", command)
		args = append(args, input.Args...)
	} else {
		args = append(args, command)
	}
	_, err = s.runProviderManagementCommand("grok", args, 90*time.Second)
	return err
}

// RemoveGrokMCPServer removes a user-scope MCP server.
func (s *AppService) RemoveGrokMCPServer(name string) error {
	name, err := validateMCPServerName(name)
	if err != nil {
		return err
	}
	_, err = s.runProviderManagementCommand("grok", []string{
		"mcp", "remove", name, "-s", "user",
	}, 60*time.Second)
	return err
}

// ClearGrokMemory wipes Grok's persisted memory (`grok memory clear`).
func (s *AppService) ClearGrokMemory() error {
	_, err := s.runProviderManagementCommand("grok", []string{"memory", "clear"}, 60*time.Second)
	return err
}

// UpsertExternalMCPServer adds or updates one MCP server in the Gemini /
// OpenCode JSON config, preserving every unrelated key.
func (s *AppService) UpsertExternalMCPServer(runtime, scope string, input MCPServerInput) error {
	runtime = normalizeExternalRuntime(runtime)
	if runtime == "" {
		return errors.New("unsupported external runtime")
	}
	name, err := validateMCPServerName(input.Name)
	if err != nil {
		return err
	}
	object, err := input.serverObject()
	if err != nil {
		return err
	}
	if runtime == "gemini" && geminiUsesAntigravity(resolveGeminiHome()) {
		object = normalizeAntigravityMCPServerObject(object)
	}
	scope, err = externalMCPScopeAndWorkspace(scope)
	if err != nil {
		return err
	}
	path := externalRuntimeConfigPath(runtime, scope, "")
	config := map[string]any{}
	_ = readLimitedJSON(path, &config)
	key := "mcp"
	if runtime == "gemini" {
		key = "mcpServers"
	}
	existing, _ := config[key].(map[string]any)
	next := make(map[string]any, len(existing)+1)
	for serverName, value := range existing {
		next[serverName] = value
	}
	next[name] = object
	config[key] = next
	payload, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writeTextFileAtomic(path, string(payload)+"\n")
}

// RemoveExternalMCPServer deletes one MCP server from the Gemini / OpenCode
// JSON config.
func (s *AppService) RemoveExternalMCPServer(runtime, scope, name string) error {
	runtime = normalizeExternalRuntime(runtime)
	if runtime == "" {
		return errors.New("unsupported external runtime")
	}
	name, err := validateMCPServerName(name)
	if err != nil {
		return err
	}
	scope, err = externalMCPScopeAndWorkspace(scope)
	if err != nil {
		return err
	}
	path := externalRuntimeConfigPath(runtime, scope, "")
	config := map[string]any{}
	_ = readLimitedJSON(path, &config)
	key := "mcp"
	if runtime == "gemini" {
		key = "mcpServers"
	}
	existing, _ := config[key].(map[string]any)
	if existing == nil || existing[name] == nil {
		return fmt.Errorf("MCP server %q was not found", name)
	}
	remaining := make(map[string]any, len(existing))
	for serverName, value := range existing {
		if serverName != name {
			remaining[serverName] = value
		}
	}
	if len(remaining) == 0 {
		delete(config, key)
	} else {
		config[key] = remaining
	}
	payload, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return writeTextFileAtomic(path, string(payload)+"\n")
}

// externalMCPScopeAndWorkspace normalizes the scope value. Only the global
// config is managed through the single-server API for now; project-scope
// editing stays in the JSON editor which already handles workspaces.
func externalMCPScopeAndWorkspace(scope string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "global":
		return "global", nil
	case "project":
		return "", errors.New("project-scope single-server editing is not supported; use the JSON editor")
	default:
		return "", errors.New("scope must be global or project")
	}
}
