# Nice Codex Desktop

[English README](./README.md)

**Nice Codex** 是面向官方 OpenAI Codex 运行时的非官方轻量桌面客户端。基于 Wails v3 + Vue 3，通过本机 `codex app-server` 完成登录、会话、回合、审批、工具与流式事件。

> 当前版本：**v1.0.0**  
> 发布页：[github.com/nsmao-com/codex-app-desktop/releases](https://github.com/nsmao-com/codex-app-desktop/releases)

## 功能亮点

- 纯 Codex 工作台，用本地 CLI / app-server 替代官方 Codex Desktop 流程
- 工作区选择、最近项目、流式对话、审批、Plan / 协作模式
- 能力中心：Skills、MCP、Apps、Hooks、实验特性
- 内嵌交互终端（ConPTY / PTY）
- 外观：主题、强调色、**本机系统字体下拉**（Windows 注册表 / macOS 字体族）
- 侧边栏左上角 **版本号**，并自动检测 **GitHub Releases 更新**

## 环境要求

- Go **1.25+**
- Node.js **22+**
- **pnpm** 10+
- [Wails v3 CLI](https://v3.wails.io/)（建议 `v3.0.0-alpha2.117`）
- 官方 Codex CLI（`@openai/codex`）

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.117
pnpm add -g @openai/codex
```

## 终端用户安装

1. 打开 [Releases](https://github.com/nsmao-com/codex-app-desktop/releases)。
2. 按平台下载：
   - Windows：`NiceCodex-<version>-windows-amd64.exe`
   - macOS Apple Silicon：`NiceCodex-<version>-darwin-arm64.zip`
   - macOS Intel：`NiceCodex-<version>-darwin-amd64.zip`
3. 运行 / 解压后，在应用内完成 Codex / ChatGPT 登录。

应用启动时会检查 GitHub Releases。若有更新，侧边栏版本号旁会出现提示。偏好设置 → 外观里也可手动「检查更新」。

## 开发启动

```powershell
Set-Location frontend
pnpm install
Set-Location ..
go mod tidy
wails3 generate bindings -clean=true -ts -i -d frontend/bindings
wails3 dev -config ./build/config.yml -port 9245
```

开发模式请不要单独执行 `wails3 task run`，它会立刻打开原生窗口并要求 `FRONTEND_DEVSERVER_URL` 已就绪。

### 本地构建

```powershell
# 当前系统二进制
wails3 build

# Windows 安装包（NSIS，需 makensis）
wails3 package

# macOS .app（需在 macOS 上执行）
wails3 task darwin:package
```

## 架构

```text
Vue 3 + TypeScript
        |
   Wails bindings
        |
     Go service
   /            \
Codex JSON-RPC   Git / 设置 / 字体 / 更新 / 终端
        |
codex app-server (stdio)
```

## 版本号与发布

版本源（需保持一致）：

| 文件 | 字段 |
|------|------|
| `version.go` | `AppVersion` |
| `build/config.yml` | `info.version` |
| `frontend/package.json` | `version` |

### 发布新版本

```bash
# 1. 同步抬高上面三处版本（例如 1.0.1）
# 2. 提交到 main
git add -A
git commit -m "chore: bump version to 1.0.1"
git push origin main

# 3. 打 tag 并推送 — Actions 会构建 Windows + macOS 并创建 Release
git tag v1.0.1
git push origin v1.0.1
```

工作流：[`.github/workflows/release.yml`](./.github/workflows/release.yml)

- 触发：`v*` tag（以及手动 `workflow_dispatch`）
- 产物：`windows-amd64`、`darwin-arm64`、`darwin-amd64`
- 自动上传资源并生成 GitHub Release

## 自动更新逻辑

`CheckForUpdates()` 请求 `https://api.github.com/repos/nsmao-com/codex-app-desktop/releases/latest`，与本地 `AppVersion` 做 semver 比较，再按 OS/架构关键字挑选资源（`windows` / `.exe`，`darwin` / `macos` / `mac`，`amd64` / `arm64`）。点击更新提示会用系统浏览器打开对应下载地址或 Release 页。

## 声明

非官方社区客户端，与 OpenAI 无隶属关系。请自行承担使用风险，并遵守 Codex / OpenAI 相关条款。
