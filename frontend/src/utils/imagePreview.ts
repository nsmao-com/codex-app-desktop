import { Call } from '@wailsio/runtime'

import * as backend from '../../bindings/nice_codex_desktop/appservice'

const previewCache = new Map<string, string>()
const previewInflight = new Map<string, Promise<string>>()

function revokeLocalPreview(value = ''): void {
  if (value.startsWith('blob:')) URL.revokeObjectURL(value)
}

export function rememberLocalImagePreview(path: string, dataUrl: string): void {
  if (!path || !dataUrl) return
  const previous = previewCache.get(path)
  if (previous && previous !== dataUrl) revokeLocalPreview(previous)
  previewCache.set(path, dataUrl)
}

export function forgetImagePreview(path: string): void {
  revokeLocalPreview(previewCache.get(path))
  previewCache.delete(path)
  previewInflight.delete(path)
}

export async function resolveImagePreview(path: string): Promise<string> {
  const cached = previewCache.get(path)
  if (cached) return cached
  const pending = previewInflight.get(path)
  if (pending) return pending

  const task = (async () => {
    try {
      const dataUrl = await backend.PreviewImage(path)
      if (dataUrl) previewCache.set(path, dataUrl)
      return dataUrl || ''
    } catch {
      // Fallback for builds where bindings lag: call by method name.
      try {
        const dataUrl = await Call.ByName('nice_codex_desktop.AppService.PreviewImage', path) as string
        if (dataUrl) previewCache.set(path, dataUrl)
        return dataUrl || ''
      } catch {
        return ''
      }
    } finally {
      previewInflight.delete(path)
    }
  })()

  previewInflight.set(path, task)
  return task
}
