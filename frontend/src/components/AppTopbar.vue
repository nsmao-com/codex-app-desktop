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
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useAppStore, useCodexStore, useWorkspaceStore } from '@/stores'

const appStore = useAppStore()
const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()
const { t } = useI18n()

const props = defineProps<{
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
  <header class="flex h-12 shrink-0 items-center justify-between border-b bg-panel px-3">
    <div class="flex items-center gap-1">
      <Button variant="ghost" size="icon-sm" :aria-label="t('sidebar.toggle')" @click="emit('toggle-sidebar')">
        <Menu :size="16" />
      </Button>
      <Button variant="ghost" size="icon-sm" :aria-label="t('common.refresh')" @click="workspaceStore.refreshWorkspace">
        <RefreshCw :size="15" />
      </Button>
    </div>

    <div class="pointer-events-none absolute left-1/2 min-w-0 max-w-[min(42vw,760px)] -translate-x-1/2 text-center">
      <p class="truncate text-xs font-semibold">{{ codexStore.activeThread?.name || workspaceStore.workspace?.name || 'Nice Codex' }}</p>
      <p v-if="codexStore.activeThread && workspaceStore.workspace" class="truncate text-[10px] text-muted-foreground">
        {{ workspaceStore.workspace.name }}
      </p>
    </div>

    <div class="flex items-center gap-1">
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger as-child>
            <Button variant="ghost" size="icon-sm" :aria-label="t('settings.toggleTheme')" @click="appStore.toggleTheme">
              <component :is="themeIcon" :size="15" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{{ t('settings.toggleTheme') }}</TooltipContent>
        </Tooltip>
      </TooltipProvider>

      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger as-child>
            <Button variant="ghost" size="icon-sm" :aria-label="t('terminal.title')" @click="emit('open-terminal')">
              <Terminal :size="15" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{{ t('terminal.title') }}</TooltipContent>
        </Tooltip>
      </TooltipProvider>

      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger as-child>
            <Button variant="ghost" size="icon-sm" :aria-label="t('browser.title')" @click="emit('open-browser')">
              <Globe :size="15" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{{ t('browser.title') }}</TooltipContent>
        </Tooltip>
      </TooltipProvider>

      <Button variant="ghost" size="icon-sm" :aria-label="t('inspector.details')" @click="emit('toggle-inspector')">
        <PanelRight v-if="inspectorCollapsed" :size="15" />
        <PanelRightClose v-else :size="15" />
      </Button>
    </div>
  </header>
</template>
