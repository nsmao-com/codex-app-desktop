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
import { easeOutQuick } from '@/lib/motion'
import { useAppStore, useBrowserStore, useClaudeStore, useCodexStore, useDialogStore, useGrokStore, useWorkspaceStore } from '@/stores'
import { Motion } from 'motion-v'
import { workspaceKey } from '@/utils/workspacePath'

const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const workspaceStore = useWorkspaceStore()
const browserStore = useBrowserStore()
const dialogStore = useDialogStore()
const { t } = useI18n()

const emit = defineEmits<{
  'show-inspector': []
}>()

type ComposerRuntime = 'codex' | 'claude' | 'grok'
type ComposerDraft = { text: string; images: string[] }
type ComposerDraftContext = {
  key: string
  runtime: ComposerRuntime
  sessionId: string
  workspace: string
}

const composerDrafts = shallowRef<Record<string, ComposerDraft>>({})
const draftKeyAliases = new Map<string, string>()

const activeComposerContext = computed<ComposerDraftContext>(() => {
  const runtime = appStore.activeRuntime as ComposerRuntime
  const sessionId = runtime === 'grok'
    ? grokStore.activeSessionId
    : runtime === 'claude'
      ? claudeStore.activeSessionId
      : codexStore.activeThreadId
  const workspace = runtime === 'grok'
    ? grokStore.workspacePath
    : runtime === 'claude'
      ? claudeStore.workspacePath
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
    images: [...new Set([...source.images, ...(target?.images ?? [])])].slice(0, 4),
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
    images: [...new Set([...current.images, ...payload.images])].slice(0, 4),
  })
}

function appendComposerDraftImages(payload: { draftKey: string; images: string[] }): void {
  const key = resolveDraftKey(payload.draftKey)
  const current = composerDrafts.value[key] ?? { text: '', images: [] }
  setComposerDraft(key, {
    images: [...new Set([...current.images, ...payload.images])].slice(0, 4),
  })
}

const welcomeEpoch = shallowRef(0)
const messageSentEpoch = shallowRef(0)
const hasConversation = computed(() => {
  if (appStore.isClaudeMode) return claudeStore.activeItems.length > 0 || Boolean(claudeStore.activeSessionId)
  if (appStore.isGrokMode) return grokStore.activeItems.length > 0 || Boolean(grokStore.activeSessionId)
  return codexStore.activeItems.length > 0
})

watch(
  [hasConversation, () => (appStore.isGrokMode
    ? grokStore.activeSessionId
    : appStore.isClaudeMode
      ? claudeStore.activeSessionId
      : codexStore.activeThreadId)],
  ([hasItems, threadId]) => {
    if (!hasItems && !threadId) welcomeEpoch.value += 1
  },
)

const workspaceTag = computed(() => workspaceStore.workspace?.name || '')
const branchLabel = computed(() => workspaceStore.branch || 'detached')
const changesCount = computed(() => workspaceStore.changes.length)

function useSuggestion(prompt: string): void {
  draft.value = prompt
}

function onMessageSent(): void {
  messageSentEpoch.value += 1
}

function onRetry(itemID: string): void {
  if (!appStore.isCodexMode) return
  const item = codexStore.activeItems.find((candidate) => candidate.id === itemID)
  if (!item?.text) return
  void codexStore.retryMessage(itemID, item.text)
}

function onRollback(payload: { turnId: string; mode: 'single' | 'fromHere' }): void {
  if (!appStore.isCodexMode) return
  void codexStore.rollbackToTurn(payload.turnId, payload.mode)
}

function onInspectDiff(payload: { path: string; diff: string }): void {
  workspaceStore.inspectInlineDiff(payload.path, payload.diff)
}

function openFullDiff(): void {
  const threadID = codexStore.activeThreadId
  const live = threadID ? (codexStore.latestDiffByThread[threadID] || '') : ''
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
  void codexStore.resolveApproval(action)
}

function onAnswer(answers: Record<string, string[]>): void {
  void codexStore.resolveUserInput(answers)
}

function onMcpSubmit(action: 'accept' | 'decline' | 'cancel', content: Record<string, unknown> | null): void {
  void codexStore.resolveMcpElicitation(action, content)
}

function onOpenUrl(url: string): void {
  void browserStore.openBrowser(url)
}

function archiveThread(): void {
  if (appStore.isGrokMode) {
    void grokStore.archiveActiveSession()
    return
  }
  if (appStore.isClaudeMode) {
    void claudeStore.archiveActiveSession()
    return
  }
  void codexStore.archiveActiveThread()
}

function compactThread(): void {
  void codexStore.compactActiveThread()
}

function forkThread(): void {
  void codexStore.forkActiveThread()
}

function renameThread(): void {
  if (appStore.isGrokMode) {
    void grokStore.renameActiveSession()
    return
  }
  if (appStore.isClaudeMode) {
    void claudeStore.renameActiveSession()
    return
  }
  void codexStore.renameActiveThread()
}

function deleteThread(): void {
  if (appStore.isGrokMode) {
    void grokStore.deleteActiveSession()
    return
  }
  if (appStore.isClaudeMode) {
    void claudeStore.deleteActiveSession()
    return
  }
  void codexStore.deleteActiveThread()
}

const activeSessionTitle = computed(() => {
  if (appStore.isGrokMode) {
    const id = grokStore.activeSessionId
    const session = grokStore.sessions.find((item) => item.id === id)
    return session?.name || id || ''
  }
  if (appStore.isClaudeMode) {
    const id = claudeStore.activeSessionId
    const session = claudeStore.sessions.find((item) => item.id === id)
    return session?.name || id || ''
  }
  return codexStore.activeThread?.name || ''
})

function reviewChanges(): void {
  void codexStore.startReview({ targetType: 'uncommittedChanges', delivery: 'inline' })
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
  <div class="relative flex h-full flex-col">
    <div
      v-if="appStore.isCodexMode && codexStore.creatingThread"
      class="pointer-events-none absolute inset-x-0 top-2 z-20 flex justify-center"
    >
      <div class="flex items-center gap-2 rounded-full border bg-card/95 px-3 py-1 text-[11px] text-muted-foreground shadow-sm backdrop-blur">
        <LoaderCircle :size="12" class="animate-spin" />
        {{ t('common.loading') }}
      </div>
    </div>

    <div
      v-if="(appStore.isGrokMode
        ? grokStore.activeSessionId
        : appStore.isClaudeMode
          ? claudeStore.activeSessionId
          : codexStore.activeThread) || workspaceStore.switchingWorkspace"
      class="flex h-9 shrink-0 items-center justify-between border-b border-border/70 px-4"
    >
      <div class="flex min-w-0 items-center gap-2">
        <div
          v-if="workspaceStore.switchingWorkspace"
          class="flex items-center gap-1.5 text-[11px] text-muted-foreground"
        >
          <LoaderCircle :size="12" class="animate-spin" />
          {{ t('chat.switchingProject') }}
        </div>
        <template v-else>
          <span
            v-if="(appStore.isGrokMode || appStore.isClaudeMode) && activeSessionTitle"
            class="truncate text-[12px] font-medium text-foreground/90"
            :title="activeSessionTitle"
          >
            {{ activeSessionTitle }}
          </span>
          <span v-else-if="workspaceTag" class="truncate text-[12px] font-medium text-foreground/90">
            {{ workspaceTag }}
          </span>
          <Badge
            v-if="appStore.isGrokMode"
            variant="secondary"
            class="h-5 shrink-0 rounded-md px-1.5 text-[9px] font-normal"
          >
            Grok
          </Badge>
          <Badge
            v-else-if="appStore.isClaudeMode"
            variant="secondary"
            class="h-5 shrink-0 rounded-md px-1.5 text-[9px] font-normal"
          >
            Claude
          </Badge>
          <div
            v-if="workspaceStore.workspace"
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
        v-if="(appStore.isCodexMode && codexStore.activeThread)
          || (appStore.isGrokMode && grokStore.activeSessionId)
          || (appStore.isClaudeMode && claudeStore.activeSessionId)"
      >
        <DropdownMenuTrigger as-child>
          <Button
            variant="ghost"
            size="icon-sm"
            class="size-7 text-muted-foreground"
            :aria-label="t('threadActions.title')"
            :disabled="appStore.isGrokMode
              ? Boolean(grokStore.sessionMutation) || grokStore.isTurnRunning
              : appStore.isClaudeMode
                ? claudeStore.isTurnRunning
                : codexStore.activeThreadBusy"
          >
            <MoreHorizontal :size="15" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <template v-if="appStore.isCodexMode">
            <DropdownMenuItem @click="reviewChanges">
              <ScanSearch :size="14" class="mr-2" />
              {{ t('threadActions.review') }}
            </DropdownMenuItem>
            <DropdownMenuItem @click="forkThread">
              <Copy :size="14" class="mr-2" />
              {{ t('threadActions.fork') }}
            </DropdownMenuItem>
          </template>
          <DropdownMenuItem @click="renameThread">
            <Pencil :size="14" class="mr-2" />
            {{ t('threadActions.rename') }}
          </DropdownMenuItem>
          <DropdownMenuItem v-if="appStore.isCodexMode" @click="compactThread">
            <Archive :size="14" class="mr-2" />
            {{ t('threadActions.compact') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            :disabled="(appStore.isGrokMode && grokStore.activeSessionId.startsWith('pending-grok-'))
              || (appStore.isClaudeMode && claudeStore.activeSessionId.startsWith('pending-claude-'))"
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
      <Motion
        :key="(appStore.isGrokMode
          ? grokStore.activeSessionId
          : appStore.isClaudeMode
            ? claudeStore.activeSessionId
            : codexStore.activeThreadId) || (hasConversation ? 'conversation' : 'welcome')"
        class="h-full"
        :initial="{ opacity: 0, y: 8 }"
        :animate="{ opacity: 1, y: 0 }"
        :transition="easeOutQuick"
      >
        <WorkspaceWelcome
          v-if="!hasConversation && !(appStore.isGrokMode
            ? grokStore.activeSessionId
            : appStore.isClaudeMode
              ? claudeStore.activeSessionId
              : codexStore.activeThread)"
          :key="`welcome-${welcomeEpoch}`"
          @suggestion="useSuggestion"
        />
        <ChatTimeline
          v-else
          :sent-epoch="messageSentEpoch"
          @retry="onRetry"
          @rollback="onRollback"
          @inspect-diff="onInspectDiff"
        />
      </Motion>
    </div>

    <div
      v-if="appStore.isCodexMode && ((changesCount && codexStore.activeThread) || codexStore.planImplementPrompt?.threadId === codexStore.activeThreadId)"
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
          v-if="codexStore.planImplementPrompt?.threadId === codexStore.activeThreadId"
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
      :request="codexStore.pendingRequest"
      @resolve="onResolveApproval"
      @answer="onAnswer"
      @mcp-submit="onMcpSubmit"
      @open-url="onOpenUrl"
    />
  </div>
</template>
