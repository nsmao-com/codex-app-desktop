//go:build !windows

package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// enrichProcessPath merges common Node / package-manager / CLI install directories
// into this process PATH. macOS/Linux GUI apps launched from Finder/Dock often
// inherit a minimal PATH and miss Homebrew, nvm, fnm, pnpm, and npm global bins.
func enrichProcessPath() {
	parts := splitPathList(os.Getenv("PATH"))
	seen := make(map[string]struct{}, len(parts)+32)
	for _, part := range parts {
		seen[filepath.Clean(part)] = struct{}{}
	}

	appendUnique := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok {
			return
		}
		if info, err := os.Stat(clean); err != nil || !info.IsDir() {
			return
		}
		seen[clean] = struct{}{}
		// Prepend so user-managed tools win over system stubs.
		parts = append([]string{clean}, parts...)
	}

	for _, dir := range commonUnixCLIBinDirs() {
		appendUnique(dir)
	}

	if len(parts) > 0 {
		_ = os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
	}
}

func commonUnixCLIBinDirs() []string {
	home, _ := os.UserHomeDir()
	candidates := make([]string, 0, 40)

	// Homebrew (Apple Silicon + Intel) and system local bins.
	candidates = append(candidates,
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
	)

	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".yarn", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".npm", "bin"),
			filepath.Join(home, ".grok", "bin"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".local", "share", "pnpm"),
			filepath.Join(home, "Library", "pnpm"), // macOS pnpm home
			filepath.Join(home, ".fnm", "current", "bin"),
			filepath.Join(home, ".local", "share", "fnm", "current", "bin"),
		)

		// nvm: ~/.nvm/versions/node/<ver>/bin — pick newest version dir name.
		nvmRoot := filepath.Join(home, ".nvm", "versions", "node")
		if entries, err := os.ReadDir(nvmRoot); err == nil {
			var best string
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if best == "" || name > best {
					best = name
				}
			}
			if best != "" {
				candidates = append(candidates, filepath.Join(nvmRoot, best, "bin"))
			}
		}

		// fnm multishell / aliases
		fnmRoots := []string{
			filepath.Join(home, ".local", "share", "fnm", "aliases", "default", "bin"),
			filepath.Join(home, "Library", "Application Support", "fnm", "aliases", "default", "bin"),
		}
		candidates = append(candidates, fnmRoots...)

		// mise / rtx shims
		candidates = append(candidates,
			filepath.Join(home, ".local", "share", "mise", "shims"),
			filepath.Join(home, ".mise", "shims"),
		)
	}

	// Optional env-driven roots (same on Windows/macOS/Linux).
	if grokHome := strings.TrimSpace(os.Getenv("GROK_HOME")); grokHome != "" {
		candidates = append(candidates, filepath.Join(grokHome, "bin"))
	}
	if npmBin := strings.TrimSpace(os.Getenv("NPM_CONFIG_PREFIX")); npmBin != "" {
		candidates = append(candidates, filepath.Join(npmBin, "bin"))
	}
	if pnpmHome := strings.TrimSpace(os.Getenv("PNPM_HOME")); pnpmHome != "" {
		candidates = append(candidates, pnpmHome)
	}

	// Linux distro package paths occasionally used for node.
	if runtime.GOOS == "linux" {
		candidates = append(candidates,
			"/home/linuxbrew/.linuxbrew/bin",
			"/snap/bin",
		)
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
	return commandSpec{}, false
}

// resolveUnixExtraCommands looks for codex outside PATH (npm global, homebrew).
func resolveUnixExtraCommands() (commandSpec, bool) {
	home, _ := os.UserHomeDir()
	candidates := []string{}
	for _, dir := range commonUnixCLIBinDirs() {
		candidates = append(candidates, filepath.Join(dir, "codex"))
	}
	if home != "" {
		// npm global package layout when linked as script next to node_modules.
		candidates = append(candidates,
			filepath.Join(home, ".npm-global", "lib", "node_modules", "@openai", "codex", "bin", "codex.js"),
			filepath.Join(home, ".local", "lib", "node_modules", "@openai", "codex", "bin", "codex.js"),
		)
	}
	// Homebrew node_modules (rare but seen).
	candidates = append(candidates,
		"/opt/homebrew/lib/node_modules/@openai/codex/bin/codex.js",
		"/usr/local/lib/node_modules/@openai/codex/bin/codex.js",
	)

	for _, path := range candidates {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if strings.HasSuffix(path, ".js") {
			nodePath, err := execLookPath("node")
			if err != nil {
				continue
			}
			return commandSpec{path: nodePath, prefixArgs: []string{path}}, true
		}
		// Ensure executable bit is present (best-effort).
		if info.Mode()&0o111 == 0 {
			continue
		}
		return commandSpec{path: path}, true
	}
	return commandSpec{}, false
}
