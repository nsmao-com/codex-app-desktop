export type ConnectionState = 'disconnected' | 'starting' | 'initializing' | 'ready' | 'stopping' | 'error'

export interface ThreadSummary {
  id: string
  name: string
  preview: string
  cwd: string
  createdAt: number
  updatedAt: number
  status: string
  cliVersion: string
  model: string
  modelProvider: string
  effort?: string
  collaborationMode?: string
  workMode?: string
  turns?: unknown[]
}

export interface ThreadGroup {
  path: string
  name: string
  active: boolean
  loading: boolean
  error: string
  threads: ThreadSummary[]
}

export type TurnFeedbackState = 'submitting' | 'running' | 'retrying' | 'failed' | 'interrupted'

export interface TurnFeedback {
  state: TurnFeedbackState
  message: string
  turnId: string
}

export type QueuedMessageState = 'queued' | 'sending' | 'failed'

export interface QueuedMessage {
  id: string
  threadId: string
  workspace: string
  text: string
  images: string[]
  createdAt: number
  localItemId: string
  retryItemId: string
  state: QueuedMessageState
  error: string
}

export type TimelineItemType =
  | 'userMessage'
  | 'agentMessage'
  | 'reasoning'
  | 'plan'
  | 'commandExecution'
  | 'fileChange'
  | 'mcpToolCall'
  | 'collabAgentToolCall'
  | 'webSearch'
  | 'imageGeneration'
  | 'hookPrompt'
  | 'dynamicToolCall'
  | 'subAgentActivity'
  | 'imageView'
  | 'sleep'
  | 'enteredReviewMode'
  | 'exitedReviewMode'
  | 'contextCompaction'
  | 'notice'

export interface FileChangeView {
  path: string
  kind: string
  diff: string
}

export interface MessageAttachment {
  kind: 'local' | 'remote'
  source: string
  name: string
}

export interface TimelineItem {
  id: string
  turnId: string
  type: TimelineItemType
  status: string
  text: string
  phase?: string
  reasoningSummary?: string
  reasoningContent?: string
  command: string
  cwd: string
  output: string
  title: string
  detail: string
  changes: FileChangeView[]
  attachments: MessageAttachment[]
  startedAt?: number
  completedAt?: number
  local?: boolean
  failed?: boolean
}

export interface ModelOption {
  id: string
  model: string
  displayName: string
  description: string
  isDefault: boolean
  defaultReasoningEffort: string
  defaultServiceTier: string
  serviceTiers: Array<{
    id: string
    name: string
    description: string
  }>
  supportsPersonality: boolean
  supportedReasoningEfforts: Array<{
    effort: string
    description: string
  }>
}

export interface ModelProviderOption {
  id: string
  name: string
  kind: 'codex' | 'claude' | 'gemini' | 'grok' | 'custom'
  configured: boolean
}

export interface SelectOption {
  value: string
  label: string
  description?: string
  badge?: string
  disabled?: boolean
}

export interface PluginView {
  id: string
  name: string
  displayName: string
  description: string
  developerName: string
  category: string
  installed: boolean
  enabled: boolean
  version: string
  marketplaceName: string
  marketplacePath: string
  sourceType: string
  logoUrl: string
}

export interface SkillView {
  name: string
  path: string
  description: string
  shortDescription: string
  displayName: string
  scope: string
  enabled: boolean
  error: string
}

export interface AppView {
  id: string
  name: string
  description: string
  enabled: boolean
  accessible: boolean
  logoUrl: string
  pluginNames: string[]
}

export interface MCPServerView {
  name: string
  title: string
  description: string
  authStatus: string
  toolCount: number
  resourceCount: number
  enabled: boolean
  statusLoaded: boolean
  statusMessage: string
  command?: string
  url?: string
  transport?: string
  args?: string[]
}

export interface HookView {
  name: string
  key: string
  event: string
  source: string
  enabled: boolean
  error: string
}

export interface ExperimentalFeatureView {
  name: string
  displayName: string
  description: string
  stage: string
  enabled: boolean
}

export interface CapabilityCatalog {
  plugins: PluginView[]
  skills: SkillView[]
  apps: AppView[]
  mcpServers: MCPServerView[]
  hooks: HookView[]
  features: ExperimentalFeatureView[]
}

export interface DiffLineView {
  kind: 'add' | 'delete' | 'context' | 'meta'
  content: string
  oldLine: number | null
  newLine: number | null
}

export interface DiffHunkView {
  header: string
  lines: DiffLineView[]
}

export interface DiffFileView {
  oldPath: string
  newPath: string
  displayPath: string
  additions: number
  deletions: number
  hunks: DiffHunkView[]
}

export interface AccountInfo {
  authenticated: boolean
  type: string
  email: string
  planType: string
  requiresOpenAIAuth: boolean
}

export interface TokenUsageBreakdown {
  inputTokens: number
  cachedInputTokens: number
  outputTokens: number
  reasoningOutputTokens: number
  totalTokens: number
}

export interface ThreadTokenUsage {
  total: TokenUsageBreakdown
  last: TokenUsageBreakdown
  modelContextWindow: number | null
}

export interface TurnMetrics {
  tokenUsage: TokenUsageBreakdown | null
  startedAt: number | null
  completedAt: number | null
  durationMs: number | null
}

export interface RateLimitWindow {
  usedPercent: number
  resetsAt: number | null
  windowDurationMins: number | null
}

export interface AccountRateLimits {
  limitName: string
  planType: string
  rateLimitReachedType: string
  primary: RateLimitWindow | null
  secondary: RateLimitWindow | null
  credits: {
    hasCredits: boolean
    unlimited: boolean
    balance: string
  } | null
}

export interface AccountUsageSummary {
  lifetimeTokens: number | null
  peakDailyTokens: number | null
  currentStreakDays: number | null
  longestStreakDays: number | null
  longestRunningTurnSec: number | null
}

export interface PendingServerRequest {
  requestKey: string
  method: string
  data: Record<string, unknown>
}

export interface ToastMessage {
  id: number
  tone: 'neutral' | 'success' | 'warning' | 'danger'
  title: string
  message: string
  action?: ToastAction
}

export type ToastAction =
  | { kind: 'reconnect'; label: string }
  | { kind: 'reload-project'; label: string; path: string }
  | { kind: 'retry-thread'; label: string; threadId: string; path?: string }
