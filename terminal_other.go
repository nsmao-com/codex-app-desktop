//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func platformTerminalProfiles() []TerminalProfile {
	shell := filepath.Base(os.Getenv("SHELL"))
	zshAvailable := shellExists("zsh") || shell == "zsh"
	bashAvailable := shellExists("bash") || shell == "bash"
	profiles := []TerminalProfile{
		{ID: "zsh", Name: "zsh", Description: "Login zsh shell.", Available: zshAvailable},
		{ID: "bash", Name: "bash", Description: "Login bash shell.", Available: bashAvailable},
	}
	if runtime.GOOS == "darwin" {
		profiles = append(profiles, TerminalProfile{
			ID: "terminal", Name: "Terminal.app", Description: "Open macOS Terminal in the workspace.", Available: true,
		})
	} else {
		profiles = append(profiles, TerminalProfile{
			ID: "terminal", Name: "System terminal", Description: "Open the default system terminal.", Available: findLinuxTerminal() != "",
		})
	}
	return profiles
}

func launchTerminal(profile string, workspace string) error {
	switch profile {
	case "terminal":
		if runtime.GOOS == "darwin" {
			if findExecutable("osascript") != "" {
				script := `tell application "Terminal" to do script "cd " & quoted form of "` + escapeAppleScript(workspace) + `"`
				command := exec.Command("osascript", "-e", script)
				return command.Start()
			}
			command := exec.Command("open", "-a", "Terminal", workspace)
			return command.Start()
		}
		binary := findLinuxTerminal()
		if binary == "" {
			return errors.New("no system terminal emulator was found")
		}
		return exec.Command(binary, "--working-directory="+workspace).Start()
	case "zsh", "bash":
		shell := resolveUnixShell(profile)
		if shell == "" {
			return errors.New(profile + " is not available")
		}
		if runtime.GOOS == "darwin" {
			script := `tell application "Terminal" to do script "cd " & quoted form of "` + escapeAppleScript(workspace) + `" & "; exec " & quoted form of "` + escapeAppleScript(shell) + `" & " -l"`
			return exec.Command("osascript", "-e", script).Start()
		}
		binary := findLinuxTerminal()
		if binary == "" {
			return errors.New("no system terminal emulator was found")
		}
		return exec.Command(binary, "--working-directory="+workspace, "-e", shell, "-l").Start()
	default:
		return errors.New("unsupported terminal profile")
	}
}

func terminalCommand(profile string, workspace string) ([]string, error) {
	_ = workspace
	switch profile {
	case "zsh", "bash", "terminal":
		shell := resolveUnixShell(profile)
		if shell == "" {
			return nil, errors.New("an interactive shell was not found")
		}
		return []string{shell, "-l", "-i"}, nil
	default:
		return nil, errors.New("unsupported terminal profile")
	}
}

func resolveUnixShell(profile string) string {
	switch profile {
	case "zsh":
		if path := findExecutable("zsh"); path != "" {
			return path
		}
	case "bash":
		if path := findExecutable("bash"); path != "" {
			return path
		}
	case "terminal":
		if shell := os.Getenv("SHELL"); shell != "" && shellExists(shell) {
			return shell
		}
		if path := findExecutable("zsh"); path != "" {
			return path
		}
		if path := findExecutable("bash"); path != "" {
			return path
		}
	}
	if shell := os.Getenv("SHELL"); shell != "" && shellExists(shell) {
		return shell
	}
	return ""
}

func shellExists(path string) bool {
	if path == "" {
		return false
	}
	if !strings.Contains(path, string(os.PathSeparator)) {
		return findExecutable(path) != ""
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func findLinuxTerminal() string {
	for _, name := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal", "kitty", "alacritty", "xterm"} {
		if path := findExecutable(name); path != "" {
			return path
		}
	}
	return ""
}

func findExecutable(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func escapeAppleScript(value string) string {
	return strings.ReplaceAll(value, `"`, `\"`)
}
