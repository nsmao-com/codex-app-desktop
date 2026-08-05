function usesCaseInsensitivePaths(): boolean {
  if (typeof navigator !== 'undefined') return /windows/i.test(navigator.userAgent)
  return false
}

export function workspaceKey(path: string): string {
  const normalized = path.trim().replace(/\\/g, '/').replace(/\/+$/, '')
  return usesCaseInsensitivePaths() ? normalized.toLocaleLowerCase('en-US') : normalized
}

export function sameWorkspacePath(left: string, right: string): boolean {
  return workspaceKey(left) === workspaceKey(right)
}

function normalizedDisplayPath(path: string): string {
  let normalized = path.trim().replace(/\\/g, '/')
  if (/^\/\/\?\/[a-zA-Z]:\//.test(normalized)) normalized = normalized.slice(4)
  return normalized
}

function isAbsoluteDisplayPath(path: string): boolean {
  return /^([a-zA-Z]:\/|\/|\/\/)/.test(path)
}

/** Resolve a provider-relative file path against the active workspace for UI display. */
export function fullDisplayPath(path: string, workspace = ''): string {
  const normalized = normalizedDisplayPath(path)
  if (!normalized || isAbsoluteDisplayPath(normalized)) return normalized

  const root = normalizedDisplayPath(workspace).replace(/\/+$/, '')
  if (!root || !isAbsoluteDisplayPath(root)) return normalized
  return `${root}/${normalized.replace(/^\.\//, '')}`
}

/** Keep the volume/root visible while compacting long file paths for narrow rows. */
export function compactDisplayPath(path: string, workspace = ''): string {
  const normalized = fullDisplayPath(path, workspace)
  if (!normalized) return ''

  const drive = normalized.match(/^([a-zA-Z]:)\/(.*)$/)
  if (drive) {
    const parts = (drive[2] || '').split('/').filter(Boolean)
    if (parts.length <= 2) return normalized
    return `${drive[1]}/…/${parts.slice(-2).join('/')}`
  }

  if (normalized.startsWith('//')) {
    const parts = normalized.slice(2).split('/').filter(Boolean)
    if (parts.length <= 4) return normalized
    return `//${parts[0]}/${parts[1]}/…/${parts.slice(-2).join('/')}`
  }

  const absolute = normalized.startsWith('/')
  const parts = normalized.split('/').filter(Boolean)
  if (parts.length <= 2) return normalized
  return `${absolute ? '/' : ''}…/${parts.slice(-2).join('/')}`
}
