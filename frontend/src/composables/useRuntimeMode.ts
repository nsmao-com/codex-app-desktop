import { computed, inject, type ComputedRef, type InjectionKey } from 'vue'

import { useArenaStore } from '@/stores/arena'
import { useAppStore, type WorkspaceRuntime } from '@/stores/app'

export const ArenaPaneRuntimeKey: InjectionKey<ComputedRef<WorkspaceRuntime>> = Symbol('arenaPaneRuntime')
export const ArenaPaneIdKey: InjectionKey<ComputedRef<string>> = Symbol('arenaPaneId')

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

/**
 * Resolves the effective product runtime for the current component tree.
 * In arena multi-pane mode each pane provides its own runtime; otherwise
 * falls back to the global workbench activeRuntime.
 */
export function useRuntimeMode() {
  const appStore = useAppStore()
  const arenaStore = useArenaStore()
  const injected = inject(ArenaPaneRuntimeKey, null)
  const paneIdRef = inject(ArenaPaneIdKey, null)

  const runtime = computed<WorkspaceRuntime>(() => {
    if (injected) return normalizeRuntime(injected.value)
    return normalizeRuntime(appStore.activeRuntime)
  })

  const paneId = computed(() => paneIdRef?.value || '')

  /** Session/thread bound to this arena pane (independent of global active selection). */
  const boundSessionId = computed(() => {
    if (!paneId.value || !arenaStore.isArenaMode) return ''
    return arenaStore.sessionForPane(paneId.value)
  })

  const isCodexMode = computed(() => runtime.value === 'codex')
  const isClaudeMode = computed(() => runtime.value === 'claude')
  const isGrokMode = computed(() => runtime.value === 'grok')
  const isGeminiMode = computed(() => runtime.value === 'gemini')
  const isOpenCodeMode = computed(() => runtime.value === 'opencode')
  const usesCodexTimeline = computed(() =>
    runtime.value === 'codex' || runtime.value === 'gemini' || runtime.value === 'opencode',
  )

  return {
    runtime,
    paneId,
    boundSessionId,
    isCodexMode,
    isClaudeMode,
    isGrokMode,
    isGeminiMode,
    isOpenCodeMode,
    usesCodexTimeline,
  }
}
