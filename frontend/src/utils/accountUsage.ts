import type { AccountUsageDailyBucket, AccountUsageSummary, ThreadTokenUsage } from '@/types/codex'

export const CODEX_CONTEXT_BASELINE_TOKENS = 12_000

export type UsageRangeDays = 1 | 7 | 14 | 30

export type UsageRangeView = {
  days: UsageRangeDays
  totalTokens: number
  dayCount: number
  averageTokens: number
  buckets: AccountUsageDailyBucket[]
  maxTokens: number
}

export type ContextUsageView = {
  available: boolean
  usedTokens: number
  contextWindow: number
  usedPercent: number
}

type ProviderModelContext = {
  model: string
  contextWindow?: number | null
}

type ProviderContextCatalog = {
  kind: string
  models?: ProviderModelContext[] | null
}

export function resolveProviderModelContextWindow(
  providers: ProviderContextCatalog[] | null | undefined,
  runtime: 'grok' | 'claude',
  model: string,
): number {
  const normalizedModel = model.trim().toLowerCase()
  if (!normalizedModel) return 0
  const catalogs = providers ?? []
  const runtimeCatalog = catalogs.find((provider) => provider.kind === runtime)
  const exact = runtimeCatalog?.models?.find((item) => item.model.trim().toLowerCase() === normalizedModel)
    ?? catalogs.flatMap((provider) => provider.models ?? [])
      .find((item) => item.model.trim().toLowerCase() === normalizedModel)
  const exactWindow = Math.max(0, Number(exact?.contextWindow) || 0)
  if (exactWindow > 0) return exactWindow

  if (runtime === 'claude') {
    const family = ['fable', 'opus', 'sonnet', 'haiku']
      .find((name) => normalizedModel.includes(name))
    if (family) {
      const alias = runtimeCatalog?.models?.find((item) => item.model.trim().toLowerCase() === family)
      return Math.max(0, Number(alias?.contextWindow) || 0)
    }
  }
  return 0
}

export function buildContextUsageView(
  usage: ThreadTokenUsage | null | undefined,
  baselineTokens = 0,
): ContextUsageView {
  const contextWindow = Math.max(0, Number(usage?.modelContextWindow) || 0)
  const usedTokens = Math.max(0, Number(usage?.last.totalTokens) || 0)
  if (!contextWindow) {
    return { available: false, usedTokens, contextWindow: 0, usedPercent: 0 }
  }

  const requestedBaseline = Math.max(0, baselineTokens)
  const baseline = contextWindow > requestedBaseline ? requestedBaseline : 0
  const effectiveWindow = Math.max(1, contextWindow - baseline)
  const effectiveUsed = Math.max(0, usedTokens - baseline)
  const usedPercent = Math.min(100, Math.max(0, (effectiveUsed / effectiveWindow) * 100))

  return { available: true, usedTokens, contextWindow, usedPercent }
}

function startOfLocalDay(date: Date): Date {
  const next = new Date(date)
  next.setHours(0, 0, 0, 0)
  return next
}

function parseBucketDate(value: string): Date | null {
  const trimmed = value.trim()
  if (!trimmed) return null
  // Prefer YYYY-MM-DD as local calendar day.
  const match = trimmed.match(/^(\d{4})-(\d{2})-(\d{2})/)
  if (match) {
    const year = Number(match[1])
    const month = Number(match[2]) - 1
    const day = Number(match[3])
    const local = new Date(year, month, day)
    return Number.isNaN(local.getTime()) ? null : local
  }
  const parsed = new Date(trimmed)
  return Number.isNaN(parsed.getTime()) ? null : startOfLocalDay(parsed)
}

export function buildUsageRangeView(
  usage: AccountUsageSummary | null | undefined,
  days: UsageRangeDays,
): UsageRangeView {
  const buckets = usage?.dailyBuckets ?? []
  const cutoff = startOfLocalDay(new Date())
  cutoff.setDate(cutoff.getDate() - (days - 1))

  const filtered = buckets
    .map((bucket) => ({ bucket, date: parseBucketDate(bucket.startDate) }))
    .filter((item): item is { bucket: AccountUsageDailyBucket; date: Date } => Boolean(item.date))
    .filter((item) => item.date.getTime() >= cutoff.getTime())
    .sort((a, b) => b.date.getTime() - a.date.getTime())
    .map((item) => item.bucket)

  const totalTokens = filtered.reduce((sum, item) => sum + Math.max(0, item.tokens), 0)
  const maxTokens = filtered.reduce((max, item) => Math.max(max, item.tokens), 0)
  const dayCount = filtered.length
  return {
    days,
    totalTokens,
    dayCount,
    averageTokens: dayCount ? Math.round(totalTokens / dayCount) : 0,
    buckets: filtered,
    maxTokens,
  }
}

export function formatTokenCount(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '—'
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`
  if (value >= 10_000) return `${Math.round(value / 1000)}K`
  if (value >= 1000) return `${(value / 1000).toFixed(1)}K`
  return Math.round(value).toLocaleString()
}

export function formatUsageDateLabel(startDate: string, locale = 'zh-CN'): string {
  const date = parseBucketDate(startDate)
  if (!date) return startDate
  return new Intl.DateTimeFormat(locale, { month: 'numeric', day: 'numeric', weekday: 'short' }).format(date)
}
