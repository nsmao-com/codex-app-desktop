package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type commandSpec struct {
	path       string
	prefixArgs []string
}

var pathLookupMu sync.Mutex

// EnrichPathForLookups merges common Node/pnpm/npm bin dirs into PATH so GUI
// apps can resolve globally installed CLIs (codex, grok, node, pnpm, …).
func EnrichPathForLookups() {
	pathLookupMu.Lock()
	defer pathLookupMu.Unlock()
	enrichProcessPath()
}

// PersistentEnvironmentValue falls back to the operating system's persisted
// environment when a long-running GUI parent has not inherited a recent change.
func PersistentEnvironmentValue(name string) string {
	if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return persistentEnvironmentValue(name)
}

func Detect() Detection {
	_, detection := resolveDetectedCommand(context.Background())
	return detection
}

// Detection and startup share the same ordered candidates and health checks.
// Keep the first failure for diagnostics if no installation is usable.
func resolveDetectedCommand(parent context.Context) (commandSpec, Detection) {
	specs, err := resolveCommandCandidates()
	if err != nil {
		return commandSpec{}, Detection{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	seen := map[string]bool{}
	var failure Detection
	for _, spec := range specs {
		key := spec.path + "\x00" + strings.Join(spec.prefixArgs, "\x00")
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		detection := detectResolvedCommand(ctx, spec)
		if detection.Available {
			return spec, detection
		}
		if failure.Error == "" {
			failure = detection
		}
		if ctx.Err() != nil {
			break
		}
	}
	return commandSpec{}, failure
}

func detectResolvedCommand(parent context.Context, spec commandSpec) Detection {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	args := append(append([]string{}, spec.prefixArgs...), "--version")
	command := exec.CommandContext(ctx, spec.path, args...)
	command.WaitDelay = 500 * time.Millisecond
	configureProcess(command)
	output, versionErr := command.CombinedOutput()
	version := strings.TrimSpace(string(output))
	if versionErr != nil {
		return Detection{
			Binary: spec.path,
			Error:  fmt.Sprintf("Codex CLI could not start (%v); repair the CLI installation and retry", versionErr),
		}
	}
	if version == "" {
		return Detection{Binary: spec.path, Error: "Codex CLI did not report a version; repair the CLI installation and retry"}
	}
	if err := validateCodexBundle(spec.path, version); err != nil {
		return Detection{Binary: spec.path, Version: version, Error: err.Error()}
	}

	return Detection{
		Available: true,
		Binary:    spec.path,
		Version:   version,
	}
}

// A working --version alone does not prove an npm native package is complete.
// Since 0.149 the official Windows bundle also needs the code-mode host. Check
// only that known bundle layout; do not impose it on wrappers or older CLIs.
func validateCodexBundle(path, version string) error {
	if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Base(filepath.Dir(path)), "bin") ||
		!strings.HasSuffix(filepath.Base(filepath.Dir(filepath.Dir(path))), "-pc-windows-msvc") {
		return nil
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(version, "codex-cli %d.%d.%d", &major, &minor, &patch); err != nil || (major == 0 && minor < 149) {
		return nil
	}
	root := filepath.Dir(filepath.Dir(path))
	for _, relative := range []string{
		"bin/codex-code-mode-host.exe",
		"codex-resources/codex-command-runner.exe",
		"codex-resources/codex-windows-sandbox-setup.exe",
	} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || info.IsDir() || info.Size() == 0 {
			return fmt.Errorf("Codex CLI 安装不完整，缺少 %s；请在设置 → CLI 工具中重新安装/更新 Codex（不会清除会话和服务商配置） / Incomplete Codex installation; reinstall or update from Settings → CLI tools", filepath.Base(relative))
		}
	}
	return nil
}

func resolveCommandCandidates() ([]commandSpec, error) {
	EnrichPathForLookups()

	if configured := strings.Trim(strings.TrimSpace(PersistentEnvironmentValue("CODEX_BIN")), `"`); configured != "" {
		path, err := filepath.Abs(configured)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if runtime.GOOS == "windows" && (strings.EqualFold(filepath.Ext(path), ".cmd") || strings.EqualFold(filepath.Ext(path), ".bat") || strings.EqualFold(filepath.Ext(path), ".ps1")) {
				if spec, ok := resolveWindowsCodexShim(path); ok {
					return []commandSpec{spec}, nil
				}
				return nil, errors.New("CODEX_BIN points to an unsupported Windows command shim")
			}
			return []commandSpec{{path: path}}, nil
		}
		return nil, errors.New("CODEX_BIN does not point to an executable file")
	}

	var specs []commandSpec
	if runtime.GOOS == "windows" {
		// Respect directory order across extensions: a newer pnpm shim in an
		// earlier PATH entry must not lose to an old standalone exe farther down.
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			if !filepath.IsAbs(dir) {
				continue
			}
			for _, name := range []string{"codex.exe", "codex.cmd", "codex.bat", "codex.ps1"} {
				path := filepath.Join(dir, name)
				if name == "codex.exe" {
					if validWindowsCodexBinary(path) {
						specs = append(specs, commandSpec{path: path})
					}
				} else if spec, ok := resolveWindowsCodexShim(path); ok {
					specs = append(specs, spec)
				}
			}
		}
		specs = append(specs, windowsExtraCommands()...)
	}

	// macOS/Linux GUI apps often miss Homebrew/nvm PATH — scan known install roots.
	if runtime.GOOS != "windows" {
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			if !filepath.IsAbs(dir) {
				continue
			}
			if path, err := execLookPath(filepath.Join(dir, "codex")); err == nil {
				specs = append(specs, commandSpec{path: path})
			}
		}
		specs = append(specs, unixExtraCommands()...)
	}

	if len(specs) == 0 {
		return nil, errors.New("Codex CLI was not found. If already installed, check again or open Settings → CLI tools to inspect paths and repair the installation. Otherwise install with pnpm add -g @openai/codex, then check again")
	}
	return specs, nil
}

func resolveWindowsNPMCommand() (commandSpec, bool) {
	commandPath, err := execLookPath("codex.cmd")
	if err != nil {
		return commandSpec{}, false
	}
	return resolveWindowsCodexShim(commandPath)
}

func resolveBundledCodexBinary(scriptPath string) (commandSpec, bool) {
	resolvedScript := scriptPath
	if evaluated, err := filepath.EvalSymlinks(scriptPath); err == nil {
		resolvedScript = evaluated
	}
	packageRoot := filepath.Dir(filepath.Dir(resolvedScript))
	targetTriple := ""
	platformPackage := ""
	switch runtime.GOARCH {
	case "amd64":
		targetTriple = "x86_64-pc-windows-msvc"
		platformPackage = "codex-win32-x64"
	case "arm64":
		targetTriple = "aarch64-pc-windows-msvc"
		platformPackage = "codex-win32-arm64"
	default:
		return commandSpec{}, false
	}

	candidates := []string{
		filepath.Join(packageRoot, "node_modules", "@openai", platformPackage, "vendor", targetTriple, "bin", "codex.exe"),
		filepath.Join(packageRoot, "vendor", targetTriple, "bin", "codex.exe"),
		filepath.Join(filepath.Dir(packageRoot), platformPackage, "vendor", targetTriple, "bin", "codex.exe"),
	}
	// pnpm can retain a shim to the JS package after installing the same
	// version's platform-specific package. Resolve that exact sibling only;
	// never pick an arbitrary (potentially older) binary from the pnpm store.
	pnpmPackageDir := filepath.Dir(filepath.Dir(filepath.Dir(packageRoot)))
	pnpmPackageName := filepath.Base(pnpmPackageDir)
	if strings.EqualFold(filepath.Base(filepath.Dir(pnpmPackageDir)), ".pnpm") &&
		strings.HasPrefix(pnpmPackageName, "@openai+codex@") {
		platformName := pnpmPackageName + strings.TrimPrefix(platformPackage, "codex")
		candidates = append(candidates, filepath.Join(filepath.Dir(pnpmPackageDir), platformName,
			"node_modules", "@openai", "codex", "vendor", targetTriple, "bin", "codex.exe"))
	}
	for _, candidate := range candidates {
		// Older npm packages placed the binary in codex/ instead of bin/.
		for _, binary := range []string{candidate, filepath.Join(filepath.Dir(filepath.Dir(candidate)), "codex", "codex.exe")} {
			if validWindowsCodexBinary(binary) {
				return commandSpec{path: binary}, true
			}
		}
	}
	return commandSpec{}, false
}

// Package managers sometimes leave an empty native placeholder after a failed
// install. Ignore it during discovery instead of hiding a working later copy.
func validWindowsCodexBinary(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var header [2]byte
	n, err := file.Read(header[:])
	return err == nil && n == 2 && header == [2]byte{'M', 'Z'}
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
