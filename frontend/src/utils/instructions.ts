import { translate } from '../i18n'
import { friendlyErrorMessage, rawErrorText } from './errorMessage'

export const maxInstructionBytes = 1024 * 1024
const encoder = new TextEncoder()

export function instructionBytes(content: string): number {
  return encoder.encode(content).byteLength
}

export function instructionError(error: unknown): string {
  const raw = rawErrorText(error)
  if (raw.includes('INSTRUCTIONS_CHANGED')) return translate('settings.instructionsConflict')
  if (raw.includes('INSTRUCTIONS_TOO_LARGE')) return translate('settings.instructionsTooLarge')
  if (raw.includes('INSTRUCTIONS_ENCODING')) return translate('settings.instructionsEncoding')
  return friendlyErrorMessage(error)
}
