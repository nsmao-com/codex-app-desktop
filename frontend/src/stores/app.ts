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
import {
  asRecord,
  asString,
  normalizeAccount,
  normalizeAccountRateLimits,
  normalizeAccountUsage,
} from '../utils/protocol'
import { translate } from '../i18n'

const AppVersionFallback = '1.0.1'

const defaultSettings: UserSettings = {
  workspace: '',
  recentWorkspaces: [],
  model: '',
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
  followUpBehavior: 'steer',
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
  const { initAppearance, setTheme, setAccent, setFont, setUiPrefs } = useAppearance()

  const bootstrapping = shallowRef(true)
  const settings = shallowRef<UserSettings>({ ...defaultSettings })
  const workspace = shallowRef<WorkspaceInfo | null>(null)
  const codexAvailable = shallowRef(false)
  const codexVersion = shallowRef('')
  const appVersion = shallowRef('1.0.1')
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
  const models = shallowRef<import('../types/codex').ModelOption[]>([])
  const modelProviders = shallowRef<import('../types/codex').ModelProviderOption[]>([])
  const agentProviders = shallowRef<AgentProviderRuntime[]>([])

  const currentWorkspacePath = computed(() => settings.value.workspace)
  const currentTheme = computed(() => settings.value.theme)

  let preferenceTimer = 0
  let preferenceVersion = 0

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

  function applyBootstrap(data: BootstrapData): void {
    codexAvailable.value = data.codex.available
    codexVersion.value = data.codex.version
    appVersion.value = asString(data.appVersion, AppVersionFallback)
    updateRepo.value = asString(data.updateRepo, 'nsmao-com/codex-app-desktop')
    agentProviders.value = data.agentProviders ?? []
    settings.value = {
      ...defaultSettings,
      ...data.settings,
      modelProvider: '',
      recentWorkspaces: data.settings.recentWorkspaces ?? [],
      customModels: data.settings.customModels ?? [],
      followUpBehavior: data.settings.followUpBehavior === 'queue' ? 'queue' : 'steer',
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
    })
    applyLocale(settings.value.language)
    void loadSystemFonts()
    void checkForUpdates(true)
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
    try {
      const saved = await backend.SavePreferences({
        ...next,
        recentWorkspaces: next.recentWorkspaces ?? [],
      })
      // Older backends may omit newer bools; never let a round-trip clear onboarding.
      const onboardingCompleted = Boolean(saved.onboardingCompleted)
        || Boolean(next.onboardingCompleted)
        || Boolean(saved.workspace || next.workspace)
      settings.value = {
        ...saved,
        recentWorkspaces: saved.recentWorkspaces ?? [],
        customModels: saved.customModels ?? [],
        onboardingCompleted,
      }
      applyAppearance(settings.value)
      supportedLocales.find((item) => item.value === saved.language)
        ? setLocale(saved.language)
        : setLocale('zh-CN')
      if (!options.silent) {
        notify('success', translate('notifications.preferencesSaved'), translate('notifications.preferencesSavedHint'))
      }
    } catch (error) {
      notify('error', translate('notifications.preferencesFailed'), errorMessage(error))
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
    settings.value = next
    applyAppearance(next)
    try {
      const saved = await backend.SavePreferences(next)
      settings.value = { ...saved, recentWorkspaces: saved.recentWorkspaces ?? [], customModels: saved.customModels ?? [] }
    } catch (error) {
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
    try {
      const usage = await backend.ReadAccountUsage()
      accountUsage.value = normalizeAccountUsage(usage)
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
      const saved = await backend.SavePreferences(next)
      if (version === preferenceVersion) {
        settings.value = { ...saved, recentWorkspaces: saved.recentWorkspaces ?? [], customModels: saved.customModels ?? [] }
      }
    } catch (error) {
      if (version === preferenceVersion) {
        notify('error', translate('notifications.agentPreferencesFailed'), errorMessage(error))
      }
    }
  }

  function applyLocale(value: string): void {
    const locale = supportedLocales.find((item) => item.value === value)?.value ?? 'zh-CN'
    setLocale(locale)
  }

  return {
    bootstrapping,
    settings,
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
    bootstrap,
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
    patchSettings,
  }
})

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : translate('notifications.unexpected')
}
