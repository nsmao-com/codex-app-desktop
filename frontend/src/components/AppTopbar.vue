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
import { useAppStore, useCodexStore, useWorkspaceStore } from '@/stores'

const appStore = useAppStore()
const codexStore = useCodexStore()
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

    <Motion
      class="pointer-events-none absolute left-1/2 min-w-0 max-w-[min(42vw,760px)] -translate-x-1/2 text-center"
      :key="codexStore.activeThreadId || workspaceStore.workspace?.path || 'home'"
      :initial="{ opacity: 0, y: -4 }"
      :animate="{ opacity: 1, y: 0 }"
      :transition="{ duration: 0.18, ease: [0.16, 1, 0.3, 1] }"
    >
      <p class="truncate text-[12.5px] font-semibold tracking-tight">{{ codexStore.activeThread?.name || workspaceStore.workspace?.name || 'Nice Codex' }}</p>
      <p v-if="codexStore.activeThread && workspaceStore.workspace" class="truncate text-[10px] text-muted-foreground">
        {{ workspaceStore.workspace.name }}
      </p>
    </Motion>

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
        <Button variant="ghost" size="icon-sm" class="rounded-lg" :aria-label="t('inspector.details')" @click="emit('toggle-inspector')">
          <PanelRight v-if="inspectorCollapsed" :size="15" />
          <PanelRightClose v-else :size="15" />
        </Button>
      </Motion>
    </div>
  </header>
</template>
