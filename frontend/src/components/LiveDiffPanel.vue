<script setup lang="ts">
import { FileDiff, PanelRightClose } from '@lucide/vue'
import { Motion } from 'motion-v'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { panelFromRight } from '@/lib/motion'
import { useCodexStore, useWorkspaceStore } from '@/stores'
import DiffViewer from './DiffViewer.vue'

const emit = defineEmits<{
  collapse: []
}>()

const { t } = useI18n()
const codexStore = useCodexStore()
const workspaceStore = useWorkspaceStore()

const liveTurnDiff = computed(() => {
  const threadID = codexStore.activeThreadId
  if (!threadID) return ''
  return codexStore.latestDiffByThread[threadID] || ''
})

const diffText = computed(() => {
  if (workspaceStore.diffSource === 'turn') {
    return liveTurnDiff.value || workspaceStore.inspectedDiff
  }
  return workspaceStore.inspectedDiff
})

const title = computed(() => {
  if (workspaceStore.diffSource === 'turn') return t('inspector.liveDiff')
  return workspaceStore.inspectedDiffPath || t('inspector.liveDiff')
})

function close(): void {
  workspaceStore.closeDiffSidebar()
  emit('collapse')
}
</script>

<template>
  <Motion
    as="aside"
    class="flex h-full min-h-0 w-[min(48vw,640px)] shrink-0 flex-col overflow-hidden border-l bg-panel max-lg:absolute max-lg:inset-y-0 max-lg:right-0 max-lg:z-40 max-lg:w-[min(100vw,640px)] max-lg:shadow-xl"
    :initial="panelFromRight.initial"
    :animate="panelFromRight.animate"
    :exit="panelFromRight.exit"
    :transition="panelFromRight.transition"
  >
    <header class="flex h-10 shrink-0 items-center gap-2 border-b px-3">
      <FileDiff :size="14" class="shrink-0 text-warning" />
      <div class="min-w-0 flex-1">
        <p class="truncate text-xs font-semibold">{{ t('inspector.liveDiff') }}</p>
        <p class="truncate text-[10px] text-muted-foreground" :title="title">{{ title }}</p>
      </div>
      <Button variant="ghost" size="icon-xs" :aria-label="t('common.close')" @click="close">
        <PanelRightClose :size="14" />
      </Button>
    </header>

    <div class="flex min-h-0 flex-1 flex-col overflow-hidden p-2">
      <DiffViewer :diff="diffText" class="min-h-0 flex-1" />
    </div>
  </Motion>
</template>
