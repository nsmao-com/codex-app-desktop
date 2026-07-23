import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

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
}

const MAX_ENTRIES = 80

function sameEntry(a: NavEntry | undefined, b: NavEntry): boolean {
  if (!a) return false
  if (a.kind !== b.kind) return false
  if (a.kind === 'route') return a.routeFullPath === b.routeFullPath
  if (a.kind === 'thread') return a.threadId === b.threadId && a.workspacePath === b.workspacePath
  return a.workspacePath === b.workspacePath
}

export const useNavigationStore = defineStore('navigation', () => {
  const entries = shallowRef<NavEntry[]>([])
  const index = shallowRef(-1)
  let applying = false
  let applyHandler: ((entry: NavEntry) => void | Promise<void>) | null = null

  const current = computed(() => (index.value >= 0 ? entries.value[index.value] ?? null : null))
  const canGoBack = computed(() => index.value > 0)
  const canGoForward = computed(() => index.value >= 0 && index.value < entries.value.length - 1)
  const backEntry = computed(() => (canGoBack.value ? entries.value[index.value - 1] ?? null : null))
  const forwardEntry = computed(() => (canGoForward.value ? entries.value[index.value + 1] ?? null : null))

  function setApplyHandler(handler: ((entry: NavEntry) => void | Promise<void>) | null): void {
    applyHandler = handler
  }

  function push(entry: NavEntry): void {
    if (applying) return
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

  async function goBack(): Promise<boolean> {
    if (!canGoBack.value) return false
    const target = entries.value[index.value - 1]
    if (!target) return false
    index.value -= 1
    applying = true
    try {
      await applyHandler?.(target)
    } finally {
      applying = false
    }
    return true
  }

  async function goForward(): Promise<boolean> {
    if (!canGoForward.value) return false
    const target = entries.value[index.value + 1]
    if (!target) return false
    index.value += 1
    applying = true
    try {
      await applyHandler?.(target)
    } finally {
      applying = false
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
    backEntry,
    forwardEntry,
    setApplyHandler,
    push,
    goBack,
    goForward,
    reset,
  }
})
