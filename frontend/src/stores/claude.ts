import { Events } from '@wailsio/runtime'
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import type { TimelineItem, TokenUsageBreakdown, TurnMetrics } from '@/types/codex'
import {
  archiveClaudeSession as archiveClaudeSessionApi,
  deleteClaudeSession as deleteClaudeSessionApi,
  interruptClaudeTurn as interruptClaudeTurnApi,
  listArchivedClaudeSessions,
  listClaudeSessionTurnUsages,
  listClaudeSessions,
  readClaudeSession,
  refreshClaudeRuntime,
  renameClaudeSession as renameClaudeSessionApi,
  sendClaudeMessage as sendClaudeMessageApi,
  unarchiveClaudeSession as unarchiveClaudeSessionApi,
  type ClaudeMessage,
  type ClaudeRuntimeStatus,
  type ClaudeSessionSummary,
  type ClaudeTurnRef,
} from '@/utils/claudeBindings'
import { notify } from '@/utils/notify'
import { translate } from '@/i18n'
import { normalizeThreadTokenUsage } from '@/utils/protocol'
import { resolveProviderModelContextWindow } from '@/utils/accountUsage'
import { useAppStore } from './app'
import { useDialogStore } from './dialog'
import { useWorkspaceStore } from './workspace'

function sameWorkspacePath(left: string, right: string): boolean {
  return workspaceKey(left) === workspaceKey(right)
}

function workspaceKey(path: string): string {
  return path
    .trim()
    .replace(/[\\/]+$/, '')
    .replace(/\//g, '\\')
    .toLowerCase() || '(unknown)'
}

function workspaceLeafName(path: string): string {
  if (!path || path === '(unknown)') return path || 'unknown'
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

function looksLikeFilesystemPath(path: string): boolean {
  return /^[a-zA-Z]:[\\/]/.test(path) || path.startsWith('/') || path.startsWith('~/')
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

export interface ClaudeSessionGroup {
  path: string
  name: string
  active: boolean
  sessions: ClaudeSessionSummary[]
}

export interface ClaudeQueuedMessage {
  id: string
  sessionId: string
  text: string
  images: string[]
  state: 'queued' | 'sending' | 'failed'
  error: string
  createdAt: number
  localAppended?: boolean
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
  const loadingSessionId = shallowRef('')
  const sending = shallowRef(false)
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
  /** Per-item bridge sequence; cumulative snapshots make out-of-order delivery harmless. */
  const streamSequenceByItem = new Map<string, number>()
  const streamedAssistantTurns = new Set<string>()
  const finalizedTurnIds = new Set<string>()
  let eventsBound = false
  let disposed = false

  const workspacePath = computed(() =>
    appStore.settings.claudeWorkspace || appStore.settings.workspace || '',
  )

  /** Route stream events to the timeline bucket that actually has the turn. */
  function resolveEventSessionId(sessionId: string): string {
    const raw = (sessionId || '').trim()
    if (!raw) return activeSessionId.value || ''

    // Always prefer promoting pending → real before reading alias, so the first
    // stream event cannot create a second timeline under the real UUID alone.
    if (!raw.startsWith('pending-claude-')) {
      const pendingActive = activeSessionId.value
      if (pendingActive.startsWith('pending-claude-')) {
        promotePendingSession(pendingActive, raw)
        return raw
      }
      for (const id of Object.keys(itemsBySession.value)) {
        if (!id.startsWith('pending-claude-')) continue
        if ((itemsBySession.value[id] || []).length === 0) continue
        promotePendingSession(id, raw)
        return raw
      }
    }

    if (sessionAlias.has(raw)) return sessionAlias.get(raw) || raw
    if (itemsBySession.value[raw]?.length) return raw
    return raw
  }

  /**
   * Move pending timeline → real session id without scrambling order.
   * Early stream deltas may already live under the real id; keep user bubbles first.
   */
  function promotePendingSession(pendingId: string, realId: string): void {
    if (!pendingId || !realId || pendingId === realId) return
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
      nextQ[realId] = q.map((row) => ({ ...row, sessionId: realId }))
      queueBySession.value = nextQ
    }
  }
  const isReady = computed(() => Boolean(runtime.value.available))
  const activeItems = computed(() => {
    const sessionId = activeSessionId.value
    const base = [...(itemsBySession.value[sessionId] || [])]
    const turn = activeTurnBySession.value[sessionId]
    if (!turn?.turnId) return base
    const activity = liveActivityBySession.value[sessionId] || []
    if (!activity.length) return base
    return mergeClaudeLiveActivity(base, activity, turn.turnId)
  })
  const isTurnRunning = computed(() =>
    Boolean(activeSessionId.value && runningSessionIds.value.includes(activeSessionId.value)),
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
    groups.sort((a, b) => {
      if (a.active !== b.active) return a.active ? -1 : 1
      const aTime = a.sessions[0]?.updatedAt || 0
      const bTime = b.sessions[0]?.updatedAt || 0
      return bTime - aTime
    })
    return groups
  })

  function bootstrapEvents(): void {
    if (eventsBound) return
    eventsBound = true
    Events.On('claude:event', (event: any) => {
      if (disposed) return
      const data = (event?.data ?? event) as Record<string, any>
      handleEvent(data)
    })
  }

  function dispose(): void {
    disposed = true
  }

  function handleEvent(data: Record<string, any>): void {
    const kind = String(data.kind || '')
    const rawSessionId = String(data.sessionId || '')
    const turnId = String(data.turnId || '')
    if (!rawSessionId && !activeSessionId.value) return
    const sessionId = resolveEventSessionId(rawSessionId || activeSessionId.value)
    if (!sessionId) return

    if (kind === 'turn.started') {
      if (turnId) {
        finalizedTurnIds.delete(turnId)
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
      const msg = data.message as ClaudeMessage
      const role = (msg.role || '').toLowerCase()
      // Skip duplicate user rows (optimistic UI already showed this text).
      if (role === 'user' || role === 'human') {
        const text = (msg.text || '').trim()
        const list = itemsBySession.value[sessionId] || []
        for (let i = list.length - 1; i >= Math.max(0, list.length - 8); i -= 1) {
          const row = list[i]
          if (row?.type === 'userMessage' && (row.text || '').trim() === text) {
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
      const list = [...(itemsBySession.value[sessionId] || [])]
      let index = list.findIndex((item) => item.id === id)
      if (index < 0 && turnId) {
        index = list.findIndex((item) =>
          item.turnId === turnId
          && (role === 'reasoning' ? item.type === 'reasoning' : item.type === 'agentMessage'),
        )
      }
      if (index >= 0) {
        const current = list[index]
        list[index] = {
          ...current,
          id: current.id || id,
          turnId: turnId || current.turnId,
          text: mode === 'replace' ? nextChunk : (current.text || '') + nextChunk,
          status: 'inProgress',
          type: role === 'reasoning' ? 'reasoning' : current.type === 'userMessage' ? current.type : (role === 'reasoning' ? 'reasoning' : 'agentMessage'),
        }
      } else {
        list.push(messageToItem({
          id, role, text: nextChunk, status: 'inProgress', createdAt: Math.floor(Date.now() / 1000),
        }, turnId || sessionId))
      }
      itemsBySession.value = { ...itemsBySession.value, [sessionId]: list }
      return
    }

    if (kind === 'turn.completed') {
      const activeTurn = activeTurnBySession.value[sessionId]
      if (turnId && activeTurn?.turnId && activeTurn.turnId !== turnId) return
      if (turnId) finalizedTurnIds.add(turnId)
      markRunning(sessionId, false)
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
        notify('error', translate('notifications.agentFailed'), String(data.error))
      }
      void appStore.loadLocalUsage().catch(() => undefined)
      void (async () => {
        try {
          if (sessionId && !sessionId.startsWith('pending-claude-')) {
            await openSession(sessionId, { switchWorkspace: false })
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

  async function enterRuntime(refreshSessions = true): Promise<void> {
    await refreshRuntime()
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
    try {
      const list = await listClaudeSessions(workspacePath.value, search.value)
      sessions.value = list || []
    } catch (error) {
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

  async function openSession(sessionId: string, options?: { switchWorkspace?: boolean }): Promise<void> {
    if (!sessionId) return
    activeSessionId.value = sessionId
    loadingSessionId.value = sessionId
    try {
      // If the session belongs to another project, switch Claude workspace first
      // so the active group highlights like Codex / Grok.
      const known = sessions.value.find((item) => item.id === sessionId)
      const targetWorkspace = known?.workspace || ''
      if (
        options?.switchWorkspace !== false
        && targetWorkspace
        && looksLikeFilesystemPath(targetWorkspace)
        && !sameWorkspacePath(targetWorkspace, workspacePath.value)
      ) {
        await workspaceStore.useWorkspace(targetWorkspace)
      }

      // Keep any in-memory timeline (especially an active stream) so switching
      // away and back does not wipe already-rendered tokens.
      const cached = itemsBySession.value[sessionId] || []
      const liveTurn = activeTurnBySession.value[sessionId]
      const isLive = runningSessionIds.value.includes(sessionId) || Boolean(liveTurn)

      const detail = await readClaudeSession(sessionId)
      const messages = detail.messages || []
      const fromDisk = buildTimelineFromMessages(sessionId, messages)

      let nextItems: TimelineItem[]
      if (isLive && cached.length > 0) {
        // Running turn: memory is authoritative for the live bubble; merge disk
        // history underneath so older completed turns are not lost.
        nextItems = mergeDiskWithLiveTimeline(fromDisk, cached, liveTurn?.turnId || '')
      } else {
        // Completed history is disk-authoritative. Keep only cache rows that
        // are genuinely newer/active so an old local snapshot cannot replace
        // or reorder the native transcript.
        nextItems = mergeDiskWithCachedTimeline(fromDisk, cached)
      }
      itemsBySession.value = { ...itemsBySession.value, [sessionId]: nextItems }
      // Ensure summary is present / refreshed at top of list.
      if (detail.summary?.id) {
        const others = sessions.value.filter((item) => item.id !== detail.summary.id)
        sessions.value = [detail.summary, ...others]
      }
      void hydrateSessionTokenUsage(sessionId)
    } catch (error) {
      notify('error', translate('sidebar.claudeEmpty'), errorMessage(error))
    } finally {
      if (loadingSessionId.value === sessionId) loadingSessionId.value = ''
    }
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

  function buildTimelineFromMessages(sessionId: string, messages: ClaudeMessage[]): TimelineItem[] {
    const items: TimelineItem[] = []
    let turnSeed = 0
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

  /**
   * Disk = completed history; live = in-memory stream. Prefer live rows for the
   * active turn and any in-progress items; keep disk order for older turns.
   */
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
      // Dedupe optimistic user vs disk user by text near the end.
      if (item.type === 'userMessage') {
        const text = (item.text || '').trim()
        if (out.some((row) => row.type === 'userMessage' && (row.text || '').trim() === text)) {
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
        const text = (item.text || '').trim()
        if (out.some((row) => row.type === 'userMessage' && (row.text || '').trim() === text)) {
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

  function newSession(): void {
    if (!workspacePath.value) {
      notify('error', translate('sidebar.claudeEmpty'), translate('app.needWorkspaceHintReady'))
      return
    }
    const id = `pending-claude-${Date.now()}`
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
    sessions.value = [summary, ...sessions.value.filter((item) => !item.id.startsWith('pending-claude-'))]
    activeSessionId.value = id
    itemsBySession.value = { ...itemsBySession.value, [id]: [] }
  }

  function ensureActiveSessionId(): string {
    let sessionId = activeSessionId.value
    if (sessionId) return sessionId
    sessionId = `pending-claude-${Date.now()}`
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
    }, ...sessions.value.filter((item) => !item.id.startsWith('pending-claude-'))]
    itemsBySession.value = { ...itemsBySession.value, [sessionId]: [] }
    return sessionId
  }

  function isSessionBusy(sessionId: string): boolean {
    const requested = sessionId.trim()
    const active = activeSessionId.value.trim()
    const sameSession = (left: string, right: string): boolean => {
      if (!left || !right) return false
      if (left === right) return true
      return sessionAlias.get(left) === right || sessionAlias.get(right) === left
    }

    // `sending` only represents the short backend hand-off window. Scope it to
    // the current session so a different session is not queued accidentally.
    if (sending.value && sameSession(requested, active)) return true
    if (runningSessionIds.value.some((id) => sameSession(id, requested))) return true
    if (Object.keys(activeTurnBySession.value).some((id) => sameSession(id, requested))) return true
    return false
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
    if (isSessionBusy(sessionId)) {
      enqueue(sessionId, content, images)
      return true
    }
    return dispatchTurn(sessionId, content, images)
  }

  /** Start exactly one CLI turn. Caller must ensure the session is not already busy. */
  async function dispatchTurn(sessionId: string, content: string, images: string[]): Promise<boolean> {
    if (!workspacePath.value) return false
    sending.value = true
    // Mark busy BEFORE await so a second sendMessage cannot also dispatch.
    markRunning(sessionId, true)
    const now = Math.floor(Date.now() / 1000)
    const localTurnId = `claude-local-${now}`
    const userItem = messageToItem({
      id: `${sessionId}:local-user-${now}`,
      role: 'user',
      text: content,
      status: 'completed',
      createdAt: now,
    }, localTurnId)
    itemsBySession.value = {
      ...itemsBySession.value,
      [sessionId]: [...(itemsBySession.value[sessionId] || []), userItem],
    }

    let attachedTurn = false
    try {
      const ref = await sendClaudeMessageApi({
        sessionId: sessionId.startsWith('pending-claude-') ? '' : sessionId,
        workspace: workspacePath.value,
        text: content,
        images,
        model: appStore.settings.claudeModel || '',
        effort: appStore.settings.claudeEffort || 'high',
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

      activeSessionId.value = nextSessionId
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
            item.turnId === localTurnId || item.id.endsWith(`local-user-${now}`)
              ? { ...item, turnId: ref.turnId }
              : item,
          ),
        }
      }
      void loadSessions()
      return true
    } catch (error) {
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
      sending.value = false
    }
  }

  function remapSessionBusy(fromId: string, toId: string): void {
    if (!fromId || !toId || fromId === toId) return
    if (runningSessionIds.value.includes(fromId)) {
      markRunning(fromId, false)
      markRunning(toId, true)
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
        const idx = out.findIndex((row) =>
          row.type === 'userMessage' && (row.text || '').trim() === (item.text || '').trim(),
        )
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

  function enqueue(sessionId: string, text: string, images: string[]): void {
    const item: ClaudeQueuedMessage = {
      id: `claude-q-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
      sessionId,
      text,
      images,
      state: 'queued',
      error: '',
      createdAt: Date.now(),
    }
    const list = [...(queueBySession.value[sessionId] || []), item]
    queueBySession.value = { ...queueBySession.value, [sessionId]: list }
  }

  async function flushQueue(sessionId: string): Promise<void> {
    const list = queueBySession.value[sessionId] || []
    const next = list.find((item) => item.state === 'queued')
    if (!next) return
    await sendQueuedMessageNow(next.id)
  }

  async function sendQueuedMessageNow(messageId: string): Promise<void> {
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
    if (!item || !sessionId) return
    if (isSessionBusy(sessionId)) return

    queueBySession.value = {
      ...queueBySession.value,
      [sessionId]: list.map((row) => (row.id === messageId ? { ...row, state: 'sending' } : row)),
    }
    // Use dispatchTurn — never sendMessage (which would re-enqueue while busy).
    const ok = await dispatchTurn(sessionId, item.text, item.images)
    const remaining = (queueBySession.value[sessionId] || []).filter((row) => row.id !== messageId)
    if (!ok) {
      queueBySession.value = {
        ...queueBySession.value,
        [sessionId]: [
          ...remaining,
          { ...item, state: 'failed', error: translate('notifications.sendFailed') },
        ],
      }
      return
    }
    queueBySession.value = { ...queueBySession.value, [sessionId]: remaining }
  }

  function reorderQueuedMessage(_messageId?: string, _direction?: 'up' | 'down'): void {
    // Queue reorder not implemented for Claude yet; accept args for Composer parity.
  }
  function retryQueuedMessage(messageId: string): void {
    void sendQueuedMessageNow(messageId)
  }
  function removeQueuedMessage(messageId: string): void {
    const sessionId = activeSessionId.value
    const list = (queueBySession.value[sessionId] || []).filter((row) => row.id !== messageId)
    queueBySession.value = { ...queueBySession.value, [sessionId]: list }
  }

  async function interruptActiveTurn(): Promise<void> {
    const ref = activeTurn.value
    if (!ref) return
    try {
      await interruptClaudeTurnApi(ref)
    } catch (error) {
      notify('error', translate('notifications.interruptFailed'), errorMessage(error))
    }
  }

  async function deleteSession(sessionId: string): Promise<void> {
    if (!sessionId) return
    try {
      if (!sessionId.startsWith('pending-claude-')) {
        await deleteClaudeSessionApi(sessionId)
      }
      sessions.value = sessions.value.filter((item) => item.id !== sessionId)
      archivedSessions.value = archivedSessions.value.filter((item) => item.id !== sessionId)
      if (activeSessionId.value === sessionId) {
        activeSessionId.value = ''
      }
      const next = { ...itemsBySession.value }
      delete next[sessionId]
      itemsBySession.value = next
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
    try {
      if (sessionId.startsWith('pending-claude-')) {
        sessions.value = sessions.value.filter((item) => item.id !== sessionId)
        if (activeSessionId.value === sessionId) activeSessionId.value = ''
        return
      }
      await archiveClaudeSessionApi(sessionId)
      sessions.value = sessions.value.filter((item) => item.id !== sessionId)
      if (activeSessionId.value === sessionId) activeSessionId.value = ''
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
    search,
    runningSessionIds,
    activeTurnMetrics,
    tokenUsageBySession,
    activeTokenUsage,
    workspacePath,
    isReady,
    activeItems,
    isTurnRunning,
    activeQueuedMessages,
    activeTurn,
    sessionGroups,
    bootstrapEvents,
    dispose,
    enterRuntime,
    refreshRuntime,
    loadSessions,
    loadArchivedSessions,
    openSession,
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
