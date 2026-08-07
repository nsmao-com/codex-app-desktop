import { Events } from '@wailsio/runtime'
import { defineStore } from 'pinia'
import { computed, shallowRef, watch } from 'vue'

import type { TimelineItem, TokenUsageBreakdown, TurnMetrics } from '@/types/codex'
import {
  archiveClaudeSession as archiveClaudeSessionApi,
  deleteClaudeSession as deleteClaudeSessionApi,
  interruptClaudeTurn as interruptClaudeTurnApi,
  listArchivedClaudeSessions,
  listClaudeSessionTurnUsages,
  listClaudeSessions,
  readClaudeSession,
  readClaudeSessionHistory,
  refreshClaudeRuntime,
  renameClaudeSession as renameClaudeSessionApi,
  sendClaudeMessage as sendClaudeMessageApi,
  unarchiveClaudeSession as unarchiveClaudeSessionApi,
  type ClaudeMessage,
  type ClaudeRuntimeStatus,
  type ClaudeSessionDetail,
  type ClaudeSessionSummary,
  type ClaudeTurnRef,
} from '@/utils/claudeBindings'
import { notify } from '@/utils/notify'
import { translate } from '@/i18n'
import { normalizeThreadTokenUsage } from '@/utils/protocol'
import { resolveProviderModelContextWindow } from '@/utils/accountUsage'
import { sameWorkspacePath, workspaceKey } from '@/utils/workspacePath'
import { useAppStore } from './app'
import { useDialogStore } from './dialog'
import { useWorkspaceStore } from './workspace'

function workspaceLeafName(path: string): string {
  if (!path || path === '(unknown)') return path || 'unknown'
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

function looksLikeFilesystemPath(path: string): boolean {
  return /^[a-zA-Z]:[\\/]/.test(path) || path.startsWith('/') || path.startsWith('~/')
}

function attachmentName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

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

function emptyRuntime(): ClaudeRuntimeStatus {
  return {
    available: false,
    authenticated: false,
    version: '',
    executable: '',
    message: '',
  }
}

function messageToItem(message: ClaudeMessage, turnId: string): TimelineItem {
  const role = (message.role || '').toLowerCase()
  const isUser = role === 'user' || role === 'human'
  const isReasoning = role === 'reasoning'
  const text = message.text || ''
  const base = {
    id: message.id || `claude-msg-${message.createdAt}-${Math.random().toString(36).slice(2, 8)}`,
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
  if (isUser) return { ...base, type: 'userMessage', text }
  if (isReasoning) return { ...base, type: 'reasoning', text, reasoningSummary: text }
  if (message.toolName) {
    return {
      ...base,
      type: 'dynamicToolCall',
      title: message.toolName,
      text: message.toolName,
      output: text,
    }
  }
  return { ...base, type: 'agentMessage', text }
}

function sameClaudeUserRow(left: TimelineItem, right: TimelineItem): boolean {
  if (left.type !== 'userMessage' || right.type !== 'userMessage') return false
  if (left.id && right.id && left.id === right.id) return true
  if (left.turnId && right.turnId && left.turnId === right.turnId) return true
  if ((left.text || '').trim() !== (right.text || '').trim()) return false

  // Repeated prompts are valid. Text alone may only bridge a provisional local
  // turn to its native row when both timestamps describe the same send.
  const leftReal = left.turnId?.startsWith('claude-turn-')
  const rightReal = right.turnId?.startsWith('claude-turn-')
  if (leftReal && rightReal) return false
  const toSeconds = (value?: number) => value && value > 1_000_000_000_000 ? value / 1000 : (value || 0)
  const leftTime = toSeconds(left.startedAt || left.completedAt)
  const rightTime = toSeconds(right.startedAt || right.completedAt)
  return leftTime > 0 && rightTime > 0 && Math.abs(leftTime - rightTime) <= 5
}

export interface ClaudeSessionGroup {
  path: string
  name: string
  active: boolean
  sessions: ClaudeSessionSummary[]
}

export interface ClaudeQueuedMessage {
  id: string
  sessionId: string
  workspace: string
  model: string
  effort: string
  text: string
  images: string[]
  state: 'queued' | 'sending' | 'failed'
  error: string
  createdAt: number
  blockedByTurnId?: string
  localAppended?: boolean
}

interface ClaudeStreamPatch {
  sessionId: string
  turnId: string
  id: string
  role: string
  text: string
  mode: 'append' | 'replace'
}

interface ClaudeHistoryState {
  start: number
  total: number
  turnOffset: number
  hasEarlier: boolean
  loadingEarlier: boolean
  loadedUpdatedAt: number
}

function claudeTextTailAfterActivity(fullText: string, activity: ClaudeMessage[]): string {
  if (!fullText) return ''
  let cursor = 0
  let matched = false
  for (const message of activity) {
    const role = (message.role || '').toLowerCase()
    if (role !== 'assistant' || message.toolName) continue
    const segment = (message.text || '').trim()
    if (!segment) continue
    const index = fullText.indexOf(segment, cursor)
    if (index < 0) return matched ? fullText.slice(cursor).trimStart() : fullText
    cursor = index + segment.length
    matched = true
  }
  return matched ? fullText.slice(cursor).trimStart() : fullText
}

/** Replace the cumulative live agent row with native ordered activity plus its uncommitted tail. */
function mergeClaudeLiveActivity(
  base: TimelineItem[],
  activity: ClaudeMessage[],
  turnId: string,
): TimelineItem[] {
  const replaceable = (item: TimelineItem) =>
    item.turnId === turnId && (item.type === 'agentMessage' || item.type === 'reasoning')
  const firstIndex = base.findIndex(replaceable)
  const insertionIndex = firstIndex >= 0 ? firstIndex : base.length
  const currentAgent = [...base].reverse().find((item) =>
    item.turnId === turnId && item.type === 'agentMessage',
  )
  const currentReasoning = [...base].reverse().find((item) =>
    item.turnId === turnId && item.type === 'reasoning',
  )
  const hasNativeReasoning = activity.some((message) =>
    (message.role || '').toLowerCase() === 'reasoning',
  )
  const ordered = activity.map((message) => messageToItem(message, turnId))
  if (currentReasoning && !hasNativeReasoning) ordered.unshift(currentReasoning)

  const tail = claudeTextTailAfterActivity(currentAgent?.text || '', activity)
  if (tail) {
    ordered.push(currentAgent
      ? { ...currentAgent, text: tail }
      : messageToItem({
          id: `${turnId}:agent`,
          role: 'assistant',
          text: tail,
          status: 'inProgress',
          createdAt: Math.floor(Date.now() / 1000),
        }, turnId))
  }

  const prefix = base.slice(0, insertionIndex).filter((item) => !replaceable(item))
  const suffix = base.slice(insertionIndex).filter((item) => !replaceable(item))
  return [...prefix, ...ordered, ...suffix]
}

export const useClaudeStore = defineStore('claude', () => {
  const appStore = useAppStore()
  const dialogStore = useDialogStore()
  const workspaceStore = useWorkspaceStore()

  const runtime = shallowRef<ClaudeRuntimeStatus>(emptyRuntime())
  const sessions = shallowRef<ClaudeSessionSummary[]>([])
  const archivedSessions = shallowRef<ClaudeSessionSummary[]>([])
  const activeSessionId = shallowRef('')
  const itemsBySession = shallowRef<Record<string, TimelineItem[]>>({})
  const historyBySession = shallowRef<Record<string, ClaudeHistoryState>>({})
  const loadingSessionId = shallowRef('')
  const sendingSessionIds = shallowRef<string[]>([])
  const interruptingSessionIds = shallowRef<string[]>([])
  const search = shallowRef('')
  const runningSessionIds = shallowRef<string[]>([])
  const activeTurnBySession = shallowRef<Record<string, ClaudeTurnRef | undefined>>({})
  /** turnId → metrics for timeline token footer */
  const activeTurnMetrics = shallowRef<Record<string, TurnMetrics>>({})
  const turnStartedAtById = shallowRef<Record<string, number>>({})
  const tokenUsageBySession = shallowRef<Record<string, ReturnType<typeof normalizeThreadTokenUsage>>>({})
  /** Ordered provider transcript assistant/reasoning/tool rows for the active turn. */
  const liveActivityBySession = shallowRef<Record<string, ClaudeMessage[]>>({})
  const queueBySession = shallowRef<Record<string, ClaudeQueuedMessage[]>>({})
  /** real session id → pending id (and reverse) while a create is in flight */
  const sessionAlias = new Map<string, string>()
  /** Latest history request and active loading barrier for each session. */
  const sessionOpenSequence = new Map<string, number>()
  const loadingSequenceBySession = new Map<string, number>()
  /** Per-item bridge sequence; cumulative snapshots make out-of-order delivery harmless. */
  const streamSequenceByItem = new Map<string, number>()
  const pendingStreamPatches = new Map<string, ClaudeStreamPatch>()
  const streamedAssistantTurns = new Set<string>()
  const finalizedTurnIds = new Set<string>()
  const latestStartedTurnBySession = new Map<string, string>()
  const discardedSessionIds = new Set<string>()
  const loadedSessionIds = new Set<string>()
  let eventUnsub: (() => void) | null = null
  let streamFlushTimer = 0
  let sessionLoadSequence = 0
  let queuedSequence = 0

  function rememberFinalizedClaudeTurn(turnId: string): void {
    if (!turnId) return
    finalizedTurnIds.delete(turnId)
    finalizedTurnIds.add(turnId)
    while (finalizedTurnIds.size > 512) {
      const oldest = finalizedTurnIds.values().next().value
      if (!oldest) break
      finalizedTurnIds.delete(oldest)
    }
  }

  function rememberDiscardedClaudeSession(sessionId: string): void {
    if (!sessionId) return
    discardedSessionIds.delete(sessionId)
    discardedSessionIds.add(sessionId)
    while (discardedSessionIds.size > 512) {
      const oldest = discardedSessionIds.values().next().value
      if (!oldest) break
      discardedSessionIds.delete(oldest)
    }
  }

  const workspacePath = computed(() =>
    appStore.settings.claudeWorkspace || appStore.settings.workspace || '',
  )

  watch(workspacePath, (next, previous) => {
    if (!previous || sameWorkspacePath(next, previous) || !activeSessionId.value) return
    const selected = sessions.value.find((item) => sameClaudeSession(item.id, activeSessionId.value))
    if (selected?.workspace && sameWorkspacePath(selected.workspace, next)) return
    // A project-only switch has no target session yet. Detach the old session so
    // typing during the following list load can only create a chat in the new project.
    activeSessionId.value = ''
    loadingSessionId.value = ''
  }, { flush: 'sync' })

  /** Route stream events to the timeline bucket that actually has the turn. */
  function resolveEventSessionId(sessionId: string): string {
    const raw = (sessionId || '').trim()
    if (!raw) return activeSessionId.value || ''
    // Never infer pending ownership from whichever session is active: a stream
    // from a background project may arrive while another new chat is selected.
    // The matching send response performs the authoritative pending → real bind.
    if (sessionAlias.has(raw)) return sessionAlias.get(raw) || raw
    return raw
  }

  /**
   * Move pending timeline → real session id without scrambling order.
   * Early stream deltas may already live under the real id; keep user bubbles first.
   */
  function promotePendingSession(pendingId: string, realId: string): void {
    if (!pendingId || !realId || pendingId === realId) return
    flushStreamPatches()
    sessionAlias.set(pendingId, realId)
    sessionAlias.set(realId, realId)

    const pending = itemsBySession.value[pendingId] || []
    const existing = itemsBySession.value[realId] || []
    const users = pending.filter((item) => item.type === 'userMessage')
    const pendingRest = pending.filter((item) => item.type !== 'userMessage')
    // Chronological intent: user (optimistic) → any early agent deltas → remaining pending.
    const merged = mergeClaudeTimeline(users, mergeClaudeTimeline(existing, pendingRest))

    const nextItems = { ...itemsBySession.value }
    delete nextItems[pendingId]
    nextItems[realId] = merged
    itemsBySession.value = nextItems

    const pendingHistory = historyBySession.value[pendingId]
    if (pendingHistory) {
      const nextHistory = { ...historyBySession.value }
      delete nextHistory[pendingId]
      if (!nextHistory[realId]) nextHistory[realId] = pendingHistory
      historyBySession.value = nextHistory
    }

    sessions.value = sessions.value.map((item) =>
      item.id === pendingId ? { ...item, id: realId } : item,
    )
    if (activeSessionId.value === pendingId) activeSessionId.value = realId
    remapSessionBusy(pendingId, realId)

    const pendingActivity = liveActivityBySession.value[pendingId]
    if (pendingActivity?.length) {
      const nextActivity = { ...liveActivityBySession.value }
      delete nextActivity[pendingId]
      nextActivity[realId] = [
        ...(nextActivity[realId] || []),
        ...pendingActivity.filter((item) => !(nextActivity[realId] || []).some((row) => row.id === item.id)),
      ]
      liveActivityBySession.value = nextActivity
    }

    const q = queueBySession.value[pendingId]
    if (q?.length) {
      const nextQ = { ...queueBySession.value }
      delete nextQ[pendingId]
      const existingIds = new Set((nextQ[realId] || []).map((row) => row.id))
      nextQ[realId] = [
        ...(nextQ[realId] || []),
        ...q.filter((row) => !existingIds.has(row.id)).map((row) => ({ ...row, sessionId: realId })),
      ].sort((left, right) => {
        if ((left.state === 'sending') !== (right.state === 'sending')) {
          return left.state === 'sending' ? -1 : 1
        }
        return left.createdAt - right.createdAt
      })
      queueBySession.value = nextQ
    }
    rememberLoadedClaudeSession(realId)
  }
  const isReady = computed(() => Boolean(runtime.value.available))
  const sending = computed(() =>
    Boolean(activeSessionId.value && sendingSessionIds.value.some((id) => sameClaudeSession(id, activeSessionId.value))),
  )
  const interrupting = computed(() =>
    Boolean(activeSessionId.value && interruptingSessionIds.value.some((id) => sameClaudeSession(id, activeSessionId.value))),
  )
  const activeItems = computed(() => {
    const sessionId = activeSessionId.value
    const base = [...(itemsBySession.value[sessionId] || [])]
    const turn = activeTurnBySession.value[sessionId]
    if (!turn?.turnId) return base
    const activity = liveActivityBySession.value[sessionId] || []
    if (!activity.length) return base
    return mergeClaudeLiveActivity(base, activity, turn.turnId)
  })
  const activeHistoryHasEarlier = computed(() => historyBySession.value[activeSessionId.value]?.hasEarlier === true)
  const activeHistoryEarlierCount = computed(() => historyBySession.value[activeSessionId.value]?.turnOffset ?? 0)
  const activeHistoryLoadingEarlier = computed(() => historyBySession.value[activeSessionId.value]?.loadingEarlier === true)
  const isTurnRunning = computed(() =>
    Boolean(activeSessionId.value && runningSessionIds.value.some((id) => sameClaudeSession(id, activeSessionId.value))),
  )
  const activeQueuedMessages = computed(() => queueBySession.value[activeSessionId.value] || [])
  const activeTurn = computed(() => activeTurnBySession.value[activeSessionId.value] || null)
  const activeTokenUsage = computed(() => {
    const id = activeSessionId.value
    if (!id) return null
    const usage = tokenUsageBySession.value[id] || null
    if (!usage) return null
    const summary = sessions.value.find((item) => item.id === id)
      ?? archivedSessions.value.find((item) => item.id === id)
    const model = summary?.model || appStore.settings.claudeModel || ''
    const contextWindow = resolveProviderModelContextWindow(appStore.agentProviders, 'claude', model)
    return contextWindow > 0 && usage.modelContextWindow !== contextWindow
      ? { ...usage, modelContextWindow: contextWindow }
      : usage
  })

  /** Group by workspace like Codex / Grok — normalize path keys so Win/Unix variants merge. */
  const sessionGroups = computed((): ClaudeSessionGroup[] => {
    const activePath = workspacePath.value
    // canonicalKey → { displayPath, sessions }
    const buckets = new Map<string, { path: string; sessions: ClaudeSessionSummary[] }>()
    for (const session of sessions.value) {
      const raw = (session.workspace || '').trim() || '(unknown)'
      const key = workspaceKey(raw)
      const bucket = buckets.get(key)
      if (bucket) {
        bucket.sessions.push(session)
        // Prefer a real absolute path over an encoded slug if we ever get both.
        if (looksLikeFilesystemPath(raw) && !looksLikeFilesystemPath(bucket.path)) {
          bucket.path = raw
        }
      } else {
        buckets.set(key, { path: raw, sessions: [session] })
      }
    }
    // Always surface the active Claude workspace, even with zero sessions.
    if (activePath && ![...buckets.values()].some((b) => sameWorkspacePath(b.path, activePath))) {
      buckets.set(workspaceKey(activePath), { path: activePath, sessions: [] })
    }
    const groups: ClaudeSessionGroup[] = [...buckets.values()].map(({ path, sessions: list }) => ({
      path,
      name: workspaceLeafName(path),
      active: activePath ? sameWorkspacePath(path, activePath) : false,
      sessions: [...list].sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0)),
    }))
    const orderedPaths = appStore.orderWorkspacePaths(
      'claude',
      groups.map((group) => group.path),
      appStore.settings.claudeRecentWorkspaces ?? [],
    )
    const order = new Map(orderedPaths.map((path, index) => [workspaceKey(path), index]))
    groups.sort((left, right) =>
      (order.get(workspaceKey(left.path)) ?? Number.MAX_SAFE_INTEGER)
      - (order.get(workspaceKey(right.path)) ?? Number.MAX_SAFE_INTEGER),
    )
    return groups
  })

  function bootstrapEvents(): void {
    if (eventUnsub) return
    eventUnsub = Events.On('claude:event', (event: any) => {
      const data = (event?.data ?? event) as Record<string, any>
      handleEvent(data)
    })
  }

  function dispose(): void {
    eventUnsub?.()
    eventUnsub = null
    if (streamFlushTimer) window.clearTimeout(streamFlushTimer)
    streamFlushTimer = 0
    pendingStreamPatches.clear()
  }

  function queueStreamPatch(patch: ClaudeStreamPatch): void {
    const key = `${patch.sessionId}:${patch.id}`
    const previous = pendingStreamPatches.get(key)
    if (previous && patch.mode === 'append') {
      pendingStreamPatches.set(key, {
        ...patch,
        text: previous.text + patch.text,
        mode: previous.mode,
      })
    } else {
      pendingStreamPatches.set(key, patch)
    }
    if (!streamFlushTimer) streamFlushTimer = window.setTimeout(flushStreamPatches, 48)
  }

  function flushStreamPatches(): void {
    if (streamFlushTimer) window.clearTimeout(streamFlushTimer)
    streamFlushTimer = 0
    if (!pendingStreamPatches.size) return

    const nextItems = { ...itemsBySession.value }
    const lists = new Map<string, TimelineItem[]>()
    for (const patch of pendingStreamPatches.values()) {
      let list = lists.get(patch.sessionId)
      if (!list) {
        list = [...(nextItems[patch.sessionId] || [])]
        lists.set(patch.sessionId, list)
      }
      let index = list.findIndex((item) => item.id === patch.id)
      if (index < 0 && patch.turnId) {
        index = list.findIndex((item) =>
          item.turnId === patch.turnId
          && (patch.role === 'reasoning' ? item.type === 'reasoning' : item.type === 'agentMessage'),
        )
      }
      if (index >= 0) {
        const current = list[index]!
        list[index] = {
          ...current,
          id: current.id || patch.id,
          turnId: patch.turnId || current.turnId,
          text: patch.mode === 'replace' ? patch.text : (current.text || '') + patch.text,
          status: 'inProgress',
          type: patch.role === 'reasoning' ? 'reasoning' : 'agentMessage',
        }
      } else {
        list.push(messageToItem({
          id: patch.id,
          role: patch.role,
          text: patch.text,
          status: 'inProgress',
          createdAt: Math.floor(Date.now() / 1000),
        }, patch.turnId || patch.sessionId))
      }
    }
    for (const [sessionId, list] of lists) nextItems[sessionId] = list
    pendingStreamPatches.clear()
    itemsBySession.value = nextItems
  }

  function handleEvent(data: Record<string, any>): void {
    const kind = String(data.kind || '')
    const rawSessionId = String(data.sessionId || '')
    const turnId = String(data.turnId || '')
    if (rawSessionId && discardedSessionIds.has(rawSessionId)) return
    if (!rawSessionId && !activeSessionId.value) return
    let sessionId = resolveEventSessionId(rawSessionId || activeSessionId.value)
    if (!sessionId) return

    if (kind === 'turn.started') {
      const clientSessionId = String(data.clientSessionId || '')
      if (
        clientSessionId.startsWith('pending-claude-')
        && rawSessionId
        && rawSessionId !== clientSessionId
      ) {
        promotePendingSession(clientSessionId, rawSessionId)
        sessionId = resolveEventSessionId(rawSessionId)
      }
      if (turnId) latestStartedTurnBySession.set(sessionId, turnId)
      // Turn ids are unique; delayed bridge delivery must not revive a turn whose
      // terminal event was already processed.
      if (turnId && finalizedTurnIds.has(turnId)) {
        const activeTurn = activeTurnBySession.value[sessionId]
        if (activeTurn?.turnId && activeTurn.turnId !== turnId) return
        markRunning(sessionId, false)
        markSending(sessionId, false)
        markInterrupting(sessionId, false)
        return
      }
      if (turnId) {
        streamedAssistantTurns.delete(turnId)
        for (const key of streamSequenceByItem.keys()) {
          if (key.startsWith(`${turnId}:`)) streamSequenceByItem.delete(key)
        }
      }
      markRunning(sessionId, true)
      activeTurnBySession.value = {
        ...activeTurnBySession.value,
        [sessionId]: { sessionId, turnId },
      }
      if (turnId) {
        const startedAt = Date.now()
        turnStartedAtById.value = {
          ...turnStartedAtById.value,
          [turnId]: startedAt,
        }
        activeTurnMetrics.value = {
          ...activeTurnMetrics.value,
          [turnId]: {
            ...emptyTurnMetrics(),
            startedAt,
          },
        }
      }
      liveActivityBySession.value = { ...liveActivityBySession.value, [sessionId]: [] }
      return
    }

    if (kind === 'message' && data.message) {
      flushStreamPatches()
      const msg = data.message as ClaudeMessage
      const role = (msg.role || '').toLowerCase()
      // Skip duplicate user rows (optimistic UI already showed this text).
      if (role === 'user' || role === 'human') {
        const list = itemsBySession.value[sessionId] || []
        const incoming = messageToItem(msg, turnId || sessionId)
        for (let i = list.length - 1; i >= Math.max(0, list.length - 8); i -= 1) {
          const row = list[i]
          if (row && sameClaudeUserRow(row, incoming)) {
            const next = [...list]
            next[i] = {
              ...row,
              id: msg.id || row.id,
              turnId: turnId || row.turnId,
              status: msg.status || row.status || 'completed',
            }
            itemsBySession.value = { ...itemsBySession.value, [sessionId]: next }
            return
          }
        }
      }
      // Assistant full messages from the wire are snapshots — prefer delta stream.
      if (role === 'assistant' || role === 'agent') {
        if (turnId && streamedAssistantTurns.has(turnId)) return
        const list = itemsBySession.value[sessionId] || []
        const id = msg.id || `${turnId}:agent`
        const index = list.findIndex((item) => item.id === id || (item.turnId === turnId && item.type === 'agentMessage'))
        if (index >= 0) {
          const current = list[index]
          const nextText = msg.text || ''
          // Only replace when snapshot is longer / current still empty — never shrink scrambled mid-stream.
          if (!current.text || nextText.length >= (current.text || '').length) {
            const next = [...list]
            next[index] = {
              ...current,
              id,
              turnId: turnId || current.turnId,
              text: nextText || current.text,
              status: msg.status || current.status,
            }
            itemsBySession.value = { ...itemsBySession.value, [sessionId]: next }
          }
          return
        }
      }
      appendMessage(sessionId, msg, turnId)
      return
    }

    if (kind === 'message.started') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      const id = String(data.id || `${turnId}:agent`)
      const role = String(data.role || 'assistant')
      const list = [...(itemsBySession.value[sessionId] || [])]
      if (list.some((item) => item.id === id)) return
      // Prefer updating an empty in-progress agent for this turn over appending a second bubble.
      const emptyAgent = list.findIndex((item) =>
        item.turnId === turnId && item.type === 'agentMessage' && !item.text,
      )
      const item = messageToItem({
        id, role, text: '', status: 'inProgress', createdAt: Math.floor(Date.now() / 1000),
      }, turnId || sessionId)
      if (emptyAgent >= 0) list[emptyAgent] = item
      else list.push(item)
      itemsBySession.value = { ...itemsBySession.value, [sessionId]: list }
      return
    }

    if (kind === 'activity.snapshot') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      const messages = Array.isArray(data.messages) ? data.messages as ClaudeMessage[] : []
      liveActivityBySession.value = { ...liveActivityBySession.value, [sessionId]: messages }
      return
    }

    if (kind === 'message.delta') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      const id = String(data.id || `${turnId}:agent`)
      const delta = String(data.delta || '')
      if (!delta) return
      const role = String(data.role || 'assistant')
      const sequence = Number(data.sequence || 0)
      const sequenceKey = `${turnId}:${id}`
      if (sequence > 0) {
        const previousSequence = streamSequenceByItem.get(sequenceKey) || 0
        if (sequence <= previousSequence) return
        streamSequenceByItem.set(sequenceKey, sequence)
      }
      if (role !== 'reasoning' && turnId) streamedAssistantTurns.add(turnId)
      const snapshot = String(data.text || '')
      const mode = sequence > 0 && snapshot ? 'replace' : String(data.mode || 'append')
      const nextChunk = mode === 'replace' && snapshot ? snapshot : delta
      queueStreamPatch({
        sessionId,
        turnId,
        id,
        role,
        text: nextChunk,
        mode: mode === 'replace' ? 'replace' : 'append',
      })
      return
    }

    if (kind === 'turn.completed') {
      if (turnId && finalizedTurnIds.has(turnId)) return
      flushStreamPatches()
      const activeTurn = activeTurnBySession.value[sessionId]
      if (turnId && activeTurn?.turnId && activeTurn.turnId !== turnId) return
      rememberFinalizedClaudeTurn(turnId)
      streamedAssistantTurns.delete(turnId)
      for (const key of [...streamSequenceByItem.keys()]) {
        if (key.startsWith(`${turnId}:`)) streamSequenceByItem.delete(key)
      }
      markRunning(sessionId, false)
      markInterrupting(sessionId, false)
      const message = data.message as ClaudeMessage | undefined
      if (message) {
        const list = [...(itemsBySession.value[sessionId] || [])]
        const id = message.id || `${turnId}:agent`
        let index = list.findIndex((item) => item.id === id)
        if (index < 0 && turnId) {
          index = list.findIndex((item) => item.turnId === turnId && item.type === 'agentMessage')
        }
        const finalText = message.text || ''
        if (index >= 0) {
          const current = list[index]
          list[index] = {
            ...current,
            id,
            turnId: turnId || current.turnId,
            text: finalText || current.text,
            status: message.status || 'completed',
            type: 'agentMessage',
          }
        } else if (finalText) {
          list.push(messageToItem({ ...message, id, text: finalText }, turnId || sessionId))
        }
        itemsBySession.value = { ...itemsBySession.value, [sessionId]: list }
      }
      if (Array.isArray(data.activity)) {
        liveActivityBySession.value = {
          ...liveActivityBySession.value,
          [sessionId]: data.activity as ClaudeMessage[],
        }
      }
      materializeLiveActivity(sessionId, turnId)
      const nextActivity = { ...liveActivityBySession.value }
      delete nextActivity[sessionId]
      liveActivityBySession.value = nextActivity
      applyTurnMetrics(sessionId, turnId, data as Record<string, unknown>)
      const nextTurns = { ...activeTurnBySession.value }
      const currentTurn = nextTurns[sessionId]
      // Do not let a stale terminal event clear a newer turn that has already
      // started under the same session (possible while pending ids are promoted).
      if (!turnId || !currentTurn?.turnId || currentTurn.turnId === turnId) {
        delete nextTurns[sessionId]
        activeTurnBySession.value = nextTurns
      }
      if (data.error) {
        notify('error', translate('chat.turnFailed'), String(data.error))
      }
      void appStore.loadLocalUsage().catch(() => undefined)
      void (async () => {
        try {
          if (sessionId && !sessionId.startsWith('pending-claude-')) {
            await openSession(sessionId, {
              switchWorkspace: false,
              terminalStatus: String(data.status || (data.error ? 'failed' : 'completed')),
              activate: false,
            })
          } else {
            await loadSessions()
          }
        } catch {
          // Keep the already-materialized timeline if the native reload lags.
        }
        await flushQueue(sessionId)
      })()
    }
  }

  function parseUsageBreakdown(data: Record<string, unknown>): TokenUsageBreakdown | null {
    const nested = (data.tokenUsage ?? data.usage ?? data.token_usage) as unknown
    const usage = normalizeThreadTokenUsage(nested)
    if (usage?.last && (usage.last.totalTokens > 0 || usage.last.inputTokens > 0 || usage.last.outputTokens > 0)) {
      return usage.last
    }
    if (usage?.total && (usage.total.totalTokens > 0 || usage.total.inputTokens > 0 || usage.total.outputTokens > 0)) {
      return usage.total
    }
    // Direct breakdown without last/total wrapper.
    const direct = normalizeThreadTokenUsage({ last: nested, total: nested })
    if (direct?.last && (direct.last.totalTokens > 0 || direct.last.inputTokens > 0 || direct.last.outputTokens > 0)) {
      return direct.last
    }
    return null
  }

  function applyTurnMetrics(sessionId: string, turnId: string, data: Record<string, unknown>): void {
    if (!turnId) return
    const tokenUsage = parseUsageBreakdown(data)
    const started = turnStartedAtById.value[turnId] ?? null
    const completedAt = Date.now()
    const durationMs = started != null ? Math.max(0, completedAt - started) : null
    const current = activeTurnMetrics.value[turnId] ?? emptyTurnMetrics()
    const next: TurnMetrics = {
      tokenUsage: tokenUsage ?? current.tokenUsage,
      startedAt: started ?? current.startedAt,
      completedAt,
      durationMs: durationMs ?? current.durationMs,
    }
    activeTurnMetrics.value = {
      ...activeTurnMetrics.value,
      [turnId]: next,
    }
    if (tokenUsage && sessionId) {
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
          last: tokenUsage,
          total: nextTotal,
          modelContextWindow: previous?.modelContextWindow ?? 0,
        },
      }
    }
    if (turnStartedAtById.value[turnId] !== undefined) {
      const nextStarts = { ...turnStartedAtById.value }
      delete nextStarts[turnId]
      turnStartedAtById.value = nextStarts
    }
  }

  function appendMessage(sessionId: string, message: ClaudeMessage, turnId: string): void {
    const list = [...(itemsBySession.value[sessionId] || [])]
    if (list.some((item) => item.id === message.id)) return
    list.push(messageToItem(message, turnId || sessionId))
    itemsBySession.value = { ...itemsBySession.value, [sessionId]: list }
  }

  function materializeLiveActivity(sessionId: string, turnId: string): void {
    if (!sessionId || !turnId) return
    const messages = liveActivityBySession.value[sessionId] || []
    if (!messages.length) return
    const list = itemsBySession.value[sessionId] || []
    itemsBySession.value = {
      ...itemsBySession.value,
      [sessionId]: mergeClaudeLiveActivity(list, messages, turnId),
    }
  }

  function markRunning(sessionId: string, running: boolean): void {
    const set = new Set(runningSessionIds.value)
    if (running) set.add(sessionId)
    else set.delete(sessionId)
    runningSessionIds.value = [...set]
  }

  function markSending(sessionId: string, active: boolean): void {
    const next = sendingSessionIds.value.filter((id) => !sameClaudeSession(id, sessionId))
    if (active) next.push(sessionId)
    sendingSessionIds.value = next
  }

  function markInterrupting(sessionId: string, active: boolean): void {
    const next = interruptingSessionIds.value.filter((id) => !sameClaudeSession(id, sessionId))
    if (active) next.push(sessionId)
    interruptingSessionIds.value = next
  }

  function sameClaudeSession(left: string, right: string): boolean {
    if (!left || !right) return false
    if (left === right) return true
    return sessionAlias.get(left) === right || sessionAlias.get(right) === left
  }

  async function enterRuntime(refreshSessions = true): Promise<void> {
    // History is local and should not wait for a slow CLI version/auth probe.
    void refreshRuntime()
    if (refreshSessions) await loadSessions()
    // Warm Claude token totals (may backfill from ~/.claude/projects).
    void appStore.loadLocalUsage().catch(() => undefined)
  }

  async function refreshRuntime(): Promise<void> {
    try {
      runtime.value = await refreshClaudeRuntime()
      // Keep agentProviders badge in sync.
      const list = [...(appStore.agentProviders ?? [])]
      const index = list.findIndex((item) => item.kind === 'claude')
      if (index >= 0) {
        list[index] = {
          ...list[index],
          installed: runtime.value.available,
          healthy: runtime.value.available,
          runtimeReady: runtime.value.available,
          version: runtime.value.version,
          executable: runtime.value.executable,
          message: runtime.value.message,
          status: runtime.value.available ? 'ready' : 'not-installed',
        }
        appStore.agentProviders = list
      }
    } catch (error) {
      runtime.value = { ...emptyRuntime(), message: errorMessage(error) }
    }
  }

  async function loadSessions(): Promise<void> {
    const sequence = ++sessionLoadSequence
    const requestedWorkspace = workspacePath.value
    const requestedSearch = search.value
    try {
      const list = await listClaudeSessions(requestedWorkspace, requestedSearch)
      if (
        sequence !== sessionLoadSequence
        || !sameWorkspacePath(requestedWorkspace, workspacePath.value)
        || requestedSearch !== search.value
      ) return
      const pending = sessions.value.filter((item) =>
        item.id.startsWith('pending-claude-')
        && (
          sameClaudeSession(activeSessionId.value, item.id)
          || isSessionBusy(item.id)
          || (queueBySession.value[item.id] || []).length > 0
        ),
      )
      const pendingIds = new Set(pending.map((item) => item.id))
      sessions.value = [
        ...pending,
        ...(list || []).filter((item) => !pendingIds.has(item.id)),
      ]
    } catch (error) {
      if (
        sequence !== sessionLoadSequence
        || !sameWorkspacePath(requestedWorkspace, workspacePath.value)
        || requestedSearch !== search.value
      ) return
      notify('error', translate('sidebar.claudeEmpty'), errorMessage(error))
    }
  }

  async function loadArchivedSessions(): Promise<void> {
    try {
      archivedSessions.value = (await listArchivedClaudeSessions()) || []
    } catch {
      archivedSessions.value = []
    }
  }

  async function openSession(
    sessionId: string,
    options?: { switchWorkspace?: boolean; terminalStatus?: string; activate?: boolean },
  ): Promise<void> {
    const requestedId = sessionId.trim()
    if (!requestedId) return
    const activate = options?.activate !== false
    const previousSessionId = activeSessionId.value
    if (activate) activeSessionId.value = requestedId
    rememberLoadedClaudeSession(requestedId)
    const known = sessions.value.find((item) => sameClaudeSession(item.id, requestedId))
    const sequence = (sessionOpenSequence.get(requestedId) || 0) + 1
    sessionOpenSequence.set(requestedId, sequence)
    loadingSequenceBySession.set(requestedId, sequence)
    if (activate) loadingSessionId.value = requestedId
    try {
      // If the session belongs to another project, switch Claude workspace first
      // so the active group highlights like Codex / Grok.
      const targetWorkspace = known?.workspace || ''
      if (
        activate && options?.switchWorkspace !== false
        && targetWorkspace
        && looksLikeFilesystemPath(targetWorkspace)
        && (
          workspaceStore.switchingWorkspace
          || !sameWorkspacePath(targetWorkspace, workspacePath.value)
        )
      ) {
        const switched = await workspaceStore.useWorkspace(targetWorkspace)
        if (sessionOpenSequence.get(requestedId) !== sequence) return
        if (!switched && !sameWorkspacePath(targetWorkspace, workspacePath.value)) {
          if (
            sameClaudeSession(activeSessionId.value, requestedId)
            && !(queueBySession.value[requestedId] || []).length
          ) activeSessionId.value = previousSessionId
          return
        }
      }

      // A local draft has no native transcript until its first send binds a real
      // Claude session id. Keep its empty/optimistic timeline without a false error.
      if (requestedId.startsWith('pending-claude-')) return

      const cachedHistory = historyBySession.value[requestedId]
      const hasCachedTimeline = Object.prototype.hasOwnProperty.call(itemsBySession.value, requestedId)
      const cacheIsCurrent = !known
        || !known.updatedAt
        || known.updatedAt <= (cachedHistory?.loadedUpdatedAt ?? 0)
      if (
        !options?.terminalStatus
        && cachedHistory
        && hasCachedTimeline
        && (cacheIsCurrent || isSessionTurnBusy(requestedId))
      ) return

      const detail = await readClaudeSession(requestedId)
      if (sessionOpenSequence.get(requestedId) !== sequence) return
      const messages = detail.messages || []
      const fromDisk = buildTimelineFromMessages(requestedId, messages, detail.historyTurnOffset || 0)

      // Re-read live state after the await. A new turn can start while the native
      // transcript is loading; using the pre-request snapshot would overwrite its
      // optimistic user row with stale disk history.
      const cached = itemsBySession.value[requestedId] || []
      const liveTurn = activeTurnBySession.value[requestedId]
      const isLive = runningSessionIds.value.some((id) => sameClaudeSession(id, requestedId)) || Boolean(liveTurn)
      const currentHistory = historyBySession.value[requestedId]
      const keepLoadedPrefix = Boolean(
        currentHistory
        && currentHistory.start < (Number(detail.historyStart) || 0)
        && (Number(detail.historyTotal) || 0) >= currentHistory.total,
      )
      const split = keepLoadedPrefix
        ? splitClaudeHistoryPrefix(fromDisk, cached)
        : { prefix: [] as TimelineItem[], current: cached }

      let nextItems: TimelineItem[]
      if (isLive && split.current.length > 0) {
        // Running turn: memory is authoritative for the live bubble; merge disk
        // history underneath so older completed turns are not lost.
        nextItems = mergeDiskWithLiveTimeline(fromDisk, split.current, liveTurn?.turnId || '')
      } else {
        // Completed history is disk-authoritative. Keep only cache rows that
        // are genuinely newer/active so an old local snapshot cannot replace
        // or reorder the native transcript.
        nextItems = mergeDiskWithCachedTimeline(fromDisk, split.current)
      }
      if (split.prefix.length) nextItems = [...split.prefix, ...nextItems]
      if (!isLive) {
        nextItems = finalizeTimelineItemStatuses(
          nextItems,
          options?.terminalStatus || 'completed',
        )
      }
      itemsBySession.value = { ...itemsBySession.value, [requestedId]: nextItems }
      setClaudeHistoryState(requestedId, detail)
      if (split.prefix.length && currentHistory) {
        patchClaudeHistoryState(requestedId, {
          start: currentHistory.start,
          turnOffset: currentHistory.turnOffset,
          hasEarlier: currentHistory.hasEarlier,
        })
      }
      // Ensure summary is present / refreshed at top of list.
      if (detail.summary?.id) {
        const others = sessions.value.filter((item) => item.id !== detail.summary.id)
        sessions.value = [detail.summary, ...others]
      }
      void hydrateSessionTokenUsage(requestedId)
    } catch (error) {
      if (sessionOpenSequence.get(requestedId) !== sequence) return
      if (!activate || !sameClaudeSession(activeSessionId.value, requestedId)) return
      notify('error', translate('sidebar.claudeEmpty'), errorMessage(error))
    } finally {
      if (loadingSequenceBySession.get(requestedId) === sequence) {
        loadingSequenceBySession.delete(requestedId)
        if (!isSessionLoading(requestedId) && sameClaudeSession(loadingSessionId.value, requestedId)) {
          loadingSessionId.value = ''
        }
        // Read failures must not strand a message admitted while the selection
        // was loading. dispatchTurn still owns the fixed session/workspace target.
        void flushQueue(requestedId)
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
    ])].filter((id) => sameClaudeSession(id, sessionId))
    for (const id of related) {
      sessionOpenSequence.set(id, (sessionOpenSequence.get(id) || 0) + 1)
      loadingSequenceBySession.delete(id)
    }
    if (sameClaudeSession(loadingSessionId.value, sessionId)) loadingSessionId.value = ''
    await openSession(sessionId)
  }

  async function hydrateSessionTokenUsage(sessionId: string): Promise<void> {
    if (!sessionId || sessionId.startsWith('pending-claude-')) return
    try {
      const list = await listClaudeSessionTurnUsages(sessionId)
      if (!list?.length) return
      let totalUsage: TokenUsageBreakdown | null = null
      let lastUsage: TokenUsageBreakdown | null = null
      for (const item of list) {
        const usage = item.tokenUsage
        if (!usage) continue
        const total = Number(usage.totalTokens)
          || (Number(usage.inputTokens) + Number(usage.cachedInputTokens)
            + Number(usage.outputTokens) + Number(usage.reasoningOutputTokens))
        if (total <= 0) continue
        const breakdown: TokenUsageBreakdown = {
          inputTokens: Number(usage.inputTokens) || 0,
          cachedInputTokens: Number(usage.cachedInputTokens) || 0,
          outputTokens: Number(usage.outputTokens) || 0,
          reasoningOutputTokens: Number(usage.reasoningOutputTokens) || 0,
          totalTokens: total,
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
      if (lastUsage && totalUsage) {
        tokenUsageBySession.value = {
          ...tokenUsageBySession.value,
          [sessionId]: {
            last: lastUsage,
            total: totalUsage,
            modelContextWindow: tokenUsageBySession.value[sessionId]?.modelContextWindow ?? null,
          },
        }
      }
    } catch {
      // Older backends may not expose the optional history usage method yet.
    }
  }

  function buildTimelineFromMessages(
    sessionId: string,
    messages: ClaudeMessage[],
    turnOffset = 0,
  ): TimelineItem[] {
    const items: TimelineItem[] = []
    let turnSeed = turnOffset
    for (const message of messages) {
      const role = (message.role || '').toLowerCase()
      if (role === 'user' || role === 'human') turnSeed += 1
      // Prefer real turn ids embedded in message ids: "claude-turn-xxx:agent"
      let turnId = `${sessionId}:turn-${turnSeed || 1}`
      const id = message.id || ''
      const colon = id.lastIndexOf(':')
      if (id.startsWith('claude-turn-') && colon > 0) {
        turnId = id.slice(0, colon)
      }
      items.push(messageToItem(message, turnId))
    }
    return items
  }

  function setClaudeHistoryState(sessionId: string, detail: ClaudeSessionDetail): void {
    const start = Math.max(0, Number(detail.historyStart) || 0)
    const turnOffset = Math.max(0, Number(detail.historyTurnOffset) || 0)
    historyBySession.value = {
      ...historyBySession.value,
      [sessionId]: {
        start,
        total: Math.max(start, Number(detail.historyTotal) || 0),
        turnOffset,
        hasEarlier: detail.hasEarlier === true || start > 0,
        loadingEarlier: false,
        loadedUpdatedAt: Number(detail.summary?.updatedAt) || historyBySession.value[sessionId]?.loadedUpdatedAt || 0,
      },
    }
  }

  function patchClaudeHistoryState(sessionId: string, patch: Partial<ClaudeHistoryState>): void {
    const current = historyBySession.value[sessionId]
    if (!current) return
    historyBySession.value = {
      ...historyBySession.value,
      [sessionId]: { ...current, ...patch },
    }
  }

  async function loadEarlierHistory(): Promise<boolean> {
    const sessionId = activeSessionId.value
    const state = historyBySession.value[sessionId]
    if (!sessionId || !state?.hasEarlier || state.loadingEarlier) return false
    const before = state.start
    patchClaudeHistoryState(sessionId, { loadingEarlier: true })
    try {
      const detail = await readClaudeSessionHistory(sessionId, before)
      const currentState = historyBySession.value[sessionId]
      if (!currentState || currentState.start !== before) return false
      const earlier = buildTimelineFromMessages(
        sessionId,
        detail.messages || [],
        detail.historyTurnOffset || 0,
      )
      const current = itemsBySession.value[sessionId] || []
      const existingIDs = new Set(current.map((item) => item.id).filter(Boolean))
      const prefix = earlier.filter((item) => !item.id || !existingIDs.has(item.id))
      if (prefix.length) {
        itemsBySession.value = {
          ...itemsBySession.value,
          [sessionId]: [...prefix, ...current],
        }
      }
      setClaudeHistoryState(sessionId, detail)
      rememberLoadedClaudeSession(sessionId)
      return prefix.length > 0 || (Number(detail.historyStart) || 0) < before
    } catch (error) {
      if (sameClaudeSession(activeSessionId.value, sessionId)) {
        notify('error', translate('notifications.taskOpenFailed'), errorMessage(error))
      }
      return false
    } finally {
      if (historyBySession.value[sessionId]?.loadingEarlier) {
        patchClaudeHistoryState(sessionId, { loadingEarlier: false })
      }
    }
  }

  /**
   * Disk = completed history; live = in-memory stream. Prefer live rows for the
   * active turn and any in-progress items; keep disk order for older turns.
   */
  function splitClaudeHistoryPrefix(
    disk: TimelineItem[],
    current: TimelineItem[],
  ): { prefix: TimelineItem[], current: TimelineItem[] } {
    if (!disk.length || !current.length) return { prefix: [], current }
    const currentByID = new Map<string, number>()
    current.forEach((item, index) => {
      if (item.id) currentByID.set(item.id, index)
    })
    let diskIndex = -1
    let currentIndex = -1
    for (let index = 0; index < disk.length; index += 1) {
      const item = disk[index]
      const byID = item.id ? currentByID.get(item.id) : undefined
      const match = byID ?? (item.type === 'userMessage'
        ? current.findIndex((row) => sameClaudeUserRow(row, item))
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

  function mergeDiskWithLiveTimeline(
    disk: TimelineItem[],
    live: TimelineItem[],
    liveTurnId: string,
  ): TimelineItem[] {
    if (!live.length) return disk
    if (!disk.length) return live

    const liveById = new Map(live.map((item) => [item.id, item]))
    const used = new Set<string>()
    const out: TimelineItem[] = []

    for (const item of disk) {
      const liveHit = liveById.get(item.id)
      if (liveHit) {
        // Prefer longer / in-progress live text for the same id.
        const liveLen = (liveHit.text || '').length
        const diskLen = (item.text || '').length
        const preferLive = isActiveItemStatus(liveHit.status) || liveLen >= diskLen
        out.push(preferLive ? liveHit : item)
        used.add(item.id)
        continue
      }
      // Disk row without live counterpart — keep unless it is a short draft of the live turn.
      if (liveTurnId && item.turnId === liveTurnId && isActiveItemStatus(item.status)) {
        continue
      }
      out.push(item)
    }

    // Append live-only rows (current stream bubble, optimistic user, reasoning).
    for (const item of live) {
      if (used.has(item.id)) continue
      if (out.some((row) => row.id === item.id)) continue
      // Dedupe the optimistic row for this turn without collapsing a valid
      // repeated prompt from a newer turn.
      if (item.type === 'userMessage') {
        if (out.some((row) => sameClaudeUserRow(row, item))) {
          continue
        }
      }
      out.push(item)
    }
    return out
  }

  /**
   * Merge a non-live reload without allowing stale cache rows to shadow disk.
   * Claude may flush the last native JSONL line slightly after turn.completed,
   * so retain active or newer cache-only rows as a short tail.
   */
  function mergeDiskWithCachedTimeline(
    disk: TimelineItem[],
    cached: TimelineItem[],
  ): TimelineItem[] {
    if (!disk.length) return cached
    if (!cached.length) return disk

    const diskIds = new Set(disk.map((item) => item.id))
    const latestDiskTime = disk.reduce((latest, item) => {
      const time = item.completedAt || item.startedAt || 0
      return Math.max(latest, typeof time === 'number' ? time : 0)
    }, 0)
    const out = [...disk]

    for (const item of cached) {
      if (item.id && diskIds.has(item.id)) continue
      if (item.type === 'userMessage') {
        if (out.some((row) => sameClaudeUserRow(row, item))) {
          continue
        }
      }
      const active = isActiveItemStatus(item.status)
      const sameTurnOnDisk = Boolean(item.turnId && disk.some((row) => row.turnId === item.turnId))
      const itemTime = item.completedAt || item.startedAt || 0
      if (active || (!sameTurnOnDisk && typeof itemTime === 'number' && itemTime >= latestDiskTime)) {
        out.push(item)
      }
    }
    return out
  }

  function isActiveItemStatus(status: string): boolean {
    const s = (status || '').toLowerCase().replace(/[_-]/g, '')
    return s === 'inprogress' || s === 'running' || s === 'started' || s === 'pending' || s === 'active'
  }

  function finalizeTimelineItemStatuses(items: TimelineItem[], status: string): TimelineItem[] {
    let terminalTurnId = ''
    for (let index = items.length - 1; index >= 0; index -= 1) {
      const item = items[index]
      if (item?.type === 'userMessage' && item.turnId) {
        terminalTurnId = item.turnId
        break
      }
    }
    if (!terminalTurnId) terminalTurnId = items.at(-1)?.turnId || ''
    let changed = false
    const next = items.map((item) => {
      if (!isActiveItemStatus(item.status)) return item
      changed = true
      return {
        ...item,
        status: terminalTurnId && item.turnId !== terminalTurnId ? 'completed' : status,
        completedAt: item.completedAt || Date.now(),
      }
    })
    return changed ? next : items
  }

  function keepPendingSession(sessionId: string): boolean {
    return isSessionBusy(sessionId)
      || (queueBySession.value[sessionId] || []).length > 0
      || (itemsBySession.value[sessionId] || []).length > 0
  }

  function newSession(): void {
    if (workspaceStore.switchingWorkspace) return
    if (!workspacePath.value) {
      notify('error', translate('sidebar.claudeEmpty'), translate('app.needWorkspaceHintReady'))
      return
    }
    const id = `pending-claude-${Date.now()}-${++queuedSequence}`
    const now = Math.floor(Date.now() / 1000)
    const summary: ClaudeSessionSummary = {
      id,
      workspace: workspacePath.value,
      name: translate('sidebar.newTask'),
      preview: '',
      model: appStore.settings.claudeModel || 'sonnet',
      effort: appStore.settings.claudeEffort || 'high',
      createdAt: now,
      updatedAt: now,
    }
    sessions.value = [summary, ...sessions.value.filter((item) =>
      !item.id.startsWith('pending-claude-') || keepPendingSession(item.id),
    )]
    activeSessionId.value = id
    itemsBySession.value = { ...itemsBySession.value, [id]: [] }
    rememberLoadedClaudeSession(id)
  }

  function ensureActiveSessionId(): string {
    let sessionId = activeSessionId.value
    if (sessionId) return sessionId
    sessionId = `pending-claude-${Date.now()}-${++queuedSequence}`
    activeSessionId.value = sessionId
    const now = Math.floor(Date.now() / 1000)
    sessions.value = [{
      id: sessionId,
      workspace: workspacePath.value,
      name: translate('sidebar.newTask'),
      preview: '',
      model: appStore.settings.claudeModel || 'sonnet',
      effort: appStore.settings.claudeEffort || 'high',
      createdAt: now,
      updatedAt: now,
    }, ...sessions.value.filter((item) =>
      !item.id.startsWith('pending-claude-') || keepPendingSession(item.id),
    )]
    itemsBySession.value = { ...itemsBySession.value, [sessionId]: [] }
    rememberLoadedClaudeSession(sessionId)
    return sessionId
  }

  function isSessionLoading(sessionId: string): boolean {
    const requested = sessionId.trim()
    return Array.from(loadingSequenceBySession.keys()).some((id) => sameClaudeSession(id, requested))
  }

  function isSessionTurnBusy(sessionId: string): boolean {
    const requested = sessionId.trim()
    if (sendingSessionIds.value.some((id) => sameClaudeSession(id, requested))) return true
    if (runningSessionIds.value.some((id) => sameClaudeSession(id, requested))) return true
    if (Object.keys(activeTurnBySession.value).some((id) => sameClaudeSession(id, requested))) return true
    return false
  }

  function isSessionBusy(sessionId: string): boolean {
    return isSessionLoading(sessionId) || isSessionTurnBusy(sessionId)
  }

  function rememberLoadedClaudeSession(sessionId: string): void {
    const id = sessionAlias.get(sessionId) || sessionId.trim()
    if (!id) return
    for (const cached of [...loadedSessionIds]) {
      if (sameClaudeSession(cached, id)) loadedSessionIds.delete(cached)
    }
    loadedSessionIds.add(id)
    while (loadedSessionIds.size > 12 || cachedClaudeConversationWeight() > 32_000_000) {
      const evicted = [...loadedSessionIds].find((candidate) =>
        !sameClaudeSession(candidate, id)
        && !sameClaudeSession(candidate, activeSessionId.value)
        && !isSessionBusy(candidate)
        && !Object.entries(queueBySession.value).some(([key, queue]) =>
          sameClaudeSession(key, candidate) && queue.length > 0,
        ),
      )
      if (!evicted) break
      evictCachedClaudeSession(evicted)
    }
  }

  function cachedClaudeConversationWeight(): number {
    let total = 0
    for (const items of Object.values(itemsBySession.value)) {
      for (const item of items) {
        total += item.text.length + item.output.length + item.detail.length + item.command.length
        total += (item.reasoningSummary?.length ?? 0) + (item.reasoningContent?.length ?? 0)
        total += item.changes.reduce((sum, change) => sum + change.path.length + change.diff.length, 0)
        total += (item.attachments ?? []).reduce((sum, attachment) => sum + attachment.source.length + attachment.name.length, 0)
      }
    }
    return total
  }

  function evictCachedClaudeSession(sessionId: string): void {
    const related = new Set<string>([
      sessionId,
      ...Object.keys(itemsBySession.value).filter((id) => sameClaudeSession(id, sessionId)),
      ...Object.keys(historyBySession.value).filter((id) => sameClaudeSession(id, sessionId)),
    ])
    const turnIds = new Set<string>()
    for (const id of related) {
      for (const item of itemsBySession.value[id] || []) {
        if (item.turnId) turnIds.add(item.turnId)
      }
    }
    const withoutRelated = <T>(bucket: Record<string, T>): Record<string, T> =>
      Object.fromEntries(Object.entries(bucket).filter(([id]) =>
        ![...related].some((key) => sameClaudeSession(id, key)),
      ))
    itemsBySession.value = withoutRelated(itemsBySession.value)
    historyBySession.value = withoutRelated(historyBySession.value)
    tokenUsageBySession.value = withoutRelated(tokenUsageBySession.value)
    liveActivityBySession.value = withoutRelated(liveActivityBySession.value)

    const nextMetrics = { ...activeTurnMetrics.value }
    const nextStarts = { ...turnStartedAtById.value }
    for (const turnId of turnIds) {
      delete nextMetrics[turnId]
      delete nextStarts[turnId]
      finalizedTurnIds.delete(turnId)
      streamedAssistantTurns.delete(turnId)
      for (const key of [...streamSequenceByItem.keys()]) {
        if (key.startsWith(`${turnId}:`)) streamSequenceByItem.delete(key)
      }
    }
    activeTurnMetrics.value = nextMetrics
    turnStartedAtById.value = nextStarts

    for (const id of [...loadedSessionIds]) {
      if ([...related].some((key) => sameClaudeSession(id, key))) loadedSessionIds.delete(id)
    }
    for (const id of [...sessionOpenSequence.keys()]) {
      if ([...related].some((key) => sameClaudeSession(id, key))) sessionOpenSequence.delete(id)
    }
    for (const id of [...latestStartedTurnBySession.keys()]) {
      if ([...related].some((key) => sameClaudeSession(id, key))) latestStartedTurnBySession.delete(id)
    }
    for (const [alias, target] of [...sessionAlias.entries()]) {
      if (related.has(alias) || related.has(target)) sessionAlias.delete(alias)
    }
  }

  /**
   * Public send: idle → dispatch CLI turn; busy → enqueue follow-up (never double-fire CLI).
   */
  async function sendMessage(text: string, images: string[] = []): Promise<boolean> {
    const content = text.trim()
    if (!content && images.length === 0) return false
    if (!workspacePath.value) {
      notify('error', translate('sidebar.claudeEmpty'), translate('app.needWorkspaceHintReady'))
      return false
    }
    if (!isReady.value) {
      notify('error', translate('sidebar.claudeEmpty'), runtime.value.message || translate('sidebar.claudeRuntimeMissing'))
      return false
    }

    const sessionId = ensureActiveSessionId()
    const busy = isSessionBusy(sessionId)
    const blockingTurnId = activeTurnBySession.value[sessionId]?.turnId || ''
    // Always enter the per-session queue first. This keeps a send from
    // overtaking an older follow-up while terminal history is being hydrated.
    const sessionWorkspace = sessions.value.find((item) => sameClaudeSession(item.id, sessionId))?.workspace
      || workspacePath.value
    enqueue(
      sessionId,
      content,
      images,
      sessionWorkspace,
      appStore.settings.claudeModel || 'sonnet',
      appStore.settings.claudeEffort || 'high',
      blockingTurnId,
    )
    if (!busy) await flushQueue(sessionId)
    return true
  }

  /** Start exactly one CLI turn. Caller must ensure the session is not already busy. */
  async function dispatchTurn(
    sessionId: string,
    content: string,
    images: string[],
    options?: { workspace?: string; model?: string; effort?: string },
  ): Promise<boolean> {
    const workspace = options?.workspace || workspacePath.value
    if (!workspace) return false
    markSending(sessionId, true)
    // Mark busy BEFORE await so a second sendMessage cannot also dispatch.
    markRunning(sessionId, true)
    const now = Math.floor(Date.now() / 1000)
    const localSequence = ++queuedSequence
    const localTurnId = `claude-local-${Date.now()}-${localSequence}`
    const userItem: TimelineItem = {
      ...messageToItem({
      id: `${sessionId}:local-user-${Date.now()}-${localSequence}`,
      role: 'user',
      text: content,
      status: 'completed',
      createdAt: now,
      }, localTurnId),
      attachments: images.map((source) => ({ kind: 'local', source, name: attachmentName(source) })),
    }
    itemsBySession.value = {
      ...itemsBySession.value,
      [sessionId]: [...(itemsBySession.value[sessionId] || []), userItem],
    }

    const startedTurnBeforeSend = latestStartedTurnForSession(sessionId)
    let attachedTurn = false
    try {
      const ref = await sendClaudeMessageApi({
        sessionId,
        workspace,
        text: content,
        images,
        model: options?.model ?? appStore.settings.claudeModel ?? '',
        effort: options?.effort || appStore.settings.claudeEffort || 'high',
      })
      attachedTurn = true

      let nextSessionId = ref.sessionId || sessionId
      if (sessionId.startsWith('pending-claude-') && nextSessionId && nextSessionId !== sessionId) {
        // Bind local user row to real turn id before promote (order-safe).
        const list = itemsBySession.value[sessionId] || []
        itemsBySession.value = {
          ...itemsBySession.value,
          [sessionId]: list.map((item) =>
            item.turnId === localTurnId
              ? { ...item, turnId: ref.turnId || item.turnId }
              : item,
          ),
        }
        promotePendingSession(sessionId, nextSessionId)
      } else {
        nextSessionId = sessionId
        const list = itemsBySession.value[sessionId] || []
        itemsBySession.value = {
          ...itemsBySession.value,
          [sessionId]: list.map((item) =>
            item.turnId === localTurnId
              ? { ...item, turnId: ref.turnId || item.turnId }
              : item,
          ),
        }
      }

      if (sameClaudeSession(activeSessionId.value, sessionId)) activeSessionId.value = nextSessionId
      // The CLI can finish before the Wails send Promise resolves. Never let a
      // late response resurrect a turn that its terminal event already closed.
      if (ref.turnId && finalizedTurnIds.has(ref.turnId)) {
        markRunning(nextSessionId, false)
        const currentTurn = activeTurnBySession.value[nextSessionId]
        if (currentTurn?.turnId === ref.turnId) {
          const nextTurns = { ...activeTurnBySession.value }
          delete nextTurns[nextSessionId]
          activeTurnBySession.value = nextTurns
        }
        return true
      }
      markRunning(nextSessionId, true)
      activeTurnBySession.value = {
        ...activeTurnBySession.value,
        [nextSessionId]: ref,
      }
      if (ref.turnId) {
        const startedAt = Date.now()
        turnStartedAtById.value = {
          ...turnStartedAtById.value,
          [ref.turnId]: startedAt,
        }
        activeTurnMetrics.value = {
          ...activeTurnMetrics.value,
          [ref.turnId]: { ...emptyTurnMetrics(), startedAt },
        }
        const list = itemsBySession.value[nextSessionId] || []
        itemsBySession.value = {
          ...itemsBySession.value,
          [nextSessionId]: list.map((item) =>
            item.turnId === localTurnId
              ? { ...item, turnId: ref.turnId }
              : item,
          ),
        }
      }
      void loadSessions()
      return true
    } catch (error) {
      const acceptedRef = turnRefForSession(sessionId)
      const latestStartedTurnId = latestStartedTurnForSession(sessionId)
      const acceptedDuringSend = Boolean(
        latestStartedTurnId && latestStartedTurnId !== startedTurnBeforeSend,
      )
      if (
        acceptedDuringSend
        || (acceptedRef && (!acceptedRef.turnId || !finalizedTurnIds.has(acceptedRef.turnId)))
      ) {
        // The bridge can deliver turn.started before the RPC rejection reaches
        // JavaScript. The event is proof that this prompt was accepted; retrying
        // it would duplicate the turn.
        attachedTurn = true
        return true
      }
      if (!attachedTurn) {
        markRunning(sessionId, false)
        // Drop optimistic user row for this failed attempt.
        const list = itemsBySession.value[sessionId] || []
        itemsBySession.value = {
          ...itemsBySession.value,
          [sessionId]: list.filter((item) => item.turnId !== localTurnId),
        }
      }
      notify('error', translate('notifications.sendFailed'), errorMessage(error))
      return false
    } finally {
      markSending(sessionId, false)
    }
  }

  function remapSessionBusy(fromId: string, toId: string): void {
    if (!fromId || !toId || fromId === toId) return
    if (sendingSessionIds.value.includes(fromId)) {
      markSending(fromId, false)
      markSending(toId, true)
    }
    if (runningSessionIds.value.includes(fromId)) {
      markRunning(fromId, false)
      markRunning(toId, true)
    }
    if (interruptingSessionIds.value.includes(fromId)) {
      markInterrupting(fromId, false)
      markInterrupting(toId, true)
    }
    const turn = activeTurnBySession.value[fromId]
    if (turn) {
      const next = { ...activeTurnBySession.value }
      delete next[fromId]
      next[toId] = { ...turn, sessionId: toId }
      activeTurnBySession.value = next
    }
  }

  /** Merge timelines without duplicating users/agents; preserve caller order. */
  function mergeClaudeTimeline(existing: TimelineItem[], incoming: TimelineItem[]): TimelineItem[] {
    const out = [...existing]
    for (const item of incoming) {
      if (item.type === 'userMessage') {
        const idx = out.findIndex((row) => sameClaudeUserRow(row, item))
        if (idx >= 0) {
          const dup = out[idx]
          // Prefer real backend turn ids over local provisional ones.
          if (item.turnId?.startsWith('claude-turn-') || !dup.turnId?.startsWith('claude-turn-')) {
            out[idx] = {
              ...dup,
              ...item,
              id: item.id || dup.id,
              turnId: item.turnId?.startsWith('claude-turn-') ? item.turnId : (dup.turnId || item.turnId),
            }
          }
          continue
        }
      }
      if (item.id && out.some((row) => row.id === item.id)) {
        const idx = out.findIndex((row) => row.id === item.id)
        if (idx >= 0) {
          const prev = out[idx]
          const text = (item.text || '').length >= (prev.text || '').length ? item.text : prev.text
          out[idx] = { ...prev, ...item, text }
        }
        continue
      }
      // Same-turn agent: merge into existing agent bubble instead of appending.
      if (item.type === 'agentMessage' && item.turnId) {
        const idx = out.findIndex((row) => row.type === 'agentMessage' && row.turnId === item.turnId)
        if (idx >= 0) {
          const prev = out[idx]
          const text = (item.text || '').length >= (prev.text || '').length ? item.text : prev.text
          out[idx] = { ...prev, ...item, id: prev.id || item.id, text }
          continue
        }
      }
      out.push(item)
    }
    return out
  }

  function enqueue(
    sessionId: string,
    text: string,
    images: string[],
    workspace: string,
    model: string,
    effort: string,
    blockedByTurnId = '',
  ): void {
    const item: ClaudeQueuedMessage = {
      id: `claude-q-${Date.now()}-${++queuedSequence}`,
      sessionId,
      workspace,
      model,
      effort,
      text,
      images,
      state: 'queued',
      error: '',
      createdAt: Date.now(),
      blockedByTurnId: blockedByTurnId || undefined,
    }
    const list = [...(queueBySession.value[sessionId] || []), item]
    queueBySession.value = { ...queueBySession.value, [sessionId]: list }
  }

  async function flushQueue(sessionId: string): Promise<void> {
    const list = queueBySession.value[sessionId] || []
    const next = list[0]
    // A failed head stays in place until retry/remove/send-now. Skipping it would
    // silently send later prompts out of order.
    if (!next || next.state !== 'queued') return
    await dispatchQueuedMessage(next.id, false)
  }

  function findQueuedMessage(messageId: string): {
    sessionId: string
    list: ClaudeQueuedMessage[]
    item: ClaudeQueuedMessage
  } | null {
    // Find the queue row across buckets (pending id may have remapped).
    let sessionId = activeSessionId.value
    let list = queueBySession.value[sessionId] || []
    let item = list.find((row) => row.id === messageId)
    if (!item) {
      for (const [sid, rows] of Object.entries(queueBySession.value)) {
        const found = rows.find((row) => row.id === messageId)
        if (found) {
          sessionId = sid
          list = rows
          item = found
          break
        }
      }
    }
    return item && sessionId ? { sessionId, list, item } : null
  }

  function moveQueuedMessage(messageId: string, action: 'up' | 'down' | 'top'): void {
    const found = findQueuedMessage(messageId)
    if (!found || found.item.state === 'sending') return
    const list = [...found.list]
    const index = list.findIndex((row) => row.id === messageId)
    if (index < 0) return
    let floor = 0
    while (floor < list.length && list[floor]?.state === 'sending') floor += 1
    const target = action === 'top'
      ? floor
      : action === 'up'
        ? Math.max(floor, index - 1)
        : Math.min(list.length - 1, index + 1)
    if (target === index) return
    const [item] = list.splice(index, 1)
    if (!item) return
    list.splice(target, 0, item)
    queueBySession.value = { ...queueBySession.value, [found.sessionId]: list }
  }

  function turnRefForSession(sessionId: string): ClaudeTurnRef | null {
    for (const [id, ref] of Object.entries(activeTurnBySession.value)) {
      if (ref && sameClaudeSession(id, sessionId)) return ref
    }
    return null
  }

  function latestStartedTurnForSession(sessionId: string): string {
    let latest = ''
    for (const [id, turnId] of latestStartedTurnBySession) {
      if (sameClaudeSession(id, sessionId)) latest = turnId
    }
    return latest
  }

  async function recoverMissingClaudeTurn(ref: ClaudeTurnRef, error: unknown): Promise<boolean> {
    if (!/not running/i.test(errorMessage(error))) return false
    const current = turnRefForSession(ref.sessionId)
    if (current?.turnId && ref.turnId && current.turnId !== ref.turnId) return false

    rememberFinalizedClaudeTurn(ref.turnId)
    flushStreamPatches()
    runningSessionIds.value = runningSessionIds.value.filter((id) => !sameClaudeSession(id, ref.sessionId))
    markSending(ref.sessionId, false)
    markInterrupting(ref.sessionId, false)

    const nextTurns = { ...activeTurnBySession.value }
    for (const [id, turn] of Object.entries(nextTurns)) {
      if (!sameClaudeSession(id, ref.sessionId)) continue
      if (ref.turnId && turn?.turnId && turn.turnId !== ref.turnId) continue
      delete nextTurns[id]
    }
    activeTurnBySession.value = nextTurns

    const nextActivity = { ...liveActivityBySession.value }
    for (const id of Object.keys(nextActivity)) {
      if (sameClaudeSession(id, ref.sessionId)) delete nextActivity[id]
    }
    liveActivityBySession.value = nextActivity

    const nextItems = { ...itemsBySession.value }
    for (const [id, items] of Object.entries(nextItems)) {
      if (sameClaudeSession(id, ref.sessionId)) {
        nextItems[id] = finalizeTimelineItemStatuses(items, 'interrupted')
      }
    }
    itemsBySession.value = nextItems

    const canonicalId = sessionAlias.get(ref.sessionId) || ref.sessionId
    if (canonicalId && !canonicalId.startsWith('pending-claude-')) {
      await openSession(canonicalId, {
        switchWorkspace: false,
        terminalStatus: 'interrupted',
        activate: false,
      }).catch(() => undefined)
    }
    const queueId = Object.keys(queueBySession.value).find((id) => sameClaudeSession(id, canonicalId)) || canonicalId
    await flushQueue(queueId)
    return true
  }

  async function dispatchQueuedMessage(messageId: string, forceNow: boolean): Promise<void> {
    let found = findQueuedMessage(messageId)
    if (!found) return
    if (found.item.state === 'sending') {
      if (isSessionTurnBusy(found.sessionId)) return
      queueBySession.value = {
        ...queueBySession.value,
        [found.sessionId]: found.list.map((row) =>
          row.id === messageId ? { ...row, state: 'queued', error: '' } : row,
        ),
      }
    } else if (found.item.state === 'failed') {
      queueBySession.value = {
        ...queueBySession.value,
        [found.sessionId]: found.list.map((row) =>
          row.id === messageId ? { ...row, state: 'queued', error: '' } : row,
        ),
      }
    }
    if (forceNow) moveQueuedMessage(messageId, 'top')
    found = findQueuedMessage(messageId)
    if (!found) return

    if (isSessionLoading(found.sessionId)) return
    const activeRef = turnRefForSession(found.sessionId)
    if (forceNow && activeRef) {
      try {
        await interruptClaudeTurnApi(activeRef)
      } catch (error) {
        if (!await recoverMissingClaudeTurn(activeRef, error)) {
          notify('error', translate('notifications.interruptFailed'), errorMessage(error))
        }
      }
      return
    }
    if (isSessionTurnBusy(found.sessionId)) return
    if (found.item.blockedByTurnId && !finalizedTurnIds.has(found.item.blockedByTurnId)) {
      if (!forceNow) return
      queueBySession.value = {
        ...queueBySession.value,
        [found.sessionId]: found.list.map((row) =>
          row.id === messageId ? { ...row, blockedByTurnId: undefined } : row,
        ),
      }
      found = findQueuedMessage(messageId)
      if (!found) return
    }

    queueBySession.value = {
      ...queueBySession.value,
      [found.sessionId]: found.list.map((row) =>
        row.id === messageId ? { ...row, state: 'sending', error: '' } : row,
      ),
    }
    // Use dispatchTurn — never sendMessage (which would re-enqueue while busy).
    const ok = await dispatchTurn(found.sessionId, found.item.text, found.item.images, {
      workspace: found.item.workspace,
      model: found.item.model,
      effort: found.item.effort,
    })
    const currentEntry = Object.entries(queueBySession.value).find(([, rows]) =>
      rows.some((row) => row.id === messageId),
    )
    if (!currentEntry) return
    const [currentSessionId, currentList] = currentEntry
    const remaining = currentList.filter((row) => row.id !== messageId)
    if (!ok) {
      queueBySession.value = {
        ...queueBySession.value,
        [currentSessionId]: currentList.map((row) => row.id === messageId
          ? { ...row, sessionId: currentSessionId, state: 'failed', error: translate('notifications.sendFailed') }
          : row),
      }
      return
    }
    const nextQueues = { ...queueBySession.value }
    if (remaining.length) nextQueues[currentSessionId] = remaining
    else delete nextQueues[currentSessionId]
    queueBySession.value = nextQueues
    if (!isSessionBusy(currentSessionId)) await flushQueue(currentSessionId)
  }

  async function sendQueuedMessageNow(messageId: string): Promise<void> {
    await dispatchQueuedMessage(messageId, true)
  }

  function reorderQueuedMessage(messageId: string, direction: 'up' | 'down'): void {
    moveQueuedMessage(messageId, direction)
  }

  function retryQueuedMessage(messageId: string): void {
    const found = findQueuedMessage(messageId)
    if (!found || found.item.state !== 'failed') return
    queueBySession.value = {
      ...queueBySession.value,
      [found.sessionId]: found.list.map((row) =>
        row.id === messageId ? { ...row, state: 'queued', error: '' } : row,
      ),
    }
    if (!isSessionBusy(found.sessionId)) void flushQueue(found.sessionId)
  }
  function removeQueuedMessage(messageId: string): void {
    for (const [sessionId, rows] of Object.entries(queueBySession.value)) {
      const message = rows.find((row) => row.id === messageId)
      if (!message) continue
      if (message.state === 'sending') return
      const remaining = rows.filter((row) => row.id !== messageId)
      const next = { ...queueBySession.value }
      if (remaining.length) next[sessionId] = remaining
      else delete next[sessionId]
      queueBySession.value = next
      if (!isSessionBusy(sessionId)) void flushQueue(sessionId)
      return
    }
  }

  async function interruptActiveTurn(): Promise<void> {
    const ref = turnRefForSession(activeSessionId.value)
    if (!ref) return
    if (interruptingSessionIds.value.some((id) => sameClaudeSession(id, ref.sessionId))) return
    markInterrupting(ref.sessionId, true)
    try {
      await interruptClaudeTurnApi(ref)
      window.setTimeout(() => {
        const current = turnRefForSession(ref.sessionId)
        if (!current || current.turnId === ref.turnId) markInterrupting(ref.sessionId, false)
      }, 8000)
    } catch (error) {
      if (!await recoverMissingClaudeTurn(ref, error)) {
        const current = turnRefForSession(ref.sessionId)
        if (!current || current.turnId !== ref.turnId) return
        markInterrupting(ref.sessionId, false)
        notify('error', translate('notifications.interruptFailed'), errorMessage(error))
      }
    }
  }

  function discardClaudeSessionState(sessionId: string): void {
    const candidates = new Set<string>([
      sessionId,
      ...sessionAlias.keys(),
      ...sessionAlias.values(),
      ...Object.keys(itemsBySession.value),
      ...Object.keys(historyBySession.value),
      ...Object.keys(queueBySession.value),
      ...Object.keys(activeTurnBySession.value),
    ])
    const related = [...candidates].filter((id) => id && sameClaudeSession(id, sessionId))
    if (!related.includes(sessionId)) related.push(sessionId)
    const relatedSet = new Set(related)
    const turnIds = new Set<string>()
    for (const id of related) {
      for (const item of itemsBySession.value[id] || []) {
        if (item.turnId) turnIds.add(item.turnId)
      }
      const activeTurn = activeTurnBySession.value[id]
      if (activeTurn?.turnId) turnIds.add(activeTurn.turnId)
    }
    for (const id of [...loadedSessionIds]) {
      if (related.some((key) => sameClaudeSession(id, key))) loadedSessionIds.delete(id)
    }
    related.forEach(rememberDiscardedClaudeSession)

    if (related.some((id) => sameClaudeSession(activeSessionId.value, id))) activeSessionId.value = ''
    if (related.some((id) => sameClaudeSession(loadingSessionId.value, id))) loadingSessionId.value = ''
    sendingSessionIds.value = sendingSessionIds.value.filter((id) => !related.some((key) => sameClaudeSession(id, key)))
    runningSessionIds.value = runningSessionIds.value.filter((id) => !related.some((key) => sameClaudeSession(id, key)))
    interruptingSessionIds.value = interruptingSessionIds.value.filter((id) => !related.some((key) => sameClaudeSession(id, key)))

    const withoutRelated = <T>(bucket: Record<string, T>): Record<string, T> =>
      Object.fromEntries(Object.entries(bucket).filter(([id]) =>
        !related.some((key) => sameClaudeSession(id, key)),
      ))
    itemsBySession.value = withoutRelated(itemsBySession.value)
    historyBySession.value = withoutRelated(historyBySession.value)
    queueBySession.value = withoutRelated(queueBySession.value)
    activeTurnBySession.value = withoutRelated(activeTurnBySession.value)
    liveActivityBySession.value = withoutRelated(liveActivityBySession.value)
    tokenUsageBySession.value = withoutRelated(tokenUsageBySession.value)

    const nextMetrics = { ...activeTurnMetrics.value }
    const nextStarts = { ...turnStartedAtById.value }
    for (const turnId of turnIds) {
      delete nextMetrics[turnId]
      delete nextStarts[turnId]
      finalizedTurnIds.delete(turnId)
      streamedAssistantTurns.delete(turnId)
      for (const key of [...streamSequenceByItem.keys()]) {
        if (key.startsWith(`${turnId}:`)) streamSequenceByItem.delete(key)
      }
    }
    activeTurnMetrics.value = nextMetrics
    turnStartedAtById.value = nextStarts
    for (const [key, patch] of [...pendingStreamPatches.entries()]) {
      if (related.some((id) => sameClaudeSession(patch.sessionId, id))) pendingStreamPatches.delete(key)
    }

    for (const id of [...sessionOpenSequence.keys()]) {
      if (related.some((key) => sameClaudeSession(id, key))) sessionOpenSequence.delete(id)
    }
    for (const id of [...loadingSequenceBySession.keys()]) {
      if (related.some((key) => sameClaudeSession(id, key))) loadingSequenceBySession.delete(id)
    }
    for (const id of [...latestStartedTurnBySession.keys()]) {
      if (related.some((key) => sameClaudeSession(id, key))) latestStartedTurnBySession.delete(id)
    }
    for (const id of [...sessionAlias.keys()]) {
      const target = sessionAlias.get(id) || ''
      if (relatedSet.has(id) || relatedSet.has(target)) sessionAlias.delete(id)
    }
  }

  function canMutateClaudeSession(sessionId: string): boolean {
    if (!isSessionTurnBusy(sessionId)) return true
    notify(
      'warning',
      translate('threadActions.busyActionBlocked'),
      translate('threadActions.busyActionBlockedHint'),
    )
    return false
  }

  async function deleteSession(sessionId: string): Promise<void> {
    if (!sessionId || !canMutateClaudeSession(sessionId)) return
    try {
      if (!sessionId.startsWith('pending-claude-')) {
        await deleteClaudeSessionApi(sessionId)
      }
      sessions.value = sessions.value.filter((item) => item.id !== sessionId)
      archivedSessions.value = archivedSessions.value.filter((item) => item.id !== sessionId)
      discardClaudeSessionState(sessionId)
    } catch (error) {
      notify('error', translate('threadActions.deleteFailed'), errorMessage(error))
    }
  }

  async function deleteActiveSession(): Promise<void> {
    if (!activeSessionId.value) return
    await deleteSession(activeSessionId.value)
  }

  async function renameSession(sessionId: string, name?: string): Promise<boolean> {
    const current = sessions.value.find((item) => item.id === sessionId)
      || archivedSessions.value.find((item) => item.id === sessionId)
    let nextName = name
    if (nextName === undefined) {
      const prompted = await dialogStore.prompt({
        title: translate('threadActions.rename'),
        description: translate('threadActions.renamePrompt'),
        placeholder: current?.name || '',
        confirmLabel: translate('threadActions.rename'),
        defaultValue: current?.name || '',
        maxlength: 80,
      })
      nextName = prompted ?? ''
    }
    nextName = nextName.trim()
    if (!nextName || nextName === current?.name) return false
    try {
      await renameClaudeSessionApi(sessionId, nextName)
      sessions.value = sessions.value.map((item) =>
        item.id === sessionId ? { ...item, name: nextName!, updatedAt: Math.floor(Date.now() / 1000) } : item,
      )
      notify('success', translate('threadActions.renamed'), '')
      return true
    } catch (error) {
      notify('error', translate('threadActions.renameFailed'), errorMessage(error))
      return false
    }
  }

  async function renameActiveSession(): Promise<void> {
    if (!activeSessionId.value) return
    await renameSession(activeSessionId.value)
  }

  async function archiveSession(sessionId: string): Promise<void> {
    if (!sessionId || !canMutateClaudeSession(sessionId)) return
    try {
      if (sessionId.startsWith('pending-claude-')) {
        sessions.value = sessions.value.filter((item) => item.id !== sessionId)
        discardClaudeSessionState(sessionId)
        return
      }
      await archiveClaudeSessionApi(sessionId)
      sessions.value = sessions.value.filter((item) => item.id !== sessionId)
      discardClaudeSessionState(sessionId)
      notify('success', translate('threadActions.archived'), translate('threadActions.archivedHint'))
    } catch (error) {
      notify('error', translate('threadActions.archiveFailed'), errorMessage(error))
    }
  }

  async function archiveActiveSession(): Promise<void> {
    if (!activeSessionId.value) return
    await archiveSession(activeSessionId.value)
  }

  async function unarchiveSession(sessionId: string): Promise<void> {
    try {
      await unarchiveClaudeSessionApi(sessionId)
      discardedSessionIds.delete(sessionId)
      await loadSessions()
      await loadArchivedSessions()
      notify('success', translate('threadActions.unarchived'), translate('threadActions.unarchivedHint'))
    } catch (error) {
      notify('error', translate('threadActions.unarchiveFailed'), errorMessage(error))
    }
  }

  return {
    runtime,
    sessions,
    archivedSessions,
    activeSessionId,
    itemsBySession,
    loadingSessionId,
    sending,
    interrupting,
    search,
    runningSessionIds,
    activeTurnMetrics,
    tokenUsageBySession,
    activeTokenUsage,
    workspacePath,
    isReady,
    activeItems,
    activeHistoryHasEarlier,
    activeHistoryEarlierCount,
    activeHistoryLoadingEarlier,
    isTurnRunning,
    activeQueuedMessages,
    activeTurn,
    sessionGroups,
    sameSession: sameClaudeSession,
    bootstrapEvents,
    dispose,
    enterRuntime,
    refreshRuntime,
    loadSessions,
    loadArchivedSessions,
    openSession,
    recoverActiveSession,
    loadEarlierHistory,
    newSession,
    sendMessage,
    interruptActiveTurn,
    deleteSession,
    deleteActiveSession,
    renameSession,
    renameActiveSession,
    archiveSession,
    archiveActiveSession,
    unarchiveSession,
    reorderQueuedMessage,
    retryQueuedMessage,
    removeQueuedMessage,
    sendQueuedMessageNow,
  }
})
