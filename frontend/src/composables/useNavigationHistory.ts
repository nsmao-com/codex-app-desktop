import { onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useAppStore, useArenaStore, useClaudeStore, useCodexStore, useGrokStore, useNavigationStore, useWorkspaceStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'
import type { NavEntry } from '@/stores/navigation'
import { sameWorkspacePath } from '@/utils/workspacePath'

function routeLabel(name: string | symbol | null | undefined, t: (key: string) => string): string {
  switch (String(name || '')) {
    case 'settings':
      return t('settings.title')
    case 'capabilities':
      return t('capabilities.title')
    case 'workbench':
      return t('app.workbench')
    default:
      return t('app.workbench')
  }
}

export function useNavigationHistory(): void {
  const { t } = useI18n()
  const route = useRoute()
  const router = useRouter()
  const nav = useNavigationStore()
  const appStore = useAppStore()
  const arenaStore = useArenaStore()
  const codexStore = useCodexStore()
  const grokStore = useGrokStore()
  const claudeStore = useClaudeStore()
  const workspaceStore = useWorkspaceStore()

  function currentSessionContext(): {
    runtime: WorkspaceRuntime
    paneId: string
    threadId: string
    threadName: string
    workspacePath: string
  } {
    const pane = arenaStore.isArenaMode ? arenaStore.focusedPane : null
    const runtime = pane?.runtime || appStore.activeRuntime
    const paneId = pane?.id || ''
    const threadId = pane
      ? arenaStore.sessionForPane(pane.id)
      : runtime === 'grok'
        ? grokStore.activeSessionId
        : runtime === 'claude'
          ? claudeStore.activeSessionId
          : codexStore.activeThreadId
    if (runtime === 'grok') {
      const session = grokStore.sessions.find((item) => grokStore.sameSession(item.id, threadId))
      return {
        runtime,
        paneId,
        threadId,
        threadName: session?.name || session?.preview || '',
        // An unknown/restored session must not inherit whichever folder happens
        // to be focused now. openSession can resolve its own persisted workspace.
        workspacePath: session?.workspace || (threadId ? '' : grokStore.workspacePath),
      }
    }
    if (runtime === 'claude') {
      const session = claudeStore.sessions.find((item) => claudeStore.sameSession(item.id, threadId))
      return {
        runtime,
        paneId,
        threadId,
        threadName: session?.name || session?.preview || '',
        workspacePath: session?.workspace || (threadId ? '' : claudeStore.workspacePath),
      }
    }
    const thread = codexStore.threads.find((item) => codexStore.sameThread(item.id, threadId))
      || Object.values(codexStore.projectThreads).flat().find((item) => codexStore.sameThread(item.id, threadId))
    return {
      runtime,
      paneId,
      threadId,
      threadName: thread?.name || thread?.preview || '',
      workspacePath: thread?.cwd || (threadId ? '' : appStore.currentWorkspacePath),
    }
  }

  function makeRouteEntry(): NavEntry {
    const context = currentSessionContext()
    const threadName = context.threadName
    const base = routeLabel(route.name, t)
    const label = route.name === 'workbench' && threadName
      ? `${base} · ${threadName}`
      : base
    return {
      id: `route-${route.fullPath}-${Date.now()}`,
      kind: 'route',
      label,
      detail: route.fullPath,
      routeName: String(route.name || 'workbench'),
      routeFullPath: route.fullPath,
      threadId: context.threadId || undefined,
      workspacePath: context.workspacePath || undefined,
      runtime: context.runtime,
      paneId: context.paneId || undefined,
    }
  }

  function makeThreadEntry(context = currentSessionContext()): NavEntry {
    return {
      id: `thread-${context.runtime}-${context.paneId}-${context.threadId}-${Date.now()}`,
      kind: 'thread',
      label: context.threadName || t('sidebar.newTask'),
      detail: t('window.historyThread'),
      routeName: 'workbench',
      routeFullPath: '/workbench',
      threadId: context.threadId,
      workspacePath: context.workspacePath || undefined,
      runtime: context.runtime,
      paneId: context.paneId || undefined,
    }
  }

  function makeWorkspaceEntry(path: string, name: string): NavEntry {
    const context = currentSessionContext()
    const threadBelongsToWorkspace = sameWorkspacePath(context.workspacePath, path)
    return {
      id: `workspace-${path}-${Date.now()}`,
      kind: 'workspace',
      label: name || path,
      detail: t('window.historyWorkspace'),
      routeName: 'workbench',
      routeFullPath: '/workbench',
      workspacePath: path,
      threadId: threadBelongsToWorkspace ? context.threadId || undefined : undefined,
      runtime: context.runtime,
      paneId: context.paneId || undefined,
    }
  }

  function sameRuntimeSession(runtime: WorkspaceRuntime, left: string, right: string): boolean {
    if (runtime === 'grok') return grokStore.sameSession(left, right)
    if (runtime === 'claude') return claudeStore.sameSession(left, right)
    return codexStore.sameThread(left, right)
  }

  function shouldPromoteCurrentThreadEntry(entry: NavEntry): boolean {
    const current = nav.current
    if (current?.kind !== 'thread' || entry.kind !== 'thread') return false
    const runtime = entry.runtime || appStore.activeRuntime
    return current.runtime === runtime
      && current.paneId === entry.paneId
      && sameWorkspacePath(current.workspacePath || '', entry.workspacePath || '')
      && sameRuntimeSession(runtime, current.threadId || '', entry.threadId || '')
  }

  async function applySessionEntry(entry: NavEntry): Promise<boolean> {
    const runtime = entry.runtime || appStore.activeRuntime
    const threadId = entry.threadId || ''
    if (arenaStore.isArenaMode) {
      const pane = entry.paneId
        ? arenaStore.panes.find((item) => item.id === entry.paneId)
        : arenaStore.focusedPane
      if (!pane) return false
      if (pane.runtime !== runtime) arenaStore.setPaneRuntime(pane.id, runtime)
      arenaStore.focusPane(pane.id)
      if (threadId) arenaStore.selectPaneSession(pane.id, threadId)
      else arenaStore.setPaneSession(pane.id, '')
      return true
    }

    if (appStore.activeRuntime !== runtime && !await appStore.setActiveRuntime(runtime)) return false
    if (appStore.activeRuntime !== runtime) return false
    if (!threadId) {
      if (runtime === 'grok') grokStore.newSession()
      else if (runtime === 'claude') claudeStore.activeSessionId = ''
      else await codexStore.clearActiveSession()
      return true
    }
    if (runtime === 'grok') {
      await grokStore.openSession(threadId)
      if (appStore.activeRuntime !== runtime) return false
      const session = grokStore.sessions.find((item) => grokStore.sameSession(item.id, threadId))
      if (session?.workspace && !sameWorkspacePath(session.workspace, grokStore.workspacePath)) {
        await grokStore.openSession(threadId)
      }
      return true
    }
    if (runtime === 'claude') {
      await claudeStore.openSession(threadId)
      if (appStore.activeRuntime !== runtime) return false
      const session = claudeStore.sessions.find((item) => claudeStore.sameSession(item.id, threadId))
      if (session?.workspace && !sameWorkspacePath(session.workspace, claudeStore.workspacePath)) {
        await claudeStore.openSession(threadId)
      }
      return true
    }
    if (entry.workspacePath) {
      await codexStore.openProjectThread(entry.workspacePath, threadId, runtime)
      return true
    }
    await codexStore.openThread(threadId, { runtime })
    if (appStore.activeRuntime !== runtime) return false
    const thread = codexStore.threads.find((item) => codexStore.sameThread(item.id, threadId))
      || Object.values(codexStore.projectThreads).flat().find((item) => codexStore.sameThread(item.id, threadId))
    if (thread?.cwd && !sameWorkspacePath(thread.cwd, appStore.currentWorkspacePath)) {
      await codexStore.openProjectThread(thread.cwd, thread.id, runtime)
    }
    return true
  }

  async function applyEntry(entry: NavEntry): Promise<boolean> {
    if (entry.kind === 'workspace' && entry.workspacePath) {
      if (
        arenaStore.isArenaMode
        && entry.paneId
        && !arenaStore.panes.some((pane) => pane.id === entry.paneId)
      ) return false
      const arenaRevision = arenaStore.isArenaMode ? arenaStore.sessionSelectionRevision : -1
      if (entry.runtime && appStore.activeRuntime !== entry.runtime) {
        if (!await appStore.setActiveRuntime(entry.runtime)) return false
      }
      const runtime = entry.runtime || appStore.activeRuntime
      if (appStore.activeRuntime !== runtime) return false
      const ok = await workspaceStore.useWorkspace(entry.workspacePath)
      if (
        !ok
        || appStore.activeRuntime !== runtime
        || !sameWorkspacePath(entry.workspacePath, appStore.currentWorkspacePath)
        || (arenaRevision >= 0 && arenaStore.sessionSelectionRevision !== arenaRevision)
      ) return false
      if (entry.threadId || arenaStore.isArenaMode) {
        if (!await applySessionEntry(entry)) return false
      } else if (entry.runtime !== 'grok' && entry.runtime !== 'claude') {
        await codexStore.activateProject(entry.workspacePath, '', runtime)
      }
      if (route.name !== 'workbench') await router.push({ name: 'workbench' })
      return true
    }

    if (entry.kind === 'route') {
      if (entry.routeFullPath && entry.routeFullPath !== route.fullPath) {
        await router.push(entry.routeFullPath)
      }
      if (entry.threadId && !await applySessionEntry(entry)) return false
      return true
    }

    if (route.name !== 'workbench') await router.push({ name: 'workbench' })
    return applySessionEntry(entry)
  }

  onMounted(() => {
    nav.setApplyHandler(applyEntry)
    if (nav.index < 0) {
      const context = currentSessionContext()
      if (context.threadId) {
        nav.reset(makeThreadEntry(context))
      } else {
        nav.reset(makeRouteEntry())
      }
    }
  })

  onUnmounted(() => {
    nav.setApplyHandler(null)
  })

  watch(
    () => route.fullPath,
    () => {
      nav.push(makeRouteEntry())
    },
  )

  watch(
    () => {
      const context = currentSessionContext()
      return [
        context.runtime,
        context.paneId,
        context.threadId,
        context.threadName,
        context.workspacePath,
      ] as const
    },
    ([, , threadId]) => {
      if (route.name !== 'workbench') {
        if (nav.current?.kind === 'route' && nav.current.routeFullPath === route.fullPath) {
          nav.replaceCurrent(makeRouteEntry())
        }
        return
      }
      if (!threadId) {
        if (!workspaceStore.switchingWorkspace && nav.current?.threadId) {
          nav.push(makeRouteEntry())
        }
        return
      }
      const entry = makeThreadEntry()
      if (shouldPromoteCurrentThreadEntry(entry)) nav.replaceCurrent(entry)
      else nav.push(entry)
    },
  )

  watch(
    () => workspaceStore.workspace?.path || '',
    (path, previous) => {
      if (!path || path === previous || arenaStore.isArenaMode) return
      nav.push(makeWorkspaceEntry(path, workspaceStore.workspace?.name || path))
    },
  )
}
