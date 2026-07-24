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

import ClaudeIcon from '@/components/icons/ClaudeIcon.vue'
import GrokIcon from '@/components/icons/GrokIcon.vue'
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
  SimpleTooltip,
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useAppStore, useClaudeStore, useCodexStore, useGrokStore, useWorkspaceStore } from '@/stores'
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
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
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
const usageSubtitle = computed(() => {
  if (appStore.isGrokMode) return t('sidebar.usageSubtitleGrok')
  if (appStore.isClaudeMode) return t('sidebar.usageSubtitleClaude')
  return t('sidebar.usageSubtitle')
})

watch(usagePopoverOpen, (open) => {
  if (!open) return
  usageLoading.value = true
  // Grok/Claude: local usage.json only. Codex may also seed from cloud after auth.
  const localOnly = appStore.isGrokMode || appStore.isClaudeMode || !appStore.account.authenticated
  const refresh = localOnly
    ? appStore.loadLocalUsage()
    : appStore.refreshAccountData()
      .catch(() => undefined)
      .then(() => appStore.loadLocalUsage())
  void refresh.finally(() => {
    usageLoading.value = false
  })
})

// Keep runtime-scoped local totals warm when switching.
watch(
  () => appStore.activeRuntime,
  () => {
    void appStore.loadLocalUsage().catch(() => undefined)
  },
)

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
  get: () => {
    if (appStore.isGrokMode) return grokStore.search
    if (appStore.isClaudeMode) return claudeStore.search
    return codexStore.threadSearch
  },
  set: (value: string) => {
    if (appStore.isGrokMode) {
      grokStore.search = value
      void grokStore.loadSessions()
      return
    }
    if (appStore.isClaudeMode) {
      claudeStore.search = value
      void claudeStore.loadSessions()
      return
    }
    codexStore.setSearch(value)
  },
})

const threadCount = computed(() => {
  if (appStore.isGrokMode) {
    return grokStore.sessionGroups.reduce((total, group) => total + group.sessions.length, 0)
  }
  if (appStore.isClaudeMode) {
    return claudeStore.sessionGroups.reduce((total, group) => total + group.sessions.length, 0)
  }
  return codexStore.filteredThreadGroups.reduce((total, group) => total + group.threads.length, 0)
})
const groups = computed(() => codexStore.filteredThreadGroups)
const grokGroups = computed(() => grokStore.sessionGroups)
const claudeGroups = computed(() => claudeStore.sessionGroups)
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
  const date = new Date(timestamp * 1000)
  // Compact numeric form avoids CJK “7月23日” wrapping vertically in the narrow column.
  return `${date.getMonth() + 1}/${date.getDate()}`
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
  if (appStore.isGrokMode) return
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

async function setActiveRuntime(runtime: 'codex' | 'claude' | 'grok'): Promise<void> {
  if (appStore.activeRuntime === runtime) return
  // Only flip the flag here. App.vue watch defers hydrate/load so the tab animation isn't blocked.
  await appStore.setActiveRuntime(runtime)
}

const claudeProvider = computed(() => appStore.agentProviders.find((item) => item.kind === 'claude'))

const recentWorkspacePaths = computed(() => {
  if (appStore.isGrokMode) {
    return appStore.settings.grokRecentWorkspaces?.length
      ? appStore.settings.grokRecentWorkspaces
      : appStore.settings.recentWorkspaces
  }
  if (appStore.isClaudeMode) {
    return appStore.settings.claudeRecentWorkspaces?.length
      ? appStore.settings.claudeRecentWorkspaces
      : appStore.settings.recentWorkspaces
  }
  return appStore.settings.recentWorkspaces
})

function runtimeSlideX(): string {
  if (appStore.isClaudeMode) return '100%'
  if (appStore.isGrokMode) return '200%'
  return '0%'
}

function visibleClaudeSessions(group: { path: string; sessions: Array<{
  id: string
  name?: string
  preview?: string
  model?: string
  updatedAt?: number
}> }) {
  if (search.value) return group.sessions
  const limit = visibleCounts.value[group.path] ?? 30
  return group.sessions.slice(0, limit)
}

/** Open a session; if it lives under another project folder, switch workspace first (Codex-style). */
function openClaudeSession(group: { path: string; active: boolean }, sessionId: string): void {
  if (!group.active && group.path && group.path !== '(unknown)') {
    void workspaceStore.useWorkspace(group.path).then(() => {
      void claudeStore.openSession(sessionId, { switchWorkspace: false })
    })
    return
  }
  void claudeStore.openSession(sessionId)
}

function openGrokSession(group: { path: string; active: boolean }, sessionId: string): void {
  if (!group.active && group.path && group.path !== '(unknown)') {
    void workspaceStore.useWorkspace(group.path).then(() => {
      void grokStore.openSession(sessionId)
    })
    return
  }
  void grokStore.openSession(sessionId)
}

async function newInClaudeProject(group: { path: string; active: boolean }, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  if (!group.path || group.path === '(unknown)') return
  if (!group.active) {
    await workspaceStore.useWorkspace(group.path)
  }
  claudeStore.newSession()
}

async function newInGrokProject(group: { path: string; active: boolean }, event?: Event): Promise<void> {
  event?.stopPropagation()
  event?.preventDefault()
  if (!group.path || group.path === '(unknown)') return
  if (!group.active) {
    await workspaceStore.useWorkspace(group.path)
  }
  grokStore.newSession()
}

function archiveClaudeSession(sessionID: string, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  void claudeStore.archiveSession(sessionID)
}

function deleteClaudeSession(sessionID: string, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  void claudeStore.deleteSession(sessionID)
}

function formatClaudeUpdated(value: number): string {
  return formatGrokUpdated(value)
}

function openThread(group: ThreadGroup, thread: ThreadSummary): void {
  if (group.active) {
    codexStore.openThread(thread.id)
  } else {
    codexStore.openProjectThread(group.path, thread.id)
  }
}

function switchWorkspace(path: string): void {
  if (appStore.isGrokMode) {
    void workspaceStore.useWorkspace(path)
    void grokStore.loadSessions()
    return
  }
  if (appStore.isClaudeMode) {
    void workspaceStore.useWorkspace(path)
    void claudeStore.loadSessions()
    return
  }
  void codexStore.switchProject(path)
}

function chooseWorkspace(): void {
  if (appStore.isGrokMode) {
    void workspaceStore.selectWorkspace().then(() => {
      void grokStore.loadSessions()
    })
    return
  }
  void codexStore.selectProject()
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

function beginRename(thread: { id: string; name?: string }, event?: Event): void {
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

async function commitRename(thread: { id: string; name?: string }): Promise<void> {
  if (renamingThreadId.value !== thread.id) return
  const next = renameDraft.value.trim()
  renamingThreadId.value = ''
  if (!next || next === thread.name) return
  if (appStore.isGrokMode) {
    await grokStore.renameSession(thread.id, next)
    return
  }
  if (appStore.isClaudeMode) {
    await claudeStore.renameSession(thread.id, next)
    return
  }
  await codexStore.renameThread(thread.id, next)
}

function cancelRename(): void {
  renamingThreadId.value = ''
  renameDraft.value = ''
}

function archiveGrokSession(sessionID: string, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  void grokStore.archiveSession(sessionID)
}

function deleteGrokSession(sessionID: string, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  void grokStore.deleteSession(sessionID)
}

function forkThread(threadID: string, event?: Event): void {
  event?.stopPropagation()
  event?.preventDefault()
  void codexStore.forkThread(threadID)
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

function isGroupCollapsed(group: { path: string, active: boolean }): boolean {
  if (search.value) return false
  return isCollapsed.value[group.path] ?? !group.active
}

function setGroupCollapsed(group: { path: string }, collapsed: boolean): void {
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

function visibleGrokSessions(group: { path: string, active: boolean, sessions: Array<{
  id: string
  name?: string
  preview?: string
  model?: string
  backend?: string
  updatedAt?: number
}> }) {
  const defaultLimit = group.active ? 40 : 20
  const limit = visibleCounts.value[group.path] ?? defaultLimit
  return search.value ? group.sessions : group.sessions.slice(0, limit)
}

function loadMore(group: { path: string, active?: boolean }): void {
  const current = visibleCounts.value[group.path] ?? (group.active ? 40 : 20)
  visibleCounts.value = { ...visibleCounts.value, [group.path]: current + 30 }
}

function formatGrokUpdated(value?: number | null): string {
  // Grok timestamps may be unix seconds or milliseconds depending on source.
  if (value == null || !Number.isFinite(value) || value <= 0) return ''
  const ms = value > 1e12 ? value : value * 1000
  const difference = Date.now() - ms
  const minutes = Math.floor(difference / 60_000)
  if (minutes < 1) return t('sidebar.now')
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  const date = new Date(ms)
  return `${date.getMonth() + 1}/${date.getDate()}`
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
          <DropdownMenuItem :disabled="workspaceStore.switchingWorkspace" @click="chooseWorkspace()">
            <FolderOpen :size="14" class="mr-2" />
            {{ t('sidebar.chooseAnother') }}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            v-for="path in recentWorkspacePaths"
            :key="path"
            :disabled="workspaceStore.switchingWorkspace"
            @click="switchWorkspace(path)"
          >
            <Folder :size="14" class="mr-2" />
            <span class="truncate">{{ workspaceName(path) }}</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <!-- Product runtime: Codex · Claude · Grok
           Pill width/travel must use equal thirds of (100% - horizontal padding).
           Using gap + calc(33.333%-2px) left a visible right inset on the last tab. -->
      <div class="relative grid grid-cols-3 rounded-lg bg-foreground/[0.08] p-0.5 ring-1 ring-foreground/[0.06] dark:bg-white/10 dark:ring-white/10">
        <Motion
          class="pointer-events-none absolute inset-y-0.5 left-0.5 w-[calc((100%-4px)/3)] rounded-md bg-background shadow-sm dark:bg-card"
          :initial="false"
          :animate="{ x: runtimeSlideX() }"
          :transition="springSnappy"
        />
        <Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-8 gap-1 rounded-md px-1 text-[10px] hover:bg-transparent sm:text-[11px]"
          :class="appStore.isCodexMode
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          :aria-label="t('sidebar.runtimeCodex')"
          @click="void setActiveRuntime('codex')"
        >
          <OpenAIIcon :size="13" class="shrink-0 opacity-90" />
          <span class="truncate">Codex</span>
        </Button>
        <Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-8 gap-1 rounded-md px-1 text-[10px] hover:bg-transparent sm:text-[11px]"
          :class="appStore.isClaudeMode
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          :aria-label="t('sidebar.runtimeClaude')"
          @click="void setActiveRuntime('claude')"
        >
          <ClaudeIcon :size="13" class="shrink-0 opacity-90" />
          <span class="truncate">Claude</span>
        </Button>
        <Button
          variant="ghost"
          size="sm"
          class="relative z-[1] h-8 gap-1 rounded-md px-1 text-[10px] hover:bg-transparent sm:text-[11px]"
          :class="appStore.isGrokMode
            ? 'font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground'"
          :aria-label="t('sidebar.runtimeGrok')"
          @click="void setActiveRuntime('grok')"
        >
          <GrokIcon :size="13" class="shrink-0 opacity-90" />
          <span class="truncate">Grok</span>
        </Button>
      </div>

      <!-- Codex-only work mode (code / writing) — same equal-track math as runtime tabs. -->
      <div
        v-if="appStore.isCodexMode"
        class="relative grid grid-cols-2 rounded-lg bg-foreground/[0.06] p-0.5 ring-1 ring-foreground/[0.05] dark:bg-white/[0.06]"
      >
        <Motion
          class="pointer-events-none absolute inset-y-0.5 left-0.5 w-[calc((100%-4px)/2)] rounded-md bg-background shadow-sm dark:bg-card"
          :initial="false"
          :animate="{ x: appStore.settings.workMode === 'cowork' ? '100%' : '0%' }"
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
          class="h-9 w-full justify-start rounded-lg bg-primary px-2.5 text-xs text-primary-foreground shadow-sm hover:bg-primary/90 disabled:opacity-60"
          :disabled="appStore.isGrokMode
            ? !grokStore.workspacePath
            : appStore.isClaudeMode
              ? !claudeStore.workspacePath
              : (!codexStore.isReady || codexStore.creatingThread)"
          @click="appStore.isGrokMode
            ? grokStore.newSession()
            : appStore.isClaudeMode
              ? claudeStore.newSession()
              : void codexStore.newThread()"
        >
          <LoaderCircle v-if="appStore.isCodexMode && codexStore.creatingThread" :size="14" class="mr-1.5 animate-spin" />
          <Plus v-else :size="14" class="mr-1.5" />
          {{ appStore.isCodexMode && codexStore.creatingThread ? t('common.loading') : t('sidebar.newTask') }}
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
        <!-- Grok sessions: project-grouped like Codex -->
        <template v-if="appStore.isGrokMode">
          <Collapsible
            v-for="group in grokGroups"
            :key="`grok-${group.path}`"
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
                {{ group.sessions.length }}
              </span>
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 shrink-0 rounded-md opacity-0 transition-opacity group-hover/project:opacity-100 focus-visible:opacity-100"
                      :aria-label="t('sidebar.newTaskInProject')"
                      @click="(event: MouseEvent) => void newInGrokProject(group, event)"
                    >
                      <Plus :size="12" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="top">{{ t('sidebar.newTaskInProject') }}</TooltipContent>
                </Tooltip>
              </TooltipProvider>
            </div>

            <CollapsibleContent>
              <div class="ml-2 space-y-0.5 border-l border-sidebar-border/60 py-0.5 pl-2">
                <div
                  v-for="session in visibleGrokSessions(group)"
                  :key="session.id"
                  class="group/thread relative"
                >
                  <div
                    role="button"
                    tabindex="0"
                    class="flex h-auto min-h-11 w-full cursor-pointer items-start gap-2 rounded-lg px-2 py-1.5 text-left text-xs transition-colors"
                    :class="group.active && session.id === grokStore.activeSessionId
                      ? 'bg-accent text-accent-foreground shadow-sm'
                      : 'hover:bg-sidebar-accent/50'"
                    @click="renamingThreadId === session.id ? undefined : openGrokSession(group, session.id)"
                    @dblclick.stop="beginRename(session)"
                    @keydown.enter.prevent="renamingThreadId === session.id ? undefined : openGrokSession(group, session.id)"
                  >
                    <SimpleTooltip
                      :content="grokStore.runningSessionIds.includes(session.id)
                        ? t('sidebar.runningInBackground')
                        : (session.model || 'Grok')"
                    >
                      <span
                        class="relative mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg border border-border/60 bg-panel/80 text-muted-foreground"
                      >
                        <GrokIcon :size="15" />
                        <span
                          v-if="grokStore.runningSessionIds.includes(session.id)"
                          class="absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full border-2 border-sidebar bg-emerald-500 shadow-[0_0_0_1px_rgba(16,185,129,0.35)]"
                        >
                          <span class="absolute inset-0 animate-ping rounded-full bg-emerald-400/70" />
                        </span>
                      </span>
                    </SimpleTooltip>
                    <span class="min-w-0 flex-1">
                      <span class="flex min-w-0 items-center gap-1 pr-1">
                        <Input
                          v-if="renamingThreadId === session.id"
                          data-thread-rename-input
                          v-model="renameDraft"
                          class="h-6 rounded-md px-1.5 text-[11px] font-medium"
                          maxlength="80"
                          :aria-label="t('threadActions.rename')"
                          @click.stop
                          @keydown.enter.prevent="commitRename(session)"
                          @keydown.esc.prevent="cancelRename"
                          @blur="commitRename(session)"
                        />
                        <SimpleTooltip v-else :content="t('sidebar.renameHint')">
                          <span class="min-w-0 flex-1 truncate font-medium leading-5 text-foreground/90">
                            {{ session.name || session.id }}
                          </span>
                        </SimpleTooltip>
                      </span>
                      <span
                        v-if="renamingThreadId !== session.id"
                        class="mt-0.5 block truncate text-[10px] leading-4 text-muted-foreground"
                        :class="{ 'text-accent-foreground/70': group.active && session.id === grokStore.activeSessionId }"
                      >
                        {{ session.preview || session.model || session.backend || t('sidebar.noPreview') }}
                      </span>
                    </span>
                    <span
                      v-if="renamingThreadId !== session.id"
                      class="relative mt-0.5 flex h-5 w-10 shrink-0 items-center justify-end"
                      :class="{ 'text-accent-foreground/70': group.active && session.id === grokStore.activeSessionId }"
                    >
                      <span
                        v-if="grokStore.loadingSessionId === session.id"
                        class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent"
                      />
                      <span
                        v-else
                        class="whitespace-nowrap text-[10px] tabular-nums leading-none text-muted-foreground transition-opacity group-hover/thread:opacity-0 group-focus-within/thread:opacity-0"
                      >
                        {{ formatGrokUpdated(session.updatedAt) }}
                      </span>
                    </span>
                  </div>
                  <DropdownMenu>
                    <DropdownMenuTrigger as-child>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        class="absolute right-1.5 top-1.5 size-6 rounded-md text-muted-foreground opacity-0 transition-opacity group-hover/thread:opacity-100 group-focus-within/thread:opacity-100 data-[state=open]:opacity-100 focus-visible:opacity-100"
                        :aria-label="t('threadActions.title')"
                        :disabled="Boolean(grokStore.sessionMutation) || renamingThreadId === session.id"
                        @click.stop
                      >
                        <MoreHorizontal :size="13" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" class="min-w-40">
                      <DropdownMenuItem @click="(event: Event) => beginRename(session, event)">
                        <Pencil :size="14" class="mr-2" />
                        {{ t('threadActions.rename') }}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        :disabled="Boolean(grokStore.sessionMutation) || session.id.startsWith('pending-grok-')"
                        @click="(event: Event) => archiveGrokSession(session.id, event)"
                      >
                        <Archive :size="14" class="mr-2" />
                        {{ t('threadActions.archive') }}
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        class="text-destructive focus:text-destructive"
                        :disabled="Boolean(grokStore.sessionMutation)"
                        @click="(event: Event) => deleteGrokSession(session.id, event)"
                      >
                        <Trash2 :size="14" class="mr-2" />
                        {{ t('threadActions.delete') }}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>

                <Button
                  v-if="!search && group.sessions.length > visibleGrokSessions(group).length"
                  variant="ghost"
                  size="xs"
                  class="h-7 w-full justify-start rounded-md px-2 text-[11px] text-muted-foreground"
                  @click="loadMore(group)"
                >
                  {{ t('sidebar.loadMore', { count: 30 }) }}
                </Button>
              </div>
            </CollapsibleContent>
          </Collapsible>

          <div
            v-if="grokGroups.length === 0 || (threadCount === 0 && !grokStore.workspacePath)"
            class="flex flex-col items-center gap-2 px-4 py-10 text-center text-[11px] text-muted-foreground"
          >
            <div class="grid size-10 place-items-center rounded-full bg-muted/60">
              <GrokIcon :size="16" class="opacity-70" />
            </div>
            <p>{{ search ? t('sidebar.noSearchResults') : t('sidebar.grokEmpty') }}</p>
            <p class="max-w-[200px] text-[10px] leading-4 text-muted-foreground/80">
              {{ t('sidebar.grokEmptyHint') }}
            </p>
          </div>
        </template>

        <Collapsible
          v-for="group in appStore.isCodexMode ? groups : []"
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
                  <SimpleTooltip
                    :content="codexStore.runningThreadIds.includes(thread.id)
                      ? t('sidebar.runningInBackground')
                      : (thread.modelProvider || 'Codex / OpenAI')"
                  >
                    <span
                      class="relative mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg border border-border/60 bg-panel/80 text-muted-foreground"
                    >
                      <component :is="providerIcon(thread)" :size="15" />
                      <span
                        v-if="codexStore.runningThreadIds.includes(thread.id)"
                        class="absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full border-2 border-sidebar bg-emerald-500 shadow-[0_0_0_1px_rgba(16,185,129,0.35)]"
                      >
                        <span class="absolute inset-0 animate-ping rounded-full bg-emerald-400/70" />
                      </span>
                    </span>
                  </SimpleTooltip>

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
                      <SimpleTooltip v-else :content="t('sidebar.renameHint')">
                        <span class="min-w-0 flex-1 truncate font-medium leading-5">{{ thread.name }}</span>
                      </SimpleTooltip>
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
                    class="relative mt-0.5 flex h-5 w-10 shrink-0 items-center justify-end"
                    :class="{ 'text-accent-foreground/70': group.active && thread.id === codexStore.activeThreadId }"
                  >
                    <span
                      v-if="codexStore.loadingThreadId === thread.id"
                      class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent"
                    />
                    <span
                      v-else
                      class="whitespace-nowrap text-[10px] tabular-nums leading-none text-muted-foreground transition-opacity group-hover/thread:opacity-0 group-focus-within/thread:opacity-0"
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

        <!-- Claude sessions: project-grouped like Codex / Grok -->
        <template v-if="appStore.isClaudeMode">
          <Collapsible
            v-for="group in claudeGroups"
            :key="`claude-${group.path}`"
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
                  <span
                    class="min-w-0 truncate"
                    :class="group.active ? 'text-foreground' : ''"
                    :title="group.path"
                  >{{ group.name }}</span>
                </button>
              </CollapsibleTrigger>
              <span class="shrink-0 px-1 text-[10px] tabular-nums text-muted-foreground/80">
                {{ group.sessions.length }}
              </span>
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger as-child>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class="size-6 shrink-0 rounded-md opacity-0 transition-opacity group-hover/project:opacity-100 focus-visible:opacity-100"
                      :aria-label="t('sidebar.newTaskInProject')"
                      @click="(event: MouseEvent) => void newInClaudeProject(group, event)"
                    >
                      <Plus :size="12" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="top">{{ t('sidebar.newTaskInProject') }}</TooltipContent>
                </Tooltip>
              </TooltipProvider>
            </div>

            <CollapsibleContent>
              <div class="ml-2 space-y-0.5 border-l border-sidebar-border/60 py-0.5 pl-2">
                <div
                  v-for="session in visibleClaudeSessions(group)"
                  :key="session.id"
                  class="group/thread relative"
                >
                  <div
                    role="button"
                    tabindex="0"
                    class="flex h-auto min-h-11 w-full cursor-pointer items-start gap-2 rounded-lg px-2 py-1.5 text-left text-xs transition-colors"
                    :class="group.active && session.id === claudeStore.activeSessionId
                      ? 'bg-accent text-accent-foreground shadow-sm'
                      : 'hover:bg-sidebar-accent/50'"
                    @click="renamingThreadId === session.id ? undefined : openClaudeSession(group, session.id)"
                    @dblclick.stop="beginRename(session)"
                    @keydown.enter.prevent="renamingThreadId === session.id ? undefined : openClaudeSession(group, session.id)"
                  >
                    <SimpleTooltip
                      :content="claudeStore.runningSessionIds.includes(session.id)
                        ? t('sidebar.runningInBackground')
                        : (session.model || 'Claude')"
                    >
                      <span
                        class="relative mt-0.5 grid size-7 shrink-0 place-items-center rounded-lg border border-border/60 bg-panel/80 text-muted-foreground"
                      >
                        <ClaudeIcon :size="15" />
                        <span
                          v-if="claudeStore.runningSessionIds.includes(session.id)"
                          class="absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full border-2 border-sidebar bg-emerald-500 shadow-[0_0_0_1px_rgba(16,185,129,0.35)]"
                        >
                          <span class="absolute inset-0 animate-ping rounded-full bg-emerald-400/70" />
                        </span>
                      </span>
                    </SimpleTooltip>
                    <span class="min-w-0 flex-1">
                      <span class="flex min-w-0 items-center gap-1 pr-1">
                        <Input
                          v-if="renamingThreadId === session.id"
                          data-thread-rename-input
                          v-model="renameDraft"
                          class="h-6 rounded-md px-1.5 text-[11px] font-medium"
                          maxlength="80"
                          :aria-label="t('threadActions.rename')"
                          @click.stop
                          @keydown.enter.prevent="commitRename(session)"
                          @keydown.esc.prevent="cancelRename"
                          @blur="commitRename(session)"
                        />
                        <SimpleTooltip v-else :content="t('sidebar.renameHint')">
                          <span class="min-w-0 flex-1 truncate font-medium leading-5 text-foreground/90">
                            {{ session.name || session.id }}
                          </span>
                        </SimpleTooltip>
                      </span>
                      <span
                        v-if="renamingThreadId !== session.id"
                        class="mt-0.5 block truncate text-[10px] leading-4 text-muted-foreground"
                        :class="{ 'text-accent-foreground/70': group.active && session.id === claudeStore.activeSessionId }"
                      >
                        {{ session.preview || session.model || t('sidebar.noPreview') }}
                      </span>
                    </span>
                    <span
                      v-if="renamingThreadId !== session.id"
                      class="relative mt-0.5 flex h-5 w-10 shrink-0 items-center justify-end"
                      :class="{ 'text-accent-foreground/70': group.active && session.id === claudeStore.activeSessionId }"
                    >
                      <span
                        v-if="claudeStore.loadingSessionId === session.id"
                        class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent"
                      />
                      <span
                        v-else
                        class="whitespace-nowrap text-[10px] tabular-nums leading-none text-muted-foreground transition-opacity group-hover/thread:opacity-0 group-focus-within/thread:opacity-0"
                      >
                        {{ formatClaudeUpdated(session.updatedAt || 0) }}
                      </span>
                    </span>
                  </div>
                  <DropdownMenu>
                    <DropdownMenuTrigger as-child>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        class="absolute right-1.5 top-1.5 size-6 rounded-md text-muted-foreground opacity-0 transition-opacity group-hover/thread:opacity-100 group-focus-within/thread:opacity-100 data-[state=open]:opacity-100 focus-visible:opacity-100"
                        :aria-label="t('threadActions.title')"
                        :disabled="renamingThreadId === session.id"
                        @click.stop
                      >
                        <MoreHorizontal :size="13" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" class="min-w-40">
                      <DropdownMenuItem @click="(event: Event) => beginRename(session, event)">
                        <Pencil :size="14" class="mr-2" />
                        {{ t('threadActions.rename') }}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        :disabled="session.id.startsWith('pending-claude-')"
                        @click="(event: Event) => archiveClaudeSession(session.id, event)"
                      >
                        <Archive :size="14" class="mr-2" />
                        {{ t('threadActions.archive') }}
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        class="text-destructive focus:text-destructive"
                        @click="(event: Event) => deleteClaudeSession(session.id, event)"
                      >
                        <Trash2 :size="14" class="mr-2" />
                        {{ t('threadActions.delete') }}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>

                <Button
                  v-if="!search && group.sessions.length > visibleClaudeSessions(group).length"
                  variant="ghost"
                  size="xs"
                  class="h-7 w-full justify-start rounded-md px-2 text-[11px] text-muted-foreground"
                  @click="loadMore(group)"
                >
                  {{ t('sidebar.loadMore', { count: group.sessions.length - visibleClaudeSessions(group).length }) }}
                </Button>

                <div
                  v-if="group.sessions.length === 0"
                  class="px-2 py-2 text-[10px] text-muted-foreground"
                >
                  {{ t('sidebar.firstTask') }}
                </div>
              </div>
            </CollapsibleContent>
          </Collapsible>

          <div
            v-if="claudeGroups.length === 0 || (threadCount === 0 && !claudeStore.workspacePath)"
            class="flex flex-col items-center gap-2 px-4 py-10 text-center text-[11px] text-muted-foreground"
          >
            <div class="grid size-10 place-items-center rounded-full bg-muted/60">
              <ClaudeIcon :size="16" class="opacity-70" />
            </div>
            <p>{{ search ? t('sidebar.noSearchResults') : t('sidebar.claudeEmpty') }}</p>
            <p class="max-w-[200px] text-[10px] leading-4 text-muted-foreground/80">
              {{ claudeProvider?.runtimeReady || claudeStore.isReady
                ? t('sidebar.claudeEmptyHint')
                : t('sidebar.claudeRuntimeMissing') }}
            </p>
          </div>
        </template>

        <div
          v-if="appStore.isCodexMode && groups.length === 0"
          class="flex flex-col items-center gap-2 px-4 py-10 text-center text-[11px] text-muted-foreground"
        >
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
      </div>
    </ScrollArea>

    <div class="border-t border-sidebar-border/40 p-2">
      <div class="flex items-center gap-1">
        <!-- Grok: same token usage popover as Codex (today / 7d / 14d / 30d). -->
        <div v-if="appStore.isGrokMode" class="flex min-w-0 flex-1 items-center gap-1">
          <Popover v-model:open="usagePopoverOpen">
            <PopoverTrigger as-child>
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1 text-left transition-colors hover:bg-muted/50"
                :aria-label="t('sidebar.usageHint')"
              >
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border/60 bg-panel/80">
                  <GrokIcon :size="13" class="opacity-80" />
                </span>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-[11px] font-medium">Grok</p>
                  <p class="truncate text-[9px] text-muted-foreground">
                    <span v-if="appStore.accountUsage?.lifetimeTokens != null">
                      {{ t('sidebar.usageLifetimeShort', { count: formatTokenCount(appStore.accountUsage.lifetimeTokens) }) }}
                    </span>
                    <span v-else>
                      {{ grokStore.isReady
                        ? (grokStore.runtime.buildVersion || appStore.settings.grokBackend || 'build')
                        : t('sidebar.grokRuntimeMissing') }}
                    </span>
                  </p>
                </div>
                <Coins :size="12" class="shrink-0 text-muted-foreground" />
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" class="w-80 p-3">
              <div class="mb-2 flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="text-xs font-semibold">{{ t('sidebar.usageTitle') }}</p>
                  <p class="mt-0.5 text-[10px] text-muted-foreground">{{ usageSubtitle }}</p>
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

              <div class="mb-2">
                <p class="mb-1.5 text-[10px] font-medium text-muted-foreground">{{ t('sidebar.usageBreakdown') }}</p>
                <div class="grid grid-cols-2 gap-2 text-[10px]">
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageInput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageCached') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeCachedInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageOutput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeOutputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageReasoning') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeReasoningTokens) }}</p>
                  </div>
                </div>
              </div>

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
            </PopoverContent>
          </Popover>
        </div>
        <div v-else-if="appStore.isClaudeMode" class="flex min-w-0 flex-1 items-center gap-1">
          <Popover v-model:open="usagePopoverOpen">
            <PopoverTrigger as-child>
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1 text-left transition-colors hover:bg-muted/50"
                :aria-label="t('sidebar.usageHint')"
              >
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border/60 bg-panel/80">
                  <ClaudeIcon :size="13" class="opacity-80" />
                </span>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-[11px] font-medium">Claude</p>
                  <p class="truncate text-[9px] text-muted-foreground">
                    <span v-if="appStore.accountUsage?.lifetimeTokens != null">
                      {{ t('sidebar.usageLifetimeShort', { count: formatTokenCount(appStore.accountUsage.lifetimeTokens) }) }}
                    </span>
                    <span v-else>
                      {{ claudeProvider?.runtimeReady
                        ? (claudeProvider.version || t('sidebar.claudeReady'))
                        : t('sidebar.claudeRuntimeMissing') }}
                    </span>
                  </p>
                </div>
                <Coins :size="12" class="shrink-0 text-muted-foreground" />
              </button>
            </PopoverTrigger>
            <PopoverContent side="top" align="start" class="w-80 p-3">
              <div class="mb-2 flex items-start justify-between gap-2">
                <div class="min-w-0">
                  <p class="text-xs font-semibold">{{ t('sidebar.usageTitle') }}</p>
                  <p class="mt-0.5 text-[10px] text-muted-foreground">{{ usageSubtitle }}</p>
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

              <div class="mb-2">
                <p class="mb-1.5 text-[10px] font-medium text-muted-foreground">{{ t('sidebar.usageBreakdown') }}</p>
                <div class="grid grid-cols-2 gap-2 text-[10px]">
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageInput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageCached') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeCachedInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageOutput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeOutputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageReasoning') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeReasoningTokens) }}</p>
                  </div>
                </div>
              </div>

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
            </PopoverContent>
          </Popover>
        </div>
        <Button
          v-else-if="!appStore.account.authenticated"
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
                :aria-label="t('sidebar.usageHint')"
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
                  <p class="mt-0.5 text-[10px] text-muted-foreground">{{ usageSubtitle }}</p>
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

              <div class="mb-2">
                <p class="mb-1.5 text-[10px] font-medium text-muted-foreground">{{ t('sidebar.usageBreakdown') }}</p>
                <div class="grid grid-cols-2 gap-2 text-[10px]">
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageInput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageCached') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeCachedInputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageOutput') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeOutputTokens) }}</p>
                  </div>
                  <div class="rounded-md border px-2 py-1.5">
                    <p class="text-muted-foreground">{{ t('sidebar.usageReasoning') }}</p>
                    <p class="mt-0.5 font-medium tabular-nums">{{ formatTokenCount(appStore.accountUsage?.lifetimeReasoningTokens) }}</p>
                  </div>
                </div>
              </div>

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
