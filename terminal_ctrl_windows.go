//go:build windows

package main

import (
	"syscall"

	gopty "github.com/aymanbagabas/go-pty"
)

// CREATE_NEW_PROCESS_GROUP keeps ConPTY shells out of the host console group so
// CTRL_C / CTRL_BREAK / CTRL_CLOSE from teardown do not terminate Nice Codex.
const createNewProcessGroup = 0x00000200

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
	// Keep the callback pinned for the lifetime of the process.
	consoleCtrlCallback = syscall.NewCallback(ignoreHostConsoleCtrl)
)

func init() {
	// GUI hosts can still receive console control events while a ConPTY session
	// is torn down. Returning 1 marks CTRL_C / BREAK / CLOSE as handled.
	_, _, _ = procSetConsoleCtrlHandler.Call(consoleCtrlCallback, 1)
}

func ignoreHostConsoleCtrl(ctrlType uint32) uintptr {
	switch ctrlType {
	case windowsCtrlCEvent, windowsCtrlBreakEvent, windowsCtrlCloseEvent:
		return 1
	default:
		return 0
	}
}

const (
	windowsCtrlCEvent     = 0
	windowsCtrlBreakEvent = 1
	windowsCtrlCloseEvent = 2
)

func configureTerminalCmd(cmd *gopty.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}
