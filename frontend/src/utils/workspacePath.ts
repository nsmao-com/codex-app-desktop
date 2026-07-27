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
