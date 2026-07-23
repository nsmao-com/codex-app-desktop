//go:build !windows

package main

import gopty "github.com/aymanbagabas/go-pty"

func configureTerminalCmd(cmd *gopty.Cmd) {
	_ = cmd
}
