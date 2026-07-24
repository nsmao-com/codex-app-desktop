/**
 * Grok backend calls with ByName fallback.
 * Generated bindings use numeric ByID hashes that go stale when the Go binary
 * and frontend bindings drift — Call.ByName survives that skew.
 */
import { Call } from '@wailsio/runtime'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import type {
  GrokRuntimeStatus,
  GrokSendRequest,
  GrokSessionDetail,
  GrokSessionSummary,
  GrokTurnRef,
  WorkspaceInfo,
} from '../../bindings/nice_codex_desktop/models'

const SERVICE = 'nice_codex_desktop.AppService'

function isBindingError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error || '')
  return /unknown bound method|binding call failed/i.test(message)
}

async function withNameFallback<T>(
  method: string,
  byId: () => Promise<T>,
  ...args: unknown[]
): Promise<T> {
  try {
    return await byId()
  } catch (error) {
    if (!isBindingError(error)) throw error
    return await Call.ByName(`${SERVICE}.${method}`, ...args) as T
  }
}

export function refreshGrokRuntime(): Promise<GrokRuntimeStatus> {
  return withNameFallback('RefreshGrokRuntime', () => backend.RefreshGrokRuntime())
}

export function listGrokSessions(
  backendId: string,
  workspace: string,
  search: string,
): Promise<GrokSessionSummary[] | null> {
  return withNameFallback(
    'ListGrokSessions',
    () => backend.ListGrokSessions(backendId, workspace, search),
    backendId,
    workspace,
    search,
  )
}

export function readGrokSession(
  backendId: string,
  sessionID: string,
): Promise<GrokSessionDetail> {
  return withNameFallback(
    'ReadGrokSession',
    () => backend.ReadGrokSession(backendId, sessionID),
    backendId,
    sessionID,
  )
}

export function sendGrokMessage(request: GrokSendRequest): Promise<GrokTurnRef> {
  return withNameFallback(
    'SendGrokMessage',
    () => backend.SendGrokMessage(request),
    request,
  )
}

export function interruptGrokTurn(ref: GrokTurnRef): Promise<void> {
  return withNameFallback(
    'InterruptGrokTurn',
    () => backend.InterruptGrokTurn(ref),
    ref,
  )
}

export function deleteGrokSession(backendId: string, sessionID: string): Promise<void> {
  return withNameFallback(
    'DeleteGrokSession',
    () => backend.DeleteGrokSession(backendId, sessionID),
    backendId,
    sessionID,
  )
}

export function renameGrokSession(
  backendId: string,
  sessionID: string,
  name: string,
): Promise<GrokSessionSummary> {
  return withNameFallback(
    'RenameGrokSession',
    () => (backend as any).RenameGrokSession?.(backendId, sessionID, name)
      ?? Call.ByName(`${SERVICE}.RenameGrokSession`, backendId, sessionID, name),
    backendId,
    sessionID,
    name,
  )
}

export function archiveGrokSession(backendId: string, sessionID: string): Promise<void> {
  return withNameFallback(
    'ArchiveGrokSession',
    () => (backend as any).ArchiveGrokSession?.(backendId, sessionID)
      ?? Call.ByName(`${SERVICE}.ArchiveGrokSession`, backendId, sessionID),
    backendId,
    sessionID,
  )
}

export function unarchiveGrokSession(
  backendId: string,
  sessionID: string,
): Promise<GrokSessionSummary> {
  return withNameFallback(
    'UnarchiveGrokSession',
    () => (backend as any).UnarchiveGrokSession?.(backendId, sessionID)
      ?? Call.ByName(`${SERVICE}.UnarchiveGrokSession`, backendId, sessionID),
    backendId,
    sessionID,
  )
}

export function listArchivedGrokSessions(
  backendId: string,
  search: string,
): Promise<GrokSessionSummary[] | null> {
  return withNameFallback(
    'ListArchivedGrokSessions',
    () => (backend as any).ListArchivedGrokSessions?.(backendId, search)
      ?? Call.ByName(`${SERVICE}.ListArchivedGrokSessions`, backendId, search),
    backendId,
    search,
  )
}

export interface GrokTurnUsageView {
  index: number
  turnId: string
  sessionId: string
  tokenUsage: {
    inputTokens: number
    cachedInputTokens: number
    outputTokens: number
    reasoningOutputTokens: number
    totalTokens: number
  }
  at?: number
}

export function listGrokSessionTurnUsages(sessionID: string): Promise<GrokTurnUsageView[] | null> {
  return withNameFallback(
    'ListGrokSessionTurnUsages',
    () => (backend as any).ListGrokSessionTurnUsages?.(sessionID)
      ?? Call.ByName(`${SERVICE}.ListGrokSessionTurnUsages`, sessionID),
    sessionID,
  )
}

export function selectGrokWorkspace(): Promise<WorkspaceInfo> {
  return withNameFallback('SelectGrokWorkspace', () => backend.SelectGrokWorkspace())
}

export function useGrokWorkspace(path: string): Promise<WorkspaceInfo> {
  return withNameFallback('UseGrokWorkspace', () => backend.UseGrokWorkspace(path), path)
}

export interface GrokMCPServerView {
  name: string
  enabled: boolean
  command: string
  args: string
  transport: string
  url: string
}

export interface GrokSkillView {
  name: string
  displayName: string
  description: string
  path: string
  scope: string
}

export interface GrokPluginView {
  name: string
  path: string
}

export interface GrokCapabilitiesCatalog {
  runtime: GrokRuntimeStatus
  configPath: string
  grokHome: string
  mcp: GrokMCPServerView[] | null
  skills: GrokSkillView[] | null
  plugins: GrokPluginView[] | null
  globalInstructions: {
    content: string
    path: string
    source: string
    exists: boolean
    emptyFile: boolean
    available: boolean
  }
  projectInstructions: {
    content: string
    workspace: string
    workspaceName: string
    path: string
    source: string
    exists: boolean
    emptyFile: boolean
    available: boolean
  }
}

export function readGrokCapabilities(): Promise<GrokCapabilitiesCatalog> {
  // Prefer generated ByID bindings; ByName only when IDs are stale.
  return withNameFallback(
    'ReadGrokCapabilities',
    () => backend.ReadGrokCapabilities() as Promise<GrokCapabilitiesCatalog>,
  )
}

export function readGrokGlobalInstructions() {
  return withNameFallback(
    'ReadGrokGlobalInstructions',
    () => backend.ReadGrokGlobalInstructions() as Promise<GrokCapabilitiesCatalog['globalInstructions']>,
  )
}

export function saveGrokGlobalInstructions(content: string) {
  return withNameFallback(
    'SaveGrokGlobalInstructions',
    () => backend.SaveGrokGlobalInstructions(content) as Promise<GrokCapabilitiesCatalog['globalInstructions']>,
    content,
  )
}

export function readGrokProjectInstructions() {
  return withNameFallback(
    'ReadGrokProjectInstructions',
    () => backend.ReadGrokProjectInstructions() as Promise<GrokCapabilitiesCatalog['projectInstructions']>,
  )
}

export function saveGrokProjectInstructions(content: string) {
  return withNameFallback(
    'SaveGrokProjectInstructions',
    () => backend.SaveGrokProjectInstructions(content) as Promise<GrokCapabilitiesCatalog['projectInstructions']>,
    content,
  )
}

export function openGrokConfigFile(): Promise<void> {
  return withNameFallback('OpenGrokConfigFile', () => backend.OpenGrokConfigFile())
}

export function openGrokHome(): Promise<void> {
  return withNameFallback('OpenGrokHome', () => backend.OpenGrokHome())
}
