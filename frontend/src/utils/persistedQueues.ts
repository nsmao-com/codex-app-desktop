import { translate } from '../i18n'
import { notify } from './notify'

const failedStorageKeys = new Set<string>()

export function loadPersistedQueues<T extends {
  id: string
  text: string
  workspace: string
  images: string[]
  createdAt: number
  state: string
  blockedByTurnId?: string
  localAppended?: boolean
}>(key: string): Record<string, T[]> {
  try {
    const parsed = JSON.parse(localStorage.getItem(key) || '{}') as Record<string, T[]>
    return Object.fromEntries(Object.entries(parsed).map(([owner, rows]) => [
      owner,
      Array.isArray(rows)
        ? rows.filter((row) => row && typeof row === 'object'
            && typeof row.id === 'string' && typeof row.text === 'string' && typeof row.workspace === 'string'
            && ['queued', 'sending', 'paused', 'failed'].includes(row.state)).map((row) => ({
            ...row,
            images: Array.isArray(row.images) ? row.images.filter((image) => typeof image === 'string') : [],
            createdAt: Number.isFinite(row.createdAt) ? row.createdAt : Date.now(),
            // A restarted client cannot know whether an in-flight send was accepted.
            // Restored work waits for an explicit resume instead of replaying silently.
            state: row.state === 'failed' || row.state === 'sending' ? 'failed' : 'paused',
            error: row.state === 'sending' ? translate('chat.queueSendingInterrupted') : ('error' in row ? row.error : ''),
            blockedByTurnId: undefined,
            localAppended: row.state === 'sending' ? false : row.localAppended,
          }))
        : [],
    ]).filter(([, rows]) => rows.length))
  } catch {
    return {}
  }
}

export function savePersistedQueues<T>(key: string, queues: Record<string, T[]>): void {
  try {
    localStorage.setItem(key, JSON.stringify(queues))
    failedStorageKeys.delete(key)
  } catch {
    if (!failedStorageKeys.has(key)) {
      failedStorageKeys.add(key)
      notify('error', translate('chat.queueSaveFailed'), translate('chat.queueSaveFailedHint'))
    }
  }
}
