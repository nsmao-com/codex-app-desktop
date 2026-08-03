import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type { WorkspaceInfo } from '../../bindings/nice_codex_desktop/models'
import { selectClaudeWorkspace, useClaudeWorkspace } from '@/utils/claudeBindings'
import { sameWorkspacePath as sameWorkspace, workspaceKey } from '@/utils/workspacePath'
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
  let workspaceSwitchSequence = 0
  let workspaceRefreshSequence = 0
  let workspaceSwitchSync: Promise<void> = Promise.resolve()

  const currentPath = computed(() => workspace.value?.path ?? '')
  const changes = computed(() => workspace.value?.changes ?? [])
  const isGit = computed(() => workspace.value?.isGit ?? false)
  const branch = computed(() => workspace.value?.branch ?? '')

  async function selectWorkspace(): Promise<string> {
    const runtime = appStore.activeRuntime
    const sequence = ++workspaceSwitchSequence
    workspaceRefreshSequence += 1
    switchingWorkspace.value = true
    const request = workspaceSwitchSync
      .catch(() => undefined)
      .then(async () => {
        if (sequence !== workspaceSwitchSequence || appStore.activeRuntime !== runtime) return null
        return runtime === 'grok'
          ? await backend.SelectGrokWorkspace()
          : runtime === 'claude'
            ? await selectClaudeWorkspace() as WorkspaceInfo
            : await backend.SelectWorkspace()
      })
    workspaceSwitchSync = request.then(() => undefined, () => undefined)
    try {
      const selected = await request
      if (!selected?.path || sequence !== workspaceSwitchSequence || appStore.activeRuntime !== runtime) return ''
      await activateWorkspace(selected, runtime)
      return selected.path
    } catch (error) {
      if (sequence === workspaceSwitchSequence && appStore.activeRuntime === runtime) {
        notify('error', translate('notifications.workspaceNotOpened'), errorMessage(error))
      }
      return ''
    } finally {
      if (sequence === workspaceSwitchSequence) switchingWorkspace.value = false
    }
  }

  async function useWorkspace(path: string): Promise<boolean> {
    if (!path) return false
    if (!switchingWorkspace.value && sameWorkspace(path, appStore.currentWorkspacePath)) {
      return true
    }
    const runtime = appStore.activeRuntime
    const sequence = ++workspaceSwitchSequence
    workspaceRefreshSequence += 1
    switchingWorkspace.value = true
    const request = workspaceSwitchSync
      .catch(() => undefined)
      .then(async () => {
        if (sequence !== workspaceSwitchSequence || appStore.activeRuntime !== runtime) return null
        return runtime === 'grok'
          ? await backend.UseGrokWorkspace(path)
          : runtime === 'claude'
            ? await useClaudeWorkspace(path) as WorkspaceInfo
            : await backend.UseWorkspace(path)
      })
    workspaceSwitchSync = request.then(() => undefined, () => undefined)
    try {
      const selected = await request
      if (!selected || sequence !== workspaceSwitchSequence || appStore.activeRuntime !== runtime) return false
      await activateWorkspace(selected, runtime)
      return true
    } catch (error) {
      if (sequence === workspaceSwitchSequence && appStore.activeRuntime === runtime) {
        notify('error', translate('notifications.workspaceNotOpened'), errorMessage(error))
      }
      return false
    } finally {
      if (sequence === workspaceSwitchSequence) switchingWorkspace.value = false
    }
  }

  async function activateWorkspace(selected: WorkspaceInfo, runtime: 'codex' | 'claude' | 'grok'): Promise<void> {
    hydrateWorkspace(selected)
    if (runtime === 'grok') {
      // SelectGrokWorkspace / UseGrokWorkspace already persisted Grok workspace on disk.
      appStore.settings = {
        ...appStore.settings,
        grokWorkspace: selected.path,
        grokRecentWorkspaces: rememberWorkspacePath(
          appStore.settings.grokRecentWorkspaces ?? [],
          selected.path,
        ),
      }
      return
    }
    if (runtime === 'claude') {
      appStore.settings = {
        ...appStore.settings,
        claudeWorkspace: selected.path,
        claudeRecentWorkspaces: rememberWorkspacePath(
          appStore.settings.claudeRecentWorkspaces ?? [],
          selected.path,
        ),
      }
      return
    }
    const settings = {
      ...appStore.settings,
      workspace: selected.path,
      recentWorkspaces: rememberWorkspacePath(appStore.settings.recentWorkspaces ?? [], selected.path),
    }
    appStore.settings = settings
    await appStore.savePreferences(settings, { silent: true })
  }

  async function hydrateActiveRuntimeWorkspace(): Promise<void> {
    const sequence = ++workspaceRefreshSequence
    const runtime = appStore.activeRuntime
    const path = appStore.currentWorkspacePath
    if (!path) {
      workspace.value = null
      return
    }
    if (!await appStore.ensureActiveRuntimeSynced(runtime)) return
    if (sequence !== workspaceRefreshSequence || appStore.activeRuntime !== runtime || !sameWorkspace(path, appStore.currentWorkspacePath)) return
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
      if (
        sequence === workspaceRefreshSequence
        && appStore.activeRuntime === runtime
        && sameWorkspace(path, appStore.currentWorkspacePath)
        && selected?.path
        && sameWorkspace(selected.path, path)
      ) hydrateWorkspace(selected)
    } catch {
      // Keep optimistic snapshot if the path is temporarily unavailable.
    }
  }

  function hydrateWorkspace(selected: WorkspaceInfo): void {
    workspace.value = { ...selected, changes: selected.changes ?? [] }
  }

  async function refreshWorkspace(): Promise<void> {
    const sequence = ++workspaceRefreshSequence
    const runtime = appStore.activeRuntime
    const path = appStore.currentWorkspacePath
    if (!path || !await appStore.ensureActiveRuntimeSynced(runtime)) return
    try {
      const selected = normalizeWorkspace(await backend.RefreshWorkspace())
      if (
        sequence === workspaceRefreshSequence
        && appStore.activeRuntime === runtime
        && sameWorkspace(path, appStore.currentWorkspacePath)
        && sameWorkspace(selected.path, path)
      ) workspace.value = selected
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

function rememberWorkspacePath(recent: string[], current: string): string[] {
  const result: string[] = []
  const seen = new Set<string>()
  for (const path of recent) {
    const value = path.trim()
    const key = workspaceKey(value)
    if (!value || seen.has(key)) continue
    seen.add(key)
    result.push(value)
    if (result.length === 8) break
  }
  const currentValue = current.trim()
  const currentKey = workspaceKey(currentValue)
  if (!currentValue || seen.has(currentKey)) return result
  if (result.length === 8) result.pop()
  result.push(currentValue)
  return result
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : translate('notifications.unexpected')
}
