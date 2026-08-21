package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
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

type GitHubIssueImportContext struct {
	Available bool   `json:"available"`
	OwnerRepo string `json:"ownerRepo"`
	RemoteURL string `json:"remoteUrl"`
	Reason    string `json:"reason,omitempty"`
}

type GitHubIssueContent struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	URL    string   `json:"url"`
	State  string   `json:"state"`
	Author string   `json:"author"`
	Labels []string `json:"labels"`
}

func (s *AppService) GetGitHubIssueImportContext(workspace string) GitHubIssueImportContext {
	cleanWorkspace, err := validateWorkspace(workspace)
	if err != nil {
		return GitHubIssueImportContext{Reason: "Choose a valid workspace first"}
	}
	remoteURL, ownerRepo := githubRemoteContext(cleanWorkspace)
	if ownerRepo == "" {
		return GitHubIssueImportContext{RemoteURL: remoteURL, Reason: "This folder does not have a GitHub origin remote"}
	}
	return GitHubIssueImportContext{Available: true, OwnerRepo: ownerRepo, RemoteURL: remoteURL}
}

func (s *AppService) ImportGitHubIssue(workspace, reference string) (GitHubIssueContent, error) {
	contextInfo := s.GetGitHubIssueImportContext(workspace)
	if !contextInfo.Available {
		return GitHubIssueContent{}, errors.New(contextInfo.Reason)
	}
	ownerRepo, number, err := githubIssueReference(reference, contextInfo.OwnerRepo)
	if err != nil {
		return GitHubIssueContent{}, err
	}
	if issue, ghErr := readGitHubIssueWithCLI(ownerRepo, number); ghErr == nil {
		return issue, nil
	}
	return readPublicGitHubIssue(ownerRepo, number)
}

func githubRemoteContext(workspace string) (string, string) {
	remoteURL := strings.TrimSuffix(strings.TrimSpace(gitRemoteHTTPS(workspace)), "/")
	clean := strings.TrimSuffix(remoteURL, ".git")
	marker := "github.com/"
	index := strings.Index(strings.ToLower(clean), marker)
	if index < 0 {
		return remoteURL, ""
	}
	ownerRepo := strings.Trim(strings.TrimSpace(clean[index+len(marker):]), "/")
	if len(strings.Split(ownerRepo, "/")) != 2 {
		return remoteURL, ""
	}
	return remoteURL, ownerRepo
}

func githubIssueReference(reference, defaultOwnerRepo string) (string, int, error) {
	value := strings.TrimSpace(reference)
	ownerRepo := defaultOwnerRepo
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
			return "", 0, errors.New("enter a github.com issue URL")
		}
		parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
		if len(parts) != 4 || !strings.EqualFold(parts[2], "issues") {
			return "", 0, errors.New("enter a GitHub issue URL such as https://github.com/owner/repo/issues/123")
		}
		owner, ownerErr := url.PathUnescape(parts[0])
		repo, repoErr := url.PathUnescape(parts[1])
		if ownerErr != nil || repoErr != nil || strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" || strings.ContainsAny(owner+repo, "/\\") {
			return "", 0, errors.New("enter a valid GitHub issue URL")
		}
		ownerRepo = owner + "/" + repo
		value = parts[3]
	} else {
		value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
	}
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return "", 0, errors.New("enter an issue number such as #123 or a GitHub issue URL")
	}
	return ownerRepo, number, nil
}

func readGitHubIssueWithCLI(ownerRepo string, number int) (GitHubIssueContent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "gh", "issue", "view", strconv.Itoa(number), "--repo", ownerRepo, "--json", "number,title,body,url,state,author,labels")
	configureBackgroundProcess(command)
	payload, err := command.Output()
	if err != nil {
		return GitHubIssueContent{}, err
	}
	var raw struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
		State  string `json:"state"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return GitHubIssueContent{}, err
	}
	labels := make([]string, 0, len(raw.Labels))
	for _, label := range raw.Labels {
		if strings.TrimSpace(label.Name) != "" {
			labels = append(labels, label.Name)
		}
	}
	return GitHubIssueContent{Number: raw.Number, Title: raw.Title, Body: raw.Body, URL: raw.URL, State: raw.State, Author: raw.Author.Login, Labels: labels}, nil
}

func readPublicGitHubIssue(ownerRepo string, number int) (GitHubIssueContent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", ownerRepo, number)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return GitHubIssueContent{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "NiceCodex")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return GitHubIssueContent{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return GitHubIssueContent{}, fmt.Errorf("GitHub returned %s", response.Status)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return GitHubIssueContent{}, err
	}
	var raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return GitHubIssueContent{}, err
	}
	labels := make([]string, 0, len(raw.Labels))
	for _, label := range raw.Labels {
		if strings.TrimSpace(label.Name) != "" {
			labels = append(labels, label.Name)
		}
	}
	return GitHubIssueContent{Number: raw.Number, Title: raw.Title, Body: raw.Body, URL: raw.HTMLURL, State: raw.State, Author: raw.User.Login, Labels: labels}, nil
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
