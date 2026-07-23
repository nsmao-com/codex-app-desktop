package main

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

func openPathInOS(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is required")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer.exe", path)
	case "darwin":
		command = exec.Command("open", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	configureBackgroundProcess(command)
	return command.Start()
}
