package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ClaudePluginCatalog struct {
	Workspace string             `json:"workspace"`
	Plugins   []ClaudePluginView `json:"plugins"`
	Native    bool               `json:"native"`
	Warning   string             `json:"warning"`
}

type ClaudePluginToggleRequest struct {
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Workspace string `json:"workspace"`
	Enabled   bool   `json:"enabled"`
}

type ClaudePluginToggleResult struct {
	Catalog  ClaudePluginCatalog `json:"catalog"`
	Verified bool                `json:"verified"`
	Message  string              `json:"message"`
}

// ClaudePluginDetails is the native report for the effective plugin in a workspace.
// Claude resolves merged scope precedence; this is not an individual install's report.
type ClaudePluginDetails struct {
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
	Report    string `json:"report"`
}

type claudePluginRecord struct {
	ID             string                     `json:"id"`
	Version        string                     `json:"version"`
	Scope          string                     `json:"scope"`
	InstallPath    string                     `json:"installPath"`
	ReadFromFolder string                     `json:"readFromFolder"`
	FolderVersion  string                     `json:"folderVersion"`
	MCPServers     map[string]json.RawMessage `json:"mcpServers"`
	HasUserConfig  bool                       `json:"hasUserConfig"`
	ProjectPath    string                     `json:"projectPath"`
	Enabled        *bool                      `json:"enabled"`
	Errors         []string                   `json:"errors"`
	Notes          []string                   `json:"notes"`
}

func (record claudePluginRecord) view() ClaudePluginView {
	path := record.InstallPath
	if record.ReadFromFolder != "" {
		path = record.ReadFromFolder
	}
	names := make([]string, 0, len(record.MCPServers))
	for name := range record.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return ClaudePluginView{Name: record.ID, Version: record.Version, FolderVersion: record.FolderVersion, MCPServerNames: names, HasUserConfig: record.HasUserConfig, Scope: record.Scope, Path: path, ProjectPath: record.ProjectPath, Enabled: record.Enabled, Errors: record.Errors, Notes: record.Notes}
}

func claudePluginApplies(scope, project, workspace string) bool {
	switch scope {
	case "project", "local":
		return project != "" && workspace != "" && samePath(project, workspace)
	case "user", "managed", "synced", "session":
		return true
	default:
		return false
	}
}

func sortClaudePlugins(plugins []ClaudePluginView) {
	sort.SliceStable(plugins, func(i, j int) bool {
		if plugins[i].Name != plugins[j].Name {
			return plugins[i].Name < plugins[j].Name
		}
		return plugins[i].Scope < plugins[j].Scope
	})
}

func parseClaudePluginList(output, workspace string) ([]ClaudePluginView, error) {
	if len(output) > 4*1024*1024 {
		return nil, errors.New("Claude 插件目录过大")
	}
	var records []claudePluginRecord
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &records) != nil || strings.TrimSpace(output) == "null" {
		return nil, errors.New("Claude 未返回可识别的插件目录，请更新 CLI 后重试")
	}
	plugins := []ClaudePluginView{}
	for _, record := range records {
		if !validClaudePluginID(record.ID) {
			return nil, errors.New("Claude 插件目录包含无效名称")
		}
		if claudePluginApplies(record.Scope, record.ProjectPath, workspace) {
			plugins = append(plugins, record.view())
		}
	}
	sortClaudePlugins(plugins)
	return plugins, nil
}

func validClaudePluginID(id string) bool {
	return id != "" && len(id) <= 220 && !strings.HasPrefix(id, "-") && !strings.ContainsAny(id, " \t\r\n\x00")
}

func (s *AppService) claudePluginWorkspace(requested string) (string, error) {
	workspace := strings.TrimSpace(requested)
	current := strings.TrimSpace(s.Settings().ClaudeWorkspace)
	if workspace != current && (workspace == "" || current == "" || !samePath(workspace, current)) {
		return "", errors.New("Claude 工作区已切换，请重新读取插件目录")
	}
	if workspace == "" {
		return "", nil
	}
	return validateWorkspace(workspace)
}

// Use the same hidden, managed process wrapper as other CLI operations, but
// bind project scope to the requested workspace and preserve bridge cancellation.
func (s *AppService) runClaudePluginCommand(ctx context.Context, workspace string, args []string) (string, error) {
	executable := s.externalExecutable("claude")
	if executable == "" {
		return "", errors.New("未检测到 Claude Code，请在 CLI 工具页检查安装路径")
	}
	commandPath, commandArgs, err := providerCommand(executable, args)
	if err != nil {
		return "", errors.New("无法解析 Claude Code 启动命令")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := execCommandContext(ctx, commandPath, commandArgs...)
	command.Dir = workspace
	if command.Dir == "" {
		command.Dir = resolveClaudeHome()
	}
	output, err := runManagedCombinedOutput(ctx, command)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("Claude 插件命令未成功，请检查 CLI、插件依赖和管理策略：%w", err)
	}
	return string(output), nil
}

func (s *AppService) readNativeClaudePlugins(ctx context.Context, workspace string) (ClaudePluginCatalog, error) {
	output, err := s.runClaudePluginCommand(ctx, workspace, []string{"plugin", "list", "--json"})
	if err != nil {
		return ClaudePluginCatalog{}, err
	}
	plugins, err := parseClaudePluginList(output, workspace)
	if err != nil {
		return ClaudePluginCatalog{}, err
	}
	return ClaudePluginCatalog{Workspace: workspace, Plugins: plugins, Native: true}, nil
}

func (s *AppService) ReadClaudePluginCatalog(ctx context.Context, workspace string) (ClaudePluginCatalog, error) {
	workspace, err := s.claudePluginWorkspace(workspace)
	if err != nil {
		return ClaudePluginCatalog{}, err
	}
	catalog, err := s.readNativeClaudePlugins(ctx, workspace)
	if err == nil {
		return catalog, nil
	}
	if ctx.Err() != nil {
		return ClaudePluginCatalog{}, ctx.Err()
	}
	return ClaudePluginCatalog{Workspace: workspace, Plugins: listClaudePlugins(resolveClaudeHome(), workspace), Warning: "无法确认原生插件状态，仅展示本地安装记录，已禁用修改操作。" + err.Error()}, nil
}

func findClaudePlugin(catalog ClaudePluginCatalog, name, scope string) *ClaudePluginView {
	for i := range catalog.Plugins {
		if catalog.Plugins[i].Name == name && catalog.Plugins[i].Scope == scope {
			return &catalog.Plugins[i]
		}
	}
	return nil
}

func (s *AppService) ReadClaudePluginDetails(ctx context.Context, name, workspace string) (ClaudePluginDetails, error) {
	if !validClaudePluginID(name) {
		return ClaudePluginDetails{}, errors.New("插件名称无效，请重新读取目录")
	}
	workspace, err := s.claudePluginWorkspace(workspace)
	if err != nil {
		return ClaudePluginDetails{}, err
	}
	catalog, err := s.readNativeClaudePlugins(ctx, workspace)
	if err != nil {
		return ClaudePluginDetails{}, err
	}
	loaded := false
	for _, plugin := range catalog.Plugins {
		if plugin.Name == name && plugin.Enabled != nil && *plugin.Enabled && len(plugin.Errors) == 0 {
			loaded = true
			break
		}
	}
	if !loaded {
		return ClaudePluginDetails{}, errors.New("插件未启用、加载失败或安装记录已变化，请重新检测后重试")
	}
	if _, err := s.claudePluginWorkspace(workspace); err != nil {
		return ClaudePluginDetails{}, err
	}
	output, err := s.runClaudePluginCommand(ctx, workspace, []string{"plugin", "details", name})
	if err != nil {
		if ctx.Err() != nil {
			return ClaudePluginDetails{}, ctx.Err()
		}
		return ClaudePluginDetails{}, errors.New("无法读取插件能力报告，请更新 Claude Code 并检查插件加载状态；未修改插件配置")
	}
	if _, err := s.claudePluginWorkspace(workspace); err != nil {
		return ClaudePluginDetails{}, err
	}
	if len(output) > 128*1024 {
		return ClaudePluginDetails{}, errors.New("插件能力报告过大，请在 Claude Code 中查看")
	}
	report := strings.TrimSpace(ansiEscapePattern.ReplaceAllString(output, ""))
	if report == "" {
		return ClaudePluginDetails{}, errors.New("Claude 未返回插件能力报告，请更新 CLI 后重试")
	}
	return ClaudePluginDetails{Name: name, Workspace: workspace, Report: report}, nil
}

func (s *AppService) SetClaudePluginEnabled(ctx context.Context, request ClaudePluginToggleRequest) (ClaudePluginToggleResult, error) {
	if !validClaudePluginID(request.Name) || !isAllowed(request.Scope, "user", "project", "local") {
		return ClaudePluginToggleResult{}, errors.New("只能修改已安装的用户、项目或本地插件")
	}
	workspace, err := s.claudePluginWorkspace(request.Workspace)
	if err != nil {
		return ClaudePluginToggleResult{}, err
	}
	if request.Scope != "user" && workspace == "" {
		return ClaudePluginToggleResult{}, errors.New("请先选择 Claude 工作区")
	}
	s.mu.Lock()
	if s.providerReloading["claude"] || s.providerHasActiveWorkLocked("claude") {
		s.mu.Unlock()
		return ClaudePluginToggleResult{}, errors.New("Claude 正在运行任务或修改配置，请结束任务后重试")
	}
	if s.providerReloading == nil {
		s.providerReloading = map[string]bool{}
	}
	s.providerReloading["claude"] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.providerReloading, "claude"); s.mu.Unlock() }()
	before, err := s.readNativeClaudePlugins(ctx, workspace)
	if err != nil {
		return ClaudePluginToggleResult{}, err
	}
	plugin := findClaudePlugin(before, request.Name, request.Scope)
	if plugin == nil || plugin.Enabled == nil {
		return ClaudePluginToggleResult{}, errors.New("插件记录已变化或状态未知，请刷新后重试")
	}
	if *plugin.Enabled == request.Enabled {
		if request.Enabled && len(plugin.Errors) > 0 {
			return ClaudePluginToggleResult{Catalog: before, Message: "插件配置已启用，但原生加载存在错误，请展开加载位置与诊断处理。"}, nil
		}
		return ClaudePluginToggleResult{Catalog: before, Verified: true, Message: "插件已处于目标状态"}, nil
	}
	// Recheck after the read: changing workspaces must not redirect a pending action.
	if _, err := s.claudePluginWorkspace(request.Workspace); err != nil {
		return ClaudePluginToggleResult{}, err
	}
	action := "disable"
	if request.Enabled {
		action = "enable"
	}
	if _, err := s.runClaudePluginCommand(ctx, workspace, []string{"plugin", action, request.Name, "--scope", request.Scope}); err != nil {
		return ClaudePluginToggleResult{}, err
	}
	after, err := s.readNativeClaudePlugins(ctx, workspace)
	if err != nil {
		return ClaudePluginToggleResult{Catalog: ClaudePluginCatalog{Workspace: workspace, Plugins: []ClaudePluginView{}}, Message: "启停命令已执行，但无法重新读取状态。请重新检测后再操作。"}, nil
	}
	current := findClaudePlugin(after, request.Name, request.Scope)
	verified := current != nil && current.Enabled != nil && *current.Enabled == request.Enabled
	message := "已确认原生状态；新的 Claude 任务将读取此设置。"
	if !verified {
		message = "命令已执行，但原生状态未达到目标值，可能被更高优先级配置或管理策略覆盖。"
	} else if request.Enabled && len(current.Errors) > 0 {
		verified = false
		message = "插件配置已启用，但原生加载存在错误，请展开加载位置与诊断处理。"
	}
	return ClaudePluginToggleResult{Catalog: after, Verified: verified, Message: message}, nil
}
