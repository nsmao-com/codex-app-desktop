package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ClaudeRuntimeStatus is the live probe for Claude Code CLI.
type ClaudeRuntimeStatus struct {
	Available     bool   `json:"available"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version"`
	Executable    string `json:"executable"`
	Message       string `json:"message"`
}

// ClaudeSessionSummary is a sidebar row for Claude history.
type ClaudeSessionSummary struct {
	ID           string `json:"id"`
	Workspace    string `json:"workspace"`
	Name         string `json:"name"`
	Preview      string `json:"preview"`
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
	ActiveTurnID string `json:"activeTurnId,omitempty"`
}

// ClaudeMessage is one timeline row stored with a session.
type ClaudeMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	ToolName  string `json:"toolName,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// ClaudeSessionDetail is one page of an open conversation.
type ClaudeSessionDetail struct {
	Summary           ClaudeSessionSummary `json:"summary"`
	Messages          []ClaudeMessage      `json:"messages"`
	HistoryStart      int                  `json:"historyStart"`
	HistoryTotal      int                  `json:"historyTotal"`
	HistoryTurnOffset int                  `json:"historyTurnOffset"`
	HasEarlier        bool                 `json:"hasEarlier"`
	ActiveTurnID      string               `json:"activeTurnId,omitempty"`
}

// ClaudeSendRequest starts a Claude Code turn.
type ClaudeSendRequest struct {
	SessionID string   `json:"sessionId"`
	Workspace string   `json:"workspace"`
	Text      string   `json:"text"`
	Images    []string `json:"images"`
	Model     string   `json:"model"`
	Effort    string   `json:"effort"`
}

// ClaudeTurnRef identifies a running turn.
type ClaudeTurnRef struct {
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
}

type claudeStoredSession struct {
	ID         string          `json:"id"`
	BackendRef string          `json:"backendRef"`
	Workspace  string          `json:"workspace"`
	Name       string          `json:"name"`
	Preview    string          `json:"preview"`
	Model      string          `json:"model"`
	Effort     string          `json:"effort"`
	CreatedAt  int64           `json:"createdAt"`
	UpdatedAt  int64           `json:"updatedAt"`
	Archived   bool            `json:"archived"`
	Messages   []ClaudeMessage `json:"messages"`
}

func claudeSessionsPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "claude-sessions.json")
}

func loadClaudeSessions(settingsPath string) map[string]*claudeStoredSession {
	result := make(map[string]*claudeStoredSession)
	payload, err := os.ReadFile(claudeSessionsPath(settingsPath))
	if err != nil {
		return result
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return make(map[string]*claudeStoredSession)
	}
	if result == nil {
		return make(map[string]*claudeStoredSession)
	}
	return result
}

func (s *AppService) persistClaudeSessionsLocked() {
	path := claudeSessionsPath(s.settingsPath)
	payload, err := json.MarshalIndent(s.claudeSessions, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, payload, 0o600)
}

func resolveClaudeHome() string {
	if value := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); value != "" {
		return filepath.Clean(value)
	}
	if value := strings.TrimSpace(os.Getenv("CLAUDE_HOME")); value != "" {
		return filepath.Clean(value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func detectClaudeRuntime() ClaudeRuntimeStatus {
	codexEnrich := func() { /* path enrichment via findCommand */ }
	_ = codexEnrich
	status := ClaudeRuntimeStatus{}
	executable := findCommand(commandCandidates("claude"))
	if executable == "" {
		status.Message = "Install Claude Code CLI (claude) to use this runtime"
		return status
	}
	status.Available = true
	status.Executable = executable
	if out, err := runProbeCommand(executable, []string{"--version"}, 4*time.Second); err == nil {
		status.Version = firstOutputLine(out)
	} else if out != "" {
		status.Version = firstOutputLine(out)
	}
	// auth status --json when available
	if out, err := runProbeCommand(executable, []string{"auth", "status", "--json"}, 5*time.Second); err == nil {
		compact := strings.ReplaceAll(out, " ", "")
		if strings.Contains(compact, `"loggedIn":true`) || strings.Contains(compact, `"authenticated":true`) {
			status.Authenticated = true
			status.Message = "Claude Code ready"
		} else {
			status.Message = "Claude Code installed (sign-in may be required)"
		}
	} else {
		status.Authenticated = true // version works; treat as usable
		status.Message = "Claude Code installed"
	}
	return status
}

func (s *AppService) RefreshClaudeRuntime() ClaudeRuntimeStatus {
	status := detectClaudeRuntime()
	s.mu.Lock()
	if len(s.agentProviders) > 0 {
		next := make([]AgentProviderRuntime, len(s.agentProviders))
		copy(next, s.agentProviders)
		for i := range next {
			if next[i].Kind != "claude" {
				continue
			}
			next[i].Installed = status.Available
			next[i].Healthy = status.Available
			next[i].RuntimeReady = status.Available
			next[i].Version = status.Version
			next[i].Executable = status.Executable
			next[i].Status = providerStatus(status.Available, status.Available, true)
			next[i].Message = status.Message
		}
		s.agentProviders = next
	}
	s.mu.Unlock()
	return status
}

func (s *AppService) SelectClaudeWorkspace() (WorkspaceInfo, error) {
	current := s.Settings().ClaudeWorkspace
	path, err := s.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                "Choose a Claude workspace",
		Message:              "Select the project folder Claude Code can work in.",
		ButtonText:           "Use this folder",
		Directory:            current,
		CanChooseDirectories: true,
		CanChooseFiles:       false,
		CanCreateDirectories: true,
	}).PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return WorkspaceInfo{}, nil
		}
		return WorkspaceInfo{}, err
	}
	if strings.TrimSpace(path) == "" {
		return WorkspaceInfo{}, nil
	}
	return s.UseClaudeWorkspace(path)
}

func (s *AppService) UseClaudeWorkspace(path string) (WorkspaceInfo, error) {
	cleanPath, err := validateWorkspace(path)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	s.mu.Lock()
	settings := cloneSettings(s.settings)
	settings.ClaudeWorkspace = cleanPath
	settings.ClaudeRecentWorkspaces = rememberWorkspace(settings.ClaudeRecentWorkspaces, cleanPath)
	err = writeSettings(s.settingsPath, settings)
	if err == nil {
		s.settings = settings
	}
	s.mu.Unlock()
	if err != nil {
		return WorkspaceInfo{}, err
	}
	return inspectWorkspace(cleanPath), nil
}

func (s *AppService) ListClaudeSessions(workspace, search string) ([]ClaudeSessionSummary, error) {
	cleanWorkspace := ""
	if strings.TrimSpace(workspace) != "" {
		if clean, err := validateWorkspace(workspace); err == nil {
			cleanWorkspace = clean
		}
	}
	query := strings.ToLower(strings.TrimSpace(search))

	// Merge NiceCodex-owned rows with official Claude Code transcripts under ~/.claude/projects.
	s.mu.Lock()
	local := make(map[string]*claudeStoredSession, len(s.claudeSessions))
	localByBackendRef := make(map[string]*claudeStoredSession, len(s.claudeSessions))
	localIsRunning := make(map[string]bool, len(s.claudeSessions))
	for id, session := range s.claudeSessions {
		if session != nil {
			local[id] = session
			localIsRunning[id] = s.externalRuns[claudeRunKey(id)] != nil
			if backendRef := strings.TrimSpace(session.BackendRef); backendRef != "" {
				owner := localByBackendRef[backendRef]
				candidateRunning := localIsRunning[session.ID]
				ownerRunning := owner != nil && localIsRunning[owner.ID]
				candidateStable := session.ID != backendRef
				ownerStable := owner != nil && owner.ID != backendRef
				if owner == nil ||
					(candidateRunning != ownerRunning && candidateRunning) ||
					(candidateRunning == ownerRunning && candidateStable != ownerStable && candidateStable) ||
					(candidateRunning == ownerRunning && candidateStable == ownerStable && session.UpdatedAt > owner.UpdatedAt) ||
					(candidateRunning == ownerRunning && candidateStable == ownerStable && session.UpdatedAt == owner.UpdatedAt && session.CreatedAt > owner.CreatedAt) ||
					(candidateRunning == ownerRunning && candidateStable == ownerStable && session.UpdatedAt == owner.UpdatedAt && session.CreatedAt == owner.CreatedAt && session.ID < owner.ID) {
					localByBackendRef[backendRef] = session
				}
			}
		}
	}
	s.mu.Unlock()

	seen := make(map[string]struct{}, len(local)+32)
	result := make([]ClaudeSessionSummary, 0, len(local)+32)

	// 1) Local (non-archived) sessions first — may include renames / NiceCodex-only turns.
	for _, session := range local {
		if session.Archived {
			continue
		}
		// Keep historical duplicate rows on disk, but expose only one stable owner
		// for a native transcript so the sidebar never shows the same chat twice.
		if backendRef := strings.TrimSpace(session.BackendRef); backendRef != "" {
			if owner := localByBackendRef[backendRef]; owner != nil && owner.ID != session.ID {
				continue
			}
		}
		haystack := strings.ToLower(session.Name + "\n" + session.Preview + "\n" + session.Workspace)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		seen[session.ID] = struct{}{}
		result = append(result, ClaudeSessionSummary{
			ID: session.ID, Workspace: session.Workspace, Name: session.Name, Preview: session.Preview,
			Model: session.Model, Effort: session.Effort, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
		})
	}

	// 2) Native Claude Code transcripts. A local row may be an older index entry
	// for the same UUID, so merge the fresh transcript summary into that row
	// instead of letting the stale index hide it.
	for _, native := range scanClaudeNativeSessions(cleanWorkspace) {
		localSession := localByBackendRef[native.Summary.ID]
		if localSession == nil {
			localSession = local[native.Summary.ID]
		}
		if localSession != nil && localSession.Archived {
			continue
		}
		summary := native.Summary
		if localSession != nil {
			// Keep explicit NiceCodex metadata while refreshing transcript-derived
			// workspace/title/model/timestamps from the native file.
			if localSession.Name != "" && localSession.Name != "Claude session" {
				summary.Name = localSession.Name
			}
			if localSession.Workspace != "" {
				summary.Workspace = localSession.Workspace
			}
			if localSession.Model != "" {
				summary.Model = localSession.Model
			}
			if localSession.UpdatedAt > summary.UpdatedAt {
				summary.UpdatedAt = localSession.UpdatedAt
			}
			// Keep the NiceCodex id stable; BackendRef owns the native transcript id.
			summary.ID = localSession.ID
		}
		haystack := strings.ToLower(summary.Name + "\n" + summary.Preview + "\n" + summary.Workspace)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		// Prefer active workspace grouping when cwd matches.
		if cleanWorkspace != "" && native.Summary.Workspace != "" && !samePath(native.Summary.Workspace, cleanWorkspace) {
			// Still include other projects (Grok-style multi-project sidebar).
		}
		if _, exists := seen[summary.ID]; exists {
			for index := range result {
				if result[index].ID == summary.ID {
					result[index] = summary
					break
				}
			}
			continue
		}
		seen[summary.ID] = struct{}{}
		result = append(result, summary)
	}
	for index := range result {
		result[index].ActiveTurnID = s.claudeActiveTurnID(result[index].ID)
	}

	sort.SliceStable(result, func(i, j int) bool {
		iActive := cleanWorkspace != "" && samePath(result[i].Workspace, cleanWorkspace)
		jActive := cleanWorkspace != "" && samePath(result[j].Workspace, cleanWorkspace)
		if iActive != jActive {
			return iActive
		}
		return result[i].UpdatedAt > result[j].UpdatedAt
	})
	return result, nil
}

func (s *AppService) ListArchivedClaudeSessions() ([]ClaudeSessionSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]ClaudeSessionSummary, 0)
	for _, session := range s.claudeSessions {
		if session == nil || !session.Archived {
			continue
		}
		result = append(result, ClaudeSessionSummary{
			ID: session.ID, Workspace: session.Workspace, Name: session.Name, Preview: session.Preview,
			Model: session.Model, Effort: session.Effort, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
		})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	return result, nil
}

func (s *AppService) ReadClaudeSession(sessionID string) (ClaudeSessionDetail, error) {
	return s.readClaudeSessionPage(sessionID, -1)
}

func (s *AppService) ReadClaudeSessionHistory(sessionID string, before int) (ClaudeSessionDetail, error) {
	return s.readClaudeSessionPage(sessionID, before)
}

func (s *AppService) readClaudeSessionPage(sessionID string, before int) (ClaudeSessionDetail, error) {
	detail, err := s.readClaudeSessionFull(sessionID)
	if err != nil {
		return ClaudeSessionDetail{}, err
	}
	detail.ActiveTurnID = s.claudeActiveTurnID(sessionID)
	detail.Summary.ActiveTurnID = detail.ActiveTurnID
	return paginateClaudeSession(detail, before), nil
}

func (s *AppService) readClaudeSessionFull(sessionID string) (ClaudeSessionDetail, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ClaudeSessionDetail{}, errors.New("Claude session id is required")
	}

	// Native Claude Code transcripts are the source of truth for message history.
	// The local index only carries NiceCodex metadata and is a fallback for
	// sessions that do not have a readable native transcript yet.
	s.mu.Lock()
	session := s.claudeSessions[sessionID]
	var localMeta *claudeStoredSession
	if session != nil {
		clone := *session
		clone.Messages = nil
		localMeta = &clone
	}
	s.mu.Unlock()
	nativeSessionID := sessionID
	if localMeta != nil {
		if backendRef := strings.TrimSpace(localMeta.BackendRef); backendRef != "" {
			nativeSessionID = backendRef
		}
	}

	if summary, messages, ok := s.cachedClaudeHistory(sessionID); ok {
		summary = mergeClaudeLocalSummary(summary, localMeta)
		if localMeta != nil {
			summary.ID = localMeta.ID
		}
		return ClaudeSessionDetail{Summary: summary, Messages: messages}, nil
	}

	if native, ok := findClaudeNativeSession(nativeSessionID); ok {
		before, _ := os.Stat(native.Path)
		messages, err := readClaudeNativeMessages(native.Path)
		if err != nil {
			localCopy := s.copyClaudeStoredSession(sessionID)
			if localCopy == nil || len(localCopy.Messages) == 0 {
				return ClaudeSessionDetail{}, err
			}
			// A file can be briefly locked while Claude appends the final line.
			// Keep the last local snapshot as a temporary fallback rather than
			// turning a transient read into an empty history.
			return ClaudeSessionDetail{
				Summary: ClaudeSessionSummary{
					ID: localCopy.ID, Workspace: localCopy.Workspace, Name: localCopy.Name, Preview: localCopy.Preview,
					Model: localCopy.Model, Effort: localCopy.Effort, CreatedAt: localCopy.CreatedAt, UpdatedAt: localCopy.UpdatedAt,
				},
				Messages: localCopy.Messages,
			}, nil
		}
		summary := mergeClaudeLocalSummary(native.Summary, localMeta)
		if localMeta != nil {
			summary.ID = localMeta.ID
		}
		s.cacheClaudeHistory(sessionID, native.Path, summary, messages, before)
		// The native transcript and history cache keep the full conversation. The
		// persisted index only needs a bounded fallback for brief file locks.
		fallbackMessages := paginateClaudeSession(ClaudeSessionDetail{Messages: messages}, -1).Messages
		s.mu.Lock()
		if s.claudeSessions[sessionID] == nil {
			s.claudeSessions[sessionID] = &claudeStoredSession{
				ID: summary.ID, BackendRef: native.Summary.ID, Workspace: summary.Workspace,
				Name: summary.Name, Preview: summary.Preview, Model: summary.Model,
				CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt,
				Messages: fallbackMessages,
			}
			s.persistClaudeSessionsLocked()
		} else {
			stored := s.claudeSessions[sessionID]
			stored.ID = summary.ID
			stored.BackendRef = native.Summary.ID
			stored.Workspace = summary.Workspace
			stored.Name = summary.Name
			stored.Preview = summary.Preview
			stored.Model = summary.Model
			stored.CreatedAt = summary.CreatedAt
			if stored.UpdatedAt < summary.UpdatedAt {
				stored.UpdatedAt = summary.UpdatedAt
			}
			stored.Messages = fallbackMessages
			s.persistClaudeSessionsLocked()
		}
		s.mu.Unlock()
		return ClaudeSessionDetail{Summary: summary, Messages: messages}, nil
	}

	localCopy := s.copyClaudeStoredSession(sessionID)
	if localCopy != nil {
		return ClaudeSessionDetail{
			Summary: ClaudeSessionSummary{
				ID: localCopy.ID, Workspace: localCopy.Workspace, Name: localCopy.Name, Preview: localCopy.Preview,
				Model: localCopy.Model, Effort: localCopy.Effort, CreatedAt: localCopy.CreatedAt, UpdatedAt: localCopy.UpdatedAt,
			},
			Messages: localCopy.Messages,
		}, nil
	}
	return ClaudeSessionDetail{}, errors.New("Claude session was not found")
}

func mergeClaudeLocalSummary(summary ClaudeSessionSummary, local *claudeStoredSession) ClaudeSessionSummary {
	if local == nil {
		return summary
	}
	if local.Name != "" && local.Name != "Claude session" {
		summary.Name = local.Name
	}
	if local.Workspace != "" {
		summary.Workspace = local.Workspace
	}
	if local.Model != "" {
		summary.Model = local.Model
	}
	if local.Effort != "" {
		summary.Effort = local.Effort
	}
	if local.UpdatedAt > summary.UpdatedAt {
		summary.UpdatedAt = local.UpdatedAt
	}
	return summary
}

func (s *AppService) copyClaudeStoredSession(sessionID string) *claudeStoredSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.claudeSessions[sessionID]
	if session == nil {
		return nil
	}
	clone := *session
	clone.Messages = append([]ClaudeMessage(nil), session.Messages...)
	return &clone
}

func paginateClaudeSession(detail ClaudeSessionDetail, before int) ClaudeSessionDetail {
	messages := detail.Messages
	start, end, turnOffset := conversationMessagePageBounds(len(messages), before, func(index int) bool {
		role := strings.ToLower(strings.TrimSpace(messages[index].Role))
		return role == "user" || role == "human"
	})
	detail.Messages = append([]ClaudeMessage(nil), messages[start:end]...)
	detail.HistoryStart = start
	detail.HistoryTotal = len(messages)
	detail.HistoryTurnOffset = turnOffset
	detail.HasEarlier = start > 0
	return detail
}

func (s *AppService) cachedClaudeHistory(sessionID string) (ClaudeSessionSummary, []ClaudeMessage, bool) {
	s.historyMu.Lock()
	snapshot := s.claudeHistoryCache[sessionID]
	s.historyMu.Unlock()
	if snapshot == nil {
		return ClaudeSessionSummary{}, nil, false
	}
	info, err := os.Stat(snapshot.path)
	if err != nil || info.Size() != snapshot.size || info.ModTime().UnixNano() != snapshot.modified {
		s.historyMu.Lock()
		if s.claudeHistoryCache[sessionID] == snapshot {
			delete(s.claudeHistoryCache, sessionID)
		}
		s.historyMu.Unlock()
		return ClaudeSessionSummary{}, nil, false
	}
	s.historyMu.Lock()
	if current := s.claudeHistoryCache[sessionID]; current == snapshot {
		current.touchedAt = time.Now()
	}
	s.historyMu.Unlock()
	return snapshot.summary, snapshot.messages, true
}

func (s *AppService) cacheClaudeHistory(
	sessionID, path string,
	summary ClaudeSessionSummary,
	messages []ClaudeMessage,
	before os.FileInfo,
) {
	info, err := os.Stat(path)
	if err != nil || before == nil || info.Size() != before.Size() || info.ModTime() != before.ModTime() {
		return
	}
	weight := info.Size() * 2
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.claudeHistoryCache == nil {
		s.claudeHistoryCache = make(map[string]*claudeHistorySnapshot)
	}
	if weight > conversationHistoryEntryBytes {
		delete(s.claudeHistoryCache, sessionID)
		return
	}
	s.claudeHistoryCache[sessionID] = &claudeHistorySnapshot{
		path: path, size: info.Size(), modified: info.ModTime().UnixNano(),
		summary: summary, messages: append([]ClaudeMessage(nil), messages...), weight: weight, touchedAt: time.Now(),
	}
	s.pruneConversationHistoryCachesLocked()
}

func (s *AppService) RenameClaudeSession(sessionID, name string) error {
	sessionID = strings.TrimSpace(sessionID)
	name = strings.TrimSpace(name)
	if sessionID == "" || name == "" {
		return errors.New("session id and name are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.claudeSessions[sessionID]
	if session == nil {
		// Allow renaming a native-only Claude Code transcript via local override.
		session = s.ensureClaudeIndexFromNativeLocked(sessionID)
		if session == nil {
			return errors.New("Claude session was not found")
		}
	}
	session.Name = truncateRunes(name, 80)
	session.UpdatedAt = time.Now().Unix()
	s.persistClaudeSessionsLocked()
	return nil
}

func (s *AppService) ArchiveClaudeSession(sessionID string) error {
	return s.setClaudeSessionArchived(sessionID, true)
}

func (s *AppService) UnarchiveClaudeSession(sessionID string) error {
	return s.setClaudeSessionArchived(sessionID, false)
}

func (s *AppService) setClaudeSessionArchived(sessionID string, archived bool) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("Claude session id is required")
	}
	if archived && s.isClaudeSessionRunning(sessionID) {
		return errors.New("stop the running Claude turn before archiving its session")
	}
	s.mu.Lock()
	session := s.claudeSessions[sessionID]
	if session == nil {
		session = s.ensureClaudeIndexFromNativeLocked(sessionID)
		if session == nil {
			s.mu.Unlock()
			return errors.New("Claude session was not found")
		}
	}
	session.Archived = archived
	session.UpdatedAt = time.Now().Unix()
	s.persistClaudeSessionsLocked()
	s.mu.Unlock()
	if archived {
		s.dropClaudeHistoryCache(sessionID)
	}
	return nil
}

// ensureClaudeIndexFromNativeLocked creates a local index row for a native transcript.
// Caller must hold s.mu.
func (s *AppService) ensureClaudeIndexFromNativeLocked(sessionID string) *claudeStoredSession {
	if existing := s.claudeSessions[sessionID]; existing != nil {
		return existing
	}
	native, ok := findClaudeNativeSession(sessionID)
	if !ok {
		return nil
	}
	session := &claudeStoredSession{
		ID: native.Summary.ID, BackendRef: native.Summary.ID, Workspace: native.Summary.Workspace,
		Name: native.Summary.Name, Preview: native.Summary.Preview, Model: native.Summary.Model,
		CreatedAt: native.Summary.CreatedAt, UpdatedAt: native.Summary.UpdatedAt,
		Messages: make([]ClaudeMessage, 0),
	}
	s.claudeSessions[sessionID] = session
	return session
}

func (s *AppService) DeleteClaudeSession(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("Claude session id is required")
	}
	if strings.HasPrefix(sessionID, "pending-claude-") {
		return nil
	}
	if s.isClaudeSessionRunning(sessionID) {
		return errors.New("stop the running Claude turn before deleting its session")
	}
	s.mu.Lock()
	// Soft-delete: archive + clear messages so native transcripts stay hidden in NiceCodex
	// (we do not delete ~/.claude/projects files).
	session := s.claudeSessions[sessionID]
	if session == nil {
		session = s.ensureClaudeIndexFromNativeLocked(sessionID)
	}
	if session == nil {
		s.mu.Unlock()
		return errors.New("Claude session was not found")
	}
	session.Archived = true
	session.Messages = nil
	session.Preview = ""
	session.UpdatedAt = time.Now().Unix()
	s.persistClaudeSessionsLocked()
	s.mu.Unlock()
	s.dropClaudeHistoryCache(sessionID)
	return nil
}

func (s *AppService) validateClaudeSendWorkspace(sessionID, requestedWorkspace string) error {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	local := s.claudeSessions[sessionID]
	ownedWorkspace := ""
	backendRef := ""
	if local != nil {
		ownedWorkspace = strings.TrimSpace(local.Workspace)
		backendRef = strings.TrimSpace(local.BackendRef)
	}
	s.mu.Unlock()

	if local == nil {
		native, ok := findClaudeNativeSession(sessionID)
		if !ok {
			return errors.New("Claude session was not found")
		}
		ownedWorkspace = strings.TrimSpace(native.Summary.Workspace)
	} else if ownedWorkspace == "" && backendRef != "" {
		if native, ok := findClaudeNativeSession(backendRef); ok {
			ownedWorkspace = strings.TrimSpace(native.Summary.Workspace)
		}
	}
	if ownedWorkspace == "" || !samePath(ownedWorkspace, requestedWorkspace) {
		return errors.New("Claude session belongs to a different workspace")
	}
	return nil
}

func (s *AppService) SendClaudeMessage(request ClaudeSendRequest) (ClaudeTurnRef, error) {
	request.SessionID = strings.TrimSpace(request.SessionID)
	clientSessionID := request.SessionID
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" && len(request.Images) == 0 {
		return ClaudeTurnRef{}, errors.New("message is required")
	}
	workspace, err := validateWorkspace(request.Workspace)
	if err != nil {
		return ClaudeTurnRef{}, err
	}
	if request.SessionID == "" {
		request.SessionID = newUUID()
	} else if strings.HasPrefix(request.SessionID, "pending-claude-") {
		request.SessionID = stablePendingSessionID("claude", request.SessionID, workspace)
	} else if err := s.validateClaudeSendWorkspace(request.SessionID, workspace); err != nil {
		return ClaudeTurnRef{}, err
	}
	request.Workspace = workspace
	turnID := "claude-turn-" + newUUID()
	ctx, cancel := context.WithCancel(context.Background())
	key := claudeRunKey(request.SessionID)
	s.mu.Lock()
	if s.externalRuns == nil {
		s.externalRuns = make(map[string]*externalRun)
	}
	// If a turn is already running for this session, do not spawn a second CLI.
	// Frontend should queue; cancel+replace is reserved for explicit interrupt paths.
	if previous := s.externalRuns[key]; previous != nil {
		s.mu.Unlock()
		cancel()
		return ClaudeTurnRef{}, errors.New("Claude turn already running for this session")
	}
	s.externalRuns[key] = &externalRun{turnID: turnID, cancel: cancel}
	s.mu.Unlock()
	startedPayload := map[string]any{"text": request.Text}
	if clientSessionID != "" && clientSessionID != request.SessionID {
		startedPayload["clientSessionId"] = clientSessionID
	}
	s.emitClaudeEvent("turn.started", request.SessionID, turnID, startedPayload)
	go s.runClaudeTurn(ctx, cancel, turnID, request)
	return ClaudeTurnRef{SessionID: request.SessionID, TurnID: turnID}, nil
}

func (s *AppService) InterruptClaudeTurn(ref ClaudeTurnRef) error {
	key := claudeRunKey(strings.TrimSpace(ref.SessionID))
	s.mu.Lock()
	run := s.externalRuns[key]
	s.mu.Unlock()
	if run == nil || (strings.TrimSpace(ref.TurnID) != "" && run.turnID != ref.TurnID) {
		return errors.New("Claude turn is not running")
	}
	run.cancel()
	return nil
}

func (s *AppService) IsClaudeTurnRunning(ref ClaudeTurnRef) bool {
	sessionID := strings.TrimSpace(ref.SessionID)
	turnID := strings.TrimSpace(ref.TurnID)
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.externalRuns[claudeRunKey(sessionID)]
	return run != nil && (turnID == "" || run.turnID == turnID)
}

func (s *AppService) isClaudeSessionRunning(sessionID string) bool {
	return s.claudeActiveTurnID(sessionID) != ""
}

func (s *AppService) claudeActiveTurnID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.externalRuns[claudeRunKey(sessionID)]; run != nil {
		return strings.TrimSpace(run.turnID)
	}
	return ""
}

func claudeRunKey(sessionID string) string {
	return "claude:" + strings.TrimSpace(sessionID)
}

func (s *AppService) clearClaudeRun(sessionID, turnID string) {
	key := claudeRunKey(sessionID)
	s.mu.Lock()
	if run := s.externalRuns[key]; run != nil && run.turnID == turnID {
		delete(s.externalRuns, key)
	}
	s.mu.Unlock()
}

func (s *AppService) emitClaudeEvent(kind, sessionID, turnID string, data map[string]any) {
	payload := map[string]any{
		"kind":      kind,
		"sessionId": sessionID,
		"turnId":    turnID,
	}
	for key, value := range data {
		payload[key] = value
	}
	s.app.Event.Emit("claude:event", payload)
}

func (s *AppService) runClaudeTurn(ctx context.Context, cancel context.CancelFunc, turnID string, request ClaudeSendRequest) {
	defer cancel()
	defer s.clearClaudeRun(request.SessionID, turnID)

	settings := s.Settings()
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = strings.TrimSpace(settings.ClaudeModel)
	}
	effort := strings.TrimSpace(request.Effort)
	if effort == "" {
		effort = strings.TrimSpace(settings.ClaudeEffort)
	}
	if effort == "" {
		effort = "high"
	}
	// Build settings for external runner permission flags.
	turnSettings := settings
	turnSettings.Model = model
	turnSettings.Effort = effort
	if settings.ClaudeSandbox != "" {
		turnSettings.Sandbox = settings.ClaudeSandbox
	}
	if settings.ClaudeApprovalPolicy != "" {
		turnSettings.ApprovalPolicy = settings.ClaudeApprovalPolicy
	}
	// ClaudePermissionMode is already on UserSettings and read by claudePermissionArgs.

	now := time.Now().Unix()
	userMsg := ClaudeMessage{
		ID: turnID + ":user", Role: "user", Text: request.Text, Status: "completed", CreatedAt: now,
	}
	s.mu.Lock()
	session := s.ensureClaudeSessionLocked(request, model, effort)
	session.Messages = append(session.Messages, userMsg)
	session.UpdatedAt = now
	if session.Preview == "" {
		session.Preview = request.Text
	}
	backendRef := session.BackendRef
	s.persistClaudeSessionsLocked()
	s.mu.Unlock()
	// A failed first turn can leave a NiceCodex session without a Claude native
	// transcript. Never pass that local-only id to `--resume` on the next send.
	resumeID := backendRef
	if resumeID != "" {
		if _, ok := findClaudeNativeSession(resumeID); !ok {
			resumeID = ""
		}
	}

	s.emitClaudeEvent("message", request.SessionID, turnID, map[string]any{
		"message": userMsg,
	})

	var agentText strings.Builder
	var streamSequence uint64
	var reasoningText strings.Builder
	var reasoningSequence uint64
	agentID := turnID + ":agent"
	pollCtx, stopToolPolling := context.WithCancel(ctx)
	defer stopToolPolling()
	toolPollingDone := make(chan struct{})
	toolPollingStarted := false
	pollSessionID := ""
	s.emitClaudeEvent("message.started", request.SessionID, turnID, map[string]any{
		"id": agentID, "role": "assistant",
	})
	// Keep an in-memory draft for session switches; the completed turn is the only
	// stream update persisted to disk, avoiding a full session rewrite every 400ms.
	lastDraftPatch := time.Now()
	patchDraft := func() {
		text := agentText.String()
		if strings.TrimSpace(text) == "" {
			return
		}
		if time.Since(lastDraftPatch) < 400*time.Millisecond {
			return
		}
		lastDraftPatch = time.Now()
		s.patchClaudeAssistantDraft(request.SessionID, agentID, text)
	}

	output, newSessionID, usage, runErr := s.executeExternalTurn(
		ctx, "claude", resumeID, request.Workspace, turnSettings, request.Text, request.Images,
		func(kind, delta string) {
			switch kind {
			case "session":
				if delta == "" || toolPollingStarted {
					return
				}
				toolPollingStarted = true
				pollSessionID = delta
				go func() {
					defer close(toolPollingDone)
					s.pollClaudeActivity(pollCtx, request.SessionID, turnID, delta, request.Text)
				}()
			case "thought":
				if delta == "" {
					return
				}
				reasoningText.WriteString(delta)
				reasoningSequence++
				s.emitClaudeEvent("message.delta", request.SessionID, turnID, map[string]any{
					"id": turnID + ":reasoning", "role": "reasoning", "delta": delta,
					"text": reasoningText.String(), "mode": "replace", "sequence": reasoningSequence,
				})
			case "replace":
				// Full snapshot (partial assistant / proxy message).
				// Grow same message, or append a new segment after tools — never shrink.
				prev := agentText.String()
				next := delta
				if prev != "" {
					switch {
					case strings.HasPrefix(delta, prev):
						next = delta
					case len(delta) >= len(prev) && strings.Contains(delta, prev):
						next = delta
					case strings.Contains(prev, delta) && len(prev) >= len(delta):
						next = prev
					default:
						// Distinct post-tool segment (GPT/GLM multi-step often does this).
						next = prev + "\n\n" + delta
					}
				}
				agentText.Reset()
				agentText.WriteString(next)
				streamSequence++
				s.emitClaudeEvent("message.delta", request.SessionID, turnID, map[string]any{
					"id": agentID, "role": "assistant", "delta": next, "text": next,
					"mode": "replace", "sequence": streamSequence,
				})
				patchDraft()
			case "text":
				if delta == "" {
					return
				}
				agentText.WriteString(delta)
				streamSequence++
				s.emitClaudeEvent("message.delta", request.SessionID, turnID, map[string]any{
					"id": agentID, "role": "assistant", "delta": delta, "text": agentText.String(),
					"mode": "append", "sequence": streamSequence,
				})
				patchDraft()
			}
		},
	)
	stopToolPolling()
	if toolPollingStarted {
		<-toolPollingDone
		s.emitClaudeActivitySnapshot(request.SessionID, turnID, pollSessionID, request.Text)
	}
	// executeExternalTurn owns source selection, so its return value is canonical.
	finalBody := output
	if finalBody == "" {
		finalBody = agentText.String()
	}
	agentText.Reset()
	agentText.WriteString(finalBody)
	// If stream-json omitted usage, try the native transcript written by Claude Code.
	if usage == nil && newSessionID != "" {
		if native, ok := findClaudeNativeSession(newSessionID); ok {
			if hits := collectClaudeNativeTurnUsage(native.Path, newSessionID); len(hits) > 0 {
				last := hits[len(hits)-1]
				usage = map[string]any{
					"inputTokens":           last.Breakdown.Input,
					"cachedInputTokens":     last.Breakdown.Cached,
					"outputTokens":          last.Breakdown.Output,
					"reasoningOutputTokens": last.Breakdown.Reasoning,
					"totalTokens":           last.Breakdown.Total,
				}
			}
		}
	}
	// Last resort: rough estimate so the timeline is not blank.
	if usage == nil {
		usage = estimateTokenUsage(request.Text, agentText.String())
	}
	completed := time.Now().Unix()
	status := "completed"
	errText := ""
	if errors.Is(runErr, context.Canceled) {
		status = "interrupted"
	} else if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	}
	assistantMsg := ClaudeMessage{
		ID: agentID, Role: "assistant", Text: agentText.String(), Status: status, CreatedAt: completed,
	}
	resolvedBackendRef := ""
	if newSessionID != "" {
		_, nativeSessionExists := findClaudeNativeSession(newSessionID)
		// Successful custom/proxy-backed runs may not expose a transcript through
		// discovery immediately. Failed runs must prove the native session exists.
		if runErr == nil || nativeSessionExists {
			resolvedBackendRef = newSessionID
		}
	}
	finalActivity, _ := loadClaudeCurrentTurnActivity(newSessionID, request.Text, turnID)
	for index := range finalActivity {
		role := strings.ToLower(strings.TrimSpace(finalActivity[index].Role))
		if role == "tool" && strings.EqualFold(finalActivity[index].Status, "inProgress") {
			if status == "completed" {
				finalActivity[index].Status = "completed"
			} else {
				finalActivity[index].Status = status
			}
		}
	}
	finalTail := claudeTextTailAfterActivity(assistantMsg.Text, finalActivity)
	s.mu.Lock()
	if session := s.claudeSessions[request.SessionID]; session != nil {
		// Remove the draft and persist provider-owned activity in native order.
		// This keeps completed/reopened sessions consistent with the live timeline.
		nextMessages := make([]ClaudeMessage, 0, len(session.Messages)+len(finalActivity)+1)
		for _, message := range session.Messages {
			if message.ID != agentID {
				nextMessages = append(nextMessages, message)
			}
		}
		nextMessages = append(nextMessages, finalActivity...)
		if finalTail != "" {
			tailMessage := assistantMsg
			tailMessage.Text = finalTail
			nextMessages = append(nextMessages, tailMessage)
		}
		session.Messages = nextMessages
		session.UpdatedAt = completed
		// Clear stale/local-only refs after failed startup so the next send creates
		// a fresh native conversation instead of repeating "No conversation found".
		session.BackendRef = resolvedBackendRef
		if model != "" {
			session.Model = model
		}
		session.Effort = effort
		s.persistClaudeSessionsLocked()
	}
	s.mu.Unlock()

	if b := breakdownFromUsageMap(usage); b.valid() {
		s.persistTurnUsage("claude", request.SessionID, turnID, b, time.Unix(completed, 0))
	}

	payload := map[string]any{
		"message":  assistantMsg,
		"activity": finalActivity,
		"status":   status,
	}
	if errText != "" {
		payload["error"] = errText
	}
	// Token usage for timeline footer + account usage popover (same shape as Grok/Codex).
	if usage != nil {
		payload["tokenUsage"] = map[string]any{
			"last":  usage,
			"total": usage,
		}
		payload["usage"] = usage
	}
	// Session reload follows the terminal event immediately. Release the native
	// run first so that snapshot cannot resurrect a turn that already finished.
	s.clearClaudeRun(request.SessionID, turnID)
	s.emitClaudeEvent("turn.completed", request.SessionID, turnID, payload)
}

func (s *AppService) pollClaudeActivity(
	ctx context.Context,
	eventSessionID, turnID, nativeSessionID, prompt string,
) {
	ticker := time.NewTicker(180 * time.Millisecond)
	defer ticker.Stop()
	lastSignature := ""
	nativePath := ""
	lastSize := int64(-1)
	lastModified := int64(0)
	emit := func() {
		if nativePath == "" {
			native, ok := findClaudeNativeSession(nativeSessionID)
			if !ok {
				return
			}
			nativePath = native.Path
		}
		info, err := os.Stat(nativePath)
		if err != nil {
			return
		}
		modified := info.ModTime().UnixNano()
		if info.Size() == lastSize && modified == lastModified {
			return
		}
		activity, err := readClaudeCurrentTurnActivity(nativePath, prompt, turnID)
		if err != nil {
			return
		}
		lastSize = info.Size()
		lastModified = modified
		payload, _ := json.Marshal(activity)
		signature := string(payload)
		if signature == lastSignature {
			return
		}
		lastSignature = signature
		s.emitClaudeEvent("activity.snapshot", eventSessionID, turnID, map[string]any{
			"messages": activity,
		})
	}

	emit()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emit()
		}
	}
}

func (s *AppService) emitClaudeActivitySnapshot(eventSessionID, turnID, nativeSessionID, prompt string) {
	activity, err := loadClaudeCurrentTurnActivity(nativeSessionID, prompt, turnID)
	if err != nil {
		return
	}
	s.emitClaudeEvent("activity.snapshot", eventSessionID, turnID, map[string]any{
		"messages": activity,
	})
}

func loadClaudeCurrentTurnActivity(sessionID, prompt, turnID string) ([]ClaudeMessage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("Claude native session id is unavailable")
	}
	native, ok := findClaudeNativeSession(sessionID)
	if !ok {
		return nil, errors.New("Claude native session was not found")
	}
	return readClaudeCurrentTurnActivity(native.Path, prompt, turnID)
}

func claudeTextTailAfterActivity(fullText string, activity []ClaudeMessage) string {
	if fullText == "" {
		return ""
	}
	cursor := 0
	matched := false
	for _, message := range activity {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") || message.ToolName != "" {
			continue
		}
		segment := strings.TrimSpace(message.Text)
		if segment == "" {
			continue
		}
		index := strings.Index(fullText[cursor:], segment)
		if index < 0 {
			if matched {
				return strings.TrimLeft(fullText[cursor:], " \t\r\n")
			}
			return fullText
		}
		cursor += index + len(segment)
		matched = true
	}
	if !matched {
		return fullText
	}
	return strings.TrimLeft(fullText[cursor:], " \t\r\n")
}

func (s *AppService) ensureClaudeSessionLocked(request ClaudeSendRequest, model, effort string) *claudeStoredSession {
	now := time.Now().Unix()
	session := s.claudeSessions[request.SessionID]
	if session == nil {
		name := request.Text
		if name == "" {
			name = "New Claude task"
		}
		session = &claudeStoredSession{
			ID: request.SessionID, Workspace: request.Workspace,
			Name: truncateRunes(name, 56), Preview: request.Text,
			Model: model, Effort: effort, CreatedAt: now, UpdatedAt: now,
			Messages: make([]ClaudeMessage, 0, 8),
		}
		s.claudeSessions[request.SessionID] = session
	}
	return session
}

// patchClaudeAssistantDraft updates only the in-memory session index. The final
// turn result owns persistence, so streaming never rewrites the whole index.
func (s *AppService) patchClaudeAssistantDraft(sessionID, agentID, text string) {
	sessionID = strings.TrimSpace(sessionID)
	agentID = strings.TrimSpace(agentID)
	if sessionID == "" || agentID == "" || strings.TrimSpace(text) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.claudeSessions[sessionID]
	if session == nil {
		return
	}
	now := time.Now().Unix()
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if session.Messages[i].ID == agentID {
			// Only grow draft text (never shrink mid-stream).
			if len(text) < len(session.Messages[i].Text) {
				return
			}
			session.Messages[i].Text = text
			session.Messages[i].Status = "inProgress"
			session.Messages[i].CreatedAt = now
			session.UpdatedAt = now
			return
		}
	}
	session.Messages = append(session.Messages, ClaudeMessage{
		ID: agentID, Role: "assistant", Text: text, Status: "inProgress", CreatedAt: now,
	})
	session.UpdatedAt = now
	s.persistClaudeSessionsLocked()
}

// ReadClaudeCapabilities is implemented in claude_capabilities.go
// (full MCP / skills / agents / plugins / hooks / settings summary).

func (s *AppService) ReadClaudeGlobalInstructions() GlobalInstructionsInfo {
	home := resolveClaudeHome()
	if home == "" {
		return GlobalInstructionsInfo{}
	}
	// Claude Code commonly uses CLAUDE.md or AGENTS.md under ~/.claude
	candidates := []string{
		filepath.Join(home, "CLAUDE.md"),
		filepath.Join(home, "AGENTS.md"),
		filepath.Join(home, "CLAUDE.local.md"),
	}
	for _, path := range candidates {
		payload, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(payload)
		return GlobalInstructionsInfo{
			Content: text, Path: path, Source: filepath.Base(path),
			Exists: true, EmptyFile: strings.TrimSpace(text) == "", Available: true,
		}
	}
	return GlobalInstructionsInfo{
		Path: filepath.Join(home, "CLAUDE.md"), Source: "CLAUDE.md", Available: true,
	}
}

func (s *AppService) SaveClaudeGlobalInstructions(content string) (GlobalInstructionsInfo, error) {
	home := resolveClaudeHome()
	if home == "" {
		return GlobalInstructionsInfo{}, os.ErrNotExist
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return GlobalInstructionsInfo{}, err
	}
	path := filepath.Join(home, "CLAUDE.md")
	// Prefer updating an existing file if present.
	for _, candidate := range []string{
		filepath.Join(home, "CLAUDE.md"),
		filepath.Join(home, "AGENTS.md"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
	}
	trimmed := sanitizeCustomInstructions(content)
	if trimmed != "" && !strings.HasSuffix(trimmed, "\n") {
		trimmed += "\n"
	}
	if err := os.WriteFile(path, []byte(trimmed), 0o600); err != nil {
		return GlobalInstructionsInfo{}, err
	}
	return s.ReadClaudeGlobalInstructions(), nil
}

func (s *AppService) ReadClaudeProjectInstructions() ProjectInstructionsInfo {
	workspace := strings.TrimSpace(s.Settings().ClaudeWorkspace)
	if workspace == "" {
		return ProjectInstructionsInfo{}
	}
	clean, err := validateWorkspace(workspace)
	if err != nil {
		return ProjectInstructionsInfo{}
	}
	// Prefer CLAUDE.md then AGENTS.md at project root.
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "CLAUDE.local.md"} {
		path := filepath.Join(clean, name)
		payload, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(payload)
		return ProjectInstructionsInfo{
			Content: text, Workspace: clean, WorkspaceName: filepath.Base(clean),
			Path: path, Source: name, Exists: true, EmptyFile: strings.TrimSpace(text) == "", Available: true,
		}
	}
	return ProjectInstructionsInfo{
		Workspace: clean, WorkspaceName: filepath.Base(clean),
		Path: filepath.Join(clean, "CLAUDE.md"), Source: "CLAUDE.md", Available: true,
	}
}

func (s *AppService) SaveClaudeProjectInstructions(content string) (ProjectInstructionsInfo, error) {
	workspace := strings.TrimSpace(s.Settings().ClaudeWorkspace)
	if workspace == "" {
		return ProjectInstructionsInfo{}, errors.New("choose a Claude workspace first")
	}
	clean, err := validateWorkspace(workspace)
	if err != nil {
		return ProjectInstructionsInfo{}, err
	}
	path := filepath.Join(clean, "CLAUDE.md")
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		candidate := filepath.Join(clean, name)
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
	}
	trimmed := sanitizeCustomInstructions(content)
	if trimmed != "" && !strings.HasSuffix(trimmed, "\n") {
		trimmed += "\n"
	}
	if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
		return ProjectInstructionsInfo{}, err
	}
	return s.ReadClaudeProjectInstructions(), nil
}

func (s *AppService) OpenClaudeHome() error {
	home := resolveClaudeHome()
	if home == "" {
		return errors.New("Claude home is unavailable")
	}
	_ = os.MkdirAll(home, 0o700)
	return openPathInOS(home)
}

func (s *AppService) OpenClaudeConfigFile() error {
	home := resolveClaudeHome()
	if home == "" {
		return errors.New("Claude home is unavailable")
	}
	_ = os.MkdirAll(home, 0o700)
	path := filepath.Join(home, "settings.json")
	if _, err := os.Stat(path); err != nil {
		_ = os.WriteFile(path, []byte("{\n}\n"), 0o600)
	}
	return openPathInOS(path)
}

func normalizeClaudeEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "high"
	}
}

// ensure activeWorkspacePath knows Claude (used by various helpers).
func activeWorkspaceForRuntime(settings UserSettings) string {
	return workspaceForRuntime(settings, normalizeRuntime(settings.ActiveRuntime))
}

func workspaceForRuntime(settings UserSettings, runtimeID string) string {
	switch normalizeRuntime(runtimeID) {
	case "grok":
		return settings.GrokWorkspace
	case "claude":
		return settings.ClaudeWorkspace
	case "gemini":
		if strings.TrimSpace(settings.GeminiWorkspace) != "" {
			return settings.GeminiWorkspace
		}
		return settings.Workspace
	case "opencode":
		if strings.TrimSpace(settings.OpenCodeWorkspace) != "" {
			return settings.OpenCodeWorkspace
		}
		return settings.Workspace
	default:
		return settings.Workspace
	}
}

func recentWorkspacesForRuntime(settings UserSettings) []string {
	return recentWorkspacesForRuntimeID(settings, normalizeRuntime(settings.ActiveRuntime))
}

func recentWorkspacesForRuntimeID(settings UserSettings, runtimeID string) []string {
	switch normalizeRuntime(runtimeID) {
	case "grok":
		if len(settings.GrokRecentWorkspaces) > 0 {
			return settings.GrokRecentWorkspaces
		}
	case "claude":
		if len(settings.ClaudeRecentWorkspaces) > 0 {
			return settings.ClaudeRecentWorkspaces
		}
	case "gemini":
		if len(settings.GeminiRecentWorkspaces) > 0 {
			return settings.GeminiRecentWorkspaces
		}
	case "opencode":
		if len(settings.OpenCodeRecentWorkspaces) > 0 {
			return settings.OpenCodeRecentWorkspaces
		}
	}
	return settings.RecentWorkspaces
}
