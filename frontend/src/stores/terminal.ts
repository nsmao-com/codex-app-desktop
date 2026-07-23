import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import * as backend from '../../bindings/nice_codex_desktop/appservice'
import { notify } from '../utils/notify'
import { translate } from '../i18n'

export const useTerminalStore = defineStore('terminal', () => {
  const terminalPanelOpen = shallowRef(false)
  const terminalStarting = shallowRef(false)
  const terminalRunning = shallowRef(false)
  const terminalProcessId = shallowRef('')
  const terminalOutput = shallowRef('')

  let terminalDecoder = new TextDecoder()
  let terminalGeneration = 0
  let writeToTerminal: ((chunk: string) => void) | null = null

  function bindTerminalWriter(writer: ((chunk: string) => void) | null): void {
    writeToTerminal = writer
    if (writer && terminalOutput.value) {
      writer(terminalOutput.value)
    }
  }

  async function openTerminal(): Promise<boolean> {
    terminalPanelOpen.value = true
    if (terminalRunning.value || terminalStarting.value) return true
    const generation = ++terminalGeneration
    const processID = `terminal-${crypto.randomUUID()}`
    terminalProcessId.value = processID
    terminalOutput.value = ''
    terminalDecoder = new TextDecoder()
    terminalStarting.value = true
    try {
      await backend.StartTerminalSession(processID)
      if (generation !== terminalGeneration || !terminalPanelOpen.value) {
        await backend.StopTerminalSession(processID).catch(() => undefined)
        return false
      }
      terminalRunning.value = true
      return true
    } catch (error) {
      if (generation === terminalGeneration) {
        terminalRunning.value = false
        notify('error', translate('notifications.terminalFailed'), errorMessage(error))
      }
      return false
    } finally {
      if (generation === terminalGeneration) terminalStarting.value = false
    }
  }

  async function writeTerminal(input: string): Promise<void> {
    if (!terminalRunning.value || !terminalProcessId.value || !input) return
    try {
      await backend.WriteTerminal(terminalProcessId.value, input)
    } catch (error) {
      notify('error', translate('notifications.terminalFailed'), errorMessage(error))
    }
  }

  async function resizeTerminal(rows: number, cols: number): Promise<void> {
    if (!terminalRunning.value || !terminalProcessId.value) return
    try {
      await backend.ResizeTerminal(terminalProcessId.value, rows, cols)
    } catch {
      // Resize can race with exit; ignore.
    }
  }

  async function closeTerminal(): Promise<void> {
    // Hide the panel immediately; teardown must never block or take down the app.
    terminalPanelOpen.value = false
    terminalGeneration += 1
    terminalStarting.value = false
    terminalRunning.value = false
    const processID = terminalProcessId.value
    terminalProcessId.value = ''
    if (!processID) return
    void backend.StopTerminalSession(processID).catch(() => undefined)
  }

  function clearTerminal(): void {
    terminalOutput.value = ''
  }

  function handleOutputDelta(processId: string, deltaBase64: string): void {
    if (processId !== terminalProcessId.value) return
    if (!deltaBase64) return
    const binary = atob(deltaBase64)
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
    const chunk = terminalDecoder.decode(bytes, { stream: true })
    terminalOutput.value = `${terminalOutput.value}${chunk}`.slice(-500_000)
    writeToTerminal?.(chunk)
  }

  function handleExit(processId: string, error?: string): void {
    if (processId !== terminalProcessId.value) return
    terminalStarting.value = false
    terminalRunning.value = false
    if (error) {
      const message = `\r\n${error}\r\n`
      terminalOutput.value = `${terminalOutput.value}${message}`
      writeToTerminal?.(message)
    }
  }

  function openExternalTerminal(): void {
    backend.OpenTerminal().catch((error: unknown) => {
      notify('error', translate('notifications.terminalFailed'), errorMessage(error))
    })
  }

  return {
    terminalPanelOpen,
    terminalStarting,
    terminalRunning,
    terminalProcessId,
    terminalOutput,
    bindTerminalWriter,
    openTerminal,
    writeTerminal,
    resizeTerminal,
    closeTerminal,
    clearTerminal,
    handleOutputDelta,
    handleExit,
    openExternalTerminal,
  }
})

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : translate('notifications.unexpected')
}
