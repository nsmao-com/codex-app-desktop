import type { ModelOption, ModelProviderOption } from '@/types/codex'

/** Codex-specific fallbacks used only when the active runtime is Codex. */
export const DEFAULT_CODEX_REASONING = [
  { effort: 'low', description: 'Fast responses with lighter reasoning' },
  { effort: 'medium', description: 'Balanced speed and depth' },
  { effort: 'high', description: 'Deeper reasoning for complex work' },
  { effort: 'xhigh', description: 'Extra-high reasoning depth' },
  { effort: 'max', description: 'Maximum reasoning for hard problems' },
  { effort: 'ultra', description: 'Ultra reasoning depth' },
] as const

export const DEFAULT_CODEX_MODEL = 'gpt-6-astra'

export const CUSTOM_MODEL_LIMIT = 24

/** Match the persisted list: keep the first spelling and the user's order. */
export function normalizeCustomModels(items: string[]): string[] {
  const seen = new Set<string>()
  return items.map((item) => item.trim()).filter((item) => {
    const key = item.toLowerCase()
    if (!item || new TextEncoder().encode(item).length > 160 || seen.has(key)) return false
    seen.add(key)
    return true
  }).slice(0, CUSTOM_MODEL_LIMIT)
}

/** Only known Claude families get an automatic suffix; gateway IDs stay intact. */
export function claudeLongContextModel(model: string): string | null {
  const base = model.trim().replace(/\[1m\]$/i, '')
  if (/^(sonnet|opus)$/i.test(base) || /^claude-(sonnet-4-[56]|opus-4-6)(?:-\d{8})?$/i.test(base)) return `${base}[1m]`
  if (/^(fable|claude-(?:fable-5|sonnet-5|opus-(?:4-[78]|5))(?:[-.].*)?)$/i.test(base)) return base
  return null
}

/** agy models: Gemini 3.1 Pro exposes low/high, not the Flash medium variant. */
export function antigravityModelEfforts<T extends { effort: string }>(model: string, options: T[]): T[] {
  const id = model.trim().toLowerCase()
  const fixed = id.match(/-(low|medium|high)$/)?.[1]
  if (fixed) return options.filter((option) => option.effort === fixed)
  if (id === 'gemini-3.1-pro') return options.filter((option) => option.effort !== 'medium')
  return options
}

export function normalizeAntigravityModelEffort(model: string, effort: string): string {
  const options = antigravityModelEfforts(model, ['high', 'medium', 'low'].map((effort) => ({ effort })))
  return options.some((option) => option.effort === effort) ? effort : options[0]!.effort
}

/** Soft fallback when model/list is unavailable. */
export const FALLBACK_CODEX_MODELS = [
  DEFAULT_CODEX_MODEL,
  'gpt-6-sol',
  'gpt-6-luna',
  'gpt-5.6-sol',
  'gpt-5.6-terra',
  'gpt-5.6-luna',
  'gpt-5.5',
  'gpt-5.4',
  'gpt-5.4-mini',
  'gpt-5.3-codex-spark',
  'gpt-5.3-codex',
  'gpt-5.2',
] as const

export const FALLBACK_GROK_MODELS = [
  'grok-4.6',
  'grok-4.5',
  'grok-4',
  'grok-3-mini',
  'grok-3',
] as const

export const DEFAULT_GROK_REASONING = [
  { effort: 'low', description: 'Faster replies with lighter reasoning' },
  { effort: 'medium', description: 'Balanced speed and depth' },
  { effort: 'high', description: 'Highest quality for complex implementation' },
  { effort: 'xhigh', description: 'Maximum reasoning depth on grok-4.6+' },
] as const

/** Strip proxy nicknames such as "gpt-5.6-sol · claude-opus-4-8". */
export function cleanModelDisplayName(model: string, displayName = ''): string {
  const raw = (displayName || model).trim()
  if (!raw) return model
  const parts = raw.split(/\s*[·•|]\s*/).map((part) => part.trim()).filter(Boolean)
  let cleaned = raw
  if (parts.length >= 2) {
    const first = parts[0] || model
    const last = parts[parts.length - 1] || model
    const firstOpenAI = /^(gpt|o\d|codex|sol)/i.test(first)
  const lastOther = /(claude|gemini|antigravity|grok|opencode|sonnet|opus|haiku|fable)/i.test(last)
    cleaned = firstOpenAI && lastOther ? first : first
  }
  return formatModelLabel(cleaned)
}

/**
 * Map config ids (gpt-5.4-mini) to closed-select friendly labels (GPT-5.4 Mini).
 * Value stays lowercase; only the visible label is prettified.
 */
export function formatModelLabel(id: string): string {
  const raw = id.trim()
  if (!raw) return raw
  const officialClaude = raw.match(/^claude-(opus|sonnet|fable|haiku)-(\d+)-(\d+)(?:-\d{8})?$/i)
  if (officialClaude) {
    const family = officialClaude[1]!
    return `Claude ${family[0]!.toUpperCase()}${family.slice(1).toLowerCase()} ${officialClaude[2]}.${officialClaude[3]}`
  }
  // Already human-authored (contains spaces + capitals) — keep.
  if (/\s/.test(raw) && /[A-Z]/.test(raw)) return raw

  let label = raw
  label = label.replace(/^gpt-/i, 'GPT-')
  label = label.replace(/^codex-/i, 'Codex-')
  label = label.replace(/^o([0-9])/i, 'O$1')
  label = label.replace(/-(mini|nano|pro|ultra|preview|latest|astra|sol|terra|luna|high|low|medium)\b/gi, (_, word: string) =>
    ` ${word.charAt(0).toUpperCase()}${word.slice(1).toLowerCase()}`,
  )
  // Capitalize leftover all-lowercase trailing tokens: foo-bar → Foo Bar segments after first brand
  if (label === label.toLowerCase() && /[a-z]/.test(label)) {
    label = label
      .split(/[-_]/)
      .filter(Boolean)
      .map((part) => (/^\d/.test(part) ? part : part.charAt(0).toUpperCase() + part.slice(1)))
      .join('-')
  }
  return label
}

function looksLikeOpenAI(text: string): boolean {
  return /^(gpt-|o[1-9]|codex)/i.test(text.trim())
    || /\b(gpt-|o[1-9][\w.-]*|codex|openai)\b/.test(text)
}

function looksLikeOtherRuntime(text: string): boolean {
  return /\b(claude|anthropic|gemini|antigravity|grok|opencode)\b/.test(text)
    || /\b(sonnet|opus|haiku|fable)(-\d|\b)/.test(text)
    || /^(sonnet|opus|haiku|fable)$/.test(text)
}

/** Keep Codex IDs and provider aliases; exclude models from other runtimes. */
export function selectCodexCatalog(codexModels: ModelOption[]): ModelOption[] {
  return codexModels.filter((item) => looksLikeOpenAI(item.model.toLowerCase())
    || !looksLikeOtherRuntime(`${item.model} ${item.displayName}`.toLowerCase()))
}

/** Merge live, custom and built-in choices without replacing live capabilities. */
export function mergeCodexCatalog(
  codexModels: ModelOption[],
  customModels: string[] = [],
): ModelOption[] {
  const custom = normalizeCustomModels(customModels)
  const customIDs = new Set(custom.map((id) => id.toLowerCase()))
  const seen = new Set<string>()
  const options: ModelOption[] = []
  for (const item of codexModels) {
    const id = item.model.trim()
    const key = id.toLowerCase()
    if (!id || seen.has(key)) continue
    if (item.isCustom && !customIDs.has(key)) continue
    if (!customIDs.has(key) && !selectCodexCatalog([item]).length) continue
    seen.add(key)
    options.push({ ...item, model: id, displayName: customIDs.has(key) ? id : cleanModelDisplayName(id, item.displayName) })
  }
  for (const id of custom) {
    if (seen.has(id.toLowerCase())) continue
    seen.add(id.toLowerCase())
    options.push({ ...stubCodexModel(id), displayName: id, isCustom: true })
  }
  if (!options.length) {
    for (const id of FALLBACK_CODEX_MODELS) {
      options.push(stubCodexModel(id))
    }
  }
  // Keep the built-in Astra fallback available for older CLIs, but preserve the
  // live catalog's default when the current CLI advertises one.
  if (!options.some((item) => item.model.toLocaleLowerCase() === DEFAULT_CODEX_MODEL)) {
    options.unshift(stubCodexModel(DEFAULT_CODEX_MODEL))
  }
  const liveDefault = options.find((item) => item.isDefault)?.model
  const fallbackDefault = liveDefault || DEFAULT_CODEX_MODEL
  for (const option of options) {
    option.isDefault = option.model.toLocaleLowerCase() === fallbackDefault.toLocaleLowerCase()
  }
  return options
}

function stubCodexModel(id: string): ModelOption {
  const normalized = id.trim().toLowerCase()
  const supportedReasoning = DEFAULT_CODEX_REASONING.filter((option) => {
    // GPT-6 Luna and the current GPT-5.6 Luna catalog do not expose ultra.
    if (option.effort === 'ultra' && normalized.endsWith('-luna')) return false
    // The public GPT-5.5 catalog currently exposes low through xhigh.
    if ((normalized === 'gpt-5.5' || normalized === 'gpt-5.4') && ['max', 'ultra'].includes(option.effort)) return false
    return true
  })
  return {
    id,
    model: id,
    displayName: cleanModelDisplayName(id, id),
    description: 'Codex model',
    isDefault: false,
    defaultReasoningEffort: /astra$/i.test(normalized) || /gpt-5\.6-sol$/i.test(normalized) ? 'low' : 'medium',
    defaultServiceTier: '',
    serviceTiers: [],
    supportsPersonality: false,
    supportedReasoningEfforts: supportedReasoning.map((option) => ({
      effort: option.effort,
      description: option.description,
    })),
  }
}

export function modelsForRuntime(
  codexModels: ModelOption[],
  customModels: string[] = [],
  preferredModel = '',
): Array<{ model: string; displayName: string; isDefault: boolean }> {
  const catalog = mergeCodexCatalog(codexModels, customModels)
  // A saved/session model can be absent while the native catalog is refreshing.
  if (preferredModel.trim() && !catalog.some((item) => item.model === preferredModel.trim())) {
    const index = catalog.findIndex((item) => item.model.toLowerCase() === preferredModel.trim().toLowerCase())
    if (index >= 0) catalog[index] = { ...catalog[index]!, model: preferredModel.trim() }
    else catalog.push({ ...stubCodexModel(preferredModel.trim()), displayName: preferredModel.trim() })
  }
  return catalog.map((item) => ({
    model: item.model,
    displayName: item.displayName,
    isDefault: item.isDefault,
  }))
}

export function modelsForGrokRuntime(
  providerModels: Array<{ model: string; displayName?: string; isDefault?: boolean }> = [],
  preferredModel = '',
  customModels: string[] = [],
): Array<{ model: string; displayName: string; isDefault: boolean }> {
  const options: Array<{ model: string; displayName: string; isDefault: boolean }> = []
  const push = (id: string, displayName = '', isDefault = false) => {
    const model = id.trim()
    if (!model) return
    if (options.some((item) => item.model.toLocaleLowerCase() === model.toLocaleLowerCase())) return
    options.push({
      model,
      displayName: displayName || formatModelLabel(model),
      isDefault,
    })
  }
  for (const item of providerModels.length ? providerModels : FALLBACK_GROK_MODELS.map((model, index) => ({ model, isDefault: index === 0, displayName: formatModelLabel(model) }))) {
    push(item.model, item.displayName || item.model, item.isDefault === true)
  }
  for (const custom of customModels) {
    push(custom, custom, false)
  }
  if (preferredModel.trim()) push(preferredModel.trim(), preferredModel.trim(), options.length === 0)
  if (!options.length) {
    for (const [index, id] of FALLBACK_GROK_MODELS.entries()) {
      push(id, formatModelLabel(id), index === 0)
    }
  }
  if (preferredModel.trim()) {
    const selected = options.find((item) => item.model.toLowerCase() === preferredModel.trim().toLowerCase())
    if (selected) selected.model = preferredModel.trim()
  }
  if (!options.some((item) => item.isDefault) && options[0]) {
    options[0].isDefault = true
  }
  return options
}

const FALLBACK_CLAUDE_MODELS = [
  { model: 'default', displayName: 'Claude Code Default', description: 'Recommended model for the current account and provider', isDefault: true },
  { model: 'claude-opus-5-5', displayName: 'Claude Opus 5.5', description: 'claude-opus-5-5', isDefault: false },
  { model: 'claude-sonnet-5-5', displayName: 'Claude Sonnet 5.5', description: 'claude-sonnet-5-5', isDefault: false },
  { model: 'claude-fable-5-1', displayName: 'Claude Fable 5.1', description: 'claude-fable-5-1', isDefault: false },
  { model: 'claude-haiku-4-5-20251001', displayName: 'Claude Haiku 4.5', description: 'claude-haiku-4-5-20251001', isDefault: false },
  { model: 'sonnet', displayName: 'Claude Sonnet', description: 'CLI alias `sonnet`; resolved by Claude Code', isDefault: false },
  { model: 'opus', displayName: 'Claude Opus', description: 'alias `opus` → latest Opus', isDefault: false },
  { model: 'haiku', displayName: 'Claude Haiku', description: 'alias `haiku` → latest Haiku', isDefault: false },
  { model: 'fable', displayName: 'Claude Fable', description: 'alias `fable` → latest Fable', isDefault: false },
] as const

/** Merge Claude Code catalog aliases with user-saved custom --model ids. */
export function modelsForClaudeRuntime(
  providerModels: Array<{ model: string; displayName?: string; description?: string; isDefault?: boolean }> = [],
  preferredModel = '',
  customModels: string[] = [],
): Array<{ model: string; displayName: string; description: string; isDefault: boolean }> {
  const options: Array<{ model: string; displayName: string; description: string; isDefault: boolean }> = []
  const push = (id: string, displayName = '', description = '', isDefault = false) => {
    const model = id.trim()
    if (!model) return
    if (options.some((item) => item.model.toLocaleLowerCase() === model.toLocaleLowerCase())) return
    options.push({
      model,
      displayName: displayName || formatModelLabel(model),
      description: description || model,
      isDefault,
    })
  }
  for (const item of providerModels.length ? providerModels : FALLBACK_CLAUDE_MODELS) {
    push(
      item.model,
      item.displayName || item.model,
      item.description || (item.displayName ? `alias \`${item.model}\`` : item.model),
      item.isDefault === true,
    )
  }
  for (const custom of customModels) {
    push(custom, custom, custom, false)
  }
  if (preferredModel.trim()) {
    push(preferredModel.trim(), formatModelLabel(preferredModel.trim()), preferredModel.trim(), options.length === 0)
  }
  if (!options.length) {
    for (const item of FALLBACK_CLAUDE_MODELS) {
      push(item.model, item.displayName, item.description, item.isDefault)
    }
  }
  if (preferredModel.trim()) {
    const selected = options.find((item) => item.model.toLowerCase() === preferredModel.trim().toLowerCase())
    if (selected) selected.model = preferredModel.trim()
  }
  if (!options.some((item) => item.isDefault) && options[0]) {
    options[0].isDefault = true
  }
  return options
}

export function buildRuntimeProviders(): ModelProviderOption[] {
  return [{ id: '', name: 'Codex', kind: 'codex', configured: true }]
}
