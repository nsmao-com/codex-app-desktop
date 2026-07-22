package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	UpdateAvailable bool  `json:"updateAvailable"`
	ReleaseURL     string `json:"releaseUrl"`
	DownloadURL    string `json:"downloadUrl"`
	ReleaseNotes   string `json:"releaseNotes"`
	PublishedAt    string `json:"publishedAt"`
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func (s *AppService) CheckForUpdates() (UpdateInfo, error) {
	info := UpdateInfo{
		CurrentVersion: AppVersion,
		LatestVersion:  AppVersion,
		ReleaseURL:     "https://github.com/" + GitHubRepo + "/releases",
	}

	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+GitHubRepo+"/releases/latest", nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NiceCodex/"+AppVersion)

	resp, err := client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return info, nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return info, fmt.Errorf("GitHub releases API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return info, err
	}

	latest := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if latest == "" {
		return info, nil
	}
	info.LatestVersion = latest
	info.ReleaseURL = release.HTMLURL
	info.ReleaseNotes = strings.TrimSpace(release.Body)
	info.PublishedAt = release.PublishedAt
	info.UpdateAvailable = compareSemver(latest, AppVersion) > 0
	info.DownloadURL = pickReleaseAsset(release, runtime.GOOS, runtime.GOARCH)
	if info.DownloadURL == "" {
		info.DownloadURL = release.HTMLURL
	}
	return info, nil
}

func pickReleaseAsset(release githubRelease, goos, goarch string) string {
	needles := []string{}
	switch goos {
	case "windows":
		needles = []string{".exe", "windows", "win"}
	case "darwin":
		needles = []string{".dmg", "darwin", "macos", "mac"}
	case "linux":
		needles = []string{".AppImage", "linux"}
	}
	archNeedles := []string{}
	switch goarch {
	case "amd64":
		archNeedles = []string{"amd64", "x64", "x86_64"}
	case "arm64":
		archNeedles = []string{"arm64", "aarch64"}
	}

	best := ""
	bestScore := -1
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		score := 0
		for _, needle := range needles {
			if strings.Contains(name, strings.ToLower(needle)) {
				score += 2
			}
		}
		for _, needle := range archNeedles {
			if strings.Contains(name, needle) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			best = asset.BrowserDownloadURL
		}
	}
	if bestScore <= 0 {
		return ""
	}
	return best
}

// compareSemver returns 1 if a>b, -1 if a<b, 0 if equal (best-effort).
func compareSemver(a, b string) int {
	pa := splitSemver(a)
	pb := splitSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] > pb[i] {
			return 1
		}
		if pa[i] < pb[i] {
			return -1
		}
	}
	return 0
}

func splitSemver(value string) [3]int {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	parts := strings.Split(value, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		num := 0
		for _, ch := range parts[i] {
			if ch < '0' || ch > '9' {
				break
			}
			num = num*10 + int(ch-'0')
		}
		out[i] = num
	}
	return out
}
