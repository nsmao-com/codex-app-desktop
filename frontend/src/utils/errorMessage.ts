import { translate } from '../i18n'

export function rawErrorText(error: unknown): string {
  if (error instanceof Error && error.message) return error.message
  if (typeof error === 'string') return error
  return ''
}

export function friendlyErrorMessage(error: unknown, fallback = translate('notifications.unexpected')): string {
  const raw = rawErrorText(error).trim()
  if (!raw) return fallback
  const lower = raw.toLowerCase()

  if (/canceled|cancelled|context canceled|context cancelled/.test(lower)) {
    return translate('errors.canceled')
  }
  if (/401|unauthorized|invalid api key|incorrect api key|api key missing|authentication|not authenticated|unauthenticated/.test(lower)) {
    return translate('errors.invalidKey')
  }
  if (/403|forbidden|permission denied/.test(lower)) {
    return translate('errors.forbidden')
  }
  if (/429|rate limit|too many requests|resource_exhausted/.test(lower)) {
    return translate('errors.rateLimited')
  }
  if (/quota|insufficient.?quota|billing|credit/.test(lower)) {
    return translate('errors.quota')
  }
  if (/session was not found|thread.*not found|conversation.*not found/.test(lower)) {
    return translate('errors.sessionMissing')
  }
  if (/timeout|deadline exceeded|timed out|i\/o timeout/.test(lower)) {
    return translate('errors.timeout')
  }
  if (/cli executable was not found|not found in path|executable was not found|command not found|not installed/.test(lower)) {
    return translate('errors.cliMissing')
  }
  if (/empty response/.test(lower)) {
    return translate('errors.emptyResponse')
  }
  if (/network|connection refused|connection reset|eof|dial tcp|no such host|failed to fetch|wsarecv/.test(lower)) {
    return translate('errors.network')
  }

  const cleaned = raw
    .replace(/^Grok API HTTP \d+:\s*/i, '')
    .replace(/^HTTP \d+:\s*/i, '')
    .replace(/\{[\s\S]*\}$/, '')
    .trim()
  if (!cleaned) return fallback
  if (cleaned.length > 180) return `${cleaned.slice(0, 177)}…`
  return cleaned
}
