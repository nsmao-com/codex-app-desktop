import { Events } from '@wailsio/runtime'
import { defineStore } from 'pinia'
import { computed, shallowRef, watch } from 'vue'

import type {
  GrokMessage,
  GrokRuntimeStatus,
  GrokSessionDetail,
  GrokSessionSummary,
  GrokTurnRef,
} from '../../bindings/nice_codex_desktop/models'
import type { MessageAttachment, TimelineItem, TokenUsageBreakdown, TurnMetrics } from '@/types/codex'
import {
  archiveGrokSession as archiveGrokSessionApi,
  deleteGrokSession as deleteGrokSessionApi,
  interruptGrokTurn as interruptGrokTurnApi,
  isGrokTurnRunning as isGrokTurnRunningApi,
  listArchivedGrokSessions,
  listGrokSessionTurnUsages,
  listGrokSessions,
  readGrokSession,
  readGrokSessionHistory,
  readGrokTurnLiveness,
  refreshGrokRuntime,
  refreshGrokRuntimeQuick,
  renameGrokSession as renameGrokSessionApi,
  sendGrokMessage as sendGrokMessageApi,
  unarchiveGrokSession as unarchiveGrokSessionApi,
} from '@/utils/grokBindings'
import { resolveProviderModelContextWindow } from '@/utils/accountUsage'
import { normalizeThreadTokenUsage } from '@/utils/protocol'
import { notify } from '@/utils/notify'
import { friendlyErrorMessage } from '@/utils/errorMessage'
import { sameWorkspacePath, workspaceKey } from '@/utils/workspacePath'
import { translate } from '@/i18n'
import { useAppStore } from './app'
import { useArenaStore } from './arena'
import { useDialogStore } from './dialog'
import { useNavigationStore } from './navigation'
import { useWorkspaceStore } from './workspace'

const emptyTurnMetrics = (): TurnMetrics => ({
  tokenUsage: null,
  startedAt: null,
  completedAt: null,
  durationMs: null,
})

const GROK_TURN_WATCHDOG_MS = 2500
const GROK_TURN_WATCHDOG_CONFIRM_MS = 450

function errorMessage(error: unknown): string {
  return friendlyErrorMessage(error)
}

function emptyRuntime(): GrokRuntimeStatus {
  return {
    buildAvailable: false,
    buildAuthenticated: false,
    buildVersion: '',
    buildExecutable: '',
    apiConfigured: false,
  }
}

function fileStem(path: string): string {
  const name = path.split(/[\\/]/).filter(Boolean).at(-1) || path || 'file'
  return name
}

/** Map Grok Build history rows onto the same timeline types Codex uses. */
function messageToItem(message: GrokMessage, turnId: string): TimelineItem {
  const role = (message.role || '').toLowerCase()
  const isUser = role === 'user' || role === 'human'
  const isReasoning = role === 'reasoning'
  const toolName = (message.toolName || '').trim()
  const toolKind = (message.toolKind || '').toLowerCase()
  const isTool = role === 'tool' || Boolean(toolName)
  const text = message.text || ''
  const attachments = (message.images ?? []).map((source): MessageAttachment => ({
    kind: 'local',
    source,
    name: fileStem(source),
  }))
  const base = {
    id: message.id || `grok-msg-${message.createdAt}-${Math.random().toString(36).slice(2, 8)}`,
    // Must be unique per user turn — reusing sessionId makes every agent group
    // stream at once (thinking on top + lite markdown for the whole history).
    turnId,
    status: message.status || 'completed',
    text: '',
    command: '',
    cwd: '',
    output: '',
    title: '',
    detail: '',
    changes: [] as TimelineItem['changes'],
    attachments,
    startedAt: message.createdAt || undefined,
    completedAt: message.createdAt || undefined,
  }

  if (isUser) {
    return { ...base, type: 'userMessage', text }
  }
  if (isReasoning) {
    return { ...base, type: 'reasoning', text, reasoningSummary: text }
  }
  if (!isTool) {
    return { ...base, type: 'agentMessage', text }
  }

  const name = toolName || 'tool'
  const path = (message.path || '').trim()
  const command = (message.command || '').trim()
  const detail = (message.detail || '').trim()
  const diff = message.diff || ''

  // File edits (search_replace / write) → patch rows, not MCP.
  if (toolKind === 'file' || /^(search_replace|write|str_replace|apply_patch|edit_file)$/i.test(name)) {
    const filePath = path || detail || name
    return {
      ...base,
      type: 'fileChange',
      title: path ? `Applying patch to ${fileStem(path)}` : 'Applying patch',
      text: '',
      output: text,
      detail: filePath,
      changes: [{
        path: filePath,
        kind: /^write$/i.test(name) ? 'add' : 'update',
        diff: diff || text,
      }],
    }
  }

  // Shell → command execution row.
  if (toolKind === 'command' || /^(run_terminal_command|bash|shell|run_command)$/i.test(name)) {
    return {
      ...base,
      type: 'commandExecution',
      title: name,
      command: command || text.slice(0, 200),
      output: text,
      detail: command,
    }
  }

  // Web / X search.
  if (toolKind === 'search' || /^(web_search|web_fetch|web_open|x_)/i.test(name)) {
    return {
      ...base,
      type: 'webSearch',
      title: name,
      detail: detail || path || command,
      text,
      output: text,
    }
  }

  // Explicit MCP bridge tools only.
  if (toolKind === 'mcp' || /^(use_tool|search_tool)$/i.test(name) || name.startsWith('mcp')) {
    const label = detail || name
    return {
      ...base,
      type: 'mcpToolCall',
      title: label.includes('/') ? label : `MCP / ${label}`,
      detail: label,
      output: text,
      text: '',
    }
  }

  // Built-in agent tools (read_file, grep, todo_write, …) keep their real names.
  const pretty = humanizeGrokToolName(name)
  const target = path ? fileStem(path) : (detail || command)
  const label = target && pretty
    ? `${pretty} · ${target}`
    : (pretty || target || name)
  return {
    ...base,
    type: 'dynamicToolCall',
    title: name === 'tool' ? '' : name,
    detail: path || detail || command,
    output: text,
    text: label,
    command: command,
  }
}

function humanizeGrokToolName(name: string): string {
  const raw = name.trim()
  if (!raw || /^tool$/i.test(raw)) return ''
  return raw
    .split(/[_.\s-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function isActiveGrokStatus(status = ''): boolean {
  const normalized = status.toLowerCase().replace(/[_-]/g, '')
  return normalized === 'inprogress'
    || normalized === 'running'
    || normalized === 'started'
    || normalized === 'pending'
    || normalized === 'active'
}

function finalizeGrokMessages(messages: GrokMessage[], status: string): GrokMessage[] {
  let terminalStart = -1
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const role = (messages[index]?.role || '').toLowerCase()
    if (role === 'user' || role === 'human') {
      terminalStart = index
      break
    }
  }
  let changed = false
  const next = messages.map((message, index) => {
    if (!isActiveGrokStatus(message.status)) return message
    changed = true
    return { ...message, status: terminalStart >= 0 && index < terminalStart ? 'completed' : status }
  })
  return changed ? next : messages
}

function mergeGrokDiskWithCurrent(
  disk: GrokMessage[],
  current: GrokMessage[],
  preserveLiveMessages = true,
): GrokMessage[] {
  if (!current.length) return disk
  const result = [...disk]
  const diskById = new Map(
    disk.filter((message) => message.id).map((message) => [message.id, message]),
  )
  const consumedDiskUsers = new Set<GrokMessage>()
  const diskUsers = disk.filter((message) => {
    const role = (message.role || '').toLowerCase()
    return role === 'user' || role === 'human'
  })
  let latestDiskTime = 0
  for (const message of disk) latestDiskTime = Math.max(latestDiskTime, Number(message.createdAt) || 0)

  // Provider rows stay authoritative; optimistic rows are inserted after the
  // last shared message instead of being appended below a completed answer.
  let lastAnchor: GrokMessage | null = null
  for (const message of current) {
    const role = (message.role || '').toLowerCase()
    const isUser = role === 'user' || role === 'human'
    const idMatch = message.id ? diskById.get(message.id) : undefined
    if (idMatch) {
      lastAnchor = idMatch
      if (isUser) consumedDiskUsers.add(idMatch)
      continue
    }
    if (isUser) {
      const diskMatch = diskUsers.find((candidate) =>
        !consumedDiskUsers.has(candidate) && sameGrokUserText(candidate.text, message.text),
      )
      if (diskMatch) {
        consumedDiskUsers.add(diskMatch)
        const diskIndex = result.indexOf(diskMatch)
        // Keep the complete optimistic text if Grok persisted a shortened variant.
        if (diskIndex >= 0 && normalizeGrokUserText(diskMatch.text) !== normalizeGrokUserText(message.text)) {
          const restored = { ...diskMatch, text: message.text, images: message.images ?? diskMatch.images }
          result[diskIndex] = restored
          lastAnchor = restored
        } else {
          lastAnchor = diskMatch
        }
        continue
      }
      const anchorIndex = lastAnchor ? result.indexOf(lastAnchor) : -1
      const insertionIndex = anchorIndex >= 0 ? anchorIndex + 1 : 0
      result.splice(insertionIndex, 0, message)
      lastAnchor = message
      continue
    }
    if (preserveLiveMessages && (isActiveGrokStatus(message.status) || (Number(message.createdAt) || 0) >= latestDiskTime)) {
      const anchorIndex = lastAnchor ? result.indexOf(lastAnchor) : -1
      const insertionIndex = anchorIndex >= 0 ? anchorIndex + 1 : result.length
      result.splice(insertionIndex, 0, message)
      lastAnchor = message
    }
  }
  return result
}

function splitGrokHistoryPrefix(
  disk: GrokMessage[],
  current: GrokMessage[],
): { prefix: GrokMessage[], current: GrokMessage[] } {
  if (!disk.length || !current.length) return { prefix: [], current }
  const currentByID = new Map<string, number>()
  current.forEach((message, index) => {
    if (message.id) currentByID.set(message.id, index)
  })
  let diskIndex = -1
  let currentIndex = -1
  for (let index = 0; index < disk.length; index += 1) {
    const message = disk[index]
    const byID = message.id ? currentByID.get(message.id) : undefined
    const role = (message.role || '').toLowerCase()
    const match = byID ?? ((role === 'user' || role === 'human')
      ? current.findIndex((row) => {
          const rowRole = (row.role || '').toLowerCase()
          return (rowRole === 'user' || rowRole === 'human')
            && sameGrokUserText(row.text, message.text)
        })
      : -1)
    if (match >= 0) {
      diskIndex = index
      currentIndex = match
      break
    }
  }
  if (diskIndex < 0 || currentIndex < 0) return { prefix: [], current }
  const prefixLength = Math.max(0, currentIndex - diskIndex)
  return {
    prefix: current.slice(0, prefixLength),
    current: current.slice(prefixLength),
  }
}

function normalizeGrokUserText(text = ''): string {
  return text.trim().replace(/\r\n/g, '\n')
}

function sameGrokUserText(left = '', right = ''): boolean {
  const a = normalizeGrokUserText(left)
  const b = normalizeGrokUserText(right)
  if (a === b) return true
  const shorter = a.length <= b.length ? a : b
  const longer = a.length > b.length ? a : b
  // Grok Build can persist only a small prefix/suffix of very long prompts.
  // A 70% ratio rejected those rows and produced a duplicate user bubble.
  if (shorter.length < 256) return false
  return longer.startsWith(shorter) || longer.endsWith(shorter)
}

export interface GrokSessionGroup {
  path: string
  name: string
  active: boolean
  sessions: GrokSessionSummary[]
}

/** Local follow-up queue while a Grok turn is running (Codex-style). */
export interface GrokQueuedMessage {
  id: string
  sessionId: string
  backend: 'build' | 'api'
  workspace: string
  model: string
  effort: string
  text: string
  images: string[]
  state: 'queued' | 'sending' | 'failed'
  error: string
  createdAt: number
  blockedByTurnId?: string
  /** Timeline user row already injected for this queue item. */
  localAppended?: boolean
}

interface GrokHistoryState {
  start: number
  total: number
  turnOffset: number
  hasEarlier: boolean
  loadingEarlier: boolean
  loadedUpdatedAt: number
  backend: 'build' | 'api'
}

function sanitizeGrokThoughtText(text: string): string {
  const lines = text.split(/\r?\n/)
  return lines
    .filter((line, index) => {
      const normalized = line.trim().toLowerCase().replace(/[\s,.;:!?_-]+/g, '')
      if (normalized === 'clear') return false
      // Streaming may deliver "clear,,," one character at a time. Hold its
      // unfinished final line so c/cl/clea never flashes in the reasoning row.
      return index !== lines.length - 1 || !normalized || !'clear'.startsWith(normalized)
    })
    .join('\n')
}

function workspaceLeafName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

/** Remove assistant segments already committed in native activity from the live cumulative stream. */
function liveTextTailAfterActivity(fullText: string, activity: GrokMessage[]): string {
  if (!fullText) return ''
  let cursor = 0
  let matched = false
  for (const message of activity) {
    const role = (message.role || '').toLowerCase()
    if (role !== 'assistant' || message.toolName) continue
    const segment = (message.text || '').trim()
    if (!segment) continue
    const index = fullText.indexOf(segment, cursor)
    if (index < 0) {
      // Keep the unmatched stream suffix visible if a provider normalized whitespace
      // differently between streaming-json and chat_history.
      return matched ? fullText.slice(cursor).trimStart() : fullText
    }
    cursor = index + segment.length
    matched = true
  }
  return matched ? fullText.slice(cursor).trimStart() : fullText
}

export const useGrokStore = defineStore('grok', () => {
  const appStore = useAppStore()
  const arenaStore = useArenaStore()
  const navigationStore = useNavigationStore()
  const dialogStore = useDialogStore()
  const workspaceStore = useWorkspaceStore()

  const runtime = shallowRef<GrokRuntimeStatus>(emptyRuntime())
  const sessions = shallowRef<GrokSessionSummary[]>([])
  const archivedSessions = shallowRef<GrokSessionSummary[]>([])
  const activeSessionId = shallowRef('')
  const messagesBySession = shallowRef<Record<string, GrokMessage[]>>({})
  const historyBySession = shallowRef<Record<string, GrokHistoryState>>({})
  const loadingSessionId = shallowRef('')
  const sessionMutationBySession = shallowRef<Record<string, string>>({})
  const sendingSessionIds = shallowRef<string[]>([])
  const runningSessionIdsState = shallowRef<string[]>([])
  const interruptingSessionIds = shallowRef<string[]>([])
  const activeTurnBySession = shallowRef<Record<string, GrokTurnRef | undefined>>({})
  const liveTextBySession = shallowRef<Record<string, string>>({})
  /** Live Grok Build thought stream (planning shimmer label). */
  const liveThoughtBySession = shallowRef<Record<string, string>>({})
  /** Ordered provider-history assistant/reasoning/tool rows for the active turn. */
  const liveActivityBySession = shallowRef<Record<string, GrokMessage[]>>({})
  /** Per-session follow-up queue — drain after turn.completed / failed / interrupted. */
  const queuedBySession = shallowRef<Record<string, GrokQueuedMessage[]>>({})
  /** Cumulative text snapshots + sequence numbers protect against bridge reordering. */
  const streamSequenceByTurn = new Map<string, number>()
  const pendingLiveText = new Map<string, string>()
  const pendingLiveThought = new Map<string, string | null>()
  const finalizedTurnIds = new Set<string>()
  const clientTurnIdByTurnId = new Map<string, string>()
  const latestStartedTurnBySession = new Map<string, string>()
  const turnWatchdogTimers = new Map<string, number>()
  /** Timeline turnId (`session:tN`) → metrics (tokens + duration). */
  const turnMetricsByKey = shallowRef<Record<string, TurnMetrics>>({})
  /** Backend turnId → wall-clock start for duration when completed. */
  const turnStartedAtById = shallowRef<Record<string, number>>({})
  /** Session-level cumulative token usage (inspector). */
  const tokenUsageBySession = shallowRef<Record<string, ReturnType<typeof normalizeThreadTokenUsage>>>({})
  const search = shallowRef('')
  let eventUnsub: (() => void) | null = null
  let liveStreamFlushTimer = 0
  let sessionsLoadedAt = 0
  let sessionsLoadedKey = ''
  let sessionLoadSequence = 0
  let archivedSessionLoadSequence = 0
  const sessionOpenSequence = new Map<string, number>()
  const loadingSequenceBySession = new Map<string, number>()
  let enterInFlight: Promise<void> | null = null
  let queuedSequence = 0
  /** pending/local session id → native Grok session id (and reverse lookups). */
  const sessionAlias = new Map<string, string>()
  const loadedSessionIds = new Set<string>()

  function rememberFinalizedGrokTurn(turnId: string): void {
    if (!turnId) return
    cancelGrokTurnWatchdog(turnId)
    finalizedTurnIds.delete(turnId)
    finalizedTurnIds.add(turnId)
    while (finalizedTurnIds.size > 512) {
      const oldest = finalizedTurnIds.values().next().value
      if (!oldest) break
      finalizedTurnIds.delete(oldest)
      clientTurnIdByTurnId.delete(oldest)
    }
  }

  function rememberGrokTurnClientId(turnId: string, clientTurnId: string): void {
    if (!turnId || !clientTurnId) return
    clientTurnIdByTurnId.delete(turnId)
    clientTurnIdByTurnId.set(turnId, clientTurnId)
    while (clientTurnIdByTurnId.size > 1024) {
      const oldest = clientTurnIdByTurnId.keys().next().value
      if (!oldest) break
      clientTurnIdByTurnId.delete(oldest)
    }
  }

  function clientTurnIdForTurn(turn: GrokTurnRef | null): string {
    if (!turn?.turnId) return ''
    if (turn.turnId.startsWith('grok-turn-pending-')) return turn.turnId
    return clientTurnIdByTurnId.get(turn.turnId) || ''
  }

  function grokEventMatchesCurrentTurn(sessionId: string, turnId: string, clientTurnId: string): boolean {
    const currentTurn = turnForSession(sessionId)
    if (!currentTurn?.turnId || !turnId || currentTurn.turnId === turnId) return true
    const currentClientTurnId = clientTurnIdForTurn(currentTurn)
    const incomingClientTurnId = clientTurnId || clientTurnIdByTurnId.get(turnId) || ''
    if (currentClientTurnId && incomingClientTurnId) {
      return currentClientTurnId === incomingClientTurnId
    }
    // Different provider turn ids cannot own the same session concurrently.
    // Keep compatibility for an early delta whose pending id has not been echoed yet.
    return currentTurn.turnId.startsWith('grok-turn-pending-')
  }

  const backendId = computed(() => {
    const value = (appStore.settings.grokBackend || 'build').toLowerCase()
    return value === 'api' ? 'api' : 'build'
  })

  const workspacePath = computed(() =>
    appStore.settings.grokWorkspace || appStore.settings.workspace || '',
  )

  function resetBackendConversationState(): void {
    if (liveStreamFlushTimer) window.clearTimeout(liveStreamFlushTimer)
    liveStreamFlushTimer = 0
    cancelAllGrokTurnWatchdogs()
    activeSessionId.value = ''
    loadingSessionId.value = ''
    sessions.value = []
    archivedSessions.value = []
    messagesBySession.value = {}
    historyBySession.value = {}
    sessionMutationBySession.value = {}
    sendingSessionIds.value = []
    runningSessionIdsState.value = []
    interruptingSessionIds.value = []
    activeTurnBySession.value = {}
    liveTextBySession.value = {}
    liveThoughtBySession.value = {}
    liveActivityBySession.value = {}
    queuedBySession.value = {}
    turnMetricsByKey.value = {}
    turnStartedAtById.value = {}
    tokenUsageBySession.value = {}
    streamSequenceByTurn.clear()
    pendingLiveText.clear()
    pendingLiveThought.clear()
    finalizedTurnIds.clear()
    clientTurnIdByTurnId.clear()
    latestStartedTurnBySession.clear()
    sessionOpenSequence.clear()
    loadingSequenceBySession.clear()
    sessionAlias.clear()
    loadedSessionIds.clear()
    sessionsLoadedAt = 0
    sessionsLoadedKey = ''
    sessionLoadSequence += 1
    archivedSessionLoadSequence += 1
  }

  watch(workspacePath, (next, previous) => {
    if (!previous || sameWorkspacePath(next, previous) || !activeSessionId.value) return
    const selected = sessions.value.find((item) => sameGrokSession(item.id, activeSessionId.value))
    if (selected?.workspace && sameWorkspacePath(selected.workspace, next)) return
    // Running turns continue in the background, but a project switch must not leave
    // another project's conversation wired to this project's composer/stop button.
    activeSessionId.value = ''
    loadingSessionId.value = ''
  }, { flush: 'sync' })

  watch([backendId, () => appStore.bootstrapping], ([next, bootstrapping]) => {
    if (bootstrapping) return
    const previous = (arenaStore.runtimeBackends.grok || '').trim().toLowerCase()
    arenaStore.syncRuntimeBackend('grok', next)
    if (!previous || previous === next) return
    navigationStore.clearRuntimeSessions('grok', translate('sidebar.newTask'))
    resetBackendConversationState()
    if (appStore.isGrokMode) {
      void loadSessions(true)
      void loadArchivedSessions()
    }
  }, { immediate: true, flush: 'sync' })

  function resolveSessionId(id: string): string {
    const raw = id.trim()
    if (!raw) return ''
    const seen = new Set<string>()
    let current = raw
    while (!seen.has(current)) {
      seen.add(current)
      const next = (sessionAlias.get(current) || '').trim()
      if (!next || next === current) break
      current = next
    }
    for (const alias of seen) sessionAlias.set(alias, current)
    return current
  }

  function rememberSessionAlias(fromId: string, toId: string): void {
    const from = fromId.trim()
    const to = resolveSessionId(toId)
    if (!from || !to || from === to) return
    sessionAlias.set(from, to)
    sessionAlias.set(to, to)
    for (const alias of sessionAlias.keys()) resolveSessionId(alias)
  }

  function sameGrokSession(left: string, right: string): boolean {
    const a = resolveSessionId(left)
    const b = resolveSessionId(right)
    return Boolean(a && b && a === b)
  }

  function isSessionTurnBusy(sessionId: string): boolean {
    const id = resolveSessionId(sessionId)
    if (!id) return false
    return sendingSessionIds.value.some((key) => sameGrokSession(key, id))
      || runningSessionIdsState.value.some((key) => sameGrokSession(key, id))
      || Object.keys(activeTurnBySession.value).some((key) => sameGrokSession(key, id))
  }

  function isSessionLoading(sessionId: string): boolean {
    const id = resolveSessionId(sessionId)
    if (!id) return false
    return Array.from(loadingSequenceBySession.keys()).some((key) => sameGrokSession(key, id))
  }

  function isSessionBusy(sessionId: string): boolean {
    const id = resolveSessionId(sessionId)
    if (!id) return false
    return isSessionLoading(id) || isSessionTurnBusy(id)
  }

  function isSessionInterrupting(sessionId: string): boolean {
    return interruptingSessionIds.value.some((id) => sameGrokSession(id, sessionId))
  }

  function patchSessionPreferences(sessionId: string, model: string, effort: string): void {
    if (!sessionId) return
    sessions.value = sessions.value.map((session) =>
      sameGrokSession(session.id, sessionId)
        ? { ...session, model: model || session.model, effort: effort || session.effort }
        : session,
    )
  }

  function sessionMutationForSession(sessionId: string): string {
    if (!sessionId) return ''
    const entry = Object.entries(sessionMutationBySession.value)
      .find(([id]) => sameGrokSession(id, sessionId))
    return entry?.[1] ?? ''
  }

  function beginSessionMutation(sessionId: string, mutation: string): boolean {
    if (!sessionId || sessionMutationForSession(sessionId)) return false
    sessionMutationBySession.value = {
      ...sessionMutationBySession.value,
      [sessionId]: mutation,
    }
    return true
  }

  function endSessionMutation(sessionId: string): void {
    const key = Object.keys(sessionMutationBySession.value)
      .find((id) => sameGrokSession(id, sessionId))
    if (!key) return
    const next = { ...sessionMutationBySession.value }
    delete next[key]
    sessionMutationBySession.value = next
  }

  const sessionMutation = computed(() => sessionMutationForSession(activeSessionId.value))

  function rememberLoadedGrokSession(sessionId: string): void {
    const id = resolveSessionId(sessionId) || sessionId.trim()
    if (!id) return
    for (const cached of [...loadedSessionIds]) {
      if (sameGrokSession(cached, id)) loadedSessionIds.delete(cached)
    }
    loadedSessionIds.add(id)
    while (loadedSessionIds.size > 12 || cachedGrokConversationWeight() > 32_000_000) {
      const evicted = [...loadedSessionIds].find((candidate) =>
        !sameGrokSession(candidate, id)
        && !sameGrokSession(candidate, activeSessionId.value)
        && !isSessionBusy(candidate)
        && !Object.entries(queuedBySession.value).some(([key, queue]) =>
          sameGrokSession(key, candidate) && queue.length > 0,
        ),
      )
      if (!evicted) break
      evictCachedGrokSession(evicted)
    }
  }

  function cachedGrokConversationWeight(): number {
    let total = 0
    for (const messages of Object.values(messagesBySession.value)) {
      for (const message of messages) {
        total += (message.text || '').length + (message.diff || '').length
        total += (message.path || '').length + (message.command || '').length + (message.detail || '').length
        total += (message.toolName || '').length + (message.toolKind || '').length
        total += (message.images || []).reduce((sum, image) => sum + image.length, 0)
      }
    }
    return total
  }

  function evictCachedGrokSession(sessionId: string): void {
    const related = new Set<string>([
      sessionId,
      ...Object.keys(messagesBySession.value).filter((id) => sameGrokSession(id, sessionId)),
      ...Object.keys(historyBySession.value).filter((id) => sameGrokSession(id, sessionId)),
    ])
    const relatedList = [...related]
    const matches = (id: string) => relatedList.some((key) => sameGrokSession(id, key))
    const withoutRelated = <T>(bucket: Record<string, T>): Record<string, T> =>
      Object.fromEntries(Object.entries(bucket).filter(([id]) => !matches(id)))

    messagesBySession.value = withoutRelated(messagesBySession.value)
    historyBySession.value = withoutRelated(historyBySession.value)
    tokenUsageBySession.value = withoutRelated(tokenUsageBySession.value)
    liveTextBySession.value = withoutRelated(liveTextBySession.value)
    liveThoughtBySession.value = withoutRelated(liveThoughtBySession.value)
    liveActivityBySession.value = withoutRelated(liveActivityBySession.value)

    const nextMetrics = { ...turnMetricsByKey.value }
    for (const key of Object.keys(nextMetrics)) {
      if (relatedList.some((id) => key.startsWith(`${id}:`))) delete nextMetrics[key]
    }
    turnMetricsByKey.value = nextMetrics
    for (const id of [...loadedSessionIds]) if (matches(id)) loadedSessionIds.delete(id)
    for (const id of [...pendingLiveText.keys()]) if (matches(id)) pendingLiveText.delete(id)
    for (const id of [...pendingLiveThought.keys()]) if (matches(id)) pendingLiveThought.delete(id)
    for (const id of [...sessionOpenSequence.keys()]) if (matches(id)) sessionOpenSequence.delete(id)
    for (const id of [...latestStartedTurnBySession.keys()]) if (matches(id)) latestStartedTurnBySession.delete(id)
    for (const [alias, target] of [...sessionAlias.entries()]) {
      if (related.has(alias) || related.has(target)) sessionAlias.delete(alias)
    }
  }

  function sessionStateKey<T>(bucket: Record<string, T>, sessionId: string): string {
    const id = sessionId.trim()
    if (!id) return ''
    if (bucket[id] !== undefined) return id
    const resolved = resolveSessionId(id)
    if (resolved && bucket[resolved] !== undefined) return resolved
    return Object.keys(bucket).find((key) => sameGrokSession(key, id)) || resolved || id
  }

  function relatedSessionKeys<T>(bucket: Record<string, T>, sessionId: string): string[] {
    const id = sessionId.trim()
    if (!id) return []
    return Object.keys(bucket).filter((key) => sameGrokSession(key, id))
  }

  function mergedSessionMessages(sessionId: string, preserveLiveMessages = true): GrokMessage[] {
    const keys = relatedSessionKeys(messagesBySession.value, sessionId)
    if (!keys.length) return []
    if (keys.length === 1) return messagesBySession.value[keys[0]!] ?? []

    // Prefer the provider-backed history as the merge base, then layer optimistic
    // user rows from any pending/temporary alias over it.
    const ranked = keys.map((key) => {
      const messages = messagesBySession.value[key] ?? []
      const providerRows = messages.reduce((count, message) =>
        count + (message.id?.startsWith('grok-user-') ? 0 : 1), 0)
      return { key, messages, providerRows }
    }).sort((left, right) =>
      right.providerRows - left.providerRows || right.messages.length - left.messages.length,
    )
    let merged = ranked[0]?.messages ?? []
    for (const entry of ranked.slice(1)) {
      merged = mergeGrokDiskWithCurrent(merged, entry.messages, preserveLiveMessages)
    }
    return merged
  }

  function replaceSessionMessages(sessionId: string, messages: GrokMessage[]): void {
    const id = resolveSessionId(sessionId) || sessionId.trim()
    if (!id) return
    const next = { ...messagesBySession.value }
    for (const key of relatedSessionKeys(next, id)) delete next[key]
    next[id] = messages
    messagesBySession.value = next
  }

  function turnForSession(sessionId: string): GrokTurnRef | null {
    const key = sessionStateKey(activeTurnBySession.value, sessionId)
    return (key && activeTurnBySession.value[key]) || null
  }

  function latestStartedTurnForSession(sessionId: string): string {
    let latest = ''
    for (const [id, turnId] of latestStartedTurnBySession) {
      if (sameGrokSession(id, sessionId)) latest = turnId
    }
    return latest
  }

  function markSessionState(target: typeof sendingSessionIds, sessionId: string, active: boolean): void {
    const id = resolveSessionId(sessionId) || sessionId.trim()
    if (!id) return
    const next = target.value.filter((key) => !sameGrokSession(key, id))
    if (active) next.push(id)
    target.value = next
  }

  function setSessionTurn(sessionId: string, turn: GrokTurnRef): void {
    const id = resolveSessionId(sessionId) || sessionId.trim()
    if (!id) return
    const next = { ...activeTurnBySession.value }
    for (const key of Object.keys(next)) {
      if (key !== id && sameGrokSession(key, id)) delete next[key]
    }
    next[id] = { ...turn, sessionId: id }
    activeTurnBySession.value = next
    markSessionState(runningSessionIdsState, id, true)
  }

  function promoteSessionState(fromId: string, toId: string): string {
    const rawFrom = fromId.trim()
    const from = resolveSessionId(rawFrom) || rawFrom
    const wasActive = Boolean(activeSessionId.value && sameGrokSession(activeSessionId.value, rawFrom))
    rememberSessionAlias(from, toId)
    rememberSessionAlias(rawFrom, toId)
    const to = resolveSessionId(toId)
    if (!from || !to) {
      if (wasActive && to) activeSessionId.value = to
      return to || from
    }
    if (from === to) {
      if (wasActive) activeSessionId.value = to
      return to
    }

    const messageKeys = relatedSessionKeys(messagesBySession.value, to)
    if (messageKeys.length) replaceSessionMessages(to, mergedSessionMessages(to))

    const historyKeys = relatedSessionKeys(historyBySession.value, to)
    if (historyKeys.length) {
      const next = { ...historyBySession.value }
      const history = historyKeys
        .map((key) => next[key])
        .filter((value): value is GrokHistoryState => Boolean(value))
        .sort((left, right) => left.start - right.start)[0]
      for (const key of historyKeys) delete next[key]
      if (history) next[to] = { ...history, loadingEarlier: false }
      historyBySession.value = next
    }

    const mergeTextBucket = (bucket: Record<string, string>): Record<string, string> => {
      const keys = relatedSessionKeys(bucket, to)
      if (!keys.length) return bucket
      const next = { ...bucket }
      let longest = ''
      for (const key of keys) {
        const value = next[key] || ''
        if (value.length >= longest.length) longest = value
        delete next[key]
      }
      next[to] = longest
      return next
    }
    liveTextBySession.value = mergeTextBucket(liveTextBySession.value)
    liveThoughtBySession.value = mergeTextBucket(liveThoughtBySession.value)
    const pendingTextKeys = [...pendingLiveText.keys()].filter((key) => sameGrokSession(key, to))
    if (pendingTextKeys.length) {
      let longest = ''
      for (const key of pendingTextKeys) {
        const value = pendingLiveText.get(key) || ''
        if (value.length >= longest.length) longest = value
        pendingLiveText.delete(key)
      }
      pendingLiveText.set(to, longest)
    }
    const pendingThoughtKeys = [...pendingLiveThought.keys()].filter((key) => sameGrokSession(key, to))
    if (pendingThoughtKeys.length) {
      let longest = ''
      let cleared = false
      for (const key of pendingThoughtKeys) {
        const value = pendingLiveThought.get(key)
        if (value === null) cleared = true
        else if ((value || '').length >= longest.length) longest = value || ''
        pendingLiveThought.delete(key)
      }
      pendingLiveThought.set(to, cleared ? null : longest)
    }

    const activityKeys = relatedSessionKeys(liveActivityBySession.value, to)
    if (activityKeys.length) {
      const orderedKeys = [to, ...activityKeys.filter((key) => key !== to)]
      const merged: GrokMessage[] = []
      const ids = new Set<string>()
      const next = { ...liveActivityBySession.value }
      for (const key of orderedKeys) {
        for (const message of next[key] ?? []) {
          if (message.id && ids.has(message.id)) continue
          if (message.id) ids.add(message.id)
          merged.push(message)
        }
        delete next[key]
      }
      next[to] = merged
      liveActivityBySession.value = next
    }

    const usageKeys = relatedSessionKeys(tokenUsageBySession.value, to)
    if (usageKeys.length) {
      const next = { ...tokenUsageBySession.value }
      const usage = next[to] ?? next[usageKeys[0]!]
      for (const key of usageKeys) delete next[key]
      if (usage) next[to] = usage
      tokenUsageBySession.value = next
    }
    for (const alias of sessionAlias.keys()) {
      if (alias !== to && sameGrokSession(alias, to)) remapTurnMetricsSession(alias, to)
    }

    const queueKeys = relatedSessionKeys(queuedBySession.value, to)
    if (queueKeys.length) {
      const next = { ...queuedBySession.value }
      const merged: GrokQueuedMessage[] = []
      const ids = new Set<string>()
      for (const key of [to, ...queueKeys.filter((item) => item !== to)]) {
        for (const item of next[key] ?? []) {
          if (ids.has(item.id)) continue
          ids.add(item.id)
          merged.push({ ...item, sessionId: to })
        }
        delete next[key]
      }
      merged.sort((left, right) => {
        if ((left.state === 'sending') !== (right.state === 'sending')) {
          return left.state === 'sending' ? -1 : 1
        }
        return left.createdAt - right.createdAt
      })
      next[to] = merged
      queuedBySession.value = next
    }

    const turnKeys = relatedSessionKeys(activeTurnBySession.value, to)
    if (turnKeys.length) {
      const next = { ...activeTurnBySession.value }
      const turns = turnKeys.map((key) => next[key]).filter((turn): turn is GrokTurnRef => Boolean(turn))
      const turn = turns.find((candidate) => !candidate.turnId.startsWith('grok-turn-pending-'))
        ?? next[to]
        ?? turns[0]
      for (const key of turnKeys) delete next[key]
      if (turn) next[to] = { ...turn, sessionId: to }
      activeTurnBySession.value = next
      if (turn) scheduleGrokTurnWatchdog({ ...turn, sessionId: to })
    }
    const wasSending = sendingSessionIds.value.some((id) => sameGrokSession(id, to))
    const wasRunning = runningSessionIdsState.value.some((id) => sameGrokSession(id, to))
    const wasInterrupting = interruptingSessionIds.value.some((id) => sameGrokSession(id, to))
    markSessionState(sendingSessionIds, to, false)
    markSessionState(runningSessionIdsState, to, false)
    markSessionState(interruptingSessionIds, to, false)
    if (wasSending) markSessionState(sendingSessionIds, to, true)
    if (wasRunning) markSessionState(runningSessionIdsState, to, true)
    if (wasInterrupting) markSessionState(interruptingSessionIds, to, true)

    const deduped = new Map<string, GrokSessionSummary>()
    for (const summary of sessions.value) {
      const patched = sameGrokSession(summary.id, to) ? { ...summary, id: to } : summary
      const previous = deduped.get(patched.id)
      deduped.set(patched.id, previous ? { ...patched, ...previous } : patched)
    }
    sessions.value = [...deduped.values()]
    // Invalidate reads started under the optimistic id. Their finally blocks keep
    // the original loading key until they return, so re-keying it here would leave
    // the real session permanently stuck in a loading state.
    for (const key of [...sessionOpenSequence.keys()]) {
      if (key !== to && sameGrokSession(key, to)) sessionOpenSequence.delete(key)
    }
    if (loadingSessionId.value && sameGrokSession(loadingSessionId.value, to)) loadingSessionId.value = to
    if (wasActive) activeSessionId.value = to
    for (const id of [...loadedSessionIds]) {
      if (id !== to && sameGrokSession(id, to)) loadedSessionIds.delete(id)
    }
    rememberLoadedGrokSession(to)
    return to
  }

  function clearTurnState(sessionId = '', turnId = ''): void {
    const key = sessionStateKey(activeTurnBySession.value, sessionId)
    const turn = (key && activeTurnBySession.value[key]) || null
    if (!turn) {
      cancelGrokTurnWatchdog(turnId)
      // A terminal event may arrive after the turn map was already reconciled.
      // Its per-session sending/running markers are still safe to release when
      // no newer turn owns this session.
      if (sessionId) {
        markSessionState(sendingSessionIds, sessionId, false)
        markSessionState(runningSessionIdsState, sessionId, false)
        markSessionState(interruptingSessionIds, sessionId, false)
      }
      return
    }
    // Stale completion/interrupt must not kill a newer turn on the same session.
    if (turnId && turn.turnId && turn.turnId !== turnId) {
      cancelGrokTurnWatchdog(turnId)
      return
    }
    if (sessionId && !sameGrokSession(turn.sessionId, sessionId)) {
      return
    }
    cancelGrokTurnWatchdog(turn.turnId)
    const next = { ...activeTurnBySession.value }
    delete next[key]
    activeTurnBySession.value = next
    if (turn.turnId && turnStartedAtById.value[turn.turnId] !== undefined) {
      const nextStarts = { ...turnStartedAtById.value }
      delete nextStarts[turn.turnId]
      turnStartedAtById.value = nextStarts
    }
    streamSequenceByTurn.delete(`${turn.turnId}:text`)
    streamSequenceByTurn.delete(`${turn.turnId}:thought`)
    markSessionState(sendingSessionIds, turn.sessionId, false)
    markSessionState(runningSessionIdsState, turn.sessionId, false)
    markSessionState(interruptingSessionIds, turn.sessionId, false)
  }

  function cancelGrokTurnWatchdog(turnId: string): void {
    const id = turnId.trim()
    if (!id) return
    const timer = turnWatchdogTimers.get(id)
    if (timer) window.clearTimeout(timer)
    turnWatchdogTimers.delete(id)
  }

  function cancelAllGrokTurnWatchdogs(): void {
    for (const timer of turnWatchdogTimers.values()) window.clearTimeout(timer)
    turnWatchdogTimers.clear()
  }

  function scheduleGrokTurnWatchdog(
    ref: GrokTurnRef,
    delay = GROK_TURN_WATCHDOG_MS,
    confirmingMissing = false,
  ): void {
    const turnId = ref.turnId.trim()
    if (!turnId || finalizedTurnIds.has(turnId)) return
    cancelGrokTurnWatchdog(turnId)
    let timer = 0
    timer = window.setTimeout(() => {
      if (turnWatchdogTimers.get(turnId) !== timer) return
      turnWatchdogTimers.delete(turnId)
      void reconcileGrokTurnLiveness(ref, confirmingMissing)
    }, delay)
    turnWatchdogTimers.set(turnId, timer)
  }

  async function reconcileGrokTurnLiveness(ref: GrokTurnRef, confirmingMissing: boolean): Promise<void> {
    const current = turnForSession(ref.sessionId)
    if (!current || current.turnId !== ref.turnId || finalizedTurnIds.has(ref.turnId)) return
    try {
      const liveness = await readGrokTurnLiveness(ref)
      if (liveness.running) {
        const latestBeforeApply = turnForSession(ref.sessionId)
        if (!latestBeforeApply) return
        if (latestBeforeApply.turnId !== ref.turnId) {
          if (liveness.turnId === latestBeforeApply.turnId) scheduleGrokTurnWatchdog(latestBeforeApply)
          return
        }
        const runningTurnId = liveness.turnId || latestBeforeApply.turnId
        if (runningTurnId !== latestBeforeApply.turnId) {
          rememberGrokTurnClientId(runningTurnId, clientTurnIdForTurn(latestBeforeApply) || latestBeforeApply.turnId)
          remapQueuedBlockingTurn(latestBeforeApply.sessionId, latestBeforeApply.turnId, runningTurnId)
          const startedAt = turnStartedAtById.value[latestBeforeApply.turnId]
          if (startedAt) {
            const nextStarts = { ...turnStartedAtById.value, [runningTurnId]: startedAt }
            delete nextStarts[latestBeforeApply.turnId]
            turnStartedAtById.value = nextStarts
          }
          setSessionTurn(latestBeforeApply.sessionId, { ...latestBeforeApply, turnId: runningTurnId })
        }
        const latest = turnForSession(latestBeforeApply.sessionId)
        if (latest) scheduleGrokTurnWatchdog(latest)
        return
      }
    } catch {
      const latest = turnForSession(ref.sessionId)
      if (latest?.turnId === ref.turnId) scheduleGrokTurnWatchdog(latest)
      return
    }

    const latest = turnForSession(ref.sessionId)
    if (!latest || latest.turnId !== ref.turnId || finalizedTurnIds.has(ref.turnId)) return
    if (!confirmingMissing) {
      scheduleGrokTurnWatchdog(latest, GROK_TURN_WATCHDOG_CONFIRM_MS, true)
      return
    }
    recoverMissingGrokTerminal(latest)
  }

  function recoverMissingGrokTerminal(ref: GrokTurnRef): void {
    const current = turnForSession(ref.sessionId)
    if (!current || current.turnId !== ref.turnId || finalizedTurnIds.has(ref.turnId)) return
    const sessionId = resolveSessionId(current.sessionId) || current.sessionId
    rememberFinalizedGrokTurn(ref.turnId)
    flushLiveStreams()
    applyTurnUsageMetrics(sessionId, ref.turnId, {})
    clearTurnState(sessionId, ref.turnId)
    resetLiveSessionState(sessionId)
    void appStore.loadLocalUsage()
    void (async () => {
      if (current.backend === backendId.value) {
        try {
          if (sessionId && !sessionId.startsWith('pending-grok-')) {
            await openSession(sessionId, { terminalStatus: 'completed', activate: false })
          } else {
            await loadSessions(true)
          }
        } catch {
          // The run is already gone; queue release must not depend on history refresh.
        }
      }
      await drainQueue(sessionId)
    })()
  }

  function snapshotActiveTurnId(value: unknown): string {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return ''
    return String((value as Record<string, unknown>).activeTurnId || '').trim()
  }

  function reconcileSnapshotTurn(
    sessionId: string,
    backend: 'build' | 'api',
    snapshotTurnId: string,
    observedTurnId = '',
  ): void {
    const id = resolveSessionId(sessionId) || sessionId.trim()
    if (!id) return
    const snapshot = snapshotTurnId.trim()
    if (snapshot) {
      const current = turnForSession(id)
      if (current?.turnId !== snapshot || current.backend !== backend) {
        setSessionTurn(id, { backend, sessionId: id, turnId: snapshot })
      } else {
        markSessionState(runningSessionIdsState, id, true)
      }
      if (!turnStartedAtById.value[snapshot]) {
        turnStartedAtById.value = { ...turnStartedAtById.value, [snapshot]: Date.now() }
        seedTurnStartMetrics(id, snapshot)
      }
      scheduleGrokTurnWatchdog({ backend, sessionId: id, turnId: snapshot })
      return
    }
    if (!observedTurnId || observedTurnId.startsWith('grok-turn-pending-')) return
    const current = turnForSession(id)
    if (current?.turnId === observedTurnId && current.backend === backend) {
      clearTurnState(id, observedTurnId)
    }
  }

  const activeMessages = computed(() => {
    return mergedSessionMessages(activeSessionId.value)
  })
  const activeQueuedMessages = computed(() => {
    const id = activeSessionId.value
    if (!id) return [] as GrokQueuedMessage[]
    const direct = queuedBySession.value[id] ?? []
    const resolved = resolveSessionId(id)
    if (resolved && resolved !== id) {
      const extra = queuedBySession.value[resolved] ?? []
      if (extra.length) return [...direct, ...extra]
    }
    return direct
  })

  /** Metrics keyed by timeline turn id (`session:tN`) for ChatMessageGroup. */
  const activeTurnMetrics = computed(() => {
    const sessionId = activeSessionId.value
    if (!sessionId) return {} as Record<string, TurnMetrics>
    const prefix = `${sessionId}:`
    const resolved = resolveSessionId(sessionId)
    const out: Record<string, TurnMetrics> = {}
    for (const [key, metrics] of Object.entries(turnMetricsByKey.value)) {
      if (key.startsWith(prefix)) {
        out[key] = metrics
        continue
      }
      if (resolved && key.startsWith(`${resolved}:`)) {
        // Alias metrics under the active session key so timeline lookups hit.
        const suffix = key.slice(resolved.length)
        out[`${sessionId}${suffix}`] = metrics
      }
    }
    return out
  })

  const activeTokenUsage = computed(() => {
    const id = activeSessionId.value
    if (!id) return null
    const usage = tokenUsageBySession.value[id]
      ?? tokenUsageBySession.value[resolveSessionId(id)]
      ?? null
    if (!usage) return null
    const resolved = resolveSessionId(id)
    const summary = sessions.value.find((item) => item.id === id || item.id === resolved)
      ?? archivedSessions.value.find((item) => item.id === id || item.id === resolved)
    const provider = appStore.agentProviders.find((item) => item.kind === 'grok')
    const model = summary?.model
      || (backendId.value === 'api' ? appStore.settings.grokAPIModel : appStore.settings.grokBuildModel)
      || provider?.models?.find((item) => item.isDefault)?.model
      || provider?.models?.[0]?.model
      || ''
    const contextWindow = resolveProviderModelContextWindow(appStore.agentProviders, 'grok', model)
    return contextWindow > 0 && usage.modelContextWindow !== contextWindow
      ? { ...usage, modelContextWindow: contextWindow }
      : usage
  })

  const activeHistoryState = computed(() => {
    const key = sessionStateKey(historyBySession.value, activeSessionId.value)
    return (key && historyBySession.value[key]) || null
  })
  const activeHistoryHasEarlier = computed(() => activeHistoryState.value?.hasEarlier === true)
  const activeHistoryEarlierCount = computed(() => activeHistoryState.value?.turnOffset ?? 0)
  const activeHistoryLoadingEarlier = computed(() => activeHistoryState.value?.loadingEarlier === true)

  const activeHistoryItems = computed<TimelineItem[]>(() => {
    const sessionId = activeSessionId.value
    let turn = activeHistoryState.value?.turnOffset ?? 0
    const items: TimelineItem[] = []
    const messages = activeMessages.value
    for (const message of messages) {
      const role = (message.role || '').toLowerCase()
      if (role === 'user' || role === 'human') turn += 1
      else if (turn === 0) turn = 1
      items.push(messageToItem(message, `${sessionId}:t${turn}`))
    }
    return items
  })
  const activeHistoryTurnCount = computed(() => {
    let count = activeHistoryState.value?.turnOffset ?? 0
    for (const message of activeMessages.value) {
      const role = (message.role || '').toLowerCase()
      if (role === 'user' || role === 'human') count += 1
    }
    return count
  })

  function historyItemsForSession(sessionId: string): TimelineItem[] {
    const id = (sessionId || '').trim()
    if (!id) return []
    const historyKey = sessionStateKey(historyBySession.value, id)
    const historyState = (historyKey && historyBySession.value[historyKey]) || null
    let turn = historyState?.turnOffset ?? 0
    const items: TimelineItem[] = []
    for (const message of mergedSessionMessages(id)) {
      const role = (message.role || '').toLowerCase()
      if (role === 'user' || role === 'human') turn += 1
      else if (turn === 0) turn = 1
      items.push(messageToItem(message, `${id}:t${turn}`))
    }
    return items
  }

  function historyTurnCountForSession(sessionId: string): number {
    const id = (sessionId || '').trim()
    if (!id) return 0
    const historyKey = sessionStateKey(historyBySession.value, id)
    const historyState = (historyKey && historyBySession.value[historyKey]) || null
    let count = historyState?.turnOffset ?? 0
    for (const message of mergedSessionMessages(id)) {
      const role = (message.role || '').toLowerCase()
      if (role === 'user' || role === 'human') count += 1
    }
    return count
  }

  /** Timeline for any session (arena multi-pane can bind non-global active ids). */
  function itemsForSession(sessionId: string): TimelineItem[] {
    const id = (sessionId || '').trim()
    if (!id) return []
    // Active session keeps the optimized live-tail path (stable history refs).
    if (sameGrokSession(id, activeSessionId.value)) return activeItems.value

    const historyItems = historyItemsForSession(id)
    const turn = Math.max(historyTurnCountForSession(id), historyItems.length ? 1 : 0)
    const turnRef = turnForSession(id)
    if (!turnRef || !sameGrokSession(turnRef.sessionId, id)) return historyItems

    const items = [...historyItems]
    const liveKeys = [id, turnRef.sessionId, resolveSessionId(turnRef.sessionId)].filter(Boolean)
    let liveRaw = ''
    let thought = ''
    let liveActivity: GrokMessage[] = []
    for (const key of liveKeys) {
      if (!liveRaw && liveTextBySession.value[key]) liveRaw = liveTextBySession.value[key]
      if (!thought && liveThoughtBySession.value[key]) thought = liveThoughtBySession.value[key]
      if (!liveActivity.length && liveActivityBySession.value[key]?.length) {
        liveActivity = liveActivityBySession.value[key] || []
      }
    }
    const live = liveTextTailAfterActivity(liveRaw, liveActivity)
    const liveTurn = turn > 0 ? turn : 1
    const hasNativeReasoning = liveActivity.some((message) =>
      (message.role || '').toLowerCase() === 'reasoning',
    )
    if (thought && !live && !hasNativeReasoning) {
      items.push({
        id: `grok-thought-${turnRef.turnId}`,
        turnId: `${id}:t${liveTurn}`,
        type: 'reasoning',
        status: 'inProgress',
        text: thought,
        command: '',
        cwd: '',
        output: '',
        title: '',
        detail: '',
        changes: [],
        attachments: [],
        reasoningSummary: thought,
      })
    }
    for (const message of liveActivity) {
      items.push(messageToItem(message, `${id}:t${liveTurn}`))
    }
    if (live) {
      items.push({
        id: `grok-live-${turnRef.turnId}`,
        turnId: `${id}:t${liveTurn}`,
        type: 'agentMessage',
        status: 'inProgress',
        text: live,
        command: '',
        cwd: '',
        output: '',
        title: '',
        detail: '',
        changes: [],
        attachments: [],
      })
    }
    return items
  }

  const activeItems = computed<TimelineItem[]>(() => {
    const sessionId = activeSessionId.value
    // Persisted history keeps stable object references while only the live tail
    // changes. This prevents every Grok delta from rebuilding old timeline rows.
    const historyItems = activeHistoryItems.value
    const turn = Math.max(activeHistoryTurnCount.value, historyItems.length ? 1 : 0)

    const turnRef = turnForSession(sessionId)
    const liveOnActive = Boolean(
      turnRef
      && sessionId
      && sameGrokSession(turnRef.sessionId, sessionId),
    )
    if (!liveOnActive || !turnRef) return historyItems

    const items = [...historyItems]
    if (liveOnActive && turnRef) {
      const liveKeys = [sessionId, turnRef.sessionId, resolveSessionId(turnRef.sessionId)].filter(Boolean)
      let liveRaw = ''
      let thought = ''
      let liveActivity: GrokMessage[] = []
      for (const key of liveKeys) {
        if (!liveRaw && liveTextBySession.value[key]) liveRaw = liveTextBySession.value[key]
        if (!thought && liveThoughtBySession.value[key]) thought = liveThoughtBySession.value[key]
        if (!liveActivity.length && liveActivityBySession.value[key]?.length) {
          liveActivity = liveActivityBySession.value[key] || []
        }
      }
      const live = liveTextTailAfterActivity(liveRaw, liveActivity)
      const liveTurn = turn > 0 ? turn : 1

      const hasNativeReasoning = liveActivity.some((message) =>
        (message.role || '').toLowerCase() === 'reasoning',
      )
      if (thought && !live && !hasNativeReasoning) {
        items.push({
          id: `grok-thought-${turnRef.turnId}`,
          turnId: `${sessionId}:t${liveTurn}`,
          type: 'reasoning',
          status: 'inProgress',
          text: thought,
          command: '',
          cwd: '',
          output: '',
          title: '',
          detail: '',
          changes: [],
          attachments: [],
          reasoningSummary: thought,
        })
      }
      for (const message of liveActivity) {
        items.push(messageToItem(message, `${sessionId}:t${liveTurn}`))
      }
      // Native activity owns committed interleaving; this row is only the stream
      // suffix that has not reached chat_history yet.
      if (live) {
        items.push({
          id: `grok-live-${turnRef.turnId}`,
          turnId: `${sessionId}:t${liveTurn}`,
          type: 'agentMessage',
          status: 'inProgress',
          text: live,
          command: '',
          cwd: '',
          output: '',
          title: '',
          detail: '',
          changes: [],
          attachments: [],
        })
      }
    }
    return items
  })

  /** Group sessions by workspace so the sidebar matches Codex project folders. */
  const sessionGroups = computed<GrokSessionGroup[]>(() => {
    const activePath = workspacePath.value
    const buckets = new Map<string, { path: string; sessions: GrokSessionSummary[] }>()
    for (const session of sessions.value) {
      const path = (session.workspace || '').trim() || '(unknown)'
      const key = workspaceKey(path)
      const bucket = buckets.get(key)
      if (bucket) {
        bucket.sessions.push(session)
      } else {
        buckets.set(key, { path, sessions: [session] })
      }
    }
    // Always surface the active Grok workspace, even when it has no sessions yet.
    if (activePath && !buckets.has(workspaceKey(activePath))) {
      buckets.set(workspaceKey(activePath), { path: activePath, sessions: [] })
    }
    const groups = [...buckets.values()].map(({ path, sessions: list }) => ({
      path,
      name: workspaceLeafName(path),
      active: activePath ? sameWorkspacePath(path, activePath) : false,
      sessions: [...list].sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0)),
    }))
    const orderedPaths = appStore.orderWorkspacePaths(
      'grok',
      groups.map((group) => group.path),
      appStore.settings.grokRecentWorkspaces ?? [],
    )
    const order = new Map(orderedPaths.map((path, index) => [workspaceKey(path), index]))
    groups.sort((left, right) =>
      (order.get(workspaceKey(left.path)) ?? Number.MAX_SAFE_INTEGER)
      - (order.get(workspaceKey(right.path)) ?? Number.MAX_SAFE_INTEGER),
    )
    return groups
  })

  const activeTurn = computed(() => turnForSession(activeSessionId.value))
  const sending = computed(() =>
    Boolean(activeSessionId.value && sendingSessionIds.value.some((id) => sameGrokSession(id, activeSessionId.value))),
  )
  const isTurnRunning = computed(() =>
    Boolean(activeSessionId.value && runningSessionIdsState.value.some((id) => sameGrokSession(id, activeSessionId.value))),
  )
  const interrupting = computed(() =>
    Boolean(activeSessionId.value && interruptingSessionIds.value.some((id) => sameGrokSession(id, activeSessionId.value))),
  )
  /** Session ids currently running a turn — drives the sidebar green pulse. */
  const runningSessionIds = computed(() => [...new Set(runningSessionIdsState.value.map(resolveSessionId).filter(Boolean))])
  const isReady = computed(() => {
    if (backendId.value === 'api') return runtime.value.apiConfigured
    return runtime.value.buildAvailable
  })

  function unwrapEventPayload(raw: unknown): Record<string, unknown> {
    if (Array.isArray(raw)) {
      const first = raw[0]
      if (first && typeof first === 'object' && !Array.isArray(first)) {
        return first as Record<string, unknown>
      }
      return {}
    }
    if (raw && typeof raw === 'object') return raw as Record<string, unknown>
    return {}
  }

  function bootstrapEvents(): void {
    if (eventUnsub) return
    eventUnsub = Events.On('grok:event', (event) => {
      handleEvent(unwrapEventPayload(event?.data))
    })
  }

  function dispose(): void {
    eventUnsub?.()
    eventUnsub = null
    if (liveStreamFlushTimer) window.clearTimeout(liveStreamFlushTimer)
    liveStreamFlushTimer = 0
    cancelAllGrokTurnWatchdogs()
    pendingLiveText.clear()
    pendingLiveThought.clear()
  }

  function scheduleLiveStreamFlush(): void {
    if (!liveStreamFlushTimer) liveStreamFlushTimer = window.setTimeout(flushLiveStreams, 48)
  }

  function flushLiveStreams(): void {
    if (liveStreamFlushTimer) window.clearTimeout(liveStreamFlushTimer)
    liveStreamFlushTimer = 0
    if (pendingLiveText.size) {
      const next = { ...liveTextBySession.value }
      for (const [key, text] of pendingLiveText) next[key] = text
      pendingLiveText.clear()
      liveTextBySession.value = next
    }
    if (pendingLiveThought.size) {
      const next = { ...liveThoughtBySession.value }
      for (const [key, thought] of pendingLiveThought) {
        if (thought === null) delete next[key]
        else next[key] = thought
      }
      pendingLiveThought.clear()
      liveThoughtBySession.value = next
    }
  }

  function resetLiveSessionState(sessionId: string): void {
    const keys = [...new Set([sessionId, resolveSessionId(sessionId)].filter(Boolean))]
    const nextText = { ...liveTextBySession.value }
    const nextThought = { ...liveThoughtBySession.value }
    const nextActivity = { ...liveActivityBySession.value }
    for (const key of keys) {
      pendingLiveText.delete(key)
      pendingLiveThought.delete(key)
      delete nextText[key]
      delete nextThought[key]
      delete nextActivity[key]
    }
    liveTextBySession.value = nextText
    liveThoughtBySession.value = nextThought
    liveActivityBySession.value = nextActivity
  }

  function liveWriteKey(eventSessionId: string): string {
    // Event routing must not depend on whichever conversation the user is viewing.
    return resolveSessionId(eventSessionId) || eventSessionId
  }

  function handleEvent(payload: Record<string, unknown>): void {
    const type = String(payload.type || '')
    const eventBackend = String(payload.backend || '').toLowerCase() === 'api' ? 'api' : 'build'
    // Build/API runs have separate identities. Once startup has restored the
    // persisted backend, delayed events from the previous backend must not
    // resurrect its turn state in the currently selected backend.
    if (!appStore.bootstrapping && eventBackend !== backendId.value) return
    const sessionId = String(payload.sessionId || '')
    const turnId = String(payload.turnId || '')
    const data = (payload.data && typeof payload.data === 'object' && !Array.isArray(payload.data))
      ? payload.data as Record<string, unknown>
      : {}
    const eventClientTurnId = String(data.clientTurnId || '')
    if (turnId && eventClientTurnId) rememberGrokTurnClientId(turnId, eventClientTurnId)

    if (type === 'turn.started') {
      const clientSessionId = String(data.clientSessionId || '')
      if (
        clientSessionId.startsWith('pending-grok-')
        && sessionId
        && sessionId !== clientSessionId
      ) {
        promoteSessionState(clientSessionId, sessionId)
      }
      const currentTurn = turnForSession(sessionId)
      const currentClientTurnId = clientTurnIdForTurn(currentTurn)
      if (
        eventClientTurnId
        && currentClientTurnId
        && currentClientTurnId !== eventClientTurnId
      ) {
        // A delayed start from the interrupted turn must not replace the new
        // pending/active turn created by an immediate resend.
        return
      }
      const startedSessionId = liveWriteKey(sessionId) || sessionId
      if (startedSessionId && turnId) latestStartedTurnBySession.set(startedSessionId, turnId)
      // A very fast terminal event may beat this notification across the bridge.
      // Turn ids are unique, so never resurrect one that is already finalized.
      if (turnId && finalizedTurnIds.has(turnId)) {
        const pendingTurn = turnForSession(sessionId)
        if (
          pendingTurn?.turnId.startsWith('grok-turn-pending-')
          && (!eventClientTurnId || pendingTurn.turnId === eventClientTurnId)
        ) {
          remapQueuedBlockingTurn(sessionId, pendingTurn.turnId, turnId)
          rememberFinalizedGrokTurn(pendingTurn.turnId)
          clearTurnState(sessionId, pendingTurn.turnId)
        }
        return
      }
      const key = startedSessionId
      if (turnId) {
        streamSequenceByTurn.delete(`${turnId}:text`)
        streamSequenceByTurn.delete(`${turnId}:thought`)
      }
      const sameTurn = Boolean(
        currentTurn
        && turnId
        && currentTurn.turnId === turnId,
      )
      // Frontend locks with grok-turn-pending-* before the API returns the real id.
      // That upgrade must NOT wipe buffers if any early deltas already landed.
      const upgradingFromPending = Boolean(
        currentTurn
        && turnId
        && currentTurn.turnId.startsWith('grok-turn-pending-')
        && (!eventClientTurnId || currentTurn.turnId === eventClientTurnId),
      )
      if (upgradingFromPending && currentTurn?.turnId && turnId) {
        remapQueuedBlockingTurn(sessionId, currentTurn.turnId, turnId)
      }
      setSessionTurn(key, {
        backend: eventBackend,
        sessionId: key,
        turnId,
      })
      scheduleGrokTurnWatchdog({ backend: eventBackend, sessionId: key, turnId })
      markSessionState(sendingSessionIds, key, true)
      if (!sameTurn && !upgradingFromPending) {
        pendingLiveText.delete(key)
        pendingLiveThought.delete(key)
        liveTextBySession.value = { ...liveTextBySession.value, [key]: '' }
        liveThoughtBySession.value = { ...liveThoughtBySession.value, [key]: '' }
        liveActivityBySession.value = { ...liveActivityBySession.value, [key]: [] }
      }
      if (turnId) {
        const nextStarts = { ...turnStartedAtById.value }
        let startedAt = nextStarts[turnId] || Date.now()
        if (upgradingFromPending && currentTurn?.turnId) {
          startedAt = nextStarts[currentTurn.turnId] || startedAt
          delete nextStarts[currentTurn.turnId]
        }
        nextStarts[turnId] = startedAt
        turnStartedAtById.value = nextStarts
      }
      // Seed duration clock on the logical timeline turn (user message already local).
      seedTurnStartMetrics(key, turnId)
      return
    }
    if (type === 'thought.delta') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      if (!grokEventMatchesCurrentTurn(sessionId, turnId, eventClientTurnId)) return
      const delta = String(data.delta || data.text || data.data || '')
      if (!sessionId || !delta) return
      const key = liveWriteKey(sessionId) || sessionId
      const currentTurn = turnForSession(sessionId)
      if (!currentTurn) {
        setSessionTurn(key, {
          backend: eventBackend,
          sessionId: key,
          turnId: turnId || `grok-turn-${Date.now()}`,
        })
        markSessionState(sendingSessionIds, key, true)
        if (turnId) scheduleGrokTurnWatchdog({ backend: eventBackend, sessionId: key, turnId })
      }
      const pendingThought = pendingLiveThought.get(key)
      const prev = pendingThought === null
        ? ''
        : (pendingThought ?? liveThoughtBySession.value[key] ?? '')
      const sequence = Number(data.sequence || 0)
      const streamKey = `${turnId || key}:thought`
      if (sequence > 0) {
        const previousSequence = streamSequenceByTurn.get(streamKey) || 0
        if (sequence <= previousSequence) return
        streamSequenceByTurn.set(streamKey, sequence)
      }
      const snapshot = String(data.text || '')
      // Cap thought buffer so long reasoning doesn't bloat the UI state.
      const next = sanitizeGrokThoughtText(sequence > 0 && snapshot ? snapshot : prev + delta).slice(-4000)
      pendingLiveThought.set(key, next)
      scheduleLiveStreamFlush()
      return
    }
    if (type === 'activity.snapshot') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      if (!sessionId) return
      if (!grokEventMatchesCurrentTurn(sessionId, turnId, eventClientTurnId)) return
      const key = liveWriteKey(sessionId) || sessionId
      // A snapshot can cross the bridge before chat_history contains the new
      // user row. Never append rows already owned by committed history; doing so
      // briefly duplicates the previous assistant answer after the new prompt.
      const historyIds = new Set(
        mergedSessionMessages(key).map((message) => message.id).filter(Boolean),
      )
      const messages = (Array.isArray(data.messages) ? data.messages as GrokMessage[] : [])
        .filter((message) => !message.id || !historyIds.has(message.id))
      liveActivityBySession.value = { ...liveActivityBySession.value, [key]: messages }
      return
    }
    if (type === 'text.delta') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      if (!grokEventMatchesCurrentTurn(sessionId, turnId, eventClientTurnId)) return
      const delta = String(data.delta || data.text || data.data || '')
      if (!sessionId || !delta) return
      const key = liveWriteKey(sessionId) || sessionId
      // Keep activeTurn pinned even if turn.started was missed.
      const currentTurn = turnForSession(sessionId)
      if (!currentTurn) {
        setSessionTurn(key, {
          backend: eventBackend,
          sessionId: key,
          turnId: turnId || `grok-turn-${Date.now()}`,
        })
        markSessionState(sendingSessionIds, key, true)
        if (turnId) scheduleGrokTurnWatchdog({ backend: eventBackend, sessionId: key, turnId })
      }
      const streamKey = `${turnId || key}:text`
      const sequence = Number(data.sequence || 0)
      if (sequence > 0) {
        const previousSequence = streamSequenceByTurn.get(streamKey) || 0
        if (sequence <= previousSequence) return
        streamSequenceByTurn.set(streamKey, sequence)
      }
      const prev = pendingLiveText.get(key) ?? liveTextBySession.value[key] ?? ''
      const snapshot = String(data.text || '')
      const nextLive = sequence > 0 && snapshot ? snapshot : prev + delta
      pendingLiveText.set(key, nextLive)
      // Answer tokens started — drop thought stream from the live row.
      if (pendingLiveThought.has(key) || liveThoughtBySession.value[key]) pendingLiveThought.set(key, null)
      scheduleLiveStreamFlush()
      return
    }
    if (type === 'session.bound') {
      if (!grokEventMatchesCurrentTurn(sessionId, turnId, eventClientTurnId)) return
      flushLiveStreams()
      const nextId = String(data.sessionId || '')
      if (nextId && nextId !== sessionId) {
        promoteSessionState(sessionId, nextId)
      }
      if (eventBackend === backendId.value) void loadSessions()
      return
    }
    if (type === 'turn.completed' || type === 'turn.failed' || type === 'turn.interrupted') {
      const terminalTurn = turnForSession(sessionId)
      const activeClientTurnId = clientTurnIdForTurn(terminalTurn)
      if (
        eventClientTurnId
        && activeClientTurnId
        && activeClientTurnId !== eventClientTurnId
      ) {
        // The interrupted turn finished after a resend already claimed this
        // session. Finalize only the old blocker; preserve the new turn state.
        rememberFinalizedGrokTurn(turnId)
        streamSequenceByTurn.delete(`${turnId}:text`)
        streamSequenceByTurn.delete(`${turnId}:thought`)
        applyTurnUsageMetrics(liveWriteKey(sessionId) || sessionId, turnId, data)
        void appStore.loadLocalUsage()
        void drainQueue(sessionId)
        return
      }
      if (turnId && finalizedTurnIds.has(turnId)) {
        clearTurnState(sessionId, turnId)
        void drainQueue(sessionId)
        return
      }
      flushLiveStreams()
      // Ignore stale completion for a turn that is no longer active (or never was).
      if (turnId && terminalTurn?.turnId && terminalTurn.turnId !== turnId && !terminalTurn.turnId.startsWith('grok-turn-pending-')) {
        // The old turn still releases queue rows explicitly blocked by its id,
        // but it must not clear the newer turn that now owns this session.
        rememberFinalizedGrokTurn(turnId)
        return
      }
      if (turnId && terminalTurn?.turnId.startsWith('grok-turn-pending-') && terminalTurn.turnId !== turnId) {
        remapQueuedBlockingTurn(sessionId, terminalTurn.turnId, turnId)
        rememberFinalizedGrokTurn(terminalTurn.turnId)
      }
      rememberFinalizedGrokTurn(turnId)
      streamSequenceByTurn.delete(`${turnId}:text`)
      streamSequenceByTurn.delete(`${turnId}:thought`)
      const key = liveWriteKey(sessionId) || sessionId
      // Capture metrics before clearTurnState wipes activeTurn.
      applyTurnUsageMetrics(key, turnId, data)
      // Release the turn lock immediately so a slow openSession cannot race with
      // the next turn (stale finally must not cancel a newer in-flight run).
      clearTurnState(sessionId, terminalTurn?.turnId || turnId)
      const targetSession = key || sessionId
      const terminalBackend = terminalTurn?.backend === 'api' ? 'api' : eventBackend
      // Drop live buffers — authoritative order comes from chat_history on disk
      // (tools interleaved with assistant segments). Do NOT inject a concatenated
      // live dump here; that reorders text relative to tools.
      if (sessionId || key) {
        const keys = [...new Set([sessionId, key, resolveSessionId(sessionId)].filter(Boolean))]
        const nextThought = { ...liveThoughtBySession.value }
        const nextLive = { ...liveTextBySession.value }
        const nextActivity = { ...liveActivityBySession.value }
        for (const liveKey of keys) {
          delete nextThought[liveKey]
          delete nextLive[liveKey]
          delete nextActivity[liveKey]
        }
        liveThoughtBySession.value = nextThought
        liveTextBySession.value = nextLive
        liveActivityBySession.value = nextActivity
      }
      if (type === 'turn.failed') {
        notify('error', translate('chat.turnFailed'), friendlyErrorMessage(data.message))
      }
      // Reload sidebar usage after local usage.json is updated by the backend.
      void appStore.loadLocalUsage()
      // Reload history first, then drain queue so openSession cannot wipe the next user row.
      void (async () => {
        if (terminalBackend === backendId.value) {
          try {
            if (targetSession && !targetSession.startsWith('pending-grok-')) {
              const terminalStatus = type === 'turn.failed'
                ? 'failed'
                : type === 'turn.interrupted'
                  ? 'interrupted'
                  : 'completed'
              await openSession(targetSession, { terminalStatus, activate: false })
            } else {
              await loadSessions(true)
            }
          } catch {
            // openSession already notifies; still try to drain.
          }
        }
        await drainQueue(targetSession)
      })()
    }
  }

  function timelineTurnKey(sessionId: string, turnIndex: number): string {
    return `${sessionId}:t${Math.max(1, turnIndex)}`
  }

  function countUserTurns(sessionId: string): number {
    const messages = mergedSessionMessages(sessionId)
    const historyKey = sessionStateKey(historyBySession.value, sessionId)
    let count = historyKey ? (historyBySession.value[historyKey]?.turnOffset ?? 0) : 0
    for (const message of messages) {
      const role = (message.role || '').toLowerCase()
      if (role === 'user' || role === 'human') count += 1
    }
    return count
  }

  function seedTurnStartMetrics(sessionId: string, turnId: string): void {
    if (!sessionId) return
    const turnIndex = Math.max(1, countUserTurns(sessionId))
    const key = timelineTurnKey(sessionId, turnIndex)
    const startedAt = turnId && turnStartedAtById.value[turnId]
      ? turnStartedAtById.value[turnId]
      : Date.now()
    const current = turnMetricsByKey.value[key] ?? emptyTurnMetrics()
    turnMetricsByKey.value = {
      ...turnMetricsByKey.value,
      [key]: {
        ...current,
        startedAt: current.startedAt ?? startedAt,
      },
    }
  }

  function parseUsageBreakdown(data: Record<string, unknown>): TokenUsageBreakdown | null {
    const nested = data.tokenUsage ?? data.usage ?? data.token_usage
    if (!nested || typeof nested !== 'object') return null
    const usage = normalizeThreadTokenUsage(nested)
    const last = usage.last
    const total = Number(last.totalTokens)
      || (last.inputTokens + last.cachedInputTokens + last.outputTokens + last.reasoningOutputTokens)
    if (total <= 0
      && last.inputTokens <= 0
      && last.outputTokens <= 0
      && last.cachedInputTokens <= 0
      && last.reasoningOutputTokens <= 0) {
      // tokenUsage may already be the breakdown itself (no last/total wrapper).
      const direct = normalizeThreadTokenUsage({ last: nested, total: nested })
      const d = direct.last
      const dTotal = Number(d.totalTokens)
        || (d.inputTokens + d.cachedInputTokens + d.outputTokens + d.reasoningOutputTokens)
      if (dTotal <= 0 && d.inputTokens <= 0 && d.outputTokens <= 0) return null
      return { ...d, totalTokens: dTotal || d.totalTokens }
    }
    return { ...last, totalTokens: total || last.totalTokens }
  }

  function applyTurnUsageMetrics(
    sessionId: string,
    turnId: string,
    data: Record<string, unknown>,
  ): void {
    if (!sessionId) return
    const turnIndex = Math.max(1, countUserTurns(sessionId))
    const key = timelineTurnKey(sessionId, turnIndex)
    const completedAt = Date.now()
    const startedAt = (turnId && turnStartedAtById.value[turnId])
      || turnMetricsByKey.value[key]?.startedAt
      || completedAt
    const durationMs = Math.max(0, completedAt - startedAt)
    const tokenUsage = parseUsageBreakdown(data)
    const current = turnMetricsByKey.value[key] ?? emptyTurnMetrics()
    turnMetricsByKey.value = {
      ...turnMetricsByKey.value,
      [key]: {
        ...current,
        startedAt,
        completedAt,
        durationMs,
        tokenUsage: tokenUsage ?? current.tokenUsage,
      },
    }
    if (tokenUsage) {
      const previous = tokenUsageBySession.value[sessionId]
      const prevTotal = previous?.total
      const nextTotal: TokenUsageBreakdown = prevTotal
        ? {
            inputTokens: prevTotal.inputTokens + tokenUsage.inputTokens,
            cachedInputTokens: prevTotal.cachedInputTokens + tokenUsage.cachedInputTokens,
            outputTokens: prevTotal.outputTokens + tokenUsage.outputTokens,
            reasoningOutputTokens: prevTotal.reasoningOutputTokens + tokenUsage.reasoningOutputTokens,
            totalTokens: prevTotal.totalTokens + tokenUsage.totalTokens,
          }
        : { ...tokenUsage }
      tokenUsageBySession.value = {
        ...tokenUsageBySession.value,
        [sessionId]: {
          total: nextTotal,
          last: tokenUsage,
          modelContextWindow: previous?.modelContextWindow ?? null,
        },
      }
    }
    if (turnId && turnStartedAtById.value[turnId] !== undefined) {
      const nextStarts = { ...turnStartedAtById.value }
      delete nextStarts[turnId]
      turnStartedAtById.value = nextStarts
    }
  }

  function remapTurnMetricsSession(fromId: string, toId: string): void {
    if (!fromId || !toId || fromId === toId) return
    const prefix = `${fromId}:`
    let changed = false
    const next = { ...turnMetricsByKey.value }
    for (const key of Object.keys(turnMetricsByKey.value)) {
      if (!key.startsWith(prefix)) continue
      const suffix = key.slice(fromId.length)
      const target = `${toId}${suffix}`
      if (!next[target]) next[target] = next[key]
      delete next[key]
      changed = true
    }
    if (changed) turnMetricsByKey.value = next
  }

  async function refreshRuntime(): Promise<void> {
    try {
      runtime.value = await refreshGrokRuntime()
      // Keep Settings badge in sync (Bootstrap snapshot can be stale / PATH-incomplete).
      const ready = runtime.value.buildAvailable || runtime.value.apiConfigured
      const list = [...(appStore.agentProviders ?? [])]
      const index = list.findIndex((item) => item.kind === 'grok')
      const next = {
        id: 'grok',
        name: 'Grok',
        kind: 'grok',
        installed: ready,
        healthy: ready,
        runtimeReady: ready,
        version: runtime.value.buildVersion || '',
        executable: runtime.value.buildExecutable || '',
        status: ready ? 'ready' : 'not-installed',
        message: ready
          ? (runtime.value.buildAvailable
            ? (runtime.value.buildAuthenticated ? 'Grok Build ready' : 'Grok Build installed')
            : 'Grok API configured')
          : 'Install Grok Build CLI or configure a Grok API key',
        capabilities: ['build-cli', 'api', 'streaming', 'reasoning', 'tools'],
        models: list[index]?.models ?? null,
        reasoningEfforts: list[index]?.reasoningEfforts ?? null,
      }
      if (index >= 0) list[index] = { ...list[index], ...next }
      else list.push(next)
      appStore.agentProviders = list
    } catch {
      runtime.value = emptyRuntime()
    }
  }

  async function refreshRuntimeQuick(): Promise<void> {
    try {
      const status = await refreshGrokRuntimeQuick()
      runtime.value = {
        ...status,
        buildAuthenticated: runtime.value.buildAuthenticated,
        buildVersion: status.buildVersion || runtime.value.buildVersion,
      }
    } catch {
      runtime.value = emptyRuntime()
    }
  }

  async function loadSessions(force = false): Promise<void> {
    const requestedBackend = backendId.value
    const requestedWorkspace = workspacePath.value
    const requestedSearch = search.value.trim()
    const requestKey = `${requestedBackend}\n${workspaceKey(requestedWorkspace)}\n${requestedSearch}`
    // Avoid rescanning ~/.grok on every tab flip — that is the main switch hitch.
    if (!force && sessions.value.length > 0 && sessionsLoadedKey === requestKey && Date.now() - sessionsLoadedAt < 20_000) {
      return
    }
    const sequence = ++sessionLoadSequence
    // Workspace is preferred for ordering/active group, but native sessions can list across projects.
    try {
      const list = await listGrokSessions(requestedBackend, requestedWorkspace, requestedSearch)
      if (
        sequence !== sessionLoadSequence
        || requestedBackend !== backendId.value
        || !sameWorkspacePath(requestedWorkspace, workspacePath.value)
        || requestedSearch !== search.value.trim()
      ) return
      const pending = sessions.value.filter((item) =>
        item.backend === requestedBackend
        && item.id.startsWith('pending-grok-')
        && (
          sameGrokSession(activeSessionId.value, item.id)
          || isSessionBusy(item.id)
          || relatedSessionKeys(queuedBySession.value, item.id)
            .some((key) => (queuedBySession.value[key] || []).length > 0)
        ),
      )
      const pendingIds = new Set(pending.map((item) => resolveSessionId(item.id)))
      for (const item of list ?? []) {
        const activeTurnId = snapshotActiveTurnId(item)
        if (activeTurnId) reconcileSnapshotTurn(item.id, requestedBackend, activeTurnId)
      }
      sessions.value = [
        ...pending,
        ...(list ?? []).filter((item) => !pendingIds.has(resolveSessionId(item.id))),
      ]
      sessionsLoadedAt = Date.now()
      sessionsLoadedKey = requestKey
    } catch (error) {
      if (
        sequence !== sessionLoadSequence
        || requestedBackend !== backendId.value
        || !sameWorkspacePath(requestedWorkspace, workspacePath.value)
        || requestedSearch !== search.value.trim()
      ) return
      const message = errorMessage(error)
      if (/unknown bound method|binding call failed/i.test(message)) {
        notify('warning', translate('sidebar.runtimeSwitchFailed'), translate('sidebar.grokBindingsStale'))
        return
      }
      notify('error', translate('notifications.taskOpenFailed'), message)
    }
  }

  async function openSession(
    sessionID: string,
    options?: { terminalStatus?: string; activate?: boolean; switchWorkspace?: boolean },
  ): Promise<void> {
    const requestedId = sessionID.trim()
    if (!requestedId) return
    const id = resolveSessionId(requestedId) || requestedId
    const activate = options?.activate !== false
    if (!activate && isSessionLoading(id)) return
    const previousSessionId = activeSessionId.value
    if (activate) activeSessionId.value = id
    rememberLoadedGrokSession(id)

    const sequence = (sessionOpenSequence.get(id) || 0) + 1
    sessionOpenSequence.set(id, sequence)
    loadingSequenceBySession.set(id, sequence)
    const known = sessions.value.find((item) => sameGrokSession(item.id, id))
    const requestedBackend = known?.backend === 'api' ? 'api' : backendId.value
    const knownActiveTurnId = snapshotActiveTurnId(known)
    if (knownActiveTurnId) reconcileSnapshotTurn(id, requestedBackend, knownActiveTurnId)
    const targetWorkspace = known?.workspace || ''
    if (activate) loadingSessionId.value = id
    let loadedSessionId = ''
    try {
      if (
        activate
        && options?.switchWorkspace !== false
        && targetWorkspace
        && targetWorkspace !== '(unknown)'
        && (
          workspaceStore.switchingWorkspace
          || !sameWorkspacePath(targetWorkspace, workspacePath.value)
        )
      ) {
        const switched = await workspaceStore.useWorkspace(targetWorkspace)
        if (sessionOpenSequence.get(id) !== sequence) return
        if (!switched && !sameWorkspacePath(targetWorkspace, workspacePath.value)) {
          const hasQueue = relatedSessionKeys(queuedBySession.value, id)
            .some((key) => (queuedBySession.value[key] || []).length > 0)
          if (sameGrokSession(activeSessionId.value, id) && !hasQueue) {
            activeSessionId.value = previousSessionId
          }
          return
        }
      }

      // A newly-created Build session has no native transcript until session.bound.
      // Keep showing its optimistic timeline instead of asking the backend for a fake id.
      if (id.startsWith('pending-grok-')) {
        loadedSessionId = id
        return
      }
      const historyKey = sessionStateKey(historyBySession.value, id)
      const cachedHistory = (historyKey && historyBySession.value[historyKey]) || null
      const hasCachedMessages = relatedSessionKeys(messagesBySession.value, id).length > 0
      const cacheIsCurrent = !known
        || !known.updatedAt
        || known.updatedAt <= (cachedHistory?.loadedUpdatedAt ?? 0)
      if (
        !options?.terminalStatus
        && Boolean(known)
        && cachedHistory?.backend === requestedBackend
        && hasCachedMessages
        && cacheIsCurrent
        && !isSessionTurnBusy(id)
      ) {
        loadedSessionId = id
        return
      }

      const observedTurnId = turnForSession(id)?.turnId || ''
      const detail = await readGrokSession(requestedBackend, id)
      if (sessionOpenSequence.get(id) !== sequence || requestedBackend !== backendId.value) return
      const targetId = resolveSessionId(id) || id
      reconcileSnapshotTurn(targetId, requestedBackend, snapshotActiveTurnId(detail), observedTurnId)
      const messages = detail.messages ?? []
      // Re-read after await: a turn may have started while disk history was loading.
      const busy = isSessionTurnBusy(targetId)
      const terminalStatus = options?.terminalStatus || (!busy ? 'completed' : '')
      const cached = mergedSessionMessages(targetId)
      const currentHistoryKey = sessionStateKey(historyBySession.value, targetId)
      const currentHistory = currentHistoryKey ? historyBySession.value[currentHistoryKey] : undefined
      const keepLoadedPrefix = Boolean(
        currentHistory
        && currentHistory.backend === requestedBackend
        && currentHistory.start < (Number(detail.historyStart) || 0)
        && (Number(detail.historyTotal) || 0) >= currentHistory.total,
      )
      const split = keepLoadedPrefix
        ? splitGrokHistoryPrefix(messages, cached)
        : { prefix: [] as GrokMessage[], current: cached }
      const merged = mergeGrokDiskWithCurrent(messages, split.current, busy)
      const combined = split.prefix.length ? [...split.prefix, ...merged] : merged
      const nextMessages = terminalStatus ? finalizeGrokMessages(combined, terminalStatus) : combined
      replaceSessionMessages(targetId, nextMessages)
      setGrokHistoryState(targetId, requestedBackend, detail)
      if (split.prefix.length && currentHistory) {
        patchGrokHistoryState(targetId, {
          start: currentHistory.start,
          turnOffset: currentHistory.turnOffset,
          hasEarlier: currentHistory.hasEarlier,
        })
      }
      loadedSessionId = targetId
      if (detail.summary?.id) {
        const others = sessions.value.filter((item) => item.id !== detail.summary.id)
        sessions.value = [detail.summary, ...others]
      }
      // Hydrate per-turn token footers from local session updates.jsonl.
      void hydrateSessionTurnUsages(targetId)
    } catch (error) {
      if (sessionOpenSequence.get(id) !== sequence || requestedBackend !== backendId.value) return
      const message = errorMessage(error)
      // A native session can be announced before its transcript is visible on disk.
      if (isSessionTurnBusy(id) && /not found|does not exist|no such file/i.test(message)) return
      if (!activate || !sameGrokSession(activeSessionId.value, id)) return
      if (/unknown bound method|binding call failed/i.test(message)) {
        notify('warning', translate('notifications.taskOpenFailed'), translate('sidebar.grokBindingsStale'))
      } else {
        notify('error', translate('notifications.taskOpenFailed'), message)
      }
    } finally {
      const loadingKey = [...loadingSequenceBySession.keys()].find((key) =>
        loadingSequenceBySession.get(key) === sequence && sameGrokSession(key, id),
      )
      const ownsLoadingBarrier = Boolean(loadingKey)
      if (loadingKey) loadingSequenceBySession.delete(loadingKey)
      if (
        ownsLoadingBarrier
        && !isSessionLoading(id)
        && (loadingSessionId.value === id || sameGrokSession(loadingSessionId.value, id))
      ) {
        loadingSessionId.value = ''
      }
      if (ownsLoadingBarrier) {
        // A transient native-history miss must not strand a prompt admitted while
        // this barrier was active; the queued row already owns backend/workspace.
        void drainQueue(loadedSessionId || resolveSessionId(id) || id)
      }
    }
  }

  async function recoverActiveSession(): Promise<void> {
    const sessionId = activeSessionId.value
    if (!sessionId) return
    const related = [...new Set([
      sessionId,
      ...sessionOpenSequence.keys(),
      ...loadingSequenceBySession.keys(),
    ])].filter((id) => sameGrokSession(id, sessionId))

    for (const id of related) {
      sessionOpenSequence.set(id, (sessionOpenSequence.get(id) || 0) + 1)
      loadingSequenceBySession.delete(id)
    }
    if (sameGrokSession(loadingSessionId.value, sessionId)) loadingSessionId.value = ''
    await openSession(sessionId)
  }

  function setGrokHistoryState(
    sessionId: string,
    backend: 'build' | 'api',
    detail: GrokSessionDetail,
  ): void {
    const id = resolveSessionId(sessionId) || sessionId.trim()
    if (!id) return
    const start = Math.max(0, Number(detail.historyStart) || 0)
    const turnOffset = Math.max(0, Number(detail.historyTurnOffset) || 0)
    const next = { ...historyBySession.value }
    for (const key of relatedSessionKeys(next, id)) delete next[key]
    next[id] = {
      start,
      total: Math.max(start, Number(detail.historyTotal) || 0),
      turnOffset,
      hasEarlier: detail.hasEarlier === true || start > 0,
      loadingEarlier: false,
      loadedUpdatedAt: Number(detail.summary?.updatedAt) || historyBySession.value[id]?.loadedUpdatedAt || 0,
      backend,
    }
    historyBySession.value = next
  }

  function patchGrokHistoryState(sessionId: string, patch: Partial<GrokHistoryState>): void {
    const key = sessionStateKey(historyBySession.value, sessionId)
    const current = key && historyBySession.value[key]
    if (!key || !current) return
    historyBySession.value = {
      ...historyBySession.value,
      [key]: { ...current, ...patch },
    }
  }

  async function loadEarlierHistory(sessionId = activeSessionId.value): Promise<boolean> {
    const key = sessionStateKey(historyBySession.value, sessionId)
    const state = key ? historyBySession.value[key] : undefined
    if (!sessionId || !key || !state?.hasEarlier || state.loadingEarlier) return false
    const before = state.start
    patchGrokHistoryState(key, { loadingEarlier: true })
    try {
      const detail = await readGrokSessionHistory(state.backend, resolveSessionId(sessionId) || sessionId, before)
      const currentKey = sessionStateKey(historyBySession.value, sessionId)
      const currentState = currentKey ? historyBySession.value[currentKey] : undefined
      if (!currentKey || !currentState || currentState.start !== before) return false
      const current = mergedSessionMessages(sessionId)
      const existingIDs = new Set(current.map((message) => message.id).filter(Boolean))
      const prefix = (detail.messages || []).filter((message) => !message.id || !existingIDs.has(message.id))
      if (prefix.length) replaceSessionMessages(sessionId, [...prefix, ...current])
      setGrokHistoryState(sessionId, state.backend, detail)
      rememberLoadedGrokSession(sessionId)
      return prefix.length > 0 || (Number(detail.historyStart) || 0) < before
    } catch (error) {
      if (sameGrokSession(activeSessionId.value, sessionId)) {
        notify('error', translate('notifications.taskOpenFailed'), errorMessage(error))
      }
      return false
    } finally {
      const finalKey = sessionStateKey(historyBySession.value, sessionId)
      if (finalKey && historyBySession.value[finalKey]?.loadingEarlier) {
        patchGrokHistoryState(sessionId, { loadingEarlier: false })
      }
    }
  }

  async function hydrateSessionTurnUsages(sessionID: string): Promise<void> {
    const id = sessionID.trim()
    if (!id || id.startsWith('pending-grok-') || backendForSession(id) === 'api') return
    try {
      const list = await listGrokSessionTurnUsages(id)
      if (!list?.length) return
      const next = { ...turnMetricsByKey.value }
      let totalUsage: TokenUsageBreakdown | null = null
      let lastUsage: TokenUsageBreakdown | null = null
      for (const item of list) {
        const usage = item.tokenUsage
        if (!usage) continue
        const total = Number(usage.totalTokens)
          || (Number(usage.inputTokens) + Number(usage.cachedInputTokens)
            + Number(usage.outputTokens) + Number(usage.reasoningOutputTokens))
        if (total <= 0) continue
        const key = timelineTurnKey(id, item.index || 1)
        const current = next[key] ?? emptyTurnMetrics()
        const breakdown: TokenUsageBreakdown = {
          inputTokens: Number(usage.inputTokens) || 0,
          cachedInputTokens: Number(usage.cachedInputTokens) || 0,
          outputTokens: Number(usage.outputTokens) || 0,
          reasoningOutputTokens: Number(usage.reasoningOutputTokens) || 0,
          totalTokens: total,
        }
        next[key] = {
          ...current,
          tokenUsage: breakdown,
          completedAt: item.at || current.completedAt,
        }
        lastUsage = breakdown
        totalUsage = totalUsage
          ? {
              inputTokens: totalUsage.inputTokens + breakdown.inputTokens,
              cachedInputTokens: totalUsage.cachedInputTokens + breakdown.cachedInputTokens,
              outputTokens: totalUsage.outputTokens + breakdown.outputTokens,
              reasoningOutputTokens: totalUsage.reasoningOutputTokens + breakdown.reasoningOutputTokens,
              totalTokens: totalUsage.totalTokens + breakdown.totalTokens,
            }
          : { ...breakdown }
      }
      turnMetricsByKey.value = next
      if (lastUsage && totalUsage) {
        tokenUsageBySession.value = {
          ...tokenUsageBySession.value,
          [id]: {
            last: lastUsage,
            total: totalUsage,
            modelContextWindow: tokenUsageBySession.value[id]?.modelContextWindow ?? null,
          },
        }
      }
    } catch {
      // Binding may be stale on older binaries; footer simply stays empty.
    }
  }

  function newSession(): void {
    activeSessionId.value = ''
  }

  function ensureSession(message: string, workspace: string, targetSessionId?: string): string {
    let sessionId = targetSessionId === undefined
      ? activeSessionId.value
      : resolveSessionId(targetSessionId)
    if (sessionId) return sessionId
    sessionId = `pending-grok-${Date.now()}-${++queuedSequence}`
    activeSessionId.value = sessionId
    sessions.value = [{
      id: sessionId,
      backend: backendId.value,
      workspace,
      name: message.slice(0, 48) || translate('chat.userMessageFallback', { index: 1 }),
      preview: message,
      model: appStore.settings.grokBuildModel || appStore.settings.grokAPIModel || '',
      effort: appStore.settings.grokEffort || 'high',
      createdAt: Date.now(),
      updatedAt: Date.now(),
    }, ...sessions.value]
    rememberLoadedGrokSession(sessionId)
    return sessionId
  }

  function appendLocalUserMessage(sessionId: string, message: string, images: string[] = []): string {
    const id = resolveSessionId(sessionId) || sessionId.trim()
    const userMessage: GrokMessage = {
      id: `grok-user-${Date.now()}-${++queuedSequence}`,
      role: 'user',
      text: message,
      images: [...images],
      status: 'completed',
      createdAt: Date.now(),
    }
    replaceSessionMessages(id, [...mergedSessionMessages(id), userMessage])
    return userMessage.id
  }

  function removeLocalMessage(sessionId: string, messageId: string): void {
    if (!messageId) return
    const messages = mergedSessionMessages(sessionId)
    if (!messages.some((message) => message.id === messageId)) return
    replaceSessionMessages(sessionId, messages.filter((message) => message.id !== messageId))
  }

  function enqueueMessage(
    sessionId: string,
    text: string,
    images: string[],
    options: { backend: 'build' | 'api'; workspace: string; model: string; effort: string },
    blockedByTurnId = '',
  ): GrokQueuedMessage {
    const item: GrokQueuedMessage = {
      id: `grok-queued-${Date.now()}-${++queuedSequence}`,
      sessionId,
      backend: options.backend,
      workspace: options.workspace,
      model: options.model,
      effort: options.effort,
      text,
      images: [...images],
      state: 'queued',
      error: '',
      createdAt: Date.now(),
      blockedByTurnId: blockedByTurnId || undefined,
    }
    queuedBySession.value = {
      ...queuedBySession.value,
      [sessionId]: [...(queuedBySession.value[sessionId] ?? []), item],
    }
    return item
  }

  function patchQueuedMessage(sessionId: string, messageId: string, patch: Partial<GrokQueuedMessage>): void {
    const key = sessionStateKey(queuedBySession.value, sessionId)
    const list = queuedBySession.value[key]
    if (!list?.length) return
    queuedBySession.value = {
      ...queuedBySession.value,
      [key]: list.map((item) => (item.id === messageId ? { ...item, ...patch, sessionId: key } : item)),
    }
  }

  function remapQueuedBlockingTurn(sessionId: string, fromTurnId: string, toTurnId: string): void {
    if (!sessionId || !fromTurnId || !toTurnId || fromTurnId === toTurnId) return
    const key = sessionStateKey(queuedBySession.value, sessionId)
    const list = queuedBySession.value[key]
    if (!list?.some((item) => item.blockedByTurnId === fromTurnId)) return
    queuedBySession.value = {
      ...queuedBySession.value,
      [key]: list.map((item) =>
        item.blockedByTurnId === fromTurnId ? { ...item, blockedByTurnId: toTurnId } : item,
      ),
    }
  }

  function removeQueuedMessageFromSession(sessionId: string, messageId: string): void {
    const key = sessionStateKey(queuedBySession.value, sessionId)
    const list = queuedBySession.value[key]
    if (!list?.length) return
    const next = list.filter((item) => item.id !== messageId)
    const queues = { ...queuedBySession.value }
    if (next.length) queues[key] = next
    else delete queues[key]
    queuedBySession.value = queues
  }

  function removeQueuedMessage(messageId: string): void {
    for (const [sessionId, list] of Object.entries(queuedBySession.value)) {
      const message = list.find((item) => item.id === messageId)
      if (!message) continue
      if (message.state === 'sending') return
      removeQueuedMessageFromSession(sessionId, messageId)
      void drainQueue(sessionId)
      return
    }
  }

  function reorderQueuedMessage(messageId: string, direction: 'up' | 'down'): void {
    for (const [sessionId, list] of Object.entries(queuedBySession.value)) {
      const index = list.findIndex((item) => item.id === messageId)
      if (index < 0) continue
      const message = list[index]
      if (!message || message.state === 'sending') return
      let floor = 0
      while (floor < list.length && list[floor]?.state === 'sending') floor += 1
      const target = direction === 'up' ? index - 1 : index + 1
      if (target < floor || target >= list.length) return
      const next = [...list]
      const [item] = next.splice(index, 1)
      next.splice(target, 0, item)
      queuedBySession.value = { ...queuedBySession.value, [sessionId]: next }
      return
    }
  }

  async function dispatchTurn(
    sessionId: string,
    message: string,
    images: string[],
    options?: {
      alreadyLocked?: boolean
      localMessageId?: string
      backend?: 'build' | 'api'
      workspace?: string
      model?: string
      effort?: string
    },
  ): Promise<'sent' | 'failed' | 'deferred'> {
    const turnBackend = options?.backend || backendId.value
    const workspace = options?.workspace || workspacePath.value
    if (!workspace) {
      notify('warning', translate('app.needWorkspace'), translate('app.needWorkspaceHintReady'))
      return 'failed'
    }
    const lockedTurn = turnForSession(sessionId)
    const pendingTurnId = options?.alreadyLocked && lockedTurn?.turnId?.startsWith('grok-turn-pending-')
      ? lockedTurn.turnId
      : `grok-turn-pending-${Date.now()}-${++queuedSequence}`
    rememberGrokTurnClientId(pendingTurnId, pendingTurnId)
    if (!options?.alreadyLocked) {
      resetLiveSessionState(sessionId)
      markSessionState(sendingSessionIds, sessionId, true)
      setSessionTurn(sessionId, {
        backend: turnBackend,
        sessionId,
        turnId: pendingTurnId,
      })
      turnStartedAtById.value = { ...turnStartedAtById.value, [pendingTurnId]: Date.now() }
      seedTurnStartMetrics(sessionId, pendingTurnId)
    } else if (!turnStartedAtById.value[pendingTurnId]) {
      turnStartedAtById.value = { ...turnStartedAtById.value, [pendingTurnId]: Date.now() }
      seedTurnStartMetrics(sessionId, pendingTurnId)
    }
    scheduleGrokTurnWatchdog({ backend: turnBackend, sessionId, turnId: pendingTurnId })
    const startedTurnBeforeSend = latestStartedTurnForSession(sessionId)
    try {
      const ref = await sendGrokMessageApi({
        backend: turnBackend,
        // The backend allocates a real id and echoes this pending id on turn.started
        // so the event can bind the optimistic timeline before the RPC resolves.
        clientTurnId: pendingTurnId,
        sessionId: sessionId.startsWith('pending-grok-') ? sessionId : resolveSessionId(sessionId),
        workspace,
        text: message,
        images,
        model: options?.model ?? (turnBackend === 'api'
          ? (appStore.settings.grokAPIModel || '')
          : (appStore.settings.grokBuildModel || '')),
        effort: options?.effort || appStore.settings.grokEffort || 'high',
      })
      const nextTurnId = ref.turnId || pendingTurnId
      rememberGrokTurnClientId(nextTurnId, pendingTurnId)
      if (nextTurnId !== pendingTurnId) remapQueuedBlockingTurn(sessionId, pendingTurnId, nextTurnId)
      // Preserve start clock across pending → real turn id.
      if (nextTurnId !== pendingTurnId && turnStartedAtById.value[pendingTurnId]) {
        const nextStarts = { ...turnStartedAtById.value }
        nextStarts[nextTurnId] = nextStarts[pendingTurnId]
        delete nextStarts[pendingTurnId]
        turnStartedAtById.value = nextStarts
      }
      // Only adopt the API turn if we still own this pending lock (not interrupted).
      const currentTurn = turnForSession(sessionId)
      const currentClientTurnId = clientTurnIdForTurn(currentTurn)
      const ownsPendingTurn = Boolean(currentTurn && (
        currentTurn.turnId === pendingTurnId
        || currentTurn.turnId === nextTurnId
        || currentClientTurnId === pendingTurnId
      ))
      // A very fast turn can complete before the Wails call Promise resolves.
      // Still bind its pending UI session so optimistic messages and queued turns
      // cannot remain stranded under the temporary id.
      const alreadyFinalized = finalizedTurnIds.has(pendingTurnId) || finalizedTurnIds.has(nextTurnId)
      const targetSessionId = ref.sessionId && ref.sessionId !== sessionId && (ownsPendingTurn || alreadyFinalized)
        ? promoteSessionState(sessionId, ref.sessionId)
        : sessionId
      if (alreadyFinalized) {
        const finalizedTurn = turnForSession(targetSessionId)
        if (finalizedTurn && (
          finalizedTurn.turnId === pendingTurnId
          || finalizedTurn.turnId === nextTurnId
          || clientTurnIdForTurn(finalizedTurn) === pendingTurnId
        )) {
          clearTurnState(targetSessionId, finalizedTurn.turnId)
        }
        return 'sent'
      }
      if (ownsPendingTurn) {
        setSessionTurn(targetSessionId, {
          backend: ref.backend || turnBackend,
          sessionId: targetSessionId,
          turnId: nextTurnId,
        })
        scheduleGrokTurnWatchdog({
          backend: ref.backend || turnBackend,
          sessionId: targetSessionId,
          turnId: nextTurnId,
        })
      }
      return 'sent'
    } catch (error) {
      const acceptedTurn = turnForSession(sessionId)
      const latestStartedTurnId = latestStartedTurnForSession(sessionId)
      const acceptedDuringSend = Boolean(
        latestStartedTurnId && latestStartedTurnId !== startedTurnBeforeSend,
      )
      if (
        acceptedDuringSend
        || finalizedTurnIds.has(pendingTurnId)
        || (acceptedTurn?.turnId && acceptedTurn.turnId !== pendingTurnId)
      ) {
        // turn.started/terminal crossed the bridge before the RPC error. Those
        // events prove the prompt was accepted; removing it here causes the
        // "message vanished while thinking, then reappeared" flicker and a retry
        // would duplicate the task.
        return 'sent'
      }
      const failure = errorMessage(error)
      rememberFinalizedGrokTurn(pendingTurnId)
      clearTurnState(sessionId, pendingTurnId)
      if (options?.localMessageId) removeLocalMessage(sessionId, options.localMessageId)
      if (/Grok turn is already running/i.test(failure)) return 'deferred'
      notify('error', translate('notifications.messageNotSent'), failure)
      return 'failed'
    }
  }

  async function drainQueue(sessionId: string): Promise<void> {
    const id = resolveSessionId(sessionId) || sessionId
    if (!id) return
    // Prefer queue on resolved id, fall back to any alias bucket that still has items.
    let queueSessionId = id
    let list = queuedBySession.value[id] ?? []
    if (!list.length) {
      for (const [key, items] of Object.entries(queuedBySession.value)) {
        if (sameGrokSession(key, id) && items.length) {
          queueSessionId = key
          list = items
          break
        }
      }
    }
    // Failed requests remain visible for retry, but a provider HTTP/stream
    // failure must not make every later prompt unsendable.
    const next = list.find((item) => item.state === 'queued')
    if (!next) return
    if (next.blockedByTurnId && !finalizedTurnIds.has(next.blockedByTurnId)) return
    if (isSessionBusy(id)) return

    patchQueuedMessage(queueSessionId, next.id, { state: 'sending', error: '' })
    // Queued follow-ups already show in the queue strip; inject into the timeline once
    // when the item actually starts sending (avoid double-append on retry).
    let localMessageId = ''
    if (!next.localAppended && (next.text || next.images.length)) {
      localMessageId = appendLocalUserMessage(
        resolveSessionId(queueSessionId) || queueSessionId,
        next.text,
        next.images,
      )
      patchQueuedMessage(queueSessionId, next.id, { localAppended: true })
    }
    const outcome = await dispatchTurn(
      resolveSessionId(queueSessionId) || queueSessionId,
      next.text,
      next.images,
      {
        backend: next.backend,
        workspace: next.workspace,
        model: next.model,
        effort: next.effort,
        localMessageId,
      },
    )
    if (outcome === 'sent') {
      removeQueuedMessageFromSession(queueSessionId, next.id)
      // Completion may have beaten the send Promise; resume FIFO now that the
      // sending item has been removed from its (possibly promoted) queue bucket.
      const targetSessionId = resolveSessionId(queueSessionId) || queueSessionId
      if (!isSessionBusy(targetSessionId)) await drainQueue(targetSessionId)
      return
    }
    if (outcome === 'deferred') {
      patchQueuedMessage(queueSessionId, next.id, {
        state: 'queued',
        error: '',
        // dispatchTurn removes this attempt's optimistic row when the old
        // backend turn is still releasing. The next drain must append it again.
        localAppended: localMessageId ? false : next.localAppended,
      })
      return
    }
    patchQueuedMessage(queueSessionId, next.id, {
      state: 'failed',
      error: translate('notifications.messageNotSent'),
      localAppended: localMessageId ? false : next.localAppended,
    })
    if (!isSessionBusy(resolveSessionId(queueSessionId) || queueSessionId)) {
      await drainQueue(queueSessionId)
    }
  }

  async function sendQueuedMessageNow(messageId: string): Promise<void> {
    let sessionId = ''
    let message: GrokQueuedMessage | undefined
    for (const [id, list] of Object.entries(queuedBySession.value)) {
      const found = list.find((item) => item.id === messageId)
      if (found) {
        sessionId = id
        message = found
        break
      }
    }
    if (!sessionId || !message) return

    // Promote to front.
    const list = [...(queuedBySession.value[sessionId] ?? [])]
    const index = list.findIndex((item) => item.id === messageId)
    let floor = 0
    while (floor < list.length && list[floor]?.state === 'sending') floor += 1
    if (index >= 0 && index !== floor) {
      const [item] = list.splice(index, 1)
      if (!item) return
      list.splice(floor, 0, item)
      queuedBySession.value = { ...queuedBySession.value, [sessionId]: list }
    }
    patchQueuedMessage(sessionId, messageId, { state: 'queued', error: '' })

    if (isSessionBusy(sessionId)) {
      await interruptTurn(sessionId)
      // turn.interrupted / completed will drain the queue.
      return
    }
    if (message.blockedByTurnId && !finalizedTurnIds.has(message.blockedByTurnId)) {
      // No live owner remains, so an explicit "send now" may release an orphaned blocker.
      patchQueuedMessage(sessionId, messageId, { blockedByTurnId: undefined })
    }
    await drainQueue(sessionId)
  }

  function retryQueuedMessage(messageId: string): void {
    for (const [sessionId, list] of Object.entries(queuedBySession.value)) {
      const found = list.find((item) => item.id === messageId)
      if (!found || found.state !== 'failed') continue
      patchQueuedMessage(sessionId, messageId, { state: 'queued', error: '' })
      void drainQueue(sessionId)
      return
    }
  }

  /**
   * Send immediately when idle; otherwise enqueue (Codex follow-up queue).
   * Composer must stay enabled while a turn runs so users can queue.
   */
  async function sendMessage(
    text: string,
    images: string[] = [],
    targetSessionId?: string,
    targetWorkspace = '',
  ): Promise<boolean> {
    const message = text.trim()
    if (!message && !images.length) return false
    const requestedSessionId = targetSessionId === undefined
      ? activeSessionId.value
      : (resolveSessionId(targetSessionId) || targetSessionId.trim())
    let summary = requestedSessionId
      ? sessions.value.find((item) => sameGrokSession(item.id, requestedSessionId))
      : undefined
    if (requestedSessionId && !requestedSessionId.startsWith('pending-grok-') && !summary) {
      await openSession(requestedSessionId, { activate: false, switchWorkspace: false })
      summary = sessions.value.find((item) => sameGrokSession(item.id, requestedSessionId))
      if (!summary) return false
    }
    const requestedWorkspace = targetWorkspace.trim()
    if (
      requestedWorkspace
      && summary?.workspace
      && !sameWorkspacePath(requestedWorkspace, summary.workspace)
    ) return false
    const workspace = summary?.workspace || requestedWorkspace || workspacePath.value
    if (!workspace) {
      notify('warning', translate('app.needWorkspace'), translate('app.needWorkspaceHintReady'))
      return false
    }
    if (!isReady.value) {
      notify('warning', translate('notifications.connectionFailed'), translate('sidebar.grokRuntimeMissing'))
      return false
    }

    const sessionId = ensureSession(message, workspace, targetSessionId)
    summary = sessions.value.find((item) => sameGrokSession(item.id, sessionId)) || summary
    const turnBackend = summary?.backend === 'api' ? 'api' : backendId.value
    const turnWorkspace = summary?.workspace || workspace
    const turnModel = summary?.model || (turnBackend === 'api'
      ? (appStore.settings.grokAPIModel || '')
      : (appStore.settings.grokBuildModel || ''))
    const turnEffort = summary?.effort || appStore.settings.grokEffort || 'high'
    const busy = isSessionBusy(sessionId)
    const activeTurnId = turnForSession(sessionId)?.turnId || ''
    // Queue-first admission preserves FIFO when a terminal event and a new send
    // land in the same frame; drainQueue remains the only dispatch path.
    enqueueMessage(sessionId, message, images, {
      backend: turnBackend,
      workspace: turnWorkspace,
      model: turnModel,
      effort: turnEffort,
    }, activeTurnId)
    if (!busy) await drainQueue(sessionId)
    return true
  }

  async function interruptTurn(sessionId = activeSessionId.value): Promise<void> {
    const ref = turnForSession(sessionId)
    if (!ref) return
    if (interruptingSessionIds.value.some((id) => sameGrokSession(id, ref.sessionId))) return
    markSessionState(interruptingSessionIds, ref.sessionId, true)
    const candidates: GrokTurnRef[] = [ref]
    const resolved = resolveSessionId(ref.sessionId)
    if (resolved && resolved !== ref.sessionId) {
      candidates.push({ ...ref, sessionId: resolved })
    }
    try {
      let lastError: unknown = null
      let ok = false
      for (const candidate of candidates) {
        try {
          await interruptGrokTurnApi(candidate)
          ok = true
          break
        } catch (error) {
          lastError = error
        }
      }
      if (!ok) throw lastError || new Error('Grok turn is not running')
      void reconcileInterruptedTurn(ref)
    } catch (error) {
      const current = turnForSession(ref.sessionId)
      if (!current || current.turnId !== ref.turnId) return
      if (/not running/i.test(errorMessage(error))) {
        // The backend has no owner for this turn, so keeping the local lock would
        // park every follow-up forever after a missed terminal bridge event.
        rememberFinalizedGrokTurn(ref.turnId)
        clearTurnState(ref.sessionId, ref.turnId)
        markSessionState(interruptingSessionIds, ref.sessionId, false)
        await drainQueue(ref.sessionId)
        return
      }
      markSessionState(interruptingSessionIds, ref.sessionId, false)
      notify('error', translate('notifications.turnStopFailed'), errorMessage(error))
    }
  }

  async function reconcileInterruptedTurn(ref: GrokTurnRef, attempt = 0): Promise<void> {
    await new Promise<void>((resolve) => window.setTimeout(resolve, 1500))
    const current = turnForSession(ref.sessionId)
    if (!current || current.turnId !== ref.turnId) return
    try {
      if (!await isGrokTurnRunningApi(ref)) {
        rememberFinalizedGrokTurn(ref.turnId)
        clearTurnState(ref.sessionId, ref.turnId)
        await drainQueue(ref.sessionId)
        return
      }
    } catch (error) {
      const latest = turnForSession(ref.sessionId)
      if (!latest || latest.turnId !== ref.turnId) return
      markSessionState(interruptingSessionIds, ref.sessionId, false)
      notify('error', translate('notifications.turnStopFailed'), errorMessage(error))
      return
    }
    const latest = turnForSession(ref.sessionId)
    if (!latest || latest.turnId !== ref.turnId) return
    if (attempt < 9) {
      void reconcileInterruptedTurn(ref, attempt + 1)
      return
    }
    markSessionState(interruptingSessionIds, ref.sessionId, false)
    notify('error', translate('notifications.turnStopFailed'), translate('chat.working'))
  }

  function discardLocalSession(sessionID: string): void {
    const candidates = new Set<string>([
      sessionID,
      ...sessionAlias.keys(),
      ...sessionAlias.values(),
      ...Object.keys(messagesBySession.value),
      ...Object.keys(historyBySession.value),
      ...Object.keys(queuedBySession.value),
      ...Object.keys(activeTurnBySession.value),
    ])
    const related = [...candidates].filter((id) => id && sameGrokSession(id, sessionID))
    if (!related.includes(sessionID)) related.push(sessionID)
    arenaStore.clearSessionBindings('grok', related)
    navigationStore.removeSessions('grok', related, translate('sidebar.newTask'))
    const relatedSet = new Set(related)
    const matches = (id: string) => related.some((key) => sameGrokSession(id, key))

    for (const id of [...loadedSessionIds]) if (matches(id)) loadedSessionIds.delete(id)

    sessions.value = sessions.value.filter((item) => !matches(item.id))
    archivedSessions.value = archivedSessions.value.filter((item) => !matches(item.id))
    if (matches(activeSessionId.value)) activeSessionId.value = ''
    if (matches(loadingSessionId.value)) loadingSessionId.value = ''
    sendingSessionIds.value = sendingSessionIds.value.filter((id) => !matches(id))
    runningSessionIdsState.value = runningSessionIdsState.value.filter((id) => !matches(id))
    interruptingSessionIds.value = interruptingSessionIds.value.filter((id) => !matches(id))
    for (const [id, turn] of Object.entries(activeTurnBySession.value)) {
      if (matches(id) && turn?.turnId) cancelGrokTurnWatchdog(turn.turnId)
    }

    const withoutRelated = <T>(bucket: Record<string, T>): Record<string, T> =>
      Object.fromEntries(Object.entries(bucket).filter(([id]) => !matches(id)))
    messagesBySession.value = withoutRelated(messagesBySession.value)
    historyBySession.value = withoutRelated(historyBySession.value)
    queuedBySession.value = withoutRelated(queuedBySession.value)
    activeTurnBySession.value = withoutRelated(activeTurnBySession.value)
    liveTextBySession.value = withoutRelated(liveTextBySession.value)
    liveThoughtBySession.value = withoutRelated(liveThoughtBySession.value)
    liveActivityBySession.value = withoutRelated(liveActivityBySession.value)
    tokenUsageBySession.value = withoutRelated(tokenUsageBySession.value)

    for (const id of [...pendingLiveText.keys()]) if (matches(id)) pendingLiveText.delete(id)
    for (const id of [...pendingLiveThought.keys()]) if (matches(id)) pendingLiveThought.delete(id)
    for (const id of [...sessionOpenSequence.keys()]) if (matches(id)) sessionOpenSequence.delete(id)
    for (const id of [...loadingSequenceBySession.keys()]) if (matches(id)) loadingSequenceBySession.delete(id)
    for (const id of [...latestStartedTurnBySession.keys()]) if (matches(id)) latestStartedTurnBySession.delete(id)

    const nextMetrics = { ...turnMetricsByKey.value }
    for (const key of Object.keys(nextMetrics)) {
      if (related.some((id) => key.startsWith(`${id}:`))) delete nextMetrics[key]
    }
    turnMetricsByKey.value = nextMetrics

    for (const id of [...sessionAlias.keys()]) {
      const target = sessionAlias.get(id) || ''
      if (relatedSet.has(id) || relatedSet.has(target)) sessionAlias.delete(id)
    }
  }

  async function renameSession(sessionID: string, name?: string): Promise<boolean> {
    const id = resolveSessionId(sessionID) || sessionID.trim()
    if (!id || sessionMutationForSession(id)) return false
    const sessionBackend = backendForSession(id)
    const current = sessions.value.find((item) => sameGrokSession(item.id, id))
      || archivedSessions.value.find((item) => sameGrokSession(item.id, id))
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

    if (!beginSessionMutation(id, 'rename')) return false
    try {
      if (id.startsWith('pending-grok-')) {
        sessions.value = sessions.value.map((item) =>
          sameGrokSession(item.id, id) ? { ...item, name: nextName, updatedAt: Date.now() } : item,
        )
        notify('success', translate('threadActions.renamed'), nextName)
        return true
      }
      const summary = await renameGrokSessionApi(sessionBackend, id, nextName)
      const patched = { ...(current || summary), ...summary, name: summary.name || nextName }
      sessions.value = sessions.value.map((item) => (sameGrokSession(item.id, id) ? patched : item))
      archivedSessions.value = archivedSessions.value.map((item) => (sameGrokSession(item.id, id) ? patched : item))
      notify('success', translate('threadActions.renamed'), patched.name)
      return true
    } catch (error) {
      notify('error', translate('threadActions.renameFailed'), errorMessage(error))
      return false
    } finally {
      endSessionMutation(id)
    }
  }

  async function renameActiveSession(name?: string): Promise<void> {
    const id = activeSessionId.value
    if (!id) return
    await renameSession(id, name)
  }

  function canMutateSession(sessionId: string): boolean {
    if (!isSessionBusy(sessionId)) return true
    notify(
      'warning',
      translate('threadActions.busyActionBlocked'),
      translate('threadActions.busyActionBlockedHint'),
    )
    return false
  }

  function backendForSession(sessionId: string): 'build' | 'api' {
    const summary = sessions.value.find((item) => sameGrokSession(item.id, sessionId))
      ?? archivedSessions.value.find((item) => sameGrokSession(item.id, sessionId))
    return summary?.backend === 'api' ? 'api' : backendId.value
  }

  async function archiveSession(sessionID: string): Promise<void> {
    const id = resolveSessionId(sessionID) || sessionID.trim()
    if (!id || sessionMutationForSession(id) || !canMutateSession(id)) return
    const sessionBackend = backendForSession(id)
    if (!beginSessionMutation(id, 'archive')) return
    try {
      const current = sessions.value.find((item) => sameGrokSession(item.id, id))
      if (!id.startsWith('pending-grok-')) {
        await archiveGrokSessionApi(sessionBackend, id)
      }
      discardLocalSession(id)
      if (current) {
        archivedSessions.value = [current, ...archivedSessions.value.filter((item) => !sameGrokSession(item.id, id))]
      }
      notify('success', translate('threadActions.archived'), translate('threadActions.archivedHint'))
      void loadArchivedSessions()
    } catch (error) {
      notify('error', translate('threadActions.archiveFailed'), errorMessage(error))
    } finally {
      endSessionMutation(id)
    }
  }

  async function archiveActiveSession(): Promise<void> {
    const id = activeSessionId.value
    if (!id) return
    await archiveSession(id)
  }

  async function unarchiveSession(sessionID: string): Promise<void> {
    const id = resolveSessionId(sessionID) || sessionID.trim()
    if (!id || sessionMutationForSession(id)) return
    const sessionBackend = backendForSession(id)
    if (!beginSessionMutation(id, 'unarchive')) return
    try {
      const summary = await unarchiveGrokSessionApi(sessionBackend, id)
      archivedSessions.value = archivedSessions.value.filter((item) => !sameGrokSession(item.id, id))
      if (summary?.id) {
        sessions.value = [summary, ...sessions.value.filter((item) => !sameGrokSession(item.id, summary.id))]
      }
      notify('success', translate('threadActions.unarchived'), translate('threadActions.unarchivedHint'))
      void loadSessions(true)
    } catch (error) {
      notify('error', translate('threadActions.unarchiveFailed'), errorMessage(error))
    } finally {
      endSessionMutation(id)
    }
  }

  async function loadArchivedSessions(searchQuery = ''): Promise<void> {
    const sequence = ++archivedSessionLoadSequence
    const requestedBackend = backendId.value
    try {
      const list = await listArchivedGrokSessions(requestedBackend, searchQuery)
      if (sequence !== archivedSessionLoadSequence || requestedBackend !== backendId.value) return
      archivedSessions.value = list ?? []
    } catch (error) {
      if (sequence !== archivedSessionLoadSequence || requestedBackend !== backendId.value) return
      notify('error', translate('notifications.taskOpenFailed'), errorMessage(error))
    }
  }

  async function deleteSession(sessionID: string, options: { confirm?: boolean } = {}): Promise<void> {
    const id = resolveSessionId(sessionID) || sessionID.trim()
    if (!id || sessionMutationForSession(id) || !canMutateSession(id)) return
    const sessionBackend = backendForSession(id)
    const needsConfirm = options.confirm !== false
    if (needsConfirm) {
      const confirmed = await dialogStore.confirm({
        title: translate('threadActions.delete'),
        description: translate('threadActions.deleteConfirm'),
        confirmLabel: translate('common.delete'),
        destructive: true,
      })
      if (!confirmed) return
    }

    if (!beginSessionMutation(id, 'delete')) return
    try {
      if (!id.startsWith('pending-grok-')) {
        await deleteGrokSessionApi(sessionBackend, id)
      }
      discardLocalSession(id)
      notify('success', translate('threadActions.deleted'), translate('threadActions.deletedHint'))
    } catch (error) {
      notify('error', translate('threadActions.deleteFailed'), errorMessage(error))
    } finally {
      endSessionMutation(id)
    }
  }

  async function deleteActiveSession(): Promise<void> {
    const id = activeSessionId.value
    if (!id) return
    await deleteSession(id)
  }

  async function enterRuntime(force = false): Promise<void> {
    bootstrapEvents()
    // Coalesce concurrent enter calls from sidebar + App.vue watch.
    if (enterInFlight && !force) return enterInFlight
    enterInFlight = (async () => {
      // Local-only readiness check: never race a user turn with `grok models`.
      await refreshRuntimeQuick()
      void appStore.loadLocalUsage().catch(() => undefined)
      await loadSessions(force)
      void loadArchivedSessions()
    })().finally(() => {
      enterInFlight = null
    })
    return enterInFlight
  }

  return {
    runtime,
    sessions,
    archivedSessions,
    sessionGroups,
    activeSessionId,
    messagesBySession,
    loadingSessionId,
    sending,
    sessionMutation,
    sessionMutationForSession,
    patchSessionPreferences,
    activeTurn,
    activeTurnBySession,
    sendingSessionIds,
    historyBySession,
    queuedBySession,
    tokenUsageBySession,
    turnMetricsByKey,
    search,
    backendId,
    workspacePath,
    activeMessages,
    activeQueuedMessages,
    activeItems,
    itemsForSession,
    activeHistoryHasEarlier,
    activeHistoryEarlierCount,
    activeHistoryLoadingEarlier,
    activeTurnMetrics,
    activeTokenUsage,
    isTurnRunning,
    interrupting,
    runningSessionIds,
    sameSession: sameGrokSession,
    resolveSessionId,
    isSessionBusy,
    isSessionLoading,
    isSessionTurnBusy,
    isSessionInterrupting,
    isReady,
    bootstrapEvents,
    dispose,
    refreshRuntime,
    loadSessions,
    loadArchivedSessions,
    openSession,
    recoverActiveSession,
    loadEarlierHistory,
    newSession,
    sendMessage,
    interruptTurn,
    renameSession,
    renameActiveSession,
    archiveSession,
    archiveActiveSession,
    unarchiveSession,
    deleteSession,
    deleteActiveSession,
    enterRuntime,
    removeQueuedMessage,
    reorderQueuedMessage,
    sendQueuedMessageNow,
    retryQueuedMessage,
  }
})
