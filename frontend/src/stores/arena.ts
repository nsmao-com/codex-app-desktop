import { computed, shallowRef } from 'vue'
import { defineStore } from 'pinia'

import type { WorkspaceRuntime } from './app'

export type ArenaColumnCount = 2 | 3 | 4 | 5 | 6 | 7 | 8

export interface ArenaPane {
  id: string
  runtime: WorkspaceRuntime
}

export const ARENA_MAX_PANES = 8
export const ARENA_MIN_PANES = 2

const STORAGE_KEY = 'nice-codex.arena.v2'
const ALL_RUNTIMES: WorkspaceRuntime[] = ['codex', 'claude', 'grok', 'gemini', 'opencode']

function newPaneId(): string {
  return `pane-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

function normalizeRuntime(value: string | undefined | null): WorkspaceRuntime {
  switch (String(value || '').toLowerCase()) {
    case 'claude':
      return 'claude'
    case 'grok':
      return 'grok'
    case 'gemini':
      return 'gemini'
    case 'opencode':
    case 'open-code':
      return 'opencode'
    default:
      return 'codex'
  }
}

function clampCount(count: number): number {
  return Math.min(ARENA_MAX_PANES, Math.max(ARENA_MIN_PANES, Math.floor(count) || ARENA_MIN_PANES))
}

/** Prefer unique runtimes first, then allow repeating the seed for extra panes. */
function defaultPanes(count: number, seed: WorkspaceRuntime): ArenaPane[] {
  const n = clampCount(count)
  const seedRuntime = normalizeRuntime(seed)
  const ordered = [
    seedRuntime,
    ...ALL_RUNTIMES.filter((runtime) => runtime !== seedRuntime),
  ]
  return Array.from({ length: n }, (_, index) => ({
    id: newPaneId(),
    // After one of each provider, keep cloning the seed so same-tab multi-pane works.
    runtime: ordered[index] || seedRuntime,
  }))
}

function loadPersisted(): {
  enabled: boolean
  panes: ArenaPane[]
  focusedPaneId: string
  sessionByPane: Record<string, string>
} | null {
  try {
    // Migrate v1 → v2 key if present.
    const raw = localStorage.getItem(STORAGE_KEY)
      || localStorage.getItem('nice-codex.arena.v1')
    if (!raw) return null
    const parsed = JSON.parse(raw) as {
      enabled?: boolean
      panes?: Array<{ id?: string; runtime?: string }>
      focusedPaneId?: string
      sessionByPane?: Record<string, string>
    }
    const panes = (parsed.panes || [])
      .map((pane) => ({
        id: String(pane.id || newPaneId()),
        runtime: normalizeRuntime(pane.runtime),
      }))
      .slice(0, ARENA_MAX_PANES)
    if (!panes.length) return null
    const focusedPaneId = panes.some((pane) => pane.id === parsed.focusedPaneId)
      ? String(parsed.focusedPaneId)
      : panes[0]!.id
    const sessionByPane: Record<string, string> = {}
    const rawSessions = parsed.sessionByPane && typeof parsed.sessionByPane === 'object'
      ? parsed.sessionByPane
      : {}
    for (const pane of panes) {
      const sessionId = String(rawSessions[pane.id] || '').trim()
      if (sessionId) sessionByPane[pane.id] = sessionId
    }
    return {
      enabled: Boolean(parsed.enabled) && panes.length >= ARENA_MIN_PANES,
      panes,
      focusedPaneId,
      sessionByPane,
    }
  } catch {
    return null
  }
}

export const useArenaStore = defineStore('arena', () => {
  const persisted = loadPersisted()
  const enabled = shallowRef(Boolean(persisted?.enabled))
  const panes = shallowRef<ArenaPane[]>(
    persisted?.panes?.length
      ? persisted.panes
      : [{ id: 'main', runtime: 'codex' }],
  )
  const focusedPaneId = shallowRef(persisted?.focusedPaneId || panes.value[0]?.id || 'main')
  const sessionByPane = shallowRef<Record<string, string>>(persisted?.sessionByPane || {})
  const dragPaneId = shallowRef('')

  const focusedPane = computed(() =>
    panes.value.find((pane) => pane.id === focusedPaneId.value) || panes.value[0] || null,
  )
  const columnCount = computed(() =>
    enabled.value ? Math.min(ARENA_MAX_PANES, Math.max(1, panes.value.length)) : 1,
  )
  const isArenaMode = computed(() => enabled.value && panes.value.length >= ARENA_MIN_PANES)
  const canAddPane = computed(() => isArenaMode.value && panes.value.length < ARENA_MAX_PANES)
  const canRemovePane = computed(() => isArenaMode.value && panes.value.length > ARENA_MIN_PANES)

  function persist(): void {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        enabled: enabled.value,
        panes: panes.value,
        focusedPaneId: focusedPaneId.value,
        sessionByPane: sessionByPane.value,
      }))
    } catch {
      // ignore quota / private mode
    }
  }

  function sessionForPane(paneId: string): string {
    return (sessionByPane.value[paneId] || '').trim()
  }

  function setPaneSession(paneId: string, sessionId: string): void {
    if (!panes.value.some((pane) => pane.id === paneId)) return
    const next = { ...sessionByPane.value }
    const clean = sessionId.trim()
    if (!clean) delete next[paneId]
    else next[paneId] = clean
    sessionByPane.value = next
    persist()
  }

  /** True if another pane already binds this session for the same runtime. */
  function isSessionTakenByOtherPane(paneId: string, sessionId: string, runtime?: WorkspaceRuntime): boolean {
    const clean = sessionId.trim()
    if (!clean) return false
    const paneRuntime = runtime || panes.value.find((pane) => pane.id === paneId)?.runtime
    for (const pane of panes.value) {
      if (pane.id === paneId) continue
      if (paneRuntime && pane.runtime !== paneRuntime) continue
      if (sessionForPane(pane.id) === clean) return true
    }
    return false
  }

  function openArena(count: number, seedRuntime: WorkspaceRuntime = 'codex'): void {
    const next = defaultPanes(count, normalizeRuntime(seedRuntime))
    panes.value = next
    focusedPaneId.value = next[0]!.id
    sessionByPane.value = {}
    enabled.value = true
    dragPaneId.value = ''
    persist()
  }

  function setColumnCount(count: number, seedRuntime?: WorkspaceRuntime): void {
    const target = clampCount(count)
    if (!enabled.value) {
      openArena(target, seedRuntime || 'codex')
      return
    }
    const current = [...panes.value]
    if (target === current.length) return
    if (target > current.length) {
      const seed = seedRuntime || focusedPane.value?.runtime || current[current.length - 1]?.runtime || 'codex'
      while (current.length < target) {
        current.push({ id: newPaneId(), runtime: normalizeRuntime(seed) })
      }
    } else {
      const removed = current.splice(target)
      const nextSessions = { ...sessionByPane.value }
      for (const pane of removed) delete nextSessions[pane.id]
      sessionByPane.value = nextSessions
    }
    panes.value = current
    if (!current.some((pane) => pane.id === focusedPaneId.value)) {
      focusedPaneId.value = current[0]!.id
    }
    persist()
  }

  function addPane(runtime?: WorkspaceRuntime): boolean {
    if (panes.value.length >= ARENA_MAX_PANES) return false
    if (!enabled.value) {
      openArena(ARENA_MIN_PANES, runtime || 'codex')
      return true
    }
    const nextRuntime = normalizeRuntime(
      runtime || focusedPane.value?.runtime || panes.value[panes.value.length - 1]?.runtime || 'codex',
    )
    const pane: ArenaPane = { id: newPaneId(), runtime: nextRuntime }
    panes.value = [...panes.value, pane]
    focusedPaneId.value = pane.id
    persist()
    return true
  }

  function duplicatePane(paneId: string): boolean {
    const source = panes.value.find((pane) => pane.id === paneId)
    if (!source || panes.value.length >= ARENA_MAX_PANES) return false
    if (!enabled.value) {
      openArena(ARENA_MIN_PANES, source.runtime)
      return true
    }
    const pane: ArenaPane = { id: newPaneId(), runtime: source.runtime }
    const index = panes.value.findIndex((item) => item.id === paneId)
    const next = [...panes.value]
    next.splice(index + 1, 0, pane)
    panes.value = next
    focusedPaneId.value = pane.id
    // Do not copy session binding — same provider, independent chat.
    persist()
    return true
  }

  function setPaneRuntime(paneId: string, runtime: WorkspaceRuntime): void {
    const nextRuntime = normalizeRuntime(runtime)
    const previous = panes.value.find((pane) => pane.id === paneId)
    if (!previous) return
    panes.value = panes.value.map((pane) =>
      pane.id === paneId ? { ...pane, runtime: nextRuntime } : pane,
    )
    if (previous.runtime !== nextRuntime && sessionByPane.value[paneId]) {
      const nextSessions = { ...sessionByPane.value }
      delete nextSessions[paneId]
      sessionByPane.value = nextSessions
    }
    persist()
  }

  function focusPane(paneId: string): void {
    if (!panes.value.some((pane) => pane.id === paneId)) return
    focusedPaneId.value = paneId
    persist()
  }

  function closePane(paneId: string): void {
    if (!enabled.value) return
    if (panes.value.length <= ARENA_MIN_PANES) {
      closeArena()
      return
    }
    const next = panes.value.filter((pane) => pane.id !== paneId)
    panes.value = next
    if (focusedPaneId.value === paneId) {
      focusedPaneId.value = next[0]!.id
    }
    if (sessionByPane.value[paneId]) {
      const nextSessions = { ...sessionByPane.value }
      delete nextSessions[paneId]
      sessionByPane.value = nextSessions
    }
    persist()
  }

  function closeArena(): void {
    const focused = focusedPane.value
    enabled.value = false
    panes.value = [{
      id: focused?.id || 'main',
      runtime: focused?.runtime || 'codex',
    }]
    focusedPaneId.value = panes.value[0]!.id
    sessionByPane.value = focused && sessionByPane.value[focused.id]
      ? { [focused.id]: sessionByPane.value[focused.id]! }
      : {}
    dragPaneId.value = ''
    persist()
  }

  function movePane(
    fromId: string,
    toId: string,
    options: { persist?: boolean } = {},
  ): void {
    if (!fromId || !toId || fromId === toId) return
    const fromIndex = panes.value.findIndex((pane) => pane.id === fromId)
    const toIndex = panes.value.findIndex((pane) => pane.id === toId)
    if (fromIndex < 0 || toIndex < 0) return
    const next = [...panes.value]
    const [moved] = next.splice(fromIndex, 1)
    if (!moved) return
    next.splice(toIndex, 0, moved)
    panes.value = next
    if (options.persist !== false) persist()
  }

  /** Flush current pane order / sessions to storage (e.g. after live drag). */
  function flushPersist(): void {
    persist()
  }

  function reorderPanes(orderedIds: string[]): void {
    if (!orderedIds.length) return
    const map = new Map(panes.value.map((pane) => [pane.id, pane]))
    const next: ArenaPane[] = []
    for (const id of orderedIds) {
      const pane = map.get(id)
      if (pane) {
        next.push(pane)
        map.delete(id)
      }
    }
    for (const pane of map.values()) next.push(pane)
    if (!next.length) return
    panes.value = next
    if (!next.some((pane) => pane.id === focusedPaneId.value)) {
      focusedPaneId.value = next[0]!.id
    }
    persist()
  }

  function setDragPaneId(paneId: string): void {
    dragPaneId.value = paneId
  }

  function clearDragPaneId(): void {
    dragPaneId.value = ''
  }

  function cyclePaneRuntime(paneId: string): void {
    const pane = panes.value.find((item) => item.id === paneId)
    if (!pane) return
    const index = ALL_RUNTIMES.indexOf(pane.runtime)
    const next = ALL_RUNTIMES[(index + 1) % ALL_RUNTIMES.length]!
    setPaneRuntime(paneId, next)
  }

  return {
    enabled,
    panes,
    focusedPaneId,
    focusedPane,
    columnCount,
    isArenaMode,
    canAddPane,
    canRemovePane,
    sessionByPane,
    dragPaneId,
    maxPanes: ARENA_MAX_PANES,
    minPanes: ARENA_MIN_PANES,
    allRuntimes: ALL_RUNTIMES,
    openArena,
    setColumnCount,
    addPane,
    duplicatePane,
    setPaneRuntime,
    focusPane,
    closePane,
    closeArena,
    movePane,
    reorderPanes,
    flushPersist,
    setDragPaneId,
    clearDragPaneId,
    cyclePaneRuntime,
    sessionForPane,
    setPaneSession,
    isSessionTakenByOtherPane,
  }
})
