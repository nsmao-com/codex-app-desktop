//go:build !windows

package main

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
)

func configureBackgroundProcess(_ *exec.Cmd) {}

func startManagedBackgroundProcess(ctx context.Context, command *exec.Cmd) (func(), error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			if command.Process != nil && command.Process.Pid > 0 {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			}
		})
	}
	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			cleanup()
		}()
	}
	return cleanup, nil
}
