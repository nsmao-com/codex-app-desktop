//go:build windows

package codex

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// enrichProcessPath merges Machine/User PATH and common Node/pnpm/npm install
// directories into this process. GUI apps launched from Explorer often miss
// shell-only PATH entries (fnm/nvm/pnpm), so LookPath("codex") fails even when
// the CLI works in a terminal. Installing Go/Wails often "fixes" this by
// rewriting the permanent User PATH — which is why some users only see Codex
// after installing Wails.
func enrichProcessPath() {
	parts := splitPathList(os.Getenv("PATH"))
	seen := make(map[string]struct{}, len(parts)+16)
	for _, part := range parts {
		seen[strings.ToLower(filepath.Clean(part))] = struct{}{}
	}

	appendUnique := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			return
		}
		if info, err := os.Stat(clean); err != nil || !info.IsDir() {
			return
		}
		seen[key] = struct{}{}
		parts = append([]string{clean}, parts...)
	}

	for _, dir := range registryPathDirs() {
		appendUnique(dir)
	}
	for _, dir := range commonCodexBinDirs() {
		appendUnique(dir)
	}

	if len(parts) > 0 {
		_ = os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
	}
}

func registryPathDirs() []string {
	var dirs []string
	dirs = append(dirs, readRegistryPATH(registry.CURRENT_USER, `Environment`)...)
	dirs = append(dirs, readRegistryPATH(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)...)
	return dirs
}

func readRegistryPATH(root registry.Key, path string) []string {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()

	value, _, err := key.GetStringValue("Path")
	if err != nil {
		return nil
	}
	return splitPathList(os.ExpandEnv(value))
}

func commonCodexBinDirs() []string {
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	localAppData := os.Getenv("LOCALAPPDATA")
	programFiles := os.Getenv("ProgramFiles")
	programFilesX86 := os.Getenv("ProgramFiles(x86)")

	candidates := []string{
		filepath.Join(appData, "npm"),
		filepath.Join(appData, "fnm"),
		filepath.Join(localAppData, "pnpm"),
		filepath.Join(localAppData, "fnm_multishells"),
		filepath.Join(localAppData, "Programs", "fnm"),
		filepath.Join(localAppData, "Yarn", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "AppData", "Roaming", "npm"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(programFiles, "nodejs"),
		filepath.Join(programFilesX86, "nodejs"),
	}

	if localAppData != "" {
		entries, err := os.ReadDir(filepath.Join(localAppData, "fnm_multishells"))
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					candidates = append(candidates, filepath.Join(localAppData, "fnm_multishells", entry.Name()))
				}
			}
		}
		nvmRoot := filepath.Join(localAppData, "nvm")
		if version := strings.TrimSpace(os.Getenv("NVM_SYMLINK")); version != "" {
			candidates = append(candidates, version)
		}
		candidates = append(candidates, nvmRoot)
	}

	return candidates
}

func splitPathList(value string) []string {
	raw := strings.Split(value, string(os.PathListSeparator))
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func resolveWindowsExtraCommands() (commandSpec, bool) {
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	localAppData := os.Getenv("LOCALAPPDATA")

	type candidate struct {
		cmd  string
		js   string
		node bool
	}

	candidates := []candidate{
		{cmd: filepath.Join(appData, "npm", "codex.cmd"), js: filepath.Join(appData, "npm", "node_modules", "@openai", "codex", "bin", "codex.js"), node: true},
		{cmd: filepath.Join(localAppData, "pnpm", "codex.CMD")},
		{cmd: filepath.Join(localAppData, "pnpm", "codex.exe")},
		{cmd: filepath.Join(localAppData, "pnpm", "codex")},
		{cmd: filepath.Join(home, ".volta", "bin", "codex.cmd")},
		{cmd: filepath.Join(home, ".volta", "bin", "codex.exe")},
		{cmd: filepath.Join(home, ".local", "bin", "codex.exe")},
		{cmd: filepath.Join(home, ".local", "bin", "codex.cmd")},
	}

	for _, item := range candidates {
		if item.cmd == "" {
			continue
		}
		info, err := os.Stat(item.cmd)
		if err != nil || info.IsDir() {
			continue
		}
		if item.node {
			if item.js == "" {
				continue
			}
			if jsInfo, err := os.Stat(item.js); err != nil || jsInfo.IsDir() {
				continue
			}
			nodePath, err := execLookPath("node.exe")
			if err != nil {
				nodePath, err = execLookPath("node")
			}
			if err != nil {
				continue
			}
			return commandSpec{path: nodePath, prefixArgs: []string{item.js}}, true
		}
		return commandSpec{path: item.cmd}, true
	}
	return commandSpec{}, false
}
