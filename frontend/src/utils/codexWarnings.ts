import { asRecord } from './protocol'

export interface CodexWarning {
  id: string
  method: string
  summary: string
  details: string
  path: string
  location: string
  threadId: string
  workspace: string
  kind: 'config' | 'compatibility' | 'runtime' | 'security'
  samplePaths: string[]
  extraCount: number
  failedScan: boolean
}

const storageKey = 'nice-codex.warnings.v1'
const methods = new Set(['warning', 'configWarning', 'deprecationNotice', 'guardianWarning', 'windows/worldWritableWarning'])
const maxWarnings = 32

function text(value: unknown, limit = 8000): string {
  if (typeof value !== 'string') return ''
  // Diagnostics can quote config values. Do not persist common credentials.
  return value.trim().slice(0, limit)
    .replace(/\b(Bearer\s+)[^\s"']+/gi, '$1[redacted]')
    .replace(/\b((?:[a-z0-9]+[_-])*(?:api[_-]?key|token|authorization|password|secret)\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)/gi, '$1[redacted]')
}

export function normalizeCodexWarning(method: string, data: unknown, workspace = ''): CodexWarning | null {
  if (!methods.has(method)) return null
  const payload = asRecord(data)
  workspace = text(workspace, 2000)
  const summary = text(payload.summary, 2000) || text(payload.message, 2000)
  const details = text(payload.details) || text(payload.detail)
  const path = text(payload.path, 2000)
  const start = asRecord(asRecord(payload.range).start)
  // Codex TextPosition lines/columns are one-based; do not add an offset.
  const line = Number.isInteger(start.line) && Number(start.line) > 0 ? Number(start.line) : 0
  const column = Number.isInteger(start.column) && Number(start.column) > 0 ? Number(start.column) : 0
  const location = line ? `${line}${column ? `:${column}` : ''}` : ''
  const threadId = text(payload.threadId, 200)
  const samplePaths = Array.isArray(payload.samplePaths) ? payload.samplePaths.slice(0, 8).map((path) => text(path, 2000)).filter(Boolean) : []
  const extraCount = (typeof payload.extraCount === 'number' && Number.isFinite(payload.extraCount) ? Math.max(0, Math.floor(payload.extraCount)) : 0)
    + (Array.isArray(payload.samplePaths) ? Math.max(0, payload.samplePaths.length - 8) : 0)
  const failedScan = payload.failedScan === true
  const kind = method === 'configWarning' ? 'config' : method === 'deprecationNotice' ? 'compatibility'
    : method === 'guardianWarning' || method === 'windows/worldWritableWarning' ? 'security' : 'runtime'
  return {
    id: JSON.stringify([method, summary, details, path, location, threadId, kind === 'runtime' ? workspace : '', samplePaths, extraCount, failedScan]),
    method, summary, details, path, location, threadId, workspace, kind, samplePaths, extraCount, failedScan,
  }
}

export function addCodexWarning(warnings: CodexWarning[], warning: CodexWarning): CodexWarning[] {
  if (warnings.some((existing) => existing.id === warning.id)) return warnings
  return [...warnings.slice(-(maxWarnings - 1)), warning]
}

export function loadCodexWarnings(): CodexWarning[] {
  try {
    const raw = localStorage.getItem(storageKey)
    if (!raw || raw.length > 2_500_000) return []
    const rows: unknown = JSON.parse(raw)
    if (!Array.isArray(rows)) return []
    return rows.slice(-maxWarnings).reduce<CodexWarning[]>((result, row) => {
      const item = asRecord(row)
      const [line, column] = text(item.location, 30).split(':').map(Number)
      const warning = normalizeCodexWarning(text(item.method, 80), {
        ...item, range: { start: { line, column } },
      }, text(item.workspace, 2000))
      return warning ? addCodexWarning(result, warning) : result
    }, [])
  } catch {
    return []
  }
}

export function saveCodexWarnings(warnings: CodexWarning[]): void {
  try {
    if (warnings.length) localStorage.setItem(storageKey, JSON.stringify(warnings.slice(-maxWarnings)))
    else localStorage.removeItem(storageKey)
  } catch {
    // The persistent indicator still works in memory if storage is unavailable.
  }
}
