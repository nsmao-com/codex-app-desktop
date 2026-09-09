//go:build !windows

package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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

	// Finder/Dock applications do not run the user's login shell and therefore
	// miss PATH entries initialized by .zprofile/.zshrc (nvm, fnm, asdf, pnpm).
	// Read the shell's exported PATH once, without executing a CLI or install
	// command, then merge it before the fixed known directories below.
	for _, dir := range loginShellPath() {
		appendUnique(dir)
	}
	for _, dir := range commonUnixCLIBinDirs() {
		appendUnique(dir)
	}

	if len(parts) > 0 {
		_ = os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
	}
}

func loginShellPath() []string {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/zsh"
	}
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	// `-ilc` loads the same interactive/login environment as a terminal. The
	// command itself is a shell builtin and emits only PATH.
	output, err := exec.CommandContext(ctx, shell, "-ilc", "printf '%s' \"$PATH\"").Output()
	if err != nil {
		return nil
	}
	return splitPathList(strings.TrimSpace(string(output)))
}

func persistentEnvironmentValue(string) string {
	return ""
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
		// Put official Grok Build (~/.grok/bin) first so it wins over any npm
		// shim for `@xai-official/grok` (platform-limited stub package).
		candidates = append(candidates,
			filepath.Join(home, ".grok", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".yarn", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".npm", "bin"),
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

func resolveWindowsCodexShim(_ string) (commandSpec, bool) {
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
			filepath.Join(home, ".local", "share", "pnpm", "global", "5", "node_modules", "@openai", "codex", "bin", "codex.js"),
			filepath.Join(home, "Library", "pnpm", "global", "5", "node_modules", "@openai", "codex", "bin", "codex.js"),
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
		// npm/pnpm shims are normally executable, but Finder-launched apps can
		// encounter copied scripts without the executable bit. Accept a readable
		// shebang script and run it through its interpreter so detection still
		// works without changing the user's file permissions.
		if info.Mode()&0o111 != 0 {
			return commandSpec{path: path}, true
		}
		file, readErr := os.ReadFile(path)
		if readErr != nil || !strings.HasPrefix(string(file), "#!") {
			continue
		}
		line := strings.SplitN(string(file), "\n", 2)[0]
		if strings.Contains(line, "node") {
			nodePath, nodeErr := execLookPath("node")
			if nodeErr == nil {
				return commandSpec{path: nodePath, prefixArgs: []string{path}}, true
			}
		}
	}
	return commandSpec{}, false
}
