//go:build !windows

package main

func setSystemSleepPrevention(active bool) {
	_ = active
}
