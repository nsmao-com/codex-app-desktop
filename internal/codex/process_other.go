//go:build !windows

package codex

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func configureProcess(command *exec.Cmd) {
	// Own process group so we can signal the whole tree on stop.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachKillOnCloseJob(_ *os.Process) (func(), error) {
	return func() {}, nil
}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	// Negative PID = process group (requires Setpgid on start).
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	// Fallback single-process kill.
	_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
}
