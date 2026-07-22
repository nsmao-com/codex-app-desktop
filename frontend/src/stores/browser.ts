import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import { notify } from '../utils/notify'
import { translate } from '../i18n'

export const useBrowserStore = defineStore('browser', () => {
  const embeddedBrowserUrl = shallowRef('')
  const browserWindowOpen = shallowRef(false)
  const browserHistory = shallowRef<string[]>([])
  const browserHistoryIndex = shallowRef(-1)

  function showBrowser(): void {
    browserWindowOpen.value = true
  }

  async function openBrowser(url: string): Promise<boolean> {
    try {
      const raw = url.trim()
      if (!raw) {
        showBrowser()
        return true
      }
      const host = raw.split('/', 1)[0]?.toLocaleLowerCase() ?? ''
      const local = host.startsWith('localhost') || host.startsWith('127.') || host.startsWith('0.0.0.0') || host.startsWith('[::1]')
      const candidate = /^https?:\/\//i.test(raw) ? raw : `${local ? 'http' : 'https'}://${raw}`
      const parsed = new URL(candidate)
      if (!['http:', 'https:'].includes(parsed.protocol)) throw new Error('Only http and https URLs are supported')
      const nextURL = parsed.toString()
      const currentURL = browserHistory.value[browserHistoryIndex.value]
      if (currentURL !== nextURL) {
        browserHistory.value = [...browserHistory.value.slice(0, browserHistoryIndex.value + 1), nextURL].slice(-32)
        browserHistoryIndex.value = browserHistory.value.length - 1
      }
      embeddedBrowserUrl.value = nextURL
      browserWindowOpen.value = true
      return true
    } catch (error) {
      notify('error', translate('notifications.browserFailed'), errorMessage(error))
      return false
    }
  }

  async function browserBack(): Promise<void> {
    if (browserHistoryIndex.value <= 0) return
    browserHistoryIndex.value -= 1
    embeddedBrowserUrl.value = browserHistory.value[browserHistoryIndex.value] ?? ''
  }

  async function browserForward(): Promise<void> {
    if (browserHistoryIndex.value >= browserHistory.value.length - 1) return
    browserHistoryIndex.value += 1
    embeddedBrowserUrl.value = browserHistory.value[browserHistoryIndex.value] ?? ''
  }

  async function browserReload(): Promise<void> {
    // The launcher owns the iframe refresh key; this method remains for API compatibility.
  }

  async function focusBrowser(): Promise<void> {
    showBrowser()
  }

  function closeBrowser(): void {
    browserWindowOpen.value = false
  }

  return {
    embeddedBrowserUrl,
    browserWindowOpen,
    browserHistory,
    browserHistoryIndex,
    showBrowser,
    openBrowser,
    browserBack,
    browserForward,
    browserReload,
    focusBrowser,
    closeBrowser,
  }
})

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : translate('notifications.unexpected')
}
