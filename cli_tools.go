package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"nice_codex_desktop/internal/codex"
)

// CLIToolID identifies a globally-installable agent CLI.
type CLIToolID string

const (
	cliToolCodex CLIToolID = "codex"
	cliToolGrok  CLIToolID = "grok"
)

// CLIToolStatus describes install / update state for one CLI.
type CLIToolStatus struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Package         string `json:"package"`
	InstallCommand  string `json:"installCommand"`
	Installed       bool   `json:"installed"`
	Executable      string `json:"executable"`
	Version         string `json:"version"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	PackageManager  string `json:"packageManager"`
	Message         string `json:"message"`
	CanInstall      bool   `json:"canInstall"`
	NodeAvailable   bool   `json:"nodeAvailable"`
}

// CLIToolsReport is the aggregate response for CheckCLITools.
type CLIToolsReport struct {
	Tools          []CLIToolStatus `json:"tools"`
	PackageManager string          `json:"packageManager"`
	NodeAvailable  bool            `json:"nodeAvailable"`
	NodeVersion    string          `json:"nodeVersion"`
	CheckedAt      int64           `json:"checkedAt"`
	// Platform is GOOS: windows | darwin | linux — for UI install hints.
	Platform string `json:"platform"`
	// Config homes (env override aware) so Settings can show real paths on each OS.
	CodexHome string `json:"codexHome"`
	GrokHome  string `json:"grokHome"`
}

// CLIToolActionResult is returned after install / update.
type CLIToolActionResult struct {
	OK      bool          `json:"ok"`
	Message string        `json:"message"`
	Output  string        `json:"output"`
	Tool    CLIToolStatus `json:"tool"`
}

type cliPackageSpec struct {
	id      CLIToolID
	name    string
	npmPkg  string
	binName string
}

var cliPackages = []cliPackageSpec{
	{id: cliToolCodex, name: "Codex CLI", npmPkg: "@openai/codex", binName: "codex"},
	{id: cliToolGrok, name: "Grok CLI", npmPkg: "@xai-official/grok", binName: "grok"},
}

var (
	cliInstallMu   sync.Mutex
	cliInstallBusy = map[string]bool{}
	semverInText   = regexp.MustCompile(`(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)`)
)

// CheckCLITools detects local Codex/Grok CLIs and queries npm for latest versions.
func (s *AppService) CheckCLITools() CLIToolsReport {
	codex.EnrichPathForLookups()
	pm, nodeOK, nodeVer := detectNodePackageManager()
	tools := make([]CLIToolStatus, 0, len(cliPackages))
	for _, spec := range cliPackages {
		tools = append(tools, probeCLITool(spec, pm, nodeOK))
	}
	return CLIToolsReport{
		Tools:          tools,
		PackageManager: pm,
		NodeAvailable:  nodeOK,
		NodeVersion:    nodeVer,
		CheckedAt:      time.Now().Unix(),
		Platform:       runtime.GOOS,
		CodexHome:      resolveCodexHome(),
		GrokHome:       resolveGrokHome(),
	}
}

// InstallCLITool installs or upgrades a CLI via pnpm (global).
func (s *AppService) InstallCLITool(toolID string) (CLIToolActionResult, error) {
	toolID = strings.ToLower(strings.TrimSpace(toolID))
	spec, ok := lookupCLIPackage(toolID)
	if !ok {
		return CLIToolActionResult{}, fmt.Errorf("unknown CLI tool: %s", toolID)
	}

	cliInstallMu.Lock()
	if cliInstallBusy[toolID] {
		cliInstallMu.Unlock()
		return CLIToolActionResult{}, errors.New("this CLI is already installing")
	}
	cliInstallBusy[toolID] = true
	cliInstallMu.Unlock()
	defer func() {
		cliInstallMu.Lock()
		delete(cliInstallBusy, toolID)
		cliInstallMu.Unlock()
	}()

	codex.EnrichPathForLookups()
	pm, nodeOK, _ := detectNodePackageManager()
	if !nodeOK || pm == "" {
		return CLIToolActionResult{
			OK:      false,
			Message: "Node.js / pnpm was not found. Install pnpm first; NiceCodex configures its global directory automatically.",
			Tool:    probeCLITool(spec, pm, false),
		}, errors.New("node package manager not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// Ensure child process inherits the enriched PATH (critical on macOS GUI).
	codex.EnrichPathForLookups()
	if pm != "pnpm" {
		return CLIToolActionResult{}, errors.New("pnpm is required to install CLI tools")
	}
	installEnv, pnpmHome, envErr := preparePNPMGlobalEnvironment()
	if envErr != nil {
		return CLIToolActionResult{}, fmt.Errorf("prepare pnpm global directory: %w", envErr)
	}
	commandArgs := []string{"add", "-g", spec.npmPkg + "@latest"}
	commandPath, resolvedArgs, resolveErr := providerCommand(packageManagerBinary(pm), commandArgs)
	if resolveErr != nil {
		return CLIToolActionResult{}, resolveErr
	}
	cmd := exec.CommandContext(ctx, commandPath, resolvedArgs...)
	cmd.Env = installEnv

	output, err := runManagedCombinedOutput(ctx, cmd)
	text := strings.TrimSpace(string(output))
	if len(text) > 8000 {
		text = text[len(text)-8000:]
	}

	// Refresh PATH so newly installed globals are visible to GUI process.
	codex.EnrichPathForLookups()
	// Also re-detect agent providers for UI badges.
	detection := codex.Detect()
	providers := detectAgentProviders(detection)
	s.mu.Lock()
	s.agentProviders = providers
	s.mu.Unlock()
	_ = s.RefreshGrokRuntime()

	tool := probeCLITool(spec, pm, true)
	if err != nil {
		msg := fmt.Sprintf("Install failed via %s: %v", pm, err)
		if text != "" {
			msg = msg + "\n" + firstOutputLines(text, 8)
		}
		return CLIToolActionResult{
			OK:      false,
			Message: msg,
			Output:  text,
			Tool:    tool,
		}, errors.New(msg)
	}
	if !tool.Installed {
		return CLIToolActionResult{
			OK:      false,
			Message: fmt.Sprintf("Install finished but CLI was not found in pnpm home (%s). Restart Nice Codex and check the pnpm installation.", pnpmHome),
			Output:  text,
			Tool:    tool,
		}, errors.New("cli not found after install")
	}
	return CLIToolActionResult{
		OK:      true,
		Message: fmt.Sprintf("%s is ready (%s)", tool.Name, firstNonEmpty(tool.Version, tool.LatestVersion, "ok")),
		Output:  text,
		Tool:    tool,
	}, nil
}

func lookupCLIPackage(id string) (cliPackageSpec, bool) {
	for _, spec := range cliPackages {
		if string(spec.id) == id {
			return spec, true
		}
	}
	return cliPackageSpec{}, false
}

func probeCLITool(spec cliPackageSpec, pm string, nodeOK bool) CLIToolStatus {
	status := CLIToolStatus{
		ID:             string(spec.id),
		Name:           spec.name,
		Package:        spec.npmPkg,
		PackageManager: pm,
		NodeAvailable:  nodeOK,
		CanInstall:     nodeOK && pm != "",
		InstallCommand: formatInstallCommand(pm, spec.npmPkg),
	}

	switch spec.id {
	case cliToolCodex:
		det := codex.Detect()
		status.Installed = det.Available
		status.Executable = det.Binary
		status.Version = normalizeCLIVersion(det.Version)
	case cliToolGrok:
		gr := detectGrokRuntime()
		status.Installed = gr.BuildAvailable
		status.Executable = gr.BuildExecutable
		status.Version = normalizeCLIVersion(gr.BuildVersion)
	}

	latest, err := fetchNPMLatestVersion(spec.npmPkg)
	if err == nil {
		status.LatestVersion = latest
		if status.Installed && status.Version != "" && latest != "" {
			status.UpdateAvailable = compareSemver(latest, status.Version) > 0
		}
	}

	switch {
	case !nodeOK:
		status.Message = "Install Node.js and pnpm first"
	case pm == "":
		status.Message = "Install pnpm first"
	case !status.Installed:
		status.Message = "Not installed"
	case status.UpdateAvailable:
		status.Message = fmt.Sprintf("Update available: %s → %s", status.Version, status.LatestVersion)
	default:
		status.Message = "Up to date"
	}
	return status
}

func formatInstallCommand(pm, npmPkg string) string {
	if pm == "pnpm" {
		return "pnpm add -g " + npmPkg
	}
	return "pnpm add -g " + npmPkg
}

func detectNodePackageManager() (manager string, nodeOK bool, nodeVersion string) {
	nodePath := findCommand(commandCandidates("node"))
	if nodePath != "" {
		nodeOK = true
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, nodePath, "--version")
		configureBackgroundProcess(command)
		if out, err := command.CombinedOutput(); err == nil {
			nodeVersion = strings.TrimSpace(string(out))
		}
	}
	if findCommand(commandCandidates("pnpm")) != "" {
		return "pnpm", true, nodeVersion
	}
	return "", nodeOK, nodeVersion
}

func preparePNPMGlobalEnvironment() ([]string, string, error) {
	pnpmHome := strings.TrimSpace(os.Getenv("PNPM_HOME"))
	if pnpmHome == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return nil, "", errors.New("user home directory is unavailable")
		}
		switch runtime.GOOS {
		case "windows":
			pnpmHome = strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
			if pnpmHome != "" {
				pnpmHome = filepath.Join(pnpmHome, "pnpm")
			} else {
				pnpmHome = filepath.Join(home, "AppData", "Local", "pnpm")
			}
		case "darwin":
			pnpmHome = filepath.Join(home, "Library", "pnpm")
		default:
			if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
				pnpmHome = filepath.Join(dataHome, "pnpm")
			} else {
				pnpmHome = filepath.Join(home, ".local", "share", "pnpm")
			}
		}
	}
	pnpmHome = filepath.Clean(pnpmHome)
	if err := os.MkdirAll(pnpmHome, 0o700); err != nil {
		return nil, "", err
	}

	pathValue := prependPathDirectory(os.Getenv("PATH"), pnpmHome)
	// Keep the freshly installed shim discoverable for the remainder of this GUI run.
	_ = os.Setenv("PNPM_HOME", pnpmHome)
	_ = os.Setenv("PATH", pathValue)
	environment := replaceEnvironmentValue(os.Environ(), "PNPM_HOME", pnpmHome)
	environment = replaceEnvironmentValue(environment, "PATH", pathValue)
	return environment, pnpmHome, nil
}

func prependPathDirectory(value, directory string) string {
	for _, item := range filepath.SplitList(value) {
		if strings.EqualFold(filepath.Clean(item), directory) {
			return value
		}
	}
	if strings.TrimSpace(value) == "" {
		return directory
	}
	return directory + string(os.PathListSeparator) + value
}

func replaceEnvironmentValue(environment []string, key, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, key) {
			continue
		}
		next = append(next, entry)
	}
	return append(next, prefix+value)
}

func packageManagerBinary(name string) string {
	if path := findCommand(commandCandidates(name)); path != "" {
		return path
	}
	return name
}

func fetchNPMLatestVersion(npmPkg string) (string, error) {
	npmPkg = strings.TrimSpace(npmPkg)
	if npmPkg == "" {
		return "", errors.New("empty package")
	}
	// Scoped packages: @scope/name → registry.npmjs.org/@scope%2Fname/latest
	encoded := strings.ReplaceAll(npmPkg, "/", "%2F")
	url := "https://registry.npmjs.org/" + encoded + "/latest"
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "NiceCodex/"+AppVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("npm registry status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Version), nil
}

func normalizeCLIVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if match := semverInText.FindStringSubmatch(value); len(match) > 1 {
		return match[1]
	}
	// Drop common prefixes
	value = strings.TrimPrefix(value, "v")
	fields := strings.Fields(value)
	if len(fields) > 0 {
		last := fields[len(fields)-1]
		last = strings.TrimPrefix(last, "v")
		if semverInText.MatchString(last) {
			return semverInText.FindString(last)
		}
	}
	return value
}

func firstOutputLines(text string, max int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= max {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(strings.Join(lines[len(lines)-max:], "\n"))
}
