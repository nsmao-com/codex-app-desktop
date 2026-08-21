<script setup lang="ts">
import {
  Archive,
  Copy,
  FileDiff,
  GitBranch,
  MoreHorizontal,
  LoaderCircle,
  Pencil,
  ScanSearch,
  Trash2,
} from '@lucide/vue'
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import ApprovalDialog from './ApprovalDialog.vue'
import ChatTimeline from './ChatTimeline.vue'
import ComposerPanel from './ComposerPanel.vue'
import WorkspaceWelcome from './WorkspaceWelcome.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useAppStore, useArenaStore, useBrowserStore, useClaudeStore, useCodexStore, useDialogStore, useGrokStore, useWorkspaceStore } from '@/stores'
import { useRuntimeMode } from '@/composables/useRuntimeMode'
import { sameWorkspacePath, workspaceKey } from '@/utils/workspacePath'

const appStore = useAppStore()
const {
  runtime: paneRuntime,
  isCodexMode,
  isClaudeMode,
  isGrokMode,
  isGeminiMode,
  isOpenCodeMode,
  usesCodexTimeline: paneUsesCodexTimeline,
  isArenaPane,
  paneId,
  boundSessionId,
} = useRuntimeMode()
const arenaStore = useArenaStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const workspaceStore = useWorkspaceStore()
const browserStore = useBrowserStore()
const dialogStore = useDialogStore()
const { t } = useI18n()
const usesCodexTimeline = paneUsesCodexTimeline

const props = withDefaults(defineProps<{
  /** Arena panes render their own provider/session chrome. */
  hideChrome?: boolean
}>(), {
  hideChrome: false,
})

const emit = defineEmits<{
  'show-inspector': []
}>()

type ComposerRuntime = 'codex' | 'claude' | 'grok' | 'gemini' | 'opencode'
type ComposerDraft = { text: string; images: string[] }
type ComposerDraftContext = {
  key: string
  runtime: ComposerRuntime
  sessionId: string
  workspace: string
}

const COMPOSER_DRAFTS_STORAGE_KEY = 'nice-codex.composer-drafts.v1'

function loadComposerDrafts(): Record<string, ComposerDraft> {
  try {
    const value = JSON.parse(localStorage.getItem(COMPOSER_DRAFTS_STORAGE_KEY) || '{}') as Record<string, ComposerDraft>
    return Object.fromEntries(Object.entries(value).filter(([, draft]) =>
      draft && typeof draft.text === 'string' && Array.isArray(draft.images),
    ))
  } catch {
    return {}
  }
}

const composerDrafts = shallowRef<Record<string, ComposerDraft>>(loadComposerDrafts())
const draftKeyAliases = new Map<string, string>()

const activeComposerContext = computed<ComposerDraftContext>(() => {
  const runtime = paneRuntime.value as ComposerRuntime
  const sessionId = isArenaPane.value
    ? boundSessionId.value
    : (runtime === 'grok'
      ? grokStore.activeSessionId
      : runtime === 'claude'
        ? claudeStore.activeSessionId
        : codexStore.activeThreadId)
  const workspace = runtime === 'grok'
    ? grokStore.workspacePath
    : runtime === 'claude'
      ? claudeStore.workspacePath
      : runtime === 'gemini'
        ? (appStore.settings.geminiWorkspace || appStore.settings.workspace)
        : runtime === 'opencode'
          ? (appStore.settings.openCodeWorkspace || appStore.settings.workspace)
          : appStore.settings.workspace
  const identity = sessionId
    ? ['session', sessionId.trim()]
    : ['workspace', workspaceKey(workspace)]
  return {
    key: JSON.stringify([runtime, ...identity]),
    runtime,
    sessionId: sessionId.trim(),
    workspace,
  }
})

watch(composerDrafts, (drafts) => {
  try {
    localStorage.setItem(COMPOSER_DRAFTS_STORAGE_KEY, JSON.stringify(drafts))
  } catch {
    // Storage can be unavailable in private mode; in-memory drafts still work.
  }
})

function setComposerDraft(key: string, patch: Partial<ComposerDraft>): void {
  const current = composerDrafts.value[key] ?? { text: '', images: [] }
  const nextDraft: ComposerDraft = {
    text: patch.text ?? current.text,
    images: patch.images ? [...patch.images] : current.images,
  }
  const next = { ...composerDrafts.value }
  if (!nextDraft.text && !nextDraft.images.length) delete next[key]
  else next[key] = nextDraft
  composerDrafts.value = next
}

function mergeComposerDraftText(restored: string, current: string): string {
  if (!restored) return current
  if (!current) return restored
  if (restored.trim() === current.trim()) return current
  return `${restored}\n\n${current}`
}

function resolveDraftKey(key: string): string {
  const seen = new Set<string>()
  let current = key
  while (!seen.has(current)) {
    seen.add(current)
    const next = draftKeyAliases.get(current)
    if (!next || next === current) break
    current = next
  }
  return current
}

function migrateComposerDraft(fromKey: string, toKey: string): void {
  const sourceKey = resolveDraftKey(fromKey)
  const targetKey = resolveDraftKey(toKey)
  draftKeyAliases.set(fromKey, targetKey)
  if (sourceKey !== fromKey) draftKeyAliases.set(sourceKey, targetKey)
  if (sourceKey === targetKey) return

  const source = composerDrafts.value[sourceKey]
  if (!source) return
  const target = composerDrafts.value[targetKey]
  const next = { ...composerDrafts.value }
  delete next[sourceKey]
  next[targetKey] = {
    text: mergeComposerDraftText(source.text, target?.text || ''),
    images: [...new Set([...source.images, ...(target?.images ?? [])])],
  }
  composerDrafts.value = next
}

function sameComposerSession(previous: ComposerDraftContext, current: ComposerDraftContext): boolean {
  if (previous.runtime !== current.runtime || !previous.sessionId || !current.sessionId) return false
  if (previous.sessionId === current.sessionId) return true
  if (current.runtime === 'grok') return grokStore.sameSession(previous.sessionId, current.sessionId)
  if (current.runtime === 'claude') return claudeStore.sameSession(previous.sessionId, current.sessionId)
  return codexStore.sameThread(previous.sessionId, current.sessionId)
}

watch(activeComposerContext, (current, previous) => {
  if (current.key !== previous.key && sameComposerSession(previous, current)) {
    migrateComposerDraft(previous.key, current.key)
  }
}, { flush: 'sync' })

const draft = computed<string>({
  get: () => composerDrafts.value[activeComposerContext.value.key]?.text ?? '',
  set: (text) => setComposerDraft(activeComposerContext.value.key, { text }),
})
const draftImages = computed<string[]>({
  get: () => composerDrafts.value[activeComposerContext.value.key]?.images ?? [],
  set: (images) => setComposerDraft(activeComposerContext.value.key, { images }),
})

function restoreComposerDraft(payload: { draftKey: string; text: string; images: string[] }): void {
  const key = resolveDraftKey(payload.draftKey)
  const current = composerDrafts.value[key] ?? { text: '', images: [] }
  setComposerDraft(key, {
    text: mergeComposerDraftText(payload.text, current.text),
    images: [...new Set([...current.images, ...payload.images])],
  })
}

function appendComposerDraftImages(payload: { draftKey: string; images: string[] }): void {
  const key = resolveDraftKey(payload.draftKey)
  const current = composerDrafts.value[key] ?? { text: '', images: [] }
  setComposerDraft(key, {
    images: [...new Set([...current.images, ...payload.images])],
  })
}

const welcomeEpoch = shallowRef(0)
const messageSentEpoch = shallowRef(0)
const paneSessionId = computed(() => {
  if (isArenaPane.value) return boundSessionId.value
  if (isGrokMode.value) return grokStore.activeSessionId
  if (isClaudeMode.value) return claudeStore.activeSessionId
  return codexStore.activeThreadId
})

function matchingCodexThreadKey(record: Record<string, unknown>, threadId: string): string {
  return Object.keys(record).find((id) => codexStore.sameThread(id, threadId)) || ''
}

const paneSessionBusy = computed(() => {
  const id = paneSessionId.value
  if (!id) return false
  if (isGrokMode.value) {
    return Boolean(grokStore.sessionMutationForSession(id))
      || grokStore.sendingSessionIds.some((candidate) => grokStore.sameSession(candidate, id))
      || grokStore.runningSessionIds.some((candidate) => grokStore.sameSession(candidate, id))
      || grokStore.isSessionLoading(id)
      || Object.entries(grokStore.queuedBySession).some(([candidate, queue]) =>
        grokStore.sameSession(candidate, id)
        && queue.some((message) => message.state === 'queued' || message.state === 'sending'),
      )
  }
  if (isClaudeMode.value) {
    return Boolean(claudeStore.sessionMutationForSession(id))
      || claudeStore.sendingSessionIds.some((candidate) => claudeStore.sameSession(candidate, id))
      || claudeStore.runningSessionIds.some((candidate) => claudeStore.sameSession(candidate, id))
      || claudeStore.isSessionLoading(id)
      || Object.entries(claudeStore.queueBySession).some(([candidate, queue]) =>
        claudeStore.sameSession(candidate, id)
        && queue.some((message) => message.state === 'queued' || message.state === 'sending'),
      )
  }
  const queueKey = matchingCodexThreadKey(codexStore.queuedMessagesByThread, id)
  return codexStore.threadIsBusy(id)
    || Boolean(codexStore.threadMutationForThread(id))
    || Boolean(queueKey && codexStore.queuedMessagesByThread[queueKey]
      ?.some((message) => message.state === 'queued' || message.state === 'sending'))
})
const paneSessionPending = computed(() => {
  const id = paneSessionId.value
  if (isGrokMode.value) return id.startsWith('pending-grok-')
  if (isClaudeMode.value) return id.startsWith('pending-claude-')
  return id.startsWith('pending-thread-')
})
const panePendingRequest = computed(() => isArenaPane.value
  ? codexStore.pendingRequestForThread(paneSessionId.value)
  : codexStore.pendingRequest)
const paneIsFocused = computed(() => !isArenaPane.value || arenaStore.focusedPaneId === paneId.value)
const hasConversation = computed(() => {
  const id = paneSessionId.value
  if (!id) return false
  if (isClaudeMode.value) {
    if (claudeStore.sameSession(id, claudeStore.activeSessionId)) {
      return claudeStore.activeItems.length > 0 || Boolean(id)
    }
    return Boolean((claudeStore.itemsBySession?.[id] || []).length || id)
  }
  if (isGrokMode.value) {
    if (grokStore.sameSession(id, grokStore.activeSessionId)) {
      return grokStore.activeItems.length > 0 || Boolean(id)
    }
    return Boolean(id)
  }
  const key = matchingCodexThreadKey(codexStore.itemsByThread, id)
  return Boolean((key && codexStore.itemsByThread[key]?.length) || id)
})

watch(
  [hasConversation, paneSessionId],
  ([hasItems, threadId]) => {
    if (!hasItems && !threadId) welcomeEpoch.value += 1
  },
)

const paneWorkspacePath = computed(() => {
  const id = paneSessionId.value
  if (isGrokMode.value) {
    return grokStore.sessions.find((session) => grokStore.sameSession(session.id, id))?.workspace
      || (!id ? grokStore.workspacePath : '')
  }
  if (isClaudeMode.value) {
    return claudeStore.sessions.find((session) => claudeStore.sameSession(session.id, id))?.workspace
      || (!id ? claudeStore.workspacePath : '')
  }
  return codexStore.threads.find((thread) => codexStore.sameThread(thread.id, id))?.cwd
    || Object.values(codexStore.projectThreads).flat()
      .find((thread) => codexStore.sameThread(thread.id, id))?.cwd
    || (!id ? activeComposerContext.value.workspace : '')
})
const paneWorkspaceIsCurrent = computed(() => Boolean(
  paneWorkspacePath.value
  && workspaceStore.currentPath
  && sameWorkspacePath(paneWorkspacePath.value, workspaceStore.currentPath),
))
const workspaceTag = computed(() => {
  if (paneWorkspaceIsCurrent.value && workspaceStore.workspace?.name) {
    return workspaceStore.workspace.name
  }
  const clean = paneWorkspacePath.value.replace(/[\\/]+$/, '')
  return clean.split(/[\\/]/).filter(Boolean).at(-1) || ''
})
const branchLabel = computed(() => paneWorkspaceIsCurrent.value
  ? (workspaceStore.branch || 'detached')
  : '')
const changesCount = computed(() => paneWorkspaceIsCurrent.value ? workspaceStore.changes.length : 0)
const paneWorkspaceSwitching = computed(() => paneIsFocused.value && workspaceStore.switchingWorkspace)

function useSuggestion(prompt: string): void {
  draft.value = prompt
}

function onMessageSent(): void {
  messageSentEpoch.value += 1
}

function onRetry(itemID: string): void {
  if (!isCodexMode.value) return
  const threadID = paneSessionId.value
  const key = matchingCodexThreadKey(codexStore.itemsByThread, threadID)
  const item = ((key && codexStore.itemsByThread[key]) || []).find((candidate) => candidate.id === itemID)
  if (!item?.text) return
  void codexStore.retryMessage(itemID, item.text, threadID)
}

function onRollback(payload: { turnId: string; mode: 'single' | 'fromHere' }): void {
  if (!isCodexMode.value) return
  void codexStore.rollbackToTurn(payload.turnId, payload.mode, paneSessionId.value)
}

function onInspectDiff(payload: { path: string; diff: string }): void {
  workspaceStore.inspectInlineDiff(payload.path, payload.diff)
}

function openFullDiff(): void {
  const threadID = paneSessionId.value
  const key = matchingCodexThreadKey(codexStore.latestDiffByThread, threadID)
  const live = key ? (codexStore.latestDiffByThread[key] || '') : ''
  if (live.trim()) {
    workspaceStore.openLiveTurnDiff(live)
    return
  }
  const first = workspaceStore.changes[0]
  if (first?.path) {
    void workspaceStore.inspectWorkspaceDiff(first.path)
    return
  }
  emit('show-inspector')
}

function onResolveApproval(action: 'once' | 'session' | 'deny' | 'cancel'): void {
  const requestKey = panePendingRequest.value?.requestKey
  if (requestKey) void codexStore.resolveApproval(action, requestKey)
}

function onAnswer(answers: Record<string, string[]>): void {
  const requestKey = panePendingRequest.value?.requestKey
  if (requestKey) void codexStore.resolveUserInput(answers, requestKey)
}

function onMcpSubmit(action: 'accept' | 'decline' | 'cancel', content: Record<string, unknown> | null): void {
  const requestKey = panePendingRequest.value?.requestKey
  if (requestKey) void codexStore.resolveMcpElicitation(action, content, requestKey)
}

function onCancelUserInput(): void {
  const requestKey = panePendingRequest.value?.requestKey
  if (requestKey) void codexStore.resolveUserInput({}, requestKey)
}

function onOpenUrl(url: string): void {
  void browserStore.openBrowser(url)
}

function sessionExists(runtime: ComposerRuntime, sessionId: string): boolean {
  if (runtime === 'grok') return grokStore.sessions.some((item) => grokStore.sameSession(item.id, sessionId))
  if (runtime === 'claude') return claudeStore.sessions.some((item) => claudeStore.sameSession(item.id, sessionId))
  return codexStore.threads.some((item) => codexStore.sameThread(item.id, sessionId))
    || Object.values(codexStore.projectThreads).flat().some((item) => codexStore.sameThread(item.id, sessionId))
}

function clearRemovedArenaSession(
  runtime: ComposerRuntime,
  arena: boolean,
  sessionId: string,
  resolvedId: string,
): void {
  if (!arena || sessionExists(runtime, resolvedId || sessionId)) return
  arenaStore.clearSessionBindings(runtime, [sessionId, resolvedId])
}

async function archiveThread(): Promise<void> {
  const sessionId = paneSessionId.value
  if (!sessionId) return
  const runtime = paneRuntime.value as ComposerRuntime
  const arena = isArenaPane.value
  const resolvedId = runtime === 'grok'
    ? grokStore.resolveSessionId(sessionId)
    : runtime === 'claude'
      ? claudeStore.resolveSessionId(sessionId)
      : codexStore.resolveThreadID(sessionId)
  if (runtime === 'grok') {
    await grokStore.archiveSession(sessionId)
    clearRemovedArenaSession(runtime, arena, sessionId, resolvedId)
    return
  }
  if (runtime === 'claude') {
    await claudeStore.archiveSession(sessionId)
    clearRemovedArenaSession(runtime, arena, sessionId, resolvedId)
    return
  }
  await codexStore.archiveThread(sessionId)
  clearRemovedArenaSession(runtime, arena, sessionId, resolvedId)
}

function compactThread(): void {
  if (paneSessionId.value) void codexStore.compactThread(paneSessionId.value, !isArenaPane.value)
}

async function forkThread(): Promise<void> {
  const sessionId = paneSessionId.value
  if (!sessionId) return
  const arena = isArenaPane.value
  const targetPaneId = paneId.value
  const runtime = paneRuntime.value
  const thread = await codexStore.forkThread(sessionId, !arena)
  const pane = arenaStore.panes.find((item) => item.id === targetPaneId)
  if (
    arena
    && thread?.id
    && arenaStore.isArenaMode
    && pane?.runtime === runtime
    && codexStore.sameThread(arenaStore.sessionForPane(targetPaneId), sessionId)
  ) arenaStore.selectPaneSession(targetPaneId, thread.id)
}

function renameThread(): void {
  const sessionId = paneSessionId.value
  if (!sessionId) return
  if (isGrokMode.value) {
    void grokStore.renameSession(sessionId)
    return
  }
  if (isClaudeMode.value) {
    void claudeStore.renameSession(sessionId)
    return
  }
  void codexStore.renameThread(sessionId)
}

async function deleteThread(): Promise<void> {
  const sessionId = paneSessionId.value
  if (!sessionId) return
  const runtime = paneRuntime.value as ComposerRuntime
  const arena = isArenaPane.value
  const resolvedId = runtime === 'grok'
    ? grokStore.resolveSessionId(sessionId)
    : runtime === 'claude'
      ? claudeStore.resolveSessionId(sessionId)
      : codexStore.resolveThreadID(sessionId)
  if (runtime === 'grok') {
    await grokStore.deleteSession(sessionId)
    clearRemovedArenaSession(runtime, arena, sessionId, resolvedId)
    return
  }
  if (runtime === 'claude') {
    await claudeStore.deleteSession(sessionId)
    clearRemovedArenaSession(runtime, arena, sessionId, resolvedId)
    return
  }
  await codexStore.deleteThread(sessionId)
  clearRemovedArenaSession(runtime, arena, sessionId, resolvedId)
}

const activeSessionTitle = computed(() => {
  const id = paneSessionId.value
  if (!id) return ''
  if (isGrokMode.value) {
    const session = grokStore.sessions.find((item) => grokStore.sameSession(item.id, id))
    return session?.name || id
  }
  if (isClaudeMode.value) {
    const session = claudeStore.sessions.find((item) => claudeStore.sameSession(item.id, id))
    return session?.name || id
  }
  return codexStore.threads.find((item) => codexStore.sameThread(item.id, id))?.name
    || Object.values(codexStore.projectThreads || {}).flat().find((item) => codexStore.sameThread(item.id, id))?.name
    || id
})

const paneOwnsActiveCodexThread = computed(() => Boolean(
  paneSessionId.value
  && codexStore.activeThreadId
  && codexStore.sameThread(paneSessionId.value, codexStore.activeThreadId)
  && appStore.activeRuntime === 'codex',
))
const paneOwnsPlanPrompt = computed(() => Boolean(
  paneSessionId.value
  && codexStore.planImplementPrompt?.threadId
  && codexStore.sameThread(paneSessionId.value, codexStore.planImplementPrompt.threadId),
))

function reviewChanges(): void {
  void codexStore.startReview(
    { targetType: 'uncommittedChanges', delivery: 'inline' },
    paneSessionId.value,
  )
}

function commitFromBar(): void {
  void (async () => {
    const message = await dialogStore.prompt({
      title: t('settings.gitCommit'),
      description: t('settings.gitCommitMessagePlaceholder'),
      placeholder: t('settings.gitCommitMessagePlaceholder'),
      confirmLabel: t('settings.gitCommit'),
      maxlength: 400,
    })
    if (!message?.trim()) return
    await workspaceStore.commitChanges(message.trim())
  })()
}

</script>

<template>
  <div class="relative flex h-full min-h-0 flex-col overflow-hidden">
    <div
      v-if="usesCodexTimeline && paneIsFocused && codexStore.creatingThread"
      class="pointer-events-none absolute inset-x-0 top-2 z-20 flex justify-center"
    >
      <div class="flex items-center gap-2 rounded-full border bg-card/95 px-3 py-1 text-[11px] text-muted-foreground shadow-sm backdrop-blur">
        <LoaderCircle :size="12" class="animate-spin" />
        {{ t('common.loading') }}
      </div>
    </div>

    <div
      v-if="!hideChrome && (paneSessionId || paneWorkspaceSwitching)"
      class="flex h-9 shrink-0 items-center justify-between border-b border-border/70 px-4"
    >
      <div class="flex min-w-0 items-center gap-2">
        <div
          v-if="paneWorkspaceSwitching"
          class="flex items-center gap-1.5 text-[11px] text-muted-foreground"
        >
          <LoaderCircle :size="12" class="animate-spin" />
          {{ t('chat.switchingProject') }}
        </div>
        <template v-else>
          <span
            v-if="(isGrokMode || isClaudeMode) && activeSessionTitle"
            class="truncate text-[12px] font-medium text-foreground/90"
            :title="activeSessionTitle"
          >
            {{ activeSessionTitle }}
          </span>
          <span v-else-if="workspaceTag" class="truncate text-[12px] font-medium text-foreground/90">
            {{ workspaceTag }}
          </span>
          <Badge
            v-if="isGrokMode"
            variant="secondary"
            class="h-5 shrink-0 rounded-md px-1.5 text-[9px] font-normal"
          >
            Grok
          </Badge>
          <Badge
            v-else-if="isClaudeMode"
            variant="secondary"
            class="h-5 shrink-0 rounded-md px-1.5 text-[9px] font-normal"
          >
            Claude
          </Badge>
          <Badge
            v-else-if="isGeminiMode"
            variant="secondary"
            class="h-5 shrink-0 rounded-md px-1.5 text-[9px] font-normal"
          >
            Gemini
          </Badge>
          <Badge
            v-else-if="isOpenCodeMode"
            variant="secondary"
            class="h-5 shrink-0 rounded-md px-1.5 text-[9px] font-normal"
          >
            OpenCode
          </Badge>
          <div
            v-if="paneWorkspaceIsCurrent && workspaceStore.workspace"
            class="hidden items-center gap-1.5 text-[11px] text-muted-foreground sm:flex"
          >
            <GitBranch :size="11" />
            <span class="truncate">{{ branchLabel }}</span>
            <span v-if="changesCount" class="text-warning">
              · {{ changesCount }}
            </span>
          </div>
        </template>
      </div>

      <DropdownMenu
        v-if="paneSessionId"
      >
        <DropdownMenuTrigger as-child>
          <Button
            variant="ghost"
            size="icon-sm"
            class="size-7 text-muted-foreground"
            :aria-label="t('threadActions.title')"
            :disabled="paneSessionBusy"
          >
            <MoreHorizontal :size="15" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <template v-if="isCodexMode">
            <DropdownMenuItem @click="reviewChanges">
              <ScanSearch :size="14" class="mr-2" />
              {{ t('threadActions.review') }}
            </DropdownMenuItem>
          </template>
          <template v-if="usesCodexTimeline">
            <DropdownMenuItem @click="forkThread">
              <Copy :size="14" class="mr-2" />
              {{ t('threadActions.fork') }}
            </DropdownMenuItem>
          </template>
          <DropdownMenuItem @click="renameThread">
            <Pencil :size="14" class="mr-2" />
            {{ t('threadActions.rename') }}
          </DropdownMenuItem>
          <DropdownMenuItem v-if="isCodexMode" @click="compactThread">
            <Archive :size="14" class="mr-2" />
            {{ t('threadActions.compact') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            :disabled="paneSessionPending"
            @click="archiveThread"
          >
            <Archive :size="14" class="mr-2" />
            {{ t('threadActions.archive') }}
          </DropdownMenuItem>
          <DropdownMenuItem class="text-destructive" @click="deleteThread">
            <Trash2 :size="14" class="mr-2" />
            {{ t('threadActions.delete') }}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>

    <div class="min-h-0 flex-1 overflow-hidden">
      <WorkspaceWelcome
        v-if="!hasConversation"
        :key="`welcome-${paneRuntime}-${welcomeEpoch}`"
        @suggestion="useSuggestion"
      />
      <ChatTimeline
        v-else
        :sent-epoch="messageSentEpoch"
        @retry="onRetry"
        @rollback="onRollback"
        @inspect-diff="onInspectDiff"
      />
    </div>

    <div
      v-if="isCodexMode && paneOwnsActiveCodexThread && ((changesCount && codexStore.activeThread) || paneOwnsPlanPrompt)"
      class="border-t border-border/70 px-4 py-1.5"
    >
      <div class="mx-auto flex max-w-[680px] flex-col gap-1.5">
        <div
          v-if="changesCount && codexStore.activeThread"
          class="flex items-center justify-between gap-3"
        >
          <div class="flex min-w-0 items-center gap-2 text-[11px] text-muted-foreground">
            <FileDiff :size="13" class="shrink-0 text-warning" />
            <span class="truncate">{{ workspaceTag }}</span>
            <span class="hidden truncate sm:inline">{{ branchLabel }}</span>
            <Badge variant="secondary" class="h-5 shrink-0 rounded-md px-1.5 text-[9px] font-normal">
              {{ changesCount }}
            </Badge>
          </div>
          <div class="flex shrink-0 items-center gap-1">
            <Button
              variant="ghost"
              size="sm"
              class="h-7 px-2 text-[11px] text-muted-foreground"
              :disabled="codexStore.activeThreadBusy"
              @click="reviewChanges"
            >
              {{ t('chat.startReview') }}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              class="h-7 px-2 text-[11px] text-muted-foreground"
              @click="commitFromBar"
            >
              {{ t('settings.gitCommit') }}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              class="h-7 px-2 text-[11px] text-muted-foreground"
              @click="openFullDiff"
            >
              {{ t('inspector.viewDiff') }}
            </Button>
          </div>
        </div>

        <!-- Official Codex: after a plan turn, ask whether to implement -->
        <div
          v-if="paneOwnsPlanPrompt"
          class="flex flex-col gap-1.5 rounded-lg border border-primary/20 bg-primary/[0.04] px-3 py-2 sm:flex-row sm:items-center sm:justify-between"
        >
          <div class="min-w-0">
            <p class="text-[12px] font-medium text-foreground">{{ t('chat.planImplementTitle') }}</p>
            <p class="text-[10px] text-muted-foreground">{{ t('chat.planImplementHint') }}</p>
          </div>
          <div class="flex shrink-0 items-center gap-1">
            <Button
              variant="ghost"
              size="sm"
              class="h-7 px-2.5 text-[11px] text-muted-foreground"
              @click="codexStore.dismissPlanImplementation()"
            >
              {{ t('chat.planImplementNo') }}
            </Button>
            <Button
              size="sm"
              class="h-7 px-2.5 text-[11px]"
              :disabled="codexStore.activeThreadBusy"
              @click="codexStore.acceptPlanImplementation()"
            >
              {{ t('chat.planImplementYes') }}
            </Button>
          </div>
        </div>
      </div>
    </div>

    <ComposerPanel
      v-model="draft"
      v-model:images="draftImages"
      :draft-key="activeComposerContext.key"
      @sent="onMessageSent"
      @restore-draft="restoreComposerDraft"
      @append-draft-images="appendComposerDraftImages"
    />

    <ApprovalDialog
      :request="panePendingRequest"
      @resolve="onResolveApproval"
      @answer="onAnswer"
      @mcp-submit="onMcpSubmit"
      @open-url="onOpenUrl"
      @cancel="onCancelUserInput"
    />
  </div>
</template>
