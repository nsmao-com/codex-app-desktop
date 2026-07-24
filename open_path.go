package main

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func openPathInOS(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if info.IsDir() {
			command = exec.Command("explorer.exe", path)
		} else {
			// explorer.exe selects files; start opens with the default app.
			command = exec.Command("cmd", "/c", "start", "", path)
		}
	case "darwin":
		command = exec.Command("open", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	configureBackgroundProcess(command)
	return command.Start()
}

// OpenLocalPath opens a workspace-local file or folder with the OS default handler.
// Accepts absolute paths, workspace-relative paths, and file:// URLs.
func (s *AppService) OpenLocalPath(rawPath string) error {
	path, err := normalizeLocalOpenPath(rawPath)
	if err != nil {
		return err
	}
	workspace := strings.TrimSpace(s.Settings().Workspace)
	if workspace == "" {
		return errors.New("choose a workspace before opening local files")
	}
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	workspaceAbs = filepath.Clean(workspaceAbs)

	absolute, err := resolveWorkspaceOpenPath(workspaceAbs, path)
	if err != nil {
		return err
	}
	return openPathInOS(absolute)
}

func resolveWorkspaceOpenPath(workspaceAbs, path string) (string, error) {
	var absolute string
	if filepath.IsAbs(path) {
		absolute = filepath.Clean(path)
	} else {
		absolute = filepath.Clean(filepath.Join(workspaceAbs, path))
	}

	if pathInsideRoot(workspaceAbs, absolute) {
		if _, err := os.Stat(absolute); err == nil {
			return absolute, nil
		}
	}

	// Fallback: search by basename inside the workspace (sandbox:/mnt/data/x.docx, etc.).
	base := filepath.Base(strings.ReplaceAll(path, `\`, `/`))
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", errors.New("file not found in the workspace")
	}
	matches := findWorkspaceFilesByName(workspaceAbs, base, 8)
	if len(matches) == 0 {
		return "", errors.New("file not found in the workspace")
	}
	// Prefer exact basename match nearest the workspace root.
	return matches[0], nil
}

func findWorkspaceFilesByName(root, name string, limit int) []string {
	want := name
	if runtime.GOOS == "windows" {
		want = strings.ToLower(name)
	}
	var matches []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				base := d.Name()
				if base == ".git" || base == "node_modules" || base == ".nice-codex" || base == "dist" || base == "build" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		base := d.Name()
		if runtime.GOOS == "windows" {
			if strings.ToLower(base) != want {
				return nil
			}
		} else if base != want {
			return nil
		}
		if !pathInsideRoot(root, path) {
			return nil
		}
		matches = append(matches, path)
		if len(matches) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	return matches
}

func normalizeLocalOpenPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is required")
	}
	if decoded, err := url.PathUnescape(raw); err == nil && decoded != "" {
		raw = decoded
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "sandbox:") {
		raw = strings.TrimSpace(raw[len("sandbox:"):])
		raw = strings.TrimPrefix(raw, "/mnt/data/")
		raw = strings.TrimPrefix(raw, "mnt/data/")
		return strings.TrimSpace(raw), nil
	}
	if strings.HasPrefix(lower, "file:") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", errors.New("invalid file URL")
		}
		path := parsed.Path
		if parsed.Opaque != "" && path == "" {
			path = parsed.Opaque
		}
		if runtime.GOOS == "windows" {
			// file:///C:/Users/... → /C:/Users/... → C:/Users/...
			if strings.HasPrefix(path, "/") && len(path) >= 3 && path[2] == ':' {
				path = path[1:]
			}
			// file://localhost/C:/...
			if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
				path = `\\` + parsed.Host + strings.ReplaceAll(path, "/", `\`)
			} else {
				path = strings.ReplaceAll(path, "/", `\`)
			}
		}
		return strings.TrimSpace(path), nil
	}
	return raw, nil
}

func pathInsideRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
