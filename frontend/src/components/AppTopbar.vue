<script setup lang="ts">
import {
  Globe,
  Menu,
  Monitor,
  Moon,
  PanelRight,
  PanelRightClose,
  RefreshCw,
  Sun,
  Terminal,
} from '@lucide/vue'
import { Motion } from 'motion-v'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { springSnappy } from '@/lib/motion'
import { useAppStore, useArenaStore, useClaudeStore, useCodexStore, useGrokStore, useSubagentsStore, useWorkspaceStore } from '@/stores'
import type { SubagentRuntime } from '@/types/subagents'

const appStore = useAppStore()
const arenaStore = useArenaStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const subagentsStore = useSubagentsStore()
const workspaceStore = useWorkspaceStore()
const { t } = useI18n()

defineProps<{
  inspectorCollapsed: boolean
}>()

const emit = defineEmits<{
  'toggle-sidebar': []
  'toggle-inspector': []
  'open-terminal': []
  'open-browser': []
}>()

const themeIcon = computed(() => {
  switch (appStore.settings.theme) {
    case 'dark': return Moon
    case 'light': return Sun
    default: return Monitor
  }
})

const subagentScope = computed(() => {
  const pane = arenaStore.isArenaMode ? arenaStore.focusedPane : null
  const runtime = (pane?.runtime || appStore.activeRuntime) as SubagentRuntime
  const sessionId = pane
    ? arenaStore.sessionForPane(pane.id)
    : runtime === 'grok'
      ? grokStore.activeSessionId
      : runtime === 'claude'
        ? claudeStore.activeSessionId
        : codexStore.activeThreadId
  return { runtime, sessionId }
})
const activeSubagentCount = computed(() => {
  const { runtime, sessionId } = subagentScope.value
  return subagentsStore.activitiesFor(runtime, sessionId)
    .filter((activity) => activity.status === 'running' || activity.status === 'pending')
    .length
})

const topbarContext = computed(() => {
  const pane = arenaStore.isArenaMode ? arenaStore.focusedPane : null
  const runtime = pane?.runtime || appStore.activeRuntime
  const sessionId = pane
    ? arenaStore.sessionForPane(pane.id)
    : runtime === 'grok'
      ? grokStore.activeSessionId
      : runtime === 'claude'
        ? claudeStore.activeSessionId
        : codexStore.activeThreadId

  if (runtime === 'grok') {
    const session = grokStore.sessions.find((item) => grokStore.sameSession(item.id, sessionId))
    return {
      title: session?.name || session?.preview || '',
      workspace: session?.workspace || grokStore.workspacePath,
      hasSession: Boolean(sessionId),
    }
  }
  if (runtime === 'claude') {
    const session = claudeStore.sessions.find((item) => claudeStore.sameSession(item.id, sessionId))
    return {
      title: session?.name || session?.preview || '',
      workspace: session?.workspace || claudeStore.workspacePath,
      hasSession: Boolean(sessionId),
    }
  }
  const thread = codexStore.activeThread && codexStore.sameThread(codexStore.activeThread.id, sessionId)
    ? codexStore.activeThread
    : codexStore.threads.find((item) => codexStore.sameThread(item.id, sessionId))
      || Object.values(codexStore.projectThreads).flat()
        .find((item) => codexStore.sameThread(item.id, sessionId))
  return {
    title: thread?.name || thread?.preview || '',
    workspace: thread?.cwd || appStore.currentWorkspacePath,
    hasSession: Boolean(sessionId),
  }
})

function workspaceName(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || path
}

const topbarWorkspaceName = computed(() => {
  const path = topbarContext.value.workspace
  if (!path) return workspaceStore.workspace?.name || ''
  if (workspaceStore.workspace?.path === path) return workspaceStore.workspace.name
  return workspaceName(path)
})
const topbarTitle = computed(() =>
  topbarContext.value.title || topbarWorkspaceName.value || 'Nice Codex',
)
</script>

<template>
  <header class="flex h-11 shrink-0 items-center justify-between border-b border-border/60 bg-card/80 px-3 backdrop-blur-[2px]">
    <div class="flex items-center gap-0.5">
      <Motion :whileHover="{ scale: 1.08 }" :whilePress="{ scale: 0.9 }" :transition="springSnappy">
        <Button variant="ghost" size="icon-sm" class="rounded-lg" :aria-label="t('sidebar.toggle')" @click="emit('toggle-sidebar')">
          <Menu :size="16" />
        </Button>
      </Motion>
      <Motion :whileHover="{ scale: 1.08 }" :whilePress="{ scale: 0.9 }" :transition="springSnappy">
        <Button variant="ghost" size="icon-sm" class="rounded-lg" :aria-label="t('common.refresh')" @click="workspaceStore.refreshWorkspace">
          <RefreshCw :size="15" />
        </Button>
      </Motion>
    </div>

    <div
      class="pointer-events-none absolute left-1/2 min-w-0 max-w-[min(42vw,760px)] -translate-x-1/2 text-center"
    >
      <p class="truncate text-[12.5px] font-semibold tracking-tight">{{ topbarTitle }}</p>
      <p v-if="topbarContext.hasSession && topbarWorkspaceName" class="truncate text-[10px] text-muted-foreground">
        {{ topbarWorkspaceName }}
      </p>
    </div>

    <div class="flex items-center gap-0.5">
      <Motion :whileHover="{ scale: 1.08 }" :whilePress="{ scale: 0.9 }" :transition="springSnappy">
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon-sm" class="rounded-lg" :aria-label="t('settings.toggleTheme')" @click="appStore.toggleTheme">
                <component :is="themeIcon" :size="15" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{{ t('settings.toggleTheme') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </Motion>

      <Motion :whileHover="{ scale: 1.08 }" :whilePress="{ scale: 0.9 }" :transition="springSnappy">
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon-sm" class="rounded-lg" :aria-label="t('terminal.title')" @click="emit('open-terminal')">
                <Terminal :size="15" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{{ t('terminal.title') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </Motion>

      <Motion :whileHover="{ scale: 1.08 }" :whilePress="{ scale: 0.9 }" :transition="springSnappy">
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" size="icon-sm" class="rounded-lg" :aria-label="t('browser.title')" @click="emit('open-browser')">
                <Globe :size="15" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{{ t('browser.title') }}</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </Motion>

      <Motion :whileHover="{ scale: 1.08 }" :whilePress="{ scale: 0.9 }" :transition="springSnappy">
        <Button
          variant="ghost"
          size="icon-sm"
          class="relative rounded-lg"
          :aria-label="activeSubagentCount ? `${t('inspector.details')} (${activeSubagentCount})` : t('inspector.details')"
          @click="emit('toggle-inspector')"
        >
          <PanelRight v-if="inspectorCollapsed" :size="15" />
          <PanelRightClose v-else :size="15" />
          <span
            v-if="activeSubagentCount"
            class="absolute right-1 top-1 size-1.5 animate-pulse rounded-full bg-primary ring-2 ring-background motion-reduce:animate-none"
            aria-hidden="true"
          />
        </Button>
      </Motion>
    </div>
  </header>
</template>
