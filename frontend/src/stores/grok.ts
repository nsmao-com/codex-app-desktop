import { Events } from '@wailsio/runtime'
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import type {
  GrokMessage,
  GrokRuntimeStatus,
  GrokSessionSummary,
  GrokTurnRef,
} from '../../bindings/nice_codex_desktop/models'
import type { TimelineItem, TokenUsageBreakdown, TurnMetrics } from '@/types/codex'
import {
  archiveGrokSession as archiveGrokSessionApi,
  deleteGrokSession as deleteGrokSessionApi,
  interruptGrokTurn as interruptGrokTurnApi,
  listArchivedGrokSessions,
  listGrokSessionTurnUsages,
  listGrokSessions,
  readGrokSession,
  refreshGrokRuntime,
  renameGrokSession as renameGrokSessionApi,
  sendGrokMessage as sendGrokMessageApi,
  unarchiveGrokSession as unarchiveGrokSessionApi,
} from '@/utils/grokBindings'
import { resolveProviderModelContextWindow } from '@/utils/accountUsage'
import { normalizeThreadTokenUsage } from '@/utils/protocol'
import { notify } from '@/utils/notify'
import { translate } from '@/i18n'
import { useAppStore } from './app'
import { useDialogStore } from './dialog'

const emptyTurnMetrics = (): TurnMetrics => ({
  tokenUsage: null,
  startedAt: null,
  completedAt: null,
  durationMs: null,
})

function errorMessage(error: unknown): string {
  if (error instanceof Error && error.message) return error.message
  return String(error || translate('notifications.unexpected'))
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
    attachments: [] as TimelineItem['attachments'],
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
        // Grok history usually only stores the success line; keep it as readable detail.
        diff: text || '',
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
  text: string
  images: string[]
  state: 'queued' | 'sending' | 'failed'
  error: string
  createdAt: number
  /** Timeline user row already injected for this queue item. */
  localAppended?: boolean
}

function sameWorkspacePath(left: string, right: string): boolean {
  const normalize = (value: string) => value.trim().replace(/[\\/]+$/, '').toLowerCase()
  return normalize(left) === normalize(right)
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
  const dialogStore = useDialogStore()

  const runtime = shallowRef<GrokRuntimeStatus>(emptyRuntime())
  const sessions = shallowRef<GrokSessionSummary[]>([])
  const archivedSessions = shallowRef<GrokSessionSummary[]>([])
  const activeSessionId = shallowRef('')
  const messagesBySession = shallowRef<Record<string, GrokMessage[]>>({})
  const loadingSessionId = shallowRef('')
  const sending = shallowRef(false)
  const sessionMutation = shallowRef('')
  const activeTurn = shallowRef<GrokTurnRef | null>(null)
  const liveTextBySession = shallowRef<Record<string, string>>({})
  /** Live Grok Build thought stream (planning shimmer label). */
  const liveThoughtBySession = shallowRef<Record<string, string>>({})
  /** Ordered provider-history assistant/reasoning/tool rows for the active turn. */
  const liveActivityBySession = shallowRef<Record<string, GrokMessage[]>>({})
  /** Per-session follow-up queue — drain after turn.completed / failed / interrupted. */
  const queuedBySession = shallowRef<Record<string, GrokQueuedMessage[]>>({})
  /** Cumulative text snapshots + sequence numbers protect against bridge reordering. */
  const streamSequenceByTurn = new Map<string, number>()
  const finalizedTurnIds = new Set<string>()
  /** Timeline turnId (`session:tN`) → metrics (tokens + duration). */
  const turnMetricsByKey = shallowRef<Record<string, TurnMetrics>>({})
  /** Backend turnId → wall-clock start for duration when completed. */
  const turnStartedAtById = shallowRef<Record<string, number>>({})
  /** Session-level cumulative token usage (inspector). */
  const tokenUsageBySession = shallowRef<Record<string, ReturnType<typeof normalizeThreadTokenUsage>>>({})
  const search = shallowRef('')
  let eventUnsub: (() => void) | null = null
  let sessionsLoadedAt = 0
  let enterInFlight: Promise<void> | null = null
  let queuedSequence = 0
  /** pending/local session id → native Grok session id (and reverse lookups). */
  const sessionAlias = new Map<string, string>()

  const backendId = computed(() => {
    const value = (appStore.settings.grokBackend || 'build').toLowerCase()
    return value === 'api' ? 'api' : 'build'
  })

  const workspacePath = computed(() =>
    appStore.settings.grokWorkspace || appStore.settings.workspace || '',
  )

  function resolveSessionId(id: string): string {
    const raw = id.trim()
    if (!raw) return ''
    return sessionAlias.get(raw) || raw
  }

  function rememberSessionAlias(fromId: string, toId: string): void {
    const from = fromId.trim()
    const to = toId.trim()
    if (!from || !to || from === to) return
    sessionAlias.set(from, to)
    sessionAlias.set(to, to)
  }

  function sameGrokSession(left: string, right: string): boolean {
    const a = resolveSessionId(left)
    const b = resolveSessionId(right)
    return Boolean(a && b && a === b)
  }

  function isSessionBusy(sessionId: string): boolean {
    const id = resolveSessionId(sessionId)
    if (!id) return sending.value || Boolean(activeTurn.value)
    if (sending.value && sameGrokSession(activeSessionId.value, id)) return true
    if (activeTurn.value && sameGrokSession(activeTurn.value.sessionId, id)) return true
    return false
  }

  function remapSessionBucket<T>(
    bucket: Record<string, T>,
    fromId: string,
    toId: string,
  ): Record<string, T> {
    if (!fromId || !toId || fromId === toId || bucket[fromId] === undefined) return bucket
    if (bucket[toId] !== undefined) {
      const next = { ...bucket }
      delete next[fromId]
      return next
    }
    const next = { ...bucket }
    next[toId] = next[fromId]
    delete next[fromId]
    return next
  }

  function clearTurnState(sessionId = '', turnId = ''): void {
    const turn = activeTurn.value
    if (!turn) {
      sending.value = false
      return
    }
    // Stale completion/interrupt must not kill a newer turn on the same session.
    if (turnId && turn.turnId && turn.turnId !== turnId) {
      return
    }
    if (sessionId && !sameGrokSession(turn.sessionId, sessionId)) {
      return
    }
    activeTurn.value = null
    sending.value = false
  }

  const activeMessages = computed(() => messagesBySession.value[activeSessionId.value] ?? [])
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

  const activeItems = computed<TimelineItem[]>(() => {
    const sessionId = activeSessionId.value
    // Codex-style timeline collage: persisted history stays immutable while the
    // current answer has one in-memory owner. Completion reloads native history
    // once so tool rows and assistant segments regain their authoritative order.
    let turn = 0
    const items: TimelineItem[] = []
    const messages = activeMessages.value
    for (const message of messages) {
      const role = (message.role || '').toLowerCase()
      if (role === 'user' || role === 'human') turn += 1
      else if (turn === 0) turn = 1
      items.push(messageToItem(message, `${sessionId}:t${turn}`))
    }

    const turnRef = activeTurn.value
    const liveOnActive = Boolean(
      turnRef
      && sessionId
      && sameGrokSession(turnRef.sessionId, sessionId),
    )
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
    const buckets = new Map<string, GrokSessionSummary[]>()
    for (const session of sessions.value) {
      const path = (session.workspace || '').trim() || '(unknown)'
      const list = buckets.get(path) ?? []
      list.push(session)
      buckets.set(path, list)
    }
    // Always surface the active Grok workspace, even when it has no sessions yet.
    if (activePath && ![...buckets.keys()].some((path) => sameWorkspacePath(path, activePath))) {
      buckets.set(activePath, [])
    }
    const groups = [...buckets.entries()].map(([path, list]) => ({
      path,
      name: workspaceLeafName(path),
      active: activePath ? sameWorkspacePath(path, activePath) : false,
      sessions: [...list].sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0)),
    }))
    groups.sort((a, b) => {
      if (a.active !== b.active) return a.active ? -1 : 1
      const aTime = a.sessions[0]?.updatedAt || 0
      const bTime = b.sessions[0]?.updatedAt || 0
      return bTime - aTime
    })
    return groups
  })

  const isTurnRunning = computed(() => Boolean(activeTurn.value))
  /** Session ids currently running a turn — drives the sidebar green pulse. */
  const runningSessionIds = computed(() => {
    const ids = new Set<string>()
    if (activeTurn.value?.sessionId) {
      ids.add(activeTurn.value.sessionId)
      const resolved = resolveSessionId(activeTurn.value.sessionId)
      if (resolved) ids.add(resolved)
    }
    // Pending draft session before turn.started / session.bound arrives.
    if (sending.value && activeSessionId.value) {
      ids.add(activeSessionId.value)
      const resolved = resolveSessionId(activeSessionId.value)
      if (resolved) ids.add(resolved)
    }
    return [...ids]
  })
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
  }

  function liveWriteKey(eventSessionId: string): string {
    // Prefer the currently active/native id so UI reads the same bucket after session.bound.
    const resolved = resolveSessionId(eventSessionId)
    if (activeSessionId.value && sameGrokSession(activeSessionId.value, eventSessionId)) {
      return activeSessionId.value
    }
    if (activeTurn.value && sameGrokSession(activeTurn.value.sessionId, eventSessionId)) {
      return resolveSessionId(activeTurn.value.sessionId) || activeTurn.value.sessionId
    }
    return resolved || eventSessionId
  }

  function handleEvent(payload: Record<string, unknown>): void {
    const type = String(payload.type || '')
    const sessionId = String(payload.sessionId || '')
    const turnId = String(payload.turnId || '')
    const data = (payload.data && typeof payload.data === 'object' && !Array.isArray(payload.data))
      ? payload.data as Record<string, unknown>
      : {}

    if (type === 'turn.started') {
      const key = liveWriteKey(sessionId) || sessionId
      if (turnId) {
        finalizedTurnIds.delete(turnId)
        streamSequenceByTurn.delete(`${turnId}:text`)
        streamSequenceByTurn.delete(`${turnId}:thought`)
      }
      const sameTurn = Boolean(
        activeTurn.value
        && turnId
        && activeTurn.value.turnId === turnId
        && sameGrokSession(activeTurn.value.sessionId, sessionId),
      )
      // Frontend locks with grok-turn-pending-* before the API returns the real id.
      // That upgrade must NOT wipe buffers if any early deltas already landed.
      const upgradingFromPending = Boolean(
        activeTurn.value
        && turnId
        && activeTurn.value.turnId.startsWith('grok-turn-pending-')
        && sameGrokSession(activeTurn.value.sessionId, sessionId),
      )
      activeTurn.value = {
        backend: String(payload.backend || backendId.value),
        sessionId: key,
        turnId,
      }
      sending.value = true
      if (!sameTurn && !upgradingFromPending) {
        liveTextBySession.value = { ...liveTextBySession.value, [key]: '' }
        liveThoughtBySession.value = { ...liveThoughtBySession.value, [key]: '' }
        liveActivityBySession.value = { ...liveActivityBySession.value, [key]: [] }
      }
      if (turnId) {
        turnStartedAtById.value = { ...turnStartedAtById.value, [turnId]: Date.now() }
      }
      // Seed duration clock on the logical timeline turn (user message already local).
      seedTurnStartMetrics(key, turnId)
      return
    }
    if (type === 'thought.delta') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      const delta = String(data.delta || data.text || data.data || '')
      if (!sessionId || !delta) return
      const key = liveWriteKey(sessionId) || sessionId
      if (!activeTurn.value || !sameGrokSession(activeTurn.value.sessionId, sessionId)) {
        activeTurn.value = {
          backend: String(payload.backend || backendId.value),
          sessionId: key,
          turnId: turnId || activeTurn.value?.turnId || `grok-turn-${Date.now()}`,
        }
      }
      const prev = liveThoughtBySession.value[key] || ''
      const sequence = Number(data.sequence || 0)
      const streamKey = `${turnId || key}:thought`
      if (sequence > 0) {
        const previousSequence = streamSequenceByTurn.get(streamKey) || 0
        if (sequence <= previousSequence) return
        streamSequenceByTurn.set(streamKey, sequence)
      }
      const snapshot = String(data.text || '')
      // Cap thought buffer so long reasoning doesn't bloat the UI state.
      const next = (sequence > 0 && snapshot ? snapshot : prev + delta).slice(-4000)
      liveThoughtBySession.value = { ...liveThoughtBySession.value, [key]: next }
      return
    }
    if (type === 'activity.snapshot') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      if (!sessionId) return
      const key = liveWriteKey(sessionId) || sessionId
      const messages = Array.isArray(data.messages) ? data.messages as GrokMessage[] : []
      liveActivityBySession.value = { ...liveActivityBySession.value, [key]: messages }
      return
    }
    if (type === 'text.delta') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      const delta = String(data.delta || data.text || data.data || '')
      if (!sessionId || !delta) return
      const key = liveWriteKey(sessionId) || sessionId
      // Keep activeTurn pinned even if turn.started was missed.
      if (!activeTurn.value || !sameGrokSession(activeTurn.value.sessionId, sessionId)) {
        activeTurn.value = {
          backend: String(payload.backend || backendId.value),
          sessionId: key,
          turnId: turnId || activeTurn.value?.turnId || `grok-turn-${Date.now()}`,
        }
      }
      const streamKey = `${turnId || key}:text`
      const sequence = Number(data.sequence || 0)
      if (sequence > 0) {
        const previousSequence = streamSequenceByTurn.get(streamKey) || 0
        if (sequence <= previousSequence) return
        streamSequenceByTurn.set(streamKey, sequence)
      }
      const prev = liveTextBySession.value[key] || ''
      const snapshot = String(data.text || '')
      const nextLive = sequence > 0 && snapshot ? snapshot : prev + delta
      liveTextBySession.value = { ...liveTextBySession.value, [key]: nextLive }
      // Answer tokens started — drop thought stream from the live row.
      if (liveThoughtBySession.value[key]) {
        const nextThought = { ...liveThoughtBySession.value }
        delete nextThought[key]
        liveThoughtBySession.value = nextThought
      }
      return
    }
    if (type === 'session.bound') {
      const nextId = String(data.sessionId || '')
      if (nextId && nextId !== sessionId) {
        rememberSessionAlias(sessionId, nextId)
        if (activeSessionId.value === sessionId || sameGrokSession(activeSessionId.value, sessionId)) {
          activeSessionId.value = nextId
        }
        if (activeTurn.value && sameGrokSession(activeTurn.value.sessionId, sessionId)) {
          activeTurn.value = { ...activeTurn.value, sessionId: nextId }
        }
        messagesBySession.value = remapSessionBucket(messagesBySession.value, sessionId, nextId)
        liveTextBySession.value = remapSessionBucket(liveTextBySession.value, sessionId, nextId)
        liveThoughtBySession.value = remapSessionBucket(liveThoughtBySession.value, sessionId, nextId)
        liveActivityBySession.value = remapSessionBucket(liveActivityBySession.value, sessionId, nextId)
        tokenUsageBySession.value = remapSessionBucket(tokenUsageBySession.value, sessionId, nextId)
        remapTurnMetricsSession(sessionId, nextId)
        const movedQueue = queuedBySession.value[sessionId]
        if (movedQueue?.length) {
          const nextQueues = { ...queuedBySession.value }
          const existing = nextQueues[nextId] ?? []
          nextQueues[nextId] = [
            ...existing,
            ...movedQueue.map((item) => ({ ...item, sessionId: nextId })),
          ]
          delete nextQueues[sessionId]
          queuedBySession.value = nextQueues
        }
      }
      void loadSessions()
      return
    }
    if (type === 'turn.completed' || type === 'turn.failed' || type === 'turn.interrupted') {
      // Ignore stale completion for a turn that is no longer active (or never was).
      if (
        turnId
        && activeTurn.value?.turnId
        && activeTurn.value.turnId !== turnId
        && !activeTurn.value.turnId.startsWith('grok-turn-pending-')
      ) {
        return
      }
      if (turnId) finalizedTurnIds.add(turnId)
      const key = liveWriteKey(sessionId) || sessionId
      // Capture metrics before clearTurnState wipes activeTurn.
      applyTurnUsageMetrics(key, turnId, data)
      // Release the turn lock immediately so a slow openSession cannot race with
      // the next turn (stale finally must not cancel a newer in-flight run).
      clearTurnState(sessionId, turnId)
      const targetSession = key || sessionId
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
        notify('error', translate('notifications.turnFailed'), String(data.message || ''))
      }
      // Reload sidebar usage after local usage.json is updated by the backend.
      void appStore.loadLocalUsage()
      // Reload history first, then drain queue so openSession cannot wipe the next user row.
      void (async () => {
        try {
          if (targetSession && !targetSession.startsWith('pending-grok-')) {
            await openSession(targetSession)
          } else {
            await loadSessions(true)
          }
        } catch {
          // openSession already notifies; still try to drain.
        }
        await drainQueue(targetSession || activeSessionId.value)
      })()
    }
  }

  function timelineTurnKey(sessionId: string, turnIndex: number): string {
    return `${sessionId}:t${Math.max(1, turnIndex)}`
  }

  function countUserTurns(sessionId: string): number {
    const messages = messagesBySession.value[sessionId] ?? []
    let count = 0
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

  async function loadSessions(force = false): Promise<void> {
    // Avoid rescanning ~/.grok on every tab flip — that is the main switch hitch.
    if (!force && sessions.value.length > 0 && Date.now() - sessionsLoadedAt < 20_000 && !search.value.trim()) {
      return
    }
    // Workspace is preferred for ordering/active group, but native sessions can list across projects.
    const workspace = workspacePath.value
    try {
      const list = await listGrokSessions(backendId.value, workspace, search.value.trim())
      sessions.value = list ?? []
      sessionsLoadedAt = Date.now()
    } catch (error) {
      sessions.value = []
      const message = errorMessage(error)
      if (/unknown bound method|binding call failed/i.test(message)) {
        notify('warning', translate('sidebar.runtimeSwitchFailed'), translate('sidebar.grokBindingsStale'))
        return
      }
      notify('error', translate('notifications.taskOpenFailed'), message)
    }
  }

  async function openSession(sessionID: string): Promise<void> {
    const id = sessionID.trim()
    if (!id) return
    loadingSessionId.value = id
    activeSessionId.value = id
    try {
      const detail = await readGrokSession(backendId.value, id)
      messagesBySession.value = {
        ...messagesBySession.value,
        [id]: detail.messages ?? [],
      }
      if (detail.summary?.id) {
        const others = sessions.value.filter((item) => item.id !== detail.summary.id)
        sessions.value = [detail.summary, ...others]
      }
      // Hydrate per-turn token footers from local session updates.jsonl.
      void hydrateSessionTurnUsages(id)
    } catch (error) {
      const message = errorMessage(error)
      if (/unknown bound method|binding call failed/i.test(message)) {
        notify('warning', translate('notifications.taskOpenFailed'), translate('sidebar.grokBindingsStale'))
      } else {
        notify('error', translate('notifications.taskOpenFailed'), message)
      }
    } finally {
      if (loadingSessionId.value === id) loadingSessionId.value = ''
    }
  }

  async function hydrateSessionTurnUsages(sessionID: string): Promise<void> {
    const id = sessionID.trim()
    if (!id || id.startsWith('pending-grok-')) return
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
    activeTurn.value = null
    sending.value = false
  }

  function ensureSession(message: string, workspace: string): string {
    let sessionId = activeSessionId.value
    if (sessionId) return sessionId
    sessionId = `pending-grok-${Date.now()}`
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
    return sessionId
  }

  function appendLocalUserMessage(sessionId: string, message: string): void {
    const userMessage: GrokMessage = {
      id: `grok-user-${Date.now()}-${++queuedSequence}`,
      role: 'user',
      text: message,
      status: 'completed',
      createdAt: Date.now(),
    }
    messagesBySession.value = {
      ...messagesBySession.value,
      [sessionId]: [...(messagesBySession.value[sessionId] ?? []), userMessage],
    }
  }

  function enqueueMessage(sessionId: string, text: string, images: string[]): GrokQueuedMessage {
    const item: GrokQueuedMessage = {
      id: `grok-queued-${Date.now()}-${++queuedSequence}`,
      sessionId,
      text,
      images: [...images],
      state: 'queued',
      error: '',
      createdAt: Date.now(),
    }
    queuedBySession.value = {
      ...queuedBySession.value,
      [sessionId]: [...(queuedBySession.value[sessionId] ?? []), item],
    }
    return item
  }

  function patchQueuedMessage(sessionId: string, messageId: string, patch: Partial<GrokQueuedMessage>): void {
    const list = queuedBySession.value[sessionId]
    if (!list?.length) return
    queuedBySession.value = {
      ...queuedBySession.value,
      [sessionId]: list.map((item) => (item.id === messageId ? { ...item, ...patch } : item)),
    }
  }

  function removeQueuedMessageFromSession(sessionId: string, messageId: string): void {
    const list = queuedBySession.value[sessionId]
    if (!list?.length) return
    const next = list.filter((item) => item.id !== messageId)
    const queues = { ...queuedBySession.value }
    if (next.length) queues[sessionId] = next
    else delete queues[sessionId]
    queuedBySession.value = queues
  }

  function removeQueuedMessage(messageId: string): void {
    for (const [sessionId, list] of Object.entries(queuedBySession.value)) {
      if (!list.some((item) => item.id === messageId)) continue
      removeQueuedMessageFromSession(sessionId, messageId)
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
    options?: { alreadyLocked?: boolean },
  ): Promise<boolean> {
    const workspace = workspacePath.value
    if (!workspace) {
      notify('warning', translate('app.needWorkspace'), translate('app.needWorkspaceHintReady'))
      return false
    }
    const pendingTurnId = options?.alreadyLocked && activeTurn.value?.turnId?.startsWith('grok-turn-pending-')
      ? activeTurn.value.turnId
      : `grok-turn-pending-${Date.now()}`
    if (!options?.alreadyLocked) {
      sending.value = true
      activeTurn.value = {
        backend: backendId.value,
        sessionId,
        turnId: pendingTurnId,
      }
      turnStartedAtById.value = { ...turnStartedAtById.value, [pendingTurnId]: Date.now() }
      seedTurnStartMetrics(sessionId, pendingTurnId)
    } else if (!turnStartedAtById.value[pendingTurnId]) {
      turnStartedAtById.value = { ...turnStartedAtById.value, [pendingTurnId]: Date.now() }
      seedTurnStartMetrics(sessionId, pendingTurnId)
    }
    try {
      const ref = await sendGrokMessageApi({
        backend: backendId.value,
        sessionId,
        workspace,
        text: message,
        images,
        model: backendId.value === 'api'
          ? (appStore.settings.grokAPIModel || '')
          : (appStore.settings.grokBuildModel || ''),
        effort: appStore.settings.grokEffort || 'high',
      })
      const nextTurnId = ref.turnId || pendingTurnId
      // Preserve start clock across pending → real turn id.
      if (nextTurnId !== pendingTurnId && turnStartedAtById.value[pendingTurnId]) {
        turnStartedAtById.value = {
          ...turnStartedAtById.value,
          [nextTurnId]: turnStartedAtById.value[pendingTurnId],
        }
      }
      // Only adopt the API turn if we still own this pending lock (not interrupted).
      if (activeTurn.value && (
        activeTurn.value.turnId === pendingTurnId
        || activeTurn.value.turnId === nextTurnId
        || activeTurn.value.turnId.startsWith('grok-turn-pending-')
      )) {
        activeTurn.value = {
          backend: ref.backend || backendId.value,
          sessionId: ref.sessionId || sessionId,
          turnId: nextTurnId,
        }
      }
      if (ref.sessionId && ref.sessionId !== sessionId) {
        rememberSessionAlias(sessionId, ref.sessionId)
        if (activeSessionId.value === sessionId || sameGrokSession(activeSessionId.value, sessionId)) {
          activeSessionId.value = ref.sessionId
        }
        remapTurnMetricsSession(sessionId, ref.sessionId)
      }
      return true
    } catch (error) {
      clearTurnState(sessionId, pendingTurnId)
      notify('error', translate('notifications.messageNotSent'), errorMessage(error))
      return false
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
    const next = list.find((item) => item.state === 'queued' || item.state === 'failed')
    if (!next) return
    if (isSessionBusy(id) || sending.value || activeTurn.value) return

    patchQueuedMessage(queueSessionId, next.id, { state: 'sending', error: '' })
    // Queued follow-ups already show in the queue strip; inject into the timeline once
    // when the item actually starts sending (avoid double-append on retry).
    if (!next.localAppended && (next.text || next.images.length)) {
      appendLocalUserMessage(resolveSessionId(queueSessionId) || queueSessionId, next.text)
      patchQueuedMessage(queueSessionId, next.id, { localAppended: true })
    }
    const ok = await dispatchTurn(
      resolveSessionId(queueSessionId) || queueSessionId,
      next.text,
      next.images,
    )
    if (ok) {
      removeQueuedMessageFromSession(queueSessionId, next.id)
      return
    }
    patchQueuedMessage(queueSessionId, next.id, {
      state: 'failed',
      error: translate('notifications.messageNotSent'),
      localAppended: true,
    })
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
    if (index > 0) {
      const [item] = list.splice(index, 1)
      list.unshift(item)
      queuedBySession.value = { ...queuedBySession.value, [sessionId]: list }
    }
    patchQueuedMessage(sessionId, messageId, { state: 'queued', error: '' })

    if (isSessionBusy(sessionId) || activeTurn.value) {
      await interruptTurn()
      // turn.interrupted / completed will drain the queue.
      return
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
  async function sendMessage(text: string, images: string[] = []): Promise<boolean> {
    const message = text.trim()
    if (!message && !images.length) return false
    const workspace = workspacePath.value
    if (!workspace) {
      notify('warning', translate('app.needWorkspace'), translate('app.needWorkspaceHintReady'))
      return false
    }
    if (!isReady.value) {
      notify('warning', translate('notifications.connectionFailed'), translate('sidebar.grokRuntimeMissing'))
      return false
    }

    const sessionId = ensureSession(message, workspace)
    // Synchronous lock BEFORE any await — prevents double-Enter from starting two
    // CLI processes where the second used to cancel the first mid-run.
    if (isSessionBusy(sessionId) || sending.value || Boolean(activeTurn.value)) {
      enqueueMessage(sessionId, message, images)
      return true
    }

    const pendingTurnId = `grok-turn-pending-${Date.now()}`
    sending.value = true
    activeTurn.value = {
      backend: backendId.value,
      sessionId,
      turnId: pendingTurnId,
    }
    turnStartedAtById.value = { ...turnStartedAtById.value, [pendingTurnId]: Date.now() }
    seedTurnStartMetrics(sessionId, pendingTurnId)
    appendLocalUserMessage(sessionId, message)
    return dispatchTurn(sessionId, message, images, { alreadyLocked: true })
  }

  async function interruptTurn(): Promise<void> {
    const ref = activeTurn.value
    if (!ref) {
      sending.value = false
      return
    }
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
      // Optimistic release — backend also emits turn.interrupted; drain happens there.
      // If the event is delayed/lost, still unlock the composer.
      window.setTimeout(() => {
        if (activeTurn.value?.turnId === ref.turnId) {
          clearTurnState(ref.sessionId, ref.turnId)
          void drainQueue(ref.sessionId)
        }
      }, 1500)
    } catch (error) {
      // Force-unlock so a dead turn cannot brick the send button forever.
      clearTurnState(ref.sessionId, ref.turnId)
      notify('error', translate('notifications.turnStopFailed'), errorMessage(error))
      void drainQueue(ref.sessionId)
    }
  }

  function discardLocalSession(sessionID: string): void {
    sessions.value = sessions.value.filter((item) => item.id !== sessionID)
    archivedSessions.value = archivedSessions.value.filter((item) => item.id !== sessionID)
    if (activeSessionId.value === sessionID || sameGrokSession(activeSessionId.value, sessionID)) {
      activeSessionId.value = ''
      const next = { ...messagesBySession.value }
      delete next[sessionID]
      messagesBySession.value = next
    }
    const queues = { ...queuedBySession.value }
    delete queues[sessionID]
    queuedBySession.value = queues
    if (activeTurn.value && sameGrokSession(activeTurn.value.sessionId, sessionID)) {
      clearTurnState(sessionID)
    }
  }

  async function renameSession(sessionID: string, name?: string): Promise<boolean> {
    const id = sessionID.trim()
    if (!id || sessionMutation.value) return false
    const current = sessions.value.find((item) => item.id === id)
      || archivedSessions.value.find((item) => item.id === id)
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

    sessionMutation.value = 'rename'
    try {
      if (id.startsWith('pending-grok-')) {
        sessions.value = sessions.value.map((item) =>
          item.id === id ? { ...item, name: nextName, updatedAt: Date.now() } : item,
        )
        notify('success', translate('threadActions.renamed'), nextName)
        return true
      }
      const summary = await renameGrokSessionApi(backendId.value, id, nextName)
      const patched = { ...(current || summary), ...summary, name: summary.name || nextName }
      sessions.value = sessions.value.map((item) => (item.id === id ? patched : item))
      archivedSessions.value = archivedSessions.value.map((item) => (item.id === id ? patched : item))
      notify('success', translate('threadActions.renamed'), patched.name)
      return true
    } catch (error) {
      notify('error', translate('threadActions.renameFailed'), errorMessage(error))
      return false
    } finally {
      sessionMutation.value = ''
    }
  }

  async function renameActiveSession(name?: string): Promise<void> {
    const id = activeSessionId.value
    if (!id) return
    await renameSession(id, name)
  }

  async function archiveSession(sessionID: string): Promise<void> {
    const id = sessionID.trim()
    if (!id || sessionMutation.value) return
    sessionMutation.value = 'archive'
    try {
      const current = sessions.value.find((item) => item.id === id)
      if (!id.startsWith('pending-grok-')) {
        await archiveGrokSessionApi(backendId.value, id)
      }
      if (current) {
        archivedSessions.value = [current, ...archivedSessions.value.filter((item) => item.id !== id)]
      }
      sessions.value = sessions.value.filter((item) => item.id !== id)
      if (activeSessionId.value === id || sameGrokSession(activeSessionId.value, id)) {
        activeSessionId.value = ''
        const next = { ...messagesBySession.value }
        delete next[id]
        messagesBySession.value = next
      }
      notify('success', translate('threadActions.archived'), translate('threadActions.archivedHint'))
      void loadArchivedSessions()
    } catch (error) {
      notify('error', translate('threadActions.archiveFailed'), errorMessage(error))
    } finally {
      sessionMutation.value = ''
    }
  }

  async function archiveActiveSession(): Promise<void> {
    const id = activeSessionId.value
    if (!id) return
    await archiveSession(id)
  }

  async function unarchiveSession(sessionID: string): Promise<void> {
    const id = sessionID.trim()
    if (!id || sessionMutation.value) return
    sessionMutation.value = 'unarchive'
    try {
      const summary = await unarchiveGrokSessionApi(backendId.value, id)
      archivedSessions.value = archivedSessions.value.filter((item) => item.id !== id)
      if (summary?.id) {
        sessions.value = [summary, ...sessions.value.filter((item) => item.id !== summary.id)]
      }
      notify('success', translate('threadActions.unarchived'), translate('threadActions.unarchivedHint'))
      void loadSessions(true)
    } catch (error) {
      notify('error', translate('threadActions.unarchiveFailed'), errorMessage(error))
    } finally {
      sessionMutation.value = ''
    }
  }

  async function loadArchivedSessions(searchQuery = ''): Promise<void> {
    try {
      const list = await listArchivedGrokSessions(backendId.value, searchQuery)
      archivedSessions.value = list ?? []
    } catch {
      archivedSessions.value = []
    }
  }

  async function deleteSession(sessionID: string, options: { confirm?: boolean } = {}): Promise<void> {
    const id = sessionID.trim()
    if (!id || sessionMutation.value) return
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

    sessionMutation.value = 'delete'
    try {
      if (!id.startsWith('pending-grok-')) {
        await deleteGrokSessionApi(backendId.value, id)
      }
      discardLocalSession(id)
      notify('success', translate('threadActions.deleted'), translate('threadActions.deletedHint'))
    } catch (error) {
      notify('error', translate('threadActions.deleteFailed'), errorMessage(error))
    } finally {
      sessionMutation.value = ''
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
      // Non-blocking load: switch UI first, hydrate sessions in background.
      void refreshRuntime()
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
    activeTurn,
    queuedBySession,
    turnMetricsByKey,
    search,
    backendId,
    workspacePath,
    activeMessages,
    activeQueuedMessages,
    activeItems,
    activeTurnMetrics,
    activeTokenUsage,
    isTurnRunning,
    runningSessionIds,
    isReady,
    bootstrapEvents,
    dispose,
    refreshRuntime,
    loadSessions,
    loadArchivedSessions,
    openSession,
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
