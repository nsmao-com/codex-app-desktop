package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type CodexFeatureFlags struct {
	MemoriesEnabled              bool `json:"memoriesEnabled"`
	MemoriesGenerate             bool `json:"memoriesGenerate"`
	MemoriesUse                  bool `json:"memoriesUse"`
	MemoriesDisableExternalContext bool `json:"memoriesDisableExternalContext"`
	BrowserUseFullCDP            bool `json:"browserUseFullCDP"`
	InAppBrowser                 bool `json:"inAppBrowser"`
}

func codexConfigPath() string {
	home := resolveCodexHome()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "config.toml")
}

func readCodexFeatureFlags() CodexFeatureFlags {
	flags := CodexFeatureFlags{
		MemoriesGenerate: true,
		MemoriesUse:      true,
		InAppBrowser:     true,
	}
	path := codexConfigPath()
	if path == "" {
		return flags
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return flags
	}
	text := string(payload)
	flags.MemoriesEnabled = readTOMLBool(text, "features", "memories", false)
	flags.BrowserUseFullCDP = readTOMLBool(text, "features", "browser_use_full_cdp_access", false)
	flags.InAppBrowser = readTOMLBool(text, "features", "in_app_browser", true)
	flags.MemoriesGenerate = readTOMLBool(text, "memories", "generate_memories", true)
	flags.MemoriesUse = readTOMLBool(text, "memories", "use_memories", true)
	flags.MemoriesDisableExternalContext = readTOMLBool(text, "memories", "disable_on_external_context", false)
	return flags
}

func writeCodexFeatureFlags(flags CodexFeatureFlags) error {
	path := codexConfigPath()
	if path == "" {
		return os.ErrNotExist
	}
	payload, err := os.ReadFile(path)
	text := ""
	if err == nil {
		text = string(payload)
	} else if !os.IsNotExist(err) {
		return err
	}
	text = upsertTOMLBool(text, "features", "memories", flags.MemoriesEnabled)
	text = upsertTOMLBool(text, "features", "browser_use_full_cdp_access", flags.BrowserUseFullCDP)
	text = upsertTOMLBool(text, "features", "in_app_browser", flags.InAppBrowser)
	text = upsertTOMLBool(text, "memories", "generate_memories", flags.MemoriesGenerate)
	text = upsertTOMLBool(text, "memories", "use_memories", flags.MemoriesUse)
	text = upsertTOMLBool(text, "memories", "disable_on_external_context", flags.MemoriesDisableExternalContext)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

func readTOMLBool(text, section, key string, fallback bool) bool {
	sectionBody := extractTOMLSection(text, section)
	if sectionBody == "" {
		return fallback
	}
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*(true|false)\s*$`)
	match := re.FindStringSubmatch(sectionBody)
	if len(match) < 2 {
		return fallback
	}
	return match[1] == "true"
}

func extractTOMLSection(text, section string) string {
	re := regexp.MustCompile(`(?ms)^\[` + regexp.QuoteMeta(section) + `\]\s*\n(.*?)(?:\n\[|\z)`)
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func upsertTOMLBool(text, section, key string, value bool) string {
	literal := strconv.FormatBool(value)
	sectionHeader := "[" + section + "]"
	keyLine := key + " = " + literal
	keyRe := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*(true|false)\s*$`)

	if strings.Contains(text, sectionHeader) {
		re := regexp.MustCompile(`(?ms)(\[` + regexp.QuoteMeta(section) + `\]\s*\n)(.*?)(\n\[|\z)`)
		return re.ReplaceAllStringFunc(text, func(block string) string {
			parts := re.FindStringSubmatch(block)
			if len(parts) < 4 {
				return block
			}
			header, body, tail := parts[1], parts[2], parts[3]
			if keyRe.MatchString(body) {
				body = keyRe.ReplaceAllString(body, keyLine)
			} else {
				body = strings.TrimRight(body, "\n")
				if body != "" {
					body += "\n"
				}
				body += keyLine + "\n"
			}
			return header + body + tail
		})
	}

	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return text + sectionHeader + "\n" + keyLine + "\n"
}

// ensureCodexProviderUserAgent injects http_headers.User-Agent into the active
// custom model_provider so reverse-proxy channels that inspect headers see an
// official Codex Desktop identity (not a blank/default UA).
//
// Note: if the first hop is itself a Go reverse-proxy that rebuilds outbound
// requests without forwarding User-Agent, the second hop still sees
// "Go-http-client/2.0" — that can only be fixed on the proxy side.
func ensureCodexProviderUserAgent(clientName, clientTitle, clientVersion, cliVersion string) error {
	path := codexConfigPath()
	if path == "" {
		return nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	text := string(payload)
	providerID := readTOMLString(text, "", "model_provider")
	if providerID == "" || providerID == "openai" {
		return nil
	}
	section := "model_providers." + providerID
	if extractTOMLSection(text, section) == "" {
		// Provider table may live only as nested tables; still try to write headers.
	}
	ua := buildCodexStyleUserAgent(clientName, clientTitle, clientVersion, cliVersion)
	if ua == "" {
		return nil
	}
	next := upsertProviderUserAgent(text, providerID, ua)
	if next == text {
		return nil
	}
	return os.WriteFile(path, []byte(next), 0o600)
}

func buildCodexStyleUserAgent(clientName, clientTitle, clientVersion, cliVersion string) string {
	name := firstNonEmpty(strings.TrimSpace(clientName), "codex_desktop")
	title := firstNonEmpty(strings.TrimSpace(clientTitle), "Codex Desktop")
	version := firstNonEmpty(strings.TrimSpace(clientVersion), "0.1.0")
	cli := normalizeCodexCLIVersion(cliVersion)
	if cli == "" {
		cli = "0.144.6"
	}
	osLabel := "Windows"
	if runtime.GOOS == "darwin" {
		osLabel = "Mac OS"
	} else if runtime.GOOS == "linux" {
		osLabel = "Linux"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}
	// Mirrors official Codex UA shape used by app-server initialize:
	//   codex_desktop/0.144.6 (Windows …; x86_64) Codex Desktop (codex_desktop; 0.1.0)
	return fmt.Sprintf("%s/%s (%s; %s) %s (%s; %s)", name, cli, osLabel, arch, title, name, version)
}

func normalizeCodexCLIVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	// "codex-cli 0.144.6" / "codex 0.144.6" / "0.144.6"
	re := regexp.MustCompile(`(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)`)
	if match := re.FindStringSubmatch(value); len(match) > 1 {
		return match[1]
	}
	return ""
}

func readTOMLString(text, section, key string) string {
	body := text
	if section != "" {
		body = extractTOMLSection(text, section)
		if body == "" {
			return ""
		}
	}
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]*)"\s*$`)
	match := re.FindStringSubmatch(body)
	if len(match) < 2 {
		// bare keys without quotes are rare for provider ids but support them
		re2 := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*([A-Za-z0-9_.-]+)\s*$`)
		match = re2.FindStringSubmatch(body)
		if len(match) < 2 {
			return ""
		}
	}
	return strings.TrimSpace(match[1])
}

func upsertProviderUserAgent(text, providerID, userAgent string) string {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || userAgent == "" {
		return text
	}
	// Prefer nested table so we don't fight complex inline maps.
	// [model_providers.custom.http_headers]
	// "User-Agent" = "..."
	section := "model_providers." + providerID + ".http_headers"
	escapedUA := strings.ReplaceAll(userAgent, `\`, `\\`)
	escapedUA = strings.ReplaceAll(escapedUA, `"`, `\"`)
	keyLine := `"User-Agent" = "` + escapedUA + `"`

	sectionHeader := "[" + section + "]"
	keyRe := regexp.MustCompile(`(?m)^\s*"User-Agent"\s*=\s*".*"\s*$`)

	if strings.Contains(text, sectionHeader) {
		re := regexp.MustCompile(`(?ms)(\[` + regexp.QuoteMeta(section) + `\]\s*\n)(.*?)(\n\[|\z)`)
		return re.ReplaceAllStringFunc(text, func(block string) string {
			parts := re.FindStringSubmatch(block)
			if len(parts) < 4 {
				return block
			}
			header, body, tail := parts[1], parts[2], parts[3]
			if keyRe.MatchString(body) {
				body = keyRe.ReplaceAllString(body, keyLine)
			} else {
				body = strings.TrimRight(body, "\n")
				if body != "" {
					body += "\n"
				}
				body += keyLine + "\n"
			}
			return header + body + tail
		})
	}

	// Also strip a conflicting inline http_headers on the provider table so the
	// nested table is authoritative.
	providerSection := "model_providers." + providerID
	providerRe := regexp.MustCompile(`(?ms)(\[` + regexp.QuoteMeta(providerSection) + `\]\s*\n)(.*?)(\n\[|\z)`)
	if providerRe.MatchString(text) {
		text = providerRe.ReplaceAllStringFunc(text, func(block string) string {
			parts := providerRe.FindStringSubmatch(block)
			if len(parts) < 4 {
				return block
			}
			header, body, tail := parts[1], parts[2], parts[3]
			inlineRe := regexp.MustCompile(`(?m)^\s*http_headers\s*=\s*\{[^}]*\}\s*\n?`)
			body = inlineRe.ReplaceAllString(body, "")
			return header + body + tail
		})
	}

	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return text + sectionHeader + "\n" + keyLine + "\n"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
