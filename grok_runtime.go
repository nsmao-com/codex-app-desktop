package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"nice_codex_desktop/internal/codex"
)

const (
	grokBackendBuild = "build"
	grokBackendAPI   = "api"
)

type GrokRuntimeStatus struct {
	BuildAvailable     bool   `json:"buildAvailable"`
	BuildAuthenticated bool   `json:"buildAuthenticated"`
	BuildVersion       string `json:"buildVersion"`
	BuildExecutable    string `json:"buildExecutable"`
	APIConfigured      bool   `json:"apiConfigured"`
}

var grokRuntimeProbeCache = struct {
	sync.Mutex
	status    GrokRuntimeStatus
	expiresAt time.Time
}{}

type GrokSessionSummary struct {
	ID        string `json:"id"`
	Backend   string `json:"backend"`
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
	Preview   string `json:"preview"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type GrokMessage struct {
	ID       string   `json:"id"`
	Role     string   `json:"role"`
	Text     string   `json:"text"`
	Images   []string `json:"images,omitempty"`
	ToolName string   `json:"toolName,omitempty"`
	// ToolKind classifies Grok Build tools for the workbench timeline:
	// file | command | search | mcp | tool
	ToolKind  string `json:"toolKind,omitempty"`
	Command   string `json:"command,omitempty"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Diff      string `json:"diff,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

type grokToolCallMeta struct {
	Name string
	Args string
}

type GrokSessionDetail struct {
	Summary           GrokSessionSummary `json:"summary"`
	Messages          []GrokMessage      `json:"messages"`
	HistoryStart      int                `json:"historyStart"`
	HistoryTotal      int                `json:"historyTotal"`
	HistoryTurnOffset int                `json:"historyTurnOffset"`
	HasEarlier        bool               `json:"hasEarlier"`
}

type GrokSendRequest struct {
	Backend   string   `json:"backend"`
	SessionID string   `json:"sessionId"`
	Workspace string   `json:"workspace"`
	Text      string   `json:"text"`
	Images    []string `json:"images"`
	Model     string   `json:"model"`
	Effort    string   `json:"effort"`
}

type GrokTurnRef struct {
	Backend   string `json:"backend"`
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
}

type grokNativeSession struct {
	Summary GrokSessionSummary
	Dir     string
}

func normalizeRuntime(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "grok":
		return "grok"
	case "claude":
		return "claude"
	case "gemini":
		return "gemini"
	case "opencode", "open-code":
		return "opencode"
	default:
		return "codex"
	}
}

func normalizeGrokBackend(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), grokBackendAPI) {
		return grokBackendAPI
	}
	return grokBackendBuild
}

func normalizeGrokEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "high"
	}
}

func resolveGrokHome() string {
	if value := strings.TrimSpace(os.Getenv("GROK_HOME")); value != "" {
		return filepath.Clean(value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	// Official Grok Build uses ~/.grok on Windows, macOS, and Linux.
	return filepath.Join(home, ".grok")
}

func detectGrokRuntime() GrokRuntimeStatus {
	now := time.Now()
	grokRuntimeProbeCache.Lock()
	if now.Before(grokRuntimeProbeCache.expiresAt) {
		status := grokRuntimeProbeCache.status
		grokRuntimeProbeCache.Unlock()
		return status
	}
	grokRuntimeProbeCache.Unlock()

	// GUI apps (Win Explorer / macOS Finder) need PATH enrichment before LookPath.
	codex.EnrichPathForLookups()
	status := GrokRuntimeStatus{APIConfigured: grokAPIKeyConfigured()}
	executable := findCommand(commandCandidates("grok"))
	if executable == "" {
		cacheGrokRuntimeStatus(status)
		return status
	}
	// Binary present = installed. Auth probe is best-effort and must not
	// flip BuildAvailable off when `grok models` is slow/offline.
	status.BuildAvailable = true
	status.BuildExecutable = executable
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	versionPath, versionArgs, resolveErr := providerCommand(executable, []string{"--version"})
	if resolveErr == nil {
		versionCommand := exec.CommandContext(ctx, versionPath, versionArgs...)
		output, versionErr := runManagedCombinedOutput(ctx, versionCommand)
		if versionErr == nil {
			status.BuildVersion = strings.TrimSpace(firstOutputLine(string(output)))
		} else if len(output) > 0 {
			// Some builds print version to stderr on non-zero codes; still capture it.
			if line := strings.TrimSpace(firstOutputLine(string(output))); line != "" {
				status.BuildVersion = line
			}
		}
	}
	ctxAuth, cancelAuth := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancelAuth()
	authPath, authArgs, authResolveErr := providerCommand(executable, []string{"models"})
	if authResolveErr == nil {
		authCommand := exec.CommandContext(ctxAuth, authPath, authArgs...)
		if _, err := runManagedCombinedOutput(ctxAuth, authCommand); err == nil {
			status.BuildAuthenticated = true
		}
	}
	cacheGrokRuntimeStatus(status)
	return status
}

func detectGrokRuntimeQuick() GrokRuntimeStatus {
	codex.EnrichPathForLookups()
	executable := findCommand(commandCandidates("grok"))
	return GrokRuntimeStatus{
		BuildAvailable:  executable != "",
		BuildExecutable: executable,
		APIConfigured:   grokAPIKeyConfigured(),
	}
}

func cacheGrokRuntimeStatus(status GrokRuntimeStatus) {
	grokRuntimeProbeCache.Lock()
	grokRuntimeProbeCache.status = status
	grokRuntimeProbeCache.expiresAt = time.Now().Add(providerProbeCacheTTL)
	grokRuntimeProbeCache.Unlock()
}

func (s *AppService) RefreshGrokRuntime() GrokRuntimeStatus {
	status := detectGrokRuntime()
	// Prefer settings-stored API key when env is empty.
	if !status.APIConfigured {
		status.APIConfigured = s.grokAPIKeyConfiguredWithSettings()
	}
	// Keep agentProviders.grok in sync so Settings "ready" badge is not stuck.
	s.mu.Lock()
	if len(s.agentProviders) > 0 {
		next := make([]AgentProviderRuntime, len(s.agentProviders))
		copy(next, s.agentProviders)
		for i := range next {
			if next[i].Kind != "grok" {
				continue
			}
			next[i].Installed = status.BuildAvailable || status.APIConfigured
			next[i].Healthy = status.BuildAvailable || status.APIConfigured
			next[i].RuntimeReady = status.BuildAvailable || status.APIConfigured
			next[i].Version = status.BuildVersion
			next[i].Executable = status.BuildExecutable
			next[i].Status = providerStatus(next[i].Installed, next[i].RuntimeReady, true)
			next[i].Message = grokProviderMessage(status)
		}
		s.agentProviders = next
	}
	s.mu.Unlock()
	return status
}

// RefreshGrokRuntimeQuick checks only local configuration and executable
// availability. Entering the Grok workspace must not race a live turn with the
// slower `grok models` authentication probe.
func (s *AppService) RefreshGrokRuntimeQuick() GrokRuntimeStatus {
	status := detectGrokRuntimeQuick()
	if !status.APIConfigured {
		status.APIConfigured = s.grokAPIKeyConfiguredWithSettings()
	}
	return status
}

func (s *AppService) SetActiveRuntime(runtimeID string) (map[string]any, error) {
	runtimeID = normalizeRuntime(runtimeID)
	s.mu.Lock()
	settings := cloneSettings(s.settings)
	settings.ActiveRuntime = runtimeID
	if err := writeSettings(s.settingsPath, settings); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.settings = settings
	s.mu.Unlock()
	workspace := settings.Workspace
	switch runtimeID {
	case "grok":
		workspace = settings.GrokWorkspace
	case "claude":
		workspace = settings.ClaudeWorkspace
	}
	result := map[string]any{"runtime": runtimeID}
	if strings.TrimSpace(workspace) != "" {
		result["workspace"] = inspectWorkspace(workspace)
	}
	return result, nil
}

func (s *AppService) activeWorkspacePath() string {
	return activeWorkspaceForRuntime(s.Settings())
}

func (s *AppService) SelectGrokWorkspace() (WorkspaceInfo, error) {
	current := s.Settings().GrokWorkspace
	path, err := s.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                "Choose a Grok workspace",
		Message:              "Select the project folder Grok can work in.",
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
	return s.UseGrokWorkspace(path)
}

func (s *AppService) UseGrokWorkspace(path string) (WorkspaceInfo, error) {
	cleanPath, err := validateWorkspace(path)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	s.mu.Lock()
	settings := cloneSettings(s.settings)
	settings.GrokWorkspace = cleanPath
	settings.GrokRecentWorkspaces = rememberWorkspace(settings.GrokRecentWorkspaces, cleanPath)
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

func (s *AppService) ListGrokSessions(backend, workspace, search string) ([]GrokSessionSummary, error) {
	backend = normalizeGrokBackend(backend)
	// Workspace is optional for native sessions: we return all projects so the sidebar
	// can group like Codex. API sessions stay scoped when a workspace is provided.
	cleanWorkspace := ""
	if strings.TrimSpace(workspace) != "" {
		if clean, err := validateWorkspace(workspace); err == nil {
			cleanWorkspace = clean
		}
	}
	s.mu.Lock()
	meta := loadGrokMeta(s.settingsPath)
	s.mu.Unlock()
	archived := meta.Archived
	if backend == grokBackendAPI {
		list := s.listGrokAPISessions(cleanWorkspace, search)
		result := make([]GrokSessionSummary, 0, len(list))
		for _, item := range list {
			if _, hidden := archived[item.ID]; hidden {
				continue
			}
			result = append(result, s.applyGrokLocalName(item))
		}
		return result, nil
	}
	sessions, err := scanGrokNativeSessions()
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(search))
	result := make([]GrokSessionSummary, 0, len(sessions))
	for _, session := range sessions {
		if _, hidden := archived[session.Summary.ID]; hidden {
			continue
		}
		summary := s.applyGrokLocalName(session.Summary)
		haystack := strings.ToLower(summary.Name + "\n" + summary.Preview + "\n" + summary.Workspace)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		result = append(result, summary)
	}
	// Active workspace first, then most recently updated.
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

func (s *AppService) ReadGrokSession(backend, sessionID string) (GrokSessionDetail, error) {
	return s.readGrokSessionPage(backend, sessionID, -1)
}

func (s *AppService) ReadGrokSessionHistory(backend, sessionID string, before int) (GrokSessionDetail, error) {
	return s.readGrokSessionPage(backend, sessionID, before)
}

func (s *AppService) readGrokSessionPage(backend, sessionID string, before int) (GrokSessionDetail, error) {
	detail, err := s.readGrokSessionFull(backend, sessionID)
	if err != nil {
		return GrokSessionDetail{}, err
	}
	return paginateGrokSession(detail, before), nil
}

func (s *AppService) readGrokSessionFull(backend, sessionID string) (GrokSessionDetail, error) {
	backend = normalizeGrokBackend(backend)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return GrokSessionDetail{}, errors.New("Grok session id is required")
	}
	if backend == grokBackendAPI {
		return s.readGrokAPISession(sessionID)
	}
	if summary, messages, ok := s.cachedGrokHistory(sessionID); ok {
		return GrokSessionDetail{Summary: s.applyGrokLocalName(summary), Messages: messages}, nil
	}
	session, err := findGrokNativeSession(sessionID)
	if err != nil {
		return GrokSessionDetail{}, err
	}
	historyPath := filepath.Join(session.Dir, "chat_history.jsonl")
	before, _ := os.Stat(historyPath)
	messages, err := readGrokNativeMessages(session.Dir)
	if err != nil {
		return GrokSessionDetail{}, err
	}
	s.cacheGrokHistory(sessionID, session, messages, before)
	return GrokSessionDetail{Summary: s.applyGrokLocalName(session.Summary), Messages: messages}, nil
}

func paginateGrokSession(detail GrokSessionDetail, before int) GrokSessionDetail {
	messages := detail.Messages
	start, end, turnOffset := conversationMessagePageBounds(len(messages), before, func(index int) bool {
		role := strings.ToLower(strings.TrimSpace(messages[index].Role))
		return role == "user" || role == "human"
	})
	detail.Messages = append([]GrokMessage(nil), messages[start:end]...)
	detail.HistoryStart = start
	detail.HistoryTotal = len(messages)
	detail.HistoryTurnOffset = turnOffset
	detail.HasEarlier = start > 0
	return detail
}

func (s *AppService) cachedGrokHistory(sessionID string) (GrokSessionSummary, []GrokMessage, bool) {
	s.historyMu.Lock()
	snapshot := s.grokHistoryCache[sessionID]
	s.historyMu.Unlock()
	if snapshot == nil {
		return GrokSessionSummary{}, nil, false
	}
	info, err := os.Stat(snapshot.path)
	if err != nil || info.Size() != snapshot.size || info.ModTime().UnixNano() != snapshot.modified {
		s.historyMu.Lock()
		if s.grokHistoryCache[sessionID] == snapshot {
			delete(s.grokHistoryCache, sessionID)
		}
		s.historyMu.Unlock()
		return GrokSessionSummary{}, nil, false
	}
	s.historyMu.Lock()
	if current := s.grokHistoryCache[sessionID]; current == snapshot {
		current.touchedAt = time.Now()
	}
	s.historyMu.Unlock()
	return snapshot.summary, snapshot.messages, true
}

func (s *AppService) cacheGrokHistory(
	sessionID string,
	session grokNativeSession,
	messages []GrokMessage,
	before os.FileInfo,
) {
	path := filepath.Join(session.Dir, "chat_history.jsonl")
	info, err := os.Stat(path)
	if err != nil || before == nil || info.Size() != before.Size() || info.ModTime() != before.ModTime() {
		return
	}
	weight := info.Size() * 2
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.grokHistoryCache == nil {
		s.grokHistoryCache = make(map[string]*grokHistorySnapshot)
	}
	if weight > conversationHistoryEntryBytes {
		delete(s.grokHistoryCache, sessionID)
		return
	}
	s.grokHistoryCache[sessionID] = &grokHistorySnapshot{
		dir: session.Dir, path: path, size: info.Size(), modified: info.ModTime().UnixNano(),
		summary: session.Summary, messages: messages, weight: weight, touchedAt: time.Now(),
	}
	s.pruneConversationHistoryCachesLocked()
}

func (s *AppService) cachedGrokUsageSource(sessionID string) (string, int, bool) {
	s.historyMu.Lock()
	snapshot := s.grokHistoryCache[sessionID]
	s.historyMu.Unlock()
	if snapshot == nil {
		return "", 0, false
	}
	info, err := os.Stat(snapshot.path)
	if err != nil || info.Size() != snapshot.size || info.ModTime().UnixNano() != snapshot.modified {
		return "", 0, false
	}
	count := 0
	for _, message := range snapshot.messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "user" || role == "human" {
			count++
		}
	}
	return snapshot.dir, count, true
}

func (s *AppService) clearGrokRun(turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, run := range s.externalRuns {
		if run != nil && run.turnID == turnID {
			delete(s.externalRuns, key)
		}
	}
}

func (s *AppService) SendGrokMessage(request GrokSendRequest) (GrokTurnRef, error) {
	request.Backend = normalizeGrokBackend(request.Backend)
	request.SessionID = strings.TrimSpace(request.SessionID)
	clientSessionID := request.SessionID
	newSession := request.SessionID == "" || strings.HasPrefix(request.SessionID, "pending-grok-")
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" && len(request.Images) == 0 {
		return GrokTurnRef{}, errors.New("message is required")
	}
	workspace, err := validateWorkspace(request.Workspace)
	if err != nil {
		return GrokTurnRef{}, err
	}
	images, err := s.validateImageAttachments(request.Images)
	if err != nil {
		return GrokTurnRef{}, err
	}
	if newSession {
		request.SessionID = newUUID()
	}
	request.Workspace = workspace
	request.Images = images
	turnID := "grok-turn-" + newUUID()
	ctx, cancel := context.WithCancel(context.Background())
	key := grokRunKey(request.Backend, request.SessionID)
	s.mu.Lock()
	// Never cancel an in-flight turn on a second Send — that looked like the agent
	// "disconnecting" mid-run when the UI double-dispatched. Explicit stop uses Interrupt.
	if previous := s.externalRuns[key]; previous != nil {
		s.mu.Unlock()
		cancel()
		return GrokTurnRef{}, errors.New("a Grok turn is already running for this session")
	}
	s.externalRuns[key] = &externalRun{turnID: turnID, cancel: cancel}
	s.mu.Unlock()
	startedPayload := map[string]any{"text": request.Text}
	if clientSessionID != "" && clientSessionID != request.SessionID {
		startedPayload["clientSessionId"] = clientSessionID
	}
	s.emitGrokEvent("turn.started", request.Backend, request.SessionID, turnID, startedPayload)
	go s.runGrokTurn(ctx, cancel, turnID, request, newSession)
	return GrokTurnRef{Backend: request.Backend, SessionID: request.SessionID, TurnID: turnID}, nil
}

func (s *AppService) InterruptGrokTurn(ref GrokTurnRef) error {
	backend := normalizeGrokBackend(ref.Backend)
	sessionID := strings.TrimSpace(ref.SessionID)
	turnID := strings.TrimSpace(ref.TurnID)
	s.mu.Lock()
	run := s.externalRuns[grokRunKey(backend, sessionID)]
	// After session.bound the UI may hold the native id while the run was keyed by pending-*.
	if run == nil {
		for _, candidate := range s.externalRuns {
			if candidate == nil {
				continue
			}
			if turnID != "" && candidate.turnID == turnID {
				run = candidate
				break
			}
		}
	}
	s.mu.Unlock()
	if run == nil || (turnID != "" && run.turnID != turnID) {
		return errors.New("Grok turn is not running")
	}
	run.cancel()
	return nil
}

func (s *AppService) IsGrokTurnRunning(ref GrokTurnRef) bool {
	backend := normalizeGrokBackend(ref.Backend)
	sessionID := strings.TrimSpace(ref.SessionID)
	turnID := strings.TrimSpace(ref.TurnID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.externalRuns[grokRunKey(backend, sessionID)]; run != nil {
		return turnID == "" || run.turnID == turnID
	}
	if turnID == "" {
		return false
	}
	for _, run := range s.externalRuns {
		if run != nil && run.turnID == turnID {
			return true
		}
	}
	return false
}

func (s *AppService) isGrokSessionRunning(backend, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.externalRuns[grokRunKey(backend, sessionID)] != nil
}

func (s *AppService) DeleteGrokSession(backend, sessionID string) error {
	backend = normalizeGrokBackend(backend)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("Grok session id is required")
	}
	if s.isGrokSessionRunning(backend, sessionID) {
		return errors.New("stop the running Grok turn before deleting its session")
	}
	if strings.HasPrefix(sessionID, "pending-grok-") {
		s.removeGrokArchiveEntry(sessionID)
		s.dropGrokHistoryCache(sessionID)
		return nil
	}
	if backend == grokBackendAPI {
		if err := s.deleteGrokAPISession(sessionID); err != nil {
			return err
		}
		s.removeGrokArchiveEntry(sessionID)
		s.dropGrokHistoryCache(sessionID)
		return nil
	}
	executable := findCommand(commandCandidates("grok"))
	if executable == "" {
		return errors.New("Grok Build is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	commandPath, commandArgs, resolveErr := providerCommand(executable, []string{"sessions", "delete", sessionID})
	if resolveErr != nil {
		return resolveErr
	}
	command := exec.CommandContext(ctx, commandPath, commandArgs...)
	output, err := runManagedCombinedOutput(ctx, command)
	if err != nil {
		return fmt.Errorf("delete Grok session: %s", strings.TrimSpace(string(output)))
	}
	s.removeGrokArchiveEntry(sessionID)
	s.dropGrokHistoryCache(sessionID)
	return nil
}

func (s *AppService) runGrokTurn(ctx context.Context, cancel context.CancelFunc, turnID string, request GrokSendRequest, newSession bool) {
	defer cancel()
	defer s.clearGrokRun(turnID)
	var (
		err   error
		usage map[string]any
	)
	if request.Backend == grokBackendAPI {
		usage, err = s.runGrokAPITurn(ctx, turnID, request)
	} else {
		usage, err = s.runGrokBuildTurn(ctx, turnID, request, newSession)
	}
	payload := grokTurnUsagePayload(usage)
	if b := breakdownFromUsageMap(usage); b.valid() {
		// Grok spend is stored under the grok runtime bucket (never mixed with Codex).
		s.persistTurnUsage("grok", request.SessionID, turnID, b, time.Now())
	}
	// Clear the run before emitting a terminal event. The frontend reloads the
	// transcript immediately; leaving the run registered can resurrect a stale
	// pending tool row until the next session switch.
	s.clearGrokRun(turnID)
	if errors.Is(err, context.Canceled) {
		s.emitGrokEvent("turn.interrupted", request.Backend, request.SessionID, turnID, payload)
		return
	}
	if err != nil {
		if payload == nil {
			payload = map[string]any{}
		}
		payload["message"] = err.Error()
		s.emitGrokEvent("turn.failed", request.Backend, request.SessionID, turnID, payload)
		return
	}
	s.emitGrokEvent("turn.completed", request.Backend, request.SessionID, turnID, payload)
}

func grokTurnUsagePayload(usage map[string]any) map[string]any {
	if usage == nil {
		return nil
	}
	// Mirror Codex thread/tokenUsage shape so the frontend can reuse normalizers.
	return map[string]any{
		"tokenUsage": map[string]any{
			"last":  usage,
			"total": usage,
		},
		"usage": usage,
	}
}

func (s *AppService) runGrokBuildTurn(ctx context.Context, turnID string, request GrokSendRequest, newSession bool) (map[string]any, error) {
	settings := s.Settings()
	turnSettings := settings
	turnSettings.Model = strings.TrimSpace(request.Model)
	if turnSettings.Model == "" {
		turnSettings.Model = strings.TrimSpace(settings.GrokBuildModel)
	}
	turnSettings.Effort = normalizeGrokEffort(request.Effort)
	turnSettings.Sandbox = settings.GrokSandbox
	turnSettings.ApprovalPolicy = settings.GrokApprovalPolicy
	resumeID := ""
	historyStart := int64(0)
	if !newSession {
		if session, err := findGrokNativeSession(request.SessionID); err == nil {
			resumeID = request.SessionID
			historyStart = -1
			if info, statErr := os.Stat(filepath.Join(session.Dir, "chat_history.jsonl")); statErr == nil {
				historyStart = info.Size()
			}
		}
	}
	var streamedText strings.Builder
	var streamSequence uint64
	var streamedThought strings.Builder
	var thoughtSequence uint64
	pollCtx, stopToolPolling := context.WithCancel(ctx)
	defer stopToolPolling()
	toolPollingDone := make(chan struct{})
	toolPollingStarted := false
	pollSessionID := ""
	_, nativeID, usage, err := s.executeExternalTurn(ctx, "grok", resumeID, request.Workspace, turnSettings, request.Text, request.Images, func(kind, delta string) {
		if delta == "" {
			return
		}
		if kind == "session" {
			if toolPollingStarted {
				return
			}
			toolPollingStarted = true
			pollSessionID = delta
			go func() {
				defer close(toolPollingDone)
				s.pollGrokBuildActivity(pollCtx, request.SessionID, turnID, delta, request.Text, historyStart)
			}()
			return
		}
		if kind == "thought" {
			streamedThought.WriteString(delta)
			thoughtSequence++
			s.emitGrokEvent("thought.delta", grokBackendBuild, request.SessionID, turnID, map[string]any{
				"delta": delta, "text": streamedThought.String(), "mode": "replace", "sequence": thoughtSequence,
			})
			return
		}
		streamedText.WriteString(delta)
		streamSequence++
		s.emitGrokEvent("text.delta", grokBackendBuild, request.SessionID, turnID, map[string]any{
			"delta": delta, "text": streamedText.String(), "mode": "replace", "sequence": streamSequence,
		})
	})
	stopToolPolling()
	if toolPollingStarted {
		<-toolPollingDone
		// Capture a result written immediately before process exit.
		s.emitGrokBuildActivitySnapshot(request.SessionID, turnID, pollSessionID, request.Text, historyStart)
	}
	bindNativeSession := err == nil
	if !bindNativeSession && nativeID != "" {
		_, findErr := findGrokNativeSession(nativeID)
		bindNativeSession = findErr == nil
	}
	if bindNativeSession && nativeID != "" && nativeID != request.SessionID {
		// Re-key the live run under the native session id so Interrupt works after bind.
		s.rekeyGrokRun(request.Backend, request.SessionID, nativeID, turnID)
		s.emitGrokEvent("session.bound", grokBackendBuild, request.SessionID, turnID, map[string]any{"sessionId": nativeID})
	}
	return usage, err
}

// pollGrokBuildActivity mirrors provider-owned assistant/tool ordering while the
// CLI is active. The cumulative streaming-json text remains the source for the
// not-yet-persisted tail and is never overwritten by this snapshot.
func (s *AppService) pollGrokBuildActivity(
	ctx context.Context,
	eventSessionID, turnID, nativeSessionID, prompt string,
	historyStart int64,
) {
	ticker := time.NewTicker(180 * time.Millisecond)
	defer ticker.Stop()
	lastSignature := ""
	sessionDir := ""
	lastSize := int64(-1)
	lastModified := int64(0)
	emit := func() {
		if sessionDir == "" {
			session, err := findGrokNativeSession(nativeSessionID)
			if err != nil {
				return
			}
			sessionDir = session.Dir
		}
		path := filepath.Join(sessionDir, "chat_history.jsonl")
		info, err := os.Stat(path)
		if err != nil {
			return
		}
		modified := info.ModTime().UnixNano()
		if info.Size() == lastSize && modified == lastModified {
			return
		}
		activity, err := readGrokCurrentTurnActivityFromDir(sessionDir, prompt, historyStart)
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
		s.emitGrokEvent("activity.snapshot", grokBackendBuild, eventSessionID, turnID, map[string]any{
			"messages": activity,
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emit()
		}
	}
}

func (s *AppService) emitGrokBuildActivitySnapshot(eventSessionID, turnID, nativeSessionID, prompt string, historyStart int64) {
	activity, err := readGrokCurrentTurnActivity(nativeSessionID, prompt, historyStart)
	if err != nil {
		return
	}
	s.emitGrokEvent("activity.snapshot", grokBackendBuild, eventSessionID, turnID, map[string]any{
		"messages": activity,
	})
}

func readGrokCurrentTurnActivity(sessionID, prompt string, historyStart int64) ([]GrokMessage, error) {
	session, err := findGrokNativeSession(sessionID)
	if err != nil {
		return nil, err
	}
	return readGrokCurrentTurnActivityFromDir(session.Dir, prompt, historyStart)
}

func readGrokCurrentTurnActivityFromDir(sessionDir, prompt string, historyStart int64) ([]GrokMessage, error) {
	if historyStart < 0 {
		// If the pre-turn boundary could not be captured, suppress native activity
		// for this turn. The streaming channel remains available and the terminal
		// history reload restores the authoritative tool/text ordering.
		return []GrokMessage{}, nil
	}
	ready, err := grokHistoryHasPromptAfter(sessionDir, prompt, historyStart)
	if err != nil {
		return nil, err
	}
	if !ready {
		return []GrokMessage{}, nil
	}
	messages, err := readGrokNativeMessages(sessionDir)
	if err != nil {
		return nil, err
	}
	start := -1
	wanted := strings.TrimSpace(prompt)
	for index := len(messages) - 1; index >= 0; index-- {
		role := strings.ToLower(strings.TrimSpace(messages[index].Role))
		if role != "user" && role != "human" {
			continue
		}
		// The current prompt must be the newest persisted user row. Before Grok
		// writes that row, falling back to the previous turn duplicates its final
		// assistant message in the live timeline.
		if wanted == "" || grokHistoryUserMatchesPrompt(messages[index].Text, wanted) {
			start = index
		}
		break
	}
	if start < 0 {
		return []GrokMessage{}, nil
	}
	activity := make([]GrokMessage, 0, 16)
	for index := start + 1; index < len(messages); index++ {
		message := messages[index]
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "assistant" || role == "reasoning" || role == "tool" || strings.TrimSpace(message.ToolName) != "" {
			activity = append(activity, message)
		}
	}
	return activity, nil
}

func grokHistoryHasPromptAfter(sessionDir, prompt string, historyStart int64) (bool, error) {
	path := filepath.Join(sessionDir, "chat_history.jsonl")
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if historyStart > info.Size() {
		return false, nil
	}
	if _, err := file.Seek(historyStart, io.SeekStart); err != nil {
		return false, err
	}
	wanted := strings.TrimSpace(prompt)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16*1024), 8*1024*1024)
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		role := strings.ToLower(firstMapString(raw, "type", "role"))
		if (role != "user" && role != "human") || firstMapString(raw, "synthetic_reason") != "" {
			continue
		}
		text := extractGrokUserFacingText(raw)
		if wanted == "" || grokHistoryUserMatchesPrompt(text, wanted) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func grokHistoryUserMatchesPrompt(historyText, prompt string) bool {
	historyText = strings.TrimSpace(strings.ReplaceAll(historyText, "\r\n", "\n"))
	prompt = strings.TrimSpace(strings.ReplaceAll(prompt, "\r\n", "\n"))
	if historyText == prompt {
		return true
	}
	shorter, longer := historyText, prompt
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	// Grok Build can persist only a prefix or suffix of very long prompts.
	// Keep short messages exact so unrelated prompts cannot share a turn.
	if len(shorter) < 256 {
		return false
	}
	return strings.HasPrefix(longer, shorter) || strings.HasSuffix(longer, shorter)
}

// rekeyGrokRun moves an in-flight run from pending/local session id → native id.
func (s *AppService) rekeyGrokRun(backend, fromID, toID, turnID string) {
	fromID = strings.TrimSpace(fromID)
	toID = strings.TrimSpace(toID)
	if fromID == "" || toID == "" || fromID == toID {
		return
	}
	fromKey := grokRunKey(backend, fromID)
	toKey := grokRunKey(backend, toID)
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.externalRuns[fromKey]
	if run == nil || run.turnID != turnID {
		return
	}
	delete(s.externalRuns, fromKey)
	s.externalRuns[toKey] = run
}

func (s *AppService) emitGrokEvent(eventType, backend, sessionID, turnID string, data any) {
	s.app.Event.Emit("grok:event", map[string]any{
		"type": eventType, "backend": backend, "sessionId": sessionID, "turnId": turnID, "data": data,
	})
}

func grokRunKey(backend, sessionID string) string {
	return "grok:" + normalizeGrokBackend(backend) + ":" + strings.TrimSpace(sessionID)
}

func scanGrokNativeSessions() ([]grokNativeSession, error) {
	root := filepath.Join(resolveGrokHome(), "sessions")
	if root == "" {
		return []grokNativeSession{}, nil
	}
	result := make([]grokNativeSession, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() || entry.Name() != "summary.json" {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil || len(payload) > 2*1024*1024 {
			return nil
		}
		var raw map[string]any
		if json.Unmarshal(payload, &raw) != nil {
			return nil
		}
		summary := grokSummaryFromMap(raw)
		if summary.ID == "" || summary.Workspace == "" {
			return nil
		}
		result = append(result, grokNativeSession{Summary: summary, Dir: filepath.Dir(path)})
		return nil
	})
	if os.IsNotExist(err) {
		return []grokNativeSession{}, nil
	}
	return result, err
}

func findGrokNativeSession(sessionID string) (grokNativeSession, error) {
	sessions, err := scanGrokNativeSessions()
	if err != nil {
		return grokNativeSession{}, err
	}
	for _, session := range sessions {
		if session.Summary.ID == sessionID {
			return session, nil
		}
	}
	return grokNativeSession{}, errors.New("Grok Build session was not found")
}

func grokSummaryFromMap(raw map[string]any) GrokSessionSummary {
	info, _ := raw["info"].(map[string]any)
	id := firstMapString(info, "id", "session_id", "sessionId")
	workspace := firstMapString(info, "cwd", "workspace")
	name := firstMapString(raw, "generated_title", "session_summary", "title")
	if name == "" {
		name = "New Grok task"
	}
	preview := firstMapString(raw, "session_summary", "generated_title")
	return GrokSessionSummary{
		ID: id, Backend: grokBackendBuild, Workspace: workspace, Name: name, Preview: preview,
		Model: firstMapString(raw, "current_model_id", "model_id"), Effort: firstMapString(raw, "reasoning_effort"),
		CreatedAt: grokTimestamp(raw["created_at"]), UpdatedAt: grokTimestamp(raw["updated_at"]),
	}
}

func grokTimestamp(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	case string:
		if number, err := strconv.ParseInt(typed, 10, 64); err == nil {
			return number
		}
		if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}

func readGrokNativeMessages(sessionDir string) ([]GrokMessage, error) {
	path := filepath.Join(sessionDir, "chat_history.jsonl")
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	messages := make([]GrokMessage, 0)
	// Grok Build stores tool names on assistant.tool_calls; tool_result only has tool_call_id.
	toolCalls := make(map[string]grokToolCallMeta)
	// callID → true once a tool_result (or completed backend tool) is seen.
	completedToolCalls := make(map[string]bool)
	// Pending in-progress tools emitted from assistant.tool_calls before their result.
	// Keyed by callID so a later tool_result can supersede without duplicates.
	pendingToolIndex := make(map[string]int) // callID → index in messages
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	index := 0
	base := filepath.Base(sessionDir)
	appendMsg := func(msg GrokMessage) int {
		index++
		if msg.ID == "" {
			msg.ID = fmt.Sprintf("%s-%d", base, index)
		}
		if msg.Status == "" {
			msg.Status = "completed"
		}
		messages = append(messages, msg)
		return len(messages) - 1
	}
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		kind := strings.ToLower(firstMapString(raw, "type", "role"))
		created := grokTimestamp(raw["created_at"])
		switch kind {
		case "system":
			continue
		case "user":
			if firstMapString(raw, "synthetic_reason") != "" {
				continue
			}
			text := extractGrokUserFacingText(raw)
			if text == "" {
				continue
			}
			appendMsg(GrokMessage{Role: "user", Text: text, CreatedAt: created})
		case "assistant":
			// Register tool calls so later tool_result rows resolve to real names.
			registerGrokToolCalls(raw["tool_calls"], toolCalls)
			text := strings.TrimSpace(textFromExternalValue(raw["content"]))
			if text != "" {
				appendMsg(GrokMessage{Role: "assistant", Text: text, CreatedAt: created})
			}
			// Mid-turn: assistant often has tool_calls with empty content. Surface those
			// as inProgress tool rows immediately so the UI shows tools while running
			// (not only after tool_result lands / turn ends).
			for _, callID := range grokToolCallIDs(raw["tool_calls"]) {
				if completedToolCalls[callID] {
					continue
				}
				if _, exists := pendingToolIndex[callID]; exists {
					continue
				}
				meta := toolCalls[callID]
				toolName := meta.Name
				if toolName == "" {
					toolName = "tool"
				}
				toolKind, filePath, command, detail, diff := classifyGrokTool(toolName, meta.Args, "")
				idx := appendMsg(GrokMessage{
					ID:        fmt.Sprintf("%s-pending-%s", base, callID),
					Role:      "tool",
					Text:      "",
					ToolName:  toolName,
					ToolKind:  toolKind,
					Path:      filePath,
					Command:   command,
					Detail:    detail,
					Diff:      diff,
					Status:    "inProgress",
					CreatedAt: created,
				})
				pendingToolIndex[callID] = idx
			}
		case "reasoning":
			text := extractGrokReasoningSummary(raw)
			if text == "" {
				continue
			}
			appendMsg(GrokMessage{Role: "reasoning", Text: text, CreatedAt: created})
		case "tool_result":
			callID := firstMapString(raw, "tool_call_id", "toolCallId", "id")
			meta := toolCalls[callID]
			toolName := meta.Name
			if toolName == "" {
				toolName = firstMapString(raw, "name", "tool_name", "toolName", "tool")
			}
			if toolName == "" {
				if call, ok := raw["tool_call"].(map[string]any); ok {
					toolName = firstMapString(call, "name", "tool_name", "toolName")
					if meta.Args == "" {
						meta.Args = stringifyGrokArgs(call["arguments"])
					}
				}
			}
			if toolName == "" {
				toolName = "tool"
			}
			output := compactGrokToolOutput(raw)
			toolKind, filePath, command, detail, diff := classifyGrokTool(toolName, meta.Args, output)
			if callID != "" {
				completedToolCalls[callID] = true
			}
			// Upgrade a pending inProgress row in place when the result arrives.
			if callID != "" {
				if idx, ok := pendingToolIndex[callID]; ok && idx >= 0 && idx < len(messages) {
					messages[idx] = GrokMessage{
						ID:        messages[idx].ID,
						Role:      "tool",
						Text:      output,
						ToolName:  toolName,
						ToolKind:  toolKind,
						Path:      filePath,
						Command:   command,
						Detail:    detail,
						Diff:      diff,
						Status:    "completed",
						CreatedAt: created,
					}
					delete(pendingToolIndex, callID)
					continue
				}
			}
			appendMsg(GrokMessage{
				Role: "tool", Text: output, ToolName: toolName, ToolKind: toolKind,
				Path: filePath, Command: command, Detail: detail, Diff: diff, CreatedAt: created,
			})
		case "backend_tool_call":
			// e.g. {"type":"backend_tool_call","kind":{"tool_type":"web_search","action":{...}}}
			toolName, toolKind, filePath, command, detail, text, diff := parseGrokBackendToolCall(raw)
			if toolName == "" {
				continue
			}
			appendMsg(GrokMessage{
				Role: "tool", Text: text, ToolName: toolName, ToolKind: toolKind,
				Path: filePath, Command: command, Detail: detail, Diff: diff, CreatedAt: created,
			})
		default:
			continue
		}
	}
	return messages, scanner.Err()
}

// grokToolCallIDs returns tool_call ids in order from an assistant.tool_calls payload.
func grokToolCallIDs(value any) []string {
	var calls []any
	switch typed := value.(type) {
	case []any:
		calls = typed
	case []map[string]any:
		calls = make([]any, 0, len(typed))
		for _, item := range typed {
			calls = append(calls, item)
		}
	default:
		return nil
	}
	ids := make([]string, 0, len(calls))
	for _, item := range calls {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := firstMapString(call, "id", "tool_call_id", "toolCallId")
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func registerGrokToolCalls(value any, into map[string]grokToolCallMeta) {
	var calls []any
	switch typed := value.(type) {
	case []any:
		calls = typed
	case []map[string]any:
		calls = make([]any, 0, len(typed))
		for _, item := range typed {
			calls = append(calls, item)
		}
	default:
		return
	}
	for _, item := range calls {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := firstMapString(call, "id", "tool_call_id", "toolCallId")
		name := firstMapString(call, "name", "tool_name", "toolName")
		args := call["arguments"]
		// OpenAI-style nested function payload (some exporters use this shape).
		if fn, ok := call["function"].(map[string]any); ok {
			if name == "" {
				name = firstMapString(fn, "name", "tool_name", "toolName")
			}
			if args == nil {
				args = fn["arguments"]
			}
		}
		if id == "" || name == "" {
			continue
		}
		into[id] = grokToolCallMeta{Name: name, Args: stringifyGrokArgs(args)}
	}
}

func stringifyGrokArgs(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case map[string]any:
		payload, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(payload)
	case []any:
		payload, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(payload)
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(payload)
	}
}

func jsonFieldFromArgs(argsJSON string, keys ...string) string {
	argsJSON = strings.TrimSpace(argsJSON)
	if argsJSON == "" {
		return ""
	}
	var raw map[string]any
	if json.Unmarshal([]byte(argsJSON), &raw) != nil {
		return ""
	}
	return firstMapString(raw, keys...)
}

// classifyGrokTool maps Grok Build tool names to timeline-friendly kinds.
func classifyGrokTool(name, argsJSON, output string) (kind, filePath, command, detail, diff string) {
	name = strings.TrimSpace(name)
	lower := strings.ToLower(name)
	filePath = jsonFieldFromArgs(argsJSON, "file_path", "path", "target_file", "targetFile")
	command = jsonFieldFromArgs(argsJSON, "command")
	detail = jsonFieldFromArgs(argsJSON, "query", "pattern", "url", "prompt")
	switch lower {
	case "search_replace", "write", "str_replace", "apply_patch", "edit_file":
		kind = "file"
		if filePath == "" {
			// Sometimes success text embeds the path.
			filePath = extractPathFromToolOutput(output)
		}
	case "run_terminal_command", "bash", "shell", "run_command":
		kind = "command"
		if command == "" {
			command = strings.TrimSpace(output)
		}
	case "web_search", "web_fetch", "web_open", "x_keyword_search", "x_semantic_search":
		kind = "search"
		if detail == "" {
			detail = jsonFieldFromArgs(argsJSON, "url", "query")
		}
	case "use_tool", "search_tool":
		kind = "mcp"
		// Prefer the MCP server/tool name from arguments when present.
		if server := jsonFieldFromArgs(argsJSON, "server", "server_name", "tool_name", "toolName", "name"); server != "" {
			detail = server
		}
		if tool := jsonFieldFromArgs(argsJSON, "tool", "tool_name", "toolName"); tool != "" {
			if detail != "" && !strings.EqualFold(detail, tool) {
				detail = detail + " / " + tool
			} else {
				detail = tool
			}
		}
	default:
		if strings.HasPrefix(lower, "mcp") || strings.Contains(lower, "__") {
			kind = "mcp"
		} else {
			// Built-in agent tools: read_file, grep, todo_write, list_dir, …
			kind = "tool"
		}
	}
	if kind == "file" {
		diff = grokFileDiffFromArgs(lower, argsJSON, filePath)
	}
	return kind, filePath, command, detail, diff
}

func grokFileDiffFromArgs(toolName, argsJSON, filePath string) string {
	var args map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(argsJSON)), &args) != nil {
		return ""
	}

	if toolName == "apply_patch" {
		patch := firstMapString(args, "diff", "patch", "patch_text", "patchText")
		if strings.Contains(patch, "@@ ") && strings.Contains(patch, "--- ") && strings.Contains(patch, "+++ ") {
			return patch
		}
	}

	oldText := firstMapString(args, "old_string", "oldString", "old_text", "oldText")
	newText := firstMapString(args, "new_string", "newString", "new_text", "newText")
	if toolName == "write" && newText == "" {
		newText = firstMapString(args, "content", "text")
	}
	return buildGrokUnifiedDiff(filePath, oldText, newText, toolName == "write")
}

func buildGrokUnifiedDiff(filePath, oldText, newText string, newFile bool) string {
	oldLines := splitGrokDiffLines(oldText)
	newLines := splitGrokDiffLines(newText)
	if slices.Equal(oldLines, newLines) {
		return ""
	}

	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	removed := oldLines[prefix : len(oldLines)-suffix]
	added := newLines[prefix : len(newLines)-suffix]

	path := filepath.ToSlash(strings.TrimSpace(filePath))
	if path == "" {
		path = "file"
	}
	oldPath := "a/" + path
	if newFile {
		oldPath = "/dev/null"
	}
	oldStart := prefix + 1
	if len(removed) == 0 {
		oldStart = prefix
	}
	newStart := prefix + 1
	if len(added) == 0 {
		newStart = prefix
	}

	var diff strings.Builder
	fmt.Fprintf(&diff, "diff --git a/%s b/%s\n--- %s\n+++ b/%s\n", path, path, oldPath, path)
	fmt.Fprintf(&diff, "@@ -%d,%d +%d,%d @@\n", oldStart, len(removed), newStart, len(added))
	for _, line := range removed {
		diff.WriteByte('-')
		diff.WriteString(line)
		diff.WriteByte('\n')
	}
	for _, line := range added {
		diff.WriteByte('+')
		diff.WriteString(line)
		diff.WriteByte('\n')
	}
	return diff.String()
}

func splitGrokDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	return strings.Split(normalized, "\n")
}

func extractPathFromToolOutput(output string) string {
	output = strings.TrimSpace(output)
	// "The file D:\...\foo.go has been updated successfully."
	lower := strings.ToLower(output)
	for _, prefix := range []string{"the file ", "updated ", "wrote "} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			rest := strings.TrimSpace(output[idx+len(prefix):])
			rest = strings.Split(rest, " has been")[0]
			rest = strings.Split(rest, " successfully")[0]
			rest = strings.Trim(rest, `"' `)
			if strings.Contains(rest, `\`) || strings.Contains(rest, `/`) {
				return rest
			}
		}
	}
	return ""
}

func parseGrokBackendToolCall(raw map[string]any) (toolName, toolKind, filePath, command, detail, text, diff string) {
	kindMap, _ := raw["kind"].(map[string]any)
	toolName = firstMapString(kindMap, "tool_type", "type", "name")
	if toolName == "" {
		toolName = firstMapString(raw, "name", "tool_name")
	}
	action, _ := kindMap["action"].(map[string]any)
	if action == nil {
		action, _ = raw["action"].(map[string]any)
	}
	detail = firstMapString(action, "query", "url", "text")
	filePath = firstMapString(action, "path", "file_path")
	command = firstMapString(action, "command")
	text = compactGrokToolOutput(raw)
	if text == "" {
		text = detail
	}
	toolKind, filePath2, command2, detail2, diff := classifyGrokTool(toolName, stringifyGrokArgs(action), text)
	if filePath == "" {
		filePath = filePath2
	}
	if command == "" {
		command = command2
	}
	if detail == "" {
		detail = detail2
	}
	return toolName, toolKind, filePath, command, detail, text, diff
}

// extractGrokUserFacingText pulls the human-visible query from Grok Build history.
// Real turns usually wrap the prompt in <user_query>...</user_query>; injected
// context blocks (user_info, system-reminder, skills lists) are dropped.
func extractGrokUserFacingText(raw map[string]any) string {
	text := strings.TrimSpace(textFromExternalValue(raw["content"]))
	if text == "" {
		text = strings.TrimSpace(firstMapString(raw, "text", "message"))
	}
	if text == "" {
		return ""
	}
	if query := extractXMLTagContent(text, "user_query"); query != "" {
		return query
	}
	if isGrokSyntheticUserBlob(text) {
		return ""
	}
	return text
}

func isGrokSyntheticUserBlob(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "<user_info"),
		strings.HasPrefix(lower, "<system-reminder"),
		strings.HasPrefix(lower, "<git_status"),
		strings.HasPrefix(lower, "<available_skills"),
		strings.HasPrefix(lower, "<agent_skills"),
		strings.Contains(lower, "mcp server connected"),
		strings.Contains(lower, "mcp servers currently connecting"),
		strings.Contains(lower, "the following skills are available for use"),
		strings.Contains(lower, "as you answer the user's questions, you can use the following context"):
		return true
	}
	// Long injected walls without a real query.
	if len(trimmed) > 2_000 && (strings.Contains(lower, "<system-reminder") || strings.Contains(lower, "claude.md") || strings.Contains(lower, "agents.md")) {
		return true
	}
	return false
}

func extractGrokReasoningSummary(raw map[string]any) string {
	if summary, ok := raw["summary"].([]any); ok {
		var parts []string
		for _, item := range summary {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if strings.EqualFold(firstMapString(block, "type"), "summary_text") || firstMapString(block, "text") != "" {
				if text := strings.TrimSpace(firstMapString(block, "text")); text != "" {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	text := strings.TrimSpace(textFromExternalValue(raw["summary"]))
	if text != "" {
		return text
	}
	// Never surface encrypted_content blobs.
	return ""
}

func compactGrokToolOutput(raw map[string]any) string {
	text := strings.TrimSpace(textFromExternalValue(raw["content"]))
	if text == "" {
		text = strings.TrimSpace(firstMapString(raw, "output", "result", "message", "text"))
	}
	if text == "" {
		return ""
	}
	if looksLikeGrokBinaryGarbage(text) {
		return "[binary or non-text tool output]"
	}
	// Keep tool cards readable; full dumps belong in expand/detail later.
	const maxToolRunes = 2500
	runes := []rune(text)
	if len(runes) > maxToolRunes {
		return string(runes[:maxToolRunes]) + "\n…"
	}
	return text
}

func looksLikeGrokBinaryGarbage(text string) bool {
	if text == "" {
		return false
	}
	sample := text
	if len(sample) > 400 {
		sample = sample[:400]
	}
	nonPrintable := 0
	for _, r := range sample {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 32 || r == 0xFFFD {
			nonPrintable++
		}
	}
	// High ratio of control/replacement characters → hide as garbage.
	return nonPrintable > 12 || (len(sample) > 40 && float64(nonPrintable)/float64(len(sample)) > 0.08)
}

func extractXMLTagContent(text, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	lower := strings.ToLower(text)
	start := strings.Index(lower, strings.ToLower(open))
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(lower[start:], strings.ToLower(close))
	if end < 0 {
		return strings.TrimSpace(text[start:])
	}
	return strings.TrimSpace(text[start : start+end])
}
