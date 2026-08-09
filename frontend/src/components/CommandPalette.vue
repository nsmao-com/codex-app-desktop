<script setup lang="ts">
import { computed, nextTick, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  Blocks,
  Command,
  FolderOpen,
  MessageSquarePlus,
  Settings,
  Sparkles,
  SquareTerminal,
  Globe,
} from '@lucide/vue'

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  useAppStore,
  useArenaStore,
  useBrowserStore,
  useClaudeStore,
  useCodexStore,
  useGrokStore,
  useShellStore,
  useTerminalStore,
  useWorkspaceStore,
} from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const arenaStore = useArenaStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const shellStore = useShellStore()
const terminalStore = useTerminalStore()
const browserStore = useBrowserStore()
const workspaceStore = useWorkspaceStore()

const query = shallowRef('')
const index = shallowRef(0)
const inputRef = useTemplateRef<HTMLInputElement>('inputRef')

type PaletteCommand = {
  id: string
  label: string
  hint: string
  icon: typeof Command
  run: () => void | Promise<void>
  keywords?: string
  disabled?: boolean
}

const palettePane = computed(() => arenaStore.isArenaMode ? arenaStore.focusedPane : null)
const paletteRuntime = computed<WorkspaceRuntime>(() => palettePane.value?.runtime || appStore.activeRuntime)
const paletteSessionId = computed(() => {
  const pane = palettePane.value
  if (pane) return arenaStore.sessionForPane(pane.id)
  if (paletteRuntime.value === 'grok') return grokStore.activeSessionId
  if (paletteRuntime.value === 'claude') return claudeStore.activeSessionId
  return codexStore.activeThreadId
})
const paletteThread = computed(() => {
  const id = paletteSessionId.value
  if (!id || paletteRuntime.value === 'grok' || paletteRuntime.value === 'claude') return null
  if (codexStore.activeThread && codexStore.sameThread(codexStore.activeThread.id, id)) {
    return codexStore.activeThread
  }
  return codexStore.threads.find((thread) => codexStore.sameThread(thread.id, id))
    || Object.values(codexStore.projectThreads).flat()
      .find((thread) => codexStore.sameThread(thread.id, id))
    || null
})
const paletteSessionBusy = computed(() => {
  const id = paletteSessionId.value
  if (!id) return false
  if (paletteRuntime.value === 'grok') {
    return Boolean(grokStore.sessionMutationForSession(id)) || grokStore.isSessionBusy(id)
  }
  if (paletteRuntime.value === 'claude') {
    return Boolean(claudeStore.sessionMutationForSession(id)) || claudeStore.isSessionBusy(id)
  }
  return Boolean(codexStore.threadMutationForThread(id))
    || codexStore.threadIsBusy(id)
    || (codexStore.queuedMessagesByThread[id]?.length ?? 0) > 0
})
const paletteNewSessionDisabled = computed(() => {
  if (paletteRuntime.value === 'grok') return !grokStore.workspacePath
  if (paletteRuntime.value === 'claude') return !claudeStore.workspacePath
  return !codexStore.isRuntimeReady(paletteRuntime.value) || codexStore.creatingThread
})
const paletteCanCompact = computed(() => Boolean(
  paletteRuntime.value !== 'grok'
  && paletteRuntime.value !== 'claude'
  && paletteSessionId.value
  && !paletteSessionId.value.startsWith('pending-thread-')
  && !paletteSessionBusy.value,
))
const paletteCanReview = computed(() => Boolean(
  paletteRuntime.value === 'codex'
  && paletteSessionId.value
  && !paletteSessionId.value.startsWith('pending-thread-')
  && !paletteSessionBusy.value,
))

function bindPaletteSession(runtime: WorkspaceRuntime, sessionId: string): boolean {
  const pane = palettePane.value
  if (!pane || pane.runtime !== runtime) return false
  arenaStore.selectPaneSession(pane.id, sessionId)
  return true
}

async function syncPaletteRuntime(): Promise<boolean> {
  const runtime = paletteRuntime.value
  return appStore.activeRuntime === runtime
    ? appStore.ensureActiveRuntimeSynced(runtime)
    : appStore.setActiveRuntime(runtime)
}

async function createPaletteSession(): Promise<void> {
  if (paletteNewSessionDisabled.value) return
  const pane = palettePane.value
  const runtime = paletteRuntime.value
  if (runtime === 'grok') {
    grokStore.newSession()
    if (pane) arenaStore.setPaneSession(pane.id, grokStore.activeSessionId)
    return
  }
  if (runtime === 'claude') {
    claudeStore.newSession(Boolean(pane))
    if (pane && claudeStore.activeSessionId) arenaStore.setPaneSession(pane.id, claudeStore.activeSessionId)
    return
  }
  const thread = pane
    ? await codexStore.newRuntimeThread(runtime, true)
    : await codexStore.newThread()
  if (pane && thread?.id) arenaStore.setPaneSession(pane.id, thread.id)
}

async function selectPaletteWorkspace(): Promise<void> {
  if (workspaceStore.switchingWorkspace || !await syncPaletteRuntime()) return
  const runtime = paletteRuntime.value
  if (runtime === 'grok') {
    const path = await workspaceStore.selectWorkspace()
    if (!path) return
    await grokStore.loadSessions(true)
    const group = grokStore.sessionGroups.find((item) => item.active)
    const target = group?.sessions.find((item) => grokStore.sameSession(item.id, grokStore.activeSessionId))
      || group?.sessions[0]
    if (target) {
      if (!bindPaletteSession(runtime, target.id)) {
        await grokStore.openSession(target.id, { switchWorkspace: false })
      }
    } else {
      grokStore.newSession()
      bindPaletteSession(runtime, grokStore.activeSessionId)
    }
    return
  }
  if (runtime === 'claude') {
    const path = await workspaceStore.selectWorkspace()
    if (!path) return
    await claudeStore.loadSessions()
    const group = claudeStore.sessionGroups.find((item) => item.active)
    const target = group?.sessions.find((item) => claudeStore.sameSession(item.id, claudeStore.activeSessionId))
      || group?.sessions[0]
    if (target) {
      if (!bindPaletteSession(runtime, target.id)) {
        await claudeStore.openSession(target.id, { switchWorkspace: false })
      }
    } else {
      claudeStore.newSession(Boolean(palettePane.value))
      bindPaletteSession(runtime, claudeStore.activeSessionId)
    }
    return
  }
  await codexStore.selectProject()
  const pane = palettePane.value
  if (pane) arenaStore.setPaneSession(pane.id, codexStore.activeThreadId)
}

function reviewPaletteSession(): void {
  if (!paletteCanReview.value) return
  void codexStore.startReview(
    { targetType: 'uncommittedChanges', delivery: 'inline' },
    paletteSessionId.value,
  )
}

function compactPaletteSession(): void {
  if (!paletteCanCompact.value) return
  void codexStore.compactThread(paletteSessionId.value, !palettePane.value)
}

function togglePalettePlanMode(): void {
  if (paletteRuntime.value !== 'codex' || !paletteThread.value || paletteSessionBusy.value) return
  const isPlan = (paletteThread.value.collaborationMode || appStore.settings.collaborationMode) === 'plan'
  void codexStore.setCollaborationMode(
    isPlan ? 'default' : 'plan',
    paletteSessionId.value,
  )
}

const commands = computed<PaletteCommand[]>(() => [
  {
    id: 'new-thread',
    label: t('palette.newThread'),
    hint: 'Ctrl+N',
    icon: MessageSquarePlus,
    keywords: 'new chat thread 新建',
    disabled: paletteNewSessionDisabled.value,
    run: createPaletteSession,
  },
  {
    id: 'terminal',
    label: t('palette.terminal'),
    hint: 'Ctrl+`',
    icon: SquareTerminal,
    keywords: 'terminal shell 终端',
    run: () => { if (workspaceStore.workspace) void terminalStore.openTerminal() },
  },
  {
    id: 'browser',
    label: t('palette.browser'),
    hint: 'Ctrl+Shift+B',
    icon: Globe,
    keywords: 'browser 浏览器',
    run: () => browserStore.openBrowser(''),
  },
  {
    id: 'settings',
    label: t('palette.settings'),
    hint: '',
    icon: Settings,
    keywords: 'settings preferences 设置',
    run: () => { void router.push({ name: 'settings' }) },
  },
  {
    id: 'capabilities',
    label: t('palette.capabilities'),
    hint: '',
    icon: Blocks,
    keywords: 'mcp skills plugins 能力',
    run: () => { void router.push({ name: 'capabilities' }) },
  },
  {
    id: 'skills',
    label: t('palette.skills'),
    hint: '',
    icon: Sparkles,
    keywords: 'skills 技能',
    run: () => { void router.push({ name: 'capabilities', query: { tab: 'skills' } }) },
  },
  {
    id: 'workspace',
    label: t('palette.chooseWorkspace'),
    hint: '',
    icon: FolderOpen,
    keywords: 'workspace folder 工作区',
    disabled: workspaceStore.switchingWorkspace,
    run: selectPaletteWorkspace,
  },
  {
    id: 'sidebar',
    label: t('palette.toggleSidebar'),
    hint: '',
    icon: Command,
    keywords: 'sidebar 侧边栏',
    run: () => shellStore.toggleSidebar(),
  },
  {
    id: 'review',
    label: t('palette.review'),
    hint: '/review',
    icon: Command,
    keywords: 'review git',
    disabled: !paletteCanReview.value,
    run: reviewPaletteSession,
  },
  {
    id: 'compact',
    label: t('palette.compact'),
    hint: '/compact',
    icon: Command,
    keywords: 'compact',
    disabled: !paletteCanCompact.value,
    run: compactPaletteSession,
  },
  {
    id: 'memories',
    label: t('memories.title'),
    hint: '/memories',
    icon: Sparkles,
    keywords: 'memories 记忆',
    run: () => { window.dispatchEvent(new Event('nice-codex:open-memories')) },
  },
  {
    id: 'plan',
    label: t('palette.togglePlan'),
    hint: 'Shift+Tab',
    icon: Command,
    keywords: 'plan mode',
    disabled: paletteRuntime.value !== 'codex' || !paletteThread.value || paletteSessionBusy.value,
    run: togglePalettePlanMode,
  },
])

const filtered = computed(() => {
  const q = query.value.trim().toLocaleLowerCase()
  if (!q) return commands.value
  return commands.value.filter((item) =>
    item.label.toLocaleLowerCase().includes(q)
    || item.id.includes(q)
    || (item.keywords || '').toLocaleLowerCase().includes(q),
  )
})

watch(filtered, (items) => {
  if (index.value >= items.length) index.value = Math.max(0, items.length - 1)
})

watch(open, async (value) => {
  if (!value) return
  query.value = ''
  index.value = 0
  await nextTick()
  inputRef.value?.focus()
})

function onOpenChange(value: boolean): void {
  open.value = value
}

async function runCommand(command?: PaletteCommand): Promise<void> {
  if (!command || command.disabled) return
  open.value = false
  await command.run()
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    if (!filtered.value.length) return
    index.value = (index.value + 1) % filtered.value.length
    return
  }
  if (event.key === 'ArrowUp') {
    event.preventDefault()
    if (!filtered.value.length) return
    index.value = (index.value - 1 + filtered.value.length) % filtered.value.length
    return
  }
  if (event.key === 'Enter') {
    event.preventDefault()
    void runCommand(filtered.value[index.value])
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="onOpenChange">
    <DialogContent class="gap-0 overflow-hidden p-0 sm:max-w-lg">
      <DialogHeader class="sr-only">
        <DialogTitle>{{ t('palette.title') }}</DialogTitle>
        <DialogDescription>{{ t('palette.hint') }}</DialogDescription>
      </DialogHeader>
      <div class="border-b px-3 py-2">
        <div class="flex items-center gap-2">
          <Command :size="14" class="text-muted-foreground" />
          <input
            ref="inputRef"
            v-model="query"
            type="text"
            class="h-9 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            :placeholder="t('palette.placeholder')"
            @keydown="onKeydown"
          >
        </div>
      </div>
      <div class="max-h-80 overflow-y-auto p-1.5">
        <button
          v-for="(command, i) in filtered"
          :key="command.id"
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm transition-colors"
          :class="command.disabled
            ? 'cursor-not-allowed text-muted-foreground/45'
            : i === index
              ? 'bg-muted text-foreground'
              : 'text-foreground/85 hover:bg-muted/60'"
          :disabled="command.disabled"
          @mouseenter="index = i"
          @click="runCommand(command)"
        >
          <component :is="command.icon" :size="14" class="shrink-0 text-muted-foreground" />
          <span class="min-w-0 flex-1 truncate">{{ command.label }}</span>
          <span v-if="command.hint" class="shrink-0 font-mono text-[10px] text-muted-foreground">{{ command.hint }}</span>
        </button>
        <p v-if="!filtered.length" class="px-2 py-6 text-center text-xs text-muted-foreground">
          {{ t('palette.empty') }}
        </p>
      </div>
    </DialogContent>
  </Dialog>
</template>
