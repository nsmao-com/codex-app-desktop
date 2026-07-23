package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type GitBranchRequest struct {
	Name string `json:"name"`
}

type GitCommitRequest struct {
	Message string `json:"message"`
}

type GitActionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Branch  string `json:"branch,omitempty"`
	PRURL   string `json:"prUrl,omitempty"`
}

func (s *AppService) CreateGitBranch(request GitBranchRequest) (GitActionResult, error) {
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return GitActionResult{}, err
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return GitActionResult{}, errors.New("branch name is required")
	}
	if strings.ContainsAny(name, " \t\n") || strings.Contains(name, "..") {
		return GitActionResult{}, errors.New("invalid branch name")
	}
	prefix := strings.TrimSpace(s.Settings().GitBranchPrefix)
	if prefix != "" && !strings.HasPrefix(name, prefix) {
		name = prefix + name
	}
	if len(name) > 120 {
		return GitActionResult{}, errors.New("branch name is too long")
	}
	output, err := runGit(workspace, 12*time.Second, "checkout", "-b", name)
	if err != nil {
		return GitActionResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(output))
	}
	return GitActionResult{OK: true, Message: "branch created", Branch: name}, nil
}

func (s *AppService) CommitGitChanges(request GitCommitRequest) (GitActionResult, error) {
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return GitActionResult{}, err
	}
	message := strings.TrimSpace(request.Message)
	if message == "" {
		return GitActionResult{}, errors.New("commit message is required")
	}
	prefix := strings.TrimSpace(s.Settings().GitCommitPrefix)
	if prefix != "" && !strings.HasPrefix(message, strings.TrimSpace(prefix)) {
		message = strings.TrimSpace(prefix) + " " + message
		message = strings.TrimSpace(message)
	}
	if len(message) > 4000 {
		return GitActionResult{}, errors.New("commit message is too long")
	}
	if out, err := runGit(workspace, 20*time.Second, "add", "-A"); err != nil {
		return GitActionResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
	}
	output, err := runGit(workspace, 20*time.Second, "commit", "-m", message)
	if err != nil {
		return GitActionResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(output))
	}
	branch := currentGitBranch(workspace)
	return GitActionResult{OK: true, Message: "committed", Branch: branch}, nil
}

func (s *AppService) PushGitBranch() (GitActionResult, error) {
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return GitActionResult{}, err
	}
	branch := currentGitBranch(workspace)
	if branch == "" {
		return GitActionResult{}, errors.New("could not determine current branch")
	}
	output, err := runGit(workspace, 90*time.Second, "push", "-u", "origin", "HEAD")
	if err != nil {
		return GitActionResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(output))
	}
	result := GitActionResult{OK: true, Message: "pushed", Branch: branch}
	if !s.Settings().GitOpenPRAfterPush {
		return result, nil
	}
	prURL, prErr := s.openPullRequest(workspace, branch)
	if prErr != nil {
		result.Message = "pushed; could not open PR: " + prErr.Error()
		return result, nil
	}
	result.PRURL = prURL
	if prURL != "" {
		result.Message = "pushed and opened PR"
	}
	return result, nil
}

func (s *AppService) openPullRequest(workspace, branch string) (string, error) {
	body := strings.TrimSpace(s.Settings().GitPRBodyTemplate)
	args := []string{"pr", "create", "--fill", "--head", branch}
	if body != "" {
		args = []string{"pr", "create", "--title", branch, "--body", body, "--head", branch}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "gh", args...)
	command.Dir = workspace
	configureBackgroundProcess(command)
	output, err := command.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err == nil {
		url := extractHTTPURL(text)
		if url != "" {
			_ = s.app.Browser.OpenURL(url)
		}
		return url, nil
	}
	// Fallback: open compare page when gh is unavailable.
	remoteURL := gitRemoteHTTPS(workspace)
	if remoteURL == "" {
		return "", fmt.Errorf("%w: %s", err, text)
	}
	compare := strings.TrimSuffix(remoteURL, ".git") + "/compare/" + branch + "?expand=1"
	_ = s.app.Browser.OpenURL(compare)
	return compare, nil
}

func runGit(workspace string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", workspace}, args...)...)
	configureBackgroundProcess(command)
	output, err := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(output), errors.New("git command timed out")
	}
	return string(output), err
}

func currentGitBranch(workspace string) string {
	output, err := runGit(workspace, 5*time.Second, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func gitRemoteHTTPS(workspace string) string {
	output, err := runGit(workspace, 5*time.Second, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(output)
	if strings.HasPrefix(raw, "git@") {
		raw = strings.TrimPrefix(raw, "git@")
		raw = strings.Replace(raw, ":", "/", 1)
		raw = "https://" + raw
	}
	return raw
}

func extractHTTPURL(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") || strings.HasPrefix(line, "http://") {
			return line
		}
	}
	return ""
}
