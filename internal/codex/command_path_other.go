//go:build !windows

package codex

func enrichProcessPath() {}

func resolveWindowsExtraCommands() (commandSpec, bool) {
	return commandSpec{}, false
}
