import { Call } from '@wailsio/runtime'
import * as backend from '../../bindings/nice_codex_desktop/appservice'

export interface FastCtxStatus {
  installed: boolean
  executable: string
  version: string
  managedVersion: string
  latestVersion: string
  updateAvailable: boolean
  updateError: string
  canInstall: boolean
  codexHome: string
  state: 'not_installed' | 'not_applied' | 'needs_apply' | 'needs_attention' | 'applied'
  shellEnabled: boolean
  message: string
  output: string
}

export interface FastCtxActionResult {
  ok: boolean
  restartRequired: boolean
  message: string
  output: string
  status: FastCtxStatus
}

async function withNameFallback<T>(method: string, invoke: () => Promise<T>, ...args: unknown[]): Promise<T> {
  try {
    return await invoke()
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    if (!/unknown bound method|binding call failed/i.test(message)) throw error
    return Call.ByName(`nice_codex_desktop.AppService.${method}`, ...args)
  }
}

export function checkFastCtx(checkUpdates = true): Promise<FastCtxStatus> {
  return withNameFallback('CheckFastCtx', () => backend.CheckFastCtx(checkUpdates) as Promise<FastCtxStatus>, checkUpdates)
}

export function syncFastCtx(update: boolean, shellEnabled: boolean): Promise<FastCtxActionResult> {
  return withNameFallback('SyncFastCtx', () => backend.SyncFastCtx(update, shellEnabled) as Promise<FastCtxActionResult>, update, shellEnabled)
}
