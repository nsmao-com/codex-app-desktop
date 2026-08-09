import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'
import type { WorkspaceRuntime } from './app'

export type NavKind = 'route' | 'thread' | 'workspace'

export type NavEntry = {
  id: string
  kind: NavKind
  label: string
  detail?: string
  routeName?: string
  routeFullPath?: string
  threadId?: string
  workspacePath?: string
  runtime?: WorkspaceRuntime
  paneId?: string
}

const MAX_ENTRIES = 80

function sameEntry(a: NavEntry | undefined, b: NavEntry): boolean {
  if (!a) return false
  if (a.kind !== b.kind) return false
  if (a.kind === 'route') return a.routeFullPath === b.routeFullPath
  if (a.kind === 'thread') {
    return a.threadId === b.threadId
      && a.workspacePath === b.workspacePath
      && a.runtime === b.runtime
      && a.paneId === b.paneId
  }
  return a.workspacePath === b.workspacePath && a.runtime === b.runtime && a.paneId === b.paneId
}

export const useNavigationStore = defineStore('navigation', () => {
  const entries = shallowRef<NavEntry[]>([])
  const index = shallowRef(-1)
  const navigating = shallowRef(false)
  let applyHandler: ((entry: NavEntry) => boolean | void | Promise<boolean | void>) | null = null

  const current = computed(() => (index.value >= 0 ? entries.value[index.value] ?? null : null))
  const canGoBack = computed(() => index.value > 0)
  const canGoForward = computed(() => index.value >= 0 && index.value < entries.value.length - 1)
  const backEntry = computed(() => (canGoBack.value ? entries.value[index.value - 1] ?? null : null))
  const forwardEntry = computed(() => (canGoForward.value ? entries.value[index.value + 1] ?? null : null))

  function setApplyHandler(
    handler: ((entry: NavEntry) => boolean | void | Promise<boolean | void>) | null,
  ): void {
    applyHandler = handler
  }

  function push(entry: NavEntry): void {
    if (navigating.value) return
    if (sameEntry(current.value ?? undefined, entry)) {
      // Refresh label/detail on the current slot.
      const next = [...entries.value]
      next[index.value] = { ...entry, id: next[index.value]?.id || entry.id }
      entries.value = next
      return
    }
    const trimmed = entries.value.slice(0, index.value + 1)
    trimmed.push(entry)
    while (trimmed.length > MAX_ENTRIES) trimmed.shift()
    entries.value = trimmed
    index.value = trimmed.length - 1
  }

  function replaceCurrent(entry: NavEntry): void {
    if (index.value < 0 || !entries.value[index.value]) {
      push(entry)
      return
    }
    const next = [...entries.value]
    next[index.value] = { ...entry, id: next[index.value]?.id || entry.id }
    entries.value = next
  }

  function removeSessions(
    runtime: WorkspaceRuntime,
    sessionIds: string[],
    replacementLabel = '',
  ): void {
    const ids = new Set(sessionIds.map((id) => id.trim()).filter(Boolean))
    if (!ids.size) return
    const currentIndex = index.value
    const next: NavEntry[] = []
    let nextIndex = -1

    entries.value.forEach((entry, entryIndex) => {
      const matches = entry.runtime === runtime
        && Boolean(entry.threadId)
        && ids.has(entry.threadId!.trim())
      if (!matches) {
        if (entryIndex === currentIndex) nextIndex = next.length
        next.push(entry)
        return
      }
      if (entryIndex !== currentIndex) return

      // Keep the current slot aligned with the empty workbench while pruning
      // stale back/forward targets for the deleted or archived session.
      nextIndex = next.length
      next.push({
        ...entry,
        label: entry.kind === 'thread' && replacementLabel ? replacementLabel : entry.label,
        threadId: undefined,
      })
    })

    entries.value = next
    index.value = next.length
      ? (nextIndex >= 0 ? nextIndex : Math.min(currentIndex, next.length - 1))
      : -1
  }

  function clearRuntimeSessions(runtime: WorkspaceRuntime, replacementLabel = ''): void {
    const currentIndex = index.value
    const next: NavEntry[] = []
    let nextIndex = -1

    entries.value.forEach((entry, entryIndex) => {
      const matches = entry.runtime === runtime && Boolean(entry.threadId)
      if (!matches) {
        if (entryIndex === currentIndex) nextIndex = next.length
        next.push(entry)
        return
      }
      if (entryIndex !== currentIndex) return

      nextIndex = next.length
      next.push({
        ...entry,
        label: entry.kind === 'thread' && replacementLabel ? replacementLabel : entry.label,
        threadId: undefined,
      })
    })

    entries.value = next
    index.value = next.length
      ? (nextIndex >= 0 ? nextIndex : Math.min(currentIndex, next.length - 1))
      : -1
  }

  async function goBack(): Promise<boolean> {
    if (navigating.value || !canGoBack.value) return false
    const target = entries.value[index.value - 1]
    if (!target) return false
    const previousIndex = index.value
    index.value -= 1
    navigating.value = true
    try {
      const applied = await applyHandler?.(target)
      if (applied === false) {
        index.value = previousIndex
        return false
      }
    } catch {
      index.value = previousIndex
      return false
    } finally {
      navigating.value = false
    }
    return true
  }

  async function goForward(): Promise<boolean> {
    if (navigating.value || !canGoForward.value) return false
    const target = entries.value[index.value + 1]
    if (!target) return false
    const previousIndex = index.value
    index.value += 1
    navigating.value = true
    try {
      const applied = await applyHandler?.(target)
      if (applied === false) {
        index.value = previousIndex
        return false
      }
    } catch {
      index.value = previousIndex
      return false
    } finally {
      navigating.value = false
    }
    return true
  }

  function reset(seed?: NavEntry): void {
    entries.value = seed ? [seed] : []
    index.value = seed ? 0 : -1
  }

  return {
    entries,
    index,
    current,
    canGoBack,
    canGoForward,
    navigating,
    backEntry,
    forwardEntry,
    setApplyHandler,
    push,
    replaceCurrent,
    removeSessions,
    clearRuntimeSessions,
    goBack,
    goForward,
    reset,
  }
})
