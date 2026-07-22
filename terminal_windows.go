//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const createNewConsole = 0x00000010

func platformTerminalProfiles() []TerminalProfile {
	return []TerminalProfile{
		{ID: "powershell", Name: "PowerShell", Description: "PowerShell 7, with Windows PowerShell as fallback.", Available: findPowerShell() != ""},
		{ID: "git-bash", Name: "Git Bash", Description: "Git for Windows Bash terminal.", Available: findGitBash() != ""},
		{ID: "wsl", Name: "WSL", Description: "Default Windows Subsystem for Linux distribution.", Available: findExecutable("wsl.exe") != ""},
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
		binary := findExecutable("wsl.exe")
		if binary == "" {
			return errors.New("WSL is not installed")
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
		binary := findExecutable("wsl.exe")
		if binary == "" {
			return nil, errors.New("WSL is not installed")
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
