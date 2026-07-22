import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { WorkspaceInfo } from '../../bindings/nice_codex_desktop/models'
import { useAppStore } from './app'
import { notify } from '../utils/notify'
import { translate } from '../i18n'

export interface GitChangeView {
  status: string
  path: string
}

export const useWorkspaceStore = defineStore('workspace', () => {
  const appStore = useAppStore()

  const workspace = shallowRef<WorkspaceInfo | null>(null)
  const switchingWorkspace = shallowRef(false)
  const diffInspectionLoading = shallowRef(false)
  const inspectedDiff = shallowRef('')
  const inspectedDiffPath = shallowRef('')
  const diffSidebarOpen = shallowRef(false)
  const diffSource = shallowRef<'file' | 'turn'>('file')

  const currentPath = computed(() => workspace.value?.path ?? '')
  const changes = computed(() => workspace.value?.changes ?? [])
  const isGit = computed(() => workspace.value?.isGit ?? false)
  const branch = computed(() => workspace.value?.branch ?? '')

  async function selectWorkspace(): Promise<string> {
    try {
      const selected = await backend.SelectWorkspace()
      if (!selected.path) return ''
      await activateWorkspace(selected)
      return selected.path
    } catch (error) {
      notify('error', translate('notifications.workspaceNotOpened'), errorMessage(error))
      return ''
    }
  }

  async function useWorkspace(path: string): Promise<boolean> {
    if (!path || switchingWorkspace.value) return false
    if (sameWorkspace(path, appStore.currentWorkspacePath)) {
      return true
    }
    switchingWorkspace.value = true
    try {
      const selected = await backend.UseWorkspace(path)
      await activateWorkspace(selected)
      return true
    } catch (error) {
      notify('error', translate('notifications.workspaceNotOpened'), errorMessage(error))
      return false
    } finally {
      switchingWorkspace.value = false
    }
  }

  async function activateWorkspace(selected: WorkspaceInfo): Promise<void> {
    hydrateWorkspace(selected)
    const settings = {
      ...appStore.settings,
      workspace: selected.path,
      recentWorkspaces: uniqueWorkspacePaths(selected.path, appStore.settings.recentWorkspaces ?? []).slice(0, 8),
    }
    appStore.settings = settings
    await appStore.savePreferences(settings, { silent: true })
  }

  function hydrateWorkspace(selected: WorkspaceInfo): void {
    workspace.value = { ...selected, changes: selected.changes ?? [] }
  }

  async function refreshWorkspace(): Promise<void> {
    if (!workspace.value?.path) return
    try {
      workspace.value = normalizeWorkspace(await backend.RefreshWorkspace())
    } catch (error) {
      notify('warning', translate('notifications.gitUnavailable'), errorMessage(error))
    }
  }

  async function inspectWorkspaceDiff(path: string): Promise<void> {
    if (!path || diffInspectionLoading.value) return
    diffInspectionLoading.value = true
    inspectedDiffPath.value = path
    diffSource.value = 'file'
    diffSidebarOpen.value = true
    try {
      inspectedDiff.value = await backend.ReadWorkspaceDiff(path)
      if (!inspectedDiff.value) notify('info', translate('inspector.noFileDiff'), path)
    } catch (error) {
      inspectedDiff.value = ''
      notify('error', translate('inspector.diffLoadFailed'), errorMessage(error))
    } finally {
      diffInspectionLoading.value = false
    }
  }

  function inspectInlineDiff(path: string, diff: string): void {
    inspectedDiffPath.value = path
    inspectedDiff.value = diff
    diffSource.value = 'file'
    diffSidebarOpen.value = true
  }

  function openLiveTurnDiff(diff: string, label = ''): void {
    inspectedDiffPath.value = label || translate('inspector.currentTurn')
    inspectedDiff.value = diff
    diffSource.value = 'turn'
    diffSidebarOpen.value = true
  }

  function clearDiff(): void {
    inspectedDiff.value = ''
    inspectedDiffPath.value = ''
    diffSource.value = 'file'
  }

  function closeDiffSidebar(): void {
    diffSidebarOpen.value = false
    clearDiff()
  }

  return {
    workspace,
    switchingWorkspace,
    diffInspectionLoading,
    inspectedDiff,
    inspectedDiffPath,
    diffSidebarOpen,
    diffSource,
    currentPath,
    changes,
    isGit,
    branch,
    selectWorkspace,
    useWorkspace,
    activateWorkspace,
    hydrateWorkspace,
    refreshWorkspace,
    inspectWorkspaceDiff,
    inspectInlineDiff,
    openLiveTurnDiff,
    clearDiff,
    closeDiffSidebar,
  }
})

function normalizeWorkspace(value: WorkspaceInfo): WorkspaceInfo {
  return { ...value, changes: value.changes ?? [] }
}

function uniqueWorkspacePaths(current: string, recent: string[]): string[] {
  const result: string[] = []
  const seen = new Set<string>()
  for (const path of [current, ...recent]) {
    const value = path.trim()
    const key = workspaceKey(value)
    if (!value || seen.has(key)) continue
    seen.add(key)
    result.push(value)
  }
  return result
}

function workspaceKey(path: string): string {
  const normalized = path.trim().replace(/\\/g, '/').replace(/\/+$/, '')
  return navigator.userAgent.includes('Windows') ? normalized.toLocaleLowerCase() : normalized
}

function sameWorkspace(left: string, right: string): boolean {
  return workspaceKey(left) === workspaceKey(right)
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : translate('notifications.unexpected')
}
