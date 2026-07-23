<script setup lang="ts">
import { ArrowDown, ChevronsDown, ChevronsUp } from '@lucide/vue'
import { computed, nextTick, onMounted, onUnmounted, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
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
const stickToBottom = shallowRef(true)
let pendingScrollForce = false
let resizeObserver: ResizeObserver | null = null
let settleToken = 0

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

const turnOrder = computed(() => {
  const ids: string[] = []
  const seen = new Set<string>()
  for (const group of groups.value) {
    if (!group.turnId || seen.has(group.turnId)) continue
    seen.add(group.turnId)
    ids.push(group.turnId)
  }
  return ids
})

const userMarkers = computed(() => {
  const markers: Array<{
    groupIndex: number
    index: number
    preview: string
    failed: boolean
  }> = []
  groups.value.forEach((group, groupIndex) => {
    if (group.kind !== 'user') return
    const text = group.items.map((item) => item.text).join(' ').replace(/\s+/g, ' ').trim()
    const preview = text
      ? (text.length > 80 ? `${text.slice(0, 80)}…` : text)
      : t('chat.userMessageFallback', { index: markers.length + 1 })
    markers.push({
      groupIndex,
      index: markers.length + 1,
      preview,
      failed: group.items.some((item) => item.failed),
    })
  })
  return markers
})

/** Cap rail DOM nodes on very long threads; keep first/last + evenly sampled ticks. */
const visibleUserMarkers = computed(() => {
  const all = userMarkers.value
  if (all.length <= 36) return all
  const out: typeof all = []
  const last = all.length - 1
  const slots = 34
  const picked = new Set<number>([0, last])
  for (let i = 1; i <= slots; i += 1) {
    picked.add(Math.round((i / (slots + 1)) * last))
  }
  ;[...picked].sort((a, b) => a - b).forEach((index) => {
    const marker = all[index]
    if (marker) out.push(marker)
  })
  return out
})

/** Tall enough to read; only compresses when markers exceed the track. */
const userNavRailStyle = computed(() => {
  const count = visibleUserMarkers.value.length
  if (count <= 0) return undefined
  if (count === 1) return { height: '16px' }
  // Ideal center-to-center gap before compression.
  const idealStep = 17
  const idealHeight = Math.round((count - 1) * idealStep + 14)
  // ~48vh keeps the rail usable without dominating the chat pane.
  return { height: `min(${idealHeight}px, 48vh, 440px)` }
})

function markerTopPercent(index: number): string {
  const count = visibleUserMarkers.value.length
  if (count <= 1) return '50%'
  return `${(index / (count - 1)) * 100}%`
}

const renderedGroups = computed(() => {
  const start = Math.max(0, groups.value.length - renderLimit.value)
  return groups.value.slice(start).map((group, index) => ({ group, index: start + index }))
})

const lastItemSignature = computed(() => {
  const item = codexStore.activeItems.at(-1)
  if (!item) return ''
  // While streaming, bucket length so we don't scroll-schedule on every character.
  if (codexStore.isTurnRunning) {
    const bucket = Math.floor((item.text.length + item.output.length) / 480)
    return `${item.id}:${item.status}:${bucket}`
  }
  return `${item.id}:${item.text.length}:${item.output.length}:${item.reasoningSummary?.length ?? 0}:${item.status}`
})

const activeTurnKey = computed(() =>
  codexStore.activeTurnId
  || codexStore.activeTurnFeedback?.turnId
  || '',
)

const lastStreamingTurnId = computed(() => {
  if (!codexStore.isTurnRunning && codexStore.activeTurnFeedback?.state !== 'running') return ''
  const turnID = activeTurnKey.value
  if (!turnID) return ''
  // Only mark the agent group for the active turn. Falling back to the previous
  // agent group made the last reply look "thinking" again while a new turn waits.
  const agentGroup = [...groups.value].reverse().find((group) => group.kind === 'agent' && group.turnId === turnID)
  return agentGroup?.turnId ?? ''
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
    if (turnID) {
      // While the turn id is known, only current-turn agent items count.
      return Boolean(item.turnId) && item.turnId === turnID
    }
    // Before turn id arrives, ignore historical agent items so the footer
    // thinking row can still appear after the latest user message.
    return false
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

async function waitFrame(): Promise<void> {
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
}

async function scrollToBottom(force = false): Promise<void> {
  await nextTick()
  await waitFrame()
  const container = scrollAreaRef.value
  if (!container) return
  const distance = distanceFromBottom()
  const shouldStick = force || stickToBottom.value
  if (!shouldStick && distance > 180) {
    updateJumpBottom()
    return
  }
  const instant = shouldStick || codexStore.isTurnRunning || showThinking.value
  if (instant) container.scrollTop = container.scrollHeight
  else container.scrollTo({ top: container.scrollHeight, behavior: 'smooth' })
  showJumpBottom.value = false
}

function scheduleScroll(force = false): void {
  pendingScrollForce = pendingScrollForce || force
  if (scrollFrame.value !== null) cancelAnimationFrame(scrollFrame.value)
  scrollFrame.value = requestAnimationFrame(() => {
    scrollFrame.value = null
    const shouldForce = pendingScrollForce || stickToBottom.value
    pendingScrollForce = false
    void scrollToBottom(shouldForce)
  })
}

/** Keep pinning to bottom while long threads finish rendering (markdown/images/layout). */
async function settleToBottom(): Promise<void> {
  stickToBottom.value = true
  const token = ++settleToken
  let previousHeight = -1
  let stableFrames = 0
  // Fewer frames while a turn is running — pin with scrollTop instead of long settle loops.
  const maxFrames = codexStore.isTurnRunning || showThinking.value ? 8 : 24
  for (let i = 0; i < maxFrames; i++) {
    if (token !== settleToken) return
    await nextTick()
    await waitFrame()
    const container = scrollAreaRef.value
    if (!container) return
    container.scrollTop = container.scrollHeight
    showJumpBottom.value = false
    const height = container.scrollHeight
    if (height === previousHeight) {
      stableFrames += 1
      if (stableFrames >= 2 && !isLoading.value) break
    } else {
      stableFrames = 0
      previousHeight = height
    }
  }
}

function isFirstTurnGroup(index: number, turnID: string): boolean {
  return groups.value.findIndex((group) => group.turnId === turnID) === index
}

async function ensureGroupRendered(groupIndex: number): Promise<void> {
  const start = Math.max(0, groups.value.length - renderLimit.value)
  if (groupIndex >= start) return
  renderLimit.value = groups.value.length - groupIndex + 24
  await nextTick()
  await waitFrame()
}

async function jumpToUserMessage(groupIndex: number): Promise<void> {
  stickToBottom.value = false
  settleToken += 1
  await ensureGroupRendered(groupIndex)
  const byData = contentRef.value?.querySelector(`[data-group-index="${groupIndex}"]`) as HTMLElement | null
  const byTurn = groups.value[groupIndex]
    ? document.getElementById(`conversation-turn-${groups.value[groupIndex].turnId}`)
    : null
  ;(byData || byTurn)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  updateJumpBottom()
}

async function scrollToTop(): Promise<void> {
  stickToBottom.value = false
  settleToken += 1
  const container = scrollAreaRef.value
  if (!container) return
  // Expand in chunks instead of mounting the entire history at once.
  if (renderLimit.value < groups.value.length) {
    renderLimit.value = Math.min(groups.value.length, renderLimit.value + 160)
    await nextTick()
    await waitFrame()
  }
  container.scrollTo({ top: 0, behavior: 'smooth' })
  updateJumpBottom()
}

async function jumpToLatest(): Promise<void> {
  stickToBottom.value = true
  await settleToBottom()
}

async function loadEarlier(): Promise<void> {
  const container = scrollAreaRef.value
  if (!container) return
  const previousHeight = container.scrollHeight
  const previousTop = container.scrollTop
  stickToBottom.value = false
  renderLimit.value = Math.min(groups.value.length, renderLimit.value + 80)
  await nextTick()
  await waitFrame()
  container.scrollTop = previousTop + (container.scrollHeight - previousHeight)
}

function onScroll(): void {
  const distance = distanceFromBottom()
  showJumpBottom.value = distance > 220
  if (distance <= 64) {
    stickToBottom.value = true
  } else if (distance > 180) {
    stickToBottom.value = false
  }
}

watch(() => codexStore.activeItems.length, () => scheduleScroll())
watch(lastItemSignature, () => scheduleScroll())
watch(showThinking, (visible) => {
  if (visible) scheduleScroll(true)
})
watch(() => codexStore.activeThreadId, () => {
  renderLimit.value = 80
  stickToBottom.value = true
  void settleToBottom()
})
watch(isLoading, (loading, wasLoading) => {
  if (wasLoading && !loading) {
    stickToBottom.value = true
    void settleToBottom()
  }
})

onMounted(() => {
  resizeObserver = new ResizeObserver(() => {
    if (stickToBottom.value) scheduleScroll(true)
    else updateJumpBottom()
  })
  if (contentRef.value) resizeObserver.observe(contentRef.value)
  scrollAreaRef.value?.addEventListener('scroll', onScroll, { passive: true })
  if (codexStore.activeThreadId) void settleToBottom()
})

onUnmounted(() => {
  settleToken += 1
  resizeObserver?.disconnect()
  resizeObserver = null
  scrollAreaRef.value?.removeEventListener('scroll', onScroll)
  if (scrollFrame.value !== null) cancelAnimationFrame(scrollFrame.value)
})
</script>

<template>
  <div class="relative h-full min-h-0">
    <div ref="scrollAreaRef" class="scrollbar-thin h-full overflow-y-auto pr-10">
      <div ref="contentRef" class="mx-auto max-w-[680px] space-y-6 px-4 pb-8 pt-5 sm:px-6">
        <div v-if="isLoading" class="space-y-5" :aria-busy="true" :aria-label="$t('chat.loadingThread')">
          <p class="text-[12px] text-muted-foreground">{{ $t('chat.loadingThread') }}</p>
          <div class="space-y-3.5">
            <Skeleton class="ml-auto h-10 w-3/4 max-w-md rounded-md" />
            <div class="space-y-1.5">
              <Skeleton class="h-3 w-full rounded" />
              <Skeleton class="h-3 w-[92%] rounded" />
              <Skeleton class="h-3 w-[70%] rounded" />
            </div>
            <Skeleton class="h-9 w-2/3 max-w-sm rounded-md" />
            <div class="space-y-1.5">
              <Skeleton class="h-3 w-[88%] rounded" />
              <Skeleton class="h-3 w-[60%] rounded" />
            </div>
          </div>
        </div>

        <div
          v-else-if="groups.length === 0"
          class="flex flex-col items-center justify-center gap-1.5 py-16 text-center"
        >
          <p class="text-[13px] font-medium text-foreground/80">{{ $t('chat.emptyThread') }}</p>
          <p class="max-w-xs text-[12px] leading-5 text-muted-foreground">{{ $t('chat.emptyThreadHint') }}</p>
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
          :id="isFirstTurnGroup(entry.index, entry.group.turnId)
            ? `conversation-turn-${entry.group.turnId}`
            : `conversation-group-${entry.index}`"
          :data-group-index="entry.index"
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
            :turn-index="turnOrder.indexOf(entry.group.turnId)"
            :turn-count="turnOrder.length"
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
    </div>

    <button
      v-if="showJumpBottom"
      type="button"
      class="absolute bottom-4 left-1/2 z-20 flex h-8 -translate-x-1/2 items-center gap-1.5 rounded-full border border-border/70 bg-card/95 px-3 text-[11px] text-muted-foreground shadow-sm backdrop-blur transition-colors hover:text-foreground"
      @click="jumpToLatest"
    >
      <ArrowDown :size="12" />
      {{ $t('chat.jumpLatest', 'Latest') }}
    </button>

    <nav
      v-if="groups.length > 0"
      class="pointer-events-none absolute right-2 top-1/2 z-30 flex -translate-y-1/2 flex-col items-center gap-2.5"
      :aria-label="$t('chat.userNavigation')"
    >
      <button
        type="button"
        class="pointer-events-auto relative z-[1] grid size-8 place-items-center rounded-full border border-border bg-card/95 text-foreground/80 shadow-md backdrop-blur transition-colors hover:border-primary/40 hover:text-foreground"
        :title="$t('chat.scrollToTop')"
        :aria-label="$t('chat.scrollToTop')"
        @click="scrollToTop"
      >
        <ChevronsUp :size="15" />
      </button>

      <div
        v-if="userMarkers.length > 0"
        class="user-nav-rail pointer-events-auto relative z-[1] w-4 overflow-visible"
        :style="userNavRailStyle"
      >
        <TooltipProvider :delay-duration="160" :disable-hoverable-content="true">
          <Tooltip v-for="(marker, markerIndex) in visibleUserMarkers" :key="`${marker.groupIndex}:${marker.index}`">
            <TooltipTrigger as-child>
              <button
                type="button"
                class="user-nav-tick absolute left-1/2"
                :class="marker.failed ? 'is-failed' : ''"
                :style="{ top: markerTopPercent(markerIndex), zIndex: markerIndex + 1 }"
                :aria-label="$t('chat.jumpUserMessage', { index: marker.index, preview: marker.preview })"
                @click="jumpToUserMessage(marker.groupIndex)"
              />
            </TooltipTrigger>
            <TooltipContent
              side="left"
              :side-offset="10"
              class="max-w-[240px] border-0 bg-card px-2.5 py-1.5 text-left text-foreground shadow-lg"
              arrow-class="!translate-y-0 border-0 bg-card fill-card shadow-none"
            >
              <p class="text-[10px] font-medium text-muted-foreground">
                {{ $t('chat.userMessageLabel', { index: marker.index }) }}
              </p>
              <p class="mt-0.5 text-[11px] leading-4 text-foreground/90">{{ marker.preview }}</p>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>

      <button
        type="button"
        class="pointer-events-auto relative z-[1] grid size-8 place-items-center rounded-full border border-border bg-card/95 text-foreground/80 shadow-md backdrop-blur transition-colors hover:border-primary/40 hover:text-foreground"
        :title="$t('chat.scrollToBottom')"
        :aria-label="$t('chat.scrollToBottom')"
        @click="jumpToLatest"
      >
        <ChevronsDown :size="15" />
      </button>
    </nav>
  </div>
</template>
