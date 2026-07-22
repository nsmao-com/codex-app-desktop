import { Events } from '@wailsio/runtime'
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type {
  SendMessageRequest,
  SteerTurnRequest,
} from '../../bindings/nice_codex_desktop/models'
import type { Event as CodexEvent, Status as CodexStatus } from '../../bindings/nice_codex_desktop/internal/codex/models'
import { useAppStore } from './app'
import { useCapabilitiesStore } from './capabilities'
import { useTerminalStore } from './terminal'
import { useWorkspaceStore } from './workspace'
import { notify } from '../utils/notify'
import {
  buildRuntimeProviders,
  cleanModelDisplayName,
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
  metricsFromTurns,
  normalizeAccountRateLimits,
  normalizeStatus,
  normalizeThread,
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
  const threadSearch = shallowRef('')
  const activeThreadId = shallowRef('')
  const activeThread = shallowRef<ThreadSummary | null>(null)
  const activeTurnByThread = shallowRef<Record<string, string>>({})
  const turnFeedbackByThread = shallowRef<Record<string, TurnFeedback>>({})
  const queuedMessagesByThread = shallowRef<Record<string, QueuedMessage[]>>({})
  const loadingThreadId = shallowRef('')
  const creatingThread = shallowRef(false)
  const itemsByThread = shallowRef<Record<string, TimelineItem[]>>({})
  const diffsByTurn = shallowRef<Record<string, string>>({})
  const latestDiffByThread = shallowRef<Record<string, string>>({})
  const tokenUsageByThread = shallowRef<Record<string, ReturnType<typeof normalizeThreadTokenUsage>>>({})
  const turnMetricsByThread = shallowRef<Record<string, Record<string, TurnMetrics>>>({})
  const pendingRequests = shallowRef<PendingServerRequest[]>([])
  const completedTurns = new Set<string>()
  const loadedThreadIDs = new Set<string>()
  const lastThreadByWorkspace = shallowRef<Record<string, string>>(loadLastThreadByWorkspace())
  /** Official Codex: after a plan turn, ask "Implement this plan?" */
  const planImplementPrompt = shallowRef<null | {
    threadId: string
    turnId: string
    planText: string
  }>(null)
  const pendingPlanByThread = new Map<string, { turnId: string; text: string }>()
  /** Official TUI: saw_plan_update_this_turn — update_plan alone can open the prompt. */
  const sawPlanUpdateByTurn = new Map<string, string>()
  const planOfferRetryTimers = new Map<string, number[]>()

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
  const pendingTokenUsage = new Map<string, { threadId: string; turnId: string; usage: ReturnType<typeof normalizeThreadTokenUsage> }>()
  const threadModelIdentity: Record<string, ThreadModelIdentity> = loadThreadModelIdentity()

  const isReady = computed(() => connection.value.state === 'ready')
  const activeTurnId = computed(() => activeTurnByThread.value[activeThreadId.value] ?? '')
  const isTurnRunning = computed(() => activeTurnId.value !== '')
  const sendingMessage = computed(() => sendingThreadIds.value.includes(activeThreadId.value))
  const activeItems = computed(() => itemsByThread.value[activeThreadId.value] ?? [])
  const activeQueuedMessages = computed(() => queuedMessagesByThread.value[activeThreadId.value] ?? [])
  const activeThreadBusy = computed(() => isTurnRunning.value || sendingMessage.value || activeQueuedMessages.value.length > 0)
  const activeThreadUsesExternalProvider = computed(() => false)
  const activeTurnFeedback = computed(() => turnFeedbackByThread.value[activeThreadId.value] ?? null)
  const activeTokenUsage = computed(() => tokenUsageByThread.value[activeThreadId.value] ?? null)
  const activeTurnMetrics = computed(() => turnMetricsByThread.value[activeThreadId.value] ?? {})
  const pendingRequest = computed(() => pendingRequests.value[0] ?? null)

  const threadGroups = computed<ThreadGroup[]>(() => {
    const paths = uniqueWorkspacePaths(appStore.settings.workspace, appStore.settings.recentWorkspaces ?? [])
    return paths.map((path) => ({
      path,
      name: workspaceName(path),
      active: sameWorkspace(path, appStore.settings.workspace),
      loading: loadingProjects.value.some((loadingPath) => sameWorkspace(loadingPath, path)),
      error: projectErrors.value[path] ?? '',
      threads: sameWorkspace(path, appStore.settings.workspace)
        ? threads.value
        : projectThreads.value[path] ?? [],
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
    if (deltaTimer) window.clearTimeout(deltaTimer)
    if (diffTimer) window.clearTimeout(diffTimer)
    if (tokenUsageTimer) window.clearTimeout(tokenUsageTimer)
    deltaBuffers.clear()
    pendingDiffs.clear()
    pendingTokenUsage.clear()
    completedTurns.clear()
    loadedThreadIDs.clear()
  }

  async function connect(path = appStore.settings.workspace): Promise<boolean> {
    if (!path || busy.value) return false
    busy.value = true
    connection.value = {
      ...connection.value,
      state: 'starting',
      running: false,
      message: translate('app.connecting'),
      workspace: path,
    }
    try {
      await backend.StartCodex(path)
      connection.value = await backend.CodexStatus()
      if (connection.value.state !== 'ready') {
        throw new Error(connection.value.message || translate('notifications.connectionFailed'))
      }
      lastTransportMessage.value = ''
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
        loadedThreadIDs.delete(threadID)
        await openThread(threadID)
      }
      loadRecentProjectThreads()
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
      activeTurnByThread.value = {}
    } catch (error) {
      notify('error', translate('notifications.unableStop'), errorMessage(error))
    }
  }

  async function loadThreads(): Promise<void> {
    const requestedPath = appStore.settings.workspace
    if (!requestedPath) return
    try {
      const [response, archivedResponse] = await Promise.all([
        backend.ListThreads(''),
        backend.ListArchivedThreads('').catch(() => ({ data: [] })),
      ])
      const list = normalizeThreadList(asRecord(response).data)
      const archived = normalizeThreadList(asRecord(archivedResponse).data)
      if (requestedPath) setProjectThreads(requestedPath, list)
      if (sameWorkspace(requestedPath, appStore.settings.workspace)) {
        threads.value = list
        archivedThreads.value = archived
      }
    } catch (error) {
      if (sameWorkspace(requestedPath, appStore.settings.workspace)) {
        notify('error', translate('sidebar.projectLoadFailed'), errorMessage(error))
      }
    }
  }

  async function loadRecentProjectThreads(): Promise<void> {
    const sequence = ++projectLoadSequence
    const current = appStore.settings.workspace
    const paths = uniqueWorkspacePaths(current, appStore.settings.recentWorkspaces ?? [])
      .filter((path) => !sameWorkspace(path, current))
    loadingProjects.value = paths
    const results = await mapWithConcurrency(paths, 3, async (path) => {
      try {
        const response = await backend.ListWorkspaceThreads(path, '')
        return { path, threads: normalizeThreadList(asRecord(response).data), error: '' }
      } catch (error) {
        return { path, threads: null, error: errorMessage(error) }
      }
    })
    if (sequence === projectLoadSequence) {
      const nextThreads = { ...projectThreads.value }
      const nextErrors = { ...projectErrors.value }
      for (const result of results) {
        if (result.threads) nextThreads[result.path] = result.threads
        if (result.error) nextErrors[result.path] = result.error
        else delete nextErrors[result.path]
      }
      projectThreads.value = nextThreads
      projectErrors.value = nextErrors
      loadingProjects.value = []
    }
  }

  async function reloadProject(path: string): Promise<void> {
    if (!path || loadingProjects.value.some((item) => sameWorkspace(item, path))) return
    if (sameWorkspace(path, appStore.settings.workspace)) {
      try {
        await loadThreads()
      } catch (error) {
        notify('error', translate('sidebar.projectLoadFailed'), errorMessage(error))
      }
      return
    }

    loadingProjects.value = [...loadingProjects.value, path]
    const nextErrors = { ...projectErrors.value }
    delete nextErrors[path]
    projectErrors.value = nextErrors
    try {
      const response = await backend.ListWorkspaceThreads(path, '')
      setProjectThreads(path, normalizeThreadList(asRecord(response).data))
    } catch (error) {
      const message = errorMessage(error)
      projectErrors.value = { ...projectErrors.value, [path]: message }
      notify('error', translate('sidebar.projectLoadFailed'), message, {
        label: translate('sidebar.retryProject'),
        onClick: () => reloadProject(path),
      })
    } finally {
      loadingProjects.value = loadingProjects.value.filter((item) => !sameWorkspace(item, path))
    }
  }

  async function loadModels(): Promise<void> {
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
    appStore.models = merged

    const configuredModel = appStore.settings.model.trim()
    const configuredInCatalog = appStore.models.some((model) => model.model === configuredModel)
    const configuredCustom = customModels.some((model) => model.toLocaleLowerCase() === configuredModel.toLocaleLowerCase())
    if (configuredModel && !configuredInCatalog && configuredCustom) return

    const preferred = (
      configuredInCatalog
        ? appStore.models.find((model) => model.model === configuredModel)
        : undefined
    )
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
    if (!isReady.value || !appStore.settings.workspace) return null
    // Drop unused empty drafts so "New task" stays instant and the sidebar stays clean.
    discardEmptyPendingThreads()
    createThreadSequence += 1
    creatingThread.value = false
    const now = Math.floor(Date.now() / 1000)
    const pendingID = `pending-thread-${Date.now()}-${createThreadSequence}`
    const optimistic: ThreadSummary = {
      id: pendingID,
      name: translate('sidebar.newTask'),
      preview: '',
      cwd: appStore.settings.workspace,
      createdAt: now,
      updatedAt: now,
      status: 'idle',
      cliVersion: '',
      model: appStore.settings.model,
      modelProvider: appStore.settings.modelProvider,
      workMode: appStore.settings.workMode || 'code',
    }
    // Show the empty composer immediately. Real Codex/external session is created
    // on the first send via the existing pending-thread drain path.
    setActiveThread(optimistic, [])
    setThreadMetrics(pendingID, [])
    rememberLoadedThread(pendingID)
    rememberProjectThread(appStore.settings.workspace, pendingID)
    addOrUpdateThread(optimistic)
    return optimistic
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
    const path = appStore.settings.workspace
    if (path) setProjectThreads(path, threads.value)
  }

  async function openThread(threadID: string): Promise<void> {
    if (!threadID || loadingThreadId.value === threadID) return
    createThreadSequence += 1
    creatingThread.value = false
    const previousThread = activeThread.value
    const previousThreadID = activeThreadId.value
    const summary = findThreadSummary(threadID)
    if (summary) {
      activeThread.value = summary
      activeThreadId.value = threadID
      rememberProjectThread(appStore.settings.workspace, threadID)
    }
    if (loadedThreadIDs.has(threadID)) {
      rememberLoadedThread(threadID)
      // Cache hit used to skip running-turn reconcile — switching Code/Cowork
      // (or reopening a background turn) left composer thinking the thread was idle.
      const knownTurnID = activeTurnByThread.value[threadID] ?? ''
      const summaryStatus = summary?.status || activeThread.value?.status || ''
      if (knownTurnID) {
        setThreadTurn(threadID, knownTurnID)
        if (!turnFeedbackByThread.value[threadID]) {
          setTurnFeedback(threadID, { state: 'running', message: '', turnId: knownTurnID })
        }
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
    loadingThreadId.value = threadID
    try {
      const response = await backend.ReadThread(threadID)
      const rawThread = asRecord(asRecord(response).thread)
      const thread = normalizeRuntimeThread(rawThread, response)
      if (!thread) throw new Error(translate('notifications.taskOpenFailed'))
      const items = timelineFromTurns(rawThread.turns)
      itemsByThread.value = { ...itemsByThread.value, [thread.id]: items }
      setThreadMetrics(thread.id, rawThread.turns)
      rememberLoadedThread(thread.id)
      let runningTurnID = ''
      for (const turnValue of [...asArray(rawThread.turns)].reverse()) {
        const turn = asRecord(turnValue)
        if (isActiveStatus(turn.status)) {
          runningTurnID = asString(turn.id)
          break
        }
      }
      if (sequence === openThreadSequence && activeThreadId.value === threadID) {
        activeThread.value = thread
        setThreadTurn(threadID, runningTurnID)
        if (runningTurnID) {
          setTurnFeedback(threadID, { state: 'running', message: '', turnId: runningTurnID })
        }
      }
      addOrUpdateThread(thread)
      scheduleThreadQueueDrain(thread.id)
    } catch (error) {
      if (sequence !== openThreadSequence) return
      activeThread.value = previousThread
      activeThreadId.value = previousThreadID
      notify('error', translate('notifications.taskOpenFailed'), errorMessage(error), {
        label: translate('common.retry'),
        onClick: () => openThread(threadID),
      })
    } finally {
      if (sequence === openThreadSequence) loadingThreadId.value = ''
    }
  }

  async function openProjectThread(path: string, threadID: string): Promise<void> {
    if (!path || !threadID) return
    if (sameWorkspace(path, appStore.settings.workspace)) {
      await openThread(threadID)
      return
    }
    await switchProject(path, threadID)
  }

  async function switchProject(path: string, preferredThreadID = ''): Promise<void> {
    if (!path) return
    const currentPath = appStore.settings.workspace
    if (activeThreadId.value && currentPath) rememberProjectThread(currentPath, activeThreadId.value)
    const switched = await workspaceStore.useWorkspace(path)
    if (!switched) return

    await activateProject(path, preferredThreadID)
  }

  async function selectProject(): Promise<void> {
    const currentPath = appStore.settings.workspace
    if (activeThreadId.value && currentPath) rememberProjectThread(currentPath, activeThreadId.value)
    const path = await workspaceStore.selectWorkspace()
    if (!path) return
    await activateProject(path)
  }

  async function activateProject(path: string, preferredThreadID = ''): Promise<void> {
    const cached = projectThreadsForPath(path)
    if (cached) {
      threads.value = [...cached]
    } else {
      await reloadProject(path)
      threads.value = [...(projectThreadsForPath(path) ?? [])]
    }
    const targetThreadID = preferredThreadID
      || projectThreadForPath(path)
      || threads.value[0]?.id
      || ''
    if (targetThreadID) {
      await openThread(targetThreadID)
    } else {
      activeThread.value = null
      activeThreadId.value = ''
    }
    // Keep the cached conversation visible while refreshing the target project.
    void loadThreads().catch(() => undefined)
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

  async function forkActiveThread(): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value || threadMutation.value) return
    threadMutation.value = 'fork'
    try {
      const response = await backend.ForkThread(threadID)
      const rawThread = asRecord(asRecord(response).thread)
      const thread = normalizeRuntimeThread(rawThread, response)
      if (!thread) throw new Error(translate('notifications.taskOpenFailed'))
      const items = timelineFromTurns(rawThread.turns)
      setActiveThread(thread, items)
      setThreadMetrics(thread.id, rawThread.turns)
      rememberLoadedThread(thread.id)
      addOrUpdateThread(thread)
      notify('success', translate('threadActions.forked'), thread.name)
    } catch (error) {
      notify('error', translate('threadActions.forkFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function archiveActiveThread(): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value || threadMutation.value) return
    threadMutation.value = 'archive'
    try {
      await backend.ArchiveThread(threadID)
      activeThread.value = null
      activeThreadId.value = ''
      await loadThreads()
      notify('success', translate('threadActions.archived'), translate('threadActions.archivedHint'))
    } catch (error) {
      notify('error', translate('threadActions.archiveFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
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
      notify('info', translate('threadActions.compacting'), translate('threadActions.compactingHint'))
    } catch (error) {
      notify('error', translate('threadActions.compactFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function renameActiveThread(name?: string): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value || threadMutation.value) return
    const nextName = (
      name !== undefined
        ? name
        : (window.prompt(translate('threadActions.renamePrompt'), activeThread.value?.name || '') || '')
    ).trim()
    if (!nextName) return
    threadMutation.value = 'rename'
    try {
      const response = await backend.SetThreadName(threadID, nextName)
      const thread = normalizeRuntimeThread(asRecord(asRecord(response).thread), response)
        ?? (activeThread.value ? { ...activeThread.value, name: nextName } : null)
      if (thread) {
        activeThread.value = thread
        addOrUpdateThread(thread)
      }
      notify('success', translate('threadActions.renamed'), nextName)
    } catch (error) {
      notify('error', translate('threadActions.renameFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function deleteActiveThread(): Promise<void> {
    const threadID = activeThreadId.value
    if (!threadID || activeThreadBusy.value || threadMutation.value) return
    if (!window.confirm(translate('threadActions.deleteConfirm'))) return
    threadMutation.value = 'delete'
    try {
      await backend.DeleteThread(threadID)
      activeThread.value = null
      activeThreadId.value = ''
      await loadThreads()
      notify('success', translate('threadActions.deleted'), translate('threadActions.deletedHint'))
    } catch (error) {
      notify('error', translate('threadActions.deleteFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
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
          cwd: appStore.settings.workspace,
          createdAt: Date.now() / 1000,
          updatedAt: Date.now() / 1000,
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
      workspaceStore.clearDiff()
      notify('warning', translate('timeline.rolledBack'), translate('timeline.rollbackFilesWarning'))
    } catch (error) {
      notify('error', translate('timeline.rollbackFailed'), errorMessage(error))
    } finally {
      threadMutation.value = ''
    }
  }

  async function sendMessage(text: string, images: string[] = []): Promise<boolean> {
    if (
      isTurnRunning.value
      && activeTurnId.value
      && !activeThreadUsesExternalProvider.value
      && !activeThreadId.value.startsWith('pending-thread-')
    ) {
      return steerMessage(text, images)
    }
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
    if (!message || !isReady.value || creatingThread.value) return false
    const imagePaths = uniqueImagePaths(images).slice(0, 4)
    const now = Date.now()
    const sequence = ++queuedMessageSequence
    const threadID = activeThread.value?.id
      ?? (activeThreadId.value.startsWith('pending-thread-') ? activeThreadId.value : `pending-thread-${now}-${sequence}`)
    const workspace = activeThread.value?.cwd || appStore.settings.workspace
    if (!threadID || !workspace) return false
    if (!activeThread.value) activeThreadId.value = threadID
    // User continued chatting — dismiss implement prompt (official dismisses on follow-up).
    if (planImplementPrompt.value?.threadId === threadID) planImplementPrompt.value = null

    const queuedMessage: QueuedMessage = {
      id: `queued-${now}-${sequence}`,
      threadId: threadID,
      workspace,
      text: message,
      images: imagePaths,
      createdAt: now,
      localItemId: retryItemID || `local-${now}-${sequence}`,
      retryItemId: retryItemID,
      state: 'queued',
      error: '',
    }
    queuedMessagesByThread.value = {
      ...queuedMessagesByThread.value,
      [threadID]: [...(queuedMessagesByThread.value[threadID] ?? []), queuedMessage],
    }
    scheduleThreadQueueDrain(threadID)
    return true
  }

  async function drainThreadQueue(threadID: string): Promise<void> {
    if (!threadID || !isReady.value || isThreadSubmitting(threadID) || threadIsRunning(threadID)) return
    const queuedMessage = queuedMessagesByThread.value[threadID]?.[0]
    if (!queuedMessage || queuedMessage.state === 'failed') return
    if (!sameWorkspace(queuedMessage.workspace, appStore.settings.workspace)) return

    let resolvedThreadID = threadID
    let continueDraining = false
    setThreadSubmitting(threadID, true)
    patchQueuedMessage(threadID, queuedMessage.id, { state: 'sending', error: '' })

    // Show the user bubble + thinking state immediately (before create/resume/send).
    const localItem = createLocalUserItem(queuedMessage.localItemId, queuedMessage.text, queuedMessage.images)
    if (queuedMessage.retryItemId) replaceItem(threadID, localItem)
    else if (!(itemsByThread.value[threadID] ?? []).some((item) => item.id === localItem.id)) {
      appendItem(threadID, localItem)
    }
    setTurnFeedback(threadID, { state: 'submitting', message: translate('chat.thinking'), turnId: '' })

    try {
      let thread = activeThread.value?.id === threadID ? activeThread.value : findThreadSummary(threadID)
      if (threadID.startsWith('pending-thread-')) {
        const pendingThreadID = threadID
        // Align global defaults with the draft composer selection before CreateThread.
        if (activeThread.value?.id === pendingThreadID) {
          const draft = activeThread.value
          if (
            draft.model !== appStore.settings.model
            || draft.modelProvider !== appStore.settings.modelProvider
          ) {
            try {
              await appStore.savePreferences({
                ...appStore.settings,
                model: draft.model || appStore.settings.model,
                modelProvider: draft.modelProvider,
              }, { silent: true })
            } catch {
              // Continue with current settings if draft sync fails.
            }
          }
        }
        thread = await createThread(false)
        resolvedThreadID = thread.id
        migratePendingThread(pendingThreadID, thread.id)
        if (activeThreadId.value === thread.id) {
          activeThread.value = {
            ...thread,
            model: activeThread.value?.model || thread.model,
            modelProvider: activeThread.value?.modelProvider || thread.modelProvider,
          }
          addOrUpdateThread(activeThread.value)
        }
      } else if (!thread) {
        throw new Error(translate('notifications.taskOpenFailed'))
      } else if (thread.status === 'notLoaded') {
        const response = await backend.ResumeThread(thread.id)
        const responseRecord = asRecord(response)
        const resumed = normalizeRuntimeThread(responseRecord.thread, responseRecord)
        if (resumed) {
          thread = resumed
          if (activeThreadId.value === resumed.id) activeThread.value = resumed
          addOrUpdateThread(resumed)
        }
      }
      if (!sameWorkspace(queuedMessage.workspace, appStore.settings.workspace)) {
        patchQueuedMessage(resolvedThreadID, queuedMessage.id, { state: 'queued', error: '' })
        return
      }

      setTurnFeedback(resolvedThreadID, { state: 'submitting', message: translate('chat.thinking'), turnId: '' })
      updateThreadModelIdentity(
        thread.id,
        appStore.settings.model || thread.model,
        appStore.settings.modelProvider || thread.modelProvider,
      )

      const response = await backend.SendMessage({
        threadId: thread.id,
        text: queuedMessage.text,
        images: queuedMessage.images,
        // Official TUI: SubmitUserMessageWithMode — mode travels with the turn.
        collaborationMode: resolveThreadCollaborationMode(thread),
      } satisfies SendMessageRequest)
      const turn = asRecord(asRecord(response).turn)
      const turnID = asString(turn.id)
      const turnStatus = asString(turn.status)
      const running = isActiveStatus(turnStatus) && !completedTurns.has(turnID)
      const startedAt = typeof turn.startedAt === 'number' ? turn.startedAt * 1000 : Date.now()
      const completedAt = typeof turn.completedAt === 'number' ? turn.completedAt * 1000 : null
      const durationMs = typeof turn.durationMs === 'number' ? turn.durationMs : null
      patchTurnMetrics(thread.id, turnID, { startedAt, completedAt, durationMs })
      setThreadTurn(thread.id, running ? turnID : '')
      if (running) {
        setTurnFeedback(thread.id, { state: 'running', message: translate('chat.thinking'), turnId: turnID })
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
      if (!thread.preview) {
        const updated = { ...thread, name: queuedMessage.text.slice(0, 56), preview: queuedMessage.text }
        if (activeThreadId.value === thread.id) activeThread.value = updated
        addOrUpdateThread(updated)
      }
      removeQueuedMessageFromThread(resolvedThreadID, queuedMessage.id)
      continueDraining = !running
    } catch (error) {
      const message = errorMessage(error)
      if (activeTurnByThread.value[resolvedThreadID]) {
        removeQueuedMessageFromThread(resolvedThreadID, queuedMessage.id)
        setTurnFeedback(resolvedThreadID, {
          state: 'running',
          message: '',
          turnId: activeTurnByThread.value[resolvedThreadID] ?? '',
        })
        return
      }
      markItemFailed(resolvedThreadID, queuedMessage.localItemId)
      setTurnFeedback(resolvedThreadID, { state: 'failed', message, turnId: '' })
      patchQueuedMessage(resolvedThreadID, queuedMessage.id, { state: 'failed', error: message })
      notify('error', translate('notifications.messageNotSent'), message)
    } finally {
      setThreadSubmitting(threadID, false)
      if (resolvedThreadID !== threadID) setThreadSubmitting(resolvedThreadID, false)
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

  async function steerMessage(text: string, images: string[] = []): Promise<boolean> {
    const message = text.trim()
    const threadID = activeThreadId.value
    const turnID = activeTurnId.value
    if (!message || !threadID || !turnID || !isReady.value || isThreadSubmitting(threadID)) return false

    const imagePaths = uniqueImagePaths(images).slice(0, 4)
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
      // Keep the turn running, but surface the steer failure so UI is not "success".
      setTurnFeedback(threadID, { state: 'failed', message, turnId: turnID })
      notify('error', translate('notifications.steerFailed'), message)
      // Restore running indicator shortly after — the turn itself is still live.
      window.setTimeout(() => {
        if (activeTurnByThread.value[threadID] === turnID) {
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
      window.setTimeout(() => {
        if (activeTurnByThread.value[threadID] !== turnID) {
          interruptingTurn.value = false
          return
        }
        completedTurns.add(turnID)
        setThreadTurn(threadID, '')
        setTurnFeedback(threadID, {
          state: 'interrupted',
          message: translate('chat.interrupted'),
          turnId: turnID,
        })
        interruptingTurn.value = false
        scheduleThreadQueueDrain(threadID)
      }, 8000)
    } catch (error) {
      interruptingTurn.value = false
      notify('error', translate('notifications.turnStopFailed'), errorMessage(error))
      if (activeTurnByThread.value[threadID] === turnID) {
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
          drainAvailableThreadQueues()
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
        for (const [threadID, turnID] of Object.entries(activeTurnByThread.value)) {
          setTurnFeedback(threadID, { state: 'failed', message, turnId: turnID })
        }
        activeTurnByThread.value = {}
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
        const threadID = activeThreadId.value
        if (threadID) {
          appendItem(threadID, {
            id: `notice-unsupported-${Date.now()}`,
            turnId: activeTurnId.value,
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
        const status = asString(asRecord(payload.status).type, 'idle')
        threads.value = threads.value.map((thread) => thread.id === threadID ? { ...thread, status } : thread)
        const nextProjects = { ...projectThreads.value }
        for (const [path, projectItems] of Object.entries(nextProjects)) {
          nextProjects[path] = projectItems.map((thread) => thread.id === threadID ? { ...thread, status } : thread)
        }
        projectThreads.value = nextProjects
        if (activeThread.value?.id === threadID) activeThread.value = { ...activeThread.value, status }
        if (status !== 'active') {
          const finishingTurnID = activeTurnByThread.value[threadID] ?? ''
          window.setTimeout(() => {
            // Do not clear / drain while a turn is still tracked as live —
            // thread can go idle slightly before turn/completed arrives.
            if (finishingTurnID && !completedTurns.has(finishingTurnID)) {
              if (activeTurnByThread.value[threadID] === finishingTurnID) return
            }
            if (finishingTurnID && activeTurnByThread.value[threadID] === finishingTurnID) {
              setThreadTurn(threadID, '')
              clearTurnFeedback(threadID)
            }
            scheduleThreadQueueDrain(threadID)
          }, 0)
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
        clearThreadQueue(threadID)
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
        notify('info', translate('notifications.contextCompacted'), translate('notifications.contextCompactedHint'))
        break
      case 'turn/started':
        {
          const threadID = asString(payload.threadId)
          const turn = asRecord(payload.turn)
          const turnID = asString(turn.id)
          completedTurns.delete(turnID)
          setThreadTurn(threadID, turnID)
          const startedAt = typeof turn.startedAt === 'number' ? turn.startedAt * 1000 : Date.now()
          patchTurnMetrics(threadID, turnID, { startedAt, completedAt: null, durationMs: null })
          setTurnFeedback(threadID, { state: 'running', message: '', turnId: turnID })
        }
        break
      case 'turn/completed': {
        const threadID = asString(payload.threadId)
        const turn = asRecord(payload.turn)
        const turnID = asString(turn.id)
        const status = asString(turn.status, 'completed')
        const completedAt = typeof turn.completedAt === 'number' ? turn.completedAt * 1000 : Date.now()
        const startedAt = typeof turn.startedAt === 'number' ? turn.startedAt * 1000 : undefined
        const durationMs = typeof turn.durationMs === 'number' ? turn.durationMs : undefined
        // Flush streamed text first — turn/completed can race ahead of item/completed.
        flushThreadDeltas(threadID)
        rememberPlanCandidatesFromTurn(threadID, turnID)
        patchTurnMetrics(threadID, turnID, { startedAt, completedAt, durationMs })
        completedTurns.add(turnID)
        const currentTurnID = activeTurnByThread.value[threadID] ?? ''
        const completedCurrentTurn = currentTurnID === turnID || (!currentTurnID && !isThreadSubmitting(threadID))
        if (currentTurnID === turnID) setThreadTurn(threadID, '')
        if (completedCurrentTurn || interruptingTurn.value) {
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
        }
        loadThreads().catch(() => undefined)
        workspaceStore.refreshWorkspace()
        scheduleThreadQueueDrain(threadID)
        break
      }
      case 'item/started':
      case 'item/completed': {
        const threadID = asString(payload.threadId)
        const turnID = asString(payload.turnId)
        const item = normalizeTimelineItem(payload.item, turnID)
        if (item) {
          // Commit any pending streamed text before a new item appears or a
          // completed snapshot merges — otherwise the UI can briefly/permanently
          // show a truncated prefix (漏字) while deltas sit in the 24ms buffer.
          if (method === 'item/completed') flushBufferedItem(threadID, item.id)
          else flushThreadDeltas(threadID)
          item.startedAt = typeof payload.startedAtMs === 'number' ? payload.startedAtMs : undefined
          item.completedAt = typeof payload.completedAtMs === 'number' ? payload.completedAtMs : undefined
          upsertItem(threadID, item)
          if (method === 'item/completed') {
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
          // Official TUI: saw_plan_update_this_turn also opens the implement prompt.
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
      case 'item/reasoning/summaryPartAdded':
        if (Number(payload.summaryIndex) <= 0) break
        queueDelta({
          threadId: asString(payload.threadId),
          turnId: asString(payload.turnId),
          itemId: asString(payload.itemId),
          field: 'reasoningSummary',
          type: 'reasoning',
          delta: '\n\n',
        })
        break
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
          if (!diffTimer) diffTimer = window.setTimeout(flushDiffs, 100)
        }
        break
      }
      case 'serverRequest/resolved':
        removePendingRequest(String(payload.requestId ?? ''))
        break
      case 'warning':
      case 'configWarning':
        notify('warning', translate('notifications.codexWarning'), asString(payload.message, translate('notifications.warningFallback')))
        break
      case 'guardianWarning':
      case 'deprecationNotice':
      case 'windows/worldWritableWarning':
        notify('warning', translate('notifications.codexWarning'), asString(payload.message, asString(payload.detail)))
        break
      case 'error':
      case 'turn/error': {
        const threadID = asString(payload.threadId)
        const turnID = asString(payload.turnId)
        const message = asString(asRecord(payload.error).message, translate('notifications.turnFailedFallback'))
        if (payload.willRetry === true) {
          if (turnID) setThreadTurn(threadID, turnID)
          setTurnFeedback(threadID, {
            state: 'retrying',
            message: `${translate('chat.retrying')} ${message}`,
            turnId: turnID,
          })
        } else {
          if (turnID) completedTurns.add(turnID)
          const currentTurnID = activeTurnByThread.value[threadID] ?? ''
          const failedCurrentTurn = currentTurnID === turnID
            || (!currentTurnID && !isThreadSubmitting(threadID))
            || !turnID
          if (!turnID || currentTurnID === turnID) setThreadTurn(threadID, '')
          if (failedCurrentTurn) {
            interruptingTurn.value = false
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
        queueTokenUsage(asString(payload.threadId), asString(payload.turnId), payload.tokenUsage)
        break
      case 'skills/changed':
      case 'app/list/updated':
        capabilitiesStore.scheduleRefresh()
        break
      case 'mcpServer/startupStatus/updated': {
        capabilitiesStore.handleMcpStatusUpdate(payload)
        break
      }
      case 'model/rerouted':
        updateThreadModelIdentity(
          asString(payload.threadId, activeThreadId.value),
          asString(payload.toModel),
          asString(payload.modelProvider),
        )
        notify(
          'info',
          translate('notifications.modelRerouted'),
          `${asString(payload.fromModel)} → ${asString(payload.toModel)} · ${asString(payload.reason)}`,
        )
        break
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

  function queueTokenUsage(threadID: string, turnID: string, value: unknown): void {
    if (!threadID) return
    const usage = normalizeThreadTokenUsage(value)
    pendingTokenUsage.set(`${threadID}:${turnID}`, { threadId: threadID, turnId: turnID, usage })
    if (!tokenUsageTimer) tokenUsageTimer = window.setTimeout(flushTokenUsage, 250)
  }

  function flushTokenUsage(): void {
    tokenUsageTimer = 0
    if (!pendingTokenUsage.size) return
    const next = { ...tokenUsageByThread.value }
    const nextMetrics = { ...turnMetricsByThread.value }
    for (const { threadId, turnId, usage } of pendingTokenUsage.values()) {
      next[threadId] = usage
      if (turnId) {
        const threadMetrics = { ...(nextMetrics[threadId] ?? {}) }
        const current = threadMetrics[turnId] ?? emptyTurnMetrics()
        threadMetrics[turnId] = { ...current, tokenUsage: usage.last }
        nextMetrics[threadId] = threadMetrics
      }
    }
    pendingTokenUsage.clear()
    tokenUsageByThread.value = next
    turnMetricsByThread.value = nextMetrics
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
    if (!deltaTimer) deltaTimer = window.setTimeout(flushDeltas, 24)
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

    const nextByThread = { ...itemsByThread.value }
    for (const [threadID, deltas] of grouped) {
      const nextItems = [...(nextByThread[threadID] ?? [])]
      for (const delta of deltas) {
        let index = nextItems.findIndex((item) => item.id === delta.itemId)
        if (index < 0) {
          nextItems.push({
            id: delta.itemId,
            turnId: delta.turnId,
            type: delta.type,
            status: 'inProgress',
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
      }
      nextByThread[threadID] = nextItems
    }
    itemsByThread.value = nextByThread
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
      window.clearTimeout(deltaTimer)
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
      await backend.UpdateSessionPreferences({
        sessionId: thread.id,
        model: thread.model || appStore.settings.model,
        effort: thread.effort || appStore.settings.effort,
        collaborationMode: next,
      })
    } catch {
      // Keep local mode usable even if persistence fails.
    }
  }

  function resolveThreadCollaborationMode(thread: ThreadSummary): 'default' | 'plan' {
    const live = activeThread.value?.id === thread.id ? activeThread.value : null
    const mode = live?.collaborationMode
      || thread.collaborationMode
      || findThreadSummary(thread.id)?.collaborationMode
      || appStore.settings.collaborationMode
      || 'default'
    return mode === 'plan' ? 'plan' : 'default'
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

  function threadIsPlanMode(threadID: string): boolean {
    const thread = activeThread.value?.id === threadID
      ? activeThread.value
      : findThreadSummary(threadID)
    const mode = thread?.collaborationMode || appStore.settings.collaborationMode
    return mode === 'plan'
  }

  function turnHasPlanItem(threadID: string, turnID: string): boolean {
    return (itemsByThread.value[threadID] ?? []).some((item) =>
      item.turnId === turnID && item.type === 'plan' && Boolean(item.text.trim()),
    )
  }

  function turnHasProposedPlan(threadID: string, turnID: string): boolean {
    return (itemsByThread.value[threadID] ?? []).some((item) =>
      item.turnId === turnID
      && item.type === 'agentMessage'
      && Boolean(extractProposedPlan(item.text)),
    )
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

  function clearPlanOfferRetries(key: string): void {
    const timers = planOfferRetryTimers.get(key)
    if (!timers) return
    for (const timer of timers) window.clearTimeout(timer)
    planOfferRetryTimers.delete(key)
  }

  function schedulePlanImplementationOffer(threadID: string, turnID: string): void {
    if (!threadID || !turnID) return
    const key = `${threadID}:${turnID}`
    clearPlanOfferRetries(key)
    maybeOfferPlanImplementation(threadID, turnID)
    // Late plan items / plan updates often arrive after turn/completed.
    const timers = [100, 350, 800].map((delay) => window.setTimeout(() => {
      maybeOfferPlanImplementation(threadID, turnID)
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

    rememberPlanCandidatesFromTurn(threadID, turnID)
    const pending = resolvePendingPlan(threadID, turnID)
    if (!pending?.text.trim()) return

    // Official: plan item OR update_plan OR <proposed_plan>; prefer while still in plan mode.
    const eligible = threadIsPlanMode(threadID)
      || turnHasPlanItem(threadID, turnID)
      || turnHasProposedPlan(threadID, turnID)
      || sawPlanUpdateByTurn.has(turnID)
    if (!eligible) return

    pendingPlanByThread.delete(threadID)
    planImplementPrompt.value = {
      threadId: threadID,
      turnId: turnID,
      planText: pending.text,
    }
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

  function setThreadMetrics(threadID: string, turns: unknown): void {
    const historical = metricsFromTurns(turns)
    const existing = turnMetricsByThread.value[threadID] ?? {}
    for (const [turnID, metrics] of Object.entries(historical)) {
      if (existing[turnID]?.tokenUsage) metrics.tokenUsage = existing[turnID].tokenUsage
    }
    turnMetricsByThread.value = { ...turnMetricsByThread.value, [threadID]: historical }
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
    while (loadedThreadIDs.size > 12 || cachedConversationWeight() > 8_000_000) {
      const evicted = [...loadedThreadIDs].find((id) => id !== threadID && id !== activeThreadId.value && !activeTurnByThread.value[id])
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
    delete nextItems[threadID]
    delete nextUsage[threadID]
    delete nextDiffs[threadID]
    delete nextMetrics[threadID]
    itemsByThread.value = nextItems
    tokenUsageByThread.value = nextUsage
    latestDiffByThread.value = nextDiffs
    turnMetricsByThread.value = nextMetrics
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
    const path = thread.cwd || appStore.settings.workspace
    const currentItems = sameWorkspace(path, appStore.settings.workspace)
      ? threads.value
      : projectThreadsForPath(path) ?? []
    const remaining = currentItems.filter((item) => item.id !== thread.id)
    const nextItems = [thread, ...remaining].sort((a, b) => b.updatedAt - a.updatedAt)
    setProjectThreads(path, nextItems)
    if (sameWorkspace(path, appStore.settings.workspace)) threads.value = nextItems
  }

  function setProjectThreads(path: string, nextThreads: ThreadSummary[]): void {
    const existingPath = Object.keys(projectThreads.value).find((projectPath) => sameWorkspace(projectPath, path))
    const key = existingPath ?? path
    projectThreads.value = { ...projectThreads.value, [key]: nextThreads }
  }

  function isThreadSubmitting(threadID: string): boolean {
    return sendingThreadIds.value.includes(threadID)
  }

  function setThreadSubmitting(threadID: string, submitting: boolean): void {
    if (!threadID) return
    if (submitting) {
      if (!sendingThreadIds.value.includes(threadID)) sendingThreadIds.value = [...sendingThreadIds.value, threadID]
      return
    }
    sendingThreadIds.value = sendingThreadIds.value.filter((id) => id !== threadID)
  }

  function threadIsRunning(threadID: string): boolean {
    return Boolean(activeTurnByThread.value[threadID])
  }

  function scheduleThreadQueueDrain(threadID: string): void {
    if (!threadID) return
    window.setTimeout(() => void drainThreadQueue(threadID), 0)
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
    const path = appStore.settings.workspace
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
    if (turnID) next[threadID] = turnID
    else delete next[threadID]
    activeTurnByThread.value = next
  }

  function setTurnFeedback(threadID: string, feedback: TurnFeedback): void {
    if (!threadID) return
    turnFeedbackByThread.value = { ...turnFeedbackByThread.value, [threadID]: feedback }
  }

  function clearTurnFeedback(threadID: string): void {
    if (!threadID || !turnFeedbackByThread.value[threadID]) return
    const next = { ...turnFeedbackByThread.value }
    delete next[threadID]
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
    threads.value = []
    archivedThreads.value = []
    itemsByThread.value = {}
    tokenUsageByThread.value = {}
    turnMetricsByThread.value = {}
    diffsByTurn.value = {}
    latestDiffByThread.value = {}
    loadedThreadIDs.clear()
    deltaBuffers.clear()
    pendingDiffs.clear()
    pendingTokenUsage.clear()
    completedTurns.clear()
    pendingPlanByThread.clear()
    sawPlanUpdateByTurn.clear()
    for (const timers of planOfferRetryTimers.values()) {
      for (const timer of timers) window.clearTimeout(timer)
    }
    planOfferRetryTimers.clear()
    planImplementPrompt.value = null
    if (deltaTimer) {
      window.clearTimeout(deltaTimer)
      deltaTimer = 0
    }
    if (diffTimer) {
      window.clearTimeout(diffTimer)
      diffTimer = 0
    }
    if (tokenUsageTimer) {
      window.clearTimeout(tokenUsageTimer)
      tokenUsageTimer = 0
    }
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
    activeQueuedMessages,
    activeThreadBusy,
    activeThreadUsesExternalProvider,
    activeTurnFeedback,
    activeTokenUsage,
    activeTurnMetrics,
    pendingRequest,
    planImplementPrompt,
    threadGroups,
    filteredThreadGroups,
    runningThreadIds,
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
    openThread,
    openProjectThread,
    switchProject,
    selectProject,
    clearActiveSession,
    switchWorkMode,
    patchActiveSessionPreferences,
    setCollaborationMode,
    dismissPlanImplementation,
    acceptPlanImplementation,
    forkActiveThread,
    archiveActiveThread,
    compactActiveThread,
    renameActiveThread,
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
      kind: ['codex', 'claude', 'gemini', 'grok', 'custom'].includes(kind)
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
  for (const path of [current, ...recent]) {
    const value = path.trim()
    const key = workspaceKey(value)
    if (!value || seen.has(key)) continue
    seen.add(key)
    result.push(value)
  }
  return result
}

function workspaceKey(path: string): string {
  const normalized = path.trim().replace(/\\/g, '/').replace(/\/+$/, '')
  return navigator.userAgent.includes('Windows') ? normalized.toLocaleLowerCase() : normalized
}

function sameWorkspace(left: string, right: string): boolean {
  return workspaceKey(left) === workspaceKey(right)
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

function loadLastThreadByWorkspace(): Record<string, string> {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem('nice-codex.lastThreads') || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    return Object.fromEntries(Object.entries(parsed).filter(([, value]) => typeof value === 'string'))
  } catch {
    return {}
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
