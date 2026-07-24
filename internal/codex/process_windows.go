//go:build windows

package codex

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
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
//
// Official Codex Desktop often leaves those children orphaned after reconnect or
// exit because only the parent PID is killed.
func attachKillOnCloseJob(proc *os.Process) (cleanup func(), err error) {
	if proc == nil || proc.Pid <= 0 {
		return func() {}, nil
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}, fmt.Errorf("CreateJobObject: %w", err)
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
		return func() {}, fmt.Errorf("SetInformationJobObject: %w", err)
	}

	// PROCESS_SET_QUOTA | PROCESS_TERMINATE are required to assign into a job.
	const access = windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE | windows.PROCESS_QUERY_INFORMATION
	handle, err := windows.OpenProcess(access, false, uint32(proc.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return func() {}, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(handle)

	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		_ = windows.CloseHandle(job)
		return func() {}, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}

	var closed bool
	cleanup = func() {
		if closed {
			return
		}
		closed = true
		// Closing the job with KILL_ON_JOB_CLOSE terminates every process in the job.
		_ = windows.CloseHandle(job)
	}
	return cleanup, nil
}

// killProcessTree forcefully terminates pid and all descendants (taskkill /T).
// Used as a fallback when the Job Object is unavailable or process already left it.
func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	configureProcess(cmd)
	_ = cmd.Run()
}
