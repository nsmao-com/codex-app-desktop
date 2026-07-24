# Nice Codex Desktop

[中文文档](./README.zh-CN.md)

**Nice Codex** is an unofficial, lightweight desktop client for the official OpenAI Codex runtime. It ships as a native Windows / macOS app (Wails v3 + Vue 3) and talks to the local `codex app-server` for auth, threads, turns, approvals, tools, and streamed agent events.

> Current version: **v1.0.3**
> Releases: [github.com/nsmao-com/codex-app-desktop/releases](https://github.com/nsmao-com/codex-app-desktop/releases)

## Highlights

- Codex-only workbench that replaces the official Codex Desktop flow with a local CLI / app-server
- Workspace picker, recent workspaces, streamed chat, approvals, plan / collaboration modes
- Capability center for Skills, MCP, Apps, Hooks, and experimental features
- Embedded interactive terminal (ConPTY / PTY)
- Appearance: theme, accent, **local system font dropdown** (Windows registry / macOS font families)
- **Version badge** in the sidebar + **GitHub Releases auto-update check**

## Requirements

- Go **1.25+**
- Node.js **22+**
- **pnpm** 10+
- [Wails v3 CLI](https://v3.wails.io/) (`v3.0.0-alpha2.117` recommended)
- Official Codex CLI (`@openai/codex`)

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.117
pnpm add -g @openai/codex
```

## Install (end users)

1. Open the [Releases](https://github.com/nsmao-com/codex-app-desktop/releases) page.
2. Download the asset for your platform:
   - Windows: `NiceCodex-<version>-windows-amd64.exe`
   - macOS Apple Silicon: `NiceCodex-<version>-darwin-arm64.zip`
   - macOS Intel: `NiceCodex-<version>-darwin-amd64.zip`
3. Run / unzip, then sign in with Codex / ChatGPT through the in-app login flow.

The app checks GitHub Releases on startup. When a newer tag exists, the sidebar shows an update hint next to the version badge. Preferences → Appearance also has **Check for updates**.

## Development

```powershell
Set-Location frontend
pnpm install
Set-Location ..
go mod tidy
wails3 generate bindings -clean=true -ts -i -d frontend/bindings
wails3 dev -config ./build/config.yml -port 9245
```

Do **not** run `wails3 task run` alone for development — that expects `FRONTEND_DEVSERVER_URL` to already be up.

### Build locally

```powershell
# current OS binary
wails3 build

# Windows package (NSIS, requires makensis)
wails3 package

# macOS .app (must run on macOS)
wails3 task darwin:package
```

## Architecture

```text
Vue 3 + TypeScript
        |
   Wails bindings
        |
     Go service
   /            \
Codex JSON-RPC   Git / settings / fonts / updates / terminal
        |
codex app-server (stdio)
```

## Versioning & releases

Version sources (keep in sync):

| File | Field |
|------|--------|
| `version.go` | `AppVersion` |
| `build/config.yml` | `info.version` |
| `frontend/package.json` | `version` |

### Publish a new version

```bash
# 1. Bump the three version fields above (e.g. 1.0.1)
# 2. Commit on main
git add -A
git commit -m "chore: bump version to 1.0.1"
git push origin main

# 3. Tag and push — GitHub Actions builds Windows + macOS and creates the Release
git tag v1.0.1
git push origin v1.0.1
```

Workflow: [`.github/workflows/release.yml`](./.github/workflows/release.yml)

- Triggers on `v*` tags (and manual `workflow_dispatch`)
- Builds `windows-amd64` + `darwin-arm64` + `darwin-amd64`
- Uploads assets and creates a GitHub Release with generated notes

## Auto-update behaviour

`CheckForUpdates()` calls `https://api.github.com/repos/nsmao-com/codex-app-desktop/releases/latest`, compares semver with the local `AppVersion`, and picks a download asset by OS/arch keywords (`windows` / `.exe`, `darwin` / `macos` / `mac`, `amd64` / `arm64`). Opening the update hint launches the asset URL (or the release page) in the system browser.

## License / disclaimer

Unofficial community client. Not affiliated with OpenAI. Use at your own risk and follow Codex / OpenAI terms of use.
