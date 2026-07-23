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
	if err != nil {
		return commandSpec{}, errors.New("Codex CLI was not found; install it with pnpm add -g @openai/codex, then restart Nice Codex (GUI apps need Node/pnpm on the User PATH)")
	}
	return commandSpec{path: path}, nil
}

func resolveWindowsNPMCommand() (commandSpec, bool) {
	commandPath, err := execLookPath("codex.cmd")
	if err != nil {
		return commandSpec{}, false
	}

	scriptPath := filepath.Join(filepath.Dir(commandPath), "node_modules", "@openai", "codex", "bin", "codex.js")
	if info, err := os.Stat(scriptPath); err != nil || info.IsDir() {
		return commandSpec{}, false
	}

	nodePath, err := execLookPath("node.exe")
	if err != nil {
		nodePath, err = execLookPath("node")
	}
	if err != nil {
		return commandSpec{}, false
	}

	return commandSpec{path: nodePath, prefixArgs: []string{scriptPath}}, true
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
