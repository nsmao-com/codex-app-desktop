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
import { useAppStore, useBrowserStore, useCodexStore, useShellStore, useTerminalStore, useWorkspaceStore } from '@/stores'

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const codexStore = useCodexStore()
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
}

const commands = computed<PaletteCommand[]>(() => [
  {
    id: 'new-thread',
    label: t('palette.newThread'),
    hint: 'Ctrl+N',
    icon: MessageSquarePlus,
    keywords: 'new chat thread 新建',
    run: () => { if (codexStore.isReady) void codexStore.newThread() },
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
    run: () => { void workspaceStore.selectWorkspace() },
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
    run: () => { void codexStore.startReview({ targetType: 'uncommittedChanges', delivery: 'inline' }) },
  },
  {
    id: 'compact',
    label: t('palette.compact'),
    hint: '/compact',
    icon: Command,
    keywords: 'compact',
    run: () => { void codexStore.compactActiveThread() },
  },
  {
    id: 'plan',
    label: t('palette.togglePlan'),
    hint: 'Shift+Tab',
    icon: Command,
    keywords: 'plan mode',
    run: () => {
      const isPlan = (codexStore.activeThread?.collaborationMode || appStore.settings.collaborationMode) === 'plan'
      void codexStore.setCollaborationMode(isPlan ? 'default' : 'plan')
    },
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
  if (!command) return
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
          :class="i === index ? 'bg-muted text-foreground' : 'text-foreground/85 hover:bg-muted/60'"
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
