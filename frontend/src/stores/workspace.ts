import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { WorkspaceInfo } from '../../bindings/nice_codex_desktop/models'
import { selectClaudeWorkspace, useClaudeWorkspace } from '@/utils/claudeBindings'
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
      const selected = appStore.isGrokMode
        ? await backend.SelectGrokWorkspace()
        : appStore.isClaudeMode
          ? await selectClaudeWorkspace() as WorkspaceInfo
          : await backend.SelectWorkspace()
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
      const selected = appStore.isGrokMode
        ? await backend.UseGrokWorkspace(path)
        : appStore.isClaudeMode
          ? await useClaudeWorkspace(path) as WorkspaceInfo
          : await backend.UseWorkspace(path)
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
    if (appStore.isGrokMode) {
      // SelectGrokWorkspace / UseGrokWorkspace already persisted Grok workspace on disk.
      appStore.settings = {
        ...appStore.settings,
        grokWorkspace: selected.path,
        grokRecentWorkspaces: uniqueWorkspacePaths(
          selected.path,
          appStore.settings.grokRecentWorkspaces ?? [],
        ).slice(0, 8),
      }
      return
    }
    if (appStore.isClaudeMode) {
      appStore.settings = {
        ...appStore.settings,
        claudeWorkspace: selected.path,
        claudeRecentWorkspaces: uniqueWorkspacePaths(
          selected.path,
          appStore.settings.claudeRecentWorkspaces ?? [],
        ).slice(0, 8),
      }
      return
    }
    const settings = {
      ...appStore.settings,
      workspace: selected.path,
      recentWorkspaces: uniqueWorkspacePaths(selected.path, appStore.settings.recentWorkspaces ?? []).slice(0, 8),
    }
    appStore.settings = settings
    await appStore.savePreferences(settings, { silent: true })
  }

  async function hydrateActiveRuntimeWorkspace(): Promise<void> {
    const path = appStore.currentWorkspacePath
    if (!path) {
      workspace.value = null
      return
    }
    // Same path already shown — skip disk I/O so runtime tab stays snappy.
    if (workspace.value?.path && sameWorkspace(workspace.value.path, path)) {
      return
    }
    // Optimistic local hydrate first (no round-trip).
    const leaf = path.split(/[\\/]/).filter(Boolean).at(-1) || path
    hydrateWorkspace({
      name: leaf,
      path,
      isGit: Boolean(workspace.value?.isGit && sameWorkspace(workspace.value.path, path)),
      branch: workspace.value && sameWorkspace(workspace.value.path, path) ? (workspace.value.branch || '') : '',
      changes: workspace.value && sameWorkspace(workspace.value.path, path) ? (workspace.value.changes ?? []) : [],
      gitError: '',
    })
    // Background refresh via active-runtime workspace (Grok or Codex).
    try {
      const selected = await backend.RefreshWorkspace()
      if (selected?.path) hydrateWorkspace(selected)
    } catch {
      // Keep optimistic snapshot if the path is temporarily unavailable.
    }
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

  async function createBranch(name: string): Promise<boolean> {
    const branchName = name.trim()
    if (!branchName) return false
    try {
      const result = await backend.CreateGitBranch({ name: branchName })
      notify('success', translate('git.branchCreated'), result.branch || branchName)
      await refreshWorkspace()
      return true
    } catch (error) {
      notify('error', translate('git.branchFailed'), errorMessage(error))
      return false
    }
  }

  async function commitChanges(message: string): Promise<boolean> {
    const text = message.trim()
    if (!text) return false
    try {
      const result = await backend.CommitGitChanges({ message: text })
      notify('success', translate('git.committed'), result.branch || '')
      await refreshWorkspace()
      return true
    } catch (error) {
      notify('error', translate('git.commitFailed'), errorMessage(error))
      return false
    }
  }

  async function pushBranch(): Promise<boolean> {
    try {
      const result = await backend.PushGitBranch()
      notify('success', translate('git.pushed'), result.prUrl || result.message || '')
      await refreshWorkspace()
      return true
    } catch (error) {
      notify('error', translate('git.pushFailed'), errorMessage(error))
      return false
    }
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
    hydrateActiveRuntimeWorkspace,
    refreshWorkspace,
    inspectWorkspaceDiff,
    inspectInlineDiff,
    openLiveTurnDiff,
    clearDiff,
    closeDiffSidebar,
    createBranch,
    commitChanges,
    pushBranch,
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
