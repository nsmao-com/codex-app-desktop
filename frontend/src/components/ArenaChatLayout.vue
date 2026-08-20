<script setup lang="ts">
import { Columns2, Columns3, Columns4, Plus, X } from '@lucide/vue'
import { computed, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import ArenaChatPane from '@/components/ArenaChatPane.vue'
import { Button } from '@/components/ui/button'
import { useArenaStore } from '@/stores/arena'
import { useAppStore } from '@/stores/app'

const emit = defineEmits<{
  'show-inspector': []
}>()

const { t } = useI18n()
const arenaStore = useArenaStore()
const appStore = useAppStore()

const paneGridStyle = computed(() => ({
  gridTemplateColumns: `repeat(${arenaStore.panes.length}, minmax(360px, 1fr))`,
  minWidth: `${arenaStore.panes.length * 360}px`,
}))

const lastLiveTargetId = shallowRef('')
let liveMoveRaf = 0

function onFocusPane(paneId: string): void {
  arenaStore.focusPane(paneId)
}

function onDragStart(paneId: string): void {
  arenaStore.setDragPaneId(paneId)
  lastLiveTargetId.value = ''
}

function onDragOver(paneId: string, event: DragEvent): void {
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  const fromId = arenaStore.dragPaneId
  if (!fromId) return
  // Cursor is over the dragged pane itself after a live swap — mark settled.
  if (fromId === paneId) {
    lastLiveTargetId.value = fromId
    return
  }
  // Live reorder so columns physically follow the drag (not only on drop).
  if (lastLiveTargetId.value === paneId) return
  lastLiveTargetId.value = paneId
  if (liveMoveRaf) cancelAnimationFrame(liveMoveRaf)
  liveMoveRaf = requestAnimationFrame(() => {
    liveMoveRaf = 0
    if (arenaStore.dragPaneId !== fromId) return
    if (!arenaStore.panes.some((pane) => pane.id === paneId)) return
    arenaStore.movePane(fromId, paneId, { persist: false })
    // After swap, allow re-entering neighbors (don't stick on previous target).
    lastLiveTargetId.value = fromId
  })
}

function onDrop(event: DragEvent): void {
  event.preventDefault()
  finishDrag()
}

function onDragEnd(): void {
  finishDrag()
}

function finishDrag(): void {
  if (liveMoveRaf) {
    cancelAnimationFrame(liveMoveRaf)
    liveMoveRaf = 0
  }
  lastLiveTargetId.value = ''
  // Persist whatever live reorder already applied.
  if (arenaStore.dragPaneId) {
    arenaStore.flushPersist()
  }
  arenaStore.clearDragPaneId()
}

function addPane(): void {
  const seed = arenaStore.focusedPane?.runtime || appStore.activeRuntime
  if (!arenaStore.addPane(seed)) return
  const pane = arenaStore.focusedPane
  if (pane) onFocusPane(pane.id)
}

function onDuplicate(paneId: string): void {
  if (!arenaStore.duplicatePane(paneId)) return
  onFocusPane(arenaStore.focusedPaneId)
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex h-8 shrink-0 items-center justify-between gap-2 border-b border-border/70 bg-muted/30 px-3">
      <div class="flex min-w-0 items-center gap-2">
        <p class="truncate text-[11px] font-medium text-foreground">{{ t('arena.title') }}</p>
        <p class="hidden truncate text-[10px] text-muted-foreground sm:block">
          {{ t('arena.hintExtended', { count: arenaStore.panes.length, max: arenaStore.maxPanes }) }}
        </p>
      </div>
      <div class="flex shrink-0 items-center gap-0.5">
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          :class="arenaStore.panes.length === 2 ? 'bg-background text-foreground shadow-sm' : ''"
          :aria-label="t('arena.columns2')"
          @click="arenaStore.setColumnCount(2, appStore.activeRuntime)"
        >
          <Columns2 :size="13" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          :class="arenaStore.panes.length === 3 ? 'bg-background text-foreground shadow-sm' : ''"
          :aria-label="t('arena.columns3')"
          @click="arenaStore.setColumnCount(3, appStore.activeRuntime)"
        >
          <Columns3 :size="13" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          :class="arenaStore.panes.length === 4 ? 'bg-background text-foreground shadow-sm' : ''"
          :aria-label="t('arena.columns4')"
          @click="arenaStore.setColumnCount(4, appStore.activeRuntime)"
        >
          <Columns4 :size="13" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          class="h-7 px-2 text-[11px]"
          :disabled="!arenaStore.canAddPane"
          :aria-label="t('arena.addPane')"
          @click="addPane"
        >
          <Plus :size="12" class="mr-1" />
          {{ t('arena.addPane') }}
          <span class="ml-1 text-[10px] text-muted-foreground">
            {{ arenaStore.panes.length }}/{{ arenaStore.maxPanes }}
          </span>
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          class="h-7 px-2 text-[11px]"
          @click="arenaStore.closeArena()"
        >
          <X :size="12" class="mr-1" />
          {{ t('arena.exit') }}
        </Button>
      </div>
    </div>

    <div class="arena-pane-scroller min-h-0 flex-1 overflow-x-auto overflow-y-hidden">
      <TransitionGroup
        name="arena-pane"
        tag="div"
        class="grid h-full min-h-0 divide-x divide-border/70"
        :style="paneGridStyle"
      >
      <div
        v-for="pane in arenaStore.panes"
        :key="pane.id"
        class="arena-pane-cell relative min-h-0 min-w-0"
        :class="arenaStore.dragPaneId === pane.id ? 'z-10 opacity-90' : ''"
        @dragover="onDragOver(pane.id, $event)"
        @drop="onDrop($event)"
      >
        <ArenaChatPane
          :pane-id="pane.id"
          :runtime="pane.runtime"
          :focused="pane.id === arenaStore.focusedPaneId"
          :dragging="arenaStore.dragPaneId === pane.id"
          class="h-full min-h-0 min-w-0"
          @focus="onFocusPane(pane.id)"
          @show-inspector="emit('show-inspector')"
          @drag-start-pane="onDragStart"
          @drag-end-pane="onDragEnd"
          @duplicate="onDuplicate(pane.id)"
        />
      </div>
      </TransitionGroup>
  </div>
</div>
</template>

<style scoped>
/* FLIP move: other panes slide into place as the dragged pane reorders live. */
.arena-pane-move {
  transition: transform 0.22s cubic-bezier(0.2, 0.8, 0.2, 1);
  will-change: transform;
}

.arena-pane-enter-active,
.arena-pane-leave-active {
  transition: opacity 0.16s ease, transform 0.16s ease;
}

.arena-pane-enter-from,
.arena-pane-leave-to {
  opacity: 0;
  transform: scale(0.98);
}

.arena-pane-leave-active {
  position: absolute;
  width: 0;
  height: 0;
  overflow: hidden;
  pointer-events: none;
}

.arena-pane-scroller {
  scrollbar-color: color-mix(in oklab, var(--muted-foreground) 28%, transparent) transparent;
  scrollbar-width: thin;
}

.arena-pane-scroller::-webkit-scrollbar {
  height: 0.48rem;
}

.arena-pane-scroller::-webkit-scrollbar-track {
  background: transparent;
}

.arena-pane-scroller::-webkit-scrollbar-thumb {
  border: 2px solid transparent;
  border-radius: 999px;
  background: color-mix(in oklab, var(--muted-foreground) 28%, transparent);
  background-clip: padding-box;
}

.arena-pane-scroller::-webkit-scrollbar-thumb:hover {
  background: color-mix(in oklab, var(--muted-foreground) 46%, transparent);
  background-clip: padding-box;
}

@media (prefers-reduced-motion: reduce) {
  .arena-pane-move,
  .arena-pane-enter-active,
  .arena-pane-leave-active {
    transition: none;
  }
}
</style>
