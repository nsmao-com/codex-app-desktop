package main

import (
	"os"
	"path/filepath"
	"regexp"
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
