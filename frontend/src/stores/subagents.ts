import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'
import { Events } from '@wailsio/runtime'

import { useAppStore } from './app'
import {
  asArray,
  asNumber,
  asRecord,
  asString,
  normalizeThreadStatus,
} from '@/utils/protocol'
import {
  SUBAGENT_RUNTIME_META,
  type SubagentActivity,
  type SubagentCapability,
  type SubagentRuntime,
  type SubagentStatus,
} from '@/types/subagents'

type EventRecord = Record<string, unknown>
type ActivityMap = Record<string, SubagentActivity[]>

const TERMINAL_STATUSES = new Set<SubagentStatus>(['completed', 'failed', 'interrupted'])
const STATUS_RANK: Record<SubagentStatus, number> = {
  unknown: 0,
  pending: 1,
  running: 2,
  completed: 3,
  failed: 3,
  interrupted: 3,
}

const EMPTY_OBSERVED: Record<SubagentRuntime, boolean> = {
  codex: false,
  claude: false,
  gemini: false,
  grok: false,
  opencode: false,
}

function scopeKey(runtime: SubagentRuntime, sessionId: string): string {
  return `${runtime}:${sessionId.trim() || 'unknown-session'}`
}

function normalizeRuntime(value: unknown): SubagentRuntime | '' {
  const raw = asString(value).trim().toLowerCase().replace(/[_-]+/g, '')
  if (raw === 'codex' || raw === 'openai') return 'codex'
  if (raw === 'claude' || raw === 'anthropic' || raw === 'claudecode') return 'claude'
  // Antigravity CLI is the Gemini-compatible runtime successor. Keep one
  // activity bucket so historical Gemini sessions and new `agy` events merge.
  if (raw === 'gemini' || raw === 'google' || raw === 'geminicli' || raw === 'antigravity' || raw === 'antigravitycli' || raw === 'agy') return 'gemini'
  if (raw === 'grok' || raw === 'xai' || raw === 'grokcli') return 'grok'
  if (raw === 'opencode' || raw === 'open') return 'opencode'
  return ''
}

function firstValue(record: EventRecord, ...keys: string[]): unknown {
  for (const key of keys) {
    const value = record[key]
    if (value !== undefined && value !== null && value !== '') return value
  }
  return undefined
}

function firstText(record: EventRecord, ...keys: string[]): string {
  for (const key of keys) {
    const value = record[key]
    if (typeof value === 'string' && value.trim()) return value.trim()
    if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  }
  return ''
}

function compactValue(value: unknown, limit = 1000): string {
  if (value === undefined || value === null || value === '') return ''
  const text = typeof value === 'string' ? value : (() => {
    try {
      return JSON.stringify(value)
    } catch {
      return String(value)
    }
  })()
  const clean = text.replace(/\s+/g, ' ').trim()
  return clean.length > limit ? `${clean.slice(0, limit)}...` : clean
}

function timestamp(value: unknown, fallback = Date.now()): number {
  const number = asNumber(value, 0)
  if (!number) return fallback
  // Provider events mix Unix seconds, milliseconds and ISO strings.
  if (number < 10_000_000_000) return Math.round(number * 1000)
  return Math.round(number)
}

function normalizeActivityStatus(value: unknown): SubagentStatus {
  const raw = normalizeThreadStatus(value).trim().toLowerCase().replace(/[_-]+/g, '')
  if (!raw) return 'unknown'
  if (raw.includes('fail') || raw === 'error') return 'failed'
  if (raw.includes('interrupt') || raw.includes('cancel')) return 'interrupted'
  if (raw.includes('complete') || raw === 'done' || raw === 'success' || raw === 'succeed' || raw === 'finished') return 'completed'
  if (raw.includes('run') || raw.includes('progress') || raw.includes('start') || raw.includes('active') || raw.includes('execut')) return 'running'
  if (raw.includes('pend') || raw.includes('queue') || raw.includes('wait')) return 'pending'
  return 'unknown'
}

function eventStatus(method: string, payload: EventRecord, item: EventRecord): SubagentStatus {
  const direct = normalizeActivityStatus(firstValue(item, 'status', 'state', 'phase'))
  if (direct !== 'unknown') return direct
  const fromPayload = normalizeActivityStatus(firstValue(payload, 'status', 'state', 'phase', 'outcome'))
  if (fromPayload !== 'unknown') return fromPayload
  const normalized = method.toLowerCase().replace(/[._/-]+/g, '')
  if (normalized.includes('complete') || normalized.includes('finish') || normalized.includes('result') || normalized.includes('done')) return 'completed'
  if (normalized.includes('fail') || normalized.includes('error')) return 'failed'
  if (normalized.includes('interrupt') || normalized.includes('cancel')) return 'interrupted'
  if (normalized.includes('start') || normalized.includes('spawn') || normalized.includes('create') || normalized.includes('run') || normalized.includes('progress') || normalized.includes('delta')) return 'running'
  return 'unknown'
}

function eventLooksLikeSubagent(method: string, payload: EventRecord, item: EventRecord): boolean {
  const toolName = firstText(
    item,
    'tool', 'toolName', 'tool_name', 'name', 'title', 'agentName', 'agentPath',
  )
  const itemType = firstText(item, 'type', 'kind', 'role')
  const haystack = [method, toolName, itemType, firstText(payload, 'agentType', 'agentKind', 'action')]
    .join(' ')
    .toLowerCase()
    .replace(/[_-]+/g, '')

  // Explicit collaboration/sub-agent item types are authoritative.
  if (/(subagent|childagent|collabagent|agentactivity|agenttool|delegatetoagent|spawnagent|invokeagent|agentinvocation)/.test(haystack)) return true
  // Claude, OpenCode and Gemini commonly expose delegation as Task/Agent tools.
  if (/^(task|agent|subtask|delegate|spawn)$/i.test(toolName)) return true
  if (itemType.toLowerCase().replace(/[_-]+/g, '') === 'subagentactivity') return true

  const childId = firstText(item, 'subagentId', 'childAgentId', 'childSessionId', 'agentId')
    || firstText(payload, 'subagentId', 'childAgentId', 'childSessionId', 'agentId')
  return Boolean(childId && /(tool|task|agent|delegat|spawn)/.test(haystack))
}

function collaborationActivities(payload: EventRecord): EventRecord[] {
  const item = asRecord(payload.item)
  const rawType = firstText(item, 'type', 'kind').toLowerCase().replace(/[_-]+/g, '')
  if (rawType !== 'collabagenttoolcall') return []
  const result = asRecord(item.result)
  const agents = asArray(firstValue(result, 'agents', 'items', 'subagents'))
  if (agents.length) {
    return agents.map((value) => ({ ...item, ...asRecord(value), itemId: firstText(item, 'id', 'itemId') }))
  }
  return [item]
}

function unwrap(raw: unknown): EventRecord {
  const outer = asRecord(raw)
  const data = asRecord(outer.data)
  // Keep the envelope fields (method/type/session) while exposing data fields.
  return Object.keys(data).length
    ? { ...outer, ...data }
    : outer
}

function eventMethod(event: EventRecord): string {
  return firstText(event, 'method', 'kind', 'event', 'type', 'name')
}

function eventPayload(event: EventRecord): EventRecord {
  const nested = asRecord(firstValue(event, 'params', 'payload', 'data'))
  return Object.keys(nested).length ? { ...event, ...nested } : event
}

function eventItem(payload: EventRecord): EventRecord {
  const candidates = [
    payload.item,
    payload.message,
    payload.tool,
    payload.toolCall,
    payload.tool_call,
    payload.activity,
    payload.agent,
  ]
  for (const candidate of candidates) {
    const record = asRecord(candidate)
    if (Object.keys(record).length) return record
  }
  return payload
}

function inferRuntime(
  hint: SubagentRuntime | '',
  event: EventRecord,
  payload: EventRecord,
  item: EventRecord,
  activeRuntime: SubagentRuntime,
): SubagentRuntime {
  const explicit = [
    firstValue(item, 'runtime', 'provider', 'providerKind'),
    firstValue(payload, 'runtime', 'provider', 'providerKind'),
    firstValue(event, 'runtime', 'provider', 'providerKind'),
  ]
    .map(normalizeRuntime)
    .find(Boolean)
  return explicit || hint || activeRuntime
}

function inferSessionId(event: EventRecord, payload: EventRecord, item: EventRecord): string {
  return firstText(
    item,
    'sessionId', 'threadId', 'conversationId',
  ) || firstText(
    payload,
    'sessionId', 'threadId', 'conversationId', 'clientSessionId',
  ) || firstText(event, 'sessionId', 'threadId', 'conversationId')
}

function inferTurnId(event: EventRecord, payload: EventRecord, item: EventRecord): string {
  return firstText(item, 'turnId', 'runId', 'requestId')
    || firstText(payload, 'turnId', 'runId', 'requestId')
    || firstText(event, 'turnId', 'runId', 'requestId')
}

function activityAction(method: string, status: SubagentStatus, item: EventRecord, payload: EventRecord): string {
  const explicit = firstText(item, 'action', 'activity', 'operation') || firstText(payload, 'action', 'operation')
  if (explicit) return explicit
  if (status === 'completed') return 'Completed'
  if (status === 'failed') return 'Failed'
  if (status === 'interrupted') return 'Interrupted'
  if (status === 'pending') return 'Queued'
  if (method.toLowerCase().includes('tool')) {
    return firstText(item, 'toolName', 'tool_name', 'name', 'tool') || 'Running tool'
  }
  if (method.toLowerCase().includes('spawn') || method.toLowerCase().includes('start')) return 'Started'
  return 'Working'
}

function activityDetail(item: EventRecord, payload: EventRecord): string {
  const values = [
    firstValue(item, 'detail', 'prompt', 'task', 'description', 'command', 'query', 'text', 'message'),
    firstValue(item, 'arguments', 'input', 'args'),
    firstValue(item, 'output', 'result', 'contentItems'),
    firstValue(payload, 'detail', 'prompt', 'task', 'description', 'command', 'query', 'text', 'message'),
    firstValue(payload, 'arguments', 'input', 'args'),
    firstValue(payload, 'output', 'result', 'contentItems'),
  ]
    .map((value) => compactValue(value, 800))
    .filter((value, index, all) => Boolean(value) && all.indexOf(value) === index)
  return compactValue(values.join(' · '), 1800)
}

function activityId(
  runtime: SubagentRuntime,
  sessionId: string,
  turnId: string,
  item: EventRecord,
  payload: EventRecord,
  method: string,
): string {
  const agent = firstText(item, 'subagentId', 'childAgentId', 'childSessionId', 'agentId')
    || firstText(payload, 'subagentId', 'childAgentId', 'childSessionId', 'agentId')
  if (agent) return `${runtime}:${sessionId || 'session'}:agent:${agent}`
  const explicit = firstText(item, 'activityId', 'id', 'itemId', 'callId', 'toolCallId', 'tool_call_id')
    || firstText(payload, 'activityId', 'itemId', 'callId', 'toolCallId', 'tool_call_id')
  if (explicit) return `${runtime}:${sessionId || 'session'}:${explicit}`
  const detail = activityDetail(item, payload)
  let hash = 2166136261
  const seed = `${runtime}\u0000${sessionId}\u0000${turnId}\u0000${agent}\u0000${method}\u0000${detail}`
  for (let index = 0; index < seed.length; index += 1) {
    hash ^= seed.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return `${runtime}:${sessionId || 'session'}:${(hash >>> 0).toString(36)}`
}

function mergeActivity(current: SubagentActivity, incoming: SubagentActivity): SubagentActivity {
  const currentTerminal = TERMINAL_STATUSES.has(current.status)
  const incomingRegresses = incoming.status === 'unknown'
    || (currentTerminal && STATUS_RANK[incoming.status] < STATUS_RANK[current.status])
  const status = incomingRegresses
    ? current.status
    : (STATUS_RANK[incoming.status] >= STATUS_RANK[current.status] ? incoming.status : current.status)
  return {
    ...current,
    ...incoming,
    status,
    agentName: incoming.agentName || current.agentName,
    action: incoming.action || current.action,
    detail: incoming.detail || current.detail,
    parentAgentId: incoming.parentAgentId || current.parentAgentId,
    startedAt: Math.min(current.startedAt || incoming.startedAt, incoming.startedAt || current.startedAt),
    updatedAt: Math.max(current.updatedAt, incoming.updatedAt),
    completedAt: incoming.completedAt || current.completedAt,
  }
}

export const useSubagentsStore = defineStore('subagents', () => {
  const appStore = useAppStore()
  const activitiesByScope = shallowRef<ActivityMap>({})
  const runtimeBySession = shallowRef<Record<string, SubagentRuntime>>({})
  const observed = shallowRef<Record<SubagentRuntime, boolean>>({ ...EMPTY_OBSERVED })
  const eventUnsubs: Array<() => void> = []
  const bootstrapped = shallowRef(false)

  const activities = computed(() => Object.values(activitiesByScope.value)
    .flat()
    .sort((left, right) => right.updatedAt - left.updatedAt))

  const activeRuntime = computed<SubagentRuntime>(() => normalizeRuntime(appStore.activeRuntime) || 'codex')

  const capabilities = computed<SubagentCapability[]>(() => SUBAGENT_RUNTIME_META.map((meta) => ({
    ...meta,
    observed: observed.value[meta.runtime] === true,
  })))

  function activeSessionId(runtime = activeRuntime.value): string {
    // Avoid importing the provider stores here (which would create a cyclic
    // dependency); the event stream itself remains useful without a selection.
    const settings = appStore.settings
    if (runtime === 'claude') return asString((settings as unknown as EventRecord).claudeSessionId)
    if (runtime === 'grok') return asString((settings as unknown as EventRecord).grokSessionId)
    return asString((settings as unknown as EventRecord).activeThreadId)
  }

  function activitiesFor(runtime: SubagentRuntime, sessionId = ''): SubagentActivity[] {
    const scoped = sessionId
      ? (activitiesByScope.value[scopeKey(runtime, sessionId)] || [])
      : activities.value.filter((item) => item.runtime === runtime)
    // Ingest stores events in arrival order, while the panel should keep the
    // newest child-agent work visible when a long run exceeds the viewport.
    return [...scoped].sort((left, right) => right.updatedAt - left.updatedAt)
  }

  function markObserved(runtime: SubagentRuntime): void {
    if (observed.value[runtime]) return
    observed.value = { ...observed.value, [runtime]: true }
  }

  function ingest(raw: unknown, channelHint: SubagentRuntime | ''): void {
    const event = unwrap(raw)
    const method = eventMethod(event)
    if (!method) return
    const payload = eventPayload(event)
    if (channelHint === 'claude' && method.toLowerCase() === 'activity.snapshot') {
      const sessionId = inferSessionId(event, payload, payload)
      const turnId = inferTurnId(event, payload, payload)
      for (const message of asArray(payload.messages)) {
        const item = asRecord(message)
        if (firstText(item, 'role').toLowerCase() !== 'tool') continue
        const toolName = firstText(item, 'toolName', 'tool_name', 'name', 'tool')
        if (!/^(task|agent|subtask|delegate|spawn)$/i.test(toolName)) continue
        ingest({
          method: 'claude/agent/activity',
          sessionId,
          turnId,
          item: {
            ...item,
            name: toolName,
            itemId: firstText(item, 'id'),
            detail: firstText(item, 'detail', 'text'),
          },
        }, 'claude')
      }
      return
    }
    const collabItems = collaborationActivities(payload)
    if (collabItems.length) {
      for (const item of collabItems) {
        ingest({
          method: `codex/collab/${method}`,
          ...payload,
          item: {
            ...item,
            itemId: firstText(item, 'subagentId', 'childAgentId', 'childSessionId', 'agentId')
              || firstText(item, 'itemId', 'id'),
          },
        }, 'codex')
      }
      return
    }
    const item = eventItem(payload)
    if (!eventLooksLikeSubagent(method, payload, item)) return

    const runtime = inferRuntime(channelHint, event, payload, item, activeRuntime.value)
    if (!runtime) return
    const sessionId = inferSessionId(event, payload, item)
    const turnId = inferTurnId(event, payload, item)
    const status = eventStatus(method, payload, item)
    const now = Date.now()
    const explicitStarted = firstValue(item, 'startedAt', 'startedAtMs', 'startTime')
      ?? firstValue(payload, 'startedAt', 'startedAtMs', 'startTime')
    const explicitCompleted = firstValue(item, 'completedAt', 'completedAtMs', 'endTime')
      ?? firstValue(payload, 'completedAt', 'completedAtMs', 'endTime')
    const agentId = firstText(item, 'subagentId', 'childAgentId', 'childSessionId', 'agentId')
      || firstText(payload, 'subagentId', 'childAgentId', 'childSessionId', 'agentId')
    const parentAgentId = firstText(item, 'parentAgentId', 'parentSubagentId', 'parentAgent')
      || firstText(payload, 'parentAgentId', 'parentSubagentId', 'parentAgent')
    const agentName = firstText(item, 'agentName', 'agentPath', 'name', 'toolName', 'tool_name', 'tool')
      || firstText(payload, 'agentName', 'agentPath', 'agent')
      || 'Sub-agent'
    const activity: SubagentActivity = {
      id: activityId(runtime, sessionId, turnId, item, payload, method),
      runtime,
      sessionId,
      turnId,
      agentId,
      parentAgentId,
      agentName,
      status,
      action: activityAction(method, status, item, payload),
      detail: activityDetail(item, payload),
      source: method,
      startedAt: timestamp(explicitStarted, now),
      updatedAt: now,
      completedAt: TERMINAL_STATUSES.has(status) ? timestamp(explicitCompleted, now) : undefined,
    }
    const key = scopeKey(runtime, sessionId)
    const current = activitiesByScope.value[key] || []
    const index = current.findIndex((candidate) => candidate.id === activity.id)
    const next = [...current]
    if (index >= 0 && next[index]) next[index] = mergeActivity(next[index], activity)
    else next.push(activity)
    // Keep the sidebar responsive during long runs while retaining the newest
    // events and the complete state of each active child agent.
    const bounded = next
      .sort((left, right) => left.updatedAt - right.updatedAt)
      .slice(-200)
    activitiesByScope.value = { ...activitiesByScope.value, [key]: bounded }
    if (sessionId) runtimeBySession.value = { ...runtimeBySession.value, [sessionId]: runtime }
    markObserved(runtime)
  }

  function bootstrapEvents(): void {
    if (bootstrapped.value) return
    bootstrapped.value = true
    const channels: Array<[string, SubagentRuntime | '']> = [
      ['codex:event', ''],
      ['claude:event', 'claude'],
      ['grok:event', 'grok'],
      // Dedicated channels are accepted for newer provider adapters; current
      // Gemini/OpenCode notifications are bridged through codex:event.
      ['gemini:event', 'gemini'],
      ['opencode:event', 'opencode'],
    ]
    for (const [channel, hint] of channels) {
      try {
        const unsubscribe = Events.On(channel, (event: unknown) => ingest(event, hint))
        if (typeof unsubscribe === 'function') eventUnsubs.push(unsubscribe)
      } catch {
        // Wails versions that do not register an optional channel can ignore it.
      }
    }
  }

  function clear(runtime?: SubagentRuntime, sessionId = ''): void {
    if (!runtime && !sessionId) {
      activitiesByScope.value = {}
      return
    }
    const next = { ...activitiesByScope.value }
    for (const key of Object.keys(next)) {
      const [keyRuntime, ...rest] = key.split(':')
      const keySession = rest.join(':')
      if ((!runtime || keyRuntime === runtime) && (!sessionId || keySession === sessionId)) delete next[key]
    }
    activitiesByScope.value = next
  }

  function dispose(): void {
    for (const unsubscribe of eventUnsubs.splice(0)) unsubscribe()
    bootstrapped.value = false
  }

  return {
    activities,
    activitiesByScope,
    activeRuntime,
    capabilities,
    observed,
    runtimeBySession,
    activeSessionId,
    activitiesFor,
    bootstrapEvents,
    ingest,
    clear,
    dispose,
  }
})
