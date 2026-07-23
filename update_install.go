package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type UpdateProgress struct {
	Phase          string `json:"phase"`
	Percent        int    `json:"percent"`
	BytesReceived  int64  `json:"bytesReceived"`
	BytesTotal     int64  `json:"bytesTotal"`
	Message        string `json:"message"`
	Error          string `json:"error,omitempty"`
	ReadyToRestart bool   `json:"readyToRestart"`
}

type updateDownloadState struct {
	mu       sync.Mutex
	phase    string
	percent  int
	received int64
	total    int64
	message  string
	errText  string
	path     string
	url      string
	cancel   context.CancelFunc
}

func (s *AppService) DownloadAndStageUpdate() (UpdateProgress, error) {
	info, err := s.CheckForUpdates()
	if err != nil {
		return UpdateProgress{}, err
	}
	if !info.UpdateAvailable {
		return UpdateProgress{}, errors.New("already up to date")
	}
	if !isTrustedUpdateURL(info.DownloadURL) {
		return UpdateProgress{}, errors.New("update download URL is not trusted")
	}
	if strings.Contains(strings.ToLower(info.DownloadURL), "github.com/"+GitHubRepo+"/releases") &&
		!strings.Contains(strings.ToLower(info.DownloadURL), "objects.githubusercontent.com") &&
		!strings.Contains(strings.ToLower(info.DownloadURL), "/download/") {
		return UpdateProgress{}, errors.New("no installable package found for this platform; open the release page instead")
	}

	s.updateState.mu.Lock()
	if s.updateState.cancel != nil {
		s.updateState.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.updateState.cancel = cancel
	s.updateState.phase = "downloading"
	s.updateState.percent = 0
	s.updateState.received = 0
	s.updateState.total = 0
	s.updateState.message = "Downloading update…"
	s.updateState.errText = ""
	s.updateState.path = ""
	s.updateState.url = info.DownloadURL
	s.updateState.mu.Unlock()
	s.emitUpdateProgress()

	go s.runUpdateDownload(ctx, info.DownloadURL, info.LatestVersion)
	return s.UpdateStatus(), nil
}

func (s *AppService) CancelUpdateDownload() error {
	s.updateState.mu.Lock()
	cancel := s.updateState.cancel
	s.updateState.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *AppService) UpdateStatus() UpdateProgress {
	s.updateState.mu.Lock()
	defer s.updateState.mu.Unlock()
	return UpdateProgress{
		Phase:          s.updateState.phase,
		Percent:        s.updateState.percent,
		BytesReceived:  s.updateState.received,
		BytesTotal:     s.updateState.total,
		Message:        s.updateState.message,
		Error:          s.updateState.errText,
		ReadyToRestart: s.updateState.phase == "ready" && s.updateState.path != "",
	}
}

func (s *AppService) ApplyUpdateAndRestart() error {
	s.updateState.mu.Lock()
	path := s.updateState.path
	phase := s.updateState.phase
	s.updateState.mu.Unlock()
	if phase != "ready" || strings.TrimSpace(path) == "" {
		return errors.New("update package is not ready")
	}
	if _, err := os.Stat(path); err != nil {
		return errors.New("downloaded update file is missing")
	}
	lower := strings.ToLower(path)
	if runtime.GOOS == "windows" && !strings.HasSuffix(lower, ".exe") {
		return errors.New("downloaded package is not a Windows executable")
	}
	if runtime.GOOS == "darwin" && (strings.HasSuffix(lower, ".dmg") || strings.HasSuffix(lower, ".pkg")) {
		_ = openPathInOS(path)
		return errors.New("macOS installer packages were opened for manual install; in-app replace supports portable binaries only")
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return err
	}
	if err := launchUpdateSwap(exePath, path, os.Getpid()); err != nil {
		return err
	}
	s.setUpdatePhase("restarting", 100, "Restarting…")
	s.emitUpdateProgress()
	go func() {
		time.Sleep(400 * time.Millisecond)
		if s.app != nil {
			s.app.Quit()
		}
	}()
	return nil
}

func (s *AppService) runUpdateDownload(ctx context.Context, downloadURL, version string) {
	path, err := s.downloadUpdateFile(ctx, downloadURL, version)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.setUpdatePhase("idle", 0, "Download cancelled")
		} else {
			s.setUpdateError(err.Error())
		}
		s.emitUpdateProgress()
		return
	}
	s.updateState.mu.Lock()
	s.updateState.path = path
	s.updateState.phase = "ready"
	s.updateState.percent = 100
	s.updateState.message = "Update ready. Restart to apply."
	s.updateState.errText = ""
	s.updateState.mu.Unlock()
	s.emitUpdateProgress()
}

func (s *AppService) downloadUpdateFile(ctx context.Context, downloadURL, version string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NiceCodex/"+AppVersion)
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("download failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	ext := ""
	if parsed, parseErr := url.Parse(resp.Request.URL.String()); parseErr == nil {
		ext = filepath.Ext(parsed.Path)
	}
	if ext == "" {
		if runtime.GOOS == "windows" {
			ext = ".exe"
		} else if runtime.GOOS == "darwin" {
			ext = ".dmg"
		} else {
			ext = ".bin"
		}
	}
	dir := filepath.Join(os.TempDir(), "NiceCodexUpdates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, fmt.Sprintf("NiceCodex-%s%s", sanitizeFileToken(version), ext))
	temp := target + ".partial"
	_ = os.Remove(temp)
	file, err := os.Create(temp)
	if err != nil {
		return "", err
	}
	defer file.Close()

	total := resp.ContentLength
	var received int64
	buf := make([]byte, 32*1024)
	lastEmit := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := file.Write(buf[:n]); writeErr != nil {
				_ = os.Remove(temp)
				return "", writeErr
			}
			received += int64(n)
			percent := 0
			if total > 0 {
				percent = int((received * 100) / total)
				if percent > 99 {
					percent = 99
				}
			}
			if time.Since(lastEmit) > 120*time.Millisecond || percent == 99 {
				s.updateState.mu.Lock()
				s.updateState.phase = "downloading"
				s.updateState.percent = percent
				s.updateState.received = received
				s.updateState.total = total
				s.updateState.message = "Downloading update…"
				s.updateState.mu.Unlock()
				s.emitUpdateProgress()
				lastEmit = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = os.Remove(temp)
			return "", readErr
		}
		if err := ctx.Err(); err != nil {
			_ = os.Remove(temp)
			return "", err
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	_ = os.Remove(target)
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	return target, nil
}

func (s *AppService) setUpdatePhase(phase string, percent int, message string) {
	s.updateState.mu.Lock()
	defer s.updateState.mu.Unlock()
	s.updateState.phase = phase
	s.updateState.percent = percent
	s.updateState.message = message
	s.updateState.errText = ""
}

func (s *AppService) setUpdateError(message string) {
	s.updateState.mu.Lock()
	defer s.updateState.mu.Unlock()
	s.updateState.phase = "error"
	s.updateState.message = "Update failed"
	s.updateState.errText = message
}

func (s *AppService) emitUpdateProgress() {
	if s.app == nil {
		return
	}
	s.app.Event.Emit("nice:update", s.UpdateStatus())
}

func isTrustedUpdateURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Host)
	path := strings.ToLower(parsed.Path)
	repo := strings.ToLower(GitHubRepo)
	switch host {
	case "github.com", "www.github.com":
		return strings.Contains(path, "/"+repo+"/")
	case "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	default:
		return strings.HasSuffix(host, ".githubusercontent.com")
	}
}

func sanitizeFileToken(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "..", ".")
	value = replacer.Replace(value)
	if value == "" {
		return "latest"
	}
	return value
}

func launchUpdateSwap(currentExe, newPackage string, pid int) error {
	switch runtime.GOOS {
	case "windows":
		return launchWindowsUpdateSwap(currentExe, newPackage, pid)
	default:
		return launchUnixUpdateSwap(currentExe, newPackage, pid)
	}
}

func launchWindowsUpdateSwap(currentExe, newPackage string, pid int) error {
	script := filepath.Join(os.TempDir(), fmt.Sprintf("nice-codex-update-%d.ps1", pid))
	content := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$pidToWait = %d
$target = %s
$source = %s
for ($i = 0; $i -lt 60; $i++) {
  if (-not (Get-Process -Id $pidToWait -ErrorAction SilentlyContinue)) { break }
  Start-Sleep -Milliseconds 500
}
Start-Sleep -Milliseconds 400
Copy-Item -LiteralPath $source -Destination $target -Force
Start-Process -FilePath $target
Remove-Item -LiteralPath $source -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $MyInvocation.MyCommand.Path -Force -ErrorAction SilentlyContinue
`, pid, powershellQuote(currentExe), powershellQuote(newPackage))
	if err := os.WriteFile(script, []byte(content), 0o600); err != nil {
		return err
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", script)
	configureBackgroundProcess(command)
	return command.Start()
}

func launchUnixUpdateSwap(currentExe, newPackage string, pid int) error {
	script := filepath.Join(os.TempDir(), fmt.Sprintf("nice-codex-update-%d.sh", pid))
	content := fmt.Sprintf(`#!/bin/sh
set -eu
pid="%d"
target=%s
source=%s
i=0
while kill -0 "$pid" 2>/dev/null; do
  i=$((i+1))
  if [ "$i" -gt 60 ]; then break; fi
  sleep 0.5
done
sleep 0.4
cp "$source" "$target"
chmod +x "$target" || true
nohup "$target" >/dev/null 2>&1 &
rm -f "$source" "$0"
`, pid, shellQuote(currentExe), shellQuote(newPackage))
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		return err
	}
	command := exec.Command("/bin/sh", script)
	configureBackgroundProcess(command)
	return command.Start()
}

func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
