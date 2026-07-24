import type { Status as CodexStatus } from '../../bindings/nice_codex_desktop/internal/codex/models'
import type {
  AccountInfo,
  AccountRateLimits,
  AccountUsageSummary,
  FileChangeView,
  MessageAttachment,
  ModelOption,
  ModelProviderOption,
  RateLimitWindow,
  ThreadSummary,
  ThreadTokenUsage,
  TurnMetrics,
  TimelineItem,
  TimelineItemType,
  TokenUsageBreakdown,
} from '../types/codex'

export function asRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

export function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

export function asString(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

/** Normalize Codex / CLI status strings: inProgress, in_progress, running, … */
function normalizeStatusKey(status: string): string {
  return status.trim().toLowerCase().replace(/[_-]/g, '')
}

/** Turn or item is still actively running. */
export function isActiveStatus(status: unknown): boolean {
  const key = normalizeStatusKey(asString(status))
  return key === 'inprogress' || key === 'running' || key === 'started' || key === 'pending' || key === 'active'
}

export function isFailedStatus(status: unknown): boolean {
  const key = normalizeStatusKey(asString(status))
  return key === 'failed' || key === 'error'
}

export function isInterruptedStatus(status: unknown): boolean {
  const key = normalizeStatusKey(asString(status))
  return key === 'interrupted' || key === 'cancelled' || key === 'canceled'
}

/** Turn has finished (success or failure) — anything else should keep the queue blocked. */
export function isTerminalTurnStatus(status: unknown): boolean {
  const key = normalizeStatusKey(asString(status))
  if (!key) return false
  return key === 'completed'
    || key === 'complete'
    || key === 'done'
    || key === 'success'
    || key === 'succeeded'
    || isFailedStatus(status)
    || isInterruptedStatus(status)
}

export function asNumber(value: unknown, fallback = 0): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'bigint') {
    const asFloat = Number(value)
    return Number.isFinite(asFloat) ? asFloat : fallback
  }
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : fallback
  }
  return fallback
}

export function normalizeThread(value: unknown): ThreadSummary | null {
  const record = asRecord(value)
  const id = asString(record.id)
  if (!id) return null

  const status = asRecord(record.status)
  const name = asString(record.name)
  const preview = asString(record.preview)
  return {
    id,
    name: name || preview || 'New task',
    preview,
    cwd: asString(record.cwd),
    createdAt: asNumber(record.createdAt),
    updatedAt: asNumber(record.recencyAt, asNumber(record.updatedAt)),
    status: asString(status.type, 'idle'),
    cliVersion: asString(record.cliVersion),
    model: asString(record.model),
    modelProvider: asString(record.modelProvider),
    effort: asString(record.effort),
    collaborationMode: asString(record.collaborationMode, 'default') || 'default',
    workMode: asString(record.workMode, 'code') || 'code',
    useMemories: typeof record.useMemories === 'boolean' ? record.useMemories : undefined,
    generateMemories: typeof record.generateMemories === 'boolean' ? record.generateMemories : undefined,
    turns: asArray(record.turns),
  }
}

export function normalizeThreads(value: unknown): ThreadSummary[] {
  return asArray(value)
    .map(normalizeThread)
    .filter((thread): thread is ThreadSummary => thread !== null)
}

export function normalizeStatus(value: unknown): CodexStatus {
  const status = asRecord(value)
  return {
    state: asString(status.state, 'disconnected'),
    running: status.running === true,
    message: asString(status.message),
    binary: asString(status.binary),
    version: asString(status.version),
    workspace: asString(status.workspace),
  }
}

export function normalizeModel(value: unknown): ModelOption | null {
  const record = asRecord(value)
  const id = asString(record.id, asString(record.model))
  if (!id) return null
  const catalogTiers = asArray(record.serviceTiers).map((tier) => {
    const entry = asRecord(tier)
    return {
      id: asString(entry.id),
      name: asString(entry.name, asString(entry.id)),
      description: asString(entry.description),
    }
  }).filter((tier) => tier.id !== '')
  const serviceTiers = catalogTiers.length
    ? catalogTiers
    : asArray(record.additionalSpeedTiers).map((tier) => asString(tier)).filter(Boolean).map((tier) => ({
        id: tier,
        name: tier,
        description: '',
      }))

  return {
    id,
    model: asString(record.model, id),
    displayName: asString(record.displayName, id),
    description: asString(record.description),
    isDefault: record.isDefault === true,
    defaultReasoningEffort: asString(record.defaultReasoningEffort, 'high'),
    defaultServiceTier: asString(record.defaultServiceTier),
    serviceTiers,
    supportsPersonality: record.supportsPersonality === true,
    supportedReasoningEfforts: asArray(record.supportedReasoningEfforts).map((option) => {
      const entry = asRecord(option)
      return {
        effort: asString(entry.reasoningEffort, asString(entry.effort)),
        description: asString(entry.description),
      }
    }).filter((option) => option.effort !== ''),
  }
}

export function normalizeModels(value: unknown): ModelOption[] {
  return asArray(value)
    .map(normalizeModel)
    .filter((model): model is ModelOption => model !== null)
}

export function normalizeModelProviders(value: unknown): ModelProviderOption[] {
  return asArray(value).map((value) => {
    const record = asRecord(value)
    const kind = asString(record.kind, 'custom')
    return {
      id: asString(record.id),
      name: asString(record.name, asString(record.id)),
      kind: ['codex', 'claude', 'gemini', 'grok', 'custom'].includes(kind)
        ? kind as ModelProviderOption['kind']
        : 'custom',
      configured: record.configured !== false,
    }
  }).filter((provider) => provider.name !== '')
}

export function normalizeAccount(value: unknown): AccountInfo {
  const response = asRecord(value)
  const account = asRecord(response.account)
  const type = asString(account.type)
  return {
    authenticated: type !== '',
    type,
    email: asString(account.email),
    planType: asString(account.planType),
    requiresOpenAIAuth: response.requiresOpenaiAuth === true,
  }
}

export function normalizeThreadTokenUsage(value: unknown): ThreadTokenUsage {
  const record = asRecord(value)
  return {
    total: normalizeTokenBreakdown(record.total),
    last: normalizeTokenBreakdown(record.last),
    modelContextWindow: nullableNumber(record.modelContextWindow),
  }
}

export function normalizeAccountRateLimits(
  value: unknown,
  current: AccountRateLimits | null = null,
): AccountRateLimits | null {
  const response = asRecord(value)
  const nested = asRecord(response.rateLimits)
  const snapshot = Object.keys(nested).length ? nested : response
  if (!Object.keys(snapshot).length) return current

  return {
    limitName: asString(snapshot.limitName, current?.limitName ?? ''),
    planType: asString(snapshot.planType, current?.planType ?? ''),
    rateLimitReachedType: asString(snapshot.rateLimitReachedType, current?.rateLimitReachedType ?? ''),
    primary: snapshot.primary !== undefined && snapshot.primary !== null
      ? normalizeRateLimitWindow(snapshot.primary)
      : current?.primary ?? null,
    secondary: snapshot.secondary !== undefined && snapshot.secondary !== null
      ? normalizeRateLimitWindow(snapshot.secondary)
      : current?.secondary ?? null,
    credits: snapshot.credits !== undefined && snapshot.credits !== null
      ? normalizeCredits(snapshot.credits, current?.credits ?? null)
      : current?.credits ?? null,
  }
}

export function normalizeAccountUsage(value: unknown): AccountUsageSummary | null {
  const response = asRecord(value)
  const summary = asRecord(response.summary)
  const bucketsRaw = response.dailyUsageBuckets ?? response.daily_usage_buckets
  const dailyBuckets = asArray(bucketsRaw).map((item) => {
    const record = asRecord(item)
    return {
      startDate: asString(record.startDate, asString(record.start_date)),
      tokens: asNumber(record.tokens),
      inputTokens: asNumber(record.inputTokens ?? record.input_tokens),
      cachedInputTokens: asNumber(record.cachedInputTokens ?? record.cached_input_tokens),
      outputTokens: asNumber(record.outputTokens ?? record.output_tokens),
      reasoningOutputTokens: asNumber(record.reasoningOutputTokens ?? record.reasoning_output_tokens),
    }
  }).filter((item) => item.startDate && item.tokens > 0)

  const lifetimeTokens = nullableNumber(summary.lifetimeTokens ?? summary.lifetime_tokens)
  const lifetimeInputTokens = nullableNumber(summary.lifetimeInputTokens ?? summary.lifetime_input_tokens)
  const lifetimeCachedInputTokens = nullableNumber(summary.lifetimeCachedInputTokens ?? summary.lifetime_cached_input_tokens)
  const lifetimeOutputTokens = nullableNumber(summary.lifetimeOutputTokens ?? summary.lifetime_output_tokens)
  const lifetimeReasoningTokens = nullableNumber(summary.lifetimeReasoningTokens ?? summary.lifetime_reasoning_tokens)
  const peakDailyTokens = nullableNumber(summary.peakDailyTokens ?? summary.peak_daily_tokens)
  const currentStreakDays = nullableNumber(summary.currentStreakDays ?? summary.current_streak_days)
  const longestStreakDays = nullableNumber(summary.longestStreakDays ?? summary.longest_streak_days)
  const longestRunningTurnSec = nullableNumber(summary.longestRunningTurnSec ?? summary.longest_running_turn_sec)
  const runtime = asString(response.runtime)

  const hasSummary = [
    lifetimeTokens,
    lifetimeInputTokens,
    lifetimeCachedInputTokens,
    lifetimeOutputTokens,
    lifetimeReasoningTokens,
    peakDailyTokens,
    currentStreakDays,
    longestStreakDays,
    longestRunningTurnSec,
  ].some((item) => item != null && item > 0)
  if (!hasSummary && !dailyBuckets.length) return null

  return {
    lifetimeTokens,
    lifetimeInputTokens,
    lifetimeCachedInputTokens,
    lifetimeOutputTokens,
    lifetimeReasoningTokens,
    peakDailyTokens,
    currentStreakDays,
    longestStreakDays,
    longestRunningTurnSec,
    dailyBuckets,
    runtime: runtime || undefined,
  }
}

function normalizeTokenBreakdown(value: unknown): TokenUsageBreakdown {
  const record = asRecord(value)
  // Codex app-server / rollouts use snake_case; Grok may use either.
  const inputTokens = asNumber(record.inputTokens ?? record.input_tokens ?? record.prompt_tokens)
  const cachedInputTokens = asNumber(
    record.cachedInputTokens
    ?? record.cached_input_tokens
    ?? record.cache_read_input_tokens
    ?? record.cachedReadTokens
    ?? record.cacheReadInputTokens,
  )
  const outputTokens = asNumber(record.outputTokens ?? record.output_tokens ?? record.completion_tokens)
  const reasoningOutputTokens = asNumber(
    record.reasoningOutputTokens
    ?? record.reasoning_output_tokens
    ?? record.reasoning_tokens
    ?? record.reasoningTokens,
  )
  let totalTokens = asNumber(record.totalTokens ?? record.total_tokens)
  if (totalTokens <= 0) {
    totalTokens = inputTokens + cachedInputTokens + outputTokens + reasoningOutputTokens
  }
  // Codex often reports input_tokens as FULL prompt (includes cache). Prefer uncached for display.
  let uncachedInput = inputTokens
  if (
    cachedInputTokens > 0
    && inputTokens >= cachedInputTokens
    && totalTokens > 0
    && Math.abs(totalTokens - (inputTokens + outputTokens)) <= 2
  ) {
    uncachedInput = Math.max(0, inputTokens - cachedInputTokens)
  }
  return {
    inputTokens: uncachedInput,
    cachedInputTokens,
    outputTokens,
    reasoningOutputTokens,
    totalTokens,
  }
}

function normalizeRateLimitWindow(value: unknown): RateLimitWindow {
  const record = asRecord(value)
  return {
    usedPercent: Math.min(100, Math.max(0, asNumber(record.usedPercent))),
    resetsAt: nullableNumber(record.resetsAt),
    windowDurationMins: nullableNumber(record.windowDurationMins),
  }
}

function normalizeCredits(
  value: unknown,
  current: AccountRateLimits['credits'],
): NonNullable<AccountRateLimits['credits']> {
  const record = asRecord(value)
  return {
    hasCredits: typeof record.hasCredits === 'boolean' ? record.hasCredits : current?.hasCredits ?? false,
    unlimited: typeof record.unlimited === 'boolean' ? record.unlimited : current?.unlimited ?? false,
    balance: asString(record.balance, current?.balance ?? ''),
  }
}

function nullableNumber(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null
  const parsed = asNumber(value, Number.NaN)
  return Number.isFinite(parsed) ? parsed : null
}

export function normalizeTimelineItem(value: unknown, turnId = ''): TimelineItem | null {
  const record = asRecord(value)
  const rawType = asString(record.type, asString(record.role, asString(record.kind)))
  const id = asString(record.id, asString(record.itemId, asString(record.messageId, asString(record.callId))))
    || `${turnId || 'item'}:${rawType || 'message'}:${stableTextHash(formatJSON(record))}`
  if (!rawType) return null

  const type = supportedItemType(rawType)
  const base: TimelineItem = {
    id,
    turnId,
    type,
    status: asString(record.status),
    text: '',
    command: '',
    cwd: asString(record.cwd),
    output: '',
    title: '',
    detail: '',
    changes: [],
    attachments: [],
  }

  switch (type) {
    case 'userMessage':
      {
        const rawContent = record.content ?? record.message ?? record.text
        const content = asArray(rawContent).map((item) => asRecord(item))
        base.text = content.length
          ? content.filter((part) => part.type === 'text' || part.type === 'input_text' || part.type === 'inputText').map((part) => normalizeTextValue(part.text ?? part.content)).filter(Boolean).join('\n')
          : normalizeTextValue(rawContent)
        base.attachments = normalizeMessageAttachments(content)
      }
      break
    case 'agentMessage':
      base.phase = asString(record.phase)
      base.text = normalizeTextValue(record.text ?? record.content ?? record.message)
      break
    case 'plan':
      base.text = normalizeTextValue(record.text ?? record.content ?? record.plan)
      base.title = 'Plan'
      break
    case 'reasoning':
      base.reasoningSummary = boundedText(normalizeReasoningParts(record.summary ?? record.summaryText), 250_000)
      base.reasoningContent = boundedText(normalizeReasoningParts(record.content ?? record.rawContent ?? record.text), 250_000)
      base.text = base.reasoningSummary || base.reasoningContent
      break
    case 'commandExecution':
      base.command = asString(record.command)
      base.output = boundedText(normalizeTextValue(record.aggregatedOutput ?? record.output ?? record.result), 300_000)
      base.detail = record.exitCode === null || record.exitCode === undefined
        ? ''
        : `Exit ${asNumber(record.exitCode)}`
      base.title = base.command ? `Ran ${base.command}` : 'Command'
      break
    case 'fileChange':
      base.changes = normalizeFileChanges(record.changes)
      base.title = fileChangeTitle(base.changes)
      base.status = asString(record.status, base.status)
      break
    case 'mcpToolCall':
      base.title = `${asString(record.server)} / ${asString(record.tool)}`
      base.detail = formatJSON(record.arguments)
      base.output = boundedText(asString(asRecord(record.error).message) || normalizeToolOutput(asRecord(record.result).content, asRecord(record.result).structuredContent), 200_000)
      break
    case 'collabAgentToolCall':
      base.title = asString(record.tool, 'Agent collaboration')
      base.detail = asString(record.prompt)
      break
    case 'webSearch':
      base.title = 'Web search'
      base.detail = asString(record.query)
      break
    case 'imageGeneration':
      base.title = 'Image generation'
      base.detail = asString(record.savedPath, asString(record.result))
      break
    case 'hookPrompt':
      base.title = 'Hook prompt'
      base.text = asArray(record.fragments).map((fragment) => asString(asRecord(fragment).text)).filter(Boolean).join('\n')
      break
    case 'dynamicToolCall':
      base.title = [asString(record.namespace), asString(record.tool)].filter(Boolean).join(' / ')
      base.detail = formatJSON(record.arguments)
      base.output = boundedText(normalizeToolOutput(record.contentItems), 200_000)
      if (record.success === false && !base.output) base.output = 'Tool call failed'
      break
    case 'subAgentActivity':
      base.title = asString(record.agentPath, 'Sub-agent')
      base.detail = asString(record.kind)
      break
    case 'imageView':
      base.title = 'Image viewed'
      base.detail = asString(record.path)
      break
    case 'sleep':
      base.title = 'Wait'
      base.detail = `${asNumber(record.durationMs)} ms`
      break
    case 'enteredReviewMode':
      base.title = 'Review mode started'
      base.detail = asString(record.review)
      break
    case 'exitedReviewMode':
      base.title = 'Review mode completed'
      base.detail = asString(record.review)
      break
    case 'contextCompaction':
      base.title = 'Context compacted'
      break
    default:
      base.title = humanize(rawType)
      base.detail = asString(record.review)
      break
  }

  return base
}

function normalizeMessageAttachments(content: Record<string, unknown>[]): MessageAttachment[] {
  return content.flatMap((item, index) => {
    const type = asString(item.type)
    const source = type === 'localImage'
      ? asString(item.path)
      : type === 'image' ? asString(item.url) : ''
    if (!source) return []
    return [{
      kind: type === 'localImage' ? 'local' : 'remote',
      source,
      name: attachmentName(source, index),
    } satisfies MessageAttachment]
  })
}

function attachmentName(source: string, index: number): string {
  const cleanSource = source.split(/[?#]/, 1)[0] ?? source
  const name = cleanSource.split(/[\\/]/).filter(Boolean).at(-1)
  return name || `Image ${index + 1}`
}

function normalizeTextValue(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (Array.isArray(value)) return value.map(normalizeTextValue).filter(Boolean).join('\n\n')
  if (value && typeof value === 'object') {
    const record = asRecord(value)
    return normalizeTextValue(record.text ?? record.content ?? record.value ?? record.summary ?? record.output ?? record.delta ?? record.parts)
  }
  return ''
}

/** Reasoning summary/content may be string[], plain string, or legacy part objects. */
function normalizeReasoningParts(value: unknown): string {
  if (Array.isArray(value)) {
    return value
      .map((part) => {
        if (typeof part === 'string') return part.trim()
        const record = asRecord(part)
        // Official shapes: { type: 'summary_text'|'reasoning_text', text }
        return normalizeTextValue(
          record.text
          ?? record.content
          ?? record.summary
          ?? record.value
          ?? part,
        ).trim()
      })
      .filter(Boolean)
      .join('\n\n')
  }
  return normalizeTextValue(value).trim()
}

function normalizeToolOutput(content: unknown, structuredContent?: unknown): string {
  const parts = asArray(content).map((value) => {
    const record = asRecord(value)
    if (record.type === 'text' || record.type === 'inputText') return asString(record.text)
    return formatJSON(value)
  }).filter(Boolean)
  const structured = formatJSON(structuredContent)
  if (structured) parts.push(structured)
  return parts.join('\n\n')
}

function formatJSON(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  if (typeof value !== 'object') return String(value)
  if (Array.isArray(value) && value.length === 0) return ''
  if (!Array.isArray(value) && Object.keys(asRecord(value)).length === 0) return ''
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function boundedText(value: string, limit: number): string {
  if (value.length <= limit) return value
  return `${value.slice(0, limit)}\n\n[Output truncated for performance]`
}

export function timelineFromTurns(value: unknown): TimelineItem[] {
  return asArray(value).flatMap((turnValue) => {
    const turn = asRecord(turnValue)
    const turnId = asString(turn.id)
    const rawItems = turn.items ?? turn.messages ?? turn.content
    return asArray(rawItems)
      .map((item) => normalizeTimelineItem(item, turnId))
      .filter((item): item is TimelineItem => item !== null)
  })
}

export function metricsFromTurns(value: unknown): Record<string, TurnMetrics> {
  const result: Record<string, TurnMetrics> = {}
  for (const turnValue of asArray(value)) {
    const turn = asRecord(turnValue)
    const turnId = asString(turn.id)
    if (!turnId) continue
    const startedAt = nullableNumber(turn.startedAt)
    const completedAt = nullableNumber(turn.completedAt)
    result[turnId] = {
      tokenUsage: null,
      startedAt: startedAt === null ? null : startedAt * 1000,
      completedAt: completedAt === null ? null : completedAt * 1000,
      durationMs: nullableNumber(turn.durationMs),
    }
  }
  return result
}

function normalizeFileChanges(value: unknown): FileChangeView[] {
  const entries = Array.isArray(value)
    ? value
    : (value !== null && typeof value === 'object')
      ? Object.entries(asRecord(value)).map(([path, changeValue]) => {
          const change = asRecord(changeValue)
          return { ...change, path: asString(change.path, path) }
        })
      : []

  return entries.map((changeValue) => {
    const change = asRecord(changeValue)
    return {
      path: asString(change.path, asString(change.filePath, 'Unknown file')),
      kind: normalizePatchKind(change.kind ?? change.type),
      diff: boundedText(
        asString(change.diff, asString(change.unifiedDiff, asString(change.content))),
        300_000,
      ),
    }
  })
}

/** Codex PatchChangeKind is tagged `{ type: "add"|"delete"|"update", movePath? }`. */
function normalizePatchKind(value: unknown): string {
  if (typeof value === 'string') {
    const key = value.trim().toLowerCase()
    if (key === 'add' || key === 'added' || key === 'create') return 'add'
    if (key === 'delete' || key === 'deleted' || key === 'remove') return 'delete'
    return 'update'
  }
  const record = asRecord(value)
  return normalizePatchKind(asString(record.type, asString(record.kind, 'update')))
}

function fileChangeTitle(changes: FileChangeView[]): string {
  if (!changes.length) return 'Applying patch'
  if (changes.length === 1) {
    const name = changes[0]?.path.split(/[\\/]/).filter(Boolean).at(-1) || 'file'
    const stem = name.replace(/\.[^.]+$/, '') || name
    return `Applying patch to ${stem}`
  }
  return `Applying patch to ${changes.length} files`
}

function supportedItemType(value: string): TimelineItemType {
  const normalized = value.replace(/[\s_-]+/g, '').toLocaleLowerCase()
  const aliases: Record<string, TimelineItemType> = {
    user: 'userMessage',
    human: 'userMessage',
    inputmessage: 'userMessage',
    usermessage: 'userMessage',
    assistant: 'agentMessage',
    agent: 'agentMessage',
    message: 'agentMessage',
    assistantmessage: 'agentMessage',
    outputtext: 'agentMessage',
    reasoningsummary: 'reasoning',
    thinking: 'reasoning',
    command: 'commandExecution',
    commandresult: 'commandExecution',
    shellexecution: 'commandExecution',
    patch: 'fileChange',
    filechanges: 'fileChange',
    toolcall: 'dynamicToolCall',
    toolresult: 'dynamicToolCall',
    functioncall: 'dynamicToolCall',
    functionresult: 'dynamicToolCall',
    mcp: 'mcpToolCall',
  }
  if (aliases[normalized]) return aliases[normalized]
  const supported: TimelineItemType[] = [
    'userMessage',
    'agentMessage',
    'reasoning',
    'plan',
    'commandExecution',
    'fileChange',
    'mcpToolCall',
    'collabAgentToolCall',
    'webSearch',
    'imageGeneration',
    'hookPrompt',
    'dynamicToolCall',
    'subAgentActivity',
    'imageView',
    'sleep',
    'enteredReviewMode',
    'exitedReviewMode',
    'contextCompaction',
    'notice',
  ]
  return supported.includes(value as TimelineItemType) ? (value as TimelineItemType) : 'notice'
}

function stableTextHash(value: string): string {
  let hash = 2166136261
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0).toString(36)
}

function humanize(value: string): string {
  return value.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/^./, (letter) => letter.toUpperCase())
}
