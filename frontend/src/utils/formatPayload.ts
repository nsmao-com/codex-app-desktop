import { escapeHTML, highlightJSON } from './highlight'

const MAX_PRETTY_CHARS = 200_000

export function formatToolPayload(source: string): { text: string; isJson: boolean } {
  const raw = source?.trim() ?? ''
  if (!raw) return { text: '', isJson: false }
  if (raw.length > MAX_PRETTY_CHARS) return { text: source, isJson: false }

  const candidate = stripJsonFences(raw)
  try {
    const parsed = JSON.parse(candidate) as unknown
    return { text: JSON.stringify(parsed, null, 2), isJson: true }
  } catch {
    return { text: source, isJson: false }
  }
}

export function renderToolPayloadHTML(source: string): string {
  const { text, isJson } = formatToolPayload(source)
  if (!text) return ''
  if (!isJson) return escapeHTML(text)
  return highlightJSON(text)
}

function stripJsonFences(value: string): string {
  const fenced = value.match(/^```(?:json|JSON)?\s*\n?([\s\S]*?)\n?```\s*$/)
  return fenced?.[1]?.trim() ?? value
}
