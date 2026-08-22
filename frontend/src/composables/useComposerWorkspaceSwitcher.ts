import { computed, shallowRef, type ComputedRef } from 'vue'

import { translate } from '@/i18n'
import { useAppStore, useArenaStore, useClaudeStore, useCodexStore, useGrokStore, useWorkspaceStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'
import { notify } from '@/utils/notify'
import { sameWorkspacePath } from '@/utils/workspacePath'

interface ComposerWorkspaceContext {
  runtime: ComputedRef<WorkspaceRuntime>
  paneId: ComputedRef<string>
  isArenaPane: ComputedRef<boolean>
  currentWorkspacePath: ComputedRef<string>
}

interface WorkspaceSelectionTarget {
  sequence: number
  runtime: WorkspaceRuntime
  paneId: string
  sessionId: string
  arenaRevision: number
}

export function useComposerWorkspaceSwitcher(context: ComposerWorkspaceContext) {
  const appStore = useAppStore()
  const arenaStore = useArenaStore()
  const codexStore = useCodexStore()
  const claudeStore = useClaudeStore()
  const grokStore = useGrokStore()
  const workspaceStore = useWorkspaceStore()
  const switchingLocally = shallowRef(false)
  let selectionSequence = 0

  const recentWorkspacePaths = computed(() => {
    const runtime = context.runtime.value
    const current = context.currentWorkspacePath.value.trim()
    const recent = runtimeRecentWorkspacePaths(runtime)
    const available = [current, ...recent].filter((path) => path && path !== '(unknown)')
    return appStore.orderWorkspacePaths(runtime, available, [current, ...recent])
  })

  const switching = computed(() => switchingLocally.value || workspaceStore.switchingWorkspace)

  function runtimeRecentWorkspacePaths(runtime: WorkspaceRuntime): string[] {
    if (runtime === 'claude') {
      return appStore.settings.claudeRecentWorkspaces?.length
        ? appStore.settings.claudeRecentWorkspaces
        : (appStore.settings.recentWorkspaces ?? [])
    }
    if (runtime === 'grok') {
      return appStore.settings.grokRecentWorkspaces?.length
        ? appStore.settings.grokRecentWorkspaces
        : (appStore.settings.recentWorkspaces ?? [])
    }
    if (runtime === 'gemini') {
      return appStore.settings.geminiRecentWorkspaces?.length
        ? appStore.settings.geminiRecentWorkspaces
        : (appStore.settings.recentWorkspaces ?? [])
    }
    if (runtime === 'opencode') {
      return appStore.settings.openCodeRecentWorkspaces?.length
        ? appStore.settings.openCodeRecentWorkspaces
        : (appStore.settings.recentWorkspaces ?? [])
    }
    return appStore.settings.recentWorkspaces ?? []
  }

  function activeSessionId(runtime: WorkspaceRuntime): string {
    if (runtime === 'claude') return claudeStore.activeSessionId
    if (runtime === 'grok') return grokStore.activeSessionId
    return codexStore.activeThreadId
  }

  function captureTarget(): WorkspaceSelectionTarget {
    const runtime = context.runtime.value
    const paneId = context.isArenaPane.value && arenaStore.isArenaMode
      ? context.paneId.value
      : ''
    return {
      sequence: ++selectionSequence,
      runtime,
      paneId,
      sessionId: paneId ? arenaStore.sessionForPane(paneId) : activeSessionId(runtime),
      arenaRevision: paneId ? arenaStore.sessionSelectionRevision : -1,
    }
  }

  function targetIsCurrent(target: WorkspaceSelectionTarget, workspacePath = ''): boolean {
    if (
      target.sequence !== selectionSequence
      || appStore.activeRuntime !== target.runtime
      || (workspacePath && !sameWorkspacePath(workspacePath, appStore.currentWorkspacePath))
    ) return false
    if (!target.paneId) return !arenaStore.isArenaMode
    const pane = arenaStore.panes.find((item) => item.id === target.paneId)
    return Boolean(
      arenaStore.isArenaMode
      && pane
      && pane.runtime === target.runtime
      && arenaStore.focusedPaneId === target.paneId
      && arenaStore.sessionSelectionRevision === target.arenaRevision
      && arenaStore.sessionForPane(target.paneId) === target.sessionId
    )
  }

  async function ensureTargetRuntime(target: WorkspaceSelectionTarget): Promise<boolean> {
    const ready = appStore.activeRuntime === target.runtime
      ? await appStore.ensureActiveRuntimeSynced(target.runtime)
      : await appStore.setActiveRuntime(target.runtime)
    return ready && targetIsCurrent(target)
  }

  function preferredSessionForTarget<T extends { id: string }>(
    target: WorkspaceSelectionTarget,
    sessions: T[],
    currentSessionId: string,
    sameSession: (left: string, right: string) => boolean,
  ): T | undefined {
    const selectedId = target.paneId ? target.sessionId : currentSessionId
    return sessions.find((session) => sameSession(session.id, selectedId))
      || sessions.find((session) =>
        !target.paneId
        || !arenaStore.isSessionTakenByOtherPane(target.paneId, session.id, target.runtime),
      )
      || sessions[0]
  }

  function bindArenaSession(target: WorkspaceSelectionTarget, sessionId: string): boolean {
    if (!target.paneId) return false
    const previousOwner = arenaStore.selectPaneSession(target.paneId, sessionId)
    if (previousOwner) {
      notify('info', translate('arena.sessionInUseTitle'), translate('arena.sessionInUseHint'))
    }
    return true
  }

  async function switchExternalWorkspace(
    target: WorkspaceSelectionTarget,
    requestedPath = '',
  ): Promise<boolean> {
    let selectedPath = ''
    if (requestedPath) {
      selectedPath = await workspaceStore.useWorkspace(requestedPath) ? requestedPath : ''
    } else {
      selectedPath = await workspaceStore.selectWorkspace()
    }
    if (!selectedPath || !targetIsCurrent(target, selectedPath)) return false

    // In single-pane mode a manual session click during the list refresh wins.
    const sessionAfterWorkspace = target.paneId ? '' : activeSessionId(target.runtime)
    if (target.runtime === 'grok') await grokStore.loadSessions(true)
    else await claudeStore.loadSessions()
    if (!targetIsCurrent(target, selectedPath)) return false
    if (
      !target.paneId
      && (target.runtime === 'grok'
        ? !grokStore.sameSession(activeSessionId(target.runtime), sessionAfterWorkspace)
        : !claudeStore.sameSession(activeSessionId(target.runtime), sessionAfterWorkspace))
    ) return false

    if (target.runtime === 'grok') {
      const group = grokStore.sessionGroups.find((item) => sameWorkspacePath(item.path, selectedPath))
      const session = preferredSessionForTarget(
        target,
        group?.sessions ?? [],
        grokStore.activeSessionId,
        grokStore.sameSession,
      )
      if (session) {
        if (bindArenaSession(target, session.id)) return true
        await grokStore.openSession(session.id, { switchWorkspace: false })
        return targetIsCurrent(target, selectedPath)
          && grokStore.sameSession(grokStore.activeSessionId, session.id)
      }
      grokStore.newSession()
      bindArenaSession(target, '')
      return true
    }

    const group = claudeStore.sessionGroups.find((item) => sameWorkspacePath(item.path, selectedPath))
    const session = preferredSessionForTarget(
      target,
      group?.sessions ?? [],
      claudeStore.activeSessionId,
      claudeStore.sameSession,
    )
    if (session) {
      if (bindArenaSession(target, session.id)) return true
      await claudeStore.openSession(session.id, { switchWorkspace: false })
      return targetIsCurrent(target, selectedPath)
        && claudeStore.sameSession(claudeStore.activeSessionId, session.id)
    }
    const sessionId = claudeStore.newSession(Boolean(target.paneId))
    if (!sessionId) return false
    bindArenaSession(target, sessionId)
    return true
  }

  function preferredCodexThread(target: WorkspaceSelectionTarget, path: string): string {
    const group = codexStore.arenaThreadGroups.find((item) => sameWorkspacePath(item.path, path))
    const threads = (group?.threads ?? []).filter((thread) =>
      codexStore.knownRuntimeIDForThread(thread.id) === target.runtime,
    )
    return preferredSessionForTarget(
      target,
      threads,
      codexStore.activeThreadId,
      codexStore.sameThread,
    )?.id ?? ''
  }

  async function switchCodexWorkspace(
    target: WorkspaceSelectionTarget,
    requestedPath = '',
  ): Promise<boolean> {
    let selectedPath = requestedPath
    if (requestedPath) {
      await codexStore.switchProject(requestedPath, preferredCodexThread(target, requestedPath))
    } else {
      selectedPath = await codexStore.selectProject()
    }
    if (!selectedPath || !targetIsCurrent(target, selectedPath)) return false
    if (bindArenaSession(target, codexStore.activeThreadId)) return true
    return true
  }

  async function runSelection(requestedPath = ''): Promise<boolean> {
    const target = captureTarget()
    switchingLocally.value = true
    try {
      if (!await ensureTargetRuntime(target)) return false
      if (target.runtime === 'grok' || target.runtime === 'claude') {
        return await switchExternalWorkspace(target, requestedPath)
      }
      return await switchCodexWorkspace(target, requestedPath)
    } finally {
      if (target.sequence === selectionSequence) switchingLocally.value = false
    }
  }

  return {
    recentWorkspacePaths,
    switching,
    selectWorkspace: () => runSelection(),
    switchWorkspace: (path: string) => runSelection(path),
  }
}
