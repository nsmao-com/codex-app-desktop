//go:build !windows

package codex

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
