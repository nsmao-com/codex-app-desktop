import type { ModelOption, ModelProviderOption } from '@/types/codex'

/** NiceCodex is Codex-only — a full replacement for Codex Desktop. */
export const DEFAULT_CODEX_REASONING = [
  { effort: 'low', description: 'Fast responses with lighter reasoning' },
  { effort: 'medium', description: 'Balanced speed and depth' },
  { effort: 'high', description: 'Deeper reasoning for complex work' },
  { effort: 'xhigh', description: 'Extra-high reasoning depth' },
  { effort: 'max', description: 'Maximum reasoning for hard problems' },
  { effort: 'ultra', description: 'Ultra reasoning depth' },
] as const

/** Soft fallback when model/list is unavailable. */
export const FALLBACK_CODEX_MODELS = [
  'gpt-5.6-sol',
  'gpt-5.6-terra',
  'gpt-5.6-luna',
  'gpt-5.5',
  'gpt-5.4',
  'gpt-5.4-mini',
  'gpt-5.2',
] as const

export const FALLBACK_GROK_MODELS = [
  'grok-4.5',
  'grok-4',
  'grok-3-mini',
  'grok-3',
] as const

export const DEFAULT_GROK_REASONING = [
  { effort: 'low', description: 'Faster replies with lighter reasoning' },
  { effort: 'medium', description: 'Balanced speed and depth' },
  { effort: 'high', description: 'Highest quality for complex implementation' },
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
    const lastOther = /(claude|gemini|grok|sonnet|opus|haiku|fable)/i.test(last)
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
  // Already human-authored (contains spaces + capitals) — keep.
  if (/\s/.test(raw) && /[A-Z]/.test(raw)) return raw

  let label = raw
  label = label.replace(/^gpt-/i, 'GPT-')
  label = label.replace(/^codex-/i, 'Codex-')
  label = label.replace(/^o([0-9])/i, 'O$1')
  label = label.replace(/-(mini|nano|pro|ultra|preview|latest|sol|terra|luna|high|low|medium)\b/gi, (_, word: string) =>
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
  return /\b(claude|anthropic|gemini|grok)\b/.test(text)
    || /\b(sonnet|opus|haiku|fable)(-\d|\b)/.test(text)
    || /^(sonnet|opus|haiku|fable)$/.test(text)
}

/** Prefer Codex / OpenAI-shaped IDs; never return an empty picker. */
export function selectCodexCatalog(codexModels: ModelOption[]): ModelOption[] {
  const openaiShaped = codexModels.filter((item) => looksLikeOpenAI(item.model.toLowerCase()))
  if (openaiShaped.length) return openaiShaped
  const withoutOther = codexModels.filter((item) => !looksLikeOtherRuntime(`${item.model} ${item.displayName}`.toLowerCase()))
  if (withoutOther.length) return withoutOther
  return codexModels
}

export function modelsForRuntime(
  codexModels: ModelOption[],
  customModels: string[] = [],
): Array<{ model: string; displayName: string; isDefault: boolean }> {
  const options = selectCodexCatalog(codexModels).map((item) => ({
    model: item.model,
    displayName: cleanModelDisplayName(item.model, item.displayName),
    isDefault: item.isDefault === true,
  }))
  for (const custom of customModels) {
    const id = custom.trim()
    if (!id) continue
    if (looksLikeOtherRuntime(id.toLowerCase()) && !looksLikeOpenAI(id.toLowerCase())) continue
    if (options.some((item) => item.model.toLocaleLowerCase() === id.toLocaleLowerCase())) continue
    options.push({ model: id, displayName: cleanModelDisplayName(id, id), isDefault: false })
  }
  if (!options.length) {
    for (const [index, id] of FALLBACK_CODEX_MODELS.entries()) {
      options.push({ model: id, displayName: formatModelLabel(id), isDefault: index === 0 })
    }
  }
  return options
}

export function modelsForGrokRuntime(
  providerModels: Array<{ model: string; displayName?: string; isDefault?: boolean }> = [],
  preferredModel = '',
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
  for (const item of providerModels) {
    push(item.model, item.displayName || item.model, item.isDefault === true)
  }
  if (preferredModel.trim()) push(preferredModel.trim(), preferredModel.trim(), options.length === 0)
  if (!options.length) {
    for (const [index, id] of FALLBACK_GROK_MODELS.entries()) {
      push(id, formatModelLabel(id), index === 0)
    }
  }
  if (preferredModel.trim()) {
    for (const option of options) {
      option.isDefault = option.model.toLocaleLowerCase() === preferredModel.trim().toLocaleLowerCase()
    }
    if (!options.some((item) => item.isDefault) && options[0]) options[0].isDefault = true
  } else if (!options.some((item) => item.isDefault) && options[0]) {
    options[0].isDefault = true
  }
  return options
}

export function buildRuntimeProviders(): ModelProviderOption[] {
  return [{ id: '', name: 'Codex', kind: 'codex', configured: true }]
}
