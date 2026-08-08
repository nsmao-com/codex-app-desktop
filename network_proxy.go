package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
)

// Network proxy env keys applied for Clash / system HTTP proxies without TUN.
// Both cases are set so Go, Node, and common CLIs pick them up.
var networkProxyEnvKeys = []string{
	"HTTP_PROXY", "http_proxy",
	"HTTPS_PROXY", "https_proxy",
	"ALL_PROXY", "all_proxy",
	"NO_PROXY", "no_proxy",
}

var (
	networkProxyMu     sync.Mutex
	networkProxyActive bool
	// networkProxyBackup stores values before NiceCodex overwrote them.
	// nil entry means the key was unset in the process environment.
	networkProxyBackup map[string]*string
)

func defaultNetworkProxyNoProxy() string {
	return "localhost,127.0.0.1,::1"
}

// normalizeNetworkProxyURL accepts full URLs or host:port shorthand (defaults to http).
func normalizeNetworkProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > 512 {
		return "", fmt.Errorf("proxy URL is too long")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid proxy URL")
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("proxy scheme must be http, https, or socks5")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("proxy host is required")
	}
	parsed.Fragment = ""
	// Reject path-only nonsense like "http:///foo".
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("proxy host is required")
	}
	return parsed.String(), nil
}

func sanitizeNetworkProxyNoProxy(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 1024 {
		raw = raw[:1024]
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return strings.Join(out, ",")
}

// applyNetworkProxyFromSettings injects HTTP(S)_PROXY into the NiceCodex process
// so Go clients and child CLIs (codex/claude/grok/gemini/opencode) inherit them
// without requiring Clash TUN mode.
func applyNetworkProxyFromSettings(settings UserSettings) {
	networkProxyMu.Lock()
	defer networkProxyMu.Unlock()

	restoreNetworkProxyEnvLocked()

	if !settings.NetworkProxyEnabled {
		return
	}
	proxyURL := strings.TrimSpace(settings.NetworkProxyURL)
	if proxyURL == "" {
		return
	}
	noProxy := strings.TrimSpace(settings.NetworkProxyNoProxy)
	if noProxy == "" {
		noProxy = defaultNetworkProxyNoProxy()
	}

	backup := make(map[string]*string, len(networkProxyEnvKeys))
	for _, key := range networkProxyEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			copyValue := value
			backup[key] = &copyValue
		} else {
			backup[key] = nil
		}
	}
	networkProxyBackup = backup
	networkProxyActive = true

	_ = os.Setenv("HTTP_PROXY", proxyURL)
	_ = os.Setenv("http_proxy", proxyURL)
	_ = os.Setenv("HTTPS_PROXY", proxyURL)
	_ = os.Setenv("https_proxy", proxyURL)
	_ = os.Setenv("ALL_PROXY", proxyURL)
	_ = os.Setenv("all_proxy", proxyURL)
	_ = os.Setenv("NO_PROXY", noProxy)
	_ = os.Setenv("no_proxy", noProxy)
}

func restoreNetworkProxyEnvLocked() {
	if !networkProxyActive {
		return
	}
	for _, key := range networkProxyEnvKeys {
		previous, ok := networkProxyBackup[key]
		if !ok || previous == nil {
			_ = os.Unsetenv(key)
			continue
		}
		_ = os.Setenv(key, *previous)
	}
	networkProxyActive = false
	networkProxyBackup = nil
}
