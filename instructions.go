package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxInstructionFileBytes = 1 << 20

type InstructionsSaveRequest struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
}

type instructionSource struct {
	candidates  []string
	defaultPath string
	skipEmpty   bool
	mode        os.FileMode
}

type instructionSnapshot struct {
	info   GlobalInstructionsInfo
	target string
	mode   os.FileMode
}

func agentsInstructionSource(dir string, personal bool) instructionSource {
	mode := os.FileMode(0o644)
	if personal {
		mode = 0o600
	}
	return instructionSource{agentsDocCandidates(dir), filepath.Join(dir, "AGENTS.md"), true, mode}
}

func claudeInstructionSource(dir string, personal bool) instructionSource {
	mode := os.FileMode(0o644)
	if personal {
		mode = 0o600
	}
	return instructionSource{[]string{
		filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, "CLAUDE.local.md"),
	}, filepath.Join(dir, "CLAUDE.md"), false, mode}
}

func grokHomeInstructionSource(home string) instructionSource {
	return instructionSource{[]string{
		filepath.Join(home, "AGENTS.md"), filepath.Join(home, "AGENTS.override.md"),
		filepath.Join(home, "Agents.md"), filepath.Join(home, "AGENT.md"),
	}, filepath.Join(home, "AGENTS.md"), false, 0o600}
}

func externalInstructionSource(runtime, scope, workspace string) (instructionSource, error) {
	if normalizeExternalRuntime(runtime) == "" {
		return instructionSource{}, errors.New("unsupported external runtime")
	}
	if scope != "global" && scope != "project" {
		return instructionSource{}, errors.New("instruction scope must be global or project")
	}
	mode := os.FileMode(0o600)
	root := workspace
	if scope == "project" {
		var err error
		root, err = validateWorkspace(workspace)
		if err != nil {
			return instructionSource{}, err
		}
		mode = 0o644
	} else if runtime == "gemini" {
		root = resolveGeminiHome()
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return instructionSource{}, err
		}
		root = openCodeConfigDir(home)
	}
	if strings.TrimSpace(root) == "" {
		return instructionSource{}, errors.New("instruction directory unavailable")
	}
	var candidates []string
	if runtime == "gemini" && scope == "global" {
		candidates = []string{geminiGlobalInstructionPath(root)}
	} else if runtime == "gemini" {
		names := []string{"GEMINI.md", "AGENTS.md"}
		if geminiPrefersAntigravityInstructions() {
			names = []string{"AGENTS.md", "GEMINI.md"}
		}
		for _, name := range names {
			candidates = append(candidates, filepath.Join(root, name))
		}
	} else if scope == "project" {
		candidates = []string{filepath.Join(root, ".opencode", "AGENTS.md"), filepath.Join(root, "AGENTS.md")}
	} else {
		candidates = []string{filepath.Join(root, "AGENTS.md")}
	}
	return instructionSource{candidates, candidates[0], false, mode}, nil
}

func (source instructionSource) read() GlobalInstructionsInfo {
	providerFileMu.Lock()
	defer providerFileMu.Unlock()
	snapshot, err := source.readLocked()
	if err != nil {
		snapshot.info.Available = false
		snapshot.info.ReadError = err.Error()
	}
	return snapshot.info
}

func (source instructionSource) readLocked() (instructionSnapshot, error) {
	var firstEmpty *instructionSnapshot
	for _, path := range source.candidates {
		snapshot := instructionSnapshot{info: GlobalInstructionsInfo{Path: path, Source: filepath.Base(path)}, target: path, mode: source.mode}
		if err := recoverInstructionBackupLocked(path); err != nil {
			return snapshot, err
		}
		file, err := os.Open(path)
		if err != nil {
			// A broken symlink is an unreadable existing file, not a new file.
			if os.IsNotExist(err) {
				if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
					continue
				}
			}
			return snapshot, err
		}
		stat, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return snapshot, statErr
		}
		if !stat.Mode().IsRegular() {
			file.Close()
			return snapshot, errors.New("instruction path is not a regular file")
		}
		payload, readErr := io.ReadAll(io.LimitReader(file, maxInstructionFileBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return snapshot, readErr
		}
		if closeErr != nil {
			return snapshot, closeErr
		}
		if len(payload) > maxInstructionFileBytes {
			return snapshot, errors.New("INSTRUCTIONS_TOO_LARGE: use an external editor for files larger than 1 MiB")
		}
		if !utf8.Valid(payload) {
			return snapshot, errors.New("INSTRUCTIONS_ENCODING: save the instruction file as UTF-8 before editing")
		}
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return snapshot, err
		}
		snapshot.target, snapshot.mode = target, stat.Mode().Perm()
		snapshot.info.Content, snapshot.info.Exists, snapshot.info.Available = string(payload), true, true
		snapshot.info.EmptyFile = strings.TrimSpace(string(payload)) == ""
		snapshot.info.Revision = instructionRevision(snapshot)
		if source.skipEmpty && snapshot.info.EmptyFile {
			if firstEmpty == nil {
				firstEmpty = &snapshot
			}
			continue
		}
		return snapshot, nil
	}
	if firstEmpty != nil {
		return *firstEmpty, nil
	}
	snapshot := instructionSnapshot{info: GlobalInstructionsInfo{Path: source.defaultPath, Source: filepath.Base(source.defaultPath), Available: true}, target: source.defaultPath, mode: source.mode}
	snapshot.info.Revision = instructionRevision(snapshot)
	return snapshot, nil
}

func instructionRevision(snapshot instructionSnapshot) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\x00%s\x00%t\x00", filepath.Clean(snapshot.info.Path), filepath.Clean(snapshot.target), snapshot.info.Exists)
	_, _ = hash.Write([]byte(snapshot.info.Content))
	return hex.EncodeToString(hash.Sum(nil))
}

// A crash while replacing a symlink's target leaves the backup beside the
// target, not beside the symlink displayed in the editor.
func recoverInstructionBackupLocked(path string) error {
	for depth := 0; depth < 40; depth++ {
		stat, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return recoverProviderFileBackupLocked(path)
		}
		if err != nil {
			return err
		}
		if stat.Mode()&os.ModeSymlink == 0 {
			return recoverProviderFileBackupLocked(path)
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = filepath.Clean(target)
	}
	return errors.New("instruction symlink chain is too deep")
}

func (source instructionSource) save(request InstructionsSaveRequest) (GlobalInstructionsInfo, error) {
	if len(request.Content) > maxInstructionFileBytes {
		return GlobalInstructionsInfo{}, errors.New("INSTRUCTIONS_TOO_LARGE: instruction content exceeds 1 MiB")
	}
	if !utf8.ValidString(request.Content) {
		return GlobalInstructionsInfo{}, errors.New("INSTRUCTIONS_ENCODING: instruction content must be UTF-8")
	}
	providerFileMu.Lock()
	defer providerFileMu.Unlock()
	current, err := source.readLocked()
	if err != nil {
		return GlobalInstructionsInfo{}, err
	}
	if request.Revision == "" || current.info.Revision != request.Revision {
		return GlobalInstructionsInfo{}, errors.New("INSTRUCTIONS_CHANGED: the instruction file or workspace changed; keep your draft and reload before saving")
	}
	check := func() error {
		latest, err := source.readLocked()
		if err != nil {
			return err
		}
		if request.Revision == "" || latest.info.Revision != request.Revision {
			return errors.New("INSTRUCTIONS_CHANGED: the instruction file or workspace changed; keep your draft and reload before saving")
		}
		return nil
	}
	// Only replace the displayed file. Clearing it must never delete another
	// instruction file that may become active through the CLI's fallback rules.
	if err := writeProviderFileAtomicLocked(current.target, []byte(request.Content), current.mode, check); err != nil {
		return GlobalInstructionsInfo{}, err
	}
	// Return the exact committed content and revision, not a later external edit
	// or a different fallback file. A later save will re-check source selection.
	current.info.Content, current.info.Exists, current.info.EmptyFile = request.Content, true, strings.TrimSpace(request.Content) == ""
	current.target, err = filepath.EvalSymlinks(current.info.Path)
	if err != nil {
		return GlobalInstructionsInfo{}, err
	}
	current.info.Revision = instructionRevision(current)
	return current.info, nil
}

func projectInstructionInfo(info GlobalInstructionsInfo, workspace string) ProjectInstructionsInfo {
	return ProjectInstructionsInfo{Content: info.Content, Path: info.Path, Source: info.Source, Exists: info.Exists, EmptyFile: info.EmptyFile,
		Available: info.Available, Revision: info.Revision, ReadError: info.ReadError, Workspace: workspace, WorkspaceName: filepath.Base(workspace)}
}
