//go:build windows

package main

import (
	"sync"
	"syscall"
)

var (
	sleepKernel32          = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecution = sleepKernel32.NewProc("SetThreadExecutionState")
	sleepMu                sync.Mutex
	sleepHeld              bool
)

const (
	esContinuous       = 0x80000000
	esSystemRequired   = 0x00000001
	esAwayModeRequired = 0x00000040
)

func setSystemSleepPrevention(active bool) {
	sleepMu.Lock()
	defer sleepMu.Unlock()
	if active == sleepHeld {
		return
	}
	if active {
		_, _, _ = procSetThreadExecution.Call(uintptr(esContinuous | esSystemRequired | esAwayModeRequired))
		sleepHeld = true
		return
	}
	_, _, _ = procSetThreadExecution.Call(uintptr(esContinuous))
	sleepHeld = false
}
