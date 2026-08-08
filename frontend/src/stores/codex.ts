import { Events } from '@wailsio/runtime'
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type {
  SendMessageRequest,
  SessionPreferencesRequest,
  SteerTurnRequest,
} from '../../bindings/nice_codex_desktop/models'
import type { Event as CodexEvent, Status as CodexStatus } from '../../bindings/nice_codex_desktop/internal/codex/models'
import { useAppStore } from './app'
import { useCapabilitiesStore } from './capabilities'
import { useDialogStore } from './dialog'
import { useTerminalStore } from './terminal'
import { useWorkspaceStore } from './workspace'
import { notify } from '../utils/notify'
import { sameWorkspacePath as sameWorkspace, workspaceKey } from '../utils/workspacePath'
import {
  buildRuntimeProviders,
  cleanModelDisplayName,
  DEFAULT_CODEX_MODEL,
  DEFAULT_CODEX_REASONING,
  FALLBACK_CODEX_MODELS,
  selectCodexCatalog,
} from '../utils/runtimeProviders'
import { translate } from '../i18n'
import type {
  PendingServerRequest,
  QueuedMessage,
  ThreadGroup,
  ThreadSummary,
  TimelineItem,
  TimelineItemType,
  TokenUsageBreakdown,
  TurnFeedback,
  TurnMetrics,
} from '../types/codex'
import {
  asArray,
  asRecord,
  asString,
  isActiveStatus,
  isFailedStatus,
  isInterruptedStatus,
  isTerminalTurnStatus,
  metricsFromTurns,
  normalizeAccountRateLimits,
  normalizeStatus,
  normalizeThread,
  normalizeThreadStatus,
  normalizeThreadTokenUsage,
  normalizeTimelineItem,
  timelineFromTurns,
} from '../utils/protocol'

export interface ThreadModelIdentity {
  model: string
  provider: string
}

interface DeltaBuffer {
  threadId: string
  itemId: string
  turnId: string
  field: 'text' | 'output' | 'reasoningSummary' | 'reasoningContent'
  type: TimelineItemType
  delta: string
}

interface PendingThreadSubmission {
  blockerId: string
  messageId: string
  previousTurnId: string
  requestStarted: boolean
  turnId: string
}

interface ThreadHistoryState {
  start: number
  total: number
  turnOffset: number
  hasEarlier: boolean
  loadingEarlier: boolean
  loadedUpdatedAt: number
}

type CollaborationMode = 'default' | 'plan'
type LocalUsageRuntime = 'codex' | 'claude' | 'grok' | 'gemini' | 'opencode'

const PENDING_SUBMISSION_ERROR_GRACE_MS = 750

const emptyTurnMetrics = (): TurnMetrics => ({
  tokenUsage: null,
  startedAt: null,
  completedAt: null,
  durationMs: null,
})

export const useCodexStore = defineStore('codex', () => {
  const appStore = useAppStore()
  const workspaceStore = useWorkspaceStore()
  const capabilitiesStore = useCapabilitiesStore()
  const terminalStore = useTerminalStore()
  const dialogStore = useDialogStore()

  const busy = shallowRef(false)
  const sendingThreadIds = shallowRef<string[]>([])
  const interruptingTurn = shallowRef(false)
  const threadMutation = shallowRef('')
  const connection = shallowRef<CodexStatus>({
    state: 'disconnected',
    running: false,
    message: 'Not connected',
    binary: '',
    version: '',
    workspace: '',
  })
  const lastTransportMessage = shallowRef('')

  const threads = shallowRef<ThreadSummary[]>([])
  const archivedThreads = shallowRef<ThreadSummary[]>([])
  const projectThreads = shallowRef<Record<string, ThreadSummary[]>>({})
  const projectErrors = shallowRef<Record<string, string>>({})
  const loadingProjects = shallowRef<string[]>([])
  // Share in-flight project list reads between the background recent-project
  // scan and an explicit folder click. Without this, a click can observe the
  // same path as "loading" and return before its sessions are available.
  const projectLoadPromises = new Map<string, Promise<ThreadSummary[] | null>>()
  const threadSearch = shallowRef('')
  const activeThreadId = shallowRef('')
  const activeThread = shallowRef<ThreadSummary | null>(null)
  const activeTurnByThread = shallowRef<Record<string, string>>({})
  const turnFeedbackByThread = shallowRef<Record<string, TurnFeedback>>({})
  const queuedMessagesByThread = shallowRef<Record<string, QueuedMessage[]>>({})
  const loadingThreadId = shallowRef('')
  const loadingSequenceByThread = new Map<string, number>()
  const workspaceSelectionSequenceByThread = new Map<string, number>()
  const threadAlias = new Map<string, string>()
  const pendingSubmissionByThread = new Map<string, PendingThreadSubmission>()
  const creatingThread = shallowRef(false)
  const itemsByThread = shallowRef<Record<string, TimelineItem[]>>({})
  const historyByThread = shallowRef<Record<string, ThreadHistoryState>>({})
  const diffsByTurn = shallowRef<Record<string, string>>({})
  const latestDiffByThread = shallowRef<Record<string, string>>({})
  const tokenUsageByThread = shallowRef<Record<string, ReturnType<typeof normalizeThreadTokenUsage>>>({})
  const turnMetricsByThread = shallowRef<Record<string, Record<string, TurnMetrics>>>({})
  const pendingRequests = shallowRef<PendingServerRequest[]>([])
  const completedTurns = new Set<string>()
  const completedTurnStatus = new Map<string, string>()
  const latestStartedTurnByThread = new Map<string, string>()
  const loadedThreadIDs = new Set<string>()
  const lastThreadByWorkspace = shallowRef<Record<string, string>>(loadLastThreadByWorkspace())
  const pinnedThreadIds = shallowRef<string[]>(loadPinnedThreadIds())
  /** Official Codex: after a plan turn, ask "Implement this plan?" */
  const planImplementPrompt = shallowRef<null | {
    threadId: string
    turnId: string
    planText: string
  }>(null)
  const pendingPlanByThread = new Map<string, { turnId: string; text: string }>()
  /** Plan candidate from update_plan; the turn's submitted mode still gates the prompt. */
  const sawPlanUpdateByTurn = new Map<string, string>()
  /** Mode awaiting a turn id while SendMessage and turn/started race each other. */
  const pendingCollaborationModeByThread = new Map<string, CollaborationMode>()
  /** Exact mode submitted for each live turn; never infer this from mutable thread state. */
  const collaborationModeByTurn = new Map<string, CollaborationMode>()
  /** Serialize full preference snapshots so a stale model/effort write cannot win. */
  const sessionPreferenceWrites = new Map<string, Promise<void>>()
  const planOfferRetryTimers = new Map<string, number[]>()
  const idleReconcileTimers = new Map<string, number>()
  const recentCompactionByThread = new Map<string, number>()
  const trackedTimeouts = new Set<number>()

  let unsubscribeEvent: (() => void) | null = null
  let openThreadSequence = 0
  let createThreadSequence = 0
  let projectLoadSequence = 0
  let deltaTimer = 0
  let diffTimer = 0
  let tokenUsageTimer = 0
  let queuedMessageSequence = 0
  const deltaBuffers = new Map<string, DeltaBuffer>()
  const pendingDiffs = new Map<string, { threadId: string; turnId: string; diff: string }>()
  /** turnId → threadId for complete eviction of diffsByTurn. */
  const diffTurnOwners = new Map<string, string>()
  const pendingTokenUsage = new Map<string, {
    threadId: string
    turnId: string
    runtime: LocalUsageRuntime | ''
    usage: ReturnType<typeof normalizeThreadTokenUsage>
  }>()
  const threadModelIdentity: Record<string, ThreadModelIdentity> = loadThreadModelIdentity()

  const isReady = computed(() => {
    if (appStore.isGeminiMode || appStore.isOpenCodeMode) {
      return appStore.agentProviders.some((provider) =>
        provider.kind === appStore.activeRuntime && provider.runtimeReady,
      )
    }
    return connection.value.state === 'ready'
  })
  const activeTurnId = computed(() => threadTurnID(activeThreadId.value))
  const isTurnRunning = computed(() => threadIsRunning(activeThreadId.value))
  const sendingMessage = computed(() => isThreadSubmitting(activeThreadId.value))
  const activeItems = computed(() => itemsByThread.value[activeThreadId.value] ?? [])
  const activeHistoryHasEarlier = computed(() => historyByThread.value[activeThreadId.value]?.hasEarlier === true)
  const activeHistoryEarlierCount = computed(() => historyByThread.value[activeThreadId.value]?.turnOffset ?? 0)
  const activeHistoryLoadingEarlier = computed(() => historyByThread.value[activeThreadId.value]?.loadingEarlier === true)
  const activeQueuedMessages = computed(() => queuedMessagesByThread.value[activeThreadId.value] ?? [])
  const activeThreadBusy = computed(() => threadIsBusy(activeThreadId.value) || activeQueuedMessages.value.length > 0)
  const activeThreadUsesExternalProvider = computed(() => {
    const provider = activeThread.value?.modelProvider || ''
    return provider === '__gemini__' || provider === '__opencode__'
      || appStore.isGeminiMode || appStore.isOpenCodeMode
  })

  function runtimeIDForThread(threadID = ''): 'codex' | 'gemini' | 'opencode' {
    const thread = threadID
      ? (findThreadSummary(threadID) || (activeThread.value?.id === threadID ? activeThread.value : null))
      : activeThread.value
    const provider = (thread?.modelProvider || threadModelIdentity[threadID]?.provider || '').toLocaleLowerCase()
    if (provider === '__gemini__' || provider === 'gemini-cli') return 'gemini'
    if (provider === '__opencode__' || provider === 'opencode-cli') return 'opencode'
    if (!threadID && appStore.isGeminiMode) return 'gemini'
    if (!threadID && appStore.isOpenCodeMode) return 'opencode'
    return 'codex'
  }

  function runtimeNameForThread(threadID = ''): string {
    const runtime = runtimeIDForThread(threadID)
    if (runtime === 'gemini') return 'Gemini CLI'
    if (runtime === 'opencode') return 'OpenCode'
    return 'Codex'
  }
  const canSteerActiveTurn = computed(() => {
    const threadID = activeThreadId.value
    const turnID = threadTurnID(threadID)
    if (appStore.settings.followUpBehavior !== 'steer' || !threadID || !turnID || !isReady.value) return false
    if (threadID.startsWith('pending-thread-') || workspaceStore.switchingWorkspace) return false
    if (threadHasLoadingBarrier(threadID) || isThreadSubmitting(threadID) || pendingThreadSubmission(threadID)) return false
    if (!threadIsRunning(threadID) || activeThreadUsesExternalProvider.value) return false
    if ((queuedMessagesByThread.value[threadID] ?? []).length > 0) return false
    return !pendingRequests.value.some((request) => sameThreadSession(asString(request.data.threadId), threadID))
  })
  const activeTurnFeedback = computed(() => threadFeedback(activeThreadId.value))
  const activeTokenUsage = computed(() => tokenUsageByThread.value[activeThreadId.value] ?? null)
  const activeTurnMetrics = computed(() => turnMetricsByThread.value[activeThreadId.value] ?? {})
  const pendingRequest = computed(() => pendingRequests.value[0] ?? null)

  function activeRuntimeRecentWorkspaces(): string[] {
    if (appStore.isGeminiMode) {
      return appStore.settings.geminiRecentWorkspaces?.length
        ? appStore.settings.geminiRecentWorkspaces
        : (appStore.settings.recentWorkspaces ?? [])
    }
    if (appStore.isOpenCodeMode) {
      return appStore.settings.openCodeRecentWorkspaces?.length
        ? appStore.settings.openCodeRecentWorkspaces
        : (appStore.settings.recentWorkspaces ?? [])
    }
    return appStore.settings.recentWorkspaces ?? []
  }

  const threadGroups = computed<ThreadGroup[]>(() => {
    const recent = activeRuntimeRecentWorkspaces()
    const paths = appStore.orderWorkspacePaths(
      appStore.activeRuntime,
      uniqueWorkspacePaths(appStore.currentWorkspacePath, recent),
      recent,
    )
    return paths.map((path) => ({
      path,
      name: workspaceName(path),
      active: sameWorkspace(path, appStore.currentWorkspacePath),
      loading: loadingProjects.value.some((loadingPath) => sameWorkspace(loadingPath, path)),
      error: projectErrors.value[path] ?? '',
      threads: sortThreadsByPin(
        sameWorkspace(path, appStore.currentWorkspacePath)
          ? threads.value
          : projectThreadsForPath(path) ?? [],
      ),
    }))
  })

  const filteredThreadGroups = computed<ThreadGroup[]>(() => {
    const query = threadSearch.value.trim().toLocaleLowerCase()
    if (!query) return threadGroups.value
    return threadGroups.value
      .map((group) => {
        if (`${group.name} ${group.path}`.toLocaleLowerCase().includes(query)) return group
        return {
          ...group,
          threads: group.threads.filter((thread) =>
            `${thread.name} ${thread.preview}`.toLocaleLowerCase().includes(query),
          ),
        }
      })
      .filter((group) => group.threads.length > 0)
  })

  const runningThreadIds = computed(() => {
    const visibleIDs = new Set(
      threadGroups.value.flatMap((group) => group.threads.map((thread) => thread.id)),
    )
    return [...new Set([
      ...Object.entries(activeTurnByThread.value)
        .filter(([threadID, turnID]) => Boolean(turnID) && visibleIDs.has(threadID))
        .map(([threadID]) => threadID),
      ...sendingThreadIds.value.filter((threadID) => visibleIDs.has(threadID)),
      ...threadGroups.value.flatMap((group) =>
        group.threads.filter((thread) => thread.status === 'active').map((thread) => thread.id),
      ),
    ])]
  })

  function bootstrapEvents(): void {
    if (!unsubscribeEvent) {
      unsubscribeEvent = Events.On('codex:event', (event) => handleCodexEvent(event.data))
    }
  }

  function dispose(): void {
    unsubscribeEvent?.()
    unsubscribeEvent = null
    clearAllTrackedTimeouts()
    deltaTimer = 0
    diffTimer = 0
    tokenUsageTimer = 0
    deltaBuffers.clear()
    pendingDiffs.clear()
    pendingTokenUsage.clear()
    idleReconcileTimers.clear()
    planOfferRetryTimers.clear()
    completedTurns.clear()
    completedTurnStatus.clear()
    loadedThreadIDs.clear()
    projectLoadPromises.clear()
  }

  function trackedTimeout(callback: () => void, delay: number): number {
    let timer = 0
    timer = window.setTimeout(() => {
      trackedTimeouts.delete(timer)
      callback()
    }, delay)
    trackedTimeouts.add(timer)
    return timer
  }

  function clearTrackedTimeout(timer: number): void {
    if (!timer) return
    window.clearTimeout(timer)
    trackedTimeouts.delete(timer)
  }

  function clearAllTrackedTimeouts(): void {
    for (const timer of trackedTimeouts) window.clearTimeout(timer)
    trackedTimeouts.clear()
  }

  async function connect(
    path = appStore.currentWorkspacePath,
    options: { forceRestart?: boolean } = {},
  ): Promise<boolean> {
    if (!path || busy.value) return false
    let previousStatus: CodexStatus | null = null
    try {
      previousStatus = await backend.CodexStatus()
    } catch {
      // StartCodex below remains the source of truth when the status probe fails.
    }
    const restartingServer = options.forceRestart === true && previousStatus?.running === true
    const reusingRunningServer = previousStatus?.running === true && !restartingServer
    busy.value = true
    connection.value = {
      ...connection.value,
      state: 'starting',
      running: false,
      message: translate('app.connecting'),
      workspace: path,
    }
    try {
      if (restartingServer) await backend.StopCodex()
      await backend.StartCodex(path)
      connection.value = await backend.CodexStatus()
      if (connection.value.state !== 'ready') {
        throw new Error(connection.value.message || translate('notifications.connectionFailed'))
      }
      lastTransportMessage.value = ''
      // Only a newly started process invalidates the app-server thread cache.
      // Switching cwd while reusing a healthy server must keep every project's
      // local timeline available, including turns running in the background.
      if (!reusingRunningServer) loadedThreadIDs.clear()
      await Promise.allSettled([
        loadThreads(),
        loadModels(),
        loadModelProviders(),
        appStore.loadAccount(),
        workspaceStore.refreshWorkspace(),
      ])
      appStore.loadAccountInsights()
      const threadID = activeThreadId.value
      if (threadID) {
        await openThread(threadID)
      }
      loadRecentProjectThreads()
      // Reconnect after interrupt: clear orphan busy so queued messages can drain.
      resetOrphanedInFlightSends()
      for (const threadID of Object.keys(queuedMessagesByThread.value)) {
        clearStaleBusyState(threadID)
      }
      for (const threadID of Object.keys(itemsByThread.value)) {
        if (!threadIsRunning(threadID)) finalizeOrphanedActiveItems(threadID)
      }
      drainAvailableThreadQueues()
      return true
    } catch (error) {
      const message = errorMessage(error)
      connection.value = {
        ...connection.value,
        state: 'error',
        running: false,
        message,
        workspace: path,
      }
      lastTransportMessage.value = message
      notify('error', translate('notifications.connectionFailed'), message, {
        label: translate('common.reconnect'),
        onClick: () => connect(path),
      })
      return false
    } finally {
      busy.value = false
    }
  }

  async function disconnect(): Promise<void> {
    try {
      await backend.StopCodex()
      releaseDisconnectedTurns()
    } catch (error) {
      notify('error', translate('notifications.unableStop'), errorMessage(error))
    }
  }

  async function loadThreads(path = appStore.currentWorkspacePath): Promise<void> {
    const requestedPath = path
    const requestedRuntime = appStore.activeRuntime
    if (!requestedPath) return
    try {
      const [list, archivedResponse] = await Promise.all([
        loadProjectThreads(requestedPath, sameWorkspace(requestedPath, appStore.currentWorkspacePath)),
        sameWorkspace(requestedPath, appStore.currentWorkspacePath)
          ? backend.ListArchivedThreads('').catch(() => ({ data: [] }))
          : Promise.resolve({ data: [] }),
      ])
      // A runtime switch can happen while the native history request is in
      // flight. Never let the old runtime overwrite the newly selected list.
      if (requestedRuntime !== appStore.activeRuntime) return
      if (list === null) return
      const archived = normalizeThreadList(asRecord(archivedResponse).data)
      if (sameWorkspace(requestedPath, appStore.currentWorkspacePath)) {
        threads.value = list
        archivedThreads.value = archived
      }
    } catch (error) {
      if (sameWorkspace(requestedPath, appStore.currentWorkspacePath)) {
        notify('error', translate('sidebar.projectLoadFailed'), errorMessage(error))
      }
    }
  }

  async function loadRecentProjectThreads(): Promise<void> {
    const sequence = ++projectLoadSequence
    const current = appStore.currentWorkspacePath
    const recent = activeRuntimeRecentWorkspaces()
    const paths = appStore.orderWorkspacePaths(appStore.activeRuntime, uniqueWorkspacePaths(current, recent), recent)
      .filter((path) => !sameWorkspace(path, current))
    loadingProjects.value = paths
    await mapWithConcurrency(paths, 3, (path) => loadProjectThreads(path))
    if (sequence === projectLoadSequence) {
      loadingProjects.value = loadingProjects.value.filter((loadingPath) =>
        !paths.some((path) => sameWorkspace(path, loadingPath)),
      )
    }
  }

  async function reloadProject(path: string): Promise<void> {
    if (!path) return
    if (sameWorkspace(path, appStore.currentWorkspacePath)) {
      try {
        await loadThreads()
      } catch (error) {
        notify('error', translate('sidebar.projectLoadFailed'), errorMessage(error))
      }
      return
    }
    await loadProjectThreads(path, true)
  }

  async function loadProjectThreads(path: string, notifyOnError = false): Promise<ThreadSummary[] | null> {
    if (!path) return null
    const key = workspaceKey(path)
    const inFlight = projectLoadPromises.get(key)
    if (inFlight) {
      const result = await inFlight
      if (result === null && notifyOnError) notifyProjectLoadError(path)
      return result
    }

    const request = (async (): Promise<ThreadSummary[] | null> => {
      loadingProjects.value = loadingProjects.value.some((item) => sameWorkspace(item, path))
        ? loadingProjects.value
        : [...loadingProjects.value, path]
      const nextErrors = { ...projectErrors.value }
      delete nextErrors[key]
      delete nextErrors[path]
      projectErrors.value = nextErrors
      try {
        const response = await backend.ListWorkspaceThreads(path, '')
        const list = normalizeThreadList(asRecord(response).data)
        setProjectThreads(path, list)
        return list
      } catch (error) {
        const message = errorMessage(error)
        projectErrors.value = { ...projectErrors.value, [path]: message }
        if (notifyOnError) notifyProjectLoadError(path, message)
        return null
      } finally {
        loadingProjects.value = loadingProjects.value.filter((item) => !sameWorkspace(item, path))
      }
    })()
    projectLoadPromises.set(key, request)
    try {
      return await request
    } finally {
      if (projectLoadPromises.get(key) === request) projectLoadPromises.delete(key)
    }
  }

  function notifyProjectLoadError(path: string, detail = ''): void {
    const message = detail
      || projectErrors.value[path]
      || Object.entries(projectErrors.value).find(([projectPath]) => sameWorkspace(projectPath, path))?.[1]
      || translate('notifications.taskOpenFailed')
    notify('error', translate('sidebar.projectLoadFailed'), message, {
      label: translate('sidebar.retryProject'),
      onClick: () => reloadProject(path),
    })
  }

  async function loadModels(): Promise<void> {
    const requestedRuntime = appStore.activeRuntime
    if (requestedRuntime === 'gemini' || requestedRuntime === 'opencode') {
      let provider = appStore.agentProviders.find((item) => item.kind === requestedRuntime)
      let catalog = provider?.models ?? []
      let nativeActiveProvider = ''
      try {
        const nativeCatalog = await backend.ReadExternalRuntimeCatalog(requestedRuntime, appStore.currentWorkspacePath || '')
        if (requestedRuntime !== appStore.activeRuntime) return
        if (nativeCatalog.models?.length) catalog = nativeCatalog.models
        nativeActiveProvider = nativeCatalog.activeProvider || ''
        if (provider && nativeCatalog.models?.length) {
          const nextProviders = [...appStore.agentProviders]
          const index = nextProviders.findIndex((item) => item.kind === requestedRuntime)
          if (index >= 0) {
            nextProviders[index] = { ...nextProviders[index], models: catalog }
            appStore.agentProviders = nextProviders
            provider = nextProviders[index]
          }
        }
      } catch {
        // Bootstrap catalog remains a usable offline fallback.
      }
      if (requestedRuntime !== appStore.activeRuntime) return
      const custom = requestedRuntime === 'gemini'
        ? (appStore.settings.geminiCustomModels ?? [])
        : (appStore.settings.openCodeCustomModels ?? [])
      const merged = [...catalog]
      for (const id of custom) {
        const trimmed = id.trim()
        if (!trimmed || merged.some((item) => item.model.toLocaleLowerCase() === trimmed.toLocaleLowerCase())) continue
        merged.push({ model: trimmed, displayName: trimmed, description: translate('settings.externalCustomModel'), isDefault: false, contextWindow: 0 } as typeof catalog[number])
      }
      // Keep the Codex catalog in appStore.models. External catalogs are owned
      // by their AgentProvider entry and are consumed by the external composer
      // branch, preventing OpenCode/Gemini models from leaking into Codex UI.
      const configured = (requestedRuntime === 'gemini' ? appStore.settings.geminiModel : appStore.settings.openCodeModel).trim()
      const configuredCustom = custom.some((item) => item.toLocaleLowerCase() === configured.toLocaleLowerCase())
      const selected = merged.find((item) => item.model.toLocaleLowerCase() === configured.toLocaleLowerCase())
        ?? merged.find((item) => item.isDefault)
        ?? merged[0]
      if (selected && !configuredCustom && !merged.some((item) => item.model.toLocaleLowerCase() === configured.toLocaleLowerCase())) {
        if (requestedRuntime === 'gemini') {
          appStore.patchSettings({ geminiModel: selected.model })
        } else {
          appStore.patchSettings({
            openCodeModel: selected.model,
            openCodeProvider: appStore.settings.openCodeProvider || selected.providerId || '',
          })
        }
      } else if (requestedRuntime === 'opencode' && !configuredCustom) {
        const modelProvider = selected?.providerId || nativeActiveProvider
        if (modelProvider && modelProvider !== appStore.settings.openCodeProvider) {
          appStore.patchSettings({ openCodeProvider: modelProvider })
        }
      }
      return
    }
    if (requestedRuntime !== 'codex') return
    // Codex-only: clear any leftover Claude/Gemini/Grok workbench provider.
    if (appStore.settings.modelProvider) {
      appStore.patchSettings({ modelProvider: '' })
    }

    let response: Awaited<ReturnType<typeof backend.ListModels>> | null = null
    try {
      response = await backend.ListModels()
    } catch {
      response = null
    }

    const customModels = appStore.settings.customModels ?? []
    const raw = response ? normalizeModels(asRecord(response).data) : []
    const selected = selectCodexCatalog(raw).map((model) => ({
      ...model,
      displayName: cleanModelDisplayName(model.model, model.displayName),
    }))

    const merged = [...selected]
    for (const custom of customModels) {
      const id = custom.trim()
      if (!id) continue
      if (merged.some((model) => model.model.toLocaleLowerCase() === id.toLocaleLowerCase())) continue
      merged.push(stubCodexModel(id, false))
    }
    if (!merged.length) {
      for (const [index, id] of FALLBACK_CODEX_MODELS.entries()) {
        merged.push(stubCodexModel(id, index === 0))
      }
    }
    if (merged.some((model) => model.model.toLocaleLowerCase() === DEFAULT_CODEX_MODEL)) {
      for (const model of merged) {
        model.isDefault = model.model.toLocaleLowerCase() === DEFAULT_CODEX_MODEL
      }
    }
    appStore.models = merged

    const configuredModel = appStore.settings.model.trim()
    const configuredCatalogModel = appStore.models.find(
      (model) => model.model.toLocaleLowerCase() === configuredModel.toLocaleLowerCase(),
    )
    const configuredInCatalog = Boolean(configuredCatalogModel)
    const configuredCustom = customModels.some((model) => model.toLocaleLowerCase() === configuredModel.toLocaleLowerCase())
    if (configuredModel && !configuredInCatalog && configuredCustom) return

    const preferred = configuredCatalogModel
      ?? appStore.models.find((model) => model.isDefault)
      ?? appStore.models[0]
    if (!preferred) return

    const supported = preferred.supportedReasoningEfforts.length
      ? preferred.supportedReasoningEfforts
      : DEFAULT_CODEX_REASONING.map((option) => ({ effort: option.effort, description: option.description }))
    const effortSupported = supported.some((option: { effort: string }) => option.effort === appStore.settings.effort)
    const next = {
      model: preferred.model,
      effort: effortSupported
        ? appStore.settings.effort
        : preferred.defaultReasoningEffort || supported[0]?.effort || 'high',
      serviceTier: preferred.serviceTiers.some((tier: { id: string }) => tier.id === appStore.settings.serviceTier)
        ? appStore.settings.serviceTier
        : preferred.defaultServiceTier,
      modelProvider: '',
    }
    if (
      next.model === appStore.settings.model
      && next.effort === appStore.settings.effort
      && next.serviceTier === appStore.settings.serviceTier
      && !appStore.settings.modelProvider
    ) return
    appStore.patchSettings(next)
  }

  function stubCodexModel(id: string, isDefault: boolean): import('../types/codex').ModelOption {
    return {
      id,
      model: id,
      displayName: cleanModelDisplayName(id, id),
      description: 'Codex model',
      isDefault,
      defaultReasoningEffort: /sol$/i.test(id) ? 'low' : 'medium',
      defaultServiceTier: '',
      serviceTiers: [],
      supportsPersonality: false,
      supportedReasoningEfforts: DEFAULT_CODEX_REASONING.map((option) => ({
        effort: option.effort,
        description: option.description,
      })),
    }
  }

  async function loadModelProviders(): Promise<void> {
    try {
      const response = await backend.ListModelProviders()
      const listed = normalizeModelProviders(asRecord(response).data)
        .filter((provider) => provider.kind === 'codex')
      appStore.modelProviders = listed.length ? listed : buildRuntimeProviders()
    } catch {
      appStore.modelProviders = buildRuntimeProviders()
    }
  }

  async function createThread(activate = true): Promise<ThreadSummary> {
    const response = await backend.CreateThread()
    const responseRecord = asRecord(response)
    const thread = normalizeRuntimeThread(responseRecord.thread, responseRecord)
    if (!thread) throw new Error(translate('notifications.newTaskFailed'))
    if (activate) setActiveThread(thread, [])
    setThreadMetrics(thread.id, [])
    rememberLoadedThread(thread.id)
    addOrUpdateThread(thread)
    return thread
  }

  async function newThread(): Promise<ThreadSummary | null> {
    if (workspaceStore.switchingWorkspace || !isReady.value || !appStore.currentWorkspacePath) return null
    const currentDraft = activeThread.value
    if (
      currentDraft?.id.startsWith('pending-thread-')
      && sameWorkspace(currentDraft.cwd, appStore.currentWorkspacePath)
      && !(itemsByThread.value[currentDraft.id] ?? []).length
      && !(queuedMessagesByThread.value[currentDraft.id] ?? []).length
    ) {
      return currentDraft
    }
    // Drop unused empty drafts so "New task" stays instant and the sidebar stays clean.
    discardEmptyPendingThreads()
    createThreadSequence += 1
    creatingThread.value = false
    const now = Math.floor(Date.now() / 1000)
    const pendingID = `pending-thread-${Date.now()}-${createThreadSequence}`
    const externalProvider = appStore.isGeminiMode ? '__gemini__' : appStore.isOpenCodeMode ? '__opencode__' : appStore.settings.modelProvider
    const externalModel = appStore.isGeminiMode
      ? (appStore.settings.geminiModel || 'gemini-2.5-pro')
      : appStore.isOpenCodeMode
        ? (appStore.settings.openCodeModel || 'anthropic/claude-sonnet-4-6')
        : appStore.settings.model
    const externalEffort = appStore.isGeminiMode
      ? (appStore.settings.geminiEffort || 'auto')
      : appStore.isOpenCodeMode
        ? (appStore.settings.openCodeEffort || 'high')
        : appStore.settings.effort
    const externalRuntime = appStore.isGeminiMode || appStore.isOpenCodeMode
    const optimistic: ThreadSummary = {
      id: pendingID,
      name: translate('sidebar.newTask'),
      preview: '',
      cwd: appStore.currentWorkspacePath,
      createdAt: now,
      updatedAt: now,
      status: 'idle',
      cliVersion: '',
      model: externalModel,
      modelProvider: externalProvider,
      effort: externalEffort,
      collaborationMode: externalRuntime ? 'default' : appStore.settings.collaborationMode,
      workMode: externalRuntime ? 'code' : (appStore.settings.workMode || 'code'),
    }
    // Show the empty composer immediately. Real Codex/external session is created
    // on the first send via the existing pending-thread drain path.
    setActiveThread(optimistic, [])
    setThreadMetrics(pendingID, [])
    rememberLoadedThread(pendingID)
    rememberProjectThread(appStore.currentWorkspacePath, pendingID)
    addOrUpdateThread(optimistic)
    return optimistic
  }

  async function newThreadInProject(path: string): Promise<ThreadSummary | null> {
    if (!path) return null
    if (!sameWorkspace(path, appStore.currentWorkspacePath)) {
      await switchProject(path)
    }
    if (!sameWorkspace(path, appStore.currentWorkspacePath)) return null
    if (!isReady.value) {
      const connected = await connect(path)
      if (!connected) return null
    }
    return newThread()
  }

  function isThreadPinned(threadID: string): boolean {
    return pinnedThreadIds.value.includes(threadID)
  }

  function toggleThreadPin(threadID: string): void {
    if (!threadID) return
    const next = isThreadPinned(threadID)
      ? pinnedThreadIds.value.filter((id) => id !== threadID)
      : [threadID, ...pinnedThreadIds.value.filter((id) => id !== threadID)]
    pinnedThreadIds.value = next
    persistPinnedThreadIds(next)
  }

  function sortThreadsByPin(list: ThreadSummary[]): ThreadSummary[] {
    const pinned = new Set(pinnedThreadIds.value)
    return [...list].sort((a, b) => {
      const ap = pinned.has(a.id) ? 1 : 0
      const bp = pinned.has(b.id) ? 1 : 0
      if (ap !== bp) return bp - ap
      return (b.updatedAt || 0) - (a.updatedAt || 0)
    })
  }

  function discardEmptyPendingThreads(): void {
    const pendingIDs = threads.value
      .filter((thread) => thread.id.startsWith('pending-thread-'))
      .map((thread) => thread.id)
    if (!pendingIDs.length) return
    for (const pendingID of pendingIDs) {
      const hasItems = (itemsByThread.value[pendingID] ?? []).length > 0
      const hasQueue = (queuedMessagesByThread.value[pendingID] ?? []).length > 0
      if (hasItems || hasQueue) continue
      loadedThreadIDs.delete(pendingID)
      const nextItems = { ...itemsByThread.value }
      delete nextItems[pendingID]
      itemsByThread.value = nextItems
      clearThreadQueue(pendingID)
    }
    threads.value = threads.value.filter((thread) => {
      if (!thread.id.startsWith('pending-thread-')) return true
      const hasItems = (itemsByThread.value[thread.id] ?? []).length > 0
      const hasQueue = (queuedMessagesByThread.value[thread.id] ?? []).length > 0
      return hasItems || hasQueue
    })
    const path = appStore.currentWorkspacePath
    if (path) setProjectThreads(path, threads.value)
  }

  async function openThread(threadID: string): Promise<void> {
    if (!threadID) return
    const hasWorkspaceSelection = workspaceSelectionSequenceByThread.has(threadID)
    if (loadingSequenceByThread.has(threadID) && activeThreadId.value === threadID && !hasWorkspaceSelection) return
    workspaceSelectionSequenceByThread.delete(threadID)
    createThreadSequence += 1
    creatingThread.value = false
    const previousThread = activeThread.value
    const previousThreadID = activeThreadId.value
    const summary = findThreadSummary(threadID)
    // Bind the selection before the history request starts. The composer may be
    // used while ReadThread is in flight and must never fall back to the old thread.
    activeThreadId.value = threadID
    if (summary) {
      activeThread.value = summary
    } else if (activeThread.value?.id !== threadID) {
      activeThread.value = null
    }
    rememberProjectThread(appStore.currentWorkspacePath, threadID)
    const cachedTimeline = Object.prototype.hasOwnProperty.call(itemsByThread.value, threadID)
    const cachedHistory = historyByThread.value[threadID]
    const cachedUpdatedAt = cachedHistory?.loadedUpdatedAt ?? 0
    const cacheIsCurrent = cachedTimeline
      && (!summary?.updatedAt || !cachedUpdatedAt || summary.updatedAt <= cachedUpdatedAt)
    if (loadedThreadIDs.has(threadID) || (cacheIsCurrent && !isActiveStatus(summary?.status || activeThread.value?.status || ''))) {
      rememberLoadedThread(threadID)
      // Cache hit used to skip running-turn reconcile — switching Code/Cowork
      // (or reopening a background turn) left composer thinking the thread was idle.
      const knownFeedback = threadFeedback(threadID)
      const knownTurnID = threadTurnID(threadID)
        || liveFeedbackTurnID(knownFeedback)
        || ''
      const summaryStatus = summary?.status || activeThread.value?.status || ''
      if (knownTurnID) {
        setThreadTurn(threadID, knownTurnID)
        if (!knownFeedback) {
          setTurnFeedback(threadID, { state: 'running', message: '', turnId: knownTurnID })
        }
        if (!isActiveStatus(summaryStatus)) scheduleIdleThreadReconcile(threadID, knownTurnID)
      } else if (isActiveStatus(summaryStatus)) {
        // Soft revalidate: thread still marked active on server but local turn was wiped.
        loadedThreadIDs.delete(threadID)
      } else {
        scheduleThreadQueueDrain(threadID)
        return
      }
      if (loadedThreadIDs.has(threadID)) {
        scheduleThreadQueueDrain(threadID)
        return
      }
    }

    const sequence = ++openThreadSequence
    loadingSequenceByThread.set(threadID, sequence)
    loadingThreadId.value = threadID
    try {
      await waitForSessionPreferences(threadID)
      const response = await backend.ReadThread(threadID)
      const rawThread = asRecord(asRecord(response).thread)
      let thread = normalizeRuntimeThread(rawThread, response)
      if (!thread) throw new Error(translate('notifications.taskOpenFailed'))
      // A newer read for this same thread owns the timeline. Letting an older
      // response write here can erase a user row or live delta added meanwhile.
      if (loadingSequenceByThread.get(threadID) !== sequence) return
      const latestPreferences = activeThread.value?.id === thread.id
        ? activeThread.value
        : (findThreadSummary(thread.id) ?? summary)
      if (latestPreferences) {
        thread = {
          ...thread,
          model: latestPreferences.model || thread.model,
          modelProvider: latestPreferences.modelProvider || thread.modelProvider,
          effort: latestPreferences.effort || thread.effort,
          collaborationMode: latestPreferences.collaborationMode || thread.collaborationMode,
        }
      }
      let runningTurnID = ''
      // Persisted turns can retain inProgress after Codex was interrupted or the
      // app exited. Only restore one as live when thread/read also confirms the
      // thread is currently active; otherwise it would block this queue forever.
      if (isActiveStatus(thread.status)) runningTurnID = activeTurnIDFromSnapshot(rawThread)
      const snapshotItems = timelineFromTurns(rawThread.turns)
      const cachedItems = itemsByThread.value[thread.id] ?? itemsByThread.value[threadID] ?? []
      const currentHistory = historyByThread.value[thread.id] ?? historyByThread.value[threadID]
      const responsePage = asRecord(response)
      const keepLoadedPrefix = Boolean(
        currentHistory
        && currentHistory.start < (Number(responsePage.historyStart) || 0)
        && (Number(responsePage.historyTotal) || 0) >= currentHistory.total,
      )
      const split = keepLoadedPrefix
        ? splitCodexHistoryPrefix(snapshotItems, cachedItems)
        : { prefix: [] as TimelineItem[], current: cachedItems }
      const knownTurnID = threadTurnID(thread.id) || threadTurnID(threadID)
      const liveTurnID = runningTurnID || (knownTurnID && !completedTurns.has(knownTurnID) ? knownTurnID : '')
      const preserveInFlightItems = Boolean(
        liveTurnID
        || pendingThreadSubmission(thread.id)
        || pendingThreadSubmission(threadID)
        || isThreadSubmitting(thread.id)
        || isThreadSubmitting(threadID)
        || (queuedMessagesByThread.value[thread.id] ?? []).length
        || (queuedMessagesByThread.value[threadID] ?? []).length
      )
      const mergedItems = preserveInFlightItems && split.current.length
        ? mergeThreadSnapshotWithLive(snapshotItems, split.current, liveTurnID)
        : snapshotItems
      const items = split.prefix.length ? [...split.prefix, ...mergedItems] : mergedItems
      itemsByThread.value = { ...itemsByThread.value, [thread.id]: items }
      setThreadHistoryState(thread.id, response)
      if (split.prefix.length && currentHistory) {
        patchThreadHistoryState(thread.id, {
          start: currentHistory.start,
          turnOffset: currentHistory.turnOffset,
          hasEarlier: currentHistory.hasEarlier,
        })
      }
      setThreadMetrics(thread.id, rawThread.turns, split.prefix.length > 0)
      syncThreadContextWindow(thread.id, rawThread)
      rememberLoadedThread(thread.id)
      if (threadID !== thread.id) {
        loadedThreadIDs.delete(threadID)
        removeThreadHistoryState(threadID)
      }
      if (sequence === openThreadSequence && (activeThreadId.value === threadID || activeThreadId.value === thread.id)) {
        activeThread.value = thread
        // Keep activeThreadId aligned with the remapped session id from the backend.
        if (thread.id && thread.id !== activeThreadId.value) {
          migrateQueueThreadKey(activeThreadId.value, thread.id)
          activeThreadId.value = thread.id
        }
        const openedID = activeThreadId.value
        const previousFeedback = threadFeedback(openedID) || threadFeedback(threadID)
        const previousTurnID = threadTurnID(openedID)
          || threadTurnID(threadID)
          || liveFeedbackTurnID(previousFeedback)
          || ''
        if (runningTurnID) {
          setThreadTurn(openedID, runningTurnID)
          setTurnFeedback(openedID, { state: 'running', message: '', turnId: runningTurnID })
        } else if (previousTurnID && !completedTurns.has(previousTurnID)) {
          // ReadThread snapshots can lag live turn events (especially after a
          // quick switch away/back). Do not wipe the local turn or the queue will drain.
          setThreadTurn(openedID, previousTurnID)
          if (!threadFeedback(openedID)) {
            setTurnFeedback(openedID, { state: 'running', message: '', turnId: previousTurnID })
          }
          if (!isActiveStatus(thread.status)) scheduleIdleThreadReconcile(openedID, previousTurnID)
        } else {
          setThreadTurn(openedID, '')
        }
      }
      addOrUpdateThread(thread)
      scheduleThreadQueueDrain(thread.id)
    } catch (error) {
      if (sequence !== openThreadSequence) return
      // Keep a message admitted during loading attached to this thread and visible
      // in its queue. A retry can hydrate it; restoring the old thread made it look
      // as if the message vanished and risked a later send using the wrong owner.
      if ((queuedMessagesByThread.value[threadID] ?? []).length === 0) {
        activeThread.value = previousThread
        activeThreadId.value = previousThreadID
      }
      notify('error', translate('notifications.taskOpenFailed'), errorMessage(error), {
        label: translate('common.retry'),
        onClick: () => openThread(threadID),
      })
    } finally {
      if (loadingSequenceByThread.get(threadID) === sequence) {
        loadingSequenceByThread.delete(threadID)
        // ReadThread is hydration, not ownership of the queued prompt. Once its
        // barrier is gone, let the normal send path resume or surface a send error.
        scheduleThreadQueueDrain(threadID)
      }
      if (sequence === openThreadSequence) loadingThreadId.value = ''
    }
  }

  async function recoverActiveThread(): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID) return

    // Invalidate only this conversation's hydration owners. The abandoned Wails
    // promise may still resolve later, but its sequence can no longer write state.
    openThreadSequence += 1
    for (const id of [...loadingSequenceByThread.keys()]) {
      if (id === threadID || sameThreadSession(id, threadID)) loadingSequenceByThread.delete(id)
    }
    for (const id of [...workspaceSelectionSequenceByThread.keys()]) {
      if (id === threadID || sameThreadSession(id, threadID)) workspaceSelectionSequenceByThread.delete(id)
    }
    if (loadingThreadId.value === threadID || sameThreadSession(loadingThreadId.value, threadID)) {
      loadingThreadId.value = ''
    }
    // A running thread's in-memory timeline is newer than thread/resume and is
    // enough to leave the loading skeleton immediately. Only cache misses need
    // another backend history read.
    if ((itemsByThread.value[threadID] ?? []).length > 0 && threadIsBusy(threadID)) {
      rememberLoadedThread(threadID)
    }
    await openThread(threadID)
  }

  async function openProjectThread(path: string, threadID: string): Promise<void> {
    if (!path || !threadID) return
    if (
      sameWorkspace(path, appStore.currentWorkspacePath)
      && !workspaceStore.switchingWorkspace
    ) {
      await openThread(threadID)
      return
    }

    const previousThread = activeThread.value
    const previousThreadID = activeThreadId.value
    const summary = findThreadSummary(threadID)
    const sequence = ++openThreadSequence
    activeThreadId.value = threadID
    activeThread.value = summary ?? null
    rememberProjectThread(path, threadID)
    workspaceSelectionSequenceByThread.set(threadID, sequence)
    loadingThreadId.value = threadID
    let handedOff = false
    try {
      const switched = await workspaceStore.useWorkspace(path)
      if (
        !switched
        || openThreadSequence !== sequence
        || activeThreadId.value !== threadID
      ) return
      handedOff = true
      await activateProject(path, threadID)
    } finally {
      if (workspaceSelectionSequenceByThread.get(threadID) === sequence) {
        workspaceSelectionSequenceByThread.delete(threadID)
        if (loadingThreadId.value === threadID) loadingThreadId.value = ''
        if (
          !handedOff
          && activeThreadId.value === threadID
          && !(queuedMessagesByThread.value[threadID] ?? []).length
        ) {
          activeThread.value = previousThread
          activeThreadId.value = previousThreadID
        }
      }
    }
  }

  async function switchProject(path: string, preferredThreadID = ''): Promise<void> {
    if (!path) return
    const currentPath = appStore.currentWorkspacePath
    if (activeThreadId.value && currentPath) rememberProjectThread(currentPath, activeThreadId.value)
    const knownTarget = preferredThreadID
      || projectThreadForPath(path)
      || projectThreadsForPath(path)?.[0]?.id
      || ''
    if (knownTarget) {
      await openProjectThread(path, knownTarget)
      return
    }
    const switched = await workspaceStore.useWorkspace(path)
    if (!switched) return

    // No target is known yet. Detach the previous project's thread before the
    // project listing await, so a send can never be admitted to the old owner.
    activeThread.value = null
    activeThreadId.value = ''
    await activateProject(path, preferredThreadID)
  }

  async function selectProject(): Promise<void> {
    const currentPath = appStore.currentWorkspacePath
    if (activeThreadId.value && currentPath) rememberProjectThread(currentPath, activeThreadId.value)
    const path = await workspaceStore.selectWorkspace()
    if (!path) return
    await activateProject(path)
  }

  async function activateProject(path: string, preferredThreadID = ''): Promise<void> {
    const requestedRuntime = appStore.activeRuntime
    const cached = projectThreadsForPath(path)
    if (cached) {
      threads.value = [...cached]
    } else {
      await reloadProject(path)
      threads.value = [...(projectThreadsForPath(path) ?? [])]
    }
    const locallySelectedThreadID = activeThread.value?.cwd
      && sameWorkspace(activeThread.value.cwd, path)
      ? activeThread.value.id
      : ''
    const targetThreadID = preferredThreadID
      || locallySelectedThreadID
      || projectThreadForPath(path)
      || threads.value[0]?.id
      || ''
    if (targetThreadID) {
      await openThread(targetThreadID)
    } else {
      activeThread.value = null
      activeThreadId.value = ''
    }
    // Do not let a background thread/list compete with the history read for the
    // next folder click. Refresh only after the selected conversation is visible,
    // and discard the task when the user has already moved to another project.
    trackedTimeout(() => {
      if (
        appStore.activeRuntime === requestedRuntime
        && sameWorkspace(path, appStore.currentWorkspacePath)
      ) void loadThreads(path).catch(() => undefined)
    }, 600)
  }

  function findThreadSummary(threadID: string): ThreadSummary | undefined {
    return threads.value.find((thread) => thread.id === threadID)
      ?? Object.values(projectThreads.value).flat().find((thread) => thread.id === threadID)
  }

  function projectThreadsForPath(path: string): ThreadSummary[] | undefined {
    const entry = Object.entries(projectThreads.value).find(([projectPath]) => sameWorkspace(projectPath, path))
    return entry?.[1]
  }

  function rememberProjectThread(path: string, threadID: string): void {
    if (!path || !threadID) return
    lastThreadByWorkspace.value = { ...lastThreadByWorkspace.value, [workspaceKey(path)]: threadID }
    try {
      localStorage.setItem('nice-codex.lastThreads', JSON.stringify(lastThreadByWorkspace.value))
    } catch {
      // Local persistence is best effort.
    }
  }

  function projectThreadForPath(path: string): string {
    return lastThreadByWorkspace.value[workspaceKey(path)] ?? ''
  }

  async function forkThread(threadID: string): Promise<void> {
    const id = threadID.trim()
    if (!id || threadMutation.value) return
    if (id.startsWith('pending-thread-')) {
      notify('warning', translate('threadActions.forkFailed'), translate('notifications.taskOpenFailed'))
      return
    }
    threadMutation.value = 'fork'
    try {
      const response = await backend.ForkThread(id)
      const rawThread = asRecord(asRecord(response).thread)
      const thread = normalizeRuntimeThread(rawThread, response)
      if (!thread) throw new Error(translate('notifications.taskOpenFailed'))
      const items = timelineFromTurns(rawThread.turns)
      setActiveThread(thread, items)
      setThreadMetrics(thread.id, rawThread.turns)
      syncThreadContextWindow(thread.id, rawThread)
      rememberLoadedThread(thread.id)
      addOrUpdateThread(thread)
      notify('success', translate('threadActions.forked'), thread.name)
    } catch (error) {
      notify('error', translate('threadActions.forkFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function forkActiveThread(): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value) return
    await forkThread(threadID)
  }

  async function archiveThread(threadID: string): Promise<void> {
    const id = threadID.trim()
    if (!id || threadMutation.value) return
    if (id.startsWith('pending-thread-')) {
      discardLocalThread(id)
      notify('success', translate('threadActions.archived'), translate('threadActions.archivedHint'))
      return
    }
    threadMutation.value = 'archive'
    try {
      await backend.ArchiveThread(id)
      if (activeThreadId.value === id) {
        activeThread.value = null
        activeThreadId.value = ''
      }
      releasePendingThreadSubmissions(id)
      clearThreadQueue(id)
      await loadThreads()
      notify('success', translate('threadActions.archived'), translate('threadActions.archivedHint'))
    } catch (error) {
      notify('error', translate('threadActions.archiveFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function archiveActiveThread(): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value) return
    await archiveThread(threadID)
  }

  async function compactActiveThread(): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value || threadMutation.value) return
    threadMutation.value = 'compact'
    try {
      await backend.CompactThread(threadID)
      if (activeThreadUsesExternalProvider.value) {
        loadedThreadIDs.delete(threadID)
        await openThread(threadID)
      }
      notify('info', translate('threadActions.compacting'), translate('threadActions.compactingRuntimeHint', {
        runtime: runtimeNameForThread(threadID),
      }))
    } catch (error) {
      notify('error', translate('threadActions.compactFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function renameThread(threadID: string, name?: string): Promise<boolean> {
    const id = threadID.trim()
    if (!id || threadMutation.value) return false
    const current = findThreadSummary(id) || (activeThread.value?.id === id ? activeThread.value : null)
    let nextName = name
    if (nextName === undefined) {
      const prompted = await dialogStore.prompt({
        title: translate('threadActions.rename'),
        description: translate('threadActions.renamePrompt'),
        defaultValue: current?.name || '',
        placeholder: translate('threadActions.renamePrompt'),
        confirmLabel: translate('common.save'),
        maxlength: 120,
      })
      nextName = prompted ?? ''
    }
    nextName = nextName.trim()
    if (!nextName || nextName === current?.name) return false

    if (id.startsWith('pending-thread-')) {
      const local = current ? { ...current, name: nextName } : null
      if (local) {
        if (activeThreadId.value === id) activeThread.value = local
        addOrUpdateThread(local)
      }
      notify('success', translate('threadActions.renamed'), nextName)
      return true
    }

    threadMutation.value = 'rename'
    try {
      const response = await backend.SetThreadName(id, nextName)
      const thread = normalizeRuntimeThread(asRecord(asRecord(response).thread), response)
        ?? (current ? { ...current, name: nextName } : null)
      if (thread) {
        if (activeThreadId.value === id) activeThread.value = thread
        addOrUpdateThread(thread)
      }
      notify('success', translate('threadActions.renamed'), nextName)
      return true
    } catch (error) {
      notify('error', translate('threadActions.renameFailed'), errorMessage(error))
      return false
    } finally {
      threadMutation.value = ''
    }
  }

  async function renameActiveThread(name?: string): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value) return
    await renameThread(threadID, name)
  }

  async function deleteThread(threadID: string): Promise<void> {
    const id = threadID.trim()
    if (!id || threadMutation.value) return
    const confirmed = await dialogStore.confirm({
      title: translate('threadActions.delete'),
      description: translate('threadActions.deleteConfirm'),
      confirmLabel: translate('common.delete'),
      destructive: true,
    })
    if (!confirmed) return
    if (id.startsWith('pending-thread-')) {
      discardLocalThread(id)
      notify('success', translate('threadActions.deleted'), translate('threadActions.deletedHint'))
      return
    }
    threadMutation.value = 'delete'
    try {
      await backend.DeleteThread(id)
      discardLocalThread(id)
      await loadThreads()
      notify('success', translate('threadActions.deleted'), translate('threadActions.deletedHint'))
    } catch (error) {
      notify('error', translate('threadActions.deleteFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function deleteActiveThread(): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value) return
    await deleteThread(threadID)
  }

  function discardLocalThread(threadID: string): void {
    if (!threadID) return
    threads.value = threads.value.filter((thread) => thread.id !== threadID)
    archivedThreads.value = archivedThreads.value.filter((thread) => thread.id !== threadID)
    const nextProjects = { ...projectThreads.value }
    for (const [path, projectItems] of Object.entries(nextProjects)) {
      nextProjects[path] = projectItems.filter((thread) => thread.id !== threadID)
    }
    projectThreads.value = nextProjects

    const nextItems = { ...itemsByThread.value }
    delete nextItems[threadID]
    itemsByThread.value = nextItems
    removeThreadHistoryState(threadID)

    releasePendingThreadSubmissions(threadID)
    clearThreadQueue(threadID)
    clearTurnFeedback(threadID)
    setThreadTurn(threadID, '')

    const nextMetrics = { ...turnMetricsByThread.value }
    delete nextMetrics[threadID]
    turnMetricsByThread.value = nextMetrics

    const nextToken = { ...tokenUsageByThread.value }
    delete nextToken[threadID]
    tokenUsageByThread.value = nextToken

    loadedThreadIDs.delete(threadID)
    latestStartedTurnByThread.delete(threadID)
    clearThreadAliases(threadID)
    if (pinnedThreadIds.value.includes(threadID)) {
      const nextPinned = pinnedThreadIds.value.filter((id) => id !== threadID)
      pinnedThreadIds.value = nextPinned
      persistPinnedThreadIds(nextPinned)
    }
    if (activeThreadId.value === threadID) {
      activeThread.value = null
      activeThreadId.value = ''
    }
  }

  async function unarchiveThread(threadID: string): Promise<void> {
    const id = threadID.trim()
    if (!id || threadMutation.value) return
    threadMutation.value = 'unarchive'
    try {
      const response = await backend.UnarchiveThread(id)
      await loadThreads()
      const thread = normalizeRuntimeThread(asRecord(asRecord(response).thread), response)
      if (thread) {
        await openThread(thread.id)
        notify('success', translate('threadActions.unarchived'), thread.name)
      } else {
        notify('success', translate('threadActions.unarchived'), translate('threadActions.unarchivedHint'))
      }
    } catch (error) {
      notify('error', translate('threadActions.unarchiveFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function startReview(options: {
    targetType?: 'uncommittedChanges' | 'baseBranch' | 'custom'
    branch?: string
    instructions?: string
    delivery?: 'inline' | 'detached'
  } = {}): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || threadID.startsWith('pending-thread-') || activeThreadBusy.value || threadMutation.value) {
      notify('warning', translate('threadActions.reviewNeedThread'), translate('threadActions.reviewNeedThreadHint'))
      return
    }
    threadMutation.value = 'review'
    try {
      const response = await backend.StartReview({
        threadId: threadID,
        targetType: options.targetType || 'uncommittedChanges',
        branch: options.branch || '',
        instructions: options.instructions || '',
        delivery: options.delivery || 'inline',
      })
      const payload = asRecord(response)
      const reviewThread = normalizeRuntimeThread(payload.thread, response)
      const reviewThreadID = asString(payload.reviewThreadId) || reviewThread?.id || threadID
      if (options.delivery === 'detached' && reviewThreadID && reviewThreadID !== threadID) {
        addOrUpdateThread(reviewThread ?? {
          id: reviewThreadID,
          name: translate('threadActions.reviewThreadName'),
          preview: '',
          cwd: appStore.currentWorkspacePath,
          createdAt: Math.floor(Date.now() / 1000),
          updatedAt: Math.floor(Date.now() / 1000),
          status: 'active',
          cliVersion: '',
          model: activeThread.value?.model || appStore.settings.model,
          modelProvider: activeThread.value?.modelProvider || '',
        })
        await openThread(reviewThreadID)
      } else {
        const turn = asRecord(payload.turn)
        const turnID = asString(turn.id)
        if (turnID) setThreadTurn(threadID, turnID)
      }
      notify('info', translate('threadActions.reviewStarted'), translate('threadActions.reviewStartedHint'))
      void workspaceStore.refreshWorkspace().catch(() => undefined)
    } catch (error) {
      notify('error', translate('threadActions.reviewFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function rollbackToTurn(turnID: string, mode: 'single' | 'fromHere' = 'fromHere'): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || !turnID || activeThreadBusy.value || threadMutation.value) return
    const turnIDs = [...new Set(activeItems.value.map((item) => item.turnId).filter(Boolean))]
    const turnIndex = turnIDs.indexOf(turnID)
    if (turnIndex < 0) return
    const isLast = turnIndex === turnIDs.length - 1
    if (mode === 'single' && !isLast) {
      notify('warning', translate('timeline.rollbackFailed'), translate('timeline.rollbackSingleOnlyLast'))
      return
    }
    const numTurns = mode === 'single' ? 1 : turnIDs.length - turnIndex
    if (numTurns < 1) return
    threadMutation.value = 'rollback'
    try {
      const response = await backend.RollbackThread(threadID, numTurns)
      const rawThread = asRecord(asRecord(response).thread)
      const thread = normalizeRuntimeThread(rawThread, response)
      if (!thread) throw new Error(translate('notifications.taskOpenFailed'))
      setActiveThread(thread, timelineFromTurns(rawThread.turns))
      setThreadMetrics(thread.id, rawThread.turns)
      syncThreadContextWindow(thread.id, rawThread)
      workspaceStore.clearDiff()
      notify('warning', translate('timeline.rolledBack'), translate('timeline.rollbackFilesWarning'))
    } catch (error) {
      notify('error', translate('timeline.rollbackFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function sendMessage(text: string, images: string[] = []): Promise<boolean> {
    return enqueueMessage(text, '', images)
  }

  async function retryMessage(itemID: string, text: string): Promise<boolean> {
    const item = activeItems.value.find((candidate) => candidate.id === itemID)
    if (!item?.failed) return false
    const queuedMessage = activeQueuedMessages.value.find((message) => message.localItemId === itemID)
    if (queuedMessage) {
      if (queuedMessage.state !== 'failed') return false
      patchQueuedMessage(activeThreadId.value, queuedMessage.id, { state: 'queued', error: '' })
      scheduleThreadQueueDrain(activeThreadId.value)
      return true
    }
    return enqueueMessage(text, itemID, localAttachmentSources(item.attachments))
  }

  async function retryLastMessage(): Promise<boolean> {
    const item = [...activeItems.value].reverse().find((candidate) => candidate.type === 'userMessage')
    if (!item) return false
    if (item.failed) return retryMessage(item.id, item.text)
    return enqueueMessage(item.text, '', localAttachmentSources(item.attachments))
  }

  function enqueueMessage(text: string, retryItemID = '', images: string[] = []): boolean {
    const message = text.trim()
    const imagePaths = uniqueImagePaths(images).slice(0, 4)
    if ((!message && !imagePaths.length) || !isReady.value || creatingThread.value) return false
    const now = Date.now()
    const sequence = ++queuedMessageSequence
    const selectedThreadID = activeThreadId.value
    const selectedThread = activeThread.value?.id === selectedThreadID ? activeThread.value : null
    const threadID = selectedThreadID || `pending-thread-${now}-${sequence}`
    const workspace = selectedThread?.cwd || findThreadSummary(threadID)?.cwd || appStore.currentWorkspacePath
    if (!threadID || !workspace) return false
    const waitInQueue = threadIsBusy(threadID) || (queuedMessagesByThread.value[threadID]?.length ?? 0) > 0
    if (!selectedThreadID) activeThreadId.value = threadID
    // User continued chatting — dismiss implement prompt (official dismisses on follow-up).
    if (planImplementPrompt.value?.threadId === threadID) planImplementPrompt.value = null

    const feedback = threadFeedback(threadID)
    const pendingSubmission = pendingThreadSubmission(threadID)?.submission
    const blockingTurnId = threadTurnID(threadID)
      || pendingSubmission?.turnId
      || pendingSubmission?.blockerId
      || liveFeedbackTurnID(feedback)
    const queuedMessage: QueuedMessage = {
      id: `queued-${now}-${sequence}`,
      threadId: threadID,
      workspace,
      text: message,
      images: imagePaths,
      createdAt: now,
      localItemId: retryItemID || `local-${now}-${sequence}`,
      retryItemId: retryItemID,
      blockedByTurnId: blockingTurnId && !completedTurns.has(blockingTurnId) ? blockingTurnId : undefined,
      state: 'queued',
      error: '',
    }
    queuedMessagesByThread.value = {
      ...queuedMessagesByThread.value,
      [threadID]: [...(queuedMessagesByThread.value[threadID] ?? []), queuedMessage],
    }
    // Start an idle send in the same task so the transient `queued` row cannot
    // flash in the UI. Busy threads keep the row queued until their terminal
    // event releases it.
    if (waitInQueue) scheduleThreadQueueDrain(threadID)
    else void drainThreadQueue(threadID)
    return true
  }

  async function drainThreadQueue(threadID: string): Promise<void> {
    if (!threadID || !isReady.value || threadIsBusy(threadID)) return
    // Failed rows remain visible/retryable, but they did not reach the provider
    // and must not permanently block later messages after a 403/429/500 or a
    // broken stream. A sending row is still protected by threadIsBusy above.
    const queuedMessage = queuedMessagesByThread.value[threadID]
      ?.find((message) => message.state === 'queued')
    if (!queuedMessage) return
    // A transient status/alias update must not release a follow-up before the
    // exact turn that accepted it as queued has reached a terminal event.
    if (queuedMessage.blockedByTurnId && !completedTurns.has(queuedMessage.blockedByTurnId)) return
    if (!sameWorkspace(queuedMessage.workspace, appStore.currentWorkspacePath)) return

    const submission = beginPendingThreadSubmission(threadID, queuedMessage.id)
    if (!submission) return
    const submissionStillOwnsThread = (ownerID: string): boolean => {
      if (isPendingThreadSubmission(ownerID, submission)) return true
      patchQueuedMessage(ownerID, queuedMessage.id, { state: 'queued', error: '' })
      return false
    }
    let resolvedThreadID = threadID
    let continueDraining = false
    let keepSubmissionPending = false
    let submissionAccepted = false
    let startedTurnBeforeSend = ''
    setThreadSubmitting(threadID, true)
    patchQueuedMessage(threadID, queuedMessage.id, { state: 'sending', error: '' })

    // Show the user bubble + thinking state immediately (before create/resume/send).
    const localItem = createLocalUserItem(queuedMessage.localItemId, queuedMessage.text, queuedMessage.images)
    const existingLocalItem = (itemsByThread.value[threadID] ?? []).find((item) => item.id === localItem.id)
    if (queuedMessage.retryItemId || existingLocalItem?.failed) replaceItem(threadID, localItem)
    else if (!existingLocalItem) {
      appendItem(threadID, localItem)
    }
    setTurnFeedback(threadID, { state: 'submitting', message: translate('chat.thinking'), turnId: '' })

    try {
      // Re-check after UI work — a turn/started or status event may have arrived.
      const earlyLiveTurnID = threadTurnID(threadID)
      if (earlyLiveTurnID || threadReportsActive(threadID)) {
        // Put the message back to queued and wait for turn/completed.
        const items = (itemsByThread.value[threadID] ?? []).filter((item) => item.id !== localItem.id)
        itemsByThread.value = { ...itemsByThread.value, [threadID]: items }
        patchQueuedMessage(threadID, queuedMessage.id, {
          state: 'queued',
          error: '',
          blockedByTurnId: earlyLiveTurnID || undefined,
        })
        if (earlyLiveTurnID) {
          replaceQueuedBlockingTurn(threadID, submission.blockerId, earlyLiveTurnID)
        }
        setTurnFeedback(threadID, { state: 'running', message: '', turnId: earlyLiveTurnID })
        return
      }

      let thread = activeThread.value?.id === threadID ? activeThread.value : findThreadSummary(threadID)
      if (threadID.startsWith('pending-thread-')) {
        const pendingThreadID = threadID
        const draft = activeThread.value?.id === pendingThreadID ? activeThread.value : null
        thread = await createThread(false)
        if (!submissionStillOwnsThread(pendingThreadID)) return
        resolvedThreadID = thread.id
        migratePendingThread(pendingThreadID, thread.id)
        if (!submissionStillOwnsThread(resolvedThreadID)) return
        // Keep all draft preferences on this session. Global preference persistence
        // is debounced, so CreateThread may still return the previous effort.
        if (draft) {
          const draftModel = draft.model || appStore.settings.model
          const draftEffort = draft.effort || appStore.settings.effort
          const draftCollaborationMode = resolveThreadCollaborationMode(draft)
          try {
            await updateSessionPreferences({
              sessionId: thread.id,
              model: draftModel,
              effort: draftEffort,
              collaborationMode: draftCollaborationMode,
            })
            thread = {
              ...thread,
              model: draftModel || thread.model,
              modelProvider: draft.modelProvider || thread.modelProvider,
              effort: draftEffort || thread.effort,
              collaborationMode: draftCollaborationMode,
            }
          } catch {
            // Continue with CreateThread defaults if session patch fails.
          }
          if (!submissionStillOwnsThread(resolvedThreadID)) return
        }
        if (activeThreadId.value === thread.id) {
          activeThread.value = {
            ...thread,
            model: activeThread.value?.model || thread.model,
            modelProvider: activeThread.value?.modelProvider || thread.modelProvider,
            effort: activeThread.value?.effort || thread.effort,
            collaborationMode: activeThread.value?.collaborationMode || thread.collaborationMode,
          }
          addOrUpdateThread(activeThread.value)
        }
      } else if (!thread) {
        throw new Error(translate('notifications.taskOpenFailed'))
      } else {
        const localPreferences = thread
        await waitForSessionPreferences(thread.id)
        if (!submissionStillOwnsThread(resolvedThreadID)) return
        // Always re-bind into app-server memory. After reconnect, status is often
        // still "idle" (not "notLoaded") so a status-only check used to skip resume
        // and turn/start returned 422 "thread not found".
        try {
          const response = await backend.ResumeThread(thread.id)
          if (!submissionStillOwnsThread(resolvedThreadID)) return
          const responseRecord = asRecord(response)
          const rawResumedThread = asRecord(responseRecord.thread)
          const resumed = normalizeRuntimeThread(rawResumedThread, responseRecord)
          if (resumed) {
            const latestPreferences = activeThread.value?.id === resumed.id
              ? activeThread.value
              : (findThreadSummary(resumed.id) ?? localPreferences)
            thread = {
              ...resumed,
              model: latestPreferences.model || resumed.model,
              modelProvider: latestPreferences.modelProvider || resumed.modelProvider,
              effort: latestPreferences.effort || resumed.effort,
              collaborationMode: latestPreferences.collaborationMode || resumed.collaborationMode,
            }
            if (activeThreadId.value === resumed.id) activeThread.value = thread
            addOrUpdateThread(thread)
            if (isActiveStatus(resumed.status)) {
              const resumedTurnID = activeTurnIDFromSnapshot(rawResumedThread)
              if (resumedTurnID) setThreadTurn(resolvedThreadID, resumedTurnID)
              setTurnFeedback(resolvedThreadID, {
                state: 'running',
                message: '',
                turnId: resumedTurnID,
              })
            }
          }
        } catch {
          // Backend SendMessage also auto-resumes; continue and surface errors there.
        }
        if (!submissionStillOwnsThread(resolvedThreadID)) return
      }
      if (!sameWorkspace(queuedMessage.workspace, appStore.currentWorkspacePath)) {
        patchQueuedMessage(resolvedThreadID, queuedMessage.id, { state: 'queued', error: '' })
        clearTurnFeedback(resolvedThreadID)
        return
      }

      await waitForSessionPreferences(thread.id)
      if (!submissionStillOwnsThread(resolvedThreadID)) return
      const latestPreferences = activeThread.value?.id === thread.id
        ? activeThread.value
        : findThreadSummary(thread.id)
      if (latestPreferences) {
        thread = {
          ...thread,
          model: latestPreferences.model || thread.model,
          modelProvider: latestPreferences.modelProvider || thread.modelProvider,
          effort: latestPreferences.effort || thread.effort,
          collaborationMode: latestPreferences.collaborationMode || thread.collaborationMode,
        }
      }
      // Another turn may have started while we were creating/resuming the thread.
      // ResumeThread can also reveal a server-active turn whose event was missed.
      const liveTurnID = threadTurnID(resolvedThreadID) || threadTurnID(thread.id)
      if (liveTurnID || threadReportsActive(resolvedThreadID) || threadReportsActive(thread.id)) {
        const items = (itemsByThread.value[resolvedThreadID] ?? []).filter((item) => item.id !== localItem.id)
        itemsByThread.value = { ...itemsByThread.value, [resolvedThreadID]: items }
        if (liveTurnID) {
          replaceQueuedBlockingTurn(resolvedThreadID, submission.blockerId, liveTurnID)
        }
        // Queue may have migrated from pending → real id.
        if (resolvedThreadID !== threadID) {
          removeQueuedMessageFromThread(threadID, queuedMessage.id)
          if ((queuedMessagesByThread.value[resolvedThreadID] ?? []).some((message) => message.id === queuedMessage.id)) {
            patchQueuedMessage(resolvedThreadID, queuedMessage.id, {
              state: 'queued',
              error: '',
              blockedByTurnId: liveTurnID || undefined,
            })
          } else {
            queuedMessagesByThread.value = {
              ...queuedMessagesByThread.value,
              [resolvedThreadID]: [...(queuedMessagesByThread.value[resolvedThreadID] ?? []), {
                ...queuedMessage,
                threadId: resolvedThreadID,
                state: 'queued',
                error: '',
                blockedByTurnId: liveTurnID || undefined,
              }],
            }
          }
        } else {
          patchQueuedMessage(resolvedThreadID, queuedMessage.id, {
            state: 'queued',
            error: '',
            blockedByTurnId: liveTurnID || undefined,
          })
        }
        setTurnFeedback(resolvedThreadID, { state: 'running', message: '', turnId: liveTurnID })
        return
      }

      setTurnFeedback(resolvedThreadID, { state: 'submitting', message: translate('chat.thinking'), turnId: '' })
      // Prefer this session's model; global settings are only a fallback for new threads.
      updateThreadModelIdentity(
        thread.id,
        thread.model || appStore.settings.model,
        thread.modelProvider || appStore.settings.modelProvider,
      )

      const collaborationMode = resolveThreadCollaborationMode(thread)
      pendingCollaborationModeByThread.set(thread.id, collaborationMode)
      startedTurnBeforeSend = latestStartedTurnByThread.get(thread.id)
        || latestStartedTurnByThread.get(threadID)
        || ''
      submission.previousTurnId = startedTurnBeforeSend
      submission.requestStarted = true
      const response = await backend.SendMessage({
        threadId: thread.id,
        text: queuedMessage.text,
        images: queuedMessage.images,
        // Official TUI: SubmitUserMessageWithMode — mode travels with the turn.
        collaborationMode,
      } satisfies SendMessageRequest)
      const turn = asRecord(asRecord(response).turn)
      const responseTurnID = asString(turn.id)
      if (responseTurnID) bindPendingThreadSubmission(thread.id, responseTurnID, true, submission)
      const turnID = responseTurnID || submission.turnId
      submissionAccepted = Boolean(turnID)
      const newerSubmission = pendingThreadSubmission(thread.id)
      const currentTurnID = threadTurnID(thread.id)
      const updateThreadPreview = () => {
        if (thread.preview || (activeThread.value?.id !== thread.id && !findThreadSummary(thread.id))) return
        const updated = { ...thread, name: queuedMessage.text.slice(0, 56), preview: queuedMessage.text }
        if (activeThreadId.value === thread.id) activeThread.value = updated
        addOrUpdateThread(updated)
      }
      if (newerSubmission && newerSubmission.submission !== submission) {
        // A newer dispatch owns the row; the old response must not clear it.
        if (turnID && submission.turnId === turnID) updateThreadPreview()
        return
      }
      if (!newerSubmission) {
        // turn/started may already have settled this exact dispatch. With no newer
        // owner, a successful response is still proof that its queued row is done.
        if (turnID) updateThreadPreview()
        if (turnID) removeQueuedMessageFromThread(resolvedThreadID, queuedMessage.id)
        return
      }
      if (turnID && currentTurnID && currentTurnID !== turnID && !completedTurns.has(currentTurnID)) {
        if (submission.turnId === turnID) updateThreadPreview()
        removeQueuedMessageFromThread(resolvedThreadID, queuedMessage.id)
        return
      }
      updateThreadPreview()
      if (turnID) {
        collaborationModeByTurn.set(turnID, collaborationMode)
        pendingCollaborationModeByThread.delete(thread.id)
      }
      const turnStatus = normalizeThreadStatus(turn.status)
      // Default to "running" whenever we have a turn id — only release the queue on
      // an explicit terminal status. Mis-classified statuses used to drain the next
      // queued message immediately as a parallel turn/start.
      const finished = turnID
        ? (isTerminalTurnStatus(turnStatus) || completedTurns.has(turnID))
        : false
      if (finished && turnID) rememberCompletedTurn(turnID, turnStatus)
      const running = Boolean(turnID) && !finished
      const startedAt = typeof turn.startedAt === 'number' ? turn.startedAt * 1000 : Date.now()
      const completedAt = typeof turn.completedAt === 'number' ? turn.completedAt * 1000 : null
      const durationMs = typeof turn.durationMs === 'number' ? turn.durationMs : null
      patchTurnMetrics(thread.id, turnID, { startedAt, completedAt, durationMs })
      if (turnID) setThreadTurn(thread.id, running ? turnID : '')
      if (finished) setLocalThreadStatus(thread.id, 'idle')
      if (running) {
        setLocalThreadStatus(thread.id, 'active')
        setTurnFeedback(thread.id, { state: 'running', message: translate('chat.thinking'), turnId: turnID })
      } else if (!turnID) {
        // Response omitted turn id — keep submitting until turn/started arrives.
        setTurnFeedback(thread.id, { state: 'submitting', message: translate('chat.thinking'), turnId: '' })
      } else if (isFailedStatus(turnStatus)) {
        setTurnFeedback(thread.id, {
          state: 'failed',
          message: asString(asRecord(turn.error).message, translate('notifications.turnFailedFallback')),
          turnId: turnID,
        })
      } else if (isInterruptedStatus(turnStatus)) {
        setTurnFeedback(thread.id, { state: 'interrupted', message: translate('chat.interrupted'), turnId: turnID })
      } else {
        clearTurnFeedback(thread.id)
      }
      if (turnID) {
        removeQueuedMessageFromThread(resolvedThreadID, queuedMessage.id)
      } else {
        // A successful RPC without a turn id is accepted but not yet bound. Keep
        // the sending row and dispatch lock until turn/started supplies ownership.
        keepSubmissionPending = true
      }
      // Only chain the next queued item when this turn is already finished.
      continueDraining = finished
    } catch (error) {
      const message = errorMessage(error)
      const currentSubmission = pendingThreadSubmissionOwner(submission)
      // External runtimes reject a second process-level send while the native
      // turn is still running. Treat that response as a queue admission race,
      // not as a failed user message; the terminal event of the original turn
      // will release the queue.
      if (/external provider turn is already running|Codex turn is already running/i.test(message)) {
        const items = (itemsByThread.value[resolvedThreadID] ?? []).filter((item) => item.id !== queuedMessage.localItemId)
        itemsByThread.value = { ...itemsByThread.value, [resolvedThreadID]: items }
        patchQueuedMessage(resolvedThreadID, queuedMessage.id, { state: 'queued', error: '', blockedByTurnId: undefined })
        setLocalThreadStatus(resolvedThreadID, 'active')
        setTurnFeedback(resolvedThreadID, { state: 'running', message: '', turnId: '' })
        return
      }
      // A reset/reconnect may already have re-dispatched this row. Ignore the old
      // rejection instead of marking the newer owner failed.
      if (!currentSubmission && !submission.turnId) return
      // Only a turn bound to this submission proves acceptance. A different live
      // turn must never consume this queued row.
      const liveTurnID = threadTurnID(resolvedThreadID) || threadTurnID(threadID)
      const latestStartedTurnID = latestStartedTurnByThread.get(resolvedThreadID)
        ?? latestStartedTurnByThread.get(threadID)
        ?? ''
      const acceptedDuringSend = Boolean(
        submission.requestStarted
        && latestStartedTurnID
        && latestStartedTurnID !== startedTurnBeforeSend,
      )
      if (!submission.turnId && acceptedDuringSend) {
        bindPendingThreadSubmission(resolvedThreadID, latestStartedTurnID, false, submission)
      }
      if (submission.turnId) {
        submissionAccepted = true
        // turn/started proves the server accepted this request even if the RPC
        // response was lost. Keep that proof after a very fast turn/completed too;
        // otherwise retrying the failed-looking row would duplicate the task.
        removeQueuedMessageFromThread(resolvedThreadID, queuedMessage.id)
        if (resolvedThreadID !== threadID) {
          removeQueuedMessageFromThread(threadID, queuedMessage.id)
        }
        if (liveTurnID) {
          setTurnFeedback(resolvedThreadID, { state: 'running', message: '', turnId: liveTurnID })
        }
        return
      }
      if (submission.requestStarted && isPendingThreadSubmission(resolvedThreadID, submission)) {
        // Wails can deliver turn/started just after the rejected call promise. Keep
        // ownership briefly so that accepted work is not exposed as retryable.
        keepSubmissionPending = true
        trackedTimeout(() => {
          const pending = pendingThreadSubmissionOwner(submission)
          if (!pending || submission.turnId) return
          const ownerID = pending.threadID
          pendingCollaborationModeByThread.delete(ownerID)
          pendingCollaborationModeByThread.delete(resolvedThreadID)
          if (resolvedThreadID !== threadID) pendingCollaborationModeByThread.delete(threadID)
          markItemFailed(ownerID, queuedMessage.localItemId)
          setTurnFeedback(ownerID, { state: 'failed', message, turnId: '' })
          patchQueuedMessage(ownerID, queuedMessage.id, { state: 'failed', error: message })
          notify('error', translate('notifications.messageNotSent'), message)
          finishPendingThreadSubmission(ownerID, submission, false)
          if (!threadIsRunning(ownerID)) setLocalThreadStatus(ownerID, 'idle')
        }, PENDING_SUBMISSION_ERROR_GRACE_MS)
        return
      }
      pendingCollaborationModeByThread.delete(resolvedThreadID)
      if (resolvedThreadID !== threadID) pendingCollaborationModeByThread.delete(threadID)
      markItemFailed(resolvedThreadID, queuedMessage.localItemId)
      setTurnFeedback(resolvedThreadID, { state: 'failed', message, turnId: '' })
      patchQueuedMessage(resolvedThreadID, queuedMessage.id, { state: 'failed', error: message })
      notify('error', translate('notifications.messageNotSent'), message)
    } finally {
      if (!keepSubmissionPending) {
        finishPendingThreadSubmission(
          resolvedThreadID,
          submission,
          submissionAccepted || Boolean(submission.turnId),
        )
      }
      if (continueDraining) scheduleThreadQueueDrain(resolvedThreadID)
    }
  }

  function removeQueuedMessage(messageID: string): void {
    const threadID = activeThreadId.value
    const message = queuedMessagesByThread.value[threadID]?.find((item) => item.id === messageID)
    if (!message || message.state === 'sending') return
    removeQueuedMessageFromThread(threadID, messageID)
    if (threadID.startsWith('pending-thread-') && !queuedMessagesByThread.value[threadID] && !activeThread.value) {
      activeThreadId.value = ''
    }
    scheduleThreadQueueDrain(threadID)
  }

  function retryQueuedMessage(messageID: string): void {
    const threadID = activeThreadId.value
    const message = queuedMessagesByThread.value[threadID]?.find((item) => item.id === messageID)
    if (!message || message.state !== 'failed') return
    patchQueuedMessage(threadID, messageID, { state: 'queued', error: '' })
    scheduleThreadQueueDrain(threadID)
  }

  /** Reorder a waiting queue item. Leading `sending` items stay pinned at the front. */
  function reorderQueuedMessage(messageID: string, action: 'up' | 'down' | 'top'): void {
    const threadID = activeThreadId.value
    if (!threadID) return
    const messages = [...(queuedMessagesByThread.value[threadID] ?? [])]
    const index = messages.findIndex((item) => item.id === messageID)
    if (index < 0) return
    const message = messages[index]
    if (!message || message.state === 'sending') return

    let floor = 0
    while (floor < messages.length && messages[floor]?.state === 'sending') floor += 1

    let target = index
    if (action === 'top') target = floor
    else if (action === 'up') target = Math.max(floor, index - 1)
    else target = Math.min(messages.length - 1, index + 1)
    if (target === index) return

    messages.splice(index, 1)
    messages.splice(target, 0, message)
    queuedMessagesByThread.value = { ...queuedMessagesByThread.value, [threadID]: messages }
  }

  /**
   * Promote a queued message to the front and send ASAP.
   * If a turn is live, interrupt it first — turn/completed will drain the queue.
   */
  async function sendQueuedMessageNow(messageID: string): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID) return
    const message = queuedMessagesByThread.value[threadID]?.find((item) => item.id === messageID)
    if (!message) return
    // Orphan "sending" after reconnect/stuck drain — allow re-fire.
    if (message.state === 'sending') {
      if (isThreadSubmitting(threadID) || threadIsRunning(threadID) || pendingThreadSubmission(threadID)) return
      patchQueuedMessage(threadID, messageID, { state: 'queued', error: '' })
    }
    if (message.state === 'failed') {
      patchQueuedMessage(threadID, messageID, { state: 'queued', error: '' })
    }
    reorderQueuedMessage(messageID, 'top')
    if (threadIsRunning(threadID)) {
      await interruptTurn()
      return
    }
    // Without a live turn, clear stale busy flags and drain immediately.
    clearStaleBusyState(threadID)
    if (
      message.blockedByTurnId
      && !completedTurns.has(message.blockedByTurnId)
      && !threadIsBusy(threadID)
    ) {
      patchQueuedMessage(threadID, messageID, { blockedByTurnId: undefined })
    }
    if (!isReady.value) {
      notify('warning', translate('notifications.connectionFailed'), translate('app.connecting'))
      return
    }
    await drainThreadQueue(threadID)
  }

  async function steerMessage(text: string, images: string[] = []): Promise<boolean> {
    const message = text.trim()
    const threadID = activeThreadId.value
    const turnID = activeTurnId.value
    const imagePaths = uniqueImagePaths(images).slice(0, 4)
    if ((!message && !imagePaths.length) || !canSteerActiveTurn.value || !threadID || !turnID) return false

    const localItemID = `local-steer-${Date.now()}-${++queuedMessageSequence}`
    appendItem(threadID, createLocalUserItem(localItemID, message, imagePaths, turnID))
    setThreadSubmitting(threadID, true)
    setTurnFeedback(threadID, { state: 'submitting', message: translate('chat.steering'), turnId: turnID })
    try {
      await backend.SteerTurn({
        threadId: threadID,
        turnId: turnID,
        text: message,
        images: imagePaths,
      } satisfies SteerTurnRequest)
      setTurnFeedback(threadID, { state: 'running', message: '', turnId: turnID })
      return true
    } catch (error) {
      const message = errorMessage(error)
      markItemFailed(threadID, localItemID)
      setTurnFeedback(threadID, { state: 'failed', message, turnId: turnID })
      notify('error', translate('notifications.steerFailed'), message)
      trackedTimeout(() => {
        if (threadTurnID(threadID) === turnID) {
          setTurnFeedback(threadID, { state: 'running', message: '', turnId: turnID })
        }
      }, 1800)
      return false
    } finally {
      setThreadSubmitting(threadID, false)
    }
  }

  async function interruptTurn(): Promise<void> {
    const threadID = activeThreadId.value
    const turnID = activeTurnId.value || activeTurnFeedback.value?.turnId || ''
    if (interruptingTurn.value) return
    if (!threadID || !turnID) {
      notify('warning', translate('notifications.turnStopFailed'), translate('chat.stopping'))
      return
    }
    interruptingTurn.value = true
    setTurnFeedback(threadID, {
      state: 'running',
      message: translate('chat.stopping'),
      turnId: turnID,
    })
    // Cancel any open approval prompt first so the turn can finish interrupting.
    if (pendingRequest.value) {
      void resolveApproval('cancel')
    }
    try {
      await backend.InterruptTurn(threadID, turnID)
      // Keep "正在停止…" until turn/completed; force-clear if Codex stalls.
      trackedTimeout(() => {
        if (threadTurnID(threadID) !== turnID) {
          interruptingTurn.value = false
          return
        }
        rememberCompletedTurn(turnID, 'interrupted')
        setThreadTurn(threadID, '')
        finalizeActiveItemsForCompletedTurns(threadID, turnID)
        finalizeOrphanedActiveItems(threadID)
        setLocalThreadStatus(threadID, 'idle')
        setTurnFeedback(threadID, {
          state: 'interrupted',
          message: translate('chat.interrupted'),
          turnId: turnID,
        })
        interruptingTurn.value = false
        clearStaleBusyState(threadID)
        scheduleThreadQueueDrain(threadID)
      }, 8000)
    } catch (error) {
      interruptingTurn.value = false
      notify('error', translate('notifications.turnStopFailed'), errorMessage(error))
      if (threadTurnID(threadID) === turnID) {
        setTurnFeedback(threadID, { state: 'running', message: '', turnId: turnID })
      }
    }
  }

  async function resolveApproval(action: 'once' | 'session' | 'deny' | 'cancel'): Promise<void> {
    const request = pendingRequest.value
    if (!request) return
    if (request.method === 'item/permissions/requestApproval') {
      const requested = asRecord(request.data.permissions)
      const permissions: Record<string, unknown> = {}
      if (action === 'once' || action === 'session') {
        const network = asRecord(requested.network)
        const fileSystem = asRecord(requested.fileSystem)
        if (Object.keys(network).length) permissions.network = network
        if (Object.keys(fileSystem).length) permissions.fileSystem = fileSystem
      }
      try {
        await backend.ResolveServerRequest(request.requestKey, {
          permissions,
          scope: action === 'session' ? 'session' : 'turn',
        })
        removePendingRequest(request.requestKey)
      } catch (error) {
        notify('error', translate('notifications.approvalFailed'), errorMessage(error))
      }
      return
    }
    const legacy = request.method === 'applyPatchApproval' || request.method === 'execCommandApproval'
    const decisions = legacy
      ? { once: 'approved', session: 'approved_for_session', deny: 'denied', cancel: 'abort' }
      : { once: 'accept', session: 'acceptForSession', deny: 'decline', cancel: 'cancel' }
    try {
      await backend.ResolveServerRequest(request.requestKey, { decision: decisions[action] })
      removePendingRequest(request.requestKey)
    } catch (error) {
      notify('error', translate('notifications.approvalFailed'), errorMessage(error))
    }
  }

  async function resolveUserInput(answers: Record<string, string[]>): Promise<void> {
    const request = pendingRequest.value
    if (!request || request.method !== 'item/tool/requestUserInput') return
    const payload = Object.fromEntries(
      Object.entries(answers).map(([id, values]) => [id, { answers: values }]),
    )
    try {
      await backend.ResolveServerRequest(request.requestKey, { answers: payload })
      removePendingRequest(request.requestKey)
    } catch (error) {
      notify('error', translate('notifications.answerFailed'), errorMessage(error))
    }
  }

  async function resolveMcpElicitation(action: 'accept' | 'decline' | 'cancel', content: Record<string, unknown> | null): Promise<void> {
    const request = pendingRequest.value
    if (!request || request.method !== 'mcpServer/elicitation/request') return
    try {
      await backend.ResolveServerRequest(request.requestKey, {
        action,
        content: action === 'accept' ? content : null,
        _meta: null,
      })
      removePendingRequest(request.requestKey)
    } catch (error) {
      notify('error', translate('notifications.answerFailed'), errorMessage(error))
    }
  }

  function setSearch(value: string): void {
    threadSearch.value = value
  }

  function removePendingRequest(requestKey: string): void {
    pendingRequests.value = pendingRequests.value.filter((request) => request.requestKey !== requestKey)
  }

  function handleCodexEvent(event: CodexEvent): void {
    switch (event.type) {
      case 'status':
        {
          const next = normalizeStatus(event.data)
          connection.value = next.state === 'disconnected'
            && connection.value.state === 'error'
            && lastTransportMessage.value
            ? { ...next, state: 'error', message: lastTransportMessage.value }
            : next
        }
        if (connection.value.state === 'ready') {
          lastTransportMessage.value = ''
          // A ready status always belongs to a newly started app-server. Turns
          // from the previous process cannot still be live in this transport.
          releaseDisconnectedTurns()
          // Connection drop can leave sending/submitting/live-item ghosts.
          resetOrphanedInFlightSends()
          for (const threadID of Object.keys(queuedMessagesByThread.value)) {
            clearStaleBusyState(threadID)
          }
          drainAvailableThreadQueues()
        } else if (connection.value.state === 'error' || connection.value.state === 'disconnected') {
          releaseDisconnectedTurns(connection.value.state === 'error' ? connection.value.message : '')
        }
        break
      case 'notification':
        handleNotification(event.method ?? '', event.data)
        break
      case 'request':
        pendingRequests.value = [
          ...pendingRequests.value,
          {
            requestKey: event.requestKey ?? '',
            method: event.method ?? '',
            data: asRecord(event.data),
          },
        ]
        break
      case 'stderr':
        lastTransportMessage.value = asString(asRecord(event.data).message)
        break
      case 'transport-error': {
        const message = asString(asRecord(event.data).message, translate('app.connectionError'))
        const duplicate = connection.value.state === 'error' && lastTransportMessage.value === message
        lastTransportMessage.value = message
        connection.value = {
          ...connection.value,
          state: 'error',
          running: false,
          message,
        }
        releaseDisconnectedTurns(message)
        if (!duplicate) {
          notify('error', translate('notifications.connectionLost'), message, {
            label: translate('common.reconnect'),
            onClick: () => connect(),
          })
        }
        break
      }
      case 'unsupported-request': {
        const data = asRecord(event.data)
        const method = asString(data.method, asString(event.method))
        const message = asString(data.message, translate('notifications.unsupported'))
        lastTransportMessage.value = method ? `${message} (${method})` : message
        // Only attach the notice to the thread named by the event — never the
        // currently focused thread (avoids cross-session UI contamination).
        const threadID = asString(data.threadId)
        if (threadID) {
          appendItem(threadID, {
            id: `notice-unsupported-${Date.now()}`,
            turnId: '',
            type: 'notice',
            status: 'completed',
            text: lastTransportMessage.value,
            command: '',
            cwd: '',
            output: '',
            title: translate('notifications.unsupported'),
            detail: method,
            failed: false,
            attachments: [],
            changes: [],
            startedAt: Date.now(),
            completedAt: Date.now(),
          })
        }
        notify('warning', translate('notifications.unsupported'), lastTransportMessage.value)
        break
      }
    }
  }

  function handleNotification(method: string, data: unknown): void {
    const payload = asRecord(data)
    switch (method) {
      case 'thread/started': {
        const thread = normalizeRuntimeThread(payload.thread, payload)
        if (thread) addOrUpdateThread(thread)
        break
      }
      case 'thread/name/updated': {
        const id = asString(payload.threadId)
        const name = asString(payload.name)
        threads.value = threads.value.map((thread) => thread.id === id ? { ...thread, name } : thread)
        const nextProjects = { ...projectThreads.value }
        for (const [path, projectItems] of Object.entries(nextProjects)) {
          nextProjects[path] = projectItems.map((thread) => thread.id === id ? { ...thread, name } : thread)
        }
        projectThreads.value = nextProjects
        if (activeThread.value?.id === id) activeThread.value = { ...activeThread.value, name }
        break
      }
      case 'thread/status/changed': {
        const threadID = asString(payload.threadId)
        const status = normalizeThreadStatus(payload.status)
        // Missing/unknown status must not be interpreted as idle: older/newer
        // app-server versions use different payload shapes.
        if (!threadID || !status) break
        setReportedThreadStatus(threadID, status)
        if (isActiveStatus(status)) {
          const reportedTurnID = asString(payload.turnId, asString(asRecord(payload.turn).id))
          if (reportedTurnID) {
            setThreadTurn(threadID, reportedTurnID)
            setTurnFeedback(threadID, { state: 'running', message: '', turnId: reportedTurnID })
          }
        } else {
          const finishingTurnID = threadTurnID(threadID) || liveFeedbackTurnID(threadFeedback(threadID))
          scheduleIdleThreadReconcile(threadID, finishingTurnID)
        }
        break
      }
      case 'thread/archived':
      case 'thread/deleted':
      case 'thread/closed': {
        const threadID = asString(payload.threadId)
        threads.value = threads.value.filter((thread) => thread.id !== threadID)
        const nextProjects = { ...projectThreads.value }
        for (const [path, projectItems] of Object.entries(nextProjects)) {
          nextProjects[path] = projectItems.filter((thread) => thread.id !== threadID)
        }
        projectThreads.value = nextProjects
        releasePendingThreadSubmissions(threadID)
        clearThreadQueue(threadID)
        latestStartedTurnByThread.delete(threadID)
        clearThreadAliases(threadID)
        if ((method === 'thread/deleted' || method === 'thread/closed') && activeThreadId.value === threadID) {
          activeThread.value = null
          activeThreadId.value = ''
        }
        break
      }
      case 'thread/unarchived': {
        void loadThreads().catch(() => undefined)
        break
      }
      case 'thread/compacted':
        {
          const compactedThreadID = asString(payload.threadId)
          const compactedTurnID = asString(payload.turnId) || threadTurnID(compactedThreadID)
          const compactedUsage = payload.tokenUsage ?? payload.usage ?? payload.token_usage
          handleContextCompaction(
            compactedThreadID,
            compactedTurnID,
            compactedUsage,
            normalizeLocalUsageRuntime(payload.runtime),
          )
        }
        break
      case 'turn/started':
        {
          const threadID = asString(payload.threadId)
          const turn = asRecord(payload.turn)
          const turnID = asString(turn.id)
          const pendingSubmission = pendingThreadSubmission(threadID)?.submission
          const duplicatePreviousTurn = Boolean(
            turnID
            && pendingSubmission?.requestStarted
            && !pendingSubmission.turnId
            && pendingSubmission.previousTurnId === turnID,
          )
          if (turnID && (completedTurns.has(turnID) || duplicatePreviousTurn)) {
            const startedAt = typeof turn.startedAt === 'number' ? turn.startedAt * 1000 : Date.now()
            patchTurnMetrics(threadID, turnID, { startedAt })
            break
          }
          bindPendingCollaborationMode(threadID, turnID)
          completedTurns.delete(turnID)
          completedTurnStatus.delete(turnID)
          if (threadID && turnID) latestStartedTurnByThread.set(threadID, turnID)
          setThreadTurn(threadID, turnID)
          setLocalThreadStatus(threadID, 'active')
          const startedAt = typeof turn.startedAt === 'number' ? turn.startedAt * 1000 : Date.now()
          patchTurnMetrics(threadID, turnID, { startedAt, completedAt: null, durationMs: null })
          setTurnFeedback(threadID, { state: 'running', message: '', turnId: turnID })
          // Bind the exact queue dispatch before releasing its submitting lock.
          // Followers admitted during the RPC gap now depend on this real turn.
          settleAcceptedPendingThreadSubmission(threadID, turnID)
        }
        break
      case 'turn/completed': {
        const threadID = asString(payload.threadId)
        const turn = asRecord(payload.turn)
        const turnID = asString(turn.id)
        const status = normalizeThreadStatus(turn.status, 'completed')
        const completedAt = typeof turn.completedAt === 'number' ? turn.completedAt * 1000 : Date.now()
        const startedAt = typeof turn.startedAt === 'number' ? turn.startedAt * 1000 : undefined
        const durationMs = typeof turn.durationMs === 'number' ? turn.durationMs : undefined
        // A terminal event is still authoritative acceptance when turn/started was
        // lost or delivered out of order; the previous-turn guard rejects stale ones.
        bindPendingCollaborationMode(threadID, turnID)
        settleAcceptedPendingThreadSubmission(threadID, turnID)
        // Flush streamed text first — turn/completed can race ahead of item/completed.
        flushThreadDeltas(threadID)
        rememberPlanCandidatesFromTurn(threadID, turnID)
        patchTurnMetrics(threadID, turnID, { startedAt, completedAt, durationMs })
        rememberCompletedTurn(turnID, status)
        const currentTurnID = threadTurnID(threadID)
        const pendingSubmission = pendingThreadSubmission(threadID)?.submission
        const pendingDifferentTurn = Boolean(
          pendingSubmission && (!turnID || pendingSubmission.turnId !== turnID),
        )
        const completedCurrentTurn = !pendingDifferentTurn && (
          !currentTurnID
          || currentTurnID === turnID
          || completedTurns.has(currentTurnID)
        )
        // Always drop local tracking for the completed turn so the queue can drain.
        if (!currentTurnID || currentTurnID === turnID) setThreadTurn(threadID, '')
        finalizeActiveItemsForCompletedTurns(threadID, turnID)
        if (!pendingDifferentTurn) setLocalThreadStatus(threadID, 'idle')
        if (completedCurrentTurn) {
          interruptingTurn.value = false
          if (isInterruptedStatus(status)) {
            setTurnFeedback(threadID, {
              state: 'interrupted',
              message: translate('chat.interrupted'),
              turnId: turnID,
            })
          } else if (isFailedStatus(status)) {
            setTurnFeedback(threadID, {
              state: 'failed',
              message: asString(asRecord(turn.error).message, translate('notifications.turnFailedFallback')),
              turnId: turnID,
            })
          } else {
            clearTurnFeedback(threadID)
            schedulePlanImplementationOffer(threadID, turnID)
          }
          if (isInterruptedStatus(status) || isFailedStatus(status)) {
            clearPlanTurnTracking(threadID, turnID)
          }
          notifyTurnCompleted(threadID, status)
        } else {
          // Mismatched turn id still means something finished — clear stale "running".
          const feedback = threadFeedback(threadID)
          if (
            !pendingDifferentTurn
            && (
              feedback?.turnId === turnID
              || (!feedback?.turnId && (feedback?.state === 'running' || feedback?.state === 'submitting'))
            )
          ) {
            clearTurnFeedback(threadID)
          }
          clearPlanTurnTracking(threadID, turnID)
        }
        // Interrupt often omits item/completed — force orphan tools off the busy path.
        finalizeOrphanedActiveItems(threadID)
        loadThreads().catch(() => undefined)
        // Gemini/OpenCode can persist authoritative usage only after their CLI
        // process exits. Refresh even when the stream omitted a token event.
        if (runtimeIDForThread(threadID) !== 'codex') {
          void appStore.loadLocalUsage()
        }
        workspaceStore.refreshWorkspace()
        // Drain now and once more shortly after late item/status events settle.
        clearStaleBusyState(threadID)
        scheduleThreadQueueDrain(threadID)
        trackedTimeout(() => {
          clearStaleBusyState(threadID)
          scheduleThreadQueueDrain(threadID)
        }, 60)
        break
      }
      case 'item/started':
      case 'item/completed': {
        const threadID = asString(payload.threadId)
        const turnID = asString(payload.turnId)
        let item = normalizeTimelineItem(payload.item, turnID)
        if (item) {
          if (completedTurns.has(turnID) && isActiveStatus(item.status)) {
            item = {
              ...item,
              status: terminalItemStatus(turnID),
              completedAt: item.completedAt ?? Date.now(),
            }
          }
          // Commit any pending streamed text before a new item appears or a
          // completed snapshot merges — otherwise the UI can briefly/permanently
          // show a truncated prefix (漏字) while deltas sit in the 24ms buffer.
          if (method === 'item/completed') flushBufferedItem(threadID, item.id)
          else flushThreadDeltas(threadID)
          item.startedAt = typeof payload.startedAtMs === 'number' ? payload.startedAtMs : item.startedAt
          item.completedAt = typeof payload.completedAtMs === 'number' ? payload.completedAtMs : item.completedAt
          upsertItem(threadID, item)
          if (method === 'item/completed') {
            if (item.type === 'contextCompaction') {
              const rawItem = asRecord(payload.item)
              handleContextCompaction(
                threadID,
                turnID || item.turnId || threadTurnID(threadID),
                payload.tokenUsage ?? payload.usage ?? payload.token_usage
                  ?? rawItem.tokenUsage ?? rawItem.usage ?? rawItem.token_usage,
                normalizeLocalUsageRuntime(payload.runtime ?? rawItem.runtime),
              )
            }
            rememberPlanCandidate(threadID, turnID, item)
            // turn/completed may arrive before the final plan / agentMessage item.
            if (completedTurns.has(turnID) && !isThreadSubmitting(threadID)) {
              schedulePlanImplementationOffer(threadID, turnID)
            }
          }
        }
        break
      }
      case 'turn/plan/updated': {
        const threadID = asString(payload.threadId)
        const turnID = asString(payload.turnId)
        const steps = asArray(payload.plan).map((entry) => {
          const row = asRecord(entry)
          const step = asString(row.step, asString(row.text))
          const status = asString(row.status, 'pending')
          return step ? `- [${status === 'completed' || status === 'done' ? 'x' : ' '}] ${step}` : ''
        }).filter(Boolean)
        const explanation = asString(payload.explanation)
        const text = [explanation, ...steps].filter(Boolean).join('\n').trim()
        if (threadID && turnID && text) {
          // Keep the candidate, but only a turn submitted in Plan mode may offer implementation.
          pendingPlanByThread.set(threadID, { turnId: turnID, text })
          sawPlanUpdateByTurn.set(turnID, text)
          if (completedTurns.has(turnID) && !isThreadSubmitting(threadID)) {
            schedulePlanImplementationOffer(threadID, turnID)
          }
        }
        break
      }
      case 'item/agentMessage/delta':
        queueDelta({
          threadId: asString(payload.threadId),
          turnId: asString(payload.turnId),
          itemId: asString(payload.itemId),
          field: 'text',
          type: 'agentMessage',
          delta: notificationTextDelta(payload),
        })
        break
      case 'item/commandExecution/outputDelta':
        queueDelta({
          threadId: asString(payload.threadId),
          turnId: asString(payload.turnId),
          itemId: asString(payload.itemId),
          field: 'output',
          type: 'commandExecution',
          delta: notificationTextDelta(payload),
        })
        break
      case 'item/reasoning/summaryTextDelta':
      case 'item/reasoning/delta':
        queueDelta({
          threadId: asString(payload.threadId),
          turnId: asString(payload.turnId),
          itemId: asString(payload.itemId),
          field: 'reasoningSummary',
          type: 'reasoning',
          delta: notificationTextDelta(payload),
        })
        break
      case 'item/reasoning/textDelta':
        queueDelta({
          threadId: asString(payload.threadId),
          turnId: asString(payload.turnId),
          itemId: asString(payload.itemId),
          field: 'reasoningContent',
          type: 'reasoning',
          delta: notificationTextDelta(payload),
        })
        break
      case 'item/reasoning/summaryTextDone': {
        // Newer sequential-cutoff path may deliver atomic summary sections.
        const threadID = asString(payload.threadId)
        const turnID = asString(payload.turnId)
        const itemID = asString(payload.itemId)
        const doneText = notificationTextDelta(payload) || asString(payload.text)
        if (threadID && itemID && doneText) {
          flushBufferedItem(threadID, itemID)
          const existing = (itemsByThread.value[threadID] ?? []).find((item) => item.id === itemID)
          const current = existing?.reasoningSummary?.trim() ?? ''
          const nextSummary = current && !current.includes(doneText)
            ? `${current}\n\n${doneText}`
            : (current || doneText)
          upsertItem(threadID, {
            id: itemID,
            turnId: turnID || existing?.turnId || '',
            type: 'reasoning',
            status: existing?.status || 'inProgress',
            text: nextSummary,
            command: '',
            cwd: '',
            output: '',
            title: '',
            detail: '',
            changes: [],
            attachments: [],
            reasoningSummary: nextSummary,
            reasoningContent: existing?.reasoningContent,
          })
        }
        break
      }
      case 'item/reasoning/summaryPartAdded': {
        if (Number(payload.summaryIndex) <= 0) break
        const threadID = asString(payload.threadId)
        const itemID = asString(payload.itemId)
        if (!threadID || !itemID) break
        // Avoid leaving a whitespace-only summary that expands to an empty body.
        const existing = (itemsByThread.value[threadID] ?? []).find((item) => item.id === itemID)
        const buffered = deltaBuffers.get(`${threadID}:${itemID}:reasoningSummary`)?.delta ?? ''
        const current = `${existing?.reasoningSummary ?? ''}${buffered}`.trim()
        if (!current) break
        queueDelta({
          threadId: threadID,
          turnId: asString(payload.turnId),
          itemId: itemID,
          field: 'reasoningSummary',
          type: 'reasoning',
          delta: '\n\n',
        })
        break
      }
      case 'item/plan/delta':
        queueDelta({
          threadId: asString(payload.threadId),
          turnId: asString(payload.turnId),
          itemId: asString(payload.itemId),
          field: 'text',
          type: 'plan',
          delta: notificationTextDelta(payload),
        })
        break
      case 'item/fileChange/outputDelta':
        queueDelta({
          threadId: asString(payload.threadId),
          turnId: asString(payload.turnId),
          itemId: asString(payload.itemId),
          field: 'output',
          type: 'fileChange',
          delta: notificationTextDelta(payload),
        })
        break
      case 'item/fileChange/patchUpdated': {
        const threadID = asString(payload.threadId)
        const turnID = asString(payload.turnId)
        const existing = (itemsByThread.value[threadID] ?? []).find((entry) => entry.id === asString(payload.itemId))
        const item = normalizeTimelineItem({
          id: asString(payload.itemId),
          type: 'fileChange',
          status: existing?.status || 'inProgress',
          changes: payload.changes,
        }, turnID)
        if (item) upsertItem(threadID, item)
        break
      }
      case 'item/mcpToolCall/progress':
        patchItem(asString(payload.threadId), asString(payload.itemId), {
          detail: asString(payload.message),
        })
        break
      case 'turn/diff/updated': {
        const turnID = asString(payload.turnId)
        const threadID = asString(payload.threadId)
        const diff = asString(payload.diff)
        if (turnID && threadID) {
          pendingDiffs.set(turnID, { threadId: threadID, turnId: turnID, diff })
          if (!diffTimer) diffTimer = trackedTimeout(flushDiffs, 100)
        }
        break
      }
      case 'serverRequest/resolved':
        removePendingRequest(String(payload.requestId ?? ''))
        break
      case 'warning':
      case 'configWarning':
        notify('warning', translate('notifications.runtimeWarning', {
          runtime: runtimeNameForThread(asString(payload.threadId)),
        }), asString(payload.message, translate('notifications.warningFallbackRuntime', {
          runtime: runtimeNameForThread(asString(payload.threadId)),
        })))
        break
      case 'guardianWarning':
      case 'deprecationNotice':
      case 'windows/worldWritableWarning':
        notify('warning', translate('notifications.runtimeWarning', {
          runtime: runtimeNameForThread(asString(payload.threadId)),
        }), asString(payload.message, asString(payload.detail)))
        break
      case 'error':
      case 'turn/error': {
        const threadID = asString(payload.threadId)
        const reportedTurnID = asString(payload.turnId, asString(asRecord(payload.turn).id))
        const knownTurnID = threadTurnID(threadID) || liveFeedbackTurnID(threadFeedback(threadID))
        const turnID = reportedTurnID || (method === 'turn/error' ? knownTurnID : '')
        const message = asString(
          asRecord(payload.error).message,
          asString(payload.message, translate('notifications.turnFailedFallback')),
        )
        if (turnID && completedTurns.has(turnID)) break
        bindPendingCollaborationMode(threadID, turnID)
        settleAcceptedPendingThreadSubmission(threadID, turnID)
        const unresolvedSubmission = pendingThreadSubmission(threadID)
        if (unresolvedSubmission) break
        // A generic transport/protocol error without turn ownership is not proof
        // that the currently running turn ended.
        if (!turnID) {
          notify('error', translate('notifications.turnFailedRuntime', {
            runtime: runtimeNameForThread(threadID),
          }), message)
          break
        }
        if (payload.willRetry === true) {
          setThreadTurn(threadID, turnID)
          setLocalThreadStatus(threadID, 'active')
          setTurnFeedback(threadID, {
            state: 'retrying',
            message: `${translate('chat.runtimeRetrying', {
              runtime: runtimeNameForThread(threadID),
            })} ${message}`,
            turnId: turnID,
          })
        } else {
          rememberCompletedTurn(turnID, 'failed')
          const currentTurnID = threadTurnID(threadID)
          const failedCurrentTurn = currentTurnID === turnID
            || (!currentTurnID && !isThreadSubmitting(threadID))
          if (!currentTurnID || currentTurnID === turnID) setThreadTurn(threadID, '')
          finalizeActiveItemsForCompletedTurns(threadID, turnID)
          if (!threadIsRunning(threadID)) finalizeOrphanedActiveItems(threadID)
          if (failedCurrentTurn) {
            interruptingTurn.value = false
            setLocalThreadStatus(threadID, 'idle')
            setTurnFeedback(threadID, { state: 'failed', message, turnId: turnID })
          }
          notify('error', translate('notifications.turnFailed'), message)
          scheduleThreadQueueDrain(threadID)
        }
        break
      }
      case 'account/login/completed':
        if (payload.success === true) {
          notify('success', translate('notifications.signedIn'), translate('notifications.signedInHint'))
          appStore.refreshAccountData()
        } else {
          notify('error', translate('notifications.signInFailed'), asString(payload.error, translate('notifications.signInFailedHint')))
        }
        break
      case 'account/rateLimits/updated':
        appStore.accountRateLimits = normalizeAccountRateLimits(payload.rateLimits, appStore.accountRateLimits)
        break
      case 'thread/tokenUsage/updated':
        {
          const threadID = asString(payload.threadId)
          const turnID = asString(payload.turnId)
            || threadTurnID(threadID)
            || liveFeedbackTurnID(threadFeedback(threadID))
          const rawUsage = payload.tokenUsage ?? payload.usage ?? payload.token_usage ?? payload
        queueTokenUsage(
          threadID,
          turnID,
          rawUsage,
          normalizeLocalUsageRuntime(payload.runtime),
        )
        }
        break
      case 'skills/changed':
      case 'app/list/updated':
        capabilitiesStore.scheduleRefresh()
        break
      case 'mcpServer/startupStatus/updated': {
        capabilitiesStore.handleMcpStatusUpdate(payload)
        break
      }
      case 'model/rerouted': {
        const rerouteThreadID = asString(payload.threadId)
        if (rerouteThreadID) {
          updateThreadModelIdentity(
            rerouteThreadID,
            asString(payload.toModel),
            asString(payload.modelProvider),
          )
        }
        notify(
          'info',
          translate('notifications.modelRerouted'),
          `${asString(payload.fromModel)} → ${asString(payload.toModel)} · ${asString(payload.reason)}`,
        )
        break
      }
      case 'mcpServer/oauthLogin/completed':
        if (payload.success === true) {
          notify('success', translate('capabilities.mcpConnected'), asString(payload.name))
        } else {
          notify('error', translate('capabilities.mcpLoginFailed'), asString(payload.error))
        }
        break
      case 'command/exec/outputDelta': {
        terminalStore.handleOutputDelta(asString(payload.processId), asString(payload.deltaBase64))
        break
      }
      case 'nice/terminal/exit':
        terminalStore.handleExit(asString(payload.processId), asString(payload.error))
        break
    }
  }

  function handleContextCompaction(
    threadID: string,
    turnID: string,
    usage: unknown,
    runtime: LocalUsageRuntime | '' = '',
  ): void {
    if (!threadID) return
    if (usage !== undefined) queueTokenUsage(threadID, turnID, usage, runtime)

    // Some app-server versions emit both the legacy thread event and the
    // contextCompaction item lifecycle for one compaction.
    const now = Date.now()
    const duplicate = now - (recentCompactionByThread.get(threadID) ?? 0) < 5_000
    recentCompactionByThread.set(threadID, now)
    if (duplicate) return

    // Refresh the persisted snapshot after upstream compaction so the context
    // window and historical token metrics survive reopening this conversation.
    if (activeThreadId.value === threadID) {
      loadedThreadIDs.delete(threadID)
      void openThread(threadID)
    }
    void appStore.loadLocalUsage()
    notify('info', translate('notifications.contextCompacted'), translate('notifications.contextCompactedRuntimeHint', {
      runtime: runtimeNameForThread(threadID),
    }))
  }

  function queueTokenUsage(
    threadID: string,
    turnID: string,
    value: unknown,
    runtime: LocalUsageRuntime | '' = '',
  ): void {
    if (!threadID) return
    const usage = normalizeThreadTokenUsage(value)
    const hasTokens = [
      usage.total.totalTokens,
      usage.total.inputTokens,
      usage.total.cachedInputTokens,
      usage.total.outputTokens,
      usage.total.reasoningOutputTokens,
      usage.last.totalTokens,
      usage.last.inputTokens,
      usage.last.cachedInputTokens,
      usage.last.outputTokens,
      usage.last.reasoningOutputTokens,
    ].some((item) => item > 0)
    if (!hasTokens && usage.modelContextWindow == null) return
    pendingTokenUsage.set(`${threadID}:${turnID}`, { threadId: threadID, turnId: turnID, runtime, usage })
    if (!tokenUsageTimer) tokenUsageTimer = trackedTimeout(flushTokenUsage, 250)
  }

  function flushTokenUsage(): void {
    tokenUsageTimer = 0
    if (!pendingTokenUsage.size) return
    const next = { ...tokenUsageByThread.value }
    const nextMetrics = { ...turnMetricsByThread.value }
    const persistJobs: Array<Promise<unknown>> = []
    for (const { threadId, turnId, runtime, usage } of pendingTokenUsage.values()) {
      next[threadId] = usage
      if (turnId) {
        const threadMetrics = { ...(nextMetrics[threadId] ?? {}) }
        const current = threadMetrics[turnId] ?? emptyTurnMetrics()
        threadMetrics[turnId] = { ...current, tokenUsage: usage.last }
        nextMetrics[threadId] = threadMetrics
        const last = usage.last
        const tokens = Math.max(
          0,
          Number(last?.totalTokens)
            || (
              (last?.inputTokens || 0)
              + (last?.cachedInputTokens || 0)
              + (last?.outputTokens || 0)
              + (last?.reasoningOutputTokens || 0)
            ),
        )
        if (tokens > 0) {
          // Always persist full breakdown when available (input/cache/output/reasoning).
          const usageRuntime = runtime || runtimeIDForThread(threadId)
          const detailedRecorder = (backend as {
            RecordLocalTurnUsageDetailed?: (
              runtime: string,
              threadID: string,
              turnID: string,
              input: number,
              cached: number,
              output: number,
              reasoning: number,
              total: number,
            ) => Promise<void>
          }).RecordLocalTurnUsageDetailed
          if (detailedRecorder) {
            persistJobs.push(
              detailedRecorder(
                usageRuntime,
                threadId,
                turnId,
                last?.inputTokens || 0,
                last?.cachedInputTokens || 0,
                last?.outputTokens || 0,
                last?.reasoningOutputTokens || 0,
                tokens,
              )
                .catch(() => usageRuntime === 'codex'
                  ? backend.RecordLocalTurnUsage(threadId, turnId, tokens).catch(() => undefined)
                  : undefined),
            )
          } else if (usageRuntime === 'codex') {
            persistJobs.push(backend.RecordLocalTurnUsage(threadId, turnId, tokens).catch(() => undefined))
          }
        }
      }
    }
    pendingTokenUsage.clear()
    tokenUsageByThread.value = next
    turnMetricsByThread.value = nextMetrics
    void Promise.allSettled(persistJobs).finally(() => {
      void appStore.loadLocalUsage()
    })
  }

  function notifyTurnCompleted(threadID: string, status: string): void {
    if (!appStore.settings.notifyOnTurnComplete) return
    // Match Codex: prefer notifying when the window is in the background.
    if (typeof document !== 'undefined' && !document.hidden && document.hasFocus()) return
    const thread = findThreadSummary(threadID) || (activeThread.value?.id === threadID ? activeThread.value : null)
    const title = thread?.name?.trim() || translate('notifications.turnCompleteTitle')
    let message = translate('notifications.turnCompleteHintRuntime', {
      runtime: runtimeNameForThread(threadID),
    })
    if (isInterruptedStatus(status)) message = translate('chat.interrupted')
    else if (isFailedStatus(status)) message = translate('notifications.turnFailedFallback')
    notify(isFailedStatus(status) ? 'error' : isInterruptedStatus(status) ? 'warning' : 'success', title, message)
    if (typeof Notification !== 'undefined' && Notification.permission === 'granted') {
      try {
        const desktop = new Notification(title, { body: message, silent: false })
        desktop.onclick = () => {
          window.focus()
          desktop.close()
        }
      } catch {
        // Embedded webviews may reject Notification construction.
      }
    }
  }

  function notificationTextDelta(payload: Record<string, unknown>): string {
    return asString(
      payload.delta,
      asString(payload.text, asString(payload.content, asString(payload.summaryDelta))),
    )
  }

  function queueDelta(delta: DeltaBuffer): void {
    if (!delta.threadId || !delta.itemId || !delta.delta) return
    const key = `${delta.threadId}:${delta.itemId}:${delta.field}`
    const previous = deltaBuffers.get(key)
    deltaBuffers.set(key, previous ? { ...delta, delta: previous.delta + delta.delta } : delta)
    if (!deltaTimer) deltaTimer = trackedTimeout(flushDeltas, 48)
  }

  function flushDeltas(): void {
    deltaTimer = 0
    const grouped = new Map<string, DeltaBuffer[]>()
    for (const delta of deltaBuffers.values()) {
      const existing = grouped.get(delta.threadId)
      if (existing) existing.push(delta)
      else grouped.set(delta.threadId, [delta])
    }
    deltaBuffers.clear()

    const current = itemsByThread.value
    const activeID = activeThreadId.value
    let activeChanged = false
    for (const [threadID, deltas] of grouped) {
      const nextItems = [...(current[threadID] ?? [])]
      for (const delta of deltas) {
        const completedStatus = completedTurns.has(delta.turnId)
          ? terminalItemStatus(delta.turnId)
          : ''
        let index = nextItems.findIndex((item) => item.id === delta.itemId)
        if (index < 0) {
          nextItems.push({
            id: delta.itemId,
            turnId: delta.turnId,
            type: delta.type,
            status: completedStatus || 'inProgress',
            text: '',
            command: '',
            cwd: '',
            output: '',
            title: '',
            detail: '',
            changes: [],
            attachments: [],
          })
          index = nextItems.length - 1
        }
        const item = nextItems[index]
        if (!item) continue
        if (delta.field === 'reasoningSummary' || delta.field === 'reasoningContent') {
          const reasoningSummary = delta.field === 'reasoningSummary'
            ? appendBoundedDelta(item.reasoningSummary ?? '', delta.delta, 250_000)
            : item.reasoningSummary ?? ''
          const reasoningContent = delta.field === 'reasoningContent'
            ? appendBoundedDelta(item.reasoningContent ?? '', delta.delta, 250_000)
            : item.reasoningContent ?? ''
          nextItems[index] = {
            ...item,
            reasoningSummary,
            reasoningContent,
            text: reasoningSummary || reasoningContent,
          }
        } else {
          const limit = delta.field === 'output' ? 300_000 : 1_000_000
          nextItems[index] = { ...item, [delta.field]: appendBoundedDelta(item[delta.field], delta.delta, limit) }
        }
        const updated = nextItems[index]
        if (completedStatus && updated && isActiveStatus(updated.status)) {
          nextItems[index] = {
            ...updated,
            status: completedStatus,
            completedAt: updated.completedAt ?? Date.now(),
          }
        }
      }
      // Background threads: mutate without shallowRef notify (timeline only watches active).
      current[threadID] = nextItems
      if (threadID === activeID) activeChanged = true
    }
    if (activeChanged) itemsByThread.value = { ...current }
  }

  function flushBufferedItem(threadID: string, itemID: string): void {
    const prefix = `${threadID}:${itemID}:`
    if (![...deltaBuffers.keys()].some((key) => key.startsWith(prefix))) return
    flushPendingDeltas()
  }

  function flushThreadDeltas(threadID: string): void {
    if (!threadID) return
    const prefix = `${threadID}:`
    if (![...deltaBuffers.keys()].some((key) => key.startsWith(prefix))) return
    flushPendingDeltas()
  }

  function flushPendingDeltas(): void {
    if (deltaTimer) {
      clearTrackedTimeout(deltaTimer)
      deltaTimer = 0
    }
    flushDeltas()
  }

  function flushDiffs(): void {
    diffTimer = 0
    if (!pendingDiffs.size) return
    const nextByTurn = { ...diffsByTurn.value }
    const nextByThread = { ...latestDiffByThread.value }
    for (const value of pendingDiffs.values()) {
      nextByTurn[value.turnId] = value.diff
      nextByThread[value.threadId] = value.diff
      if (value.turnId) diffTurnOwners.set(value.turnId, value.threadId)
    }
    pendingDiffs.clear()
    diffsByTurn.value = nextByTurn
    latestDiffByThread.value = nextByThread
  }

  function setActiveThread(thread: ThreadSummary, items: TimelineItem[]): void {
    activeThread.value = thread
    activeThreadId.value = thread.id
    itemsByThread.value = { ...itemsByThread.value, [thread.id]: items }
    workspaceStore.clearDiff()
  }

  async function clearActiveSession(): Promise<void> {
    activeThread.value = null
    activeThreadId.value = ''
    workspaceStore.clearDiff()
  }

  async function switchWorkMode(): Promise<void> {
    // Clear the composer selection for the other tab, but keep per-thread
    // running/queue maps so background turns are not treated as idle.
    activeThread.value = null
    activeThreadId.value = ''
    workspaceStore.clearDiff()
    await loadThreads()
  }

  function pendingSessionPreferenceWrite(sessionId: string): Promise<void> | undefined {
    let pending: Promise<void> | undefined
    for (const [id, write] of sessionPreferenceWrites) {
      if (sameThreadSession(id, sessionId)) pending = write
    }
    return pending
  }

  function updateSessionPreferences(request: SessionPreferencesRequest): Promise<void> {
    const sessionId = request.sessionId.trim()
    if (!sessionId || sessionId.startsWith('pending-thread-')) return Promise.resolve()
    const previous = pendingSessionPreferenceWrite(sessionId) ?? Promise.resolve()
    const write = previous
      .catch(() => undefined)
      .then(() => backend.UpdateSessionPreferences({ ...request, sessionId }))
    sessionPreferenceWrites.delete(sessionId)
    sessionPreferenceWrites.set(sessionId, write)
    const cleanup = () => {
      if (sessionPreferenceWrites.get(sessionId) === write) sessionPreferenceWrites.delete(sessionId)
    }
    void write.then(cleanup, cleanup)
    return write
  }

  async function waitForSessionPreferences(sessionId: string): Promise<void> {
    while (sessionId) {
      const pending = pendingSessionPreferenceWrite(sessionId)
      if (!pending) return
      try {
        await pending
      } catch {
        // Sending remains available with the last successfully persisted values.
      }
      const next = pendingSessionPreferenceWrite(sessionId)
      if (!next || next === pending) return
    }
  }

  function patchActiveSessionPreferences(
    model: string,
    effort = '',
    provider?: string,
    collaborationMode?: string,
  ): void {
    const thread = activeThread.value
    if (!thread) return
    const next: ThreadSummary = {
      ...thread,
      model: model || thread.model,
      effort: effort || thread.effort,
      modelProvider: provider === undefined ? thread.modelProvider : provider,
      collaborationMode: collaborationMode === undefined
        ? thread.collaborationMode
        : (collaborationMode || 'default'),
      updatedAt: Math.floor(Date.now() / 1000),
    }
    activeThread.value = next
    addOrUpdateThread(next)
    rememberThreadModelIdentity(next.id, next.model, next.modelProvider)
  }

  function patchActiveThreadMemories(useMemories: boolean, generateMemories: boolean): void {
    const thread = activeThread.value
    if (!thread) return
    const next: ThreadSummary = {
      ...thread,
      useMemories,
      generateMemories,
      updatedAt: Math.floor(Date.now() / 1000),
    }
    activeThread.value = next
    addOrUpdateThread(next)
  }

  async function setCollaborationMode(mode: 'default' | 'plan'): Promise<void> {
    const next = mode === 'plan' ? 'plan' : 'default'
    appStore.patchSettings({ collaborationMode: next })
    const thread = activeThread.value
    if (!thread) return
    patchActiveSessionPreferences(thread.model, thread.effort || '', thread.modelProvider, next)
    if (thread.id.startsWith('pending-thread-')) return
    try {
      // Persisting Default bumps CollabResetNonce so the next turn injects a
      // fresh "Plan Mode is now ended" developer message into Codex context.
      await updateSessionPreferences({
        sessionId: thread.id,
        model: thread.model || appStore.settings.model,
        effort: thread.effort || appStore.settings.effort,
        collaborationMode: next,
      })
    } catch {
      // Keep local mode usable even if persistence fails.
    }
  }

  function resolveThreadCollaborationMode(thread: ThreadSummary): CollaborationMode {
    const live = activeThread.value?.id === thread.id ? activeThread.value : null
    const mode = live?.collaborationMode
      || thread.collaborationMode
      || findThreadSummary(thread.id)?.collaborationMode
      || appStore.settings.collaborationMode
      || 'default'
    return mode === 'plan' ? 'plan' : 'default'
  }

  function bindPendingCollaborationMode(threadID: string, turnID: string): void {
    if (!threadID || !turnID) return
    const submission = pendingThreadSubmission(threadID)?.submission
    if (
      completedTurns.has(turnID)
      || (submission?.turnId && submission.turnId !== turnID)
      || (submission?.requestStarted && !submission.turnId && submission.previousTurnId === turnID)
    ) return
    const submittedMode = pendingCollaborationModeByThread.get(threadID)
    if (!submittedMode) return
    collaborationModeByTurn.set(turnID, submittedMode)
    pendingCollaborationModeByThread.delete(threadID)
  }

  function rememberPlanCandidate(threadID: string, turnID: string, item: TimelineItem): void {
    if (!threadID || !turnID) return
    // Official: final plan item from item/completed is authoritative.
    if (item.type === 'plan' && item.text.trim()) {
      pendingPlanByThread.set(threadID, { turnId: turnID, text: item.text.trim() })
      return
    }
    if (item.type === 'agentMessage') {
      const proposed = extractProposedPlan(item.text)
      if (proposed) pendingPlanByThread.set(threadID, { turnId: turnID, text: proposed })
    }
  }

  function rememberPlanCandidatesFromTurn(threadID: string, turnID: string): void {
    if (!threadID || !turnID) return
    for (const item of itemsByThread.value[threadID] ?? []) {
      if (item.turnId !== turnID) continue
      rememberPlanCandidate(threadID, turnID, item)
    }
  }

  function extractProposedPlan(text: string): string {
    if (!text) return ''
    const closed = text.match(/<proposed_plan>\s*([\s\S]*?)\s*<\/proposed_plan>/i)
    if (closed?.[1]?.trim()) return closed[1].trim()
    // Stream/race may leave an unclosed block; still treat as a plan candidate.
    const open = text.match(/<proposed_plan>\s*([\s\S]+)/i)
    return open?.[1]?.replace(/<\/proposed_plan>\s*$/i, '').trim() ?? ''
  }

  function resolvePendingPlan(threadID: string, turnID: string): { turnId: string; text: string } | null {
    const pending = pendingPlanByThread.get(threadID)
    if (pending && pending.turnId === turnID && pending.text.trim()) return pending

    const fromUpdate = sawPlanUpdateByTurn.get(turnID)
    if (fromUpdate?.trim()) return { turnId: turnID, text: fromUpdate.trim() }

    const items = [...(itemsByThread.value[threadID] ?? [])].reverse()
    for (const item of items) {
      if (item.turnId !== turnID) continue
      if (item.type === 'plan' && item.text.trim()) {
        return { turnId: turnID, text: item.text.trim() }
      }
      if (item.type === 'agentMessage') {
        const proposed = extractProposedPlan(item.text)
        if (proposed) return { turnId: turnID, text: proposed }
      }
    }
    return null
  }

  function clearPlanCandidates(threadID: string, turnID: string): void {
    sawPlanUpdateByTurn.delete(turnID)
    const pending = pendingPlanByThread.get(threadID)
    if (pending?.turnId === turnID) pendingPlanByThread.delete(threadID)
  }

  function clearPlanTurnTracking(threadID: string, turnID: string): void {
    clearPlanCandidates(threadID, turnID)
    collaborationModeByTurn.delete(turnID)
  }

  function clearPlanOfferRetries(key: string): void {
    const timers = planOfferRetryTimers.get(key)
    if (!timers) return
    for (const timer of timers) clearTrackedTimeout(timer)
    planOfferRetryTimers.delete(key)
  }

  function schedulePlanImplementationOffer(threadID: string, turnID: string): void {
    if (!threadID || !turnID) return
    const key = `${threadID}:${turnID}`
    clearPlanOfferRetries(key)
    maybeOfferPlanImplementation(threadID, turnID)
    // Late plan items / plan updates often arrive after turn/completed.
    const delays = [100, 350, 800]
    const timers = delays.map((delay, index) => trackedTimeout(() => {
      maybeOfferPlanImplementation(threadID, turnID)
      if (index === delays.length - 1) {
        clearPlanTurnTracking(threadID, turnID)
        planOfferRetryTimers.delete(key)
      }
    }, delay))
    planOfferRetryTimers.set(key, timers)
  }

  function maybeOfferPlanImplementation(threadID: string, turnID: string): void {
    if (!threadID || !turnID) return
    if (
      planImplementPrompt.value?.threadId === threadID
      && planImplementPrompt.value.turnId === turnID
    ) return
    // Official TUI suppresses when approvals/queue are pending.
    if (pendingRequests.value.length) return
    const queued = queuedMessagesByThread.value[threadID] ?? []
    if (queued.some((message) => message.state !== 'failed')) return

    const submittedMode = collaborationModeByTurn.get(turnID)
    if (submittedMode === 'default') {
      clearPlanCandidates(threadID, turnID)
      return
    }
    // Wait for turn/started or SendMessage to bind the exact submitted mode.
    if (submittedMode !== 'plan') return

    rememberPlanCandidatesFromTurn(threadID, turnID)
    const pending = resolvePendingPlan(threadID, turnID)
    if (!pending?.text.trim()) return

    planImplementPrompt.value = {
      threadId: threadID,
      turnId: turnID,
      planText: pending.text,
    }
    clearPlanTurnTracking(threadID, turnID)
  }

  function dismissPlanImplementation(): void {
    const prompt = planImplementPrompt.value
    if (prompt) clearPlanOfferRetries(`${prompt.threadId}:${prompt.turnId}`)
    planImplementPrompt.value = null
  }

  async function acceptPlanImplementation(): Promise<void> {
    const prompt = planImplementPrompt.value
    if (!prompt) return
    clearPlanOfferRetries(`${prompt.threadId}:${prompt.turnId}`)
    planImplementPrompt.value = null
    if (activeThreadId.value !== prompt.threadId) {
      await openThread(prompt.threadId)
    }
    // Official: SubmitUserMessageWithMode(default) + "Implement the plan."
    await setCollaborationMode('default')
    await sendMessage(translate('chat.implementPlanMessage'))
  }

  function normalizeThreadList(value: unknown): ThreadSummary[] {
    return normalizeThreads(value).map(withKnownThreadModel)
  }

  function normalizeRuntimeThread(threadValue: unknown, responseValue: unknown): ThreadSummary | null {
    const source = { ...asRecord(threadValue) }
    const response = asRecord(responseValue)
    const model = asString(response.model, asString(source.model))
    const provider = asString(response.modelProvider, asString(source.modelProvider))
    if (model) source.model = model
    if (provider) source.modelProvider = provider
    const thread = normalizeThread(source)
    if (!thread) return null
    rememberThreadModelIdentity(thread.id, thread.model, thread.modelProvider)
    return withKnownThreadModel(thread)
  }

  function withKnownThreadModel(thread: ThreadSummary): ThreadSummary {
    const known = threadModelIdentity[thread.id]
    if (!known) {
      rememberThreadModelIdentity(thread.id, thread.model, thread.modelProvider)
      return thread
    }
    return {
      ...thread,
      model: thread.model || known.model,
      modelProvider: thread.modelProvider || known.provider,
    }
  }

  function rememberThreadModelIdentity(threadID: string, model: string, provider: string): void {
    if (!threadID || (!model && !provider)) return
    const current = threadModelIdentity[threadID]
    const next = { model: model || current?.model || '', provider: provider || current?.provider || '' }
    if (current?.model === next.model && current.provider === next.provider) return
    threadModelIdentity[threadID] = next
    persistThreadModelIdentity(threadModelIdentity)
  }

  function updateThreadModelIdentity(threadID: string, model: string, provider: string): void {
    if (!threadID || (!model && !provider)) return
    rememberThreadModelIdentity(threadID, model, provider)
    const apply = (thread: ThreadSummary) => thread.id === threadID
      ? { ...thread, model: model || thread.model, modelProvider: provider || thread.modelProvider }
      : thread
    threads.value = threads.value.map(apply)
    const nextProjects = { ...projectThreads.value }
    for (const [path, projectItems] of Object.entries(nextProjects)) nextProjects[path] = projectItems.map(apply)
    projectThreads.value = nextProjects
    if (activeThread.value?.id === threadID) activeThread.value = apply(activeThread.value)
  }

  function setThreadMetrics(threadID: string, turns: unknown, merge = false): void {
    const historical = metricsFromTurns(turns)
    const existing = turnMetricsByThread.value[threadID] ?? {}
    for (const [turnID, metrics] of Object.entries(historical)) {
      if (existing[turnID]?.tokenUsage) metrics.tokenUsage = existing[turnID].tokenUsage
    }
    turnMetricsByThread.value = {
      ...turnMetricsByThread.value,
      [threadID]: merge ? { ...existing, ...historical } : historical,
    }
    const metrics = turnMetricsByThread.value[threadID] ?? {}
    const usageEntries = Object.values(metrics)
      .filter((item): item is TurnMetrics & { tokenUsage: TokenUsageBreakdown } => Boolean(item.tokenUsage))
    if (!usageEntries.length) return
    const total: TokenUsageBreakdown = usageEntries.reduce((sum, item) => ({
      inputTokens: sum.inputTokens + item.tokenUsage.inputTokens,
      cachedInputTokens: sum.cachedInputTokens + item.tokenUsage.cachedInputTokens,
      outputTokens: sum.outputTokens + item.tokenUsage.outputTokens,
      reasoningOutputTokens: sum.reasoningOutputTokens + item.tokenUsage.reasoningOutputTokens,
      totalTokens: sum.totalTokens + item.tokenUsage.totalTokens,
    }), {
      inputTokens: 0,
      cachedInputTokens: 0,
      outputTokens: 0,
      reasoningOutputTokens: 0,
      totalTokens: 0,
    })
    const last = [...usageEntries].sort((a, b) => (
      (a.completedAt ?? a.startedAt ?? 0) - (b.completedAt ?? b.startedAt ?? 0)
    )).at(-1)?.tokenUsage ?? usageEntries.at(-1)?.tokenUsage
    if (!last) return
    const previous = tokenUsageByThread.value[threadID]
    tokenUsageByThread.value = {
      ...tokenUsageByThread.value,
      [threadID]: {
        total,
        last,
        modelContextWindow: previous?.modelContextWindow ?? null,
      },
    }
  }

  function syncThreadContextWindow(threadID: string, rawThread: unknown): void {
    if (!threadID) return
    const thread = asRecord(rawThread)
    const usageValue = thread.tokenUsage ?? thread.token_usage ?? thread.usage
    const usage = normalizeThreadTokenUsage(usageValue)
    if (usage.modelContextWindow == null) return
    const current = tokenUsageByThread.value[threadID] ?? {
      total: { inputTokens: 0, cachedInputTokens: 0, outputTokens: 0, reasoningOutputTokens: 0, totalTokens: 0 },
      last: { inputTokens: 0, cachedInputTokens: 0, outputTokens: 0, reasoningOutputTokens: 0, totalTokens: 0 },
      modelContextWindow: null,
    }
    tokenUsageByThread.value = {
      ...tokenUsageByThread.value,
      [threadID]: { ...current, modelContextWindow: usage.modelContextWindow },
    }
  }

  function setThreadHistoryState(threadID: string, value: unknown): void {
    const page = asRecord(value)
    const start = Math.max(0, Number(page.historyStart) || 0)
    const total = Math.max(start, Number(page.historyTotal) || 0)
    const turnOffset = Math.max(0, Number(page.historyTurnOffset) || start)
    historyByThread.value = {
      ...historyByThread.value,
      [threadID]: {
        start,
        total,
        turnOffset,
        hasEarlier: page.hasEarlier === true || start > 0,
        loadingEarlier: false,
        loadedUpdatedAt: Number(asRecord(page.thread).updatedAt) || 0,
      },
    }
  }

  function patchThreadHistoryState(threadID: string, patch: Partial<ThreadHistoryState>): void {
    const current = historyByThread.value[threadID] ?? {
      start: 0,
      total: 0,
      turnOffset: 0,
      hasEarlier: false,
      loadingEarlier: false,
      loadedUpdatedAt: 0,
    }
    historyByThread.value = {
      ...historyByThread.value,
      [threadID]: { ...current, ...patch },
    }
  }

  function removeThreadHistoryState(threadID: string): void {
    if (!historyByThread.value[threadID]) return
    const next = { ...historyByThread.value }
    delete next[threadID]
    historyByThread.value = next
  }

  async function loadEarlierHistory(): Promise<boolean> {
    const threadID = activeThreadId.value
    const state = historyByThread.value[threadID]
    if (!threadID || !state?.hasEarlier || state.loadingEarlier) return false
    const before = state.start
    patchThreadHistoryState(threadID, { loadingEarlier: true })
    try {
      const response = await backend.ReadThreadHistory(threadID, before)
      const currentState = historyByThread.value[threadID]
      if (!currentState || currentState.start !== before) return false
      const page = asRecord(response)
      const turns = page.turns
      const earlierItems = timelineFromTurns(turns)
      const currentItems = itemsByThread.value[threadID] ?? []
      const existingIDs = new Set(currentItems.map((item) => item.id).filter(Boolean))
      const prefix = earlierItems.filter((item) => !item.id || !existingIDs.has(item.id))
      if (prefix.length) {
        itemsByThread.value = {
          ...itemsByThread.value,
          [threadID]: [...prefix, ...currentItems],
        }
      }
      setThreadMetrics(threadID, turns, true)
      setThreadHistoryState(threadID, page)
      return prefix.length > 0 || (Number(page.historyStart) || 0) < before
    } catch (error) {
      if (activeThreadId.value === threadID) {
        notify('error', translate('notifications.taskOpenFailed'), errorMessage(error))
      }
      return false
    } finally {
      if (historyByThread.value[threadID]?.loadingEarlier) {
        patchThreadHistoryState(threadID, { loadingEarlier: false })
      }
    }
  }

  function patchTurnMetrics(threadID: string, turnID: string, patch: Partial<TurnMetrics>): void {
    if (!threadID || !turnID) return
    const threadMetrics = { ...(turnMetricsByThread.value[threadID] ?? {}) }
    const current = threadMetrics[turnID] ?? emptyTurnMetrics()
    threadMetrics[turnID] = {
      tokenUsage: patch.tokenUsage !== undefined ? patch.tokenUsage : current.tokenUsage,
      startedAt: patch.startedAt !== undefined ? patch.startedAt : current.startedAt,
      completedAt: patch.completedAt !== undefined ? patch.completedAt : current.completedAt,
      durationMs: patch.durationMs !== undefined ? patch.durationMs : current.durationMs,
    }
    turnMetricsByThread.value = { ...turnMetricsByThread.value, [threadID]: threadMetrics }
  }

  function rememberLoadedThread(threadID: string): void {
    loadedThreadIDs.delete(threadID)
    loadedThreadIDs.add(threadID)
    while (loadedThreadIDs.size > 12 || cachedConversationWeight() > 64_000_000) {
      const recentlyOpened = [...loadedThreadIDs].slice(-2)
      const evicted = [...loadedThreadIDs].find((id) =>
        id !== threadID
        && !recentlyOpened.includes(id)
        && id !== activeThreadId.value
        && !threadIsBusy(id)
        && !(queuedMessagesByThread.value[id] ?? []).length,
      )
      if (!evicted) break
      evictCachedThread(evicted)
    }
  }

  function evictCachedThread(threadID: string): void {
    loadedThreadIDs.delete(threadID)
    const nextItems = { ...itemsByThread.value }
    const nextUsage = { ...tokenUsageByThread.value }
    const nextDiffs = { ...latestDiffByThread.value }
    const nextMetrics = { ...turnMetricsByThread.value }
    const nextByTurn = { ...diffsByTurn.value }
    delete nextItems[threadID]
    delete nextUsage[threadID]
    delete nextDiffs[threadID]
    delete nextMetrics[threadID]
    for (const [turnID, owner] of [...diffTurnOwners.entries()]) {
      if (owner !== threadID) continue
      delete nextByTurn[turnID]
      diffTurnOwners.delete(turnID)
    }
    for (const [key, pending] of [...pendingDiffs.entries()]) {
      if (pending.threadId === threadID) pendingDiffs.delete(key)
    }
    itemsByThread.value = nextItems
    removeThreadHistoryState(threadID)
    tokenUsageByThread.value = nextUsage
    latestDiffByThread.value = nextDiffs
    turnMetricsByThread.value = nextMetrics
    diffsByTurn.value = nextByTurn
  }

  function cachedConversationWeight(): number {
    let total = 0
    for (const items of Object.values(itemsByThread.value)) {
      for (const item of items) {
        total += item.text.length + item.output.length + item.detail.length + item.command.length
        total += (item.reasoningSummary?.length ?? 0) + (item.reasoningContent?.length ?? 0)
        total += item.changes.reduce((sum, change) => sum + change.path.length + change.diff.length, 0)
      }
    }
    return total
  }

  function addOrUpdateThread(thread: ThreadSummary): void {
    const path = thread.cwd || appStore.currentWorkspacePath
    const currentItems = sameWorkspace(path, appStore.currentWorkspacePath)
      ? threads.value
      : projectThreadsForPath(path) ?? []
    const remaining = currentItems.filter((item) => item.id !== thread.id)
    const nextItems = [thread, ...remaining].sort((a, b) => b.updatedAt - a.updatedAt)
    setProjectThreads(path, nextItems)
    if (sameWorkspace(path, appStore.currentWorkspacePath)) threads.value = nextItems
  }

  function setProjectThreads(path: string, nextThreads: ThreadSummary[]): void {
    const existingPath = Object.keys(projectThreads.value).find((projectPath) => sameWorkspace(projectPath, path))
    const key = existingPath ?? path
    projectThreads.value = { ...projectThreads.value, [key]: nextThreads }
  }

  function isThreadSubmitting(threadID: string): boolean {
    return sendingThreadIds.value.some((id) => sameThreadSession(id, threadID))
  }

  function setThreadSubmitting(threadID: string, submitting: boolean): void {
    if (!threadID) return
    if (submitting) {
      if (!isThreadSubmitting(threadID)) sendingThreadIds.value = [...sendingThreadIds.value, threadID]
      return
    }
    sendingThreadIds.value = sendingThreadIds.value.filter((id) => !sameThreadSession(id, threadID))
  }

  function pendingThreadSubmission(threadID: string): {
    threadID: string
    submission: PendingThreadSubmission
  } | null {
    if (!threadID) return null
    const direct = pendingSubmissionByThread.get(threadID)
    if (direct) return { threadID, submission: direct }
    for (const [id, submission] of pendingSubmissionByThread) {
      if (sameThreadSession(id, threadID)) return { threadID: id, submission }
    }
    return null
  }

  function isPendingThreadSubmission(threadID: string, submission: PendingThreadSubmission): boolean {
    return pendingThreadSubmission(threadID)?.submission === submission
  }

  function pendingThreadSubmissionOwner(submission: PendingThreadSubmission): {
    threadID: string
    submission: PendingThreadSubmission
  } | null {
    for (const [threadID, current] of pendingSubmissionByThread) {
      if (current === submission) return { threadID, submission: current }
    }
    return null
  }

  function beginPendingThreadSubmission(threadID: string, messageID: string): PendingThreadSubmission | null {
    if (!threadID || !messageID || pendingThreadSubmission(threadID)) return null
    const submission: PendingThreadSubmission = {
      blockerId: `pending-codex-turn-${messageID}`,
      messageId: messageID,
      previousTurnId: '',
      requestStarted: false,
      turnId: '',
    }
    pendingSubmissionByThread.set(threadID, submission)
    const messages = queuedMessagesByThread.value[threadID]
    const headIndex = messages?.findIndex((message) => message.id === messageID) ?? -1
    if (messages && headIndex >= 0) {
      let changed = false
      const nextMessages = messages.map((message, index) => {
        if (
          index <= headIndex
          || (message.blockedByTurnId && !completedTurns.has(message.blockedByTurnId))
        ) return message
        changed = true
        return { ...message, blockedByTurnId: submission.blockerId }
      })
      if (changed) {
        queuedMessagesByThread.value = {
          ...queuedMessagesByThread.value,
          [threadID]: nextMessages,
        }
      }
    }
    return submission
  }

  function replaceQueuedBlockingTurn(threadID: string, fromTurnID: string, toTurnID = ''): void {
    if (!threadID || !fromTurnID || fromTurnID === toTurnID) return
    let changed = false
    const nextQueues = { ...queuedMessagesByThread.value }
    for (const [id, messages] of Object.entries(nextQueues)) {
      if (id !== threadID && !sameThreadSession(id, threadID)) continue
      let listChanged = false
      const nextMessages = messages.map((message) => {
        if (message.blockedByTurnId !== fromTurnID) return message
        listChanged = true
        return { ...message, blockedByTurnId: toTurnID || undefined }
      })
      if (listChanged) {
        changed = true
        nextQueues[id] = nextMessages
      }
    }
    if (changed) queuedMessagesByThread.value = nextQueues
  }

  function bindPendingThreadSubmission(
    threadID: string,
    turnID: string,
    authoritative = false,
    expectedSubmission?: PendingThreadSubmission,
  ): PendingThreadSubmission | null {
    if (!threadID || !turnID) return null
    const pending = pendingThreadSubmission(threadID)
    if (!pending) {
      return expectedSubmission?.turnId === turnID ? expectedSubmission : null
    }
    if (expectedSubmission && pending.submission !== expectedSubmission) {
      return expectedSubmission.turnId === turnID ? expectedSubmission : null
    }
    if (!pending.submission.requestStarted) return null
    if (!authoritative && completedTurns.has(turnID)) return null
    if (!authoritative && !pending.submission.turnId && pending.submission.previousTurnId === turnID) return null
    if (pending.submission.turnId && pending.submission.turnId !== turnID) return null
    pending.submission.turnId = turnID
    replaceQueuedBlockingTurn(pending.threadID, pending.submission.blockerId, turnID)
    return pending.submission
  }

  function finishPendingThreadSubmission(
    threadID: string,
    submission: PendingThreadSubmission,
    accepted: boolean,
  ): void {
    const ownerIDs: string[] = []
    for (const [id, current] of [...pendingSubmissionByThread.entries()]) {
      if (current !== submission) continue
      ownerIDs.push(id)
      pendingSubmissionByThread.delete(id)
    }
    if (!ownerIDs.length) return
    const ownerID = ownerIDs[0]!
    if (!accepted) replaceQueuedBlockingTurn(ownerID, submission.blockerId)
    const replacement = pendingThreadSubmission(threadID) ?? pendingThreadSubmission(ownerID)
    if (replacement && replacement.submission !== submission) return
    sendingThreadIds.value = sendingThreadIds.value.filter((id) =>
      id !== threadID
      && !ownerIDs.includes(id)
      && !sameThreadSession(id, ownerID),
    )
    if (!accepted) scheduleThreadQueueDrain(ownerID)
  }

  function settleAcceptedPendingThreadSubmission(threadID: string, turnID: string): void {
    const pending = pendingThreadSubmission(threadID)
    const submission = bindPendingThreadSubmission(threadID, turnID)
    if (!pending || !submission) return
    for (const id of Object.keys(queuedMessagesByThread.value)) {
      if (id === pending.threadID || id === threadID || sameThreadSession(id, threadID)) {
        removeQueuedMessageFromThread(id, submission.messageId)
      }
    }
    finishPendingThreadSubmission(pending.threadID, submission, true)
  }

  function migratePendingThreadSubmission(fromID: string, toID: string): void {
    if (!fromID || !toID || fromID === toID) return
    const submission = pendingSubmissionByThread.get(fromID)
    if (!submission) return
    const target = pendingSubmissionByThread.get(toID)
    if (target && target !== submission) return
    pendingSubmissionByThread.delete(fromID)
    pendingSubmissionByThread.set(toID, submission)
  }

  function releasePendingThreadSubmissions(threadID = ''): void {
    const pending = [...pendingSubmissionByThread.entries()].filter(([id]) =>
      !threadID || id === threadID || sameThreadSession(id, threadID),
    )
    for (const [id, submission] of pending) finishPendingThreadSubmission(id, submission, false)
    for (const id of [...pendingCollaborationModeByThread.keys()]) {
      if (!threadID || id === threadID || sameThreadSession(id, threadID)) {
        pendingCollaborationModeByThread.delete(id)
      }
    }
  }

  function activeTurnIDFromSnapshot(threadValue: unknown): string {
    const rawThread = asRecord(threadValue)
    for (const turnValue of [...asArray(rawThread.turns)].reverse()) {
      const turn = asRecord(turnValue)
      const turnID = asString(turn.id)
      if (turnID && isActiveStatus(turn.status)) return turnID
    }
    return ''
  }

  function feedbackIsBusy(feedback: TurnFeedback | null | undefined): boolean {
    return feedback?.state === 'submitting'
      || feedback?.state === 'running'
      || feedback?.state === 'retrying'
  }

  function liveFeedbackTurnID(feedback: TurnFeedback | null | undefined): string {
    return feedbackIsBusy(feedback)
      && feedback?.turnId
      && !completedTurns.has(feedback.turnId)
      ? feedback.turnId
      : ''
  }

  /** Read live state through local/backend thread aliases created by resume/read. */
  function trackedThreadTurnIDs(threadID: string): string[] {
    if (!threadID) return []
    const direct = activeTurnByThread.value[threadID] ?? ''
    const aliases = Object.entries(activeTurnByThread.value)
      .filter(([id]) => id !== threadID && sameThreadSession(id, threadID))
      .map(([, turnID]) => turnID)
      .filter(Boolean)
    return [direct, ...aliases].filter(Boolean)
  }

  function trackedThreadTurnID(threadID: string): string {
    return trackedThreadTurnIDs(threadID)[0] ?? ''
  }

  function threadTurnID(threadID: string): string {
    return trackedThreadTurnIDs(threadID).find((turnID) => !completedTurns.has(turnID)) ?? ''
  }

  function threadFeedback(threadID: string): TurnFeedback | null {
    if (!threadID) return null
    const direct = turnFeedbackByThread.value[threadID]
    const aliases = Object.entries(turnFeedbackByThread.value)
      .filter(([id]) => id !== threadID && sameThreadSession(id, threadID))
      .map(([, feedback]) => feedback)
    if (liveFeedbackTurnID(direct)) return direct ?? null
    const liveAlias = aliases.find((feedback) => Boolean(liveFeedbackTurnID(feedback)))
    if (liveAlias) return liveAlias
    if (direct && feedbackIsBusy(direct) && !direct.turnId) return direct
    return aliases.find((feedback) => feedbackIsBusy(feedback) && !feedback.turnId)
      ?? direct
      ?? aliases[0]
      ?? null
  }

  function threadReportsActive(threadID: string): boolean {
    if (!threadID) return false
    if (
      activeThread.value
      && (activeThread.value.id === threadID || sameThreadSession(activeThread.value.id, threadID))
      && activeThread.value.status
    ) {
      return isActiveStatus(activeThread.value.status)
    }
    const exact = findThreadSummary(threadID)
    if (exact?.status) return isActiveStatus(exact.status)
    return [
      ...threads.value,
      ...Object.values(projectThreads.value).flat(),
    ].some((thread) => sameThreadSession(thread.id, threadID) && isActiveStatus(thread.status))
  }

  function threadIsRunning(threadID: string): boolean {
    const turnID = threadTurnID(threadID)
    return Boolean(turnID && !completedTurns.has(turnID))
  }

  /**
   * Only items belonging to a *live* turn block the queue.
   * After interrupt/reconnect, leftover inProgress rows used to keep the queue
   * stuck forever even though no turn was running.
   */
  function threadHasLiveItems(threadID: string): boolean {
    const liveTurn = threadTurnID(threadID)
    if (!liveTurn) return false
    const items = Object.entries(itemsByThread.value)
      .filter(([id]) => id === threadID || sameThreadSession(id, threadID))
      .flatMap(([, threadItems]) => threadItems)
    return items.some((item) => {
      if (item.type === 'userMessage') return false
      if (!isActiveStatus(item.status)) return false
      if (item.turnId && completedTurns.has(item.turnId)) return false
      // Belong to another finished/other turn — ignore.
      if (item.turnId && item.turnId !== liveTurn) return false
      return true
    })
  }

  function threadHasLoadingBarrier(threadID: string): boolean {
    const matches = (id: string) => id === threadID || sameThreadSession(id, threadID)
    return [...loadingSequenceByThread.keys()].some(matches)
      || [...workspaceSelectionSequenceByThread.keys()].some(matches)
  }

  /** True while a turn is live OR a send/steer is in flight — used to keep follow-ups queued. */
  function threadIsBusy(threadID: string): boolean {
    if (!threadID) return false
    // Reading history is a dispatch barrier: queue now, then drain after
    // openThread finishes so the disk snapshot cannot overwrite the new turn.
    if (threadHasLoadingBarrier(threadID)) return true
    // Keep ownership between turn/start RPC acceptance and the later
    // turn/started event. Releasing here creates a false-idle dispatch window.
    if (pendingThreadSubmission(threadID)) return true
    if (threadIsRunning(threadID) || isThreadSubmitting(threadID)) return true
    const feedback = threadFeedback(threadID)
    if (feedbackIsBusy(feedback)) {
      // Stale feedback after turn/completed used to park the queue forever.
      if (!(feedback?.turnId && completedTurns.has(feedback.turnId))) return true
    }
    // Approval / user-input prompts mean the turn is still blocking the queue.
    if (pendingRequests.value.some((request) => sameThreadSession(asString(request.data.threadId), threadID))) return true
    if (threadHasLiveItems(threadID)) return true
    // Server status is the final ownership signal when turn/started and the RPC
    // response are briefly out of sync. Explicit terminal paths set it back idle.
    return threadReportsActive(threadID)
  }

  /**
   * After disconnect/reconnect or a stalled interrupt, in-flight queue rows and
   * submitting flags can be orphaned. Reset them so drain can run again.
   */
  function resetOrphanedInFlightSends(threadID = ''): void {
    if (threadID) {
      releasePendingThreadSubmissions(threadID)
      if (isThreadSubmitting(threadID)) setThreadSubmitting(threadID, false)
      const messages = queuedMessagesByThread.value[threadID]
      if (!messages?.some((message) => message.state === 'sending')) return
      queuedMessagesByThread.value = {
        ...queuedMessagesByThread.value,
        [threadID]: messages.map((message) =>
          message.state === 'sending' ? { ...message, state: 'queued', error: '' } : message,
        ),
      }
      return
    }
    releasePendingThreadSubmissions()
    if (sendingThreadIds.value.length) sendingThreadIds.value = []
    let touched = false
    const next: Record<string, QueuedMessage[]> = { ...queuedMessagesByThread.value }
    for (const [id, messages] of Object.entries(next)) {
      if (!messages.some((message) => message.state === 'sending')) continue
      touched = true
      next[id] = messages.map((message) =>
        message.state === 'sending' ? { ...message, state: 'queued', error: '' } : message,
      )
    }
    if (touched) queuedMessagesByThread.value = next
  }

  function releaseDisconnectedTurns(message = ''): void {
    const disconnectedTurns = new Map(Object.entries(activeTurnByThread.value))
    for (const [threadID, feedback] of Object.entries(turnFeedbackByThread.value)) {
      if (feedbackIsBusy(feedback) && !disconnectedTurns.has(threadID)) {
        disconnectedTurns.set(threadID, feedback.turnId)
      }
    }
    for (const threadID of sendingThreadIds.value) {
      if (!disconnectedTurns.has(threadID)) disconnectedTurns.set(threadID, '')
    }
    activeTurnByThread.value = {}
    for (const [threadID, turnID] of disconnectedTurns) {
      if (turnID) rememberCompletedTurn(turnID, message ? 'failed' : 'interrupted')
      finalizeActiveItemsForCompletedTurns(threadID, turnID)
      finalizeOrphanedActiveItems(threadID)
      setLocalThreadStatus(threadID, 'idle')
      if (message) {
        setTurnFeedback(threadID, { state: 'failed', message, turnId: turnID })
      } else {
        clearTurnFeedback(threadID)
      }
    }
    for (const threadID of Object.keys(itemsByThread.value)) finalizeOrphanedActiveItems(threadID)
    pendingRequests.value = []
    interruptingTurn.value = false
    resetOrphanedInFlightSends()
  }

  function scheduleIdleThreadReconcile(threadID: string, turnID: string): void {
    const previous = idleReconcileTimers.get(threadID)
    if (previous) clearTrackedTimeout(previous)
    const timer = trackedTimeout(() => {
      idleReconcileTimers.delete(threadID)
      void reconcileIdleThread(threadID, turnID)
    }, 150)
    idleReconcileTimers.set(threadID, timer)
  }

  async function reconcileIdleThread(threadID: string, turnID: string): Promise<void> {
    if (!turnID || completedTurns.has(turnID) || threadTurnID(threadID) !== turnID) {
      scheduleThreadQueueDrain(threadID)
      return
    }

    let terminalStatus = ''
    try {
      const response = await backend.ReadThread(threadID)
      const rawThread = asRecord(asRecord(response).thread)
      const turns = [...asArray(rawThread.turns)].reverse().map(asRecord)
      const matchingTurn = turns.find((turn) => asString(turn.id) === turnID)
      const matchingStatus = normalizeThreadStatus(matchingTurn?.status)
      const runningTurn = turns.find((turn) => isActiveStatus(turn.status))
      const runningTurnID = asString(runningTurn?.id)
      if (runningTurnID) {
        if (runningTurnID !== turnID) {
          rememberCompletedTurn(turnID, isTerminalTurnStatus(matchingStatus) ? matchingStatus : 'completed')
          replaceQueuedBlockingTurn(threadID, turnID, runningTurnID)
        }
        setThreadTurn(threadID, runningTurnID)
        setLocalThreadStatus(threadID, 'active')
        setTurnFeedback(threadID, { state: 'running', message: '', turnId: runningTurnID })
        return
      }
      if (isActiveStatus(normalizeThreadStatus(rawThread.status))) {
        // An active thread with no turn snapshot is still owned by the server.
        setThreadTurn(threadID, turnID)
        setLocalThreadStatus(threadID, 'active')
        setTurnFeedback(threadID, { state: 'running', message: '', turnId: turnID })
        return
      }
      terminalStatus = isTerminalTurnStatus(matchingStatus) ? matchingStatus : 'completed'
    } catch {
      // A failed/read-fallback snapshot is not proof that a live turn ended.
      // Keep follow-ups queued until turn/completed or an explicit terminal snapshot.
      return
    }

    // A newer dispatch may have claimed the thread while ReadThread was in flight.
    // Its submitting feedback and queue row must not be cleared by the older idle event.
    if (pendingThreadSubmission(threadID) || isThreadSubmitting(threadID)) return

    const currentTurnID = threadTurnID(threadID)
    if (currentTurnID && currentTurnID !== turnID && !completedTurns.has(currentTurnID)) {
      rememberCompletedTurn(turnID, terminalStatus)
      replaceQueuedBlockingTurn(threadID, turnID, currentTurnID)
      return
    }
    const feedbackTurnID = liveFeedbackTurnID(threadFeedback(threadID))
    if (feedbackTurnID && feedbackTurnID !== turnID && !completedTurns.has(feedbackTurnID)) {
      rememberCompletedTurn(turnID, terminalStatus)
      setThreadTurn(threadID, feedbackTurnID)
      replaceQueuedBlockingTurn(threadID, turnID, feedbackTurnID)
      return
    }
    if (completedTurns.has(turnID)) {
      scheduleThreadQueueDrain(threadID)
      return
    }
    rememberCompletedTurn(turnID, terminalStatus)
    finalizeActiveItemsForCompletedTurns(threadID, turnID)
    if (currentTurnID === turnID) setThreadTurn(threadID, '')
    finalizeOrphanedActiveItems(threadID)
    clearTurnFeedback(threadID)
    setLocalThreadStatus(threadID, 'idle')
    clearStaleBusyState(threadID)
    scheduleThreadQueueDrain(threadID)
  }

  /** Clear local busy markers that outlive a finished turn. */
  function clearStaleBusyState(threadID: string): void {
    if (!threadID) return
    const pendingSubmission = pendingThreadSubmission(threadID)
    const feedback = threadFeedback(threadID)
    let liveTurnID = threadTurnID(threadID)
    if (!liveTurnID && liveFeedbackTurnID(feedback)) {
      // Feedback with an unfinished real turn is ownership evidence, not a stale flag.
      const feedbackTurnID = liveFeedbackTurnID(feedback)
      setThreadTurn(threadID, feedbackTurnID)
      liveTurnID = feedbackTurnID
    }
    const trackedTurnID = trackedThreadTurnID(threadID)
    if (!liveTurnID && trackedTurnID && completedTurns.has(trackedTurnID)) {
      setThreadTurn(threadID, '')
    }
    finalizeActiveItemsForCompletedTurns(threadID)
    // No live turn → leftover inProgress rows are orphans (interrupt lag / missed item/completed).
    // Do NOT clear isThreadSubmitting here — drainThreadQueue owns that flag while send is in flight.
    if (!threadIsRunning(threadID) && !pendingSubmission && !threadReportsActive(threadID)) {
      finalizeOrphanedActiveItems(threadID)
      // Only reclaim "sending" queue rows when no drain owns the thread.
      if (!isThreadSubmitting(threadID)) resetOrphanedInFlightSends(threadID)
    }
    const currentFeedback = threadFeedback(threadID)
    if (feedbackIsBusy(currentFeedback)) {
      // Keep "submitting" feedback while drainThreadQueue owns the in-flight send.
      const drainOwnsFeedback = currentFeedback?.state === 'submitting'
        && (isThreadSubmitting(threadID) || Boolean(pendingSubmission))
      const feedbackTurnCompleted = Boolean(
        currentFeedback?.turnId && completedTurns.has(currentFeedback.turnId),
      )
      if (
        !drainOwnsFeedback
        && (
          feedbackTurnCompleted
          || (!threadIsRunning(threadID) && !threadReportsActive(threadID))
        )
      ) {
        clearTurnFeedback(threadID)
      }
    }
    if (
      !threadIsRunning(threadID)
      && !isThreadSubmitting(threadID)
      && !pendingSubmission
      && !threadReportsActive(threadID)
    ) {
      setLocalThreadStatus(threadID, 'idle')
    }
  }

  function finalizeActiveItemsForCompletedTurns(threadID: string, turnID = ''): void {
    const items = itemsByThread.value[threadID]
    if (!items?.length) return
    let changed = false
    const next = items.map((item) => {
      if (item.type === 'userMessage' || !isActiveStatus(item.status)) return item
      const itemTurn = item.turnId || ''
      const shouldFinalize = (turnID && itemTurn === turnID)
        || (itemTurn && completedTurns.has(itemTurn))
      if (!shouldFinalize) return item
      changed = true
      return {
        ...item,
        status: terminalItemStatus(itemTurn || turnID),
        completedAt: item.completedAt ?? Date.now(),
      }
    })
    if (changed) itemsByThread.value = { ...itemsByThread.value, [threadID]: next }
  }

  /** Mark leftover inProgress/running rows completed when no turn is live. */
  function finalizeOrphanedActiveItems(threadID: string): void {
    if (!threadID || threadIsRunning(threadID)) return
    const items = itemsByThread.value[threadID]
    if (!items?.length) return
    let changed = false
    const next = items.map((item) => {
      if (item.type === 'userMessage' || !isActiveStatus(item.status)) return item
      if (item.turnId && !completedTurns.has(item.turnId)) rememberCompletedTurn(item.turnId, 'completed')
      changed = true
      return {
        ...item,
        status: terminalItemStatus(item.turnId || ''),
        completedAt: item.completedAt ?? Date.now(),
      }
    })
    if (changed) itemsByThread.value = { ...itemsByThread.value, [threadID]: next }
  }

  function rememberCompletedTurn(turnID: string, status: unknown = 'completed'): void {
    if (!turnID) return
    completedTurns.delete(turnID)
    completedTurns.add(turnID)
    completedTurnStatus.set(
      turnID,
      isFailedStatus(status) ? 'failed' : isInterruptedStatus(status) ? 'interrupted' : 'completed',
    )
    while (completedTurns.size > 2048) {
      const oldest = completedTurns.values().next().value
      if (!oldest) break
      completedTurns.delete(oldest)
      completedTurnStatus.delete(oldest)
    }
  }

  function terminalItemStatus(turnID: string): string {
    return completedTurnStatus.get(turnID) || 'completed'
  }

  function setReportedThreadStatus(threadID: string, status: string): void {
    if (!threadID || !status) return
    const matchesThread = (id: string) => id === threadID || sameThreadSession(id, threadID)
    let touched = false
    const nextThreads = threads.value.map((thread) => {
      if (!matchesThread(thread.id) || thread.status === status) return thread
      touched = true
      return { ...thread, status }
    })
    if (touched) threads.value = nextThreads
    if (activeThread.value && matchesThread(activeThread.value.id) && activeThread.value.status !== status) {
      activeThread.value = { ...activeThread.value, status }
    }
    const nextProjects = { ...projectThreads.value }
    let projectTouched = false
    for (const [path, list] of Object.entries(nextProjects)) {
      let listTouched = false
      const mapped = list.map((thread) => {
        if (!matchesThread(thread.id) || thread.status === status) return thread
        listTouched = true
        return { ...thread, status }
      })
      if (listTouched) {
        nextProjects[path] = mapped
        projectTouched = true
      }
    }
    if (projectTouched) projectThreads.value = nextProjects
  }

  function setLocalThreadStatus(threadID: string, status: string): void {
    if (!threadID || !status) return
    // Only force idle when we know the turn is gone — avoids clobbering a real active turn.
    if (status === 'idle' && threadIsRunning(threadID)) return
    setReportedThreadStatus(threadID, status)
  }

  function rememberThreadAlias(fromID: string, toID: string): void {
    const from = fromID.trim()
    const to = toID.trim()
    if (!from || !to || from === to) return
    threadAlias.set(from, to)
    threadAlias.set(to, to)
  }

  function sameThreadSession(left: string, right: string): boolean {
    if (!left || !right) return false
    const resolve = (id: string) => {
      const seen = new Set<string>()
      let current = id
      while (!seen.has(current)) {
        seen.add(current)
        const next = threadAlias.get(current)
        if (!next || next === current) break
        current = next
      }
      return current
    }
    return resolve(left) === resolve(right)
  }

  function clearThreadAliases(threadID: string): void {
    const related = [...threadAlias.keys()].filter((id) => sameThreadSession(id, threadID))
    for (const id of related) threadAlias.delete(id)
  }

  function migrateThreadMapEntry<T>(entries: Map<string, T>, fromID: string, toID: string): void {
    const value = entries.get(fromID)
    if (value === undefined) return
    entries.delete(fromID)
    if (!entries.has(toID)) entries.set(toID, value)
  }

  /** Move queued messages when ReadThread remaps a thread/session id. */
  function migrateQueueThreadKey(fromID: string, toID: string): void {
    if (!fromID || !toID || fromID === toID) return
    rememberThreadAlias(fromID, toID)
    migratePendingThreadSubmission(fromID, toID)
    migrateThreadMapEntry(latestStartedTurnByThread, fromID, toID)
    migrateThreadMapEntry(pendingCollaborationModeByThread, fromID, toID)
    const pending = queuedMessagesByThread.value[fromID]
    if (pending?.length) {
      const nextQueues = { ...queuedMessagesByThread.value }
      delete nextQueues[fromID]
      nextQueues[toID] = [
        ...(nextQueues[toID] ?? []),
        ...pending.map((message) => ({ ...message, threadId: toID })),
      ]
      queuedMessagesByThread.value = nextQueues
    }

    const fromTurn = activeTurnByThread.value[fromID]
    if (fromTurn) {
      const nextTurns = { ...activeTurnByThread.value }
      delete nextTurns[fromID]
      if (!nextTurns[toID]) nextTurns[toID] = fromTurn
      activeTurnByThread.value = nextTurns
    }
    const fromFeedback = turnFeedbackByThread.value[fromID]
    if (fromFeedback) {
      const nextFeedback = { ...turnFeedbackByThread.value }
      delete nextFeedback[fromID]
      if (!nextFeedback[toID]) nextFeedback[toID] = fromFeedback
      turnFeedbackByThread.value = nextFeedback
    }
    if (sendingThreadIds.value.includes(fromID)) {
      sendingThreadIds.value = [...new Set(sendingThreadIds.value.map((id) => id === fromID ? toID : id))]
    }
    const history = historyByThread.value[fromID]
    if (history) {
      const nextHistory = { ...historyByThread.value }
      delete nextHistory[fromID]
      if (!nextHistory[toID]) nextHistory[toID] = history
      historyByThread.value = nextHistory
    }
  }

  function scheduleThreadQueueDrain(threadID: string): void {
    if (!threadID) return
    trackedTimeout(() => {
      // Reclaim only when neither the server nor a dispatch owns this thread.
      if (
        !threadReportsActive(threadID)
        && !threadHasLoadingBarrier(threadID)
        && !pendingThreadSubmission(threadID)
        && !isThreadSubmitting(threadID)
      ) {
        clearStaleBusyState(threadID)
      }
      void drainThreadQueue(threadID)
    }, 0)
  }

  function drainAvailableThreadQueues(): void {
    for (const threadID of Object.keys(queuedMessagesByThread.value)) scheduleThreadQueueDrain(threadID)
  }

  function patchQueuedMessage(threadID: string, messageID: string, patch: Partial<QueuedMessage>): void {
    const messages = queuedMessagesByThread.value[threadID]
    if (!messages?.some((message) => message.id === messageID)) return
    queuedMessagesByThread.value = {
      ...queuedMessagesByThread.value,
      [threadID]: messages.map((message) => message.id === messageID ? { ...message, ...patch } : message),
    }
  }

  function removeQueuedMessageFromThread(threadID: string, messageID: string): void {
    const messages = queuedMessagesByThread.value[threadID]
    if (!messages?.some((message) => message.id === messageID)) return
    const remaining = messages.filter((message) => message.id !== messageID)
    const next = { ...queuedMessagesByThread.value }
    if (remaining.length) next[threadID] = remaining
    else delete next[threadID]
    queuedMessagesByThread.value = next
  }

  function clearThreadQueue(threadID: string): void {
    if (!queuedMessagesByThread.value[threadID]) return
    const next = { ...queuedMessagesByThread.value }
    delete next[threadID]
    queuedMessagesByThread.value = next
  }

  function appendItem(threadID: string, item: TimelineItem): void {
    itemsByThread.value = {
      ...itemsByThread.value,
      [threadID]: [...(itemsByThread.value[threadID] ?? []), item],
    }
  }

  function replaceItem(threadID: string, item: TimelineItem): void {
    const items = [...(itemsByThread.value[threadID] ?? [])]
    const index = items.findIndex((candidate) => candidate.id === item.id)
    if (index >= 0) items[index] = item
    else items.push(item)
    itemsByThread.value = { ...itemsByThread.value, [threadID]: items }
  }

  function migratePendingThread(pendingThreadID: string, threadID: string): void {
    if (!pendingThreadID || pendingThreadID === threadID) return
    rememberThreadAlias(pendingThreadID, threadID)
    migratePendingThreadSubmission(pendingThreadID, threadID)
    migrateThreadMapEntry(latestStartedTurnByThread, pendingThreadID, threadID)
    migrateThreadMapEntry(pendingCollaborationModeByThread, pendingThreadID, threadID)
    const nextItems = { ...itemsByThread.value, [threadID]: itemsByThread.value[pendingThreadID] ?? [] }
    delete nextItems[pendingThreadID]
    itemsByThread.value = nextItems

    const feedback = turnFeedbackByThread.value[pendingThreadID]
    const nextFeedback = { ...turnFeedbackByThread.value }
    delete nextFeedback[pendingThreadID]
    if (feedback) nextFeedback[threadID] = feedback
    turnFeedbackByThread.value = nextFeedback

    const pendingMessages = queuedMessagesByThread.value[pendingThreadID] ?? []
    const nextQueues = { ...queuedMessagesByThread.value }
    delete nextQueues[pendingThreadID]
    if (pendingMessages.length) {
      nextQueues[threadID] = [
        ...(nextQueues[threadID] ?? []),
        ...pendingMessages.map((message) => ({ ...message, threadId: threadID })),
      ]
    }
    queuedMessagesByThread.value = nextQueues
    sendingThreadIds.value = [...new Set(sendingThreadIds.value.map((id) => id === pendingThreadID ? threadID : id))]
    if (activeThreadId.value === pendingThreadID) activeThreadId.value = threadID
    loadedThreadIDs.delete(pendingThreadID)
    loadedThreadIDs.add(threadID)

    const replacePending = (list: ThreadSummary[]) => {
      const pending = list.find((thread) => thread.id === pendingThreadID)
      const withoutPending = list.filter((thread) => thread.id !== pendingThreadID && thread.id !== threadID)
      if (!pending) return list
      return [{ ...pending, id: threadID }, ...withoutPending]
    }
    threads.value = replacePending(threads.value)
    const path = appStore.currentWorkspacePath
    if (path) {
      const projectItems = projectThreadsForPath(path)
      if (projectItems) setProjectThreads(path, replacePending(projectItems))
    }
  }

  function upsertItem(threadID: string, item: TimelineItem): void {
    const items = [...(itemsByThread.value[threadID] ?? [])]
    const index = items.findIndex((existing) => existing.id === item.id)
    if (index >= 0) {
      const current = items[index]
      if (!current) return
      items[index] = {
        ...current,
        ...item,
        // Never let a shorter completed/snapshot replace a longer streamed body.
        text: mergeStreamText(item.text, current.text),
        reasoningSummary: mergeStreamText(item.reasoningSummary, current.reasoningSummary),
        reasoningContent: mergeStreamText(item.reasoningContent, current.reasoningContent),
        output: mergeStreamText(item.output, current.output),
        attachments: item.attachments.length ? item.attachments : current.attachments,
      }
    } else if (item.type === 'userMessage') {
      let localIndex = -1
      for (let index = items.length - 1; index >= 0; index -= 1) {
        const existing = items[index]
        if (existing?.local && existing.text === item.text) {
          localIndex = index
          break
        }
      }
      if (localIndex >= 0) items[localIndex] = item
      else items.push(item)
    } else {
      items.push(item)
    }
    itemsByThread.value = { ...itemsByThread.value, [threadID]: items }
  }

  function markItemFailed(threadID: string, itemID: string): void {
    const items = (itemsByThread.value[threadID] ?? []).map((item) =>
      item.id === itemID ? { ...item, failed: true } : item,
    )
    itemsByThread.value = { ...itemsByThread.value, [threadID]: items }
  }

  function patchItem(threadID: string, itemID: string, patch: Partial<TimelineItem>): void {
    if (!threadID || !itemID) return
    const items = [...(itemsByThread.value[threadID] ?? [])]
    const index = items.findIndex((item) => item.id === itemID)
    const current = items[index]
    if (!current) return
    items[index] = { ...current, ...patch }
    itemsByThread.value = { ...itemsByThread.value, [threadID]: items }
  }

  function setThreadTurn(threadID: string, turnID: string): void {
    if (!threadID) return
    const next = { ...activeTurnByThread.value }
    for (const id of Object.keys(next)) {
      if (id === threadID || sameThreadSession(id, threadID)) delete next[id]
    }
    if (turnID) next[threadID] = turnID
    activeTurnByThread.value = next
  }

  function setTurnFeedback(threadID: string, feedback: TurnFeedback): void {
    if (!threadID) return
    const next = { ...turnFeedbackByThread.value }
    for (const id of Object.keys(next)) {
      if (id === threadID || sameThreadSession(id, threadID)) delete next[id]
    }
    next[threadID] = feedback
    turnFeedbackByThread.value = next
  }

  function clearTurnFeedback(threadID: string): void {
    if (!threadID) return
    const next = { ...turnFeedbackByThread.value }
    let changed = false
    for (const id of Object.keys(next)) {
      if (id !== threadID && !sameThreadSession(id, threadID)) continue
      delete next[id]
      changed = true
    }
    if (!changed) return
    turnFeedbackByThread.value = next
  }

  function resetConversationState(): void {
    openThreadSequence += 1
    projectLoadSequence += 1
    loadingProjects.value = []
    activeThread.value = null
    activeThreadId.value = ''
    activeTurnByThread.value = {}
    turnFeedbackByThread.value = {}
    queuedMessagesByThread.value = {}
    sendingThreadIds.value = []
    loadingThreadId.value = ''
    loadingSequenceByThread.clear()
    workspaceSelectionSequenceByThread.clear()
    threadAlias.clear()
    pendingSubmissionByThread.clear()
    threads.value = []
    archivedThreads.value = []
    itemsByThread.value = {}
    historyByThread.value = {}
    tokenUsageByThread.value = {}
    turnMetricsByThread.value = {}
    diffsByTurn.value = {}
    latestDiffByThread.value = {}
    diffTurnOwners.clear()
    loadedThreadIDs.clear()
    deltaBuffers.clear()
    pendingDiffs.clear()
    pendingTokenUsage.clear()
    completedTurns.clear()
    completedTurnStatus.clear()
    latestStartedTurnByThread.clear()
    pendingPlanByThread.clear()
    sawPlanUpdateByTurn.clear()
    pendingCollaborationModeByThread.clear()
    collaborationModeByTurn.clear()
    clearAllTrackedTimeouts()
    planOfferRetryTimers.clear()
    idleReconcileTimers.clear()
    planImplementPrompt.value = null
    deltaTimer = 0
    diffTimer = 0
    tokenUsageTimer = 0
  }

  return {
    busy,
    sendingMessage,
    sendingThreadIds,
    interruptingTurn,
    threadMutation,
    connection,
    lastTransportMessage,
    threads,
    archivedThreads,
    projectThreads,
    projectErrors,
    loadingProjects,
    threadSearch,
    activeThread,
    activeThreadId,
    activeTurnByThread,
    turnFeedbackByThread,
    queuedMessagesByThread,
    loadingThreadId,
    creatingThread,
    itemsByThread,
    diffsByTurn,
    latestDiffByThread,
    tokenUsageByThread,
    turnMetricsByThread,
    pendingRequests,
    isReady,
    isTurnRunning,
    activeTurnId,
    activeItems,
    activeHistoryHasEarlier,
    activeHistoryEarlierCount,
    activeHistoryLoadingEarlier,
    activeQueuedMessages,
    activeThreadBusy,
    activeThreadUsesExternalProvider,
    canSteerActiveTurn,
    activeTurnFeedback,
    activeTokenUsage,
    activeTurnMetrics,
    pendingRequest,
    planImplementPrompt,
    threadGroups,
    filteredThreadGroups,
    runningThreadIds,
    sameThread: sameThreadSession,
    bootstrapEvents,
    dispose,
    connect,
    disconnect,
    loadThreads,
    loadRecentProjectThreads,
    reloadProject,
    loadModels,
    loadModelProviders,
    createThread,
    newThread,
    newThreadInProject,
    pinnedThreadIds,
    isThreadPinned,
    toggleThreadPin,
    openThread,
    recoverActiveThread,
    loadEarlierHistory,
    openProjectThread,
    switchProject,
    selectProject,
    activateProject,
    clearActiveSession,
    switchWorkMode,
    updateSessionPreferences,
    patchActiveSessionPreferences,
    patchActiveThreadMemories,
    setCollaborationMode,
    dismissPlanImplementation,
    acceptPlanImplementation,
    forkActiveThread,
    forkThread,
    archiveThread,
    archiveActiveThread,
    compactActiveThread,
    renameThread,
    renameActiveThread,
    deleteThread,
    deleteActiveThread,
    unarchiveThread,
    startReview,
    rollbackToTurn,
    sendMessage,
    steerMessage,
    retryMessage,
    retryLastMessage,
    removeQueuedMessage,
    retryQueuedMessage,
    reorderQueuedMessage,
    sendQueuedMessageNow,
    interruptTurn,
    resolveApproval,
    resolveUserInput,
    resolveMcpElicitation,
    setSearch,
    resetConversationState,
  }
})

function normalizeThreads(value: unknown): ThreadSummary[] {
  return asArray(value)
    .map(normalizeThread)
    .filter((thread): thread is ThreadSummary => thread !== null)
}

function normalizeModels(value: unknown): import('../types/codex').ModelOption[] {
  return asArray(value)
    .map((model) => {
      const record = asRecord(model)
      const id = asString(record.id, asString(record.model))
      if (!id) return null
      const catalogTiers = asArray(record.serviceTiers).map((tier) => {
        const entry = asRecord(tier)
        return {
          id: asString(entry.id),
          name: asString(entry.name, asString(entry.id)),
          description: asString(entry.description),
        }
      }).filter((tier) => tier.id !== '')
      const serviceTiers = catalogTiers.length
        ? catalogTiers
        : asArray(record.additionalSpeedTiers).map((tier) => asString(tier)).filter(Boolean).map((tier) => ({
            id: tier,
            name: tier,
            description: '',
          }))
      return {
        id,
        model: asString(record.model, id),
        displayName: asString(record.displayName, id),
        description: asString(record.description),
        isDefault: record.isDefault === true,
        defaultReasoningEffort: asString(record.defaultReasoningEffort, 'high'),
        defaultServiceTier: asString(record.defaultServiceTier),
        serviceTiers,
        supportsPersonality: record.supportsPersonality === true,
        supportedReasoningEfforts: asArray(record.supportedReasoningEfforts).map((option) => {
          const entry = asRecord(option)
          return {
            effort: asString(entry.reasoningEffort, asString(entry.effort)),
            description: asString(entry.description),
          }
        }).filter((option) => option.effort !== ''),
      }
    })
    .filter((model): model is import('../types/codex').ModelOption => model !== null)
}

function normalizeModelProviders(value: unknown): import('../types/codex').ModelProviderOption[] {
  return asArray(value).map((value) => {
    const record = asRecord(value)
    const kind = asString(record.kind, 'custom')
    return {
      id: asString(record.id),
      name: asString(record.name, asString(record.id)),
      kind: ['codex', 'claude', 'gemini', 'grok', 'opencode', 'custom'].includes(kind)
        ? kind as import('../types/codex').ModelProviderOption['kind']
        : 'custom',
      configured: record.configured !== false,
    }
  }).filter((provider) => provider.name !== '')
}

const threadModelCacheKey = 'nice-codex:thread-models'

function loadThreadModelIdentity(): Record<string, ThreadModelIdentity> {
  try {
    const parsed = asRecord(JSON.parse(localStorage.getItem(threadModelCacheKey) || '{}'))
    return Object.fromEntries(Object.entries(parsed).flatMap(([threadID, value]) => {
      const identity = asRecord(value)
      const model = asString(identity.model)
      const provider = asString(identity.provider)
      return model || provider ? [[threadID, { model, provider } satisfies ThreadModelIdentity]] : []
    }))
  } catch {
    return {}
  }
}

function persistThreadModelIdentity(value: Record<string, ThreadModelIdentity>): void {
  try {
    const recent = Object.fromEntries(Object.entries(value).slice(-300))
    localStorage.setItem(threadModelCacheKey, JSON.stringify(recent))
  } catch {
    // Model labels remain available in memory when persistent storage is unavailable.
  }
}

function uniqueWorkspacePaths(current: string, recent: string[]): string[] {
  const result: string[] = []
  const seen = new Set<string>()
  for (const path of [...recent, current]) {
    const value = path.trim()
    const key = workspaceKey(value)
    if (!value || seen.has(key)) continue
    seen.add(key)
    result.push(value)
  }
  return result
}

function normalizeLocalUsageRuntime(value: unknown): LocalUsageRuntime | '' {
  const runtime = asString(value).trim().toLocaleLowerCase()
  if (
    runtime === 'codex'
    || runtime === 'claude'
    || runtime === 'grok'
    || runtime === 'gemini'
    || runtime === 'opencode'
  ) return runtime
  return ''
}

function workspaceName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

function createLocalUserItem(id: string, text: string, images: string[], turnId = ''): TimelineItem {
  return {
    id,
    turnId,
    type: 'userMessage',
    status: 'completed',
    text,
    command: '',
    cwd: '',
    output: '',
    title: '',
    detail: '',
    changes: [],
    attachments: images.map((source): import('../types/codex').MessageAttachment => ({
      kind: 'local',
      source,
      name: attachmentName(source),
    })),
    local: true,
    failed: false,
  }
}

function localAttachmentSources(attachments: import('../types/codex').MessageAttachment[]): string[] {
  return attachments.filter((attachment) => attachment.kind === 'local').map((attachment) => attachment.source)
}

function uniqueImagePaths(paths: string[]): string[] {
  const result: string[] = []
  const seen = new Set<string>()
  for (const path of paths) {
    const value = path.trim()
    const normalized = value.replace(/\\/g, '/')
    const key = navigator.userAgent.includes('Windows') ? normalized.toLocaleLowerCase() : normalized
    if (!value || seen.has(key)) continue
    seen.add(key)
    result.push(value)
  }
  return result
}

function attachmentName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : translate('notifications.unexpected')
}

function appendBoundedDelta(current: string, delta: string, limit: number): string {
  const marker = '\n\n[Output truncated for performance]'
  if (current.endsWith(marker)) return current
  if (current.length + delta.length <= limit) return current + delta
  return `${(current + delta).slice(0, limit)}${marker}`
}

/** Prefer the longer/complete stream when a snapshot would otherwise truncate. */
function mergeStreamText(incoming: string | undefined, current: string | undefined): string {
  const next = incoming ?? ''
  const prev = current ?? ''
  if (!next) return prev
  if (!prev) return next
  if (next === prev) return next
  // Completed/snapshot is only a prefix of what deltas already built → keep stream.
  if (prev.startsWith(next)) return prev
  // Snapshot extends the stream → accept it.
  if (next.startsWith(prev)) return next
  // Divergent payloads: keep the longer body to avoid occasional 漏字.
  return prev.length > next.length ? prev : next
}

function mergeThreadSnapshotWithLive(
  snapshot: TimelineItem[],
  cached: TimelineItem[],
  liveTurnID: string,
): TimelineItem[] {
  const result = [...snapshot]
  const cachedServerItemIDs = new Set(
    cached.filter((item) => !item.local && item.id).map((item) => item.id),
  )
  for (const item of cached) {
    const live = item.local
      || Boolean(liveTurnID && item.turnId === liveTurnID)
      || isActiveStatus(item.status)
    let index = item.id ? result.findIndex((candidate) => candidate.id === item.id) : -1
    if (index < 0 && item.type === 'userMessage' && item.text && item.turnId) {
      for (let cursor = result.length - 1; cursor >= 0; cursor -= 1) {
        const candidate = result[cursor]
        if (
          candidate?.type === 'userMessage'
          && candidate.turnId === item.turnId
          && candidate.text === item.text
        ) {
          index = cursor
          break
        }
      }
    }
    // ReadThread can expose the accepted server user row before item/started has
    // replaced its optimistic local counterpart. Match only rows absent from cache.
    if (index < 0 && item.local && item.type === 'userMessage' && item.text) {
      for (let cursor = result.length - 1; cursor >= 0; cursor -= 1) {
        const candidate = result[cursor]
        if (
          candidate?.type === 'userMessage'
          && candidate.text === item.text
          && (
            !cachedServerItemIDs.has(candidate.id)
            || Boolean(liveTurnID && candidate.turnId === liveTurnID)
          )
          && (!liveTurnID || !candidate.turnId || candidate.turnId === liveTurnID)
        ) {
          index = cursor
          break
        }
      }
    }
    if (index < 0 && item.turnId && item.turnId === liveTurnID) {
      index = result.findIndex((candidate) =>
        candidate.turnId === item.turnId && candidate.type === item.type,
      )
    }
    if (index < 0) {
      if (live) {
        const firstLiveAgentIndex = item.local && item.type === 'userMessage' && !item.turnId
          ? result.findIndex((candidate) =>
              candidate.type !== 'userMessage'
              && (
                (liveTurnID && candidate.turnId === liveTurnID)
                || (!liveTurnID && isActiveStatus(candidate.status))
              ),
            )
          : -1
        if (firstLiveAgentIndex >= 0) result.splice(firstLiveAgentIndex, 0, item)
        else result.push(item)
      }
      continue
    }
    if (!live) continue
    const current = result[index]!
    result[index] = {
      ...item,
      ...current,
      text: mergeStreamText(item.text, current.text),
      reasoningSummary: mergeStreamText(item.reasoningSummary, current.reasoningSummary),
      reasoningContent: mergeStreamText(item.reasoningContent, current.reasoningContent),
      output: mergeStreamText(item.output, current.output),
      attachments: item.attachments.length ? item.attachments : current.attachments,
      status: item.status,
      failed: item.failed || current.failed,
    }
  }
  return result
}

function splitCodexHistoryPrefix(
  snapshot: TimelineItem[],
  current: TimelineItem[],
): { prefix: TimelineItem[], current: TimelineItem[] } {
  if (!snapshot.length || !current.length) return { prefix: [], current }
  const currentByID = new Map<string, number>()
  current.forEach((item, index) => {
    if (item.id) currentByID.set(item.id, index)
  })
  let snapshotIndex = -1
  let currentIndex = -1
  for (let index = 0; index < snapshot.length; index += 1) {
    const item = snapshot[index]
    const byID = item.id ? currentByID.get(item.id) : undefined
    const match = byID ?? current.findIndex((row) =>
      row.turnId === item.turnId
      && row.type === item.type
      && row.text === item.text,
    )
    if (match >= 0) {
      snapshotIndex = index
      currentIndex = match
      break
    }
  }
  if (snapshotIndex < 0 || currentIndex < 0) return { prefix: [], current }
  const prefixLength = Math.max(0, currentIndex - snapshotIndex)
  return {
    prefix: current.slice(0, prefixLength),
    current: current.slice(prefixLength),
  }
}

function loadLastThreadByWorkspace(): Record<string, string> {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem('nice-codex.lastThreads') || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    return Object.fromEntries(Object.entries(parsed).filter(([, value]) => typeof value === 'string'))
  } catch {
    return {}
  }
}

function loadPinnedThreadIds(): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem('nice-codex.pinnedThreads') || '[]')
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item): item is string => typeof item === 'string' && item.trim() !== '')
  } catch {
    return []
  }
}

function persistPinnedThreadIds(ids: string[]): void {
  try {
    localStorage.setItem('nice-codex.pinnedThreads', JSON.stringify(ids.slice(0, 200)))
  } catch {
    // Local persistence is best effort.
  }
}

async function mapWithConcurrency<T, R>(items: T[], limit: number, worker: (item: T) => Promise<R>): Promise<R[]> {
  const results = new Array<R>(items.length)
  let cursor = 0
  const run = async () => {
    while (cursor < items.length) {
      const index = cursor
      cursor += 1
      const item = items[index]
      if (item !== undefined) results[index] = await worker(item)
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, run))
  return results
}
