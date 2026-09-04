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
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"nice_codex_desktop/internal/codex"
)

type AppService struct {
	app                    *application.App
	pluginAssets           *pluginAssetServer
	mu                     sync.Mutex
	historyMu              sync.Mutex
	usageMu                sync.Mutex
	usagePersistMu         sync.Mutex
	client                 *codex.Client
	settings               UserSettings
	settingsPath           string
	allowedThreads         map[string]string
	allowedImages          map[string]struct{}
	terminalSessions       map[string]*terminalSession
	agentProviders         []AgentProviderRuntime
	sessions               map[string]*SessionRecord
	externalRuns           map[string]*externalRun
	grokAPISessions        map[string]*GrokAPISession
	grokApprovals          map[string]*grokPendingApproval
	claudeSessions         map[string]*claudeStoredSession
	scheduledTasks         *scheduledTaskStore
	schedulerStop          chan struct{}
	shutdownOnce           sync.Once
	usageCache             *localUsageFile
	usageFlushTimer        *time.Timer
	usageFlushGen          uint64
	usageBackfillAt        map[string]time.Time
	externalUsageCache     map[string]map[string]any
	externalUsageCachedAt  map[string]time.Time
	nativeSessionsSyncedAt map[string]time.Time
	updateState            updateDownloadState
	codexLifecycleMu       sync.Mutex
	codexThreadStartMu     sync.Mutex
	codexActiveTurns       map[string]string
	codexPendingDispatches map[string]bool
	codexGoalEventSequence uint64
	// pendingCodexSessions maps a workspace to the NiceCodex session that is
	// mid-first-allocation (thread/start sent, response or thread/started event
	// not yet bound). A map keeps concurrent allocations in different workspaces
	// from clobbering each other's pending slot.
	pendingCodexSessions map[string]string
	codexHistoryCache    map[string]*codexHistorySnapshot
	claudeHistoryCache   map[string]*claudeHistorySnapshot
	grokHistoryCache     map[string]*grokHistorySnapshot
	nativeHistoryCache   map[string]nativeHistoryCacheEntry
	providerRouter       *providerRouter
}

const defaultCodexModel = "gpt-5.6-sol"

const (
	conversationHistoryPageTurns        = 24
	conversationHistoryCacheLimit       = 12
	codexHistoryCacheLimit              = 12
	conversationHistoryCacheBytes int64 = 64 << 20
	conversationHistoryEntryBytes int64 = 32 << 20
)

type codexHistorySnapshot struct {
	turns     []any
	weight    int64
	touchedAt time.Time
}

type claudeHistorySnapshot struct {
	path      string
	size      int64
	modified  int64
	summary   ClaudeSessionSummary
	messages  []ClaudeMessage
	weight    int64
	touchedAt time.Time
}

type grokHistorySnapshot struct {
	dir       string
	path      string
	size      int64
	modified  int64
	summary   GrokSessionSummary
	messages  []GrokMessage
	weight    int64
	touchedAt time.Time
}

type BootstrapData struct {
	Codex            codex.Detection        `json:"codex"`
	Grok             GrokRuntimeStatus      `json:"grok"`
	AgentProviders   []AgentProviderRuntime `json:"agentProviders"`
	Settings         UserSettings           `json:"settings"`
	Workspace        *WorkspaceInfo         `json:"workspace,omitempty"`
	TerminalProfiles []TerminalProfile      `json:"terminalProfiles"`
	AppVersion       string                 `json:"appVersion"`
	UpdateRepo       string                 `json:"updateRepo"`
}

type UserSettings struct {
	ActiveRuntime        string   `json:"activeRuntime"`
	Workspace            string   `json:"workspace"`
	RecentWorkspaces     []string `json:"recentWorkspaces"`
	GrokWorkspace        string   `json:"grokWorkspace"`
	GrokRecentWorkspaces []string `json:"grokRecentWorkspaces"`
	GrokBackend          string   `json:"grokBackend"`
	GrokBuildModel       string   `json:"grokBuildModel"`
	GrokAPIModel         string   `json:"grokAPIModel"`
	GrokEffort           string   `json:"grokEffort"`
	GrokSandbox          string   `json:"grokSandbox"`
	GrokApprovalPolicy   string   `json:"grokApprovalPolicy"`
	GrokWebSearch        bool     `json:"grokWebSearch"`
	GrokXSearch          bool     `json:"grokXSearch"`
	// GrokAPIKey / GrokAPIBaseURL configure NiceCodex Grok API mode (OpenAI-compatible).
	// Empty key falls back to XAI_API_KEY / GROK_API_KEY env; empty base uses https://api.x.ai/v1.
	GrokAPIKey     string `json:"grokAPIKey"`
	GrokAPIBaseURL string `json:"grokAPIBaseURL"`
	// Claude Code independent stack (workspace / model / permissions).
	ClaudeWorkspace        string   `json:"claudeWorkspace"`
	ClaudeRecentWorkspaces []string `json:"claudeRecentWorkspaces"`
	ClaudeModel            string   `json:"claudeModel"`
	ClaudeEffort           string   `json:"claudeEffort"`
	ClaudeSandbox          string   `json:"claudeSandbox"`
	ClaudeApprovalPolicy   string   `json:"claudeApprovalPolicy"`
	// ClaudePermissionMode is the official Claude Code --permission-mode value when set:
	// acceptEdits | auto | bypassPermissions | manual | dontAsk | plan.
	// Empty falls back to ClaudeSandbox + ClaudeApprovalPolicy mapping.
	ClaudePermissionMode string `json:"claudePermissionMode"`
	// ClaudeCustomModels are free-form --model ids (aliases or full IDs) kept per-user.
	ClaudeCustomModels []string `json:"claudeCustomModels"`
	// GrokCustomModels are free-form model ids for Build CLI / API mode pickers.
	GrokCustomModels []string `json:"grokCustomModels"`
	// Gemini/OpenCode preferences stay separate from Codex/Claude/Grok so a
	// runtime switch never changes another provider's default model.
	GeminiWorkspace           string   `json:"geminiWorkspace"`
	GeminiRecentWorkspaces    []string `json:"geminiRecentWorkspaces"`
	GeminiModel               string   `json:"geminiModel"`
	GeminiEffort              string   `json:"geminiEffort"`
	GeminiSandbox             string   `json:"geminiSandbox"`
	GeminiApprovalPolicy      string   `json:"geminiApprovalPolicy"`
	GeminiCustomModels        []string `json:"geminiCustomModels"`
	OpenCodeModel             string   `json:"openCodeModel"`
	OpenCodeWorkspace         string   `json:"openCodeWorkspace"`
	OpenCodeRecentWorkspaces  []string `json:"openCodeRecentWorkspaces"`
	OpenCodeEffort            string   `json:"openCodeEffort"`
	OpenCodeSandbox           string   `json:"openCodeSandbox"`
	OpenCodeApprovalPolicy    string   `json:"openCodeApprovalPolicy"`
	OpenCodeProvider          string   `json:"openCodeProvider"`
	OpenCodeCustomModels      []string `json:"openCodeCustomModels"`
	Model                     string   `json:"model"`
	CodexContextWindow        int64    `json:"codexContextWindow"`
	CodexAutoCompactThreshold int64    `json:"codexAutoCompactThreshold"`
	ModelProvider             string   `json:"modelProvider"`
	CustomModels              []string `json:"customModels"`
	Effort                    string   `json:"effort"`
	ServiceTier               string   `json:"serviceTier"`
	CollaborationMode         string   `json:"collaborationMode"`
	Personality               string   `json:"personality"`
	MultiAgentMode            string   `json:"multiAgentMode"`
	Sandbox                   string   `json:"sandbox"`
	ApprovalPolicy            string   `json:"approvalPolicy"`
	Theme                     string   `json:"theme"`
	AccentColor               string   `json:"accentColor"`
	FontFamily                string   `json:"fontFamily"`
	TerminalProfile           string   `json:"terminalProfile"`
	Language                  string   `json:"language"`
	AutoConnect               bool     `json:"autoConnect"`
	WorkMode                  string   `json:"workMode"`
	SendWithModifier          bool     `json:"sendWithModifier"`
	FollowUpBehavior          string   `json:"followUpBehavior"`
	NotifyOnTurnComplete      bool     `json:"notifyOnTurnComplete"`
	CustomInstructions        string   `json:"customInstructions"`
	TranslucentSidebar        bool     `json:"translucentSidebar"`
	HighContrast              bool     `json:"highContrast"`
	PointerCursor             bool     `json:"pointerCursor"`
	ReduceMotion              bool     `json:"reduceMotion"`
	UiFontSize                string   `json:"uiFontSize"`
	CodeFontSize              string   `json:"codeFontSize"`
	PreventSleepWhileRunning  bool     `json:"preventSleepWhileRunning"`
	AlwaysOnTop               bool     `json:"alwaysOnTop"`
	GitBranchPrefix           string   `json:"gitBranchPrefix"`
	GitCommitPrefix           string   `json:"gitCommitPrefix"`
	GitOpenPRAfterPush        bool     `json:"gitOpenPRAfterPush"`
	GitPRBodyTemplate         string   `json:"gitPRBodyTemplate"`
	BrowserAllowedHosts       []string `json:"browserAllowedHosts"`
	BrowserBlockedHosts       []string `json:"browserBlockedHosts"`
	BrowserDownloadDir        string   `json:"browserDownloadDir"`
	BrowserFullCDP            bool     `json:"browserFullCDP"`
	ShortcutCommandPalette    string   `json:"shortcutCommandPalette"`
	ShortcutNewThread         string   `json:"shortcutNewThread"`
	ShortcutTerminal          string   `json:"shortcutTerminal"`
	ShortcutBrowser           string   `json:"shortcutBrowser"`
	// CodexClient* is the app-server initialize clientInfo sent upstream as User-Agent.
	// Empty values fall back to official Codex Desktop defaults (or NICE_CODEX_CLIENT_* env).
	CodexClientName    string `json:"codexClientName"`
	CodexClientTitle   string `json:"codexClientTitle"`
	CodexClientVersion string `json:"codexClientVersion"`
	// NetworkProxy* lets users point Codex / CLI traffic at Clash (or similar)
	// HTTP mixed ports without enabling TUN. Applied as process env + child inherit.
	NetworkProxyEnabled bool   `json:"networkProxyEnabled"`
	NetworkProxyURL     string `json:"networkProxyUrl"`
	NetworkProxyNoProxy string `json:"networkProxyNoProxy"`
	OnboardingCompleted bool   `json:"onboardingCompleted"`
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
	ThreadID string   `json:"threadId"`
	Text     string   `json:"text"`
	Images   []string `json:"images"`
	// Per-turn mode override — mirrors official TUI SubmitUserMessageWithMode.
	CollaborationMode string `json:"collaborationMode,omitempty"`
}

// TurnLivenessView is the authoritative process/app-server state for one session.
// It intentionally excludes cached history, which can retain stale in-progress rows.
type TurnLivenessView struct {
	Running          bool   `json:"running"`
	TurnID           string `json:"turnId,omitempty"`
	LatestTurnID     string `json:"latestTurnId,omitempty"`
	LatestTurnStatus string `json:"latestTurnStatus,omitempty"`
	Runtime          string `json:"runtime"`
	State            string `json:"state"`
}

type SessionPreferencesRequest struct {
	SessionID         string `json:"sessionId"`
	Model             string `json:"model"`
	Effort            string `json:"effort"`
	CollaborationMode string `json:"collaborationMode"`
	// Goal is a pointer so ordinary model/mode updates preserve an existing
	// session objective; an explicit empty value clears it.
	Goal               *string `json:"goal,omitempty"`
	GoalStatus         *string `json:"goalStatus,omitempty"`
	GoalTokenBudget    *int64  `json:"goalTokenBudget,omitempty"`
	GoalTokenBudgetSet bool    `json:"goalTokenBudgetSet,omitempty"`
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

func NewAppService(app *application.App, pluginAssets *pluginAssetServer) *AppService {
	settingsPath := resolveSettingsPath()
	settings := defaultSettings()
	if loaded, err := readSettings(settingsPath); err == nil {
		settings = loaded
	}

	service := &AppService{
		app:                    app,
		pluginAssets:           pluginAssets,
		settings:               settings,
		settingsPath:           settingsPath,
		allowedThreads:         make(map[string]string),
		allowedImages:          make(map[string]struct{}),
		terminalSessions:       make(map[string]*terminalSession),
		sessions:               loadSessions(settingsPath),
		externalRuns:           make(map[string]*externalRun),
		nativeSessionsSyncedAt: make(map[string]time.Time),
		grokAPISessions:        loadGrokAPISessions(settingsPath),
		grokApprovals:          make(map[string]*grokPendingApproval),
		claudeSessions:         loadClaudeSessions(settingsPath),
		codexHistoryCache:      make(map[string]*codexHistorySnapshot),
		claudeHistoryCache:     make(map[string]*claudeHistorySnapshot),
		grokHistoryCache:       make(map[string]*grokHistorySnapshot),
		nativeHistoryCache:     make(map[string]nativeHistoryCacheEntry),
		scheduledTasks:         newScheduledTaskStore(settingsPath),
		schedulerStop:          make(chan struct{}),
		providerRouter:         newProviderRouter(),
		codexActiveTurns:       make(map[string]string),
		codexPendingDispatches: make(map[string]bool),
		pendingCodexSessions:   make(map[string]string),
	}
	if routerConfig, err := loadProviderRouterConfig(settingsPath); err != nil {
		service.providerRouter.setError(err.Error())
	} else if err := service.providerRouter.configure(routerConfig); err != nil {
		service.providerRouter.setError(err.Error())
	}
	_ = service.scheduledTasks.load()
	service.client = codex.NewClient(func(event codex.Event) {
		service.remapCodexEvent(&event)
		service.trackCodexGoal(event)
		service.trackCodexActivity(event)
		service.maybeRecordLocalUsage(event)
		app.Event.Emit("codex:event", event)
	})
	// Apply proxy before any outbound HTTP / child CLI is started.
	applyNetworkProxyFromSettings(settings)
	go service.runScheduledTaskLoop()
	return service
}

func (s *AppService) Bootstrap() BootstrapData {
	settings := s.Settings()
	settings.ModelProvider = sanitizeWorkbenchProvider(settings.ModelProvider)
	codexDetection := codex.Detect()
	grokStatus := detectGrokRuntimeQuick()
	if !grokStatus.APIConfigured {
		grokStatus.APIConfigured = strings.TrimSpace(resolveGrokAPIKey(settings)) != ""
	}
	agentProviders := detectAgentProvidersQuick(codexDetection, grokStatus)
	s.mu.Lock()
	s.agentProviders = agentProviders
	s.settings.ModelProvider = settings.ModelProvider
	s.mu.Unlock()
	data := BootstrapData{
		Codex:            codexDetection,
		Grok:             grokStatus,
		AgentProviders:   agentProviders,
		Settings:         settings,
		TerminalProfiles: listTerminalProfiles(),
		AppVersion:       AppVersion,
		UpdateRepo:       GitHubRepo,
	}
	s.applyAlwaysOnTop(settings.AlwaysOnTop)
	// Surface the workspace for the currently active product runtime.
	activePath := strings.TrimSpace(activeWorkspaceForRuntime(settings))
	if activePath != "" {
		workspace := inspectWorkspace(activePath)
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
	settings.Workspace = strings.TrimSpace(settings.Workspace)
	settings.Model = strings.TrimSpace(settings.Model)
	if settings.Model == "" {
		settings.Model = defaultCodexModel
	}
	settings.CodexContextWindow, settings.CodexAutoCompactThreshold = normalizeCodexContextSettings(
		settings.CodexContextWindow,
		settings.CodexAutoCompactThreshold,
	)
	settings.ModelProvider = "" // Codex-only: never persist Claude/Gemini/Grok workbench providers
	settings.CustomModels = sanitizeCustomModels(settings.CustomModels)
	settings.ClaudeCustomModels = sanitizeCustomModels(settings.ClaudeCustomModels)
	settings.GrokCustomModels = sanitizeCustomModels(settings.GrokCustomModels)
	settings.Effort = strings.TrimSpace(settings.Effort)
	settings.ActiveRuntime = normalizeRuntime(settings.ActiveRuntime)
	settings.GrokBackend = normalizeGrokBackend(settings.GrokBackend)
	settings.GrokBuildModel = sanitizeShortText(settings.GrokBuildModel, 160)
	settings.GrokAPIModel = sanitizeShortText(settings.GrokAPIModel, 160)
	settings.GrokAPIKey = sanitizeShortText(settings.GrokAPIKey, 512)
	settings.GrokAPIBaseURL = sanitizeShortText(settings.GrokAPIBaseURL, 512)
	settings.GrokEffort = normalizeGrokEffort(settings.GrokEffort)
	settings.ClaudeModel = sanitizeShortText(settings.ClaudeModel, 160)
	settings.ClaudeEffort = normalizeClaudeEffort(settings.ClaudeEffort)
	settings.GeminiWorkspace = strings.TrimSpace(settings.GeminiWorkspace)
	settings.GeminiRecentWorkspaces = sanitizeRecentWorkspaces(settings.GeminiRecentWorkspaces)
	settings.GeminiModel = sanitizeShortText(settings.GeminiModel, 160)
	settings.GeminiCustomModels = sanitizeCustomModels(settings.GeminiCustomModels)
	if !isAllowed(settings.GeminiSandbox, "read-only", "workspace-write", "danger-full-access") {
		settings.GeminiSandbox = "workspace-write"
	}
	if !isAllowed(settings.GeminiApprovalPolicy, "on-request", "never") {
		settings.GeminiApprovalPolicy = "on-request"
	}
	settings.GeminiEffort = normalizeGeminiEffort(settings.GeminiEffort)
	settings.OpenCodeModel = sanitizeShortText(settings.OpenCodeModel, 160)
	settings.OpenCodeWorkspace = strings.TrimSpace(settings.OpenCodeWorkspace)
	settings.OpenCodeRecentWorkspaces = sanitizeRecentWorkspaces(settings.OpenCodeRecentWorkspaces)
	settings.OpenCodeEffort = sanitizeShortText(settings.OpenCodeEffort, 32)
	if !isAllowed(settings.OpenCodeSandbox, "read-only", "workspace-write", "danger-full-access") {
		settings.OpenCodeSandbox = "workspace-write"
	}
	if !isAllowed(settings.OpenCodeApprovalPolicy, "on-request", "never") {
		settings.OpenCodeApprovalPolicy = "on-request"
	}
	settings.OpenCodeProvider = sanitizeShortText(settings.OpenCodeProvider, 160)
	settings.OpenCodeCustomModels = sanitizeCustomModels(settings.OpenCodeCustomModels)
	if settings.OpenCodeEffort == "" {
		settings.OpenCodeEffort = "high"
	}
	if !isAllowed(settings.GrokSandbox, "read-only", "workspace-write", "danger-full-access") {
		return UserSettings{}, errors.New("invalid Grok sandbox mode")
	}
	if !isAllowed(settings.GrokApprovalPolicy, "on-request", "never") {
		return UserSettings{}, errors.New("invalid Grok approval policy")
	}
	if !isAllowed(settings.ClaudeSandbox, "read-only", "workspace-write", "danger-full-access") {
		return UserSettings{}, errors.New("invalid Claude sandbox mode")
	}
	if !isAllowed(settings.ClaudeApprovalPolicy, "on-request", "never") {
		return UserSettings{}, errors.New("invalid Claude approval policy")
	}
	settings.ClaudePermissionMode = normalizeClaudePermissionMode(settings.ClaudePermissionMode)
	if !isAllowed(settings.Theme, "dark", "light", "claude", "system") {
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
	settings.CodexClientName = sanitizeCodexClientField(settings.CodexClientName, 64)
	settings.CodexClientTitle = sanitizeCodexClientField(settings.CodexClientTitle, 80)
	settings.CodexClientVersion = sanitizeCodexClientField(settings.CodexClientVersion, 32)
	proxyURL, proxyErr := normalizeNetworkProxyURL(settings.NetworkProxyURL)
	if proxyErr != nil {
		return UserSettings{}, proxyErr
	}
	settings.NetworkProxyURL = proxyURL
	settings.NetworkProxyNoProxy = sanitizeNetworkProxyNoProxy(settings.NetworkProxyNoProxy)
	if settings.NetworkProxyEnabled && settings.NetworkProxyURL == "" {
		return UserSettings{}, errors.New("network proxy URL is required when proxy is enabled")
	}
	if settings.Effort == "" {
		settings.Effort = "high"
	}
	settings.ServiceTier = strings.TrimSpace(settings.ServiceTier)
	if len(settings.Model) > 160 || len(settings.ModelProvider) > 160 || len(settings.Effort) > 64 || len(settings.ServiceTier) > 64 {
		return UserSettings{}, errors.New("model preferences are too long")
	}
	// Disk AGENTS.md is only mutated by SaveGlobalInstructions — never wipe it from generic preference saves.
	settings.CustomInstructions = readCodexPersonalInstructions()
	s.mu.Lock()
	// Workspace/runtime fields have dedicated mutation APIs. Merge them while
	// holding the same lock as the write so an older preferences request cannot
	// overwrite a folder or runtime selected concurrently.
	latest := cloneSettings(s.settings)
	settings.Workspace = latest.Workspace
	settings.RecentWorkspaces = latest.RecentWorkspaces
	settings.GrokWorkspace = latest.GrokWorkspace
	settings.GrokRecentWorkspaces = latest.GrokRecentWorkspaces
	settings.ClaudeWorkspace = latest.ClaudeWorkspace
	settings.ClaudeRecentWorkspaces = latest.ClaudeRecentWorkspaces
	settings.GeminiWorkspace = latest.GeminiWorkspace
	settings.GeminiRecentWorkspaces = latest.GeminiRecentWorkspaces
	settings.OpenCodeWorkspace = latest.OpenCodeWorkspace
	settings.OpenCodeRecentWorkspaces = latest.OpenCodeRecentWorkspaces
	settings.ActiveRuntime = latest.ActiveRuntime
	// Onboarding completion is monotonic — concurrent preference saves must not reopen the wizard.
	if latest.OnboardingCompleted || settings.OnboardingCompleted || strings.TrimSpace(settings.Workspace) != "" {
		settings.OnboardingCompleted = true
	}
	flags := readCodexFeatureFlags()
	flags.BrowserUseFullCDP = settings.BrowserFullCDP
	_ = writeCodexFeatureFlags(flags)
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
		applyNetworkProxyFromSettings(result)
	}
	return result, err
}

func (s *AppService) SelectWorkspace() (WorkspaceInfo, error) {
	current := s.activeWorkspacePath()
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
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		cleanPath, err := s.importSelectedImageAttachment(path)
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

type ComposerFileSelection struct {
	Images []string `json:"images"`
	Files  []string `json:"files"`
}

// SelectComposerFiles keeps image attachments on the managed attachment path
// and returns other user-selected files as explicit context paths.
func (s *AppService) SelectComposerFiles() (ComposerFileSelection, error) {
	paths, err := s.app.Dialog.OpenFile().
		SetTitle("Add files or photos").
		CanChooseFiles(true).
		PromptForMultipleSelection()
	if err != nil {
		if isDialogCancelled(err) {
			return ComposerFileSelection{Images: []string{}, Files: []string{}}, nil
		}
		return ComposerFileSelection{}, err
	}
	result := ComposerFileSelection{Images: []string{}, Files: []string{}}
	for _, path := range paths {
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif" {
			cleanPath, importErr := s.importSelectedImageAttachment(path)
			if importErr != nil {
				return ComposerFileSelection{}, importErr
			}
			result.Images = append(result.Images, cleanPath)
			continue
		}
		absolute, absErr := filepath.Abs(strings.TrimSpace(path))
		if absErr != nil {
			return ComposerFileSelection{}, absErr
		}
		info, statErr := os.Stat(absolute)
		if statErr != nil {
			return ComposerFileSelection{}, statErr
		}
		if !info.Mode().IsRegular() {
			return ComposerFileSelection{}, errors.New("selected context must be a file")
		}
		result.Files = append(result.Files, filepath.Clean(absolute))
	}
	if len(result.Images) > 0 {
		s.mu.Lock()
		for _, path := range result.Images {
			s.allowedImages[imageAttachmentKey(path)] = struct{}{}
		}
		s.mu.Unlock()
	}
	return result, nil
}

// AttachImageData saves a pasted/dropped image into NiceCodex-managed persistent
// attachment storage and registers it for SendMessage (same allow-list as SelectImages).
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
	dir := s.managedAttachmentDir()
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
	switch normalizeRuntime(updated.ActiveRuntime) {
	case "gemini":
		updated.GeminiWorkspace = cleanPath
		updated.GeminiRecentWorkspaces = rememberWorkspace(updated.GeminiRecentWorkspaces, cleanPath)
	case "opencode":
		updated.OpenCodeWorkspace = cleanPath
		updated.OpenCodeRecentWorkspaces = rememberWorkspace(updated.OpenCodeRecentWorkspaces, cleanPath)
	default:
		updated.Workspace = cleanPath
		updated.RecentWorkspaces = rememberWorkspace(updated.RecentWorkspaces, cleanPath)
	}
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
	// Respect the active product runtime so Grok mode doesn't refresh Codex cwd.
	workspace := strings.TrimSpace(s.activeWorkspacePath())
	if workspace == "" {
		return WorkspaceInfo{}, errors.New("no workspace is selected")
	}
	return inspectWorkspace(workspace), nil
}

func (s *AppService) StartCodex(workspace string) error {
	s.codexLifecycleMu.Lock()
	defer s.codexLifecycleMu.Unlock()

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
	// A single app-server can serve threads from multiple workspaces via the
	// per-request cwd. StartCodex is also used by project switching, so it must
	// be idempotent: never tear down a running process and interrupt its turn.
	// Call StopCodex explicitly before this method when a true reconnect is needed.
	if client.Status().Running {
		return nil
	}
	settings := s.Settings()
	name := strings.TrimSpace(settings.CodexClientName)
	title := strings.TrimSpace(settings.CodexClientTitle)
	version := strings.TrimSpace(settings.CodexClientVersion)
	// Best-effort: stamp provider http_headers.User-Agent so custom base_url
	// reverse proxies see an official Codex Desktop identity.
	cliVersion := ""
	if detection := codex.Detect(); detection.Available {
		cliVersion = detection.Version
	}
	if err := ensureCodexProviderUserAgent(name, title, version, cliVersion); err != nil {
		// Non-fatal: app-server clientInfo + originator env still apply.
		_ = err
	}
	return client.Start(s.app.Context(), cleanPath, codex.ClientInfo{
		Name:    name,
		Title:   title,
		Version: version,
	})
}

func (s *AppService) StopCodex() error {
	s.codexLifecycleMu.Lock()
	defer s.codexLifecycleMu.Unlock()

	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return nil
	}
	s.mu.Lock()
	s.codexActiveTurns = make(map[string]string)
	s.codexPendingDispatches = make(map[string]bool)
	s.mu.Unlock()
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
	return s.listThreadsForWorkspace(activeWorkspaceForRuntime(settings), search)
}

func (s *AppService) ListWorkspaceThreads(workspace string, search string) (map[string]any, error) {
	return s.ListRuntimeWorkspaceThreads(normalizeRuntime(s.Settings().ActiveRuntime), workspace, search)
}

func (s *AppService) ListRuntimeWorkspaceThreads(runtimeID string, workspace string, search string) (map[string]any, error) {
	cleanWorkspace, err := validateWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	runtimeID = normalizeRuntime(runtimeID)
	settings := s.Settings()
	activeWorkspace := workspaceForRuntime(settings, runtimeID)
	allowed := samePath(cleanWorkspace, activeWorkspace)
	if !allowed {
		for _, recent := range recentWorkspacesForRuntimeID(settings, runtimeID) {
			if samePath(cleanWorkspace, recent) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return nil, errors.New("workspace is not in the recent workspace list")
	}
	return s.listThreadsForWorkspaceRuntime(cleanWorkspace, search, runtimeID)
}

func (s *AppService) listThreadsForWorkspace(workspace string, search string) (map[string]any, error) {
	settings := s.Settings()
	return s.listThreadsForWorkspaceRuntime(workspace, search, normalizeRuntime(settings.ActiveRuntime))
}

func (s *AppService) listThreadsForWorkspaceRuntime(workspace string, search string, runtimeID string) (map[string]any, error) {
	settings := s.Settings()
	workMode := normalizeWorkMode(settings.WorkMode)
	activeRuntime := normalizeRuntime(runtimeID)
	if activeRuntime != "codex" {
		workMode = "code"
	}
	// Only Codex owns an app-server thread index. External runtimes use the
	// NiceCodex session store and must not probe a stopped Codex process during
	// every runtime/workspace switch (which otherwise adds a long timeout).
	if activeRuntime == "codex" {
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
	}
	if activeRuntime == "gemini" || activeRuntime == "opencode" {
		s.syncNativeExternalSessions(activeRuntime, workspace)
	}
	return s.listSessionsForWorkspaceRuntimeFiltered(workspace, search, workMode, false, activeRuntime), nil
}

func (s *AppService) UpdateSessionPreferences(request SessionPreferencesRequest) error {
	sessionID := strings.TrimSpace(request.SessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	if strings.HasPrefix(sessionID, "pending-thread-") {
		return nil
	}
	model := strings.TrimSpace(request.Model)
	effort := strings.TrimSpace(request.Effort)
	collaborationMode := normalizeCollaborationMode(request.CollaborationMode)
	if len(model) > 160 || len(effort) > 64 {
		return errors.New("session preferences are too long")
	}
	goal := ""
	if request.Goal != nil {
		goal = normalizeSessionGoal(*request.Goal)
		if utf8.RuneCountInString(goal) > 4_000 {
			return errors.New("session goal is too long")
		}
	}
	goalStatus := ""
	if request.GoalStatus != nil {
		goalStatus = normalizeSessionGoalStatus(*request.GoalStatus)
		if goalStatus == "" {
			return errors.New("invalid session goal status")
		}
	}
	if request.GoalTokenBudgetSet && request.GoalTokenBudget != nil {
		if *request.GoalTokenBudget <= 0 || *request.GoalTokenBudget > 1_000_000_000 {
			return errors.New("session goal token budget is out of range")
		}
	}
	goalChanged := request.Goal != nil || request.GoalStatus != nil || request.GoalTokenBudgetSet
	now := time.Now().Unix()
	s.mu.Lock()
	record := s.sessions[sessionID]
	if record == nil || record.Archived {
		s.mu.Unlock()
		return errors.New("session not found")
	}
	previousGoalSession := cloneSession(record)
	effectiveGoal := normalizeSessionGoal(record.Goal)
	if request.Goal != nil {
		effectiveGoal = goal
	}
	if effectiveGoal == "" && (request.GoalStatus != nil || request.GoalTokenBudgetSet) {
		s.mu.Unlock()
		return errors.New("set a session goal before changing its status or budget")
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
	if request.Goal != nil {
		previousGoal := normalizeSessionGoal(record.Goal)
		if goal != previousGoal && goal != "" {
			record.GoalSynced = false
			record.GoalTokensUsed = 0
			record.GoalTimeUsedSeconds = 0
			record.GoalCreatedAt = now
			if request.GoalStatus == nil {
				record.GoalStatus = "active"
			}
		}
		record.Goal = goal
		if goal == "" {
			// Keep GoalSynced until thread/goal/clear succeeds; it records that the
			// native thread may still need its old objective removed.
			record.GoalStatus = ""
			record.GoalTokenBudget = nil
			record.GoalTokensUsed = 0
			record.GoalTimeUsedSeconds = 0
			record.GoalCreatedAt = 0
			record.GoalUpdatedAt = 0
		}
	}
	if record.Goal != "" {
		if request.GoalStatus != nil {
			record.GoalStatus = goalStatus
		}
		if normalizeSessionGoalStatus(record.GoalStatus) == "" {
			record.GoalStatus = "active"
		}
		if request.GoalTokenBudgetSet {
			if request.GoalTokenBudget == nil {
				record.GoalTokenBudget = nil
			} else {
				budget := *request.GoalTokenBudget
				record.GoalTokenBudget = &budget
			}
		}
		if goalChanged {
			record.GoalUpdatedAt = now
		}
	}
	record.UpdatedAt = now
	s.persistSessionsLocked()
	goalSession := cloneSession(record)
	s.mu.Unlock()
	if goalChanged && !isExternalSession(goalSession) && strings.TrimSpace(goalSession.BackendRef) != "" {
		needsSync := goalSession.Goal != "" || goalSession.GoalSynced
		if needsSync {
			if nativeGoal, ok := s.syncNativeCodexGoal(goalSession, goalSession.BackendRef); ok {
				s.applyNativeGoalForSession(goalSession.ID, nativeGoal)
			} else if previousGoalSession.GoalSynced {
				s.restoreSessionGoal(previousGoalSession)
				return errors.New("Codex could not update the native session goal")
			}
		}
	}
	return nil
}

func (s *AppService) CreateThread() (map[string]any, error) {
	settings := s.Settings()
	runtimeID := normalizeRuntime(settings.ActiveRuntime)
	workspace, err := validateWorkspace(workspaceForRuntime(settings, runtimeID))
	if err != nil {
		return nil, err
	}
	return s.createThreadForRuntime(settings, runtimeID, workspace)
}

// CreateRuntimeThread allocates a lazy local session for one arena pane without
// relying on the globally focused runtime changing while another pane is sending.
func (s *AppService) CreateRuntimeThread(runtimeID string, workspace string) (map[string]any, error) {
	runtimeID = normalizeRuntime(runtimeID)
	if runtimeID != "codex" && runtimeID != "gemini" && runtimeID != "opencode" {
		return nil, errors.New("runtime does not use Codex-compatible sessions")
	}
	cleanWorkspace, err := validateWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	return s.createThreadForRuntime(s.Settings(), runtimeID, cleanWorkspace)
}

func (s *AppService) createThreadForRuntime(settings UserSettings, runtimeID string, workspace string) (map[string]any, error) {
	workMode := normalizeWorkMode(settings.WorkMode)
	collaborationMode := strings.TrimSpace(settings.CollaborationMode)
	if collaborationMode == "" {
		collaborationMode = "default"
	}
	if workMode == "cowork" && collaborationMode == "default" {
		collaborationMode = "plan"
	}

	provider := ""
	providerID := ""
	model := settings.Model
	effort := settings.Effort
	switch normalizeRuntime(runtimeID) {
	case "gemini":
		provider, providerID = "gemini", externalProviderID("gemini")
		model, effort = settings.GeminiModel, settings.GeminiEffort
	case "opencode":
		provider, providerID = "opencode", externalProviderID("opencode")
		model, effort = settings.OpenCodeModel, settings.OpenCodeEffort
	}
	if provider != "" {
		// Collaboration/work modes are Codex concepts. External runtimes keep
		// their own native execution mode and must not inherit a Codex Plan state.
		collaborationMode = "default"
		workMode = "code"
	}
	if model == "" || effort == "" {
		models, efforts := discoverProviderCatalog(provider)
		if model == "" && len(models) > 0 {
			model = models[0].Model
		}
		if effort == "" && len(efforts) > 0 {
			effort = efforts[0].Effort
		}
	}
	// App-server threads and external CLI sessions are both allocated lazily on
	// the first send; the local UUID remains stable for queue/history ownership.
	record := s.createSessionRecord(workspace, provider, providerID, model, effort, collaborationMode, workMode)
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
	workspace := activeWorkspaceForRuntime(settings)
	session := s.sessionForID(threadID)
	if session != nil {
		workspace = session.Workspace
	}
	if session != nil && isExternalSession(session) {
		s.rememberThread(threadID, workspace)
		return s.sessionResponse(session), nil
	}
	// NiceCodex Codex session that has not started an app-server thread yet.
	if session != nil && session.BackendRef == "" {
		s.rememberThread(threadID, workspace)
		return s.sessionResponse(session), nil
	}
	backendID := s.codexBackendID(threadID, workspace)
	result, err := s.loadBackendThread(backendID, workspace, settings, session)
	if err != nil {
		// Fall back to local index if the backend thread is gone.
		if session != nil {
			s.rememberThread(threadID, workspace)
			return s.sessionResponse(session), nil
		}
		return nil, err
	}
	if session != nil && (session.GoalSynced || normalizeSessionGoal(session.Goal) == "") {
		session = s.refreshNativeCodexGoal(session, backendID)
	}
	s.rememberThread(threadID, workspace)
	s.attachSessionIdentity(result, session, threadID)
	return result, nil
}

func (s *AppService) ForkThread(threadID string) (map[string]any, error) {
	threadID = strings.TrimSpace(threadID)
	settings := s.Settings()
	source := s.sessionForID(threadID)
	workspace := activeWorkspaceForRuntime(settings)
	if source != nil {
		workspace = source.Workspace
	}
	workspace, err := validateWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	if source != nil && isExternalSession(source) {
		return s.forkExternalSession(source)
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
		record.Goal = source.Goal
		record.GoalStatus = source.GoalStatus
		if source.GoalTokenBudget != nil {
			budget := *source.GoalTokenBudget
			record.GoalTokenBudget = &budget
		}
		record.GoalCreatedAt = now
		record.GoalUpdatedAt = now
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
	workspace := s.activeWorkspacePath()
	session := s.sessionForID(threadID)
	if session != nil {
		workspace = session.Workspace
	}
	if session != nil && isExternalSession(session) && s.externalSessionIsRunning(threadID) {
		return errors.New("stop the running external turn before archiving its session")
	}
	if session != nil && !isExternalSession(session) && s.codexActiveTurnID(threadID, session.BackendRef) != "" {
		return errors.New("stop the running Codex turn before archiving its session")
	}
	// Local directory is authoritative.
	s.markSessionArchived(threadID)
	if session == nil || isExternalSession(session) || session.BackendRef == "" {
		s.dropCodexHistoryCache(threadID)
		return nil
	}
	backendID := session.BackendRef
	s.dropCodexHistoryCache(backendID)
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
	session := s.sessionForIDAny(threadID)
	if session == nil {
		return nil, errors.New("session not found")
	}
	workspace := session.Workspace
	if isExternalSession(session) {
		restored := s.markSessionUnarchived(threadID)
		if restored == nil {
			return nil, errors.New("session not found")
		}
		s.rememberThread(restored.ID, workspace)
		return s.sessionResponse(restored), nil
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
	session := s.sessionForIDAny(threadID)
	workspace := s.activeWorkspacePath()
	if session != nil {
		workspace = session.Workspace
	}
	if session != nil && isExternalSession(session) {
		if s.externalSessionIsRunning(threadID) {
			return errors.New("stop the running external turn before deleting its session")
		}
		if err := s.deleteNativeExternalSession(session); err != nil {
			return err
		}
	}
	if session != nil && !isExternalSession(session) && s.codexActiveTurnID(threadID, session.BackendRef) != "" {
		return errors.New("stop the running Codex turn before deleting its session")
	}
	deleted := s.deleteSession(threadID)
	if deleted == nil && session == nil {
		return errors.New("session not found")
	}
	if session != nil && !isExternalSession(session) && session.BackendRef != "" {
		s.dropCodexHistoryCache(session.BackendRef)
		if err := s.ensureThreadInWorkspace(session.BackendRef, workspace); err == nil {
			_, _ = s.call("thread/delete", map[string]any{"threadId": session.BackendRef})
		}
	}
	s.dropCodexHistoryCache(threadID)
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
	session := s.sessionForID(threadID)
	if session == nil {
		return nil, errors.New("session not found")
	}
	workspace := session.Workspace
	if isExternalSession(session) {
		updated := s.renameSession(threadID, name)
		if updated == nil {
			return nil, errors.New("session not found")
		}
		s.rememberThread(updated.ID, workspace)
		return s.sessionResponse(updated), nil
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
	session := s.sessionForID(threadID)
	workspace := s.activeWorkspacePath()
	if session != nil {
		workspace = session.Workspace
	}
	if session != nil && isExternalSession(session) {
		return nil, errors.New("change review is available only for Codex sessions")
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
	workspace, err := validateWorkspace(activeWorkspaceForRuntime(settings))
	if err != nil {
		return map[string]any{"data": []any{}}, nil
	}
	return s.listArchivedSessionsForWorkspace(workspace, search, settings.WorkMode), nil
}

func (s *AppService) CompactThread(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	workspace := s.activeWorkspacePath()
	session := s.sessionForID(threadID)
	if session != nil {
		workspace = session.Workspace
	}
	if session != nil && isExternalSession(session) {
		return s.compactExternalSession(session)
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
	workspace := s.activeWorkspacePath()
	session := s.sessionForID(threadID)
	if session != nil {
		workspace = session.Workspace
	}
	if session != nil && isExternalSession(session) {
		return s.rollbackExternalSession(session, numTurns)
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
	settings := s.Settings()
	workspace := activeWorkspaceForRuntime(settings)
	session := s.sessionForID(threadID)
	if session != nil {
		workspace = session.Workspace
	}
	if session != nil && isExternalSession(session) {
		s.rememberThread(threadID, workspace)
		if session.Native {
			page, err := s.readNativeExternalHistoryPage(session, -1)
			if err != nil {
				return nil, err
			}
			return s.nativeExternalSessionResponsePage(session, page), nil
		}
		return s.externalSessionResponsePage(session, -1), nil
	}
	if session != nil && session.BackendRef == "" {
		s.rememberThread(threadID, workspace)
		return paginateCodexThreadResponse(s.sessionResponse(session), -1), nil
	}
	backendID := s.codexBackendID(threadID, workspace)
	// Switching projects while another long turn is running should not force a
	// second full thread/resume round-trip. The Go history cache already contains
	// the complete snapshot from the previous open and the frontend will merge
	// any newer in-flight items on top of this page.
	if session != nil {
		if turns, ok := s.cachedCodexHistory(backendID); ok {
			response := s.sessionResponse(session)
			if thread, ok := response["thread"].(map[string]any); ok {
				thread["turns"] = turns
			}
			return paginateCodexThreadResponse(response, -1), nil
		}
	}
	// Opening history does not need to mutate app-server thread state. A read-only
	// snapshot is substantially faster while another project owns a long running
	// turn; SendMessage still resumes the backend thread immediately before send.
	result, err := s.readBackendThreadSnapshot(backendID, workspace)
	if err != nil {
		// Older app-server versions can reject includeTurns. Preserve the proven
		// resume path as a compatibility fallback.
		result, err = s.loadBackendThread(backendID, workspace, settings, session)
	}
	if err != nil {
		if session != nil {
			s.rememberThread(threadID, workspace)
			return paginateCodexThreadResponse(s.sessionResponse(session), -1), nil
		}
		return nil, err
	}
	if session != nil && (session.GoalSynced || normalizeSessionGoal(session.Goal) == "") {
		session = s.refreshNativeCodexGoal(session, backendID)
	}
	s.attachSessionIdentity(result, session, threadID)
	s.rememberThread(threadID, workspace)
	s.cacheCodexHistory(backendID, codexThreadTurns(result))
	return paginateCodexThreadResponse(result, -1), nil
}

// ReadThreadLiveness bypasses ReadThread's display cache and answers only
// whether this exact session still owns backend work.
func (s *AppService) ReadThreadLiveness(threadID string) (TurnLivenessView, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return TurnLivenessView{}, errors.New("thread id is required")
	}
	settings := s.Settings()
	workspace := activeWorkspaceForRuntime(settings)
	session := s.sessionForID(threadID)
	if session != nil {
		workspace = session.Workspace
	}
	if session != nil && isExternalSession(session) {
		runtime := normalizeExternalRuntime(session.Provider)
		if runtime == "" {
			runtime = normalizeExternalRuntime(externalProviderKind(session.ProviderID))
		}
		if runtime == "" {
			runtime = "external"
		}
		s.mu.Lock()
		run := s.externalRuns[session.ID]
		view := TurnLivenessView{Runtime: runtime, State: "idle"}
		if count := len(session.Turns); count > 0 {
			latest := session.Turns[count-1]
			view.LatestTurnID = strings.TrimSpace(latest.ID)
			view.LatestTurnStatus = lifecycleStatus(latest.Status)
		}
		if run != nil {
			view.Running = true
			view.TurnID = strings.TrimSpace(run.turnID)
			view.State = "running"
		}
		s.mu.Unlock()
		return view, nil
	}

	backendID := s.codexBackendID(threadID, workspace)
	expectedTurnID, pending := s.codexLivenessLocalState(threadID, backendID)
	if pending {
		return TurnLivenessView{
			Running: true,
			TurnID:  concreteTurnID(expectedTurnID),
			Runtime: "codex",
			State:   "submitting",
		}, nil
	}
	if backendID == "" {
		return TurnLivenessView{Runtime: "codex", State: "idle"}, nil
	}

	result, err := s.readBackendThreadSnapshot(backendID, workspace)
	if err != nil {
		return TurnLivenessView{}, err
	}
	thread, ok := result["thread"].(map[string]any)
	if !ok {
		return TurnLivenessView{}, errors.New("Codex returned an invalid thread snapshot")
	}
	threadStatus := lifecycleStatus(thread["status"])
	latestTurnID, latestTurnStatus := latestLifecycleTurn(thread["turns"])
	running := lifecycleStatusIsActive(threadStatus)
	runningTurnID := ""
	if running {
		runningTurnID = strings.TrimSpace(stringFromAny(thread["activeTurnId"]))
		if runningTurnID == "" || runningTurnID == "active" {
			runningTurnID = activeLifecycleTurnID(thread["turns"])
		}
		if runningTurnID == "" {
			runningTurnID = concreteTurnID(expectedTurnID)
		}
		s.syncCodexLivenessActive(threadID, backendID, expectedTurnID, runningTurnID)
	} else {
		s.clearCodexLivenessIdle(threadID, backendID, expectedTurnID)
	}
	state := threadStatus
	if state == "" {
		state = "idle"
	}
	return TurnLivenessView{
		Running:          running,
		TurnID:           runningTurnID,
		LatestTurnID:     latestTurnID,
		LatestTurnStatus: latestTurnStatus,
		Runtime:          "codex",
		State:            state,
	}, nil
}

// ReadThreadHistory returns the page immediately before a previously opened
// Codex page. The complete app-server snapshot stays in Go so the webview never
// has to deserialize an entire long conversation at once.
func (s *AppService) ReadThreadHistory(threadID string, before int) (map[string]any, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("thread id is required")
	}
	settings := s.Settings()
	workspace := activeWorkspaceForRuntime(settings)
	session := s.sessionForID(threadID)
	if session != nil {
		workspace = session.Workspace
	}
	if session == nil && !s.threadAllowed(threadID, workspace) {
		return nil, errors.New("open this thread in the current workspace before reading its history")
	}
	if session != nil && isExternalSession(session) {
		if session.Native {
			page, err := s.readNativeExternalHistoryPage(session, before)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"turns":             nativeExternalTurnMaps(page.Turns),
				"historyStart":      page.Start,
				"historyTotal":      page.Total,
				"historyTurnOffset": page.Start,
				"hasEarlier":        page.Start > 0,
			}, nil
		}
		return externalTurnPage(session.Turns, before), nil
	}
	if session != nil && session.BackendRef == "" {
		return codexTurnsPage(nil, before), nil
	}
	backendID := s.codexBackendID(threadID, workspace)
	turns, ok := s.cachedCodexHistory(backendID)
	if !ok {
		if _, err := s.ReadThread(threadID); err != nil {
			return nil, err
		}
		turns, ok = s.cachedCodexHistory(backendID)
		if !ok {
			return nil, errors.New("thread history is temporarily unavailable")
		}
	}
	return codexTurnsPage(turns, before), nil
}

func codexThreadTurns(response map[string]any) []any {
	thread, _ := response["thread"].(map[string]any)
	turns, _ := thread["turns"].([]any)
	return turns
}

func paginateCodexThreadResponse(response map[string]any, before int) map[string]any {
	next := make(map[string]any, len(response)+4)
	for key, value := range response {
		next[key] = value
	}
	thread, _ := response["thread"].(map[string]any)
	threadCopy := make(map[string]any, len(thread))
	for key, value := range thread {
		threadCopy[key] = value
	}
	page := codexTurnsPage(codexThreadTurns(response), before)
	threadCopy["turns"] = page["turns"]
	next["thread"] = threadCopy
	for _, key := range []string{"historyStart", "historyTotal", "historyTurnOffset", "hasEarlier"} {
		next[key] = page[key]
	}
	return next
}

func codexTurnsPage(turns []any, before int) map[string]any {
	total := len(turns)
	end := before
	if end < 0 || end > total {
		end = total
	}
	if end < 0 {
		end = 0
	}
	start := end - conversationHistoryPageTurns
	if start < 0 {
		start = 0
	}
	page := append([]any(nil), turns[start:end]...)
	return map[string]any{
		"turns":             page,
		"historyStart":      start,
		"historyTotal":      total,
		"historyTurnOffset": start,
		"hasEarlier":        start > 0,
	}
}

func (s *AppService) cacheCodexHistory(backendID string, turns []any) {
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return
	}
	weight := estimateConversationValueWeight(turns)
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.codexHistoryCache == nil {
		s.codexHistoryCache = make(map[string]*codexHistorySnapshot)
	}
	if weight > conversationHistoryEntryBytes {
		delete(s.codexHistoryCache, backendID)
		return
	}
	s.codexHistoryCache[backendID] = &codexHistorySnapshot{
		turns:     append([]any(nil), turns...),
		weight:    weight,
		touchedAt: time.Now(),
	}
	s.pruneConversationHistoryCachesLocked()
}

func (s *AppService) cachedCodexHistory(backendID string) ([]any, bool) {
	backendID = strings.TrimSpace(backendID)
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	snapshot := s.codexHistoryCache[backendID]
	if snapshot == nil {
		return nil, false
	}
	snapshot.touchedAt = time.Now()
	return snapshot.turns, true
}

func (s *AppService) dropCodexHistoryCache(backendID string) {
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return
	}
	s.historyMu.Lock()
	delete(s.codexHistoryCache, backendID)
	s.historyMu.Unlock()
}

func (s *AppService) dropClaudeHistoryCache(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.historyMu.Lock()
	delete(s.claudeHistoryCache, sessionID)
	s.historyMu.Unlock()
}

func (s *AppService) dropGrokHistoryCache(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.historyMu.Lock()
	delete(s.grokHistoryCache, sessionID)
	s.historyMu.Unlock()
}

func (s *AppService) pruneConversationHistoryCachesLocked() {
	for len(s.codexHistoryCache) > codexHistoryCacheLimit {
		id := oldestCodexHistoryID(s.codexHistoryCache)
		if id == "" {
			break
		}
		delete(s.codexHistoryCache, id)
	}
	for len(s.claudeHistoryCache) > conversationHistoryCacheLimit {
		id := oldestClaudeHistoryID(s.claudeHistoryCache)
		if id == "" {
			break
		}
		delete(s.claudeHistoryCache, id)
	}
	for len(s.grokHistoryCache) > conversationHistoryCacheLimit {
		id := oldestGrokHistoryID(s.grokHistoryCache)
		if id == "" {
			break
		}
		delete(s.grokHistoryCache, id)
	}

	for conversationHistoryWeight(s) > conversationHistoryCacheBytes {
		kind, id := oldestConversationHistory(s)
		if id == "" {
			break
		}
		switch kind {
		case "codex":
			delete(s.codexHistoryCache, id)
		case "claude":
			delete(s.claudeHistoryCache, id)
		case "grok":
			delete(s.grokHistoryCache, id)
		}
	}
}

func oldestCodexHistoryID(cache map[string]*codexHistorySnapshot) string {
	var id string
	var touched time.Time
	for key, snapshot := range cache {
		if snapshot != nil && (id == "" || snapshot.touchedAt.Before(touched)) {
			id, touched = key, snapshot.touchedAt
		}
	}
	return id
}

func oldestClaudeHistoryID(cache map[string]*claudeHistorySnapshot) string {
	var id string
	var touched time.Time
	for key, snapshot := range cache {
		if snapshot != nil && (id == "" || snapshot.touchedAt.Before(touched)) {
			id, touched = key, snapshot.touchedAt
		}
	}
	return id
}

func oldestGrokHistoryID(cache map[string]*grokHistorySnapshot) string {
	var id string
	var touched time.Time
	for key, snapshot := range cache {
		if snapshot != nil && (id == "" || snapshot.touchedAt.Before(touched)) {
			id, touched = key, snapshot.touchedAt
		}
	}
	return id
}

func conversationHistoryWeight(s *AppService) int64 {
	var total int64
	for _, snapshot := range s.codexHistoryCache {
		if snapshot != nil {
			total += snapshot.weight
		}
	}
	for _, snapshot := range s.claudeHistoryCache {
		if snapshot != nil {
			total += snapshot.weight
		}
	}
	for _, snapshot := range s.grokHistoryCache {
		if snapshot != nil {
			total += snapshot.weight
		}
	}
	return total
}

func oldestConversationHistory(s *AppService) (string, string) {
	kind, id := "", ""
	var touched time.Time
	consider := func(nextKind, nextID string, next time.Time) {
		if nextID != "" && (id == "" || next.Before(touched)) {
			kind, id, touched = nextKind, nextID, next
		}
	}
	if nextID := oldestCodexHistoryID(s.codexHistoryCache); nextID != "" {
		consider("codex", nextID, s.codexHistoryCache[nextID].touchedAt)
	}
	if nextID := oldestClaudeHistoryID(s.claudeHistoryCache); nextID != "" {
		consider("claude", nextID, s.claudeHistoryCache[nextID].touchedAt)
	}
	if nextID := oldestGrokHistoryID(s.grokHistoryCache); nextID != "" {
		consider("grok", nextID, s.grokHistoryCache[nextID].touchedAt)
	}
	return kind, id
}

func estimateConversationValueWeight(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return int64(len(typed)) + 16
	case []byte:
		return int64(len(typed)) + 24
	case json.RawMessage:
		return int64(len(typed)) + 24
	case []any:
		total := int64(24 + len(typed)*8)
		for _, item := range typed {
			total += estimateConversationValueWeight(item)
		}
		return total
	case map[string]any:
		total := int64(48)
		for key, item := range typed {
			total += int64(len(key)+24) + estimateConversationValueWeight(item)
		}
		return total
	default:
		return 32
	}
}

func conversationMessagePageBounds(total, before int, isUser func(index int) bool) (start, end, turnOffset int) {
	end = before
	if end < 0 || end > total {
		end = total
	}
	if end < 0 {
		end = 0
	}
	start = end
	turns := 0
	for start > 0 {
		start--
		if isUser(start) {
			turns++
			if turns >= conversationHistoryPageTurns {
				break
			}
		}
	}
	for index := 0; index < start; index++ {
		if isUser(index) {
			turnOffset++
		}
	}
	return start, end, turnOffset
}

func (s *AppService) SendMessage(request SendMessageRequest) (map[string]any, error) {
	request.ThreadID = strings.TrimSpace(request.ThreadID)
	if request.ThreadID == "" {
		return nil, errors.New("thread id is required")
	}
	settings := s.Settings()
	session := s.sessionForID(request.ThreadID)
	workspace := activeWorkspaceForRuntime(settings)
	if session != nil {
		workspace = session.Workspace
	}
	workspace, err := validateWorkspace(workspace)
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

	if session != nil && isExternalSession(session) {
		return s.runExternalTurn(request.ThreadID, session.Provider, workspace, settings, request.Text, request.Images)
	}
	if !s.claimCodexDispatch(request.ThreadID) {
		return nil, errors.New("Codex turn is already being submitted; message was kept in queue")
	}
	backendIDForDispatch := ""
	defer func() {
		s.setCodexDispatchPending(false, request.ThreadID, backendIDForDispatch)
	}()

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
	// Two local session records can temporarily alias the same backend thread
	// after history reconciliation. Claim that backend identity as well so the
	// aliases cannot dispatch parallel turns into one Codex thread.
	if backendID != request.ThreadID {
		if !s.claimCodexDispatch(backendID) {
			return nil, errors.New("Codex turn is already being submitted; message was kept in queue")
		}
		backendIDForDispatch = backendID
	}
	// The frontend normally serializes turns from event state, but an event can
	// be delayed or lost on a busy WebView. The backend registry is authoritative
	// at the dispatch boundary and prevents a second parallel turn/start.
	if busyTurnID := s.codexActiveTurnID(request.ThreadID, backendID); busyTurnID != "" {
		return nil, fmt.Errorf("Codex turn is already running (turn %s); message was kept in queue", busyTurnID)
	}

	// App-server process restarts drop in-memory threads; turn/start then 422s
	// "thread not found". Existing sessions must be resumed once before sending.
	if session == nil || session.BackendRef != "" {
		if _, err := s.loadBackendThread(backendID, workspace, settings, session); err != nil {
			return nil, fmt.Errorf("load thread for send: %w", err)
		}
	}
	if session != nil && (session.GoalSynced || normalizeSessionGoal(session.Goal) == "") {
		session = s.refreshNativeCodexGoal(session, backendID)
	}
	goalManagedByCodex := session != nil && session.GoalSynced && normalizeSessionGoal(session.Goal) != ""
	if session != nil {
		goalNeedsSync := (session.Goal != "" && !session.GoalSynced) || (session.Goal == "" && session.GoalSynced)
		if goalNeedsSync {
			if nativeGoal, ok := s.syncNativeCodexGoal(session, backendID); ok {
				s.applyNativeGoalForSession(session.ID, nativeGoal)
				session = s.sessionForIDAny(session.ID)
				goalManagedByCodex = session != nil && session.GoalSynced && normalizeSessionGoal(session.Goal) != ""
			}
		}
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

	model = strings.TrimSpace(model)
	if model == "" {
		model = defaultCodexModel
	}
	params := map[string]any{
		"threadId":       backendID,
		"input":          input,
		"cwd":            workspace,
		"approvalPolicy": normalizeApproval(settings.ApprovalPolicy),
		"sandboxPolicy":  sandboxPolicy(settings.Sandbox, workspace),
		"model":          model,
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
	resetNonce := 0
	if session != nil {
		resetNonce = session.CollabResetNonce
	}
	developerInstructions := collaborationModeDeveloperInstructions(collaborationMode, resetNonce)
	guidanceParts := make([]string, 0, 2)
	if !goalManagedByCodex {
		if guidance := sessionGoalGuidance(session); guidance != "" {
			guidanceParts = append(guidanceParts, guidance)
		}
	}
	if guidance := sessionMemoryGuidance(session); guidance != "" {
		guidanceParts = append(guidanceParts, guidance)
	}
	if len(guidanceParts) > 0 {
		guidance := strings.Join(guidanceParts, "\n\n")
		if text, ok := developerInstructions.(string); ok && strings.TrimSpace(text) != "" {
			developerInstructions = strings.TrimSpace(text) + "\n\n" + guidance
		} else {
			// A goal still needs to reach Plan turns, where Codex normally uses
			// its built-in instructions represented by a null developer value.
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
	if err != nil && isThreadNotFoundError(err) {
		// Race: process restarted between load and turn/start — resume once more.
		if _, loadErr := s.loadBackendThread(backendID, workspace, settings, session); loadErr == nil {
			result, err = s.call("turn/start", params)
		}
	}
	if err != nil {
		return nil, err
	}
	if turn, ok := result["turn"].(map[string]any); ok {
		if turnID := strings.TrimSpace(stringFromAny(turn["id"])); turnID != "" {
			s.mu.Lock()
			if s.codexActiveTurns == nil {
				s.codexActiveTurns = make(map[string]string)
			}
			// remapCodexEvent rewrites lifecycle events to the NiceCodex session id.
			// Keep the dispatch guard under that same id so terminal events always
			// remove the exact entry that was registered here.
			s.codexActiveTurns[request.ThreadID] = turnID
			s.mu.Unlock()
		}
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
	settings := s.Settings()
	session := s.sessionForID(request.ThreadID)
	workspace := settings.Workspace
	if session != nil {
		workspace = session.Workspace
	}
	workspace, err := validateWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	if !s.threadAllowed(request.ThreadID, workspace) {
		return nil, errors.New("open this thread in the current workspace before steering the turn")
	}
	if session != nil && isExternalSession(session) {
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
	images, err := s.validateImageAttachments(images)
	if err != nil {
		return nil, err
	}

	input := make([]any, 0, len(images)+1)
	for _, path := range images {
		input = append(input, map[string]any{"type": "localImage", "path": path})
	}
	if text == "" && len(input) == 0 {
		return nil, errors.New("message cannot be empty")
	}
	if text != "" {
		input = append(input, map[string]any{"type": "text", "text": text, "text_elements": []any{}})
	}
	return input, nil
}

func (s *AppService) validateImageAttachments(images []string) ([]string, error) {
	validated := make([]string, 0, len(images))
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
		if !allowed && !s.isManagedImageAttachment(cleanPath) {
			return nil, errors.New("select image attachments through Nice Codex before sending")
		}
		if !allowed {
			s.mu.Lock()
			s.allowedImages[key] = struct{}{}
			s.mu.Unlock()
		}
		seenImages[key] = struct{}{}
		validated = append(validated, cleanPath)
	}
	return validated, nil
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
	if !allowed && !s.isManagedImageAttachment(cleanPath) {
		return "", errors.New("select image attachments through Nice Codex before previewing")
	}
	if !allowed {
		s.mu.Lock()
		s.allowedImages[key] = struct{}{}
		s.mu.Unlock()
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
	if err == nil {
		return nil
	}

	// turn/completed can race with a user's stop click, and a missed lifecycle
	// event can leave the WebView holding an older turn id. Reconcile against the
	// authoritative thread snapshot so an already-finished turn is idempotent and
	// a changed active turn can still be stopped without affecting another thread.
	liveness, livenessErr := s.ReadThreadLiveness(threadID)
	if livenessErr != nil || !liveness.Running {
		if livenessErr == nil {
			return nil
		}
		return err
	}
	activeTurnID := strings.TrimSpace(liveness.TurnID)
	if activeTurnID == "" || activeTurnID == turnID {
		return err
	}
	_, retryErr := s.callWithTimeout("turn/interrupt", map[string]any{
		"threadId": backendID,
		"turnId":   activeTurnID,
	}, 8*time.Second)
	return retryErr
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
	result, err := s.call("plugin/list", map[string]any{"cwds": []string{workspace}})
	if err != nil {
		return nil, err
	}
	if s.pluginAssets != nil {
		s.pluginAssets.updatePluginIconURLs(result)
	}
	return result, nil
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

func (s *AppService) SetSkillEnabled(request SkillConfigRequest) (map[string]any, error) {
	name := strings.TrimSpace(request.Name)
	path := strings.TrimSpace(request.Path)
	if name == "" && path == "" {
		return nil, errors.New("skill name or path is required")
	}
	params := map[string]any{"enabled": request.Enabled}
	if path != "" {
		params["path"] = path
	} else {
		params["name"] = name
	}
	return s.call("skills/config/write", params)
}

func (s *AppService) ListApps() (map[string]any, error) {
	return s.call("app/list", map[string]any{"forceRefetch": true, "limit": 100})
}

func (s *AppService) ListMCPServers() (map[string]any, error) {
	servers, err := s.readMCPServerConfigs()
	if err != nil {
		return nil, err
	}
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
		env := map[string]any{}
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
			if values, ok := server["env"].(map[string]any); ok {
				env = values
			}
		}
		data = append(data, map[string]any{
			"name":         name,
			"enabled":      enabled,
			"command":      command,
			"url":          url,
			"transport":    transport,
			"args":         args,
			"env":          env,
			"statusLoaded": false,
		})
	}
	return map[string]any{"data": data}, nil
}

func (s *AppService) readMCPServerConfigs() (map[string]any, error) {
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
	if servers == nil {
		servers = map[string]any{}
	}
	return servers, nil
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

type MCPServersImportRequest struct {
	Servers []MCPServerWriteRequest `json:"servers"`
}

func (s *AppService) UpsertMCPServer(request MCPServerWriteRequest) error {
	name, value, err := mcpServerConfigValue(request)
	if err != nil {
		return err
	}
	servers, err := s.readMCPServerConfigs()
	if err != nil {
		return err
	}
	existing, _ := servers[name].(map[string]any)
	_, err = s.call("config/batchWrite", map[string]any{
		"edits": []any{
			map[string]any{
				"keyPath":       mcpServerConfigKeyPath(name),
				"value":         mcpServerConfigReplacement(existing, value),
				"mergeStrategy": "replace",
			},
		},
		"reloadUserConfig": true,
	})
	if err != nil {
		return err
	}
	_, err = s.call("config/mcpServer/reload", nil)
	return err
}

func (s *AppService) ImportMCPServers(request MCPServersImportRequest) error {
	if len(request.Servers) == 0 {
		return errors.New("at least one MCP server is required")
	}
	if len(request.Servers) > 100 {
		return errors.New("import up to 100 MCP servers at a time")
	}
	servers, err := s.readMCPServerConfigs()
	if err != nil {
		return err
	}
	edits := make([]any, 0, len(request.Servers))
	seenNames := make(map[string]struct{}, len(request.Servers))
	for _, server := range request.Servers {
		name, value, err := mcpServerConfigValue(server)
		if err != nil {
			return err
		}
		nameKey := strings.ToLower(name)
		if _, duplicate := seenNames[nameKey]; duplicate {
			return fmt.Errorf("duplicate MCP server name: %s", name)
		}
		seenNames[nameKey] = struct{}{}
		existing, _ := servers[name].(map[string]any)
		edits = append(edits, map[string]any{
			"keyPath":       mcpServerConfigKeyPath(name),
			"value":         mcpServerConfigReplacement(existing, value),
			"mergeStrategy": "replace",
		})
	}
	_, err = s.call("config/batchWrite", map[string]any{
		"edits":            edits,
		"reloadUserConfig": true,
	})
	if err != nil {
		return err
	}
	_, err = s.call("config/mcpServer/reload", nil)
	return err
}

func mcpServerConfigValue(request MCPServerWriteRequest) (string, map[string]any, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" || len(name) > 120 || strings.ContainsAny(name, "\r\n") {
		return "", nil, errors.New("a valid MCP server name is required")
	}
	value := map[string]any{"enabled": request.Enabled}
	transport := strings.TrimSpace(request.Transport)
	serverURL := strings.TrimSpace(request.URL)
	command := strings.TrimSpace(request.Command)
	if len(command) > 2048 || len(serverURL) > 4096 || len(transport) > 64 {
		return "", nil, errors.New("MCP server configuration is too long")
	}
	if serverURL != "" {
		parsed, err := url.ParseRequestURI(serverURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return "", nil, errors.New("MCP server URL must use http or https")
		}
		if transport == "" {
			transport = "http"
		}
		value["type"] = transport
		value["url"] = serverURL
	} else if command != "" {
		value["command"] = command
		if transport != "" {
			value["type"] = transport
		}
		if len(request.Args) > 128 {
			return "", nil, errors.New("MCP server has too many arguments")
		}
		if len(request.Args) > 0 {
			args := make([]any, 0, len(request.Args))
			for _, arg := range request.Args {
				arg = strings.TrimSpace(arg)
				if len(arg) > 4096 {
					return "", nil, errors.New("MCP server argument is too long")
				}
				if arg != "" {
					args = append(args, arg)
				}
			}
			value["args"] = args
		}
	} else {
		return "", nil, errors.New("MCP server requires a command or url")
	}
	if len(request.Env) > 128 {
		return "", nil, errors.New("MCP server has too many environment variables")
	}
	if len(request.Env) > 0 {
		env := make(map[string]any, len(request.Env))
		for key, raw := range request.Env {
			key = strings.TrimSpace(key)
			if key == "" || len(key) > 256 || len(raw) > 16_384 {
				return "", nil, errors.New("invalid MCP environment variable")
			}
			env[key] = raw
		}
		if len(env) > 0 {
			value["env"] = env
		}
	}
	return name, value, nil
}

func mcpServerConfigKeyPath(name string) string {
	return "mcp_servers." + strconv.Quote(name)
}

func mcpServerConfigReplacement(existing, value map[string]any) map[string]any {
	replacement := make(map[string]any, len(existing)+len(value))
	for key, current := range existing {
		replacement[key] = current
	}
	// Preserve Codex's advanced options but remove fields owned by this editor
	// before switching between STDIO and HTTP configurations.
	for _, key := range []string{"command", "args", "url", "type", "transport", "env", "enabled"} {
		delete(replacement, key)
	}
	for key, current := range value {
		replacement[key] = current
	}
	return replacement
}

func (s *AppService) DeleteMCPServer(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return errors.New("a valid MCP server name is required")
	}
	_, err := s.call("config/batchWrite", map[string]any{
		"edits": []any{
			map[string]any{
				"keyPath":       mcpServerConfigKeyPath(name),
				"value":         nil,
				"mergeStrategy": "replace",
			},
		},
		"reloadUserConfig": true,
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

// ReadAccountUsage returns spend for the active runtime only.
// Codex may backfill from ~/.codex rollouts / ChatGPT cloud when its local bucket is empty.
// Grok backfills input/output/cache/reasoning from ~/.grok session updates.jsonl when missing.
// Claude backfills from ~/.claude/projects/**/*.jsonl assistant message.usage when missing.
func (s *AppService) ReadAccountUsage() (map[string]any, error) {
	runtime := normalizeUsageRuntime(s.Settings().ActiveRuntime)
	return s.readAccountUsageForRuntime(runtime)
}

// ReadRuntimeAccountUsage keeps split-pane usage cards scoped to their own
// provider instead of leaking the globally active provider's counters.
func (s *AppService) ReadRuntimeAccountUsage(runtimeName string) (map[string]any, error) {
	runtimeName = strings.ToLower(strings.TrimSpace(runtimeName))
	switch runtimeName {
	case "codex", "claude", "grok", "gemini", "opencode", "open-code":
		return s.readAccountUsageForRuntime(normalizeUsageRuntime(runtimeName))
	default:
		return nil, errors.New("unsupported usage runtime")
	}
}

func (s *AppService) readAccountUsageForRuntime(runtime string) (map[string]any, error) {

	// Grok: rebuild breakdown from local sessions when the bucket is empty or total-only.
	if runtime == "grok" && s.shouldRunUsageBackfill(runtime) {
		_ = s.backfillGrokUsageFromSessions()
	}
	// Claude: rebuild from official Claude Code project transcripts.
	if runtime == "claude" && s.shouldRunUsageBackfill(runtime) {
		_ = s.backfillClaudeUsageFromProjects()
	}
	// Codex: rebuild input/output/cache from ~/.codex rollouts when missing detail.
	if runtime == "codex" && s.shouldRunUsageBackfill(runtime) {
		_ = s.backfillLocalUsageFromRollouts()
	}
	// Gemini CLI and OpenCode own their historical usage outside usage.json.
	// Read the native catalog so the usage panel reflects completed sessions even
	// when the streaming event did not include a usage payload.
	var externalUsage map[string]any
	if runtime == "gemini" || runtime == "opencode" {
		// Native usage is account-wide. Session/catalog lists remain project-scoped,
		// but an empty active workspace must not make the usage panel look empty.
		externalUsage = s.readExternalUsageCached(runtime, "")
		if externalUsage != nil && !localUsageResponseEmpty(externalUsage) {
			return externalUsage, nil
		}
	}

	if local := s.localUsageSummaryFor(runtime); !localUsageResponseEmpty(local) {
		return local, nil
	}
	if externalUsage != nil {
		// A reachable native store with zero counters is still a valid result. It
		// lets the UI distinguish "no recorded tokens yet" from a failed read.
		return externalUsage, nil
	}

	// Cloud seed is Codex-only (ChatGPT account usage).
	if runtime == "codex" {
		cloud, err := s.call("account/usage/read", nil)
		if err == nil && len(cloud) > 0 {
			_ = s.seedLocalUsageFromCloud(cloud)
			if local := s.localUsageSummaryFor("codex"); !localUsageResponseEmpty(local) {
				return local, nil
			}
			return cloud, nil
		}
	}

	// Empty object => frontend normalizeAccountUsage(null); do not return fake zeros.
	return map[string]any{"runtime": runtime, "source": "local"}, nil
}

func (s *AppService) readExternalUsageCached(runtimeName, workspace string) map[string]any {
	runtimeName = normalizeExternalRuntime(runtimeName)
	if runtimeName == "" {
		return nil
	}
	workspace = strings.TrimSpace(workspace)
	cacheKey := runtimeName + "\x00" + strings.ToLower(filepath.Clean(workspace))
	now := time.Now()
	s.usageMu.Lock()
	if s.externalUsageCache != nil {
		cached := s.externalUsageCache[cacheKey]
		cachedAt := s.externalUsageCachedAt[cacheKey]
		if cached != nil && now.Sub(cachedAt) < 30*time.Second {
			s.usageMu.Unlock()
			return cached
		}
	}
	s.usageMu.Unlock()

	home, _ := os.UserHomeDir()
	var usage ExternalUsageSummary
	switch runtimeName {
	case "gemini":
		usage = collectGeminiUsage(resolveGeminiHome(), workspace)
	case "opencode":
		// The usage summary is presented as lifetime totals. The native SQLite
		// query is already aggregated, so do not silently discard older sessions.
		usage = collectOpenCodeUsage(home, workspace, 0)
	default:
		return nil
	}
	response := externalUsageResponse(runtimeName, usage)
	if response == nil {
		return nil
	}
	s.usageMu.Lock()
	if s.externalUsageCache == nil {
		s.externalUsageCache = make(map[string]map[string]any)
		s.externalUsageCachedAt = make(map[string]time.Time)
	}
	s.externalUsageCache[cacheKey] = response
	s.externalUsageCachedAt[cacheKey] = now
	s.usageMu.Unlock()
	return response
}

// invalidateExternalUsageCache drops native usage snapshots after a turn has
// been persisted. The next UI refresh must observe the provider's latest
// database/session totals instead of serving the 30-second read cache.
func (s *AppService) invalidateExternalUsageCache(runtimeName string) {
	runtimeName = normalizeExternalRuntime(runtimeName)
	if runtimeName == "" {
		return
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	for key := range s.externalUsageCache {
		if strings.HasPrefix(key, runtimeName+"\x00") {
			delete(s.externalUsageCache, key)
			delete(s.externalUsageCachedAt, key)
		}
	}
}

func externalUsageResponse(runtime string, usage ExternalUsageSummary) map[string]any {
	total := usage.TotalTokens
	if total <= 0 {
		total = usage.InputTokens + usage.CachedTokens + usage.OutputTokens + usage.Reasoning
	}
	daily := make([]any, 0, len(usage.DailyBuckets))
	for _, bucket := range usage.DailyBuckets {
		daily = append(daily, map[string]any{
			"startDate":             bucket.StartDate,
			"tokens":                bucket.Tokens,
			"inputTokens":           bucket.InputTokens,
			"cachedInputTokens":     bucket.CachedInputTokens,
			"outputTokens":          bucket.OutputTokens,
			"reasoningOutputTokens": bucket.ReasoningOutputTokens,
		})
	}
	return map[string]any{
		"runtime": runtime,
		"source":  usage.Source,
		"summary": map[string]any{
			"lifetimeTokens":            total,
			"lifetimeInputTokens":       usage.InputTokens,
			"lifetimeCachedInputTokens": usage.CachedTokens,
			"lifetimeOutputTokens":      usage.OutputTokens,
			"lifetimeReasoningTokens":   usage.Reasoning,
			"peakDailyTokens":           nil,
			"currentStreakDays":         nil,
			"longestStreakDays":         nil,
			"longestRunningTurnSec":     nil,
		},
		"dailyUsageBuckets": daily,
	}
}

// RecordLocalTurnUsage lets the frontend persist per-turn totals (belt-and-suspenders with event hook).
// Always attributes to the Codex runtime (frontend Codex store only calls this).
func (s *AppService) RecordLocalTurnUsage(threadID, turnID string, totalTokens float64) error {
	tokens := int64(totalTokens)
	if tokens <= 0 {
		return nil
	}
	s.persistTurnUsage("codex", threadID, turnID, tokenBreakdown{Total: tokens}, time.Now())
	return nil
}

// RecordLocalTurnUsageDetailed persists a full input/output/cache/reasoning breakdown for a runtime.
func (s *AppService) RecordLocalTurnUsageDetailed(
	runtime, threadID, turnID string,
	inputTokens, cachedInputTokens, outputTokens, reasoningOutputTokens, totalTokens float64,
) error {
	b := tokenBreakdown{
		Input:     int64(inputTokens),
		Cached:    int64(cachedInputTokens),
		Output:    int64(outputTokens),
		Reasoning: int64(reasoningOutputTokens),
		Total:     int64(totalTokens),
	}
	b.normalize()
	if !b.valid() {
		return nil
	}
	s.persistTurnUsage(runtime, threadID, turnID, b, time.Now())
	return nil
}

func localUsageResponseEmpty(summary map[string]any) bool {
	if summary == nil {
		return true
	}
	meta, _ := summary["summary"].(map[string]any)
	var lifetime int64
	if meta != nil {
		for _, key := range []string{
			"lifetimeTokens",
			"lifetimeInputTokens",
			"lifetimeCachedInputTokens",
			"lifetimeOutputTokens",
			"lifetimeReasoningTokens",
		} {
			lifetime += int64(anyToFloat(meta[key]))
		}
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
	threadID, turnID, b, ok := extractTurnTokenBreakdown(data)
	if !ok {
		return
	}
	// Official Codex app-server events always belong to the codex runtime.
	s.persistTurnUsage("codex", threadID, turnID, b, time.Now())
}

// trackCodexActivity mirrors app-server lifecycle notifications into a small
// backend registry. It deliberately stores only the active turn id, not the
// conversation, so the lock stays cheap during long streams.
func (s *AppService) trackCodexActivity(event codex.Event) {
	if event.Type == "status" {
		if status, ok := event.Data.(codex.Status); ok && !status.Running {
			s.mu.Lock()
			s.codexActiveTurns = make(map[string]string)
			s.codexPendingDispatches = make(map[string]bool)
			s.mu.Unlock()
		}
		return
	}
	if event.Type != "notification" || event.Data == nil {
		return
	}
	data, ok := event.Data.(map[string]any)
	if !ok {
		return
	}
	threadID := strings.TrimSpace(stringFromAny(data["threadId"]))
	if threadID == "" {
		if thread, ok := data["thread"].(map[string]any); ok {
			threadID = strings.TrimSpace(stringFromAny(thread["id"]))
		}
	}
	if threadID == "" {
		return
	}
	turnID := ""
	if turn, ok := data["turn"].(map[string]any); ok {
		turnID = strings.TrimSpace(stringFromAny(turn["id"]))
		if turnID == "" {
			turnID = strings.TrimSpace(stringFromAny(turn["turnId"]))
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codexActiveTurns == nil {
		s.codexActiveTurns = make(map[string]string)
	}
	switch event.Method {
	case "turn/started":
		if turnID == "" {
			turnID = "active"
		}
		s.codexActiveTurns[threadID] = turnID
	case "turn/completed", "turn/failed", "turn/aborted", "turn/cancelled", "turn/interrupted":
		if current := s.codexActiveTurns[threadID]; current == "" || turnID == "" || current == turnID || current == "active" {
			delete(s.codexActiveTurns, threadID)
		}
	case "thread/status/changed":
		status := ""
		if rawStatus, ok := data["status"].(map[string]any); ok {
			status = strings.ToLower(strings.TrimSpace(firstMapString(rawStatus, "type", "status")))
		} else {
			status = strings.ToLower(strings.TrimSpace(stringFromAny(data["status"])))
		}
		if status == "idle" || status == "completed" || status == "failed" || status == "error" {
			delete(s.codexActiveTurns, threadID)
		} else if status == "active" || status == "running" || status == "inprogress" {
			if _, exists := s.codexActiveTurns[threadID]; !exists {
				s.codexActiveTurns[threadID] = "active"
			}
		}
	}
}

func (s *AppService) codexActiveTurnID(threadIDs ...string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, threadID := range threadIDs {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			continue
		}
		if turnID := strings.TrimSpace(s.codexActiveTurns[threadID]); turnID != "" {
			return turnID
		}
	}
	return ""
}

func (s *AppService) codexLivenessLocalState(threadIDs ...string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turnID := ""
	pending := false
	for _, threadID := range threadIDs {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			continue
		}
		if turnID == "" {
			turnID = strings.TrimSpace(s.codexActiveTurns[threadID])
		}
		pending = pending || s.codexPendingDispatches[threadID]
	}
	return turnID, pending
}

func (s *AppService) syncCodexLivenessActive(threadID, backendID, expectedTurnID, runningTurnID string) {
	runningTurnID = strings.TrimSpace(runningTurnID)
	if runningTurnID == "" {
		runningTurnID = "active"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codexActiveTurns == nil {
		s.codexActiveTurns = make(map[string]string)
	}
	for _, id := range []string{threadID, backendID} {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		current := strings.TrimSpace(s.codexActiveTurns[id])
		if id == threadID && current == "" {
			s.codexActiveTurns[id] = runningTurnID
		} else if current != "" && (current == expectedTurnID || current == "active") {
			s.codexActiveTurns[id] = runningTurnID
		}
	}
}

func (s *AppService) clearCodexLivenessIdle(threadID, backendID, expectedTurnID string) {
	expectedTurnID = strings.TrimSpace(expectedTurnID)
	if expectedTurnID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range []string{threadID, backendID} {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		current := strings.TrimSpace(s.codexActiveTurns[id])
		if current == expectedTurnID || (expectedTurnID == "active" && current == "active") {
			delete(s.codexActiveTurns, id)
		}
	}
}

func concreteTurnID(turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if turnID == "active" {
		return ""
	}
	return turnID
}

func lifecycleStatus(value any) string {
	if status, ok := value.(map[string]any); ok {
		value = firstMapString(status, "type", "status")
	}
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(
		strings.TrimSpace(stringFromAny(value)), "_", ""), "-", ""))
}

func lifecycleStatusIsActive(status string) bool {
	switch lifecycleStatus(status) {
	case "active", "running", "started", "pending", "inprogress":
		return true
	default:
		return false
	}
}

func activeLifecycleTurnID(value any) string {
	turns, _ := value.([]any)
	for index := len(turns) - 1; index >= 0; index-- {
		turn, ok := turns[index].(map[string]any)
		if !ok || !lifecycleStatusIsActive(lifecycleStatus(turn["status"])) {
			continue
		}
		if turnID := strings.TrimSpace(stringFromAny(turn["id"])); turnID != "" {
			return turnID
		}
	}
	return ""
}

func latestLifecycleTurn(value any) (string, string) {
	turns, _ := value.([]any)
	for index := len(turns) - 1; index >= 0; index-- {
		turn, ok := turns[index].(map[string]any)
		if !ok {
			continue
		}
		turnID := strings.TrimSpace(stringFromAny(turn["id"]))
		if turnID != "" {
			return turnID, lifecycleStatus(turn["status"])
		}
	}
	return "", ""
}

func (s *AppService) setCodexDispatchPending(pending bool, threadIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codexPendingDispatches == nil {
		s.codexPendingDispatches = make(map[string]bool)
	}
	for _, threadID := range threadIDs {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			continue
		}
		if pending {
			s.codexPendingDispatches[threadID] = true
		} else {
			delete(s.codexPendingDispatches, threadID)
		}
	}
}

func (s *AppService) claimCodexDispatch(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codexPendingDispatches == nil {
		s.codexPendingDispatches = make(map[string]bool)
	}
	if s.codexPendingDispatches[threadID] {
		return false
	}
	s.codexPendingDispatches[threadID] = true
	return true
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
	s.shutdownOnce.Do(func() {
		setSystemSleepPrevention(false)
		s.cancelExternalRuns()
		s.stopAllTerminalSessions()
		if s.providerRouter != nil {
			s.providerRouter.close()
		}
		s.historyMu.Lock()
		s.codexHistoryCache = nil
		s.claudeHistoryCache = nil
		s.grokHistoryCache = nil
		s.nativeHistoryCache = nil
		s.historyMu.Unlock()
		s.flushLocalUsage()
		if s.schedulerStop != nil {
			close(s.schedulerStop)
		}
		s.mu.Lock()
		client := s.client
		s.codexActiveTurns = make(map[string]string)
		s.codexPendingDispatches = make(map[string]bool)
		s.mu.Unlock()
		_ = client.Stop()
	})
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
		ActiveRuntime:             "codex",
		GrokBackend:               "build",
		GrokBuildModel:            "",
		GrokAPIModel:              "grok-4.6",
		GrokAPIKey:                "",
		GrokAPIBaseURL:            "",
		GrokEffort:                "high",
		GrokSandbox:               "workspace-write",
		GrokApprovalPolicy:        "on-request",
		GrokWebSearch:             false,
		GrokXSearch:               false,
		ClaudeWorkspace:           "",
		ClaudeRecentWorkspaces:    []string{},
		ClaudeModel:               "sonnet",
		ClaudeEffort:              "high",
		ClaudeSandbox:             "workspace-write",
		ClaudeApprovalPolicy:      "on-request",
		ClaudePermissionMode:      "acceptEdits",
		ClaudeCustomModels:        []string{},
		GrokCustomModels:          []string{},
		GeminiWorkspace:           "",
		GeminiRecentWorkspaces:    []string{},
		GeminiModel:               "",
		GeminiEffort:              "high",
		GeminiSandbox:             "workspace-write",
		GeminiApprovalPolicy:      "on-request",
		GeminiCustomModels:        []string{},
		OpenCodeModel:             "anthropic/claude-sonnet-4-6",
		OpenCodeWorkspace:         "",
		OpenCodeRecentWorkspaces:  []string{},
		OpenCodeEffort:            "high",
		OpenCodeSandbox:           "workspace-write",
		OpenCodeApprovalPolicy:    "on-request",
		OpenCodeProvider:          "",
		OpenCodeCustomModels:      []string{},
		Model:                     defaultCodexModel,
		CodexContextWindow:        0,
		CodexAutoCompactThreshold: 0,
		Effort:                    "high",
		CollaborationMode:         "default",
		Personality:               "pragmatic",
		MultiAgentMode:            "explicitRequestOnly",
		Sandbox:                   "workspace-write",
		ApprovalPolicy:            "on-request",
		Theme:                     "light",
		AccentColor:               "codex",
		FontFamily:                "system",
		TerminalProfile:           profile,
		Language:                  "zh-CN",
		AutoConnect:               true,
		WorkMode:                  "code",
		SendWithModifier:          false,
		FollowUpBehavior:          "queue",
		NotifyOnTurnComplete:      true,
		CustomInstructions:        "",
		TranslucentSidebar:        true,
		HighContrast:              false,
		PointerCursor:             false,
		ReduceMotion:              false,
		UiFontSize:                "md",
		CodeFontSize:              "md",
		PreventSleepWhileRunning:  false,
		AlwaysOnTop:               false,
		GitBranchPrefix:           "",
		GitCommitPrefix:           "",
		GitOpenPRAfterPush:        false,
		GitPRBodyTemplate:         "",
		BrowserAllowedHosts:       []string{},
		BrowserBlockedHosts:       []string{},
		BrowserDownloadDir:        "",
		BrowserFullCDP:            false,
		ShortcutCommandPalette:    "Ctrl+K",
		ShortcutNewThread:         "Ctrl+N",
		ShortcutTerminal:          "Ctrl+`",
		ShortcutBrowser:           "Ctrl+Shift+B",
		// Empty = use official Codex Desktop defaults at handshake time.
		CodexClientName:      "",
		CodexClientTitle:     "",
		CodexClientVersion:   "",
		NetworkProxyEnabled:  false,
		NetworkProxyURL:      "",
		NetworkProxyNoProxy:  defaultNetworkProxyNoProxy(),
		OnboardingCompleted:  false,
		RecentWorkspaces:     []string{},
		GrokRecentWorkspaces: []string{},
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
	settings.Model = strings.TrimSpace(settings.Model)
	if settings.Model == "" {
		settings.Model = defaultCodexModel
	}
	settings.CodexContextWindow, settings.CodexAutoCompactThreshold = normalizeCodexContextSettings(
		settings.CodexContextWindow,
		settings.CodexAutoCompactThreshold,
	)
	settings.RecentWorkspaces = sanitizeRecentWorkspaces(settings.RecentWorkspaces)
	settings.GrokRecentWorkspaces = sanitizeRecentWorkspaces(settings.GrokRecentWorkspaces)
	settings.GeminiRecentWorkspaces = sanitizeRecentWorkspaces(settings.GeminiRecentWorkspaces)
	settings.OpenCodeRecentWorkspaces = sanitizeRecentWorkspaces(settings.OpenCodeRecentWorkspaces)
	settings.CustomModels = sanitizeCustomModels(settings.CustomModels)
	settings.ClaudeCustomModels = sanitizeCustomModels(settings.ClaudeCustomModels)
	settings.GrokCustomModels = sanitizeCustomModels(settings.GrokCustomModels)
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
	settings.ActiveRuntime = normalizeRuntime(settings.ActiveRuntime)
	if normalized, err := normalizeNetworkProxyURL(settings.NetworkProxyURL); err == nil {
		settings.NetworkProxyURL = normalized
	} else {
		settings.NetworkProxyURL = strings.TrimSpace(settings.NetworkProxyURL)
	}
	settings.NetworkProxyNoProxy = sanitizeNetworkProxyNoProxy(settings.NetworkProxyNoProxy)
	if settings.NetworkProxyNoProxy == "" {
		settings.NetworkProxyNoProxy = defaultNetworkProxyNoProxy()
	}
	if settings.NetworkProxyEnabled && settings.NetworkProxyURL == "" {
		settings.NetworkProxyEnabled = false
	}
	settings.GrokBackend = normalizeGrokBackend(settings.GrokBackend)
	settings.GrokEffort = normalizeGrokEffort(settings.GrokEffort)
	if !isAllowed(settings.GrokSandbox, "read-only", "workspace-write", "danger-full-access") {
		settings.GrokSandbox = "workspace-write"
	}
	if !isAllowed(settings.GrokApprovalPolicy, "on-request", "never") {
		settings.GrokApprovalPolicy = "on-request"
	}
	settings.ClaudeRecentWorkspaces = sanitizeRecentWorkspaces(settings.ClaudeRecentWorkspaces)
	settings.ClaudeModel = sanitizeShortText(settings.ClaudeModel, 160)
	settings.ClaudeEffort = normalizeClaudeEffort(settings.ClaudeEffort)
	settings.GeminiModel = sanitizeShortText(settings.GeminiModel, 160)
	migratedGeminiModel := false
	legacyGeminiModel := strings.ToLower(strings.TrimSpace(settings.GeminiModel))
	if nativeModel := readEnvValue(filepath.Join(resolveGeminiHome(), ".env"), "GEMINI_MODEL"); nativeModel != "" {
		if legacyGeminiModel == "" || legacyGeminiModel == "gemini-2.5-pro" || legacyGeminiModel == "gemini-2.5-flash" {
			settings.GeminiModel = nativeModel
			migratedGeminiModel = true
		}
	} else if (legacyGeminiModel == "gemini-2.5-pro" || legacyGeminiModel == "gemini-2.5-flash") &&
		geminiRuntimeDisplayName(findGeminiExecutable()) == "Antigravity CLI" {
		// Older NiceCodex releases injected Gemini 2.5 as an app fallback. Do not
		// carry that synthetic choice into Antigravity; an empty value lets agy use
		// its current native default model.
		settings.GeminiModel = ""
		migratedGeminiModel = true
	}
	settings.GeminiWorkspace = strings.TrimSpace(settings.GeminiWorkspace)
	settings.GeminiCustomModels = sanitizeCustomModels(settings.GeminiCustomModels)
	if !isAllowed(settings.GeminiSandbox, "read-only", "workspace-write", "danger-full-access") {
		settings.GeminiSandbox = "workspace-write"
	}
	if !isAllowed(settings.GeminiApprovalPolicy, "on-request", "never") {
		settings.GeminiApprovalPolicy = "on-request"
	}
	previousGeminiEffort := settings.GeminiEffort
	settings.GeminiEffort = normalizeGeminiEffort(settings.GeminiEffort)
	migratedGeminiEffort := settings.GeminiEffort != previousGeminiEffort
	settings.OpenCodeModel = sanitizeShortText(settings.OpenCodeModel, 160)
	settings.OpenCodeWorkspace = strings.TrimSpace(settings.OpenCodeWorkspace)
	settings.OpenCodeEffort = sanitizeShortText(settings.OpenCodeEffort, 32)
	if !isAllowed(settings.OpenCodeSandbox, "read-only", "workspace-write", "danger-full-access") {
		settings.OpenCodeSandbox = "workspace-write"
	}
	if !isAllowed(settings.OpenCodeApprovalPolicy, "on-request", "never") {
		settings.OpenCodeApprovalPolicy = "on-request"
	}
	settings.OpenCodeProvider = sanitizeShortText(settings.OpenCodeProvider, 160)
	settings.OpenCodeCustomModels = sanitizeCustomModels(settings.OpenCodeCustomModels)
	if settings.OpenCodeEffort == "" {
		settings.OpenCodeEffort = "high"
	}
	if !isAllowed(settings.ClaudeSandbox, "read-only", "workspace-write", "danger-full-access") {
		settings.ClaudeSandbox = "workspace-write"
	}
	if !isAllowed(settings.ClaudeApprovalPolicy, "on-request", "never") {
		settings.ClaudeApprovalPolicy = "on-request"
	}
	settings.ClaudePermissionMode = normalizeClaudePermissionMode(settings.ClaudePermissionMode)
	if settings.ClaudePermissionMode == "" {
		// Sensible default aligned with official Claude Code headless usage.
		settings.ClaudePermissionMode = "acceptEdits"
	}
	if _, err := validateWorkspace(settings.ClaudeWorkspace); err != nil {
		settings.ClaudeWorkspace = ""
	}
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
	if _, err := validateWorkspace(settings.GrokWorkspace); err != nil {
		settings.GrokWorkspace = ""
	}
	if _, err := validateWorkspace(settings.GeminiWorkspace); err != nil {
		settings.GeminiWorkspace = ""
	}
	if _, err := validateWorkspace(settings.OpenCodeWorkspace); err != nil {
		settings.OpenCodeWorkspace = ""
	}
	// Persist migrations so subsequent launches and the frontend see the same values.
	if migratedOnboarding || migratedGeminiModel || migratedGeminiEffort {
		_ = writeSettings(path, settings)
	}
	return settings, nil
}

func normalizeCodexContextSettings(window, threshold int64) (int64, int64) {
	if window < 0 {
		window = 0
	}
	if window > 0 && window < 16_384 {
		window = 16_384
	}
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 0 && threshold < 8_192 {
		threshold = 8_192
	}
	if window > 0 && threshold >= window {
		threshold = window - 1_024
	}
	return window, threshold
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

func (s *AppService) managedAttachmentDir() string {
	return filepath.Join(filepath.Dir(s.settingsPath), "attachments")
}

func (s *AppService) isManagedImageAttachment(path string) bool {
	dir, err := filepath.Abs(s.managedAttachmentDir())
	if err != nil {
		return false
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	file, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	file, err = filepath.EvalSymlinks(file)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(dir, file)
	if err != nil || relative == "." || relative == "" {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (s *AppService) importSelectedImageAttachment(source string) (string, error) {
	cleanSource, err := validateImageAttachment(source)
	if err != nil {
		return "", err
	}
	if s.isManagedImageAttachment(cleanSource) {
		return cleanSource, nil
	}
	raw, err := os.ReadFile(cleanSource)
	if err != nil {
		return "", err
	}
	ext := imageExtensionForMime("", cleanSource, raw)
	if ext == "" {
		return "", errors.New("unsupported image format")
	}
	dir := s.managedAttachmentDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := sanitizeAttachmentFileName(filepath.Base(cleanSource), ext)
	target := filepath.Join(dir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), name))
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		return "", err
	}
	return validateImageAttachment(target)
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
	settings.GrokRecentWorkspaces = append([]string(nil), settings.GrokRecentWorkspaces...)
	settings.ClaudeRecentWorkspaces = append([]string(nil), settings.ClaudeRecentWorkspaces...)
	settings.ClaudeCustomModels = append([]string(nil), settings.ClaudeCustomModels...)
	settings.GrokCustomModels = append([]string(nil), settings.GrokCustomModels...)
	settings.GeminiCustomModels = append([]string(nil), settings.GeminiCustomModels...)
	settings.OpenCodeCustomModels = append([]string(nil), settings.OpenCodeCustomModels...)
	settings.CustomModels = append([]string(nil), settings.CustomModels...)
	settings.BrowserAllowedHosts = append([]string(nil), settings.BrowserAllowedHosts...)
	settings.BrowserBlockedHosts = append([]string(nil), settings.BrowserBlockedHosts...)
	return settings
}

func normalizeFollowUpBehavior(value string) string {
	_ = value
	return "queue"
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

// sanitizeCodexClientField keeps free-form clientInfo fields safe for UA / settings storage.
func sanitizeCodexClientField(value string, max int) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
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
		return filepath.Clean(codexHome)
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	// Official Codex uses ~/.codex on Windows, macOS, and Linux.
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
	isPersonal := home != "" && samePath(dir, home)
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

func rememberWorkspace(items []string, workspace string) []string {
	result := append([]string(nil), items...)
	for _, item := range result {
		if strings.EqualFold(filepath.Clean(item), workspace) {
			return result
		}
	}
	if len(result) >= 8 {
		result = result[:7]
	}
	return append(result, workspace)
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

func normalizeSessionGoal(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func normalizeSessionGoalStatus(value string) string {
	status := strings.ToLower(strings.TrimSpace(value))
	status = strings.ReplaceAll(status, "_", "")
	status = strings.ReplaceAll(status, "-", "")
	switch status {
	case "active":
		return "active"
	case "paused", "pause":
		return "paused"
	case "blocked":
		return "blocked"
	case "usagelimited":
		return "usageLimited"
	case "budgetlimited":
		return "budgetLimited"
	case "complete", "completed", "done":
		return "complete"
	default:
		return ""
	}
}

func sessionGoalStatus(session *SessionRecord) string {
	if session == nil || normalizeSessionGoal(session.Goal) == "" {
		return ""
	}
	if status := normalizeSessionGoalStatus(session.GoalStatus); status != "" {
		return status
	}
	return "active"
}

func (s *AppService) syncNativeCodexGoal(session *SessionRecord, backendID string) (map[string]any, bool) {
	if session == nil || isExternalSession(session) || strings.TrimSpace(backendID) == "" {
		return nil, false
	}
	goal := normalizeSessionGoal(session.Goal)
	method := "thread/goal/clear"
	params := map[string]any{"threadId": strings.TrimSpace(backendID)}
	if goal != "" {
		method = "thread/goal/set"
		params["objective"] = goal
		params["status"] = sessionGoalStatus(session)
		if session.GoalTokenBudget == nil {
			params["tokenBudget"] = nil
		} else {
			params["tokenBudget"] = *session.GoalTokenBudget
		}
	}
	result, err := s.call(method, params)
	if err != nil {
		return nil, false
	}
	if goal == "" {
		return nil, true
	}
	if nativeGoal, ok := result["goal"].(map[string]any); ok {
		return nativeGoal, true
	}
	// Older experimental builds returned an empty success body. Treat the local
	// values as synced so the compatibility prompt is not injected twice.
	fallback := map[string]any{
		"objective":       goal,
		"status":          sessionGoalStatus(session),
		"tokensUsed":      session.GoalTokensUsed,
		"timeUsedSeconds": session.GoalTimeUsedSeconds,
		"createdAt":       session.GoalCreatedAt,
		"updatedAt":       session.GoalUpdatedAt,
	}
	if session.GoalTokenBudget == nil {
		fallback["tokenBudget"] = nil
	} else {
		fallback["tokenBudget"] = *session.GoalTokenBudget
	}
	return fallback, true
}

func applyNativeGoalLocked(record *SessionRecord, goal map[string]any) {
	if record == nil {
		return
	}
	if goal == nil {
		record.Goal = ""
		record.GoalStatus = ""
		record.GoalTokenBudget = nil
		record.GoalTokensUsed = 0
		record.GoalTimeUsedSeconds = 0
		record.GoalCreatedAt = 0
		record.GoalUpdatedAt = 0
		record.GoalSynced = false
		return
	}
	objective := normalizeSessionGoal(stringFromAny(goal["objective"]))
	if objective == "" {
		return
	}
	record.Goal = objective
	record.GoalStatus = normalizeSessionGoalStatus(stringFromAny(goal["status"]))
	if record.GoalStatus == "" {
		record.GoalStatus = "active"
	}
	if rawBudget, exists := goal["tokenBudget"]; exists {
		if rawBudget == nil {
			record.GoalTokenBudget = nil
		} else if budget := int64(numericMapValue(goal, "tokenBudget")); budget > 0 {
			record.GoalTokenBudget = &budget
		}
	}
	record.GoalTokensUsed = int64(numericMapValue(goal, "tokensUsed"))
	record.GoalTimeUsedSeconds = int64(numericMapValue(goal, "timeUsedSeconds"))
	record.GoalCreatedAt = int64(numericMapValue(goal, "createdAt"))
	record.GoalUpdatedAt = int64(numericMapValue(goal, "updatedAt"))
	record.GoalSynced = true
}

func (s *AppService) applyNativeGoalForSession(sessionID string, goal map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[sessionID]
	if record == nil {
		return
	}
	applyNativeGoalLocked(record, goal)
	s.persistSessionsLocked()
}

func (s *AppService) restoreSessionGoal(previous *SessionRecord) {
	if previous == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.sessions[previous.ID]
	if record == nil {
		return
	}
	record.Goal = previous.Goal
	record.GoalStatus = previous.GoalStatus
	record.GoalTokenBudget = nil
	if previous.GoalTokenBudget != nil {
		budget := *previous.GoalTokenBudget
		record.GoalTokenBudget = &budget
	}
	record.GoalTokensUsed = previous.GoalTokensUsed
	record.GoalTimeUsedSeconds = previous.GoalTimeUsedSeconds
	record.GoalCreatedAt = previous.GoalCreatedAt
	record.GoalUpdatedAt = previous.GoalUpdatedAt
	record.GoalSynced = previous.GoalSynced
	s.persistSessionsLocked()
}

func (s *AppService) refreshNativeCodexGoal(session *SessionRecord, backendID string) *SessionRecord {
	if session == nil || isExternalSession(session) || strings.TrimSpace(backendID) == "" {
		return session
	}
	result, err := s.call("thread/goal/get", map[string]any{"threadId": strings.TrimSpace(backendID)})
	if err != nil {
		return session
	}
	rawGoal, exists := result["goal"]
	if !exists {
		return session
	}
	if rawGoal == nil {
		// An unsynced local objective is a compatibility fallback, not stale data.
		if session.GoalSynced {
			s.applyNativeGoalForSession(session.ID, nil)
		}
	} else if goal, ok := rawGoal.(map[string]any); ok {
		s.applyNativeGoalForSession(session.ID, goal)
	}
	if refreshed := s.sessionForIDAny(session.ID); refreshed != nil {
		return refreshed
	}
	return session
}

func (s *AppService) trackCodexGoal(event codex.Event) {
	if event.Type != "notification" || event.Data == nil {
		return
	}
	if event.Method != "thread/goal/updated" && event.Method != "thread/goal/cleared" {
		return
	}
	data, ok := event.Data.(map[string]any)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(stringFromAny(data["threadId"]))
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	record := s.sessions[sessionID]
	if record != nil {
		if event.Method == "thread/goal/cleared" {
			applyNativeGoalLocked(record, nil)
		} else if goal, ok := data["goal"].(map[string]any); ok {
			applyNativeGoalLocked(record, goal)
		}
		s.persistSessionsLocked()
	}
	s.codexGoalEventSequence++
	data["goalSequence"] = s.codexGoalEventSequence
	s.mu.Unlock()
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
	if s.allowedThreads == nil {
		s.allowedThreads = make(map[string]string)
	}
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

func isThreadNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "thread not found") ||
		(strings.Contains(msg, "not found") && strings.Contains(msg, "thread"))
}

// loadBackendThread ensures the Codex app-server has the thread in memory.
// After reconnect, history lives on disk but turn/start fails until thread/resume.
func (s *AppService) loadBackendThread(backendID, workspace string, settings UserSettings, session *SessionRecord) (map[string]any, error) {
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return nil, errors.New("thread id is required")
	}
	cleanWorkspace, err := validateWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	// thread/read can read rollout history from disk without registering the
	// thread in the current app-server process. Only thread/resume guarantees a
	// subsequent turn/start can find it, and it safely rejoins a running thread.
	params := map[string]any{
		"threadId":       backendID,
		"cwd":            cleanWorkspace,
		"sandbox":        normalizeSandbox(settings.Sandbox),
		"approvalPolicy": normalizeApproval(settings.ApprovalPolicy),
	}
	model := strings.TrimSpace(settings.Model)
	providerID := strings.TrimSpace(settings.ModelProvider)
	if session != nil {
		if session.Model != "" {
			model = session.Model
		}
		if session.ProviderID != "" {
			providerID = session.ProviderID
		}
	}
	if externalProviderKind(providerID) == "" && model != "" {
		params["model"] = model
	}
	if externalProviderKind(providerID) == "" && providerID != "" {
		params["modelProvider"] = providerID
	}
	result, err := s.call("thread/resume", params)
	if err != nil {
		return nil, err
	}
	s.rememberThread(backendID, cleanWorkspace)
	return result, nil
}

// readBackendThreadSnapshot loads history without registering or resuming the
// thread in the app-server. It is used only for display; SendMessage owns the
// stateful resume needed before turn/start.
func (s *AppService) readBackendThreadSnapshot(backendID, workspace string) (map[string]any, error) {
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return nil, errors.New("thread id is required")
	}
	cleanWorkspace, err := validateWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	result, err := s.call("thread/read", map[string]any{
		"threadId":     backendID,
		"includeTurns": true,
	})
	if err != nil {
		return nil, err
	}
	thread, ok := result["thread"].(map[string]any)
	if !ok {
		return nil, errors.New("Codex returned an invalid thread snapshot")
	}
	if _, ok := thread["turns"]; !ok {
		return nil, errors.New("Codex thread snapshot did not include history")
	}
	if threadWorkspace, _ := thread["cwd"].(string); strings.TrimSpace(threadWorkspace) != "" && !samePath(threadWorkspace, cleanWorkspace) {
		return nil, errors.New("this thread belongs to a different workspace")
	}
	return result, nil
}

func (s *AppService) ensureCodexBackendThread(session *SessionRecord, settings UserSettings, workspace string) (string, error) {
	if session == nil {
		return "", errors.New("session not found")
	}

	// Serialize lazy allocation so concurrent first sends cannot create two threads.
	s.codexThreadStartMu.Lock()
	defer s.codexThreadStartMu.Unlock()

	s.mu.Lock()
	if s.pendingCodexSessions == nil {
		s.pendingCodexSessions = make(map[string]string)
	}
	record := s.sessions[session.ID]
	if record == nil || record.Archived {
		s.mu.Unlock()
		return "", errors.New("session not found")
	}
	if ref := strings.TrimSpace(record.BackendRef); ref != "" {
		s.mu.Unlock()
		return ref, nil
	}
	session = cloneSession(record)
	s.pendingCodexSessions[workspaceKey(workspace)] = session.ID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.pendingCodexSessions[workspaceKey(workspace)] == session.ID {
			delete(s.pendingCodexSessions, workspaceKey(workspace))
		}
		s.mu.Unlock()
	}()

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
	if !s.bindCodexBackendThreadLocked(session.ID, backendID) {
		s.mu.Unlock()
		return "", errors.New("session could not claim the allocated Codex thread")
	}
	record = s.sessions[session.ID]
	if name, ok := result["thread"].(map[string]any); ok {
		if value, _ := name["name"].(string); value != "" && (record.Name == "" || record.Name == "New task") {
			record.Name = value
		}
	}
	s.persistSessionsLocked()
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
	effort := ""
	collaborationMode := ""
	workMode := "code"
	if session != nil {
		if current := s.sessionForIDAny(session.ID); current != nil {
			session = current
		}
		id = session.ID
		model = session.Model
		providerID = session.ProviderID
		effort = session.Effort
		collaborationMode = normalizeCollaborationMode(session.CollaborationMode)
		if collaborationMode == "" {
			collaborationMode = "default"
		}
		workMode = normalizeWorkMode(session.WorkMode)
	}
	if model != "" {
		result["model"] = model
	}
	if effort != "" {
		result["effort"] = effort
	}
	if collaborationMode != "" {
		result["collaborationMode"] = collaborationMode
	}
	result["modelProvider"] = providerID
	result["workMode"] = workMode
	if thread, ok := result["thread"].(map[string]any); ok {
		thread["id"] = id
		backendRef := ""
		if session != nil {
			backendRef = session.BackendRef
		}
		if turnID := s.codexActiveTurnID(id, sessionID, backendRef); turnID != "" && turnID != "active" {
			thread["activeTurnId"] = turnID
		}
		if model != "" {
			thread["model"] = model
		}
		if effort != "" {
			thread["effort"] = effort
		}
		if collaborationMode != "" {
			thread["collaborationMode"] = collaborationMode
		}
		thread["modelProvider"] = providerID
		thread["workMode"] = workMode
		applySessionGoalView(thread, session)
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
	return s.sessionIDForBackendRefLocked(backendID)
}

// bindCodexBackendThreadLocked gives one NiceCodex UUID exclusive ownership of
// a Codex app-server thread and removes a raw thread-id mirror in the same lock.
// It must only be called while s.mu is held.
func (s *AppService) bindCodexBackendThreadLocked(sessionID, backendID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	backendID = strings.TrimSpace(backendID)
	if sessionID == "" || backendID == "" {
		return false
	}
	record := s.sessions[sessionID]
	if record == nil || record.Archived || isExternalSession(record) {
		return false
	}

	if mirror := s.sessions[backendID]; mirror != nil && mirror != record &&
		!isExternalSession(mirror) && samePath(mirror.Workspace, record.Workspace) {
		if (record.Name == "" || record.Name == "New task") && mirror.Name != "" {
			record.Name = mirror.Name
		}
		if record.Preview == "" {
			record.Preview = mirror.Preview
		}
		if record.Model == "" {
			record.Model = mirror.Model
		}
		if record.ProviderID == "" {
			record.ProviderID = mirror.ProviderID
		}
		delete(s.sessions, backendID)
	}

	record.BackendRef = backendID
	record.UpdatedAt = time.Now().Unix()
	if s.allowedThreads == nil {
		s.allowedThreads = make(map[string]string)
	}
	s.allowedThreads[sessionID] = record.Workspace
	s.allowedThreads[backendID] = record.Workspace
	delete(s.pendingCodexSessions, workspaceKey(record.Workspace))
	return true
}

func (s *AppService) claimPendingCodexSession(backendID, eventWorkspace string) string {
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// The backend thread must not already be owned by a different session. A
	// late thread/started event must never rebind an allocated thread to another
	// session just because that session's pending slot is now the only one left.
	if ref := s.sessionIDForBackendRefLocked(backendID); ref != "" && ref != backendID {
		return ""
	}

	sessionID := s.pendingCodexSessions[workspaceKey(eventWorkspace)]
	if eventWorkspace == "" {
		// A cwd-less event is safe to infer only when exactly one distinct pending
		// session exists. Map iteration order must never choose a random workspace.
		var candidateID string
		for _, candidate := range s.pendingCodexSessions {
			if candidate == "" {
				continue
			}
			if candidateID != "" && candidateID != candidate {
				return ""
			}
			candidateID = candidate
		}
		sessionID = candidateID
	}
	record := s.sessions[sessionID]
	if sessionID == "" || record == nil || record.Archived || isExternalSession(record) {
		return ""
	}
	if eventWorkspace != "" && !samePath(eventWorkspace, record.Workspace) {
		return ""
	}
	if ref := strings.TrimSpace(record.BackendRef); ref != "" && ref != backendID {
		return ""
	}
	if !s.bindCodexBackendThreadLocked(sessionID, backendID) {
		return ""
	}
	s.persistSessionsLocked()
	return sessionID
}

// sessionIDForBackendRefLocked is the locked-only variant of
// sessionIDForBackendRef. It must only be called while s.mu is held.
func (s *AppService) sessionIDForBackendRefLocked(backendID string) string {
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return ""
	}
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
	// thread/started precedes the thread/start response. Bind it to the pending
	// NiceCodex session before the raw backend id can become a second sidebar row.
	if mapped == threadID && event.Method == "thread/started" {
		threadWorkspace := ""
		if thread, ok := data["thread"].(map[string]any); ok {
			threadWorkspace, _ = thread["cwd"].(string)
		}
		if sessionID := s.claimPendingCodexSession(threadID, threadWorkspace); sessionID != "" {
			mapped = sessionID
		}
	}
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
	if goal, ok := data["goal"].(map[string]any); ok {
		if value, _ := goal["threadId"].(string); value == threadID {
			goal["threadId"] = mapped
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

// workspaceKey returns a canonical key for workspace maps. It must be the only
// way pending/thread workspace maps are keyed so event cwd strings (which may
// differ in case or separator style) hit the same entry.
func workspaceKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
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
	case strings.Contains(value, "opencode"):
		return "opencode"
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
