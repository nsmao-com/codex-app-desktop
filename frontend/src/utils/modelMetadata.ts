import type { AgentProviderModel as NativeProviderModel, AgentProviderRuntime as NativeProviderRuntime } from '../../bindings/nice_codex_desktop/models'

// Metadata is fetched by the frontend; keep its types outside generated bindings.
export interface ModelPricing {
  inputPerMillion: number
  outputPerMillion: number
  cacheReadPerMillion: number
  currency: string
  source: string
  updatedAt: string
}

export interface AgentProviderModel extends NativeProviderModel {
  contextSource?: string
  pricing?: ModelPricing
  metadataSource?: string
  metadataUpdatedAt?: string
}

export interface AgentProviderRuntime extends Omit<NativeProviderRuntime, 'models'> {
  models: AgentProviderModel[] | null
}

export type ModelMetadataSource = 'openrouter' | 'models.dev' | 'litellm'

export interface RemoteModelMetadata {
  model: string
  contextWindow?: number
  pricing?: ModelPricing
  source: ModelMetadataSource
  updatedAt: string
}

interface MetadataCache {
  savedAt: string
  entries: RemoteModelMetadata[]
}

interface SyncResult {
  providers: AgentProviderRuntime[]
  cache: MetadataCache
}

const CACHE_KEY = 'nice-codex.model-metadata.v1'
const CACHE_TTL_MS = 12 * 60 * 60 * 1000
const REQUEST_TIMEOUT_MS = 8_000
const SOURCE_URLS: Record<ModelMetadataSource, string> = {
  openrouter: 'https://openrouter.ai/api/v1/models',
  'models.dev': 'https://models.dev/api.json',
  litellm: 'https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json',
}

function finiteNumber(value: unknown): number | undefined {
  const number = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(number) && number > 0 ? number : undefined
}

function normalizeModel(value: unknown): string {
  return String(value || '').trim().toLowerCase().replace(/\s+/g, '').replace(/\[(?:1m|200k|100k)\]$/i, '')
}

function modelKeys(model: string): string[] {
  const normalized = normalizeModel(model)
  if (!normalized) return []
  const keys = new Set<string>([normalized])
  const slash = normalized.lastIndexOf('/')
  if (slash >= 0) keys.add(normalized.slice(slash + 1))
  return [...keys]
}

function sourcePriority(source: ModelMetadataSource): number {
  return source === 'openrouter' ? 0 : source === 'models.dev' ? 1 : 2
}

function readCache(): MetadataCache | null {
  try {
    const parsed = JSON.parse(localStorage.getItem(CACHE_KEY) || '') as Partial<MetadataCache>
    if (!Array.isArray(parsed.entries)) return null
    return { savedAt: String(parsed.savedAt || ''), entries: parsed.entries.filter((entry): entry is RemoteModelMetadata => Boolean(entry && typeof entry === 'object' && typeof entry.model === 'string' && typeof entry.source === 'string')) }
  } catch {
    return null
  }
}

function writeCache(cache: MetadataCache): void {
  try { localStorage.setItem(CACHE_KEY, JSON.stringify(cache)) } catch { /* storage is optional */ }
}

function metadataIndex(entries: RemoteModelMetadata[]): Map<string, RemoteModelMetadata> {
  const index = new Map<string, RemoteModelMetadata>()
  for (const entry of entries) for (const key of modelKeys(entry.model)) {
    const previous = index.get(key)
    if (!previous || sourcePriority(entry.source) < sourcePriority(previous.source) || (!previous.contextWindow && Boolean(entry.contextWindow))) index.set(key, entry)
  }
  return index
}

function providerModelKeys(provider: AgentProviderRuntime, model: AgentProviderModel): string[] {
  const values = [model.model, model.providerId ? `${model.providerId}/${model.model}` : '']
  const kind = normalizeModel(provider.kind)
  if (kind === 'codex') values.push(`openai/${model.model}`)
  if (kind === 'claude') {
    if (/sonnet/i.test(model.model)) values.push('claude-sonnet')
    if (/opus/i.test(model.model)) values.push('claude-opus')
    if (/haiku/i.test(model.model)) values.push('claude-haiku')
  }
  if (kind === 'gemini') values.push(`google/${model.model}`)
  if (kind === 'grok') values.push(`x-ai/${model.model}`)
  return values.flatMap(modelKeys)
}

function mergeProviders(providers: AgentProviderRuntime[], entries: RemoteModelMetadata[]): AgentProviderRuntime[] {
  const index = metadataIndex(entries)
  return providers.map((provider) => ({ ...provider, models: (provider.models || []).map((model) => {
    const keys = providerModelKeys(provider, model)
    const remote = keys.map((key) => index.get(key)).find(Boolean)
      || keys.map((key) => [...index.entries()].find(([candidate]) => candidate.startsWith(key) || key.startsWith(candidate))?.[1]).find(Boolean)
    const localContext = finiteNumber(model.contextWindow)
    if (!remote) return localContext && !model.contextSource ? { ...model, contextSource: 'local-config' } : model
    return {
      ...model,
      contextWindow: localContext || finiteNumber(remote.contextWindow) || 0,
      contextSource: localContext ? (model.contextSource || 'local-config') : (remote.contextWindow ? remote.source : model.contextSource),
      pricing: remote.pricing || model.pricing,
      metadataSource: remote.source,
      metadataUpdatedAt: remote.updatedAt,
    }
  }) }))
}

async function fetchJSON(url: string): Promise<unknown> {
  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS)
  try {
    const response = await fetch(url, { signal: controller.signal, headers: { Accept: 'application/json' } })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    return await response.json()
  } finally { window.clearTimeout(timer) }
}

function parseOpenRouter(payload: unknown, updatedAt: string): RemoteModelMetadata[] {
  const data = (payload as { data?: unknown[] })?.data
  if (!Array.isArray(data)) return []
  return data.flatMap((item) => {
    const row = item as Record<string, unknown>
    const pricing = (row.pricing || {}) as Record<string, unknown>
    const contextWindow = finiteNumber(row.context_length) || finiteNumber((row.top_provider as Record<string, unknown> | undefined)?.context_length)
    const input = finiteNumber(pricing.prompt); const output = finiteNumber(pricing.completion); const cache = finiteNumber(pricing.input_cache_read) || finiteNumber(pricing.cache_read)
    if (!row.id || (!contextWindow && !input && !output && !cache)) return []
    return [{ model: String(row.id), contextWindow, source: 'openrouter' as const, updatedAt, pricing: input || output || cache ? { inputPerMillion: (input || 0) * 1_000_000, outputPerMillion: (output || 0) * 1_000_000, cacheReadPerMillion: (cache || 0) * 1_000_000, currency: 'USD', source: 'openrouter', updatedAt } : undefined }]
  })
}

function parseModelsDev(payload: unknown, updatedAt: string): RemoteModelMetadata[] {
  if (!payload || typeof payload !== 'object') return []
  const result: RemoteModelMetadata[] = []
  for (const [providerID, providerValue] of Object.entries(payload as Record<string, unknown>)) {
    const models = (providerValue as { models?: Record<string, unknown> })?.models
    if (!models || typeof models !== 'object') continue
    for (const [modelID, value] of Object.entries(models)) {
      const row = value as Record<string, unknown>; const limit = (row.limit || {}) as Record<string, unknown>; const cost = (row.cost || {}) as Record<string, unknown>
      const contextWindow = finiteNumber(limit.context); const input = finiteNumber(cost.input); const output = finiteNumber(cost.output); const cache = finiteNumber(cost.cache_read) || finiteNumber(cost.cacheRead)
      if (!contextWindow && !input && !output && !cache) continue
      result.push({ model: `${providerID}/${String(row.id || modelID)}`, contextWindow, source: 'models.dev', updatedAt, pricing: input || output || cache ? { inputPerMillion: input || 0, outputPerMillion: output || 0, cacheReadPerMillion: cache || 0, currency: 'USD', source: 'models.dev', updatedAt } : undefined })
    }
  }
  return result
}

function parseLiteLLM(payload: unknown, updatedAt: string): RemoteModelMetadata[] {
  if (!payload || typeof payload !== 'object') return []
  return Object.entries(payload as Record<string, unknown>).flatMap(([model, value]) => {
    if (!value || typeof value !== 'object') return []
    const row = value as Record<string, unknown>; const contextWindow = finiteNumber(row.max_input_tokens) || finiteNumber(row.max_tokens); const input = finiteNumber(row.input_cost_per_token); const output = finiteNumber(row.output_cost_per_token); const cache = finiteNumber(row.cache_read_input_token_cost)
    if (!contextWindow && !input && !output && !cache) return []
    return [{ model, contextWindow, source: 'litellm' as const, updatedAt, pricing: input || output || cache ? { inputPerMillion: (input || 0) * 1_000_000, outputPerMillion: (output || 0) * 1_000_000, cacheReadPerMillion: (cache || 0) * 1_000_000, currency: 'USD', source: 'litellm', updatedAt } : undefined }]
  })
}

function parseSource(source: ModelMetadataSource, payload: unknown, updatedAt: string): RemoteModelMetadata[] {
  return source === 'openrouter' ? parseOpenRouter(payload, updatedAt) : source === 'models.dev' ? parseModelsDev(payload, updatedAt) : parseLiteLLM(payload, updatedAt)
}

export function applyCachedModelMetadata(providers: AgentProviderRuntime[]): AgentProviderRuntime[] { return mergeProviders(providers, readCache()?.entries || []) }

let activeSync: Promise<SyncResult | null> | null = null
export async function syncModelMetadata(providers: AgentProviderRuntime[], force = false): Promise<SyncResult | null> {
  const cached = readCache(); const age = cached?.savedAt ? Date.now() - Date.parse(cached.savedAt) : Number.POSITIVE_INFINITY
  if (!force && cached && Number.isFinite(age) && age < CACHE_TTL_MS) return { providers: applyCachedModelMetadata(providers), cache: cached }
  if (activeSync) return activeSync
  activeSync = (async () => {
    const updatedAt = new Date().toISOString()
    const responses = await Promise.allSettled((Object.entries(SOURCE_URLS) as Array<[ModelMetadataSource, string]>).map(async ([source, url]) => ({ source, payload: await fetchJSON(url) })))
    const entries = responses.flatMap((response) => response.status === 'fulfilled' ? parseSource(response.value.source, response.value.payload, updatedAt) : [])
    if (!entries.length) return null
    const nextCache = { savedAt: updatedAt, entries }; writeCache(nextCache)
    return { providers: mergeProviders(providers, entries), cache: nextCache }
  })().finally(() => { activeSync = null })
  return activeSync
}

export function modelMetadataCacheInfo(): { savedAt: string; modelCount: number } {
  const cache = readCache(); return { savedAt: cache?.savedAt || '', modelCount: cache?.entries.length || 0 }
}
