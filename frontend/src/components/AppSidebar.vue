<script setup lang="ts">
import {
  Archive,
  Blocks,
  Bot,
  ChevronDown,
  ChevronRight,
  Coins,
  Copy,
  Folder,
  FolderOpen,
  LoaderCircle,
  LogIn,
  LogOut,
  MessageSquareText,
  MoreHorizontal,
  Pencil,
  Pin,
  Plus,
  RefreshCw,
  Search,
  Settings,
  SlidersHorizontal,
  Sparkles,
  Trash2,
  X,
} from '@lucide/vue'
import { Motion } from 'motion-v'
import { useRouter } from 'vue-router'
import { computed, nextTick, shallowRef, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'

import OpenAIIcon from '@/components/icons/OpenAIIcon.vue'
import { springPanel, springSnappy } from '@/lib/motion'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Progress } from '@/components/ui/progress'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useAppStore, useCodexStore, useWorkspaceStore } from '@/stores'
import type { ThreadGroup, ThreadSummary } from '@/types/codex'
import {
  buildUsageRangeView,
  formatTokenCount,
  formatUsageDateLabel,
  type UsageRangeDays,
} from '@/utils/accountUsage'

const router = useRouter()
const appStore = useAppStore()
const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()
const { locale, t } = useI18n()

const props = defineProps<{
  collapsed?: boolean
  mobile?: boolean
}>()

const emit = defineEmits<{
  'toggle-sidebar': []
}>()

const usagePopoverOpen = shallowRef(false)
const usageRangeDays = shallowRef<UsageRangeDays>(7)
const usageLoading = shallowRef(false)

const usageRanges = computed(() => ([
  { days: 1 as const, label: t('sidebar.usageToday') },
  { days: 7 as const, label: t('sidebar.usageWeek') },
  { days: 14 as const, label: t('sidebar.usageTwoWeeks') },
  { days: 30 as const, label: t('sidebar.usageMonth') },
]))

const usageRangeView = computed(() => buildUsageRangeView(appStore.accountUsage, usageRangeDays.value))
const usageLocale = computed(() => (locale.value === 'zh-CN' ? 'zh-CN' : 'en-US'))

watch(usagePopoverOpen, (open) => {
  if (!open || !appStore.account.authenticated) return
  usageLoading.value = true
  // Refresh account first so Codex session is warm, then read usage (may seed from cloud).
  void appStore.refreshAccountData()
    .catch(() => undefined)
    .then(() => appStore.loadLocalUsage())
    .finally(() => {
      usageLoading.value = false
    })
})

const sidebarMotion = computed(() => {
  if (props.mobile) {
    return {
      width: 276,
      x: props.collapsed ? -288 : 0,
      opacity: props.collapsed ? 0 : 1,
    }
  }
  return {
    width: props.collapsed ? 0 : 276,
    x: 0,
    opacity: props.collapsed ? 0 : 1,
  }
})

const search = computed({
  get: () => codexStore.threadSearch,
  set: (value) => codexStore.setSearch(value),
})

const threadCount = computed(() => codexStore.filteredThreadGroups.reduce((total, group) => total + group.threads.length, 0))
const groups = computed(() => codexStore.filteredThreadGroups)
const archivedThreads = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  const list = codexStore.archivedThreads
  if (!query) return list
  return list.filter((thread) => `${thread.name} ${thread.preview}`.toLocaleLowerCase().includes(query))
})
const archivedOpen = shallowRef(false)
const creatingInProject = shallowRef('')
const renamingThreadId = shallowRef('')
const renameDraft = shallowRef('')

function formatUpdated(timestamp: number): string {
  if (!timestamp) return ''
  const difference = Date.now() - timestamp * 1000
  const minutes = Math.floor(difference / 60_000)
  if (minutes < 1) return t('sidebar.now')
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  return new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric' }).format(timestamp * 1000)
}

function workspaceName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path
}

function openSettings(): void {
  router.push('/settings')
}

function openCapabilities(): void {
  router.push('/capabilities')
}

async function setWorkMode(mode: 'code' | 'cowork'): Promise<void> {
  if (appStore.settings.workMode === mode) return
  const previous = { ...appStore.settings }
  const next = {
    ...appStore.settings,
    workMode: mode,
    collaborationMode: mode === 'cowork' && appStore.settings.collaborationMode === 'default'
      ? 'plan'
      : appStore.settings.collaborationMode,
  }
  appStore.settings = next
  try {
    await appStore.savePreferences(next, { silent: true })
    await codexStore.switchWorkMode()
  } catch {
    appStore.settings = previous
    await appStore.savePreferences(previous, { silent: true }).catch(() => undefined)
  }
}

function openThread(group: ThreadGroup, thread: ThreadSummary): void {
  if (group.active) {
    codexStore.openThread(thread.id)
  } else {
    codexStore.openProjectThread(group.path, thread.id)
  }
}

function switchWorkspace(path: string): void {
  void codexStore.switchProject(path)
}

function restoreArchived(threadID: string): void {
  void codexStore.unarchiveThread(threadID)
}

function archiveThread(threadID: string, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  void codexStore.archiveThread(threadID)
}

function deleteThread(threadID: string, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  void codexStore.deleteThread(threadID)
}

function beginRename(thread: ThreadSummary, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  renamingThreadId.value = thread.id
  renameDraft.value = thread.name || ''
  void nextTick(() => {
    const input = document.querySelector<HTMLInputElement>('[data-thread-rename-input]')
    input?.focus()
    input?.select()
  })
}

async function commitRename(thread: ThreadSummary): Promise<void> {
  if (renamingThreadId.value !== thread.id) return
  const next = renameDraft.value.trim()
  renamingThreadId.value = ''
  if (!next || next === thread.name) return
  await codexStore.renameThread(thread.id, next)
}

function cancelRename(): void {
  renamingThreadId.value = ''
  renameDraft.value = ''
}

function forkThread(threadID: string, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  void codexStore.forkThread(threadID)
}

function setArchivedOpen(open: boolean): void {
  archivedOpen.value = open
}

async function newInProject(group: ThreadGroup, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  if (creatingInProject.value || codexStore.creatingThread) return
  creatingInProject.value = group.path
  try {
    // Expand the project so the new draft is visible.
    setGroupCollapsed(group, false)
    await codexStore.newThreadInProject(group.path)
  } finally {
    creatingInProject.value = ''
  }
}

function togglePin(thread: ThreadSummary, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  codexStore.toggleThreadPin(thread.id)
}

function providerIcon(thread: ThreadSummary): Component {
  const provider = `${thread.modelProvider} ${thread.model}`.toLocaleLowerCase()
  if (provider.includes('anthropic') || provider.includes('claude')) return Bot
  if (provider.includes('google') || provider.includes('gemini')) return Sparkles
  if (provider.includes('xai') || provider.includes('grok')) return Blocks
  return OpenAIIcon
}

const isCollapsed = shallowRef<Record<string, boolean>>({})
const visibleCounts = shallowRef<Record<string, number>>({})

function isGroupCollapsed(group: ThreadGroup): boolean {
  if (search.value) return false
  return isCollapsed.value[group.path] ?? !group.active
}

function setGroupCollapsed(group: ThreadGroup, collapsed: boolean): void {
  isCollapsed.value = {
    ...isCollapsed.value,
    [group.path]: collapsed,
  }
}

function visibleThreads(group: ThreadGroup): ThreadSummary[] {
  const defaultLimit = group.active ? 40 : 20
  const limit = visibleCounts.value[group.path] ?? defaultLimit
  return search.value ? group.threads : group.threads.slice(0, limit)
}

function loadMore(group: ThreadGroup): void {
  const current = visibleCounts.value[group.path] ?? (group.active ? 40 : 20)
  visibleCounts.value = { ...visibleCounts.value, [group.path]: current + 30 }
}
</script>

<template>
  <Motion
    as="aside"
    class="flex h-full shrink-0 flex-col overflow-hidden bg-transparent max-md:absolute max-md:inset-y-0 max-md:left-0 max-md:z-40 max-md:bg-sidebar max-md:shadow-xl max-md:backdrop-blur-md"
    :class="collapsed ? 'pointer-events-none' : 'pointer-events-auto'"
    :initial="false"
    :animate="sidebarMotion"
    :transition="springPanel"
  >
    <div class="flex h-full w-[276px] flex-col">
    <div class="flex h-12 items-center justify-between px-3.5">
      <div class="flex min-w-0 items-center gap-2.5">
        <div class="grid size-6 place-items-center rounded-lg bg-foreground/90 text-[10px] font-bold text-background shadow-sm">N</div>
        <div class="min-w-0">
          <div class="flex items-center gap-1.5">
            <span class="text-[13px] font-semibold tracking-tight">Nice Codex</span>
            <span class="rounded-md bg-muted/80 px-1 py-0.5 font-mono text-[9px] tabular-nums text-muted-foreground">v{{ appStore.appVersion }}</span>
          </div>
          <button
            v-if="appStore.updateInfo?.updateAvailable"
            type="button"
            class="text-[10px] text-primary hover:underline"
            @click="appStore.openUpdateCheckDialog"
          >
            {{ t('updates.availableShort', { version: appStore.updateInfo.latestVersion }) }}
          </button>
        </div>
      </div>
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger as-child>
            <Button variant="ghost" size="icon-xs" class="rounded-md" :aria-label="t('sidebar.toggle')" @click="emit('toggle-sidebar')">
              <X :size="14" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{{ t('sidebar.toggle') }}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    </div>

    <div class="space-y-2 px-3 pb-1">
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button
            variant="ghost"
            class="h-8 w-full justify-between rounded-lg px-2 text-xs hover:bg-sidebar-accent/70"
            :disabled="workspaceStore.switchingWorkspace"
          >
            <span class="flex min-w-0 items-center gap-2">
              <Folder :size="14" class="opacity-70" />
              <span class="truncate">{{ workspaceStore.workspace?.name || t('sidebar.chooseFolder') }}</span>
            </span>
            <ChevronDown :size="13" class="opacity-50" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" class="w-60">
          <DropdownMenuItem :disabled="workspaceStore.switchingWorkspace" @click="codexStore.selectProject()">
            <FolderOpen :size="14" class="mr-2" />
            {{ t('sidebar.chooseAnother') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            v-for="path in appStore.settings.recentWorkspaces"
            :key="path"
            :disabled="workspaceStore.switchingWorkspace"
            @click="switchWorkspace(path)"
          >
            <Folder :size="14" class="mr-2" />
            <span class="truncate">{{ workspaceName(path) }}</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <div class="relative grid grid-cols-2 gap-0.5 rounded-lg bg-foreground/[0.08] p-0.5 ring-1 ring-foreground/[0.06] dark:bg-white/10 dark:ring-white/10">
        <Motion
          class="absolute inset-y-0.5 w-[calc(50%-2px)] rounded-md bg-background shadow-sm dark:bg-card"
          :initial="false"
          :animate="{ x: appStore.settings.workMode === 'cowork' ? '100%' : '0%' }"
          :style="{ left: '2px' }"
          :transition="springSnappy"
        />
        <Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-7 rounded-md text-[11px] hover:bg-transparent"
          :class="appStore.settings.workMode !== 'cowork'
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          @click="void setWorkMode('code')"
        >
          {{ t('sidebar.code') }}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-7 rounded-md text-[11px] hover:bg-transparent"
          :class="appStore.settings.workMode === 'cowork'
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          @click="void setWorkMode('cowork')"
        >
          {{ t('sidebar.cowork') }}
        </Button>
      </div>

      <Motion
        as="div"
        class="w-full"
        :whileHover="{ scale: 1.015 }"
        :whilePress="{ scale: 0.98 }"
        :transition="springSnappy"
      >
        <Button
          class="h-9 w-full justify-start rounded-lg bg-primary px-2.5 text-xs text-white shadow-sm hover:bg-primary/90 hover:text-white disabled:text-white disabled:[&_svg]:text-white [&_svg]:text-white"
          :disabled="!codexStore.isReady || codexStore.creatingThread"
          @click="void codexStore.newThread()"
        >
          <LoaderCircle v-if="codexStore.creatingThread" :size="14" class="mr-1.5 animate-spin" />
          <Plus v-else :size="14" class="mr-1.5" />
          {{ codexStore.creatingThread ? t('common.loading') : t('sidebar.newTask') }}
        </Button>
      </Motion>

      <Button
        variant="ghost"
        class="h-8 w-full justify-start rounded-lg px-2 text-xs text-muted-foreground hover:bg-sidebar-accent/60"
        @click="openCapabilities"
      >
        <SlidersHorizontal :size="14" class="mr-2 opacity-70" />
        {{ t('sidebar.customize') }}
      </Button>
    </div>

    <div class="flex items-center justify-between px-4 pb-1 pt-3">
      <span class="text-[10px] font-medium uppercase tracking-[0.06em] text-muted-foreground/80">{{ t('sidebar.recents') }}</span>
      <span class="rounded-full bg-muted/70 px-1.5 py-0.5 text-[10px] tabular-nums text-muted-foreground">{{ threadCount }}</span>
    </div>

    <div class="px-3 pb-2">
      <div class="relative">
        <Search class="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground/70" />
        <Input
          v-model="search"
          type="search"
          :placeholder="t('sidebar.searchPlaceholder')"
          class="h-8 rounded-lg border-transparent bg-muted/40 pl-8 text-xs shadow-none focus-visible:border-transparent focus-visible:bg-background"
        />
      </div>
    </div>

    <ScrollArea class="min-h-0 flex-1 px-2">
      <div class="space-y-1.5 pb-3">
        <Collapsible
          v-for="group in groups"
          :key="group.path"
          :open="!isGroupCollapsed(group)"
          @update:open="(open) => setGroupCollapsed(group, !open)"
        >
          <div
            class="group/project flex items-center gap-0.5 rounded-lg px-0.5 transition-colors"
            :class="group.active ? 'bg-sidebar-accent/40' : 'hover:bg-sidebar-accent/25'"
          >
            <CollapsibleTrigger as-child>
              <button
                type="button"
                class="flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-md px-1.5 text-left text-[11.5px] font-medium text-muted-foreground transition-colors hover:text-foreground"
              >
                <ChevronRight
                  :size="12"
                  class="shrink-0 opacity-50 transition-transform duration-200"
                  :class="{ 'rotate-90': !isGroupCollapsed(group) }"
                />
                <FolderOpen v-if="group.active" :size="13" class="shrink-0 text-foreground/70" />
                <Folder v-else :size="13" class="shrink-0 opacity-60" />
                <span class="min-w-0 truncate" :class="group.active ? 'text-foreground' : ''">{{ group.name }}</span>
              </button>
            </CollapsibleTrigger>

            <span class="shrink-0 px-1 text-[10px] tabular-nums text-muted-foreground/80">
              <span v-if="group.loading" class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
              <span v-else>{{ group.threads.length }}</span>
            </span>

            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    class="size-6 shrink-0 rounded-md opacity-0 transition-opacity group-hover/project:opacity-100 focus-visible:opacity-100"
                    :class="creatingInProject === group.path ? 'opacity-100' : ''"
                    :disabled="Boolean(creatingInProject) || codexStore.creatingThread || workspaceStore.switchingWorkspace"
                    :aria-label="t('sidebar.newTaskInProject')"
                    @click="(event: MouseEvent) => void newInProject(group, event)"
                  >
                    <LoaderCircle v-if="creatingInProject === group.path" :size="12" class="animate-spin" />
                    <Plus v-else :size="12" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side="top">{{ t('sidebar.newTaskInProject') }}</TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </div>

          <CollapsibleContent>
            <div class="ml-2 space-y-0.5 border-l border-sidebar-border/60 py-0.5 pl-2">
              <div v-if="group.error" class="rounded-lg bg-destructive/10 p-2 text-[10px] text-destructive">
                {{ group.error }}
                <Button size="xs" variant="ghost" class="mt-1 h-6" @click="codexStore.reloadProject(group.path)">
                  <RefreshCw :size="11" class="mr-1" />
                  {{ t('sidebar.retryProject') }}
                </Button>
              </div>

              <div
                v-for="thread in visibleThreads(group)"
                :key="thread.id"
                class="group/thread relative"
              >
                <Motion
                  as="div"
                  role="button"
                  tabindex="0"
                  class="flex h-auto min-h-11 w-full cursor-pointer items-start gap-2 rounded-lg px-2 py-1.5 text-left text-xs transition-colors"
                  :class="group.active && thread.id === codexStore.activeThreadId
                    ? 'bg-accent text-accent-foreground shadow-sm'
                    : 'hover:bg-sidebar-accent/50'"
                  :whileHover="{ x: 2 }"
                  :whilePress="{ scale: 0.985 }"
                  :transition="springSnappy"
                  @click="renamingThreadId === thread.id ? undefined : openThread(group, thread)"
                  @dblclick.stop="beginRename(thread)"
                  @keydown.enter.prevent="renamingThreadId === thread.id ? undefined : openThread(group, thread)"
                >
                  <span
                    class="relative mt-0.5 grid size-5 shrink-0 place-items-center rounded-md border border-border/60 bg-panel/80 text-muted-foreground"
                    :title="thread.modelProvider || 'Codex / OpenAI'"
                  >
                    <component :is="providerIcon(thread)" :size="11" />
                    <span
                      v-if="codexStore.runningThreadIds.includes(thread.id)"
                      class="absolute -bottom-0.5 -right-0.5 size-1.5 rounded-full border-2 border-sidebar bg-emerald-500"
                    />
                  </span>

                  <span class="min-w-0 flex-1">
                    <span class="flex min-w-0 items-center gap-1 pr-1">
                      <Pin
                        v-if="codexStore.pinnedThreadIds.includes(thread.id)"
                        :size="10"
                        class="shrink-0 fill-current opacity-70"
                      />
                      <Input
                        v-if="renamingThreadId === thread.id"
                        data-thread-rename-input
                        v-model="renameDraft"
                        class="h-6 rounded-md px-1.5 text-[11px] font-medium"
                        maxlength="80"
                        :aria-label="t('threadActions.rename')"
                        @click.stop
                        @keydown.enter.prevent="commitRename(thread)"
                        @keydown.esc.prevent="cancelRename"
                        @blur="commitRename(thread)"
                      />
                      <span
                        v-else
                        class="min-w-0 flex-1 truncate font-medium leading-5"
                        :title="t('sidebar.renameHint')"
                      >{{ thread.name }}</span>
                    </span>
                    <span
                      v-if="renamingThreadId !== thread.id"
                      class="mt-0.5 block truncate text-[10px] leading-4 text-muted-foreground"
                      :class="{ 'text-accent-foreground/70': group.active && thread.id === codexStore.activeThreadId }"
                    >
                      {{ thread.model || thread.preview || t('sidebar.noPreview') }}
                    </span>
                  </span>

                  <span
                    v-if="renamingThreadId !== thread.id"
                    class="relative mt-0.5 flex h-5 w-9 shrink-0 items-center justify-end"
                    :class="{ 'text-accent-foreground/70': group.active && thread.id === codexStore.activeThreadId }"
                  >
                    <span
                      v-if="codexStore.loadingThreadId === thread.id"
                      class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent"
                    />
                    <span
                      v-else
                      class="text-[10px] tabular-nums text-muted-foreground transition-opacity group-hover/thread:opacity-0 group-focus-within/thread:opacity-0"
                    >
                      {{ formatUpdated(thread.updatedAt) }}
                    </span>
                  </span>
                </Motion>

                <DropdownMenu>
                  <DropdownMenuTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="absolute right-1.5 top-1.5 size-6 rounded-md text-muted-foreground opacity-0 transition-opacity group-hover/thread:opacity-100 group-focus-within/thread:opacity-100 data-[state=open]:opacity-100 focus-visible:opacity-100"
                      :aria-label="t('threadActions.title')"
                      :disabled="Boolean(codexStore.threadMutation) || renamingThreadId === thread.id"
                      @click.stop
                    >
                      <MoreHorizontal :size="13" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" class="min-w-40">
                    <DropdownMenuItem @click="(event: Event) => beginRename(thread, event)">
                      <Pencil :size="14" class="mr-2" />
                      {{ t('threadActions.rename') }}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      :disabled="Boolean(codexStore.threadMutation) || thread.id.startsWith('pending-thread-')"
                      @click="(event: Event) => forkThread(thread.id, event)"
                    >
                      <Copy :size="14" class="mr-2" />
                      {{ t('threadActions.fork') }}
                    </DropdownMenuItem>
                    <DropdownMenuItem @click="(event: Event) => togglePin(thread, event)">
                      <Pin :size="14" class="mr-2" :class="codexStore.pinnedThreadIds.includes(thread.id) ? 'fill-current' : ''" />
                      {{ codexStore.pinnedThreadIds.includes(thread.id) ? t('sidebar.unpin') : t('sidebar.pin') }}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      :disabled="Boolean(codexStore.threadMutation)"
                      @click="(event: Event) => archiveThread(thread.id, event)"
                    >
                      <Archive :size="14" class="mr-2" />
                      {{ t('threadActions.archive') }}
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem
                      class="text-destructive focus:text-destructive"
                      :disabled="Boolean(codexStore.threadMutation)"
                      @click="(event: Event) => deleteThread(thread.id, event)"
                    >
                      <Trash2 :size="14" class="mr-2" />
                      {{ t('threadActions.delete') }}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>

              <Button
                v-if="visibleThreads(group).length < group.threads.length"
                variant="ghost"
                size="sm"
                class="h-7 w-full justify-start rounded-md px-2 text-[10px] text-muted-foreground"
                @click="loadMore(group)"
              >
                {{ t('sidebar.loadMore', { count: group.threads.length - visibleThreads(group).length }) }}
              </Button>

              <div v-if="!group.loading && !group.error && group.threads.length === 0" class="px-2 py-2 text-[10px] text-muted-foreground">
                {{ t('sidebar.firstTask') }}
              </div>
            </div>
          </CollapsibleContent>
        </Collapsible>

        <div v-if="groups.length === 0" class="flex flex-col items-center gap-2 px-4 py-10 text-center text-[11px] text-muted-foreground">
          <div class="grid size-10 place-items-center rounded-full bg-muted/60">
            <MessageSquareText :size="16" class="opacity-70" />
          </div>
          <p>{{ search ? t('sidebar.noSearchResults') : t('sidebar.firstTask') }}</p>
          <p v-if="!search" class="max-w-[200px] text-[10px] leading-4 text-muted-foreground/80">
            {{
              appStore.settings.workMode === 'cowork'
                ? t('sidebar.switchToCodeHint')
                : t('sidebar.switchToCoworkHint')
            }}
          </p>
          <Button
            v-if="!search"
            type="button"
            variant="outline"
            class="h-7 rounded-md px-2.5 text-[11px]"
            @click="void setWorkMode(appStore.settings.workMode === 'cowork' ? 'code' : 'cowork')"
          >
            {{ appStore.settings.workMode === 'cowork' ? t('sidebar.code') : t('sidebar.cowork') }}
          </Button>
        </div>

        <Collapsible
          :open="archivedOpen"
          class="mt-2"
          @update:open="setArchivedOpen"
        >
          <CollapsibleTrigger as-child>
            <Button variant="ghost" class="h-8 w-full justify-between rounded-lg px-2 text-[11px] text-muted-foreground">
              <span class="flex items-center gap-1.5">
                <Archive :size="12" />
                {{ t('sidebar.archived') }}
                <span class="rounded-full bg-muted/70 px-1.5 text-[10px]">{{ archivedThreads.length }}</span>
              </span>
              <ChevronDown v-if="archivedOpen" :size="12" />
              <ChevronRight v-else :size="12" />
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div class="space-y-0.5 px-1 pb-1">
              <div
                v-for="thread in archivedThreads"
                :key="thread.id"
                class="group/archived flex items-center gap-1 rounded-lg px-1 py-1.5 hover:bg-muted/40"
              >
                <div class="min-w-0 flex-1 px-1">
                  <p class="truncate text-[11px]">{{ thread.name }}</p>
                  <p class="truncate text-[10px] text-muted-foreground">{{ thread.preview || t('sidebar.noPreview') }}</p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  class="h-6 shrink-0 rounded-md px-2 text-[10px]"
                  :disabled="Boolean(codexStore.threadMutation)"
                  @click="restoreArchived(thread.id)"
                >
                  {{ t('sidebar.restore') }}
                </Button>
                <DropdownMenu>
                  <DropdownMenuTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 shrink-0 rounded-md text-muted-foreground"
                      :aria-label="t('threadActions.title')"
                      :disabled="Boolean(codexStore.threadMutation)"
                      @click.stop
                    >
                      <MoreHorizontal :size="12" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" class="min-w-36">
                    <DropdownMenuItem
                      :disabled="Boolean(codexStore.threadMutation)"
                      @click="restoreArchived(thread.id)"
                    >
                      {{ t('threadActions.unarchive') }}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      class="text-destructive focus:text-destructive"
                      :disabled="Boolean(codexStore.threadMutation)"
                      @click="(event: Event) => deleteThread(thread.id, event)"
                    >
                      <Trash2 :size="14" class="mr-2" />
                      {{ t('threadActions.delete') }}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
              <p v-if="!archivedThreads.length" class="px-2 py-2 text-[10px] text-muted-foreground">
                {{ t('sidebar.archivedEmpty') }}
              </p>
            </div>
          </CollapsibleContent>
        </Collapsible>
      </div>
    </ScrollArea>

    <div class="border-t border-sidebar-border/40 p-2">
      <div class="flex items-center gap-1">
        <Button
          v-if="!appStore.account.authenticated"
          variant="ghost"
          class="h-8 flex-1 justify-start rounded-lg px-2 text-xs"
          @click="appStore.startLogin()"
        >
          <LogIn :size="14" class="mr-2" />
          {{ t('sidebar.signIn') }}
        </Button>
        <div v-else class="flex min-w-0 flex-1 items-center gap-1">
          <Popover v-model:open="usagePopoverOpen">
            <PopoverTrigger as-child>
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1 text-left transition-colors hover:bg-muted/50"
                :title="t('sidebar.usageHint')"
              >
                <Avatar class="size-6">
                  <AvatarFallback class="bg-primary text-[10px] text-primary-foreground">
                    {{ appStore.account.email.slice(0, 1).toUpperCase() || 'C' }}
                  </AvatarFallback>
                </Avatar>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-[11px] font-medium">{{ appStore.account.email }}</p>
                  <p class="truncate text-[9px] text-muted-foreground">
                    <span v-if="appStore.accountUsage?.lifetimeTokens != null">
                      {{ t('sidebar.usageLifetimeShort', { count: formatTokenCount(appStore.accountUsage.lifetimeTokens) }) }}
                    </span>
                    <span v-else>{{ appStore.account.planType || appStore.account.type }}</span>
                  </p>
                </div>
                <Coins :size="12" class="shrink-0 text-muted-foreground" />
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" class="w-80 p-3">
              <div class="mb-2 flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="text-xs font-semibold">{{ t('sidebar.usageTitle') }}</p>
                  <p class="mt-0.5 text-[10px] text-muted-foreground">{{ t('sidebar.usageSubtitle') }}</p>
                </div>
                <LoaderCircle v-if="usageLoading" :size="12" class="mt-0.5 animate-spin text-muted-foreground" />
              </div>

              <div class="mb-3 flex flex-wrap gap-1">
                <button
                  v-for="range in usageRanges"
                  :key="range.days"
                  type="button"
                  class="h-6 rounded-md px-2 text-[10px] transition-colors"
                  :class="usageRangeDays === range.days
                    ? 'bg-foreground text-background'
                    : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'"
                  @click="usageRangeDays = range.days"
                >
                  {{ range.label }}
                </button>
              </div>

              <div class="mb-3 rounded-lg border bg-muted/30 px-3 py-2.5">
                <p class="text-[10px] text-muted-foreground">{{ t('sidebar.usageRangeTotal') }}</p>
                <p class="mt-0.5 text-lg font-semibold tabular-nums tracking-tight">
                  {{ usageRangeView.dayCount ? formatTokenCount(usageRangeView.totalTokens) : '—' }}
                  <span class="text-[11px] font-normal text-muted-foreground">tokens</span>
                </p>
                <p class="mt-1 text-[10px] text-muted-foreground">
                  {{ t('sidebar.usageRangeMeta', {
                    days: usageRangeView.days,
                    avg: formatTokenCount(usageRangeView.averageTokens),
                    count: usageRangeView.dayCount,
                  }) }}
                </p>
              </div>

              <div v-if="usageRangeView.buckets.length" class="mb-3 max-h-36 space-y-1.5 overflow-y-auto pr-0.5">
                <div
                  v-for="bucket in usageRangeView.buckets"
                  :key="bucket.startDate"
                  class="grid grid-cols-[64px_1fr_40px] items-center gap-2 text-[10px]"
                >
                  <span class="truncate text-muted-foreground">{{ formatUsageDateLabel(bucket.startDate, usageLocale) }}</span>
                  <div class="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div
                      class="h-full rounded-full bg-foreground/70"
                      :style="{ width: `${usageRangeView.maxTokens ? Math.max(6, (bucket.tokens / usageRangeView.maxTokens) * 100) : 0}%` }"
                    />
                  </div>
                  <span class="text-right tabular-nums text-foreground/80">{{ formatTokenCount(bucket.tokens) }}</span>
                </div>
              </div>
              <p v-else class="mb-3 text-[11px] text-muted-foreground">
                {{ t('sidebar.usageEmpty') }}
              </p>

              <div class="grid grid-cols-2 gap-2 text-[10px]">
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('inspector.lifetimeTokens') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeTokens) }}</p>
                </div>
                <div class="rounded-md border px-2 py-1.5">
                  <p class="text-muted-foreground">{{ t('sidebar.usagePeakDaily') }}</p>
                  <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.peakDailyTokens) }}</p>
                </div>
              </div>

              <div
                v-if="appStore.accountRateLimits?.primary || appStore.accountRateLimits?.secondary"
                class="mt-3 space-y-2 border-t pt-2"
              >
                <p class="text-[10px] font-medium text-muted-foreground">{{ t('inspector.rateLimits') }}</p>
                <div v-if="appStore.accountRateLimits?.primary" class="space-y-1">
                  <div class="flex justify-between text-[10px]">
                    <span class="text-muted-foreground">{{ t('inspector.primaryLimit') }}</span>
                    <span class="tabular-nums">{{ appStore.accountRateLimits.primary.usedPercent }}%</span>
                  </div>
                  <Progress :model-value="appStore.accountRateLimits.primary.usedPercent" class="h-1" />
                </div>
                <div v-if="appStore.accountRateLimits?.secondary" class="space-y-1">
                  <div class="flex justify-between text-[10px]">
                    <span class="text-muted-foreground">{{ t('inspector.secondaryLimit') }}</span>
                    <span class="tabular-nums">{{ appStore.accountRateLimits.secondary.usedPercent }}%</span>
                  </div>
                  <Progress :model-value="appStore.accountRateLimits.secondary.usedPercent" class="h-1" />
                </div>
              </div>
            </PopoverContent>
          </Popover>
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button variant="ghost" size="icon-xs" class="rounded-md" :aria-label="t('sidebar.signOut')" @click="appStore.logout()">
                  <LogOut :size="14" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="top">{{ t('sidebar.signOut') }}</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>

        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon-xs" class="rounded-md" :aria-label="t('capabilities.title')" @click="openCapabilities">
                <Blocks :size="14" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">{{ t('capabilities.title') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>

        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon-xs" class="rounded-md" :aria-label="t('sidebar.openSettings')" @click="openSettings">
                <Settings :size="14" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">{{ t('sidebar.openSettings') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>
    </div>
    </div>
  </Motion>
</template>
