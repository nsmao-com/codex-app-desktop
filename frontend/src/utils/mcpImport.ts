import { asArray, asRecord, asString } from './protocol'

export type ImportedMCPServer = {
  name: string
  enabled: boolean
  command: string
  args: string[]
  url: string
  transport: string
  env: Record<string, string>
}

/** Parse Cursor / Claude / Codex style MCP JSON into upsert payloads. */
export function parseMCPImportJSON(raw: string): ImportedMCPServer[] {
  const text = raw.trim()
  if (!text) throw new Error('JSON is empty')

  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error('Invalid JSON')
  }

  const root = asRecord(parsed)
  const serverMap = asRecord(
    root.mcpServers
    ?? root.mcp_servers
    ?? root.servers
    ?? (looksLikeServerMap(root) ? parsed : null),
  )

  const entries: Array<[string, unknown]> = Object.keys(serverMap).length
    ? Object.entries(serverMap)
    : root.name
      ? [[asString(root.name), parsed]]
      : []

  if (!entries.length) {
    throw new Error('No mcpServers / mcp_servers found')
  }

  const result: ImportedMCPServer[] = []
  for (const [name, value] of entries) {
    const server = normalizeImportedServer(name, value)
    if (server) result.push(server)
  }
  if (!result.length) throw new Error('No valid MCP server entries')
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

function normalizeImportedServer(name: string, value: unknown): ImportedMCPServer | null {
  const server = asRecord(value)
  const trimmedName = asString(server.name, name).trim()
  if (!trimmedName) return null

  const command = asString(server.command)
  const url = asString(server.url, asString(server.serverUrl, asString(server.server_url)))
  const transport = asString(server.type, asString(server.transport, url ? 'http' : 'stdio'))
  const args = asArray(server.args).map((arg) => asString(arg)).filter(Boolean)
  const envRecord = asRecord(server.env)
  const env: Record<string, string> = {}
  for (const [key, raw] of Object.entries(envRecord)) {
    const envKey = key.trim()
    if (!envKey) continue
    env[envKey] = typeof raw === 'string' ? raw : String(raw ?? '')
  }

  if (!command && !url) return null
  return {
    name: trimmedName,
    enabled: server.enabled !== false && server.disabled !== true,
    command,
    args,
    url,
    transport,
    env,
  }
}
