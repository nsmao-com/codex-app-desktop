export function loadPersistedQueues<T extends { state: string; blockedByTurnId?: string; localAppended?: boolean }>(key: string): Record<string, T[]> {
  try {
    const parsed = JSON.parse(localStorage.getItem(key) || '{}') as Record<string, T[]>
    return Object.fromEntries(Object.entries(parsed).map(([owner, rows]) => [
      owner,
      Array.isArray(rows)
        ? rows.filter((row) => row && typeof row === 'object').map((row) => ({
            ...row,
            state: row.state === 'sending' ? 'queued' : row.state,
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
  } catch {
    // In-memory queue remains authoritative when storage is unavailable.
  }
}
