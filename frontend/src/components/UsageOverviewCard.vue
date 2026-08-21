<script setup lang="ts">
import { Activity, BarChart3 } from '@lucide/vue'
import { computed, onMounted, shallowRef, watch } from 'vue'
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
let loadSequence = 0

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

const heatmapDays = computed<HeatmapDay[]>(() => {
  const buckets = new Map((usage.value?.dailyBuckets ?? []).map((item) => [item.startDate.slice(0, 10), Math.max(0, item.tokens)]))
  const end = new Date()
  end.setHours(0, 0, 0, 0)
  const oldestBucket = activeRange.value === 'cumulative'
    ? [...buckets.keys()]
        .map((key) => {
          const [year, month, day] = key.split('-').map(Number)
          return year && month && day ? new Date(year, month - 1, day) : null
        })
        .filter((date): date is Date => Boolean(date && !Number.isNaN(date.getTime())))
        .sort((left, right) => left.getTime() - right.getTime())[0]
    : null
  const cumulativeDays = oldestBucket
    ? Math.round((Date.UTC(end.getFullYear(), end.getMonth(), end.getDate())
        - Date.UTC(oldestBucket.getFullYear(), oldestBucket.getMonth(), oldestBucket.getDate())) / 86_400_000) + 1
    : 91
  const span = activeRange.value === 'cumulative'
    ? Math.min(182, Math.max(91, cumulativeDays))
    : activeRange.value
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

watch(() => props.runtime, loadUsage)
watch(
  () => appStore.accountUsage,
  (value) => {
    if (appStore.activeRuntime === props.runtime && value) usage.value = value
  },
)
onMounted(loadUsage)
</script>

<template>
  <section
    class="usage-overview overflow-hidden rounded-2xl border border-border/70 bg-card/90 text-left shadow-sm backdrop-blur-sm"
    :class="compact ? 'w-full' : 'w-full max-w-2xl'"
  >
    <header class="flex items-center justify-between gap-3 border-b border-border/60 px-4 py-3">
      <div class="flex min-w-0 items-center gap-2.5">
        <div class="grid size-8 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
          <Activity :size="15" />
        </div>
        <div class="min-w-0">
          <h3 class="truncate text-[13px] font-semibold">{{ t('usageOverview.title') }}</h3>
          <p class="truncate text-[10px] text-muted-foreground">{{ t('usageOverview.subtitle') }}</p>
        </div>
      </div>
      <div class="flex shrink-0 rounded-lg bg-muted/65 p-0.5" role="tablist" :aria-label="t('usageOverview.title')">
        <button
          v-for="tab in (['overview', 'models'] as OverviewTab[])"
          :key="tab"
          type="button"
          role="tab"
          class="rounded-md px-2.5 py-1 text-[10px] transition-colors"
          :class="activeTab === tab ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'"
          :aria-selected="activeTab === tab"
          @click="activeTab = tab"
        >
          {{ t(`usageOverview.${tab}`) }}
        </button>
      </div>
    </header>

    <div v-if="activeTab === 'overview'" class="space-y-3 p-4">
      <div class="flex items-center justify-end gap-3">
        <div class="flex rounded-lg border border-border/60 p-0.5">
          <button
            v-for="option in rangeOptions"
            :key="String(option.value)"
            type="button"
            class="rounded-md px-2 py-1 text-[10px] transition-colors"
            :class="activeRange === option.value ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:text-foreground'"
            @click="activeRange = option.value"
          >
            {{ option.label }}
          </button>
        </div>
      </div>

      <div class="usage-heatmap scrollbar-thin min-h-[74px] overflow-x-auto rounded-xl border border-border/55 bg-muted/20 p-2.5">
        <div
          v-if="loading"
          class="heatmap-grid min-w-full"
          :class="activeRange === 7 ? 'is-strip' : 'is-calendar'"
          aria-hidden="true"
        >
          <span
            v-for="index in (activeRange === 7 ? 7 : activeRange === 30 ? 30 : 182)"
            :key="index"
            class="usage-loading-cell h-full w-full rounded-[3px] bg-muted-foreground/15"
            :style="{ animationDelay: `${index * 24}ms` }"
          />
        </div>
        <div
          v-else
          class="heatmap-grid min-w-full"
          :class="activeRange === 7 ? 'is-strip' : 'is-calendar'"
          aria-label="Token usage heatmap"
        >
          <span
            v-for="(day, index) in heatmapDays"
            :key="day.key"
            class="h-full w-full rounded-[3px] ring-1 ring-inset ring-foreground/[0.035] transition-transform hover:scale-125"
            :class="heatmapClass(day.intensity)"
            :title="dateTooltip(day)"
            :style="index === 0 && activeRange !== 7 ? { gridRowStart: day.date.getDay() + 1 } : undefined"
          />
        </div>
      </div>

      <div class="grid grid-cols-2 gap-1.5 sm:grid-cols-4">
        <div class="rounded-lg bg-muted/45 px-2.5 py-2">
          <p class="text-[10px] text-muted-foreground">{{ t('usageOverview.sessions') }}</p>
          <p class="mt-0.5 truncate text-sm font-semibold tabular-nums">{{ modelSessionCount }}</p>
        </div>
        <div class="rounded-lg bg-muted/45 px-2.5 py-2">
          <p class="text-[10px] text-muted-foreground">{{ t('usageOverview.totalTokens') }}</p>
          <p class="mt-0.5 truncate text-sm font-semibold tabular-nums">{{ formatTokenCount(rangeTotal) }}</p>
        </div>
        <div class="rounded-lg bg-muted/45 px-2.5 py-2">
          <p class="text-[10px] text-muted-foreground">{{ t('usageOverview.activeDays') }}</p>
          <p class="mt-0.5 truncate text-sm font-semibold tabular-nums">{{ rangeView.dayCount }}</p>
        </div>
        <div class="rounded-lg bg-muted/45 px-2.5 py-2">
          <p class="text-[10px] text-muted-foreground">{{ t('usageOverview.dailyAverage') }}</p>
          <p class="mt-0.5 truncate text-sm font-semibold tabular-nums">{{ formatTokenCount(rangeView.averageTokens) }}</p>
        </div>
        <div class="rounded-lg bg-muted/45 px-2.5 py-2">
          <p class="text-[10px] text-muted-foreground">{{ t('usageOverview.currentStreak') }}</p>
          <p class="mt-0.5 truncate text-sm font-semibold tabular-nums">{{ usage?.currentStreakDays ?? 0 }}</p>
        </div>
        <div class="rounded-lg bg-muted/45 px-2.5 py-2">
          <p class="text-[10px] text-muted-foreground">{{ t('usageOverview.longestStreak') }}</p>
          <p class="mt-0.5 truncate text-sm font-semibold tabular-nums">{{ usage?.longestStreakDays ?? 0 }}</p>
        </div>
        <div class="rounded-lg bg-muted/45 px-2.5 py-2">
          <p class="text-[10px] text-muted-foreground">{{ t('usageOverview.peakDay') }}</p>
          <p class="mt-0.5 truncate text-sm font-semibold tabular-nums">{{ formatTokenCount(usage?.peakDailyTokens) }}</p>
        </div>
        <div class="rounded-lg bg-muted/45 px-2.5 py-2">
          <p class="text-[10px] text-muted-foreground">{{ t('usageOverview.favoriteModel') }}</p>
          <p class="mt-0.5 truncate text-sm font-semibold" :title="favoriteModel">{{ favoriteModel }}</p>
        </div>
      </div>
      <p v-if="loadError" class="truncate text-[9px] text-muted-foreground" :title="loadError">
        {{ t('usageOverview.loadFailed') }}
      </p>
    </div>

    <div v-else class="p-4">
      <div v-if="modelRows.length" class="grid gap-5 sm:grid-cols-[168px_minmax(0,1fr)] sm:items-center">
        <div class="relative mx-auto grid size-36 place-items-center rounded-full" :style="modelDonutStyle">
          <div class="grid size-24 place-items-center rounded-full bg-card text-center shadow-inner">
            <div>
              <p class="text-xl font-semibold tabular-nums">{{ modelSessionCount }}</p>
              <p class="text-[9px] text-muted-foreground">{{ t('usageOverview.sessions') }}</p>
            </div>
          </div>
        </div>
        <div class="space-y-3">
          <div v-for="(row, index) in modelRows" :key="row.model" class="space-y-1.5">
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
      <div v-else class="grid min-h-36 place-items-center text-center">
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
  grid-auto-flow: column;
  gap: 4px;
}

.heatmap-grid.is-strip {
  grid-auto-columns: 14px;
  grid-template-rows: 14px;
  justify-content: center;
}

.heatmap-grid.is-calendar {
  grid-auto-columns: 14px;
  grid-template-rows: repeat(7, 14px);
  justify-content: center;
}

.usage-overview {
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
</style>
