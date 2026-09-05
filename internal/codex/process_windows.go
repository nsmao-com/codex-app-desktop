//go:build windows

package codex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CREATE_NO_WINDOW keeps the app-server console hidden.
const createNoWindow = 0x08000000

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

// attachKillOnCloseJob puts the process in a Windows Job Object so that when the
// job handle is closed (or NiceCodex exits), the whole tree dies — including MCP
// servers (python / node / npx / …) that Codex spawns as children.
func attachKillOnCloseJob(proc *os.Process) (cleanup func(), err error) {
	if proc == nil || proc.Pid <= 0 {
		return func() {}, nil
	}
	// Always return a working cleanup, even when Windows refuses Job assignment.
	// Stop must run this fallback before closing stdin, while the root still lives.
	defer func() {
		if err != nil {
			cleanup = sync.OnceFunc(func() {
				killProcessTree(proc.Pid)
				_ = proc.Kill()
			})
		}
	}()

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}

	// PROCESS_SET_QUOTA | PROCESS_TERMINATE are required to assign into a job.
	const access = windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE | windows.PROCESS_QUERY_INFORMATION
	handle, err := windows.OpenProcess(access, false, uint32(proc.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(handle)

	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}

	cleanup = sync.OnceFunc(func() {
		// Closing the job with KILL_ON_JOB_CLOSE terminates every process in the job.
		_ = windows.CloseHandle(job)
	})
	return cleanup, nil
}

// killProcessTree forcefully terminates pid and all descendants (taskkill /T).
// Used as a fallback when the Job Object is unavailable or process already left it.
func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	configureProcess(cmd)
	_ = cmd.Run()
}
