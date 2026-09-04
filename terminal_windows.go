//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

const createNewConsole = 0x00000010

var wslTerminalProfileCache struct {
	sync.Mutex
	profile TerminalProfile
	distro  string
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
		if profile := detectWSLTerminalProfile(); !profile.Available {
			if profile.Status == "not-installed" {
				return errors.New("WSL is not installed")
			}
			return errors.New("no WSL distribution can be started")
		}
		args := []string{}
		if distro := detectedWSLDistro(); distro != "" {
			args = append(args, "-d", distro)
		}
		args = append(args, "--cd", workspace)
		command = exec.Command(binary, args...)
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
		if profile := detectWSLTerminalProfile(); !profile.Available {
			if profile.Status == "not-installed" {
				return nil, errors.New("WSL is not installed")
			}
			return nil, errors.New("no WSL distribution can be started")
		}
		args := []string{}
		if distro := detectedWSLDistro(); distro != "" {
			args = append(args, "-d", distro)
		}
		args = append(args, "--cd", workspace)
		return append([]string{binary}, args...), nil
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
	wslTerminalProfileCache.distro = ""
	if binary == "" {
		wslTerminalProfileCache.profile = profile
		wslTerminalProfileCache.checked = time.Now()
		return profile
	}

	profile.Status = "runtime-unavailable"
	distros, listErr := listWSLDistros(binary)
	if listErr == nil {
		// A WSL executable can be present while no distribution is registered.
		// Keep this distinct from a missing executable so the settings page can
		// offer the right repair command.
		if len(distros) == 0 {
			profile.Status = "runtime-unavailable"
		} else if distro := probeWSLDistros(binary, distros); distro != "" {
			profile.Available = true
			profile.Status = "ready"
			wslTerminalProfileCache.distro = distro
		}
	}
	wslTerminalProfileCache.profile = profile
	wslTerminalProfileCache.checked = time.Now()
	return profile
}

// detectedWSLDistro returns the distro selected by the last health check. The
// value is intentionally kept out of the public TerminalProfile JSON because it
// is only a launch detail, not a user setting.
func detectedWSLDistro() string {
	wslTerminalProfileCache.Lock()
	distro := wslTerminalProfileCache.distro
	wslTerminalProfileCache.Unlock()
	return distro
}

func listWSLDistros(binary string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-l", "-q")
	configureBackgroundProcess(command)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return parseWSLDistroList(output), nil
}

func probeWSLDistros(binary string, distros []string) string {
	// Probe in WSL's reported order (normally the default distro first), then
	// fall back to the first healthy distro when the default is broken.
	deadline := time.Now().Add(10 * time.Second)
	for _, distro := range distros {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if remaining > 4*time.Second {
			remaining = 4 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), remaining)
		command := exec.CommandContext(ctx, binary, "-d", distro, "--exec", "/bin/sh", "-lc", "exit 0")
		configureBackgroundProcess(command)
		err := command.Run()
		ctxErr := ctx.Err()
		cancel()
		if err == nil && ctxErr == nil {
			return distro
		}
	}
	return ""
}

func parseWSLDistroList(output []byte) []string {
	text := decodeWSLOutput(output)
	seen := make(map[string]struct{})
	distros := make([]string, 0, 4)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if line == "" {
			continue
		}
		// Some older WSL builds ignore --quiet and print a localized header.
		lower := strings.ToLower(line)
		if strings.Contains(lower, "windows subsystem for linux") || lower == "name" || strings.HasPrefix(lower, "default") {
			continue
		}
		key := strings.ToLower(line)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		distros = append(distros, line)
	}
	return distros
}

func decodeWSLOutput(output []byte) string {
	if len(output) < 2 {
		return string(output)
	}
	little := output[0] == 0xff && output[1] == 0xfe
	big := output[0] == 0xfe && output[1] == 0xff
	if !little && !big {
		// WSL commonly omits the BOM when stdout is redirected. Detect UTF-16 by
		// the NUL byte pattern before falling back to UTF-8/ACP bytes.
		limit := len(output)
		if limit > 64 {
			limit = 64
		}
		var oddNUL, evenNUL int
		for i := 0; i+1 < limit; i += 2 {
			if output[i] == 0 {
				evenNUL++
			}
			if output[i+1] == 0 {
				oddNUL++
			}
		}
		little = oddNUL >= 2 && oddNUL >= evenNUL*2
		big = evenNUL >= 2 && evenNUL >= oddNUL*2
	}
	if !little && !big {
		return string(output)
	}
	start := 0
	if len(output) >= 2 && ((little && output[0] == 0xff && output[1] == 0xfe) || (big && output[0] == 0xfe && output[1] == 0xff)) {
		start = 2
	}
	if len(output)-start < 2 {
		return ""
	}
	units := make([]uint16, 0, (len(output)-start)/2)
	for index := start; index+1 < len(output); index += 2 {
		if little {
			units = append(units, binary.LittleEndian.Uint16(output[index:index+2]))
		} else {
			units = append(units, binary.BigEndian.Uint16(output[index:index+2]))
		}
	}
	return string(utf16.Decode(units))
}

func invalidateTerminalProfileCache() {
	wslTerminalProfileCache.Lock()
	wslTerminalProfileCache.profile = TerminalProfile{}
	wslTerminalProfileCache.distro = ""
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
