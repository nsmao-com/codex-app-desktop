package main

// AppVersion is the Nice Codex release version shown in UI and used for update checks.
// Keep in sync with build/config.yml info.version and frontend/package.json.
// CI may override via: -ldflags "-X main.AppVersion=x.y.z"
var AppVersion = "1.0.2"

// GitHubRepo is the public release source for auto-update checks.
const GitHubRepo = "nsmao-com/codex-app-desktop"

func (s *AppService) AppVersion() string {
	return AppVersion
}
