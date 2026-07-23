package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"nice_codex_desktop/internal/codex"
)

type AppService struct {
	app              *application.App
	mu               sync.Mutex
	client           *codex.Client
	settings         UserSettings
	settingsPath     string
	allowedThreads   map[string]string
	allowedImages    map[string]struct{}
	terminalSessions map[string]*terminalSession
	agentProviders   []AgentProviderRuntime
	sessions         map[string]*SessionRecord
	externalRuns     map[string]*externalRun
	scheduledTasks   *scheduledTaskStore
	schedulerStop    chan struct{}
	updateState      updateDownloadState
}

type BootstrapData struct {
	Codex            codex.Detection        `json:"codex"`
	AgentProviders   []AgentProviderRuntime `json:"agentProviders"`
	Settings         UserSettings           `json:"settings"`
	Workspace        *WorkspaceInfo         `json:"workspace,omitempty"`
	TerminalProfiles []TerminalProfile      `json:"terminalProfiles"`
	AppVersion       string                 `json:"appVersion"`
	UpdateRepo       string                 `json:"updateRepo"`
}

type UserSettings struct {
	Workspace              string   `json:"workspace"`
	RecentWorkspaces       []string `json:"recentWorkspaces"`
	Model                  string   `json:"model"`
	ModelProvider          string   `json:"modelProvider"`
	CustomModels           []string `json:"customModels"`
	Effort                 string   `json:"effort"`
	ServiceTier            string   `json:"serviceTier"`
	CollaborationMode      string   `json:"collaborationMode"`
	Personality            string   `json:"personality"`
	MultiAgentMode         string   `json:"multiAgentMode"`
	Sandbox                string   `json:"sandbox"`
	ApprovalPolicy         string   `json:"approvalPolicy"`
	Theme                  string   `json:"theme"`
	AccentColor            string   `json:"accentColor"`
	FontFamily             string   `json:"fontFamily"`
	TerminalProfile        string   `json:"terminalProfile"`
	Language               string   `json:"language"`
	AutoConnect            bool     `json:"autoConnect"`
	WorkMode               string   `json:"workMode"`
	SendWithModifier       bool     `json:"sendWithModifier"`
	FollowUpBehavior       string   `json:"followUpBehavior"`
	NotifyOnTurnComplete   bool     `json:"notifyOnTurnComplete"`
	CustomInstructions     string   `json:"customInstructions"`
	TranslucentSidebar     bool     `json:"translucentSidebar"`
	HighContrast           bool     `json:"highContrast"`
	PointerCursor          bool     `json:"pointerCursor"`
	ReduceMotion           bool     `json:"reduceMotion"`
	UiFontSize             string   `json:"uiFontSize"`
	CodeFontSize           string   `json:"codeFontSize"`
	PreventSleepWhileRunning bool   `json:"preventSleepWhileRunning"`
	AlwaysOnTop            bool     `json:"alwaysOnTop"`
	GitBranchPrefix        string   `json:"gitBranchPrefix"`
	GitCommitPrefix        string   `json:"gitCommitPrefix"`
	GitOpenPRAfterPush     bool     `json:"gitOpenPRAfterPush"`
	GitPRBodyTemplate      string   `json:"gitPRBodyTemplate"`
	BrowserAllowedHosts    []string `json:"browserAllowedHosts"`
	BrowserBlockedHosts    []string `json:"browserBlockedHosts"`
	BrowserDownloadDir     string   `json:"browserDownloadDir"`
	BrowserFullCDP         bool     `json:"browserFullCDP"`
	ShortcutCommandPalette string   `json:"shortcutCommandPalette"`
	ShortcutNewThread      string   `json:"shortcutNewThread"`
	ShortcutTerminal       string   `json:"shortcutTerminal"`
	ShortcutBrowser        string   `json:"shortcutBrowser"`
	OnboardingCompleted    bool     `json:"onboardingCompleted"`
}

type WorkspaceInfo struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsGit    bool        `json:"isGit"`
	Branch   string      `json:"branch"`
	Changes  []GitChange `json:"changes"`
	GitError string      `json:"gitError,omitempty"`
}

// GlobalInstructionsInfo is the personal Codex AGENTS.md under CODEX_HOME.
type GlobalInstructionsInfo struct {
	Content   string `json:"content"`
	Path      string `json:"path"`
	Source    string `json:"source"`
	Exists    bool   `json:"exists"`
	EmptyFile bool   `json:"emptyFile"`
	Available bool   `json:"available"`
}

// ProjectInstructionsInfo is the workspace-root AGENTS.md (project-scoped Codex guidance).
type ProjectInstructionsInfo struct {
	Content       string `json:"content"`
	Workspace     string `json:"workspace"`
	WorkspaceName string `json:"workspaceName"`
	Path          string `json:"path"`
	Source        string `json:"source"`
	Exists        bool   `json:"exists"`
	EmptyFile     bool   `json:"emptyFile"`
	Available     bool   `json:"available"`
}

type GitChange struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

type SendMessageRequest struct {
	ThreadID          string   `json:"threadId"`
	Text              string   `json:"text"`
	Images            []string `json:"images"`
	// Per-turn mode override — mirrors official TUI SubmitUserMessageWithMode.
	CollaborationMode string `json:"collaborationMode,omitempty"`
}

type SessionPreferencesRequest struct {
	SessionID         string `json:"sessionId"`
	Model             string `json:"model"`
	Effort            string `json:"effort"`
	CollaborationMode string `json:"collaborationMode"`
}

type SteerTurnRequest struct {
	ThreadID string   `json:"threadId"`
	TurnID   string   `json:"turnId"`
	Text     string   `json:"text"`
	Images   []string `json:"images"`
}

type PluginInstallRequest struct {
	PluginName            string `json:"pluginName"`
	MarketplacePath       string `json:"marketplacePath"`
	RemoteMarketplaceName string `json:"remoteMarketplaceName"`
}

type ReviewStartRequest struct {
	ThreadID     string `json:"threadId"`
	TargetType   string `json:"targetType"`
	Branch       string `json:"branch"`
	Instructions string `json:"instructions"`
	Delivery     string `json:"delivery"`
}

type SkillConfigRequest struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

func NewAppService(app *application.App) *AppService {
	settingsPath := resolveSettingsPath()
	settings := defaultSettings()
	if loaded, err := readSettings(settingsPath); err == nil {
		settings = loaded
	}

	service := &AppService{
		app:              app,
		settings:         settings,
		settingsPath:     settingsPath,
		allowedThreads:   make(map[string]string),
		allowedImages:    make(map[string]struct{}),
		terminalSessions: make(map[string]*terminalSession),
		sessions:         loadSessions(settingsPath),
		externalRuns:     make(map[string]*externalRun),
		scheduledTasks:   newScheduledTaskStore(settingsPath),
		schedulerStop:    make(chan struct{}),
	}
	_ = service.scheduledTasks.load()
	service.client = codex.NewClient(func(event codex.Event) {
		service.remapCodexEvent(&event)
		service.maybeRecordLocalUsage(event)
		app.Event.Emit("codex:event", event)
	})
	go service.runScheduledTaskLoop()
	return service
}

func (s *AppService) Bootstrap() BootstrapData {
	settings := s.Settings()
	settings.ModelProvider = sanitizeWorkbenchProvider(settings.ModelProvider)
	codexDetection := codex.Detect()
	agentProviders := detectAgentProviders(codexDetection)
	s.mu.Lock()
	s.agentProviders = agentProviders
	s.settings.ModelProvider = settings.ModelProvider
	s.mu.Unlock()
	data := BootstrapData{
		Codex:            codexDetection,
		AgentProviders:   agentProviders,
		Settings:         settings,
		TerminalProfiles: listTerminalProfiles(),
		AppVersion:       AppVersion,
		UpdateRepo:       GitHubRepo,
	}
	s.applyAlwaysOnTop(settings.AlwaysOnTop)
	if settings.Workspace != "" {
		workspace := inspectWorkspace(settings.Workspace)
		data.Workspace = &workspace
	}
	return data
}

func (s *AppService) Settings() UserSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSettings(s.settings)
}

func (s *AppService) SavePreferences(settings UserSettings) (UserSettings, error) {
	current := s.Settings()
	settings.Workspace = current.Workspace
	settings.RecentWorkspaces = current.RecentWorkspaces
	settings.Workspace = strings.TrimSpace(settings.Workspace)
	settings.Model = strings.TrimSpace(settings.Model)
	settings.ModelProvider = "" // Codex-only: never persist Claude/Gemini/Grok workbench providers
	settings.CustomModels = sanitizeCustomModels(settings.CustomModels)
	settings.Effort = strings.TrimSpace(settings.Effort)
	if !isAllowed(settings.Theme, "dark", "light", "system") {
		return UserSettings{}, errors.New("invalid theme")
	}
	if !isAllowed(settings.AccentColor, "codex", "amber", "gold", "rose", "coral", "emerald", "moss", "ocean", "sky", "slate", "graphite") {
		return UserSettings{}, errors.New("invalid accent color")
	}
	if !isValidFontFamily(settings.FontFamily) {
		return UserSettings{}, errors.New("invalid font family")
	}
	if !isValidTerminalProfile(settings.TerminalProfile) {
		return UserSettings{}, errors.New("invalid terminal profile")
	}
	if !isAllowed(settings.Sandbox, "read-only", "workspace-write", "danger-full-access") {
		return UserSettings{}, errors.New("invalid sandbox mode")
	}
	if !isAllowed(settings.ApprovalPolicy, "untrusted", "on-request", "never") {
		return UserSettings{}, errors.New("invalid approval policy")
	}
	if !isAllowed(settings.Language, "zh-CN", "en-US") {
		return UserSettings{}, errors.New("invalid language")
	}
	settings.CollaborationMode = strings.TrimSpace(settings.CollaborationMode)
	if settings.CollaborationMode == "" {
		settings.CollaborationMode = "default"
	}
	if len(settings.CollaborationMode) > 64 {
		return UserSettings{}, errors.New("invalid collaboration mode")
	}
	if !isAllowed(settings.Personality, "none", "friendly", "pragmatic") {
		return UserSettings{}, errors.New("invalid personality")
	}
	if !isAllowed(settings.MultiAgentMode, "explicitRequestOnly", "proactive") {
		return UserSettings{}, errors.New("invalid multi-agent mode")
	}
	settings.FollowUpBehavior = normalizeFollowUpBehavior(settings.FollowUpBehavior)
	settings.UiFontSize = normalizeUiFontSize(settings.UiFontSize)
	settings.CodeFontSize = normalizeCodeFontSize(settings.CodeFontSize)
	settings.GitBranchPrefix = sanitizeShortText(settings.GitBranchPrefix, 64)
	settings.GitCommitPrefix = sanitizeShortText(settings.GitCommitPrefix, 64)
	settings.GitPRBodyTemplate = sanitizeCustomInstructions(settings.GitPRBodyTemplate)
	settings.BrowserDownloadDir = strings.TrimSpace(settings.BrowserDownloadDir)
	if len(settings.BrowserDownloadDir) > 512 {
		return UserSettings{}, errors.New("download directory is too long")
	}
	settings.BrowserAllowedHosts = sanitizeHostList(settings.BrowserAllowedHosts)
	settings.BrowserBlockedHosts = sanitizeHostList(settings.BrowserBlockedHosts)
	settings.WorkMode = normalizeWorkMode(settings.WorkMode)
	settings.ShortcutCommandPalette = normalizeShortcutBinding(settings.ShortcutCommandPalette, "Ctrl+K")
	settings.ShortcutNewThread = normalizeShortcutBinding(settings.ShortcutNewThread, "Ctrl+N")
	settings.ShortcutTerminal = normalizeShortcutBinding(settings.ShortcutTerminal, "Ctrl+`")
	settings.ShortcutBrowser = normalizeShortcutBinding(settings.ShortcutBrowser, "Ctrl+Shift+B")
	if settings.Effort == "" {
		settings.Effort = "high"
	}
	settings.ServiceTier = strings.TrimSpace(settings.ServiceTier)
	if len(settings.Model) > 160 || len(settings.ModelProvider) > 160 || len(settings.Effort) > 64 || len(settings.ServiceTier) > 64 {
		return UserSettings{}, errors.New("model preferences are too long")
	}
	// Disk AGENTS.md is only mutated by SaveGlobalInstructions — never wipe it from generic preference saves.
	settings.CustomInstructions = readCodexPersonalInstructions()
	// Onboarding completion is monotonic — concurrent preference saves must not reopen the wizard.
	if current.OnboardingCompleted || settings.OnboardingCompleted || strings.TrimSpace(settings.Workspace) != "" {
		settings.OnboardingCompleted = true
	}
	flags := readCodexFeatureFlags()
	flags.BrowserUseFullCDP = settings.BrowserFullCDP
	_ = writeCodexFeatureFlags(flags)
	s.mu.Lock()
	err := writeSettings(s.settingsPath, settings)
	if err == nil {
		s.settings = cloneSettings(settings)
	}
	result := cloneSettings(settings)
	s.mu.Unlock()
	if err == nil {
		s.applyAlwaysOnTop(result.AlwaysOnTop)
		if !result.PreventSleepWhileRunning {
			setSystemSleepPrevention(false)
		}
	}
	return result, err
}

func (s *AppService) SelectWorkspace() (WorkspaceInfo, error) {
	current := s.Settings().Workspace
	path, err := s.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                "Choose a workspace",
		Message:              "Select the project folder Nice Codex can work in.",
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
	return s.UseWorkspace(path)
}

func (s *AppService) SelectImages() ([]string, error) {
	paths, err := s.app.Dialog.OpenFile().
		SetTitle("Attach images to this message").
		CanChooseFiles(true).
		AddFilter("Images", "*.png;*.jpg;*.jpeg;*.webp;*.gif").
		PromptForMultipleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return []string{}, nil
		}
		return nil, err
	}
	if len(paths) == 0 {
		return []string{}, nil
	}
	if len(paths) > 4 {
		return nil, errors.New("attach up to 4 images per message")
	}

	result := make([]string, 0, len(paths))
	for _, path := range paths {
		cleanPath, err := validateImageAttachment(path)
		if err != nil {
			return nil, err
		}
		result = append(result, cleanPath)
	}
	s.mu.Lock()
	for _, path := range result {
		s.allowedImages[imageAttachmentKey(path)] = struct{}{}
	}
	s.mu.Unlock()
	return result, nil
}

// AttachImageData saves a pasted/dropped image into the NiceCodex temp folder
// and registers it for SendMessage (same allow-list as SelectImages).
func (s *AppService) AttachImageData(fileName string, mimeType string, dataBase64 string) (string, error) {
	dataBase64 = strings.TrimSpace(dataBase64)
	if dataBase64 == "" {
		return "", errors.New("image data is required")
	}
	if strings.Contains(dataBase64, ",") {
		// Accept data URL payloads: data:image/png;base64,....
		dataBase64 = dataBase64[strings.LastIndex(dataBase64, ",")+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		// Some paste paths use URL-safe / raw encodings.
		raw, err = base64.RawStdEncoding.DecodeString(dataBase64)
		if err != nil {
			return "", errors.New("invalid image data encoding")
		}
	}
	if len(raw) == 0 {
		return "", errors.New("image data is empty")
	}
	if len(raw) > 20*1024*1024 {
		return "", errors.New("image attachments must be 20 MB or smaller")
	}

	ext := imageExtensionForMime(mimeType, fileName, raw)
	if ext == "" {
		return "", errors.New("unsupported image format")
	}
	dir := filepath.Join(filepath.Dir(s.settingsPath), "attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	safeName := sanitizeAttachmentFileName(fileName, ext)
	path := filepath.Join(dir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), safeName))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	cleanPath, err := validateImageAttachment(path)
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	s.mu.Lock()
	s.allowedImages[imageAttachmentKey(cleanPath)] = struct{}{}
	s.mu.Unlock()
	return cleanPath, nil
}

func (s *AppService) UseWorkspace(path string) (WorkspaceInfo, error) {
	cleanPath, err := validateWorkspace(path)
	if err != nil {
		return WorkspaceInfo{}, err
	}

	s.mu.Lock()
	updated := cloneSettings(s.settings)
	updated.Workspace = cleanPath
	updated.RecentWorkspaces = prependWorkspace(updated.RecentWorkspaces, cleanPath)
	err = writeSettings(s.settingsPath, updated)
	if err == nil {
		s.settings = updated
	}
	s.mu.Unlock()
	if err != nil {
		return WorkspaceInfo{}, err
	}
	return inspectWorkspace(cleanPath), nil
}

func (s *AppService) RefreshWorkspace() (WorkspaceInfo, error) {
	workspace := s.Settings().Workspace
	if workspace == "" {
		return WorkspaceInfo{}, errors.New("no workspace is selected")
	}
	return inspectWorkspace(workspace), nil
}

func (s *AppService) StartCodex(workspace string) error {
	cleanPath, err := validateWorkspace(workspace)
	if err != nil {
		return err
	}

	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return errors.New("Codex client is not initialized")
	}
	status := client.Status()
	if status.Running && filepath.Clean(status.Workspace) != cleanPath {
		if err := client.Stop(); err != nil {
			return err
		}
	}
	return client.Start(s.app.Context(), cleanPath)
}

func (s *AppService) StopCodex() error {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.Stop()
}

func (s *AppService) CodexStatus() codex.Status {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return codex.Status{State: "disconnected", Message: "Codex client is not initialized"}
	}
	return client.Status()
}

func (s *AppService) ListThreads(search string) (map[string]any, error) {
	settings := s.Settings()
	return s.listThreadsForWorkspace(settings.Workspace, search)
}

func (s *AppService) ListWorkspaceThreads(workspace string, search string) (map[string]any, error) {
	cleanWorkspace, err := validateWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	settings := s.Settings()
	allowed := samePath(cleanWorkspace, settings.Workspace)
	if !allowed {
		for _, recent := range settings.RecentWorkspaces {
			if samePath(cleanWorkspace, recent) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return nil, errors.New("workspace is not in the recent workspace list")
	}
	return s.listThreadsForWorkspace(cleanWorkspace, search)
}

func (s *AppService) listThreadsForWorkspace(workspace string, search string) (map[string]any, error) {
	settings := s.Settings()
	workMode := normalizeWorkMode(settings.WorkMode)
	// Sync Codex app-server history into the NiceCodex index so the sidebar
	// shows real past threads (names/previews), not just empty local stubs.
	// useStateDbOnly keeps this fast; a timeout must not block the local index.
	if response, err := s.callWithTimeout("thread/list", map[string]any{
		"cwd":            workspace,
		"limit":          100,
		"archived":       false,
		"sortKey":        "updated_at",
		"sortDirection":  "desc",
		"useStateDbOnly": true,
	}, 12*time.Second); err == nil {
		s.syncCodexThreadsIntoSessions(response, workspace, workMode)
	}
	return s.listSessionsForWorkspace(workspace, search, workMode), nil
}

func (s *AppService) UpdateSessionPreferences(request SessionPreferencesRequest) error {
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	if strings.HasPrefix(sessionID, "pending-thread-") {
		return nil
	}
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return err
	}
	model := strings.TrimSpace(request.Model)
	effort := strings.TrimSpace(request.Effort)
	collaborationMode := normalizeCollaborationMode(request.CollaborationMode)
	if len(model) > 160 || len(effort) > 64 {
		return errors.New("session preferences are too long")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil || record.Archived || !samePath(record.Workspace, workspace) {
		return errors.New("session not found in the current workspace")
	}
	if model != "" {
		record.Model = model
	}
	if effort != "" {
		record.Effort = effort
	}
	if collaborationMode != "" {
		prev := normalizeCollaborationMode(record.CollaborationMode)
		record.CollaborationMode = collaborationMode
		if collaborationMode == "plan" {
			record.HadPlan = true
		}
		if collaborationMode == "default" {
			if prev == "plan" {
				record.HadPlan = true
			}
			// Always bump on Default selection so a stuck Plan context can be
			// cleared by re-selecting 执行模式 (Codex only emits on inequality).
			record.CollabResetNonce++
			if record.CollabResetNonce <= 0 {
				record.CollabResetNonce = 1
			}
		}
	}
	record.UpdatedAt = time.Now().Unix()
	s.persistSessionsLocked()
	return nil
}

func (s *AppService) CreateThread() (map[string]any, error) {
	settings := s.Settings()
	workspace, err := validateWorkspace(settings.Workspace)
	if err != nil {
		return nil, err
	}
	workMode := normalizeWorkMode(settings.WorkMode)
	collaborationMode := strings.TrimSpace(settings.CollaborationMode)
	if collaborationMode == "" {
		collaborationMode = "default"
	}
	if workMode == "cowork" && collaborationMode == "default" {
		collaborationMode = "plan"
	}

	// Codex-only: every workbench session is NiceCodex-owned Codex.
	// App-server threads are allocated lazily on the first send (BackendRef).
	record := s.createSessionRecord(workspace, "", "", settings.Model, settings.Effort, collaborationMode, workMode)
	s.mu.Lock()
	s.upsertSessionLocked(record)
	s.mu.Unlock()
	s.rememberThread(record.ID, workspace)
	return s.sessionResponse(record), nil
}

func (s *AppService) ResumeThread(threadID string) (map[string]any, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, errors.New("thread id is required")
	}
	settings := s.Settings()
	workspace := settings.Workspace
	session := s.sessionFor(threadID, workspace)
	if session != nil && isExternalSession(session) {
		return nil, errors.New("NiceCodex is Codex-only; create a new Codex session to continue")
	}
	// NiceCodex Codex session that has not started an app-server thread yet.
	if session != nil && session.BackendRef == "" {
		s.rememberThread(threadID, workspace)
		return s.sessionResponse(session), nil
	}
	backendID := s.codexBackendID(threadID, workspace)
	if err := s.ensureThreadInWorkspace(backendID, workspace); err != nil {
		// Fall back to local index if the backend thread is gone.
		if session != nil {
			s.rememberThread(threadID, workspace)
			return s.sessionResponse(session), nil
		}
		return nil, err
	}
	params := map[string]any{
		"threadId":       backendID,
		"cwd":            workspace,
		"sandbox":        normalizeSandbox(settings.Sandbox),
		"approvalPolicy": normalizeApproval(settings.ApprovalPolicy),
	}
	model := settings.Model
	providerID := settings.ModelProvider
	if session != nil {
		if session.Model != "" {
			model = session.Model
		}
		providerID = session.ProviderID
	}
	if externalProviderKind(providerID) == "" && model != "" {
		params["model"] = model
	}
	if externalProviderKind(providerID) == "" && providerID != "" {
		params["modelProvider"] = providerID
	}
	result, err := s.call("thread/resume", params)
	if err == nil {
		s.rememberThread(threadID, workspace)
		s.attachSessionIdentity(result, session, threadID)
	}
	return result, err
}

func (s *AppService) ForkThread(threadID string) (map[string]any, error) {
	threadID = strings.TrimSpace(threadID)
	settings := s.Settings()
	workspace, err := validateWorkspace(settings.Workspace)
	if err != nil {
		return nil, err
	}
	source := s.sessionFor(threadID, workspace)
	if source != nil && isExternalSession(source) {
		return nil, errors.New("NiceCodex is Codex-only; create a new Codex session to continue")
	}
	// NiceCodex-owned fork: keep our UUID as the directory id.
	if source != nil && source.BackendRef == "" {
		return s.forkExternalSession(source)
	}
	backendID := s.codexBackendID(threadID, workspace)
	if backendID == "" {
		return nil, errors.New("session not found")
	}
	if err := s.ensureThreadInWorkspace(backendID, workspace); err != nil {
		return nil, err
	}
	result, err := s.call("thread/fork", map[string]any{"threadId": backendID, "cwd": workspace})
	if err != nil {
		return nil, err
	}
	forkedBackendID := threadIDFromResult(result)
	if forkedBackendID == "" {
		return result, nil
	}
	now := time.Now().Unix()
	record := &SessionRecord{
		ID: newUUID(), Workspace: workspace, BackendRef: forkedBackendID,
		ProviderID: "", WorkMode: normalizeWorkMode(settings.WorkMode),
		Name: "New task", CreatedAt: now, UpdatedAt: now,
	}
	if source != nil {
		record.Model = source.Model
		record.ProviderID = source.ProviderID
		record.Effort = source.Effort
		record.CollaborationMode = source.CollaborationMode
		record.WorkMode = source.WorkMode
		record.Name = source.Name + " (fork)"
	}
	s.mu.Lock()
	s.upsertSessionLocked(record)
	s.mu.Unlock()
	s.rememberThread(record.ID, workspace)
	return s.sessionResponse(record), nil
}

func (s *AppService) ArchiveThread(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	workspace := s.Settings().Workspace
	session := s.sessionFor(threadID, workspace)
	// Local directory is authoritative.
	s.markSessionArchived(threadID)
	if session == nil || isExternalSession(session) || session.BackendRef == "" {
		return nil
	}
	backendID := session.BackendRef
	if err := s.ensureThreadInWorkspace(backendID, workspace); err != nil {
		return nil
	}
	_, _ = s.call("thread/archive", map[string]any{"threadId": backendID})
	return nil
}

func (s *AppService) UnarchiveThread(threadID string) (map[string]any, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("thread id is required")
	}
	workspace := s.Settings().Workspace
	session := s.sessionForAny(threadID, workspace)
	if session == nil {
		return nil, errors.New("session not found")
	}
	if isExternalSession(session) {
		return nil, errors.New("NiceCodex is Codex-only; create a new Codex session to continue")
	}
	restored := s.markSessionUnarchived(threadID)
	if restored == nil {
		return nil, errors.New("session not found")
	}
	if restored.BackendRef != "" {
		if err := s.ensureThreadInWorkspace(restored.BackendRef, workspace); err == nil {
			_, _ = s.call("thread/unarchive", map[string]any{"threadId": restored.BackendRef})
		}
	}
	s.rememberThread(restored.ID, workspace)
	return s.sessionResponse(restored), nil
}

func (s *AppService) DeleteThread(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("thread id is required")
	}
	workspace := s.Settings().Workspace
	session := s.sessionForAny(threadID, workspace)
	deleted := s.deleteSession(threadID)
	if deleted == nil && session == nil {
		return errors.New("session not found")
	}
	if session != nil && !isExternalSession(session) && session.BackendRef != "" {
		if err := s.ensureThreadInWorkspace(session.BackendRef, workspace); err == nil {
			_, _ = s.call("thread/delete", map[string]any{"threadId": session.BackendRef})
		}
	}
	s.mu.Lock()
	delete(s.allowedThreads, threadID)
	if session != nil && session.BackendRef != "" {
		delete(s.allowedThreads, session.BackendRef)
	}
	s.mu.Unlock()
	return nil
}

func (s *AppService) SetThreadName(threadID string, name string) (map[string]any, error) {
	threadID = strings.TrimSpace(threadID)
	name = truncateRunes(strings.TrimSpace(name), 80)
	if threadID == "" {
		return nil, errors.New("thread id is required")
	}
	if name == "" {
		return nil, errors.New("thread name is required")
	}
	workspace := s.Settings().Workspace
	session := s.sessionFor(threadID, workspace)
	if session == nil {
		return nil, errors.New("session not found")
	}
	if isExternalSession(session) {
		return nil, errors.New("NiceCodex is Codex-only; create a new Codex session to continue")
	}
	updated := s.renameSession(threadID, name)
	if updated == nil {
		return nil, errors.New("session not found")
	}
	if updated.BackendRef != "" {
		if err := s.ensureThreadInWorkspace(updated.BackendRef, workspace); err == nil {
			_, _ = s.call("thread/name/set", map[string]any{
				"threadId": updated.BackendRef,
				"name":     name,
			})
		}
	}
	s.rememberThread(updated.ID, workspace)
	return s.sessionResponse(updated), nil
}

func (s *AppService) StartReview(request ReviewStartRequest) (map[string]any, error) {
	threadID := strings.TrimSpace(request.ThreadID)
	if threadID == "" {
		return nil, errors.New("thread id is required")
	}
	workspace := s.Settings().Workspace
	session := s.sessionFor(threadID, workspace)
	if session != nil && isExternalSession(session) {
		return nil, errors.New("NiceCodex is Codex-only; create a new Codex session to continue")
	}
	backendID := s.codexBackendID(threadID, workspace)
	if backendID == "" || (session != nil && session.BackendRef == "") {
		return nil, errors.New("start a conversation turn before reviewing changes")
	}
	if err := s.ensureThreadInWorkspace(backendID, workspace); err != nil {
		return nil, err
	}

	targetType := strings.TrimSpace(request.TargetType)
	if targetType == "" {
		targetType = "uncommittedChanges"
	}
	var target map[string]any
	switch targetType {
	case "uncommittedChanges":
		target = map[string]any{"type": "uncommittedChanges"}
	case "baseBranch":
		branch := strings.TrimSpace(request.Branch)
		if branch == "" {
			return nil, errors.New("base branch is required")
		}
		target = map[string]any{"type": "baseBranch", "branch": branch}
	case "custom":
		instructions := strings.TrimSpace(request.Instructions)
		if instructions == "" {
			return nil, errors.New("review instructions are required")
		}
		target = map[string]any{"type": "custom", "instructions": instructions}
	default:
		return nil, errors.New("unsupported review target")
	}

	delivery := strings.TrimSpace(request.Delivery)
	if delivery == "" {
		delivery = "inline"
	}
	if delivery != "inline" && delivery != "detached" {
		return nil, errors.New("review delivery must be inline or detached")
	}

	params := map[string]any{
		"threadId": backendID,
		"target":   target,
		"delivery": delivery,
	}
	result, err := s.call("review/start", params)
	if err != nil {
		return nil, err
	}

	// Detached reviews allocate a new Codex thread — mirror it into NiceCodex sessions.
	if delivery == "detached" {
		reviewBackendID, _ := result["reviewThreadId"].(string)
		reviewBackendID = strings.TrimSpace(reviewBackendID)
		if reviewBackendID != "" && reviewBackendID != backendID {
			now := time.Now().Unix()
			record := &SessionRecord{
				ID: newUUID(), Workspace: workspace, BackendRef: reviewBackendID,
				ProviderID: "", WorkMode: normalizeWorkMode(s.Settings().WorkMode),
				Name: "Review", CreatedAt: now, UpdatedAt: now,
			}
			if session != nil {
				record.Model = session.Model
				record.Effort = session.Effort
				record.CollaborationMode = session.CollaborationMode
				record.WorkMode = session.WorkMode
				if session.Name != "" {
					record.Name = session.Name + " (review)"
				}
			}
			s.mu.Lock()
			s.upsertSessionLocked(record)
			s.mu.Unlock()
			s.rememberThread(record.ID, workspace)
			s.rememberThread(reviewBackendID, workspace)
			result["reviewThreadId"] = record.ID
			s.attachSessionIdentity(result, record, record.ID)
			return result, nil
		}
	}
	s.attachSessionIdentity(result, session, threadID)
	return result, nil
}

func (s *AppService) ListArchivedThreads(search string) (map[string]any, error) {
	settings := s.Settings()
	workspace, err := validateWorkspace(settings.Workspace)
	if err != nil {
		return map[string]any{"data": []any{}}, nil
	}
	return s.listArchivedSessionsForWorkspace(workspace, search, settings.WorkMode), nil
}

func (s *AppService) CompactThread(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	workspace := s.Settings().Workspace
	if session := s.sessionFor(threadID, workspace); session != nil && isExternalSession(session) {
		return errors.New("NiceCodex is Codex-only; create a new Codex session to continue")
	}
	backendID := s.codexBackendID(threadID, workspace)
	if backendID == "" {
		return errors.New("this session has not started a Codex thread yet")
	}
	if err := s.ensureThreadInWorkspace(backendID, workspace); err != nil {
		return err
	}
	_, err := s.call("thread/compact/start", map[string]any{"threadId": backendID})
	return err
}

func (s *AppService) RollbackThread(threadID string, numTurns int) (map[string]any, error) {
	threadID = strings.TrimSpace(threadID)
	if numTurns < 1 || numTurns > 1000 {
		return nil, errors.New("rollback turn count must be between 1 and 1000")
	}
	workspace := s.Settings().Workspace
	if session := s.sessionFor(threadID, workspace); session != nil && isExternalSession(session) {
		return nil, errors.New("NiceCodex is Codex-only; create a new Codex session to continue")
	}
	backendID := s.codexBackendID(threadID, workspace)
	if backendID == "" {
		return nil, errors.New("this session has not started a Codex thread yet")
	}
	if err := s.ensureThreadInWorkspace(backendID, workspace); err != nil {
		return nil, err
	}
	return s.call("thread/rollback", map[string]any{"threadId": backendID, "numTurns": numTurns})
}

func (s *AppService) ReadThread(threadID string) (map[string]any, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, errors.New("thread id is required")
	}
	workspace := s.Settings().Workspace
	session := s.sessionFor(threadID, workspace)
	if session != nil && isExternalSession(session) {
		s.rememberThread(threadID, workspace)
		return s.sessionResponse(session), nil
	}
	if session != nil && session.BackendRef == "" {
		s.rememberThread(threadID, workspace)
		return s.sessionResponse(session), nil
	}
	backendID := s.codexBackendID(threadID, workspace)
	if err := s.ensureThreadInWorkspace(backendID, workspace); err != nil {
		if session != nil {
			s.rememberThread(threadID, workspace)
			return s.sessionResponse(session), nil
		}
		return nil, err
	}
	result, err := s.call("thread/read", map[string]any{"threadId": backendID, "includeTurns": true})
	if err != nil {
		return nil, err
	}
	s.attachSessionIdentity(result, session, threadID)
	s.rememberThread(threadID, workspace)
	return result, nil
}

func (s *AppService) SendMessage(request SendMessageRequest) (map[string]any, error) {
	request.ThreadID = strings.TrimSpace(request.ThreadID)
	if request.ThreadID == "" {
		return nil, errors.New("thread id is required")
	}
	settings := s.Settings()
	workspace, err := validateWorkspace(settings.Workspace)
	if err != nil {
		return nil, err
	}
	if !s.threadAllowed(request.ThreadID, workspace) {
		// Allow NiceCodex sessions from the local index even before rememberThread.
		if session := s.sessionFor(request.ThreadID, workspace); session != nil {
			s.rememberThread(request.ThreadID, workspace)
		} else {
			return nil, errors.New("open this thread in the current workspace before sending a message")
		}
	}

	session := s.sessionFor(request.ThreadID, workspace)
	if session != nil && isExternalSession(session) {
		return nil, errors.New("NiceCodex is Codex-only; create a new Codex session to continue")
	}

	backendID := request.ThreadID
	if session != nil {
		ensured, err := s.ensureCodexBackendThread(session, settings, workspace)
		if err != nil {
			return nil, err
		}
		backendID = ensured
	} else if ref := s.codexBackendID(request.ThreadID, workspace); ref != "" {
		backendID = ref
	}

	input, err := s.buildUserInput(request.Text, request.Images)
	if err != nil {
		return nil, err
	}

	model := settings.Model
	effort := settings.Effort
	collaborationMode := settings.CollaborationMode
	if session != nil {
		if session.Model != "" {
			model = session.Model
		}
		if session.Effort != "" {
			effort = session.Effort
		}
		if session.CollaborationMode != "" {
			collaborationMode = session.CollaborationMode
		}
	}
	// Client-supplied mode wins (UI toggle / "Implement this plan?").
	if override := normalizeCollaborationMode(request.CollaborationMode); override != "" {
		collaborationMode = override
		if session != nil {
			s.mu.Lock()
			if record := s.sessions[session.ID]; record != nil {
				prev := normalizeCollaborationMode(record.CollaborationMode)
				record.CollaborationMode = override
				if override == "plan" {
					record.HadPlan = true
				}
				// Transitioning Plan→Default on this turn (e.g. Implement click without prefs write).
				if override == "default" && prev == "plan" {
					record.HadPlan = true
					record.CollabResetNonce++
					if record.CollabResetNonce <= 0 {
						record.CollabResetNonce = 1
					}
				}
				record.UpdatedAt = time.Now().Unix()
				s.persistSessionsLocked()
				session = cloneSession(record)
			}
			s.mu.Unlock()
		}
	}
	collaborationMode = normalizeCollaborationMode(collaborationMode)
	if collaborationMode == "" {
		collaborationMode = "default"
	}

	params := map[string]any{
		"threadId":       backendID,
		"input":          input,
		"cwd":            workspace,
		"approvalPolicy": normalizeApproval(settings.ApprovalPolicy),
		"sandboxPolicy":  sandboxPolicy(settings.Sandbox, workspace),
	}
	if model = strings.TrimSpace(model); model != "" {
		params["model"] = model
	}
	if effort = strings.TrimSpace(effort); effort != "" {
		params["effort"] = effort
	}
	params["summary"] = "detailed"
	if serviceTier := strings.TrimSpace(settings.ServiceTier); serviceTier != "" {
		params["serviceTier"] = serviceTier
	}
	if personality := strings.TrimSpace(settings.Personality); personality != "" {
		params["personality"] = personality
	}
	if mode := strings.TrimSpace(settings.MultiAgentMode); mode != "" {
		params["multiAgentMode"] = mode
	}
	// Always send collaborationMode on every turn so UI toggles take effect.
	// Codex core only injects a mode developer message when the CollaborationMode
	// object changes AND developer_instructions is non-empty (null/empty = no reset).
	if model == "" {
		model = "gpt-5.4"
	}
	resetNonce := 0
	if session != nil {
		resetNonce = session.CollabResetNonce
	}
	developerInstructions := collaborationModeDeveloperInstructions(collaborationMode, resetNonce)
	if guidance := sessionMemoryGuidance(session); guidance != "" {
		if text, ok := developerInstructions.(string); ok && strings.TrimSpace(text) != "" {
			developerInstructions = strings.TrimSpace(text) + "\n\n" + guidance
		} else {
			developerInstructions = guidance
		}
	}
	collabSettings := map[string]any{
		"model":                  model,
		"developer_instructions": developerInstructions,
	}
	if effort != "" {
		collabSettings["reasoning_effort"] = effort
	}
	params["collaborationMode"] = map[string]any{
		"mode":     collaborationMode,
		"settings": collabSettings,
	}
	result, err := s.call("turn/start", params)
	if err != nil {
		return nil, err
	}
	s.touchSessionPreview(request.ThreadID, request.Text)
	return result, nil
}

func (s *AppService) SteerTurn(request SteerTurnRequest) (map[string]any, error) {
	request.ThreadID = strings.TrimSpace(request.ThreadID)
	request.TurnID = strings.TrimSpace(request.TurnID)
	if request.ThreadID == "" || request.TurnID == "" {
		return nil, errors.New("thread id and active turn id are required")
	}
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return nil, err
	}
	if !s.threadAllowed(request.ThreadID, workspace) {
		return nil, errors.New("open this thread in the current workspace before steering the turn")
	}
	if session := s.sessionFor(request.ThreadID, workspace); session != nil && isExternalSession(session) {
		return nil, errors.New("steer is only available for Codex sessions; message will be queued instead")
	}
	backendID := s.codexBackendID(request.ThreadID, workspace)
	input, err := s.buildUserInput(request.Text, request.Images)
	if err != nil {
		return nil, err
	}
	return s.call("turn/steer", map[string]any{
		"threadId":       backendID,
		"expectedTurnId": request.TurnID,
		"input":          input,
	})
}

func (s *AppService) buildUserInput(text string, images []string) ([]any, error) {
	text = strings.TrimSpace(text)
	if len(images) > 4 {
		return nil, errors.New("attach up to 4 images per message")
	}

	input := make([]any, 0, len(images)+1)
	seenImages := make(map[string]struct{}, len(images))
	for _, path := range images {
		cleanPath, err := validateImageAttachment(path)
		if err != nil {
			return nil, err
		}
		key := imageAttachmentKey(cleanPath)
		if _, duplicate := seenImages[key]; duplicate {
			continue
		}
		s.mu.Lock()
		_, allowed := s.allowedImages[key]
		s.mu.Unlock()
		if !allowed {
			return nil, errors.New("select image attachments through Nice Codex before sending")
		}
		seenImages[key] = struct{}{}
		input = append(input, map[string]any{"type": "localImage", "path": cleanPath})
	}
	if text == "" && len(input) == 0 {
		return nil, errors.New("message cannot be empty")
	}
	if text != "" {
		input = append(input, map[string]any{"type": "text", "text": text, "text_elements": []any{}})
	}
	return input, nil
}

// PreviewImage returns a data-URL for an allow-listed attachment so the UI can show thumbnails.
func (s *AppService) PreviewImage(path string) (string, error) {
	cleanPath, err := validateImageAttachment(path)
	if err != nil {
		return "", err
	}
	key := imageAttachmentKey(cleanPath)
	s.mu.Lock()
	_, allowed := s.allowedImages[key]
	s.mu.Unlock()
	if !allowed {
		return "", errors.New("select image attachments through Nice Codex before previewing")
	}
	raw, err := os.ReadFile(cleanPath)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("image data is empty")
	}
	mime := mimeFromImageExt(filepath.Ext(cleanPath))
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func (s *AppService) InterruptTurn(threadID string, turnID string) error {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return errors.New("thread id and turn id are required")
	}
	if s.interruptExternalTurn(threadID, turnID) {
		return nil
	}
	backendID := s.resolveInterruptBackendID(threadID)
	if backendID == "" {
		return errors.New("Codex thread is not ready to interrupt")
	}
	// Interrupt should return quickly; turn/completed arrives asynchronously.
	_, err := s.callWithTimeout("turn/interrupt", map[string]any{
		"threadId": backendID,
		"turnId":   turnID,
	}, 8*time.Second)
	return err
}

// resolveInterruptBackendID maps NiceCodex session ids to Codex app-server thread ids.
// Unlike codexBackendID, it never returns an empty BackendRef as a valid id.
func (s *AppService) resolveInterruptBackendID(threadID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.sessions[threadID]; record != nil && !record.Archived {
		if ref := strings.TrimSpace(record.BackendRef); ref != "" {
			return ref
		}
		return ""
	}
	return threadID
}

func (s *AppService) ListModels() (map[string]any, error) {
	configured := readCodexConfiguredModel()
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return ensureConfiguredModelInList(map[string]any{"data": []any{}}, configured), nil
	}

	merged := make([]any, 0, 64)
	var cursor any
	for page := 0; page < 8; page++ {
		params := map[string]any{"limit": 100, "includeHidden": true}
		if cursor != nil {
			params["cursor"] = cursor
		}
		result, err := s.call("model/list", params)
		if err != nil {
			if len(merged) == 0 {
				return ensureConfiguredModelInList(map[string]any{"data": []any{}}, configured), nil
			}
			break
		}
		chunk, _ := result["data"].([]any)
		merged = append(merged, chunk...)
		next := result["nextCursor"]
		if next == nil || next == "" {
			break
		}
		cursor = next
	}
	return ensureConfiguredModelInList(map[string]any{"data": merged}, configured), nil
}

func readCodexConfiguredModel() string {
	codexHome := resolveCodexHome()
	if codexHome == "" {
		return ""
	}
	return readTOMLModel(filepath.Join(codexHome, "config.toml"))
}

func ensureConfiguredModelInList(result map[string]any, configured string) map[string]any {
	if result == nil {
		result = map[string]any{}
	}
	configured = strings.TrimSpace(configured)
	data, _ := result["data"].([]any)
	if data == nil {
		data = []any{}
	}
	if configured == "" {
		result["data"] = data
		return result
	}
	hasDefault := false
	for _, item := range data {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(fmt.Sprint(entry["model"]))
		if id == "" || id == "<nil>" {
			id = strings.TrimSpace(fmt.Sprint(entry["id"]))
		}
		if strings.EqualFold(id, configured) {
			result["data"] = data
			return result
		}
		if entry["isDefault"] == true {
			hasDefault = true
		}
	}
	defaultEffort := "high"
	if strings.Contains(strings.ToLower(configured), "sol") {
		defaultEffort = "low"
	}
	stub := map[string]any{
		"id":                     configured,
		"model":                  configured,
		"displayName":            configured,
		"description":            "Configured in Codex config.toml",
		"hidden":                 false,
		"isDefault":              !hasDefault,
		"defaultReasoningEffort": defaultEffort,
		"supportedReasoningEfforts": []any{
			map[string]any{"reasoningEffort": "low", "effort": "low", "description": "Fast responses with lighter reasoning"},
			map[string]any{"reasoningEffort": "medium", "effort": "medium", "description": "Balanced speed and depth"},
			map[string]any{"reasoningEffort": "high", "effort": "high", "description": "Deeper reasoning for complex work"},
			map[string]any{"reasoningEffort": "xhigh", "effort": "xhigh", "description": "Extra-high reasoning depth"},
			map[string]any{"reasoningEffort": "max", "effort": "max", "description": "Maximum reasoning for hard problems"},
			map[string]any{"reasoningEffort": "ultra", "effort": "ultra", "description": "Ultra reasoning depth"},
		},
		"serviceTiers":         []any{},
		"additionalSpeedTiers": []any{},
		"inputModalities":      []any{"text"},
		"supportsPersonality":  false,
		"defaultServiceTier":   nil,
		"upgrade":              nil,
		"upgradeInfo":          nil,
		"availabilityNux":      nil,
	}
	result["data"] = append([]any{stub}, data...)
	return result
}

func (s *AppService) ListPlugins() (map[string]any, error) {
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return nil, err
	}
	return s.call("plugin/list", map[string]any{"cwds": []string{workspace}})
}

func (s *AppService) InstallPlugin(request PluginInstallRequest) (map[string]any, error) {
	name := strings.TrimSpace(request.PluginName)
	if name == "" || len(name) > 180 {
		return nil, errors.New("a valid plugin name is required")
	}
	params := map[string]any{"pluginName": name}
	if path := strings.TrimSpace(request.MarketplacePath); path != "" {
		params["marketplacePath"] = path
	}
	if remote := strings.TrimSpace(request.RemoteMarketplaceName); remote != "" {
		params["remoteMarketplaceName"] = remote
	}
	return s.call("plugin/install", params)
}

func (s *AppService) UninstallPlugin(pluginID string) error {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" || len(pluginID) > 220 {
		return errors.New("a valid plugin id is required")
	}
	_, err := s.call("plugin/uninstall", map[string]any{"pluginId": pluginID})
	return err
}

func (s *AppService) ListSkills() (map[string]any, error) {
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return nil, err
	}
	return s.call("skills/list", map[string]any{"cwds": []string{workspace}, "forceReload": true})
}

func (s *AppService) SetSkillEnabled(request SkillConfigRequest) error {
	name := strings.TrimSpace(request.Name)
	path := strings.TrimSpace(request.Path)
	if name == "" && path == "" {
		return errors.New("skill name or path is required")
	}
	// Official examples use { path|name, enabled }. Some docs also mention absolutePath/config.
	params := map[string]any{"enabled": request.Enabled}
	if name != "" {
		params["name"] = name
	}
	if path != "" {
		params["path"] = path
		params["absolutePath"] = path
	}
	params["config"] = map[string]any{"enabled": request.Enabled}
	_, err := s.call("skills/config/write", params)
	return err
}

func (s *AppService) ListApps() (map[string]any, error) {
	return s.call("app/list", map[string]any{"forceRefetch": true, "limit": 100})
}

func (s *AppService) ListMCPServers() (map[string]any, error) {
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return nil, err
	}
	response, err := s.call("config/read", map[string]any{
		"cwd":           workspace,
		"includeLayers": false,
	})
	if err != nil {
		return nil, err
	}
	config, _ := response["config"].(map[string]any)
	servers, _ := config["mcp_servers"].(map[string]any)
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	data := make([]any, 0, len(names))
	for _, name := range names {
		enabled := true
		command := ""
		url := ""
		transport := ""
		args := []any{}
		if server, ok := servers[name].(map[string]any); ok {
			if server["enabled"] == false {
				enabled = false
			}
			command, _ = server["command"].(string)
			url, _ = server["url"].(string)
			transport, _ = server["type"].(string)
			if transport == "" {
				transport, _ = server["transport"].(string)
			}
			if list, ok := server["args"].([]any); ok {
				args = list
			}
		}
		data = append(data, map[string]any{
			"name":         name,
			"enabled":      enabled,
			"command":      command,
			"url":          url,
			"transport":    transport,
			"args":         args,
			"statusLoaded": false,
		})
	}
	return map[string]any{"data": data}, nil
}

type MCPServerWriteRequest struct {
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	URL       string            `json:"url"`
	Transport string            `json:"transport"`
	Env       map[string]string `json:"env"`
}

func (s *AppService) UpsertMCPServer(request MCPServerWriteRequest) error {
	name := strings.TrimSpace(request.Name)
	if name == "" || len(name) > 120 {
		return errors.New("a valid MCP server name is required")
	}
	value := map[string]any{"enabled": request.Enabled}
	transport := strings.TrimSpace(request.Transport)
	url := strings.TrimSpace(request.URL)
	command := strings.TrimSpace(request.Command)
	if url != "" {
		if transport == "" {
			transport = "http"
		}
		value["type"] = transport
		value["url"] = url
	} else if command != "" {
		value["command"] = command
		if transport != "" {
			value["type"] = transport
		}
		if len(request.Args) > 0 {
			args := make([]any, 0, len(request.Args))
			for _, arg := range request.Args {
				arg = strings.TrimSpace(arg)
				if arg != "" {
					args = append(args, arg)
				}
			}
			value["args"] = args
		}
	} else {
		return errors.New("MCP server requires a command or url")
	}
	if len(request.Env) > 0 {
		env := make(map[string]any, len(request.Env))
		for key, raw := range request.Env {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			env[key] = raw
		}
		if len(env) > 0 {
			value["env"] = env
		}
	}
	_, err := s.call("config/value/write", map[string]any{
		"key":   "mcp_servers." + name,
		"value": value,
	})
	if err != nil {
		return err
	}
	_, err = s.call("config/mcpServer/reload", nil)
	return err
}

func (s *AppService) DeleteMCPServer(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return errors.New("a valid MCP server name is required")
	}
	_, err := s.call("config/value/write", map[string]any{
		"key":   "mcp_servers." + name,
		"value": nil,
	})
	if err != nil {
		return err
	}
	_, err = s.call("config/mcpServer/reload", nil)
	return err
}

func (s *AppService) SetHookEnabled(hookKey string, enabled bool) error {
	hookKey = strings.TrimSpace(hookKey)
	if hookKey == "" || len(hookKey) > 500 {
		return errors.New("a valid hook key is required")
	}
	_, err := s.call("config/batchWrite", map[string]any{
		"edits": []any{
			map[string]any{
				"keyPath":       "hooks.state",
				"value":         map[string]any{hookKey: map[string]any{"enabled": enabled}},
				"mergeStrategy": "upsert",
			},
		},
		"reloadUserConfig": true,
	})
	return err
}

func (s *AppService) SetAppEnabled(appID string, enabled bool) error {
	appID = strings.TrimSpace(appID)
	if appID == "" || len(appID) > 180 {
		return errors.New("a valid app id is required")
	}
	_, err := s.call("config/batchWrite", map[string]any{
		"edits": []any{
			map[string]any{
				"keyPath": "apps",
				"value": map[string]any{
					appID: map[string]any{"enabled": enabled},
				},
				"mergeStrategy": "upsert",
			},
		},
		"reloadUserConfig": true,
	})
	return err
}

func (s *AppService) ListMCPServerStatus() (map[string]any, error) {
	result, err := s.callWithTimeout("mcpServerStatus/list", map[string]any{
		"detail": "toolsAndAuthOnly",
		"limit":  100,
	}, 20*time.Second)
	if errors.Is(err, context.DeadlineExceeded) {
		return map[string]any{"data": []any{}, "statusTimedOut": true}, nil
	}
	return result, err
}

func (s *AppService) ListModelProviders() (map[string]any, error) {
	// Codex-only workbench — no Claude/Gemini/Grok entries.
	s.mu.Lock()
	agentProviders := append([]AgentProviderRuntime(nil), s.agentProviders...)
	s.mu.Unlock()
	if len(agentProviders) == 0 {
		agentProviders = detectAgentProviders(codex.Detect())
		s.mu.Lock()
		s.agentProviders = append([]AgentProviderRuntime(nil), agentProviders...)
		s.mu.Unlock()
	}

	provider := AgentProviderRuntime{
		ID: "codex", Name: "Codex", Kind: "codex", Status: "not-installed",
		Message: "CLI executable was not found in PATH",
	}
	for _, item := range agentProviders {
		if item.Kind == "codex" {
			provider = item
			break
		}
	}
	return map[string]any{
		"data": []any{
			map[string]any{
				"id":         "",
				"name":       "Codex",
				"kind":       "codex",
				"configured": provider.RuntimeReady,
				"status":     provider.Status,
				"message":    provider.Message,
			},
		},
	}, nil
}

func (s *AppService) RefreshMCPServers() error {
	_, err := s.call("config/mcpServer/reload", nil)
	return err
}

func (s *AppService) StartMCPLogin(name string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 180 {
		return nil, errors.New("a valid MCP server name is required")
	}
	return s.call("mcpServer/oauth/login", map[string]any{"name": name})
}

func (s *AppService) ListHooks() (map[string]any, error) {
	workspace, err := validateWorkspace(s.Settings().Workspace)
	if err != nil {
		return nil, err
	}
	return s.call("hooks/list", map[string]any{"cwds": []string{workspace}})
}

func (s *AppService) ListCollaborationModes() (map[string]any, error) {
	return s.call("collaborationMode/list", map[string]any{})
}

func (s *AppService) ListExperimentalFeatures() (map[string]any, error) {
	return s.call("experimentalFeature/list", map[string]any{"limit": 100})
}

func (s *AppService) SetExperimentalFeature(name string, enabled bool) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 180 {
		return errors.New("a valid feature name is required")
	}
	_, err := s.call("experimentalFeature/enablement/set", map[string]any{
		"enablement": map[string]bool{name: enabled},
	})
	return err
}

func (s *AppService) ReadAccount() (map[string]any, error) {
	return s.call("account/read", map[string]any{"refreshToken": false})
}

func (s *AppService) ReadAccountRateLimits() (map[string]any, error) {
	result, err := s.call("account/rateLimits/read", nil)
	if isChatGPTAuthenticationRequired(err) {
		return map[string]any{}, nil
	}
	return result, err
}

// ReadAccountUsage prefers local usage.json.
// When empty, it backfills from ~/.codex session rollouts (true local history),
// then optionally seeds from ChatGPT account/usage/read as a fallback.
func (s *AppService) ReadAccountUsage() (map[string]any, error) {
	if local := s.localUsageSummary(); !localUsageResponseEmpty(local) {
		return local, nil
	}

	if s.backfillLocalUsageFromRollouts() {
		if local := s.localUsageSummary(); !localUsageResponseEmpty(local) {
			return local, nil
		}
	}

	cloud, err := s.call("account/usage/read", nil)
	if err == nil && len(cloud) > 0 {
		_ = s.seedLocalUsageFromCloud(cloud)
		if local := s.localUsageSummary(); !localUsageResponseEmpty(local) {
			return local, nil
		}
		return cloud, nil
	}

	// Empty object => frontend normalizeAccountUsage(null); do not return fake zeros.
	return map[string]any{}, nil
}

// RecordLocalTurnUsage lets the frontend persist per-turn totals (belt-and-suspenders with event hook).
func (s *AppService) RecordLocalTurnUsage(threadID, turnID string, totalTokens float64) error {
	tokens := int64(totalTokens)
	if tokens <= 0 {
		return nil
	}
	s.recordLocalTurnUsage(threadID, turnID, tokens, time.Now())
	return nil
}

func localUsageResponseEmpty(summary map[string]any) bool {
	if summary == nil {
		return true
	}
	meta, _ := summary["summary"].(map[string]any)
	var lifetime int64
	if meta != nil {
		lifetime = int64(anyToFloat(meta["lifetimeTokens"]))
	}
	if lifetime > 0 {
		return false
	}
	buckets, _ := summary["dailyUsageBuckets"].([]map[string]any)
	if len(buckets) > 0 {
		return false
	}
	// JSON round-trip shape may be []any
	if raw, ok := summary["dailyUsageBuckets"].([]any); ok && len(raw) > 0 {
		return false
	}
	return true
}

func (s *AppService) maybeRecordLocalUsage(event codex.Event) {
	if event.Type != "notification" || event.Method != "thread/tokenUsage/updated" {
		return
	}
	data, ok := event.Data.(map[string]any)
	if !ok {
		return
	}
	threadID, turnID, tokens, ok := extractTurnTokens(data)
	if !ok {
		return
	}
	s.recordLocalTurnUsage(threadID, turnID, tokens, time.Now())
}

func (s *AppService) StartChatGPTLogin() (map[string]any, error) {
	return s.call("account/login/start", map[string]any{
		"type":                      "chatgpt",
		"codexStreamlinedLogin":     true,
		"useHostedLoginSuccessPage": true,
	})
}

func (s *AppService) LogoutAccount() error {
	_, err := s.call("account/logout", nil)
	return err
}

func (s *AppService) ResolveServerRequest(requestKey string, result map[string]any) error {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	return client.ResolveServerRequest(requestKey, result)
}

func (s *AppService) OpenExternal(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("only http and https links can be opened")
	}
	return s.app.Browser.OpenURL(parsed.String())
}

// SetPreventSleepActive keeps the machine awake while a Codex turn is running
// when the user enabled Prevent sleep while running.
func (s *AppService) SetPreventSleepActive(active bool) error {
	if active && !s.Settings().PreventSleepWhileRunning {
		active = false
	}
	setSystemSleepPrevention(active)
	return nil
}

func (s *AppService) SetAlwaysOnTop(enabled bool) error {
	s.applyAlwaysOnTop(enabled)
	settings := s.Settings()
	settings.AlwaysOnTop = enabled
	s.mu.Lock()
	err := writeSettings(s.settingsPath, settings)
	if err == nil {
		s.settings = cloneSettings(settings)
	}
	s.mu.Unlock()
	return err
}

func (s *AppService) applyAlwaysOnTop(enabled bool) {
	if s.app == nil {
		return
	}
	if window, exists := s.app.Window.GetByName("main"); exists {
		window.SetAlwaysOnTop(enabled)
	}
}

func normalizeShortcutBinding(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if len(value) > 48 {
		return fallback
	}
	return value
}

func (s *AppService) OpenBrowser(rawURL string) (string, error) {
	browserURL, err := normalizeBrowserURL(rawURL)
	if err != nil {
		return "", err
	}
	if err := s.assertBrowserHostAllowed(browserURL); err != nil {
		return "", err
	}
	if window, exists := s.app.Window.GetByName("browser"); exists {
		window.SetURL(browserURL)
		window.Show()
		window.Focus()
		return browserURL, nil
	}

	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "browser",
		Title:            "Nice Codex Browser",
		Width:            1180,
		Height:           780,
		MinWidth:         760,
		MinHeight:        520,
		URL:              browserURL,
		BackgroundColour: application.NewRGB(20, 21, 18),
		DevToolsEnabled:  true,
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone:    application.PermissionDeny,
			application.PermissionCamera:        application.PermissionDeny,
			application.PermissionGeolocation:   application.PermissionDeny,
			application.PermissionNotifications: application.PermissionDeny,
			application.PermissionClipboardRead: application.PermissionDeny,
		},
		KeyBindings: map[string]func(application.Window){
			"ctrl+r":    func(window application.Window) { window.Reload() },
			"f5":        func(window application.Window) { window.Reload() },
			"alt+left":  func(window application.Window) { window.ExecJS("history.back()") },
			"alt+right": func(window application.Window) { window.ExecJS("history.forward()") },
			"f12":       func(window application.Window) { window.OpenDevTools() },
		},
	})
	window.Show()
	window.Focus()
	return browserURL, nil
}

func (s *AppService) BrowserBack() error {
	return s.withBrowserWindow(func(window application.Window) { window.ExecJS("history.back()") })
}

func (s *AppService) BrowserForward() error {
	return s.withBrowserWindow(func(window application.Window) { window.ExecJS("history.forward()") })
}

func (s *AppService) BrowserReload() error {
	return s.withBrowserWindow(func(window application.Window) { window.Reload() })
}

func (s *AppService) FocusBrowser() error {
	return s.withBrowserWindow(func(window application.Window) {
		window.Show()
		window.Focus()
	})
}

func (s *AppService) OpenBrowserDevTools() error {
	return s.withBrowserWindow(func(window application.Window) { window.OpenDevTools() })
}

// SelectBrowserDownloadDir opens a folder picker for the preferred download directory.
func (s *AppService) SelectBrowserDownloadDir() (string, error) {
	current := strings.TrimSpace(s.Settings().BrowserDownloadDir)
	path, err := s.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                "Choose download folder",
		Message:              "Select the folder Nice Codex should open for browser downloads.",
		ButtonText:           "Use this folder",
		Directory:            current,
		CanChooseDirectories: true,
		CanChooseFiles:       false,
		CanCreateDirectories: true,
	}).PromptForSingleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return "", nil
		}
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		return "", errors.New("download folder must be an existing directory")
	}
	return filepath.Clean(path), nil
}

// OpenBrowserDownloadDir reveals the configured download folder in the OS file manager.
func (s *AppService) OpenBrowserDownloadDir() error {
	path := strings.TrimSpace(s.Settings().BrowserDownloadDir)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return errors.New("download folder is not configured")
		}
		path = filepath.Join(home, "Downloads")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return errors.New("download folder does not exist")
	}
	return openPathInOS(path)
}

func (s *AppService) assertBrowserHostAllowed(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("enter a valid browser address")
	}
	host := strings.ToLower(parsed.Hostname())
	settings := s.Settings()
	for _, item := range settings.BrowserBlockedHosts {
		blocked := strings.ToLower(strings.TrimSpace(item))
		if blocked == "" {
			continue
		}
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return errors.New("this host is blocked in browser settings")
		}
	}
	allowed := settings.BrowserAllowedHosts
	if len(allowed) == 0 {
		return nil
	}
	for _, item := range allowed {
		entry := strings.ToLower(strings.TrimSpace(item))
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return nil
		}
	}
	return errors.New("this host is not in the browser allow list")
}

func (s *AppService) withBrowserWindow(action func(application.Window)) error {
	window, exists := s.app.Window.GetByName("browser")
	if !exists {
		return errors.New("open the built-in browser first")
	}
	action(window)
	return nil
}

func (s *AppService) shutdown() {
	setSystemSleepPrevention(false)
	s.cancelExternalRuns()
	s.stopAllTerminalSessions()
	if s.schedulerStop != nil {
		select {
		case <-s.schedulerStop:
		default:
			close(s.schedulerStop)
		}
	}
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	_ = client.Stop()
}

func (s *AppService) call(method string, params any) (map[string]any, error) {
	return s.callWithTimeout(method, params, 45*time.Second)
}

func (s *AppService) callWithTimeout(method string, params any, timeout time.Duration) (map[string]any, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return nil, errors.New("Codex app-server is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, err := client.Request(ctx, method, params)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", method, err)
	}
	return result, nil
}

func defaultSettings() UserSettings {
	profile := "powershell"
	if runtime.GOOS != "windows" {
		profile = "zsh"
		if shell := filepath.Base(os.Getenv("SHELL")); shell == "bash" {
			profile = "bash"
		}
	}
	return UserSettings{
		Effort:               "high",
		CollaborationMode:    "default",
		Personality:          "pragmatic",
		MultiAgentMode:       "explicitRequestOnly",
		Sandbox:              "workspace-write",
		ApprovalPolicy:       "on-request",
		Theme:                "light",
		AccentColor:          "codex",
		FontFamily:           "system",
		TerminalProfile:      profile,
		Language:             "zh-CN",
		AutoConnect:          true,
		WorkMode:             "code",
		SendWithModifier:      false,
		FollowUpBehavior:     "steer",
		NotifyOnTurnComplete: true,
		CustomInstructions:   "",
		TranslucentSidebar:   true,
		HighContrast:         false,
		PointerCursor:        false,
		ReduceMotion:         false,
		UiFontSize:           "md",
		CodeFontSize:         "md",
		PreventSleepWhileRunning: false,
		AlwaysOnTop:          false,
		GitBranchPrefix:      "",
		GitCommitPrefix:      "",
		GitOpenPRAfterPush:   false,
		GitPRBodyTemplate:    "",
		BrowserAllowedHosts:  []string{},
		BrowserBlockedHosts:  []string{},
		BrowserDownloadDir:   "",
		BrowserFullCDP:       false,
		ShortcutCommandPalette: "Ctrl+K",
		ShortcutNewThread:      "Ctrl+N",
		ShortcutTerminal:       "Ctrl+`",
		ShortcutBrowser:        "Ctrl+Shift+B",
		OnboardingCompleted:    false,
		RecentWorkspaces:     []string{},
		CustomModels:         []string{},
	}
}

func isValidTerminalProfile(profile string) bool {
	for _, option := range listTerminalProfiles() {
		if option.ID == profile {
			return true
		}
	}
	// Allow saving a preferred profile even if not currently available.
	return isAllowed(profile, "powershell", "git-bash", "wsl", "zsh", "bash", "terminal")
}

func resolveSettingsPath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		directory = "."
	}
	return filepath.Join(directory, "NiceCodex", "settings.json")
}

func readSettings(path string) (UserSettings, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return UserSettings{}, err
	}
	settings := defaultSettings()
	if err := json.Unmarshal(payload, &settings); err != nil {
		return UserSettings{}, err
	}
	settings.RecentWorkspaces = sanitizeRecentWorkspaces(settings.RecentWorkspaces)
	settings.CustomModels = sanitizeCustomModels(settings.CustomModels)
	if settings.MultiAgentMode == "proactiveAgents" {
		settings.MultiAgentMode = "proactive"
	}
	settings.FollowUpBehavior = normalizeFollowUpBehavior(settings.FollowUpBehavior)
	// Disk AGENTS.md is the source of truth (matches Codex runtime).
	settings.CustomInstructions = readCodexPersonalInstructions()
	settings.UiFontSize = normalizeUiFontSize(settings.UiFontSize)
	settings.CodeFontSize = normalizeCodeFontSize(settings.CodeFontSize)
	settings.WorkMode = normalizeWorkMode(settings.WorkMode)
	settings.ShortcutCommandPalette = normalizeShortcutBinding(settings.ShortcutCommandPalette, "Ctrl+K")
	settings.ShortcutNewThread = normalizeShortcutBinding(settings.ShortcutNewThread, "Ctrl+N")
	settings.ShortcutTerminal = normalizeShortcutBinding(settings.ShortcutTerminal, "Ctrl+`")
	settings.ShortcutBrowser = normalizeShortcutBinding(settings.ShortcutBrowser, "Ctrl+Shift+B")
	settings.ModelProvider = sanitizeWorkbenchProvider(settings.ModelProvider)
	workspaceBeforeValidate := strings.TrimSpace(settings.Workspace)
	// Existing installs already configured a workspace — skip first-run wizard.
	migratedOnboarding := false
	if !settings.OnboardingCompleted && workspaceBeforeValidate != "" {
		settings.OnboardingCompleted = true
		migratedOnboarding = true
	}
	if _, err := validateWorkspace(settings.Workspace); err != nil {
		settings.Workspace = ""
	}
	// Persist the migration so a later empty/invalid workspace can't reopen the wizard.
	if migratedOnboarding {
		_ = writeSettings(path, settings)
	}
	return settings, nil
}

func sanitizeWorkbenchProvider(value string) string {
	_ = value
	// NiceCodex is Codex-only. Provider selection lives in ~/.codex/config.toml.
	return ""
}

func writeSettings(path string, settings UserSettings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

func validateWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("workspace path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace must be a directory")
	}
	return filepath.Clean(absolute), nil
}

func normalizeBrowserURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || len(rawURL) > 2048 {
		return "", errors.New("enter a valid browser address")
	}
	if !strings.Contains(rawURL, "://") {
		host := strings.ToLower(strings.Split(rawURL, "/")[0])
		scheme := "https://"
		if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "0.0.0.0") || strings.HasPrefix(host, "[::1]") {
			scheme = "http://"
		}
		rawURL = scheme + rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("the built-in browser only supports http and https addresses")
	}
	if parsed.User != nil {
		return "", errors.New("browser addresses cannot include credentials")
	}
	return parsed.String(), nil
}

func validateImageAttachment(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("image path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("image attachment must be a file")
	}
	if info.Size() > 20*1024*1024 {
		return "", errors.New("image attachments must be 20 MB or smaller")
	}
	switch strings.ToLower(filepath.Ext(absolute)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
	default:
		return "", errors.New("unsupported image format")
	}
	return filepath.Clean(absolute), nil
}

func imageExtensionForMime(mimeType, fileName string, raw []byte) string {
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		if ext == ".jpeg" {
			return ".jpg"
		}
		return ext
	}
	if len(raw) >= 8 && string(raw[:8]) == "\x89PNG\r\n\x1a\n" {
		return ".png"
	}
	if len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff {
		return ".jpg"
	}
	if len(raw) >= 6 && (string(raw[:6]) == "GIF87a" || string(raw[:6]) == "GIF89a") {
		return ".gif"
	}
	if len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP" {
		return ".webp"
	}
	return ""
}

func sanitizeAttachmentFileName(fileName, ext string) string {
	base := strings.TrimSpace(filepath.Base(fileName))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
		if b.Len() >= 48 {
			break
		}
	}
	name := b.String()
	if name == "" {
		name = "paste"
	}
	return name + ext
}

func imageAttachmentKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func mimeFromImageExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

func cloneSettings(settings UserSettings) UserSettings {
	settings.RecentWorkspaces = append([]string(nil), settings.RecentWorkspaces...)
	settings.CustomModels = append([]string(nil), settings.CustomModels...)
	settings.BrowserAllowedHosts = append([]string(nil), settings.BrowserAllowedHosts...)
	settings.BrowserBlockedHosts = append([]string(nil), settings.BrowserBlockedHosts...)
	return settings
}

func normalizeFollowUpBehavior(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "queue" {
		return "queue"
	}
	return "steer"
}

func normalizeUiFontSize(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "sm", "lg":
		return value
	default:
		return "md"
	}
}

func normalizeCodeFontSize(value string) string {
	return normalizeUiFontSize(value)
}

func sanitizeShortText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max > 0 && len(value) > max {
		value = value[:max]
	}
	return value
}

func sanitizeHostList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		host := strings.ToLower(strings.TrimSpace(value))
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		if slash := strings.Index(host, "/"); slash >= 0 {
			host = host[:slash]
		}
		if host == "" || len(host) > 180 {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
		if len(out) >= 64 {
			break
		}
	}
	return out
}

func sanitizeCustomInstructions(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimSpace(value)
	if len(value) > 16_000 {
		value = value[:16_000]
	}
	return value
}

func resolveCodexHome() string {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome != "" {
		return codexHome
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func agentsDocCandidates(dir string) []string {
	return []string{
		filepath.Join(dir, "AGENTS.override.md"),
		filepath.Join(dir, "AGENTS.md"),
	}
}

func resolveAgentsDoc(dir string) (path string, source string, content string, exists bool, emptyFile bool) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", "AGENTS.md", "", false, false
	}
	emptyPath := ""
	emptySource := ""
	for _, candidate := range agentsDocCandidates(dir) {
		payload, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		trimmed := sanitizeCustomInstructions(string(payload))
		if trimmed == "" {
			// Codex uses the first non-empty file; keep empty candidates for UI state only.
			if emptyPath == "" {
				emptyPath = candidate
				emptySource = filepath.Base(candidate)
			}
			continue
		}
		return candidate, filepath.Base(candidate), trimmed, true, false
	}
	if emptyPath != "" {
		return emptyPath, emptySource, "", false, true
	}
	return filepath.Join(dir, "AGENTS.md"), "AGENTS.md", "", false, false
}

func writeAgentsDoc(dir string, value string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("agents directory unavailable")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	home := resolveCodexHome()
	isPersonal := home != "" && filepath.Clean(dir) == filepath.Clean(home)
	// Prefer updating an existing override; otherwise write AGENTS.md.
	path := filepath.Join(dir, "AGENTS.md")
	for _, candidate := range agentsDocCandidates(dir) {
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
	}
	trimmed := sanitizeCustomInstructions(value)
	if trimmed == "" {
		if filepath.Base(path) == "AGENTS.override.md" {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return path, err
			}
			fallback := filepath.Join(dir, "AGENTS.md")
			if isPersonal {
				return fallback, os.WriteFile(fallback, []byte(""), 0o600)
			}
			_ = os.Remove(fallback)
			return fallback, nil
		}
		if isPersonal {
			return path, os.WriteFile(path, []byte(""), 0o600)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return path, err
		}
		return path, nil
	}
	if !strings.HasSuffix(trimmed, "\n") {
		trimmed += "\n"
	}
	mode := os.FileMode(0o644)
	if isPersonal {
		mode = 0o600
	}
	return path, os.WriteFile(path, []byte(trimmed), mode)
}

func readCodexPersonalInstructions() string {
	_, _, content, _, _ := resolveAgentsDoc(resolveCodexHome())
	return content
}

func writeCodexPersonalInstructions(value string) error {
	home := resolveCodexHome()
	if home == "" {
		return errors.New("codex home unavailable")
	}
	_, err := writeAgentsDoc(home, value)
	return err
}

// ReadGlobalInstructions returns personal Codex AGENTS.md content from disk.
func (s *AppService) ReadGlobalInstructions() GlobalInstructionsInfo {
	home := resolveCodexHome()
	if home == "" {
		return GlobalInstructionsInfo{}
	}
	path, source, content, exists, emptyFile := resolveAgentsDoc(home)
	return GlobalInstructionsInfo{
		Content:   content,
		Path:      path,
		Source:    source,
		Exists:    exists,
		EmptyFile: emptyFile,
		Available: true,
	}
}

// SaveGlobalInstructions writes personal Codex AGENTS.md and mirrors settings cache.
func (s *AppService) SaveGlobalInstructions(content string) (GlobalInstructionsInfo, error) {
	trimmed := sanitizeCustomInstructions(content)
	if err := writeCodexPersonalInstructions(trimmed); err != nil {
		return GlobalInstructionsInfo{}, err
	}
	s.mu.Lock()
	updated := cloneSettings(s.settings)
	updated.CustomInstructions = readCodexPersonalInstructions()
	if err := writeSettings(s.settingsPath, updated); err == nil {
		s.settings = updated
	}
	s.mu.Unlock()
	return s.ReadGlobalInstructions(), nil
}

// ReadProjectInstructions returns the current workspace AGENTS.md content.
func (s *AppService) ReadProjectInstructions() ProjectInstructionsInfo {
	workspace := strings.TrimSpace(s.Settings().Workspace)
	if workspace == "" {
		return ProjectInstructionsInfo{}
	}
	clean, err := validateWorkspace(workspace)
	if err != nil {
		return ProjectInstructionsInfo{}
	}
	path, source, content, exists, emptyFile := resolveAgentsDoc(clean)
	return ProjectInstructionsInfo{
		Content:       content,
		Workspace:     clean,
		WorkspaceName: filepath.Base(clean),
		Path:          path,
		Source:        source,
		Exists:        exists,
		EmptyFile:     emptyFile,
		Available:     true,
	}
}

// SaveProjectInstructions writes the current workspace AGENTS.md (project-scoped Codex guidance).
func (s *AppService) SaveProjectInstructions(content string) (ProjectInstructionsInfo, error) {
	workspace := strings.TrimSpace(s.Settings().Workspace)
	if workspace == "" {
		return ProjectInstructionsInfo{}, errors.New("no workspace is selected")
	}
	clean, err := validateWorkspace(workspace)
	if err != nil {
		return ProjectInstructionsInfo{}, err
	}
	if _, err := writeAgentsDoc(clean, content); err != nil {
		return ProjectInstructionsInfo{}, err
	}
	return s.ReadProjectInstructions(), nil
}

func sanitizeCustomModels(items []string) []string {
	result := make([]string, 0, min(len(items), 24))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		key := strings.ToLower(item)
		if item == "" || len(item) > 160 {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
		if len(result) == 24 {
			break
		}
	}
	return result
}

func prependWorkspace(items []string, workspace string) []string {
	result := []string{workspace}
	for _, item := range items {
		if !strings.EqualFold(filepath.Clean(item), workspace) {
			result = append(result, item)
		}
		if len(result) == 8 {
			break
		}
	}
	return result
}

func sanitizeRecentWorkspaces(items []string) []string {
	result := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for _, item := range items {
		cleaned, err := validateWorkspace(item)
		if err != nil {
			continue
		}
		key := strings.ToLower(cleaned)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, cleaned)
		if len(result) == 8 {
			break
		}
	}
	return result
}

func normalizeSandbox(value string) string {
	if isAllowed(value, "read-only", "workspace-write", "danger-full-access") {
		return value
	}
	return "workspace-write"
}

func normalizeApproval(value string) string {
	if isAllowed(value, "untrusted", "on-request", "never") {
		return value
	}
	return "on-request"
}

func normalizeCollaborationMode(value string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case "plan":
		return "plan"
	case "default", "code", "execute", "pair_programming", "custom":
		return "default"
	default:
		return ""
	}
}

// Plan: null → official built-in plan.md (proposed_plan rules).
// Default: non-empty exit text is required — Codex skips the mode update when
// developer_instructions is null/empty, leaving stale Plan rules in context
// (openai/codex#10185, #25582).
func collaborationModeDeveloperInstructions(mode string, resetNonce int) any {
	switch normalizeCollaborationMode(mode) {
	case "plan":
		return nil
	default:
		// Closely matches official default.md, plus an explicit Plan end signal
		// (Plan mode requires a developer message that "explicitly ends it").
		text := strings.TrimSpace(`
# Collaboration Mode: Default

**Plan Mode is now ended.** This developer message explicitly ends Plan Mode.
Any previous instructions for other modes (e.g. Plan mode) are no longer active and must be ignored.

You are now in Default mode. You may execute commands, edit files, apply patches, and perform mutating actions.

Your active mode changes only when new developer instructions with a different collaboration mode change it; user requests or tool descriptions do not change mode by themselves.
`)
		// Bump inequality vs prior Default+null / prior reset so core emits an update.
		if resetNonce > 0 {
			text = text + "\n\n(mode-reset:" + strconv.Itoa(resetNonce) + ")"
		}
		return text
	}
}

func sandboxPolicy(value string, workspace string) map[string]any {
	switch normalizeSandbox(value) {
	case "read-only":
		return map[string]any{"type": "readOnly", "networkAccess": false}
	case "danger-full-access":
		return map[string]any{"type": "dangerFullAccess"}
	default:
		return map[string]any{
			"type":                "workspaceWrite",
			"writableRoots":       []string{workspace},
			"networkAccess":       false,
			"excludeTmpdirEnvVar": false,
			"excludeSlashTmp":     false,
		}
	}
}

func (s *AppService) ensureThreadInWorkspace(threadID string, workspace string) error {
	cleanWorkspace, err := validateWorkspace(workspace)
	if err != nil {
		return err
	}
	if s.threadAllowed(threadID, cleanWorkspace) {
		return nil
	}
	// NiceCodex local sessions are authoritative for workspace membership.
	if session := s.sessionFor(threadID, cleanWorkspace); session != nil {
		s.rememberThread(threadID, cleanWorkspace)
		return nil
	}
	result, err := s.call("thread/read", map[string]any{"threadId": threadID, "includeTurns": false})
	if err != nil {
		return err
	}
	thread, ok := result["thread"].(map[string]any)
	if !ok {
		return errors.New("Codex returned an invalid thread")
	}
	threadWorkspace, _ := thread["cwd"].(string)
	if !samePath(threadWorkspace, cleanWorkspace) {
		return errors.New("this thread belongs to a different workspace")
	}
	s.rememberThread(threadID, cleanWorkspace)
	return nil
}

func (s *AppService) rememberThread(threadID string, workspace string) {
	s.mu.Lock()
	s.allowedThreads[threadID] = filepath.Clean(workspace)
	s.mu.Unlock()
}

func (s *AppService) threadAllowed(threadID string, workspace string) bool {
	s.mu.Lock()
	threadWorkspace := s.allowedThreads[threadID]
	session := s.sessions[threadID]
	s.mu.Unlock()
	if threadWorkspace != "" && samePath(threadWorkspace, workspace) {
		return true
	}
	return session != nil && !session.Archived && samePath(session.Workspace, workspace)
}

func (s *AppService) ensureCodexBackendThread(session *SessionRecord, settings UserSettings, workspace string) (string, error) {
	if session == nil {
		return "", errors.New("session not found")
	}
	if session.BackendRef != "" {
		return session.BackendRef, nil
	}
	params := map[string]any{
		"cwd":            workspace,
		"sandbox":        normalizeSandbox(settings.Sandbox),
		"approvalPolicy": normalizeApproval(settings.ApprovalPolicy),
	}
	model := strings.TrimSpace(session.Model)
	if model == "" {
		model = strings.TrimSpace(settings.Model)
	}
	if model != "" {
		params["model"] = model
	}
	providerID := strings.TrimSpace(session.ProviderID)
	if providerID == "" {
		providerID = strings.TrimSpace(settings.ModelProvider)
	}
	if externalProviderKind(providerID) == "" && providerID != "" {
		params["modelProvider"] = providerID
	}
	result, err := s.call("thread/start", params)
	if err != nil {
		return "", err
	}
	backendID := threadIDFromResult(result)
	if backendID == "" {
		return "", errors.New("Codex did not return a thread id")
	}
	s.mu.Lock()
	if record := s.sessions[session.ID]; record != nil {
		record.BackendRef = backendID
		record.UpdatedAt = time.Now().Unix()
		if name, ok := result["thread"].(map[string]any); ok {
			if value, _ := name["name"].(string); value != "" && (record.Name == "" || record.Name == "New task") {
				record.Name = value
			}
		}
		s.persistSessionsLocked()
	}
	s.mu.Unlock()
	s.rememberThread(session.ID, workspace)
	s.rememberThread(backendID, workspace)
	return backendID, nil
}

func (s *AppService) attachSessionIdentity(result map[string]any, session *SessionRecord, sessionID string) {
	if result == nil {
		return
	}
	id := sessionID
	model := ""
	providerID := ""
	workMode := "code"
	if session != nil {
		id = session.ID
		model = session.Model
		providerID = session.ProviderID
		workMode = normalizeWorkMode(session.WorkMode)
	}
	if model != "" {
		result["model"] = model
	}
	result["modelProvider"] = providerID
	result["workMode"] = workMode
	if thread, ok := result["thread"].(map[string]any); ok {
		thread["id"] = id
		if model != "" {
			thread["model"] = model
		}
		thread["modelProvider"] = providerID
		thread["workMode"] = workMode
	}
}

func (s *AppService) touchSessionPreview(sessionID, text string) {
	preview := truncateRunes(strings.TrimSpace(text), 96)
	if preview == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil {
		return
	}
	record.Preview = preview
	if record.Name == "" || record.Name == "New task" {
		record.Name = truncateRunes(preview, 56)
	}
	record.UpdatedAt = time.Now().Unix()
	s.persistSessionsLocked()
}

func (s *AppService) sessionIDForBackendRef(backendID string) string {
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Prefer NiceCodex UUID sessions that own this app-server thread.
	// Codex-id mirrors (id == backendRef) must not win, or events/UI can split
	// across two rows that share the same backend conversation context.
	var mirrorID string
	for id, record := range s.sessions {
		if record == nil || record.Archived {
			continue
		}
		if strings.TrimSpace(record.BackendRef) != backendID && id != backendID {
			continue
		}
		ownsBackend := strings.TrimSpace(record.BackendRef) == backendID
		if ownsBackend && id != backendID {
			return id
		}
		if id == backendID {
			mirrorID = id
		}
	}
	if mirrorID != "" {
		return mirrorID
	}
	return backendID
}

func (s *AppService) remapCodexEvent(event *codex.Event) {
	if event == nil || event.Data == nil {
		return
	}
	data, ok := event.Data.(map[string]any)
	if !ok {
		return
	}
	threadID, _ := data["threadId"].(string)
	if threadID == "" {
		if thread, ok := data["thread"].(map[string]any); ok {
			threadID, _ = thread["id"].(string)
		}
	}
	if threadID == "" {
		return
	}
	mapped := s.sessionIDForBackendRef(threadID)
	if mapped == "" || mapped == threadID {
		return
	}
	if _, exists := data["threadId"]; exists {
		data["threadId"] = mapped
	}
	if turn, ok := data["turn"].(map[string]any); ok {
		if value, _ := turn["threadId"].(string); value == threadID {
			turn["threadId"] = mapped
		}
	}
	if thread, ok := data["thread"].(map[string]any); ok {
		if value, _ := thread["id"].(string); value == threadID {
			thread["id"] = mapped
		}
	}
}

func threadIDFromResult(result map[string]any) string {
	thread, ok := result["thread"].(map[string]any)
	if !ok {
		return ""
	}
	threadID, _ := thread["id"].(string)
	return threadID
}

func samePath(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func isAllowed(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func isDialogCancelled(err error) bool {
	return err != nil && strings.EqualFold(strings.TrimSpace(err.Error()), "cancelled by user")
}

func isChatGPTAuthenticationRequired(err error) bool {
	return err != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(err.Error())), "chatgpt authentication required")
}

func providerKind(name string) string {
	value := strings.ToLower(name)
	switch {
	case strings.Contains(value, "claude") || strings.Contains(value, "anthropic"):
		return "claude"
	case strings.Contains(value, "gemini") || strings.Contains(value, "google"):
		return "gemini"
	case strings.Contains(value, "grok") || strings.Contains(value, "xai"):
		return "grok"
	default:
		return "custom"
	}
}

func providerDisplayName(name string, entry map[string]any) string {
	if display, ok := entry["name"].(string); ok && strings.TrimSpace(display) != "" {
		return strings.TrimSpace(display)
	}
	return name
}

func (s *AppService) ReadCodexFeatureFlags() CodexFeatureFlags {
	return readCodexFeatureFlags()
}

func (s *AppService) SaveCodexFeatureFlags(flags CodexFeatureFlags) (CodexFeatureFlags, error) {
	if err := writeCodexFeatureFlags(flags); err != nil {
		return CodexFeatureFlags{}, err
	}
	s.mu.Lock()
	updated := cloneSettings(s.settings)
	updated.BrowserFullCDP = flags.BrowserUseFullCDP
	_ = writeSettings(s.settingsPath, updated)
	s.settings = updated
	s.mu.Unlock()
	return readCodexFeatureFlags(), nil
}

func (s *AppService) ListScheduledTasks() []ScheduledTask {
	if s.scheduledTasks == nil {
		return []ScheduledTask{}
	}
	return s.scheduledTasks.list()
}

func (s *AppService) SaveScheduledTask(task ScheduledTask) (ScheduledTask, error) {
	if s.scheduledTasks == nil {
		return ScheduledTask{}, errors.New("scheduler unavailable")
	}
	if task.Workspace == "" {
		task.Workspace = s.Settings().Workspace
	}
	if task.Workspace != "" {
		if clean, err := validateWorkspace(task.Workspace); err == nil {
			task.Workspace = clean
		}
	}
	return s.scheduledTasks.upsert(task)
}

func (s *AppService) DeleteScheduledTask(id string) error {
	if s.scheduledTasks == nil {
		return errors.New("scheduler unavailable")
	}
	return s.scheduledTasks.delete(id)
}

func (s *AppService) runScheduledTaskLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.schedulerStop:
			return
		case <-ticker.C:
			s.tickScheduledTasks()
		}
	}
}

func (s *AppService) tickScheduledTasks() {
	if s.scheduledTasks == nil {
		return
	}
	now := time.Now().Unix()
	for _, task := range s.scheduledTasks.due(now) {
		err := s.executeScheduledTask(task)
		s.scheduledTasks.markRan(task.ID, err)
	}
}

func (s *AppService) executeScheduledTask(task ScheduledTask) error {
	workspace := strings.TrimSpace(task.Workspace)
	if workspace == "" {
		workspace = s.Settings().Workspace
	}
	if workspace == "" {
		return errors.New("no workspace for scheduled task")
	}
	clean, err := validateWorkspace(workspace)
	if err != nil {
		return err
	}
	runWorkspace := clean
	if task.UseWorktree {
		worktreePath, worktreeErr := ensureScheduledWorktree(clean, task.ID)
		if worktreeErr != nil {
			return worktreeErr
		}
		runWorkspace = worktreePath
	}
	settings := s.Settings()
	collaborationMode := strings.TrimSpace(settings.CollaborationMode)
	if collaborationMode == "" {
		collaborationMode = "default"
	}
	note := "Scheduled task: " + task.Title
	if task.UseWorktree && runWorkspace != clean {
		note += " (git worktree: " + runWorkspace + ")"
	}
	prompt := strings.TrimSpace(task.Prompt)
	if prompt == "" {
		return errors.New("empty scheduled prompt")
	}
	fullPrompt := note + "\n\n" + prompt
	record := s.createSessionRecord(runWorkspace, "", "", settings.Model, settings.Effort, collaborationMode, normalizeWorkMode(settings.WorkMode))
	s.mu.Lock()
	s.upsertSessionLocked(record)
	s.mu.Unlock()
	s.rememberThread(record.ID, runWorkspace)
	_, err = s.SendMessage(SendMessageRequest{
		ThreadID: record.ID,
		Text:     fullPrompt,
	})
	return err
}

func ensureScheduledWorktree(workspace, taskID string) (string, error) {
	if currentGitBranch(workspace) == "" {
		return "", errors.New("prefer worktree requires a Git repository")
	}
	safeID := sanitizeFileToken(taskID)
	if safeID == "" {
		safeID = fmt.Sprintf("%d", time.Now().Unix())
	}
	if len(safeID) > 24 {
		safeID = safeID[:24]
	}
	root := filepath.Join(workspace, ".nice-codex", "worktrees")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(root, safeID)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return target, nil
	}
	branch := "nice-codex/sched-" + safeID
	output, err := runGit(workspace, 90*time.Second, "worktree", "add", "-B", branch, target)
	if err != nil {
		return "", fmt.Errorf("git worktree add failed: %w: %s", err, strings.TrimSpace(output))
	}
	return target, nil
}
