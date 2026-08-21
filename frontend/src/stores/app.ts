import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'
import { Events } from '@wailsio/runtime'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type {
  BootstrapData,
  AgentProviderRuntime,
  TerminalProfile,
  UserSettings,
  WorkspaceInfo,
} from '../../bindings/nice_codex_desktop/models'
import { setLocale, supportedLocales } from '../i18n'
import { useAppearance } from '../composables/useAppearance'
import type { AppAccent } from '../lib/accents'
import { notify } from '../utils/notify'
import { friendlyErrorMessage } from '../utils/errorMessage'
import {
  asNumber,
  asRecord,
  asString,
  normalizeAccount,
  normalizeAccountRateLimits,
  normalizeAccountUsage,
} from '../utils/protocol'
import { translate } from '../i18n'
import { DEFAULT_CODEX_MODEL } from '../utils/runtimeProviders'
import { workspaceKey } from '../utils/workspacePath'

const AppVersionFallback = '1.4.0'
const workspaceOrderStorageKey = 'nice-codex.workspaceOrder.v1'

export type WorkspaceRuntime = 'codex' | 'claude' | 'grok' | 'gemini' | 'opencode'
type WorkspaceOrderByRuntime = Record<WorkspaceRuntime, string[]>

const defaultSettings: UserSettings = {
  activeRuntime: 'codex',
  workspace: '',
  recentWorkspaces: [],
  grokWorkspace: '',
  grokRecentWorkspaces: [],
  grokBackend: 'build',
  grokBuildModel: '',
  grokAPIModel: '',
  grokEffort: 'high',
  grokSandbox: 'workspace-write',
  grokApprovalPolicy: 'on-request',
  grokWebSearch: true,
  grokXSearch: false,
  grokAPIKey: '',
  grokAPIBaseURL: '',
  claudeWorkspace: '',
  claudeRecentWorkspaces: [],
  claudeModel: 'sonnet',
  claudeEffort: 'high',
  claudeSandbox: 'workspace-write',
  claudeApprovalPolicy: 'on-request',
  claudePermissionMode: 'acceptEdits',
  claudeCustomModels: [],
  grokCustomModels: [],
  geminiWorkspace: '',
  geminiRecentWorkspaces: [],
  geminiModel: '',
  geminiEffort: 'auto',
  geminiSandbox: 'workspace-write',
  geminiApprovalPolicy: 'on-request',
  geminiCustomModels: [],
  openCodeModel: 'anthropic/claude-sonnet-4-6',
  openCodeWorkspace: '',
  openCodeRecentWorkspaces: [],
  openCodeEffort: 'high',
  openCodeSandbox: 'workspace-write',
  openCodeApprovalPolicy: 'on-request',
  openCodeProvider: '',
  openCodeCustomModels: [],
  model: DEFAULT_CODEX_MODEL,
  codexContextWindow: 0,
  codexAutoCompactThreshold: 0,
  modelProvider: '',
  customModels: [],
  effort: 'high',
  serviceTier: '',
  collaborationMode: 'default',
  personality: 'pragmatic',
  multiAgentMode: 'explicitRequestOnly',
  sandbox: 'workspace-write',
  approvalPolicy: 'on-request',
  theme: 'light',
  accentColor: 'codex',
  fontFamily: 'system',
  terminalProfile: 'powershell',
  language: 'zh-CN',
  autoConnect: true,
  workMode: 'code',
  sendWithModifier: false,
  followUpBehavior: 'queue',
  notifyOnTurnComplete: true,
  customInstructions: '',
  translucentSidebar: true,
  highContrast: false,
  pointerCursor: false,
  reduceMotion: false,
  uiFontSize: 'md',
  codeFontSize: 'md',
  preventSleepWhileRunning: false,
  alwaysOnTop: false,
  gitBranchPrefix: '',
  gitCommitPrefix: '',
  gitOpenPRAfterPush: false,
  gitPRBodyTemplate: '',
  browserAllowedHosts: [],
  browserBlockedHosts: [],
  browserDownloadDir: '',
  browserFullCDP: false,
  shortcutCommandPalette: 'Ctrl+K',
  shortcutNewThread: 'Ctrl+N',
  shortcutTerminal: 'Ctrl+`',
  shortcutBrowser: 'Ctrl+Shift+B',
  codexClientName: '',
  codexClientTitle: '',
  codexClientVersion: '',
  networkProxyEnabled: false,
  networkProxyUrl: '',
  networkProxyNoProxy: 'localhost,127.0.0.1,::1',
  onboardingCompleted: false,
}

export interface AccountInfo {
  authenticated: boolean
  type: string
  email: string
  planType: string
  requiresOpenAIAuth: boolean
}

const emptyAccount: AccountInfo = {
  authenticated: false,
  type: '',
  email: '',
  planType: '',
  requiresOpenAIAuth: false,
}

export const useAppStore = defineStore('app', () => {
  const { initAppearance, setTheme, setAccent, setFont, setUiPrefs, setRuntime } = useAppearance()

  const bootstrapping = shallowRef(true)
  const settings = shallowRef<UserSettings>({ ...defaultSettings })
  const workspaceOrderByRuntime = shallowRef<WorkspaceOrderByRuntime>(loadWorkspaceOrder())
  const workspace = shallowRef<WorkspaceInfo | null>(null)
  const codexAvailable = shallowRef(false)
  const codexVersion = shallowRef('')
  const appVersion = shallowRef('1.4.0')
  const updateRepo = shallowRef('nsmao-com/codex-app-desktop')
  const systemFonts = shallowRef<Array<{ family: string; source: string }>>([])
  const updateInfo = shallowRef<{
    currentVersion: string
    latestVersion: string
    updateAvailable: boolean
    releaseUrl: string
    downloadUrl: string
    releaseNotes: string
  } | null>(null)
  const updateDialogOpen = shallowRef(false)
  const updateChecking = shallowRef(false)
  const updateCheckError = shallowRef('')
  const updateProgress = shallowRef<{
    phase: string
    percent: number
    bytesReceived: number
    bytesTotal: number
    message: string
    error: string
    readyToRestart: boolean
  } | null>(null)
  const updateInstalling = shallowRef(false)
  let updateEventUnsub: (() => void) | null = null
  const terminalProfiles = shallowRef<TerminalProfile[]>([])
  const account = shallowRef<AccountInfo>({ ...emptyAccount })
  const accountRateLimits = shallowRef<ReturnType<typeof normalizeAccountRateLimits>>(null)
  const accountUsage = shallowRef<ReturnType<typeof normalizeAccountUsage>>(null)
  let usageLoadSequence = 0
  const models = shallowRef<import('../types/codex').ModelOption[]>([])
  const modelProviders = shallowRef<import('../types/codex').ModelProviderOption[]>([])
  const agentProviders = shallowRef<AgentProviderRuntime[]>([])

  const currentWorkspacePath = computed(() => {
    if (isGrokMode.value) return settings.value.grokWorkspace || settings.value.workspace
    if (isClaudeMode.value) return settings.value.claudeWorkspace || settings.value.workspace
    if (isGeminiMode.value) return settings.value.geminiWorkspace || settings.value.workspace
    if (isOpenCodeMode.value) return settings.value.openCodeWorkspace || settings.value.workspace
    return settings.value.workspace
  })
  const currentTheme = computed(() => settings.value.theme)
  const activeRuntime = computed(() => normalizeRuntimeID(settings.value.activeRuntime))
  const isGrokMode = computed(() => activeRuntime.value === 'grok')
  const isClaudeMode = computed(() => activeRuntime.value === 'claude')
  const isGeminiMode = computed(() => activeRuntime.value === 'gemini')
  const isOpenCodeMode = computed(() => activeRuntime.value === 'opencode')
  const isCodexMode = computed(() => activeRuntime.value === 'codex')

  function orderWorkspacePaths(
    runtime: WorkspaceRuntime,
    availablePaths: string[],
    preferredPaths: string[] = [],
  ): string[] {
    const available = sanitizeWorkspaceOrder(availablePaths)
    const availableByKey = new Map(available.map((path) => [workspaceKey(path), path]))
    const result: string[] = []
    const seen = new Set<string>()
    const appendAvailable = (path: string): void => {
      const key = workspaceKey(path)
      const availablePath = availableByKey.get(key)
      if (!availablePath || seen.has(key)) return
      seen.add(key)
      result.push(availablePath)
    }

    workspaceOrderByRuntime.value[runtime].forEach(appendAvailable)
    preferredPaths.forEach(appendAvailable)
    available
      .filter((path) => !seen.has(workspaceKey(path)))
      .sort((left, right) => {
        const leftKey = workspaceKey(left)
        const rightKey = workspaceKey(right)
        return leftKey < rightKey ? -1 : leftKey > rightKey ? 1 : 0
      })
      .forEach(appendAvailable)
    return result
  }

  function setWorkspaceOrder(runtime: WorkspaceRuntime, orderedPaths: string[]): void {
    workspaceOrderByRuntime.value = {
      ...workspaceOrderByRuntime.value,
      [runtime]: sanitizeWorkspaceOrder(orderedPaths),
    }
    persistWorkspaceOrder(workspaceOrderByRuntime.value)
  }

  let preferenceTimer = 0
  let preferenceVersion = 0
  let preferenceSync: Promise<void> = Promise.resolve()
  let runtimeSync: Promise<void> = Promise.resolve()

  async function bootstrap(): Promise<void> {
    bootstrapping.value = true
    try {
      const data = await backend.Bootstrap()
      applyBootstrap(data)
    } catch (error) {
      notify('error', translate('notifications.unableStart'), errorMessage(error))
    } finally {
      bootstrapping.value = false
    }
  }

  /** Soft re-detect CLIs / providers without full-screen bootstrap spinner. */
  async function refreshRuntimes(): Promise<void> {
    try {
      const data = await backend.Bootstrap()
      codexAvailable.value = data.codex.available
      codexVersion.value = data.codex.version
      agentProviders.value = data.agentProviders ?? []
    } catch {
      // best-effort
    }
  }

  function applyBootstrap(data: BootstrapData): void {
    codexAvailable.value = data.codex.available
    codexVersion.value = data.codex.version
    appVersion.value = asString(data.appVersion, AppVersionFallback)
    updateRepo.value = asString(data.updateRepo, 'nsmao-com/codex-app-desktop')
    agentProviders.value = data.agentProviders ?? []
    settings.value = {
      ...defaultSettings,
      ...data.settings,
      activeRuntime: normalizeRuntimeID(data.settings.activeRuntime),
      modelProvider: '',
      recentWorkspaces: data.settings.recentWorkspaces ?? [],
      grokWorkspace: data.settings.grokWorkspace ?? '',
      grokRecentWorkspaces: data.settings.grokRecentWorkspaces ?? [],
      grokBackend: data.settings.grokBackend === 'api' ? 'api' : 'build',
      grokBuildModel: data.settings.grokBuildModel ?? '',
      grokAPIModel: data.settings.grokAPIModel ?? '',
      grokEffort: data.settings.grokEffort || 'high',
      grokSandbox: data.settings.grokSandbox || 'workspace-write',
      grokApprovalPolicy: data.settings.grokApprovalPolicy || 'on-request',
      grokWebSearch: data.settings.grokWebSearch !== false,
      grokXSearch: Boolean(data.settings.grokXSearch),
      grokAPIKey: data.settings.grokAPIKey ?? '',
      grokAPIBaseURL: data.settings.grokAPIBaseURL ?? '',
      claudeWorkspace: data.settings.claudeWorkspace ?? '',
      claudeRecentWorkspaces: data.settings.claudeRecentWorkspaces ?? [],
      claudeModel: data.settings.claudeModel || 'sonnet',
      claudeEffort: data.settings.claudeEffort || 'high',
      claudeSandbox: data.settings.claudeSandbox || 'workspace-write',
      claudeApprovalPolicy: data.settings.claudeApprovalPolicy || 'on-request',
      claudePermissionMode: data.settings.claudePermissionMode || 'acceptEdits',
      claudeCustomModels: data.settings.claudeCustomModels ?? [],
      grokCustomModels: data.settings.grokCustomModels ?? [],
      geminiWorkspace: data.settings.geminiWorkspace ?? '',
      geminiRecentWorkspaces: data.settings.geminiRecentWorkspaces ?? [],
      geminiModel: data.settings.geminiModel || 'gemini-2.5-pro',
      geminiEffort: data.settings.geminiEffort || 'auto',
      geminiSandbox: data.settings.geminiSandbox || 'workspace-write',
      geminiApprovalPolicy: data.settings.geminiApprovalPolicy || 'on-request',
      geminiCustomModels: data.settings.geminiCustomModels ?? [],
      openCodeModel: data.settings.openCodeModel || 'anthropic/claude-sonnet-4-6',
      openCodeWorkspace: data.settings.openCodeWorkspace ?? '',
      openCodeRecentWorkspaces: data.settings.openCodeRecentWorkspaces ?? [],
      openCodeEffort: data.settings.openCodeEffort || 'high',
      openCodeSandbox: data.settings.openCodeSandbox || 'workspace-write',
      openCodeApprovalPolicy: data.settings.openCodeApprovalPolicy || 'on-request',
      openCodeProvider: data.settings.openCodeProvider ?? '',
      openCodeCustomModels: data.settings.openCodeCustomModels ?? [],
      customModels: data.settings.customModels ?? [],
      followUpBehavior: 'queue',
      notifyOnTurnComplete: data.settings.notifyOnTurnComplete !== false,
      customInstructions: data.settings.customInstructions ?? '',
      sendWithModifier: Boolean(data.settings.sendWithModifier),
      translucentSidebar: data.settings.translucentSidebar !== false,
      highContrast: Boolean(data.settings.highContrast),
      pointerCursor: Boolean(data.settings.pointerCursor),
      reduceMotion: Boolean(data.settings.reduceMotion),
      uiFontSize: data.settings.uiFontSize === 'sm' || data.settings.uiFontSize === 'lg' ? data.settings.uiFontSize : 'md',
      codeFontSize: data.settings.codeFontSize === 'sm' || data.settings.codeFontSize === 'lg' ? data.settings.codeFontSize : 'md',
      preventSleepWhileRunning: Boolean(data.settings.preventSleepWhileRunning),
      alwaysOnTop: Boolean(data.settings.alwaysOnTop),
      gitBranchPrefix: data.settings.gitBranchPrefix ?? '',
      gitCommitPrefix: data.settings.gitCommitPrefix ?? '',
      gitOpenPRAfterPush: Boolean(data.settings.gitOpenPRAfterPush),
      gitPRBodyTemplate: data.settings.gitPRBodyTemplate ?? '',
      browserAllowedHosts: data.settings.browserAllowedHosts ?? [],
      browserBlockedHosts: data.settings.browserBlockedHosts ?? [],
      browserDownloadDir: data.settings.browserDownloadDir ?? '',
      browserFullCDP: Boolean(data.settings.browserFullCDP),
      shortcutCommandPalette: data.settings.shortcutCommandPalette || 'Ctrl+K',
      shortcutNewThread: data.settings.shortcutNewThread || 'Ctrl+N',
      shortcutTerminal: data.settings.shortcutTerminal || 'Ctrl+`',
      shortcutBrowser: data.settings.shortcutBrowser || 'Ctrl+Shift+B',
      networkProxyEnabled: Boolean(data.settings.networkProxyEnabled),
      networkProxyUrl: data.settings.networkProxyUrl ?? '',
      networkProxyNoProxy: data.settings.networkProxyNoProxy || 'localhost,127.0.0.1,::1',
      onboardingCompleted: Boolean(data.settings.onboardingCompleted) || Boolean(data.settings.workspace),
    }
    // Codex-only: ignore leftover Claude/Gemini/Grok model preferences until catalog loads.
    if (/claude|gemini|grok|sonnet|opus|haiku|fable/i.test(settings.value.model) && !/^(gpt-|o[1-9]|codex)/i.test(settings.value.model)) {
      settings.value = { ...settings.value, model: '' }
    }
    terminalProfiles.value = data.terminalProfiles ?? []
    workspace.value = data.workspace ? { ...data.workspace, changes: data.workspace.changes ?? [] } : null
    initAppearance({
      theme: settings.value.theme as 'light' | 'dark' | 'system',
      accent: settings.value.accentColor as AppAccent,
      font: settings.value.fontFamily || 'system',
      uiFontSize: settings.value.uiFontSize,
      codeFontSize: settings.value.codeFontSize,
      translucentSidebar: settings.value.translucentSidebar,
      highContrast: settings.value.highContrast,
      pointerCursor: settings.value.pointerCursor,
      reduceMotion: settings.value.reduceMotion,
      runtime: normalizeRuntimeID(settings.value.activeRuntime),
    })
    applyLocale(settings.value.language)
    void loadSystemFonts()
    void checkForUpdates(true)
  }

  async function setActiveRuntime(runtimeID: WorkspaceRuntime): Promise<boolean> {
    const nextRuntime = normalizeRuntimeID(runtimeID)
    if (activeRuntime.value !== nextRuntime) {
      // Instant UI switch — backend persistence continues in the returned promise.
      settings.value = { ...settings.value, activeRuntime: nextRuntime }
      accountUsage.value = null
      setRuntime(nextRuntime)
      patchSettings({ activeRuntime: nextRuntime })
    }
    // Keep backend transitions ordered while preserving the instant UI switch.
    const request = runtimeSync
      .catch(() => undefined)
      .then(async () => {
        await backend.SetActiveRuntime(nextRuntime)
      })
    runtimeSync = request
    try {
      await request
      // The runtime switch is persisted before reading the runtime-scoped
      // usage store. This prevents the old provider's snapshot from winning a
      // fast tab switch.
      void loadLocalUsage()
      return activeRuntime.value === nextRuntime
    } catch (error) {
      if (activeRuntime.value === nextRuntime) {
        notify('error', translate('sidebar.runtimeSwitchFailed'), errorMessage(error))
      }
      return false
    }
  }

  async function ensureActiveRuntimeSynced(runtimeID: WorkspaceRuntime): Promise<boolean> {
    try {
      await runtimeSync
    } catch {
      const retry = runtimeSync
        .catch(() => undefined)
        .then(async () => {
          await backend.SetActiveRuntime(runtimeID)
        })
      runtimeSync = retry
      try {
        await retry
      } catch {
        return false
      }
    }
    return activeRuntime.value === runtimeID
  }

  /** Merge saved prefs without dropping Grok-specific fields or activeRuntime. */
  function mergeSavedSettings(saved: UserSettings, next: UserSettings): UserSettings {
    const runtime = normalizeRuntimeID(saved.activeRuntime || next.activeRuntime)
    return {
      ...defaultSettings,
      ...saved,
      activeRuntime: runtime,
      recentWorkspaces: saved.recentWorkspaces ?? next.recentWorkspaces ?? [],
      grokWorkspace: saved.grokWorkspace ?? next.grokWorkspace ?? '',
      grokRecentWorkspaces: saved.grokRecentWorkspaces ?? next.grokRecentWorkspaces ?? [],
      grokBackend: saved.grokBackend === 'api' || next.grokBackend === 'api' ? 'api' : 'build',
      grokBuildModel: saved.grokBuildModel ?? next.grokBuildModel ?? '',
      grokAPIModel: saved.grokAPIModel ?? next.grokAPIModel ?? '',
      grokEffort: saved.grokEffort || next.grokEffort || 'high',
      grokSandbox: saved.grokSandbox || next.grokSandbox || 'workspace-write',
      grokApprovalPolicy: saved.grokApprovalPolicy || next.grokApprovalPolicy || 'on-request',
      grokWebSearch: saved.grokWebSearch !== undefined ? saved.grokWebSearch : next.grokWebSearch !== false,
      grokXSearch: saved.grokXSearch !== undefined ? Boolean(saved.grokXSearch) : Boolean(next.grokXSearch),
      grokAPIKey: saved.grokAPIKey ?? next.grokAPIKey ?? '',
      grokAPIBaseURL: saved.grokAPIBaseURL ?? next.grokAPIBaseURL ?? '',
      claudeWorkspace: saved.claudeWorkspace ?? next.claudeWorkspace ?? '',
      claudeRecentWorkspaces: saved.claudeRecentWorkspaces ?? next.claudeRecentWorkspaces ?? [],
      claudeModel: saved.claudeModel || next.claudeModel || 'sonnet',
      claudeEffort: saved.claudeEffort || next.claudeEffort || 'high',
      claudeSandbox: saved.claudeSandbox || next.claudeSandbox || 'workspace-write',
      claudeApprovalPolicy: saved.claudeApprovalPolicy || next.claudeApprovalPolicy || 'on-request',
      claudePermissionMode: saved.claudePermissionMode || next.claudePermissionMode || 'acceptEdits',
      claudeCustomModels: saved.claudeCustomModels ?? next.claudeCustomModels ?? [],
      grokCustomModels: saved.grokCustomModels ?? next.grokCustomModels ?? [],
      geminiWorkspace: saved.geminiWorkspace ?? next.geminiWorkspace ?? '',
      geminiRecentWorkspaces: saved.geminiRecentWorkspaces ?? next.geminiRecentWorkspaces ?? [],
      geminiModel: saved.geminiModel || next.geminiModel || 'gemini-2.5-pro',
      geminiEffort: saved.geminiEffort || next.geminiEffort || 'auto',
      geminiSandbox: saved.geminiSandbox || next.geminiSandbox || 'workspace-write',
      geminiApprovalPolicy: saved.geminiApprovalPolicy || next.geminiApprovalPolicy || 'on-request',
      geminiCustomModels: saved.geminiCustomModels ?? next.geminiCustomModels ?? [],
      openCodeModel: saved.openCodeModel || next.openCodeModel || 'anthropic/claude-sonnet-4-6',
      openCodeWorkspace: saved.openCodeWorkspace ?? next.openCodeWorkspace ?? '',
      openCodeRecentWorkspaces: saved.openCodeRecentWorkspaces ?? next.openCodeRecentWorkspaces ?? [],
      openCodeEffort: saved.openCodeEffort || next.openCodeEffort || 'high',
      openCodeSandbox: saved.openCodeSandbox || next.openCodeSandbox || 'workspace-write',
      openCodeApprovalPolicy: saved.openCodeApprovalPolicy || next.openCodeApprovalPolicy || 'on-request',
      openCodeProvider: saved.openCodeProvider ?? next.openCodeProvider ?? '',
      openCodeCustomModels: saved.openCodeCustomModels ?? next.openCodeCustomModels ?? [],
      customModels: saved.customModels ?? next.customModels ?? [],
      onboardingCompleted: Boolean(saved.onboardingCompleted)
        || Boolean(next.onboardingCompleted)
        || Boolean(saved.workspace || next.workspace),
    }
  }

  async function loadSystemFonts(): Promise<void> {
    try {
      const fonts = await backend.ListSystemFonts()
      systemFonts.value = (fonts ?? [])
        .map((font) => ({ family: font.family, source: font.source }))
        .filter((font) => font.family)
    } catch {
      systemFonts.value = []
    }
  }

  async function checkForUpdates(silent = false): Promise<boolean> {
    updateChecking.value = true
    updateCheckError.value = ''
    try {
      const info = await backend.CheckForUpdates()
      const next = {
        currentVersion: info.currentVersion || appVersion.value,
        latestVersion: info.latestVersion || appVersion.value,
        updateAvailable: Boolean(info.updateAvailable),
        releaseUrl: info.releaseUrl || '',
        downloadUrl: info.downloadUrl || '',
        releaseNotes: info.releaseNotes || '',
      }
      updateInfo.value = next
      appVersion.value = next.currentVersion || appVersion.value
      // Toast only for background/manual paths that are not using the dialog.
      if (!silent && !updateDialogOpen.value) {
        if (next.updateAvailable) {
          notify('info', translate('updates.available'), translate('updates.availableDialogHint', { version: next.latestVersion }))
        } else {
          notify('info', translate('updates.upToDate'), translate('updates.upToDateHint'))
        }
      }
      return true
    } catch (error) {
      const message = errorMessage(error)
      updateCheckError.value = message
      if (!silent && !updateDialogOpen.value) {
        notify('warning', translate('updates.checkFailed'), message)
      }
      return false
    } finally {
      updateChecking.value = false
    }
  }

  async function openUpdateCheckDialog(): Promise<void> {
    updateDialogOpen.value = true
    await checkForUpdates(true)
  }

  async function openUpdatePage(): Promise<void> {
    const url = updateInfo.value?.releaseUrl || `https://github.com/${updateRepo.value}/releases`
    if (!url) return
    try {
      await backend.OpenExternal(url)
    } catch (error) {
      notify('error', translate('updates.openFailed'), errorMessage(error))
    }
  }

  function bindUpdateEvents(): void {
    if (updateEventUnsub) return
    updateEventUnsub = Events.On('nice:update', (event) => {
      const data = asRecord(event?.data)
      updateProgress.value = {
        phase: asString(data.phase),
        percent: typeof data.percent === 'number' ? data.percent : 0,
        bytesReceived: typeof data.bytesReceived === 'number' ? data.bytesReceived : 0,
        bytesTotal: typeof data.bytesTotal === 'number' ? data.bytesTotal : 0,
        message: asString(data.message),
        error: asString(data.error),
        readyToRestart: data.readyToRestart === true,
      }
      if (data.phase === 'error' && data.error) {
        updateCheckError.value = asString(data.error)
        updateInstalling.value = false
      }
      if (data.phase === 'ready') {
        updateInstalling.value = false
      }
    }) as unknown as () => void
  }

  async function downloadAndInstallUpdate(): Promise<void> {
    if (!updateInfo.value?.updateAvailable || updateInstalling.value) return
    bindUpdateEvents()
    updateInstalling.value = true
    updateCheckError.value = ''
    updateProgress.value = {
      phase: 'downloading',
      percent: 0,
      bytesReceived: 0,
      bytesTotal: 0,
      message: translate('updates.downloading'),
      error: '',
      readyToRestart: false,
    }
    try {
      const status = await backend.DownloadAndStageUpdate()
      updateProgress.value = {
        phase: status.phase || 'downloading',
        percent: status.percent || 0,
        bytesReceived: status.bytesReceived || 0,
        bytesTotal: status.bytesTotal || 0,
        message: status.message || translate('updates.downloading'),
        error: status.error || '',
        readyToRestart: Boolean(status.readyToRestart),
      }
      if (status.phase === 'ready' || status.phase === 'error' || status.phase === 'idle') {
        updateInstalling.value = false
      }
    } catch (error) {
      updateCheckError.value = errorMessage(error)
      updateProgress.value = {
        phase: 'error',
        percent: 0,
        bytesReceived: 0,
        bytesTotal: 0,
        message: translate('updates.installFailed'),
        error: errorMessage(error),
        readyToRestart: false,
      }
      notify('error', translate('updates.installFailed'), errorMessage(error))
      updateInstalling.value = false
    }
  }

  async function applyUpdateAndRestart(): Promise<void> {
    updateInstalling.value = true
    try {
      await backend.ApplyUpdateAndRestart()
      notify('info', translate('updates.restarting'), translate('updates.restartingHint'))
    } catch (error) {
      updateCheckError.value = errorMessage(error)
      notify('error', translate('updates.installFailed'), errorMessage(error))
      updateInstalling.value = false
    }
  }

  async function completeOnboarding(options: { theme: string; language: string }): Promise<void> {
    const next = {
      ...settings.value,
      theme: options.theme,
      language: options.language,
      onboardingCompleted: true,
    }
    // Optimistic: leave the wizard immediately; persist must not regress this flag.
    settings.value = next
    applyAppearance(next)
    applyLocale(options.language)
    try {
      await savePreferences(next, { silent: true })
    } finally {
      if (!settings.value.onboardingCompleted) {
        settings.value = { ...settings.value, onboardingCompleted: true }
      }
    }
  }

  async function openReleasesPage(): Promise<void> {
    const url = updateInfo.value?.releaseUrl || `https://github.com/${updateRepo.value}/releases`
    try {
      await backend.OpenExternal(url)
    } catch (error) {
      notify('error', translate('updates.openFailed'), errorMessage(error))
    }
  }

  async function openGitHubRepo(): Promise<void> {
    try {
      await backend.OpenExternal(`https://github.com/${updateRepo.value}`)
    } catch (error) {
      notify('error', translate('updates.openFailed'), errorMessage(error))
    }
  }

  async function savePreferences(next: UserSettings, options: { silent?: boolean } = {}): Promise<void> {
    const version = beginPreferenceWrite()
    try {
      const saved = await queuePreferenceWrite({
        ...next,
        recentWorkspaces: next.recentWorkspaces ?? [],
        activeRuntime: normalizeRuntimeID(next.activeRuntime),
      })
      if (version !== preferenceVersion) return
      settings.value = mergeSavedSettings(saved, next)
      applyAppearance(settings.value)
      setRuntime(normalizeRuntimeID(settings.value.activeRuntime))
      supportedLocales.find((item) => item.value === settings.value.language)
        ? setLocale(settings.value.language)
        : setLocale('zh-CN')
      if (!options.silent) {
        notify('success', translate('notifications.preferencesSaved'), translate('notifications.preferencesSavedHint'))
      }
    } catch (error) {
      if (version === preferenceVersion) {
        notify('error', translate('notifications.preferencesFailed'), errorMessage(error))
      }
      throw error
    }
  }

  async function toggleTheme(): Promise<void> {
    const previous = settings.value
    const systemIsLight = window.matchMedia('(prefers-color-scheme: light)').matches
    const nextTheme = previous.theme === 'dark'
      ? 'light'
      : previous.theme === 'light'
        ? 'dark'
        : systemIsLight ? 'dark' : 'light'
    const next = { ...previous, theme: nextTheme }
    const version = beginPreferenceWrite()
    settings.value = next
    applyAppearance(next)
    try {
      const saved = await queuePreferenceWrite(next)
      if (version !== preferenceVersion) return
      settings.value = mergeSavedSettings(saved, next)
      applyAppearance(settings.value)
    } catch (error) {
      if (version !== preferenceVersion) return
      settings.value = previous
      applyAppearance(previous)
      notify('error', translate('notifications.preferencesFailed'), errorMessage(error))
    }
  }

  function previewAppearance(appearance: Pick<UserSettings, 'theme' | 'accentColor' | 'fontFamily' | 'uiFontSize' | 'codeFontSize' | 'translucentSidebar' | 'highContrast' | 'pointerCursor' | 'reduceMotion'>): void {
    applyAppearance(appearance)
  }

  function restoreAppearance(): void {
    applyAppearance(settings.value)
  }

  function applyAppearance(appearance: Pick<UserSettings, 'theme' | 'accentColor' | 'fontFamily' | 'uiFontSize' | 'codeFontSize' | 'translucentSidebar' | 'highContrast' | 'pointerCursor' | 'reduceMotion'>): void {
    setTheme(appearance.theme as 'light' | 'dark' | 'system')
    setAccent(appearance.accentColor as AppAccent)
    setFont(appearance.fontFamily || 'system')
    setUiPrefs({
      uiFontSize: appearance.uiFontSize,
      codeFontSize: appearance.codeFontSize,
      translucentSidebar: appearance.translucentSidebar,
      highContrast: appearance.highContrast,
      pointerCursor: appearance.pointerCursor,
      reduceMotion: appearance.reduceMotion,
    })
  }

  async function loadAccount(): Promise<void> {
    const response = await backend.ReadAccount()
    account.value = normalizeAccount(response)
  }

  async function loadLocalUsage(): Promise<void> {
    const sequence = ++usageLoadSequence
    const requestedRuntime = activeRuntime.value
    try {
      const usage = await backend.ReadAccountUsage()
      if (sequence !== usageLoadSequence || requestedRuntime !== activeRuntime.value) return
      let normalized = normalizeAccountUsage(usage)
      // Keep a second read path for packaged builds where the generated
      // ReadAccountUsage bridge can return an empty snapshot while the native
      // Gemini/OpenCode catalog is already available. This is deliberately a
      // fallback; Codex/Claude/Grok continue to use their normal usage store.
      if (!normalized && (requestedRuntime === 'gemini' || requestedRuntime === 'opencode')) {
        try {
          const catalog = await backend.ReadExternalRuntimeCatalog(
            requestedRuntime,
            requestedRuntime === activeRuntime.value ? currentWorkspacePath.value : '',
          )
          const native = asRecord(catalog.usage)
          normalized = normalizeAccountUsage({
            runtime: requestedRuntime,
            summary: {
              lifetimeTokens: asNumber(native.totalTokens),
              lifetimeInputTokens: asNumber(native.inputTokens),
              lifetimeCachedInputTokens: asNumber(native.cachedTokens),
              lifetimeOutputTokens: asNumber(native.outputTokens),
              lifetimeReasoningTokens: asNumber(native.reasoningTokens),
            },
            dailyUsageBuckets: [],
          })
        } catch {
          // The native CLI/database is optional; keep the empty state.
        }
      }
      const responseRuntime = normalized?.runtime
      // A request issued just before SetActiveRuntime completes can return the
      // previous provider's usage. Do not show it under the new provider.
      if (responseRuntime && responseRuntime !== requestedRuntime) return
      accountUsage.value = normalized
    } catch {
      // Keep previous snapshot if the local store is temporarily unavailable.
    }
  }

  async function loadAccountInsights(): Promise<void> {
    const usagePromise = loadLocalUsage()
    if (!account.value.authenticated || account.value.type.toLocaleLowerCase() !== 'chatgpt') {
      accountRateLimits.value = null
      await usagePromise
      return
    }
    const [rateLimitsResult] = await Promise.allSettled([
      backend.ReadAccountRateLimits(),
      usagePromise,
    ])
    if (rateLimitsResult.status === 'fulfilled') {
      accountRateLimits.value = normalizeAccountRateLimits(rateLimitsResult.value)
    }
  }

  async function refreshAccountData(): Promise<void> {
    try {
      await loadAccount()
      await loadAccountInsights()
    } catch {
      accountRateLimits.value = null
      await loadLocalUsage()
    }
  }

  async function startLogin(): Promise<void> {
    try {
      const response = await backend.StartChatGPTLogin()
      const authURL = asString(asRecord(response).authUrl)
      if (authURL) {
        await backend.OpenExternal(authURL)
        notify('info', translate('notifications.continueBrowser'), translate('notifications.continueBrowserHint'))
      } else {
        await loadAccount()
      }
    } catch (error) {
      notify('error', translate('notifications.signInStartFailed'), errorMessage(error))
    }
  }

  async function logout(): Promise<void> {
    try {
      await backend.LogoutAccount()
      account.value = { ...emptyAccount }
      accountRateLimits.value = null
      await loadLocalUsage()
      notify('success', translate('notifications.signedOut'), translate('notifications.signedOutHint'))
    } catch (error) {
      notify('error', translate('notifications.signOutFailed'), errorMessage(error))
    }
  }

  function updateAgentPreferences(
    model: string,
    effort: string,
    serviceTier = settings.value.serviceTier,
    collaborationMode = settings.value.collaborationMode,
  ): void {
    patchSettings({ model, effort, serviceTier, collaborationMode })
  }

  /** Instant local settings update + debounced disk persist (keeps provider switching snappy). */
  function patchSettings(partial: Partial<UserSettings>): void {
    settings.value = { ...settings.value, ...partial }
    const version = ++preferenceVersion
    if (preferenceTimer) window.clearTimeout(preferenceTimer)
    preferenceTimer = window.setTimeout(() => {
      preferenceTimer = 0
      void persistAgentPreferences(version)
    }, 120)
  }

  async function persistAgentPreferences(version: number): Promise<void> {
    const next = settings.value
    try {
      const saved = await queuePreferenceWrite(next)
      if (version === preferenceVersion) {
        settings.value = mergeSavedSettings(saved, next)
        setRuntime(normalizeRuntimeID(settings.value.activeRuntime))
      }
    } catch (error) {
      if (version === preferenceVersion) {
        notify('error', translate('notifications.agentPreferencesFailed'), errorMessage(error))
      }
    }
  }

  function beginPreferenceWrite(): number {
    const version = ++preferenceVersion
    if (preferenceTimer) {
      window.clearTimeout(preferenceTimer)
      preferenceTimer = 0
    }
    return version
  }

  function queuePreferenceWrite(next: UserSettings): Promise<UserSettings> {
    const request = preferenceSync
      .catch(() => undefined)
      .then(() => backend.SavePreferences(next))
    preferenceSync = request.then(() => undefined, () => undefined)
    return request
  }

  function updateGrokPreferences(partial: Partial<Pick<UserSettings,
    'grokBackend' | 'grokBuildModel' | 'grokAPIModel' | 'grokEffort' | 'grokSandbox' | 'grokApprovalPolicy' | 'grokWebSearch' | 'grokXSearch'
  >>): void {
    patchSettings(partial)
  }

  function applyLocale(value: string): void {
    const locale = supportedLocales.find((item) => item.value === value)?.value ?? 'zh-CN'
    setLocale(locale)
  }

  return {
    bootstrapping,
    settings,
    workspaceOrderByRuntime,
    workspace,
    codexAvailable,
    codexVersion,
    appVersion,
    updateRepo,
    systemFonts,
    updateInfo,
    updateDialogOpen,
    updateChecking,
    updateCheckError,
    updateProgress,
    updateInstalling,
    terminalProfiles,
    account,
    accountRateLimits,
    accountUsage,
    models,
    modelProviders,
    agentProviders,
    currentWorkspacePath,
    currentTheme,
    activeRuntime,
    isGrokMode,
    isClaudeMode,
    isGeminiMode,
    isOpenCodeMode,
    isCodexMode,
    orderWorkspacePaths,
    setWorkspaceOrder,
    bootstrap,
    refreshRuntimes,
    setActiveRuntime,
    ensureActiveRuntimeSynced,
    savePreferences,
    toggleTheme,
    previewAppearance,
    restoreAppearance,
    loadSystemFonts,
    checkForUpdates,
    openUpdateCheckDialog,
    openUpdatePage,
    downloadAndInstallUpdate,
    applyUpdateAndRestart,
    completeOnboarding,
    openReleasesPage,
    openGitHubRepo,
    loadAccount,
    loadAccountInsights,
    loadLocalUsage,
    refreshAccountData,
    startLogin,
    logout,
    updateAgentPreferences,
    updateGrokPreferences,
    patchSettings,
  }
})

function normalizeRuntimeID(value: string | undefined | null): WorkspaceRuntime {
  const id = String(value || '').trim().toLowerCase()
  if (id === 'grok') return 'grok'
  if (id === 'claude') return 'claude'
  if (id === 'gemini') return 'gemini'
  if (id === 'opencode' || id === 'open-code') return 'opencode'
  return 'codex'
}

function loadWorkspaceOrder(): WorkspaceOrderByRuntime {
  const fallback: WorkspaceOrderByRuntime = { codex: [], claude: [], grok: [], gemini: [], opencode: [] }
  if (typeof window === 'undefined') return fallback
  try {
    const value = asRecord(JSON.parse(window.localStorage.getItem(workspaceOrderStorageKey) || '{}'))
    return {
      codex: sanitizeWorkspaceOrder(value.codex),
      claude: sanitizeWorkspaceOrder(value.claude),
      grok: sanitizeWorkspaceOrder(value.grok),
      gemini: sanitizeWorkspaceOrder(value.gemini),
      opencode: sanitizeWorkspaceOrder(value.opencode),
    }
  } catch {
    return fallback
  }
}

function sanitizeWorkspaceOrder(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  const result: string[] = []
  const seen = new Set<string>()
  for (const item of value) {
    if (typeof item !== 'string') continue
    const path = item.trim()
    const key = workspaceKey(path)
    if (!path || path.length > 1024 || seen.has(key)) continue
    seen.add(key)
    result.push(path)
    if (result.length === 200) break
  }
  return result
}

function persistWorkspaceOrder(value: WorkspaceOrderByRuntime): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(workspaceOrderStorageKey, JSON.stringify(value))
  } catch {
    // Ordering remains reactive for this run when local storage is unavailable.
  }
}

function errorMessage(error: unknown): string {
  return friendlyErrorMessage(error)
}
