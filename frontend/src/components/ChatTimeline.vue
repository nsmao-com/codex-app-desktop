<script setup lang="ts">
import { ArrowDown, ChevronsDown, ChevronsUp } from '@lucide/vue'
import { computed, nextTick, onMounted, onUnmounted, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  SimpleTooltip,
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useAppStore, useClaudeStore, useCodexStore, useGrokStore } from '@/stores'
import type { TimelineItem } from '@/types/codex'
import ChatMessageGroup from './ChatMessageGroup.vue'

const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const { t } = useI18n()

const timelineItems = computed(() => {
  if (appStore.isGrokMode) return grokStore.activeItems
  if (appStore.isClaudeMode) return claudeStore.activeItems
  return codexStore.activeItems
})
const timelineThreadId = computed(() => {
  if (appStore.isGrokMode) return grokStore.activeSessionId
  if (appStore.isClaudeMode) return claudeStore.activeSessionId
  return codexStore.activeThreadId
})
const timelineLoading = computed(() => {
  if (appStore.isGrokMode) {
    return Boolean(grokStore.loadingSessionId && grokStore.loadingSessionId === grokStore.activeSessionId)
  }
  if (appStore.isClaudeMode) {
    return Boolean(claudeStore.loadingSessionId && claudeStore.loadingSessionId === claudeStore.activeSessionId)
  }
  return codexStore.loadingThreadId === codexStore.activeThreadId && codexStore.activeThreadId !== ''
})
const timelineTurnRunning = computed(() => {
  if (appStore.isGrokMode) return grokStore.isTurnRunning
  if (appStore.isClaudeMode) return claudeStore.isTurnRunning
  return codexStore.isTurnRunning
})

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
/** Distance past which we treat the user as having left the bottom (no sticky snap-back). */
const UNSTICK_DISTANCE = 48
/** Re-enable sticky only when the viewport is essentially flush with the bottom. */
const RESTICK_DISTANCE = 4
let pendingScrollForce = false
let resizeObserver: ResizeObserver | null = null
let settleToken = 0
/** Ignore scroll events caused by our own programmatic scrollTop writes. */
let ignoreScrollUntil = 0
let lastTouchY = 0
let resizeScrollCooldownUntil = 0
/** Delayed re-pin timers after thread open / layout settle. */
const settleFollowUpTimers: number[] = []

const isLoading = timelineLoading

interface MessageGroup {
  kind: 'user' | 'agent'
  items: TimelineItem[]
  turnId: string
}

const groups = computed<MessageGroup[]>(() => {
  const result: MessageGroup[] = []
  for (const item of timelineItems.value) {
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
  const all = groups.value
  const order = turnOrder.value
  const start = Math.max(0, all.length - renderLimit.value)
  return all.slice(start).map((group, index) => ({
    group,
    index: start + index,
    turnIndex: order.indexOf(group.turnId),
    turnCount: order.length,
  }))
})

const lastItemSignature = computed(() => {
  const item = timelineItems.value.at(-1)
  if (!item) return ''
  // While streaming, bucket length so we don't scroll-schedule on every character.
  if (timelineTurnRunning.value) {
    const bucket = Math.floor((item.text.length + item.output.length) / 720)
    return `${item.id}:${item.status}:${bucket}`
  }
  return `${item.id}:${item.text.length}:${item.output.length}:${item.reasoningSummary?.length ?? 0}:${item.status}`
})

const activeTurnKey = computed(() => {
  if (appStore.isGrokMode) return grokStore.activeTurn?.turnId || grokStore.activeSessionId || ''
  if (appStore.isClaudeMode) return claudeStore.activeTurn?.turnId || claudeStore.activeSessionId || ''
  return codexStore.activeTurnId || codexStore.activeTurnFeedback?.turnId || ''
})

/**
 * Timeline turn id currently streaming in an agent group.
 * Grok/Claude items use `${sessionId}:tN`, NOT the runtime `activeTurn.turnId`
 * (`grok-turn-…`). Returning a non-matching runtime id used to suppress the
 * footer "正在思考" row while no agent group had `streaming=true` — blank wait.
 */
const lastStreamingTurnId = computed(() => {
  if (appStore.isGrokMode || appStore.isClaudeMode) {
    const running = appStore.isGrokMode
      ? (grokStore.isTurnRunning || grokStore.sending)
      : (claudeStore.isTurnRunning || claudeStore.sending)
    if (!running) return ''
    // Only mark a group as streaming when it actually exists on the timeline.
    // Right after send the last row is the user bubble — return '' so the
    // Codex-style footer thinking shimmer can show.
    const lastGroup = groups.value[groups.value.length - 1]
    if (lastGroup?.kind === 'agent') return lastGroup.turnId
    // Live thought/text share the user turn id; if an agent group for that
    // turn already exists above a trailing user row (rare), prefer it.
    if (lastGroup?.kind === 'user' && lastGroup.turnId) {
      const agentForTurn = [...groups.value].reverse().find(
        (group) => group.kind === 'agent' && group.turnId === lastGroup.turnId,
      )
      if (agentForTurn) return agentForTurn.turnId
    }
    return ''
  }
  if (!codexStore.isTurnRunning && codexStore.activeTurnFeedback?.state !== 'running') return ''
  const turnID = activeTurnKey.value
  if (!turnID) return ''
  const agentGroup = [...groups.value].reverse().find((group) => group.kind === 'agent' && group.turnId === turnID)
  return agentGroup?.turnId ?? ''
})

const showThinking = computed(() => {
  if (isLoading.value) return false
  // The streaming agent group already owns thinking / reasoning UI — never duplicate it below.
  if (lastStreamingTurnId.value) return false
  if (appStore.isGrokMode) {
    // Mirror Codex: show footer shimmer until the first live agent activity lands.
    if (!(grokStore.sending || grokStore.isTurnRunning)) return false
    return !timelineItems.value.some((item) => (
      item.type !== 'userMessage'
      && (
        item.status === 'inProgress'
        || item.status === 'running'
        || item.status === 'pending'
        || item.id.startsWith('grok-live-')
        || item.id.startsWith('grok-thought-')
      )
    ))
  }
  if (appStore.isClaudeMode) {
    if (!(claudeStore.sending || claudeStore.isTurnRunning)) return false
    return !timelineItems.value.some((item) => (
      item.type !== 'userMessage'
      && (item.status === 'inProgress' || item.status === 'running' || item.status === 'pending')
    ))
  }
  const feedback = codexStore.activeTurnFeedback
  const waiting = codexStore.sendingMessage
    || codexStore.isTurnRunning
    || feedback?.state === 'submitting'
    || feedback?.state === 'running'
  if (!waiting) return false

  const turnID = activeTurnKey.value
  const hasTurnActivity = timelineItems.value.some((item) => {
    if (item.type === 'userMessage') return false
    if (turnID) {
      // Prefer current-turn items; also accept live items that have not received turnId yet.
      if (item.turnId === turnID) return true
      if (!item.turnId && (item.status === 'inProgress' || item.status === 'running' || item.status === 'pending')) {
        return true
      }
      return false
    }
    // Before turn id arrives, only count live non-user items so historical replies
    // do not suppress the footer thinking row.
    return item.status === 'inProgress' || item.status === 'running' || item.status === 'pending'
  })
  return !hasTurnActivity
})

const thinkingLabel = computed(() => {
  if (appStore.isGrokMode || appStore.isClaudeMode) return t('chat.thinking')
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

function clearSettleFollowUps(): void {
  while (settleFollowUpTimers.length) {
    const id = settleFollowUpTimers.pop()
    if (id !== undefined) window.clearTimeout(id)
  }
}

/** User left the bottom — cancel settle loops and stop ResizeObserver snap-back. */
function unlockFromBottom(): void {
  stickToBottom.value = false
  settleToken += 1
  pendingScrollForce = false
  clearSettleFollowUps()
  if (scrollFrame.value !== null) {
    cancelAnimationFrame(scrollFrame.value)
    scrollFrame.value = null
  }
  updateJumpBottom()
}

function markProgrammaticScroll(holdMs = 80): void {
  // Must outlast multi-frame settle + layout growth, or onScroll will unstick mid-pin.
  ignoreScrollUntil = Math.max(ignoreScrollUntil, performance.now() + holdMs)
}

async function waitFrame(): Promise<void> {
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
}

function pinScrollTop(): void {
  const container = scrollAreaRef.value
  if (!container || !stickToBottom.value) return
  markProgrammaticScroll(160)
  container.scrollTop = container.scrollHeight
  showJumpBottom.value = false
}

async function scrollToBottom(force = false): Promise<void> {
  const container = scrollAreaRef.value
  if (!container) return
  if (!force && !stickToBottom.value) {
    updateJumpBottom()
    return
  }
  if (!stickToBottom.value) {
    updateJumpBottom()
    return
  }
  // During a live turn, pin synchronously — skip nextTick/rAF to avoid scroll jank.
  const live = timelineTurnRunning.value || showThinking.value
  if (live) {
    pinScrollTop()
    return
  }
  await nextTick()
  await waitFrame()
  if (!stickToBottom.value) {
    updateJumpBottom()
    return
  }
  markProgrammaticScroll(120)
  if (force) container.scrollTop = container.scrollHeight
  else container.scrollTo({ top: container.scrollHeight, behavior: 'smooth' })
  showJumpBottom.value = false
}

function scheduleScroll(force = false): void {
  if (!stickToBottom.value) {
    updateJumpBottom()
    return
  }
  pendingScrollForce = pendingScrollForce || force
  if (scrollFrame.value !== null) return
  scrollFrame.value = requestAnimationFrame(() => {
    scrollFrame.value = null
    if (!stickToBottom.value) {
      pendingScrollForce = false
      updateJumpBottom()
      return
    }
    const shouldForce = pendingScrollForce
    pendingScrollForce = false
    void scrollToBottom(shouldForce)
  })
}

type SettleOptions = {
  /** Wait until thread load skeleton is gone before measuring. */
  waitForLoad?: boolean
  /** How many rAF frames to keep re-pinning while height grows. */
  maxFrames?: number
  /** Schedule extra pins after the main loop (markdown/images). */
  followUp?: boolean
}

/**
 * Keep pinning to bottom while long threads finish rendering (markdown/images/layout).
 * Thread switch must wait for load + extra frames — otherwise we pin to skeleton / mid-height.
 */
async function settleToBottom(options: SettleOptions = {}): Promise<void> {
  stickToBottom.value = true
  const token = ++settleToken
  clearSettleFollowUps()

  const waitForLoad = options.waitForLoad === true
  if (waitForLoad) {
    // Wait for openThread to finish (or give up after ~3s).
    for (let i = 0; i < 180; i += 1) {
      if (token !== settleToken || !stickToBottom.value) return
      if (!isLoading.value) break
      await waitFrame()
    }
  }

  // If still loading (user switched again into another load), bail — next settle will run.
  if (isLoading.value) return

  let previousHeight = -1
  let stableFrames = 0
  const live = timelineTurnRunning.value || showThinking.value
  const maxFrames = options.maxFrames
    ?? (live ? 10 : 36)

  for (let i = 0; i < maxFrames; i += 1) {
    if (token !== settleToken || !stickToBottom.value) return
    // Thread switched mid-settle into a loading state — let that open own settle.
    if (isLoading.value) return
    await nextTick()
    await waitFrame()
    if (token !== settleToken || !stickToBottom.value || isLoading.value) return
    pinScrollTop()
    const container = scrollAreaRef.value
    if (!container) return
    const height = container.scrollHeight
    if (height === previousHeight) {
      stableFrames += 1
      // Need a few stable frames after load so markdown/code blocks can finish.
      if (stableFrames >= 3) break
    } else {
      stableFrames = 0
      previousHeight = height
    }
  }

  const wantFollowUp = options.followUp !== false && !live
  if (!wantFollowUp) return
  // Late layout: images, syntax highlight, fonts, tool rows.
  for (const delay of [100, 280, 560, 1000]) {
    const timer = window.setTimeout(() => {
      if (token !== settleToken || !stickToBottom.value || isLoading.value) return
      pinScrollTop()
      updateJumpBottom()
    }, delay)
    settleFollowUpTimers.push(timer)
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
  unlockFromBottom()
  await ensureGroupRendered(groupIndex)
  const byData = contentRef.value?.querySelector(`[data-group-index="${groupIndex}"]`) as HTMLElement | null
  const byTurn = groups.value[groupIndex]
    ? document.getElementById(`conversation-turn-${groups.value[groupIndex].turnId}`)
    : null
  ;(byData || byTurn)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  updateJumpBottom()
}

async function scrollToTop(): Promise<void> {
  unlockFromBottom()
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

/** User-clicked “scroll to bottom” — smooth like scrollToTop (auto-pin stays instant). */
async function jumpToLatest(): Promise<void> {
  stickToBottom.value = true
  const container = scrollAreaRef.value
  if (!container) return
  markProgrammaticScroll()
  container.scrollTo({ top: container.scrollHeight, behavior: 'smooth' })
  showJumpBottom.value = false
  // After the smooth scroll, re-pin once in case layout grew mid-animation.
  window.setTimeout(() => {
    if (!stickToBottom.value) return
    const el = scrollAreaRef.value
    if (!el) return
    if (el.scrollHeight - el.scrollTop - el.clientHeight > 8) {
      markProgrammaticScroll()
      el.scrollTop = el.scrollHeight
    }
    updateJumpBottom()
  }, 420)
}

async function loadEarlier(): Promise<void> {
  const container = scrollAreaRef.value
  if (!container) return
  const previousHeight = container.scrollHeight
  const previousTop = container.scrollTop
  unlockFromBottom()
  renderLimit.value = Math.min(groups.value.length, renderLimit.value + 80)
  await nextTick()
  await waitFrame()
  markProgrammaticScroll()
  container.scrollTop = previousTop + (container.scrollHeight - previousHeight)
}

function onScroll(): void {
  if (performance.now() < ignoreScrollUntil) return
  const distance = distanceFromBottom()
  showJumpBottom.value = distance > 220
  if (distance <= RESTICK_DISTANCE) {
    stickToBottom.value = true
  } else if (distance > UNSTICK_DISTANCE) {
    unlockFromBottom()
  }
}

/** Wheel/trackpad up = explicit leave-bottom intent (before layout resize can snap back). */
function onWheel(event: WheelEvent): void {
  if (event.deltaY < 0) unlockFromBottom()
}

function onTouchStart(event: TouchEvent): void {
  lastTouchY = event.touches[0]?.clientY ?? 0
}

function onTouchMove(event: TouchEvent): void {
  const y = event.touches[0]?.clientY ?? lastTouchY
  // Finger moves down → content scrolls up → leave bottom.
  if (y - lastTouchY > 6) unlockFromBottom()
  lastTouchY = y
}

function onKeyDown(event: KeyboardEvent): void {
  if (event.key === 'PageUp' || event.key === 'Home' || (event.key === 'ArrowUp' && !event.altKey)) {
    unlockFromBottom()
  }
}

watch(() => timelineItems.value.length, () => {
  if (stickToBottom.value) scheduleScroll(true)
})
watch(lastItemSignature, () => {
  if (stickToBottom.value) scheduleScroll(true)
})
watch(showThinking, (visible) => {
  if (visible && stickToBottom.value) scheduleScroll(true)
})
// Planning shimmer / final agent group height changes while stick is on.
watch(
  () => codexStore.activeTurnFeedback?.state,
  (state, prev) => {
    if (appStore.isGrokMode || !stickToBottom.value) return
    if (prev === 'running' || prev === 'submitting') {
      if (state === 'failed' || state === 'interrupted' || !state) {
        void settleToBottom({ maxFrames: 16, followUp: true })
      }
    }
  },
)
watch(timelineThreadId, () => {
  renderLimit.value = 80
  stickToBottom.value = true
  // Do not pin to skeleton/mid-layout: wait for load, then long settle + follow-ups.
  void settleToBottom({ waitForLoad: true, maxFrames: 48, followUp: true })
})
watch(isLoading, (loading, wasLoading) => {
  if (wasLoading && !loading) {
    stickToBottom.value = true
    void settleToBottom({ maxFrames: 48, followUp: true })
  }
})
/**
 * When a turn ends, final reply + file list may expand layout.
 * Prefer settling to bottom if the user was following (or still near the end),
 * instead of unlocking and leaving the viewport mid-thread.
 */
watch(timelineTurnRunning, (running, wasRunning) => {
  if (!wasRunning || running) return
  pendingScrollForce = false
  const nearBottom = distanceFromBottom() <= Math.max(UNSTICK_DISTANCE * 3, 160)
  if (stickToBottom.value || nearBottom) {
    stickToBottom.value = true
    void settleToBottom({ maxFrames: 20, followUp: true })
    return
  }
  settleToken += 1
  clearSettleFollowUps()
  updateJumpBottom()
})

onMounted(() => {
  resizeObserver = new ResizeObserver(() => {
    if (!stickToBottom.value) {
      updateJumpBottom()
      return
    }
    // While loading a thread, height is skeleton — skip until real content is up.
    if (isLoading.value) return
    // Coalesce layout-driven scroll pins while content streams in.
    const now = performance.now()
    if (timelineTurnRunning.value || showThinking.value) {
      if (now < resizeScrollCooldownUntil) return
      resizeScrollCooldownUntil = now + 80
    }
    // Force pin — smooth scroll mid-growth leaves the viewport mid-thread.
    scheduleScroll(true)
  })
  if (contentRef.value) resizeObserver.observe(contentRef.value)
  const area = scrollAreaRef.value
  area?.addEventListener('scroll', onScroll, { passive: true })
  area?.addEventListener('wheel', onWheel, { passive: true })
  area?.addEventListener('touchstart', onTouchStart, { passive: true })
  area?.addEventListener('touchmove', onTouchMove, { passive: true })
  area?.addEventListener('keydown', onKeyDown)
  if (timelineThreadId.value) {
    void settleToBottom({ waitForLoad: true, maxFrames: 48, followUp: true })
  }
})

onUnmounted(() => {
  settleToken += 1
  clearSettleFollowUps()
  resizeObserver?.disconnect()
  resizeObserver = null
  const area = scrollAreaRef.value
  area?.removeEventListener('scroll', onScroll)
  area?.removeEventListener('wheel', onWheel)
  area?.removeEventListener('touchstart', onTouchStart)
  area?.removeEventListener('touchmove', onTouchMove)
  area?.removeEventListener('keydown', onKeyDown)
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

        <!-- Hide real messages while loading so we never pin scroll to partial mid-thread. -->
        <template v-else>
          <div
            v-if="groups.length === 0"
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
              :metrics="appStore.isGrokMode
                ? grokStore.activeTurnMetrics[entry.group.turnId]
                : appStore.isClaudeMode
                  ? claudeStore.activeTurnMetrics[entry.group.turnId]
                  : codexStore.activeTurnMetrics[entry.group.turnId]"
              :animated="entry.index >= groups.length - 2"
              :streaming="entry.group.kind === 'agent' && entry.group.turnId === lastStreamingTurnId"
              :turn-diff="appStore.isCodexMode ? (codexStore.diffsByTurn[entry.group.turnId] || '') : ''"
              :allow-turn-actions="appStore.isCodexMode"
              :turn-index="entry.turnIndex"
              :turn-count="entry.turnCount"
              @retry="emit('retry', $event)"
              @rollback="emit('rollback', $event)"
              @inspect-diff="emit('inspect-diff', $event)"
            />
          </div>

          <Transition name="timeline-step">
            <div
              v-if="showThinking"
              key="timeline-thinking"
              class="timeline-thinking reasoning-live-row flex min-w-0 items-center py-1.5"
              :aria-label="thinkingLabel"
            >
              <!-- Same Cursor-style sweep as in-message planning shimmer -->
              <span class="reasoning-shimmer min-w-0 max-w-full">
                <span class="reasoning-shimmer__base truncate text-[13px]">{{ thinkingLabel }}</span>
                <span class="reasoning-shimmer__sheen truncate text-[13px]" aria-hidden="true">{{ thinkingLabel }}</span>
              </span>
            </div>
          </Transition>
        </template>
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
      v-if="!isLoading && groups.length > 0"
      class="pointer-events-none absolute right-1.5 top-1/2 z-40 flex -translate-y-1/2 flex-col items-center gap-2.5 sm:right-2"
      :aria-label="$t('chat.userNavigation')"
    >
      <SimpleTooltip :content="$t('chat.scrollToTop')">
        <button
          type="button"
          class="pointer-events-auto relative z-[1] grid size-8 place-items-center rounded-full border border-border bg-card/95 text-foreground/80 shadow-md backdrop-blur transition-colors hover:border-primary/40 hover:text-foreground"
          :aria-label="$t('chat.scrollToTop')"
          @click="scrollToTop"
        >
          <ChevronsUp :size="15" />
        </button>
      </SimpleTooltip>

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

      <SimpleTooltip :content="$t('chat.scrollToBottom')">
        <button
          type="button"
          class="pointer-events-auto relative z-[1] grid size-8 place-items-center rounded-full border border-border bg-card/95 text-foreground/80 shadow-md backdrop-blur transition-colors hover:border-primary/40 hover:text-foreground"
          :aria-label="$t('chat.scrollToBottom')"
          @click="jumpToLatest"
        >
          <ChevronsDown :size="15" />
        </button>
      </SimpleTooltip>
    </nav>
  </div>
</template>
