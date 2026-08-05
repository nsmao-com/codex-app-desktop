import { asArray, asRecord, asString } from './protocol'
import { translate } from '@/i18n'

export type ImportedMCPServer = {
  name: string
  enabled: boolean
  command: string
  args: string[]
  url: string
  transport: string
  env: Record<string, string>
}

export const MCP_IMPORT_MAX_LENGTH = 2 * 1024 * 1024

/** Parse Cursor / Claude / Codex style MCP JSON into upsert payloads. */
export function parseMCPImportJSON(raw: string): ImportedMCPServer[] {
  const text = raw.trim()
  if (!text) throw new Error(translate('capabilities.mcpImportJsonEmpty'))
  if (text.length > MCP_IMPORT_MAX_LENGTH) {
    throw new Error(translate('capabilities.mcpImportFileTooLarge'))
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error(translate('capabilities.mcpImportJsonInvalid'))
  }

  const root = asRecord(parsed)
  const source = Array.isArray(parsed)
    ? parsed
    : root.mcpServers
      ?? root.mcp_servers
      ?? root.servers
      ?? (looksLikeServerMap(root) ? parsed : null)
  const serverMap = asRecord(source)
  let entries: Array<[string, unknown]> = Object.entries(serverMap)
  if (!entries.length && Array.isArray(source)) {
    entries = source.map((server, index) => {
      const record = asRecord(server)
      return [asString(record.name, `server-${index + 1}`), server]
    })
  }
  if (!entries.length && root.name) entries = [[asString(root.name), parsed]]

  if (!entries.length) {
    throw new Error(translate('capabilities.mcpImportNoServers'))
  }
  if (entries.length > 100) throw new Error(translate('capabilities.mcpImportTooMany'))

  const result: ImportedMCPServer[] = []
  const names = new Set<string>()
  for (const [name, value] of entries) {
    const server = normalizeImportedServer(name, value)
    const nameKey = server.name.toLocaleLowerCase('en-US')
    if (names.has(nameKey)) {
      throw new Error(translate('capabilities.mcpImportDuplicate', { name: server.name }))
    }
    names.add(nameKey)
    result.push(server)
  }
  return result
}

function looksLikeServerMap(root: Record<string, unknown>): boolean {
  const keys = Object.keys(root)
  if (!keys.length) return false
  return keys.every((key) => {
    const entry = asRecord(root[key])
    return Boolean(asString(entry.command) || asString(entry.url) || asString(entry.serverUrl))
  })
}

function normalizeImportedServer(name: string, value: unknown): ImportedMCPServer {
  const server = asRecord(value)
  const trimmedName = asString(server.name, name).trim()
  if (!trimmedName || trimmedName.length > 120 || /[\r\n]/.test(trimmedName)) {
    throw new Error(translate('capabilities.mcpImportInvalidName', { name: name || '?' }))
  }

  let command = asString(server.command).trim()
  const url = asString(server.url, asString(server.serverUrl, asString(server.server_url))).trim()
  let transport = asString(server.type, asString(server.transport, url ? 'http' : 'stdio')).trim()
  const args = asArray(server.args).map((arg) => asString(arg)).filter(Boolean)
  if (!command && !url) {
    throw new Error(translate('capabilities.mcpImportMissingTarget', { name: trimmedName }))
  }
  if (command.length > 2048 || url.length > 4096 || transport.length > 64) {
    throw new Error(translate('capabilities.mcpImportEntryTooLong', { name: trimmedName }))
  }
  if (url) {
    let parsedURL: URL
    try {
      parsedURL = new URL(url)
    } catch {
      throw new Error(translate('capabilities.mcpImportInvalidUrl', { name: trimmedName }))
    }
    if (parsedURL.protocol !== 'http:' && parsedURL.protocol !== 'https:') {
      throw new Error(translate('capabilities.mcpImportInvalidUrl', { name: trimmedName }))
    }
    command = ''
    transport ||= 'http'
  } else {
    transport = 'stdio'
  }
  if (args.length > 128 || args.some((arg) => arg.length > 4096)) {
    throw new Error(translate('capabilities.mcpImportArgsInvalid', { name: trimmedName }))
  }
  const envRecord = asRecord(server.env)
  if (Object.keys(envRecord).length > 128) {
    throw new Error(translate('capabilities.mcpImportEnvInvalid', { name: trimmedName }))
  }
  const env: Record<string, string> = {}
  for (const [key, raw] of Object.entries(envRecord)) {
    const envKey = key.trim()
    const envValue = typeof raw === 'string' ? raw : String(raw ?? '')
    if (!envKey || envKey.length > 256 || envValue.length > 16_384) {
      throw new Error(translate('capabilities.mcpImportEnvInvalid', { name: trimmedName }))
    }
    env[envKey] = envValue
  }

  return {
    name: trimmedName,
    enabled: server.enabled !== false && server.disabled !== true,
    command,
    args: command ? args : [],
    url,
    transport,
    env,
  }
}
