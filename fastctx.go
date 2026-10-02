package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"nice_codex_desktop/internal/codex"
)

// FastCtx is a Codex integration, not an agent runtime. Keep it out of cliPackages.
type FastCtxStatus struct {
	Installed       bool   `json:"installed"`
	Executable      string `json:"executable"`
	Version         string `json:"version"`
	ManagedVersion  string `json:"managedVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	UpdateError     string `json:"updateError"`
	CanInstall      bool   `json:"canInstall"`
	CodexHome       string `json:"codexHome"`
	State           string `json:"state"`
	ShellEnabled    bool   `json:"shellEnabled"`
	Message         string `json:"message"`
	Output          string `json:"output"`
}

type FastCtxActionResult struct {
	OK              bool          `json:"ok"`
	RestartRequired bool          `json:"restartRequired"`
	Message         string        `json:"message"`
	Output          string        `json:"output"`
	Status          FastCtxStatus `json:"status"`
}

var fastCtxMu sync.Mutex

// Upstream resolves HOME before USERPROFILE, including on Windows.
func fastCtxDirectory() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".fastctx")
}

func fastCtxManagedBinary() string {
	name := "fastctx"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(fastCtxDirectory(), "bin", name)
}

func fastCtxExecutable() string {
	// Prefer the pnpm launcher after updates, even if an older native binary is on PATH.
	if home := os.Getenv("PNPM_HOME"); home != "" {
		for _, name := range commandCandidates("fastctx") {
			if path, err := exec.LookPath(filepath.Join(home, name)); err == nil {
				return path
			}
		}
	}
	if path := findCommand(commandCandidates("fastctx")); path != "" {
		return path
	}
	if path, err := exec.LookPath(fastCtxManagedBinary()); err == nil {
		return path
	}
	return ""
}

func (s *AppService) runFastCtxCommand(executable string, args []string, timeout time.Duration, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(s.serviceContext(), timeout)
	defer cancel()
	command, resolvedArgs, err := providerCommand(executable, args)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, command, resolvedArgs...)
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = replaceEnvironmentValue(env, "FASTCTX_DISABLE_UPDATE_CHECK", "1")
	output, err := runManagedCombinedOutput(ctx, cmd)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return strings.TrimSpace(ansiEscapePattern.ReplaceAllString(string(output), "")), err
}

// Exit zero alone is insufficient: upstream reports "not applied" as INFO/zero.
// Fail closed if a future upstream version changes the status contract.
func fastCtxApplicationState(output string, commandErr error, version, managedVersion string, shellEnabled bool) string {
	checks := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		for _, level := range []string{"PASS", "INFO", "FAIL"} {
			if body, ok := strings.CutPrefix(line, "["+level+"] "); ok {
				name, _, found := strings.Cut(body, ":")
				if found {
					checks[name] = level
				}
			}
		}
	}
	if commandErr != nil {
		return "needs_attention"
	}
	for _, level := range checks {
		if level == "FAIL" {
			return "needs_attention"
		}
	}
	if checks["Applied state"] == "INFO" {
		return "not_applied"
	}
	for _, name := range []string{"Applied state", "Installed binary", "MCP server contract", "AGENTS guidance"} {
		if checks[name] != "PASS" {
			return "needs_attention"
		}
	}
	if version == "" || managedVersion == "" || version != managedVersion {
		return "needs_apply"
	}
	if shellEnabled && checks["fastshell"] != "PASS" {
		return "needs_apply"
	}
	if !shellEnabled && checks["fastshell"] == "INFO" && strings.Contains(output, "fastshell is still applied") {
		return "needs_apply"
	}
	return "applied"
}

func (s *AppService) CheckFastCtx(checkUpdates bool) (FastCtxStatus, error) {
	if !fastCtxMu.TryLock() {
		return FastCtxStatus{}, errors.New("FastCtx 正在处理另一项操作，请稍后重试")
	}
	defer fastCtxMu.Unlock()
	return s.checkFastCtx(checkUpdates), nil
}

func (s *AppService) checkFastCtx(checkUpdates bool) FastCtxStatus {
	codex.EnrichPathForLookups()
	pm, nodeOK, _ := detectNodePackageManager()
	status := FastCtxStatus{CodexHome: resolveCodexHome(), State: "not_installed", CanInstall: nodeOK && pm == "pnpm"}
	status.Executable = fastCtxExecutable()
	if content, err := os.ReadFile(filepath.Join(fastCtxDirectory(), "config.toml")); err == nil {
		status.ShellEnabled = readTOMLBool(string(content), "fastshell", "enabled", false)
	}
	if status.Executable != "" {
		version, err := s.runFastCtxCommand(status.Executable, []string{"--version"}, 10*time.Second, nil)
		status.Installed = err == nil && semverInText.MatchString(version)
		if status.Installed {
			status.Version = semverInText.FindString(version)
			managed, managedErr := s.runFastCtxCommand(fastCtxManagedBinary(), []string{"--version"}, 10*time.Second, nil)
			if managedErr == nil {
				status.ManagedVersion = semverInText.FindString(managed)
			}
			output, statusErr := s.runFastCtxCommand(status.Executable, []string{"status", "--codex-home", status.CodexHome}, 90*time.Second, nil)
			status.Output = output
			status.State = fastCtxApplicationState(output, statusErr, status.Version, status.ManagedVersion, status.ShellEnabled)
			if statusErr != nil {
				status.Output += "\n" + statusErr.Error()
			}
		} else {
			status.State = "needs_attention"
			status.Output = version
			if err != nil {
				status.Output += "\n" + err.Error()
			}
		}
	}
	if checkUpdates {
		latest, err := fetchNPMLatestVersion("fastctx")
		if err != nil {
			status.UpdateError = "暂时无法查询上游版本；本地应用状态仍可查看。"
		} else {
			status.LatestVersion = latest
			status.UpdateAvailable = status.Version != "" && compareSemver(latest, status.Version) > 0
		}
	}
	status.Message = map[string]string{
		"not_installed":   "尚未安装 FastCtx，可一键安装并应用到 Codex。",
		"not_applied":     "FastCtx 尚未完整应用到当前 Codex 配置，请点击应用。",
		"needs_apply":     "安装版本或工具设置尚未同步到 Codex，请重新应用。",
		"needs_attention": "FastCtx 检查未通过，可重新应用；若仍失败，请查看检测详情。",
		"applied":         "Codex 配置、全局指令和 FastCtx 服务检查通过。",
	}[status.State]
	return status
}

// SyncFastCtx follows the published upstream package; Apply owns the Codex
// configuration, managed binary and AGENTS block so upgrades follow its contract.
func (s *AppService) SyncFastCtx(update bool, shellEnabled bool) (FastCtxActionResult, error) {
	if !fastCtxMu.TryLock() {
		return FastCtxActionResult{}, errors.New("FastCtx 正在处理另一项操作，请稍后重试")
	}
	defer fastCtxMu.Unlock()
	var output string
	var targetVersion string
	initialized := false
	result := func(message string, changed bool) FastCtxActionResult {
		status := s.checkFastCtx(false)
		status.LatestVersion = targetVersion
		status.UpdateAvailable = targetVersion != "" && status.Version != "" && compareSemver(targetVersion, status.Version) > 0
		if changed && status.UpdateAvailable {
			message = "已应用，但安装版本仍落后于上游，请查看操作详情后重新同步。"
		}
		return FastCtxActionResult{OK: changed && status.State == "applied" && !status.UpdateAvailable, RestartRequired: changed || initialized,
			Message: message, Output: output, Status: status}
	}
	if update {
		if !cliPNPMInstallMu.TryLock() {
			return FastCtxActionResult{}, errors.New("其他工具正在通过 pnpm 安装，请完成后重试")
		}
		defer cliPNPMInstallMu.Unlock()
		codex.EnrichPathForLookups()
		pm, nodeOK, _ := detectNodePackageManager()
		if !nodeOK || pm != "pnpm" {
			return FastCtxActionResult{}, errors.New("请先安装 Node.js 18 或更新版本及 pnpm")
		}
		latest, versionErr := fetchNPMLatestVersion("fastctx")
		if versionErr != nil || !semverInText.MatchString(latest) || semverInText.FindString(latest) != latest {
			return result("无法确认 FastCtx 上游最新版本，请检查网络后重试。", false), nil
		}
		targetVersion = latest
		env, _, err := preparePNPMGlobalEnvironment()
		if err != nil {
			return FastCtxActionResult{}, err
		}
		// No lifecycle scripts are required by the upstream launcher/native packages.
		args := pnpmGlobalInstallArgs("fastctx", false)
		args[len(args)-1] = "fastctx@" + targetVersion
		args = append(args, "--ignore-scripts")
		output, err = s.runFastCtxCommand(packageManagerBinary(pm), args, 8*time.Minute, env)
		if err != nil {
			return result("安装或更新失败，请查看详情后重试："+err.Error(), false), nil
		}
	}
	executable := fastCtxExecutable()
	if executable == "" {
		return result("未找到 FastCtx，请先安装并应用。", false), nil
	}
	versionOutput, versionErr := s.runFastCtxCommand(executable, []string{"--version"}, 10*time.Second, nil)
	if versionErr != nil || !semverInText.MatchString(versionOutput) {
		output = strings.TrimSpace(output + "\n" + versionOutput)
		return result("FastCtx 无法运行，请检查安装后重试。", false), nil
	}
	if targetVersion != "" && semverInText.FindString(versionOutput) != targetVersion {
		return result("安装命令已结束，但当前程序版本未切换到目标版本，请检查 pnpm 安装路径后重试。", false), nil
	}
	apply := func() error {
		// Serialize with NiceCodex's other config writers while upstream commits its transaction.
		providerFileMu.Lock()
		defer providerFileMu.Unlock()
		applyOutput, err := s.runFastCtxCommand(executable, []string{"apply", "--codex-home", resolveCodexHome(), "--yes"}, 90*time.Second, nil)
		output = strings.TrimSpace(output + "\n" + applyOutput)
		return err
	}
	settingsPath := filepath.Join(fastCtxDirectory(), "config.toml")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		// Let upstream initialize its schema and defaults. A hand-written file
		// containing only [fastshell] is invalid (schema_version is mandatory).
		if err := apply(); err != nil {
			return result("首次应用未完成："+err.Error(), false), nil
		}
		initialized = true
	} else if err != nil {
		return result("无法读取 FastCtx 配置："+err.Error(), false), nil
	}
	// Reuse the existing atomic config editor; retain upstream budgets and user settings.
	err := updateProviderTextConfig(settingsPath, func(text string) (string, error) {
		next := upsertTOMLBool(text, "fastshell", "enabled", shellEnabled)
		if readTOMLBool(next, "fastshell", "enabled", !shellEnabled) != shellEnabled {
			return "", errors.New("无法识别 fastshell 配置段，请检查 FastCtx 配置格式")
		}
		return next, nil
	})
	if err != nil {
		return result("无法保存工具设置："+err.Error(), false), nil
	}
	if applyErr := apply(); applyErr != nil {
		return result(fmt.Sprintf("应用未完成，工具偏好已保存，可修复后重试：%v", applyErr), false), nil
	}
	response := result("已应用到 Codex。请重启 Codex 连接或 NiceCodex，让新会话加载工具。", true)
	if response.Status.State != "applied" {
		response.Message = "已执行应用，但复检未通过，请查看检测详情。"
	}
	return response, nil
}
