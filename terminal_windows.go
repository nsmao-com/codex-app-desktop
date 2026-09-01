//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const createNewConsole = 0x00000010

var wslTerminalProfileCache struct {
	sync.Mutex
	profile TerminalProfile
	checked time.Time
}

func platformTerminalProfiles() []TerminalProfile {
	return []TerminalProfile{
		{ID: "powershell", Name: "PowerShell", Description: "PowerShell 7, with Windows PowerShell as fallback.", Available: findPowerShell() != ""},
		{ID: "git-bash", Name: "Git Bash", Description: "Git for Windows Bash terminal.", Available: findGitBash() != ""},
		detectWSLTerminalProfile(),
	}
}

func launchTerminal(profile string, workspace string) error {
	var command *exec.Cmd
	switch profile {
	case "powershell":
		binary := findPowerShell()
		if binary == "" {
			return errors.New("PowerShell is not installed")
		}
		command = exec.Command(binary, "-NoExit")
	case "git-bash":
		binary := findGitBash()
		if binary == "" {
			return errors.New("Git Bash is not installed")
		}
		command = exec.Command(binary)
	case "wsl":
		binary := findWSLExecutable()
		if binary == "" {
			return errors.New("WSL is not installed")
		}
		if !detectWSLTerminalProfile().Available {
			return errors.New("the default WSL distribution is not available")
		}
		command = exec.Command(binary, "--cd", workspace)
	default:
		return errors.New("unsupported terminal profile")
	}

	command.Dir = workspace
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	return command.Start()
}

func terminalCommand(profile string, workspace string) ([]string, error) {
	switch profile {
	case "powershell":
		binary := findPowerShell()
		if binary == "" {
			return nil, errors.New("PowerShell is not installed")
		}
		// Interactive login shell for ConPTY / xterm (not pipe-fed -NonInteractive).
		return []string{binary, "-NoLogo"}, nil
	case "git-bash":
		launcher := findGitBash()
		if launcher == "" {
			return nil, errors.New("Git Bash is not installed")
		}
		bash := existingFile(filepath.Join(filepath.Dir(launcher), "bin", "bash.exe"))
		if bash == "" {
			return nil, errors.New("Git Bash executable was not found")
		}
		return []string{bash, "--login", "-i"}, nil
	case "wsl":
		binary := findWSLExecutable()
		if binary == "" {
			return nil, errors.New("WSL is not installed")
		}
		if !detectWSLTerminalProfile().Available {
			return nil, errors.New("the default WSL distribution is not available")
		}
		return []string{binary, "--cd", workspace}, nil
	default:
		return nil, errors.New("unsupported terminal profile")
	}
}

func findPowerShell() string {
	if path := findExecutable("pwsh.exe"); path != "" {
		return path
	}
	return findExecutable("powershell.exe")
}

func findGitBash() string {
	if path := findExecutable("git-bash.exe"); path != "" {
		return path
	}
	if gitPath := findExecutable("git.exe"); gitPath != "" {
		if path := existingFile(filepath.Join(filepath.Dir(filepath.Dir(gitPath)), "git-bash.exe")); path != "" {
			return path
		}
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if path := existingFile(filepath.Join(root, "Git", "git-bash.exe")); path != "" {
			return path
		}
	}
	return ""
}

func detectWSLTerminalProfile() TerminalProfile {
	wslTerminalProfileCache.Lock()
	defer wslTerminalProfileCache.Unlock()
	if !wslTerminalProfileCache.checked.IsZero() && time.Since(wslTerminalProfileCache.checked) < 5*time.Minute {
		return wslTerminalProfileCache.profile
	}

	profile := TerminalProfile{
		ID:          "wsl",
		Name:        "WSL",
		Description: "Default Windows Subsystem for Linux distribution.",
		Status:      "not-installed",
	}
	binary := findWSLExecutable()
	if binary != "" {
		profile.Status = "runtime-unavailable"
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		command := exec.CommandContext(ctx, binary, "--exec", "/bin/sh", "-lc", "exit 0")
		configureBackgroundProcess(command)
		err := command.Run()
		ctxErr := ctx.Err()
		cancel()
		if err == nil && ctxErr == nil {
			profile.Available = true
			profile.Status = "ready"
		}
	}
	wslTerminalProfileCache.profile = profile
	wslTerminalProfileCache.checked = time.Now()
	return profile
}

func invalidateTerminalProfileCache() {
	wslTerminalProfileCache.Lock()
	wslTerminalProfileCache.profile = TerminalProfile{}
	wslTerminalProfileCache.checked = time.Time{}
	wslTerminalProfileCache.Unlock()
}

func findWSLExecutable() string {
	if path := findExecutable("wsl.exe"); path != "" {
		return path
	}
	windowsRoot := os.Getenv("SystemRoot")
	if windowsRoot == "" {
		windowsRoot = os.Getenv("windir")
	}
	for _, relative := range []string{
		filepath.Join("System32", "wsl.exe"),
		filepath.Join("Sysnative", "wsl.exe"),
	} {
		if path := existingFile(filepath.Join(windowsRoot, relative)); path != "" {
			return path
		}
	}
	return existingFile(filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", "wsl.exe"))
}

func findExecutable(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func existingFile(path string) string {
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return path
}
