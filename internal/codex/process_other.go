//go:build !windows

package codex

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
)

func configureProcess(command *exec.Cmd) {
	// Own process group so we can signal the whole tree on stop.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachKillOnCloseJob(process *os.Process) (func(), error) {
	if process == nil || process.Pid <= 0 {
		return func() {}, nil
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
		})
	}, nil
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
