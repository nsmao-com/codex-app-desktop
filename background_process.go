package main

import (
	"bytes"
	"context"
	"os/exec"
)

func runManagedCombinedOutput(ctx context.Context, command *exec.Cmd) ([]byte, error) {
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	cleanup, err := startManagedBackgroundProcess(ctx, command)
	if err != nil {
		return nil, err
	}
	err = command.Wait()
	cleanup()
	return output.Bytes(), err
}
