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
	cliToolCodex    CLIToolID = "codex"
	cliToolClaude   CLIToolID = "claude"
	cliToolGrok     CLIToolID = "grok"
	cliToolGemini   CLIToolID = "gemini"
	cliToolOpenCode CLIToolID = "opencode"
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
	CodexHome    string `json:"codexHome"`
	ClaudeHome   string `json:"claudeHome"`
	GrokHome     string `json:"grokHome"`
	GeminiHome   string `json:"geminiHome"`
	OpenCodeHome string `json:"openCodeHome"`
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
	{id: cliToolClaude, name: "Claude Code", npmPkg: "@anthropic-ai/claude-code", binName: "claude"},
	// Grok Build is NOT managed via npm. Official install is x.ai/cli (native binary
	// under ~/.grok/bin). The npm package @xai-official/grok is a platform-limited stub
	// and must never be used for install/update or version checks.
	{id: cliToolGrok, name: "Grok Build CLI", npmPkg: "x.ai/cli", binName: "grok"},
	{id: cliToolGemini, name: "Gemini CLI", npmPkg: "@google/gemini-cli", binName: "gemini"},
	{id: cliToolOpenCode, name: "OpenCode", npmPkg: "opencode-ai", binName: "opencode"},
}

var (
	cliInstallMu     sync.Mutex
	cliPNPMInstallMu sync.Mutex
	cliInstallBusy   = map[string]bool{}
	semverInText     = regexp.MustCompile(`(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)`)
)

// CheckCLITools detects every supported local CLI and checks applicable latest versions.
func (s *AppService) CheckCLITools() CLIToolsReport {
	codex.EnrichPathForLookups()
	pm, nodeOK, nodeVer := detectNodePackageManager()
	tools := make([]CLIToolStatus, len(cliPackages))
	var probeGroup sync.WaitGroup
	for index, spec := range cliPackages {
		probeGroup.Add(1)
		go func() {
			defer probeGroup.Done()
			tools[index] = probeCLITool(spec, pm, nodeOK)
		}()
	}
	probeGroup.Wait()
	home, _ := os.UserHomeDir()
	return CLIToolsReport{
		Tools:          tools,
		PackageManager: pm,
		NodeAvailable:  nodeOK,
		NodeVersion:    nodeVer,
		CheckedAt:      time.Now().Unix(),
		Platform:       runtime.GOOS,
		CodexHome:      resolveCodexHome(),
		ClaudeHome:     resolveClaudeHome(),
		GrokHome:       resolveGrokHome(),
		GeminiHome:     resolveGeminiHome(),
		OpenCodeHome:   openCodeConfigDir(home),
	}
}

// InstallCLITool installs or upgrades a CLI.
// Codex / Claude / legacy Gemini / OpenCode use pnpm global packages.
// Antigravity and Grok Build use their official native installers/update paths.
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

	if spec.id == cliToolGrok {
		return s.installOrUpdateGrokCLI()
	}
	if spec.id == cliToolGemini {
		// New consumer installs use the native Antigravity binary (`agy`). Keep
		// updating an existing legacy Gemini CLI through pnpm when it is the only
		// binary present, so enterprise/API users are not migrated unexpectedly.
		if executable := findGeminiExecutable(); executable == "" || isAntigravityExecutable(executable) {
			return s.installOrUpdateAntigravityCLI()
		}
	}

	// All pnpm-managed providers share one global manifest and virtual store.
	// Reject overlapping installs instead of racing another provider's update.
	if !cliPNPMInstallMu.TryLock() {
		return CLIToolActionResult{}, errors.New("another CLI is installing via pnpm; wait for it to finish before trying again")
	}
	defer cliPNPMInstallMu.Unlock()

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

	before := probeCLITool(spec, pm, true)

	// pnpm 10+ blocks dependency lifecycle scripts by default. Claude Code /
	// OpenCode ship stub bin/*.exe placeholders and only install the real native
	// binary in postinstall — without --allow-build the command "succeeds" but
	// leaves a non-runnable stub (or leaves an older npm copy on PATH in use).
	commandArgs := pnpmGlobalInstallArgs(spec.npmPkg, true)
	commandPath, resolvedArgs, resolveErr := providerCommand(packageManagerBinary(pm), commandArgs)
	if resolveErr != nil {
		return CLIToolActionResult{}, resolveErr
	}
	cmd := exec.CommandContext(ctx, commandPath, resolvedArgs...)
	cmd.Env = installEnv

	output, err := runManagedCombinedOutput(ctx, cmd)
	// Older pnpm without --allow-build: retry plain install, then run postinstall ourselves.
	if err != nil && looksLikeUnknownPNPMOption(string(output), err) {
		fallbackArgs := pnpmGlobalInstallArgs(spec.npmPkg, false)
		commandPath, resolvedArgs, resolveErr = providerCommand(packageManagerBinary(pm), fallbackArgs)
		if resolveErr != nil {
			return CLIToolActionResult{}, resolveErr
		}
		cmd = exec.CommandContext(ctx, commandPath, resolvedArgs...)
		cmd.Env = installEnv
		output, err = runManagedCombinedOutput(ctx, cmd)
	}
	text := strings.TrimSpace(string(output))

	// Always run package postinstall after global add. pnpm may still skip
	// builds for some configs; Claude/OpenCode are unusable without it.
	if postText, postErr := ensureCLINativeBinary(ctx, installEnv, spec.npmPkg); postText != "" {
		if text != "" {
			text = text + "\n" + postText
		} else {
			text = postText
		}
		if err == nil && postErr != nil {
			// Install command itself succeeded but native binary placement failed.
			err = postErr
		}
	} else if postErr != nil && err == nil {
		err = postErr
	}

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
	// Binary present but unusable (postinstall still missing / wrong arch).
	if strings.TrimSpace(tool.Version) == "" {
		msg := fmt.Sprintf(
			"%s is installed but did not report a version. Native postinstall may have failed — try again, or run `pnpm add -g --allow-build=%s %s@latest` in a terminal.",
			tool.Name, spec.npmPkg, spec.npmPkg,
		)
		return CLIToolActionResult{
			OK:      false,
			Message: msg,
			Output:  text,
			Tool:    tool,
		}, errors.New(msg)
	}
	// Same guard as Grok: never report a fake "ready" while an update is still pending.
	if tool.UpdateAvailable && tool.LatestVersion != "" &&
		compareSemver(tool.LatestVersion, tool.Version) > 0 {
		msg := fmt.Sprintf(
			"%s is still at %s (latest %s). pnpm finished but the active binary was not upgraded — another install earlier on PATH may be taking priority, or postinstall did not place the new binary.",
			tool.Name, tool.Version, tool.LatestVersion,
		)
		return CLIToolActionResult{
			OK:      false,
			Message: msg,
			Output:  text,
			Tool:    tool,
		}, errors.New(msg)
	}

	versionNote := tool.Version
	if before.Version != "" && tool.Version != "" && before.Version != tool.Version {
		versionNote = before.Version + " → " + tool.Version
	}
	return CLIToolActionResult{
		OK:      true,
		Message: fmt.Sprintf("%s is ready (%s)", tool.Name, firstNonEmpty(versionNote, "ok")),
		Output:  text,
		Tool:    tool,
	}, nil
}

// installOrUpdateGrokCLI installs or upgrades the official Grok Build native CLI.
// Prefer `grok update` when a real binary exists; otherwise run the x.ai install script.
func (s *AppService) installOrUpdateGrokCLI() (CLIToolActionResult, error) {
	codex.EnrichPathForLookups()
	invalidateGrokRuntimeProbeCache()

	before := probeCLITool(cliPackages[cliPackageIndex(cliToolGrok)], "pnpm", true)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	var (
		output []byte
		err    error
		method string
	)

	// Prefer the official binary for updates — never the broken npm shim.
	exe := officialGrokBinaryPath()
	if exe == "" {
		// If only a PATH hit exists, still try `grok update` when it looks native.
		if candidate := findGrokExecutable(); candidate != "" && !looksLikeNPMGrokShim(candidate) {
			exe = candidate
		}
	}

	if exe != "" {
		method = "grok update"
		commandPath, commandArgs, resolveErr := providerCommand(exe, []string{"update"})
		if resolveErr != nil {
			return CLIToolActionResult{}, resolveErr
		}
		cmd := exec.CommandContext(ctx, commandPath, commandArgs...)
		output, err = runManagedCombinedOutput(ctx, cmd)
	} else {
		method = "official installer"
		output, err = runOfficialGrokInstaller(ctx)
	}

	text := strings.TrimSpace(string(output))
	if len(text) > 8000 {
		text = text[len(text)-8000:]
	}

	// Refresh PATH + force re-probe so UI version is not stale TTL cache.
	codex.EnrichPathForLookups()
	invalidateGrokRuntimeProbeCache()
	_ = s.RefreshGrokRuntime()
	detection := codex.Detect()
	providers := detectAgentProviders(detection)
	s.mu.Lock()
	s.agentProviders = providers
	s.mu.Unlock()

	tool := probeCLITool(cliPackages[cliPackageIndex(cliToolGrok)], "pnpm", true)
	if err != nil {
		msg := fmt.Sprintf("Grok Build update failed via %s: %v", method, err)
		if text != "" {
			msg = msg + "\n" + firstOutputLines(text, 10)
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
			Message: "Installer finished but Grok Build was not found under ~/.grok/bin. Run the official install from https://x.ai/cli, then restart Nice Codex.",
			Output:  text,
			Tool:    tool,
		}, errors.New("grok not found after install")
	}

	// If update still claims a newer version, report that clearly instead of a fake "ready".
	if tool.UpdateAvailable && tool.LatestVersion != "" && tool.Version != "" &&
		compareSemver(tool.LatestVersion, tool.Version) > 0 {
		msg := fmt.Sprintf(
			"Grok Build is still at %s (latest %s). %s completed but the binary was not upgraded — try again or run `grok update` in a terminal.",
			tool.Version, tool.LatestVersion, method,
		)
		return CLIToolActionResult{
			OK:      false,
			Message: msg,
			Output:  text,
			Tool:    tool,
		}, errors.New(msg)
	}

	versionNote := tool.Version
	if before.Version != "" && tool.Version != "" && before.Version != tool.Version {
		versionNote = before.Version + " → " + tool.Version
	}
	return CLIToolActionResult{
		OK:      true,
		Message: fmt.Sprintf("Grok Build is ready (%s)", firstNonEmpty(versionNote, "ok")),
		Output:  text,
		Tool:    tool,
	}, nil
}

// installOrUpdateAntigravityCLI installs Google's native replacement for the
// consumer Gemini CLI. The installer is intentionally invoked through a bounded
// child process so a stalled network request cannot freeze the desktop UI.
func (s *AppService) installOrUpdateAntigravityCLI() (CLIToolActionResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	output, err := runOfficialAntigravityInstaller(ctx)
	text := strings.TrimSpace(string(output))
	if len(text) > 8000 {
		text = text[len(text)-8000:]
	}
	codex.EnrichPathForLookups()
	// Invalidate the short provider probe cache so the new agy binary appears
	// immediately in the runtime picker.
	providerProbeCache.Lock()
	delete(providerProbeCache.entries, "gemini")
	providerProbeCache.Unlock()
	detection := codex.Detect()
	providers := detectAgentProviders(detection)
	s.mu.Lock()
	s.agentProviders = providers
	s.mu.Unlock()
	tool := probeCLITool(cliPackages[cliPackageIndex(cliToolGemini)], "official", true)
	if err != nil {
		message := fmt.Sprintf("Antigravity CLI installer failed: %v", err)
		if text != "" {
			message += "\n" + firstOutputLines(text, 10)
		}
		return CLIToolActionResult{OK: false, Message: message, Output: text, Tool: tool}, errors.New(message)
	}
	if !tool.Installed || !isAntigravityExecutable(tool.Executable) {
		message := "Installer finished but agy was not found. Restart Nice Codex or add the Antigravity CLI directory to PATH."
		return CLIToolActionResult{OK: false, Message: message, Output: text, Tool: tool}, errors.New(message)
	}
	return CLIToolActionResult{
		OK: true, Message: fmt.Sprintf("Antigravity CLI is ready (%s)", firstNonEmpty(tool.Version, "ok")), Output: text, Tool: tool,
	}, nil
}

func runOfficialAntigravityInstaller(ctx context.Context) ([]byte, error) {
	if runtime.GOOS == "windows" {
		ps := findCommand(commandCandidates("powershell"))
		if ps == "" {
			ps = findCommand(commandCandidates("pwsh"))
		}
		if ps == "" {
			ps = "powershell"
		}
		// Official Windows bootstrap from antigravity.google/docs/cli.
		script := "irm https://antigravity.google/cli/install.ps1 | iex"
		cmd := exec.CommandContext(ctx, ps, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
		return runManagedCombinedOutput(ctx, cmd)
	}
	shell := findCommand(commandCandidates("bash"))
	if shell == "" {
		shell = "/bin/bash"
	}
	script := "curl -fsSL https://antigravity.google/cli/install.sh | bash"
	cmd := exec.CommandContext(ctx, shell, "-lc", script)
	return runManagedCombinedOutput(ctx, cmd)
}

func cliPackageIndex(id CLIToolID) int {
	for index, spec := range cliPackages {
		if spec.id == id {
			return index
		}
	}
	return 0
}

type grokUpdateCheckResult struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Error           any    `json:"error"`
}

func probeGrokUpdateCheck(executable string) (grokUpdateCheckResult, error) {
	if strings.TrimSpace(executable) == "" {
		return grokUpdateCheckResult{}, errors.New("empty grok executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	commandPath, commandArgs, err := providerCommand(executable, []string{"update", "--check", "--json"})
	if err != nil {
		return grokUpdateCheckResult{}, err
	}
	cmd := exec.CommandContext(ctx, commandPath, commandArgs...)
	output, runErr := runManagedCombinedOutput(ctx, cmd)
	text := strings.TrimSpace(string(output))
	// Even on non-zero exit, JSON may still be on stdout.
	if text == "" && runErr != nil {
		return grokUpdateCheckResult{}, runErr
	}
	// Extract the first JSON object from mixed logs.
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		if runErr != nil {
			return grokUpdateCheckResult{}, runErr
		}
		return grokUpdateCheckResult{}, errors.New("no JSON in grok update --check output")
	}
	var result grokUpdateCheckResult
	if err := json.Unmarshal([]byte(text[start:end+1]), &result); err != nil {
		return grokUpdateCheckResult{}, err
	}
	return result, nil
}

func runOfficialGrokInstaller(ctx context.Context) ([]byte, error) {
	if runtime.GOOS == "windows" {
		// Official: irm https://x.ai/cli/install.ps1 | iex
		ps := findCommand(commandCandidates("powershell"))
		if ps == "" {
			ps = findCommand(commandCandidates("pwsh"))
		}
		if ps == "" {
			ps = "powershell"
		}
		script := "irm https://x.ai/cli/install.ps1 | iex"
		cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
		return runManagedCombinedOutput(ctx, cmd)
	}
	// Official: curl -fsSL https://x.ai/cli/install.sh | bash
	shell := findCommand(commandCandidates("bash"))
	if shell == "" {
		shell = "/bin/bash"
	}
	script := "curl -fsSL https://x.ai/cli/install.sh | bash"
	cmd := exec.CommandContext(ctx, shell, "-lc", script)
	return runManagedCombinedOutput(ctx, cmd)
}

func looksLikeNPMGrokShim(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(lower, "/pnpm/") ||
		strings.Contains(lower, "\\pnpm\\") ||
		strings.Contains(lower, "@xai-official") ||
		strings.Contains(lower, "node_modules") ||
		strings.HasSuffix(lower, ".cmd") ||
		strings.HasSuffix(lower, ".ps1")
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
		status.Message = det.Error
	case cliToolGrok:
		// Always re-probe the official binary for CLI tools UI (skip short TTL cache).
		invalidateGrokRuntimeProbeCache()
		gr := detectGrokRuntime()
		status.Installed = gr.BuildAvailable
		status.Executable = gr.BuildExecutable
		status.Version = normalizeCLIVersion(gr.BuildVersion)
		// Grok Build can install/update without Node when the binary already exists,
		// or via the official PowerShell/curl installer.
		status.CanInstall = true
		status.PackageManager = "official"
		status.InstallCommand = officialGrokInstallCommand()
		if status.Installed && status.Executable != "" && !looksLikeNPMGrokShim(status.Executable) {
			if check, err := probeGrokUpdateCheck(status.Executable); err == nil {
				if v := normalizeCLIVersion(check.CurrentVersion); v != "" {
					status.Version = v
				}
				if v := normalizeCLIVersion(check.LatestVersion); v != "" {
					status.LatestVersion = v
				}
				if status.Version != "" && status.LatestVersion != "" {
					status.UpdateAvailable = compareSemver(status.LatestVersion, status.Version) > 0
				} else {
					status.UpdateAvailable = check.UpdateAvailable
				}
			}
		}
	case cliToolClaude, cliToolOpenCode:
		status.Executable = findCommand(commandCandidates(spec.binName))
		status.Installed = status.Executable != ""
		if status.Installed {
			if output, err := runProbeCommand(status.Executable, []string{"--version"}, 4*time.Second); err == nil {
				status.Version = normalizeCLIVersion(firstOutputLine(output))
			}
		}
	case cliToolGemini:
		status.Executable = findGeminiExecutable()
		status.Installed = status.Executable != ""
		if isAntigravityExecutable(status.Executable) {
			status.Name = "Antigravity CLI"
			status.Package = "antigravity-cli (native)"
			status.PackageManager = "official"
			status.NodeAvailable = nodeOK
			status.CanInstall = true
			status.InstallCommand = officialAntigravityInstallCommand()
		} else if status.Executable == "" {
			// Offer the supported native installer even on machines without
			// Node/pnpm. The legacy npm package remains available as a manual
			// fallback for enterprise users.
			status.Name = "Antigravity CLI"
			status.Package = "antigravity-cli (native)"
			status.PackageManager = "official"
			status.CanInstall = true
			status.InstallCommand = officialAntigravityInstallCommand()
		} else {
			status.Name = "Gemini CLI"
		}
		if status.Installed {
			if output, err := runProbeCommand(status.Executable, []string{"--version"}, 4*time.Second); err == nil {
				status.Version = normalizeCLIVersion(firstOutputLine(output))
			}
		}
	}

	// Antigravity is native, while an actually selected legacy Gemini binary is
	// still managed by @google/gemini-cli and needs the normal update check.
	npmManaged := spec.id != cliToolGrok && strings.TrimSpace(spec.npmPkg) != "" && !strings.Contains(spec.npmPkg, "x.ai")
	if spec.id == cliToolGemini {
		npmManaged = status.Installed && !isAntigravityExecutable(status.Executable)
	}
	if npmManaged {
		latest, err := fetchNPMLatestVersion(spec.npmPkg)
		if err == nil {
			status.LatestVersion = latest
			if status.Installed && status.Version != "" && latest != "" {
				status.UpdateAvailable = compareSemver(latest, status.Version) > 0
			}
		}
	}

	switch {
	case spec.id == cliToolCodex && status.Message != "":
		// Keep the actionable missing-bundle diagnostic, not just "Not installed".
	case spec.id == cliToolGrok && !status.Installed:
		status.Message = "Not installed — uses official x.ai/cli installer"
	case spec.id == cliToolGemini && isAntigravityExecutable(status.Executable):
		status.Message = "Antigravity CLI (agy) ready"
	case spec.id == cliToolGemini && status.Executable == "":
		status.Message = "Not installed — uses the official Antigravity CLI installer"
	case spec.id != cliToolGrok && spec.id != cliToolGemini && !nodeOK:
		status.Message = "Install Node.js and pnpm first"
	case spec.id != cliToolGrok && pm == "":
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

func pnpmGlobalInstallArgs(npmPkg string, allowBuild bool) []string {
	args := []string{"add", "-g"}
	if allowBuild {
		args = append(args, "--allow-build="+npmPkg)
	}
	if runtime.GOOS == "windows" {
		// Retain old global packages during this install (100 years, in minutes).
		// A running CLI can lock its EXE: pnpm pruning otherwise removes its
		// helpers first and then stalls on the EXE. Do not change user config or
		// terminate sessions; unused bundles can be cleaned after all CLIs exit.
		args = append(args, "--config.modules-cache-max-age=52560000")
	}
	return append(args, npmPkg+"@latest")
}

func formatInstallCommand(pm, npmPkg string) string {
	if npmPkg == "x.ai/cli" || strings.EqualFold(npmPkg, "x.ai/cli") {
		return officialGrokInstallCommand()
	}
	// --allow-build is required on pnpm 10+ so postinstall can place native binaries
	// (Claude Code / OpenCode ship stub bin placeholders without it).
	return "pnpm " + strings.Join(pnpmGlobalInstallArgs(npmPkg, true), " ")
}

func officialGrokInstallCommand() string {
	if runtime.GOOS == "windows" {
		return "irm https://x.ai/cli/install.ps1 | iex"
	}
	return "curl -fsSL https://x.ai/cli/install.sh | bash"
}

func officialAntigravityInstallCommand() string {
	if runtime.GOOS == "windows" {
		return "irm https://antigravity.google/cli/install.ps1 | iex"
	}
	return "curl -fsSL https://antigravity.google/cli/install.sh | bash"
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

func looksLikeUnknownPNPMOption(output string, err error) bool {
	text := strings.ToLower(output + " " + fmt.Sprint(err))
	return strings.Contains(text, "unknown option") ||
		strings.Contains(text, "unknown flag") ||
		strings.Contains(text, "allow-build")
}

// ensureCLINativeBinary runs the package postinstall/install script so native
// CLI binaries (Claude Code, OpenCode, …) replace the stub bin/*.exe placeholder.
// pnpm 10+ skips dependency lifecycle scripts unless --allow-build is used; even
// then some global installs still leave the stub, so we always re-run postinstall.
func ensureCLINativeBinary(ctx context.Context, env []string, npmPkg string) (string, error) {
	npmPkg = strings.TrimSpace(npmPkg)
	if npmPkg == "" || strings.Contains(npmPkg, "x.ai") {
		return "", nil
	}
	pkgDir, err := resolvePNPMGlobalPackageDir(ctx, env, npmPkg)
	if err != nil {
		return "", err
	}
	packageJSONPath := filepath.Join(pkgDir, "package.json")
	raw, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return "", err
	}
	var meta struct {
		Scripts map[string]string `json:"scripts"`
		Bin     any               `json:"bin"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", err
	}

	// Prefer postinstall, then install. prepare is intentionally skipped (publish guards).
	script := strings.TrimSpace(meta.Scripts["postinstall"])
	if script == "" {
		script = strings.TrimSpace(meta.Scripts["install"])
	}
	if script == "" {
		// No lifecycle script — package is pure JS / prebuilt. Nothing to fix.
		return "", nil
	}

	// If the declared binary already looks like a real native build, still re-run
	// postinstall on upgrades so a newer optionalDependency binary is copied over.
	// Only skip when the package has no bin entry at all.
	if !packageDeclaresBin(meta.Bin) {
		return "", nil
	}

	node := findCommand(commandCandidates("node"))
	if node == "" {
		return "", errors.New("node is required to run CLI postinstall scripts")
	}

	// package.json scripts are shell snippets ("node install.cjs"). Run via node
	// when the command is a simple `node <file>` form; otherwise use the platform shell.
	postOutput, postErr := runPackageLifecycleScript(ctx, env, node, pkgDir, script)
	text := strings.TrimSpace(postOutput)
	if postErr != nil {
		if text == "" {
			return text, fmt.Errorf("postinstall for %s failed: %w", npmPkg, postErr)
		}
		return text, fmt.Errorf("postinstall for %s failed: %w\n%s", npmPkg, postErr, firstOutputLines(text, 8))
	}
	if text == "" {
		text = "postinstall: ok (" + npmPkg + ")"
	}
	return text, nil
}

func packageDeclaresBin(bin any) bool {
	switch value := bin.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case map[string]any:
		return len(value) > 0
	default:
		return false
	}
}

func resolvePNPMGlobalPackageDir(ctx context.Context, env []string, npmPkg string) (string, error) {
	pnpmBin := packageManagerBinary("pnpm")
	commandPath, args, err := providerCommand(pnpmBin, []string{"root", "-g"})
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, commandPath, args...)
	cmd.Env = env
	output, runErr := runManagedCombinedOutput(ctx, cmd)
	root := strings.TrimSpace(string(output))
	if runErr != nil || root == "" {
		if runErr == nil {
			runErr = errors.New("empty pnpm root -g")
		}
		return "", fmt.Errorf("resolve pnpm global node_modules: %w", runErr)
	}
	pkgDir := filepath.Join(append([]string{root}, strings.Split(npmPkg, "/")...)...)
	if info, err := os.Stat(pkgDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("global package directory not found: %s", pkgDir)
	}
	return pkgDir, nil
}

func runPackageLifecycleScript(ctx context.Context, env []string, node, pkgDir, script string) (string, error) {
	script = strings.TrimSpace(script)
	fields := strings.Fields(script)
	// Common forms: "node install.cjs", "node ./postinstall.mjs"
	if len(fields) >= 2 && (fields[0] == "node" || strings.EqualFold(fields[0], "node.exe")) {
		scriptPath := fields[1]
		if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(pkgDir, filepath.FromSlash(scriptPath))
		}
		args := append([]string{scriptPath}, fields[2:]...)
		cmd := exec.CommandContext(ctx, node, args...)
		cmd.Dir = pkgDir
		cmd.Env = env
		output, err := runManagedCombinedOutput(ctx, cmd)
		return string(output), err
	}

	// Generic fallback: run the script line through a shell in the package directory.
	if runtime.GOOS == "windows" {
		ps := findCommand(commandCandidates("powershell"))
		if ps == "" {
			ps = findCommand(commandCandidates("pwsh"))
		}
		if ps == "" {
			ps = "powershell"
		}
		cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
		cmd.Dir = pkgDir
		cmd.Env = env
		output, err := runManagedCombinedOutput(ctx, cmd)
		return string(output), err
	}
	shell := findCommand(commandCandidates("bash"))
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.CommandContext(ctx, shell, "-lc", script)
	cmd.Dir = pkgDir
	cmd.Env = env
	output, err := runManagedCombinedOutput(ctx, cmd)
	return string(output), err
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
