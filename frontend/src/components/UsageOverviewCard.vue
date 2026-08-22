<script setup lang="ts">
import { BarChart3 } from '@lucide/vue'
import { computed, nextTick, shallowRef, useTemplateRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { useAppStore, useClaudeStore, useCodexStore, useGrokStore } from '@/stores'
import type { WorkspaceRuntime } from '@/stores/app'
import type { AccountUsageSummary } from '@/types/codex'
import { buildUsageRangeView, formatTokenCount, type UsageRangeDays } from '@/utils/accountUsage'
import { normalizeAccountUsage } from '@/utils/protocol'

const props = withDefaults(defineProps<{
  runtime: WorkspaceRuntime
  compact?: boolean
}>(), {
  compact: false,
})

const appStore = useAppStore()
const codexStore = useCodexStore()
const grokStore = useGrokStore()
const claudeStore = useClaudeStore()
const { t, locale } = useI18n()

type OverviewTab = 'overview' | 'models'
type RangeOption = { value: UsageRangeDays; label: string }
type HeatmapDay = { key: string; date: Date; tokens: number; intensity: number }

const activeTab = shallowRef<OverviewTab>('overview')
const activeRange = shallowRef<UsageRangeDays>('cumulative')
const usage = shallowRef<AccountUsageSummary | null>(null)
const loading = shallowRef(false)
const loadError = shallowRef('')
const heatmapScroller = useTemplateRef<HTMLDivElement>('heatmapScroller')
const heatmapDragging = shallowRef(false)
let loadSequence = 0
let dragPointerId: number | null = null
let dragStartX = 0
let dragStartScrollLeft = 0

const rangeOptions = computed<RangeOption[]>(() => [
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
    claudeStore.sessions.forEach((session) => add(session.model))
  } else {
    const seen = new Set<string>()
    for (const group of codexStore.threadGroups) {
      for (const thread of group.threads) {
        if (seen.has(thread.id) || codexStore.runtimeIDForThread(thread.id) !== props.runtime) continue
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
  <section
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
            :title="dateTooltip(day)"
            :style="index === 0 && activeRange !== 7 ? { gridRowStart: day.date.getDay() + 1 } : undefined"
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
        <span class="min-w-0">{{ t('usageOverview.favoriteModel') }} <strong class="inline-block max-w-28 truncate align-bottom" :title="favoriteModel">{{ favoriteModel }}</strong></span>
      </div>
      <p v-if="loadError" class="truncate text-[9px] text-muted-foreground" :title="loadError">
        {{ t('usageOverview.loadFailed') }}
      </p>
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
              <span class="flex min-w-0 items-center gap-2 truncate font-medium" :title="row.model">
                <i class="size-2 shrink-0 rounded-sm" :style="{ background: modelChartPalette[index % modelChartPalette.length] }" />
                <span class="truncate">{{ row.model }}</span>
              </span>
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
</template>

<style scoped>
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
  .usage-loading-cell {
    animation: none;
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
