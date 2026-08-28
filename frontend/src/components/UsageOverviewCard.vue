<script setup lang="ts">
import { BarChart3 } from '@lucide/vue'
import { computed, nextTick, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { SimpleTooltip } from '@/components/ui/tooltip'
import { useAppStore, useClaudeStore, useCodexStore, useGrokStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'
import type { AccountUsageSummary } from '@/types/codex'
import { buildUsageRangeView, formatTokenCount, type UsageRangeDays } from '@/utils/accountUsage'
import { normalizeAccountUsage } from '@/utils/protocol'

const props = withDefaults(defineProps<{
  runtime: WorkspaceRuntime
  compact?: boolean
  detailed?: boolean
}>(), {
  compact: false,
  detailed: false,
})

const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const { t, locale } = useI18n()

type OverviewTab = 'overview' | 'models'
type RangeOption = { value: UsageRangeDays; label: string }
type HeatmapDay = { key: string; date: Date; tokens: number; intensity: number }
type TrendDay = HeatmapDay & {
  inputTokens: number
  cachedInputTokens: number
  outputTokens: number
  reasoningOutputTokens: number
}

const activeTab = shallowRef<OverviewTab>('overview')
const activeRange = shallowRef<UsageRangeDays>(props.detailed ? 30 : 'cumulative')
const usage = shallowRef<AccountUsageSummary | null>(null)
const loading = shallowRef(false)
const loadError = shallowRef('')
const heatmapScroller = useTemplateRef<HTMLDivElement>('heatmapScroller')
const heatmapDragging = shallowRef(false)
const activeTrendIndex = shallowRef(-1)
const heatmapTooltipDay = shallowRef<HeatmapDay | null>(null)
const heatmapTooltipPosition = shallowRef({ x: 0, y: 0 })
let loadSequence = 0
let dragPointerId: number | null = null
let dragStartX = 0
let dragStartScrollLeft = 0

const rangeOptions = computed<RangeOption[]>(() => props.detailed
  ? [
      { value: 7, label: t('usageOverview.rangeWeek') },
      { value: 30, label: t('usageOverview.rangeMonth') },
      { value: 90, label: t('usageOverview.rangeQuarter') },
      { value: 'cumulative', label: t('usageOverview.rangeAll') },
    ]
  : [
      { value: 'cumulative', label: t('usageOverview.rangeAll') },
      { value: 30, label: t('usageOverview.rangeMonth') },
      { value: 7, label: t('usageOverview.rangeWeek') },
    ])
const rangeView = computed(() => buildUsageRangeView(usage.value, activeRange.value))
const rangeTotal = computed(() => {
  if (activeRange.value === 'cumulative' && rangeView.value.totalTokens <= 0) {
    return Math.max(0, usage.value?.lifetimeTokens ?? 0)
  }
  return rangeView.value.totalTokens
})

function localDateKey(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function parseLocalDate(value: string): Date | null {
  const match = value.trim().match(/^(\d{4})-(\d{2})-(\d{2})/)
  if (!match) return null
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
  return Number.isNaN(date.getTime()) ? null : date
}

const heatmapSpanDays = computed(() => {
  return activeRange.value === 'cumulative' ? 365 : activeRange.value
})

const heatmapDays = computed<HeatmapDay[]>(() => {
  const buckets = new Map((usage.value?.dailyBuckets ?? []).map((item) => [item.startDate.slice(0, 10), Math.max(0, item.tokens)]))
  const end = new Date()
  end.setHours(0, 0, 0, 0)
  const span = heatmapSpanDays.value
  const start = new Date(end)
  start.setDate(start.getDate() - (span - 1))
  const values: Array<{ key: string; date: Date; tokens: number }> = []
  for (let cursor = new Date(start); cursor <= end; cursor.setDate(cursor.getDate() + 1)) {
    const date = new Date(cursor)
    const key = localDateKey(date)
    values.push({ key, date, tokens: buckets.get(key) ?? 0 })
  }
  const max = values.reduce((highest, item) => Math.max(highest, item.tokens), 0)
  return values.map((item) => ({
    ...item,
    intensity: item.tokens <= 0 || max <= 0 ? 0 : Math.max(1, Math.ceil((item.tokens / max) * 4)),
  }))
})

const trendSpanDays = computed(() => {
  if (activeRange.value !== 'cumulative') return activeRange.value
  const dates = (usage.value?.dailyBuckets ?? [])
    .map((item) => parseLocalDate(item.startDate)?.getTime() ?? Number.NaN)
    .filter(Number.isFinite)
  if (!dates.length) return 30
  const first = Math.min(...dates)
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return Math.min(365, Math.max(30, Math.round((today.getTime() - first) / 86_400_000) + 1))
})

const trendDays = computed<TrendDay[]>(() => {
  const buckets = new Map((usage.value?.dailyBuckets ?? []).map((item) => [item.startDate.slice(0, 10), item]))
  const end = new Date()
  end.setHours(0, 0, 0, 0)
  const start = new Date(end)
  start.setDate(start.getDate() - (trendSpanDays.value - 1))
  const rows: TrendDay[] = []
  for (let cursor = new Date(start); cursor <= end; cursor.setDate(cursor.getDate() + 1)) {
    const date = new Date(cursor)
    const key = localDateKey(date)
    const bucket = buckets.get(key)
    rows.push({
      key,
      date,
      tokens: Math.max(0, bucket?.tokens ?? 0),
      inputTokens: Math.max(0, bucket?.inputTokens ?? 0),
      cachedInputTokens: Math.max(0, bucket?.cachedInputTokens ?? 0),
      outputTokens: Math.max(0, bucket?.outputTokens ?? 0),
      reasoningOutputTokens: Math.max(0, bucket?.reasoningOutputTokens ?? 0),
      intensity: 0,
    })
  }
  return rows
})

const chartGeometry = {
  width: 720,
  height: 252,
  left: 58,
  right: 18,
  top: 14,
  bottom: 38,
} as const
const chartPlotWidth = chartGeometry.width - chartGeometry.left - chartGeometry.right
const chartPlotHeight = chartGeometry.height - chartGeometry.top - chartGeometry.bottom
const chartBaseline = chartGeometry.top + chartPlotHeight
const trendMax = computed(() => trendDays.value.reduce((highest, day) => Math.max(highest, day.tokens), 0))
const trendHasData = computed(() => trendMax.value > 0)
const trendPoints = computed(() => trendDays.value.map((day, index, rows) => {
  const x = chartGeometry.left + (rows.length <= 1 ? 0 : (index / (rows.length - 1)) * chartPlotWidth)
  const y = trendMax.value > 0
    ? chartGeometry.top + chartPlotHeight - (day.tokens / trendMax.value) * chartPlotHeight
    : chartBaseline
  return { ...day, x, y }
}))
const trendLinePath = computed(() => trendPoints.value
  .map((point, index) => `${index ? 'L' : 'M'} ${point.x.toFixed(2)} ${point.y.toFixed(2)}`)
  .join(' '))
const trendAreaPath = computed(() => {
  const points = trendPoints.value
  if (!points.length) return ''
  return `${trendLinePath.value} L ${points.at(-1)?.x.toFixed(2)} ${chartBaseline} L ${points[0].x.toFixed(2)} ${chartBaseline} Z`
})
const chartGradientId = computed(() => `usage-area-${props.runtime}-${props.detailed ? 'detail' : 'compact'}`)
const trendYAxisTicks = computed(() => Array.from({ length: 5 }, (_, index) => {
  const ratio = 1 - index / 4
  return {
    value: trendMax.value * ratio,
    y: chartGeometry.top + chartPlotHeight * (index / 4),
  }
}))
const trendXAxisTicks = computed(() => {
  const points = trendPoints.value
  if (!points.length) return []
  const indexes = [0, Math.round((points.length - 1) / 3), Math.round(((points.length - 1) * 2) / 3), points.length - 1]
  return [...new Set(indexes)].map((index) => points[index])
})
const trendPointMarkers = computed(() => trendPoints.value.length <= 31
  ? trendPoints.value.filter((point) => point.tokens > 0)
  : [])
const activeTrendPoint = computed(() => trendPoints.value[activeTrendIndex.value] ?? null)

function trendDateLabel(date: Date, includeYear = false): string {
  return new Intl.DateTimeFormat(locale.value, includeYear
    ? { month: 'short', day: 'numeric', year: '2-digit' }
    : { month: 'short', day: 'numeric' }).format(date)
}

function nonCachedInputTokens(day: TrendDay): number {
  // Native usage normalization already stores inputTokens as the uncached part.
  return Math.max(0, day.inputTokens)
}

function nonReasoningOutputTokens(day: TrendDay): number {
  return Math.max(0, day.outputTokens - day.reasoningOutputTokens)
}

function updateTrendHover(event: PointerEvent): void {
  const target = event.currentTarget as SVGElement
  const rect = target.getBoundingClientRect()
  const points = trendPoints.value
  if (!rect.width || !points.length) return
  const viewX = ((event.clientX - rect.left) / rect.width) * chartGeometry.width
  const plotRatio = Math.min(1, Math.max(0, (viewX - chartGeometry.left) / chartPlotWidth))
  activeTrendIndex.value = Math.round(plotRatio * (points.length - 1))
}

function moveTrendFocus(direction: number): void {
  const last = trendPoints.value.length - 1
  if (last < 0) return
  const current = activeTrendIndex.value < 0 ? last : activeTrendIndex.value
  activeTrendIndex.value = Math.min(last, Math.max(0, current + direction))
}

function onTrendKeydown(event: KeyboardEvent): void {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  event.preventDefault()
  moveTrendFocus(event.key === 'ArrowLeft' ? -1 : 1)
}

const tokenBreakdown = computed(() => {
  const buckets = rangeView.value.buckets
  const useLifetime = activeRange.value === 'cumulative'
  const sum = (field: 'inputTokens' | 'cachedInputTokens' | 'outputTokens' | 'reasoningOutputTokens'): number => (
    buckets.reduce((total, bucket) => total + Math.max(0, bucket[field] ?? 0), 0)
  )
  const rawInput = useLifetime && usage.value?.lifetimeInputTokens != null
    ? Math.max(0, usage.value.lifetimeInputTokens)
    : sum('inputTokens')
  const cachedInput = useLifetime && usage.value?.lifetimeCachedInputTokens != null
    ? Math.max(0, usage.value.lifetimeCachedInputTokens)
    : sum('cachedInputTokens')
  const rawOutput = useLifetime && usage.value?.lifetimeOutputTokens != null
    ? Math.max(0, usage.value.lifetimeOutputTokens)
    : sum('outputTokens')
  const reasoningOutput = useLifetime && usage.value?.lifetimeReasoningTokens != null
    ? Math.max(0, usage.value.lifetimeReasoningTokens)
    : sum('reasoningOutputTokens')
  const values = [
    {
      key: 'input',
      label: t('usageOverview.inputTokens'),
      value: rawInput,
      color: 'var(--usage-input)',
    },
    {
      key: 'cached',
      label: t('usageOverview.cachedTokens'),
      value: cachedInput,
      color: 'var(--usage-cached)',
    },
    {
      key: 'output',
      label: t('usageOverview.outputTokens'),
      value: Math.max(0, rawOutput - reasoningOutput),
      color: 'var(--usage-output)',
    },
    {
      key: 'reasoning',
      label: t('usageOverview.reasoningTokens'),
      value: reasoningOutput,
      color: 'var(--usage-reasoning)',
    },
  ]
  const classified = values.reduce((total, item) => total + item.value, 0)
  const unclassified = Math.max(0, rangeTotal.value - classified)
  if (unclassified > 0) {
    values.push({
      key: 'unclassified',
      label: t('usageOverview.unclassifiedTokens'),
      value: unclassified,
      color: 'var(--usage-unclassified)',
    })
  }
  const total = values.reduce((sumValue, item) => sumValue + item.value, 0)
  return values
    .filter((item) => item.value > 0)
    .map((item) => ({ ...item, percent: total ? (item.value / total) * 100 : 0 }))
})
const tokenDonutStyle = computed(() => {
  if (!tokenBreakdown.value.length) return { background: 'var(--muted)' }
  let cursor = 0
  const segments = tokenBreakdown.value.map((item) => {
    const start = cursor
    cursor += item.percent
    return `${item.color} ${start}% ${cursor}%`
  })
  return { background: `conic-gradient(${segments.join(', ')})` }
})

function heatmapClass(intensity: number): string {
  if (intensity >= 4) return 'bg-primary'
  if (intensity === 3) return 'bg-primary/70'
  if (intensity === 2) return 'bg-primary/45'
  if (intensity === 1) return 'bg-primary/25'
  return 'bg-muted'
}

function dateTooltip(day: HeatmapDay): string {
  const date = new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric', year: 'numeric' }).format(day.date)
  return `${date} · ${formatTokenCount(day.tokens)} ${t('usageOverview.tokens')}`
}

function showHeatmapTooltip(day: HeatmapDay, event: PointerEvent): void {
  if (loading.value || heatmapDragging.value) return
  heatmapTooltipDay.value = day
  heatmapTooltipPosition.value = {
    x: Math.min(window.innerWidth - 220, Math.max(8, event.clientX + 12)),
    y: Math.max(38, event.clientY - 12),
  }
}

function hideHeatmapTooltip(): void {
  heatmapTooltipDay.value = null
}

function scrollHeatmapToLatest(): void {
  void nextTick(() => {
    const element = heatmapScroller.value
    if (element) element.scrollLeft = element.scrollWidth
  })
}

function handleHeatmapWheel(event: WheelEvent): void {
  const element = heatmapScroller.value
  if (!element || element.scrollWidth <= element.clientWidth) return
  const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY
  if (!delta) return
  const canScroll = delta < 0
    ? element.scrollLeft > 0
    : element.scrollLeft + element.clientWidth < element.scrollWidth - 1
  if (!canScroll) return
  event.preventDefault()
  element.scrollLeft += delta
}

function handleHeatmapKeydown(event: KeyboardEvent): void {
  const element = heatmapScroller.value
  if (!element || element.scrollWidth <= element.clientWidth) return
  const step = Math.max(80, Math.round(element.clientWidth * 0.72))
  if (event.key === 'ArrowLeft') element.scrollLeft -= step
  else if (event.key === 'ArrowRight') element.scrollLeft += step
  else if (event.key === 'Home') element.scrollLeft = 0
  else if (event.key === 'End') element.scrollLeft = element.scrollWidth
  else return
  event.preventDefault()
}

function startHeatmapDrag(event: PointerEvent): void {
  const element = heatmapScroller.value
  if (!element || event.button !== 0 || element.scrollWidth <= element.clientWidth) return
  dragPointerId = event.pointerId
  dragStartX = event.clientX
  dragStartScrollLeft = element.scrollLeft
  heatmapDragging.value = true
  hideHeatmapTooltip()
  event.preventDefault()
  element.focus({ preventScroll: true })
  element.setPointerCapture(event.pointerId)
}

function moveHeatmapDrag(event: PointerEvent): void {
  const element = heatmapScroller.value
  if (!element || dragPointerId !== event.pointerId) return
  element.scrollLeft = dragStartScrollLeft - (event.clientX - dragStartX)
}

function stopHeatmapDrag(event: PointerEvent): void {
  const element = heatmapScroller.value
  if (dragPointerId !== event.pointerId) return
  if (element?.hasPointerCapture(event.pointerId)) element.releasePointerCapture(event.pointerId)
  dragPointerId = null
  heatmapDragging.value = false
}

const modelStats = computed(() => {
  const counts = new Map<string, number>()
  const add = (model: string | null | undefined): void => {
    const name = model?.trim() || t('usageOverview.unknownModel')
    counts.set(name, (counts.get(name) ?? 0) + 1)
  }
  if (props.runtime === 'grok') {
    grokStore.sessions.forEach((session) => add(session.model))
  } else if (props.runtime === 'claude') {
    claudeStore.sessions.forEach((session) => {
      const emptyDraft = session.id.startsWith('pending-claude-')
        && !(claudeStore.itemsBySession[session.id] ?? []).length
        && !(claudeStore.queueBySession[session.id] ?? []).length
        && !claudeStore.isSessionBusy(session.id)
      if (!emptyDraft) add(session.model)
    })
  } else {
    const seen = new Set<string>()
    for (const group of codexStore.threadGroups) {
      for (const thread of group.threads) {
        if (seen.has(thread.id) || codexStore.runtimeIDForThread(thread.id) !== props.runtime) continue
        const emptyDraft = thread.id.startsWith('pending-thread-')
          && !(codexStore.itemsByThread[thread.id] ?? []).length
          && !(codexStore.queuedMessagesByThread[thread.id] ?? []).length
          && !codexStore.threadHasActiveWork(thread.id)
        if (emptyDraft) continue
        seen.add(thread.id)
        add(thread.model)
      }
    }
  }
  const allRows = [...counts.entries()]
    .map(([model, sessions]) => ({ model, sessions }))
    .sort((left, right) => right.sessions - left.sessions || left.model.localeCompare(right.model))
  const total = allRows.reduce((sum, item) => sum + item.sessions, 0)
  const rows = allRows.length > 6
    ? [
        ...allRows.slice(0, 5),
        {
          model: t('usageOverview.otherModels'),
          sessions: allRows.slice(5).reduce((sum, item) => sum + item.sessions, 0),
        },
      ]
    : allRows
  return {
    total,
    rows: rows.map((item) => ({
      ...item,
      percent: total ? (item.sessions / total) * 100 : 0,
    })),
  }
})
const modelRows = computed(() => modelStats.value.rows)
const modelSessionCount = computed(() => modelStats.value.total)
const favoriteModel = computed(() => modelRows.value[0]?.model || t('usageOverview.noValue'))
const modelChartPalette = [
  'var(--primary)',
  'color-mix(in oklab, var(--primary) 76%, var(--muted-foreground))',
  'color-mix(in oklab, var(--primary) 56%, var(--muted))',
  'color-mix(in oklab, var(--primary) 38%, var(--muted))',
  'color-mix(in oklab, var(--primary) 24%, var(--muted))',
  'color-mix(in oklab, var(--primary) 14%, var(--muted))',
]
const modelDonutStyle = computed(() => {
  if (!modelRows.value.length) return { background: 'var(--muted)' }
  let cursor = 0
  const segments = modelRows.value.map((row, index) => {
    const start = cursor
    cursor += row.percent
    return `${modelChartPalette[index % modelChartPalette.length]} ${start}% ${cursor}%`
  })
  return { background: `conic-gradient(${segments.join(', ')})` }
})

async function loadUsage(): Promise<void> {
  const sequence = ++loadSequence
  loading.value = true
  loadError.value = ''
  try {
    const raw = await backend.ReadRuntimeAccountUsage(props.runtime)
    if (sequence !== loadSequence) return
    usage.value = normalizeAccountUsage(raw)
    // Keep the sidebar/footer snapshot in sync with a rollout backfill. The
    // runtime-scoped card used to show the repaired total while the footer kept
    // displaying its stale launch-time value until the user reopened usage.
    if (appStore.activeRuntime === props.runtime) {
      await appStore.loadLocalUsage()
      if (sequence !== loadSequence) return
      if (appStore.accountUsage) usage.value = appStore.accountUsage
    }
  } catch (error) {
    if (sequence !== loadSequence) return
    loadError.value = error instanceof Error ? error.message : String(error)
    usage.value = null
  } finally {
    if (sequence === loadSequence) loading.value = false
  }
}

watch(() => props.runtime, loadUsage, { immediate: true })
watch(
  () => appStore.accountUsage,
  (value) => {
    if (appStore.activeRuntime === props.runtime && value) usage.value = value
  },
)
watch(
  () => [activeRange.value, loading.value, heatmapDays.value.length] as const,
  scrollHeatmapToLatest,
  { immediate: true, flush: 'post' },
)
</script>

<template>
  <section v-if="detailed" class="usage-dashboard w-full text-left">
    <header class="usage-dashboard-header">
      <div class="min-w-0">
        <h2 class="text-[15px] font-semibold tracking-tight">{{ t('usageOverview.dashboardTitle') }}</h2>
        <p class="mt-1 max-w-2xl text-[11px] leading-5 text-muted-foreground">
          {{ t('usageOverview.dashboardDescription') }}
        </p>
      </div>
      <div class="usage-range-control" role="group" :aria-label="t('usageOverview.rangeLabel')">
        <button
          v-for="option in rangeOptions"
          :key="String(option.value)"
          type="button"
          class="usage-range-button"
          :class="{ 'is-active': activeRange === option.value }"
          :aria-pressed="activeRange === option.value"
          @click="activeRange = option.value"
        >
          {{ option.label }}
        </button>
      </div>
    </header>

    <div class="usage-stat-grid" aria-live="polite">
      <article class="usage-stat-card">
        <p>{{ t('usageOverview.totalTokens') }}</p>
        <span v-if="loading" class="usage-skeleton mt-2 h-6 w-20" aria-hidden="true" />
        <strong v-else>{{ formatTokenCount(rangeTotal) }}</strong>
        <small>{{ t('usageOverview.selectedRange') }}</small>
      </article>
      <article class="usage-stat-card">
        <p>{{ t('usageOverview.sessions') }}</p>
        <span v-if="loading" class="usage-skeleton mt-2 h-6 w-14" aria-hidden="true" />
        <strong v-else>{{ modelSessionCount.toLocaleString() }}</strong>
        <small>{{ t('usageOverview.loadedSessions') }}</small>
      </article>
      <article class="usage-stat-card">
        <p>{{ t('usageOverview.activeDays') }}</p>
        <span v-if="loading" class="usage-skeleton mt-2 h-6 w-12" aria-hidden="true" />
        <strong v-else>{{ rangeView.dayCount.toLocaleString() }}</strong>
        <small>{{ t('usageOverview.daysWithActivity') }}</small>
      </article>
      <article class="usage-stat-card">
        <p>{{ t('usageOverview.dailyAverage') }}</p>
        <span v-if="loading" class="usage-skeleton mt-2 h-6 w-16" aria-hidden="true" />
        <strong v-else>{{ formatTokenCount(rangeView.averageTokens) }}</strong>
        <small>{{ t('usageOverview.perActiveDay') }}</small>
      </article>
    </div>

    <section class="usage-panel usage-trend-panel">
      <div class="usage-panel-heading">
        <div>
          <h3>{{ t('usageOverview.trendTitle') }}</h3>
          <p>{{ t('usageOverview.trendDescription') }}</p>
        </div>
        <div class="usage-chart-legend" aria-hidden="true">
          <span><i class="is-total" />{{ t('usageOverview.totalTokens') }}</span>
        </div>
      </div>

      <div v-if="loading" class="usage-chart-loading" aria-hidden="true">
        <span v-for="index in 10" :key="index" class="usage-chart-loading-bar" :style="{ height: `${22 + ((index * 29) % 68)}%`, animationDelay: `${index * 45}ms` }" />
      </div>
      <div v-else-if="trendHasData" class="usage-trend-chart">
        <svg
          class="usage-trend-svg"
          :viewBox="`0 0 ${chartGeometry.width} ${chartGeometry.height}`"
          role="img"
          tabindex="0"
          :aria-label="t('usageOverview.trendAriaLabel')"
          @pointermove="updateTrendHover"
          @pointerleave="activeTrendIndex = -1"
          @focus="activeTrendIndex = trendPoints.length - 1"
          @blur="activeTrendIndex = -1"
          @keydown="onTrendKeydown"
        >
          <defs>
            <linearGradient :id="chartGradientId" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stop-color="var(--primary)" stop-opacity="0.22" />
              <stop offset="100%" stop-color="var(--primary)" stop-opacity="0.015" />
            </linearGradient>
          </defs>
          <g class="usage-chart-grid">
            <g v-for="tick in trendYAxisTicks" :key="tick.y">
              <line :x1="chartGeometry.left" :x2="chartGeometry.width - chartGeometry.right" :y1="tick.y" :y2="tick.y" />
              <text :x="chartGeometry.left - 10" :y="tick.y + 3" text-anchor="end">{{ formatTokenCount(tick.value) }}</text>
            </g>
          </g>
          <path :d="trendAreaPath" :fill="`url(#${chartGradientId})`" />
          <path class="usage-trend-line" :d="trendLinePath" />
          <circle
            v-for="point in trendPointMarkers"
            :key="point.key"
            class="usage-trend-point"
            :cx="point.x"
            :cy="point.y"
            r="3"
          />
          <g v-if="activeTrendPoint" class="usage-trend-active" aria-hidden="true">
            <line
              :x1="activeTrendPoint.x"
              :x2="activeTrendPoint.x"
              :y1="chartGeometry.top"
              :y2="chartBaseline"
            />
            <circle :cx="activeTrendPoint.x" :cy="activeTrendPoint.y" r="4.5" />
          </g>
          <g class="usage-chart-x-labels">
            <text
              v-for="(point, index) in trendXAxisTicks"
              :key="point.key"
              :x="point.x"
              :y="chartGeometry.height - 12"
              :text-anchor="index === 0 ? 'start' : index === trendXAxisTicks.length - 1 ? 'end' : 'middle'"
            >{{ trendDateLabel(point.date) }}</text>
          </g>
        </svg>
        <div
          v-if="activeTrendPoint"
          class="usage-chart-tooltip"
          :class="activeTrendPoint.x > chartGeometry.width * 0.68 ? 'is-left' : 'is-right'"
          :style="{
            left: `${(activeTrendPoint.x / chartGeometry.width) * 100}%`,
            top: `${Math.min(50, Math.max(30, (activeTrendPoint.y / chartGeometry.height) * 100))}%`,
          }"
          role="status"
        >
          <strong>{{ trendDateLabel(activeTrendPoint.date, true) }}</strong>
          <span class="is-total"><i />{{ t('usageOverview.totalTokens') }} <b>{{ formatTokenCount(activeTrendPoint.tokens) }}</b></span>
          <span class="is-input"><i />{{ t('usageOverview.inputTokens') }} <b>{{ formatTokenCount(nonCachedInputTokens(activeTrendPoint)) }}</b></span>
          <span class="is-cached"><i />{{ t('usageOverview.cachedTokens') }} <b>{{ formatTokenCount(activeTrendPoint.cachedInputTokens) }}</b></span>
          <span class="is-output"><i />{{ t('usageOverview.outputTokens') }} <b>{{ formatTokenCount(nonReasoningOutputTokens(activeTrendPoint)) }}</b></span>
          <span class="is-reasoning"><i />{{ t('usageOverview.reasoningTokens') }} <b>{{ formatTokenCount(activeTrendPoint.reasoningOutputTokens) }}</b></span>
        </div>
      </div>
      <div v-else class="usage-chart-empty">
        <BarChart3 :size="22" />
        <p>{{ t('usageOverview.noUsage') }}</p>
        <span>{{ t('usageOverview.noUsageHint') }}</span>
      </div>
    </section>

    <div class="usage-insight-strip">
      <span>{{ t('usageOverview.currentStreak') }} <strong>{{ usage?.currentStreakDays ?? 0 }}{{ t('usageOverview.dayUnit') }}</strong></span>
      <span>{{ t('usageOverview.longestStreak') }} <strong>{{ usage?.longestStreakDays ?? 0 }}{{ t('usageOverview.dayUnit') }}</strong></span>
      <span>{{ t('usageOverview.peakDay') }} <strong>{{ formatTokenCount(usage?.peakDailyTokens) }}</strong></span>
      <span class="min-w-0">{{ t('usageOverview.favoriteModel') }} <SimpleTooltip :content="favoriteModel"><strong class="inline-block max-w-44 truncate align-bottom">{{ favoriteModel }}</strong></SimpleTooltip></span>
    </div>

    <div class="usage-secondary-grid">
      <section class="usage-panel usage-breakdown-panel">
        <div class="usage-panel-heading">
          <div>
            <h3>{{ t('usageOverview.breakdownTitle') }}</h3>
            <p>{{ t('usageOverview.breakdownDescription') }}</p>
          </div>
        </div>
        <div v-if="tokenBreakdown.length" class="usage-donut-layout">
          <div class="usage-token-donut" :style="tokenDonutStyle" aria-hidden="true">
            <div><strong>{{ formatTokenCount(rangeTotal) }}</strong><span>Token</span></div>
          </div>
          <div class="usage-breakdown-list">
            <div v-for="item in tokenBreakdown" :key="item.key" class="usage-breakdown-row">
              <span class="min-w-0 truncate"><i :style="{ background: item.color }" />{{ item.label }}</span>
              <strong>{{ formatTokenCount(item.value) }}</strong>
              <em>{{ item.percent.toFixed(item.percent >= 10 ? 0 : 1) }}%</em>
            </div>
          </div>
        </div>
        <div v-else class="usage-small-empty">{{ t('usageOverview.noBreakdown') }}</div>
      </section>

      <section class="usage-panel usage-model-panel">
        <div class="usage-panel-heading">
          <div>
            <h3>{{ t('usageOverview.modelDistribution') }}</h3>
            <p>{{ t('usageOverview.modelDistributionDescription') }}</p>
          </div>
        </div>
        <div v-if="modelRows.length" class="usage-model-list">
          <div v-for="(row, index) in modelRows" :key="row.model" class="usage-model-row">
            <div>
              <SimpleTooltip :content="row.model"><span><i :style="{ background: modelChartPalette[index % modelChartPalette.length] }" />{{ row.model }}</span></SimpleTooltip>
              <strong>{{ t('usageOverview.sessionCount', { count: row.sessions }) }}</strong>
            </div>
            <div class="usage-model-track"><span :style="{ width: `${Math.max(2, row.percent)}%`, background: modelChartPalette[index % modelChartPalette.length] }" /></div>
          </div>
        </div>
        <div v-else class="usage-small-empty">{{ t('usageOverview.noModelsHint') }}</div>
      </section>
    </div>

    <section class="usage-panel usage-activity-panel">
      <div class="usage-panel-heading">
        <div>
          <h3>{{ t('usageOverview.activityTitle') }}</h3>
          <p>{{ t('usageOverview.activityDescription') }}</p>
        </div>
      </div>
      <div
        ref="heatmapScroller"
        class="usage-heatmap usage-dashboard-heatmap overflow-x-auto outline-none ring-primary/25 transition-shadow focus-visible:ring-2"
        :class="{ 'can-scroll': activeRange === 'cumulative', 'is-dragging': heatmapDragging }"
        tabindex="0"
        role="region"
        :aria-label="t('usageOverview.heatmapLabel')"
        @scroll.passive="hideHeatmapTooltip"
        @wheel="handleHeatmapWheel"
        @keydown="handleHeatmapKeydown"
        @pointerdown="startHeatmapDrag"
        @pointermove="moveHeatmapDrag"
        @pointerup="stopHeatmapDrag"
        @pointercancel="stopHeatmapDrag"
        @lostpointercapture="stopHeatmapDrag"
      >
        <div
          class="heatmap-grid"
          :class="[activeRange === 7 || activeRange === 30 ? 'is-strip' : 'is-calendar', { 'is-scrollable': activeRange === 'cumulative' }]"
          aria-hidden="true"
        >
          <span
            v-for="(day, index) in heatmapDays"
            :key="day.key"
            class="usage-heatmap-cell h-full w-full rounded-[3px] ring-1 ring-inset ring-foreground/[0.025]"
            :class="loading ? 'usage-loading-cell bg-muted-foreground/15' : heatmapClass(day.intensity)"
            :style="index === 0 && activeRange !== 7 && activeRange !== 30 ? { gridRowStart: day.date.getDay() + 1 } : undefined"
            @pointerenter="showHeatmapTooltip(day, $event)"
            @pointermove="showHeatmapTooltip(day, $event)"
            @pointerleave="hideHeatmapTooltip"
          />
        </div>
      </div>
      <p v-if="activeRange === 'cumulative'" class="mt-2 text-[10px] text-muted-foreground">{{ t('usageOverview.heatmapScrollHint') }}</p>
    </section>

    <div v-if="loadError" class="usage-error" role="alert">
      <span>{{ t('usageOverview.loadFailed') }}</span>
      <button type="button" @click="loadUsage">{{ t('common.retry') }}</button>
    </div>
  </section>

  <section
    v-else
    class="usage-overview overflow-hidden rounded-xl border border-border/60 bg-card/80 text-left shadow-[0_10px_30px_-24px_color-mix(in_oklab,var(--foreground)_42%,transparent)] backdrop-blur-sm"
    :class="compact ? 'w-full' : 'w-full max-w-xl'"
  >
    <header class="flex items-center justify-between gap-2 px-3 py-2.5">
      <div class="flex shrink-0 rounded-lg bg-muted/55 p-0.5" role="tablist" :aria-label="t('usageOverview.title')">
        <button
          v-for="tab in (['overview', 'models'] as OverviewTab[])"
          :key="tab"
          type="button"
          role="tab"
          class="rounded-md px-2.5 py-1 text-[10px] transition-[background-color,color,box-shadow]"
          :class="activeTab === tab ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'"
          :aria-selected="activeTab === tab"
          @click="activeTab = tab"
        >
          {{ t(`usageOverview.${tab}`) }}
        </button>
      </div>
      <div v-if="activeTab === 'overview'" class="flex min-w-0 rounded-lg bg-muted/45 p-0.5">
        <button
          v-for="option in rangeOptions"
          :key="String(option.value)"
          type="button"
          class="whitespace-nowrap rounded-md px-2 py-1 text-[10px] transition-colors"
          :class="activeRange === option.value ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'"
          @click="activeRange = option.value"
        >
          {{ option.label }}
        </button>
      </div>
    </header>

    <div v-if="activeTab === 'overview'" class="space-y-2.5 px-3 pb-3">
      <div class="usage-metrics rounded-lg bg-muted/28 px-1.5 py-2">
        <div class="usage-metric">
          <p>{{ t('usageOverview.sessions') }}</p>
          <strong>{{ modelSessionCount }}</strong>
        </div>
        <div class="usage-metric">
          <p>{{ t('usageOverview.totalTokens') }}</p>
          <strong>{{ formatTokenCount(rangeTotal) }}</strong>
        </div>
        <div class="usage-metric">
          <p>{{ t('usageOverview.activeDays') }}</p>
          <strong>{{ rangeView.dayCount }}</strong>
        </div>
        <div class="usage-metric">
          <p>{{ t('usageOverview.dailyAverage') }}</p>
          <strong>{{ formatTokenCount(rangeView.averageTokens) }}</strong>
        </div>
      </div>

      <div
        ref="heatmapScroller"
        class="usage-heatmap overflow-x-auto rounded-lg bg-muted/20 px-2 py-2.5 outline-none ring-primary/25 transition-shadow focus-visible:ring-2"
        :class="{ 'can-scroll': activeRange === 'cumulative', 'is-dragging': heatmapDragging }"
        tabindex="0"
        role="region"
        :aria-label="t('usageOverview.heatmapLabel')"
        @scroll.passive="hideHeatmapTooltip"
        @wheel="handleHeatmapWheel"
        @keydown="handleHeatmapKeydown"
        @pointerdown="startHeatmapDrag"
        @pointermove="moveHeatmapDrag"
        @pointerup="stopHeatmapDrag"
        @pointercancel="stopHeatmapDrag"
        @lostpointercapture="stopHeatmapDrag"
      >
        <div
          v-if="loading"
          class="heatmap-grid"
          :class="[activeRange === 7 ? 'is-strip' : 'is-calendar', { 'is-scrollable': activeRange === 'cumulative' }]"
          aria-hidden="true"
        >
          <span
            v-for="index in heatmapSpanDays"
            :key="index"
            class="usage-loading-cell h-full w-full rounded-[2px] bg-muted-foreground/15"
            :style="{ animationDelay: `${(index % 18) * 30}ms` }"
          />
        </div>
        <div
          v-else
          class="heatmap-grid"
          :class="[activeRange === 7 ? 'is-strip' : 'is-calendar', { 'is-scrollable': activeRange === 'cumulative' }]"
          aria-hidden="true"
        >
          <span
            v-for="(day, index) in heatmapDays"
            :key="day.key"
            class="usage-heatmap-cell h-full w-full rounded-[2px] ring-1 ring-inset ring-foreground/[0.035]"
            :class="heatmapClass(day.intensity)"
            :style="index === 0 && activeRange !== 7 ? { gridRowStart: day.date.getDay() + 1 } : undefined"
            @pointerenter="showHeatmapTooltip(day, $event)"
            @pointermove="showHeatmapTooltip(day, $event)"
            @pointerleave="hideHeatmapTooltip"
          />
        </div>
      </div>

      <div class="flex min-w-0 items-center justify-between gap-3 text-[9px] text-muted-foreground/75">
        <span class="truncate">{{ activeRange === 'cumulative' ? t('usageOverview.heatmapScrollHint') : '' }}</span>
        <span class="shrink-0">{{ t('usageOverview.subtitle') }}</span>
      </div>

      <div class="usage-details">
        <span>{{ t('usageOverview.currentStreak') }} <strong>{{ usage?.currentStreakDays ?? 0 }}</strong></span>
        <span>{{ t('usageOverview.longestStreak') }} <strong>{{ usage?.longestStreakDays ?? 0 }}</strong></span>
        <span>{{ t('usageOverview.peakDay') }} <strong>{{ formatTokenCount(usage?.peakDailyTokens) }}</strong></span>
        <span class="min-w-0">{{ t('usageOverview.favoriteModel') }} <SimpleTooltip :content="favoriteModel"><strong class="inline-block max-w-28 truncate align-bottom">{{ favoriteModel }}</strong></SimpleTooltip></span>
      </div>
      <SimpleTooltip v-if="loadError" :content="loadError">
        <p class="truncate text-[9px] text-muted-foreground">{{ t('usageOverview.loadFailed') }}</p>
      </SimpleTooltip>
    </div>

    <div v-else class="px-3 pb-3">
      <div v-if="modelRows.length" class="grid gap-4 rounded-lg bg-muted/20 p-3 sm:grid-cols-[132px_minmax(0,1fr)] sm:items-center">
        <div class="relative mx-auto grid size-28 place-items-center rounded-full" :style="modelDonutStyle">
          <div class="grid size-[74px] place-items-center rounded-full bg-card text-center shadow-inner">
            <div>
              <p class="text-xl font-semibold tabular-nums">{{ modelSessionCount }}</p>
              <p class="text-[9px] text-muted-foreground">{{ t('usageOverview.sessions') }}</p>
            </div>
          </div>
        </div>
        <div class="space-y-2.5">
          <div v-for="(row, index) in modelRows" :key="row.model" class="space-y-1">
            <div class="flex items-center justify-between gap-3 text-[11px]">
              <SimpleTooltip :content="row.model">
                <span class="flex min-w-0 items-center gap-2 truncate font-medium">
                <i class="size-2 shrink-0 rounded-sm" :style="{ background: modelChartPalette[index % modelChartPalette.length] }" />
                <span class="truncate">{{ row.model }}</span>
                </span>
              </SimpleTooltip>
              <span class="shrink-0 text-muted-foreground">{{ t('usageOverview.sessionCount', { count: row.sessions }) }}</span>
            </div>
            <div class="h-1.5 overflow-hidden rounded-full bg-muted">
              <div class="h-full rounded-full transition-[width] duration-500" :style="{ width: `${Math.max(2, row.percent)}%`, background: modelChartPalette[index % modelChartPalette.length] }" />
            </div>
          </div>
          <p class="pt-1 text-[10px] text-muted-foreground">
            {{ t('usageOverview.modelsHint', { count: modelSessionCount }) }}
          </p>
        </div>
      </div>
      <div v-else class="grid min-h-28 place-items-center rounded-lg bg-muted/20 text-center">
        <div>
          <BarChart3 :size="20" class="mx-auto mb-2 text-muted-foreground/60" />
          <p class="text-xs font-medium">{{ t('usageOverview.noModels') }}</p>
          <p class="mt-1 text-[10px] text-muted-foreground">{{ t('usageOverview.noModelsHint') }}</p>
        </div>
      </div>
    </div>
  </section>

  <Teleport to="body">
    <Transition name="usage-tooltip">
      <div
        v-if="heatmapTooltipDay"
        class="usage-heatmap-tooltip"
        :style="{ left: `${heatmapTooltipPosition.x}px`, top: `${heatmapTooltipPosition.y}px` }"
        role="status"
      >
        {{ dateTooltip(heatmapTooltipDay) }}
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.usage-dashboard {
  --usage-input: #d97757;
  --usage-cached: #2f9e76;
  --usage-output: #4f83bf;
  --usage-reasoning: #9a72b0;
  --usage-unclassified: color-mix(in oklab, var(--muted-foreground) 72%, transparent);
  container-type: inline-size;
  display: grid;
  gap: 1rem;
  animation: usage-card-in 420ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.usage-dashboard-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1.25rem;
}

.usage-range-control {
  display: flex;
  flex: 0 0 auto;
  padding: 0.2rem;
  border-radius: 0.65rem;
  background: var(--muted);
}

.usage-range-button {
  min-width: 2.75rem;
  height: 1.75rem;
  padding: 0 0.65rem;
  border-radius: 0.48rem;
  color: var(--muted-foreground);
  font-size: 0.6875rem;
  line-height: 1;
  transition: background-color 140ms ease, color 140ms ease, box-shadow 140ms ease;
}

.usage-range-button:hover {
  color: var(--foreground);
}

.usage-range-button:focus-visible {
  outline: 2px solid color-mix(in oklab, var(--ring) 48%, transparent);
  outline-offset: 1px;
}

.usage-range-button.is-active {
  background: var(--background);
  box-shadow: 0 1px 2px color-mix(in oklab, var(--foreground) 13%, transparent);
  color: var(--foreground);
  font-weight: 600;
}

.usage-stat-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0.75rem;
}

.usage-stat-card,
.usage-panel {
  border: 1px solid color-mix(in oklab, var(--border) 78%, transparent);
  background: color-mix(in oklab, var(--card) 96%, var(--muted));
}

.usage-stat-card {
  min-width: 0;
  min-height: 7rem;
  padding: 0.9rem 1rem;
  border-radius: 0.8rem;
}

.usage-stat-card p {
  overflow: hidden;
  color: var(--muted-foreground);
  font-size: 0.6875rem;
  line-height: 1rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-stat-card strong {
  display: block;
  overflow: hidden;
  margin-top: 0.35rem;
  color: var(--foreground);
  font-size: clamp(1.25rem, 2.8cqi, 1.65rem);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  letter-spacing: -0.035em;
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-stat-card small {
  display: block;
  overflow: hidden;
  margin-top: 0.3rem;
  color: color-mix(in oklab, var(--muted-foreground) 82%, transparent);
  font-size: 0.625rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-panel {
  min-width: 0;
  overflow: hidden;
  border-radius: 0.9rem;
}

.usage-panel-heading {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.1rem 0.75rem;
}

.usage-panel-heading h3 {
  color: var(--foreground);
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1.25rem;
}

.usage-panel-heading p {
  margin-top: 0.15rem;
  color: var(--muted-foreground);
  font-size: 0.6875rem;
  line-height: 1rem;
}

.usage-chart-legend {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 0.75rem;
  padding-top: 0.15rem;
  color: var(--muted-foreground);
  font-size: 0.625rem;
}

.usage-chart-legend span,
.usage-breakdown-row > span,
.usage-model-row span {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.45rem;
}

.usage-chart-legend i,
.usage-breakdown-row i,
.usage-model-row i {
  display: inline-block;
  width: 0.5rem;
  height: 0.5rem;
  flex: 0 0 auto;
  border-radius: 0.16rem;
}

.usage-chart-legend i.is-total {
  background: var(--primary);
}

.usage-trend-chart {
  position: relative;
}

.usage-trend-svg {
  display: block;
  width: 100%;
  min-height: 15.75rem;
  padding: 0 0.5rem 0.3rem;
  outline: none;
}

.usage-trend-svg:focus-visible {
  filter: drop-shadow(0 0 2px color-mix(in oklab, var(--ring) 55%, transparent));
}

.usage-chart-grid line {
  stroke: color-mix(in oklab, var(--border) 72%, transparent);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}

.usage-chart-grid text,
.usage-chart-x-labels text {
  fill: var(--muted-foreground);
  font-family: var(--font-family-sans);
  font-size: 9px;
  font-variant-numeric: tabular-nums;
}

.usage-trend-line {
  fill: none;
  stroke: var(--primary);
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
}

.usage-trend-point {
  fill: var(--card);
  stroke: var(--primary);
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
}

.usage-trend-active line {
  stroke: color-mix(in oklab, var(--foreground) 32%, transparent);
  stroke-dasharray: 3 3;
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}

.usage-trend-active circle {
  fill: var(--card);
  stroke: var(--primary);
  stroke-width: 2.5;
  vector-effect: non-scaling-stroke;
}

.usage-chart-tooltip {
  position: absolute;
  z-index: 4;
  display: grid;
  width: 10.75rem;
  gap: 0.3rem;
  padding: 0.65rem 0.7rem;
  border: 1px solid color-mix(in oklab, var(--border) 86%, transparent);
  border-radius: 0.65rem;
  background: color-mix(in oklab, var(--popover) 96%, transparent);
  box-shadow: 0 10px 30px -12px color-mix(in oklab, var(--foreground) 34%, transparent);
  color: var(--popover-foreground);
  font-size: 0.625rem;
  pointer-events: none;
  transform: translate(0.65rem, -50%);
  backdrop-filter: blur(12px);
}

.usage-chart-tooltip.is-left {
  transform: translate(calc(-100% - 0.65rem), -50%);
}

.usage-chart-tooltip strong {
  margin-bottom: 0.12rem;
  font-size: 0.6875rem;
  font-weight: 600;
}

.usage-chart-tooltip span {
  display: grid;
  grid-template-columns: 0.4rem minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.35rem;
  color: var(--muted-foreground);
}

.usage-chart-tooltip i {
  width: 0.38rem;
  height: 0.38rem;
  border-radius: 999px;
  background: var(--muted-foreground);
}

.usage-chart-tooltip b {
  color: var(--foreground);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.usage-chart-tooltip .is-total i { background: var(--primary); }
.usage-chart-tooltip .is-input i { background: var(--usage-input); }
.usage-chart-tooltip .is-cached i { background: var(--usage-cached); }
.usage-chart-tooltip .is-output i { background: var(--usage-output); }
.usage-chart-tooltip .is-reasoning i { background: var(--usage-reasoning); }

.usage-heatmap-tooltip {
  position: fixed;
  z-index: 100;
  max-width: min(17rem, calc(100vw - 1rem));
  padding: 0.38rem 0.55rem;
  border: 1px solid color-mix(in oklab, var(--border) 88%, transparent);
  border-radius: 0.45rem;
  background: color-mix(in oklab, var(--popover) 96%, transparent);
  box-shadow: 0 8px 22px -12px color-mix(in oklab, var(--foreground) 38%, transparent);
  color: var(--popover-foreground);
  font-size: 0.625rem;
  line-height: 1rem;
  pointer-events: none;
  transform: translateY(-100%);
  backdrop-filter: blur(10px);
}

.usage-tooltip-enter-active,
.usage-tooltip-leave-active {
  transition: opacity 100ms ease, transform 100ms ease;
}

.usage-tooltip-enter-from,
.usage-tooltip-leave-to {
  opacity: 0;
  transform: translateY(calc(-100% + 3px));
}

.usage-chart-loading {
  display: flex;
  height: 15.75rem;
  align-items: flex-end;
  gap: 0.65rem;
  margin: 0 1.1rem 1rem;
  padding: 1.2rem 1rem 0.75rem;
  border-bottom: 1px solid var(--border);
}

.usage-chart-loading-bar {
  flex: 1;
  border-radius: 0.25rem 0.25rem 0 0;
  background: color-mix(in oklab, var(--primary) 26%, var(--muted));
  animation: usage-cell-pulse 1.4s ease-in-out infinite;
}

.usage-chart-empty {
  display: grid;
  min-height: 15.75rem;
  place-content: center;
  justify-items: center;
  padding: 2rem;
  color: var(--muted-foreground);
  text-align: center;
}

.usage-chart-empty p {
  margin-top: 0.6rem;
  color: var(--foreground);
  font-size: 0.75rem;
  font-weight: 600;
}

.usage-chart-empty span {
  max-width: 22rem;
  margin-top: 0.2rem;
  font-size: 0.65rem;
  line-height: 1rem;
}

.usage-insight-strip {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0;
  overflow: hidden;
  border: 1px solid color-mix(in oklab, var(--border) 74%, transparent);
  border-radius: 0.75rem;
  background: color-mix(in oklab, var(--muted) 42%, transparent);
}

.usage-insight-strip > span {
  min-width: 0;
  padding: 0.65rem 0.8rem;
  border-left: 1px solid color-mix(in oklab, var(--border) 66%, transparent);
  color: var(--muted-foreground);
  font-size: 0.625rem;
  text-align: center;
}

.usage-insight-strip > span:first-child {
  border-left: 0;
}

.usage-insight-strip strong {
  margin-left: 0.25rem;
  color: var(--foreground);
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.usage-secondary-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 1rem;
}

.usage-donut-layout {
  display: grid;
  grid-template-columns: 7.5rem minmax(0, 1fr);
  align-items: center;
  gap: 1rem;
  min-height: 12.5rem;
  padding: 0.35rem 1.1rem 1rem;
}

.usage-token-donut {
  display: grid;
  width: 7rem;
  height: 7rem;
  place-items: center;
  border-radius: 999px;
}

.usage-token-donut > div {
  display: grid;
  width: 4.7rem;
  height: 4.7rem;
  place-content: center;
  border-radius: 999px;
  background: var(--card);
  box-shadow: inset 0 0 0 1px color-mix(in oklab, var(--border) 60%, transparent);
  text-align: center;
}

.usage-token-donut strong {
  font-size: 1rem;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.usage-token-donut span {
  color: var(--muted-foreground);
  font-size: 0.5625rem;
  text-transform: uppercase;
}

.usage-breakdown-list {
  display: grid;
  gap: 0.55rem;
  min-width: 0;
}

.usage-breakdown-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 2.4rem;
  align-items: center;
  gap: 0.55rem;
  color: var(--muted-foreground);
  font-size: 0.625rem;
}

.usage-breakdown-row strong {
  color: var(--foreground);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.usage-breakdown-row em {
  font-style: normal;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.usage-model-list {
  display: grid;
  align-content: center;
  gap: 0.8rem;
  min-height: 12.5rem;
  padding: 0.35rem 1.1rem 1rem;
}

.usage-model-row {
  min-width: 0;
}

.usage-model-row > div:first-child {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  font-size: 0.6875rem;
}

.usage-model-row span {
  overflow: hidden;
  color: var(--foreground);
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-model-row strong {
  flex: 0 0 auto;
  color: var(--muted-foreground);
  font-size: 0.625rem;
  font-weight: 400;
}

.usage-model-track {
  height: 0.3rem;
  margin-top: 0.35rem;
  overflow: hidden;
  border-radius: 999px;
  background: var(--muted);
}

.usage-model-track span {
  display: block;
  height: 100%;
  border-radius: inherit;
  transition: width 420ms cubic-bezier(0.22, 1, 0.36, 1);
}

.usage-small-empty {
  display: grid;
  min-height: 12.5rem;
  place-items: center;
  padding: 2rem;
  color: var(--muted-foreground);
  font-size: 0.6875rem;
  text-align: center;
}

.usage-activity-panel {
  padding-bottom: 0.85rem;
}

.usage-dashboard-heatmap {
  min-height: 6.5rem;
  margin: 0 1.1rem;
  padding: 0.75rem;
  border-radius: 0.65rem;
  background: color-mix(in oklab, var(--muted) 42%, transparent);
}

.usage-activity-panel > p {
  padding: 0 1.1rem;
}

.usage-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.65rem 0.8rem;
  border: 1px solid color-mix(in oklab, var(--destructive) 24%, var(--border));
  border-radius: 0.65rem;
  background: color-mix(in oklab, var(--destructive) 7%, var(--card));
  color: var(--foreground);
  font-size: 0.6875rem;
}

.usage-error button {
  flex: 0 0 auto;
  color: var(--destructive);
  font-weight: 600;
}

.usage-error button:focus-visible {
  outline: 2px solid color-mix(in oklab, var(--destructive) 38%, transparent);
  outline-offset: 2px;
}

.usage-skeleton {
  display: block;
  border-radius: 0.35rem;
  background: color-mix(in oklab, var(--muted-foreground) 18%, var(--muted));
  animation: usage-cell-pulse 1.4s ease-in-out infinite;
}

.heatmap-grid {
  display: grid;
  flex: 0 0 auto;
  grid-auto-flow: column;
  width: max-content;
  min-width: 100%;
  gap: 3px;
}

.heatmap-grid.is-strip {
  grid-auto-columns: 10px;
  grid-template-rows: 10px;
  justify-content: center;
}

.heatmap-grid.is-calendar {
  grid-auto-columns: 10px;
  grid-template-rows: repeat(7, 10px);
  justify-content: center;
}

.heatmap-grid.is-calendar.is-scrollable {
  justify-content: end;
}

.usage-metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(104px, 1fr));
}

.usage-metric {
  min-width: 0;
  padding: 0 0.625rem;
  border-left: 1px solid color-mix(in oklab, var(--border) 55%, transparent);
}

.usage-metric:first-child {
  border-left-color: transparent;
}

.usage-metric p {
  overflow: hidden;
  color: var(--muted-foreground);
  font-size: 9px;
  line-height: 1rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-metric strong {
  display: block;
  overflow: hidden;
  color: var(--foreground);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  line-height: 1.125rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-details {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem 0.875rem;
  padding-top: 0.125rem;
  color: var(--muted-foreground);
  font-size: 9px;
}

.usage-details strong {
  color: var(--foreground);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.usage-heatmap {
  display: flex;
  min-height: 104px;
  align-items: center;
  cursor: default;
  overscroll-behavior-inline: contain;
  scrollbar-color: color-mix(in oklab, var(--muted-foreground) 28%, transparent) transparent;
  scrollbar-width: thin;
}

.usage-heatmap.can-scroll {
  cursor: grab;
}

.usage-heatmap.is-dragging {
  cursor: grabbing;
  user-select: none;
}

.usage-heatmap::-webkit-scrollbar {
  height: 6px;
}

.usage-heatmap::-webkit-scrollbar-track {
  background: transparent;
}

.usage-heatmap::-webkit-scrollbar-thumb {
  border: 2px solid transparent;
  border-radius: 999px;
  background: color-mix(in oklab, var(--muted-foreground) 28%, transparent);
  background-clip: padding-box;
}

.usage-heatmap-cell {
  transition: filter 120ms ease, box-shadow 120ms ease;
}

.usage-heatmap-cell:hover {
  filter: saturate(1.15) brightness(0.94);
  box-shadow: 0 0 0 1px color-mix(in oklab, var(--foreground) 20%, transparent);
}

.usage-overview {
  container-type: inline-size;
  animation: usage-card-in 480ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.usage-loading-cell {
  animation: usage-cell-pulse 1.4s ease-in-out infinite;
}

@keyframes usage-card-in {
  from { opacity: 0; transform: translateY(10px) scale(0.985); }
  to { opacity: 1; transform: translateY(0) scale(1); }
}

@keyframes usage-cell-pulse {
  0%, 100% { opacity: 0.35; }
  45% { opacity: 1; background-color: color-mix(in oklab, var(--primary) 35%, transparent); }
}

@media (prefers-reduced-motion: reduce) {
  .usage-overview,
  .usage-dashboard,
  .usage-loading-cell,
  .usage-chart-loading-bar,
  .usage-skeleton {
    animation: none;
  }
}

@container (max-width: 680px) {
  .usage-dashboard-header {
    flex-direction: column;
    gap: 0.75rem;
  }

  .usage-range-control {
    align-self: stretch;
  }

  .usage-range-button {
    flex: 1;
  }

  .usage-stat-grid,
  .usage-insight-strip {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .usage-insight-strip > span:nth-child(3) {
    border-left: 0;
    border-top: 1px solid color-mix(in oklab, var(--border) 66%, transparent);
  }

  .usage-insight-strip > span:nth-child(4) {
    border-top: 1px solid color-mix(in oklab, var(--border) 66%, transparent);
  }

  .usage-secondary-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

@container (max-width: 420px) {
  .usage-stat-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .usage-stat-card {
    min-height: 5.7rem;
  }

  .usage-donut-layout {
    grid-template-columns: minmax(0, 1fr);
    justify-items: center;
  }

  .usage-breakdown-list {
    width: 100%;
  }
}

@container (max-width: 470px) {
  .usage-metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    row-gap: 0.5rem;
  }

  .usage-metric:nth-child(odd) {
    border-left-color: transparent;
  }
}
</style>
