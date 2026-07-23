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
import { useBrowserStore, useCodexStore, useDialogStore, useWorkspaceStore } from '@/stores'
import { Motion } from 'motion-v'

const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()
const browserStore = useBrowserStore()
const dialogStore = useDialogStore()
const { t } = useI18n()

const emit = defineEmits<{
  'show-inspector': []
}>()

const draft = shallowRef('')
const welcomeEpoch = shallowRef(0)
const hasConversation = computed(() => codexStore.activeItems.length > 0)

watch(
  [hasConversation, () => codexStore.activeThreadId],
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

function onRetry(itemID: string): void {
  const item = codexStore.activeItems.find((candidate) => candidate.id === itemID)
  if (!item?.text) return
  void codexStore.retryMessage(itemID, item.text)
}

function onRollback(payload: { turnId: string; mode: 'single' | 'fromHere' }): void {
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
  void codexStore.archiveActiveThread()
}

function compactThread(): void {
  void codexStore.compactActiveThread()
}

function forkThread(): void {
  void codexStore.forkActiveThread()
}

function renameThread(): void {
  void codexStore.renameActiveThread()
}

function deleteThread(): void {
  void codexStore.deleteActiveThread()
}

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

watch(() => codexStore.activeThreadId, () => {
  draft.value = ''
})
</script>

<template>
  <div class="relative flex h-full flex-col">
    <div
      v-if="codexStore.creatingThread"
      class="pointer-events-none absolute inset-x-0 top-2 z-20 flex justify-center"
    >
      <div class="flex items-center gap-2 rounded-full border bg-card/95 px-3 py-1 text-[11px] text-muted-foreground shadow-sm backdrop-blur">
        <LoaderCircle :size="12" class="animate-spin" />
        {{ t('common.loading') }}
      </div>
    </div>

    <div
      v-if="codexStore.activeThread || workspaceStore.switchingWorkspace"
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
          <span v-if="workspaceTag" class="truncate text-[12px] font-medium text-foreground/90">
            {{ workspaceTag }}
          </span>
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

      <DropdownMenu v-if="codexStore.activeThread">
        <DropdownMenuTrigger as-child>
          <Button
            variant="ghost"
            size="icon-sm"
            class="size-7 text-muted-foreground"
            :aria-label="t('threadActions.title')"
            :disabled="codexStore.activeThreadBusy"
          >
            <MoreHorizontal :size="15" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem @click="reviewChanges">
            <ScanSearch :size="14" class="mr-2" />
            {{ t('threadActions.review') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="forkThread">
            <Copy :size="14" class="mr-2" />
            {{ t('threadActions.fork') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="renameThread">
            <Pencil :size="14" class="mr-2" />
            {{ t('threadActions.rename') }}
          </DropdownMenuItem>
          <DropdownMenuItem @click="compactThread">
            <Archive :size="14" class="mr-2" />
            {{ t('threadActions.compact') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem @click="archiveThread">
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
        :key="codexStore.activeThreadId || (hasConversation ? 'conversation' : 'welcome')"
        class="h-full"
        :initial="{ opacity: 0, y: 8 }"
        :animate="{ opacity: 1, y: 0 }"
        :transition="easeOutQuick"
      >
        <WorkspaceWelcome
          v-if="!hasConversation && !codexStore.activeThread"
          :key="`welcome-${welcomeEpoch}`"
          @suggestion="useSuggestion"
        />
        <ChatTimeline
          v-else
          @retry="onRetry"
          @rollback="onRollback"
          @inspect-diff="onInspectDiff"
        />
      </Motion>
    </div>

    <div
      v-if="(changesCount && codexStore.activeThread) || codexStore.planImplementPrompt?.threadId === codexStore.activeThreadId"
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

    <ComposerPanel v-model="draft" />

    <ApprovalDialog
      :request="codexStore.pendingRequest"
      @resolve="onResolveApproval"
      @answer="onAnswer"
      @mcp-submit="onMcpSubmit"
      @open-url="onOpenUrl"
    />
  </div>
</template>
