import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

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
  accentColor: 'amber',
  fontFamily: 'manrope',
  terminalProfile: 'powershell',
  language: 'zh-CN',
  autoConnect: true,
  workMode: 'code',
  sendWithModifier: false,
  followUpBehavior: 'steer',
  notifyOnTurnComplete: true,
  customInstructions: '',
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
  const { initAppearance, setTheme, setAccent, setFont } = useAppearance()

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
      font: settings.value.fontFamily || 'manrope',
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
    const url = updateInfo.value?.downloadUrl || updateInfo.value?.releaseUrl || `https://github.com/${updateRepo.value}/releases`
    if (!url) return
    try {
      await backend.OpenExternal(url)
    } catch (error) {
      notify('error', translate('updates.openFailed'), errorMessage(error))
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
      settings.value = { ...saved, recentWorkspaces: saved.recentWorkspaces ?? [], customModels: saved.customModels ?? [] }
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

  function previewAppearance(appearance: Pick<UserSettings, 'theme' | 'accentColor' | 'fontFamily'>): void {
    applyAppearance(appearance)
  }

  function restoreAppearance(): void {
    applyAppearance(settings.value)
  }

  function applyAppearance(appearance: Pick<UserSettings, 'theme' | 'accentColor' | 'fontFamily'>): void {
    setTheme(appearance.theme as 'light' | 'dark' | 'system')
    setAccent(appearance.accentColor as AppAccent)
    setFont(appearance.fontFamily || 'manrope')
  }

  async function loadAccount(): Promise<void> {
    const response = await backend.ReadAccount()
    account.value = normalizeAccount(response)
  }

  async function loadAccountInsights(): Promise<void> {
    if (!account.value.authenticated || account.value.type.toLocaleLowerCase() !== 'chatgpt') {
      accountRateLimits.value = null
      accountUsage.value = null
      return
    }
    const [rateLimitsResult, usageResult] = await Promise.allSettled([
      backend.ReadAccountRateLimits(),
      backend.ReadAccountUsage(),
    ])
    if (rateLimitsResult.status === 'fulfilled') {
      accountRateLimits.value = normalizeAccountRateLimits(rateLimitsResult.value)
    }
    if (usageResult.status === 'fulfilled') {
      accountUsage.value = normalizeAccountUsage(usageResult.value)
    }
  }

  async function refreshAccountData(): Promise<void> {
    try {
      await loadAccount()
      await loadAccountInsights()
    } catch {
      accountRateLimits.value = null
      accountUsage.value = null
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
      accountUsage.value = null
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
    openReleasesPage,
    openGitHubRepo,
    loadAccount,
    loadAccountInsights,
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
