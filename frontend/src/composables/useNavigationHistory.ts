import { onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useCodexStore, useNavigationStore, useWorkspaceStore } from '@/stores'
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
  const codexStore = useCodexStore()
  const workspaceStore = useWorkspaceStore()

  function makeRouteEntry(): NavEntry {
    const threadName = codexStore.activeThread?.name
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
      threadId: codexStore.activeThreadId || undefined,
      workspacePath: workspaceStore.workspace?.path || undefined,
    }
  }

  function makeThreadEntry(threadID: string, threadName: string): NavEntry {
    return {
      id: `thread-${threadID}-${Date.now()}`,
      kind: 'thread',
      label: threadName || t('sidebar.newTask'),
      detail: t('window.historyThread'),
      routeName: 'workbench',
      routeFullPath: '/workbench',
      threadId: threadID,
      workspacePath: workspaceStore.workspace?.path || undefined,
    }
  }

  function makeWorkspaceEntry(path: string, name: string): NavEntry {
    return {
      id: `workspace-${path}-${Date.now()}`,
      kind: 'workspace',
      label: name || path,
      detail: t('window.historyWorkspace'),
      routeName: 'workbench',
      routeFullPath: '/workbench',
      workspacePath: path,
      threadId: codexStore.activeThreadId || undefined,
    }
  }

  async function applyEntry(entry: NavEntry): Promise<void> {
    if (entry.kind === 'workspace' && entry.workspacePath) {
      const ok = await workspaceStore.useWorkspace(entry.workspacePath)
      if (ok) await codexStore.activateProject(entry.workspacePath, entry.threadId || '')
      if (route.name !== 'workbench') await router.push({ name: 'workbench' })
      return
    }

    if (entry.kind === 'route') {
      if (entry.routeFullPath && entry.routeFullPath !== route.fullPath) {
        await router.push(entry.routeFullPath)
      }
      if (entry.threadId && entry.threadId !== codexStore.activeThreadId && !entry.threadId.startsWith('pending-thread-')) {
        await codexStore.openThread(entry.threadId)
      }
      return
    }

    if (route.name !== 'workbench') await router.push({ name: 'workbench' })
    if (entry.threadId && !entry.threadId.startsWith('pending-thread-')) {
      await codexStore.openThread(entry.threadId)
    } else if (entry.threadId?.startsWith('pending-thread-')) {
      await codexStore.newThread()
    }
  }

  onMounted(() => {
    nav.setApplyHandler(applyEntry)
    if (nav.index < 0) {
      if (codexStore.activeThreadId) {
        nav.reset(makeThreadEntry(
          codexStore.activeThreadId,
          codexStore.activeThread?.name || t('sidebar.newTask'),
        ))
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
    () => codexStore.activeThreadId,
    (threadID, previous) => {
      if (!threadID || threadID === previous) return
      if (threadID.startsWith('pending-thread-')) {
        nav.push(makeThreadEntry(threadID, t('sidebar.newTask')))
        return
      }
      const name = codexStore.activeThread?.name
        || codexStore.threads.find((item) => item.id === threadID)?.name
        || t('sidebar.recents')
      nav.push(makeThreadEntry(threadID, name))
    },
  )

  watch(
    () => workspaceStore.workspace?.path || '',
    (path, previous) => {
      if (!path || path === previous) return
      nav.push(makeWorkspaceEntry(path, workspaceStore.workspace?.name || path))
    },
  )
}
