<script setup lang="ts">
import { ArrowDown } from '@lucide/vue'
import { computed, nextTick, onMounted, onUnmounted, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { useCodexStore } from '@/stores'
import type { TimelineItem } from '@/types/codex'
import ChatMessageGroup from './ChatMessageGroup.vue'

const codexStore = useCodexStore()
const { t } = useI18n()

const emit = defineEmits<{
  retry: [itemID: string]
  rollback: [payload: { turnId: string; mode: 'single' | 'fromHere' }]
  'inspect-diff': [payload: { path: string; diff: string }]
}>()

const scrollAreaRef = useTemplateRef<HTMLElement>('scrollAreaRef')
const contentRef = useTemplateRef<HTMLElement>('contentRef')
const scrollFrame = shallowRef<number | null>(null)
const renderLimit = shallowRef(80)
const showJumpBottom = shallowRef(false)
let pendingScrollForce = false
let forceScrollUntil = 0
let resizeObserver: ResizeObserver | null = null
const isLoading = computed(() => codexStore.loadingThreadId === codexStore.activeThreadId && codexStore.activeThreadId !== '')

interface MessageGroup {
  kind: 'user' | 'agent'
  items: TimelineItem[]
  turnId: string
}

const groups = computed<MessageGroup[]>(() => {
  const result: MessageGroup[] = []
  for (const item of codexStore.activeItems) {
    const kind = item.type === 'userMessage' ? 'user' : 'agent'
    const last = result[result.length - 1]
    if (last && last.kind === kind && last.turnId === item.turnId) {
      last.items.push(item)
    } else {
      result.push({ kind, items: [item], turnId: item.turnId })
    }
  }
  return result
})

const turnMarkers = computed(() => {
  const seen = new Set<string>()
  return groups.value.flatMap((group, index) => {
    if (!group.turnId || seen.has(group.turnId)) return []
    seen.add(group.turnId)
    return [{ id: group.turnId, index: index + 1, failed: group.items.some((item) => item.failed) }]
  })
})

const renderedGroups = computed(() => {
  const start = Math.max(0, groups.value.length - renderLimit.value)
  return groups.value.slice(start).map((group, index) => ({ group, index: start + index }))
})

const lastItemSignature = computed(() => {
  const item = codexStore.activeItems.at(-1)
  return item
    ? `${item.id}:${item.text.length}:${item.output.length}:${item.reasoningSummary?.length ?? 0}:${item.status}`
    : ''
})

const activeTurnKey = computed(() =>
  codexStore.activeTurnId
  || codexStore.activeTurnFeedback?.turnId
  || '',
)

const lastStreamingTurnId = computed(() => {
  if (!codexStore.isTurnRunning && codexStore.activeTurnFeedback?.state !== 'running') return ''
  const turnID = activeTurnKey.value
  if (turnID) {
    const agentGroup = [...groups.value].reverse().find((group) => group.kind === 'agent' && group.turnId === turnID)
    if (agentGroup) return agentGroup.turnId
  }
  return groups.value.at(-1)?.kind === 'agent' ? (groups.value.at(-1)?.turnId ?? '') : ''
})

const showThinking = computed(() => {
  if (isLoading.value) return false
  const feedback = codexStore.activeTurnFeedback
  const waiting = codexStore.sendingMessage
    || codexStore.isTurnRunning
    || feedback?.state === 'submitting'
    || feedback?.state === 'running'
  if (!waiting) return false

  const turnID = activeTurnKey.value
  const hasAgentItem = codexStore.activeItems.some((item) => {
    if (item.type === 'userMessage') return false
    if (turnID && item.turnId && item.turnId !== turnID) return false
    return true
  })
  return !hasAgentItem
})

const thinkingLabel = computed(() => {
  const feedback = codexStore.activeTurnFeedback
  if (feedback?.message) return feedback.message
  return t('chat.thinking')
})

function distanceFromBottom(): number {
  const container = scrollAreaRef.value
  if (!container) return 0
  return container.scrollHeight - container.scrollTop - container.clientHeight
}

function updateJumpBottom(): void {
  showJumpBottom.value = distanceFromBottom() > 220
}

async function scrollToBottom(force = false): Promise<void> {
  await nextTick()
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
  const container = scrollAreaRef.value
  if (!container) return
  const distance = distanceFromBottom()
  if (!force && distance > 180) {
    updateJumpBottom()
    return
  }
  const behavior: ScrollBehavior = force || codexStore.isTurnRunning || showThinking.value ? 'auto' : 'smooth'
  container.scrollTo({ top: container.scrollHeight, behavior })
  showJumpBottom.value = false
}

function scheduleScroll(force = false): void {
  pendingScrollForce = pendingScrollForce || force
  if (scrollFrame.value !== null) cancelAnimationFrame(scrollFrame.value)
  scrollFrame.value = requestAnimationFrame(() => {
    scrollFrame.value = null
    const shouldForce = pendingScrollForce
    pendingScrollForce = false
    void scrollToBottom(shouldForce)
  })
}

function isFirstTurnGroup(index: number, turnID: string): boolean {
  return groups.value.findIndex((group) => group.turnId === turnID) === index
}

function jumpToTurn(turnID: string): void {
  const index = groups.value.findIndex((group) => group.turnId === turnID)
  if (index < 0) return
  if (index < groups.value.length - renderLimit.value) {
    renderLimit.value = groups.value.length - index + 20
    void nextTick().then(() => document.getElementById(`conversation-turn-${turnID}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' }))
    return
  }
  document.getElementById(`conversation-turn-${turnID}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
}

async function loadEarlier(): Promise<void> {
  const container = scrollAreaRef.value
  if (!container) return
  const previousHeight = container.scrollHeight
  renderLimit.value = Math.min(groups.value.length, renderLimit.value + 80)
  await nextTick()
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
  container.scrollTop += container.scrollHeight - previousHeight
}

function onScroll(): void {
  updateJumpBottom()
}

watch(() => codexStore.activeItems.length, () => scheduleScroll())
watch(lastItemSignature, () => scheduleScroll())
watch(showThinking, (visible) => {
  if (visible) scheduleScroll(true)
})
watch(() => codexStore.activeThreadId, () => {
  renderLimit.value = 80
  forceScrollUntil = performance.now() + 600
  scheduleScroll(true)
})
watch(isLoading, (loading) => {
  if (!loading) {
    forceScrollUntil = performance.now() + 600
    scheduleScroll(true)
  }
})

onMounted(() => {
  resizeObserver = new ResizeObserver(() => scheduleScroll(performance.now() < forceScrollUntil))
  if (contentRef.value) resizeObserver.observe(contentRef.value)
  scrollAreaRef.value?.addEventListener('scroll', onScroll, { passive: true })
})

onUnmounted(() => {
  resizeObserver?.disconnect()
  resizeObserver = null
  scrollAreaRef.value?.removeEventListener('scroll', onScroll)
  if (scrollFrame.value !== null) cancelAnimationFrame(scrollFrame.value)
})
</script>

<template>
  <div ref="scrollAreaRef" class="scrollbar-thin relative h-full overflow-y-auto">
    <div ref="contentRef" class="mx-auto max-w-[680px] space-y-6 px-4 pb-8 pt-5 sm:px-6">
      <div v-if="isLoading" class="space-y-3.5">
        <Skeleton class="h-10 w-3/4 rounded-md" />
        <div class="space-y-1.5">
          <Skeleton class="h-3 w-full rounded" />
          <Skeleton class="h-3 w-[92%] rounded" />
          <Skeleton class="h-3 w-[70%] rounded" />
        </div>
        <Skeleton class="h-9 w-2/3 rounded-md" />
        <div class="space-y-1.5">
          <Skeleton class="h-3 w-[88%] rounded" />
          <Skeleton class="h-3 w-[60%] rounded" />
        </div>
      </div>

      <div v-if="renderLimit < groups.length" class="flex items-center gap-3 py-1">
        <div class="h-px flex-1 bg-border/70" />
        <Button
          variant="ghost"
          size="sm"
          class="h-6 shrink-0 px-2 text-[11px] text-muted-foreground"
          @click="loadEarlier"
        >
          {{ $t('chat.loadEarlier', { count: Math.min(80, groups.length - renderLimit) }) }}
        </Button>
        <div class="h-px flex-1 bg-border/70" />
      </div>

      <div
        v-for="entry in renderedGroups"
        :id="isFirstTurnGroup(entry.index, entry.group.turnId) ? `conversation-turn-${entry.group.turnId}` : undefined"
        :key="`${entry.group.turnId}:${entry.index}`"
        class="scroll-mt-8"
      >
        <ChatMessageGroup
          :kind="entry.group.kind"
          :items="entry.group.items"
          :metrics="codexStore.activeTurnMetrics[entry.group.turnId]"
          :animated="entry.index >= groups.length - 6"
          :streaming="entry.group.kind === 'agent' && entry.group.turnId === lastStreamingTurnId"
          :turn-diff="codexStore.diffsByTurn[entry.group.turnId] || ''"
          @retry="emit('retry', $event)"
          @rollback="emit('rollback', $event)"
          @inspect-diff="emit('inspect-diff', $event)"
        />
      </div>

      <Transition name="timeline-step">
        <div
          v-if="showThinking"
          key="timeline-thinking"
          class="timeline-thinking flex items-center gap-2.5 py-1.5 text-[13px] text-muted-foreground"
        >
          <span class="thinking-dots" aria-hidden="true">
            <span />
            <span />
            <span />
          </span>
          <span class="min-w-0">
            <span class="text-foreground/75">{{ thinkingLabel }}</span>
          </span>
        </div>
      </Transition>
    </div>

    <button
      v-if="showJumpBottom"
      type="button"
      class="absolute bottom-4 left-1/2 z-10 flex h-8 -translate-x-1/2 items-center gap-1.5 rounded-full border border-border/70 bg-card/95 px-3 text-[11px] text-muted-foreground shadow-sm backdrop-blur transition-colors hover:text-foreground"
      @click="scrollToBottom(true)"
    >
      <ArrowDown :size="12" />
      {{ $t('chat.jumpLatest', 'Latest') }}
    </button>

    <nav
      v-if="turnMarkers.length > 1"
      class="absolute right-2 top-1/2 z-10 hidden max-h-[50vh] -translate-y-1/2 flex-col items-end gap-1 overflow-y-auto py-2 lg:flex"
      :aria-label="$t('chat.turnNavigation')"
    >
      <button
        v-for="marker in turnMarkers"
        :key="marker.id"
        type="button"
        class="h-1 rounded-full transition-[width,background-color] hover:w-3.5"
        :class="marker.failed
          ? 'w-3 bg-destructive'
          : marker.index % 5 === 0
            ? 'w-2.5 bg-muted-foreground/40'
            : 'w-1.5 bg-muted-foreground/25'"
        :title="$t('chat.jumpTurn', { index: marker.index })"
        @click="jumpToTurn(marker.id)"
      />
    </nav>
  </div>
</template>
