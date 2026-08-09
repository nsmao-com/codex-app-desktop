import { onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useAppStore, useArenaStore, useClaudeStore, useCodexStore, useGrokStore, useNavigationStore, useWorkspaceStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'
import type { NavEntry } from '@/stores/navigation'

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
        workspacePath: session?.workspace || grokStore.workspacePath,
      }
    }
    if (runtime === 'claude') {
      const session = claudeStore.sessions.find((item) => claudeStore.sameSession(item.id, threadId))
      return {
        runtime,
        paneId,
        threadId,
        threadName: session?.name || session?.preview || '',
        workspacePath: session?.workspace || claudeStore.workspacePath,
      }
    }
    const thread = codexStore.threads.find((item) => codexStore.sameThread(item.id, threadId))
      || Object.values(codexStore.projectThreads).flat().find((item) => codexStore.sameThread(item.id, threadId))
    return {
      runtime,
      paneId,
      threadId,
      threadName: thread?.name || thread?.preview || '',
      workspacePath: thread?.cwd || appStore.currentWorkspacePath,
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
    return {
      id: `workspace-${path}-${Date.now()}`,
      kind: 'workspace',
      label: name || path,
      detail: t('window.historyWorkspace'),
      routeName: 'workbench',
      routeFullPath: '/workbench',
      workspacePath: path,
      threadId: context.threadId || undefined,
      runtime: context.runtime,
      paneId: context.paneId || undefined,
    }
  }

  async function applySessionEntry(entry: NavEntry): Promise<void> {
    const runtime = entry.runtime || appStore.activeRuntime
    const threadId = entry.threadId || ''
    if (arenaStore.isArenaMode) {
      const pane = arenaStore.panes.find((item) => item.id === entry.paneId) || arenaStore.focusedPane
      if (!pane) return
      if (pane.runtime !== runtime) arenaStore.setPaneRuntime(pane.id, runtime)
      arenaStore.focusPane(pane.id)
      if (threadId) arenaStore.selectPaneSession(pane.id, threadId)
      else arenaStore.setPaneSession(pane.id, '')
      return
    }

    if (appStore.activeRuntime !== runtime && !await appStore.setActiveRuntime(runtime)) return
    if (!threadId) return
    if (runtime === 'grok') {
      await grokStore.openSession(threadId)
      return
    }
    if (runtime === 'claude') {
      await claudeStore.openSession(threadId)
      return
    }
    if (entry.workspacePath) {
      await codexStore.openProjectThread(entry.workspacePath, threadId)
      return
    }
    await codexStore.openThread(threadId, { runtime })
  }

  async function applyEntry(entry: NavEntry): Promise<void> {
    if (entry.kind === 'workspace' && entry.workspacePath) {
      if (entry.runtime && appStore.activeRuntime !== entry.runtime) {
        if (!await appStore.setActiveRuntime(entry.runtime)) return
      }
      const ok = await workspaceStore.useWorkspace(entry.workspacePath)
      if (ok) {
        if (entry.runtime === 'grok' || entry.runtime === 'claude') await applySessionEntry(entry)
        else await codexStore.activateProject(entry.workspacePath, entry.threadId || '')
      }
      if (route.name !== 'workbench') await router.push({ name: 'workbench' })
      return
    }

    if (entry.kind === 'route') {
      if (entry.routeFullPath && entry.routeFullPath !== route.fullPath) {
        await router.push(entry.routeFullPath)
      }
      if (entry.threadId) await applySessionEntry(entry)
      return
    }

    if (route.name !== 'workbench') await router.push({ name: 'workbench' })
    await applySessionEntry(entry)
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
      if (!threadId) return
      nav.push(makeThreadEntry())
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
