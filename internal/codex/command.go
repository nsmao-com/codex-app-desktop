package codex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type commandSpec struct {
	path       string
	prefixArgs []string
}

// EnrichPathForLookups merges common Node/pnpm/npm bin dirs into PATH so GUI
// apps can resolve globally installed CLIs (codex, grok, node, pnpm, …).
func EnrichPathForLookups() {
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
	enrichProcessPath()

	spec, err := resolveCommand()
	if err != nil {
		return Detection{Error: err.Error()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	args := append(append([]string{}, spec.prefixArgs...), "--version")
	command := exec.CommandContext(ctx, spec.path, args...)
	configureProcess(command)
	output, versionErr := command.CombinedOutput()
	version := strings.TrimSpace(string(output))
	if versionErr != nil && version == "" {
		version = "unknown"
	}

	return Detection{
		Available: true,
		Binary:    spec.path,
		Version:   version,
	}
}

func resolveCommand() (commandSpec, error) {
	enrichProcessPath()

	if configured := strings.TrimSpace(os.Getenv("CODEX_BIN")); configured != "" {
		path, err := filepath.Abs(configured)
		if err != nil {
			return commandSpec{}, err
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if runtime.GOOS == "windows" && (strings.EqualFold(filepath.Ext(path), ".cmd") || strings.EqualFold(filepath.Ext(path), ".bat")) {
				if spec, ok := resolveWindowsCodexShim(path); ok {
					return spec, nil
				}
				return commandSpec{}, errors.New("CODEX_BIN points to an unsupported Windows command shim")
			}
			return commandSpec{path: path}, nil
		}
		return commandSpec{}, errors.New("CODEX_BIN does not point to an executable file")
	}

	if runtime.GOOS == "windows" {
		if path, err := execLookPath("codex.exe"); err == nil {
			return commandSpec{path: path}, nil
		}

		if spec, ok := resolveWindowsNPMCommand(); ok {
			return spec, nil
		}
		if spec, ok := resolveWindowsExtraCommands(); ok {
			return spec, nil
		}
	}

	path, err := execLookPath("codex")
	if err == nil {
		return commandSpec{path: path}, nil
	}

	// macOS/Linux GUI apps often miss Homebrew/nvm PATH — scan known install roots.
	if runtime.GOOS != "windows" {
		if spec, ok := resolveUnixExtraCommands(); ok {
			return spec, nil
		}
	}

	return commandSpec{}, errors.New("Codex CLI was not found; install it with pnpm add -g @openai/codex, then restart Nice Codex (GUI apps need Node/pnpm on PATH — macOS: Homebrew/nvm/fnm bins)")
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
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return commandSpec{path: candidate}, true
		}
	}
	return commandSpec{}, false
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
