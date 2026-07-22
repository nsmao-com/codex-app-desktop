<script setup lang="ts">
import {
  Archive,
  Blocks,
  Bot,
  ChevronDown,
  ChevronRight,
  Folder,
  FolderOpen,
  LoaderCircle,
  LogIn,
  LogOut,
  MessageSquareText,
  Plus,
  RefreshCw,
  Search,
  Settings,
  SlidersHorizontal,
  Sparkles,
  X,
} from '@lucide/vue'
import { useRouter } from 'vue-router'
import { computed, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

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
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useAppStore, useCodexStore, useWorkspaceStore } from '@/stores'
import type { ThreadGroup, ThreadSummary } from '@/types/codex'

const router = useRouter()
const appStore = useAppStore()
const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()
const { locale, t } = useI18n()

const props = defineProps<{
  collapsed?: boolean
}>()

const emit = defineEmits<{
  'toggle-sidebar': []
}>()

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
  // Optimistic UI so Code/Cowork switches even before preferences round-trip.
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

function setArchivedOpen(open: boolean): void {
  archivedOpen.value = open
}


function providerIcon(thread: ThreadSummary): typeof Bot {
  const provider = `${thread.modelProvider} ${thread.model}`.toLocaleLowerCase()
  if (provider.includes('anthropic') || provider.includes('claude')) return Bot
  if (provider.includes('google') || provider.includes('gemini')) return Sparkles
  if (provider.includes('xai') || provider.includes('grok')) return Blocks
  return Sparkles
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
  <aside
    class="flex h-full w-[292px] shrink-0 flex-col border-r border-sidebar-border bg-sidebar transition-[width,opacity] duration-200 max-md:absolute max-md:inset-y-0 max-md:left-0 max-md:z-40"
    :class="collapsed
      ? 'pointer-events-none w-0 overflow-hidden border-r-0 p-0 opacity-0'
      : 'pointer-events-auto'"
  >
    <div class="flex h-12 items-center justify-between px-3">
      <div class="flex min-w-0 items-center gap-2.5">
        <div class="grid size-6 place-items-center rounded-md bg-foreground text-[10px] font-bold text-background">N</div>
        <div class="min-w-0">
          <div class="flex items-center gap-1.5">
            <span class="text-[13px] font-semibold">Nice Codex</span>
            <span class="rounded bg-muted px-1 py-0.5 font-mono text-[9px] tabular-nums text-muted-foreground">v{{ appStore.appVersion }}</span>
          </div>
          <button
            v-if="appStore.updateInfo?.updateAvailable"
            type="button"
            class="text-[10px] text-primary hover:underline"
            @click="appStore.openUpdatePage"
          >
            {{ t('updates.availableShort', { version: appStore.updateInfo.latestVersion }) }}
          </button>
        </div>
      </div>
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger as-child>
            <Button variant="ghost" size="icon-xs" :aria-label="t('sidebar.toggle')" @click="emit('toggle-sidebar')">
              <X :size="14" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{{ t('sidebar.toggle') }}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    </div>

    <div class="space-y-2 px-3 pb-2">
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button variant="ghost" class="h-8 w-full justify-between px-2 text-xs" :disabled="workspaceStore.switchingWorkspace">
            <span class="flex min-w-0 items-center gap-2">
              <Folder :size="14" />
              <span class="truncate">{{ workspaceStore.workspace?.name || t('sidebar.chooseFolder') }}</span>
            </span>
            <ChevronDown :size="13" />
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

      <div class="grid grid-cols-2 gap-1 rounded-md border border-sidebar-border p-0.5">
        <Button
          variant="ghost"
          size="sm"
          class="h-7 text-[11px]"
          :class="appStore.settings.workMode !== 'cowork' ? 'bg-accent text-accent-foreground' : 'text-muted-foreground'"
          @click="void setWorkMode('code')"
        >
          {{ t('sidebar.code') }}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          class="h-7 text-[11px]"
          :class="appStore.settings.workMode === 'cowork' ? 'bg-accent text-accent-foreground' : 'text-muted-foreground'"
          @click="void setWorkMode('cowork')"
        >
          {{ t('sidebar.cowork') }}
        </Button>
      </div>
      <Button
        class="h-9 w-full justify-start bg-primary px-2.5 text-xs text-white hover:bg-primary/90 hover:text-white disabled:text-white disabled:[&_svg]:text-white [&_svg]:text-white"
        :disabled="!codexStore.isReady || codexStore.creatingThread"
        @click="void codexStore.newThread()"
      >
        <LoaderCircle v-if="codexStore.creatingThread" :size="14" class="mr-1.5 animate-spin" />
        <Plus v-else :size="14" class="mr-1.5" />
        {{ codexStore.creatingThread ? t('common.loading') : t('sidebar.newTask') }}
      </Button>
      <Button variant="ghost" class="h-8 w-full justify-start px-2 text-xs text-muted-foreground" @click="openCapabilities">
        <SlidersHorizontal :size="14" class="mr-2" />
         {{ t('sidebar.customize') }}
      </Button>
    </div>

    <div class="flex items-center justify-between px-4 py-2">
       <span class="text-[11px] font-semibold text-muted-foreground">{{ t('sidebar.recents') }}</span>
      <span class="text-[10px] tabular-nums text-muted-foreground">{{ threadCount }}</span>
    </div>

    <div class="px-3 pb-2">
      <div class="relative">
        <Search class="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          v-model="search"
          type="search"
          :placeholder="t('sidebar.searchPlaceholder')"
          class="h-8 pl-8 text-xs"
        />
      </div>
    </div>

    <ScrollArea class="min-h-0 flex-1 px-2">
      <div class="space-y-0.5 pb-2">
        <Collapsible
          v-for="group in groups"
          :key="group.path"
          :open="!isGroupCollapsed(group)"
          @update:open="(open) => setGroupCollapsed(group, !open)"
        >
          <CollapsibleTrigger as-child>
            <Button
              variant="ghost"
              class="h-7 w-full justify-between px-2 text-[11px] font-semibold text-muted-foreground"
            >
              <span class="flex items-center gap-1.5">
                <ChevronRight
                  :size="12"
                  class="transition-transform"
                  :class="{ 'rotate-90': !isGroupCollapsed(group) }"
                />
                <FolderOpen v-if="group.active" :size="12" />
                <Folder v-else :size="12" />
                <span class="truncate">{{ group.name }}</span>
              </span>
              <span class="text-[10px]">
                <span v-if="group.loading" class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
                <span v-else>{{ group.threads.length }}</span>
              </span>
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div class="ml-3 space-y-0.5 border-l border-sidebar-border pl-2">
              <div v-if="group.error" class="rounded-md bg-destructive/10 p-2 text-[10px] text-destructive">
                {{ group.error }}
                <Button size="xs" variant="ghost" class="mt-1 h-6" @click="codexStore.reloadProject(group.path)">
                  <RefreshCw :size="11" class="mr-1" />
                  {{ t('sidebar.retryProject') }}
                </Button>
              </div>

              <Button
                v-for="thread in visibleThreads(group)"
                :key="thread.id"
                variant="ghost"
                class="h-auto min-h-12 w-full justify-start gap-2 rounded-md px-2 py-1.5 text-left text-xs"
                :class="{ 'bg-accent text-accent-foreground': group.active && thread.id === codexStore.activeThreadId }"
                @click="openThread(group, thread)"
              >
                 <span class="relative grid size-5 shrink-0 place-items-center rounded-[5px] border bg-panel text-muted-foreground" :title="thread.modelProvider || 'Codex / OpenAI'">
                   <component :is="providerIcon(thread)" :size="12" />
                  <span v-if="codexStore.runningThreadIds.includes(thread.id)" class="absolute -bottom-0.5 -right-0.5 size-1.5 rounded-full border-2 border-muted bg-green-500" />
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block truncate">{{ thread.name }}</span>
                  <span class="flex min-w-0 items-center gap-1 text-[10px] text-muted-foreground" :class="{ 'text-accent-foreground/70': group.active && thread.id === codexStore.activeThreadId }">
                    <span class="truncate">{{ thread.model || thread.preview || t('sidebar.noPreview') }}</span>
                  </span>
                </span>
                <span class="shrink-0 text-[10px] text-muted-foreground" :class="{ 'text-accent-foreground/70': group.active && thread.id === codexStore.activeThreadId }">
                  <span v-if="codexStore.loadingThreadId === thread.id" class="inline-block size-3 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
                  <span v-else>{{ formatUpdated(thread.updatedAt) }}</span>
                </span>
              </Button>

              <Button
                v-if="visibleThreads(group).length < group.threads.length"
                variant="ghost"
                size="sm"
                class="h-7 w-full justify-start px-2 text-[10px] text-muted-foreground"
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

        <div v-if="groups.length === 0" class="flex flex-col items-center gap-2 px-4 py-8 text-center text-[11px] text-muted-foreground">
          <MessageSquareText :size="18" />
          <p>{{ search ? t('sidebar.noSearchResults') : t('sidebar.firstTask') }}</p>
        </div>

        <Collapsible
          v-if="archivedThreads.length"
          :open="archivedOpen"
          class="mt-3"
          @update:open="setArchivedOpen"
        >
          <CollapsibleTrigger as-child>
            <Button variant="ghost" class="h-8 w-full justify-between px-2 text-[11px] text-muted-foreground">
              <span class="flex items-center gap-1.5">
                <Archive :size="12" />
                {{ t('sidebar.archived') }}
                <span class="text-[10px]">{{ archivedThreads.length }}</span>
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
                class="flex items-center gap-1 rounded-md px-1 py-1 hover:bg-muted/50"
              >
                <div class="min-w-0 flex-1 px-1">
                  <p class="truncate text-[11px]">{{ thread.name }}</p>
                  <p class="truncate text-[10px] text-muted-foreground">{{ thread.preview || t('sidebar.noPreview') }}</p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  class="h-6 shrink-0 px-2 text-[10px]"
                  :disabled="Boolean(codexStore.threadMutation)"
                  @click="restoreArchived(thread.id)"
                >
                  {{ t('sidebar.restore') }}
                </Button>
              </div>
            </div>
          </CollapsibleContent>
        </Collapsible>
      </div>
    </ScrollArea>

    <div class="border-t border-sidebar-border p-2">
      <div class="flex items-center gap-1">
        <Button v-if="!appStore.account.authenticated" variant="ghost" class="h-8 flex-1 justify-start px-2 text-xs" @click="appStore.startLogin()">
          <LogIn :size="14" class="mr-2" />
          {{ t('sidebar.signIn') }}
        </Button>
        <div v-else class="flex min-w-0 flex-1 items-center gap-1">
          <div class="flex min-w-0 flex-1 items-center gap-2 px-2">
            <Avatar class="size-6">
              <AvatarFallback class="bg-primary text-[10px] text-primary-foreground">
                {{ appStore.account.email.slice(0, 1).toUpperCase() || 'C' }}
              </AvatarFallback>
            </Avatar>
            <div class="min-w-0">
              <p class="truncate text-[11px] font-medium">{{ appStore.account.email }}</p>
              <p class="truncate text-[9px] text-muted-foreground">{{ appStore.account.planType || appStore.account.type }}</p>
            </div>
          </div>
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger as-child>
                <Button variant="ghost" size="icon-xs" :aria-label="t('sidebar.signOut')" @click="appStore.logout()">
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
              <Button variant="ghost" size="icon-xs" :aria-label="t('capabilities.title')" @click="openCapabilities">
                <Blocks :size="14" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">{{ t('capabilities.title') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>

        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon-xs" :aria-label="t('sidebar.openSettings')" @click="openSettings">
                <Settings :size="14" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">{{ t('sidebar.openSettings') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>
    </div>
  </aside>
</template>
