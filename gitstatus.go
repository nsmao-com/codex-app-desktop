package main

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (s *AppService) ReadWorkspaceDiff(relativePath string) (string, error) {
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return "", err
	}
	relativePath = strings.TrimSpace(relativePath)
	if _, renamedPath, found := strings.Cut(relativePath, " -> "); found {
		relativePath = renamedPath
	}
	relativePath = strings.Trim(relativePath, "\"")
	cleanPath := filepath.Clean(relativePath)
	resolvedPath := filepath.Join(workspace, cleanPath)
	rel, relErr := filepath.Rel(workspace, resolvedPath)
	if relativePath == "" || filepath.IsAbs(relativePath) || relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("a valid workspace-relative file path is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", workspace, "diff", "--no-ext-diff", "--unified=3", "--", cleanPath)
	configureBackgroundProcess(command)
	output, commandErr := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", errors.New("diff inspection timed out")
	}
	if commandErr != nil && len(output) == 0 {
		return "", commandErr
	}
	if len(output) == 0 {
		command = exec.CommandContext(ctx, "git", "-C", workspace, "diff", "--no-ext-diff", "--unified=3", "--cached", "--", cleanPath)
		configureBackgroundProcess(command)
		output, commandErr = command.CombinedOutput()
	}
	return string(output), commandErr
}

func inspectWorkspace(path string) WorkspaceInfo {
	workspace := WorkspaceInfo{
		Name:    filepath.Base(path),
		Path:    path,
		Changes: []GitChange{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", path, "status", "--short", "--branch", "--porcelain=v1")
	configureBackgroundProcess(command)
	output, err := command.Output()
	if err != nil {
		workspace.GitError = "This folder is not a Git repository."
		return workspace
	}

	workspace.IsGit = true
	for index, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		if index == 0 && strings.HasPrefix(line, "## ") {
			branch := strings.TrimPrefix(line, "## ")
			if position := strings.Index(branch, "..."); position >= 0 {
				branch = branch[:position]
			}
			workspace.Branch = strings.TrimSpace(branch)
			continue
		}
		if len(line) < 4 {
			continue
		}
		workspace.Changes = append(workspace.Changes, GitChange{
			Status: strings.TrimSpace(line[:2]),
			Path:   strings.TrimSpace(line[3:]),
		})
	}
	return workspace
}
